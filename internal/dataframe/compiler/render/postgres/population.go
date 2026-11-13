package postgres

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type postgresMembershipTableKind string

const (
	membershipSelectionMembers   postgresMembershipTableKind = "selection_members"
	membershipSelectionRevisions postgresMembershipTableKind = "selection_revisions"
	membershipGroupRevisions     postgresMembershipTableKind = "explorer_explicit_group_revisions"
	membershipGroupMemberships   postgresMembershipTableKind = "explorer_explicit_group_memberships"
)

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

var membershipSchemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

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

type PostgresPopulationRootBinding struct {
	schema string
}

func NewPostgresPopulationRootBinding(schema string) (PostgresPopulationRootBinding, error) {
	if !membershipSchemaPattern.MatchString(schema) {
		return PostgresPopulationRootBinding{}, fmt.Errorf("%w: membership schema name %q must match [a-z_][a-z0-9_]{0,62}",
			ir.ErrUnregisteredPostgresIdentifier, schema)
	}
	return PostgresPopulationRootBinding{schema: schema}, nil
}

func (b PostgresPopulationRootBinding) Validate() error {
	if !membershipSchemaPattern.MatchString(b.schema) {
		return fmt.Errorf("%w: membership binding schema %q is not registered",
			ir.ErrUnregisteredPostgresIdentifier, b.schema)
	}
	return nil
}

func (b PostgresPopulationRootBinding) tableSQL(kind postgresMembershipTableKind) string {
	spec, ok := postgresMembershipTables[kind]
	if !ok {
		panic(fmt.Sprintf("population registry: unknown membership table kind %q", string(kind)))
	}
	return quoteMembershipIdentifier(b.schema) + "." + quoteMembershipIdentifier(spec.physicalTable)
}

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

const (
	cohortGuardRevisionInvalid  = "EXPLICIT_GROUP_REVISION_INVALID"
	cohortGuardSourceStale      = "EXPLICIT_GROUP_SOURCE_STALE"
	cohortGuardUnassignedMember = "EXPLICIT_GROUP_UNASSIGNED_MEMBER"

	cohortStateComplete = "COMPLETE"
	cohortPolicyError   = "ERROR"
)

var canonicalCohortPolicies = map[string]bool{
	"ERROR": true, "EXCLUDE": true, "GROUP_AS_UNASSIGNED": true,
}

var populationMemberFilterOrder = []struct {
	memberColumn  string
	planPath      string
	canonicalBind string
	nonEmpty      bool
}{
	{"selection_id", "selectionId", "", true},
	{"project", "project", "", true},
	{"generation", "generation", datasetGenerationBind, false},
	{"resource_type", "resourceType", "", true},
}

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
		_, err := validatePopulationPayload(plan, scan, false)
		return err
	}
	return validateCohortPayload(plan, scan)
}

