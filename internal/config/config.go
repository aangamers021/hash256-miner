package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Miner    MinerConfig    `toml:"miner"`
	RPC      RPCConfig      `toml:"rpc"`
	Contract ContractConfig `toml:"contract"`
	Wallet   WalletConfig   `toml:"wallet"`
	Mining   MiningConfig   `toml:"mining"`
}

type MinerConfig struct {
	Mode       string `toml:"mode"`
	DataDir    string `toml:"data_dir"`
	CPUThreads int    `toml:"cpu_threads"`
	GPUDevices []int  `toml:"gpu_devices"`
	BatchSize  uint64 `toml:"batch_size"`
	CoreBinary string `toml:"core_binary"`
}

type RPCConfig struct {
	Endpoints        []string `toml:"endpoints"`
	ChainID          int64    `toml:"chain_id"`
	RequestTimeoutMs int      `toml:"request_timeout_ms"`
}

type ContractConfig struct {
	Address string `toml:"address"`
}

type WalletConfig struct {
	Mode           string `toml:"mode"`
	Keystore       string `toml:"keystore"`
	PassphraseEnv  string `toml:"passphrase_env"`
	PassphraseFile string `toml:"passphrase_file"`
	MnemonicFile   string `toml:"mnemonic_file"`
	Accounts       int    `toml:"accounts"`
}

type MiningConfig struct {
	PollIntervalMs         int     `toml:"poll_interval_ms"`
	SubmitStrategy         string  `toml:"submit_strategy"`
	MaxPriorityFeeGwei     float64 `toml:"max_priority_fee_gwei"`
	PriorityFeeBumpNearCap float64 `toml:"priority_fee_bump_near_cap"`
	GasLimitFloor          uint64  `toml:"gas_limit_floor"`
	GasLimitCeiling        uint64  `toml:"gas_limit_ceiling"`
	GasLimitSafetyMult     float64 `toml:"gas_limit_safety_mult"`
	VerifyBeforeSubmit     bool    `toml:"verify_before_submit"`
	StandByPollIntervalMs  int     `toml:"stand_by_poll_interval_ms"`
}

func Load(path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", absPath, err)
	}
	var c Config
	if _, err := toml.Decode(string(data), &c); err != nil {
		return nil, fmt.Errorf("decode toml: %w", err)
	}
	if err := c.applyDefaults(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() error {
	if c.Miner.Mode == "" {
		c.Miner.Mode = "auto"
	}
	switch c.Miner.Mode {
	case "auto", "cpu", "gpu":
	default:
		return fmt.Errorf("miner.mode must be auto|cpu|gpu, got %q", c.Miner.Mode)
	}
	if c.Miner.DataDir == "" {
		c.Miner.DataDir = "./data"
	}
	if c.Miner.BatchSize == 0 {
		c.Miner.BatchSize = 1 << 20
	}
	if c.RPC.ChainID == 0 {
		c.RPC.ChainID = 1
	}
	if c.RPC.RequestTimeoutMs == 0 {
		c.RPC.RequestTimeoutMs = 8000
	}
	if len(c.RPC.Endpoints) == 0 {
		return fmt.Errorf("rpc.endpoints is required")
	}
	if c.Contract.Address == "" {
		c.Contract.Address = "0xAC7b5d06fa1e77D08aea40d46cB7C5923A87A0cc"
	}
	if c.Wallet.Mode == "" {
		c.Wallet.Mode = "single"
	}
	switch c.Wallet.Mode {
	case "single", "multi":
	default:
		return fmt.Errorf("wallet.mode must be single|multi, got %q", c.Wallet.Mode)
	}
	if c.Wallet.Accounts == 0 {
		c.Wallet.Accounts = 5
	}
	if c.Mining.PollIntervalMs == 0 {
		c.Mining.PollIntervalMs = 9000
	}
	if c.Mining.StandByPollIntervalMs == 0 {
		c.Mining.StandByPollIntervalMs = 30000
	}
	if c.Mining.SubmitStrategy == "" {
		c.Mining.SubmitStrategy = "eip1559"
	}
	if c.Mining.MaxPriorityFeeGwei == 0 {
		c.Mining.MaxPriorityFeeGwei = 3.0
	}
	if c.Mining.PriorityFeeBumpNearCap == 0 {
		c.Mining.PriorityFeeBumpNearCap = 2.5
	}
	if c.Mining.GasLimitFloor == 0 {
		c.Mining.GasLimitFloor = 200_000
	}
	if c.Mining.GasLimitCeiling == 0 {
		c.Mining.GasLimitCeiling = 450_000
	}
	if c.Mining.GasLimitSafetyMult == 0 {
		c.Mining.GasLimitSafetyMult = 1.5
	}
	return nil
}
