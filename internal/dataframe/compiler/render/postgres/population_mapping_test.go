package postgres

// Offline battery for pg-sql-22: the population member-to-output mapping
// terminals (flat and construction) render onto the trusted storage +
// membership bindings as fully parameterized, deterministic PostgreSQL with
// the AQL twin's witness contract (sort-unique member arrays, the null/
// non-array empty-expansion guard, the (identity, member) de-duplication,
// the identity-then-member COLLATE "C" total order, and the bound preview
// limit). Plans are production-compiled through semantic/lower and then
// carry the compiler's mapping stamp (compiler/population_mapping.go),
// replicated here for the render-side door; refusals are typed, and the
// entry==dispatch law is checked cell by cell. The live PG18 acceptance
// legs (L1-L7, owned namespace verify_sql22v1_*) are in the same file.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	control "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	explorer "github.com/calypr/loom/internal/explorer"
)

var (
	sql22FixturesMu      sync.Mutex
	sql22TrackedFixtures = map[string]bool{}
)

// sql22RunDeltaAudit registers this ticket's disjoint verify_sql22v1_*
// namespace with the package delta audit (main_test.go chain).
func sql22RunDeltaAudit(inner func() int) int {
	return runVerifyNamespaceDeltaAudit("verify_sql22v1_", &sql22FixturesMu, sql22TrackedFixtures, inner)
}

// sql22TrackResource registers one minted verify_sql22v1_* resource with
// the package delta audit.
func sql22TrackResource(t *testing.T, name string) {
	t.Helper()
	sql22FixturesMu.Lock()
	defer sql22FixturesMu.Unlock()
	sql22TrackedFixtures[name] = true
}

// ---------------------------------------------------------------------------
// Builders: production-compiled mapping shapes with the compiler's stamp.
// ---------------------------------------------------------------------------

func map22Bindings() recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "fixture_program-project-a", SelectionProject: "fixture_program/project-a",
		DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members",
	}
}

func map22Compile(t *testing.T, output recipe.Output) ir.PhysicalPlan {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-mapping-render-test", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle validate: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, map22Bindings())
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	return compiled.Outputs[0].Plan
}

func map22Population(resourceType string) *recipe.PopulationConstraint {
	return &recipe.PopulationConstraint{
		SelectionRevisionID: "selrev_22", MembershipDigest: "sha256:members-22", MemberCount: 3,
		ResourceType: resourceType,
	}
}

func map22SpecimenOutput() recipe.Output {
	return recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields:     []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: map22Population("Specimen"),
	}
}

// map22StampFlat replicates compiler/population_mapping.go:139-152 on a
// freshly lowered flat plan: the member-collecting root plus the terminal
// swap carrying the compiled identity (row-identity fields read through
// their RETURN projection, or their bind when the field is a constant).
func map22StampFlat(t *testing.T, plan ir.PhysicalPlan) ir.PhysicalPlan {
	t.Helper()
	plan = ir.ClonePhysicalPlan(plan)
	scan := plan.Operations[0].RootScan
	scan.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
	returnIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			returnIndex = index
		}
	}
	if returnIndex < 0 {
		t.Fatal("flat mapping plan has no RETURN operation")
	}
	returnOp := plan.Operations[returnIndex].Return
	projections := map[string]ir.PhysicalProjection{}
	for _, projection := range returnOp.Projections {
		projections[projection.Name] = projection
	}
	parts := make([]ir.PhysicalPopulationMappingIdentityPart, 0, 2)
	for _, field := range []string{"project", "_key"} {
		if projection, ok := projections[field]; ok {
			expression := ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull, Value: &projection.Value,
			}
			if projection.Expression != nil {
				expression = ir.ClonePhysicalExpression(*projection.Expression)
			}
			parts = append(parts, ir.PhysicalPopulationMappingIdentityPart{Name: field, Expression: expression})
			continue
		}
		if _, bound := plan.BindVars[field]; bound {
			parts = append(parts, ir.PhysicalPopulationMappingIdentityPart{
				Name: field,
				Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					Value:        &ir.PhysicalValue{BindKey: field},
				},
			})
			continue
		}
		t.Fatalf("identity field %q is neither projected nor bound", field)
	}
	members := ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Value:        &ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable},
	}
	plan.Operations[returnIndex] = ir.PhysicalOperation{
		Kind:   ir.PhysicalPopulationMappingReturnOp,
		Source: plan.Operations[returnIndex].Source,
		PopulationMappingReturn: &ir.PhysicalPopulationMappingReturn{
			Members: members, IdentityParts: parts,
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("stamp flat validate: %v", err)
	}
	return plan
}

// map22SummaryOutput is the registered pure composition (the keyed or
// grand-total summary GROUP) under a population constraint; the keyed
// variant drives the contributor pool's GROUP BY/join path.
func map22SummaryOutput(keyed bool) recipe.Output {
	output := recipe.Output{
		Name: "construction_summary_mapping", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "amount_id", Name: "amount"}},
			Steps: []recipe.ConstructionStep{{
				ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "whole_table_summary",
					Aggregates: []recipe.ConstructionGroupAggregate{
						{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
					},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "rows_id", Name: "rows"}, {ID: "status_count_id", Name: "status_count"},
				},
			}},
		},
		Population: map22Population("Observation"),
	}
	if keyed {
		group := output.Construction.Steps[0].Operation.Group
		group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "status_key_id"}}
		output.Construction.Steps[0].Outputs = append(
			[]recipe.StageColumn{{ID: "status_key_id", Name: "status_key"}},
			output.Construction.Steps[0].Outputs...)
	}
	return output
}

