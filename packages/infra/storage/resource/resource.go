// Package resource provides Application lifecycle composition for storage backends.
package resource

import (
	"context"
	"errors"
	"fmt"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/infra/storage"
	"github.com/wplbyx/modular/packages/infra/storage/alioss"
	"github.com/wplbyx/modular/packages/infra/storage/filedisk"
)

// Factory 根据配置创建具体的 Storage 适配器。
type Factory func(*configitem.Storage) (storage.Storage, error)

type resourceConfig struct {
	factory Factory
}

// Option 配置 Storage Resource。
type Option func(*resourceConfig)

// WithFactory 覆盖 Storage 构造函数，主要用于测试或应用侧扩展。
func WithFactory(factory Factory) Option {
	return func(cfg *resourceConfig) {
		if factory != nil {
			cfg.factory = factory
		}
	}
}

// New 创建 disk 或 OSS Storage 生命周期资源。
func New(cfg *configitem.Storage, options ...Option) *core.ManagedResource[storage.Storage] {
	resourceCfg := resourceConfig{factory: newStorage}
	for _, option := range options {
		if option != nil {
			option(&resourceCfg)
		}
	}

	return core.NewManagedResource(
		"storage",
		func(context.Context) (storage.Storage, error) {
			if cfg == nil {
				return nil, errors.New("storage config is nil")
			}
			return resourceCfg.factory(cfg)
		},
		nil,
	)
}

func newStorage(cfg *configitem.Storage) (storage.Storage, error) {
	switch cfg.Type {
	case "disk":
		return filedisk.NewDiskStorage(cfg)
	case "oss":
		return alioss.NewOSSStorage(cfg)
	default:
		return nil, fmt.Errorf("%w: %s", storage.ErrUnsupportedStorageType, cfg.Type)
	}
}
