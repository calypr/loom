package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

type legacyRepositoryConfig struct {
	Project          string          `json:"project"`
	Workspace        json.RawMessage `json:"workspace"`
	IntentDigest     string          `json:"intentDigest"`
	ActiveRevisionID string          `json:"activeRevisionId"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

func (s *Store) MigrateLegacyRepositoryConfigs(ctx context.Context) error {
	legacy := []legacyRepositoryConfig{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.activeRevisionId != null AND d.activeRevisionId != "" RETURN d`, 1000, map[string]any{"@c": LegacyRepositoryConfigsCollection}, func(row map[string]any) error {
		value, decodeErr := db.Decode[legacyRepositoryConfig](row)
		if decodeErr == nil {
			legacy = append(legacy, value)
		}
		return decodeErr
	})
	if err != nil {
		return fmt.Errorf("read legacy Explorer repository configs: %w", err)
	}
	for _, value := range legacy {
		project := projectid.Legacy(value.Project)
		if _, err := s.Get(ctx, project, "default"); err == nil {
			continue
		} else if !errors.Is(err, explorer.ErrNotFound) {
			return err
		}
		revision, err := s.GetRevision(ctx, value.ActiveRevisionID)
		if err != nil {
			return fmt.Errorf("migrate legacy Explorer %q revision %q: %w", project, value.ActiveRevisionID, err)
		}
		draft := value.Workspace
		digest := value.IntentDigest
		if len(draft) == 0 {
			draft = revision.AuthoringWorkspaceBytes()
			if len(revision.AuthoringBundle) == 0 {
				object, _ := workspacedb.AuditObject(revision.Config)
				if !workspacedb.AuditWorkspaceShape(object) {
					draft = nil
				}
			}
			if len(draft) != 0 {
				domain, err := explorer.NewService(s)
				if err != nil {
					return err
				}
				workspace, err := domain.RevisionWorkspace(ctx, *revision)
				if err != nil {
					return fmt.Errorf("legacy repository authoring state requires explicit workspace migration: %w", err)
				}
				draft, err = workspace.CanonicalJSON()
				if err != nil {
					return err
				}
				digest, err = workspace.Digest()
				if err != nil {
					return err
				}
			}
		}
		title := "Default"
		if len(draft) != 0 {
			workspace, decodeErr := authoringv2.DecodeCurrentWorkspace(draft)
			if decodeErr != nil {
				return fmt.Errorf("legacy repository draft requires explicit workspace migration: %w", decodeErr)
			}
			if strings.TrimSpace(workspace.Explorer.Title) != "" {
				title = workspace.Explorer.Title
			}
		}
		if digest == "" {
			digest = revision.IntentDigest
		}
		updatedAt := value.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = revision.CreatedAt
		}
		_, err = s.Create(ctx, explorer.Explorer{Project: project, ExplorerID: "default", Title: title, ManagementMode: explorer.ManagementRepository, DraftConfig: append([]byte(nil), draft...), DraftVersion: 1, DraftDigest: digest, ActiveRevisionID: revision.ID, UpdatedAt: updatedAt})
		if err != nil && !errors.Is(err, explorer.ErrDraftConflict) {
			return fmt.Errorf("migrate legacy Explorer %q owner: %w", project, err)
		}
	}
	return nil
}
