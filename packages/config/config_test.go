package config_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kelseyhightower/envconfig"

	modularconfig "github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/config/configitem"
)

type CustomConfig struct {
	Application configitem.Application `mapstructure:"Application"`
	Database    configitem.Database    `mapstructure:"Database"`
	Redis       configitem.Redis       `mapstructure:"Redis"`
	HTTP        configitem.HTTP        `mapstructure:"HTTP"`
}

func NewCustomConfig() *CustomConfig {
	return &CustomConfig{}
}

func TestConfigure(t *testing.T) {
	os.Setenv("CUSTOM_APPLICATION_SERVICE", "IT_WORKS_NOW")

	// 1. 创建各个模块需要的配置实例
	customConfig := NewCustomConfig()

	if err := modularconfig.InitConfigure(customConfig,
		// config.WithConfigFile("./develop.yaml", true),
		modularconfig.WithEnvPrefix("HELLO", strings.NewReplacer(".", "_")),
	); err != nil {
		fmt.Println(err)
		return
	}

	// // 2. 创建中央加载器，并一次性加载所有指定配置
	// loader, err := NewConfigureLoader(
	//	WithConfigFile("./app.yml", false),
	//	WithEnvPrefix("CUSTOM", strings.NewReplacer(".", "_")),
	// )
	// if err != nil {
	//	log.Fatal(err)
	//	return
	// }
	// if err = loader.Load(customConfig); err != nil {
	//	log.Fatalf("Failed to load configuration: \n%Viper", err)
	// }

	bytes, _ := json.MarshalIndent(customConfig, "", "  ")
	t.Log(string(bytes))
	fmt.Println("--- Configuration Loaded Successfully ---")
}

func TestWithEnv(t *testing.T) {
	//os.Setenv("CUSTOM_APPLICATION_NAME", "ttt")
	os.Setenv("CUSTOM_APPLICATION_MODE", "dev")
	os.Setenv("CUSTOM_APPLICATION_SERVICE", "service")
	os.Setenv("CUSTOM_APPLICATION_VERSION", "1.0.1")
	os.Setenv("CUSTOM_APPLICATION_CLIENTS_AAAA", "0.0.0.0:10001")
	os.Setenv("CUSTOM_APPLICATION_CLIENTS_BBBB", "0.0.0.0:10002")

	var cfg CustomConfig
	cfg.Application.Name = "default-name"
	if err := envconfig.Process("CUSTOM", &cfg); err != nil {
		t.Error(err)
		return
	}
	t.Log(cfg)
}

func TestStorageConfig_DiskFields(t *testing.T) {
	c := &configitem.Storage{Type: "disk", PublicBaseURL: "https://cdn.example.com",
		Disk: &configitem.DiskStorageConfig{RootDir: "/data", BaseUrl: "cdn.example.com"}}
	if c.Disk.RootDir != "/data" || c.Disk.BaseUrl != "cdn.example.com" {
		t.Fatalf("unexpected disk config: %+v", c.Disk)
	}
	if c.Type != "disk" {
		t.Fatalf("unexpected type: %s", c.Type)
	}
}

func TestStorageConfig_OSSBaseDir(t *testing.T) {
	c := &configitem.Storage{Type: "oss", OSS: &configitem.OSSStorageConfig{Bucket: "b", Region: "cn-hangzhou", BaseDir: "prefix"}}
	if c.OSS.BaseDir != "prefix" {
		t.Fatalf("BaseDir not set: %+v", c.OSS)
	}
}

func TestDatabaseConfigRequiresExplicitDSN(t *testing.T) {
	cfg := &configitem.Database{DSN: "postgres://localhost/app"}
	if err := modularconfig.ValidateNode(cfg); err != nil {
		t.Fatalf("ValidateNode(database) error = %v", err)
	}
}

func TestAppYAMLLoadsCurrentConfig(t *testing.T) {
	type appYAMLConfig struct {
		Application configitem.Application `mapstructure:"Application"`
		Logging     configitem.Logging     `mapstructure:"Logging"`
		Database    configitem.Database    `mapstructure:"Database"`
		Redis       configitem.Redis       `mapstructure:"Redis"`
		HTTP        configitem.HTTP        `mapstructure:"HTTP"`
		GRPC        configitem.GRPC        `mapstructure:"GRPC"`
		Storage     configitem.Storage     `mapstructure:"Storage"`
	}

	var cfg appYAMLConfig
	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFile("app.yml", false)); err != nil {
		t.Fatalf("InitConfigure(app.yml) error = %v", err)
	}
	if cfg.Application.Name != "custom-modular-monolith" {
		t.Fatalf("Application.Name = %q, want custom-modular-monolith", cfg.Application.Name)
	}
	if cfg.Storage.Type != "disk" {
		t.Fatalf("storage type = %q, want disk", cfg.Storage.Type)
	}
	if cfg.Storage.Disk == nil || cfg.Storage.Disk.RootDir == "" {
		t.Fatalf("disk storage config not loaded: %+v", cfg.Storage.Disk)
	}
}

func TestWithConfigFileLoadsExactPath(t *testing.T) {
	type exactPathConfig struct {
		Name string `mapstructure:"Name"`
	}

	configFile := filepath.Join(t.TempDir(), "custom-name.yaml")
	if err := os.WriteFile(configFile, []byte("Name: exact-path\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var cfg exactPathConfig
	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFile(configFile, false)); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Name != "exact-path" {
		t.Fatalf("Name = %q, want exact-path", cfg.Name)
	}
}

func TestWithConfigFileIgnoresMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var cfg struct{}

	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFile(missing, true)); err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
}

func TestWithConfigFileReturnsMissingPathError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var cfg struct{}

	err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFile(missing, false))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("InitConfigure() error = %v, want os.ErrNotExist", err)
	}
}

func TestWithConfigFileDoesNotIgnoreMalformedFile(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "malformed.yaml")
	if err := os.WriteFile(configFile, []byte("name: [\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var cfg struct{}
	if err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFile(configFile, true)); err == nil {
		t.Fatalf("InitConfigure() error = nil, want malformed config error")
	}
}

func TestWithConfigFSLoadsEmbeddedConfig(t *testing.T) {
	filesystem := fstest.MapFS{
		"config/app.yaml": &fstest.MapFile{Data: []byte("Name: embedded\n")},
	}
	var cfg struct {
		Name string `mapstructure:"Name"`
	}

	err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFS(filesystem, "config/app.yaml"))
	if err != nil {
		t.Fatalf("InitConfigure() error = %v", err)
	}
	if cfg.Name != "embedded" {
		t.Fatalf("Name = %q, want embedded", cfg.Name)
	}
}

func TestWithConfigFSRejectsMissingFile(t *testing.T) {
	var cfg struct{}
	err := modularconfig.InitConfigure(&cfg, modularconfig.WithConfigFS(fstest.MapFS{}, "missing.yaml"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("InitConfigure() error = %v, want os.ErrNotExist", err)
	}
}
