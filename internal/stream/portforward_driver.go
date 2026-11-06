package stream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"kubehack/internal/cluster"
)

// forwardAddress is the only address forwards listen on. Binding anything wider
// would hand the cluster's internal services to the local network.
const forwardAddress = "127.0.0.1"

// ClusterForwards is the ForwardDriver for real clusters.
type ClusterForwards struct{}

func forwardClient(contextName string) (*rest.Config, kubernetes.Interface, error) {
	config, err := cluster.RESTConfig(contextName)
	if err != nil {
		return nil, nil, err
	}
	config = rest.AddUserAgent(config, "kubehack")
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("create client: %w", err)
	}
	return config, client, nil
}

// Resolve implements ForwardDriver.
func (ClusterForwards) Resolve(ctx context.Context, request ForwardRequest) (ForwardTarget, error) {
	_, client, err := forwardClient(request.Context)
	if err != nil {
		return ForwardTarget{}, err
	}
	return resolveForward(ctx, client, request)
}

func targetErrorf(format string, args ...any) error {
	return ForwardTargetError(fmt.Sprintf(format, args...))
}

func resolveForward(ctx context.Context, client kubernetes.Interface, request ForwardRequest) (ForwardTarget, error) {
	if request.Kind == ForwardService {
		return resolveService(ctx, client, request)
	}
	pod, err := client.CoreV1().Pods(request.Namespace).Get(ctx, request.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ForwardTarget{}, targetErrorf("Pod %s no longer exists.", request.Name)
	}
	if err != nil {
		return ForwardTarget{}, err
	}
	if pod.Status.Phase != corev1.PodRunning {
		return ForwardTarget{}, targetErrorf("Pod %s is %s, not running.", pod.Name, pod.Status.Phase)
	}
	return ForwardTarget{Pod: pod.Name, UID: string(pod.UID), Port: request.Port}, nil
}

func resolveService(ctx context.Context, client kubernetes.Interface, request ForwardRequest) (ForwardTarget, error) {
	service, err := client.CoreV1().Services(request.Namespace).Get(ctx, request.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ForwardTarget{}, targetErrorf("Service %s no longer exists.", request.Name)
	}
	if err != nil {
		return ForwardTarget{}, err
	}
	if len(service.Spec.Selector) == 0 {
		return ForwardTarget{}, targetErrorf("Service %s has no selector, so it has no pods to connect to.", service.Name)
	}
	var servicePort *corev1.ServicePort
	for i := range service.Spec.Ports {
		if int(service.Spec.Ports[i].Port) == request.Port {
			servicePort = &service.Spec.Ports[i]
		}
	}
	if servicePort == nil {
		return ForwardTarget{}, targetErrorf("Service %s has no port %d.", service.Name, request.Port)
	}

	pods, err := client.CoreV1().Pods(request.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(service.Spec.Selector).String(),
	})
	if err != nil {
		return ForwardTarget{}, err
	}
	sort.Slice(pods.Items, func(i, j int) bool { return pods.Items[i].Name < pods.Items[j].Name })
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podReady(pod) {
			continue
		}
		port, err := targetPort(pod, *servicePort)
		if err != nil {
			return ForwardTarget{}, err
		}
		return ForwardTarget{Pod: pod.Name, UID: string(pod.UID), Port: port}, nil
	}
	return ForwardTarget{}, targetErrorf("Service %s has no ready pods.", service.Name)
}

func podReady(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// targetPort finds the pod port behind a service port. A service can name a
// container port, and that name means something different in each pod.
func targetPort(pod *corev1.Pod, port corev1.ServicePort) (int, error) {
	if port.TargetPort.Type == intstr.String && port.TargetPort.StrVal != "" {
		for _, container := range pod.Spec.Containers {
			for _, containerPort := range container.Ports {
				if containerPort.Name == port.TargetPort.StrVal {
					return int(containerPort.ContainerPort), nil
				}
			}
		}
		return 0, targetErrorf("Pod %s has no port named %s.", pod.Name, port.TargetPort.StrVal)
	}
	if port.TargetPort.IntVal != 0 {
		return int(port.TargetPort.IntVal), nil
	}
	return int(port.Port), nil
}

// Run implements ForwardDriver.
func (ClusterForwards) Run(ctx context.Context, request ForwardRequest, target ForwardTarget, ready func(int)) error {
	config, client, err := forwardClient(request.Context)
	if err != nil {
		return err
	}
	url := client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(request.Namespace).Name(target.Pod).SubResource("portforward").URL()

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("create spdy transport: %w", err)
	}
	spdyDialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	webSocketDialer, err := portforward.NewSPDYOverWebsocketDialer(url, config)
	if err != nil {
		return fmt.Errorf("create websocket dialer: %w", err)
	}
	// Older API servers only speak SPDY, the same fallback kubectl uses.
	dialer := portforward.NewFallbackDialer(webSocketDialer, spdyDialer, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})

	listening := make(chan struct{})
	var listenErrors strings.Builder
	forwarder, err := portforward.NewOnAddressesWithContext(
		ctx,
		dialer,
		[]string{forwardAddress},
		[]string{fmt.Sprintf("%d:%d", request.LocalPort, target.Port)},
		listening,
		io.Discard,
		&listenErrors,
	)
	if err != nil {
		return fmt.Errorf("create port forward: %w", err)
	}

	go func() {
		select {
		case <-listening:
			ports, err := forwarder.GetPorts()
			if err == nil && len(ports) == 1 {
				ready(int(ports[0].Local))
			}
		case <-ctx.Done():
		}
	}()

	err = forwarder.ForwardPorts()
	if strings.Contains(listenErrors.String(), "address already in use") {
		return fmt.Errorf("%w: %d", ErrLocalPortInUse, request.LocalPort)
	}
	return err
}

// Check implements ForwardDriver.
func (ClusterForwards) Check(ctx context.Context, request ForwardRequest, target ForwardTarget) error {
	_, client, err := forwardClient(request.Context)
	if err != nil {
		return err
	}
	return checkForward(ctx, client, request, target)
}

func checkForward(ctx context.Context, client kubernetes.Interface, request ForwardRequest, target ForwardTarget) error {
	pod, err := client.CoreV1().Pods(request.Namespace).Get(ctx, target.Pod, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return PodGoneError{Reason: fmt.Sprintf("Pod %s was deleted.", target.Pod)}
	case err != nil:
		return err
	case string(pod.UID) != target.UID:
		return PodGoneError{Reason: fmt.Sprintf("Pod %s was replaced.", target.Pod)}
	case pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed:
		return PodGoneError{Reason: fmt.Sprintf("Pod %s has stopped.", target.Pod)}
	}
	return nil
}
