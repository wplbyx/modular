package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrResourceNotReady 表示资源尚未成功初始化或已经关闭。
var ErrResourceNotReady = errors.New("resource is not ready")

type managedResourceState uint8

const (
	managedResourceNew managedResourceState = iota
	managedResourceSettingUp
	managedResourceReady
	managedResourceClosing
	managedResourceClosed
)

// ManagedResourceOption 配置 ManagedResource 的可选行为。
type ManagedResourceOption[T any] func(*ManagedResource[T])

// WithResourceCheck 为资源配置就绪检查。
func WithResourceCheck[T any](check func(context.Context, T) error) ManagedResourceOption[T] {
	return func(resource *ManagedResource[T]) {
		resource.check = check
	}
}

// ManagedResource 集中管理一个带值资源的初始化、关闭和值访问。
//
// Setup 成功前和 Close 调用后，Value 与 Check 均返回 ErrResourceNotReady。
// Setup 可在失败后重试；成功后重复调用不会重复创建资源。Close 最多执行一次，
// 并缓存关闭结果供后续调用返回。
type ManagedResource[T any] struct {
	name  string
	setup func(context.Context) (T, error)
	close func(context.Context, T) error
	check func(context.Context, T) error

	mu       sync.Mutex
	state    managedResourceState
	value    T
	closeErr error
	wait     chan struct{}
}

// NewManagedResource 创建一个带类型安全值访问能力的生命周期资源。
func NewManagedResource[T any](
	name string,
	setup func(context.Context) (T, error),
	close func(context.Context, T) error,
	options ...ManagedResourceOption[T],
) *ManagedResource[T] {
	resource := &ManagedResource[T]{
		name:  name,
		setup: setup,
		close: close,
	}
	for _, option := range options {
		if option != nil {
			option(resource)
		}
	}
	return resource
}

// Name 返回用于日志区分的资源名称。
func (resource *ManagedResource[T]) Name() string {
	return resource.name
}

// Setup 初始化资源。成功初始化后重复调用不会再次执行 setup。
func (resource *ManagedResource[T]) Setup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		resource.mu.Lock()
		switch resource.state {
		case managedResourceReady:
			resource.mu.Unlock()
			return nil
		case managedResourceClosed:
			resource.mu.Unlock()
			return fmt.Errorf("setup resource %s: %w", resource.name, ErrResourceNotReady)
		case managedResourceSettingUp, managedResourceClosing:
			wait := resource.wait
			resource.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wait:
			}
			continue
		}
		if resource.setup == nil {
			resource.mu.Unlock()
			return fmt.Errorf("setup resource %s: setup function is nil", resource.name)
		}
		resource.state = managedResourceSettingUp
		resource.wait = make(chan struct{})
		wait := resource.wait
		resource.mu.Unlock()

		value, err := resource.setup(ctx)

		resource.mu.Lock()
		if err != nil {
			resource.state = managedResourceNew
		} else {
			resource.value = value
			resource.state = managedResourceReady
		}
		resource.wait = nil
		close(wait)
		resource.mu.Unlock()
		return err
	}
}

// Value 返回已初始化的资源值。
func (resource *ManagedResource[T]) Value() (T, error) {
	resource.mu.Lock()
	defer resource.mu.Unlock()

	if resource.state != managedResourceReady {
		var zero T
		return zero, fmt.Errorf("resource %s: %w", resource.name, ErrResourceNotReady)
	}
	return resource.value, nil
}

// Check 检查资源是否就绪以及底层依赖是否健康。
func (resource *ManagedResource[T]) Check(ctx context.Context) error {
	resource.mu.Lock()
	if resource.state != managedResourceReady {
		resource.mu.Unlock()
		return fmt.Errorf("resource %s: %w", resource.name, ErrResourceNotReady)
	}
	check := resource.check
	value := resource.value
	resource.mu.Unlock()
	if check == nil {
		return nil
	}
	return check(ctx, value)
}

// Close 关闭资源。底层 close 最多执行一次，关闭结果会被缓存。
func (resource *ManagedResource[T]) Close(ctx context.Context) error {
	for {
		resource.mu.Lock()
		switch resource.state {
		case managedResourceClosed:
			err := resource.closeErr
			resource.mu.Unlock()
			return err
		case managedResourceNew:
			resource.mu.Unlock()
			return nil
		case managedResourceSettingUp, managedResourceClosing:
			wait := resource.wait
			resource.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wait:
			}
			continue
		}

		value := resource.value
		closeFn := resource.close
		resource.state = managedResourceClosing
		resource.wait = make(chan struct{})
		wait := resource.wait
		resource.mu.Unlock()

		var closeErr error
		if closeFn != nil {
			closeErr = closeFn(ctx, value)
		}

		resource.mu.Lock()
		resource.closeErr = closeErr
		var zero T
		resource.value = zero
		resource.state = managedResourceClosed
		resource.wait = nil
		close(wait)
		resource.mu.Unlock()
		return closeErr
	}
}

type funcResource struct {
	name  string
	setup func(context.Context) error
	close func(context.Context) error
}

// NewFuncResource 将初始化和关闭函数适配为 Resource。
func NewFuncResource(
	name string,
	setup func(context.Context) error,
	close func(context.Context) error,
) Resource {
	return &funcResource{name: name, setup: setup, close: close}
}

func (resource *funcResource) Name() string {
	return resource.name
}

func (resource *funcResource) Setup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if resource.setup == nil {
		return nil
	}
	return resource.setup(ctx)
}

func (resource *funcResource) Close(ctx context.Context) error {
	if resource.close == nil {
		return nil
	}
	return resource.close(ctx)
}
