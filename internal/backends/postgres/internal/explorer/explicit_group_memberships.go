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
	ExplicitGroupMembershipsTable = "explorer_explicit_group_memberships"

	explicitGroupMembershipPageSize = 1000
)

const (
	explicitGroupMembershipPKConstraint       = "explorer_explicit_group_memberships_pkey"
	explicitGroupMembershipRevisionFK         = "explorer_explicit_group_memberships_revision_fkey"
	explicitGroupMembershipGroupFK            = "explorer_explicit_group_memberships_group_fkey"
	explicitGroupRevisionLifecycleValuesCheck = "explorer_explicit_group_revisions_lifecycle_values"
)

var _ explorer.ExplicitGroupRepository = (*ExplicitGroupStore)(nil)

var explicitGroupMembershipColumns = []string{
	"revision_id",
	"group_id",
	"project",
	"generation",
	"resource_type",
	"resource_id",
}

var explicitGroupMembershipScanColumns = explicitGroupMembershipColumns[1:]

type explicitGroupMembershipStatements struct {
	memberInsert         string
	memberGet            string
	memberPage           string
	memberDeleteRevision string
	memberDeleteBatch    string
	sourceSelection      string
	sourceMembers        string
	completeCAS          string
}

func newExplicitGroupMembershipStatements(schema string) explicitGroupMembershipStatements {
	members := sanitizeTableRef(schema, ExplicitGroupMembershipsTable)
	headers := sanitizeTableRef(schema, ExplicitGroupRevisionsTable)
	selHeaders := sanitizeTableRef(schema, SelectionRevisionsTable)
	selMembers := sanitizeTableRef(schema, SelectionMembersTable)
	mcols := strings.Join(explicitGroupMembershipColumns, ", ")
	scols := strings.Join(explicitGroupMembershipScanColumns, ", ")
	hcols := strings.Join(explicitGroupColumns, ", ")
	return explicitGroupMembershipStatements{
		memberInsert: "INSERT INTO " + members + " (" + mcols + ")" +
			" VALUES (" + orderedPlaceholders(len(explicitGroupMembershipColumns)) + ")" +
			" ON CONFLICT (" + mcols + ") DO NOTHING" +
			" RETURNING " + scols,
		memberGet: "SELECT " + scols + " FROM " + members +
			" WHERE revision_id = $1 AND group_id = $2 AND project = $3" +
			" AND generation = $4 AND resource_type = $5 AND resource_id = $6",
		memberPage: "SELECT " + scols + " FROM " + members +
			" WHERE revision_id = $1" +
			" AND (group_id > $2 OR (group_id = $2 AND resource_id > $3))" +
			" ORDER BY group_id, resource_id" +
			" LIMIT $4",
		memberDeleteRevision: "DELETE FROM " + members + " WHERE revision_id = $1",
		memberDeleteBatch:    "DELETE FROM " + members + " WHERE revision_id = ANY($1::text[])",
		sourceSelection: "SELECT " + strings.Join(selectionReadColumns, ", ") + " FROM " + selHeaders +
			" WHERE id = $1 AND project = $2",
		sourceMembers: "SELECT " + strings.Join(memberColumns, ", ") + " FROM " + selMembers +
			" WHERE selection_id = $1 AND project = $2 AND member_key = ANY($3::text[])",
		completeCAS: "UPDATE " + headers +
			" SET state = 'COMPLETE', definition_digest = $2, membership_digest = $3," +
			" group_count = $4, member_count = $5, completed_at = $6" +
			" WHERE id = $1 AND state = 'STAGING' AND writer_token = $7" +
			" RETURNING " + hcols,
	}
}

func membershipArgs(revisionID string, m explorer.ExplicitGroupMembership) []any {
	return []any{revisionID, string(m.GroupID), m.Ref.Project, m.Ref.Generation, m.Ref.ResourceType, m.Ref.ID}
}

