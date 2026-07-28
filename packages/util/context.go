package util

import (
	"context"
	"sort"
	"time"

	grpcmetadata "google.golang.org/grpc/metadata"

	modularmetadata "github.com/wplbyx/modular/packages/metadata"
)

// DetachOption 配置 DetachContext 的行为。
type DetachOption func(*detachConfig)

type detachConfig struct {
	timeout    time.Duration
	wantCancel bool
}

// WithTimeout 在脱离父 ctx 之后，给新 ctx 设置一个独立的全新超时。
// 适合延时任务，避免脱离后协程因无 deadline 而无限期挂起。
func WithTimeout(d time.Duration) DetachOption {
	return func(c *detachConfig) { c.timeout = d }
}

// WithCancel 表示需要一个可主动调用的 cancel 句柄（使用即开启）。
// 不带超时时，脱离后的 ctx 仅在调用返回的 cancel 时取消；
// 与 WithTimeout 组合时等价于超时本身提供的 cancel（两者返回的是同一个句柄）。
func WithCancel() DetachOption {
	return func(c *detachConfig) { c.wantCancel = true }
}

// DetachContext 复制父 ctx 的全部 Value 链，但切断其取消信号与 deadline，
// 用于异步 / 延时任务：请求响应后原 ctx 会被取消，但后台协程仍需保持之前的会话内容。
// 实现基于 Go 1.21+ 的 context.WithoutCancel。
//
// 始终返回 (ctx, cancel)：cancel 永远非 nil 且可安全调用——
// 使用了 WithTimeout / WithCancel 时是真实句柄，纯脱离时为 no-op。
// 这样调用方可以无条件 `defer cancel()` 释放资源。
//
// 用法：
//
//	ctx, _ := util.DetachContext(reqCtx)                                        // 纯脱离（fire-and-forget）
//	ctx, cancel := util.DetachContext(reqCtx, util.WithTimeout(10*time.Second)) // 脱离 + 独立超时
//	ctx, cancel := util.DetachContext(reqCtx, util.WithCancel())                // 脱离 + 手动取消
//	ctx, cancel := util.DetachContext(reqCtx, util.WithTimeout(10*time.Second), util.WithCancel())
func DetachContext(parent context.Context, opts ...DetachOption) (context.Context, context.CancelFunc) {
	cfg := detachConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	// 1. 脱离：保留 Value 链，去掉 Done/Err/Deadline。
	ctx := context.WithoutCancel(parent)
	// 2. 可选：挂载新的超时 / 可主动取消句柄。cancel 始终通过返回值逃逸，天然满足 go vet 的 lostcancel 检查。
	if cfg.timeout > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, cfg.timeout)
		return ctx, c
	}
	if cfg.wantCancel {
		var c context.CancelFunc
		ctx, c = context.WithCancel(ctx)
		return ctx, c
	}
	return ctx, func() {}
}

// ForwardContext 按统一传播策略将安全的入站 gRPC metadata 写入出站 context。
// local、未声明和敏感字段不会跨进程传播；没有 metadata 时原样返回。
// Deprecated: transport 默认策略会自动完成传播，新代码不应手动调用。
func ForwardContext(ctx context.Context) context.Context {
	incoming, ok := grpcmetadata.FromIncomingContext(ctx)
	if !ok || len(incoming) == 0 {
		return ctx
	}

	propagator := modularmetadata.NewPropagator()
	propagated, err := propagator.Extract(ctx, grpcMetadataCarrier{metadata: incoming})
	if err != nil {
		return ctx
	}

	outgoing, _ := grpcmetadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	if outgoing == nil {
		outgoing = grpcmetadata.MD{}
	}
	if err := propagator.Inject(propagated, grpcMetadataCarrier{metadata: outgoing}); err != nil {
		return ctx
	}
	return grpcmetadata.NewOutgoingContext(propagated, outgoing)
}

type grpcMetadataCarrier struct {
	metadata grpcmetadata.MD
}

func (c grpcMetadataCarrier) Get(key string) string {
	values := c.metadata.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (c grpcMetadataCarrier) Set(key, value string) {
	c.metadata.Set(key, value)
}

func (c grpcMetadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.metadata))
	for key := range c.metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
