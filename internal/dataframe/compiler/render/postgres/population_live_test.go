package postgres

import (
	"context"
	"errors"
	"fmt"
	control "github.com/calypr/loom/internal/backends/postgres/control"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	explorer "github.com/calypr/loom/internal/explorer"
)

const (
	pop18Project     = "pop18-proj"
	pop18Foreign     = "pop18-proj-x"
	pop18Generation  = "gen-1"
	pop18RouteLabel  = "encounter-diagnosed_condition"
	pop18PatientAuth = "FHIR/Patient"
	pop18ObsAuth     = "FHIR/Observation"
	pop18SecretAuth  = "FHIR/Secret"
)

type pop18Service struct {
	*pgir02Service
	store   *control.ExplorerStore
	groups  *control.ExplicitGroupStore
	binding ir.PostgresStorageBinding
	members PostgresPopulationRootBinding
}

func newPop18Service(t *testing.T) *pop18Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_pop18")
	applyExplorerMembershipDDL(t, svc)
	store, err := control.NewExplorerStore(svc.client, svc.schema)
	if err != nil {
		svc.fail("open explorer store", err)
	}
	groups, err := control.NewExplicitGroupStore(svc.client, svc.schema)
	if err != nil {
		svc.fail("open explicit-group store", err)
	}
	binding, err := ir.NewPostgresStorageBinding(svc.schema)
	if err != nil {
		svc.fail("storage binding", err)
	}
	members, err := NewPostgresPopulationRootBinding(svc.schema)
	if err != nil {
		svc.fail("membership binding", err)
	}
	return &pop18Service{pgir02Service: svc, store: store, groups: groups, binding: binding, members: members}
}

