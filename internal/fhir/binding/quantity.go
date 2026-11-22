package binding

import (
	"fmt"
	"github.com/calypr/loom/internal/fhir/schema"
	"strings"
)

func QuantityIdentityPaths(resourceType, valuePath string) (string, string, error) {
	valuePath = schema.CanonicalizePath(valuePath)
	if !strings.HasSuffix(valuePath, ".value") {
		return "", "", fmt.Errorf("unit normalization path %q must select a FHIR Quantity value", valuePath)
	}
	if strings.Contains(valuePath, "[]") {
		return "", "", fmt.Errorf("unit normalization path %q is repeated; select one indexed Quantity first", valuePath)
	}
	base := strings.TrimSuffix(valuePath, ".value")
	quantity, quantityOK := schema.ResolveFieldSemantics(resourceType, base)
	if !quantityOK || quantity.Kind != schema.FieldKindObject || quantity.Reference != "Quantity" {
		return "", "", fmt.Errorf("unit normalization path %q must be owned by a FHIR Quantity", valuePath)
	}
	systemPath, codePath := base+".system", base+".code"
	valueMetadata, valueOK := schema.ResolveTerminalScalarMetadata(resourceType, valuePath)
	systemMetadata, systemOK := schema.ResolveTerminalScalarMetadata(resourceType, systemPath)
	codeMetadata, codeOK := schema.ResolveTerminalScalarMetadata(resourceType, codePath)
	if !valueOK || (valueMetadata.Primitive != schema.PrimitiveInteger && valueMetadata.Primitive != schema.PrimitiveDecimal) {
		return "", "", fmt.Errorf("unit normalization path %q must resolve to an integer or decimal Quantity value", valuePath)
	}
	if !systemOK || systemMetadata.Primitive != schema.PrimitiveString || !codeOK || codeMetadata.Primitive != schema.PrimitiveString {
		return "", "", fmt.Errorf("unit normalization Quantity path %q must expose string system and code siblings", base)
	}
	return systemPath, codePath, nil
}
