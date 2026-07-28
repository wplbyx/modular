package errs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	modularlog "github.com/wplbyx/modular/packages/log"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// RequestInfo 是 transport 交给错误处理器的请求关联信息。
type RequestInfo struct {
	Transport string
	Operation string
	RequestID string
	Language  string
}

// Result 是客户端错误投影。
type Result struct {
	Error  *Error
	Locale string
}

// Handler 集中文案渲染、客户端投影和内部诊断日志。
type Handler struct {
	catalog *Catalog
	logger  modularlog.Logger
}

func NewHandler(catalog *Catalog, logger modularlog.Logger) (*Handler, error) {
	if catalog == nil {
		return nil, errors.New("error catalog is nil")
	}
	if logger == nil {
		return nil, errors.New("error handler logger is nil")
	}
	return &Handler{catalog: catalog, logger: logger}, nil
}

// Handle 记录完整诊断信息，并返回不含内部数据的客户端错误。
func (handler *Handler) Handle(ctx context.Context, err error, info RequestInfo) Result {
	if err == nil {
		return Result{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	typed := FromError(err)
	code := int(typed.Code)
	if code < 400 || code > 599 {
		code = UnknownCode
	}
	definition := typed.definition
	if definition.reason == "" {
		definition = literalMessage(typed.Reason, typed.Message)
	}
	if typed.Reason == "" {
		definition = unknownMessage
	}
	rendered := handler.catalog.Render(info.Language, definition)
	reason := definition.reason
	if reason == "" {
		reason = UnknownReason
	}
	clientErr := &Error{Code: int32(code), Reason: reason, Message: rendered.Message}

	fields := []zap.Field{
		zap.Int("error.code", code),
		zap.String("error.reason", reason),
		zap.NamedError("error.cause", err),
		zap.String("error.locale", rendered.Locale),
		zap.String("error.translation_fallback", rendered.Fallback),
		zap.Any("error.template_params", definition.Params()),
		zap.Any("error.chain", diagnosticChain(err)),
		zap.String("transport", info.Transport),
		zap.String("operation", info.Operation),
		zap.String("request_id", info.RequestID),
	}
	if rendered.TemplateError != nil {
		fields = append(fields, zap.NamedError("error.template_error", rendered.TemplateError))
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		fields = append(fields,
			zap.String("trace_id", spanContext.TraceID().String()),
			zap.String("span_id", spanContext.SpanID().String()),
		)
	}
	if code >= http.StatusInternalServerError {
		handler.logger.Error(ctx, "request failed", fields...)
	} else {
		handler.logger.Warn(ctx, "request failed", fields...)
	}
	return Result{Error: clientErr, Locale: rendered.Locale}
}

type diagnostic struct {
	Type   string         `json:"type"`
	Error  string         `json:"error"`
	Code   int32          `json:"code,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
	Stack  string         `json:"stack,omitempty"`
}

func diagnosticChain(err error) []diagnostic {
	const maxErrors = 64
	chain := make([]diagnostic, 0, 4)
	queue := []error{err}
	seen := make(map[string]struct{})
	for len(queue) > 0 && len(chain) < maxErrors {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		identity := errorIdentity(current)
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}

		item := diagnostic{Type: fmt.Sprintf("%T", current), Error: current.Error()}
		if typed, ok := current.(*Error); ok {
			item.Code = typed.Code
			item.Reason = typed.Reason
			item.Fields = typed.Fields()
			item.Stack = formatStack(typed.stack)
		}
		chain = append(chain, item)
		switch unwrapped := current.(type) {
		case interface{ Unwrap() []error }:
			queue = append(queue, unwrapped.Unwrap()...)
		case interface{ Unwrap() error }:
			queue = append(queue, unwrapped.Unwrap())
		}
	}
	return chain
}

func errorIdentity(err error) string {
	value := reflect.ValueOf(err)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
		return fmt.Sprintf("%T:%x", err, value.Pointer())
	}
	return fmt.Sprintf("%T:%s", err, err.Error())
}
