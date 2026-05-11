mod cpu;
mod gpu;
mod keccak;
mod protocol;

use anyhow::{Context, Result};
use crossbeam_channel::{bounded, unbounded, Sender};
use keccak::{parse_hex32, parse_hex_prefix24, to_hex32, Target};
use protocol::{Command, Event, Mode};
use std::io::{BufRead, Write};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Instant;

const VERSION: &str = env!("CARGO_PKG_VERSION");

fn main() -> Result<()> {
    let stdin = std::io::stdin();
    let (event_tx, event_rx) = unbounded::<Event>();

    let emitter = thread::spawn(move || {
        let stdout = std::io::stdout();
        let mut out = stdout.lock();
        while let Ok(ev) = event_rx.recv() {
            if let Ok(line) = serde_json::to_string(&ev) {
                let _ = writeln!(out, "{line}");
                let _ = out.flush();
            }
        }
    });

    let gpu_devices = gpu::list_devices();
    let gpu_available = !gpu_devices.is_empty();

    let _ = event_tx.send(Event::Ready {
        version: VERSION.to_string(),
        cpu_threads: num_cpus_available(),
        gpu_devices: gpu_devices.clone(),
    });

    let mut active: Option<ActiveRun> = None;

    for line in stdin.lock().lines() {
        let line = line.context("read stdin")?;
        let line = line.trim();
        if line.is_empty() {
            continue;
        }

        let cmd: Command = match serde_json::from_str(line) {
            Ok(c) => c,
            Err(e) => {
                let _ = event_tx.send(Event::Error {
                    message: format!("bad command: {e}"),
                });
                continue;
            }
        };

        match cmd {
            Command::Start(args) => {
                if let Some(a) = active.take() {
                    a.stop_and_wait();
                }
                match start_run(args, gpu_available, event_tx.clone()) {
                    Ok(run) => active = Some(run),
                    Err(e) => {
                        let _ = event_tx.send(Event::Error { message: e.to_string() });
                    }
                }
            }
            Command::Retarget(args) => {
                if let Some(a) = active.take() {
                    a.stop_and_wait();
                }
                let start = protocol::StartArgs {
                    challenge: args.challenge,
                    difficulty: args.difficulty,
                    prefix: random_prefix24(),
                    batch: 1 << 20,
                    mode: Mode::Cpu,
                    gpu_device: None,
                    cpu_threads: None,
                };
                match start_run(start, gpu_available, event_tx.clone()) {
                    Ok(run) => active = Some(run),
                    Err(e) => {
                        let _ = event_tx.send(Event::Error { message: e.to_string() });
                    }
                }
            }
            Command::Stop => {
                if let Some(a) = active.take() {
                    a.stop_and_wait();
                }
            }
            Command::Probe => {
                let _ = event_tx.send(Event::Devices { gpu: gpu_devices.clone() });
            }
        }
    }

    if let Some(a) = active.take() {
        a.stop_and_wait();
    }

    drop(event_tx);
    let _ = emitter.join();
    Ok(())
}

fn num_cpus_available() -> usize {
    thread::available_parallelism().map(|n| n.get()).unwrap_or(1)
}

fn random_prefix24() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let seed = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0);
    let mut state = seed ^ 0x9E37_79B9_7F4A_7C15;
    let mut bytes = [0u8; 24];
    for chunk in bytes.chunks_mut(8) {
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        let le = state.to_le_bytes();
        let n = chunk.len();
        chunk.copy_from_slice(&le[..n]);
    }
    let mut s = String::with_capacity(50);
    s.push_str("0x");
    s.push_str(&hex::encode(bytes));
    s
}

struct ActiveRun {
    stop: Arc<AtomicBool>,
    handle: Option<thread::JoinHandle<()>>,
}

impl ActiveRun {
    fn stop_and_wait(mut self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(h) = self.handle.take() {
            let _ = h.join();
        }
    }
}

fn start_run(
    args: protocol::StartArgs,
    gpu_available: bool,
    event_tx: Sender<Event>,
) -> Result<ActiveRun> {
    let challenge = parse_hex32(&args.challenge)?;
    let difficulty = parse_hex32(&args.difficulty)?;
    let prefix = parse_hex_prefix24(&args.prefix)?;
    let target = Target::from_be(difficulty);

    let stop = Arc::new(AtomicBool::new(false));

    let mode = args.mode;
    let gpu_device = args.gpu_device;
    let cpu_threads = args.cpu_threads.unwrap_or_else(num_cpus_available);
    let batch = args.batch.max(1 << 14);

    let stop_clone = stop.clone();

    let started = Instant::now();

    let handle = thread::spawn(move || {
        let (prog_tx, prog_rx) = bounded::<cpu::ProgressUpdate>(64);

        let forwarder_stop = Arc::new(AtomicBool::new(false));
        let forwarder_stop_clone = forwarder_stop.clone();
        let forwarder_tx = event_tx.clone();
        let fwd = thread::spawn(move || {
            loop {
                match prog_rx.recv_timeout(std::time::Duration::from_millis(200)) {
                    Ok(cpu::ProgressUpdate::Progress { hashes, hashrate, elapsed_ms }) => {
                        let _ = forwarder_tx.send(Event::Progress { hashes, hashrate, elapsed_ms });
                    }
                    Ok(cpu::ProgressUpdate::Stopped { .. }) => {
                    }
                    Err(crossbeam_channel::RecvTimeoutError::Timeout) => {
                        if forwarder_stop_clone.load(Ordering::Relaxed) {
                            break;
                        }
                    }
                    Err(crossbeam_channel::RecvTimeoutError::Disconnected) => break,
                }
            }
        });

        let result = match mode {
            Mode::Gpu => {
                if !gpu_available {
                    let _ = event_tx.send(Event::Error {
                        message: "GPU mode requested but no OpenCL device available".to_string(),
                    });
                    None
                } else {
                    let cfg = gpu::GpuConfig {
                        device_index: gpu_device.unwrap_or(0),
                        batch,
                    };
                    match gpu::run_gpu(challenge, target, prefix, cfg, stop_clone.clone(), prog_tx.clone()) {
                        Ok(r) => Some((r.found, r.hashes, r.elapsed_ms)),
                        Err(e) => {
                            let _ = event_tx.send(Event::Error { message: e.to_string() });
                            None
                        }
                    }
                }
            }
            Mode::Cpu => {
                let cfg = cpu::CpuConfig {
                    threads: cpu_threads,
                    batch_per_thread: batch,
                };
                let r = cpu::run_cpu(challenge, target, prefix, cfg, stop_clone.clone(), prog_tx.clone());
                Some((r.found, r.hashes, r.elapsed_ms))
            }
        };

        drop(prog_tx);
        forwarder_stop.store(true, Ordering::Relaxed);
        let _ = fwd.join();

        let elapsed_ms = started.elapsed().as_millis();
        match result {
            Some((Some((nonce, proof)), hashes, _)) => {
                let _ = event_tx.send(Event::Found {
                    nonce: to_hex32(&nonce),
                    result: to_hex32(&proof),
                    hashes,
                    elapsed_ms,
                });
            }
            Some((None, hashes, _)) => {
                let _ = event_tx.send(Event::Stopped { hashes, elapsed_ms });
            }
            None => {}
        }
    });

    Ok(ActiveRun {
        stop,
        handle: Some(handle),
    })
}
