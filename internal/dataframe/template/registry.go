package template

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

// Registry owns immutable, ordered template definitions.
type Registry struct {
	definitions []Definition
}

// NewRegistry validates and defensively copies definitions. The supplied
// order is the product order returned to clients.
func NewRegistry(definitions []Definition) (Registry, error) {
	if len(definitions) == 0 {
		return Registry{}, fmt.Errorf("template registry requires at least one definition")
	}
	seen := make(map[string]struct{}, len(definitions))
	validated := make([]Definition, 0, len(definitions))
	for _, definition := range definitions {
		if err := validateDefinition(definition); err != nil {
			return Registry{}, err
		}
		if _, ok := seen[definition.ID]; ok {
			return Registry{}, fmt.Errorf("duplicate template id %q", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		validated = append(validated, definition.clone())
	}
	return Registry{definitions: validated}, nil
}

// DefaultRegistry returns the six initial guided dataframe families.
func DefaultRegistry() Registry {
	registry, err := NewRegistry(defaultDefinitions())
	if err != nil {
		panic(err)
	}
	return registry
}

// Definitions returns a defensive copy in stable product order.
func (r Registry) Definitions() []Definition {
	definitions := make([]Definition, len(r.definitions))
	for i, definition := range r.definitions {
		definitions[i] = definition.clone()
	}
	return definitions
}

// Definition returns a defensive copy of one template.
func (r Registry) Definition(id string) (Definition, bool) {
	for _, definition := range r.definitions {
		if definition.ID == strings.TrimSpace(id) {
			return definition.clone(), true
		}
	}
	return Definition{}, false
}

func validateDefinition(definition Definition) error {
	if strings.TrimSpace(definition.ID) == "" {
		return fmt.Errorf("template id is required")
	}
	if definition.Version <= 0 {
		return fmt.Errorf("template %q version must be positive", definition.ID)
	}
	if strings.TrimSpace(definition.Label) == "" {
		return fmt.Errorf("template %q label is required", definition.ID)
	}
	if len(definition.RootCandidates) == 0 {
		return fmt.Errorf("template %q requires a root candidate", definition.ID)
	}
	for _, resourceType := range definition.RootCandidates {
		if !fhirschema.HasResource(resourceType) {
			return fmt.Errorf("template %q references unknown root resource type %q", definition.ID, resourceType)
		}
	}
	columnIDs := map[string]struct{}{}
	for _, suggestion := range definition.SuggestedColumns {
		if err := validateSuggestionID(definition.ID, "column", suggestion.ID); err != nil {
			return err
		}
		if _, ok := columnIDs[suggestion.ID]; ok {
			return fmt.Errorf("template %q duplicates column suggestion %q", definition.ID, suggestion.ID)
		}
		columnIDs[suggestion.ID] = struct{}{}
		if !nonEmpty(suggestion.FieldRefAlternatives) {
			return fmt.Errorf("template %q column %q requires fieldRef alternatives", definition.ID, suggestion.ID)
		}
	}
	traversalIDs := map[string]struct{}{}
	for _, suggestion := range definition.SuggestedTraversals {
		if err := validateSuggestionID(definition.ID, "traversal", suggestion.ID); err != nil {
			return err
		}
		if _, ok := traversalIDs[suggestion.ID]; ok {
			return fmt.Errorf("template %q duplicates traversal suggestion %q", definition.ID, suggestion.ID)
		}
		traversalIDs[suggestion.ID] = struct{}{}
		if strings.TrimSpace(suggestion.SemanticRole) == "" || len(suggestion.FromResourceTypes) == 0 || len(suggestion.ToResourceTypes) == 0 {
			return fmt.Errorf("template %q traversal %q requires semantic role and source/target types", definition.ID, suggestion.ID)
		}
		for _, resourceType := range append(cloneStrings(suggestion.FromResourceTypes), suggestion.ToResourceTypes...) {
			if !fhirschema.HasResource(resourceType) {
				return fmt.Errorf("template %q traversal %q references unknown resource type %q", definition.ID, suggestion.ID, resourceType)
			}
		}
	}
	pivotIDs := map[string]struct{}{}
	for _, suggestion := range definition.SuggestedPivots {
		if err := validateSuggestionID(definition.ID, "pivot", suggestion.ID); err != nil {
			return err
		}
		if _, ok := pivotIDs[suggestion.ID]; ok {
			return fmt.Errorf("template %q duplicates pivot suggestion %q", definition.ID, suggestion.ID)
		}
		pivotIDs[suggestion.ID] = struct{}{}
		if !nonEmpty(suggestion.FieldRefAlternatives) {
			return fmt.Errorf("template %q pivot %q requires fieldRef alternatives", definition.ID, suggestion.ID)
		}
	}
	return nil
}

func validateSuggestionID(templateID, kind, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("template %q %s suggestion id is required", templateID, kind)
	}
	return nil
}

func defaultDefinitions() []Definition {
	return []Definition{
		{
			ID: "patient-cohort", Version: 1, Label: "Patient cohort",
			Description:    "One row per patient with common demographics and related clinical facts.",
			RootCandidates: []string{"Patient"}, RowGrain: "patient",
			SuggestedColumns: []ColumnSuggestion{
				{ID: "patient-identifier", Label: "Patient identifier", FieldRefAlternatives: []string{"Patient.identifier_value", "Patient.id"}, DefaultSelected: true},
				{ID: "gender", Label: "Gender", FieldRefAlternatives: []string{"Patient.gender"}, DefaultSelected: true},
				{ID: "birth-date", Label: "Birth date", FieldRefAlternatives: []string{"Patient.birthdate"}, DefaultSelected: true},
				{ID: "deceased", Label: "Deceased", FieldRefAlternatives: []string{"Patient.deceasedboolean", "Patient.deceaseddatetime"}, Advanced: true},
			},
			SuggestedTraversals: []TraversalSuggestion{
				{ID: "diagnoses", Label: "Diagnoses", SemanticRole: "diagnosis", FromResourceTypes: []string{"Patient"}, ToResourceTypes: []string{"Condition"}},
				{ID: "specimens", Label: "Specimens", SemanticRole: "specimen", FromResourceTypes: []string{"Patient"}, ToResourceTypes: []string{"Specimen"}},
				{ID: "observations", Label: "Observations", SemanticRole: "observation", FromResourceTypes: []string{"Patient"}, ToResourceTypes: []string{"Observation"}, Advanced: true},
			},
		},
		{
			ID: "specimen-inventory", Version: 1, Label: "Specimen inventory",
			Description:    "One row per specimen with type, status, subject, and collection facts.",
			RootCandidates: []string{"Specimen"}, RowGrain: "specimen",
			SuggestedColumns: []ColumnSuggestion{
				{ID: "specimen-id", Label: "Specimen identifier", FieldRefAlternatives: []string{"Specimen.id", "Specimen.identifier_value"}, DefaultSelected: true},
				{ID: "specimen-type", Label: "Specimen type", FieldRefAlternatives: []string{"Specimen.type_coding_display", "Specimen.type_text"}, DefaultSelected: true},
				{ID: "specimen-status", Label: "Status", FieldRefAlternatives: []string{"Specimen.status"}, DefaultSelected: true},
				{ID: "specimen-subject", Label: "Subject", FieldRefAlternatives: []string{"Specimen.subject_reference"}, Advanced: true},
				{ID: "collected-at", Label: "Collected at", FieldRefAlternatives: []string{"Specimen.collection_collecteddatetime", "Specimen.collection_collectedperiod_start"}, Advanced: true},
			},
			SuggestedTraversals: []TraversalSuggestion{
				{ID: "files", Label: "Files", SemanticRole: "file", FromResourceTypes: []string{"Specimen"}, ToResourceTypes: []string{"DocumentReference"}},
			},
		},
		{
			ID: "file-manifest", Version: 1, Label: "File manifest",
			Description:    "One row per file-like DocumentReference with attachment metadata.",
			RootCandidates: []string{"DocumentReference"}, RowGrain: "file",
			SuggestedColumns: []ColumnSuggestion{
				{ID: "file-id", Label: "File identifier", FieldRefAlternatives: []string{"DocumentReference.id"}, DefaultSelected: true},
				{ID: "file-name", Label: "File name", FieldRefAlternatives: []string{"DocumentReference.content_attachment_title"}, DefaultSelected: true},
				{ID: "file-url", Label: "File URL", FieldRefAlternatives: []string{"DocumentReference.content_attachment_url"}, DefaultSelected: true},
				{ID: "content-type", Label: "Content type", FieldRefAlternatives: []string{"DocumentReference.content_attachment_contenttype"}, Advanced: true},
				{ID: "file-size", Label: "File size", FieldRefAlternatives: []string{"DocumentReference.content_attachment_size"}, Advanced: true},
				{ID: "file-subject", Label: "Subject", FieldRefAlternatives: []string{"DocumentReference.subject_reference"}, Advanced: true},
			},
			SuggestedTraversals: []TraversalSuggestion{
				{ID: "specimens", Label: "Specimens", SemanticRole: "specimen", FromResourceTypes: []string{"DocumentReference"}, ToResourceTypes: []string{"Specimen"}, Advanced: true},
			},
		},
		{
			ID: "diagnoses", Version: 1, Label: "Diagnoses",
			Description:    "One row per Condition with diagnosis code and clinical status.",
			RootCandidates: []string{"Condition"}, RowGrain: "diagnosis",
			SuggestedColumns: []ColumnSuggestion{
				{ID: "diagnosis-id", Label: "Diagnosis identifier", FieldRefAlternatives: []string{"Condition.id"}, DefaultSelected: true},
				{ID: "diagnosis-code", Label: "Diagnosis code", FieldRefAlternatives: []string{"Condition.code_coding_display", "Condition.code_text"}, DefaultSelected: true},
				{ID: "clinical-status", Label: "Clinical status", FieldRefAlternatives: []string{"Condition.clinicalstatus_coding_display"}, DefaultSelected: true},
				{ID: "verification-status", Label: "Verification status", FieldRefAlternatives: []string{"Condition.verificationstatus_coding_display"}, Advanced: true},
				{ID: "diagnosis-subject", Label: "Subject", FieldRefAlternatives: []string{"Condition.subject_reference"}, Advanced: true},
			},
		},
		{
			ID: "labs-observations", Version: 1, Label: "Labs and observations",
			Description:    "One row per Observation with code, value, timing, and optional bounded pivots.",
			RootCandidates: []string{"Observation"}, RowGrain: "observation",
			SuggestedColumns: []ColumnSuggestion{
				{ID: "observation-id", Label: "Observation identifier", FieldRefAlternatives: []string{"Observation.id"}, DefaultSelected: true},
				{ID: "observation-code", Label: "Observation code", FieldRefAlternatives: []string{"Observation.code_coding_display", "Observation.code_text"}, DefaultSelected: true},
				{ID: "value", Label: "Value", FieldRefAlternatives: []string{"Observation.valuequantity_value", "Observation.valuestring", "Observation.valueinteger", "Observation.valuedecimal"}, DefaultSelected: true},
				{ID: "unit", Label: "Unit", FieldRefAlternatives: []string{"Observation.valuequantity_unit"}, Advanced: true},
				{ID: "effective-at", Label: "Effective at", FieldRefAlternatives: []string{"Observation.effectivedatetime", "Observation.effectiveperiod_start"}, Advanced: true},
				{ID: "observation-subject", Label: "Subject", FieldRefAlternatives: []string{"Observation.subject_reference"}, Advanced: true},
			},
			SuggestedPivots: []PivotSuggestion{
				{ID: "observation-values", Label: "Observation values by code", FieldRefAlternatives: []string{"Observation.code"}, DefaultSelected: true},
			},
		},
		{
			ID: "study-enrollment", Version: 1, Label: "Study enrollment",
			Description:    "One row per ResearchSubject with subject and study relationship facts.",
			RootCandidates: []string{"ResearchSubject"}, RowGrain: "study_enrollment",
			SuggestedColumns: []ColumnSuggestion{
				{ID: "enrollment-id", Label: "Enrollment identifier", FieldRefAlternatives: []string{"ResearchSubject.id"}, DefaultSelected: true},
				{ID: "enrollment-status", Label: "Enrollment status", FieldRefAlternatives: []string{"ResearchSubject.status"}, DefaultSelected: true},
				{ID: "enrolled-patient", Label: "Patient", FieldRefAlternatives: []string{"ResearchSubject.patient_reference"}, DefaultSelected: true},
				{ID: "enrolled-study", Label: "Study", FieldRefAlternatives: []string{"ResearchSubject.study_reference"}, DefaultSelected: true},
				{ID: "period-start", Label: "Enrollment start", FieldRefAlternatives: []string{"ResearchSubject.period_start"}, Advanced: true},
				{ID: "period-end", Label: "Enrollment end", FieldRefAlternatives: []string{"ResearchSubject.period_end"}, Advanced: true},
			},
			SuggestedTraversals: []TraversalSuggestion{
				{ID: "study", Label: "Research study", SemanticRole: "study", FromResourceTypes: []string{"ResearchSubject"}, ToResourceTypes: []string{"ResearchStudy"}, DefaultSelected: true},
			},
		},
	}
}
