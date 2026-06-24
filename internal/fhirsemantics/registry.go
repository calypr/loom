package fhirsemantics

import (
	"strings"

	"arangodb-proto/internal/fhirschema"
)

const (
	NormalizationNone                     = ""
	NormalizationDocumentReferenceSummary = "DOCUMENT_REFERENCE_SUMMARY"

	TraversalRolePatientNeighborChild      = "PATIENT_NEIGHBOR_CHILD"
	TraversalRolePatientDirectChild        = "PATIENT_DIRECT_CHILD"
	TraversalRolePatientDocumentReference  = "PATIENT_DOCUMENT_REFERENCE"
	TraversalRoleSpecimenGroup             = "SPECIMEN_GROUP"
	TraversalRoleSpecimenDocumentReference = "SPECIMEN_DOCUMENT_REFERENCE"
	TraversalRoleGroupDocumentReference    = "GROUP_DOCUMENT_REFERENCE"
)

type TraversalSpec struct {
	FromType                   string
	EdgeLabel                  string
	ToType                     string
	Role                       string
	SetName                    string
	SharedRootNeighborEligible bool
}

type FieldSpec struct {
	ResourceType   string
	FieldRef       string
	Label          string
	Selector       fhirschema.FieldSelectorSpec
	Normalization  string
	NormalizedName string
}

type AliasSpec = FieldSpec

var aliases = map[string][]FieldSpec{
	"Patient": {
		field("Patient", "Patient.case_id", "Case ID", selector("identifier[]", whereContains("system", "case_id"), "value")),
		field("Patient", "Patient.case_submitter_id", "Case Submitter ID", selector("identifier[]", whereContains("system", "case_submitter_id"), "value")),
		field("Patient", "Patient.gender", "Gender", selector("", nil, "gender")),
		field("Patient", "Patient.deceased", "Deceased", selector("", nil, "deceasedBoolean")),
		field("Patient", "Patient.race", "Race", selector("extension[]", whereContains("url", "us-core-race"), "valueString")),
		field("Patient", "Patient.ethnicity", "Ethnicity", selector("extension[]", whereContains("url", "us-core-ethnicity"), "valueString")),
		field("Patient", "Patient.birth_sex", "Birth Sex", selector("extension[]", whereContains("url", "us-core-birthsex"), "valueCode")),
		field("Patient", "Patient.patient_age", "Patient Age", selector("extension[]", whereContains("url", "Patient-age"), "valueQuantity.value")),
		field("Patient", "Patient.part_of_study", "Part Of Study", selector("extension[]", whereContains("url", "part-of-study"), "valueReference.reference")),
	},
	"Condition": {
		field("Condition", "Condition.id", "Condition ID", selector("", nil, "id")),
		field("Condition", "Condition.diagnosis", "Diagnosis", selector("code.coding[]", nil, "display")),
		field("Condition", "Condition.body_site", "Body Site", selector("bodySite[].coding[]", nil, "display")),
	},
	"Specimen": {
		field("Specimen", "Specimen.id", "Specimen ID", selector("", nil, "id")),
		field("Specimen", "Specimen.type_display", "Specimen Type", selector("type.coding[]", nil, "display")),
		field("Specimen", "Specimen.preservation_method", "Preservation Method", selector("processing[].method.coding[]", whereContains("system", "preservation_method"), "display")),
	},
	"ResearchSubject": {
		field("ResearchSubject", "ResearchSubject.id", "Research Subject ID", selector("", nil, "id")),
		field("ResearchSubject", "ResearchSubject.status", "Status", selector("", nil, "status")),
		field("ResearchSubject", "ResearchSubject.study_ref", "Study Reference", selector("study", nil, "reference")),
	},
	"DocumentReference": {
		normalizedField("DocumentReference", "DocumentReference.file_id", "File ID", selector("identifier[]", whereContains("system", "file_id"), "value"), "file_id"),
		normalizedField("DocumentReference", "DocumentReference.file_name", "File Name", selector("content[].attachment", nil, "title"), "file_name"),
		normalizedField("DocumentReference", "DocumentReference.file_url", "File URL", selector("content[].attachment", nil, "url"), "file_url"),
		normalizedField("DocumentReference", "DocumentReference.file_size", "File Size", selector("content[].attachment", nil, "size"), "file_size"),
		normalizedField("DocumentReference", "DocumentReference.data_category", "Data Category", selector("category[].coding[]", whereContains("system", "data_category"), "display"), "data_category"),
		normalizedField("DocumentReference", "DocumentReference.data_type", "Data Type", selector("category[].coding[]", whereContains("system", "data_type"), "display"), "data_type"),
		normalizedField("DocumentReference", "DocumentReference.experimental_strategy", "Experimental Strategy", selector("category[].coding[]", whereContains("system", "experimental_strategy"), "display"), "experimental_strategy"),
		normalizedField("DocumentReference", "DocumentReference.workflow_type", "Workflow Type", selector("category[].coding[]", whereContains("system", "workflow_type"), "display"), "workflow_type"),
		normalizedField("DocumentReference", "DocumentReference.platform", "Platform", selector("category[].coding[]", whereContains("system", "platform"), "display"), "platform"),
		normalizedField("DocumentReference", "DocumentReference.access", "Access", selector("category[].coding[]", whereContains("system", "access"), "display"), "access"),
		normalizedField("DocumentReference", "DocumentReference.data_format", "Data Format", selector("type.coding[]", nil, "display"), "data_format"),
	},
}

