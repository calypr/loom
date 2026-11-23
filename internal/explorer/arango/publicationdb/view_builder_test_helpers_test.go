package publicationdb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer/viewer"

	publicationdata "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"github.com/calypr/loom/internal/projectid"
)

func projectConfigTestBundle(name, translation string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name, TranslationVersion: translation,
		Outputs: []recipe.Output{{Name: "output", RootResourceType: "Patient", RowGrain: "resource"}},
	}
}

func projectConfigTestDefinition(id, recipeName, title string) explorerconfig.ProjectConfig {
	return explorerconfig.ProjectConfig{
		ID: id, Title: title, RecipeRef: explorerconfig.ProjectConfigRecipeRef{Name: recipeName},
		Views: []explorerconfig.ConfigView{{
			ID: "view", Title: title, Output: "output",
			Table:   explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{{Column: "extension_code", Visible: true}}},
			Filters: []explorerconfig.ConfigFilter{{Column: "extension_code"}},
			Charts:  []explorerconfig.ConfigChart{{Column: "extension_code", Type: "pie", Title: "Extension codes"}},
		}},
	}
}

func projectConfigPublication(t *testing.T, project, id, recipeName, title, revisionID, generation, executionID string, bundle recipe.Bundle) explorer.ProjectConfigPublication {
	t.Helper()
	config := projectConfigTestDefinition(id, recipeName, title)
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	resolvedBundle := bundle
	resolvedBundle.Outputs = append([]recipe.Output(nil), bundle.Outputs...)
	resolvedBundle.Outputs[0].RootOccurrenceID = "resolved-recipe-snapshot"
	resolvedBundle.Outputs[0].DynamicColumns = []recipe.DynamicColumn{{
		Name: "extension_catalog", Source: recipe.Expression{Select: "root.extension[]"}, Columns: []string{"extension_code"},
		ColumnTypes: map[string]string{"extension_code": "string"}, MaxColumns: 1,
	}}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	readyAt := now
	physical := publicationdata.PhysicalColumn{Name: "extension_code", ClickHouse: "Nullable(String)", LogicalType: "string", Nullable: true}
	selector := dataset.DataframeSelector{Recipe: recipeName, TranslationVersion: bundle.TranslationVersion, Output: "output"}
	viewsJSON, err := json.Marshal(config.Views)
	if err != nil {
		t.Fatal(err)
	}
	explorerJSON, err := json.Marshal(explorerconfig.ConfigExplorer{ID: id, Title: title, Management: "repository"})
	if err != nil {
		t.Fatal(err)
	}
	configJSON := json.RawMessage(fmt.Sprintf(`{ "views": %s, "project": %q, "kind": "ExplorerConfig", "explorer": %s, "apiVersion": %q }`, viewsJSON, project, explorerJSON, explorerconfig.ConfigV2APIVersion))
	contractJSON := json.RawMessage(`{
  "outputs": [{
    "columns": [{
      "column": "extension_code",
      "label": "Extension code",
      "logicalType": "string",
      "cardinality": "optional_one",
      "nullable": true,
      "shape": "scalar",
      "lossless": true,
      "mlReady": false,
      "filterable": true,
      "chartable": true
    }],
    "outputId": "output",
    "rootResourceType": "Patient",
    "rowGrain": "resource",
    "lossless": true,
    "mlReady": false
  }]
}`)
	return explorer.ProjectConfigPublication{
		Definition: config,
		Revision: explorer.Revision{
			ID: revisionID, Project: projectid.Legacy(project), ExplorerID: id,
			Recipe: resolvedBundle, RecipeDigest: digest, ResolvedSchemaDigest: strings.Repeat("d", 64),
			Config: configJSON, PublicOutputContract: contractJSON,
			EmittedColumns: []viewer.EmittedColumn{{
				EmissionID: "dynamic:extension_code", OutputID: "output", PublicColumn: "extension_code",
				Label: "Extension code", LogicalType: "string", Cardinality: "optional_one",
				Nullable: true, Shape: "scalar", Lossless: true, Filterable: true, Chartable: true,
			}},
			SourceGeneration: generation, Dataset: viewer.DatasetMetadata{
				Generation: generation, SchemaDigest: strings.Repeat("c", 64),
				Outputs: []viewer.DatasetOutput{{Name: "output", State: "PUBLISHED", Queryable: true, Selector: &selector, Columns: []publicationdata.PhysicalColumn{physical}}},
			},
			Materializations: []viewer.Materialization{{OutputID: "output", Output: "output", MaterializationID: executionID, Selector: &selector, Columns: []publicationdata.PhysicalColumn{physical}}},
			QualityReports:   []publicationdata.QualityReport{projectConfigTestQuality(project, generation, executionID)},
			Publication:      viewer.PublicationMetadata{State: "READY", Generation: generation, ExecutionID: executionID, UpdatedAt: now},
			Status:           explorer.RevisionReady, CreatedBy: "test", CreatedAt: now, ReadyAt: &readyAt,
		},
	}
}

func projectConfigTestQuality(project, generation, executionID string) publicationdata.QualityReport {
	return publicationdata.QualityReport{
		ID: "quality-" + executionID, ReceiptID: "receipt-" + executionID,
		Project: project, DatasetGeneration: generation, ScopeDigest: "scope",
		Output: "output", PolicyVersion: publicationdata.DefaultQualityPolicyVersion,
		Completeness: publicationdata.QualityComplete, Verdict: publicationdata.QualityPassed,
		RowCount: 1, Columns: []publicationdata.ColumnQuality{{Column: "extension_code", Present: 1}},
		KeyIntegrity: publicationdata.KeyIntegrity{Distinct: 1},
	}
}
