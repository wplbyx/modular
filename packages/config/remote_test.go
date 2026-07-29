package config_test

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	modularconfig "github.com/wplbyx/modular/packages/config"
)

type fakeRemoteConfig struct {
	data     []byte
	err      error
	calls    int
	provider string
	endpoint string
	path     string
}

func (f *fakeRemoteConfig) Get(provider viper.RemoteProvider) (io.Reader, error) {
	f.capture(provider)
	if f.err != nil {
		return nil, f.err
	}
	return bytes.NewReader(f.data), nil
}

func (f *fakeRemoteConfig) Watch(provider viper.RemoteProvider) (io.Reader, error) {
	return f.Get(provider)
}

func (f *fakeRemoteConfig) WatchChannel(provider viper.RemoteProvider) (<-chan *viper.RemoteResponse, chan bool) {
	f.capture(provider)
	return nil, make(chan bool)
}

func (f *fakeRemoteConfig) capture(provider viper.RemoteProvider) {
	f.calls++
	f.provider = provider.Provider()
	f.endpoint = provider.Endpoint()
	f.path = provider.Path()
}

func installFakeRemoteConfig(t *testing.T, fake *fakeRemoteConfig) {
	t.Helper()

	previous := viper.RemoteConfig
	viper.RemoteConfig = fake
	t.Cleanup(func() {
		viper.RemoteConfig = previous
	})
}

func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	return &output
}

func TestWithRemoteURLMapsEtcdToEtcd3(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte("Name: remote-etcd\n")}
	installFakeRemoteConfig(t, fake)

	var cfg struct {
		Name string `mapstructure:"Name"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithRemoteURL("etcd://10.0.0.1:2379/config/myapp"),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "remote-etcd" {
		t.Fatalf("Name = %q, want remote-etcd", cfg.Name)
	}
	if fake.provider != "etcd3" {
		t.Fatalf("provider = %q, want etcd3", fake.provider)
	}
	if fake.endpoint != "http://10.0.0.1:2379" {
		t.Fatalf("endpoint = %q", fake.endpoint)
	}
	if fake.path != "/config/myapp" {
		t.Fatalf("path = %q", fake.path)
	}
}

func TestWithRemoteURLLoadsConsulJSON(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte(`{"Name":"remote-consul"}`)}
	installFakeRemoteConfig(t, fake)

	var cfg struct {
		Name string `mapstructure:"Name"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithRemoteURL("consul://10.0.0.2:8500/config/myapp?format=json"),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "remote-consul" {
		t.Fatalf("Name = %q, want remote-consul", cfg.Name)
	}
	if fake.provider != "consul" || fake.endpoint != "10.0.0.2:8500" {
		t.Fatalf("provider = %q, endpoint = %q", fake.provider, fake.endpoint)
	}
}

