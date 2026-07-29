// Package snowflake provides a configurable leased-node Snowflake generator.
package snowflake

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wplbyx/modular/packages/idgen"
)

// Generator produces positive-int64 Snowflake values encoded as decimal strings.
type Generator struct {
	layout Layout
	lease  idgen.NodeLease

	mu          sync.Mutex
	initialized bool
	lastTime    uint64
	sequence    uint64
	closed      atomic.Bool
	closeErr    error

	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

var _ idgen.Generator = (*Generator)(nil)

// New validates the layout and acquires one node lease.
func New(ctx context.Context, layout Layout, provider idgen.NodeLeaseProvider) (*Generator, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: node lease provider is nil", idgen.ErrInvalidConfig)
	}
	lease, err := provider.Acquire(ctx, layout.MaxNodeID())
	if err != nil {
		return nil, fmt.Errorf("acquire ID generator node lease: %w", err)
	}
	if lease == nil {
		return nil, fmt.Errorf("%w: node lease is nil", idgen.ErrInvalidConfig)
	}
	if lease.NodeID() > layout.MaxNodeID() {
		_ = lease.Close(context.Background())
		return nil, fmt.Errorf("%w: leased node ID %d exceeds maximum %d", idgen.ErrInvalidConfig, lease.NodeID(), layout.MaxNodeID())
	}
	return newWithLease(layout, lease, time.Now, waitContext), nil
}

func newWithLease(
	layout Layout,
	lease idgen.NodeLease,
	now func() time.Time,
	wait func(context.Context, time.Duration) error,
) *Generator {
	return &Generator{layout: layout, lease: lease, now: now, wait: wait}
}

// Generate returns one Snowflake value as an opaque decimal ID.
func (generator *Generator) Generate(ctx context.Context) (idgen.ID, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := generator.available(ctx); err != nil {
			return "", err
		}

		generator.mu.Lock()
		if err := generator.available(ctx); err != nil {
			generator.mu.Unlock()
			return "", err
		}
		now := generator.now()
		if now.Before(generator.layout.Epoch) {
			generator.mu.Unlock()
			return "", fmt.Errorf("%w: current time precedes epoch", idgen.ErrClockMovedBackward)
		}
		timestamp := uint64(now.Sub(generator.layout.Epoch) / generator.layout.Unit)
		if timestamp > generator.layout.maxTimestamp() {
			generator.mu.Unlock()
			return "", idgen.ErrTimestampExhausted
		}
		if generator.initialized && timestamp < generator.lastTime {
			last := generator.lastTime
			generator.mu.Unlock()
			return "", fmt.Errorf("%w: timestamp %d is behind %d", idgen.ErrClockMovedBackward, timestamp, last)
		}

		if generator.initialized && timestamp == generator.lastTime {
			if generator.sequence == generator.layout.MaxSequence() {
				next := generator.layout.Epoch.Add(time.Duration(generator.lastTime+1) * generator.layout.Unit)
				waitFor := next.Sub(now)
				generator.mu.Unlock()
				if waitFor <= 0 {
					waitFor = generator.layout.Unit
				}
				if err := generator.wait(ctx, waitFor); err != nil {
					return "", err
				}
				continue
			}
			generator.sequence++
		} else {
			generator.sequence = 0
		}

		generator.initialized = true
		generator.lastTime = timestamp
		value := timestamp << (generator.layout.NodeBits + generator.layout.SequenceBits)
		value |= generator.lease.NodeID() << generator.layout.SequenceBits
		value |= generator.sequence
		generator.mu.Unlock()

		return idgen.ID(strconv.FormatUint(value, 10)), nil
	}
}

func (generator *Generator) available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if generator.closed.Load() {
		return idgen.ErrClosed
	}
	if lost := generator.lease.Lost(); lost != nil {
		select {
		case <-lost:
			return idgen.ErrNodeLeaseLost
		default:
		}
	}
	return nil
}

// Close releases the node partition and prevents further generation.
func (generator *Generator) Close(ctx context.Context) error {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	if !generator.closed.CompareAndSwap(false, true) {
		return generator.closeErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := generator.lease.Close(ctx); err != nil {
		generator.closeErr = fmt.Errorf("release ID generator node lease: %w", err)
	}
	return generator.closeErr
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
