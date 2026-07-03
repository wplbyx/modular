package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

func RandomHex(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	buf := make([]byte, (length+1)/2)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand read failed: %w", err)
	}
	return hex.EncodeToString(buf)[:length], nil
}

func RandomCode(length int) (string, error) {
	return random("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", length)
}

func RandomNumber(length int) (string, error) {
	return random("0123456789", length)
}

func random(bytes string, length int) (string, error) {
	if length <= 0 || len(bytes) == 0 {
		return "", nil
	}

	result := make([]byte, 0, length)
	mv := big.NewInt(int64(len(bytes)))
	for i := 0; i < length; i++ {
		value, err := rand.Int(rand.Reader, mv)
		if err != nil {
			return "", fmt.Errorf("crypto/rand int failed: %w", err)
		}
		result = append(result, bytes[value.Int64()])
	}
	return string(result), nil
}
