package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hash256-miner/hash256-miner/internal/chain"
	"github.com/hash256-miner/hash256-miner/internal/config"
	"github.com/hash256-miner/hash256-miner/internal/logx"
	"github.com/hash256-miner/hash256-miner/internal/orchestrator"
	"github.com/hash256-miner/hash256-miner/internal/wallet"
)

type mineFlags struct {
	config string
}

func mineCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "mine", Short: "Mining commands"}
	cmd.AddCommand(mineStartCommand())
	cmd.AddCommand(mineStatusCommand())
	return cmd
}

func mineStartCommand() *cobra.Command {
	var f mineFlags
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the mining loop",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMineStart(cmd.Context(), f)
		},
	}
	cmd.Flags().StringVar(&f.config, "config", "configs/miner.toml", "path to TOML config")
	return cmd
}

func mineStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print on-chain mining state snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMineStatus(cmd.Context(), "configs/miner.toml")
		},
	}
}

func runMineStart(parent context.Context, f mineFlags) error {
	cfg, err := config.Load(f.config)
	if err != nil {
		return err
	}

	pass, err := resolveWalletPassphrase(cfg, "passphrase: ")
	if err != nil {
		return err
	}

	var wm *wallet.Manager
	switch cfg.Wallet.Mode {
	case "single":
		wm, err = wallet.LoadSingle(cfg.Wallet.Keystore, pass)
	case "multi":
		wm, err = wallet.LoadMulti(cfg.Wallet.MnemonicFile, pass, cfg.Wallet.Accounts)
	default:
		return fmt.Errorf("unknown wallet mode %q", cfg.Wallet.Mode)
	}
	if err != nil {
		return err
	}
	defer wm.Wipe()

	logx.Infof("wallet mode: %s, %d account(s) loaded", wm.Mode(), wm.Count())

	client, abiObj, err := buildChain(parent, cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	reader := chain.NewReader(client, abiObj)
	submitter := chain.NewSubmitter(client, abiObj, client.ChainID(),
		cfg.Mining.GasLimitFloor, cfg.Mining.GasLimitCeiling, cfg.Mining.GasLimitSafetyMult)

	coreBin, err := resolveCoreBinary(cfg)
	if err != nil {
		return err
	}

	engine := orchestrator.NewEngine(cfg, reader, submitter, wm, coreBin)

	ctx, cancel := signalContext(parent)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			logx.Infof("shutdown requested")
			return nil
		}
		return err
	}
	return nil
}

func runMineStatus(parent context.Context, cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	client, abiObj, err := buildChain(parent, cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	reader := chain.NewReader(client, abiObj)
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()

	gs, err := reader.GenesisState(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("genesis: minted=%s remaining=%s ethRaised(wei)=%s complete=%v\n",
		gs.Minted, gs.Remaining, gs.EthRaised, gs.Complete)

	ms, err := reader.MiningState(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("mining:  era=%s reward=%s diff=%s minted=%s remaining=%s epoch=%s blocks_left=%s\n",
		ms.Era, ms.Reward, ms.Difficulty, ms.Minted, ms.Remaining, ms.Epoch, ms.EpochBlocksLeft)
	return nil
}

func buildChain(ctx context.Context, cfg *config.Config) (*chain.Client, *chain.Hash256ABI, error) {
	timeout := time.Duration(cfg.RPC.RequestTimeoutMs) * time.Millisecond
	client, err := chain.NewClient(ctx, cfg.RPC.Endpoints, cfg.RPC.ChainID, timeout)
	if err != nil {
		return nil, nil, fmt.Errorf("rpc: %w", err)
	}
	abiPath := filepath.Join("contracts", "abi", "hash256.json")
	abiObj, err := chain.LoadABIFromFile(abiPath, cfg.Contract.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("abi: %w", err)
	}
	return client, abiObj, nil
}

func resolveCoreBinary(cfg *config.Config) (string, error) {
	if cfg.Miner.CoreBinary != "" {
		return cfg.Miner.CoreBinary, nil
	}
	candidates := []string{
		"./bin/hash256-miner-core",
		"./miner-core/target/release/hash256-miner-core",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("core binary not found; run `make build-rust` or set miner.core_binary")
}

func resolveWalletPassphrase(cfg *config.Config, prompt string) (string, error) {
	p := wallet.PassphraseSource{
		EnvVar:      cfg.Wallet.PassphraseEnv,
		File:        cfg.Wallet.PassphraseFile,
		Interactive: true,
		Prompt:      prompt,
	}
	return p.Resolve()
}

func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
