package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	datasetpostgres "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	publicationpostgres "github.com/calypr/loom/internal/backends/postgres/internal/publication"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

var pgactNameRe = regexp.MustCompile(`^verify_pgact_[0-9]+_[0-9a-f]+(?:_(?:ctl|lanes|execs))?$`)

var (
	pgactBaselineOnce    sync.Once
	pgactDBBaseline      atomic.Int64
	pgactLive            atomic.Int64
	pgactSchemaBaseline  atomic.Int64
	pgactBaselineFailure atomic.Value
)

type pgactMigrationPin struct {
	version  int64
	name     string
	checksum string
}

var (
	pgactControlPins = []pgactMigrationPin{
		{6, "explorer_revision", "3f6c44bff72d7837634e8c170f0b97e256e38d35f7c844fad27f4fbda38ecc09"},
	}
	pgactLanePins = []pgactMigrationPin{
		{3, "dataset_generation_manifest", "06c614680a9c1cd4ba2ef0ac0471053bcb3d8e471c36d3852e70682887a72f5d"},
	}
	pgactExecPins = []pgactMigrationPin{
		{4, "publication_bundle_catalog", "adcb4683ffd1611b9c68e7a49aaa4b8670d106d65df927cbb8e11866be224943"},
	}
)

func pgactCaptureBaseline(admin *pgxpool.Pool) {
	pgactBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var mine, schemas int64
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_pgact\_%' ESCAPE '\'`).Scan(&mine); err != nil {
			pgactBaselineFailure.Store(fmt.Errorf("baseline: query owned database namespace: %w", err))
			return
		}
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_pgact\_%' ESCAPE '\'`).Scan(&schemas); err != nil {
			pgactBaselineFailure.Store(fmt.Errorf("baseline: query owned schema namespace: %w", err))
			return
		}
		pgactDBBaseline.Store(mine)
		pgactSchemaBaseline.Store(schemas)
	})
	if stored := pgactBaselineFailure.Load(); stored != nil {
		panic(stored.(error))
	}
}

func pgactRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type pgactFixture struct {
	ctx         context.Context
	admin       *pgxpool.Pool
	iso         *pgxpool.Pool
	dbName      string
	ctlSchema   string
	lanesSchema string
	execsSchema string
	dsn         string
	scrub       func(any) string
	fail        func(step string, err error)
}

func newPGActivationFixture(t *testing.T) *pgactFixture {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 repository-generation activation integration tests")
	}
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
	pgactCaptureBaseline(admin)
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("verify_pgact_%d_%s", time.Now().UnixNano(), pgactRandomHex(t))
	if !pgactNameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		var namespaceDatabases int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_pgact\_%' ESCAPE '\'`).Scan(&namespaceDatabases); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if want := pgactDBBaseline.Load() + pgactLive.Load(); int64(namespaceDatabases) != want {
			t.Errorf("leftover audit: %d verify_pgact_* databases remain, baseline+live is %d", namespaceDatabases, want)
		}
		var namespaceSchemas int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_pgact\_%' ESCAPE '\'`).Scan(&namespaceSchemas); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if int64(namespaceSchemas) != pgactSchemaBaseline.Load() {
			t.Errorf("leftover audit: %d verify_pgact_* schemas remain in the service database, baseline is %d", namespaceSchemas, pgactSchemaBaseline.Load())
		}
	})
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
		pgactLive.Add(-1)
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	pgactLive.Add(1)
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	if probe, err := url.Parse(dsn); err != nil || probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	base := fmt.Sprintf("verify_pgact_%d_%s", time.Now().UnixNano(), pgactRandomHex(t))
	ctlSchema := base + "_ctl"
	lanesSchema := base + "_lanes"
	execsSchema := base + "_execs"
	for _, schema := range []string{ctlSchema, lanesSchema, execsSchema} {
		if !pgactNameRe.MatchString(schema) {
			t.Fatalf("owned schema name %q does not match the required pattern", schema)
		}
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			fail("create owned schema "+schema, err)
		}
	}
	t.Cleanup(func() {
		for _, schema := range []string{execsSchema, lanesSchema, ctlSchema} {
			if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
				t.Errorf("drop owned schema %s: %s", schema, scrub(err))
			}
		}
	})

	return &pgactFixture{
		ctx: ctx, admin: admin, iso: iso, dbName: dbName,
		ctlSchema: ctlSchema, lanesSchema: lanesSchema, execsSchema: execsSchema,
		dsn: dsn, scrub: scrub, fail: fail,
	}
}

