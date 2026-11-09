package postgres

// Real-PostgreSQL acceptance tests for the pg-catalog-04 semantic-inventory
// build lifecycle, lease, and typed checkpoint state (check-2:
// go test ./internal/catalog/postgres -run SemanticInventory).
//
// Every test drives the real SemanticBuildRepository against the PostgreSQL
// 18 service named by LOOM_TEST_POSTGRES_URL. Fixtures are literal rows
// inserted by direct SQL; witnesses are literal rows read back by direct
// SQL. Nothing is mocked. Fixture databases and schemas are uniquely owned
// as pgcat04_it_* and dropped by the test that created them; the final
// TestZZ audit is ownership-scoped — it checks each name this file created
// by exact match, never a global scan, so sibling workers sweeping their
// own prefixes run concurrently without interference.
//
// Per the coordinator's migration conditions, this file also proves the
// 000006 live contract on real PG18: a double-apply idempotency probe with
// the ledger row pinned to its own sha256sum, and per-constraint negative
// tests that each named CHECK and identity constraint REJECTS a violating
// literal row (with a valid control row succeeding alongside, so a missing
// constraint cannot let a test pass vacuously).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/calypr/loom/internal/catalog"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgcat04NamePattern = regexp.MustCompile(`^pgcat04_it_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every resource whose creation this file verified is
// recorded here, and TestZZ asserts each exact name is gone. Ownership
// begins at verified CREATE, exactly as the per-test cleanup drops them.
var (
	semanticBuildOwnedMu     sync.Mutex
	semanticBuildOwnedByName = map[string]string{} // database -> owned schema
)

func semanticBuildRegister(db, schema string) {
	semanticBuildOwnedMu.Lock()
	defer semanticBuildOwnedMu.Unlock()
	semanticBuildOwnedByName[db] = schema
}

type semanticBuildService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func semanticBuildTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func semanticBuildRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func newSemanticBuildService(t *testing.T) *semanticBuildService {
	t.Helper()
	raw := semanticBuildTestPostgresURL(t)

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

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	var dbName, schema string

	dbName = fmt.Sprintf("pgcat04_it_%d_%s", time.Now().UnixNano(), semanticBuildRandomHex(t))
	if !pgcat04NamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	// Ownership begins here: this exact database is this test's to drop.
	semanticBuildRegister(dbName, "")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
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

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema = fmt.Sprintf("pgcat04_it_%d_%s", time.Now().UnixNano(), semanticBuildRandomHex(t))
	if !pgcat04NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	semanticBuildRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	service := &semanticBuildService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
	if _, err := storepostgres.MigrateSchema(ctx, service.openClient(t), schema); err != nil {
		fail("MigrateSchema", err)
	}
	return service
}

