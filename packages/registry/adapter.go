package registry

import "context"

// Registrar 服务注册接口
type Registrar interface {
	Register(ctx context.Context, service *ServiceNode) error                   // 注册
	Unregister(ctx context.Context, service *ServiceNode) error                 // 注销
	GetService(ctx context.Context, serviceName string) ([]*ServiceNode, error) // 获取服务实例
	Subscribe(ctx context.Context, serviceName string) error                    // 监听服务实例变化
}

// Discovery 服务发现接口
type Discovery interface {
	GetService(ctx context.Context, serviceName string) ([]*ServiceNode, error)   // 获取服务实例
	Watch(ctx context.Context, serviceName string) (<-chan []*ServiceNode, error) // 监听服务变化
}

// ServiceNode 服务节点对象
type ServiceNode struct {
	ID        string            `json:"id"`        // 服务id，全局唯一
	Name      string            `json:"name"`      // 服务名称，同服务名称相同
	Port      int               `json:"port"`      //
	Address   string            `json:"address"`   //
	Version   string            `json:"version"`   // 服务版本
	Metadata  map[string]string `json:"metadata"`  // 服务元数据
	Endpoints []string          `json:"endpoints"` // http://127.0.0.1:8080 , grpc://127.0.0.1:9000
}
