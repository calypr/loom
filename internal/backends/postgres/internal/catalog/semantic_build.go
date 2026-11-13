package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	semanticInventoryBuildTableName     = "fhir_semantic_inventory_build"
	maxSemanticInventoryDiagnosticBytes = 512
	defaultSemanticInventoryDiagnostic  = "backfill failed"
)

var semanticBuildSchemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

var (
	ErrSemanticInventoryClaimed          = errors.New("postgres: semantic inventory build is already claimed")
	ErrSemanticInventoryClaimLost        = errors.New("postgres: semantic inventory build claim was lost")
	ErrUnsupportedSemanticInventoryBuild = errors.New("postgres: unsupported semantic inventory build request")
	ErrInvalidSemanticInventoryBuild     = errors.New("postgres: invalid semantic inventory build")
)

type SemanticInventoryUnsupportedError struct {
	Case   string
	Detail string
}

func (e *SemanticInventoryUnsupportedError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrUnsupportedSemanticInventoryBuild, e.Case, e.Detail)
}

func (e *SemanticInventoryUnsupportedError) Unwrap() error {
	return ErrUnsupportedSemanticInventoryBuild
}

type SemanticInventoryClaimedError struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	State             catalog.SemanticInventoryState
}

func (e *SemanticInventoryClaimedError) Error() string {
	return fmt.Sprintf("%v: %s/%s build %s is in state %q under a live lease; "+
		"the lease must expire or be released before a new claimant can take it",
		ErrSemanticInventoryClaimed, e.Project, e.DatasetGeneration, e.BuildID, e.State)
}

func (e *SemanticInventoryClaimedError) Unwrap() error { return ErrSemanticInventoryClaimed }

type SemanticInventoryClaimLostError struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	Reason            string
}

func (e *SemanticInventoryClaimLostError) Error() string {
	return fmt.Sprintf("%v: %s/%s build %s: %s",
		ErrSemanticInventoryClaimLost, e.Project, e.DatasetGeneration, e.BuildID, e.Reason)
}

func (e *SemanticInventoryClaimLostError) Unwrap() error { return ErrSemanticInventoryClaimLost }

type SemanticBuildRepository struct {
	src      storepostgres.ConnSource
	buildFQN string
}

func NewSemanticBuildRepository(src storepostgres.ConnSource, schema string) (*SemanticBuildRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil semantic build connection source")
	}
	if !semanticBuildSchemaPattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", storepostgres.ErrInvalidSchemaName, schema)
	}
	return &SemanticBuildRepository{
		src:      src,
		buildFQN: pgx.Identifier{schema, semanticInventoryBuildTableName}.Sanitize(),
	}, nil
}

