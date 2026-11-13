package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgpubrel01ResourceNamePattern = regexp.MustCompile(`^pgpubrel01_it_[0-9]+_[0-9a-f]+$`)

type releaseService struct {
	ctx     context.Context
	admin   *pgxpool.Pool
	witness *pgxpool.Pool
	poolA   *pgxpool.Pool
	poolB   *pgxpool.Pool
	pidA    int32
	pidB    int32
	pidW    int32
	schema  string
	scrub   func(any) string
	fail    func(step string, err error)

	releases  *ReleaseStore
	releasesB *ReleaseStore
	gen       *Store
}

func newReleaseService(t *testing.T) *releaseService {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgpubrel01_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgpubrel01ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := admin.Exec(cleanupCtx,
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	newPool := func(label string) *pgxpool.Pool {
		config, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			fail("parse pool config "+label, err)
		}
		config.MaxConns = 1
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			fail("connect pool "+label, err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	witness := newPool("witness")
	poolA := newPool("writer-a")
	poolB := newPool("writer-b")

	schema := fmt.Sprintf("pgpubrel01_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgpubrel01ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := witness.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := witness.Exec(cleanupCtx,
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &releaseService{
		ctx: ctx, admin: admin, witness: witness, poolA: poolA, poolB: poolB,
		schema: schema, scrub: scrub, fail: fail,
	}
	s.migrate(t)
	s.pidA = s.backendPID(t, poolA, "writer A")
	s.pidB = s.backendPID(t, poolB, "writer B")
	s.pidW = s.backendPID(t, witness, "witness")
	if s.pidA == s.pidB || s.pidA == s.pidW || s.pidB == s.pidW {
		t.Fatalf("writer and witness backends are not distinct physical connections: A=%d B=%d W=%d", s.pidA, s.pidB, s.pidW)
	}

	releases, err := NewReleaseStore(poolA, schema)
	if err != nil {
		fail("build release store A", err)
	}
	s.releases = releases
	releasesB, err := NewReleaseStore(poolB, schema)
	if err != nil {
		fail("build release store B", err)
	}
	s.releasesB = releasesB
	gen, err := New(witness, schema)
	if err != nil {
		fail("build generation store", err)
	}
	s.gen = gen
	return s
}

func (s *releaseService) backendPID(t *testing.T, pool *pgxpool.Pool, label string) int32 {
	t.Helper()
	var pid int32
	if err := pool.QueryRow(s.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		s.fail("backend pid "+label, err)
	}
	if pid <= 0 {
		t.Fatalf("%s backend pid = %d, want a positive server pid", label, pid)
	}
	return pid
}

func (s *releaseService) migrate(t *testing.T) {
	t.Helper()
	history, err := OwnerMigrations()
	if err != nil {
		s.fail("owner migrations", err)
	}
	if len(history) != 3 ||
		history[0].Version != 1 || history[0].Name != "ledger" ||
		history[1].Version != 3 || history[1].Name != "dataset_generation_manifest" ||
		history[2].Version != 8 || history[2].Name != "project_release" {
		t.Fatalf("owner history = %+v, want exactly {1 ledger, 3 dataset_generation_manifest, 8 project_release}", history)
	}
	runner, err := storepostgres.NewMigrationRunner(s.witness, s.schema, history)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate release history", err)
	}
	if result.LatestVersion != 8 {
		t.Fatalf("latest migration version = %d, want 8", result.LatestVersion)
	}
	wantLedger := []storepostgres.LedgerRecord{
		{Version: 1, Name: "ledger", Checksum: "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
		{Version: 3, Name: "dataset_generation_manifest", Checksum: "06c614680a9c1cd4ba2ef0ac0471053bcb3d8e471c36d3852e70682887a72f5d"},
		{Version: 8, Name: "project_release", Checksum: "1d581ec39731958dd0f79cdab18e92861a380007d3e85d6de4940800378480b1"},
	}
	if len(result.Applied) != len(wantLedger) {
		t.Fatalf("applied history = %+v, want %+v", result.Applied, wantLedger)
	}
	for i, want := range wantLedger {
		got := result.Applied[i]
		if got.Version != want.Version || got.Name != want.Name || got.Checksum != want.Checksum {
			t.Fatalf("applied[%d] = {Version:%d Name:%q Checksum:%q}, want {Version:%d Name:%q Checksum:%q}",
				i, got.Version, got.Name, got.Checksum, want.Version, want.Name, want.Checksum)
		}
	}
}

func (s *releaseService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *releaseService) mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(s.ctx, sql, args...); err != nil {
		s.fail("exec "+sql, err)
	}
}

func (s *releaseService) execCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.witness.QueryRow(s.ctx, sql, args...).Scan(&n); err != nil {
		s.fail("count "+sql, err)
	}
	return n
}

func (s *releaseService) mustWitnessBool(t *testing.T, sql string, args ...any) bool {
	t.Helper()
	var value bool
	if err := s.witness.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *releaseService) mustWitnessText(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var value string
	if err := s.witness.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

type pointerRow struct {
	project   string
	releaseID string
	revision  int64
	createdAt string
	updatedAt string
	ctid      string
	exists    bool
}

func (s *releaseService) witnessPointer(project string) pointerRow {
	var w pointerRow
	err := s.witness.QueryRow(s.ctx,
		`SELECT project, release_id, revision, created_at::text, updated_at::text, ctid::text FROM `+s.qualified(ActiveProjectReleasesTable)+`
WHERE project = $1`, project).
		Scan(&w.project, &w.releaseID, &w.revision, &w.createdAt, &w.updatedAt, &w.ctid)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness pointer "+project, err)
	}
	w.exists = true
	return w
}

func (s *releaseService) assertPointerAbsent(t *testing.T, project string) {
	t.Helper()
	if w := s.witnessPointer(project); w.exists {
		t.Fatalf("expected no active release pointer for %s, found %+v", project, w)
	}
}

func (s *releaseService) assertPointerLiteral(t *testing.T, project, wantReleaseID string, wantRevision int64) {
	t.Helper()
	w := s.witnessPointer(project)
	if !w.exists || w.project != project || w.releaseID != wantReleaseID || w.revision != wantRevision {
		t.Fatalf("active release pointer for %s = %+v, want {project:%s release_id:%s revision:%d}",
			project, w, project, wantReleaseID, wantRevision)
	}
}

type candidateRow struct {
	project, id, generation, gitCommit, payloadText, ctid string
	exists                                                bool
}

func (s *releaseService) witnessCandidate(project, id string) candidateRow {
	var w candidateRow
	err := s.witness.QueryRow(s.ctx,
		`SELECT project, id, generation, git_commit, payload::text, ctid::text FROM `+s.qualified(ProjectReleasesTable)+`
WHERE project = $1 AND id = $2`, project, id).
		Scan(&w.project, &w.id, &w.generation, &w.gitCommit, &w.payloadText, &w.ctid)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness candidate "+project+"/"+id, err)
	}
	w.exists = true
	return w
}

