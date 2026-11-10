package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Spy runner + recording infrastructure.
// ---------------------------------------------------------------------------

type spyState struct {
	mu sync.Mutex

	calls           map[string]int
	seenPGOptions   *ingest.PGSourceOptions
	seenPGLoadOpts  *ingest.LoadOptions
	seenArangoOpts  *ingest.LoadOptions
	seenRequireDB   [2]string
	seenCatalogArgs [3]string
	seenVerify      *publication.Ref
	printed         []any
	notes           []string
	closes          int

	pgErr         error
	arangoErr     error
	catalogErr    error
	verifyErr     error
	pgSummary     ingest.LoadSummary
	arangoSummary ingest.LoadSummary
}

func newSpyState() *spyState {
	return &spyState{calls: map[string]int{}}
}

func (s *spyState) record(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[name]++
}

func (s *spyState) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

func (s *spyState) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, c := range s.calls {
		total += c
	}
	return total
}

func (s *spyState) runner() loadRunner {
	return loadRunner{
		loadArango: func(_ context.Context, opts ingest.LoadOptions) (ingest.LoadSummary, error) {
			s.record("loadArango")
			s.mu.Lock()
			s.seenArangoOpts = &opts
			err := s.arangoErr
			summary := s.arangoSummary
			s.mu.Unlock()
			return summary, err
		},
		loadPostgres: func(_ context.Context, opts ingest.LoadOptions, pg ingest.PGSourceOptions) (ingest.LoadSummary, error) {
			s.record("loadPostgres")
			s.mu.Lock()
			s.seenPGLoadOpts = &opts
			s.seenPGOptions = &pg
			err := s.pgErr
			summary := s.pgSummary
			s.mu.Unlock()
			return summary, err
		},
		requireDatabase: func(_ context.Context, url, database string) error {
			s.record("requireDatabase")
			s.mu.Lock()
			s.seenRequireDB = [2]string{url, database}
			s.mu.Unlock()
			return nil
		},
		openCatalogWriters: func(_ context.Context, dsn, schema, application string) (ingest.GenerationCatalogWriters, func() error, error) {
			s.record("openCatalogWriters")
			s.mu.Lock()
			s.seenCatalogArgs = [3]string{dsn, schema, application}
			err := s.catalogErr
			s.mu.Unlock()
			if err != nil {
				return ingest.GenerationCatalogWriters{}, nil, err
			}
			return ingest.GenerationCatalogWriters{
					FieldProfiles: stubFieldProfiles{},
					Inventory:     stubInventory{},
					Relationships: stubRelationships{},
					Contributions: stubContributions{},
				}, func() error {
					s.record("closeCatalogWriters")
					return nil
				}, nil
		},
		verifyStaged: func(_ context.Context, ref publication.Ref, dsn, schema string) error {
			s.record("verifyStaged")
			s.mu.Lock()
			s.seenVerify = &ref
			err := s.verifyErr
			s.mu.Unlock()
			return err
		},
		printSummary: func(value any) error {
			s.record("printSummary")
			s.mu.Lock()
			s.printed = append(s.printed, value)
			s.mu.Unlock()
			return nil
		},
		printNote: func(note string) {
			s.mu.Lock()
			s.notes = append(s.notes, note)
			s.mu.Unlock()
		},
	}
}

// Catalog writer stubs satisfy the four GenerationCatalogWriters interfaces;
// they exist only so the spy can hand back a Complete() bundle without any
// network, never standing in for a commit (no positive claims ride on them).
type stubFieldProfiles struct{}

func (stubFieldProfiles) WriteFieldProfile(context.Context, storepostgres.FieldProfileScope, []catalog.FieldCatalogDocument, int) error {
	return nil
}

type stubInventory struct{}

func (stubInventory) RebuildResourceInventory(context.Context, catalog.ResourceInventoryRebuildOptions) (catalog.ResourceInventoryRebuildSummary, error) {
	return catalog.ResourceInventoryRebuildSummary{}, nil
}

type stubRelationships struct{}

func (stubRelationships) DiscoverRelationshipObservations(context.Context, catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return catalog.RelationshipObservationResult{}, nil
}

type stubContributions struct{}

func (stubContributions) WriteSemanticInventoryContributions(context.Context, []catalog.SemanticInventoryContribution, int) error {
	return nil
}

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	var wg sync.WaitGroup
	var outBuf, errBuf bytes.Buffer
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = outBuf.ReadFrom(outR) }()
	go func() { defer wg.Done(); _, _ = errBuf.ReadFrom(errR) }()
	fn()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	os.Stdout, os.Stderr = origOut, origErr
	return outBuf.String(), errBuf.String()
}

func mustRef(t *testing.T, project, generation string) publication.Ref {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("NewRef(%q,%q): %v", project, generation, err)
	}
	return ref
}

func pgBaseArgs() []string {
	return []string{
		"--backend=postgres",
		"--project=prj-cli",
		"--generation=gen-cli",
		"--pg-dsn=postgresql://cliuser:SENTINELPW999@127.0.0.1:1/clidb?sslmode=disable",
		"--pg-schema=verify_clidisp_s_unit",
	}
}

func arangoBaseArgs() []string {
	return []string{"--backend=arango", "--generation=gen-cli"}
}

// ---------------------------------------------------------------------------
// Stage 1: backend tag first, dialect second.
// ---------------------------------------------------------------------------

func TestInspectLoadGenerationArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantTag  string
		wantSet  bool
		wantHelp bool
	}{
		{"equals form", []string{"--backend=postgres"}, "postgres", true, false},
		{"single dash equals", []string{"-backend=arango"}, "arango", true, false},
		{"space form", []string{"--backend", "postgres"}, "postgres", true, false},
		{"flag after backend", []string{"--project=p", "--backend=postgres"}, "postgres", true, false},
		{"terminator stops scan", []string{"--", "--backend=postgres"}, "", false, false},
		{"missing", []string{"--project=p"}, "", false, false},
		{"help", []string{"-h"}, "", false, true},
		{"help with backend", []string{"--backend=postgres", "--help"}, "postgres", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, set, help := inspectLoadGenerationArgs(tc.args)
			if tag != tc.wantTag || set != tc.wantSet || help != tc.wantHelp {
				t.Fatalf("inspect(%v) = (%q,%v,%v), want (%q,%v,%v)", tc.args, tag, set, help, tc.wantTag, tc.wantSet, tc.wantHelp)
			}
		})
	}
}

func TestNormalizeBackendTag(t *testing.T) {
	if tag, err := normalizeBackendTag(" PostGRES ", true); err != nil || tag != backendPostgres {
		t.Fatalf("postgres normalization = (%q,%v)", tag, err)
	}
	if tag, err := normalizeBackendTag("ARANGO", true); err != nil || tag != backendArango {
		t.Fatalf("arango normalization = (%q,%v)", tag, err)
	}
	if _, err := normalizeBackendTag("", true); !errors.Is(err, ErrBackendRequired) {
		t.Fatalf("empty tag error = %v, want ErrBackendRequired", err)
	}
	if _, err := normalizeBackendTag("postgres", false); !errors.Is(err, ErrBackendRequired) {
		t.Fatalf("unset tag error = %v, want ErrBackendRequired", err)
	}
	_, err := normalizeBackendTag("cockroachdb", true)
	if !errors.Is(err, ErrBackendUnknown) {
		t.Fatalf("unknown tag error = %v, want ErrBackendUnknown", err)
	}
}

