package postgres

// population.go registers the population and cohort-scoped root sources as
// implemented, closed PostgreSQL operations (pg-sql-18). It is the single
// authority in this package for the two membership-driven ROOT_SCAN payloads
// the backend-neutral plan carries on PhysicalRootScan:
//
//   - Population (selection-bounded) root — the AQL counterpart is the
//     Population branch of render/aql/render.go renderRootScan (lines
//     679-755), the sole producer of that shape: a FOR loop over the pinned
//     selection-members relation with the four canonical member filters
//     (lower/population.go:44-56), an INNER source scan joined on the member
//     logical id, the compiler-owned reverse route rendered with
//     renderTraversalScan (render/aql/render.go:721 →
//     render/aql/navigation_render.go:39-61) — the INNER FOR-loop form, NOT
//     the renderTraversalSet LET-set OPTIONAL form (navigation_render.go:11-37,
//     spine-only) — which is the same composition renderUnnest uses for owner
//     routes (navigation_render.go:159) and therefore the same PostgreSQL
//     contract pg-sql-06 registered as the routeInner INNER edge×target join;
//     then COLLECT rootKey dedup (render.go:743) and the DOCUMENT-based root
//     restore + null filter (render.go:751-754), which become a DISTINCT
//     candidate subquery joined to the root resource row on the full typed
//     identity.
//   - Cohort (pinned group-revision) root — the AQL counterpart is
//     renderCohortRootScan (render/aql/group_rows.go:226-271), the sole
//     producer: COMPLETE-revision and unexpired-source-selection ASSERT
//     guards, the revision-scoped membership scan (whose Arango ref
//     self-consistency filter, group_rows.go:254, is a table invariant in
//     PostgreSQL: migration 000010 stores one column set, not a ref
//     duplicate), the unassigned-member anti-semijoin over
//     (project, generation, resourceType, id) (group_rows.go:256-259), the
//     ERROR policy guard (group_rows.go:260-261), UNION_DISTINCT candidate
//     ids (group_rows.go:262-265), and the root loop filtered by logical id,
//     resource-storage project, dataset generation, and resource type
//     (group_rows.go:266-268) whose auth leg is the plan-canonical scope
//     block (the scope.go contract, lines 10-21) and therefore structural
//     here.
//
// Membership-relation trust: the plan carries the compiler-pinned Arango
// collection names (lower/group_rows.go:33-37; arango/collections.go:12) on
// the payload bind keys. They are re-read and must match the closed registry
// below exactly (the traversal.go:170 "fhir_edge" precedent); every SQL
// identifier for the four state relations comes exclusively from this file's
// registry through PostgresPopulationRootBinding, whose only constructor is
// NewPostgresPopulationRootBinding. Registry names and column sets are pinned
// literally against internal/explorer/postgres/migrations/000008, 000009, and
// 000010 by TestMembershipAllowlistsMatchMigrationsLiterally; a migration
// rename fails closed. Plan strings never become SQL text.
//
// Exactness decisions D1-D5 (self-drafted in this ticket's plan; the plan
// as a whole was ratified by the coordinator on 2026-11-10):
//
//   - D1: candidate sets deduplicate via SELECT DISTINCT over the full typed
//     identity (population) and UNION over logical ids (cohort). No AQL
//     frontier COLLECT equivalent is required: the output rows are
//     fhir_resource rows joined on (project, dataset_generation,
//     resource_type, logical_id), which migration 000002 pins UNIQUE, so
//     every route multiplicity collapses into the candidate dedup and no
//     duplicate exact root identity can reach the output.
//   - D2: CollectMembersVariable must be empty. The member-witness branch
//     (render.go:744-749) belongs to the pg-sql-22 population terminal and
//     keeps a typed refusal here.
//   - D3: cohort guard violations are query errors, not silent empty results.
//     The three guards render as one uncorrelated scalar InitPlan conjunct —
//     a CASE whose violating arm casts the canonical ASSERT tag text to
//     integer (SQLSTATE 22P02, tag embedded in the driver-visible message).
//     Uncorrelated scalar subqueries are evaluated once at executor
//     initialization, so the guards fire exactly once independently of the
//     candidate/root row count, mirroring ASSERT placement outside the AQL
//     root loop. CASE order matches Arango ASSERT order: revision → stale →
//     unassigned.
//   - D4: the canonical member-relation predicate is
//     selection_id = $n AND project = $n AND generation = $n AND
//     resource_type = $n, re-read from the four canonical MemberFilters in
//     lowered order; the generation filter must bind the plan's canonical
//     "dataset_generation" key with the plan's generation value.
//   - D5: every resource and edge row inside the subqueries carries the
//     structural scope.go predicate (canonical deny included). The canonical
//     scope blocks lower/population.go attaches inside ResourceOperations
//     (appendProjectScope/appendDatasetGenerationScope/appendAuthScope,
//     lower/plan_orchestration.go:227-277) are re-read and ignored: they must
//     match the canonical shape exactly (the shape
//     ir/navigation.go validateGenericNavigationScopeBlock pins) or the plan
//     is refused; they never become predicates, because scope.go is the
//     single scope authority.
//
// Everything outside this closed surface — PageCandidateSeed (still refused
// by render.go's renderRootScan), member witnesses (D2), ANY-direction or
// endpoint-lookup route steps (validateTraversalContract), UNNEST on a
// membership root (validateUnnestRootScan), plan-authored scope deviations,
// unregistered collection strings, hand-assembled bindings, and any plan
// reaching these roots without an explicit PostgresPopulationRootBinding —
// fails closed with a typed error and yields no SQL. There is no fallback to
// the AQL renderer and no AQL-to-SQL conversion.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// ---------------------------------------------------------------------------
// Closed registry of the four control-plane membership relations.
// ---------------------------------------------------------------------------

// postgresMembershipTableKind names one state relation of the Explorer
// control-plane schema. The set is closed and mirrors the owning migrations
// in internal/explorer/postgres/migrations literally.
type postgresMembershipTableKind string

const (
	membershipSelectionMembers   postgresMembershipTableKind = "selection_members"
	membershipSelectionRevisions postgresMembershipTableKind = "selection_revisions"
	membershipGroupRevisions     postgresMembershipTableKind = "explorer_explicit_group_revisions"
	membershipGroupMemberships   postgresMembershipTableKind = "explorer_explicit_group_memberships"
)

// canonicalMembershipCollections maps each payload collection-bind value the
// compiler-pinned lowering emits (lower/group_rows.go:33-37; the population
// member collection is runtime-bound from arango.SelectionMembersCollection,
// internal/explorer/arango/collections.go:12) to its registered state
// relation. The map is validation-only: plan strings select a registry entry,
// they never reach SQL text.
var canonicalMembershipCollections = map[string]postgresMembershipTableKind{
	"loom_explorer_selection_members":          membershipSelectionMembers,
	"loom_explorer_selections":                 membershipSelectionRevisions,
	"loom_explorer_explicit_group_revisions":   membershipGroupRevisions,
	"loom_explorer_explicit_group_memberships": membershipGroupMemberships,
}