func (s *releaseService) assertCandidateLiteral(t *testing.T, want publication.ProjectRelease) {
	t.Helper()
	payload, err := marshalReleasePayload(want)
	if err != nil {
		t.Fatal(err)
	}
	w := s.witnessCandidate(want.Project, want.ID)
	if !w.exists {
		t.Fatalf("candidate %s/%s does not exist", want.Project, want.ID)
	}
	if w.project != want.Project || w.id != want.ID || w.generation != want.Generation || w.gitCommit != want.GitCommit {
		t.Fatalf("candidate scan columns = %+v, want {project:%s id:%s generation:%s git_commit:%s}",
			w, want.Project, want.ID, want.Generation, want.GitCommit)
	}
	matches := s.mustWitnessBool(t,
		`SELECT payload = $1::jsonb FROM `+s.qualified(ProjectReleasesTable)+` WHERE project = $2 AND id = $3`,
		payload, want.Project, want.ID)
	if !matches {
		t.Fatalf("candidate %s/%s payload does not equal the literal expected document\nwant: %s\ngot:  %s",
			want.Project, want.ID, payload, w.payloadText)
	}
}

func releaseSelector(recipe, version, output string) publication.DataframeSelector {
	return publication.DataframeSelector{Recipe: recipe, TranslationVersion: version, Output: output}
}

func candidateFixture(project, generation, id string) publication.ProjectRelease {
	verified := time.Date(2026, 8, 6, 12, 0, 0, 123456789, time.UTC)
	created := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	return publication.ProjectRelease{
		ID:         id,
		Project:    project,
		GitCommit:  generation,
		Generation: generation,
		Publications: []publication.ReleasePublication{
			{Selector: releaseSelector("recipe-main", "v2", "patients"), ExecutionID: "exec-1", Generation: generation, Required: true, VerifiedAt: verified},
			{Selector: releaseSelector("recipe-aux", "v1", "encounters"), ExecutionID: "exec-2", Generation: generation, Required: false, Stale: true, VerifiedAt: verified},
		},
		RequiredVerifications: []publication.ContractVerification{
			{Selector: releaseSelector("recipe-main", "v2", "patients"), ExecutionID: "exec-1", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: verified},
			{Selector: releaseSelector("recipe-optional", "v1", "bundle"), Generation: generation, Queryable: false, ErrorCode: "RELEASE_REQUIREMENTS_UNMET"},
		},
		CreatedAt: created,
	}
}

