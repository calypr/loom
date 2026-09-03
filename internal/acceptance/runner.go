package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Connections struct {
	LoomURL            string
	ArangoURL          string
	ClickHouseURL      string
	ClickHouseUsername string
	ClickHousePassword string
}

type Namespace struct {
	Run                string `json:"run"`
	Project            string `json:"project"`
	Generation         string `json:"generation"`
	ArangoDatabase     string `json:"arango_database"`
	ClickHouseDatabase string `json:"clickhouse_database"`
}

type RunSpec struct {
	RunID              RunID
	Project            string
	Generation         string
	ArangoDatabase     string
	ClickHouseDatabase string
}

type Lease interface {
	Connections() Connections
	Namespace() Namespace
	Close(context.Context) error
}

type Target interface {
	Acquire(context.Context, RunSpec) (Lease, error)
}

type StaticTarget struct{ Conn Connections }

func (t StaticTarget) Acquire(_ context.Context, spec RunSpec) (Lease, error) {
	ns, err := namespaceFromSpec(spec)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(t.Conn.LoomURL) == "" || strings.TrimSpace(t.Conn.ArangoURL) == "" || strings.TrimSpace(t.Conn.ClickHouseURL) == "" {
		return nil, errors.New("static target requires Loom, ArangoDB, and ClickHouse URLs")
	}
	return staticLease{conn: t.Conn, ns: ns}, nil
}

type staticLease struct {
	conn Connections
	ns   Namespace
}

func (l staticLease) Connections() Connections    { return l.conn }
func (l staticLease) Namespace() Namespace        { return l.ns }
func (l staticLease) Close(context.Context) error { return nil }

type Config struct {
	Target          Target
	Fixture         Fetcher
	Run             RunSpec
	ArtifactDir     string
	WorkspacePath   string
	OraclePath      string
	HTTPClient      *http.Client
	SourceCommit    string
	SkipPerformance bool
}

type StageReport struct {
	Name    string         `json:"name"`
	Seconds float64        `json:"seconds"`
	Details map[string]any `json:"details,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type Report struct {
	Run        Namespace        `json:"run"`
	Fixture    ValidatedFixture `json:"fixture"`
	Status     string           `json:"status"`
	BaseStatus string           `json:"base_status,omitempty"`
	Stages     []StageReport    `json:"stages"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Error      string           `json:"error,omitempty"`
}

type Runner struct{ cfg Config }

var databaseRE = regexp.MustCompile(`^loom_acceptance_[a-f0-9]{16}$`)

func New(cfg Config) (*Runner, error) {
	if cfg.Target == nil {
		return nil, errors.New("acceptance target is required")
	}
	if cfg.Fixture.CacheDir == "" {
		return nil, errors.New("fixture cache directory is required")
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return nil, errors.New("workspace path is required")
	}
	if strings.TrimSpace(cfg.ArtifactDir) == "" {
		return nil, errors.New("artifact directory is required")
	}
	if cfg.Run.Project == "" {
		cfg.Run.Project = "NCPI_ACCEPTANCE"
	}
	if cfg.Run.Generation == "" {
		cfg.Run.Generation = "tcga-brca-locked"
	}
	if cfg.Run.RunID == "" {
		cfg.Run.RunID = randomRunID()
	}
	if cfg.Run.ArangoDatabase == "" {
		cfg.Run.ArangoDatabase = databaseName(cfg.Run.RunID)
	}
	if cfg.Run.ClickHouseDatabase == "" {
		cfg.Run.ClickHouseDatabase = databaseName(cfg.Run.RunID)
	}
	if _, err := namespaceFromSpec(cfg.Run); err != nil {
		return nil, err
	}
	if cfg.SourceCommit == "" {
		cfg.SourceCommit = "acceptance"
	}
	return &Runner{cfg: cfg}, nil
}

func randomRunID() RunID {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return RunID("0000000000000000")
	}
	return RunID(hex.EncodeToString(bytes[:]))
}
func databaseName(run RunID) string {
	value := string(run)
	if !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(value) {
		sum := sha256.Sum256([]byte(value))
		value = hex.EncodeToString(sum[:8])
	}
	return "loom_acceptance_" + value
}

func namespaceFromSpec(spec RunSpec) (Namespace, error) {
	if !databaseRE.MatchString(spec.ArangoDatabase) || !databaseRE.MatchString(spec.ClickHouseDatabase) {
		return Namespace{}, fmt.Errorf("run-specific database names must match %s", databaseRE.String())
	}
	if strings.TrimSpace(string(spec.RunID)) == "" || strings.TrimSpace(spec.Project) == "" || strings.TrimSpace(spec.Generation) == "" {
		return Namespace{}, errors.New("run, project, and generation are required")
	}
	return Namespace{Run: string(spec.RunID), Project: spec.Project, Generation: spec.Generation, ArangoDatabase: spec.ArangoDatabase, ClickHouseDatabase: spec.ClickHouseDatabase}, nil
}

func (r *Runner) Run(ctx context.Context) (report Report, runErr error) {
	started := time.Now().UTC()
	report.StartedAt = started
	lease, err := r.cfg.Target.Acquire(ctx, r.cfg.Run)
	if err != nil {
		report.Status = "TARGET_UNAVAILABLE"
		report.Error = err.Error()
		r.writeEvidence(report)
		return report, err
	}
	ns := lease.Namespace()
	report.Run = ns
	defer func() {
		report.FinishedAt = time.Now().UTC()
		if runErr != nil {
			report.Status = "FAILED"
		} else if report.Status == "" {
			report.Status = "PASSED"
		}
		if err := r.writeEvidence(report); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if closeErr := lease.Close(context.WithoutCancel(ctx)); closeErr != nil {
			runErr = errors.Join(runErr, closeErr)
			report.Status = "FAILED"
			report.Error = closeErr.Error()
			_ = r.writeEvidence(report)
		}
	}()
	fixture := r.cfg.Fixture
	validated, err := fixture.Acquire(ctx)
	report.Fixture = validated
	if err != nil {
		report.Status = "FIXTURE_UNAVAILABLE"
		report.Error = err.Error()
		runErr = err
		return report, err
	}
	if err := validateFixtureClosure(validated, fixture.Lock.Counts); err != nil {
		report.Status = "FIXTURE_INVALID"
		report.Error = err.Error()
		runErr = err
		return report, err
	}
	startedStage := time.Now()
	scenario := ScenarioConfig{Connections: lease.Connections(), Namespace: ns, Fixture: validated, WorkspacePath: r.cfg.WorkspacePath, OraclePath: r.cfg.OraclePath, SourceCommit: r.cfg.SourceCommit, ArtifactDir: r.cfg.ArtifactDir, HTTPClient: r.cfg.HTTPClient}
	result, err := RunScenario(ctx, scenario)
	report.Stages = append(report.Stages, result.Stages...)
	report.BaseStatus = result.BaseStatus
	report.Status = result.Status
	report.Stages = append(report.Stages, StageReport{Name: "acceptance", Seconds: time.Since(startedStage).Seconds()})
	if err != nil {
		report.Error = err.Error()
		runErr = err
		return report, err
	}
	return report, nil
}

func (r *Runner) writeEvidence(report Report) error {
	if strings.TrimSpace(r.cfg.ArtifactDir) == "" {
		return nil
	}
	if err := os.MkdirAll(r.cfg.ArtifactDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.cfg.ArtifactDir, ".report.json.tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.cfg.ArtifactDir, "report.json"))
}