func scanMembershipRow(row rowScanner, revisionID string) (explorer.ExplicitGroupMembership, error) {
	var (
		groupID, project, generation, resourceType, resourceID string
	)
	if err := row.Scan(&groupID, &project, &generation, &resourceType, &resourceID); err != nil {
		return explorer.ExplicitGroupMembership{}, err
	}
	membership := explorer.ExplicitGroupMembership{
		GroupID: explorer.ExplicitGroupID(groupID),
		Ref: explorer.ResourceRef{
			Project:      project,
			Generation:   generation,
			ResourceType: resourceType,
			ID:           resourceID,
		},
	}.Canonical()
	if string(membership.GroupID) != groupID ||
		membership.Ref.Project != project || membership.Ref.Generation != generation ||
		membership.Ref.ResourceType != resourceType || membership.Ref.ID != resourceID {
		return explorer.ExplicitGroupMembership{}, fmt.Errorf(
			"%w: membership %q under group %q of revision %q is stored non-canonically",
			explorer.ErrCorruptExplicitGroupRevision, resourceID, groupID, revisionID)
	}
	return membership, nil
}

func validateExplicitGroupMembershipRow(membership explorer.ExplicitGroupMembership, revisionID explorer.ExplicitGroupRevisionID, header *explorer.ExplicitGroupRevision, groupIDs map[explorer.ExplicitGroupID]struct{}) error {
	if _, declared := groupIDs[membership.GroupID]; !declared {
		return fmt.Errorf("%w: membership of revision %q names undeclared group %q",
			explorer.ErrCorruptExplicitGroupRevision, string(revisionID), string(membership.GroupID))
	}
	if err := membership.Ref.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
		return fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	return nil
}

func (s *ExplicitGroupStore) readExplicitGroupSourceSelection(ctx context.Context, conn explicitGroupConn, header *explorer.ExplicitGroupRevision) (*explorer.SelectionRevision, error) {
	stored, err := scanSelection(conn.QueryRow(ctx, s.memberStmts.sourceSelection,
		header.SourceSelectionRevisionID, header.Project))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrExplicitGroupRevisionStaleSource
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read explicit-group source selection %q of revision %q: %w",
			header.SourceSelectionRevisionID, string(header.ID), err)
	}
	source := stored.revision
	if err := header.ValidateSourceSelection(source); err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *ExplicitGroupStore) validateExplicitGroupSourceMemberships(ctx context.Context, conn explicitGroupConn, header *explorer.ExplicitGroupRevision, source explorer.SelectionRevision, memberships []explorer.ExplicitGroupMembership) error {
	if err := header.ValidateSourceSelection(source); err != nil {
		return err
	}
	for start := 0; start < len(memberships); start += explicitGroupMembershipPageSize {
		end := start + explicitGroupMembershipPageSize
		if end > len(memberships) {
			end = len(memberships)
		}
		batch := memberships[start:end]
		keys := make([]string, 0, len(batch))
		want := make(map[string]explorer.ResourceRef, len(batch))
		for _, membership := range batch {
			membership = membership.Canonical()
			key := selectionMemberKey(header.SourceSelectionRevisionID, membership.Ref)
			keys = append(keys, key)
			want[key] = membership.Ref
		}
		found := make(map[string]bool, len(keys))
		rows, err := conn.Query(ctx, s.memberStmts.sourceMembers,
			header.SourceSelectionRevisionID, header.Project, keys)
		if err != nil {
			return fmt.Errorf("postgres explorer store: read source members for revision %q: %w",
				string(header.ID), err)
		}
		for rows.Next() {
			var (
				memberKey, mProject, mGeneration, mResourceType, mID string
				ordinal                                              int64
			)
			if err := rows.Scan(&memberKey, &mProject, &mGeneration, &mResourceType, &mID, &ordinal); err != nil {
				rows.Close()
				return fmt.Errorf("postgres explorer store: scan source member for revision %q: %w",
					string(header.ID), err)
			}
			member := explorer.SelectionMember{
				MemberKey: memberKey,
				Ref: explorer.ResourceRef{
					Project:      mProject,
					Generation:   mGeneration,
					ResourceType: mResourceType,
					ID:           mID,
				},
			}
			ref, ok := want[memberKey]
			if !ok || member.Ref.Canonical() != ref || member.MemberKey != memberKey {
				rows.Close()
				return explorer.ErrCorruptExplicitGroupRevision
			}
			if err := member.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
				rows.Close()
				return err
			}
			found[memberKey] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("postgres explorer store: read source members for revision %q: %w",
				string(header.ID), err)
		}
		rows.Close()
		for key := range want {
			if !found[key] {
				return explorer.ErrExplicitGroupMemberNotInSelection
			}
		}
	}
	return nil
}

