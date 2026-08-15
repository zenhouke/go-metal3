package provision

import (
	"fmt"
	"net/url"
	"regexp"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	"k8s.io/apimachinery/pkg/util/validation"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

const maxConfigDataSize = 900 * 1024

var customDeployMethodPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

func validateInstall(req metal3sdk.InstallRequest) error {
	parsed, err := url.Parse(req.ImageURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "oci") {
		return metal3sdk.ValidationError("install", "image URL must use http, https, or oci and include a host")
	}
	if req.OCIAuthSecretName != "" {
		if parsed.Scheme != "oci" {
			return metal3sdk.ValidationError("install", "OCI auth Secret can only be used with an oci:// image")
		}
		if problems := validation.IsDNS1123Subdomain(req.OCIAuthSecretName); len(problems) != 0 {
			return metal3sdk.ValidationError("install", fmt.Sprintf("invalid OCI auth Secret name %q", req.OCIAuthSecretName))
		}
	}
	format := ""
	if req.DiskFormat != nil {
		format = *req.DiskFormat
		switch format {
		case "raw", "qcow2", "vdi", "vmdk", "live-iso":
		default:
			return metal3sdk.ValidationError("install", fmt.Sprintf("unsupported image format %q", format))
		}
	}
	if format == "live-iso" {
		if req.Checksum != "" || req.RootDeviceHints != nil || len(req.UserData) > 0 || len(req.NetworkData) > 0 || len(req.MetaData) > 0 {
			return metal3sdk.ValidationError("install", "live-iso does not use checksum, root device hints, user data, network data, or meta data")
		}
		return nil
	}
	if parsed.Scheme == "oci" {
		if req.Checksum != "" || req.ChecksumType != "" {
			return metal3sdk.ValidationError("install", "OCI images do not use BareMetalHost checksum fields")
		}
	} else {
		if req.Checksum == "" {
			return metal3sdk.ValidationError("install", "checksum is required for non-OCI images")
		}
		switch req.ChecksumType {
		case "", metal3v1alpha1.AutoChecksum, metal3v1alpha1.SHA256, metal3v1alpha1.SHA512:
		case metal3v1alpha1.MD5:
			return metal3sdk.ValidationError("install", "MD5 checksums are deprecated and are not accepted by this SDK")
		default:
			return metal3sdk.ValidationError("install", fmt.Sprintf("unsupported checksum type %q", req.ChecksumType))
		}
	}
	for name, data := range map[string][]byte{"user data": req.UserData, "network data": req.NetworkData, "meta data": req.MetaData} {
		if len(data) > maxConfigDataSize {
			return metal3sdk.ValidationError("install", fmt.Sprintf("%s exceeds the safe Kubernetes Secret size", name))
		}
	}
	return nil
}

func validateCustomDeploy(req metal3sdk.CustomDeployRequest) error {
	if !customDeployMethodPattern.MatchString(req.Method) {
		return metal3sdk.ValidationError("custom-deploy", "custom deploy method must contain 1-255 letters, digits, dots, underscores, or hyphens")
	}
	for name, data := range map[string][]byte{"user data": req.UserData, "network data": req.NetworkData, "meta data": req.MetaData} {
		if len(data) > maxConfigDataSize {
			return metal3sdk.ValidationError("custom-deploy", fmt.Sprintf("%s exceeds the safe Kubernetes Secret size", name))
		}
	}
	return nil
}
