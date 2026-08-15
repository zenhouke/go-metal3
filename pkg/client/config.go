package client

import (
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ConfigOptions controls loading an in-cluster or kubeconfig REST config.
type ConfigOptions struct {
	Kubeconfig string
	Context    string
}

// LoadConfig loads an explicit kubeconfig first and otherwise attempts
// in-cluster configuration followed by the standard local kubeconfig.
func LoadConfig(opts ConfigOptions) (*rest.Config, error) {
	if opts.Kubeconfig != "" {
		return loadKubeconfig(opts.Kubeconfig, opts.Context)
	}

	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if envPath := os.Getenv(clientcmd.RecommendedConfigPathEnvVar); envPath != "" {
		rules.ExplicitPath = envPath
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: opts.Context}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	return cfg, nil
}

func loadKubeconfig(path, contextName string) (*rest.Config, error) {
	rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q: %w", path, err)
	}
	return cfg, nil
}
