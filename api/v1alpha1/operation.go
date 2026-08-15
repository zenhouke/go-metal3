package v1alpha1

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// OperationSpec is the persistence-facing representation of a long-running
// SDK operation. It is intentionally independent from the BareMetalHost API.
type OperationSpec struct {
	ID             string               `json:"id"`
	Kind           string               `json:"kind"`
	Host           types.NamespacedName `json:"host"`
	IdempotencyKey string               `json:"idempotencyKey,omitempty"`
	RequestedAt    time.Time            `json:"requestedAt"`
}

// OperationStatus stores recoverable progress for a long-running operation.
type OperationStatus struct {
	Phase      string     `json:"phase"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}
