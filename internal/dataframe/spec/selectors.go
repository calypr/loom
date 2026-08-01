package spec

import (
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type Selector = fhirschema.Selector
type SelectorStep = fhirschema.SelectorStep
type ContainsFilter = fhirschema.ContainsFilter

func ParseSelector(input string) (Selector, error) {
	return fhirschema.ParseSelector(input)
}
