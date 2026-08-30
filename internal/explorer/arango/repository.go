package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

// legacyRepositoryConfig contains only the fields needed to recover the
// canonical default Explorer owner. Immutable generated state already lives in
// the referenced revision and is deliberately not decoded or copied here.
type legacyRepositoryConfig struct {
	Project          string          `json:"project"`
	Workspace        json.RawMessage `json:"workspace"`
	IntentDigest     string          `json:"intentDigest"`
	ActiveRevisionID string          `json:"activeRevisionId"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

// MigrateLegacyRepositoryConfigs is an idempotent startup migration. It
// recovers only missing repository owners and never overwrites a current owner
// or deletes the legacy source document.
func (s *Store) MigrateLegacyRepositoryConfigs(ctx context.Context) error {
	legacy := []legacyRepositoryConfig{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.activeRevisionId != null AND d.activeRevisionId != "" RETURN d`, 1000, map[string]any{"@c": LegacyRepositoryConfigsCollection}, func(row map[string]any) error {
		value, decodeErr := decode[legacyRepositoryConfig](row)
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
		if len(draft) == 0 {
			draft = revision.AuthoringBundle
		}
		title := "Default"
		if workspace, decodeErr := authoringv2.DecodeWorkspace(draft); decodeErr == nil && strings.TrimSpace(workspace.Explorer.Title) != "" {
			title = workspace.Explorer.Title
		}
		digest := value.IntentDigest
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
