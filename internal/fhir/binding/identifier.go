package binding

import (
	"fmt"
	"github.com/calypr/loom/internal/fhir/schema"
	"strings"
)

type IdentifierBinding struct {
	OwnerPath   string `json:"ownerPath"`
	SystemPath  string `json:"systemPath"`
	ValuePath   string `json:"valuePath"`
	SystemURI   string `json:"systemURI"`
	LogicalType string `json:"logicalType"`
}

type IdentifierBindingSpec struct {
	ResourceType   string
	OwnerSelector  schema.Selector
	OwnerResource  string
	SystemSelector schema.Selector
	ValueSelector  schema.Selector
	SystemURI      string
	LogicalType    string
	ValuePrimitive schema.PrimitiveKind
}

func ValidateIdentifierBinding(resourceType string, binding IdentifierBinding) (IdentifierBindingSpec, error) {
	if !schema.HasResource(resourceType) {
		return IdentifierBindingSpec{}, fmt.Errorf("resource type %q is not represented by generated FHIR schema", resourceType)
	}
	ownerPath := schema.CanonicalizePath(binding.OwnerPath)
	if ownerPath == "" {
		return IdentifierBindingSpec{}, fmt.Errorf("ownerPath is required")
	}
	ownerSelector, err := schema.ParseSelector(ownerPath)
	if err != nil {
		return IdentifierBindingSpec{}, fmt.Errorf("ownerPath: %w", err)
	}
	owner, ok := schema.ResolvePath(resourceType, ownerPath)
	if !ok || owner.Property.Kind != "array" || owner.Property.ItemRef != "Identifier" {
		return IdentifierBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to a repeated Identifier", ownerPath)
	}

	systemPath := schema.CanonicalizePath(binding.SystemPath)
	if systemPath != "system" {
		return IdentifierBindingSpec{}, fmt.Errorf("systemPath must select Identifier.system")
	}
	systemSelector, err := schema.ParseSelector(systemPath)
	if err != nil {
		return IdentifierBindingSpec{}, fmt.Errorf("systemPath: %w", err)
	}
	systemMetadata, ok := schema.ResolveTerminalScalarMetadata("Identifier", systemPath)
	if !ok || systemMetadata.Primitive != schema.PrimitiveString {
		return IdentifierBindingSpec{}, fmt.Errorf("systemPath %q must resolve to a string in Identifier", systemPath)
	}

	valuePath := schema.CanonicalizePath(binding.ValuePath)
	if valuePath != "value" {
		return IdentifierBindingSpec{}, fmt.Errorf("valuePath must select Identifier.value")
	}
	valueSelector, err := schema.ParseSelector(valuePath)
	if err != nil {
		return IdentifierBindingSpec{}, fmt.Errorf("valuePath: %w", err)
	}
	valueMetadata, ok := schema.ResolveTerminalScalarMetadata("Identifier", valuePath)
	if !ok || valueMetadata.Primitive != schema.PrimitiveString {
		return IdentifierBindingSpec{}, fmt.Errorf("valuePath %q must resolve to a string in Identifier", valuePath)
	}

	systemURI := strings.TrimSpace(binding.SystemURI)
	if systemURI == "" {
		return IdentifierBindingSpec{}, fmt.Errorf("systemURI is required")
	}
	logicalType := strings.ToLower(strings.TrimSpace(binding.LogicalType))
	if logicalType != "string" || !correlatedPrimitiveCompatible(logicalType, valueMetadata.Primitive, false) {
		return IdentifierBindingSpec{}, fmt.Errorf("logicalType %q is incompatible with Identifier.value", binding.LogicalType)
	}

	return IdentifierBindingSpec{
		ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: owner.Property.ItemRef,
		SystemSelector: systemSelector, ValueSelector: valueSelector, SystemURI: systemURI,
		LogicalType: logicalType, ValuePrimitive: valueMetadata.Primitive,
	}, nil
}