func applyExplorerMembershipDDL(t *testing.T, svc *pgir02Service) {
	t.Helper()
	migrations, err := control.OwnerMigrations()
	if err != nil {
		svc.fail("load owner migrations", err)
	}
	conn, err := svc.iso.Acquire(svc.ctx)
	if err != nil {
		svc.fail("acquire DDL connection", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(svc.ctx, `SET search_path TO "`+svc.schema+`"`); err != nil {
		svc.fail("pin search_path for membership DDL", err)
	}
	applied := 0
	for _, migration := range migrations {
		if migration.Version == 1 {
			continue
		}
		if _, err := conn.Exec(svc.ctx, migration.SQL); err != nil {
			svc.fail(fmt.Sprintf("apply explorer DDL %d_%s", migration.Version, migration.Name), err)
		}
		applied++
	}
	if applied != len(migrations)-1 {
		t.Fatalf("applied %d explorer migrations, want %d", applied, len(migrations)-1)
	}
}

func (s *pop18Service) render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPopulationRootScan(plan, s.binding, s.members)
	if err != nil {
		s.fail("render membership plan", err)
	}
	return rendered
}

func (s *pop18Service) runErr(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) ([][]string, error) {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := s.iso.Query(s.ctx, rendered.SQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func wantGuardTag(t *testing.T, err error, tag string) {
	t.Helper()
	if err == nil {
		t.Fatalf("guard %s accepted instead of failing", tag)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("guard %s error is not a PgError: %v", tag, err)
	}
	if pgErr.Code != "22P02" || !strings.Contains(pgErr.Message, tag) {
		t.Fatalf("guard %s error = %s: %s, want 22P02 containing %q", tag, pgErr.Code, pgErr.Message, tag)
	}
}

func singleColumnRows(rows [][]string) []string {
	var out []string
	for _, row := range rows {
		if len(row) != 1 {
			out = append(out, strings.Join(row, "|"))
			continue
		}
		out = append(out, row[0])
	}
	return out
}

func (s *pop18Service) seedSelection(t *testing.T, id, project, resourceType string, memberIDs []string, writer string) explorer.SelectionRevision {
	t.Helper()
	now := time.Now().UTC()
	header, err := s.store.BeginSelection(s.ctx, explorer.SelectionRevision{
		ID: id, Project: project, Generation: pop18Generation, ResourceType: resourceType,
		Rule:        explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:      explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: "scope-18", RuleDigest: "rule-18", CreatedAt: now,
	}, writer)
	if err != nil {
		s.fail("begin selection "+id, err)
	}
	members := make([]explorer.SelectionMember, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		members = append(members, explorer.SelectionMember{Ref: explorer.ResourceRef{
			Project: project, Generation: pop18Generation, ResourceType: resourceType, ID: memberID}})
	}
	if _, err := s.store.AppendSelectionMembers(s.ctx, header.ID, writer, members); err != nil {
		s.fail("append selection members "+id, err)
	}
	digest, count, bytes, err := s.store.DigestSelectionMembers(s.ctx, project, header.ID)
	if err != nil {
		s.fail("digest selection "+id, err)
	}
	completed, err := s.store.CompleteSelection(s.ctx, header.ID, writer, digest, count, bytes, time.Now().UTC())
	if err != nil {
		s.fail("complete selection "+id, err)
	}
	return *completed
}

func (s *pop18Service) seedGroup(t *testing.T, key string, source explorer.SelectionRevision, definitions []explorer.ExplicitGroupDefinition, memberships []explorer.ExplicitGroupMembership, writer string) explorer.ExplicitGroupRevision {
	t.Helper()
	revision := explorer.ExplicitGroupRevision{
		ID:      explorer.ExplicitGroupRevisionIDFor(source.Project, key),
		Project: source.Project, Generation: source.Generation, ScopeDigest: source.ScopeDigest,
		ResourceType: source.ResourceType, SourceSelectionRevisionID: source.ID,
		SourceMembershipDigest: source.MembershipDigest,
		State:                  explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:         key, CreatedAt: time.Now().UTC(),
	}
	revision, err := s.seedGroupFrom(t, revision, source, definitions, memberships, writer)
	if err != nil {
		s.fail("seed group "+key, err)
	}
	return revision
}

func (s *pop18Service) seedGroupFrom(t *testing.T, revision explorer.ExplicitGroupRevision, source explorer.SelectionRevision, definitions []explorer.ExplicitGroupDefinition, memberships []explorer.ExplicitGroupMembership, writer string) (explorer.ExplicitGroupRevision, error) {
	t.Helper()
	if _, err := s.groups.BeginExplicitGroupRevision(s.ctx, revision, source, writer); err != nil {
		return revision, err
	}
	if err := s.groups.PutExplicitGroupDefinitions(s.ctx, revision.ID, writer, definitions); err != nil {
		return revision, err
	}
	if _, err := s.groups.AppendExplicitGroupMemberships(s.ctx, revision.ID, writer, memberships); err != nil {
		return revision, err
	}
	definitionDigest, membershipDigest, groupCount, memberCount, err := s.groups.DigestExplicitGroupRevision(s.ctx, revision.Project, revision.ID)
	if err != nil {
		return revision, err
	}
	if _, err := s.groups.CompleteExplicitGroupRevision(s.ctx, revision.ID, writer, definitionDigest, membershipDigest, groupCount, memberCount, time.Now().UTC()); err != nil {
		return revision, err
	}
	return revision, nil
}

func pop18Patient(t *testing.T, s *pop18Service, project, logicalID, authPath string) {
	t.Helper()
	s.insertResource(t, s.schema, project, pop18Generation, "Patient", logicalID, authPath,
		map[string]any{"resourceType": "Patient", "id": logicalID})
}

func livePopulationPlan(selectionID string, paths []string, unrestricted, allowed bool) ir.PhysicalPlan {
	plan := populationPlan(1)
	plan.BindVars["project"] = pop18Project
	plan.BindVars["dataset_generation"] = pop18Generation
	plan.BindVars["population_project"] = pop18Project
	plan.BindVars["population_selection_id"] = selectionID
	plan.BindVars["population_resource_type"] = "Observation"
	plan.BindVars["population_source_collection"] = "Observation"
	plan.BindVars["root_collection"] = "Patient"
	plan.BindVars["population_root_route_0_target_type"] = "Patient"
	plan.BindVars["population_root_route_0_edge_label"] = pop18RouteLabel
	plan.BindVars["auth_resource_paths"] = paths
	plan.BindVars["auth_resource_paths_unrestricted"] = unrestricted
	plan.BindVars["scope_allowed"] = allowed
	return plan
}

func seedPopulationFixture(t *testing.T, s *pop18Service) {
	t.Helper()
	s.insertResource(t, s.schema, pop18Project, pop18Generation, "Observation", "O1", pop18ObsAuth,
		map[string]any{"resourceType": "Observation", "id": "O1"})
	s.insertResource(t, s.schema, pop18Project, pop18Generation, "Observation", "O2", pop18ObsAuth,
		map[string]any{"resourceType": "Observation", "id": "O2"})
	s.insertResource(t, s.schema, pop18Project, pop18Generation, "Observation", "O3", pop18ObsAuth,
		map[string]any{"resourceType": "Observation", "id": "O3"})
	s.seedSelection(t, "sel18_obs", pop18Project, "Observation", []string{"O1"}, "pop18-writer")
	s.seedSelection(t, "sel18_obs_x", pop18Foreign, "Observation", []string{"O3"}, "pop18-writer-x")
	pop18Patient(t, s, pop18Project, "P1", pop18PatientAuth)
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P1",
		TargetResourceType: "Observation", TargetLogicalID: "O1", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	pop18Patient(t, s, pop18Project, "P2", pop18PatientAuth)
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P2",
		TargetResourceType: "Observation", TargetLogicalID: "O2", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	pop18Patient(t, s, pop18Project, "P3", pop18PatientAuth)
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: "provenance-entity", SourceResourceType: "Patient", SourceLogicalID: "P3",
		TargetResourceType: "Observation", TargetLogicalID: "O3", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	pop18Patient(t, s, pop18Project, "P4", pop18PatientAuth)
	s.insertResource(t, s.schema, pop18Project, "gen-2", "Patient", "P4", pop18PatientAuth,
		map[string]any{"resourceType": "Patient", "id": "P4"})
	s.insertResource(t, s.schema, pop18Project, "gen-2", "Observation", "O3", pop18ObsAuth,
		map[string]any{"resourceType": "Observation", "id": "O3"})
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: "gen-2", AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P4",
		TargetResourceType: "Observation", TargetLogicalID: "O3", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	pop18Patient(t, s, pop18Project, "P5", pop18SecretAuth)
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P5",
		TargetResourceType: "Observation", TargetLogicalID: "O1", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	pop18Patient(t, s, pop18Project, "P6", pop18PatientAuth)
	pop18Patient(t, s, pop18Foreign, "P6", pop18PatientAuth)
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Foreign, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P6",
		TargetResourceType: "Observation", TargetLogicalID: "O1", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	pop18Patient(t, s, pop18Project, "P7", pop18PatientAuth)
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P7",
		TargetResourceType: "Observation", TargetLogicalID: "O3", SourcePath: "focus[0]", SourceOrdinal: 0,
	})
	s.insertEdge(t, control.EdgeOccurrenceRow{
		Project: pop18Project, DatasetGeneration: pop18Generation, AuthResourcePath: pop18PatientAuth,
		Label: pop18RouteLabel, SourceResourceType: "Patient", SourceLogicalID: "P1",
		TargetResourceType: "Observation", TargetLogicalID: "O1", SourcePath: "focus[1]", SourceOrdinal: 1,
	})
}

func TestPopulationLiveSelectedRowsOnly(t *testing.T) {
	s := newPop18Service(t)
	seedPopulationFixture(t, s)
	rendered := s.render(t, livePopulationPlan("sel18_obs",
		[]string{pop18PatientAuth, pop18ObsAuth}, false, true))
	rows, err := s.runErr(t, rendered)
	if err != nil {
		s.fail("execute population root", err)
	}
	got := singleColumnRows(rows)
	want := []string{"P1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("population rows = %v, want %v", got, want)
	}
	if len(rows) != 1 {
		t.Fatalf("duplicate route edge leaked a second row: %v", rows)
	}
	edgeCount := s.witness(t, `SELECT count(*) FROM "`+s.schema+`".fhir_edge_occurrence
		WHERE project = $1 AND dataset_generation = $2 AND label = $3
		  AND source_resource_type = 'Patient' AND source_logical_id = 'P1'`,
		pop18Project, pop18Generation, pop18RouteLabel)
	if len(edgeCount) != 1 || edgeCount[0][0] != "2" {
		t.Fatalf("duplicate route edge insertion not present: %v", edgeCount)
	}
	candDupes := s.witness(t, `SELECT count(*) FROM (
		SELECT x1."project", x1."dataset_generation", x1."resource_type", x1."logical_id"
		FROM "`+s.schema+`"."selection_members" pm
		INNER JOIN "`+s.schema+`"."fhir_resource" x0 ON x0."project" = $1 AND x0."dataset_generation" = $2
		  AND x0."resource_type" = 'Observation' AND x0."logical_id" = pm."id"
		INNER JOIN "`+s.schema+`"."fhir_edge_occurrence" e1 ON e1."project" = $1 AND e1."dataset_generation" = $2
		  AND e1."label" = $3 AND e1."target_resource_type" = 'Observation'
		  AND e1."target_logical_id" = x0."logical_id" AND e1."source_resource_type" = 'Patient'
		INNER JOIN "`+s.schema+`"."fhir_resource" x1 ON x1."project" = $1 AND x1."dataset_generation" = $2
		  AND x1."resource_type" = 'Patient' AND x1."logical_id" = e1."source_logical_id"
		WHERE pm."selection_id" = 'sel18_obs' AND pm."project" = $1
		  AND pm."generation" = $2 AND pm."resource_type" = 'Observation') t
		WHERE t."logical_id" = 'P1'`,
		pop18Project, pop18Generation, pop18RouteLabel)
	if len(candDupes) != 1 || candDupes[0][0] != "2" {
		t.Fatalf("undistinct candidate join must yield the P1 identity tuple twice, got %v", candDupes)
	}
	dupes := s.witness(t, `SELECT count(*) FROM (
		SELECT project, dataset_generation, resource_type, logical_id
		FROM "`+s.schema+`"."fhir_resource"
		GROUP BY 1, 2, 3, 4 HAVING count(*) > 1) d`)
	if len(dupes) != 1 || dupes[0][0] != "0" {
		t.Fatalf("duplicate identity witness = %v, want [[0]]", dupes)
	}
}

func TestPopulationLiveMembershipScopingAlive(t *testing.T) {
	s := newPop18Service(t)
	seedPopulationFixture(t, s)
	s.seedSelection(t, "sel18_obs_two", pop18Project, "Observation", []string{"O1", "O3"}, "pop18-writer-2")
	rows := s.runRendered(t, s.render(t, livePopulationPlan("sel18_obs_two",
		[]string{pop18PatientAuth, pop18ObsAuth}, false, true)))
	got := strings.Join(singleColumnRows(rows), ",")
	if got != "P1,P7" {
		t.Fatalf("promoted-membership rows = %q, want P1,P7", got)
	}
}

func TestPopulationLiveCanonicalDenyAndUnrestricted(t *testing.T) {
	s := newPop18Service(t)
	seedPopulationFixture(t, s)
	denied := s.runRendered(t, s.render(t, livePopulationPlan("sel18_obs",
		[]string{pop18PatientAuth, pop18ObsAuth}, false, false)))
	if len(denied) != 0 {
		t.Fatalf("canonical deny must read nothing, got %v", denied)
	}
	allowed := s.runRendered(t, s.render(t, livePopulationPlan("sel18_obs",
		[]string{pop18PatientAuth, pop18ObsAuth}, false, true)))
	if strings.Join(singleColumnRows(allowed), ",") != "P1" {
		t.Fatalf("allowed control rows = %v, want [P1]", allowed)
	}
	unrestricted := s.runRendered(t, s.render(t, livePopulationPlan("sel18_obs",
		nil, true, true)))
	if got := strings.Join(singleColumnRows(unrestricted), ","); got != "P1,P5" {
		t.Fatalf("unrestricted rows = %q, want P1,P5", got)
	}
}

func liveCohortPlan(revisionID string, paths []string, unrestricted, allowed bool, policy string) ir.PhysicalPlan {
	plan := cohortPlan()
	plan.BindVars["project"] = pop18Project
	plan.BindVars["dataset_generation"] = pop18Generation
	plan.BindVars["cohort_project"] = pop18Project
	plan.BindVars["cohort_resource_project"] = pop18Project
	plan.BindVars["cohort_dataset_generation"] = pop18Generation
	plan.BindVars["cohort_revision_id"] = revisionID
	plan.BindVars["cohort_policy"] = policy
	plan.BindVars["auth_resource_paths"] = paths
	plan.BindVars["auth_resource_paths_unrestricted"] = unrestricted
	plan.BindVars["scope_allowed"] = allowed
	return plan
}

type pop18CohortFixture struct {
	source explorer.SelectionRevision
	group  explorer.ExplicitGroupRevision
}

func seedCohortFixture(t *testing.T, s *pop18Service) pop18CohortFixture {
	t.Helper()
	for _, id := range []string{"R1", "R2", "R3"} {
		pop18Patient(t, s, pop18Project, id, pop18PatientAuth)
	}
	pop18Patient(t, s, pop18Project, "R4", pop18SecretAuth)
	pop18Patient(t, s, pop18Project, "R9", pop18PatientAuth)
	source := s.seedSelection(t, "sel18_pat", pop18Project, "Patient",
		[]string{"R1", "R2", "R3", "R4"}, "pop18-cwriter")
	definitions := []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha", Ordinal: 0},
		{ID: "group-beta", Label: "Beta", Ordinal: 1},
	}
	var memberships []explorer.ExplicitGroupMembership
	for _, group := range []string{"group-alpha", "group-beta"} {
		memberships = append(memberships, explorer.ExplicitGroupMembership{GroupID: explorer.ExplicitGroupID(group), Ref: explorer.ResourceRef{
			Project: pop18Project, Generation: pop18Generation, ResourceType: "Patient", ID: "R1"}})
	}
	group := s.seedGroup(t, "pop18-group-a", source, definitions, memberships, "pop18-gwriter")
	return pop18CohortFixture{source: source, group: group}
}

