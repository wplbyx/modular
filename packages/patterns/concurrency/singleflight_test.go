package concurrency

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSingleFlightCollapsesConcurrentCalls 验证同一 key 的并发调用被合并为一次执行。
func TestSingleFlightCollapsesConcurrentCalls(t *testing.T) {
	sf := NewSingleFlight[string, string]()
	started := make(chan struct{})
	release := make(chan struct{})

	var calls int32
	var wg sync.WaitGroup
	results := make(chan Result[string], 2)
	run := func() {
		defer wg.Done()
		val, err := sf.Do(context.Background(), "key", func(ctx context.Context) (string, error) {
			atomic.AddInt32(&calls, 1)
			close(started)
			<-release
			return "value", nil
		})
		results <- Result[string]{Val: val, Err: err}
	}

	wg.Add(1)
	go run()
	<-started

	wg.Add(1)
	go run()
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	for result := range results {
		if result.Err != nil || result.Val != "value" {
			t.Fatalf("SingleFlight result = %+v", result)
		}
	}
	if calls != 1 {
		t.Fatalf("SingleFlight calls = %d", calls)
	}
}

// TestSingleFlightWaiterCanCancel 验证 waiter 可以单独取消，不影响 leader。
func TestSingleFlightWaiterCanCancel(t *testing.T) {
	sf := NewSingleFlight[string, string]()
	started := make(chan struct{})
	release := make(chan struct{})

	done := make(chan Result[string], 1)
	go func() {
		val, err := sf.Do(context.Background(), "key", func(ctx context.Context) (string, error) {
			close(started)
			<-release
			return "value", nil
		})
		done <- Result[string]{Val: val, Err: err}
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if val, err := sf.Do(ctx, "key", func(ctx context.Context) (string, error) {
		t.Fatal("duplicate call should not execute")
		return "", nil
	}); !errors.Is(err, context.DeadlineExceeded) || val != "" {
		t.Fatalf("cancelled waiter result = %v, %v", val, err)
	}

	close(release)
	result := <-done
	if result.Err != nil || result.Val != "value" {
		t.Fatalf("leader result = %+v", result)
	}
}

// TestSingleFlightFnReceivesContext 验证 fn 收到的 ctx 可被取消，长耗时任务能响应中断。
func TestSingleFlightFnReceivesContext(t *testing.T) {
	sf := NewSingleFlight[string, string]()
	started := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	val, err := sf.Do(ctx, "key", func(fnCtx context.Context) (string, error) {
		close(started)
		<-fnCtx.Done()
		return "", fnCtx.Err()
	})

	if val != "" {
		t.Fatalf("expected zero value on cancel, got %q", val)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestSingleFlightPanicPropagatesError 验证 leader fn panic 时，
// waiter 拿到错误而非 (zero, nil) 成功假象，且 leader 调用方也收到 error 而非裸 panic。
func TestSingleFlightPanicPropagatesError(t *testing.T) {
	sf := NewSingleFlight[string, string]()
	started := make(chan struct{})
	release := make(chan struct{})

	// leader：fn panic。leader 由 leader goroutine 注册并执行 fn。
	leaderDone := make(chan Result[string], 1)
	go func() {
		val, err := sf.Do(context.Background(), "key", func(ctx context.Context) (string, error) {
			close(started)
			<-release
			panic("boom")
		})
		leaderDone <- Result[string]{Val: val, Err: err}
	}()
	<-started // leader 的 fn 已开始执行，call 已登记在 sf.calls 中

	// waiter：挂到已登记的 call 上，其 fn 不应执行（被 collapse）。
	// waiter 的 fn 返回一个可识别错误，便于在（不该发生的）退化场景下诊断。
	waiterDone := make(chan Result[string], 1)
	go func() {
		val, err := sf.Do(context.Background(), "key", func(ctx context.Context) (string, error) {
			return "", errors.New("waiter fn should not execute")
		})
		waiterDone <- Result[string]{Val: val, Err: err}
	}()
	// 等待 waiter 真正挂载到进行中的 call 上（同步方式与上方合并测试一致）。
	time.Sleep(10 * time.Millisecond)

	close(release)

	leader := <-leaderDone
	if leader.Err == nil || !strings.Contains(leader.Err.Error(), "boom") {
		t.Fatalf("leader expected panic error containing 'boom', got %v", leader.Err)
	}
	waiter := <-waiterDone
	if waiter.Err == nil {
		t.Fatalf("waiter expected error, got nil (val=%q)", waiter.Val)
	}
	if !strings.Contains(waiter.Err.Error(), "boom") {
		t.Fatalf("waiter expected error containing 'boom', got %v", waiter.Err)
	}
}

// TestSingleFlightForget 验证 Forget 后下一次 Do 同 key 会再次执行 fn。
func TestSingleFlightForget(t *testing.T) {
	sf := NewSingleFlight[string, string]()
	var calls int32

	inc := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "v", nil
	}

	if _, err := sf.Do(context.Background(), "k", inc); err != nil {
		t.Fatalf("first Do: %v", err)
	}
	sf.Forget("k")
	if _, err := sf.Do(context.Background(), "k", inc); err != nil {
		t.Fatalf("second Do: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected fn executed twice after Forget, got %d", calls)
	}
}

// TestSingleFlightDoChan 验证 DoChan 正常返回结果并关闭 channel。
func TestSingleFlightDoChan(t *testing.T) {
	sf := NewSingleFlight[string, string]()
	ch := sf.DoChan(context.Background(), "k", func(ctx context.Context) (string, error) {
		return "ok", nil
	})

	result, ok := <-ch
	if !ok {
		t.Fatal("expected channel open before receive")
	}
	if result.Err != nil || result.Val != "ok" {
		t.Fatalf("DoChan result = %+v", result)
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after result")
	}
}

// TestSingleFlightNilFn 验证 nil fn 返回错误。
func TestSingleFlightNilFn(t *testing.T) {
	sf := NewSingleFlight[string, string]()

	val, err := sf.Do(context.Background(), "k", nil)
	if err == nil {
		t.Fatal("expected error for nil fn")
	}
	if val != "" {
		t.Fatalf("expected zero value, got %q", val)
	}
}
