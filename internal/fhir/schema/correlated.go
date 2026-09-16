package schema

import (
	"fmt"
	"strings"
)

// CorrelatedBinding is the backend-neutral description of one terminology
// key/value relationship. OwnerPath names the repeated FHIR item that owns the
// value; KeyPath names the repeated Coding collection within that item. System
// and Code are relative to one Coding item, so they cannot be accidentally
// paired from different array members.
type CorrelatedBinding struct {
	OwnerPath     string   `json:"ownerPath,omitempty"`
	KeyPath       string   `json:"keyPath"`
	SystemPath    string   `json:"systemPath"`
	CodePath      string   `json:"codePath"`
	ValuePath     string   `json:"valuePath"`
	ValueFallback []string `json:"valueFallback,omitempty"`
	ChoiceArms    []string `json:"choiceArms,omitempty"`
	LogicalType   string   `json:"logicalType"`
	UnitPath      string   `json:"unitPath,omitempty"`
}

// CorrelatedKey is the selected terminology identity for one correlated
// projection. It is deliberately separate from CorrelatedBinding, which only
// describes where the identity and value are structurally found.
type CorrelatedKey struct {
	System string `json:"system"`
	Code   string `json:"code"`
}

// CorrelatedBindingSpec is the checked form consumed by semantic and physical
// compilers. Selectors in this value are relative to the owning item unless
// OwnerSelector is empty (the root resource is the owner).
type CorrelatedBindingSpec struct {
	ResourceType   string
	OwnerSelector  Selector
	OwnerResource  string
	KeySelector    Selector
	KeyResource    string
	SystemSelector Selector
	CodeSelector   Selector
	ValueSelector  Selector
	ValueFallbacks []Selector
	ChoiceArms     []string
	LogicalType    string
	ValuePrimitive PrimitiveKind
	UnitSelector   *Selector
}

