package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionCodedGroupCompilesNestedAndDirectGeneratedCodingPaths(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		path         string
		pathFields   []string
	}{
		{
			name:         "BodyStructure nested coding",
			resourceType: "BodyStructure",
			path:         "includedStructure[].structure.coding[]",
			pathFields:   []string{"includedStructure", "structure", "coding"},
		},
		{
			name:         "Observation component coding",
			resourceType: "Observation",
			path:         "component[].code.coding[]",
			pathFields:   []string{"component", "code", "coding"},
		},
		{
			name:         "Specimen direct type coding",
			resourceType: "Specimen",
			path:         "type.coding[]",
			pathFields:   []string{"type", "coding"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled := compileDerivedTestOutput(t, codedGroupOutput(test.resourceType, test.path, recipe.ConstructionGroupMissingKeyGroup))
			sequence := compiled.Plan.StageSequence
			if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Kind != ir.PhysicalStageCodedGroupOp {
				t.Fatalf("stage sequence = %#v, want one CODED_GROUP stage", sequence)
			}
			stage := sequence.Stages[0]
			if stage.CodedGroup == nil || stage.CodedGroup.SourceIdentityColumn != "_key" || !stage.CodedGroup.SourceRowsUnique || stage.CodedGroup.CodingPath != test.path {
				t.Fatalf("coded group payload = %#v, want direct root path %q", stage.CodedGroup, test.path)
			}
			if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"code_system", "code_version", "code", "source_records", "__loom_row_id"}; !equalStringSlices(got, want) {
				t.Fatalf("compiled output schema = %#v, want %#v", got, want)
			}
			if compiled.OutputSchema[0].Cardinality != string(expression.OptionalOne) || !compiled.OutputSchema[0].Nullable ||
				compiled.OutputSchema[3].Kind != string(expression.KindInteger) || compiled.OutputSchema[3].Nullable {
				t.Fatalf("coded group output types = %#v, want nullable string keys and required integer count", compiled.OutputSchema)
			}

			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{
				"DOCUMENT(@@root_collection,",
				".payload[",
				"IS_ARRAY(",
				"construction_coded_system",
				"construction_coded_version",
				"construction_coded_code",
				"AGGREGATE " + stage.CodedGroup.CountVariable + " = SUM(1)",
				"CODED_GROUP_SOURCE_ID_MISSING",
				"TO_STRING([\"construction\"",
			} {
				if !strings.Contains(rendered.Query, expected) {
					t.Errorf("AQL is missing %q:\n%s", expected, rendered.Query)
				}
			}
			if strings.Contains(rendered.Query, "display") || strings.Contains(rendered.Query, "ordinal") {
				t.Fatalf("coding display or ordinal must not enter grouping or row identity:\n%s", rendered.Query)
			}
			for _, field := range test.pathFields {
				if !strings.Contains(rendered.Query, "[\""+field+"\"]") {
					t.Errorf("nested path %q was not rendered from generated field %q:\n%s", test.path, field, rendered.Query)
				}
			}
		})
	}
}

func TestConstructionCodedGroupMissingPoliciesAndTupleDeduplication(t *testing.T) {
	policies := []struct {
		name     string
		policy   recipe.ConstructionGroupMissingKeyPolicy
		contains string
	}{
		{name: "group", policy: recipe.ConstructionGroupMissingKeyGroup},
		{name: "exclude", policy: recipe.ConstructionGroupMissingKeyExclude, contains: "FILTER __loom_physical_construction_construction_coded_group_valid"},
		{name: "error", policy: recipe.ConstructionGroupMissingKeyError, contains: "CONSTRUCTION_CODED_GROUP_MISSING_KEY"},
	}
	for _, test := range policies {
		t.Run(test.name, func(t *testing.T) {
			compiled := compileDerivedTestOutput(t, codedGroupOutput("Specimen", "type.coding[]", test.policy))
			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rendered.Query, test.contains) {
				t.Fatalf("%s policy is missing %q:\n%s", test.policy, test.contains, rendered.Query)
			}
			coded := compiled.Plan.StageSequence.Stages[0].CodedGroup
			if test.policy == recipe.ConstructionGroupMissingKeyGroup &&
				!strings.Contains(rendered.Query, ": null)") {
				t.Fatalf("GROUP must emit a null tuple for a Coding missing system or code:\n%s", rendered.Query)
			}
			if strings.Contains(rendered.Query, "SORTED_UNIQUE(") || strings.Contains(rendered.Query, " INTO ") {
				t.Fatalf("CODED_GROUP must not retain contributor IDs after per-source tuple deduplication:\n%s", rendered.Query)
			}
			if strings.Count(rendered.Query, "COLLECT ") < 2 {
				t.Fatalf("CODED_GROUP must deduplicate tuples within each source before global aggregation:\n%s", rendered.Query)
			}
			if !strings.Contains(rendered.Query, "COLLECT "+coded.SystemVariable+" =") ||
				!strings.Contains(rendered.Query, coded.VersionVariable+" =") ||
				!strings.Contains(rendered.Query, coded.CodeVariable+" =") {
				t.Fatalf("group identity must preserve code+system+version as a correlated tuple:\n%s", rendered.Query)
			}
		})
	}
}

