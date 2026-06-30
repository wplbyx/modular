package config

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-playground/validator/v10"
	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/gosuri/uitable"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// ConfigureLoader 负责加载所有实现了 Configurer 接口的配置对象
type ConfigureLoader struct {
	v *viper.Viper // 配置管理器
}

// ConfigureLoaderOption 定义配置选项的函数类型
type ConfigureLoaderOption func(*ConfigureLoader) error

// WithConfigFile 设置配置文件路径
func WithConfigFile(filename, filetype string, paths ...string) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		// 设置读取配置文件相关配置
		c.v.SetConfigName(filename)
		c.v.SetConfigType(filetype)
		for _, path := range paths {
			c.v.AddConfigPath(path)
		}
		// 读取配置文件
		if err := c.v.ReadInConfig(); err != nil {
			var configFileNotFoundError viper.ConfigFileNotFoundError
			if !errors.As(err, &configFileNotFoundError) {
				return fmt.Errorf("failed to read config file: %w", err)
			}
		}
		return nil
	}
}

// WithEnvPrefix 设置环境变量前缀，例如 MYAPP_
func WithEnvPrefix(prefix string, replaces ...*strings.Replacer) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		// 设置读取环境变量相关配置
		c.v.SetEnvPrefix(prefix)
		if len(replaces) > 0 {
			c.v.SetEnvKeyReplacer(replaces[0])
		}
		// // viper 自动读取环境变量序列化有坑，采用手动赋值
		// c.v.AutomaticEnv()

		// 读取环境变量（手动赋值）
		for _, environ := range os.Environ() {
			parts := strings.SplitN(environ, "=", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// 有前缀
			if prefix != "" {
				if !strings.HasPrefix(key, prefix+"_") {
					continue
				}
				key = strings.TrimPrefix(key, prefix+"_")
			}

			viperKey := strings.ReplaceAll(strings.ToLower(key), "_", ".")
			c.v.Set(viperKey, value)
		}
		return nil
	}
}

// WithCommandLine 绑定命令行参数
func WithCommandLine(flagSet *pflag.FlagSet) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		if flagSet == nil {
			flagSet = pflag.NewFlagSet("root", pflag.ContinueOnError)
		}
		// 解析命令行参数
		if err := flagSet.Parse(os.Args[1:]); err != nil {
			return err
		}
		return c.v.BindPFlags(flagSet)
	}
}

// WithRemoteProvider 设置远程配置中心
// provider: "etcd", "consul", "firestore" 等
// endpoint: 远程地址，如 "127.0.0.1:2379"
// path: 配置在远程中心的路径，如 "/config/myapp"
func WithRemoteProvider(provider, endpoint, path string) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		if err := c.v.AddRemoteProvider(provider, endpoint, path); err != nil {
			return err
		}

		// 读取远程配置
		if err := c.v.ReadRemoteConfig(); err != nil {
			return fmt.Errorf("failed to read remote config: %w", err)
		}
		return nil
	}
}

func InitConfigure(config interface{}, options ...ConfigureLoaderOption) error {
	loader, err := NewConfigureLoader(options...)
	if err != nil {
		return err
	}

	if err = loader.v.Unmarshal(config, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
		return err
	}

	v := validator.New()
	if err = v.Struct(config); err != nil {
		var validationErrors validator.ValidationErrors
		if errors.As(err, &validationErrors) {
			table := uitable.New()
			table.Separator = " "
			for _, e := range validationErrors {
				if e.Tag() == "oneof" {
					table.AddRow(fmt.Sprintf("Validate '%s'", e.StructNamespace()), fmt.Sprintf("failed: oneof [%v]", e.Param()))
				} else {
					table.AddRow(fmt.Sprintf("Validate '%s'", e.StructNamespace()), fmt.Sprintf("failed: %s", e.Tag()))
				}
			}
			return errors.New(table.String())
		}
		return err
	}

	return nil
}

// NewConfigureLoader 创建一个新的配置加载器
func NewConfigureLoader(options ...ConfigureLoaderOption) (*ConfigureLoader, error) {
	if len(options) == 0 {
		return nil, errors.New("please provide at least one configure loader option")
	}

	pflag.Parse()

	loader := &ConfigureLoader{v: viper.New()}

	for _, option := range options {
		if err := option(loader); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return loader, nil
}

func ValidateNode(object interface{}) error {
	v := validator.New()
	if err := v.Struct(object); err != nil {
		var validationErrors validator.ValidationErrors
		if errors.As(err, &validationErrors) {
			var errs []error
			for _, e := range validationErrors {
				errs = append(errs, fmt.Errorf("field '%s' validation failed on the '%s' tag", e.StructNamespace(), e.Tag()))
			}
			return errors.Join(errs...)
		}
		return err
	}
	return nil // 验证通过
}

// // Load 加载并解析所有传入的配置节点
// // 它会按顺序执行：读取配置文件 -> 绑定标志 -> 解析命令行 -> Unmarshal -> 验证
// func (l *ConfigureLoader) Load(node Configurer) error {
//
//	// 5. 将 Viper 中的所有值 Unmarshal 到对应的配置节点
//	// 此时的值已经融合了：配置文件默认值 + 环境变量 + 命令行参数
//	if err := node.Bind(l.v); err != nil {
//		return fmt.Errorf("failed to unmarshal final config for node %T: %w", node, err)
//	}
//
//	// 6. 验证所有配置节点的有效性
//	if err := node.Validate(); err != nil {
//		return fmt.Errorf("failed to validate final config for node %T: %w", node, err)
//	}
//
//	return nil
// }

// Watch 监听配置文件和远程配置的变更
// 当配置变更时，会调用提供的 callback 函数
func (l *ConfigureLoader) Watch(callback func(fsnotify.Event)) {
	// 创建一个 context 用于取消远程监听
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // 确保在函数退出时取消
	println(ctx)

	// 监听本地配置文件
	l.v.WatchConfig()
	l.v.OnConfigChange(callback)
	log.Println("Watching for local config file changes...")

	// // 如果设置了远程配置，则启动一个 goroutine 来监听远程变更
	// l.v.WatchRemoteConfigOnChannel()
	// if l.remote {
	// 	go l.watchRemoteConfig(ctx, callback)
	// }
}

// watchRemoteConfig 定期轮询远程配置中心
func (l *ConfigureLoader) watchRemoteConfig(ctx context.Context, callback func(e fsnotify.Event)) {
	ticker := time.NewTicker(5 * time.Second) // 每5秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping remote config watcher.")
			return
		case <-ticker.C:
			if err := l.v.WatchRemoteConfig(); err != nil {
				log.Printf("Error watching remote config: %v", err)
				continue
			}
			callback(fsnotify.Event{Name: "remote_config", Op: fsnotify.Write})
		}
	}
}

// ============================================

type CustomConfig struct {
	Application Application `mapstructure:"application"`
	Database    Database    `mapstructure:"database"`
	Redis       Redis       `mapstructure:"redis"`
	HTTP        HTTP        `mapstructure:"http"`
}

func NewCustomConfig() *CustomConfig {
	return &CustomConfig{}
}
