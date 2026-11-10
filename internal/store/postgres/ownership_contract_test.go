// TestPostgresMigrationOwnershipContract is the arch-backend-05 acceptance
// suite for the authoritative migration-ownership record in
// migration_ownership.go. It runs without PostgreSQL for the offline parts
// and, when LOOM_TEST_POSTGRES_URL is set, additionally drives real
// PostgreSQL state through every production owner path.
//
// What this suite proves:
//   - The authored manifest equals the embedded bytes of every owner
//     package, exactly and in order (no extra, no missing, no drift).
//   - The pre-refactor fixture bytes preserved under
//     testdata/ab05_pre_refactor are byte-identical to the migrations now
//     embedded in their owner packages (the move changed paths, not bytes).
//   - Shared bootstrap migrations (ledger v1, dataset_generation_manifest
//     v3, the recipe ledger v1) are byte-identical across the series that
//     pin them, which is exactly what ValidateAssignment permits to share a
//     schema.
//   - ValidateAssignment refuses, before any DDL, every two-series
//     assignment whose pinned ledgers disagree at a shared version.
//   - On PostgreSQL: each series bootstraps fresh through its owner and
//     converges on rerun; a representative domain operation per series runs
//     on the migrated tables; an incompatible history is refused with the
//     ledger and catalog witnesses unchanged; and a schema seeded from the
//     frozen pre-refactor bytes upgrades to the owner history with the
//     ledger unchanged.
//   - The TestZZ sweep leaves zero verify_ab05_* schemas behind.
//
// Every schema this suite creates is named verify_ab05_* so the sweep can
// prove zero-survivor cleanup, and every mutation runs only when the test
// URL is present (CI asserts zero skips through the runner lane).
package postgres_test

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/authscope"
	chartifact "github.com/calypr/loom/internal/dataframe/execution/chartifact"
	chartifactpg "github.com/calypr/loom/internal/dataframe/execution/chartifact/postgres"
	"github.com/calypr/loom/internal/dataframe/publication"
	publicationpg "github.com/calypr/loom/internal/dataframe/publication/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	recipeexecpg "github.com/calypr/loom/internal/dataframe/recipe/exec/postgres"
	recipepg "github.com/calypr/loom/internal/dataframe/recipe/postgres"
	"github.com/calypr/loom/internal/dataset"
	datasetpg "github.com/calypr/loom/internal/dataset/postgres"
	"github.com/calypr/loom/internal/explorer"
	explorerpg "github.com/calypr/loom/internal/explorer/postgres"
	"github.com/calypr/loom/internal/source"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

const (
	// ab05Prefix namespaces every schema this suite creates so the
	// zero-survivor sweep can scan for exactly its leftovers.
	ab05Prefix = "verify_ab05_"
	// preRefactorRoot pins the permanent snapshot of the migration files
	// as they existed at their old fixture-directory paths before this
	// ticket moved them into their owner packages (git mv, bytes frozen).
	// Coordinator-ratified as IMMUTABLE EVIDENCE, like the migrations
	// themselves: any byte change under this directory must intentionally
	// redden PreRefactorBytesFrozenInOwners -- never loosen the check.
	preRefactorRoot = "testdata/ab05_pre_refactor"
)

// ab05SourceFS reads the source-series migrations directly from this
// package's own embed surface, with no migration loader between the bytes
// and the assertion (condition C1: non-circularity needs at least one
// path from embed FS to manifest literal that trusts no shared code).
//
//go:embed migrations/*.sql
var ab05SourceFS embed.FS

// ---------------------------------------------------------------------------
// offline: manifest <-> embedded owner bytes
// ---------------------------------------------------------------------------

// ab05OwnerHistories maps every registered series to the function that
// returns the owner package's embedded history. A new series without an
// entry here fails TestPostgresMigrationOwnershipContract immediately.
var ab05OwnerHistories = map[pgstore.SeriesID]func() ([]pgstore.Migration, error){
	pgstore.SeriesSource:               pgstore.EmbeddedMigrations,
	pgstore.SeriesExplorerControl:      explorerpg.OwnerMigrations,
	pgstore.SeriesPublicationControl:   publicationpg.OwnerMigrations,
	pgstore.SeriesDatasetRelease:       datasetpg.OwnerMigrations,
	pgstore.SeriesRecipeChains:         recipepg.ChainMigrations,
	pgstore.SeriesRecipeExecRegistries: recipeexecpg.RegistryMigrations,
}

// ab05SignatureTable is one real (not just the ledger) table each series'
// DDL creates, used to witness that the owner history actually built the
// schema it claims to own.
var ab05SignatureTable = map[pgstore.SeriesID]string{
	pgstore.SeriesSource:               "fhir_resource",
	pgstore.SeriesExplorerControl:      explorerpg.OwnerTable,
	pgstore.SeriesPublicationControl:   publicationpg.BundleLeasesTable,
	pgstore.SeriesDatasetRelease:       datasetpg.ProjectReleasesTable,
	pgstore.SeriesRecipeChains:         recipepg.RevisionsTable,
	pgstore.SeriesRecipeExecRegistries: recipeexecpg.RecipesTable,
}

var ab05ChecksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ab05ManifestMatchesOwners(t *testing.T) {
	t.Helper()
	manifest := pgstore.Manifest()
	if len(manifest) != len(ab05OwnerHistories) {
		t.Fatalf("manifest holds %d series, ownership map knows %d: register the new series in both places",
			len(manifest), len(ab05OwnerHistories))
	}
	for _, m := range manifest {
		history, ok := ab05OwnerHistories[m.Series]
		if !ok {
			t.Fatalf("series %s has no owner history function in the contract test", m.Series)
		}
		entries, err := history()
		if err != nil {
			t.Fatalf("series %s: owner history: %v", m.Series, err)
		}
		if len(entries) != len(m.Entries) {
			t.Fatalf("series %s: owner embeds %d migrations, manifest records %d: append the new version to migration_ownership.go in the same change",
				m.Series, len(entries), len(m.Entries))
		}
		for i := range entries {
			if entries[i].Version != m.Entries[i].Version || entries[i].Name != m.Entries[i].Name ||
				entries[i].Checksum != m.Entries[i].Checksum {
				t.Fatalf("series %s entry %d = %d/%s/%s, manifest records %d/%s/%s: manifest drifted from the embedded bytes",
					m.Series, i, entries[i].Version, entries[i].Name, entries[i].Checksum[:12],
					m.Entries[i].Version, m.Entries[i].Name, m.Entries[i].Checksum[:12])
			}
			if i > 0 && entries[i-1].Version >= entries[i].Version {
				t.Fatalf("series %s: owner history not strictly ascending at entry %d", m.Series, i)
			}
		}
		// LookupManifest must agree with the ordered Manifest() view and
		// hand out copies, not views into the authored record.
		looked, ok := pgstore.LookupManifest(m.Series)
		if !ok {
			t.Fatalf("LookupManifest(%s) not found", m.Series)
		}
		if !reflect.DeepEqual(looked, m) {
			t.Fatalf("LookupManifest(%s) disagrees with Manifest()", m.Series)
		}
		looked.Entries[0].Checksum = strings.Repeat("0", 64)
		looked.Stores = append(looked.Stores, "tampered")
		if again, _ := pgstore.LookupManifest(m.Series); !reflect.DeepEqual(again, m) {
			t.Fatalf("LookupManifest(%s) hands out views into the authored record, not copies", m.Series)
		}
	}
	// ManifestJSON is the on-demand rendering of the same record.
	raw, err := pgstore.ManifestJSON()
	if err != nil {
		t.Fatalf("ManifestJSON: %v", err)
	}
	var rendered []pgstore.HistoryManifest
	if err := json.Unmarshal(raw, &rendered); err != nil {
		t.Fatalf("ManifestJSON did not round-trip: %v", err)
	}
	if !reflect.DeepEqual(rendered, manifest) {
		t.Fatal("ManifestJSON rendering differs from Manifest()")
	}
}

