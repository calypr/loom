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
