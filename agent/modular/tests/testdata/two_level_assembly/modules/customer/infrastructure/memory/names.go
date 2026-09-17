package memory

import (
	"context"
	"fmt"

	"github.com/wplbyx/modular/packages/core"
)

type Names struct {
	data core.Provider[map[string]string]
}

func NewNames(data core.Provider[map[string]string]) *Names { return &Names{data: data} }

func (n *Names) Find(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := n.data.Value()
	if err != nil {
		return "", err
	}
	name, ok := data[id]
	if !ok {
		return "", fmt.Errorf("unknown fixture customer %s", id)
	}
	return name, nil
}
