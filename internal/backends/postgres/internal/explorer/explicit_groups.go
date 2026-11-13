package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const (
	ExplicitGroupRevisionsTable   = "explorer_explicit_group_revisions"
	ExplicitGroupDefinitionsTable = "explorer_explicit_group_definitions"
)

const (
	explicitGroupWriterLease  = 10 * time.Minute
	explicitGroupCleanupLimit = 100
	explicitGroupListLimit    = 100
)

const (
	gucWriterToken = "loom.explorer_explicit_group_writer_token"
	gucAbortToken  = "loom.explorer_explicit_group_abort_token"
	gucCleanupNow  = "loom.explorer_explicit_group_cleanup_now"
)

const (
	explicitGroupDefinitionPKConstraint   = "explorer_explicit_group_definitions_pkey"
	explicitGroupDefinitionOrdinalCK      = "explorer_explicit_group_definitions_revision_ordinal_key"
	explicitGroupRevisionFKConstraint     = "explorer_explicit_group_definitions_revision_fkey"
	explicitGroupRevisionIdentityCheck    = "explorer_explicit_group_revisions_identity_binding"
	explicitGroupRevisionRequiredFieldsCK = "explorer_explicit_group_revisions_required_fields"
)

var explicitGroupColumns = []string{
	"id",
	"project",
	"generation",
	"scope_digest",
	"resource_type",
	"source_selection_revision_id",
	"source_membership_digest",
	"definition_digest",
	"membership_digest",
	"group_count",
	"member_count",
	"state",
	"idempotency_key",
	"created_at",
	"completed_at",
	"writer_token",
	"writer_expires_at",
}

var explicitGroupDefinitionColumns = []string{
	"revision_id",
	"group_id",
	"label",
	"ordinal",
}

type explicitGroupStatements struct {
	headerInsert      string
	headerGet         string
	headerGetScoped   string
	headerClaim       string
	headerRenew       string
	headerAbort       string
	headerDeleteBatch string
	defList           string
	defDeleteBatch    string
	cleanupSelect     string
	listComplete      string
	setWriterTokenGUC string
	setAbortTokenGUC  string
	setCleanupNowGUC  string
	defInsertBase     string
}

func newExplicitGroupStatements(schema string) explicitGroupStatements {
	headerTable := sanitizeTableRef(schema, ExplicitGroupRevisionsTable)
	defTable := sanitizeTableRef(schema, ExplicitGroupDefinitionsTable)
	hcols := strings.Join(explicitGroupColumns, ", ")
	hph := orderedPlaceholders(len(explicitGroupColumns))
	dcols := strings.Join(explicitGroupDefinitionColumns, ", ")
	return explicitGroupStatements{
		headerInsert: "INSERT INTO " + headerTable + " (" + hcols + ") VALUES (" + hph + ") " +
			"ON CONFLICT (id) DO NOTHING RETURNING " + hcols,
		headerGet: "SELECT " + hcols + " FROM " + headerTable + " WHERE id = $1",
		headerGetScoped: "SELECT " + hcols + " FROM " + headerTable +
			" WHERE project = $1 AND id = $2",
		headerClaim: "SELECT " + hcols + " FROM " + headerTable + " WHERE id = $1 FOR UPDATE",
		headerRenew: "UPDATE " + headerTable + " SET writer_expires_at = $2 " +
			"WHERE id = $1 AND state = 'STAGING' AND writer_token = $3",
		headerAbort: "DELETE FROM " + headerTable +
			" WHERE id = $1 AND state = 'STAGING' AND writer_token = $2",
		headerDeleteBatch: "DELETE FROM " + headerTable + " WHERE id = ANY($1::text[])",
		defList: "SELECT group_id, label, ordinal FROM " + defTable +
			" WHERE revision_id = $1 ORDER BY ordinal, group_id",
		defDeleteBatch: "DELETE FROM " + defTable + " WHERE revision_id = ANY($1::text[])",
		cleanupSelect: "SELECT id FROM " + headerTable +
			" WHERE state = 'STAGING' AND created_at < $1" +
			" AND (writer_expires_at IS NULL OR writer_expires_at < $2)" +
			" ORDER BY created_at, id LIMIT $3 FOR UPDATE SKIP LOCKED",
		listComplete: "SELECT " + hcols + " FROM " + headerTable +
			" WHERE project = $1 AND generation = $2 AND scope_digest = $3" +
			" AND resource_type = $4 AND state = 'COMPLETE'" +
			" ORDER BY created_at DESC, id LIMIT $5",
		setWriterTokenGUC: "SELECT set_config('" + gucWriterToken + "', $1, true)",
		setAbortTokenGUC:  "SELECT set_config('" + gucAbortToken + "', $1, true)",
		setCleanupNowGUC:  "SELECT set_config('" + gucCleanupNow + "', $1, true)",
		defInsertBase:     "INSERT INTO " + defTable + " (" + dcols + ") VALUES ",
	}
}