func (s *semanticBuildService) openClient(t *testing.T) *storepostgres.Client {
	t.Helper()
	client, err := storepostgres.Open(s.ctx, storepostgres.Config{
		DSN:            s.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (s *semanticBuildService) repository(t *testing.T) *SemanticBuildRepository {
	t.Helper()
	repo, err := NewSemanticBuildRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new semantic build repository", err)
	}
	return repo
}

func (s *semanticBuildService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *semanticBuildService) buildFQN() string {
	return pgx.Identifier{s.schema, "fhir_semantic_inventory_build"}.Sanitize()
}

// semanticBuildRow is the full literal row: every persisted column, read
// back by direct SQL. This is the witness every guard claim is checked
// against.
type semanticBuildRow struct {
	BuildKey             string
	Project              string
	DatasetGeneration    string
	BuildID              string
	State                string
	ObservationSchema    int
	InventorySchema      int
	EntryIndexVersion    int
	RuleVersion          int
	SourceKind           string
	SourceAvailability   string
	AuthResourcePath     string
	ScannedResources     int64
	Checkpoint           string
	CheckpointCollection string
	CheckpointKey        string
	LeaseToken           string
	LeaseExpiresAt       int64
	Diagnostics          []string
	UpdatedAt            time.Time
}

// sameSemanticRow compares two full witnesses for byte equality, including
// the ordered diagnostics array and the stored updated_at.
func sameSemanticRow(a, b semanticBuildRow) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func (s *semanticBuildService) witnessBuild(t *testing.T, project, generation, buildID string) (semanticBuildRow, bool) {
	t.Helper()
	var w semanticBuildRow
	err := s.iso.QueryRow(s.ctx, "SELECT build_key, project, dataset_generation, build_id, state,"+
		" observation_schema, inventory_schema, entry_index_version, rule_version, source_kind,"+
		" source_availability, auth_resource_path, scanned_resources, checkpoint, checkpoint_collection,"+
		" checkpoint_key, lease_token, lease_expires_at, diagnostics, updated_at"+
		" FROM "+s.buildFQN()+" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3",
		project, generation, buildID).Scan(
		&w.BuildKey, &w.Project, &w.DatasetGeneration, &w.BuildID, &w.State,
		&w.ObservationSchema, &w.InventorySchema, &w.EntryIndexVersion, &w.RuleVersion, &w.SourceKind,
		&w.SourceAvailability, &w.AuthResourcePath, &w.ScannedResources, &w.Checkpoint, &w.CheckpointCollection,
		&w.CheckpointKey, &w.LeaseToken, &w.LeaseExpiresAt, &w.Diagnostics, &w.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return semanticBuildRow{}, false
	}
	if err != nil {
		s.fail("witness semantic build row", err)
	}
	return w, true
}

func (s *semanticBuildService) requireNoRow(t *testing.T, project, generation, buildID, what string) {
	t.Helper()
	if row, found := s.witnessBuild(t, project, generation, buildID); found {
		t.Fatalf("%s: unexpected row %+v", what, row)
	}
}

// seedSemanticBuildLiteral inserts one fully literal row by direct SQL, so
// negative tests start from an exact stored state the repository did not
// write.
func (s *semanticBuildService) seedSemanticBuildLiteral(t *testing.T, w semanticBuildRow) {
	t.Helper()
	diagnostics := w.Diagnostics
	if diagnostics == nil {
		diagnostics = []string{}
	}
	s.exec(t, "INSERT INTO "+s.buildFQN()+
		" (build_key, project, dataset_generation, build_id, state, observation_schema,"+
		" inventory_schema, entry_index_version, rule_version, source_kind, source_availability,"+
		" auth_resource_path, scanned_resources, checkpoint, checkpoint_collection, checkpoint_key,"+
		" lease_token, lease_expires_at, diagnostics)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19::text[])",
		w.BuildKey, w.Project, w.DatasetGeneration, w.BuildID, w.State, w.ObservationSchema,
		w.InventorySchema, w.EntryIndexVersion, w.RuleVersion, w.SourceKind, w.SourceAvailability,
		w.AuthResourcePath, w.ScannedResources, w.Checkpoint, w.CheckpointCollection, w.CheckpointKey,
		w.LeaseToken, w.LeaseExpiresAt, diagnostics)
}

// trySeedSemanticBuild runs the same literal INSERT as
// seedSemanticBuildLiteral but returns the error instead of failing, so
// constraint negative tests can name the exact rejection.
func (s *semanticBuildService) trySeedSemanticBuild(t *testing.T, w semanticBuildRow) error {
	t.Helper()
	diagnostics := w.Diagnostics
	if diagnostics == nil {
		diagnostics = []string{}
	}
	_, err := s.iso.Exec(s.ctx, "INSERT INTO "+s.buildFQN()+
		" (build_key, project, dataset_generation, build_id, state, observation_schema,"+
		" inventory_schema, entry_index_version, rule_version, source_kind, source_availability,"+
		" auth_resource_path, scanned_resources, checkpoint, checkpoint_collection, checkpoint_key,"+
		" lease_token, lease_expires_at, diagnostics)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19::text[])",
		w.BuildKey, w.Project, w.DatasetGeneration, w.BuildID, w.State, w.ObservationSchema,
		w.InventorySchema, w.EntryIndexVersion, w.RuleVersion, w.SourceKind, w.SourceAvailability,
		w.AuthResourcePath, w.ScannedResources, w.Checkpoint, w.CheckpointCollection, w.CheckpointKey,
		w.LeaseToken, w.LeaseExpiresAt, diagnostics)
	return err
}

// semanticBuildFixture is a valid retained-vertex claim build for one scope.
func semanticBuildFixture(project, generation, authPath string) catalog.SemanticInventoryBuild {
	build := catalog.NewSemanticInventoryBuild(project, generation, authPath)
	build.SourceKind = catalog.SemanticInventorySourceRetained
	return build
}

// literalBuildRowFor builds a fully literal stored row for the identity of
// (project, generation), with every column set to a distinctive non-default
// value. It is both a seeding template and the expected witness shape.
func literalBuildRowFor(project, generation, state string) semanticBuildRow {
	return semanticBuildRow{
		BuildKey:             catalog.SemanticInventoryBuildKey(project, generation),
		Project:              project,
		DatasetGeneration:    catalog.NormalizeDatasetGeneration(generation),
		BuildID:              catalog.SemanticInventoryBuildID(project, generation),
		State:                state,
		ObservationSchema:    catalog.SemanticObservationLegacyV1SchemaVersion,
		InventorySchema:      catalog.SemanticInventorySchemaVersion,
		EntryIndexVersion:    catalog.SemanticInventoryEntryIndexVersion,
		RuleVersion:          catalog.SemanticObservationLegacyV1RuleVersion,
		SourceKind:           catalog.SemanticInventorySourceRetained,
		SourceAvailability:   catalog.SemanticInventorySourceAvailabilityUnproven,
		AuthResourcePath:     "fhir/Composition?type=hub",
		ScannedResources:     42,
		Checkpoint:           "legacy-string-checkpoint",
		CheckpointCollection: "Observation",
		CheckpointKey:        "obs-0009",
		LeaseToken:           "",
		LeaseExpiresAt:       0,
		Diagnostics:          []string{"first diagnostic", "second diagnostic"},
	}
}

// compareDomainBuildToRow checks that a repository-read build agrees with
// the stored literal row on every field the struct carries.
func compareDomainBuildToRow(t *testing.T, got catalog.SemanticInventoryBuild, row semanticBuildRow) {
	t.Helper()
	want := catalog.SemanticInventoryBuild{
		Key:                row.BuildKey,
		Project:            row.Project,
		DatasetGeneration:  row.DatasetGeneration,
		BuildID:            row.BuildID,
		State:              catalog.SemanticInventoryState(row.State),
		ObservationSchema:  row.ObservationSchema,
		InventorySchema:    row.InventorySchema,
		EntryIndexVersion:  row.EntryIndexVersion,
		RuleVersion:        row.RuleVersion,
		SourceKind:         row.SourceKind,
		SourceAvailability: row.SourceAvailability,
		AuthResourcePath:   row.AuthResourcePath,
		ScannedResources:   row.ScannedResources,
		Checkpoint:         row.Checkpoint,
		SourceCheckpoint:   catalog.SemanticInventoryCheckpoint{Collection: row.CheckpointCollection, Key: row.CheckpointKey},
		LeaseToken:         row.LeaseToken,
		LeaseExpiresAt:     row.LeaseExpiresAt,
		Diagnostics:        row.Diagnostics,
	}
	if got.Key != want.Key || got.Project != want.Project || got.DatasetGeneration != want.DatasetGeneration ||
		got.BuildID != want.BuildID || got.State != want.State ||
		got.ObservationSchema != want.ObservationSchema || got.InventorySchema != want.InventorySchema ||
		got.EntryIndexVersion != want.EntryIndexVersion || got.RuleVersion != want.RuleVersion ||
		got.SourceKind != want.SourceKind || got.SourceAvailability != want.SourceAvailability ||
		got.AuthResourcePath != want.AuthResourcePath || got.ScannedResources != want.ScannedResources ||
		got.Checkpoint != want.Checkpoint || got.SourceCheckpoint != want.SourceCheckpoint ||
		got.LeaseToken != want.LeaseToken || got.LeaseExpiresAt != want.LeaseExpiresAt ||
		strings.Join(got.Diagnostics, "|") != strings.Join(want.Diagnostics, "|") {
		t.Fatalf("repository read = %#v, want stored row %#v", got, want)
	}
}

func TestSemanticInventoryBuildClaimRoundTripsEveryField(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "g", "fhir/Composition?type=hub")

	before := time.Now().UnixMilli()
	claimedBuild, ok, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-owner", 30*time.Minute)
	after := time.Now().UnixMilli()
	if err != nil || !ok {
		t.Fatalf("claim = ok %v err %v, want claimed", ok, err)
	}
	if claimedBuild.State != catalog.SemanticInventoryRunning || claimedBuild.LeaseToken != "tok-owner" {
		t.Fatalf("claim returned %#v, want the running claimed build", claimedBuild)
	}

	// The stored literal row: running, unknown availability, zeroed
	// checkpoints and scan totals, cleared diagnostics, live fenced lease.
	row, found := s.witnessBuild(t, "p", "g", build.BuildID)
	if !found {
		t.Fatal("claimed build was not stored")
	}
	if row.BuildKey != build.Key || row.Project != "p" || row.DatasetGeneration != "g" || row.BuildID != build.BuildID {
		t.Fatalf("identity columns = %+v", row)
	}
	if !strings.HasPrefix(row.BuildKey, "sib_") || len(row.BuildKey) != len("sib_")+64 {
		t.Fatalf("build key %q is not a sib_ identity", row.BuildKey)
	}
	if row.State != "running" || row.SourceKind != "retained_vertex" || row.SourceAvailability != "unknown" {
		t.Fatalf("lifecycle columns = state %q kind %q availability %q", row.State, row.SourceKind, row.SourceAvailability)
	}
	if row.ObservationSchema != catalog.SemanticObservationSchemaVersion ||
		row.InventorySchema != catalog.SemanticInventorySchemaVersion ||
		row.RuleVersion != catalog.SemanticObservationRuleVersion || row.EntryIndexVersion != 0 {
		t.Fatalf("version columns = %+v", row)
	}
	if row.AuthResourcePath != "fhir/Composition?type=hub" || row.ScannedResources != 0 || row.Checkpoint != "" ||
		row.CheckpointCollection != "" || row.CheckpointKey != "" || len(row.Diagnostics) != 0 {
		t.Fatalf("progress columns = %+v", row)
	}
	if row.LeaseToken != "tok-owner" || row.LeaseExpiresAt < before+30*60*1000 || row.LeaseExpiresAt > after+30*60*1000 {
		t.Fatalf("lease columns = token %q expiry %d, want tok-owner in [%d,%d]",
			row.LeaseToken, row.LeaseExpiresAt, before+30*60*1000, after+30*60*1000)
	}

	// The repository read round-trips every field against the stored row.
	got, found, err := repo.ReadSemanticInventoryBuild(s.ctx, "p", "g", build.BuildID)
	if err != nil || !found {
		t.Fatalf("read = found %v err %v", found, err)
	}
	compareDomainBuildToRow(t, got, row)
	if got.LeaseToken != "tok-owner" {
		t.Fatalf("read lost the lease token: %#v", got)
	}

	// A fully literal stored row (written by direct SQL, never by the
	// repository) round-trips through the read untouched, including the
	// legacy string checkpoint and ordered diagnostics.
	literal := literalBuildRowFor("p", "g2", "failed")
	literal.Diagnostics = []string{"first diagnostic", "second diagnostic"}
	s.seedSemanticBuildLiteral(t, literal)
	got, found, err = repo.ReadSemanticInventoryBuild(s.ctx, "p", "g2", literal.BuildID)
	if err != nil || !found {
		t.Fatalf("literal read = found %v err %v", found, err)
	}
	compareDomainBuildToRow(t, got, literal)
	stored, found := s.witnessBuild(t, "p", "g2", literal.BuildID)
	if !found || stored.Checkpoint != "legacy-string-checkpoint" ||
		strings.Join(stored.Diagnostics, "|") != "first diagnostic|second diagnostic" {
		t.Fatalf("literal witness = %+v", stored)
	}
}

func TestSemanticInventoryBuildAllSixStatesRoundTrip(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	for i, state := range []string{"unknown", "not_started", "running", "complete", "failed", "invalidated"} {
		generation := fmt.Sprintf("state%d", i)
		literal := literalBuildRowFor("p", generation, state)
		if state == "running" {
			literal.LeaseToken = "tok-live"
			literal.LeaseExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		}
		s.seedSemanticBuildLiteral(t, literal)
		got, found, err := repo.ReadSemanticInventoryBuild(s.ctx, "p", generation, literal.BuildID)
		if err != nil || !found {
			t.Fatalf("state %q: read found %v err %v", state, found, err)
		}
		if got.State != catalog.SemanticInventoryState(state) {
			t.Fatalf("state %q read back as %q", state, got.State)
		}
		compareDomainBuildToRow(t, got, literal)
	}
}

func TestSemanticInventoryBuildSingleClaimantAndStaleTokenCannotAdvanceOrComplete(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "g", "")

	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("first claim = claimed %v err %v", claimed, err)
	}

	// A second claimant cannot steal a live lease: typed claimed error, and
	// the stored row is byte-for-byte untouched.
	untouched, _ := s.witnessBuild(t, "p", "g", build.BuildID)
	_, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-b", 30*time.Minute)
	if claimed || err == nil || !errors.Is(err, ErrSemanticInventoryClaimed) {
		t.Fatalf("second claim = claimed %v err %v, want typed claimed error", claimed, err)
	}
	var claimedErr *SemanticInventoryClaimedError
	if !errors.As(err, &claimedErr) || claimedErr.State != catalog.SemanticInventoryRunning {
		t.Fatalf("claimed error is not typed with its state: %v", err)
	}
	after, _ := s.witnessBuild(t, "p", "g", build.BuildID)
	if !sameSemanticRow(after, untouched) {
		t.Fatalf("rejected claim changed the row:\n before %+v\n after  %+v", untouched, after)
	}

	// A stale token cannot advance, complete, or fail: typed claim-lost
	// naming the token guard, with the row unchanged each time.
	staleCheckpoint := catalog.SemanticInventoryCheckpoint{}
	next := catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-1"}
	_, staleTokenErr := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-b", staleCheckpoint, next, 7, time.Minute)
	if !errors.Is(staleTokenErr, ErrSemanticInventoryClaimLost) {
		t.Fatalf("stale-token advance err = %v, want claim lost", staleTokenErr)
	}
	var lost *SemanticInventoryClaimLostError
	if !errors.As(staleTokenErr, &lost) || !strings.Contains(lost.Reason, "token") {
		t.Fatalf("stale-token advance reason = %v, want the token guard", staleTokenErr)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); !sameSemanticRow(row, untouched) {
		t.Fatalf("stale-token advance changed the row to %+v", row)
	}
	if _, err := repo.CompleteSemanticInventoryBackfill(s.ctx, build, "tok-b", staleCheckpoint, 7); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("stale-token complete err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); !sameSemanticRow(row, untouched) {
		t.Fatalf("stale-token complete changed the row to %+v", row)
	}
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-b", "intruder"); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("stale-token fail err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); !sameSemanticRow(row, untouched) {
		t.Fatalf("stale-token fail changed the row to %+v", row)
	}

	// The real token holder advances under the CAS, renews the lease, and
	// completes exactly.
	advanceBefore := time.Now().UnixMilli()
	updated, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", staleCheckpoint, next, 7, 60*time.Minute)
	advanceAfter := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("owner advance: %v", err)
	}
	row, _ := s.witnessBuild(t, "p", "g", build.BuildID)
	if row.CheckpointCollection != "Patient" || row.CheckpointKey != "p-1" || row.ScannedResources != 7 {
		t.Fatalf("advance did not persist the checkpoint: %+v", row)
	}
	if row.LeaseToken != "tok-a" || row.LeaseExpiresAt < advanceBefore+60*60*1000 || row.LeaseExpiresAt > advanceAfter+60*60*1000 {
		t.Fatalf("advance did not renew the lease to now+60m: expiry %d, want [%d,%d]",
			row.LeaseExpiresAt, advanceBefore+60*60*1000, advanceAfter+60*60*1000)
	}
	if updated.SourceCheckpoint != next || updated.ScannedResources != 7 || updated.LeaseToken != "tok-a" {
		t.Fatalf("advance returned %#v", updated)
	}
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	completed, err := repo.CompleteSemanticInventoryBackfill(s.ctx, build, "tok-a", next, 7)
	if err != nil {
		t.Fatalf("owner complete: %v", err)
	}
	row, _ = s.witnessBuild(t, "p", "g", build.BuildID)
	if row.State != "complete" || row.LeaseToken != "" || row.LeaseExpiresAt != 0 || len(row.Diagnostics) != 0 ||
		row.SourceAvailability != "verified" || row.ScannedResources != 7 ||
		row.CheckpointCollection != "Patient" || row.CheckpointKey != "p-1" {
		t.Fatalf("complete transition stored %+v", row)
	}
	if completed.State != catalog.SemanticInventoryComplete || completed.LeaseToken != "" {
		t.Fatalf("complete returned %#v", completed)
	}

	// Re-claiming a completed build returns it unclaimed and never rewrites
	// it — file and retained contribution sets cannot be mixed.
	returned, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", ""), "tok-c", 30*time.Minute)
	if err != nil || claimed {
		t.Fatalf("claim on complete = claimed %v err %v, want unclaimed success", claimed, err)
	}
	if returned.State != catalog.SemanticInventoryComplete || returned.BuildID != build.BuildID {
		t.Fatalf("claim on complete returned %#v", returned)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "complete" || row.LeaseToken != "" {
		t.Fatalf("claim on complete rewrote the marker row: %+v", row)
	}

	// Exactly one of eight concurrent claimants wins a fresh scope.
	other := semanticBuildFixture("p", "race", "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners, tokens := 0, make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := fmt.Sprintf("tok-race-%d", i)
			_, claimedNow, err := repo.ClaimSemanticInventoryBackfill(context.Background(), other, token, time.Hour)
			if err != nil && !errors.Is(err, ErrSemanticInventoryClaimed) {
				mu.Lock()
				t.Errorf("racer %d: %v", i, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if claimedNow {
				winners++
				tokens = append(tokens, token)
			}
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("concurrent claims = %d winners, want exactly 1", winners)
	}
	if row, _ := s.witnessBuild(t, "p", "race", other.BuildID); row.LeaseToken != tokens[0] {
		t.Fatalf("stored lease %q is not the winner %q", row.LeaseToken, tokens[0])
	}
}

func TestSemanticInventoryBuildCheckpointCASRejectsStaleExpected(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "g", "")
	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("claim = claimed %v err %v", claimed, err)
	}
	zero := catalog.SemanticInventoryCheckpoint{}
	first := catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-1"}
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", zero, first, 3, 5*time.Minute); err != nil {
		t.Fatalf("first advance: %v", err)
	}
	stored, _ := s.witnessBuild(t, "p", "g", build.BuildID)

	// Replaying the first CAS with its now-stale expectation is rejected by
	// name, and nothing about the row moves — not the checkpoint, not the
	// scan total, not the lease.
	_, staleErr := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", zero,
		catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-2"}, 99, 5*time.Minute)
	if !errors.Is(staleErr, ErrSemanticInventoryClaimLost) {
		t.Fatalf("stale-expected advance err = %v, want claim lost", staleErr)
	}
	var lost *SemanticInventoryClaimLostError
	if !errors.As(staleErr, &lost) || !strings.Contains(lost.Reason, "checkpoint is Patient/p-1, expected /") {
		t.Fatalf("stale-expected reason = %v, want the checkpoint guard naming the stored pair", staleErr)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); !sameSemanticRow(row, stored) {
		t.Fatalf("rejected CAS changed the row:\n before %+v\n after  %+v", stored, row)
	}

	// A wrong key under the right collection is likewise rejected untouched.
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a",
		catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-99"},
		catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-2"}, 99, 5*time.Minute); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("wrong-key expected advance err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); !sameSemanticRow(row, stored) {
		t.Fatalf("rejected wrong-key CAS changed the row to %+v", row)
	}

	// The live checkpoint advances the build exactly once; scanned counts
	// are clamped at zero like the Arango adapter.
	second := catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-2"}
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", first, second, -5, 5*time.Minute); err != nil {
		t.Fatalf("second advance: %v", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.CheckpointKey != "p-2" || row.ScannedResources != 0 {
		t.Fatalf("second advance stored %+v", row)
	}

	// Completion shares the same CAS: a stale expectation cannot complete
	// the build.
	if _, err := repo.CompleteSemanticInventoryBackfill(s.ctx, build, "tok-a", first, 3); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("stale-expected complete err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "running" {
		t.Fatalf("stale-expected complete changed state to %q", row.State)
	}
}

