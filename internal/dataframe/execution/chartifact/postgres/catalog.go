// Package postgres implements the PostgreSQL-side catalog for temporary
// private ClickHouse artifact manifests and their cleanup leases
// (pg-private-clickhouse-artifact-catalog-v1).
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication row store. This catalog persists the chartifact.Manifest
// metadata and lease state only: the physical tables it describes live in
// ClickHouse, this package never reads, writes, or drops ClickHouse data,
// and it never falls back to another backend on its own -- every failure is
// returned for the caller to surface. It is deliberately not the published
// BundleCatalog (internal/dataframe/publication/postgres) and not the
// Explorer filesystem ZIP ArtifactStore: private artifacts are never
// registered as published outputs and never advance a visibility pointer.
//
// Guarantees, mirroring internal/dataframe/execution/chartifact/arango:
//   - Create is conditional on the ArtifactID being absent (ON CONFLICT DO
//     NOTHING): a concurrent duplicate reports ErrArtifactExists and the
//     stored manifest is never replaced, so Create is immutable.
//   - Update and Renew compare the current lease owner and the current
//     expiry inside the single write statement; an expired or stolen lease
//     cannot be updated (ErrLeaseLost) and Renew never resurrects an
//     expired lease. Active-state updates extend the lease monotonically
//     (GREATEST); only CLEANUP_PENDING may make a lease immediately
//     reclaimable, and CLEANUP_PENDING is terminal -- an owned row cannot
//     return to an active state. The identity, schema, and physical-table
//     columns and their payload members are never in any UPDATE SET list,
//     so progress writes preserve them structurally.
//   - ListExpired is bounded (limit <= 0 empty, capped at maxExpiredPageSize)
//     and stable: the page walks lease order with a COLLATE "C" artifact-ID
//     tie-break, the deterministic byte-order tie Arango's _key gives.
//   - ClaimCleanup rechecks lease_until <= expiredBefore inside its UPDATE,
//     so a concurrent renewer wins cleanly and only one cleanup owner ever
//     claims a manifest; the claimed manifest is returned from the same
//     statement.
//   - Delete and ReleaseCleanup act only while the caller is the current
//     cleanup-pending owner, otherwise ErrLeaseLost. Every catalog operation
//     is one statement on one connection: no catalog method opens a
//     transaction, so the caller's ClickHouse DropTable (idempotent per the
//     chartifact.ClickHouse contract) always runs outside any database
//     transaction, and there is no cross-database atomicity anywhere.
//   - The jsonb payload is authoritative for reads: nanosecond timestamps
//     and the missing-versus-JSON-null member shape (for example a nil
//     versus empty AuthResourcePaths) round-trip untouched. Every column is
//     NOT NULL, so nothing silently reads back as SQL NULL; unsupported
//     values fail explicitly, at the boundary or on decode.
//
// The schema is created by tests/migrations-chartifact/000007_private_
// clickhouse_artifacts.sql applied through the pg-store-02 MigrationRunner;
// this package contains no DDL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
)

// PrivateArtifactsTable stores one row per private ClickHouse artifact
// manifest. It is created by
// tests/migrations-chartifact/000007_private_clickhouse_artifacts.sql; this
// file contains no DDL.
const PrivateArtifactsTable = "chartifact_private_artifacts"

const (
	cleanupPendingState = string(chartifact.StateCleanupPending)
	maxExpiredPageSize  = 1000
)

// schemaNamePattern matches the migration runner's own schema rule so a
// catalog can only ever address a safe lowercase identifier schema.
var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ErrInvalidSchema reports a schema name that is not a safe lowercase
// identifier; the catalog refuses to build rather than interpolate it.
var ErrInvalidSchema = errors.New("chartifact postgres: invalid schema name")

// ErrArtifactExists reports a Create whose ArtifactID is already stored. The
// existing manifest is untouched: Create never replaces an immutable
// manifest, and concurrent duplicate creates have exactly one winner.
var ErrArtifactExists = errors.New("chartifact postgres: artifact manifest already exists")

// DB is the statement surface the catalog needs. *pgxpool.Pool satisfies it;
// the catalog owns no connection beyond what a call acquires. Deliberately
// absent: Begin. No catalog operation may hold a transaction, so the
// caller's idempotent ClickHouse DropTable always runs outside any database
// transaction and no statement can block a drop behind a held lock.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Catalog implements chartifact.Catalog on PostgreSQL 18. All statements use
// ordered $n parameters and schema-qualified table names; nothing
// user-supplied is interpolated into SQL.
type Catalog struct {
	db        DB
	artifacts string
}