// map22StampConstruction replicates compiler/population_mapping.go:127-137
// (addConstructionPopulationMembersSourceProjection + the terminal stamp).
func map22StampConstruction(t *testing.T, plan ir.PhysicalPlan) ir.PhysicalPlan {
	t.Helper()
	plan = ir.ClonePhysicalPlan(plan)
	plan.Operations[0].RootScan.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
	rootKeyFound := false
	for _, column := range plan.StageSequence.SourceColumns {
		if column.Name == "_key" && column.Internal &&
			column.RootContributorResourceType == plan.Source.ResourceType &&
			column.Kind == "string" && column.Cardinality == "required_one" {
			rootKeyFound = true
		}
	}
	if !rootKeyFound {
		t.Fatal("construction mapping source does not retain the exact root storage key")
	}
	stamped := false
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{
			Name: ir.PhysicalPopulationMappingMembersColumn, Hidden: true,
			Value: ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable},
		})
		stamped = true
		break
	}
	if !stamped {
		t.Fatal("construction mapping source has no terminal source RETURN")
	}
	// The compiler reads the contributor cardinality from the final typed
	// column (compiler/population_mapping.go finalConstructionPopulationMappingIdentity).
	contributorsMany := false
	contributorFound := false
	for _, column := range plan.StageSequence.FinalColumns {
		if column.Internal && column.Name == "__loom_root_contributor_keys" &&
			column.RootContributorResourceType == plan.Source.ResourceType && column.Kind == "string" {
			contributorsMany = column.Cardinality == "many"
			contributorFound = true
		}
	}
	if !contributorFound {
		t.Fatal("construction mapping requires compiler-proven root contributor identities in its final stage")
	}
	plan.StageSequence.PopulationMappingReturn = &ir.PhysicalStagePopulationMappingReturn{
		SourceRootKeyColumn: "_key", SourceMemberIDsColumn: ir.PhysicalPopulationMappingMembersColumn,
		FinalRootContributorColumn: "__loom_root_contributor_keys", FinalRootContributorsMany: contributorsMany,
		RowIdentityColumn: plan.StageSequence.FinalRowIdentity,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("stamp construction validate: %v", err)
	}
	return plan
}

func map22Render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPopulationMappingPlan(plan, mustBinding(t), populationBindingOK(t))
	if err != nil {
		t.Fatalf("RenderPopulationMappingPlan: %v\n%s", err, map22Dump(plan))
	}
	return rendered
}

func map22Dump(plan ir.PhysicalPlan) string {
	var builder strings.Builder
	for index, operation := range plan.Operations {
		if index > 12 {
			break
		}
		builder.WriteString(string(operation.Kind) + " ")
	}
	return builder.String()
}

// map22WantRefusal asserts the typed refusal from both entry points with
// the SAME message (entry==dispatch), and that it is ErrUnsupportedPostgresPlan.
// Where the registered ir schema gate speaks first (the sql16RequireRefusal
// convention), the render entry is compared against the composition door
// entry instead, both carrying the identical ir words.
func map22WantRefusal(t *testing.T, plan ir.PhysicalPlan, want string) {
	t.Helper()
	binding := populationBindingOK(t)
	_, renderErr := RenderPopulationMappingPlan(plan, mustBinding(t), binding)
	validateErr := ValidatePopulationMappingSource(plan, binding)
	if renderErr == nil {
		t.Fatalf("render accepted a plan that must refuse %q:\n%s", want, map22Dump(plan))
	}
	if !strings.Contains(renderErr.Error(), want) {
		t.Fatalf("refusal for %q says %q", want, renderErr)
	}
	if validateErr == nil || validateErr.Error() != renderErr.Error() {
		t.Fatalf("entry==dispatch broken for %q: render=%v validate=%v", want, renderErr, validateErr)
	}
	if !strings.Contains(renderErr.Error(), ErrUnsupportedPostgresPlan.Error()) &&
		!strings.HasPrefix(renderErr.Error(), "validate physical plan:") {
		t.Fatalf("refusal for %q is neither typed nor the registered ir gate: %v", want, renderErr)
	}
}

func map22WantSQL(t *testing.T, sql string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(sql, want) {
			t.Fatalf("mapping SQL is missing %q:\n%s", want, sql)
		}
	}
}

// ---------------------------------------------------------------------------
// Flat terminal.
// ---------------------------------------------------------------------------