func TestSemanticInventoryBuildFailureAndCompletionDiagnosticSemantics(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "g", "fhir/Patient")
	zero := catalog.SemanticInventoryCheckpoint{}
	first := catalog.SemanticInventoryCheckpoint{Collection: "Observation", Key: "obs-1"}

	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("claim = claimed %v err %v", claimed, err)
	}
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", zero, first, 11, 30*time.Minute); err != nil {
		t.Fatalf("advance: %v", err)
	}

	// Failure releases the lease, records exactly one diagnostic, and keeps
	// scanned progress, both checkpoints, the auth path, and the source
	// availability meaning intact.
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-a", "page read failed: timeout"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	row, _ := s.witnessBuild(t, "p", "g", build.BuildID)
	if row.State != "failed" || row.LeaseToken != "" || row.LeaseExpiresAt != 0 {
		t.Fatalf("fail did not release the lease: %+v", row)
	}
	if len(row.Diagnostics) != 1 || row.Diagnostics[0] != "page read failed: timeout" {
		t.Fatalf("fail diagnostics = %v, want exactly the one failure message", row.Diagnostics)
	}
	if row.ScannedResources != 11 || row.CheckpointCollection != "Observation" || row.CheckpointKey != "obs-1" ||
		row.AuthResourcePath != "fhir/Patient" || row.SourceAvailability != "unproven" {
		t.Fatalf("fail lost preserved fields: %+v", row)
	}
	got, found, err := repo.ReadSemanticInventoryBuild(s.ctx, "p", "g", build.BuildID)
	if err != nil || !found || len(got.Diagnostics) != 1 || got.Diagnostics[0] != "page read failed: timeout" {
		t.Fatalf("fail read-back = %#v found %v err %v", got, found, err)
	}

	// A blank diagnostic becomes the default; a 600-byte diagnostic is
	// capped at 512 bytes, matching the Arango adapter. A failed build is
	// resumable: re-claiming it starts running again while preserving the
	// typed checkpoint and scan total.
	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", "fhir/Patient"), "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("re-claim after failure = claimed %v err %v (a failed build must be resumable)", claimed, err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "running" || row.ScannedResources != 11 ||
		row.CheckpointCollection != "Observation" || row.CheckpointKey != "obs-1" {
		t.Fatalf("re-claim after failure lost preserved progress: %+v", row)
	}
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-a", "   "); err != nil {
		t.Fatalf("blank fail: %v", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); len(row.Diagnostics) != 1 || row.Diagnostics[0] != "backfill failed" {
		t.Fatalf("blank diagnostic stored %v, want the default", row.Diagnostics)
	}

	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", "fhir/Patient"), "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("re-claim 2 = claimed %v err %v", claimed, err)
	}
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-a", strings.Repeat("x", 600)); err != nil {
		t.Fatalf("long fail: %v", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); len(row.Diagnostics) != 1 || len(row.Diagnostics[0]) != 512 {
		t.Fatalf("long diagnostic not capped at 512 bytes: %d", len(row.Diagnostics[0]))
	}

	// A failed build's lease is released, so only a fresh claim — never the
	// dead token — can move it again; failing twice is a guard miss.
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-a", "again"); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("double fail err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "failed" || row.Diagnostics[0] != strings.Repeat("x", 512) {
		t.Fatalf("double fail changed the row: %+v", row)
	}

	// Completion clears diagnostics — a complete build carries no failure
	// evidence — and records the caller's source availability exactly. The
	// re-claimed failed build still stores the typed checkpoint, so the CAS
	// completes directly under it.
	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", "fhir/Patient"), "tok-b", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("re-claim after failure = claimed %v err %v", claimed, err)
	}
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnproven
	if _, err := repo.CompleteSemanticInventoryBackfill(s.ctx, build, "tok-b", first, 11); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "complete" || len(row.Diagnostics) != 0 ||
		row.SourceAvailability != "unproven" || row.ScannedResources != 11 {
		t.Fatalf("complete lost diagnostic/availability meaning: %+v", row)
	}
	// A completed build cannot be failed.
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-b", "too late"); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("fail after complete err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "complete" || len(row.Diagnostics) != 0 {
		t.Fatalf("fail after complete changed the row: %+v", row)
	}
}