func ab05ManifestRecordInvariants(t *testing.T) {
	t.Helper()
	want := []pgstore.SeriesID{
		pgstore.SeriesDatasetRelease, pgstore.SeriesExplorerControl, pgstore.SeriesPublicationControl,
		pgstore.SeriesRecipeChains, pgstore.SeriesRecipeExecRegistries, pgstore.SeriesSource,
	}
	manifest := pgstore.Manifest()
	if len(manifest) != len(want) {
		t.Fatalf("series count = %d, want exactly the six production series", len(manifest))
	}
	for i, m := range manifest {
		// Manifest() sorts by series ID; want is alphabetical.
		if m.Series != want[i] {
			t.Fatalf("Manifest() order = %v, want sorted %v", manifest[i].Series, want[i])
		}
		if m.OwnerPackage == "" {
			t.Fatalf("series %s: no owner package recorded", m.Series)
		}
		if _, err := os.Stat(filepath.Join("..", "..", "..", m.OwnerPackage)); err != nil {
			t.Fatalf("series %s: owner package %s is not a real directory: %v", m.Series, m.OwnerPackage, err)
		}
		if len(m.Stores) == 0 {
			t.Fatalf("series %s: no production stores recorded for this history", m.Series)
		}
		var prev int64
		for _, e := range m.Entries {
			if e.Version <= prev {
				t.Fatalf("series %s: entries not strictly ascending at version %d", m.Series, e.Version)
			}
			prev = e.Version
			if e.Name == "" {
				t.Fatalf("series %s version %d: empty name", m.Series, e.Version)
			}
			if !ab05ChecksumPattern.MatchString(e.Checksum) {
				t.Fatalf("series %s version %d: checksum %q is not 64 lowercase hex", m.Series, e.Version, e.Checksum)
			}
		}
	}
}

// ab05MovedFiles records each migration this ticket relocated into its
// owner package: the frozen pre-refactor path and the series/version the
// owner now embeds it as.
var ab05MovedFiles = []struct {
	preRefactor string
	series      pgstore.SeriesID
	version     int64
	name        string
}{
	{"tests/migrations-capabilities/000004_capability_artifacts.sql", pgstore.SeriesExplorerControl, 4, "capability_artifacts"},
	{"tests/migrations-interpretations/000007_interpretation_library.sql", pgstore.SeriesExplorerControl, 7, "interpretation_library"},
	{"tests/migrations-selections/000008_selection_revisions.sql", pgstore.SeriesExplorerControl, 8, "selection_revisions"},
	{"tests/migrations/000004_publication_bundle_catalog.sql", pgstore.SeriesPublicationControl, 4, "publication_bundle_catalog"},
	{"tests/migrations-leases/000006_publication_lease_checkpoints.sql", pgstore.SeriesPublicationControl, 6, "publication_lease_checkpoints"},
	{"tests/migrations-chartifact/000007_private_clickhouse_artifacts.sql", pgstore.SeriesPublicationControl, 7, "private_clickhouse_artifacts"},
	{"tests/migrations-readpins/000009_publication_read_pins.sql", pgstore.SeriesPublicationControl, 9, "publication_read_pins"},
	{"internal/dataset/postgres/testdata/migrations-releases/000008_project_release.sql", pgstore.SeriesDatasetRelease, 8, "project_release"},
}

func ab05PreRefactorBytesFrozen(t *testing.T) {
	t.Helper()
	for _, moved := range ab05MovedFiles {
		snapshot := filepath.Join(preRefactorRoot, filepath.FromSlash(moved.preRefactor))
		disk, err := os.ReadFile(snapshot)
		if err != nil {
			t.Fatalf("%s: frozen snapshot missing: %v", snapshot, err)
		}
		m, ok := pgstore.LookupManifest(moved.series)
		if !ok {
			t.Fatalf("series %s missing from the manifest", moved.series)
		}
		var pinned *pgstore.HistoryEntry
		for i := range m.Entries {
			if m.Entries[i].Version == moved.version {
				pinned = &m.Entries[i]
			}
		}
		if pinned == nil || pinned.Name != moved.name {
			t.Fatalf("%s: manifest series %s does not pin version %d as %s",
				moved.preRefactor, moved.series, moved.version, moved.name)
		}
		sum := sha256.Sum256(disk)
		if got := hex.EncodeToString(sum[:]); got != pinned.Checksum {
			t.Fatalf("%s: snapshot sha256 %s != manifest/ledger identity %s for %d/%s",
				moved.preRefactor, got[:12], pinned.Checksum[:12], pinned.Version, pinned.Name)
		}
		entries, err := ab05OwnerHistories[moved.series]()
		if err != nil {
			t.Fatalf("series %s owner history: %v", moved.series, err)
		}
		var embedded *pgstore.Migration
		for i := range entries {
			if entries[i].Version == moved.version {
				embedded = &entries[i]
			}
		}
		if embedded == nil || string(disk) != embedded.SQL {
			t.Fatalf("%s: frozen snapshot bytes differ from the bytes embedded in %s (the move must change paths, never bytes)",
				moved.preRefactor, m.OwnerPackage)
		}
	}
}

func ab05SharedBootstrapBytesIdentical(t *testing.T) {
	t.Helper()
	source, err := pgstore.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("source history: %v", err)
	}
	find := func(history []pgstore.Migration, version int64, what string) pgstore.Migration {
		t.Helper()
		for _, m := range history {
			if m.Version == version {
				return m
			}
		}
		t.Fatalf("%s: no version %d", what, version)
		return pgstore.Migration{}
	}
	publicationHistory, err := publicationpg.OwnerMigrations()
	if err != nil {
		t.Fatalf("publication history: %v", err)
	}
	datasetHistory, err := datasetpg.OwnerMigrations()
	if err != nil {
		t.Fatalf("dataset history: %v", err)
	}
	chains, err := recipepg.ChainMigrations()
	if err != nil {
		t.Fatalf("recipe chains history: %v", err)
	}
	execRegistries, err := recipeexecpg.RegistryMigrations()
	if err != nil {
		t.Fatalf("recipe exec history: %v", err)
	}
	pairs := []struct {
		what  string
		left  pgstore.Migration
		right pgstore.Migration
	}{
		{"publication ledger v1 vs source ledger v1", find(publicationHistory, 1, "publication"), find(source, 1, "source")},
		{"dataset ledger v1 vs source ledger v1", find(datasetHistory, 1, "dataset"), find(source, 1, "source")},
		{"dataset manifest v3 vs source manifest v3", find(datasetHistory, 3, "dataset"), find(source, 3, "source")},
		{"exec ledger v1 vs chains ledger v1", find(execRegistries, 1, "exec"), find(chains, 1, "chains")},
	}
	for _, p := range pairs {
		if p.left.Name != p.right.Name || p.left.Checksum != p.right.Checksum || p.left.SQL != p.right.SQL {
			t.Fatalf("%s: shared bootstrap bytes diverged: %s vs %s", p.what, p.left.Checksum[:12], p.right.Checksum[:12])
		}
	}
}

