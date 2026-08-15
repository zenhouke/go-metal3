package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
	"time"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalpatch "github.com/zenhouke/go-metal3/pkg/internal/patch"
	internalsecret "github.com/zenhouke/go-metal3/pkg/internal/secret"
	"github.com/zenhouke/go-metal3/pkg/operation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

const defaultMaintenanceTimeout = 30 * time.Minute

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

func (s *Service) Inspect(ctx context.Context, key types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("inspect", key)
	host, err := s.availableHost(ctx, key, "inspect")
	if err != nil {
		return op, err
	}
	if host.InspectionDisabled() {
		return op, metal3sdk.InvalidStateError("inspect", key, "inspection enabled", "disabled")
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[metal3v1alpha1.InspectAnnotationPrefix] = ""
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("inspect", key, err)
	}
	if !opts.Wait {
		op.Message = "inspection submitted"
		return op, nil
	}
	_, err = operation.WaitForHost(ctx, s.reader, key, maintenanceTimeout(opts.Timeout), func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		_, pending := current.Annotations[metal3v1alpha1.InspectAnnotationPrefix]
		ended := current.Status.OperationHistory.Inspect.End
		completed := !ended.IsZero() && !ended.Time.Before(op.StartedAt)
		return !pending && completed && isAvailable(current), nil
	})
	if err != nil {
		operation.Fail(op, "inspection failed")
		return op, err
	}
	operation.Succeed(op, "inspection completed")
	return op, nil
}

// SetExternalInspectionData submits hardware details collected outside BMO.
// BMO acknowledges the data by consuming its control annotation. Generic host
// metadata APIs intentionally cannot set this annotation.
func (s *Service) SetExternalInspectionData(ctx context.Context, key types.NamespacedName, details *metal3v1alpha1.HardwareDetails, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("set-external-inspection-data", key)
	if err := validateHardwareDetails(details); err != nil {
		return op, err
	}
	host, err := s.getHost(ctx, key)
	if err != nil {
		return op, err
	}
	if host.Status.Provisioning.State == metal3v1alpha1.StateInspecting {
		return op, metal3sdk.InvalidStateError("set-external-inspection-data", key, "host not actively inspecting", string(host.Status.Provisioning.State))
	}
	hasHardware := host.Status.HardwareDetails != nil
	if !hasHardware {
		data := &metal3v1alpha1.HardwareData{}
		if err := s.reader.Get(ctx, key, data); err == nil {
			hasHardware = data.Spec.HardwareDetails != nil
		} else if !apierrors.IsNotFound(err) {
			return op, metal3sdk.KubernetesError("set-external-inspection-data", key, fmt.Errorf("check existing HardwareData: %w", err))
		}
	}
	if !host.InspectionDisabled() && hasHardware {
		return op, metal3sdk.InvalidStateError("set-external-inspection-data", key, "inspection disabled or no existing hardware data", "inspection enabled with existing hardware data")
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return op, metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("hardware details cannot be encoded: %v", err))
	}
	annotationBytes := len(metal3v1alpha1.HardwareDetailsAnnotation) + len(raw)
	for annotationKey, value := range host.Annotations {
		if annotationKey != metal3v1alpha1.HardwareDetailsAnnotation {
			annotationBytes += len(annotationKey) + len(value)
		}
	}
	if annotationBytes > 256*1024 {
		return op, metal3sdk.ValidationError("set-external-inspection-data", "hardware details and existing annotations exceed Kubernetes' 256 KiB annotation limit")
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[metal3v1alpha1.HardwareDetailsAnnotation] = string(raw)
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("set-external-inspection-data", key, err)
	}
	if !opts.Wait {
		op.Message = "external inspection data submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, maintenanceTimeout(opts.Timeout), func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		_, pending := current.Annotations[metal3v1alpha1.HardwareDetailsAnnotation]
		return !pending, nil
	}); err != nil {
		operation.Fail(op, "external inspection data was not accepted")
		return op, err
	}
	operation.Succeed(op, "external inspection data accepted")
	return op, nil
}

