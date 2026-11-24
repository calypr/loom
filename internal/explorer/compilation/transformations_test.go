package compilation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"github.com/calypr/loom/internal/dataframe/columntransform"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestExactCategoryRecodeCompilesLowersAndRendersWithoutChangingShape(t *testing.T) {
	transformation := columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings:      []columntransform.CategoryMapping{{From: "recorded-A", To: "group-1"}},
			UnknownPolicy: columntransform.UnknownError,
		},
	}
	document := exactRecodeDocument(&transformation)
	snapshot := fixtureSnapshotForProject("project")
	compiled, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 || len(compiled.Bundle.Outputs[0].ColumnTransformations) != 1 {
		t.Fatalf("compiled column transformations = %#v", compiled.Bundle.Outputs)
	}
	if got := compiled.Bundle.Outputs[0].ColumnTransformations[0]; got.Column != "status" || !reflect.DeepEqual(got.Transformation, transformation) {
		t.Fatalf("compiled transformation = %#v", got)
	}

	base := exactRecodeDocument(nil)
	baseCompiled, err := Compile(context.Background(), "project", "explorer", base, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiled.EmittedColumns, baseCompiled.EmittedColumns) || !reflect.DeepEqual(compiled.OutputContract, baseCompiled.OutputContract) {
		t.Fatalf("recode changed the output contract: transformed=%#v plain=%#v", compiled.OutputContract, baseCompiled.OutputContract)
	}

	physical := compileRecipePhysicalPlan(t, compiled.Bundle)
	plainPhysical := compileRecipePhysicalPlan(t, baseCompiled.Bundle)
	transformedProjection := returnProjection(t, physical, "status")
	if transformedProjection.Expression == nil || transformedProjection.Expression.Kind != facade.PhysicalCallExpression || transformedProjection.Expression.Call == nil || transformedProjection.Expression.Call.Name != "case" {
		t.Fatalf("status projection = %#v, want typed exact-recode expression", transformedProjection)
	}
	if !reflect.DeepEqual(returnProjection(t, physical, "untouched"), returnProjection(t, plainPhysical, "untouched")) {
		t.Fatalf("recode changed another projection: transformed=%#v plain=%#v", returnProjection(t, physical, "untouched"), returnProjection(t, plainPhysical, "untouched"))
	}
	if !reflect.DeepEqual(returnProjection(t, physical, "_key"), returnProjection(t, plainPhysical, "_key")) {
		t.Fatal("recode changed the backend row identity projection")
	}
	if len(physical.Operations) != len(plainPhysical.Operations) {
		t.Fatalf("recode changed row-plan operation count: transformed=%d plain=%d", len(physical.Operations), len(plainPhysical.Operations))
	}
	for _, operation := range physical.Operations {
		if operation.Kind == facade.PhysicalUnnestOp {
			t.Fatal("scalar column recoding changed row membership with UNNEST")
		}
	}

	rendered, err := facade.Render(physical, facade.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "ASSERT(@category_recode_value_") {
		t.Fatalf("ERROR unknown-value policy did not render an assertion: %s", rendered.Query)
	}
	if !bindContains(rendered.BindVars, "recorded-A") || !bindContains(rendered.BindVars, "group-1") || !bindContains(rendered.BindVars, "CATEGORY_RECODE_UNKNOWN_VALUE") || !bindContains(rendered.BindVars, false) || !bindContains(rendered.BindVars, nil) {
		t.Fatalf("rendered exact mapping/policy literals are missing: %#v", rendered.BindVars)
	}

	transformation.ExactCategoryRecode.UnknownPolicy = columntransform.UnknownKeepOriginal
	keepCompiled, err := Compile(context.Background(), "project", "explorer", exactRecodeDocument(&transformation), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	keepRendered, err := facade.Render(compileRecipePhysicalPlan(t, keepCompiled.Bundle), facade.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(keepRendered.Query, "ASSERT(@category_recode_value_") {
		t.Fatalf("KEEP_ORIGINAL policy rendered an error assertion: %s", keepRendered.Query)
	}
}

func TestCodedValueRecodeCompilationExplainsLossOfSystemIdentity(t *testing.T) {
	document := exactRecodeDocument(nil)
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	document.Columns[0].OccurrenceID = ws.RootOccurrenceID
	document.Columns[0].Source = ws.ColumnSource{Kind: ws.SourceCodedValue, Lookup: &ws.LookupSource{
		Binding: &fhirbinding.CorrelatedBinding{
			OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code",
			ValuePath: "valueString", LogicalType: "string",
		},
		Key: &fhirbinding.CorrelatedKey{System: "urn:study", Code: "active"},
	}}
	transform := columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings: []columntransform.CategoryMapping{{From: "active", To: "eligible"}}, UnknownPolicy: columntransform.UnknownError,
		},
	}
	document.Columns[0].ValueTransformation = &transform
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "observation"})
	_, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	var compileError *Error
	if err == nil || !strings.Contains(err.Error(), "CODED_VALUE_RECODE_UNAVAILABLE") || !errors.As(err, &compileError) {
		t.Fatalf("coded-value compile error = %v, want CODED_VALUE_RECODE_UNAVAILABLE diagnostic", err)
	}
	if compileError.Code != "CODED_VALUE_RECODE_UNAVAILABLE" || !strings.Contains(compileError.Message, "Coding.system and Coding.code") {
		t.Fatalf("coded-value diagnostic = %#v", compileError)
	}
}

