package config_test

import (
	"errors"
	"strings"
	"testing"

	modularconfig "github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/config/configitem"
)

func TestValidateReturnsStructuredCanonicalViolations(t *testing.T) {
	type generated struct {
		Application configitem.Application `mapstructure:"Application"`
	}
	type aggregate struct {
		Generated generated `mapstructure:",squash"`
	}

	err := modularconfig.Validate(&aggregate{})
	var validationError *modularconfig.ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Validate() error = %T, want *config.ValidationError", err)
	}
	if !hasViolation(validationError.Violations, "Application.Name", "required") {
		t.Fatalf("violations = %#v, want Application.Name required", validationError.Violations)
	}
	if strings.Contains(err.Error(), "aggregate") || strings.Contains(err.Error(), "Generated") {
		t.Fatalf("Validate() error contains implementation wrapper: %v", err)
	}
}

func TestValidationErrorDoesNotExposeFieldValues(t *testing.T) {
	secret := "do-not-print-this-secret"
	err := modularconfig.Validate(&configitem.Application{
		Name:    secret,
		Mode:    "invalid-mode",
		Version: "v1",
	})
	if err == nil {
		t.Fatal("Validate() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "invalid-mode") {
		t.Fatalf("Validate() leaked a field value: %v", err)
	}
}

func TestValidateStorageSelectedBackend(t *testing.T) {
	tests := []struct {
		name string
		cfg  configitem.Storage
		path string
	}{
		{name: "disk config required", cfg: configitem.Storage{Type: "disk"}, path: "Disk"},
		{name: "disk root required", cfg: configitem.Storage{Type: "disk", Disk: &configitem.DiskStorageConfig{}}, path: "Disk.RootDir"},
		{name: "oss config required", cfg: configitem.Storage{Type: "oss"}, path: "OSS"},
		{name: "oss credentials required", cfg: configitem.Storage{Type: "oss", OSS: &configitem.OSSStorageConfig{}}, path: "OSS.AccessKeyID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := modularconfig.Validate(&tt.cfg)
			var validationError *modularconfig.ValidationError
			if !errors.As(err, &validationError) || !hasViolationPath(validationError.Violations, tt.path) {
				t.Fatalf("Validate() error = %#v, want path %s", err, tt.path)
			}
		})
	}
}

func TestValidateNestedStructureRuleUsesCanonicalPath(t *testing.T) {
	var cfg struct {
		Storage configitem.Storage `mapstructure:"Storage"`
	}
	cfg.Storage.Type = "disk"
	cfg.Storage.Disk = &configitem.DiskStorageConfig{}

	err := modularconfig.Validate(&cfg)
	var validationError *modularconfig.ValidationError
	if !errors.As(err, &validationError) || !hasViolationPath(validationError.Violations, "Storage.Disk.RootDir") {
		t.Fatalf("Validate() error = %#v, want Storage.Disk.RootDir", err)
	}
}

func TestValidateMongoConnectionSource(t *testing.T) {
	tests := []struct {
		name string
		cfg  configitem.Mongo
		path string
	}{
		{name: "missing", cfg: configitem.Mongo{}, path: "URI"},
		{name: "mutually exclusive", cfg: configitem.Mongo{URI: "mongodb://localhost", Hosts: []string{"localhost:27017"}}, path: "Hosts"},
		{name: "invalid uri", cfg: configitem.Mongo{URI: "localhost:27017"}, path: "URI"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := modularconfig.Validate(&tt.cfg)
			var validationError *modularconfig.ValidationError
			if !errors.As(err, &validationError) || !hasViolationPath(validationError.Violations, tt.path) {
				t.Fatalf("Validate() error = %#v, want path %s", err, tt.path)
			}
		})
	}
}

func TestValidateRedisFallbackAddress(t *testing.T) {
	tests := []struct {
		name string
		cfg  configitem.Redis
		path string
	}{
		{name: "host required", cfg: configitem.Redis{Port: 6379}, path: "Host"},
		{name: "port required", cfg: configitem.Redis{Host: "localhost"}, path: "Port"},
		{name: "urls bypass fallback", cfg: configitem.Redis{Urls: []string{"localhost:6379"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := modularconfig.Validate(&tt.cfg)
			if tt.path == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			var validationError *modularconfig.ValidationError
			if !errors.As(err, &validationError) || !hasViolationPath(validationError.Violations, tt.path) {
				t.Fatalf("Validate() error = %#v, want path %s", err, tt.path)
			}
		})
	}
}

func TestValidateHTTPTLSAndEphemeralPort(t *testing.T) {
	valid := configitem.HTTP{Host: "127.0.0.1", Port: 0}
	if err := modularconfig.Validate(&valid); err != nil {
		t.Fatalf("Validate(Port=0) error = %v", err)
	}

	tls := configitem.HTTP{Host: "127.0.0.1", Port: 0, EnableTLS: true}
	err := modularconfig.Validate(&tls)
	var validationError *modularconfig.ValidationError
	if !errors.As(err, &validationError) ||
		!hasViolationPath(validationError.Violations, "TLSCertFile") ||
		!hasViolationPath(validationError.Violations, "TLSKeyFile") {
		t.Fatalf("Validate(TLS) error = %#v", err)
	}
}

func hasViolation(violations []modularconfig.Violation, path, rule string) bool {
	for _, violation := range violations {
		if violation.Path == path && violation.Rule == rule {
			return true
		}
	}
	return false
}

func hasViolationPath(violations []modularconfig.Violation, path string) bool {
	for _, violation := range violations {
		if violation.Path == path {
			return true
		}
	}
	return false
}
