package pubsub

import (
	"context"

	modularmetadata "github.com/wplbyx/modular/packages/metadata"
)

var defaultMetadataPropagator = modularmetadata.NewPropagator()

// ResolvePublishOptions applies options to a copy of defaults and injects the
// globally scoped metadata supported by header-capable message transports.
func ResolvePublishOptions(ctx context.Context, defaults PublishOptions, opts ...PublishOption) (PublishOptions, error) {
	resolved := defaults
	resolved.Headers = cloneHeaders(defaults.Headers)
	for _, option := range opts {
		if option != nil {
			option(&resolved)
		}
	}
	resolved.Headers = cloneHeaders(resolved.Headers)
	if resolved.Headers == nil {
		resolved.Headers = make(map[string]string)
	}
	if err := defaultMetadataPropagator.Inject(ctx, modularmetadata.MapCarrier(resolved.Headers)); err != nil {
		return PublishOptions{}, err
	}
	return resolved, nil
}

// WithMessageMetadata extracts message headers before invoking the handler.
func WithMessageMetadata(handler MessageHandler) MessageHandler {
	return withMessageMetadata(defaultMetadataPropagator, handler)
}

func withMessageMetadata(propagator *modularmetadata.Propagator, handler MessageHandler) MessageHandler {
	return func(ctx context.Context, message Message) error {
		if handler == nil {
			return nil
		}
		if len(message.Headers) == 0 && len(modularmetadata.FromContext(ctx).Get(modularmetadata.RequestIDKey)) > 0 {
			return handler(ctx, message)
		}
		requestCtx, err := propagator.Extract(ctx, modularmetadata.MapCarrier(message.Headers))
		if err != nil {
			return err
		}
		return handler(requestCtx, message)
	}
}

func cloneHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	clone := make(map[string]string, len(headers))
	for key, value := range headers {
		clone[key] = value
	}
	return clone
}
