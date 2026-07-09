package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/grpc/resolver"

	"github.com/wplbyx/modular/packages/core"
)

// gRPCResolver 使用服务发现后端（Consul/K8s）解析服务地址。
type gRPCResolver struct {
	target    resolver.Target
	cc        resolver.ClientConn
	discovery Discovery
	service   string
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewGRPCResolverBuilder 创建 gRPC 解析器构建器。
func NewGRPCResolverBuilder(discovery Discovery) resolver.Builder {
	return &grpcResolverBuilder{discovery: discovery}
}

type grpcResolverBuilder struct {
	discovery Discovery
}

func (b *grpcResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	ctx, cancel := context.WithCancel(context.Background())

	serviceName := extractServiceName(target)
	if serviceName == "" {
		cancel()
		return nil, fmt.Errorf("invalid service name in target: %s", target.URL.Host)
	}

	r := &gRPCResolver{
		target:    target,
		cc:        cc,
		discovery: b.discovery,
		service:   serviceName,
		ctx:       ctx,
		cancel:    cancel,
	}

	go r.watch()

	return r, nil
}

func (b *grpcResolverBuilder) Scheme() string {
	return "consul"
}

func (r *gRPCResolver) ResolveNow(resolver.ResolveNowOptions) {
	// 使用阻塞式 watch，ResolveNow 不需要额外操作
}

func (r *gRPCResolver) Close() {
	r.cancel()
}

// watch 使用 Discovery.Watch 的阻塞式通道监听服务变化。
func (r *gRPCResolver) watch() {
	ch, err := r.discovery.Watch(r.ctx, r.service)
	if err != nil {
		r.cc.ReportError(err)
		return
	}

	for {
		select {
		case <-r.ctx.Done():
			return
		case nodes, ok := <-ch:
			if !ok {
				return
			}
			r.update(nodes)
		}
	}
}

// update 将 ServiceNode 列表转换为 gRPC resolver.Address 并更新连接状态。
func (r *gRPCResolver) update(nodes []*core.ServiceNode) {
	var addresses []resolver.Address
	for _, svc := range nodes {
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

// RegisterConsulResolver 注册 Consul 解析器。
func RegisterConsulResolver(discovery Discovery) {
	resolver.Register(NewGRPCResolverBuilder(discovery))
}

// BuildConsulTarget 构建 Consul 目标地址。
func BuildConsulTarget(serviceName string) string {
	return fmt.Sprintf("consul:///%s", serviceName)
}
