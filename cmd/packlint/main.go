// packlint validates ObservabilityPack manifests against the schema, checks
// reference integrity, and scores conformance per the maturity-model rubric.
//
// Usage:
//
//	packlint [--schema PATH] [--format text|yaml|json] FILE [FILE...]
//
// Exit codes:
//
//	0 — every file passed schema, refs, and MUST clauses at its target tier
//	1 — at least one file produced an error-severity finding
//	2 — invocation error (bad flags, file not found, schema compile failure)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/example/observability-pack/internal/lint"
	"github.com/example/observability-pack/internal/pack"
)

func main() {
	var (
		schemaPath string
		format     string
	)
	flag.StringVar(&schemaPath, "schema", defaultSchemaPath(), "path to the JSON Schema")
	flag.StringVar(&format, "format", "text", "output format: text|yaml|json")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: packlint [--schema PATH] [--format text|yaml|json] FILE [FILE...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	anyErr := false
	results := make([]*lint.Result, 0, flag.NArg())

	for _, path := range flag.Args() {
		p, err := pack.Load(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load %s: %v\n", path, err)
			anyErr = true
			continue
		}
		r := &lint.Result{
			Pack:        p.Metadata.Name,
			Version:     p.Metadata.Version,
			Service:     p.Metadata.Bindings.Service,
			Criticality: p.Metadata.Bindings.Criticality,
		}
		if err := lint.Schema(p, schemaPath, r); err != nil {
			fmt.Fprintf(os.Stderr, "schema compile: %v\n", err)
			os.Exit(2)
		}
		lint.Refs(p, r)
		lint.Conformance(p, r)
		results = append(results, r)
		if r.HasErrors() {
			anyErr = true
		}
	}

	if err := emit(format, results); err != nil {
		fmt.Fprintf(os.Stderr, "emit: %v\n", err)
		os.Exit(2)
	}

	if anyErr {
		os.Exit(1)
	}
}

func emit(format string, results []*lint.Result) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	case "yaml":
		b, err := yaml.Marshal(results)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	case "text", "":
		for _, r := range results {
			emitText(r)
		}
		return nil
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

func emitText(r *lint.Result) {
	fmt.Println(r.Summary())
	for _, f := range r.Findings {
		fmt.Printf("  [%s] %s  %s\n      %s\n", f.Severity, f.Code, f.Path, f.Message)
	}
	fmt.Println()
}

// defaultSchemaPath looks for schema/observability-pack.schema.json next to
// the binary or in the current working directory tree.
func defaultSchemaPath() string {
	candidates := []string{
		"schema/observability-pack.schema.json",
		"../schema/observability-pack.schema.json",
		"../../schema/observability-pack.schema.json",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return "schema/observability-pack.schema.json"
}
