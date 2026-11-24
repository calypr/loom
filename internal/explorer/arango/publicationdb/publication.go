package publicationdb

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/releasecommit"
	store "github.com/calypr/loom/internal/store/arango"
)

func (s *PublicationStore) PublishAuthoring(ctx context.Context, receipt receiptmodel.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	receiptDoc, err := db.Document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	revisionDoc, err := db.Document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	management := viewer.ManagementInteractive
	if revision.ExplorerID == "default" {
		management = viewer.ManagementRepository
	}
	var out *explorer.Revision
	_, err = releasecommit.GoLive(ctx, s.client, releasecommit.Request{
		Release: release, ExpectedRevision: expectedReleaseRevision, RequireCurrentGeneration: true,
		Collections: store.TransactionCollections{Write: []string{db.ExplorersCollection, db.RevisionsCollection, db.CompilationReceiptsCollection, db.ViewBuilderSettingsCollection}},
		Prepare: func(txCtx context.Context, tx store.RowQueryer) (releasecommit.Participant, error) {
			if err := tx.QueryRows(txCtx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": db.CompilationReceiptsCollection, "doc": receiptDoc}, func(map[string]any) error { return nil }); err != nil {
				return nil, err
			}
			var stored *receiptmodel.CompilationReceipt
			if err := tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId RETURN d`, 1, map[string]any{"@c": db.CompilationReceiptsCollection, "key": receipt.ID, "project": receipt.Project, "explorerId": receipt.ExplorerID}, func(row map[string]any) error {
				value, err := db.Decode[receiptmodel.CompilationReceipt](row)
				if err != nil {
					return err
				}
				if err := validateReceipt(value); err != nil {
					return err
				}
				stored = &value
				return nil
			}); err != nil {
				return nil, err
			}
			if stored == nil {
				return nil, explorer.ErrNotFound
			}
			if !sameReceipt(*stored, receipt) {
				return nil, explorer.ErrCorruptReceipt
			}
			if err := tx.QueryRows(txCtx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN NEW`, 1, map[string]any{"@c": db.RevisionsCollection, "key": revision.ID, "doc": revisionDoc}, func(map[string]any) error { return nil }); err != nil {
				return nil, err
			}
			return func(txCtx context.Context, tx store.RowQueryer, _ dataset.ActiveRelease) error {
				row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{"@explorers": db.ExplorersCollection, "@revisions": db.RevisionsCollection, "explorerKey": explorerDocumentKey(revision.Project, revision.ExplorerID), "revisionKey": revision.ID, "project": revision.Project, "explorerId": revision.ExplorerID, "management": management})
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
				if err := registerAuthoringViewCatalog(txCtx, tx, revision); err != nil {
					return err
				}
				return tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": db.RevisionsCollection, "key": revision.ID}, func(row map[string]any) error {
					value, err := db.Decode[explorer.Revision](row)
					out = &value
					return err
				})
			}, nil
		},
	})
	return out, err
}

func registerAuthoringViewCatalog(ctx context.Context, tx store.RowQueryer, revision explorer.Revision) error {
	name := revision.Recipe.Name
	snapshot := AdoptedCatalogSnapshot(revision, name)
	current, err := readViewBuilderSettings(ctx, tx, revision.Project)
	if err != nil {
		return err
	}
	settings := emptyViewBuilderSettings(revision.Project)
	if current != nil {
		if current.value.Catalogs[name] == snapshot.ID {
			return nil
		}
		settings = cloneViewBuilderSettings(current.value)
	}

	if _, err := explorer.ViewBuilderCatalogFromRevision(snapshot); err != nil {
		return err
	}
	stored, err := readProjectConfigRevisions(ctx, tx, []string{snapshot.ID})
	if err != nil {
		return err
	}
	if err := insertProjectConfigRevision(ctx, tx, snapshot, stored[snapshot.ID]); err != nil {
		return err
	}
	settings.Catalogs[name] = snapshot.ID
	settings.Version++
	return writeViewBuilderSettings(ctx, tx, current, settings)
}