func (r *SemanticBuildRepository) ClaimSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, lease time.Duration) (catalog.SemanticInventoryBuild, bool, error) {
	if err := validateSemanticInventoryBuildIdentity(build); err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	normalizeSemanticBuildRequest(&build)
	if build.SourceKind != catalog.SemanticInventorySourceRetained {
		return catalog.SemanticInventoryBuild{}, false, &SemanticInventoryUnsupportedError{
			Case:   "source_kind",
			Detail: fmt.Sprintf("claim requires %q, got %q; the file-sourced profiler path is not persisted by this slice", catalog.SemanticInventorySourceRetained, build.SourceKind),
		}
	}
	if strings.TrimSpace(token) == "" {
		return catalog.SemanticInventoryBuild{}, false, &SemanticInventoryUnsupportedError{Case: "claim token", Detail: "blank claim token"}
	}
	if lease <= 0 {
		return catalog.SemanticInventoryBuild{}, false, &SemanticInventoryUnsupportedError{Case: "lease", Detail: fmt.Sprintf("lease must be positive, got %s", lease)}
	}
	now := time.Now()
	sql := "INSERT INTO " + r.buildFQN +
		" (build_key, project, dataset_generation, build_id, state, observation_schema," +
		" inventory_schema, entry_index_version, rule_version, source_kind, source_availability," +
		" auth_resource_path, scanned_resources, checkpoint, checkpoint_collection, checkpoint_key," +
		" lease_token, lease_expires_at, diagnostics)" +
		" VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, $8, $9, 'unknown'," +
		" $10, 0, '', '', '', $11, $12, '{}')" +
		" ON CONFLICT (project, dataset_generation, build_id) DO UPDATE SET" +
		" state = 'running'," +
		" source_kind = EXCLUDED.source_kind," +
		" source_availability = CASE WHEN " + semanticInventoryBuildTableName + ".source_kind = EXCLUDED.source_kind" +
		" THEN " + semanticInventoryBuildTableName + ".source_availability ELSE 'unknown' END," +
		" scanned_resources = CASE WHEN " + semanticInventoryBuildTableName + ".source_kind = EXCLUDED.source_kind" +
		" THEN " + semanticInventoryBuildTableName + ".scanned_resources ELSE 0 END," +
		" checkpoint = ''," +
		" checkpoint_collection = CASE WHEN " + semanticInventoryBuildTableName + ".source_kind = EXCLUDED.source_kind" +
		" THEN " + semanticInventoryBuildTableName + ".checkpoint_collection ELSE '' END," +
		" checkpoint_key = CASE WHEN " + semanticInventoryBuildTableName + ".source_kind = EXCLUDED.source_kind" +
		" THEN " + semanticInventoryBuildTableName + ".checkpoint_key ELSE '' END," +
		" diagnostics = '{}'," +
		" lease_token = EXCLUDED.lease_token," +
		" lease_expires_at = EXCLUDED.lease_expires_at," +
		" updated_at = now()" +
		" WHERE " + semanticInventoryBuildTableName + ".state <> 'complete'" +
		" AND (" + semanticInventoryBuildTableName + ".lease_expires_at <= $13" +
		" OR " + semanticInventoryBuildTableName + ".lease_token = $14)" +
		" RETURNING " + semanticInventoryBuildColumns
	var out catalog.SemanticInventoryBuild
	var claimed bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		err := conn.QueryRow(ctx, sql,
			build.Key, build.Project, build.DatasetGeneration, build.BuildID,
			build.ObservationSchema, build.InventorySchema, build.EntryIndexVersion, build.RuleVersion,
			build.SourceKind, build.AuthResourcePath, token, now.Add(lease).UnixMilli(), now.UnixMilli(), token,
		).Scan(scanSemanticInventoryBuild(&out)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return classifySemanticBuildWrite("claim", build, err)
		}
		claimed = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	if claimed {
		normalizeReadBuild(&out, build.Project, build.DatasetGeneration, build.BuildID)
		return out, true, nil
	}
	current, found, err := r.ReadSemanticInventoryBuild(ctx, build.Project, build.DatasetGeneration, build.BuildID)
	if err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	if !found {
		return catalog.SemanticInventoryBuild{}, false, fmt.Errorf(
			"postgres: claim semantic inventory build %s/%s: row vanished between claim and re-read",
			build.Project, build.DatasetGeneration)
	}
	if current.State == catalog.SemanticInventoryComplete {
		return current, false, nil
	}
	return catalog.SemanticInventoryBuild{}, false, &SemanticInventoryClaimedError{
		Project: build.Project, DatasetGeneration: build.DatasetGeneration,
		BuildID: build.BuildID, State: current.State,
	}
}

func (r *SemanticBuildRepository) ReadSemanticInventoryBuild(ctx context.Context, project, datasetGeneration, buildID string) (catalog.SemanticInventoryBuild, bool, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(buildID) == "" {
		return catalog.SemanticInventoryBuild{}, false, &SemanticInventoryUnsupportedError{
			Case: "scope", Detail: "project and build id are required for a scoped read",
		}
	}
	datasetGeneration = catalog.NormalizeDatasetGeneration(datasetGeneration)
	sql := "SELECT " + semanticInventoryBuildColumns + " FROM " + r.buildFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3"
	var out catalog.SemanticInventoryBuild
	var found bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		err := conn.QueryRow(ctx, sql, project, datasetGeneration, buildID).Scan(scanSemanticInventoryBuild(&out)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("postgres: read semantic inventory build %s/%s: %w", project, datasetGeneration, err)
		}
		found = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	if !found {
		return catalog.SemanticInventoryBuild{}, false, nil
	}
	normalizeReadBuild(&out, project, datasetGeneration, buildID)
	return out, true, nil
}

func (r *SemanticBuildRepository) AdvanceSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected, next catalog.SemanticInventoryCheckpoint, scanned int64, lease time.Duration) (catalog.SemanticInventoryBuild, error) {
	if err := validateSemanticInventoryTransitionRequest(build, token, lease, true); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	normalizeSemanticBuildRequest(&build)
	if err := validateSemanticInventoryCheckpoint(expected, "expected", false); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	if err := validateSemanticInventoryCheckpoint(next, "next", true); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	if err := validateSemanticInventorySourceAvailability(build.SourceAvailability); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	now := time.Now()
	sql := "UPDATE " + r.buildFQN + " SET" +
		" checkpoint_collection = $8, checkpoint_key = $9," +
		" scanned_resources = $10, source_availability = $11," +
		" lease_expires_at = $12, diagnostics = '{}', updated_at = now()" +
		" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3" +
		" AND state = 'running' AND lease_token = $4 AND lease_expires_at > $5" +
		" AND checkpoint_collection = $6 AND checkpoint_key = $7" +
		" RETURNING " + semanticInventoryBuildColumns
	return r.executeGuarded(ctx, "advance", build, sql, token, &expected,
		build.Project, build.DatasetGeneration, build.BuildID, token, now.UnixMilli(),
		expected.Collection, expected.Key, next.Collection, next.Key, max(scanned, 0),
		build.SourceAvailability, now.Add(lease).UnixMilli())
}

