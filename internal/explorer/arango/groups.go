package arango

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	explicitGroupWriterLease  = 10 * time.Minute
	explicitGroupCleanupLimit = 100
	explicitGroupPageSize     = 1000
)

var _ explorerselection.ExplicitGroupRepository = (*Store)(nil)

func explicitGroupDefinitionKey(revisionID explorerselection.ExplicitGroupRevisionID, groupID explorerselection.ExplicitGroupID) string {
	return "groupdef_" + explicitGroupStorageKey(string(revisionID), string(groupID))
}

func explicitGroupMembershipKey(revisionID explorerselection.ExplicitGroupRevisionID, membership explorerselection.ExplicitGroupMembership) string {
	membership = membership.Canonical()
	return "groupmember_" + explicitGroupStorageKey(string(revisionID), string(membership.GroupID), membership.Ref.Project, membership.Ref.Generation, membership.Ref.ResourceType, membership.Ref.ID)
}

func explicitGroupStorageKey(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *ExplicitGroupStore) BeginExplicitGroupRevision(ctx context.Context, revision explorerselection.ExplicitGroupRevision, source explorerselection.SelectionRevision, writerToken string) (*explorerselection.ExplicitGroupRevision, error) {
	revision = revision.Canonical()
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return nil, explorerselection.ErrExplicitGroupRevisionConflict
	}
	if err := revision.Validate(); err != nil {
		return nil, err
	}
	if err := revision.ValidateSourceSelection(source); err != nil {
		return nil, err
	}
	storedSource, err := s.selections.GetSelection(ctx, revision.Project, revision.SourceSelectionRevisionID)
	if err != nil {
		return nil, fmt.Errorf("%w: source selection unavailable: %w", explorerselection.ErrExplicitGroupRevisionStaleSource, err)
	}
	if err := revision.ValidateSourceSelection(*storedSource); err != nil {
		return nil, err
	}
	if revision.State != explorerselection.ExplicitGroupRevisionStaging {
		return nil, fmt.Errorf("explicit group revision must begin in staging state")
	}
	if err := s.CleanupExplicitGroupStaging(ctx, time.Now().UTC(), explicitGroupCleanupLimit); err != nil {
		return nil, err
	}
	doc, err := db.Document(revision, string(revision.ID))
	if err != nil {
		return nil, err
	}
	doc["writerToken"] = writerToken
	doc["writerExpiresAt"] = time.Now().UTC().Add(explicitGroupWriterLease).UnixMilli()
	if err := s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		if shared.IsArangoErrorWithErrorNum(err, shared.ErrArangoConflict) {
			return nil, explorerselection.ErrExplicitGroupRevisionConflict
		}
		return nil, err
	}
	stored, row, err := s.readExplicitGroupRevision(ctx, s.client, revision.ID, revision.Project)
	if err != nil {
		return nil, err
	}
	if !sameExplicitGroupRevisionIntent(*stored, revision) {
		return nil, explorerselection.ErrExplicitGroupRevisionConflict
	}
	if stored.State == explorerselection.ExplicitGroupRevisionStaging {
		storedWriterToken, _ := row["writerToken"].(string)
		if strings.TrimSpace(storedWriterToken) != writerToken {
			return nil, explorerselection.ErrExplicitGroupRevisionConflict
		}
		var renewed bool
		if err := s.client.QueryRows(ctx, `FOR d IN @@c
FILTER d._key == @key AND d.state == @state AND d.writerToken == @writerToken
UPDATE d WITH {writerExpiresAt: @expiresAt} IN @@c
RETURN {renewed: true}`, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revision.ID), "state": explorerselection.ExplicitGroupRevisionStaging, "writerToken": writerToken, "expiresAt": time.Now().UTC().Add(explicitGroupWriterLease).UnixMilli()}, func(map[string]any) error {
			renewed = true
			return nil
		}); err != nil {
			return nil, err
		}
		if !renewed {
			return nil, explorerselection.ErrExplicitGroupRevisionConflict
		}
	}
	return stored, nil
}

func sameExplicitGroupRevisionIntent(left, right explorerselection.ExplicitGroupRevision) bool {
	left, right = left.Canonical(), right.Canonical()
	return left.ID == right.ID && left.Project == right.Project && left.Generation == right.Generation && left.ScopeDigest == right.ScopeDigest && left.ResourceType == right.ResourceType && left.SourceSelectionRevisionID == right.SourceSelectionRevisionID && left.SourceMembershipDigest == right.SourceMembershipDigest && left.IdempotencyKey == right.IdempotencyKey
}

