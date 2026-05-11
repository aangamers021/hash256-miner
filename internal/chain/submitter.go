package chain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type Submitter struct {
	Client       *Client
	SubmitClient *Client
	ABI          *Hash256ABI
	ChainID      *big.Int
	MinGas       uint64
	MaxGas       uint64
	SafetyMult   float64
}

type SubmitParams struct {
	Key       *ecdsa.PrivateKey
	From      common.Address
	Nonce     *big.Int
	TipCapGwei float64
	BumpFactor float64
}

func NewSubmitter(c *Client, submit *Client, a *Hash256ABI, chainID *big.Int, minGas, maxGas uint64, safetyMult float64) *Submitter {
	if submit == nil {
		submit = c
	}
	return &Submitter{
		Client:       c,
		SubmitClient: submit,
		ABI:          a,
		ChainID:      chainID,
		MinGas:       minGas,
		MaxGas:       maxGas,
		SafetyMult:   safetyMult,
	}
}

func (s *Submitter) SubmitMine(ctx context.Context, p SubmitParams) (common.Hash, error) {
	callData, err := s.ABI.PackMine(p.Nonce)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack mine: %w", err)
	}

	baseFee, tipCap, err := s.Client.SuggestFees(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("suggest fees: %w", err)
	}

	priorityGwei := p.TipCapGwei
	if p.BumpFactor > 1.0 {
		priorityGwei *= p.BumpFactor
	}
	priorityWei := gweiToWei(priorityGwei)
	if priorityWei.Cmp(tipCap) < 0 {
		priorityWei = tipCap
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), priorityWei)

	gasEst, err := s.Client.EstimateGas(ctx, ethereum.CallMsg{
		From:      p.From,
		To:        &s.ABI.Address,
		Data:      callData,
		GasTipCap: priorityWei,
		GasFeeCap: maxFee,
	})
	if err != nil {
		gasEst = s.MinGas
	}
	gasLimit := uint64(float64(gasEst) * s.SafetyMult)
	if gasLimit < s.MinGas {
		gasLimit = s.MinGas
	}
	if gasLimit > s.MaxGas {
		gasLimit = s.MaxGas
	}

	nonce, err := s.Client.NonceAt(ctx, p.From, true)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pending nonce: %w", err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   s.ChainID,
		Nonce:     nonce,
		To:        &s.ABI.Address,
		Value:     big.NewInt(0),
		Gas:       gasLimit,
		GasTipCap: priorityWei,
		GasFeeCap: maxFee,
		Data:      callData,
	})

	signer := types.NewLondonSigner(s.ChainID)
	signed, err := types.SignTx(tx, signer, p.Key)
	if err != nil {
		return common.Hash{}, fmt.Errorf("sign: %w", err)
	}
	if err := s.SubmitClient.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("broadcast: %w", err)
	}
	return signed.Hash(), nil
}

func gweiToWei(gwei float64) *big.Int {
	wei := new(big.Float).Mul(big.NewFloat(gwei), big.NewFloat(1e9))
	out, _ := wei.Int(nil)
	if out.Sign() <= 0 {
		return big.NewInt(0)
	}
	return out
}