type postgresMembershipTableSpec struct {
	physicalTable string
	columns       map[string]struct{}
}

func membershipColumnSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// postgresMembershipTables is the compiler-owned column allowlist, taken
// literally from the owning migrations (population_test.go pins every entry
// against the migration text).
var postgresMembershipTables = map[postgresMembershipTableKind]postgresMembershipTableSpec{
	membershipSelectionMembers: {
		physicalTable: "selection_members",
		columns: membershipColumnSet(
			"selection_id", "member_key", "project", "generation",
			"resource_type", "id", "ordinal",
		),
	},
	membershipSelectionRevisions: {
		physicalTable: "selection_revisions",
		columns: membershipColumnSet(
			"id", "project", "generation", "resource_type",
			"scope_digest", "membership_digest", "complete",
		),
	},
	membershipGroupRevisions: {
		physicalTable: "explorer_explicit_group_revisions",
		columns: membershipColumnSet(
			"id", "project", "generation", "resource_type", "scope_digest",
			"source_selection_revision_id", "source_membership_digest",
			"membership_digest", "state",
		),
	},
	membershipGroupMemberships: {
		physicalTable: "explorer_explicit_group_memberships",
		columns: membershipColumnSet(
			"revision_id", "group_id", "project", "generation",
			"resource_type", "resource_id",
		),
	},
}

// membershipSchemaPattern is the same schema-name contract
// ir/postgres_storage.go enforces for the source store (its private
// postgresSchemaNamePattern): a lowercase owner-chosen schema name, so a name
// that survived construction can be emitted quoted without the quoting rules
// defending against embedded quotes.
var membershipSchemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// quoteMembershipIdentifier doubles embedded quotes for registry-owned names
// only (every caller passes a compile-time constant or a name that already
// passed membershipSchemaPattern), mirroring ir.quotePostgresIdentifier.
func quoteMembershipIdentifier(name string) string {
	out := make([]byte, 0, len(name)+2)
	out = append(out, '"')
	for index := 0; index < len(name); index++ {
		if name[index] == '"' {
			out = append(out, '"')
		}
		out = append(out, name[index])
	}
	out = append(out, '"')
	return string(out)
}

// PostgresPopulationRootBinding binds the four membership relations into one
// validated schema for the population/cohort root sources. The only way to
// obtain one is NewPostgresPopulationRootBinding; the zero value and any
// hand-assembled value fail Validate with an
// ir.ErrUnregisteredPostgresIdentifier-classified error and are refused by
// every entry point. Deployments co-locate the control plane and the source
// store in one schema (the merged migration tests apply both histories to a
// single schema, explorer/postgres/explicit_groups_test.go:1905-1908); the
// schema stays a validated constructor parameter.
type PostgresPopulationRootBinding struct {
	schema string
}

// NewPostgresPopulationRootBinding validates the owner-chosen schema that
// carries every registry relation. The binding is validated by construction.
func NewPostgresPopulationRootBinding(schema string) (PostgresPopulationRootBinding, error) {
	if !membershipSchemaPattern.MatchString(schema) {
		return PostgresPopulationRootBinding{}, fmt.Errorf("%w: membership schema name %q must match [a-z_][a-z0-9_]{0,62}",
			ir.ErrUnregisteredPostgresIdentifier, schema)
	}
	return PostgresPopulationRootBinding{schema: schema}, nil
}

// Validate reports whether the binding is a constructed, registered binding.
// The zero value and any hand-assembled value fail.
func (b PostgresPopulationRootBinding) Validate() error {
	if !membershipSchemaPattern.MatchString(b.schema) {
		return fmt.Errorf("%w: membership binding schema %q is not registered",
			ir.ErrUnregisteredPostgresIdentifier, b.schema)
	}
	return nil
}

// tableSQL returns the trusted, quoted identifier of one registry relation.
func (b PostgresPopulationRootBinding) tableSQL(kind postgresMembershipTableKind) string {
	spec, ok := postgresMembershipTables[kind]
	if !ok {
		panic(fmt.Sprintf("population registry: unknown membership table kind %q", string(kind)))
	}
	return quoteMembershipIdentifier(b.schema) + "." + quoteMembershipIdentifier(spec.physicalTable)
}

// columnSQL resolves one column against the registry allowlist and returns
// its quoted SQL text. An unallowlisted name is a compiler bug (every call
// site names a literal the owning migration defines), mirroring MustColumn.
func (b PostgresPopulationRootBinding) columnSQL(kind postgresMembershipTableKind, column string) string {
	spec, ok := postgresMembershipTables[kind]
	if !ok {
		panic(fmt.Sprintf("population registry: unknown membership table kind %q", string(kind)))
	}
	if _, allowed := spec.columns[column]; !allowed {
		panic(fmt.Sprintf("unregistered postgres identifier: column %q is not a column of membership relation %q",
			column, string(kind)))
	}
	return quoteMembershipIdentifier(column)
}

// ---------------------------------------------------------------------------
// Canonical constants re-read by the two renderers.
// ---------------------------------------------------------------------------

const (
	// cohortGuardTags are the canonical Arango ASSERT tags
	// (render/aql/group_rows.go:247,250,260). A violating arm casts the tag
	// text to integer: SQLSTATE 22P02 with the tag embedded in the
	// driver-visible message. Error text is not part of the data-plane
	// contract; the firing semantics are (D3).
	cohortGuardRevisionInvalid  = "EXPLICIT_GROUP_REVISION_INVALID"
	cohortGuardSourceStale      = "EXPLICIT_GROUP_SOURCE_STALE"
	cohortGuardUnassignedMember = "EXPLICIT_GROUP_UNASSIGNED_MEMBER"

	// cohortStateComplete and cohortPolicyError are structural renderer text,
	// not plan data — exactly as the AQL renderer spells them inline
	// (group_rows.go:247,260). The policy value itself stays plan data and
	// stays a $n parameter.
	cohortStateComplete = "COMPLETE"
	cohortPolicyError   = "ERROR"
)

// canonicalCohortPolicies is the closed UnassignedMemberPolicy domain
// (lower/group_rows.go:31, recipe/document_validation.go:183-188). Only
// ERROR changes root-source behavior (the guard); EXCLUDE and
// GROUP_AS_UNASSIGNED differ at the group-stage wave (pg-sql-16/19).
var canonicalCohortPolicies = map[string]bool{
	"ERROR": true, "EXCLUDE": true, "GROUP_AS_UNASSIGNED": true,
}

