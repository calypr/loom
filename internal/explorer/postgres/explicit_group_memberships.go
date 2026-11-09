package postgres

// Explicit-group membership batching, streaming, and completion on
// PostgreSQL 18 (pg-state-explicit-group-members-v1).
//
// This file is the membership half of the relational counterpart of
// internal/explorer/arango/explicit_groups.go: the header/definition half
// (Begin, Put, Abort, Cleanup, Get, List) lives in explicit_groups.go,
// while Append, Digest, Complete, and Visit -- the methods the Arango
// adapter's membership lifecycle exposes -- live here method for method so
// TestPostgresExplicitGroupMemberships can hold the same guarantees against
// real PG18:
//
//   - Append stages one bounded batch of relations in ONE transaction:
//     the header is locked FOR UPDATE, its writer token and STAGING state
//     are verified, the batch is canonicalized against the stored
//     definitions (a relation naming an undeclared group is the domain's
//     plain error), every relation is checked against the immutable source
//     selection (readExplicitGroupSourceSelection and
//     validateExplicitGroupSourceMemberships, the Arango adapter's
//     per-append boundary checks), and only then are the insert-once
//     membership rows written and the writer lease renewed. A resource may
//     occur under several GroupIDs -- the primary key carries the group,
//     so overlaps are distinct rows -- while a duplicate retry of the same
//     (revision, group, reference) collides on that key and creates
//     nothing, exactly like the adapter's UPSERT/OLD==null accounting.
//   - Digest returns the frozen facts of a COMPLETE revision without
//     re-streaming, and for STAGING recomputes the definition digest and
//     the membership digest/count by paging the membership rows in
//     byte-order (group_id, resource_id) through the shared
//     WriteExplicitGroupMembershipFrame framing, source-validating each
//     page -- one implementation shared with Complete, so the facts a
//     caller may finalize are exactly the facts a reader may digest.
//   - Complete is the fenced finalization: under the header lock it
//     re-derives the whole definitions-plus-membership set and compares
//     every claimed fact (definition digest, membership digest, group
//     count, member count); a mismatch refuses with the typed conflict and
//     flips NOTHING -- the revision stays STAGING, which every reader path
//     refuses as unusable. Only on agreement does the CAS flip the row,
//     and the migration's guard re-verifies authorization and the claimed
//     counts against the child rows at write time, so the transition is a
//     database fact as well as a transactional one.
//   - Visit streams at most one bounded page of COMPLETE membership per
//     call over the composite keyset cursor, honors cancellation between
//     members, and never materializes the full membership set.
//
// Database-enforced invariants (DDL in
// 000010_explorer_explicit_group_memberships.sql; this file contains no
// DDL):
//   - explorer_explicit_group_memberships PRIMARY KEY
//     (revision_id, group_id, project, generation, resource_type,
//     resource_id) makes insert-once retries idempotent while legitimate
//     cross-group overlaps survive.
//   - The compound foreign key to (revision_id, group_id) in the
//     definitions table refuses memberships naming an undeclared group.
//   - The membership guard trigger admits INSERT only under a STAGING
//     parent whose scope the reference repeats exactly, refuses every real
//     UPDATE, and admits DELETE only inside an authorized parent teardown.
//   - The header guard's completion channel admits STAGING -> COMPLETE
//     only for the session holding the row's writer token and only with
//     counts equal to the stored child rows; COMPLETE rows are frozen.
//
// There is no fallback to any other backend: every failure is returned
// typed for the caller to surface.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/explorer"
)

const (
	// ExplicitGroupMembershipsTable holds one row per
	// (revision, group, resource reference): the relations that make a
	// group what it is, including the same resource under several groups.
	ExplicitGroupMembershipsTable = "explorer_explicit_group_memberships"

	// explicitGroupMembershipPageSize bounds every membership page -- the
	// digest stream, the visit walk, and the source-validation batches --
	// exactly like the Arango adapter's explicitGroupPageSize of 1000.
	explicitGroupMembershipPageSize = 1000
)

