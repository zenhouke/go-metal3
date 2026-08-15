package power

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalpatch "github.com/zenhouke/go-metal3/pkg/internal/patch"
	"github.com/zenhouke/go-metal3/pkg/operation"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

const softPoweroffFallbackMessage = "Continuing with hard poweroff after soft poweroff fails. More details: "

// Service translates imperative power operations into BMH desired state.
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

func (s *Service) PowerOn(ctx context.Context, key types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	return s.setOnline(ctx, key, true, opts)
}

func (s *Service) PowerOff(ctx context.Context, key types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	return s.setOnline(ctx, key, false, opts)
}

func (s *Service) setOnline(ctx context.Context, key types.NamespacedName, online bool, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	kind := "power-off"
	if online {
		kind = "power-on"
	}
	op := operation.New(kind, key)
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			if !stablePowerState(host.Status.Provisioning.State) {
				return metal3sdk.InvalidStateError(kind, key, "available, ready, provisioned, or externally provisioned", string(host.Status.Provisioning.State))
			}
			host.Spec.Online = online
			return nil
		}); err != nil {
		return op, metal3sdk.KubernetesError(kind, key, fmt.Errorf("set desired power state: %w", err))
	}
	if !opts.Wait {
		op.Message = "desired power state submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, opts.Timeout, func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		return host.Status.PoweredOn == online, nil
	}); err != nil {
		return op, err
	}
	operation.Succeed(op, "power state converged")
	return op, nil
}

func stablePowerState(state metal3v1alpha1.ProvisioningState) bool {
	switch state {
	case metal3v1alpha1.StateAvailable, metal3v1alpha1.StateReady,
		metal3v1alpha1.StateProvisioned, metal3v1alpha1.StateExternallyProvisioned:
		return true
	default:
		return false
	}
}

func (s *Service) Reboot(ctx context.Context, key types.NamespacedName, opts metal3sdk.RebootOptions) (*metal3sdk.Operation, error) {
	op := operation.New("reboot", key)
	value, err := rebootAnnotationValue("reboot", opts.Mode, opts.Force)
	if err != nil {
		return op, err
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			if host.Status.Provisioning.State != metal3v1alpha1.StateProvisioned && host.Status.Provisioning.State != metal3v1alpha1.StateExternallyProvisioned {
				return metal3sdk.InvalidStateError("reboot", key, "provisioned or externally provisioned", string(host.Status.Provisioning.State))
			}
			if host.Annotations == nil {
				host.Annotations = map[string]string{}
			}
			if _, pending := host.Annotations[metal3v1alpha1.RebootAnnotationPrefix]; pending {
				return &metal3sdk.Error{Code: metal3sdk.CodeConflict, Operation: "reboot", Host: key, Retryable: true, Message: "a reboot is already pending"}
			}
			host.Annotations[metal3v1alpha1.RebootAnnotationPrefix] = value
			return nil
		}); err != nil {
		return op, err
	}
	if !opts.Wait {
		op.Message = "reboot annotation submitted"
		return op, nil
	}
	if _, err := operation.WaitForHostToleratingErrors(ctx, s.reader, key, opts.Timeout, func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		_, pending := host.Annotations[metal3v1alpha1.RebootAnnotationPrefix]
		return !pending && host.Status.PoweredOn && host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK, nil
	}, tolerableRebootError); err != nil {
		operation.Fail(op, "reboot failed")
		return op, err
	}
	operation.Succeed(op, "host rebooted")
	return op, nil
}

