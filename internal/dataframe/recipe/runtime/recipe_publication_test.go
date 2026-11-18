package runtime

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/spec"
)

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

func TestCanonicalizeGroupPublicationIdentityPreservesGeneratedPivotIdentity(t *testing.T) {
	const rowID = `["GROUPED_PIVOT","pivot_8c755908-4b92-459d-bea3-068924052545",["STRING","Two fixture Patients"]]`
	output := lower.CompiledRecipeOutput{
		Name: "grouped-pivot", RowGrain: spec.RowGrainGroups,
		RowIdentity: &spec.RowIdentity{Grain: spec.RowGrainGroups, Fields: []string{"__loom_row_id"}},
		OutputSchema: []lower.CompiledOutputColumn{{
			ID: "__loom_row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true,
		}},
	}
	row := map[string]any{"__loom_row_id": rowID}
	if err := canonicalizeGroupPublicationIdentity(output, row); err != nil {
		t.Fatalf("preserve compiler-generated Pivot identity: %v", err)
	}
	if got, ok := row["__loom_row_id"].(string); !ok || got != rowID {
		t.Fatalf("generated Pivot identity = %#v, want exact unchanged string %q", row["__loom_row_id"], rowID)
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
	generated := lower.CompiledRecipeOutput{
		Name: "generated", RowGrain: spec.RowGrainGroups,
		RowIdentity: &spec.RowIdentity{Grain: spec.RowGrainGroups, Fields: []string{"__loom_row_id"}},
		OutputSchema: []lower.CompiledOutputColumn{{
			ID: "__loom_row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true,
		}},
	}
	for _, malformed := range []map[string]any{
		{"__loom_row_id": "  "},
		{"__loom_row_id": map[string]any{"group_id": "group-1"}},
		{"__loom_row_id": 42},
	} {
		if err := canonicalizeGroupPublicationIdentity(generated, malformed); err == nil {
			t.Errorf("accepted malformed generated identity %#v", malformed)
		}
	}
	generated.OutputSchema[0].Kind = "object"
	if err := canonicalizeGroupPublicationIdentity(generated, map[string]any{"__loom_row_id": "generated-row"}); err == nil {
		t.Fatal("accepted a generated group row identity whose compiled schema was not a string")
	}
}