func (s *ExplicitGroupStore) PutExplicitGroupDefinitions(ctx context.Context, revisionID explorerselection.ExplicitGroupRevisionID, writerToken string, groups []explorerselection.ExplicitGroupDefinition) error {
	revisionID = explorerselection.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return explorerselection.ErrExplicitGroupRevisionConflict
	}
	canonical, err := explorerselection.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return err
	}
	return s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{ExplicitGroupRevisionsCollection, ExplicitGroupDefinitionsCollection}}, func(txCtx context.Context, tx store.RowQueryer) error {
		header, row, err := s.readExplicitGroupRevision(txCtx, tx, revisionID, "")
		if err != nil {
			return err
		}
		if err := explicitGroupWriter(header, row, writerToken); err != nil {
			return err
		}
		stored, err := listExplicitGroupDefinitions(txCtx, tx, *header)
		if err != nil {
			return err
		}
		if len(stored) > 0 {
			if !reflect.DeepEqual(stored, canonical) {
				return explorerselection.ErrExplicitGroupRevisionConflict
			}
			return renewExplicitGroupWriter(txCtx, tx, revisionID, writerToken)
		}
		docs := make([]map[string]any, 0, len(canonical))
		for _, group := range canonical {
			doc, err := db.Document(group, explicitGroupDefinitionKey(revisionID, group.ID))
			if err != nil {
				return err
			}
			doc["revisionId"] = string(revisionID)
			doc["project"] = header.Project
			doc["groupId"] = string(group.ID)
			docs = append(docs, doc)
		}
		if err := tx.QueryRows(txCtx, `FOR doc IN @docs
UPSERT {_key: doc._key}
INSERT doc
UPDATE {}
IN @@c
RETURN {key: NEW._key}`, len(docs), map[string]any{"@c": ExplicitGroupDefinitionsCollection, "docs": docs}, func(row map[string]any) error {
			if _, ok := row["key"].(string); !ok {
				return explorerselection.ErrCorruptExplicitGroupRevision
			}
			return nil
		}); err != nil {
			return err
		}
		stored, err = listExplicitGroupDefinitions(txCtx, tx, *header)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(stored, canonical) {
			return explorerselection.ErrExplicitGroupRevisionConflict
		}
		return renewExplicitGroupWriter(txCtx, tx, revisionID, writerToken)
	})
}

