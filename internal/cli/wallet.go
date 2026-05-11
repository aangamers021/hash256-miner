package cli

import (
	"bufio"
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/spf13/cobra"

	"github.com/hash256-miner/hash256-miner/internal/chain"
	"github.com/hash256-miner/hash256-miner/internal/config"
	"github.com/hash256-miner/hash256-miner/internal/wallet"
)

func walletCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "wallet", Short: "Wallet management"}
	cmd.AddCommand(walletInitCommand())
	cmd.AddCommand(walletListCommand())
	cmd.AddCommand(walletBalanceCommand())
	return cmd
}

func walletInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive wallet setup (import existing or generate new)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWalletInit(cmd.Context())
		},
	}
}

func walletListCommand() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured wallet accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWalletList(cfgPath)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "configs/miner.toml", "config file")
	return cmd
}

func walletBalanceCommand() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Show ETH balance of configured wallet(s)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWalletBalance(cmd.Context(), cfgPath)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "configs/miner.toml", "config file")
	return cmd
}

func runWalletInit(_ context.Context) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("┌ hash256-miner wallet setup")
	fmt.Println("│")

	mode := askChoice(reader, "│ wallet mode: (1) single wallet  (2) multi-wallet HD  [1]: ", []string{"1", "2"}, "1")
	multi := mode == "2"

	origin := askChoice(reader, "│ source:      (1) generate new   (2) import existing   [2]: ", []string{"1", "2"}, "2")
	importing := origin == "2"

	dir := filepath.Join("data", "wallets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	if multi {
		return runInitMulti(reader, dir, importing)
	}
	return runInitSingle(reader, dir, importing)
}