// ab05ManifestChecksumsNonCircular discharges condition C1: the manifest
// literals are hand-authored, so a manifest that only agreed with itself
// would prove nothing. This recomputes the SHA-256 in the test itself over
// the exact SQL bytes each owner package served from its embed FS (via the
// shared loader) and ties three independent identities together: the
// test-recomputed hash, the loader-computed ledger identity, and the
// manifest literal. For the source series it additionally reads the files
// through this package's own embed surface with no loader at all, so at
// least one comparison runs embed FS -> manifest literal directly. A future
// edit that changes a migration's bytes without re-deriving the manifest
// literal reddens here, and a manifest literal retyped to match tampered
// SQL reddens the ledger checks against deployed databases.
func ab05ManifestChecksumsNonCircular(t *testing.T) {
	t.Helper()
	for _, m := range pgstore.Manifest() {
		entries, err := ab05OwnerHistories[m.Series]()
		if err != nil {
			t.Fatalf("series %s: owner history: %v", m.Series, err)
		}
		if len(entries) != len(m.Entries) {
			t.Fatalf("series %s: %d embedded migrations, manifest records %d", m.Series, len(entries), len(m.Entries))
		}
		for i := range entries {
			sum := sha256.Sum256([]byte(entries[i].SQL))
			recomputed := hex.EncodeToString(sum[:])
			if recomputed != entries[i].Checksum {
				t.Fatalf("series %s %d/%s: test-recomputed sha256 %s != loader checksum %s: loader is not hashing the served bytes",
					m.Series, entries[i].Version, entries[i].Name, recomputed[:12], entries[i].Checksum[:12])
			}
			if recomputed != m.Entries[i].Checksum {
				t.Fatalf("series %s %d/%s: test-recomputed sha256 %s != manifest literal %s: the manifest is pinning itself, not these bytes",
					m.Series, entries[i].Version, entries[i].Name, recomputed[:12], m.Entries[i].Checksum[:12])
			}
		}
		if m.Series != pgstore.SeriesSource {
			continue
		}
		// Loader-free path straight from this package's embed FS.
		for _, e := range m.Entries {
			file := fmt.Sprintf("migrations/%06d_%s.sql", e.Version, e.Name)
			raw, err := ab05SourceFS.ReadFile(file)
			if err != nil {
				t.Fatalf("source embed FS read %s: %v", file, err)
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != e.Checksum {
				t.Fatalf("source embed FS %s: sha256 %s != manifest literal %s", file, got[:12], e.Checksum[:12])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// offline: ValidateAssignment matrix
// ---------------------------------------------------------------------------

func ab05AssignmentValidation(t *testing.T) {
	t.Helper()
	type wantConflict struct {
		version int64
		a, b    pgstore.SeriesID
	}
	cases := []struct {
		name     string
		assign   pgstore.Assignment
		errIs    error
		conflict *wantConflict
	}{
		{
			name: "all six series on distinct schemas",
			assign: pgstore.Assignment{
				pgstore.SeriesSource: "s_source", pgstore.SeriesExplorerControl: "s_explorer",
				pgstore.SeriesPublicationControl: "s_pub", pgstore.SeriesDatasetRelease: "s_rel",
				pgstore.SeriesRecipeChains: "s_chains", pgstore.SeriesRecipeExecRegistries: "s_exec",
			},
		},
		{
			name:   "empty assignment",
			assign: pgstore.Assignment{},
		},
		{
			name: "recipe pair may share (identical ledger v1)",
			assign: pgstore.Assignment{
				pgstore.SeriesRecipeChains: "s_recipe", pgstore.SeriesRecipeExecRegistries: "s_recipe",
			},
		},
		{
			// Condition C5, accept side: the only shared version is pinned
			// byte-identical, so one ledger can validate both histories.
			name: "publication and dataset release may share (ledger v1 byte-identical)",
			assign: pgstore.Assignment{
				pgstore.SeriesPublicationControl: "s_shared", pgstore.SeriesDatasetRelease: "s_shared",
			},
		},
		{
			// Condition C5, distinct-schema side: identical pinned shared
			// versions across separate schemas are pure byte-copy legality
			// and must pass.
			name: "identical shared versions on distinct schemas",
			assign: pgstore.Assignment{
				pgstore.SeriesRecipeChains: "s_chains_a", pgstore.SeriesRecipeExecRegistries: "s_chains_b",
				pgstore.SeriesPublicationControl: "s_pub_a", pgstore.SeriesDatasetRelease: "s_rel_a",
			},
		},
		{
			name: "source and explorer share: ledger v1 checksums differ",
			assign: pgstore.Assignment{
				pgstore.SeriesSource: "x", pgstore.SeriesExplorerControl: "x",
			},
			conflict: &wantConflict{1, pgstore.SeriesExplorerControl, pgstore.SeriesSource},
		},
		{
			name: "source and dataset share: v1/v3 identical but v8 differs",
			assign: pgstore.Assignment{
				pgstore.SeriesSource: "x", pgstore.SeriesDatasetRelease: "x",
			},
			conflict: &wantConflict{8, pgstore.SeriesDatasetRelease, pgstore.SeriesSource},
		},
		{
			name: "source and publication share: v1 identical but v4 differs",
			assign: pgstore.Assignment{
				pgstore.SeriesSource: "x", pgstore.SeriesPublicationControl: "x",
			},
			conflict: &wantConflict{4, pgstore.SeriesPublicationControl, pgstore.SeriesSource},
		},
		{
			name: "publication and recipe chains share: ledger v1 differs",
			assign: pgstore.Assignment{
				pgstore.SeriesPublicationControl: "x", pgstore.SeriesRecipeChains: "x",
			},
			conflict: &wantConflict{1, pgstore.SeriesPublicationControl, pgstore.SeriesRecipeChains},
		},
		{
			name:   "unknown series",
			assign: pgstore.Assignment{"warehouse_ledger": "x"},
			errIs:  pgstore.ErrInvalidAssignment,
		},
		{
			name:   "uppercase schema",
			assign: pgstore.Assignment{pgstore.SeriesSource: "Bad_Name"},
			errIs:  pgstore.ErrInvalidAssignment,
		},
		{
			name:   "empty schema",
			assign: pgstore.Assignment{pgstore.SeriesSource: ""},
			errIs:  pgstore.ErrInvalidAssignment,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pgstore.ValidateAssignment(tc.assign)
			if tc.conflict == nil && tc.errIs == nil {
				if err != nil {
					t.Fatalf("ValidateAssignment = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateAssignment accepted %v, want rejection", tc.assign)
			}
			if tc.errIs != nil {
				if !errors.Is(err, tc.errIs) {
					t.Fatalf("ValidateAssignment error %v does not wrap %v", err, tc.errIs)
				}
				return
			}
			var conflict *pgstore.AssignmentConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("ValidateAssignment error %v is not an *AssignmentConflict", err)
			}
			if !errors.Is(err, pgstore.ErrInvalidAssignment) {
				t.Fatalf("conflict error %v does not wrap ErrInvalidAssignment", err)
			}
			if conflict.Version != tc.conflict.version || conflict.SeriesA != tc.conflict.a ||
				conflict.SeriesB != tc.conflict.b {
				t.Fatalf("conflict = %s/%s at version %d, want %s/%s at %d",
					conflict.SeriesA, conflict.SeriesB, conflict.Version,
					tc.conflict.a, tc.conflict.b, tc.conflict.version)
			}
			if conflict.ChecksumA == conflict.ChecksumB && conflict.NameA == conflict.NameB {
				t.Fatalf("conflict names no distinguishing identity: %+v", conflict)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL harness
// ---------------------------------------------------------------------------

func ab05PostgresURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("LOOM_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL halves of the ownership contract " +
			"(the runner lane asserts this suite runs without skips)")
	}
	return raw
}

func ab05Admin(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, ab05PostgresURL(t))
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("admin ping: %v", err)
	}
	return pool
}

// ab05Schema creates one verify_ab05_* schema and registers its removal.
func ab05Schema(t *testing.T, ctx context.Context, admin *pgxpool.Pool, name string) string {
	t.Helper()
	schema := ab05Prefix + name
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgxIdentifier(schema)); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+pgxIdentifier(schema)+" CASCADE")
	})
	return schema
}

func pgxIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ab05ReadLedger returns the literal rows of one schema's ledger.
func ab05ReadLedger(t *testing.T, ctx context.Context, admin *pgxpool.Pool, schema string) []pgstore.LedgerRecord {
	t.Helper()
	rows, err := admin.Query(ctx, "SELECT version, name, checksum, applied_at FROM "+
		pgxIdentifier(schema)+`.schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read ledger of %s: %v", schema, err)
	}
	defer rows.Close()
	var out []pgstore.LedgerRecord
	for rows.Next() {
		var r pgstore.LedgerRecord
		if err := rows.Scan(&r.Version, &r.Name, &r.Checksum, &r.AppliedAt); err != nil {
			t.Fatalf("scan ledger row of %s: %v", schema, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("ledger rows of %s: %v", schema, err)
	}
	return out
}

func ab05ledgerEqual(t *testing.T, schema string, got []pgstore.LedgerRecord, want []pgstore.HistoryEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ledger of %s holds %d rows, want %d", schema, len(got), len(want))
	}
	for i := range got {
		if got[i].Version != want[i].Version || got[i].Name != want[i].Name || got[i].Checksum != want[i].Checksum {
			t.Fatalf("ledger row %d of %s = %d/%s/%s, want %d/%s/%s",
				i, schema, got[i].Version, got[i].Name, got[i].Checksum[:12],
				want[i].Version, want[i].Name, want[i].Checksum[:12])
		}
	}
}

func ab05RegclassExists(t *testing.T, ctx context.Context, admin *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	var present bool
	if err := admin.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL",
		schema+"."+table).Scan(&present); err != nil {
		t.Fatalf("to_regclass %s.%s: %v", schema, table, err)
	}
	return present
}

// ab05OwnerMigrate applies one series' production owner entry point.
func ab05OwnerMigrate(t *testing.T, ctx context.Context, client *pgstore.Client, admin *pgxpool.Pool,
	series pgstore.SeriesID, schema string) (pgstore.MigrateResult, error) {
	t.Helper()
	switch series {
	case pgstore.SeriesSource:
		history, err := pgstore.EmbeddedMigrations()
		if err != nil {
			return pgstore.MigrateResult{}, err
		}
		runner, err := pgstore.NewMigrationRunner(client, schema, history)
		if err != nil {
			return pgstore.MigrateResult{}, err
		}
		return runner.Migrate(ctx)
	case pgstore.SeriesExplorerControl:
		return explorerpg.MigrateSchema(ctx, client, schema)
	case pgstore.SeriesPublicationControl:
		return publicationpg.MigrateSchema(ctx, client, schema)
	case pgstore.SeriesDatasetRelease:
		return datasetpg.MigrateSchema(ctx, client, schema)
	case pgstore.SeriesRecipeChains:
		return recipepg.MigrateSchema(ctx, client, schema)
	case pgstore.SeriesRecipeExecRegistries:
		return recipeexecpg.MigrateSchema(ctx, client, schema)
	}
	t.Fatalf("no owner entry point for series %s", series)
	return pgstore.MigrateResult{}, nil
}

func ab05Client(t *testing.T, ctx context.Context) *pgstore.Client {
	t.Helper()
	client, err := pgstore.Open(ctx, pgstore.Config{
		DSN:            ab05PostgresURL(t),
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open store client: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

// ---------------------------------------------------------------------------
// PostgreSQL: fresh bootstrap convergence through the owner entry points
// ---------------------------------------------------------------------------

func ab05FreshBootstrapsConverge(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin := ab05Admin(t, ctx)
	client := ab05Client(t, ctx)
	for _, m := range pgstore.Manifest() {
		t.Run(string(m.Series), func(t *testing.T) {
			schema := ab05Schema(t, ctx, admin, "boot_"+string(m.Series))
			first, err := ab05OwnerMigrate(t, ctx, client, admin, m.Series, schema)
			if err != nil {
				t.Fatalf("owner bootstrap of %s: %v", m.Series, err)
			}
			ab05ledgerEqual(t, schema, first.Applied, m.Entries)
			ab05ledgerEqual(t, schema, ab05ReadLedger(t, ctx, admin, schema), m.Entries)
			if !ab05RegclassExists(t, ctx, admin, schema, "schema_migrations") ||
				!ab05RegclassExists(t, ctx, admin, schema, ab05SignatureTable[m.Series]) {
				t.Fatalf("series %s: ledger or signature table missing after bootstrap", m.Series)
			}
			second, err := ab05OwnerMigrate(t, ctx, client, admin, m.Series, schema)
			if err != nil {
				t.Fatalf("owner rerun of %s: %v", m.Series, err)
			}
			if !reflect.DeepEqual(first.Applied, second.Applied) {
				t.Fatalf("series %s: rerun ledger drifted from first apply", m.Series)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL: one representative real domain operation per series
// ---------------------------------------------------------------------------

func ab05RepresentativeDomainOperations(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin := ab05Admin(t, ctx)
	client := ab05Client(t, ctx)
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	owner := "ab05-owner"

	t.Run("source resource round trip", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "op_source")
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesSource, schema); err != nil {
			t.Fatalf("bootstrap source: %v", err)
		}
		unrestricted := true
		scope := source.ResourceScope{
			Project: "verify-ab05", DatasetGeneration: "gen-1",
			AuthResourcePathsUnrestricted: &unrestricted,
		}
		repo, err := pgstore.NewResourceRepository(client, schema)
		if err != nil {
			t.Fatalf("resource repository: %v", err)
		}
		row := source.Resource{
			Project: scope.Project, DatasetGeneration: scope.DatasetGeneration,
			ResourceType: "Patient", LogicalID: "pat-1", AuthResourcePath: "fhir/Patient",
			Payload: json.RawMessage(`{"resourceType":"Patient","id":"pat-1"}`),
		}
		seq, err := repo.InsertResource(ctx, row)
		if err != nil {
			t.Fatalf("insert resource: %v", err)
		}
		if seq <= 0 {
			t.Fatalf("inserted resource seq = %d, want a server-assigned positive seq", seq)
		}
		got, err := repo.GetResource(ctx, scope, "Patient", "pat-1")
		if err != nil {
			t.Fatalf("get resource: %v", err)
		}
		if got.ResourceSeq != seq || got.LogicalID != "pat-1" || got.AuthResourcePath != "fhir/Patient" {
			t.Fatalf("resource round trip = %+v, want seq %d", got, seq)
		}
	})

	t.Run("explorer owner create and read", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "op_explorer")
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesExplorerControl, schema); err != nil {
			t.Fatalf("bootstrap explorer: %v", err)
		}
		store, err := explorerpg.New(client, schema)
		if err != nil {
			t.Fatalf("explorer store: %v", err)
		}
		e := explorer.Explorer{
			Project: "verify-ab05", ExplorerID: "exp-1", Title: "Ownership Probe",
			ManagementMode:                explorer.ManagementRepository,
			DraftConfig:                   json.RawMessage(`{"panels":[]}`),
			DraftVersion:                  1,
			DraftDigest:                   "sha256:ab05",
			DraftSnapshotToken:            "snap-1",
			DraftSourceGeneration:         "gen-1",
			DraftAuthorizationScopeDigest: "authz-1",
			LastAuthoringCommandID:        "cmd-1",
			LastAuthoringCommandDigest:    "sha256:cmd",
			LastAuthoringCommandResults:   json.RawMessage(`{"ok":true}`),
			UpdatedBy:                     "ab05@calypr",
			UpdatedAt:                     now,
		}
		created, err := store.Create(ctx, e)
		if err != nil {
			t.Fatalf("create explorer: %v", err)
		}
		if created.Title != e.Title {
			t.Fatalf("created explorer title = %q", created.Title)
		}
		got, err := store.Get(ctx, e.Project, e.ExplorerID)
		if err != nil {
			t.Fatalf("get explorer: %v", err)
		}
		if got.DraftDigest != "sha256:ab05" || got.DraftVersion != 1 {
			t.Fatalf("explorer round trip = %+v", got)
		}
	})

	t.Run("publication control plane registration", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "op_pub")
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesPublicationControl, schema); err != nil {
			t.Fatalf("bootstrap publication: %v", err)
		}
		registry, err := publicationpg.New(admin, schema)
		if err != nil {
			t.Fatalf("publication registry: %v", err)
		}
		key, executionID := "ab05-key", "ab05-exec-1"
		held, err := registry.AcquireBundleLease(ctx, key, owner, now.Add(time.Hour))
		if err != nil || !held {
			t.Fatalf("acquire bundle lease = (%v, %v), want held", held, err)
		}
		execution := publication.BundleExecution{
			ID: executionID, Key: key,
			BundleIdentity: publication.BundleIdentity{
				Name: "panel", TranslationVersion: "v1", Project: "verify-ab05",
				DatasetGeneration: "gen-1", RecipeDigest: "sha256:recipe", SchemaDigest: "sha256:schema",
				ScopeDigest: "sha256:scope", EngineVersion: "loom-ab05", AuthScopeMode: "project",
				AuthResourcePaths: []string{"fhir/Patient"},
			},
			State: publication.BundleQueued, CreatedAt: now, UpdatedAt: now, ReadyAt: &now,
		}
		if err := registry.SaveExecution(ctx, execution, owner); err != nil {
			t.Fatalf("save execution: %v", err)
		}
		got, err := registry.GetExecution(ctx, executionID)
		if err != nil {
			t.Fatalf("get execution: %v", err)
		}
		if got.State != publication.BundleQueued || got.Project != "verify-ab05" {
			t.Fatalf("execution round trip = %+v", got)
		}
		pins, err := publicationpg.NewReadPins(admin, schema)
		if err != nil {
			t.Fatalf("read pins: %v", err)
		}
		pinned, err := pins.AcquireExecutionReadPin(ctx, executionID, owner, now.Add(time.Hour))
		if err != nil || !pinned {
			t.Fatalf("acquire execution read pin = (%v, %v), want pinned", pinned, err)
		}
		catalog, err := chartifactpg.NewCatalog(admin, schema)
		if err != nil {
			t.Fatalf("artifact catalog: %v", err)
		}
		artifactID := "ab05-artifact-1"
		manifest := chartifact.Manifest{
			ArtifactID: artifactID,
			Identity: chartifact.Identity{
				ExecutionID: executionID, OutputID: "output-1", StageID: "stage-1",
				Project: "verify-ab05", DatasetGeneration: "gen-1",
				RecipeDigest: "recipe-digest", PlanDigest: "plan-digest",
				SchemaDigest: "schema-digest", ScopeDigest: "scope-digest",
				AuthScopeMode:     authscope.ReadScopeRestricted,
				AuthResourcePaths: []string{"/Programs/ab05"},
				ScopeMode:         chartifact.ScopeModeRows,
			},
			Columns: []chartifact.Column{
				{ID: "loom:row_id", Name: "__loom_row_id", SemanticPath: "loom:row_id",
					LogicalType: "string", ClickHouseType: "String", Internal: true},
			},
			PhysicalTable: "loom_private_stage_ab05",
			State:         chartifact.StateCreating,
			LeaseOwner:    owner,
			LeaseUntil:    now.Add(time.Hour),
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := catalog.Create(ctx, manifest); err != nil {
			t.Fatalf("create private artifact: %v", err)
		}
		renewed, err := catalog.Renew(ctx, artifactID, owner, now.Add(2*time.Hour))
		if err != nil || !renewed {
			t.Fatalf("renew private artifact lease = (%v, %v), want renewed", renewed, err)
		}
	})

	t.Run("recipe chain registration", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "op_chains")
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesRecipeChains, schema); err != nil {
			t.Fatalf("bootstrap recipe chains: %v", err)
		}
		registry, err := recipepg.New(admin, schema)
		if err != nil {
			t.Fatalf("recipe registry: %v", err)
		}
		bundle := recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "ab05-recipe",
			TranslationVersion:  "v1",
			Outputs: []recipe.Output{{
				Name: "DocumentReference", RootResourceType: "DocumentReference",
				RowGrain: "document_reference",
			}},
		}
		if _, err := registry.Register(ctx, "verify-ab05", bundle, ""); err != nil {
			t.Fatalf("register revision: %v", err)
		}
		list, err := registry.List(ctx, "verify-ab05", "ab05-recipe")
		if err != nil {
			t.Fatalf("list revisions: %v", err)
		}
		if len(list) != 1 || list[0].Bundle.Name != "ab05-recipe" {
			t.Fatalf("revision list = %+v, want the one registered revision", list)
		}
	})

	t.Run("recipe exec registry registration", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "op_exec")
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesRecipeExecRegistries, schema); err != nil {
			t.Fatalf("bootstrap recipe exec: %v", err)
		}
		registry, err := recipeexecpg.New(admin, schema)
		if err != nil {
			t.Fatalf("exec registry: %v", err)
		}
		bundle := recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "ab05-exec-recipe",
			TranslationVersion:  "v1",
			Outputs: []recipe.Output{{
				Name: "DocumentReference", RootResourceType: "DocumentReference",
				RowGrain: "document_reference",
			}},
		}
		canonical, err := bundle.CanonicalBundle()
		if err != nil {
			t.Fatalf("canonicalize: %v", err)
		}
		digest, err := canonical.Digest()
		if err != nil {
			t.Fatalf("digest: %v", err)
		}
		entry := recipeexec.Entry{Bundle: canonical, Digest: digest}
		if err := registry.SaveRecipe(ctx, entry); err != nil {
			t.Fatalf("save recipe: %v", err)
		}
		loaded, err := registry.LoadRecipeVersion(ctx, "ab05-exec-recipe", "v1")
		if err != nil {
			t.Fatalf("load recipe version: %v", err)
		}
		if loaded.Digest != digest {
			t.Fatalf("loaded digest %s != saved %s", loaded.Digest, digest)
		}
	})

	t.Run("dataset release lane typed read", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "op_release")
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesDatasetRelease, schema); err != nil {
			t.Fatalf("bootstrap dataset release: %v", err)
		}
		releases, err := datasetpg.NewReleaseStore(client, schema)
		if err != nil {
			t.Fatalf("release store: %v", err)
		}
		if _, err := releases.ReadActiveRelease(ctx, "verify-ab05"); !errors.Is(err, dataset.ErrNoActiveRelease) {
			t.Fatalf("ReadActiveRelease on a fresh schema = %v, want the typed ErrNoActiveRelease "+
				"(a real query against the version-8 tables, not a table-missing failure)", err)
		}
	})
}

// ---------------------------------------------------------------------------
// PostgreSQL: the runner refuses histories ValidateAssignment already refused
// ---------------------------------------------------------------------------

func ab05IncompatibleHistoriesRefused(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin := ab05Admin(t, ctx)
	client := ab05Client(t, ctx)

	t.Run("explorer history onto a source schema", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "clash_src_exp")
		sourceManifest, _ := pgstore.LookupManifest(pgstore.SeriesSource)
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesSource, schema); err != nil {
			t.Fatalf("bootstrap source: %v", err)
		}
		before := ab05ReadLedger(t, ctx, admin, schema)
		_, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesExplorerControl, schema)
		if err == nil {
			t.Fatal("explorer history was accepted onto a source-leased schema")
		}
		if !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
			t.Fatalf("explorer onto source = %v, want ErrMigrationChecksumMismatch at shared version 1", err)
		}
		if ab05RegclassExists(t, ctx, admin, schema, explorerpg.OwnerTable) {
			t.Fatal("explorer signature table exists after a refused migration: the failure mutated state")
		}
		ab05ledgerEqual(t, schema, ab05ReadLedger(t, ctx, admin, schema), sourceManifest.Entries)
		_ = before
	})

	t.Run("dataset history onto a publication schema", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "clash_pub_rel")
		pubManifest, _ := pgstore.LookupManifest(pgstore.SeriesPublicationControl)
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesPublicationControl, schema); err != nil {
			t.Fatalf("bootstrap publication: %v", err)
		}
		_, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesDatasetRelease, schema)
		if err == nil {
			t.Fatal("dataset release history was accepted onto a publication-control schema")
		}
		if !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) &&
			!errors.Is(err, pgstore.ErrFutureSchemaVersion) &&
			!errors.Is(err, pgstore.ErrMigrationMissing) {
			t.Fatalf("dataset onto publication = %v, want a typed ledger-history refusal", err)
		}
		if ab05RegclassExists(t, ctx, admin, schema, datasetpg.ProjectReleasesTable) {
			t.Fatal("project_release exists after a refused migration: the failure mutated state")
		}
		ab05ledgerEqual(t, schema, ab05ReadLedger(t, ctx, admin, schema), pubManifest.Entries)
	})

	t.Run("source history onto an explorer schema", func(t *testing.T) {
		schema := ab05Schema(t, ctx, admin, "clash_exp_src")
		expManifest, _ := pgstore.LookupManifest(pgstore.SeriesExplorerControl)
		if _, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesExplorerControl, schema); err != nil {
			t.Fatalf("bootstrap explorer: %v", err)
		}
		_, err := ab05OwnerMigrate(t, ctx, client, admin, pgstore.SeriesSource, schema)
		if err == nil {
			t.Fatal("source history was accepted onto an explorer-control schema")
		}
		if !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
			t.Fatalf("source onto explorer = %v, want ErrMigrationChecksumMismatch", err)
		}
		if ab05RegclassExists(t, ctx, admin, schema, "fhir_resource") {
			t.Fatal("fhir_resource exists after a refused migration: the failure mutated state")
		}
		ab05ledgerEqual(t, schema, ab05ReadLedger(t, ctx, admin, schema), expManifest.Entries)
	})
}

// ab05AssertUpgradeLedger discharges the ledger-preservation half of
// conditions C2/C3: every seeded ledger row must survive an owner upgrade
// byte-identical (the upgrade never re-runs or rewrites applied rows), and
// the rows the upgrade adds must be exactly wantAdded, ascending.
func ab05AssertUpgradeLedger(t *testing.T, schema string, seeded, after []pgstore.LedgerRecord, wantAdded []pgstore.HistoryEntry) {
	t.Helper()
	seededVersions := make(map[int64]bool, len(seeded))
	for _, r := range seeded {
		seededVersions[r.Version] = true
	}
	shared := make([]pgstore.LedgerRecord, 0, len(seeded))
	added := make([]pgstore.LedgerRecord, 0, len(after))
	for _, r := range after {
		if seededVersions[r.Version] {
			shared = append(shared, r)
		} else {
			added = append(added, r)
		}
	}
	if !reflect.DeepEqual(shared, seeded) {
		t.Fatalf("ledger of %s: rows present before the upgrade were rewritten or reordered by it (no-rerun rule): got %v, seeded %v", schema, shared, seeded)
	}
	if len(added) != len(wantAdded) {
		t.Fatalf("ledger of %s: upgrade added %d rows, want exactly %d (%v)", schema, len(added), len(wantAdded), wantAdded)
	}
	for i := range added {
		if added[i].Version != wantAdded[i].Version || added[i].Name != wantAdded[i].Name || added[i].Checksum != wantAdded[i].Checksum {
			t.Fatalf("ledger of %s: added row %d = %d/%s/%s, want %d/%s/%s",
				schema, i, added[i].Version, added[i].Name, added[i].Checksum[:12],
				wantAdded[i].Version, wantAdded[i].Name, wantAdded[i].Checksum[:12])
		}
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL: a schema seeded from the frozen pre-refactor bytes upgrades
// to the owner history with the ledger unchanged (condition C2 for all
// three test-composed series S2/S3/S4, with the S2 ledger-preservation
// witness of condition C3)
// ---------------------------------------------------------------------------

func ab05PreRefactorUpgradeConverges(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin := ab05Admin(t, ctx)
	client := ab05Client(t, ctx)

	// loadPreRefactor reads the frozen snapshot directory through the real
	// runner loader, exactly as the pre-refactor fixture suites did.
	loadPreRefactor := func(t *testing.T, dir string) []pgstore.Migration {
		t.Helper()
		migrations, err := pgstore.LoadMigrations(os.DirFS(preRefactorRoot), dir)
		if err != nil {
			t.Fatalf("load pre-refactor %s: %v", dir, err)
		}
		return migrations
	}
	seed := func(t *testing.T, name string, history []pgstore.Migration) (string, []pgstore.LedgerRecord) {
		t.Helper()
		sort.Slice(history, func(i, j int) bool { return history[i].Version < history[j].Version })
		schema := ab05Schema(t, ctx, admin, name)
		runner, err := pgstore.NewMigrationRunner(client, schema, history)
		if err != nil {
			t.Fatalf("seed runner %s: %v", name, err)
		}
		result, err := runner.Migrate(ctx)
		if err != nil {
			t.Fatalf("seed migrate %s: %v", name, err)
		}
		return schema, result.Applied
	}
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

	t.Run("ExplorerDeployedSchemaRefusesRetroactive478", func(t *testing.T) {
		// Condition C3, runtime law half. The pre-refactor DEPLOYED
		// explorer history was the old owner embed: 1,2,3,5,6,9,10,11 --
		// versions 4,7,8 existed only as test fixtures, exactly the
		// ownership gap this ticket closes. The shared runner refuses to
		// apply any pending version below the highest recorded one
		// ("add a new version instead of editing history"), so an
		// already-deployed 11-maximum schema can NEVER retroactively gain
		// 4/7/8: history editing is impossible, which is the manifest's
		// immutability clause enforced by the runner. This refusal is
		// DELIBERATE DESIGN, not a deferred fix -- an upgrade that
		// silently filled version holes would be the bug. No production
		// gap-schema exists today: S2/S3/S4 (explorer_control,
		// publication_control, dataset_release) have no production
		// startup callers yet, so no deployed ledger can sit at version
		// 11 without 4/7/8 (the source series' only production migrator
		// is generation load, and its history has never had gaps). If a
		// future deployment ever reports this typed error, it is the
		// actionable signal that its schema was assembled by a non-owner
		// path, and the OutOfOrderMigrationError (pending version vs
		// recorded max) names the exact disagreement. The supported
		// upgrade witness for the pre-refactor state is the adjacent
		// fixture-composition subtest.
		owner, err := explorerpg.OwnerMigrations()
		if err != nil {
			t.Fatalf("owner history: %v", err)
		}
		legacy := make([]pgstore.Migration, 0, len(owner))
		moved := map[int64]bool{4: true, 7: true, 8: true}
		for _, m := range owner {
			if !moved[m.Version] {
				legacy = append(legacy, m)
			}
		}
		schema, seeded := seed(t, "up_explorer_refuse", legacy)
		if len(seeded) != len(owner)-3 {
			t.Fatalf("seeded explorer ledger holds %d rows, want %d (pre-refactor deployed composition)", len(seeded), len(owner)-3)
		}
		store, err := explorerpg.New(client, schema)
		if err != nil {
			t.Fatalf("explorer store: %v", err)
		}
		e := explorer.Explorer{
			Project: "verify-ab05", ExplorerID: "ab05-refuse", Title: "Refusal Survivor",
			ManagementMode:                explorer.ManagementRepository,
			DraftConfig:                   json.RawMessage(`{"panels":[]}`),
			DraftVersion:                  1,
			DraftDigest:                   "sha256:ab05-refuse",
			DraftSnapshotToken:            "snap-r",
			DraftSourceGeneration:         "gen-1",
			DraftAuthorizationScopeDigest: "authz-r",
			LastAuthoringCommandID:        "cmd-r",
			LastAuthoringCommandDigest:    "sha256:cmd-r",
			LastAuthoringCommandResults:   json.RawMessage(`{"ok":true}`),
			UpdatedBy:                     "ab05@calypr",
			UpdatedAt:                     now,
		}
		if _, err := store.Create(ctx, e); err != nil {
			t.Fatalf("create explorer before refusal attempt: %v", err)
		}
		_, err = explorerpg.MigrateSchema(ctx, client, schema)
		if !errors.Is(err, pgstore.ErrMigrationOutOfOrder) {
			t.Fatalf("retroactive gap-fill was not refused with ErrMigrationOutOfOrder: %v", err)
		}
		var outOfOrder *pgstore.OutOfOrderMigrationError
		if !errors.As(err, &outOfOrder) || outOfOrder.Version != 4 || outOfOrder.LatestRecord != 11 {
			t.Fatalf("refusal = %+v, want pending version 4 against recorded max 11", outOfOrder)
		}
		if after := ab05ReadLedger(t, ctx, admin, schema); !reflect.DeepEqual(after, seeded) {
			t.Fatalf("the refused retroactive migration mutated the deployed ledger: seeded %v, after %v", seeded, after)
		}
		if got, err := store.Get(ctx, e.Project, e.ExplorerID); err != nil || got.Title != "Refusal Survivor" {
			t.Fatalf("explorer row did not survive the refusal: %+v, %v", got, err)
		}
	})

	t.Run("ExplorerControlPlanePreRefactorFixtureCompositionUpgrade", func(t *testing.T) {
		// Condition C3, no-rerun half: the schema as the pre-refactor
		// suites actually deployed it -- the old owner embed plus the
		// three fixture migrations loaded from the frozen snapshots --
		// upgrades through the new owner entry point to a ledger-identical
		// no-op: every applied row (including 4/7/8) keeps its exact
		// (version, name, checksum) identity, the added set is empty, and
		// a durable row survives.
		owner, err := explorerpg.OwnerMigrations()
		if err != nil {
			t.Fatalf("owner history: %v", err)
		}
		legacy := make([]pgstore.Migration, 0, len(owner))
		moved := map[int64]bool{4: true, 7: true, 8: true}
		for _, m := range owner {
			if !moved[m.Version] {
				legacy = append(legacy, m)
			}
		}
		legacy = append(legacy, loadPreRefactor(t, "tests/migrations-capabilities")...)
		legacy = append(legacy, loadPreRefactor(t, "tests/migrations-interpretations")...)
		legacy = append(legacy, loadPreRefactor(t, "tests/migrations-selections")...)
		schema, seeded := seed(t, "up_explorer", legacy)
		expManifest, _ := pgstore.LookupManifest(pgstore.SeriesExplorerControl)
		ab05ledgerEqual(t, schema, seeded, expManifest.Entries)
		// The fixture-supplied 4/7/8 ledger rows equal the sha256 of the
		// frozen pre-refactor snapshot bytes: what the fixtures applied
		// then is what the owner embeds now, byte for byte.
		for _, movedFile := range ab05MovedFiles {
			if movedFile.series != pgstore.SeriesExplorerControl {
				continue
			}
			disk, err := os.ReadFile(filepath.Join(preRefactorRoot, filepath.FromSlash(movedFile.preRefactor)))
			if err != nil {
				t.Fatalf("pre-refactor snapshot %s: %v", movedFile.preRefactor, err)
			}
			sum := sha256.Sum256(disk)
			var row *pgstore.LedgerRecord
			for i := range seeded {
				if seeded[i].Version == movedFile.version {
					row = &seeded[i]
				}
			}
			if row == nil || row.Name != movedFile.name || row.Checksum != hex.EncodeToString(sum[:]) {
				t.Fatalf("seeded ledger row for %s does not equal the frozen snapshot identity", movedFile.preRefactor)
			}
		}
		store, err := explorerpg.New(client, schema)
		if err != nil {
			t.Fatalf("explorer store: %v", err)
		}
		e := explorer.Explorer{
			Project: "verify-ab05", ExplorerID: "ab05-upgrade", Title: "Upgrade Survivor",
			ManagementMode:                explorer.ManagementRepository,
			DraftConfig:                   json.RawMessage(`{"panels":[]}`),
			DraftVersion:                  1,
			DraftDigest:                   "sha256:ab05-up",
			DraftSnapshotToken:            "snap-up",
			DraftSourceGeneration:         "gen-1",
			DraftAuthorizationScopeDigest: "authz-up",
			LastAuthoringCommandID:        "cmd-up",
			LastAuthoringCommandDigest:    "sha256:cmd-up",
			LastAuthoringCommandResults:   json.RawMessage(`{"ok":true}`),
			UpdatedBy:                     "ab05@calypr",
			UpdatedAt:                     now,
		}
		if _, err := store.Create(ctx, e); err != nil {
			t.Fatalf("create explorer before upgrade: %v", err)
		}
		after, err := explorerpg.MigrateSchema(ctx, client, schema)
		if err != nil {
			t.Fatalf("owner upgrade of a fixture-seeded explorer schema: %v", err)
		}
		ab05ledgerEqual(t, schema, after.Applied, expManifest.Entries)
		// The composition already carries the full owner history: the
		// upgrade must add nothing and rewrite nothing -- no applied row
		// is ever re-run.
		ab05AssertUpgradeLedger(t, schema, seeded, after.Applied, nil)
		if got, err := store.Get(ctx, e.Project, e.ExplorerID); err != nil || got.Title != "Upgrade Survivor" {
			t.Fatalf("explorer row did not survive the upgrade: %+v, %v", got, err)
		}
		// Double-apply through the owner entry point is a ledger-identical
		// no-op.
		twice, err := explorerpg.MigrateSchema(ctx, client, schema)
		if err != nil {
			t.Fatalf("owner double-apply of explorer schema: %v", err)
		}
		if !reflect.DeepEqual(twice.Applied, after.Applied) {
			t.Fatal("explorer double-apply through the owner drifted the ledger")
		}
	})

	t.Run("PublicationControlPlaneUpgrade", func(t *testing.T) {
		source, err := pgstore.EmbeddedMigrations()
		if err != nil {
			t.Fatalf("source history: %v", err)
		}
		legacy := []pgstore.Migration{source[0]}
		for _, dir := range []string{
			"tests/migrations", "tests/migrations-leases",
			"tests/migrations-chartifact", "tests/migrations-readpins",
		} {
			legacy = append(legacy, loadPreRefactor(t, dir)...)
		}
		schema, seeded := seed(t, "up_pub", legacy)
		pubManifest, _ := pgstore.LookupManifest(pgstore.SeriesPublicationControl)
		ab05ledgerEqual(t, schema, seeded, pubManifest.Entries)
		// Durable rows exist before the upgrade: a lease and a private
		// artifact row, each written through its production store.
		registry, err := publicationpg.New(admin, schema)
		if err != nil {
			t.Fatalf("publication registry: %v", err)
		}
		key := "ab05-up-key"
		held, err := registry.AcquireBundleLease(ctx, key, "ab05-owner", now.Add(time.Hour))
		if err != nil || !held {
			t.Fatalf("pre-upgrade lease = (%v, %v), want held", held, err)
		}
		catalog, err := chartifactpg.NewCatalog(admin, schema)
		if err != nil {
			t.Fatalf("artifact catalog: %v", err)
		}
		artifactID := "ab05-up-artifact"
		manifest := chartifact.Manifest{
			ArtifactID: artifactID,
			Identity: chartifact.Identity{
				ExecutionID: "ab05-up-exec", OutputID: "output-1", StageID: "stage-1",
				Project: "verify-ab05", DatasetGeneration: "gen-1",
				RecipeDigest: "recipe-digest", PlanDigest: "plan-digest",
				SchemaDigest: "schema-digest", ScopeDigest: "scope-digest",
				AuthScopeMode:     authscope.ReadScopeRestricted,
				AuthResourcePaths: []string{"/Programs/ab05"},
				ScopeMode:         chartifact.ScopeModeRows,
			},
			Columns: []chartifact.Column{
				{ID: "loom:row_id", Name: "__loom_row_id", SemanticPath: "loom:row_id",
					LogicalType: "string", ClickHouseType: "String", Internal: true},
			},
			PhysicalTable: "loom_private_stage_ab05_up",
			State:         chartifact.StateCreating,
			LeaseOwner:    "ab05-owner",
			LeaseUntil:    now.Add(time.Hour),
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := catalog.Create(ctx, manifest); err != nil {
			t.Fatalf("pre-upgrade artifact create: %v", err)
		}
		after, err := publicationpg.MigrateSchema(ctx, client, schema)
		if err != nil {
			t.Fatalf("owner upgrade of a fixture-seeded publication schema: %v", err)
		}
		ab05ledgerEqual(t, schema, after.Applied, pubManifest.Entries)
		// The pre-refactor composition already covers the full owner
		// history: the upgrade must add nothing and rewrite nothing.
		ab05AssertUpgradeLedger(t, schema, seeded, after.Applied, nil)
		renewed, err := catalog.Renew(ctx, artifactID, "ab05-owner", now.Add(2*time.Hour))
		if err != nil || !renewed {
			t.Fatalf("private artifact did not survive the upgrade: renew = (%v, %v)", renewed, err)
		}
		if held, err := registry.AcquireBundleLease(ctx, key, "ab05-owner", now.Add(time.Hour)); err != nil || !held {
			t.Fatalf("lease did not survive the upgrade: reacquire = (%v, %v)", held, err)
		}
		twice, err := publicationpg.MigrateSchema(ctx, client, schema)
		if err != nil {
			t.Fatalf("owner double-apply of publication schema: %v", err)
		}
		if !reflect.DeepEqual(twice.Applied, after.Applied) {
			t.Fatal("publication double-apply through the owner drifted the ledger")
		}
	})

	t.Run("DatasetReleaseLaneUpgrade", func(t *testing.T) {
		source, err := pgstore.EmbeddedMigrations()
		if err != nil {
			t.Fatalf("source history: %v", err)
		}
		legacy := []pgstore.Migration{source[0], source[2]}
		legacy = append(legacy, loadPreRefactor(t,
			"internal/dataset/postgres/testdata/migrations-releases")...)
		schema, seeded := seed(t, "up_rel", legacy)
		relManifest, _ := pgstore.LookupManifest(pgstore.SeriesDatasetRelease)
		ab05ledgerEqual(t, schema, seeded, relManifest.Entries)
		// A staged generation manifest is persisted through the production
		// dataset store before the upgrade.
		gen, err := datasetpg.New(client, schema)
		if err != nil {
			t.Fatalf("dataset store: %v", err)
		}
		ref, err := dataset.NewRef("verify-ab05", "gen-ab05-up")
		if err != nil {
			t.Fatalf("NewRef: %v", err)
		}
		snapshot, err := dataset.NewSchemaSnapshot("urn:ab05-gen-ab05-up", "R5", strings.Repeat("0123456789abcdef", 4), []string{"Observation", "Patient"})
		if err != nil {
			t.Fatalf("NewSchemaSnapshot: %v", err)
		}
		loading, err := dataset.NewManifest(ref, snapshot)
		if err != nil {
			t.Fatalf("NewManifest: %v", err)
		}
		if _, err := gen.CreateManifest(ctx, loading); err != nil {
			t.Fatalf("CreateManifest before upgrade: %v", err)
		}
		if _, err := gen.TransitionManifest(ctx, loading, dataset.StateStaged); err != nil {
			t.Fatalf("TransitionManifest before upgrade: %v", err)
		}
		after, err := datasetpg.MigrateSchema(ctx, client, schema)
		if err != nil {
			t.Fatalf("owner upgrade of a fixture-seeded release schema: %v", err)
		}
		ab05ledgerEqual(t, schema, after.Applied, relManifest.Entries)
		ab05AssertUpgradeLedger(t, schema, seeded, after.Applied, nil)
		staged, err := gen.ReadManifest(ctx, ref)
		if err != nil || staged.State != dataset.StateStaged {
			t.Fatalf("staged generation manifest did not survive the upgrade: %+v, %v", staged, err)
		}
		twice, err := datasetpg.MigrateSchema(ctx, client, schema)
		if err != nil {
			t.Fatalf("owner double-apply of release schema: %v", err)
		}
		if !reflect.DeepEqual(twice.Applied, after.Applied) {
			t.Fatal("release double-apply through the owner drifted the ledger")
		}
	})
}

// ---------------------------------------------------------------------------
// zero-survivor sweep
// ---------------------------------------------------------------------------

func ab05ZeroSurvivorSweep(t *testing.T) {
	t.Helper()
	if os.Getenv("LOOM_TEST_POSTGRES_URL") == "" {
		t.Skip("no PostgreSQL in this environment; nothing was created to sweep")
	}
	ctx := context.Background()
	admin := ab05Admin(t, ctx)
	rows, err := admin.Query(ctx,
		"SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify_ab05\\_%'")
	if err != nil {
		t.Fatalf("sweep scan: %v", err)
	}
	var leftovers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("sweep scan row: %v", err)
		}
		leftovers = append(leftovers, name)
	}
	rows.Close()
	for _, name := range leftovers {
		// Defensive, never silent: drop, then fail so the leak is loud.
		_, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgxIdentifier(name)+" CASCADE")
	}
	if len(leftovers) > 0 {
		t.Fatalf("zero-survivor sweep found leaked schemas: %v", leftovers)
	}
}

// ---------------------------------------------------------------------------
// the single named acceptance test
// ---------------------------------------------------------------------------

func TestPostgresMigrationOwnershipContract(t *testing.T) {
	// Offline record integrity runs always, with no database present.
	t.Run("ManifestMatchesEmbeddedOwners", ab05ManifestMatchesOwners)
	t.Run("ManifestChecksumsNonCircular", ab05ManifestChecksumsNonCircular)
	t.Run("ManifestRecordInvariants", ab05ManifestRecordInvariants)
	t.Run("PreRefactorBytesFrozenInOwners", ab05PreRefactorBytesFrozen)
	t.Run("SharedBootstrapBytesIdentical", ab05SharedBootstrapBytesIdentical)
	t.Run("ValidateAssignmentMatrix", ab05AssignmentValidation)

	// PostgreSQL halves: the runner lane asserts this suite runs with zero
	// skips against the CI PostgreSQL-18 service.
	t.Run("FreshBootstrapsConverge", ab05FreshBootstrapsConverge)
	t.Run("RepresentativeDomainOperations", ab05RepresentativeDomainOperations)
	t.Run("IncompatibleHistoriesRefused", ab05IncompatibleHistoriesRefused)
	t.Run("PreRefactorUpgradeConverges", ab05PreRefactorUpgradeConverges)
	t.Run("TestZZZeroSurvivorSweep", ab05ZeroSurvivorSweep)
}