func (s *ExplicitGroupStore) AppendExplicitGroupMemberships(ctx context.Context, revisionID explorerselection.ExplicitGroupRevisionID, writerToken string, memberships []explorerselection.ExplicitGroupMembership) ([]explorerselection.ExplicitGroupMembership, error) {
	revisionID = explorerselection.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return nil, explorerselection.ErrExplicitGroupRevisionConflict
	}
	if len(memberships) == 0 {
		return nil, nil
	}
	inserted := make([]explorerselection.ExplicitGroupMembership, 0, len(memberships))
	err := s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{SelectionsCollection, SelectionMembersCollection},
		Write: []string{ExplicitGroupRevisionsCollection, ExplicitGroupMembershipsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		header, row, err := s.readExplicitGroupRevision(txCtx, tx, revisionID, "")
		if err != nil {
			return err
		}
		if err := explicitGroupWriter(header, row, writerToken); err != nil {
			return err
		}
		groups, err := listExplicitGroupDefinitions(txCtx, tx, *header)
		if err != nil {
			return err
		}
		canonical, err := explorerselection.CanonicalExplicitGroupMemberships(*header, groups, memberships)
		if err != nil {
			return err
		}
		source, err := readExplicitGroupSourceSelection(txCtx, tx, *header)
		if err != nil {
			return err
		}
		if err := validateExplicitGroupSourceMemberships(txCtx, tx, *header, *source, canonical); err != nil {
			return err
		}
		docs := make([]map[string]any, 0, len(canonical))
		byKey := make(map[string]explorerselection.ExplicitGroupMembership, len(canonical))
		for _, membership := range canonical {
			memberKey := explicitGroupMembershipKey(revisionID, membership)
			doc, err := db.Document(membership, memberKey)
			if err != nil {
				return err
			}
			doc["revisionId"] = string(revisionID)
			doc["project"] = membership.Ref.Project
			doc["generation"] = membership.Ref.Generation
			doc["resourceType"] = membership.Ref.ResourceType
			doc["groupId"] = string(membership.GroupID)
			doc["id"] = membership.Ref.ID
			docs = append(docs, doc)
			byKey[memberKey] = membership
		}
		created := make(map[string]bool, len(docs))
		if err := tx.QueryRows(txCtx, `FOR doc IN @docs
UPSERT {_key: doc._key}
INSERT doc
UPDATE {}
IN @@c
RETURN {key: NEW._key, inserted: OLD == null}`, len(docs), map[string]any{"@c": ExplicitGroupMembershipsCollection, "docs": docs}, func(row map[string]any) error {
			key, _ := row["key"].(string)
			wasInserted, _ := row["inserted"].(bool)
			created[key] = created[key] || wasInserted
			return nil
		}); err != nil {
			return err
		}
		keys := make([]string, 0, len(byKey))
		for memberKey := range byKey {
			keys = append(keys, memberKey)
		}
		stored := make(map[string]explorerselection.ExplicitGroupMembership, len(keys))
		if err := tx.QueryRows(txCtx, `FOR m IN @@c FILTER m._key IN @keys RETURN m`, len(keys), map[string]any{"@c": ExplicitGroupMembershipsCollection, "keys": keys}, func(row map[string]any) error {
			membership, err := db.Decode[explorerselection.ExplicitGroupMembership](row)
			if err != nil {
				return err
			}
			if err := validateExplicitGroupMembershipRow(row, membership, *header); err != nil {
				return err
			}
			memberKey, _ := row["_key"].(string)
			stored[memberKey] = membership.Canonical()
			return nil
		}); err != nil {
			return err
		}
		for memberKey, membership := range byKey {
			if storedMembership, ok := stored[memberKey]; !ok || storedMembership != membership {
				return explorerselection.ErrExplicitGroupRevisionConflict
			}
		}
		for _, membership := range canonical {
			if created[explicitGroupMembershipKey(revisionID, membership)] {
				inserted = append(inserted, membership)
			}
		}
		if err := renewExplicitGroupWriter(txCtx, tx, revisionID, writerToken); err != nil {
			return err
		}
		return nil
	})
	return inserted, err
}

func explicitGroupWriter(header *explorerselection.ExplicitGroupRevision, row map[string]any, writerToken string) error {
	if header == nil {
		return explorerselection.ErrExplicitGroupRevisionNotFound
	}
	if header.State != explorerselection.ExplicitGroupRevisionStaging {
		return explorerselection.ErrExplicitGroupRevisionConflict
	}
	storedWriterToken, _ := row["writerToken"].(string)
	if strings.TrimSpace(storedWriterToken) != writerToken {
		return explorerselection.ErrExplicitGroupRevisionConflict
	}
	return nil
}

func renewExplicitGroupWriter(ctx context.Context, tx store.RowQueryer, revisionID explorerselection.ExplicitGroupRevisionID, writerToken string) error {
	var renewed bool
	if err := tx.QueryRows(ctx, `FOR d IN @@c
FILTER d._key == @key AND d.state == @state AND d.writerToken == @writerToken
UPDATE d WITH {writerExpiresAt: @expiresAt} IN @@c
RETURN {renewed: true}`, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revisionID), "state": explorerselection.ExplicitGroupRevisionStaging, "writerToken": writerToken, "expiresAt": time.Now().UTC().Add(explicitGroupWriterLease).UnixMilli()}, func(map[string]any) error {
		renewed = true
		return nil
	}); err != nil {
		return err
	}
	if !renewed {
		return explorerselection.ErrExplicitGroupRevisionConflict
	}
	return nil
}

func (s *ExplicitGroupStore) DigestExplicitGroupRevision(ctx context.Context, project string, revisionID explorerselection.ExplicitGroupRevisionID) (string, string, int64, int64, error) {
	header, _, err := s.readExplicitGroupRevision(ctx, s.client, revisionID, project)
	if err != nil {
		return "", "", 0, 0, err
	}
	if header.State == explorerselection.ExplicitGroupRevisionComplete {
		return header.DefinitionDigest, header.MembershipDigest, header.GroupCount, header.MemberCount, nil
	}
	return digestExplicitGroupChildren(ctx, s.client, *header)
}

