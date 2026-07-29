package config_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	modularconfig "github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/config/configitem"
)

type nestedServiceConfig struct {
	HTTP  configitem.HTTP  `mapstructure:"HTTP"`
	Redis configitem.Redis `mapstructure:"Redis"`
}

func (nestedServiceConfig) Flags(prefix string) []modularconfig.FlagSpec {
	return modularconfig.GetConfigFlagSpecsWithPrefix[nestedServiceConfig](prefix)
}

type nestedRuntimeConfig struct {
	User nestedServiceConfig `mapstructure:"User"`
}

func TestNewRootLoadsConfigFileAndFlagOverride(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	var got *CustomConfig

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
		DefaultFile: file,
		EnvPrefix:   "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--HTTP.Port", "19090"})

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

func TestNewRootUsesPascalCaseConfigFlags(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	var got *CustomConfig

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
		DefaultFile: file,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--HTTP.Port", "19090"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got == nil || got.HTTP.Port != 19090 {
		t.Fatalf("config = %#v", got)
	}
	if cmd.Flags().Lookup("http.port") != nil {
		t.Fatal("legacy lowercase flag http.port must not be registered")
	}
}

func TestNewRootStrictDecodeRejectsUnknownKeys(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(file, append(body, []byte("Unknown: rejected\n")...), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:         "test-app",
		DefaultFile:  file,
		StrictDecode: true,
		Run: func(_ context.Context, _ *CustomConfig) error {
			return nil
		},
	})

	err = cmd.Execute()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown") {
		t.Fatalf("Execute() error = %v, want unknown key error", err)
	}
}

func TestNewRootAliasOverridesConfigFile(t *testing.T) {
	file := writeCobraTestConfig(t, 18080)
	var got *CustomConfig

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
		DefaultFile: file,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--Port", "19090"})

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

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
		DefaultFile: file,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--Port", "19090", "--HTTP.Port", "20080"})

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

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
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

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
		DefaultFile: file,
		EnvPrefix:   "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--HTTP.Port", "20080"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.HTTP.Port != 20080 {
		t.Fatalf("HTTP.Port = %d", got.HTTP.Port)
	}
}

func TestNewRootLoadsRemoteFlag(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte(`Application:
  Name: remote-app
  Mode: prod
  Version: v2.0.0
HTTP:
  Host: 127.0.0.1
  Port: 18080
Database:
  DSN: postgres://localhost/app
Redis:
  Host: remote-redis
  Port: 6379
`)}
	installFakeRemoteConfig(t, fake)
	var got *CustomConfig

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name: "test-app",
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
	fake := &fakeRemoteConfig{data: []byte(`Application:
  Name: remote-app
  Mode: prod
  Version: v2.0.0
HTTP:
  Host: remote-host
  Port: 17070
Database:
  DSN: app:app@tcp(localhost:3306)/app
Redis:
  Host: remote-redis
  Port: 6379
`)}
	installFakeRemoteConfig(t, fake)
	file := writeCobraTestConfig(t, 18080)
	t.Setenv("TESTAPP_HTTP_PORT", "19090")
	var got *CustomConfig

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:          "test-app",
		DefaultFile:   file,
		DefaultRemote: "consul://127.0.0.1:8500/config/app",
		EnvPrefix:     "TESTAPP",
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{"--HTTP.Port", "20080"})

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

	if !hasFlagSpec(specs, "HTTP.Port") {
		t.Fatalf("HTTP.Port spec missing")
	}
	if !hasFlagSpec(specs, "Redis.Host") {
		t.Fatalf("Redis.Host spec missing")
	}
	if !hasFlagSpec(specs, "Database.DSN") {
		t.Fatalf("Database.DSN spec missing")
	}
	if hasFlagSpec(specs, "Storage.Type") {
		t.Fatalf("Storage.Type spec should not be registered")
	}
}

func TestConfigFlagSpecsSupportsNestedConfigPrefix(t *testing.T) {
	specs := modularconfig.GetConfigFlagSpecs[nestedRuntimeConfig]()

	if !hasFlagSpec(specs, "User.HTTP.Port") {
		t.Fatalf("User.HTTP.Port spec missing")
	}
	if !hasFlagSpec(specs, "User.Redis.Host") {
		t.Fatalf("User.Redis.Host spec missing")
	}
	if hasFlagSpec(specs, "HTTP.Port") {
		t.Fatalf("unprefixed HTTP.Port spec should not be registered")
	}
	if !hasFlagAlias(specs, "User.HTTP.Port", "User.Port") {
		t.Fatalf("User.HTTP.Port alias User.Port missing")
	}
	if hasAnyFlagAlias(specs, "Port") {
		t.Fatalf("bare alias Port should not be registered for nested config")
	}
}

func TestNewRootExplicitMissingConfigReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
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

	cmd := modularconfig.NewRootCommand[CustomConfig](modularconfig.CommandOptions[CustomConfig]{
		Name:        "test-app",
		DefaultFile: missing,
		Run: func(_ context.Context, cfg *CustomConfig) error {
			got = cfg
			return nil
		},
	})
	cmd.SetArgs([]string{
		"--Application.Name", "test-app",
		"--Database.DSN", "postgres",
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
	body := []byte(`Application:
  Name: cobra-test
  Mode: prod
  Version: v1.0.0
HTTP:
  Host: 127.0.0.1
  Port: ` + itoa(port) + `
Redis:
  Host: 127.0.0.1
  Port: 6379
Database:
  DSN: postgres://localhost/app
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

func hasFlagAlias(specs []modularconfig.FlagSpec, name, alias string) bool {
	for _, spec := range specs {
		if spec.Name != name {
			continue
		}
		for _, candidate := range spec.Aliases {
			if candidate == alias {
				return true
			}
		}
	}
	return false
}

func hasAnyFlagAlias(specs []modularconfig.FlagSpec, alias string) bool {
	for _, spec := range specs {
		for _, candidate := range spec.Aliases {
			if candidate == alias {
				return true
			}
		}
	}
	return false
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
