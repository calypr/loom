package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

func (s *Store) SaveDraft(ctx context.Context, e explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	e.UpdatedAt = time.Now().UTC()
	draftPatch, err := draftPatchDocument(e)
	if err != nil {
		return nil, err
	}
	digest := ""
	if len(expectedDigest) > 0 {
		digest = strings.TrimSpace(expectedDigest[0])
	}
	var out *explorer.Explorer
	key := explorerKey(e.Project, e.ExplorerID)
	cas := map[string]any{
		"key": key, "project": e.Project, "explorerId": e.ExplorerID,
		"expected": expected, "expectedDigest": digest,
	}
	err = s.client.WithTransaction(ctx, store.TransactionCollections{
		Write: []string{ExplorersCollection, DraftRevisionsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		var previous *explorer.Explorer
		if err := tx.QueryRows(txCtx, `FOR d IN @@c
FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId
  AND (d.draftVersion == @expected OR (!HAS(d, "draftVersion") AND @expected == 0))
  AND (@expectedDigest == "" OR d.draftDigest == @expectedDigest)
RETURN d`, 1, map[string]any{
			"@c": ExplorersCollection, "key": key, "project": e.Project, "explorerId": e.ExplorerID,
			"expected": expected, "expectedDigest": digest,
		}, func(row map[string]any) error {
			value, decodeErr := decode[explorer.Explorer](row)
			previous = &value
			return decodeErr
		}); err != nil {
			return fmt.Errorf("read current Explorer draft for history: %w", err)
		}
		if previous == nil {
			return explorer.ErrDraftConflict
		}

		revision := draftRevisionFromExplorer(*previous)
		revision.ID = draftRevisionKey(previous.Project, previous.ExplorerID, previous.DraftVersion)
		revisionDoc, err := document(revision, revision.ID)
		if err != nil {
			return fmt.Errorf("encode prior Explorer draft revision: %w", err)
		}
		if err := tx.QueryRows(txCtx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{
			"@c": DraftRevisionsCollection, "doc": revisionDoc,
		}, func(map[string]any) error { return nil }); err != nil {
			return fmt.Errorf("persist prior Explorer draft revision: %w", err)
		}

		var updated *explorer.Explorer
		if err := tx.QueryRows(txCtx, `FOR d IN @@c
FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId
  AND (d.draftVersion == @expected OR (!HAS(d, "draftVersion") AND @expected == 0))
  AND (@expectedDigest == "" OR d.draftDigest == @expectedDigest)
LET base = @clearDraftConfig ? UNSET(d, "draftConfig") : d
UPDATE d WITH MERGE(base, @doc, {
  draftVersion: @nextVersion,
  previousDraftRevisionId: @revisionId
}) IN @@c RETURN NEW`, 1, map[string]any{
			"@c": ExplorersCollection, "key": cas["key"], "project": cas["project"], "explorerId": cas["explorerId"],
			"expected": cas["expected"], "expectedDigest": cas["expectedDigest"],
			"nextVersion": expected + 1, "revisionId": revision.ID, "doc": draftPatch,
			"clearDraftConfig": e.DraftConfig == nil,
		}, func(row map[string]any) error {
			value, decodeErr := decode[explorer.Explorer](row)
			updated = &value
			return decodeErr
		}); err != nil {
			return fmt.Errorf("save Explorer draft with history: %w", err)
		}
		if updated == nil {
			return explorer.ErrDraftConflict
		}
		out = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetDraftRevision(ctx context.Context, project, explorerID, revisionID string) (*explorer.DraftRevision, error) {
	project = strings.TrimSpace(project)
	explorerID = strings.TrimSpace(explorerID)
	revisionID = strings.TrimSpace(revisionID)
	if project == "" || explorerID == "" || revisionID == "" {
		return nil, explorer.ErrNotFound
	}
	var out *explorer.DraftRevision
	err := s.client.QueryRows(ctx, `FOR d IN @@c
FILTER d._key == @key AND d.id == @revisionId AND d.project == @project AND d.explorerId == @explorerId
RETURN d`, 1, map[string]any{
		"@c": DraftRevisionsCollection, "key": revisionID, "revisionId": revisionID,
		"project": project, "explorerId": explorerID,
	}, func(row map[string]any) error {
		value, decodeErr := decode[explorer.DraftRevision](row)
		out = &value
		return decodeErr
	})
	if err != nil {
		return nil, fmt.Errorf("read Explorer draft revision: %w", err)
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func draftRevisionFromExplorer(value explorer.Explorer) explorer.DraftRevision {
	return explorer.DraftRevision{
		Project: value.Project, ExplorerID: value.ExplorerID, DraftVersion: value.DraftVersion,
		DraftDigest: value.DraftDigest, DraftConfig: append([]byte(nil), value.DraftConfig...), Title: value.Title,
		SnapshotToken: value.DraftSnapshotToken, SourceGeneration: value.DraftSourceGeneration,
		AuthorizationScopeDigest: value.DraftAuthorizationScopeDigest, UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt,
	}
}

func draftRevisionKey(project, explorerID string, version int64) string {
	return "draft_revision_" + key(project, explorerID, strconv.FormatInt(version, 10))
}

func draftPatchDocument(value explorer.Explorer) (map[string]any, error) {
	patch := map[string]any{
		"title":                         value.Title,
		"draftDigest":                   value.DraftDigest,
		"draftSnapshotToken":            value.DraftSnapshotToken,
		"draftSourceGeneration":         value.DraftSourceGeneration,
		"draftAuthorizationScopeDigest": value.DraftAuthorizationScopeDigest,
		"lastAuthoringCommandId":        value.LastAuthoringCommandID,
		"lastAuthoringCommandDigest":    value.LastAuthoringCommandDigest,
		"lastAuthoringCommandResults":   value.LastAuthoringCommandResults,
		"updatedBy":                     value.UpdatedBy,
		"updatedAt":                     value.UpdatedAt,
	}
	if value.DraftConfig != nil {
		patch["draftConfig"] = value.DraftConfig
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("encode Explorer draft patch: %w", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode Explorer draft patch: %w", err)
	}
	return encoded, nil
}
