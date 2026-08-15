package operation

import (
	"time"

	"github.com/google/uuid"
	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	"k8s.io/apimachinery/pkg/types"
)

// New creates an in-progress operation descriptor.
func New(kind string, host types.NamespacedName) *metal3sdk.Operation {
	return &metal3sdk.Operation{
		ID:        uuid.NewString(),
		Kind:      kind,
		Host:      host,
		StartedAt: time.Now().UTC(),
		Phase:     metal3sdk.OperationRunning,
	}
}

// Succeed marks an operation as completed.
func Succeed(op *metal3sdk.Operation, message string) {
	now := time.Now().UTC()
	op.FinishedAt = &now
	op.Phase = metal3sdk.OperationSucceeded
	op.Message = message
}

// Fail marks an operation as failed without exposing the underlying error.
func Fail(op *metal3sdk.Operation, message string) {
	now := time.Now().UTC()
	op.FinishedAt = &now
	op.Phase = metal3sdk.OperationFailed
	op.Message = message
}
