package importedsource

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestRemoveFieldEditsLegacyPinAndPreservesAuthoredAddition(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "resource", Fields: []recipe.Field{
		{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
		{Name: "gender", Expr: recipe.Expression{Select: "root.gender"}},
	}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	resolver := Sources{registry: registry, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		if typed, ok := err.(*ports.Error); ok {
			t.Fatalf("load imported source: %s: %v", typed.Code, typed.Cause)
		}
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, compiled := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(compiled.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			schemas[compiled.Name] = append(schemas[compiled.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "patients"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	if len(document.ImportedSource.FieldBindings) != 0 {
		t.Fatal("fixture must represent a legacy draft without FieldBindings")
	}
	pinnedID := ""
	for _, column := range document.Columns {
		if column.Column == "id" {
			pinnedID = column.ColumnID
		}
	}
	if pinnedID == "" {
		t.Fatal("fixture is missing its sibling pinned field")
	}
	authored := authoringv2.Column{ColumnID: "project-id", Column: "project_id", Label: "Project", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}}
	directField := authoringv2.Column{ColumnID: "patient-birthdate", Column: "birth_date", Label: "Birth date", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}}
	codedValue := authoringv2.Column{
		ColumnID: "coded-value", Column: "identifier_type", Label: "Identifier type", OccurrenceID: authoringv2.RootOccurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceCodedValue, Lookup: &authoringv2.LookupSource{
			Binding: &fhirschema.CorrelatedBinding{OwnerPath: "identifier[]", KeyPath: "identifier[].type.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "value", LogicalType: "string"},
			Key:     &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "MRN"},
		}},
	}
	document.Columns = append(document.Columns, authored, directField, codedValue)
	network, err := resolver.Network(ctx, "project-a", authorized, document)
	if err != nil {
		t.Fatal(err)
	}
	var target authoringv2.ProjectedField
	for _, field := range network.ProjectedFields {
		if field.Column == "gender" {
			target = field
			break
		}
	}
	if target.ColumnID == "" || target.Owner != authoringv2.ProjectedFieldOwnerPinned || target.Removal != authoringv2.ProjectedFieldRemovalDisablePinned || target.SourceStepID != "fields" || target.SourceFieldID != "fields[1]" {
		t.Fatalf("legacy pinned field removal capability = %+v", target)
	}
	updated, err := resolver.RemoveField(ctx, "project-a", authorized, document, target.SourceStepID, target.SourceFieldID)
	if err != nil {
		t.Fatal(err)
	}
	if document.ImportedRecipe == nil || updated.ImportedRecipe == nil || *document.ImportedRecipe != *updated.ImportedRecipe {
		t.Fatal("field removal changed the immutable pin")
	}
	if hasServerColumn(updated, target.ColumnID) || !hasServerColumn(updated, "project-id") {
		t.Fatalf("pinned removal dropped authored or target identity was reused: %#v", updated.Columns)
	}
	if len(updated.ImportedSource.FieldBindings) != 2 || updated.ImportedSource.Steps[1].Parameters.FieldGroup.Fields[1].Enabled {
		t.Fatalf("exact source field edit did not persist its slot bindings: %#v", updated.ImportedSource)
	}
	if len(updated.Columns) != 4 || !hasServerColumn(updated, pinnedID) || !hasServerColumn(updated, "patient-birthdate") || !hasServerColumn(updated, "coded-value") {
		t.Fatalf("source rebuild did not preserve sibling pin field, direct field, and codedValue addition: %#v", updated.Columns)
	}
	for _, column := range updated.Columns {
		var expected *authoringv2.Column
		switch column.ColumnID {
		case authored.ColumnID:
			expected = &authored
		case directField.ColumnID:
			expected = &directField
		case codedValue.ColumnID:
			expected = &codedValue
		}
		if expected != nil && (column.Column != expected.Column || column.Label != expected.Label || !reflect.DeepEqual(column.Source, expected.Source)) {
			t.Fatalf("source rebuild changed authored addition: %+v", column)
		}
	}
}

func TestPinnedFieldRemovalInventoryKeepsUneditedTraversalFieldActionable(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{
		Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields:     []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "status", Expr: recipe.Expression{Select: "root.status"}}},
		Traversals: []recipe.Traversal{{Name: "subject_Patient", Alias: "patient", ToResourceType: "Patient", OccurrenceID: "patient_subject", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "patient.id"}}}}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	resolver := Sources{registry: &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}},
		[]capability.Edge{{ID: "subject", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject", StorageDirection: "OUTBOUND"}}, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		if typed, ok := err.(*ports.Error); ok {
			t.Fatalf("load traversal source: %s: %v", typed.Code, typed.Cause)
		}
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, compiled := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(compiled.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			schemas[compiled.Name] = append(schemas[compiled.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "Specimen"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	if len(document.ImportedSource.FieldBindings) != 0 {
		t.Fatal("fixture must start as a legacy draft with no field bindings")
	}
	network, err := resolver.Network(ctx, "project-a", authorized, document)
	if err != nil {
		t.Fatal(err)
	}
	rootStatus, traversalID := "", ""
	for _, field := range network.ProjectedFields {
		if field.Column == "status" {
			rootStatus = field.ColumnID
		}
		if field.Column == "patient__id" {
			traversalID = field.ColumnID
			if field.Removal != authoringv2.ProjectedFieldRemovalDisablePinned || field.SourceFieldID != "traversals[0].fields[0]" {
				t.Fatalf("initial traversal field has no exact removal action: %+v", field)
			}
		}
	}
	if rootStatus == "" || traversalID == "" {
		t.Fatalf("fixture output columns not found: %#v", network.ProjectedFields)
	}
	updated, err := resolver.RemoveField(ctx, "project-a", authorized, document, "fields", "fields[1]")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.ImportedSource.FieldBindings) != 2 {
		t.Fatalf("root edit should bind only its field group: %#v", updated.ImportedSource.FieldBindings)
	}
	for _, binding := range updated.ImportedSource.FieldBindings {
		if binding.FieldID == "traversals[0].fields[0]" {
			t.Fatal("fixture unexpectedly persisted a binding for the unrelated traversal group")
		}
	}
	after, err := resolver.Network(ctx, "project-a", authorized, updated)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range after.ProjectedFields {
		if field.ColumnID == traversalID && (field.Removal != authoringv2.ProjectedFieldRemovalDisablePinned || field.SourceFieldID != "traversals[0].fields[0]") {
			t.Fatalf("unmodified traversal field lost its exact removal action after root edit: %+v", field)
		}
	}
}

func hasServerColumn(document authoringv2.Document, columnID string) bool {
	for _, column := range document.Columns {
		if column.ColumnID == columnID {
			return true
		}
	}
	return false
}

func TestPinnedFieldRemovalUsesExactLegacySlotsAcrossRepeatedResourceOccurrences(t *testing.T) {
	output := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		Fields:             []recipe.Field{{Name: "status", Expr: recipe.Expression{Select: "root.status"}}},
		CatalogProjections: []recipe.CatalogProjection{{Name: "patient_fields", MaxColumns: 10}},
		Traversals: []recipe.Traversal{
			{Name: "subject", Alias: "patient_subject", ToResourceType: "Patient", OccurrenceID: "patient_subject", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "patient_subject.id"}}}},
			{Name: "performer", Alias: "patient_performer", ToResourceType: "Patient", OccurrenceID: "patient_performer", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "patient_performer.id"}}}},
		},
	}
	plan := recipeimport.SourcePlan(output)
	columns := []lower.CompiledOutputColumn{
		{ID: "subject-id", Name: "patient_subject__id", SourceOrigin: &lower.CompiledSourceOrigin{SourceKind: "FIELD_GROUP", OccurrenceID: "patient_subject", SourcePath: "traversals[0].fields"}},
		{ID: "performer-id", Name: "patient_performer__id", SourceOrigin: &lower.CompiledSourceOrigin{SourceKind: "FIELD_GROUP", OccurrenceID: "patient_performer", SourcePath: "traversals[1].fields"}},
		{ID: "discovered-gender", Name: "gender", SourceOrigin: &lower.CompiledSourceOrigin{SourceKind: "CATALOG_PROJECTION", SourceKey: "patient_fields", OutputKey: "gender", OccurrenceID: "base", SourcePath: "catalogProjections[0]"}},
	}
	document := authoringv2.Document{
		Output:         authoringv2.Output{ID: "observations"},
		ImportedRecipe: &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: "pin", Output: "observations"},
		ImportedSource: &plan,
		Columns: []authoringv2.Column{
			{ColumnID: "subject-id", Column: "patient_subject__id", Label: "Subject ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: "patient_subject__id"}}},
			{ColumnID: "performer-id", Column: "patient_performer__id", Label: "Performer ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: "patient_performer__id"}}},
			{ColumnID: "discovered-gender", Column: "gender", Label: "Gender", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: "gender"}}},
		},
	}
	network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Occurrences = []authoringv2.NetworkOccurrence{
		{OccurrenceID: "base", ResourceType: "Observation", Enabled: true},
		{OccurrenceID: "patient_subject", SourceStepID: "traversals[0]", ResourceType: "Patient", Enabled: true},
		{OccurrenceID: "patient_performer", SourceStepID: "traversals[1]", ResourceType: "Patient", Enabled: true},
	}
	network.Bindings = []authoringv2.NetworkColumnBinding{
		{ColumnID: "subject-id", Status: "UNKNOWN"},
		{ColumnID: "performer-id", Status: "UNKNOWN"},
		{ColumnID: "discovered-gender", Status: "UNKNOWN"},
	}
	bound, err := recipeimport.BindCompiledNetwork(network, columns, &plan)
	if err != nil {
		t.Fatal(err)
	}
	bound.ProjectedFields = authoringv2.ProjectedFieldsForDocument(document, bound)
	current := importedRecipeResolution{resolved: dataframeexecution.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: output.Name, OutputSchema: columns}}}}}
	if err := bindPinnedFieldRemoval(document, output, current, plan, &bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.ProjectedFields) != 3 {
		t.Fatalf("inventory hid a source output: %#v", bound.ProjectedFields)
	}
	byID := map[string]authoringv2.ProjectedField{}
	for _, field := range bound.ProjectedFields {
		byID[field.ColumnID] = field
	}
	for _, want := range []struct {
		id, occurrence, sourceStep, fieldID string
	}{
		{"subject-id", "patient_subject", "traversals[0].fields", "traversals[0].fields[0]"},
		{"performer-id", "patient_performer", "traversals[1].fields", "traversals[1].fields[0]"},
	} {
		got := byID[want.id]
		if got.Owner != authoringv2.ProjectedFieldOwnerPinned || got.Status != authoringv2.ProjectedFieldStatusExact || got.Removal != authoringv2.ProjectedFieldRemovalDisablePinned || got.OccurrenceID != want.occurrence || got.SourceStepID != want.sourceStep || got.SourceFieldID != want.fieldID {
			t.Fatalf("legacy field %q mapping = %+v, want exact slot %+v", want.id, got, want)
		}
	}
	generated := byID["discovered-gender"]
	if generated.Owner != authoringv2.ProjectedFieldOwnerPinned || generated.Status != authoringv2.ProjectedFieldStatusExact || generated.SourceStepID != "catalogProjections[0]" || generated.Removal != authoringv2.ProjectedFieldRemovalDisablePinnedOutput || generated.SourceOutputKey != "gender" || generated.SourceFieldID != "" {
		t.Fatalf("generated projection must have an exact output removal identity: %+v", generated)
	}
}