func TestCohortLiveAssignedUnassignedAndScoping(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	plan := liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, true, "EXCLUDE")
	first := s.runRendered(t, s.render(t, plan))
	if got := strings.Join(singleColumnRows(first), ","); got != "R1,R2,R3" {
		t.Fatalf("cohort rows = %q, want R1,R2,R3", got)
	}
	unionDupes := s.witness(t, `SELECT count(*) FROM (
		SELECT sm."id" FROM "`+s.schema+`"."selection_members" sm
		WHERE sm."selection_id" = 'sel18_pat' AND sm."project" = $1 AND sm."generation" = $2 AND sm."resource_type" = 'Patient'
		UNION ALL
		SELECT mm."resource_id" FROM "`+s.schema+`"."explorer_explicit_group_memberships" mm
		WHERE mm."revision_id" = $3 AND mm."project" = $1 AND mm."generation" = $2 AND mm."resource_type" = 'Patient') u
		WHERE u."id" = 'R1'`, pop18Project, pop18Generation, string(fixture.group.ID))
	if len(unionDupes) != 1 || unionDupes[0][0] != "3" {
		t.Fatalf("undeduplicated candidates must carry R1 three times, got %v", unionDupes)
	}
	second, err := s.runErr(t, s.render(t, plan))
	if err != nil || !reflectDeepEqualStrings(singleColumnRows(first), singleColumnRows(second)) {
		t.Fatalf("cohort rows are not deterministic: %v vs %v (%v)", singleColumnRows(first), second, err)
	}
	policy := liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, true, "GROUP_AS_UNASSIGNED")
	if got := strings.Join(singleColumnRows(s.runRendered(t, s.render(t, policy))), ","); got != "R1,R2,R3" {
		t.Fatalf("GROUP_AS_UNASSIGNED rows = %q, want R1,R2,R3", got)
	}
	unrestricted := liveCohortPlan(string(fixture.group.ID), nil, true, true, "EXCLUDE")
	if got := strings.Join(singleColumnRows(s.runRendered(t, s.render(t, unrestricted))), ","); got != "R1,R2,R3,R4" {
		t.Fatalf("unrestricted cohort rows = %q, want R1,R2,R3,R4", got)
	}
	denied := liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, false, "EXCLUDE")
	if rows := s.runRendered(t, s.render(t, denied)); len(rows) != 0 {
		t.Fatalf("cohort deny must read nothing, got %v", rows)
	}
}