func TestPopulationMappingFlatWitnessSQLIsExactAndParameterized(t *testing.T) {
	plan := map22StampFlat(t, map22Compile(t, map22SpecimenOutput()))
	rendered := map22Render(t, plan)
	if rendered.SQL == "" {
		t.Fatal("empty SQL")
	}
	if want := []string{ir.PhysicalPopulationMappingMemberField, ir.PhysicalPopulationMappingIdentityPartsField}; !reflect.DeepEqual(rendered.OutputColumns, want) {
		t.Fatalf("output columns = %#v, want %#v", rendered.OutputColumns, want)
	}
	memberTable := `FROM "` + testSchema + `"."selection_members" AS pm`
	map22WantSQL(t, rendered.SQL,
		// Witness candidate: GROUP BY identity + sort-unique member array.
		memberTable,
		`to_jsonb(ARRAY_AGG(DISTINCT (pm."id" COLLATE "C") ORDER BY (pm."id" COLLATE "C"))) AS "__loom_population_members"`,
		// The AQL "== null ? []" iterable twin.
		`CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN pop."__loom_population_members" IS NULL OR jsonb_typeof(pop."__loom_population_members") <> 'array' THEN '[]'::jsonb ELSE pop."__loom_population_members" END) AS w0(member)`,
		// The two positional witness columns and the default identity array.
		`SELECT w0.member AS "c0", jsonb_build_array(`,
		// Total order: window order then the member bytewise.
		`w0.member COLLATE "C" ASC`,
	)
	// Full parameterization: no plan string inlined, no AQL bind syntax.
	if strings.Contains(rendered.SQL, "@") {
		t.Fatalf("mapping SQL carries AQL bind syntax:\n%s", rendered.SQL)
	}
	// No plan string may be inlined: every quoted literal in the statement
	// is a structural constant (the empty-array guard, the member column),
	// never plan data.
	if strings.Contains(rendered.SQL, "selrev_22") || strings.Contains(rendered.SQL, "fixture_program") {
		t.Fatalf("mapping SQL inlines plan data:\n%s", rendered.SQL)
	}
	foundSelection := false
	for _, parameter := range rendered.Args {
		if text, ok := parameter.Value.(string); ok && text == "selrev_22" {
			foundSelection = true
		}
	}
	if !foundSelection {
		t.Fatalf("selection revision bind never parameterized: %#v", rendered.Args)
	}
}

func TestPopulationMappingFlatDeterministic(t *testing.T) {
	plan := map22StampFlat(t, map22Compile(t, map22SpecimenOutput()))
	first := map22Render(t, plan)
	second := map22Render(t, plan)
	if first.SQL != second.SQL {
		t.Fatal("rendering the same plan twice produced different SQL")
	}
	if len(first.Args) != len(second.Args) {
		t.Fatalf("argument count drifted: %d vs %d", len(first.Args), len(second.Args))
	}
	for index := range first.Args {
		if first.Args[index].Type != second.Args[index].Type || !reflect.DeepEqual(first.Args[index].Value, second.Args[index].Value) {
			t.Fatalf("argument %d drifted: %#v vs %#v", index, first.Args[index], second.Args[index])
		}
	}
}

