package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"kubectl-metrics/pkg/discover"
	"kubectl-metrics/pkg/scrape"
)

// Options holds the configuration for the plugin command.
type Options struct {
	ConfigFlags *genericclioptions.ConfigFlags
	Verbose         bool
	ShowValues      bool
	ShowDescription bool
	Streams         genericiooptions.IOStreams
}

// NewCmd creates the cobra command for kubectl-metrics.
func NewCmd(streams genericiooptions.IOStreams, version string) *cobra.Command {
	o := &Options{
		ConfigFlags: genericclioptions.NewConfigFlags(true),
		Streams:     streams,
	}

	cmd := &cobra.Command{
		Use:     "kubectl-metrics POD",
		Short:   "A kubectl plugin to display prometheus metrics exported by a pod",
		Version: version,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.Run(cmd.Context(), args[0])
		},
	}

	o.ConfigFlags.AddFlags(cmd.Flags())
	cmd.Flags().BoolVar(&o.Verbose, "verbose", false, "Show detailed discovery and connection info")
	cmd.Flags().BoolVar(&o.ShowValues, "show-values", false, "Show metric values in addition to names and types")
	cmd.Flags().BoolVar(&o.ShowDescription, "show-description", false, "Show metric help text as a description column")

	return cmd
}

// Run executes the main plugin logic for the given pod name.
func (o *Options) Run(ctx context.Context, podName string) error {
	restConfig, err := o.ConfigFlags.ToRESTConfig()
	if err != nil {
		return err
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	ns, err := o.Namespace()
	if err != nil {
		return err
	}

	o.verbose("Namespace: %s\n", ns)

	// Get the target pod.
	pod, err := clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting pod %s: %w", podName, err)
	}

	o.verbose("Pod %s has labels: %v\n", podName, pod.Labels)

	endpoints, err := discover.Endpoints(ctx, dynClient, clientset, ns, pod, o.Verbose, o.Streams.ErrOut)
	if err != nil {
		return err
	}

	if len(endpoints) == 0 {
		fmt.Fprintf(o.Streams.Out, "No ServiceMonitors or PodMonitors found matching pod %s in namespace %s\n", podName, ns)
		return nil
	}

	o.verbose("Found %d endpoint(s) to scrape\n", len(endpoints))

	for _, ep := range endpoints {
		if err := scrape.Endpoint(ctx, restConfig, clientset, ns, pod, ep, o.ShowValues, o.ShowDescription, o.Verbose, o.Streams); err != nil {
			fmt.Fprintf(o.Streams.ErrOut, "Error scraping %s (port %s, path %s): %v\n", ep.Source, ep.Port, ep.Path, err)
		}
	}

	return nil
}

func (o *Options) verbose(format string, args ...interface{}) {
	if o.Verbose {
		fmt.Fprintf(o.Streams.ErrOut, format, args...)
	}
}

// Namespace returns the resolved namespace.
func (o *Options) Namespace() (string, error) {
	ns, _, err := o.ConfigFlags.ToRawKubeConfigLoader().Namespace()
	return ns, err
}
