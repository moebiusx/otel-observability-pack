package lint

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/example/observability-pack/internal/pack"
)

// Schema compiles the JSON Schema at schemaPath and validates the raw pack
// document against it. All schema violations are appended to result as
// error-severity findings under code "schema/violation".
func Schema(p *pack.Pack, schemaPath string, r *Result) error {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	sch, err := c.Compile(schemaPath)
	if err != nil {
		return fmt.Errorf("compile schema %s: %w", schemaPath, err)
	}
	docJSON, err := p.RawJSON()
	if err != nil {
		return fmt.Errorf("re-encode pack: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(docJSON))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("decode pack: %w", err)
	}
	if err := sch.Validate(doc); err != nil {
		if verr, ok := err.(*jsonschema.ValidationError); ok {
			walkValidationErrors(verr, r)
			r.SchemaOK = false
			return nil
		}
		r.AddFindingf(SeverityError, "schema/violation", "", "%v", err)
		r.SchemaOK = false
		return nil
	}
	r.SchemaOK = true
	return nil
}

// walkValidationErrors flattens the nested *jsonschema.ValidationError tree
// into Result findings, one per leaf cause.
func walkValidationErrors(verr *jsonschema.ValidationError, r *Result) {
	if len(verr.Causes) == 0 {
		r.AddFinding(SeverityError, "schema/violation", verr.InstanceLocation, verr.Message)
		return
	}
	for _, c := range verr.Causes {
		walkValidationErrors(c, r)
	}
}
