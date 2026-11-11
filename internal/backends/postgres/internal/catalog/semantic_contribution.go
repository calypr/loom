// Deterministic semantic-inventory contribution persistence and the
// retained-resource keyset page adapter for the PostgreSQL source store
// (pg-catalog-05).
//
// SemanticContributionRepository persists the one contribution table that the
// retained-source backfill (internal/ingest/semantic_inventory_backfill.go)
// writes beneath the fenced checkpoint protocol, created by migration
// 000009_semantic_inventory_contribution:
//
//   - WriteSemanticInventoryContributions writes and replays the same
//     contribution identity idempotently. The contribution key is the
//     content-addressed identity (catalog.SemanticInventoryContribution.Key,
//     scoped by build/source/ordinal); a duplicate key whose stored payload
//     digest matches is a no-op replay, and a duplicate key whose payload
//     digest differs is a typed conflict that never overwrites the stored
//     row. This is deliberately stricter than the Arango adapter's overwrite
//     insert (internal/catalog/arango/semantic_inventory.go
//     WriteSemanticInventoryContributions): an ambiguous replay can never
//     replace a different payload under one identity, mirroring the
//     pg-store-06 ledger digest rule.
//   - ReadRetainedSemanticInventoryPage returns at most limit retained
//     source rows of one exact (project, dataset_generation, resource_type)
//     scope in stable byte-order logical_id keyset order, preserving the
//     authorization path and the exact payload JSONB (SQL NULL stays
//     distinct from the bytes "null"). Paging is stable because the order
//     key is part of the immutable natural identity. Ordering is pinned to
//     COLLATE "C" on both the keyset predicate and the sort so PostgreSQL
//     compares exact bytes — the same byte order the backfill's Go-side
//     cursor guard uses. The server's default collation is never trusted to
//     agree with Go byte order, and no SQL-side trim/lower approximation is
//     ever applied; catalog.NormalizeDatasetGeneration in Go is the single
//     canonicalization point before any statement runs.
//   - Source availability keeps three distinct first-page readings when a
//     generated resource table yields no rows: field-profile rows with
//     doc_count > 0 make the missing source a contradiction (typed error,
//     matching the Arango adapter's ErrRetainedSemanticSourceMissing), a
//     coverage marker without document evidence is a verified known-empty
//     population, and neither marker at all leaves availability unproven
//     (SourceExists=false), exactly the distinctions the backfill reports.
//   - A contribution batch commits before the typed checkpoint advances:
//     the checkpoint is moved only by the build-state repository's CAS
//     (pg-catalog-04 SemanticBuildRepository, reused here unchanged), so a
//     failed batch leaves the checkpoint untouched and a resumed scan
//     rewrites already-written contributions under identical keys.
//
// The composite SemanticInventoryBackfillStore wires these methods with
// SemanticBuildRepository's claim/advance/complete/fail so
// ingest.BackfillSemanticInventory can drive the full loop on PostgreSQL;
// structural satisfaction of the ingest backend contract is asserted at the
// ingest call site in tests. Entry materialization (the
// fhir_semantic_inventory_entries analogue) is a separate slice and is
// refused by name here, never silently skipped. Unsupported request shapes
// are typed errors, never silent fallbacks. There is no cross-database
// atomicity and no backend fallback: every failure is a named error the
// caller must surface.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// semanticInventoryContributionTableName is the table created by
	// migration 000009_semantic_inventory_contribution.
	semanticInventoryContributionTableName = "fhir_semantic_inventory_contribution"
	// retainedResourceTableName is the shared resource table created by
	// migration 000002_fhir_resource.sql. It is read-only for this slice.
	retainedResourceTableName = "fhir_resource"
	// fieldProfileTableName and fieldProfileCoverageTableName are the
	// source-availability evidence tables created by migration
	// 000007_field_profile.sql. They are read-only for this slice; their
	// absence means "no evidence", never an error, matching the Arango
	// adapter's collection-existence handling.
	fieldProfileTableName         = "fhir_field_profile"
	fieldProfileCoverageTableName = "fhir_field_profile_coverage"
	// maxRetainedSemanticPageLimit bounds one retained-source page,
	// matching the Arango adapter's limit window (1..1000).
	maxRetainedSemanticPageLimit = 1000
	// defaultSemanticContributionBatchSize is the fallback chunk size when
	// a caller passes batchSize <= 0, matching the Arango adapter.
	defaultSemanticContributionBatchSize = 500
	// maxSemanticContributionChunkSize bounds one INSERT chunk's row count.
	// writeChunk binds 14 parameters per row, and PostgreSQL caps one
	// statement at 65535 bind parameters (the Parse message encodes the
	// count in a 16-bit integer), so no caller-supplied batchSize —
	// ingest.BackfillSemanticInventory legally passes up to 5000 — may
	// build a statement beyond the protocol ceiling. 1000 rows x 14 =
	// 14000 parameters sits far below it, matching the named bounds in
	// internal/backends/postgres/internal/store/inventory.go (MaxInventoryWriteBatch = 1000)
	// and staying under the floor(32767/14) safe limit.
	maxSemanticContributionChunkSize = 1000
	// undefinedTableSQLState42P01 is PostgreSQL's undefined_table state.
	// The store package's constant is unexported, so this adapter names it.
	undefinedTableSQLState42P01 = "42P01"
)

