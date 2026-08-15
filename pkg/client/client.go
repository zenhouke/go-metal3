package client

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

const (
	DefaultQPS        float32 = 20
	DefaultBurst              = 40
	DefaultFieldOwner         = "go-metal3-sdk"
	DefaultUserAgent          = "go-metal3-sdk/dev"
)

// Options configures the Kubernetes infrastructure client.
type Options struct {
	Config     *rest.Config
	Scheme     *runtime.Scheme
	Reader     ctrlclient.Reader
	QPS        float32
	Burst      int
	FieldOwner string
	UserAgent  string
}

// Client groups the direct Kubernetes client, an optional strong-consistency
// reader, discovery, and field ownership information used by domain services.
type Client struct {
	Kube       ctrlclient.Client
	Reader     ctrlclient.Reader
	Discovery  discovery.DiscoveryInterface
	Scheme     *runtime.Scheme
	FieldOwner string
}

// New creates a Kubernetes client and registers all types used by the SDK.
func New(opts Options) (*Client, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("kubernetes REST config is required")
	}

	scheme := opts.Scheme
	if scheme == nil {
		scheme = runtime.NewScheme()
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register core API scheme: %w", err)
	}
	if err := metal3v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Metal3 API scheme: %w", err)
	}

	cfg := rest.CopyConfig(opts.Config)
	if opts.QPS <= 0 {
		opts.QPS = DefaultQPS
	}
	if opts.Burst <= 0 {
		opts.Burst = DefaultBurst
	}
	if opts.FieldOwner == "" {
		opts.FieldOwner = DefaultFieldOwner
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}
	cfg.QPS = opts.QPS
	cfg.Burst = opts.Burst
	cfg.UserAgent = opts.UserAgent

	kube, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create controller-runtime client: %w", err)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create discovery client: %w", err)
	}

	reader := opts.Reader
	if reader == nil {
		reader = kube
	}

	return &Client{
		Kube:       kube,
		Reader:     reader,
		Discovery:  discoveryClient,
		Scheme:     scheme,
		FieldOwner: opts.FieldOwner,
	}, nil
}
