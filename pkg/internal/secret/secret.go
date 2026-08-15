package secret

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "go-metal3-sdk"
	PurposeLabel   = "metal3sdk.io/secret-purpose"
)

// Upsert creates a Secret or updates only its SDK-owned metadata and payload.
func Upsert(ctx context.Context, c ctrlclient.Client, desired *corev1.Secret) error {
	if desired.Labels == nil {
		desired.Labels = map[string]string{}
	}
	desired.Labels[ManagedByLabel] = ManagedByValue

	err := c.Create(ctx, desired)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}

	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current := &corev1.Secret{}
		if err := c.Get(ctx, key, current); err != nil {
			return err
		}
		if current.Labels[ManagedByLabel] != ManagedByValue {
			return fmt.Errorf("refusing to overwrite Secret %s/%s not owned by go-metal3 SDK", desired.Namespace, desired.Name)
		}
		before := current.DeepCopy()
		current.Type = desired.Type
		current.Immutable = desired.Immutable
		current.Data = cloneBytesMap(desired.Data)
		current.StringData = cloneStringMap(desired.StringData)
		current.Labels = mergeMap(current.Labels, desired.Labels)
		current.Annotations = mergeMap(current.Annotations, desired.Annotations)
		return c.Patch(ctx, current, ctrlclient.MergeFrom(before))
	})
}

// BMC builds the Secret shape expected by BareMetalHost.spec.bmc.
func BMC(namespace, name, username string, password []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
				PurposeLabel:   "bmc",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte(username),
			"password": append([]byte(nil), password...),
		},
	}
}

// ContentAddressed creates or verifies an immutable, content-addressed Secret.
// The returned reference can be atomically installed into a BareMetalHost spec.
func ContentAddressed(
	ctx context.Context,
	c ctrlclient.Client,
	namespace, hostName, purpose, dataKey string,
	data []byte,
	owner *metav1.OwnerReference,
) (*corev1.SecretReference, error) {
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])[:12]
	base := hostName
	const suffixBudget = 1 + 32 + 1 + 12
	if len(base) > 253-suffixBudget {
		base = base[:253-suffixBudget]
	}
	name := fmt.Sprintf("%s-%s-%s", base, purpose, hash)
	immutable := true
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
				PurposeLabel:   purpose,
			},
		},
		Type:      corev1.SecretTypeOpaque,
		Immutable: &immutable,
		Data:      map[string][]byte{dataKey: append([]byte(nil), data...)},
	}
	if owner != nil {
		desired.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	if err := c.Create(ctx, desired); err == nil {
		return &corev1.SecretReference{Name: name, Namespace: namespace}, nil
	} else if !apierrors.IsAlreadyExists(err) {
		return nil, err
	}

	existing := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, existing); err != nil {
		return nil, err
	}
	if existing.Labels[ManagedByLabel] != ManagedByValue ||
		existing.Labels[PurposeLabel] != purpose ||
		!bytes.Equal(existing.Data[dataKey], data) {
		return nil, fmt.Errorf("content-addressed Secret %s/%s exists with unexpected ownership or content", namespace, name)
	}
	if owner != nil && (len(existing.OwnerReferences) != 1 || existing.OwnerReferences[0].UID != owner.UID) {
		before := existing.DeepCopy()
		existing.OwnerReferences = []metav1.OwnerReference{*owner}
		if err := c.Patch(ctx, existing, ctrlclient.MergeFrom(before)); err != nil {
			return nil, err
		}
	}
	return &corev1.SecretReference{Name: name, Namespace: namespace}, nil
}

// DeleteIfManaged removes a Secret only when it is explicitly owned by this SDK.
func DeleteIfManaged(ctx context.Context, c ctrlclient.Client, ref corev1.SecretReference) error {
	current := &corev1.Secret{}
	key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	if err := c.Get(ctx, key, current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if current.Labels[ManagedByLabel] != ManagedByValue {
		return nil
	}
	if err := c.Delete(ctx, current); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func cloneBytesMap(in map[string][]byte) map[string][]byte {
	if in == nil {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for key, value := range in {
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeMap(base, extra map[string]string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}