// ValidateCorrelatedBinding validates one binding against generated FHIR
// metadata and returns selectors suitable for lowering. It intentionally
// rejects an independently rooted system/code path and any key outside the
// declared owner scope.
func ValidateCorrelatedBinding(resourceType string, binding CorrelatedBinding) (CorrelatedBindingSpec, error) {
	if !HasResource(resourceType) {
		return CorrelatedBindingSpec{}, fmt.Errorf("resource type %q is not represented by generated FHIR schema", resourceType)
	}
	ownerPath := CanonicalizePath(binding.OwnerPath)
	keyPath := CanonicalizePath(binding.KeyPath)
	if keyPath == "" || !strings.Contains(keyPath, "[]") {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated keyPath must identify a repeated Coding item")
	}
	if strings.TrimSpace(binding.SystemPath) == "" || strings.TrimSpace(binding.CodePath) == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated binding requires systemPath and codePath")
	}
	if strings.TrimSpace(binding.ValuePath) == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated binding requires valuePath")
	}
	ownerResource := resourceType
	ownerSelector := Selector{}
	if ownerPath != "" {
		var err error
		ownerSelector, err = ParseSelector(ownerPath)
		if err != nil {
			return CorrelatedBindingSpec{}, fmt.Errorf("ownerPath: %w", err)
		}
		resolved, ok := ResolvePath(resourceType, ownerPath)
		if !ok || resolved.Property.Kind != "array" || strings.TrimSpace(resolved.Property.ItemRef) == "" {
			return CorrelatedBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to a repeated object", ownerPath)
		}
		ownerResource = resolved.Property.ItemRef
	}
	if ownerPath != "" && keyPath != ownerPath && !strings.HasPrefix(keyPath, ownerPath+".") {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q is outside ownerPath %q", keyPath, ownerPath)
	}
	keyRelative := keyPath
	if ownerPath != "" {
		keyRelative = strings.TrimPrefix(keyPath, ownerPath+".")
	}
	keyResolved, ok := ResolvePath(ownerResource, keyRelative)
	if !ok || keyResolved.Property.Kind != "array" || strings.TrimSpace(keyResolved.Property.ItemRef) == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q must resolve to a repeated object within owner scope", keyPath)
	}
	keyResource := keyResolved.Property.ItemRef
	system, err := ParseSelector(binding.SystemPath)
	if err != nil {
		return CorrelatedBindingSpec{}, fmt.Errorf("systemPath: %w", err)
	}
	code, err := ParseSelector(binding.CodePath)
	if err != nil {
		return CorrelatedBindingSpec{}, fmt.Errorf("codePath: %w", err)
	}
	if _, ok := ResolvePath(keyResource, CanonicalizePath(binding.SystemPath)); !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("systemPath %q is not a field of Coding item %q", binding.SystemPath, keyResource)
	}
	if _, ok := ResolvePath(keyResource, CanonicalizePath(binding.CodePath)); !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("codePath %q is not a field of Coding item %q", binding.CodePath, keyResource)
	}
	valuePath := CanonicalizePath(binding.ValuePath)
	valueResource := ownerResource
	if ownerPath == "" {
		valueResource = resourceType
	}
	valueSelectorPath := valuePath
	if _, ok := ResolvePath(valueResource, valueSelectorPath); !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("valuePath %q is not represented in owner resource %q", valuePath, valueResource)
	}
	value, err := ParseSelector(valueSelectorPath)
	if err != nil {
		return CorrelatedBindingSpec{}, fmt.Errorf("valuePath: %w", err)
	}
	fallbacks := make([]Selector, 0, len(binding.ValueFallback))
	for index, fallbackPath := range binding.ValueFallback {
		fallbackPath = CanonicalizePath(fallbackPath)
		if _, ok := ResolvePath(valueResource, fallbackPath); !ok {
			return CorrelatedBindingSpec{}, fmt.Errorf("valueFallback[%d] %q is not represented in owner resource %q", index, fallbackPath, valueResource)
		}
		fallback, parseErr := ParseSelector(fallbackPath)
		if parseErr != nil {
			return CorrelatedBindingSpec{}, fmt.Errorf("valueFallback[%d]: %w", index, parseErr)
		}
		fallbacks = append(fallbacks, fallback)
	}
	if strings.TrimSpace(binding.LogicalType) == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("logicalType is required")
	}
	logicalType, ok := normalizeCorrelatedLogicalType(binding.LogicalType)
	if !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("logicalType %q is unsupported", binding.LogicalType)
	}
	valueMetadata, ok := ResolveTerminalScalarMetadata(valueResource, valuePath)
	if !ok || valueMetadata.Primitive == PrimitiveUnknown {
		return CorrelatedBindingSpec{}, fmt.Errorf("valuePath %q does not resolve to a scalar in owner resource %q", valuePath, valueResource)
	}
	choiceArms := append([]string(nil), binding.ChoiceArms...)
	for index, arm := range choiceArms {
		choiceArms[index] = strings.TrimSpace(arm)
		if choiceArms[index] == "" {
			return CorrelatedBindingSpec{}, fmt.Errorf("choiceArms[%d] is empty", index)
		}
	}
	validChoiceArms := map[string]bool{}
	for _, option := range ChoiceValueSelectorOptions(valueResource) {
		path := CanonicalPath(option)
		if path == "" {
			continue
		}
		validChoiceArms[strings.TrimSuffix(strings.Split(path, ".")[0], "[]")] = true
	}
	for index, arm := range choiceArms {
		if !validChoiceArms[arm] {
			return CorrelatedBindingSpec{}, fmt.Errorf("choiceArms[%d] %q is not a value[x] arm of %s", index, arm, valueResource)
		}
	}
	valueArm := strings.TrimSuffix(strings.Split(valuePath, ".")[0], "[]")
	if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, valueArm) {
		return CorrelatedBindingSpec{}, fmt.Errorf("valuePath %q is outside declared choiceArms", valuePath)
	}
	if len(choiceArms) > 1 && logicalType != "string" {
		return CorrelatedBindingSpec{}, fmt.Errorf("mixed choice arms require an explicit string logicalType")
	}
	if !correlatedPrimitiveCompatible(logicalType, valueMetadata.Primitive, len(choiceArms) > 1) {
		return CorrelatedBindingSpec{}, fmt.Errorf("logicalType %q is incompatible with valuePath %q primitive %q", logicalType, valuePath, valueMetadata.Primitive)
	}
	for index, fallbackPath := range binding.ValueFallback {
		fallbackPath = CanonicalizePath(fallbackPath)
		fallbackMetadata, metadataOK := ResolveTerminalScalarMetadata(valueResource, fallbackPath)
		if !metadataOK || fallbackMetadata.Primitive == PrimitiveUnknown || !correlatedPrimitiveCompatible(logicalType, fallbackMetadata.Primitive, len(choiceArms) > 1) {
			return CorrelatedBindingSpec{}, fmt.Errorf("valueFallback[%d] %q is incompatible with logicalType %q", index, fallbackPath, logicalType)
		}
		fallbackArm := strings.TrimSuffix(strings.Split(fallbackPath, ".")[0], "[]")
		if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, fallbackArm) {
			return CorrelatedBindingSpec{}, fmt.Errorf("valueFallback[%d] %q is outside declared choiceArms", index, fallbackPath)
		}
	}
	var unit *Selector
	if strings.TrimSpace(binding.UnitPath) != "" {
		unitSelector, parseErr := ParseSelector(CanonicalizePath(binding.UnitPath))
		if parseErr != nil {
			return CorrelatedBindingSpec{}, fmt.Errorf("unitPath: %w", parseErr)
		}
		if _, ok := ResolvePath(valueResource, unitSelector.CanonicalPath()); !ok {
			return CorrelatedBindingSpec{}, fmt.Errorf("unitPath %q is not represented in owner resource %q", binding.UnitPath, valueResource)
		}
		unitMetadata, metadataOK := ResolveTerminalScalarMetadata(valueResource, unitSelector.CanonicalPath())
		if !metadataOK || unitMetadata.Primitive != PrimitiveString {
			return CorrelatedBindingSpec{}, fmt.Errorf("unitPath %q must resolve to a string", binding.UnitPath)
		}
		unit = &unitSelector
	}
	return CorrelatedBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: ownerResource, KeySelector: mustParseSelector(keyRelative), KeyResource: keyResource, SystemSelector: system, CodeSelector: code, ValueSelector: value, ValueFallbacks: fallbacks, ChoiceArms: choiceArms, LogicalType: logicalType, ValuePrimitive: valueMetadata.Primitive, UnitSelector: unit}, nil
}

func normalizeCorrelatedLogicalType(input string) (string, bool) {
	value := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input), "-", "_"))
	switch value {
	case "string", "code", "boolean", "integer", "decimal", "date", "date_time":
		return value, true
	case "number":
		return value, true
	default:
		return "", false
	}
}

func containsCorrelatedString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func correlatedPrimitiveCompatible(logicalType string, primitive PrimitiveKind, explicitMixed bool) bool {
	if explicitMixed && logicalType == "string" {
		return primitive != PrimitiveUnknown
	}
	switch logicalType {
	case "string", "code":
		return primitive == PrimitiveString
	case "boolean":
		return primitive == PrimitiveBoolean
	case "integer":
		return primitive == PrimitiveInteger
	case "decimal":
		return primitive == PrimitiveDecimal
	case "number":
		return primitive == PrimitiveDecimal || primitive == PrimitiveInteger
	case "date":
		return primitive == PrimitiveDate
	case "date_time":
		return primitive == PrimitiveDateTime
	default:
		return false
	}
}

func mustParseSelector(path string) Selector {
	selector, _ := ParseSelector(path)
	return selector
}
