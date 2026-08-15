// Package cluster provides read-only Kubernetes API capability discovery.
package cluster

import (
	"context"
	"fmt"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

type Service struct{ discovery discovery.DiscoveryInterface }

func New(client discovery.DiscoveryInterface) *Service { return &Service{discovery: client} }

func (s *Service) Info(ctx context.Context) (*metal3sdk.ClusterInfo, error) {
	if s.discovery == nil {
		return nil, fmt.Errorf("Kubernetes discovery client is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	version, err := s.discovery.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("discover Kubernetes version: %w", err)
	}
	groupVersion := metal3v1alpha1.GroupVersion.String()
	resources, err := s.discovery.ServerResourcesForGroupVersion(groupVersion)
	if apierrors.IsNotFound(err) {
		return &metal3sdk.ClusterInfo{KubernetesVersion: version.GitVersion, Metal3APIVersion: groupVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("discover %s resources: %w", groupVersion, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &metal3sdk.ClusterInfo{
		KubernetesVersion:      version.GitVersion,
		Metal3APIVersion:       groupVersion,
		BareMetalHosts:         resourceInfo(resources.APIResources, "baremetalhosts"),
		BMCEventSubscriptions:  resourceInfo(resources.APIResources, "bmceventsubscriptions"),
		DataImages:             resourceInfo(resources.APIResources, "dataimages"),
		FirmwareSchemas:        resourceInfo(resources.APIResources, "firmwareschemas"),
		HardwareData:           resourceInfo(resources.APIResources, "hardwaredata"),
		HostClaims:             resourceInfo(resources.APIResources, "hostclaims"),
		HostDeployPolicies:     resourceInfo(resources.APIResources, "hostdeploypolicies"),
		HostFirmwareComponents: resourceInfo(resources.APIResources, "hostfirmwarecomponents"),
		HostFirmwareSettings:   resourceInfo(resources.APIResources, "hostfirmwaresettings"),
		HostUpdatePolicies:     resourceInfo(resources.APIResources, "hostupdatepolicies"),
		PreprovisioningImages:  resourceInfo(resources.APIResources, "preprovisioningimages"),
	}, nil
}

func resourceInfo(resources []metav1.APIResource, name string) metal3sdk.APIResourceInfo {
	for _, resource := range resources {
		if resource.Name != name {
			continue
		}
		return metal3sdk.APIResourceInfo{Available: true, Namespaced: resource.Namespaced, Verbs: append([]string(nil), resource.Verbs...)}
	}
	return metal3sdk.APIResourceInfo{}
}
