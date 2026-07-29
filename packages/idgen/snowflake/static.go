package snowflake

import (
	"context"
	"fmt"
	"sync"

	"github.com/wplbyx/modular/packages/idgen"
)

type staticNodeProvider struct{ nodeID uint64 }

// StaticNode returns a provider for deployments that guarantee a unique node ID externally.
func StaticNode(nodeID uint64) idgen.NodeLeaseProvider {
	return staticNodeProvider{nodeID: nodeID}
}

func (provider staticNodeProvider) Acquire(ctx context.Context, maxNodeID uint64) (idgen.NodeLease, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if provider.nodeID > maxNodeID {
		return nil, fmt.Errorf("%w: node ID %d exceeds maximum %d", idgen.ErrInvalidConfig, provider.nodeID, maxNodeID)
	}
	return &staticNodeLease{nodeID: provider.nodeID, lost: make(chan struct{})}, nil
}

type staticNodeLease struct {
	nodeID uint64
	once   sync.Once
	lost   chan struct{}
}

func (lease *staticNodeLease) NodeID() uint64 { return lease.nodeID }

func (lease *staticNodeLease) Lost() <-chan struct{} {
	return lease.lost
}

func (lease *staticNodeLease) Close(context.Context) error {
	lease.once.Do(func() {
		close(lease.lost)
	})
	return nil
}
