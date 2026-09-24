package schema

import (
	"fmt"
	"strings"
)

// CategoricalBinding describes a CodeableConcept/Coding namespace projection.
// The selected system is supplied separately by the authoring source; codes
// are values returned by the projection, never column identities.
type CategoricalBinding struct {
	OwnerPath         string   `json:"ownerPath,omitempty"`
	KeyPath           string   `json:"keyPath"`
	SystemPath        string   `json:"systemPath"`
	ValuePath         string   `json:"valuePath"`
	ValueFallback     []string `json:"valueFallback,omitempty"`
	LogicalType       string   `json:"logicalType"`
	ValuePresentation string   `json:"valuePresentation,omitempty"`
}

type CategoricalBindingSpec struct {
	ResourceType      string
	OwnerSelector     Selector
	OwnerResource     string
	KeySelector       Selector
	KeyResource       string
	SystemSelector    Selector
	ValueSelector     Selector
	ValueFallbacks    []Selector
	LogicalType       string
	ValuePrimitive    PrimitiveKind
	ValuePresentation string
	ValueRepeated     bool
}

// ValidateCategoricalBinding checks that the system and value selectors are
// evaluated against one Coding item, including singleton Coding shapes.
func ValidateCategoricalBinding(resourceType string, binding CategoricalBinding) (CategoricalBindingSpec, error) {
	if !HasResource(resourceType) {
		return CategoricalBindingSpec{}, fmt.Errorf("resource type %q is not represented by generated FHIR schema", resourceType)
	}
	ownerPath := CanonicalizePath(binding.OwnerPath)
	keyPath := CanonicalizePath(binding.KeyPath)
	if keyPath == "" {
		return CategoricalBindingSpec{}, fmt.Errorf("categorical keyPath must identify a Coding item")
	}
	if strings.TrimSpace(binding.SystemPath) == "" || strings.TrimSpace(binding.ValuePath) == "" {
		return CategoricalBindingSpec{}, fmt.Errorf("categorical binding requires systemPath and valuePath")
	}
	ownerResource := resourceType
	ownerSelector := Selector{}
	if ownerPath != "" {
		var err error
		ownerSelector, err = ParseSelector(ownerPath)
		if err != nil {
			return CategoricalBindingSpec{}, fmt.Errorf("ownerPath: %w", err)
		}
		resolved, ok := ResolvePath(resourceType, ownerPath)
		if !ok {
			return CategoricalBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to an object", ownerPath)
		}
		ownerResource = resolved.Property.Ref
		if resolved.Property.Kind == "array" {
			ownerResource = resolved.Property.ItemRef
		}
		if ownerResource == "" {
			return CategoricalBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to an object", ownerPath)
		}
	}
	if keyPath != ownerPath && ownerPath != "" && !strings.HasPrefix(keyPath, ownerPath+".") {
		return CategoricalBindingSpec{}, fmt.Errorf("keyPath %q is outside ownerPath %q", keyPath, ownerPath)
	}
	keyRelative := keyPath
	if ownerPath != "" {
		keyRelative = strings.TrimPrefix(keyPath, ownerPath+".")
	}
	keyResolved, ok := ResolvePath(ownerResource, keyRelative)
	if !ok {
		return CategoricalBindingSpec{}, fmt.Errorf("keyPath %q must resolve to Coding within owner scope", keyPath)
	}
	keyResource := keyResolved.Property.Ref
	if keyResolved.Property.Kind == "array" {
		keyResource = keyResolved.Property.ItemRef
	}
	if keyResource != "Coding" {
		return CategoricalBindingSpec{}, fmt.Errorf("keyPath %q must resolve to Coding, got %q", keyPath, keyResource)
	}
	keySelector, err := ParseSelector(keyRelative)
	if err != nil {
		return CategoricalBindingSpec{}, fmt.Errorf("keyPath: %w", err)
	}
	systemSelector, err := ParseSelector(CanonicalizePath(binding.SystemPath))
	if err != nil {
		return CategoricalBindingSpec{}, fmt.Errorf("systemPath: %w", err)
	}
	valueSelector, err := ParseSelector(CanonicalizePath(binding.ValuePath))
	if err != nil {
		return CategoricalBindingSpec{}, fmt.Errorf("valuePath: %w", err)
	}
	if systemSelector.CanonicalPath() != "system" || valueSelector.CanonicalPath() != "code" {
		return CategoricalBindingSpec{}, fmt.Errorf("categorical binding requires Coding.system and Coding.code")
	}
	systemMetadata, ok := ResolveTerminalScalarMetadata(keyResource, systemSelector.CanonicalPath())
	if !ok || systemMetadata.Primitive != PrimitiveString {
		return CategoricalBindingSpec{}, fmt.Errorf("systemPath %q must resolve to a string scalar on Coding", binding.SystemPath)
	}
	valueMetadata, ok := ResolveTerminalScalarMetadata(keyResource, valueSelector.CanonicalPath())
	if !ok || valueMetadata.Primitive == PrimitiveUnknown {
		return CategoricalBindingSpec{}, fmt.Errorf("valuePath %q must resolve to a scalar on Coding", binding.ValuePath)
	}
	fallbacks := make([]Selector, 0, len(binding.ValueFallback))
	for index, raw := range binding.ValueFallback {
		selector, parseErr := ParseSelector(CanonicalizePath(raw))
		if parseErr != nil {
			return CategoricalBindingSpec{}, fmt.Errorf("valueFallback[%d]: %w", index, parseErr)
		}
		if index != 0 || selector.CanonicalPath() != "display" {
			return CategoricalBindingSpec{}, fmt.Errorf("categorical value fallback must be Coding.display")
		}
		metadata, metadataOK := ResolveTerminalScalarMetadata(keyResource, selector.CanonicalPath())
		if !metadataOK || metadata.Primitive == PrimitiveUnknown {
			return CategoricalBindingSpec{}, fmt.Errorf("valueFallback[%d] %q must resolve to a scalar on Coding", index, raw)
		}
		fallbacks = append(fallbacks, selector)
	}
	presentation := strings.ToUpper(strings.TrimSpace(binding.ValuePresentation))
	if presentation != "" && presentation != ValuePresentationDisplayOrCode {
		return CategoricalBindingSpec{}, fmt.Errorf("unsupported categorical value presentation %q", binding.ValuePresentation)
	}
	logicalType := strings.TrimSpace(binding.LogicalType)
	if logicalType == "" {
		logicalType = string(valueMetadata.Primitive)
	}
	normalized, valid := normalizeCorrelatedLogicalType(logicalType)
	if !valid || !correlatedPrimitiveCompatible(normalized, valueMetadata.Primitive, false) {
		return CategoricalBindingSpec{}, fmt.Errorf("logicalType %q is incompatible with Coding.code", logicalType)
	}
	return CategoricalBindingSpec{
		ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: ownerResource,
		KeySelector: keySelector, KeyResource: keyResource, SystemSelector: systemSelector,
		ValueSelector: valueSelector, ValueFallbacks: fallbacks, LogicalType: logicalType,
		ValuePrimitive: valueMetadata.Primitive, ValuePresentation: presentation,
		ValueRepeated: valueMetadata.Repeated,
	}, nil
}