// TestBackendTagValidatedBeforeDialect is the alive control for the
// tag-before-dialect ordering law (internal/dataframe/postgres/rows.go:172-178):
// every backend-specific setting is simultaneously invalid, and the error
// must still be the backend sentinel. Reordering the stages flips the error
// identity and fails this test.
func TestBackendTagValidatedBeforeDialect(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	args := []string{
		"--backend=cockroachdb",
		"--pg-schema=",
		"--project=",
		"--generation=gen-x",
	}
	_, _ = captureOutput(t, func() {
		err := dispatchLoadGeneration(context.Background(), args, spy.runner())
		if !errors.Is(err, ErrBackendUnknown) {
			t.Fatalf("error = %v, want ErrBackendUnknown ahead of every dialect guard", err)
		}
		if errors.Is(err, ErrPGSchemaRequired) || errors.Is(err, ErrProjectRequired) || errors.Is(err, ErrPGDSNRequired) {
			t.Fatalf("dialect guard fired before the tag check: %v", err)
		}
	})
	if spy.totalCalls() != 0 {
		t.Fatalf("misrouted backend touched %d seams, want 0", spy.totalCalls())
	}

	// Alive control: the same invocation with a valid tag reaches a dialect
	// guard instead, proving the ordering witness is not vacuous.
	_, _ = captureOutput(t, func() {
		control := append([]string{}, args...)
		control[0] = "--backend=postgres"
		err := dispatchLoadGeneration(context.Background(), control, spy.runner())
		if !errors.Is(err, ErrProjectRequired) {
			t.Fatalf("control error = %v, want ErrProjectRequired", err)
		}
	})
}

func TestBackendRequiredRefusesEntireInvocation(t *testing.T) {
	t.Setenv(pgDSNEnv, "postgresql://env-user/envpass@127.0.0.1:1/envdb")
	spy := newSpyState()
	// Even a fully-populated environment DSN cannot substitute for the tag.
	_, _ = captureOutput(t, func() {
		err := dispatchLoadGeneration(context.Background(),
			[]string{"--project=prj-a", "--generation=gen-a", "--pg-schema=s"}, spy.runner())
		if !errors.Is(err, ErrBackendRequired) {
			t.Fatalf("error = %v, want ErrBackendRequired", err)
		}
	})
	if spy.totalCalls() != 0 {
		t.Fatalf("backend-required refusal touched %d seams, want 0", spy.totalCalls())
	}
}

func TestHelpWithoutBackendPrintsCombinedUsage(t *testing.T) {
	spy := newSpyState()
	_, stderr := captureOutput(t, func() {
		if err := dispatchLoadGeneration(context.Background(), []string{"-h"}, spy.runner()); err != nil {
			t.Fatalf("-h without a tag: %v", err)
		}
	})
	for _, want := range []string{"--backend arango", "--backend postgres", "STAGED"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("combined usage must mention %q, got %q", want, stderr)
		}
	}
}

// ---------------------------------------------------------------------------
// Dispatch exclusivity: the PG invocation uses only the PG loader.
// ---------------------------------------------------------------------------

func TestDispatchPostgresUsesOnlyPGLoader(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	injected := errors.New("injected postgres loader failure")
	spy.pgErr = injected

	_, stderr := captureOutput(t, func() {
		err := dispatchLoadGeneration(context.Background(), pgBaseArgs(), spy.runner())
		if !errors.Is(err, injected) {
			t.Fatalf("error = %v, want the injected PG error preserved by identity", err)
		}
	})
	if spy.count("loadPostgres") != 1 {
		t.Fatalf("loadPostgres calls = %d, want 1", spy.count("loadPostgres"))
	}
	// The injected PG error proves Arango is never opened: not the Arango
	// loader, not the Arango database-exists gate, nothing.
	if spy.count("loadArango") != 0 || spy.count("requireDatabase") != 0 {
		t.Fatalf("PG invocation touched Arango seams: %v", spy.calls)
	}
	if spy.count("openCatalogWriters") != 1 {
		t.Fatalf("catalog wiring calls = %d, want 1", spy.count("openCatalogWriters"))
	}
	if spy.count("closeCatalogWriters") != 1 {
		t.Fatalf("catalog pool must be closed after the failure")
	}
	if spy.count("verifyStaged") != 0 || spy.count("printSummary") != 0 {
		t.Fatalf("failed load must not verify or print success")
	}
	if strings.Contains(stderr, "SENTINELPW999") {
		t.Fatalf("stderr leaked the DSN password: %q", stderr)
	}
	if spy.seenPGOptions == nil || spy.seenPGOptions.DSN == "" || !spy.seenPGOptions.Catalog.Complete() {
		t.Fatalf("loader received incomplete PGSourceOptions: %+v", spy.seenPGOptions)
	}
}

func TestDispatchArangoUsesExistingImplementation(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	ref := mustRef(t, "ARANGODB_PROTO", "gen-cli")
	spy.arangoSummary = ingest.LoadSummary{Files: 1, VerticesInserted: 7, Dataset: &ref}

	_, _ = captureOutput(t, func() {
		if err := dispatchLoadGeneration(context.Background(), arangoBaseArgs(), spy.runner()); err != nil {
			t.Fatalf("arango dispatch: %v", err)
		}
	})
	if spy.count("loadArango") != 1 || spy.count("requireDatabase") != 1 {
		t.Fatalf("arango route must use the existing implementation path: %v", spy.calls)
	}
	if spy.count("loadPostgres") != 0 || spy.count("openCatalogWriters") != 0 || spy.count("verifyStaged") != 0 {
		t.Fatalf("arango invocation touched PG seams: %v", spy.calls)
	}
	if spy.seenRequireDB != [2]string{defaultURL, defaultDatabase} {
		t.Fatalf("requireDatabase gate args = %v, want existing CLI defaults", spy.seenRequireDB)
	}
	if spy.seenArangoOpts == nil || spy.seenArangoOpts.Dataset == nil {
		t.Fatalf("arango options lost the dataset reference")
	}
	if spy.seenArangoOpts.WriteAPI != "import" || spy.seenArangoOpts.BatchSize != 5000 || spy.seenArangoOpts.WorkerCount != 2 {
		t.Fatalf("arango defaults drifted from the existing contract: %+v", spy.seenArangoOpts)
	}
	if spy.count("printSummary") != 1 {
		t.Fatalf("success must print the summary once, got %v", spy.calls)
	}
}

