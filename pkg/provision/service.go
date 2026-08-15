package provision

import (
	"context"
	"fmt"
	"time"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalpatch "github.com/zenhouke/go-metal3/pkg/internal/patch"
	internalsecret "github.com/zenhouke/go-metal3/pkg/internal/secret"
	"github.com/zenhouke/go-metal3/pkg/operation"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

const defaultProvisionTimeout = 2 * time.Hour

// Service owns operating-system deployment orchestration and the immutable
// configuration Secrets referenced by BareMetalHost specs.
type Service struct {
	kube   ctrlclient.Client
	reader ctrlclient.Reader
}

type configReferences struct {
	userData    *corev1.SecretReference
	networkData *corev1.SecretReference
	metaData    *corev1.SecretReference
}

func New(kube ctrlclient.Client, reader ctrlclient.Reader) *Service {
	if reader == nil {
		reader = kube
	}
	return &Service{kube: kube, reader: reader}
}

func (s *Service) Install(ctx context.Context, key types.NamespacedName, req metal3sdk.InstallRequest) (*metal3sdk.Operation, error) {
	op := operation.New("install", key)
	if err := validateInstall(req); err != nil {
		return op, err
	}
	host, err := s.availableHost(ctx, key, "install")
	if err != nil {
		return op, err
	}
	if err := s.validateOCIAuthSecret(ctx, host, req, "install"); err != nil {
		return op, err
	}
	refs, err := s.materializeConfigSecrets(ctx, host, req)
	if err != nil {
		return op, metal3sdk.KubernetesError("install", key, fmt.Errorf("materialize provisioning configuration: %w", err))
	}
	if err := s.submitInstall(ctx, key, req, refs, "install"); err != nil {
		return op, err
	}
	if !req.Wait {
		op.Message = "operating-system installation submitted"
		return op, nil
	}
	if err := s.waitProvisioned(ctx, key, req); err != nil {
		operation.Fail(op, "operating-system installation failed")
		return op, err
	}
	operation.Succeed(op, "host is provisioned")
	return op, nil
}

func (s *Service) CustomDeploy(ctx context.Context, key types.NamespacedName, req metal3sdk.CustomDeployRequest) (*metal3sdk.Operation, error) {
	op := operation.New("custom-deploy", key)
	if err := validateCustomDeploy(req); err != nil {
		return op, err
	}
	host, err := s.availableHost(ctx, key, "custom-deploy")
	if err != nil {
		return op, err
	}
	config := metal3sdk.InstallRequest{UserData: req.UserData, NetworkData: req.NetworkData, MetaData: req.MetaData}
	refs, err := s.materializeConfigSecrets(ctx, host, config)
	if err != nil {
		return op, metal3sdk.KubernetesError("custom-deploy", key, fmt.Errorf("materialize custom deployment configuration: %w", err))
	}
	err = internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(current *metal3v1alpha1.BareMetalHost) error {
			if current.Status.Provisioning.State != metal3v1alpha1.StateAvailable && current.Status.Provisioning.State != metal3v1alpha1.StateReady {
				return metal3sdk.InvalidStateError("custom-deploy", key, string(metal3v1alpha1.StateAvailable), string(current.Status.Provisioning.State))
			}
			current.Spec.Image = nil
			current.Spec.CustomDeploy = &metal3v1alpha1.CustomDeploy{Method: req.Method}
			current.Spec.RootDeviceHints = req.RootDeviceHints
			current.Spec.UserData, current.Spec.NetworkData, current.Spec.MetaData = refs.userData, refs.networkData, refs.metaData
			current.Spec.Online = req.PowerOn
			return nil
		})
	if err != nil {
		return op, metal3sdk.KubernetesError("custom-deploy", key, err)
	}
	if !req.Wait {
		op.Message = "custom deployment submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, provisionTimeout(req.Timeout), func(current *metal3v1alpha1.BareMetalHost) (bool, error) {
		return current.Status.Provisioning.State == metal3v1alpha1.StateProvisioned &&
			current.Status.Provisioning.CustomDeploy != nil && current.Status.Provisioning.CustomDeploy.Method == req.Method, nil
	}); err != nil {
		operation.Fail(op, "custom deployment failed")
		return op, err
	}
	operation.Succeed(op, "custom deployment completed")
	return op, nil
}