func (s *ExplicitGroupStore) digestExplicitGroupChildren(ctx context.Context, conn explicitGroupConn, domain *explorer.ExplicitGroupRevision, stored *storedExplicitGroupHeader) (string, string, int64, int64, error) {
	groups, err := s.listExplicitGroupDefinitions(ctx, conn, string(domain.ID), stored)
	if err != nil {
		return "", "", 0, 0, err
	}
	definitionDigest, err := explorer.ExplicitGroupDefinitionDigest(groups)
	if err != nil {
		return "", "", 0, 0, err
	}
	groupIDs := make(map[explorer.ExplicitGroupID]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}
	source, err := s.readExplicitGroupSourceSelection(ctx, conn, domain)
	if err != nil {
		return "", "", 0, 0, err
	}
	hash := sha256.New()
	explorer.StartExplicitGroupMembershipDigest(hash)
	var memberCount int64
	afterGroupID, afterResourceID := "", ""
	for {
		pageCount := 0
		var page []explorer.ExplicitGroupMembership
		rows, err := conn.Query(ctx, s.memberStmts.memberPage,
			string(domain.ID), afterGroupID, afterResourceID, explicitGroupMembershipPageSize)
		if err != nil {
			return "", "", 0, 0, fmt.Errorf("postgres explorer store: stream explicit-group memberships %q: %w",
				string(domain.ID), err)
		}
		for rows.Next() {
			membership, err := scanMembershipRow(rows, string(domain.ID))
			if err != nil {
				rows.Close()
				return "", "", 0, 0, fmt.Errorf("postgres explorer store: scan explicit-group membership %q: %w",
					string(domain.ID), err)
			}
			if err := validateExplicitGroupMembershipRow(membership, domain.ID, domain, groupIDs); err != nil {
				rows.Close()
				return "", "", 0, 0, err
			}
			explorer.WriteExplicitGroupMembershipFrame(hash, membership)
			memberCount++
			pageCount++
			page = append(page, membership)
			afterGroupID, afterResourceID = string(membership.GroupID), membership.Ref.ID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", "", 0, 0, fmt.Errorf("postgres explorer store: stream explicit-group memberships %q: %w",
				string(domain.ID), err)
		}
		rows.Close()
		if err := ctx.Err(); err != nil {
			return "", "", 0, 0, err
		}
		if s.membershipPageStreamed != nil {
			s.membershipPageStreamed(pageCount)
		}
		if len(page) > 0 {
			if err := s.validateExplicitGroupSourceMemberships(ctx, conn, domain, *source, page); err != nil {
				return "", "", 0, 0, err
			}
		}
		if pageCount < explicitGroupMembershipPageSize {
			break
		}
	}
	return definitionDigest, hex.EncodeToString(hash.Sum(nil)), int64(len(groups)), memberCount, nil
}