func (f *pgactFixture) migrate(t *testing.T) {
	t.Helper()
	ctlHistory, err := OwnerMigrations()
	if err != nil {
		f.fail("explorer owner migrations", err)
	}
	f.verifyPins(t, "explorer control", ctlHistory, pgactControlPins)
	lanesHistory, err := datasetpostgres.OwnerMigrations()
	if err != nil {
		f.fail("dataset owner migrations", err)
	}
	f.verifyPins(t, "dataset lane", lanesHistory, pgactLanePins)
	execsHistory, err := publicationpostgres.OwnerMigrations()
	if err != nil {
		f.fail("publication owner migrations", err)
	}
	f.verifyPins(t, "publication lane", execsHistory, pgactExecPins)

	client := f.newPool(t)
	f.migrateHistory(t, client, f.ctlSchema, ctlHistory, 11, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.migrateHistory(t, client, f.lanesSchema, lanesHistory, 8, []int64{1, 3, 8})
	f.migrateHistory(t, client, f.execsSchema, execsHistory, 9, []int64{1, 4, 6, 7, 9})
}

func (f *pgactFixture) verifyPins(t *testing.T, label string, history []pgstore.Migration, pins []pgactMigrationPin) {
	t.Helper()
	byVersion := make(map[int64]pgstore.Migration, len(history))
	for _, m := range history {
		byVersion[m.Version] = m
	}
	for _, pin := range pins {
		m, ok := byVersion[pin.version]
		if !ok {
			t.Fatalf("%s owner history lacks version %d (%s): the owning ticket's migration vanished", label, pin.version, pin.name)
		}
		if m.Name != pin.name || m.Checksum != pin.checksum {
			t.Fatalf("%s owner history version %d = {Name:%q Checksum:%q}, want the byte-frozen merge pin {Name:%q Checksum:%q}: the owning ticket's migration drifted",
				label, pin.version, m.Name, m.Checksum, pin.name, pin.checksum)
		}
	}
}

func (f *pgactFixture) migrateHistory(t *testing.T, client *pgstore.Client, schema string,
	history []pgstore.Migration, wantTop int64, wantVersions []int64,
) {
	t.Helper()
	runner, err := pgstore.NewMigrationRunner(client, schema, history)
	if err != nil {
		f.fail("new migration runner for "+schema, err)
	}
	result, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("migrate schema "+schema, err)
	}
	if len(result.Applied) != len(wantVersions) {
		t.Fatalf("schema %s applied %d migrations, want %d", schema, len(result.Applied), len(wantVersions))
	}
	if result.LatestVersion != wantTop {
		t.Fatalf("schema %s latest version = %d, want %d", schema, result.LatestVersion, wantTop)
	}
	for i, want := range wantVersions {
		if result.Applied[i].Version != want {
			t.Fatalf("schema %s ledger position %d = %d, want %d", schema, i, result.Applied[i].Version, want)
		}
	}
}

