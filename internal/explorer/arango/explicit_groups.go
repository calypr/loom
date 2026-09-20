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

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	explicitGroupWriterLease  = 10 * time.Minute
	explicitGroupCleanupLimit = 100
	explicitGroupPageSize     = 1000
)

var _ explorer.ExplicitGroupRepository = (*Store)(nil)

func explicitGroupDefinitionKey(revisionID explorer.ExplicitGroupRevisionID, groupID explorer.ExplicitGroupID) string {
	return "groupdef_" + explicitGroupStorageKey(string(revisionID), string(groupID))
}

func explicitGroupMembershipKey(revisionID explorer.ExplicitGroupRevisionID, membership explorer.ExplicitGroupMembership) string {
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

func (s *Store) BeginExplicitGroupRevision(ctx context.Context, revision explorer.ExplicitGroupRevision, source explorer.SelectionRevision, writerToken string) (*explorer.ExplicitGroupRevision, error) {
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
	storedSource, err := s.GetSelection(ctx, revision.Project, revision.SourceSelectionRevisionID)
	if err != nil {
		return nil, fmt.Errorf("%w: source selection unavailable: %w", explorer.ErrExplicitGroupRevisionStaleSource, err)
	}
	if err := revision.ValidateSourceSelection(*storedSource); err != nil {
		return nil, err
	}
	if revision.State != explorer.ExplicitGroupRevisionStaging {
		return nil, fmt.Errorf("explicit group revision must begin in staging state")
	}
	if err := s.CleanupExplicitGroupStaging(ctx, time.Now().UTC(), explicitGroupCleanupLimit); err != nil {
		return nil, err
	}
	doc, err := document(revision, string(revision.ID))
	if err != nil {
		return nil, err
	}
	doc["writerToken"] = writerToken
	doc["writerExpiresAt"] = time.Now().UTC().Add(explicitGroupWriterLease).UnixMilli()
	if err := s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		return nil, err
	}
	stored, row, err := s.readExplicitGroupRevision(ctx, s.client, revision.ID, revision.Project)
	if err != nil {
		return nil, err
	}
	if !sameExplicitGroupRevisionIntent(*stored, revision) {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if stored.State == explorer.ExplicitGroupRevisionStaging {
		storedWriterToken, _ := row["writerToken"].(string)
		if strings.TrimSpace(storedWriterToken) != writerToken {
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
		var renewed bool
		if err := s.client.QueryRows(ctx, `FOR d IN @@c
FILTER d._key == @key AND d.state == @state AND d.writerToken == @writerToken
UPDATE d WITH {writerExpiresAt: @expiresAt} IN @@c
RETURN {renewed: true}`, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revision.ID), "state": explorer.ExplicitGroupRevisionStaging, "writerToken": writerToken, "expiresAt": time.Now().UTC().Add(explicitGroupWriterLease).UnixMilli()}, func(map[string]any) error {
			renewed = true
			return nil
		}); err != nil {
			return nil, err
		}
		if !renewed {
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
	}
	return stored, nil
}

func sameExplicitGroupRevisionIntent(left, right explorer.ExplicitGroupRevision) bool {
	left, right = left.Canonical(), right.Canonical()
	return left.ID == right.ID && left.Project == right.Project && left.Generation == right.Generation && left.ScopeDigest == right.ScopeDigest && left.ResourceType == right.ResourceType && left.SourceSelectionRevisionID == right.SourceSelectionRevisionID && left.SourceMembershipDigest == right.SourceMembershipDigest && left.IdempotencyKey == right.IdempotencyKey
}

func (s *Store) PutExplicitGroupDefinitions(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string, groups []explorer.ExplicitGroupDefinition) error {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
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
				return explorer.ErrExplicitGroupRevisionConflict
			}
			return renewExplicitGroupWriter(txCtx, tx, revisionID, writerToken)
		}
		docs := make([]map[string]any, 0, len(canonical))
		for _, group := range canonical {
			doc, err := document(group, explicitGroupDefinitionKey(revisionID, group.ID))
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
				return explorer.ErrCorruptExplicitGroupRevision
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
			return explorer.ErrExplicitGroupRevisionConflict
		}
		return renewExplicitGroupWriter(txCtx, tx, revisionID, writerToken)
	})
}

