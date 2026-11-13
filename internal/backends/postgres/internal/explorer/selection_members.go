package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const (
	selectionDigestPageSize       = 1000
	selectionMembersRefConstraint = "selection_members_ref_ux"
)

func selectionMemberKey(selectionID string, ref explorer.ResourceRef) string {
	h := sha256.New()
	for _, part := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return selectionID + "_" + hex.EncodeToString(h.Sum(nil))
}

var memberColumns = []string{
	"member_key",
	"project",
	"generation",
	"resource_type",
	"id",
	"ordinal",
}

type selectionMemberSQL struct {
	memberInsert     string
	memberByKey      string
	memberDigestPage string
	memberVisitPage  string
	leaseRenew       string
	completeCAS      string
}

func newSelectionMemberSQL(schema string) selectionMemberSQL {
	members := pgx.Identifier{schema, SelectionMembersTable}.Sanitize()
	headers := pgx.Identifier{schema, SelectionRevisionsTable}.Sanitize()
	readCols := strings.Join(selectionReadColumns, ", ")
	return selectionMemberSQL{
		memberInsert: "INSERT INTO " + members +
			" (selection_id, " + strings.Join(memberColumns, ", ") + ")" +
			" VALUES (" + orderedPlaceholders(len(memberColumns)+1) + ")" +
			" ON CONFLICT (selection_id, member_key) DO NOTHING" +
			" RETURNING " + strings.Join(memberColumns, ", "),
		memberByKey: "SELECT " + strings.Join(memberColumns, ", ") + " FROM " + members +
			" WHERE selection_id = $1 AND member_key = $2",
		memberDigestPage: "SELECT project, generation, resource_type, id FROM " + members +
			" WHERE selection_id = $1 AND project = $2 AND generation = $3 AND resource_type = $4 AND id > $5" +
			" ORDER BY id LIMIT $6",
		memberVisitPage: "SELECT " + strings.Join(memberColumns, ", ") + " FROM " + members +
			" WHERE selection_id = $1 AND project = $2 AND member_key > $3" +
			" ORDER BY member_key LIMIT $4",
		leaseRenew: "UPDATE " + headers + " SET writer_expires_at = $2" +
			" WHERE id = $1 AND writer_token = $3 AND state = '" + selectionStagingState + "'",
		completeCAS: "UPDATE " + headers + " SET state = '" + selectionCompleteState + "', complete = true," +
			" membership_digest = $2, member_count = $3, member_bytes = $4, completed_at = $5" +
			" WHERE id = $1 AND state = '" + selectionStagingState + "' AND writer_token = $6" +
			" RETURNING " + readCols,
	}
}

func (s *Store) streamMemberFacts(ctx context.Context, q selectionQuerier, project, generation, resourceType, selectionID string) (string, int64, int64, error) {
	hash := sha256.New()
	var count, byteTotal int64
	afterID := ""
	for {
		pageCount := 0
		rows, err := q.Query(ctx, s.selectionMembers.memberDigestPage,
			selectionID, project, generation, resourceType, afterID, selectionDigestPageSize)
		if err != nil {
			return "", 0, 0, fmt.Errorf("postgres explorer store: stream selection members %q: %w", selectionID, err)
		}
		for rows.Next() {
			var ref explorer.ResourceRef
			if err := rows.Scan(&ref.Project, &ref.Generation, &ref.ResourceType, &ref.ID); err != nil {
				rows.Close()
				return "", 0, 0, fmt.Errorf("postgres explorer store: scan selection member %q: %w", selectionID, err)
			}
			ref = ref.Canonical()
			explorer.WriteMembershipFrame(hash, ref)
			for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
				byteTotal += int64(8 + len(value))
			}
			count++
			pageCount++
			afterID = ref.ID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", 0, 0, fmt.Errorf("postgres explorer store: iterate selection members %q: %w", selectionID, err)
		}
		rows.Close()
		if s.memberPageStreamed != nil {
			s.memberPageStreamed(pageCount)
		}
		if pageCount < selectionDigestPageSize {
			break
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), count, byteTotal, nil
}

func isMemberRefConflict(err error) bool {
	return pgstore.IsUniqueConstraintViolation(err, selectionMembersRefConstraint)
}