func (f *pgactFixture) newPool(t *testing.T) *pgstore.Client {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN: f.dsn, MaxConns: 8, MinConns: 0, AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open pg-store pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (f *pgactFixture) newStore(t *testing.T, client *pgstore.Client) *Store {
	t.Helper()
	store, err := New(client, f.ctlSchema)
	if err != nil {
		f.fail("build Explorer control-plane store", err)
	}
	return store
}

func (f *pgactFixture) newPublishStore(t *testing.T, client *pgstore.Client) *PublishStore {
	t.Helper()
	ps, err := NewPublishStore(client, f.ctlSchema, f.lanesSchema)
	if err != nil {
		f.fail("build publish store", err)
	}
	return ps
}

func (f *pgactFixture) newActivationStore(t *testing.T, client *pgstore.Client) *ActivationStore {
	t.Helper()
	as, err := NewActivationStore(client, f.ctlSchema, f.lanesSchema, f.execsSchema)
	if err != nil {
		f.fail("build activation store", err)
	}
	return as
}

func (f *pgactFixture) newGenerationStore(t *testing.T, client *pgstore.Client) *datasetpostgres.Store {
	t.Helper()
	gen, err := datasetpostgres.New(client, f.lanesSchema)
	if err != nil {
		f.fail("build generation store", err)
	}
	return gen
}

func (f *pgactFixture) newReleaseStore(t *testing.T, client *pgstore.Client) *datasetpostgres.ReleaseStore {
	t.Helper()
	rel, err := datasetpostgres.NewReleaseStore(client, f.lanesSchema)
	if err != nil {
		f.fail("build release store", err)
	}
	return rel
}

func (f *pgactFixture) newRegistry(t *testing.T) *publicationpostgres.Registry {
	t.Helper()
	registry, err := publicationpostgres.New(f.iso, f.execsSchema)
	if err != nil {
		f.fail("build publication registry", err)
	}
	return registry
}

func (f *pgactFixture) ctlTable(table string) string {
	return sanitizeTableRef(f.ctlSchema, table)
}

func (f *pgactFixture) lanesTable(table string) string {
	return sanitizeTableRef(f.lanesSchema, table)
}

func (f *pgactFixture) execsTable(table string) string {
	return sanitizeTableRef(f.execsSchema, table)
}

func (f *pgactFixture) execRaw(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.iso.Exec(f.ctx, sql, args...); err != nil {
		f.fail("direct exec: "+sql, err)
	}
}

type pgactRevisionState struct {
	Status      string
	ActivatedAt *time.Time
	FailedAt    *time.Time
	Diagnostics string
	Publication string
}

func (f *pgactFixture) revisionState(t *testing.T, id string) pgactRevisionState {
	t.Helper()
	var out pgactRevisionState
	err := f.iso.QueryRow(f.ctx, "SELECT status, activated_at, failed_at, COALESCE(diagnostics::text, '<null>'), COALESCE(publication::text, '<null>') FROM "+
		f.ctlTable(RevisionsTable)+" WHERE id = $1", id).
		Scan(&out.Status, &out.ActivatedAt, &out.FailedAt, &out.Diagnostics, &out.Publication)
	if err != nil {
		f.fail("read revision state "+id, err)
	}
	return out
}

func (f *pgactFixture) ownerActivationState(t *testing.T, project string) (string, time.Time) {
	t.Helper()
	var activeRevisionID string
	var updatedAt time.Time
	err := f.iso.QueryRow(f.ctx, "SELECT active_revision_id, updated_at FROM "+
		f.ctlTable(OwnerTable)+" WHERE project = $1 AND explorer_id = 'default'", project).
		Scan(&activeRevisionID, &updatedAt)
	if err != nil {
		f.fail("read owner activation state "+project, err)
	}
	return activeRevisionID, updatedAt
}

func (f *pgactFixture) snapshotRevisions(t *testing.T, project string) string {
	t.Helper()
	var out string
	err := f.iso.QueryRow(f.ctx, "SELECT COALESCE(string_agg(row_text, E'\\x1e' ORDER BY row_text), '<none>') FROM (SELECT (to_jsonb(t) - 'activated_at' - 'created_at')::text AS row_text FROM "+
		f.ctlTable(RevisionsTable)+" t WHERE project = $1) s", project).Scan(&out)
	if err != nil {
		f.fail("snapshot revisions "+project, err)
	}
	return out
}

func (f *pgactFixture) snapshotOwners(t *testing.T, project string) string {
	t.Helper()
	var out string
	err := f.iso.QueryRow(f.ctx, "SELECT COALESCE(string_agg(row_text, E'\\x1e' ORDER BY row_text), '<none>') FROM (SELECT (to_jsonb(t) - 'updated_at')::text AS row_text FROM "+
		f.ctlTable(OwnerTable)+" t WHERE project = $1) s", project).Scan(&out)
	if err != nil {
		f.fail("snapshot owners "+project, err)
	}
	return out
}

func pgactRevisionWithMaterialization(id, project, receiptID, tag, materializationID string, withMaterialization bool) explorer.Revision {
	revision := pubauthRevision(id, project, "default", receiptID, tag)
	if withMaterialization {
		revision.Materializations = []explorer.Materialization{{
			OutputID: "output", Output: "output", MaterializationID: materializationID,
			Fingerprint: "fp-pgact-" + tag,
		}}
	} else {
		revision.Materializations = nil
	}
	return revision
}

func (f *pgactFixture) stageGeneration(t *testing.T, gen *datasetpostgres.Store, project, generation, urn string) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		f.fail("ref", err)
	}
	snapshot, err := dataset.NewSchemaSnapshot(urn, "4.0.1", pubauthSnapshotSHA256, []string{"Patient"})
	if err != nil {
		f.fail("snapshot", err)
	}
	loading, err := dataset.NewManifest(ref, snapshot)
	if err != nil {
		f.fail("manifest", err)
	}
	if _, err := gen.CreateManifest(f.ctx, loading); err != nil {
		f.fail("create manifest", err)
	}
	staged, err := gen.TransitionManifest(f.ctx, loading, dataset.StateStaged)
	if err != nil {
		f.fail("stage manifest", err)
	}
	return staged
}