func digestExplicitGroupChildren(ctx context.Context, queryer store.RowQueryer, header explorerselection.ExplicitGroupRevision) (string, string, int64, int64, error) {
	groups, err := listExplicitGroupDefinitions(ctx, queryer, header)
	if err != nil {
		return "", "", 0, 0, err
	}
	definitionDigest, err := explorerselection.ExplicitGroupDefinitionDigest(groups)
	if err != nil {
		return "", "", 0, 0, err
	}
	groupIDs := make(map[explorerselection.ExplicitGroupID]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}
	source, err := readExplicitGroupSourceSelection(ctx, queryer, header)
	if err != nil {
		return "", "", 0, 0, err
	}
	hash := sha256.New()
	explorerselection.StartExplicitGroupMembershipDigest(hash)
	var memberCount int64
	batch := make([]explorerselection.ExplicitGroupMembership, 0, explicitGroupPageSize)
	err = readStagedExplicitGroupMemberships(ctx, queryer, header, groupIDs, func(membership explorerselection.ExplicitGroupMembership) error {
		batch = append(batch, membership)
		if len(batch) >= explicitGroupPageSize {
			if err := validateExplicitGroupSourceMemberships(ctx, queryer, header, *source, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
		explorerselection.WriteExplicitGroupMembershipFrame(hash, membership)
		memberCount++
		return nil
	})
	if err != nil {
		return "", "", 0, 0, err
	}
	if len(batch) > 0 {
		if err := validateExplicitGroupSourceMemberships(ctx, queryer, header, *source, batch); err != nil {
			return "", "", 0, 0, err
		}
	}
	return definitionDigest, hex.EncodeToString(hash.Sum(nil)), int64(len(groups)), memberCount, nil
}

func (s *ExplicitGroupStore) CompleteExplicitGroupRevision(ctx context.Context, revisionID explorerselection.ExplicitGroupRevisionID, writerToken, definitionDigest, membershipDigest string, groupCount, memberCount int64, completedAt time.Time) (*explorerselection.ExplicitGroupRevision, error) {
	revisionID = explorerselection.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	definitionDigest, membershipDigest = strings.TrimSpace(definitionDigest), strings.TrimSpace(membershipDigest)
	if revisionID == "" || writerToken == "" || definitionDigest == "" || membershipDigest == "" || groupCount <= 0 || memberCount < 0 {
		return nil, explorerselection.ErrExplicitGroupRevisionIncomplete
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	var result *explorerselection.ExplicitGroupRevision
	err := s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{SelectionsCollection, SelectionMembersCollection, ExplicitGroupDefinitionsCollection, ExplicitGroupMembershipsCollection},
		Write: []string{ExplicitGroupRevisionsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		header, row, err := s.readExplicitGroupRevision(txCtx, tx, revisionID, "")
		if err != nil {
			return err
		}
		if header.State == explorerselection.ExplicitGroupRevisionComplete {
			if header.DefinitionDigest != definitionDigest || header.MembershipDigest != membershipDigest || header.GroupCount != groupCount || header.MemberCount != memberCount {
				return explorerselection.ErrExplicitGroupRevisionConflict
			}
			result = header
			return nil
		}
		if err := explicitGroupWriter(header, row, writerToken); err != nil {
			return err
		}
		actualDefinition, actualMembership, actualGroups, actualMembers, err := digestExplicitGroupChildren(txCtx, tx, *header)
		if err != nil {
			return err
		}
		if actualDefinition != definitionDigest || actualMembership != membershipDigest || actualGroups != groupCount || actualMembers != memberCount {
			return explorerselection.ErrExplicitGroupRevisionConflict
		}
		var changed bool
		query := `FOR d IN @@c
FILTER d._key == @key AND d.state == @staging AND d.writerToken == @writerToken
UPDATE d WITH {state: @complete, definitionDigest: @definitionDigest, membershipDigest: @membershipDigest, groupCount: @groupCount, memberCount: @memberCount, completedAt: @completedAt} IN @@c
RETURN NEW`
		if err := tx.QueryRows(txCtx, query, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revisionID), "staging": explorerselection.ExplicitGroupRevisionStaging, "complete": explorerselection.ExplicitGroupRevisionComplete, "writerToken": writerToken, "definitionDigest": definitionDigest, "membershipDigest": membershipDigest, "groupCount": groupCount, "memberCount": memberCount, "completedAt": completedAt.UTC()}, func(row map[string]any) error {
			value, err := db.Decode[explorerselection.ExplicitGroupRevision](row)
			if err != nil {
				return err
			}
			if err := value.Validate(); err != nil {
				return err
			}
			result, changed = &value, true
			return nil
		}); err != nil {
			return err
		}
		if !changed {
			return explorerselection.ErrExplicitGroupRevisionConflict
		}
		return nil
	})
	return result, err
}

