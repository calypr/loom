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
	semanticInventoryContributionTableName = "fhir_semantic_inventory_contribution"
	retainedResourceTableName              = "fhir_resource"
	fieldProfileTableName                  = "fhir_field_profile"
	fieldProfileCoverageTableName          = "fhir_field_profile_coverage"
	maxRetainedSemanticPageLimit           = 1000
	defaultSemanticContributionBatchSize   = 500
	maxSemanticContributionChunkSize       = 1000
	undefinedTableSQLState42P01            = "42P01"
)

var (
	ErrSemanticInventoryContributionConflict = errors.New("postgres: semantic inventory contribution conflict")
	ErrInvalidSemanticInventoryContribution  = errors.New("postgres: invalid semantic inventory contribution")
	ErrInvalidRetainedSemanticPageRequest    = errors.New("postgres: invalid retained semantic inventory page request")
	ErrRetainedSemanticSourceMissing         = errors.New("postgres: retained semantic inventory source is missing despite field-profile evidence")
	ErrSemanticInventoryStorageMissing       = errors.New("postgres: semantic inventory storage is not migrated")
	ErrSemanticInventoryEntriesUnsupported   = errors.New("postgres: semantic inventory entry materialization is not persisted by this slice")
)

type SemanticInventoryContributionConflictError struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	SourceKind        string
	SourceID          string
	Ordinal           int
	ContributionKey   string
	Slot              bool
	StoredDigest      string
	ProvidedDigest    string
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

type SemanticContributionRepository struct {
	src                storepostgres.ConnSource
	schema             string
	contributionFQN    string
	buildFQN           string
	resourceFQN        string
	fieldProfileFQN    string
	fieldProfileCovFQN string
}

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

func (r *SemanticContributionRepository) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	if len(contributions) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = defaultSemanticContributionBatchSize
	}
	if batchSize > maxSemanticContributionChunkSize {
		batchSize = maxSemanticContributionChunkSize
	}
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

type preparedContribution struct {
	contribution catalog.SemanticInventoryContribution
	observation  []byte
	digest       string
}

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
				return fmt.Errorf(
					"postgres: semantic inventory contribution %s vanished between insert and classification; retry the batch",
					c.Key)
			}
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

	var sql strings.Builder
	sql.WriteString("SELECT logical_id, auth_resource_path, payload FROM " + r.resourceFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3")
	args := []any{project, generation, collection}
	if afterKey != "" {
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

type retainedEvidenceKind int

const (
	retainedEvidenceNone retainedEvidenceKind = iota
	retainedEvidenceCoverage
	retainedEvidenceDocuments
)

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

func (r *SemanticContributionRepository) scopedEvidenceExists(ctx context.Context, fqn, project, generation, resourceType, extra string) (bool, error) {
	exists, err := r.tableExists(ctx, fqn)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
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

func semanticContributionSlotKey(c catalog.SemanticInventoryContribution) string {
	frame := func(s string) string { return strconv.Itoa(len(s)) + ":" + s }
	return frame(c.Project) + frame(c.DatasetGeneration) + frame(c.BuildID) +
		frame(c.SourceKind) + frame(c.SourceID) + frame(strconv.Itoa(c.Ordinal))
}

func seenSlotFirst(prepared []preparedContribution, slotKey string) catalog.SemanticInventoryContribution {
	for _, p := range prepared {
		if semanticContributionSlotKey(p.contribution) == slotKey {
			return p.contribution
		}
	}
	return catalog.SemanticInventoryContribution{}
}

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

type SemanticInventoryBackfillStore struct {
	contributions *SemanticContributionRepository
	builds        *SemanticBuildRepository
}

func NewSemanticInventoryBackfillStore(contributions *SemanticContributionRepository, builds *SemanticBuildRepository) (*SemanticInventoryBackfillStore, error) {
	if contributions == nil {
		return nil, errors.New("postgres: nil semantic contribution repository")
	}
	if builds == nil {
		return nil, errors.New("postgres: nil semantic build repository")
	}
	return &SemanticInventoryBackfillStore{contributions: contributions, builds: builds}, nil
}

func (s *SemanticInventoryBackfillStore) PrepareSemanticInventoryBackfill(ctx context.Context) error {
	return s.contributions.PrepareSemanticInventoryBackfill(ctx)
}

func (s *SemanticInventoryBackfillStore) ReadRetainedSemanticInventoryPage(ctx context.Context, project, generation, collection, afterKey string, limit int) (catalog.RetainedSemanticInventoryPage, error) {
	return s.contributions.ReadRetainedSemanticInventoryPage(ctx, project, generation, collection, afterKey, limit)
}

func (s *SemanticInventoryBackfillStore) ClaimSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, lease time.Duration) (catalog.SemanticInventoryBuild, bool, error) {
	out, claimed, err := s.builds.ClaimSemanticInventoryBackfill(ctx, build, token, lease)
	if isConcurrentSemanticBuildKeyRace(err) {
		out, claimed, err = s.builds.ClaimSemanticInventoryBackfill(ctx, build, token, lease)
	}
	return out, claimed, err
}

func isConcurrentSemanticBuildKeyRace(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == semanticInventoryBuildTableName+"_key_ux"
}

func (s *SemanticInventoryBackfillStore) AdvanceSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected, next catalog.SemanticInventoryCheckpoint, scanned int64, lease time.Duration) (catalog.SemanticInventoryBuild, error) {
	return s.builds.AdvanceSemanticInventoryBackfill(ctx, build, token, expected, next, scanned, lease)
}

func (s *SemanticInventoryBackfillStore) CompleteSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected catalog.SemanticInventoryCheckpoint, scanned int64) (catalog.SemanticInventoryBuild, error) {
	return s.builds.CompleteSemanticInventoryBackfill(ctx, build, token, expected, scanned)
}

func (s *SemanticInventoryBackfillStore) FailSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token, diagnostic string) error {
	return s.builds.FailSemanticInventoryBackfill(ctx, build, token, diagnostic)
}

func (s *SemanticInventoryBackfillStore) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	return s.contributions.WriteSemanticInventoryContributions(ctx, contributions, batchSize)
}

func (s *SemanticInventoryBackfillStore) EnsureSemanticInventoryEntries(ctx context.Context, build catalog.SemanticInventoryBuild) (catalog.SemanticInventoryBuild, error) {
	return s.contributions.EnsureSemanticInventoryEntries(ctx, build)
}