func TestDispatchPostgresSuccessVerifiesStagedAndAnnouncesIt(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	ref := mustRef(t, "prj-cli", "gen-cli")
	spy.pgSummary = ingest.LoadSummary{Files: 2, VerticesInserted: 5, EdgesInserted: 1, Dataset: &ref}

	_, _ = captureOutput(t, func() {
		if err := dispatchLoadGeneration(context.Background(), pgBaseArgs(), spy.runner()); err != nil {
			t.Fatalf("postgres dispatch: %v", err)
		}
	})
	if spy.count("verifyStaged") != 1 {
		t.Fatalf("STAGED verification must run once: %v", spy.calls)
	}
	if spy.seenVerify == nil || *spy.seenVerify != ref {
		t.Fatalf("verification read back the wrong manifest: %v", spy.seenVerify)
	}
	if spy.count("printSummary") != 1 {
		t.Fatalf("summary must print once")
	}
	var announced bool
	for _, note := range spy.notes {
		if strings.Contains(note, "STAGED") && strings.Contains(note, "not activated") {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("success note must say STAGED and must not imply activation, got %q", spy.notes)
	}
	if spy.seenPGOptions == nil || spy.seenPGOptions.DSN != pgBaseArgs()[3][len("--pg-dsn="):] {
		t.Fatalf("loader DSN not passed through: %+v", spy.seenPGOptions)
	}
}

func TestDispatchPostgresVerifyFailureSuppressesSuccess(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	ref := mustRef(t, "prj-cli", "gen-cli")

	// Alive pair: identical inputs, opposite verification verdict.
	spy := newSpyState()
	spy.pgSummary = ingest.LoadSummary{Dataset: &ref}
	spy.verifyErr = errors.New("manifest is LOADING")
	errFailing := captureError(t, spy)
	if errFailing == nil {
		t.Fatal("a failed STAGED verification must exit nonzero")
	}
	if spy.count("printSummary") != 0 {
		t.Fatal("a generation not verified STAGED must not print a success summary")
	}

	spyOK := newSpyState()
	spyOK.pgSummary = ingest.LoadSummary{Dataset: &ref}
	if errOK := captureError(t, spyOK); errOK != nil {
		t.Fatalf("control: identical run with STAGED verification = %v, want success", errOK)
	}
	if spyOK.count("printSummary") != 1 {
		t.Fatal("control: success path must print the summary")
	}
}

func captureError(t *testing.T, spy *spyState) error {
	t.Helper()
	var result error
	_, _ = captureOutput(t, func() {
		result = dispatchLoadGeneration(context.Background(), pgBaseArgs(), spy.runner())
	})
	return result
}

func TestDispatchPostgresNilManifestGuard(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	spy.pgSummary = ingest.LoadSummary{} // success without a dataset manifest
	err := captureError(t, spy)
	if err == nil || !strings.Contains(err.Error(), "dataset manifest") {
		t.Fatalf("nil-manifest success must be refused, got %v", err)
	}
	if spy.count("printSummary") != 0 || spy.count("verifyStaged") != 0 {
		t.Fatalf("refused success may neither verify nor print: %v", spy.calls)
	}
}

func TestDispatchFailureSurfacesInactiveManifestForInspection(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	ref := mustRef(t, "prj-cli", "gen-cli")

	spy := newSpyState()
	spy.pgErr = errors.New("partial load")
	spy.pgSummary = ingest.LoadSummary{Dataset: &ref, ValidationErrors: 3}
	_ = captureError(t, spy)
	var inspect bool
	for _, note := range spy.notes {
		if strings.Contains(note, "staged-manifest-inspect") && strings.Contains(note, "gen-cli") {
			inspect = true
		}
	}
	if !inspect {
		t.Fatalf("failure with a known manifest must print the inspection note, got %q", spy.notes)
	}

	// Alive control: without a manifest reference the note must NOT appear.
	spy2 := newSpyState()
	spy2.pgErr = errors.New("too early to have a manifest")
	_ = captureError(t, spy2)
	for _, note := range spy2.notes {
		if strings.Contains(note, "staged-manifest-inspect") {
			t.Fatalf("inspection note printed without a manifest reference: %q", note)
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-backend misrouting refusals.
// ---------------------------------------------------------------------------

func TestCrossBackendFlagsAreRefusedWithoutEcho(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	_, _ = captureOutput(t, func() {
		_, err := parsePostgresLoadCommand(append(pgBaseArgs(), "--url=http://arango-host-secret"), flag.ContinueOnError)
		// The flag package normalizes to one dash in "flag provided but not
		// defined: -url"; the setting name must be named, the value never.
		if err == nil || !strings.Contains(err.Error(), "-url") {
			t.Fatalf("postgres route must refuse --url, got %v", err)
		}
		if strings.Contains(err.Error(), "arango-host-secret") {
			t.Fatalf("parse refusal echoed the flag value: %v", err)
		}
	})
	_, _ = captureOutput(t, func() {
		_, err := parseArangoLoadCommand(append(arangoBaseArgs(), "--pg-dsn=pgsecretvalue"), flag.ContinueOnError)
		if err == nil || !strings.Contains(err.Error(), "-pg-dsn") {
			t.Fatalf("arango route must refuse --pg-dsn, got %v", err)
		}
		if strings.Contains(err.Error(), "pgsecretvalue") {
			t.Fatalf("parse refusal echoed the flag value: %v", err)
		}
	})
	// Alive control: the same flags are accepted by their own route.
	_, _ = captureOutput(t, func() {
		if _, err := parseArangoLoadCommand(append(arangoBaseArgs(), "--url=http://arango-host"), flag.ContinueOnError); err != nil {
			t.Fatalf("control: arango must accept --url: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Typed refusals with per-sentinel alive controls.
// ---------------------------------------------------------------------------

func TestTypedRefusalsWithAliveControls(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	cases := []struct {
		name      string
		args      func() []string
		sentinel  error
		envDSN    string
		controlOK func() []string
	}{
		{
			name: "project required",
			args: func() []string {
				return []string{"--backend=postgres", "--generation=gen-a", "--pg-dsn=postgresql://h/db", "--pg-schema=s"}
			},
			sentinel: ErrProjectRequired,
			controlOK: func() []string {
				return pgBaseArgs()
			},
		},
		{
			name: "dsn required",
			args: func() []string {
				return []string{"--backend=postgres", "--project=p", "--generation=gen-a", "--pg-schema=s"}
			},
			sentinel: ErrPGDSNRequired,
			controlOK: func() []string {
				a := []string{"--backend=postgres", "--project=p", "--generation=gen-a", "--pg-schema=s", "--pg-dsn=postgresql://h/db"}
				return a
			},
		},
		{
			name: "dsn conflict",
			args: func() []string {
				return []string{"--backend=postgres", "--project=p", "--generation=gen-a", "--pg-schema=s", "--pg-dsn=postgresql://h/db"}
			},
			sentinel: ErrPGDSNConflict,
			envDSN:   "postgresql://env-host/envdb",
			controlOK: func() []string {
				return []string{"--backend=postgres", "--project=p", "--generation=gen-a", "--pg-schema=s", "--pg-dsn=postgresql://h/db"}
			},
		},
		{
			name: "schema required",
			args: func() []string {
				return []string{"--backend=postgres", "--project=p", "--generation=gen-a", "--pg-dsn=postgresql://h/db"}
			},
			sentinel:  ErrPGSchemaRequired,
			controlOK: func() []string { return pgBaseArgs() },
		},
		{
			name:      "truncate forbidden",
			args:      func() []string { return append(pgBaseArgs(), "--truncate=true") },
			sentinel:  ErrPGTruncateForbidden,
			controlOK: func() []string { return append(pgBaseArgs(), "--truncate=false") },
		},
		{
			name:      "application name bounded",
			args:      func() []string { return append(pgBaseArgs(), "--pg-application-name=bad name;password=x") },
			sentinel:  ErrPGApplicationNameInvalid,
			controlOK: func() []string { return append(pgBaseArgs(), "--pg-application-name=verify_clidisp-ok.1") },
		},
		{
			name:      "max conns negative",
			args:      func() []string { return append(pgBaseArgs(), "--pg-max-conns=-1") },
			sentinel:  ErrPGMaxConnsInvalid,
			controlOK: func() []string { return append(pgBaseArgs(), "--pg-max-conns=4") },
		},
		{
			// Alive control for the int32 conversion wrap: flag.IntVar
			// accepts this on 64-bit platforms, so without the explicit
			// bound it would silently become MaxConns=705032704.
			name:      "max conns int32 overflow",
			args:      func() []string { return append(pgBaseArgs(), "--pg-max-conns=5000000000") },
			sentinel:  ErrPGMaxConnsInvalid,
			controlOK: func() []string { return append(pgBaseArgs(), "--pg-max-conns=2147483647") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := newSpyState()
			if tc.envDSN != "" {
				t.Setenv(pgDSNEnv, tc.envDSN)
			} else {
				t.Setenv(pgDSNEnv, "")
			}
			err := captureErrorArgs(t, spy, tc.args())
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("error = %v, want %v", err, tc.sentinel)
			}
			// No sentinel case may open a backend: zero seam calls.
			if spy.totalCalls() != 0 {
				t.Fatalf("refusal touched %d seams, want 0: %v", spy.totalCalls(), spy.calls)
			}
			// Alive control: the repaired invocation passes the guard. The
			// environment DSN channel is cleared so a conflict-case control
			// tests its own guard, not the conflict already exercised.
			if tc.controlOK != nil {
				t.Setenv(pgDSNEnv, "")
				spyControl := newSpyState()
				controlRef := mustRef(t, "prj-cli", "gen-cli")
				spyControl.pgSummary = ingest.LoadSummary{Dataset: &controlRef}
				if err := captureErrorArgs(t, spyControl, tc.controlOK()); err != nil {
					t.Fatalf("control lost the guard's effect: %v", err)
				}
				if spyControl.count("loadPostgres") != 1 {
					t.Fatalf("control did not reach the loader: %v", spyControl.calls)
				}
			}
		})
	}
}

func captureErrorArgs(t *testing.T, spy *spyState, args []string) error {
	t.Helper()
	var result error
	_, _ = captureOutput(t, func() {
		result = dispatchLoadGeneration(context.Background(), args, spy.runner())
	})
	return result
}

func TestApplicationNamePassesThroughToLoader(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	ref := mustRef(t, "prj-cli", "gen-cli")
	spy.pgSummary = ingest.LoadSummary{Dataset: &ref}
	args := append(pgBaseArgs(), "--pg-application-name=verify_clidisp_app")
	if err := captureErrorArgs(t, spy, args); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if spy.seenPGOptions == nil || spy.seenPGOptions.ApplicationName != "verify_clidisp_app" {
		t.Fatalf("application name not passed to the loader: %+v", spy.seenPGOptions)
	}
	if spy.seenCatalogArgs[2] != "verify_clidisp_app" {
		t.Fatalf("catalog pool did not receive the application name: %v", spy.seenCatalogArgs)
	}
}

// TestApplicationNamePaddedUsesTrimmedEverywhere is the alive control for
// the padded-value review finding: the string that is regex-validated must
// be the exact string that reaches both the loader and the catalog pool DSN
// application_name, never a padded variant.
func TestApplicationNamePaddedUsesTrimmedEverywhere(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	spy := newSpyState()
	ref := mustRef(t, "prj-cli", "gen-cli")
	spy.pgSummary = ingest.LoadSummary{Dataset: &ref}
	args := append(pgBaseArgs(), "--pg-application-name=  verify_clidisp_pad  ")
	if err := captureErrorArgs(t, spy, args); err != nil {
		t.Fatalf("padded application name must validate: %v", err)
	}
	if spy.seenPGOptions == nil || spy.seenPGOptions.ApplicationName != "verify_clidisp_pad" {
		t.Fatalf("loader saw a non-trimmed application name: %+v", spy.seenPGOptions)
	}
	if spy.seenCatalogArgs[2] != "verify_clidisp_pad" {
		t.Fatalf("catalog pool saw a non-trimmed application name: %q", spy.seenCatalogArgs[2])
	}
}

// ---------------------------------------------------------------------------
// Arango route invariants preserved from the existing CLI.
// ---------------------------------------------------------------------------

func TestArangoGenerationInvariantsPreserved(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	_, _ = captureOutput(t, func() {
		if _, err := parseArangoLoadCommand([]string{"--backend=arango", "--project=prj-a"}, flag.ContinueOnError); err == nil ||
			!strings.Contains(err.Error(), "--generation is required for load-generation") {
			t.Fatalf("missing --generation must refuse with the existing message, got %v", err)
		}
		// Same opaque-identifier violation the existing CLI suite uses:
		// leading whitespace (cmd/arango-fhir-proto/main_test.go:128-132).
		if _, err := parseArangoLoadCommand([]string{"--backend=arango", "--project=prj-a", "--generation= generation-a"}, flag.ContinueOnError); err == nil ||
			!strings.Contains(err.Error(), "invalid --generation for load-generation") {
			t.Fatalf("invalid --generation must refuse with the existing message, got %v", err)
		}
		config, err := parseArangoLoadCommand(arangoBaseArgs(), flag.ContinueOnError)
		if err != nil {
			t.Fatalf("baseline parse: %v", err)
		}
		if config.Options.Dataset == nil || config.Options.Dataset.Project != defaultProject {
			t.Fatalf("dataset reference wiring drifted: %+v", config.Options.Dataset)
		}
	})
}

func TestJoinApplicationName(t *testing.T) {
	joined, tagged := joinApplicationName("postgresql://user@host:5432/db?sslmode=disable", "loom-cli")
	if !tagged || !strings.Contains(joined, "application_name=loom-cli") {
		t.Fatalf("URL-form DSN not tagged: %q %v", joined, tagged)
	}
	if _, tagged := joinApplicationName("host=h dbname=d", "loom-cli"); tagged {
		t.Fatal("keyword/value DSN must stay untagged rather than a guessed syntax")
	}
	existing, tagged := joinApplicationName("postgresql://user@host/db?application_name=keepme", "loom-cli")
	if !tagged || !strings.Contains(existing, "application_name=keepme") {
		t.Fatalf("explicit application_name must be preserved: %q", existing)
	}
}

// ---------------------------------------------------------------------------
// Credential redaction (acceptance: no credential value in diagnostics).
// ---------------------------------------------------------------------------

const unitDSNPassword = "SENTINELPW999"
const unitDSNKeywordSecret = "KEYSECRET123"

func TestRedactPGDiagnosticRemovesCredentials(t *testing.T) {
	sentinel := errors.New("store sentinel")

	urlDSN := fmt.Sprintf("postgresql://cliuser:%s@bad host/clidb", unitDSNPassword)
	raw := fmt.Errorf("%w: cannot parse `%s`", sentinel, urlDSN)
	redacted := redactPGDiagnostic(raw, urlDSN)
	if strings.Contains(redacted.Error(), unitDSNPassword) {
		t.Fatalf("URL-form credential survived redaction: %q", redacted.Error())
	}
	if !strings.Contains(raw.Error(), unitDSNPassword) {
		t.Fatal("alive control broken: fixture no longer carries the credential, so the redaction test is vacuous")
	}
	if !errors.Is(redacted, sentinel) {
		t.Fatalf("redaction must preserve errors.Is identity, got %v", redacted)
	}
	underlying, ok := redacted.(*redactedPGError)
	if !ok || underlying.original != raw {
		t.Fatalf("redacted diagnostic must wrap the original, got %T", redacted)
	}

	keywordDSN := fmt.Sprintf("host=db.example user=u password=%s dbname=x", unitDSNKeywordSecret)
	rawKW := fmt.Errorf("%w: failed to connect to `%s`", sentinel, keywordDSN)
	redactedKW := redactPGDiagnostic(rawKW, keywordDSN)
	if strings.Contains(redactedKW.Error(), unitDSNKeywordSecret) {
		t.Fatalf("keyword/value credential survived redaction: %q", redactedKW.Error())
	}
	if !strings.Contains(rawKW.Error(), unitDSNKeywordSecret) {
		t.Fatal("alive control broken: keyword fixture no longer carries the credential")
	}
	if !errors.Is(redactedKW, sentinel) {
		t.Fatal("keyword redaction must preserve classification")
	}

	// Identity passthrough when there is nothing to redact.
	plain := errors.New("nothing secret here")
	if got := redactPGDiagnostic(plain, urlDSN); got != plain {
		t.Fatalf("credential-free error must pass through unchanged, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Subprocess contract: exit codes, differential parity, real-path redaction.
// ---------------------------------------------------------------------------

var (
	buildOnce      sync.Once
	loomDataBin    string
	arangoProtoBin string
	buildErr       error
)

func builtBinaries(t *testing.T) (string, string) {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "loom-data-cli-builds")
		if err != nil {
			buildErr = err
			return
		}
		loomDataBin = filepath.Join(dir, "loom-data")
		arangoProtoBin = filepath.Join(dir, "arango-fhir-proto")
		for _, build := range []struct {
			pkg, out string
		}{
			{"github.com/calypr/loom/cmd/loom-data", loomDataBin},
			{"github.com/calypr/loom/cmd/arango-fhir-proto", arangoProtoBin},
		} {
			cmd := exec.Command("go", "build", "-o", build.out, build.pkg)
			if output, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v: %s", build.pkg, err, output)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatalf("binary build failed: %v", buildErr)
	}
	return loomDataBin, arangoProtoBin
}

type cliResult struct {
	stdout string
	stderr string
	code   int
}

func runCLI(t *testing.T, bin string, env []string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	return cliResult{stdout: out.String(), stderr: errb.String(), code: code}
}

func noDSNEnv() []string { return []string{pgDSNEnv + "=", "LOOM_TEST_POSTGRES_URL="} }

func TestCLIBinaryUsageAndExitCodes(t *testing.T) {
	loomBin, _ := builtBinaries(t)

	if res := runCLI(t, loomBin, noDSNEnv()); res.code != 2 || !strings.Contains(res.stderr, "usage:") {
		t.Fatalf("no args: code %d stderr %q, want usage + exit 2", res.code, res.stderr)
	}
	if res := runCLI(t, loomBin, noDSNEnv(), "not-a-command"); res.code != 2 || !strings.Contains(res.stderr, "usage:") {
		t.Fatalf("unknown command: code %d, want exit 2 with usage", res.code)
	}
	res := runCLI(t, loomBin, noDSNEnv(), "load-generation", "--project=prj-a", "--generation=gen-a")
	if res.code != 1 || !strings.Contains(res.stderr, "backend is required") {
		t.Fatalf("missing backend: code %d stderr %q, want exit 1 naming the refusal", res.code, res.stderr)
	}
	if res.stdout != "" {
		t.Fatalf("refusal must keep stdout machine-clean, got %q", res.stdout)
	}
	if res := runCLI(t, loomBin, noDSNEnv(), "load-generation", "--backend=postgres", "--zz-not-a-flag"); res.code != 2 {
		t.Fatalf("unknown flag must exit 2 via flag.ExitOnError, got %d", res.code)
	}
	// PrintDefaults renders flags with a single dash; assert that spelling.
	if res := runCLI(t, loomBin, noDSNEnv(), "load-generation", "--backend=postgres", "-h"); res.code != 0 ||
		!strings.Contains(res.stderr, "-pg-dsn") || !strings.Contains(res.stderr, "STAGED") || !strings.Contains(res.stderr, "NOT activated") {
		t.Fatalf("route help: code %d stderr %q, want exit 0 naming pg flags and STAGED/NOT activated", res.code, res.stderr)
	}
}

// flagLineRE matches PrintDefaults flag-opening lines.
var flagLineRE = regexp.MustCompile(`^  -([A-Za-z0-9][A-Za-z0-9_-]*)`)

// parseFlagUsage extracts per-flag records from a PrintDefaults block,
// dropping the failf error line the caller must strip beforehand.
func parseFlagUsage(t *testing.T, stderr string) map[string]string {
	t.Helper()
	lines := strings.Split(stderr, "\n")
	if len(lines) == 0 {
		t.Fatal("empty usage stderr")
	}
	// The first line is the flag package's failf error line.
	lines = lines[1:]
	records := map[string]string{}
	current := ""
	var buf []string
	for _, line := range lines {
		if match := flagLineRE.FindStringSubmatch(line); match != nil {
			if current != "" {
				records[current] = strings.Join(buf, "\n")
			}
			current = match[1]
			buf = []string{line}
			continue
		}
		if current != "" {
			buf = append(buf, line)
		}
	}
	if current != "" {
		records[current] = strings.Join(buf, "\n")
	}
	if len(records) == 0 {
		t.Fatalf("no flag records parsed from %q", stderr)
	}
	return records
}

// TestArangoUsageSurfaceIsDifferentialParity builds the untouched
// arango-fhir-proto binary and this one and compares the per-flag
// PrintDefaults records of load-generation byte-for-byte. The only allowed
// difference is loom-data's extra --backend routing flag.
func TestArangoUsageSurfaceIsDifferentialParity(t *testing.T) {
	loomBin, arangoBin := builtBinaries(t)

	oldRes := runCLI(t, arangoBin, nil, "load-generation", "--zz-not-a-flag")
	if oldRes.code != 2 || !strings.Contains(oldRes.stderr, "flag provided but not defined") {
		t.Fatalf("old CLI parse-error probe must exit 2 with the flag error, got %d %q", oldRes.code, oldRes.stderr)
	}
	newRes := runCLI(t, loomBin, noDSNEnv(), "load-generation", "--backend=arango", "--zz-not-a-flag")
	if newRes.code != 2 {
		t.Fatalf("loom-data arango parse-error probe must exit 2, got %d", newRes.code)
	}

	oldRecords := parseFlagUsage(t, oldRes.stderr)
	newRecords := parseFlagUsage(t, newRes.stderr)

	backendRecord, hasBackend := newRecords["backend"]
	if !hasBackend {
		t.Fatal("loom-data load-generation must advertise --backend")
	}
	delete(newRecords, "backend")
	if _, leaked := oldRecords["backend"]; leaked {
		t.Fatal("the untouched Arango CLI must not have gained a --backend flag")
	}

	if len(newRecords) != len(oldRecords) {
		t.Fatalf("flag count drifted: loom-data %d vs arango-fhir-proto %d", len(newRecords), len(oldRecords))
	}
	for name, oldLine := range oldRecords {
		newLine, ok := newRecords[name]
		if !ok {
			t.Fatalf("loom-data lost flag --%s present in arango-fhir-proto", name)
		}
		if newLine != oldLine {
			t.Fatalf("flag --%s surface drifted:\n old: %q\n new: %q", name, oldLine, newLine)
		}
	}
	if !strings.Contains(backendRecord, "arango or postgres") {
		t.Fatalf("--backend usage must name both tags, got %q", backendRecord)
	}
}

// TestPostgresRouteUsageSurface proves the PG route advertises its own
// settings, keeps the shared-knob records byte-identical to the existing
// CLI, and never advertises Arango-only flags.
func TestPostgresRouteUsageSurface(t *testing.T) {
	loomBin, arangoBin := builtBinaries(t)

	oldRecords := parseFlagUsage(t, runCLI(t, arangoBin, nil, "load-generation", "--zz-not-a-flag").stderr)
	pgRes := runCLI(t, loomBin, noDSNEnv(), "load-generation", "--backend=postgres", "--zz-not-a-flag")
	if pgRes.code != 2 {
		t.Fatalf("pg parse-error probe must exit 2, got %d", pgRes.code)
	}
	pgRecords := parseFlagUsage(t, pgRes.stderr)

	for _, forbidden := range []string{"url", "database", "write-api"} {
		if _, present := pgRecords[forbidden]; present {
			t.Fatalf("postgres route advertises Arango-only flag --%s", forbidden)
		}
	}
	for _, required := range []string{"pg-dsn", "pg-schema", "pg-max-conns", "pg-acquire-timeout", "pg-application-name", "semantic-flush-threshold", "truncate", "project", "generation", "backend"} {
		if _, present := pgRecords[required]; !present {
			t.Fatalf("postgres route is missing flag --%s", required)
		}
	}
	// Shared knobs must be byte-identical to the existing CLI records.
	arangoOnly := map[string]bool{"url": true, "database": true, "write-api": true, "project": true}
	sharedChecked := 0
	for name, oldLine := range oldRecords {
		if arangoOnly[name] {
			continue
		}
		pgLine, ok := pgRecords[name]
		if !ok {
			t.Fatalf("postgres route lost shared flag --%s", name)
		}
		if pgLine != oldLine {
			t.Fatalf("shared flag --%s drifted between routes:\n old: %q\n pg:  %q", name, oldLine, pgLine)
		}
		sharedChecked++
	}
	if sharedChecked < 20 {
		t.Fatalf("shared-surface comparison degenerate: only %d shared flags checked", sharedChecked)
	}
}

// TestBadDSNErrsWithoutCredential is the coordinator-mandated live leg of
// the boundary-redaction law: the REAL binary takes the REAL
// storepostgres.Open path (client.go:141-143 pgx parse echo and the
// loopback-connect path) with a sentinel password and must exit nonzero
// with the password absent from every diagnostic.
func TestBadDSNErrsWithoutCredential(t *testing.T) {
	loomBin, _ := builtBinaries(t)

	cases := []struct {
		name string
		dsn  string
	}{
		{"parse echo (invalid URL)", fmt.Sprintf("postgresql://cliuser:%s@bad host/clidb", unitDSNPassword)},
		{"connect refused (loopback)", fmt.Sprintf("postgresql://cliuser:%s@127.0.0.1:1/clidb?sslmode=disable", unitDSNPassword)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runCLI(t, loomBin, noDSNEnv(), "load-generation",
				"--backend=postgres", "--project=prj-cli", "--generation=gen-cli",
				"--pg-schema=verify_clidisp_s_unit", "--pg-dsn="+tc.dsn)
			if res.code == 0 {
				t.Fatal("a bad DSN must exit nonzero")
			}
			if strings.Contains(res.stderr, unitDSNPassword) || strings.Contains(res.stdout, unitDSNPassword) {
				t.Fatalf("credential leaked into diagnostics: stdout=%q stderr=%q", res.stdout, res.stderr)
			}
			if !strings.Contains(res.stderr, "postgres") {
				t.Fatalf("alive control: diagnostics must still say something useful, got %q", res.stderr)
			}
		})
	}

	// Alive control for the env channel: a both-set invocation is refused by
	// the conflict sentinel, proving the environment path is live.
	res := runCLI(t, loomBin, []string{pgDSNEnv + "=postgresql://env-host/envdb"}, "load-generation",
		"--backend=postgres", "--project=prj-cli", "--generation=gen-cli",
		"--pg-schema=verify_clidisp_s_unit", "--pg-dsn=postgresql://flag-host/flagdb")
	if res.code != 1 || !strings.Contains(res.stderr, "cannot both be set") {
		t.Fatalf("DSN conflict path: code %d stderr %q", res.code, res.stderr)
	}
}

// ---------------------------------------------------------------------------
// Live PostgreSQL 18 leg (granted window). Own-prefix fixtures only,
// ownership-scoped with a pre-snapshot, mirroring the pg-ingest-03
// conventions (internal/ingest/generation_load_postgres_test.go:40-45,
// 76-172, 1663-1681). LOOM_TEST_POSTGRES_URL is never printed: every test
// diagnostic passes through the scrub closure first.
// ---------------------------------------------------------------------------

const cliDispPrefix = "verify_clidisp_"

var (
	cliDispDBNamePattern     = regexp.MustCompile(`^verify_clidisp_[0-9]+_[0-9a-f]+$`)
	cliDispSchemaNamePattern = regexp.MustCompile(`^verify_clidisp_s_[0-9]+_[0-9a-f]+$`)
	cliDispAppPattern        = regexp.MustCompile(`^verify-clidisp-[0-9a-f]+$`)
)

var (
	cliDispOwnedMu      sync.Mutex
	cliDispOwned        = map[string]bool{}
	cliDispBaselineOnce sync.Once
	cliDispBaseline     int64
	cliDispBaselineErr  error
	cliDispUsed         bool
)

// cliDispDSNSecrets enumerates every credential view of a test-service URL
// so no diagnostic can echo it (LOOM_TEST_POSTGRES_URL is never printed;
// the pattern follows internal/ingest/generation_load_postgres_test.go:362-378).
func cliDispDSNSecrets(raw string) []string {
	secrets := []string{raw}
	if parsed, err := url.Parse(raw); err == nil && parsed.User != nil {
		if info := parsed.User.String(); info != "" {
			secrets = append(secrets, info)
		}
		if pw, ok := parsed.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	return secrets
}

// cliDispScrubber renders any value with every credential view of the test
// service URL replaced. Every fatal/diagnostic in the live leg goes through
// it: pgxpool.New parse failures echo the full connection string, password
// included.
func cliDispScrubber(raw string) func(any) string {
	secrets := cliDispDSNSecrets(raw)
	return func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
}

func cliDispCaptureBaseline(raw string) {
	cliDispBaselineOnce.Do(func() {
		// Errors are stored already-scrubbed: the pgxpool.New failure text
		// carries the DSN, and the stored error is printed by callers.
		scrub := cliDispScrubber(raw)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			cliDispBaselineErr = fmt.Errorf("connect for baseline: %s", scrub(err))
			return
		}
		defer admin.Close()
		var count int64
		// Static literal LIKE pattern, not built input.
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_clidisp\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			cliDispBaselineErr = fmt.Errorf("count baseline: %s", scrub(err))
			return
		}
		cliDispBaseline = count
	})
}

func cliDispRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

type cliDispService struct {
	t         *testing.T
	ctx       context.Context
	admin     *pgxpool.Pool
	iso       *pgxpool.Pool
	dsn       string
	schema    string
	scrub     func(any) string
	appProbe  string
	witnesses *cliDispWitnessPoller
}

func newCLIDispService(t *testing.T) *cliDispService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the live PostgreSQL 18 CLI leg")
	}
	cliDispUsed = true
	// Pre-snapshot before this process creates anything.
	cliDispCaptureBaseline(raw)
	if cliDispBaselineErr != nil {
		t.Fatalf("baseline: %v", cliDispBaselineErr)
	}

	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	secrets := cliDispDSNSecrets(raw)
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	t.Cleanup(cancel)

	adminDSN := raw
	if q := parsed.Query(); q.Get("application_name") == "" {
		q.Set("application_name", "verify_clidisp_admin_probe")
		parsed.RawQuery = q.Encode()
		adminDSN = parsed.String()
		secrets = append(secrets, adminDSN)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect for admin: %s", scrub(err))
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("%s%d_%s", cliDispPrefix, time.Now().UnixNano(), cliDispRandomHex(t))
	if !cliDispDBNamePattern.MatchString(dbName) {
		t.Fatalf("owned database name %q violates the pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		t.Fatalf("create owned database: %s", scrub(err))
	}
	cliDispOwnedMu.Lock()
	cliDispOwned[dbName] = false
	cliDispOwnedMu.Unlock()
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(context.Background()),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop owned database: %s", scrub(err))
			return
		}
		cliDispOwnedMu.Lock()
		cliDispOwned[dbName] = true
		cliDispOwnedMu.Unlock()
	})

	if q := parsed.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		parsed.RawQuery = q.Encode()
	}
	parsed.Path = "/" + dbName
	dsn := parsed.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to owned database: %s", scrub(err))
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("%ss_%d_%s", cliDispPrefix, time.Now().UnixNano(), cliDispRandomHex(t))
	if !cliDispSchemaNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q violates the pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create owned schema: %s", scrub(err))
	}
	return &cliDispService{t: t, ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, appProbe: "verify_clidisp_admin_probe"}
}

// cliDispWitnessPoller observes pg_stat_activity for a bound application_name
// within this service's own database, so positive attribution cannot be
// confused with sibling churn.
type cliDispWitnessPoller struct {
	t      *testing.T
	svc    *cliDispService
	stop   chan struct{}
	done   chan struct{}
	stopCh sync.Once
	mu     sync.Mutex
	seen   map[string]int
}

func startCLIDispWitness(t *testing.T, svc *cliDispService, apps ...string) *cliDispWitnessPoller {
	t.Helper()
	w := &cliDispWitnessPoller{t: t, svc: svc, stop: make(chan struct{}), done: make(chan struct{}), seen: map[string]int{}}
	for _, app := range apps {
		w.seen[app] = 0
	}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				for app := range w.seen {
					var count int
					if err := svc.admin.QueryRow(svc.ctx,
						"SELECT count(*) FROM pg_stat_activity WHERE application_name = $1 AND datname = $2",
						app, svc.dbNameForWitness()).Scan(&count); err != nil {
						continue
					}
					if count > 0 {
						w.mu.Lock()
						w.seen[app] = count
						w.mu.Unlock()
					}
				}
			}
		}
	}()
	return w
}