func TestPopulationMappingFlatExplicitIdentityPreservesCompiledExpression(t *testing.T) {
	output := map22SpecimenOutput()
	output.Identity = &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}}
	plan := map22Compile(t, output)
	plan = ir.ClonePhysicalPlan(plan)
	plan.Operations[0].RootScan.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
	returnIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			returnIndex = index
		}
	}
	var explicit *ir.PhysicalExpression
	for _, projection := range plan.Operations[returnIndex].Return.Projections {
		if projection.Name == "__loom_row_id" && projection.Expression != nil {
			expression := ir.ClonePhysicalExpression(*projection.Expression)
			explicit = &expression
		}
	}
	if explicit == nil {
		t.Fatal("compiled explicit identity projection is missing")
	}
	// ir keeps the ordered parts beside an explicit identity (the compiler
	// stamps both from the row identity; the renderer prefers explicit).
	parts := []ir.PhysicalPopulationMappingIdentityPart{
		{Name: "project", Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: "project"},
		}},
		{Name: "_key", Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: plan.Operations[0].RootScan.Variable, Path: []string{"_key"}},
		}},
	}
	plan.Operations[returnIndex] = ir.PhysicalOperation{
		Kind:   ir.PhysicalPopulationMappingReturnOp,
		Source: plan.Operations[returnIndex].Source,
		PopulationMappingReturn: &ir.PhysicalPopulationMappingReturn{
			Members: ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable},
			},
			IdentityParts:    parts,
			ExplicitIdentity: explicit,
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("explicit stamp validate: %v", err)
	}
	rendered := map22Render(t, plan)
	if want := []string{ir.PhysicalPopulationMappingMemberField, ir.PhysicalPopulationMappingExplicitIdentityField}; !reflect.DeepEqual(rendered.OutputColumns, want) {
		t.Fatalf("output columns = %#v, want %#v", rendered.OutputColumns, want)
	}
	// The compiled expression survives as a payload path read, not a plan
	// string and not an identity array.
	map22WantSQL(t, rendered.SQL, `#> $`, `AS "c1"`)
	if strings.Contains(rendered.SQL, "jsonb_build_array(") {
		t.Fatalf("explicit identity re-spelled as a parts array:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "root.payload.id") || strings.Contains(rendered.SQL, "__loom_row_id") {
		t.Fatalf("explicit identity leaked a plan string into SQL:\n%s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Construction terminal.
// ---------------------------------------------------------------------------

func TestPopulationMappingConstructionWitnessSQL(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		name := "grand-total"
		if keyed {
			name = "keyed"
		}
		t.Run(name, func(t *testing.T) {
			plan := map22StampConstruction(t, map22Compile(t, map22SummaryOutput(keyed)))
			rendered := map22Render(t, plan)
			map22WantSQL(t, rendered.SQL,
				"WITH __loom_src AS (",
				`FROM "`+testSchema+`"."selection_members" AS pm`,
				`pop."__loom_population_members" AS "c3"`,
				"rcpool AS (",
				"ARRAY_AGG(DISTINCT",
				"INNER JOIN __loom_src AS ps ON",
				"CROSS JOIN LATERAL jsonb_array_elements_text(",
				`GROUP BY s."`,
				`COLLATE "C" ASC, mn0.member COLLATE "C" ASC`,
			)
			if strings.Contains(rendered.SQL, "@") {
				t.Fatalf("construction mapping SQL carries AQL bind syntax:\n%s", rendered.SQL)
			}
			if want := []string{ir.PhysicalPopulationMappingMemberField, ir.PhysicalPopulationMappingIdentityPartsField}; !reflect.DeepEqual(rendered.OutputColumns, want) {
				t.Fatalf("output columns = %#v, want %#v", rendered.OutputColumns, want)
			}
		})
	}
}

func TestPopulationMappingConstructionPreviewLimitIsBound(t *testing.T) {
	plan := map22StampConstruction(t, map22Compile(t, map22SummaryOutput(true)))
	plan.StageSequence.PreviewLimitBindKey = "preview_limit"
	plan.BindVars["preview_limit"] = 7
	rendered := map22Render(t, plan)
	if !strings.HasSuffix(rendered.SQL, " LIMIT $13") && !strings.Contains(rendered.SQL, " LIMIT $") {
		t.Fatalf("preview limit is not a bound parameter:\n%s", rendered.SQL)
	}
	found := false
	for _, parameter := range rendered.Args {
		if value, ok := parameter.Value.(int64); ok && value == 7 && parameter.Type == command.PostgreSQLParameterBigint {
			found = true
		}
	}
	if !found {
		t.Fatalf("preview limit bind is not a typed bigint 7: %#v", rendered.Args)
	}
	// A non-positive or foreign-typed limit refuses typed.
	broken := ir.ClonePhysicalPlan(plan)
	broken.BindVars["preview_limit"] = 0
	map22WantRefusal(t, broken, "preview limit bind")
}

// ---------------------------------------------------------------------------
// Refusal battery (each gate arm; entry==dispatch on every cell).
// ---------------------------------------------------------------------------

func TestPopulationMappingRefusals(t *testing.T) {
	flat := func() ir.PhysicalPlan {
		return map22StampFlat(t, map22Compile(t, map22SpecimenOutput()))
	}
	construction := func() ir.PhysicalPlan {
		return map22StampConstruction(t, map22Compile(t, map22SummaryOutput(true)))
	}

	t.Run("wrong-collect-members-variable", func(t *testing.T) {
		plan := flat()
		plan.Operations[0].RootScan.Population.CollectMembersVariable = "someone_elses_members"
		map22WantRefusal(t, plan, "CollectMembersVariable")
	})
	t.Run("plain-population-root-unstamped", func(t *testing.T) {
		plan := map22Compile(t, map22SpecimenOutput())
		map22WantRefusal(t, plan, "CollectMembersVariable")
	})
	t.Run("members-variable-mismatch", func(t *testing.T) {
		plan := flat()
		terminal := plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
		terminal.Members.Value.Variable = "wrong_members"
		map22WantRefusal(t, plan, "collected-member value")
	})
	t.Run("identity-less-terminal", func(t *testing.T) {
		plan := flat()
		terminal := plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
		terminal.IdentityParts = nil
		map22WantRefusal(t, plan, "no renderable value")
	})
	t.Run("unnamed-identity-part", func(t *testing.T) {
		plan := flat()
		terminal := plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
		terminal.IdentityParts[0].Name = ""
		map22WantRefusal(t, plan, "has no name")
	})
	t.Run("flat-return-alongside-terminal", func(t *testing.T) {
		plan := flat()
		terminal := *plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
		plan.Operations = plan.Operations[:len(plan.Operations)-1]
		plan.Operations = append(plan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalReturnOp,
			Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
				{Name: "x", Value: ir.PhysicalValue{BindKey: "project"}},
			}},
		})
		plan.Operations = append(plan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalPopulationMappingReturnOp, PopulationMappingReturn: &terminal,
		})
		map22WantRefusal(t, plan, "replaces the RETURN projection list")
	})
	t.Run("flat-terminal-not-last", func(t *testing.T) {
		plan := flat()
		terminal := *plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
		operations := []ir.PhysicalOperation{
			plan.Operations[0],
			{Kind: ir.PhysicalPopulationMappingReturnOp, PopulationMappingReturn: &terminal},
		}
		operations = append(operations, plan.Operations[1:len(plan.Operations)-1]...)
		plan.Operations = operations
		map22WantRefusal(t, plan, "single final operation")
	})
	t.Run("unnest-on-mapping-root", func(t *testing.T) {
		plan := flat()
		terminal := *plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
		plan.Operations = plan.Operations[:len(plan.Operations)-1]
		plan.Operations = append(plan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalUnnestOp,
			Unnest: &ir.PhysicalUnnest{OutputVariable: "item"},
		})
		plan.Operations = append(plan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalPopulationMappingReturnOp, PopulationMappingReturn: &terminal,
		})
		map22WantRefusal(t, plan, "named capability gap")
	})
	t.Run("construction-missing-member-projection", func(t *testing.T) {
		plan := map22Compile(t, map22SummaryOutput(true))
		plan = ir.ClonePhysicalPlan(plan)
		plan.Operations[0].RootScan.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
		plan.StageSequence.PopulationMappingReturn = &ir.PhysicalStagePopulationMappingReturn{
			SourceRootKeyColumn: "_key", SourceMemberIDsColumn: ir.PhysicalPopulationMappingMembersColumn,
			FinalRootContributorColumn: "__loom_root_contributor_keys", FinalRootContributorsMany: false,
			RowIdentityColumn: plan.StageSequence.FinalRowIdentity,
		}
		map22WantRefusal(t, plan, "omits the compiler-owned member witness projection")
	})
	t.Run("construction-foreign-contributor-column", func(t *testing.T) {
		// The registered ir provenance gate speaks first (its voice is the
		// composition door's, compared in map22WantRefusal).
		plan := construction()
		plan.StageSequence.PopulationMappingReturn.FinalRootContributorColumn = "someone_elses_keys"
		map22WantRefusal(t, plan, "not compiler-owned provenance")
	})
	t.Run("construction-missing-terminal-payload", func(t *testing.T) {
		plan := construction()
		plan.StageSequence.PopulationMappingReturn = nil
		map22WantRefusal(t, plan, "PopulationMappingReturn payload")
	})
	t.Run("construction-foreign-terminal-columns", func(t *testing.T) {
		plan := construction()
		plan.StageSequence.PopulationMappingReturn.RowIdentityColumn = ""
		map22WantRefusal(t, plan, "row identity column does not match")
	})
	t.Run("preview-source-window-flag", func(t *testing.T) {
		plan := flat()
		plan.PreviewSourceWindowByRootID = true
		map22WantRefusal(t, plan, "preview source windowing")
	})
	t.Run("foreign-engine", func(t *testing.T) {
		plan := flat()
		plan.Engine = ir.PhysicalEngineClickHouse
		map22WantRefusal(t, plan, "has no PostgreSQL source")
	})
	t.Run("no-population-root", func(t *testing.T) {
		plan := map22StampConstruction(t, map22Compile(t, map22SummaryOutput(false)))
		plan.Operations[0].RootScan.Population = nil
		map22WantRefusal(t, plan, "membership-driven population root source")
	})
	// Slice-door law, hand-assembled plans: the flat surface gate does
	// not nil-check operation payloads and renderOperations dereferences
	// Sort/Limit/Filter, so the registered ir schema gate (plan.Validate,
	// run by both entries like every sibling slice entry) must fail the
	// nil payload closed — never a nil-pointer panic out of the render.
	for _, kind := range []struct {
		name    string
		opKind  ir.PhysicalOperationKind
		opValue ir.PhysicalOperation
		want    string
	}{
		{"nil-sort-payload", ir.PhysicalSortOp, ir.PhysicalOperation{Kind: ir.PhysicalSortOp}, "validate physical plan: operation 5 (SORT)"},
		{"nil-limit-payload", ir.PhysicalLimitOp, ir.PhysicalOperation{Kind: ir.PhysicalLimitOp}, "validate physical plan: operation 5 (LIMIT)"},
		{"nil-filter-payload", ir.PhysicalFilterOp, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp}, "validate physical plan: operation 5 (FILTER)"},
	} {
		kind := kind
		t.Run(kind.name, func(t *testing.T) {
			plan := flat()
			terminal := plan.Operations[len(plan.Operations)-1]
			plan.Operations = append(plan.Operations[:len(plan.Operations)-1], kind.opValue, terminal)
			map22WantRefusal(t, plan, kind.want)
		})
	}
}

