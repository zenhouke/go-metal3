package host

import (
	"context"
	"encoding/json"
	"testing"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalsecret "github.com/zenhouke/go-metal3/pkg/internal/secret"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func TestPhaseOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		host  *metal3v1alpha1.BareMetalHost
		phase metal3sdk.HostPhase
	}{
		{name: "available", host: hostInState(metal3v1alpha1.StateAvailable), phase: metal3sdk.HostAvailable},
		{name: "matching profile", host: hostInState(metal3v1alpha1.StateMatchProfile), phase: metal3sdk.HostMatchingProfile},
		{name: "provisioned", host: hostInState(metal3v1alpha1.StateProvisioned), phase: metal3sdk.HostProvisioned},
		{name: "servicing", host: &metal3v1alpha1.BareMetalHost{Status: metal3v1alpha1.BareMetalHostStatus{OperationalStatus: metal3v1alpha1.OperationalStatusServicing, Provisioning: metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateProvisioned}}}, phase: metal3sdk.HostServicing},
		{name: "detached", host: &metal3v1alpha1.BareMetalHost{Status: metal3v1alpha1.BareMetalHostStatus{OperationalStatus: metal3v1alpha1.OperationalStatusDetached}}, phase: metal3sdk.HostDetached},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := PhaseOf(test.host); got != test.phase {
				t.Fatalf("PhaseOf() = %q, want %q", got, test.phase)
			}
		})
	}
}

func TestAddCreatesBMHAndBMCSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).Build()
	service := New(kube, nil)
	req := metal3sdk.HostCreateRequest{Namespace: "metal3", Name: "worker-0", BMCAddress: "redfish+https://192.0.2.10/redfish/v1/Systems/1", BMCUsername: "admin", BMCPassword: []byte("secret"), BootMACAddress: "00:11:22:33:44:55", InspectionDisabled: true, Online: true, DisablePowerOff: true}
	host, err := service.Add(ctx, req)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if host.Spec.BMC.CredentialsName != "worker-0-bmc" {
		t.Fatalf("credentials name = %q", host.Spec.BMC.CredentialsName)
	}
	if host.Spec.InspectionMode != metal3v1alpha1.InspectionModeDisabled || !host.Spec.DisablePowerOff {
		t.Fatalf("new v0.13 host fields were not mapped: %#v", host.Spec)
	}
	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-0-bmc"}, secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["username"]) != "admin" || string(secret.Data["password"]) != "secret" {
		t.Fatal("BMC Secret data mismatch")
	}
}

func TestImportCreatesHostWithStatusAnnotationAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).Build()
	service := New(kube, nil)
	desiredStatus := metal3v1alpha1.BareMetalHostStatus{
		OperationalStatus: metal3v1alpha1.OperationalStatusDetached,
		PoweredOn:         true,
		Provisioning:      metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateProvisioned, ID: "ironic-node-id"},
	}
	host, err := service.Import(ctx, metal3sdk.HostImportRequest{
		Host: metal3sdk.HostCreateRequest{
			Namespace: "metal3", Name: "worker-0", BMCAddress: "ipmi://192.0.2.10",
			BMCUsername: "admin", BMCPassword: []byte("secret"), BootMACAddress: "00:11:22:33:44:55", InspectionDisabled: true,
		},
		Status: desiredStatus,
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	raw, exists := host.Annotations[metal3v1alpha1.StatusAnnotation]
	if !exists {
		t.Fatal("status reconstruction annotation is missing")
	}
	var decoded metal3v1alpha1.BareMetalHostStatus
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode status annotation: %v", err)
	}
	if decoded.Provisioning.State != metal3v1alpha1.StateProvisioned || decoded.Provisioning.ID != "ironic-node-id" || !decoded.PoweredOn {
		t.Fatalf("status annotation = %#v", decoded)
	}
	if value, exists := host.Annotations[metal3v1alpha1.DetachedAnnotation]; !exists || value != "" {
		t.Fatalf("detached import annotation = %q, exists=%t", value, exists)
	}
	if _, err := service.Import(ctx, metal3sdk.HostImportRequest{
		Host:   metal3sdk.HostCreateRequest{Namespace: "metal3", Name: "worker-1", BMCAddress: "ipmi://192.0.2.11", BMCUsername: "admin", BMCPassword: []byte("secret")},
		Status: metal3v1alpha1.BareMetalHostStatus{Provisioning: metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateInspecting}},
	}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("transitional status error = %v", err)
	}
	var deprecatedFirmwareStatus metal3v1alpha1.BareMetalHostStatus
	if err := json.Unmarshal([]byte(`{"operationalStatus":"OK","errorMessage":"","poweredOn":false,"errorCount":0,"provisioning":{"state":"available","firmware":{}}}`), &deprecatedFirmwareStatus); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Import(ctx, metal3sdk.HostImportRequest{
		Host:   metal3sdk.HostCreateRequest{Namespace: "metal3", Name: "worker-2", BMCAddress: "ipmi://192.0.2.12", BMCUsername: "admin", BMCPassword: []byte("secret")},
		Status: deprecatedFirmwareStatus,
	}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("deprecated firmware status error = %v", err)
	}
}

