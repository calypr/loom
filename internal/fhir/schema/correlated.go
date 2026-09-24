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
	OwnerPath         string               `json:"ownerPath,omitempty"`
	KeyPath           string               `json:"keyPath"`
	SystemPath        string               `json:"systemPath"`
	CodePath          string               `json:"codePath"`
	ValueScope        CorrelatedValueScope `json:"valueScope,omitempty"`
	ValuePath         string               `json:"valuePath"`
	ValueFallback     []string             `json:"valueFallback,omitempty"`
	ValuePresentation string               `json:"valuePresentation,omitempty"`
	ChoiceArms        []string             `json:"choiceArms,omitempty"`
	LogicalType       string               `json:"logicalType"`
	UnitPath          string               `json:"unitPath,omitempty"`
}

// CorrelatedValueScope identifies the object that owns a correlated value.
// OWNER preserves the historical code-to-neighboring-value relationship.
// KEY_ITEM projects a member of the exact Coding item that matched system and
// code, which is the lossless representation of categorical FHIR values.
type CorrelatedValueScope string

const (
	CorrelatedValueOwner   CorrelatedValueScope = "OWNER"
	CorrelatedValueKeyItem CorrelatedValueScope = "KEY_ITEM"
)

// CanonicalValuePath returns the resource-root path of the selected value.
// Runtime selectors stay relative to their lexical owner, while capability
// candidates and authoring validation address fields from the resource root.
func (b CorrelatedBinding) CanonicalValuePath() string {
	base := b.OwnerPath
	if CorrelatedValueScope(strings.ToUpper(strings.TrimSpace(string(b.ValueScope)))) == CorrelatedValueKeyItem {
		base = b.KeyPath
	}
	base = CanonicalizePath(base)
	value := CanonicalizePath(b.ValuePath)
	if base == "" || value == base || strings.HasPrefix(value, base+".") {
		return value
	}
	if value == "" {
		return base
	}
	return base + "." + value
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
	OwnerPath         string   `json:"ownerPath,omitempty"`
	URLPath           []string `json:"urlPath"`
	ValuePath         string   `json:"valuePath"`
	LogicalType       string   `json:"logicalType"`
	ChoiceArms        []string `json:"choiceArms,omitempty"`
	ValueFallback     []string `json:"valueFallback,omitempty"`
	ValuePresentation string   `json:"valuePresentation,omitempty"`
	UnitPath          string   `json:"unitPath,omitempty"`
}

// CorrelatedBindingSpec is the checked form consumed by semantic and physical
// compilers. Selectors in this value are relative to the owning item unless
// OwnerSelector is empty (the root resource is the owner).
type CorrelatedBindingSpec struct {
	ResourceType      string
	OwnerSelector     Selector
	OwnerResource     string
	KeySelector       Selector
	KeyResource       string
	SystemSelector    Selector
	CodeSelector      Selector
	ValueScope        CorrelatedValueScope
	ValueSelector     Selector
	ValueFallbacks    []Selector
	ChoiceArms        []string
	LogicalType       string
	ValuePrimitive    PrimitiveKind
	ValuePresentation string
	ValueRepeated     bool
	UnitSelector      *Selector
}

