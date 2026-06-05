// Code in this file is hand-written rather than generated. The
// observability-pack operator deliberately avoids the kubebuilder
// code-generator toolchain so the build stays simple. If we ever adopt
// it, this file is the one to remove and replace with the generated
// counterpart.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeepCopyInto copies src into dst.
func (in *Pack) DeepCopyInto(out *Pack) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *Pack) DeepCopy() *Pack {
	if in == nil {
		return nil
	}
	out := new(Pack)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies src into dst.
func (in *PackList) DeepCopyInto(out *PackList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Pack, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy.
func (in *PackList) DeepCopy() *PackList {
	if in == nil {
		return nil
	}
	out := new(PackList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies src into dst.
func (in *PackSpec) DeepCopyInto(out *PackSpec) {
	*out = *in
	if in.AzurePipeline != nil {
		out.AzurePipeline = new(AzurePipelineRef)
		*out.AzurePipeline = *in.AzurePipeline
	}
	out.Manifest = deepCopyMap(in.Manifest)
}

// DeepCopyInto copies src into dst.
func (in *PackStatus) DeepCopyInto(out *PackStatus) {
	*out = *in
	if in.Tools != nil {
		out.Tools = append([]ToolStatus(nil), in.Tools...)
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	if in.AzurePipelineRun != nil {
		out.AzurePipelineRun = new(PipelineRunInfo)
		*out.AzurePipelineRun = *in.AzurePipelineRun
	}
}

// deepCopyMap is a recursive copy for the free-form Manifest field.
// We accept the cost of reflection-free recursion because the depth is
// bounded by the JSON Schema and we want zero codegen dependencies.
func deepCopyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopySlice(in []any) []any {
	if in == nil {
		return nil
	}
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopyMap(x)
	case []any:
		return deepCopySlice(x)
	default:
		// Scalars (string, bool, float64, int, nil) are immutable in Go's
		// JSON-decoded representation; aliasing is safe.
		return x
	}
}