func (s explicitGroupStatements) defInsertStatement(n int) string {
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, fmt.Sprintf("($%d, $%d, $%d, $%d)", 4*i+1, 4*i+2, 4*i+3, 4*i+4))
	}
	return s.defInsertBase + strings.Join(rows, ", ")
}

type explicitGroupConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var (
	_ explicitGroupConn = (*pgxpool.Conn)(nil)
	_ explicitGroupConn = pgx.Tx(nil)
)

type ExplicitGroupStore struct {
	client                   *pgstore.Client
	stmts                    explicitGroupStatements
	memberStmts              explicitGroupMembershipStatements
	afterDefinitionsInserted func(context.Context, pgx.Tx) error
	afterDefinitionsDeleted  func(context.Context, pgx.Tx) error
	afterMembershipsInserted func(context.Context, pgx.Tx) error
	beforeCompleteCommit     func(context.Context, pgx.Tx) error
	membershipPageStreamed   func(rows int)
}

func NewExplicitGroupStore(client *pgstore.Client, schema string) (*ExplicitGroupStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: explicit-group client is required")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &ExplicitGroupStore{
		client:      client,
		stmts:       newExplicitGroupStatements(schema),
		memberStmts: newExplicitGroupMembershipStatements(schema),
	}, nil
}

type storedExplicitGroupHeader struct {
	ID                        string
	Project                   string
	Generation                string
	ScopeDigest               string
	ResourceType              string
	SourceSelectionRevisionID string
	SourceMembershipDigest    string
	DefinitionDigest          string
	MembershipDigest          string
	GroupCount                int64
	MemberCount               int64
	State                     string
	IdempotencyKey            string
	CreatedAt                 time.Time
	CompletedAt               *time.Time
	WriterToken               *string
	WriterExpiresAt           *time.Time
}

func scanExplicitGroupHeader(row rowScanner, out *storedExplicitGroupHeader) error {
	return row.Scan(
		&out.ID,
		&out.Project,
		&out.Generation,
		&out.ScopeDigest,
		&out.ResourceType,
		&out.SourceSelectionRevisionID,
		&out.SourceMembershipDigest,
		&out.DefinitionDigest,
		&out.MembershipDigest,
		&out.GroupCount,
		&out.MemberCount,
		&out.State,
		&out.IdempotencyKey,
		&out.CreatedAt,
		&out.CompletedAt,
		&out.WriterToken,
		&out.WriterExpiresAt,
	)
}

func headerArgs(revision explorer.ExplicitGroupRevision, writerToken string, writerExpiresAt time.Time) []any {
	return []any{
		string(revision.ID),
		revision.Project,
		revision.Generation,
		revision.ScopeDigest,
		revision.ResourceType,
		revision.SourceSelectionRevisionID,
		revision.SourceMembershipDigest,
		"",
		"",
		int64(0),
		int64(0),
		string(explorer.ExplicitGroupRevisionStaging),
		revision.IdempotencyKey,
		revision.CreatedAt,
		(*time.Time)(nil),
		writerToken,
		writerExpiresAt,
	}
}

