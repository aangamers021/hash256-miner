package wallet

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

type PassphraseSource struct {
	EnvVar      string
	File        string
	Interactive bool
	Prompt      string
}

func (p PassphraseSource) Resolve() (string, error) {
	if p.EnvVar != "" {
		v := os.Getenv(p.EnvVar)
		if v != "" {
			return v, nil
		}
	}
	if p.File != "" {
		if err := checkFilePermissions(p.File); err != nil {
			return "", err
		}
		raw, err := os.ReadFile(p.File)
		if err != nil {
			return "", fmt.Errorf("read passphrase file: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	if p.Interactive {
		return promptPassword(p.Prompt)
	}
	return "", fmt.Errorf("no passphrase source available (env/file/interactive)")
}

func promptPassword(prompt string) (string, error) {
	if prompt == "" {
		prompt = "passphrase: "
	}
	fmt.Fprint(os.Stderr, prompt)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("cannot read passphrase: stdin is not a terminal")
	}
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func PromptPasswordConfirm(prompt string) (string, error) {
	first, err := promptPassword(prompt)
	if err != nil {
		return "", err
	}
	if first == "" {
		return "", fmt.Errorf("passphrase cannot be empty")
	}
	second, err := promptPassword("confirm: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", fmt.Errorf("passphrases do not match")
	}
	return first, nil
}

func checkFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return fmt.Errorf("file %s permissions too loose (%o); expected 0600", path, mode)
	}
	return nil
}

func zeroPrivateKey(k *ecdsa.PrivateKey) {
	if k == nil || k.D == nil {
		return
	}
	k.D.SetInt64(0)
}
