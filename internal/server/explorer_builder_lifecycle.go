package server

import (
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type explorerLifecycleHandlers struct {
	list   fiber.Handler
	get    fiber.Handler
	create fiber.Handler
}

// newExplorerLifecycleHandlers exposes only collection summaries, creation,
// and the active runtime projection. Generated OpenAPI routing owns paths.
func newExplorerLifecycleHandlers(authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) *explorerLifecycleHandlers {
	handlers := &explorerLifecycleHandlers{}
	if authorizer == nil || authorizeRead == nil || explorers == nil {
		return handlers
	}
	application := newExplorerLifecycleApplication(explorers, capabilities)
	handlers.list = func(c fiber.Ctx) error {
		project := explorerProjectParam(c)
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		value, err := application.List(c.Context(), project)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(value.Summaries)
	}

	handlers.get = func(c fiber.Ctx) error {
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		state, err := application.Get(c.Context(), project, id)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(state)
	}

	handlers.create = func(c fiber.Ctx) error {
		project := explorerProjectParam(c)
		if err := authorizeExplorerWrite(c, authorizer, project); err != nil {
			return err
		}
		var request struct {
			Name             string `json:"name"`
			Title            string `json:"title,omitempty"`
			SourceExplorerID string `json:"sourceExplorerId,omitempty"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.Name) == "" {
			return explorerV2Error(c, 400, "MALFORMED_REQUEST", "name is required")
		}
		value, err := application.Create(c.Context(), lifecycle.CreateRequest{Project: project, Name: request.Name, Title: request.Title, SourceExplorerID: request.SourceExplorerID, Actor: subjectFromFiber(c)})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.Status(http.StatusCreated).JSON(value)
	}
	return handlers
}
