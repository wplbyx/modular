package kafka

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProducer_DefaultTopicDoesNotConflictWithMessageTopic(t *testing.T) {
	p, err := NewProducer(WithBrokers("127.0.0.1:1"), WithTopic("events"))
	require.NoError(t, err)
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = p.Publish(ctx, "", []byte("payload"))
	require.ErrorIs(t, err, context.Canceled)
}