func (h *storedExplicitGroupHeader) toDomain(expectedID explorer.ExplicitGroupRevisionID) (*explorer.ExplicitGroupRevision, error) {
	if expectedID != "" && h.ID != string(expectedID) {
		return nil, fmt.Errorf("%w: header stored id %q is filed under %q",
			explorer.ErrCorruptExplicitGroupRevision, h.ID, string(expectedID))
	}
	revision := explorer.ExplicitGroupRevision{
		ID:                        explorer.ExplicitGroupRevisionID(h.ID),
		Project:                   h.Project,
		Generation:                h.Generation,
		ScopeDigest:               h.ScopeDigest,
		ResourceType:              h.ResourceType,
		SourceSelectionRevisionID: h.SourceSelectionRevisionID,
		SourceMembershipDigest:    h.SourceMembershipDigest,
		DefinitionDigest:          h.DefinitionDigest,
		MembershipDigest:          h.MembershipDigest,
		GroupCount:                h.GroupCount,
		MemberCount:               h.MemberCount,
		State:                     explorer.ExplicitGroupRevisionState(h.State),
		IdempotencyKey:            h.IdempotencyKey,
		CreatedAt:                 h.CreatedAt,
		CompletedAt:               h.CompletedAt,
	}.Canonical()
	if err := revision.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	return &revision, nil
}

func guardExplicitGroupWriter(header *storedExplicitGroupHeader, writerToken string) error {
	if header == nil {
		return explorer.ErrExplicitGroupRevisionNotFound
	}
	if header.State != string(explorer.ExplicitGroupRevisionStaging) {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	stored := ""
	if header.WriterToken != nil {
		stored = strings.TrimSpace(*header.WriterToken)
	}
	if stored != writerToken {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return nil
}

func (s *ExplicitGroupStore) BeginExplicitGroupRevision(ctx context.Context, revision explorer.ExplicitGroupRevision, source explorer.SelectionRevision, writerToken string) (*explorer.ExplicitGroupRevision, error) {
	revision = revision.Canonical()
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if err := revision.Validate(); err != nil {
		return nil, err
	}
	if err := revision.ValidateSourceSelection(source); err != nil {
		return nil, err
	}
	if revision.State != explorer.ExplicitGroupRevisionStaging {
		return nil, fmt.Errorf("explicit group revision must begin in staging state")
	}
	if err := s.CleanupExplicitGroupStaging(ctx, time.Now().UTC(), explicitGroupCleanupLimit); err != nil {
		return nil, err
	}

	writerExpiresAt := time.Now().UTC().Add(explicitGroupWriterLease)
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group begin connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin explicit-group claim %q: %w", revision.ID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stored storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerInsert,
		headerArgs(revision, writerToken, writerExpiresAt)...), &stored)
	if err == nil {
		domain, err := stored.toDomain(revision.ID)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: decode claimed header %q: %w", revision.ID, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: commit explicit-group claim %q: %w", revision.ID, err)
		}
		return domain, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres explorer store: claim explicit-group header %q: %w", revision.ID, err)
	}

	var winner storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revision.ID)), &winner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: conflicting explicit-group claim %q vanished before it became visible",
			explorer.ErrExplicitGroupRevisionConflict, revision.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read conflicting explicit-group header %q: %w", revision.ID, err)
	}
	winnerDomain, err := winner.toDomain(revision.ID)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: decode conflicting header %q: %w", revision.ID, err)
	}
	if !sameExplicitGroupRevisionIntent(*winnerDomain, revision) {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if winnerDomain.State == explorer.ExplicitGroupRevisionStaging {
		if err := guardExplicitGroupWriter(&winner, writerToken); err != nil {
			return nil, err
		}
		renewedAt := time.Now().UTC().Add(explicitGroupWriterLease)
		if _, err := tx.Exec(ctx, s.stmts.setWriterTokenGUC, writerToken); err != nil {
			return nil, fmt.Errorf("postgres explorer store: set writer token guard for begin %q: %w", revision.ID, err)
		}
		tag, err := tx.Exec(ctx, s.stmts.headerRenew, string(revision.ID), renewedAt, writerToken)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: renew explicit-group writer lease %q: %w", revision.ID, err)
		}
		if tag.RowsAffected() != 1 {
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit explicit-group begin %q: %w", revision.ID, err)
	}
	return winnerDomain, nil
}