func (s *Store) AppendExplicitGroupMemberships(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string, memberships []explorer.ExplicitGroupMembership) ([]explorer.ExplicitGroupMembership, error) {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if len(memberships) == 0 {
		return nil, nil
	}
	inserted := make([]explorer.ExplicitGroupMembership, 0, len(memberships))
	err := s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{ExplicitGroupRevisionsCollection, ExplicitGroupMembershipsCollection}}, func(txCtx context.Context, tx store.RowQueryer) error {
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
		canonical, err := explorer.CanonicalExplicitGroupMemberships(*header, groups, memberships)
		if err != nil {
			return err
		}
		docs := make([]map[string]any, 0, len(canonical))
		byKey := make(map[string]explorer.ExplicitGroupMembership, len(canonical))
		for _, membership := range canonical {
			memberKey := explicitGroupMembershipKey(revisionID, membership)
			doc, err := document(membership, memberKey)
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
		stored := make(map[string]explorer.ExplicitGroupMembership, len(keys))
		if err := tx.QueryRows(txCtx, `FOR m IN @@c FILTER m._key IN @keys RETURN m`, len(keys), map[string]any{"@c": ExplicitGroupMembershipsCollection, "keys": keys}, func(row map[string]any) error {
			membership, err := decode[explorer.ExplicitGroupMembership](row)
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
				return explorer.ErrExplicitGroupRevisionConflict
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

func explicitGroupWriter(header *explorer.ExplicitGroupRevision, row map[string]any, writerToken string) error {
	if header == nil {
		return explorer.ErrExplicitGroupRevisionNotFound
	}
	if header.State != explorer.ExplicitGroupRevisionStaging {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	storedWriterToken, _ := row["writerToken"].(string)
	if strings.TrimSpace(storedWriterToken) != writerToken {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return nil
}

func renewExplicitGroupWriter(ctx context.Context, tx store.RowQueryer, revisionID explorer.ExplicitGroupRevisionID, writerToken string) error {
	var renewed bool
	if err := tx.QueryRows(ctx, `FOR d IN @@c
FILTER d._key == @key AND d.state == @state AND d.writerToken == @writerToken
UPDATE d WITH {writerExpiresAt: @expiresAt} IN @@c
RETURN {renewed: true}`, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revisionID), "state": explorer.ExplicitGroupRevisionStaging, "writerToken": writerToken, "expiresAt": time.Now().UTC().Add(explicitGroupWriterLease).UnixMilli()}, func(map[string]any) error {
		renewed = true
		return nil
	}); err != nil {
		return err
	}
	if !renewed {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return nil
}

func (s *Store) DigestExplicitGroupRevision(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) (string, string, int64, int64, error) {
	header, _, err := s.readExplicitGroupRevision(ctx, s.client, revisionID, project)
	if err != nil {
		return "", "", 0, 0, err
	}
	if header.State == explorer.ExplicitGroupRevisionComplete {
		return header.DefinitionDigest, header.MembershipDigest, header.GroupCount, header.MemberCount, nil
	}
	return digestExplicitGroupChildren(ctx, s.client, *header)
}

func digestExplicitGroupChildren(ctx context.Context, queryer store.RowQueryer, header explorer.ExplicitGroupRevision) (string, string, int64, int64, error) {
	groups, err := listExplicitGroupDefinitions(ctx, queryer, header)
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
	hash := sha256.New()
	explorer.StartExplicitGroupMembershipDigest(hash)
	var memberCount int64
	err = readStagedExplicitGroupMemberships(ctx, queryer, header, groupIDs, func(membership explorer.ExplicitGroupMembership) error {
		explorer.WriteExplicitGroupMembershipFrame(hash, membership)
		memberCount++
		return nil
	})
	if err != nil {
		return "", "", 0, 0, err
	}
	return definitionDigest, hex.EncodeToString(hash.Sum(nil)), int64(len(groups)), memberCount, nil
}

func (s *Store) CompleteExplicitGroupRevision(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken, definitionDigest, membershipDigest string, groupCount, memberCount int64, completedAt time.Time) (*explorer.ExplicitGroupRevision, error) {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	definitionDigest, membershipDigest = strings.TrimSpace(definitionDigest), strings.TrimSpace(membershipDigest)
	if revisionID == "" || writerToken == "" || definitionDigest == "" || membershipDigest == "" || groupCount <= 0 || memberCount < 0 {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	var result *explorer.ExplicitGroupRevision
	err := s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{ExplicitGroupDefinitionsCollection, ExplicitGroupMembershipsCollection},
		Write: []string{ExplicitGroupRevisionsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		header, row, err := s.readExplicitGroupRevision(txCtx, tx, revisionID, "")
		if err != nil {
			return err
		}
		if header.State == explorer.ExplicitGroupRevisionComplete {
			if header.DefinitionDigest != definitionDigest || header.MembershipDigest != membershipDigest || header.GroupCount != groupCount || header.MemberCount != memberCount {
				return explorer.ErrExplicitGroupRevisionConflict
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
			return explorer.ErrExplicitGroupRevisionConflict
		}
		var changed bool
		query := `FOR d IN @@c
FILTER d._key == @key AND d.state == @staging AND d.writerToken == @writerToken
UPDATE d WITH {state: @complete, definitionDigest: @definitionDigest, membershipDigest: @membershipDigest, groupCount: @groupCount, memberCount: @memberCount, completedAt: @completedAt} IN @@c
RETURN NEW`
		if err := tx.QueryRows(txCtx, query, 1, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revisionID), "staging": explorer.ExplicitGroupRevisionStaging, "complete": explorer.ExplicitGroupRevisionComplete, "writerToken": writerToken, "definitionDigest": definitionDigest, "membershipDigest": membershipDigest, "groupCount": groupCount, "memberCount": memberCount, "completedAt": completedAt.UTC()}, func(row map[string]any) error {
			value, err := decode[explorer.ExplicitGroupRevision](row)
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
			return explorer.ErrExplicitGroupRevisionConflict
		}
		return nil
	})
	return result, err
}

func (s *Store) AbortExplicitGroupRevision(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string) error {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" {
		return nil
	}
	if writerToken == "" {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{ExplicitGroupRevisionsCollection, ExplicitGroupDefinitionsCollection, ExplicitGroupMembershipsCollection}}, func(txCtx context.Context, tx store.RowQueryer) error {
		header, row, err := s.readExplicitGroupRevision(txCtx, tx, revisionID, "")
		if errors.Is(err, explorer.ErrExplicitGroupRevisionNotFound) {
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
		return tx.QueryRows(txCtx, query, 1, map[string]any{"@headers": ExplicitGroupRevisionsCollection, "@definitions": ExplicitGroupDefinitionsCollection, "@memberships": ExplicitGroupMembershipsCollection, "revisionId": string(revisionID), "state": explorer.ExplicitGroupRevisionStaging, "writerToken": writerToken}, func(row map[string]any) error {
			removed, _ := row["removed"].(bool)
			if !removed {
				return explorer.ErrExplicitGroupRevisionConflict
			}
			return nil
		})
	})
}

func (s *Store) CleanupExplicitGroupStaging(ctx context.Context, before time.Time, limit int) error {
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
	return s.client.QueryRows(ctx, query, limit, map[string]any{"@revisions": ExplicitGroupRevisionsCollection, "@definitions": ExplicitGroupDefinitionsCollection, "@memberships": ExplicitGroupMembershipsCollection, "state": explorer.ExplicitGroupRevisionStaging, "before": before.UTC(), "now": time.Now().UTC().UnixMilli(), "limit": limit}, func(map[string]any) error { return nil })
}

func (s *Store) GetExplicitGroupRevision(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) (*explorer.ExplicitGroupRevision, error) {
	header, _, err := s.readExplicitGroupRevision(ctx, s.client, revisionID, project)
	if err != nil {
		return nil, err
	}
	if header.State != explorer.ExplicitGroupRevisionComplete {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	return header, nil
}

func (s *Store) readExplicitGroupRevision(ctx context.Context, queryer store.RowQueryer, revisionID explorer.ExplicitGroupRevisionID, project string) (*explorer.ExplicitGroupRevision, map[string]any, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return nil, nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	query := `FOR d IN @@c FILTER d._key == @key`
	binds := map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revisionID)}
	if strings.TrimSpace(project) != "" {
		query += ` AND d.project == @project`
		binds["project"] = strings.TrimSpace(project)
	}
	query += ` RETURN d`
	var result *explorer.ExplicitGroupRevision
	var found map[string]any
	err := queryer.QueryRows(ctx, query, 1, binds, func(row map[string]any) error {
		value, err := decode[explorer.ExplicitGroupRevision](row)
		if err != nil {
			return err
		}
		if string(value.ID) != string(revisionID) {
			return explorer.ErrCorruptExplicitGroupRevision
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
		}
		result = &value
		found = row
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	return result, found, nil
}

func listExplicitGroupDefinitions(ctx context.Context, queryer store.RowQueryer, header explorer.ExplicitGroupRevision) ([]explorer.ExplicitGroupDefinition, error) {
	groups := make([]explorer.ExplicitGroupDefinition, 0)
	var afterOrdinal any
	afterGroupID := ""
	for {
		page := make([]explorer.ExplicitGroupDefinition, 0, explicitGroupPageSize)
		err := queryer.QueryRows(ctx, `FOR g IN @@c
FILTER g.revisionId == @revisionId
AND (@afterOrdinal == null OR g.ordinal > @afterOrdinal OR (g.ordinal == @afterOrdinal AND g.groupId > @afterGroupId))
SORT g.ordinal ASC, g.groupId ASC
LIMIT @limit
RETURN g`, explicitGroupPageSize, map[string]any{"@c": ExplicitGroupDefinitionsCollection, "revisionId": string(header.ID), "afterOrdinal": afterOrdinal, "afterGroupId": afterGroupID, "limit": explicitGroupPageSize}, func(row map[string]any) error {
			group, err := decode[explorer.ExplicitGroupDefinition](row)
			if err != nil {
				return err
			}
			group = group.Canonical()
			groupID, _ := row["groupId"].(string)
			revisionID, _ := row["revisionId"].(string)
			project, _ := row["project"].(string)
			if groupID != string(group.ID) || revisionID != string(header.ID) || project != header.Project || row["_key"] != explicitGroupDefinitionKey(header.ID, group.ID) {
				return explorer.ErrCorruptExplicitGroupRevision
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
	if len(groups) == 0 && header.State == explorer.ExplicitGroupRevisionStaging {
		return groups, nil
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	if !reflect.DeepEqual(canonical, groups) {
		return nil, explorer.ErrCorruptExplicitGroupRevision
	}
	if header.State == explorer.ExplicitGroupRevisionComplete {
		digest, err := explorer.ExplicitGroupDefinitionDigest(groups)
		if err != nil || int64(len(groups)) != header.GroupCount || digest != header.DefinitionDigest {
			return nil, explorer.ErrCorruptExplicitGroupRevision
		}
	}
	return groups, nil
}

func validateExplicitGroupMembershipRow(row map[string]any, membership explorer.ExplicitGroupMembership, header explorer.ExplicitGroupRevision) error {
	membership = membership.Canonical()
	groupID, _ := row["groupId"].(string)
	revisionID, _ := row["revisionId"].(string)
	project, _ := row["project"].(string)
	generation, _ := row["generation"].(string)
	resourceType, _ := row["resourceType"].(string)
	resourceID, _ := row["id"].(string)
	if groupID != string(membership.GroupID) || revisionID != string(header.ID) || project != membership.Ref.Project || generation != membership.Ref.Generation || resourceType != membership.Ref.ResourceType || resourceID != membership.Ref.ID || row["_key"] != explicitGroupMembershipKey(header.ID, membership) {
		return explorer.ErrCorruptExplicitGroupRevision
	}
	if err := membership.Ref.Validate(header.Project, header.Generation, header.ResourceType); err != nil {
		return fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	return nil
}

func (s *Store) ListExplicitGroupDefinitions(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) ([]explorer.ExplicitGroupDefinition, error) {
	header, err := s.GetExplicitGroupRevision(ctx, project, revisionID)
	if err != nil {
		return nil, err
	}
	return listExplicitGroupDefinitions(ctx, s.client, *header)
}

func (s *Store) VisitExplicitGroupMemberships(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID, after explorer.ExplicitGroupMembershipCursor, pageSize int, visit func(explorer.ExplicitGroupMembership) error) (explorer.ExplicitGroupMembershipCursor, error) {
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
	groupIDs := make(map[explorer.ExplicitGroupID]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}
	if pageSize <= 0 {
		pageSize = explicitGroupPageSize
	}
	page := make([]explorer.ExplicitGroupMembership, 0, pageSize)
	err = s.client.QueryRows(ctx, `FOR m IN @@c
FILTER m.revisionId == @revisionId
AND (m.groupId > @afterGroupId OR (m.groupId == @afterGroupId AND m.id > @afterResourceId))
SORT m.groupId ASC, m.id ASC
LIMIT @limit
RETURN m`, pageSize, map[string]any{"@c": ExplicitGroupMembershipsCollection, "revisionId": string(revisionID), "afterGroupId": string(after.GroupID), "afterResourceId": after.ResourceID, "limit": pageSize}, func(row map[string]any) error {
		membership, err := decode[explorer.ExplicitGroupMembership](row)
		if err != nil {
			return err
		}
		if err := validateExplicitGroupMembershipRow(row, membership, *header); err != nil {
			return err
		}
		if _, ok := groupIDs[membership.GroupID]; !ok {
			return explorer.ErrCorruptExplicitGroupRevision
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
		after = explorer.ExplicitGroupMembershipCursor{GroupID: membership.GroupID, ResourceID: membership.Ref.ID}
	}
	return after, nil
}

func readStagedExplicitGroupMemberships(ctx context.Context, queryer store.RowQueryer, header explorer.ExplicitGroupRevision, groupIDs map[explorer.ExplicitGroupID]struct{}, visit func(explorer.ExplicitGroupMembership) error) error {
	cursor := explorer.ExplicitGroupMembershipCursor{}
	for {
		page := make([]explorer.ExplicitGroupMembership, 0, explicitGroupPageSize)
		err := queryer.QueryRows(ctx, `FOR m IN @@c
FILTER m.revisionId == @revisionId
AND (m.groupId > @afterGroupId OR (m.groupId == @afterGroupId AND m.id > @afterResourceId))
SORT m.groupId ASC, m.id ASC
LIMIT @limit
RETURN m`, explicitGroupPageSize, map[string]any{"@c": ExplicitGroupMembershipsCollection, "revisionId": string(header.ID), "afterGroupId": string(cursor.GroupID), "afterResourceId": cursor.ResourceID, "limit": explicitGroupPageSize}, func(row map[string]any) error {
			membership, err := decode[explorer.ExplicitGroupMembership](row)
			if err != nil {
				return err
			}
			if err := validateExplicitGroupMembershipRow(row, membership, header); err != nil {
				return err
			}
			if _, ok := groupIDs[membership.GroupID]; !ok {
				return explorer.ErrCorruptExplicitGroupRevision
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
			cursor = explorer.ExplicitGroupMembershipCursor{GroupID: membership.GroupID, ResourceID: membership.Ref.ID}
		}
		if len(page) < explicitGroupPageSize {
			return nil
		}
	}
}
