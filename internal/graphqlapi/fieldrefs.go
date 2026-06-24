package graphqlapi

import (
	"fmt"
	"regexp"
	"strings"

	"arangodb-proto/internal/fhirschema"
	"arangodb-proto/internal/fhirsemantics"
	"arangodb-proto/internal/proto"
)

type FieldHintResponse struct {
	ResourceType      string
	FieldRef          string
	Label             string
	Path              string
	Selector          FieldSelectorResponse
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

type FieldSelectorResponse struct {
	SourcePath string
	Where      *FieldPredicateResponse
	ValuePath  string
}

type FieldPredicateResponse struct {
	Path  string
	Op    string
	Value string
}

var fieldRefSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func discoveredFieldHints(resourceType string, fields []proto.PopulatedField) []FieldHintResponse {
	if len(fields) == 0 {
		return []FieldHintResponse{}
	}
	out := make([]FieldHintResponse, 0, len(fields)+8)
	seen := map[string]struct{}{}
	for _, spec := range fhirsemantics.AliasesForResource(resourceType) {
		base := findFieldByPath(fields, fhirschema.CanonicalPath(spec.Selector))
		if base == nil {
			continue
		}
		out = append(out, fieldHintFromSemantic(*base, spec))
		seen[spec.FieldRef] = struct{}{}
	}
	for _, field := range fields {
		ref := defaultFieldRef(resourceType, field.Path)
		if _, ok := seen[ref]; ok {
			continue
		}
		out = append(out, fieldHintFromDiscovered(field, ref, defaultFieldLabel(field.Path), field.Path))
	}
	return out
}

func fieldHintFromSemantic(field proto.PopulatedField, spec fhirsemantics.FieldSpec) FieldHintResponse {
	return FieldHintResponse{
		ResourceType:      field.ResourceType,
		FieldRef:          spec.FieldRef,
		Label:             spec.Label,
		Path:              field.Path,
		Selector:          fieldSelectorResponseFromSpec(spec.Selector),
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

func fieldHintFromDiscovered(field proto.PopulatedField, fieldRef string, label string, selector string) FieldHintResponse {
	selectorResp := decomposeSelector(selector)
	if spec, ok := fhirschema.LookupField(field.ResourceType, field.Path); ok {
		selectorResp = fieldSelectorResponseFromSpec(fhirschema.SelectorFromField(spec))
	}
	return FieldHintResponse{
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

func resolveFieldRef(resourceType string, discovered []proto.PopulatedField, fieldRef string) (string, error) {
	fieldRef = strings.TrimSpace(fieldRef)
	if fieldRef == "" {
		return "", fmt.Errorf("fieldRef is required")
	}
	if spec, ok := fhirsemantics.ResolveFieldRef(resourceType, fieldRef); ok {
		return fhirschema.SelectorExpression(spec.Selector), nil
	}
	for _, field := range discovered {
		if defaultFieldRef(resourceType, field.Path) == fieldRef {
			return field.Path, nil
		}
	}
	return "", fmt.Errorf("unknown fieldRef %q for resourceType %q", fieldRef, resourceType)
}

func findFieldByPath(fields []proto.PopulatedField, path string) *proto.PopulatedField {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
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

func decomposeSelector(expression string) FieldSelectorResponse {
	sel, err := fhirschema.ParseSelector(expression)
	if err != nil {
		return FieldSelectorResponse{
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
	var where *FieldPredicateResponse
	if sel.Filter != nil {
		where = &FieldPredicateResponse{
			Path:  sel.Filter.Field,
			Op:    fhirschema.PredicateContains,
			Value: sel.Filter.Needle,
		}
	}
	return FieldSelectorResponse{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  valuePath,
	}
}

func fieldSelectorResponseFromSpec(spec fhirschema.FieldSelectorSpec) FieldSelectorResponse {
	var where *FieldPredicateResponse
	if spec.Where != nil {
		where = &FieldPredicateResponse{
			Path:  spec.Where.Path,
			Op:    spec.Where.Op,
			Value: spec.Where.Value,
		}
	}
	return FieldSelectorResponse{
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
