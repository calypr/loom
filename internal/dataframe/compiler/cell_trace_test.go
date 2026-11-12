package compiler

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCompileCellTraceUsesFinalValueAndStableIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "patient_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"root.payload.id", "@cell_trace_offset", "@cell_trace_limit", "[@project, root._key]"} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("trace query missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	for _, field := range []string{ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField, ir.PhysicalCellTraceStatusField, ir.PhysicalCellTraceHasMoreField} {
		if !containsBindValue(testAQLBinds(t, compiled.Command), field) {
			t.Fatalf("trace field %q missing from binds %#v", field, testAQLBinds(t, compiled.Command))
		}
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("trace identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
}

func TestCompileCellTraceForSourceOnlyGroupRowsUsesFinalMemberValueAndObjectIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "GroupedSpecimens", RootResourceType: "Specimen", RowGrain: "groups",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "resourceType", ColumnID: "resource_type", Expr: recipe.Expression{Select: "root.resourceType"}}},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "resource_type", Policy: recipe.ConstructionRowValueOne}},
		},
	})
	if output.Plan.StageSequence != nil || len(output.Plan.Operations) != 1 || output.Plan.Operations[0].GroupRows == nil {
		t.Fatalf("source-only explicit cohort was not compiled as the ordinary GROUP_ROWS terminal: %#v", output.Plan)
	}
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "resourceType", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile CellTrace for ordinary group-row member field: %v", err)
	}
	if compiled.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || !compiled.ExplicitIdentityObject {
		t.Fatalf("group-row trace identity = %#v, want the compiler-proven Preview object identity", compiled)
	}
	if output.Plan.Operations[0].GroupRows.CellTrace != nil {
		t.Fatal("compiling a diagnostic terminal mutated the canonical Preview plan")
	}
	for _, want := range []string{
		"ASSERT(revision != null AND revision.state == \"COMPLETE\"",
		"revision.project == @project",
		"revision.generation == @dataset_generation",
		"source.auth_resource_path IN @auth_resource_paths",
		"cohort_member.payload.resourceType",
		".resourceType,",
		"__loom_row_id",
		`== null ? "RECORDED_NULL" : "VALUE"`,
		"__loom_trace_source_identity",
		"__loom_trace_source_identity.id, value:",
		"UNIQUE(",
	} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Errorf("typed GROUP_ROWS CellTrace query is missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if strings.Contains(testAQLText(t, compiled.Command), "TRACE_CONTRIBUTORS_UNAVAILABLE") {
		t.Fatalf("selected scalar member field should carry exact contributor evidence:\n%s", testAQLText(t, compiled.Command))
	}
	for _, field := range []string{ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceExplicitIdentityField} {
		if !containsBindValue(testAQLBinds(t, compiled.Command), field) {
			t.Errorf("group-row CellTrace is missing typed output field %q: %#v", field, testAQLBinds(t, compiled.Command))
		}
	}
}

func TestCompileSourceOnlyGroupRowsCellTraceMatchesExactOneAndAllMemberValues(t *testing.T) {
	tests := []struct {
		name       string
		field      recipe.Field
		policy     recipe.ConstructionRowValuePolicy
		want       []string
		unexpected []string
	}{
		{
			name:   "ONE scalar",
			field:  recipe.Field{Name: "resourceType", ColumnID: "resource_type", Expr: recipe.Expression{Select: "root.resourceType"}},
			policy: recipe.ConstructionRowValueOne,
			want: []string{
				"cohort_member.payload.resourceType",
				"__loom_trace_source_identity",
				" == __loom_physical_cohort_group_final_row.resourceType",
				"__loom_trace_source_identity.id, value:",
				"UNIQUE(",
			},
			unexpected: []string{" IN __loom_physical_cohort_group_final_row.resourceType"},
		},
		{
			name:   "ALL repeated scalar leaf",
			field:  recipe.Field{Name: "given", ColumnID: "given", Expr: recipe.Expression{Select: "root.name[].given[]"}},
			policy: recipe.ConstructionRowValueAll,
			want: []string{
				"ASSERT(IS_ARRAY(",
				"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH",
				"cohort_member.payload.name",
				" IN __loom_physical_cohort_group_final_row.given",
				"__loom_trace_source_identity.id, value:",
				"UNIQUE(",
			},
			unexpected: []string{" == __loom_physical_cohort_group_final_row.given"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := compilePopulationMappingOutput(t, recipe.Output{
				Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
				RootColumnNaming: recipe.RootColumnNamingExact,
				Fields:           []recipe.Field{test.field},
				GroupRows: &recipe.GroupRows{
					RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
					RowValues: []recipe.GroupRowValuePolicy{{ColumnID: test.field.ColumnID, Policy: test.policy}},
				},
			})
			compiled, err := CompileCellTraceOutput(ArangoTarget(), output, test.field.Name, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile %s GROUP_ROWS CellTrace: %v", test.name, err)
			}
			for _, marker := range test.want {
				if !strings.Contains(testAQLText(t, compiled.Command), marker) {
					t.Errorf("query is missing %q:\n%s", marker, testAQLText(t, compiled.Command))
				}
			}
			for _, marker := range test.unexpected {
				if strings.Contains(testAQLText(t, compiled.Command), marker) {
					t.Errorf("query unexpectedly contains %q:\n%s", marker, testAQLText(t, compiled.Command))
				}
			}
			if strings.Contains(testAQLText(t, compiled.Command), "TRACE_CONTRIBUTORS_UNAVAILABLE") {
				t.Errorf("supported %s output has unavailable contributor evidence:\n%s", test.name, testAQLText(t, compiled.Command))
			}
			if !strings.Contains(testAQLText(t, compiled.Command), "source.auth_resource_path IN @auth_resource_paths") ||
				!strings.Contains(testAQLText(t, compiled.Command), "revision.project == @project") ||
				!strings.Contains(testAQLText(t, compiled.Command), "revision.generation == @dataset_generation") {
				t.Errorf("%s trace lost its project, generation, or authorization scope:\n%s", test.name, testAQLText(t, compiled.Command))
			}
		})
	}
}