func TestPopulationMappingPlainEntriesKeepTheDoorsShut(t *testing.T) {
	// The plain spine entry must never render a mapping plan (the dispatch
	// arm is gated on the mapping posture the plain entry never sets).
	plan := map22StampFlat(t, map22Compile(t, map22SpecimenOutput()))
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), ErrUnsupportedPostgresPlan.Error()) {
		t.Fatalf("plain spine accepted a mapping plan: %v", err)
	}
	// The plain population entry must refuse a member-collecting root (D2).
	if _, err := RenderPopulationRootScan(plan, mustBinding(t), populationBindingOK(t)); err == nil ||
		!strings.Contains(err.Error(), "pg-sql-22 population terminal") {
		t.Fatalf("RenderPopulationRootScan accepted a member witness root: %v", err)
	}
	// The registered composition door must refuse the mapping terminal.
	construction := map22StampConstruction(t, map22Compile(t, map22SummaryOutput(true)))
	if _, err := RenderPhysicalPlan(construction, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), ErrUnsupportedPostgresPlan.Error()) {
		t.Fatalf("composition door accepted a mapping terminal: %v", err)
	}
	// The registered surface voice specifically: the mapping terminal is
	// unregistered for every non-mapping door (validateStageSequencePlanSurface).
	if _, err := renderStageSequencePhysicalPlan(construction, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), "population-mapping returns have no PostgreSQL compiled route") {
		t.Fatalf("composition door surface spoke a different voice: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Live PostgreSQL 18 acceptance (L1-L7), owned namespace verify_sql22v1_*.
// The evidence run executes with LOOM_TEST_POSTGRES_URL set and asserts
// zero skips among the mapping legs.
// ---------------------------------------------------------------------------

const (
	map22Project    = "sql22-proj"
	map22Foreign    = "sql22-proj-x"
	map22Generation = "gen-1"
	map22ObsAuth    = "FHIR/Observation"
	map22PatAuth    = "FHIR/Patient"
	map22SecretAuth = "FHIR/Secret"
	map22RouteLabel = "encounter-diagnosed_condition"
)

type map22Service struct {
	*pgir02Service
	store   *control.ExplorerStore
	binding ir.PostgresStorageBinding
	members PostgresPopulationRootBinding
}

func newMap22Service(t *testing.T) *map22Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql22v1")
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	sql22TrackResource(t, database)
	sql22TrackResource(t, svc.schema)
	applyExplorerMembershipDDL(t, svc)
	store, err := control.NewExplorerStore(svc.client, svc.schema)
	if err != nil {
		svc.fail("open explorer store", err)
	}
	binding, err := ir.NewPostgresStorageBinding(svc.schema)
	if err != nil {
		svc.fail("storage binding", err)
	}
	members, err := NewPostgresPopulationRootBinding(svc.schema)
	if err != nil {
		svc.fail("membership binding", err)
	}
	return &map22Service{pgir02Service: svc, store: store, binding: binding, members: members}
}

func (s *map22Service) render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPopulationMappingPlan(plan, s.binding, s.members)
	if err != nil {
		s.fail("render mapping plan", err)
	}
	return rendered
}

