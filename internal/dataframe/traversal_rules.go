package dataframe

import "github.com/calypr/loom/internal/fhirschema"

type traversalRole string

const (
	traversalRolePatientNeighborChild      traversalRole = "PATIENT_NEIGHBOR_CHILD"
	traversalRolePatientDirectChild        traversalRole = "PATIENT_DIRECT_CHILD"
	traversalRolePatientDocumentReference  traversalRole = "PATIENT_DOCUMENT_REFERENCE"
	traversalRoleSpecimenGroup             traversalRole = "SPECIMEN_GROUP"
	traversalRoleSpecimenDocumentReference traversalRole = "SPECIMEN_DOCUMENT_REFERENCE"
	traversalRoleGroupDocumentReference    traversalRole = "GROUP_DOCUMENT_REFERENCE"
)

type plannerTraversal struct {
	Schema                     fhirschema.TraversalSpec
	Role                       traversalRole
	SetName                    string
	SharedRootNeighborEligible bool
}

func lookupPlannerTraversal(fromType, edgeLabel, toType string) (plannerTraversal, bool) {
	spec, ok := fhirschema.LookupTraversal(fromType, edgeLabel, toType)
	if !ok {
		return plannerTraversal{}, false
	}
	rule, ok := classifyPlannerTraversal(spec)
	if !ok {
		return plannerTraversal{}, false
	}
	return rule, true
}

func classifyPlannerTraversal(spec fhirschema.TraversalSpec) (plannerTraversal, bool) {
	switch traversalKey(spec.FromType, spec.EdgeLabel, spec.ToType) {
	case traversalKey("Patient", "subject_Patient", "Condition"):
		return patientNeighborTraversal(spec, "patient_condition_set"), true
	case traversalKey("Patient", "subject_Patient", "ResearchSubject"):
		return patientNeighborTraversal(spec, "patient_research_subject_set"), true
	case traversalKey("Patient", "subject_Patient", "Specimen"):
		return patientNeighborTraversal(spec, "patient_specimen_set"), true
	case traversalKey("Patient", "subject_Patient", "MedicationAdministration"):
		return patientNeighborTraversal(spec, "patient_medication_administration_set"), true
	case traversalKey("Patient", "subject_Patient", "Observation"):
		return patientNeighborTraversal(spec, "patient_subject_observation_set"), true
	case traversalKey("Patient", "subject_Patient", "ImagingStudy"):
		return patientNeighborTraversal(spec, "patient_imaging_study_set"), true
	case traversalKey("Patient", "subject_Patient", "DocumentReference"):
		return plannerTraversal{
			Schema:                     spec,
			Role:                       traversalRolePatientDocumentReference,
			SetName:                    "patient_document_reference_set",
			SharedRootNeighborEligible: true,
		}, true
	case traversalKey("Patient", "focus_Patient", "Observation"):
		return patientDirectTraversal(spec, "patient_focus_observation_set"), true
	case traversalKey("Patient", "member_entity_Patient", "Group"):
		return patientDirectTraversal(spec, "patient_group_set"), true
	case traversalKey("Specimen", "member_entity_Specimen", "Group"):
		return plannerTraversal{
			Schema:  spec,
			Role:    traversalRoleSpecimenGroup,
			SetName: "specimen_group_set",
		}, true
	case traversalKey("Specimen", "subject_Specimen", "DocumentReference"):
		return plannerTraversal{
			Schema:  spec,
			Role:    traversalRoleSpecimenDocumentReference,
			SetName: "specimen_document_reference_set",
		}, true
	case traversalKey("Group", "subject_Group", "DocumentReference"):
		return plannerTraversal{
			Schema:  spec,
			Role:    traversalRoleGroupDocumentReference,
			SetName: "group_document_reference_set",
		}, true
	default:
		return plannerTraversal{}, false
	}
}

func patientNeighborTraversal(spec fhirschema.TraversalSpec, setName string) plannerTraversal {
	return plannerTraversal{
		Schema:                     spec,
		Role:                       traversalRolePatientNeighborChild,
		SetName:                    setName,
		SharedRootNeighborEligible: true,
	}
}

func patientDirectTraversal(spec fhirschema.TraversalSpec, setName string) plannerTraversal {
	return plannerTraversal{
		Schema:  spec,
		Role:    traversalRolePatientDirectChild,
		SetName: setName,
	}
}

func traversalKey(fromType, edgeLabel, toType string) string {
	return fromType + "|" + edgeLabel + "|" + toType
}