func (s *releaseService) stageGeneration(t *testing.T, project, generation, digest string) publication.Manifest {
	t.Helper()
	ref := mustRef(t, project, generation)
	schema := mustSnapshot(t, generation, digest)
	loading, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("NewManifest(%s/%s): %v", project, generation, err)
	}
	if _, err := s.gen.CreateManifest(s.ctx, loading); err != nil {
		s.fail("CreateManifest "+project+"/"+generation, err)
	}
	staged, err := s.gen.TransitionManifest(s.ctx, loading, publication.StateStaged)
	if err != nil {
		s.fail("TransitionManifest STAGED "+project+"/"+generation, err)
	}
	return staged
}

func (s *releaseService) activateGeneration(t *testing.T, staged publication.Manifest) {
	t.Helper()
	if err := s.gen.ActivateGeneration(s.ctx, staged, nil); err != nil {
		s.fail("ActivateGeneration "+staged.Dataset.Project+"/"+staged.Dataset.Generation, err)
	}
}

func (s *releaseService) mustSave(t *testing.T, store *ReleaseStore, release publication.ProjectRelease) publication.ProjectRelease {
	t.Helper()
	saved, err := store.SaveRelease(s.ctx, release)
	if err != nil {
		s.fail(fmt.Sprintf("SaveRelease %s/%s", release.Project, release.ID), err)
	}
	return saved
}

func TestNewReleaseStoreGuardsNilSourceAndUnsafeSchema(t *testing.T) {
	if _, err := NewReleaseStore(nil, "ok_schema"); !errors.Is(err, ErrNilReleaseConnSource) {
		t.Fatalf("NewReleaseStore(nil) = %v, want ErrNilReleaseConnSource", err)
	}
	for _, schema := range []string{"", "Public", "a;drop", "0starts_with_digit", "has space", "tab\tname", strings.Repeat("a", 64)} {
		if _, err := NewReleaseStore((*pgxpool.Pool)(nil), schema); !errors.Is(err, ErrInvalidSchemaName) {
			t.Fatalf("NewReleaseStore(schema %q) = %v, want ErrInvalidSchemaName", schema, err)
		}
	}
	if _, err := NewReleaseStore((*pgxpool.Pool)(nil), "ok_schema"); err != nil {
		t.Fatalf("NewReleaseStore(valid schema) = %v, want success", err)
	}
}

func TestReleaseSchemaCompositionAndColumnContract(t *testing.T) {
	s := newReleaseService(t)

	rows, err := s.witness.Query(s.ctx,
		"SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = $1 ORDER BY tablename", s.schema)
	if err != nil {
		s.fail("list tables", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			s.fail("scan table", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		s.fail("list tables", err)
	}
	rows.Close()
	want := []string{
		"active_project_release",
		"dataset_active_generation",
		"dataset_generation_manifest",
		"project_release",
		"schema_migrations",
	}
	if strings.Join(tables, ",") != strings.Join(want, ",") {
		t.Fatalf("schema tables = %#v, want exactly %#v (no duplicated generation schema, no data-plane tables)", tables, want)
	}

	type column struct {
		name, typ string
		nullable  bool
	}
	for _, wantTable := range []struct {
		table   string
		columns []column
	}{
		{ProjectReleasesTable, []column{
			{"project", "text", false},
			{"id", "text", false},
			{"generation", "text", false},
			{"git_commit", "text", false},
			{"payload", "jsonb", false},
		}},
		{ActiveProjectReleasesTable, []column{
			{"project", "text", false},
			{"release_id", "text", false},
			{"revision", "bigint", false},
			{"created_at", "timestamp with time zone", false},
			{"updated_at", "timestamp with time zone", false},
		}},
	} {
		rows, err := s.witness.Query(s.ctx,
			"SELECT column_name, data_type, is_nullable = 'YES' FROM information_schema.columns"+
				" WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position",
			s.schema, wantTable.table)
		if err != nil {
			s.fail("list columns "+wantTable.table, err)
		}
		var columns []column
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.name, &c.typ, &c.nullable); err != nil {
				s.fail("scan column", err)
			}
			columns = append(columns, c)
		}
		if err := rows.Err(); err != nil {
			s.fail("list columns "+wantTable.table, err)
		}
		rows.Close()
		if len(columns) != len(wantTable.columns) {
			t.Fatalf("%s columns = %+v, want %+v", wantTable.table, columns, wantTable.columns)
		}
		for i, wantCol := range wantTable.columns {
			if columns[i] != wantCol {
				t.Fatalf("%s column %d = %+v, want %+v", wantTable.table, i, columns[i], wantCol)
			}
		}
	}
}

