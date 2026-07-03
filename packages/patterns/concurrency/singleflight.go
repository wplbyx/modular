package concurrency

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

// SingleFlight 抑制重复调用：同一 key 的多个并发请求只执行一次 fn，
// 其余调用者（waiter）阻塞等待 leader 的结果广播。类型参数 K 为 key 类型，
// V 为结果值类型，避免调用方做类型断言（风格对齐同包 [Memoizer]）。
type SingleFlight[K comparable, V any] struct {
	mu    sync.Mutex
	calls map[K]*call[V]
}

// call 描述一次进行中的（key -> fn）调用，由 leader 创建并广播结果给所有 waiter。
type call[V any] struct {
	done chan struct{}
	val  V
	err  error
}

// NewSingleFlight 创建一个新的 SingleFlight 实例。
func NewSingleFlight[K comparable, V any]() *SingleFlight[K, V] {
	return &SingleFlight[K, V]{
		calls: make(map[K]*call[V]),
	}
}

// Do 对给定 key 执行 fn，保证同一 key 同时只执行一次。
// 第一个调用者成为 leader 并执行 fn(ctx)，后续调用者成为 waiter，阻塞直到
// leader 完成或自身 ctx 被取消。waiter 取消只影响它自己，leader 与其它
// waiter 不受影响（语义对齐 golang.org/x/sync/singleflight）。
//
// fn 收到与 Do 相同的 ctx，长耗时任务可据此响应取消。
//
// 使用命名返回值，以便 fn panic 时 defer recover 能把错误写回返回值；
// 否则 panic 路径会丢失错误、返回 (zero, nil)。
func (sf *SingleFlight[K, V]) Do(ctx context.Context, key K, fn func(context.Context) (V, error)) (val V, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return val, errors.New("singleflight function is nil")
	}

	sf.mu.Lock()
	if sf.calls == nil {
		sf.calls = make(map[K]*call[V])
	}

	// waiter 分支：已有 leader 在跑，等待结果或自身取消。
	if c, ok := sf.calls[key]; ok {
		sf.mu.Unlock()
		select {
		case <-ctx.Done():
			return val, ctx.Err()
		case <-c.done:
			return c.val, c.err
		}
	}

	// leader 分支：登记 call，执行 fn，完成后广播并清理。
	c := &call[V]{done: make(chan struct{})}
	sf.calls[key] = c
	sf.mu.Unlock()

	defer func() {
		// 防御 panic：leader fn panic 时不能让 waiter 拿到 (zero, nil) 的成功假象。
		// recover 后同时写入命名返回值 err（给 leader 调用方）和 c.err（给 waiter）。
		if r := recover(); r != nil {
			err = fmt.Errorf("singleflight panic: %v\n%s", r, debug.Stack())
			c.err = err
		}
		close(c.done) // 广播给所有 waiter
		sf.mu.Lock()
		// Forget 可能在执行期间移除该 key；只删自己登记的 call。
		if cur, ok := sf.calls[key]; ok && cur == c {
			delete(sf.calls, key)
		}
		sf.mu.Unlock()
	}()

	// 进入执行前再做一次取消检查，与 memoize.GetOrLoad 保持一致。
	if e := ctx.Err(); e != nil {
		c.err = e
		return c.val, c.err
	}

	c.val, c.err = fn(ctx)
	return c.val, c.err
}

// DoChan 以 channel 形式返回结果。它内部启动一个 goroutine 调用 Do；
// 该 goroutine 在 Do 返回后即向 channel 发送结果并关闭，随 channel 关闭而终止，
// 不会泄漏。
func (sf *SingleFlight[K, V]) DoChan(ctx context.Context, key K, fn func(context.Context) (V, error)) <-chan Result[V] {
	ch := make(chan Result[V], 1)

	go func() {
		defer close(ch)
		val, err := sf.Do(ctx, key, fn)
		ch <- Result[V]{Val: val, Err: err}
	}()

	return ch
}

// Result 表示一次 SingleFlight 执行的结果。
type Result[V any] struct {
	Val V
	Err error
}

// Forget 立即从进行中追踪里移除 key。若该 key 的 leader 仍在执行，已登记的 waiter
// 仍会收到该次结果；但此后对该 key 的下一次 Do 会成为新的 leader 再次执行 fn。
func (sf *SingleFlight[K, V]) Forget(key K) {
	sf.mu.Lock()
	delete(sf.calls, key)
	sf.mu.Unlock()
}
