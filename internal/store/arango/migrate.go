package arango

import (
	"context"
	"errors"
	"fmt"

	"sort"

	"time"
)

func (preview PreviewIndexes) MigratePreviewSupersededIndexes(ctx context.Context, dryRun bool) (PreviewSupersedesAudit, error) {
	c := preview.client
	if c == nil || c.db == nil {
		return PreviewSupersedesAudit{}, errors.New("preview-index migration requires an open database")
	}
	c.previewIndexMu.Lock()
	defer c.previewIndexMu.Unlock()
	audit, err := preview.AuditPreviewSupersededIndexes(ctx)
	if err != nil {
		return audit, err
	}
	if dryRun {
		audit.Action = "dry-run"
		audit.WouldRecordVersion = !audit.LedgerPresent
		return audit, nil
	}
	audit.Action = "apply"
	if audit.LedgerPresent {
		if audit.LegacyCandidateCount != 0 {
			return audit, errors.New("preview-index migration ledger exists but legacy indexes remain")
		}
		return audit, nil
	}
	for _, candidate := range audit.Candidates {
		collection, err := c.db.GetCollection(ctx, candidate.Collection, nil)
		if err != nil {
			return audit, fmt.Errorf("open collection %q for migration: %w", candidate.Collection, err)
		}
		if err := migratePreviewSupersededIndex(ctx, collection, candidate); err != nil {
			return audit, fmt.Errorf("migrate %s/%s: %w", candidate.Collection, candidate.OldName, err)
		}
	}
	final, err := preview.AuditPreviewSupersededIndexes(ctx)
	final.Action = "apply"
	if err != nil {
		return final, err
	}
	if final.LegacyCandidateCount != 0 {
		return final, errors.New("preview-index migration cannot complete while legacy indexes remain")
	}
	if err := preview.recordPreviewSupersedesMigration(ctx, audit.Candidates); err != nil {
		return final, err
	}
	final.LedgerPresent = true
	return final, nil
}

func (preview PreviewIndexes) recordPreviewSupersedesMigration(ctx context.Context, candidates []PreviewSupersededIndex) error {
	c := preview.client
	ledger, err := c.ensureCollection(ctx, SchemaMigrationsCollection, false)
	if err != nil {
		return fmt.Errorf("open migration ledger: %w", err)
	}
	replaced := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		replaced = append(replaced, candidate.Collection+"/"+candidate.OldName)
	}
	sort.Strings(replaced)
	_, err = ledger.CreateDocument(ctx, map[string]interface{}{
		"_key": previewSupersedesMigrationVersion, "version": previewSupersedesMigrationVersion,
		"completedAt": time.Now().UTC(), "replaced": replaced,
	})
	if err != nil {
		return fmt.Errorf("record preview-index migration: %w", err)
	}
	return nil
}