func TestSaveReleasePersistsImmutableCandidate(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-save"
	cand := candidateFixture(project, "gen-1", "release_"+digestA)

	saved := s.mustSave(t, s.releases, cand)
	if !reflect.DeepEqual(saved, cand) {
		t.Fatalf("SaveRelease = %+v, want %+v", saved, cand)
	}
	s.assertCandidateLiteral(t, cand)

	s.assertPointerAbsent(t, project)
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(ActiveProjectReleasesTable)); n != 0 {
		t.Fatalf("active_project_release rows after save = %d, want 0", n)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified("dataset_active_generation")); n != 0 {
		t.Fatalf("active generation rows after save = %d, want 0", n)
	}

	for _, absentKey := range []string{"executionId", "state"} {
		if present := s.mustWitnessBool(t,
			`SELECT (payload -> 'requiredVerifications' -> 1) ? $3 FROM `+s.qualified(ProjectReleasesTable)+` WHERE project = $1 AND id = $2`,
			project, cand.ID, absentKey); present {
			t.Fatalf("stored payload fabricated a %s key for the zero-value verification field", absentKey)
		}
	}
	if got := s.mustWitnessText(t,
		`SELECT (payload -> 'requiredVerifications' -> 1) ->> 'verifiedAt' FROM `+s.qualified(ProjectReleasesTable)+` WHERE project = $1 AND id = $2`,
		project, cand.ID); got != "0001-01-01T00:00:00Z" {
		t.Fatalf("zero-time verification stored as %q, want the canonical zero instant 0001-01-01T00:00:00Z", got)
	}

	retry := cand
	retry.CreatedAt = time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	retry.Publications = append([]publication.ReleasePublication(nil), cand.Publications...)
	retry.RequiredVerifications = append([]publication.ContractVerification(nil), cand.RequiredVerifications...)
	before := s.witnessCandidate(project, cand.ID)
	savedRetry := s.mustSave(t, s.releases, retry)
	if !savedRetry.CreatedAt.Equal(cand.CreatedAt) {
		t.Fatalf("same-content retry returned createdAt %v, want the stored original %v", savedRetry.CreatedAt, cand.CreatedAt)
	}
	after := s.witnessCandidate(project, cand.ID)
	if before.payloadText != after.payloadText || before.ctid != after.ctid {
		t.Fatalf("same-content retry rewrote the candidate: before=%+v after=%+v", before, after)
	}

	changed := candidateFixture(project, "gen-1", "release_"+digestA)
	changed.Publications = append([]publication.ReleasePublication(nil), cand.Publications...)
	changed.Publications[0].Required = false
	if _, err := s.releases.SaveRelease(s.ctx, changed); !errors.Is(err, ErrReleaseContentConflict) {
		t.Fatalf("changed-content save = %v, want ErrReleaseContentConflict", err)
	}
	afterChange := s.witnessCandidate(project, cand.ID)
	if afterChange.payloadText != before.payloadText || afterChange.ctid != before.ctid {
		t.Fatalf("refused changed-content save mutated the candidate: before=%+v after=%+v", before, afterChange)
	}

	other := candidateFixture(project, "gen-1", "release_"+digestB)
	other.Publications = append([]publication.ReleasePublication(nil), cand.Publications...)
	other.Publications[1].ExecutionID = "exec-9"
	s.mustSave(t, s.releases, other)
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(ProjectReleasesTable)+" WHERE project = $1", project); n != 2 {
		t.Fatalf("candidate rows = %d, want 2 (history retained)", n)
	}
}

func TestReleaseActivationAtomicallyUpdatesReleasePointer(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-activate"
	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)

	candA := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, candA)

	active, err := s.releases.CompareAndSwapActivateRelease(s.ctx, candA, 0)
	if err != nil {
		s.fail("first activation", err)
	}
	if !reflect.DeepEqual(active, publication.ActiveRelease{Release: candA, Revision: 1}) {
		t.Fatalf("first activation = %+v, want {%+v 1}", active, candA)
	}
	s.assertPointerLiteral(t, project, candA.ID, 1)

	candB := candidateFixture(project, "gen-1", "release_"+digestB)
	candB.Publications = append([]publication.ReleasePublication(nil), candA.Publications...)
	candB.Publications[0].ExecutionID = "exec-b"
	s.mustSave(t, s.releasesB, candB)
	stale := s.witnessPointer(project)
	if _, err := s.releasesB.CompareAndSwapActivateRelease(s.ctx, candB, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("stale-expected activation = %v, want ErrReleaseActivationConflict", err)
	}
	fresh := s.witnessPointer(project)
	if fresh != stale {
		t.Fatalf("stale activation changed the pointer: before=%+v after=%+v", stale, fresh)
	}

	active, err = s.releasesB.CompareAndSwapActivateRelease(s.ctx, candB, 1)
	if err != nil {
		s.fail("second activation", err)
	}
	if !reflect.DeepEqual(active, publication.ActiveRelease{Release: candB, Revision: 2}) {
		t.Fatalf("second activation = %+v, want {%+v 2}", active, candB)
	}
	s.assertPointerLiteral(t, project, candB.ID, 2)
	s.assertCandidateLiteral(t, candA)
	s.assertCandidateLiteral(t, candB)

	read, err := s.releases.ReadActiveRelease(s.ctx, project)
	if err != nil {
		s.fail("ReadActiveRelease", err)
	}
	if !reflect.DeepEqual(read, publication.ActiveRelease{Release: candB, Revision: 2}) {
		t.Fatalf("ReadActiveRelease = %+v, want {%+v 2}", read, candB)
	}
	if _, err := s.releases.ReadActiveRelease(s.ctx, "pgpubrel01-never-active"); !errors.Is(err, publication.ErrNoActiveRelease) {
		t.Fatalf("ReadActiveRelease(unprojected) = %v, want ErrNoActiveRelease", err)
	}
}

