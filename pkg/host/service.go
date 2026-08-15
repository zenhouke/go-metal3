package host

import (
	"context"
	"encoding/json"
	"fmt"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalpatch "github.com/zenhouke/go-metal3/pkg/internal/patch"
	internalsecret "github.com/zenhouke/go-metal3/pkg/internal/secret"
	"github.com/zenhouke/go-metal3/pkg/operation"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

// Service implements the host inventory layer over BareMetalHost resources.
type Service struct {
	kube   ctrlclient.Client
	reader ctrlclient.Reader
}

func New(kube ctrlclient.Client, reader ctrlclient.Reader) *Service {
	if reader == nil {
		reader = kube
	}
	return &Service{kube: kube, reader: reader}
}

func (s *Service) Add(ctx context.Context, req metal3sdk.HostCreateRequest) (*metal3v1alpha1.BareMetalHost, error) {
	return s.create(ctx, "add-host", req, nil)
}

// Import creates the BMC Secret and BareMetalHost together with BMO's status
// reconstruction annotation. The annotation must be present on the first
// reconcile, so this cannot be implemented as Add followed by Update.
func (s *Service) Import(ctx context.Context, req metal3sdk.HostImportRequest) (*metal3v1alpha1.BareMetalHost, error) {
	if !detachableState(req.Status.Provisioning.State) {
		return nil, metal3sdk.ValidationError("import-host", "status provisioning state must be available, ready, provisioned, or externally provisioned")
	}
	if req.Status.OperationalStatus != metal3v1alpha1.OperationalStatusOK && req.Status.OperationalStatus != metal3v1alpha1.OperationalStatusDetached {
		return nil, metal3sdk.ValidationError("import-host", "status operationalStatus must be OK or detached")
	}
	if req.Status.ErrorType != "" {
		return nil, metal3sdk.ValidationError("import-host", "a host with an active BMO error cannot be imported")
	}
	if req.Status.Provisioning.Firmware != nil {
		return nil, metal3sdk.ValidationError("import-host", "status.provisioning.firmware is unsupported and must be omitted")
	}
	rawStatus, err := json.Marshal(req.Status)
	if err != nil {
		return nil, metal3sdk.ValidationError("import-host", fmt.Sprintf("status cannot be encoded: %v", err))
	}
	annotationBytes := len(metal3v1alpha1.StatusAnnotation) + len(rawStatus)
	for key, value := range req.Host.Annotations {
		annotationBytes += len(key) + len(value)
	}
	if annotationBytes > 256*1024 {
		return nil, metal3sdk.ValidationError("import-host", "status and host annotations exceed Kubernetes' 256 KiB annotation limit")
	}
	controlAnnotations := map[string]string{metal3v1alpha1.StatusAnnotation: string(rawStatus)}
	if req.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached {
		// A detached source host must remain unmanaged in the target cluster.
		// Without this annotation BMO immediately removes the reconstructed
		// detached status and registers the host in the target Ironic.
		controlAnnotations[metal3v1alpha1.DetachedAnnotation] = ""
	}
	return s.create(ctx, "import-host", req.Host, controlAnnotations)
}

func (s *Service) create(ctx context.Context, operationName string, req metal3sdk.HostCreateRequest, controlAnnotations map[string]string) (*metal3v1alpha1.BareMetalHost, error) {
	if err := validateCreate(operationName, req); err != nil {
		return nil, err
	}
	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}
	existing := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, existing); err == nil {
		return nil, &metal3sdk.Error{
			Code: metal3sdk.CodeConflict, Operation: operationName, Host: key,
			Message: "BareMetalHost already exists",
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, metal3sdk.KubernetesError(operationName, key, fmt.Errorf("check existing BareMetalHost: %w", err))
	}
	secretName := req.Name + "-bmc"
	if err := internalsecret.Upsert(ctx, s.kube, internalsecret.BMC(
		req.Namespace, secretName, req.BMCUsername, req.BMCPassword,
	)); err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, fmt.Errorf("apply BMC credentials: %w", err))
	}
	preprovisioningName := ""
	if len(req.PreprovisioningNetworkData) > 0 {
		ref, err := internalsecret.ContentAddressed(ctx, s.kube, req.Namespace, req.Name, "preprovisioning-networkdata", "networkData", req.PreprovisioningNetworkData, nil)
		if err != nil {
			return nil, metal3sdk.KubernetesError(operationName, key, fmt.Errorf("create preprovisioning network data: %w", err))
		}
		preprovisioningName = ref.Name
	}

	host := mapCreateRequest(req, secretName)
	host.Annotations = mergeMap(host.Annotations, controlAnnotations)
	host.Spec.PreprovisioningNetworkDataName = preprovisioningName
	if err := s.kube.Create(ctx, host); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, &metal3sdk.Error{
				Code: metal3sdk.CodeConflict, Operation: operationName,
				Host:    types.NamespacedName{Namespace: req.Namespace, Name: req.Name},
				Message: "BareMetalHost already exists", Cause: err,
			}
		}
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	return host, nil
}