func TestPinnedPivotOutputGetsExactRemovalIdentity(t *testing.T) {
	output := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		Pivots: []recipe.Pivot{{Name: "components", Columns: []string{"urn:loinc|8480-6", "urn:loinc|8462-4"}, ColumnExpr: recipe.Expression{Select: "root.code"}, ValueExpr: recipe.Expression{Select: "root.value"}}},
	}
	plan := recipeimport.SourcePlan(output)
	columns := []lower.CompiledOutputColumn{
		{ID: "systolic", Name: "components__urn_loinc_8480_6", SourceOrigin: &lower.CompiledSourceOrigin{SourceKind: "PIVOT", SourceKey: "components", OutputKey: "urn:loinc|8480-6", OccurrenceID: "base", SourcePath: "pivots[0]"}},
	}
	document := authoringv2.Document{
		Output:         authoringv2.Output{ID: "observations"},
		ImportedRecipe: &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: "pin", Output: "observations"},
		ImportedSource: &plan,
		Columns:        []authoringv2.Column{{ColumnID: "systolic", Column: columns[0].Name, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: columns[0].Name}}}},
	}
	network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Occurrences = []authoringv2.NetworkOccurrence{{OccurrenceID: "base", ResourceType: "Observation", Enabled: true}}
	network.Bindings = []authoringv2.NetworkColumnBinding{{ColumnID: "systolic", Status: "UNKNOWN"}}
	bound, err := recipeimport.BindCompiledNetwork(network, columns, &plan)
	if err != nil {
		t.Fatal(err)
	}
	bound.ProjectedFields = authoringv2.ProjectedFieldsForDocument(document, bound)
	current := importedRecipeResolution{resolved: dataframeexecution.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: output.Name, OutputSchema: columns}}}}}
	if err := bindPinnedFieldRemoval(document, output, current, plan, &bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.ProjectedFields) != 1 {
		t.Fatalf("projected field count = %d", len(bound.ProjectedFields))
	}
	got := bound.ProjectedFields[0]
	if got.ColumnID != "systolic" || got.SourceStepID != "pivots[0]" || got.SourceOutputKey != "urn:loinc|8480-6" || got.Removal != authoringv2.ProjectedFieldRemovalDisablePinnedOutput {
		t.Fatalf("pivot output removal identity = %+v", got)
	}
}

