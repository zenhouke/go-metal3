package metal3sdk

import sdkapi "github.com/zenhouke/go-metal3/internal/api"

type ErrorCode = sdkapi.ErrorCode
type Error = sdkapi.Error

const (
	CodeNotFound       = sdkapi.CodeNotFound
	CodeConflict       = sdkapi.CodeConflict
	CodeInvalidState   = sdkapi.CodeInvalidState
	CodeValidation     = sdkapi.CodeValidation
	CodeBMCCredentials = sdkapi.CodeBMCCredentials
	CodeRegistration   = sdkapi.CodeRegistration
	CodeProvisioning   = sdkapi.CodeProvisioning
	CodeTimeout        = sdkapi.CodeTimeout
	CodeInternal       = sdkapi.CodeInternal
)

var IsCode = sdkapi.IsCode
var ValidationError = sdkapi.ValidationError
var InvalidStateError = sdkapi.InvalidStateError
var KubernetesError = sdkapi.KubernetesError