func TestReleaseActivationEmptyCASResultIsConflict(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-cas-negative"

	ghost := candidateFixture(project, "gen-ghost", "release_"+digestC)
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, ghost, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("unpersisted candidate = %v, want ErrReleaseActivationConflict", err)
	}
	s.assertPointerAbsent(t, project)

	cand := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, cand)
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("no manifest = %v, want ErrReleaseActivationConflict", err)
	}
	s.assertPointerAbsent(t, project)

	ref := mustRef(t, project, "gen-1")
	schema := mustSnapshot(t, "gen-1", digestA)
	loading, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.gen.CreateManifest(s.ctx, loading); err != nil {
		s.fail("CreateManifest", err)
	}
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("LOADING manifest = %v, want ErrReleaseActivationConflict", err)
	}
	s.assertPointerAbsent(t, project)

	staged, err := s.gen.TransitionManifest(s.ctx, loading, publication.StateStaged)
	if err != nil {
		s.fail("TransitionManifest", err)
	}
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("missing active generation = %v, want ErrReleaseActivationConflict", err)
	}
	s.assertPointerAbsent(t, project)

	s.activateGeneration(t, staged)

	s.mustExec(t, s.witness, `UPDATE `+s.qualified(ProjectReleasesTable)+
		` SET payload = jsonb_set(payload, '{publications,0,executionId}', '"tampered"') WHERE project = $1 AND id = $2`,
		project, cand.ID)
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("tampered candidate = %v, want ErrReleaseActivationConflict", err)
	}
	s.assertPointerAbsent(t, project)
	if _, err := s.releases.SaveRelease(s.ctx, cand); !errors.Is(err, ErrReleaseContentConflict) {
		t.Fatalf("resave over tampered payload = %v, want ErrReleaseContentConflict", err)
	}

	const project2 = "pgpubrel01-cas-negative-2"
	staged2 := s.stageGeneration(t, project2, "gen-1", digestB)
	s.activateGeneration(t, staged2)
	cand2 := candidateFixture(project2, "gen-1", "release_"+digestB)
	s.mustSave(t, s.releases, cand2)
	s.mustExec(t, s.witness, `UPDATE `+s.qualified("dataset_generation_manifest")+
		` SET state = 'FAILED' WHERE project = $1 AND generation = $2`, project2, "gen-1")
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand2, 0); !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("FAILED manifest = %v, want ErrReleaseActivationConflict", err)
	}
	s.assertPointerAbsent(t, project2)
}

func TestActiveReleaseReadSurfacesStoredInconsistency(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-tamper-read"
	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)
	cand := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, cand)
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); err != nil {
		s.fail("activation", err)
	}

	s.mustExec(t, s.witness, `UPDATE `+s.qualified(ProjectReleasesTable)+
		` SET payload = jsonb_set(payload, '{id}', '"release_forged"') WHERE project = $1 AND id = $2`,
		project, cand.ID)
	if _, err := s.releases.ReadActiveRelease(s.ctx, project); !errors.Is(err, ErrUnexpectedStoreResult) {
		t.Fatalf("read over forged payload = %v, want ErrUnexpectedStoreResult", err)
	}
}

func TestReleaseActivationAlreadyActiveIsZeroWriteAndRevisionStable(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-idempotent"
	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)
	cand := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, cand)
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); err != nil {
		s.fail("first activation", err)
	}
	before := s.witnessPointer(project)

	for i, expected := range []int64{1, 0} {
		active, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, expected)
		if err != nil {
			t.Fatalf("already-active activation %d (expected %d): %v", i, expected, err)
		}
		if !reflect.DeepEqual(active, publication.ActiveRelease{Release: cand, Revision: 1}) {
			t.Fatalf("already-active activation %d = %+v, want {%+v 1}", i, active, cand)
		}
	}
	after := s.witnessPointer(project)
	if after != before {
		t.Fatalf("already-active re-activation rewrote the pointer: before=%+v after=%+v", before, after)
	}
}