func TestCompileCellTraceExplainsOptionalTraversalBeforeFirstReduction(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
			Fields: []recipe.Field{{Name: "condition_id", Expr: recipe.Expression{Select: "id"}, ValueMode: recipe.ValueModeFirst}},
		}},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "condition_id", 5, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "TO_ARRAY(", "resourceType:", "resourceId:", "LENGTH(__loom_physical_trace_status_candidates) > 1 ? \"AMBIGUOUS\""} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("trace query missing pre-reduction contributor evidence %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if compiled.ContributionOffset != 5 || compiled.ContributionLimit != 10 {
		t.Fatalf("trace page = %d/%d", compiled.ContributionOffset, compiled.ContributionLimit)
	}
}

func TestCompileCellTraceExplainsRelatedAggregateContributors(t *testing.T) {
	value := recipe.Expression{Select: "valueQuantity.value"}
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			Aggregates: []recipe.Aggregate{{Name: "maximum_value", Operation: recipe.AggregateMax, Expr: &value}},
		}},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "maximum_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "resourceType:", "resourceId:", ".payload.valueQuantity.value"} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("trace query missing related aggregate contributor evidence %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
}

func TestCompileCellTracePreservesAggregateContributorsThroughCategoryRecode(t *testing.T) {
	value := recipe.Expression{Select: "valueString"}
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			Aggregates: []recipe.Aggregate{{Name: "category", Operation: recipe.AggregateMin, Expr: &value}},
		}},
		ColumnTransformations: []recipe.ColumnTransformation{{
			Column: "category",
			Transformation: columntransform.ValueTransformation{
				Kind: columntransform.KindExactCategoryRecode,
				ExactCategoryRecode: &columntransform.ExactCategoryRecode{
					Mappings:      []columntransform.CategoryMapping{{From: "A", To: "Positive"}},
					UnknownPolicy: columntransform.UnknownKeepOriginal,
				},
			},
		}},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "category", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", ".payload.valueString", "resourceType:", "resourceId:"} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("recoded aggregate trace is missing contributor evidence %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if containsBindValue(testAQLBinds(t, compiled.Command), "TRACE_CONTRIBUTORS_UNAVAILABLE") {
		t.Fatalf("recoded aggregate trace unexpectedly reports unavailable contributors: %#v", testAQLBinds(t, compiled.Command))
	}
}

func TestCompileCellTraceExplainsNumericAggregateContributors(t *testing.T) {
	value := recipe.Expression{Select: "root.component[].valueInteger"}
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Observations", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "observation_id", Expr: recipe.Expression{Select: "root.id"}}},
		Aggregates: []recipe.Aggregate{
			{Name: "sum_value", Operation: recipe.AggregateSum, Expr: &value},
			{Name: "mean_value", Operation: recipe.AggregateMean, Expr: &value},
		},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "sum_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NUMERIC_AGGREGATE_NON_NUMERIC", "resourceType:", "resourceId:", "value:", "!= null"} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("numeric aggregate trace is missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if strings.Contains(testAQLText(t, compiled.Command), "AMBIGUOUS") {
		t.Fatalf("a numeric reduction with explicit contributor enumeration must not be marked lossy:\n%s", testAQLText(t, compiled.Command))
	}
}

