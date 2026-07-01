package registry

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"modular/packages/core"
)

type K8sRegistry struct {
	clientset kubernetes.Interface
	namespace string
	stopCh    chan struct{}
	stopOnce  sync.Once
	informers informers.SharedInformerFactory
}

func newK8sRegistry(namespace string) (*K8sRegistry, error) {
	var config *rest.Config
	var err error

	config, err = rest.InClusterConfig()
	if err != nil {
		log.Println("Not in cluster, trying to use local kubeconfig...")
		kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	stopCh := make(chan struct{})
	informers := informers.NewSharedInformerFactoryWithOptions(clientset, time.Minute, informers.WithNamespace(namespace))

	return &K8sRegistry{
		clientset: clientset,
		namespace: namespace,
		stopCh:    stopCh,
		informers: informers,
	}, nil
}

// Register 在 K8s 中是空操作
func (r *K8sRegistry) Register(ctx context.Context, node *core.ServiceNode) error {
	log.Println("K8s registry: Register is a no-op.")
	return nil
}

// Unregister 在 K8s 中是空操作
func (r *K8sRegistry) Unregister(ctx context.Context, node *core.ServiceNode) error {
	log.Println("K8s registry: Unregister is a no-op.")
	return nil
}

func (r *K8sRegistry) Discover(serviceName string) ([]*core.ServiceNode, error) {
	endpoints, err := r.clientset.CoreV1().Endpoints(r.namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints for service %s: %w", serviceName, err)
	}
	return endpointsToInstances(serviceName, endpoints), nil
}

func (r *K8sRegistry) Watch(serviceName string) (<-chan []*core.ServiceNode, <-chan struct{}, error) {
	updateCh := make(chan []*core.ServiceNode, 1)
	sendUpdate := func(nodes []*core.ServiceNode) {
		select {
		case updateCh <- nodes:
		default:
		}
	}

	endpointsInformer := r.informers.Core().V1().Endpoints().Informer()

	_, err := endpointsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ep := obj.(*corev1.Endpoints)
			if ep.Name == serviceName {
				sendUpdate(endpointsToInstances(serviceName, ep))
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			ep := newObj.(*corev1.Endpoints)
			if ep.Name == serviceName {
				sendUpdate(endpointsToInstances(serviceName, ep))
			}
		},
		DeleteFunc: func(obj interface{}) {
			if ep, ok := obj.(*corev1.Endpoints); ok && ep.Name == serviceName {
				sendUpdate([]*core.ServiceNode{})
			}
		},
	})
	if err != nil {
		return nil, nil, err
	}

	go r.informers.Start(r.stopCh)

	return updateCh, r.stopCh, nil
}

func (r *K8sRegistry) Close() error {
	log.Println("Closing K8s registry...")
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	return nil
}

// endpointsToInstances 将 K8s Endpoints 对象转换为 ServiceNode 列表
func endpointsToInstances(serviceName string, ep *corev1.Endpoints) []*core.ServiceNode {
	var instances []*core.ServiceNode
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			for _, port := range subset.Ports {
				instances = append(instances, &core.ServiceNode{
					Identity: core.Identity{
						Name: serviceName,
					},
					ID: fmt.Sprintf("%s-%s", serviceName, addr.TargetRef.Name),
					Transports: []core.Transport{
						{
							Protocol: port.Name,
							Address:  addr.IP,
							Port:     int(port.Port),
						},
					},
				})
			}
		}
	}
	return instances
}
