package client

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ConfigOptions controls loading a kubeconfig, explicit ServiceAccount token,
// or in-cluster REST config.
type ConfigOptions struct {
	Kubeconfig              string
	Context                 string
	APIServer               string
	ServiceAccountToken     string
	ServiceAccountTokenFile string
	CAFile                  string
}

// LoadConfig loads an explicit kubeconfig first, then an explicit
// ServiceAccount token config, and otherwise attempts in-cluster configuration
// followed by the standard local kubeconfig.
func LoadConfig(opts ConfigOptions) (*rest.Config, error) {
	if opts.Kubeconfig != "" {
		return loadKubeconfig(opts.Kubeconfig, opts.Context)
	}

	if opts.APIServer != "" || opts.ServiceAccountToken != "" || opts.ServiceAccountTokenFile != "" || opts.CAFile != "" {
		return loadServiceAccountConfig(opts)
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

// loadServiceAccountConfig builds a REST config from a service account bearer
// token. This is useful for callers outside a Pod that have been given a
// token, the Kubernetes API address, and (optionally) the cluster CA file.
func loadServiceAccountConfig(opts ConfigOptions) (*rest.Config, error) {
	if strings.TrimSpace(opts.APIServer) == "" {
		return nil, fmt.Errorf("API server is required when using a service account token")
	}
	token := strings.TrimSpace(opts.ServiceAccountToken)
	if opts.ServiceAccountTokenFile != "" {
		contents, err := os.ReadFile(opts.ServiceAccountTokenFile)
		if err != nil {
			return nil, fmt.Errorf("read service account token %q: %w", opts.ServiceAccountTokenFile, err)
		}
		if token != "" && strings.TrimSpace(string(contents)) != token {
			return nil, fmt.Errorf("service account token and token file do not match")
		}
		token = strings.TrimSpace(string(contents))
	}
	if token == "" {
		return nil, fmt.Errorf("service account token cannot be empty")
	}
	return &rest.Config{
		Host:        strings.TrimRight(strings.TrimSpace(opts.APIServer), "/"),
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: opts.CAFile,
		},
	}, nil
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
