package dataframe

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
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

func TestCompileRejectsNonLoweredBuilder(t *testing.T) {
	_, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
	}, 25)
	if err == nil || !strings.Contains(err.Error(), "unsupported dataframe query shape") {
		t.Fatalf("expected unsupported lowering error, got %v", err)
	}
}

func TestLowerGraphQLBuilderUsesGenericCompilerForComplexPatientGraph(t *testing.T) {
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
	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		t.Fatalf("expected generic graph lowering, got %v", err)
	}
	if !usesLoweredBuilder(planned) {
		t.Fatal("expected planner to return lowered builder")
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("unexpected plan hint: %#v", planned.PlanHint)
	}
	if len(planned.DerivedFields) == 0 {
		t.Fatalf("expected lowered derived fields, got %#v", planned.DerivedFields)
	}
	if containsDerivedField(planned.DerivedFields, "recipe") {
		t.Fatalf("did not expect canned recipe field in lowered builder: %#v", planned.DerivedFields)
	}
	for _, expectedSet := range []string{"generic_root_subject_Patient_neighbors_set", "generic_condition_set", "generic_specimen_set", "generic_group_set"} {
		if !containsNamedSet(planned.Sets, expectedSet) {
			t.Fatalf("expected lowered set %q, got %#v", expectedSet, planned.Sets)
		}
	}
}

func TestLowerGraphQLBuilderUsesGenericPlanForSimpleTraversal(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{
			{Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen"},
		},
	}
	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		t.Fatalf("lowerGraphQLBuilder() error = %v", err)
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("expected generic plan, got %#v", planned.PlanHint)
	}
}

func TestLowerGraphQLBuilderKeepsDocumentReferenceSelectorsInGenericFHIR(t *testing.T) {
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
	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		t.Fatalf("expected generic lowering, got %v", err)
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("expected generic plan, got %#v", planned.PlanHint)
	}
	if !containsDerivedFieldWithSelect(planned.DerivedFields, "specimen_file__data_category", `category[].coding[].display where system contains "data_category"`) {
		t.Fatalf("expected raw FHIR selector, got %#v", planned.DerivedFields)
	}
}

func TestLowerGraphQLBuilderSupportsExpandedPatientRootFamily(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		Traversals: []TraversalStep{
			{Label: "subject_Patient", ToResourceType: "Observation", Alias: "subject_observation", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
			{Label: "focus_Patient", ToResourceType: "Observation", Alias: "focus_observation", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
			{Label: "subject_Patient", ToResourceType: "ImagingStudy", Alias: "imaging_study", Fields: []FieldSelect{{Name: "status", Select: "status"}}},
			{
				Label:          "member_entity_Patient",
				ToResourceType: "Group",
				Alias:          "patient_group",
				Traversals: []TraversalStep{
					{Label: "subject_Group", ToResourceType: "DocumentReference", Alias: "group_file", Fields: []FieldSelect{{Name: "file_name", Select: "content[].attachment.title"}}},
				},
			},
		},
	}
	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		t.Fatalf("expected expanded patient-root family to lower, got %v", err)
	}
	for _, expectedSet := range []string{
		"generic_subject_observation_set",
		"generic_focus_observation_set",
		"generic_imaging_study_set",
		"generic_patient_group_set",
		"generic_group_file_set",
	} {
		if !containsNamedSet(planned.Sets, expectedSet) {
			t.Fatalf("expected lowered set %q, got %#v", expectedSet, planned.Sets)
		}
	}
}