func (s *cliDispService) dbNameForWitness() string {
	parsed, err := url.Parse(s.dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

func (w *cliDispWitnessPoller) stopPolling() {
	w.stopCh.Do(func() { close(w.stop) })
	<-w.done
}

func (w *cliDispWitnessPoller) observed(app string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen[app]
}

// cliDispWriteFixtures writes the minimal two-resource staging directory in
// the canonical shape the pg-ingest-03 suite uses (Patient + Observation
// with a resolving subject reference and one LOINC-coded observation).
func cliDispWriteFixtures(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	line := func(doc map[string]any) string {
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal fixture line: %v", err)
		}
		return string(encoded)
	}
	patients := make([]string, 0, 3)
	for _, id := range []string{"pat-0000", "pat-0001", "pat-0002"} {
		patients = append(patients, line(map[string]any{"resourceType": "Patient", "id": id, "gender": "female"}))
	}
	if err := os.WriteFile(filepath.Join(dir, "Patient.ndjson"), []byte(strings.Join(patients, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write Patient fixture: %v", err)
	}
	observations := []string{
		line(map[string]any{
			"resourceType": "Observation", "id": "obs-a", "status": "final",
			"subject": map[string]any{"reference": "Patient/pat-0000"},
			"code":    map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": "8867-4", "display": "Heart rate"}}},
		}),
		line(map[string]any{
			"resourceType": "Observation", "id": "obs-b", "status": "final",
			"subject": map[string]any{"reference": "Patient/pat-0001"},
			"code":    map[string]any{"text": "plain-text code"},
		}),
	}
	if err := os.WriteFile(filepath.Join(dir, "Observation.ndjson"), []byte(strings.Join(observations, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write Observation fixture: %v", err)
	}
}

func TestCLIPostgresLoadGenerationLiveStagedLoad(t *testing.T) {
	svc := newCLIDispService(t)
	staging := t.TempDir()
	cliDispWriteFixtures(t, staging)

	project := "prj-cli-disp"
	generation := "gen-cli-live"
	app := "verify-clidisp-" + cliDispRandomHex(t)
	if !cliDispAppPattern.MatchString(app) {
		t.Fatalf("witness application name %q violates the pattern", app)
	}

	// Witness: our load's application_name, the always-present admin probe
	// (alive control for the poll itself), and a nonexistent name (selectivity
	// control).
	witness := startCLIDispWitness(t, svc, app, svc.appProbe, "verify-clidisp-no-such-app-xyz")
	defer witness.stopPolling()

	args := []string{
		"--backend=postgres",
		"--project=" + project,
		"--generation=" + generation,
		"--pg-dsn=" + svc.dsn,
		"--pg-schema=" + svc.schema,
		"--meta-dir=" + staging,
		"--schema=" + filepath.Join("..", "..", "schemas", "graph-fhir.json"),
		"--pg-application-name=" + app,
		"--use-generic=false",
	}
	var stdout, stderr string
	var dispatchErr error
	stdout, stderr = captureOutput(t, func() {
		dispatchErr = dispatchLoadGeneration(svc.ctx, args, defaultRunner())
	})
	witness.stopPolling()

	if dispatchErr != nil {
		t.Fatalf("live load: %s", svc.scrub(dispatchErr))
	}
	if !strings.Contains(stderr, "STAGED") || !strings.Contains(stderr, "not activated") {
		t.Fatalf("success must announce STAGED-and-not-activated, got %q", stderr)
	}

	// The last stdout line is the machine-readable summary (progress events
	// precede it).
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var summary ingest.LoadSummary
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatalf("last stdout line is not the summary JSON: %v\n%q", err, lines[len(lines)-1])
	}
	if summary.Files != 2 || summary.VerticesInserted != 5 || summary.EdgesInserted != 2 {
		t.Fatalf("summary counts = files %d vertices %d edges %d, want literal 2/5/2",
			summary.Files, summary.VerticesInserted, summary.EdgesInserted)
	}
	if summary.Dataset == nil || summary.Dataset.Project != project || summary.Dataset.Generation != generation {
		t.Fatalf("summary dataset = %+v, want %s/%s", summary.Dataset, project, generation)
	}
	// Credential hygiene: the service DSN must never appear in CLI output.
	if strings.Contains(stdout, svc.dsn) || strings.Contains(stderr, svc.dsn) {
		t.Fatal("raw DSN appeared in CLI output")
	}

	// Literal-row witness through direct SQL: STAGED manifest and an
	// untouched active-generation pointer (the STAGED-not-activated boundary).
	manifestTable := pgx.Identifier{svc.schema, "dataset_generation_manifest"}.Sanitize()
	activeTable := pgx.Identifier{svc.schema, "dataset_active_generation"}.Sanitize()
	var state string
	if err := svc.iso.QueryRow(svc.ctx,
		"SELECT state FROM "+manifestTable+" WHERE project = $1 AND generation = $2", project, generation).Scan(&state); err != nil {
		t.Fatalf("read manifest row: %s", svc.scrub(err))
	}
	if state != "STAGED" {
		t.Fatalf("manifest state = %q, want literal STAGED", state)
	}
	var activeCount int
	if err := svc.iso.QueryRow(svc.ctx,
		"SELECT count(*) FROM "+activeTable+" WHERE project = $1", project).Scan(&activeCount); err != nil {
		t.Fatalf("count active pointers: %s", svc.scrub(err))
	}
	if activeCount != 0 {
		t.Fatalf("active-generation pointer count = %d, want 0: the CLI must never activate", activeCount)
	}

	// Attribution: the load's own application_name was witnessed on this
	// database (D5), with the admin probe proving the poll can see anything
	// and the nonexistent name proving the predicate is selective.
	if witness.observed(app) == 0 {
		t.Fatalf("load backends were never witnessed with application_name %q", app)
	}
	if witness.observed(svc.appProbe) == 0 {
		t.Fatal("witness poller saw even the admin probe: poller is broken (alive control)")
	}
	if witness.observed("verify-clidisp-no-such-app-xyz") != 0 {
		t.Fatal("witness predicate is not selective")
	}

	// Negative leg: an empty staging directory is refused by the loader's
	// zero-files guard, and a virgin schema is left untouched (never
	// migrated) while the CLI connected only over PG.
	virginSchema := fmt.Sprintf("%ss_%d_%s", cliDispPrefix, time.Now().UnixNano(), cliDispRandomHex(t))
	if _, err := svc.iso.Exec(svc.ctx, `CREATE SCHEMA "`+virginSchema+`"`); err != nil {
		t.Fatalf("create virgin schema: %s", svc.scrub(err))
	}
	emptyDir := t.TempDir()
	_, _ = captureOutput(t, func() {
		err := dispatchLoadGeneration(svc.ctx, []string{
			"--backend=postgres",
			"--project=" + project,
			"--generation=gen-empty",
			"--pg-dsn=" + svc.dsn,
			"--pg-schema=" + virginSchema,
			"--meta-dir=" + emptyDir,
			"--schema=" + filepath.Join("..", "..", "schemas", "graph-fhir.json"),
		}, defaultRunner())
		if !errors.Is(err, ingest.ErrGenerationLoadRequiresFiles) {
			t.Fatalf("empty staging dir error = %s, want ErrGenerationLoadRequiresFiles", svc.scrub(err))
		}
	})
	var missing bool
	if err := svc.iso.QueryRow(svc.ctx,
		"SELECT to_regclass($1) IS NULL", virginSchema+".dataset_generation_manifest").Scan(&missing); err != nil {
		t.Fatalf("probe virgin schema: %s", svc.scrub(err))
	}
	if !missing {
		t.Fatal("the zero-files guard still migrated the virgin schema")
	}
}

// TestZZCLIDispLiveOwnershipAudit runs last in the package and is
// ownership-scoped: it counts only this process's own verify_clidisp_*
// prefix against the pre-snapshot baseline, so concurrent sibling workers
// neither race nor trip it.
func TestZZCLIDispLiveOwnershipAudit(t *testing.T) {
	if !cliDispUsed {
		t.Skip("live leg did not run in this process")
	}
	if cliDispBaselineErr != nil {
		// Stored already-scrubbed by cliDispCaptureBaseline.
		t.Fatalf("baseline was never captured: %v", cliDispBaselineErr)
	}
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	// pgxpool.New echoes the full DSN (password included) on parse failure:
	// every audit diagnostic goes through the scrubber.
	scrub := cliDispScrubber(raw)
	admin, err := pgxpool.New(context.Background(), raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	var count int64
	if err := admin.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_clidisp\\_%' ESCAPE '\\'").Scan(&count); err != nil {
		t.Fatalf("audit count: %s", scrub(err))
	}
	if count != cliDispBaseline {
		t.Errorf("verify_clidisp_* database count = %d, want baseline %d (zero own-prefix survivors)", count, cliDispBaseline)
	}
	cliDispOwnedMu.Lock()
	defer cliDispOwnedMu.Unlock()
	for name, dropped := range cliDispOwned {
		if !dropped {
			t.Errorf("registered fixture %s was never dropped by its owner", name)
		}
	}
}
