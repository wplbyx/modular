package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRead(t *testing.T) {
	info := Read("orders", "v1.2.3")

	assert.Equal(t, "orders", info.Name)
	assert.Equal(t, "v1.2.3", info.Version)
	require.NotEmpty(t, info.GoVersion)
}
