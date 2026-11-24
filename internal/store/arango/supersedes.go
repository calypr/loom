package arango

import (
	"context"
	"errors"
	"fmt"

	"sort"
	"strings"
)

func (preview PreviewIndexes) AuditPreviewSupersededIndexes(ctx context.Context) (PreviewSupersedesAudit, error) {
	c := preview.client
	if c == nil || c.db == nil {
		return PreviewSupersedesAudit{}, errors.New("preview-index audit requires an open database")
	}
	audit := PreviewSupersedesAudit{
		SchemaVersion: 1, MigrationVersion: previewSupersedesMigrationVersion, Action: "audit",
		OtherOwnedIndexes: []PreviewIndexInventoryEntry{}, Candidates: []PreviewSupersededIndex{},
	}
	collections, err := c.db.Collections(ctx)
	if err != nil {
		return audit, fmt.Errorf("list collections for preview-index audit: %w", err)
	}
	for _, collection := range collections {
		name := collection.Name()
		if strings.HasPrefix(name, "_") || name == SchemaMigrationsCollection {
			continue
		}
		audit.CollectionsScanned++
		indexes, err := collection.Indexes(ctx)
		if err != nil {
			return audit, fmt.Errorf("list indexes in %q: %w", name, err)
		}
		entries, candidates, ownedCount := auditPreviewIndexCollection(name, indexes)
		audit.CompilerOwnedCount += ownedCount
		audit.OtherOwnedIndexes = append(audit.OtherOwnedIndexes, entries...)
		audit.Candidates = append(audit.Candidates, candidates...)
	}
	audit.LegacyCandidateCount = len(audit.Candidates)
	for _, candidate := range audit.Candidates {
		if candidate.ReplacementExists {
			audit.ReplacementPresentCount++
		}
	}
	sort.Slice(audit.Candidates, func(i, j int) bool {
		if audit.Candidates[i].Collection != audit.Candidates[j].Collection {
			return audit.Candidates[i].Collection < audit.Candidates[j].Collection
		}
		return audit.Candidates[i].OldName < audit.Candidates[j].OldName
	})
	sort.Slice(audit.OtherOwnedIndexes, func(i, j int) bool {
		if audit.OtherOwnedIndexes[i].Collection != audit.OtherOwnedIndexes[j].Collection {
			return audit.OtherOwnedIndexes[i].Collection < audit.OtherOwnedIndexes[j].Collection
		}
		return audit.OtherOwnedIndexes[i].Name < audit.OtherOwnedIndexes[j].Name
	})
	audit.LedgerPresent, err = preview.previewSupersedesLedgerExists(ctx)
	if err != nil {
		return audit, err
	}
	return audit, nil
}

func (preview PreviewIndexes) previewSupersedesLedgerExists(ctx context.Context) (bool, error) {
	c := preview.client
	exists, err := c.db.CollectionExists(ctx, SchemaMigrationsCollection)
	if err != nil || !exists {
		return exists, err
	}
	found := false
	err = c.QueryRows(ctx, "FOR migration IN @@collection FILTER migration._key == @key LIMIT 1 RETURN {key: migration._key}", 1,
		map[string]interface{}{"@collection": SchemaMigrationsCollection, "key": previewSupersedesMigrationVersion},
		func(map[string]interface{}) error { found = true; return nil })
	return found, err
}
