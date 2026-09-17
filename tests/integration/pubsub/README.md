# Pub/Sub integration checks

Real Redis, Mosquitto and RocketMQ Broker/Proxy tests bind ports only to loopback.
The anonymous-access configurations are for tests, not production deployment.

```sh
docker compose -f tests/integration/pubsub/compose.yaml up -d --wait --wait-timeout 240
docker compose -f tests/integration/pubsub/compose.yaml exec -T rocket sh mqadmin updateTopic -n namesrv:9876 -c DefaultCluster -t modular-reliability
docker compose -f tests/integration/pubsub/compose.yaml exec -T rocket sh mqadmin updateSubGroup -n namesrv:9876 -c DefaultCluster -g modular-reliability
MODULAR_TEST_REDIS=127.0.0.1:16379 MODULAR_TEST_MQTT=tcp://127.0.0.1:11883 MODULAR_TEST_ROCKET=127.0.0.1:18081 go test -tags=integration -count=1 -timeout=8m ./tests/integration/pubsub
docker compose -f tests/integration/pubsub/compose.yaml down -v
```

In PowerShell set `$env:NAME='value'` for each variable before running Go.
Run Go on the host: RocketMQ Proxy advertises loopback:18081 to match the published
port. Missing variables or unavailable services fail rather than skip tests.
Use a fresh stack per run for deterministic consumer-group offsets.

Scenarios: MQTT persistent-session unacknowledged redelivery; Redis bounded
concurrency, drain, deadline cancellation and injected-client ownership;
RocketMQ backlog after consumer recreation and failed-handler redelivery.
Unit tests separately cover ACK timing, panic, startup races and late SDK results.

On failure collect Compose logs and RocketMQ's
`/home/rocketmq/logs/rocketmqlogs/proxy.log` before stopping the stack.
GitHub Actions runs integration checks independently from unit/race jobs.
