// Package httpapi exposes the SDK through an authenticated JSON API.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	metal3sdk "github.com/zenhouke/go-metal3"
)

const defaultMaxBodyBytes int64 = 1 << 20

type Options struct {
	SDK          metal3sdk.SDK
	APIKey       string
	MaxBodyBytes int64
	// AllowedNamespaces is mandatory because this server accepts BMC
	// credentials and exposes destructive lifecycle operations.
	AllowedNamespaces []string
}

type Server struct {
	sdk               metal3sdk.SDK
	apiKey            string
	maxBodyBytes      int64
	allowedNamespaces map[string]struct{}
	mux               *http.ServeMux
}

type hostCreateRequest struct {
	Namespace                    string                          `json:"namespace"`
	Name                         string                          `json:"name"`
	Labels                       map[string]string               `json:"labels,omitempty"`
	Annotations                  map[string]string               `json:"annotations,omitempty"`
	Taints                       []corev1.Taint                  `json:"taints,omitempty"`
	BMCAddress                   string                          `json:"bmcAddress"`
	BMCUsername                  string                          `json:"bmcUsername"`
	BMCPassword                  string                          `json:"bmcPassword"`
	BootMACAddress               string                          `json:"bootMACAddress,omitempty"`
	BootMode                     string                          `json:"bootMode,omitempty"`
	DisableCertificateValidation bool                            `json:"disableCertificateValidation,omitempty"`
	Online                       bool                            `json:"online,omitempty"`
	Description                  string                          `json:"description,omitempty"`
	Architecture                 string                          `json:"architecture,omitempty"`
	RootDeviceHints              *metal3v1alpha1.RootDeviceHints `json:"rootDeviceHints,omitempty"`
	ConsumerRef                  *corev1.ObjectReference         `json:"consumerRef,omitempty"`
	DisablePowerOff              bool                            `json:"disablePowerOff,omitempty"`
	InspectionDisabled           bool                            `json:"inspectionDisabled,omitempty"`
	ExternallyProvisioned        bool                            `json:"externallyProvisioned,omitempty"`
	AutomatedCleaningMode        string                          `json:"automatedCleaningMode,omitempty"`
	PreprovisioningNetworkData   string                          `json:"preprovisioningNetworkData,omitempty"`
}

type hostImportRequest struct {
	Host   hostCreateRequest                  `json:"host"`
	Status metal3v1alpha1.BareMetalHostStatus `json:"status"`
}

type waitRequest struct {
	Wait           bool `json:"wait"`
	TimeoutSeconds int  `json:"timeoutSeconds,omitempty"`
}

type rebootRequest struct {
	waitRequest
	Mode  string `json:"mode,omitempty"`
	Force bool   `json:"force,omitempty"`
}

type phasedRebootCompleteRequest struct {
	waitRequest
	PhaseID string `json:"phaseID"`
}

type detachRequest struct {
	waitRequest
	Force bool `json:"force,omitempty"`
}

type installRequest struct {
	ImageURL          string                          `json:"imageURL"`
	Checksum          string                          `json:"checksum,omitempty"`
	ChecksumType      string                          `json:"checksumType,omitempty"`
	DiskFormat        string                          `json:"diskFormat,omitempty"`
	OCIAuthSecretName string                          `json:"ociAuthSecretName,omitempty"`
	RootDeviceHints   *metal3v1alpha1.RootDeviceHints `json:"rootDeviceHints,omitempty"`
	UserData          string                          `json:"userData,omitempty"`
	NetworkData       string                          `json:"networkData,omitempty"`
	MetaData          string                          `json:"metaData,omitempty"`
	PowerOn           bool                            `json:"powerOn"`
	Wait              bool                            `json:"wait"`
	TimeoutSeconds    int                             `json:"timeoutSeconds,omitempty"`
}

type customDeployRequest struct {
	Method          string                          `json:"method"`
	RootDeviceHints *metal3v1alpha1.RootDeviceHints `json:"rootDeviceHints,omitempty"`
	UserData        string                          `json:"userData,omitempty"`
	NetworkData     string                          `json:"networkData,omitempty"`
	MetaData        string                          `json:"metaData,omitempty"`
	PowerOn         bool                            `json:"powerOn"`
	Wait            bool                            `json:"wait"`
	TimeoutSeconds  int                             `json:"timeoutSeconds,omitempty"`
}