func (s *map22Service) seedSelection(t *testing.T, id, project, resourceType string, memberIDs []string) {
	t.Helper()
	now := time.Now().UTC()
	header, err := s.store.BeginSelection(s.ctx, explorer.SelectionRevision{
		ID: id, Project: project, Generation: map22Generation, ResourceType: resourceType,
		Rule:        explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:      explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: "scope-22", RuleDigest: "rule-22", CreatedAt: now,
	}, "sql22-writer")
	if err != nil {
		s.fail("begin selection "+id, err)
	}
	members := make([]explorer.SelectionMember, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		members = append(members, explorer.SelectionMember{Ref: explorer.ResourceRef{
			Project: project, Generation: map22Generation, ResourceType: resourceType, ID: memberID}})
	}
	if _, err := s.store.AppendSelectionMembers(s.ctx, header.ID, "sql22-writer", members); err != nil {
		s.fail("append selection members "+id, err)
	}
	digest, count, bytes, err := s.store.DigestSelectionMembers(s.ctx, project, header.ID)
	if err != nil {
		s.fail("digest selection "+id, err)
	}
	if _, err := s.store.CompleteSelection(s.ctx, header.ID, "sql22-writer", digest, count, bytes, time.Now().UTC()); err != nil {
		s.fail("complete selection "+id, err)
	}
}

func map22Observation(t *testing.T, s *map22Service, logicalID, status string, value float64, authPath string) {
	t.Helper()
	s.insertResource(t, s.schema, map22Project, map22Generation, "Observation", logicalID, authPath,
		map[string]any{
			"resourceType": "Observation", "id": logicalID, "status": status,
			"valueQuantity": map[string]any{"value": value},
		})
}

// map22Rebind re-points a production-compiled plan onto the live fixture's
// project/generation/selection identity. Every replacement targets exactly
// one offline constant, so a missing constant fails the cell instead of
// leaving a stale bind behind.
func map22Rebind(t *testing.T, plan ir.PhysicalPlan, selectionID string) ir.PhysicalPlan {
	t.Helper()
	plan = ir.ClonePhysicalPlan(plan)
	seen := map[string]bool{}
	for key, value := range plan.BindVars {
		text, ok := value.(string)
		if !ok {
			continue
		}
		switch text {
		case "fixture_program-project-a", "fixture_program/project-a":
			plan.BindVars[key] = map22Project
			seen["project"] = true
		case "generation-a":
			plan.BindVars[key] = map22Generation
			seen["generation"] = true
		case "selrev_22":
			plan.BindVars[key] = selectionID
			seen["selection"] = true
		}
	}
	for _, want := range []string{"project", "generation", "selection"} {
		if !seen[want] {
			t.Fatalf("rebind never saw the offline %s constant", want)
		}
	}
	plan.BindVars["auth_resource_paths"] = []string{map22ObsAuth, map22PatAuth}
	plan.BindVars["auth_resource_paths_unrestricted"] = false
	plan.BindVars["scope_allowed"] = true
	return plan
}

// map22LiveFlatPlan builds the hand-shaped one-hop population plan (the
// pop18 builder) with the pg-sql-22 witness stamp applied.
func map22LiveFlatPlan(hops int, selectionID string, authPaths []string, unrestricted, allowed bool) ir.PhysicalPlan {
	plan := populationPlan(hops)
	plan.BindVars["project"] = map22Project
	plan.BindVars["dataset_generation"] = map22Generation
	plan.BindVars["population_project"] = map22Project
	plan.BindVars["population_selection_id"] = selectionID
	plan.BindVars["population_resource_type"] = "Observation"
	plan.BindVars["population_source_collection"] = "Observation"
	plan.BindVars["root_collection"] = "Observation"
	if hops > 0 {
		plan.BindVars["root_collection"] = "Patient"
		plan.BindVars["population_root_route_0_target_type"] = "Patient"
		plan.BindVars["population_root_route_0_edge_label"] = map22RouteLabel
	}
	plan.BindVars["auth_resource_paths"] = authPaths
	plan.BindVars["auth_resource_paths_unrestricted"] = unrestricted
	plan.BindVars["scope_allowed"] = allowed
	plan.Operations[0].RootScan.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
	returnIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			returnIndex = index
		}
	}
	plan.Operations[returnIndex] = ir.PhysicalOperation{
		Kind:   ir.PhysicalPopulationMappingReturnOp,
		Source: plan.Operations[returnIndex].Source,
		PopulationMappingReturn: &ir.PhysicalPopulationMappingReturn{
			Members: ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable},
			},
			IdentityParts: []ir.PhysicalPopulationMappingIdentityPart{
				{Name: "project", Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: "project"},
				}},
				{Name: "_key", Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					Value:        &ir.PhysicalValue{Variable: plan.Operations[0].RootScan.Variable, Path: []string{"_key"}},
				}},
			},
		},
	}
	return plan
}

func (s *map22Service) runRows(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := s.iso.Query(s.ctx, rendered.SQL, args...)
	if err != nil {
		s.fail("execute mapping statement", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read mapping row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = map22Cell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("mapping row iteration", err)
	}
	return out
}

// map22Cell renders identity cells in canonical JSON text: pgx decodes
// jsonb columns into Go composites, which stringifyCell's %v form would
// print Go-style ([a b]) instead of the JSON the adapter will transport.
func map22Cell(value any) string {
	switch value.(type) {
	case []any, map[string]any:
		text, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("<unmarshalable:%v>", err)
		}
		return string(text)
	default:
		return stringifyCell(value)
	}
}

