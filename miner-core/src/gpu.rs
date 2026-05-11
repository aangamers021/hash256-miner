use crate::protocol::GpuDevice;

#[cfg(feature = "gpu")]
use crate::keccak::{is_below_target_be, keccak256_64, NonceBuilder};
use crate::keccak::Target;

#[cfg(feature = "gpu")]
pub fn list_devices() -> Vec<GpuDevice> {
    let mut out = Vec::new();
    let platforms = ocl::Platform::list();
    let mut idx = 0usize;
    for p in &platforms {
        let pname = p.name().unwrap_or_default();
        let Ok(devs) = ocl::Device::list_all(p) else { continue };
        for d in devs {
            out.push(GpuDevice {
                index: idx,
                platform: pname.clone(),
                name: d.name().unwrap_or_default(),
                compute_units: d.info(ocl::core::DeviceInfo::MaxComputeUnits)
                    .map(|v| format!("{v}").parse::<u32>().unwrap_or(0))
                    .unwrap_or(0),
                max_work_group_size: d
                    .info(ocl::core::DeviceInfo::MaxWorkGroupSize)
                    .map(|v| format!("{v}").parse::<usize>().unwrap_or(0))
                    .unwrap_or(0),
            });
            idx += 1;
        }
    }
    out
}

#[cfg(not(feature = "gpu"))]
pub fn list_devices() -> Vec<GpuDevice> {
    Vec::new()
}

pub struct GpuConfig {
    pub device_index: usize,
    pub batch: u64,
}

pub struct GpuRunResult {
    pub found: Option<([u8; 32], [u8; 32])>,
    pub hashes: u64,
    pub elapsed_ms: u128,
}

#[cfg(not(feature = "gpu"))]
pub fn run_gpu(
    _challenge: [u8; 32],
    _target: Target,
    _prefix: [u8; 24],
    _cfg: GpuConfig,
    _stop_flag: std::sync::Arc<std::sync::atomic::AtomicBool>,
    _progress_tx: crossbeam_channel::Sender<crate::cpu::ProgressUpdate>,
) -> anyhow::Result<GpuRunResult> {
    anyhow::bail!("GPU support not compiled in; rebuild with `--features gpu`")
}