func validateHardwareDetails(details *metal3v1alpha1.HardwareDetails) error {
	if details == nil {
		return metal3sdk.ValidationError("set-external-inspection-data", "hardwareDetails is required")
	}
	if details.RAMMebibytes < 0 || details.CPU.Count < 0 || details.CPU.ClockMegahertz < 0 {
		return metal3sdk.ValidationError("set-external-inspection-data", "RAM, CPU count, and CPU clock cannot be negative")
	}
	for index, device := range details.Storage {
		if device.SizeBytes < 0 {
			return metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("storage[%d].sizeBytes cannot be negative", index))
		}
		switch device.Type {
		case "", metal3v1alpha1.HDD, metal3v1alpha1.SSD, metal3v1alpha1.NVME:
		default:
			return metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("storage[%d].type must be HDD, SSD, or NVME", index))
		}
	}
	for index, nic := range details.NIC {
		if nic.MAC != "" {
			parsed, err := net.ParseMAC(nic.MAC)
			if err != nil || len(parsed) != 6 {
				return metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("nics[%d].mac is invalid", index))
			}
		}
		if nic.IP != "" && net.ParseIP(nic.IP) == nil {
			return metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("nics[%d].ip is invalid", index))
		}
		if nic.SpeedGbps < 0 || nic.VLANID < 0 || nic.VLANID > 4094 {
			return metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("nics[%d] has an invalid speed or VLAN ID", index))
		}
		for vlanIndex, vlan := range nic.VLANs {
			if vlan.ID < 0 || vlan.ID > 4094 {
				return metal3sdk.ValidationError("set-external-inspection-data", fmt.Sprintf("nics[%d].vlans[%d].id is outside 0..4094", index, vlanIndex))
			}
		}
	}
	return nil
}

func (s *Service) SetInspectionMode(ctx context.Context, key types.NamespacedName, mode metal3sdk.InspectionMode) (*metal3v1alpha1.BareMetalHost, error) {
	if mode != metal3sdk.InspectionAutomatic && mode != metal3sdk.InspectionDisabled {
		return nil, metal3sdk.ValidationError("set-inspection-mode", "inspection mode must be automatic or disabled")
	}
	err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			if host.Status.Provisioning.State == metal3v1alpha1.StateInspecting {
				return metal3sdk.InvalidStateError("set-inspection-mode", key, "host not actively inspecting", string(host.Status.Provisioning.State))
			}
			if mode == metal3sdk.InspectionDisabled {
				host.Spec.InspectionMode = metal3v1alpha1.InspectionModeDisabled
			} else {
				host.Spec.InspectionMode = metal3v1alpha1.InspectionModeAgent
				if host.Annotations != nil && host.Annotations[metal3v1alpha1.InspectAnnotationPrefix] == metal3v1alpha1.InspectAnnotationValueDisabled {
					delete(host.Annotations, metal3v1alpha1.InspectAnnotationPrefix)
				}
			}
			return nil
		})
	if err != nil {
		return nil, metal3sdk.KubernetesError("set-inspection-mode", key, err)
	}
	return s.getHost(ctx, key)
}

func (s *Service) ConfigureRAID(ctx context.Context, key types.NamespacedName, raid *metal3v1alpha1.RAIDConfig, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("configure-raid", key)
	if raid == nil {
		return op, metal3sdk.ValidationError("configure-raid", "RAID config is required; use an explicit empty hardwareRAIDVolumes list to remove hardware RAID")
	}
	if err := validateRAID(raid); err != nil {
		return op, err
	}
	if _, err := s.availableHost(ctx, key, "configure-raid"); err != nil {
		return op, err
	}
	desired := raid.DeepCopy()
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			host.Spec.RAID = desired.DeepCopy()
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("configure-raid", key, err)
	}
	return s.waitForPreparation(ctx, op, desired, opts)
}

func (s *Service) GetFirmwareSettings(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.HostFirmwareSettings, error) {
	settings := &metal3v1alpha1.HostFirmwareSettings{}
	if err := s.reader.Get(ctx, key, settings); err != nil {
		return nil, metal3sdk.KubernetesError("get-firmware-settings", key, err)
	}
	return settings, nil
}

func (s *Service) GetFirmwareComponents(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.HostFirmwareComponents, error) {
	components := &metal3v1alpha1.HostFirmwareComponents{}
	if err := s.reader.Get(ctx, key, components); err != nil {
		return nil, metal3sdk.KubernetesError("get-firmware-components", key, err)
	}
	return components, nil
}

func (s *Service) GetFirmwareSchema(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.FirmwareSchema, error) {
	schema := &metal3v1alpha1.FirmwareSchema{}
	if err := s.reader.Get(ctx, key, schema); err != nil {
		return nil, metal3sdk.KubernetesError("get-firmware-schema", key, err)
	}
	return schema, nil
}

func (s *Service) GetPreprovisioningImage(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.PreprovisioningImage, error) {
	image := &metal3v1alpha1.PreprovisioningImage{}
	if err := s.reader.Get(ctx, key, image); err != nil {
		return nil, metal3sdk.KubernetesError("get-preprovisioning-image", key, err)
	}
	return image, nil
}