func TestDisablePinnedProjectionOutputChangesOnlyExactKey(t *testing.T) {
	name := "systolic"
	catalog := authoringv2.ImportedCatalogParameters{Outputs: []authoringv2.ImportedCatalogOutput{
		{FieldPath: "status", Enabled: true, Name: "study_status"},
		{FieldPath: "birthDate", Enabled: true},
	}}
	changed, err := disableCatalogOutput(&catalog, "status")
	if err != nil || !changed || catalog.Outputs[0].Enabled || catalog.Outputs[0].Name != "study_status" || !catalog.Outputs[1].Enabled {
		t.Fatalf("catalog exact-key disable = changed %t, outputs %+v, err %v", changed, catalog.Outputs, err)
	}
	if _, err := disableCatalogOutput(&catalog, "status"); err == nil {
		t.Fatal("repeated catalog output disable was accepted")
	}
	pivot := authoringv2.ImportedPivotParameters{Outputs: []authoringv2.ImportedPivotOutput{
		{Key: "urn:loinc|8480-6", Enabled: true, Name: &name},
		{Key: "urn:loinc|8462-4", Enabled: true},
	}}
	changed, err = disablePivotOutput(&pivot, "urn:loinc|8480-6")
	if err != nil || !changed || pivot.Outputs[0].Enabled || pivot.Outputs[0].Name == nil || *pivot.Outputs[0].Name != name || !pivot.Outputs[1].Enabled {
		t.Fatalf("pivot exact raw-key disable = changed %t, outputs %+v, err %v", changed, pivot.Outputs, err)
	}
	if _, err := disablePivotOutput(&pivot, "urn:loinc|8480-6"); err == nil {
		t.Fatal("repeated pivot output disable was accepted")
	}
	const whitespaceKey = "  urn:study:raw-key  "
	whitespacePivot := authoringv2.ImportedPivotParameters{Outputs: []authoringv2.ImportedPivotOutput{{Key: whitespaceKey, Enabled: true}}}
	if changed, err := disablePivotOutput(&whitespacePivot, whitespaceKey); err != nil || !changed || whitespacePivot.Outputs[0].Enabled {
		t.Fatalf("pivot raw whitespace key was not preserved: changed=%t outputs=%+v err=%v", changed, whitespacePivot.Outputs, err)
	}
}

