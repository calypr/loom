// Semantic-inventory build lifecycle, lease, and typed checkpoint state for
// the PostgreSQL source store (pg-catalog-04).
//
// SemanticBuildRepository persists the one build record that governs a
// semantic-inventory rebuild for an exact (project, dataset_generation,
// build_id) identity, created by migration 000006_semantic_inventory_build:
//
//   - ClaimSemanticInventoryBackfill acquires or resumes the retained-source
//     scan under a fenced lease. A live lease held by a different token is a
//     typed claimed error, never a steal; a completed build is returned with
//     claimed=false and is never rewritten, so file and retained-vertex
//     contribution sets cannot be mixed by a second scan.
//   - AdvanceSemanticInventoryBackfill moves the typed source checkpoint
//     (collection, key) only under a compare-and-swap guard: running state,
//     the claimant's own live lease token, and the exact expected checkpoint.
//     A stale expected checkpoint is rejected without changing state.
//   - CompleteSemanticInventoryBackfill and FailSemanticInventoryBackfill
//     keep the Arango adapter's meanings: completion releases the lease and
//     clears diagnostics (a complete build carries no failure evidence) and
//     records the caller's source availability; failure releases the lease,
//     keeps scanned progress and checkpoints, and records exactly one
//     diagnostic.
//
// Every guard is one statement, so PostgreSQL is the arbiter: no read-modify
// write window exists. Lease expiry is compared in caller-supplied epoch
// milliseconds, matching the domain's LeaseExpiresAt field exactly.
//
// This slice is the build-state repository only: contribution rows, entry
// materialization, and public page reads are separate tickets. Unsupported
// request shapes (for example a file-sourced claim, which this retained
// backfill path never issues) are typed errors, never silent fallbacks. The
// Arango adapter (internal/backends/arango/internal/catalog/semantic_inventory.go) remains the
// behavioral reference, including its convergence of whitespace-padded
// generations: every entry point normalizes the requested dataset generation
// with catalog.NormalizeDatasetGeneration before any statement runs, so the
// padded and clean spellings can never fork the primary key. There is no
// cross-database atomicity and no backend fallback: every failure is a named
// error the caller must surface.
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// semanticInventoryBuildTableName is the table created by migration
	// 000006_semantic_inventory_build.sql.
	semanticInventoryBuildTableName = "fhir_semantic_inventory_build"
	// maxSemanticInventoryDiagnosticBytes bounds one persisted diagnostic,
	// matching the Arango adapter's 512-byte truncation.
	maxSemanticInventoryDiagnosticBytes = 512
	// defaultSemanticInventoryDiagnostic is persisted when a failure carries
	// no message, matching the Arango adapter.
	defaultSemanticInventoryDiagnostic = "backfill failed"
	// uniqueViolationSQLState23505 is PostgreSQL's unique-violation state.
	// The store package's constant is unexported, so this adapter names it.
	uniqueViolationSQLState23505 = "23505"
)

// semanticBuildSchemaPattern accepts the same safe lowercase identifiers the
// store package's runner accepts; the store copy is unexported.
var semanticBuildSchemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Sentinel errors for classifying semantic build-state failures without
// string matching.
var (
	// ErrSemanticInventoryClaimed reports that a live, unexpired lease held
	// by a different token blocks the claim. The stored build is untouched.
	ErrSemanticInventoryClaimed = errors.New("postgres: semantic inventory build is already claimed")
	// ErrSemanticInventoryClaimLost reports that an advance, completion, or
	// failure transition did not match its guards (missing row, state moved
	// off running, foreign or expired lease token, or a stale expected
	// checkpoint). The stored build is unchanged.
	ErrSemanticInventoryClaimLost = errors.New("postgres: semantic inventory build claim was lost")
	// ErrUnsupportedSemanticInventoryBuild reports a request shape this
	// PostgreSQL build-state slice refuses (file-sourced claim, blank token,
	// non-positive lease, malformed typed checkpoint, or an unknown state or
	// source-availability value). It is a refusal, not a fallback: nothing
	// was persisted.
	ErrUnsupportedSemanticInventoryBuild = errors.New("postgres: unsupported semantic inventory build request")
	// ErrInvalidSemanticInventoryBuild reports a build whose identity the
	// shared catalog contract itself rejects (project/generation/build id/key
	// mismatch) before any statement runs.
	ErrInvalidSemanticInventoryBuild = errors.New("postgres: invalid semantic inventory build")
)

// SemanticInventoryUnsupportedError names the refused request shape. It
// matches errors.Is(err, ErrUnsupportedSemanticInventoryBuild).
type SemanticInventoryUnsupportedError struct {
	Case   string // e.g. "source_kind", "claim token", "lease", "checkpoint", "source_availability"
	Detail string
}

func (e *SemanticInventoryUnsupportedError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrUnsupportedSemanticInventoryBuild, e.Case, e.Detail)
}