var traversals = map[string]TraversalSpec{
	traversalKey("Patient", "subject_Patient", "Condition"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Condition",
		Role: TraversalRolePatientNeighborChild, SetName: "patient_condition_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "subject_Patient", "ResearchSubject"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "ResearchSubject",
		Role: TraversalRolePatientNeighborChild, SetName: "patient_research_subject_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "subject_Patient", "Specimen"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Specimen",
		Role: TraversalRolePatientNeighborChild, SetName: "patient_specimen_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "subject_Patient", "MedicationAdministration"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "MedicationAdministration",
		Role: TraversalRolePatientNeighborChild, SetName: "patient_medication_administration_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "subject_Patient", "Observation"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Observation",
		Role: TraversalRolePatientNeighborChild, SetName: "patient_subject_observation_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "subject_Patient", "ImagingStudy"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "ImagingStudy",
		Role: TraversalRolePatientNeighborChild, SetName: "patient_imaging_study_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "subject_Patient", "DocumentReference"): {
		FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "DocumentReference",
		Role: TraversalRolePatientDocumentReference, SetName: "patient_document_reference_set", SharedRootNeighborEligible: true,
	},
	traversalKey("Patient", "focus_Patient", "Observation"): {
		FromType: "Patient", EdgeLabel: "focus_Patient", ToType: "Observation",
		Role: TraversalRolePatientDirectChild, SetName: "patient_focus_observation_set",
	},
	traversalKey("Patient", "member_entity_Patient", "Group"): {
		FromType: "Patient", EdgeLabel: "member_entity_Patient", ToType: "Group",
		Role: TraversalRolePatientDirectChild, SetName: "patient_group_set",
	},
	traversalKey("Specimen", "member_entity_Specimen", "Group"): {
		FromType: "Specimen", EdgeLabel: "member_entity_Specimen", ToType: "Group",
		Role: TraversalRoleSpecimenGroup, SetName: "specimen_group_set",
	},
	traversalKey("Specimen", "subject_Specimen", "DocumentReference"): {
		FromType: "Specimen", EdgeLabel: "subject_Specimen", ToType: "DocumentReference",
		Role: TraversalRoleSpecimenDocumentReference, SetName: "specimen_document_reference_set",
	},
	traversalKey("Group", "subject_Group", "DocumentReference"): {
		FromType: "Group", EdgeLabel: "subject_Group", ToType: "DocumentReference",
		Role: TraversalRoleGroupDocumentReference, SetName: "group_document_reference_set",
	},
}

func field(resourceType, fieldRef, label string, sel fhirschema.FieldSelectorSpec) FieldSpec {
	return FieldSpec{
		ResourceType: resourceType,
		FieldRef:     fieldRef,
		Label:        label,
		Selector:     sel,
	}
}

func normalizedField(resourceType, fieldRef, label string, sel fhirschema.FieldSelectorSpec, normalized string) FieldSpec {
	spec := field(resourceType, fieldRef, label, sel)
	spec.Normalization = NormalizationDocumentReferenceSummary
	spec.NormalizedName = normalized
	return spec
}

func selector(sourcePath string, predicate *fhirschema.FieldPredicateSpec, valuePath string) fhirschema.FieldSelectorSpec {
	return fhirschema.FieldSelectorSpec{
		SourcePath: strings.TrimSpace(sourcePath),
		Where:      predicate,
		ValuePath:  strings.TrimSpace(valuePath),
	}
}

func whereContains(path, value string) *fhirschema.FieldPredicateSpec {
	return &fhirschema.FieldPredicateSpec{Path: path, Op: fhirschema.PredicateContains, Value: value}
}

func traversalKey(fromType, edgeLabel, toType string) string {
	return fromType + "|" + edgeLabel + "|" + toType
}

func AliasesForResource(resourceType string) []AliasSpec {
	specs := aliases[resourceType]
	out := make([]AliasSpec, len(specs))
	copy(out, specs)
	return out
}

func ResolveFieldRef(resourceType, fieldRef string) (FieldSpec, bool) {
	for _, spec := range aliases[resourceType] {
		if spec.FieldRef == fieldRef {
			return spec, true
		}
	}
	return FieldSpec{}, false
}

func ResolveTraversal(fromType, edgeLabel, toType string) (TraversalSpec, bool) {
	spec, ok := traversals[traversalKey(fromType, edgeLabel, toType)]
	return spec, ok
}

func DocumentReferenceSummaryField(selectorExpr string) (string, bool) {
	parsed, err := fhirschema.ParseSelector(selectorExpr)
	if err != nil {
		return "", false
	}
	for _, spec := range aliases["DocumentReference"] {
		if spec.Normalization != NormalizationDocumentReferenceSummary || spec.NormalizedName == "" {
			continue
		}
		if fhirschema.CanonicalPath(spec.Selector) != parsed.CanonicalPath() {
			continue
		}
		if !samePredicate(spec.Selector.Where, parsed.Filter) {
			continue
		}
		return spec.NormalizedName, true
	}
	return "", false
}

func SelectorNeedsDocumentReferenceSummary(selectorExpr string) bool {
	_, ok := DocumentReferenceSummaryField(selectorExpr)
	return ok
}

func RequiresResearchStudyHydration(selectorExpr string, fieldRef string) bool {
	if strings.TrimSpace(fieldRef) == "ResearchSubject.study_ref" {
		return false
	}
	parsed, err := fhirschema.ParseSelector(selectorExpr)
	if err != nil {
		return false
	}
	return strings.HasPrefix(parsed.CanonicalPath(), "study.")
}

func samePredicate(a *fhirschema.FieldPredicateSpec, b *fhirschema.ContainsFilter) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.Path), strings.TrimSpace(b.Field)) &&
		strings.EqualFold(strings.TrimSpace(a.Op), fhirschema.PredicateContains) &&
		strings.TrimSpace(a.Value) == strings.TrimSpace(b.Needle)
}
