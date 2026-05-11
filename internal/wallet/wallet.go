package wallet

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

type Mode string

const (
	ModeSingle Mode = "single"
	ModeMulti  Mode = "multi"
)

type Account struct {
	Address    common.Address
	PrivateKey *ecdsa.PrivateKey
	Path       string
	Index      uint32
}

type Manager struct {
	mode     Mode
	accounts []*Account
}

func (m *Manager) Mode() Mode             { return m.mode }
func (m *Manager) Accounts() []*Account   { return m.accounts }
func (m *Manager) Primary() *Account      { return m.accounts[0] }
func (m *Manager) Count() int             { return len(m.accounts) }
func (m *Manager) Addresses() []common.Address {
	out := make([]common.Address, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, a.Address)
	}
	return out
}

func LoadSingle(keystorePath, passphrase string) (*Manager, error) {
	priv, addr, err := LoadKeystore(keystorePath, passphrase)
	if err != nil {
		return nil, err
	}
	return &Manager{
		mode: ModeSingle,
		accounts: []*Account{{
			Address:    addr,
			PrivateKey: priv,
		}},
	}, nil
}

func LoadMulti(mnemonicPath, passphrase string, count int) (*Manager, error) {
	if count <= 0 {
		return nil, fmt.Errorf("count must be > 0")
	}
	mnemonic, err := LoadMnemonicEncrypted(mnemonicPath, passphrase)
	if err != nil {
		return nil, fmt.Errorf("load mnemonic: %w", err)
	}
	hdAccounts, err := DeriveAccounts(mnemonic, "", count)
	if err != nil {
		return nil, fmt.Errorf("derive: %w", err)
	}
	accounts := make([]*Account, 0, len(hdAccounts))
	for _, a := range hdAccounts {
		accounts = append(accounts, &Account{
			Address:    a.Address,
			PrivateKey: a.Key,
			Path:       a.Path,
			Index:      a.Index,
		})
	}
	return &Manager{mode: ModeMulti, accounts: accounts}, nil
}

func (m *Manager) Wipe() {
	for _, a := range m.accounts {
		zeroPrivateKey(a.PrivateKey)
	}
}
