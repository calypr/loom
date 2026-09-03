package arango

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

func (s *Store) PublishAuthoring(ctx context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	receiptDoc, err := document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	revisionDoc, err := document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	management := explorer.ManagementInteractive
	if revision.ExplorerID == "default" {
		management = explorer.ManagementRepository
	}
	var out *explorer.Revision
	err = s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{datasetarango.LifecycleCollection, ExplorersCollection, RevisionsCollection, CompilationReceiptsCollection}}, func(txCtx context.Context, tx store.RowQueryer) error {
		if err := tx.QueryRows(txCtx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": CompilationReceiptsCollection, "doc": receiptDoc}, func(map[string]any) error { return nil }); err != nil {
			return err
		}
		var stored *explorer.CompilationReceipt
		if err := tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId RETURN d`, 1, map[string]any{"@c": CompilationReceiptsCollection, "key": receipt.ID, "project": receipt.Project, "explorerId": receipt.ExplorerID}, func(row map[string]any) error {
			value, err := decode[explorer.CompilationReceipt](row)
			if err != nil {
				return err
			}
			if err := validateReceipt(value); err != nil {
				return err
			}
			stored = &value
			return nil
		}); err != nil {
			return err
		}
		if stored == nil {
			return explorer.ErrNotFound
		}
		if !sameReceipt(*stored, receipt) {
			return explorer.ErrCorruptReceipt
		}
		if err := tx.QueryRows(txCtx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID, "doc": revisionDoc}, func(map[string]any) error { return nil }); err != nil {
			return err
		}
		if _, err := datasetarango.CompareAndSwapActivateRelease(txCtx, tx, release, expectedReleaseRevision); err != nil {
			return err
		}
		row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{"@explorers": ExplorersCollection, "@revisions": RevisionsCollection, "explorerKey": explorerKey(revision.Project, revision.ExplorerID), "revisionKey": revision.ID, "project": revision.Project, "explorerId": revision.ExplorerID, "management": management})
		if err != nil {
			return err
		}
		if !found {
			return explorer.ErrDraftConflict
		}
		state, err := activationStateFromRow(row)
		if err != nil {
			return err
		}
		if err := activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC()); err != nil {
			return err
		}
		return tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	})
	return out, err
}
