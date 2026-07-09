package bun

import (
	"context"
	"errors"

	bunlib "github.com/uptrace/bun"

	"modular/packages/config"
	"modular/packages/core"
)

var _ core.Resource = (*Resource)(nil)

// Resource 将 Bun 数据库连接纳入 Application 生命周期。
type Resource struct {
	cfg     *config.Database
	db      *bunlib.DB
	connect func(*config.Database) (*bunlib.DB, error)
}

type ResourceOption func(*Resource)

// WithConnector 覆盖 Bun 建连函数，主要用于测试或自定义连接。
func WithConnector(fn func(*config.Database) (*bunlib.DB, error)) ResourceOption {
	return func(r *Resource) {
		if fn != nil {
			r.connect = fn
		}
	}
}

// NewResource 创建 Bun 生命周期资源。
func NewResource(cfg *config.Database, opts ...ResourceOption) *Resource {
	r := &Resource{cfg: cfg, connect: NewBunConnection}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

func (r *Resource) Name() string { return "bun" }

func (r *Resource) Setup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.cfg == nil {
		return errors.New("database config is nil")
	}
	db, err := r.connect(r.cfg)
	if err != nil {
		return err
	}
	r.db = db
	return nil
}

func (r *Resource) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}

func (r *Resource) DB() *bunlib.DB { return r.db }
