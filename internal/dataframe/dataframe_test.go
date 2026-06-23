package dataframe

import (
	"context"
	"strings"
	"testing"

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

func TestCompileSupportsFallbackAggregatesAndSlices(t *testing.T) {
	compiled, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields: []FieldSelect{
			{Name: "case_id", Select: `identifier[].value where system contains "case_id"`, FallbackSelects: []string{`identifier[].value where system contains "submitter_id"`}},
		},
		Traversals: []TraversalStep{
			{
				Label:          "subject_Patient",
				ToResourceType: "Specimen",
				Alias:          "specimen",
				Aggregates: []AggregateSelect{
					{Name: "specimen_count", Operation: "COUNT"},
					{Name: "specimen_types", Operation: "DISTINCT_VALUES", Select: "type.coding[].display"},
					{Name: "has_primary_tumor", Operation: "EXISTS", PredicatePath: "type.coding[].display", PredicateEquals: "Primary Tumor"},
				},
				Slices: []RepresentativeSlice{
					{
						Name:            "sample_slice",
						Limit:           2,
						PredicatePath:   "type.coding[].display",
						PredicateEquals: "Primary Tumor",
						Fields: []FieldSelect{
							{Name: "type_display", Select: "type.coding[].display"},
							{Name: "id", Select: "id"},
						},
					},
				},
			},
		},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`"case_id": FIRST(`,
		`"specimen__specimen_count": LENGTH(specimen_nodes)`,
		`"specimen__specimen_types": UNIQUE(FLATTEN(`,
		`"specimen__has_primary_tumor": LENGTH(FOR __item IN specimen_nodes FILTER`,
		`"specimen__sample_slice": SLICE(FOR __item IN specimen_nodes FILTER`,
	} {
		if !strings.Contains(compiled.Query, needle) {
			t.Fatalf("compiled query missing %q:\n%s", needle, compiled.Query)
		}
	}
}

func TestCompileCaseAssayRecipe(t *testing.T) {
	recipeCompiled, err := Compile(buildGDCCaseAssayMatrixAdvancedBuilder("P1", []string{"pathA", "pathB"}), 25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recipeCompiled.Query, "LET patient_neighbors") {
		t.Fatalf("compiled recipe missing case-assay sets:\n%s", recipeCompiled.Query)
	}
	if !strings.Contains(recipeCompiled.Query, "LIMIT @limit") {
		t.Fatalf("compiled recipe missing row limit:\n%s", recipeCompiled.Query)
	}
	if got := recipeCompiled.BindVars["auth_resource_paths"]; len(got.([]string)) != 2 {
		t.Fatalf("unexpected auth path bind vars: %#v", recipeCompiled.BindVars)
	}
	if len(recipeCompiled.Columns) < 40 {
		t.Fatalf("expected case-assay columns, got %d", len(recipeCompiled.Columns))
	}
}

func TestCompileAdvancedIncludesNamedSetsAndAggregates(t *testing.T) {
	compiled, err := Compile(buildGDCCaseAssayMatrixAdvancedBuilder("P1", []string{"pathA"}), 25)
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
	recipe := buildGDCCaseAssayMatrixAdvancedBuilder("P1", []string{"pathA"})
	for _, field := range recipe.DerivedFields {
		if field.Operation == DerivedOpRawExpr {
			t.Fatalf("unexpected raw expr field still present: %+v", field)
		}
	}
}

