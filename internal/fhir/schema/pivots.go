package schema

import (
	"fmt"
	"strings"
)

type PivotSpec struct {
	Family           string
	CatalogRootPath  string
	ItemSourcePath   string
	ItemResourceType string
	ColumnSelector   FieldSelectorSpec
	ValueSelector    FieldSelectorSpec
	ValueSelectors   []FieldSelectorSpec
}

const (
	PivotFamilyCodeableConcept      = "CODEABLE_CONCEPT"
	PivotFamilyObservationCodeValue = "OBSERVATION_CODE_VALUE"
)

func ChoiceValueSelectorOptions(resourceType string) []FieldSelectorSpec {
	candidates := []string{
		"valueQuantity.value", "valueCodeableConcept.text", "valueCodeableConcept.coding[].display",
		"valueString", "valueInteger", "valueBoolean", "valueDecimal", "valueDateTime", "valueTime",
		"valuePeriod.start", "valuePeriod.end", "valueRange.low.value", "valueRange.high.value",
		"valueRatio.numerator.value", "valueRatio.denominator.value",
	}
	out := make([]FieldSelectorSpec, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := LookupField(resourceType, CanonicalizePath(candidate)); !ok {
			continue
		}
		out = append(out, FieldSelectorSpecFromPath(candidate))
	}
	return out
}

func prependSelector(existing []FieldSelectorSpec, preferred FieldSelectorSpec) []FieldSelectorSpec {
	preferredPath := CanonicalPath(preferred)
	result := []FieldSelectorSpec{preferred}
	for _, candidate := range existing {
		if CanonicalPath(candidate) != preferredPath {
			result = append(result, candidate)
		}
	}
	return result
}

func normalizePivotSelectors(resourceType, catalogRootPath string, column, value FieldSelectorSpec) (FieldSelectorSpec, FieldSelectorSpec, string, string) {
	itemType := resourceType
	itemSource := ""
	if source, repeatedType, ok := repeatedPivotItemScope(resourceType, catalogRootPath); ok {
		itemType = repeatedType
		itemSource = source
		column = relativeSelector(column, itemSource)
		value = relativeSelector(value, itemSource)
	}
	return column, value, itemType, itemSource
}

func repeatedPivotItemScope(resourceType, canonicalPath string) (string, string, bool) {
	parts := strings.Split(CanonicalizePath(canonicalPath), ".")
	for index := len(parts) - 1; index >= 0; index-- {
		if !strings.HasSuffix(parts[index], "[]") {
			continue
		}
		source := strings.Join(parts[:index+1], ".")
		resolved, ok := ResolvePath(resourceType, source)
		if !ok || resolved.Property.Kind != "array" || strings.TrimSpace(resolved.Property.ItemRef) == "" || index+1 >= len(parts) {
			continue
		}
		itemType := resolved.Property.ItemRef
		relative := strings.Join(parts[index+1:], ".")
		if _, ok := ResolvePath(itemType, relative); ok {
			return source, itemType, true
		}
	}
	return "", "", false
}

func relativeSelector(selector FieldSelectorSpec, prefix string) FieldSelectorSpec {
	prefix = CanonicalizePath(prefix)
	path := CanonicalPath(selector)
	if strings.HasPrefix(path, prefix+".") {
		return FieldSelectorSpecFromPath(strings.TrimPrefix(path, prefix+"."))
	}
	return selector
}

func ValidatePivotSelectors(resourceType string, column FieldSelectorSpec, value FieldSelectorSpec) (PivotSpec, error) {
	columnExpr := SelectorExpression(column)
	valueExpr := SelectorExpression(value)
	columnCanonical := CanonicalPath(column)
	valueCanonical := CanonicalPath(value)
	if strings.TrimSpace(columnCanonical) == "" {
		return PivotSpec{}, fmt.Errorf("pivot column selector is required")
	}
	if strings.TrimSpace(valueCanonical) == "" {
		return PivotSpec{}, fmt.Errorf("pivot value selector is required")
	}
	if match, ok := resolvePivotFamily(resourceType, columnCanonical, valueCanonical); ok {
		column, value, itemType, itemSource := normalizePivotSelectors(resourceType, match.catalogRootPath, column, value)
		return PivotSpec{Family: match.family, CatalogRootPath: match.catalogRootPath, ItemSourcePath: itemSource, ItemResourceType: itemType, ColumnSelector: normalizeSelectorSpec(column, columnExpr), ValueSelector: normalizeSelectorSpec(value, valueExpr), ValueSelectors: []FieldSelectorSpec{normalizeSelectorSpec(value, valueExpr)}}, nil
	}
	return PivotSpec{}, fmt.Errorf("unsupported pivot selector pair %q / %q for resourceType %q", columnExpr, valueExpr, resourceType)
}

