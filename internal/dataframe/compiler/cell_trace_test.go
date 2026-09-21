package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
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
