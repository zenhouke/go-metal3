package provision

import (
	"context"
	"testing"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func TestInstallMaterializesConfigAndPatchesHostAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := testHost(key, metal3v1alpha1.StateAvailable)
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)
	format := "qcow2"
	req := metal3sdk.InstallRequest{
		ImageURL: "https://images.example/os.qcow2", Checksum: "abc", ChecksumType: metal3v1alpha1.SHA256,
		DiskFormat: &format, UserData: []byte("#cloud-config\n"), NetworkData: []byte(`{"links":[]}`), MetaData: []byte(`{"hostname":"worker-0"}`), PowerOn: true,
	}
	op, err := service.Install(ctx, key, req)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}

	updated := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.Image == nil || updated.Spec.Image.URL != req.ImageURL || !updated.Spec.Online {
		t.Fatalf("unexpected host spec: %#v", updated.Spec)
	}
	assertSecretRef(t, ctx, kube, updated.Spec.UserData, "userData", req.UserData)
	assertSecretRef(t, ctx, kube, updated.Spec.NetworkData, "networkData", req.NetworkData)
	assertSecretRef(t, ctx, kube, updated.Spec.MetaData, "metaData", req.MetaData)
}

func TestInstallValidation(t *testing.T) {
	t.Parallel()
	format := "live-iso"
	tests := []struct {
		name string
		req  metal3sdk.InstallRequest
	}{
		{name: "missing checksum", req: metal3sdk.InstallRequest{ImageURL: "https://images.example/os.qcow2"}},
		{name: "md5", req: metal3sdk.InstallRequest{ImageURL: "https://images.example/os.qcow2", Checksum: "abc", ChecksumType: metal3v1alpha1.MD5}},
		{name: "live iso config", req: metal3sdk.InstallRequest{ImageURL: "https://images.example/os.iso", DiskFormat: &format, UserData: []byte("data")}},
		{name: "oci checksum", req: metal3sdk.InstallRequest{ImageURL: "oci://registry.example/os:1", Checksum: "abc"}},
		{name: "auth with http image", req: metal3sdk.InstallRequest{ImageURL: "https://images.example/os.qcow2", Checksum: "abc", OCIAuthSecretName: "registry-auth"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateInstall(test.req); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
				t.Fatalf("validateInstall() error = %v", err)
			}
		})
	}
}

func TestInstallOCIImageReferencesExistingRegistrySecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := testHost(key, metal3v1alpha1.StateAvailable)
	registrySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: "registry-auth"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
	}
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(host, registrySecret).Build()
	service := New(kube, nil)
	req := metal3sdk.InstallRequest{ImageURL: "oci://registry.example/os:1", OCIAuthSecretName: registrySecret.Name, PowerOn: true}
	if _, err := service.Install(ctx, key, req); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	updated := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.Image == nil || updated.Spec.Image.OCIAuthSecretName == nil || *updated.Spec.Image.OCIAuthSecretName != registrySecret.Name {
		t.Fatalf("OCI auth Secret was not referenced: %#v", updated.Spec.Image)
	}

	missingHost := testHost(types.NamespacedName{Namespace: key.Namespace, Name: "worker-1"}, metal3v1alpha1.StateAvailable)
	if err := kube.Create(ctx, missingHost); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(ctx, types.NamespacedName{Namespace: key.Namespace, Name: missingHost.Name}, metal3sdk.InstallRequest{ImageURL: "oci://registry.example/os:1", OCIAuthSecretName: "missing"}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("missing OCI auth Secret error = %v", err)
	}
}

func TestCustomDeployIsMutuallyExclusiveWithImage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := testHost(key, metal3v1alpha1.StateAvailable)
	host.Spec.Image = &metal3v1alpha1.Image{URL: "https://images.example/old.qcow2"}
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(host).Build()
	service := New(kube, nil)
	op, err := service.CustomDeploy(ctx, key, metal3sdk.CustomDeployRequest{Method: "site_deploy", UserData: []byte("config"), PowerOn: true})
	if err != nil {
		t.Fatalf("CustomDeploy() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	updated := &metal3v1alpha1.BareMetalHost{}
	if err := kube.Get(ctx, key, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.Image != nil || updated.Spec.CustomDeploy == nil || updated.Spec.CustomDeploy.Method != "site_deploy" || updated.Spec.UserData == nil {
		t.Fatalf("unexpected custom deployment spec: %#v", updated.Spec)
	}
	if _, err := service.CustomDeploy(ctx, key, metal3sdk.CustomDeployRequest{Method: "bad method"}); !metal3sdk.IsCode(err, metal3sdk.CodeValidation) {
		t.Fatalf("invalid custom deploy method error = %v", err)
	}
}

func TestReinstallDeprovisionsBeforeSubmitting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	host := testHost(key, metal3v1alpha1.StateProvisioned)
	host.Spec.Image = &metal3v1alpha1.Image{URL: "https://images.example/os.qcow2"}
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(host).Build()
	kube := &deprovisionTransitionClient{Client: base}
	service := New(kube, kube)
	req := metal3sdk.InstallRequest{ImageURL: "https://images.example/os.qcow2", Checksum: "abc", ChecksumType: metal3v1alpha1.SHA256}
	op, err := service.Reinstall(ctx, key, req)
	if err != nil {
		t.Fatalf("Reinstall() error = %v", err)
	}
	if op.Phase != metal3sdk.OperationRunning {
		t.Fatalf("operation phase = %q", op.Phase)
	}
	updated := &metal3v1alpha1.BareMetalHost{}
	if err := base.Get(ctx, key, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.Image == nil || updated.Spec.Image.URL != req.ImageURL {
		t.Fatalf("image was not resubmitted: %#v", updated.Spec.Image)
	}
}

type deprovisionTransitionClient struct{ ctrlclient.Client }

func (c *deprovisionTransitionClient) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if host, ok := obj.(*metal3v1alpha1.BareMetalHost); ok && host.Spec.Image == nil {
		host.Status.Provisioning.State = metal3v1alpha1.StateAvailable
	}
	return nil
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

func testHost(key types.NamespacedName, state metal3v1alpha1.ProvisioningState) *metal3v1alpha1.BareMetalHost {
	return &metal3v1alpha1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name, UID: types.UID("host-uid")},
		Status:     metal3v1alpha1.BareMetalHostStatus{Provisioning: metal3v1alpha1.ProvisionStatus{State: state}},
	}
}

func assertSecretRef(t *testing.T, ctx context.Context, kube ctrlclient.Client, ref *corev1.SecretReference, dataKey string, want []byte) {
	t.Helper()
	if ref == nil {
		t.Fatalf("%s reference is nil", dataKey)
	}
	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		t.Fatal(err)
	}
	if secret.Immutable == nil || !*secret.Immutable {
		t.Fatalf("%s Secret is not immutable", dataKey)
	}
	if string(secret.Data[dataKey]) != string(want) {
		t.Fatalf("%s data = %q, want %q", dataKey, secret.Data[dataKey], want)
	}
}
