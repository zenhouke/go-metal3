package metal3sdk

import sdkapi "github.com/zenhouke/go-metal3/internal/api"

const (
	DeleteAndDeprovision      = sdkapi.DeleteAndDeprovision
	DeleteInventoryOnly       = sdkapi.DeleteInventoryOnly
	RebootAuto                = sdkapi.RebootAuto
	RebootSoft                = sdkapi.RebootSoft
	RebootHard                = sdkapi.RebootHard
	InspectionAutomatic       = sdkapi.InspectionAutomatic
	InspectionDisabled        = sdkapi.InspectionDisabled
	HostRegistering           = sdkapi.HostRegistering
	HostMatchingProfile       = sdkapi.HostMatchingProfile
	HostUnmanaged             = sdkapi.HostUnmanaged
	HostInspecting            = sdkapi.HostInspecting
	HostPreparing             = sdkapi.HostPreparing
	HostAvailable             = sdkapi.HostAvailable
	HostProvisioning          = sdkapi.HostProvisioning
	HostProvisioned           = sdkapi.HostProvisioned
	HostServicing             = sdkapi.HostServicing
	HostExternallyProvisioned = sdkapi.HostExternallyProvisioned
	HostDeprovisioning        = sdkapi.HostDeprovisioning
	HostDeleting              = sdkapi.HostDeleting
	HostError                 = sdkapi.HostError
	HostDetached              = sdkapi.HostDetached
	HostUnknown               = sdkapi.HostUnknown
	OperationPending          = sdkapi.OperationPending
	OperationRunning          = sdkapi.OperationRunning
	OperationSucceeded        = sdkapi.OperationSucceeded
	OperationFailed           = sdkapi.OperationFailed
	OperationCancelled        = sdkapi.OperationCancelled
)
