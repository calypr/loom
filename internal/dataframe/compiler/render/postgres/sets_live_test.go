package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	pgstore "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newSets08Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "sets08_it")
}

var (
	sets08FixturesMu      sync.Mutex
	sets08TrackedFixtures = map[string]bool{}
)

func sets08TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	sets08FixturesMu.Lock()
	defer sets08FixturesMu.Unlock()
	sets08TrackedFixtures[database] = true
	sets08TrackedFixtures[svc.schema] = true
}

func sets08SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'sets08_it_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'sets08_it_%'`)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func sets08RunDeltaAudit(m *testing.M) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return trvRunDeltaAudit(m)
	}
	beforeList, err := sets08SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-08 delta audit: snapshot before run: %v\n", err)
		return trvRunDeltaAudit(m)
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := trvRunDeltaAudit(m)
	after, err := sets08SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-08 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	sets08FixturesMu.Lock()
	defer sets08FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if sets08TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-08 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-08 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	survivors, err := sets08SnapshotOwnedResources(ctx, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-08 delta audit: survivor scan: %v\n", err)
		code = 1
	} else if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-08 delta audit: %d sets08_it_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

const (
	setsProj      = "proj-a"
	setsGen       = "gen-1"
	setsEdgeLabel = "has-observation"
)

func setsInsertPatient(t *testing.T, svc *pgir02Service, logicalID string) {
	t.Helper()
	svc.insertResource(t, svc.schema, setsProj, setsGen, "Patient", logicalID, "FHIR/Patient", nil)
}

func setsInsertObservation(t *testing.T, svc *pgir02Service, logicalID, authPath string, payload map[string]any) {
	t.Helper()
	svc.insertResource(t, svc.schema, setsProj, setsGen, "Observation", logicalID, authPath, payload)
}

func setsInsertRouteEdge(t *testing.T, svc *pgir02Service, from, to, sourcePath string) {
	t.Helper()
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: setsProj, DatasetGeneration: setsGen, AuthResourcePath: "FHIR/Patient",
		Label: setsEdgeLabel, SourceResourceType: "Patient", SourceLogicalID: from,
		TargetResourceType: "Observation", TargetLogicalID: to, SourcePath: sourcePath, SourceOrdinal: 0,
	})
}

func jsonMin(value string) string {
	return strings.ReplaceAll(value, " ", "")
}