func (s *Service) Get(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.BareMetalHost, error) {
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, host); err != nil {
		return nil, metal3sdk.KubernetesError("get-host", key, err)
	}
	return host, nil
}

func (s *Service) List(ctx context.Context, opts metal3sdk.HostListOptions) ([]metal3v1alpha1.BareMetalHost, error) {
	list := &metal3v1alpha1.BareMetalHostList{}
	listOpts := []ctrlclient.ListOption{ctrlclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(opts.Labels)}}
	if opts.Namespace != "" {
		listOpts = append(listOpts, ctrlclient.InNamespace(opts.Namespace))
	}
	if err := s.reader.List(ctx, list, listOpts...); err != nil {
		return nil, metal3sdk.KubernetesError("list-hosts", types.NamespacedName{Namespace: opts.Namespace}, err)
	}
	return list.Items, nil
}

// GetHardwareData returns the inspection data stored by BMO for a host.
// HardwareData uses the same namespace and name as its BareMetalHost.
func (s *Service) GetHardwareData(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.HardwareData, error) {
	data := &metal3v1alpha1.HardwareData{}
	if err := s.reader.Get(ctx, key, data); err != nil {
		return nil, metal3sdk.KubernetesError("get-hardware-data", key, err)
	}
	return data, nil
}

func (s *Service) Update(ctx context.Context, key types.NamespacedName, req metal3sdk.HostPatch) (*metal3v1alpha1.BareMetalHost, error) {
	if err := validateUserAnnotations("update-host", req.Annotations); err != nil {
		return nil, err
	}
	err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			host.Labels = mergeMap(host.Labels, req.Labels)
			host.Annotations = mergeMap(host.Annotations, req.Annotations)
			if req.Description != nil {
				host.Spec.Description = *req.Description
			}
			if req.AutomatedCleaningMode != nil {
				switch *req.AutomatedCleaningMode {
				case metal3v1alpha1.CleaningModeMetadata, metal3v1alpha1.CleaningModeDisabled:
				default:
					return metal3sdk.ValidationError("update-host", "automated cleaning mode must be metadata or disabled")
				}
				host.Spec.AutomatedCleaningMode = *req.AutomatedCleaningMode
			}
			return nil
		})
	if err != nil {
		return nil, metal3sdk.KubernetesError("update-host", key, err)
	}
	return s.Get(ctx, key)
}

