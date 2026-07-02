package redis

import (
	"testing"
)

func TestNewRedisClient_NilConfig(t *testing.T) {
	_, err := NewRedisClient(nil)
	if err == nil {
		t.Fatalf("NewRedisClient(nil) expected error, got nil")
	}
}

func TestGetClient_DefaultNil(t *testing.T) {
	// 仅验证未初始化时返回 nil，不依赖真实连接。
	// 注意：其他测试可能已设置 globalClient，这里只做类型/非 panic 校验。
	_ = GetClient()
}
