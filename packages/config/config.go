package config

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
	validator "github.com/go-playground/validator/v10"
	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/gosuri/uitable"
	"github.com/spf13/viper"
)

// ConfigureLoader 负责聚合并加载多来源配置。
type ConfigureLoader struct {
	v            *viper.Viper        // 配置管理器
	fileSource   *configFileSource   // 待加载的本地配置源
	remoteSource *remoteConfigSource // 待加载的远程配置源
	remoteReady  bool                // 远程 provider 已注册，可用于后续轮询
}

func InitConfigure(config interface{}, options ...ConfigureLoaderOption) error {
	loader, err := NewConfigureLoader(options...)
	if err != nil {
		return err
	}

	return LoadFromViper(loader.v, config)
}

// LoadFromViper 将 Viper 中的配置反序列化到目标对象，并执行结构体验证。
func LoadFromViper(v *viper.Viper, config interface{}) error {
	if v == nil {
		return errors.New("viper instance is nil")
	}
	if err := v.Unmarshal(config, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
		return err
	}
	return validateConfig(config)
}

func validateConfig(config interface{}) error {
	v := validator.New()
	if err := v.Struct(config); err != nil {
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

	loader := &ConfigureLoader{v: viper.New()}

	for _, option := range options {
		if err := option(loader); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}
	if err := loader.loadConfigSources(); err != nil {
		return nil, fmt.Errorf("failed to load config source: %w", err)
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

// Watch 监听本地配置文件的变更。当文件发生变更时，调用提供的 callback 函数。
// 内部使用 viper 的 WatchConfig + OnConfigChange 实现文件系统级监听。
func (l *ConfigureLoader) Watch(callback func(fsnotify.Event)) {
	l.v.OnConfigChange(callback)
	l.v.WatchConfig()
	log.Println("Watching for local config file changes...")
}

// WatchRemoteConfig 定期轮询远程配置中心（etcd/consul/firestore），
// 当检测到变更时触发 callback。调用方负责通过 ctx 控制轮询生命周期。
func (l *ConfigureLoader) WatchRemoteConfig(ctx context.Context, callback func(e fsnotify.Event)) {
	if l == nil || !l.remoteReady {
		log.Println("Remote config watcher is disabled because no compatible remote provider is registered.")
		return
	}

	ticker := time.NewTicker(5 * time.Second) // 每 5 秒检查一次
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
