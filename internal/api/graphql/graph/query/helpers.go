package queryapi

import (
	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/catalog"
)

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func predicatePathFromInput(input *model.FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Path
}

func predicateOpFromInput(input *model.FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Op.String()
}

func predicateValueFromInput(input *model.FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Value
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func cloneTraversals(in []catalog.PopulatedReference) []catalog.PopulatedReference {
	if len(in) == 0 {
		return []catalog.PopulatedReference{}
	}
	return append([]catalog.PopulatedReference(nil), in...)
}