func TestGenericAnnotationsCannotTriggerMetal3ControlOperations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).Build()
	_, err := New(kube, nil).Add(ctx, metal3sdk.HostCreateRequest{
		Namespace: "metal3", Name: "worker-0", BMCAddress: "ipmi://192.0.2.10",
		BMCUsername: "admin", BMCPassword: []byte("secret"),
		Annotations: map[string]string{metal3v1alpha1.DetachedAnnotation: ""},
	})
	if !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("reserved annotation error = %v", err)
	}
}

func TestGenericAnnotationsAllowNonControlMetal3Prefix(t *testing.T) {
	t.Parallel()
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).Build()
	host, err := New(kube, nil).Add(context.Background(), metal3sdk.HostCreateRequest{
		Namespace: "metal3", Name: "worker-meta", BMCAddress: "ipmi://192.0.2.11",
		BMCUsername: "admin", BMCPassword: []byte("secret"), BootMACAddress: "00:11:22:33:44:55",
		Annotations: map[string]string{"baremetalhost.metal3.io/inventory-id": "rack-42"},
	})
	if err != nil {
		t.Fatalf("non-control annotation rejected: %v", err)
	}
	if got := host.Annotations["baremetalhost.metal3.io/inventory-id"]; got != "rack-42" {
		t.Fatalf("annotation = %q, want rack-42", got)
	}
}

func TestAddRejectsBMOAdmissionConflictsBeforeCreatingSecret(t *testing.T) {
	t.Parallel()
	base := metal3sdk.HostCreateRequest{
		Namespace: "metal3", Name: "worker-0", BMCAddress: "redfish+https://192.0.2.10/redfish/v1/Systems/1",
		BMCUsername: "admin", BMCPassword: []byte("secret"),
	}
	tests := []struct {
		name   string
		mutate func(*metal3sdk.HostCreateRequest)
	}{
		{name: "driver requires boot MAC"},
		{name: "disabled inspection requires boot MAC for virtual media", mutate: func(req *metal3sdk.HostCreateRequest) {
			req.BMCAddress = "redfish-virtualmedia+https://192.0.2.10/redfish/v1/Systems/1"
			req.InspectionDisabled = true
		}},
		{name: "disable power off requires online", mutate: func(req *metal3sdk.HostCreateRequest) {
			req.BootMACAddress = "00:11:22:33:44:55"
			req.DisablePowerOff = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := base
			if test.mutate != nil {
				test.mutate(&req)
			}
			kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).Build()
			if _, err := New(kube, nil).Add(context.Background(), req); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
				t.Fatalf("Add() error = %v", err)
			}
			err := kube.Get(context.Background(), types.NamespacedName{Namespace: req.Namespace, Name: req.Name + "-bmc"}, &corev1.Secret{})
			if err == nil {
				t.Fatal("validation failure left a BMC Secret")
			} else if !apierrors.IsNotFound(err) {
				t.Fatalf("unexpected Secret lookup error: %v", err)
			}
		})
	}
}

func TestAddExistingHostDoesNotOverwriteCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := &metal3v1alpha1.BareMetalHost{ObjectMeta: metav1.ObjectMeta{Namespace: "metal3", Name: "worker-0"}}
	secret := internalsecret.BMC("metal3", "worker-0-bmc", "original", []byte("original"))
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(host, secret).Build()
	service := New(kube, nil)
	_, err := service.Add(ctx, metal3sdk.HostCreateRequest{Namespace: "metal3", Name: "worker-0", BMCAddress: "ipmi://192.0.2.10", BMCUsername: "changed", BMCPassword: []byte("changed"), BootMACAddress: "00:11:22:33:44:55"})
	if !metal3sdk.IsCode(err, metal3sdk.CodeConflict) {
		t.Fatalf("Add() error = %v", err)
	}
	current := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-0-bmc"}, current); err != nil {
		t.Fatal(err)
	}
	if string(current.Data["username"]) != "original" {
		t.Fatal("existing credentials were overwritten")
	}
}

func TestAddDoesNotTakeOverUnmanagedBMCSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "metal3", Name: "worker-0-bmc"}, Data: map[string][]byte{"username": []byte("external"), "password": []byte("external")}}
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(secret).Build()
	service := New(kube, nil)
	_, err := service.Add(ctx, metal3sdk.HostCreateRequest{Namespace: "metal3", Name: "worker-0", BMCAddress: "ipmi://192.0.2.10", BMCUsername: "changed", BMCPassword: []byte("changed"), BootMACAddress: "00:11:22:33:44:55"})
	if err == nil {
		t.Fatal("Add() unexpectedly took over an unmanaged Secret")
	}
	current := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-0-bmc"}, current); err != nil {
		t.Fatal(err)
	}
	if string(current.Data["username"]) != "external" || current.Labels[internalsecret.ManagedByLabel] != "" {
		t.Fatalf("unmanaged Secret changed: %#v", current)
	}
}

func TestInventoryOnlyDeleteDetachesFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := &metal3v1alpha1.BareMetalHost{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}, Status: metal3v1alpha1.BareMetalHostStatus{Provisioning: metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateProvisioned}}}
	base := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(host).Build()
	kube := &detachTransitionClient{Client: base}
	service := New(kube, kube)
	op, err := service.Delete(ctx, key, metal3sdk.DeleteOptions{Mode: metal3sdk.DeleteInventoryOnly, Wait: true})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationSucceeded {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	if err := base.Get(ctx, key, &metal3v1alpha1.BareMetalHost{}); err == nil {
		t.Fatal("host still exists")
	}
}

func TestUpdateBMCSafelyDetachesAndReattaches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := &metal3v1alpha1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Spec:       metal3v1alpha1.BareMetalHostSpec{BMC: metal3v1alpha1.BMCDetails{Address: "ipmi://192.0.2.10", CredentialsName: "worker-0-bmc"}},
		Status:     metal3v1alpha1.BareMetalHostStatus{OperationalStatus: metal3v1alpha1.OperationalStatusOK, Provisioning: metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateProvisioned}},
	}
	secret := internalsecret.BMC("metal3", "worker-0-bmc", "old", []byte("old"))
	base := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(host, secret).Build()
	kube := &detachTransitionClient{Client: base}
	op, err := New(kube, kube).UpdateBMC(ctx, key, metal3sdk.BMCUpdateRequest{Address: "ipmi://192.0.2.20", Username: "new", Password: []byte("new"), Wait: true})
	if err != nil {
		t.Fatalf("UpdateBMC() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationSucceeded {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := base.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.BMC.Address != "ipmi://192.0.2.20" {
		t.Fatalf("BMC address = %q", current.Spec.BMC.Address)
	}
	if _, exists := current.Annotations[metal3v1alpha1.DetachedAnnotation]; exists {
		t.Fatal("SDK detach annotation was not removed")
	}
	credentials := &corev1.Secret{}
	if err := base.Get(ctx, types.NamespacedName{Namespace: "metal3", Name: "worker-0-bmc"}, credentials); err != nil {
		t.Fatal(err)
	}
	if string(credentials.Data["username"]) != "new" {
		t.Fatal("BMC credentials were not rotated")
	}
}