func TestConcurrentReleaseActivationsSameExpectedYieldOneWinner(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-race"
	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)

	candA := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, candA)
	candB := candidateFixture(project, "gen-1", "release_"+digestB)
	candB.Publications = append([]publication.ReleasePublication(nil), candA.Publications...)
	candB.Publications[1].ExecutionID = "exec-b"
	s.mustSave(t, s.releasesB, candB)

	candidates := []publication.ProjectRelease{candA, candB}
	stores := []*ReleaseStore{s.releases, s.releasesB}
	results := make([]error, len(candidates))
	winnerIDs := make([]publication.ActiveRelease, len(candidates))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			active, err := stores[i].CompareAndSwapActivateRelease(s.ctx, candidates[i], 0)
			results[i], winnerIDs[i] = err, active
		}(i)
	}
	close(start)
	wg.Wait()

	winner, loser := -1, -1
	for i, err := range results {
		t.Logf("activation %d (candidate %s, backend pids A=%d B=%d): err=%v", i, candidates[i].ID, s.pidA, s.pidB, err)
		if err == nil {
			if winner != -1 {
				t.Fatalf("both concurrent activations won: %v", results)
			}
			winner = i
			continue
		}
		if !errors.Is(err, publication.ErrReleaseActivationConflict) {
			t.Fatalf("activation %d failed with untyped error: %v", i, err)
		}
		if loser != -1 {
			t.Fatalf("both concurrent activations lost: %v", results)
		}
		loser = i
	}
	if winner == -1 || loser == -1 {
		t.Fatalf("expected exactly one winner and one typed-conflict loser, got %v", results)
	}
	if winnerIDs[winner].Revision != 1 || winnerIDs[winner].Release.ID != candidates[winner].ID {
		t.Fatalf("winner verdict = %+v, want revision 1 for %s", winnerIDs[winner], candidates[winner].ID)
	}
	s.assertPointerLiteral(t, project, candidates[winner].ID, 1)

	if _, err := stores[loser].CompareAndSwapActivateRelease(s.ctx, candidates[loser], 1); err != nil {
		s.fail("loser re-activation with fresh expected", err)
	}
	s.assertPointerLiteral(t, project, candidates[loser].ID, 2)
}

type observingAdapter struct {
	inner    GenerationOwnerAdapter
	pointers string

	entered       chan ReleaseActivation
	release       chan struct{}
	activated     []ReleaseActivation
	mu            sync.Mutex
	hookErr       error
	ensureErr     error
	ensureOn      bool
	hookViolation string
}

func newObservingAdapter(inner GenerationOwnerAdapter, pointersFQN string) *observingAdapter {
	return &observingAdapter{
		inner:    inner,
		pointers: pointersFQN,
		entered:  make(chan ReleaseActivation, 4),
		release:  make(chan struct{}),
	}
}

func (a *observingAdapter) EnsureGenerationPrerequisites(ctx context.Context, tx pgx.Tx, project, generation string) error {
	if a.ensureOn && a.ensureErr != nil {
		return a.ensureErr
	}
	return a.inner.EnsureGenerationPrerequisites(ctx, tx, project, generation)
}

func (a *observingAdapter) OnReleaseActivated(ctx context.Context, tx pgx.Tx, activation ReleaseActivation) error {
	var (
		seenReleaseID string
		seenRevision  int64
	)
	err := tx.QueryRow(ctx,
		`SELECT release_id, revision FROM `+a.pointers+` WHERE project = $1`, activation.Project).
		Scan(&seenReleaseID, &seenRevision)
	a.mu.Lock()
	if err != nil {
		a.hookViolation = fmt.Sprintf("in-transaction pointer read failed: %v", err)
	} else if seenReleaseID != activation.Release.ID || seenRevision != activation.Revision {
		a.hookViolation = fmt.Sprintf("in-transaction pointer read saw {%s %d}, want {%s %d}",
			seenReleaseID, seenRevision, activation.Release.ID, activation.Revision)
	}
	a.activated = append(a.activated, activation)
	a.mu.Unlock()
	a.entered <- activation
	select {
	case <-a.release:
	case <-ctx.Done():
		return fmt.Errorf("gate released by context: %w", ctx.Err())
	}
	return a.hookErr
}

func (a *observingAdapter) hookCalls() ([]ReleaseActivation, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ReleaseActivation(nil), a.activated...), a.hookViolation
}

