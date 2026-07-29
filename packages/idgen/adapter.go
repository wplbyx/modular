// Package idgen defines opaque business ID generation and node-lease seams.
package idgen

import (
	"context"
	"errors"
)

var (
	ErrInvalidConfig      = errors.New("invalid ID generator configuration")
	ErrClosed             = errors.New("ID generator is closed")
	ErrNodeLeaseLost      = errors.New("ID generator node lease is lost")
	ErrClockMovedBackward = errors.New("ID generator clock moved backward")
	ErrTimestampExhausted = errors.New("ID generator timestamp range is exhausted")
)

// ID is opaque to callers. Its concrete encoding belongs to the Generator.
type ID string

func (id ID) String() string { return string(id) }

// Generator produces globally unique opaque IDs.
type Generator interface {
	Generate(context.Context) (ID, error)
}

// NodeLease owns one Snowflake node partition until Lost is closed.
type NodeLease interface {
	NodeID() uint64
	Lost() <-chan struct{}
	Close(context.Context) error
}

// NodeLeaseProvider acquires a unique node partition from zero through maxNodeID.
type NodeLeaseProvider interface {
	Acquire(context.Context, uint64) (NodeLease, error)
}
