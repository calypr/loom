package arango

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	projectCatalogSettingsReadAQL   = `FOR d IN @@c FILTER d._key == @key RETURN d`
	projectCatalogSettingsInsertAQL = `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`
	projectCatalogSettingsCASAQL    = `FOR d IN @@c
FILTER d._key == @key AND d._rev == @expectedRev
UPDATE d WITH @patch IN @@c OPTIONS { ignoreRevs: false, mergeObjects: false }
RETURN NEW`
)

type projectCatalogSettingsRow struct {
	value explorer.ProjectCatalogSettings
	doc   map[string]any
}

func projectCatalogSettingsKey(project string) string {
	return "view_builder_" + key(project)
}

func emptyProjectCatalogSettings(project string) explorer.ProjectCatalogSettings {
	return explorer.ProjectCatalogSettings{Project: project, Catalogs: map[string]string{}}
}

func (s *Store) GetProjectCatalogSettings(ctx context.Context, project string) (explorer.ProjectCatalogSettings, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return explorer.ProjectCatalogSettings{}, fmt.Errorf("view builder project is required")
	}
	project = canonicalProjectConfigProject(project)
	row, err := readProjectCatalogSettings(ctx, s.client, project)
	if err != nil {
		return explorer.ProjectCatalogSettings{}, err
	}
	if row == nil {
		return emptyProjectCatalogSettings(project), nil
	}
	return cloneProjectCatalogSettings(row.value), nil
}

func normalizeProjectConfigConflict(err error) error {
	if err == nil || errors.Is(err, explorer.ErrDraftConflict) || !shared.IsConflict(err) {
		return err
	}
	return fmt.Errorf("view builder state changed concurrently: %w: %v", explorer.ErrDraftConflict, err)
}

func readProjectCatalogSettings(ctx context.Context, tx store.RowQueryer, project string) (*projectCatalogSettingsRow, error) {
	var found *projectCatalogSettingsRow
	err := tx.QueryRows(ctx, projectCatalogSettingsReadAQL, 1, map[string]any{
		"@c": ProjectCatalogSettingsCollection, "key": projectCatalogSettingsKey(project),
	}, func(doc map[string]any) error {
		if found != nil {
			return fmt.Errorf("duplicate view builder settings for project %q", project)
		}
		value, err := decode[explorer.ProjectCatalogSettings](doc)
		if err != nil {
			return err
		}
		if value.Project != project || stringValue(doc["_key"]) != projectCatalogSettingsKey(project) || value.Version < 1 {
			return fmt.Errorf("stored view builder settings identity is invalid")
		}
		if value.Catalogs == nil {
			value.Catalogs = map[string]string{}
		}
		found = &projectCatalogSettingsRow{value: value, doc: doc}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read view builder settings for %q: %w", project, err)
	}
	return found, nil
}

func cloneProjectCatalogSettings(value explorer.ProjectCatalogSettings) explorer.ProjectCatalogSettings {
	clone := value
	clone.Catalogs = make(map[string]string, len(value.Catalogs))
	for recipeName, revisionID := range value.Catalogs {
		clone.Catalogs[recipeName] = revisionID
	}
	return clone
}

func writeProjectCatalogSettings(ctx context.Context, tx store.RowQueryer, current *projectCatalogSettingsRow, value explorer.ProjectCatalogSettings) error {
	if value.Project == "" || value.Version < 1 {
		return fmt.Errorf("view builder settings identity is incomplete")
	}
	if value.Catalogs == nil {
		value.Catalogs = map[string]string{}
	}
	key := projectCatalogSettingsKey(value.Project)
	doc, err := document(value, key)
	if err != nil {
		return fmt.Errorf("encode view builder settings: %w", err)
	}
	doc["updatedAt"] = time.Now().UTC()
	if current == nil {
		var inserted bool
		err := tx.QueryRows(ctx, projectCatalogSettingsInsertAQL, 1, map[string]any{
			"@c": ProjectCatalogSettingsCollection, "doc": doc,
		}, func(map[string]any) error { inserted = true; return nil })
		if err != nil {
			return fmt.Errorf("insert view builder settings: %w", err)
		}
		if !inserted {
			return explorer.ErrDraftConflict
		}
		return nil
	}
	expectedRev := stringValue(current.doc["_rev"])
	if expectedRev == "" {
		return fmt.Errorf("view builder settings have no Arango revision guard")
	}
	patch := map[string]any{
		"project": value.Project, "version": value.Version,
		"defaultConfigId": value.DefaultConfigID, "defaultFrameId": value.DefaultFrameID,
		"catalogs": value.Catalogs, "updatedAt": time.Now().UTC(),
	}
	var updated bool
	err = tx.QueryRows(ctx, projectCatalogSettingsCASAQL, 1, map[string]any{
		"@c": ProjectCatalogSettingsCollection, "key": key, "expectedRev": expectedRev, "patch": patch,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil {
		return fmt.Errorf("update view builder settings: %w", err)
	}
	if !updated {
		return explorer.ErrDraftConflict
	}
	return nil
}

func canonicalProjectConfigProject(project string) string {
	// Project-config Explorer records use this normalization already; keeping
	// settings on the same canonical key lets both aggregates share one scope.
	return projectid.Legacy(project)
}