func readExplicitGroupSourceSelection(ctx context.Context, queryer store.RowQueryer, header explorerselection.ExplicitGroupRevision) (*explorerselection.SelectionRevision, error) {
	var source *explorerselection.SelectionRevision
	err := queryer.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, 1, map[string]any{
		"@c": SelectionsCollection, "key": header.SourceSelectionRevisionID, "project": header.Project,
	}, func(row map[string]any) error {
		value, err := db.Decode[explorerselection.SelectionRevision](row)
		if err != nil {
			return err
		}
		source = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, explorerselection.ErrExplicitGroupRevisionStaleSource
	}
	if err := header.ValidateSourceSelection(*source); err != nil {
		return nil, err
	}
	return source, nil
}

func validateExplicitGroupSourceMemberships(ctx context.Context, queryer store.RowQueryer, header explorerselection.ExplicitGroupRevision, source explorerselection.SelectionRevision, memberships []explorerselection.ExplicitGroupMembership) error {
	if err := header.ValidateSourceSelection(source); err != nil {
		return err
	}
	for start := 0; start < len(memberships); start += explicitGroupPageSize {
		end := start + explicitGroupPageSize
		if end > len(memberships) {
			end = len(memberships)
		}
		batch := memberships[start:end]
		keys := make([]string, 0, len(batch))
		want := make(map[string]explorerselection.ResourceRef, len(batch))
		for _, membership := range batch {
			membership = membership.Canonical()
			key := selectiondb.SelectionMemberKey(header.SourceSelectionRevisionID, membership.Ref)
			keys = append(keys, key)
			want[key] = membership.Ref
		}
		found := make(map[string]bool, len(keys))
		err := queryer.QueryRows(ctx, `FOR m IN @@c
FILTER m._key IN @keys AND m.selectionId == @selectionId AND m.project == @project
RETURN m`, len(keys), map[string]any{
			"@c": SelectionMembersCollection, "keys": keys, "selectionId": header.SourceSelectionRevisionID, "project": header.Project,
		}, func(row map[string]any) error {
			member, err := db.Decode[explorerselection.SelectionMember](row)
			if err != nil {
				return err
			}
			key, _ := row["_key"].(string)
			ref, ok := want[key]
			if !ok || member.Ref.Canonical() != ref || member.MemberKey != key {
				return explorerselection.ErrCorruptExplicitGroupRevision
			}
			if err := member.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
				return err
			}
			found[key] = true
			return nil
		})
		if err != nil {
			return err
		}
		for key := range want {
			if !found[key] {
				return explorerselection.ErrExplicitGroupMemberNotInSelection
			}
		}
	}
	return nil
}

func (s *ExplicitGroupStore) AbortExplicitGroupRevision(ctx context.Context, revisionID explorerselection.ExplicitGroupRevisionID, writerToken string) error {
	revisionID = explorerselection.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" {
		return nil
	}
	if writerToken == "" {
		return explorerselection.ErrExplicitGroupRevisionConflict
	}
	return s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{ExplicitGroupRevisionsCollection, ExplicitGroupDefinitionsCollection, ExplicitGroupMembershipsCollection}}, func(txCtx context.Context, tx store.RowQueryer) error {
		header, row, err := s.readExplicitGroupRevision(txCtx, tx, revisionID, "")
		if errors.Is(err, explorerselection.ErrExplicitGroupRevisionNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := explicitGroupWriter(header, row, writerToken); err != nil {
			return err
		}
		query := `LET removedMemberships = (
  FOR m IN @@memberships
    FILTER m.revisionId == @revisionId
    REMOVE m IN @@memberships
    RETURN 1
)
LET removedDefinitions = (
  FOR g IN @@definitions
    FILTER g.revisionId == @revisionId
    REMOVE g IN @@definitions
    RETURN 1
)
LET removedHeaders = (
  FOR d IN @@headers
    FILTER d._key == @revisionId AND d.state == @state AND d.writerToken == @writerToken
    REMOVE d IN @@headers
    RETURN 1
)
RETURN {removed: LENGTH(removedHeaders) > 0}`
		return tx.QueryRows(txCtx, query, 1, map[string]any{"@headers": ExplicitGroupRevisionsCollection, "@definitions": ExplicitGroupDefinitionsCollection, "@memberships": ExplicitGroupMembershipsCollection, "revisionId": string(revisionID), "state": explorerselection.ExplicitGroupRevisionStaging, "writerToken": writerToken}, func(row map[string]any) error {
			removed, _ := row["removed"].(bool)
			if !removed {
				return explorerselection.ErrExplicitGroupRevisionConflict
			}
			return nil
		})
	})
}

