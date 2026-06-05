package controller_test

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var promRuleGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}

// upsertApplier is a controller.ObjectApplier that uses Create-or-Update
// semantics so tests can run against the controller-runtime fake client
// (which does not implement server-side apply for arbitrary
// unstructured types). It mirrors what SSA would do in steady state.
type upsertApplier struct{}

func (upsertApplier) Apply(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(obj.GroupVersionKind())
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	err := c.Get(ctx, key, cur)
	switch {
	case apierrors.IsNotFound(err):
		return c.Create(ctx, obj)
	case err != nil:
		return err
	default:
		obj.SetResourceVersion(cur.GetResourceVersion())
		return c.Update(ctx, obj)
	}
}
