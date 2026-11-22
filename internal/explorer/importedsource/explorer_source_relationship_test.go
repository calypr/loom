package importedsource

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	"github.com/calypr/loom/internal/dataframe/fielddiscovery"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

func TestImportedRelationshipAddMatchRemoveRoundTripPreservesPin(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: registry, ScopeDigest: reciperuntime.ScopeDigest,
		ResolveBundle: fielddiscovery.RecipeSchemaResolver(func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			if options.ResourceType != "Patient" {
				return nil, nil
			}
			return []fieldcatalog.PopulatedField{{ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"fixture-system"}}}, nil
		}, nil),
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return errors.New("unexpected query during source editing")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, []capability.Edge{{ID: "subject", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject_Patient", StorageDirection: "OUTBOUND"}}, []capability.Candidate{{ID: "patient-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "required_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "test"), map[string][]recipe.StageColumn{"Specimen": {{ID: "specimen-id", Name: "id", Label: "ID", Type: "string"}}})
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
	t.Run("identifier rule on Graph Connect occurrence", func(t *testing.T) {
		key, value := recipe.Expression{Select: "item.system"}, recipe.Expression{Select: "item.value"}
		params := &authoringv2.ImportedSourceParameters{Dynamic: &authoringv2.ImportedDynamicParameters{
			Name: "patient_identifiers", Source: recipe.Expression{Select: "identifier[]"}, Key: &key, Value: &value,
		}}
		created, err := resolver.CreateSourceRule(ctx, "project-a", authorized, added, authoringv2.SourceRuleCreate{
			Kind: "DYNAMIC_COLUMN", OccurrenceID: relation.OccurrenceID, Parameters: params,
		})
		if err != nil {
			t.Fatal(err)
		}
		effective, err := recipeimport.ApplySourcePlan(output, created.ImportedSource)
		if err != nil {
			t.Fatal(err)
		}
		if len(effective.DynamicColumns) != 0 || len(effective.Traversals) != 1 || len(effective.Traversals[0].DynamicColumns) != 1 ||
			effective.Traversals[0].DynamicColumns[0].Source.Select != relation.OccurrenceID+".identifier[]" {
			t.Fatalf("rule escaped its Graph Connect traversal: %#v", effective.Traversals)
		}
		if len(created.Columns) != len(added.Columns)+1 {
			t.Fatalf("identifier rule did not generate a column: %#v", created.Columns)
		}
		hydrated, err := recipeimport.HydrateSourcePlan(output, created.ImportedSource)
		if err != nil {
			t.Fatal(err)
		}
		step := hydrated.Steps[len(hydrated.Steps)-1]
		if step.OccurrenceID != relation.OccurrenceID || step.ParentID != "relationships."+relation.OccurrenceID {
			t.Fatalf("rule step is not nested under its occurrence: %#v", step)
		}
		beforeIDs := map[string]bool{}
		for _, column := range added.Columns {
			beforeIDs[column.ColumnID] = true
		}
		var generatedID, generatedName string
		for _, column := range created.Columns {
			if !beforeIDs[column.ColumnID] {
				generatedID, generatedName = column.ColumnID, column.Column
			}
		}
		prefix := "external"
		editedParams := copySourceParametersForTest(t, created, step.ID)
		editedParams.Dynamic.ColumnPrefix = &prefix
		edited, err := resolver.Edit(ctx, "project-a", authorized, created, &authoringv2.ImportedSourceStepChange{
			StepID: step.ID, Parameters: editedParams,
		})
		if err != nil {
			t.Fatal(err)
		}
		retained := false
		for _, column := range edited.Columns {
			retained = retained || (column.ColumnID == generatedID && column.Column != generatedName)
		}
		if generatedID == "" || !retained {
			t.Fatal("renaming the generated column changed its ID")
		}
		_, err = resolver.EditRelationship(ctx, "project-a", authorized, created, authoringv2.ImportedRelationshipChange{Action: "REMOVE", OccurrenceID: relation.OccurrenceID})
		var unavailable *authoringv2.UnavailableSourceRuleOccurrence
		if !errors.As(err, &unavailable) || unavailable.OccurrenceID != relation.OccurrenceID {
			t.Fatalf("removed rule occurrence error = %v", err)
		}
	})
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
