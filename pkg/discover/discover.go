package discover

import (
	"context"
	"fmt"
	"io"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var (
	podMonitorGVR = schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "podmonitors",
	}
	serviceMonitorGVR = schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "servicemonitors",
	}
)

// MetricsEndpoint represents a single endpoint to scrape from a monitor.
type MetricsEndpoint struct {
	Source  string // e.g. "PodMonitor/my-monitor" or "ServiceMonitor/my-monitor"
	Port    string // display label (port name or number)
	PortNum int32  // resolved pod container port number
	Path    string // HTTP path, defaults to /metrics
}

// Endpoints finds all ServiceMonitor and PodMonitor endpoints matching a pod.
func Endpoints(ctx context.Context, dynClient dynamic.Interface, clientset kubernetes.Interface, namespace string, pod *corev1.Pod, verbose bool, w io.Writer) ([]MetricsEndpoint, error) {
	var endpoints []MetricsEndpoint

	pmEndpoints, err := findPodMonitorEndpoints(ctx, dynClient, namespace, pod, verbose, w)
	if err != nil {
		return nil, err
	}
	endpoints = append(endpoints, pmEndpoints...)

	smEndpoints, err := findServiceMonitorEndpoints(ctx, dynClient, clientset, namespace, pod, verbose, w)
	if err != nil {
		return nil, err
	}
	endpoints = append(endpoints, smEndpoints...)

	return endpoints, nil
}

func findPodMonitorEndpoints(ctx context.Context, dynClient dynamic.Interface, namespace string, pod *corev1.Pod, verbose bool, w io.Writer) ([]MetricsEndpoint, error) {
	verboseLog(verbose, w, "Looking for PodMonitors in namespace %s...\n", namespace)

	list, err := dynClient.Resource(podMonitorGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing PodMonitors: %w", err)
	}

	verboseLog(verbose, w, "Found %d PodMonitor(s)\n", len(list.Items))

	var endpoints []MetricsEndpoint
	podLabels := labels.Set(pod.Labels)

	for _, item := range list.Items {
		name := item.GetName()
		spec, ok := item.Object["spec"].(map[string]interface{})
		if !ok {
			continue
		}

		sel, err := selectorFromUnstructured(spec["selector"])
		if err != nil {
			verboseLog(verbose, w, "  PodMonitor/%s: invalid selector: %v\n", name, err)
			continue
		}

		if !sel.Matches(podLabels) {
			verboseLog(verbose, w, "  PodMonitor/%s: selector does not match pod\n", name)
			continue
		}

		verboseLog(verbose, w, "  PodMonitor/%s: selector matches pod\n", name)

		eps, err := extractPodMonitorEndpoints(spec, pod)
		if err != nil {
			verboseLog(verbose, w, "  PodMonitor/%s: %v\n", name, err)
			continue
		}
		for i := range eps {
			eps[i].Source = "PodMonitor/" + name
		}
		endpoints = append(endpoints, eps...)
	}

	return endpoints, nil
}