func TestExtensionOutputRemovalRequiresOpaqueCompilerKeyAndPreservesSibling(t *testing.T) {
	key := "ext-v1:" + strings.Repeat("ab", 32)
	other := "ext-v1:" + strings.Repeat("cd", 32)
	parameters := &authoringv2.ImportedExtensionParameters{Outputs: []authoringv2.ImportedExtensionOutput{
		{Key: key, Enabled: true}, {Key: other, Enabled: true},
	}}
	step := authoringv2.ImportedSourceStep{Kind: recipeimport.SourcePlanExtensionColumn, Parameters: &authoringv2.ImportedSourceParameters{Extension: parameters}}
	if !supportsPinnedOutputRemoval(step, key) || supportsPinnedOutputRemoval(step, "last_segment") || supportsPinnedOutputRemoval(step, "ext-v1:abcd") {
		t.Fatal("extension removal accepted a non-compiler opaque mapping key")
	}
	if changed, err := disableExtensionOutput(parameters, key); err != nil || !changed || parameters.Outputs[0].Enabled || !parameters.Outputs[1].Enabled {
		t.Fatalf("extension exact-key disable = changed %t, outputs %+v, err %v", changed, parameters.Outputs, err)
	}
	if _, err := disableExtensionOutput(parameters, key); err == nil {
		t.Fatal("repeated extension output disable was accepted")
	}
	if _, err := disableExtensionOutput(parameters, "last_segment"); err == nil {
		t.Fatal("a runtime URL key was accepted as an extension output identity")
	}
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
		ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 2,
			Columns: []recipe.ExtensionColumnMapping{{Name: "parent__status", URL: "https://example.test/parent/status", SourcePath: "extension[]", ValuePath: "valueString", ValueType: "string"}}}},
	}
	plan := recipeimport.SourcePlan(output)
	columns := []lower.CompiledOutputColumn{{ID: "extension-status", Name: "extensions__parent__status", SourceOrigin: &lower.CompiledSourceOrigin{
		SourceKind: "EXTENSION_COLUMN", SourceKey: "extensions", OutputKey: key, OccurrenceID: "base", SourcePath: "extensionColumns[0]",
	}}}
	document := authoringv2.Document{
		Output: authoringv2.Output{ID: "patients"}, ImportedRecipe: &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: "pin", Output: "patients"},
		ImportedSource: &plan, Columns: []authoringv2.Column{{ColumnID: "extension-status", Column: columns[0].Name, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: columns[0].Name}}}},
	}
	network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Occurrences = []authoringv2.NetworkOccurrence{{OccurrenceID: "base", ResourceType: "Patient", Enabled: true}}
	network.Bindings = []authoringv2.NetworkColumnBinding{{ColumnID: "extension-status", Status: "UNKNOWN"}}
	bound, err := recipeimport.BindCompiledNetwork(network, columns, &plan)
	if err != nil {
		t.Fatal(err)
	}
	bound.ProjectedFields = authoringv2.ProjectedFieldsForDocument(document, bound)
	current := importedRecipeResolution{resolved: dataframeexecution.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: output.Name, OutputSchema: columns}}}}}
	if err := bindPinnedFieldRemoval(document, output, current, plan, &bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.ProjectedFields) != 1 {
		t.Fatalf("extension projected field inventory = %+v", bound.ProjectedFields)
	}
	field := bound.ProjectedFields[0]
	if field.Removal != authoringv2.ProjectedFieldRemovalDisablePinnedOutput || field.SourceStepID != "extensionColumns[0]" || field.SourceOutputKey != key {
		t.Fatalf("extension compiler binding did not yield exact removal: %+v", field)
	}
}

