package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newPWin01Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "pwin01_it")
}

var (
	pwin01FixturesMu      sync.Mutex
	pwin01TrackedFixtures = map[string]bool{}
)

func pwin01TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	pwin01FixturesMu.Lock()
	defer pwin01FixturesMu.Unlock()
	pwin01TrackedFixtures[database] = true
	pwin01TrackedFixtures[svc.schema] = true
}

func pwin01SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'pwin01_it_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'pwin01_it_%'`)
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

func pwin01RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := pwin01SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pwin-01 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := pwin01SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pwin-01 delta audit: survivor scan: %v\n", err)
		return 1
	}
	var survivors []string
	for _, name := range after {
		if !before[name] {
			survivors = append(survivors, name)
		}
	}
	if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pwin-01 delta audit: %d pwin01_it_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

// TestPostWindowLiveReducersExecuteWithPredicates executes the accepted post-window aggregate
// statements on a live PostgreSQL 18 instance. The items subselect is (SELECT ... FROM edge
// JOIN resource WHERE <predicate over the target payload>) AS a<seq>: a FROM alias is not
// visible inside the subselect it defines, so a predicate built over the items alias must be
// rejected at plan analysis ("invalid reference to FROM-clause entry") even though substring
// assertions on the rendered SQL cannot see it. Seeded fixtures pin the semantics too: a child
// reached by two edge occurrences contributes twice, and a predicate-mismatched child is
// excluded from every reducer.
func TestPostWindowLiveReducersExecuteWithPredicates(t *testing.T) {
	svc := newPWin01Service(t)
	pwin01TrackFixtures(t, svc)

	setsInsertPatient(t, svc, "p-1")
	setsInsertPatient(t, svc, "p-2")
	setsInsertObservation(t, svc, "obs-a", "FHIR/Observation", map[string]any{
		"code":          map[string]any{"coding": []any{map[string]any{"code": "asthma"}}},
		"valueQuantity": map[string]any{"value": 10},
	})
	setsInsertObservation(t, svc, "obs-b", "FHIR/Observation", map[string]any{
		"code":          map[string]any{"coding": []any{map[string]any{"code": "asthma"}}},
		"valueQuantity": map[string]any{"value": 30},
	})
	setsInsertObservation(t, svc, "obs-c", "FHIR/Observation", map[string]any{
		"code":          map[string]any{"coding": []any{map[string]any{"code": "diabetes"}}},
		"valueQuantity": map[string]any{"value": 99},
	})
	setsInsertObservation(t, svc, "obs-d", "FHIR/Observation", map[string]any{
		"valueQuantity": map[string]any{"value": 5},
	})

	setsInsertRouteEdge(t, svc, "p-1", "obs-a", "observation[0]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-a", "observation[1]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-b", "observation[2]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-c", "observation[3]")
	setsInsertRouteEdge(t, svc, "p-1", "obs-d", "observation[4]")
	setsInsertRouteEdge(t, svc, "p-2", "obs-c", "observation[0]")

	plan := postWindowAggregatePlan(postWindowAggregateSet(false),
		postWindowAggregateProjection("sum_age", ir.PhysicalSumAggregate, true, true),
		postWindowAggregateProjection("count_age", ir.PhysicalCountAggregate, false, true),
		postWindowAggregateProjection("min_age", ir.PhysicalMinAggregate, true, true),
		postWindowAggregateProjection("max_age", ir.PhysicalMaxAggregate, true, true))
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	got := svc.runRendered(t, rendered)

	want := [][]string{
		{"p-1", "50", "3", "10", "30"},
		{"p-2", "<null>", "0", "<null>", "<null>"},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for row := range want {
		if strings.Join(got[row], "|") != strings.Join(want[row], "|") {
			t.Fatalf("row %d = %v, want %v", row, got[row], want[row])
		}
	}

	counts := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence"`+
			` WHERE project=$1 AND dataset_generation=$2 AND source_logical_id='p-1' AND target_logical_id='obs-a'`,
		setsProj, setsGen)
	if len(counts) != 1 || counts[0][0] != "2" {
		t.Fatalf("duplicate occurrence must be stored for the per-occurrence grain: %v", counts)
	}
}
