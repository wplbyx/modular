package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/resolver"
)

// gRPC服务发现解析器
type grpcResolver struct {
	target   resolver.Target
	cc       resolver.ClientConn
	registry *Registry
	service  string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewGRPCResolverBuilder 创建gRPC解析器构建器
func NewGRPCResolverBuilder(registry *Registry) resolver.Builder {
	return &grpcResolverBuilder{registry: registry}
}

type grpcResolverBuilder struct {
	registry *Registry
}

// Build 构建解析器
func (b *grpcResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 从target中提取服务名称
	serviceName := extractServiceName(target)
	if serviceName == "" {
		cancel()
		return nil, fmt.Errorf("invalid service name in target: %s", target.URL.Host)
	}

	r := &grpcResolver{
		target:   target,
		cc:       cc,
		registry: b.registry,
		service:  serviceName,
		ctx:      ctx,
		cancel:   cancel,
	}

	r.wg.Add(1)
	go r.watch()

	return r, nil
}

// Scheme 返回解析器支持的协议
func (b *grpcResolverBuilder) Scheme() string {
	return "consul"
}

// ResolveNow 立即解析
func (r *grpcResolver) ResolveNow(resolver.ResolveNowOptions) {
	r.resolve()
}

// Close 关闭解析器
func (r *grpcResolver) Close() {
	r.cancel()
	r.wg.Wait()
}

// watch 监听服务变化
func (r *grpcResolver) watch() {
	defer r.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// 立即执行一次解析
	r.resolve()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.resolve()
		}
	}
}

// resolve 解析服务地址
func (r *grpcResolver) resolve() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()

	services, err := r.registry.GetService(ctx, r.service)
	if err != nil {
		r.cc.ReportError(err)
		return
	}

	var addresses []resolver.Address
	for _, service := range services {
		// 解析grpc端点
		for _, endpoint := range service.Endpoints {
			if strings.HasPrefix(endpoint, "grpc://") {
				addr := strings.TrimPrefix(endpoint, "grpc://")
				addresses = append(addresses, resolver.Address{
					Addr:       addr,
					ServerName: service.Name,
					Metadata:   createMetadata(service),
				})
			}
		}
	}

	if len(addresses) == 0 {
		r.cc.ReportError(fmt.Errorf("no available addresses for service: %s", r.service))
		return
	}

	r.cc.UpdateState(resolver.State{
		Addresses: addresses,
	})
}

// extractServiceName 从target中提取服务名称
func extractServiceName(target resolver.Target) string {
	// target.URL.Host 格式: "consul://service-name"
	// 或者直接是服务名称
	if target.URL.Host != "" {
		return target.URL.Host
	}
	return target.URL.Path
}

// createMetadata 创建gRPC地址元数据
func createMetadata(service *ServiceNode) *json.RawMessage {
	metadata := make(map[string]interface{})
	metadata["id"] = service.ID
	metadata["version"] = service.Version
	metadata["name"] = service.Name

	if service.Metadata != nil {
		for k, v := range service.Metadata {
			metadata[k] = v
		}
	}

	data, _ := json.Marshal(metadata)
	var raw json.RawMessage = data
	return &raw
}

// RegisterConsulResolver 注册Consul解析器
func RegisterConsulResolver(registry *Registry) {
	resolver.Register(NewGRPCResolverBuilder(registry))
}

// BuildConsulTarget 构建Consul目标地址
func BuildConsulTarget(serviceName string) string {
	return fmt.Sprintf("consul:///%s", serviceName)
}
