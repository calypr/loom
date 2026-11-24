package queryapi

import (
	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	"github.com/calypr/loom/internal/catalog"
)

func predicatePathFromInput(input *dataframemodel.FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Path
}

func predicateOpFromInput(input *dataframemodel.FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Op.String()
}

func predicateValueFromInput(input *dataframemodel.FhirFieldPredicateInput) string {
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
