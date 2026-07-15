package spec

import (
	"github.com/calypr/loom/fhirschema"
)

type Selector = fhirschema.Selector
type SelectorStep = fhirschema.SelectorStep
type ContainsFilter = fhirschema.ContainsFilter

func ParseSelector(input string) (Selector, error) {
	return fhirschema.ParseSelector(input)
}
