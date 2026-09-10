package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wplbyx/modular/packages/core"
)

// parseEndpoint 已删除：ServiceNode 现在直接携带结构化的 Transport 列表。
// consul.go 的 Register 遍历 node.Transports，每个 Transport 注册为一条 Consul 记录。

func TestTransportIDIncludesAddressAndPort(t *testing.T) {
	first := transportID("orders-1", core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080})
	second := transportID("orders-1", core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8081})

	assert.Equal(t, "orders-1-http-127-0-0-1-8080", first)
	assert.NotEqual(t, first, second)
}
