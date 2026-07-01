package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/resolver"

	"modular/packages/core"
)

// gRPC 服务发现解析器
type grpcResolver struct {
	target   resolver.Target
	cc       resolver.ClientConn
	registry *Registry
	service  string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewGRPCResolverBuilder 创建 gRPC 解析器构建器
func NewGRPCResolverBuilder(registry *Registry) resolver.Builder {
	return &grpcResolverBuilder{registry: registry}
}

type grpcResolverBuilder struct {
	registry *Registry
}

func (b *grpcResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	ctx, cancel := context.WithCancel(context.Background())

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

func (b *grpcResolverBuilder) Scheme() string {
	return "consul"
}

func (r *grpcResolver) ResolveNow(resolver.ResolveNowOptions) {
	r.resolve()
}

func (r *grpcResolver) Close() {
	r.cancel()
	r.wg.Wait()
}

func (r *grpcResolver) watch() {
	defer r.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

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

func (r *grpcResolver) resolve() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()

	services, err := r.registry.GetService(ctx, r.service)
	if err != nil {
		r.cc.ReportError(err)
		return
	}

	var addresses []resolver.Address
	for _, svc := range services {
		for _, t := range svc.Transports {
			if strings.EqualFold(t.Protocol, "grpc") {
				addresses = append(addresses, resolver.Address{
					Addr:       fmt.Sprintf("%s:%d", t.Address, t.Port),
					ServerName: svc.Name,
					Metadata:   createMetadata(svc),
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

func extractServiceName(target resolver.Target) string {
	if target.URL.Host != "" {
		return target.URL.Host
	}
	return target.URL.Path
}

func createMetadata(svc *core.ServiceNode) *json.RawMessage {
	metadata := make(map[string]interface{})
	metadata["id"] = svc.ID
	metadata["version"] = svc.Version
	metadata["name"] = svc.Name

	for k, v := range svc.Metadata {
		metadata[k] = v
	}

	data, _ := json.Marshal(metadata)
	raw := json.RawMessage(data)
	return &raw
}

// RegisterConsulResolver 注册 Consul 解析器
func RegisterConsulResolver(registry *Registry) {
	resolver.Register(NewGRPCResolverBuilder(registry))
}

// BuildConsulTarget 构建 Consul 目标地址
func BuildConsulTarget(serviceName string) string {
	return fmt.Sprintf("consul:///%s", serviceName)
}
