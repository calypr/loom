package dataframe

import (
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

type documentReferenceSummarySpec struct {
	Selector    fhirschema.FieldSelectorSpec
	SummaryName string
}

var documentReferenceSummarySpecs = []documentReferenceSummarySpec{
	{Selector: selector("identifier[]", whereContains("system", "file_id"), "value"), SummaryName: "file_id"},
	{Selector: selector("content[].attachment", nil, "title"), SummaryName: "file_name"},
	{Selector: selector("content[].attachment", nil, "url"), SummaryName: "file_url"},
	{Selector: selector("content[].attachment", nil, "size"), SummaryName: "file_size"},
	{Selector: selector("category[].coding[]", whereContains("system", "data_category"), "display"), SummaryName: "data_category"},
	{Selector: selector("category[].coding[]", whereContains("system", "data_type"), "display"), SummaryName: "data_type"},
	{Selector: selector("category[].coding[]", whereContains("system", "experimental_strategy"), "display"), SummaryName: "experimental_strategy"},
	{Selector: selector("category[].coding[]", whereContains("system", "workflow_type"), "display"), SummaryName: "workflow_type"},
	{Selector: selector("category[].coding[]", whereContains("system", "platform"), "display"), SummaryName: "platform"},
	{Selector: selector("category[].coding[]", whereContains("system", "access"), "display"), SummaryName: "access"},
	{Selector: selector("type.coding[]", nil, "display"), SummaryName: "data_format"},
}

func mapDocumentReferenceSelectorToSummaryField(selectorExpr string) (string, bool) {
	parsed, err := fhirschema.ParseSelector(selectorExpr)
	if err != nil {
		return "", false
	}
	for _, spec := range documentReferenceSummarySpecs {
		if fhirschema.CanonicalPath(spec.Selector) != parsed.CanonicalPath() {
			continue
		}
		if !sameContainsPredicate(spec.Selector.Where, parsed.Filter) {
			continue
		}
		return spec.SummaryName, true
	}
	return "", false
}

func selectorNeedsDocumentReferenceSummary(selectorExpr string) bool {
	_, ok := mapDocumentReferenceSelectorToSummaryField(selectorExpr)
	return ok
}

func requiresResearchStudyHydration(selectorExpr string, fieldRef string) bool {
	if strings.TrimSpace(fieldRef) == "ResearchSubject.study_reference" {
		return false
	}
	parsed, err := fhirschema.ParseSelector(selectorExpr)
	if err != nil {
		return false
	}
	return strings.HasPrefix(parsed.CanonicalPath(), "study.")
}

func selector(sourcePath string, predicate *fhirschema.FieldPredicateSpec, valuePath string) fhirschema.FieldSelectorSpec {
	return fhirschema.FieldSelectorSpec{
		SourcePath: strings.TrimSpace(sourcePath),
		Where:      predicate,
		ValuePath:  strings.TrimSpace(valuePath),
	}
}

func whereContains(path, value string) *fhirschema.FieldPredicateSpec {
	return &fhirschema.FieldPredicateSpec{
		Path:  path,
		Op:    fhirschema.PredicateContains,
		Value: value,
	}
}

func sameContainsPredicate(expected *fhirschema.FieldPredicateSpec, actual *fhirschema.ContainsFilter) bool {
	if expected == nil && actual == nil {
		return true
	}
	if expected == nil || actual == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(expected.Path), strings.TrimSpace(actual.Field)) &&
		strings.EqualFold(strings.TrimSpace(expected.Op), fhirschema.PredicateContains) &&
		strings.TrimSpace(expected.Value) == strings.TrimSpace(actual.Needle)
}
