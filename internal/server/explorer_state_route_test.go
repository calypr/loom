package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestExplorerStateRouteBuildsDefaultProjectionFromRecipe(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "patients", "Patients", "", "test"); err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		ID:               "revision-a",
		Project:          "project-a",
		ExplorerID:       "patients",
		Recipe:           recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}}}}},
		SourceGeneration: "generation-a",
		Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns:       []explorer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"}},
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":false,"chartable":false}]}]}`),
		Publication:          explorer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a"},
		Status:               explorer.RevisionActive,
	}
	store.mu.Lock()
	store.revisions[revision.ID] = revision
	owner := store.explorers[testExplorerKey("project-a", "patients")]
	owner.ActiveRevisionID = revision.ID
	store.explorers[testExplorerKey("project-a", "patients")] = owner
	store.mu.Unlock()

	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.APIVersion != explorer.ExplorerStateV1APIVersion || state.Kind != explorer.ExplorerStateV1Kind {
		t.Fatalf("state identity = %q/%q", state.APIVersion, state.Kind)
	}
	if state.Runtime == nil {
		t.Fatalf("runtime projection is nil: %s", response.Body)
	}
	if state.Runtime.Status != "ACTIVE" || len(state.Runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", state.Runtime)
	}
	output := state.Runtime.Outputs[0]
	if output.OutputID != "patients" || output.Selector != selector || len(output.Columns) != 1 || output.Columns[0].Column != "patient_id" {
		t.Fatalf("default output = %#v", output)
	}
	if !output.Columns[0].Visible || len(output.Table.Columns) != 1 || len(output.Filters) != 0 || len(output.Charts) != 0 {
		t.Fatalf("default presentation = %#v", output)
	}
}