func TestCohortLiveGuardIsUncorrelatedInitPlan(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	rendered := s.render(t, liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, true, "EXCLUDE"))
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	planRows := s.witness(t, "EXPLAIN (FORMAT TEXT) "+rendered.SQL, args...)
	var text strings.Builder
	for _, row := range planRows {
		text.WriteString(strings.Join(row, "\n"))
		text.WriteString("\n")
	}
	planText := text.String()
	if !strings.Contains(planText, "InitPlan") {
		t.Fatalf("guard sublinks must execute as InitPlans, plan:\n%s", planText)
	}
	if strings.Contains(planText, "SubPlan") {
		t.Fatalf("guard must stay uncorrelated (no per-row SubPlans), plan:\n%s", planText)
	}
	t.Logf("plan:\n%s", planText)
}

func TestCohortLiveUnassignedErrorPolicy(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	_, err := s.runErr(t, s.render(t,
		liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, true, "ERROR")))
	wantGuardTag(t, err, "EXPLICIT_GROUP_UNASSIGNED_MEMBER")
	definitions := []explorer.ExplicitGroupDefinition{{ID: "group-all", Label: "All", Ordinal: 0}}
	var memberships []explorer.ExplicitGroupMembership
	for _, id := range []string{"R1", "R2", "R3", "R4"} {
		memberships = append(memberships, explorer.ExplicitGroupMembership{GroupID: "group-all", Ref: explorer.ResourceRef{
			Project: pop18Project, Generation: pop18Generation, ResourceType: "Patient", ID: id}})
	}
	assigned := s.seedGroup(t, "pop18-group-all", fixture.source, definitions, memberships, "pop18-gwriter-all")
	rows := s.runRendered(t, s.render(t,
		liveCohortPlan(string(assigned.ID), []string{pop18PatientAuth}, false, true, "ERROR")))
	if got := strings.Join(singleColumnRows(rows), ","); got != "R1,R2,R3" {
		t.Fatalf("fully assigned ERROR rows = %q, want R1,R2,R3", got)
	}
}

