package catalog

import (
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type RelationshipEvidenceKey struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	StorageFromType   string
	Label             string
	StorageToType     string
	BuilderFromType   string
	BuilderToType     string
	BuilderDirection  string
}

func RelationshipEvidenceKeyFor(observation RelationshipObservation) RelationshipEvidenceKey {
	return RelationshipEvidenceKey{
		Project:           observation.Project,
		DatasetGeneration: observation.DatasetGeneration,
		AuthResourcePath:  observation.AuthResourcePath,
		StorageFromType:   observation.StorageFromType,
		Label:             observation.Label,
		StorageToType:     observation.StorageToType,
		BuilderFromType:   observation.BuilderFromType,
		BuilderToType:     observation.BuilderToType,
		BuilderDirection:  observation.BuilderDirection,
	}
}

func RelationshipObservationFromKey(key RelationshipEvidenceKey, count int64) RelationshipObservation {
	return RelationshipObservation{
		Project:           key.Project,
		DatasetGeneration: key.DatasetGeneration,
		AuthResourcePath:  key.AuthResourcePath,
		FromType:          key.BuilderFromType,
		Label:             key.Label,
		ToType:            key.BuilderToType,
		EdgeCount:         count,
		StorageFromType:   key.StorageFromType,
		StorageToType:     key.StorageToType,
		StorageDirection:  "OUTBOUND",
		BuilderFromType:   key.BuilderFromType,
		BuilderToType:     key.BuilderToType,
		BuilderDirection:  key.BuilderDirection,
	}
}

func ProjectRelationshipObservation(project, datasetGeneration, auth, storageFrom, label, storageTo string, count int64) (RelationshipObservation, bool, EvidenceDiagnostic) {
	return projectRelationshipObservation(fhirschema.ResolveCompilerTraversal, project, datasetGeneration, auth, storageFrom, label, storageTo, count)
}

func projectRelationshipObservation(resolve func(fromType, edgeLabel, toType string) (fhirschema.CompilerTraversal, bool, error), project, datasetGeneration, auth, storageFrom, label, storageTo string, count int64) (RelationshipObservation, bool, EvidenceDiagnostic) {
	diagnostic := EvidenceDiagnostic{StorageFromType: storageFrom, Label: label, StorageToType: storageTo, Count: count}
	if count <= 0 {
		diagnostic.Code = "INVALID_RELATIONSHIP_COUNT"
		diagnostic.Message = "relationship evidence count must be positive"
		return RelationshipObservation{}, false, diagnostic
	}
	if _, found, err := resolve(storageTo, label, storageFrom); found && err == nil {
		return RelationshipObservation{Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: auth, FromType: storageTo, Label: label, ToType: storageFrom, EdgeCount: count, StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND", BuilderFromType: storageTo, BuilderToType: storageFrom, BuilderDirection: "INBOUND"}, true, diagnostic
	}
	if _, found, err := resolve(storageFrom, label, storageTo); found && err == nil {
		return RelationshipObservation{Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: auth, FromType: storageFrom, Label: label, ToType: storageTo, EdgeCount: count, StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND", BuilderFromType: storageFrom, BuilderToType: storageTo, BuilderDirection: "OUTBOUND"}, true, diagnostic
	}
	diagnostic.Code = "UNSUPPORTED_GENERATED_TRAVERSAL"
	diagnostic.Message = "relationship endpoints and label are not a supported generated traversal"
	return RelationshipObservation{}, false, diagnostic
}

func RelationshipEvidenceLess(a, b RelationshipObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.FromType, b.FromType}, {a.Label, b.Label}, {a.ToType, b.ToType}, {a.StorageFromType, b.StorageFromType}, {a.StorageToType, b.StorageToType}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.EdgeCount < b.EdgeCount
}

func EvidenceStatusFor(available, complete, truncated bool, count int) EvidenceStatus {
	if !available {
		return EvidenceUnavailable
	}
	if truncated {
		return EvidenceTruncated
	}
	if !complete {
		return EvidenceIncomplete
	}
	if count == 0 {
		return EvidenceEmpty
	}
	return EvidenceAvailable
}
