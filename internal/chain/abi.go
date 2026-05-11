package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

type Hash256ABI struct {
	ABI     abi.ABI
	Address common.Address
}

func LoadABIFromFile(path, overrideAddress string) (*Hash256ABI, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read abi file: %w", err)
	}
	return parseABI(raw, overrideAddress)
}

func LoadABIFromBytes(raw []byte, overrideAddress string) (*Hash256ABI, error) {
	return parseABI(raw, overrideAddress)
}

func parseABI(raw []byte, overrideAddress string) (*Hash256ABI, error) {
	var envelope struct {
		Address string          `json:"address"`
		ABI     json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal abi envelope: %w", err)
	}
	if envelope.ABI == nil {
		envelope.ABI = raw
	}
	parsed, err := abi.JSON(strings.NewReader(string(envelope.ABI)))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}
	addr := overrideAddress
	if addr == "" {
		addr = envelope.Address
	}
	if addr == "" {
		return nil, fmt.Errorf("no contract address")
	}
	return &Hash256ABI{ABI: parsed, Address: common.HexToAddress(addr)}, nil
}

type MiningState struct {
	Era             *big.Int
	Reward          *big.Int
	Difficulty      *big.Int
	Minted          *big.Int
	Remaining       *big.Int
	Epoch           *big.Int
	EpochBlocksLeft *big.Int
}

type GenesisState struct {
	Minted    *big.Int
	Remaining *big.Int
	EthRaised *big.Int
	Complete  bool
}

func (h *Hash256ABI) PackMine(nonce *big.Int) ([]byte, error) {
	return h.ABI.Pack("mine", nonce)
}

func (h *Hash256ABI) PackGetChallenge(miner common.Address) ([]byte, error) {
	return h.ABI.Pack("getChallenge", miner)
}

func (h *Hash256ABI) PackMiningState() ([]byte, error) {
	return h.ABI.Pack("miningState")
}

func (h *Hash256ABI) PackGenesisState() ([]byte, error) {
	return h.ABI.Pack("genesisState")
}

func (h *Hash256ABI) PackMintsInBlock(blockNumber *big.Int) ([]byte, error) {
	return h.ABI.Pack("mintsInBlock", blockNumber)
}

func (h *Hash256ABI) UnpackMiningState(data []byte) (*MiningState, error) {
	out, err := h.ABI.Unpack("miningState", data)
	if err != nil {
		return nil, err
	}
	if len(out) != 7 {
		return nil, fmt.Errorf("miningState: expected 7 outputs, got %d", len(out))
	}
	return &MiningState{
		Era:             asBigInt(out[0]),
		Reward:          asBigInt(out[1]),
		Difficulty:      asBigInt(out[2]),
		Minted:          asBigInt(out[3]),
		Remaining:       asBigInt(out[4]),
		Epoch:           asBigInt(out[5]),
		EpochBlocksLeft: asBigInt(out[6]),
	}, nil
}

func (h *Hash256ABI) UnpackGenesisState(data []byte) (*GenesisState, error) {
	out, err := h.ABI.Unpack("genesisState", data)
	if err != nil {
		return nil, err
	}
	if len(out) != 4 {
		return nil, fmt.Errorf("genesisState: expected 4 outputs, got %d", len(out))
	}
	return &GenesisState{
		Minted:    asBigInt(out[0]),
		Remaining: asBigInt(out[1]),
		EthRaised: asBigInt(out[2]),
		Complete:  asBool(out[3]),
	}, nil
}

func (h *Hash256ABI) UnpackChallenge(data []byte) ([32]byte, error) {
	out, err := h.ABI.Unpack("getChallenge", data)
	if err != nil {
		return [32]byte{}, err
	}
	if len(out) != 1 {
		return [32]byte{}, fmt.Errorf("getChallenge unexpected outputs")
	}
	b, ok := out[0].([32]byte)
	if !ok {
		return [32]byte{}, fmt.Errorf("getChallenge expected bytes32, got %T", out[0])
	}
	return b, nil
}

func (h *Hash256ABI) UnpackMintsInBlock(data []byte) (*big.Int, error) {
	out, err := h.ABI.Unpack("mintsInBlock", data)
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("mintsInBlock unexpected outputs")
	}
	return asBigInt(out[0]), nil
}

func asBigInt(v any) *big.Int {
	if bi, ok := v.(*big.Int); ok {
		return bi
	}
	return new(big.Int)
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

var _ context.Context
