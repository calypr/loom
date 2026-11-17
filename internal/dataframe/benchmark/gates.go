package benchmark

import (
	"fmt"
	"sort"
	"strings"
)

// Correctness gates run BEFORE any timing is reported. A workload is
// measurable only when every gate below passes for it; a gate failure keeps
// raw evidence, classifies the workload honestly, and excludes its timings
// from performance claims. There is no path from an empty or absent result
// to a pass.

// Backend identity is closed: exactly the two compared engines.
const (
	BackendArango   = "arango"
	BackendPostgres = "postgres"
)

func ValidBackend(name string) bool {
	return name == BackendArango || name == BackendPostgres
}

// WorkloadOutcome is the per-(workload, backend) classification. Every value
// is reportable; none of them collapses into a silent skip.
type WorkloadOutcome string

const (
	// OutcomeLabelPassed: correctness passed; timings are claimable.
	OutcomeLabelPassed WorkloadOutcome = "passed"
	// OutcomeLabelMismatch: outputs diverge from oracle or from the other
	// backend under the canonical contract. Fails the run.
	OutcomeLabelMismatch WorkloadOutcome = "mismatch"
	// OutcomeLabelUnsupported: the backend typed-refused the plan (e.g.
	// ErrUnsupportedPostgresPlan). Published, never hidden.
	OutcomeLabelUnsupported WorkloadOutcome = "unsupported"
	// OutcomeLabelFailed: execution error with an identity.
	OutcomeLabelFailed WorkloadOutcome = "failed"
	// OutcomeLabelTimeout: the attempt exceeded its deadline.
	OutcomeLabelTimeout WorkloadOutcome = "timeout"
	// OutcomeLabelBlocked: a prerequisite (service, dataset) never became
	// available. The whole run is blocked; never a pass.
	OutcomeLabelBlocked WorkloadOutcome = "blocked"
)

// GateName names one correctness gate.
type GateName string

const (
	GateOracleRows       GateName = "oracle_rows"       // tiny fixtures vs independently derived oracle
	GateBagEquality      GateName = "bag_equality"      // cross-backend canonical bag equality
	GateColumnContract   GateName = "column_contract"   // identical public column sets
	GateRowCount         GateName = "row_count"         // identical total row counts
	GateNegativeAuth     GateName = "negative_auth"     // scoped denial returns zero rows, both arms
	GateAuthUnrestricted GateName = "auth_unrestricted" // unrestricted principal sees the unrestricted baseline
	GateCompleteness     GateName = "completeness"      // sampled=false when the expectation is a complete output
	GateArmOutcome       GateName = "arm_outcome"       // each arm executed to completion
)

// GateResult records one gate evaluation with the evidence identity that
// decided it (digests, never raw rows).
type GateResult struct {
	Gate   GateName `json:"gate"`
	Pass   bool     `json:"pass"`
	Detail string   `json:"detail,omitempty"`
}

// BackendResult is one arm's execution evidence for a workload.
type BackendResult struct {
	Backend   string          `json:"backend"`
	Outcome   WorkloadOutcome `json:"outcome"`
	Columns   []string        `json:"columns,omitempty"`
	RowCount  int             `json:"rowCount"`
	Truncated bool            `json:"truncated"` // preview bound hit (sampled=true)
	BagDigest string          `json:"bagDigest,omitempty"`
	ErrorCode string          `json:"errorCode,omitempty"`
	Detail    string          `json:"detail,omitempty"`
}

// WorkloadGateReport ties one workload's both-arm evaluation together.
type WorkloadGateReport struct {
	WorkloadID      string        `json:"workloadId"`
	Arango          BackendResult `json:"arango"`
	Postgres        BackendResult `json:"postgres"`
	Gates           []GateResult  `json:"gates"`
	Claimable       bool          `json:"claimable"` // BOTH arms passed every gate
	ExclusionReason string        `json:"exclusionReason,omitempty"`
}

