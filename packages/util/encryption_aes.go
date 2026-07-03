package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	AesKeyBits128 = 128
	AesKeyBits192 = 192
	AesKeyBits256 = 256
)

// GenerateAESKey 生成 AES 随机密钥（base64 编码）。
// bits 仅允许 128 / 192 / 256（对应密钥字节长度 16 / 24 / 32）。
func GenerateAESKey(bits int) (string, error) {
	switch bits {
	case AesKeyBits128, AesKeyBits192, AesKeyBits256:
	default:
		return "", fmt.Errorf("invalid AES key bits: %d, want 128/192/256", bits)
	}

	key := make([]byte, bits/8)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// EncryptAESGCM 用 AES-GCM 加密（AEAD，自带认证标签，推荐主路径）。
// key 为 base64 编码的 AES 密钥（16/24/32 字节原文）。
// 返回 base64(nonce || ciphertext || tag)，调用方无需单独管理 nonce。
func EncryptAESGCM(key, plaintext string) (string, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}

	gcm, err := newGCM(keyBytes)
	if err != nil {
		return "", err
	}

	// 随机 nonce（Go GCM 默认 12 字节），每次加密都不同 -> 密文不同。
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	// Seal 返回 ciphertext||tag，拼在 nonce 之后。
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// DecryptAESGCM 解密 EncryptAESGCM 的产物。
// 密文或 nonce 被篡改时返回 error（认证失败），故无需额外 MAC。
func DecryptAESGCM(key, ciphertext string) (string, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	gcm, err := newGCM(keyBytes)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("invalid ciphertext: too short")
	}
	nonce, sealed := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// newGCM 由原始密钥字节构造 AES-GCM。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