func (s *Store) AppendSelectionMembers(ctx context.Context, selectionID, writerToken string, members []explorer.SelectionMember) ([]explorer.SelectionMember, error) {
	selectionID = strings.TrimSpace(selectionID)
	writerToken = strings.TrimSpace(writerToken)
	if selectionID == "" {
		return nil, fmt.Errorf("selection id is required")
	}
	if writerToken == "" {
		return nil, explorer.ErrSelectionConflict
	}
	if len(members) == 0 {
		return nil, nil
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire append-members connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin append members %q: %w", selectionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storedToken, state string
	var complete bool
	var project, generation, resourceType string
	err = tx.QueryRow(ctx, s.selections.headerForAppend, selectionID).
		Scan(&storedToken, &state, &complete, &project, &generation, &resourceType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrSelectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: lock selection header %q: %w", selectionID, err)
	}
	if strings.TrimSpace(storedToken) != writerToken {
		return nil, explorer.ErrSelectionConflict
	}
	if complete || (state != "" && state != selectionStagingState) {
		return nil, explorer.ErrSelectionConflict
	}

	type pendingMember struct {
		memberKey string
		member    explorer.SelectionMember
	}
	batch := make([]pendingMember, 0, len(members))
	for _, raw := range members {
		member := raw.Canonical()
		expectedMemberKey := selectionMemberKey(selectionID, member.Ref)
		if member.MemberKey != "" && member.MemberKey != expectedMemberKey {
			return nil, explorer.ErrSelectionConflict
		}
		member.MemberKey = expectedMemberKey
		if err := member.Ref.Validate(project, generation, resourceType); err != nil {
			return nil, err
		}
		batch = append(batch, pendingMember{memberKey: expectedMemberKey, member: member})
	}

	inserted := make([]explorer.SelectionMember, 0, len(batch))
	for _, entry := range batch {
		var (
			memberKey, mProject, mGeneration, mResourceType, mID string
			ordinal                                              int64
		)
		err := tx.QueryRow(ctx, s.selectionMembers.memberInsert,
			selectionID, entry.memberKey, entry.member.Ref.Project, entry.member.Ref.Generation,
			entry.member.Ref.ResourceType, entry.member.Ref.ID, entry.member.Ordinal).
			Scan(&memberKey, &mProject, &mGeneration, &mResourceType, &mID, &ordinal)
		if errors.Is(err, pgx.ErrNoRows) {
			var skippedKey, sProject, sGeneration, sResourceType, sID string
			var sOrdinal int64
			scanErr := tx.QueryRow(ctx, s.selectionMembers.memberByKey, selectionID, entry.memberKey).
				Scan(&skippedKey, &sProject, &sGeneration, &sResourceType, &sID, &sOrdinal)
			if errors.Is(scanErr, pgx.ErrNoRows) {
				continue
			}
			if scanErr != nil {
				return nil, fmt.Errorf("postgres explorer store: reread staged member %q of selection %q: %w",
					entry.memberKey, selectionID, scanErr)
			}
			if skippedKey != entry.memberKey ||
				sProject != entry.member.Ref.Project || sGeneration != entry.member.Ref.Generation ||
				sResourceType != entry.member.Ref.ResourceType || sID != entry.member.Ref.ID {
				return nil, explorer.ErrSelectionConflict
			}
			continue
		}
		if err != nil {
			if isMemberRefConflict(err) {
				return nil, explorer.ErrSelectionConflict
			}
			return nil, fmt.Errorf("postgres explorer store: stage member %q of selection %q: %w",
				entry.memberKey, selectionID, err)
		}
		if memberKey != entry.memberKey || mProject != entry.member.Ref.Project ||
			mGeneration != entry.member.Ref.Generation || mResourceType != entry.member.Ref.ResourceType ||
			mID != entry.member.Ref.ID || ordinal != entry.member.Ordinal {
			return nil, fmt.Errorf("postgres explorer store: staged member %q of selection %q reread mismatch",
				entry.memberKey, selectionID)
		}
		inserted = append(inserted, entry.member)
	}

	if s.afterMemberInsert != nil {
		if err := s.afterMemberInsert(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: append members %q after member insert: %w",
				selectionID, err)
		}
	}

	renewAt := time.Now().UTC().Add(selectionWriterLease)
	tag, err := tx.Exec(ctx, s.selectionMembers.leaseRenew, selectionID, renewAt, writerToken)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: renew selection writer lease %q: %w", selectionID, err)
	}
	if tag.RowsAffected() != 1 {
		return nil, explorer.ErrSelectionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit append members %q: %w", selectionID, err)
	}
	return inserted, nil
}

func (s *Store) DigestSelectionMembers(ctx context.Context, project, selectionID string) (string, int64, int64, error) {
	selectionID = strings.TrimSpace(selectionID)
	project = strings.TrimSpace(project)
	if selectionID == "" || project == "" {
		return "", 0, 0, explorer.ErrSelectionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return "", 0, 0, fmt.Errorf("postgres explorer store: acquire digest-members connection: %w", err)
	}
	defer conn.Release()

	header, err := s.readSelectionRow(ctx, conn, s.selections.headerScoped, selectionID, project)
	if err != nil {
		return "", 0, 0, err
	}
	if header.revision.Complete {
		return header.revision.MembershipDigest, header.revision.MemberCount, header.revision.MemberBytes, nil
	}
	return s.streamMemberFacts(ctx, conn, project, header.revision.Generation, header.revision.ResourceType, selectionID)
}

