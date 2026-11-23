package compilation

import (
	"context"
	"encoding/json"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

func TestImportedRecipeCompilationPreservesSourceOutputAndUsesStructuralSchema(t *testing.T) {
	output := recipe.Output{Name: "GroupMember", RootResourceType: "Group", RowGrain: "GROUP_MEMBER", Expand: &recipe.Expansion{From: recipe.Expression{Select: "root.member[]"}, As: "member"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Call: "uuid5", Args: []recipe.Expression{{Literal: json.RawMessage(`"stable namespace"`)}, {Select: "root.id"}, {Select: "member.entity.reference"}}}}, Fields: []recipe.Field{{Name: "member_id", Expr: recipe.Expression{Call: "reference_id", Args: []recipe.Expression{{Select: "member.entity.reference"}}}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	document := ws.Document{Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Group members"}, RootResourceType: output.RootResourceType, Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType}, Rows: recipeimport.ImportRows(output), ImportedRecipe: &ref, Columns: []ws.Column{{ColumnID: "member_id_source", Column: "member_id", Label: "Member", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: "member_id"}}, Table: &ws.TablePresentation{Visible: &visible}}}}
	resolvedOutput := output
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Outputs: []recipe.Output{resolvedOutput}}, SourceColumns: []ResolvedImportedSourceColumn{{Column: recipe.StageColumn{ID: "member_id_source", Name: "member_id", Type: "string"}, Cardinality: "required_one", Shape: "scalar", SemanticPath: "member.entity.reference"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, ws.CatalogSnapshot{}, capability.Snapshot{}, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	got := compiled.Bundle.Outputs[0]
	if got.Name != document.Output.ID || got.Identity == nil || got.Identity.Name != output.Identity.Name || got.Identity.Expr.Call != "uuid5" || len(got.Fields) != 1 || got.Fields[0].Name != output.Fields[0].Name {
		t.Fatalf("imported source operations or identity changed: %#v", got)
	}
	if got.Construction == nil || len(got.Construction.SourceColumns) != 1 || got.Construction.SourceColumns[0].ID != "member_id_source" || got.Construction.SourceColumns[0].Name != "member_id" {
		t.Fatalf("legacy source column did not receive stable construction identity: %#v", got.Construction)
	}
	if got.Fields[0].ColumnID != output.Fields[0].ColumnID || got.Expand.From.Select != output.Expand.From.Select {
		t.Fatalf("source recipe field or expansion changed: %#v", got)
	}
	if compiled.OutputContract.Lossless || compiled.OutputContract.MLReady || len(compiled.OutputContract.LossReasons) == 0 || compiled.OutputContract.Columns[0].LogicalType != "string" || compiled.OutputContract.Columns[0].Filterable || compiled.OutputContract.Columns[0].Chartable {
		t.Fatalf("unsafe imported public assessment: %#v", compiled.OutputContract)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("imported output contract does not match emitted schema: %v", err)
	}
}

func TestImportedRecipeRejectsStalePinnedSourceAndEditedInputs(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD"}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	workspace := ws.Workspace{Documents: []ws.Document{{ImportedRecipe: &ref, Rows: ws.RecordsRowDefinition()}}, SemanticsVersion: ws.CurrentSemanticsVersion}
	input := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle}
	if err := validateResolvedImportedRecipeCoverage(workspace, ws.CatalogSnapshot{}, ResolvedInputs{ImportedRecipes: []ResolvedImportedRecipe{input}}); err != nil {
		t.Fatalf("valid pin rejected: %v", err)
	}
	input.Bundle.Outputs[0].RowGrain = "CHANGED"
	if err := validateResolvedImportedRecipeCoverage(workspace, ws.CatalogSnapshot{}, ResolvedInputs{ImportedRecipes: []ResolvedImportedRecipe{input}}); err == nil {
		t.Fatal("stale pinned source accepted")
	}
}

func TestImportedRecipePreservesConstructionOverlay(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD", Fields: []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	column := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	minimum := int64(18)
	document := ws.Document{Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType, Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType}, Rows: ws.RecordsRowDefinition(), ImportedRecipe: &ref, Columns: []ws.Column{{ColumnID: column.ID, Column: column.Name, Label: column.Label, OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: column.Name}}, Table: &ws.TablePresentation{Visible: &visible}}}, Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{ID: "adults", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: column.ID, Operator: ws.ConstructionFilterGreaterEq, Values: []ws.FilterValue{{Kind: ws.ConstructionFilterInteger, Integer: &minimum}}}}, Outputs: []ws.StageColumn{{ID: column.ID, Name: column.Name, Label: column.Label, Type: "integer", Table: &ws.TablePresentation{Visible: &visible}}}}}}}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: column, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, ws.CatalogSnapshot{}, capability.Snapshot{}, resolved)
	if err != nil {
		t.Fatalf("supported construction overlay rejected: %v", err)
	}
	if compiled.Bundle.Outputs[0].Construction == nil {
		t.Fatal("construction overlay was dropped")
	}
	if compiled.Bundle.Outputs[0].Construction.Steps[0].Operation.Kind != recipe.ConstructionFilterOp {
		t.Fatalf("filter overlay operation = %#v", compiled.Bundle.Outputs[0].Construction.Steps[0].Operation)
	}
	if err := compiled.Bundle.Validate(); err != nil {
		t.Fatalf("translated imported filter bundle is invalid: %v", err)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("filtered imported output contract does not match emitted schema: %v", err)
	}
}

