package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

// RegisterExplorerLifecycleRoutes exposes only collection summaries, creation,
// and the active runtime projection. All browser editing uses authoring/v2.
func RegisterExplorerLifecycleRoutes(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil {
		return
	}
	app.Get("/api/v1/projects/:project/explorers", func(c fiber.Ctx) error {
		project := explorerProjectParam(c)
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		values, err := explorers.List(c.Context(), project)
		if err != nil {
			return explorerV2Error(c, 500, "EXPLORER_READ_FAILED", err.Error())
		}
		summaries := make([]explorer.ExplorerSummaryV1, 0, len(values))
		for _, value := range values {
			summaries = append(summaries, explorer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, ActiveRevisionID: value.ActiveRevisionID, UpdatedAt: value.UpdatedAt})
		}
		return c.JSON(summaries)
	})

	app.Get("/api/v1/projects/:project/explorers/:explorerId", func(c fiber.Ctx) error {
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		state, err := loadExplorerRuntimeState(c.Context(), explorers, project, id)
		if errors.Is(err, explorer.ErrNotFound) {
			return explorerV2Error(c, 404, "EXPLORER_NOT_FOUND", "Explorer not found")
		}
		if err != nil {
			return explorerV2Error(c, 500, "EXPLORER_READ_FAILED", err.Error())
		}
		return c.JSON(state)
	})

	app.Post("/api/v1/projects/:project/explorers", func(c fiber.Ctx) error {
		project := explorerProjectParam(c)
		if err := authorizeExplorerWrite(c, authorizer, project); err != nil {
			return err
		}
		var request struct {
			Name  string `json:"name"`
			Title string `json:"title,omitempty"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.Name) == "" {
			return explorerV2Error(c, 400, "MALFORMED_REQUEST", "name is required")
		}
		id := explorer.StableExplorerID(request.Name)
		if id == "default" {
			return explorerV2Error(c, 409, "EXPLORER_EXISTS", "the repository default already exists")
		}
		title := strings.TrimSpace(request.Title)
		if title == "" {
			title = strings.TrimSpace(request.Name)
		}
		value, err := explorers.CreateEmptyInteractive(c.Context(), project, id, title, subjectFromFiber(c))
		if errors.Is(err, explorer.ErrDraftConflict) {
			return explorerV2Error(c, 409, "EXPLORER_EXISTS", "an Explorer with this name already exists")
		}
		if err != nil {
			return explorerV2Error(c, 422, "INVALID_EXPLORER", err.Error())
		}
		return c.Status(http.StatusCreated).JSON(explorer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, UpdatedAt: value.UpdatedAt})
	})
}

// loadExplorerRuntimeState reads the Explorer identity and its immutable active
// revision directly. The selected runtime endpoint must not pass through the
// retired ExplorerConfigV2 reconciliation state: the revision is the sole
// source of published configuration, materializations, and diagnostics.
func loadExplorerRuntimeState(ctx context.Context, service *explorer.Service, project, id string) (explorer.ExplorerStateV1, error) {
	project = projectid.Canonical(project)
	id = strings.TrimSpace(id)
	owner, err := service.Get(ctx, project, id)
	if errors.Is(err, explorer.ErrNotFound) && id == "default" {
		// Repository bootstrap can briefly expose its repository record before
		// the Explorer identity is created. Preserve the read contract for that
		// window without manufacturing editable authoring state.
		repository, repositoryErr := service.RepositoryConfig(ctx, project)
		if repositoryErr != nil {
			return explorer.ExplorerStateV1{}, err
		}
		owner = &explorer.Explorer{
			Project:          projectid.Legacy(project),
			ExplorerID:       "default",
			Title:            configV2Title(repository.Config),
			ManagementMode:   explorer.ManagementRepository,
			ActiveRevisionID: repository.ActiveRevisionID,
			Publication:      repository.Publication,
			UpdatedAt:        repository.UpdatedAt,
		}
		err = nil
	}
	if err != nil {
		return explorer.ExplorerStateV1{}, err
	}
	if owner == nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("Explorer %s/%s resolved to an empty identity", project, id)
	}

	state := explorerStateV1FromIdentity(owner)
	if owner.ActiveRevisionID == "" {
		return state, nil
	}
	revision, err := service.Revision(ctx, owner.ActiveRevisionID)
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("load active Explorer revision %q: %w", owner.ActiveRevisionID, err)
	}
	state.Active.RevisionID = revision.ID
	state.Active.IntentDigest = revision.IntentDigest
	state.Active.Status = string(revision.Status)
	state.Generated.RecipeDigest = revision.RecipeDigest
	state.Generated.ResolvedSchemaDigest = revision.ResolvedSchemaDigest
	state.Generated.SourceGeneration = revision.SourceGeneration
	state.Generated.EmittedColumns = append([]explorer.EmittedColumn(nil), revision.EmittedColumns...)
	state.Generated.Materializations, state.Generated.Dataset = explorer.WithDataframeSelectors(revision.Recipe, revision.Materializations, revision.Dataset)
	state.Generated.Publication = revision.Publication
	state.Generated.Publication.State = string(revision.Status)
	state.Generated.Publication.RevisionID = revision.ID
	state.Generated.Diagnostics = append([]explorer.Diagnostic(nil), revision.Diagnostics...)
	state.Runtime = runtimeV1FromPublishedRevision(revision)
	return state, nil
}

func explorerStateV1FromIdentity(owner *explorer.Explorer) explorer.ExplorerStateV1 {
	project := projectid.Canonical(owner.Project)
	return explorer.ExplorerStateV1{
		APIVersion: explorer.ExplorerStateV1APIVersion,
		Kind:       explorer.ExplorerStateV1Kind,
		Project:    project,
		ExplorerID: owner.ExplorerID,
		Title:      owner.Title,
		Management: owner.ManagementMode,
		Draft:      explorer.ExplorerStateV1Draft{},
		Active:     explorer.ExplorerStateV1Active{},
		Generated: explorer.ExplorerStateV1Generated{
			RecipeDigest:         owner.RecipeDigest,
			ResolvedSchemaDigest: owner.ResolvedSchemaDigest,
			SourceGeneration:     owner.SourceGeneration,
			EmittedColumns:       append([]explorer.EmittedColumn(nil), owner.EmittedColumns...),
			Materializations:     append([]explorer.Materialization(nil), owner.Materializations...),
			Dataset:              owner.Dataset,
			Publication:          owner.Publication,
			Diagnostics:          append([]explorer.Diagnostic(nil), owner.Diagnostics...),
		},
		ActiveURL: explorerURL(project, owner.ExplorerID),
		UpdatedBy: owner.UpdatedBy,
		UpdatedAt: owner.UpdatedAt,
	}
}
