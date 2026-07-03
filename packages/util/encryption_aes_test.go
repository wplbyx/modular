package util

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenerateAESKey 校验 AES 密钥生成：合法长度成功且随机，非法长度报错。
func TestGenerateAESKey(t *testing.T) {
	for _, bits := range []int{AesKeyBits128, AesKeyBits192, AesKeyBits256} {
		k1, err := GenerateAESKey(bits)
		require.NoError(t, err)
		raw1, err := base64.StdEncoding.DecodeString(k1)
		require.NoError(t, err)
		require.Len(t, raw1, bits/8)

		// 同长度两次生成应不同（随机性）。
		k2, err := GenerateAESKey(bits)
		require.NoError(t, err)
		require.NotEqual(t, k1, k2)
		t.Log(k1, k2)
	}

	// 非法长度。
	_, err := GenerateAESKey(100)
	require.Error(t, err)
}

// TestEncryptDecryptAESGCM 验证 AES-GCM 加解密回环、随机性、篡改检测。
func TestEncryptDecryptAESGCM(t *testing.T) {
	key, err := GenerateAESKey(AesKeyBits256)
	require.NoError(t, err)
	plaintext := `{"user":"alice","role":"admin"}`

	// 回环：加密 -> 解密还原原文。
	ciphertext, err := EncryptAESGCM(key, plaintext)
	require.NoError(t, err)
	require.NotEmpty(t, ciphertext)

	got, err := DecryptAESGCM(key, ciphertext)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// 随机 nonce：同一明文两次密文应不同。
	again, err := EncryptAESGCM(key, plaintext)
	require.NoError(t, err)
	require.NotEqual(t, ciphertext, again)
}

// TestDecryptAESGCM_Tampered 篡改密文后解密应失败（GCM 认证反例）。
func TestDecryptAESGCM_Tampered(t *testing.T) {
	key, err := GenerateAESKey(AesKeyBits256)
	require.NoError(t, err)

	ciphertext, err := EncryptAESGCM(key, "secret")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	// 翻转最后一个字节（认证标签区域）。
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)

	_, err = DecryptAESGCM(key, tampered)
	require.Error(t, err)
}

// TestDecryptAESGCM_WrongKey 用不匹配的密钥解密应失败。
func TestDecryptAESGCM_WrongKey(t *testing.T) {
	key1, err := GenerateAESKey(AesKeyBits256)
	require.NoError(t, err)
	key2, err := GenerateAESKey(AesKeyBits256)
	require.NoError(t, err)

	ciphertext, err := EncryptAESGCM(key1, "secret")
	require.NoError(t, err)

	_, err = DecryptAESGCM(key2, ciphertext)
	require.Error(t, err)
}

// TestDecryptAESGCM_TooShort 过短的密文（短于 nonce）应返回错误而非 panic。
func TestDecryptAESGCM_TooShort(t *testing.T) {
	key, err := GenerateAESKey(AesKeyBits256)
	require.NoError(t, err)

	short := base64.StdEncoding.EncodeToString([]byte("tiny"))
	_, err = DecryptAESGCM(key, short)
	require.Error(t, err)
}

// TestEncryptAESGCM_InvalidKey 非法密钥长度应返回错误。
func TestEncryptAESGCM_InvalidKey(t *testing.T) {
	// 15 字节，非合法 AES 密钥长度。
	badKey := base64.StdEncoding.EncodeToString(make([]byte, 15))
	_, err := EncryptAESGCM(badKey, "x")
	require.Error(t, err)
}
