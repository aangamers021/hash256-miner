use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Deserialize)]
#[serde(tag = "cmd", rename_all = "lowercase")]
pub enum Command {
    Start(StartArgs),
    Retarget(RetargetArgs),
    Stop,
    Probe,
}

#[derive(Debug, Clone, Deserialize)]
pub struct StartArgs {
    pub challenge: String,
    pub difficulty: String,
    pub prefix: String,
    #[serde(default = "default_batch")]
    pub batch: u64,
    #[serde(default)]
    pub mode: Mode,
    #[serde(default)]
    pub gpu_device: Option<usize>,
    #[serde(default)]
    pub cpu_threads: Option<usize>,
}

fn default_batch() -> u64 {
    1 << 20
}

#[derive(Debug, Clone, Deserialize)]
pub struct RetargetArgs {
    pub challenge: String,
    pub difficulty: String,
}

#[derive(Debug, Default, Clone, Copy, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Mode {
    #[default]
    Cpu,
    Gpu,
}

#[derive(Debug, Clone, Serialize)]
#[serde(tag = "event", rename_all = "lowercase")]
pub enum Event {
    Ready {
        version: String,
        cpu_threads: usize,
        gpu_devices: Vec<GpuDevice>,
    },
    Progress {
        hashes: u64,
        hashrate: f64,
        elapsed_ms: u128,
    },
    Found {
        nonce: String,
        result: String,
        hashes: u64,
        elapsed_ms: u128,
    },
    Stopped {
        hashes: u64,
        elapsed_ms: u128,
    },
    Error {
        message: String,
    },
    Devices {
        gpu: Vec<GpuDevice>,
    },
}

#[derive(Debug, Clone, Serialize)]
pub struct GpuDevice {
    pub index: usize,
    pub platform: String,
    pub name: String,
    pub compute_units: u32,
    pub max_work_group_size: usize,
}
