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

// ExtensionBinding is the closed authoring shape for an ancestor-aware FHIR
// extension lookup. OwnerPath names the full repeated path ending at the
// terminal extension (for example extension[].extension[]); URLPath contains
// one selected URL for each extension boundary. ValuePath and its optional
// fallbacks are relative to the terminal Extension object.
//
// URLPath is deliberately a literal sequence rather than a predicate or a
// flattened URL string. This keeps each ancestor identity in the same lexical
// traversal scope as the value it owns.
type ExtensionBinding struct {
	OwnerPath     string   `json:"ownerPath,omitempty"`
	URLPath       []string `json:"urlPath"`
	ValuePath     string   `json:"valuePath"`
	LogicalType   string   `json:"logicalType"`
	ChoiceArms    []string `json:"choiceArms,omitempty"`
	ValueFallback []string `json:"valueFallback,omitempty"`
	UnitPath      string   `json:"unitPath,omitempty"`
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

// ExtensionBindingSpec is the checked extension form consumed by semantic and
// physical lowering. OwnerSelector addresses the repeated item immediately
// before the first extension boundary; URLSelectors are each relative to the
// current Extension item and therefore preserve nested parent identity.
type ExtensionBindingSpec struct {
	ResourceType   string
	OwnerSelector  Selector
	OwnerResource  string
	URLSelectors   []Selector
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
	valueResource := ownerResource
	if ownerPath == "" {
		valueResource = resourceType
	}
	value, valueErr := validateBindingValue(valueResource, binding.ValuePath, binding.ValueFallback, binding.ChoiceArms, binding.LogicalType, binding.UnitPath)
	if valueErr != nil {
		return CorrelatedBindingSpec{}, valueErr
	}
	return CorrelatedBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: ownerResource, KeySelector: mustParseSelector(keyRelative), KeyResource: keyResource, SystemSelector: system, CodeSelector: code, ValueSelector: value.ValueSelector, ValueFallbacks: value.ValueFallbacks, ChoiceArms: value.ChoiceArms, LogicalType: value.LogicalType, ValuePrimitive: value.ValuePrimitive, UnitSelector: value.UnitSelector}, nil
}

type checkedBindingValue struct {
	ValueSelector  Selector
	ValueFallbacks []Selector
	ChoiceArms     []string
	LogicalType    string
	ValuePrimitive PrimitiveKind
	UnitSelector   *Selector
}