// UpdateBMC follows the BMO detach-update-reattach workflow. If the host was
// already detached by another actor, this method preserves that state.
func (s *Service) UpdateBMC(ctx context.Context, key types.NamespacedName, req metal3sdk.BMCUpdateRequest) (*metal3sdk.Operation, error) {
	op := operation.New("update-bmc", key)
	if req.Address == "" && req.Username == "" && len(req.Password) == 0 {
		return op, metal3sdk.ValidationError("update-bmc", "address or credentials are required")
	}
	if req.Address != "" {
		if err := validateBMCAddress("update-bmc", req.Address); err != nil {
			return op, err
		}
	}
	if (req.Username == "") != (len(req.Password) == 0) {
		return op, metal3sdk.ValidationError("update-bmc", "BMC username and password must be provided together")
	}
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, host); err != nil {
		return op, metal3sdk.KubernetesError("update-bmc", key, err)
	}
	alreadyDetached := host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached
	registering := host.Status.Provisioning.State == metal3v1alpha1.StateRegistering
	detachedBySDK := false
	if !alreadyDetached && !registering {
		if !detachableState(host.Status.Provisioning.State) {
			return op, metal3sdk.InvalidStateError("update-bmc", key, "registering, detached, or a stable provisioning state", string(host.Status.Provisioning.State))
		}
		if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
			func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
			func(current *metal3v1alpha1.BareMetalHost) error {
				if current.Annotations == nil {
					current.Annotations = map[string]string{}
				}
				current.Annotations[metal3v1alpha1.DetachedAnnotation] = "go-metal3-sdk:update-bmc"
				return nil
			}); err != nil {
			return op, metal3sdk.KubernetesError("update-bmc", key, err)
		}
		detachedBySDK = true
		if _, err := operation.WaitForHost(ctx, s.reader, key, req.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
			return current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached, nil
		}); err != nil {
			operation.Fail(op, "BMC update detach failed")
			return op, err
		}
	}
	if req.Username != "" {
		secretName := host.Spec.BMC.CredentialsName
		if secretName == "" {
			return op, metal3sdk.ValidationError("update-bmc", "BareMetalHost has no BMC credentials Secret name")
		}
		if err := internalsecret.Upsert(ctx, s.kube, internalsecret.BMC(host.Namespace, secretName, req.Username, req.Password)); err != nil {
			return op, metal3sdk.KubernetesError("update-bmc", key, err)
		}
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if req.Address != "" {
				current.Spec.BMC.Address = req.Address
			}
			if detachedBySDK {
				delete(current.Annotations, metal3v1alpha1.DetachedAnnotation)
			}
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("update-bmc", key, err)
	}
	if alreadyDetached {
		operation.Succeed(op, "BMC update applied; host remains detached")
		return op, nil
	}
	if !req.Wait {
		op.Message = "BMC update submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, req.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		return current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK, nil
	}); err != nil {
		operation.Fail(op, "BMC update reattach failed")
		return op, err
	}
	operation.Succeed(op, "BMC update applied")
	return op, nil
}

// AdoptExternallyProvisioned marks an inspected Available host as already
// deployed by another system. BMO does not support reversing this transition.
func (s *Service) AdoptExternallyProvisioned(ctx context.Context, key types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("adopt-externally-provisioned", key)
	host, err := s.Get(ctx, key)
	if err != nil {
		return op, err
	}
	if host.Spec.ExternallyProvisioned && host.Status.Provisioning.State == metal3v1alpha1.StateExternallyProvisioned {
		operation.Succeed(op, "host is already externally provisioned")
		return op, nil
	}
	if host.Status.Provisioning.State != metal3v1alpha1.StateAvailable && host.Status.Provisioning.State != metal3v1alpha1.StateReady {
		return op, metal3sdk.InvalidStateError("adopt-externally-provisioned", key, "available or ready", string(host.Status.Provisioning.State))
	}
	if host.Spec.Image != nil || host.Spec.CustomDeploy != nil {
		return op, metal3sdk.InvalidStateError("adopt-externally-provisioned", key, "host without pending image or customDeploy", "deployment pending")
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if current.Status.Provisioning.State != metal3v1alpha1.StateAvailable && current.Status.Provisioning.State != metal3v1alpha1.StateReady {
				return metal3sdk.InvalidStateError("adopt-externally-provisioned", key, "available or ready", string(current.Status.Provisioning.State))
			}
			if current.Spec.Image != nil || current.Spec.CustomDeploy != nil {
				return metal3sdk.InvalidStateError("adopt-externally-provisioned", key, "host without pending image or customDeploy", "deployment pending")
			}
			current.Spec.ExternallyProvisioned = true
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("adopt-externally-provisioned", key, err)
	}
	if !opts.Wait {
		op.Message = "external provisioning adoption submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, opts.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		return current.Status.Provisioning.State == metal3v1alpha1.StateExternallyProvisioned && current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK, nil
	}); err != nil {
		operation.Fail(op, "external provisioning adoption failed")
		return op, err
	}
	operation.Succeed(op, "host adopted as externally provisioned")
	return op, nil
}