func (s *Service) UpdateFirmwareSettings(ctx context.Context, key types.NamespacedName, settings metal3v1alpha1.DesiredSettingsMap, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("update-firmware-settings", key)
	if len(settings) == 0 {
		return op, metal3sdk.ValidationError("update-firmware-settings", "at least one firmware setting is required")
	}
	host, err := s.getHost(ctx, key)
	if err != nil {
		return op, err
	}
	if !stableMaintenanceState(host.Status.Provisioning.State) {
		return op, metal3sdk.InvalidStateError("update-firmware-settings", key, "available, ready, provisioned, or externally provisioned", string(host.Status.Provisioning.State))
	}
	if opts.Wait && (host.Status.Provisioning.State == metal3v1alpha1.StateProvisioned || host.Status.Provisioning.State == metal3v1alpha1.StateExternallyProvisioned) {
		return op, metal3sdk.ValidationError("update-firmware-settings", "wait cannot complete on a provisioned host before servicing; submit with wait=false, configure HostUpdatePolicy onReboot, then reboot with wait=true")
	}
	desired := cloneDesiredSettings(settings)
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.HostFirmwareSettings { return &metal3v1alpha1.HostFirmwareSettings{} },
		func(current *metal3v1alpha1.HostFirmwareSettings) error {
			if current.Spec.Settings == nil {
				current.Spec.Settings = metal3v1alpha1.DesiredSettingsMap{}
			}
			for name, value := range desired {
				current.Spec.Settings[name] = value
			}
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("update-firmware-settings", key, err)
	}
	if !opts.Wait {
		op.Message = "firmware settings update submitted"
		return op, nil
	}
	if err := s.waitForFirmwareSettings(ctx, key, desired, opts.Timeout); err != nil {
		operation.Fail(op, "firmware settings update failed")
		return op, err
	}
	operation.Succeed(op, "firmware settings applied")
	return op, nil
}

// UpdateFirmwareComponents requests BIOS, BMC, or NIC firmware image updates.
// On an Available host BMO applies them while preparing. On a Provisioned or
// ExternallyProvisioned host a HostUpdatePolicy with firmwareUpdates=onReboot
// and a subsequent reboot are required before a waiting call can complete.
func (s *Service) UpdateFirmwareComponents(ctx context.Context, key types.NamespacedName, updates []metal3v1alpha1.FirmwareUpdate, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("update-firmware-components", key)
	desired := append([]metal3v1alpha1.FirmwareUpdate(nil), updates...)
	if err := validateFirmwareUpdates(desired); err != nil {
		return op, err
	}
	host, err := s.getHost(ctx, key)
	if err != nil {
		return op, err
	}
	if !stableMaintenanceState(host.Status.Provisioning.State) {
		return op, metal3sdk.InvalidStateError("update-firmware-components", key, "available, ready, provisioned, or externally provisioned", string(host.Status.Provisioning.State))
	}
	if opts.Wait && (host.Status.Provisioning.State == metal3v1alpha1.StateProvisioned || host.Status.Provisioning.State == metal3v1alpha1.StateExternallyProvisioned) {
		return op, metal3sdk.ValidationError("update-firmware-components", "wait cannot complete on a provisioned host before servicing; submit with wait=false, configure HostUpdatePolicy onReboot, then reboot with wait=true")
	}

	current := &metal3v1alpha1.HostFirmwareComponents{}
	err = s.reader.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		current = &metal3v1alpha1.HostFirmwareComponents{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: key.Namespace,
				Name:      key.Name,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: metal3v1alpha1.GroupVersion.String(), Kind: "BareMetalHost",
					Name: host.Name, UID: host.UID,
				}},
			},
			Spec: metal3v1alpha1.HostFirmwareComponentsSpec{Updates: desired},
		}
		if err := s.kube.Create(ctx, current); err != nil {
			return op, metal3sdk.KubernetesError("update-firmware-components", key, err)
		}
	} else if err != nil {
		return op, metal3sdk.KubernetesError("update-firmware-components", key, err)
	} else if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.HostFirmwareComponents { return &metal3v1alpha1.HostFirmwareComponents{} },
		func(resource *metal3v1alpha1.HostFirmwareComponents) error {
			resource.Spec.Updates = append([]metal3v1alpha1.FirmwareUpdate(nil), desired...)
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError("update-firmware-components", key, err)
	}
	if !opts.Wait {
		op.Message = "firmware component update submitted"
		return op, nil
	}
	if err := s.waitForFirmwareComponents(ctx, key, desired, opts.Timeout); err != nil {
		operation.Fail(op, "firmware component update failed")
		return op, err
	}
	operation.Succeed(op, "firmware component update applied")
	return op, nil
}