// StartPhasedReboot adds a unique reboot annotation owned by this operation.
// BMO powers the host off and keeps it off until every phased annotation has
// been removed by its owning client.
func (s *Service) StartPhasedReboot(ctx context.Context, key types.NamespacedName, opts metal3sdk.PhasedRebootOptions) (*metal3sdk.Operation, error) {
	op := operation.New("phased-reboot-start", key)
	value, err := rebootAnnotationValue("phased-reboot-start", opts.Mode, opts.Force)
	if err != nil {
		return op, err
	}
	annotationKey := metal3v1alpha1.RebootAnnotationPrefix + "/" + op.ID
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			if host.Status.Provisioning.State != metal3v1alpha1.StateProvisioned && host.Status.Provisioning.State != metal3v1alpha1.StateExternallyProvisioned {
				return metal3sdk.InvalidStateError("phased-reboot-start", key, "provisioned or externally provisioned", string(host.Status.Provisioning.State))
			}
			if !host.Spec.Online || !host.Status.PoweredOn {
				return metal3sdk.InvalidStateError("phased-reboot-start", key, "online and currently powered on", "offline or powered off")
			}
			if host.Annotations == nil {
				host.Annotations = map[string]string{}
			}
			if _, pending := host.Annotations[metal3v1alpha1.RebootAnnotationPrefix]; pending {
				return &metal3sdk.Error{Code: metal3sdk.CodeConflict, Operation: "phased-reboot-start", Host: key, Retryable: true, Message: "a simple reboot is already pending"}
			}
			host.Annotations[annotationKey] = value
			return nil
		}); err != nil {
		return op, err
	}
	if !opts.Wait {
		op.Message = "phased reboot submitted; complete it with this operation ID after the host is powered off"
		return op, nil
	}
	if _, err := operation.WaitForHostToleratingErrors(ctx, s.reader, key, opts.Timeout, func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		_, owned := host.Annotations[annotationKey]
		return owned && !host.Status.PoweredOn, nil
	}, tolerableRebootError); err != nil {
		operation.Fail(op, "host did not reach the phased reboot powered-off state")
		return op, err
	}
	op.Message = "host is powered off; complete the phased reboot with this operation ID"
	return op, nil
}

// CompletePhasedReboot removes only the annotation identified by phaseID. It
// never removes annotations owned by other clients.
func (s *Service) CompletePhasedReboot(ctx context.Context, key types.NamespacedName, phaseID string, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("phased-reboot-complete", key)
	if parsed, err := uuid.Parse(phaseID); err != nil || parsed.String() != phaseID {
		return op, metal3sdk.ValidationError("phased-reboot-complete", "phaseID must be the operation ID returned by phased-reboot-start")
	}
	annotationKey := metal3v1alpha1.RebootAnnotationPrefix + "/" + phaseID
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, host); err != nil {
		return op, metal3sdk.KubernetesError("phased-reboot-complete", key, err)
	}
	if _, exists := host.Annotations[annotationKey]; !exists {
		return op, &metal3sdk.Error{Code: metal3sdk.CodeNotFound, Operation: "phased-reboot-complete", Host: key, Message: "phased reboot ID does not exist on this host"}
	}
	if host.Status.PoweredOn {
		return op, metal3sdk.InvalidStateError("phased-reboot-complete", key, "powered off by the phased reboot", "powered on")
	}
	if err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if _, exists := current.Annotations[annotationKey]; !exists {
				return &metal3sdk.Error{Code: metal3sdk.CodeNotFound, Operation: "phased-reboot-complete", Host: key, Message: "phased reboot ID no longer exists on this host"}
			}
			delete(current.Annotations, annotationKey)
			return nil
		}); err != nil {
		return op, err
	}
	if !opts.Wait {
		op.Message = "this client's phased reboot hold was removed"
		return op, nil
	}
	if _, err := operation.WaitForHostToleratingErrors(ctx, s.reader, key, opts.Timeout, func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		for annotation := range current.Annotations {
			if strings.HasPrefix(annotation, metal3v1alpha1.RebootAnnotationPrefix+"/") {
				return false, nil
			}
		}
		return current.Status.PoweredOn && current.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK, nil
	}, tolerableRebootError); err != nil {
		operation.Fail(op, "host did not complete the phased reboot")
		return op, err
	}
	operation.Succeed(op, "phased reboot completed")
	return op, nil
}

func tolerableRebootError(host *metal3v1alpha1.BareMetalHost) bool {
	return host.Status.ErrorType == metal3v1alpha1.PowerManagementError &&
		strings.HasPrefix(host.Status.ErrorMessage, softPoweroffFallbackMessage)
}

func rebootAnnotationValue(operationName string, mode metal3sdk.RebootMode, force bool) (string, error) {
	if mode != metal3sdk.RebootAuto && mode != metal3sdk.RebootSoft && mode != metal3sdk.RebootHard {
		return "", metal3sdk.ValidationError(operationName, "reboot mode must be auto, soft, or hard")
	}
	if mode == metal3sdk.RebootAuto && !force {
		return "", nil
	}
	raw, err := json.Marshal(metal3v1alpha1.RebootAnnotationArguments{Mode: metal3v1alpha1.RebootMode(mode), Force: force})
	if err != nil {
		return "", fmt.Errorf("encode reboot annotation: %w", err)
	}
	return string(raw), nil
}
