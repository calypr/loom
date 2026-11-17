package benchmark

import (
	"testing"
)

func passingArm(backend string, columns []string, rows []map[string]any) (BackendResult, []map[string]any) {
	mapped := rows
	return BackendResult{
		Backend: backend, Outcome: OutcomeLabelPassed, Columns: columns,
		RowCount: len(rows), BagDigest: Bag(CanonicalizeRows(columns, mapped)).Digest(),
	}, rows
}

func armsAgree(columns []string, rows []map[string]any) (BackendResult, BackendResult, []map[string]any, []map[string]any) {
	a, ar := passingArm(BackendArango, columns, rows)
	p, pr := passingArm(BackendPostgres, columns, rows)
	return a, p, ar, pr
}

func TestEvaluateGatesHappyPath(t *testing.T) {
	facts := tinyFacts(t)
	idMap := NewStorageIDMap(facts)
	columns := []string{"patient_id", "gender"}
	rows := []map[string]any{{"patient_id": facts.Patients[1].ID, "gender": "female"}}
	a, p, ar, pr := armsAgree(columns, rows)
	expected := mustBag(t, columns, rows)
	report := EvaluateGates(WorkloadFilterActiveGender, columns, expected, a, p, ar, pr, idMap)
	if !report.Claimable {
		t.Fatalf("matching arms must be claimable: %+v", report)
	}
}

func mustBag(t *testing.T, columns []string, rows []map[string]any) *CanonicalBag {
	t.Helper()
	bag := Bag(CanonicalizeRows(columns, rows))
	return &bag
}

func TestEvaluateGatesMismatchFails(t *testing.T) {
	columns := []string{"patient_id", "gender"}
	a, ar := passingArm(BackendArango, columns, []map[string]any{{"patient_id": "x", "gender": "female"}})
	p, pr := passingArm(BackendPostgres, columns, []map[string]any{{"patient_id": "x", "gender": "male"}})
	report := EvaluateGates("w", columns, nil, a, p, ar, pr, &StorageIDMap{})
	if report.Claimable {
		t.Fatal("divergent arms must not be claimable")
	}
	if report.ExclusionReason == "" {
		t.Fatal("mismatch must record an exclusion reason")
	}
}

func TestEvaluateGatesEmptyNeedsEmptyExpectation(t *testing.T) {
	columns := []string{"v"}
	// Both arms empty: claimable only because the EXPECTATION is empty.
	a, p, ar, pr := armsAgree(columns, nil)
	report := EvaluateGates("w", columns, mustBag(t, columns, nil), a, p, ar, pr, &StorageIDMap{})
	if !report.Claimable {
		t.Fatalf("empty-vs-empty-expectation is a legitimate pass: %+v", report)
	}
	// Both arms empty against a NON-empty expectation: never a pass.
	nonEmpty := mustBag(t, columns, []map[string]any{{"v": "x"}})
	report = EvaluateGates("w", columns, nonEmpty, a, p, ar, pr, &StorageIDMap{})
	if report.Claimable {
		t.Fatal("empty results must never pass against a non-empty expectation")
	}
}

func TestEvaluateGatesTruncatedNeverComplete(t *testing.T) {
	columns := []string{"v"}
	rows := []map[string]any{{"v": "x"}}
	a, _ := passingArm(BackendArango, columns, rows)
	a.Truncated = true
	p, _ := passingArm(BackendPostgres, columns, rows)
	report := EvaluateGates("w", columns, mustBag(t, columns, rows), a, p, rows, rows, &StorageIDMap{})
	if report.Claimable {
		t.Fatal("a truncated arm must never be treated as complete")
	}
}

func TestEvaluateGatesUnsupportedPublishedNotHidden(t *testing.T) {
	columns := []string{"v"}
	rows := []map[string]any{{"v": "x"}}
	a, p, ar, pr := armsAgree(columns, rows)
	p.Outcome = OutcomeLabelUnsupported
	p.ErrorCode = "ErrUnsupportedPostgresPlan"
	p.BagDigest = ""
	report := EvaluateGates("w", columns, nil, a, p, ar, pr, &StorageIDMap{})
	if report.Claimable {
		t.Fatal("unsupported arm excludes timings")
	}
	unsupported := UnsupportedCases([]WorkloadGateReport{report})
	if len(unsupported) != 1 || unsupported[0] != "w/postgres" {
		t.Fatalf("unsupported case must be published: %v", unsupported)
	}
}

