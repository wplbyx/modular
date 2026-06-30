package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewAntsWorkerPool 测试创建协程池
func TestNewAntsWorkerPool(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		wantErr  bool
	}{
		{
			name:     "valid capacity",
			capacity: 10,
			wantErr:  false,
		},
		{
			name:     "zero capacity",
			capacity: 0,
			wantErr:  true,
		},
		{
			name:     "negative capacity",
			capacity: -1,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewAntsWorkerPool(tt.capacity)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewAntsWorkerPool() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if pool == nil {
					t.Error("NewAntsWorkerPool() returned nil pool")
					return
				}
				pool.Close()
			}
		})
	}
}

// TestAntsWorkerPool_Submit 测试提交任务
func TestAntsWorkerPool_Submit(t *testing.T) {
	pool, err := NewAntsWorkerPool(5)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	var counter int64

	// 提交多个任务
	numTasks := 10
	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		task := func(ctx context.Context) error {
			defer wg.Done()
			atomic.AddInt64(&counter, 1)
			time.Sleep(10 * time.Millisecond)
			return nil
		}

		if err := pool.Submit(ctx, task); err != nil {
			t.Errorf("Failed to submit task %d: %v", i, err)
		}
	}

	wg.Wait()
	if got := atomic.LoadInt64(&counter); got != int64(numTasks) {
		t.Errorf("Expected %d tasks to be executed, got %d", numTasks, got)
	}
}

// TestAntsWorkerPool_SubmitWithContextCancellation 测试上下文取消
func TestAntsWorkerPool_SubmitWithContextCancellation(t *testing.T) {
	pool, err := NewAntsWorkerPool(3)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var executed int64
	var wg sync.WaitGroup

	// 提交一个会长时间运行的任务
	wg.Add(1)
	longTask := func(ctx context.Context) error {
		defer wg.Done()
		select {
		case <-time.After(100 * time.Millisecond):
			atomic.AddInt64(&executed, 1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if err := pool.Submit(ctx, longTask); err != nil {
		t.Errorf("Failed to submit long task: %v", err)
	}

	wg.Wait()
	// 由于上下文超时，任务应该被取消
	if got := atomic.LoadInt64(&executed); got > 0 {
		t.Errorf("Task should have been cancelled due to context timeout, but executed %d times", got)
	}
}

// TestAntsWorkerPool_SubmitWithError 测试任务返回错误
func TestAntsWorkerPool_SubmitWithError(t *testing.T) {
	pool, err := NewAntsWorkerPool(2)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	testErr := errors.New("test error")
	var executed int64
	var wg sync.WaitGroup

	// 提交会返回错误的任务
	wg.Add(1)
	errorTask := func(ctx context.Context) error {
		defer wg.Done()
		atomic.AddInt64(&executed, 1)
		return testErr
	}

	// Submit 应该成功，即使任务返回错误
	if err := pool.Submit(ctx, errorTask); err != nil {
		t.Errorf("Submit failed: %v", err)
	}

	wg.Wait()
	if got := atomic.LoadInt64(&executed); got != 1 {
		t.Errorf("Expected task to execute once, got %d", got)
	}
}

// TestAntsWorkerPool_PanicRecovery 测试 panic 恢复
func TestAntsWorkerPool_PanicRecovery(t *testing.T) {
	pool, err := NewAntsWorkerPool(2)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	// 提交会 panic 的任务
	wg.Add(1)
	panicTask := func(ctx context.Context) error {
		defer wg.Done()
		panic("test panic")
	}

	// Submit 应该成功，即使任务 panic
	if err := pool.Submit(ctx, panicTask); err != nil {
		t.Errorf("Submit failed: %v", err)
	}

	wg.Wait()
}

// TestAntsWorkerPool_ConcurrentSubmit 测试并发提交
func TestAntsWorkerPool_ConcurrentSubmit(t *testing.T) {
	pool, err := NewAntsWorkerPool(10)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	var submitWg sync.WaitGroup
	var counter int64

	// 多个 goroutine 并发提交任务
	numGoroutines := 5
	tasksPerGoroutine := 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < tasksPerGoroutine; j++ {
				submitWg.Add(1)
				task := func(ctx context.Context) error {
					defer submitWg.Done()
					atomic.AddInt64(&counter, 1)
					return nil
				}
				if err := pool.Submit(ctx, task); err != nil {
					t.Errorf("Goroutine %d failed to submit task %d: %v", goroutineID, j, err)
					submitWg.Done()
				}
			}
		}(i)
	}

	wg.Wait()

	// 等待所有任务执行完成
	submitWg.Wait()

	// 再等待一小段时间确保所有任务都已完成
	time.Sleep(100 * time.Millisecond)

	expected := int64(numGoroutines * tasksPerGoroutine)
	if got := atomic.LoadInt64(&counter); got != expected {
		t.Errorf("Expected %d tasks to be executed, got %d", expected, got)
	}
}

// TestAntsWorkerPool_Close 测试关闭协程池
func TestAntsWorkerPool_Close(t *testing.T) {
	pool, err := NewAntsWorkerPool(3)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	var started int64

	// 提交一些需要时间的任务
	for i := 0; i < 5; i++ {
		wg.Add(1)
		task := func(ctx context.Context) error {
			defer wg.Done()
			atomic.AddInt64(&started, 1)
			time.Sleep(50 * time.Millisecond)
			return nil
		}
		if err := pool.Submit(ctx, task); err != nil {
			t.Errorf("Failed to submit task: %v", err)
		}
	}

	// 关闭池，应该等待所有任务完成
	pool.Close()

	if got := atomic.LoadInt64(&started); got != 5 {
		t.Errorf("Expected all 5 tasks to start before close, got %d", got)
	}
}

// BenchmarkAntsWorkerPool_Submit 基准测试：任务提交性能
func BenchmarkAntsWorkerPool_Submit(b *testing.B) {
	pool, err := NewAntsWorkerPool(100)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			task := func(ctx context.Context) error {
				// 模拟轻量级任务
				return nil
			}
			if err := pool.Submit(ctx, task); err != nil {
				b.Errorf("Submit failed: %v", err)
			}
		}
	})
}