// populationMemberFilterOrder is the lowered member-filter order of
// lower/population.go:44-56 (selectionId, project, generation, resourceType),
// each EQUALS against one bind. The generation filter must bind the plan's
// canonical "dataset_generation" key and carry the plan's generation value
// (D4): the pinned membership set is generation-scoped to the plan.
var populationMemberFilterOrder = []struct {
	memberColumn  string
	planPath      string
	canonicalBind string // "" = any bind name; the value is checked instead
	nonEmpty      bool
}{
	{"selection_id", "selectionId", "", true},
	{"project", "project", "", true},
	{"generation", "generation", datasetGenerationBind, false},
	{"resource_type", "resourceType", "", true},
}

// ---------------------------------------------------------------------------
// Registered entry points.
// ---------------------------------------------------------------------------

// ValidatePopulationRootSource checks that plan carries exactly one leading
// ROOT_SCAN whose membership payload is inside the closed registered slice
// for binding, without rendering. It is the executor-facing capability gate:
// a nil error means RenderPopulationRootScan accepts the shape, and every
// failure is classified as ErrUnsupportedPostgresPlan or
// ErrInvalidPostgresPlanScope.
func ValidatePopulationRootSource(plan ir.PhysicalPlan, binding PostgresPopulationRootBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate physical plan: %w", err)
	}
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	root := plan.Operations[0]
	if root.Kind != ir.PhysicalRootScanOp || root.RootScan == nil {
		return fmt.Errorf("%w: the leading operation must be ROOT_SCAN with its payload set", ErrUnsupportedPostgresPlan)
	}
	scan := *root.RootScan
	if scan.Population == nil && scan.CohortSource == nil {
		return fmt.Errorf("%w: the plan carries no membership root source; the direct root scan is registered by root_scan.go, not this slice",
			ErrUnsupportedPostgresPlan)
	}
	if scan.Population != nil && scan.CohortSource != nil {
		return fmt.Errorf("%w: root scan cannot combine cohort and population sources", ErrUnsupportedPostgresPlan)
	}
	if scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: membership roots cannot combine with the candidate seed, which stays unregistered",
			ErrUnsupportedPostgresPlan)
	}
	if err := requireMembershipRootBinds(scan); err != nil {
		return err
	}
	if scan.Population != nil {
		_, err := validatePopulationPayload(plan, scan)
		return err
	}
	return validateCohortPayload(plan, scan)
}

// requireMembershipRootBinds pins the payload identity fields shared by both
// root sources before their payload-specific walkers dereference payloads.
func requireMembershipRootBinds(scan ir.PhysicalRootScan) error {
	if strings.TrimSpace(scan.Variable) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(scan.CollectionBindKey) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	return nil
}

// RenderPopulationRootScan renders one plan whose leading ROOT_SCAN carries a
// registered population or cohort root source onto the trusted storage
// binding plus the trusted membership binding. It is the capability
// registration point named by pg-sql-18: RenderPhysicalPlan itself keeps
// refusing membership roots unless the plan renderer carries a binding (only
// this entry provides one), so the closed spine surface and the
// planner-facing fail-closed gate are unchanged.
//
// The result is deterministic (byte-identical SQL, value-identical ordered
// arguments for one plan and bindings), parameterized (every value is $n),
// and free of AQL bind syntax. Rejections are typed and yield no SQL;
// nothing falls back to AQL.
func RenderPopulationRootScan(
	plan ir.PhysicalPlan,
	storage ir.PostgresStorageBinding,
	binding PostgresPopulationRootBinding,
) (compiler.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	if err := ValidatePopulationRootSource(plan, binding); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	// The gates below mirror RenderPhysicalPlan exactly (engine, ClickHouse,
	// staged sequences, preview windowing, deferred lets, and the UNNEST
	// surface pin whose validateUnnestRootScan keeps membership roots out of
	// the unnest slice). The dispatch line in render.go routes any plan that
	// reaches the shared spine with a bound renderer through the same
	// renderRootSources code; this entry stays the only binder.
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			if err := validateUnnestPlanSurface(plan); err != nil {
				return compiler.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: staged row-operator sequences are not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: preview source windowing is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}
	bound := binding
	renderer := &planRenderer{
		storage:    storage,
		rows:       map[string]rowScope{},
		parameters: map[string]int{},
		membership: &bound,
	}
	if err := renderer.loadScope(plan); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	if err := renderer.renderOperations(plan); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	return compiler.NewRenderedPostgresPhysicalPlan(renderer.assemble(), renderer.args, renderer.outputColumns)
}

// renderRootSources is the dispatch target of render.go's single
// population/cohort line. Without a binding (every RenderPhysicalPlan path
// that did not enter through RenderPopulationRootScan) it keeps the merged
// fail-closed refusal in kind: membership roots stay a capability gap, never
// an AQL fallback.
func (r *planRenderer) renderRootSources(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil && scan.CohortSource != nil {
		return fmt.Errorf("%w: root scan cannot combine cohort and population sources", ErrUnsupportedPostgresPlan)
	}
	if scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: membership roots cannot combine with the candidate seed, which stays unregistered",
			ErrUnsupportedPostgresPlan)
	}
	if r.membership == nil {
		return fmt.Errorf("%w: population and cohort roots require the membership binding registered by RenderPopulationRootScan; the plain spine keeps them a capability gap",
			ErrUnsupportedPostgresPlan)
	}
	if err := r.membership.Validate(); err != nil {
		return err
	}
	// Bind the root row exactly as the direct scan does (identical structural
	// scope, identical rootAlias), then layer the membership subqueries.
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	r.rootAlias = "r0"
	r.rows[scan.Variable] = rowScope{alias: r.rootAlias, resourceType: resourceType}
	r.where = append(r.where, r.scopedResourcePredicate(r.rootAlias,
		r.textParameter(resourceType, "text:"+resourceType)))
	if scan.Population != nil {
		return r.renderPopulationRootSource(plan, scan)
	}
	return r.renderCohortRootSource(plan, scan)
}

// ---------------------------------------------------------------------------
// Population (selection-bounded) root source.
// ---------------------------------------------------------------------------

// populationRootShape is one fully re-read population payload: only facts
// the renderer needs, each re-derived from the plan at render time.
type populationRootShape struct {
	memberVariable   string
	sourceVariable   string
	sourceType       string
	terminalVariable string
	memberBindKeys   [4]string
}

