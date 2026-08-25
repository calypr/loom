package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

// RegisterExplorerConfigV2Route is the sole repository Explorer deployment
// surface. The body is the portable, executable baseline ExplorerConfigV2
// document; generation and source commit are deployment context, while live
// schema/readiness and publication metadata are derived by Loom.
type explorerConfigReadAuthorizer func(context.Context, *authscope.Principal, string) error

func RegisterExplorerConfigV2Route(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, materialize graphresolver.ExplorerBundleMaterializer, lifecycle ...ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil || materialize == nil {
		return
	}
	app.Post("/api/v1/projects/:project/generations/:generation/explorer-config", func(c fiber.Ctx) error {
		project, generation := explorerProjectParam(c), strings.TrimSpace(c.Params("generation"))
		if project == "" || generation == "" {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "project and generation are required"})
		}
		// The dataset key (for example HTAN_INT-BForePC) is not an Arborist
		// resource path. Reuse the scope supplied during the deferred upload.
		authResourcePath := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
		principal, _ := c.Locals("principal").(*authscope.Principal)
		if err := authorizer.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
			return c.Status(http.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}
		_, bundle, err := explorer.DecodeConfigV2(c.Body(), project)
		if err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		// Version the execution by the checked-out repository commit while
		// retaining a data-only recipe body. A project-specific bundle name
		// prevents two repository configs from sharing a publication namespace.
		commit := strings.TrimSpace(c.Get("X-Loom-Source-Commit"))
		if commit == "" {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "X-Loom-Source-Commit is required"})
		}
		// Recipe names are storage/execution identifiers, not public project
		// URLs. Keep the historical hyphenated form so a canonical slash in the
		// route never becomes an invalid name containing '/'.
		bundle.Name = "explorer_" + projectid.Legacy(project) + "_default"
		bundle.TranslationVersion = "repository-" + commit
		if err := bundle.Validate(); err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": fmt.Sprintf("invalid recipe: %v", err)})
		}
		_, _, canonicalConfig, configDigest, err := explorer.CanonicalConfigV2(c.Body(), project, "default", "repository")
		if err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		execution, err := materialize(c.Context(), bundle, recipe.RuntimeBindings{Project: projectid.Legacy(project), DatasetGeneration: generation})
		if err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": fmt.Sprintf("materialize ExplorerConfigV2: %v", err)})
		}
		if err := verifyQueryableOutputs(bundle, execution); err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error(), "executionId": execution.ID})
		}
		materializations := explorerMaterializations(bundle, execution)
		dataset := datasetMetadataFromExecution(bundle, generation, execution.ResolvedSchemaDigest, execution)
		publication := explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: generation, ExecutionID: execution.ID, UpdatedAt: time.Now().UTC()}
		owner, revision, err := explorers.UpsertRepositoryV2(c.Context(), canonicalConfig, commit, generation, "", explorer.Compilation{Bundle: bundle, RecipeDigest: mustBundleDigest(bundle)}, execution.ResolvedSchemaDigest, materializations, dataset, publication)
		if err != nil {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("persist Explorer lifecycle V2: %v", err)})
		}
		if owner.ManagementMode != explorer.ManagementRepository {
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": "default Explorer has invalid management mode"})
		}
		if len(lifecycle) == 0 || lifecycle[0].ActivateRelease == nil {
			return c.Status(http.StatusServiceUnavailable).JSON(fiber.Map{"error": "release activation is not configured", "executionId": execution.ID})
		}
		if err := lifecycle[0].ActivateRelease(c.Context(), projectid.Legacy(project), generation, selectorsForBundle(bundle)); err != nil {
			_, _ = explorers.FailRevision(c.Context(), revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("activate published ExplorerConfigV2: %v", err), "executionId": execution.ID})
		}
		if err := explorers.ActivateRepositoryGeneration(c.Context(), project, generation, revision.ID); err != nil {
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("activate ExplorerConfigV2: %v", err), "executionId": execution.ID})
		}
		publication.State = string(explorer.RevisionActive)
		publication.RevisionID = revision.ID
		publication.UpdatedAt = time.Now().UTC()
		if _, err := explorers.SaveRepositoryConfig(c.Context(), explorer.RepositoryConfig{Project: project, Config: canonicalConfig, ConfigDigest: configDigest, ActiveRevisionID: revision.ID, DraftVersion: owner.DraftVersion, SourceGeneration: generation, SourceCommit: commit, ExecutionID: execution.ID, Materializations: materializations, Dataset: dataset, Publication: publication}); err != nil {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("persist ExplorerConfigV2: %v", err)})
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{"project": project, "generation": generation, "explorerId": "default", "executionId": execution.ID, "recipe": bundle.Name, "translationVersion": bundle.TranslationVersion, "activated": true})
	})
	app.Get("/api/v1/projects/:project/explorer-config", func(c fiber.Ctx) error {
		project := explorerProjectParam(c)
		principal, _ := c.Locals("principal").(*authscope.Principal)
		if err := authorizeRead(c.Context(), principal, project); err != nil {
			return c.Status(http.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}
		value, err := explorers.RepositoryConfig(c.Context(), project)
		if err == explorer.ErrNotFound {
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "repository ExplorerConfigV2 not found"})
		}
		if err != nil {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(value)
	})
	if len(lifecycle) == 0 {
		lifecycle = []ExplorerV2LifecycleConfig{{Materialize: materialize}}
	}
	RegisterExplorerLifecycleRoutes(app, authorizer, authorizeRead, explorers, lifecycle[0])
	RegisterExplorerAuthoringV2Routes(app, authorizer, authorizeRead, explorers, lifecycle[0])
}

func explorerMaterializations(bundle recipe.Bundle, execution graphresolver.RecipeExecution) []explorer.Materialization {
	out := make([]explorer.Materialization, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		out = append(out, explorer.Materialization{OutputID: output.Name, Output: output.Name, MaterializationID: execution.ID, Selector: explorerOutputSelector(bundle, output.Name), Columns: output.Columns})
	}
	return out
}

func datasetMetadataFromExecution(bundle recipe.Bundle, generation, schemaDigest string, execution graphresolver.RecipeExecution) explorer.DatasetMetadata {
	outputs := make([]explorer.DatasetOutput, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		state := strings.ToUpper(output.State)
		outputs = append(outputs, explorer.DatasetOutput{
			Name: output.Name, State: state,
			Queryable: state == "PUBLISHED" || state == "READY" || state == "ACTIVE",
			Selector:  explorerOutputSelector(bundle, output.Name),
			Columns:   output.Columns,
		})
	}
	return explorer.DatasetMetadata{Generation: generation, SchemaDigest: schemaDigest, Outputs: outputs}
}

func explorerOutputSelector(bundle recipe.Bundle, output string) *dataset.DataframeSelector {
	selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output}
	return &selector
}