func sameExplicitGroupRevisionIntent(left, right explorer.ExplicitGroupRevision) bool {
	left, right = left.Canonical(), right.Canonical()
	return left.ID == right.ID &&
		left.Project == right.Project &&
		left.Generation == right.Generation &&
		left.ScopeDigest == right.ScopeDigest &&
		left.ResourceType == right.ResourceType &&
		left.SourceSelectionRevisionID == right.SourceSelectionRevisionID &&
		left.SourceMembershipDigest == right.SourceMembershipDigest &&
		left.IdempotencyKey == right.IdempotencyKey
}

func (s *ExplicitGroupStore) PutExplicitGroupDefinitions(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string, groups []explorer.ExplicitGroupDefinition) error {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return err
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire explicit-group put connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin explicit-group put %q: %w", revisionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var header storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revisionID)), &header)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.ErrExplicitGroupRevisionNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres explorer store: lock explicit-group header %q: %w", revisionID, err)
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return err
	}

	stored, err := s.listExplicitGroupDefinitions(ctx, tx, string(revisionID), &header)
	if err != nil {
		return err
	}
	if len(stored) > 0 {
		if !reflect.DeepEqual(stored, canonical) {
			return explorer.ErrExplicitGroupRevisionConflict
		}
		if err := s.renewExplicitGroupWriter(ctx, tx, revisionID, writerToken); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres explorer store: commit explicit-group put %q: %w", revisionID, err)
		}
		return nil
	}

	if len(canonical) > 0 {
		args := make([]any, 0, 4*len(canonical))
		for _, group := range canonical {
			args = append(args, string(revisionID), string(group.ID), group.Label, group.Ordinal)
		}
		if _, err := tx.Exec(ctx, s.stmts.defInsertStatement(len(canonical)), args...); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == duplicateKeySQLState &&
				(pgErr.ConstraintName == explicitGroupDefinitionPKConstraint ||
					pgErr.ConstraintName == explicitGroupDefinitionOrdinalCK) {
				return errors.Join(explorer.ErrExplicitGroupRevisionConflict, err)
			}
			return fmt.Errorf("postgres explorer store: insert explicit-group definitions for %q: %w", revisionID, err)
		}

		if s.afterDefinitionsInserted != nil {
			if err := s.afterDefinitionsInserted(ctx, tx); err != nil {
				return fmt.Errorf("postgres explorer store: put explicit-group definitions %q after definition insert: %w",
					revisionID, err)
			}
		}

		verified, err := s.listExplicitGroupDefinitions(ctx, tx, string(revisionID), &header)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(verified, canonical) {
			return explorer.ErrExplicitGroupRevisionConflict
		}
	}
	if err := s.renewExplicitGroupWriter(ctx, tx, revisionID, writerToken); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit explicit-group put %q: %w", revisionID, err)
	}
	return nil
}