func TestSetLiveReductionsMatchLiteralRowsAndWitness(t *testing.T) {
	svc := newSets08Service(t)
	sets08TrackFixtures(t, svc)

	setsInsertPatient(t, svc, "p-1")
	setsInsertPatient(t, svc, "p-2")
	setsInsertPatient(t, svc, "p-3")
	setsInsertPatient(t, svc, "p-4")
	setsInsertObservation(t, svc, "obs-final", "FHIR/Observation", map[string]any{"status": "final"})
	setsInsertObservation(t, svc, "obs-prelim", "FHIR/Observation", map[string]any{"status": "prelim"})
	setsInsertObservation(t, svc, "obs-null", "FHIR/Observation", map[string]any{"status": nil})
	setsInsertObservation(t, svc, "obs-absent", "FHIR/Observation", map[string]any{"note": "x"})
	setsInsertObservation(t, svc, "obs-c1", "FHIR/Observation", map[string]any{"status": "dup"})
	setsInsertObservation(t, svc, "obs-c2", "FHIR/Observation", map[string]any{"status": "dup"})
	setsInsertObservation(t, svc, "obs-blocked", "FHIR/Blocked", map[string]any{"status": "sneaky"})

	setsInsertRouteEdge(t, svc, "p-1", "obs-final", "observation[0]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-final", "observation[1]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-prelim", "observation[2]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-blocked", "observation[9]")
	setsInsertRouteEdge(t, svc, "p-2", "obs-null", "observation[0]")
	setsInsertRouteEdge(t, svc, "p-2", "obs-absent", "observation[1]")
	setsInsertRouteEdge(t, svc, "p-4", "obs-c1", "observation[0]")
	setsInsertRouteEdge(t, svc, "p-4", "obs-c2", "observation[1]")

	set := setsChildSet(ir.PhysicalSetReductionFirst, ir.PhysicalSetReductionAll, ir.PhysicalSetReductionDistinct)
	rendered, err := RenderPhysicalPlan(setsPlanWithSet(set,
		"__loom_reduced_0", "__loom_reduced_1", "__loom_reduced_2"), bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	for _, leaked := range []string{"p-1", "obs-final", "final", setsProj, setsGen, setsEdgeLabel, "FHIR/Patient"} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("value %q leaked into rendered SQL: %s", leaked, rendered.SQL)
		}
	}
	got := svc.runRendered(t, rendered)

	if len(got) != 4 {
		t.Fatalf("parent grain broken: %v", got)
	}
	want := [][]string{
		{"p-1", `final`, `[final prelim]`, `[final prelim]`},
		{"p-2", "<null>", `[]`, `[]`},
		{"p-3", "<null>", `[]`, `[]`},
		{"p-4", `dup`, `[dup dup]`, `[dup dup]`},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for row := range want {
		if got[row][0] != want[row][0] || got[row][1] != want[row][1] ||
			jsonMin(got[row][2]) != jsonMin(want[row][2]) || jsonMin(got[row][3]) != jsonMin(want[row][3]) {
			t.Fatalf("row %d = %v, want %v", row, got[row], want[row])
		}
	}

	witness := svc.witness(t,
		`SELECT 'p-1'::text, '"final"'::jsonb, '["final","prelim"]'::jsonb, '["final","prelim"]'::jsonb`+
			` UNION ALL SELECT 'p-2', NULL, '[]'::jsonb, '[]'::jsonb`+
			` UNION ALL SELECT 'p-3', NULL, '[]'::jsonb, '[]'::jsonb`+
			` UNION ALL SELECT 'p-4', '"dup"', '["dup","dup"]'::jsonb, '["dup","dup"]'::jsonb`+
			` ORDER BY 1`)
	if len(witness) != 4 {
		t.Fatalf("witness shape: %v", witness)
	}
	for row := range want {
		if witness[row][0] != want[row][0] || witness[row][1] != want[row][1] ||
			jsonMin(witness[row][2]) != jsonMin(want[row][2]) || jsonMin(witness[row][3]) != jsonMin(want[row][3]) {
			t.Fatalf("witness row %d = %v, want %v", row, witness[row], want[row])
		}
	}

	counts := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence"`+
			` WHERE project=$1 AND dataset_generation=$2 AND source_logical_id='p-1' AND target_logical_id='obs-final'`,
		setsProj, setsGen)
	if len(counts) != 1 || counts[0][0] != "2" {
		t.Fatalf("duplicate occurrence must be stored (UNIQUE parity needs it): %v", counts)
	}
	blocked := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2 AND logical_id='obs-blocked' AND payload #> '{status}'::text[] = '"sneaky"'::jsonb`,
		setsProj, setsGen)
	if len(blocked) != 1 || blocked[0][0] != "1" {
		t.Fatalf("denied-path child must be stored for the E4 exclusion to mean anything: %v", blocked)
	}
	for _, row := range got {
		for _, cell := range row {
			if strings.Contains(cell, "sneaky") {
				t.Fatalf("denied-path value leaked into results: %v", got)
			}
		}
	}

	if _, err := svc.iso.Exec(svc.ctx,
		`UPDATE "`+svc.schema+`"."fhir_resource" SET payload = '{"status":"withdrawn"}'::jsonb`+
			` WHERE project=$1 AND dataset_generation=$2 AND logical_id='obs-prelim'`,
		setsProj, setsGen); err != nil {
		svc.fail("mutation probe", err)
	}
	mutated := svc.runRendered(t, rendered)
	if len(mutated) != 4 || jsonMin(mutated[0][2]) != jsonMin(`[final withdrawn]`) {
		t.Fatalf("mutation probe did not move the aggregate: %v", mutated)
	}
}

