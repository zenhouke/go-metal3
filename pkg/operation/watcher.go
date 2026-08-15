package operation

import (
	"context"
	"errors"
	"fmt"
	"time"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

const defaultWaitTimeout = 30 * time.Minute

// HostPredicate reports whether a host has reached the desired state.
type HostPredicate func(*metal3v1alpha1.BareMetalHost) (bool, error)

// HostErrorFilter returns true only for an observed Metal3 error that is a
// documented, recoverable intermediate state for the current operation.
type HostErrorFilter func(*metal3v1alpha1.BareMetalHost) bool

// WaitForHost polls through a Reader. A future informer-backed implementation
// can replace this without changing domain-service APIs.
func WaitForHost(
	ctx context.Context,
	reader ctrlclient.Reader,
	key types.NamespacedName,
	timeout time.Duration,
	predicate HostPredicate,
) (*metal3v1alpha1.BareMetalHost, error) {
	return waitForHost(ctx, reader, key, timeout, predicate, nil)
}

// WaitForHostToleratingErrors behaves like WaitForHost, but allows one domain
// operation to identify its own recoverable Metal3 error states. All other
// errors retain the fail-fast behavior of WaitForHost.
func WaitForHostToleratingErrors(
	ctx context.Context,
	reader ctrlclient.Reader,
	key types.NamespacedName,
	timeout time.Duration,
	predicate HostPredicate,
	tolerate HostErrorFilter,
) (*metal3v1alpha1.BareMetalHost, error) {
	if tolerate == nil {
		return nil, metal3sdk.ValidationError("wait-host", "host error filter is required")
	}
	return waitForHost(ctx, reader, key, timeout, predicate, tolerate)
}

func waitForHost(
	ctx context.Context,
	reader ctrlclient.Reader,
	key types.NamespacedName,
	timeout time.Duration,
	predicate HostPredicate,
	tolerate HostErrorFilter,
) (*metal3v1alpha1.BareMetalHost, error) {
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	if predicate == nil {
		return nil, metal3sdk.ValidationError("wait-host", "host predicate is required")
	}

	var result *metal3v1alpha1.BareMetalHost
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		host := &metal3v1alpha1.BareMetalHost{}
		if err := reader.Get(ctx, key, host); err != nil {
			if apierrors.IsNotFound(err) {
				return false, &metal3sdk.Error{Code: metal3sdk.CodeNotFound, Operation: "wait-host", Host: key, Message: "BareMetalHost was deleted while waiting", Cause: err}
			}
			return false, err
		}
		done, err := predicate(host)
		if done {
			result = host.DeepCopy()
			return true, err
		}
		if err != nil {
			return false, err
		}
		if host.Status.ErrorType != "" || host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusError {
			if tolerate != nil && tolerate(host) {
				return false, nil
			}
			errorType := string(host.Status.ErrorType)
			if errorType == "" {
				errorType = string(host.Status.OperationalStatus)
			}
			return false, &metal3sdk.Error{
				Code:      metal3sdk.CodeProvisioning,
				Operation: "wait-host",
				Host:      key,
				Retryable: false,
				Message:   fmt.Sprintf("Metal3 reported %s: %s", errorType, host.Status.ErrorMessage),
			}
		}
		return false, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wait.ErrWaitTimeout) {
			return nil, &metal3sdk.Error{Code: metal3sdk.CodeTimeout, Operation: "wait-host", Host: key, Retryable: true, Message: "timed out waiting for BareMetalHost state", Cause: err}
		}
		return nil, err
	}
	return result, nil
}

// WaitForDeletion waits until the BareMetalHost is no longer present.
func WaitForDeletion(ctx context.Context, reader ctrlclient.Reader, key types.NamespacedName, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		host := &metal3v1alpha1.BareMetalHost{}
		err := reader.Get(ctx, key, host)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wait.ErrWaitTimeout) {
		return &metal3sdk.Error{Code: metal3sdk.CodeTimeout, Operation: "wait-delete", Host: key, Retryable: true, Message: "timed out waiting for BareMetalHost deletion", Cause: err}
	}
	return err
}
