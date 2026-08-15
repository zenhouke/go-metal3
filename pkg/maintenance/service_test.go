package maintenance

import (
	"context"
	"strings"
	"testing"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	internalsecret "github.com/zenhouke/go-metal3/pkg/internal/secret"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func TestInspectRequiresAvailableHostAndEnabledInspection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	disabled := maintenanceHost(key, metal3v1alpha1.StateAvailable)
	disabled.Spec.InspectionMode = metal3v1alpha1.InspectionModeDisabled
	service := New(fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(disabled).Build(), nil)
	if _, err := service.Inspect(ctx, key, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeInvalidState) {
		t.Fatalf("Inspect() disabled error = %v", err)
	}

	provisioned := maintenanceHost(key, metal3v1alpha1.StateProvisioned)
	service = New(fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(provisioned).Build(), nil)
	if _, err := service.Inspect(ctx, key, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeInvalidState) {
		t.Fatalf("Inspect() provisioned error = %v", err)
	}
}

func TestExternalInspectionDataUsesDedicatedAnnotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := maintenanceHost(key, metal3v1alpha1.StateAvailable)
	host.Spec.InspectionMode = metal3v1alpha1.InspectionModeDisabled
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)
	details := &metal3v1alpha1.HardwareDetails{
		RAMMebibytes: 32768,
		CPU:          metal3v1alpha1.CPU{Arch: "x86_64", Count: 16},
		NIC:          []metal3v1alpha1.NIC{{Name: "eno1", MAC: "00:11:22:33:44:55", IP: "192.0.2.20", VLANID: 100}},
		Storage:      []metal3v1alpha1.Storage{{Name: "/dev/disk/by-id/disk-0", Type: metal3v1alpha1.SSD, SizeBytes: 500 * metal3v1alpha1.GigaByte}},
	}
	op, err := service.SetExternalInspectionData(ctx, key, details, metal3sdk.WaitOptions{})
	if err != nil {
		t.Fatalf("SetExternalInspectionData() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.Annotations[metal3v1alpha1.HardwareDetailsAnnotation], `"ramMebibytes":32768`) {
		t.Fatalf("hardware details annotation = %q", current.Annotations[metal3v1alpha1.HardwareDetailsAnnotation])
	}

	invalid := details.DeepCopy()
	invalid.NIC[0].VLANID = 4095
	if _, err := service.SetExternalInspectionData(ctx, key, invalid, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("invalid external hardware error = %v", err)
	}
}

func TestInspectionModeAndRAIDMutateHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(maintenanceHost(key, metal3v1alpha1.StateAvailable)).Build()
	service := New(kube, nil)
	if _, err := service.SetInspectionMode(ctx, key, metal3sdk.InspectionDisabled); err != nil {
		t.Fatal(err)
	}
	size := 100
	desired := &metal3v1alpha1.RAIDConfig{HardwareRAIDVolumes: []metal3v1alpha1.HardwareRAIDVolume{{Level: "1", SizeGibibytes: &size}}}
	op, err := service.ConfigureRAID(ctx, key, desired, metal3sdk.WaitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	current := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.InspectionMode != metal3v1alpha1.InspectionModeDisabled || current.Spec.RAID == nil || len(current.Spec.RAID.HardwareRAIDVolumes) != 1 {
		t.Fatalf("unexpected host: %#v", current)
	}
}

func TestInspectionModeRejectsActiveInspection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(maintenanceHost(key, metal3v1alpha1.StateInspecting)).Build()
	_, err := New(kube, nil).SetInspectionMode(ctx, key, metal3sdk.InspectionDisabled)
	if !metal3sdk.IsCode(err, metal3sdk.CodeInvalidState) {
		t.Fatalf("SetInspectionMode() error = %v", err)
	}
}

func TestRAIDRequiresExplicitTypeAndValidSoftwareLayout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(maintenanceHost(key, metal3v1alpha1.StateAvailable)).Build()
	service := New(kube, nil)
	if _, err := service.ConfigureRAID(ctx, key, &metal3v1alpha1.RAIDConfig{}, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("ambiguous RAID error = %v", err)
	}
	invalid := &metal3v1alpha1.RAIDConfig{SoftwareRAIDVolumes: []metal3v1alpha1.SoftwareRAIDVolume{{Level: "0"}}}
	if _, err := service.ConfigureRAID(ctx, key, invalid, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("invalid software RAID error = %v", err)
	}
	remove := &metal3v1alpha1.RAIDConfig{HardwareRAIDVolumes: []metal3v1alpha1.HardwareRAIDVolume{}}
	if _, err := service.ConfigureRAID(ctx, key, remove, metal3sdk.WaitOptions{}); err != nil {
		t.Fatalf("explicit hardware RAID removal error = %v", err)
	}
}