func (s *Service) SetPreprovisioningNetworkData(ctx context.Context, key types.NamespacedName, data []byte) (*metal3v1alpha1.BareMetalHost, error) {
	host, err := s.getHost(ctx, key)
	if err != nil {
		return nil, err
	}
	name := ""
	if len(data) > 0 {
		owner := metav1.OwnerReference{APIVersion: metal3v1alpha1.GroupVersion.String(), Kind: "BareMetalHost", Name: host.Name, UID: host.UID}
		ref, err := internalsecret.ContentAddressed(ctx, s.kube, host.Namespace, host.Name, "preprovisioning-networkdata", "networkData", data, &owner)
		if err != nil {
			return nil, metal3sdk.KubernetesError("set-preprovisioning-network-data", key, err)
		}
		name = ref.Name
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			current.Spec.PreprovisioningNetworkDataName = name
			return nil
		}); err != nil {
		return nil, metal3sdk.KubernetesError("set-preprovisioning-network-data", key, err)
	}
	return s.getHost(ctx, key)
}

func (s *Service) waitForPreparation(ctx context.Context, op *metal3sdk.Operation, raid *metal3v1alpha1.RAIDConfig, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	if !opts.Wait {
		op.Message = "host preparation submitted"
		return op, nil
	}
	_, err := operation.WaitForHost(ctx, s.reader, op.Host, maintenanceTimeout(opts.Timeout), func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		if !isAvailable(host) {
			return false, nil
		}
		if raid != nil && !reflect.DeepEqual(host.Status.Provisioning.RAID, raid) {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		operation.Fail(op, "host preparation failed")
		return op, err
	}
	operation.Succeed(op, "host preparation completed")
	return op, nil
}

func (s *Service) waitForFirmwareSettings(ctx context.Context, key types.NamespacedName, desired metal3v1alpha1.DesiredSettingsMap, timeout time.Duration) error {
	timeout = maintenanceTimeout(timeout)
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		current := &metal3v1alpha1.HostFirmwareSettings{}
		if err := s.reader.Get(ctx, key, current); err != nil {
			if apierrors.IsNotFound(err) {
				return false, metal3sdk.KubernetesError("update-firmware-settings", key, err)
			}
			return false, err
		}
		valid := apimeta.FindStatusCondition(current.Status.Conditions, string(metal3v1alpha1.FirmwareSettingsValid))
		if valid != nil && valid.Status == metav1.ConditionFalse {
			return false, metal3sdk.ValidationError("update-firmware-settings", "firmware settings were rejected by the FirmwareSchema")
		}
		changed := apimeta.FindStatusCondition(current.Status.Conditions, string(metal3v1alpha1.FirmwareSettingsChangeDetected))
		if valid == nil || valid.Status != metav1.ConditionTrue || changed == nil || changed.Status != metav1.ConditionFalse {
			return false, nil
		}
		for name, value := range desired {
			if current.Status.Settings[name] != value.String() {
				return false, nil
			}
		}
		return true, nil
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wait.ErrWaitTimeout) {
		return &metal3sdk.Error{Code: metal3sdk.CodeTimeout, Operation: "update-firmware-settings", Host: key, Retryable: true, Message: "timed out waiting for firmware settings", Cause: err}
	}
	return err
}

func (s *Service) waitForFirmwareComponents(ctx context.Context, key types.NamespacedName, desired []metal3v1alpha1.FirmwareUpdate, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, maintenanceTimeout(timeout), true, func(ctx context.Context) (bool, error) {
		current := &metal3v1alpha1.HostFirmwareComponents{}
		if err := s.reader.Get(ctx, key, current); err != nil {
			return false, metal3sdk.KubernetesError("update-firmware-components", key, err)
		}
		valid := apimeta.FindStatusCondition(current.Status.Conditions, string(metal3v1alpha1.HostFirmwareComponentsValid))
		if valid != nil && valid.Status == metav1.ConditionFalse {
			return false, metal3sdk.ValidationError("update-firmware-components", "firmware component update was rejected by Bare Metal Operator")
		}
		changed := apimeta.FindStatusCondition(current.Status.Conditions, string(metal3v1alpha1.HostFirmwareComponentsChangeDetected))
		return valid != nil && valid.Status == metav1.ConditionTrue &&
			changed != nil && changed.Status == metav1.ConditionFalse &&
			firmwareUpdatesEqual(current.Status.Updates, desired), nil
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wait.ErrWaitTimeout) {
		return &metal3sdk.Error{Code: metal3sdk.CodeTimeout, Operation: "update-firmware-components", Host: key, Retryable: true, Message: "timed out waiting for firmware component update", Cause: err}
	}
	return err
}

