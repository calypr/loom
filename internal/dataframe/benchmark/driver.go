package benchmark

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The live driver: spawn/attach arms, load the deterministic dataset, author
// every battery workload through public routes, pass the pre-timing
// authorization gate (ruling iii), measure interleaved, run control runs,
// and emit a report that VerifyReport must accept. Every failure aborts
// before any success-shaped artifact exists.

// ListenPlaceholder marks where the chosen listen address is rendered into
// the arm config: --config makes YAML authoritative for listen.
const ListenPlaceholder = "__BENCH_LISTEN__"

// ArmConfig describes one backend arm: either attach to a running server
// (BaseURL) or spawn one from BinaryPath with ConfigYAML.
type ArmConfig struct {
	BinaryPath string
	ConfigYAML string
	Env        []string
	BaseURL    string
	// ScrubSecrets are redacted from arm logs (DSNs, tokens).
	ScrubSecrets []string
	// ReadyURL overrides the readiness poll target for spawned arms.
	ReadyURL string
}

// SpawnFunc launches a server process; the default is provision.SpawnServer.
// Injectable so the driver's protocol is testable offline.
type SpawnFunc func(ctx context.Context, spec ServerSpec, secrets []string, listenAddr string) (*ServerProcess, error)

// VersionProbeFunc returns the LIVE backend version string for an arm
// (e.g. by executing the launched binary's --version). Required: the
// verifier refuses a passed run without observed versions.
type VersionProbeFunc func(ctx context.Context, cfg ArmConfig, backend string) (string, error)

// GateFunc and SingleArmGateFunc default to the package evaluators; the
// seam exists for protocol tests, never for production use.
type GateFunc func(workloadID string, columns []string, expected *CanonicalBag, arango, postgres BackendResult, arangoRows, postgresRows []map[string]any, idMap *StorageIDMap) WorkloadGateReport
type SingleArmGateFunc func(workloadID, backend string, columns []string, expected *CanonicalBag, arm BackendResult, rows []map[string]any, idMap *StorageIDMap, blockedDetail string) WorkloadGateReport

// DriverOptions is the full, explicit driver input. No silent defaults for
// anything that lands in the report.
type DriverOptions struct {
	RunID        string
	StartedAt    time.Time
	Plan         RunPlan
	Preset       string
	Params       DatasetParams
	Config       RunConfig
	PreviewLimit int
	SourceCommit string

	UnrestrictedToken string
	ScopedToken       string

	Arango   ArmConfig
	Postgres ArmConfig

	StderrDir string
	Clock     Clock

	Spawn        SpawnFunc
	VersionProbe VersionProbeFunc

	Gates           GateFunc
	SingleArmGates  SingleArmGateFunc
	EmptyFloorIters int
}

// ArmEvidence is the per-arm operational record kept beside the report.
type ArmEvidence struct {
	Backend string `json:"backend"`
	BaseURL string `json:"baseUrl"`
	LogPath string `json:"logPath,omitempty"`
	Version string `json:"version"`
	Spawned bool   `json:"spawned"`
}

type runningArm struct {
	arm      *Arm
	proc     *ServerProcess
	evidence ArmEvidence
}

