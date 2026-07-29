package config_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	modularconfig "github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/config/configitem"
)

type rootProviderConfig struct {
	Timeout time.Duration `mapstructure:"Timeout"`
	Output  []string      `mapstructure:"Output"`
}

func (rootProviderConfig) Flags(prefix string) []modularconfig.FlagSpec {
	return []modularconfig.FlagSpec{
		{Name: prefix + "Timeout", Default: 5 * time.Second},
		{Name: prefix + "Output", Default: []string{"console"}},
	}
}

func TestGetConfigFlagSpecsUsesRootProvider(t *testing.T) {
	specs := modularconfig.GetConfigFlagSpecs[rootProviderConfig]()

	if !hasFlagSpec(specs, "Timeout") {
		t.Fatalf("Timeout spec missing: %+v", specs)
	}
	if !hasFlagSpec(specs, "Output") {
		t.Fatalf("Output spec missing: %+v", specs)
	}
}

func TestInitConfigureAppliesDefaultsWithoutSources(t *testing.T) {
	var cfg rootProviderConfig

	if err := modularconfig.InitConfigure(&cfg); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", cfg.Timeout)
	}
	if len(cfg.Output) != 1 || cfg.Output[0] != "console" {
		t.Fatalf("Output = %#v, want [console]", cfg.Output)
	}
}

func TestInitConfigurePreservesViperDefaultDecodeHooks(t *testing.T) {
	filesystem := fstest.MapFS{
		"Config.yaml": &fstest.MapFile{Data: []byte("Timeout: 2s\nOutput: console,file\n")},
	}
	var cfg rootProviderConfig

	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFS(filesystem, "Config.yaml")); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Timeout != 2*time.Second {
		t.Fatalf("Timeout = %v, want 2s", cfg.Timeout)
	}
	if len(cfg.Output) != 2 || cfg.Output[0] != "console" || cfg.Output[1] != "file" {
		t.Fatalf("Output = %#v, want [console file]", cfg.Output)
	}
}

func TestInitConfigureIgnoresUnknownKeysByDefault(t *testing.T) {
	filesystem := fstest.MapFS{
		"Config.yaml": &fstest.MapFile{Data: []byte("Name: known\nUnexpected: ignored\n")},
	}
	var cfg struct {
		Name string `mapstructure:"Name"`
	}

	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFS(filesystem, "Config.yaml")); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Name != "known" {
		t.Fatalf("Name = %q, want known", cfg.Name)
	}
}

func TestInitConfigureStillAcceptsLowercaseFileKeys(t *testing.T) {
	filesystem := fstest.MapFS{
		"Config.yaml": &fstest.MapFile{Data: []byte("name: compatible\n")},
	}
	var cfg struct {
		Name string `mapstructure:"Name"`
	}

	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFS(filesystem, "Config.yaml")); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Name != "compatible" {
		t.Fatalf("Name = %q, want compatible", cfg.Name)
	}
}

func TestInitConfigureStrictDecodeRejectsUnknownKeys(t *testing.T) {
	filesystem := fstest.MapFS{
		"Config.yaml": &fstest.MapFile{Data: []byte("Name: known\nUnexpected: rejected\n")},
	}
	var cfg struct {
		Name string `mapstructure:"Name"`
	}

	err := modularconfig.InitConfigure(
		&cfg,
		modularconfig.WithConfigFS(filesystem, "Config.yaml"),
		modularconfig.WithStrictDecode(),
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unexpected") {
		t.Fatalf("InitConfigure() error = %v, want unknown key error", err)
	}
}

func TestWithEnvPrefixUsesCaseInsensitivePrefixAndUnderscoreHierarchy(t *testing.T) {
	t.Setenv("APP_STORAGE_OSS_ACCESSKEYID", "test-access-key")
	var cfg struct {
		Storage configitem.Storage `mapstructure:"Storage"`
	}

	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithEnvPrefix("app")); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Storage.OSS == nil || cfg.Storage.OSS.AccessKeyID != "test-access-key" {
		t.Fatalf("Storage.OSS = %#v", cfg.Storage.OSS)
	}
}

func TestInitConfigureRejectsInvalidTarget(t *testing.T) {
	t.Run("non-pointer", func(t *testing.T) {
		err := modularconfig.InitConfigure(struct{}{})
		if err == nil {
			t.Fatal("InitConfigure() error = nil, want invalid target error")
		}
	})

	t.Run("nil pointer", func(t *testing.T) {
		var target *struct{}
		err := modularconfig.InitConfigure(target)
		if err == nil {
			t.Fatal("InitConfigure() error = nil, want invalid target error")
		}
	})

	t.Run("error type", func(t *testing.T) {
		var target *struct{}
		err := modularconfig.InitConfigure(target)
		var targetError *modularconfig.TargetError
		if !errors.As(err, &targetError) {
			t.Fatalf("InitConfigure() error = %T, want *config.TargetError", err)
		}
	})
}
