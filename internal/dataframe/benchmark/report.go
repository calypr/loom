package benchmark

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"
)

// The report is the deliverable. Its trust model: every derived number is
// RECOMPUTED from the raw record by VerifyReport, so a hand-edited (or
// partially forged) report fails verification. The verifier is deliberately
// independent of the harness: CI and reviewers run it on the artifact alone.

const ReportSchemaVersion = "loom.benchmark-report/v1"

var (
	sha256HexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// RunIdentity ties the report to exactly what ran.
type RunIdentity struct {
	RunID           string    `json:"runId"`
	StartedAt       time.Time `json:"startedAt"`
	Preset          string    `json:"preset"`
	SourceCommit    string    `json:"sourceCommit"`
	DatasetDigest   string    `json:"datasetDigest"` // fixture manifest digest
	ArangoVersion   string    `json:"arangoVersion"`
	PostgresVersion string    `json:"postgresVersion"`
	// ClickHouseDisabled records the ruling: dataframe legs run with
	// ClickHouse disabled on both arms so the reader path is matched.
	ClickHouseDisabled bool      `json:"clickhouseDisabled"`
	Config             RunConfig `json:"config"`
}

// ControlRuns records the measurement-integrity controls (ruling i). Each
// control must have been RUN; absent controls are a verification failure.
type ControlRuns struct {
	// StabilityCVLimit and the per-cell CVs used to decide stability.
	StabilityCVLimit float64  `json:"stabilityCvLimit"`
	UnstableCells    []string `json:"unstableCells"` // "workload/backend" cells over the limit
	// SelfRatio: backend vs itself across separate rounds; must sit at 1.
	SelfRatioArango   float64 `json:"selfRatioArango"`
	SelfRatioPostgres float64 `json:"selfRatioPostgres"`
	// EmptyResultFloor: timed no-result requests establishing the client
	// overhead floor subtracted nowhere but reported as context.
	EmptyFloorMillis float64 `json:"emptyFloorMillis"`
	// RatioPolicy: ratios are published only for workloads both arms passed.
	RatioPolicy string `json:"ratioPolicy"`
}

// Claim is one publishable performance claim.
type Claim struct {
	WorkloadID string     `json:"workloadId"`
	Arango     Summary    `json:"arango"`
	Postgres   Summary    `json:"postgres"`
	Ratio      SpeedRatio `json:"ratio"`
}

// Report is the full artifact.
type Report struct {
	Schema        string               `json:"schema"`
	Identity      RunIdentity          `json:"identity"`
	Status        string               `json:"status"` // passed | failed | blocked
	StatusReasons []string             `json:"statusReasons,omitempty"`
	Controls      ControlRuns          `json:"controls"`
	GateReports   []WorkloadGateReport `json:"gateReports"`
	Samples       []Sample             `json:"samples"`
	Summaries     []SummaryRow         `json:"summaries"`
	Claims        []Claim              `json:"claims"`
	Unsupported   []string             `json:"unsupported"`
	Exclusions    string               `json:"exclusions"`
}

// SummaryRow ties a Summary to its exact cell.
type SummaryRow struct {
	WorkloadID string     `json:"workloadId"`
	Backend    string     `json:"backend"`
	Cache      CacheLabel `json:"cache"`
	Summary    Summary    `json:"summary"`
}

// BuildReport assembles the artifact from harness-collected evidence and
// recomputes summaries/claims/status so the written file is internally
// consistent by construction. claimableOf decides per-workload eligibility
// (the harness passes the gate outcome); unstable cells are excluded from
// claims per the stability control.
func BuildReport(identity RunIdentity, controls ControlRuns, gateReports []WorkloadGateReport, samples []Sample) (*Report, error) {
	if err := identity.Config.Validate(); err != nil {
		return nil, err
	}
	status, reasons := RunStatus(gateReports)
	report := &Report{
		Schema:        ReportSchemaVersion,
		Identity:      identity,
		Status:        status,
		StatusReasons: reasons,
		Controls:      controls,
		GateReports:   gateReports,
		Samples:       samples,
		Unsupported:   UnsupportedCases(gateReports),
		Exclusions:    FormatExclusionList(gateReports),
	}
	byCell := map[string][]Sample{}
	for _, s := range samples {
		key := s.WorkloadID + "|" + s.Backend + "|" + string(s.Cache)
		byCell[key] = append(byCell[key], s)
	}
	keys := make([]string, 0, len(byCell))
	for key := range byCell {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := splitCell(key)
		report.Summaries = append(report.Summaries, SummaryRow{
			WorkloadID: parts[0], Backend: parts[1], Cache: CacheLabel(parts[2]),
			Summary: Summarize(byCell[key]),
		})
	}
	report.Claims = buildClaims(gateReports, report.Summaries, controls)
	if err := VerifyReport(report); err != nil {
		return nil, fmt.Errorf("benchmark: internally inconsistent report (bug): %w", err)
	}
	return report, nil
}

func buildClaims(gateReports []WorkloadGateReport, summaries []SummaryRow, controls ControlRuns) []Claim {
	byCell := map[string]Summary{}
	for _, row := range summaries {
		byCell[row.WorkloadID+"|"+row.Backend+"|"+string(row.Cache)] = row.Summary
	}
	unstable := map[string]bool{}
	for _, cell := range controls.UnstableCells {
		unstable[cell] = true
	}
	var claims []Claim
	for _, gate := range gateReports {
		if !gate.Claimable {
			continue // ratio-only-both-passed policy
		}
		if unstable[gate.WorkloadID+"/"+BackendArango] || unstable[gate.WorkloadID+"/"+BackendPostgres] {
			continue // stability control
		}
		a := byCell[gate.WorkloadID+"|"+BackendArango+"|"+string(CacheWarm)]
		p := byCell[gate.WorkloadID+"|"+BackendPostgres+"|"+string(CacheWarm)]
		if a.Count == 0 || p.Count == 0 || a.FailedCount != 0 || p.FailedCount != 0 {
			continue
		}
		ratio, err := NewSpeedRatio(a.MedianMillis, p.MedianMillis)
		if err != nil {
			continue
		}
		claims = append(claims, Claim{WorkloadID: gate.WorkloadID, Arango: a, Postgres: p, Ratio: ratio})
	}
	return claims
}

func splitCell(key string) [3]string {
	var out [3]string
	i, j := 0, 0
	for k := 0; k < len(key); k++ {
		if key[k] == '|' {
			out[i] = key[j:k]
			i++
			j = k + 1
		}
	}
	out[i] = key[j:]
	return out
}

// VerifyReport recomputes everything derivable and fails on any mismatch.
// It is the check a synthetic forgery must not survive (ruling ii).
func VerifyReport(report *Report) error {
	if report.Schema != ReportSchemaVersion {
		return fmt.Errorf("benchmark report: unexpected schema %q", report.Schema)
	}
	if !idRe.MatchString(report.Identity.RunID) {
		return fmt.Errorf("benchmark report: bad runId")
	}
	if err := report.Identity.Config.Validate(); err != nil {
		return fmt.Errorf("benchmark report: %w", err)
	}
	if !sha256HexRe.MatchString(report.Identity.DatasetDigest) {
		return fmt.Errorf("benchmark report: dataset digest is not sha256 hex")
	}
	// Controls must have been run.
	if report.Controls.StabilityCVLimit <= 0 || report.Controls.RatioPolicy == "" {
		return fmt.Errorf("benchmark report: measurement controls were not run (missing stability limit or ratio policy)")
	}
	if report.Controls.EmptyFloorMillis <= 0 {
		return fmt.Errorf("benchmark report: empty-result floor control was not run")
	}
	if report.Controls.SelfRatioArango <= 0 || report.Controls.SelfRatioPostgres <= 0 {
		return fmt.Errorf("benchmark report: self-ratio controls were not run")
	}
	if math.Abs(report.Controls.SelfRatioArango-1) > 0.05 || math.Abs(report.Controls.SelfRatioPostgres-1) > 0.05 {
		return fmt.Errorf("benchmark report: self-ratio %v/%v off 1.0 beyond 5%% — environment unfit for ratios",
			report.Controls.SelfRatioArango, report.Controls.SelfRatioPostgres)
	}
	// Status must follow from the gate reports.
	status, reasons := RunStatus(report.GateReports)
	if report.Status != status {
		return fmt.Errorf("benchmark report: status %q but gates imply %q", report.Status, status)
	}
	if !sameStrings(report.StatusReasons, reasons) {
		return fmt.Errorf("benchmark report: status reasons do not match gate evaluation")
	}
	// Unsupported list must match the gate reports.
	if !sameStrings(report.Unsupported, UnsupportedCases(report.GateReports)) {
		return fmt.Errorf("benchmark report: unsupported list does not match gate reports")
	}
	if report.Exclusions != FormatExclusionList(report.GateReports) {
		return fmt.Errorf("benchmark report: exclusion note does not match gate reports")
	}
	// Every sample is well-formed.
	for _, s := range report.Samples {
		if !ValidBackend(s.Backend) {
			return fmt.Errorf("benchmark report: sample has unknown backend %q", s.Backend)
		}
		if _, ok := workloadColumns(s.WorkloadID); !ok {
			return fmt.Errorf("benchmark report: sample references unknown workload %q", s.WorkloadID)
		}
		if s.Millis < 0 {
			return fmt.Errorf("benchmark report: negative duration sample")
		}
		switch s.Outcome {
		case OutcomePassed, OutcomeFailed, OutcomeTimeout, OutcomeError:
		default:
			return fmt.Errorf("benchmark report: unknown sample outcome %q", s.Outcome)
		}
	}
	// Summaries are recomputed from samples and must match.
	byCell := map[string][]Sample{}
	for _, s := range report.Samples {
		key := s.WorkloadID + "|" + s.Backend + "|" + string(s.Cache)
		byCell[key] = append(byCell[key], s)
	}
	seen := map[string]bool{}
	for _, row := range report.Summaries {
		key := row.WorkloadID + "|" + row.Backend + "|" + string(row.Cache)
		if seen[key] {
			return fmt.Errorf("benchmark report: duplicate summary cell %s", key)
		}
		seen[key] = true
		expected := Summarize(byCell[key])
		if !summaryNearEqual(expected, row.Summary) {
			return fmt.Errorf("benchmark report: summary for %s does not recompute from raw samples", key)
		}
	}
	for key := range byCell {
		if !seen[key] {
			return fmt.Errorf("benchmark report: raw samples for %s have no summary row", key)
		}
	}
	// Gate bag digests are sha256-shaped when present.
	for _, gate := range report.GateReports {
		for _, arm := range []BackendResult{gate.Arango, gate.Postgres} {
			if arm.BagDigest != "" && !sha256HexRe.MatchString(arm.BagDigest) {
				return fmt.Errorf("benchmark report: %s/%s bag digest is not sha256 hex", gate.WorkloadID, arm.Backend)
			}
		}
	}
	// Claims: each must be recomputable, claimable, stable, and both-clean.
	summaryOf := map[string]Summary{}
	for _, row := range report.Summaries {
		summaryOf[row.WorkloadID+"|"+row.Backend+"|"+string(row.Cache)] = row.Summary
	}
	claimableByID := map[string]WorkloadGateReport{}
	for _, gate := range report.GateReports {
		claimableByID[gate.WorkloadID] = gate
	}
	for _, claim := range report.Claims {
		gate, ok := claimableByID[claim.WorkloadID]
		if !ok || !gate.Claimable {
			return fmt.Errorf("benchmark report: claim for %s is not gate-claimable", claim.WorkloadID)
		}
		na := summaryOf[claim.WorkloadID+"|"+BackendArango+"|"+string(CacheWarm)]
		np := summaryOf[claim.WorkloadID+"|"+BackendPostgres+"|"+string(CacheWarm)]
		if !summaryNearEqual(na, claim.Arango) || !summaryNearEqual(np, claim.Postgres) {
			return fmt.Errorf("benchmark report: claim for %s cites summaries that do not recompute", claim.WorkloadID)
		}
		if na.Count == 0 || np.Count == 0 || na.FailedCount != 0 || np.FailedCount != 0 {
			return fmt.Errorf("benchmark report: claim for %s rests on cells with failures or no samples", claim.WorkloadID)
		}
		expected, err := NewSpeedRatio(na.MedianMillis, np.MedianMillis)
		if err != nil || math.Abs(expected.ArangoOverPostgres-claim.Ratio.ArangoOverPostgres) > 1e-9 {
			return fmt.Errorf("benchmark report: claim ratio for %s does not match medians", claim.WorkloadID)
		}
	}
	// A passed run must contain at least one claim and both arms' evidence.
	if report.Status == "passed" {
		if len(report.Claims) == 0 {
			return fmt.Errorf("benchmark report: passed run with no claims")
		}
		if err := requireBothArms(report); err != nil {
			return err
		}
	}
	return nil
}

func requireBothArms(report *Report) error {
	arms := map[string]bool{}
	for _, gate := range report.GateReports {
		if gate.Arango.Outcome == OutcomeLabelPassed {
			arms[BackendArango] = true
		}
		if gate.Postgres.Outcome == OutcomeLabelPassed {
			arms[BackendPostgres] = true
		}
	}
	if !arms[BackendArango] || !arms[BackendPostgres] {
		return fmt.Errorf("benchmark report: passed run missing a backend arm's evidence")
	}
	return nil
}

func summaryNearEqual(a, b Summary) bool {
	const eps = 1e-6
	near := func(x, y float64) bool {
		return math.Abs(x-y) <= eps*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))
	}
	return a.Count == b.Count && a.FailedCount == b.FailedCount &&
		a.ThroughputMeasured == b.ThroughputMeasured &&
		near(a.MedianMillis, b.MedianMillis) && near(a.MeanMillis, b.MeanMillis) &&
		near(a.VarianceMs, b.VarianceMs) && near(a.StdDevMs, b.StdDevMs) &&
		near(a.MinMillis, b.MinMillis) && near(a.MaxMillis, b.MaxMillis) &&
		near(a.P50Millis, b.P50Millis) && near(a.P95Millis, b.P95Millis) &&
		near(a.CoefficientOfVariation, b.CoefficientOfVariation) &&
		near(a.ThroughputPerSecond, b.ThroughputPerSecond) &&
		a.QuantileMethod == b.QuantileMethod
}

func sameStrings(a, b []string) bool {
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

// MarshalReport renders the canonical JSON bytes of the artifact.
func MarshalReport(report *Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// LoadReport parses bytes and verifies the artifact on load. This is the
// entry point CI and the node verifier use.
func LoadReport(data []byte) (*Report, error) {
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("benchmark report: %w", err)
	}
	if err := VerifyReport(&report); err != nil {
		return nil, err
	}
	return &report, nil
}
