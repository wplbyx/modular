package util

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
)

// GenerateRsaKey 生成公私钥秘钥对字符串
func GenerateRsaKey(bits int) (prv, pub string, err error) {
	// 生成RSA公私钥
	privateKey, err := rsa.GenerateKey(rand.Reader, bits) //1024位
	if err != nil {
		return "", "", err
	}

	// 序列化公私钥
	derPkix, err := x509.MarshalPKIXPublicKey(&(privateKey.PublicKey))
	if err != nil {
		return "", "", err
	}

	prv = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}))
	pub = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: derPkix}))

	return
}

// RsaEncrypt 公钥加密
func RsaEncrypt(publicKey, data string) (string, error) {
	// 解析公钥
	block, _ := pem.Decode([]byte(publicKey))
	if block == nil {
		return "", errors.New("public key error")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", err
	}
	// 加密
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, pub.(*rsa.PublicKey), []byte(data))
	if err != nil {
		return "", err
	}

	// 密文编码(base64编码)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// RsaDecrypt 私钥解密
func RsaDecrypt(privateKey, ciphertext string) (string, error) {
	// 解析私钥
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		return "", errors.New("private key error")
	}
	prv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	// 解码密文(base64编码)
	cipherData, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	// 解密
	data, err := rsa.DecryptPKCS1v15(rand.Reader, prv, cipherData)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RsaSignature 私钥签名
func RsaSignature(privateKey, data string) (string, error) {
	// 解析私钥
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		return "", errors.New("decode private key error")
	}
	prv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}

	// 签名
	hash := sha256.Sum256([]byte(data))
	signature, err := rsa.SignPKCS1v15(rand.Reader, prv, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}

	// 编码
	return base64.StdEncoding.EncodeToString(signature), nil

}

// RsaVerification 公钥验签
func RsaVerification(publicKey, signature, data string) error {
	// 解析公钥
	block, _ := pem.Decode([]byte(publicKey))
	if block == nil {
		return errors.New("decode public key error")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}

	// 解码
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return err
	}

	// 验签
	hash := sha256.Sum256([]byte(data))
	if err = rsa.VerifyPKCS1v15(pub.(*rsa.PublicKey), crypto.SHA256, hash[:], signatureBytes); err != nil {
		return err
	}

	return nil
}

func DecodeAES128CBC(encrypted, sessionKey, iv string, callback func(bs []byte) error) error {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted) // 密文
	if err != nil {
		return err
	}
	aeskey, err := base64.StdEncoding.DecodeString(sessionKey) // 秘钥,16位->AES-128
	if err != nil {
		return err
	}
	vector, err := base64.StdEncoding.DecodeString(iv) // 初始向量
	if err != nil {
		return err
	}

	//
	block, err := aes.NewCipher(aeskey) // block, 默认 AES-128
	if err != nil {
		return err
	}

	//plaintext := make([]byte, len(ciphertext))

	decrypter := cipher.NewCBCDecrypter(block, vector)
	decrypter.CryptBlocks(ciphertext, ciphertext)

	result, err := PKCS7Unpad(ciphertext, block.BlockSize())
	if err != nil {
		return err
	}

	return callback(result)
}

func PKCS7Unpad(data []byte, blockSize int) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errors.New("invalid block size")
	}
	if len(data)%blockSize != 0 || len(data) == 0 {
		return nil, errors.New("invalid PKCS7 data")
	}
	c := data[len(data)-1]
	n := int(c)
	if n == 0 || n > len(data) {
		return nil, errors.New("invalid padding on input")
	}
	for i := 0; i < n; i++ {
		if data[len(data)-n+i] != c {
			return nil, errors.New("invalid padding on input")
		}
	}
	return data[:len(data)-n], nil
}