func TestCompileRejectsUnsupportedTransformationTypeAndShape(t *testing.T) {
	transform := columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings: []columntransform.CategoryMapping{{From: "recorded", To: "replacement"}}, UnknownPolicy: columntransform.UnknownError,
		},
	}
	t.Run("numeric input", func(t *testing.T) {
		snapshot := fixtureSnapshotForProject("project")
		snapshot.Candidates[0].LogicalType = "integer"
		document := exactRecodeDocument(&transform)
		document.Columns = document.Columns[:1]
		document.Columns[0].LogicalType = "integer"
		_, err := Compile(context.Background(), "project", "explorer", document, snapshot)
		var compileError *Error
		if !errors.As(err, &compileError) || compileError.Code != "UNSUPPORTED_VALUE_TRANSFORMATION_TYPE" {
			t.Fatalf("numeric recoding error = %v, want UNSUPPORTED_VALUE_TRANSFORMATION_TYPE", err)
		}
	})

	t.Run("array output", func(t *testing.T) {
		document := exactRecodeDocument(&transform)
		document.Columns = document.Columns[:1]
		document.Columns[0].Source.Field.Path = "name[].given[]"
		document.Columns[0].Source.Field.ProjectionMode = "ALL"
		_, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
		var compileError *Error
		if !errors.As(err, &compileError) || compileError.Code != "UNSUPPORTED_VALUE_TRANSFORMATION_SHAPE" {
			t.Fatalf("array recoding error = %v, want UNSUPPORTED_VALUE_TRANSFORMATION_SHAPE", err)
		}
	})

	t.Run("indexed expansion", func(t *testing.T) {
		document := exactRecodeDocument(&transform)
		document.Columns = document.Columns[:1]
		document.Columns[0].Source.Field.Path = "name[].given[]"
		document.Columns[0].Source.Field.ProjectionMode = "INDEXED"
		_, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
		var compileError *Error
		if !errors.As(err, &compileError) || compileError.Code != "UNSUPPORTED_VALUE_TRANSFORMATION_SHAPE" {
			t.Fatalf("indexed recoding error = %v, want UNSUPPORTED_VALUE_TRANSFORMATION_SHAPE", err)
		}
	})
}

func exactRecodeDocument(transformation *columntransform.ValueTransformation) ws.Document {
	visible := true
	return ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Rows: ws.RecordsRowDefinition(),
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []ws.Column{
			{Column: "status", Label: "Status", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
				Source:              ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				ValueTransformation: transformation, Table: &ws.TablePresentation{Visible: &visible}},
			{Column: "untouched", Label: "Untouched", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceProjectID},
				Table:  &ws.TablePresentation{Visible: &visible}},
		},
	}
}

func compileRecipePhysicalPlan(t *testing.T, bundle recipe.Bundle) facade.PhysicalPlan {
	t.Helper()
	plan, err := recipeplan.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0].Plan
}

func returnProjection(t *testing.T, plan facade.PhysicalPlan, name string) facade.PhysicalProjection {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.Kind != facade.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == name {
				return projection
			}
		}
	}
	t.Fatalf("physical RETURN has no %q projection", name)
	return facade.PhysicalProjection{}
}

func bindContains(values map[string]any, want any) bool {
	for _, value := range values {
		if reflect.DeepEqual(value, want) {
			return true
		}
	}
	return false
}
