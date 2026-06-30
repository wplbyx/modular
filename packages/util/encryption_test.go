package util

import (
	"fmt"
	"testing"
)

const (
	PasswordText  = "123456"
	PasswordHash1 = "xFzjLfmyJoIWc81wEAlBKOS10G9JCelSTiBD9nHY/oc=" // BbAKdYNbtzqXdukWbhgLNBiAksp2H3hMCXHJ5rFFZ7Q=
	PasswordHash2 = "BbAKdYNbtzqXdukWbhgLNBiAksp2H3hMCXHJ5rFFZ7Q=" //
	PasswordHash3 = "f+2NgbG4tcnx2xypjKpZUwHISSVuY5oZ/x5gbAt9o04=" //

	PublicKey  = "-----BEGIN PUBLIC KEY-----\nMDwwDQYJKoZIhvcNAQEBBQADKwAwKAIhAN6W2iE7SRzpI11C58vf1IGjFju2xx4v\nGJRO+eDpAUW5AgMBAAE=\n-----END PUBLIC KEY-----\n"
	PrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nMIGrAgEAAiEA3pbaITtJHOkjXULny9/UgaMWO7bHHi8YlE754OkBRbkCAwEAAQIg\nR3vcy6VVgqJgyBevh2r3vJzKmRpzF7LtIfsTNIgagL0CEQD8X3NcdUy4xVR8L/c+\nRVGzAhEA4cnRxD87H8ki/nPZsVzc4wIQYDC1VJE029wCdo8FqotbNwIRAIi5EjnY\n5C+CN5uHgYoiJmsCEQCuSP5LVkeciOcithnsLIWX\n-----END RSA PRIVATE KEY-----\n"
)

// TestRsaEncrypt 验证公钥加密
//
//	admin111  -> FNc/zJHwdVhm3qsk4p+cRFb6w1ZmYKNNvBwDM8qmmWc=
//	123456    -> YvDupnd2OMUrYblfMoaSAs0iHjCvaOymffJzZIWiyb4=
//	123456    -> Ti/BTPR+wWr3pwBwnywlvgtGc25YNZPZlL+3vBZcUek=
//	12345678  -> cXIipPI5AhHCg/DIzy+nWo+NPWTdPzB4rTFsl8wR/74=
//	hello     -> j5ydPJdPV+vwIxG2GpI0zoZ0bcf3kWJCYKaJAc+5OtE=
//	hello     -> QFSw+RfSg986pUuYycUBRsA89AZhzRzEfyIGoCAJR84=
//	hello     -> cprXsF3zVJnoZE+RUXsGf2j4S+L7HdpexLk4rD2QlPA=
func TestRsaEncrypt(t *testing.T) {
	prv, pub, err := GenerateRsaKey(2048)
	if err != nil {
		t.Error(err.Error())
		return
	}

	encrypt, err := RsaEncrypt(pub, PasswordText)
	if err != nil {
		t.Error(err.Error())
		return
	}
	decrypt, err := RsaDecrypt(prv, encrypt)
	if err != nil {
		t.Error(err.Error())
		return
	}

	fmt.Println(encrypt)
	fmt.Println(decrypt)
	fmt.Println(PasswordText == decrypt)
}

func TestSign(t *testing.T) {
	prv, pub, _ := GenerateRsaKey(2048)

	var data = "this is a message to sign and verify."
	signature, err := RsaSignature(prv, data)
	if err != nil {
		t.Error(err.Error())
		return
	}

	if err = RsaVerification(pub, signature, data); err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Println("Signature verified successfully.")
}