func (f *pgactFixture) activateGeneration(t *testing.T, gen *datasetpostgres.Store, staged dataset.Manifest, expected *dataset.Ref) {
	t.Helper()
	if err := gen.ActivateGeneration(f.ctx, staged, expected); err != nil {
		f.fail("activate generation", err)
	}
}

func (f *pgactFixture) seedPublishedExecution(t *testing.T, registry *publicationpostgres.Registry, project, generation, executionID, state string) {
	t.Helper()
	unique := strings.TrimPrefix(executionID, "pgact-exec-")
	key := "pgact-key-" + unique
	owner := "pgact-publisher-" + unique
	claimed, err := registry.AcquireBundleLease(f.ctx, key, owner, time.Now().UTC().Add(5*time.Minute))
	if err != nil || !claimed {
		f.fail("acquire publication lease", fmt.Errorf("claimed=%v err=%v", claimed, err))
	}
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	execution := dataframepublication.BundleExecution{
		ID:  executionID,
		Key: key,
		BundleIdentity: dataframepublication.BundleIdentity{
			Name: "pgact-recipe", TranslationVersion: "v1", OutputName: "output",
			Project: project, DatasetGeneration: generation,
			RecipeDigest: "sha256:pgact-" + unique + "-r", SchemaDigest: "sha256:pgact-" + unique + "-s",
			ReceiptID: "pgact-receipt-" + unique, ScopeDigest: "pgact-scope-" + unique, EngineVersion: "pgact-engine",
		},
		State: dataframepublication.BundleState(state),
		Outputs: []dataframepublication.BundleOutputRecord{{
			Name: "output", PhysicalTable: "ch_pgact_" + unique,
			Selector: dataset.DataframeSelector{Recipe: "pgact-recipe", TranslationVersion: "v1", Output: "output"},
			RowCount: 7, ByteCount: 700,
			State: dataframepublication.BundleState(state), VerifiedAt: &stamp,
		}},
		CreatedAt: stamp.Add(-time.Minute),
		UpdatedAt: stamp,
		OwnerID:   owner,
	}
	if state == "PUBLISHED" {
		execution.ReadyAt = &stamp
		execution.PublishedAt = &stamp
	}
	if err := registry.SaveExecution(f.ctx, execution, owner); err != nil {
		f.fail("save execution "+executionID, err)
	}
}

type pgactWorld struct {
	store       *Store
	publish     *PublishStore
	activation  *ActivationStore
	registry    *publicationpostgres.Registry
	generations *datasetpostgres.Store
	releases    *datasetpostgres.ReleaseStore
	project     string
	priorID     string
	candidateID string
	executionID string
	generation  string
}

func (f *pgactFixture) seedWorld(t *testing.T, client *pgstore.Client, project, tag string,
	mutateCandidate func(revision *explorer.Revision), opts func(w *pgactWorld),
) *pgactWorld {
	t.Helper()
	world := &pgactWorld{
		project:     project,
		generation:  pubauthGeneration,
		priorID:     "pgact-prior-" + tag,
		candidateID: "pgact-candidate-" + tag,
		executionID: "pgact-exec-" + tag,
	}
	world.store = f.newStore(t, client)
	world.publish = f.newPublishStore(t, client)
	world.activation = f.newActivationStore(t, client)
	world.registry = f.newRegistry(t)
	world.generations = f.newGenerationStore(t, client)
	world.releases = f.newReleaseStore(t, client)

	if _, err := world.store.Create(f.ctx, pubauthOwner(project, "default", explorer.ManagementRepository)); err != nil {
		f.fail("create repository owner", err)
	}
	staged := f.stageGeneration(t, world.generations, project, world.generation, "urn:pgact-"+tag)
	f.activateGeneration(t, world.generations, staged, nil)

	release := pubauthRelease(project, world.generation, "pgact-rel-"+tag)
	if _, err := world.releases.SaveRelease(f.ctx, release); err != nil {
		f.fail("save prior release", err)
	}
	priorReceipt := pubauthReceipt(t, project, "default", tag+"-prior")
	priorRevision := pubauthRevision(world.priorID, project, "default", priorReceipt.ID, tag+"-prior")
	priorRevision.Materializations = []explorer.Materialization{{
		OutputID: "output", Output: "output", MaterializationID: "pgact-prior-mat-" + tag, Fingerprint: "fp-pgact-prior",
	}}
	if _, err := world.publish.PublishAuthoring(f.ctx, priorReceipt, priorRevision, release, 0); err != nil {
		f.fail("publish prior revision", err)
	}
	f.seedPublishedExecution(t, world.registry, project, world.generation, world.executionID, "PUBLISHED")

	candidateReceipt := pubauthReceipt(t, project, "default", tag+"-cand")
	candidate := pgactRevisionWithMaterialization(world.candidateID, project, candidateReceipt.ID, tag+"-cand", world.executionID, true)
	if mutateCandidate != nil {
		mutateCandidate(&candidate)
	}
	if _, err := world.store.InsertRevision(f.ctx, candidate); err != nil {
		f.fail("insert candidate revision", err)
	}
	if opts != nil {
		opts(world)
	}
	return world
}