// Sentinel errors for classifying contribution persistence and retained-page
// reads without string matching.
var (
	// ErrSemanticInventoryContributionConflict reports a contribution
	// insert whose identity (content-addressed contribution key, or the
	// build/source/ordinal slot) is already held by different content. The
	// stored row is untouched: persistence never overwrites a conflicting
	// payload silently.
	ErrSemanticInventoryContributionConflict = errors.New("postgres: semantic inventory contribution conflict")
	// ErrInvalidSemanticInventoryContribution reports a contribution whose
	// identity the shared catalog contract or this store rejects before
	// any statement runs. Nothing was persisted.
	ErrInvalidSemanticInventoryContribution = errors.New("postgres: invalid semantic inventory contribution")
	// ErrInvalidRetainedSemanticPageRequest reports a retained-source page
	// request that cannot be honored: a non-canonical collection name, a
	// blank project, or a page size outside [1, 1000].
	ErrInvalidRetainedSemanticPageRequest = errors.New("postgres: invalid retained semantic inventory page request")
	// ErrRetainedSemanticSourceMissing reports that a resource type yields
	// no retained rows although field-profile records with doc_count > 0
	// claim the population was profiled — the same contradiction the
	// Arango adapter raises via ErrRetainedSemanticSourceMissing.
	ErrRetainedSemanticSourceMissing = errors.New("postgres: retained semantic inventory source is missing despite field-profile evidence")
	// ErrSemanticInventoryStorageMissing reports that Prepare found a
	// required table absent from the schema. This adapter never creates
	// tables; the migration runner owns schema content.
	ErrSemanticInventoryStorageMissing = errors.New("postgres: semantic inventory storage is not migrated")
	// ErrSemanticInventoryEntriesUnsupported names the slice boundary:
	// entry materialization is a separate ticket and this repository
	// neither reads nor writes entry rows.
	ErrSemanticInventoryEntriesUnsupported = errors.New("postgres: semantic inventory entry materialization is not persisted by this slice")
)

// SemanticInventoryContributionConflictError names the identity whose stored
// payload differs from the offered one. The stored contribution keeps its
// payload; nothing was overwritten. It matches
// errors.Is(err, ErrSemanticInventoryContributionConflict).
type SemanticInventoryContributionConflictError struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	SourceKind        string
	SourceID          string
	Ordinal           int
	ContributionKey   string
	// Slot reports that the conflict is on the (build, source, ordinal)
	// primary key rather than on the content-addressed contribution key:
	// a second, differently keyed contribution claims one source slot.
	Slot bool
	// StoredDigest and ProvidedDigest are the lowercase hex SHA-256 of the
	// canonical payload encodings compared in Go. StoredDigest is empty
	// for a slot conflict, where the competing rows carry different keys
	// and no single digest comparison applies.
	StoredDigest   string
	ProvidedDigest string
}

func (e *SemanticInventoryContributionConflictError) Error() string {
	if e.Slot {
		return fmt.Sprintf("%v: %s/%s build %s kind %q source %s ordinal %d is held by a different contribution key; "+
			"the stored row was not replaced (offered key %s)",
			ErrSemanticInventoryContributionConflict, e.Project, e.DatasetGeneration, e.BuildID,
			e.SourceKind, e.SourceID, e.Ordinal, e.ContributionKey)
	}
	return fmt.Sprintf("%v: contribution %s (%s/%s build %s kind %q source %s ordinal %d) already stores digest %s; "+
		"offered digest %s differs; the stored payload was not replaced",
		ErrSemanticInventoryContributionConflict, e.ContributionKey, e.Project, e.DatasetGeneration,
		e.BuildID, e.SourceKind, e.SourceID, e.Ordinal, e.StoredDigest, e.ProvidedDigest)
}

func (e *SemanticInventoryContributionConflictError) Unwrap() error {
	return ErrSemanticInventoryContributionConflict
}

// RetainedSemanticSourceMissingError names the scope whose rows vanished
// despite field-profile document evidence. It matches
// errors.Is(err, ErrRetainedSemanticSourceMissing).
type RetainedSemanticSourceMissingError struct {
	Project           string
	DatasetGeneration string
	ResourceType      string
}

func (e *RetainedSemanticSourceMissingError) Error() string {
	return fmt.Sprintf("%v: %s/%s/%s has no retained rows although field-profile records with doc_count > 0 exist",
		ErrRetainedSemanticSourceMissing, e.Project, e.DatasetGeneration, e.ResourceType)
}

func (e *RetainedSemanticSourceMissingError) Unwrap() error { return ErrRetainedSemanticSourceMissing }

// SemanticInventoryStorageMissingError names the missing table. It matches
// errors.Is(err, ErrSemanticInventoryStorageMissing).
type SemanticInventoryStorageMissingError struct {
	Schema string
	Table  string
}

func (e *SemanticInventoryStorageMissingError) Error() string {
	return fmt.Sprintf("%v: schema %q has no table %q; run MigrateSchema before the semantic inventory backfill",
		ErrSemanticInventoryStorageMissing, e.Schema, e.Table)
}

func (e *SemanticInventoryStorageMissingError) Unwrap() error {
	return ErrSemanticInventoryStorageMissing
}

// SemanticInventoryEntriesUnsupportedError names the build whose entry
// materialization this slice refuses. It matches
// errors.Is(err, ErrSemanticInventoryEntriesUnsupported).
type SemanticInventoryEntriesUnsupportedError struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	EntryIndexVersion int
}