func TestSemanticInventoryBuildLeaseExpiryBlocksAdvanceButNotFailure(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "g", "")
	zero := catalog.SemanticInventoryCheckpoint{}
	first := catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-1"}

	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("claim = claimed %v err %v", claimed, err)
	}
	// Age the lease by direct SQL: the token holder has lost the fence.
	s.exec(t, "UPDATE "+s.buildFQN()+" SET lease_expires_at = $4 WHERE project = $1 AND dataset_generation = $2 AND build_id = $3",
		"p", "g", build.BuildID, time.Now().Add(-time.Minute).UnixMilli())
	stored, _ := s.witnessBuild(t, "p", "g", build.BuildID)

	_, expiredErr := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", zero, first, 1, time.Minute)
	if !errors.Is(expiredErr, ErrSemanticInventoryClaimLost) {
		t.Fatalf("expired advance err = %v, want claim lost", expiredErr)
	}
	if !strings.Contains(expiredErr.Error(), "lease expired") {
		t.Fatalf("expired advance reason = %v, want the expiry guard", expiredErr)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); !sameSemanticRow(row, stored) {
		t.Fatalf("expired-lease advance changed the row to %+v", row)
	}

	// A new claimant takes over the stale lease and resumes progress; the
	// dead token can no longer fail the build.
	reclaimed, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", ""), "tok-b", 30*time.Minute)
	if err != nil || !claimed {
		t.Fatalf("stale-lease takeover = claimed %v err %v", claimed, err)
	}
	if reclaimed.LeaseToken != "tok-b" {
		t.Fatalf("takeover returned lease %#v", reclaimed)
	}
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-a", "zombie cleanup"); !errors.Is(err, ErrSemanticInventoryClaimLost) {
		t.Fatalf("dead-token fail err = %v, want claim lost", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "running" || row.LeaseToken != "tok-b" {
		t.Fatalf("dead-token fail changed the row: %+v", row)
	}

	// The token holder can always land cleanup, even after its own lease
	// lapsed: the fail path carries no expiry guard, exactly like Arango.
	s.exec(t, "UPDATE "+s.buildFQN()+" SET lease_expires_at = $4 WHERE project = $1 AND dataset_generation = $2 AND build_id = $3",
		"p", "g", build.BuildID, time.Now().Add(-time.Minute).UnixMilli())
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "tok-b", "cleanup after expiry"); err != nil {
		t.Fatalf("expired-holder fail: %v", err)
	}
	if row, _ := s.witnessBuild(t, "p", "g", build.BuildID); row.State != "failed" || row.LeaseToken != "" ||
		len(row.Diagnostics) != 1 || row.Diagnostics[0] != "cleanup after expiry" {
		t.Fatalf("expired-holder fail stored %+v", row)
	}
}

