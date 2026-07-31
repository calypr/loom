package spec

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// ValidateTypedFilterForResource proves that a resolved filter selector has a
// compatible shape in the active generated FHIR schema. Runtime catalog
// validation remains responsible for whether that field is populated in the
// selected project; this function prevents a request from using a value type
// that cannot be represented by the generated resource definition.
func ValidateTypedFilterForResource(resourceType string, filter TypedFilter) error {
	if err := filter.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(filter.Selector) == "" {
		return fmt.Errorf("filter %q requires a resolved selector", filter.FieldRef)
	}
	selector, err := ParseSelector(filter.Selector)
	if err != nil {
		return fmt.Errorf("filter %q selector: %w", filter.FieldRef, err)
	}
	if _, _, err := selectorCardinality(resourceType, selector); err != nil {
		return fmt.Errorf("filter %q selector: %w", filter.FieldRef, err)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
	if !ok {
		return fmt.Errorf("filter selector %q is not represented by generated resource type %q", filter.Selector, resourceType)
	}
	if filter.Repeated != metadata.Repeated {
		return fmt.Errorf("filter selector %q repeated=%t does not match generated cardinality repeated=%t", filter.Selector, filter.Repeated, metadata.Repeated)
	}
	if !filterKindMatchesGeneratedPrimitive(filter.FieldKind, metadata.Primitive, selector.CanonicalPath()) {
		return fmt.Errorf("filter selector %q has generated primitive %q, incompatible with filter value kind %q", filter.Selector, metadata.Primitive, filter.FieldKind)
	}
	if filter.FieldKind == FilterCode {
		for _, value := range filter.Values {
			if value.Code == nil {
				continue
			}
			// The current selector compiler safely matches the terminal code. It
			// must not pretend an independently collected system/display belongs
			// to that same Coding array member; paired Coding lowering is added
			// only once represented explicitly in the physical expression IR.
			if strings.TrimSpace(value.Code.System) != "" || strings.TrimSpace(value.Code.Display) != "" {
				return fmt.Errorf("filter %q supplies code system/display, which requires paired Coding lowering not available in this compiler version", filter.FieldRef)
			}
		}
	}
	return nil
}

func filterKindMatchesGeneratedPrimitive(kind FilterValueKind, primitive fhirschema.PrimitiveKind, canonicalPath string) bool {
	switch primitive {
	case fhirschema.PrimitiveString:
		if kind == FilterString {
			return true
		}
		// A code filter is safe only when the resolved generated selector ends
		// in a code primitive. This remains deliberately conservative: fields
		// such as Patient.gender are strings, not terminology codes.
		return kind == FilterCode && (canonicalPath == "code" || strings.HasSuffix(canonicalPath, ".code"))
	case fhirschema.PrimitiveBoolean:
		return kind == FilterBoolean
	case fhirschema.PrimitiveInteger:
		return kind == FilterInteger
	case fhirschema.PrimitiveDecimal:
		return kind == FilterDecimal
	case fhirschema.PrimitiveDate:
		return kind == FilterDate
	case fhirschema.PrimitiveDateTime:
		return kind == FilterDateTime
	default:
		return false
	}
}