func (s *ExplicitGroupStore) AppendExplicitGroupMemberships(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string, memberships []explorer.ExplicitGroupMembership) ([]explorer.ExplicitGroupMembership, error) {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if len(memberships) == 0 {
		return nil, nil
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire append-memberships connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin append memberships %q: %w", revisionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var header storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revisionID)), &header)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: lock explicit-group header %q: %w", revisionID, err)
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return nil, err
	}
	domain, err := header.toDomain(revisionID)
	if err != nil {
		return nil, err
	}
	groups, err := s.listExplicitGroupDefinitions(ctx, tx, string(revisionID), &header)
	if err != nil {
		return nil, err
	}
	canonical, err := explorer.CanonicalExplicitGroupMemberships(*domain, groups, memberships)
	if err != nil {
		return nil, err
	}
	source, err := s.readExplicitGroupSourceSelection(ctx, tx, domain)
	if err != nil {
		return nil, err
	}
	if err := s.validateExplicitGroupSourceMemberships(ctx, tx, domain, *source, canonical); err != nil {
		return nil, err
	}

	inserted := make([]explorer.ExplicitGroupMembership, 0, len(canonical))
	for _, membership := range canonical {
		var (
			groupID, project, generation, resourceType, resourceID string
		)
		err := tx.QueryRow(ctx, s.memberStmts.memberInsert,
			membershipArgs(string(revisionID), membership)...).
			Scan(&groupID, &project, &generation, &resourceType, &resourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			var (
				sGroupID, sProject, sGeneration, sResourceType, sResourceID string
			)
			scanErr := tx.QueryRow(ctx, s.memberStmts.memberGet,
				string(revisionID), string(membership.GroupID), membership.Ref.Project,
				membership.Ref.Generation, membership.Ref.ResourceType, membership.Ref.ID).
				Scan(&sGroupID, &sProject, &sGeneration, &sResourceType, &sResourceID)
			if errors.Is(scanErr, pgx.ErrNoRows) ||
				(scanErr == nil && (sGroupID != string(membership.GroupID) ||
					sProject != membership.Ref.Project || sGeneration != membership.Ref.Generation ||
					sResourceType != membership.Ref.ResourceType || sResourceID != membership.Ref.ID)) {
				return nil, explorer.ErrExplicitGroupRevisionConflict
			}
			if scanErr != nil {
				return nil, fmt.Errorf("postgres explorer store: reread staged membership of %q under group %q: %w",
					revisionID, string(membership.GroupID), scanErr)
			}
			continue
		}
		if err != nil {
			if pgstore.IsUniqueConstraintViolation(err, explicitGroupMembershipPKConstraint) {
				return nil, errors.Join(explorer.ErrExplicitGroupRevisionConflict, err)
			}
			if pgstore.IsForeignKeyConstraintViolation(err, explicitGroupMembershipGroupFK, explicitGroupMembershipRevisionFK) {
				return nil, errors.Join(explorer.ErrExplicitGroupRevisionConflict, err)
			}
			return nil, fmt.Errorf("postgres explorer store: stage explicit-group membership of %q under group %q: %w",
				revisionID, string(membership.GroupID), err)
		}
		if groupID != string(membership.GroupID) || project != membership.Ref.Project ||
			generation != membership.Ref.Generation || resourceType != membership.Ref.ResourceType ||
			resourceID != membership.Ref.ID {
			return nil, fmt.Errorf("postgres explorer store: staged membership of %q under group %q reread mismatch",
				revisionID, string(membership.GroupID))
		}
		inserted = append(inserted, membership)
	}

	if s.afterMembershipsInserted != nil {
		if err := s.afterMembershipsInserted(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: append memberships %q after membership insert: %w",
				revisionID, err)
		}
	}

	if err := s.renewExplicitGroupWriter(ctx, tx, revisionID, writerToken); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit append memberships %q: %w", revisionID, err)
	}
	return inserted, nil
}

func (s *ExplicitGroupStore) DigestExplicitGroupRevision(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) (string, string, int64, int64, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return "", "", 0, 0, explorer.ErrExplicitGroupRevisionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("postgres explorer store: acquire digest-memberships connection: %w", err)
	}
	defer conn.Release()

	stored, err := s.readHeaderUnlocked(ctx, conn, project, revisionID)
	if err != nil {
		return "", "", 0, 0, err
	}
	domain, err := stored.toDomain(revisionID)
	if err != nil {
		return "", "", 0, 0, err
	}
	if domain.State == explorer.ExplicitGroupRevisionComplete {
		return domain.DefinitionDigest, domain.MembershipDigest, domain.GroupCount, domain.MemberCount, nil
	}
	return s.digestExplicitGroupChildren(ctx, conn, domain, stored)
}

