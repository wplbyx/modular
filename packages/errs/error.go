package errs

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/wplbyx/modular/packages/internal/errtemplate"
)

const (
	UnknownCode   = http.StatusInternalServerError
	UnknownReason = "UNKNOWN"
)

const unknownSlotValue = "UNKNOWN"

var (
	reasonPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	unknownMessage = Define(UnknownReason, Template("request failed"))
)

// Name 是错误文案中由编码人员声明的具名变量槽。
type Name string

// TemplateSpec 是由代码默认文案和有序变量槽组成的不可变模板定义。
type TemplateSpec struct {
	pattern string
	names   []Name
	parsed  errtemplate.Template
}

// Template 创建模板定义。仅支持 %v 槽位和 %% 字面量百分号。
// 非法模板属于编程错误，会立即 panic。
func Template(pattern string, names ...Name) TemplateSpec {
	rawNames := make([]string, len(names))
	for index, name := range names {
		rawNames[index] = string(name)
	}
	parsed, err := errtemplate.FromPattern(pattern, rawNames)
	if err != nil {
		panic(fmt.Sprintf("errs.Template: %v", err))
	}
	return TemplateSpec{
		pattern: pattern,
		names:   append([]Name(nil), names...),
		parsed:  parsed,
	}
}

// Message 是与具体语言无关的文案定义。With 返回副本，可安全复用全局定义。
type Message struct {
	reason   string
	fallback string
	template TemplateSpec
	params   map[Name]any
}

// Define 创建可被生成器静态发现的错误文案定义。
// 非法 reason 或零值模板属于编程错误，会立即 panic。
func Define(reason string, spec TemplateSpec) Message {
	reason = strings.TrimSpace(reason)
	if !reasonPattern.MatchString(reason) {
		panic(fmt.Sprintf("errs.Define: invalid reason %q", reason))
	}
	if spec.parsed.Text() == "" {
		panic("errs.Define: template is empty")
	}
	return Message{reason: reason, fallback: spec.parsed.Text(), template: spec}
}

// With 绑定一个模板参数，不修改原 Message。
func (m Message) With(key Name, value any) Message {
	params := m.Params()
	params[key] = value
	m.params = params
	return m
}

// WithParams 绑定一组模板参数，不修改原 Message。
func (m Message) WithParams(values map[Name]any) Message {
	params := m.Params()
	for key, value := range values {
		params[key] = value
	}
	m.params = params
	return m
}

func (m Message) Reason() string { return m.reason }

func (m Message) Fallback() string {
	message, _, _ := m.render(m.template.parsed)
	return message
}

// Params 返回模板参数的副本。
func (m Message) Params() map[Name]any {
	if len(m.params) == 0 {
		return make(map[Name]any)
	}
	params := make(map[Name]any, len(m.params))
	for key, value := range m.params {
		params[key] = value
	}
	return params
}

