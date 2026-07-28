// Package transport defines protocol-neutral transport policy.
package transport

import (
	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/metadata"
	"github.com/wplbyx/modular/packages/resilience"
)

// Policy centralizes the middleware decisions owned by a process cmd.
// Protocol packages adapt this policy but do not mutate it.
type Policy struct {
	serviceName string
	logger      log.Logger
	propagator  *metadata.Propagator
	protection  resilience.Protection
	tracing     bool
	accessLog   bool
}

// PolicyOption configures a transport Policy before it is shared.
type PolicyOption func(*Policy)

// NewPolicy creates the out-of-box transport policy: metadata propagation,
// tracing, access logs, and adaptive BBR/SRE protection are enabled.
func NewPolicy(serviceName string, options ...PolicyOption) *Policy {
	policy := &Policy{
		serviceName: serviceName,
		logger:      log.Default(),
		propagator:  metadata.NewPropagator(),
		protection:  resilience.NewAdaptiveProtection(resilience.DefaultAdaptiveConfig()),
		tracing:     true,
		accessLog:   true,
	}
	for _, option := range options {
		if option != nil {
			option(policy)
		}
	}
	return policy
}

// WithLogger injects the mandatory-context logger used by transport adapters.
func WithLogger(logger log.Logger) PolicyOption {
	return func(policy *Policy) {
		if logger != nil {
			policy.logger = logger
		}
	}
}

// WithPropagator replaces the metadata and trace propagation policy.
func WithPropagator(propagator *metadata.Propagator) PolicyOption {
	return func(policy *Policy) {
		if propagator != nil {
			policy.propagator = propagator
		}
	}
}

// WithProtection replaces adaptive admission. A nil value disables it.
func WithProtection(protection resilience.Protection) PolicyOption {
	return func(policy *Policy) { policy.protection = protection }
}

// WithTracing enables or disables OpenTelemetry instrumentation.
func WithTracing(enabled bool) PolicyOption {
	return func(policy *Policy) { policy.tracing = enabled }
}

// WithAccessLog enables or disables transport completion logs.
func WithAccessLog(enabled bool) PolicyOption {
	return func(policy *Policy) { policy.accessLog = enabled }
}

func (p *Policy) ServiceName() string {
	if p == nil || p.serviceName == "" {
		return "modular"
	}
	return p.serviceName
}

func (p *Policy) Logger() log.Logger {
	if p == nil || p.logger == nil {
		return log.Default()
	}
	return p.logger
}

func (p *Policy) Propagator() *metadata.Propagator {
	if p == nil || p.propagator == nil {
		return metadata.NewPropagator()
	}
	return p.propagator
}

func (p *Policy) Protection() resilience.Protection {
	if p == nil {
		return nil
	}
	return p.protection
}

func (p *Policy) TracingEnabled() bool { return p != nil && p.tracing }

func (p *Policy) AccessLogEnabled() bool { return p != nil && p.accessLog }
