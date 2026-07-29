package config

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// ConfigureLoader 负责聚合并加载多来源配置。
type ConfigureLoader struct {
	v            *viper.Viper        // 配置管理器
	fileSource   *configFileSource   // 待加载的本地配置源
	remoteSource *remoteConfigSource // 待加载的远程配置源
	remoteReady  bool                // 远程 provider 已注册，可用于后续轮询
	strictDecode bool                // 是否拒绝目标结构体中不存在的配置键
}

func InitConfigure(config interface{}, options ...ConfigureLoaderOption) error {
	loader, err := NewConfigureLoader(options...)
	if err != nil {
		return err
	}

	return loader.Load(config)
}

// LoadFromViper 将 Viper 中的配置反序列化到目标对象，并执行结构体验证。
func LoadFromViper(v *viper.Viper, config interface{}) error {
	if v == nil {
		return errors.New("viper instance is nil")
	}
	return loadFromViper(v, config, false)
}

// Load 将已聚合的配置源解码到目标对象，并执行最终校验。
func (l *ConfigureLoader) Load(config interface{}) error {
	if l == nil || l.v == nil {
		return errors.New("configure loader is nil")
	}
	return loadFromViper(l.v, config, l.strictDecode)
}

func loadFromViper(v *viper.Viper, config interface{}, strict bool) error {
	if err := validateTarget(config); err != nil {
		return err
	}
	for _, spec := range getConfigFlagSpecs(reflect.TypeOf(config), "", true) {
		v.SetDefault(spec.Name, spec.Default)
	}

	var err error
	if strict {
		err = v.UnmarshalExact(config)
	} else {
		err = v.Unmarshal(config)
	}
	if err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	return Validate(config)
}

// TargetError 表示配置解码目标不是可写的结构体指针。
type TargetError struct {
	Reason string
}

func (e *TargetError) Error() string {
	return "invalid config target: " + e.Reason
}

func validateTarget(config interface{}) error {
	if config == nil {
		return &TargetError{Reason: "target is nil"}
	}
	value := reflect.ValueOf(config)
	if value.Kind() != reflect.Ptr {
		return &TargetError{Reason: "target must be a pointer to a struct"}
	}
	if value.IsNil() {
		return &TargetError{Reason: "target pointer is nil"}
	}
	if value.Elem().Kind() != reflect.Struct {
		return &TargetError{Reason: "target must point to a struct"}
	}
	return nil
}

// NewConfigureLoader 创建一个新的配置加载器
func NewConfigureLoader(options ...ConfigureLoaderOption) (*ConfigureLoader, error) {
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

// ValidateNode 保留旧名称以兼容现有调用；新代码应使用 Validate。
// Deprecated: 使用 Validate。
func ValidateNode(object interface{}) error {
	return Validate(object)
}

// Watch 监听本地配置文件的变更。当文件发生变更时，调用提供的 callback 函数。
// 内部使用 viper 的 WatchConfig + OnConfigChange 实现文件系统级监听。
func (l *ConfigureLoader) Watch(callback func(fsnotify.Event)) {
	if l.fileSource != nil && l.fileSource.filesystem != nil {
		log.Println("Embedded config source does not support file watching.")
		return
	}
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
