package benchmark

import "testing"

// Measurement-integrity controls (review r4561031154): claims cite only a
// claimable regime (warm, or service-warm for attached arms) and only from
// cells the stability control accepted.

func warmRows(arangoMedian, postgresMedian float64) []SummaryRow {
	return []SummaryRow{
		{WorkloadID: "w", Backend: BackendArango, Cache: CacheWarm, Summary: Summary{Count: 5, MedianMillis: arangoMedian}},
		{WorkloadID: "w", Backend: BackendPostgres, Cache: CacheWarm, Summary: Summary{Count: 5, MedianMillis: postgresMedian}},
	}
}

func TestClaimsRequireClaimableRegime(t *testing.T) {
	gates := []WorkloadGateReport{{WorkloadID: "w", Claimable: true}}
	coldOnly := []SummaryRow{
		{WorkloadID: "w", Backend: BackendArango, Cache: CacheProcessCold, Summary: Summary{Count: 5, MedianMillis: 20}},
		{WorkloadID: "w", Backend: BackendPostgres, Cache: CacheProcessCold, Summary: Summary{Count: 5, MedianMillis: 10}},
	}
	controls := ControlRuns{StabilityCVLimit: StabilityCVLimit}
	if claims := buildClaims(gates, coldOnly, controls); len(claims) != 0 {
		t.Fatalf("process-cold-only cells must never back claims, got %v", claims)
	}
	// One arm warm, the peer only process-cold: no pair, no claim.
	split := []SummaryRow{warmRows(20, 10)[0], coldOnly[1]}
	if claims := buildClaims(gates, split, controls); len(claims) != 0 {
		t.Fatalf("a claim needs the claimable regime on both arms, got %v", claims)
	}
	claims := buildClaims(gates, warmRows(20, 10), controls)
	if len(claims) != 1 {
		t.Fatalf("warm pair with passing gates must claim, got %v", claims)
	}
	if claims[0].Ratio.ArangoOverPostgres != 2 || claims[0].Arango.MedianMillis != 20 || claims[0].Postgres.MedianMillis != 10 {
		t.Fatalf("claim cites the wrong cells: %+v", claims[0])
	}
}

func TestStabilityControlExcludesUnstableCellClaims(t *testing.T) {
	gates := []WorkloadGateReport{{WorkloadID: "w", Claimable: true}}
	rows := warmRows(20, 10)
	claims := buildClaims(gates, rows, ControlRuns{StabilityCVLimit: StabilityCVLimit, UnstableCells: []string{"w/" + BackendPostgres}})
	if len(claims) != 0 {
		t.Fatalf("an unstable cell excludes its workload's claims, got %v", claims)
	}
	// An unstable cell elsewhere does not touch this workload.
	claims = buildClaims(gates, rows, ControlRuns{StabilityCVLimit: StabilityCVLimit, UnstableCells: []string{"other/" + BackendPostgres}})
	if len(claims) != 1 {
		t.Fatalf("unrelated unstable cells must not suppress this claim, got %v", claims)
	}
	// Non-claimable gates never claim even with perfect cells.
	if claims := buildClaims([]WorkloadGateReport{{WorkloadID: "w", Claimable: false}}, rows, ControlRuns{StabilityCVLimit: StabilityCVLimit}); len(claims) != 0 {
		t.Fatalf("ratio-only-both-passed policy must hold, got %v", claims)
	}
}

// TestControlUnstableCells pins the regime discipline of the stability
// control (review r4561644054): CV is judged over the claimable steady
// regime only. The mandated spawned protocol (iterations=10, rounds=5)
// puts one legitimately-slow process-cold sample per round into every
// cell; mixing it into the CV would flag every spawned cell unstable and
// suppress all claims -- the round-2 no-claims failure re-entering
// through the control path.
func TestControlUnstableCells(t *testing.T) {
	// Spawned-protocol cell: five rounds of one process-cold outlier plus
	// four tight warm samples. Warm CV is tiny; the cold outliers must not
	// inflate it into an unstable verdict.
	var spawned []Sample
	for round := 1; round <= 5; round++ {
		cold := sample(BackendArango, "w", OutcomePassed, 400, false)
		cold.Cache = CacheProcessCold
		cold.Round, cold.Iteration = round, 1
		spawned = append(spawned, cold)
		for it, ms := range []float64{20, 20.2, 19.8, 20.1} {
			warm := sample(BackendArango, "w", OutcomePassed, ms, false)
			warm.Round, warm.Iteration = round, it+2
			spawned = append(spawned, warm)
		}
	}
	if got := ControlUnstableCells(spawned, StabilityCVLimit); len(got) != 0 {
		t.Fatalf("cold outliers of the spawned protocol must not flag a stable warm cell unstable: %v", got)
	}
	// Control of the control: relabel those same cold outliers as warm
	// and the cell must be flagged -- CV is really computed, and the
	// regime filter is what saves the spawned cell above.
	relabelled := append([]Sample(nil), spawned...)
	for i := range relabelled {
		if relabelled[i].Millis == 400 {
			relabelled[i].Cache = CacheWarm
		}
	}
	if got := ControlUnstableCells(relabelled, StabilityCVLimit); len(got) != 1 || got[0] != "w/"+BackendArango {
		t.Fatalf("warm-spread outlier must flag the cell unstable, got %v", got)
	}
	// Genuinely unstable warm cell is flagged.
	var wild []Sample
	for i, ms := range []float64{20, 200, 20, 200} {
		s := sample(BackendArango, "w", OutcomePassed, ms, false)
		s.Round, s.Iteration = i/2+1, i%2+1
		wild = append(wild, s)
	}
	if got := ControlUnstableCells(wild, StabilityCVLimit); len(got) != 1 || got[0] != "w/"+BackendArango {
		t.Fatalf("wildly varying warm cell must be unstable, got %v", got)
	}
	// Fewer than three steady samples: never silently trusted.
	thin := wild[:2]
	if got := ControlUnstableCells(thin, StabilityCVLimit); len(got) != 1 || got[0] != "w/"+BackendArango {
		t.Fatalf("two-sample cell cannot demonstrate stability, got %v", got)
	}
	// Cold-only cells carry no claims and need no stability verdict.
	coldOnly := []Sample{spawned[0]}
	if got := ControlUnstableCells(coldOnly, StabilityCVLimit); len(got) != 0 {
		t.Fatalf("process-cold-only samples are a different regime, not an unstable cell: %v", got)
	}
}