func (s *Service) Detach(ctx context.Context, key types.NamespacedName, opts metal3sdk.DetachOptions) (*metal3sdk.Operation, error) {
	op := operation.New("detach-host", key)
	host, err := s.Get(ctx, key)
	if err != nil {
		return op, err
	}
	if host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached {
		operation.Succeed(op, "host is already detached")
		return op, nil
	}
	if !opts.Force && !detachableState(host.Status.Provisioning.State) {
		return op, metal3sdk.InvalidStateError("detach-host", key, "available, ready, provisioned, or externally provisioned", string(host.Status.Provisioning.State))
	}
	value := ""
	if opts.Force {
		raw, err := json.Marshal(metal3v1alpha1.DetachedAnnotationArguments{Force: true})
		if err != nil {
			return op, fmt.Errorf("encode detach options: %w", err)
		}
		value = string(raw)
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[metal3v1alpha1.DetachedAnnotation] = value
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("detach-host", key, err)
	}
	if !opts.Wait {
		op.Message = "host detach submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, opts.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		return current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached, nil
	}); err != nil {
		operation.Fail(op, "host detach failed")
		return op, err
	}
	operation.Succeed(op, "host detached")
	return op, nil
}

func (s *Service) Attach(ctx context.Context, key types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("attach-host", key)
	host, err := s.Get(ctx, key)
	if err != nil {
		return op, err
	}
	if _, detached := host.Annotations[metal3v1alpha1.DetachedAnnotation]; !detached {
		if host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK {
			operation.Succeed(op, "host is already attached")
		} else {
			op.Message = "host is already under provisioner management"
		}
		return op, nil
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			delete(current.Annotations, metal3v1alpha1.DetachedAnnotation)
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("attach-host", key, err)
	}
	if !opts.Wait {
		op.Message = "host attach submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, opts.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		return current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK, nil
	}); err != nil {
		operation.Fail(op, "host attach failed")
		return op, err
	}
	operation.Succeed(op, "host attached")
	return op, nil
}

func (s *Service) Delete(ctx context.Context, key types.NamespacedName, opts metal3sdk.DeleteOptions) (*metal3sdk.Operation, error) {
	op := operation.New("delete-host", key)
	if opts.Mode != metal3sdk.DeleteAndDeprovision && opts.Mode != metal3sdk.DeleteInventoryOnly {
		return op, metal3sdk.ValidationError("delete-host", "delete mode must be Deprovision or InventoryOnly")
	}
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.kube.Get(ctx, key, host); err != nil {
		if apierrors.IsNotFound(err) {
			operation.Succeed(op, "host already deleted")
			return op, nil
		}
		return op, metal3sdk.KubernetesError("delete-host", key, err)
	}
	if host.Spec.ConsumerRef != nil && !opts.Force {
		return op, metal3sdk.InvalidStateError("delete-host", key, "host without consumerRef", "in use")
	}
	secretRefs := referencedSecrets(host)
	if opts.Mode == metal3sdk.DeleteInventoryOnly && host.Status.OperationalStatus != metal3v1alpha1.OperationalStatusDetached {
		if !detachableState(host.Status.Provisioning.State) {
			return op, metal3sdk.InvalidStateError("delete-host", key, "available, provisioned, or externally provisioned", string(host.Status.Provisioning.State))
		}
		if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
			func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
			func(current *metal3v1alpha1.BareMetalHost) error {
				if current.Annotations == nil {
					current.Annotations = map[string]string{}
				}
				current.Annotations[metal3v1alpha1.DetachedAnnotation] = ""
				return nil
			}); err != nil {
			return op, metal3sdk.KubernetesError("delete-host", key, fmt.Errorf("detach BareMetalHost: %w", err))
		}
		if _, err := operation.WaitForHost(ctx, s.reader, key, opts.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
			return current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached, nil
		}); err != nil {
			operation.Fail(op, "host detach failed")
			return op, err
		}
	}
	if err := s.kube.Delete(ctx, host); err != nil && !apierrors.IsNotFound(err) {
		return op, metal3sdk.KubernetesError("delete-host", key, err)
	}
	if opts.Wait || opts.DeleteOwnedSecrets {
		if err := operation.WaitForDeletion(ctx, s.reader, key, opts.Timeout); err != nil {
			operation.Fail(op, "host deletion failed")
			return op, err
		}
	}
	if opts.DeleteOwnedSecrets {
		for _, ref := range secretRefs {
			if err := internalsecret.DeleteIfManaged(ctx, s.kube, ref); err != nil {
				return op, metal3sdk.KubernetesError("delete-host-secrets", key, fmt.Errorf("delete SDK-managed Secret %s/%s: %w", ref.Namespace, ref.Name, err))
			}
		}
	}
	if opts.Wait || opts.DeleteOwnedSecrets {
		operation.Succeed(op, "BareMetalHost deleted")
	} else {
		op.Message = "BareMetalHost deletion submitted"
	}
	return op, nil
}

