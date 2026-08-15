package host

import (
	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func mapCreateRequest(req metal3sdk.HostCreateRequest, secretName string) *metal3v1alpha1.BareMetalHost {
	annotations := cloneMap(req.Annotations)
	inspectionMode := metal3v1alpha1.InspectionModeAgent
	if req.InspectionDisabled {
		inspectionMode = metal3v1alpha1.InspectionModeDisabled
	}
	return &metal3v1alpha1.BareMetalHost{
		TypeMeta: metav1.TypeMeta{
			APIVersion: metal3v1alpha1.GroupVersion.String(),
			Kind:       "BareMetalHost",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   req.Namespace,
			Name:        req.Name,
			Labels:      cloneMap(req.Labels),
			Annotations: annotations,
		},
		Spec: metal3v1alpha1.BareMetalHostSpec{
			Taints:                append([]corev1.Taint(nil), req.Taints...),
			Online:                req.Online,
			BootMACAddress:        req.BootMACAddress,
			BootMode:              req.BootMode,
			Description:           req.Description,
			Architecture:          req.Architecture,
			RootDeviceHints:       req.RootDeviceHints.DeepCopy(),
			ConsumerRef:           req.ConsumerRef.DeepCopy(),
			DisablePowerOff:       req.DisablePowerOff,
			ExternallyProvisioned: req.ExternallyProvisioned,
			AutomatedCleaningMode: req.AutomatedCleaningMode,
			InspectionMode:        inspectionMode,
			BMC: metal3v1alpha1.BMCDetails{
				Address:                        req.BMCAddress,
				CredentialsName:                secretName,
				DisableCertificateVerification: req.DisableCertificateValidation,
			},
		},
	}
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