func TestSemanticInventoryBuildReclaimResumesProgressAndClearsVolatileFields(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "g", "fhir/Composition")
	zero := catalog.SemanticInventoryCheckpoint{}
	first := catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p-1"}

	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-a", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("claim = claimed %v err %v", claimed, err)
	}
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", zero, first, 3, 30*time.Minute); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// Seed volatile state a re-claim must clear: the legacy string
	// checkpoint and a stray diagnostic (only reachable by direct SQL).
	s.exec(t, "UPDATE "+s.buildFQN()+" SET checkpoint = $4, diagnostics = ARRAY['stale']::text[]"+
		" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3",
		"p", "g", build.BuildID, "volatile-string-checkpoint")
	if seeded, _ := s.witnessBuild(t, "p", "g", build.BuildID); seeded.Checkpoint != "volatile-string-checkpoint" ||
		len(seeded.Diagnostics) != 1 || seeded.Diagnostics[0] != "stale" {
		t.Fatalf("volatile seed did not land: %+v", seeded)
	}

	// Same-token resume keeps scanned progress, the typed checkpoint, and
	// the stored auth path, and clears volatile fields.
	resumed, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", "OTHER/Path"), "tok-a", 30*time.Minute)
	if err != nil || !claimed {
		t.Fatalf("resume = claimed %v err %v", claimed, err)
	}
	row, _ := s.witnessBuild(t, "p", "g", build.BuildID)
	if row.State != "running" || row.ScannedResources != 3 || row.CheckpointCollection != "Patient" || row.CheckpointKey != "p-1" {
		t.Fatalf("resume lost progress: %+v", row)
	}
	if row.Checkpoint != "" || len(row.Diagnostics) != 0 || row.SourceAvailability != "unknown" {
		t.Fatalf("resume kept volatile state: %+v", row)
	}
	if row.AuthResourcePath != "fhir/Composition" {
		t.Fatalf("resume overwrote the stored auth path: %+v", row)
	}
	if resumed.ScannedResources != 3 || resumed.SourceCheckpoint != first {
		t.Fatalf("resume returned %#v", resumed)
	}

	// A stored file-kind row (direct SQL — the repository never writes one)
	// is reset by a retained claim: progress and availability restart, so
	// the two contribution sets are never mixed.
	mixed := literalBuildRowFor("p", "g-mixed", "running")
	mixed.SourceKind = catalog.SemanticInventorySourceFile
	mixed.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	mixed.LeaseToken = "tok-old"
	mixed.LeaseExpiresAt = time.Now().Add(-time.Minute).UnixMilli()
	s.seedSemanticBuildLiteral(t, mixed)
	if _, claimed, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g-mixed", ""), "tok-new", 30*time.Minute); err != nil || !claimed {
		t.Fatalf("kind-switch claim = claimed %v err %v", claimed, err)
	}
	row, _ = s.witnessBuild(t, "p", "g-mixed", mixed.BuildID)
	if row.SourceKind != "retained_vertex" || row.ScannedResources != 0 || row.CheckpointCollection != "" ||
		row.CheckpointKey != "" || row.SourceAvailability != "unknown" || row.LeaseToken != "tok-new" {
		t.Fatalf("kind switch did not restart progress: %+v", row)
	}
}

