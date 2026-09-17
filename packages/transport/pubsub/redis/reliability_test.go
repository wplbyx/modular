package redis

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

type streamScript struct {
	goredis.UniversalClient
	sent atomic.Bool
	ack  chan struct{}
}

type recoveringStream struct {
	*streamScript
	foreign     bool
	recovered   atomic.Bool
	ackAttempts atomic.Int32
}

func (s *recoveringStream) XReadGroup(ctx context.Context, a *goredis.XReadGroupArgs) *goredis.XStreamSliceCmd {
	if !s.foreign && a.Streams[1] == "0" && s.recovered.CompareAndSwap(false, true) {
		cmd := goredis.NewXStreamSliceCmd(ctx)
		cmd.SetVal([]goredis.XStream{{Stream: "events", Messages: []goredis.XMessage{{ID: "old-0", Values: map[string]interface{}{"payload": "recovered"}}}}})
		return cmd
	}
	return s.streamScript.XReadGroup(ctx, a)
}
func (s *recoveringStream) XAutoClaim(ctx context.Context, a *goredis.XAutoClaimArgs) *goredis.XAutoClaimCmd {
	cmd := goredis.NewXAutoClaimCmd(ctx)
	if s.foreign && s.recovered.CompareAndSwap(false, true) {
		cmd.SetVal([]goredis.XMessage{{ID: "old-0", Values: map[string]interface{}{"payload": "recovered"}}}, "0-0")
	} else {
		cmd.SetVal(nil, "0-0")
	}
	return cmd
}
func (s *recoveringStream) XAck(ctx context.Context, stream, group string, ids ...string) *goredis.IntCmd {
	if s.ackAttempts.Add(1) == 1 {
		return goredis.NewIntResult(0, errors.New("ack unavailable"))
	}
	return s.streamScript.XAck(ctx, stream, group, ids...)
}
func TestStreamClient_RecoversPendingAndRetriesAck(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "own"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			s := &recoveringStream{streamScript: &streamScript{ack: make(chan struct{}, 1)}, foreign: foreign}
			s.sent.Store(true)
			c, err := NewStreamClient(WithStreamClient(s), WithGroup("group"), WithStreamRetries(0, time.Millisecond))
			require.NoError(t, err)
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var calls atomic.Int32
			require.NoError(t, c.Subscribe(ctx, "events", func(_ context.Context, m pubsub.Message) error {
				calls.Add(1)
				if string(m.Payload) != "recovered" {
					return errors.New("unexpected payload")
				}
				return nil
			}))
			select {
			case <-s.ack:
				require.Equal(t, int32(1), calls.Load())
				require.Equal(t, int32(2), s.ackAttempts.Load())
			case <-ctx.Done():
				t.Fatal("pending was not recovered")
			}
		})
	}
}

func (*streamScript) XGroupCreateMkStream(context.Context, string, string, string) *goredis.StatusCmd {
	return goredis.NewStatusResult("OK", nil)
}
func (s *streamScript) XReadGroup(ctx context.Context, a *goredis.XReadGroupArgs) *goredis.XStreamSliceCmd {
	if a.Streams[1] != ">" {
		return goredis.NewXStreamSliceCmd(ctx)
	}
	if s.sent.CompareAndSwap(false, true) {
		cmd := goredis.NewXStreamSliceCmd(ctx)
		cmd.SetVal([]goredis.XStream{{Stream: "events", Messages: []goredis.XMessage{{ID: "1-0", Values: map[string]interface{}{"payload": "one"}}}}})
		return cmd
	}
	<-ctx.Done()
	cmd := goredis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(ctx.Err())
	return cmd
}
func (*streamScript) XAutoClaim(ctx context.Context, a *goredis.XAutoClaimArgs) *goredis.XAutoClaimCmd {
	cmd := goredis.NewXAutoClaimCmd(ctx)
	cmd.SetVal(nil, "0-0")
	return cmd
}
func (s *streamScript) XAck(ctx context.Context, stream, group string, ids ...string) *goredis.IntCmd {
	s.ack <- struct{}{}
	return goredis.NewIntResult(1, nil)
}

func TestStreamClient_RetriesWithoutDeadLetterBeforeAck(t *testing.T) {
	s := &streamScript{ack: make(chan struct{}, 1)}
	c, err := NewStreamClient(WithStreamClient(s), WithGroup("group"), WithStreamRetries(0, time.Millisecond))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer c.Close()
	var attempts atomic.Int32
	require.NoError(t, c.Subscribe(ctx, "events", func(context.Context, pubsub.Message) error {
		if attempts.Add(1) == 1 {
			return errors.New("temporary")
		}
		return nil
	}))
	select {
	case <-s.ack:
		require.GreaterOrEqual(t, attempts.Load(), int32(2))
	case <-ctx.Done():
		t.Fatal("message never acknowledged")
	}
}
