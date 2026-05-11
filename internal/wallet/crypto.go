package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

const (
	scryptN      = 1 << 17
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	saltLen      = 32
	ivLen        = 16
)

func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
}

func aesEncryptWithScrypt(plaintext []byte, passphrase string) (ciphertext, iv, salt, mac []byte, err error) {
	salt = make([]byte, saltLen)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("salt: %w", err)
	}
	iv = make([]byte, ivLen)
	if _, err = rand.Read(iv); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("iv: %w", err)
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("kdf: %w", err)
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("aes: %w", err)
	}
	stream := cipher.NewCTR(block, iv)
	ciphertext = make([]byte, len(plaintext))
	stream.XORKeyStream(ciphertext, plaintext)

	h := sha3.NewLegacyKeccak256()
	h.Write(key[16:32])
	h.Write(ciphertext)
	mac = h.Sum(nil)
	return
}

func aesDecryptWithScrypt(ciphertext, iv, salt, expectedMAC []byte, passphrase string) ([]byte, error) {
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, fmt.Errorf("kdf: %w", err)
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(key[16:32])
	h.Write(ciphertext)
	gotMAC := h.Sum(nil)
	if !constantTimeEqual(gotMAC, expectedMAC) {
		return nil, fmt.Errorf("MAC mismatch (wrong passphrase?)")
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	stream := cipher.NewCTR(block, iv)
	plaintext := make([]byte, len(ciphertext))
	stream.XORKeyStream(plaintext, ciphertext)
	return plaintext, nil
}

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}
