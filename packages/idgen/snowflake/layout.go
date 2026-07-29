package snowflake

import (
	"fmt"
	"time"

	"github.com/wplbyx/modular/packages/idgen"
)

const snowflakeBits = 63

// Layout defines an immutable positive-int64 Snowflake bit allocation.
type Layout struct {
	Epoch        time.Time
	Unit         time.Duration
	TimeBits     uint8
	NodeBits     uint8
	SequenceBits uint8
}

// DefaultLayout returns a 41/10/12 millisecond layout with a fixed 2020 epoch.
func DefaultLayout() Layout {
	return Layout{
		Epoch:        time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		Unit:         time.Millisecond,
		TimeBits:     41,
		NodeBits:     10,
		SequenceBits: 12,
	}
}

// Validate rejects layouts that cannot fit in a positive int64.
func (layout Layout) Validate() error {
	if layout.Epoch.IsZero() {
		return fmt.Errorf("%w: epoch is zero", idgen.ErrInvalidConfig)
	}
	if layout.Unit <= 0 {
		return fmt.Errorf("%w: time unit must be positive", idgen.ErrInvalidConfig)
	}
	if layout.TimeBits == 0 || layout.NodeBits == 0 || layout.SequenceBits == 0 {
		return fmt.Errorf("%w: all bit allocations must be positive", idgen.ErrInvalidConfig)
	}
	if int(layout.TimeBits)+int(layout.NodeBits)+int(layout.SequenceBits) != snowflakeBits {
		return fmt.Errorf("%w: bit allocations must total %d", idgen.ErrInvalidConfig, snowflakeBits)
	}
	return nil
}

// MaxNodeID returns the largest node ID representable by this layout.
func (layout Layout) MaxNodeID() uint64 { return maxForBits(layout.NodeBits) }

// MaxSequence returns the largest per-unit sequence value.
func (layout Layout) MaxSequence() uint64 { return maxForBits(layout.SequenceBits) }

func (layout Layout) maxTimestamp() uint64 { return maxForBits(layout.TimeBits) }

func maxForBits(bits uint8) uint64 { return (uint64(1) << bits) - 1 }