func TestUpdateBMCRejectsMissingCredentialsBeforeDetach(t *testing.T) {
	t.Parallel()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-no-secret"}
	host := &metal3v1alpha1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Spec:       metal3v1alpha1.BareMetalHostSpec{BMC: metal3v1alpha1.BMCDetails{Address: "ipmi://192.0.2.30"}},
		Status:     metal3v1alpha1.BareMetalHostStatus{OperationalStatus: metal3v1alpha1.OperationalStatusOK, Provisioning: metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateProvisioned}},
	}
	base := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(host).Build()
	kube := &detachTransitionClient{Client: base}
	_, err := New(kube, kube).UpdateBMC(context.Background(), key, metal3sdk.BMCUpdateRequest{Username: "new", Password: []byte("new")})
	if !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("UpdateBMC() error = %v", err)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := base.Get(context.Background(), key, current); err != nil {
		t.Fatal(err)
	}
	if _, detached := current.Annotations[metal3v1alpha1.DetachedAnnotation]; detached {
		t.Fatal("validation failure left host detached")
	}
}

func TestDetachAndAttach(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := &metal3v1alpha1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Status:     metal3v1alpha1.BareMetalHostStatus{OperationalStatus: metal3v1alpha1.OperationalStatusOK, Provisioning: metal3v1alpha1.ProvisionStatus{State: metal3v1alpha1.StateProvisioned}},
	}
	base := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(host).Build()
	kube := &detachTransitionClient{Client: base}
	service := New(kube, kube)
	if op, err := service.Detach(ctx, key, metal3sdk.DetachOptions{Wait: true}); err != nil || op.Phase != metal3sdk.OperationSucceeded {
		t.Fatalf("Detach() = %#v, %v", op, err)
	}
	if op, err := service.Attach(ctx, key, metal3sdk.WaitOptions{Wait: true}); err != nil || op.Phase != metal3sdk.OperationSucceeded {
		t.Fatalf("Attach() = %#v, %v", op, err)
	}
}

func TestAdoptExternallyProvisionedIsOneWayFromAvailable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := hostInState(metal3v1alpha1.StateAvailable)
	host.Namespace, host.Name = key.Namespace, key.Name
	kube := fake.NewClientBuilder().WithScheme(hostTestScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)
	op, err := service.AdoptExternallyProvisioned(ctx, key, metal3sdk.WaitOptions{})
	if err != nil {
		t.Fatalf("AdoptExternallyProvisioned() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if !current.Spec.ExternallyProvisioned {
		t.Fatal("spec.externallyProvisioned was not set")
	}

	provisionedKey := types.NamespacedName{Namespace: "metal3", Name: "worker-1"}
	provisioned := hostInState(metal3v1alpha1.StateProvisioned)
	provisioned.Namespace, provisioned.Name = provisionedKey.Namespace, provisionedKey.Name
	if err := kube.Create(ctx, provisioned); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdoptExternallyProvisioned(ctx, provisionedKey, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeInvalidState) {
		t.Fatalf("provisioned adoption error = %v", err)
	}
}

type detachTransitionClient struct{ ctrlclient.Client }

func (c *detachTransitionClient) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if host, ok := obj.(*metal3v1alpha1.BareMetalHost); ok {
		if _, detached := host.Annotations[metal3v1alpha1.DetachedAnnotation]; detached {
			host.Status.OperationalStatus = metal3v1alpha1.OperationalStatusDetached
		} else if host.Status.Provisioning.State == metal3v1alpha1.StateProvisioned {
			host.Status.OperationalStatus = metal3v1alpha1.OperationalStatusOK
		}
	}
	return nil
}

func hostTestScheme(t *testing.T) *runtime.Scheme {
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

func hostInState(state metal3v1alpha1.ProvisioningState) *metal3v1alpha1.BareMetalHost {
	return &metal3v1alpha1.BareMetalHost{
		Status: metal3v1alpha1.BareMetalHostStatus{
			Provisioning: metal3v1alpha1.ProvisionStatus{State: state},
		},
	}
}
