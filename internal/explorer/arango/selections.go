package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	selectionStagingState = "STAGING"
	selectionWriterLease  = 10 * time.Minute
	selectionCleanupLimit = 100
)

func selectionMemberKey(selectionID string, ref explorer.ResourceRef) string {
	return selectionID + "_" + key(ref.Project, ref.Generation, ref.ResourceType, ref.ID)
}

func (s *Store) BeginSelection(ctx context.Context, selection explorer.SelectionRevision, writerToken string) (*explorer.SelectionRevision, error) {
	selection = selection.Canonical()
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return nil, explorer.ErrSelectionConflict
	}
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	if selection.Complete {
		return nil, fmt.Errorf("selection must begin incomplete")
	}
	// A crashed creator leaves a staging header behind. Reap only expired
	// leases, in a bounded batch, before idempotency lookup so a retry with the
	// same key can claim a clean immutable revision. Active writers are
	// protected by writerExpiresAt and are never touched here.
	if err := s.CleanupSelectionStaging(ctx, time.Now().UTC(), selectionCleanupLimit); err != nil {
		return nil, err
	}
	doc, err := document(selection, selection.ID)
	if err != nil {
		return nil, err
	}
	doc["state"] = selectionStagingState
	doc["writerToken"] = writerToken
	doc["writerExpiresAt"] = time.Now().UTC().Add(selectionWriterLease).UnixMilli()
	var existingID string
	if err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project AND d.idempotencyKey == @idempotencyKey LIMIT 1 RETURN {_key: d._key}`, 1, map[string]any{"@c": SelectionsCollection, "project": selection.Project, "idempotencyKey": selection.IdempotencyKey}, func(row map[string]any) error {
		existingID, _ = row["_key"].(string)
		return nil
	}); err != nil {
		return nil, err
	}
	if existingID != "" && existingID != selection.ID {
		return nil, explorer.ErrSelectionConflict
	}
	if err := s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": SelectionsCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		return nil, err
	}
	stored, err := s.readSelection(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, map[string]any{"@c": SelectionsCollection, "key": selection.ID})
	if err != nil {
		return nil, err
	}
	if stored.Project != selection.Project || stored.Generation != selection.Generation || stored.ResourceType != selection.ResourceType || stored.RuleDigest != selection.RuleDigest || stored.ScopeDigest != selection.ScopeDigest || stored.IdempotencyKey != selection.IdempotencyKey {
		return nil, explorer.ErrSelectionConflict
	}
	if !stored.Complete {
		storedWriterToken := ""
		if err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN {writerToken: d.writerToken}`, 1, map[string]any{"@c": SelectionsCollection, "key": selection.ID}, func(row map[string]any) error {
			storedWriterToken, _ = row["writerToken"].(string)
			return nil
		}); err != nil {
			return nil, err
		}
		if strings.TrimSpace(storedWriterToken) != writerToken {
			return nil, explorer.ErrSelectionConflict
		}
	}
	return stored, nil
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
	// A batch is intentionally bounded by the lifecycle caller. The adapter
	// inserts each member with an immutable key so retries are idempotent and
	// never require buffering the complete membership.
	inserted := make([]explorer.SelectionMember, 0, len(members))
	err := s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{SelectionsCollection, SelectionMembersCollection}}, func(txCtx context.Context, tx store.RowQueryer) error {
		var header *explorer.SelectionRevision
		storedWriterToken := ""
		state := ""
		if err := tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": SelectionsCollection, "key": selectionID}, func(row map[string]any) error {
			value, err := decode[explorer.SelectionRevision](row)
			if err != nil {
				return err
			}
			header = &value
			storedWriterToken, _ = row["writerToken"].(string)
			state, _ = row["state"].(string)
			return nil
		}); err != nil {
			return err
		}
		if header == nil {
			return explorer.ErrSelectionNotFound
		}
		if strings.TrimSpace(storedWriterToken) != writerToken {
			return explorer.ErrSelectionConflict
		}
		if header.Complete || (state != "" && state != selectionStagingState) {
			return explorer.ErrSelectionConflict
		}
		docs := make([]map[string]any, 0, len(members))
		byKey := make(map[string]explorer.SelectionMember, len(members))
		for _, raw := range members {
			member := raw.Canonical()
			expectedMemberKey := selectionMemberKey(selectionID, member.Ref)
			if member.MemberKey != "" && member.MemberKey != expectedMemberKey {
				return explorer.ErrSelectionConflict
			}
			member.MemberKey = expectedMemberKey
			if err := member.Ref.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
				return err
			}
			doc, err := document(member, member.MemberKey)
			if err != nil {
				return err
			}
			doc["selectionId"] = selectionID
			doc["project"] = member.Ref.Project
			doc["generation"] = member.Ref.Generation
			doc["resourceType"] = member.Ref.ResourceType
			doc["id"] = member.Ref.ID
			docs = append(docs, doc)
			byKey[member.MemberKey] = member
		}
		created := make(map[string]bool, len(docs))
		if err := tx.QueryRows(txCtx, `FOR doc IN @docs
UPSERT {_key: doc._key}
INSERT doc
UPDATE {}
IN @@c
RETURN {key: NEW._key, inserted: OLD == null}`, 1, map[string]any{"@c": SelectionMembersCollection, "docs": docs}, func(row map[string]any) error {
			key, _ := row["key"].(string)
			value, _ := row["inserted"].(bool)
			// A duplicate key can occur within one parameter batch. Arango
			// reports the first UPSERT as inserted and the retry as existing;
			// preserve the fact that this call created the member.
			created[key] = created[key] || value
			return nil
		}); err != nil {
			return err
		}
		if err := tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key AND d.writerToken == @writerToken UPDATE d WITH {writerExpiresAt: @writerExpiresAt} IN @@c RETURN {renewed: true}`, 1, map[string]any{"@c": SelectionsCollection, "key": selectionID, "writerToken": writerToken, "writerExpiresAt": time.Now().UTC().Add(selectionWriterLease).UnixMilli()}, func(map[string]any) error { return nil }); err != nil {
			return err
		}
		for key, member := range byKey {
			if created[key] {
				inserted = append(inserted, member)
			}
		}
		return nil
	})
	return inserted, err
}

func (s *Store) DigestSelectionMembers(ctx context.Context, project, selectionID string) (string, int64, int64, error) {
	selectionID = strings.TrimSpace(selectionID)
	project = strings.TrimSpace(project)
	if selectionID == "" || project == "" {
		return "", 0, 0, explorer.ErrSelectionNotFound
	}
	header, err := s.GetSelection(ctx, project, selectionID)
	if err != nil {
		return "", 0, 0, err
	}
	if header.Complete {
		return header.MembershipDigest, header.MemberCount, header.MemberBytes, nil
	}
	hash := sha256.New()
	var count, bytes int64
	afterID := ""
	for {
		pageCount := 0
		err := s.client.QueryRows(ctx, `FOR m IN @@c
	FILTER m.selectionId == @selectionId AND m.project == @project AND m.id > @afterID
	SORT m.id ASC, m._key ASC
	LIMIT @limit
	RETURN m`, 1000, map[string]any{"@c": SelectionMembersCollection, "selectionId": selectionID, "project": project, "afterID": afterID, "limit": 1000}, func(row map[string]any) error {
			member, decodeErr := decode[explorer.SelectionMember](row)
			if decodeErr != nil {
				return decodeErr
			}
			member = member.Canonical()
			explorer.WriteMembershipFrame(hash, member.Ref)
			for _, value := range []string{member.Ref.Project, member.Ref.Generation, member.Ref.ResourceType, member.Ref.ID} {
				bytes += int64(8 + len(value))
			}
			count++
			pageCount++
			afterID = member.Ref.ID
			return nil
		})
		if err != nil {
			return "", 0, 0, err
		}
		if pageCount < 1000 {
			break
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), count, bytes, nil
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
	var changed bool
	ownerFilter := `FILTER d._key == @key AND d.state == @state AND d.complete != true AND d.writerToken == @writerToken`
	binds := map[string]any{"@c": SelectionsCollection, "key": selectionID, "state": selectionStagingState, "writerToken": writerToken, "digest": digest, "count": count, "bytes": bytes, "completedAt": completedAt}
	query := `FOR d IN @@c