#[cfg(feature = "gpu")]
pub fn run_gpu(
    challenge: [u8; 32],
    target: Target,
    prefix: [u8; 24],
    cfg: GpuConfig,
    stop_flag: std::sync::Arc<std::sync::atomic::AtomicBool>,
    progress_tx: crossbeam_channel::Sender<crate::cpu::ProgressUpdate>,
) -> anyhow::Result<GpuRunResult> {
    use anyhow::{bail, Context};
    use ocl::{flags, Buffer, Context as OclContext, Device, Platform, Program, Queue};
    use std::sync::atomic::Ordering;
    use std::time::Instant;

    let platforms = Platform::list();
    let mut flat: Vec<(Platform, Device)> = Vec::new();
    for p in platforms {
        let devs = Device::list_all(&p).unwrap_or_default();
        for d in devs {
            flat.push((p, d));
        }
    }
    if flat.is_empty() {
        bail!("no OpenCL device found");
    }
    let (platform, device) = flat
        .get(cfg.device_index)
        .cloned()
        .with_context(|| format!("gpu_device index {} out of range (have {})", cfg.device_index, flat.len()))?;

    let context = OclContext::builder()
        .platform(platform)
        .devices(device)
        .build()
        .context("build OpenCL context")?;
    let queue = Queue::new(&context, device, None).context("build OpenCL queue")?;

    let src = include_str!("../kernels/keccak256.cl");
    let program = Program::builder()
        .devices(device)
        .src(src)
        .build(&context)
        .context("compile kernel")?;

    let nonce_builder = NonceBuilder::new(prefix);

    let mut challenge_le = [0u64; 4];
    for (i, chunk) in challenge.chunks(8).enumerate() {
        challenge_le[i] = u64::from_le_bytes(chunk.try_into().unwrap());
    }
    let mut nonce_hi_le = [0u64; 3];
    for (i, chunk) in prefix.chunks(8).enumerate() {
        nonce_hi_le[i] = u64::from_le_bytes(chunk.try_into().unwrap());
    }
    let mut target_be = [0u64; 4];
    for (i, chunk) in target.be.chunks(8).enumerate() {
        target_be[i] = u64::from_be_bytes(chunk.try_into().unwrap());
    }

    let buf_challenge = Buffer::<u64>::builder()
        .queue(queue.clone())
        .flags(flags::MEM_READ_ONLY | flags::MEM_COPY_HOST_PTR)
        .len(4)
        .copy_host_slice(&challenge_le)
        .build()?;
    let buf_prefix = Buffer::<u64>::builder()
        .queue(queue.clone())
        .flags(flags::MEM_READ_ONLY | flags::MEM_COPY_HOST_PTR)
        .len(3)
        .copy_host_slice(&nonce_hi_le)
        .build()?;
    let buf_target = Buffer::<u64>::builder()
        .queue(queue.clone())
        .flags(flags::MEM_READ_ONLY | flags::MEM_COPY_HOST_PTR)
        .len(4)
        .copy_host_slice(&target_be)
        .build()?;
    let buf_found_flag = Buffer::<u32>::builder()
        .queue(queue.clone())
        .flags(flags::MEM_READ_WRITE)
        .len(1)
        .build()?;
    let buf_nonce_low = Buffer::<u64>::builder()
        .queue(queue.clone())
        .flags(flags::MEM_READ_WRITE)
        .len(1)
        .build()?;
    let buf_result_be = Buffer::<u64>::builder()
        .queue(queue.clone())
        .flags(flags::MEM_READ_WRITE)
        .len(4)
        .build()?;

    let local = device
        .info(ocl::core::DeviceInfo::MaxWorkGroupSize)
        .map(|v| format!("{v}").parse::<usize>().unwrap_or(64))
        .unwrap_or(64)
        .min(256)
        .max(32);
    let global = (cfg.batch as usize).max(local);
    let global = ((global + local - 1) / local) * local;

    let started = Instant::now();
    let mut counter: u64 = 0;
    let mut total_hashes: u64 = 0;
    let mut last_report = Instant::now();
    let mut last_hashes: u64 = 0;

    loop {
        if stop_flag.load(Ordering::Relaxed) {
            break;
        }

        let zero_flag: [u32; 1] = [0];
        buf_found_flag.write(&zero_flag[..]).enq()?;

        let kernel = ocl::Kernel::builder()
            .program(&program)
            .name("grind")
            .queue(queue.clone())
            .global_work_size(global)
            .local_work_size(local)
            .arg(&buf_challenge)
            .arg(&buf_prefix)
            .arg(&buf_target)
            .arg(counter)
            .arg(&buf_found_flag)
            .arg(&buf_nonce_low)
            .arg(&buf_result_be)
            .build()?;

        unsafe { kernel.enq()?; }
        queue.finish()?;

        total_hashes = total_hashes.saturating_add(global as u64);

        let mut flag = [0u32; 1];
        buf_found_flag.read(&mut flag[..]).enq()?;
        if flag[0] != 0 {
            let mut nonce_low = [0u64; 1];
            let mut result_be = [0u64; 4];
            buf_nonce_low.read(&mut nonce_low[..]).enq()?;
            buf_result_be.read(&mut result_be[..]).enq()?;

            let candidate_lo = nonce_low[0];
            let nonce = nonce_builder.build(candidate_lo);
            let verify = keccak256_64(&challenge, &nonce);
            if !is_below_target_be(&verify, &target.be) {
                anyhow::bail!("gpu returned false positive (verify mismatch)");
            }
            let elapsed_ms = started.elapsed().as_millis();
            return Ok(GpuRunResult {
                found: Some((nonce, verify)),
                hashes: total_hashes,
                elapsed_ms,
            });
        }

        counter = counter.wrapping_add(global as u64);

        if last_report.elapsed() >= std::time::Duration::from_millis(500) {
            let now = Instant::now();
            let dh = total_hashes.saturating_sub(last_hashes) as f64;
            let dt = now.duration_since(last_report).as_secs_f64().max(1e-6);
            let _ = progress_tx.send(crate::cpu::ProgressUpdate::Progress {
                hashes: total_hashes,
                hashrate: dh / dt,
                elapsed_ms: started.elapsed().as_millis(),
            });
            last_hashes = total_hashes;
            last_report = now;
        }
    }

    let elapsed_ms = started.elapsed().as_millis();
    let _ = progress_tx.send(crate::cpu::ProgressUpdate::Stopped {
        hashes: total_hashes,
        elapsed_ms,
    });
    Ok(GpuRunResult {
        found: None,
        hashes: total_hashes,
        elapsed_ms,
    })
}
