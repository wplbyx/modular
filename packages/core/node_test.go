package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateID(t *testing.T) {
	tests := []struct {
		name   string
		parts  []string
		expect string
	}{
		{
			name:   "basic",
			parts:  []string{"myapp", "HTTP Server", "127.0.0.1", "8080"},
			expect: "myapp-http-server-127-0-0-1-8080",
		},
		{
			name:   "with underscores and spaces",
			parts:  []string{"my_app", "gRPC Server"},
			expect: "my-app-grpc-server",
		},
		{
			name:   "single part",
			parts:  []string{"service"},
			expect: "service",
		},
		{
			name:   "empty parts filtered",
			parts:  []string{"a", "", "b"},
			expect: "a-b",
		},
		{
			name:   "all special chars",
			parts:  []string{"::1", "9090"},
			expect: "1-9090",
		},
		{
			name:   "uppercase normalized",
			parts:  []string{"MyApp", "V2"},
			expect: "myapp-v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, GenerateID(tt.parts...))
		})
	}
}

func TestNewServiceNode(t *testing.T) {
	node := NewServiceNode(
		Identity{Name: "user-service", Version: "v1.0"},
		Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080, HealthPath: "/health"},
		Transport{Protocol: "grpc", Address: "127.0.0.1", Port: 9090},
	)

	assert.Equal(t, "user-service", node.Name)
	assert.Equal(t, "v1.0", node.Version)
	assert.NotEmpty(t, node.ID)
	assert.Len(t, node.Transports, 2)

	eps := node.Endpoints()
	assert.Len(t, eps, 2)
	assert.Equal(t, "http://127.0.0.1:8080", eps[0])
	assert.Equal(t, "grpc://127.0.0.1:9090", eps[1])
}

func TestNormalizeHost(t *testing.T) {
	assert.Equal(t, "127.0.0.1", NormalizeHost("0.0.0.0"))
	assert.Equal(t, "127.0.0.1", NormalizeHost("::"))
	assert.Equal(t, "127.0.0.1", NormalizeHost(""))
	assert.Equal(t, "192.168.1.1", NormalizeHost("192.168.1.1"))
	assert.Equal(t, "fe80::1", NormalizeHost("[fe80::1]"))
}

func TestServiceNodeZeroValue(t *testing.T) {
	var node ServiceNode
	assert.Empty(t, node.ID)
	assert.Nil(t, node.Transports)
	assert.Nil(t, node.Metadata)
}
