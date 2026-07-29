package resilience

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wplbyx/modular/packages/errs"
)

func TestBulkheadExecuteRecoversPanicAndResetsRunning(t *testing.T) {
	bh := NewBulkhead(BulkheadConfig{
		Name:               "panic-test",
		MaxConcurrentCalls: 1,
		QueueSize:          1,
		WaitTimeout:        time.Second,
	})

	err := bh.Execute(context.Background(), func() error {
		panic("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "panic: boom") || errs.Code(err) != 500 || errs.Reason(err) != "BULKHEAD_PANIC" {
		t.Fatalf("Execute() error = %v, want panic error", err)
	}
	if got := bh.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0", got)
	}
}

func TestBulkheadExecuteAfterCloseReturnsClosedError(t *testing.T) {
	bh := NewBulkhead(BulkheadConfig{Name: "closed-test"})
	bh.Close()

	err := bh.Execute(context.Background(), func() error { return nil })
	if !errors.Is(err, ErrBulkheadClosed) {
		t.Fatalf("Execute() error = %v, want ErrBulkheadClosed", err)
	}
}

func TestBulkheadCloseUnblocksWaitingExecute(t *testing.T) {
	bh := NewBulkhead(BulkheadConfig{
		Name:               "close-test",
		MaxConcurrentCalls: 1,
		QueueSize:          1,
		WaitTimeout:        time.Second,
	})

	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- bh.Execute(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first call did not start")
	}

	waitingStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	panicCh := make(chan interface{}, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicCh <- recovered
			}
		}()
		close(waitingStarted)
		secondDone <- bh.Execute(context.Background(), func() error { return nil })
	}()

	select {
	case <-waitingStarted:
	case <-time.After(time.Second):
		t.Fatal("second call did not start waiting")
	}

	bh.Close()
	close(release)

	select {
	case recovered := <-panicCh:
		t.Fatalf("Execute() panicked after Close(): %v", recovered)
	case err := <-secondDone:
		if !errors.Is(err, ErrBulkheadClosed) {
			t.Fatalf("Execute() error = %v, want ErrBulkheadClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting Execute() did not return after Close()")
	}

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Execute() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Execute() did not finish")
	}

	if got := bh.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0", got)
	}
}

func TestBulkheadZeroQueueRejectsImmediately(t *testing.T) {
	bh := NewBulkhead(BulkheadConfig{
		Name:               "zero-queue",
		MaxConcurrentCalls: 1,
		QueueSize:          0,
		WaitTimeout:        time.Second,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- bh.Execute(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	start := time.Now()
	err := bh.Execute(context.Background(), func() error { return nil })
	if !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("Execute() error = %v, want ErrBulkheadFull", err)
	}
	if elapsed := time.Since(start); elapsed >= 50*time.Millisecond {
		t.Fatalf("full bulkhead blocked for %s", elapsed)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBulkheadFullQueueRejectsAdditionalWaiter(t *testing.T) {
	bh := NewBulkhead(BulkheadConfig{
		Name:               "full-queue",
		MaxConcurrentCalls: 1,
		QueueSize:          1,
		WaitTimeout:        time.Second,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = bh.Execute(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- bh.Execute(context.Background(), func() error { return nil })
	}()
	deadline := time.Now().Add(time.Second)
	for bh.(*bulkheadImpl).Waiting() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := bh.(*bulkheadImpl).Waiting(); got != 1 {
		t.Fatalf("Waiting() = %d, want 1", got)
	}

	err := bh.Execute(context.Background(), func() error { return nil })
	if !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("Execute() error = %v, want ErrBulkheadFull", err)
	}
	close(release)
	if err := <-waiterDone; err != nil {
		t.Fatal(err)
	}
}

func TestBulkheadConcurrentReleasesWakeAllWaiters(t *testing.T) {
	bh := NewBulkhead(BulkheadConfig{
		Name:               "release-test",
		MaxConcurrentCalls: 2,
		QueueSize:          2,
		WaitTimeout:        200 * time.Millisecond,
	})

	releaseInitial := make(chan struct{})
	initialStarted := make(chan struct{}, 2)
	initialDone := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			initialDone <- bh.Execute(context.Background(), func() error {
				initialStarted <- struct{}{}
				<-releaseInitial
				return nil
			})
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-initialStarted:
		case <-time.After(time.Second):
			t.Fatal("initial call did not start")
		}
	}

	waiterStarted := make(chan struct{}, 2)
	releaseWaiters := make(chan struct{})
	waiterDone := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			waiterDone <- bh.Execute(context.Background(), func() error {
				waiterStarted <- struct{}{}
				<-releaseWaiters
				return nil
			})
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(releaseInitial)

	for i := 0; i < 2; i++ {
		select {
		case <-waiterStarted:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("waiter did not start after capacity was released")
		}
	}

	close(releaseWaiters)
	for i := 0; i < 2; i++ {
		if err := <-initialDone; err != nil {
			t.Fatalf("initial Execute() error = %v", err)
		}
		if err := <-waiterDone; err != nil {
			t.Fatalf("waiter Execute() error = %v", err)
		}
	}
}
