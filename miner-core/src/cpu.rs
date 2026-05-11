use crate::keccak::{is_below_target_be, keccak256_64, NonceBuilder, Target};
use crossbeam_channel::{bounded, select, Receiver, Sender};
use rayon::prelude::*;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Instant;

pub struct CpuConfig {
    pub threads: usize,
    pub batch_per_thread: u64,
}

pub struct CpuRunResult {
    pub found: Option<([u8; 32], [u8; 32])>,
    pub hashes: u64,
    pub elapsed_ms: u128,
}

pub enum ProgressUpdate {
    Progress { hashes: u64, hashrate: f64, elapsed_ms: u128 },
    Stopped { hashes: u64, elapsed_ms: u128 },
}

pub fn run_cpu(
    challenge: [u8; 32],
    target: Target,
    prefix: [u8; 24],
    cfg: CpuConfig,
    stop_flag: Arc<AtomicBool>,
    progress_tx: Sender<ProgressUpdate>,
) -> CpuRunResult {
    let builder = NonceBuilder::new(prefix);
    let found = Arc::new(AtomicBool::new(false));
    let total_hashes = Arc::new(AtomicU64::new(0));
    let result_slot: Arc<std::sync::Mutex<Option<([u8; 32], [u8; 32])>>> =
        Arc::new(std::sync::Mutex::new(None));

    let n = cfg.threads.max(1);
    let batch = cfg.batch_per_thread.max(1 << 12);
    let started = Instant::now();

    let reporter_stop = Arc::new(AtomicBool::new(false));
    let reporter_handle = {
        let stop_flag = stop_flag.clone();
        let reporter_stop = reporter_stop.clone();
        let total_hashes = total_hashes.clone();
        let progress_tx = progress_tx.clone();
        let started = started;
        thread::spawn(move || {
            let mut last_hashes: u64 = 0;
            let mut last_ts = Instant::now();
            loop {
                thread::sleep(std::time::Duration::from_millis(500));
                if reporter_stop.load(Ordering::Relaxed) || stop_flag.load(Ordering::Relaxed) {
                    break;
                }
                let now = Instant::now();
                let h = total_hashes.load(Ordering::Relaxed);
                let dh = h.saturating_sub(last_hashes) as f64;
                let dt = now.duration_since(last_ts).as_secs_f64().max(1e-6);
                let _ = progress_tx.send(ProgressUpdate::Progress {
                    hashes: h,
                    hashrate: dh / dt,
                    elapsed_ms: now.duration_since(started).as_millis(),
                });
                last_hashes = h;
                last_ts = now;
            }
        })
    };

    (0..n).into_par_iter().for_each_with(
        (found.clone(), total_hashes.clone(), result_slot.clone(), stop_flag.clone()),
        |(found, total_hashes, result_slot, stop_flag), thread_idx| {
            let mut counter: u64 = (thread_idx as u64).wrapping_mul(0x1_0000_0000_0000);
            loop {
                if found.load(Ordering::Relaxed) || stop_flag.load(Ordering::Relaxed) {
                    return;
                }
                let end = counter.wrapping_add(batch);
                let mut local_hashes: u64 = 0;
                while counter < end {
                    let nonce = builder.build(counter);
                    let r = keccak256_64(&challenge, &nonce);
                    local_hashes += 1;
                    if is_below_target_be(&r, &target.be) {
                        if !found.swap(true, Ordering::SeqCst) {
                            *result_slot.lock().unwrap() = Some((nonce, r));
                        }
                        total_hashes.fetch_add(local_hashes, Ordering::Relaxed);
                        return;
                    }
                    counter = counter.wrapping_add(1);
                }
                total_hashes.fetch_add(local_hashes, Ordering::Relaxed);
            }
        },
    );

    reporter_stop.store(true, Ordering::Relaxed);
    let _ = reporter_handle.join();

    let elapsed_ms = started.elapsed().as_millis();
    let hashes = total_hashes.load(Ordering::Relaxed);

    let _ = progress_tx.send(ProgressUpdate::Stopped { hashes, elapsed_ms });

    let found = result_slot.lock().unwrap().take();
    CpuRunResult { found, hashes, elapsed_ms }
}

pub fn spawn_listener() -> (Sender<ProgressUpdate>, Receiver<ProgressUpdate>) {
    bounded(64)
}

pub fn drain_with_stop(
    rx: &Receiver<ProgressUpdate>,
    stop: &Receiver<()>,
    mut on_update: impl FnMut(ProgressUpdate),
) {
    loop {
        select! {
            recv(rx) -> msg => match msg {
                Ok(u) => on_update(u),
                Err(_) => break,
            },
            recv(stop) -> _ => break,
        }
    }
}