func TestImportedRecipeCompilesAdditiveRootColumn(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD", Fields: []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	importedColumn := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType,
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType},
		Rows:  ws.RecordsRowDefinition(), ImportedRecipe: &ref,
		Columns: []ws.Column{
			{ColumnID: importedColumn.ID, Column: importedColumn.Name, Label: importedColumn.Label, LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: importedColumn.Name}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_birth_date", Column: "birthDate", Label: "Birth date", LogicalType: "date", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_given_all", Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "ALL"}}, Table: &ws.TablePresentation{Visible: &visible}},
		},
	}
	catalog := ws.CatalogSnapshot{
		Nodes: []ws.CatalogNode{{ID: "node-patient", ResourceType: "Patient"}},
		Candidates: []ws.CatalogCandidate{
			{ID: "candidate-birth-date", NodeID: "node-patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE"},
			{ID: "candidate-given", NodeID: "node-patient", FieldPath: "name[].given[]", Label: "Given", LogicalType: "string", Cardinality: "many", ProjectionModes: []string{"FIRST", "ALL", "DISTINCT"}, DefaultProjectionMode: "ALL"},
		},
	}
	capabilitySnapshot := capability.Snapshot{
		Status: capability.StatusReady, Complete: true,
		Nodes: []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true}},
		Candidates: []capability.Candidate{
			{ID: "candidate-birth-date", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
			{ID: "candidate-given", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "name[].given[]", Label: "Given", LogicalType: "string", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}},
		},
	}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: importedColumn, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, catalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	got := compiled.Bundle.Outputs[0]
	if len(got.Fields) != 3 || got.Fields[1].Name != "birthDate" || got.Fields[1].Expr.Select != "root.birthDate" || got.Fields[1].ColumnID != "additive_birth_date" {
		t.Fatalf("additive root field was not compiled into the imported output: %#v", got.Fields)
	}
	if got.Fields[2].ValueMode != recipe.ValueModeAll || compiled.EmittedColumns[2].Shape != "array" {
		t.Fatalf("additive repeated projection lost authored ALL intent: field=%#v emitted=%#v", got.Fields[2], compiled.EmittedColumns[2])
	}
	if len(compiled.OutputContract.Columns) != 3 || compiled.OutputContract.Columns[1].Column != "birthDate" || compiled.OutputContract.Columns[2].Shape != "array" {
		t.Fatalf("additive root column missing from output contract: %#v", compiled.OutputContract.Columns)
	}
	if err := compiled.Bundle.Validate(); err != nil {
		t.Fatalf("additive imported bundle is invalid: %v", err)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("additive imported output contract does not match emitted schema: %v", err)
	}
}

