package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

func selectorStepText(step SelectorStep) string {
	switch {
	case step.Iterate:
		return step.Field + "[]"
	case step.Index != nil:
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	default:
		return step.Field
	}
}

func sanitizeColumnName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func selectorSpecFromSelector(sel Selector) fhirschema.FieldSelectorSpec {
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
		where = &fhirschema.FieldPredicateSpec{
			Path:  sel.Filter.Field,
			Op:    fhirschema.PredicateContains,
			Value: sel.Filter.Needle,
		}
	}
	return fhirschema.FieldSelectorSpec{SourcePath: sourcePath, Where: where, ValuePath: valuePath}
}
