package app

import (
	"modular/packages/core"
	"modular/packages/registry"
)

// WithServiceNode 注入服务节点元数据（一个 app 对应一个 node）。
// node 从启动配置构建，Application 只负责在 registrar 之间传值。
func WithServiceNode(node *core.ServiceNode) Option {
	return func(a *Application) {
		a.node = node
	}
}

// WithRegistrar 注入服务注册中心
func WithRegistrar(reg registry.Registrar) Option {
	return func(a *Application) {
		a.registrar = reg
	}
}

// WithResource 注入基础设施资源（DB、Redis、Telemetry 等）。
// 所有 Resource 在 Endpoint 启动前按 FIFO 调用 Setup，
// 在 Endpoint 停止后按 LIFO 调用 Close。
func WithResource(r core.Resource) Option {
	return func(a *Application) {
		if r != nil {
			a.resources = append(a.resources, r)
		}
	}
}

// WithEndpoint injects an application-managed transport entrypoint.
func WithEndpoint(endpoint core.Endpoint) Option {
	return func(a *Application) {
		if endpoint != nil {
			a.endpoints = append(a.endpoints, endpoint)
		}
	}
}
