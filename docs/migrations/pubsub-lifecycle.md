# Pub/Sub consumption and shutdown migration

Existing public signatures remain available. MQTT, Redis Channel and RocketMQ
PushConsumer gain bounded processing, context-aware shutdown and Stats(). Kafka
and Redis Streams retain their existing policies.

## Delivery

| Client | Processing | Failure and confirmation |
| --- | --- | --- |
| MQTT | 8 workers, 256 pending tasks by default | SDK auto-ACK disabled; success ACKs, error/panic retries locally with cancellable jittered backoff (100 ms initial, 30 s cap) |
| Redis Channel | 8 workers; ChannelSize pending tasks (default 100) | Errors/panics are logged and counted, without retry; Redis Pub/Sub has no ACK or persistence |
| RocketMQ | Existing SDK Threads/MaxCache bounds | Errors, panics and missing handlers return FAILURE; Broker governs retry/DLQ; only success returns SUCCESS |

Full MQTT/Redis queues apply backpressure. Limits bound task counts, not payload
bytes or Broker buffers. Prolonged backpressure can cause disconnects. QoS 0 and
Redis Channels remain best-effort. MQTT QoS 1/2 recovery needs stable ClientID,
persistent session, suitable Broker retention and SDK store. Keep business
handlers idempotent; this is not an exactly-once business guarantee.

MQTT WithOrderMatters(true) uses one business worker. SDK callbacks only enqueue.
Reconnect preserves each subscription's QoS; failed/removed subscriptions are not
restored. Persistent-session deliveries arriving before handler registration
wait in the same bounded queue. Unknown topics can occupy workers until closing.
Legacy raw default handlers also run through the queue: normal return succeeds,
panic retries, and manual Ack is deferred to the delivery boundary. Avoid blocking
subscription management inside handlers when applying backpressure.

Redis Channel permits one subscription per instance. Unsubscribe removes the
Broker subscription; create another instance for another subscription. The
injected Redis client remains caller-owned and is never closed by ChannelClient.

## Lifecycle

RocketMQ NewPushConsumer now only validates configuration. First Subscribe must
match ConsumerOptions.Topic and installs the handler before SDK construction and
startup. Additional topics may then be added. Network startup errors now come
from Subscribe rather than NewPushConsumer.

SubscriberEndpoint is single-use: duplicate Startup and Startup after Shutdown
fail, failed startup wakes Ready, and Shutdown is idempotent. Subscribers with
pubsub.ContextCloser are closed once through CloseContext instead of invoking
Disconnect plus Close. Legacy subscribers retain their hook/Close path, waiting
for late startup before cleanup.

CloseContext(ctx) and Disconnect(ctx) permanently stop admission, drain accepted
work, and cancel cooperative handlers at the deadline. Close() defaults to a
30-second budget. Recreate a client after closing. Handler context values are
preserved, but cancellation belongs to the consumer lifecycle so Application
cancellation does not skip draining.

SDK operations without cancellation may outlive callers; one operation/cleanup
owner is retained and late startup is cleaned up. Timeout does not guarantee all
goroutines have exited. Non-cooperative handlers cannot be forcibly terminated;
incomplete reliable work is not acknowledged by the adapter.

## Options and statistics

- MQTT: WithWorkers, WithQueueSize, WithCloseTimeout.
- Redis Channel: WithChannelWorkers, WithChannelCloseTimeout. WithChannelSize
  bounds pending business tasks; the receiver now reads directly from the SDK.
- RocketMQ: WithConsumerCloseTimeout; existing Threads/MaxCache remain in use.
- Stats returns pubsub.DeliveryStats: Queued, Active, Succeeded, Failed, Retried,
  Canceled. Failed counts attempts. RocketMQ Retried counts Broker attempts > 1;
  its Queued is zero because the SDK does not expose queue depth. These are
  observational snapshots, not durable delivery receipts.

CI adds Linux race checks and isolated real-Broker integration tests. See
[integration instructions](../../tests/integration/pubsub/README.md).