` + ownerFilter + `
UPDATE d WITH {complete: true, state: "COMPLETE", membershipDigest: @digest, memberCount: @count, memberBytes: @bytes, completedAt: @completedAt} IN @@c
	RETURN NEW`
	err := s.client.QueryRows(ctx, query, 1, binds, func(row map[string]any) error {
		changed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	stored, err := s.readSelection(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, map[string]any{"@c": SelectionsCollection, "key": selectionID})
	if err != nil {
		return nil, err
	}
	if stored.Complete {
		if stored.MembershipDigest != digest || stored.MemberCount != count || stored.MemberBytes != bytes {
			return nil, explorer.ErrSelectionConflict
		}
		return stored, nil
	}
	if !changed {
		storedWriterToken := ""
		if err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN {writerToken: d.writerToken}`, 1, map[string]any{"@c": SelectionsCollection, "key": selectionID}, func(row map[string]any) error {
			storedWriterToken, _ = row["writerToken"].(string)
			return nil
		}); err != nil {
			return nil, err
		}
		if strings.TrimSpace(storedWriterToken) != writerToken {
			return nil, explorer.ErrSelectionConflict
		}
		return nil, explorer.ErrSelectionIncomplete
	}
	return stored, nil
}

func (s *Store) AbortSelection(ctx context.Context, selectionID, writerToken string) error {
	selectionID = strings.TrimSpace(selectionID)
	writerToken = strings.TrimSpace(writerToken)
	if selectionID == "" {
		return nil
	}
	if writerToken == "" {
		return explorer.ErrSelectionConflict
	}
	ownerExpr := `header != null AND header.complete != true AND header.writerToken == @writerToken`
	binds := map[string]any{"@members": SelectionMembersCollection, "@headers": SelectionsCollection, "selectionId": selectionID, "writerToken": writerToken}
	query := `LET header = DOCUMENT(@@headers, @selectionId)
LET owned = ` + ownerExpr + `
LET removedMembers = (
  FOR m IN @@members
    FILTER owned AND m.selectionId == @selectionId
    REMOVE m IN @@members
    RETURN 1
)
LET removedHeaders = (
  FOR d IN @@headers
    FILTER owned AND d._key == @selectionId
    REMOVE d IN @@headers
    RETURN 1
)
RETURN {removed: LENGTH(removedHeaders) > 0}`
	return s.client.QueryRows(ctx, query, 1, binds, func(map[string]any) error { return nil })
}

