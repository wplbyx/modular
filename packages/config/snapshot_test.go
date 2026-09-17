package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSnapshotWatcher_RejectsInvalidAndPublishesFreshValues(t *testing.T) {
	type settings struct {
		Port int `mapstructure:"Port" validate:"min=1"`
	}
	file := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(file, []byte("Port: 8080\n"), 0600))
	w, err := NewSnapshotWatcher[settings](5*time.Millisecond, WithConfigFile(file, false))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values := make(chan *settings, 3)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(_ context.Context, s *settings) error { values <- s; return nil }) }()
	var first *settings
	select {
	case first = <-values:
		require.Equal(t, 8080, first.Port)
	case <-ctx.Done():
		t.Fatal("no initial snapshot")
	}
	require.NoError(t, os.WriteFile(file, []byte("Port: 0\n"), 0600))
	select {
	case <-w.Errors():
	case <-ctx.Done():
		t.Fatal("invalid snapshot not reported")
	}
	require.NoError(t, os.WriteFile(file, []byte("Port: 9090\n"), 0600))
	select {
	case next := <-values:
		require.Equal(t, 9090, next.Port)
		require.Equal(t, 8080, first.Port)
	case <-ctx.Done():
		t.Fatal("no updated snapshot")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
