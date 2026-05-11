package orchestrator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

func difficultyToHex32(v *big.Int) string {
	if v == nil {
		return "0x" + hexZeros(64)
	}
	b := v.Bytes()
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)
	return "0x" + hex.EncodeToString(padded)
}

func challengeToHex32(c [32]byte) string {
	return "0x" + hex.EncodeToString(c[:])
}

func randomPrefix24Hex() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return "0x" + hex.EncodeToString(buf), nil
}

func hexZeros(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = '0'
	}
	return string(out)
}