func TestFirmwareSettingsAndNetworkData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := maintenanceHost(key, metal3v1alpha1.StateAvailable)
	host.UID = types.UID("host-uid")
	settings := &metal3v1alpha1.HostFirmwareSettings{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(host, settings).Build()
	service := New(kube, nil)

	desired := metal3v1alpha1.DesiredSettingsMap{"BootMode": intstr.FromString("Uefi"), "ProcCores": intstr.FromInt(16)}
	if _, err := service.UpdateFirmwareSettings(ctx, key, desired, metal3sdk.WaitOptions{}); err != nil {
		t.Fatal(err)
	}
	currentSettings := &metal3v1alpha1.HostFirmwareSettings{}
	if err := kube.Get(ctx, key, currentSettings); err != nil {
		t.Fatal(err)
	}
	bootMode := currentSettings.Spec.Settings["BootMode"]
	procCores := currentSettings.Spec.Settings["ProcCores"]
	if bootMode.String() != "Uefi" || procCores.IntValue() != 16 {
		t.Fatalf("unexpected settings: %#v", currentSettings.Spec.Settings)
	}

	networkData := []byte(`links:\n- id: provisioning`)
	currentHost, err := service.SetPreprovisioningNetworkData(ctx, key, networkData)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(currentHost.Spec.PreprovisioningNetworkDataName, "worker-0-preprovisioning-networkdata-") {
		t.Fatalf("network data secret = %q", currentHost.Spec.PreprovisioningNetworkDataName)
	}
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: key.Namespace, Name: currentHost.Spec.PreprovisioningNetworkDataName}
	if err := kube.Get(ctx, secretKey, secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["networkData"]) != string(networkData) || secret.Labels[internalsecret.ManagedByLabel] != internalsecret.ManagedByValue || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != host.UID {
		t.Fatalf("unexpected managed Secret: %#v", secret)
	}
}

func TestFirmwareComponentsCreateAndValidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := maintenanceHost(key, metal3v1alpha1.StateAvailable)
	host.UID = types.UID("host-uid")
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)

	updates := []metal3v1alpha1.FirmwareUpdate{{Component: "bios", URL: "https://firmware.example/bios.bin"}, {Component: "nic:eth0", URL: "https://firmware.example/nic.bin"}}
	op, err := service.UpdateFirmwareComponents(ctx, key, updates, metal3sdk.WaitOptions{})
	if err != nil {
		t.Fatalf("UpdateFirmwareComponents() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	current := &metal3v1alpha1.HostFirmwareComponents{}
	if err := kube.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if !firmwareUpdatesEqual(current.Spec.Updates, updates) || len(current.OwnerReferences) != 1 || current.OwnerReferences[0].UID != host.UID {
		t.Fatalf("unexpected firmware components: %#v", current)
	}

	invalid := []metal3v1alpha1.FirmwareUpdate{{Component: "bios", URL: "https://firmware.example/one"}, {Component: "bios", URL: "https://firmware.example/two"}}
	if _, err := service.UpdateFirmwareComponents(ctx, key, invalid, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("duplicate component error = %v", err)
	}
	if _, err := service.UpdateFirmwareComponents(ctx, key, []metal3v1alpha1.FirmwareUpdate{{Component: "disk", URL: "file:///tmp/fw"}}, metal3sdk.WaitOptions{}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("invalid component error = %v", err)
	}
}

func TestProvisionedFirmwareUpdatesRequireSubmitThenReboot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := maintenanceHost(key, metal3v1alpha1.StateProvisioned)
	settings := &metal3v1alpha1.HostFirmwareSettings{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
	components := &metal3v1alpha1.HostFirmwareComponents{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
	kube := fake.NewClientBuilder().WithScheme(maintenanceScheme(t)).WithObjects(host, settings, components).Build()
	service := New(kube, nil)

	desiredSettings := metal3v1alpha1.DesiredSettingsMap{"QuietBoot": intstr.FromString("true")}
	if _, err := service.UpdateFirmwareSettings(ctx, key, desiredSettings, metal3sdk.WaitOptions{Wait: true}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("waiting live firmware settings error = %v", err)
	}
	if _, err := service.UpdateFirmwareSettings(ctx, key, desiredSettings, metal3sdk.WaitOptions{}); err != nil {
		t.Fatalf("submitting live firmware settings error = %v", err)
	}
	desiredComponents := []metal3v1alpha1.FirmwareUpdate{{Component: "bios", URL: "https://firmware.example/bios.bin"}}
	if _, err := service.UpdateFirmwareComponents(ctx, key, desiredComponents, metal3sdk.WaitOptions{Wait: true}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("waiting live firmware components error = %v", err)
	}
	if _, err := service.UpdateFirmwareComponents(ctx, key, desiredComponents, metal3sdk.WaitOptions{}); err != nil {
		t.Fatalf("submitting live firmware components error = %v", err)
	}
}

func maintenanceHost(key types.NamespacedName, state metal3v1alpha1.ProvisioningState) *metal3v1alpha1.BareMetalHost {
	return &metal3v1alpha1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Status:     metal3v1alpha1.BareMetalHostStatus{Provisioning: metal3v1alpha1.ProvisionStatus{State: state}},
	}
}

func maintenanceScheme(t *testing.T) *runtime.Scheme {
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
