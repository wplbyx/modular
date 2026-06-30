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

	// 尝试在集群内运行
	config, err = rest.InClusterConfig()
	if err != nil {
		// 如果不在集群内，则尝试使用本地 kubeconfig
		log.Println("Not in cluster, trying to use local kubeconfig...")
		kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config (both in-cluster and out-of-cluster failed): %w", err)
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

// Register 在 K8s 中是空操作，服务通过 Deployment/StatefulSet 和 Service 对象自动注册
func (r *K8sRegistry) Register(ctx context.Context, instance *ServiceNode) error {
	log.Println("K8s registry: Register is a no-op. Service registration is handled by K8s objects.")
	return nil
}

// Deregister 在 K8s 中是空操作
func (r *K8sRegistry) Deregister(ctx context.Context, instanceID string) error {
	log.Println("K8s registry: Deregister is a no-op. Service deregistration is handled by K8s.")
	return nil
}

func (r *K8sRegistry) Discover(serviceName string) ([]*ServiceNode, error) {
	endpoints, err := r.clientset.CoreV1().Endpoints(r.namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints for service %s: %w", serviceName, err)
	}
	// // discoveryv1.EndpointSlice
	// for i, subset := range endpoints.Subsets() {
	//
	// }
	// var instances []*ServiceNode
	// for _, subset := range endpoints.Subsets() {
	// 	for _, addr := range subset.Addresses {
	// 		for _, port := range subset.Ports {
	// 			instances = append(instances, &ServiceNode{
	// 				ID:      fmt.Sprintf("%s-%s", serviceName, addr.TargetRef.Name),
	// 				Name:    serviceName,
	// 				Address: addr.IP,
	// 				Port:    int(port.Port),
	// 			})
	// 		}
	// 	}
	// }

	return endpointsToInstances(serviceName, endpoints), nil
}

func (r *K8sRegistry) Watch(serviceName string) (<-chan []*ServiceNode, <-chan struct{}, error) {
	updateCh := make(chan []*ServiceNode, 1)
	sendUpdate := func(nodes []*ServiceNode) {
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
			// 服务被删除，发送空列表
			if ep, ok := obj.(*corev1.Endpoints); ok && ep.Name == serviceName {
				sendUpdate([]*ServiceNode{})
			}
		},
	})
	if err != nil {
		return nil, nil, err
	}

	// 启动 informer，只启动一次
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

// endpointsToInstances 将 K8s Endpoints 对象转换为 ServiceInstance 列表
func endpointsToInstances(serviceName string, ep *corev1.Endpoints) []*ServiceNode {
	var instances []*ServiceNode
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			for _, port := range subset.Ports {
				instances = append(instances, &ServiceNode{
					ID:      fmt.Sprintf("%s-%s", serviceName, addr.TargetRef.Name),
					Name:    serviceName,
					Address: addr.IP,
					Port:    int(port.Port),
				})
			}
		}
	}
	return instances
}
