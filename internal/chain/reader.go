package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

type Reader struct {
	Client *Client
	ABI    *Hash256ABI
}

func NewReader(c *Client, a *Hash256ABI) *Reader { return &Reader{Client: c, ABI: a} }

func (r *Reader) MiningState(ctx context.Context) (*MiningState, error) {
	data, err := r.ABI.PackMiningState()
	if err != nil {
		return nil, err
	}
	res, err := r.Client.CallContract(ctx, r.ABI.Address, data)
	if err != nil {
		return nil, fmt.Errorf("miningState call: %w", err)
	}
	return r.ABI.UnpackMiningState(res)
}

func (r *Reader) GenesisState(ctx context.Context) (*GenesisState, error) {
	data, err := r.ABI.PackGenesisState()
	if err != nil {
		return nil, err
	}
	res, err := r.Client.CallContract(ctx, r.ABI.Address, data)
	if err != nil {
		return nil, fmt.Errorf("genesisState call: %w", err)
	}
	return r.ABI.UnpackGenesisState(res)
}

func (r *Reader) GetChallenge(ctx context.Context, miner common.Address) ([32]byte, error) {
	data, err := r.ABI.PackGetChallenge(miner)
	if err != nil {
		return [32]byte{}, err
	}
	res, err := r.Client.CallContract(ctx, r.ABI.Address, data)
	if err != nil {
		return [32]byte{}, fmt.Errorf("getChallenge call: %w", err)
	}
	return r.ABI.UnpackChallenge(res)
}

func (r *Reader) MintsInBlock(ctx context.Context, blockNumber *big.Int) (*big.Int, error) {
	data, err := r.ABI.PackMintsInBlock(blockNumber)
	if err != nil {
		return nil, err
	}
	res, err := r.Client.CallContract(ctx, r.ABI.Address, data)
	if err != nil {
		return nil, fmt.Errorf("mintsInBlock call: %w", err)
	}
	return r.ABI.UnpackMintsInBlock(res)
}
