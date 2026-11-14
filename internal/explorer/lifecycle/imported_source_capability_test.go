package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type importedSourceChoicePlanner struct{ calls int }

func (planner *importedSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
	planner.calls++
	return nil, errors.New("generic FHIR route planner must not run for imported source choices")
}

func TestImportedEmptyRootSkipsSourceChoicesButKeepsStageOperations(t *testing.T) {
	document := authoringv2.Document{
		RootResourceType: "MedicationAdministration",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "MedicationAdministration"},
		ImportedRecipe:   &authoringv2.ImportedRecipeRef{Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "MedicationAdministration"},
	}
	base := constructionBase{document: document, snapshot: capability.Snapshot{Nodes: []capability.Node{}}}
	stage := explorer.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Capabilities: []explorer.ReceiptConstructionOperationChoice{
			{Kind: "GROUP", Supported: true}, {Kind: "PIVOT", Supported: true},
			{Kind: "DERIVE", Supported: true}, {Kind: "FILTER", Supported: true},
			{Kind: "UNPIVOT", Supported: true}, {Kind: "EXPAND", Supported: true},
			{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true},
		},
		GroupFieldSourceContext: &explorer.ReceiptConstructionGroupFieldSourceContext{
			SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "imported-source-shape",
			SourceIdentityColumn: "_key", FieldOwners: []explorer.ReceiptConstructionGroupFieldOwner{{
				Role: "ROOT", OccurrenceID: authoringv2.RootOccurrenceID,
				OwnerResourceType: "MedicationAdministration", ResourceType: "MedicationAdministration",
			}},
		},
	}
	ordinaryOperations := append([]explorer.ReceiptConstructionOperationChoice(nil), stage.Capabilities...)
	service := &Service{}
	coded, err := constructionCodedGroupChoices(base, "MedicationAdministration", stage)
	if err != nil { t.Fatalf("coded-group choices on empty imported root: %v", err) }
	semantic, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "MedicationAdministration", stage)
	if err != nil { t.Fatalf("semantic group choices on empty imported root: %v", err) }
	group, groupIncomplete, err := service.constructionGroupFieldChoices(context.Background(), base, "MedicationAdministration", stage, semantic, incomplete, "", "")
	if err != nil { t.Fatalf("Group field choices on empty imported root: %v", err) }
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
		document: authoringv2.Document{RootResourceType: "MedicationAdministration", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "MedicationAdministration"}},
		snapshot: capability.Snapshot{Nodes: []capability.Node{}},
	}
	stage := explorer.ReceiptConstructionStage{ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key", Capabilities: []explorer.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true}}}
	if _, err := constructionCodedGroupChoices(base, "MedicationAdministration", stage); err == nil {
		t.Fatal("generic missing root should remain a hard coded-group route failure")
	}
}

func TestImportedSourceCapabilityHelpersSkipGenericRowChoicePlanner(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service, constructionBase) (bool, string, string, int, error)
	}{
		{name: "group", call: func(service *Service, base constructionBase) (bool, string, string, int, error) {
			got, err := service.constructionGroupSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "pivot", call: func(service *Service, base constructionBase) (bool, string, string, int, error) {
			got, err := service.constructionPivotSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "filter", call: func(service *Service, base constructionBase) (bool, string, string, int, error) {
			got, err := service.constructionFilterSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "derive", call: func(service *Service, base constructionBase) (bool, string, string, int, error) {
			got, err := service.constructionDeriveSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "unpivot", call: func(service *Service, base constructionBase) (bool, string, string, int, error) {
			got, err := service.constructionUnpivotSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
		{name: "expand", call: func(service *Service, base constructionBase) (bool, string, string, int, error) {
			got, err := service.constructionExpandSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
			return got.Supported, got.ReasonCode, got.Reason, len(got.Choices), err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planner := &importedSourceChoicePlanner{}
			service := &Service{config: Config{RowChoicePlanner: planner}}
			base := constructionBase{document: authoringv2.Document{ImportedRecipe: &authoringv2.ImportedRecipeRef{
				Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "DocumentReference",
			}}}
			supported, code, reason, choices, err := test.call(service, base)
			if err != nil {
				t.Fatalf("capability helper: %v", err)
			}
			if supported || code != "IMPORTED_SOURCE_PROJECTION_UNAVAILABLE" || reason == "" || choices != 0 {
				t.Fatalf("imported capability = supported %t code %q reason %q choices %d", supported, code, reason, choices)
			}
			if planner.calls != 0 {
				t.Fatalf("generic row-choice planner calls = %d, want 0", planner.calls)
			}
		})
	}
}
