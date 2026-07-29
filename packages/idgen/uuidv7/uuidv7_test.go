package uuidv7

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratorProducesOrderedUUIDv7(t *testing.T) {
	generator := New()
	first, err := generator.Generate(context.Background())
	require.NoError(t, err)
	second, err := generator.Generate(context.Background())
	require.NoError(t, err)

	parsed, err := uuid.Parse(first.String())
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), parsed.Version())
	assert.Less(t, first.String(), second.String())
}

func TestGeneratorHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().Generate(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
