// Package resources provides typed CRUD helpers for the auxiliary resources
// shipped with Bare Metal Operator v0.13.
package resources

import (
	"context"
	"fmt"
	"net/url"
	"regexp"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalpatch "github.com/zenhouke/go-metal3/pkg/internal/patch"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

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

func (s *Service) CreateBMCEventSubscription(ctx context.Context, req metal3sdk.BMCEventSubscriptionCreateRequest) (*metal3v1alpha1.BMCEventSubscription, error) {
	const operationName = "create-bmc-event-subscription"
	if err := validateObjectKey(operationName, req.Namespace, req.Name); err != nil {
		return nil, err
	}
	if req.HostName == "" {
		return nil, metal3sdk.ValidationError(operationName, "host name is required")
	}
	if problems := validation.IsDNS1123Subdomain(req.HostName); len(problems) != 0 {
		return nil, metal3sdk.ValidationError(operationName, fmt.Sprintf("invalid host name %q", req.HostName))
	}
	if err := validateHTTPURL(operationName, "destination", req.Destination); err != nil {
		return nil, err
	}
	headersRef := req.HTTPHeadersRef.DeepCopy()
	if headersRef != nil {
		if headersRef.Namespace == "" {
			headersRef.Namespace = req.Namespace
		}
		if headersRef.Namespace != req.Namespace {
			return nil, metal3sdk.ValidationError(operationName, "HTTP headers Secret must be in the subscription namespace")
		}
		if problems := validation.IsDNS1123Subdomain(headersRef.Name); len(problems) != 0 {
			return nil, metal3sdk.ValidationError(operationName, "HTTP headers Secret has an invalid name")
		}
	}
	object := &metal3v1alpha1.BMCEventSubscription{}
	object.Namespace = req.Namespace
	object.Name = req.Name
	object.Labels = cloneMap(req.Labels)
	object.Annotations = cloneMap(req.Annotations)
	object.Spec = metal3v1alpha1.BMCEventSubscriptionSpec{
		HostName: req.HostName, Destination: req.Destination,
		Context: req.Context, HTTPHeadersRef: headersRef,
	}
	if err := s.kube.Create(ctx, object); err != nil {
		return nil, metal3sdk.KubernetesError(operationName, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, err)
	}
	return object, nil
}

func (s *Service) GetBMCEventSubscription(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.BMCEventSubscription, error) {
	object := &metal3v1alpha1.BMCEventSubscription{}
	if err := s.reader.Get(ctx, key, object); err != nil {
		return nil, metal3sdk.KubernetesError("get-bmc-event-subscription", key, err)
	}
	return object, nil
}

func (s *Service) ListBMCEventSubscriptions(ctx context.Context, opts metal3sdk.ResourceListOptions) ([]metal3v1alpha1.BMCEventSubscription, error) {
	list := &metal3v1alpha1.BMCEventSubscriptionList{}
	if err := s.reader.List(ctx, list, listOptions(opts)...); err != nil {
		return nil, metal3sdk.KubernetesError("list-bmc-event-subscriptions", types.NamespacedName{Namespace: opts.Namespace}, err)
	}
	return list.Items, nil
}

func (s *Service) DeleteBMCEventSubscription(ctx context.Context, key types.NamespacedName) error {
	return deleteObject(ctx, s.kube, "delete-bmc-event-subscription", key, &metal3v1alpha1.BMCEventSubscription{})
}

func (s *Service) ApplyDataImage(ctx context.Context, req metal3sdk.DataImageApplyRequest) (*metal3v1alpha1.DataImage, error) {
	const operationName = "apply-data-image"
	if err := validateObjectKey(operationName, req.Namespace, req.Name); err != nil {
		return nil, err
	}
	if err := validateHTTPURL(operationName, "data image URL", req.URL); err != nil {
		return nil, err
	}
	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}
	current := &metal3v1alpha1.DataImage{}
	err := s.reader.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		current.Namespace, current.Name = req.Namespace, req.Name
		current.Labels, current.Annotations = cloneMap(req.Labels), cloneMap(req.Annotations)
		current.Spec.URL = req.URL
		if err := s.kube.Create(ctx, current); err != nil {
			return nil, metal3sdk.KubernetesError(operationName, key, err)
		}
		return current, nil
	}
	if err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	err = internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.DataImage { return &metal3v1alpha1.DataImage{} },
		func(object *metal3v1alpha1.DataImage) error {
			object.Labels = mergeMap(object.Labels, req.Labels)
			object.Annotations = mergeMap(object.Annotations, req.Annotations)
			object.Spec.URL = req.URL
			return nil
		})
	if err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	return s.GetDataImage(ctx, key)
}

func (s *Service) GetDataImage(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.DataImage, error) {
	object := &metal3v1alpha1.DataImage{}
	if err := s.reader.Get(ctx, key, object); err != nil {
		return nil, metal3sdk.KubernetesError("get-data-image", key, err)
	}
	return object, nil
}