func requireActivationConflict(t *testing.T, f *pgactFixture, err error, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: activation returned nil, want explorer.ErrDraftConflict", label)
	}
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("%s: error = %s, want explorer.ErrDraftConflict", label, f.scrub(err))
	}
}

func TestActivateRepositoryGenerationUsesCompositeGuardsAgainstPostgreSQL(t *testing.T) {
	f := newPGActivationFixture(t)
	f.migrate(t)
	client := f.newPool(t)

	t.Run("happy-flip-supersedes-prior-and-is-idempotent", func(t *testing.T) {
		const project = "pgact_happy_01"
		world := f.seedWorld(t, client, project, "happy01", nil, nil)

		if err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID); err != nil {
			f.fail("ActivateRepositoryGeneration", err)
		}
		candidate := f.revisionState(t, world.candidateID)
		if candidate.Status != "ACTIVE" {
			t.Fatalf("candidate status = %s, want ACTIVE", candidate.Status)
		}
		if candidate.ActivatedAt == nil || candidate.ActivatedAt.IsZero() {
			t.Fatalf("candidate activated_at = %v, want set", candidate.ActivatedAt)
		}
		if candidate.FailedAt != nil {
			t.Fatalf("candidate failed_at = %v, want NULL", candidate.FailedAt)
		}
		if candidate.Diagnostics != "[]" {
			t.Fatalf("candidate diagnostics = %s, want []", candidate.Diagnostics)
		}
		var publication struct {
			State      string    `json:"state"`
			RevisionID string    `json:"revisionId"`
			Generation string    `json:"generation"`
			UpdatedAt  time.Time `json:"updatedAt"`
		}
		if err := json.Unmarshal([]byte(candidate.Publication), &publication); err != nil {
			f.fail("decode candidate publication", err)
		}
		if publication.State != "ACTIVE" || publication.RevisionID != world.candidateID ||
			publication.Generation != world.generation || publication.UpdatedAt.IsZero() {
			t.Fatalf("activation publication = %+v, want ACTIVE/%s/%s/non-zero updatedAt",
				publication, world.candidateID, world.generation)
		}
		if prior := f.revisionState(t, world.priorID); prior.Status != "SUPERSEDED" {
			t.Fatalf("prior status = %s, want SUPERSEDED", prior.Status)
		}
		if active, _ := f.ownerActivationState(t, project); active != world.candidateID {
			t.Fatalf("owner active_revision_id = %q, want %q", active, world.candidateID)
		}

		before := f.revisionState(t, world.candidateID)
		beforeOwner, beforeOwnerUpdated := f.ownerActivationState(t, project)
		if err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID); err != nil {
			f.fail("idempotent replay", err)
		}
		after := f.revisionState(t, world.candidateID)
		afterOwner, afterOwnerUpdated := f.ownerActivationState(t, project)
		if before.ActivatedAt == nil || after.ActivatedAt == nil || !before.ActivatedAt.Equal(*after.ActivatedAt) {
			t.Fatalf("replay restated activated_at: %v -> %v", before.ActivatedAt, after.ActivatedAt)
		}
		if beforeOwner != afterOwner || !beforeOwnerUpdated.Equal(afterOwnerUpdated) {
			t.Fatalf("replay churned the owner pointer: %q/%v -> %q/%v", beforeOwner, beforeOwnerUpdated, afterOwner, afterOwnerUpdated)
		}
	})

	guardCases := []struct {
		name      string
		candidate func(revision *explorer.Revision)
		setup     func(f *pgactFixture, w *pgactWorld)
	}{
		{"no-manifest", nil, func(f *pgactFixture, w *pgactWorld) {
			f.execRaw(t, "DELETE FROM "+f.lanesTable("dataset_active_generation")+" WHERE project = $1", w.project)
			f.execRaw(t, "DELETE FROM "+f.lanesTable("dataset_generation_manifest")+" WHERE project = $1", w.project)
		}},
		{"manifest-failed", nil, func(f *pgactFixture, w *pgactWorld) {
			f.execRaw(t, "UPDATE "+f.lanesTable("dataset_generation_manifest")+" SET state = 'FAILED' WHERE project = $1", w.project)
		}},
		{"no-active-generation", nil, func(f *pgactFixture, w *pgactWorld) {
			f.execRaw(t, "DELETE FROM "+f.lanesTable("dataset_active_generation")+" WHERE project = $1", w.project)
		}},
		{"pointer-names-other-generation", nil, func(f *pgactFixture, w *pgactWorld) {
			other := w.generation + "_second"
			staged := f.stageGeneration(t, w.generations, w.project, other, "urn:pgact-second-"+w.project)
			current, err := dataset.NewRef(w.project, w.generation)
			if err != nil {
				f.fail("second-generation pointer expected ref", err)
			}
			f.activateGeneration(t, w.generations, staged, &current)
		}},
		{"execution-missing", nil, func(f *pgactFixture, w *pgactWorld) {
			f.execRaw(t, "DELETE FROM "+f.execsTable(publicationpostgres.BundleExecutionsTable)+" WHERE id = $1", w.executionID)
		}},
		{"execution-not-published", nil, func(f *pgactFixture, w *pgactWorld) {
			f.execRaw(t, "UPDATE "+f.execsTable(publicationpostgres.BundleExecutionsTable)+" SET state = 'READY' WHERE id = $1", w.executionID)
		}},
		{"zero-materializations", func(revision *explorer.Revision) {
			revision.Materializations = nil
		}, nil},
		{"owner-not-repository-mode", nil, func(f *pgactFixture, w *pgactWorld) {
			f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET management_mode = 'INTERACTIVE' WHERE project = $1 AND explorer_id = 'default'", w.project)
		}},
		{"candidate-wrong-explorer", func(revision *explorer.Revision) {
			revision.ExplorerID = "other"
		}, nil},
		{"candidate-wrong-generation", func(revision *explorer.Revision) {
			revision.SourceGeneration = "pgact_other_gen"
		}, nil},
		{"candidate-failed", nil, func(f *pgactFixture, w *pgactWorld) {
			if _, err := w.store.FailRevisionFrom(f.ctx, w.candidateID, explorer.RevisionReady,
				[]explorer.Diagnostic{{Severity: "ERROR", Code: "MATERIALIZATION_FAILED", Message: "injected", Retryable: false}}); err != nil {
				f.fail("fail candidate before activation", err)
			}
		}},
	}
	for _, tc := range guardCases {
		t.Run(tc.name, func(t *testing.T) {
			project := "pgact_guard_" + strings.ReplaceAll(tc.name, "-", "_")
			tag := strings.TrimPrefix(project, "pgact_guard_")
			world := f.seedWorld(t, client, project, tag, tc.candidate, func(w *pgactWorld) {
				if tc.setup != nil {
					tc.setup(f, w)
				}
			})
			beforeRevisions := f.snapshotRevisions(t, project)
			beforeOwners := f.snapshotOwners(t, project)

			err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID)
			requireActivationConflict(t, f, err, tc.name)

			if after := f.snapshotRevisions(t, project); after != beforeRevisions {
				t.Fatalf("%s: guard miss mutated revisions:\nbefore=%s\nafter=%s", tc.name, f.scrub(beforeRevisions), f.scrub(after))
			}
			if after := f.snapshotOwners(t, project); after != beforeOwners {
				t.Fatalf("%s: guard miss mutated owners:\nbefore=%s\nafter=%s", tc.name, f.scrub(beforeOwners), f.scrub(after))
			}
		})
	}

	t.Run("missing-candidate-revision", func(t *testing.T) {
		const project = "pgact_missing_candidate"
		world := f.seedWorld(t, client, project, "missingcand", nil, nil)
		err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, "pgact-revision-that-does-not-exist")
		requireActivationConflict(t, f, err, "missing-candidate-revision")
	})

	t.Run("dangling-prior-pointer", func(t *testing.T) {
		const project = "pgact_dangling_prior"
		world := f.seedWorld(t, client, project, "dangling01", nil, func(w *pgactWorld) {
			f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET active_revision_id = 'pgact-revision-that-does-not-exist' WHERE project = $1 AND explorer_id = 'default'", w.project)
		})
		beforeRevisions := f.snapshotRevisions(t, project)
		err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID)
		requireActivationConflict(t, f, err, "dangling-prior-pointer")
		if after := f.snapshotRevisions(t, project); after != beforeRevisions {
			t.Fatalf("dangling prior mutated revisions:\nbefore=%s\nafter=%s", f.scrub(beforeRevisions), f.scrub(after))
		}
		if candidate := f.revisionState(t, world.candidateID); candidate.Status != "READY" {
			t.Fatalf("candidate status after dangling prior = %s, want READY", candidate.Status)
		}
	})

	t.Run("replay-with-orphaned-active-candidate-conflicts", func(t *testing.T) {
		const project = "pgact_orphan_active"
		world := f.seedWorld(t, client, project, "orphana1", nil, nil)
		if err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID); err != nil {
			f.fail("initial activation", err)
		}
		f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET active_revision_id = $2 WHERE project = $1 AND explorer_id = 'default'", project, world.priorID)
		beforeRevisions := f.snapshotRevisions(t, project)
		beforeOwners := f.snapshotOwners(t, project)

		err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID)
		requireActivationConflict(t, f, err, "replay-with-orphaned-active-candidate-conflicts")

		if after := f.snapshotRevisions(t, project); after != beforeRevisions {
			t.Fatalf("orphaned-ACTIVE replay mutated revisions:\nbefore=%s\nafter=%s", f.scrub(beforeRevisions), f.scrub(after))
		}
		if after := f.snapshotOwners(t, project); after != beforeOwners {
			t.Fatalf("orphaned-ACTIVE replay churned the owner pointer:\nbefore=%s\nafter=%s", f.scrub(beforeOwners), f.scrub(after))
		}
	})

	t.Run("superseded-candidate-conflicts", func(t *testing.T) {
		const project = "pgact_superseded_cand"
		world := f.seedWorld(t, client, project, "supcan01", nil, nil)
		if err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID); err != nil {
			f.fail("initial activation", err)
		}
		beforeRevisions := f.snapshotRevisions(t, project)
		beforeOwners := f.snapshotOwners(t, project)

		err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.priorID)
		requireActivationConflict(t, f, err, "superseded-candidate-conflicts")

		if after := f.snapshotRevisions(t, project); after != beforeRevisions {
			t.Fatalf("superseded-candidate activation mutated revisions:\nbefore=%s\nafter=%s", f.scrub(beforeRevisions), f.scrub(after))
		}
		if after := f.snapshotOwners(t, project); after != beforeOwners {
			t.Fatalf("superseded-candidate activation churned the owner pointer:\nbefore=%s\nafter=%s", f.scrub(beforeOwners), f.scrub(after))
		}
		if candidate := f.revisionState(t, world.candidateID); candidate.Status != "ACTIVE" {
			t.Fatalf("current ACTIVE revision downgraded by superseded-candidate attempt: %s", candidate.Status)
		}
	})

	t.Run("pointer-already-names-ready-candidate-activates-without-self-supersede", func(t *testing.T) {
		const project = "pgact_prior_not_active"
		world := f.seedWorld(t, client, project, "priornotact", nil, func(w *pgactWorld) {
			f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET active_revision_id = $2 WHERE project = $1 AND explorer_id = 'default'", w.project, w.candidateID)
		})
		if err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID); err != nil {
			f.fail("activation with pointer already naming the READY candidate", err)
		}
		if candidate := f.revisionState(t, world.candidateID); candidate.Status != "ACTIVE" {
			t.Fatalf("candidate status = %s, want ACTIVE", candidate.Status)
		}
		if active, _ := f.ownerActivationState(t, project); active != world.candidateID {
			t.Fatalf("owner active_revision_id = %q, want %q", active, world.candidateID)
		}
		if prior := f.revisionState(t, world.priorID); prior.Status != "ACTIVE" {
			t.Fatalf("prior status = %s, want ACTIVE: supersede follows the pointer, and the pointer named the candidate", prior.Status)
		}
	})
}

