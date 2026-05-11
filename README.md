# hash256-miner

CLI auto-miner for [**HASH256**](https://hash256.org) — a browser-mined, post-quantum PoW token on Ethereum mainnet.

**Hybrid architecture**: Go orchestrator + Rust mining core (subprocess IPC via JSON-lines).
Dual-mode: **GPU** (OpenCL, for local rigs) and **CPU-only** (for VPS).

Contract: [`0xAC7b5d06fa1e77D08aea40d46cB7C5923A87A0cc`](https://etherscan.io/address/0xAC7b5d06fa1e77D08aea40d46cB7C5923A87A0cc)

---

## Table of Contents

- [What it does](#what-it-does)
- [How HASH256 mining works](#how-hash256-mining-works)
- [Architecture](#architecture)
- [Quick start](#quick-start)
- [CLI reference](#cli-reference)
- [Configuration](#configuration)
- [Wallet setup](#wallet-setup)
- [Running modes](#running-modes)
- [IPC protocol](#ipc-protocol)
- [Build from source](#build-from-source)
- [Security](#security)
- [Roadmap](#roadmap)
- [License](#license)
- [Disclaimer](#disclaimer)

---

## What it does

- Reads on-chain mining state from the HASH256 contract (challenge, difficulty, epoch, per-block cap).
- Grinds `keccak256(challenge ‖ nonce) < difficulty` locally using either CPU threads (Rust + rayon) or GPU (OpenCL).
- Submits winning nonces as `mine(uint256)` transactions via EIP-1559 on Ethereum mainnet.
- Hot-retargets workers on epoch rotation (every 100 blocks) without restart.
- Supports **single wallet** or **multi-wallet HD (BIP39)** — up to N derived accounts mining in parallel.
- Includes RPC failover across multiple endpoints.
- Stores keys in **Ethereum keystore v3** (single) or **scrypt + AES-CTR encrypted BIP39 blob** (multi).

---

## How HASH256 mining works

From the [verified contract](https://sourcify.dev/#/lookup/0xAC7b5d06fa1e77D08aea40d46cB7C5923A87A0cc):

| Property | Value |
|---|---|
| Challenge | `keccak256(chainId ‖ contract ‖ miner ‖ epoch)` |
| Valid nonce | `keccak256(challenge ‖ nonce) < currentDifficulty` |
| Epoch | `block.number / 100` (~20 min rotation) |
| Difficulty retarget | every 2,016 mints, targeting 1 mint / 5 blocks (±4× clamp) |
| **Global rate limit** | **10 mints per block** (hard cap) |
| Base reward | 100 HASH / mint, halves every 100,000 mints |
| Anti-replay | `(miner, nonce, epoch)` tuple can only be used once |
| Mining supply | 18,900,000 HASH (of 21M total) |

Because challenge is bound to `miner = msg.sender`, solutions cannot be stolen from the mempool. The global **10/block cap** is the bottleneck — when competition is high, failed submissions revert with `BlockCapReached` and still cost gas.

---

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│                      hash256-miner  (Go)                       │
│                                                                │
│  ┌──────────┐   ┌──────────────┐   ┌───────────────────────┐  │
│  │   CLI    │   │   Wallet     │   │   Chain Client        │  │
│  │ (cobra)  │   │ (BIP39/v3)   │   │ (go-ethereum, RPC)    │  │
│  └────┬─────┘   └──────┬───────┘   └───────────┬───────────┘  │
│       │                │                        │              │
│       └────────────────┴────── Orchestrator ────┘              │
│                                   │                            │
│                   stdin/stdout (JSON-lines IPC)                │
│                                   │                            │
└───────────────────────────────────┼────────────────────────────┘
                                    ▼
             ┌─────────────────────────────────────┐
             │    hash256-miner-core  (Rust)       │
             │                                     │
             │  ┌────────┐         ┌────────────┐  │
             │  │  CPU   │         │    GPU     │  │
             │  │ rayon  │         │  OpenCL    │  │
             │  │ + sha3 │         │ keccak256  │  │
             │  └────────┘         └────────────┘  │
             └─────────────────────────────────────┘
```

- **Go side** handles user interaction, on-chain reads/writes, wallet management, and the mining loop.
- **Rust side** does only one thing: grind hashes. Driven entirely via JSON-lines over stdio.
- Subprocess boundary gives **crash isolation** (miner core crash ≠ orchestrator crash) and **easy cross-compilation** (two static binaries).

---

## Private Orderflow (CRITICAL)

Ethereum validators include transactions from private block-builder lanes (Flashbots, MEVBlocker, etc.) **ahead of public-mempool txs**. Public-mempool priority tips do NOT buy block placement against private orderflow — block builders ignore them.

For HASH256 this matters because of the **10 mints/block global cap**: if 10 mine() txs come through private lanes, your public-mempool tx reverts with `BlockCapReached` and you still pay gas.

This miner routes all `mine()` submissions through private RPCs while keeping reads on public endpoints:

```toml
[rpc]
endpoints = [
  "https://ethereum-rpc.publicnode.com",
  "https://eth.llamarpc.com",
  "https://rpc.ankr.com/eth",
]
submit_endpoints = [
  "https://rpc.mevblocker.io/fast",
  "https://rpc.flashbots.net/fast",
]
```

Supported out of the box:
- **Flashbots Protect** `https://rpc.flashbots.net/fast` — fastest builder inclusion path
- **MEVBlocker Fast** `https://rpc.mevblocker.io/fast` — alternative builder network

These only accept `eth_sendRawTransaction` (and a few MEV-specific methods). The miner handles this — reads always go to the public pool.

If you leave `submit_endpoints` empty, you'll get a loud warning and fall back to public mempool (you will lose races).

---



## Quick start

### Prerequisites

- **Go** 1.25+
- **Rust** 1.70+ (stable)
- *(Optional, GPU mode only)* OpenCL runtime:
  - macOS: shipped with Metal/OpenCL framework
  - Linux NVIDIA: `nvidia-opencl-icd`
  - Linux AMD: `rocm-opencl-runtime`
  - Linux Intel: `intel-opencl-icd`
  - Windows: GPU driver usually ships it
- An Ethereum RPC endpoint (defaults use public endpoints; for production use your own Alchemy/Infura/QuickNode)
- An Ethereum wallet with some ETH for gas (~0.05 ETH recommended per wallet)

### Install

```bash
git clone https://github.com/<your-user>/hash256-miner.git
cd hash256-miner
make build            # CPU-only build (works on any VPS)
# or:
make build GPU=1      # build with OpenCL GPU support
```

Produces:
- `bin/hash256-miner`       — Go CLI + orchestrator
- `bin/hash256-miner-core`  — Rust mining worker

### First run

```bash
# 1. Check chain state (no wallet required)
./bin/hash256-miner chain state

# 2. Initialize wallet (interactive)
./bin/hash256-miner wallet init

# 3. Check balance
./bin/hash256-miner wallet balance

# 4. Start mining
./bin/hash256-miner mine start
```

Default config path is `configs/miner.toml` — edit it to customize RPC endpoints, gas strategy, etc.

---

## CLI reference

### `wallet init`

Interactive setup. Prompts for:

1. **Mode** — single wallet (keystore v3) or multi-wallet (BIP39 HD).
2. **Source** — generate new or import existing.
   - Single / import → paste a hex private key (0x-prefixed or not).
   - Single / new → generates a fresh secp256k1 key; displays it once, then encrypts.
   - Multi / import → paste a BIP39 mnemonic (12 or 24 words).
   - Multi / new → generates a 12/24-word mnemonic; displays it once, then encrypts.
3. **Number of accounts** (multi only) — derives `m/44'/60'/0'/0/{0..N-1}`.
4. **Passphrase** (twice to confirm).

Output: `data/wallets/main.keystore` (single) or `data/wallets/mnemonic.enc` (multi).

### `wallet list [--config ...]`

Lists configured addresses without unlocking (uses the address field of the keystore file directly; multi mode just shows the config, since derivation requires the passphrase).

### `wallet balance [--config ...]`

Unlocks the wallet and queries ETH balance for every account.

### `mine start [--config ...]`

Main mining loop:
1. Loads and unlocks wallet.
2. Connects to RPC pool (with failover).
3. If `genesisComplete == false`: enters stand-by mode, polls every `stand_by_poll_interval_ms`.
4. Once open: spawns one Rust worker per wallet account, dispatches challenge + difficulty.
5. On `found` event → verifies locally → submits `mine(nonce)` → restarts worker.
6. On epoch rotation → broadcasts retarget to all workers (hot-swap, no restart).
7. On `DifficultyAdjusted` event (or periodic poll) → updates difficulty.

### `mine status`

One-shot snapshot equivalent to `chain state`.

### `chain state [--config ...]`

Prints the contract's `genesisState()` and `miningState()` tuples. No wallet needed.

---

## Configuration

Full reference in [`configs/miner.toml`](configs/miner.toml):

```toml
[miner]
mode = "auto"             # auto | cpu | gpu
data_dir = "./data"
cpu_threads = 0           # 0 = all available
gpu_devices = []          # [0], [0,1], [] = first device in auto mode
batch_size = 1048576
core_binary = ""          # auto-detected from ./bin/ or miner-core/target/

[rpc]
endpoints = [
  "https://ethereum-rpc.publicnode.com",
  "https://eth.llamarpc.com",
  "https://rpc.ankr.com/eth",
]
chain_id = 1
request_timeout_ms = 8000

[contract]
address = "0xAC7b5d06fa1e77D08aea40d46cB7C5923A87A0cc"

[wallet]
mode = "single"                               # single | multi
keystore = "./data/wallets/main.keystore"     # for single
mnemonic_file = "./data/wallets/mnemonic.enc" # for multi
accounts = 5                                  # multi only
passphrase_env = ""                           # e.g. "HASH_WALLET_PASS" for headless
passphrase_file = ""                          # path with 0600 perms

[mining]
poll_interval_ms = 9000
submit_strategy = "eip1559"                   # "flashbots" planned for v2
max_priority_fee_gwei = 3.0
priority_fee_bump_near_cap = 2.5              # multiplier when block ≥7/10
gas_limit_floor = 200000
gas_limit_ceiling = 450000
gas_limit_safety_mult = 1.5
verify_before_submit = true
stand_by_poll_interval_ms = 30000
```

---

## Wallet setup

### Single wallet (simple)

```
./bin/hash256-miner wallet init
# choose: 1 (single), 2 (import) or 1 (new)
```

Resulting file: `data/wallets/main.keystore` — a **standard Ethereum keystore v3** JSON, interoperable with MetaMask, Ledger Live, Brownie, ethers.js `Wallet.fromEncryptedJson`, etc.

### Multi-wallet (parallel mining)

```
./bin/hash256-miner wallet init
# choose: 2 (multi), 2 (import) or 1 (new), then N = 5
```

Resulting file: `data/wallets/mnemonic.enc` — the BIP39 mnemonic encrypted with scrypt (N=131072, r=8, p=1) + AES-128-CTR + Keccak256 MAC. Not directly importable by MetaMask; re-decrypt via `wallet balance` or re-import the mnemonic into MetaMask manually.

With N wallets, the miner spawns N workers, each grinding for its own challenge (challenge is address-bound). Each wallet signs its own transactions, giving you N submission slots against the 10/block global cap.

### Headless (VPS) unlock

Avoid interactive prompts:

```toml
[wallet]
passphrase_env = "HASH_WALLET_PASS"
```

```bash
export HASH_WALLET_PASS='your-passphrase-here'
./bin/hash256-miner mine start
```

Or `passphrase_file = "/run/secrets/hash_pass"` (the file **must** be `0600`).

---

## Running modes

### VPS (CPU-only)

```bash
make build           # no GPU dependency, builds anywhere
./bin/hash256-miner mine start
# in configs/miner.toml:
#   miner.mode = "cpu"
#   miner.cpu_threads = 0   (use all cores)
```

Deploy via `scp bin/ configs/ data/ vps:~/hash256-miner/`. Run under `systemd`, `tmux`, or `screen`.

### Local rig (GPU)

```bash
make build GPU=1
./bin/hash256-miner mine start
# in configs/miner.toml:
#   miner.mode = "gpu"
#   miner.gpu_devices = [0]   (or [0,1] for dual-GPU)
```

On first run you'll see detected GPU devices in the worker `ready` event.

### Auto mode

```toml
miner.mode = "auto"
```

Spawns a probe worker, detects whether any OpenCL GPU is available. If yes → GPU mode, else CPU.

---

## IPC protocol

The Rust core reads JSON commands from stdin and writes events to stdout, line-delimited.

### Commands (Go → Rust)

```jsonc
// Start a mining job
{"cmd":"start",
 "challenge":"0x<32 bytes hex>",
 "difficulty":"0x<32 bytes hex>",
 "prefix":"0x<24 bytes hex>",
 "batch":1048576,
 "mode":"cpu",
 "gpu_device":0,
 "cpu_threads":4}

// Hot-swap challenge + difficulty (epoch rotated) without restart
{"cmd":"retarget",
 "challenge":"0x...",
 "difficulty":"0x..."}

// Stop the current job
{"cmd":"stop"}

// Probe GPU devices (no job)
{"cmd":"probe"}
```

### Events (Rust → Go)

```jsonc
// Emitted once on process start
{"event":"ready","version":"0.1.0","cpu_threads":8,"gpu_devices":[...]}

// Every ~500 ms while running
{"event":"progress","hashes":8388608,"hashrate":16712403.89,"elapsed_ms":501}

// On solution
{"event":"found","nonce":"0x...","result":"0x...","hashes":...,"elapsed_ms":...}

// On manual stop or retarget
{"event":"stopped","hashes":...,"elapsed_ms":...}

// On failure
{"event":"error","message":"..."}
```

This protocol is stable; you can swap out the Rust core for any other grinder that conforms to it.

---

## Build from source

```bash
# CPU-only (VPS-friendly, no OpenCL dependency)
make build

# With GPU support
make build GPU=1

# Individual targets
make build-rust      # just the Rust worker
make build-go        # just the Go CLI
make clean           # remove bin/, dist/, miner-core/target/
make test            # go test ./... + cargo test
make lint            # go vet + cargo clippy -D warnings
```

### Cross-compile for Linux VPS (from macOS)

```bash
# Go (easy)
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
  -o dist/hash256-miner-linux-amd64 ./cmd/hash256-miner

# Rust (requires cross or target toolchain)
rustup target add x86_64-unknown-linux-musl
cd miner-core
cargo build --release --target x86_64-unknown-linux-musl
# output: miner-core/target/x86_64-unknown-linux-musl/release/hash256-miner-core
```

For GPU-enabled cross-builds you'll need the target OpenCL ICD loader. CPU-only cross-compiles are unconditional.

---

## Security

- **Keystore v3** uses standard scrypt + AES-128-CTR + Keccak256 MAC (go-ethereum's `accounts/keystore`).
- **Mnemonic store** uses a similar cryptographic envelope, with a custom JSON header (`"kind":"bip39-mnemonic"`).
- All wallet files are written with mode `0600` and their directory with `0700`.
- Passphrases from `passphrase_file` are rejected if the file is not `0600`.
- `data/wallets/` is gitignored by default.
- No telemetry, no third-party API calls, no bundled RPC keys.
- The Rust subprocess never sees the wallet passphrase or private key — it only receives hex challenge/difficulty/prefix strings.

**Never commit**:
- `data/wallets/*`
- Any `.toml` containing RPC keys in URLs (use `configs/*.local.toml` — gitignored)
- Backups of keystore JSON or mnemonic

---

## Roadmap

**v0.1 (current)** — MVP

- [x] Verified contract analysis + ABI
- [x] Rust CPU miner (rayon + sha3)
- [x] Rust GPU miner (OpenCL keccak256 kernel)
- [x] JSON-lines IPC protocol
- [x] Go CLI (cobra): wallet init/list/balance, mine start/status, chain state
- [x] BIP39 HD wallet + keystore v3
- [x] Multi-wallet parallel mining
- [x] EIP-1559 tx submission with adaptive priority tip
- [x] Local verify before submit (gas-saver)
- [x] RPC failover pool
- [x] Epoch retarget hot-swap
- [x] Stand-by mode (waits for mining to open)

**v0.2 (planned)**

- [ ] Flashbots / MEV-Relay bundle submission (avoid `BlockCapReached` gas burn under competition)
- [ ] Centralized nonce coordinator for multi-wallet (avoid submission races)
- [ ] Pre-epoch challenge precompute (zero idle time during rotation)
- [ ] Subscribe to `Mined` / `DifficultyAdjusted` events directly (fewer polls)
- [ ] TUI dashboard (live hashrate, ETA, submissions, P/L)
- [ ] Prometheus exporter

**v0.3+ (maybe)**

- [ ] CUDA backend (for ~3x perf vs OpenCL on NVIDIA)
- [ ] Distributed mining coordinator (multiple machines, one wallet pool)
- [ ] Integrated Uniswap V4 fee claim (`claimFees()`)

---

## Project layout

```
hash256-miner/
├── cmd/hash256-miner/       # Go binary entrypoint
├── internal/
│   ├── chain/               # go-ethereum client, ABI, reader, EIP-1559 submitter
│   ├── cli/                 # cobra commands
│   ├── config/              # TOML loader + defaults
│   ├── logx/                # leveled logger
│   ├── orchestrator/        # worker lifecycle, mining loop, tx coordination
│   └── wallet/              # BIP39 HD, keystore v3, encrypted mnemonic store
├── miner-core/              # Rust subprocess
│   ├── src/
│   │   ├── main.rs          # stdin/stdout IPC loop
│   │   ├── cpu.rs           # rayon + sha3 CPU grinder
│   │   ├── gpu.rs           # OpenCL GPU host (feature = "gpu")
│   │   ├── keccak.rs        # shared verify + nonce construction
│   │   └── protocol.rs      # IPC command/event types
│   └── kernels/keccak256.cl # OpenCL keccak-f[1600] kernel
├── contracts/abi/hash256.json
├── configs/miner.toml
├── data/wallets/            # encrypted keystores (gitignored)
├── Makefile
└── README.md
```

---

## License

MIT — see [LICENSE](LICENSE).

---

## Disclaimer

This is unofficial, community tooling. Not affiliated with hash256.org. Use at your own risk.

Mining uses your ETH for gas. Failed submissions (block cap reached, tx reverted) still cost gas. Start with small balances until you've verified behavior on your setup.

Cryptocurrency is volatile. The mined HASH token may go to zero. Understand the [whitepaper](https://hash256.org/whitepaper) and the [contract source](https://sourcify.dev/#/lookup/0xAC7b5d06fa1e77D08aea40d46cB7C5923A87A0cc) before running this on any meaningful amount of ETH.