func findServiceMonitorEndpoints(ctx context.Context, dynClient dynamic.Interface, clientset kubernetes.Interface, namespace string, pod *corev1.Pod, verbose bool, w io.Writer) ([]MetricsEndpoint, error) {
	verboseLog(verbose, w, "Looking for Services selecting pod %s in namespace %s...\n", pod.Name, namespace)

	svcList, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing Services: %w", err)
	}

	podLabels := labels.Set(pod.Labels)
	var matchingServices []corev1.Service
	for _, svc := range svcList.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		svcSelector := labels.SelectorFromSet(labels.Set(svc.Spec.Selector))
		if svcSelector.Matches(podLabels) {
			verboseLog(verbose, w, "  Service/%s selects this pod\n", svc.Name)
			matchingServices = append(matchingServices, svc)
		}
	}

	if len(matchingServices) == 0 {
		verboseLog(verbose, w, "  No services select this pod\n")
		return nil, nil
	}

	verboseLog(verbose, w, "Looking for ServiceMonitors in namespace %s...\n", namespace)

	list, err := dynClient.Resource(serviceMonitorGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing ServiceMonitors: %w", err)
	}

	verboseLog(verbose, w, "Found %d ServiceMonitor(s)\n", len(list.Items))

	var endpoints []MetricsEndpoint

	for _, item := range list.Items {
		name := item.GetName()
		spec, ok := item.Object["spec"].(map[string]interface{})
		if !ok {
			continue
		}

		sel, err := selectorFromUnstructured(spec["selector"])
		if err != nil {
			verboseLog(verbose, w, "  ServiceMonitor/%s: invalid selector: %v\n", name, err)
			continue
		}

		// Find the first matching service for port resolution.
		var matchedService *corev1.Service
		for i := range matchingServices {
			svcLabels := labels.Set(matchingServices[i].Labels)
			if sel.Matches(svcLabels) {
				verboseLog(verbose, w, "  ServiceMonitor/%s: selector matches Service/%s\n", name, matchingServices[i].Name)
				matchedService = &matchingServices[i]
				break
			}
		}

		if matchedService == nil {
			verboseLog(verbose, w, "  ServiceMonitor/%s: selector does not match any service\n", name)
			continue
		}

		eps, err := extractServiceMonitorEndpoints(spec, pod, matchedService)
		if err != nil {
			verboseLog(verbose, w, "  ServiceMonitor/%s: %v\n", name, err)
			continue
		}
		for i := range eps {
			eps[i].Source = "ServiceMonitor/" + name
		}
		endpoints = append(endpoints, eps...)
	}

	return endpoints, nil
}

// extractPodMonitorEndpoints resolves endpoints from a PodMonitor spec.
// PodMonitor ports reference pod container ports directly.
func extractPodMonitorEndpoints(spec map[string]interface{}, pod *corev1.Pod) ([]MetricsEndpoint, error) {
	raw, ok := spec["podMetricsEndpoints"].([]interface{})
	if !ok {
		return nil, nil
	}

	var endpoints []MetricsEndpoint
	for _, item := range raw {
		ep, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		path, _ := ep["path"].(string)
		if path == "" {
			path = "/metrics"
		}

		portNum, portName, err := resolveMonitorPort(ep, pod, nil)
		if err != nil {
			return nil, fmt.Errorf("resolving port: %w", err)
		}

		endpoints = append(endpoints, MetricsEndpoint{
			Port:    portName,
			PortNum: portNum,
			Path:    path,
		})
	}
	return endpoints, nil
}

// extractServiceMonitorEndpoints resolves endpoints from a ServiceMonitor spec.
// ServiceMonitor `port` references a Service port name (resolved via service -> pod).
// ServiceMonitor `targetPort` references a pod port directly.
func extractServiceMonitorEndpoints(spec map[string]interface{}, pod *corev1.Pod, svc *corev1.Service) ([]MetricsEndpoint, error) {
	raw, ok := spec["endpoints"].([]interface{})
	if !ok {
		return nil, nil
	}

	var endpoints []MetricsEndpoint
	for _, item := range raw {
		ep, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		path, _ := ep["path"].(string)
		if path == "" {
			path = "/metrics"
		}

		portNum, portName, err := resolveMonitorPort(ep, pod, svc)
		if err != nil {
			return nil, fmt.Errorf("resolving port: %w", err)
		}

		endpoints = append(endpoints, MetricsEndpoint{
			Port:    portName,
			PortNum: portNum,
			Path:    path,
		})
	}
	return endpoints, nil
}

