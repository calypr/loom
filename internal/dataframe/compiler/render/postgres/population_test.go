package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// population_test.go pins the closed pg-sql-18 surface: the exact SQL and
// ordered arguments of the population and cohort root sources (byte-stable,
// fully parameterized), the typed-refusal battery with alive-assertion
// controls (every refusal is paired with the minimal accepted variant, so no
// guard is vacuous), the membership column allowlist pinned literally to the
// owning explorer migrations, and — service-gated — the real PostgreSQL 18
// legs in the registered verify_pop18_* namespace.

const (
	popTestMembersCollection   = "loom_explorer_selection_members"
	popTestSelectionCollection = "loom_explorer_selections"
	popTestRevisionsCollection = "loom_explorer_explicit_group_revisions"
	popTestGroupCollection     = "loom_explorer_explicit_group_memberships"
)

func populationBindingOK(t *testing.T) PostgresPopulationRootBinding {
	t.Helper()
	binding, err := NewPostgresPopulationRootBinding(testSchema)
	if err != nil {
		t.Fatalf("NewPostgresPopulationRootBinding: %v", err)
	}
	return binding
}

func renderPopulationOK(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPopulationRootScan(plan, mustBinding(t), populationBindingOK(t))
	if err != nil {
		t.Fatalf("RenderPopulationRootScan: %v", err)
	}
	return rendered
}

// populationPlan builds the exact payload lower/population.go emits for one
// reverse route hop: Observation is the selected member/source type, Patient
// the root type, one INBOUND edge step (the flipped storage direction of the
// forward OUTBOUND route step lower/population.go:75-101 records).
func populationPlan(hops int) ir.PhysicalPlan {
	plan := basePlan()
	scan := plan.Operations[0].RootScan
	memberVariable := "population_member"
	sourceVariable := "population_source"
	memberID := ir.PhysicalValue{Variable: memberVariable, Path: []string{"id"}}
	memberBind := func(field string) *ir.PhysicalValue {
		return &ir.PhysicalValue{BindKey: "population_" + field}
	}
	source := &ir.PhysicalPopulationRootSource{
		MemberScan: ir.PhysicalCollectionScan{Variable: memberVariable, CollectionBindKey: "population_members_collection"},
		MemberID:   memberID,
		MemberFilters: []ir.PhysicalFilter{
			canonicalMemberFilter(memberVariable, "selectionId", memberBind("selection_id")),
			canonicalMemberFilter(memberVariable, "project", memberBind("project")),
			canonicalMemberFilter(memberVariable, "generation", &ir.PhysicalValue{BindKey: "dataset_generation"}),
			canonicalMemberFilter(memberVariable, "resourceType", memberBind("resource_type")),
		},
		RootKey: ir.PhysicalValue{Variable: "population_root_node_0", Path: []string{"_key"}},
	}
	if hops == 0 {
		source.RootKey = ir.PhysicalValue{Variable: sourceVariable, Path: []string{"_key"}}
	}
	source.ResourceOperations = append(source.ResourceOperations,
		ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{
			Variable: sourceVariable, CollectionBindKey: "population_source_collection"}})
	source.ResourceOperations = appendScopeBlock(source.ResourceOperations, []string{sourceVariable}, "population_source_scope_allowed")
	source.ResourceOperations = append(source.ResourceOperations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
		Predicate: ir.PhysicalPredicate{Operator: "EQUALS",
			Left:  ir.PhysicalValue{Variable: sourceVariable, Path: []string{"id"}},
			Right: &memberID}}})
	terminal := sourceVariable
	for hop := 0; hop < hops; hop++ {
		target := fmt.Sprintf("population_root_node_%d", hop)
		edge := fmt.Sprintf("population_root_edge_%d", hop)
		prefix := fmt.Sprintf("population_root_route_%d", hop)
		source.ResourceOperations = append(source.ResourceOperations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: terminal, TargetVariable: target, EdgeVariable: edge,
			Direction: ir.PhysicalInbound, EdgeCollectionBindKey: prefix + "_edge_collection",
			EdgeLabelBindKey: prefix + "_edge_label", TargetTypeBindKey: prefix + "_target_type",
			EdgeTargetTypeField: "from_type",
		}})
		source.ResourceOperations = appendScopeBlock(source.ResourceOperations, []string{edge, target}, prefix+"_scope_allowed")
		terminal = target
	}
	scan.Population = source
	plan.BindVars["population_selection_id"] = "selrev_7"
	plan.BindVars["population_project"] = "proj-a"
	plan.BindVars["population_resource_type"] = "Observation"
	plan.BindVars["population_members_collection"] = popTestMembersCollection
	plan.BindVars["population_source_collection"] = "Observation"
	for hop := 0; hop < hops; hop++ {
		prefix := fmt.Sprintf("population_root_route_%d", hop)
		plan.BindVars[prefix+"_edge_collection"] = "fhir_edge"
		plan.BindVars[prefix+"_edge_label"] = "encounter-diagnosed_condition"
		plan.BindVars[prefix+"_target_type"] = "Patient"
	}
	return plan
}

