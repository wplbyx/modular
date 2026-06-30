package kafka

import (
	"time"

	"github.com/segmentio/kafka-go"
)

// ProducerOptions contains options for Kafka producer
type ProducerOptions struct {
	Brokers      []string
	Topic        string
	BatchSize    int
	BatchTimeout time.Duration
	Compression  kafka.Compression
	RequiredAcks kafka.RequiredAcks
}

// DefaultProducerOptions returns default producer options
func DefaultProducerOptions() *ProducerOptions {
	return &ProducerOptions{
		BatchSize:    100,
		BatchTimeout: 10 * time.Second,
		Compression:  kafka.Gzip,
		RequiredAcks: kafka.RequireOne,
	}
}

// ProducerOption is a function that configures producer options
type ProducerOption func(*ProducerOptions)

// WithBrokers sets the Kafka broker addresses
func WithBrokers(brokers ...string) ProducerOption {
	return func(o *ProducerOptions) {
		o.Brokers = brokers
	}
}

// WithTopic sets the default topic
func WithTopic(topic string) ProducerOption {
	return func(o *ProducerOptions) {
		o.Topic = topic
	}
}

// WithBatchSize sets the batch size
func WithBatchSize(size int) ProducerOption {
	return func(o *ProducerOptions) {
		o.BatchSize = size
	}
}

// WithBatchTimeout sets the batch timeout
func WithBatchTimeout(timeout time.Duration) ProducerOption {
	return func(o *ProducerOptions) {
		o.BatchTimeout = timeout
	}
}

// ConsumerOptions contains options for Kafka consumer
type ConsumerOptions struct {
	Brokers        []string
	GroupID        string
	Topic          string
	MinBytes       int
	MaxBytes       int
	CommitInterval time.Duration
	StartOffset    int64
	Workers        int
	MaxRetries     int
	RetryBackoff   time.Duration
	DLQTopic       string
	DLQProducer    *ProducerOptions
}

// DefaultConsumerOptions returns default consumer options
func DefaultConsumerOptions() *ConsumerOptions {
	return &ConsumerOptions{
		MinBytes:       1,
		MaxBytes:       10e6, // 10MB
		CommitInterval: 0,    // Manual commit
		StartOffset:    -1,   // Latest
		Workers:        1,
		MaxRetries:     0,
		RetryBackoff:   100 * time.Millisecond,
	}
}

// ConsumerOption is a function that configures consumer options
type ConsumerOption func(*ConsumerOptions)

// WithConsumerBrokers sets the Kafka broker addresses
func WithConsumerBrokers(brokers ...string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Brokers = brokers
	}
}

// WithGroupID sets the consumer group ID
func WithGroupID(groupID string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.GroupID = groupID
	}
}

// WithConsumerTopic sets the topic to consume
func WithConsumerTopic(topic string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Topic = topic
	}
}

// WithStartOffset sets the start offset
func WithStartOffset(offset int64) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.StartOffset = offset
	}
}

// WithConsumerWorkers sets the number of concurrent consumer workers.
func WithConsumerWorkers(workers int) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Workers = workers
	}
}

// WithConsumerRetries sets handler retry behavior before a message is sent to DLQ or left uncommitted.
func WithConsumerRetries(maxRetries int, backoff time.Duration) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.MaxRetries = maxRetries
		if backoff > 0 {
			o.RetryBackoff = backoff
		}
	}
}

// WithConsumerDLQ configures a dead-letter topic for messages that exhaust retries.
func WithConsumerDLQ(topic string, producer *ProducerOptions) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.DLQTopic = topic
		o.DLQProducer = producer
	}
}