func TestSemanticInventoryBuildUnsupportedRequestsAreTypedRefusals(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)

	// A file-sourced claim is refused by the retained-backfill entry point;
	// nothing is stored — no silent fallback to a file build path.
	fileBuild := catalog.NewSemanticInventoryBuild("p", "file-kind", "")
	_, fileClaimed, fileErr := repo.ClaimSemanticInventoryBackfill(s.ctx, fileBuild, "tok-a", time.Minute)
	if !errors.Is(fileErr, ErrUnsupportedSemanticInventoryBuild) || fileClaimed {
		t.Fatalf("file-kind claim = claimed %v err %v, want typed refusal", fileClaimed, fileErr)
	}
	var unsupported *SemanticInventoryUnsupportedError
	if !errors.As(fileErr, &unsupported) || unsupported.Case != "source_kind" {
		t.Fatalf("file-kind refusal is not typed by case: %v", fileErr)
	}
	s.requireNoRow(t, "p", "file-kind", fileBuild.BuildID, "file-kind claim stored a row")

	// Blank token and non-positive lease are refused before any statement.
	if _, _, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g", ""), "  ", time.Minute); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("blank-token claim err = %v, want typed refusal", err)
	}
	if _, _, err := repo.ClaimSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "g2", ""), "tok-a", 0); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("zero-lease claim err = %v, want typed refusal", err)
	}
	s.requireNoRow(t, "p", "g", catalog.SemanticInventoryBuildID("p", "g"), "blank-token claim stored a row")
	s.requireNoRow(t, "p", "g2", catalog.SemanticInventoryBuildID("p", "g2"), "zero-lease claim stored a row")

	// A build whose identity violates the shared contract is refused by the
	// dedicated invalid-identity sentinel.
	broken := semanticBuildFixture("p", "g3", "")
	broken.BuildID = strings.Repeat("0", 64)
	if _, _, err := repo.ClaimSemanticInventoryBackfill(s.ctx, broken, "tok-a", time.Minute); !errors.Is(err, ErrInvalidSemanticInventoryBuild) {
		t.Fatalf("broken identity err = %v, want invalid-build sentinel", err)
	}

	// Malformed typed checkpoints, unknown availability, and blank read
	// scope are refused before touching the table.
	build := semanticBuildFixture("p", "g4", "")
	half := catalog.SemanticInventoryCheckpoint{Collection: "Patient"}
	if _, _, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-a", time.Minute); err != nil {
		t.Fatalf("setup claim: %v", err)
	}
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a", half, half, 1, time.Minute); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("half checkpoint advance err = %v, want typed refusal", err)
	}
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a",
		catalog.SemanticInventoryCheckpoint{}, catalog.SemanticInventoryCheckpoint{}, 1, time.Minute); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("zero next advance err = %v, want typed refusal", err)
	}
	build.SourceAvailability = "sort-of-verified"
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a",
		catalog.SemanticInventoryCheckpoint{}, catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p"}, 1, time.Minute); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("unknown availability advance err = %v, want typed refusal", err)
	}
	// A lease renewal of zero or less would store an already-expired lease:
	// refused before the statement; a blank token likewise.
	if _, err := repo.AdvanceSemanticInventoryBackfill(s.ctx, build, "tok-a",
		catalog.SemanticInventoryCheckpoint{}, catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p"}, 1, 0); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("zero-lease advance err = %v, want typed refusal", err)
	}
	if err := repo.FailSemanticInventoryBackfill(s.ctx, build, "", "no token"); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("blank-token fail err = %v, want typed refusal", err)
	}
	stored, _ := s.witnessBuild(t, "p", "g4", build.BuildID)
	if stored.ScannedResources != 0 || stored.CheckpointCollection != "" || stored.SourceAvailability != "unknown" {
		t.Fatalf("refused advances changed the row: %+v", stored)
	}
	if _, _, err := repo.ReadSemanticInventoryBuild(s.ctx, "", "g", "b"); !errors.Is(err, ErrUnsupportedSemanticInventoryBuild) {
		t.Fatalf("blank-scope read err = %v, want typed refusal", err)
	}

	// Transitioning an identity that was never claimed is a claim-lost
	// naming the missing row, not a fallback write.
	_, missingErr := repo.AdvanceSemanticInventoryBackfill(s.ctx, semanticBuildFixture("p", "never", ""), "tok-a",
		catalog.SemanticInventoryCheckpoint{}, catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "p"}, 1, time.Minute)
	if !errors.Is(missingErr, ErrSemanticInventoryClaimLost) {
		t.Fatalf("missing-row advance err = %v, want claim lost", missingErr)
	}
	var lost *SemanticInventoryClaimLostError
	if !errors.As(missingErr, &lost) || lost.Reason != "row is missing" {
		t.Fatalf("missing-row reason = %v", missingErr)
	}
	s.requireNoRow(t, "p", "never", catalog.SemanticInventoryBuildID("p", "never"), "advance created a row")
}

// TestSemanticInventoryBuildFailureTruncatesDiagnosticOnRuneBoundary pins
// review finding 1: a >512-byte failure diagnostic whose 512-byte cut lands
// inside a multi-byte rune must still record the failure. PostgreSQL
// rejects a split rune with SQLSTATE 22021, so a naive byte cut loses the
// failure entirely and leaves the build running with its lease live.
func TestSemanticInventoryBuildFailureTruncatesDiagnosticOnRuneBoundary(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	build := semanticBuildFixture("p", "utf8-truncation", "")
	claimed, ok, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-owner", 30*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = claimed %v err %v", ok, err)
	}
	// 511 ASCII bytes, then euro signs: byte 512 is the lead byte of the
	// first three-byte rune, so the raw cut ends mid-rune.
	diagnostic := strings.Repeat("a", 511) + strings.Repeat("€", 30)
	if err := repo.FailSemanticInventoryBackfill(s.ctx, claimed, "tok-owner", diagnostic); err != nil {
		s.fail("FailSemanticInventoryBackfill with multi-byte diagnostic", err)
	}
	row, found := s.witnessBuild(t, "p", "utf8-truncation", claimed.BuildID)
	if !found {
		t.Fatal("multi-byte diagnostic failure did not record the build")
	}
	if row.State != "failed" || row.LeaseToken != "" || row.LeaseExpiresAt != 0 {
		t.Fatalf("split-rune diagnostic lost the failure: %+v", row)
	}
	if len(row.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want exactly one", row.Diagnostics)
	}
	stored := row.Diagnostics[0]
	if !utf8.ValidString(stored) {
		t.Fatalf("stored diagnostic is not valid UTF-8: %d bytes", len(stored))
	}
	if len(stored) > 512 || stored != strings.Repeat("a", 511) {
		t.Fatalf("stored diagnostic = %q (%d bytes), want the complete-rune 511-byte prefix", stored, len(stored))
	}
}

