package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hash256-miner/hash256-miner/internal/logx"
)

func Root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hash256-miner",
		Short: "CLI auto-miner for the HASH256 PoW token",
	}
	cmd.AddCommand(walletCommand())
	cmd.AddCommand(mineCommand())
	cmd.AddCommand(chainCommand())
	return cmd
}

func chainCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "chain", Short: "On-chain inspection"}
	var cfg string
	state := &cobra.Command{
		Use:   "state",
		Short: "Print mining + genesis state snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMineStatus(cmd.Context(), cfg)
		},
	}
	state.Flags().StringVar(&cfg, "config", "configs/miner.toml", "config file")
	cmd.AddCommand(state)
	return cmd
}

func Execute() {
	ctx := context.Background()
	if err := Root().ExecuteContext(ctx); err != nil {
		logx.Errorf("%v", err)
		fmt.Fprintln(os.Stderr, "exit: 1")
		os.Exit(1)
	}
}
