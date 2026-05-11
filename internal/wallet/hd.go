package wallet

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
)

const EthereumPurpose = uint32(0x8000002C)

const EthereumCoinType = uint32(0x8000003C)

type HDAccount struct {
	Path    string
	Index   uint32
	Address common.Address
	Key     *ecdsa.PrivateKey
}

func GenerateMnemonic(entropyBits int) (string, error) {
	if entropyBits != 128 && entropyBits != 160 && entropyBits != 192 && entropyBits != 224 && entropyBits != 256 {
		return "", fmt.Errorf("entropy bits must be 128/160/192/224/256, got %d", entropyBits)
	}
	ent, err := bip39.NewEntropy(entropyBits)
	if err != nil {
		return "", fmt.Errorf("generate entropy: %w", err)
	}
	mnemonic, err := bip39.NewMnemonic(ent)
	if err != nil {
		return "", fmt.Errorf("build mnemonic: %w", err)
	}
	return mnemonic, nil
}

func ValidateMnemonic(mnemonic string) bool {
	return bip39.IsMnemonicValid(mnemonic)
}

func DeriveAccount(mnemonic, passphrase string, index uint32) (*HDAccount, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid mnemonic")
	}
	seed := bip39.NewSeed(mnemonic, passphrase)

	master, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	purpose, err := master.NewChildKey(EthereumPurpose)
	if err != nil {
		return nil, fmt.Errorf("derive purpose: %w", err)
	}
	coin, err := purpose.NewChildKey(EthereumCoinType)
	if err != nil {
		return nil, fmt.Errorf("derive coin: %w", err)
	}
	account, err := coin.NewChildKey(0x80000000)
	if err != nil {
		return nil, fmt.Errorf("derive account: %w", err)
	}
	change, err := account.NewChildKey(0)
	if err != nil {
		return nil, fmt.Errorf("derive change: %w", err)
	}
	leaf, err := change.NewChildKey(index)
	if err != nil {
		return nil, fmt.Errorf("derive leaf[%d]: %w", index, err)
	}

	priv, err := crypto.ToECDSA(leaf.Key)
	if err != nil {
		return nil, fmt.Errorf("parse ECDSA: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	path := fmt.Sprintf("m/44'/60'/0'/0/%d", index)

	return &HDAccount{
		Path:    path,
		Index:   index,
		Address: addr,
		Key:     priv,
	}, nil
}

func DeriveAccounts(mnemonic, passphrase string, count int) ([]*HDAccount, error) {
	if count <= 0 {
		return nil, fmt.Errorf("count must be > 0")
	}
	out := make([]*HDAccount, 0, count)
	for i := 0; i < count; i++ {
		acc, err := DeriveAccount(mnemonic, passphrase, uint32(i))
		if err != nil {
			return nil, err
		}
		out = append(out, acc)
	}
	return out, nil
}
