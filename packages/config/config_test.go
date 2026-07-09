package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"
)

func TestConfigure(t *testing.T) {
	os.Setenv("CUSTOM_APPLICATION_SERVICE", "IT_WORKS_NOW")

	// 1. 创建各个模块需要的配置实例
	customConfig := NewCustomConfig()

	if err := InitConfigure(customConfig,
		// config.WithConfigFile("develop", "yaml", "."),
		WithEnvPrefix("HELLO", strings.NewReplacer(".", "_")),
		// config.WithCommandLine(pflag.CommandLine),
	); err != nil {
		fmt.Println(err)
		return
	}

	// // 2. 创建中央加载器，并一次性加载所有指定配置
	// loader, err := NewConfigureLoader(
	//	WithConfigFile("app", "yaml", "."),
	//	WithEnvPrefix("CUSTOM", strings.NewReplacer(".", "_")),
	//	WithCommandLine(pflag.CommandLine),
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
	c := &Storage{Type: "disk", PublicBaseURL: "https://cdn.example.com",
		Disk: &DiskStorageConfig{RootDir: "/data", BaseUrl: "cdn.example.com"}}
	if c.Disk.RootDir != "/data" || c.Disk.BaseUrl != "cdn.example.com" {
		t.Fatalf("unexpected disk config: %+v", c.Disk)
	}
	if c.Type != "disk" {
		t.Fatalf("unexpected type: %s", c.Type)
	}
}

func TestStorageConfig_OSSBaseDir(t *testing.T) {
	c := &Storage{Type: "oss", OSS: &OSSStorageConfig{Bucket: "b", Region: "cn-hangzhou", BaseDir: "prefix"}}
	if c.OSS.BaseDir != "prefix" {
		t.Fatalf("BaseDir not set: %+v", c.OSS)
	}
}

func TestDatabaseConfigAllowsClickhouseForGORM(t *testing.T) {
	cfg := &Database{Dsn: "clickhouse"}
	if err := ValidateNode(cfg); err != nil {
		t.Fatalf("ValidateNode(clickhouse database) error = %v", err)
	}
}

func TestAppYAMLLoadsCurrentConfig(t *testing.T) {
	type appYAMLConfig struct {
		Application Application `mapstructure:"application"`
		Logging     Logging     `mapstructure:"logging"`
		Database    Database    `mapstructure:"database"`
		Redis       Redis       `mapstructure:"redis"`
		HTTP        HTTP        `mapstructure:"http"`
		GRPC        GRPC        `mapstructure:"grpc"`
		Storage     Storage     `mapstructure:"storage"`
	}

	var cfg appYAMLConfig
	if err := InitConfigure(&cfg, WithConfigFile("app", "yaml", ".")); err != nil {
		t.Fatalf("InitConfigure(app.yml) error = %v", err)
	}
	if cfg.Storage.Type != "disk" {
		t.Fatalf("storage type = %q, want disk", cfg.Storage.Type)
	}
	if cfg.Storage.Disk == nil || cfg.Storage.Disk.RootDir == "" {
		t.Fatalf("disk storage config not loaded: %+v", cfg.Storage.Disk)
	}
}
