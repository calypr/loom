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
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

// RegisterExplorerConfigV2Route is the sole repository Explorer deployment
// surface. The body is the portable V2 authoring workspace. Repository and
// browser publication deliberately share the same compiler receipt pipeline.
type explorerConfigReadAuthorizer func(context.Context, *authscope.Principal, string) error

func RegisterExplorerConfigV2Route(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, materialize graphresolver.ExplorerBundleMaterializer, lifecycle ...ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil {
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
		if len(lifecycle) == 0 || lifecycle[0].Capability == nil || lifecycle[0].AuthorizedCapabilityCompile == nil || lifecycle[0].CompileReceipt == nil {
			return c.Status(http.StatusServiceUnavailable).JSON(fiber.Map{"error": "repository V2 compilation is not configured"})
		}
		config := lifecycle[0]
		snapshot, err := config.Capability(c.Context(), project, "default", generation)
		if err != nil || !snapshot.Usable() || snapshot.Identity.Generation != generation {
			if err == nil {
				err = fmt.Errorf("capability generation %q does not match deployment generation %q", snapshot.Identity.Generation, generation)
			}
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("resolve repository V2 capability: %v", err)})
		}
		authorized, err := config.AuthorizedCapabilityCompile(c.Context(), project, snapshot.Token)
		if err != nil {
			return c.Status(http.StatusForbidden).JSON(fiber.Map{"error": fmt.Sprintf("authorize repository V2 compilation: %v", err)})
		}
		receipt, err := config.CompileReceipt(c.Context(), ExplorerV2ReceiptCompileRequest{Project: project, ExplorerID: "default", Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: requestIDFromFiber(c), Authorized: authorized})
		if err != nil || receipt == nil {
			if err == nil {
				err = fmt.Errorf("compiler returned no receipt")
			}
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": fmt.Sprintf("compile repository V2 workspace: %v", err)})
		}
		if receipt.SourceGeneration != generation {
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": "compiled receipt generation does not match deployment generation"})
		}
		if config.AuthorizedCapabilityExecution != nil {
			authorized, err = config.AuthorizedCapabilityExecution(c.Context(), project, receipt.SnapshotToken)
			if err != nil {
				return c.Status(http.StatusForbidden).JSON(fiber.Map{"error": fmt.Sprintf("authorize repository V2 materialization: %v", err)})
			}
		}
		bindings := recipe.RuntimeBindings{Project: projectid.Legacy(project), DatasetGeneration: generation}
		applyAuthorizedScope(&bindings, authorized, true)
		var execution graphresolver.RecipeExecution
		if config.MaterializeReceipt != nil {
			execution, err = config.MaterializeReceipt(c.Context(), receipt, bindings)
		} else if materialize != nil {
			execution, err = materialize(c.Context(), receipt.Bundle, bindings)
		} else {
			err = fmt.Errorf("repository V2 materialization is not configured")
		}
		if err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": fmt.Sprintf("materialize repository V2 workspace: %v", err)})
		}
		if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
			return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error(), "executionId": execution.ID})
		}
		materializations := explorerMaterializations(receipt.Bundle, execution)
		dataset := datasetMetadataFromExecution(receipt.Bundle, generation, receipt.ResolvedSchemaDigest, execution)
		publication := explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: generation, ExecutionID: execution.ID, UpdatedAt: time.Now().UTC()}
		owner, revision, err := explorers.UpsertRepositoryV2(c.Context(), *receipt, commit, subjectFromFiber(c), materializations, dataset, publication)
		if err != nil {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("persist Explorer lifecycle V2: %v", err)})
		}
		if owner.ManagementMode != explorer.ManagementRepository {
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": "default Explorer has invalid management mode"})
		}
		if config.ActivateRelease == nil {
			return c.Status(http.StatusServiceUnavailable).JSON(fiber.Map{"error": "release activation is not configured", "executionId": execution.ID})
		}
		if err := config.ActivateRelease(c.Context(), projectid.Legacy(project), generation, selectorsForBundle(receipt.Bundle)); err != nil {
			_, _ = explorers.FailRevision(c.Context(), revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("activate published ExplorerConfigV2: %v", err), "executionId": execution.ID})
		}
		if err := explorers.ActivateRepositoryGeneration(c.Context(), project, generation, revision.ID); err != nil {
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("activate ExplorerConfigV2: %v", err), "executionId": execution.ID})
		}
		publication.State = string(explorer.RevisionActive)
		publication.RevisionID = revision.ID
		publication.UpdatedAt = time.Now().UTC()
		if _, err := explorers.SaveRepositoryConfig(c.Context(), explorer.RepositoryConfig{Project: project, Config: append([]byte(nil), receipt.CompiledConfig...), Workspace: append([]byte(nil), receipt.NormalizedBundle...), ConfigDigest: receipt.IntentDigest, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: append([]byte(nil), receipt.PublicOutputContract...), ActiveRevisionID: revision.ID, DraftVersion: owner.DraftVersion, SourceGeneration: generation, SourceCommit: commit, ExecutionID: execution.ID, Materializations: materializations, Dataset: dataset, Publication: publication}); err != nil {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("persist ExplorerConfigV2: %v", err)})
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{"project": project, "generation": generation, "explorerId": "default", "receiptId": receipt.ID, "revisionId": revision.ID, "executionId": execution.ID, "recipe": receipt.Bundle.Name, "translationVersion": receipt.Bundle.TranslationVersion, "activated": true})
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
