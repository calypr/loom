package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	recipeapi "github.com/calypr/loom/internal/api/recipe"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"

	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	importedOutputID    = "MedicationAdministration"
	doseUnitColumnID    = "builder_dose_unit"
	medCategoryColumnID = "builder_medication_category"
)

type unifiedAuthoringFixture struct {
	bundle     recipe.Bundle
	digest     string
	resolver   importedRecipeSources
	authorized lifecycle.AuthorizedCapability
	workspace  authoringv2.Workspace
}

func findUnifiedRecipeOutput(bundle recipe.Bundle, name string) (recipe.Output, bool) {
	for _, output := range bundle.Outputs {
		if output.Name == name {
			return output, true
		}
	}
	return recipe.Output{}, false
}

func unifiedColumnIDs(columns []authoringv2.Column) []string {
	ids := make([]string, 0, len(columns))
	for _, column := range columns {
		ids = append(ids, column.ColumnID)
	}
	return ids
}

func newUnifiedAuthoringFixture(t *testing.T) unifiedAuthoringFixture {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "explorer", "recipeimport", "testdata", "default-dataframer.json"))
	if err != nil {
		t.Fatalf("read checked-in imported recipe fixture: %v", err)
	}
	var bundle recipe.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode checked-in imported recipe fixture: %v", err)
	}
	if len(bundle.Outputs) == 0 {
		t.Fatal("imported recipe fixture has no outputs")
	}
	output, ok := findUnifiedRecipeOutput(bundle, importedOutputID)
	if !ok {
		t.Fatalf("imported recipe fixture has no %q output", importedOutputID)
	}
	bundle.Outputs = []recipe.Output{output}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatalf("digest imported recipe fixture: %v", err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: registry, ScopeDigest: reciperuntime.ScopeDigest,
		ResolveBundle: recipeapi.RecipeSchemaResolver(func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return nil, nil
		}, nil),
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			t.Fatal("baseline fixture unexpectedly queried live data")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("create isolated recipe resolver: %v", err)
	}
	resolver := importedRecipeSources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_medication_administration", ResourceType: "MedicationAdministration", RowRootEligible: true, RowGrain: "resource"}},
		nil,
		[]capability.Candidate{{ID: "dose-unit", NodeID: "n_medication_administration", ResourceType: "MedicationAdministration", FieldPath: "dosage.dose.unit", Label: "Dose unit", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
	)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(context.Background(), "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		if sourceErr, ok := err.(*lifecycle.Error); ok && sourceErr.Cause != nil {
			t.Fatalf("resolve checked-in imported recipe fixture: %v", sourceErr.Cause)
		}
		t.Fatalf("resolve checked-in imported recipe fixture: %v", err)
	}
	schemas := make(map[string][]recipe.StageColumn, len(source.resolved.Compiled.Outputs))
	for _, output := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(output.OutputSchema)
		if err != nil {
			t.Fatalf("derive %q imported schema: %v", output.Name, err)
		}
		for _, column := range columns {
			schemas[output.Name] = append(schemas[output.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "test"), schemas)
	if err != nil {
		t.Fatalf("import checked-in recipe into Builder document: %v", err)
	}
	var selected *authoringv2.Document
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == importedOutputID {
			selected = &workspace.Documents[index]
			break
		}
	}
	if selected == nil {
		t.Fatalf("imported workspace has no %q document", importedOutputID)
	}
	var selectedTab []authoringv2.Tab
	for _, tab := range workspace.Tabs {
		if tab.OutputID == importedOutputID {
			selectedTab = append(selectedTab, tab)
		}
	}
	workspace.Documents = []authoringv2.Document{*selected}
	workspace.Tabs = selectedTab
	if err := workspace.Validate(); err != nil {
		t.Fatalf("selected imported output workspace is invalid: %v", err)
	}
	return unifiedAuthoringFixture{bundle: bundle, digest: digest, resolver: resolver, authorized: authorized, workspace: workspace}
}

