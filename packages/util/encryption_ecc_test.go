package util

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenerateEccKey_KeyFormat 校验生成的 X25519 密钥为 PKCS#8 / PKIX PEM。
func TestGenerateEccKey_KeyFormat(t *testing.T) {
	prv, pub, err := GenerateEccKey()
	require.NoError(t, err)
	require.Contains(t, prv, "-----BEGIN PRIVATE KEY-----")
	require.Contains(t, pub, "-----BEGIN PUBLIC KEY-----")
}

// TestEccEncryptDecrypt 验证 X25519 混合加密 / 解密的完整回环和随机性。
func TestEccEncryptDecrypt(t *testing.T) {
	prv, pub, err := GenerateEccKey()
	require.NoError(t, err)

	plaintext := `{"user":"alice","role":"admin","payload":"long enough for hybrid encryption"}`
	ciphertext, err := EccEncrypt(pub, plaintext)
	require.NoError(t, err)
	require.NotEmpty(t, ciphertext)

	got, err := EccDecrypt(prv, ciphertext)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	again, err := EccEncrypt(pub, plaintext)
	require.NoError(t, err)
	require.NotEqual(t, ciphertext, again)
}

// TestEccDecrypt_WrongKey 用不匹配的私钥解密应失败。
func TestEccDecrypt_WrongKey(t *testing.T) {
	_, pub, err := GenerateEccKey()
	require.NoError(t, err)
	otherPrv, _, err := GenerateEccKey()
	require.NoError(t, err)

	ciphertext, err := EccEncrypt(pub, "secret")
	require.NoError(t, err)

	_, err = EccDecrypt(otherPrv, ciphertext)
	require.Error(t, err)
}

// TestEccDecrypt_TamperedCiphertext 篡改认证区域后解密应失败。
func TestEccDecrypt_TamperedCiphertext(t *testing.T) {
	prv, pub, err := GenerateEccKey()
	require.NoError(t, err)

	ciphertext, err := EccEncrypt(pub, "secret")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	raw[len(raw)-1] ^= 0xFF

	_, err = EccDecrypt(prv, base64.StdEncoding.EncodeToString(raw))
	require.Error(t, err)
}

// TestEccDecrypt_TamperedHeader 篡改密文头部后应被拒绝。
func TestEccDecrypt_TamperedHeader(t *testing.T) {
	prv, pub, err := GenerateEccKey()
	require.NoError(t, err)

	ciphertext, err := EccEncrypt(pub, "secret")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	raw[0] ^= 0xFF

	_, err = EccDecrypt(prv, base64.StdEncoding.EncodeToString(raw))
	require.Error(t, err)
}

// TestEccDecrypt_InvalidCiphertext 非法 base64 / 过短密文应返回错误。
func TestEccDecrypt_InvalidCiphertext(t *testing.T) {
	prv, _, err := GenerateEccKey()
	require.NoError(t, err)

	_, err = EccDecrypt(prv, "!!!not-base64!!!")
	require.Error(t, err)

	short := base64.StdEncoding.EncodeToString([]byte{eccCipherVersion})
	_, err = EccDecrypt(prv, short)
	require.Error(t, err)
}

// TestEccRejectRSAKey 传入 RSA 密钥应返回错误而非 panic。
func TestEccRejectRSAKey(t *testing.T) {
	rsaPrv, rsaPub, err := GenerateRsaKey(2048)
	require.NoError(t, err)

	_, err = EccEncrypt(rsaPub, "secret")
	require.Error(t, err)

	_, eccPub, err := GenerateEccKey()
	require.NoError(t, err)
	ciphertext, err := EccEncrypt(eccPub, "secret")
	require.NoError(t, err)

	_, err = EccDecrypt(rsaPrv, ciphertext)
	require.Error(t, err)
}