// validateBindingValue is shared by terminology and extension bindings. The
// generated schema, not a client-declared label, owns primitive compatibility
// and valid value[x] arms.
func validateBindingValue(valueResource, valuePath string, valueFallback []string, requestedChoiceArms []string, requestedLogicalType, unitPath string) (checkedBindingValue, error) {
	valuePath = CanonicalizePath(valuePath)
	if valuePath == "" {
		return checkedBindingValue{}, fmt.Errorf("valuePath is required")
	}
	if _, ok := ResolvePath(valueResource, valuePath); !ok {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q is not represented in owner resource %q", valuePath, valueResource)
	}
	value, err := ParseSelector(valuePath)
	if err != nil {
		return checkedBindingValue{}, fmt.Errorf("valuePath: %w", err)
	}
	if strings.TrimSpace(requestedLogicalType) == "" {
		return checkedBindingValue{}, fmt.Errorf("logicalType is required")
	}
	logicalType, ok := normalizeCorrelatedLogicalType(requestedLogicalType)
	if !ok {
		return checkedBindingValue{}, fmt.Errorf("logicalType %q is unsupported", requestedLogicalType)
	}
	valueMetadata, ok := ResolveTerminalScalarMetadata(valueResource, valuePath)
	if !ok || valueMetadata.Primitive == PrimitiveUnknown {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q does not resolve to a scalar in owner resource %q", valuePath, valueResource)
	}
	choiceArms := append([]string(nil), requestedChoiceArms...)
	for index, arm := range choiceArms {
		choiceArms[index] = strings.TrimSpace(arm)
		if choiceArms[index] == "" {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] is empty", index)
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
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] %q is not a value[x] arm of %s", index, arm, valueResource)
		}
	}
	valueArm := strings.TrimSuffix(strings.Split(valuePath, ".")[0], "[]")
	if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, valueArm) {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q is outside declared choiceArms", valuePath)
	}
	if len(choiceArms) > 1 && logicalType != "string" {
		return checkedBindingValue{}, fmt.Errorf("mixed choice arms require an explicit string logicalType")
	}
	if !correlatedPrimitiveCompatible(logicalType, valueMetadata.Primitive, len(choiceArms) > 1) {
		return checkedBindingValue{}, fmt.Errorf("logicalType %q is incompatible with valuePath %q primitive %q", logicalType, valuePath, valueMetadata.Primitive)
	}
	fallbacks := make([]Selector, 0, len(valueFallback))
	for index, fallbackPath := range valueFallback {
		fallbackPath = CanonicalizePath(fallbackPath)
		if _, ok := ResolvePath(valueResource, fallbackPath); !ok {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d] %q is not represented in owner resource %q", index, fallbackPath, valueResource)
		}
		fallback, parseErr := ParseSelector(fallbackPath)
		if parseErr != nil {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d]: %w", index, parseErr)
		}
		fallbackMetadata, metadataOK := ResolveTerminalScalarMetadata(valueResource, fallbackPath)
		if !metadataOK || fallbackMetadata.Primitive == PrimitiveUnknown || !correlatedPrimitiveCompatible(logicalType, fallbackMetadata.Primitive, len(choiceArms) > 1) {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d] %q is incompatible with logicalType %q", index, fallbackPath, logicalType)
		}
		fallbackArm := strings.TrimSuffix(strings.Split(fallbackPath, ".")[0], "[]")
		if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, fallbackArm) {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d] %q is outside declared choiceArms", index, fallbackPath)
		}
		fallbacks = append(fallbacks, fallback)
	}
	var unit *Selector
	if strings.TrimSpace(unitPath) != "" {
		unitSelector, parseErr := ParseSelector(CanonicalizePath(unitPath))
		if parseErr != nil {
			return checkedBindingValue{}, fmt.Errorf("unitPath: %w", parseErr)
		}
		if _, ok := ResolvePath(valueResource, unitSelector.CanonicalPath()); !ok {
			return checkedBindingValue{}, fmt.Errorf("unitPath %q is not represented in owner resource %q", unitPath, valueResource)
		}
		unitMetadata, metadataOK := ResolveTerminalScalarMetadata(valueResource, unitSelector.CanonicalPath())
		if !metadataOK || unitMetadata.Primitive != PrimitiveString {
			return checkedBindingValue{}, fmt.Errorf("unitPath %q must resolve to a string", unitPath)
		}
		unit = &unitSelector
	}
	return checkedBindingValue{ValueSelector: value, ValueFallbacks: fallbacks, ChoiceArms: choiceArms, LogicalType: logicalType, ValuePrimitive: valueMetadata.Primitive, UnitSelector: unit}, nil
}

