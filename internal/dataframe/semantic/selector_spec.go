package semantic

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func selectorSpecFromSelector(sel spec.Selector) fhirschema.FieldSelectorSpec {
	sourcePath := ""
	valuePath := ""
	if len(sel.Steps) > 0 {
		last := len(sel.Steps) - 1
		valuePath = selectorStepText(sel.Steps[last])
		if last > 0 {
			parts := make([]string, 0, last)
			for _, step := range sel.Steps[:last] {
				parts = append(parts, selectorStepText(step))
			}
			sourcePath = strings.Join(parts, ".")
		}
	}
	var where *fhirschema.FieldPredicateSpec
	if sel.Filter != nil {
		where = &fhirschema.FieldPredicateSpec{Path: sel.Filter.Field, Op: fhirschema.PredicateContains, Value: sel.Filter.Needle}
	}
	return fhirschema.FieldSelectorSpec{SourcePath: sourcePath, Where: where, ValuePath: valuePath}
}

func selectorStepText(step spec.SelectorStep) string {
	text := step.Field
	if step.Iterate {
		text += "[]"
	}
	if step.Index != nil {
		text += "[" + fmt.Sprint(*step.Index) + "]"
	}
	return text
}
