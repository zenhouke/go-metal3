package resources

import (
	"context"
	"testing"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func TestAuxiliaryResourceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	service := New(kube, nil)

	subscription, err := service.CreateBMCEventSubscription(ctx, metal3sdk.BMCEventSubscriptionCreateRequest{
		Namespace: "metal3", Name: "events", HostName: "worker-0",
		Destination:    "https://events.example/metal3",
		HTTPHeadersRef: &corev1.SecretReference{Name: "event-headers"},
	})
	if err != nil {
		t.Fatalf("CreateBMCEventSubscription() error = %v", err)
	}
	if subscription.Spec.HTTPHeadersRef == nil || subscription.Spec.HTTPHeadersRef.Namespace != "metal3" {
		t.Fatalf("headers Secret namespace was not defaulted: %#v", subscription.Spec.HTTPHeadersRef)
	}

	dataImage, err := service.ApplyDataImage(ctx, metal3sdk.DataImageApplyRequest{Namespace: "metal3", Name: "worker-0", URL: "https://images.example/config.iso"})
	if err != nil || dataImage.Spec.URL == "" {
		t.Fatalf("ApplyDataImage() = %#v, %v", dataImage, err)
	}
	dataImage, err = service.ApplyDataImage(ctx, metal3sdk.DataImageApplyRequest{Namespace: "metal3", Name: "worker-0", URL: "https://images.example/new.iso"})
	if err != nil || dataImage.Spec.URL != "https://images.example/new.iso" {
		t.Fatalf("updated ApplyDataImage() = %#v, %v", dataImage, err)
	}

	claim, err := service.CreateHostClaim(ctx, metal3sdk.HostClaimCreateRequest{
		Namespace: "tenant-a", Name: "claim-0",
		Spec: metal3v1alpha1.HostClaimSpec{PoweredOn: true, HostSelector: metal3v1alpha1.HostSelector{InNamespace: "metal3", MatchLabels: map[string]string{"rack": "a"}}},
	})
	if err != nil || !claim.Spec.PoweredOn {
		t.Fatalf("CreateHostClaim() = %#v, %v", claim, err)
	}

	deployPolicy, err := service.ApplyHostDeployPolicy(ctx, metal3sdk.HostDeployPolicyApplyRequest{
		Namespace: "metal3", Name: "claims",
		Spec: metal3v1alpha1.HostDeployPolicySpec{HostClaimNamespaces: &metal3v1alpha1.HostClaimNamespaces{Names: []string{"tenant-a"}}},
	})
	if err != nil || deployPolicy.Spec.HostClaimNamespaces == nil {
		t.Fatalf("ApplyHostDeployPolicy() = %#v, %v", deployPolicy, err)
	}

	updatePolicy, err := service.ApplyHostUpdatePolicy(ctx, metal3sdk.HostUpdatePolicyApplyRequest{
		Namespace: "metal3", Name: "worker-0",
		Spec: metal3v1alpha1.HostUpdatePolicySpec{FirmwareSettings: metal3v1alpha1.HostUpdatePolicyOnReboot, FirmwareUpdates: metal3v1alpha1.HostUpdatePolicyOnReboot},
	})
	if err != nil || updatePolicy.Spec.FirmwareUpdates != metal3v1alpha1.HostUpdatePolicyOnReboot {
		t.Fatalf("ApplyHostUpdatePolicy() = %#v, %v", updatePolicy, err)
	}

	images, err := service.ListDataImages(ctx, metal3sdk.ResourceListOptions{Namespace: "metal3"})
	if err != nil || len(images) != 1 {
		t.Fatalf("ListDataImages() = %#v, %v", images, err)
	}
	if err := service.DeleteDataImage(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-0"}); err != nil {
		t.Fatalf("DeleteDataImage() error = %v", err)
	}
	if err := service.DeleteDataImage(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-0"}); err != nil {
		t.Fatalf("idempotent DeleteDataImage() error = %v", err)
	}
}

func TestAuxiliaryResourceValidation(t *testing.T) {
	t.Parallel()
	service := New(fake.NewClientBuilder().WithScheme(testScheme(t)).Build(), nil)
	ctx := context.Background()
	if _, err := service.CreateBMCEventSubscription(ctx, metal3sdk.BMCEventSubscriptionCreateRequest{Namespace: "metal3", Name: "events", HostName: "worker-0", Destination: "file:///tmp/events"}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("destination error = %v", err)
	}
	if _, err := service.CreateHostClaim(ctx, metal3sdk.HostClaimCreateRequest{Namespace: "metal3", Name: "claim", Spec: metal3v1alpha1.HostClaimSpec{Image: &metal3v1alpha1.Image{URL: "https://images.example/os"}, CustomDeploy: &metal3v1alpha1.CustomDeploy{Method: "custom"}}}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("claim image/customDeploy error = %v", err)
	}
	if _, err := service.ApplyHostDeployPolicy(ctx, metal3sdk.HostDeployPolicyApplyRequest{Namespace: "metal3", Name: "policy", Spec: metal3v1alpha1.HostDeployPolicySpec{HostClaimNamespaces: &metal3v1alpha1.HostClaimNamespaces{NameMatches: "["}}}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("policy regex error = %v", err)
	}
	if _, err := service.ApplyHostUpdatePolicy(ctx, metal3sdk.HostUpdatePolicyApplyRequest{Namespace: "metal3", Name: "worker-0", Spec: metal3v1alpha1.HostUpdatePolicySpec{FirmwareUpdates: "always"}}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("update policy error = %v", err)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := metal3v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}
