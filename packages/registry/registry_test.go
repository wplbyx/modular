package registry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceBuilder(t *testing.T) {
	// 测试服务构建器
	service := NewServiceBuilder().
		WithID("test-service-001").
		WithName("test-service").
		WithVersion("v1.0.0").
		WithEndpoint("grpc://127.0.0.1:8080").
		WithMetadata("env", "test").
		WithMetadata("team", "platform").
		Build()

	assert.Equal(t, "test-service-001", service.ID)
	assert.Equal(t, "test-service", service.Name)
	assert.Equal(t, "v1.0.0", service.Version)
	assert.Contains(t, service.Endpoints, "grpc://127.0.0.1:8080")
	assert.Equal(t, "test", service.Metadata["env"])
	assert.Equal(t, "platform", service.Metadata["team"])
}

func TestCreateGrpcService(t *testing.T) {
	service := CreateGrpcService("hello-service", "v1.0.0", "127.0.0.1", 18001)

	assert.Equal(t, "hello-service", service.Name)
	assert.Equal(t, "v1.0.0", service.Version)
	assert.Contains(t, service.Endpoints, "grpc://127.0.0.1:18001")
	assert.Equal(t, "grpc", service.Metadata["protocol"])
}

func TestCreateHttpService(t *testing.T) {
	service := CreateHttpService("api-service", "v2.0.0", "127.0.0.1", 8080)

	assert.Equal(t, "api-service", service.Name)
	assert.Equal(t, "v2.0.0", service.Version)
	assert.Contains(t, service.Endpoints, "http://127.0.0.1:8080")
	assert.Equal(t, "http", service.Metadata["protocol"])
	assert.Equal(t, "/check", service.Metadata["health_path"])
}

func TestBuildConsulTarget(t *testing.T) {
	target := BuildConsulTarget("hello-service")
	assert.Equal(t, "consul:///hello-service", target)
}

func TestServiceManager(t *testing.T) {
	// 注意：这个测试需要Consul服务器运行
	// 跳过测试如果没有Consul环境
	t.Skip("Skipping ServiceManager test - requires Consul server")

	// 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	require.NoError(t, err)

	// 创建服务管理器
	serviceManager := NewServiceManager(registry, registry)

	// 创建服务节点
	serviceNode := CreateGrpcService("test-service", "v1.0.0", "127.0.0.1", 18001)

	// 注册服务
	err = serviceManager.RegisterService(serviceNode)
	require.NoError(t, err)

	// 等待注册完成
	time.Sleep(1 * time.Second)

	// 获取服务实例
	services, err := serviceManager.GetServiceInstances("test-service")
	require.NoError(t, err)
	assert.NotEmpty(t, services)

	// 验证服务信息
	assert.Equal(t, "test-service", services[0].Name)
	assert.Equal(t, "v1.0.0", services[0].Version)

	// 注销服务
	err = serviceManager.UnregisterService()
	require.NoError(t, err)

	// 等待注销完成
	time.Sleep(1 * time.Second)

	// 验证服务已注销
	services, err = serviceManager.GetServiceInstances("test-service")
	require.NoError(t, err)
	assert.Empty(t, services)
}

func TestRegistry_GetService(t *testing.T) {
	// 注意：这个测试需要Consul服务器运行
	t.Skip("Skipping GetService test - requires Consul server")

	// 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	require.NoError(t, err)

	// 获取服务实例
	services, err := registry.GetService(context.Background(), "non-existent-service")
	require.NoError(t, err)
	assert.Empty(t, services)
}

func TestRegistry_Watch(t *testing.T) {
	// 注意：这个测试需要Consul服务器运行
	t.Skip("Skipping Watch test - requires Consul server")

	// 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 监听服务变化
	watchCh, err := registry.Watch(ctx, "test-service")
	require.NoError(t, err)

	// 等待变化
	select {
	case services := <-watchCh:
		t.Logf("Received service update: %d instances", len(services))
	case <-ctx.Done():
		t.Log("Watch context cancelled")
	}
}

func TestRegistry_RegisterAndUnregister(t *testing.T) {
	// 注意：这个测试需要Consul服务器运行
	t.Skip("Skipping RegisterAndUnregister test - requires Consul server")

	// 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	require.NoError(t, err)

	// 创建服务节点
	serviceNode := CreateGrpcService("test-service", "v1.0.0", "127.0.0.1", 18001)

	// 注册服务
	err = registry.Register(context.Background(), serviceNode)
	require.NoError(t, err)

	// 等待注册完成
	time.Sleep(1 * time.Second)

	// 验证服务已注册
	services, err := registry.GetService(context.Background(), "test-service")
	require.NoError(t, err)
	assert.NotEmpty(t, services)

	// 注销服务
	err = registry.Unregister(context.Background(), serviceNode)
	require.NoError(t, err)

	// 等待注销完成
	time.Sleep(1 * time.Second)

	// 验证服务已注销
	services, err = registry.GetService(context.Background(), "test-service")
	require.NoError(t, err)
	assert.Empty(t, services)
}
