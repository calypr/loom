package ingest

import (
	"sort"

	generated "github.com/calypr/loom/fhir/schema"
	publication "github.com/calypr/loom/internal/dataset"
)

type runtimeFHIRSchema struct {
	classes map[string]struct{}
}

func (s *runtimeFHIRSchema) HasClass(name string) bool {
	_, ok := s.classes[name]
	return ok
}

func loadRuntimeFHIRSchema() *runtimeFHIRSchema {
	resourceTypes := generated.ResourceTypes
	classes := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		classes[resourceType] = struct{}{}
	}
	return &runtimeFHIRSchema{classes: classes}
}

func loadSchemaSnapshot() (publication.SchemaSnapshot, error) {
	resourceTypes := append([]string(nil), generated.ResourceTypes...)
	sort.Strings(resourceTypes)
	return publication.NewSchemaSnapshot(
		generated.SchemaID,
		generated.FHIRVersion,
		generated.SourceSHA256,
		resourceTypes,
	)
}

func loadSchema() (publication.SchemaSnapshot, *runtimeFHIRSchema, error) {
	snapshot, err := loadSchemaSnapshot()
	if err != nil {
		return publication.SchemaSnapshot{}, nil, err
	}
	return snapshot, loadRuntimeFHIRSchema(), nil
}