func TestExplorerStateRoutePreservesCompilerResultUnits(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	weightUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}
	bmiUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg/m2"}
	revision := explorer.Revision{
		ID: "revision-result-units", Project: "project-a", ExplorerID: "patients",
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion,
			Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
		},
		SourceGeneration: "generation-a",
		Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{
				{Name: "mean_weight", LogicalType: "decimal", ClickHouse: "Decimal"},
				{Name: "bmi", LogicalType: "decimal", ClickHouse: "Decimal"},
				{Name: "patient_id", LogicalType: "string", ClickHouse: "String"},
			},
		}}},
		EmittedColumns: []explorer.EmittedColumn{
			{OutputID: "patients", EmissionID: "em_mean_weight", PublicColumn: "mean_weight", Label: "Mean weight", LogicalType: "decimal", ResultUnit: weightUnit},
			{OutputID: "patients", EmissionID: "em_bmi", PublicColumn: "bmi", Label: "BMI", LogicalType: "decimal", ResultUnit: bmiUnit},
			{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"},
		},
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"mean_weight","label":"Mean weight","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg"}},{"column":"bmi","label":"BMI","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg/m2"}},{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":false,"chartable":false}]}]}`),
		Publication:          explorer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a"},
		Status:               explorer.RevisionActive,
	}
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "patients", "Patients", "", "test"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.revisions[revision.ID] = revision
	owner := store.explorers[testExplorerKey("project-a", "patients")]
	owner.ActiveRevisionID = revision.ID
	store.explorers[testExplorerKey("project-a", "patients")] = owner
	store.mu.Unlock()

	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Runtime == nil || len(state.Runtime.Outputs) != 1 || len(state.Runtime.Outputs[0].Columns) != 3 {
		t.Fatalf("runtime = %#v", state.Runtime)
	}
	columns := make(map[string]explorer.ExplorerRuntimeColumnV1, len(state.Runtime.Outputs[0].Columns))
	for _, column := range state.Runtime.Outputs[0].Columns {
		columns[column.Column] = column
	}
	for name, want := range map[string]*unit.UnitIdentity{"mean_weight": weightUnit, "bmi": bmiUnit} {
		got := columns[name].ResultUnit
		if got == nil || got.System != want.System || got.Code != want.Code {
			t.Fatalf("runtime %s result unit = %#v, want %#v", name, got, want)
		}
	}
	if columns["patient_id"].ResultUnit != nil {
		t.Fatalf("unitless runtime column carries result unit: %#v", columns["patient_id"].ResultUnit)
	}
	var wire struct {
		Runtime struct {
			Outputs []struct {
				Columns []map[string]json.RawMessage `json:"columns"`
			} `json:"outputs"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal([]byte(response.Body), &wire); err != nil {
		t.Fatalf("decode Viewer state JSON: %v", err)
	}
	wireColumns := make(map[string]map[string]json.RawMessage, len(wire.Runtime.Outputs[0].Columns))
	for _, column := range wire.Runtime.Outputs[0].Columns {
		var name string
		if err := json.Unmarshal(column["column"], &name); err != nil {
			t.Fatalf("decode Viewer column name: %v", err)
		}
		wireColumns[name] = column
	}
	for name, code := range map[string]string{"mean_weight": "kg", "bmi": "kg/m2"} {
		var got map[string]string
		if err := json.Unmarshal(wireColumns[name]["resultUnit"], &got); err != nil {
			t.Fatalf("decode Viewer %s resultUnit: %v", name, err)
		}
		if len(got) != 2 || got["system"] != "http://unitsofmeasure.org" || got["code"] != code {
			t.Fatalf("Viewer %s resultUnit JSON = %#v", name, got)
		}
	}
	if _, exists := wireColumns["patient_id"]["resultUnit"]; exists {
		t.Fatalf("unitless Viewer column includes resultUnit: %s", wireColumns["patient_id"]["resultUnit"])
	}
}

func TestViewerProjectionDoesNotInventColumnsWithoutPublishedSchema(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                selector.Recipe,
			TranslationVersion:  selector.TranslationVersion,
			Outputs:             []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
		},
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{Name: "patients", Selector: &selector}}},
		EmittedColumns: []explorer.EmittedColumn{{
			OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string",
		}},
	}

	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Columns) != 0 {
		t.Fatalf("invented columns = %#v", output.Columns)
	}
}

func TestViewerProjectionRejectsAliasedPhysicalColumns(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                selector.Recipe,
			TranslationVersion:  selector.TranslationVersion,
			Outputs: []recipe.Output{{
				Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
				Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}},
			}},
		},
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "patients", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "route_0__patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{{
			OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string",
		}},
	}

	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
	if len(runtime.Outputs[0].Columns) != 0 {
		t.Fatalf("aliased columns leaked = %#v", runtime.Outputs[0].Columns)
	}
}

func TestExplorerStateRouteReportsMalformedCurrentProjection(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	baseRevision := explorer.Revision{
		ID: "revision-integrity", Project: "project-a", ExplorerID: "patients",
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion,
			Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}}}},
		},
		Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"}},
		Status:         explorer.RevisionActive,
	}
	for _, test := range []struct {
		name     string
		config   json.RawMessage
		contract json.RawMessage
	}{
		{name: "malformed current config", config: json.RawMessage(`{"apiVersion":"` + explorer.ConfigV2APIVersion + `","kind":"ExplorerConfig","unknown":true}`)},
		{name: "malformed current public contract", contract: json.RawMessage(`{"outputs":`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestExplorerStore()
			service, err := explorer.NewService(store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "patients", "Patients", "", "test"); err != nil {
				t.Fatal(err)
			}
			revision := baseRevision
			revision.Config = test.config
			revision.PublicOutputContract = test.contract
			store.mu.Lock()
			store.revisions[revision.ID] = revision
			owner := store.explorers[testExplorerKey("project-a", "patients")]
			owner.ActiveRevisionID = revision.ID
			store.explorers[testExplorerKey("project-a", "patients")] = owner
			store.mu.Unlock()

			app := fiber.New()
			registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
			response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
			if response.StatusCode != http.StatusInternalServerError || !strings.Contains(response.Body, `"code":"EXPLORER_INTEGRITY_FAILURE"`) {
				t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
			}
		})
	}
}

func TestViewerProjectionKeepsRecipeOnlyLegacyFallback(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "legacy-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		Recipe:  recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}},
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{Name: "patients", Selector: &selector}}}, Status: explorer.RevisionActive,
	}
	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil || runtime == nil || len(runtime.Outputs) != 1 || runtime.Outputs[0].OutputID != "patients" {
		t.Fatalf("legacy runtime=%#v, err=%v", runtime, err)
	}
}

func TestExplorerStateRouteReturnsExplicitUnpublishedProjection(t *testing.T) {
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "empty", "Empty", "", "test"); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/empty", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(response.Body), &wire); err != nil {
		t.Fatal(err)
	}
	if raw, ok := wire["runtime"]; !ok || string(raw) != "null" {
		t.Fatalf("runtime = %s, want explicit null", raw)
	}
	var state explorer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Runtime != nil || state.Generated.Publication.State != explorer.ExplorerRuntimeV1NotPublished {
		t.Fatalf("unpublished state = %#v", state)
	}
}
