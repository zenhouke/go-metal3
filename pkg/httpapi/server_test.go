package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"k8s.io/apimachinery/pkg/types"

	metal3sdk "github.com/zenhouke/go-metal3"
)

const testAPIKey = "0123456789abcdef0123456789abcdef"

func testOptions(sdk metal3sdk.SDK) Options {
	return Options{SDK: sdk, APIKey: testAPIKey, AllowedNamespaces: []string{"metal3"}}
}

func TestAuthenticatedClusterInfo(t *testing.T) {
	t.Parallel()
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}}))
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":true`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestRejectsUnknownJSONBeforeDispatch(t *testing.T) {
	t.Parallel()
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hosts", strings.NewReader(`{"unknown":true}`))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestRequiresStrongAPIKey(t *testing.T) {
	t.Parallel()
	options := testOptions(&testSDK{cluster: testClusterService{}})
	options.APIKey = "short"
	if _, err := New(options); err == nil {
		t.Fatal("weak API key unexpectedly accepted")
	}
}

func TestRequiresNamespaceAllowlist(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{SDK: &testSDK{cluster: testClusterService{}}, APIKey: testAPIKey}); err == nil {
		t.Fatal("missing namespace allowlist unexpectedly accepted")
	}
}

func TestRejectsNamespaceOutsideAllowlistBeforeDispatch(t *testing.T) {
	t.Parallel()
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/hosts/other/host-1", nil)
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestMaintenanceInspectRoute(t *testing.T) {
	t.Parallel()
	maintenance := &testMaintenanceService{}
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, maintenance: maintenance}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/metal3/worker-0/actions/inspect", strings.NewReader(`{"wait":false}`))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || maintenance.inspected != (types.NamespacedName{Namespace: "metal3", Name: "worker-0"}) {
		t.Fatalf("response = %d %s, inspected = %v", response.Code, response.Body.String(), maintenance.inspected)
	}
}

func TestExternalInspectionRouteDispatchesHardwareDetails(t *testing.T) {
	t.Parallel()
	maintenance := &testMaintenanceService{}
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, maintenance: maintenance}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/metal3/worker-0/actions/external-inspection", strings.NewReader(`{"hardwareDetails":{"ramMebibytes":8192,"cpu":{"arch":"x86_64","count":4}},"wait":false}`))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || maintenance.externalDetails == nil || maintenance.externalDetails.RAMMebibytes != 8192 {
		t.Fatalf("response = %d %s, details = %#v", response.Code, response.Body.String(), maintenance.externalDetails)
	}
}

func TestHostImportRouteDispatchesReconstructedStatus(t *testing.T) {
	t.Parallel()
	hosts := &testHostService{}
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, hosts: hosts}))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"host":{"namespace":"metal3","name":"worker-0","bmcAddress":"ipmi://192.0.2.10","bmcUsername":"admin","bmcPassword":"secret"},"status":{"operationalStatus":"detached","errorMessage":"","poweredOn":true,"errorCount":0,"provisioning":{"state":"provisioned","ID":"node-id"}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/import", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || hosts.imported.Host.Name != "worker-0" || hosts.imported.Status.Provisioning.ID != "node-id" {
		t.Fatalf("response = %d %s, request = %#v", response.Code, response.Body.String(), hosts.imported)
	}
}

func TestAdoptExternalRouteDispatchesWaitOptions(t *testing.T) {
	t.Parallel()
	hosts := &testHostService{}
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, hosts: hosts}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/metal3/worker-0/actions/adopt-external", strings.NewReader(`{"wait":true,"timeoutSeconds":120}`))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !hosts.adopted.Wait || hosts.adopted.Timeout.Seconds() != 120 {
		t.Fatalf("response = %d %s, options = %#v", response.Code, response.Body.String(), hosts.adopted)
	}
}

func TestPhasedRebootRoutesPreservePhaseID(t *testing.T) {
	t.Parallel()
	power := &testPowerService{}
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, power: power}))
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/metal3/worker-0/actions/phased-reboot-start", strings.NewReader(`{"mode":"hard","wait":true,"timeoutSeconds":60}`))
	start.Header.Set("Authorization", "Bearer "+testAPIKey)
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted || power.started.Mode != metal3sdk.RebootHard || !power.started.Wait {
		t.Fatalf("start response = %d %s, options = %#v", startResponse.Code, startResponse.Body.String(), power.started)
	}

	complete := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/metal3/worker-0/actions/phased-reboot-complete", strings.NewReader(`{"phaseID":"11111111-1111-1111-1111-111111111111","wait":false}`))
	complete.Header.Set("Authorization", "Bearer "+testAPIKey)
	completeResponse := httptest.NewRecorder()
	server.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusAccepted || power.completedID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("complete response = %d %s, phaseID = %q", completeResponse.Code, completeResponse.Body.String(), power.completedID)
	}
}