func (s *Service) ListDataImages(ctx context.Context, opts metal3sdk.ResourceListOptions) ([]metal3v1alpha1.DataImage, error) {
	list := &metal3v1alpha1.DataImageList{}
	if err := s.reader.List(ctx, list, listOptions(opts)...); err != nil {
		return nil, metal3sdk.KubernetesError("list-data-images", types.NamespacedName{Namespace: opts.Namespace}, err)
	}
	return list.Items, nil
}

func (s *Service) DeleteDataImage(ctx context.Context, key types.NamespacedName) error {
	return deleteObject(ctx, s.kube, "delete-data-image", key, &metal3v1alpha1.DataImage{})
}

func (s *Service) CreateHostClaim(ctx context.Context, req metal3sdk.HostClaimCreateRequest) (*metal3v1alpha1.HostClaim, error) {
	const operationName = "create-host-claim"
	if err := validateObjectKey(operationName, req.Namespace, req.Name); err != nil {
		return nil, err
	}
	if req.Spec.Image != nil && req.Spec.CustomDeploy != nil {
		return nil, metal3sdk.ValidationError(operationName, "image and customDeploy are mutually exclusive")
	}
	object := &metal3v1alpha1.HostClaim{}
	object.Namespace, object.Name = req.Namespace, req.Name
	object.Labels, object.Annotations = cloneMap(req.Labels), cloneMap(req.Annotations)
	req.Spec.DeepCopyInto(&object.Spec)
	if err := s.kube.Create(ctx, object); err != nil {
		return nil, metal3sdk.KubernetesError(operationName, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, err)
	}
	return object, nil
}

func (s *Service) GetHostClaim(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.HostClaim, error) {
	object := &metal3v1alpha1.HostClaim{}
	if err := s.reader.Get(ctx, key, object); err != nil {
		return nil, metal3sdk.KubernetesError("get-host-claim", key, err)
	}
	return object, nil
}

func (s *Service) ListHostClaims(ctx context.Context, opts metal3sdk.ResourceListOptions) ([]metal3v1alpha1.HostClaim, error) {
	list := &metal3v1alpha1.HostClaimList{}
	if err := s.reader.List(ctx, list, listOptions(opts)...); err != nil {
		return nil, metal3sdk.KubernetesError("list-host-claims", types.NamespacedName{Namespace: opts.Namespace}, err)
	}
	return list.Items, nil
}

func (s *Service) DeleteHostClaim(ctx context.Context, key types.NamespacedName) error {
	return deleteObject(ctx, s.kube, "delete-host-claim", key, &metal3v1alpha1.HostClaim{})
}

func (s *Service) ApplyHostDeployPolicy(ctx context.Context, req metal3sdk.HostDeployPolicyApplyRequest) (*metal3v1alpha1.HostDeployPolicy, error) {
	const operationName = "apply-host-deploy-policy"
	if err := validateObjectKey(operationName, req.Namespace, req.Name); err != nil {
		return nil, err
	}
	if namespaces := req.Spec.HostClaimNamespaces; namespaces != nil && namespaces.NameMatches != "" {
		if _, err := regexp.Compile(namespaces.NameMatches); err != nil {
			return nil, metal3sdk.ValidationError(operationName, "host claim namespace nameMatches is not a valid regular expression")
		}
	}
	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}
	current := &metal3v1alpha1.HostDeployPolicy{}
	err := s.reader.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		current.Namespace, current.Name = req.Namespace, req.Name
		current.Labels, current.Annotations = cloneMap(req.Labels), cloneMap(req.Annotations)
		req.Spec.DeepCopyInto(&current.Spec)
		if err := s.kube.Create(ctx, current); err != nil {
			return nil, metal3sdk.KubernetesError(operationName, key, err)
		}
		return current, nil
	}
	if err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	err = internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.HostDeployPolicy { return &metal3v1alpha1.HostDeployPolicy{} },
		func(object *metal3v1alpha1.HostDeployPolicy) error {
			object.Labels, object.Annotations = mergeMap(object.Labels, req.Labels), mergeMap(object.Annotations, req.Annotations)
			req.Spec.DeepCopyInto(&object.Spec)
			return nil
		})
	if err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	return s.GetHostDeployPolicy(ctx, key)
}

func (s *Service) GetHostDeployPolicy(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.HostDeployPolicy, error) {
	object := &metal3v1alpha1.HostDeployPolicy{}
	if err := s.reader.Get(ctx, key, object); err != nil {
		return nil, metal3sdk.KubernetesError("get-host-deploy-policy", key, err)
	}
	return object, nil
}

func (s *Service) ListHostDeployPolicies(ctx context.Context, opts metal3sdk.ResourceListOptions) ([]metal3v1alpha1.HostDeployPolicy, error) {
	list := &metal3v1alpha1.HostDeployPolicyList{}
	if err := s.reader.List(ctx, list, listOptions(opts)...); err != nil {
		return nil, metal3sdk.KubernetesError("list-host-deploy-policies", types.NamespacedName{Namespace: opts.Namespace}, err)
	}
	return list.Items, nil
}