func DefaultPivotSpec(resourceType, canonicalPath string, observedValuePath string) (PivotSpec, bool) {
	canonicalPath = CanonicalizePath(canonicalPath)
	if source, itemType, ok := repeatedPivotItemScope(resourceType, canonicalPath); ok {
		relativeRoot := strings.TrimPrefix(canonicalPath, source+".")
		if !ResolvesToCodeableConcept(itemType, relativeRoot) {
			return PivotSpec{}, false
		}
		column := defaultCodeableColumnSelector(itemType, relativeRoot)
		values := ChoiceValueSelectorOptions(itemType)
		if strings.TrimSpace(observedValuePath) != "" {
			values = prependSelector(values, FieldSelectorSpecFromPath(observedValuePath))
		}
		if len(values) == 0 {
			return PivotSpec{}, false
		}
		return PivotSpec{Family: PivotFamilyCodeableConcept, CatalogRootPath: canonicalPath, ItemSourcePath: source, ItemResourceType: itemType, ColumnSelector: column, ValueSelector: values[0], ValueSelectors: values}, true
	}
	if ResolvesToCodeableConcept(resourceType, canonicalPath) {
		column := defaultCodeableColumnSelector(resourceType, canonicalPath)
		values := []FieldSelectorSpec{column}
		if canonicalPath == "code" {
			options := ChoiceValueSelectorOptions(resourceType)
			if strings.TrimSpace(observedValuePath) != "" {
				values = prependSelector(options, FieldSelectorSpecFromPath(observedValuePath))
			} else if len(options) > 0 {
				values = options
			}
		}
		spec, err := ValidatePivotSelectors(resourceType, column, values[0])
		if err == nil {
			spec.ValueSelectors = values
		}
		return spec, err == nil
	}
	return PivotSpec{}, false
}

func defaultCodeableColumnSelector(resourceType, rootPath string) FieldSelectorSpec {
	rootPath = strings.TrimSuffix(CanonicalizePath(rootPath), ".")
	if rootPath != "code" {
		return FieldSelectorSpecFromPath(rootPath + ".coding[].display")
	}
	textPath := rootPath + ".text"
	if _, ok := LookupField(resourceType, textPath); ok {
		return FieldSelectorSpecFromPath(textPath)
	}
	return FieldSelectorSpecFromPath(rootPath + ".coding[].display")
}

func codeableConceptRoots(resourceType, canonicalPath string) ([]string, bool) {
	parts := strings.Split(canonicalPath, ".")
	for i := len(parts); i > 0; i-- {
		root := strings.Join(parts[:i], ".")
		if ResolvesToCodeableConcept(resourceType, root) {
			return []string{root}, true
		}
	}
	return nil, false
}

type pivotFamilyMatch struct {
	family          string
	catalogRootPath string
}

func resolvePivotFamily(resourceType, columnCanonical, valueCanonical string) (pivotFamilyMatch, bool) {
	for _, resolver := range []func(string, string, string) (pivotFamilyMatch, bool){matchObservationCodeValuePivot, matchSharedCodeableConceptPivot} {
		if match, ok := resolver(resourceType, columnCanonical, valueCanonical); ok {
			return match, true
		}
	}
	return pivotFamilyMatch{}, false
}

func matchObservationCodeValuePivot(resourceType, columnCanonical, valueCanonical string) (pivotFamilyMatch, bool) {
	if ResolvesToCodeableConcept(resourceType, "code") && isObservationCodeSelector(columnCanonical) && isObservationValueSelector(valueCanonical) {
		if _, valueExists := ResolvePath(resourceType, valueCanonical); !valueExists {
			return pivotFamilyMatch{}, false
		}
		return pivotFamilyMatch{family: PivotFamilyObservationCodeValue, catalogRootPath: "code"}, true
	}
	return pivotFamilyMatch{}, false
}

func matchSharedCodeableConceptPivot(resourceType, columnCanonical, valueCanonical string) (pivotFamilyMatch, bool) {
	roots, ok := codeableConceptRoots(resourceType, columnCanonical)
	if !ok {
		return pivotFamilyMatch{}, false
	}
	valueRoots, valueOK := codeableConceptRoots(resourceType, valueCanonical)
	if !valueOK {
		return pivotFamilyMatch{}, false
	}
	for _, root := range roots {
		if slicesContains(valueRoots, root) {
			return pivotFamilyMatch{family: PivotFamilyCodeableConcept, catalogRootPath: root}, true
		}
	}
	return pivotFamilyMatch{}, false
}

func isObservationCodeSelector(canonicalPath string) bool {
	return canonicalPath == "code" || strings.HasPrefix(canonicalPath, "code.")
}

func isObservationValueSelector(canonicalPath string) bool {
	return canonicalPath == "valueString" || canonicalPath == "valueInteger" || canonicalPath == "valueBoolean" || canonicalPath == "valueDecimal" || canonicalPath == "valueDateTime" || canonicalPath == "valueTime" || strings.HasPrefix(canonicalPath, "valueQuantity.") || strings.HasPrefix(canonicalPath, "valueCodeableConcept.") || strings.HasPrefix(canonicalPath, "valuePeriod.") || strings.HasPrefix(canonicalPath, "valueRange.") || strings.HasPrefix(canonicalPath, "valueRatio.")
}

func normalizeSelectorSpec(spec FieldSelectorSpec, expr string) FieldSelectorSpec {
	if strings.TrimSpace(spec.ValuePath) != "" {
		return spec
	}
	return FieldSelectorSpecFromPath(expr)
}

func slicesContains(in []string, want string) bool {
	for _, item := range in {
		if item == want {
			return true
		}
	}
	return false
}
