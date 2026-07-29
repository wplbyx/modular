// Package pool provides a bounded, observable worker pool for asynchronous tasks.
package pool

import (
	"context"
	"errors"
	"time"

	"github.com/wplbyx/modular/packages/core"
)

var (
	ErrNotRunning = errors.New("worker pool is not running")
	ErrClosed     = errors.New("worker pool is closed")
	ErrOverloaded = errors.New("worker pool is overloaded")
	ErrInvalid    = errors.New("worker pool configuration is invalid")
)

// Policy defines what happens when all workers are busy.
type Policy uint8

const (
	// Reject rejects a task immediately when no worker is available.
	Reject Policy = iota
	// Queue accepts a task into a bounded process-local queue.
	Queue
)

// Config configures one process-local worker pool.
type Config struct {
	Name           string
	Capacity       int
	Policy         Policy
	QueueCapacity  int
	ExpiryDuration time.Duration
	PreAlloc       bool
}

// WorkerTask is an asynchronous task executed by a WorkerPool.
type WorkerTask func(context.Context) error

// Stats is a point-in-time worker-pool snapshot.
type Stats struct {
	Capacity      int
	QueueCapacity int
	Running       int
	Queued        int64
	Accepted      uint64
	Completed     uint64
	Rejected      uint64
	Canceled      uint64
	Failed        uint64
	Panicked      uint64
}

// WorkerPool is a bounded asynchronous executor managed as a Resource.
type WorkerPool interface {
	core.Resource
	Submit(context.Context, WorkerTask) error
	Stats() Stats
}
