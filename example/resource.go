package main

import (
	"context"
	"fmt"
	"sync"
)

// mockResource 是一个自包含的 core.Resource 示例实现，
// 仅用日志演示 Application 对 Resource 的 FIFO Setup / LIFO Close 编排。
// 实际项目里这里通常是对接真实中间件的适配器（DB、Redis、Storage、Telemetry）。
type mockResource struct {
	name  string
	mu    sync.Mutex
	ready bool
}

func newMockResource(name string) *mockResource {
	return &mockResource{name: name}
}

func (r *mockResource) Name() string { return r.name }

func (r *mockResource) Setup(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = true
	fmt.Printf("    [resource] %-10s Setup 完成\n", r.name)
	return nil
}

func (r *mockResource) Close(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = false
	fmt.Printf("    [resource] %-10s Close 完成\n", r.name)
	return nil
}