func TestCohortLiveNullMembershipSubstitutedControls(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	definitionsC := []explorer.ExplicitGroupDefinition{{ID: "group-d", Label: "D", Ordinal: 0}}
	var membershipsC []explorer.ExplicitGroupMembership
	for _, id := range []string{"R1", "R2", "R3", "R4"} {
		membershipsC = append(membershipsC, explorer.ExplicitGroupMembership{GroupID: "group-d", Ref: explorer.ResourceRef{
			Project: pop18Project, Generation: pop18Generation, ResourceType: "Patient", ID: id}})
	}
	danglingSource := s.seedSelection(t, "sel18_pat_d", pop18Project, "Patient",
		[]string{"R1", "R2", "R3", "R4", "R8"}, "pop18-cwriter-d")
	revisionC := s.seedGroup(t, "pop18-group-d", danglingSource, definitionsC, membershipsC, "pop18-gwriter-d")
	_, err := s.runErr(t, s.render(t,
		liveCohortPlan(string(revisionC.ID), []string{pop18PatientAuth}, false, true, "ERROR")))
	wantGuardTag(t, err, "EXPLICIT_GROUP_UNASSIGNED_MEMBER")
	if got := strings.Join(singleColumnRows(s.runRendered(t, s.render(t,
		liveCohortPlan(string(revisionC.ID), []string{pop18PatientAuth}, false, true, "EXCLUDE")))), ","); got != "R1,R2,R3" {
		t.Fatalf("dangling-reference EXCLUDE rows = %q, want R1,R2,R3", got)
	}
	definitionsE := []explorer.ExplicitGroupDefinition{{ID: "group-e", Label: "E", Ordinal: 0}}
	var membershipsE []explorer.ExplicitGroupMembership
	for _, id := range []string{"R2", "R3"} {
		membershipsE = append(membershipsE, explorer.ExplicitGroupMembership{GroupID: "group-e", Ref: explorer.ResourceRef{
			Project: pop18Project, Generation: pop18Generation, ResourceType: "Patient", ID: id}})
	}
	revisionE := s.seedGroup(t, "pop18-group-e", fixture.source, definitionsE, membershipsE, "pop18-gwriter-e")
	_, err = s.runErr(t, s.render(t,
		liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, true, "ERROR")))
	wantGuardTag(t, err, "EXPLICIT_GROUP_UNASSIGNED_MEMBER")
	if got := strings.Join(singleColumnRows(s.runRendered(t, s.render(t,
		liveCohortPlan(string(revisionE.ID), []string{pop18PatientAuth}, false, true, "EXCLUDE")))), ","); got != "R1,R2,R3" {
		t.Fatalf("foreign-scope control rows = %q, want R1,R2,R3", got)
	}
}