// validatePopulationPayload re-reads the closed population payload: the
// member scan, exactly the four canonical member filters in lowered order,
// the resource-operation sequence lower/population.go emits (source scan,
// canonical scope block, member-id join, then per route step a depth-one
// traversal plus its canonical scope block), and the terminal root key.
// Every deviation is a typed refusal; the canonical scope ops are recognized
// and never rendered (D5 — scope.go is the single scope authority).
func validatePopulationPayload(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) (populationRootShape, error) {
	var shape populationRootShape
	population := scan.Population
	if population.CollectMembersVariable != "" {
		return shape, fmt.Errorf("%w: population member witnesses (CollectMembersVariable %q) belong to the pg-sql-22 population terminal and are not registered by pg-sql-18 (D2)",
			ErrUnsupportedPostgresPlan, population.CollectMembersVariable)
	}
	shape.memberVariable = population.MemberScan.Variable
	if shape.memberVariable == "" {
		return shape, fmt.Errorf("%w: population member scan must name its variable", ErrUnsupportedPostgresPlan)
	}
	if err := requireMembershipCollection(plan, population.MemberScan.CollectionBindKey, membershipSelectionMembers); err != nil {
		return shape, err
	}
	if len(population.MemberFilters) != len(populationMemberFilterOrder) {
		return shape, fmt.Errorf("%w: population member scan must carry exactly the %d canonical member filters of lower/population.go, got %d",
			ErrUnsupportedPostgresPlan, len(populationMemberFilterOrder), len(population.MemberFilters))
	}
	if err := matchMemberFilters(plan, population, &shape); err != nil {
		return shape, err
	}

	// Resource operations: [COLLECTION_SCAN, source scope block, member-id
	// join, (TRAVERSAL, route scope block)*] — the exact
	// lower/population.go:59-101 emission.
	operations := population.ResourceOperations
	if len(operations) == 0 || operations[0].Kind != ir.PhysicalCollectionScanOp || operations[0].CollectionScan == nil {
		return shape, fmt.Errorf("%w: population resource operations must begin with the single source COLLECTION_SCAN of lower/population.go:59",
			ErrUnsupportedPostgresPlan)
	}
	sourceScan := operations[0].CollectionScan
	shape.sourceVariable = sourceScan.Variable
	if shape.sourceVariable == "" || shape.sourceVariable == shape.memberVariable {
		return shape, fmt.Errorf("%w: population source scan must name its own variable", ErrUnsupportedPostgresPlan)
	}
	sourceType, ok := plan.BindVars[sourceScan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(sourceType) == "" {
		return shape, fmt.Errorf("%w: population source collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, sourceScan.CollectionBindKey)
	}
	shape.sourceType = sourceType

	index := 1
	if err := matchCanonicalScopeBlock(operations, &index, []string{shape.sourceVariable}); err != nil {
		return shape, fmt.Errorf("population source scope block: %w: %w", ErrUnsupportedPostgresPlan, err)
	}
	// Member-id join: source.id == member.id, exactly the lowered predicate
	// against Population.MemberID (lower/population.go:63-64).
	if index >= len(operations) || operations[index].Kind != ir.PhysicalFilterOp || operations[index].Filter == nil {
		return shape, fmt.Errorf("%w: population resource operations must carry the source-id/member-id join FILTER after the source scope block",
			ErrUnsupportedPostgresPlan)
	}
	memberID := population.MemberID
	if memberID.Variable != shape.memberVariable || len(memberID.Path) != 1 || memberID.Path[0] != "id" || memberID.BindKey != "" {
		return shape, fmt.Errorf("%w: population MemberID must be the member-variable logical id", ErrUnsupportedPostgresPlan)
	}
	if operations[index].Filter.Expression != nil ||
		!sameCanonicalPredicate(operations[index].Filter.Predicate,
			ir.PhysicalValue{Variable: shape.sourceVariable, Path: []string{"id"}},
			ir.PhysicalValue{Variable: memberID.Variable, Path: []string{"id"}}) {
		return shape, fmt.Errorf("%w: population source-id/member-id join must be exactly %s.id == %s.id (EQUALS, no quantifier or correlation)",
			ErrUnsupportedPostgresPlan, shape.sourceVariable, shape.memberVariable)
	}
	index++

	// Reverse route steps: TRAVERSAL anchors on the previous reached row and
	// closes with its canonical scope block. Types are tracked so the
	// traversal contract probe sees the real anchored type.
	terminalVariable := shape.sourceVariable
	terminalType := sourceType
	for step := 0; index < len(operations); step++ {
		operation := operations[index]
		if operation.Kind != ir.PhysicalTraversalOp || operation.Traversal == nil {
			return shape, fmt.Errorf("%w: population resource operation %d has kind %q; the route is a TRAVERSAL followed by its canonical scope block",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
		traversal := *operation.Traversal
		if traversal.SourceVariable != terminalVariable {
			return shape, fmt.Errorf("%w: population route step %d must anchor on %q (the source scan or the previous step's reached row), got %q",
				ErrUnsupportedPostgresPlan, index, terminalVariable, traversal.SourceVariable)
		}
		verified, err := probeTraversalContract(plan, traversal, rowScope{resourceType: terminalType})
		if err != nil {
			return shape, fmt.Errorf("population route step %d: %w: %w", index, ErrUnsupportedPostgresPlan, err)
		}
		index++
		if err := matchCanonicalScopeBlock(operations, &index, []string{traversal.EdgeVariable, traversal.TargetVariable}); err != nil {
			return shape, fmt.Errorf("population route step %d scope block: %w: %w", index, ErrUnsupportedPostgresPlan, err)
		}
		terminalVariable = traversal.TargetVariable
		terminalType = verified.targetType
	}
	if population.RootKey.Variable != terminalVariable ||
		len(population.RootKey.Path) != 1 || population.RootKey.Path[0] != "_key" ||
		population.RootKey.BindKey != "" {
		return shape, fmt.Errorf("%w: population RootKey must be the final route row's storage identity (the terminal _key of lower/population.go:102), got variable %q path %v",
			ErrUnsupportedPostgresPlan, population.RootKey.Variable, population.RootKey.Path)
	}
	// The reverse route terminates at the root type (lower/population.go:66-73).
	if rootType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(rootType) == "" {
		return shape, fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	} else if rootType != terminalType {
		return shape, fmt.Errorf("%w: population root collection type %q must match the final route row type %q",
			ErrUnsupportedPostgresPlan, rootType, terminalType)
	}
	shape.terminalVariable = terminalVariable
	return shape, nil
}

// probeTraversalContract runs validateTraversalContract on a throwaway
// renderer whose parameter sink is discarded, so entry-facing validation
// checks the same contract the renderer enforces (direction,
// direction-aware discriminators, the registered edge collection, label and
// type binds, native strategy) without shifting the main render's parameter
// order. validateTraversalContract reads only plan binds and the anchored
// row's resource type — never storage — so the probe is faithful.
func probeTraversalContract(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal, source rowScope) (validatedTraversal, error) {
	probe := &planRenderer{parameters: map[string]int{}}
	return probe.validateTraversalContract(plan, traversal, source)
}

// matchMemberFilters re-reads the four canonical member filters in lowered
// order (selectionId, project, generation, resourceType): EQUALS on the
// member-variable path against one bind, Predicate form, no quantifier,
// correlation, selector left side, or path on the right. The generation
// filter must bind the canonical "dataset_generation" key and carry the
// plan's generation value (D4). The positional bind keys are recorded on the
// shape for the renderer.
func matchMemberFilters(plan ir.PhysicalPlan, population *ir.PhysicalPopulationRootSource, shape *populationRootShape) error {
	for position, expected := range populationMemberFilterOrder {
		filter := population.MemberFilters[position]
		if filter.Expression != nil {
			return fmt.Errorf("%w: member filter %d must use the canonical Predicate form of lower/population.go", ErrUnsupportedPostgresPlan, position)
		}
		predicate := filter.Predicate
		if !strings.EqualFold(strings.TrimSpace(predicate.Operator), "EQUALS") ||
			predicate.Left.Variable != population.MemberScan.Variable ||
			len(predicate.Left.Path) != 1 || predicate.Left.Path[0] != expected.planPath ||
			predicate.Left.BindKey != "" ||
			predicate.Right == nil || predicate.Right.BindKey == "" ||
			predicate.Right.Variable != "" || len(predicate.Right.Path) != 0 ||
			predicate.Quantifier != "" || predicate.Correlation != nil || predicate.LeftExpression != nil {
			return fmt.Errorf("%w: member filter %d must be exactly %s.%s == @bind (EQUALS, member-variable path, bind-backed right side)",
				ErrUnsupportedPostgresPlan, position, population.MemberScan.Variable, expected.planPath)
		}
		value, ok := plan.BindVars[predicate.Right.BindKey].(string)
		if !ok {
			return fmt.Errorf("%w: member filter %d right bind %q must be a string", ErrUnsupportedPostgresPlan, position, predicate.Right.BindKey)
		}
		if expected.nonEmpty && strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: member filter %d bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, position, predicate.Right.BindKey)
		}
		if expected.canonicalBind != "" {
			if predicate.Right.BindKey != expected.canonicalBind {
				return fmt.Errorf("%w: member filter %d must bind the plan-canonical %q key (D4: the pinned member set is generation-scoped to the plan)",
					ErrUnsupportedPostgresPlan, position, expected.canonicalBind)
			}
			planGeneration, ok := plan.BindVars[datasetGenerationBind].(string)
			if !ok || planGeneration != value {
				return fmt.Errorf("%w: member filter %d must carry the plan dataset_generation value exactly", ErrUnsupportedPostgresPlan, position)
			}
		}
		shape.memberBindKeys[position] = predicate.Right.BindKey
	}
	return nil
}

// matchCanonicalScopeBlock consumes one canonical project/generation/auth
// block for the given variables at *index — the exact shape
// lower/plan_orchestration.go appendProjectScope/appendDatasetGenerationScope/
// appendAuthScope emit and ir/navigation.go
// validateGenericNavigationScopeBlock pins. Recognized blocks are ignored:
// scope.go's structural predicates are the single scope authority (D5).
func matchCanonicalScopeBlock(operations []ir.PhysicalOperation, index *int, variables []string) error {
	expectedLength := 2*len(variables) + 2
	if *index+expectedLength > len(operations) {
		return fmt.Errorf("requires %d operations for the project, dataset_generation, and auth legs of %s",
			expectedLength, strings.Join(variables, ", "))
	}
	for position, variable := range variables {
		operation := operations[*index+position]
		if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil ||
			!sameCanonicalPredicate(operation.Filter.Predicate,
				ir.PhysicalValue{Variable: variable, Path: []string{"project"}},
				ir.PhysicalValue{BindKey: projectBindKey}) {
			return fmt.Errorf("project scope must be %s.project == @%s", variable, projectBindKey)
		}
	}
	for position, variable := range variables {
		operation := operations[*index+len(variables)+position]
		if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil ||
			!sameCanonicalPredicate(operation.Filter.Predicate,
				ir.PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}},
				ir.PhysicalValue{BindKey: datasetGenerationBind}) {
			return fmt.Errorf("dataset generation scope must be %s.dataset_generation == @%s", variable, datasetGenerationBind)
		}
	}
	authLet := operations[*index+2*len(variables)]
	if authLet.Kind != ir.PhysicalDerivedLetOp || authLet.DerivedLet == nil ||
		strings.ToUpper(strings.TrimSpace(authLet.DerivedLet.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" ||
		authLet.DerivedLet.Variable == "" {
		return fmt.Errorf("scope block requires the AUTH_RESOURCE_PATH_ALLOWED LET after the dataset generation filters")
	}
	expectedInputs := make([]ir.PhysicalValue, 0, len(variables)+2)
	for _, variable := range variables {
		expectedInputs = append(expectedInputs, ir.PhysicalValue{Variable: variable, Path: []string{"auth_resource_path"}})
	}
	expectedInputs = append(expectedInputs,
		ir.PhysicalValue{BindKey: authPathsBindKey},
		ir.PhysicalValue{BindKey: authUnrestrictedBind})
	if len(authLet.DerivedLet.Inputs) != len(expectedInputs) {
		return fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires the exact canonical auth scope inputs")
	}
	for position, wanted := range expectedInputs {
		if !sameCanonicalPhysicalValue(authLet.DerivedLet.Inputs[position], wanted) {
			return fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED input %d must be %v", position, wanted)
		}
	}
	authEquality := operations[*index+2*len(variables)+1]
	if authEquality.Kind != ir.PhysicalFilterOp || authEquality.Filter == nil ||
		!sameCanonicalPredicate(authEquality.Filter.Predicate,
			ir.PhysicalValue{Variable: authLet.DerivedLet.Variable},
			ir.PhysicalValue{BindKey: scopeAllowedBind}) {
		return fmt.Errorf("scope block must close with FILTER %s == @%s", authLet.DerivedLet.Variable, scopeAllowedBind)
	}
	*index += expectedLength
	return nil
}

// sameCanonicalPredicate is the exact shape comparison of the canonical
// scope EQUALS forms (operator, both sides, and the absence of every
// non-canonical predicate leg).
func sameCanonicalPredicate(predicate ir.PhysicalPredicate, left, right ir.PhysicalValue) bool {
	if !strings.EqualFold(strings.TrimSpace(predicate.Operator), "EQUALS") {
		return false
	}
	if predicate.Quantifier != "" || predicate.Correlation != nil || predicate.LeftExpression != nil {
		return false
	}
	if !sameCanonicalPhysicalValue(predicate.Left, left) {
		return false
	}
	return predicate.Right != nil && sameCanonicalPhysicalValue(*predicate.Right, right)
}

func sameCanonicalPhysicalValue(got, wanted ir.PhysicalValue) bool {
	if got.Variable != wanted.Variable || got.BindKey != wanted.BindKey || len(got.Path) != len(wanted.Path) {
		return false
	}
	for index := range wanted.Path {
		if got.Path[index] != wanted.Path[index] {
			return false
		}
	}
	return true
}

// renderPopulationRootSource emits the candidate-join layer. The AQL
// composition (render/aql/render.go renderRootScan:679-755) becomes one
// inline subquery: the member scan with the D4 predicate chain, the INNER
// source join on the member logical id with the structural scope predicate,
// one INNER edge×target join per route step (renderTraversalScan parity —
// the route is an inner edge scan, never the LET-set OPTIONAL form), and
// SELECT DISTINCT over the terminal row's four identity columns, joined to
// the root row on exactly those columns (the DOCUMENT restore + null filter
// of render.go:751-754: an unmatched identity simply is not a root).
func (r *planRenderer) renderPopulationRootSource(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	shape, err := validatePopulationPayload(plan, scan)
	if err != nil {
		return err
	}
	// D4 cross-check: the member resource type and the source collection
	// carry the same type (lower/population.go:34-36 binds one value to
	// both).
	memberResourceType, ok := plan.BindVars[shape.memberBindKeys[3]].(string)
	if !ok || memberResourceType != shape.sourceType {
		return fmt.Errorf("%w: population member resource type must equal the source collection type %q (lower/population.go binds one value to both)",
			ErrUnsupportedPostgresPlan, shape.sourceType)
	}

	// Member scan: the four canonical parameters, in filter order (D4).
	memberAlias := "pm"
	memberTable := r.membership.tableSQL(membershipSelectionMembers)
	predicates := make([]string, 0, 4)
	for position, expected := range populationMemberFilterOrder {
		value, _ := plan.BindVars[shape.memberBindKeys[position]].(string)
		predicates = append(predicates,
			memberAlias+"."+r.membership.columnSQL(membershipSelectionMembers, expected.memberColumn)+
				" = $"+strconv.Itoa(r.textParameter(value, "pop-member:"+expected.memberColumn+":"+value)))
	}

	// Source scan: the structural scope predicate plus the member logical-id
	// join (render.go:719 and lower/population.go:63-64).
	resource := r.storage.Resource
	sourceAlias := "x0"
	sourcePredicate := r.scopedResourcePredicate(sourceAlias,
		r.textParameter(shape.sourceType, "text:"+shape.sourceType))
	sourcePredicate += " AND " + sourceAlias + "." + resource.MustColumn("logical_id").SQLText() +
		" = " + memberAlias + "." + r.membership.columnSQL(membershipSelectionMembers, "id")
	fromParts := []string{
		memberTable + " AS " + memberAlias,
		"INNER JOIN " + resource.Identifier().SQLText() + " AS " + sourceAlias + " ON " + sourcePredicate,
	}

	// Route steps: one INNER edge×target join each, through the shared
	// traversal contract, exactly as the EXISTS subplan and unnest routes
	// reuse it. The route-local rows are deliberately never bound into
	// r.rows: the AQL final COLLECT/DOCUMENT drops route variables, so a
	// main-sequence reference to one must fail closed.
	operations := scan.Population.ResourceOperations
	index := 1 + (2*1 + 2) + 1 // scan + source scope block + member-id join
	currentAlias := sourceAlias
	currentType := shape.sourceType
	for index < len(operations) {
		traversal := operations[index].Traversal
		sourceScope := rowScope{alias: currentAlias, resourceType: currentType}
		verified, err := r.validateTraversalContract(plan, *traversal, sourceScope)
		if err != nil {
			return fmt.Errorf("population route step: %w", err)
		}
		seq := r.nextTraversalSequence()
		edgeAlias := "e" + strconv.Itoa(seq)
		targetAlias := "x" + strconv.Itoa(seq)
		edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, targetAlias, sourceScope.alias)
		fromParts = append(fromParts,
			"INNER JOIN "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias+" ON "+edgeWhere,
			"INNER JOIN "+resource.Identifier().SQLText()+" AS "+targetAlias+" ON "+targetOn)
		currentAlias = targetAlias
		currentType = verified.targetType
		index += 1 + (2*2 + 2) // traversal + route scope block (two variables × two legs + LET + equality)
	}

	// Candidate subquery: DISTINCT terminal identity (render.go:743 COLLECT;
	// D1 — the UNIQUE identity of migration 000002 makes the join duplicate
	// free), joined to the root row on the exact identity columns.
	terminalAlias := currentAlias
	identityColumns := []string{"project", "dataset_generation", "resource_type", "logical_id"}
	projection := make([]string, 0, 4)
	join := make([]string, 0, 4)
	for _, column := range identityColumns {
		projection = append(projection, terminalAlias+"."+resource.MustColumn(column).SQLText())
		join = append(join, "pop."+resource.MustColumn(column).SQLText()+" = "+r.rootAlias+"."+resource.MustColumn(column).SQLText())
	}
	candidate := "INNER JOIN (SELECT DISTINCT " + strings.Join(projection, ", ") +
		" FROM " + strings.Join(fromParts, " ") +
		" WHERE " + strings.Join(predicates, " AND ") +
		") AS pop ON " + strings.Join(join, " AND ")
	r.joins = append(r.joins, candidate)
	return nil
}

// ---------------------------------------------------------------------------
// Cohort (pinned group-revision) root source.
// ---------------------------------------------------------------------------

// validateCohortPayload re-reads the closed cohort payload: the four
// collection binds must name the registered relations, the value binds carry
// their canonical types, the policy sits in the closed domain, and the
// identity fields carry the lowered constants. Values are plan data and
// become $n parameters at render.
func validateCohortPayload(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	cohort := scan.CohortSource
	if err := requireMembershipCollection(plan, cohort.RevisionCollectionBindKey, membershipGroupRevisions); err != nil {
		return err
	}
	if err := requireMembershipCollection(plan, cohort.SelectionCollectionBindKey, membershipSelectionRevisions); err != nil {
		return err
	}
	if err := requireMembershipCollection(plan, cohort.SelectionMembersCollectionBindKey, membershipSelectionMembers); err != nil {
		return err
	}
	if err := requireMembershipCollection(plan, cohort.MembershipsCollectionBindKey, membershipGroupMemberships); err != nil {
		return err
	}
	if cohort.RootIdentityColumn != "_key" {
		return fmt.Errorf("%w: cohort RootIdentityColumn must be the lowered \"_key\" identity (lower/recipe_construction.go:403), got %q",
			ErrUnsupportedPostgresPlan, cohort.RootIdentityColumn)
	}
	if strings.TrimSpace(cohort.CohortStageID) == "" || strings.TrimSpace(cohort.CohortInputStageID) == "" {
		return fmt.Errorf("%w: cohort root source must name its cohort stage and input stage", ErrUnsupportedPostgresPlan)
	}
	if rootType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(rootType) == "" || cohort.RootResourceType != rootType {
		return fmt.Errorf("%w: cohort RootResourceType must equal the root collection resource type", ErrUnsupportedPostgresPlan)
	}
	read := func(key, role string, nonEmpty bool) error {
		value, ok := plan.BindVars[key].(string)
		if !ok {
			return fmt.Errorf("%w: cohort %s bind %q must be a string", ErrUnsupportedPostgresPlan, role, key)
		}
		if nonEmpty && strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: cohort %s bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, role, key)
		}
		return nil
	}
	for _, check := range []struct {
		key      string
		role     string
		nonEmpty bool
	}{
		{cohort.RevisionIDBindKey, "revision id", true},
		{cohort.ProjectBindKey, "selection project", true},
		{cohort.DatasetGenerationBindKey, "dataset generation", true},
		{cohort.ResourceTypeBindKey, "resource type", true},
		{cohort.ResourceProjectBindKey, "resource-storage project", true},
		{cohort.PolicyBindKey, "unassigned member policy", true},
	} {
		if err := read(check.key, check.role, check.nonEmpty); err != nil {
			return err
		}
	}
	policy, _ := plan.BindVars[cohort.PolicyBindKey].(string)
	if !canonicalCohortPolicies[policy] {
		return fmt.Errorf("%w: cohort policy bind %q carries %q; the closed domain is ERROR, EXCLUDE, GROUP_AS_UNASSIGNED",
			ErrUnsupportedPostgresPlan, cohort.PolicyBindKey, policy)
	}
	return nil
}

