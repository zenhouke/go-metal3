package api

import (
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// ErrorCode is a stable, transport-neutral SDK error classification.
type ErrorCode string

const (
	CodeNotFound       ErrorCode = "NOT_FOUND"
	CodeConflict       ErrorCode = "CONFLICT"
	CodeInvalidState   ErrorCode = "INVALID_STATE"
	CodeValidation     ErrorCode = "VALIDATION_FAILED"
	CodeBMCCredentials ErrorCode = "BMC_CREDENTIALS_ERROR"
	CodeRegistration   ErrorCode = "REGISTRATION_ERROR"
	CodeProvisioning   ErrorCode = "PROVISIONING_ERROR"
	CodeTimeout        ErrorCode = "TIMEOUT"
	CodeInternal       ErrorCode = "INTERNAL"
)

// Error is returned by all domain services. Message must never contain
// credentials, Secret data, or authorization headers.
type Error struct {
	Code      ErrorCode
	Operation string
	Host      types.NamespacedName
	Retryable bool
	Message   string
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Host.Name == "" {
		return fmt.Sprintf("metal3sdk %s: %s", e.Operation, e.Message)
	}
	return fmt.Sprintf("metal3sdk %s for %s/%s: %s", e.Operation, e.Host.Namespace, e.Host.Name, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// IsCode reports whether err or one of its wrapped causes is an SDK Error
// with the requested code.
func IsCode(err error, code ErrorCode) bool {
	var sdkErr *Error
	return errors.As(err, &sdkErr) && sdkErr.Code == code
}

func newError(code ErrorCode, operation string, host types.NamespacedName, retryable bool, message string, cause error) error {
	return &Error{
		Code:      code,
		Operation: operation,
		Host:      host,
		Retryable: retryable,
		Message:   message,
		Cause:     cause,
	}
}

// ValidationError creates a non-retryable request validation error.
func ValidationError(operation, message string) error {
	return newError(CodeValidation, operation, types.NamespacedName{}, false, message, nil)
}

// InvalidStateError creates an error for an operation that is not valid in
// the host's current provisioning state.
func InvalidStateError(operation string, host types.NamespacedName, expected, actual string) error {
	return newError(
		CodeInvalidState,
		operation,
		host,
		false,
		fmt.Sprintf("expected state %q, got %q", expected, actual),
		nil,
	)
}

// KubernetesError converts common API-server failures into stable SDK error
// codes while preserving the original error for errors.Is/errors.As.
func KubernetesError(operation string, host types.NamespacedName, err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return err
	}
	code, retryable, message := CodeInternal, false, "Kubernetes API request failed"
	switch {
	case apierrors.IsNotFound(err):
		code, message = CodeNotFound, "Kubernetes resource was not found"
	case apierrors.IsAlreadyExists(err), apierrors.IsConflict(err):
		code, retryable, message = CodeConflict, true, "Kubernetes resource changed concurrently"
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), apierrors.IsTooManyRequests(err):
		retryable, message = true, "Kubernetes API request should be retried"
	}
	return newError(code, operation, host, retryable, message, err)
}
