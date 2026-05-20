//go:build ignore

// wrap.go reads the YAML pack manifest from examples/payment-service.pack.yaml
// (or the path given via -f) and emits a Kubernetes `Pack` CR with that
// manifest embedded under spec.manifest. Used by the kind smoke driver
// to avoid hand-maintaining a duplicate of the example manifest.
//
//	go run ./hack/smoke/wrap.go -f examples/payment-service.pack.yaml -name payments -namespace obs -target ske > hack/smoke/pack-cr.yaml
package main

import (
	"flag"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

func main() {
	src := flag.String("f", "examples/payment-service.pack.yaml", "path to pack YAML")
	name := flag.String("name", "payments", "Pack CR name")
	namespace := flag.String("namespace", "obs", "Pack CR namespace")
	target := flag.String("target", "ske", "target: ske|bare-k8s|azure")
	flag.Parse()

	raw, err := os.ReadFile(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read pack:", err)
		os.Exit(1)
	}
	var manifest map[string]any
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		fmt.Fprintln(os.Stderr, "parse pack:", err)
		os.Exit(1)
	}

	cr := map[string]any{
		"apiVersion": "observability.platform/v1alpha1",
		"kind":       "Pack",
		"metadata": map[string]any{
			"name":      *name,
			"namespace": *namespace,
		},
		"spec": map[string]any{
			"target":   *target,
			"manifest": manifest,
		},
	}

	out, err := yaml.Marshal(cr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
