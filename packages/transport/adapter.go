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

// Endpointer is registry endpoint.
type Endpointer interface {
	Endpoint() (*url.URL, error)
}