func TestCompileWindowedAggregateCellTraceUsesEligibleContributors(t *testing.T) {
	value := recipe.Expression{Select: "valueQuantity.value"}
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			Aggregates: []recipe.Aggregate{{
				Name: "window_sum", Operation: recipe.AggregateSum, Expr: &value,
				ContributorWindow: &recipe.ContributorWindow{
					Timestamp:   recipe.Expression{Select: "observation.effectiveDateTime"},
					Anchor:      recipe.Expression{Select: "root.meta.lastUpdated"},
					LowerOffset: -172800, UpperOffset: 0, LowerInclusive: true, UpperInclusive: false,
					Precision: recipe.TemporalPrecisionInstant,
				},
			}},
		}},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "window_sum", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"__loom_physical_contributor_window_eligible",
		"DATE_ADD(__loom_physical_contributor_window_anchor",
		"FOR __loom_physical_trace_numeric_contributor IN FIRST(FOR __loom_physical_contributor_window_scope",
		"__loom_physical_contributor_window_timestamp",
	} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("windowed aggregate trace query missing eligible contributor evidence %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
}

func TestCompileCellTraceUsesOwnerRecordEvidenceAsContributions(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Observations", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		OwnerRecords: []recipe.OwnerRecordProjection{{
			Name: "height_records", FieldRef: "component[].valueQuantity.value",
			Binding: fhirschema.CorrelatedBinding{
				OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
				SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value",
				ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal",
			},
			Key: fhirschema.CorrelatedKey{System: "http://loinc.org", Code: "8302-2"},
		}},
	})
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "height_records", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"trace_owner_record", ".source.resourceType", ".source.resourceId", "ownerOrdinal", "INVALID_CHOICE_ARM"} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("owner-record trace query missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
}

