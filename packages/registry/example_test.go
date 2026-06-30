package registry

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ExampleServer 服务端示例
func ExampleServer() {
	// 1. 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	if err != nil {
		log.Fatalf("Failed to create registry: %v", err)
	}

	// 2. 注册gRPC解析器
	RegisterConsulResolver(registry)

	// 3. 创建服务管理器
	serviceManager := NewServiceManager(registry, registry)

	// 4. 创建服务节点
	serviceNode := CreateGrpcService(
		"hello-service",
		"v1.0.0",
		"127.0.0.1",
		18001,
	)

	// 5. 注册服务
	if err := serviceManager.RegisterService(serviceNode); err != nil {
		log.Fatalf("Failed to register service: %v", err)
	}

	// 6. 启动gRPC服务器
	// ... 这里启动实际的gRPC服务 ...

	// 7. 等待退出信号
	<-serviceManager.ctx.Done()

	// 8. 注销服务
	if err := serviceManager.UnregisterService(); err != nil {
		log.Printf("Failed to unregister service: %v", err)
	}
}

// ExampleClient 客户端示例
func ExampleClient() {
	// 1. 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	if err != nil {
		log.Fatalf("Failed to create registry: %v", err)
	}

	// 2. 注册gRPC解析器
	RegisterConsulResolver(registry)

	// 3. 创建服务管理器
	serviceManager := NewServiceManager(registry, registry)

	// 4. 获取服务连接
	conn, err := serviceManager.GetServiceClient("hello-service")
	if err != nil {
		log.Fatalf("Failed to get service client: %v", err)
	}
	defer conn.Close()

	// 5. 使用gRPC客户端
	// client := hello_service.NewHelloServiceClient(conn)
	// response, err := client.SayHello(context.Background(), &hello_info.SayHelloRequest{
	//     Question: "Hello, World!",
	// })
	// if err != nil {
	//     log.Fatalf("Failed to call SayHello: %v", err)
	// }
	// fmt.Printf("Response: %s\n", response.Answer)
}

// ExampleServiceManager_WatchService 监听服务变化示例
func ExampleServiceManager_WatchService() {
	// 1. 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	if err != nil {
		log.Fatalf("Failed to create registry: %v", err)
	}

	// 2. 创建服务管理器
	serviceManager := NewServiceManager(registry, registry)

	// 3. 监听服务变化
	watchCh, err := serviceManager.WatchService("hello-service")
	if err != nil {
		log.Fatalf("Failed to watch service: %v", err)
	}

	// 4. 处理服务变化
	for services := range watchCh {
		fmt.Printf("Service instances changed. Current instances:\n")
		for _, service := range services {
			fmt.Printf("  - ID: %s, Version: %s, Endpoints: %v\n",
				service.ID, service.Version, service.Endpoints)
		}
	}
}

// ExampleBuildConsulTarget 手动创建客户端示例
func ExampleBuildConsulTarget() {
	// 1. 创建Consul注册中心
	registry, err := NewConsulRegistry("127.0.0.1:8500")
	if err != nil {
		log.Fatalf("Failed to create registry: %v", err)
	}

	// 2. 注册gRPC解析器
	RegisterConsulResolver(registry)

	// 3. 手动创建gRPC连接
	target := BuildConsulTarget("hello-service")
	conn, err := grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy": "round_robin"}`),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// 4. 使用连接
	fmt.Printf("Connected to service: %s\n", target)

	// 5. 获取服务实例
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	services, err := registry.GetService(ctx, "hello-service")
	if err != nil {
		log.Printf("Failed to get services: %v", err)
		return
	}

	fmt.Printf("Available service instances:\n")
	for _, service := range services {
		fmt.Printf("  - ID: %s, Version: %s, Endpoints: %v\n",
			service.ID, service.Version, service.Endpoints)
	}
}
