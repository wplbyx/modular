package core

import (
	"context"
)

// Resource 是需要生命周期管理的基础设施资源（DB、Redis、Cache、Storage、Telemetry）。
// 与 Endpoint 的区别：Setup/Close 立即返回，不阻塞，不接收流量。
//
// Application 在所有 Endpoint 启动前按 FIFO 调用 Setup，
// 在所有 Endpoint 停止后按 LIFO 调用 Close。
type Resource interface {
	Name() string
	Setup(ctx context.Context) error
	Close(ctx context.Context) error
}

// Endpoint is an application-managed transport entrypoint.
//
// HTTP servers, gRPC servers, SSE servers, and message subscribers are
// endpoints because they own a lifecycle and receive inbound traffic/events.
// Resource infrastructure such as databases, Redis, and storage should not
// implement this interface; register those as application resources instead.
type Endpoint interface {
	Name() string
	Startup(context.Context) error
	Shutdown(context.Context) error
}

// Startup must block until the service is no longer running. It must not return
// early on its own under normal operation. Application.Run treats any Startup
// return (nil or error) as an exit signal and triggers a full shutdown.
//
// Shutdown must unblock Startup (e.g. by calling http.Server.Shutdown or
// grpc.Server.GracefulStop). Startup does not need to react to its context being
// cancelled; Shutdown is the mechanism that brings Startup down.
