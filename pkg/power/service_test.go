package power

import (
	"context"
	"testing"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func TestPowerOnAndRebootSubmitDesiredState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := powerTestHost(key, metal3v1alpha1.StateProvisioned)
	kube := fake.NewClientBuilder().WithScheme(powerTestScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)
	if _, err := service.PowerOn(ctx, key, metal3sdk.WaitOptions{}); err != nil {
		t.Fatalf("PowerOn() error = %v", err)
	}
	if _, err := service.Reboot(ctx, key, metal3sdk.RebootOptions{Mode: metal3sdk.RebootHard, Force: true}); err != nil {
		t.Fatalf("Reboot() error = %v", err)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if !current.Spec.Online {
		t.Fatal("spec.online was not set")
	}
	if current.Annotations[metal3v1alpha1.RebootAnnotationPrefix] != `{"mode":"hard","force":true}` {
		t.Fatalf("reboot annotation = %q", current.Annotations[metal3v1alpha1.RebootAnnotationPrefix])
	}
}

func TestPowerRejectsTransientState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	kube := fake.NewClientBuilder().WithScheme(powerTestScheme(t)).WithObjects(powerTestHost(key, metal3v1alpha1.StateProvisioning)).Build()
	_, err := New(kube, nil).PowerOff(ctx, key, metal3sdk.WaitOptions{})
	if !metal3sdk.IsCode(err, metal3sdk.CodeInvalidState) {
		t.Fatalf("PowerOff() error = %v", err)
	}
}

func TestRebootSupportsExternallyProvisionedHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "external-0"}
	kube := fake.NewClientBuilder().WithScheme(powerTestScheme(t)).WithObjects(powerTestHost(key, metal3v1alpha1.StateExternallyProvisioned)).Build()
	if _, err := New(kube, nil).Reboot(ctx, key, metal3sdk.RebootOptions{}); err != nil {
		t.Fatalf("Reboot() error = %v", err)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if _, exists := current.Annotations[metal3v1alpha1.RebootAnnotationPrefix]; !exists {
		t.Fatal("reboot annotation was not set")
	}
}

func TestTolerableRebootErrorMatchesOnlyBMOHardFallback(t *testing.T) {
	t.Parallel()
	host := &metal3v1alpha1.BareMetalHost{Status: metal3v1alpha1.BareMetalHostStatus{
		ErrorType:    metal3v1alpha1.PowerManagementError,
		ErrorMessage: softPoweroffFallbackMessage + "soft power off failed",
	}}
	if !tolerableRebootError(host) {
		t.Fatal("BMO soft-to-hard fallback was not tolerated")
	}
	host.Status.ErrorMessage = "hard power off failed"
	if tolerableRebootError(host) {
		t.Fatal("unrelated power management error was tolerated")
	}
	host.Status.ErrorMessage = softPoweroffFallbackMessage + "soft power off failed"
	host.Status.ErrorType = metal3v1alpha1.RegistrationError
	if tolerableRebootError(host) {
		t.Fatal("non-power error was tolerated")
	}
}

func TestPhasedRebootRemovesOnlyItsOwnHold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := powerTestHost(key, metal3v1alpha1.StateProvisioned)
	host.Spec.Online = true
	host.Status.PoweredOn = true
	kube := fake.NewClientBuilder().WithScheme(powerTestScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)

	started, err := service.StartPhasedReboot(ctx, key, metal3sdk.PhasedRebootOptions{Mode: metal3sdk.RebootHard})
	if err != nil {
		t.Fatalf("StartPhasedReboot() error = %v", err)
	}
	ownedAnnotation := metal3v1alpha1.RebootAnnotationPrefix + "/" + started.ID
	current := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations[ownedAnnotation] != `{"mode":"hard","force":false}` {
		t.Fatalf("phased reboot annotation = %q", current.Annotations[ownedAnnotation])
	}
	if _, err := service.CompletePhasedReboot(ctx, key, started.ID, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeInvalidState) {
		t.Fatalf("early CompletePhasedReboot() error = %v", err)
	}

	otherAnnotation := metal3v1alpha1.RebootAnnotationPrefix + "/other-client"
	current.Annotations[otherAnnotation] = ""
	current.Status.PoweredOn = false
	if err := kube.Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompletePhasedReboot(ctx, key, started.ID, metal3sdk.WaitOptions{}); err != nil {
		t.Fatalf("CompletePhasedReboot() error = %v", err)
	}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if _, exists := current.Annotations[ownedAnnotation]; exists {
		t.Fatal("owned phased reboot annotation was not removed")
	}
	if _, exists := current.Annotations[otherAnnotation]; !exists {
		t.Fatal("another client's phased reboot annotation was removed")
	}
}

func powerTestHost(key types.NamespacedName, state metal3v1alpha1.ProvisioningState) *metal3v1alpha1.BareMetalHost {
	return &metal3v1alpha1.BareMetalHost{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}, Status: metal3v1alpha1.BareMetalHostStatus{Provisioning: metal3v1alpha1.ProvisionStatus{State: state}}}
}

func powerTestScheme(t *testing.T) *runtime.Scheme {
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