func TestLowerGraphQLBuilderUsesStructuralLowering(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		Traversals: []TraversalStep{
			{Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition", Fields: []FieldSelect{{Name: "id", Select: "id"}}},
			{Label: "subject_Patient", ToResourceType: "ResearchSubject", Alias: "research_subject", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
			{
				Label:          "subject_Patient",
				ToResourceType: "Specimen",
				Alias:          "specimen",
				Fields:         []FieldSelect{{Name: "type_display", Select: "type.coding[].display"}},
				Traversals: []TraversalStep{
					{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "specimen_file", Fields: []FieldSelect{{Name: "file_name", Select: "content[].attachment.title"}}},
					{
						Label:          "member_entity_Specimen",
						ToResourceType: "Group",
						Alias:          "group",
						Traversals: []TraversalStep{
							{Label: "subject_Group", ToResourceType: "DocumentReference", Alias: "group_file", Fields: []FieldSelect{{Name: "id", Select: "id"}}},
						},
					},
				},
			},
			{Label: "subject_Patient", ToResourceType: "MedicationAdministration", Alias: "treatment", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
		},
	}
	planned, matched := lowerGraphQLBuilder(builder)
	if !matched {
		t.Fatal("expected case-assay profile to match")
	}
	if !usesAdvancedBuilder(planned) {
		t.Fatal("expected planner to return advanced builder")
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "patient_case_assay_family" {
		t.Fatalf("unexpected plan hint: %#v", planned.PlanHint)
	}
	if len(planned.DerivedFields) == 0 {
		t.Fatalf("expected lowered derived fields, got %#v", planned.DerivedFields)
	}
	if containsDerivedField(planned.DerivedFields, "recipe") {
		t.Fatalf("did not expect canned recipe field in lowered builder: %#v", planned.DerivedFields)
	}
	for _, expectedSet := range []string{"root_patient_neighbor_set", "patient_condition_set", "patient_specimen_set", "specimen_group_set"} {
		if !containsNamedSet(planned.Sets, expectedSet) {
			t.Fatalf("expected lowered set %q, got %#v", expectedSet, planned.Sets)
		}
	}
}

func TestLowerGraphQLBuilderFallsBackForSimpleTraversal(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{
			{Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen"},
		},
	}
	planned, matched := lowerGraphQLBuilder(builder)
	if matched {
		t.Fatal("did not expect simple traversal request to match case-assay profile")
	}
	if usesAdvancedBuilder(planned) {
		t.Fatal("expected generic fallback builder to remain non-advanced")
	}
	if planned.PlanHint == nil || planned.PlanHint.Mode != "generic_traversal" {
		t.Fatalf("unexpected fallback plan hint: %#v", planned.PlanHint)
	}
}

func TestLowerGraphQLBuilderMapsSingleDocumentReferenceSelectorToSummary(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{
			{
				Label:          "subject_Patient",
				ToResourceType: "Specimen",
				Alias:          "specimen",
				Traversals: []TraversalStep{
					{
						Label:          "subject_Specimen",
						ToResourceType: "DocumentReference",
						Alias:          "specimen_file",
						Fields: []FieldSelect{
							{Name: "data_category", Select: `category[].coding[].display where system contains "data_category"`},
						},
					},
				},
			},
		},
	}
	planned, matched := lowerGraphQLBuilder(builder)
	if !matched {
		t.Fatal("expected supported structural lowering")
	}
	if !containsNamedSet(planned.Sets, "document_reference_summary_set") {
		t.Fatalf("expected summary set, got %#v", planned.Sets)
	}
	if !containsDerivedFieldWithSelect(planned.DerivedFields, "specimen_file__data_category", "data_category") {
		t.Fatalf("expected summary-backed derived field, got %#v", planned.DerivedFields)
	}
}

func TestServiceRunQuery(t *testing.T) {
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
	if result == nil || result.RowCount != 1 || len(result.Rows) != 1 {
		t.Fatalf("unexpected dataframe result: %#v", result)
	}
}

func TestServiceRunCaseAssayRecipe(t *testing.T) {
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
		Builder: buildGDCCaseAssayMatrixAdvancedBuilder("P1", []string{"pathA"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.RowCount != 1 {
		t.Fatalf("unexpected dataframe result: %#v", result)
	}
	if len(result.Columns) == 0 || result.Columns[0] != "_key" {
		t.Fatalf("unexpected recipe columns: %#v", result.Columns)
	}
}

func TestServiceRunPromotesGraphQLCaseAssayShape(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			switch opts.NodeType {
			case "Patient":
				return []proto.PopulatedReference{
					{FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 1},
					{FromType: "Patient", Label: "subject_Patient", ToType: "ResearchSubject", EdgeCount: 1},
					{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1},
					{FromType: "Patient", Label: "subject_Patient", ToType: "MedicationAdministration", EdgeCount: 1},
				}, nil
			case "Specimen":
				return []proto.PopulatedReference{
					{FromType: "Specimen", Label: "subject_Specimen", ToType: "DocumentReference", EdgeCount: 1},
					{FromType: "Specimen", Label: "member_entity_Specimen", ToType: "Group", EdgeCount: 1},
				}, nil
			case "Group":
				return []proto.PopulatedReference{
					{FromType: "Group", Label: "subject_Group", ToType: "DocumentReference", EdgeCount: 1},
				}, nil
			default:
				return []proto.PopulatedReference{}, nil
			}
		},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			return []proto.PopulatedField{
				{ResourceType: opts.ResourceType, Path: "gender", Kind: "scalar"},
				{ResourceType: opts.ResourceType, Path: "id", Kind: "scalar"},
				{ResourceType: opts.ResourceType, Path: "status", Kind: "scalar"},
			}, nil
		},
		ExecuteRows: func(ctx context.Context, opts proto.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "LET root_patient_neighbor_set") || !strings.Contains(query, "LET patient_condition_set") || !strings.Contains(query, "LET specimen_group_set") {
				t.Fatalf("expected advanced planned query, got:\n%s", query)
			}
			if strings.Contains(query, `"recipe"`) {
				t.Fatalf("unexpected canned recipe field in lowered query:\n%s", query)
			}
			return visit(map[string]any{"_key": "p1", "gender": "female"})
		},
	})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})
	_, err := svc.Run(ctx, RunRequest{
		Builder: Builder{
			Project:          "P1",
			RootResourceType: "Patient",
			Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
			Traversals: []TraversalStep{
				{Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition", Fields: []FieldSelect{{Name: "id", Select: "id"}}},
				{Label: "subject_Patient", ToResourceType: "ResearchSubject", Alias: "research_subject", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
				{
					Label:          "subject_Patient",
					ToResourceType: "Specimen",
					Alias:          "specimen",
					Traversals: []TraversalStep{
						{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "specimen_file", Fields: []FieldSelect{{Name: "id", Select: "id"}}},
						{
							Label:          "member_entity_Specimen",
							ToResourceType: "Group",
							Alias:          "group",
							Traversals: []TraversalStep{
								{Label: "subject_Group", ToResourceType: "DocumentReference", Alias: "group_file", Fields: []FieldSelect{{Name: "id", Select: "id"}}},
							},
						},
					},
				},
				{Label: "subject_Patient", ToResourceType: "MedicationAdministration", Alias: "treatment", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsNamedSet(sets []NamedSet, want string) bool {
	for _, set := range sets {
		if set.Name == want {
			return true
		}
	}
	return false
}

func containsDerivedField(fields []DerivedField, want string) bool {
	for _, field := range fields {
		if field.Name == want {
			return true
		}
	}
	return false
}

func containsDerivedFieldWithSelect(fields []DerivedField, wantName, wantSelect string) bool {
	for _, field := range fields {
		if field.Name == wantName && field.Select == wantSelect {
			return true
		}
	}
	return false
}

func TestServiceRunReturnsRows(t *testing.T) {
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
		Builder: Builder{
			Project:          "P1",
			RootResourceType: "Patient",
			Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.RowCount != 1 || len(result.Rows) != 1 {
		t.Fatalf("unexpected dataframe result: %#v", result)
	}
}

func TestServiceRejectsUnauthorizedAuthPath(t *testing.T) {
	svc := NewService(ServiceConfig{ConnectionOptions: proto.ConnectionOptions{Backend: "arango"}})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})
	_, err := svc.Run(ctx, RunRequest{
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