// BenchmarkAntsWorkerPool_TaskExecution 基准测试：任务执行性能
func BenchmarkAntsWorkerPool_TaskExecution(b *testing.B) {
	pool, err := NewAntsWorkerPool(100)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wg.Add(1)
			task := func(ctx context.Context) error {
				defer wg.Done()
				// 模拟一些计算
				sum := 0
				for i := 0; i < 100; i++ {
					sum += i
				}
				_ = sum
				return nil
			}
			if err := pool.Submit(ctx, task); err != nil {
				b.Errorf("Submit failed: %v", err)
			}
		}
	})
	wg.Wait()
}

// BenchmarkAntsWorkerPool_HeavyTasks 基准测试：重量级任务
func BenchmarkAntsWorkerPool_HeavyTasks(b *testing.B) {
	pool, err := NewAntsWorkerPool(10)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		task := func(ctx context.Context) error {
			defer wg.Done()
			// 模拟较重的任务
			time.Sleep(10 * time.Microsecond)
			return nil
		}
		if err := pool.Submit(ctx, task); err != nil {
			b.Errorf("Submit failed: %v", err)
		}
	}
	wg.Wait()
}

// ExampleAntsWorkerPool 展示协程池的基本用法
func ExampleAntsWorkerPool() {
	// 创建一个容量为 5 的协程池
	pool, err := NewAntsWorkerPool(5)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	// 提交 10 个任务
	for i := 0; i < 10; i++ {
		wg.Add(1)
		taskID := i
		task := func(ctx context.Context) error {
			defer wg.Done()
			// 执行任务
			_ = taskID
			return nil
		}

		if err := pool.Submit(ctx, task); err != nil {
			panic(err)
		}
	}

	// 等待所有任务完成
	wg.Wait()
}