func TestSetLiveRelatedExistsAndNegationAreSemiJoins(t *testing.T) {
	svc := newSets08Service(t)
	sets08TrackFixtures(t, svc)

	setsInsertPatient(t, svc, "p-has")
	setsInsertPatient(t, svc, "p-none")
	setsInsertPatient(t, svc, "p-denied")
	setsInsertObservation(t, svc, "obs-live", "FHIR/Observation", map[string]any{"status": "final"})
	setsInsertObservation(t, svc, "obs-hidden", "FHIR/Blocked", map[string]any{"status": "final"})
	setsInsertRouteEdge(t, svc, "p-has", "obs-live", "observation[0]")
	setsInsertRouteEdge(t, svc, "p-denied", "obs-hidden", "observation[0]")

	positive := svc.runRendered(t, mustRender(t, setsRelatedExistsPlan(false), svc.schema))
	if len(positive) != 1 || positive[0][0] != "p-has" {
		t.Fatalf("EXISTS form must keep exactly the parent with a reachable child: %v", positive)
	}
	negative := svc.runRendered(t, mustRender(t, setsRelatedExistsPlan(true), svc.schema))
	if len(negative) != 2 {
		t.Fatalf("NOT(EXISTS) must keep the non-matching parents (p-none and p-denied): %v", negative)
	}
	if negative[0][0] != "p-none" || negative[1][0] != "p-denied" {
		t.Fatalf("NOT(EXISTS) rows = %v, want p-none then p-denied", negative)
	}

	stored := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2`,
		setsProj, setsGen)
	if len(stored) != 1 || stored[0][0] != "2" {
		t.Fatalf("both route occurrences must be stored: %v", stored)
	}
	hidden := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project=$1 AND dataset_generation=$2 AND logical_id='obs-hidden'`,
		setsProj, setsGen)
	if len(hidden) != 1 || hidden[0][0] != "1" {
		t.Fatalf("denied-path child must exist for the E4 leg to mean anything: %v", hidden)
	}
}

func mustRender(t *testing.T, plan ir.PhysicalPlan, schema string) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered
}

func TestSetLiveRelatedExistsChainedRouteSemiJoin(t *testing.T) {
	svc := newSets08Service(t)
	sets08TrackFixtures(t, svc)

	setsInsertPatient(t, svc, "p-chain")
	setsInsertPatient(t, svc, "p-deadend")
	svc.insertResource(t, svc.schema, setsProj, setsGen, "Observation", "obs-mid", "FHIR/Observation", nil)
	svc.insertResource(t, svc.schema, setsProj, setsGen, "Observation", "obs-alt", "FHIR/Observation", nil)
	svc.insertResource(t, svc.schema, setsProj, setsGen, "Specimen", "spec-1", "FHIR/Specimen", nil)
	setsInsertRouteEdge(t, svc, "p-chain", "obs-mid", "observation[0]")
	setsInsertRouteEdge(t, svc, "p-deadend", "obs-alt", "observation[0]")
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: setsProj, DatasetGeneration: setsGen, AuthResourcePath: "FHIR/Observation",
		Label: "has-specimen", SourceResourceType: "Observation", SourceLogicalID: "obs-mid",
		TargetResourceType: "Specimen", TargetLogicalID: "spec-1", SourcePath: "specimen[0]", SourceOrdinal: 0,
	})

	plan := setsRelatedExistsTwoHopPlan()
	plan.BindVars["auth_resource_paths"] = []string{"FHIR/Patient", "FHIR/Observation", "FHIR/Specimen"}
	positive := svc.runRendered(t, mustRender(t, plan, svc.schema))
	if len(positive) != 1 || positive[0][0] != "p-chain" {
		t.Fatalf("chained EXISTS must keep only the parent whose second hop resolves: %v", positive)
	}

	firstHop := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence"`+
			` WHERE project=$1 AND dataset_generation=$2 AND label=$3 AND source_logical_id='p-deadend'`,
		setsProj, setsGen, setsEdgeLabel)
	if len(firstHop) != 1 || firstHop[0][0] != "1" {
		t.Fatalf("dead-end parent must store its first hop for the exclusion to mean anything: %v", firstHop)
	}
	secondHop := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence"`+
			` WHERE project=$1 AND dataset_generation=$2 AND label='has-specimen'`,
		setsProj, setsGen)
	if len(secondHop) != 1 || secondHop[0][0] != "1" {
		t.Fatalf("the chained occurrence must be stored exactly once: %v", secondHop)
	}
}
