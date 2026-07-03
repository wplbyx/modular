package concurrency

import (
	"context"
	"errors"
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