func TestWithRemoteProviderDefaultsToYAML(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte("Name: direct-provider\n")}
	installFakeRemoteConfig(t, fake)

	var cfg struct {
		Name string `mapstructure:"Name"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithRemoteProvider("consul", "127.0.0.1:8500", "/config/myapp"),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "direct-provider" {
		t.Fatalf("Name = %q, want direct-provider", cfg.Name)
	}
}

func TestWithRemoteURLRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{name: "unsupported scheme", remote: "redis://127.0.0.1:6379/config/app", want: "unsupported remote config URL scheme"},
		{name: "missing host", remote: "etcd:///config/app", want: "host is empty"},
		{name: "missing path", remote: "etcd://127.0.0.1:2379", want: "path is empty"},
		{name: "credentials", remote: "consul://user:secret@127.0.0.1:8500/config/app", want: "must not contain credentials"},
		{name: "fragment", remote: "consul://127.0.0.1:8500/config/app#value", want: "must not contain a fragment"},
		{name: "unsupported format", remote: "consul://127.0.0.1:8500/config/app?format=xml", want: "unsupported remote config format"},
		{name: "unknown query", remote: "consul://127.0.0.1:8500/config/app?type=yaml", want: "unsupported remote config URL query parameter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg struct{}
			err := modularconfig.InitConfigure(&cfg, modularconfig.WithRemoteURL(tt.remote))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("InitConfigure() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestConfigSourcesLocalOverridesRemoteAndRemoteFillsMissingValues(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte(`Name: remote
Port: 17070
RemoteOnly: loaded
Unknown: ignored
`)}
	installFakeRemoteConfig(t, fake)

	configFile := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(configFile, []byte("Name: local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var cfg struct {
		Name       string `mapstructure:"Name"`
		Port       int    `mapstructure:"Port"`
		RemoteOnly string `mapstructure:"RemoteOnly"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/app"),
		modularconfig.WithConfigFile(configFile, false),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "local" || cfg.Port != 17070 || cfg.RemoteOnly != "loaded" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestConfigSourcesRemoteFailureFallsBackToLocal(t *testing.T) {
	fake := &fakeRemoteConfig{err: errors.New("remote unavailable")}
	installFakeRemoteConfig(t, fake)
	logOutput := captureStandardLog(t)

	configFile := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(configFile, []byte("Name: local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var cfg struct {
		Name string `mapstructure:"Name"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithConfigFile(configFile, false),
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/app"),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "local" {
		t.Fatalf("Name = %q, want local", cfg.Name)
	}
	if !strings.Contains(logOutput.String(), "using local file") {
		t.Fatalf("log output = %q", logOutput.String())
	}
}

func TestConfigSourcesMalformedRemoteFallsBackWithoutPartialValues(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte("Name: remote\nPort: [\n")}
	installFakeRemoteConfig(t, fake)
	captureStandardLog(t)

	configFile := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(configFile, []byte("Name: local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var cfg struct {
		Name string `mapstructure:"Name"`
		Port int    `mapstructure:"Port"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithConfigFile(configFile, false),
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/app"),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "local" || cfg.Port != 0 {
		t.Fatalf("config = %+v, want only local values", cfg)
	}
}

func TestConfigSourcesRemoteFailureWithoutLocalReturnsError(t *testing.T) {
	fake := &fakeRemoteConfig{err: errors.New("remote unavailable")}
	installFakeRemoteConfig(t, fake)

	var cfg struct{}
	err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/app"),
	)
	if err == nil || !strings.Contains(err.Error(), "read remote config") {
		t.Fatalf("InitConfigure() error = %v", err)
	}
}

func TestConfigSourcesMissingDefaultFileDoesNotMaskRemoteFailure(t *testing.T) {
	fake := &fakeRemoteConfig{err: errors.New("remote unavailable")}
	installFakeRemoteConfig(t, fake)

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var cfg struct{}
	err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithConfigFile(missing, true),
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/app"),
	)
	if err == nil || !strings.Contains(err.Error(), "read remote config") {
		t.Fatalf("InitConfigure() error = %v", err)
	}
}

func TestConfigSourcesFormatMismatchSkipsRemote(t *testing.T) {
	fake := &fakeRemoteConfig{data: []byte(`{"Name":"remote"}`)}
	installFakeRemoteConfig(t, fake)
	logOutput := captureStandardLog(t)

	configFile := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(configFile, []byte("Name: local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var cfg struct {
		Name string `mapstructure:"Name"`
	}
	if err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithConfigFile(configFile, false),
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/app?format=json"),
	); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	if cfg.Name != "local" || fake.calls != 0 {
		t.Fatalf("config = %+v, remote calls = %d", cfg, fake.calls)
	}
	if !strings.Contains(logOutput.String(), "skip remote") {
		t.Fatalf("log output = %q", logOutput.String())
	}
}

func TestConfigSourcesRejectDuplicateSources(t *testing.T) {
	var cfg struct{}
	err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithConfigFile("first.yaml", true),
		modularconfig.WithConfigFile("second.yaml", true),
	)
	if err == nil || !strings.Contains(err.Error(), "config file source already configured") {
		t.Fatalf("InitConfigure() error = %v", err)
	}

	err = modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/first"),
		modularconfig.WithRemoteURL("consul://127.0.0.1:8500/config/second"),
	)
	if err == nil || !strings.Contains(err.Error(), "remote config source already configured") {
		t.Fatalf("InitConfigure() error = %v", err)
	}
}
