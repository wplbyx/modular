package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"
)

// SnapshotWatcher 每次重新构造 loader，只发布解码和校验成功的新对象。
// options 及其捕获的配置/flags 在 Run 期间必须保持不变。publish 拥有快照，需响应 ctx。
type SnapshotWatcher[T any] struct {
	interval time.Duration
	options  []ConfigureLoaderOption
	errors   chan error
	running  atomic.Bool
}

func NewSnapshotWatcher[T any](interval time.Duration, options ...ConfigureLoaderOption) (*SnapshotWatcher[T], error) {
	if interval <= 0 {
		return nil, errors.New("snapshot interval must be positive")
	}
	if err := validateTarget(new(T)); err != nil {
		return nil, err
	}
	return &SnapshotWatcher[T]{interval: interval, options: append([]ConfigureLoaderOption(nil), options...), errors: make(chan error, 1)}, nil
}

// Errors 提供最新一次加载错误；失败快照不会发布。channel 在 Run 结束后保持可读。
func (w *SnapshotWatcher[T]) Errors() <-chan error { return w.errors }

// Run 首次加载失败直接返回；后续加载失败保留调用方已有快照，继续等待下一次轮询。
func (w *SnapshotWatcher[T]) Run(ctx context.Context, publish func(context.Context, *T) error) error {
	if publish == nil {
		return errors.New("snapshot publisher is nil")
	}
	if !w.running.CompareAndSwap(false, true) {
		return errors.New("snapshot watcher can only run once")
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	var previous [32]byte
	initialized := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, digest, err := w.load(ctx)
		if err != nil {
			if !initialized {
				return err
			}
			select {
			case w.errors <- err:
			default:
				select {
				case <-w.errors:
				default:
				}
				select {
				case w.errors <- err:
				default:
				}
			}
		} else {

			if !initialized || digest != previous {
				if err = ctx.Err(); err != nil {
					return err
				}
				if err = publish(ctx, value); err != nil {
					return err
				}
				previous = digest
				initialized = true
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *SnapshotWatcher[T]) load(ctx context.Context) (*T, [32]byte, error) {
	type result struct {
		value  *T
		digest [32]byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		value := new(T)
		loader, err := NewConfigureLoader(w.options...)
		var digest [32]byte
		if err == nil {
			err = loader.Load(value)
		}
		if err == nil {
			var encoded []byte
			encoded, err = json.Marshal(loader.v.AllSettings())
			digest = sha256.Sum256(encoded)
		}
		done <- result{value, digest, err}
	}()
	select {
	case loaded := <-done:
		return loaded.value, loaded.digest, loaded.err
	case <-ctx.Done():
		return nil, [32]byte{}, ctx.Err()
	}
}
