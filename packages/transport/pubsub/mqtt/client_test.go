package mqtt

import (
	"context"
	"errors"
	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type token struct {
	err  error
	done chan struct{}
}

func completed(err error) *token       { d := make(chan struct{}); close(d); return &token{err, d} }
func (t *token) Done() <-chan struct{} { return t.done }
func (t *token) Error() error          { return t.err }
func (t *token) Wait() bool            { <-t.done; return true }
func (t *token) WaitTimeout(d time.Duration) bool {
	select {
	case <-t.done:
		return true
	case <-time.After(d):
		return false
	}
}

type fakeClient struct {
	paho.Client
	mu           sync.Mutex
	handlers     map[string]paho.MessageHandler
	qos          []byte
	subscribeErr error
	disconnected atomic.Int32
}

func (f *fakeClient) Subscribe(topic string, qos byte, h paho.MessageHandler) paho.Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qos = append(f.qos, qos)
	f.handlers[topic] = h
	return completed(f.subscribeErr)
}
func (f *fakeClient) Unsubscribe(topics ...string) paho.Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range topics {
		delete(f.handlers, s)
	}
	return completed(nil)
}
func (f *fakeClient) Disconnect(uint) { f.disconnected.Add(1) }
func (f *fakeClient) emit(topic string, m paho.Message) {
	f.mu.Lock()
	h := f.handlers[topic]
	f.mu.Unlock()
	h(f, m)
}

type message struct {
	paho.Message
	data []byte
	ack  chan struct{}
	once sync.Once
}

func (m *message) Topic() string   { return "orders" }
func (m *message) Payload() []byte { return m.data }
func (m *message) Ack()            { m.once.Do(func() { close(m.ack) }) }
func newTestClient(t *testing.T, opts ...Option) (*MQTTClient, *fakeClient) {
	t.Helper()
	c, e := NewClient(append([]Option{WithBrokerURL("tcp://localhost:1883")}, opts...)...)
	require.NoError(t, e)
	f := &fakeClient{handlers: map[string]paho.MessageHandler{}}
	c.client = f
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c, f
}
func TestClient_RetryBeforeAckAndCopy(t *testing.T) {
	c, f := newTestClient(t, WithOrderMatters(true))
	entered, release := make(chan struct{}), make(chan struct{})
	var n atomic.Int32
	require.NoError(t, c.Subscribe(context.Background(), "orders", func(ctx context.Context, m pubsub.Message) error {
		if n.Add(1) == 1 {
			close(entered)
			<-release
			return errors.New("retry")
		}
		if string(m.Payload) != "original" {
			return errors.New("payload changed")
		}
		return nil
	}))
	m := &message{data: []byte("original"), ack: make(chan struct{})}
	f.emit("orders", m)
	<-entered
	copy(m.data, []byte("modified"))
	select {
	case <-m.ack:
		t.Fatal("early ack")
	default:
	}
	close(release)
	select {
	case <-m.ack:
	case <-time.After(time.Second):
		t.Fatal("missing ack")
	}
	require.NoError(t, c.Close())
	require.EqualValues(t, 1, c.Stats().Retried)
	require.EqualValues(t, 1, f.disconnected.Load())
}
func TestClient_RestoreQoSAndRemoveFailedSubscription(t *testing.T) {
	c, f := newTestClient(t)
	handler := func(context.Context, pubsub.Message) error { return nil }
	require.NoError(t, c.Subscribe(context.Background(), "orders", handler, func(o *pubsub.SubscribeOptions) { o.QoS = 2 }))
	c.restore(f)
	require.Eventually(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.qos) == 2 }, time.Second, time.Millisecond)
	f.mu.Lock()
	require.Equal(t, []byte{2, 2}, f.qos)
	f.mu.Unlock()
	require.NoError(t, c.Unsubscribe(context.Background(), "orders"))
	f.mu.Lock()
	f.subscribeErr = errors.New("rejected")
	f.mu.Unlock()
	require.Error(t, c.Subscribe(context.Background(), "failed", handler))
	c.mu.RLock()
	require.Empty(t, c.subscriptions)
	c.mu.RUnlock()
}
func TestClient_CloseDeadlineDoesNotAck(t *testing.T) {
	c, f := newTestClient(t)
	entered := make(chan struct{})
	require.NoError(t, c.Subscribe(context.Background(), "orders", func(ctx context.Context, _ pubsub.Message) error { close(entered); <-ctx.Done(); return nil }))
	m := &message{data: []byte("x"), ack: make(chan struct{})}
	f.emit("orders", m)
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.CloseContext(ctx), context.Canceled)
	require.NoError(t, c.Close())
	select {
	case <-m.ack:
		t.Fatal("canceled delivery acknowledged")
	default:
	}
	require.Error(t, c.Subscribe(context.Background(), "orders", func(context.Context, pubsub.Message) error { return nil }))
}

func TestClient_OrderedDeliveryOverridesWorkerCount(t *testing.T) {
	c, f := newTestClient(t, WithWorkers(8), WithOrderMatters(true))
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	require.NoError(t, c.Subscribe(context.Background(), "orders", func(context.Context, pubsub.Message) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}))
	f.emit("orders", &message{data: []byte("first"), ack: make(chan struct{})})
	<-entered
	f.emit("orders", &message{data: []byte("second"), ack: make(chan struct{})})
	require.EqualValues(t, 1, c.Stats().Active)
	require.Equal(t, 1, c.Stats().Queued)
	close(release)
	require.NoError(t, c.Close())
	require.EqualValues(t, 2, calls.Load())
}

func TestClient_CanceledOperationDoesNotWaitForOtherSubscription(t *testing.T) {
	c, _ := newTestClient(t)
	c.opGate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.Subscribe(ctx, "orders", func(context.Context, pubsub.Message) error { return nil }), context.Canceled)
	<-c.opGate
}
