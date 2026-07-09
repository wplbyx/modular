package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
)

const (
	eccCipherVersion = byte(1)
	eccHeaderSize    = 3
	eccX25519KeySize = 32
	eccAESKeySize    = 32
	eccHKDFInfo      = "modular util ecc x25519 aes-256-gcm v1"
)

// GenerateEccKey 生成 X25519 公私钥对字符串。
// 私钥使用 PKCS#8 编码，公钥使用 PKIX 编码。
func GenerateEccKey() (prv, pub string, err error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}

	prvDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(privateKey.PublicKey())
	if err != nil {
		return "", "", err
	}

	prv = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: prvDER}))
	pub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	return
}

// EccEncrypt 使用 X25519 ECIES 风格混合加密。
// 返回 base64(version || ephemeralPublicDERLen || ephemeralPublicDER || nonce || ciphertext || tag)。
func EccEncrypt(publicKey, data string) (string, error) {
	recipientPub, err := parseEccPublicKey(publicKey)
	if err != nil {
		return "", err
	}

	ephemeralPrv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	ephemeralPub := ephemeralPrv.PublicKey()

	sharedSecret, err := ephemeralPrv.ECDH(recipientPub)
	if err != nil {
		return "", err
	}
	aesKey, err := deriveEccAESKey(sharedSecret, ephemeralPub, recipientPub)
	if err != nil {
		return "", err
	}

	gcm, err := newEccGCM(aesKey)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ephemeralDER, err := x509.MarshalPKIXPublicKey(ephemeralPub)
	if err != nil {
		return "", err
	}
	if len(ephemeralDER) > math.MaxUint16 {
		return "", fmt.Errorf("ephemeral public key too large: %d", len(ephemeralDER))
	}

	header := makeEccHeader(ephemeralDER)
	sealed := gcm.Seal(nil, nonce, []byte(data), header)

	out := make([]byte, 0, len(header)+len(nonce)+len(sealed))
	out = append(out, header...)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// EccDecrypt 解密 EccEncrypt 的产物。
func EccDecrypt(privateKey, ciphertext string) (string, error) {
	recipientPrv, err := parseEccPrivateKey(privateKey)
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	header, payload, ephemeralPub, err := splitEccCiphertext(data)
	if err != nil {
		return "", err
	}

	sharedSecret, err := recipientPrv.ECDH(ephemeralPub)
	if err != nil {
		return "", err
	}
	aesKey, err := deriveEccAESKey(sharedSecret, ephemeralPub, recipientPrv.PublicKey())
	if err != nil {
		return "", err
	}

	gcm, err := newEccGCM(aesKey)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return "", errors.New("invalid ciphertext: too short")
	}
	nonce, sealed := payload[:nonceSize], payload[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, sealed, header)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func makeEccHeader(ephemeralDER []byte) []byte {
	header := make([]byte, eccHeaderSize+len(ephemeralDER))
	header[0] = eccCipherVersion
	binary.BigEndian.PutUint16(header[1:eccHeaderSize], uint16(len(ephemeralDER)))
	copy(header[eccHeaderSize:], ephemeralDER)
	return header
}

func splitEccCiphertext(data []byte) (header, payload []byte, ephemeralPub *ecdh.PublicKey, err error) {
	if len(data) < eccHeaderSize {
		return nil, nil, nil, errors.New("invalid ciphertext: too short")
	}
	if data[0] != eccCipherVersion {
		return nil, nil, nil, fmt.Errorf("invalid ciphertext version: %d", data[0])
	}

	ephemeralLen := int(binary.BigEndian.Uint16(data[1:eccHeaderSize]))
	if ephemeralLen == 0 {
		return nil, nil, nil, errors.New("invalid ciphertext: empty ephemeral public key")
	}

	headerLen := eccHeaderSize + ephemeralLen
	if len(data) < headerLen {
		return nil, nil, nil, errors.New("invalid ciphertext: truncated ephemeral public key")
	}

	ephemeralPub, err = parseEccPublicKeyDER(data[eccHeaderSize:headerLen])
	if err != nil {
		return nil, nil, nil, err
	}
	return data[:headerLen], data[headerLen:], ephemeralPub, nil
}

func parseEccPublicKey(publicKey string) (*ecdh.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKey))
	if block == nil {
		return nil, errors.New("public key error: PEM decode failed")
	}
	return parseEccPublicKeyDER(block.Bytes)
}

func parseEccPublicKeyDER(der []byte) (*ecdh.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("public key error: unsupported format: %w", err)
	}

	pub, ok := key.(*ecdh.PublicKey)
	if !ok || len(pub.Bytes()) != eccX25519KeySize {
		return nil, errors.New("public key error: not an X25519 public key")
	}

	x25519Pub, err := ecdh.X25519().NewPublicKey(pub.Bytes())
	if err != nil {
		return nil, fmt.Errorf("public key error: invalid X25519 public key: %w", err)
	}
	return x25519Pub, nil
}

func parseEccPrivateKey(privateKey string) (*ecdh.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		return nil, errors.New("private key error: PEM decode failed")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("private key error: unsupported format: %w", err)
	}

	prv, ok := key.(*ecdh.PrivateKey)
	if !ok || len(prv.Bytes()) != eccX25519KeySize {
		return nil, errors.New("private key error: not an X25519 private key")
	}

	x25519Prv, err := ecdh.X25519().NewPrivateKey(prv.Bytes())
	if err != nil {
		return nil, fmt.Errorf("private key error: invalid X25519 private key: %w", err)
	}
	return x25519Prv, nil
}

func deriveEccAESKey(sharedSecret []byte, ephemeralPub, recipientPub *ecdh.PublicKey) ([]byte, error) {
	saltHash := sha256.New()
	saltHash.Write(ephemeralPub.Bytes())
	saltHash.Write(recipientPub.Bytes())

	return hkdf.Key(sha256.New, sharedSecret, saltHash.Sum(nil), eccHKDFInfo, eccAESKeySize)
}

func newEccGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
