package snowflake

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/idgen"
)

func TestDefaultLayout(t *testing.T) {
	layout := DefaultLayout()
	require.NoError(t, layout.Validate())
	assert.Equal(t, uint64(1023), layout.MaxNodeID())
	assert.Equal(t, uint64(4095), layout.MaxSequence())
}

func TestLayoutValidation(t *testing.T) {
	tests := []Layout{
		{},
		{Epoch: time.Now(), Unit: time.Millisecond, TimeBits: 41, NodeBits: 10, SequenceBits: 11},
		{Epoch: time.Now(), Unit: 0, TimeBits: 41, NodeBits: 10, SequenceBits: 12},
		{Epoch: time.Now(), Unit: time.Millisecond, TimeBits: 0, NodeBits: 10, SequenceBits: 53},
	}
	for _, layout := range tests {
		require.ErrorIs(t, layout.Validate(), idgen.ErrInvalidConfig)
	}
}

func TestGeneratorEncodesLayoutFields(t *testing.T) {
	layout := DefaultLayout()
	now := layout.Epoch.Add(42 * layout.Unit)
	lease := newFakeLease(7)
	generator := newWithLease(layout, lease, func() time.Time { return now }, waitContext)

	id, err := generator.Generate(context.Background())
	require.NoError(t, err)
	value, err := strconv.ParseUint(id.String(), 10, 64)
	require.NoError(t, err)

	sequenceMask := layout.MaxSequence()
	nodeMask := layout.MaxNodeID()
	assert.Equal(t, uint64(0), value&sequenceMask)
	assert.Equal(t, uint64(7), (value>>layout.SequenceBits)&nodeMask)
	assert.Equal(t, uint64(42), value>>(layout.NodeBits+layout.SequenceBits))
}

func TestGeneratorIsConcurrentAndUnique(t *testing.T) {
	layout := DefaultLayout()
	generator := newWithLease(layout, newFakeLease(1), time.Now, waitContext)

	const count = 2000
	values := make(chan string, count)
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range count / 20 {
				value, err := generator.Generate(context.Background())
				require.NoError(t, err)
				values <- value.String()
			}
		}()
	}
	group.Wait()
	close(values)

	seen := make(map[string]struct{}, count)
	for value := range values {
		if _, exists := seen[value]; exists {
			t.Fatalf("duplicate ID %s", value)
		}
		seen[value] = struct{}{}
	}
	assert.Len(t, seen, count)
}

func TestGeneratorRejectsClockRollback(t *testing.T) {
	layout := DefaultLayout()
	times := []time.Time{
		layout.Epoch.Add(2 * layout.Unit),
		layout.Epoch.Add(layout.Unit),
	}
	var index atomic.Int64
	generator := newWithLease(layout, newFakeLease(0), func() time.Time {
		return times[index.Add(1)-1]
	}, waitContext)

	_, err := generator.Generate(context.Background())
	require.NoError(t, err)
	_, err = generator.Generate(context.Background())
	require.ErrorIs(t, err, idgen.ErrClockMovedBackward)
}

func TestGeneratorWaitsForNextUnitAfterSequenceExhaustion(t *testing.T) {
	layout := Layout{
		Epoch:        time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		Unit:         time.Millisecond,
		TimeBits:     61,
		NodeBits:     1,
		SequenceBits: 1,
	}
	now := layout.Epoch
	var waits atomic.Int64
	generator := newWithLease(layout, newFakeLease(0), func() time.Time { return now }, func(ctx context.Context, duration time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		waits.Add(1)
		now = now.Add(duration)
		return nil
	})

	first, err := generator.Generate(context.Background())
	require.NoError(t, err)
	second, err := generator.Generate(context.Background())
	require.NoError(t, err)
	third, err := generator.Generate(context.Background())
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.NotEqual(t, second, third)
	assert.Equal(t, int64(1), waits.Load())
}

func TestGeneratorStopsWhenLeaseIsLost(t *testing.T) {
	layout := DefaultLayout()
	lease := newFakeLease(0)
	generator := newWithLease(layout, lease, time.Now, waitContext)
	close(lease.lost)

	_, err := generator.Generate(context.Background())
	require.ErrorIs(t, err, idgen.ErrNodeLeaseLost)
}

func TestGeneratorCloseReleasesLeaseAndIsIdempotent(t *testing.T) {
	layout := DefaultLayout()
	lease := newFakeLease(0)
	generator := newWithLease(layout, lease, time.Now, waitContext)
	require.NoError(t, generator.Close(context.Background()))
	require.NoError(t, generator.Close(context.Background()))
	assert.Equal(t, int64(1), lease.closed.Load())
	_, err := generator.Generate(context.Background())
	require.ErrorIs(t, err, idgen.ErrClosed)
}

func TestStaticNodeValidatesLayoutRange(t *testing.T) {
	_, err := New(context.Background(), DefaultLayout(), StaticNode(DefaultLayout().MaxNodeID()+1))
	require.ErrorIs(t, err, idgen.ErrInvalidConfig)
}

func TestGenerateHonorsCanceledContext(t *testing.T) {
	generator, err := New(context.Background(), DefaultLayout(), StaticNode(0))
	require.NoError(t, err)
	t.Cleanup(func() { _ = generator.Close(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = generator.Generate(ctx)
	require.True(t, errors.Is(err, context.Canceled))
}

type fakeLease struct {
	nodeID uint64
	lost   chan struct{}
	closed atomic.Int64
}

func newFakeLease(nodeID uint64) *fakeLease {
	return &fakeLease{nodeID: nodeID, lost: make(chan struct{})}
}

func (lease *fakeLease) NodeID() uint64              { return lease.nodeID }
func (lease *fakeLease) Lost() <-chan struct{}       { return lease.lost }
func (lease *fakeLease) Close(context.Context) error { lease.closed.Add(1); return nil }