func (s *ExplicitGroupStore) renewExplicitGroupWriter(ctx context.Context, tx pgx.Tx, revisionID explorer.ExplicitGroupRevisionID, writerToken string) error {
	if _, err := tx.Exec(ctx, s.stmts.setWriterTokenGUC, writerToken); err != nil {
		return fmt.Errorf("postgres explorer store: set writer token guard for %q: %w", revisionID, err)
	}
	tag, err := tx.Exec(ctx, s.stmts.headerRenew, string(revisionID),
		time.Now().UTC().Add(explicitGroupWriterLease), writerToken)
	if err != nil {
		return fmt.Errorf("postgres explorer store: renew explicit-group writer lease %q: %w", revisionID, err)
	}
	if tag.RowsAffected() != 1 {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return nil
}

func (s *ExplicitGroupStore) listExplicitGroupDefinitions(ctx context.Context, conn explicitGroupConn, revisionID string, header *storedExplicitGroupHeader) ([]explorer.ExplicitGroupDefinition, error) {
	groups := make([]explorer.ExplicitGroupDefinition, 0)
	rows, err := conn.Query(ctx, s.stmts.defList, revisionID)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group definitions %q: %w", revisionID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			groupID string
			label   string
			ordinal int64
		)
		if err := rows.Scan(&groupID, &label, &ordinal); err != nil {
			return nil, fmt.Errorf("postgres explorer store: scan explicit-group definition %q: %w", revisionID, err)
		}
		group := explorer.ExplicitGroupDefinition{ID: explorer.ExplicitGroupID(groupID), Label: label, Ordinal: ordinal}
		canonicalGroup := group.Canonical()
		if string(canonicalGroup.ID) != groupID || canonicalGroup.Label != label {
			return nil, fmt.Errorf("%w: definition %q of revision %q is stored non-canonically",
				explorer.ErrCorruptExplicitGroupRevision, groupID, revisionID)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group definitions %q: %w", revisionID, err)
	}
	if len(groups) == 0 && header.State == string(explorer.ExplicitGroupRevisionStaging) {
		return groups, nil
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	if !reflect.DeepEqual(canonical, groups) {
		return nil, explorer.ErrCorruptExplicitGroupRevision
	}
	if header.State == string(explorer.ExplicitGroupRevisionComplete) {
		digest, err := explorer.ExplicitGroupDefinitionDigest(groups)
		if err != nil || int64(len(groups)) != header.GroupCount || digest != header.DefinitionDigest {
			return nil, explorer.ErrCorruptExplicitGroupRevision
		}
	}
	return groups, nil
}

func (s *ExplicitGroupStore) AbortExplicitGroupRevision(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string) error {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	if revisionID == "" {
		return nil
	}
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return explorer.ErrExplicitGroupRevisionConflict
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire explicit-group abort connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin explicit-group abort %q: %w", revisionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var header storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revisionID)), &header)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("postgres explorer store: lock explicit-group header %q: %w", revisionID, err)
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, s.stmts.setAbortTokenGUC, writerToken); err != nil {
		return fmt.Errorf("postgres explorer store: set abort guard for %q: %w", revisionID, err)
	}
	if _, err := tx.Exec(ctx, s.memberStmts.memberDeleteRevision, string(revisionID)); err != nil {
		return fmt.Errorf("postgres explorer store: delete explicit-group memberships for abort %q: %w", revisionID, err)
	}
	if _, err := tx.Exec(ctx, s.stmts.defDeleteBatch, []string{string(revisionID)}); err != nil {
		return fmt.Errorf("postgres explorer store: delete explicit-group definitions for abort %q: %w", revisionID, err)
	}

	if s.afterDefinitionsDeleted != nil {
		if err := s.afterDefinitionsDeleted(ctx, tx); err != nil {
			return fmt.Errorf("postgres explorer store: abort explicit-group revision %q after definition delete: %w",
				revisionID, err)
		}
	}

	tag, err := tx.Exec(ctx, s.stmts.headerAbort, string(revisionID), writerToken)
	if err != nil {
		return fmt.Errorf("postgres explorer store: delete explicit-group header %q: %w", revisionID, err)
	}
	if tag.RowsAffected() != 1 {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit explicit-group abort %q: %w", revisionID, err)
	}
	return nil
}

func (s *ExplicitGroupStore) CleanupExplicitGroupStaging(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = explicitGroupCleanupLimit
	}
	now := time.Now().UTC()

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire explicit-group cleanup connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin explicit-group cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, s.stmts.setCleanupNowGUC, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("postgres explorer store: set cleanup guard: %w", err)
	}
	rows, err := tx.Query(ctx, s.stmts.cleanupSelect, before, now, limit)
	if err != nil {
		return fmt.Errorf("postgres explorer store: select expired explicit-group headers: %w", err)
	}
	var expired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("postgres explorer store: scan expired explicit-group headers: %w", err)
		}
		expired = append(expired, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres explorer store: select expired explicit-group headers: %w", err)
	}
	if len(expired) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres explorer store: commit empty explicit-group cleanup: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, s.memberStmts.memberDeleteBatch, expired); err != nil {
		return fmt.Errorf("postgres explorer store: delete memberships of expired explicit-group headers: %w", err)
	}
	if _, err := tx.Exec(ctx, s.stmts.defDeleteBatch, expired); err != nil {
		return fmt.Errorf("postgres explorer store: delete definitions of expired explicit-group headers: %w", err)
	}
	tag, err := tx.Exec(ctx, s.stmts.headerDeleteBatch, expired)
	if err != nil {
		return fmt.Errorf("postgres explorer store: delete expired explicit-group headers: %w", err)
	}
	if int(tag.RowsAffected()) != len(expired) {
		return fmt.Errorf("postgres explorer store: cleanup removed %d explicit-group headers, selected %d: %w",
			tag.RowsAffected(), len(expired), explorer.ErrExplicitGroupRevisionConflict)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit explicit-group cleanup: %w", err)
	}
	return nil
}