var _ chartifact.Catalog = (*Catalog)(nil)

// NewCatalog binds a catalog to one schema of a PostgreSQL 18 database
// served by pool. The schema must already contain chartifact_private_
// artifacts (tests/migrations-chartifact/000007 via the pg-store-02
// MigrationRunner); NewCatalog never runs DDL and never falls back when it
// is missing -- the first statement surfaces the failure.
func NewCatalog(pool DB, schema string) (*Catalog, error) {
	if pool == nil {
		return nil, errors.New("chartifact postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchema, schema)
	}
	return &Catalog{
		db:        pool,
		artifacts: pgx.Identifier{schema, PrivateArtifactsTable}.Sanitize(),
	}, nil
}

// manifestParams are the ordered $1..$19 bind values for a full row write:
// identity, the mirrored scan columns, and the marshaled payload. The
// payload binds as text so PostgreSQL infers jsonb from the column context;
// []byte would be typed bytea and rejected.
func manifestParams(manifest chartifact.Manifest) ([]any, error) {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("chartifact postgres: marshal artifact manifest: %w", err)
	}
	return []any{
		manifest.ArtifactID, manifest.PhysicalTable, string(manifest.State),
		manifest.RowCount, manifest.ByteCount, manifest.LeaseOwner,
		manifest.LeaseUntil, manifest.CreatedAt, manifest.UpdatedAt,
		manifest.Identity.ExecutionID, manifest.Identity.OutputID, manifest.Identity.StageID,
		manifest.Identity.Project, manifest.Identity.DatasetGeneration,
		string(manifest.Identity.AuthScopeMode), manifest.Identity.ScopeMode,
		manifest.Identity.SchemaDigest, manifest.Identity.ScopeDigest,
		string(payload),
	}, nil
}

// Create inserts an immutable manifest only when its ArtifactID is unused.
// A concurrent duplicate create converges on the winner: the loser reports
// ErrArtifactExists and the stored manifest keeps its exact bytes.
func (c *Catalog) Create(ctx context.Context, manifest chartifact.Manifest) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.ArtifactID) == "" || manifest.ArtifactID != strings.TrimSpace(manifest.ArtifactID) {
		return fmt.Errorf("chartifact postgres: manifest requires a trimmed artifact ID")
	}
	if err := validateProgressFields(chartifact.Progress{
		State: manifest.State, RowCount: manifest.RowCount, ByteCount: manifest.ByteCount,
	}); err != nil {
		return fmt.Errorf("chartifact postgres: create manifest %q: %w", manifest.ArtifactID, err)
	}
	params, err := manifestParams(manifest)
	if err != nil {
		return err
	}
	tag, err := c.db.Exec(ctx,
		"INSERT INTO "+c.artifacts+" (artifact_id, physical_table, state, row_count, byte_count,"+
			" lease_owner, lease_until, created_at, updated_at, execution_id, output_id, stage_id,"+
			" project, dataset_generation, auth_scope_mode, scope_mode, schema_digest, scope_digest, payload)"+
			" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)"+
			" ON CONFLICT (artifact_id) DO NOTHING",
		params...)
	if err != nil {
		return fmt.Errorf("chartifact postgres: insert artifact manifest %q: %w", manifest.ArtifactID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: artifact manifest %q", ErrArtifactExists, manifest.ArtifactID)
	}
	return nil
}

