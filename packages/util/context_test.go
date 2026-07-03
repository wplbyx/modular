package util

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// ctxKey 仅用于测试 Value 链的保留。
type ctxKey string

const testKey ctxKey = "trace-id"

func TestDetachContext_PreservesValues(t *testing.T) {
	parent := context.WithValue(context.Background(), testKey, "abc-123")

	ctx, _ := DetachContext(parent)

	assert.Equal(t, "abc-123", ctx.Value(testKey), "脱离后应保留父 ctx 的 Value 链")
}

func TestDetachContext_CutsParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	parent = context.WithValue(parent, testKey, "v")
	ctx, _ := DetachContext(parent)

	// 取消父 ctx，脱离后的 ctx 不应受影响。
	cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("脱离后的 ctx 不应随父 ctx 取消而 Done: %v", ctx.Err())
	case <-time.After(20 * time.Millisecond):
	}
	assert.NoError(t, ctx.Err(), "脱离后 Err 应为 nil")
	// Value 链仍然保留。
	assert.Equal(t, "v", ctx.Value(testKey))
}

func TestDetachContext_CutsParentDeadline(t *testing.T) {
	// 父 ctx 已经过期。
	parent, cancel := context.WithTimeout(context.Background(), -1*time.Second)
	defer cancel()
	require.ErrorIs(t, parent.Err(), context.DeadlineExceeded)

	ctx, _ := DetachContext(parent)

	select {
	case <-ctx.Done():
		t.Fatalf("脱离后的 ctx 不应继承父 ctx 的过期 deadline: %v", ctx.Err())
	case <-time.After(20 * time.Millisecond):
	}
	assert.NoError(t, ctx.Err())
}

func TestDetachContext_NoDeadlineByDefault(t *testing.T) {
	ctx, _ := DetachContext(context.Background())

	_, ok := ctx.Deadline()
	assert.False(t, ok, "默认（不传 option）不应带 deadline")
	select {
	case <-ctx.Done():
		t.Fatalf("默认不应取消: %v", ctx.Err())
	case <-time.After(10 * time.Millisecond):
	}
}

func TestDetachContext_PureDetachCancelIsNoop(t *testing.T) {
	ctx, cancel := DetachContext(context.Background())
	require.NotNil(t, cancel, "纯脱离时也应返回非 nil 的 cancel")

	// 调用 no-op cancel，ctx 应保持存活，可无条件 defer cancel()。
	cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("no-op cancel 不应触发 Done: %v", ctx.Err())
	case <-time.After(15 * time.Millisecond):
	}
	assert.NoError(t, ctx.Err())
}

func TestDetachContext_WithTimeout(t *testing.T) {
	ctx, cancel := DetachContext(context.Background(), WithTimeout(20*time.Millisecond))
	defer cancel()

	_, ok := ctx.Deadline()
	require.True(t, ok, "WithTimeout 后应有 deadline")

	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("超时后 ctx 未 Done")
	}
}

func TestDetachContext_WithCancel(t *testing.T) {
	ctx, cancel := DetachContext(context.Background(), WithCancel())
	require.NotNil(t, cancel, "应返回非 nil 的 cancel 句柄")

	// 初始存活。
	select {
	case <-ctx.Done():
		t.Fatalf("不应提前 Done: %v", ctx.Err())
	default:
	}

	cancel()

	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("调用 cancel 后 ctx 未 Done")
	}
}

func TestDetachContext_WithTimeoutAndCancel(t *testing.T) {
	ctx, cancel := DetachContext(context.Background(),
		WithTimeout(5*time.Second),
		WithCancel(),
	)
	require.NotNil(t, cancel)

	// deadline 仍保留（来自 timeout）。
	_, ok := ctx.Deadline()
	require.True(t, ok)

	// 暴露的 cancel 可以提前取消，且语义是 Canceled 而非 DeadlineExceeded
	// （即 WithCancel 与 WithTimeout 返回的是同一个句柄，提前触发走 Canceled）。
	cancel()
	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled, "提前取消应为 Canceled 而非 DeadlineExceeded")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("组合后 cancel 未生效")
	}
}

func TestDetachContext_PreservesValuesWithOptions(t *testing.T) {
	parent := context.WithValue(context.Background(), testKey, "xyz")
	ctx, cancel := DetachContext(parent, WithTimeout(time.Second), WithCancel())
	defer cancel()

	assert.Equal(t, "xyz", ctx.Value(testKey), "即便组合 option，Value 链也应保留")
}

func TestPropagateGrpcContext_NoMetadata(t *testing.T) {
	ctx := ForwardContext(context.Background())

	_, ok := metadata.FromOutgoingContext(ctx)
	assert.False(t, ok, "无入站 metadata 时不应附加出站 MD")
}

func TestPropagateGrpcContext_EmptyMetadata(t *testing.T) {
	// len(md)==0 时与无 metadata 走同一分支。
	incoming := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	ctx := ForwardContext(incoming)

	_, ok := metadata.FromOutgoingContext(ctx)
	assert.False(t, ok, "空 MD 应原样返回，不附加出站 MD")
}

func TestPropagateGrpcContext_Propagates(t *testing.T) {
	incoming := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs("trace-id", "abc-123", "user-id", "u1"),
	)
	ctx := ForwardContext(incoming)

	out, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok, "应有出站 MD")
	assert.Equal(t, []string{"abc-123"}, out.Get("trace-id"))
	assert.Equal(t, []string{"u1"}, out.Get("user-id"))
}

func TestPropagateGrpcContext_DoesNotMutateInput(t *testing.T) {
	md := metadata.Pairs("trace-id", "abc")
	incoming := metadata.NewIncomingContext(context.Background(), md)
	_ = ForwardContext(incoming)

	// 入站 MD 不应被改动（内部用 md.Copy()）。
	require.Equal(t, []string{"abc"}, md.Get("trace-id"))
}