// EvaluateGates applies every gate for one workload. expected may be nil
// (representative profile without a hand oracle): then only cross-backend
// equality gates apply, and that fact is recorded in the gate detail.
//
// Rules that must never relax:
//   - either arm failing/timing out/unsupported => not claimable (and the
//     run-level status is failed if it was a mismatch);
//   - empty results pass only when the expectation IS empty;
//   - truncated outputs are never treated as complete;
//   - identity columns are mapped before bagging (storage IDs are not
//     comparison material).
func EvaluateGates(workloadID string, columns []string, expected *CanonicalBag, arango, postgres BackendResult, arangoRows, postgresRows []map[string]any, idMap *StorageIDMap) WorkloadGateReport {
	report := WorkloadGateReport{WorkloadID: workloadID, Arango: arango, Postgres: postgres, Claimable: true}
	add := func(gate GateName, pass bool, format string, args ...any) {
		result := GateResult{Gate: gate, Pass: pass}
		if !pass {
			result.Detail = fmt.Sprintf(format, args...)
			report.Claimable = false
			if report.ExclusionReason == "" {
				report.ExclusionReason = string(gate) + ": " + result.Detail
			}
		}
		report.Gates = append(report.Gates, result)
	}

	// Structural outcome gates first: unsupported/failed/timeout/blocked are
	// published as-is and exclude timings.
	for _, arm := range []BackendResult{arango, postgres} {
		if arm.Outcome != OutcomeLabelPassed {
			add(GateArmOutcome, false, "%s arm outcome=%s (error=%s)", arm.Backend, arm.Outcome, arm.ErrorCode)
		}
	}
	if !report.Claimable {
		return report
	}

	// Column contract: identical public column sets, same order.
	add(GateColumnContract, equalStrings(arango.Columns, postgres.Columns),
		"column sets differ: arango=%d columns, postgres=%d columns", len(arango.Columns), len(postgres.Columns))
	if columns != nil {
		add(GateColumnContract, equalStrings(arango.Columns, columns), "arango columns deviate from the authored contract")
		add(GateColumnContract, equalStrings(postgres.Columns, columns), "postgres columns deviate from the authored contract")
	}

	// Row counts.
	add(GateRowCount, arango.RowCount == postgres.RowCount, "row counts differ: arango=%d postgres=%d", arango.RowCount, postgres.RowCount)

	// Completeness: a claim of equality needs complete arms (or an
	// explicitly truncated expectation, which the caller encodes by passing
	// expected=nil plus Truncated-consistent arms).
	for _, arm := range []BackendResult{arango, postgres} {
		if arm.Truncated {
			add(GateCompleteness, false, "%s arm consumed a truncated output; equality claims need complete consumption", arm.Backend)
		}
	}

	// Map storage identities, canonicalize, and compare bags.
	arangoMapped := idMap.Apply(arango.Columns, arangoRows)
	postgresMapped := idMap.Apply(postgres.Columns, postgresRows)
	arangoBag := Bag(CanonicalizeRows(arango.Columns, arangoMapped))
	postgresBag := Bag(CanonicalizeRows(postgres.Columns, postgresMapped))
	add(GateBagEquality, arango.BagDigest == arangoBag.Digest(), "arango bag digest does not reproduce its reported digest")
	add(GateBagEquality, postgres.BagDigest == postgresBag.Digest(), "postgres bag digest does not reproduce its reported digest")
	if firstA, firstB, diff := FirstDifference(arangoBag, postgresBag); diff {
		add(GateBagEquality, false, "canonical bags differ at first divergence arango=%s postgres=%s", firstA, firstB)
	}

	// Independent oracle comparison (tiny deterministic fixtures only).
	if expected != nil {
		if arangoBag.Digest() != expected.Digest() {
			firstA, firstB, _ := FirstDifference(arangoBag, *expected)
			add(GateOracleRows, false, "arango bag differs from independent oracle at arango=%s oracle=%s", firstA, firstB)
		}
		if postgresBag.Digest() != expected.Digest() {
			firstA, firstB, _ := FirstDifference(postgresBag, *expected)
			add(GateOracleRows, false, "postgres bag differs from independent oracle at postgres=%s oracle=%s", firstA, firstB)
		}
	} else {
		report.Gates = append(report.Gates, GateResult{Gate: GateOracleRows, Pass: true, Detail: "no independent oracle for this profile; cross-backend equality only"})
	}
	return report
}

// EvaluateNegativeAuth is the pre-timing authorization gate. It runs BEFORE
// any timing is reported for the workload it guards. The scoped principal
// must receive zero rows for data outside its grant on BOTH arms; the
// unrestricted principal must see the unrestricted baseline, proving the
// denial was authorization, not an empty dataset or a dead route.
func EvaluateNegativeAuth(workloadID string, unrestrictedRows int, scopedArangoRows, scopedPostgresRows int, scopedArangoErr, scopedPostgresErr string) WorkloadGateReport {
	report := WorkloadGateReport{WorkloadID: workloadID, Claimable: true}
	add := func(pass bool, format string, args ...any) {
		result := GateResult{Gate: GateNegativeAuth, Pass: pass}
		if !pass {
			result.Detail = fmt.Sprintf(format, args...)
			report.Claimable = false
			if report.ExclusionReason == "" {
				report.ExclusionReason = "negative_auth: " + result.Detail
			}
		}
		report.Gates = append(report.Gates, result)
	}
	if unrestrictedRows <= 0 {
		add(false, "unrestricted baseline has %d rows; a denial against an empty dataset proves nothing", unrestrictedRows)
		return report
	}
	if scopedArangoErr != "" || scopedPostgresErr != "" {
		add(false, "scoped read errored (arango=%q postgres=%q); a transport failure is not a denial", scopedArangoErr, scopedPostgresErr)
		return report
	}
	add(scopedArangoRows == 0, "arango scoped principal saw %d rows outside its grant", scopedArangoRows)
	add(scopedPostgresRows == 0, "postgres scoped principal saw %d rows outside its grant", scopedPostgresRows)
	report.Gates = append(report.Gates, GateResult{Gate: GateAuthUnrestricted, Pass: true, Detail: fmt.Sprintf("unrestricted baseline rows=%d", unrestrictedRows)})
	return report
}

