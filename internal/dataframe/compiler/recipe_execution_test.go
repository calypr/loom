package compiler_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func prefixTestBindings(mode authscope.ReadScopeMode) recipe.RuntimeBindings {
	bindings := recipe.RuntimeBindings{
		Project: "project", DatasetGeneration: "generation", AuthScopeMode: mode,
	}
	if mode == authscope.ReadScopeRestricted {
		bindings.AuthResourcePaths = []string{"/programs/p1"}
		bindings.IncludeAuthResourcePath = true
	}
	return bindings
}

func prefixTestComposite(t *testing.T, backend ir.PhysicalPrefixSourceBackend, bindings recipe.RuntimeBindings) ir.PhysicalPlan {
	return prefixTestCompositeFromOutput(t, prefixTestConstructionOutput(), backend, bindings)
}

func prefixTestCompositeFromOutput(t *testing.T, output recipe.Output, backend ir.PhysicalPrefixSourceBackend, bindings recipe.RuntimeBindings) ir.PhysicalPlan {
	return prefixTestCompositeWithCombine(t, output, backend, bindings, prefixTestAppendCombine)
}

func prefixTestCompositeWithCombine(t *testing.T, output recipe.Output, backend ir.PhysicalPrefixSourceBackend, bindings recipe.RuntimeBindings, combineFor func(stageID string) ir.PhysicalClickHouseCombine) ir.PhysicalPlan {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "composite-test",
		TranslationVersion: "test", Outputs: []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan() error = %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-digest", bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan() error = %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan() error = %v", err)
	}
	compiledOutput := compiled.Outputs[0]
	combine := combineFor(compiledOutput.Plan.StageSequence.FinalStageID)
	composite, err := lower.ComposeClickHouseCombine(compiledOutput.Plan, combine, backend, bindings)
	if err != nil {
		t.Fatalf("ComposeClickHouseCombine() error = %v", err)
	}
	return composite
}

func prefixTestExpectedSchema(composite ir.PhysicalPlan) []string {
	columns := make([]string, 0, len(composite.StageSequence.FinalColumns))
	for _, column := range composite.StageSequence.FinalColumns {
		columns = append(columns, column.Name)
	}
	return columns
}

// TestCompileClickHousePrefixAQLPreservesExactPrefixBytes pins the AQL arm of
// the prefix seam to the pre-tag baseline bytes and named binds. Any change
// to AQL prefix rendering must fail this test with an explicit review.
func TestCompileClickHousePrefixAQLPreservesExactPrefixBytes(t *testing.T) {
	composite := prefixTestComposite(t, ir.PhysicalPrefixSourceAQL, prefixTestBindings(authscope.ReadScopeRestricted))
	compiledPrefix, err := compiler.CompileClickHousePrefix(compiler.ArangoTarget(), composite)
	if err != nil {
		t.Fatalf("CompileClickHousePrefix() error = %v", err)
	}
	if compiledPrefix.Command.Backend() != command.CompiledBackendAQL {
		t.Fatalf("prefix command backend = %q, want AQL", string(compiledPrefix.Command.Backend()))
	}
	query, binds, err := compiledPrefix.Command.AQLParameters()
	if err != nil {
		t.Fatalf("AQLParameters() error = %v", err)
	}
	wantQuery, err := os.ReadFile("testdata/clickhouse_prefix_aql.query")
	if err != nil {
		t.Fatalf("read golden query: %v", err)
	}
	if query != string(wantQuery) {
		t.Fatalf("AQL prefix bytes drifted from the frozen baseline:\n%s", query)
	}
	wantBindsJSON, err := os.ReadFile("testdata/clickhouse_prefix_aql.binds.json")
	if err != nil {
		t.Fatalf("read golden binds: %v", err)
	}
	var wantBinds map[string]any
	if err := json.Unmarshal(wantBindsJSON, &wantBinds); err != nil {
		t.Fatalf("decode golden binds: %v", err)
	}
	gotBindsJSON, err := json.Marshal(binds)
	if err != nil {
		t.Fatalf("encode binds: %v", err)
	}
	normalizedWant, err := json.Marshal(wantBinds)
	if err != nil {
		t.Fatalf("re-encode golden binds: %v", err)
	}
	if string(gotBindsJSON) != string(normalizedWant) {
		t.Fatalf("AQL prefix binds drifted:\n%s", string(gotBindsJSON))
	}
	if compiledPrefix.Fingerprint == "" {
		t.Fatal("AQL prefix command has no fingerprint")
	}
	if want := prefixTestExpectedSchema(composite); !reflect.DeepEqual(compiledPrefix.OutputColumns, want) {
		t.Fatalf("AQL prefix columns = %v, want the typed final schema %v", compiledPrefix.OutputColumns, want)
	}
}

