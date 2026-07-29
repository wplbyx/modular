// Package uuidv7 provides a coordination-free UUID version 7 ID generator.
package uuidv7

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/wplbyx/modular/packages/idgen"
)

// Generator produces time-ordered UUID version 7 identifiers.
type Generator struct{}

var _ idgen.Generator = (*Generator)(nil)

// New creates a coordination-free UUIDv7 generator.
func New() *Generator { return &Generator{} }

// Generate returns one canonical UUIDv7 string.
func (g *Generator) Generate(ctx context.Context) (idgen.ID, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7: %w", err)
	}
	return idgen.ID(value.String()), nil
}