// Reinstall performs the required two-stage workflow for a same-URL image:
// deprovision to Available, then atomically submit the new image and config.
// It always waits for deprovisioning because safely resuming this workflow
// requires persisted orchestration outside an in-process SDK call.
func (s *Service) Reinstall(ctx context.Context, key types.NamespacedName, req metal3sdk.InstallRequest) (*metal3sdk.Operation, error) {
	op := operation.New("reinstall", key)
	if err := validateInstall(req); err != nil {
		return op, err
	}
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, host); err != nil {
		return op, metal3sdk.KubernetesError("reinstall", key, err)
	}
	if host.Status.Provisioning.State != metal3v1alpha1.StateProvisioned {
		return op, metal3sdk.InvalidStateError("reinstall", key, string(metal3v1alpha1.StateProvisioned), string(host.Status.Provisioning.State))
	}
	if err := s.validateOCIAuthSecret(ctx, host, req, "reinstall"); err != nil {
		return op, err
	}
	if err := s.clearProvisioningSpec(ctx, key); err != nil {
		return op, fmt.Errorf("start deprovisioning for reinstall: %w", err)
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, provisionTimeout(req.Timeout), func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		return host.Status.Provisioning.State == metal3v1alpha1.StateAvailable || host.Status.Provisioning.State == metal3v1alpha1.StateReady, nil
	}); err != nil {
		operation.Fail(op, "deprovisioning for reinstall failed")
		return op, err
	}
	host, err := s.availableHost(ctx, key, "reinstall")
	if err != nil {
		return op, err
	}
	refs, err := s.materializeConfigSecrets(ctx, host, req)
	if err != nil {
		return op, metal3sdk.KubernetesError("reinstall", key, fmt.Errorf("materialize provisioning configuration: %w", err))
	}
	if err := s.submitInstall(ctx, key, req, refs, "reinstall"); err != nil {
		return op, err
	}
	if !req.Wait {
		op.Message = "reinstallation submitted after successful deprovisioning"
		return op, nil
	}
	if err := s.waitProvisioned(ctx, key, req); err != nil {
		operation.Fail(op, "operating-system reinstallation failed")
		return op, err
	}
	operation.Succeed(op, "host is reprovisioned")
	return op, nil
}

func (s *Service) Deprovision(ctx context.Context, key types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	op := operation.New("deprovision", key)
	if err := s.clearProvisioningSpec(ctx, key); err != nil {
		return op, err
	}
	if !opts.Wait {
		op.Message = "deprovisioning submitted"
		return op, nil
	}
	if _, err := operation.WaitForHost(ctx, s.reader, key, provisionTimeout(opts.Timeout), func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		return host.Status.Provisioning.State == metal3v1alpha1.StateAvailable || host.Status.Provisioning.State == metal3v1alpha1.StateReady, nil
	}); err != nil {
		operation.Fail(op, "deprovisioning failed")
		return op, err
	}
	operation.Succeed(op, "host is available")
	return op, nil
}

func (s *Service) availableHost(ctx context.Context, key types.NamespacedName, operationName string) (*metal3v1alpha1.BareMetalHost, error) {
	host := &metal3v1alpha1.BareMetalHost{}
	if err := s.reader.Get(ctx, key, host); err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	if host.Status.Provisioning.State != metal3v1alpha1.StateAvailable && host.Status.Provisioning.State != metal3v1alpha1.StateReady {
		return nil, metal3sdk.InvalidStateError(operationName, key, string(metal3v1alpha1.StateAvailable), string(host.Status.Provisioning.State))
	}
	return host, nil
}

