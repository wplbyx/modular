package registry

import (
	"context"
	"github.com/wplbyx/modular/packages/core"
)

// Registrar 服务注册接口
type Registrar interface {
	Register(ctx context.Context, node *core.ServiceNode) error   // 注册
	Unregister(ctx context.Context, node *core.ServiceNode) error // 注销
}

// Discovery 服务发现接口
type Discovery interface {
	GetService(ctx context.Context, serviceName string) ([]*core.ServiceNode, error)   // 获取服务实例
	Watch(ctx context.Context, serviceName string) (<-chan []*core.ServiceNode, error) // 监控服务变化
}
