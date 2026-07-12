package dataframeapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/catalog"
)

type FieldHint struct {
	ResourceType      string
	FieldRef          string
	Label             string
	Path              string
	Selector          FieldSelector
	Kind              string
	DocCount          int64
	SampleCount       int
	DistinctValues    []string
	DistinctTruncated bool
	PivotCandidate    bool
	PivotKind         string
	PivotColumns      []string
	PivotFamily       string
	PivotColumnSelect string
	PivotValueSelect  string
}

type FieldSelector struct {
	SourcePath string
	Where      *FieldPredicate
	ValuePath  string
}

type FieldPredicate struct {
	Path  string
	Op    string
	Value string
}

var fieldRefSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func discoveredFieldHints(resourceType string, fields []catalog.PopulatedField) []FieldHint {
	if len(fields) == 0 {
		return []FieldHint{}
	}
	out := make([]FieldHint, 0, len(fields))
	for _, field := range fields {
		ref := defaultFieldRef(resourceType, field.Path)
		out = append(out, fieldHintFromDiscovered(field, ref, defaultFieldLabel(field.Path), field.Path))
	}
	return out
}

func fieldHintFromDiscovered(field catalog.PopulatedField, fieldRef string, label string, selector string) FieldHint {
	selectorResp := decomposeSelector(selector)
	if spec, ok := fhirschema.LookupField(field.ResourceType, field.Path); ok {
		selectorResp = fieldSelectorFromSpec(fhirschema.SelectorFromField(spec))
	}
	return FieldHint{
		ResourceType:      field.ResourceType,
		FieldRef:          fieldRef,
		Label:             label,
		Path:              field.Path,
		Selector:          selectorResp,
		Kind:              field.Kind,
		DocCount:          field.DocCount,
		SampleCount:       field.SampleCount,
		DistinctValues:    cloneStrings(field.DistinctValues),
		DistinctTruncated: field.DistinctTruncated,
		PivotCandidate:    field.PivotCandidate,
		PivotKind:         field.PivotKind,
		PivotColumns:      cloneStrings(field.PivotColumns),
		PivotFamily:       field.PivotFamily,
		PivotColumnSelect: field.PivotColumnSelect,
		PivotValueSelect:  field.PivotValueSelect,
	}
}

func resolveFieldRef(resourceType string, discovered []catalog.PopulatedField, fieldRef string) (string, error) {
	fieldRef = strings.TrimSpace(fieldRef)
	if fieldRef == "" {
		return "", fmt.Errorf("fieldRef is required")
	}
	for _, field := range discovered {
		if defaultFieldRef(resourceType, field.Path) == fieldRef {
			return field.Path, nil
		}
	}
	return "", fmt.Errorf("unknown fieldRef %q for resourceType %q", fieldRef, resourceType)
}

func defaultFieldRef(resourceType string, path string) string {
	key := strings.ToLower(path)
	key = strings.ReplaceAll(key, "[]", "_")
	key = strings.ReplaceAll(key, ".", "_")
	key = fieldRefSanitizer.ReplaceAllString(key, "_")
	key = strings.Trim(key, "_")
	return resourceType + "." + key
}

func defaultFieldLabel(path string) string {
	path = strings.ReplaceAll(path, "[]", "")
	parts := strings.Split(path, ".")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func decomposeSelector(expression string) FieldSelector {
	sel, err := fhirschema.ParseSelector(expression)
	if err != nil {
		return FieldSelector{
			ValuePath: strings.TrimSpace(expression),
		}
	}
	sourcePath := ""
	valuePath := ""
	if len(sel.Steps) > 0 {
		last := len(sel.Steps) - 1
		valuePath = selectorStepText(sel.Steps[last])
		if last > 0 {
			sourceParts := make([]string, 0, last)
			for _, step := range sel.Steps[:last] {
				sourceParts = append(sourceParts, selectorStepText(step))
			}
			sourcePath = strings.Join(sourceParts, ".")
		}
	}
	var where *FieldPredicate
	if sel.Filter != nil {
		where = &FieldPredicate{
			Path:  sel.Filter.Field,
			Op:    fhirschema.PredicateContains,
			Value: sel.Filter.Needle,
		}
	}
	return FieldSelector{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  valuePath,
	}
}

func DecomposeSelector(expression string) FieldSelector {
	return decomposeSelector(expression)
}

func fieldSelectorFromSpec(spec fhirschema.FieldSelectorSpec) FieldSelector {
	var where *FieldPredicate
	if spec.Where != nil {
		where = &FieldPredicate{
			Path:  spec.Where.Path,
			Op:    spec.Where.Op,
			Value: spec.Where.Value,
		}
	}
	return FieldSelector{
		SourcePath: spec.SourcePath,
		Where:      where,
		ValuePath:  spec.ValuePath,
	}
}

func composeSelector(sourcePath string, wherePath string, whereOp string, whereValue string, valuePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	valuePath = strings.TrimSpace(valuePath)
	if valuePath == "" {
		return ""
	}
	path := valuePath
	if sourcePath != "" {
		path = sourcePath + "." + valuePath
	}
	wherePath = strings.TrimSpace(wherePath)
	whereOp = strings.TrimSpace(strings.ToUpper(whereOp))
	whereValue = strings.TrimSpace(whereValue)
	if wherePath == "" || whereOp == "" || whereValue == "" {
		return path
	}
	if whereOp == "CONTAINS" {
		return fmt.Sprintf(`%s where %s contains %q`, path, wherePath, whereValue)
	}
	return path
}

func selectorStepText(step fhirschema.SelectorStep) string {
	switch {
	case step.Iterate:
		return step.Field + "[]"
	case step.Index != nil:
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	default:
		return step.Field
	}
}
