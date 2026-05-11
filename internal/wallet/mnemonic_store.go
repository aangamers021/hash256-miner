package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type encryptedEnvelope struct {
	Version    int                    `json:"version"`
	Kind       string                 `json:"kind"`
	Ciphertext string                 `json:"ciphertext"`
	MAC        string                 `json:"mac"`
	Cipher     string                 `json:"cipher"`
	CipherIV   string                 `json:"cipherIV"`
	KDF        string                 `json:"kdf"`
	KDFParams  map[string]interface{} `json:"kdfParams"`
}

func SaveMnemonicEncrypted(path, mnemonic, passphrase string) error {
	if !ValidateMnemonic(mnemonic) {
		return fmt.Errorf("invalid mnemonic")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	ciphertext, iv, salt, mac, err := aesEncryptWithScrypt([]byte(mnemonic), passphrase)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	env := encryptedEnvelope{
		Version:    1,
		Kind:       "bip39-mnemonic",
		Ciphertext: hex.EncodeToString(ciphertext),
		MAC:        hex.EncodeToString(mac),
		Cipher:     "aes-128-ctr",
		CipherIV:   hex.EncodeToString(iv),
		KDF:        "scrypt",
		KDFParams: map[string]interface{}{
			"n":     scryptN,
			"r":     scryptR,
			"p":     scryptP,
			"dklen": scryptKeyLen,
			"salt":  hex.EncodeToString(salt),
		},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func LoadMnemonicEncrypted(path, passphrase string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var env encryptedEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Kind != "bip39-mnemonic" {
		return "", fmt.Errorf("unexpected envelope kind: %s", env.Kind)
	}
	ciphertext, err := hex.DecodeString(env.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	iv, err := hex.DecodeString(env.CipherIV)
	if err != nil {
		return "", fmt.Errorf("decode iv: %w", err)
	}
	saltHex, _ := env.KDFParams["salt"].(string)
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return "", fmt.Errorf("decode salt: %w", err)
	}
	mac, err := hex.DecodeString(env.MAC)
	if err != nil {
		return "", fmt.Errorf("decode mac: %w", err)
	}
	plain, err := aesDecryptWithScrypt(ciphertext, iv, salt, mac, passphrase)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	m := strings.TrimSpace(string(plain))
	if !ValidateMnemonic(m) {
		return "", fmt.Errorf("decrypted payload is not a valid BIP39 mnemonic (wrong passphrase?)")
	}
	return m, nil
}

func PrivateKeyToChecksumAddress(hexKey string) (common.Address, error) {
	hexKey = strings.TrimPrefix(hexKey, "0x")
	priv, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return common.Address{}, fmt.Errorf("parse key: %w", err)
	}
	return crypto.PubkeyToAddress(priv.PublicKey), nil
}
