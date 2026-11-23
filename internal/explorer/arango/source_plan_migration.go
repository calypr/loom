package arango

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	store "github.com/calypr/loom/internal/store/arango"
)

type SourcePlanMigrationReport struct {
	PreviousDraftVersion int64  `json:"previousDraftVersion"`
	DraftVersion         int64  `json:"draftVersion"`
	DraftDigest          string `json:"draftDigest"`
	Changed              bool   `json:"changed"`
	DryRun               bool   `json:"dryRun"`
}

func MigrateStoredSourcePlan(ctx context.Context, client *store.Client, project, explorerID string, dryRun bool) (SourcePlanMigrationReport, error) {
	report := SourcePlanMigrationReport{DryRun: dryRun}
	adapter, err := New(client)
	if err != nil {
		return report, err
	}
	type storedOwner struct {
		explorer.Explorer
		Revision string `json:"_rev"`
	}
	var stored storedOwner
	err = client.QueryRows(ctx, db.MigrationJSONQuery(`FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorer RETURN d`, true), 1,
		map[string]any{"@c": ExplorersCollection, "key": explorerKey(project, explorerID), "project": project, "explorer": explorerID}, func(row map[string]any) error {
			value, decodeErr := db.DecodeMigrationJSON[storedOwner](row["migrationJSON"])
			stored = value
			return decodeErr
		})
	if err != nil {
		return report, err
	}
	owner := stored.Explorer
	if stored.Revision == "" || owner.DraftVersion == 0 {
		return report, explorer.ErrNotFound
	}
	workspace, changed, err := authoringv2.ConvertStoredSourcePlan(owner.DraftConfig, owner.DraftDigest)
	if err != nil {
		return report, err
	}
	report.PreviousDraftVersion, report.DraftVersion, report.DraftDigest = owner.DraftVersion, owner.DraftVersion, owner.DraftDigest
	if !changed {
		return report, nil
	}
	report.Changed = true
	report.DraftVersion++
	report.DraftDigest, err = workspace.Digest()
	if err != nil || dryRun {
		return report, err
	}
	oldDigest := owner.DraftDigest
	owner.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		return report, err
	}
	owner.DraftDigest = report.DraftDigest
	owner.LastAuthoringCommandID, owner.LastAuthoringCommandDigest, owner.LastAuthoringCommandResults = "", "", nil
	updated, err := adapter.saveDraft(ctx, owner, owner.DraftVersion, []string{oldDigest}, &draftMigrationWrite{revision: stored.Revision})
	if err != nil {
		return report, err
	}
	if updated.DraftVersion != report.DraftVersion || updated.DraftDigest != report.DraftDigest {
		return report, fmt.Errorf("saved draft differs from migrated draft")
	}
	return report, nil
}