func TestCompileClickHousePrefixTagAndTargetDispatchIsClosed(t *testing.T) {
	unrestricted := prefixTestBindings(authscope.ReadScopeUnrestricted)
	postgresTarget, err := compiler.PostgreSQLTarget(mustPostgresBinding(t, "verify_ir03v1_offline"))
	if err != nil {
		t.Fatalf("PostgreSQLTarget() error = %v", err)
	}
	cases := []struct {
		name    string
		backend ir.PhysicalPrefixSourceBackend
		target  compiler.Target
		wants   string
	}{
		{"untagged fails closed", "", compiler.ArangoTarget(), "explicit supported source backend"},
		{"unknown tag fails closed", ir.PhysicalPrefixSourceBackend("CLICKHOUSE"), compiler.ArangoTarget(), "explicit supported source backend"},
		{"AQL tag needs AQL target", ir.PhysicalPrefixSourceAQL, postgresTarget, "requires the AQL compile target"},
		{"PostgreSQL tag needs PostgreSQL target", ir.PhysicalPrefixSourcePostgreSQL, compiler.ArangoTarget(), "requires the PostgreSQL compile target"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			composite := prefixTestComposite(t, ir.PhysicalPrefixSourceAQL, unrestricted)
			composite.ClickHousePrefix = &ir.PhysicalClickHousePrefix{
				SourceBackend:     testCase.backend,
				StageID:           composite.ClickHousePrefix.StageID,
				AuthScopeMode:     composite.ClickHousePrefix.AuthScopeMode,
				AuthResourcePaths: append([]string(nil), composite.ClickHousePrefix.AuthResourcePaths...),
			}
			if _, err := compiler.CompileClickHousePrefix(testCase.target, composite); err == nil || !strings.Contains(err.Error(), testCase.wants) {
				t.Fatalf("error = %v, want %q", err, testCase.wants)
			}
		})
	}
}

func TestCompileClickHousePrefixRejectsNonCompositePlans(t *testing.T) {
	bindings := prefixTestBindings(authscope.ReadScopeUnrestricted)
	plain := prefixTestComposite(t, ir.PhysicalPrefixSourceAQL, bindings)
	standalone := ir.ClonePhysicalPlan(plain)
	standalone.ClickHousePrefix = nil
	if _, err := compiler.CompileClickHousePrefix(compiler.ArangoTarget(), standalone); err == nil || !strings.Contains(err.Error(), "composite ClickHouse plan with a typed prefix is required") {
		t.Fatalf("prefix-less plan error = %v", err)
	}
	aqlOnly := ir.ClonePhysicalPlan(plain)
	aqlOnly.Engine = ir.PhysicalEngineAQL
	aqlOnly.ClickHouseCombine = nil
	aqlOnly.ClickHousePrefix = nil
	if _, err := compiler.CompileClickHousePrefix(compiler.ArangoTarget(), aqlOnly); err == nil || !strings.Contains(err.Error(), "composite ClickHouse plan with a typed prefix is required") {
		t.Fatalf("AQL-only plan error = %v", err)
	}
	if _, err := compiler.CompileClickHousePrefix(compiler.Target{}, plain); err != compiler.ErrTargetNotSelected {
		t.Fatalf("zero-target error = %v, want ErrTargetNotSelected", err)
	}
}