// resolveMonitorPort resolves the pod port number from a monitor endpoint definition.
//
// Resolution order:
//  1. targetPort (int or string) -> resolve directly against pod
//  2. port (string) -> for ServiceMonitors, look up service port, follow targetPort to pod;
//     for PodMonitors (svc==nil), resolve directly against pod
//  3. Neither set + service available -> use service's first port -> follow targetPort to pod
func resolveMonitorPort(ep map[string]interface{}, pod *corev1.Pod, svc *corev1.Service) (int32, string, error) {
	// 1. Check targetPort (can be int64, float64, or string from unstructured JSON).
	if tp, ok := ep["targetPort"]; ok {
		switch v := tp.(type) {
		case int64:
			p := int32(v)
			return p, strconv.Itoa(int(p)), nil
		case float64:
			p := int32(v)
			return p, strconv.Itoa(int(p)), nil
		case string:
			p, err := resolvePortOnPod(pod, v)
			return p, v, err
		}
	}

	// 2. Check port (string).
	if portName, ok := ep["port"].(string); ok && portName != "" {
		if svc != nil {
			// ServiceMonitor: port names a service port.
			p, err := resolveServicePortToPod(svc, portName, pod)
			return p, portName, err
		}
		// PodMonitor: port names a pod container port directly.
		p, err := resolvePortOnPod(pod, portName)
		return p, portName, err
	}

	// 3. Neither set - fall back to service's first port if available.
	if svc != nil && len(svc.Spec.Ports) > 0 {
		sp := svc.Spec.Ports[0]
		p, err := resolveServiceTargetPort(sp, pod)
		name := sp.Name
		if name == "" {
			name = strconv.Itoa(int(sp.Port))
		}
		return p, name, err
	}

	return 0, "", fmt.Errorf("no port specified and no service available")
}

// resolveServicePortToPod looks up a service port by name, then resolves its targetPort on the pod.
func resolveServicePortToPod(svc *corev1.Service, portName string, pod *corev1.Pod) (int32, error) {
	for _, sp := range svc.Spec.Ports {
		if sp.Name == portName {
			return resolveServiceTargetPort(sp, pod)
		}
	}
	return 0, fmt.Errorf("service %s has no port named %q", svc.Name, portName)
}

// resolveServiceTargetPort resolves a service port's targetPort to a pod container port number.
func resolveServiceTargetPort(sp corev1.ServicePort, pod *corev1.Pod) (int32, error) {
	tp := sp.TargetPort
	if tp.IntValue() != 0 {
		return int32(tp.IntValue()), nil
	}
	if tp.String() != "" && tp.String() != "0" {
		return resolvePortOnPod(pod, tp.String())
	}
	// targetPort defaults to port if not set.
	return sp.Port, nil
}

// resolvePortOnPod resolves a port reference (name or number string) to a pod container port.
func resolvePortOnPod(pod *corev1.Pod, portRef string) (int32, error) {
	if num, err := strconv.ParseInt(portRef, 10, 32); err == nil {
		return int32(num), nil
	}

	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Name == portRef {
				return p.ContainerPort, nil
			}
		}
	}

	return 0, fmt.Errorf("port %q not found on pod %s", portRef, pod.Name)
}

// selectorFromUnstructured converts an unstructured label selector into labels.Selector.
func selectorFromUnstructured(raw interface{}) (labels.Selector, error) {
	if raw == nil {
		return labels.Everything(), nil
	}

	obj, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("selector is not an object")
	}

	ls := &metav1.LabelSelector{}

	if ml, ok := obj["matchLabels"].(map[string]interface{}); ok {
		ls.MatchLabels = make(map[string]string)
		for k, v := range ml {
			ls.MatchLabels[k] = fmt.Sprintf("%v", v)
		}
	}

	if me, ok := obj["matchExpressions"].([]interface{}); ok {
		for _, item := range me {
			expr, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			req := metav1.LabelSelectorRequirement{
				Key:      fmt.Sprintf("%v", expr["key"]),
				Operator: metav1.LabelSelectorOperator(fmt.Sprintf("%v", expr["operator"])),
			}
			if vals, ok := expr["values"].([]interface{}); ok {
				for _, v := range vals {
					req.Values = append(req.Values, fmt.Sprintf("%v", v))
				}
			}
			ls.MatchExpressions = append(ls.MatchExpressions, req)
		}
	}

	return metav1.LabelSelectorAsSelector(ls)
}

func verboseLog(verbose bool, w io.Writer, format string, args ...interface{}) {
	if verbose {
		fmt.Fprintf(w, format, args...)
	}
}
