package cluster

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestInfoReportsMetal3Resources(t *testing.T) {
	t.Parallel()
	discovery := &discoveryfake.FakeDiscovery{
		Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{{
			GroupVersion: "metal3.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "baremetalhosts", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "patch"}},
				{Name: "bmceventsubscriptions", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "delete"}},
				{Name: "dataimages", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "patch", "delete"}},
				{Name: "hostclaims", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "patch", "delete"}},
				{Name: "hostdeploypolicies", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "patch", "delete"}},
				{Name: "hostfirmwarecomponents", Namespaced: true, Verbs: metav1.Verbs{"get", "patch"}},
				{Name: "hostupdatepolicies", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "patch", "delete"}},
				{Name: "hardwaredata", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
				{Name: "hostfirmwaresettings", Namespaced: true, Verbs: metav1.Verbs{"get", "patch"}},
				{Name: "firmwareschemas", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
				{Name: "preprovisioningimages", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
			},
		}}},
		FakedServerVersion: &version.Info{GitVersion: "v1.35.0"},
	}
	info, err := New(discovery).Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.KubernetesVersion != "v1.35.0" || !info.BareMetalHosts.Available ||
		!info.BMCEventSubscriptions.Available || !info.DataImages.Available ||
		!info.FirmwareSchemas.Available || !info.HardwareData.Available ||
		!info.HostClaims.Available || !info.HostDeployPolicies.Available ||
		!info.HostFirmwareComponents.Available || !info.HostFirmwareSettings.Available ||
		!info.HostUpdatePolicies.Available || !info.PreprovisioningImages.Available {
		t.Fatalf("unexpected info: %#v", info)
	}
}