// TestCompileRecipeOutputRefusesCompositePlans proves the terminal output
// path never renders a composite plan through a source renderer: the prefix
// goes through CompileClickHousePrefix and the combine through the ClickHouse
// stream path.
func TestCompileRecipeOutputRefusesCompositePlans(t *testing.T) {
	composite := prefixTestComposite(t, ir.PhysicalPrefixSourceAQL, prefixTestBindings(authscope.ReadScopeRestricted))
	output := lower.CompiledRecipeOutput{Name: "composite", Plan: composite}
	_, err := compiler.CompileRecipeOutput(compiler.ArangoTarget(), output, prefixTestBindings(authscope.ReadScopeRestricted), 0, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !strings.Contains(err.Error(), "composite ClickHouse plan") {
		t.Fatalf("CompileRecipeOutput() composite error = %v", err)
	}
	optimized := composite
	output.OptimizedPlan = &optimized
	if _, err := compiler.CompileRecipeOutput(compiler.ArangoTarget(), output, prefixTestBindings(authscope.ReadScopeRestricted), 0, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "composite ClickHouse plan") {
		t.Fatalf("optimized composite error = %v", err)
	}
}

func TestCompileClickHousePrefixPostgreSQLRendersParameterizedSQL(t *testing.T) {
	composite := prefixTestCompositeWithCombine(t, prefixTestFilterOnlyOutput(), ir.PhysicalPrefixSourcePostgreSQL, prefixTestBindings(authscope.ReadScopeUnrestricted), func(stageID string) ir.PhysicalClickHouseCombine {
		return prefixTestAppendCombineFor(stageID, "gender_id", "right_gender", "gender", "string", "String")
	})
	if composite.ClickHousePrefix.SourceBackend != ir.PhysicalPrefixSourcePostgreSQL {
		t.Fatalf("composer did not stamp the PostgreSQL source backend")
	}
	target, err := compiler.PostgreSQLTarget(mustPostgresBinding(t, "verify_ir03v1_offline"))
	if err != nil {
		t.Fatalf("PostgreSQLTarget() error = %v", err)
	}
	compiledPrefix, err := compiler.CompileClickHousePrefix(target, composite)
	if err != nil {
		t.Fatalf("CompileClickHousePrefix() error = %v", err)
	}
	if compiledPrefix.Command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("prefix command backend = %q, want POSTGRESQL", string(compiledPrefix.Command.Backend()))
	}
	sqlText, args, err := compiledPrefix.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("PostgreSQLParameters() error = %v", err)
	}
	if !strings.Contains(sqlText, "$1") {
		t.Fatalf("PostgreSQL prefix is not parameterized: %s", sqlText)
	}
	if len(args) == 0 {
		t.Fatal("PostgreSQL prefix declares no typed arguments")
	}
	if strings.Contains(strings.ToUpper(sqlText), "@") {
		t.Fatalf("PostgreSQL prefix leaked an AQL named bind: %s", sqlText)
	}
	if want := prefixTestExpectedSchema(composite); !reflect.DeepEqual(compiledPrefix.OutputColumns, want) {
		t.Fatalf("PostgreSQL prefix columns = %v, want the exact typed final schema %v", compiledPrefix.OutputColumns, want)
	}
	if compiledPrefix.Fingerprint == "" {
		t.Fatal("PostgreSQL prefix command has no fingerprint")
	}

	// The statement must carry the row identity the terminal Combine consumes:
	// a prefix whose typed sequence points at a missing identity column fails
	// closed instead of producing an unjoinable private artifact.
	broken := ir.ClonePhysicalPlan(composite)
	broken.StageSequence.FinalRowIdentity = "__loom_missing_row_id"
	if _, err := compiler.CompileClickHousePrefix(target, broken); err == nil || !strings.Contains(err.Error(), "row identity") {
		t.Fatalf("missing row identity error = %v", err)
	}
}

