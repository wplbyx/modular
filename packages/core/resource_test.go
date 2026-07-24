package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedResource_Lifecycle(t *testing.T) {
	var setupCalls atomic.Int32
	var closeCalls atomic.Int32
	resource := NewManagedResource(
		"database",
		func(context.Context) (string, error) {
			setupCalls.Add(1)
			return "connection", nil
		},
		func(_ context.Context, value string) error {
			closeCalls.Add(1)
			require.Equal(t, "connection", value)
			return nil
		},
		WithResourceCheck(func(_ context.Context, value string) error {
			require.Equal(t, "connection", value)
			return nil
		}),
	)

	_, err := resource.Value()
	require.ErrorIs(t, err, ErrResourceNotReady)
	require.ErrorIs(t, resource.Check(context.Background()), ErrResourceNotReady)

	require.NoError(t, resource.Setup(context.Background()))
	require.NoError(t, resource.Setup(context.Background()))
	value, err := resource.Value()
	require.NoError(t, err)
	assert.Equal(t, "connection", value)
	require.NoError(t, resource.Check(context.Background()))
	assert.EqualValues(t, 1, setupCalls.Load())

	require.NoError(t, resource.Close(context.Background()))
	require.NoError(t, resource.Close(context.Background()))
	assert.EqualValues(t, 1, closeCalls.Load())
	_, err = resource.Value()
	require.ErrorIs(t, err, ErrResourceNotReady)
	require.ErrorIs(t, resource.Setup(context.Background()), ErrResourceNotReady)
}

func TestManagedResource_SetupCanRetryAfterFailure(t *testing.T) {
	setupErr := errors.New("unavailable")
	var calls atomic.Int32
	resource := NewManagedResource(
		"cache",
		func(context.Context) (int, error) {
			if calls.Add(1) == 1 {
				return 0, setupErr
			}
			return 42, nil
		},
		func(context.Context, int) error { return nil },
	)

	require.ErrorIs(t, resource.Setup(context.Background()), setupErr)
	require.NoError(t, resource.Setup(context.Background()))
	value, err := resource.Value()
	require.NoError(t, err)
	assert.Equal(t, 42, value)
}

func TestManagedResource_CloseIsConcurrentSafe(t *testing.T) {
	var closeCalls atomic.Int32
	resource := NewManagedResource(
		"resource",
		func(context.Context) (int, error) { return 1, nil },
		func(context.Context, int) error {
			closeCalls.Add(1)
			return errors.New("close failed")
		},
	)
	require.NoError(t, resource.Setup(context.Background()))

	const callers = 10
	errs := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			errs <- resource.Close(context.Background())
		}()
	}
	group.Wait()
	close(errs)

	for err := range errs {
		assert.EqualError(t, err, "close failed")
	}
	assert.EqualValues(t, 1, closeCalls.Load())
}

func TestManagedResource_CloseBeforeSetupIsNoop(t *testing.T) {
	resource := NewManagedResource(
		"resource",
		func(context.Context) (int, error) { return 1, nil },
		func(context.Context, int) error { return nil },
	)

	require.NoError(t, resource.Close(context.Background()))
	require.NoError(t, resource.Setup(context.Background()))
}

func TestManagedResource_SetupHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool
	resource := NewManagedResource(
		"resource",
		func(context.Context) (int, error) {
			called.Store(true)
			return 1, nil
		},
		func(context.Context, int) error { return nil },
	)

	require.ErrorIs(t, resource.Setup(ctx), context.Canceled)
	assert.False(t, called.Load())
}

func TestFuncResource(t *testing.T) {
	var events []string
	resource := NewFuncResource(
		"migration",
		func(context.Context) error {
			events = append(events, "setup")
			return nil
		},
		func(context.Context) error {
			events = append(events, "close")
			return nil
		},
	)

	assert.Equal(t, "migration", resource.Name())
	require.NoError(t, resource.Setup(context.Background()))
	require.NoError(t, resource.Close(context.Background()))
	assert.Equal(t, []string{"setup", "close"}, events)
}