func TestImportedRecipeCompilesAdditiveRelatedColumn(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD",
		Fields:     []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}},
		Traversals: []recipe.Traversal{{Name: "subject", OccurrenceID: "rel_obs", ToResourceType: "Observation", MatchMode: recipe.MatchOptional}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	importedColumn := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType,
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "rel_obs", ResourceType: "Observation", Relationship: "subject", CatalogEdgeID: "e-obs", MatchMode: ws.RouteMatchOptional}}},
		Rows:  ws.RecordsRowDefinition(), ImportedRecipe: &ref,
		Columns: []ws.Column{
			{ColumnID: importedColumn.ID, Column: importedColumn.Name, Label: importedColumn.Label, LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: importedColumn.Name}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_status", Column: "status", Label: "Observation status", LogicalType: "string", OccurrenceID: "rel_obs", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
		},
	}
	wireCatalog := ws.CatalogSnapshot{
		Nodes:      []ws.CatalogNode{{ID: "n-patient", ResourceType: "Patient"}, {ID: "n-obs", ResourceType: "Observation"}},
		Edges:      []ws.CatalogEdge{{ID: "e-obs", FromNodeID: "n-patient", ToNodeID: "n-obs", Label: "subject"}},
		Candidates: []ws.CatalogCandidate{{ID: "candidate-status", NodeID: "n-obs", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}}},
	}
	capabilitySnapshot := capability.Snapshot{
		Status: capability.StatusReady, Complete: true,
		Nodes:      []capability.Node{{ID: "n-patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "n-obs", ResourceType: "Observation"}},
		Edges:      []capability.Edge{{ID: "e-obs", FromNodeID: "n-patient", ToNodeID: "n-obs", Label: "subject", SourceResourceType: "Patient", TargetResourceType: "Observation"}},
		Candidates: []capability.Candidate{{ID: "candidate-status", NodeID: "n-obs", ResourceType: "Observation", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}},
	}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: importedColumn, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, wireCatalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	got := compiled.Bundle.Outputs[0]
	if len(got.Traversals) != 1 || len(got.Traversals[0].Fields) != 1 {
		t.Fatalf("related additive field was not merged into the imported traversal: %#v", got.Traversals)
	}
	field := got.Traversals[0].Fields[0]
	if field.Name != "status" || field.Expr.Select != "subject.status" || field.ColumnID != "additive_status" {
		t.Fatalf("related additive field selector/identity = %#v", field)
	}
	if emitted := compiled.EmittedColumns[len(compiled.EmittedColumns)-1]; emitted.OccurrenceID != "rel_obs" || emitted.SourcePath != "status" {
		t.Fatalf("related additive emission = %#v", emitted)
	}
	if err := compiled.Bundle.Validate(); err != nil {
		t.Fatalf("related additive imported bundle is invalid: %v", err)
	}
	again, err := compileImportedDocument(context.Background(), "project", "explorer", document, wireCatalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("second compileImportedDocument: %v", err)
	}
	if fields := again.Bundle.Outputs[0].Traversals[0].Fields; len(fields) != 1 {
		t.Fatalf("resolved bundle was mutated across compiles: %#v", fields)
	}
}

func TestImportedRecipeAdditiveRelatedColumnToleratesUnobservedOptionalEdge(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD",
		Fields:     []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}},
		Traversals: []recipe.Traversal{{Name: "subject", OccurrenceID: "rel_obs", ToResourceType: "Observation", MatchMode: recipe.MatchOptional}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	importedColumn := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "rel_obs", ResourceType: "Observation", Relationship: "subject", MatchMode: ws.RouteMatchOptional}}},
		Rows:  ws.RecordsRowDefinition(), ImportedRecipe: &ref,
		Columns: []ws.Column{
			{ColumnID: importedColumn.ID, Column: importedColumn.Name, Label: importedColumn.Label, LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: importedColumn.Name}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_status", Column: "status", Label: "Observation status", LogicalType: "string", OccurrenceID: "rel_obs", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
		},
	}
	wireCatalog := ws.CatalogSnapshot{
		Nodes:      []ws.CatalogNode{{ID: "n-patient", ResourceType: "Patient"}, {ID: "n-obs", ResourceType: "Observation"}},
		Candidates: []ws.CatalogCandidate{{ID: "candidate-status", NodeID: "n-obs", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}}},
	}
	capabilitySnapshot := capability.Snapshot{
		Status: capability.StatusReady, Complete: true,
		Nodes:      []capability.Node{{ID: "n-patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "n-obs", ResourceType: "Observation"}},
		Candidates: []capability.Candidate{{ID: "candidate-status", NodeID: "n-obs", ResourceType: "Observation", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}},
	}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: importedColumn, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, wireCatalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	if len(compiled.Bundle.Outputs[0].Traversals) != 1 || len(compiled.Bundle.Outputs[0].Traversals[0].Fields) != 1 {
		t.Fatalf("additive related field was not compiled: %#v", compiled.Bundle.Outputs[0].Traversals)
	}
}
