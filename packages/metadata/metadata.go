// Package metadata provides immutable request metadata with explicit
// propagation scopes.
package metadata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	RequestIDKey   = "x-request-id"
	LanguageKey    = "x-language"
	GlobalPrefix   = "x-md-global-"
	LocalPrefix    = "x-md-local-"
	Authorization  = "authorization"
	Cookie         = "cookie"
	defaultMaxKeys = 64
	defaultMaxKey  = 128
	defaultMaxSize = 16 * 1024
)

var (
	ErrInvalidKey  = errors.New("metadata key is invalid")
	ErrLimit       = errors.New("metadata limit exceeded")
	ErrInvalidPair = errors.New("metadata key/value pairs are invalid")
)

// Scope controls whether a value is forwarded to another process.
type Scope uint8

const (
	ScopeLocal Scope = iota
	ScopeGlobal
)

type entry struct {
	values []string
	scope  Scope
}

// Metadata is an immutable snapshot. Methods return copies of stored values.
type Metadata struct {
	entries map[string]entry
}

// New validates and copies a metadata map into one scope.
func New(scope Scope, values map[string][]string) (Metadata, error) {
	md := Metadata{entries: make(map[string]entry, len(values))}
	for key, value := range values {
		if err := md.set(scope, key, value); err != nil {
			return Metadata{}, err
		}
	}
	if err := validateLimits(md.entries, defaultMaxKeys, defaultMaxKey, defaultMaxSize); err != nil {
		return Metadata{}, err
	}
	return md, nil
}

// Get returns a defensive copy of the values for key.
func (m Metadata) Get(key string) []string {
	item, ok := m.entries[normalizeKey(key)]
	if !ok {
		return nil
	}
	return append([]string(nil), item.values...)
}

// Scope reports the propagation scope for key.
func (m Metadata) Scope(key string) (Scope, bool) {
	item, ok := m.entries[normalizeKey(key)]
	return item.scope, ok
}

// Range visits a stable, sorted snapshot.
func (m Metadata) Range(fn func(scope Scope, key string, values []string) bool) {
	keys := make([]string, 0, len(m.entries))
	for key := range m.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := m.entries[key]
		if !fn(item.scope, key, append([]string(nil), item.values...)) {
			return
		}
	}
}

func (m Metadata) clone() Metadata {
	clone := Metadata{entries: make(map[string]entry, len(m.entries))}
	for key, item := range m.entries {
		clone.entries[key] = entry{values: append([]string(nil), item.values...), scope: item.scope}
	}
	return clone
}

func (m *Metadata) set(scope Scope, key string, values []string) error {
	key = normalizeKey(key)
	if !validKey(key) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	copyValues := make([]string, 0, len(values))
	for _, value := range values {
		if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%w: %q", ErrInvalidPair, key)
		}
		copyValues = append(copyValues, value)
	}
	m.entries[key] = entry{values: copyValues, scope: scope}
	return nil
}

type contextKey struct{}

// FromContext returns an immutable metadata snapshot.
func FromContext(ctx context.Context) Metadata {
	if ctx == nil {
		return Metadata{entries: map[string]entry{}}
	}
	md, _ := ctx.Value(contextKey{}).(Metadata)
	return md.clone()
}

// NewContext stores a defensive copy in ctx.
func NewContext(ctx context.Context, md Metadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, md.clone())
}

// With adds or replaces one metadata value without mutating an existing snapshot.
func With(ctx context.Context, scope Scope, key string, values ...string) (context.Context, error) {
	md := FromContext(ctx)
	if err := md.set(scope, key, values); err != nil {
		return ctx, err
	}
	if err := validateLimits(md.entries, defaultMaxKeys, defaultMaxKey, defaultMaxSize); err != nil {
		return ctx, err
	}
	return NewContext(ctx, md), nil
}

// MustWith is intended for stable framework keys and panics on invalid input.
func MustWith(ctx context.Context, scope Scope, key string, values ...string) context.Context {
	next, err := With(ctx, scope, key, values...)
	if err != nil {
		panic(err)
	}
	return next
}

// Carrier is implemented by HTTP, gRPC, and message header adapters.
type Carrier interface {
	Get(string) string
	Set(string, string)
	Keys() []string
}

type propagatorConfig struct {
	maxKeys          int
	maxKeyBytes      int
	maxTotalBytes    int
	allowedSensitive map[string]struct{}
	trace            propagation.TextMapPropagator
	newRequestID     func() string
}

// Propagator extracts and injects safe metadata plus W3C trace context.
type Propagator struct {
	config propagatorConfig
}

// Option configures a Propagator.
type Option func(*propagatorConfig)

// WithAllowedSensitive permits selected sensitive keys to cross a process boundary.
func WithAllowedSensitive(keys ...string) Option {
	return func(cfg *propagatorConfig) {
		for _, key := range keys {
			cfg.allowedSensitive[normalizeKey(key)] = struct{}{}
		}
	}
}

