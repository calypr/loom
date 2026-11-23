package lifecycle

import (
	"context"
	"errors"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
)

type importedSourceChoicePlanner struct{ calls int }

func (planner *importedSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error) {
	planner.calls++
	return nil, errors.New("generic FHIR route planner must not run for imported source choices")
}

func TestImportedSourceCapabilityHelpersSkipGenericRowChoicePlanner(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service, constructionBase) (bool, string, string, int, error)
	}{

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
			base := constructionBase{Document: ws.Document{ImportedRecipe: &ws.ImportedRecipeRef{
				Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "DocumentReference",
			}, Rows: ws.RecordsRowDefinition(),
			}}
			supported, code, reason, choices, err := test.call(service, base)
			if err != nil {
				t.Fatalf("capability helper: %v", err)
			}
			if !supported || code != "" || reason != "" || choices != 0 {
				t.Fatalf("imported capability = supported %t code %q reason %q choices %d, want supported with no live-catalog choices", supported, code, reason, choices)
			}
			if planner.calls != 0 {
				t.Fatalf("generic row-choice planner calls = %d, want 0", planner.calls)
			}
		})
	}
}