func installActivationCrashSeam(as *ActivationStore, gate func(step string, tx pgx.Tx) error) *[]string {
	steps := &[]string{}
	as.crashAt = func(ctx context.Context, tx pgx.Tx, step string) error {
		*steps = append(*steps, step)
		if gate != nil {
			return gate(step, tx)
		}
		return nil
	}
	return steps
}

func TestActivationUsesTransactionsAndSingleModificationQueriesAgainstPostgreSQL(t *testing.T) {
	f := newPGActivationFixture(t)
	f.migrate(t)
	client := f.newPool(t)

	t.Run("mutations-run-in-one-ordered-leg-sequence", func(t *testing.T) {
		const project = "pgact_tx_seq_01"
		world := f.seedWorld(t, client, project, "txseq01", nil, nil)
		steps := installActivationCrashSeam(world.activation, nil)
		if err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID); err != nil {
			f.fail("ActivateRepositoryGeneration", err)
		}
		want := []string{"prior-supersede", "candidate-activate", "owner-activate", "pre-commit"}
		if strings.Join(*steps, ",") != strings.Join(want, ",") {
			t.Fatalf("leg sequence = %v, want %v", *steps, want)
		}
	})

	for _, crashStep := range []string{"prior-supersede", "candidate-activate", "owner-activate"} {
		t.Run("rollback-at-"+crashStep, func(t *testing.T) {
			project := "pgact_tx_" + crashStep
			tag := strings.ReplaceAll(crashStep, "-", "")
			world := f.seedWorld(t, client, project, tag, nil, nil)
			steps := installActivationCrashSeam(world.activation, func(step string, _ pgx.Tx) error {
				if step == crashStep {
					return errors.New("injected activation fault")
				}
				return nil
			})
			beforeRevisions := f.snapshotRevisions(t, project)
			beforeOwners := f.snapshotOwners(t, project)

			err := world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID)
			if err == nil || !strings.Contains(err.Error(), "injected activation fault") {
				t.Fatalf("%s: error = %s, want the injected fault", crashStep, f.scrub(err))
			}
			if strings.Join(*steps, ",") == "" {
				t.Fatalf("%s: crash seam never fired", crashStep)
			}
			if after := f.snapshotRevisions(t, project); after != beforeRevisions {
				t.Fatalf("%s: fault left partial revision state:\nbefore=%s\nafter=%s",
					crashStep, f.scrub(beforeRevisions), f.scrub(after))
			}
			if after := f.snapshotOwners(t, project); after != beforeOwners {
				t.Fatalf("%s: fault left partial owner state:\nbefore=%s\nafter=%s",
					crashStep, f.scrub(beforeOwners), f.scrub(after))
			}
			if candidate := f.revisionState(t, world.candidateID); candidate.Status != "READY" {
				t.Fatalf("%s: candidate status after rollback = %s, want READY", crashStep, candidate.Status)
			}
			if prior := f.revisionState(t, world.priorID); prior.Status != "ACTIVE" {
				t.Fatalf("%s: prior status after rollback = %s, want ACTIVE", crashStep, prior.Status)
			}
			if active, _ := f.ownerActivationState(t, project); active != world.priorID {
				t.Fatalf("%s: owner pointer after rollback = %q, want %q", crashStep, active, world.priorID)
			}
		})
	}

	t.Run("activation-races-failure-single-winner", func(t *testing.T) {
		const project = "pgact_cas_race_01"
		world := f.seedWorld(t, client, project, "race011", nil, nil)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var activationErr, failureErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			activationErr = world.activation.ActivateRepositoryGeneration(f.ctx, project, world.generation, world.candidateID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, failureErr = world.store.FailRevisionFrom(f.ctx, world.candidateID, explorer.RevisionReady,
				[]explorer.Diagnostic{{Severity: "ERROR", Code: "GENERATION_ACTIVATION_FAILED", Message: "racing failure", Retryable: false}})
		}()
		close(start)
		wg.Wait()

		if (activationErr == nil) == (failureErr == nil) {
			t.Fatalf("activation and failure must produce exactly one winner: activation=%s failure=%s",
				f.scrub(activationErr), f.scrub(failureErr))
		}
		candidate := f.revisionState(t, world.candidateID)
		active, _ := f.ownerActivationState(t, project)
		prior := f.revisionState(t, world.priorID)
		switch {
		case activationErr == nil:
			if candidate.Status != "ACTIVE" || active != world.candidateID {
				t.Fatalf("activation won but state disagrees: status=%s pointer=%q", candidate.Status, active)
			}
			if prior.Status != "SUPERSEDED" {
				t.Fatalf("activation won but prior status = %s, want SUPERSEDED", prior.Status)
			}
			if !errors.Is(failureErr, ErrStaleRevisionTransition) {
				t.Fatalf("losing failure error = %s, want ErrStaleRevisionTransition", f.scrub(failureErr))
			}
		default:
			if candidate.Status != "FAILED" || active != world.priorID {
				t.Fatalf("failure won but state disagrees: status=%s pointer=%q", candidate.Status, active)
			}
			if prior.Status != "ACTIVE" {
				t.Fatalf("failure won but prior status = %s, want ACTIVE", prior.Status)
			}
			if !errors.Is(activationErr, explorer.ErrDraftConflict) {
				t.Fatalf("losing activation error = %s, want explorer.ErrDraftConflict", f.scrub(activationErr))
			}
		}
	})
}
