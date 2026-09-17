package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/core"
	"google.golang.org/grpc/resolver"
)

type snapshotDiscovery struct {
	names   chan string
	updates chan []*core.ServiceNode
}

func TestConsul_PartialRegistrationRollsBack(t *testing.T) {
	var mu sync.Mutex
	var removed []string
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "deregister/") {
			removed = append(removed, r.URL.Path)
			return
		}
		attempts++
		if attempts == 2 {
			http.Error(w, "unavailable", 500)
		}
	}))
	defer server.Close()
	registry, err := NewConsulRegistry(server.URL)
	require.NoError(t, err)
	err = registry.Register(context.Background(), &core.ServiceNode{ID: "node", Name: "orders", Transports: []core.Transport{{Protocol: "http", Address: "localhost", Port: 80}, {Protocol: "grpc", Address: "localhost", Port: 90}}})
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, removed, 1)
}

func (d *snapshotDiscovery) GetService(context.Context, string) ([]*core.ServiceNode, error) {
	return nil, nil
}
func (d *snapshotDiscovery) Watch(ctx context.Context, name string) (<-chan []*core.ServiceNode, error) {
	d.names <- name
	return d.updates, nil
}

type snapshotConnection struct {
	resolver.ClientConn
	states chan resolver.State
}

func (c *snapshotConnection) UpdateState(state resolver.State) error { c.states <- state; return nil }
func (c *snapshotConnection) ReportError(error)                      {}
func TestResolver_ServiceNameIPv6AndEmptySnapshot(t *testing.T) {
	d := &snapshotDiscovery{make(chan string, 1), make(chan []*core.ServiceNode, 2)}
	c := &snapshotConnection{states: make(chan resolver.State, 2)}
	u, err := url.Parse(BuildConsulTarget("orders"))
	require.NoError(t, err)
	r, err := NewGRPCResolverBuilder(d).Build(resolver.Target{URL: *u}, c, resolver.BuildOptions{})
	require.NoError(t, err)
	defer r.Close()
	require.Equal(t, "orders", <-d.names)
	d.updates <- []*core.ServiceNode{{Name: "orders", Transports: []core.Transport{{Protocol: "grpc", Address: "::1", Port: 1234}}}}
	d.updates <- nil
	select {
	case s := <-c.states:
		require.Equal(t, "[::1]:1234", s.Addresses[0].Addr)
	case <-time.After(time.Second):
		t.Fatal("no addresses")
	}
	select {
	case s := <-c.states:
		require.Empty(t, s.Addresses)
	case <-time.After(time.Second):
		t.Fatal("empty snapshot not delivered")
	}
}
