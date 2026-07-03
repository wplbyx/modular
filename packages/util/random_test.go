package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRandomHex 校验长度、字符集与边界；非法长度返回空串。
func TestRandomHex(t *testing.T) {

	t.Run("偶数长度", func(t *testing.T) {
		got, err := RandomHex(10)
		require.NoError(t, err)
		require.Len(t, got, 10)
		// 仅含十六进制字符。
		for _, c := range got {
			assert.Truef(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"含非法字符 %q", c)
		}
	})

	t.Run("奇数长度", func(t *testing.T) {
		got, err := RandomHex(7)
		require.NoError(t, err)
		require.Len(t, got, 7)
	})

	t.Run("长度为零", func(t *testing.T) {
		got, err := RandomHex(0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("负长度", func(t *testing.T) {
		got, err := RandomHex(-3)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("随机性：同长度两次结果不同", func(t *testing.T) {
		a, err := RandomHex(16)
		require.NoError(t, err)
		b, err := RandomHex(16)
		require.NoError(t, err)
		assert.NotEqual(t, a, b)
	})
}

// TestRandomCode 校验长度、字符集（数字+大小写字母）与边界。
func TestRandomCode(t *testing.T) {

	t.Run("正常长度", func(t *testing.T) {
		const length = 32
		got, err := RandomCode(length)
		require.NoError(t, err)
		require.Len(t, got, length)
		for _, c := range got {
			assert.Truef(t,
				(c >= '0' && c <= '9') ||
					(c >= 'a' && c <= 'z') ||
					(c >= 'A' && c <= 'Z'),
				"含非法字符 %q", c)
		}
	})

	t.Run("长度为零", func(t *testing.T) {
		got, err := RandomCode(0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("负长度", func(t *testing.T) {
		got, err := RandomCode(-1)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("随机性：同长度两次结果不同", func(t *testing.T) {
		a, err := RandomCode(24)
		require.NoError(t, err)
		b, err := RandomCode(24)
		require.NoError(t, err)
		assert.NotEqual(t, a, b)
	})
}

// TestRandomNumber 校验长度、字符集（仅数字）与边界。
func TestRandomNumber(t *testing.T) {

	t.Run("正常长度", func(t *testing.T) {
		const length = 8
		got, err := RandomNumber(length)
		require.NoError(t, err)
		require.Len(t, got, length)
		for _, c := range got {
			assert.Truef(t, c >= '0' && c <= '9', "含非法字符 %q", c)
		}
	})

	t.Run("长度为零", func(t *testing.T) {
		got, err := RandomNumber(0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("负长度", func(t *testing.T) {
		got, err := RandomNumber(-5)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("随机性：同长度两次结果不同", func(t *testing.T) {
		a, err := RandomNumber(12)
		require.NoError(t, err)
		b, err := RandomNumber(12)
		require.NoError(t, err)
		assert.NotEqual(t, a, b)
	})
}