// ValidateExtensionBinding validates the ancestor chain and terminal value
// against generated FHIR metadata. Every path segment before the first
// extension boundary is scoped normally; once an extension boundary begins,
// only nested extension[] segments are accepted. This makes URLPath depth and
// lexical traversal unambiguous.
func ValidateExtensionBinding(resourceType string, binding ExtensionBinding) (ExtensionBindingSpec, error) {
	if !HasResource(resourceType) {
		return ExtensionBindingSpec{}, fmt.Errorf("resource type %q is not represented by generated FHIR schema", resourceType)
	}
	ownerPath := CanonicalizePath(binding.OwnerPath)
	if ownerPath == "" {
		return ExtensionBindingSpec{}, fmt.Errorf("ownerPath is required")
	}
	parts := strings.Split(ownerPath, ".")
	baseParts := make([]string, 0, len(parts))
	currentResource := resourceType
	startedExtension := false
	extensionDepth := 0
	for index, part := range parts {
		resolved, ok := ResolvePath(currentResource, part)
		if !ok {
			return ExtensionBindingSpec{}, fmt.Errorf("ownerPath segment %q is not represented in resource %q", part, currentResource)
		}
		name := strings.TrimSuffix(part, "[]")
		isExtension := name == "extension" && strings.HasSuffix(part, "[]")
		if isExtension {
			if resolved.Property.Kind != "array" || resolved.Property.ItemRef != "Extension" {
				return ExtensionBindingSpec{}, fmt.Errorf("ownerPath segment %q must be a repeated Extension", part)
			}
			startedExtension = true
			extensionDepth++
			currentResource = "Extension"
			continue
		}
		if startedExtension {
			return ExtensionBindingSpec{}, fmt.Errorf("ownerPath segment %q follows an extension boundary and is not allowed", part)
		}
		if !strings.HasSuffix(part, "[]") && index == len(parts)-1 {
			return ExtensionBindingSpec{}, fmt.Errorf("ownerPath must end at a repeated extension")
		}
		baseParts = append(baseParts, part)
		switch resolved.Property.Kind {
		case "array":
			if strings.TrimSpace(resolved.Property.ItemRef) == "" {
				return ExtensionBindingSpec{}, fmt.Errorf("ownerPath segment %q must resolve to a repeated object", part)
			}
			currentResource = resolved.Property.ItemRef
		case "object":
			if strings.TrimSpace(resolved.Property.Ref) == "" {
				return ExtensionBindingSpec{}, fmt.Errorf("ownerPath segment %q must resolve to a named object", part)
			}
			currentResource = resolved.Property.Ref
		default:
			return ExtensionBindingSpec{}, fmt.Errorf("ownerPath segment %q is not an object boundary", part)
		}
	}
	if extensionDepth == 0 {
		return ExtensionBindingSpec{}, fmt.Errorf("ownerPath must contain at least one repeated extension")
	}
	if len(binding.URLPath) != extensionDepth {
		return ExtensionBindingSpec{}, fmt.Errorf("urlPath must contain one URL per extension boundary (got %d, want %d)", len(binding.URLPath), extensionDepth)
	}
	urls := make([]string, len(binding.URLPath))
	for index, value := range binding.URLPath {
		urls[index] = strings.TrimSpace(value)
		if urls[index] == "" {
			return ExtensionBindingSpec{}, fmt.Errorf("urlPath[%d] is empty", index)
		}
	}
	ownerSelector := Selector{}
	if len(baseParts) > 0 {
		basePath := strings.Join(baseParts, ".")
		var err error
		ownerSelector, err = ParseSelector(basePath)
		if err != nil {
			return ExtensionBindingSpec{}, fmt.Errorf("ownerPath base: %w", err)
		}
		resolved, ok := ResolvePath(resourceType, basePath)
		if !ok || resolved.Property.Kind != "array" || strings.TrimSpace(resolved.Property.ItemRef) == "" {
			return ExtensionBindingSpec{}, fmt.Errorf("ownerPath base %q must resolve to a repeated object", basePath)
		}
	}
	value, err := validateBindingValue("Extension", binding.ValuePath, binding.ValueFallback, binding.ChoiceArms, binding.LogicalType, binding.UnitPath)
	if err != nil {
		return ExtensionBindingSpec{}, err
	}
	urlSelectors := make([]Selector, extensionDepth)
	for index := range urlSelectors {
		urlSelectors[index], _ = ParseSelector("url")
	}
	return ExtensionBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: "Extension", URLSelectors: urlSelectors, ValueSelector: value.ValueSelector, ValueFallbacks: value.ValueFallbacks, ChoiceArms: value.ChoiceArms, LogicalType: value.LogicalType, ValuePrimitive: value.ValuePrimitive, UnitSelector: value.UnitSelector}, nil
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
