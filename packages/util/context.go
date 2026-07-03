package util

import (
	"context"
	"time"

	"google.golang.org/grpc/metadata"
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

// ForwardContext 将入站 gRPC metadata 透传为出站 metadata，
// 用于 gRPC 服务间调用时上下文参数（metadata）丢失的问题。
// 没有 metadata 时原样返回，避免附加空 MD。
func ForwardContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, md.Copy())
}
