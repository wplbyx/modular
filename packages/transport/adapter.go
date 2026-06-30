package transport

import (
	"context"
	"net/url"
)

// Endpoint is an application-managed transport entrypoint.
//
// HTTP servers, gRPC servers, SSE servers, and message subscribers are
// endpoints because they own a lifecycle and receive inbound traffic/events.
// Resource infrastructure such as databases, Redis, and storage should not
// implement this interface; register those as application cleanups instead.
type Endpoint interface {
	Name() string
	Start(context.Context) error
	Stop(context.Context) error
}

// Start must block until the service is no longer running. It must not return
// early on its own under normal operation. Application.Run treats any Start
// return (nil or error) as an exit signal and triggers a full shutdown.
//
// Stop must unblock Start (e.g. by calling http.Server.Shutdown or
// grpc.Server.GracefulStop). Start does not need to react to its context being
// cancelled; Stop is the mechanism that brings Start down.

// Endpointer is registry endpoint.
type Endpointer interface {
	Endpoint() (*url.URL, error)
}