// renderCohortRootSource emits the guard InitPlan and the candidate UNION,
// the PostgreSQL counterpart of renderCohortRootScan
// (render/aql/group_rows.go:226-271): the revision and stale-selection
// guards, the ERROR-policy unassigned guard, the UNION_DISTINCT candidate id
// set, and the root-loop equalities against the resource-storage project,
// dataset generation, and resource type. The guard is an uncorrelated scalar
// subquery — one InitPlan evaluated at executor initialization, so it fires
// exactly once even when zero candidate rows survive (D3/requirement B).
func (r *planRenderer) renderCohortRootSource(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if err := validateCohortPayload(plan, scan); err != nil {
		return err
	}
	cohort := scan.CohortSource
	binding := r.membership
	revisionID, _ := plan.BindVars[cohort.RevisionIDBindKey].(string)
	project, _ := plan.BindVars[cohort.ProjectBindKey].(string)
	generation, _ := plan.BindVars[cohort.DatasetGenerationBindKey].(string)
	resourceType, _ := plan.BindVars[cohort.ResourceTypeBindKey].(string)
	resourceProject, _ := plan.BindVars[cohort.ResourceProjectBindKey].(string)
	policy, _ := plan.BindVars[cohort.PolicyBindKey].(string)

	revisionIndex := r.textParameter(revisionID, "cohort-rev:"+revisionID)
	projectIndex := r.textParameter(project, "cohort-proj:"+project)
	generationIndex := r.textParameter(generation, "cohort-gen:"+generation)
	resourceTypeIndex := r.textParameter(resourceType, "cohort-type:"+resourceType)
	resourceProjectIndex := r.textParameter(resourceProject, "text:"+resourceProject)
	policyIndex := r.textParameter(policy, "cohort-policy:"+policy)

	groupRevisions := binding.tableSQL(membershipGroupRevisions)
	selectionRevisions := binding.tableSQL(membershipSelectionRevisions)
	selectionMembers := binding.tableSQL(membershipSelectionMembers)
	groupMemberships := binding.tableSQL(membershipGroupMemberships)
	col := func(kind postgresMembershipTableKind, column string) string {
		return binding.columnSQL(kind, column)
	}

	// Root-loop equalities beyond the structural scope predicate: the AQL
	// root line filters project/dataset_generation/resourceType against the
	// cohort binds (group_rows.go:268) in addition to the canonical block
	// scope.go renders structurally.
	r.where = append(r.where,
		r.rootAlias+"."+r.storage.Resource.MustColumn("project").SQLText()+" = $"+strconv.Itoa(resourceProjectIndex),
		r.rootAlias+"."+r.storage.Resource.MustColumn("dataset_generation").SQLText()+" = $"+strconv.Itoa(generationIndex),
		r.rootAlias+"."+r.storage.Resource.MustColumn("resource_type").SQLText()+" = $"+strconv.Itoa(resourceTypeIndex),
	)

	// --- Guard InitPlan (D3). CASE order matches the Arango ASSERT order. ---
	revisionGuard := "EXISTS (SELECT 1 FROM " + groupRevisions + " gr0 WHERE " +
		"gr0." + col(membershipGroupRevisions, "id") + " = $" + strconv.Itoa(revisionIndex) +
		" AND gr0." + col(membershipGroupRevisions, "state") + " = " + quoteSQLLiteral(cohortStateComplete) +
		" AND gr0." + col(membershipGroupRevisions, "project") + " = $" + strconv.Itoa(projectIndex) +
		" AND gr0." + col(membershipGroupRevisions, "generation") + " = $" + strconv.Itoa(generationIndex) +
		" AND gr0." + col(membershipGroupRevisions, "resource_type") + " = $" + strconv.Itoa(resourceTypeIndex) + ")"

	selectionGuard := "EXISTS (SELECT 1 FROM " + groupRevisions + " gr1 INNER JOIN " + selectionRevisions +
		" sv0 ON sv0." + col(membershipSelectionRevisions, "id") + " = gr1." + col(membershipGroupRevisions, "source_selection_revision_id") +
		" WHERE gr1." + col(membershipGroupRevisions, "id") + " = $" + strconv.Itoa(revisionIndex) +
		" AND sv0." + col(membershipSelectionRevisions, "complete") + " = TRUE" +
		" AND sv0." + col(membershipSelectionRevisions, "project") + " = $" + strconv.Itoa(projectIndex) +
		" AND sv0." + col(membershipSelectionRevisions, "generation") + " = $" + strconv.Itoa(generationIndex) +
		" AND sv0." + col(membershipSelectionRevisions, "resource_type") + " = $" + strconv.Itoa(resourceTypeIndex) +
		" AND sv0." + col(membershipSelectionRevisions, "scope_digest") + " = gr1." + col(membershipGroupRevisions, "scope_digest") +
		" AND sv0." + col(membershipSelectionRevisions, "membership_digest") + " = gr1." + col(membershipGroupRevisions, "source_membership_digest") + ")"

	// Unassigned anti-semijoin (requirement A): selection members of the
	// pinned source selection with no matching membership on
	// (project, generation, resource_type, resource_id) — the exact keys of
	// group_rows.go:258, with the membership side revision-scoped exactly as
	// the AQL `memberships` LET is (group_rows.go:253). Every column is
	// NOT NULL CHECK non-empty in 000010/000008, so the AND chain has no
	// null-poisoning leg and the NOT-EXISTS reading is exactly the Arango
	// LENGTH(...) == 0 two-valued semantics.
	pinnedSelectionID := "(SELECT gr2." + col(membershipGroupRevisions, "source_selection_revision_id") +
		" FROM " + groupRevisions + " gr2 WHERE gr2." + col(membershipGroupRevisions, "id") +
		" = $" + strconv.Itoa(revisionIndex) + ")"
	unassignedGuard := "EXISTS (SELECT 1 FROM " + selectionMembers + " sm0 WHERE " +
		"sm0." + col(membershipSelectionMembers, "selection_id") + " = " + pinnedSelectionID +
		" AND sm0." + col(membershipSelectionMembers, "project") + " = $" + strconv.Itoa(projectIndex) +
		" AND sm0." + col(membershipSelectionMembers, "generation") + " = $" + strconv.Itoa(generationIndex) +
		" AND sm0." + col(membershipSelectionMembers, "resource_type") + " = $" + strconv.Itoa(resourceTypeIndex) +
		" AND NOT EXISTS (SELECT 1 FROM " + groupMemberships + " mm0 WHERE " +
		"mm0." + col(membershipGroupMemberships, "revision_id") + " = $" + strconv.Itoa(revisionIndex) +
		" AND mm0." + col(membershipGroupMemberships, "project") + " = sm0." + col(membershipSelectionMembers, "project") +
		" AND mm0." + col(membershipGroupMemberships, "generation") + " = sm0." + col(membershipSelectionMembers, "generation") +
		" AND mm0." + col(membershipGroupMemberships, "resource_type") + " = sm0." + col(membershipSelectionMembers, "resource_type") +
		" AND mm0." + col(membershipGroupMemberships, "resource_id") + " = sm0." + col(membershipSelectionMembers, "id") + "))"

	guard := "(SELECT CASE WHEN NOT (" + revisionGuard + ") THEN " + guardCast(cohortGuardRevisionInvalid) +
		" WHEN NOT (" + selectionGuard + ") THEN " + guardCast(cohortGuardSourceStale) +
		" WHEN $" + strconv.Itoa(policyIndex) + " = " + quoteSQLLiteral(cohortPolicyError) + " AND (" + unassignedGuard + ")" +
		" THEN " + guardCast(cohortGuardUnassignedMember) +
		" ELSE 1 END)"
	r.where = append(r.where, guard+" = 1")

	// Candidate set: UNION (deduplicating, the UNION_DISTINCT of
	// group_rows.go:262-265) of the scoped selection-member ids and the
	// scoped membership resource ids, joined to the root logical id.
	selectionCandidates := "SELECT sm1." + col(membershipSelectionMembers, "id") +
		" AS " + quoteMembershipIdentifier("candidate_id") + " FROM " + selectionMembers + " sm1 WHERE " +
		"sm1." + col(membershipSelectionMembers, "selection_id") + " = " + pinnedSelectionID +
		" AND sm1." + col(membershipSelectionMembers, "project") + " = $" + strconv.Itoa(projectIndex) +
		" AND sm1." + col(membershipSelectionMembers, "generation") + " = $" + strconv.Itoa(generationIndex) +
		" AND sm1." + col(membershipSelectionMembers, "resource_type") + " = $" + strconv.Itoa(resourceTypeIndex)
	membershipCandidates := "SELECT mm1." + col(membershipGroupMemberships, "resource_id") + " FROM " + groupMemberships + " mm1 WHERE " +
		"mm1." + col(membershipGroupMemberships, "revision_id") + " = $" + strconv.Itoa(revisionIndex) +
		" AND mm1." + col(membershipGroupMemberships, "project") + " = $" + strconv.Itoa(projectIndex) +
		" AND mm1." + col(membershipGroupMemberships, "generation") + " = $" + strconv.Itoa(generationIndex) +
		" AND mm1." + col(membershipGroupMemberships, "resource_type") + " = $" + strconv.Itoa(resourceTypeIndex)
	candidate := "INNER JOIN (" + selectionCandidates + " UNION " + membershipCandidates +
		") AS cand ON cand." + quoteMembershipIdentifier("candidate_id") + " = " + r.rootAlias + "." +
		r.storage.Resource.MustColumn("logical_id").SQLText()
	r.joins = append(r.joins, candidate)
	return nil
}