func TestCompileCellTraceCarriesPivotAndDerivedLeafLineage(t *testing.T) {
	output := reshapeOracleOutput("trace_pivot", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "trace-pivot", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"},
				{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.DerivedColumns = []recipe.DerivedColumn{{
		ConstructionID: "trace-derived", Name: "alpha_plus_zero", Label: "Alpha plus zero", Operation: recipe.DerivedAdd,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "alpha"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "zero"},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}}
	compiled, _, err := compileReshapeOracle(output, "trace-pivot-project", "trace-generation", 100)
	if err != nil {
		t.Fatal(err)
	}

	alpha, err := CompileCellTraceOutput(ArangoTarget(), compiled, "alpha", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UNIQUE(FLATTEN", "resourceId:", "FILTER", " != null"} {
		if !strings.Contains(testAQLText(t, alpha.Command), want) {
			t.Fatalf("pivot trace query missing %q:\n%s", want, testAQLText(t, alpha.Command))
		}
	}
	if !containsBindValue(testAQLBinds(t, alpha.Command), ir.PhysicalCellTraceSourceDocumentField) || !containsBindValue(testAQLBinds(t, alpha.Command), ir.PhysicalCellTraceSourcePresencePrefix+"6") {
		t.Fatalf("pivot trace did not bind source document and value-presence fields: %#v", testAQLBinds(t, alpha.Command))
	}

	group, err := CompileCellTraceOutput(ArangoTarget(), compiled, "group_text", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !containsBindValue(testAQLBinds(t, group.Command), "group_text") || !containsBindValue(testAQLBinds(t, group.Command), ir.PhysicalCellTraceSourcePresencePrefix+"2") {
		t.Fatalf("pivot group-key trace does not retain the selected source field:\n%s", testAQLText(t, group.Command))
	}

	derived, err := CompileCellTraceOutput(ArangoTarget(), compiled, "alpha_plus_zero", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(testAQLText(t, derived.Command), "FOR __loom_physical_trace_pivot_cell_source"); got != 4 {
		t.Fatalf("derived trace has %d pivot contributor subqueries, want status/page for alpha and zero:\n%s", got, testAQLText(t, derived.Command))
	}
	if strings.Contains(testAQLText(t, derived.Command), ir.PhysicalCellTraceDerivedOmission) {
		t.Fatalf("supported derived lineage has omission:\n%s", testAQLText(t, derived.Command))
	}
	if !containsBindValue(testAQLBinds(t, derived.Command), "alpha") || !containsBindValue(testAQLBinds(t, derived.Command), "zero") {
		t.Fatalf("derived trace lost pivot category bindings: %#v", testAQLBinds(t, derived.Command))
	}
}

func TestCompileCellTraceDistinguishesUnpivotValueFromGeneratedKey(t *testing.T) {
	output := reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "trace-unpivot")
	compiled, _, err := compileReshapeOracle(output, "trace-unpivot-project", "trace-generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	value, err := CompileCellTraceOutput(ArangoTarget(), compiled, "amount", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sourceDocument", "sourcePresence", "sourceSupported"} {
		if !strings.Contains(testAQLText(t, value.Command), want) {
			t.Fatalf("unpivot trace query missing %q:\n%s", want, testAQLText(t, value.Command))
		}
	}
	if !containsBindValue(testAQLBinds(t, value.Command), ir.PhysicalCellTraceSourceDocumentField) {
		t.Fatalf("unpivot trace did not bind the source-document field: %#v", testAQLBinds(t, value.Command))
	}
	passed, err := CompileCellTraceOutput(ArangoTarget(), compiled, "text_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !containsBindValue(testAQLBinds(t, passed.Command), ir.PhysicalCellTracePassSourceDocumentField) || !containsBindValue(testAQLBinds(t, passed.Command), ir.PhysicalCellTracePassSourcePresenceField) {
		t.Fatalf("unpivot pass-through trace lost source evidence fields: %#v", testAQLBinds(t, passed.Command))
	}
	if !containsBindValue(testAQLBinds(t, value.Command), "group_number") || !containsBindValue(testAQLBinds(t, value.Command), "numeric_value") {
		t.Fatalf("unpivot trace lost selected source columns: %#v", testAQLBinds(t, value.Command))
	}

	key, err := CompileCellTraceOutput(ArangoTarget(), compiled, "measure", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(testAQLText(t, key.Command), ir.PhysicalCellTraceGeneratedKeyOmission) {
		t.Fatalf("unpivot key trace omitted its generated-key reason:\n%s", testAQLText(t, key.Command))
	}
}

func TestConstructionCellTraceResolvesDeriveInputsThroughFilterStage(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	if output.Plan.StageSequence == nil {
		t.Fatal("compiled output has no construction stage sequence")
	}
	lineage, err := constructionCellTraceLineage(output, *output.Plan.StageSequence, "total")
	if err != nil {
		t.Fatal(err)
	}
	if lineage.FinalStageID != "keep_positive" || lineage.ProducerStageID != "derive_total" || lineage.ConstructionID != "calc_total" || lineage.Operation != "DERIVE" {
		t.Fatalf("construction output provenance = %#v", lineage)
	}
	if lineage.OutputColumnID != "total_id" || lineage.OutputColumn != "total" || lineage.RowIdentityColumn != output.Plan.StageSequence.FinalRowIdentity {
		t.Fatalf("construction output identity = %#v", lineage)
	}
	want := ir.PhysicalCellTraceConstructionInput{
		StageID: "source_projection", ColumnID: "amount_id", Column: "amount", FinalValueColumn: "amount",
	}
	if len(lineage.Inputs) != 1 || lineage.Inputs[0] != want || lineage.OmissionCode != "" {
		t.Fatalf("construction source lineage = %#v, want %#v without omission", lineage, want)
	}
}

func TestCompileConstructionCellTraceReturnsDerivedValueAndStageEvidence(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "total", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionTraceUsesPreviewIdentity(t, output, compiled)
	for _, want := range []string{
		"SORT __loom_construction_final_row._key ASC",
		"__loom_construction_final_row.total",
		"__loom_construction_final_row[@",
		"@cell_trace_offset, @cell_trace_fetch_limit",
		"inputStageId:", "inputColumnId:", "outputStageId:", "outputColumnId:", "finalStageId:", "constructionId:", "operation:",
	} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("construction cell trace query is missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	for _, want := range []string{
		"source_projection", "amount_id", "amount", "derive_total", "total_id", "total", "keep_positive", "calc_total", "DERIVE",
	} {
		if !containsBindValue(testAQLBinds(t, compiled.Command), want) {
			t.Errorf("construction trace bind value %q is missing from %#v", want, testAQLBinds(t, compiled.Command))
		}
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("construction trace identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
}

func TestConstructionRelatedSourceCellTraceReplaysScopedRoutePerOccurrence(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionRelatedSourceCellTraceRecipeOutput())
	sequence := output.Plan.StageSequence
	if sequence == nil {
		t.Fatal("compiled output has no construction stage sequence")
	}
	lineage, err := constructionCellTraceLineage(output, *sequence, "observation_status")
	if err != nil {
		t.Fatal(err)
	}
	if lineage.Operation != "RELATED_SOURCE" || lineage.ProducerStageID != "add_observation_status" || lineage.RelatedSource == nil || lineage.OmissionCode != "" {
		t.Fatalf("related-source lineage = %#v", lineage)
	}
	related := lineage.RelatedSource
	if related.AnchorColumn != "_key" || related.ResourceType != "Observation" || len(related.Subplan.Captures) != 1 || related.Subplan.Captures[0] != related.InputRowVariable || related.Subplan.Unique {
		t.Fatalf("related-source route identity or occurrence policy = %#v", related)
	}
	producer := sequence.Stages[1]
	projection, found := stageProjectionByName(producer.OutputProjections, "observation_status")
	if !found || projection.Expression == nil || projection.Expression.Subplan == nil || !reflect.DeepEqual(related.Subplan, *projection.Expression.Subplan) {
		t.Fatal("trace did not retain the exact producer route subplan")
	}

	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "observation_status", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionTraceUsesPreviewIdentity(t, output, compiled)
	for _, want := range []string{
		"SORT __loom_construction_final_row._key ASC",
		"__loom_construction_final_row.observation_status",
		"._key == __loom_construction_final_row._key",
		"__loom_construction_final_row._key",
		".payload.status",
		"LIMIT @cell_trace_offset, @cell_trace_fetch_limit",
		"NO_MATCH",
	} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Errorf("related-source trace query is missing %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if strings.Contains(testAQLText(t, compiled.Command), "UNIQUE(") || strings.Contains(testAQLText(t, compiled.Command), "SORTED_UNIQUE(") || strings.Contains(testAQLText(t, compiled.Command), ".payload.status != null") {
		t.Fatalf("related-source trace must preserve path occurrences and null values without deduplication/filtering:\n%s", testAQLText(t, compiled.Command))
	}
	for _, want := range []string{
		"fixture_program-project-a", "generation-a", "subject_Patient", "Patient", "Observation",
		"resourceType", "resourceId", "value", "outputStageId", "outputColumnId", "observation_status", "add_observation_status", "finalStageId", "RELATED_SOURCE",
	} {
		if !containsBindValue(testAQLBinds(t, compiled.Command), want) {
			t.Errorf("related-source route bind value %q is missing from %#v", want, testAQLBinds(t, compiled.Command))
		}
	}
	if !strings.Contains(testAQLText(t, compiled.Command), "dataset_generation") || !strings.Contains(testAQLText(t, compiled.Command), "auth_resource_path") {
		t.Fatalf("related-source trace lost generation or authorization filters:\n%s", testAQLText(t, compiled.Command))
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" || compiled.RowIdentity == nil {
		t.Fatalf("related-source trace lost exact output row identity: %#v", compiled)
	}
}

func TestConstructionCellTraceRejectsMalformedPublishedIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	output.RowIdentity = output.RowIdentity.Clone()
	output.RowIdentity.Fields = []string{"project", "not_the_root_key"}
	if _, err := CompileCellTraceOutput(ArangoTarget(), output, "total", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || err.Error() != "construction cell trace row identity does not match the final stage" {
		t.Fatalf("malformed construction identity error = %v", err)
	}
}

func TestConstructionCellTraceRejectsStructuredGeneratedIdentity(t *testing.T) {
	recipeOutput := constructionOracleOutput()
	recipeOutput.Construction.Steps = recipeOutput.Construction.Steps[:1]
	output := compilePopulationMappingOutput(t, recipeOutput)
	if output.Plan.StageSequence.FinalRowIdentity != "__loom_row_id" {
		t.Fatalf("fixture final identity = %q, want generated row ID", output.Plan.StageSequence.FinalRowIdentity)
	}
	for index := range output.Plan.StageSequence.FinalColumns {
		if output.Plan.StageSequence.FinalColumns[index].Name == "__loom_row_id" {
			output.Plan.StageSequence.FinalColumns[index].Kind = "object"
		}
	}
	lastStage := len(output.Plan.StageSequence.Stages) - 1
	for index := range output.Plan.StageSequence.Stages[lastStage].OutputColumns {
		if output.Plan.StageSequence.Stages[lastStage].OutputColumns[index].Name == "__loom_row_id" {
			output.Plan.StageSequence.Stages[lastStage].OutputColumns[index].Kind = "object"
		}
	}
	if _, err := CompileCellTraceOutput(ArangoTarget(), output, "alpha", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || err.Error() != "construction cell trace row identity does not match the final stage" {
		t.Fatalf("structured generated identity error = %v", err)
	}
}

func assertConstructionTraceUsesPreviewIdentity(t *testing.T, output lower.CompiledRecipeOutput, compiled CompiledCellTraceQuery) {
	t.Helper()
	if output.RowIdentity == nil || len(output.RowIdentity.Fields) != 2 || output.RowIdentity.Fields[0] != "project" || output.RowIdentity.Fields[1] != "_key" {
		t.Fatalf("normal construction lowering identity = %#v, want ordered project/_key", output.RowIdentity)
	}
	if compiled.RowIdentity == nil || !reflect.DeepEqual(compiled.RowIdentity.Fields, output.RowIdentity.Fields) {
		t.Fatalf("cell trace identity = %#v, want Preview identity %#v", compiled.RowIdentity, output.RowIdentity)
	}
	if testAQLBinds(t, compiled.Command)["project"] != "fixture_program-project-a" {
		t.Fatalf("cell trace project identity bind = %#v, want the same pinned project as Preview", testAQLBinds(t, compiled.Command)["project"])
	}
	if !strings.Contains(testAQLText(t, compiled.Command), "[@project, __loom_construction_final_row._key]") {
		t.Fatalf("cell trace identity parts do not match Preview project/key order:\n%s", testAQLText(t, compiled.Command))
	}
}

func constructionRelatedSourceCellTraceRecipeOutput() recipe.Output {
	columns := []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}}
	outputs := append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{ID: "observation_status", Name: "observation_status"})
	return recipe.Output{
		Name: "related_source_cell_trace", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{
			{
				ID: "keep_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "patient_id", Operator: recipe.FilterExists}},
				Outputs:   columns,
			},
			{
				ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_patients"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: "trace_route_choice", SourceOccurrenceID: "observation-node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "observation_status",
				}},
				Outputs: outputs,
			},
		}},
	}
}

func TestConstructionCellTraceMarksPivotLineageAsUnsupported(t *testing.T) {
	recipeOutput := constructionOracleOutput()
	recipeOutput.Construction.Steps = recipeOutput.Construction.Steps[:1]
	output := compilePopulationMappingOutput(t, recipeOutput)
	lineage, err := constructionCellTraceLineage(output, *output.Plan.StageSequence, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if lineage.Operation != "PIVOT" || lineage.ProducerStageID != "pivot" || lineage.OmissionCode != "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED" || len(lineage.Inputs) != 0 {
		t.Fatalf("pivot trace lineage = %#v, want explicit unsupported-operation omission", lineage)
	}
	if len(lineage.RowIdentityFields) != 1 || lineage.RowIdentityFields[0] != "__loom_row_id" {
		t.Fatalf("pivot trace identity = %#v, want the compiler-generated explicit scalar identity", lineage.RowIdentityFields)
	}
	compiled, err := CompileCellTraceOutput(ArangoTarget(), output, "alpha", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(testAQLText(t, compiled.Command), "__loom_construction_final_row.alpha") || !strings.Contains(testAQLText(t, compiled.Command), "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED") {
		t.Fatalf("unsupported reshape trace must retain the value and explicit omission:\n%s\n%#v", testAQLText(t, compiled.Command), testAQLBinds(t, compiled.Command))
	}
	if compiled.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || compiled.IdentityPartsColumn != "" ||
		!strings.Contains(testAQLText(t, compiled.Command), "__loom_construction_final_row.__loom_row_id") {
		t.Fatalf("pivot trace must match Preview's literal generated row ID without re-hashing:\n%s\n%#v", testAQLText(t, compiled.Command), compiled)
	}
}

func constructionCellTraceRecipeOutput() recipe.Output {
	two := int64(2)
	zero := int64(0)
	return recipe.Output{
		Name: "construction_trace", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "derive_total", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "calc_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "keep_positive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive_total"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterThan,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &zero}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
			},
		},
	}
}

func TestCompileCellTraceRejectsUnknownOrHiddenColumn(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	if _, err := CompileCellTraceOutput(ArangoTarget(), output, "missing", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("unknown trace column was accepted")
	}
	if _, err := CompileCellTraceOutput(ArangoTarget(), output, "__loom_row_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("internal row identity was accepted as a trace column")
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL compile target (pg-sql-24): the same CompileCellTraceOutput
// entry compiles the registered arms for the PostgreSQL target through the
// render/postgres terminal slice, refuses unregistered shapes by name, and
// leaves the Arango voice byte-identical (the AQL control cells below).
// ---------------------------------------------------------------------------

func mustPostgresCellTraceTarget(t *testing.T) Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("pgir02_it_unit")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func compilePopulationMappingOutputWithPolicy(t *testing.T, output recipe.Output, policy ir.PhysicalOptimizationPolicy) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-mapping-test",
		TranslationVersion:  "population-mapping-test",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, policy)
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}

func sql24PostgreSQLOutput() recipe.Output {
	return recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "gender", Expr: recipe.Expression{Select: "root.gender"}},
		},
	}
}

