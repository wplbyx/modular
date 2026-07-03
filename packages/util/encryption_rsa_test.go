package util

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const PasswordText = "123456"

// TestRsaEncrypt 验证 OAEP 公钥加密 / 私钥解密的完整回环。
func TestRsaEncrypt(t *testing.T) {
	prv, pub, err := GenerateRsaKey(2048)
	require.NoError(t, err)

	ciphertext, err := RsaEncrypt(pub, PasswordText)
	require.NoError(t, err)
	require.NotEmpty(t, ciphertext)
	t.Log(ciphertext)
	// OAEP 是随机化的，每次密文应不同。
	again, err := RsaEncrypt(pub, PasswordText)
	require.NoError(t, err)
	require.NotEqual(t, ciphertext, again)

	decrypt, err := RsaDecrypt(prv, ciphertext)
	require.NoError(t, err)
	require.Equal(t, PasswordText, decrypt)
}

// TestRsaDecrypt_WrongKey 用不匹配的私钥解密应失败（OAEP 反例）。
func TestRsaDecrypt_WrongKey(t *testing.T) {
	_, pub, err := GenerateRsaKey(2048)
	require.NoError(t, err)
	otherPrv, _, err := GenerateRsaKey(2048)
	require.NoError(t, err)

	ciphertext, err := RsaEncrypt(pub, PasswordText)
	require.NoError(t, err)

	_, err = RsaDecrypt(otherPrv, ciphertext)
	require.Error(t, err)
}

// TestRsaDecrypt_InvalidCiphertext 非法 base64 / 畸形密文应返回错误而非 panic。
func TestRsaDecrypt_InvalidCiphertext(t *testing.T) {
	prv, _, err := GenerateRsaKey(2048)
	require.NoError(t, err)

	_, err = RsaDecrypt(prv, "!!!not-base64!!!")
	require.Error(t, err)
}

// TestSign 验证 PSS 签名 / 验签的完整回环。
func TestSign(t *testing.T) {
	prv, pub, err := GenerateRsaKey(2048)
	require.NoError(t, err)

	data := "this is a message to sign and verify."
	signature, err := RsaSignature(prv, data)
	require.NoError(t, err)
	require.NotEmpty(t, signature)

	// PSS 是随机化签名，同一数据两次签名应不同。
	again, err := RsaSignature(prv, data)
	require.NoError(t, err)
	require.NotEqual(t, signature, again)

	require.NoError(t, RsaVerification(pub, signature, data))
}

// TestRsaVerification_Tampered 篡改原文后验签应失败（PSS 反例）。
func TestRsaVerification_Tampered(t *testing.T) {
	prv, pub, err := GenerateRsaKey(2048)
	require.NoError(t, err)

	data := "original message"
	signature, err := RsaSignature(prv, data)
	require.NoError(t, err)

	require.Error(t, RsaVerification(pub, signature, "tampered message"))
}

// TestGenerateRsaKey_KeyFormat 校验生成的私钥为 PKCS#8、公钥为 PKIX。
func TestGenerateRsaKey_KeyFormat(t *testing.T) {
	prv, pub, err := GenerateRsaKey(2048)
	require.NoError(t, err)
	require.Contains(t, prv, "-----BEGIN PRIVATE KEY-----")
	require.Contains(t, pub, "-----BEGIN PUBLIC KEY-----")
}