// RunStatus aggregates workload gate reports into the run verdict. Mismatch,
// failure, or timeout in any gate-marked workload fails the run; blocked
// arms block it. Empty workload sets are refused rather than treated as
// success-shaped passes.
func RunStatus(reports []WorkloadGateReport) (status string, reasons []string) {
	if len(reports) == 0 {
		return "failed", []string{"no workloads were evaluated; an empty matrix is not a pass"}
	}
	claimable := 0
	byID := make([]WorkloadGateReport, len(reports))
	copy(byID, reports)
	sort.Slice(byID, func(i, j int) bool { return byID[i].WorkloadID < byID[j].WorkloadID })
	for _, report := range byID {
		switch {
		case report.Arango.Outcome == OutcomeLabelBlocked || report.Postgres.Outcome == OutcomeLabelBlocked:
			reasons = append(reasons, report.WorkloadID+": blocked")
		case !report.Claimable:
			reasons = append(reasons, report.WorkloadID+": "+report.ExclusionReason)
		default:
			claimable++
		}
	}
	if len(reasons) > 0 {
		return "failed", reasons
	}
	if claimable == 0 {
		return "failed", []string{"no workload completed both arms; unsupported-only matrices are not passes"}
	}
	return "passed", nil
}

// ClaimableTimings decides whether a sample set may feed performance claims.
// Samples of non-claimable workloads remain in the raw record and are
// excluded here; Summarize must not be called for them on claim paths.
func ClaimableTimings(sampleBackend string, report WorkloadGateReport) bool {
	if !report.Claimable {
		return false
	}
	arm := report.Arango
	if sampleBackend == BackendPostgres {
		arm = report.Postgres
	}
	return arm.Outcome == OutcomeLabelPassed
}

// UnsupportedCases returns the published unsupported matrix rows: every
// (workload, backend) pair typed-unsupported. Publishing this list is
// required so unsupported coverage is visible, never success-shaped.
func UnsupportedCases(reports []WorkloadGateReport) []string {
	var out []string
	for _, report := range reports {
		if report.Arango.Outcome == OutcomeLabelUnsupported {
			out = append(out, report.WorkloadID+"/"+BackendArango)
		}
		if report.Postgres.Outcome == OutcomeLabelUnsupported {
			out = append(out, report.WorkloadID+"/"+BackendPostgres)
		}
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FormatExclusionList renders the excluded-timings note for the report.
func FormatExclusionList(reports []WorkloadGateReport) string {
	var excluded []string
	for _, report := range reports {
		if !report.Claimable {
			excluded = append(excluded, fmt.Sprintf("%s (%s)", report.WorkloadID, strings.TrimSpace(report.ExclusionReason)))
		}
	}
	if len(excluded) == 0 {
		return "no workloads excluded from performance claims"
	}
	return "timings excluded (correctness not passed): " + strings.Join(excluded, "; ")
}

// EvaluateNegativeAuthSingleArm applies the pre-timing authorization gate
// for a single participating arm (postgres-only sessions): the ran arm must
// see data unrestricted and zero rows scoped; the absent arm is disclosed,
// never imputed a pass.
func EvaluateNegativeAuthSingleArm(workloadID, backend string, unrestrictedRows, scopedRows int, scopedErr string) WorkloadGateReport {
	report := WorkloadGateReport{WorkloadID: workloadID, Claimable: true}
	add := func(pass bool, format string, args ...any) {
		result := GateResult{Gate: GateNegativeAuth, Pass: pass}
		if !pass {
			result.Detail = fmt.Sprintf(format, args...)
			report.Claimable = false
			if report.ExclusionReason == "" {
				report.ExclusionReason = "negative_auth: " + result.Detail
			}
		}
		report.Gates = append(report.Gates, result)
	}
	if unrestrictedRows <= 0 {
		add(false, "unrestricted baseline has %d rows; a denial against an empty dataset proves nothing", unrestrictedRows)
		return report
	}
	if scopedErr != "" {
		add(false, "%s scoped read errored (%q); a transport failure is not a denial", backend, scopedErr)
		return report
	}
	add(scopedRows == 0, "%s scoped principal saw %d rows outside its grant", backend, scopedRows)
	report.Gates = append(report.Gates, GateResult{Gate: GateAuthUnrestricted, Pass: true,
		Detail: fmt.Sprintf("unrestricted baseline rows=%d; other arm disclosed as not run", unrestrictedRows)})
	return report
}