// RunBenchmark executes the matched battery. It returns the verified report
// and per-arm evidence, or an error before any artifact is written.
func RunBenchmark(ctx context.Context, opts DriverOptions) (*Report, []ArmEvidence, error) {
	if err := ValidateLegPlan(opts.Plan.Scope, opts.Plan.Legs); err != nil {
		return nil, nil, err
	}
	if opts.Plan.Scope == RunScopePlanned {
		// The live driver spawns, authoring-runs, and times; a planned run
		// is built through BuildReport+PlannedGateReports, never here.
		return nil, nil, fmt.Errorf("benchmark: the live driver refuses planned scope; build the planned report directly")
	}
	if err := opts.Config.Validate(); err != nil {
		return nil, nil, err
	}
	if opts.PreviewLimit < 1 {
		return nil, nil, fmt.Errorf("benchmark: preview limit must be >= 1")
	}
	if strings.TrimSpace(opts.SourceCommit) == "" || len(opts.SourceCommit) != 40 {
		return nil, nil, fmt.Errorf("benchmark: source commit must be the full 40-hex SHA the arms were built from")
	}
	clock := opts.Clock
	if clock == nil {
		clock = NewMonotonicClock()
	}
	spawn := opts.Spawn
	if spawn == nil {
		spawn = SpawnServer
	}
	gates := opts.Gates
	if gates == nil {
		gates = EvaluateGates
	}
	singleGates := opts.SingleArmGates
	if singleGates == nil {
		singleGates = EvaluateGatesSingleArm
	}

	files, err := GenerateFiles(opts.Params)
	if err != nil {
		return nil, nil, err
	}

	// Participating arms, in fixed order.
	type backendArm struct {
		backend string
		cfg     ArmConfig
	}
	var participants []backendArm
	runLegs := map[string]bool{}
	for _, leg := range opts.Plan.Legs {
		if leg.Mode == "run" {
			runLegs[leg.Backend] = true
		}
	}
	for _, b := range []backendArm{{BackendArango, opts.Arango}, {BackendPostgres, opts.Postgres}} {
		if runLegs[b.backend] {
			participants = append(participants, b)
		}
	}
	arms := make(map[string]*runningArm, len(participants))
	defer func() {
		for _, r := range arms {
			if r.proc != nil {
				r.proc.Stop()
			}
		}
	}()
	for _, p := range participants {
		ra, err := startArm(ctx, p.backend, p.cfg, spawn, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("benchmark: %s arm: %w", p.backend, err)
		}
		arms[p.backend] = ra
	}

	// Load the dataset on every participating arm.
	for _, p := range participants {
		if err := arms[p.backend].arm.UploadGeneration(ctx, opts.UnrestrictedToken, opts.Params.Project, opts.Params.Generation, files.Files, opts.Params.AuthResourcePath); err != nil {
			return nil, nil, fmt.Errorf("benchmark: %s upload: %w", p.backend, err)
		}
	}

	// Author every workload on every participating arm. Explorer ids are
	// derived from names (StableExplorerID), so the arms must agree on the
	// id or the matched comparison is invalid.
	receiptIDs := map[string]map[string]string{} // backend -> workload -> receipt
	outputIDs := map[string]map[string]string{}  // backend -> workload -> output
	perBackend := map[string]map[string]string{}
	for _, p := range participants {
		perBackend[p.backend] = map[string]string{}
		receiptIDs[p.backend] = map[string]string{}
		outputIDs[p.backend] = map[string]string{}
	}
	for _, spec := range Battery {
		workspace, err := BuildWorkspace(spec.ID)
		if err != nil {
			return nil, nil, err
		}
		outputID := OutputIDFor(spec.ID)
		name := "bench-" + spec.ID
		var explorerID string
		for _, p := range participants {
			eid, rid, err := arms[p.backend].arm.AuthorWorkload(ctx, opts.UnrestrictedToken, opts.Params.Project, opts.Params.Generation,
				workspace, opts.SourceCommit, name, outputID)
			if err != nil {
				return nil, nil, fmt.Errorf("benchmark: author %s on %s: %w", spec.ID, p.backend, err)
			}
			if explorerID == "" {
				explorerID = eid
			} else if eid != explorerID {
				return nil, nil, fmt.Errorf("benchmark: %s authored under divergent explorer ids %s vs %s; arms are not matched", spec.ID, explorerID, eid)
			}
			perBackend[p.backend][spec.ID] = eid
			receiptIDs[p.backend][spec.ID] = rid
			outputIDs[p.backend][spec.ID] = outputID
		}
	}
	// One explorer identity serves both arms (asserted equal above).
	explorerIDByWorkload := perBackend[participants[0].backend]

	// Empty-floor explorers (zero-row expectation) per arm.
	floorWorkspace, err := BuildEmptyFloorWorkspace()
	if err != nil {
		return nil, nil, err
	}
	floorExplorers := map[string]string{}
	floorReceipts := map[string]string{}
	for _, p := range participants {
		eid, rid, err := arms[p.backend].arm.AuthorWorkload(ctx, opts.UnrestrictedToken, opts.Params.Project, opts.Params.Generation,
			floorWorkspace, opts.SourceCommit, "bench-empty-floor", "empty_floor")
		if err != nil {
			return nil, nil, fmt.Errorf("benchmark: author empty-floor on %s: %w", p.backend, err)
		}
		floorExplorers[p.backend] = eid
		floorReceipts[p.backend] = rid
	}

	// Pre-timing authorization gate (ruling iii). If the scoped principal
	// sees anything it was not granted, timings are meaningless: abort
	// before producing any artifact.
	authWorkload := WorkloadFilterActiveGender
	baseOpts := RunOptions{
		Project: opts.Params.Project, Generation: opts.Params.Generation,
		AuthResourcePath:  opts.Params.AuthResourcePath,
		UnrestrictedToken: opts.UnrestrictedToken, ScopedToken: opts.ScopedToken,
		PreviewLimit: opts.PreviewLimit, Config: opts.Config, SourceCommit: opts.SourceCommit,
	}
	{
		ra := RunOptions{Project: baseOpts.Project, Generation: baseOpts.Generation, AuthResourcePath: baseOpts.AuthResourcePath, UnrestrictedToken: baseOpts.UnrestrictedToken, ScopedToken: baseOpts.ScopedToken, PreviewLimit: baseOpts.PreviewLimit, Config: baseOpts.Config, SourceCommit: baseOpts.SourceCommit,
			Explorer: explorerIDByWorkload[authWorkload]}
		rc := map[string]string{}
		oi := map[string]string{}
		for _, p := range participants {
			rc[p.backend] = receiptIDs[p.backend][authWorkload]
			oi[p.backend] = outputIDs[p.backend][authWorkload]
		}
		var authReport WorkloadGateReport
		if len(participants) == 2 {
			authReport = NegativeAuthControl(ctx, arms[BackendArango].arm, arms[BackendPostgres].arm, ra, rc, oi)
		} else {
			authReport = NegativeAuthControlSingleArm(ctx, authWorkload, arms[participants[0].backend].arm, ra, rc, oi)
		}
		if !authReport.Claimable {
			return nil, nil, fmt.Errorf("benchmark: pre-timing authorization gate FAILED: %s; no artifact is produced", authReport.ExclusionReason)
		}
	}

	// Timing, workload by workload, interleaved round-robin.
	var allSamples []Sample
	var gateReports []WorkloadGateReport
	idMap := NewStorageIDMap(files.Facts)
	var cold func(ctx context.Context, arm *Arm) error
	procCold := false
	for _, p := range participants {
		if arms[p.backend].proc != nil {
			procCold = true
		}
	}
	if procCold {
		cold = func(ctx context.Context, arm *Arm) error {
			if arm == nil || arm.Proc == nil {
				return nil
			}
			return arm.Proc.Restart(ctx)
		}
	}
	for _, spec := range Battery {
		ra := RunOptions{Project: baseOpts.Project, Generation: baseOpts.Generation, AuthResourcePath: baseOpts.AuthResourcePath, UnrestrictedToken: baseOpts.UnrestrictedToken, ScopedToken: baseOpts.ScopedToken, PreviewLimit: baseOpts.PreviewLimit, Config: baseOpts.Config, SourceCommit: baseOpts.SourceCommit,
			Explorer: explorerIDByWorkload[spec.ID]}
		rc := map[string]string{}
		oi := map[string]string{}
		for _, p := range participants {
			rc[p.backend] = receiptIDs[p.backend][spec.ID]
			oi[p.backend] = outputIDs[p.backend][spec.ID]
		}
		var arangoArm, postgresArm *Arm
		if a, ok := arms[BackendArango]; ok {
			arangoArm = a.arm
		}
		if a, ok := arms[BackendPostgres]; ok {
			postgresArm = a.arm
		}
		samples, finals, err := MeasureArms(ctx, clock, spec.ID, arangoArm, postgresArm, ra, rc, oi, cold)
		if err != nil {
			return nil, nil, fmt.Errorf("benchmark: measure %s: %w", spec.ID, err)
		}
		allSamples = append(allSamples, samples...)
		var expected *CanonicalBag
		if spec.OracleAvailable {
			expected, err = OracleFor(spec.ID, files.Facts)
			if err != nil {
				return nil, nil, fmt.Errorf("benchmark: oracle %s: %w", spec.ID, err)
			}
		}
		arangoResult := backendResultFrom(BackendArango, samples, finals)
		postgresResult := backendResultFrom(BackendPostgres, samples, finals)
		var report WorkloadGateReport
		if len(participants) == 2 {
			report = gates(spec.ID, spec.Columns, expected, arangoResult, postgresResult,
				rowsOf(finals[BackendArango]), rowsOf(finals[BackendPostgres]), idMap)
		} else {
			backend := participants[0].backend
			blockedDetail := ""
			for _, leg := range opts.Plan.Legs {
				if leg.Backend == otherBackend(backend) {
					blockedDetail = "leg not run: " + leg.Capability
				}
			}
			result := arangoResult
			if backend == BackendPostgres {
				result = postgresResult
			}
			report = singleGates(spec.ID, backend, spec.Columns, expected, result, rowsOf(finals[backend]), idMap, blockedDetail)
		}
		gateReports = append(gateReports, report)
	}

	// Control runs.
	controls := ControlRuns{
		StabilityCVLimit: StabilityCVLimit,
		UnstableCells:    ControlUnstableCells(allSamples, StabilityCVLimit),
		RatioPolicy:      "ratio-only-both-passed",
	}
	for _, backend := range []string{BackendArango, BackendPostgres} {
		if !runLegs[backend] {
			continue // stays 0: absent, never forged
		}
		value, ok := ControlSelfRatio(allSamples, backend)
		if !ok {
			return nil, nil, fmt.Errorf("benchmark: %s self-ratio control cannot be computed from the collected samples; run is not fit for claims", backend)
		}
		if backend == BackendArango {
			controls.SelfRatioArango = value
		} else {
			controls.SelfRatioPostgres = value
		}
	}
	floorIters := opts.EmptyFloorIters
	if floorIters == 0 {
		floorIters = 5
	}
	worstFloor := 0.0
	for _, p := range participants {
		median, err := arms[p.backend].arm.MeasureEmptyFloor(ctx, clock, opts.UnrestrictedToken, opts.Params.Project, floorExplorers[p.backend], floorReceipts[p.backend], "empty_floor", floorIters)
		if err != nil {
			return nil, nil, fmt.Errorf("benchmark: %s empty-floor control: %w", p.backend, err)
		}
		if median > worstFloor {
			worstFloor = median
		}
	}
	controls.EmptyFloorMillis = worstFloor

	identity := RunIdentity{
		RunID: opts.RunID, StartedAt: opts.StartedAt.UTC(), Preset: opts.Preset,
		SourceCommit: opts.SourceCommit, DatasetDigest: files.Digest,
		ClickHouseDisabled: true, Config: opts.Config,
	}
	for _, p := range participants {
		version, err := opts.VersionProbe(ctx, p.cfg, p.backend)
		if err != nil {
			return nil, nil, fmt.Errorf("benchmark: %s version probe: %w", p.backend, err)
		}
		if strings.TrimSpace(version) == "" {
			return nil, nil, fmt.Errorf("benchmark: %s version probe returned an empty version", p.backend)
		}
		arms[p.backend].evidence.Version = version
		if p.backend == BackendArango {
			identity.ArangoVersion = version
		} else {
			identity.PostgresVersion = version
		}
	}

	report, err := BuildReport(identity, opts.Plan, controls, gateReports, allSamples)
	if err != nil {
		return nil, nil, fmt.Errorf("benchmark: report would not verify (refusing to emit): %w", err)
	}
	var evidence []ArmEvidence
	for _, p := range participants {
		evidence = append(evidence, arms[p.backend].evidence)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].Backend < evidence[j].Backend })
	return report, evidence, nil
}