// ExtensionBindingSpec is the checked extension form consumed by semantic and
// physical lowering. OwnerSelector addresses the repeated item immediately
// before the first extension boundary; URLSelectors are each relative to the
// current Extension item and therefore preserve nested parent identity.
type ExtensionBindingSpec struct {
	ResourceType      string
	OwnerSelector     Selector
	OwnerResource     string
	URLSelectors      []Selector
	ValueSelector     Selector
	ValueFallbacks    []Selector
	ChoiceArms        []string
	LogicalType       string
	ValuePrimitive    PrimitiveKind
	ValuePresentation string
	ValueRepeated     bool
	UnitSelector      *Selector
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
	if keyPath == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated keyPath must identify a Coding item")
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
		if !ok {
			return CorrelatedBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to an object", ownerPath)
		}
		ownerResource = resolved.Property.Ref
		if resolved.Property.Kind == "array" {
			ownerResource = resolved.Property.ItemRef
		}
		if ownerResource == "" {
			return CorrelatedBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to an object", ownerPath)
		}
	}
	if ownerPath != "" && keyPath != ownerPath && !strings.HasPrefix(keyPath, ownerPath+".") {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q is outside ownerPath %q", keyPath, ownerPath)
	}
	keyRelative := keyPath
	if ownerPath != "" {
		keyRelative = strings.TrimPrefix(keyPath, ownerPath+".")
	}
	keyResolved, ok := ResolvePath(ownerResource, keyRelative)
	if !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q must resolve to Coding within owner scope", keyPath)
	}
	keyResource := keyResolved.Property.Ref
	if keyResolved.Property.Kind == "array" {
		keyResource = keyResolved.Property.ItemRef
	}
	if keyResource != "Coding" {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q must resolve to Coding within owner scope", keyPath)
	}
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
	valueScope := CorrelatedValueScope(strings.ToUpper(strings.TrimSpace(string(binding.ValueScope))))
	if valueScope == "" {
		valueScope = CorrelatedValueOwner
	}
	if valueScope != CorrelatedValueOwner && valueScope != CorrelatedValueKeyItem {
		return CorrelatedBindingSpec{}, fmt.Errorf("valueScope %q is unsupported", binding.ValueScope)
	}
	valueResource := ownerResource
	if ownerPath == "" {
		valueResource = resourceType
	}
	if valueScope == CorrelatedValueKeyItem {
		valueResource = keyResource
	}
	value, valueErr := validateBindingValue(valueResource, binding.ValuePath, binding.ValueFallback, binding.ChoiceArms, binding.LogicalType, binding.UnitPath)
	if valueErr != nil {
		return CorrelatedBindingSpec{}, valueErr
	}
	if err := validateValuePresentation(valueResource, binding.ValuePath, binding.ValuePresentation, binding.ValueFallback); err != nil {
		return CorrelatedBindingSpec{}, err
	}
	return CorrelatedBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: ownerResource, KeySelector: mustParseSelector(keyRelative), KeyResource: keyResource, SystemSelector: system, CodeSelector: code, ValueScope: valueScope, ValueSelector: value.ValueSelector, ValueFallbacks: value.ValueFallbacks, ChoiceArms: value.ChoiceArms, LogicalType: value.LogicalType, ValuePrimitive: value.ValuePrimitive, ValuePresentation: binding.ValuePresentation, ValueRepeated: value.ValueRepeated, UnitSelector: value.UnitSelector}, nil
}

const ValuePresentationDisplayOrCode = "DISPLAY_OR_CODE"

func validateValuePresentation(resourceType, path, presentation string, fallbacks []string) error {
	if presentation == "" {
		return nil
	}
	if presentation != ValuePresentationDisplayOrCode || len(fallbacks) != 0 {
		return fmt.Errorf("unsupported value presentation %q or conflicting fallbacks", presentation)
	}
	parent, member, found := strings.Cut(path, ".")
	if last := strings.LastIndex(path, "."); last >= 0 {
		parent, member, found = path[:last], path[last+1:], true
	}
	owner := resourceType
	if found {
		resolved, ok := ResolvePath(resourceType, parent)
		if !ok {
			return fmt.Errorf("value presentation parent %q is unavailable", parent)
		}
		owner = resolved.Property.Ref
		if resolved.Property.Kind == "array" {
			owner = resolved.Property.ItemRef
		}
	} else {
		member = path
	}
	if owner != "Coding" || member != "code" {
		return fmt.Errorf("DISPLAY_OR_CODE requires the code member of a Coding, got %s.%s", resourceType, path)
	}
	return nil
}