// Update changes only progress fields, and only while owner holds an
// unexpired lease. Active states extend the lease monotonically and can
// never shorten it; CLEANUP_PENDING may make the table immediately
// reclaimable after the owner has stopped using it and is terminal, so an
// owned cleanup-pending row cannot return to an active state. The identity,
// schema, and physical-table columns and payload members are not in the SET
// list at all, so an update preserves them structurally. An expired or
// stolen lease fails with chartifact.ErrLeaseLost.
func (c *Catalog) Update(ctx context.Context, artifactID, owner string, progress chartifact.Progress) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if err := validateUpdate(artifactID, owner, progress); err != nil {
		return err
	}
	now := time.Now().UTC()
	leaseJSON, err := jsonTime(progress.LeaseUntil)
	if err != nil {
		return fmt.Errorf("chartifact postgres: update artifact %q: %w", artifactID, err)
	}
	updatedJSON, err := jsonTime(progress.UpdatedAt)
	if err != nil {
		return fmt.Errorf("chartifact postgres: update artifact %q: %w", artifactID, err)
	}
	cleanupPending := progress.State == chartifact.StateCleanupPending
	// The payload leaseUntil follows exactly the same three-way choice as
	// the timestamptz column so the authoritative payload and the scan
	// column never disagree about which lease won: cleanup-pending stores
	// the supplied lease directly, otherwise the later of stored and
	// supplied wins.
	tag, err := c.db.Exec(ctx,
		"UPDATE "+c.artifacts+" AS m SET"+
			" state = $3::text, row_count = $4, byte_count = $5,"+
			" lease_until = CASE WHEN $6::boolean THEN $7::timestamptz"+
			"   WHEN m.lease_until > $7::timestamptz THEN m.lease_until ELSE $7::timestamptz END,"+
			" updated_at = $9::timestamptz,"+
			" payload = m.payload || jsonb_build_object("+
			"   'state', $3::text, 'rowCount', $4::bigint, 'byteCount', $5::bigint,"+
			"   'leaseUntil', CASE WHEN $6::boolean THEN $8::jsonb"+
			"     WHEN m.lease_until > $7::timestamptz THEN m.payload -> 'leaseUntil' ELSE $8::jsonb END,"+
			"   'updatedAt', $10::jsonb)"+
			" WHERE m.artifact_id = $1 AND m.lease_owner = $2"+
			"   AND m.lease_until >= $11::timestamptz"+
			"   AND (m.state <> '"+cleanupPendingState+"' OR $3::text = '"+cleanupPendingState+"')",
		artifactID, owner, string(progress.State), progress.RowCount, progress.ByteCount,
		cleanupPending, progress.LeaseUntil, leaseJSON, progress.UpdatedAt, updatedJSON, now)
	if err != nil {
		return fmt.Errorf("chartifact postgres: update artifact manifest %q: %w", artifactID, err)
	}
	if tag.RowsAffected() == 0 {
		return chartifact.ErrLeaseLost
	}
	return nil
}