func (s *ExplicitGroupStore) CleanupExplicitGroupStaging(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = explicitGroupCleanupLimit
	}
	query := `FOR d IN @@revisions
FILTER d.state == @state AND d.createdAt < @before AND (!HAS(d, "writerExpiresAt") OR d.writerExpiresAt < @now)
SORT d.createdAt ASC
LIMIT @limit
LET revisionId = d._key
LET removedMemberships = (
  FOR m IN @@memberships
    FILTER m.revisionId == revisionId
    REMOVE m IN @@memberships
    RETURN 1
)
LET removedDefinitions = (
  FOR g IN @@definitions
    FILTER g.revisionId == revisionId
    REMOVE g IN @@definitions
    RETURN 1
)
REMOVE d IN @@revisions
RETURN {removed: true}`
	return s.client.QueryRows(ctx, query, limit, map[string]any{"@revisions": ExplicitGroupRevisionsCollection, "@definitions": ExplicitGroupDefinitionsCollection, "@memberships": ExplicitGroupMembershipsCollection, "state": explorerselection.ExplicitGroupRevisionStaging, "before": before.UTC(), "now": time.Now().UTC().UnixMilli(), "limit": limit}, func(map[string]any) error { return nil })
}

func (s *ExplicitGroupStore) GetExplicitGroupRevision(ctx context.Context, project string, revisionID explorerselection.ExplicitGroupRevisionID) (*explorerselection.ExplicitGroupRevision, error) {
	header, _, err := s.readExplicitGroupRevision(ctx, s.client, revisionID, project)
	if err != nil {
		return nil, err
	}
	if header.State != explorerselection.ExplicitGroupRevisionComplete {
		return nil, explorerselection.ErrExplicitGroupRevisionIncomplete
	}
	return header, nil
}

func (s *ExplicitGroupStore) ListExplicitGroupRevisions(ctx context.Context, project, generation, scopeDigest, resourceType string, limit int) ([]explorerselection.ExplicitGroupRevision, error) {
	if limit <= 0 {
		limit = 100
	}
	revisions := make([]explorerselection.ExplicitGroupRevision, 0)
	err := s.client.QueryRows(ctx, `FOR d IN @@c
FILTER d.project == @project
AND d.generation == @generation
AND d.scopeDigest == @scopeDigest
AND d.resourceType == @resourceType
AND d.state == @state
SORT d.createdAt DESC, d._key ASC
LIMIT @limit
RETURN d`, limit, map[string]any{
		"@c": ExplicitGroupRevisionsCollection, "project": project, "generation": generation,
		"scopeDigest": scopeDigest, "resourceType": resourceType,
		"state": explorerselection.ExplicitGroupRevisionComplete, "limit": limit,
	}, func(row map[string]any) error {
		revision, err := db.Decode[explorerselection.ExplicitGroupRevision](row)
		if err != nil {
			return err
		}
		if err := revision.Validate(); err != nil {
			return fmt.Errorf("%w: %v", explorerselection.ErrCorruptExplicitGroupRevision, err)
		}
		revisions = append(revisions, revision)
		return nil
	})
	return revisions, err
}

func (s *ExplicitGroupStore) readExplicitGroupRevision(ctx context.Context, queryer store.RowQueryer, revisionID explorerselection.ExplicitGroupRevisionID, project string) (*explorerselection.ExplicitGroupRevision, map[string]any, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return nil, nil, explorerselection.ErrExplicitGroupRevisionNotFound
	}
	query := `FOR d IN @@c FILTER d._key == @key`
	binds := map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revisionID)}
	if strings.TrimSpace(project) != "" {
		query += ` AND d.project == @project`
		binds["project"] = strings.TrimSpace(project)
	}
	query += ` RETURN d`
	var result *explorerselection.ExplicitGroupRevision
	var found map[string]any
	err := queryer.QueryRows(ctx, query, 1, binds, func(row map[string]any) error {
		value, err := db.Decode[explorerselection.ExplicitGroupRevision](row)
		if err != nil {
			return err
		}
		if string(value.ID) != string(revisionID) {
			return explorerselection.ErrCorruptExplicitGroupRevision
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("%w: %v", explorerselection.ErrCorruptExplicitGroupRevision, err)
		}
		result = &value
		found = row
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, explorerselection.ErrExplicitGroupRevisionNotFound
	}
	return result, found, nil
}