func requireMembershipRootBinds(scan ir.PhysicalRootScan) error {
	if strings.TrimSpace(scan.Variable) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(scan.CollectionBindKey) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func RenderPopulationRootScan(
	plan ir.PhysicalPlan,
	storage ir.PostgresStorageBinding,
	binding PostgresPopulationRootBinding,
) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := ValidatePopulationRootSource(plan, binding); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			if err := validateUnnestPlanSurface(plan); err != nil {
				return command.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: staged row-operator sequences are not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: preview source windowing is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
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
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := renderer.renderOperations(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(renderer.assemble(), renderer.args, renderer.outputColumns)
}

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
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	r.rootAlias = "r0"
	r.rows[scan.Variable] = rowScope{alias: r.rootAlias, resourceType: resourceType}
	denyConjunct := " AND FALSE"
	if scan.Population == nil {
		denyConjunct = ""
	}
	r.where = append(r.where, r.scopedResourcePredicateDenyConjunct(r.rootAlias,
		r.textParameter(resourceType, "text:"+resourceType), denyConjunct))
	if scan.Population != nil {
		return r.renderPopulationRootSource(plan, scan)
	}
	return r.renderCohortRootSource(plan, scan)
}

type populationRootShape struct {
	memberVariable   string
	sourceVariable   string
	sourceType       string
	terminalVariable string
	memberBindKeys   [4]string
}

func validatePopulationPayload(plan ir.PhysicalPlan, scan ir.PhysicalRootScan, witness bool) (populationRootShape, error) {
	var shape populationRootShape
	population := scan.Population
	if !witness && population.CollectMembersVariable != "" {
		return shape, fmt.Errorf("%w: population member witnesses (CollectMembersVariable %q) belong to the pg-sql-22 population terminal and are not registered by pg-sql-18 (D2)",
			ErrUnsupportedPostgresPlan, population.CollectMembersVariable)
	}
	if witness && population.CollectMembersVariable != ir.PopulationMappingMembersVariable {
		return shape, fmt.Errorf("%w: the member witness is registered only for CollectMembersVariable %q, got %q",
			ErrUnsupportedPostgresPlan, ir.PopulationMappingMembersVariable, population.CollectMembersVariable)
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

func probeTraversalContract(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal, source rowScope) (validatedTraversal, error) {
	probe := &planRenderer{parameters: map[string]int{}}
	return probe.validateTraversalContract(plan, traversal, source)
}

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

func (r *planRenderer) renderPopulationRootSource(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	shape, err := validatePopulationPayload(plan, scan, r.mapping != nil && r.mapping.witness)
	if err != nil {
		return err
	}
	memberResourceType, ok := plan.BindVars[shape.memberBindKeys[3]].(string)
	if !ok || memberResourceType != shape.sourceType {
		return fmt.Errorf("%w: population member resource type must equal the source collection type %q (lower/population.go binds one value to both)",
			ErrUnsupportedPostgresPlan, shape.sourceType)
	}

	memberAlias := "pm"
	memberTable := r.membership.tableSQL(membershipSelectionMembers)
	predicates := make([]string, 0, 4)
	for position, expected := range populationMemberFilterOrder {
		value, _ := plan.BindVars[shape.memberBindKeys[position]].(string)
		predicates = append(predicates,
			memberAlias+"."+r.membership.columnSQL(membershipSelectionMembers, expected.memberColumn)+
				" = $"+strconv.Itoa(r.textParameter(value, "pop-member:"+expected.memberColumn+":"+value)))
	}

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

	operations := scan.Population.ResourceOperations
	index := 1 + (2*1 + 2) + 1
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
		index += 1 + (2*2 + 2)
	}

	terminalAlias := currentAlias
	identityColumns := []string{"project", "dataset_generation", "resource_type", "logical_id"}
	projection := make([]string, 0, 4)
	join := make([]string, 0, 4)
	for _, column := range identityColumns {
		projection = append(projection, terminalAlias+"."+resource.MustColumn(column).SQLText())
		join = append(join, "pop."+resource.MustColumn(column).SQLText()+" = "+r.rootAlias+"."+resource.MustColumn(column).SQLText())
	}
	candidate := "INNER JOIN ("
	if r.mapping != nil && r.mapping.witness {
		memberRead := memberAlias + "." + r.membership.columnSQL(membershipSelectionMembers, "id") + ` COLLATE "C"`
		candidate += "SELECT " + strings.Join(projection, ", ") +
			", to_jsonb(ARRAY_AGG(DISTINCT (" + memberRead + ") ORDER BY (" + memberRead + `))) AS "` +
			populationMappingMembersColumnAlias + `"` +
			" FROM " + strings.Join(fromParts, " ") +
			" WHERE " + strings.Join(predicates, " AND ") +
			" GROUP BY " + strings.Join(projection, ", ") +
			") AS pop ON " + strings.Join(join, " AND ")
		r.mapping.membersColumnRef = "pop." + quoteMembershipIdentifier(populationMappingMembersColumnAlias)
	} else {
		candidate += "SELECT DISTINCT " + strings.Join(projection, ", ") +
			" FROM " + strings.Join(fromParts, " ") +
			" WHERE " + strings.Join(predicates, " AND ") +
			") AS pop ON " + strings.Join(join, " AND ")
	}
	r.joins = append(r.joins, candidate)
	return nil
}

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

	r.where = append(r.where,
		r.rootAlias+"."+r.storage.Resource.MustColumn("project").SQLText()+" = $"+strconv.Itoa(resourceProjectIndex),
		r.rootAlias+"."+r.storage.Resource.MustColumn("dataset_generation").SQLText()+" = $"+strconv.Itoa(generationIndex),
		r.rootAlias+"."+r.storage.Resource.MustColumn("resource_type").SQLText()+" = $"+strconv.Itoa(resourceTypeIndex),
	)

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
	if r.scope.denied {
		r.where = append(r.where, "("+guard+cohortDenyGuardSuffix+")")
	} else {
		r.where = append(r.where, guard+" = 1")
	}

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

const guardBarrierGUC = "loom.explorer_guard_barrier"

const cohortDenyGuardSuffix = " * 0 = 1"

func guardCast(tag string) string {
	return "CAST(" + quoteSQLLiteral(tag) +
		" || COALESCE(current_setting(" + quoteSQLLiteral(guardBarrierGUC) + ", true), '') AS integer)"
}

func quoteSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

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
