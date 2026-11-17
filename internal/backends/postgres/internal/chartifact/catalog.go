package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
)

const PrivateArtifactsTable = "chartifact_private_artifacts"

const (
	cleanupPendingState = string(chartifact.StateCleanupPending)
	maxExpiredPageSize  = 1000
)

var ErrInvalidSchema = errors.New("chartifact postgres: invalid schema name")

var ErrArtifactExists = errors.New("chartifact postgres: artifact manifest already exists")

type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Catalog struct {
	db        DB
	artifacts string
}

var _ chartifact.Catalog = (*Catalog)(nil)

func NewCatalog(pool DB, schema string) (*Catalog, error) {
	if pool == nil {
		return nil, errors.New("chartifact postgres: nil pool")
	}
	if !storepostgres.ValidSchemaName(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchema, schema)
	}
	return &Catalog{
		db:        pool,
		artifacts: pgx.Identifier{schema, PrivateArtifactsTable}.Sanitize(),
	}, nil
}

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

func (c *Catalog) Create(ctx context.Context, manifest chartifact.Manifest) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if err := validateCreate(manifest); err != nil {
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

func jsonTime(value time.Time) (string, error) {
	encoded, err := json.Marshal(value.UTC())
	if err != nil {
		return "", fmt.Errorf("encode artifact timestamp: %w", err)
	}
	return string(encoded), nil
}

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

func validateCreate(manifest chartifact.Manifest) error {
	if strings.TrimSpace(manifest.ArtifactID) == "" || manifest.ArtifactID != strings.TrimSpace(manifest.ArtifactID) {
		return fmt.Errorf("manifest requires a trimmed artifact ID")
	}
	if strings.TrimSpace(manifest.PhysicalTable) == "" || manifest.PhysicalTable != strings.TrimSpace(manifest.PhysicalTable) {
		return fmt.Errorf("manifest requires a trimmed physical table name")
	}
	if strings.TrimSpace(manifest.LeaseOwner) == "" || manifest.LeaseOwner != strings.TrimSpace(manifest.LeaseOwner) {
		return fmt.Errorf("manifest requires a trimmed lease owner")
	}
	if err := validateProgressFields(chartifact.Progress{
		State: manifest.State, RowCount: manifest.RowCount, ByteCount: manifest.ByteCount,
	}); err != nil {
		return err
	}
	if manifest.LeaseUntil.IsZero() || manifest.CreatedAt.IsZero() || manifest.UpdatedAt.IsZero() {
		return fmt.Errorf("manifest requires lease, created, and updated timestamps")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"execution ID", manifest.Identity.ExecutionID},
		{"output ID", manifest.Identity.OutputID},
		{"stage ID", manifest.Identity.StageID},
		{"project", manifest.Identity.Project},
		{"dataset generation", manifest.Identity.DatasetGeneration},
	} {
		if field.value == "" {
			return fmt.Errorf("manifest requires a %s", field.name)
		}
	}
	return nil
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
