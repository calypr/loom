package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCompileCellTraceUsesFinalValueAndStableIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	compiled, err := CompileCellTraceOutputWithPolicy(output, "patient_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"root.payload.id", "@cell_trace_offset", "@cell_trace_limit", "[@project, root._key]"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("trace query missing %q:\n%s", want, compiled.Query)
		}
	}
	for _, field := range []string{ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField, ir.PhysicalCellTraceStatusField, ir.PhysicalCellTraceHasMoreField} {
		if !containsBindValue(compiled.BindVars, field) {
			t.Fatalf("trace field %q missing from binds %#v", field, compiled.BindVars)
		}
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("trace identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "condition_id", 5, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "TO_ARRAY(", "resourceType:", "resourceId:", "LENGTH(__loom_physical_trace_status_candidates) > 1 ? \"AMBIGUOUS\""} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("trace query missing pre-reduction contributor evidence %q:\n%s", want, compiled.Query)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "maximum_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "resourceType:", "resourceId:", ".payload.valueQuantity.value"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("trace query missing related aggregate contributor evidence %q:\n%s", want, compiled.Query)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "category", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", ".payload.valueString", "resourceType:", "resourceId:"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("recoded aggregate trace is missing contributor evidence %q:\n%s", want, compiled.Query)
		}
	}
	if containsBindValue(compiled.BindVars, "TRACE_CONTRIBUTORS_UNAVAILABLE") {
		t.Fatalf("recoded aggregate trace unexpectedly reports unavailable contributors: %#v", compiled.BindVars)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "sum_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NUMERIC_AGGREGATE_NON_NUMERIC", "resourceType:", "resourceId:", "value:", "!= null"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("numeric aggregate trace is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "AMBIGUOUS") {
		t.Fatalf("a numeric reduction with explicit contributor enumeration must not be marked lossy:\n%s", compiled.Query)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "window_sum", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"__loom_physical_contributor_window_eligible",
		"DATE_ADD(__loom_physical_contributor_window_anchor",
		"FOR __loom_physical_trace_numeric_contributor IN FIRST(FOR __loom_physical_contributor_window_scope",
		"__loom_physical_contributor_window_timestamp",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("windowed aggregate trace query missing eligible contributor evidence %q:\n%s", want, compiled.Query)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "height_records", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"trace_owner_record", ".source.resourceType", ".source.resourceId", "ownerOrdinal", "INVALID_CHOICE_ARM"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("owner-record trace query missing %q:\n%s", want, compiled.Query)
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

	alpha, err := CompileCellTraceOutputWithPolicy(compiled, "alpha", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UNIQUE(FLATTEN", "resourceId:", "FILTER", " != null"} {
		if !strings.Contains(alpha.Query, want) {
			t.Fatalf("pivot trace query missing %q:\n%s", want, alpha.Query)
		}
	}
	if !containsBindValue(alpha.BindVars, ir.PhysicalCellTraceSourceDocumentField) || !containsBindValue(alpha.BindVars, ir.PhysicalCellTraceSourcePresencePrefix+"6") {
		t.Fatalf("pivot trace did not bind source document and value-presence fields: %#v", alpha.BindVars)
	}

	group, err := CompileCellTraceOutputWithPolicy(compiled, "group_text", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !containsBindValue(group.BindVars, "group_text") || !containsBindValue(group.BindVars, ir.PhysicalCellTraceSourcePresencePrefix+"2") {
		t.Fatalf("pivot group-key trace does not retain the selected source field:\n%s", group.Query)
	}

	derived, err := CompileCellTraceOutputWithPolicy(compiled, "alpha_plus_zero", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(derived.Query, "FOR __loom_physical_trace_pivot_cell_source"); got != 4 {
		t.Fatalf("derived trace has %d pivot contributor subqueries, want status/page for alpha and zero:\n%s", got, derived.Query)
	}
	if strings.Contains(derived.Query, ir.PhysicalCellTraceDerivedOmission) {
		t.Fatalf("supported derived lineage has omission:\n%s", derived.Query)
	}
	if !containsBindValue(derived.BindVars, "alpha") || !containsBindValue(derived.BindVars, "zero") {
		t.Fatalf("derived trace lost pivot category bindings: %#v", derived.BindVars)
	}
}

func TestCompileCellTraceDistinguishesUnpivotValueFromGeneratedKey(t *testing.T) {
	output := reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "trace-unpivot")
	compiled, _, err := compileReshapeOracle(output, "trace-unpivot-project", "trace-generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	value, err := CompileCellTraceOutputWithPolicy(compiled, "amount", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sourceDocument", "sourcePresence", "sourceSupported"} {
		if !strings.Contains(value.Query, want) {
			t.Fatalf("unpivot trace query missing %q:\n%s", want, value.Query)
		}
	}
	if !containsBindValue(value.BindVars, ir.PhysicalCellTraceSourceDocumentField) {
		t.Fatalf("unpivot trace did not bind the source-document field: %#v", value.BindVars)
	}
	passed, err := CompileCellTraceOutputWithPolicy(compiled, "text_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !containsBindValue(passed.BindVars, ir.PhysicalCellTracePassSourceDocumentField) || !containsBindValue(passed.BindVars, ir.PhysicalCellTracePassSourcePresenceField) {
		t.Fatalf("unpivot pass-through trace lost source evidence fields: %#v", passed.BindVars)
	}
	if !containsBindValue(value.BindVars, "group_number") || !containsBindValue(value.BindVars, "numeric_value") {
		t.Fatalf("unpivot trace lost selected source columns: %#v", value.BindVars)
	}

	key, err := CompileCellTraceOutputWithPolicy(compiled, "measure", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(key.Query, ir.PhysicalCellTraceGeneratedKeyOmission) {
		t.Fatalf("unpivot key trace omitted its generated-key reason:\n%s", key.Query)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "total", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
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
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("construction cell trace query is missing %q:\n%s", want, compiled.Query)
		}
	}
	for _, want := range []string{
		"source_projection", "amount_id", "amount", "derive_total", "total_id", "total", "keep_positive", "calc_total", "DERIVE",
	} {
		if !containsBindValue(compiled.BindVars, want) {
			t.Errorf("construction trace bind value %q is missing from %#v", want, compiled.BindVars)
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

	compiled, err := CompileCellTraceOutputWithPolicy(output, "observation_status", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
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
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("related-source trace query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "UNIQUE(") || strings.Contains(compiled.Query, "SORTED_UNIQUE(") || strings.Contains(compiled.Query, ".payload.status != null") {
		t.Fatalf("related-source trace must preserve path occurrences and null values without deduplication/filtering:\n%s", compiled.Query)
	}
	for _, want := range []string{
		"project-a", "generation-a", "subject_Patient", "Patient", "Observation",
		"resourceType", "resourceId", "value", "outputStageId", "outputColumnId", "observation_status", "add_observation_status", "finalStageId", "RELATED_SOURCE",
	} {
		if !containsBindValue(compiled.BindVars, want) {
			t.Errorf("related-source route bind value %q is missing from %#v", want, compiled.BindVars)
		}
	}
	if !strings.Contains(compiled.Query, "dataset_generation") || !strings.Contains(compiled.Query, "auth_resource_path") {
		t.Fatalf("related-source trace lost generation or authorization filters:\n%s", compiled.Query)
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" || compiled.RowIdentity == nil {
		t.Fatalf("related-source trace lost exact output row identity: %#v", compiled)
	}
}

func TestConstructionCellTraceRejectsMalformedPublishedIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	output.RowIdentity = output.RowIdentity.Clone()
	output.RowIdentity.Fields = []string{"project", "not_the_root_key"}
	if _, err := CompileCellTraceOutputWithPolicy(output, "total", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || err.Error() != "construction cell trace row identity does not match the final stage" {
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
	if _, err := CompileCellTraceOutputWithPolicy(output, "alpha", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || err.Error() != "construction cell trace row identity does not match the final stage" {
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
	if compiled.BindVars["project"] != "project-a" {
		t.Fatalf("cell trace project identity bind = %#v, want the same pinned project as Preview", compiled.BindVars["project"])
	}
	if !strings.Contains(compiled.Query, "[@project, __loom_construction_final_row._key]") {
		t.Fatalf("cell trace identity parts do not match Preview project/key order:\n%s", compiled.Query)
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
	compiled, err := CompileCellTraceOutputWithPolicy(output, "alpha", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "__loom_construction_final_row.alpha") || !strings.Contains(compiled.Query, "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED") {
		t.Fatalf("unsupported reshape trace must retain the value and explicit omission:\n%s\n%#v", compiled.Query, compiled.BindVars)
	}
	if compiled.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || compiled.IdentityPartsColumn != "" ||
		!strings.Contains(compiled.Query, "__loom_construction_final_row.__loom_row_id") {
		t.Fatalf("pivot trace must match Preview's literal generated row ID without re-hashing:\n%s\n%#v", compiled.Query, compiled)
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
	if _, err := CompileCellTraceOutputWithPolicy(output, "missing", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("unknown trace column was accepted")
	}
	if _, err := CompileCellTraceOutputWithPolicy(output, "__loom_row_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("internal row identity was accepted as a trace column")
	}
}