func TestCompilePrecomputesPivotMapForDerivedPivots(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		Traversals: []TraversalStep{
			{
				Label:          "subject_Patient",
				ToResourceType: "Condition",
				Alias:          "condition",
				Aggregates:     []AggregateSelect{{Name: "condition_count", Operation: "COUNT"}},
			},
			{
				Label:          "subject_Patient",
				ToResourceType: "Observation",
				Alias:          "observation",
				Pivots: []PivotSelect{
					{
						Name:         "observation_values",
						ColumnSelect: "code.coding[].display",
						ValueSelect:  "valueQuantity.value",
						Columns:      []string{"Tumor Purity", "Adenocarcinoma"},
					},
				},
			},
		},
	}

	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		t.Fatalf("expected supported lowering, got %v", err)
	}
	compiled, err := Compile(planned, 25)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if !strings.Contains(compiled.Query, "LET __pivot_map_0 = MERGE(") {
		t.Fatalf("expected precomputed pivot map, got query:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, `"observation__observation_values": MERGE(`) {
		t.Fatalf("expected pivot output to be a sparse object column, got query:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "FILTER LENGTH(__values) > 0") {
		t.Fatalf("expected pivot query to suppress empty-value keys, got query:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "RETURN { [__key]: FIRST(__flat_values) }") {
		t.Fatalf("expected pivot query to emit scalar key/value pairs, got query:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, `FILTER HAS(__pivot_map_0, __key)`) {
		t.Fatalf("expected pivot projection to keep only requested keys that exist, got query:\n%s", compiled.Query)
	}
	if !slices.Contains(compiled.PivotFields, "observation__observation_values") {
		t.Fatalf("expected derived pivot field to be marked flattenable, got %#v", compiled.PivotFields)
	}
}

func TestLowerGraphQLBuilderPreservesUnrestrictedAuthScope(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		Traversals: []TraversalStep{
			{Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition", Aggregates: []AggregateSelect{{Name: "condition_count", Operation: "COUNT"}}},
			{Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen", Aggregates: []AggregateSelect{{Name: "specimen_count", Operation: "COUNT"}}},
		},
	}

	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		t.Fatalf("expected structural lowering to match, got %v", err)
	}
	if planned.AuthResourcePaths != nil {
		t.Fatalf("expected unrestricted auth scope to stay nil, got %#v", planned.AuthResourcePaths)
	}

	compiled, err := Compile(planned, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.BindVars["auth_resource_paths_unrestricted"]; got != true {
		t.Fatalf("expected unrestricted auth bind var to be true, got %#v", got)
	}
}

func TestServiceRunsGenericSimpleQuery(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1}}, nil
		},
		DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			if opts.PivotOnly {
				return []catalog.PopulatedField{}, nil
			}
			if opts.ResourceType == "Patient" {
				return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
			}
			return []catalog.PopulatedField{{ResourceType: "Specimen", Path: "type.coding[].display", Kind: "scalar"}}, nil
		},
		ExecuteRows: func(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"_key": "p1", "gender": "female", "specimen__specimen_type": []string{"Blood"}})
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
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
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 1 || !slices.Contains(result.Columns, "specimen__specimen_type") {
		t.Fatalf("unexpected generic result: %#v", result)
	}
}

func TestServiceRunCaseAssayRecipe(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			switch opts.NodeType {
			case "Patient":
				return []catalog.PopulatedReference{
					{FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 1},
					{FromType: "Patient", Label: "subject_Patient", ToType: "ResearchSubject", EdgeCount: 1},
					{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1},
					{FromType: "Patient", Label: "subject_Patient", ToType: "MedicationAdministration", EdgeCount: 1},
				}, nil
			case "Specimen":
				return []catalog.PopulatedReference{
					{FromType: "Specimen", Label: "subject_Specimen", ToType: "DocumentReference", EdgeCount: 1},
					{FromType: "Specimen", Label: "member_entity_Specimen", ToType: "Group", EdgeCount: 1},
				}, nil
			case "Group":
				return []catalog.PopulatedReference{
					{FromType: "Group", Label: "subject_Group", ToType: "DocumentReference", EdgeCount: 1},
				}, nil
			default:
				return []catalog.PopulatedReference{}, nil
			}
		},
		DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return []catalog.PopulatedField{
				{ResourceType: opts.ResourceType, Path: "gender", Kind: "scalar"},
				{ResourceType: opts.ResourceType, Path: "id", Kind: "scalar"},
				{ResourceType: opts.ResourceType, Path: "status", Kind: "scalar"},
			}, nil
		},
		ExecuteRows: func(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "LET generic_root_subject_Patient_neighbors_set") || !strings.Contains(query, "LET generic_condition_set") || !strings.Contains(query, "LET generic_group_set") {
				t.Fatalf("expected advanced planned query, got:\n%s", query)
			}
			if strings.Contains(query, `"recipe"`) {
				t.Fatalf("unexpected canned recipe field in lowered query:\n%s", query)
			}
			return visit(map[string]any{"_key": "p1", "gender": "female"})
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
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

func TestServiceRunsGenericRootOnlyQuery(t *testing.T) {
	svc := NewService(ServiceConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			return []catalog.PopulatedReference{}, nil
		},
		DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
		},
		ExecuteRows: func(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
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
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 1 || !slices.Contains(result.Columns, "gender") {
		t.Fatalf("unexpected generic result: %#v", result)
	}
}

func TestServiceRejectsUnauthorizedAuthPath(t *testing.T) {
	svc := NewService(ServiceConfig{ConnectionOptions: arangostore.ConnectionOptions{}})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
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
