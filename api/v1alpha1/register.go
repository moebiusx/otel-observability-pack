// Package v1alpha1 — scheme registration glue.
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// GroupVersion is the API surface this package owns.
var GroupVersion = schema.GroupVersion{Group: "observability.platform", Version: "v1alpha1"}

// SchemeBuilder collects the type registrations.
var SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

// AddToScheme registers Pack/PackList with a runtime scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func init() {
	SchemeBuilder.Register(&Pack{}, &PackList{})
}

// Resource is a helper for building unstructured GroupResource references.
func Resource(name string) schema.GroupResource {
	return GroupVersion.WithResource(name).GroupResource()
}

// Ensure runtime.Object interface compile-time check.
var (
	_ runtime.Object = (*Pack)(nil)
	_ runtime.Object = (*PackList)(nil)
)
