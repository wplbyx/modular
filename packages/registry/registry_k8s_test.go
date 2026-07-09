package registry

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestEndpointsToServiceNodesAllowsNilTargetRef(t *testing.T) {
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "orders"},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.5"}},
				Ports:     []corev1.EndpointPort{{Name: "grpc", Port: 50051}},
			},
		},
	}

	nodes := endpointsToServiceNodes("orders", ep)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].ID != "orders-10.0.0.5-50051" {
		t.Fatalf("node ID = %q", nodes[0].ID)
	}
	if nodes[0].Transports[0].Address != "10.0.0.5" {
		t.Fatalf("transport address = %q", nodes[0].Transports[0].Address)
	}
}

func TestK8sWatchCancelRemovesHandler(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "default"},
	})
	factory := informers.NewSharedInformerFactoryWithOptions(client, time.Hour, informers.WithNamespace("default"))
	registry := &K8sRegistry{
		clientset: client,
		namespace: "default",
		stopCh:    make(chan struct{}),
		informers: factory,
	}
	defer registry.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := registry.Watch(ctx, "orders")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	if ok := cache.WaitForCacheSync(ctx.Done(), factory.Core().V1().Endpoints().Informer().HasSynced); !ok {
		t.Fatal("informer cache did not sync")
	}

	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(time.Second):
		t.Fatal("watch channel did not close")
	}

	_, err = client.CoreV1().Endpoints("default").Update(context.Background(), &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "default"},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.6"}},
				Ports:     []corev1.EndpointPort{{Name: "grpc", Port: 50051}},
			},
		},
	}, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("update endpoints: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
}
