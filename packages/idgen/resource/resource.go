// Package resource adapts ID generators to the modular Resource lifecycle.
package resource

import (
	"context"

	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/idgen"
	"github.com/wplbyx/modular/packages/idgen/snowflake"
	"github.com/wplbyx/modular/packages/idgen/uuidv7"
)

// NewUUIDv7 creates a ready-on-Setup UUIDv7 Generator provider.
func NewUUIDv7(name string) *core.ManagedResource[idgen.Generator] {
	return core.NewManagedResource[idgen.Generator](
		name,
		func(context.Context) (idgen.Generator, error) { return uuidv7.New(), nil },
		nil,
	)
}

// NewSnowflake creates a leased Snowflake Generator provider.
func NewSnowflake(
	name string,
	layout snowflake.Layout,
	provider idgen.NodeLeaseProvider,
) *core.ManagedResource[idgen.Generator] {
	return core.NewManagedResource[idgen.Generator](
		name,
		func(ctx context.Context) (idgen.Generator, error) {
			return snowflake.New(ctx, layout, provider)
		},
		func(ctx context.Context, generator idgen.Generator) error {
			return generator.(*snowflake.Generator).Close(ctx)
		},
	)
}
