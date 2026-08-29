package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type explorerConfigReadAuthorizer func(context.Context, *authscope.Principal, string) error

type explorerHTTPHandlers struct {
	publishRepositoryConfig fiber.Handler
	lifecycle               *explorerLifecycleHandlers
	authoring               *explorerAuthoringHandlers
	authorizer              authscope.Authorizer
	authorizeRead           explorerConfigReadAuthorizer
	explorers               *explorer.Service
	materialize             graphresolver.ExplorerBundleMaterializer
	lifecycleConfig         ExplorerV2LifecycleConfig
	application             *lifecycle.Service
}

// newExplorerHTTPHandlers wires transport adapters to the single Explorer
// application service. HTTP response conversion and deployment adapters stay
// in server; lifecycle policy does not.
func newExplorerHTTPHandlers(authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, materialize graphresolver.ExplorerBundleMaterializer, configs ...ExplorerV2LifecycleConfig) *explorerHTTPHandlers {
	handlers := &explorerHTTPHandlers{authorizer: authorizer, authorizeRead: authorizeRead, explorers: explorers, materialize: materialize}
	config := ExplorerV2LifecycleConfig{Materialize: materialize}
	if len(configs) > 0 {
		config = configs[0]
		if config.Materialize == nil {
			config.Materialize = materialize
		}
	}
	handlers.lifecycleConfig = config
	handlers.application = newExplorerLifecycleApplication(explorers, config)
	if authorizer == nil || authorizeRead == nil || explorers == nil || handlers.application == nil {
		return handlers
	}
	handlers.publishRepositoryConfig = func(c fiber.Ctx) error {
		project, generation := explorerProjectParam(c), strings.TrimSpace(c.Params("generation"))
		if project == "" || generation == "" {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "project and generation are required"})
		}
		path := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
		if err := authorizer.AuthorizeWrite(c.Context(), principalFromFiber(c), project, path); err != nil {
			return c.Status(http.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}
		workspace, err := authoringv2.DecodeWorkspace(c.Body())
		if err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		if err := workspace.ValidateForPublication(); err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		commit := strings.TrimSpace(c.Get("X-Loom-Source-Commit"))
		if commit == "" {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "X-Loom-Source-Commit is required"})
		}
		value, err := handlers.application.PublishRepository(c.Context(), lifecycle.RepositoryPublishRequest{Project: project, Generation: generation, Workspace: workspace, Commit: commit, Actor: subjectFromFiber(c)})
		if err != nil {
			status := http.StatusInternalServerError
			var applicationErr *lifecycle.Error
			if errors.As(err, &applicationErr) {
				status = lifecycleErrorStatus(applicationErr.Class)
			}
			return c.Status(status).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{"project": project, "generation": generation, "explorerId": "default", "receiptId": value.Receipt.ID, "revisionId": value.Revision.ID, "executionId": value.Execution.ID, "recipe": value.Receipt.Bundle.Name, "translationVersion": value.Receipt.Bundle.TranslationVersion, "activated": true})
	}
	handlers.lifecycle = newExplorerLifecycleHandlers(authorizer, authorizeRead, explorers, config)
	handlers.authoring = newExplorerAuthoringHandlers(authorizer, authorizeRead, explorers, config)
	return handlers
}