func sql24TraversalOutput() recipe.Output {
	return recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
			Fields: []recipe.Field{{Name: "condition_id", Expr: recipe.Expression{Select: "id"}, ValueMode: recipe.ValueModeFirst}},
		}},
	}
}

func sql24PostgresSQL(t *testing.T, compiled CompiledCellTraceQuery) (string, []command.PostgreSQLParameter) {
	t.Helper()
	sqlText, parameters, err := compiled.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("compiled cell trace command is not a PostgreSQL command: %v", err)
	}
	return sqlText, parameters
}

func TestCompileCellTracePostgreSQLRootExtract(t *testing.T) {
	output := compilePopulationMappingOutput(t, sql24PostgreSQLOutput())
	compiled, err := CompileCellTraceOutput(mustPostgresCellTraceTarget(t), output, "gender", 2, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("PostgreSQL root-extract cell trace compile: %v", err)
	}
	sqlText, parameters := sql24PostgresSQL(t, compiled)
	for _, want := range []string{
		`AS "__loom_trace_value"`, `AS "__loom_trace_contributions"`,
		`AS "__loom_trace_identity_parts"`, `AS "__loom_trace_status"`,
		`AS "__loom_trace_has_more"`, `AS "__loom_trace_omission"`,
		`'NO_MATCH'`, `'RECORDED_NULL'`, `'VALUE'`, `false AS "__loom_trace_has_more"`,
		`jsonb_build_object('resourceType',`,
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("PostgreSQL root-extract trace is missing %q:\n%s", want, sqlText)
		}
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("trace identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
	if compiled.ValueColumn != ir.PhysicalCellTraceValueField || compiled.StatusColumn != ir.PhysicalCellTraceStatusField ||
		compiled.ContributionsColumn != ir.PhysicalCellTraceContributionsField ||
		compiled.HasMoreColumn != ir.PhysicalCellTraceHasMoreField || compiled.OmissionColumn != ir.PhysicalCellTraceOmissionField {
		t.Fatalf("contract columns do not match the registered field names: %#v", compiled)
	}
	if compiled.ContributionOffset != 2 || compiled.ContributionLimit != 5 {
		t.Fatalf("trace page = %d/%d", compiled.ContributionOffset, compiled.ContributionLimit)
	}
	if len(parameters) == 0 {
		t.Fatal("every scope and page fact must travel as an ordered parameter")
	}
	if output.Plan.Operations[terminalIndexOf(t, output.Plan)].CellTraceReturn != nil {
		t.Fatal("compiling a diagnostic terminal mutated the canonical Preview plan")
	}
}

// The real-mint both spelling, produced end to end rather than hand-
// attached: an inline output that authors an Identity projects
// __loom_row_id into the final RETURN (lower/recipe_expression_lowering.go
// appendRecipeIdentity), so the real mint finalPopulationMappingIdentity
// (compiler/population_mapping.go) attaches it beside the ordered parts,
// and cellTraceQuery names the explicit contract column. This pins the
// whole mint-to-contract chain on the PostgreSQL target: the renderer must
// publish the explicit spelling, and the identity-parts name must not
// appear in the statement.
func TestCompileCellTracePostgreSQLAuthoredIdentityMintsExplicitColumn(t *testing.T) {
	output := sql24PostgreSQLOutput()
	output.Identity = &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}}
	root := compilePopulationMappingOutput(t, output)
	compiled, err := CompileCellTraceOutput(mustPostgresCellTraceTarget(t), root, "gender", 0, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("authored-identity cell trace must compile on the PostgreSQL target: %v", err)
	}
	if compiled.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || compiled.IdentityPartsColumn != "" {
		t.Fatalf("contract columns = (%q, %q), want the explicit identity named by the mint",
			compiled.ExplicitIdentityColumn, compiled.IdentityPartsColumn)
	}
	sqlText, _ := sql24PostgresSQL(t, compiled)
	if !strings.Contains(sqlText, ` AS "`+ir.PhysicalCellTraceExplicitIdentityField+`"`) {
		t.Fatalf("the minted explicit identity must publish as its contract column:\n%s", sqlText)
	}
	if strings.Contains(sqlText, ir.PhysicalCellTraceIdentityPartsField) {
		t.Fatalf("the identity-parts spelling must stay unpublished under the mint's pick law:\n%s", sqlText)
	}
	// The real mint kept the ordered parts beside the explicit identity:
	// the terminal is the both state, not an explicit-only forgery.
	terminalSeen := false
	for _, operation := range root.Plan.Operations {
		if operation.Kind == ir.PhysicalCellTraceReturnOp {
			terminalSeen = true
		}
	}
	if terminalSeen {
		t.Fatal("compiling the trace must not mutate the canonical plan")
	}
}

