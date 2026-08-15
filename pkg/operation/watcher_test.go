package operation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metal3sdk "github.com/zenhouke/go-metal3/internal/api"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
)

func TestWaitForHostToleratesOnlyFilteredIntermediateError(t *testing.T) {
	t.Parallel()
	key := types.NamespacedName{Namespace: "metal3", Name: "worker-0"}
	intermediate := &metal3v1alpha1.BareMetalHost{}
	intermediate.Namespace = key.Namespace
	intermediate.Name = key.Name
	intermediate.Status.ErrorType = metal3v1alpha1.PowerManagementError
	intermediate.Status.OperationalStatus = metal3v1alpha1.OperationalStatusError
	intermediate.Status.ErrorMessage = "recoverable"
	ready := intermediate.DeepCopy()
	ready.Status.ErrorType = ""
	ready.Status.ErrorMessage = ""
	ready.Status.OperationalStatus = metal3v1alpha1.OperationalStatusOK

	reader := &sequenceReader{hosts: []*metal3v1alpha1.BareMetalHost{intermediate, ready}}
	result, err := WaitForHostToleratingErrors(context.Background(), reader, key, 3*time.Second,
		func(host *metal3v1alpha1.BareMetalHost) (bool, error) {
			return host.Status.OperationalStatus == metal3v1alpha1.OperationalStatusOK, nil
		},
		func(host *metal3v1alpha1.BareMetalHost) bool { return host.Status.ErrorMessage == "recoverable" },
	)
	if err != nil || result.Status.OperationalStatus != metal3v1alpha1.OperationalStatusOK {
		t.Fatalf("WaitForHostToleratingErrors() = %#v, %v", result, err)
	}

	_, err = WaitForHost(context.Background(), &sequenceReader{hosts: []*metal3v1alpha1.BareMetalHost{intermediate}}, key, time.Second,
		func(*metal3v1alpha1.BareMetalHost) (bool, error) { return false, nil })
	if !metal3sdk.IsCode(err, metal3sdk.CodeProvisioning) {
		t.Fatalf("WaitForHost() unfiltered error = %v", err)
	}
}

type sequenceReader struct {
	mu    sync.Mutex
	hosts []*metal3v1alpha1.BareMetalHost
	next  int
}

func (r *sequenceReader) Get(_ context.Context, _ ctrlclient.ObjectKey, object ctrlclient.Object, _ ...ctrlclient.GetOption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.hosts) == 0 {
		return errors.New("no test hosts")
	}
	index := r.next
	if index >= len(r.hosts) {
		index = len(r.hosts) - 1
	} else {
		r.next++
	}
	r.hosts[index].DeepCopyInto(object.(*metal3v1alpha1.BareMetalHost))
	return nil
}

func (r *sequenceReader) List(context.Context, ctrlclient.ObjectList, ...ctrlclient.ListOption) error {
	return errors.New("unexpected List call")
}
