package server

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRecipeOutputLogicalColumnsUsesSortableGroupStorageKey(t *testing.T) {
	for _, test := range []struct {
		name     string
		grain    spec.RowGrain
		kind     string
		wantKind string
	}{
		{name: "resource row identity", grain: spec.RowGrainPatient, kind: "string", wantKind: "string"},
		{name: "group row identity", grain: spec.RowGrainGroups, kind: "object", wantKind: "string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved := dataframeexecution.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{
				Name: "output", RootResourceType: "Patient", RowGrain: test.grain,
				OutputSchema: []lower.CompiledOutputColumn{{
					Name: "__loom_row_id", Kind: test.kind, Cardinality: "one", Internal: true, Identity: true,
				}},
			}}}}

			columns := recipeOutputLogicalColumns(resolved, "output")
			if len(columns) != 1 || columns[0].Name != "__loom_row_id" || columns[0].Kind != test.wantKind || !columns[0].IsIdentity || !columns[0].LoomOwned {
				t.Fatalf("publication identity column = %#v, want storage kind %q and Loom-owned identity", columns, test.wantKind)
			}
			if resolved.Compiled.Outputs[0].OutputSchema[0].Kind != test.kind {
				t.Fatalf("publication conversion mutated compiler identity kind to %q", resolved.Compiled.Outputs[0].OutputSchema[0].Kind)
			}
		})
	}
}

func TestCanonicalizeGroupPublicationIdentityRoundTripsNamedTuple(t *testing.T) {
	output := lower.CompiledRecipeOutput{
		Name: "groups", RowGrain: spec.RowGrainGroups,
		RowIdentity: &spec.RowIdentity{Grain: spec.RowGrainGroups, Fields: []string{"group_revision_id", "group_id"}},
	}
	row := map[string]any{"__loom_row_id": map[string]any{"group_id": "group-1", "group_revision_id": "grouprev-1"}}
	if err := canonicalizeGroupPublicationIdentity(output, row); err != nil {
		t.Fatal(err)
	}
	encoded, ok := row["__loom_row_id"].(string)
	if !ok {
		t.Fatalf("publication row identity = %#v, want canonical JSON text", row["__loom_row_id"])
	}
	var decoded struct {
		GroupRevisionID string `json:"group_revision_id"`
		GroupID         string `json:"group_id"`
	}
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode publication row identity %q: %v", encoded, err)
	}
	if decoded.GroupRevisionID != "grouprev-1" || decoded.GroupID != "group-1" {
		t.Fatalf("decoded tuple = %#v", decoded)
	}
	other := map[string]any{"__loom_row_id": map[string]any{"group_revision_id": "grouprev-1", "group_id": "group-1"}}
	if err := canonicalizeGroupPublicationIdentity(output, other); err != nil {
		t.Fatal(err)
	}
	if other["__loom_row_id"] != encoded {
		t.Fatalf("canonical identity changed with input map key order: %q != %q", other["__loom_row_id"], encoded)
	}
}

func TestCanonicalizeGroupPublicationIdentityRejectsUnknownShapes(t *testing.T) {
	output := lower.CompiledRecipeOutput{
		Name: "groups", RowGrain: spec.RowGrainGroups,
		RowIdentity: &spec.RowIdentity{Grain: spec.RowGrainGroups, Fields: []string{"group_revision_id", "group_id"}},
	}
	for _, row := range []map[string]any{
		{"__loom_row_id": "arbitrary-string"},
		{"__loom_row_id": map[string]any{"group_revision_id": "grouprev-1", "group_id": "group-1", "extra": "not-allowed"}},
		{"__loom_row_id": map[string]any{"group_revision_id": "", "group_id": "group-1"}},
	} {
		if err := canonicalizeGroupPublicationIdentity(output, row); err == nil {
			t.Fatalf("accepted malformed identity %#v", row)
		}
	}
	output.RowIdentity.Fields = []string{"group_id", "group_revision_id"}
	row := map[string]any{"__loom_row_id": map[string]any{"group_revision_id": "grouprev-1", "group_id": "group-1"}}
	if err := canonicalizeGroupPublicationIdentity(output, row); err == nil {
		t.Fatal("accepted an unknown compiled identity field order")
	}
}