func addUnifiedAuthoringColumns(t *testing.T, document authoringv2.Document) authoringv2.Document {
	t.Helper()
	visible := true
	document.Columns = append(document.Columns,
		authoringv2.Column{
			ColumnID: doseUnitColumnID, Column: "dose_unit", Label: "Dose unit", LogicalType: "string",
			OccurrenceID: authoringv2.RootOccurrenceID,
			Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "dosage.dose.unit", ProjectionMode: "VALUE"}},
			Table:        &authoringv2.TablePresentation{Visible: &visible},
		},
		authoringv2.Column{
			ColumnID: medCategoryColumnID, Column: "medication_category", Label: "Medication category", LogicalType: "string",
			OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceCodedValue, Lookup: &authoringv2.LookupSource{
				Binding: &fhirschema.CorrelatedBinding{
					OwnerPath: "category[]", KeyPath: "category[].coding[]", ValueScope: fhirschema.CorrelatedValueScopeCoding,
					SelectionScope: fhirschema.CorrelatedSelectionScopeExact,
					SystemPath:     "system", CodePath: "code", ValuePath: "display",
					ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
				},
				Key: &fhirschema.CorrelatedKey{System: "urn:example:medication-administration-category", Code: "inpatient"},
			}},
			Table: &authoringv2.TablePresentation{Visible: &visible},
		},
	)
	if err := document.Validate(); err != nil {
		t.Fatalf("imported recipe plus Builder columns is invalid: %v", err)
	}
	return document
}

func TestImportedSourceEditRetainsBuilderAddedColumns(t *testing.T) {
	fixture := newUnifiedAuthoringFixture(t)
	document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
	params := copySourceParametersForTest(t, document, "fields")
	if params.FieldGroup == nil || len(params.FieldGroup.Fields) == 0 {
		t.Fatal("MedicationAdministration fixture has no editable source fields")
	}
	params.FieldGroup.Fields[0].Label = "Administration identifier"
	updated, err := fixture.resolver.Edit(context.Background(), "project-a", fixture.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params})
	if err != nil {
		t.Fatalf("edit imported recipe source: %v", err)
	}
	if !reflect.DeepEqual(updated.ImportedRecipe, document.ImportedRecipe) {
		t.Fatal("source edit changed the immutable imported recipe pin")
	}
	wantColumns := append([]authoringv2.Column(nil), document.Columns...)
	wantColumns[0].Label = params.FieldGroup.Fields[0].Label
	if !reflect.DeepEqual(updated.Columns, wantColumns) {
		t.Fatalf("source edit changed the authored column sequence or dropped Builder additions: got ids %v, want ids %v", unifiedColumnIDs(updated.Columns), unifiedColumnIDs(wantColumns))
	}

	workspace := fixture.workspace
	workspace.Documents = []authoringv2.Document{updated}
	resolved, err := fixture.resolver.Resolve(context.Background(), "project-a", workspace, fixture.authorized)
	if err != nil {
		t.Fatalf("resolve edited imported recipe: %v", err)
	}
	compiled, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "test", workspace, fixture.authorized.Snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved})
	if err != nil {
		t.Fatalf("compile imported recipe after source edit: %v", err)
	}
	if len(compiled.Bundle.Outputs) != 1 || len(compiled.Presentations) != 1 || len(compiled.OutputContracts) != 1 {
		t.Fatalf("edited compile has incomplete output metadata: %#v", compiled)
	}
	output := compiled.Bundle.Outputs[0]
	var doseUnit *recipe.Field
	for index := range output.Fields {
		if output.Fields[index].ColumnID == doseUnitColumnID {
			doseUnit = &output.Fields[index]
			break
		}
	}
	if doseUnit == nil || doseUnit.Name != "dose_unit" || doseUnit.Label != "Dose unit" || doseUnit.FieldRef != "dosage.dose.unit" {
		t.Fatalf("compiled direct Builder projection lost its identity or meaning: %#v", doseUnit)
	}
	var medicationCategory *recipe.Pivot
	for index := range output.Pivots {
		if output.Pivots[index].ColumnAliases["inpatient"] == "medication_category" {
			medicationCategory = &output.Pivots[index]
			break
		}
	}
	if medicationCategory == nil || medicationCategory.Correlation == nil || medicationCategory.CorrelationCode != "inpatient" {
		t.Fatalf("compiled coded-value Builder projection lost its exact category binding: %#v", medicationCategory)
	}
	presentation := compiled.Presentations[0].Columns
	columnOrder := map[string]int{}
	for _, column := range presentation {
		columnOrder[column.PublicColumn] = column.Order
	}
	doseOrder, dosePresent := columnOrder["dose_unit"]
	categoryOrder, categoryPresent := columnOrder["medication_category"]
	if !dosePresent || !categoryPresent || doseOrder >= categoryOrder {
		t.Fatalf("compiled presentation lost authored column order: dose=%d present=%v category=%d present=%v", doseOrder, dosePresent, categoryOrder, categoryPresent)
	}
	for _, expected := range []string{"dose_unit", "medication_category"} {
		found := false
		for _, column := range compiled.OutputContracts[0].Columns {
			if column.Column == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("compiled output contract omitted authored column %q", expected)
		}
	}
}
