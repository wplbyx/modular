package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"

	"modular/packages/core"
)

// Registry 实现了 Registrar 和 Discovery 接口
type Registry struct {
	client  *api.Client
	address string
}

var _ Registrar = (*Registry)(nil)
var _ Discovery = (*Registry)(nil)

// NewConsulRegistry 创建一个新的 Consul 注册中心实例
func NewConsulRegistry(addr string) (*Registry, error) {
	config := api.DefaultConfig()
	config.Address = addr

	client, err := api.NewClient(config)
	if err != nil {
		return nil, err
	}

	return &Registry{
		client:  client,
		address: addr,
	}, nil
}

// Register 注册服务到 Consul。
// 一个 ServiceNode 可能包含多个 Transport（如同时暴露 HTTP 和 gRPC），
// 每个 Transport 注册为一条独立的 Consul 服务记录，ID 以 transport 后缀区分。
func (c *Registry) Register(ctx context.Context, node *core.ServiceNode) error {
	if node == nil {
		return fmt.Errorf("service node cannot be nil")
	}

	for _, t := range node.Transports {
		reg := &api.AgentServiceRegistration{
			ID:      transportID(node.ID, t.Protocol),
			Name:    node.Name,
			Address: t.Address,
			Port:    t.Port,
			Meta:    buildMeta(node, t),
			Tags: []string{
				fmt.Sprintf("version=%s", node.Version),
				fmt.Sprintf("protocol=%s", t.Protocol),
			},
			Check: consulHealthCheck(t),
		}

		if err := c.client.Agent().ServiceRegister(reg); err != nil {
			return fmt.Errorf("register transport %s: %w", t.Protocol, err)
		}
	}

	return nil
}

// Unregister 从 Consul 注销服务的所有 Transport 记录
func (c *Registry) Unregister(ctx context.Context, node *core.ServiceNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("service node or service ID cannot be nil")
	}

	var errs error
	for _, t := range node.Transports {
		id := transportID(node.ID, t.Protocol)
		if err := c.client.Agent().ServiceDeregister(id); err != nil {
			errs = errors.Join(errs, fmt.Errorf("deregister transport %s: %w", t.Protocol, err))
		}
	}
	return errs
}

// GetService 从 Consul 获取服务实例列表
func (c *Registry) GetService(ctx context.Context, serviceName string) ([]*core.ServiceNode, error) {
	services, _, err := c.client.Health().Service(serviceName, "", true, nil)
	if err != nil {
		return nil, err
	}
	return consulToServiceNodes(services), nil
}

// Watch 监控服务实例变化，返回变化通道
func (c *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*core.ServiceNode, error) {
	ch := make(chan []*core.ServiceNode, 10)

	go func() {
		defer close(ch)

		params := &api.QueryOptions{
			WaitIndex: 0,
			WaitTime:  5 * time.Minute,
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
				services, meta, err := c.client.Health().Service(serviceName, "", true, params)
				if err != nil {
					continue
				}
				params.WaitIndex = meta.LastIndex
				nodes := consulToServiceNodes(services)
				select {
				case ch <- nodes:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// transportID 为单个 Transport 生成 Consul 服务 ID
func transportID(baseID, protocol string) string {
	return fmt.Sprintf("%s-%s", baseID, protocol)
}

// buildMeta 合并 node.Metadata 与 transport 级别的 health_path
func buildMeta(node *core.ServiceNode, t core.Transport) map[string]string {
	meta := make(map[string]string)
	for k, v := range node.Metadata {
		meta[k] = v
	}
	meta["protocol"] = t.Protocol
	if t.HealthPath != "" {
		meta["health_path"] = t.HealthPath
	}
	return meta
}

// consulToServiceNodes 将 Consul 服务条目转换回 ServiceNode
func consulToServiceNodes(services []*api.ServiceEntry) []*core.ServiceNode {
	var nodes []*core.ServiceNode
	for _, entry := range services {
		version := ""
		protocol := entry.Service.Meta["protocol"]
		for _, tag := range entry.Service.Tags {
			if strings.HasPrefix(tag, "version=") {
				version = strings.TrimPrefix(tag, "version=")
			}
		}

		t := core.Transport{
			Protocol: protocol,
			Address:  entry.Service.Address,
			Port:     entry.Service.Port,
		}
		if hp, ok := entry.Service.Meta["health_path"]; ok {
			t.HealthPath = hp
		}

		nodes = append(nodes, &core.ServiceNode{
			Identity: core.Identity{
				Name:    entry.Service.Service,
				Version: version,
			},
			ID:         entry.Service.ID,
			Transports: []core.Transport{t},
			Metadata:   entry.Service.Meta,
		})
	}
	return nodes
}

// consulHealthCheck 根据 transport 配置构建 Consul 健康检查
func consulHealthCheck(t core.Transport) *api.AgentServiceCheck {
	check := &api.AgentServiceCheck{
		Timeout:                        "5s",
		Interval:                       "10s",
		DeregisterCriticalServiceAfter: "30s",
	}

	switch strings.ToLower(t.Protocol) {
	case "http", "https":
		path := t.HealthPath
		if path == "" {
			path = "/health"
		}
		check.HTTP = fmt.Sprintf("%s://%s:%d%s", t.Protocol, t.Address, t.Port, path)
	case "grpc":
		check.GRPC = fmt.Sprintf("%s:%d", t.Address, t.Port)
	default:
		check.TCP = fmt.Sprintf("%s:%d", t.Address, t.Port)
	}

	return check
}