func TestConstructionCodedGroupRejectsCardinalityChangingRootSource(t *testing.T) {
	compiled := compileDerivedTestOutput(t, codedGroupOutput("Specimen", "type.coding[]", recipe.ConstructionGroupMissingKeyGroup))
	operations := append([]ir.PhysicalOperation(nil), compiled.Plan.Operations...)
	returnIndex := len(operations) - 1
	operations = append(operations, ir.PhysicalOperation{})
	copy(operations[returnIndex+1:], operations[returnIndex:])
	operations[returnIndex] = ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp}
	plan := compiled.Plan
	plan.Operations = operations
	if err := constructionCodedGroupSourceRowsUnique(&plan); err == nil || !strings.Contains(err.Error(), "cardinality-changing operation") {
		t.Fatalf("cardinality-changing root source error = %v, want precise unique-root rejection", err)
	}
}

func TestConstructionCodedGroupOutputFeedsLaterConstructionStage(t *testing.T) {
	output := codedGroupOutput("Specimen", "type.coding[]", recipe.ConstructionGroupMissingKeyGroup)
	groupOutputs := append([]recipe.StageColumn(nil), output.Construction.Steps[0].Outputs...)
	output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
		ID: "filter_coded_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group_codes"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "code_id", Operator: recipe.FilterExists,
		}},
		Outputs: groupOutputs,
	})
	compiled := compileDerivedTestOutput(t, output)
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 2 || sequence.Stages[0].Kind != ir.PhysicalStageCodedGroupOp ||
		sequence.Stages[1].Kind != ir.PhysicalStageFilterOp || sequence.Stages[1].InputStageID != "group_codes" {
		t.Fatalf("CODED_GROUP plus later Filter stages = %#v", sequence)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatalf("render later construction stage: %v", err)
	}
	if !strings.Contains(rendered.Query, "__loom_construction_stage_1") || !strings.Contains(rendered.Query, "FILTER") {
		t.Fatalf("later stage did not consume grouped rows:\n%s", rendered.Query)
	}
}

func codedGroupOutput(resourceType, path string, policy recipe.ConstructionGroupMissingKeyPolicy) recipe.Output {
	return recipe.Output{
		Name: "construction_coded_group", RootResourceType: resourceType, RowGrain: "resource",
		Fields: []recipe.Field{{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "resource_id", Name: "resource_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{
					Kind: recipe.ConstructionCodedGroupOp,
					CodedGroup: &recipe.ConstructionCodedGroup{
						ConstructionID: "group_codes",
						Source: recipe.ConstructionCodedGroupSource{
							OccurrenceID: "base", ResourceType: resourceType, CodingPath: path,
							FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY",
							Route: []recipe.ConstructionRelatedRouteStep{},
						},
						MissingKeyPolicy:     policy,
						SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
						CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
					},
				},
				Outputs: []recipe.StageColumn{
					{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
					{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
					{ID: "code_id", Name: "code", Type: "string", Nullable: true},
					{ID: "count_id", Name: "source_records", Type: "integer"},
				},
			}},
		},
	}
}