type bmcUpdateRequest struct {
	Address        string `json:"address,omitempty"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	Wait           bool   `json:"wait"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

type hostPatchRequest struct {
	Labels                map[string]string `json:"labels,omitempty"`
	Annotations           map[string]string `json:"annotations,omitempty"`
	Description           *string           `json:"description,omitempty"`
	AutomatedCleaningMode *string           `json:"automatedCleaningMode,omitempty"`
}

type inspectionModeRequest struct {
	Mode string `json:"mode"`
}

type raidRequest struct {
	waitRequest
	RAID *metal3v1alpha1.RAIDConfig `json:"raid"`
}

type firmwareSettingsRequest struct {
	waitRequest
	Settings metal3v1alpha1.DesiredSettingsMap `json:"settings"`
}

type firmwareComponentsRequest struct {
	waitRequest
	Updates []metal3v1alpha1.FirmwareUpdate `json:"updates"`
}

type preprovisioningNetworkDataRequest struct {
	NetworkData string `json:"networkData"`
}

type externalInspectionRequest struct {
	waitRequest
	HardwareDetails *metal3v1alpha1.HardwareDetails `json:"hardwareDetails"`
}

type bmcEventSubscriptionRequest struct {
	Namespace      string                  `json:"namespace"`
	Name           string                  `json:"name"`
	Labels         map[string]string       `json:"labels,omitempty"`
	Annotations    map[string]string       `json:"annotations,omitempty"`
	HostName       string                  `json:"hostName"`
	Destination    string                  `json:"destination"`
	Context        string                  `json:"context,omitempty"`
	HTTPHeadersRef *corev1.SecretReference `json:"httpHeadersRef,omitempty"`
}

type dataImageRequest struct {
	URL         string            `json:"url"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type hostClaimRequest struct {
	Namespace   string                       `json:"namespace"`
	Name        string                       `json:"name"`
	Labels      map[string]string            `json:"labels,omitempty"`
	Annotations map[string]string            `json:"annotations,omitempty"`
	Spec        metal3v1alpha1.HostClaimSpec `json:"spec"`
}

type hostDeployPolicyRequest struct {
	Labels      map[string]string                   `json:"labels,omitempty"`
	Annotations map[string]string                   `json:"annotations,omitempty"`
	Spec        metal3v1alpha1.HostDeployPolicySpec `json:"spec"`
}

type hostUpdatePolicyRequest struct {
	Labels      map[string]string                   `json:"labels,omitempty"`
	Annotations map[string]string                   `json:"annotations,omitempty"`
	Spec        metal3v1alpha1.HostUpdatePolicySpec `json:"spec"`
}

type errorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

func New(opts Options) (*Server, error) {
	if opts.SDK == nil {
		return nil, fmt.Errorf("Metal3 SDK is required")
	}
	if len(opts.APIKey) < 32 {
		return nil, fmt.Errorf("API key must contain at least 32 characters")
	}
	if len(opts.AllowedNamespaces) == 0 {
		return nil, fmt.Errorf("at least one allowed namespace is required")
	}
	allowedNamespaces := make(map[string]struct{}, len(opts.AllowedNamespaces))
	for _, namespace := range opts.AllowedNamespaces {
		if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
			return nil, fmt.Errorf("invalid allowed namespace %q", namespace)
		}
		allowedNamespaces[namespace] = struct{}{}
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	server := &Server{sdk: opts.SDK, apiKey: opts.APIKey, maxBodyBytes: opts.MaxBodyBytes, allowedNamespaces: allowedNamespaces, mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /api/v1/cluster", s.auth(s.clusterInfo))
	s.mux.HandleFunc("GET /api/v1/hosts", s.auth(s.listHosts))
	s.mux.HandleFunc("POST /api/v1/hosts", s.auth(s.addHost))
	s.mux.HandleFunc("POST /api/v1/hosts/import", s.auth(s.importHost))
	s.mux.HandleFunc("GET /api/v1/hosts/{namespace}/{name}", s.auth(s.getHost))
	s.mux.HandleFunc("PATCH /api/v1/hosts/{namespace}/{name}", s.auth(s.updateHost))
	s.mux.HandleFunc("GET /api/v1/hosts/{namespace}/{name}/hardware", s.auth(s.getHardware))
	s.mux.HandleFunc("GET /api/v1/hosts/{namespace}/{name}/firmware-settings", s.auth(s.getFirmwareSettings))
	s.mux.HandleFunc("PATCH /api/v1/hosts/{namespace}/{name}/firmware-settings", s.auth(s.updateFirmwareSettings))
	s.mux.HandleFunc("GET /api/v1/hosts/{namespace}/{name}/firmware-components", s.auth(s.getFirmwareComponents))
	s.mux.HandleFunc("PATCH /api/v1/hosts/{namespace}/{name}/firmware-components", s.auth(s.updateFirmwareComponents))
	s.mux.HandleFunc("GET /api/v1/hosts/{namespace}/{name}/preprovisioning-image", s.auth(s.getPreprovisioningImage))
	s.mux.HandleFunc("GET /api/v1/firmware-schemas/{namespace}/{name}", s.auth(s.getFirmwareSchema))
	s.mux.HandleFunc("POST /api/v1/hosts/{namespace}/{name}/actions/{action}", s.auth(s.hostAction))
	s.mux.HandleFunc("DELETE /api/v1/hosts/{namespace}/{name}", s.auth(s.deleteHost))
	s.mux.HandleFunc("GET /api/v1/bmc-event-subscriptions", s.auth(s.listBMCEventSubscriptions))
	s.mux.HandleFunc("POST /api/v1/bmc-event-subscriptions", s.auth(s.createBMCEventSubscription))
	s.mux.HandleFunc("GET /api/v1/bmc-event-subscriptions/{namespace}/{name}", s.auth(s.getBMCEventSubscription))
	s.mux.HandleFunc("DELETE /api/v1/bmc-event-subscriptions/{namespace}/{name}", s.auth(s.deleteBMCEventSubscription))
	s.mux.HandleFunc("GET /api/v1/data-images", s.auth(s.listDataImages))
	s.mux.HandleFunc("GET /api/v1/data-images/{namespace}/{name}", s.auth(s.getDataImage))
	s.mux.HandleFunc("PUT /api/v1/data-images/{namespace}/{name}", s.auth(s.applyDataImage))
	s.mux.HandleFunc("DELETE /api/v1/data-images/{namespace}/{name}", s.auth(s.deleteDataImage))
	s.mux.HandleFunc("GET /api/v1/host-claims", s.auth(s.listHostClaims))
	s.mux.HandleFunc("POST /api/v1/host-claims", s.auth(s.createHostClaim))
	s.mux.HandleFunc("GET /api/v1/host-claims/{namespace}/{name}", s.auth(s.getHostClaim))
	s.mux.HandleFunc("DELETE /api/v1/host-claims/{namespace}/{name}", s.auth(s.deleteHostClaim))
	s.mux.HandleFunc("GET /api/v1/host-deploy-policies", s.auth(s.listHostDeployPolicies))
	s.mux.HandleFunc("GET /api/v1/host-deploy-policies/{namespace}/{name}", s.auth(s.getHostDeployPolicy))
	s.mux.HandleFunc("PUT /api/v1/host-deploy-policies/{namespace}/{name}", s.auth(s.applyHostDeployPolicy))
	s.mux.HandleFunc("DELETE /api/v1/host-deploy-policies/{namespace}/{name}", s.auth(s.deleteHostDeployPolicy))
	s.mux.HandleFunc("GET /api/v1/host-update-policies", s.auth(s.listHostUpdatePolicies))
	s.mux.HandleFunc("GET /api/v1/host-update-policies/{namespace}/{name}", s.auth(s.getHostUpdatePolicy))
	s.mux.HandleFunc("PUT /api/v1/host-update-policies/{namespace}/{name}", s.auth(s.applyHostUpdatePolicy))
	s.mux.HandleFunc("DELETE /api/v1/host-update-policies/{namespace}/{name}", s.auth(s.deleteHostUpdatePolicy))
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(s.apiKey) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiKey)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, errorResponse{Code: "UNAUTHORIZED", Message: "valid bearer token required"})
			return
		}
		next(w, r)
	}
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	info, err := s.sdk.Cluster().Info(r.Context())
	if err != nil || !info.BareMetalHosts.Available {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not-ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) clusterInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.sdk.Cluster().Info(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if !s.requireNamespace(w, namespace) {
		return
	}
	labels, err := parseLabels(r.URL.Query()["label"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: err.Error()})
		return
	}
	hosts, err := s.sdk.Hosts().List(r.Context(), metal3sdk.HostListOptions{Namespace: namespace, Labels: labels})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": hosts})
}

func (s *Server) addHost(w http.ResponseWriter, r *http.Request) {
	var request hostCreateRequest
	if !s.decode(w, r, &request) {
		return
	}
	if !s.requireNamespace(w, request.Namespace) {
		return
	}
	host, err := s.sdk.Hosts().Add(r.Context(), request.sdkRequest())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, host)
}

func (s *Server) importHost(w http.ResponseWriter, r *http.Request) {
	var request hostImportRequest
	if !s.decode(w, r, &request) {
		return
	}
	if !s.requireNamespace(w, request.Host.Namespace) {
		return
	}
	host, err := s.sdk.Hosts().Import(r.Context(), metal3sdk.HostImportRequest{Host: request.Host.sdkRequest(), Status: request.Status})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, host)
}

func (request hostCreateRequest) sdkRequest() metal3sdk.HostCreateRequest {
	return metal3sdk.HostCreateRequest{
		Namespace: request.Namespace, Name: request.Name, Labels: request.Labels, Annotations: request.Annotations, Taints: request.Taints,
		BMCAddress: request.BMCAddress, BMCUsername: request.BMCUsername, BMCPassword: []byte(request.BMCPassword),
		BootMACAddress: request.BootMACAddress, BootMode: metal3v1alpha1.BootMode(request.BootMode),
		DisableCertificateValidation: request.DisableCertificateValidation, Online: request.Online,
		Description: request.Description, Architecture: request.Architecture, RootDeviceHints: request.RootDeviceHints,
		ConsumerRef: request.ConsumerRef, DisablePowerOff: request.DisablePowerOff,
		InspectionDisabled: request.InspectionDisabled, ExternallyProvisioned: request.ExternallyProvisioned,
		AutomatedCleaningMode:      metal3v1alpha1.AutomatedCleaningMode(request.AutomatedCleaningMode),
		PreprovisioningNetworkData: []byte(request.PreprovisioningNetworkData),
	}
}

func (s *Server) getHost(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	host, err := s.sdk.Hosts().Get(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) updateHost(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	var request hostPatchRequest
	if !s.decode(w, r, &request) {
		return
	}
	var cleaningMode *metal3v1alpha1.AutomatedCleaningMode
	if request.AutomatedCleaningMode != nil {
		value := metal3v1alpha1.AutomatedCleaningMode(*request.AutomatedCleaningMode)
		cleaningMode = &value
	}
	host, err := s.sdk.Hosts().Update(r.Context(), hostKey(r), metal3sdk.HostPatch{
		Labels: request.Labels, Annotations: request.Annotations, Description: request.Description, AutomatedCleaningMode: cleaningMode,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) getHardware(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	hardware, err := s.sdk.Hosts().GetHardwareData(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hardware)
}

func (s *Server) getFirmwareSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	settings, err := s.sdk.Maintenance().GetFirmwareSettings(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateFirmwareSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	var request firmwareSettingsRequest
	if !s.decode(w, r, &request) {
		return
	}
	result, err := s.sdk.Maintenance().UpdateFirmwareSettings(r.Context(), hostKey(r), request.Settings, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	if err != nil {
		writeError(w, err)
		return
	}
	writeOperation(w, result)
}

func (s *Server) getFirmwareComponents(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	components, err := s.sdk.Maintenance().GetFirmwareComponents(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, components)
}

func (s *Server) updateFirmwareComponents(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	var request firmwareComponentsRequest
	if !s.decode(w, r, &request) {
		return
	}
	result, err := s.sdk.Maintenance().UpdateFirmwareComponents(r.Context(), hostKey(r), request.Updates, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	if err != nil {
		writeError(w, err)
		return
	}
	writeOperation(w, result)
}

func (s *Server) getFirmwareSchema(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	schema, err := s.sdk.Maintenance().GetFirmwareSchema(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (s *Server) getPreprovisioningImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	image, err := s.sdk.Maintenance().GetPreprovisioningImage(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, image)
}

func (s *Server) hostAction(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	key := hostKey(r)
	var result any
	var err error
	switch r.PathValue("action") {
	case "power-on", "power-off":
		var request waitRequest
		if !s.decode(w, r, &request) {
			return
		}
		opts := metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)}
		if r.PathValue("action") == "power-on" {
			result, err = s.sdk.Power().PowerOn(r.Context(), key, opts)
		} else {
			result, err = s.sdk.Power().PowerOff(r.Context(), key, opts)
		}
	case "reboot":
		var request rebootRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Power().Reboot(r.Context(), key, metal3sdk.RebootOptions{Mode: metal3sdk.RebootMode(request.Mode), Force: request.Force, Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "phased-reboot-start":
		var request rebootRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Power().StartPhasedReboot(r.Context(), key, metal3sdk.PhasedRebootOptions{Mode: metal3sdk.RebootMode(request.Mode), Force: request.Force, Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "phased-reboot-complete":
		var request phasedRebootCompleteRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Power().CompletePhasedReboot(r.Context(), key, request.PhaseID, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "detach":
		var request detachRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Hosts().Detach(r.Context(), key, metal3sdk.DetachOptions{Force: request.Force, Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "attach":
		var request waitRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Hosts().Attach(r.Context(), key, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "adopt-external":
		var request waitRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Hosts().AdoptExternallyProvisioned(r.Context(), key, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "install", "reinstall":
		var request installRequest
		if !s.decode(w, r, &request) {
			return
		}
		converted := request.toSDK()
		if r.PathValue("action") == "install" {
			result, err = s.sdk.Provisioning().Install(r.Context(), key, converted)
		} else {
			result, err = s.sdk.Provisioning().Reinstall(r.Context(), key, converted)
		}
	case "custom-deploy":
		var request customDeployRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Provisioning().CustomDeploy(r.Context(), key, metal3sdk.CustomDeployRequest{
			Method: request.Method, RootDeviceHints: request.RootDeviceHints,
			UserData: []byte(request.UserData), NetworkData: []byte(request.NetworkData), MetaData: []byte(request.MetaData),
			PowerOn: request.PowerOn, Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds),
		})
	case "deprovision":
		var request waitRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Provisioning().Deprovision(r.Context(), key, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "update-bmc":
		var request bmcUpdateRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Hosts().UpdateBMC(r.Context(), key, metal3sdk.BMCUpdateRequest{Address: request.Address, Username: request.Username, Password: []byte(request.Password), Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "inspect":
		var request waitRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Maintenance().Inspect(r.Context(), key, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "external-inspection":
		var request externalInspectionRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Maintenance().SetExternalInspectionData(r.Context(), key, request.HardwareDetails, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "inspection-mode":
		var request inspectionModeRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Maintenance().SetInspectionMode(r.Context(), key, metal3sdk.InspectionMode(request.Mode))
	case "configure-raid":
		var request raidRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Maintenance().ConfigureRAID(r.Context(), key, request.RAID, metal3sdk.WaitOptions{Wait: request.Wait, Timeout: seconds(request.TimeoutSeconds)})
	case "preprovisioning-network-data":
		var request preprovisioningNetworkDataRequest
		if !s.decode(w, r, &request) {
			return
		}
		result, err = s.sdk.Maintenance().SetPreprovisioningNetworkData(r.Context(), key, []byte(request.NetworkData))
	default:
		writeJSON(w, http.StatusNotFound, errorResponse{Code: "NOT_FOUND", Message: "unknown host action"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if operation, ok := result.(*metal3sdk.Operation); ok {
		writeOperation(w, operation)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeOperation(w http.ResponseWriter, operation *metal3sdk.Operation) {
	status := http.StatusOK
	if operation.Phase == metal3sdk.OperationRunning {
		status = http.StatusAccepted
	}
	writeJSON(w, status, operation)
}

func (s *Server) deleteHost(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	wait, err := parseBool(r.URL.Query().Get("wait"), false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: err.Error()})
		return
	}
	deleteSecrets, err := parseBool(r.URL.Query().Get("deleteOwnedSecrets"), false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: err.Error()})
		return
	}
	force, err := parseBool(r.URL.Query().Get("force"), false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: err.Error()})
		return
	}
	timeoutSeconds, err := strconv.Atoi(defaultString(r.URL.Query().Get("timeoutSeconds"), "0"))
	if err != nil || timeoutSeconds < 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: "timeoutSeconds must be a non-negative integer"})
		return
	}
	op, err := s.sdk.Hosts().Delete(r.Context(), hostKey(r), metal3sdk.DeleteOptions{Mode: metal3sdk.DeleteMode(r.URL.Query().Get("mode")), Wait: wait, Timeout: seconds(timeoutSeconds), DeleteOwnedSecrets: deleteSecrets, Force: force})
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if op.Phase == metal3sdk.OperationRunning {
		status = http.StatusAccepted
	}
	writeJSON(w, status, op)
}

func (s *Server) listBMCEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.resourceListOptions(w, r)
	if !ok {
		return
	}
	items, err := s.sdk.Resources().ListBMCEventSubscriptions(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createBMCEventSubscription(w http.ResponseWriter, r *http.Request) {
	var request bmcEventSubscriptionRequest
	if !s.decode(w, r, &request) || !s.requireNamespace(w, request.Namespace) {
		return
	}
	object, err := s.sdk.Resources().CreateBMCEventSubscription(r.Context(), metal3sdk.BMCEventSubscriptionCreateRequest{
		Namespace: request.Namespace, Name: request.Name, Labels: request.Labels, Annotations: request.Annotations,
		HostName: request.HostName, Destination: request.Destination, Context: request.Context, HTTPHeadersRef: request.HTTPHeadersRef,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, object)
}

func (s *Server) getBMCEventSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	object, err := s.sdk.Resources().GetBMCEventSubscription(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) deleteBMCEventSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	if err := s.sdk.Resources().DeleteBMCEventSubscription(r.Context(), hostKey(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDataImages(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.resourceListOptions(w, r)
	if !ok {
		return
	}
	items, err := s.sdk.Resources().ListDataImages(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getDataImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	object, err := s.sdk.Resources().GetDataImage(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) applyDataImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	var request dataImageRequest
	if !s.decode(w, r, &request) {
		return
	}
	object, err := s.sdk.Resources().ApplyDataImage(r.Context(), metal3sdk.DataImageApplyRequest{
		Namespace: r.PathValue("namespace"), Name: r.PathValue("name"), URL: request.URL,
		Labels: request.Labels, Annotations: request.Annotations,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) deleteDataImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	if err := s.sdk.Resources().DeleteDataImage(r.Context(), hostKey(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listHostClaims(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.resourceListOptions(w, r)
	if !ok {
		return
	}
	items, err := s.sdk.Resources().ListHostClaims(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createHostClaim(w http.ResponseWriter, r *http.Request) {
	var request hostClaimRequest
	if !s.decode(w, r, &request) || !s.requireNamespace(w, request.Namespace) {
		return
	}
	object, err := s.sdk.Resources().CreateHostClaim(r.Context(), metal3sdk.HostClaimCreateRequest{
		Namespace: request.Namespace, Name: request.Name, Labels: request.Labels, Annotations: request.Annotations, Spec: request.Spec,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, object)
}

func (s *Server) getHostClaim(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	object, err := s.sdk.Resources().GetHostClaim(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) deleteHostClaim(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	if err := s.sdk.Resources().DeleteHostClaim(r.Context(), hostKey(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listHostDeployPolicies(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.resourceListOptions(w, r)
	if !ok {
		return
	}
	items, err := s.sdk.Resources().ListHostDeployPolicies(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getHostDeployPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	object, err := s.sdk.Resources().GetHostDeployPolicy(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) applyHostDeployPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	var request hostDeployPolicyRequest
	if !s.decode(w, r, &request) {
		return
	}
	object, err := s.sdk.Resources().ApplyHostDeployPolicy(r.Context(), metal3sdk.HostDeployPolicyApplyRequest{
		Namespace: r.PathValue("namespace"), Name: r.PathValue("name"), Labels: request.Labels, Annotations: request.Annotations, Spec: request.Spec,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) deleteHostDeployPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	if err := s.sdk.Resources().DeleteHostDeployPolicy(r.Context(), hostKey(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listHostUpdatePolicies(w http.ResponseWriter, r *http.Request) {
	opts, ok := s.resourceListOptions(w, r)
	if !ok {
		return
	}
	items, err := s.sdk.Resources().ListHostUpdatePolicies(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getHostUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	object, err := s.sdk.Resources().GetHostUpdatePolicy(r.Context(), hostKey(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) applyHostUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	var request hostUpdatePolicyRequest
	if !s.decode(w, r, &request) {
		return
	}
	object, err := s.sdk.Resources().ApplyHostUpdatePolicy(r.Context(), metal3sdk.HostUpdatePolicyApplyRequest{
		Namespace: r.PathValue("namespace"), Name: r.PathValue("name"), Labels: request.Labels, Annotations: request.Annotations, Spec: request.Spec,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) deleteHostUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, r.PathValue("namespace")) {
		return
	}
	if err := s.sdk.Resources().DeleteHostUpdatePolicy(r.Context(), hostKey(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resourceListOptions(w http.ResponseWriter, r *http.Request) (metal3sdk.ResourceListOptions, bool) {
	namespace := r.URL.Query().Get("namespace")
	if !s.requireNamespace(w, namespace) {
		return metal3sdk.ResourceListOptions{}, false
	}
	selector, err := parseLabels(r.URL.Query()["label"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: err.Error()})
		return metal3sdk.ResourceListOptions{}, false
	}
	return metal3sdk.ResourceListOptions{Namespace: namespace, Labels: selector}, true
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: "invalid JSON request"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: "request must contain one JSON object"})
		return false
	}
	return true
}

func (s *Server) requireNamespace(w http.ResponseWriter, namespace string) bool {
	if namespace == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Code: "VALIDATION_FAILED", Message: "namespace is required"})
		return false
	}
	if _, allowed := s.allowedNamespaces[namespace]; !allowed {
		// Deliberately use 404 so the public API does not enumerate namespace
		// policy to an otherwise authenticated caller.
		writeJSON(w, http.StatusNotFound, errorResponse{Code: "NOT_FOUND", Message: "namespace is not managed by this API"})
		return false
	}
	return true
}

func parseLabels(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok || key == "" || item == "" {
			return nil, fmt.Errorf("label must use key=value syntax")
		}
		result[key] = item
	}
	return result, nil
}

func (r installRequest) toSDK() metal3sdk.InstallRequest {
	var format *string
	if r.DiskFormat != "" {
		value := r.DiskFormat
		format = &value
	}
	return metal3sdk.InstallRequest{
		ImageURL: r.ImageURL, Checksum: r.Checksum, ChecksumType: metal3v1alpha1.ChecksumType(r.ChecksumType), DiskFormat: format,
		OCIAuthSecretName: r.OCIAuthSecretName,
		RootDeviceHints:   r.RootDeviceHints, UserData: []byte(r.UserData), NetworkData: []byte(r.NetworkData), MetaData: []byte(r.MetaData),
		PowerOn: r.PowerOn, Wait: r.Wait, Timeout: seconds(r.TimeoutSeconds),
	}
}

func hostKey(r *http.Request) types.NamespacedName {
	return types.NamespacedName{Namespace: r.PathValue("namespace"), Name: r.PathValue("name")}
}

func seconds(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func parseBool(raw string, fallback bool) (bool, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
	return value, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	response := errorResponse{Code: "INTERNAL", Message: "internal server error"}
	var sdkErr *metal3sdk.Error
	if errors.As(err, &sdkErr) {
		response = errorResponse{Code: string(sdkErr.Code), Message: sdkErr.Message, Retryable: sdkErr.Retryable}
		switch sdkErr.Code {
		case metal3sdk.CodeValidation:
			status = http.StatusBadRequest
		case metal3sdk.CodeNotFound:
			status = http.StatusNotFound
		case metal3sdk.CodeConflict, metal3sdk.CodeInvalidState:
			status = http.StatusConflict
		case metal3sdk.CodeTimeout:
			status = http.StatusGatewayTimeout
		default:
			status = http.StatusBadGateway
		}
	}
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// ShutdownContext creates a bounded context suitable for HTTP shutdown.
func ShutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 20*time.Second)
}
