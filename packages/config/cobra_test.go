package config_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	modularconfig "github.com/wplbyx/modular/packages/config"
)

func TestNewRootLoadsConfigFileAndFlagOverride(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: file,
		EnvPrefix:   "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--http.port", "19090"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got == nil {
		t.Fatalf("Run was not called")
	}
	if got.Application.Name != "cobra-test" || got.Application.Mode != "prod" || got.Application.Version != "v1.0.0" {
		t.Fatalf("Application = %+v", got.Application)
	}
	if got.HTTP.Host != "127.0.0.1" || got.HTTP.Port != 19090 {
		t.Fatalf("HTTP = %+v", got.HTTP)
	}
}

func TestNewRootAliasOverridesConfigFile(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: file,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--port", "19090"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.HTTP.Port != 19090 {
		t.Fatalf("HTTP.Port = %d", got.HTTP.Port)
	}
}

func TestNewRootFullPathFlagWinsOverAlias(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: file,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--port", "19090", "--http.port", "20080"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.HTTP.Port != 20080 {
		t.Fatalf("HTTP.Port = %d", got.HTTP.Port)
	}
}

func TestNewRootEnvOverridesConfigFile(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	t.Setenv("TESTAPP_HTTP_PORT", "19090")
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: file,
		EnvPrefix:   "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.HTTP.Port != 19090 {
		t.Fatalf("HTTP.Port = %d", got.HTTP.Port)
	}
}

func TestNewRootFlagOverridesEnv(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	t.Setenv("TESTAPP_HTTP_PORT", "19090")
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: file,
		EnvPrefix:   "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--http.port", "20080"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.HTTP.Port != 20080 {
		t.Fatalf("HTTP.Port = %d", got.HTTP.Port)
	}
}

func TestNewRootLoadsRemoteFlag(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte(`application:
  Name: remote-app
  Mode: prod
  Version: v2.0.0
http:
  Host: 127.0.0.1
  Port: 18080
database:
  Dsn: postgres
redis:
  Host: remote-redis
  Port: 6379
`)}
	installFakeRemoteConfig(t, fake)
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName: "test-app",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--remote", "consul://127.0.0.1:8500/config/app"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got == nil || got.Application.Name != "remote-app" || got.Redis.Host != "remote-redis" {
		t.Fatalf("config = %+v", got)
	}
}

func TestNewRootPrecedenceFlagEnvFileRemote(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte(`application:
  Name: remote-app
  Mode: prod
  Version: v2.0.0
http:
  Host: remote-host
  Port: 17070
database:
  Dsn: mysql
redis:
  Host: remote-redis
  Port: 6379
`)}
	installFakeRemoteConfig(t, fake)
	file := writeCobraTestConfig(t, 18080)
	t.Setenv("TESTAPP_HTTP_PORT", "19090")
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:       "test-app",
		DefaultFile:   file,
		DefaultRemote: "consul://127.0.0.1:8500/config/app",
		EnvPrefix:     "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--http.port", "20080"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.Application.Name != "cobra-test" {
		t.Fatalf("Application.Name = %q, want cobra-test", got.Application.Name)
	}
	if got.HTTP.Host != "127.0.0.1" {
		t.Fatalf("HTTP.Host = %q, want local file value", got.HTTP.Host)
	}
	if got.HTTP.Port != 20080 {
		t.Fatalf("HTTP.Port = %d, want CLI value", got.HTTP.Port)
	}
}

func TestConfigFlagSpecsUsesCustomConfigModules(t *testing.T) {
	specs := modularconfig.GetConfigFlagSpecs[CustomConfig]()

	if !hasFlagSpec(specs, "http.port") {
		t.Fatalf("http.port spec missing")
	}
	if !hasFlagSpec(specs, "redis.host") {
		t.Fatalf("redis.host spec missing")
	}
	if !hasFlagSpec(specs, "database.dsn") {
		t.Fatalf("database.dsn spec missing")
	}
	if hasFlagSpec(specs, "storage.type") {
		t.Fatalf("storage.type spec should not be registered")
	}
}

func TestNewRootExplicitMissingConfigReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: missing,
		Run: func(_ context.Context, _ *CustomConfig) error {
			return nil
		},
	})
	cmd.SetArgs([]string{"--config", missing})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "read config file") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestNewRootMissingDefaultConfigIsIgnored(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var got *CustomConfig

	cmd := modularconfig.NewRoot[CustomConfig](modularconfig.Options[CustomConfig]{
		AppName:     "test-app",
		DefaultFile: missing,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{
		"--application.name", "test-app",
		"--database.dsn", "postgres",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got == nil {
		t.Fatalf("Run was not called")
	}
}

func writeCobraTestConfig(t *testing.T, port int) string {
	t.Helper()

	dir := t.TempDir()
	file := filepath.Join(dir, "app.yaml")
	body := []byte(`application:
  Name: cobra-test
  Mode: prod
  Version: v1.0.0
http:
  Host: 127.0.0.1
  Port: ` + itoa(port) + `
redis:
  Host: 127.0.0.1
  Port: 6379
database:
  Dsn: postgres
`)
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return file
}

func hasFlagSpec(specs []modularconfig.FlagSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
