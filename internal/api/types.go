// Package api defines the transport-neutral contracts shared by the public
// SDK facade and its domain service implementations.
package api

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

// SDK exposes the domain services implemented by the Metal3 SDK.
type SDK interface {
	Cluster() ClusterService
	Hosts() HostService
	Power() PowerService
	Provisioning() ProvisioningService
	Maintenance() MaintenanceService
	Resources() ResourceService
}

// ClusterService discovers the Kubernetes and Metal3 APIs available to the SDK.
type ClusterService interface {
	Info(context.Context) (*ClusterInfo, error)
}

// APIResourceInfo describes one discovered Kubernetes API resource.
type APIResourceInfo struct {
	Available  bool     `json:"available"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs,omitempty"`
}

// ClusterInfo is a read-only compatibility snapshot from Kubernetes discovery.
type ClusterInfo struct {
	KubernetesVersion      string          `json:"kubernetesVersion"`
	Metal3APIVersion       string          `json:"metal3APIVersion"`
	BareMetalHosts         APIResourceInfo `json:"bareMetalHosts"`
	BMCEventSubscriptions  APIResourceInfo `json:"bmcEventSubscriptions"`
	DataImages             APIResourceInfo `json:"dataImages"`
	FirmwareSchemas        APIResourceInfo `json:"firmwareSchemas"`
	HardwareData           APIResourceInfo `json:"hardwareData"`
	HostClaims             APIResourceInfo `json:"hostClaims"`
	HostDeployPolicies     APIResourceInfo `json:"hostDeployPolicies"`
	HostFirmwareComponents APIResourceInfo `json:"hostFirmwareComponents"`
	HostFirmwareSettings   APIResourceInfo `json:"hostFirmwareSettings"`
	HostUpdatePolicies     APIResourceInfo `json:"hostUpdatePolicies"`
	PreprovisioningImages  APIResourceInfo `json:"preprovisioningImages"`
}

// HostService manages BareMetalHost inventory and lifecycle records.
type HostService interface {
	Add(context.Context, HostCreateRequest) (*metal3v1alpha1.BareMetalHost, error)
	Import(context.Context, HostImportRequest) (*metal3v1alpha1.BareMetalHost, error)
	Get(context.Context, types.NamespacedName) (*metal3v1alpha1.BareMetalHost, error)
	List(context.Context, HostListOptions) ([]metal3v1alpha1.BareMetalHost, error)
	Update(context.Context, types.NamespacedName, HostPatch) (*metal3v1alpha1.BareMetalHost, error)
	UpdateBMC(context.Context, types.NamespacedName, BMCUpdateRequest) (*Operation, error)
	AdoptExternallyProvisioned(context.Context, types.NamespacedName, WaitOptions) (*Operation, error)
	Detach(context.Context, types.NamespacedName, DetachOptions) (*Operation, error)
	Attach(context.Context, types.NamespacedName, WaitOptions) (*Operation, error)
	Delete(context.Context, types.NamespacedName, DeleteOptions) (*Operation, error)
	WaitForPhase(context.Context, types.NamespacedName, HostPhase) (*metal3v1alpha1.BareMetalHost, error)
	GetHardwareData(context.Context, types.NamespacedName) (*metal3v1alpha1.HardwareData, error)
}

// PowerService manages the desired and observed power state of a host.
type PowerService interface {
	PowerOn(context.Context, types.NamespacedName, WaitOptions) (*Operation, error)
	PowerOff(context.Context, types.NamespacedName, WaitOptions) (*Operation, error)
	Reboot(context.Context, types.NamespacedName, RebootOptions) (*Operation, error)
	StartPhasedReboot(context.Context, types.NamespacedName, PhasedRebootOptions) (*Operation, error)
	CompletePhasedReboot(context.Context, types.NamespacedName, string, WaitOptions) (*Operation, error)
}

// ProvisioningService manages operating-system deployment workflows.
type ProvisioningService interface {
	Install(context.Context, types.NamespacedName, InstallRequest) (*Operation, error)
	CustomDeploy(context.Context, types.NamespacedName, CustomDeployRequest) (*Operation, error)
	Reinstall(context.Context, types.NamespacedName, InstallRequest) (*Operation, error)
	Deprovision(context.Context, types.NamespacedName, WaitOptions) (*Operation, error)
}

