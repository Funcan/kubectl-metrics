package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"kubectl-metrics/pkg/discover"
	naturalsort "kubectl-metrics/pkg/sort"
)

// Endpoint port-forwards to the given pod endpoint, scrapes Prometheus metrics, and displays them.
func Endpoint(ctx context.Context, restConfig *rest.Config, clientset kubernetes.Interface, namespace string, pod *corev1.Pod, endpoint discover.MetricsEndpoint, showValues bool, verbose bool, streams genericiooptions.IOStreams) error {
	verboseLog(verbose, streams.ErrOut, "Port-forwarding to pod %s port %d...\n", pod.Name, endpoint.PortNum)

	reqURL := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(namespace).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return fmt.Errorf("creating SPDY round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, reqURL)

	stopChan := make(chan struct{})
	readyChan := make(chan struct{})
	errChan := make(chan error, 1)

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", endpoint.PortNum)}, stopChan, readyChan, io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("creating port forwarder: %w", err)
	}

	go func() {
		errChan <- fw.ForwardPorts()
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		return fmt.Errorf("port-forward failed: %w", err)
	}
	defer close(stopChan)

	forwardedPorts, err := fw.GetPorts()
	if err != nil {
		return fmt.Errorf("getting forwarded ports: %w", err)
	}
	localPort := forwardedPorts[0].Local

	metricsURL := fmt.Sprintf("http://localhost:%d%s", localPort, endpoint.Path)
	verboseLog(verbose, streams.ErrOut, "Scraping metrics from %s...\n", metricsURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("metrics endpoint returned %s", resp.Status)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return fmt.Errorf("parsing metrics: %w", err)
	}

	displayMetrics(streams.Out, endpoint, families, showValues)
	return nil
}

func displayMetrics(out io.Writer, endpoint discover.MetricsEndpoint, families map[string]*dto.MetricFamily, showValues bool) {
	fmt.Fprintf(out, "\n%s (port: %s, path: %s)\n", endpoint.Source, endpoint.Port, endpoint.Path)

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return naturalsort.NaturalLess(names[i], names[j])
	})

	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)

	if showValues {
		fmt.Fprintln(tw, "NAME\tTYPE\tVALUE")
		for _, name := range names {
			fam := families[name]
			typeName := strings.ToLower(fam.GetType().String())
			for _, m := range fam.GetMetric() {
				labelStr := formatLabels(m.GetLabel())
				fullName := name + labelStr
				value := extractValue(fam.GetType(), m)
				fmt.Fprintf(tw, "%s\t%s\t%s\n", fullName, typeName, value)
			}
		}
	} else {
		fmt.Fprintln(tw, "NAME\tTYPE")
		for _, name := range names {
			fam := families[name]
			typeName := strings.ToLower(fam.GetType().String())
			fmt.Fprintf(tw, "%s\t%s\n", name, typeName)
		}
	}

	tw.Flush()
}

func formatLabels(pairs []*dto.LabelPair) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, len(pairs))
	for i, lp := range pairs {
		parts[i] = fmt.Sprintf("%s=%q", lp.GetName(), lp.GetValue())
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func extractValue(t dto.MetricType, m *dto.Metric) string {
	switch t {
	case dto.MetricType_COUNTER:
		return strconv.FormatFloat(m.GetCounter().GetValue(), 'f', -1, 64)
	case dto.MetricType_GAUGE:
		return strconv.FormatFloat(m.GetGauge().GetValue(), 'f', -1, 64)
	case dto.MetricType_UNTYPED:
		return strconv.FormatFloat(m.GetUntyped().GetValue(), 'f', -1, 64)
	case dto.MetricType_SUMMARY:
		return strconv.FormatFloat(m.GetSummary().GetSampleSum(), 'f', -1, 64)
	case dto.MetricType_HISTOGRAM:
		return strconv.FormatFloat(m.GetHistogram().GetSampleSum(), 'f', -1, 64)
	default:
		return "?"
	}
}

func verboseLog(verbose bool, w io.Writer, format string, args ...interface{}) {
	if verbose {
		fmt.Fprintf(w, format, args...)
	}
}
