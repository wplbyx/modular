package errs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	modularlog "github.com/wplbyx/modular/packages/log"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMessageWithDoesNotMutateDefinition(t *testing.T) {
	definition := Define("USER_NOT_FOUND", Template("user %v not found", Name("user_id")))
	bound := definition.With("user_id", "42")

	if len(definition.Params()) != 0 {
		t.Fatalf("definition params = %v, want empty", definition.Params())
	}
	if got := bound.Params()["user_id"]; got != "42" {
		t.Fatalf("bound user_id = %v, want 42", got)
	}
	params := bound.Params()
	params["user_id"] = "changed"
	if got := bound.Params()["user_id"]; got != "42" {
		t.Fatalf("external mutation changed message params: %v", got)
	}
}

func TestTemplateExpandsFmtStyleSlots(t *testing.T) {
	definition := Define(
		"QUOTA_EXCEEDED",
		Template("used %v%% of quota %v", Name("percent"), Name("quota")),
	)
	message := definition.With("percent", 80).With("quota", "daily")

	if got := message.Fallback(); got != "used 80% of quota daily" {
		t.Fatalf("fallback = %q, want %q", got, "used 80% of quota daily")
	}
}

func TestTemplateRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "unsupported verb", fn: func() { Template("value %s", Name("value")) }},
		{name: "missing name", fn: func() { Template("value %v") }},
		{name: "duplicate name", fn: func() { Template("%v %v", Name("value"), Name("value")) }},
		{name: "invalid reason", fn: func() { Define("invalid", Template("invalid")) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !panics(test.fn) {
				t.Fatal("expected panic")
			}
		})
	}
}

func panics(fn func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	fn()
	return false
}

func TestErrorWrapAndGRPCStatus(t *testing.T) {
	cause := errors.New("database password=secret")
	err := Wrap(
		cause,
		http.StatusNotFound,
		Define("USER_NOT_FOUND", Template("user not found")),
		WithField("query", "select secret"),
	)

	if !errors.Is(err, cause) {
		t.Fatal("wrapped error does not match cause")
	}
	if err.GRPCStatus().Code() != codes.NotFound {
		t.Fatalf("gRPC code = %v, want %v", err.GRPCStatus().Code(), codes.NotFound)
	}
	if strings.Contains(err.GRPCStatus().Message(), "secret") {
		t.Fatalf("gRPC message leaked cause: %q", err.GRPCStatus().Message())
	}
	details := err.GRPCStatus().Details()
	if len(details) != 1 || details[0].(*errdetails.ErrorInfo).Reason != "USER_NOT_FOUND" {
		t.Fatalf("gRPC details = %#v", details)
	}

	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("JSON leaked internal data: %s", data)
	}
}

func TestErrorsIsUsesCodeAndReason(t *testing.T) {
	target := NotFound(Define("USER_NOT_FOUND", Template("not found")))
	err := fmt.Errorf("lookup: %w", NotFound(Define("USER_NOT_FOUND", Template("missing"))))
	if !errors.Is(err, target) {
		t.Fatal("errors.Is did not match code and reason")
	}
	if errors.Is(err, BadRequest(Define("USER_NOT_FOUND", Template("missing")))) {
		t.Fatal("errors.Is matched a different code")
	}
}

func TestFromErrorConvertsGRPCStatus(t *testing.T) {
	grpcStatus, err := status.New(codes.ResourceExhausted, "slow down").WithDetails(
		&errdetails.ErrorInfo{Reason: "RATE_LIMITED"},
	)
	if err != nil {
		t.Fatal(err)
	}
	converted := FromError(grpcStatus.Err())
	if converted.Code != http.StatusTooManyRequests || converted.Reason != "RATE_LIMITED" {
		t.Fatalf("converted = %+v", converted)
	}
}

func TestFromErrorHidesPlainErrorMessage(t *testing.T) {
	converted := FromError(errors.New("dsn contains secret"))
	if converted.Code != UnknownCode || converted.Reason != UnknownReason {
		t.Fatalf("converted = %+v", converted)
	}
	if strings.Contains(converted.Message, "secret") {
		t.Fatalf("plain error leaked into public message: %q", converted.Message)
	}
}

func TestCatalogRenderAndFallback(t *testing.T) {
	catalog := testCatalog(t)
	message := Define("USER_NOT_FOUND", Template("user %v not found", Name("user_id"))).With("user_id", "42")

	result := catalog.Render("en-US;q=0.5, zh-CN;q=0.9", message)
	if result.Message != "用户 42 不存在" || result.Locale != "zh-CN" || result.Fallback != "none" {
		t.Fatalf("render result = %+v", result)
	}

	result = catalog.Render("en-US", Define("NOT_TRANSLATED", Template("not translated")))
	if result.Message != "Request failed" || result.Locale != "en-US" || result.Fallback != "requested_unknown" {
		t.Fatalf("fallback result = %+v", result)
	}

	result = catalog.Render("zh-CN", Define("USER_NOT_FOUND", Template("user %v not found", Name("user_id"))))
	if result.Message != "用户 UNKNOWN 不存在" || result.TemplateError == nil {
		t.Fatalf("missing parameter fallback = %+v", result)
	}
}

