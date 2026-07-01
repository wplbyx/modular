package registry

import (
	"context"
	"fmt"
	"log"
	"time"

	"modular/packages/core"
)

// ExampleRegistrar 服务端示例：从配置构建 ServiceNode，注册到 Consul
func ExampleRegistrar() {
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	if err != nil {
		log.Fatalf("Failed to create registry: %v", err)
	}

	// 从配置构建 ServiceNode（一个实例可以包含多个 Transport）
	node := core.NewServiceNode(
		core.Identity{Name: "hello-service", Version: "v1.0.0"},
		core.Transport{Protocol: "grpc", Address: "127.0.0.1", Port: 18001},
	)

	ctx := context.Background()
	if err := registry.Register(ctx, node); err != nil {
		log.Fatalf("Failed to register: %v", err)
	}
	defer func() { _ = registry.Unregister(ctx, node) }()

	// 启动 gRPC 服务...
	fmt.Println("registered:", node.ID)
}

// ExampleDiscovery 客户端示例：使用 Discovery 接口发现服务
func ExampleDiscovery() {
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	if err != nil {
		log.Fatalf("Failed to create registry: %v", err)
	}

	RegisterConsulResolver(registry)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	services, err := registry.GetService(ctx, "hello-service")
	if err != nil {
		log.Printf("Failed to get services: %v", err)
		return
	}

	for _, svc := range services {
		fmt.Printf("ID: %s, Version: %s, Endpoints: %v\n", svc.ID, svc.Version, svc.Endpoints())
	}
}