type checkedBindingValue struct {
	ValueSelector  Selector
	ValueFallbacks []Selector
	ChoiceArms     []string
	LogicalType    string
	ValuePrimitive PrimitiveKind
	ValueRepeated  bool
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
	if !ok {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q does not resolve to a scalar in owner resource %q", valuePath, valueResource)
	}
	if logicalType == "object" {
		shape, found := ResolveFieldSemantics(valueResource, valuePath)
		if !found || (shape.Kind != FieldKindObject && !(shape.Kind == FieldKindArray && shape.ElementKind == FieldKindObject)) {
			return checkedBindingValue{}, fmt.Errorf("valuePath %q is not a structured value in %s", valuePath, valueResource)
		}
		if len(valueFallback) != 0 || unitPath != "" {
			return checkedBindingValue{}, fmt.Errorf("structured values retain their complete object and cannot declare scalar fallbacks or units")
		}
		valueMetadata.Primitive = ""
	} else if valueMetadata.Primitive == PrimitiveUnknown {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q requires object logicalType", valuePath)
	}
	choiceArms := append([]string(nil), requestedChoiceArms...)
	for index, arm := range choiceArms {
		choiceArms[index] = strings.TrimSpace(arm)
		if choiceArms[index] == "" {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] is empty", index)
		}
	}
	valueArm := strings.TrimSuffix(strings.Split(valuePath, ".")[0], "[]")
	valueArmMetadata, valueArmExists := ResolvePath(valueResource, valueArm)
	valueChoiceGroup := ""
	if valueArmExists {
		valueChoiceGroup = strings.TrimSpace(valueArmMetadata.Property.ChoiceGroup)
	}
	for index, arm := range choiceArms {
		resolved, exists := ResolvePath(valueResource, arm)
		choiceGroup := ""
		if exists {
			choiceGroup = strings.TrimSpace(resolved.Property.ChoiceGroup)
		}
		if choiceGroup == "" {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] %q is not a choice arm of %s", index, arm, valueResource)
		}
		if valueChoiceGroup == "" || choiceGroup != valueChoiceGroup {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] %q is outside valuePath %q choice group", index, arm, valuePath)
		}
	}
	if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, valueArm) {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q is outside declared choiceArms", valuePath)
	}
	if len(choiceArms) > 1 && logicalType != "string" {
		return checkedBindingValue{}, fmt.Errorf("mixed choice arms require an explicit string logicalType")
	}
	if logicalType != "object" && !correlatedPrimitiveCompatible(logicalType, valueMetadata.Primitive, len(choiceArms) > 1) {
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
	if strings.TrimSpace(unitPath) == "" {
		if inferred, ok := inferQuantityUnitSelector(valueResource, valuePath); ok {
			unit = &inferred
		}
	} else {
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
	return checkedBindingValue{ValueSelector: value, ValueFallbacks: fallbacks, ChoiceArms: choiceArms, LogicalType: logicalType, ValuePrimitive: valueMetadata.Primitive, ValueRepeated: valueMetadata.Repeated, UnitSelector: unit}, nil
}

// inferQuantityUnitSelector retains the display unit owned by the same FHIR
// Quantity as a selected numeric value. The generated datatype reference is
// the authority; a coincidental sibling named "unit" on another structure is
// not treated as a measurement unit.
func inferQuantityUnitSelector(valueResource, valuePath string) (Selector, bool) {
	valuePath = CanonicalizePath(valuePath)
	separator := strings.LastIndex(valuePath, ".")
	if separator < 1 || valuePath[separator+1:] != "value" {
		return Selector{}, false
	}
	parentPath := valuePath[:separator]
	parent, ok := ResolvePath(valueResource, parentPath)
	if !ok || parent.PropertyRef != "Quantity" {
		return Selector{}, false
	}
	unitPath := parentPath + ".unit"
	metadata, ok := ResolveTerminalScalarMetadata(valueResource, unitPath)
	if !ok || metadata.Primitive != PrimitiveString {
		return Selector{}, false
	}
	unit, err := ParseSelector(unitPath)
	return unit, err == nil
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
	if err := validateValuePresentation("Extension", binding.ValuePath, binding.ValuePresentation, binding.ValueFallback); err != nil {
		return ExtensionBindingSpec{}, err
	}
	urlSelectors := make([]Selector, extensionDepth)
	for index := range urlSelectors {
		urlSelectors[index], _ = ParseSelector("url")
	}
	return ExtensionBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: "Extension", URLSelectors: urlSelectors, ValueSelector: value.ValueSelector, ValueFallbacks: value.ValueFallbacks, ChoiceArms: value.ChoiceArms, LogicalType: value.LogicalType, ValuePrimitive: value.ValuePrimitive, ValuePresentation: binding.ValuePresentation, ValueRepeated: value.ValueRepeated, UnitSelector: value.UnitSelector}, nil
}

func normalizeCorrelatedLogicalType(input string) (string, bool) {
	value := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input), "-", "_"))
	switch value {
	case "string", "code", "boolean", "integer", "decimal", "date", "date_time", "object":
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
