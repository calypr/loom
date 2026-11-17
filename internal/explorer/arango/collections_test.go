package arango

import (
	"reflect"
	"testing"
)

func TestCollectionSpecsArePersistentAndIndexed(t *testing.T) {
	s := CollectionSpecs()
	if len(s) != 15 {
		t.Fatalf("specs=%#v", s)
	}
	for _, spec := range s {
		if spec.Truncate {
			t.Fatalf("%s truncates persisted Explorer state", spec.Name)
		}
		// Projections are addressed only by their source-derived primary key.
		if len(spec.Indexes) == 0 && spec.Name != workspaceProjectionCollection {
			t.Fatalf("%s has no indexes", spec.Name)
		}
	}
	indexes := make(map[string][][]string, len(s))
	for _, spec := range s {
		indexes[spec.Name] = spec.Indexes
	}
	for collection, required := range map[string][][]string{
		DraftRevisionsCollection:           {{"project", "explorerId", "draftVersion"}},
		ProjectCatalogSettingsCollection:   {{"project"}},
		ExplicitGroupRevisionsCollection:   {{"project", "id"}, {"project", "idempotencyKey"}, {"project", "generation", "resourceType", "createdAt"}, {"state", "createdAt"}},
		ExplicitGroupDefinitionsCollection: {{"revisionId", "ordinal"}, {"revisionId", "groupId"}},
		ExplicitGroupMembershipsCollection: {{"revisionId", "groupId", "id"}, {"revisionId", "project", "generation", "resourceType", "id"}},
	} {
		for _, want := range required {
			found := false
			for _, got := range indexes[collection] {
				if reflect.DeepEqual(got, want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s indexes %v do not include %v", collection, indexes[collection], want)
			}
		}
	}
}