func TestCatalogAllowsSlotReorderingAndReportsBindingIssues(t *testing.T) {
	catalog, err := LoadCatalog(fstest.MapFS{
		"locales/en-US.yaml": {Data: []byte("UNKNOWN: 'Request failed'\nMEMBER_NOT_FOUND: 'Organization {{.org}} has no user {{.user_id}}'\n")},
	}, "locales", "en-US")
	if err != nil {
		t.Fatal(err)
	}
	definition := Define(
		"MEMBER_NOT_FOUND",
		Template("user %v was not found in %v", Name("user_id"), Name("org")),
	)

	result := catalog.Render("en-US", definition.With("user_id", 42).With("unused", true))
	if result.Message != "Organization UNKNOWN has no user 42" {
		t.Fatalf("message = %q", result.Message)
	}
	if result.TemplateError == nil || !strings.Contains(result.TemplateError.Error(), "undeclared template values: unused") {
		t.Fatalf("template error = %v", result.TemplateError)
	}
}

func TestCatalogConcurrentRender(t *testing.T) {
	catalog := testCatalog(t)
	definition := Define("USER_NOT_FOUND", Template("user %v not found", Name("user_id")))
	var wait sync.WaitGroup
	for index := range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				result := catalog.Render("zh-CN", definition.With("user_id", index))
				if !strings.Contains(result.Message, fmt.Sprint(index)) {
					t.Errorf("render result = %+v", result)
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestCatalogRejectsInvalidConfiguration(t *testing.T) {
	_, err := LoadCatalog(fstest.MapFS{
		"locales/en-US.yaml": {Data: []byte("BROKEN: '{{.value'\n")},
	}, "locales", "en-US")
	if err == nil {
		t.Fatal("invalid template was accepted")
	}

	_, err = LoadCatalog(fstest.MapFS{
		"locales/en-US.yaml": {Data: []byte("SOMETHING: ok\n")},
	}, "locales", "en-US")
	if err == nil || !strings.Contains(err.Error(), UnknownReason) {
		t.Fatalf("missing UNKNOWN error = %v", err)
	}
}

func TestHandlerSeparatesClientAndDiagnosticData(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	handler, err := NewHandler(testCatalog(t), observedLogger{logger: zap.New(core)})
	if err != nil {
		t.Fatal(err)
	}
	traceID := trace.TraceID{1}
	spanID := trace.SpanID{2}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	internal := Wrap(
		errors.New("database secret"),
		http.StatusInternalServerError,
		Define("QUERY_FAILED", Template("query %v failed", Name("table"))).With("table", "users"),
		WithField("sql", "select password"),
	)

	result := handler.Handle(ctx, internal, RequestInfo{
		Transport: "http",
		Operation: "GET /users/:id",
		RequestID: "req-1",
		Language:  "zh-CN",
	})
	if result.Error.Message != "请求失败" {
		t.Fatalf("client message = %q", result.Error.Message)
	}
	public, _ := json.Marshal(result.Error)
	if strings.Contains(string(public), "secret") || strings.Contains(string(public), "password") {
		t.Fatalf("client projection leaked diagnostics: %s", public)
	}

	entries := observed.All()
	if len(entries) != 1 || entries[0].Level != zapcore.ErrorLevel {
		t.Fatalf("log entries = %+v", entries)
	}
	logged := fmt.Sprint(entries[0].ContextMap())
	for _, want := range []string{"database secret", "select password", "req-1", traceID.String()} {
		if !strings.Contains(logged, want) {
			t.Fatalf("diagnostic log missing %q: %s", want, logged)
		}
	}
}

func TestHandlerUsesWarnForClientErrors(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	handler, err := NewHandler(testCatalog(t), observedLogger{logger: zap.New(core)})
	if err != nil {
		t.Fatal(err)
	}
	handler.Handle(context.Background(), BadRequest(Define("BAD_INPUT", Template("bad input"))), RequestInfo{})
	if observed.Len() != 1 || observed.All()[0].Level != zapcore.WarnLevel {
		t.Fatalf("log entries = %+v", observed.All())
	}
}

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog, err := LoadCatalog(fstest.MapFS{
		"locales/zh-CN.yaml": {Data: []byte("UNKNOWN: '请求失败'\nUSER_NOT_FOUND: '用户 {{.user_id}} 不存在'\n")},
		"locales/en-US.yaml": {Data: []byte("UNKNOWN: 'Request failed'\nUSER_NOT_FOUND: 'User {{.user_id}} was not found'\n")},
	}, "locales", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

type observedLogger struct{ logger *zap.Logger }

func (l observedLogger) Debug(_ context.Context, message string, fields ...modularlog.Field) {
	l.logger.Debug(message, fields...)
}
func (l observedLogger) Info(_ context.Context, message string, fields ...modularlog.Field) {
	l.logger.Info(message, fields...)
}
func (l observedLogger) Warn(_ context.Context, message string, fields ...modularlog.Field) {
	l.logger.Warn(message, fields...)
}
func (l observedLogger) Error(_ context.Context, message string, fields ...modularlog.Field) {
	l.logger.Error(message, fields...)
}
func (l observedLogger) With(fields ...modularlog.Field) modularlog.Logger {
	return observedLogger{logger: l.logger.With(fields...)}
}
func (l observedLogger) Named(name string) modularlog.Logger {
	return observedLogger{logger: l.logger.Named(name)}
}