func TestCohortLiveRevisionInvalidFiresAtZeroRows(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	staging := explorer.ExplicitGroupRevision{
		ID:      explorer.ExplicitGroupRevisionIDFor(pop18Project, "pop18-group-staging"),
		Project: pop18Project, Generation: pop18Generation, ScopeDigest: fixture.source.ScopeDigest,
		ResourceType: fixture.source.ResourceType, SourceSelectionRevisionID: fixture.source.ID,
		SourceMembershipDigest: fixture.source.MembershipDigest,
		State:                  explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:         "pop18-group-staging", CreatedAt: time.Now().UTC(),
	}
	if _, err := s.groups.BeginExplicitGroupRevision(s.ctx, staging, fixture.source, "pop18-gwriter-staging"); err != nil {
		s.fail("begin staging group", err)
	}
	_, err := s.runErr(t, s.render(t,
		liveCohortPlan(string(staging.ID), []string{pop18PatientAuth}, false, true, "EXCLUDE")))
	wantGuardTag(t, err, "EXPLICIT_GROUP_REVISION_INVALID")
	_, err = s.runErr(t, s.render(t,
		liveCohortPlan(string(staging.ID), []string{"FHIR/Nothing"}, false, true, "EXCLUDE")))
	wantGuardTag(t, err, "EXPLICIT_GROUP_REVISION_INVALID")
	rows := s.runRendered(t, s.render(t,
		liveCohortPlan(string(fixture.group.ID), []string{"FHIR/Nothing"}, false, true, "EXCLUDE")))
	if len(rows) != 0 {
		t.Fatalf("impossible-auth cohort rows = %v, want none", rows)
	}
}

