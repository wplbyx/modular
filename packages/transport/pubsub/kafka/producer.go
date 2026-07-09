package kafka

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

// Ensure Producer implements pubsub.Publisher interface
var _ pubsub.Publisher = (*Producer)(nil)

// Producer implements pubsub.Publisher using Kafka
type Producer struct {
	writer *kafka.Writer
	topic  string
}

// NewProducer creates a new Kafka producer
func NewProducer(opts ...ProducerOption) (*Producer, error) {
	o := DefaultProducerOptions()
	for _, opt := range opts {
		opt(o)
	}

	if len(o.Brokers) == 0 {
		return nil, fmt.Errorf("kafka brokers cannot be empty")
	}

	var addr net.Addr
	if len(o.Brokers) == 1 {
		addr = kafka.TCP(o.Brokers[0])
	} else {
		addr = kafka.TCP(o.Brokers...)
	}

	return &Producer{
		topic: o.Topic,
		writer: &kafka.Writer{
			Addr:                   addr,
			Topic:                  o.Topic,
			Balancer:               &kafka.Hash{},
			Async:                  false,
			BatchSize:              o.BatchSize,
			BatchTimeout:           o.BatchTimeout,
			RequiredAcks:           o.RequiredAcks,
			Compression:            o.Compression,
			ReadTimeout:            30 * time.Second,
			WriteTimeout:           30 * time.Second,
			AllowAutoTopicCreation: false,
		},
	}, nil
}

// Publish publishes a message to Kafka
func (p *Producer) Publish(ctx context.Context, topic string, payload []byte, opts ...pubsub.PublishOption) error {
	// Apply options
	publishOpts := &pubsub.PublishOptions{}
	for _, opt := range opts {
		opt(publishOpts)
	}

	// Use provided topic or default
	if topic == "" {
		topic = p.topic
	}

	// Build message
	msg := kafka.Message{
		Topic: topic,
		Value: payload,
	}

	if publishOpts.Key != "" {
		msg.Key = []byte(publishOpts.Key)
	}

	// Add headers
	if len(publishOpts.Headers) > 0 {
		msg.Headers = make([]kafka.Header, 0, len(publishOpts.Headers))
		for k, v := range publishOpts.Headers {
			msg.Headers = append(msg.Headers, kafka.Header{Key: k, Value: []byte(v)})
		}
	}

	err := p.writer.WriteMessages(ctx, msg)
	if err != nil {
		log.Errorf("Failed to send message to Kafka (topic: %s): %v", topic, err)
		return fmt.Errorf("kafka producer send message failed: %w", err)
	}

	return nil
}

// Close closes the producer
func (p *Producer) Close() error {
	log.Info("Kafka producer closing...")
	if p.writer != nil {
		return p.writer.Close()
	}
	return nil
}

// Ensure Producer implements io.Closer
var _ io.Closer = (*Producer)(nil)