// MaintenanceService manages inspection and host preparation operations.
type MaintenanceService interface {
	Inspect(context.Context, types.NamespacedName, WaitOptions) (*Operation, error)
	SetExternalInspectionData(context.Context, types.NamespacedName, *metal3v1alpha1.HardwareDetails, WaitOptions) (*Operation, error)
	SetInspectionMode(context.Context, types.NamespacedName, InspectionMode) (*metal3v1alpha1.BareMetalHost, error)
	ConfigureRAID(context.Context, types.NamespacedName, *metal3v1alpha1.RAIDConfig, WaitOptions) (*Operation, error)
	GetFirmwareSettings(context.Context, types.NamespacedName) (*metal3v1alpha1.HostFirmwareSettings, error)
	GetFirmwareComponents(context.Context, types.NamespacedName) (*metal3v1alpha1.HostFirmwareComponents, error)
	GetFirmwareSchema(context.Context, types.NamespacedName) (*metal3v1alpha1.FirmwareSchema, error)
	GetPreprovisioningImage(context.Context, types.NamespacedName) (*metal3v1alpha1.PreprovisioningImage, error)
	UpdateFirmwareSettings(context.Context, types.NamespacedName, metal3v1alpha1.DesiredSettingsMap, WaitOptions) (*Operation, error)
	UpdateFirmwareComponents(context.Context, types.NamespacedName, []metal3v1alpha1.FirmwareUpdate, WaitOptions) (*Operation, error)
	SetPreprovisioningNetworkData(context.Context, types.NamespacedName, []byte) (*metal3v1alpha1.BareMetalHost, error)
}

// ResourceService exposes the remaining first-class BMO v0.13 resources that
// are not tied to one imperative host lifecycle operation.
type ResourceService interface {
	CreateBMCEventSubscription(context.Context, BMCEventSubscriptionCreateRequest) (*metal3v1alpha1.BMCEventSubscription, error)
	GetBMCEventSubscription(context.Context, types.NamespacedName) (*metal3v1alpha1.BMCEventSubscription, error)
	ListBMCEventSubscriptions(context.Context, ResourceListOptions) ([]metal3v1alpha1.BMCEventSubscription, error)
	DeleteBMCEventSubscription(context.Context, types.NamespacedName) error

	ApplyDataImage(context.Context, DataImageApplyRequest) (*metal3v1alpha1.DataImage, error)
	GetDataImage(context.Context, types.NamespacedName) (*metal3v1alpha1.DataImage, error)
	ListDataImages(context.Context, ResourceListOptions) ([]metal3v1alpha1.DataImage, error)
	DeleteDataImage(context.Context, types.NamespacedName) error

	CreateHostClaim(context.Context, HostClaimCreateRequest) (*metal3v1alpha1.HostClaim, error)
	GetHostClaim(context.Context, types.NamespacedName) (*metal3v1alpha1.HostClaim, error)
	ListHostClaims(context.Context, ResourceListOptions) ([]metal3v1alpha1.HostClaim, error)
	DeleteHostClaim(context.Context, types.NamespacedName) error

	ApplyHostDeployPolicy(context.Context, HostDeployPolicyApplyRequest) (*metal3v1alpha1.HostDeployPolicy, error)
	GetHostDeployPolicy(context.Context, types.NamespacedName) (*metal3v1alpha1.HostDeployPolicy, error)
	ListHostDeployPolicies(context.Context, ResourceListOptions) ([]metal3v1alpha1.HostDeployPolicy, error)
	DeleteHostDeployPolicy(context.Context, types.NamespacedName) error

	ApplyHostUpdatePolicy(context.Context, HostUpdatePolicyApplyRequest) (*metal3v1alpha1.HostUpdatePolicy, error)
	GetHostUpdatePolicy(context.Context, types.NamespacedName) (*metal3v1alpha1.HostUpdatePolicy, error)
	ListHostUpdatePolicies(context.Context, ResourceListOptions) ([]metal3v1alpha1.HostUpdatePolicy, error)
	DeleteHostUpdatePolicy(context.Context, types.NamespacedName) error
}

type ResourceListOptions struct {
	Namespace string
	Labels    map[string]string
}

type BMCEventSubscriptionCreateRequest struct {
	Namespace      string
	Name           string
	Labels         map[string]string
	Annotations    map[string]string
	HostName       string
	Destination    string
	Context        string
	HTTPHeadersRef *corev1.SecretReference
}

type DataImageApplyRequest struct {
	Namespace   string
	Name        string
	Labels      map[string]string
	Annotations map[string]string
	URL         string
}

type HostClaimCreateRequest struct {
	Namespace   string
	Name        string
	Labels      map[string]string
	Annotations map[string]string
	Spec        metal3v1alpha1.HostClaimSpec
}

type HostDeployPolicyApplyRequest struct {
	Namespace   string
	Name        string
	Labels      map[string]string
	Annotations map[string]string
	Spec        metal3v1alpha1.HostDeployPolicySpec
}

type HostUpdatePolicyApplyRequest struct {
	Namespace   string
	Name        string
	Labels      map[string]string
	Annotations map[string]string
	Spec        metal3v1alpha1.HostUpdatePolicySpec
}

// HostCreateRequest contains the minimum information needed to enroll a host.
type HostCreateRequest struct {
	Namespace                    string
	Name                         string
	Labels                       map[string]string
	Annotations                  map[string]string
	Taints                       []corev1.Taint
	BMCAddress                   string
	BMCUsername                  string
	BMCPassword                  []byte
	BootMACAddress               string
	BootMode                     metal3v1alpha1.BootMode
	DisableCertificateValidation bool
	Online                       bool
	Description                  string
	Architecture                 string
	RootDeviceHints              *metal3v1alpha1.RootDeviceHints
	ConsumerRef                  *corev1.ObjectReference
	DisablePowerOff              bool
	InspectionDisabled           bool
	ExternallyProvisioned        bool
	AutomatedCleaningMode        metal3v1alpha1.AutomatedCleaningMode
	PreprovisioningNetworkData   []byte
}

