package registry

import "testing"

func TestParseEndpoint(t *testing.T) {
	protocol, host, port, err := parseEndpoint("grpc://127.0.0.1:50051")
	if err != nil {
		t.Fatalf("parseEndpoint() error = %v", err)
	}
	if protocol != "grpc" || host != "127.0.0.1" || port != 50051 {
		t.Fatalf("parseEndpoint() = %s, %s, %d", protocol, host, port)
	}
}

func TestConsulHealthCheckByProtocol(t *testing.T) {
	httpCheck := consulHealthCheck("http", "127.0.0.1", 8080, map[string]string{
		"health_path":     "/ready",
		"health_interval": "2s",
	})
	if httpCheck.HTTP != "http://127.0.0.1:8080/ready" ||
		httpCheck.Interval != "2s" ||
		httpCheck.TCP != "" ||
		httpCheck.GRPC != "" {
		t.Fatalf("http check = %+v", httpCheck)
	}

	defaultHTTPCheck := consulHealthCheck("http", "127.0.0.1", 8080, nil)
	if defaultHTTPCheck.HTTP != "http://127.0.0.1:8080/check" {
		t.Fatalf("default http check = %+v", defaultHTTPCheck)
	}

	grpcCheck := consulHealthCheck("grpc", "127.0.0.1", 50051, nil)
	if grpcCheck.GRPC != "127.0.0.1:50051" || grpcCheck.HTTP != "" || grpcCheck.TCP != "" {
		t.Fatalf("grpc check = %+v", grpcCheck)
	}

	mqttCheck := consulHealthCheck("mqtt", "127.0.0.1", 1883, nil)
	if mqttCheck.TCP != "127.0.0.1:1883" || mqttCheck.HTTP != "" || mqttCheck.GRPC != "" {
		t.Fatalf("mqtt check = %+v", mqttCheck)
	}
}