func (m Message) render(parsed errtemplate.Template) (string, []Name, []Name) {
	if parsed.Text() == "" {
		return m.fallback, nil, nil
	}
	values := make(map[string]any, len(m.params))
	for name, value := range m.params {
		values[string(name)] = value
	}
	message, missingStrings := parsed.Render(values, unknownSlotValue)
	missing := make([]Name, len(missingStrings))
	for index, name := range missingStrings {
		missing[index] = Name(name)
	}

	declared := make(map[Name]struct{}, len(m.template.names))
	for _, name := range m.template.names {
		declared[name] = struct{}{}
	}
	extra := make([]Name, 0)
	for name := range m.params {
		if _, ok := declared[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
	return message, missing, extra
}

func (m Message) slots() []string {
	slots := make([]string, len(m.template.names))
	for index, name := range m.template.names {
		slots[index] = string(name)
	}
	return slots
}

func literalMessage(reason, fallback string) Message {
	reason = strings.TrimSpace(reason)
	if !reasonPattern.MatchString(reason) {
		reason = UnknownReason
	}
	if fallback == "" {
		fallback = unknownMessageFallback()
	}
	return Message{reason: reason, fallback: fallback}
}

func unknownMessageFallback() string { return "request failed" }

// Option 为错误附加仅供诊断使用的信息。
type Option func(*Error)

// WithCause 设置底层错误。
func WithCause(cause error) Option {
	return func(err *Error) { err.cause = cause }
}

// WithField 设置一个仅写入日志的上下文字段。
func WithField(key string, value any) Option {
	return func(err *Error) {
		if err.fields == nil {
			err.fields = make(map[string]any)
		}
		err.fields[key] = value
	}
}

// WithFields 设置一组仅写入日志的上下文字段。
func WithFields(fields map[string]any) Option {
	return func(err *Error) {
		if err.fields == nil {
			err.fields = make(map[string]any, len(fields))
		}
		for key, value := range fields {
			err.fields[key] = value
		}
	}
}

// Error 是统一错误对象。导出字段是唯一允许进入客户端响应的内容。
type Error struct {
	Code    int32  `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`

	definition Message
	cause      error
	fields     map[string]any
	stack      []uintptr
}

var (
	_ error                       = (*Error)(nil)
	_ interface{ Unwrap() error } = (*Error)(nil)
)

// New 创建错误并捕获创建位置的调用栈。
func New(code int, message Message, opts ...Option) *Error {
	message = normalizeMessage(message)
	err := &Error{
		Code:       int32(code),
		Reason:     message.reason,
		Message:    message.Fallback(),
		definition: message,
		stack:      captureStack(3),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(err)
		}
	}
	return err
}

// Wrap 包装底层错误。cause 为 nil 时返回 nil。
func Wrap(cause error, code int, message Message, opts ...Option) *Error {
	if cause == nil {
		return nil
	}
	opts = append([]Option{WithCause(cause)}, opts...)
	return New(code, message, opts...)
}

func normalizeMessage(message Message) Message {
	if message.reason == "" {
		return unknownMessage
	}
	if message.fallback == "" {
		message.fallback = unknownMessageFallback()
	}
	return message
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause != nil {
		return fmt.Sprintf("error: code = %d reason = %s message = %s cause = %v", e.Code, e.Reason, e.Message, e.cause)
	}
	return fmt.Sprintf("error: code = %d reason = %s message = %s", e.Code, e.Reason, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is 使用 code 和 reason 判断两个统一错误是否相同。
func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) || e == nil || other == nil {
		return false
	}
	return e.Code == other.Code && e.Reason == other.Reason
}

// WithCause 返回带新 cause 的副本。
func (e *Error) WithCause(cause error) *Error {
	clone := Clone(e)
	if clone != nil {
		clone.cause = cause
	}
	return clone
}

// Fields 返回内部日志字段的副本。
func (e *Error) Fields() map[string]any {
	if e == nil || len(e.fields) == 0 {
		return nil
	}
	fields := make(map[string]any, len(e.fields))
	for key, value := range e.fields {
		fields[key] = value
	}
	return fields
}

// GRPCStatus 返回安全的 gRPC status，仅编码公开字段。
func (e *Error) GRPCStatus() *status.Status {
	if e == nil {
		return status.New(toGRPCCode(UnknownCode), unknownMessageFallback())
	}
	st, detailErr := status.New(toGRPCCode(int(e.Code)), e.Message).WithDetails(&errdetails.ErrorInfo{Reason: e.Reason})
	if detailErr != nil {
		return status.New(toGRPCCode(int(e.Code)), e.Message)
	}
	return st
}

// Clone 深拷贝 Error 的可变字段。
func Clone(err *Error) *Error {
	if err == nil {
		return nil
	}
	clone := *err
	clone.definition.params = err.definition.Params()
	clone.fields = err.Fields()
	clone.stack = append([]uintptr(nil), err.stack...)
	return &clone
}

// FromError 将标准错误或 gRPC status 转换为统一错误。
func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}

	grpcStatus, ok := status.FromError(err)
	if !ok {
		return Wrap(err, UnknownCode, unknownMessage)
	}
	reason := UnknownReason
	for _, detail := range grpcStatus.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Reason != "" {
			reason = info.Reason
			break
		}
	}
	message := literalMessage(reason, grpcStatus.Message())
	return Wrap(err, fromGRPCCode(grpcStatus.Code()), message)
}

func Code(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return int(FromError(err).Code)
}

func Reason(err error) string {
	if err == nil {
		return ""
	}
	return FromError(err).Reason
}

func captureStack(skip int) []uintptr {
	const maxDepth = 32
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(skip, pcs)
	return pcs[:n]
}

func formatStack(pcs []uintptr) string {
	if len(pcs) == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs)
	var builder strings.Builder
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&builder, "%s:%d %s\n", frame.File, frame.Line, frame.Function)
		if !more {
			break
		}
	}
	return builder.String()
}