// WithLimits overrides the metadata abuse limits. Non-positive values retain defaults.
func WithLimits(maxKeys, maxKeyBytes, maxTotalBytes int) Option {
	return func(cfg *propagatorConfig) {
		if maxKeys > 0 {
			cfg.maxKeys = maxKeys
		}
		if maxKeyBytes > 0 {
			cfg.maxKeyBytes = maxKeyBytes
		}
		if maxTotalBytes > 0 {
			cfg.maxTotalBytes = maxTotalBytes
		}
	}
}

// WithTracePropagator overrides the global OpenTelemetry propagator.
func WithTracePropagator(trace propagation.TextMapPropagator) Option {
	return func(cfg *propagatorConfig) {
		if trace != nil {
			cfg.trace = trace
		}
	}
}

// NewPropagator creates the default safe propagation policy.
func NewPropagator(options ...Option) *Propagator {
	cfg := propagatorConfig{
		maxKeys:          defaultMaxKeys,
		maxKeyBytes:      defaultMaxKey,
		maxTotalBytes:    defaultMaxSize,
		allowedSensitive: make(map[string]struct{}),
		newRequestID:     randomRequestID,
	}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	return &Propagator{config: cfg}
}

// Extract creates a new context from recognized carrier keys.
func (p *Propagator) Extract(ctx context.Context, carrier Carrier) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if carrier == nil {
		return ctx, nil
	}
	ctx = p.tracePropagator().Extract(ctx, propagation.TextMapCarrier(carrierAdapter{carrier}))
	md := Metadata{entries: make(map[string]entry)}
	for _, rawKey := range carrier.Keys() {
		key := normalizeKey(rawKey)
		scope, include := p.scopeForInbound(key)
		if !include {
			continue
		}
		value := carrier.Get(rawKey)
		if err := md.set(scope, key, []string{value}); err != nil {
			return ctx, err
		}
	}
	if len(md.Get(RequestIDKey)) == 0 {
		if err := md.set(ScopeGlobal, RequestIDKey, []string{p.config.newRequestID()}); err != nil {
			return ctx, err
		}
	}
	if len(md.Get(LanguageKey)) == 0 {
		if language := carrier.Get("accept-language"); language != "" {
			if err := md.set(ScopeGlobal, LanguageKey, []string{firstLanguage(language)}); err != nil {
				return ctx, err
			}
		}
	}
	if err := validateLimits(md.entries, p.config.maxKeys, p.config.maxKeyBytes, p.config.maxTotalBytes); err != nil {
		return ctx, err
	}
	return NewContext(ctx, md), nil
}

// Inject writes globally scoped metadata and trace context to carrier.
func (p *Propagator) Inject(ctx context.Context, carrier Carrier) error {
	if carrier == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.tracePropagator().Inject(ctx, propagation.TextMapCarrier(carrierAdapter{carrier}))
	md := FromContext(ctx)
	md.Range(func(scope Scope, key string, values []string) bool {
		if scope != ScopeGlobal || len(values) == 0 || !p.allowedOutbound(key) {
			return true
		}
		carrier.Set(key, strings.Join(values, ","))
		return true
	})
	return nil
}

func (p *Propagator) tracePropagator() propagation.TextMapPropagator {
	if p != nil && p.config.trace != nil {
		return p.config.trace
	}
	return otel.GetTextMapPropagator()
}

func (p *Propagator) scopeForInbound(key string) (Scope, bool) {
	switch {
	case key == RequestIDKey, key == LanguageKey, strings.HasPrefix(key, GlobalPrefix):
		return ScopeGlobal, true
	case strings.HasPrefix(key, LocalPrefix):
		return ScopeLocal, true
	case key == Authorization, key == Cookie:
		_, ok := p.config.allowedSensitive[key]
		return ScopeGlobal, ok
	default:
		_, ok := p.config.allowedSensitive[key]
		return ScopeGlobal, ok
	}
}

func (p *Propagator) allowedOutbound(key string) bool {
	if key == RequestIDKey || key == LanguageKey || strings.HasPrefix(key, GlobalPrefix) {
		return true
	}
	_, ok := p.config.allowedSensitive[key]
	return ok
}

type carrierAdapter struct{ Carrier }

func (c carrierAdapter) Keys() []string { return c.Carrier.Keys() }

func normalizeKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for _, char := range key {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validateLimits(entries map[string]entry, maxKeys, maxKeyBytes, maxTotalBytes int) error {
	if len(entries) > maxKeys {
		return fmt.Errorf("%w: key count %d exceeds %d", ErrLimit, len(entries), maxKeys)
	}
	total := 0
	for key, item := range entries {
		if len(key) > maxKeyBytes {
			return fmt.Errorf("%w: key %q exceeds %d bytes", ErrLimit, key, maxKeyBytes)
		}
		total += len(key)
		for _, value := range item.values {
			total += len(value)
		}
	}
	if total > maxTotalBytes {
		return fmt.Errorf("%w: payload %d exceeds %d bytes", ErrLimit, total, maxTotalBytes)
	}
	return nil
}

func randomRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(value[:])
}

func firstLanguage(value string) string {
	value = strings.TrimSpace(strings.Split(value, ",")[0])
	return strings.TrimSpace(strings.Split(value, ";")[0])
}
