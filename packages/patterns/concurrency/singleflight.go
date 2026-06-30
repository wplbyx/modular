package concurrency

import (
	"context"
	"errors"
	"sync"
)

// SingleFlight prevents duplicate function executions
// When multiple goroutines call the same key, only one execution runs
type SingleFlight struct {
	mu    sync.Mutex
	calls map[string]*call
}

type call struct {
	done chan struct{}
	val  interface{}
	err  error
}

// NewSingleFlight creates a new SingleFlight instance
func NewSingleFlight() *SingleFlight {
	return &SingleFlight{
		calls: make(map[string]*call),
	}
}

// Do executes the function for the given key, ensuring only one execution per key
func (sf *SingleFlight) Do(ctx context.Context, key string, fn func() (interface{}, error)) (interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return nil, errors.New("singleflight function is nil")
	}

	sf.mu.Lock()
	if sf.calls == nil {
		sf.calls = make(map[string]*call)
	}

	if c, ok := sf.calls[key]; ok {
		sf.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return c.val, c.err
		}
	}

	c := &call{done: make(chan struct{})}
	sf.calls[key] = c
	sf.mu.Unlock()

	defer func() {
		close(c.done)
		sf.mu.Lock()
		delete(sf.calls, key)
		sf.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		c.err = ctx.Err()
	default:
		c.val, c.err = fn()
	}

	return c.val, c.err
}

// DoChan executes the function and returns a channel for the result
func (sf *SingleFlight) DoChan(ctx context.Context, key string, fn func() (interface{}, error)) <-chan Result {
	ch := make(chan Result, 1)

	go func() {
		defer close(ch)
		val, err := sf.Do(ctx, key, fn)
		ch <- Result{Val: val, Err: err}
	}()

	return ch
}

// Result represents the result of a SingleFlight execution
type Result struct {
	Val interface{}
	Err error
}

// Forget removes the key from the in-flight tracking
func (sf *SingleFlight) Forget(key string) {
	sf.mu.Lock()
	delete(sf.calls, key)
	sf.mu.Unlock()
}