func TestGenerationOwnerAdapterParticipatesInActivationTransaction(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-adapter"
	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)
	cand := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, cand)

	adapter := newObservingAdapter(s.releases.adapter, s.releases.pointers)
	s.releases.adapter = adapter

	type outcome struct {
		active publication.ActiveRelease
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		active, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0)
		done <- outcome{active, err}
	}()

	select {
	case activation := <-adapter.entered:
		if activation.Release.ID != cand.ID || activation.Revision != 1 {
			t.Fatalf("adapter saw activation %+v, want candidate %s at revision 1", activation, cand.ID)
		}
		if w := s.witnessPointer(project); w.exists {
			t.Fatalf("witness observed the pointer row before commit: %+v", w)
		}
		close(adapter.release)
	case <-time.After(60 * time.Second):
		t.Fatal("activation never reached the generation-owner hook")
	}

	select {
	case result := <-done:
		if result.err != nil {
			s.fail("activation with observing adapter", result.err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("activation never completed")
	}
	s.assertPointerLiteral(t, project, cand.ID, 1)
	if _, violation := adapter.hookCalls(); violation != "" {
		t.Fatalf("generation-owner hook did not observe its own uncommitted pointer write inside the activation transaction: %s", violation)
	}

	beforeHookCalls, _ := adapter.hookCalls()
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 1); err != nil {
		s.fail("already-active re-activation", err)
	}
	afterHookCalls, _ := adapter.hookCalls()
	if len(afterHookCalls) != len(beforeHookCalls) {
		t.Fatalf("already-active re-activation invoked the generation-owner hook %d more times, want 0 (zero-write path)",
			len(afterHookCalls)-len(beforeHookCalls))
	}

	second := candidateFixture(project, "gen-1", "release_"+digestB)
	second.Publications = append([]publication.ReleasePublication(nil), cand.Publications...)
	second.Publications[1].ExecutionID = "exec-fault"
	s.mustSave(t, s.releases, second)
	adapter2 := newObservingAdapter(s.releases.adapter, s.releases.pointers)
	adapter2.hookErr = errors.New("injected generation-owner fault")
	s.releases.adapter = adapter2
	done2 := make(chan error, 1)
	go func() {
		_, err := s.releases.CompareAndSwapActivateRelease(s.ctx, second, 1)
		done2 <- err
	}()
	select {
	case activation := <-adapter2.entered:
		if activation.Revision != 2 {
			t.Fatalf("faulted activation hook saw revision %d, want 2", activation.Revision)
		}
		s.assertPointerLiteral(t, project, cand.ID, 1)
		close(adapter2.release)
	case <-time.After(60 * time.Second):
		t.Fatal("activation never reached the faulted generation-owner hook")
	}
	var hookErr error
	select {
	case hookErr = <-done2:
	case <-time.After(60 * time.Second):
		t.Fatal("activation with faulty hook never completed")
	}
	if hookErr == nil {
		t.Fatal("activation with faulty generation-owner hook = nil, want failure")
	}
	if errors.Is(hookErr, publication.ErrReleaseActivationConflict) {
		t.Fatalf("generation-owner fault misclassified as CAS conflict: %v", hookErr)
	}
	if _, violation := adapter2.hookCalls(); violation != "" {
		t.Fatalf("faulted activation hook did not observe its own uncommitted pointer write: %s", violation)
	}
	s.assertPointerLiteral(t, project, cand.ID, 1)
	s.releases.adapter = adapter
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, second, 1); err != nil {
		s.fail("activation after hook recovery", err)
	}
	s.assertPointerLiteral(t, project, second.ID, 2)
}

func TestInjectedFailureBeforeCommitKeepsPointerUnchanged(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-commitfault"
	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)
	cand := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, cand)

	s.releases.commitFault = func(string) error { return errors.New("injected commit failure") }
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); err == nil {
		t.Fatal("activation with injected commit fault returned nil")
	} else if errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("commit fault misclassified as CAS conflict: %v", err)
	}
	s.releases.commitFault = nil
	s.assertPointerAbsent(t, project)

	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); err != nil {
		s.fail("activation after fault removal", err)
	}
	s.assertPointerLiteral(t, project, cand.ID, 1)
}

func TestReleaseStoreFailsClosedWithoutItsTables(t *testing.T) {
	s := newReleaseService(t)
	emptySchema := fmt.Sprintf("pgpubrel01_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgpubrel01ResourceNamePattern.MatchString(emptySchema) {
		t.Fatalf("owned schema name %q does not match the required pattern", emptySchema)
	}
	s.mustExec(t, s.witness, `CREATE SCHEMA "`+emptySchema+`"`)
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := s.witness.Exec(cleanupCtx, `DROP SCHEMA IF EXISTS "`+emptySchema+`" CASCADE`); err != nil {
			t.Errorf("drop empty schema: %s", s.scrub(err))
		}
	})
	orphan, err := NewReleaseStore(s.poolB, emptySchema)
	if err != nil {
		s.fail("build orphan store", err)
	}
	cand := candidateFixture("pgpubrel01-orphan", "gen-1", "release_"+digestA)
	_, err = orphan.SaveRelease(s.ctx, cand)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("save against unmigrated schema = %v, want SQLSTATE 42P01 undefined_table", s.scrub(err))
	}
	if _, err := orphan.ReadActiveRelease(s.ctx, "pgpubrel01-orphan"); !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("read against unmigrated schema = %v, want SQLSTATE 42P01", s.scrub(err))
	}
	if _, err := orphan.CompareAndSwapActivateRelease(s.ctx, cand, 0); !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("activate against unmigrated schema = %v, want SQLSTATE 42P01, never a typed conflict or fallback", s.scrub(err))
	}
}

