package ingest

import (
	"sort"

	generated "github.com/calypr/loom/fhir/schema"
	publication "github.com/calypr/loom/internal/dataset"
)

type RuntimeFHIRSchema struct {
	classes map[string]struct{}
}

func (s *RuntimeFHIRSchema) HasClass(name string) bool {
	_, ok := s.classes[name]
	return ok
}

func LoadRuntimeFHIRSchema() *RuntimeFHIRSchema {
	resourceTypes := generated.ResourceTypes
	classes := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		classes[resourceType] = struct{}{}
	}
	return &RuntimeFHIRSchema{classes: classes}
}

func LoadSchemaSnapshot() (publication.SchemaSnapshot, error) {
	resourceTypes := append([]string(nil), generated.ResourceTypes...)
	sort.Strings(resourceTypes)
	return publication.NewSchemaSnapshot(
		generated.SchemaID,
		generated.FHIRVersion,
		generated.SourceSHA256,
		resourceTypes,
	)
}

func LoadSchema() (publication.SchemaSnapshot, *RuntimeFHIRSchema, error) {
	snapshot, err := LoadSchemaSnapshot()
	if err != nil {
		return publication.SchemaSnapshot{}, nil, err
	}
	return snapshot, LoadRuntimeFHIRSchema(), nil
}