func (s *ExplicitGroupStore) GetExplicitGroupRevision(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) (*explorer.ExplicitGroupRevision, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group get connection: %w", err)
	}
	defer conn.Release()

	header, err := s.readHeaderUnlocked(ctx, conn, project, revisionID)
	if err != nil {
		return nil, err
	}
	domain, err := header.toDomain(revisionID)
	if err != nil {
		return nil, err
	}
	if domain.State != explorer.ExplicitGroupRevisionComplete {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	if _, err := s.listExplicitGroupDefinitions(ctx, conn, string(revisionID), header); err != nil {
		return nil, err
	}
	return domain, nil
}

func (s *ExplicitGroupStore) ListExplicitGroupRevisions(ctx context.Context, project, generation, scopeDigest, resourceType string, limit int) ([]explorer.ExplicitGroupRevision, error) {
	if limit <= 0 {
		limit = explicitGroupListLimit
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group list connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, s.stmts.listComplete, project, generation, scopeDigest, resourceType, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group revisions %q/%q/%q/%q: %w",
			project, generation, scopeDigest, resourceType, err)
	}
	defer rows.Close()
	stored := make([]storedExplicitGroupHeader, 0)
	for rows.Next() {
		var header storedExplicitGroupHeader
		if err := scanExplicitGroupHeader(rows, &header); err != nil {
			return nil, fmt.Errorf("postgres explorer store: scan explicit-group revision %q: %w", project, err)
		}
		stored = append(stored, header)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group revisions %q: %w", project, err)
	}
	rows.Close()
	revisions := make([]explorer.ExplicitGroupRevision, 0, len(stored))
	for i := range stored {
		domain, err := stored[i].toDomain("")
		if err != nil {
			return nil, err
		}
		if _, err := s.listExplicitGroupDefinitions(ctx, conn, stored[i].ID, &stored[i]); err != nil {
			return nil, err
		}
		revisions = append(revisions, *domain)
	}
	return revisions, nil
}

func (s *ExplicitGroupStore) ListExplicitGroupDefinitions(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) ([]explorer.ExplicitGroupDefinition, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group definitions connection: %w", err)
	}
	defer conn.Release()
	stored, err := s.readHeaderUnlocked(ctx, conn, project, revisionID)
	if err != nil {
		return nil, err
	}
	domain, err := stored.toDomain(revisionID)
	if err != nil {
		return nil, err
	}
	if domain.State != explorer.ExplicitGroupRevisionComplete {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	return s.listExplicitGroupDefinitions(ctx, conn, string(revisionID), stored)
}

func (s *ExplicitGroupStore) readHeaderUnlocked(ctx context.Context, conn explicitGroupConn, project string, revisionID explorer.ExplicitGroupRevisionID) (*storedExplicitGroupHeader, error) {
	var header storedExplicitGroupHeader
	var err error
	if strings.TrimSpace(project) == "" {
		err = scanExplicitGroupHeader(conn.QueryRow(ctx, s.stmts.headerGet, string(revisionID)), &header)
	} else {
		err = scanExplicitGroupHeader(conn.QueryRow(ctx, s.stmts.headerGetScoped, strings.TrimSpace(project), string(revisionID)), &header)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read explicit-group header %q: %w", revisionID, err)
	}
	return &header, nil
}