func listExplicitGroupDefinitions(ctx context.Context, queryer store.RowQueryer, header explorerselection.ExplicitGroupRevision) ([]explorerselection.ExplicitGroupDefinition, error) {
	groups := make([]explorerselection.ExplicitGroupDefinition, 0)
	var afterOrdinal any
	afterGroupID := ""
	for {
		page := make([]explorerselection.ExplicitGroupDefinition, 0, explicitGroupPageSize)
		err := queryer.QueryRows(ctx, `FOR g IN @@c
FILTER g.revisionId == @revisionId
AND (@afterOrdinal == null OR g.ordinal > @afterOrdinal OR (g.ordinal == @afterOrdinal AND g.groupId > @afterGroupId))
SORT g.ordinal ASC, g.groupId ASC
LIMIT @limit
RETURN g`, explicitGroupPageSize, map[string]any{"@c": ExplicitGroupDefinitionsCollection, "revisionId": string(header.ID), "afterOrdinal": afterOrdinal, "afterGroupId": afterGroupID, "limit": explicitGroupPageSize}, func(row map[string]any) error {
			group, err := db.Decode[explorerselection.ExplicitGroupDefinition](row)
			if err != nil {
				return err
			}
			group = group.Canonical()
			groupID, _ := row["groupId"].(string)
			revisionID, _ := row["revisionId"].(string)
			project, _ := row["project"].(string)
			if groupID != string(group.ID) || revisionID != string(header.ID) || project != header.Project || row["_key"] != explicitGroupDefinitionKey(header.ID, group.ID) {
				return explorerselection.ErrCorruptExplicitGroupRevision
			}
			page = append(page, group)
			return nil
		})
		if err != nil {
			return nil, err
		}
		groups = append(groups, page...)
		if len(page) < explicitGroupPageSize {
			break
		}
		last := page[len(page)-1]
		afterOrdinal, afterGroupID = last.Ordinal, string(last.ID)
	}
	if len(groups) == 0 && header.State == explorerselection.ExplicitGroupRevisionStaging {
		return groups, nil
	}
	canonical, err := explorerselection.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", explorerselection.ErrCorruptExplicitGroupRevision, err)
	}
	if !reflect.DeepEqual(canonical, groups) {
		return nil, explorerselection.ErrCorruptExplicitGroupRevision
	}
	if header.State == explorerselection.ExplicitGroupRevisionComplete {
		digest, err := explorerselection.ExplicitGroupDefinitionDigest(groups)
		if err != nil || int64(len(groups)) != header.GroupCount || digest != header.DefinitionDigest {
			return nil, explorerselection.ErrCorruptExplicitGroupRevision
		}
	}
	return groups, nil
}

func validateExplicitGroupMembershipRow(row map[string]any, membership explorerselection.ExplicitGroupMembership, header explorerselection.ExplicitGroupRevision) error {
	membership = membership.Canonical()
	groupID, _ := row["groupId"].(string)
	revisionID, _ := row["revisionId"].(string)
	project, _ := row["project"].(string)
	generation, _ := row["generation"].(string)
	resourceType, _ := row["resourceType"].(string)
	resourceID, _ := row["id"].(string)
	if groupID != string(membership.GroupID) || revisionID != string(header.ID) || project != membership.Ref.Project || generation != membership.Ref.Generation || resourceType != membership.Ref.ResourceType || resourceID != membership.Ref.ID || row["_key"] != explicitGroupMembershipKey(header.ID, membership) {
		return explorerselection.ErrCorruptExplicitGroupRevision
	}
	if err := membership.Ref.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
		return fmt.Errorf("%w: %v", explorerselection.ErrCorruptExplicitGroupRevision, err)
	}
	return nil
}