func (r *SemanticBuildRepository) CompleteSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected catalog.SemanticInventoryCheckpoint, scanned int64) (catalog.SemanticInventoryBuild, error) {
	if err := validateSemanticInventoryTransitionRequest(build, token, 0, false); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	normalizeSemanticBuildRequest(&build)
	if err := validateSemanticInventoryCheckpoint(expected, "expected", false); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	if err := validateSemanticInventorySourceAvailability(build.SourceAvailability); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	now := time.Now()
	sql := "UPDATE " + r.buildFQN + " SET" +
		" state = 'complete', scanned_resources = $8, source_availability = $9," +
		" lease_token = '', lease_expires_at = 0, diagnostics = '{}', updated_at = now()" +
		" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3" +
		" AND state = 'running' AND lease_token = $4 AND lease_expires_at > $5" +
		" AND checkpoint_collection = $6 AND checkpoint_key = $7" +
		" RETURNING " + semanticInventoryBuildColumns
	return r.executeGuarded(ctx, "complete", build, sql, token, &expected,
		build.Project, build.DatasetGeneration, build.BuildID, token, now.UnixMilli(),
		expected.Collection, expected.Key, max(scanned, 0), build.SourceAvailability)
}

func (r *SemanticBuildRepository) FailSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token, diagnostic string) error {
	if err := validateSemanticInventoryTransitionRequest(build, token, 0, false); err != nil {
		return err
	}
	normalizeSemanticBuildRequest(&build)
	if text := strings.TrimSpace(diagnostic); text != "" {
		if len(text) > maxSemanticInventoryDiagnosticBytes {
			cut := []byte(text[:maxSemanticInventoryDiagnosticBytes])
			for len(cut) > 0 && !utf8.Valid(cut) {
				cut = cut[:len(cut)-1]
			}
			text = string(cut)
		}
		diagnostic = text
	} else {
		diagnostic = defaultSemanticInventoryDiagnostic
	}
	sql := "UPDATE " + r.buildFQN + " SET" +
		" state = 'failed', lease_token = '', lease_expires_at = 0," +
		" diagnostics = $5::text[], updated_at = now()" +
		" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3" +
		" AND state = 'running' AND lease_token = $4" +
		" RETURNING " + semanticInventoryBuildColumns
	_, err := r.executeGuarded(ctx, "fail", build, sql, token, nil,
		build.Project, build.DatasetGeneration, build.BuildID, token, []string{diagnostic})
	return err
}

func (r *SemanticBuildRepository) executeGuarded(ctx context.Context, op string, build catalog.SemanticInventoryBuild, sql, token string, expected *catalog.SemanticInventoryCheckpoint, args ...any) (catalog.SemanticInventoryBuild, error) {
	var out catalog.SemanticInventoryBuild
	matched := false
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		err := conn.QueryRow(ctx, sql, args...).Scan(scanSemanticInventoryBuild(&out)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return classifySemanticBuildWrite(op, build, err)
		}
		matched = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	if matched {
		normalizeReadBuild(&out, build.Project, build.DatasetGeneration, build.BuildID)
		return out, nil
	}
	current, found, readErr := r.ReadSemanticInventoryBuild(ctx, build.Project, build.DatasetGeneration, build.BuildID)
	if readErr != nil {
		return catalog.SemanticInventoryBuild{}, readErr
	}
	if !found {
		return catalog.SemanticInventoryBuild{}, &SemanticInventoryClaimLostError{
			Project: build.Project, DatasetGeneration: build.DatasetGeneration,
			BuildID: build.BuildID, Reason: "row is missing",
		}
	}
	return catalog.SemanticInventoryBuild{}, &SemanticInventoryClaimLostError{
		Project: build.Project, DatasetGeneration: build.DatasetGeneration,
		BuildID: build.BuildID, Reason: semanticBuildGuardMissReason(current, token, expected),
	}
}