// TestSemanticInventoryBuildPaddedGenerationCannotForkIdentity pins review
// finding 2: a whitespace-padded generation must not fork the primary key
// against the clean spelling. The domain derives one build_id/build_key for
// both spellings, and internal/catalog documents NormalizeDatasetGeneration
// as the convention every catalog boundary applies; the Arango adapter
// converges both spellings onto one _key, so the PostgreSQL repository must
// converge them onto one row too.
func TestSemanticInventoryBuildPaddedGenerationCannotForkIdentity(t *testing.T) {
	s := newSemanticBuildService(t)
	repo := s.repository(t)
	const padded = " padded generation "
	const clean = "padded generation"
	// rawBuild carries the raw generation spelling, as a rehydrating caller
	// does: the constructor normalizes, but the repository must not rely on
	// every caller using it.
	rawBuild := func(generation string) catalog.SemanticInventoryBuild {
		b := catalog.NewSemanticInventoryBuild("p", generation, "")
		b.SourceKind = catalog.SemanticInventorySourceRetained
		b.DatasetGeneration = generation
		return b
	}

	build := rawBuild(padded)
	claimed, ok, err := repo.ClaimSemanticInventoryBackfill(s.ctx, build, "tok-owner", 30*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = claimed %v err %v", ok, err)
	}

	// The clean spelling is the SAME identity, not a new row: it must meet
	// the live lease, never a corrupted-derivation refusal.
	cleanBuild := rawBuild(clean)
	if _, secondOk, secondErr := repo.ClaimSemanticInventoryBackfill(s.ctx, cleanBuild, "tok-other", 30*time.Minute); secondOk || !errors.Is(secondErr, ErrSemanticInventoryClaimed) {
		t.Fatalf("clean-spelling claim = claimed %v err %v, want ErrSemanticInventoryClaimed", secondOk, secondErr)
	}

	// Every follow-up under EITHER spelling acts on the one row.
	if _, found, readErr := repo.ReadSemanticInventoryBuild(s.ctx, "p", clean, claimed.BuildID); readErr != nil || !found {
		t.Fatalf("clean-spelling read = found %v err %v", found, readErr)
	}
	zero := catalog.SemanticInventoryCheckpoint{}
	first := catalog.SemanticInventoryCheckpoint{Collection: "Observation", Key: "obs-1"}
	if _, advErr := repo.AdvanceSemanticInventoryBackfill(s.ctx, cleanBuild, "tok-owner", zero, first, 3, 30*time.Minute); advErr != nil {
		t.Fatalf("clean-spelling advance: %v", advErr)
	}
	if _, compErr := repo.CompleteSemanticInventoryBackfill(s.ctx, build, "tok-owner", first, 3); compErr != nil {
		t.Fatalf("padded-spelling complete: %v", compErr)
	}

	// Exactly one row exists across both spellings, stored normalized.
	var rowCount int
	var storedGeneration string
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*), COALESCE(MAX(dataset_generation), '') FROM "+s.buildFQN()+
		" WHERE project = $1 AND dataset_generation IN ($2, $3)", "p", padded, clean).
		Scan(&rowCount, &storedGeneration); err != nil {
		s.fail("count padded-generation rows", err)
	}
	if rowCount != 1 || storedGeneration != clean {
		t.Fatalf("padded generation forked identity: %d rows, stored %q", rowCount, storedGeneration)
	}
	row, found := s.witnessBuild(t, "p", clean, claimed.BuildID)
	if !found || row.State != "complete" {
		t.Fatalf("converged row missing or wrong state: found %v %+v", found, row)
	}
}

// semanticBuildMigrationChecksum is the sha256sum of the exact bytes of
// internal/store/postgres/migrations/000006_semantic_inventory_build.sql,
// pinned independently of the runner. The store package's
// wantEmbeddedChecksums gate pins the same literal.
const semanticBuildMigrationChecksum = "e69d0dfdd3fc67438b65df7da73ea12edb61324ec8356cbdc4abc1ae5172af3c"

// TestSemanticInventoryBuildMigrationDoubleAppliesAndRecordsLedgerLiterally
// is the runner-contract probe the coordinator required: applying the
// embedded history twice to the live schema must be idempotent, and the
// ledger must record migration 6 exactly once under the literal name and
// checksum matching the file's own sha256sum. It also witnesses that every
// named constraint of the table exists, which is the schema-level half of
// the negative-test non-vacuity below.
func TestSemanticInventoryBuildMigrationDoubleAppliesAndRecordsLedgerLiterally(t *testing.T) {
	s := newSemanticBuildService(t) // first apply happened during service setup

	first, err := storepostgres.MigrateSchema(s.ctx, s.openClient(t), s.schema)
	if err != nil {
		s.fail("MigrateSchema (probe apply 1)", err)
	}
	second, err := storepostgres.MigrateSchema(s.ctx, s.openClient(t), s.schema)
	if err != nil {
		s.fail("MigrateSchema (probe apply 2)", err)
	}
	if second.LatestVersion != first.LatestVersion {
		t.Fatalf("re-apply latest version = %d, want idempotent %d", second.LatestVersion, first.LatestVersion)
	}
	if len(first.Applied) != len(second.Applied) {
		t.Fatalf("re-apply recorded %d rows, want the idempotent %d", len(second.Applied), len(first.Applied))
	}
	for i := range first.Applied {
		a, b := first.Applied[i], second.Applied[i]
		if a.Version != b.Version || a.Name != b.Name || a.Checksum != b.Checksum || !a.AppliedAt.Equal(b.AppliedAt) {
			t.Fatalf("re-apply rewrote ledger row %d: %+v vs %+v", i, a, b)
		}
	}

	// The ledger holds migration 6 exactly once, literally, and the pinned
	// checksum agrees with the sha256sum of the actual file bytes.
	var ledgerRows int
	var name, checksum string
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*), COALESCE(MAX(name), ''), COALESCE(MAX(checksum), '') FROM "+
		pgx.Identifier{s.schema, storepostgres.LedgerTable}.Sanitize()+" WHERE version = $1", int64(6)).
		Scan(&ledgerRows, &name, &checksum); err != nil {
		s.fail("read ledger for version 6", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("ledger holds %d rows for version 6, want exactly one", ledgerRows)
	}
	if name != "semantic_inventory_build" {
		t.Fatalf("ledger name = %q, want semantic_inventory_build", name)
	}
	if checksum != semanticBuildMigrationChecksum {
		t.Fatalf("ledger checksum = %s, want the pinned %s", checksum, semanticBuildMigrationChecksum)
	}
	fileBytes, err := os.ReadFile("../../store/postgres/migrations/000006_semantic_inventory_build.sql")
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	sum := sha256.Sum256(fileBytes)
	if got := hex.EncodeToString(sum[:]); got != checksum {
		t.Fatalf("file sha256sum %s disagrees with ledger checksum %s", got, checksum)
	}

	// Every constraint the contract and the negative tests rely on is
	// actually present on the live table.
	wantConstraints := []string{
		"fhir_semantic_inventory_build_pkey",
		"fhir_semantic_inventory_build_key_ux",
		"fhir_semantic_inventory_build_key_shape",
		"fhir_semantic_inventory_build_project_present",
		"fhir_semantic_inventory_build_build_present",
		"fhir_semantic_inventory_build_build_shape",
		"fhir_semantic_inventory_build_state_known",
		"fhir_semantic_inventory_build_versions_sane",
		"fhir_semantic_inventory_build_source_kind_known",
		"fhir_semantic_inventory_build_availability_known",
		"fhir_semantic_inventory_build_scanned_nonnegative",
		"fhir_semantic_inventory_build_checkpoint_paired",
		"fhir_semantic_inventory_build_lease_paired",
		"fhir_semantic_inventory_build_lease_expiry_nonnegative",
	}
	present := map[string]bool{}
	rows, err := s.iso.Query(s.ctx, "SELECT conname FROM pg_constraint WHERE conrelid = $1::regclass", s.buildFQN())
	if err != nil {
		s.fail("read table constraints", err)
	}
	for rows.Next() {
		var conname string
		if err := rows.Scan(&conname); err != nil {
			rows.Close()
			s.fail("read table constraints", err)
		}
		present[conname] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.fail("read table constraints", err)
	}
	for _, conname := range wantConstraints {
		if !present[conname] {
			t.Fatalf("constraint %s is absent from the live table", conname)
		}
	}
}