func TestRemovedFirmwareConfigRouteReturnsNotFound(t *testing.T) {
	t.Parallel()
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, maintenance: &testMaintenanceService{}}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hosts/metal3/worker-0/actions/configure-firmware", strings.NewReader(`{"settings":{"sriovEnabled":true}}`))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestDataImageRouteEnforcesNamespaceAndDispatches(t *testing.T) {
	t.Parallel()
	resources := &testResourceService{}
	server, err := New(testOptions(&testSDK{cluster: testClusterService{}, resources: resources}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/data-images/metal3/worker-0", strings.NewReader(`{"url":"https://images.example/config.iso"}`))
	request.Header.Set("Authorization", "Bearer "+testAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || resources.applied.Namespace != "metal3" || resources.applied.Name != "worker-0" {
		t.Fatalf("response = %d %s, request = %#v", response.Code, response.Body.String(), resources.applied)
	}

	outside := httptest.NewRequest(http.MethodPut, "/api/v1/data-images/other/worker-0", strings.NewReader(`{"url":"https://images.example/config.iso"}`))
	outside.Header.Set("Authorization", "Bearer "+testAPIKey)
	outsideResponse := httptest.NewRecorder()
	server.ServeHTTP(outsideResponse, outside)
	if outsideResponse.Code != http.StatusNotFound {
		t.Fatalf("outside namespace status = %d", outsideResponse.Code)
	}
}

type testSDK struct {
	cluster     metal3sdk.ClusterService
	hosts       metal3sdk.HostService
	power       metal3sdk.PowerService
	maintenance metal3sdk.MaintenanceService
	resources   metal3sdk.ResourceService
}

func (s *testSDK) Cluster() metal3sdk.ClusterService           { return s.cluster }
func (s *testSDK) Hosts() metal3sdk.HostService                { return s.hosts }
func (s *testSDK) Power() metal3sdk.PowerService               { return s.power }
func (s *testSDK) Provisioning() metal3sdk.ProvisioningService { return nil }
func (s *testSDK) Maintenance() metal3sdk.MaintenanceService   { return s.maintenance }
func (s *testSDK) Resources() metal3sdk.ResourceService        { return s.resources }

type testClusterService struct{}

func (testClusterService) Info(context.Context) (*metal3sdk.ClusterInfo, error) {
	return &metal3sdk.ClusterInfo{KubernetesVersion: "v1.35.0", Metal3APIVersion: "metal3.io/v1alpha1", BareMetalHosts: metal3sdk.APIResourceInfo{Available: true, Namespaced: true}}, nil
}

type testHostService struct {
	metal3sdk.HostService
	imported metal3sdk.HostImportRequest
	adopted  metal3sdk.WaitOptions
}

func (s *testHostService) AdoptExternallyProvisioned(_ context.Context, _ types.NamespacedName, opts metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	s.adopted = opts
	return &metal3sdk.Operation{Phase: metal3sdk.OperationRunning}, nil
}

type testPowerService struct {
	metal3sdk.PowerService
	started     metal3sdk.PhasedRebootOptions
	completedID string
}

func (s *testPowerService) StartPhasedReboot(_ context.Context, _ types.NamespacedName, opts metal3sdk.PhasedRebootOptions) (*metal3sdk.Operation, error) {
	s.started = opts
	return &metal3sdk.Operation{ID: "11111111-1111-1111-1111-111111111111", Phase: metal3sdk.OperationRunning}, nil
}

func (s *testPowerService) CompletePhasedReboot(_ context.Context, _ types.NamespacedName, phaseID string, _ metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	s.completedID = phaseID
	return &metal3sdk.Operation{Phase: metal3sdk.OperationRunning}, nil
}

func (s *testHostService) Import(_ context.Context, request metal3sdk.HostImportRequest) (*metal3v1alpha1.BareMetalHost, error) {
	s.imported = request
	return &metal3v1alpha1.BareMetalHost{}, nil
}

type testMaintenanceService struct {
	inspected       types.NamespacedName
	externalDetails *metal3v1alpha1.HardwareDetails
}

type testResourceService struct {
	metal3sdk.ResourceService
	applied metal3sdk.DataImageApplyRequest
}

func (s *testResourceService) ApplyDataImage(_ context.Context, request metal3sdk.DataImageApplyRequest) (*metal3v1alpha1.DataImage, error) {
	s.applied = request
	return &metal3v1alpha1.DataImage{}, nil
}

func (s *testMaintenanceService) Inspect(_ context.Context, key types.NamespacedName, _ metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	s.inspected = key
	return &metal3sdk.Operation{Phase: metal3sdk.OperationRunning}, nil
}

func (s *testMaintenanceService) SetExternalInspectionData(_ context.Context, _ types.NamespacedName, details *metal3v1alpha1.HardwareDetails, _ metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	s.externalDetails = details
	return &metal3sdk.Operation{Phase: metal3sdk.OperationRunning}, nil
}

func (*testMaintenanceService) SetInspectionMode(context.Context, types.NamespacedName, metal3sdk.InspectionMode) (*metal3v1alpha1.BareMetalHost, error) {
	return nil, nil
}

func (*testMaintenanceService) ConfigureRAID(context.Context, types.NamespacedName, *metal3v1alpha1.RAIDConfig, metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	return nil, nil
}

func (*testMaintenanceService) GetFirmwareSettings(context.Context, types.NamespacedName) (*metal3v1alpha1.HostFirmwareSettings, error) {
	return nil, nil
}

func (*testMaintenanceService) GetFirmwareComponents(context.Context, types.NamespacedName) (*metal3v1alpha1.HostFirmwareComponents, error) {
	return nil, nil
}

func (*testMaintenanceService) GetFirmwareSchema(context.Context, types.NamespacedName) (*metal3v1alpha1.FirmwareSchema, error) {
	return nil, nil
}

func (*testMaintenanceService) GetPreprovisioningImage(context.Context, types.NamespacedName) (*metal3v1alpha1.PreprovisioningImage, error) {
	return nil, nil
}

func (*testMaintenanceService) UpdateFirmwareSettings(context.Context, types.NamespacedName, metal3v1alpha1.DesiredSettingsMap, metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	return nil, nil
}

func (*testMaintenanceService) UpdateFirmwareComponents(context.Context, types.NamespacedName, []metal3v1alpha1.FirmwareUpdate, metal3sdk.WaitOptions) (*metal3sdk.Operation, error) {
	return nil, nil
}

func (*testMaintenanceService) SetPreprovisioningNetworkData(context.Context, types.NamespacedName, []byte) (*metal3v1alpha1.BareMetalHost, error) {
	return nil, nil
}