func canonicalMemberFilter(memberVariable, path string, right *ir.PhysicalValue) ir.PhysicalFilter {
	return ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
		Operator: "EQUALS",
		Left:     ir.PhysicalValue{Variable: memberVariable, Path: []string{path}},
		Right:    right,
	}}
}

// appendScopeBlock appends the canonical project/generation/auth block for
// the given variables, exactly as lower/plan_orchestration.go emits it.
func appendScopeBlock(operations []ir.PhysicalOperation, variables []string, resultVariable string) []ir.PhysicalOperation {
	projectRight := ir.PhysicalValue{BindKey: "project"}
	generationRight := ir.PhysicalValue{BindKey: "dataset_generation"}
	scopeRight := ir.PhysicalValue{BindKey: "scope_allowed"}
	for _, variable := range variables {
		operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
			Predicate: ir.PhysicalPredicate{Operator: "EQUALS",
				Left:  ir.PhysicalValue{Variable: variable, Path: []string{"project"}},
				Right: &projectRight}}})
	}
	for _, variable := range variables {
		operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
			Predicate: ir.PhysicalPredicate{Operator: "EQUALS",
				Left:  ir.PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}},
				Right: &generationRight}}})
	}
	inputs := make([]ir.PhysicalValue, 0, len(variables)+2)
	for _, variable := range variables {
		inputs = append(inputs, ir.PhysicalValue{Variable: variable, Path: []string{"auth_resource_path"}})
	}
	inputs = append(inputs,
		ir.PhysicalValue{BindKey: "auth_resource_paths"},
		ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
		Variable: resultVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: inputs}})
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
		Predicate: ir.PhysicalPredicate{Operator: "EQUALS",
			Left:  ir.PhysicalValue{Variable: resultVariable},
			Right: &scopeRight}}})
	return operations
}

// cohortPlan builds the exact payload lower/recipe_construction.go:401-412
// emits: a Patient root bounded by a pinned explicit-group revision whose
// rows retain exact root identity.
func cohortPlan() ir.PhysicalPlan {
	plan := basePlan()
	plan.Source = ir.PhysicalSource{ResourceType: "Patient"}
	plan.Operations[0].RootScan.CohortSource = &ir.PhysicalCohortRootSource{
		CohortStageID: "group_rows", CohortInputStageID: "source-stage",
		RootIdentityColumn: "_key", RootResourceType: "Patient",
		RevisionCollectionBindKey:         "cohort_revision_collection",
		SelectionCollectionBindKey:        "cohort_selection_collection",
		SelectionMembersCollectionBindKey: "cohort_selection_members_collection",
		MembershipsCollectionBindKey:      "cohort_memberships_collection",
		RevisionIDBindKey:                 "cohort_revision_id",
		ProjectBindKey:                    "cohort_project",
		ResourceProjectBindKey:            "cohort_resource_project",
		DatasetGenerationBindKey:          "cohort_dataset_generation",
		ResourceTypeBindKey:               "cohort_resource_type",
		PolicyBindKey:                     "cohort_policy",
	}
	plan.BindVars["cohort_revision_collection"] = popTestRevisionsCollection
	plan.BindVars["cohort_selection_collection"] = popTestSelectionCollection
	plan.BindVars["cohort_selection_members_collection"] = popTestMembersCollection
	plan.BindVars["cohort_memberships_collection"] = popTestGroupCollection
	plan.BindVars["cohort_revision_id"] = "grouprev_1"
	plan.BindVars["cohort_project"] = "proj-a"
	plan.BindVars["cohort_resource_project"] = "proj-a"
	plan.BindVars["cohort_dataset_generation"] = "gen-1"
	plan.BindVars["cohort_resource_type"] = "Patient"
	plan.BindVars["cohort_policy"] = "EXCLUDE"
	return plan
}

