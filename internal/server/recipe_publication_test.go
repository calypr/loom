package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
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

func TestRecipeOutputLogicalColumnsPreservesAuthoredIDsAcrossOutputOwners(t *testing.T) {
	for _, test := range []struct {
		name   string
		grain  spec.RowGrain
		fields []lower.CompiledOutputColumn
	}{
		{
			name:  "direct source column",
			grain: spec.RowGrainPatient,
			fields: []lower.CompiledOutputColumn{{
				ID: "authored-source-id", Name: "patient_id", Kind: "string", Cardinality: "one",
			}},
		},
		{
			name:  "group and pivot outputs",
			grain: spec.RowGrainGroups,
			fields: []lower.CompiledOutputColumn{
				{ID: "authored-group-key-id", Name: "group_label", Kind: "string", Cardinality: "one"},
				{ID: "authored-pivot-output-id", Name: "active_total", Kind: "integer", Cardinality: "optional_one"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved := dataframeexecution.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{
				Name: "output", RootResourceType: "Patient", RowGrain: test.grain,
				OutputSchema: append([]lower.CompiledOutputColumn{{
					ID: "__loom_row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "one", Internal: true, Identity: true,
				}}, test.fields...),
			}}}}

			logical := recipeOutputLogicalColumns(resolved, "output")
			if len(logical) != len(test.fields)+1 {
				t.Fatalf("publication columns = %#v, want row identity plus %d authored columns", logical, len(test.fields))
			}
			for index, field := range test.fields {
				if got := logical[index+1].ID; got != field.ID {
					t.Errorf("publication column %q ID = %q, want authored ID %q", logical[index+1].Name, got, field.ID)
				}
			}
		})
	}
}

func TestPlainTableAuthoredColumnIDReachesCompiledAndPublishedSchema(t *testing.T) {
	capabilityCandidate := capability.Candidate{
		ID: "patient-id", NodeID: "patient-node", ResourceType: "Patient", FieldPath: "id", Label: "Patient.id", LogicalType: "string",
		Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect},
	}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-a", SchemaDigest: "schema-a", CompilerVersion: "compiler-a"},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: "route-a", AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: "projection-a", Modes: []capability.ProjectionMode{capability.ProjectionScalar}},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient-node", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}},
		nil,
		[]capability.Candidate{capabilityCandidate},
		nil,
	)
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, capabilityCandidate)
	if err != nil {
		t.Fatalf("build signed field choice for catalog: %v", err)
	}

	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder"}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{},
	}
	authoringCatalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: "project-a", ExplorerID: "explorer-a", SourceGeneration: "generation-a", AuthorizationScopeDigest: "scope-a",
		ResolvedSchemaDigest: "schema-a", SnapshotToken: snapshot.Token, Complete: true,
		RoutePolicy: authoringv2.RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
		Nodes:       []authoringv2.CatalogNode{{ID: "patient-node", ResourceType: "Patient", RowRootEligible: true}},
		Candidates: []authoringv2.CatalogCandidate{{
			ID: "patient-id", NodeID: "patient-node", FieldPath: "id", Label: "Patient ID", LogicalType: "string",
			Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
		}},
	}
	workspace, created, err := authoringv2.ApplyCommands(workspace, authoringCatalog, "create-patients", []authoringv2.Command{{
		Type: authoringv2.CommandCreateTable, Title: "Patients", RootNodeID: "patient-node",
	}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err = authoringv2.ApplyCommands(workspace, authoringCatalog, "add-patient-id", []authoringv2.Command{{
		Type: authoringv2.CommandAddColumn, OutputID: created[0].OutputID, OccurrenceID: authoringv2.RootOccurrenceID, CandidateID: "patient-id",
	}})
	if err != nil {
		t.Fatal(err)
	}
	authored := workspace.Documents[0].Columns[0]
	if authored.ColumnID == "" {
		t.Fatal("plain-table ADD_COLUMN did not persist an authored column identity")
	}

	translated, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatalf("compile authored workspace: %v", err)
	}
	semanticPlan, err := semantic.BuildRecipePlan(translated.Bundle, recipe.RuntimeBindings{Project: "project-a"})
	if err != nil {
		t.Fatalf("build semantic plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("resolve semantic plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower resolved plan: %v", err)
	}
	var compiledID string
	for _, output := range compiled.Outputs {
		if output.Name != created[0].OutputID {
			continue
		}
		for _, column := range output.OutputSchema {
			if column.Name == authored.Column {
				compiledID = column.ID
				break
			}
		}
	}
	if compiledID != authored.ColumnID {
		t.Fatalf("compiler output schema ID = %q, want exact authored ID %q", compiledID, authored.ColumnID)
	}

	logical := recipeOutputLogicalColumns(dataframeexecution.Resolved{Compiled: compiled}, created[0].OutputID)
	for _, column := range logical {
		if column.Name == authored.Column {
			if column.ID != authored.ColumnID {
				t.Fatalf("published logical column ID = %q, want exact authored ID %q", column.ID, authored.ColumnID)
			}
			return
		}
	}
	t.Fatalf("published logical schema omitted authored column %q: %#v", authored.Column, logical)
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
