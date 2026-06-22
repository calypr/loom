package dataframe

import (
	"context"
	"strings"
	"testing"
	"time"

	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

func TestParseSelector(t *testing.T) {
	sel, err := ParseSelector(`identifier[].value where system contains "case_id"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.CanonicalPath(); got != "identifier[].value" {
		t.Fatalf("canonical path = %q", got)
	}
	if sel.Filter == nil || sel.Filter.Field != "system" || sel.Filter.Needle != "case_id" {
		t.Fatalf("unexpected filter: %#v", sel.Filter)
	}
}

func TestCompileIncludesTraversalAndAuthScope(t *testing.T) {
	compiled, err := Compile(Builder{
		Project:           "P1",
		AuthResourcePaths: []string{"pathA"},
		RootResourceType:  "Patient",
		Fields: []FieldSelect{
			{Name: "gender", Select: "gender"},
			{Name: "case_id", Select: `identifier[].value where system contains "case_id"`},
		},
		Traversals: []TraversalStep{
			{
				Label:          "subject_Patient",
				ToResourceType: "Specimen",
				Alias:          "specimen",
				Fields: []FieldSelect{
					{Name: "specimen_type", Select: "type.coding[].display"},
				},
			},
		},
	}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "FILTER @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths") {
		t.Fatalf("compiled query missing auth filter:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "LET specimen_nodes") {
		t.Fatalf("compiled query missing traversal:\n%s", compiled.Query)
	}
	if got := compiled.BindVars["__specimen_label_1"]; got != "subject_Patient" {
		t.Fatalf("expected traversal bind var, got %#v", compiled.BindVars)
	}
	if len(compiled.Columns) != 3 {
		t.Fatalf("columns = %#v", compiled.Columns)
	}
}

func TestCompileCaseAssayRecipe(t *testing.T) {
	recipeCompiled, err := Compile(buildGDCCaseAssayMatrixRecipe("P1", []string{"pathA", "pathB"}), 25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recipeCompiled.Query, "LET patient_neighbors") {
		t.Fatalf("compiled recipe missing case-assay sets:\n%s", recipeCompiled.Query)
	}
	if !strings.Contains(recipeCompiled.Query, "LIMIT @preview_limit") {
		t.Fatalf("compiled recipe missing preview limit:\n%s", recipeCompiled.Query)
	}
	if got := recipeCompiled.BindVars["auth_resource_paths"]; len(got.([]string)) != 2 {
		t.Fatalf("unexpected auth path bind vars: %#v", recipeCompiled.BindVars)
	}
	if len(recipeCompiled.Columns) < 40 {
		t.Fatalf("expected case-assay columns, got %d", len(recipeCompiled.Columns))
	}
}

func TestCompileAdvancedIncludesNamedSetsAndAggregates(t *testing.T) {
	compiled, err := Compile(buildGDCCaseAssayMatrixRecipe("P1", []string{"pathA"}), 25)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"LET patient_neighbors",
		"LET file_summaries",
		`"file_count": LENGTH(file_summaries)`,
		`"representative_snv_files": SLICE`,
		`"study_id": FIRST(`,
	} {
		if !strings.Contains(compiled.Query, needle) {
			t.Fatalf("compiled query missing %q:\n%s", needle, compiled.Query)
		}
	}
}

func TestCaseAssayRecipeNoLongerUsesRawExpr(t *testing.T) {
	recipe := buildGDCCaseAssayMatrixRecipe("P1", []string{"pathA"})
	for _, field := range recipe.DerivedFields {
		if field.Operation == DerivedOpRawExpr {
			t.Fatalf("unexpected raw expr field still present: %+v", field)
		}
	}
}

func TestServiceRunPreview(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			return []proto.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1}}, nil
		},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			if opts.PivotOnly {
				return []proto.PopulatedField{}, nil
			}
			if opts.ResourceType == "Patient" {
				return []proto.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
			}
			return []proto.PopulatedField{{ResourceType: "Specimen", Path: "type.coding[].display", Kind: "scalar"}}, nil
		},
		ExecuteRows: func(ctx context.Context, opts proto.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"_key": "p1", "gender": "female", "specimen__specimen_type": []string{"Blood"}})
		},
	})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})
	result, err := svc.Run(ctx, RunRequest{
		Mode: ModePreview,
		Builder: Builder{
			Project:           "P1",
			RootResourceType:  "Patient",
			AuthResourcePaths: []string{"pathA"},
			Fields:            []FieldSelect{{Name: "gender", Select: "gender"}},
			Traversals: []TraversalStep{{
				Label:          "subject_Patient",
				ToResourceType: "Specimen",
				Alias:          "specimen",
				Fields:         []FieldSelect{{Name: "specimen_type", Select: "type.coding[].display"}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview == nil || result.Preview.RowCount != 1 || len(result.Preview.Rows) != 1 {
		t.Fatalf("unexpected preview result: %#v", result)
	}
}

func TestServiceRunPreviewCaseAssayRecipe(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		ExecuteRows: func(ctx context.Context, opts proto.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "patient_neighbors") || !strings.Contains(query, "file_summaries") {
				t.Fatalf("expected case-assay recipe query, got:\n%s", query)
			}
			return visit(map[string]any{
				"_key":                    "p1",
				"project":                 "P1",
				"recipe":                  "gdc_case_assay_matrix",
				"case_id":                 "CASE-1",
				"specimen_count":          2,
				"data_categories":         []string{"Clinical"},
				"experimental_strategies": []string{"WXS"},
			})
		},
	})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})
	result, err := svc.Run(ctx, RunRequest{
		Mode:    ModePreview,
		Builder: buildGDCCaseAssayMatrixRecipe("P1", []string{"pathA"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview == nil || result.Preview.RowCount != 1 {
		t.Fatalf("unexpected preview result: %#v", result)
	}
	if len(result.Preview.Columns) == 0 || result.Preview.Columns[0] != "_key" {
		t.Fatalf("unexpected recipe columns: %#v", result.Preview.Columns)
	}
}

func TestServiceRunExportReturnsHandle(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			return []proto.PopulatedReference{}, nil
		},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			return []proto.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
		},
		ExecuteRows: func(ctx context.Context, opts proto.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"_key": "p1", "gender": "female"})
		},
	})
	result, err := svc.Run(context.Background(), RunRequest{
		Mode: ModeExport,
		Builder: Builder{
			Project:          "P1",
			RootResourceType: "Patient",
			Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Export == nil || result.Export.ExportID == "" {
		t.Fatalf("unexpected export handle: %#v", result)
	}
	time.Sleep(50 * time.Millisecond)
}

func TestServiceRejectsUnauthorizedAuthPath(t *testing.T) {
	svc := NewService(ServiceConfig{ConnectionOptions: proto.ConnectionOptions{Backend: "arango"}})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})
	_, err := svc.Run(ctx, RunRequest{
		Mode: ModePreview,
		Builder: Builder{
			Project:           "P1",
			RootResourceType:  "Patient",
			AuthResourcePaths: []string{"pathB"},
			Fields:            []FieldSelect{{Name: "gender", Select: "gender"}},
		},
	})
	if err == nil {
		t.Fatal("expected auth path error")
	}
}
