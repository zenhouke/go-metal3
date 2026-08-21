package host

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

var supportedBMCSchemes = map[string]struct{}{
	"ipmi":                       {},
	"redfish":                    {},
	"redfish+http":               {},
	"redfish+https":              {},
	"redfish-virtualmedia":       {},
	"redfish-virtualmedia+http":  {},
	"redfish-virtualmedia+https": {},
	"idrac-redfish":              {},
	"idrac-redfish+http":         {},
	"idrac-redfish+https":        {},
	"idrac-virtualmedia":         {},
	"idrac-virtualmedia+http":    {},
	"idrac-virtualmedia+https":   {},
}

func validateCreate(operation string, req metal3sdk.HostCreateRequest) error {
	if req.Namespace == "" || req.Name == "" {
		return metal3sdk.ValidationError(operation, "namespace and name are required")
	}
	if problems := k8svalidation.IsDNS1123Label(req.Namespace); len(problems) > 0 {
		return metal3sdk.ValidationError(operation, "namespace is not a valid Kubernetes name")
	}
	if problems := k8svalidation.IsDNS1123Subdomain(req.Name); len(problems) > 0 || len(req.Name) > 249 {
		return metal3sdk.ValidationError(operation, "host name is invalid or too long for its managed BMC Secret")
	}
	if strings.TrimSpace(req.BMCUsername) == "" || len(req.BMCPassword) == 0 {
		return metal3sdk.ValidationError(operation, "BMC username and password are required")
	}
	if err := validateBMCAddress(operation, req.BMCAddress); err != nil {
		return err
	}
	if req.DisablePowerOff && !req.Online {
		return metal3sdk.ValidationError(operation, "online must be true when disablePowerOff is enabled")
	}
	if req.BootMACAddress == "" && bootMACRequired(req.BMCAddress, req.InspectionDisabled) {
		return metal3sdk.ValidationError(operation, "boot MAC address is required by this BMC driver or when inspection is disabled")
	}
	if req.BootMACAddress != "" {
		if _, err := net.ParseMAC(req.BootMACAddress); err != nil {
			return metal3sdk.ValidationError(operation, "boot MAC address is invalid")
		}
	}
	switch req.BootMode {
	case "", metal3v1alpha1.UEFI, metal3v1alpha1.UEFISecureBoot, metal3v1alpha1.Legacy:
	default:
		return metal3sdk.ValidationError(operation, fmt.Sprintf("unsupported boot mode %q", req.BootMode))
	}
	switch req.AutomatedCleaningMode {
	case "", metal3v1alpha1.CleaningModeMetadata, metal3v1alpha1.CleaningModeDisabled:
	default:
		return metal3sdk.ValidationError(operation, "automated cleaning mode must be metadata or disabled")
	}
	if req.ConsumerRef != nil {
		if req.ConsumerRef.Name == "" || req.ConsumerRef.Kind == "" || req.ConsumerRef.APIVersion == "" {
			return metal3sdk.ValidationError(operation, "consumerRef requires apiVersion, kind, and name")
		}
		if req.ConsumerRef.Namespace != "" {
			if problems := k8svalidation.IsDNS1123Label(req.ConsumerRef.Namespace); len(problems) > 0 {
				return metal3sdk.ValidationError(operation, "consumerRef namespace is invalid")
			}
		}
	}
	if err := validateUserAnnotations(operation, req.Annotations); err != nil {
		return err
	}
	return nil
}

func bootMACRequired(address string, inspectionDisabled bool) bool {
	if inspectionDisabled || !strings.Contains(address, "://") {
		return true
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return true
	}
	return !strings.Contains(strings.ToLower(parsed.Scheme), "virtualmedia")
}

func validateUserAnnotations(operation string, annotations map[string]string) error {
	for key := range annotations {
		// Only annotations interpreted by BMO as commands/state-transfer
		// markers are reserved.  The baremetalhost.metal3.io prefix is not
		// itself reserved: integrations commonly store ordinary metadata under
		// that prefix, and the public API explicitly supports non-control
		// annotations.
		if key == metal3v1alpha1.PausedAnnotation ||
			key == metal3v1alpha1.DetachedAnnotation ||
			key == metal3v1alpha1.StatusAnnotation ||
			key == metal3v1alpha1.HardwareDetailsAnnotation ||
			key == metal3v1alpha1.InspectAnnotationPrefix ||
			strings.HasPrefix(key, metal3v1alpha1.InspectAnnotationPrefix+"/") ||
			key == metal3v1alpha1.RebootAnnotationPrefix ||
			strings.HasPrefix(key, metal3v1alpha1.RebootAnnotationPrefix+"/") {
			return metal3sdk.ValidationError(operation, fmt.Sprintf("Metal3 control annotation %q must be changed through its dedicated SDK operation", key))
		}
	}
	return nil
}

func validateBMCAddress(operation, address string) error {
	if !strings.Contains(address, "://") {
		if validateImplicitIPMIAddress(address) {
			return nil
		}
		return metal3sdk.ValidationError(operation, "implicit IPMI address must be a host name or IP address with an optional port")
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return metal3sdk.ValidationError(operation, "BMC address must be an absolute URL")
	}
	if _, ok := supportedBMCSchemes[parsed.Scheme]; !ok {
		return metal3sdk.ValidationError(operation, fmt.Sprintf("unsupported BMC scheme %q", parsed.Scheme))
	}
	if parsed.User != nil {
		return metal3sdk.ValidationError(operation, "BMC credentials must be provided through the Secret, not embedded in the address")
	}
	return nil
}

func validateImplicitIPMIAddress(address string) bool {
	if address == "" || strings.ContainsAny(address, "/?#@") {
		return false
	}
	host := address
	if parsedHost, port, err := net.SplitHostPort(address); err == nil {
		if port == "" {
			return false
		}
		host = parsedHost
	} else if strings.Count(address, ":") > 0 && net.ParseIP(address) == nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if net.ParseIP(host) != nil {
		return true
	}
	return len(k8svalidation.IsDNS1123Subdomain(strings.ToLower(host))) == 0
}
