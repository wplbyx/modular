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

// Registry 瀹炵幇浜?Registrar 鍜?Discovery 鎺ュ彛
type Registry struct {
	client  *api.Client
	address string
}

var _ Registrar = (*Registry)(nil)
var _ Discovery = (*Registry)(nil)

// NewConsulRegistry 鍒涘缓涓€涓柊鐨?Consul 娉ㄥ唽涓績瀹炰緥
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

// Register 娉ㄥ唽鏈嶅姟鍒?Consul銆?// 涓€涓?ServiceNode 鍙兘鍖呭惈澶氫釜 Transport锛堝鍚屾椂鏆撮湶 HTTP 鍜?gRPC锛夛紝
// 姣忎釜 Transport 娉ㄥ唽涓轰竴鏉＄嫭绔嬬殑 Consul 鏈嶅姟璁板綍锛孖D 浠?transport 鍚庣紑鍖哄垎銆?
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

		opts := api.ServiceRegisterOpts{}.WithContext(ctx)
		if err := c.client.Agent().ServiceRegisterOpts(reg, opts); err != nil {
			return fmt.Errorf("register transport %s: %w", t.Protocol, err)
		}
	}

	return nil
}

// Unregister 浠?Consul 娉ㄩ攢鏈嶅姟鐨勬墍鏈?Transport 璁板綍
func (c *Registry) Unregister(ctx context.Context, node *core.ServiceNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("service node or service ID cannot be nil")
	}

	var errs error
	for _, t := range node.Transports {
		id := transportID(node.ID, t.Protocol)
		if err := c.client.Agent().ServiceDeregisterOpts(id, (&api.QueryOptions{}).WithContext(ctx)); err != nil {
			errs = errors.Join(errs, fmt.Errorf("deregister transport %s: %w", t.Protocol, err))
		}
	}
	return errs
}

// GetService 浠?Consul 鑾峰彇鏈嶅姟瀹炰緥鍒楄〃
func (c *Registry) GetService(ctx context.Context, serviceName string) ([]*core.ServiceNode, error) {
	services, _, err := c.client.Health().Service(serviceName, "", true, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, err
	}
	return consulToServiceNodes(services), nil
}

// Watch 鐩戞帶鏈嶅姟瀹炰緥鍙樺寲锛岃繑鍥炲彉鍖栭€氶亾
func (c *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*core.ServiceNode, error) {
	ch := make(chan []*core.ServiceNode, 10)

	go func() {
		defer close(ch)

		params := (&api.QueryOptions{
			WaitIndex: 0,
			WaitTime:  5 * time.Minute,
		}).WithContext(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				services, meta, err := c.client.Health().Service(serviceName, "", true, params)
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
					continue
				}
				if meta != nil {
					params.WaitIndex = meta.LastIndex
				}
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

// transportID 涓哄崟涓?Transport 鐢熸垚 Consul 鏈嶅姟 ID
func transportID(baseID, protocol string) string {
	return fmt.Sprintf("%s-%s", baseID, protocol)
}

// buildMeta 鍚堝苟 node.Metadata 涓?transport 绾у埆鐨?health_path
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

// consulToServiceNodes 灏?Consul 鏈嶅姟鏉＄洰杞崲鍥?ServiceNode
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
			Name:       entry.Service.Service,
			Version:    version,
			ID:         entry.Service.ID,
			Transports: []core.Transport{t},
			Metadata:   entry.Service.Meta,
		})
	}
	return nodes
}

// consulHealthCheck 鏍规嵁 transport 閰嶇疆鏋勫缓 Consul 鍋ュ悍妫€鏌?
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