func terminalIndexOf(t *testing.T, plan ir.PhysicalPlan) int {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalCellTraceReturnOp {
			t.Fatalf("canonical preview plan already carries the evidence terminal")
		}
		_ = operation
	}
	return len(plan.Operations) - 1
}

func TestCompileCellTracePostgreSQLReducedSetContributors(t *testing.T) {
	// The registered-surface lowering: the endpoint-lookup planner label
	// has no PostgreSQL storage counterpart, so the registered shape is
	// the native typed-identity join (the spine refuses the other spelling
	// fail-closed — see the endpoint control cell below).
	output := compilePopulationMappingOutputWithPolicy(t, sql24TraversalOutput(),
		ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false))
	compiled, err := CompileCellTraceOutput(mustPostgresCellTraceTarget(t), output, "condition_id", 5, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("PostgreSQL reduced-set cell trace compile: %v", err)
	}
	sqlText, parameters := sql24PostgresSQL(t, compiled)
	for _, want := range []string{
		`jsonb_agg(jsonb_build_object('resourceType',`, `'resourceId',`, `'value',`,
		`AS capped`, `OFFSET $`, `WITH ORDINALITY`,
		`'AMBIGUOUS'`, `'NO_MATCH'`, `AS "__loom_trace_has_more"`,
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("PostgreSQL reduced-set trace is missing %q:\n%s", want, sqlText)
		}
	}
	if compiled.ContributionOffset != 5 || compiled.ContributionLimit != 10 {
		t.Fatalf("trace page = %d/%d", compiled.ContributionOffset, compiled.ContributionLimit)
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField {
		t.Fatalf("identity column = %q", compiled.IdentityPartsColumn)
	}
	_ = parameters
}