func TestRemoveOutputDisablesExactNestedExtensionMappingAcrossReload(t *testing.T) {
	ctx := context.Background()
	const parentA = "https://example.test/parent-a"
	const parentB = "https://example.test/parent-b"
	const statusURL = "https://example.test/status"
	fields := []fieldprofile.PopulatedField{
		{ResourceType: "DocumentReference", Path: "extension[].extension[].url", ExtensionValues: []fieldprofile.ExtensionValueObservation{{URL: statusURL, SourcePath: "extension[].extension[]", URLPath: []string{parentA}, ValuePath: "valueString", ValueType: "string"}}},
		{ResourceType: "DocumentReference", Path: "extension[].extension[].url", ExtensionValues: []fieldprofile.ExtensionValueObservation{{URL: statusURL, SourcePath: "extension[].extension[]", URLPath: []string{parentB}, ValuePath: "valueString", ValueType: "string"}}},
	}
	output := recipe.Output{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file", ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 8}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:    registry,
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		ScopeDigest: reciperuntime.ScopeDigest,
		ResolveBundle: reciperuntime.RecipeSchemaResolver(func(_ context.Context, options fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
			if options.ResourceType != "DocumentReference" {
				return nil, fmt.Errorf("unexpected discovery resource type %q", options.ResourceType)
			}
			return fields, nil
		}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "document-reference", ResourceType: "DocumentReference", RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	resolved, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		t.Fatal(err)
	}
	mappings := resolved.resolved.Bundle.Outputs[0].ExtensionColumns[0].Columns
	keyA, keyB := "", ""
	for _, mapping := range mappings {
		switch mapping.Name {
		case "parent_a__status":
			keyA = recipe.ExtensionColumnOutputKey(mapping)
		case "parent_b__status":
			keyB = recipe.ExtensionColumnOutputKey(mapping)
		}
	}
	if !isExtensionOutputKey(keyA) || !isExtensionOutputKey(keyB) || keyA == keyB {
		t.Fatalf("nested mapping identities collided or were not opaque: %q %q mappings=%+v", keyA, keyB, mappings)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, compiled := range resolved.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(compiled.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			schemas[compiled.Name] = append(schemas[compiled.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "DocumentReference"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := resolver.RemoveOutput(ctx, "project-a", authorized, workspace.Documents[0], "extensionColumns[0]", keyA)
	if err != nil {
		t.Fatal(err)
	}
	var extension *authoringv2.ImportedExtensionParameters
	for _, step := range updated.ImportedSource.Steps {
		if step.ID == "extensionColumns[0]" && step.Parameters != nil {
			extension = step.Parameters.Extension
		}
	}
	if extension == nil || len(extension.Outputs) != 1 || extension.Outputs[0].Key != keyA || extension.Outputs[0].Enabled {
		t.Fatalf("persisted extension output override = %+v", extension)
	}
	reloaded, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized, updated)
	if err != nil {
		t.Fatal(err)
	}
	var foundA, foundB bool
	for _, compiled := range reloaded.resolved.Compiled.Outputs {
		for _, column := range compiled.OutputSchema {
			if column.SourceOrigin == nil {
				continue
			}
			foundA = foundA || column.SourceOrigin.OutputKey == keyA
			foundB = foundB || column.SourceOrigin.OutputKey == keyB
		}
	}
	if foundA || !foundB {
		t.Fatalf("reload restored the removed extension mapping or lost its sibling: foundRemoved=%t foundSibling=%t", foundA, foundB)
	}
}

func TestRemoveOutputDisablesExactDynamicKeyAndPreservesSibling(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		DynamicColumns: []recipe.DynamicColumn{{
			Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"},
			Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"},
			Columns: []string{"key-a", "key-b"}, MaxColumns: 4,
		}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:    registry,
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		ScopeDigest: reciperuntime.ScopeDigest,
		ResolveBundle: reciperuntime.RecipeSchemaResolver(func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
			return nil, nil
		}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	resolved, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		if typed, ok := err.(*ports.Error); ok {
			t.Fatalf("load source: %s: %#v", typed.Code, typed.Cause)
		}
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, compiled := range resolved.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(compiled.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			schemas[compiled.Name] = append(schemas[compiled.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "patients"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := resolver.RemoveOutput(ctx, "project-a", authorized, workspace.Documents[0], "dynamicColumns[0]", "key-a")
	if err != nil {
		t.Fatal(err)
	}
	var dynamic *authoringv2.ImportedDynamicParameters
	for _, step := range updated.ImportedSource.Steps {
		if step.ID == "dynamicColumns[0]" && step.Parameters != nil {
			dynamic = step.Parameters.Dynamic
		}
	}
	if dynamic == nil || len(dynamic.Outputs) != 1 || dynamic.Outputs[0].Key != "key-a" || dynamic.Outputs[0].Enabled {
		t.Fatalf("saved dynamic output override = %+v", dynamic)
	}
	removed, sibling := false, false
	for _, column := range updated.Columns {
		if column.Source.Imported == nil {
			continue
		}
		removed = removed || column.Column == "identifiers_key_a"
		sibling = sibling || column.Column == "identifiers_key_b"
	}
	if removed || !sibling {
		t.Fatalf("dynamic output removal changed siblings: removed=%t sibling=%t columns=%+v", removed, sibling, updated.Columns)
	}
}
