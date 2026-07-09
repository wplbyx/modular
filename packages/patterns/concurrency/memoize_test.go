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

func TestMemoizerGetOrLoadCollapsesConcurrentMisses(t *testing.T) {
	m := NewMemoizer[string, string](time.Minute)
	var loads int32

	var wg sync.WaitGroup
	results := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m.GetOrLoad(context.Background(), "key", func(context.Context) (string, error) {
				atomic.AddInt32(&loads, 1)
				time.Sleep(20 * time.Millisecond)
				return "value", nil
			})
			if err != nil {
				t.Errorf("GetOrLoad() error = %v", err)
				return
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)

	for got := range results {
		if got != "value" {
			t.Fatalf("GetOrLoad() = %q", got)
		}
	}
	if got := atomic.LoadInt32(&loads); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
}