func (s *ExplicitGroupStore) ListExplicitGroupDefinitions(ctx context.Context, project string, revisionID explorerselection.ExplicitGroupRevisionID) ([]explorerselection.ExplicitGroupDefinition, error) {
	header, err := s.GetExplicitGroupRevision(ctx, project, revisionID)
	if err != nil {
		return nil, err
	}
	return listExplicitGroupDefinitions(ctx, s.client, *header)
}

func (s *ExplicitGroupStore) VisitExplicitGroupMemberships(ctx context.Context, project string, revisionID explorerselection.ExplicitGroupRevisionID, after explorerselection.ExplicitGroupMembershipCursor, pageSize int, visit func(explorerselection.ExplicitGroupMembership) error) (explorerselection.ExplicitGroupMembershipCursor, error) {
	if visit == nil {
		return after, fmt.Errorf("explicit group membership visitor is required")
	}
	header, err := s.GetExplicitGroupRevision(ctx, project, revisionID)
	if err != nil {
		return after, err
	}
	groups, err := listExplicitGroupDefinitions(ctx, s.client, *header)
	if err != nil {
		return after, err
	}
	groupIDs := make(map[explorerselection.ExplicitGroupID]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}
	if pageSize <= 0 {
		pageSize = explicitGroupPageSize
	}
	page := make([]explorerselection.ExplicitGroupMembership, 0, pageSize)
	err = s.client.QueryRows(ctx, `FOR m IN @@c
FILTER m.revisionId == @revisionId
AND (m.groupId > @afterGroupId OR (m.groupId == @afterGroupId AND m.id > @afterResourceId))
SORT m.groupId ASC, m.id ASC
LIMIT @limit
RETURN m`, pageSize, map[string]any{"@c": ExplicitGroupMembershipsCollection, "revisionId": string(revisionID), "afterGroupId": string(after.GroupID), "afterResourceId": after.ResourceID, "limit": pageSize}, func(row map[string]any) error {
		membership, err := db.Decode[explorerselection.ExplicitGroupMembership](row)
		if err != nil {
			return err
		}
		if err := validateExplicitGroupMembershipRow(row, membership, *header); err != nil {
			return err
		}
		if _, ok := groupIDs[membership.GroupID]; !ok {
			return explorerselection.ErrCorruptExplicitGroupRevision
		}
		page = append(page, membership.Canonical())
		return nil
	})
	if err != nil {
		return after, err
	}
	for _, membership := range page {
		if err := visit(membership); err != nil {
			return after, err
		}
		after = explorerselection.ExplicitGroupMembershipCursor{GroupID: membership.GroupID, ResourceID: membership.Ref.ID}
	}
	return after, nil
}

func readStagedExplicitGroupMemberships(ctx context.Context, queryer store.RowQueryer, header explorerselection.ExplicitGroupRevision, groupIDs map[explorerselection.ExplicitGroupID]struct{}, visit func(explorerselection.ExplicitGroupMembership) error) error {
	cursor := explorerselection.ExplicitGroupMembershipCursor{}
	for {
		page := make([]explorerselection.ExplicitGroupMembership, 0, explicitGroupPageSize)
		err := queryer.QueryRows(ctx, `FOR m IN @@c
FILTER m.revisionId == @revisionId
AND (m.groupId > @afterGroupId OR (m.groupId == @afterGroupId AND m.id > @afterResourceId))
SORT m.groupId ASC, m.id ASC
LIMIT @limit
RETURN m`, explicitGroupPageSize, map[string]any{"@c": ExplicitGroupMembershipsCollection, "revisionId": string(header.ID), "afterGroupId": string(cursor.GroupID), "afterResourceId": cursor.ResourceID, "limit": explicitGroupPageSize}, func(row map[string]any) error {
			membership, err := db.Decode[explorerselection.ExplicitGroupMembership](row)
			if err != nil {
				return err
			}
			if err := validateExplicitGroupMembershipRow(row, membership, header); err != nil {
				return err
			}
			if _, ok := groupIDs[membership.GroupID]; !ok {
				return explorerselection.ErrCorruptExplicitGroupRevision
			}
			page = append(page, membership.Canonical())
			return nil
		})
		if err != nil {
			return err
		}
		for _, membership := range page {
			if err := visit(membership); err != nil {
				return err
			}
			cursor = explorerselection.ExplicitGroupMembershipCursor{GroupID: membership.GroupID, ResourceID: membership.Ref.ID}
		}
		if len(page) < explicitGroupPageSize {
			return nil
		}
	}
}