func semanticBuildGuardMissReason(current catalog.SemanticInventoryBuild, token string, expected *catalog.SemanticInventoryCheckpoint) string {
	if current.State != catalog.SemanticInventoryRunning {
		return fmt.Sprintf("state is %q, not running", current.State)
	}
	if current.LeaseToken != token {
		return fmt.Sprintf("lease token is held by another claimant (expiry %d)", current.LeaseExpiresAt)
	}
	if current.LeaseExpiresAt <= time.Now().UnixMilli() {
		return fmt.Sprintf("lease expired at %d", current.LeaseExpiresAt)
	}
	if expected != nil && (current.SourceCheckpoint.Collection != expected.Collection || current.SourceCheckpoint.Key != expected.Key) {
		return fmt.Sprintf("checkpoint is %s/%s, expected %s/%s",
			current.SourceCheckpoint.Collection, current.SourceCheckpoint.Key, expected.Collection, expected.Key)
	}
	return "row changed concurrently; re-read matches every guard"
}

const semanticInventoryBuildColumns = "build_key, state, observation_schema, inventory_schema," +
	" entry_index_version, rule_version, source_kind, source_availability, auth_resource_path," +
	" scanned_resources, checkpoint, checkpoint_collection, checkpoint_key, lease_token," +
	" lease_expires_at, diagnostics"

func scanSemanticInventoryBuild(out *catalog.SemanticInventoryBuild) []any {
	return []any{
		&out.Key, &out.State, &out.ObservationSchema, &out.InventorySchema,
		&out.EntryIndexVersion, &out.RuleVersion, &out.SourceKind, &out.SourceAvailability,
		&out.AuthResourcePath, &out.ScannedResources, &out.Checkpoint,
		&out.SourceCheckpoint.Collection, &out.SourceCheckpoint.Key, &out.LeaseToken,
		&out.LeaseExpiresAt, &out.Diagnostics,
	}
}

func normalizeReadBuild(out *catalog.SemanticInventoryBuild, project, datasetGeneration, buildID string) {
	out.Project = project
	out.DatasetGeneration = datasetGeneration
	out.BuildID = buildID
}

func (r *SemanticBuildRepository) withConn(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire semantic build connection: %w", err)
	}
	defer conn.Release()
	return fn(conn)
}

func classifySemanticBuildWrite(op string, build catalog.SemanticInventoryBuild, err error) error {
	if storepostgres.IsUniqueConstraintViolation(err, semanticInventoryBuildTableName+"_key_ux") {
		return fmt.Errorf("postgres: semantic inventory build key %s collides across scopes (corrupted sib_ derivation): %w",
			build.Key, err)
	}
	return fmt.Errorf("postgres: %s semantic inventory build %s/%s: %w", op, build.Project, build.DatasetGeneration, err)
}

func validateSemanticInventoryBuildIdentity(build catalog.SemanticInventoryBuild) error {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSemanticInventoryBuild, err)
	}
	return nil
}

func normalizeSemanticBuildRequest(build *catalog.SemanticInventoryBuild) {
	build.DatasetGeneration = catalog.NormalizeDatasetGeneration(build.DatasetGeneration)
}

func validateSemanticInventoryTransitionRequest(build catalog.SemanticInventoryBuild, token string, lease time.Duration, requireLease bool) error {
	if err := validateSemanticInventoryBuildIdentity(build); err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return &SemanticInventoryUnsupportedError{Case: "claim token", Detail: "blank claim token"}
	}
	if requireLease && lease <= 0 {
		return &SemanticInventoryUnsupportedError{Case: "lease", Detail: fmt.Sprintf("lease renewal must be positive, got %s", lease)}
	}
	if lease < 0 {
		return &SemanticInventoryUnsupportedError{Case: "lease", Detail: fmt.Sprintf("lease must not be negative, got %s", lease)}
	}
	return nil
}

func validateSemanticInventoryCheckpoint(cp catalog.SemanticInventoryCheckpoint, name string, requirePresent bool) error {
	if (cp.Collection == "") != (cp.Key == "") {
		return &SemanticInventoryUnsupportedError{
			Case:   "checkpoint",
			Detail: fmt.Sprintf("%s checkpoint must set both collection and key, or neither (collection=%q key=%q)", name, cp.Collection, cp.Key),
		}
	}
	if requirePresent && cp.Collection == "" {
		return &SemanticInventoryUnsupportedError{
			Case:   "checkpoint",
			Detail: "next checkpoint must name a collection and key; completion uses CompleteSemanticInventoryBackfill",
		}
	}
	return nil
}

func validateSemanticInventorySourceAvailability(availability string) error {
	switch availability {
	case catalog.SemanticInventorySourceAvailabilityUnknown,
		catalog.SemanticInventorySourceAvailabilityVerified,
		catalog.SemanticInventorySourceAvailabilityUnproven:
		return nil
	default:
		return &SemanticInventoryUnsupportedError{Case: "source_availability", Detail: fmt.Sprintf("unknown value %q", availability)}
	}
}