// Renew extends an active, unexpired lease owned by owner. It never shortens
// an existing lease and never resurrects an expired one; CLEANUP_PENDING is
// not renewable, exactly as in the Arango catalog.
func (c *Catalog) Renew(ctx context.Context, artifactID, owner string, until time.Time) (bool, error) {
	if err := requireContext(ctx); err != nil {
		return false, err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" || until.IsZero() {
		return false, fmt.Errorf("chartifact postgres: renewal requires artifact ID, owner, and lease time")
	}
	now := time.Now().UTC()
	untilJSON, err := jsonTime(until)
	if err != nil {
		return false, fmt.Errorf("chartifact postgres: renew artifact %q: %w", artifactID, err)
	}
	nowJSON, err := jsonTime(now)
	if err != nil {
		return false, fmt.Errorf("chartifact postgres: renew artifact %q: %w", artifactID, err)
	}
	tag, err := c.db.Exec(ctx,
		"UPDATE "+c.artifacts+" AS m SET"+
			" lease_until = CASE WHEN m.lease_until > $3::timestamptz THEN m.lease_until ELSE $3::timestamptz END,"+
			" updated_at = $5::timestamptz,"+
			" payload = m.payload || jsonb_build_object("+
			"   'leaseUntil', CASE WHEN m.lease_until > $3::timestamptz THEN m.payload -> 'leaseUntil' ELSE $4::jsonb END,"+
			"   'updatedAt', $6::jsonb)"+
			" WHERE m.artifact_id = $1 AND m.lease_owner = $2"+
			"   AND m.state IN ('CREATING', 'WRITING', 'READY')"+
			"   AND m.lease_until >= $7::timestamptz",
		artifactID, owner, until, untilJSON, now, nowJSON, now)
	if err != nil {
		return false, fmt.Errorf("chartifact postgres: renew artifact lease %q: %w", artifactID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListExpired returns a bounded, stable-order page of manifests whose lease
// is expired at or before expiredBefore: ascending lease with a byte-order
// (COLLATE "C") artifact-ID tie-break, so repeated reconciler passes see
// the same prefix. ClaimCleanup rechecks this condition inside its write.
func (c *Catalog) ListExpired(ctx context.Context, expiredBefore time.Time, limit int) ([]chartifact.Manifest, error) {
	if err := requireContext(ctx); err != nil {
		return nil, err
	}
	if expiredBefore.IsZero() {
		return nil, fmt.Errorf("chartifact postgres: artifact expiry cutoff is required")
	}
	if limit <= 0 {
		return []chartifact.Manifest{}, nil
	}
	if limit > maxExpiredPageSize {
		limit = maxExpiredPageSize
	}
	rows, err := c.db.Query(ctx,
		"SELECT payload FROM "+c.artifacts+
			" WHERE lease_until <= $1::timestamptz"+
			" ORDER BY lease_until ASC, artifact_id COLLATE \"C\" ASC LIMIT $2",
		expiredBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("chartifact postgres: list expired artifacts: %w", err)
	}
	defer rows.Close()
	manifests := make([]chartifact.Manifest, 0, limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("chartifact postgres: read expired artifact row: %w", err)
		}
		manifest, err := decodeManifest(payload)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chartifact postgres: list expired artifacts: %w", err)
	}
	return manifests, nil
}

// ClaimCleanup atomically changes an expired manifest's owner, state, and
// cleanup lease. The expiry is rechecked inside the UPDATE, so a concurrent
// renewer wins cleanly and only one cleanup owner can claim a given expired
// manifest; the loser rechecks against the committed lease and stands down.
func (c *Catalog) ClaimCleanup(ctx context.Context, artifactID, owner string, expiredBefore, until time.Time) (chartifact.Manifest, bool, error) {
	if err := requireContext(ctx); err != nil {
		return chartifact.Manifest{}, false, err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" || expiredBefore.IsZero() || !until.After(expiredBefore) {
		return chartifact.Manifest{}, false, fmt.Errorf("chartifact postgres: cleanup claim requires ID, owner, and a future cleanup lease")
	}
	untilJSON, err := jsonTime(until)
	if err != nil {
		return chartifact.Manifest{}, false, fmt.Errorf("chartifact postgres: claim artifact %q cleanup: %w", artifactID, err)
	}
	now := time.Now().UTC()
	nowJSON, err := jsonTime(now)
	if err != nil {
		return chartifact.Manifest{}, false, fmt.Errorf("chartifact postgres: claim artifact %q cleanup: %w", artifactID, err)
	}
	var payload []byte
	err = c.db.QueryRow(ctx,
		"UPDATE "+c.artifacts+" AS m SET"+
			" state = '"+cleanupPendingState+"', lease_owner = $3::text,"+
			" lease_until = $4::timestamptz, updated_at = $6::timestamptz,"+
			" payload = m.payload || jsonb_build_object("+
			"   'state', '"+cleanupPendingState+"', 'leaseOwner', $3::text,"+
			"   'leaseUntil', $5::jsonb, 'updatedAt', $7::jsonb)"+
			" WHERE m.artifact_id = $1 AND m.lease_until <= $2::timestamptz"+
			" RETURNING payload",
		artifactID, expiredBefore, owner, until, untilJSON, now, nowJSON).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return chartifact.Manifest{}, false, nil
	}
	if err != nil {
		return chartifact.Manifest{}, false, fmt.Errorf("chartifact postgres: claim artifact %q cleanup: %w", artifactID, err)
	}
	manifest, err := decodeManifest(payload)
	if err != nil {
		return chartifact.Manifest{}, false, err
	}
	return manifest, true, nil
}

// Delete removes a cleanup-pending manifest only for its current cleanup
// owner. The caller drops the ClickHouse table first (idempotently, outside
// any transaction -- this catalog never opens one), so a lost owner can
// never delete a manifest another replica is cleaning up.
func (c *Catalog) Delete(ctx context.Context, artifactID, owner string) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" {
		return fmt.Errorf("chartifact postgres: deletion requires artifact ID and owner")
	}
	tag, err := c.db.Exec(ctx,
		"DELETE FROM "+c.artifacts+
			" WHERE artifact_id = $1 AND lease_owner = $2 AND state = '"+cleanupPendingState+"'",
		artifactID, owner)
	if err != nil {
		return fmt.Errorf("chartifact postgres: delete artifact manifest %q: %w", artifactID, err)
	}
	if tag.RowsAffected() == 0 {
		return chartifact.ErrLeaseLost
	}
	return nil
}

