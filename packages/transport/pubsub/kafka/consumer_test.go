package kafka

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

type scriptedReader struct {
	messages   chan kafkalib.Message
	commits    chan int64
	mu         sync.Mutex
	failCommit bool
}

func (r *scriptedReader) FetchMessage(ctx context.Context) (kafkalib.Message, error) {
	select {
	case m := <-r.messages:
		return m, nil
	case <-ctx.Done():
		return kafkalib.Message{}, ctx.Err()
	}
}
func (r *scriptedReader) CommitMessages(ctx context.Context, messages ...kafkalib.Message) error {
	r.mu.Lock()
	fail := r.failCommit
	r.failCommit = false
	r.mu.Unlock()
	if fail {
		return errors.New("commit unavailable")
	}
	for _, m := range messages {
		select {
		case r.commits <- m.Offset:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (*scriptedReader) Close() error { return nil }

func TestConsumer_FailedMessagePrecedesLaterCommit(t *testing.T) {
	r := &scriptedReader{messages: make(chan kafkalib.Message, 2), commits: make(chan int64, 4), failCommit: true}
	r.messages <- kafkalib.Message{Partition: 0, Offset: 10, Value: []byte("first")}
	r.messages <- kafkalib.Message{Partition: 0, Offset: 11, Value: []byte("second")}
	c, err := NewConsumer(WithConsumerBrokers("unused"), WithConsumerTopic("events"), WithGroupID("group"), WithConsumerWorkers(2), WithConsumerReader(r), WithConsumerRetries(0, time.Millisecond))
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	attempts := 0
	require.NoError(t, c.Subscribe(ctx, "events", func(ctx context.Context, m pubsub.Message) error {
		if string(m.Payload) == "first" {
			attempts++
			if attempts == 1 {
				return errors.New("temporary")
			}
		}
		return nil
	}))
	for _, want := range []int64{10, 11} {
		select {
		case got := <-r.commits:
			require.Equal(t, want, got)
		case <-ctx.Done():
			t.Fatal("missing commit")
		}
	}
	require.Error(t, c.Subscribe(ctx, "events", func(context.Context, pubsub.Message) error { return nil }))
}
