// packopctl is a dry-run CLI that exercises the same Resolve -> Plan ->
// Render path the in-cluster operator drives, without requiring a
// Kubernetes cluster. It is the operator core's CLI face: same code,
// no apiserver, JSON output suitable for golden-file diffing in CI.
//
// Usage:
//
//	packopctl plan -f examples/payment-service.pack.yaml [-target ske|bare-k8s|azure] [-namespace observability]
//
// Output is a Result JSON with .plan, .objects (for cluster targets),
// and .azureRequest (for Azure targets).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/operator"
	"github.com/example/observability-pack/internal/pack"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "plan" {
		fmt.Fprintln(os.Stderr, "usage: packopctl plan -f <pack> [-target ...] [-namespace ...]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	file := fs.String("f", "", "path to pack manifest (yaml or json)")
	target := fs.String("target", string(apiv1.TargetSKE), "execution target: ske | bare-k8s | azure")
	namespace := fs.String("namespace", "observability", "namespace for in-cluster CRDs")
	pipeline := fs.String("pipeline", "", "azure pipeline name (target=azure only)")
	provider := fs.String("provider", "azure-devops", "azure pipeline provider")
	branch := fs.String("branch", "", "azure pipeline branch")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	if *file == "" {
		fmt.Fprintln(os.Stderr, "error: -f is required")
		os.Exit(2)
	}

	p, err := pack.Load(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}

	var azureRef *apiv1.AzurePipelineRef
	if apiv1.Target(*target) == apiv1.TargetAzure {
		azureRef = &apiv1.AzurePipelineRef{
			Provider: *provider,
			Pipeline: *pipeline,
			Branch:   *branch,
		}
	}

	res, err := operator.Reconcile(p.Raw, apiv1.Target(*target), *namespace, azureRef)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reconcile:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}
