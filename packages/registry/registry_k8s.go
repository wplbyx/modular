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

	"github.com/wplbyx/modular/packages/core"
)

var _ Discovery = (*K8sRegistry)(nil)

// K8sRegistry 基于 Kubernetes Endpoints 实现服务发现。
// 在 K8s 环境中，服务的注册由 Kubernetes 本身（Deployment+Service）完成，
// 因此 Register/Unregister 是空操作。
// 发现逻辑通过 Endpoints 资源实现。
type K8sRegistry struct {
	clientset kubernetes.Interface
	namespace string
	stopCh    chan struct{}
	stopOnce  sync.Once
	informers informers.SharedInformerFactory
	startOnce sync.Once
}

// NewK8sRegistry 创建 K8s 服务发现实例。
// 优先使用 InClusterConfig，回退到本地 kubeconfig。
func NewK8sRegistry(namespace string) (*K8sRegistry, error) {
	if namespace == "" {
		namespace = "default"
	}

	config, err := rest.InClusterConfig()
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
	informersFactory := informers.NewSharedInformerFactoryWithOptions(clientset, time.Minute, informers.WithNamespace(namespace))

	return &K8sRegistry{
		clientset: clientset,
		namespace: namespace,
		stopCh:    stopCh,
		informers: informersFactory,
	}, nil
}

// Register 在 K8s 中是空操作（由 K8s 平台自身完成注册）。
func (r *K8sRegistry) Register(ctx context.Context, node *core.ServiceNode) error {
	return nil
}

// Unregister 在 K8s 中是空操作。
func (r *K8sRegistry) Unregister(ctx context.Context, node *core.ServiceNode) error {
	return nil
}

// GetService 从 K8s Endpoints 获取服务实例列表。
func (r *K8sRegistry) GetService(ctx context.Context, serviceName string) ([]*core.ServiceNode, error) {
	endpoints, err := r.clientset.CoreV1().Endpoints(r.namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints for service %s: %w", serviceName, err)
	}
	return endpointsToServiceNodes(serviceName, endpoints), nil
}

// Watch 监控服务实例变化，返回变化通道。
// 当 context 取消时，informer 停止并关闭通道。
// informers factory 仅在首次 Watch 时启动（通过 startOnce 保证）。
func (r *K8sRegistry) Watch(ctx context.Context, serviceName string) (<-chan []*core.ServiceNode, error) {
	ch := make(chan []*core.ServiceNode, 1)
	sendUpdate := func(nodes []*core.ServiceNode) {
		select {
		case ch <- nodes:
		default:
		}
	}

	endpointsInformer := r.informers.Core().V1().Endpoints().Informer()

	_, err := endpointsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if ep, ok := obj.(*corev1.Endpoints); ok && ep.Name == serviceName {
				sendUpdate(endpointsToServiceNodes(serviceName, ep))
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if ep, ok := newObj.(*corev1.Endpoints); ok && ep.Name == serviceName {
				sendUpdate(endpointsToServiceNodes(serviceName, ep))
			}
		},
		DeleteFunc: func(obj interface{}) {
			if ep, ok := obj.(*corev1.Endpoints); ok && ep.Name == serviceName {
				sendUpdate([]*core.ServiceNode{})
			}
		},
	})
	if err != nil {
		return nil, err
	}

	// informers factory 仅启动一次，避免多次 Watch 重复 Start
	r.startOnce.Do(func() {
		r.informers.Start(r.stopCh)
	})

	// 发送初始快照
	go func() {
		defer close(ch)
		if nodes, err := r.GetService(ctx, serviceName); err == nil {
			sendUpdate(nodes)
		}
		<-ctx.Done()
	}()

	return ch, nil
}

// Close 关闭 K8s informer。
func (r *K8sRegistry) Close() error {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	return nil
}

// endpointsToServiceNodes 将 K8s Endpoints 对象转换为 ServiceNode 列表。
func endpointsToServiceNodes(serviceName string, ep *corev1.Endpoints) []*core.ServiceNode {
	var nodes []*core.ServiceNode
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			for _, port := range subset.Ports {
				nodes = append(nodes, &core.ServiceNode{
					Name: serviceName,
					ID:   fmt.Sprintf("%s-%s", serviceName, addr.TargetRef.Name),
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
	return nodes
}