func map22JoinRows(rows [][]string) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, strings.Join(row, "|"))
	}
	return strings.Join(parts, " ; ")
}

// L1: exact rows and scope, direct population root (no route). Selected
// O1,O2 witness; unselected O3 and the foreign-project selection never
// produce witness rows.
func TestPopulationMappingLiveExactRowsAndScope(t *testing.T) {
	s := newMap22Service(t)
	map22Observation(t, s, "O1", "final", 1, map22ObsAuth)
	map22Observation(t, s, "O2", "final", 2, map22ObsAuth)
	map22Observation(t, s, "O3", "final", 3, map22ObsAuth)
	s.seedSelection(t, "sel22_obs", map22Project, "Observation", []string{"O1", "O2"})
	s.seedSelection(t, "sel22_obs_x", map22Foreign, "Observation", []string{"O3"})
	plan := map22LiveFlatPlan(0, "sel22_obs", []string{map22ObsAuth}, false, true)
	rows := s.runRows(t, s.render(t, plan))
	want := `O1|["` + map22Project + `","O1"] ; O2|["` + map22Project + `","O2"]`
	if got := map22JoinRows(rows); got != want {
		t.Fatalf("witness rows = %q, want %q", got, want)
	}
}

// L1b + L3-dedup: a one-hop route where P1 reaches members O1 and O2 —
// through a DUPLICATE edge for O1. The member array must stay
// sort-unique (ARRAY_AGG DISTINCT); the witness rows are exactly the two
// (member, identity) pairs in identity-then-member byte order.
func TestPopulationMappingLiveRouteMembersDedupAndOrder(t *testing.T) {
	s := newMap22Service(t)
	map22Observation(t, s, "O1", "final", 1, map22ObsAuth)
	map22Observation(t, s, "O2", "final", 2, map22ObsAuth)
	map22Observation(t, s, "O3", "final", 3, map22ObsAuth)
	s.seedSelection(t, "sel22_obs", map22Project, "Observation", []string{"O1", "O2"})
	s.insertResource(t, s.schema, map22Project, map22Generation, "Patient", "P1", map22PatAuth,
		map[string]any{"resourceType": "Patient", "id": "P1"})
	s.insertResource(t, s.schema, map22Project, map22Generation, "Patient", "P2", map22PatAuth,
		map[string]any{"resourceType": "Patient", "id": "P2"})
	for _, pair := range []struct{ target, path string }{{"O1", "focus[0]"}, {"O1", "focus[1]"}, {"O2", "focus[2]"}} {
		s.insertEdge(t, control.EdgeOccurrenceRow{
			Project: map22Project, DatasetGeneration: map22Generation, AuthResourcePath: map22PatAuth,
			Label: map22RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P1",
			TargetResourceType: "Observation", TargetLogicalID: pair.target, SourcePath: pair.path,
		})
	}
	// P2 routes only to the unselected O3.
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: map22Project, DatasetGeneration: map22Generation, AuthResourcePath: map22PatAuth,
		Label: map22RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P2",
		TargetResourceType: "Observation", TargetLogicalID: "O3", SourcePath: "focus[0]",
	})
	plan := map22LiveFlatPlan(1, "sel22_obs", []string{map22PatAuth, map22ObsAuth}, false, true)
	rendered := s.render(t, plan)
	rows := s.runRows(t, rendered)
	want := `O1|["` + map22Project + `","P1"] ; O2|["` + map22Project + `","P1"]`
	if got := map22JoinRows(rows); got != want {
		t.Fatalf("route witness rows = %q, want %q\nSQL: %s", got, want, rendered.SQL)
	}
	// The duplicate edge is really there: the raw member aggregation sees
	// O1 twice, which is what the sort-unique witness array must collapse.
	raw := s.witness(t, `SELECT count(*) FROM "`+s.schema+`".selection_members pm
		INNER JOIN "`+s.schema+`".fhir_resource x0 ON x0.project = $1 AND x0.dataset_generation = $2
		  AND x0.resource_type = 'Observation' AND x0.logical_id = pm.id
		INNER JOIN "`+s.schema+`".fhir_edge_occurrence e1 ON e1.project = $1 AND e1.dataset_generation = $2
		  AND e1.label = $3 AND e1.target_resource_type = 'Observation' AND e1.target_logical_id = x0.logical_id
		  AND e1.source_resource_type = 'Patient' AND e1.source_logical_id = 'P1'
		WHERE pm.selection_id = $4 AND pm.project = $1`,
		map22Project, map22Generation, map22RouteLabel, "sel22_obs")
	if len(raw) != 1 || raw[0][0] != "3" {
		t.Fatalf("duplicate-edge fixture not present (raw aggregation = %v, want 3 rows)", raw)
	}
}

