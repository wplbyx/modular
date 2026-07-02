package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoizerGetOrLoadCachesValue(t *testing.T) {
	m := NewMemoizer[string, string](time.Minute)

	var loads int32
	got, err := m.GetOrLoad(context.Background(), "key", func(context.Context) (string, error) {
		atomic.AddInt32(&loads, 1)
		return "value", nil
	})
	if err != nil {
		t.Fatalf("GetOrLoad() error = %v", err)
	}
	if got != "value" {
		t.Fatalf("GetOrLoad() = %q", got)
	}

	got, err = m.GetOrLoad(context.Background(), "key", func(context.Context) (string, error) {
		atomic.AddInt32(&loads, 1)
		return "new", nil
	})
	if err != nil {
		t.Fatalf("GetOrLoad() cached error = %v", err)
	}
	if got != "value" || loads != 1 {
		t.Fatalf("cached GetOrLoad() = %q, loads = %d", got, loads)
	}
}

func TestMemoizerExpiresValue(t *testing.T) {
	m := NewMemoizer[string, int](50 * time.Millisecond)

	m.Set("key", 10)
	if got, ok := m.Get("key"); !ok || got != 10 {
		t.Fatalf("Get() before expiration = %d, %v", got, ok)
	}

	time.Sleep(100 * time.Millisecond)
	if got, ok := m.Get("key"); ok || got != 0 {
		t.Fatalf("Get() after expiration = %d, %v", got, ok)
	}
	if m.Len() != 0 {
		t.Fatalf("Len() after expiration = %d", m.Len())
	}
}

func TestMemoizerLoaderErrorIsNotCached(t *testing.T) {
	m := NewMemoizer[string, string](0)
	wantErr := errors.New("load failed")

	if _, err := m.GetOrLoad(context.Background(), "key", func(context.Context) (string, error) {
		return "", wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("GetOrLoad() error = %v", err)
	}
	if _, ok := m.Get("key"); ok {
		t.Fatal("Get() returned value after loader error")
	}
}

func TestSingleFlightCollapsesConcurrentCalls(t *testing.T) {
	sf := NewSingleFlight()
	started := make(chan struct{})
	release := make(chan struct{})

	var calls int32
	var wg sync.WaitGroup
	results := make(chan Result, 2)
	run := func() {
		defer wg.Done()
		val, err := sf.Do(context.Background(), "key", func() (interface{}, error) {
			atomic.AddInt32(&calls, 1)
			close(started)
			<-release
			return "value", nil
		})
		results <- Result{Val: val, Err: err}
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

func TestSingleFlightWaiterCanCancel(t *testing.T) {
	sf := NewSingleFlight()
	started := make(chan struct{})
	release := make(chan struct{})

	done := make(chan Result, 1)
	go func() {
		val, err := sf.Do(context.Background(), "key", func() (interface{}, error) {
			close(started)
			<-release
			return "value", nil
		})
		done <- Result{Val: val, Err: err}
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if val, err := sf.Do(ctx, "key", func() (interface{}, error) {
		t.Fatal("duplicate call should not execute")
		return nil, nil
	}); !errors.Is(err, context.DeadlineExceeded) || val != nil {
		t.Fatalf("cancelled waiter result = %v, %v", val, err)
	}

	close(release)
	result := <-done
	if result.Err != nil || result.Val != "value" {
		t.Fatalf("leader result = %+v", result)
	}
}
