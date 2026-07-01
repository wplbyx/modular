package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

var (
	// ErrInvalidToken 无效的Token
	ErrInvalidToken = errors.New("invalid token")
	// ErrInvalidKey 无效的密钥
	ErrInvalidKey = errors.New("invalid key: key must be 16, 24 or 32 bytes")
)

// TokenManager Token管理器接口
type TokenManager interface {
	Generate(deviceSN string) (string, error)
	Parse(token string) (deviceSN string, err error)
}

// TODO: Token 感觉可以删除掉

// AESTokenManager AES Token管理器
type AESTokenManager struct {
	key []byte // AES密钥，必须是16, 24或32字节
}

// NewAESTokenManager 创建AES Token管理器
// key必须是16(AES-128), 24(AES-192)或32(AES-256)字节
func NewAESTokenManager(key string) (*AESTokenManager, error) {
	keyBytes := []byte(key)
	keyLen := len(keyBytes)
	if keyLen != 16 && keyLen != 24 && keyLen != 32 {
		return nil, ErrInvalidKey
	}
	return &AESTokenManager{key: keyBytes}, nil
}

// Generate 生成Token
// 使用AES-GCM加密deviceSN，返回base64编码的Token
func (m *AESTokenManager) Generate(deviceSN string) (string, error) {
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// 创建nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	// 加密
	plaintext := []byte(deviceSN)
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	// base64编码
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

// Parse 解析Token
// 解密base64编码的Token，返回deviceSN
func (m *AESTokenManager) Parse(token string) (string, error) {
	// base64解码
	ciphertext, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", ErrInvalidToken
	}

	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", ErrInvalidToken
	}

	// 提取nonce和密文
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// 解密
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrInvalidToken
	}

	return string(plaintext), nil
}

// 全局Token管理器
var defaultManager *AESTokenManager

// InitTokenManager 初始化全局Token管理器
func InitTokenManager(key string) error {
	var err error
	defaultManager, err = NewAESTokenManager(key)
	return err
}

// Generate 生成Token（使用全局管理器）
func Generate(deviceSN string) (string, error) {
	if defaultManager == nil {
		return "", errors.New("token manager not initialized, please call Setup() first")
	}
	return defaultManager.Generate(deviceSN)
}

// Parse 解析Token（使用全局管理器）
func Parse(token string) (string, error) {
	if defaultManager == nil {
		return "", errors.New("token manager not initialized, please call Setup() first")
	}
	return defaultManager.Parse(token)
}