// HostImportRequest atomically creates a BareMetalHost with a BMO status
// reconstruction annotation. It is intended only for moving an existing host
// between BMO clusters without inspecting or reprovisioning it again.
type HostImportRequest struct {
	Host   HostCreateRequest
	Status metal3v1alpha1.BareMetalHostStatus
}

// HostListOptions controls namespace and label filtering.
type HostListOptions struct {
	Namespace string
	Labels    map[string]string
}

// HostPatch contains fields that are safe to change through the generic host API.
type HostPatch struct {
	Labels                map[string]string
	Annotations           map[string]string
	Description           *string
	AutomatedCleaningMode *metal3v1alpha1.AutomatedCleaningMode
}

// InspectionMode controls automatic inspection during enrollment.
type InspectionMode string

const (
	InspectionAutomatic InspectionMode = "automatic"
	InspectionDisabled  InspectionMode = "disabled"
)

// BMCUpdateRequest safely changes a BMC address and/or rotates credentials.
type BMCUpdateRequest struct {
	Address  string
	Username string
	Password []byte
	Wait     bool
	Timeout  time.Duration
}

type DetachOptions struct {
	Force   bool
	Wait    bool
	Timeout time.Duration
}

// DeleteMode makes destructive deletion semantics explicit.
type DeleteMode string

const (
	DeleteAndDeprovision DeleteMode = "Deprovision"
	DeleteInventoryOnly  DeleteMode = "InventoryOnly"
)

// DeleteOptions controls deletion behavior.
type DeleteOptions struct {
	Mode               DeleteMode
	Wait               bool
	Timeout            time.Duration
	DeleteOwnedSecrets bool
	Force              bool
}

// WaitOptions controls an asynchronous state wait.
type WaitOptions struct {
	Wait    bool
	Timeout time.Duration
}

// RebootMode selects how BMO should reboot a host.
type RebootMode string

const (
	RebootAuto RebootMode = ""
	RebootSoft RebootMode = "soft"
	RebootHard RebootMode = "hard"
)

// RebootOptions controls reboot behavior.
type RebootOptions struct {
	Mode    RebootMode
	Force   bool
	Wait    bool
	Timeout time.Duration
}

// PhasedRebootOptions starts a reboot that remains powered off until the
// caller completes it with the returned Operation ID.
type PhasedRebootOptions struct {
	Mode    RebootMode
	Force   bool
	Wait    bool
	Timeout time.Duration
}

// InstallRequest is the desired operating-system deployment.
type InstallRequest struct {
	ImageURL          string
	Checksum          string
	ChecksumType      metal3v1alpha1.ChecksumType
	DiskFormat        *string
	OCIAuthSecretName string
	RootDeviceHints   *metal3v1alpha1.RootDeviceHints
	UserData          []byte
	NetworkData       []byte
	MetaData          []byte
	PowerOn           bool
	Wait              bool
	Timeout           time.Duration
}

type CustomDeployRequest struct {
	Method          string
	RootDeviceHints *metal3v1alpha1.RootDeviceHints
	UserData        []byte
	NetworkData     []byte
	MetaData        []byte
	PowerOn         bool
	Wait            bool
	Timeout         time.Duration
}

// HostPhase is a stable SDK-level view over BMO provisioning states.
type HostPhase string

const (
	HostRegistering           HostPhase = "Registering"
	HostMatchingProfile       HostPhase = "MatchingProfile"
	HostUnmanaged             HostPhase = "Unmanaged"
	HostInspecting            HostPhase = "Inspecting"
	HostPreparing             HostPhase = "Preparing"
	HostAvailable             HostPhase = "Available"
	HostProvisioning          HostPhase = "Provisioning"
	HostProvisioned           HostPhase = "Provisioned"
	HostServicing             HostPhase = "Servicing"
	HostExternallyProvisioned HostPhase = "ExternallyProvisioned"
	HostDeprovisioning        HostPhase = "Deprovisioning"
	HostDeleting              HostPhase = "Deleting"
	HostError                 HostPhase = "Error"
	HostDetached              HostPhase = "Detached"
	HostUnknown               HostPhase = "Unknown"
)

// OperationPhase is the lifecycle of a submitted SDK operation.
type OperationPhase string

const (
	OperationPending   OperationPhase = "Pending"
	OperationRunning   OperationPhase = "Running"
	OperationSucceeded OperationPhase = "Succeeded"
	OperationFailed    OperationPhase = "Failed"
	OperationCancelled OperationPhase = "Cancelled"
)

// Operation identifies an asynchronous workflow.
type Operation struct {
	ID         string               `json:"id"`
	Kind       string               `json:"kind"`
	Host       types.NamespacedName `json:"host"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt *time.Time           `json:"finishedAt,omitempty"`
	Phase      OperationPhase       `json:"phase"`
	Message    string               `json:"message,omitempty"`
}