func runInitSingle(reader *bufio.Reader, dir string, importing bool) error {
	keystorePath := filepath.Join(dir, "main.keystore")
	if _, err := os.Stat(keystorePath); err == nil {
		ok := askYesNo(reader, fmt.Sprintf("│ %s already exists. Overwrite? [y/N]: ", keystorePath), false)
		if !ok {
			return fmt.Errorf("cancelled")
		}
	}

	var priv string
	if importing {
		fmt.Println("│ paste your private key (64 hex chars, with or without 0x):")
		fmt.Print("│ > ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		priv = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "0x"))
	} else {
		key, err := crypto.GenerateKey()
		if err != nil {
			return fmt.Errorf("gen key: %w", err)
		}
		priv = fmt.Sprintf("%064x", key.D)
	}

	pass, err := wallet.PromptPasswordConfirm("│ new passphrase: ")
	if err != nil {
		return err
	}

	ecdsaKey, err := crypto.HexToECDSA(priv)
	if err != nil {
		return fmt.Errorf("parse key: %w", err)
	}
	addr, err := wallet.SaveKeystore(keystorePath, ecdsaKey, pass)
	if err != nil {
		return err
	}
	fmt.Println("│")
	fmt.Printf("│ ✓ keystore saved: %s\n", keystorePath)
	fmt.Printf("│ ✓ address:        %s\n", addr.Hex())
	if !importing {
		fmt.Println("│")
		fmt.Println("│ ⚠ WRITE DOWN YOUR PRIVATE KEY — this is the only copy:")
		fmt.Printf("│   0x%s\n", priv)
	}
	fmt.Println("└")
	fmt.Println()
	fmt.Println("update configs/miner.toml:")
	fmt.Printf("  wallet.mode     = \"single\"\n")
	fmt.Printf("  wallet.keystore = %q\n", keystorePath)
	return nil
}

func runInitMulti(reader *bufio.Reader, dir string, importing bool) error {
	mnemonicPath := filepath.Join(dir, "mnemonic.enc")
	if _, err := os.Stat(mnemonicPath); err == nil {
		ok := askYesNo(reader, fmt.Sprintf("│ %s already exists. Overwrite? [y/N]: ", mnemonicPath), false)
		if !ok {
			return fmt.Errorf("cancelled")
		}
	}

	var mnemonic string
	if importing {
		fmt.Println("│ paste your BIP39 mnemonic (12/24 words, space-separated):")
		fmt.Print("│ > ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		mnemonic = strings.TrimSpace(line)
		if !wallet.ValidateMnemonic(mnemonic) {
			return fmt.Errorf("invalid BIP39 mnemonic")
		}
	} else {
		bits := askChoice(reader, "│ entropy bits: (1) 128 (12 words)  (2) 256 (24 words) [2]: ", []string{"1", "2"}, "2")
		ent := 256
		if bits == "1" {
			ent = 128
		}
		m, err := wallet.GenerateMnemonic(ent)
		if err != nil {
			return err
		}
		mnemonic = m
		fmt.Println("│")
		fmt.Println("│ ⚠ WRITE DOWN THESE WORDS NOW — they are the only copy:")
		fmt.Println("│")
		fmt.Println("│   " + mnemonic)
		fmt.Println("│")
		ok := askYesNo(reader, "│ continue? [y/N]: ", false)
		if !ok {
			return fmt.Errorf("cancelled")
		}
	}

	countStr := askString(reader, "│ how many accounts to derive? [5]: ", "5")
	var count int
	if _, err := fmt.Sscanf(countStr, "%d", &count); err != nil || count < 1 {
		return fmt.Errorf("invalid count")
	}

	pass, err := wallet.PromptPasswordConfirm("│ new passphrase: ")
	if err != nil {
		return err
	}
	if err := wallet.SaveMnemonicEncrypted(mnemonicPath, mnemonic, pass); err != nil {
		return err
	}

	accounts, err := wallet.DeriveAccounts(mnemonic, "", count)
	if err != nil {
		return err
	}
	fmt.Println("│")
	fmt.Printf("│ ✓ mnemonic saved: %s (encrypted)\n", mnemonicPath)
	fmt.Println("│ ✓ derived accounts:")
	for _, a := range accounts {
		fmt.Printf("│   [%d] %s   %s\n", a.Index, a.Address.Hex(), a.Path)
	}
	fmt.Println("└")
	fmt.Println()
	fmt.Println("update configs/miner.toml:")
	fmt.Printf("  wallet.mode          = \"multi\"\n")
	fmt.Printf("  wallet.mnemonic_file = %q\n", mnemonicPath)
	fmt.Printf("  wallet.accounts      = %d\n", count)
	return nil
}

func runWalletList(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	switch cfg.Wallet.Mode {
	case "single":
		addr, err := wallet.PeekKeystoreAddress(cfg.Wallet.Keystore)
		if err != nil {
			return err
		}
		fmt.Printf("[0] %s (keystore: %s)\n", addr.Hex(), cfg.Wallet.Keystore)
	case "multi":
		fmt.Printf("multi-wallet mnemonic: %s\n", cfg.Wallet.MnemonicFile)
		fmt.Printf("configured accounts: %d\n", cfg.Wallet.Accounts)
		fmt.Println("(run `wallet balance` after unlocking to see derived addresses)")
	default:
		return fmt.Errorf("unknown wallet mode %q", cfg.Wallet.Mode)
	}
	return nil
}

func runWalletBalance(parent context.Context, cfgPath string) error {
	cfg, err := config.Load(cfgPath)
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
	}
	if err != nil {
		return err
	}
	defer wm.Wipe()

	client, _, err := buildChain(parent, cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	for _, acc := range wm.Accounts() {
		ctx, cancel := contextWithShortTimeout(parent)
		bal, err := client.BalanceOf(ctx, acc.Address)
		cancel()
		if err != nil {
			fmt.Printf("[%d] %s  (error: %v)\n", acc.Index, acc.Address.Hex(), err)
			continue
		}
		eth := weiToEthStr(bal)
		fmt.Printf("[%d] %s  %s ETH\n", acc.Index, acc.Address.Hex(), eth)
	}
	return nil
}

func weiToEthStr(w *big.Int) string {
	if w == nil {
		return "0"
	}
	f := new(big.Float).SetInt(w)
	f.Quo(f, big.NewFloat(1e18))
	return f.Text('f', 6)
}

func contextWithShortTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
}

func chainNoOp(_ *chain.Client) {}

func askChoice(r *bufio.Reader, prompt string, valid []string, def string) string {
	for {
		fmt.Print(prompt)
		line, err := r.ReadString('\n')
		if err != nil {
			return def
		}
		s := strings.TrimSpace(line)
		if s == "" {
			return def
		}
		for _, v := range valid {
			if s == v {
				return s
			}
		}
	}
}

func askString(r *bufio.Reader, prompt, def string) string {
	fmt.Print(prompt)
	line, err := r.ReadString('\n')
	if err != nil {
		return def
	}
	s := strings.TrimSpace(line)
	if s == "" {
		return def
	}
	return s
}

func askYesNo(r *bufio.Reader, prompt string, def bool) bool {
	fmt.Print(prompt)
	line, err := r.ReadString('\n')
	if err != nil {
		return def
	}
	s := strings.ToLower(strings.TrimSpace(line))
	if s == "" {
		return def
	}
	return s == "y" || s == "yes"
}