func TestReleaseSchemaConstraintsRejectInvalidRows(t *testing.T) {
	s := newReleaseService(t)
	const project = "pgpubrel01-constraints"
	cand := candidateFixture(project, "gen-1", "release_"+digestA)
	s.mustSave(t, s.releases, cand)
	payload, err := marshalReleasePayload(cand)
	if err != nil {
		t.Fatal(err)
	}

	type negative struct {
		name      string
		sql       string
		args      []any
		wantState string
	}
	cases := []negative{
		{
			name:      "candidate project is never SQL NULL",
			sql:       `INSERT INTO "%[1]s".project_release (project, id, generation, git_commit, payload) VALUES (NULL, 'r', 'g', 'c', '{}'::jsonb)`,
			wantState: "23502",
		},
		{
			name:      "candidate project is never empty",
			sql:       `INSERT INTO "%[1]s".project_release (project, id, generation, git_commit, payload) VALUES ('', 'r', 'g', 'c', '{}'::jsonb)`,
			wantState: "23514",
		},
		{
			name:      "candidate payload is never SQL NULL",
			sql:       `INSERT INTO "%[1]s".project_release (project, id, generation, git_commit, payload) VALUES ('p', 'r', 'g', 'c', NULL)`,
			wantState: "23502",
		},
		{
			name:      "candidate payload JSON null is not an object",
			sql:       `INSERT INTO "%[1]s".project_release (project, id, generation, git_commit, payload) VALUES ('p', 'r', 'g', 'c', 'null'::jsonb)`,
			wantState: "23514",
		},
		{
			name:      "candidate payload JSON array is not an object",
			sql:       `INSERT INTO "%[1]s".project_release (project, id, generation, git_commit, payload) VALUES ('p', 'r', 'g', 'c', '[]'::jsonb)`,
			wantState: "23514",
		},
		{
			name:      "duplicate (project, id) candidates are impossible",
			sql:       `INSERT INTO "%[1]s".project_release (project, id, generation, git_commit, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`,
			args:      []any{project, cand.ID, cand.Generation, cand.GitCommit, payload},
			wantState: "23505",
		},
		{
			name:      "pointer revision floor is 1 (0 stands in for absent only as expectedRevision)",
			sql:       `INSERT INTO "%[1]s".active_project_release (project, release_id, revision) VALUES ('p', 'r', 0)`,
			wantState: "23514",
		},
		{
			name:      "pointer revision is never SQL NULL",
			sql:       `INSERT INTO "%[1]s".active_project_release (project, release_id, revision) VALUES ('p', 'r', NULL)`,
			wantState: "23502",
		},
		{
			name:      "pointer release_id is never SQL NULL",
			sql:       `INSERT INTO "%[1]s".active_project_release (project, release_id, revision) VALUES ('p', NULL, 1)`,
			wantState: "23502",
		},
		{
			name:      "pointer must name a persisted candidate",
			sql:       `INSERT INTO "%[1]s".active_project_release (project, release_id, revision) VALUES ('p', 'release_missing', 1)`,
			wantState: "23503",
		},
	}
	for _, c := range cases {
		sql := fmt.Sprintf(c.sql, s.schema)
		_, err := s.witness.Exec(s.ctx, sql, c.args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != c.wantState {
			t.Errorf("%s: err = %v, want SQLSTATE %s", c.name, s.scrub(err), c.wantState)
		}
	}

	staged := s.stageGeneration(t, project, "gen-1", digestA)
	s.activateGeneration(t, staged)
	if _, err := s.releases.CompareAndSwapActivateRelease(s.ctx, cand, 0); err != nil {
		s.fail("activation for FK fixture", err)
	}
	if _, err := s.witness.Exec(s.ctx,
		`DELETE FROM `+s.qualified(ProjectReleasesTable)+` WHERE project = $1 AND id = $2`, project, cand.ID); err == nil {
		t.Fatal("deleting the active candidate = nil, want FK violation")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("delete active candidate: err = %v, want SQLSTATE 23503", s.scrub(err))
		}
	}
}

func TestZZNoLeftoverPgpubrel01Resources(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	var leftoverDBs int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'pgpubrel01\_it\_%' ESCAPE '\'`).Scan(&leftoverDBs); err != nil {
		t.Fatalf("count leftover databases: %v", err)
	}
	if leftoverDBs != 0 {
		var names []string
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'pgpubrel01\_it\_%' ESCAPE '\'`)
		if err == nil {
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err == nil {
					names = append(names, n)
				}
			}
			rows.Close()
		}
		t.Fatalf("%d isolated databases left behind: %v", leftoverDBs, names)
	}

	var leftoverSchemas int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgpubrel01\_it\_%' ESCAPE '\'`).Scan(&leftoverSchemas); err != nil {
		t.Fatalf("count leftover schemas: %v", err)
	}
	if leftoverSchemas != 0 {
		t.Fatalf("%d owned schemas left behind in the service database", leftoverSchemas)
	}
}