// Constraint names the membership slice classifies. Matching is always
// SQLSTATE plus the exact constraint name so a violation of a different
// guard can never masquerade as the expected outcome.
const (
	explicitGroupMembershipPKConstraint       = "explorer_explicit_group_memberships_pkey"
	explicitGroupMembershipRevisionFK         = "explorer_explicit_group_memberships_revision_fkey"
	explicitGroupMembershipGroupFK            = "explorer_explicit_group_memberships_group_fkey"
	explicitGroupMembershipForeignKeyState    = "23503"
	explicitGroupRevisionLifecycleValuesCheck = "explorer_explicit_group_revisions_lifecycle_values"
)

// _ExplicitGroupRepository conformance: with this file's four methods the
// ExplicitGroupStore is the complete PostgreSQL counterpart of
// explorer.ExplicitGroupRepository.
var _ explorer.ExplicitGroupRepository = (*ExplicitGroupStore)(nil)

// explicitGroupMembershipColumns is the single source of truth for
// membership read/write order: the INSERT column list, the RETURNING/SELECT
// lists, the argument order, and the scan order all derive from it, so
// ordered SQL parameters ($1..$N) match the Go argument slice by
// construction. Every column is part of the primary key; there is no
// payload column to drift.
var explicitGroupMembershipColumns = []string{
	"revision_id",
	"group_id",
	"project",
	"generation",
	"resource_type",
	"resource_id",
}

// explicitGroupMembershipScanColumns is the identity tail of a membership
// row -- everything the domain ExplicitGroupMembership re-derives from.
var explicitGroupMembershipScanColumns = explicitGroupMembershipColumns[1:]