func (e *SemanticInventoryUnsupportedError) Unwrap() error {
	return ErrUnsupportedSemanticInventoryBuild
}

// SemanticInventoryClaimedError names the scope a live lease blocks. It
// matches errors.Is(err, ErrSemanticInventoryClaimed). The competing token
// is deliberately not exposed.
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

// SemanticInventoryClaimLostError names which guard rejected the
// transition, so callers can distinguish a stolen/expired lease from a stale
// checkpoint without string matching. The stored row is unchanged. It
// matches errors.Is(err, ErrSemanticInventoryClaimLost).
type SemanticInventoryClaimLostError struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	// Reason is one of: "row is missing", "state is <state>", "lease token
	// is held by another claimant", "lease expired at <epoch ms>", or
	// "checkpoint is <collection>/<key>, expected <collection>/<key>".
	Reason string
}

func (e *SemanticInventoryClaimLostError) Error() string {
	return fmt.Sprintf("%v: %s/%s build %s: %s",
		ErrSemanticInventoryClaimLost, e.Project, e.DatasetGeneration, e.BuildID, e.Reason)
}

func (e *SemanticInventoryClaimLostError) Unwrap() error { return ErrSemanticInventoryClaimLost }

// SemanticBuildRepository stores and reads the semantic-inventory build
// lifecycle state in one schema of the PostgreSQL source store. Create one
// with NewSemanticBuildRepository. The table (created by MigrateSchema) must
// exist before use; the caller owns the connection source and the schema
// lifecycle. All guards are single statements: exactly one claimant can hold
// a live lease, and a stale token or stale expected checkpoint can never
// advance, complete, or fail the build.
type SemanticBuildRepository struct {
	src      storepostgres.ConnSource
	buildFQN string
}

// NewSemanticBuildRepository validates the connection source and schema name
// and binds the repository to the semantic build table inside <schema>. The
// connection source must be live only when a method runs, not here.
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

// ClaimSemanticInventoryBackfill atomically acquires or resumes the retained
// source scan for build's identity. On success the stored row reads: state
// running, this token, lease expiry now+lease, diagnostics cleared, the
// legacy string checkpoint cleared. When the identity was already running
// under the same source kind, scanned progress and the typed checkpoint are
// resumed; a switch of source kind resets them (contribution sets are never
// mixed). A completed build is returned unchanged with claimed=false. A live
// lease held by another token returns a *SemanticInventoryClaimedError.
//
// This retained-backfill entry point requires
// build.SourceKind == catalog.SemanticInventorySourceRetained; any other
// shape is a *SemanticInventoryUnsupportedError, never a silent fallback.
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
	// Ordered parameters: $1 build key, $2 project, $3 generation,
	// $4 build id, $5 observation schema, $6 inventory schema,
	// $7 entry index version, $8 rule version, $9 source kind,
	// $10 auth path, $11 token, $12 lease expiry (epoch ms), $13 now
	// (epoch ms), $14 token again for the same-token resume guard.
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
	// The conflict guard rejected the update. Re-read to report why, exactly
	// as the Arango adapter's claim does (complete -> claimed=false, else
	// already claimed).
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

