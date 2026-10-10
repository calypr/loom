package schema

import (
	"fmt"
	"strings"
)

// QuantityIdentityPaths validates a scalar FHIR Quantity value and returns its
// sibling system and code paths.
func QuantityIdentityPaths(resourceType, valuePath string) (string, string, error) {
	valuePath = CanonicalizePath(valuePath)
	if !strings.HasSuffix(valuePath, ".value") {
		return "", "", fmt.Errorf("unit normalization path %q must select a FHIR Quantity value", valuePath)
	}
	if strings.Contains(valuePath, "[]") {
		return "", "", fmt.Errorf("unit normalization path %q is repeated; select one indexed Quantity first", valuePath)
	}
	base := strings.TrimSuffix(valuePath, ".value")
	quantity, quantityOK := ResolveFieldSemantics(resourceType, base)
	if !quantityOK || quantity.Kind != FieldKindObject || quantity.Reference != "Quantity" {
		return "", "", fmt.Errorf("unit normalization path %q must be owned by a FHIR Quantity", valuePath)
	}
	systemPath, codePath := base+".system", base+".code"
	valueMetadata, valueOK := ResolveTerminalScalarMetadata(resourceType, valuePath)
	systemMetadata, systemOK := ResolveTerminalScalarMetadata(resourceType, systemPath)
	codeMetadata, codeOK := ResolveTerminalScalarMetadata(resourceType, codePath)
	if !valueOK || (valueMetadata.Primitive != PrimitiveInteger && valueMetadata.Primitive != PrimitiveDecimal) {
		return "", "", fmt.Errorf("unit normalization path %q must resolve to an integer or decimal Quantity value", valuePath)
	}
	if !systemOK || systemMetadata.Primitive != PrimitiveString || !codeOK || codeMetadata.Primitive != PrimitiveString {
		return "", "", fmt.Errorf("unit normalization Quantity path %q must expose string system and code siblings", base)
	}
	return systemPath, codePath, nil
}