// explicitGroupMembershipStatements are the backend-tagged, schema-
// qualified statements of the membership lifecycle: insert-once staging,
// the keyset pages the digest stream and the visit walk run, the source
// reads the append/completion boundary checks perform, and the completion
// CAS the fenced finalize commits.
type explicitGroupMembershipStatements struct {
	// memberInsert stages one relation insert-once; a returning row means
	// this call created it, no row means the exact (revision, group,
	// reference) already existed.
	memberInsert string
	// memberGet re-reads one stored relation by its full primary key. The
	// append skip path uses it to witness that the row a retry collided
	// with is present and agrees with what the batch asserts.
	memberGet string
	// memberPage streams one bounded page in byte-order (group_id,
	// resource_id) -- the composite continuation cursor of the Arango
	// adapter's membership visit, over COLLATE "C" columns so this is the
	// adapter's byte-order SORT. The trailing key columns are pure
	// tiebreakers: the trigger-pinned parent scope makes
	// (group_id, resource_id) unique within a revision, but the full-key
	// tail keeps the order total no matter what a bypassing writer did.
	memberPage string
	// memberDeleteRevision removes one revision's whole membership set
	// (owner abort, under the abort-token GUC).
	memberDeleteRevision string
	// memberDeleteBatch removes the membership sets of a cleanup batch
	// (under the cleanup-now GUC) before the definition rows go.
	memberDeleteBatch string
	// sourceSelection reads the stored source selection revision, scoped
	// by project -- the relational counterpart of Arango's
	// readExplicitGroupSourceSelection over SelectionsCollection.
	sourceSelection string
	// sourceMembers reads source selection members by derived member key
	// -- the counterpart of validateExplicitGroupSourceMemberships'
	// @keys lookup over SelectionMembersCollection.
	sourceMembers string
	// completeCAS is the completion CAS: it flips a STAGING row to
	// COMPLETE with its frozen facts only under the calling writer's
	// token. The fenced finalize has already verified every claimed fact
	// under the header lock; state and token are re-evaluated by
	// PostgreSQL at write time, and the migration's guard re-verifies
	// authorization and the claimed counts against the child rows.
	completeCAS string
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
			" ORDER BY group_id, resource_id, project, generation, resource_type" +
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

// explicitGroupMembershipWhereKey renders the full-primary-key predicate
// starting at placeholder number start ($start..$start+5) in
// explicitGroupMembershipColumns order.
func explicitGroupMembershipWhereKey(start string) string {
	offset := 0
	parts := make([]string, 0, len(explicitGroupMembershipColumns))
	for _, col := range explicitGroupMembershipColumns {
		parts = append(parts, col+" = $"+start)
		if offset == 0 {
			// Expand "$start" into "$start..$start+5" by ordinal: the
			// caller passes the decimal start so the numbering stays
			// literal and auditable.
			startNum := 0
			for _, ch := range start {
				startNum = startNum*10 + int(ch-'0')
			}
			parts = parts[:0]
			for i, col := range explicitGroupMembershipColumns {
				parts = append(parts, fmt.Sprintf("%s = $%d", col, startNum+i))
			}
			break
		}
	}
	return strings.Join(parts, " AND ")
}

// membershipArgs maps one canonical membership to the INSERT argument
// order (explicitGroupMembershipColumns).
func membershipArgs(revisionID string, m explorer.ExplicitGroupMembership) []any {
	return []any{revisionID, string(m.GroupID), m.Ref.Project, m.Ref.Generation, m.Ref.ResourceType, m.Ref.ID}
}

// scanMembershipRow decodes one explicitGroupMembershipScanColumns row into
// the domain membership, re-applying canonicalization: a stored row that
// is not in canonical form is corruption, mirroring the definitions read
// path (the adapter's validateExplicitGroupMembershipRow).
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

// validateExplicitGroupMembershipRow is the read-side counterpart of the
// Arango adapter's validateExplicitGroupMembershipRow: the decoded
// membership must agree with the header it was read under (its revision
// ID), name a declared group, and carry a reference inside the header's
// exact project/generation/resource-type scope. Disagreement is
// corruption -- never a silent repair, never a silent serve.
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

// readExplicitGroupSourceSelection is the relational counterpart of the
// Arango adapter's readExplicitGroupSourceSelection: the header's source
// selection must exist as a STORED revision under the header's own
// project, and the header's binding (complete, id, project, generation,
// resource type, scope digest, membership digest) must still match it. A
// missing or foreign row is ErrExplicitGroupRevisionStaleSource, never a
// silent pass -- the binding a Begin validated against the caller's copy
// is re-validated against the durable one on every append and completion.
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

// validateExplicitGroupSourceMemberships is the relational counterpart of
// the Arango adapter's validateExplicitGroupSourceMemberships of the same
// name: inside each append/completion boundary it checks every relation
// against the immutable source selection before any group-membership row
// can be inserted or the revision completed. Relations are checked in
// bounded pages of explicitGroupMembershipPageSize, exactly as the adapter
// batches its @keys lookups; a stored source member that disagrees with
// the key it is filed under is corruption, and a relation with no source
// member at all is ErrExplicitGroupMemberNotInSelection.
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

// digestExplicitGroupChildren recomputes the whole definitions-plus-
// membership contract of a revision: the definition digest over the stored
// ordered definitions, and the membership digest plus counts by paging the
// membership rows in byte-order (group_id, resource_id) through the shared
// WriteExplicitGroupMembershipFrame framing, source-validating each page
// against the immutable source selection -- one implementation shared by
// DigestExplicitGroupRevision (read path) and CompleteExplicitGroupRevision
// (fenced finalize), so the facts a caller may finalize are exactly the
// facts a reader may digest, and there is no second digest path to drift.
// The walk never buffers more than one page.
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

// AppendExplicitGroupMemberships stages one bounded batch of relations
// under the writer's lease, the exact counterpart of the Arango adapter's
// AppendExplicitGroupMemberships. Everything happens in ONE transaction:
// the header is locked FOR UPDATE, its writer token and STAGING state are
// verified, the batch is canonicalized against the stored definitions --
// a relation naming an undeclared group, a reference outside the header's
// scope, or a malformed identity is the domain's plain error and no row of
// the batch exists -- and every canonical relation is checked against the
// stored source selection before a single insert runs, so a relation whose
// resource is not in the source selection is refused with
// ErrExplicitGroupMemberNotInSelection before anything is staged.
//
// Insert-once retries are idempotent without duplicates: ON CONFLICT on
// the full primary key means an already-present relation returns no row
// and is NOT reported as created by this call, while the same resource
// under a DIFFERENT GroupID is a distinct row (the overlap the contract
// requires). A stored row a retry collided with is re-read and witnessed
// to agree with the batch. Every accepted write renews the writer lease
// under the token CAS; a renewal miss is the typed conflict. Blank
// revision ID or writer token is the typed conflict before any database
// touch; an empty batch is a no-op, like the adapter's early return.
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
			// The exact (revision, group, reference) already existed.
			// Witness the stored row agrees with what this batch asserts
			// before crediting it as staged; membership rows are
			// insert-once and cannot be removed while this transaction
			// holds the header lock (abort and cleanup take it first), so
			// a vanished row here means the row was never this batch's --
			// the adapter's stored-mismatch conflict.
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
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == duplicateKeySQLState &&
				pgErr.ConstraintName == explicitGroupMembershipPKConstraint {
				// The header lock makes this unreachable today; classified
				// as the conflict with the real PostgreSQL cause retained,
				// never absorbed.
				return nil, errors.Join(explorer.ErrExplicitGroupRevisionConflict, err)
			}
			if errors.As(err, &pgErr) && pgErr.Code == explicitGroupMembershipForeignKeyState &&
				(pgErr.ConstraintName == explicitGroupMembershipGroupFK ||
					pgErr.ConstraintName == explicitGroupMembershipRevisionFK) {
				// The declared-group / parent-exists foreign keys decided
				// this, not the adapter: a relation naming a group that is
				// not stored for this revision is a conflict with the
				// database cause retained.
				return nil, errors.Join(explorer.ErrExplicitGroupRevisionConflict, err)
			}
			return nil, fmt.Errorf("postgres explorer store: stage explicit-group membership of %q under group %q: %w",
				revisionID, string(membership.GroupID), err)
		}
		if groupID != string(membership.GroupID) || project != membership.Ref.Project ||
			generation != membership.Ref.Generation || resourceType != membership.Ref.ResourceType ||
			resourceID != membership.Ref.ID {
			// The stored row disagrees with what was written in the same
			// statement: an integrity failure of the storage itself.
			return nil, fmt.Errorf("postgres explorer store: staged membership of %q under group %q reread mismatch",
				revisionID, string(membership.GroupID))
		}
		inserted = append(inserted, membership)
	}

	// Test-only seam: exercises faults and interleavings after the real
	// membership inserts and before the lease renewal inside this same
	// live transaction. Always nil in production (unexported, never
	// assigned outside explicit_groups_test.go); an error here aborts the
	// transaction, so both the membership rows and the renewal vanish
	// together -- a partially applied batch never leaks members.
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