func startArm(ctx context.Context, backend string, cfg ArmConfig, spawn SpawnFunc, opts DriverOptions) (*runningArm, error) {
	if cfg.BaseURL != "" {
		return &runningArm{
			arm:      &Arm{Backend: backend, BaseURL: strings.TrimRight(cfg.BaseURL, "/"), Client: &http.Client{Timeout: 120 * time.Second}, AuthResourcePath: opts.Params.AuthResourcePath},
			evidence: ArmEvidence{Backend: backend, BaseURL: cfg.BaseURL, Spawned: false},
		}, nil
	}
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("arm must provide either BaseURL or BinaryPath")
	}
	port, err := FreePort()
	if err != nil {
		return nil, err
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	// --config makes YAML authoritative for listen, so the chosen address
	// must be rendered into the config, not only passed as --listen.
	cfgYAML := cfg.ConfigYAML
	if addr, mark, ok := strings.Cut(cfgYAML, ListenPlaceholder); ok {
		cfgYAML = addr + listen + mark
	}
	ready := cfg.ReadyURL
	if ready == "" {
		ready = "http://" + listen + "/readyz"
	}
	timeout := 60 * time.Second
	spec := ServerSpec{BinaryPath: cfg.BinaryPath, Backend: backend, ConfigYAML: cfgYAML, Env: cfg.Env, ReadyURL: ready, Timeout: timeout, StderrDir: opts.StderrDir}
	proc, err := spawn(ctx, spec, cfg.ScrubSecrets, listen)
	if err != nil {
		return nil, err
	}
	return &runningArm{
		arm:      &Arm{Backend: backend, BaseURL: "http://" + listen, Client: &http.Client{Timeout: 120 * time.Second}, Proc: proc, AuthResourcePath: opts.Params.AuthResourcePath},
		proc:     proc,
		evidence: ArmEvidence{Backend: backend, BaseURL: "http://" + listen, LogPath: proc.LogPath(), Spawned: true},
	}, nil
}