func (s *Service) DeleteHostDeployPolicy(ctx context.Context, key types.NamespacedName) error {
	return deleteObject(ctx, s.kube, "delete-host-deploy-policy", key, &metal3v1alpha1.HostDeployPolicy{})
}

func (s *Service) ApplyHostUpdatePolicy(ctx context.Context, req metal3sdk.HostUpdatePolicyApplyRequest) (*metal3v1alpha1.HostUpdatePolicy, error) {
	const operationName = "apply-host-update-policy"
	if err := validateObjectKey(operationName, req.Namespace, req.Name); err != nil {
		return nil, err
	}
	if err := validateUpdatePolicy(req.Spec.FirmwareSettings); err != nil {
		return nil, err
	}
	if err := validateUpdatePolicy(req.Spec.FirmwareUpdates); err != nil {
		return nil, err
	}
	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}
	current := &metal3v1alpha1.HostUpdatePolicy{}
	err := s.reader.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		current.Namespace, current.Name = req.Namespace, req.Name
		current.Labels, current.Annotations = cloneMap(req.Labels), cloneMap(req.Annotations)
		current.Spec = req.Spec
		if err := s.kube.Create(ctx, current); err != nil {
			return nil, metal3sdk.KubernetesError(operationName, key, err)
		}
		return current, nil
	}
	if err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	err = internalpatch.MutateWithRetry(ctx, s.kube, key,
		func() *metal3v1alpha1.HostUpdatePolicy { return &metal3v1alpha1.HostUpdatePolicy{} },
		func(object *metal3v1alpha1.HostUpdatePolicy) error {
			object.Labels, object.Annotations = mergeMap(object.Labels, req.Labels), mergeMap(object.Annotations, req.Annotations)
			object.Spec = req.Spec
			return nil
		})
	if err != nil {
		return nil, metal3sdk.KubernetesError(operationName, key, err)
	}
	return s.GetHostUpdatePolicy(ctx, key)
}

func (s *Service) GetHostUpdatePolicy(ctx context.Context, key types.NamespacedName) (*metal3v1alpha1.HostUpdatePolicy, error) {
	object := &metal3v1alpha1.HostUpdatePolicy{}
	if err := s.reader.Get(ctx, key, object); err != nil {
		return nil, metal3sdk.KubernetesError("get-host-update-policy", key, err)
	}
	return object, nil
}

func (s *Service) ListHostUpdatePolicies(ctx context.Context, opts metal3sdk.ResourceListOptions) ([]metal3v1alpha1.HostUpdatePolicy, error) {
	list := &metal3v1alpha1.HostUpdatePolicyList{}
	if err := s.reader.List(ctx, list, listOptions(opts)...); err != nil {
		return nil, metal3sdk.KubernetesError("list-host-update-policies", types.NamespacedName{Namespace: opts.Namespace}, err)
	}
	return list.Items, nil
}

func (s *Service) DeleteHostUpdatePolicy(ctx context.Context, key types.NamespacedName) error {
	return deleteObject(ctx, s.kube, "delete-host-update-policy", key, &metal3v1alpha1.HostUpdatePolicy{})
}

func validateObjectKey(operationName, namespace, name string) error {
	if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
		return metal3sdk.ValidationError(operationName, fmt.Sprintf("invalid namespace %q", namespace))
	}
	if problems := validation.IsDNS1123Subdomain(name); len(problems) != 0 {
		return metal3sdk.ValidationError(operationName, fmt.Sprintf("invalid resource name %q", name))
	}
	return nil
}

func validateHTTPURL(operationName, field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return metal3sdk.ValidationError(operationName, fmt.Sprintf("%s must use http or https and include a host", field))
	}
	return nil
}

func validateUpdatePolicy(value metal3v1alpha1.UpdatePolicy) error {
	if value == "" || value == metal3v1alpha1.HostUpdatePolicyOnPreparing || value == metal3v1alpha1.HostUpdatePolicyOnReboot {
		return nil
	}
	return metal3sdk.ValidationError("apply-host-update-policy", "update policy must be onPreparing or onReboot")
}

func listOptions(opts metal3sdk.ResourceListOptions) []ctrlclient.ListOption {
	result := []ctrlclient.ListOption{ctrlclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(opts.Labels)}}
	if opts.Namespace != "" {
		result = append(result, ctrlclient.InNamespace(opts.Namespace))
	}
	return result
}

func deleteObject(ctx context.Context, kube ctrlclient.Client, operationName string, key types.NamespacedName, object ctrlclient.Object) error {
	object.SetNamespace(key.Namespace)
	object.SetName(key.Name)
	if err := kube.Delete(ctx, object); err != nil && !apierrors.IsNotFound(err) {
		return metal3sdk.KubernetesError(operationName, key, err)
	}
	return nil
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func mergeMap(current, updates map[string]string) map[string]string {
	if current == nil && updates != nil {
		current = map[string]string{}
	}
	for key, value := range updates {
		current[key] = value
	}
	return current
}
