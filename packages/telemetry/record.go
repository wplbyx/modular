package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"holographic/packages/log"
)

// RecordError records an error on the active span and writes a correlated log.
func RecordError(ctx context.Context, err error, msg string, attrs ...attribute.KeyValue) {
	if err == nil {
		return
	}
	if msg == "" {
		msg = err.Error()
	}

	span := trace.SpanFromContext(ctx)
	span.SetStatus(codes.Error, msg)
	span.RecordError(err, trace.WithAttributes(attrs...))

	fields := []zap.Field{zap.Error(err)}
	spanCtx := span.SpanContext()
	if spanCtx.IsValid() {
		fields = append(fields,
			zap.String("trace_id", spanCtx.TraceID().String()),
			zap.String("span_id", spanCtx.SpanID().String()),
		)
	}
	fields = append(fields, attributeFields(attrs)...)

	log.GetLogger().Error(msg, fields...)
}

func attributeFields(attrs []attribute.KeyValue) []zap.Field {
	fields := make([]zap.Field, 0, len(attrs))
	for _, attr := range attrs {
		fields = append(fields, zap.Any(string(attr.Key), attr.Value.AsInterface()))
	}
	return fields
}