func (s *Store) GetSelection(ctx context.Context, project, selectionID string) (*explorer.SelectionRevision, error) {
	return s.readSelection(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, map[string]any{"@c": SelectionsCollection, "key": strings.TrimSpace(selectionID), "project": strings.TrimSpace(project)})
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
		pageSize = 1000
	}
	page := make([]explorer.SelectionMember, 0, pageSize)
	last := strings.TrimSpace(after)
	err = s.client.QueryRows(ctx, `FOR m IN @@c
FILTER m.selectionId == @selectionId AND m.project == @project AND m._key > @after
SORT m._key ASC
LIMIT @limit
RETURN m`, pageSize, map[string]any{"@c": SelectionMembersCollection, "selectionId": selectionID, "project": strings.TrimSpace(project), "after": last, "limit": pageSize}, func(row map[string]any) error {
		member, decodeErr := decode[explorer.SelectionMember](row)
		if decodeErr != nil {
			return decodeErr
		}
		if member.MemberKey == "" {
			member.MemberKey, _ = row["_key"].(string)
		}
		page = append(page, member.Canonical())
		return nil
	})
	if err != nil {
		return "", err
	}
	for _, member := range page {
		if err := visit(member); err != nil {
			return last, err
		}
		last = member.MemberKey
	}
	return last, nil
}

func (s *Store) readSelection(ctx context.Context, query string, binds map[string]any) (*explorer.SelectionRevision, error) {
	var result *explorer.SelectionRevision
	err := s.client.QueryRows(ctx, query, 1, binds, func(row map[string]any) error {
		value, err := decode[explorer.SelectionRevision](row)
		if err != nil {
			return err
		}
		result = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, explorer.ErrSelectionNotFound
	}
	if result.Complete {
		if err := result.Validate(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// CleanupSelectionStaging removes bounded abandoned staging records. It is
// deliberately explicit so startup callers can budget cleanup work rather
// than scanning all selections into memory.
func (s *Store) CleanupSelectionStaging(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	return s.client.QueryRows(ctx, `FOR d IN @@c
  FILTER d.state == @state AND d.createdAt < @before AND (!HAS(d, "writerExpiresAt") OR d.writerExpiresAt < @now)
  SORT d.createdAt ASC
  LIMIT @limit
  LET selectionId = d._key
  LET removedMembers = (
    FOR m IN @@members
      FILTER m.selectionId == selectionId
      REMOVE m IN @@members
      RETURN 1
  )
  REMOVE d IN @@c
  RETURN {removed: true}`, limit, map[string]any{"@c": SelectionsCollection, "@members": SelectionMembersCollection, "state": selectionStagingState, "before": before, "now": time.Now().UTC().UnixMilli(), "limit": limit}, func(map[string]any) error { return nil })
}

var _ json.Marshaler
