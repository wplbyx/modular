package transport

import "testing"

func TestNewPolicyEnablesOperationalDefaults(t *testing.T) {
	policy := NewPolicy("orders")
	if policy.ServiceName() != "orders" {
		t.Fatalf("service name = %q", policy.ServiceName())
	}
	if policy.Propagator() == nil || policy.Protection() == nil {
		t.Fatal("default propagation and protection must be enabled")
	}
	if !policy.TracingEnabled() || !policy.AccessLogEnabled() {
		t.Fatal("default tracing and access log must be enabled")
	}
}

func TestPolicyAllowsCmdToDisableDefaults(t *testing.T) {
	policy := NewPolicy("orders", WithProtection(nil), WithTracing(false), WithAccessLog(false))
	if policy.Protection() != nil || policy.TracingEnabled() || policy.AccessLogEnabled() {
		t.Fatal("explicit cmd policy was not preserved")
	}
}