// DigestExplicitGroupRevision returns the definition digest, membership
// digest, group count, and member count of one revision, tenant-scoped by
// project. A COMPLETE revision returns its frozen facts without
// re-streaming, so the digest the completion CAS stored is exactly what
// readers get back; a STAGING revision is recomputed from its stored
// children through the same paged walk the fenced finalize verifies. A
// missing or cross-project header is ErrExplicitGroupRevisionNotFound.
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

// CompleteExplicitGroupRevision is the fenced finalization, the exact
// counterpart of the Arango adapter's CompleteExplicitGroupRevision.
// Everything runs in ONE transaction:
//
//  1. The header is locked FOR UPDATE. A COMPLETE row is classified from
//     the stored facts exactly like the adapter's early branch: identical
//     claimed facts are the idempotent retry and return the stored
//     revision; different facts are the typed conflict. A STAGING row must
//     pass the writer guard (state plus token) before anything is verified.
//  2. Under that lock the adapter RE-DERIVES the whole definitions-plus-
//     membership set -- the same paged walk DigestExplicitGroupRevision
//     uses, source-validating each page -- and compares every claimed
//     fact against it. A mismatch is the typed conflict and NOTHING is
//     flipped: the revision stays STAGING, which every reader path (Get,
//     List, ListDefinitions, Visit) refuses as unusable, and the writer
//     can keep appending and retry. Membership cannot move underneath the
//     comparison -- append and abort must take the same header lock, and
//     rows are insert-once and never rewritten.
//  3. Only on full agreement does the CAS flip STAGING -> COMPLETE with
//     the claimed facts under the token; PostgreSQL re-evaluates state and
//     token at write time, and the migration's guard additionally demands
//     the writer-token GUC, freezes the identity columns, and refuses the
//     transition unless the claimed counts equal the child rows actually
//     stored. A zero-row CAS is the typed conflict, never a silent success.
//
// Invalid input follows the adapter exactly: a blank revision ID, writer
// token, or either digest, a non-positive group count, or a negative
// member count is ErrExplicitGroupRevisionIncomplete before any database
// touch; a zero completedAt becomes the current UTC instant.
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
		// The CAS already had its chance to speak; classify from the
		// stored row exactly as the adapter's early branch does --
		// identical facts are the idempotent retry, different facts are a
		// conflict.
		if domain.DefinitionDigest != definitionDigest || domain.MembershipDigest != membershipDigest ||
			domain.GroupCount != groupCount || domain.MemberCount != memberCount {
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
		return domain, nil
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return nil, err
	}

	// Finalize under the fence: the claimed facts must describe the whole
	// definitions-plus-membership set as actually stored, re-derived here
	// rather than trusted from the caller.
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
		// Unreachable while the FOR UPDATE lock is held -- the row was
		// just verified STAGING under this token. If it ever fires, the
		// flip was not this writer's to make.
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: complete explicit-group revision %q: %w", revisionID, err)
	}
	completedDomain, err := completed.toDomain(revisionID)
	if err != nil {
		return nil, err
	}

	// Test-only seam: exercises a crash between the completion CAS and the
	// commit of the finalize transaction. Always nil in production; an
	// error here aborts the transaction, so the header stays STAGING with
	// its definitions and memberships intact -- the freeze either commits
	// whole or not at all, and the writer can retry after the crash.
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

