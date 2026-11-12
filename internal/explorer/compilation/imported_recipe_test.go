package compilation

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

func TestImportedRecipeCompilationPreservesSourceOutputAndUsesStructuralSchema(t *testing.T) {
	output := recipe.Output{Name: "GroupMember", RootResourceType: "Group", RowGrain: "GROUP_MEMBER", Expand: &recipe.Expansion{From: recipe.Expression{Select: "root.member[]"}, As: "member"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Call: "uuid5", Args: []recipe.Expression{{Literal: json.RawMessage(`"stable namespace"`)}, {Select: "root.id"}, {Select: "member.entity.reference"}}}}, Fields: []recipe.Field{{Name: "member_id", Expr: recipe.Expression{Call: "reference_id", Args: []recipe.Expression{{Select: "member.entity.reference"}}}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := authoringv2.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	document := authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: output.Name, Title: "Group members"}, RootResourceType: output.RootResourceType, Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: output.RootResourceType}, Rows: recipeimport.ImportRows(output), ImportedRecipe: &ref, Columns: []authoringv2.Column{{ColumnID: "member_id_source", Column: "member_id", Label: "Member", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: "member_id"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}}}
	resolvedOutput := output
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Outputs: []recipe.Output{resolvedOutput}}, SourceColumns: []ResolvedImportedSourceColumn{{Column: recipe.StageColumn{ID: "member_id_source", Name: "member_id", Type: "string"}, Cardinality: "required_one", Shape: "scalar", SemanticPath: "member.entity.reference"}}}
	compiled, err := compileImportedDocument(document, authoringv2.CatalogSnapshot{}, resolved)
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
	ref := authoringv2.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	workspace := authoringv2.Workspace{Documents: []authoringv2.Document{{ImportedRecipe: &ref}}}
	input := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle}
	if err := validateResolvedImportedRecipeCoverage(workspace, authoringv2.CatalogSnapshot{}, ResolvedInputs{ImportedRecipes: []ResolvedImportedRecipe{input}}); err != nil {
		t.Fatalf("valid pin rejected: %v", err)
	}
	input.Bundle.Outputs[0].RowGrain = "CHANGED"
	if err := validateResolvedImportedRecipeCoverage(workspace, authoringv2.CatalogSnapshot{}, ResolvedInputs{ImportedRecipes: []ResolvedImportedRecipe{input}}); err == nil {
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
	ref := authoringv2.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	column := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	minimum := int64(18)
	document := authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType, Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: output.RootResourceType}, Rows: authoringv2.RecordsRowDefinition(), ImportedRecipe: &ref, Columns: []authoringv2.Column{{ColumnID: column.ID, Column: column.Name, Label: column.Label, OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: column.Name}}, Table: &authoringv2.TablePresentation{Visible: &visible}}}, Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{ID: "adults", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: column.ID, Operator: authoringv2.ConstructionFilterGreaterEq, Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterInteger, Integer: &minimum}}}}, Outputs: []authoringv2.StageColumn{{ID: column.ID, Name: column.Name, Label: column.Label, Type: "integer", Table: &authoringv2.TablePresentation{Visible: &visible}}}}}}}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: column, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(document, authoringv2.CatalogSnapshot{}, resolved)
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