func (s *Service) availableHost(ctx context.Context, key types.NamespacedName, operationName string) (*metal3v1alpha1.BareMetalHost, error) {
	host, err := s.getHost(ctx, key)
	if err != nil {
		return nil, err
	}
	if !isAvailable(host) {
		return nil, metal3sdk.InvalidStateError(operationName, key, "available", string(host.Status.Provisioning.State))
	}
	return host, nil
}

func (s *Service) getHost(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.BareMetalHost, error) {
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, host); err != nil {
		return nil, metal3sdk.KubernetesError("get-host", key, err)
	}
	return host, nil
}

func isAvailable(host *metal3v1alpha1.BareMetalHost) bool {
	return host.Status.Provisioning.State == metal3v1alpha1.StateAvailable || host.Status.Provisioning.State == metal3v1alpha1.StateReady
}

func stableMaintenanceState(state metal3v1alpha1.ProvisioningState) bool {
	return state == metal3v1alpha1.StateAvailable || state == metal3v1alpha1.StateReady ||
		state == metal3v1alpha1.StateProvisioned || state == metal3v1alpha1.StateExternallyProvisioned
}

func maintenanceTimeout(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultMaintenanceTimeout
	}
	return value
}

func cloneDesiredSettings(in metal3v1alpha1.DesiredSettingsMap) metal3v1alpha1.DesiredSettingsMap {
	out := make(metal3v1alpha1.DesiredSettingsMap, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func validateFirmwareUpdates(updates []metal3v1alpha1.FirmwareUpdate) error {
	candidate := &metal3v1alpha1.HostFirmwareComponents{Spec: metal3v1alpha1.HostFirmwareComponentsSpec{Updates: updates}}
	if err := candidate.ValidateHostFirmwareComponents(); err != nil {
		return metal3sdk.ValidationError("update-firmware-components", err.Error())
	}
	seen := make(map[string]struct{}, len(updates))
	for _, update := range updates {
		component := strings.TrimSpace(update.Component)
		if component == "" || component != update.Component {
			return metal3sdk.ValidationError("update-firmware-components", "firmware component names must be non-empty and cannot contain surrounding whitespace")
		}
		if _, exists := seen[component]; exists {
			return metal3sdk.ValidationError("update-firmware-components", fmt.Sprintf("duplicate firmware component %q", component))
		}
		seen[component] = struct{}{}
		parsed, err := url.Parse(update.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return metal3sdk.ValidationError("update-firmware-components", fmt.Sprintf("firmware URL for %q must use http or https and include a host", component))
		}
	}
	return nil
}

func firmwareUpdatesEqual(left, right []metal3v1alpha1.FirmwareUpdate) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]string, len(left))
	for _, update := range left {
		values[update.Component] = update.URL
	}
	for _, update := range right {
		if values[update.Component] != update.URL {
			return false
		}
	}
	return true
}

func validateRAID(raid *metal3v1alpha1.RAIDConfig) error {
	hardwareSet := raid.HardwareRAIDVolumes != nil
	softwareSet := raid.SoftwareRAIDVolumes != nil
	if hardwareSet == softwareSet {
		return metal3sdk.ValidationError("configure-raid", "set exactly one of hardwareRAIDVolumes or softwareRAIDVolumes; use an explicit empty list to remove that RAID type")
	}
	for _, volume := range raid.HardwareRAIDVolumes {
		if volume.Level == "" {
			return metal3sdk.ValidationError("configure-raid", "each hardware RAID volume requires a level")
		}
		if volume.NumberOfPhysicalDisks != nil && len(volume.PhysicalDisks) > 0 {
			return metal3sdk.ValidationError("configure-raid", "hardware RAID volume cannot set both numberOfPhysicalDisks and physicalDisks")
		}
	}
	if len(raid.SoftwareRAIDVolumes) > 2 {
		return metal3sdk.ValidationError("configure-raid", "software RAID supports at most two volumes")
	}
	for index, volume := range raid.SoftwareRAIDVolumes {
		if (index == 0 && volume.Level != "1") || (index > 0 && volume.Level != "0" && volume.Level != "1" && volume.Level != "1+0") {
			return metal3sdk.ValidationError("configure-raid", "the first software RAID volume must use level 1; a second may use 0, 1, or 1+0")
		}
		if len(volume.PhysicalDisks) == 1 {
			return metal3sdk.ValidationError("configure-raid", "software RAID physicalDisks must contain at least two device hints")
		}
	}
	return nil
}