func detachableState(state metal3v1alpha1.ProvisioningState) bool {
	switch state {
	case metal3v1alpha1.StateAvailable, metal3v1alpha1.StateReady,
		metal3v1alpha1.StateProvisioned, metal3v1alpha1.StateExternallyProvisioned:
		return true
	default:
		return false
	}
}

func referencedSecrets(host *metal3v1alpha1.BareMetalHost) []corev1.SecretReference {
	seen := map[types.NamespacedName]struct{}{}
	refs := make([]corev1.SecretReference, 0, 4)
	add := func(ref corev1.SecretReference) {
		if ref.Name == "" {
			return
		}
		if ref.Namespace == "" {
			ref.Namespace = host.Namespace
		}
		key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}
	add(corev1.SecretReference{Name: host.Spec.BMC.CredentialsName, Namespace: host.Namespace})
	if host.Spec.UserData != nil {
		add(*host.Spec.UserData)
	}
	if host.Spec.NetworkData != nil {
		add(*host.Spec.NetworkData)
	}
	if host.Spec.MetaData != nil {
		add(*host.Spec.MetaData)
	}
	if host.Spec.PreprovisioningNetworkDataName != "" {
		add(corev1.SecretReference{Name: host.Spec.PreprovisioningNetworkDataName, Namespace: host.Namespace})
	}
	if host.Spec.Image != nil && host.Spec.Image.OCIAuthSecretName != nil {
		add(corev1.SecretReference{Name: *host.Spec.Image.OCIAuthSecretName, Namespace: host.Namespace})
	}
	return refs
}

func (s *Service) WaitForPhase(ctx context.Context, key types.NamespacedName, phase metal3sdk.HostPhase) (*metal3v1alpha1.BareMetalHost, error) {
	return operation.WaitForHost(ctx, s.reader, key, 0, func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		return PhaseOf(host) == phase, nil
	})
}

// PhaseOf maps BMO status into the stable SDK phase model.
func PhaseOf(host *metal3v1alpha1.BareMetalHost) metal3sdk.HostPhase {
	if host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusDetached {
		return metal3sdk.HostDetached
	}
	if host.Status.ErrorType != "" || host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusError {
		return metal3sdk.HostError
	}
	if host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusServicing {
		return metal3sdk.HostServicing
	}
	switch host.Status.Provisioning.State {
	case metal3v1alpha1.StateUnmanaged:
		return metal3sdk.HostUnmanaged
	case metal3v1alpha1.StateRegistering:
		return metal3sdk.HostRegistering
	case metal3v1alpha1.StateMatchProfile:
		return metal3sdk.HostMatchingProfile
	case metal3v1alpha1.StateInspecting:
		return metal3sdk.HostInspecting
	case metal3v1alpha1.StatePreparing:
		return metal3sdk.HostPreparing
	case metal3v1alpha1.StateAvailable, metal3v1alpha1.StateReady:
		return metal3sdk.HostAvailable
	case metal3v1alpha1.StateProvisioning:
		return metal3sdk.HostProvisioning
	case metal3v1alpha1.StateProvisioned:
		return metal3sdk.HostProvisioned
	case metal3v1alpha1.StateExternallyProvisioned:
		return metal3sdk.HostExternallyProvisioned
	case metal3v1alpha1.StateDeprovisioning:
		return metal3sdk.HostDeprovisioning
	case metal3v1alpha1.StatePoweringOffBeforeDelete, metal3v1alpha1.StateDeleting:
		return metal3sdk.HostDeleting
	default:
		return metal3sdk.HostUnknown
	}
}

func mergeMap(base, extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = map[string]string{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}