// ReadSemanticInventoryBuild reads the full stored build for one exact
// identity, including lease and checkpoint fields, for control-plane use.
// found is false when the identity has never been claimed. Public page
// projection (which must hide lease internals) is a separate ticket.
func (r *SemanticBuildRepository) ReadSemanticInventoryBuild(ctx context.Context, project, datasetGeneration, buildID string) (catalog.SemanticInventoryBuild, bool, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(buildID) == "" {
		return catalog.SemanticInventoryBuild{}, false, &SemanticInventoryUnsupportedError{
			Case: "scope", Detail: "project and build id are required for a scoped read",
		}
	}
	datasetGeneration = catalog.NormalizeDatasetGeneration(datasetGeneration)
	// Ordered parameters: $1 project, $2 generation, $3 build id.
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

// AdvanceSemanticInventoryBackfill moves the typed source checkpoint from
// expected to next only while the build is running under the caller's live
// lease token and still stores exactly the expected checkpoint; the update
// renews the lease, records source availability from build, and clears
// stale diagnostics — persisted progress means the prior failure evidence
// no longer applies. A stale expected checkpoint, an expired or foreign
// token, or any state move is a *SemanticInventoryClaimLostError naming the
// failed guard, and the stored build is unchanged; malformed requests
// (blank token, non-positive lease, half-set checkpoint) are typed
// unsupported refusals raised before any statement runs.
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
	// Ordered parameters: $1 project, $2 generation, $3 build id,
	// $4 token, $5 now (epoch ms), $6 expected collection, $7 expected key,
	// $8 next collection, $9 next key, $10 scanned, $11 availability,
	// $12 renewed expiry (epoch ms).
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

// CompleteSemanticInventoryBackfill performs the final compare-and-swap:
// running state, the caller's live lease token, and the exact expected
// checkpoint. On success the build becomes complete, the lease is released
// (token cleared, expiry zeroed), diagnostics are cleared — a completed
// build carries no failure evidence — and source availability is the
// caller's verified meaning, exactly as the Arango adapter records it. Entry
// materialization is not this slice's job and is never silently skipped
// here: the repository does not touch contribution rows at all.
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
	// Ordered parameters: $1 project, $2 generation, $3 build id,
	// $4 token, $5 now (epoch ms), $6 expected collection, $7 expected key,
	// $8 scanned, $9 availability.
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

// FailSemanticInventoryBackfill records one failure diagnostic and releases
// the lease for the running build under the caller's token. It mirrors the
// Arango adapter: no lease-expiry guard (cleanup must land even after the
// lease lapsed), a blank diagnostic becomes "backfill failed", one
// diagnostic is capped at 512 bytes, and scanned progress, both checkpoints,
// auth path, and source availability keep their stored meanings. Only the
// token holder can fail the build; any guard miss is a typed claim-lost
// error and the stored build is unchanged.
func (r *SemanticBuildRepository) FailSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token, diagnostic string) error {
	if err := validateSemanticInventoryTransitionRequest(build, token, 0, false); err != nil {
		return err
	}
	normalizeSemanticBuildRequest(&build)
	if text := strings.TrimSpace(diagnostic); text != "" {
		if len(text) > maxSemanticInventoryDiagnosticBytes {
			// A raw byte cut can split a multi-byte rune, and PostgreSQL then
			// rejects the whole UPDATE with SQLSTATE 22021, losing the failure
			// record entirely; back off to the last complete rune.
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
	// Ordered parameters: $1 project, $2 generation, $3 build id,
	// $4 token, $5 diagnostic.
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

// executeGuarded runs one compare-and-swap statement and classifies a
// zero-row outcome by naming the first guard the stored row violates, in
// guard order (state, token, expiry, then the checkpoint expectation when
// the statement carried one). The statement itself already changed nothing
// when it matched zero rows.
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

// semanticBuildGuardMissReason attributes a zero-row compare-and-swap to the
// first guard the stored row violates, in guard order: state, then lease
// token, then (for statements that carry an expiry guard) lease expiry,
// then the checkpoint expectation.
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
	// The stored row satisfies every guard on re-read: it changed under a
	// race between the statement and this attribution read.
	return "row changed concurrently; re-read matches every guard"
}

// semanticInventoryBuildColumns is the ordered column list every read and
// RETURNING clause uses, in the order scanSemanticInventoryBuild consumes.
const semanticInventoryBuildColumns = "build_key, state, observation_schema, inventory_schema," +
	" entry_index_version, rule_version, source_kind, source_availability, auth_resource_path," +
	" scanned_resources, checkpoint, checkpoint_collection, checkpoint_key, lease_token," +
	" lease_expires_at, diagnostics"

// scanSemanticInventoryBuild binds one row of semanticInventoryBuildColumns
// to a domain build. Project, dataset generation, and build id are the
// caller's scope (bound by every query) and set by normalizeReadBuild.
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

// classifySemanticBuildWrite separates a build_key identity collision (a
// corrupted sib_ derivation across scopes) from ordinary write failures.
func classifySemanticBuildWrite(op string, build catalog.SemanticInventoryBuild, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationSQLState23505 &&
		pgErr.ConstraintName == semanticInventoryBuildTableName+"_key_ux" {
		return fmt.Errorf("postgres: semantic inventory build key %s collides across scopes (corrupted sib_ derivation): %w",
			build.Key, err)
	}
	return fmt.Errorf("postgres: %s semantic inventory build %s/%s: %w", op, build.Project, build.DatasetGeneration, err)
}

// validateSemanticInventoryBuildIdentity runs the shared catalog contract
// first (project/generation/build id/key agreement), then the shapes only
// this store requires.
func validateSemanticInventoryBuildIdentity(build catalog.SemanticInventoryBuild) error {
	if err := catalog.ValidateSemanticInventoryBuild(build); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSemanticInventoryBuild, err)
	}
	return nil
}

// normalizeSemanticBuildRequest converges a whitespace-padded dataset
// generation onto the normalized identity that the derived build key and
// build id already use. catalog.NormalizeDatasetGeneration is the convention
// every catalog boundary applies; without it here, the raw spelling would
// fork the (project, dataset_generation, build_id) primary key against the
// clean spelling while both share one build_key.
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

// validateSemanticInventoryCheckpoint enforces the table invariant: a
// checkpoint has both parts or neither. requirePresent additionally demands
// a fully populated next checkpoint (the start-of-scan zero checkpoint is
// only legal as the CAS expectation).
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
