package patch

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// MutateWithRetry applies a merge patch and retries optimistic-concurrency
// conflicts. The latest object is fetched before every mutation attempt.
func MutateWithRetry[T ctrlclient.Object](
	ctx context.Context,
	c ctrlclient.Client,
	key types.NamespacedName,
	newObject func() T,
	mutate func(T) error,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current := newObject()
		if err := c.Get(ctx, key, current); err != nil {
			return err
		}
		before, ok := current.DeepCopyObject().(ctrlclient.Object)
		if !ok {
			return apierrors.NewInternalError(nil)
		}
		if err := mutate(current); err != nil {
			return err
		}
		return c.Patch(ctx, current, ctrlclient.MergeFrom(before))
	})
}