// ReleaseCleanup makes a failed cleanup immediately reclaimable, but only
// while the caller still owns the cleanup lease. The stored lease becomes
// the caller-supplied time, so the next expired page includes it.
func (c *Catalog) ReleaseCleanup(ctx context.Context, artifactID, owner string, until time.Time) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" || until.IsZero() {
		return fmt.Errorf("chartifact postgres: cleanup release requires artifact ID, owner, and lease time")
	}
	now := time.Now().UTC()
	untilJSON, err := jsonTime(until)
	if err != nil {
		return fmt.Errorf("chartifact postgres: release artifact %q cleanup: %w", artifactID, err)
	}
	nowJSON, err := jsonTime(now)
	if err != nil {
		return fmt.Errorf("chartifact postgres: release artifact %q cleanup: %w", artifactID, err)
	}
	tag, err := c.db.Exec(ctx,
		"UPDATE "+c.artifacts+" AS m SET"+
			" lease_until = $3::timestamptz, updated_at = $5::timestamptz,"+
			" payload = m.payload || jsonb_build_object('leaseUntil', $4::jsonb, 'updatedAt', $6::jsonb)"+
			" WHERE m.artifact_id = $1 AND m.lease_owner = $2 AND m.state = '"+cleanupPendingState+"'",
		artifactID, owner, until, untilJSON, now, nowJSON)
	if err != nil {
		return fmt.Errorf("chartifact postgres: release artifact cleanup lease %q: %w", artifactID, err)
	}
	if tag.RowsAffected() == 0 {
		return chartifact.ErrLeaseLost
	}
	return nil
}

// jsonTime encodes a manifest timestamp as the exact JSON string Go would
// marshal for it (quoted RFC 3339 with nanoseconds), so payload lease and
// update stamps stay byte-faithful to the Arango catalog's fixed-width
// storage and round-trip losslessly into time.Time.
func jsonTime(value time.Time) (string, error) {
	encoded, err := json.Marshal(value.UTC())
	if err != nil {
		return "", fmt.Errorf("encode artifact timestamp: %w", err)
	}
	return string(encoded), nil
}

// decodeManifest restores the authoritative manifest from its stored payload.
// A payload whose identity or timestamps cannot be read fails explicitly:
// json.Unmarshal silently ignores a JSON null for time.Time, so nullish
// lease/created/updated members are refused here rather than decoding to a
// zero time, and there is no silent reconstruction from scan columns.
func decodeManifest(payload []byte) (chartifact.Manifest, error) {
	var manifest chartifact.Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return chartifact.Manifest{}, fmt.Errorf("chartifact postgres: decode stored artifact manifest: %w", err)
	}
	if strings.TrimSpace(manifest.ArtifactID) == "" {
		return chartifact.Manifest{}, errors.New("chartifact postgres: stored artifact manifest has no artifact ID")
	}
	if manifest.LeaseUntil.IsZero() || manifest.CreatedAt.IsZero() || manifest.UpdatedAt.IsZero() {
		return chartifact.Manifest{}, fmt.Errorf("chartifact postgres: stored artifact manifest %q has a nullish timestamp member", manifest.ArtifactID)
	}
	switch manifest.State {
	case chartifact.StateCreating, chartifact.StateWriting, chartifact.StateReady, chartifact.StateCleanupPending:
	default:
		return chartifact.Manifest{}, fmt.Errorf("chartifact postgres: stored artifact manifest %q has invalid state %q", manifest.ArtifactID, manifest.State)
	}
	return manifest, nil
}

func validateUpdate(artifactID, owner string, progress chartifact.Progress) error {
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" {
		return fmt.Errorf("chartifact postgres: update requires artifact ID and owner")
	}
	if err := validateProgressFields(progress); err != nil {
		return err
	}
	if progress.LeaseUntil.IsZero() || progress.UpdatedAt.IsZero() {
		return fmt.Errorf("chartifact postgres: update requires lease timestamps")
	}
	return nil
}

func validateProgressFields(progress chartifact.Progress) error {
	switch progress.State {
	case chartifact.StateCreating, chartifact.StateWriting, chartifact.StateReady, chartifact.StateCleanupPending:
	default:
		return fmt.Errorf("invalid state %q", progress.State)
	}
	if progress.RowCount < 0 || progress.ByteCount < 0 {
		return fmt.Errorf("counts must be nonnegative, got rows %d bytes %d", progress.RowCount, progress.ByteCount)
	}
	return nil
}

func requireContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("chartifact postgres operation requires a context")
	}
	return ctx.Err()
}