func TestEvaluateGatesColumnContract(t *testing.T) {
	rows := []map[string]any{{"v": "x"}}
	a, _ := passingArm(BackendArango, []string{"v", "extra"}, rows)
	p, _ := passingArm(BackendPostgres, []string{"v"}, rows)
	report := EvaluateGates("w", []string{"v"}, nil, a, p, rows, rows, &StorageIDMap{})
	if report.Claimable {
		t.Fatal("column-set divergence must fail the contract gate")
	}
}

func TestEvaluateGatesRowDoesNotReproduceDigest(t *testing.T) {
	columns := []string{"v"}
	rows := []map[string]any{{"v": "x"}}
	a, p, ar, pr := armsAgree(columns, rows)
	a.BagDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	report := EvaluateGates("w", columns, nil, a, p, ar, pr, &StorageIDMap{})
	if report.Claimable {
		t.Fatal("a reported digest that does not reproduce from the rows must fail")
	}
}

func TestNegativeAuthRules(t *testing.T) {
	// Baseline has rows, scoped sees zero: pass.
	r := EvaluateNegativeAuth("w", 10, 0, 0, "", "")
	if !r.Claimable {
		t.Fatalf("proper denial should pass: %+v", r)
	}
	// Empty baseline proves nothing.
	r = EvaluateNegativeAuth("w", 0, 0, 0, "", "")
	if r.Claimable {
		t.Fatal("denial against an empty dataset must not pass")
	}
	// Scoped sees rows => leak.
	r = EvaluateNegativeAuth("w", 10, 3, 0, "", "")
	if r.Claimable {
		t.Fatal("scoped principal seeing rows must fail")
	}
	// Transport error is not a denial.
	r = EvaluateNegativeAuth("w", 10, 0, 0, "boom", "")
	if r.Claimable {
		t.Fatal("errored scoped read must not count as denial")
	}
}

func TestRunStatusVerdicts(t *testing.T) {
	if status, _ := RunStatus(nil); status != "failed" {
		t.Fatal("empty matrix is not a pass")
	}
	ok := WorkloadGateReport{WorkloadID: "a", Claimable: true,
		Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelPassed},
		Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelPassed}}
	bad := WorkloadGateReport{WorkloadID: "b", Claimable: false, ExclusionReason: "gate x",
		Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelPassed},
		Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelMismatch}}
	if status, _ := RunStatus([]WorkloadGateReport{ok}); status != "passed" {
		t.Fatal("all-claimable matrix must pass")
	}
	if status, reasons := RunStatus([]WorkloadGateReport{ok, bad}); status != "failed" || len(reasons) != 1 {
		t.Fatalf("one bad workload fails the run: %v %v", status, reasons)
	}
	// Unsupported-only: no arm passed => not a pass.
	unsupportedOnly := WorkloadGateReport{WorkloadID: "u", Claimable: false,
		Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelUnsupported},
		Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelUnsupported}}
	if status, _ := RunStatus([]WorkloadGateReport{unsupportedOnly}); status != "failed" {
		t.Fatal("unsupported-only matrix must not read as success")
	}
	// Blocked surfaces distinctly.
	blocked := WorkloadGateReport{WorkloadID: "x", Claimable: false,
		Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelPassed},
		Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelBlocked}}
	if _, reasons := RunStatus([]WorkloadGateReport{blocked}); len(reasons) != 1 || reasons[0] != "x: blocked" {
		t.Fatalf("blocked must be named: %v", reasons)
	}
}

func TestClaimableTimingsRule(t *testing.T) {
	report := WorkloadGateReport{WorkloadID: "a", Claimable: true,
		Arango:   BackendResult{Backend: BackendArango, Outcome: OutcomeLabelPassed},
		Postgres: BackendResult{Backend: BackendPostgres, Outcome: OutcomeLabelPassed}}
	if !ClaimableTimings(BackendArango, report) || !ClaimableTimings(BackendPostgres, report) {
		t.Fatal("claimable both-arms cell must allow timings")
	}
	report.Postgres.Outcome = OutcomeLabelFailed
	report.Claimable = false
	if ClaimableTimings(BackendArango, report) {
		t.Fatal("timings on a not-claimable workload are forbidden even for the passing arm")
	}
}

func TestFormatExclusionList(t *testing.T) {
	if got := FormatExclusionList(nil); got == "" {
		t.Fatal("exclusion note must never be blank")
	}
	report := WorkloadGateReport{WorkloadID: "w", ExclusionReason: "gate: detail", Claimable: false}
	got := FormatExclusionList([]WorkloadGateReport{report})
	if got == "" || got == "no workloads excluded from performance claims" {
		t.Fatalf("exclusion must list the workload: %q", got)
	}
}
