package pubsub

import "context"

// ContextCloser 支持有期限的消费排空与关闭。
type ContextCloser interface{ CloseContext(context.Context) error }

// DeliveryStats 是消费状态的只读快照；Failed 按失败的处理尝试计数。
type DeliveryStats struct {
	Queued    int
	Active    int64
	Succeeded uint64
	Failed    uint64
	Retried   uint64
	Canceled  uint64
}