// ---------------------------------------------------------------------------
// Registry trust and the migration-literal pin
// ---------------------------------------------------------------------------

func TestMembershipBindingTrust(t *testing.T) {
	if _, err := NewPostgresPopulationRootBinding(""); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("empty schema error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	for _, schema := range []string{"Public", `x"; DROP`, "0abc", strings.Repeat("a", 64)} {
		if _, err := NewPostgresPopulationRootBinding(schema); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
			t.Errorf("schema %q error = %v, want ErrUnregisteredPostgresIdentifier", schema, err)
		}
	}
	if err := (PostgresPopulationRootBinding{}).Validate(); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero binding error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	if _, err := RenderPopulationRootScan(populationPlan(1), mustBinding(t), PostgresPopulationRootBinding{}); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero membership binding error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	// A hand-assembled value is the zero value here: the only construction
	// route is the constructor.
	binding := populationBindingOK(t)
	if binding.schema != testSchema {
		t.Fatalf("constructed binding schema = %q", binding.schema)
	}
	if got, want := binding.tableSQL(membershipSelectionMembers), `"pgir02_it_unit"."selection_members"`; got != want {
		t.Fatalf("member table SQL = %q, want %q", got, want)
	}
}

// TestMembershipAllowlistsMatchMigrationsLiterally pins every registry
// column to the owning explorer migration: the expected list equals the
// registry, and every expected column is defined in the migration text as a
// NOT NULL column of its table.
func TestMembershipAllowlistsMatchMigrationsLiterally(t *testing.T) {
	cases := []struct {
		migration string
		table     postgresMembershipTableKind
		want      []string
	}{
		{"000008_selection_revisions.sql", membershipSelectionMembers,
			[]string{"generation", "id", "member_key", "ordinal", "project", "resource_type", "selection_id"}},
		{"000008_selection_revisions.sql", membershipSelectionRevisions,
			[]string{"complete", "generation", "id", "membership_digest", "project", "resource_type", "scope_digest"}},
		{"000009_explorer_explicit_groups.sql", membershipGroupRevisions,
			[]string{"generation", "id", "membership_digest", "project", "resource_type", "scope_digest", "source_membership_digest", "source_selection_revision_id", "state"}},
		{"000010_explorer_explicit_group_memberships.sql", membershipGroupMemberships,
			[]string{"generation", "group_id", "project", "resource_id", "resource_type", "revision_id"}},
	}
	for _, tc := range cases {
		spec, ok := postgresMembershipTables[tc.table]
		if !ok {
			t.Fatalf("registry missing relation %q", string(tc.table))
		}
		if spec.physicalTable != string(tc.table) {
			t.Fatalf("relation %q physical table %q drift", string(tc.table), spec.physicalTable)
		}
		var got []string
		for name := range spec.columns {
			got = append(got, name)
		}
		sortStrings(got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("relation %q columns = %v, want %v", string(tc.table), got, tc.want)
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "explorer", "postgres", "migrations", tc.migration))
		if err != nil {
			t.Fatalf("read %s: %v", tc.migration, err)
		}
		text := string(raw)
		if !strings.Contains(text, "CREATE TABLE IF NOT EXISTS "+spec.physicalTable+" (") {
			t.Fatalf("%s: table %q creation not found literally", tc.migration, spec.physicalTable)
		}
		for _, column := range tc.want {
			pattern := regexp.MustCompile(`(?m)^    ` + regexp.QuoteMeta(column) + ` +(text|bigint|boolean) +NOT NULL`)
			if !pattern.MatchString(text) {
				t.Fatalf("%s: column %q of %q is not a literal NOT NULL column", tc.migration, column, spec.physicalTable)
			}
		}
	}
	// The canonical collection strings are the exact lowered constants.
	for name, kind := range canonicalMembershipCollections {
		switch kind {
		case membershipSelectionMembers, membershipSelectionRevisions, membershipGroupRevisions, membershipGroupMemberships:
		default:
			t.Fatalf("collection %q maps to unregistered kind %q", name, string(kind))
		}
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// ---------------------------------------------------------------------------
// Golden SQL: population root source
// ---------------------------------------------------------------------------

func goldenString(v string) string { return v }

// argumentText renders an argument value for comparison, decoding JSON
// payloads back to their literal text.
func argumentText(value any) string {
	if raw, ok := value.(json.RawMessage); ok {
		return string(raw)
	}
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	return fmt.Sprintf("%v", value)
}

func TestPopulationSingleHopSQLIsExactAndParameterized(t *testing.T) {
	plan := populationPlan(1)
	plan.Operations = append(plan.Operations[:1],
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
			Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}},
		plan.Operations[1])
	rendered := renderPopulationOK(t, plan)
	wantSQL := goldenString(`SELECT r0."logical_id" AS "c0"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` INNER JOIN (SELECT DISTINCT x1."project", x1."dataset_generation", x1."resource_type", x1."logical_id"` +
		` FROM "pgir02_it_unit"."selection_members" AS pm` +
		` INNER JOIN "pgir02_it_unit"."fhir_resource" AS x0 ON x0."project" = $1 AND x0."dataset_generation" = $2` +
		` AND x0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND x0."resource_type" = $9` +
		` AND x0."logical_id" = pm."id"` +
		` INNER JOIN "pgir02_it_unit"."fhir_edge_occurrence" AS e1 ON e1."project" = $1 AND e1."dataset_generation" = $2` +
		` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND e1."label" = $10 AND e1."target_resource_type" = $9 AND e1."target_logical_id" = x0."logical_id"` +
		` AND e1."source_resource_type" = $4` +
		` INNER JOIN "pgir02_it_unit"."fhir_resource" AS x1 ON x1."project" = $1 AND x1."dataset_generation" = $2` +
		` AND x1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND x1."resource_type" = $4` +
		` AND x1."logical_id" = e1."source_logical_id"` +
		` WHERE pm."selection_id" = $5 AND pm."project" = $6 AND pm."generation" = $7 AND pm."resource_type" = $8)` +
		` AS pop ON pop."project" = r0."project" AND pop."dataset_generation" = r0."dataset_generation"` +
		` AND pop."resource_type" = r0."resource_type" AND pop."logical_id" = r0."logical_id"` +
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND r0."resource_type" = $4` +
		` ORDER BY r0."logical_id" ASC, r0."resource_seq" ASC`)
	if rendered.SQL != wantSQL {
		t.Fatalf("population SQL drift:\n got: %s\nwant: %s", rendered.SQL, wantSQL)
	}
	wantArgs := []struct {
		value any
	}{
		{"proj-a"}, {"gen-1"}, {`["FHIR/Patient","FHIR/Observation"]`}, {"Patient"}, {"selrev_7"},
		{"proj-a"}, {"gen-1"}, {"Observation"}, {"Observation"}, {"encounter-diagnosed_condition"},
	}
	if len(rendered.Args) != len(wantArgs) {
		t.Fatalf("argument count = %d, want %d: %#v", len(rendered.Args), len(wantArgs), rendered.Args)
	}
	for index, want := range wantArgs {
		got := argumentText(rendered.Args[index].Value)
		if got != fmt.Sprintf("%v", want.value) {
			t.Fatalf("argument %d = %v, want %v", index+1, got, want.value)
		}
	}
	// Determinism: the same plan renders byte-identical SQL and
	// value-identical arguments.
	second, err := RenderPopulationRootScan(plan, mustBinding(t), populationBindingOK(t))
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("population render is not deterministic: %v", err)
	}
}

func TestPopulationDirectSourceSQL(t *testing.T) {
	plan := populationPlan(0)
	// A zero-hop population root reads members as the rows themselves, so
	// the root collection is the member type (lower/population.go keeps
	// RootScan.CollectionBindKey pinned to the population resource type).
	plan.BindVars["root_collection"] = "Observation"
	rendered := renderPopulationOK(t, plan)
	if !strings.Contains(rendered.SQL, `SELECT DISTINCT x0."project", x0."dataset_generation", x0."resource_type", x0."logical_id"`) {
		t.Fatalf("zero-hop candidate must dedup the source identity:\n%s", rendered.SQL)
	}
	// Alive-control counterpart of the route battery: the plan renders and
	// carries exactly one resource join.
	if strings.Contains(rendered.SQL, "fhir_edge_occurrence") {
		t.Fatalf("zero-hop population root must not join the edge relation:\n%s", rendered.SQL)
	}
}

func TestPopulationCanonicalDenyReadsNothing(t *testing.T) {
	plan := populationPlan(1)
	plan.BindVars["scope_allowed"] = false
	rendered := renderPopulationOK(t, plan)
	for _, fragment := range []string{
		`x0."resource_type" = $9 AND FALSE`,
		`x1."resource_type" = $4 AND FALSE`,
		`e1."source_resource_type" = $4`,
		`r0."resource_type" = $4 AND FALSE`,
	} {
		if !strings.Contains(rendered.SQL, fragment) {
			t.Fatalf("canonical deny must close every scoped row chain (%q):\n%s", fragment, rendered.SQL)
		}
	}
	// The deny is shape-fixed, never a parameter.
	for _, argument := range rendered.Args {
		if fmt.Sprintf("%v", argument.Value) == "false" {
			t.Fatalf("deny must never bind a boolean parameter: %#v", rendered.Args)
		}
	}
}

func TestPopulationUnrestrictedAddsNoAuthPredicate(t *testing.T) {
	plan := populationPlan(1)
	plan.BindVars["auth_resource_paths_unrestricted"] = true
	plan.BindVars["auth_resource_paths"] = []string(nil)
	rendered := renderPopulationOK(t, plan)
	if strings.Contains(rendered.SQL, "auth_resource_path") {
		t.Fatalf("explicit unrestricted mode must drop every auth predicate:\n%s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Refusal battery: every typed refusal has an alive control (the same plan
// without the mutation renders via TestPopulation*/TestCohort* goldens).
// ---------------------------------------------------------------------------

func TestMembershipRootRefusals(t *testing.T) {
	cases := []struct {
		name    string
		builder func() ir.PhysicalPlan
	}{
		{"collect-members variable is deferred to pg-sql-22", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.Operations[0].RootScan.Population.CollectMembersVariable = "population_members"
			return plan
		}},
		{"member filters out of canonical order", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			f := plan.Operations[0].RootScan.Population.MemberFilters
			f[0], f[1] = f[1], f[0]
			return plan
		}},
		{"member filter missing", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.Operations[0].RootScan.Population.MemberFilters =
				plan.Operations[0].RootScan.Population.MemberFilters[:3]
			return plan
		}},
		{"member filter path altered", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.Operations[0].RootScan.Population.MemberFilters[0].Predicate.Left.Path = []string{"member_key"}
			return plan
		}},
		{"member filter binds its left side", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.Operations[0].RootScan.Population.MemberFilters[0].Predicate.Left =
				ir.PhysicalValue{BindKey: "population_selection_id"}
			return plan
		}},
		{"member filter compares non-string bind", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_selection_id"] = 42
			return plan
		}},
		{"member generation must bind the canonical dataset_generation", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_generation"] = "gen-1"
			plan.Operations[0].RootScan.Population.MemberFilters[2].Predicate.Right =
				&ir.PhysicalValue{BindKey: "population_generation"}
			return plan
		}},
		{"member collection must be a registered canonical relation", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_members_collection"] = "selection_members"
			return plan
		}},
		{"member collection must be a known collection", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_members_collection"] = "not_a_loom_collection"
			return plan
		}},
		{"member join predicate may not flip", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			filter := ops[5].Filter.Predicate
			filter.Left, *filter.Right = *filter.Right, filter.Left
			return plan
		}},
		{"source scan must read the member type", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_source_collection"] = "Condition"
			return plan
		}},
		{"scope block may rebind the project leg", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			*ops[1].Filter.Predicate.Right = ir.PhysicalValue{BindKey: "population_project"}
			return plan
		}},
		{"auth block may drop an input", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			ops[3].DerivedLet.Inputs = ops[3].DerivedLet.Inputs[:2]
			return plan
		}},
		{"auth result may filter a row instead of the LET result", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			ops[4].Filter.Predicate.Left = ir.PhysicalValue{Variable: "population_source", Path: []string{"project"}}
			return plan
		}},
		{"route step may anchor on the member row", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			ops[6].Traversal.SourceVariable = "population_member"
			return plan
		}},
		{"route step may not widen direction", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			ops[6].Traversal.Direction = ir.PhysicalAny
			return plan
		}},
		{"route step may not use endpoint lookup", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			ops[6].Traversal.Strategy = ir.PhysicalTraversalEndpointLookup
			ops[6].Traversal.EndpointIndexFields = []string{"_from"}
			return plan
		}},
		{"route step edge relation must be the canonical bind name", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_root_route_0_edge_collection"] = "fhir_edge_occurrence"
			return plan
		}},
		{"route target type must match the root type", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.BindVars["population_root_route_0_target_type"] = "Encounter"
			return plan
		}},
		{"root key must name the terminal row", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.Operations[0].RootScan.Population.RootKey.Variable = "population_root_node_1"
			return plan
		}},
		{"root key must be _key", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			plan.Operations[0].RootScan.Population.RootKey.Path = []string{"id"}
			return plan
		}},
		{"population payload may not carry a stray operation", func() ir.PhysicalPlan {
			plan := populationPlan(1)
			ops := plan.Operations[0].RootScan.Population.ResourceOperations
			plan.Operations[0].RootScan.Population.ResourceOperations = append(ops,
				ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}})
			return plan
		}},
		{"cohort collections must be registered canonical relations", func() ir.PhysicalPlan {
			plan := cohortPlan()
			plan.BindVars["cohort_selection_members_collection"] = "selection_members"
			return plan
		}},
		{"cohort collections must be known collections", func() ir.PhysicalPlan {
			plan := cohortPlan()
			plan.BindVars["cohort_memberships_collection"] = "random_table"
			return plan
		}},
		{"cohort policy must stay in the lowered domain", func() ir.PhysicalPlan {
			plan := cohortPlan()
			plan.BindVars["cohort_policy"] = "ERRORS"
			return plan
		}},
		{"cohort policy bind must exist", func() ir.PhysicalPlan {
			plan := cohortPlan()
			delete(plan.BindVars, "cohort_policy")
			return plan
		}},
		{"cohort revision id must be a nonblank string", func() ir.PhysicalPlan {
			plan := cohortPlan()
			plan.BindVars["cohort_revision_id"] = "  "
			return plan
		}},
	}
	for _, tc := range cases {
		plan := tc.builder()
		_, err := RenderPopulationRootScan(plan, mustBinding(t), populationBindingOK(t))
		if err == nil {
			t.Fatalf("%s: mutation accepted, want typed refusal", tc.name)
		}
		if !errors.Is(err, ErrUnsupportedPostgresPlan) && !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
			// Plan-level ir validation may reject first; the renderer must
			// still have failed closed. Only the two typed gates qualify.
			if !strings.Contains(err.Error(), "validate physical plan") {
				t.Fatalf("%s: error = %v (%T), want a typed gate", tc.name, err, err)
			}
		}
	}
	// Alive controls: the canonical shapes render, so no refusal above is
	// vacuous.
	if _, err := RenderPopulationRootScan(populationPlan(1), mustBinding(t), populationBindingOK(t)); err != nil {
		t.Fatalf("alive control (population) refused: %v", err)
	}
	if _, err := RenderPopulationRootScan(cohortPlan(), mustBinding(t), populationBindingOK(t)); err != nil {
		t.Fatalf("alive control (cohort) refused: %v", err)
	}
}

func TestPlainSpineKeepsTheBindingGate(t *testing.T) {
	// The membership entry requires a membership root: a plain spine is a
	// typed refusal instead of silently rendering.
	if _, err := RenderPopulationRootScan(basePlan(), mustBinding(t), populationBindingOK(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("plain spine via membership entry error = %v, want ErrUnsupportedPostgresPlan", err)
	}
	// The unbound spine entry keeps refusing membership roots (and the seed)
	// exactly as before pg-sql-18: the fixture's typed-refusal tolerance
	// must not widen.
	plan := populationPlan(1)
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("population without binding error = %v, want ErrUnsupportedPostgresPlan", err)
	}
	// Candidate seed stays refused on BOTH entries, at the RENDER gate: the
	// seed carries a structurally valid uncorrelated subplan (an empty one
	// would die earlier at plan.Validate, proving nothing about the split
	// branch), so the refusal under test is renderRootScan's.
	seeded := basePlan()
	seeded.BindVars["seed_collection"] = "Observation"
	seedValue := ir.PhysicalValue{Variable: "seed_scan", Path: []string{"logical_id"}}
	seeded.Operations[0].RootScan.PageCandidateSeed = &ir.PhysicalRootPageCandidateSeed{
		Subplan: ir.PhysicalSubplan{
			Operations: []ir.PhysicalOperation{
				{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{
					Variable: "seed_scan", CollectionBindKey: "seed_collection"}},
			},
			Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression,
				Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
				Value: &seedValue},
		},
	}
	if _, err := RenderPhysicalPlan(seeded, mustBinding(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("candidate seed via spine error = %v, want ErrUnsupportedPostgresPlan (render gate, not ir)", err)
	}
	if _, err := RenderPopulationRootScan(seeded, mustBinding(t), populationBindingOK(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("candidate seed via membership entry error = %v, want ErrUnsupportedPostgresPlan (render gate, not ir)", err)
	}
}

func TestCohortRootSourceSQLIsExactAndParameterized(t *testing.T) {
	rendered := renderPopulationOK(t, cohortPlan())
	if !strings.Contains(rendered.SQL,
		`INNER JOIN (SELECT sm1."id" AS "candidate_id" FROM "pgir02_it_unit"."selection_members" sm1 WHERE sm1."selection_id" = (SELECT gr2."source_selection_revision_id" FROM "pgir02_it_unit"."explorer_explicit_group_revisions" gr2 WHERE gr2."id" = $5)`) {
		t.Fatalf("cohort candidate selection side drifted:\n%s", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL,
		`UNION SELECT mm1."resource_id" FROM "pgir02_it_unit"."explorer_explicit_group_memberships" mm1 WHERE mm1."revision_id" = $5`) {
		t.Fatalf("cohort candidate membership side drifted:\n%s", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL,
		`(SELECT CASE WHEN NOT (EXISTS (SELECT 1 FROM "pgir02_it_unit"."explorer_explicit_group_revisions" gr0 WHERE gr0."id" = $5 AND gr0."state" = 'COMPLETE'`) {
		t.Fatalf("cohort revision guard drifted:\n%s", rendered.SQL)
	}
	for _, fragment := range []string{
		`CAST('EXPLICIT_GROUP_REVISION_INVALID' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer)`,
		`CAST('EXPLICIT_GROUP_SOURCE_STALE' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer)`,
		`CAST('EXPLICIT_GROUP_UNASSIGNED_MEMBER' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer)`,
		`AND NOT EXISTS (SELECT 1 FROM "pgir02_it_unit"."explorer_explicit_group_memberships" mm0 WHERE mm0."revision_id" = $5`,
		`$9 = 'ERROR'`,
		`) = 1`,
		`r0."project" = $1 AND r0."dataset_generation" = $7 AND r0."resource_type" = $8`,
		`cand."candidate_id" = r0."logical_id"`,
		`ORDER BY r0."resource_seq" ASC`,
	} {
		if !strings.Contains(rendered.SQL, fragment) {
			t.Fatalf("cohort SQL missing fragment %q:\n%s", fragment, rendered.SQL)
		}
	}
	if strings.Contains(rendered.SQL, "@") {
		t.Fatalf("cohort SQL carries AQL bind syntax:\n%s", rendered.SQL)
	}
	// The guard is one InitPlan conjunct, evaluated once (uncorrelated: no
	// outer reference inside the CASE subquery).
	if strings.Contains(rendered.SQL, "r0.\"logical_id\") THEN") {
		t.Fatalf("guard must stay uncorrelated:\n%s", rendered.SQL)
	}
	wantArgs := []string{"proj-a", "gen-1", `["FHIR/Patient","FHIR/Observation"]`, "Patient",
		"grouprev_1", "proj-a", "gen-1", "Patient", "EXCLUDE"}
	if len(rendered.Args) != len(wantArgs) {
		t.Fatalf("cohort argument count = %d, want %d: %#v", len(rendered.Args), len(wantArgs), rendered.Args)
	}
	for index, want := range wantArgs {
		if got := argumentText(rendered.Args[index].Value); got != want {
			t.Fatalf("cohort argument %d = %q, want %q", index+1, got, want)
		}
	}
	if strings.Contains(rendered.SQL, "$10") {
		t.Fatalf("cohort render must reuse parameters, not mint new ones:\n%s", rendered.SQL)
	}
	second, err := RenderPopulationRootScan(cohortPlan(), mustBinding(t), populationBindingOK(t))
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("cohort render is not deterministic: %v", err)
	}
}

func TestCohortDenyAndUnrestrictedScope(t *testing.T) {
	denied := cohortPlan()
	denied.BindVars["scope_allowed"] = false
	rendered := renderPopulationOK(t, denied)
	// Under the canonical deny the guard clause IS the deny: one atomic
	// always-false clause "(<guard sublink>) * 0 = 1". No const-FALSE
	// conjunct may exist anywhere (eval_const_expressions folds the whole
	// AND tree and prunes the guard InitPlans) and no separate stable-false
	// conjunct may exist either (cost-sorted hoisting short-circuits the
	// lazily-fetched guard before it runs) -- deny must never suppress
	// guard violations, matching AQL, which raises before the deny FILTER
	// (render/aql/group_rows.go:245-261; pinned live by
	// TestCohortLiveDenyStillRaisesGuards).
	if !strings.Contains(rendered.SQL, `ELSE 1 END) * 0 = 1)`) {
		t.Fatalf("cohort deny must merge into the always-false guard clause:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, " AND FALSE") {
		t.Fatalf("cohort deny must never fold to a literal FALSE conjunct:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, " = 1 AND") || strings.Contains(rendered.SQL, " = 1 WHERE") ||
		strings.HasSuffix(rendered.SQL, " = 1") || strings.Contains(rendered.SQL, " = 1 ORDER BY") {
		t.Fatalf("denied cohort must not keep a separate guard-equals-one conjunct:\n%s", rendered.SQL)
	}
	// The deny stays shape-fixed: never a bind parameter.
	for _, argument := range rendered.Args {
		if fmt.Sprintf("%v", argument.Value) == "false" {
			t.Fatalf("cohort deny must never bind a boolean parameter: %#v", rendered.Args)
		}
	}
	// The membership relations carry no auth column and never did in Arango
	// (group_rows.go:252-259): auth lives on source rows only.
	unrestricted := cohortPlan()
	unrestricted.BindVars["auth_resource_paths_unrestricted"] = true
	unrestricted.BindVars["auth_resource_paths"] = []string(nil)
	rendered = renderPopulationOK(t, unrestricted)
	if strings.Contains(rendered.SQL, "auth_resource_path") {
		t.Fatalf("explicit unrestricted cohort root must not add auth predicates:\n%s", rendered.SQL)
	}
}