func (e *SemanticInventoryEntriesUnsupportedError) Error() string {
	return fmt.Sprintf("%v: build %s/%s (entry index version %d) requires entry materialization, which this "+
		"PostgreSQL slice does not persist; entry rows are a separate slice, never a silent skip",
		ErrSemanticInventoryEntriesUnsupported, e.Project, e.DatasetGeneration, e.EntryIndexVersion)
}

func (e *SemanticInventoryEntriesUnsupportedError) Unwrap() error {
	return ErrSemanticInventoryEntriesUnsupported
}

// SemanticContributionRepository stores contribution rows and reads retained
// source pages for the semantic-inventory backfill in one schema of the
// PostgreSQL source store. Create one with NewSemanticContributionRepository.
// The tables (created by MigrateSchema) must exist before use; the caller
// owns the connection source and the schema lifecycle. Identity arbitration
// and conflict detection are database-arbitrated: one statement inserts, and
// the payload digest comparison in Go classifies a skipped key as an
// idempotent replay or a conflict.
type SemanticContributionRepository struct {
	src                storepostgres.ConnSource
	schema             string
	contributionFQN    string
	buildFQN           string
	resourceFQN        string
	fieldProfileFQN    string
	fieldProfileCovFQN string
}

// NewSemanticContributionRepository validates the connection source and
// schema name and binds the repository to the contribution, build, resource,
// and field-profile tables inside <schema>. The connection source must be
// live only when a method runs, not here.
func NewSemanticContributionRepository(src storepostgres.ConnSource, schema string) (*SemanticContributionRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil semantic contribution connection source")
	}
	if !semanticBuildSchemaPattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", storepostgres.ErrInvalidSchemaName, schema)
	}
	return &SemanticContributionRepository{
		src:                src,
		schema:             schema,
		contributionFQN:    pgx.Identifier{schema, semanticInventoryContributionTableName}.Sanitize(),
		buildFQN:           pgx.Identifier{schema, semanticInventoryBuildTableName}.Sanitize(),
		resourceFQN:        pgx.Identifier{schema, retainedResourceTableName}.Sanitize(),
		fieldProfileFQN:    pgx.Identifier{schema, fieldProfileTableName}.Sanitize(),
		fieldProfileCovFQN: pgx.Identifier{schema, fieldProfileCoverageTableName}.Sanitize(),
	}, nil
}

// PrepareSemanticInventoryBackfill confirms the derived persistence this
// backfill needs is migrated. Retained source rows and field-profile
// evidence are never created or changed here, and their tables' absence is
// an availability question resolved per page, not a preparation failure. A
// missing build, contribution, or resource table is a named
// *SemanticInventoryStorageMissingError; this adapter never creates tables.
func (r *SemanticContributionRepository) PrepareSemanticInventoryBackfill(ctx context.Context) error {
	for _, table := range []struct {
		fqn       string
		tableName string
	}{
		{r.buildFQN, semanticInventoryBuildTableName},
		{r.contributionFQN, semanticInventoryContributionTableName},
		{r.resourceFQN, retainedResourceTableName},
	} {
		exists, err := r.tableExists(ctx, table.fqn)
		if err != nil {
			return err
		}
		if !exists {
			return &SemanticInventoryStorageMissingError{Schema: r.schema, Table: table.tableName}
		}
	}
	return nil
}