func backendResultFrom(backend string, samples []Sample, finals map[string]PreviewResult) BackendResult {
	result := BackendResult{Backend: backend, Outcome: OutcomeLabelPassed}
	var failures []string
	for _, s := range samples {
		if s.Backend != backend || s.Warmup {
			continue
		}
		if s.Outcome != OutcomePassed {
			result.Outcome = OutcomeLabelFailed
			failures = append(failures, string(s.Outcome)+":"+s.ErrorCode)
		}
	}
	if len(failures) > 0 {
		seen := map[string]bool{}
		var uniq []string
		for _, f := range failures {
			if !seen[f] {
				seen[f] = true
				uniq = append(uniq, f)
			}
		}
		sort.Strings(uniq)
		if len(uniq) > 3 {
			uniq = uniq[:3]
		}
		result.ErrorCode = strings.Join(uniq, ";")
		return result
	}
	final, ok := finals[backend]
	if !ok {
		result.Outcome = OutcomeLabelFailed
		result.ErrorCode = "no completed measured execution"
		return result
	}
	result.Columns = final.Columns
	result.RowCount = final.RowCount
	result.Truncated = final.Sampled
	bag := Bag(CanonicalizeRows(final.Columns, final.Rows))
	result.BagDigest = bag.Digest()
	return result
}

func rowsOf(result PreviewResult) []map[string]any { return result.Rows }

// ErrAbortBeforeArtifact distinguishes "refused to run" from "run failed".
var ErrAbortBeforeArtifact = errors.New("benchmark: aborted before producing an artifact")
