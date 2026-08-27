package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
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
		state, err := explorers.LoadExplorerState(c.Context(), project, id)
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