// WriteSemanticInventoryContributions persists a bounded contribution batch
// in chunks of at most batchSize rows (batchSize <= 0 selects the
// 500-row default, matching the Arango adapter). One multi-row INSERT per
// chunk is the arbitration point: ON CONFLICT DO NOTHING makes a duplicate
// content-addressed key a no-op skip, and the stored payload digest —
// recomputed in Go from the offered contribution, never a SQL-side
// re-encoding — decides whether the skip was an idempotent replay (equal
// digest) or a conflict (*SemanticInventoryContributionConflictError). A
// second, differently keyed contribution claiming the same
// (build, source, ordinal) slot is refused by the primary key as the same
// typed conflict. Malformed contributions are refused by name before any
// statement runs, and a batch offering one slot twice never partially
// writes. Chunk statements are individually atomic; a failure after an
// earlier chunk may leave earlier chunks persisted, which the checkpoint
// protocol makes harmless because a resumed scan rewrites those rows under
// identical keys and equal digests.
func (r *SemanticContributionRepository) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	if len(contributions) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = defaultSemanticContributionBatchSize
	}
	if batchSize > maxSemanticContributionChunkSize {
		// Clamp rather than refuse: the backend contract accepts
		// batchSize up to 5000 and a larger value carries no meaning
		// beyond "chunk no bigger than this".
		batchSize = maxSemanticContributionChunkSize
	}
	// Validate and encode every contribution before the first statement
	// runs, so a malformed batch writes nothing.
	preparedAll := make([]preparedContribution, 0, len(contributions))
	seenSlot := make(map[string]struct{}, len(contributions))
	for i, contribution := range contributions {
		p, err := prepareSemanticContribution(contribution)
		if err != nil {
			return fmt.Errorf("%w: contribution %d: %v", ErrInvalidSemanticInventoryContribution, i, err)
		}
		slotKey := semanticContributionSlotKey(p.contribution)
		if _, dup := seenSlot[slotKey]; dup {
			first := seenSlotFirst(preparedAll, slotKey)
			return &SemanticInventoryContributionConflictError{
				Project: first.Project, DatasetGeneration: first.DatasetGeneration,
				BuildID: first.BuildID, SourceKind: first.SourceKind,
				SourceID: first.SourceID, Ordinal: first.Ordinal,
				ContributionKey: first.Key, Slot: true,
			}
		}
		seenSlot[slotKey] = struct{}{}
		preparedAll = append(preparedAll, p)
	}

	for start := 0; start < len(preparedAll); start += batchSize {
		end := min(start+batchSize, len(preparedAll))
		if err := r.writeChunk(ctx, preparedAll[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// preparedContribution is one validated contribution with its canonical
// observation encoding and payload digest computed in Go.
type preparedContribution struct {
	contribution catalog.SemanticInventoryContribution
	observation  []byte
	digest       string
}

// writeChunk inserts one chunk with a single statement. Every row the
// statement skips on either unique index is classified per row in Go on
// both axes — the stored digest of the row's content key and the holder of
// the row's slot identity — so no skipped row can be mistaken for a
// successful insert: not a replay, not a content conflict, not a slot
// squat, and not a second slot claiming one content key under a forged
// caller-supplied key. Nothing is ever overwritten; conflicts are named
// with the offered row's identity.
func (r *SemanticContributionRepository) writeChunk(ctx context.Context, chunk []preparedContribution) error {
	var sql strings.Builder
	sql.WriteString("INSERT INTO " + r.contributionFQN +
		" (contribution_key, project, dataset_generation, build_id, source_kind," +
		" auth_resource_path, resource_type, source_id, ordinal, concept_id, binding_id," +
		" example, payload_digest, observation)")
	sql.WriteString(" VALUES ")
	args := make([]any, 0, len(chunk)*14)
	distinctKeys := make([]string, 0, len(chunk))
	seenKey := make(map[string]struct{}, len(chunk))
	parameter := 0
	for i := range chunk {
		c := chunk[i].contribution
		if i > 0 {
			sql.WriteString(", ")
		}
		sql.WriteString("(")
		for field := 0; field < 14; field++ {
			if field > 0 {
				sql.WriteString(", ")
			}
			parameter++
			sql.WriteString("$" + strconv.Itoa(parameter))
		}
		sql.WriteString(")")
		args = append(args,
			c.Key, c.Project, c.DatasetGeneration, c.BuildID, c.SourceKind,
			c.AuthResourcePath, c.ResourceType, c.SourceID, c.Ordinal,
			c.ConceptID, c.BindingID, c.Example, chunk[i].digest, chunk[i].observation)
		if _, dup := seenKey[c.Key]; !dup {
			distinctKeys = append(distinctKeys, c.Key)
			seenKey[c.Key] = struct{}{}
		}
	}
	// Unqualified DO NOTHING skips conflicts on either unique index (the
	// content-addressed key and the slot primary key) without raising.
	// An identical replay row conflicts on BOTH indexes at once, and a
	// targeted ON CONFLICT would only cover one of them, surfacing the
	// other as a 23505 on a pure replay. Every skip is classified in Go
	// below, so no skipped conflict — replay, slot squat, or concurrent
	// abort — is ever mistaken for the other.
	sql.WriteString(" ON CONFLICT DO NOTHING")

	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire semantic contribution connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, sql.String(), args...)
	if err != nil {
		return fmt.Errorf("postgres: write semantic inventory contributions: %w", err)
	}
	// The gate compares against the chunk's ROW count, not its distinct
	// key count: a caller-forged content key can repeat across different
	// slots, making the statement skip a row on the global key index while
	// every distinct key still looks fully inserted. Every short row is
	// then classified per row, on BOTH unique axes — the stored digest of
	// the content key and the holder of the row's slot — so no skip is
	// ever mistaken for a successful insert, and nothing is overwritten.
	if tag.RowsAffected() == int64(len(chunk)) {
		return nil
	}
	stored, err := r.readContributionDigests(ctx, conn, distinctKeys)
	if err != nil {
		return err
	}
	holders, err := r.readContributionSlotHolders(ctx, conn, chunk)
	if err != nil {
		return err
	}
	for i := range chunk {
		p := &chunk[i]
		c := p.contribution
		storedDigest, keyStored := stored[c.Key]
		holder, slotHeld := holders[semanticContributionSlotKey(c)]
		if !keyStored {
			if !slotHeld || holder == c.Key {
				// A concurrent writer that had claimed the identity rolled
				// back between this statement and the classification read:
				// nothing owns the identity, and this batch must not
				// proceed half-written — the caller retries.
				return fmt.Errorf(
					"postgres: semantic inventory contribution %s vanished between insert and classification; retry the batch",
					c.Key)
			}
			// The slot is held by a different contribution key.
			return &SemanticInventoryContributionConflictError{
				Project: c.Project, DatasetGeneration: c.DatasetGeneration,
				BuildID: c.BuildID, SourceKind: c.SourceKind,
				SourceID: c.SourceID, Ordinal: c.Ordinal,
				ContributionKey: c.Key, Slot: true,
			}
		}
		if storedDigest != p.digest {
			return &SemanticInventoryContributionConflictError{
				Project: c.Project, DatasetGeneration: c.DatasetGeneration,
				BuildID: c.BuildID, SourceKind: c.SourceKind,
				SourceID: c.SourceID, Ordinal: c.Ordinal,
				ContributionKey: c.Key,
				StoredDigest:    storedDigest, ProvidedDigest: p.digest,
			}
		}
		if !slotHeld || holder != c.Key {
			// The content key is stored with exactly this row's digest,
			// yet this row's own slot holds nothing (or another key): the
			// row was skipped on the global key index because a DIFFERENT
			// slot already committed this content key. A second slot
			// claiming one content identity is a refusal, not a replay:
			// the offered slot must not silently hold nothing.
			return &SemanticInventoryContributionConflictError{
				Project: c.Project, DatasetGeneration: c.DatasetGeneration,
				BuildID: c.BuildID, SourceKind: c.SourceKind,
				SourceID: c.SourceID, Ordinal: c.Ordinal,
				ContributionKey: c.Key, Slot: true,
				StoredDigest: storedDigest, ProvidedDigest: p.digest,
			}
		}
	}
	return nil
}

// readContributionSlotHolders fetches, in one statement, the contribution
// key stored at every chunk row's exact slot identity (project, dataset
// generation, build, source kind, source id, ordinal). The result is keyed
// by the length-prefixed slot identity; a slot absent from the result holds
// no row at all.
func (r *SemanticContributionRepository) readContributionSlotHolders(
	ctx context.Context, conn *pgxpool.Conn, chunk []preparedContribution,
) (map[string]string, error) {
	var query strings.Builder
	query.WriteString("SELECT project, dataset_generation, build_id, source_kind, source_id, ordinal, contribution_key FROM " +
		r.contributionFQN +
		" WHERE (project, dataset_generation, build_id, source_kind, source_id, ordinal) IN (VALUES ")
	args := make([]any, 0, len(chunk)*6)
	parameter := 0
	for i := range chunk {
		if i > 0 {
			query.WriteString(", ")
		}
		c := chunk[i].contribution
		query.WriteString("(")
		for field := 0; field < 6; field++ {
			if field > 0 {
				query.WriteString(", ")
			}
			parameter++
			if field == 5 {
				query.WriteString("$" + strconv.Itoa(parameter) + "::int")
			} else {
				query.WriteString("$" + strconv.Itoa(parameter) + "::text")
			}
		}
		query.WriteString(")")
		args = append(args, c.Project, c.DatasetGeneration, c.BuildID, c.SourceKind, c.SourceID, c.Ordinal)
	}
	query.WriteString(")")

	rows, err := conn.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: classify semantic inventory contribution slots: %w", err)
	}
	defer rows.Close()
	holders := make(map[string]string, len(chunk))
	for rows.Next() {
		var c catalog.SemanticInventoryContribution
		var heldKey string
		if err := rows.Scan(&c.Project, &c.DatasetGeneration, &c.BuildID, &c.SourceKind,
			&c.SourceID, &c.Ordinal, &heldKey); err != nil {
			return nil, fmt.Errorf("postgres: scan semantic inventory contribution slot: %w", err)
		}
		holders[semanticContributionSlotKey(c)] = heldKey
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read semantic inventory contribution slots: %w", err)
	}
	return holders, nil
}

// readContributionDigests fetches contribution_key → payload_digest for the
// named keys in one statement. The lookup is by identity only; every value
// comparison happens in Go on exact strings.
func (r *SemanticContributionRepository) readContributionDigests(ctx context.Context, conn *pgxpool.Conn, keys []string) (map[string]string, error) {
	rows, err := conn.Query(ctx,
		"SELECT contribution_key, payload_digest FROM "+r.contributionFQN+" WHERE contribution_key = ANY($1::text[])",
		keys)
	if err != nil {
		return nil, fmt.Errorf("postgres: classify semantic inventory contributions: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string, len(keys))
	for rows.Next() {
		var key, digest string
		if err := rows.Scan(&key, &digest); err != nil {
			return nil, fmt.Errorf("postgres: classify semantic inventory contributions: %w", err)
		}
		out[key] = digest
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: classify semantic inventory contributions: %w", err)
	}
	return out, nil
}

// ReadRetainedSemanticInventoryPage returns at most limit retained source
// rows of one exact (project, dataset_generation, resource_type) scope in
// stable byte-order logical_id keyset order — logical_id strictly greater
// than afterKey, ascending — without loading the resource type into
// application memory. Each row carries the row's authorization path (never
// SQL NULL in this store: the empty string is the legacy namespace) and the
// exact payload JSONB (SQL NULL stays a nil RawMessage, distinct from the
// bytes "null"). Both the keyset predicate and the sort are pinned to
// COLLATE "C": PostgreSQL compares exact bytes, matching the byte order the
// backfill's Go byte-order cursor guard applies, on any server collation.
// The dataset generation is normalized in Go before binding, exactly like
// every other catalog boundary.
//
// When the first page (afterKey == "") yields no rows the three-way
// source-availability reading is preserved: field-profile rows with
// doc_count > 0 make the missing population a contradiction
// (*RetainedSemanticSourceMissingError); a coverage marker is a verified
// known-empty population (SourceExists true with no rows); and neither
// marker leaves the source unproven (SourceExists false), matching the
// Arango adapter's distinctions. An empty page resumed mid-walk is simply
// the end of the collection.
func (r *SemanticContributionRepository) ReadRetainedSemanticInventoryPage(ctx context.Context, project, generation, collection, afterKey string, limit int) (catalog.RetainedSemanticInventoryPage, error) {
	if canonical, ok := fhirschema.ConcreteResourceType(collection); !ok || canonical != collection {
		return catalog.RetainedSemanticInventoryPage{}, fmt.Errorf(
			"%w: collection %q is not a canonical concrete resource type", ErrInvalidRetainedSemanticPageRequest, collection)
	}
	if strings.TrimSpace(project) == "" {
		return catalog.RetainedSemanticInventoryPage{}, fmt.Errorf("%w: project is blank", ErrInvalidRetainedSemanticPageRequest)
	}
	if limit < 1 || limit > maxRetainedSemanticPageLimit {
		return catalog.RetainedSemanticInventoryPage{}, fmt.Errorf("%w: limit is %d, want 1..%d",
			ErrInvalidRetainedSemanticPageRequest, limit, maxRetainedSemanticPageLimit)
	}
	generation = catalog.NormalizeDatasetGeneration(generation)

	// Ordered parameters: $1 project, $2 generation, $3 resource type,
	// optional $4 afterKey (byte order), last page size (+1 to detect
	// more).
	var sql strings.Builder
	sql.WriteString("SELECT logical_id, auth_resource_path, payload FROM " + r.resourceFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3")
	args := []any{project, generation, collection}
	if afterKey != "" {
		// Exact byte-order comparison and sort: COLLATE "C" on both sides
		// of the operator pins byte semantics regardless of the server's
		// default collation, so the store's order can never disagree with
		// the caller's Go byte-order cursor guard.
		sql.WriteString(` AND logical_id COLLATE "C" > $4 COLLATE "C"`)
		args = append(args, afterKey)
	}
	sql.WriteString(fmt.Sprintf(` ORDER BY logical_id COLLATE "C" LIMIT $%d`, len(args)+1))
	args = append(args, limit+1)

	page := catalog.RetainedSemanticInventoryPage{Rows: make([]catalog.RetainedSemanticInventoryRow, 0, limit)}
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		rows, err := conn.Query(ctx, sql.String(), args...)
		if err != nil {
			return fmt.Errorf("postgres: read retained %s source page: %w", collection, err)
		}
		defer rows.Close()
		for rows.Next() {
			var source catalog.RetainedSemanticInventoryRow
			var payload []byte
			if err := rows.Scan(&source.Key, &source.AuthResourcePath, &payload); err != nil {
				return fmt.Errorf("postgres: read retained %s source page: %w", collection, err)
			}
			if source.Key == "" {
				return fmt.Errorf("postgres: retained %s source row is missing logical_id", collection)
			}
			if payload != nil {
				source.Payload = json.RawMessage(payload)
			}
			page.Rows = append(page.Rows, source)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: read retained %s source page: %w", collection, err)
		}
		return nil
	})
	if err != nil {
		return catalog.RetainedSemanticInventoryPage{}, err
	}
	if len(page.Rows) > limit {
		page.Rows = page.Rows[:limit]
		page.HasMore = true
	}
	if len(page.Rows) == 0 && afterKey == "" {
		// First-page empty: keep the three-way availability reading.
		evidence, err := r.retainedSourceEvidence(ctx, project, generation, collection)
		if err != nil {
			return catalog.RetainedSemanticInventoryPage{}, err
		}
		switch evidence {
		case retainedEvidenceDocuments:
			return catalog.RetainedSemanticInventoryPage{}, &RetainedSemanticSourceMissingError{
				Project: project, DatasetGeneration: generation, ResourceType: collection,
			}
		case retainedEvidenceCoverage:
			page.SourceExists = true
		case retainedEvidenceNone:
			page.SourceExists = false
		}
	} else {
		page.SourceExists = true
	}
	return page, nil
}

// retainedEvidenceKind is the first-page evidence reading for an empty
// resource-type population.
type retainedEvidenceKind int

const (
	retainedEvidenceNone retainedEvidenceKind = iota
	retainedEvidenceCoverage
	retainedEvidenceDocuments
)

// retainedSourceEvidence reads the availability evidence for one scope from
// the field-profile tables: any field-profile row with doc_count > 0 is
// document evidence (the population was profiled with documents), otherwise
// any coverage marker is known-empty evidence, otherwise nothing is known.
// Absent evidence tables read as absent evidence, matching the Arango
// adapter's collection-existence handling.
func (r *SemanticContributionRepository) retainedSourceEvidence(ctx context.Context, project, generation, resourceType string) (retainedEvidenceKind, error) {
	documents, err := r.scopedEvidenceExists(ctx, r.fieldProfileFQN, project, generation, resourceType,
		" AND record_type = '' AND doc_count > 0")
	if err != nil {
		return retainedEvidenceNone, err
	}
	if documents {
		return retainedEvidenceDocuments, nil
	}
	coverage, err := r.scopedEvidenceExists(ctx, r.fieldProfileCovFQN, project, generation, resourceType, "")
	if err != nil {
		return retainedEvidenceNone, err
	}
	if coverage {
		return retainedEvidenceCoverage, nil
	}
	return retainedEvidenceNone, nil
}

// scopedEvidenceExists runs one bounded existence probe and treats a
// missing evidence table as no evidence.
func (r *SemanticContributionRepository) scopedEvidenceExists(ctx context.Context, fqn, project, generation, resourceType, extra string) (bool, error) {
	exists, err := r.tableExists(ctx, fqn)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	// Ordered parameters: $1 project, $2 generation, $3 resource type.
	var found bool
	err = r.withConn(ctx, func(conn *pgxpool.Conn) error {
		err := conn.QueryRow(ctx,
			"SELECT true FROM "+fqn+
				" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3"+extra+" LIMIT 1",
			project, generation, resourceType).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == undefinedTableSQLState42P01 {
				found = false
				return nil
			}
			return fmt.Errorf("postgres: probe semantic inventory source evidence %s: %w", fqn, err)
		}
		found = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// EnsureSemanticInventoryEntries refuses the entry-materialization request
// by name: entry rows are a separate slice. It never silently bumps the
// entry index version, so a caller can never mistake "no entries" for
// "entries current". The build identity is still validated against the
// shared contract before the refusal so a corrupted request fails as
// invalid rather than unsupported.
func (r *SemanticContributionRepository) EnsureSemanticInventoryEntries(ctx context.Context, build catalog.SemanticInventoryBuild) (catalog.SemanticInventoryBuild, error) {
	if err := validateSemanticInventoryBuildIdentity(build); err != nil {
		return catalog.SemanticInventoryBuild{}, err
	}
	return catalog.SemanticInventoryBuild{}, &SemanticInventoryEntriesUnsupportedError{
		Project:           build.Project,
		DatasetGeneration: catalog.NormalizeDatasetGeneration(build.DatasetGeneration),
		BuildID:           build.BuildID,
		EntryIndexVersion: build.EntryIndexVersion,
	}
}

// tableExists resolves one qualified table name.
func (r *SemanticContributionRepository) tableExists(ctx context.Context, fqn string) (bool, error) {
	var exists bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", fqn).Scan(&exists); err != nil {
			return fmt.Errorf("postgres: check semantic inventory table %s: %w", fqn, err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (r *SemanticContributionRepository) withConn(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire semantic contribution connection: %w", err)
	}
	defer conn.Release()
	return fn(conn)
}

// prepareSemanticContribution validates one contribution against the shared
// catalog contract and this store's identity discipline, normalizes the
// dataset generation, and encodes the observation exactly once for both the
// JSONB column and the payload digest. A malformed identity is refused by
// name; nothing is truncated or trimmed silently.
func prepareSemanticContribution(contribution catalog.SemanticInventoryContribution) (preparedContribution, error) {
	contribution.DatasetGeneration = catalog.NormalizeDatasetGeneration(contribution.DatasetGeneration)
	if !isLowerHex64(contribution.Key) {
		return preparedContribution{}, fmt.Errorf("contribution key %q is not 64 lowercase hex characters", contribution.Key)
	}
	if contribution.Project == "" || contribution.DatasetGeneration == "" || contribution.BuildID == "" ||
		contribution.SourceID == "" || contribution.ConceptID == "" || contribution.BindingID == "" ||
		contribution.ResourceType == "" {
		return preparedContribution{}, errors.New("contribution is missing stable identity")
	}
	if contribution.Ordinal < 0 {
		return preparedContribution{}, fmt.Errorf("contribution ordinal is %d, want >= 0", contribution.Ordinal)
	}
	switch contribution.SourceKind {
	case catalog.SemanticInventorySourceFile, catalog.SemanticInventorySourceRetained:
	default:
		return preparedContribution{}, fmt.Errorf("contribution source kind %q is unknown", contribution.SourceKind)
	}
	if contribution.BuildID != catalog.SemanticInventoryBuildID(contribution.Project, contribution.DatasetGeneration) {
		return preparedContribution{}, errors.New("contribution build does not match its generation")
	}
	observation, err := json.Marshal(contribution.Observation)
	if err != nil {
		return preparedContribution{}, fmt.Errorf("encode contribution observation: %w", err)
	}
	if len(observation) == 0 || observation[0] != '{' {
		return preparedContribution{}, errors.New("contribution observation does not encode as a JSON object")
	}
	return preparedContribution{
		contribution: contribution,
		observation:  observation,
		digest:       semanticContributionPayloadDigest(contribution, observation),
	}, nil
}

// semanticContributionPayloadDigest is the lowercase hex SHA-256 of the
// canonical encoding of the contribution's payload-bearing content. Every
// field is length-prefixed ("len:bytes") so distinct field boundaries
// cannot collide, following the pg-store-06 batchDigest convention. The
// digest is computed only in Go from the domain contribution: PostgreSQL
// stores and compares it as an opaque exact string and never re-encodes or
// canonicalizes payload content, so there is one source of truth for what
// "same payload" means.
func semanticContributionPayloadDigest(c catalog.SemanticInventoryContribution, observation []byte) string {
	h := sha256.New()
	put := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}
	put("loom.semantic-contribution/v1\n")
	put(c.Project)
	put(c.DatasetGeneration)
	put(c.BuildID)
	put(c.SourceKind)
	put(c.AuthResourcePath)
	put(c.ResourceType)
	put(c.SourceID)
	put("o" + strconv.Itoa(c.Ordinal))
	put(c.ConceptID)
	put(c.BindingID)
	put(c.Example)
	put(string(observation))
	return hex.EncodeToString(h.Sum(nil))
}

// semanticContributionSlotKey names the (build, source, ordinal) slot for
// in-batch duplicate detection, length-prefixed so no field boundary can
// alias another tuple.
func semanticContributionSlotKey(c catalog.SemanticInventoryContribution) string {
	frame := func(s string) string { return strconv.Itoa(len(s)) + ":" + s }
	return frame(c.Project) + frame(c.DatasetGeneration) + frame(c.BuildID) +
		frame(c.SourceKind) + frame(c.SourceID) + frame(strconv.Itoa(c.Ordinal))
}

// seenSlotFirst returns the first prepared contribution holding the slot,
// for naming the in-batch duplicate deterministically.
func seenSlotFirst(prepared []preparedContribution, slotKey string) catalog.SemanticInventoryContribution {
	for _, p := range prepared {
		if semanticContributionSlotKey(p.contribution) == slotKey {
			return p.contribution
		}
	}
	return catalog.SemanticInventoryContribution{}
}

// isLowerHex64 reports whether s is exactly 64 lowercase hex characters
// without allocating a regexp match.
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// SemanticInventoryBackfillStore is the composite backend
// ingest.BackfillSemanticInventory drives on PostgreSQL: contribution
// persistence and retained-source pages come from
// SemanticContributionRepository, and the claim/advance/complete/fail
// lifecycle is the reused pg-catalog-04 SemanticBuildRepository unchanged.
// Create one with NewSemanticInventoryBackfillStore. The structural match to
// the ingest backfill backend contract is pinned at the ingest call site in
// tests, so wiring regressions fail compilation there.
type SemanticInventoryBackfillStore struct {
	contributions *SemanticContributionRepository
	builds        *SemanticBuildRepository
}

// NewSemanticInventoryBackfillStore validates the two repositories it
// composes. Both must address the same schema; the caller owns that
// invariant along with the pool and schema lifecycle.
func NewSemanticInventoryBackfillStore(contributions *SemanticContributionRepository, builds *SemanticBuildRepository) (*SemanticInventoryBackfillStore, error) {
	if contributions == nil {
		return nil, errors.New("postgres: nil semantic contribution repository")
	}
	if builds == nil {
		return nil, errors.New("postgres: nil semantic build repository")
	}
	return &SemanticInventoryBackfillStore{contributions: contributions, builds: builds}, nil
}

// PrepareSemanticInventoryBackfill delegates to the contribution
// repository's migration check.
func (s *SemanticInventoryBackfillStore) PrepareSemanticInventoryBackfill(ctx context.Context) error {
	return s.contributions.PrepareSemanticInventoryBackfill(ctx)
}

// ReadRetainedSemanticInventoryPage delegates to the contribution
// repository's stable byte-order keyset page over retained resources.
func (s *SemanticInventoryBackfillStore) ReadRetainedSemanticInventoryPage(ctx context.Context, project, generation, collection, afterKey string, limit int) (catalog.RetainedSemanticInventoryPage, error) {
	return s.contributions.ReadRetainedSemanticInventoryPage(ctx, project, generation, collection, afterKey, limit)
}

// ClaimSemanticInventoryBackfill delegates to the reused build-state
// repository, retrying once when two concurrent first-claims collide on the
// global build_key unique index before the slot primary key is visible to
// both: the competing insert is either about to commit (the retry takes the
// ordinary upsert path and lands on the lease guard, i.e. the named
// claimed refusal) or about to abort (the retry claims cleanly). The
// build_key is derived only from (project, dataset_generation), so a
// genuine cross-scope collision cannot vanish on retry: a second identical
// failure still surfaces the corruption classification unchanged.
func (s *SemanticInventoryBackfillStore) ClaimSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, lease time.Duration) (catalog.SemanticInventoryBuild, bool, error) {
	out, claimed, err := s.builds.ClaimSemanticInventoryBackfill(ctx, build, token, lease)
	if isConcurrentSemanticBuildKeyRace(err) {
		out, claimed, err = s.builds.ClaimSemanticInventoryBackfill(ctx, build, token, lease)
	}
	return out, claimed, err
}

// isConcurrentSemanticBuildKeyRace reports the exact unique-violation
// signature of two first-claims racing on the derived build_key index: the
// reused classifier wraps any 23505 on fhir_semantic_inventory_build_key_ux
// as a corruption suspicion; this predicate selects the same signature for
// one bounded retry, which is what separates a transient race (converges)
// from corrupted sib_ derivation (repeats).
func isConcurrentSemanticBuildKeyRace(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == semanticInventoryBuildTableName+"_key_ux"
}

// AdvanceSemanticInventoryBackfill delegates to the reused build-state
// repository; the typed checkpoint moves only after the caller has
// persisted its contributions.
func (s *SemanticInventoryBackfillStore) AdvanceSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected, next catalog.SemanticInventoryCheckpoint, scanned int64, lease time.Duration) (catalog.SemanticInventoryBuild, error) {
	return s.builds.AdvanceSemanticInventoryBackfill(ctx, build, token, expected, next, scanned, lease)
}

// CompleteSemanticInventoryBackfill delegates to the reused build-state
// repository.
func (s *SemanticInventoryBackfillStore) CompleteSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected catalog.SemanticInventoryCheckpoint, scanned int64) (catalog.SemanticInventoryBuild, error) {
	return s.builds.CompleteSemanticInventoryBackfill(ctx, build, token, expected, scanned)
}

// FailSemanticInventoryBackfill delegates to the reused build-state
// repository.
func (s *SemanticInventoryBackfillStore) FailSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token, diagnostic string) error {
	return s.builds.FailSemanticInventoryBackfill(ctx, build, token, diagnostic)
}

// WriteSemanticInventoryContributions delegates to the contribution
// repository's idempotent conflict-refusing write.
func (s *SemanticInventoryBackfillStore) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	return s.contributions.WriteSemanticInventoryContributions(ctx, contributions, batchSize)
}

// EnsureSemanticInventoryEntries delegates the refusal: entry
// materialization is a separate slice and is never silently skipped.
func (s *SemanticInventoryBackfillStore) EnsureSemanticInventoryEntries(ctx context.Context, build catalog.SemanticInventoryBuild) (catalog.SemanticInventoryBuild, error) {
	return s.contributions.EnsureSemanticInventoryEntries(ctx, build)
}
