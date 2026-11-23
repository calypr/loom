package construction

import (
	"context"
	"errors"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestImportedEmptyRootSkipsSourceChoicesButKeepsStageOperations(t *testing.T) {
	document := ws.Document{
		RootResourceType: "MedicationAdministration",
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "MedicationAdministration"},
		ImportedRecipe:   &ws.ImportedRecipeRef{Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "MedicationAdministration"}, Rows: ws.RecordsRowDefinition(),
	}
	base := constructionBase{Document: document, Snapshot: capability.Snapshot{Nodes: []capability.Node{}}}
	stage := receiptmodel.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{
			{Kind: "GROUP", Supported: true}, {Kind: "PIVOT", Supported: true},
			{Kind: "DERIVE", Supported: true}, {Kind: "FILTER", Supported: true},
			{Kind: "UNPIVOT", Supported: true}, {Kind: "EXPAND", Supported: true},
			{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true},
		},
		GroupFieldSourceContext: &receiptmodel.ReceiptConstructionGroupFieldSourceContext{
			SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "imported-source-shape",
			SourceIdentityColumn: "_key", FieldOwners: []receiptmodel.ReceiptConstructionGroupFieldOwner{{
				Role: "ROOT", OccurrenceID: ws.RootOccurrenceID,
				OwnerResourceType: "MedicationAdministration", ResourceType: "MedicationAdministration",
			}},
		},
	}
	ordinaryOperations := append([]receiptmodel.ReceiptConstructionOperationChoice(nil), stage.Capabilities...)
	service := &Service{}
	coded, err := constructionCodedGroupChoices(base, "MedicationAdministration", stage)
	if err != nil {
		t.Fatalf("coded-group choices on empty imported root: %v", err)
	}
	semantic, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "MedicationAdministration", stage)
	if err != nil {
		t.Fatalf("semantic group choices on empty imported root: %v", err)
	}
	group, groupIncomplete, err := service.constructionGroupFieldChoices(context.Background(), base, "MedicationAdministration", stage, semantic, incomplete, "", "")
	if err != nil {
		t.Fatalf("Group field choices on empty imported root: %v", err)
	}
	if len(coded) != 0 || len(semantic) != 0 || len(group) != 0 || groupIncomplete || groupIncomplete != incomplete {
		t.Fatalf("empty imported root returned invented source choices: coded=%#v semantic=%#v group=%#v incomplete=(%t,%t)", coded, semantic, group, incomplete, groupIncomplete)
	}
	if len(stage.Capabilities) != len(ordinaryOperations) {
		t.Fatalf("source-choice fallback changed the stage operation count: got %#v, want %#v", stage.Capabilities, ordinaryOperations)
	}
	for _, want := range ordinaryOperations {
		found := false
		for _, got := range stage.Capabilities {
			if got.Kind == want.Kind && got.Supported == want.Supported {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ordinary stage operation %q was removed from %#v", want.Kind, stage.Capabilities)
		}
	}
}

func TestGenericEmptyRootStillFailsClosedForCodedGroupChoices(t *testing.T) {
	base := constructionBase{
		Document: ws.Document{RootResourceType: "MedicationAdministration", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "MedicationAdministration"}, Rows: ws.RecordsRowDefinition()},
		Snapshot: capability.Snapshot{Nodes: []capability.Node{}},
	}
	stage := receiptmodel.ReceiptConstructionStage{ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key", Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true}}}
	if _, err := constructionCodedGroupChoices(base, "MedicationAdministration", stage); err == nil {
		t.Fatal("generic missing root should remain a hard coded-group route failure")
	}
}

type importedSourceChoicePlanner struct{ calls int }

func (planner *importedSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error) {
	planner.calls++
	return nil, errors.New("generic FHIR route planner must not run for imported source choices")
}

func TestImportedSourceGroupAndPivotCapabilitiesSkipGenericRowChoicePlanner(t *testing.T) {
	planner := &importedSourceChoicePlanner{}
	service := &Service{config: Config{RowChoicePlanner: planner}}
	base := constructionBase{Document: ws.Document{ImportedRecipe: &ws.ImportedRecipeRef{
		Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "DocumentReference",
	}, Rows: ws.RecordsRowDefinition(),
	}}
	group, err := service.constructionSources().constructionGroupSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
	if err != nil || !group.Supported || group.ReasonCode != "" || len(group.Choices) != 0 {
		t.Fatalf("imported group capability = %#v, %v", group, err)
	}
	pivot, err := service.constructionSources().constructionPivotSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
	if err != nil || !pivot.Supported || pivot.ReasonCode != "" || len(pivot.Choices) != 0 {
		t.Fatalf("imported pivot capability = %#v, %v", pivot, err)
	}
	if planner.calls != 0 {
		t.Fatalf("generic row-choice planner calls = %d, want 0", planner.calls)
	}
}
