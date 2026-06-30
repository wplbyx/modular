package registry

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
)

type RegistryOptions func()

// Registry 实现了Registrar和Discovery接口
type Registry struct {
	client  *api.Client // consul 客户端连接，连接到consul服务器
	address string      // consul 地址
}

var _ Registrar = (*Registry)(nil)

var _ Discovery = (*Registry)(nil)

// NewConsulRegistry 创建一个新的Consul注册中心实例
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

// =================

// Register 客户端调用http,主动注册服务到Consul
func (c *Registry) Register(ctx context.Context, service *ServiceNode) error {
	if service == nil {
		return fmt.Errorf("service node cannot be nil")
	}
	if len(service.Endpoints) == 0 {
		return fmt.Errorf("service node endpoints cannot be empty")
	}

	protocol, address, port, err := parseEndpoint(service.Endpoints[0])
	if err != nil {
		return err
	}
	if service.Address != "" {
		address = service.Address
	}
	if service.Port != 0 {
		port = service.Port
	}

	meta := make(map[string]string, len(service.Metadata)+1)
	for k, v := range service.Metadata {
		meta[k] = v
	}
	if meta["protocol"] == "" {
		meta["protocol"] = protocol
	}

	reg := &api.AgentServiceRegistration{
		ID:      service.ID,
		Name:    service.Name,
		Address: address,
		Port:    port,
		Meta:    meta,
		Tags: []string{
			fmt.Sprintf("version=%s", service.Version),
		},
		Check: consulHealthCheck(protocol, address, port, meta),
	}

	return c.client.Agent().ServiceRegister(reg)
}

// Unregister 客户端调用http,主动从Consul注销服务
func (c *Registry) Unregister(ctx context.Context, service *ServiceNode) error {
	if service == nil || service.ID == "" {
		return fmt.Errorf("service node or service ID cannot be nil")
	}

	return c.client.Agent().ServiceDeregister(service.ID)
}

// GetService 从Consul获取服务实例列表
func (c *Registry) GetService(ctx context.Context, serviceName string) ([]*ServiceNode, error) {
	services, _, err := c.client.Health().Service(serviceName, "", true, nil)
	if err != nil {
		return nil, err
	}

	var nodes []*ServiceNode
	for _, service := range services {
		// 提取版本信息
		version := ""
		for _, tag := range service.Service.Tags {
			if strings.HasPrefix(tag, "version=") {
				version = strings.TrimPrefix(tag, "version=")
				break
			}
		}

		node := &ServiceNode{
			ID:       service.Service.ID,
			Name:     service.Service.Service,
			Version:  version,
			Metadata: service.Service.Meta,
			Endpoints: []string{
				fmt.Sprintf("%s://%s:%d", getProtocol(service.Service.Port, service.Service.Meta), service.Service.Address, service.Service.Port),
			},
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// Subscribe 监听服务实例变化（没什么大的作用）
func (c *Registry) Subscribe(ctx context.Context, serviceName string) error {
	// 创建一个新的查询参数，设置WaitIndex为0将获取最新的索引
	params := &api.QueryOptions{
		WaitIndex: 0,
		WaitTime:  5 * time.Minute,
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 查询服务并等待变化
			services, meta, err := c.client.Health().Service(serviceName, "", true, params)
			if err != nil {
				return err
			}

			// 更新WaitIndex以监听后续变化
			params.WaitIndex = meta.LastIndex

			// 这里可以处理服务变化，例如发送事件或更新本地缓存
			// 实际使用中应该通过回调函数或通道将变化通知给调用者
			fmt.Printf("Service %s changed, new instances: %d\n", serviceName, len(services))
		}
	}
}

// Watch 监听服务实例变化，返回变化通道
func (c *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*ServiceNode, error) {
	ch := make(chan []*ServiceNode, 10)

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
				// 查询服务并等待变化
				services, meta, err := c.client.Health().Service(serviceName, "", true, params)
				if err != nil {
					continue
				}

				// 更新WaitIndex以监听后续变化
				params.WaitIndex = meta.LastIndex

				// 转换为ServiceNode
				nodes := c.convertToServiceNodes(services)

				// 发送到通道
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

// convertToServiceNodes 将Consul服务转换为ServiceNode
func (c *Registry) convertToServiceNodes(services []*api.ServiceEntry) []*ServiceNode {
	var nodes []*ServiceNode
	for _, service := range services {
		// 提取版本信息
		version := ""
		for _, tag := range service.Service.Tags {
			if strings.HasPrefix(tag, "version=") {
				version = strings.TrimPrefix(tag, "version=")
				break
			}
		}

		node := &ServiceNode{
			ID:       service.Service.ID,
			Name:     service.Service.Service,
			Version:  version,
			Metadata: service.Service.Meta,
			Endpoints: []string{
				fmt.Sprintf("%s://%s:%d", getProtocol(service.Service.Port, service.Service.Meta), service.Service.Address, service.Service.Port),
			},
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// 解析服务端点，提取地址和端口
func parseEndpoint(endpoint string) (string, string, int, error) {
	parts := strings.SplitN(endpoint, "://", 2)
	if len(parts) != 2 {
		return "", "", 0, fmt.Errorf("invalid endpoint format: %s", endpoint)
	}

	host, port, err := net.SplitHostPort(parts[1])
	if err != nil {
		return "", "", 0, err
	}

	portInt := 0
	if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
		return "", "", 0, fmt.Errorf("invalid endpoint port %q: %w", port, err)
	}

	return strings.ToLower(parts[0]), host, portInt, nil
}

func getProtocol(port int, metadata ...map[string]string) string {
	if len(metadata) > 0 && metadata[0] != nil {
		if protocol := metadata[0]["protocol"]; protocol != "" {
			return protocol
		}
	}
	switch port {
	case 80, 8080:
		return "http"
	case 443:
		return "https"
	case 50051:
		return "grpc"
	default:
		return "http"
	}
}

func consulHealthCheck(protocol, address string, port int, metadata map[string]string) *api.AgentServiceCheck {
	check := &api.AgentServiceCheck{
		Timeout:                        defaultMeta(metadata, "health_timeout", "5s"),
		Interval:                       defaultMeta(metadata, "health_interval", "10s"),
		DeregisterCriticalServiceAfter: defaultMeta(metadata, "deregister_after", "30s"),
	}

	switch strings.ToLower(protocol) {
	case "http", "https":
		path := defaultMeta(metadata, "health_path", "/check")
		check.HTTP = fmt.Sprintf("%s://%s:%d%s", protocol, address, port, path)
	case "grpc":
		check.GRPC = fmt.Sprintf("%s:%d", address, port)
	case "tcp", "mqtt", "kafka":
		check.TCP = fmt.Sprintf("%s:%d", address, port)
	default:
		check.TCP = fmt.Sprintf("%s:%d", address, port)
	}

	return check
}

func defaultMeta(metadata map[string]string, key, fallback string) string {
	if metadata == nil {
		return fallback
	}
	if value := metadata[key]; value != "" {
		return value
	}
	return fallback
}