// L2: an empty population yields ZERO witness rows — never an
// empty-aggregate row.
func TestPopulationMappingLiveEmptyPopulationNoRows(t *testing.T) {
	s := newMap22Service(t)
	map22Observation(t, s, "O1", "final", 1, map22ObsAuth)
	s.seedSelection(t, "sel22_empty", map22Project, "Observation", []string{"O9"})
	plan := map22LiveFlatPlan(0, "sel22_empty", []string{map22ObsAuth}, false, true)
	rows := s.runRows(t, s.render(t, plan))
	if len(rows) != 0 {
		t.Fatalf("empty population produced witness rows: %v", map22JoinRows(rows))
	}
	// The selection row and its member row really exist: the zero rows
	// come from the missing resource twin, not from missing fixture data.
	members := s.witness(t, `SELECT count(*) FROM "`+s.schema+`".selection_members
		WHERE selection_id = $1 AND project = $2`, "sel22_empty", map22Project)
	if len(members) != 1 || members[0][0] != "1" {
		t.Fatalf("empty-population fixture missing: %v", members)
	}
}

// L3/L4: the construction terminal (keyed summary GROUP) over the
// population-bounded source, with the preview-limit leg returning the
// bound prefix in the deterministic order.
func TestPopulationMappingLiveConstructionAndPreviewLimit(t *testing.T) {
	s := newMap22Service(t)
	map22Observation(t, s, "O1", "final", 1, map22ObsAuth)
	map22Observation(t, s, "O2", "final", 2, map22ObsAuth)
	map22Observation(t, s, "O3", "amended", 3, map22ObsAuth)
	s.seedSelection(t, "sel22_obs", map22Project, "Observation", []string{"O1", "O2"})
	plan := map22Rebind(t, map22StampConstruction(t, map22CompileLive(t, map22SummaryOutput(true))), "sel22_obs")
	rendered := s.render(t, plan)
	rows := s.runRows(t, rendered)
	// Both members land in the single "final" group (O3 is unselected and
	// out of scope); identity is the group's construction identity string,
	// members are byte-ordered.
	if len(rows) != 2 || rows[0][0] != "O1" || rows[1][0] != "O2" {
		t.Fatalf("construction witness rows = %v", map22JoinRows(rows))
	}
	if rows[0][1] != rows[1][1] {
		t.Fatalf("both members must share the one group identity: %v", map22JoinRows(rows))
	}
	// L4: the preview limit is a bound parameter returning the ordered prefix.
	limited := ir.ClonePhysicalPlan(plan)
	limited.StageSequence.PreviewLimitBindKey = "preview_limit"
	limited.BindVars["preview_limit"] = 1
	limitedRendered := s.render(t, limited)
	if !strings.Contains(limitedRendered.SQL, " LIMIT $") {
		t.Fatalf("preview limit not parameterized:\n%s", limitedRendered.SQL)
	}
	limitedRows := s.runRows(t, limitedRendered)
	if len(limitedRows) != 1 || limitedRows[0][0] != rows[0][0] || limitedRows[0][1] != rows[0][1] {
		t.Fatalf("preview prefix must equal the full-run first row: %v vs %v",
			map22JoinRows(limitedRows), map22JoinRows(rows))
	}
}

func map22CompileLive(t *testing.T, output recipe.Output) ir.PhysicalPlan {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-mapping-live", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle validate: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, map22Bindings())
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, map22Project, map22Generation)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	return compiled.Outputs[0].Plan
}

// L5: a null identity part keeps the member row a witness row — the row
// survives with a jsonb null part (typed rejection stays the adapter's
// law, never the renderer dropping the row).
func TestPopulationMappingLiveNullIdentityPartSurvives(t *testing.T) {
	s := newMap22Service(t)
	map22Observation(t, s, "O1", "final", 1, map22ObsAuth)
	s.seedSelection(t, "sel22_obs", map22Project, "Observation", []string{"O1"})
	plan := map22LiveFlatPlan(0, "sel22_obs", []string{map22ObsAuth}, false, true)
	terminal := plan.Operations[len(plan.Operations)-1].PopulationMappingReturn
	terminal.IdentityParts = []ir.PhysicalPopulationMappingIdentityPart{
		{Name: "nullable", Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: "nullable_part"},
		}},
		{Name: "_key", Expression: terminal.IdentityParts[1].Expression},
	}
	plan.BindVars["nullable_part"] = nil
	rows := s.runRows(t, s.render(t, plan))
	want := `O1|[null,"O1"]`
	if got := map22JoinRows(rows); got != want {
		t.Fatalf("null-part witness rows = %q, want %q", got, want)
	}
}

// L6 + L7: repeat execution is stably ordered; the deny posture (scope
// false, and an auth set excluding every fixture row) reads zero rows.
func TestPopulationMappingLiveStableOrderAndDeny(t *testing.T) {
	s := newMap22Service(t)
	map22Observation(t, s, "O1", "final", 1, map22ObsAuth)
	map22Observation(t, s, "O2", "final", 2, map22ObsAuth)
	s.seedSelection(t, "sel22_obs", map22Project, "Observation", []string{"O1", "O2"})
	plan := map22LiveFlatPlan(0, "sel22_obs", []string{map22ObsAuth}, false, true)
	rendered := s.render(t, plan)
	first := map22JoinRows(s.runRows(t, rendered))
	second := map22JoinRows(s.runRows(t, rendered))
	if first != second {
		t.Fatalf("repeat execution diverged:\n%s\n%s", first, second)
	}
	denied := ir.ClonePhysicalPlan(plan)
	denied.BindVars["scope_allowed"] = false
	denyRows := s.runRows(t, s.render(t, denied))
	if len(denyRows) != 0 {
		t.Fatalf("canonical deny read rows: %v", map22JoinRows(denyRows))
	}
	unauth := ir.ClonePhysicalPlan(plan)
	unauth.BindVars["auth_resource_paths"] = []string{map22SecretAuth}
	if len(s.runRows(t, s.render(t, unauth))) != 0 {
		t.Fatal("auth exclusion read rows")
	}
}