// VisitExplicitGroupMemberships visits at most one bounded page of
// COMPLETE membership per call over the composite keyset cursor
// (group_id, resource_id), the relational counterpart of the Arango
// adapter's VisitExplicitGroupMemberships. The header passes the public
// gate first: a missing or cross-project header is
// ErrExplicitGroupRevisionNotFound and a STAGING header is
// ErrExplicitGroupRevisionIncomplete -- streaming membership that was
// never finalized into a digest would let callers publish facts no
// completion CAS ever froze. Every row is read-side verified (canonical
// round-trip, declared group, in-scope reference), the page budget is
// bounded to explicitGroupMembershipPageSize however large the request
// is, and the walk is cancellation-aware: a cancelled call stops between
// members and returns the cursor of the last member the visitor actually
// saw, so resuming continues with no gap and no duplicate. The cursor is
// stable under key ties because the page order is the full primary-key
// order and the membership set is insert-once under the header lock.
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
		// One call never materializes more than one bounded page, no
		// matter what page budget a caller asks for.
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

	// One page per call, like the adapter: visiting stops at pageSize
	// members and the caller resumes from the returned cursor. The
	// caller's context is honored between members; the cursor names the
	// last member the visitor actually saw, so a cancelled walk resumes
	// from there with no gap and no duplicate.
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