// Fail-closed control: the default-policy lowering carries the
// endpoint-lookup planner label; the PostgreSQL target refuses it typed,
// exactly where the spine refuses it for the preview — no AQL fallback, no
// silent translation.
func TestCompileCellTracePostgreSQLRefusesEndpointLookupTyped(t *testing.T) {
	output := compilePopulationMappingOutput(t, sql24TraversalOutput())
	_, err := CompileCellTraceOutput(mustPostgresCellTraceTarget(t), output, "condition_id", 5, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !strings.Contains(err.Error(), "endpoint-lookup") {
		t.Fatalf("endpoint-lookup shape must refuse with the spine's typed voice, got: %v", err)
	}
	if !errors.Is(err, postgresrender.ErrUnsupportedPostgresPlan) {
		t.Fatalf("refusal must be the typed unsupported-plan error: %v", err)
	}
}

func TestCompileCellTracePostgreSQLNamedRefusals(t *testing.T) {
	target := mustPostgresCellTraceTarget(t)

	groupRows := compilePopulationMappingOutput(t, recipe.Output{
		Name: "GroupedSpecimens", RootResourceType: "Specimen", RowGrain: "groups",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "resourceType", ColumnID: "resource_type", Expr: recipe.Expression{Select: "root.resourceType"}}},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "resource_type", Policy: recipe.ConstructionRowValueOne}},
		},
	})
	if _, err := CompileCellTraceOutput(target, groupRows, "resourceType", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil ||
		!strings.Contains(err.Error(), "explicit group rows cell trace is unsupported on the PostgreSQL compile target") {
		t.Fatalf("group-rows trace must refuse by name: %v", err)
	}

	construction := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	if construction.Plan.StageSequence == nil {
		t.Fatal("construction fixture lost its stage sequence")
	}
	if _, err := CompileCellTraceOutput(target, construction, "total", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil ||
		!strings.Contains(err.Error(), "composed construction cell trace is unsupported on the PostgreSQL compile target") {
		t.Fatalf("composed-construction trace must refuse by name: %v", err)
	}

	aggregateValue := recipe.Expression{Select: "valueQuantity.value"}
	aggregate := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			Aggregates: []recipe.Aggregate{{Name: "maximum_value", Operation: recipe.AggregateMax, Expr: &aggregateValue}},
		}},
	})
	if _, err := CompileCellTraceOutput(target, aggregate, "maximum_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy()); err == nil ||
		!strings.Contains(err.Error(), "aggregate, owner-records, call-recode, and prepared-extract values") {
		t.Fatalf("aggregate trace must refuse with the terminal-surface voice: %v", err)
	}

	root := compilePopulationMappingOutput(t, sql24PostgreSQLOutput())
	if _, err := CompileCellTraceOutput(target, root, "missing", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("unknown trace column was accepted on the PostgreSQL target")
	}
	if _, err := CompileCellTraceOutput(target, root, "__loom_row_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("internal row identity was accepted as a trace column on the PostgreSQL target")
	}
	if _, err := CompileCellTraceOutput(target, root, "gender", -1, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil ||
		!strings.Contains(err.Error(), "cell trace offset cannot be negative") {
		t.Fatalf("negative offset must refuse with the shared page voice: %v", err)
	}
	if defaulted, err := CompileCellTraceOutput(target, root, "gender", 0, 0, ir.DefaultPhysicalOptimizationPolicy()); err != nil ||
		defaulted.ContributionLimit != 25 {
		t.Fatalf("limit <= 0 must take the registered default 25 on the shared page contract: %v / %+v", err, defaulted)
	}
	if _, err := CompileCellTraceOutput(target, root, "gender", 0, MaxCellTraceContributions+1, ir.DefaultPhysicalOptimizationPolicy()); err == nil ||
		!strings.Contains(err.Error(), "cell trace contribution limit exceeds") {
		t.Fatalf("over-max limit must refuse with the shared page voice: %v", err)
	}
}

// AQL control cells: the shared preparation refactor left the Arango voice
// untouched for exactly the fixtures the PostgreSQL arm compiles.
func TestCompileCellTraceArangoVoiceUnchangedForPostgresArms(t *testing.T) {
	root := compilePopulationMappingOutput(t, sql24PostgreSQLOutput())
	compiled, err := CompileCellTraceOutput(ArangoTarget(), root, "gender", 2, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"root.payload.gender", "@cell_trace_offset", "@cell_trace_limit", "LENGTH(__loom_physical_trace_status_candidates) == 0"} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("AQL root-extract voice lost %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}

	traversal := compilePopulationMappingOutput(t, sql24TraversalOutput())
	compiled, err = CompileCellTraceOutput(ArangoTarget(), traversal, "condition_id", 5, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "LENGTH(__loom_physical_trace_status_candidates) > 1 ? \"AMBIGUOUS\"", "SLICE("} {
		if !strings.Contains(testAQLText(t, compiled.Command), want) {
			t.Fatalf("AQL reduced-set voice lost %q:\n%s", want, testAQLText(t, compiled.Command))
		}
	}
	if compiled.ContributionOffset != 5 || compiled.ContributionLimit != 10 {
		t.Fatalf("AQL trace page = %d/%d", compiled.ContributionOffset, compiled.ContributionLimit)
	}
}