func (s *Store) CompleteSelection(ctx context.Context, selectionID, writerToken, digest string, count, bytes int64, completedAt time.Time) (*explorer.SelectionRevision, error) {
	selectionID = strings.TrimSpace(selectionID)
	writerToken = strings.TrimSpace(writerToken)
	if selectionID == "" || strings.TrimSpace(digest) == "" || count < 0 || bytes < 0 {
		return nil, explorer.ErrSelectionIncomplete
	}
	if writerToken == "" {
		return nil, explorer.ErrSelectionConflict
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire complete-selection connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin complete selection %q: %w", selectionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storedToken, state string
	var complete bool
	var project, generation, resourceType string
	err = tx.QueryRow(ctx, s.selections.headerForAppend, selectionID).
		Scan(&storedToken, &state, &complete, &project, &generation, &resourceType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrSelectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: lock selection header %q for complete: %w", selectionID, err)
	}
	if complete {
		stored, err := s.readSelectionRow(ctx, tx, s.selections.headerByID, selectionID)
		if err != nil {
			return nil, err
		}
		if stored.revision.MembershipDigest != digest ||
			stored.revision.MemberCount != count || stored.revision.MemberBytes != bytes {
			return nil, explorer.ErrSelectionConflict
		}
		revision := stored.revision
		return &revision, nil
	}
	if strings.TrimSpace(storedToken) != writerToken {
		return nil, explorer.ErrSelectionConflict
	}
	if state != "" && state != selectionStagingState {
		return nil, explorer.ErrSelectionConflict
	}

	stagedDigest, stagedCount, _, err := s.streamMemberFacts(ctx, tx, project, generation, resourceType, selectionID)
	if err != nil {
		return nil, err
	}
	if stagedDigest != digest || stagedCount != count {
		return nil, explorer.ErrSelectionIncomplete
	}

	winner, err := s.readSelectionRowTx(ctx, tx, s.selectionMembers.completeCAS,
		selectionID, digest, count, bytes, completedAt.UTC(), writerToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrSelectionConflict
	}
	if err != nil {
		return nil, err
	}
	if err := winner.revision.Validate(); err != nil {
		return nil, err
	}

	if s.beforeCompleteCommit != nil {
		if err := s.beforeCompleteCommit(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: complete selection %q before commit: %w",
				selectionID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit complete selection %q: %w", selectionID, err)
	}
	revision := winner.revision
	return &revision, nil
}

func (s *Store) VisitSelectionMembers(ctx context.Context, project, selectionID, after string, pageSize int, visit func(explorer.SelectionMember) error) (string, error) {
	if visit == nil {
		return "", fmt.Errorf("selection member visitor is required")
	}
	header, err := s.GetSelection(ctx, project, selectionID)
	if err != nil {
		return "", err
	}
	if !header.Complete {
		return "", explorer.ErrSelectionIncomplete
	}
	if pageSize <= 0 {
		pageSize = selectionDigestPageSize
	} else if pageSize > selectionDigestPageSize {
		pageSize = selectionDigestPageSize
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres explorer store: acquire visit-members connection: %w", err)
	}
	defer conn.Release()

	last := strings.TrimSpace(after)
	page := make([]explorer.SelectionMember, 0, pageSize)
	keys := make([]string, 0, pageSize)
	rows, err := conn.Query(ctx, s.selectionMembers.memberVisitPage, selectionID, strings.TrimSpace(project), last, pageSize)
	if err != nil {
		return last, fmt.Errorf("postgres explorer store: page selection members %q: %w", selectionID, err)
	}
	for rows.Next() {
		var member explorer.SelectionMember
		if err := rows.Scan(&member.MemberKey, &member.Ref.Project, &member.Ref.Generation,
			&member.Ref.ResourceType, &member.Ref.ID, &member.Ordinal); err != nil {
			rows.Close()
			return last, fmt.Errorf("postgres explorer store: scan selection member page %q: %w", selectionID, err)
		}
		member = member.Canonical()
		page = append(page, member)
		keys = append(keys, member.MemberKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return last, fmt.Errorf("postgres explorer store: iterate selection member page %q: %w", selectionID, err)
	}
	rows.Close()
	for i, member := range page {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		if err := visit(member); err != nil {
			return last, err
		}
		last = keys[i]
	}
	return last, nil
}