// TestCompileClickHousePrefixRefusesDirectlyBuiltRestrictedPostgreSQLPrefix
// pins that the seam itself enforces the composite IR contract. The
// PostgreSQL arm renders a stripped standalone clone, so the full composite
// validation must run at the seam before backend dispatch — otherwise every
// composite refusal (restricted PostgreSQL prefix, cell-trace/row-lineage,
// preview/LIMIT, GROUP/COHORT_GROUP, prefix-to-final-stage binding,
// OutputAuthResourcePathBindKey equality) would be contingent on the caller
// having gone through ComposeClickHouseCombine. A hand-built PG-tagged
// restricted prefix — exactly the artifact the contract promises to refuse
// before any rendering or I/O — must fail here, including in non-canonical
// restricted-mode spellings the exact-match denylist would miss.
func TestCompileClickHousePrefixRefusesDirectlyBuiltRestrictedPostgreSQLPrefix(t *testing.T) {
	target, err := compiler.PostgreSQLTarget(mustPostgresBinding(t, "verify_ir03v1_offline"))
	if err != nil {
		t.Fatalf("PostgreSQLTarget() error = %v", err)
	}
	base := prefixTestCompositeWithCombine(t, prefixTestFilterOnlyOutput(), ir.PhysicalPrefixSourcePostgreSQL, prefixTestBindings(authscope.ReadScopeUnrestricted), func(stageID string) ir.PhysicalClickHouseCombine {
		return prefixTestAppendCombineFor(stageID, "gender_id", "right_gender", "gender", "string", "String")
	})
	for _, mode := range []string{"restricted", "Restricted", ""} {
		t.Run("mode="+strconv.Quote(mode), func(t *testing.T) {
			handBuilt := ir.ClonePhysicalPlan(base)
			handBuilt.ClickHousePrefix.AuthScopeMode = mode
			handBuilt.ClickHousePrefix.AuthResourcePaths = []string{"/programs/p1"}
			handBuilt.ClickHousePrefix.IncludeAuthResourcePath = true
			handBuilt.ClickHousePrefix.AuthResourcePathBindKey = "construction_private_auth_resource_path"
			handBuilt.BindVars["auth_resource_paths"] = []string{"/programs/p1"}
			handBuilt.BindVars["auth_resource_paths_unrestricted"] = false
			handBuilt.BindVars["scope_allowed"] = true
			handBuilt.BindVars["construction_private_auth_resource_path"] = "/programs/p1"
			compiled, err := compiler.CompileClickHousePrefix(target, handBuilt)
			if err == nil || !strings.Contains(err.Error(), "unsupported on the PostgreSQL source backend") {
				t.Fatalf("hand-built restricted PostgreSQL prefix error = %v, want the restricted refusal", err)
			}
			if compiled.Fingerprint != "" || compiled.Command.Backend() != "" || len(compiled.OutputColumns) != 0 {
				t.Fatalf("hand-built restricted PostgreSQL prefix produced a compiled artifact: backend=%q columns=%v fingerprint=%q", string(compiled.Command.Backend()), compiled.OutputColumns, compiled.Fingerprint)
			}
		})
	}
	// The unmodified composer-built plan still compiles, proving the refusal
	// is the scope gate and not the added composite validation rejecting
	// legitimate plans.
	if _, err := compiler.CompileClickHousePrefix(target, base); err != nil {
		t.Fatalf("composer-built unrestricted PostgreSQL prefix: %v", err)
	}
}

func mustPostgresBinding(t *testing.T, schema string) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding(%q): %v", schema, err)
	}
	return binding
}

func prefixTestConstructionOutput() recipe.Output {
	groupLabel, categoryLabel, amountLabel := "Group", "Category", "Amount"
	integerZero, integerOne := int64(0), int64(1)
	stringA, stringB := "a", "b"
	filterValue := int64(5)
	return recipe.Output{
		Name: "construction_test", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "group", ColumnID: "group_id", Label: groupLabel, Expr: recipe.Expression{Select: "gender"}},
			{Name: "category", ColumnID: "category_id", Label: categoryLabel, Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "amount", ColumnID: "amount_id", Label: amountLabel, Expr: recipe.Expression{Select: "multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: groupLabel},
				{ID: "category_id", Name: "category", Label: categoryLabel},
				{ID: "amount_id", Name: "amount", Label: amountLabel},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "pivot_values", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &integerZero}, OutputColumnID: "amount_a_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &integerOne}, OutputColumnID: "amount_b_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}},
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "derive_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_a_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &integerOne}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}, {ID: "total_id", Name: "total"}},
				},
				{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterEq,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &filterValue}},
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}, {ID: "total_id", Name: "total"}},
				},
				{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "unpivot_values", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "amount_a_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringA}},
							{ColumnID: "amount_b_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringB}},
						}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_id", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "amount_id", Name: "amount"}},
				},
			},
		},
	}
}

func prefixTestFilterOnlyOutput() recipe.Output {
	value := "male"
	return recipe.Output{
		Name: "filter_prefix", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "id"}},
			{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "gender"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "patient_id", Name: "patient_id", Type: "string"},
				{ID: "gender_id", Name: "gender", Type: "string"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "source_filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "gender_id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "patient_id", Name: "patient_id", Type: "string"},
					{ID: "gender_id", Name: "gender", Type: "string"},
				},
			}},
		},
	}
}

func prefixTestAppendCombine(stageID string) ir.PhysicalClickHouseCombine {
	return prefixTestAppendCombineFor(stageID, "amount_id", "right_amount", "amount", "integer", "Int64")
}

func prefixTestAppendCombineFor(stageID, inputColumnID, rightColumnID, outputID, logicalType, clickHouseType string) ir.PhysicalClickHouseCombine {
	return ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{PrivateStageID: stageID},
			{TableID: "table-right", RevisionID: "revision-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: outputID, InputIndex: 0, InputColumnID: inputColumnID},
			{OutputColumnID: outputID, InputIndex: 1, InputColumnID: rightColumnID},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: outputID, Name: outputID, LogicalType: logicalType, ClickHouseType: clickHouseType}},
	}
}