func TestCohortLiveGuardFiresAtZeroRowsUnderExplainAnalyze(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	staging := explorer.ExplicitGroupRevision{
		ID:      explorer.ExplicitGroupRevisionIDFor(pop18Project, "pop18-group-analyze"),
		Project: pop18Project, Generation: pop18Generation, ScopeDigest: fixture.source.ScopeDigest,
		ResourceType: fixture.source.ResourceType, SourceSelectionRevisionID: fixture.source.ID,
		SourceMembershipDigest: fixture.source.MembershipDigest,
		State:                  explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:         "pop18-group-analyze", CreatedAt: time.Now().UTC(),
	}
	if _, err := s.groups.BeginExplicitGroupRevision(s.ctx, staging, fixture.source, "pop18-gwriter-analyze"); err != nil {
		s.fail("begin staging group for analyze", err)
	}
	rendered := s.render(t, liveCohortPlan(string(staging.ID), []string{"FHIR/Nothing"}, false, true, "EXCLUDE"))
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	_, err := s.iso.Exec(ctx, "EXPLAIN (ANALYZE, TIMING OFF, SUMMARY OFF) "+rendered.SQL, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22P02" || !strings.Contains(pgErr.Message, "EXPLICIT_GROUP_REVISION_INVALID") {
		t.Fatalf("EXPLAIN ANALYZE zero-row guard raise = %v, want 22P02 naming EXPLICIT_GROUP_REVISION_INVALID", err)
	}
	planRows := s.witness(t, "EXPLAIN "+rendered.SQL, args...)
	if len(planRows) == 0 {
		t.Fatalf("plain EXPLAIN produced no plan under the violating guard")
	}
}

func seedStaleGroup(t *testing.T, s *pop18Service, fixture pop18CohortFixture) explorer.ExplicitGroupRevisionID {
	t.Helper()
	key := "pop18-group-stale"
	staleID := explorer.ExplicitGroupRevisionIDFor(pop18Project, key)
	writer := "pop18-gwriter-stale"
	now := time.Now().UTC()
	s.witness(t, `INSERT INTO "`+s.schema+`".explorer_explicit_group_revisions
		(id, project, generation, scope_digest, resource_type, source_selection_revision_id,
		 source_membership_digest, definition_digest, membership_digest, group_count, member_count,
		 state, idempotency_key, created_at, completed_at, writer_token, writer_expires_at)
		VALUES ($1, $2, $3, 'scope-forged', $4, $5, $6, '', '', 0, 0, 'STAGING', $7, $8, NULL, $9, $10)`,
		string(staleID), pop18Project, pop18Generation, "Patient", fixture.source.ID,
		fixture.source.MembershipDigest, key, now, writer, now.Add(time.Hour))
	s.witness(t, `INSERT INTO "`+s.schema+`".explorer_explicit_group_definitions
		(revision_id, group_id, label, ordinal) VALUES ($1, 'group-stale', 'Stale', 0)`, string(staleID))
	s.witness(t, `INSERT INTO "`+s.schema+`".explorer_explicit_group_memberships
		(revision_id, group_id, project, generation, resource_type, resource_id)
		VALUES ($1, 'group-stale', $2, $3, 'Patient', 'R1')`, string(staleID), pop18Project, pop18Generation)
	conn, err := s.iso.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire completion channel", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(s.ctx, `SELECT set_config('loom.explorer_explicit_group_writer_token', $1, false)`, writer); err != nil {
		s.fail("set writer-token guard channel", err)
	}
	if _, err := conn.Exec(s.ctx, `UPDATE "`+s.schema+`".explorer_explicit_group_revisions
		SET state = 'COMPLETE', definition_digest = 'def-18', membership_digest = 'mem-18',
		    group_count = 1, member_count = 1, completed_at = $2
		WHERE id = $1`, string(staleID), time.Now().UTC()); err != nil {
		s.fail("complete stale header through the guarded channel", err)
	}
	return staleID
}