// guardBarrierGUC is an absent placeholder setting. Reading it through the
// stable function current_setting is what keeps PostgreSQL from
// constant-folding a guard cast: a bare CAST('TAG' AS integer) raises its
// 22P02 at plan time even in a never-selected CASE arm (measured live),
// which would refuse every cohort query. The barrier keeps each branch a
// stable expression evaluated only when its WHEN fires, and the literal
// still carries the classification tag inside the 22P02 message text.
const guardBarrierGUC = "loom.explorer_guard_barrier"

// guardCast renders one guard branch: integer-casting the tag text behind
// the constant-folding barrier. Reaching the cast is itself the failure,
// so the executor aborts with SQLSTATE 22P02 naming the tag.
func guardCast(tag string) string {
	return "CAST(" + quoteSQLLiteral(tag) +
		" || COALESCE(current_setting(" + quoteSQLLiteral(guardBarrierGUC) + ", true), '') AS integer)"
}

// quoteSQLLiteral renders a renderer-owned constant as a single-quoted SQL
// string literal. Every call site passes a compile-time constant defined in
// this file (guard tags, state/policy names); plan data never reaches it —
// data is bound as $n parameters.
func quoteSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// requireMembershipCollection re-reads one payload collection bind and pins
// it to the exact canonical string of the registered relation (the
// traversal.go:170 "fhir_edge" precedent). The bound value is compared,
// never rendered.
func requireMembershipCollection(plan ir.PhysicalPlan, bindKey string, kind postgresMembershipTableKind) error {
	bound, ok := plan.BindVars[bindKey].(string)
	if !ok {
		return fmt.Errorf("%w: membership collection bind %q must be a string", ErrUnsupportedPostgresPlan, bindKey)
	}
	if expected, registered := canonicalMembershipCollections[bound]; !registered || expected != kind {
		return fmt.Errorf("%w: membership collection bind %q must be bound to %q (the only registered relation for this payload role; lower/group_rows.go:33-37 pins the name)",
			ErrUnsupportedPostgresPlan, bindKey, canonicalCollectionName(kind))
	}
	return nil
}

// canonicalCollectionName reports the canonical collection string registered
// for one relation kind. The registry holds exactly one name per kind.
func canonicalCollectionName(kind postgresMembershipTableKind) string {
	for _, name := range []string{
		"loom_explorer_selection_members",
		"loom_explorer_selections",
		"loom_explorer_explicit_group_revisions",
		"loom_explorer_explicit_group_memberships",
	} {
		if canonicalMembershipCollections[name] == kind {
			return name
		}
	}
	return string(kind)
}