// TestSemanticInventoryBuildMigrationConstraintsRejectViolations proves, per
// constraint, that a violating literal row is REFUSED by the named
// PostgreSQL constraint and stores nothing. Every negative is paired with a
// valid control row that succeeds in the same run, so a dropped or renamed
// constraint cannot let the test pass vacuously.
func TestSemanticInventoryBuildMigrationConstraintsRejectViolations(t *testing.T) {
	s := newSemanticBuildService(t)

	const (
		checkViolation  = "23514"
		uniqueViolation = "23505"
	)
	base := func(generation string) semanticBuildRow {
		row := literalBuildRowFor("p", generation, "running")
		row.LeaseToken = "tok-live"
		row.LeaseExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return row
	}
	cases := []struct {
		name           string
		mutate         func(*semanticBuildRow)
		wantCode       string
		wantConstraint string
	}{
		{"checkpoint collection without key", func(r *semanticBuildRow) { r.CheckpointCollection = "Patient"; r.CheckpointKey = "" },
			checkViolation, "fhir_semantic_inventory_build_checkpoint_paired"},
		{"checkpoint key without collection", func(r *semanticBuildRow) { r.CheckpointCollection = ""; r.CheckpointKey = "p-1" },
			checkViolation, "fhir_semantic_inventory_build_checkpoint_paired"},
		{"lease token with zero expiry", func(r *semanticBuildRow) { r.LeaseToken = "tok-x"; r.LeaseExpiresAt = 0 },
			checkViolation, "fhir_semantic_inventory_build_lease_paired"},
		{"lease expiry without token", func(r *semanticBuildRow) { r.LeaseToken = ""; r.LeaseExpiresAt = time.Now().Add(time.Hour).UnixMilli() },
			checkViolation, "fhir_semantic_inventory_build_lease_paired"},
		{"negative lease expiry under a token", func(r *semanticBuildRow) { r.LeaseToken = "tok-x"; r.LeaseExpiresAt = -5 },
			checkViolation, "fhir_semantic_inventory_build_lease_expiry_nonnegative"},
		{"unknown state", func(r *semanticBuildRow) { r.State = "queued" },
			checkViolation, "fhir_semantic_inventory_build_state_known"},
		{"unknown source kind", func(r *semanticBuildRow) { r.SourceKind = "stream" },
			checkViolation, "fhir_semantic_inventory_build_source_kind_known"},
		{"unknown source availability", func(r *semanticBuildRow) { r.SourceAvailability = "probably" },
			checkViolation, "fhir_semantic_inventory_build_availability_known"},
		{"negative scanned_resources", func(r *semanticBuildRow) { r.ScannedResources = -1 },
			checkViolation, "fhir_semantic_inventory_build_scanned_nonnegative"},
		{"malformed build_key", func(r *semanticBuildRow) { r.BuildKey = "sib_short" },
			checkViolation, "fhir_semantic_inventory_build_key_shape"},
		{"blank project", func(r *semanticBuildRow) { r.Project = "" },
			checkViolation, "fhir_semantic_inventory_build_project_present"},
		{"malformed build_id", func(r *semanticBuildRow) { r.BuildID = strings.Repeat("z", 64) },
			checkViolation, "fhir_semantic_inventory_build_build_shape"},
		{"zero observation_schema", func(r *semanticBuildRow) { r.ObservationSchema = 0 },
			checkViolation, "fhir_semantic_inventory_build_versions_sane"},
	}
	for i, tc := range cases {
		bad := base(fmt.Sprintf("neg%d", i))
		tc.mutate(&bad)
		// Non-vacuity control: the identical row WITHOUT the violation must
		// insert successfully in this same run and scope family.
		control := base(fmt.Sprintf("ctrl%d", i))
		s.seedSemanticBuildLiteral(t, control)
		if _, found := s.witnessBuild(t, control.Project, control.DatasetGeneration, control.BuildID); !found {
			t.Fatalf("%s: control row did not insert — the harness is not proving anything", tc.name)
		}
		err := s.trySeedSemanticBuild(t, bad)
		var pgErr *pgconn.PgError
		if err == nil {
			t.Fatalf("%s: violating row was ACCEPTED — constraint missing or renamed", tc.name)
		}
		if !errors.As(err, &pgErr) {
			t.Fatalf("%s: violating row failed with a non-database error: %v", tc.name, err)
		}
		if pgErr.Code != tc.wantCode || pgErr.ConstraintName != tc.wantConstraint {
			t.Fatalf("%s: rejected by code=%s constraint=%s, want code=%s constraint=%s",
				tc.name, pgErr.Code, pgErr.ConstraintName, tc.wantCode, tc.wantConstraint)
		}
		s.requireNoRow(t, bad.Project, bad.DatasetGeneration, bad.BuildID, tc.name)
	}

	// Identity arbitration lives in the database: the primary key refuses a
	// second row for one (project, dataset_generation, build_id), and the
	// UNIQUE build_key refuses a sib_ identity reused across scopes.
	dup := base("dup-pk")
	s.seedSemanticBuildLiteral(t, dup)
	rewritten := dup
	rewritten.ScannedResources = 999
	err := s.trySeedSemanticBuild(t, rewritten)
	var pgErr *pgconn.PgError
	if err == nil || !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation || pgErr.ConstraintName != "fhir_semantic_inventory_build_pkey" {
		t.Fatalf("duplicate identity accepted or misattributed: %v", err)
	}
	if row, _ := s.witnessBuild(t, dup.Project, dup.DatasetGeneration, dup.BuildID); row.ScannedResources != 42 {
		t.Fatalf("duplicate identity replaced the stored row: %+v", row)
	}
	other := base("dup-key")
	stolen := other
	stolen.BuildKey = base("dup-pk").BuildKey
	err = s.trySeedSemanticBuild(t, stolen)
	if err == nil || !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation || pgErr.ConstraintName != "fhir_semantic_inventory_build_key_ux" {
		t.Fatalf("reused sib_ build_key accepted or misattributed: %v", err)
	}
	s.requireNoRow(t, other.Project, other.DatasetGeneration, other.BuildID, "reused build_key")
}

// TestZZZeroLeftoverSemanticInventoryBuildFixtures is the ownership-scoped
// zero-leftover audit. It checks, by exact name only, every database (and
// the schema that lived inside it) this file verified into existence. It
// performs no global scan of pg_database or pg_namespace, so concurrent
// sibling workers sweeping their own prefixes neither race this audit nor
// trip it.
func TestZZZeroLeftoverSemanticInventoryBuildFixtures(t *testing.T) {
	raw := semanticBuildTestPostgresURL(t)
	semanticBuildOwnedMu.Lock()
	owned := make(map[string]string, len(semanticBuildOwnedByName))
	for db, schema := range semanticBuildOwnedByName {
		owned[db] = schema
	}
	semanticBuildOwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no pgcat04_it_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, "pgcat04_it_") {
			t.Fatalf("registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Errorf("audit owned database %s: %v", db, err)
			continue
		}
		if count != 0 {
			t.Errorf("leftover owned database %s after all cleanups", db)
			if schema != "" {
				// Report the schema inside the leftover database, exact
				// name only.
				u, err := url.Parse(raw)
				if err == nil {
					if q := u.Query(); q.Get("dbname") != "" {
						q.Del("dbname")
						u.RawQuery = q.Encode()
					}
					u.Path = "/" + db
					if probe, err := pgxpool.New(ctx, u.String()); err == nil {
						var schemaCount int
						if err := probe.QueryRow(ctx, "SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
							t.Errorf("audit owned schema %s in %s: %v", schema, db, err)
						} else if schemaCount != 0 {
							t.Errorf("leftover owned schema %s in leftover database %s", schema, db)
						}
						probe.Close()
					}
				}
			}
		}
	}
}