func (s *ExplicitGroupStore) CompleteExplicitGroupRevision(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken, definitionDigest, membershipDigest string, groupCount, memberCount int64, completedAt time.Time) (*explorer.ExplicitGroupRevision, error) {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	definitionDigest, membershipDigest = strings.TrimSpace(definitionDigest), strings.TrimSpace(membershipDigest)
	if revisionID == "" || writerToken == "" || definitionDigest == "" || membershipDigest == "" ||
		groupCount <= 0 || memberCount < 0 {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire complete-revision connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin complete revision %q: %w", revisionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var header storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revisionID)), &header)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: lock explicit-group header %q for complete: %w", revisionID, err)
	}
	domain, err := header.toDomain(revisionID)
	if err != nil {
		return nil, err
	}
	if domain.State == explorer.ExplicitGroupRevisionComplete {
		if domain.DefinitionDigest != definitionDigest || domain.MembershipDigest != membershipDigest ||
			domain.GroupCount != groupCount || domain.MemberCount != memberCount {
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
		return domain, nil
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return nil, err
	}

	actualDefinition, actualMembership, actualGroups, actualMembers, err :=
		s.digestExplicitGroupChildren(ctx, tx, domain, &header)
	if err != nil {
		return nil, err
	}
	if actualDefinition != definitionDigest || actualMembership != membershipDigest ||
		actualGroups != groupCount || actualMembers != memberCount {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}

	if _, err := tx.Exec(ctx, s.stmts.setWriterTokenGUC, writerToken); err != nil {
		return nil, fmt.Errorf("postgres explorer store: set writer token guard for complete %q: %w", revisionID, err)
	}
	var completed storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.memberStmts.completeCAS,
		string(revisionID), definitionDigest, membershipDigest, groupCount, memberCount,
		completedAt.UTC(), writerToken), &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: complete explicit-group revision %q: %w", revisionID, err)
	}
	completedDomain, err := completed.toDomain(revisionID)
	if err != nil {
		return nil, err
	}

	if s.beforeCompleteCommit != nil {
		if err := s.beforeCompleteCommit(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: complete explicit-group revision %q before commit: %w",
				revisionID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit complete explicit-group revision %q: %w", revisionID, err)
	}
	return completedDomain, nil
}

func (s *ExplicitGroupStore) VisitExplicitGroupMemberships(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID, after explorer.ExplicitGroupMembershipCursor, pageSize int, visit func(explorer.ExplicitGroupMembership) error) (explorer.ExplicitGroupMembershipCursor, error) {
	if visit == nil {
		return after, fmt.Errorf("explicit group membership visitor is required")
	}
	header, err := s.GetExplicitGroupRevision(ctx, project, revisionID)
	if err != nil {
		return after, err
	}
	if pageSize <= 0 {
		pageSize = explicitGroupMembershipPageSize
	} else if pageSize > explicitGroupMembershipPageSize {
		pageSize = explicitGroupMembershipPageSize
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return after, fmt.Errorf("postgres explorer store: acquire visit-memberships connection: %w", err)
	}
	defer conn.Release()

	stored, err := s.readHeaderUnlocked(ctx, conn, project, revisionID)
	if err != nil {
		return after, err
	}
	groups, err := s.listExplicitGroupDefinitions(ctx, conn, string(revisionID), stored)
	if err != nil {
		return after, err
	}
	groupIDs := make(map[explorer.ExplicitGroupID]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}

	page := make([]explorer.ExplicitGroupMembership, 0, pageSize)
	rows, err := conn.Query(ctx, s.memberStmts.memberPage,
		string(revisionID), string(after.GroupID), after.ResourceID, pageSize)
	if err != nil {
		return after, fmt.Errorf("postgres explorer store: page explicit-group memberships %q: %w", revisionID, err)
	}
	for rows.Next() {
		membership, err := scanMembershipRow(rows, string(revisionID))
		if err != nil {
			rows.Close()
			return after, fmt.Errorf("postgres explorer store: scan explicit-group membership page %q: %w", revisionID, err)
		}
		if err := validateExplicitGroupMembershipRow(membership, revisionID, header, groupIDs); err != nil {
			rows.Close()
			return after, err
		}
		page = append(page, membership)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return after, fmt.Errorf("postgres explorer store: page explicit-group memberships %q: %w", revisionID, err)
	}
	rows.Close()

	for _, membership := range page {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		if err := visit(membership); err != nil {
			return after, err
		}
		after = explorer.ExplicitGroupMembershipCursor{GroupID: membership.GroupID, ResourceID: membership.Ref.ID}
	}
	return after, nil
}
