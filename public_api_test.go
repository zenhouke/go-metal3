package metal3sdk_test

import (
	"testing"

	metal3sdk "github.com/zenhouke/go-metal3"
)

// TestPublicAPISurface compiles as an external consumer of the root package.
// It guards the facade aliases from accidentally exposing an import path that
// callers cannot use.
func TestPublicAPISurface(t *testing.T) {
	var _ metal3sdk.SDK = (*metal3sdk.Client)(nil)

	_ = metal3sdk.Options{}
	_ = metal3sdk.HostCreateRequest{}
	_ = metal3sdk.Operation{Phase: metal3sdk.OperationPending}
}
