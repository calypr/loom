package server

import (
	"context"
	"encoding/json"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"strings"
	"testing"
)

func TestImportedRelationshipAddMatchRemoveRoundTripPreservesPin(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	resolver := importedRecipeSources{registry: registry, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, []capability.Edge{{ID: "subject", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject_Patient", StorageDirection: "OUTBOUND"}}, []capability.Candidate{{ID: "patient-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "required_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}, nil)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "test"), map[string][]recipe.StageColumn{"Specimen": {{ID: "specimen-id", Name: "id", Label: "ID", Type: "string"}}})
	if err != nil {
		t.Fatal(err)
	}
	original := workspace.Documents[0]
	before, _ := json.Marshal(original)
	added, err := resolver.EditRelationship(ctx, "project-a", authorized, original, authoringv2.ImportedRelationshipChange{Action: "ADD", ParentOccurrenceID: authoringv2.RootOccurrenceID, EdgeID: "subject", MatchMode: authoringv2.RouteMatchOptional})
	if err != nil {
		t.Fatal(err)
	}
	if *added.ImportedRecipe != *original.ImportedRecipe || len(added.Route.Children) != 1 || added.Rows.Kind != authoringv2.RowDefinitionRecords {
		t.Fatal("add changed pin or row identity")
	}
	relation := added.ImportedSource.Relationships.Additions[0]
	if !strings.HasPrefix(relation.OccurrenceID, "project_") || relation.Fields[0].ValueMode != "ALL" {
		t.Fatal("addition missing stable identity or explicit collection policy")
	}
	workspace.Documents[0] = added
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	required, err := resolver.EditRelationship(ctx, "project-a", authorized, reloaded.Documents[0], authoringv2.ImportedRelationshipChange{Action: "MATCH", OccurrenceID: relation.OccurrenceID, MatchMode: authoringv2.RouteMatchRequired})
	if err != nil {
		t.Fatal(err)
	}
	if required.Route.Children[0].OccurrenceID != relation.OccurrenceID || required.Route.Children[0].MatchMode != authoringv2.RouteMatchRequired {
		t.Fatal("match edit retargeted occurrence")
	}
	effective, err := recipeimport.ApplySourcePlan(output, required.ImportedSource)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Traversals) != 1 || effective.Traversals[0].MatchMode != recipe.MatchRequired || effective.Traversals[0].Fields[0].ValueMode != recipe.ValueModeAll {
		t.Fatalf("native recipe lost requested policy: %#v", effective.Traversals)
	}
	removed, err := resolver.EditRelationship(ctx, "project-a", authorized, required, authoringv2.ImportedRelationshipChange{Action: "REMOVE", OccurrenceID: relation.OccurrenceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Route.Children) != 0 || *removed.ImportedRecipe != *original.ImportedRecipe {
		t.Fatal("removal lost pin or retained traversal")
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("relationship edit mutated original document")
	}
	if actual, _ := registry.entry.Bundle.Digest(); actual != digest {
		t.Fatal("shared immutable recipe changed")
	}
	for _, change := range []authoringv2.ImportedRelationshipChange{{Action: "ADD", ParentOccurrenceID: "missing", EdgeID: "subject", MatchMode: authoringv2.RouteMatchOptional}, {Action: "ADD", ParentOccurrenceID: authoringv2.RootOccurrenceID, EdgeID: "unauthorized", MatchMode: authoringv2.RouteMatchOptional}, {Action: "MATCH", OccurrenceID: authoringv2.RootOccurrenceID, MatchMode: authoringv2.RouteMatchRequired}} {
		if _, err := resolver.EditRelationship(ctx, "project-a", authorized, original, change); err == nil {
			t.Fatalf("accepted unauthorized edit: %#v", change)
		}
	}
}