func (s *Service) submitInstall(ctx context.Context, key types.NamespacedName, req metal3sdk.InstallRequest, refs configReferences, operationName string) error {
	err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			if host.Status.Provisioning.State != metal3v1alpha1.StateAvailable && host.Status.Provisioning.State != metal3v1alpha1.StateReady {
				return metal3sdk.InvalidStateError(operationName, key, string(metal3v1alpha1.StateAvailable), string(host.Status.Provisioning.State))
			}
			image := &metal3v1alpha1.Image{URL: req.ImageURL, Checksum: req.Checksum, ChecksumType: req.ChecksumType, DiskFormat: req.DiskFormat}
			if req.OCIAuthSecretName != "" {
				name := req.OCIAuthSecretName
				image.OCIAuthSecretName = &name
			}
			host.Spec.Image = image
			host.Spec.RootDeviceHints = req.RootDeviceHints
			host.Spec.UserData = refs.userData
			host.Spec.NetworkData = refs.networkData
			host.Spec.MetaData = refs.metaData
			host.Spec.CustomDeploy = nil
			host.Spec.Online = req.PowerOn
			return nil
		})
	return metal3sdk.KubernetesError(operationName, key, err)
}

func (s *Service) validateOCIAuthSecret(ctx context.Context, host *metal3v1alpha1.BareMetalHost, req metal3sdk.InstallRequest, operationName string) error {
	if req.OCIAuthSecretName == "" {
		return nil
	}
	secretKey := types.NamespacedName{Namespace: host.Namespace, Name: req.OCIAuthSecretName}
	secret := &corev1.Secret{}
	if err := s.reader.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return metal3sdk.ValidationError(operationName, "OCI auth Secret does not exist in the BareMetalHost namespace")
		}
		return metal3sdk.KubernetesError(operationName, types.NamespacedName{Namespace: host.Namespace, Name: host.Name}, fmt.Errorf("read OCI auth Secret: %w", err))
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson && secret.Type != corev1.SecretTypeDockercfg {
		return metal3sdk.ValidationError(operationName, "OCI auth Secret must use kubernetes.io/dockerconfigjson or kubernetes.io/dockercfg type")
	}
	return nil
}

func (s *Service) clearProvisioningSpec(ctx context.Context, key types.NamespacedName) error {
	err := internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.BareMetalHost { return &metal3v1alpha1.BareMetalHost{} },
		func(host *metal3v1alpha1.BareMetalHost) error {
			host.Spec.Image = nil
			host.Spec.UserData = nil
			host.Spec.NetworkData = nil
			host.Spec.MetaData = nil
			host.Spec.CustomDeploy = nil
			return nil
		})
	return metal3sdk.KubernetesError("deprovision", key, err)
}

func (s *Service) materializeConfigSecrets(ctx context.Context, host *metal3v1alpha1.BareMetalHost, req metal3sdk.InstallRequest) (configReferences, error) {
	owner := metav1.OwnerReference{APIVersion: metal3v1alpha1.GroupVersion.String(), Kind: "BareMetalHost", Name: host.Name, UID: host.UID}
	var refs configReferences
	var err error
	if len(req.UserData) > 0 {
		refs.userData, err = internalsecret.ContentAddressed(ctx, s.kube, host.Namespace, host.Name, "userdata", "userData", req.UserData, &owner)
		if err != nil {
			return refs, err
		}
	}
	if len(req.NetworkData) > 0 {
		refs.networkData, err = internalsecret.ContentAddressed(ctx, s.kube, host.Namespace, host.Name, "networkdata", "networkData", req.NetworkData, &owner)
		if err != nil {
			return refs, err
		}
	}
	if len(req.MetaData) > 0 {
		refs.metaData, err = internalsecret.ContentAddressed(ctx, s.kube, host.Namespace, host.Name, "metadata", "metaData", req.MetaData, &owner)
		if err != nil {
			return refs, err
		}
	}
	return refs, nil
}

func (s *Service) waitProvisioned(ctx context.Context, key types.NamespacedName, req metal3sdk.InstallRequest) error {
	_, err := operation.WaitForHost(ctx, s.reader, key, provisionTimeout(req.Timeout), func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
		return host.Status.Provisioning.State == metal3v1alpha1.StateProvisioned && host.Status.Provisioning.Image.URL == req.ImageURL, nil
	})
	return err
}

func provisionTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultProvisionTimeout
	}
	return timeout
}
