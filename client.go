package metal3sdk

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	clientinfra "github.com/zenhouke/go-metal3/pkg/client"
	clustersvc "github.com/zenhouke/go-metal3/pkg/cluster"
	hostsvc "github.com/zenhouke/go-metal3/pkg/host"
	maintenancesvc "github.com/zenhouke/go-metal3/pkg/maintenance"
	powersvc "github.com/zenhouke/go-metal3/pkg/power"
	provisionsvc "github.com/zenhouke/go-metal3/pkg/provision"
	resourcessvc "github.com/zenhouke/go-metal3/pkg/resources"
)

// Options configures SDK infrastructure and optional providers.
type Options struct {
	Config     *rest.Config
	Reader     ctrlclient.Reader
	QPS        float32
	Burst      int
	FieldOwner string
	UserAgent  string
	Logger     logr.Logger
}

// Client is the default SDK composition root.
type Client struct {
	infrastructure *clientinfra.Client
	cluster        ClusterService
	hosts          HostService
	power          PowerService
	provisioning   ProvisioningService
	maintenance    MaintenanceService
	resources      ResourceService
	logger         logr.Logger
}

// New constructs all domain services over one Kubernetes infrastructure client.
func New(opts Options) (*Client, error) {
	infra, err := clientinfra.New(clientinfra.Options{
		Config: opts.Config, Reader: opts.Reader, QPS: opts.QPS, Burst: opts.Burst,
		FieldOwner: opts.FieldOwner, UserAgent: opts.UserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Metal3 SDK: %w", err)
	}
	logger := opts.Logger
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}
	return &Client{
		infrastructure: infra,
		cluster:        clustersvc.New(infra.Discovery),
		hosts:          hostsvc.New(infra.Kube, infra.Reader),
		power:          powersvc.New(infra.Kube, infra.Reader),
		provisioning:   provisionsvc.New(infra.Kube, infra.Reader),
		maintenance:    maintenancesvc.New(infra.Kube, infra.Reader),
		resources:      resourcessvc.New(infra.Kube, infra.Reader),
		logger:         logger,
	}, nil
}

func (c *Client) Cluster() ClusterService           { return c.cluster }
func (c *Client) Hosts() HostService                { return c.hosts }
func (c *Client) Power() PowerService               { return c.power }
func (c *Client) Provisioning() ProvisioningService { return c.provisioning }
func (c *Client) Maintenance() MaintenanceService   { return c.maintenance }
func (c *Client) Resources() ResourceService        { return c.resources }

var _ SDK = (*Client)(nil)
