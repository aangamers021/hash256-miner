package wallet

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func SaveKeystore(path string, priv *ecdsa.PrivateKey, passphrase string) (common.Address, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return common.Address{}, fmt.Errorf("mkdir: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	raw, err := keystore.EncryptKey(&keystore.Key{
		Address:    addr,
		PrivateKey: priv,
	}, passphrase, keystore.StandardScryptN, keystore.StandardScryptP)
	if err != nil {
		return common.Address{}, fmt.Errorf("encrypt: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return common.Address{}, fmt.Errorf("write %s: %w", path, err)
	}
	return addr, nil
}

func LoadKeystore(path, passphrase string) (*ecdsa.PrivateKey, common.Address, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("read %s: %w", path, err)
	}
	k, err := keystore.DecryptKey(raw, passphrase)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("decrypt: %w", err)
	}
	return k.PrivateKey, k.Address, nil
}

func PeekKeystoreAddress(path string) (common.Address, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return common.Address{}, fmt.Errorf("read %s: %w", path, err)
	}
	var header struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return common.Address{}, fmt.Errorf("parse: %w", err)
	}
	if header.Address == "" {
		return common.Address{}, fmt.Errorf("keystore missing address field")
	}
	addr := common.HexToAddress(header.Address)
	return addr, nil
}
