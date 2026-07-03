package util

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
)

// GenerateRsaKey 生成公私钥秘钥对字符串。
// 私钥使用 PKCS#8 编码（算法无关，更通用），公钥使用 PKIX 编码。
func GenerateRsaKey(bits int) (prv, pub string, err error) {
	// 生成 RSA 公私钥，位数由参数 bits 决定（推荐 >= 2048）。
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return "", "", err
	}

	// 序列化公私钥。
	prvDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	prv = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: prvDER}))
	pub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	return
}

// RsaEncrypt 公钥加密（OAEP + SHA-256）。
// 使用 OAEP 填充以避免 PKCS1v15 的 Bleichenbacher padding-oracle 攻击。
func RsaEncrypt(publicKey, data string) (string, error) {
	pub, err := parsePublicKey(publicKey)
	if err != nil {
		return "", err
	}

	// OAEP 加密。
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, []byte(data), nil)
	if err != nil {
		return "", err
	}

	// 密文 base64 编码。
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// RsaDecrypt 私钥解密（OAEP + SHA-256）。
func RsaDecrypt(privateKey, ciphertext string) (string, error) {
	prv, err := parsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}

	// base64 解码密文。
	cipherData, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	// OAEP 解密。
	plaintext, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, prv, cipherData, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// RsaSignature 私钥签名（PSS + SHA-256）。
// PSS 是随机化签名，安全性优于确定性的 PKCS1v15。
func RsaSignature(privateKey, data string) (string, error) {
	prv, err := parsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}

	// 签名。
	hash := sha256.Sum256([]byte(data))
	signature, err := rsa.SignPSS(rand.Reader, prv, crypto.SHA256, hash[:], nil)
	if err != nil {
		return "", err
	}

	// base64 编码。
	return base64.StdEncoding.EncodeToString(signature), nil
}

// RsaVerification 公钥验签（PSS + SHA-256）。
func RsaVerification(publicKey, signature, data string) error {
	pub, err := parsePublicKey(publicKey)
	if err != nil {
		return err
	}

	// base64 解码签名。
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return err
	}

	// 验签。
	hash := sha256.Sum256([]byte(data))
	return rsa.VerifyPSS(pub, crypto.SHA256, hash[:], signatureBytes, nil)
}

// parsePublicKey 解析 PEM 编码的公钥，仅接受 RSA 公钥（PKIX / PKCS#1 均可）。
// 非 RSA 公钥返回错误，而不是 panic。
func parsePublicKey(publicKey string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKey))
	if block == nil {
		return nil, errors.New("public key error: PEM decode failed")
	}

	// 优先 PKIX（SubjectPublicKeyInfo，最常见），失败再回退 PKCS#1。
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key error: not an RSA public key")
		}
		return rsaPub, nil
	}

	if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return pub, nil
	}

	return nil, errors.New("public key error: unsupported format")
}

// parsePrivateKey 解析 PEM 编码的私钥，兼容 PKCS#8 / PKCS#1 两种格式，仅接受 RSA 私钥。
func parsePrivateKey(privateKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		return nil, errors.New("private key error: PEM decode failed")
	}

	// PKCS#8（算法无关，优先尝试）。
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key error: not an RSA private key")
		}
		return rsaKey, nil
	}

	// 回退 PKCS#1。
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	return nil, errors.New("private key error: unsupported format")
}
