package registry

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
)

// ServiceManager 服务管理器
type ServiceManager struct {
	registry  Registrar
	discovery Discovery
	localNode *ServiceNode
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewServiceManager 创建服务管理器
func NewServiceManager(registry Registrar, discovery Discovery) *ServiceManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServiceManager{
		registry:  registry,
		discovery: discovery,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// RegisterService 注册服务
func (sm *ServiceManager) RegisterService(service *ServiceNode) error {
	if service == nil {
		return fmt.Errorf("service node cannot be nil")
	}

	sm.localNode = service

	// 注册服务到注册中心
	if err := sm.registry.Register(sm.ctx, service); err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	// 启动健康检查
	go sm.healthCheck()

	return nil
}

// UnregisterService 注销服务
func (sm *ServiceManager) UnregisterService() error {
	if sm.localNode == nil {
		return nil
	}

	sm.cancel()
	return sm.registry.Unregister(context.Background(), sm.localNode)
}

// GetServiceClient 获取gRPC服务客户端
func (sm *ServiceManager) GetServiceClient(serviceName string) (*grpc.ClientConn, error) {
	// 构建Consul目标地址
	target := BuildConsulTarget(serviceName)

	// 创建gRPC连接
	conn, err := grpc.Dial(target,
		grpc.WithInsecure(), // 生产环境应该使用TLS
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy": "round_robin"}`),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to service %s: %w", serviceName, err)
	}

	return conn, nil
}

// WatchService 监听服务变化
func (sm *ServiceManager) WatchService(serviceName string) (<-chan []*ServiceNode, error) {
	return sm.discovery.Watch(sm.ctx, serviceName)
}

// GetServiceInstances 获取服务实例
func (sm *ServiceManager) GetServiceInstances(serviceName string) ([]*ServiceNode, error) {
	return sm.discovery.GetService(sm.ctx, serviceName)
}

// healthCheck 健康检查
func (sm *ServiceManager) healthCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-ticker.C:
			// 这里可以实现自定义的健康检查逻辑
			// 例如检查服务状态、资源使用情况等
			sm.performHealthCheck()
		}
	}
}

// performHealthCheck 执行健康检查
func (sm *ServiceManager) performHealthCheck() {
	// 这里可以实现具体的健康检查逻辑
	// 例如：检查数据库连接、缓存连接、队列状态等
	// 如果健康检查失败，可以选择自动注销服务或发送告警
}

// Close 关闭服务管理器
func (sm *ServiceManager) Close() error {
	return sm.UnregisterService()
}

// ServiceBuilder 服务构建器
type ServiceBuilder struct {
	service *ServiceNode
}

// NewServiceBuilder 创建服务构建器
func NewServiceBuilder() *ServiceBuilder {
	return &ServiceBuilder{
		service: &ServiceNode{
			Metadata: make(map[string]string),
		},
	}
}

// WithID 设置服务ID
func (sb *ServiceBuilder) WithID(id string) *ServiceBuilder {
	sb.service.ID = id
	return sb
}

// WithName 设置服务名称
func (sb *ServiceBuilder) WithName(name string) *ServiceBuilder {
	sb.service.Name = name
	return sb
}

// WithVersion 设置服务版本
func (sb *ServiceBuilder) WithVersion(version string) *ServiceBuilder {
	sb.service.Version = version
	return sb
}

// WithEndpoint 添加服务端点
func (sb *ServiceBuilder) WithEndpoint(endpoint string) *ServiceBuilder {
	sb.service.Endpoints = append(sb.service.Endpoints, endpoint)
	return sb
}

// WithMetadata 添加元数据
func (sb *ServiceBuilder) WithMetadata(key, value string) *ServiceBuilder {
	if sb.service.Metadata == nil {
		sb.service.Metadata = make(map[string]string)
	}
	sb.service.Metadata[key] = value
	return sb
}

// Build 构建服务节点
func (sb *ServiceBuilder) Build() *ServiceNode {
	return sb.service
}

// CreateGrpcService 创建gRPC服务节点
func CreateGrpcService(name, version, host string, port int) *ServiceNode {
	return NewServiceBuilder().
		WithName(name).
		WithVersion(version).
		WithEndpoint(fmt.Sprintf("grpc://%s:%d", host, port)).
		WithMetadata("protocol", "grpc").
		Build()
}

// CreateHttpService 创建HTTP服务节点
func CreateHttpService(name, version, host string, port int) *ServiceNode {
	return NewServiceBuilder().
		WithName(name).
		WithVersion(version).
		WithEndpoint(fmt.Sprintf("http://%s:%d", host, port)).
		WithMetadata("protocol", "http").
		WithMetadata("health_path", "/check").
		Build()
}