func TestCohortLiveDenyStillRaisesGuards(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	staging := explorer.ExplicitGroupRevision{
		ID:      explorer.ExplicitGroupRevisionIDFor(pop18Project, "pop18-group-deny-staging"),
		Project: pop18Project, Generation: pop18Generation, ScopeDigest: fixture.source.ScopeDigest,
		ResourceType: fixture.source.ResourceType, SourceSelectionRevisionID: fixture.source.ID,
		SourceMembershipDigest: fixture.source.MembershipDigest,
		State:                  explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:         "pop18-group-deny-staging", CreatedAt: time.Now().UTC(),
	}
	if _, err := s.groups.BeginExplicitGroupRevision(s.ctx, staging, fixture.source, "pop18-gwriter-deny-staging"); err != nil {
		s.fail("begin staging group for deny", err)
	}
	staleID := seedStaleGroup(t, s, fixture)
	arms := []struct {
		name     string
		revision string
		tag      string
	}{
		{"revision-invalid", string(staging.ID), "EXPLICIT_GROUP_REVISION_INVALID"},
		{"source-stale", string(staleID), "EXPLICIT_GROUP_SOURCE_STALE"},
		{"unassigned-member", string(fixture.group.ID), "EXPLICIT_GROUP_UNASSIGNED_MEMBER"},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			_, err := s.runErr(t, s.render(t,
				liveCohortPlan(arm.revision, []string{pop18PatientAuth}, false, false, "ERROR")))
			wantGuardTag(t, err, arm.tag)
		})
	}
	if rows := s.runRendered(t, s.render(t,
		liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, false, "EXCLUDE"))); len(rows) != 0 {
		t.Fatalf("healthy denied cohort must read nothing, got %v", rows)
	}
	rendered := s.render(t, liveCohortPlan(string(staging.ID), []string{pop18PatientAuth}, false, false, "ERROR"))
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	planRows := s.witness(t, "EXPLAIN (FORMAT TEXT) "+rendered.SQL, args...)
	var text strings.Builder
	for _, row := range planRows {
		text.WriteString(strings.Join(row, "\n"))
		text.WriteString("\n")
	}
	planText := text.String()
	if !strings.Contains(planText, "InitPlan") {
		t.Fatalf("guard InitPlan was pruned under canonical deny, plan:\n%s", planText)
	}
	if strings.Contains(planText, "One-Time Filter: false") {
		t.Fatalf("denied cohort folded to a constant-FALSE one-time filter, pruning the guard, plan:\n%s", planText)
	}
	t.Logf("denied cohort plan:\n%s", planText)
}

func TestCohortLiveSourceStale(t *testing.T) {
	s := newPop18Service(t)
	fixture := seedCohortFixture(t, s)
	staleID := seedStaleGroup(t, s, fixture)
	_, err := s.runErr(t, s.render(t,
		liveCohortPlan(string(staleID), []string{pop18PatientAuth}, false, true, "EXCLUDE")))
	wantGuardTag(t, err, "EXPLICIT_GROUP_SOURCE_STALE")
	rows := s.runRendered(t, s.render(t,
		liveCohortPlan(string(fixture.group.ID), []string{pop18PatientAuth}, false, true, "EXCLUDE")))
	if got := strings.Join(singleColumnRows(rows), ","); got != "R1,R2,R3" {
		t.Fatalf("unaffected cohort rows = %q, want R1,R2,R3", got)
	}
}

func reflectDeepEqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestZZNoLeftoverPop18Resources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return
	}
	if _, err := os.Stat("/proc/self/"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_pop18_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d verify_pop18_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_pop18_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d verify_pop18_* schemas still exist in the service database", schemas)
	}
}
