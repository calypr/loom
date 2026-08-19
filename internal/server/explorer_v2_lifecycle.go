package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

// ExplorerV2CompileRequest is the server-side authoring seam. Browser
// requests contain opaque candidate IDs only; the compiler resolves them from
// Loom's authoritative catalog.
type ExplorerV2CompileRequest struct {
	Project                    string
	ExplorerID                 string
	Config                     []byte
	SnapshotToken              string
	SelectedCandidateIDsByNode map[string][]string
	Output                     string
}

type ExplorerV2CompileResult struct {
	Config               []byte
	Bundle               recipe.Bundle
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	OutputFingerprints   map[string]string
	EmittedColumns       []explorer.EmittedColumn
	Diagnostics          []explorer.Diagnostic
}

type ExplorerV2Compiler func(context.Context, ExplorerV2CompileRequest) (ExplorerV2CompileResult, error)
type ExplorerV2Previewer func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (map[string][]map[string]any, error)
type ExplorerV2Materializer = graphresolver.ExplorerBundleMaterializer
type ExplorerV2GenerationValidator func(context.Context, string, string) error
type ExplorerV2ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error

// ExplorerV2LifecycleConfig contains explicit capabilities for the REST
// surface. The HTTP layer does not reach into catalog, compiler, or storage
// internals.
type ExplorerV2LifecycleConfig struct {
	Compile     ExplorerV2Compiler
	Catalog     explorerV2CatalogReader
	Preview     ExplorerV2Previewer
	Materialize ExplorerV2Materializer
	Logger      *slog.Logger
	// ValidateReleaseGeneration preflights the immutable snapshot required by
	// release activation before Publish performs expensive materialization.
	ValidateReleaseGeneration ExplorerV2GenerationValidator
	// Publish invokes ActivateRelease when the draft requires outputs that are
	// not already present in the active dataset release.
	ActivateRelease ExplorerV2ReleaseActivator
}

type explorerV2State struct {
	Project    string                  `json:"project"`
	ExplorerID string                  `json:"explorerId"`
	Management explorer.ManagementMode `json:"management"`
	// BaselineConfig is present for the repository default. It is the
	// presentation-free recipe/schema projection of the current default draft.
	BaselineConfig json.RawMessage `json:"baselineConfig,omitempty"`
	// DraftConfig is returned for custom Explorers and for the editable default.
	DraftConfig          json.RawMessage              `json:"draftConfig,omitempty"`
	DraftVersion         int64                        `json:"draftVersion"`
	DraftDigest          string                       `json:"draftDigest"`
	ActiveConfig         json.RawMessage              `json:"activeConfig,omitempty"`
	ActiveRevisionID     string                       `json:"activeRevisionId,omitempty"`
	RecipeDigest         string                       `json:"recipeDigest,omitempty"`
	ResolvedSchemaDigest string                       `json:"resolvedSchemaDigest,omitempty"`
	SourceGeneration     string                       `json:"sourceGeneration,omitempty"`
	EmittedColumns       []explorer.EmittedColumn     `json:"emittedColumns,omitempty"`
	Materializations     []explorer.Materialization   `json:"materializations,omitempty"`
	Dataset              explorer.DatasetMetadata     `json:"dataset,omitempty"`
	Publication          explorer.PublicationMetadata `json:"publication,omitempty"`
	Diagnostics          []explorer.Diagnostic        `json:"diagnostics,omitempty"`
	PublicationState     string                       `json:"publicationState,omitempty"`
	ActiveURL            string                       `json:"activeUrl"`
	UpdatedBy            string                       `json:"updatedBy,omitempty"`
	UpdatedAt            time.Time                    `json:"updatedAt"`
}

func RegisterExplorerLifecycleRoutes(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil {
		return
	}

	app.Get("/api/v1/projects/:project/explorers", func(c fiber.Ctx) error {
		project := strings.TrimSpace(c.Params("project"))
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		values, err := listExplorerV2States(c.Context(), explorers, project)
		if err != nil {
			return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error())
		}
		return c.JSON(values)
	})

	app.Get("/api/v1/projects/:project/explorers/:explorerId", func(c fiber.Ctx) error {
		project, id := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		value, err := getExplorerV2State(c.Context(), explorers, project, id)
		if errors.Is(err, explorer.ErrNotFound) {
			return explorerV2Error(c, http.StatusNotFound, "EXPLORER_NOT_FOUND", "Explorer not found")
		}
		if err != nil {
			return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error())
		}
		return c.JSON(value)
	})
	app.Get("/api/v1/projects/:project/explorers/:explorerId/authoring/catalog", func(c fiber.Ctx) error {
		project, id := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		if capabilities.Catalog == nil {
			return explorerV2Error(c, http.StatusServiceUnavailable, "CATALOG_UNAVAILABLE", "Explorer authoring catalog is not configured")
		}
		snapshot, err := capabilities.Catalog(c.Context(), project, id, "")
		if err != nil {
			return explorerV2Error(c, http.StatusServiceUnavailable, "CATALOG_UNAVAILABLE", err.Error())
		}
		nodes := make([]fiber.Map, 0, len(snapshot.Catalog.Nodes))
		for _, node := range snapshot.Catalog.Nodes {
			nodes = append(nodes, fiber.Map{"nodeId": node.ID, "label": node.ResourceType})
		}
		selections := make([]fiber.Map, 0, len(snapshot.Catalog.Selections))
		for _, selection := range snapshot.Catalog.Selections {
			selections = append(selections, fiber.Map{
				"selectionId": selection.ID, "nodeId": selection.NodeID, "fieldRef": selection.FieldRef,
				"select": selection.Select, "logicalType": selection.LogicalType,
				"filterable": selection.Filterable, "chartable": selection.Chartable,
			})
		}
		edges := make([]fiber.Map, 0, len(snapshot.Catalog.Edges))
		for _, edge := range snapshot.Catalog.Edges {
			edges = append(edges, fiber.Map{"edgeId": edge.ID, "fromNodeId": edge.FromNodeID, "toNodeId": edge.ToNodeID, "label": edge.Label})
		}
		return c.JSON(fiber.Map{
			"snapshotToken": snapshot.Token, "project": snapshot.Project, "explorerId": id,
			"sourceGeneration": snapshot.Generation, "authorizationScopeDigest": snapshot.AuthorizationScopeDigest,
			"resolvedSchemaDigest": snapshot.ResolvedSchemaDigest, "nodes": nodes,
			"selections": selections, "routeEdges": edges,
			"completeness": fiber.Map{"complete": snapshot.Complete, "truncated": snapshot.Truncated, "diagnostics": snapshot.Diagnostics},
			"diagnostics":  snapshot.Diagnostics,
		})
	})

	app.Post("/api/v1/projects/:project/explorers", func(c fiber.Ctx) error {
		project := strings.TrimSpace(c.Params("project"))
		if err := authorizeExplorerWrite(c, authorizer, project); err != nil {
			return err
		}
		var request struct {
			Name       string `json:"name"`
			Title      string `json:"title"`
			ExplorerID string `json:"explorerId"`
			Source     string `json:"source"`
			From       string `json:"from"`
			Blank      bool   `json:"blank"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil {
			return explorerV2Error(c, http.StatusBadRequest, "MALFORMED_REQUEST", err.Error())
		}
		name := strings.TrimSpace(request.Name)
		if name == "" {
			name = strings.TrimSpace(request.Title)
		}
		if name == "" {
			name = strings.TrimSpace(request.ExplorerID)
		}
		id := explorer.StableExplorerID(name)
		if id == "default" {
			return explorerV2Error(c, http.StatusConflict, "EXPLORER_EXISTS", "the repository default already exists; edit its draft")
		}
		title := strings.TrimSpace(request.Title)
		if title == "" {
			title = name
		}
		var raw []byte
		if request.Blank || strings.EqualFold(strings.TrimSpace(request.Source), "blank") || strings.EqualFold(strings.TrimSpace(request.From), "blank") {
			raw = blankExplorerConfigV2(project, id, title)
		} else {
			base, err := explorers.RepositoryConfig(c.Context(), project)
			if errors.Is(err, explorer.ErrNotFound) {
				return explorerV2Error(c, http.StatusConflict, "REPOSITORY_CONFIG_REQUIRED", "deploy the repository default or request a blank Explorer")
			}
			if err != nil {
				return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error())
			}
			raw, err = forkRepositoryConfig(base.Config, project, id, title)
			if err != nil {
				return explorerV2Error(c, http.StatusUnprocessableEntity, "INVALID_REPOSITORY_CONFIG", err.Error())
			}
		}
		value, err := explorers.CreateInteractiveV2(c.Context(), project, id, raw, subjectFromFiber(c))
		if err != nil {
			if errors.Is(err, explorer.ErrDraftConflict) {
				return explorerV2Error(c, http.StatusConflict, "EXPLORER_EXISTS", "an Explorer with this name already exists")
			}
			return explorerV2Error(c, http.StatusUnprocessableEntity, "INVALID_EXPLORER", err.Error())
		}
		state, err := getExplorerV2State(c.Context(), explorers, project, value.ExplorerID)
		if err != nil {
			return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error())
		}
		return c.Status(http.StatusCreated).JSON(state)
	})

	app.Put("/api/v1/projects/:project/explorers/:explorerId/draft", func(c fiber.Ctx) error {
		project, id := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeExplorerWrite(c, authorizer, project); err != nil {
			return err
		}
		var request struct {
			Config               json.RawMessage `json:"config"`
			ExpectedDraftVersion *int64          `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string          `json:"expectedDraftDigest"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil || request.ExpectedDraftVersion == nil {
			if err == nil {
				err = fmt.Errorf("expectedDraftVersion is required")
			}
			return explorerV2Error(c, http.StatusBadRequest, "MALFORMED_REQUEST", err.Error())
		}
		_, _, canonical, _, err := explorer.CanonicalConfigV2(request.Config, project, id, explorer.ConfigManagementForID(id))
		if err != nil {
			return explorerV2Error(c, http.StatusUnprocessableEntity, "INVALID_CONFIG", err.Error())
		}
		value, err := explorers.SaveDraftV2(c.Context(), project, id, canonical, *request.ExpectedDraftVersion, request.ExpectedDraftDigest, subjectFromFiber(c))
		if errors.Is(err, explorer.ErrDraftConflict) {
			return draftConflictResponse(c, explorers, project, id)
		}
		if errors.Is(err, explorer.ErrNotFound) {
			return explorerV2Error(c, http.StatusNotFound, "EXPLORER_NOT_FOUND", "Explorer not found")
		}
		if err != nil {
			return explorerV2Error(c, http.StatusUnprocessableEntity, "DRAFT_SAVE_FAILED", err.Error())
		}
		return c.JSON(stateFromExplorer(value))
	})

	app.Post("/api/v1/projects/:project/explorers/:explorerId/authoring/compile", func(c fiber.Ctx) error {
		project, id := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeExplorerWrite(c, authorizer, project); err != nil {
			return err
		}
		var request struct {
			Output                     string              `json:"output"`
			Config                     json.RawMessage     `json:"config"`
			SnapshotToken              string              `json:"snapshotToken"`
			SelectedCandidateIDsByNode map[string][]string `json:"selectedCandidateIdsByNode"`
			ExpectedDraftVersion       *int64              `json:"expectedDraftVersion"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil {
			return explorerV2Error(c, http.StatusBadRequest, "MALFORMED_REQUEST", err.Error())
		}
		result, digest, err := compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, Config: request.Config, SnapshotToken: request.SnapshotToken, SelectedCandidateIDsByNode: request.SelectedCandidateIDsByNode, Output: request.Output})
		if err != nil {
			return explorerV2Error(c, http.StatusUnprocessableEntity, "COMPILE_FAILED", err.Error())
		}
		diagnostics := result.Diagnostics
		if diagnostics == nil {
			diagnostics = []explorer.Diagnostic{}
		}
		emittedColumns := result.EmittedColumns
		if emittedColumns == nil {
			emittedColumns = []explorer.EmittedColumn{}
		}
		return c.JSON(fiber.Map{"project": project, "explorerId": id, "config": json.RawMessage(result.Config), "draftDigest": digest, "snapshotToken": request.SnapshotToken, "recipeDigest": result.RecipeDigest, "resolvedSchemaDigest": result.ResolvedSchemaDigest, "sourceGeneration": result.SourceGeneration, "emittedColumns": emittedColumns, "diagnostics": diagnostics})
	})

	app.Post("/api/v1/projects/:project/explorers/:explorerId/preview", func(c fiber.Ctx) error {
		project, id := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		var request struct {
			Config      json.RawMessage `json:"config"`
			Output      string          `json:"output"`
			Limit       int             `json:"limit"`
			DraftDigest string          `json:"draftDigest"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil {
			return explorerV2Error(c, http.StatusBadRequest, "MALFORMED_REQUEST", err.Error())
		}
		if request.Limit == 0 {
			request.Limit = 25
		}
		if request.Limit < 1 || request.Limit > 1000 {
			return explorerV2Error(c, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 1000")
		}
		result, digest, err := compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, Config: request.Config, Output: request.Output})
		if err != nil {
			return previewFailure(c, digest, err)
		}
		if !bundleHasOutput(result.Bundle, request.Output) {
			return previewFailure(c, digest, fmt.Errorf("unsupported output %q", request.Output))
		}
		if capabilities.Preview == nil {
			return previewFailure(c, digest, fmt.Errorf("preview executor is not configured"))
		}
		rows, err := capabilities.Preview(c.Context(), result.Bundle, recipe.RuntimeBindings{Project: project, PreviewLimit: request.Limit, DatasetGeneration: result.SourceGeneration, OutputNames: []string{request.Output}})
		if err != nil {
			return previewFailure(c, digest, err)
		}
		selected := rows[request.Output]
		return c.JSON(fiber.Map{"project": project, "explorerId": id, "output": request.Output, "columns": columnsForOutput(result, request.Output), "rows": selected, "rowCount": len(selected), "digest": digest, "diagnostics": result.Diagnostics})
	})

	app.Post("/api/v1/projects/:project/explorers/:explorerId/publish", func(c fiber.Ctx) error {
		project, id := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("explorerId"))
		if err := authorizeExplorerWrite(c, authorizer, project); err != nil {
			return err
		}
		var request struct {
			ExpectedDraftVersion *int64 `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string `json:"expectedDraftDigest"`
		}
		if err := decodeStrict(c.Body(), &request); err != nil {
			return explorerV2Error(c, http.StatusBadRequest, "MALFORMED_REQUEST", err.Error())
		}
		requestID, _ := c.Locals("request_id").(string)
		publishInfo := func(event string, args ...any) {
			if capabilities.Logger == nil {
				return
			}
			base := []any{"request_id", requestID, "project", project, "explorer_id", id}
			capabilities.Logger.Info(event, append(base, args...)...)
		}
		publishFailure := func(status int, code, message, phase string, details any) error {
			if capabilities.Logger != nil {
				capabilities.Logger.Error("Explorer publish failed", "request_id", requestID, "project", project, "explorer_id", id, "phase", phase, "code", code, "details", details)
			}
			return explorerV2ErrorWithDetails(c, status, code, message, publicExplorerPublishDetails(details))
		}
		publishInfo("Explorer publish started")
		owner, err := explorers.Get(c.Context(), project, id)
		if errors.Is(err, explorer.ErrNotFound) {
			return explorerV2Error(c, http.StatusNotFound, "EXPLORER_NOT_FOUND", "Explorer not found")
		}
		if err != nil {
			return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error())
		}
		result, digest, err := compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, Config: owner.DraftConfig})
		if err != nil {
			// A persisted canonical draft should compile. Retry against the
			// latest draft once so a concurrent last-write-wins save or a
			// transient catalog update does not become a user-facing 422.
			if latest, readErr := explorers.Get(c.Context(), project, id); readErr == nil {
				owner = latest
				result, digest, err = compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, Config: owner.DraftConfig})
			}
			if err != nil {
				return publishFailure(http.StatusInternalServerError, "PUBLISH_PIPELINE_FAILED", "the current Explorer draft could not be compiled", "COMPILE", fiber.Map{"draftVersion": owner.DraftVersion, "draftDigest": owner.DraftDigest, "cause": err.Error()})
			}
		}
		publishInfo("Explorer publish compiled", "recipe_digest", result.RecipeDigest, "resolved_schema_digest", result.ResolvedSchemaDigest, "source_generation", result.SourceGeneration, "output_count", len(result.Bundle.Outputs))
		// Publish is the single user-facing visibility operation. Reuse each
		// unchanged output and materialize only outputs whose compiled fingerprint
		// is absent or different from the active dataset metadata.
		repository, repositoryErr := explorers.RepositoryConfig(c.Context(), project)
		var activeRelease activeExplorerDataset
		var activeErr error
		if repositoryErr != nil || repository == nil {
			activeErr = &activeExplorerDatasetError{message: fmt.Sprintf("active repository dataset release is unavailable for project %q", project), details: activeDatasetCompatibility{MissingOutputs: missingDatasetOutputs(result.Bundle, "ACTIVE_RELEASE_UNAVAILABLE")}}
		} else {
			activeRelease = activeExplorerDatasetFromRepository(*repository)
			if activeRelease.Generation == "" {
				activeErr = &activeExplorerDatasetError{message: fmt.Sprintf("active repository dataset release for project %q has no generation", project), details: activeDatasetCompatibility{MissingOutputs: missingDatasetOutputs(result.Bundle, "ACTIVE_RELEASE_UNAVAILABLE")}}
			} else if state := strings.ToUpper(strings.TrimSpace(activeRelease.Publication.State)); state != "" && state != "ACTIVE" && state != "READY" {
				activeErr = &activeExplorerDatasetError{message: fmt.Sprintf("active repository dataset release for project %q is not active", project), details: activeDatasetCompatibility{MissingOutputs: missingDatasetOutputs(result.Bundle, "ACTIVE_RELEASE_UNAVAILABLE")}}
			}
		}
		// Compilation, fingerprints, materialization, and release activation must
		// all use one generation. Repository metadata describes the prior release
		// and is only a reuse candidate; it must never override the generation the
		// compiler resolved from the active catalog.
		generation := strings.TrimSpace(result.SourceGeneration)
		if generation == "" {
			return publishFailure(http.StatusServiceUnavailable, "DATASET_BUILD_UNAVAILABLE", "publishing this Explorer requires a dataset generation", "RESOLVE_GENERATION", activeDatasetErrorDetails(activeErr, result.Bundle))
		}
		if repositoryErr == nil && repository != nil && strings.TrimSpace(repository.SourceGeneration) != "" && strings.TrimSpace(repository.SourceGeneration) != generation {
			publishInfo("Explorer dataset generation advanced", "previous_generation", strings.TrimSpace(repository.SourceGeneration), "generation", generation)
		}
		changedOutputs := result.Bundle.Outputs
		if activeErr == nil {
			changedOutputs = outputsNeedingMaterialization(result.Bundle, activeRelease, generation, result.OutputFingerprints)
		}
		publishInfo("Explorer publish output plan", "generation", generation, "changed_outputs", recipeOutputNames(changedOutputs), "reused_output_count", len(result.Bundle.Outputs)-len(changedOutputs))
		if len(changedOutputs) > 0 {
			if capabilities.Materialize == nil || capabilities.ActivateRelease == nil {
				return publishFailure(http.StatusServiceUnavailable, "DATASET_BUILD_UNAVAILABLE", "publishing this Explorer requires a dataset build that is not configured", "MATERIALIZE_UNAVAILABLE", activeDatasetErrorDetails(activeErr, result.Bundle))
			}
			if capabilities.ValidateReleaseGeneration != nil {
				if validateErr := capabilities.ValidateReleaseGeneration(c.Context(), project, generation); validateErr != nil {
					status, code, message := explorerReleaseFailure(validateErr, true)
					return publishFailure(status, code, message, "VALIDATE_DATASET_RELEASE", fiber.Map{"generation": generation, "cause": validateErr.Error()})
				}
			}
			var executions []graphresolver.RecipeExecution
			for _, output := range changedOutputs {
				publishInfo("Explorer output materialization started", "output", output.Name)
				execution, materializeErr := capabilities.Materialize(c.Context(), result.Bundle, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, OutputNames: []string{output.Name}})
				if materializeErr != nil {
					return publishFailure(http.StatusServiceUnavailable, "DATASET_BUILD_FAILED", "the dataset could not be built for this Explorer", "MATERIALIZE", fiber.Map{"generation": generation, "output": output.Name, "cause": materializeErr.Error(), "details": activeDatasetErrorDetails(activeErr, result.Bundle)})
				}
				if verifyErr := verifyQueryableOutput(output.Name, generation, execution); verifyErr != nil {
					return publishFailure(http.StatusServiceUnavailable, "DATASET_BUILD_FAILED", "the dataset build did not produce a queryable Explorer output", "VERIFY_MATERIALIZE", fiber.Map{"generation": generation, "output": output.Name, "cause": verifyErr.Error(), "details": activeDatasetErrorDetails(activeErr, result.Bundle)})
				}
				publishInfo("Explorer output materialization complete", "output", output.Name, "execution_id", execution.ID)
				executions = append(executions, execution)
			}
			publishInfo("Explorer dataset release activation started", "outputs", recipeOutputNames(changedOutputs))
			if err := activateExplorerReleaseWithRetry(c.Context(), capabilities.ActivateRelease, project, generation, selectorsForOutputs(changedOutputs, result.Bundle)); err != nil {
				status, code, message := explorerReleaseFailure(err, false)
				return publishFailure(status, code, message, "ACTIVATE_DATASET_RELEASE", fiber.Map{"generation": generation, "cause": err.Error(), "outputs": recipeOutputNames(changedOutputs)})
			}
			publishInfo("Explorer dataset release activation complete", "outputs", recipeOutputNames(changedOutputs))
			resolvedSchemaDigest := result.ResolvedSchemaDigest
			if len(executions) > 0 && executions[len(executions)-1].ResolvedSchemaDigest != "" {
				resolvedSchemaDigest = executions[len(executions)-1].ResolvedSchemaDigest
			}
			activeRelease = mergeMaterializedExplorerOutputs(result.Bundle, activeRelease, generation, resolvedSchemaDigest, result.OutputFingerprints, executions)
			if repositoryErr == nil && repository != nil {
				if err := persistActiveExplorerDataset(c.Context(), explorers, *repository, activeRelease); err != nil {
					return publishFailure(http.StatusInternalServerError, "DATASET_METADATA_PERSIST_FAILED", "the dataset was built but its release metadata could not be persisted", "PERSIST_DATASET_METADATA", fiber.Map{"generation": generation, "cause": err.Error()})
				}
				publishInfo("Explorer dataset metadata persisted", "outputs", recipeOutputNames(changedOutputs))
			}
		} else if activeErr != nil {
			return publishFailure(http.StatusServiceUnavailable, "DATASET_BUILD_UNAVAILABLE", "the active dataset release could not be resolved", "RESOLVE_ACTIVE_DATASET", activeDatasetErrorDetails(activeErr, result.Bundle))
		}
		materializations, datasetMetadata, err := reuseActiveExplorerDataset(result.Bundle, activeRelease)
		if err != nil {
			return publishFailure(http.StatusInternalServerError, "DATASET_OUTPUT_UNAVAILABLE", "the active dataset metadata became inconsistent during publish", "REUSE_DATASET_METADATA", fiber.Map{"cause": err.Error()})
		}
		compiled := explorer.Compilation{Bundle: result.Bundle, RecipeDigest: result.RecipeDigest, EmittedColumns: result.EmittedColumns}
		sourceGeneration := activeRelease.Generation
		resolvedSchemaDigest := activeRelease.SchemaDigest
		if resolvedSchemaDigest == "" {
			resolvedSchemaDigest = result.ResolvedSchemaDigest
		}
		publication := activeRelease.Publication
		publication.State = string(explorer.RevisionReady)
		publication.Generation = sourceGeneration
		revision, err := explorers.InsertReadyRevisionV2WithMetadata(c.Context(), owner, result.Config, digest, compiled, resolvedSchemaDigest, sourceGeneration, subjectFromFiber(c), materializations, datasetMetadata, publication)
		if err != nil {
			return publishFailure(http.StatusInternalServerError, "REVISION_INSERT_FAILED", err.Error(), "INSERT_REVISION", fiber.Map{"cause": err.Error()})
		}
		publishInfo("Explorer revision inserted", "revision_id", revision.ID, "reused_output_count", len(result.Bundle.Outputs)-len(changedOutputs))
		var activationErr error
		if id == "default" {
			activationErr = explorers.ActivateRepository(c.Context(), project, revision.ID)
		} else {
			activationErr = explorers.ActivateInteractive(c.Context(), project, id, revision.ID)
		}
		if activationErr != nil {
			return publishFailure(http.StatusServiceUnavailable, "ACTIVATION_FAILED", "the Explorer revision could not be activated", "ACTIVATE_EXPLORER", fiber.Map{"cause": activationErr.Error()})
		}
		state, err := getExplorerV2State(c.Context(), explorers, project, id)
		if err != nil {
			return publishFailure(http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error(), "READ_PUBLISHED_STATE", fiber.Map{"cause": err.Error()})
		}
		publishInfo("Explorer publish complete", "revision_id", revision.ID, "reused_output_count", len(result.Bundle.Outputs)-len(changedOutputs))
		return c.JSON(fiber.Map{"project": project, "explorerId": id, "publicationId": revision.ID, "draftVersion": owner.DraftVersion, "draftDigest": owner.DraftDigest, "activeUrl": state.ActiveURL, "state": state, "materializations": materializations, "diagnostics": result.Diagnostics})
	})
}

func selectorsForBundle(bundle recipe.Bundle) []dataset.DataframeSelector {
	selectors := make([]dataset.DataframeSelector, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		selectors = append(selectors, dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name})
	}
	return selectors
}

func selectorsForOutputs(outputs []recipe.Output, bundle recipe.Bundle) []dataset.DataframeSelector {
	selectors := make([]dataset.DataframeSelector, 0, len(outputs))
	for _, output := range outputs {
		selectors = append(selectors, dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name})
	}
	return selectors
}

func recipeOutputNames(outputs []recipe.Output) []string {
	names := make([]string, 0, len(outputs))
	for _, output := range outputs {
		names = append(names, output.Name)
	}
	return names
}

func outputsNeedingMaterialization(bundle recipe.Bundle, active activeExplorerDataset, generation string, fingerprints map[string]string) []recipe.Output {
	if strings.TrimSpace(active.Generation) == "" || active.Generation != generation {
		return append([]recipe.Output(nil), bundle.Outputs...)
	}
	result := make([]recipe.Output, 0)
	for _, output := range bundle.Outputs {
		var datasetOutput *explorer.DatasetOutput
		for index := range active.Dataset.Outputs {
			if active.Dataset.Outputs[index].Name == output.Name {
				candidate := active.Dataset.Outputs[index]
				datasetOutput = &candidate
				break
			}
		}
		var materialization *explorer.Materialization
		for index := range active.Materializations {
			if active.Materializations[index].Output == output.Name {
				candidate := active.Materializations[index]
				materialization = &candidate
				break
			}
		}
		fingerprint := strings.TrimSpace(fingerprints[output.Name])
		fingerprintRequired := len(fingerprints) > 0
		if datasetOutput == nil || materialization == nil || !datasetOutput.Queryable ||
			!selectorForOutputIsCanonical(datasetOutput.Selector, output.Name) ||
			!selectorForOutputIsCanonical(materialization.Selector, output.Name) ||
			*datasetOutput.Selector != *materialization.Selector ||
			(fingerprintRequired && (fingerprint == "" || (datasetOutput.Fingerprint != fingerprint && materialization.Fingerprint != fingerprint))) {
			result = append(result, output)
		}
	}
	return result
}

func verifyQueryableOutput(output, generation string, execution graphresolver.RecipeExecution) error {
	if strings.TrimSpace(execution.SourceGeneration) != strings.TrimSpace(generation) {
		return fmt.Errorf("output %q was materialized for generation %q instead of %q", output, execution.SourceGeneration, generation)
	}
	executionState := strings.ToUpper(strings.TrimSpace(execution.State))
	if executionState != "PUBLISHED" && executionState != "READY" && executionState != "ACTIVE" {
		return fmt.Errorf("output %q execution is not queryable (state %q)", output, execution.State)
	}
	for _, item := range execution.Outputs {
		if item.Name == output {
			state := strings.ToUpper(item.State)
			if state == "PUBLISHED" || state == "READY" || state == "ACTIVE" {
				return nil
			}
			return fmt.Errorf("output %q is not queryable (state %q)", output, item.State)
		}
	}
	return fmt.Errorf("output %q was not returned by the materializer", output)
}

func mergeMaterializedExplorerOutputs(bundle recipe.Bundle, active activeExplorerDataset, generation, schemaDigest string, fingerprints map[string]string, executions []graphresolver.RecipeExecution) activeExplorerDataset {
	materializations := make(map[string]explorer.Materialization, len(active.Materializations)+len(executions))
	for _, item := range active.Materializations {
		materializations[item.Output] = item
	}
	datasetOutputs := make(map[string]explorer.DatasetOutput, len(active.Dataset.Outputs)+len(executions))
	for _, item := range active.Dataset.Outputs {
		datasetOutputs[item.Name] = item
	}
	lastExecutionID := active.Publication.ExecutionID
	for _, execution := range executions {
		lastExecutionID = execution.ID
		for _, output := range execution.Outputs {
			selector := explorerOutputSelector(bundle, output.Name)
			fingerprint := fingerprints[output.Name]
			materializations[output.Name] = explorer.Materialization{OutputID: output.Name, Output: output.Name, MaterializationID: execution.ID, Fingerprint: fingerprint, Selector: selector, Columns: append([]publication.PhysicalColumn(nil), output.Columns...)}
			state := strings.ToUpper(output.State)
			datasetOutputs[output.Name] = explorer.DatasetOutput{Name: output.Name, State: state, Queryable: state == "PUBLISHED" || state == "READY" || state == "ACTIVE", Fingerprint: fingerprint, Selector: selector, Columns: append([]publication.PhysicalColumn(nil), output.Columns...)}
		}
	}
	mergedMaterials := make([]explorer.Materialization, 0, len(materializations))
	for _, item := range materializations {
		mergedMaterials = append(mergedMaterials, item)
	}
	sort.Slice(mergedMaterials, func(i, j int) bool { return mergedMaterials[i].Output < mergedMaterials[j].Output })
	mergedOutputs := make([]explorer.DatasetOutput, 0, len(datasetOutputs))
	for _, item := range datasetOutputs {
		mergedOutputs = append(mergedOutputs, item)
	}
	sort.Slice(mergedOutputs, func(i, j int) bool { return mergedOutputs[i].Name < mergedOutputs[j].Name })
	return activeExplorerDataset{
		Generation: generation, SchemaDigest: schemaDigest,
		Materializations: mergedMaterials,
		Dataset:          explorer.DatasetMetadata{Generation: generation, SchemaDigest: schemaDigest, Outputs: mergedOutputs},
		Publication:      explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: generation, ExecutionID: lastExecutionID, UpdatedAt: time.Now().UTC()},
	}
}

func activateExplorerReleaseWithRetry(ctx context.Context, activate ExplorerV2ReleaseActivator, project, generation string, selectors []dataset.DataframeSelector) error {
	err := activate(ctx, project, generation, selectors)
	if err != nil && errors.Is(err, dataset.ErrReleaseActivationConflict) {
		return activate(ctx, project, generation, selectors)
	}
	return err
}

func explorerReleaseFailure(err error, preflight bool) (int, string, string) {
	switch {
	case errors.Is(err, dataset.ErrSnapshotNotFound):
		return http.StatusServiceUnavailable, "DATASET_GENERATION_UNAVAILABLE", "the active dataset generation is not ready for publishing"
	case errors.Is(err, dataset.ErrReleaseRequirementsUnmet):
		return http.StatusServiceUnavailable, "DATASET_GENERATION_NOT_READY", "the active dataset generation is still being prepared"
	case errors.Is(err, dataset.ErrReleaseActivationConflict):
		return http.StatusConflict, "DATASET_RELEASE_CONFLICT", "the active dataset changed while publishing; retry publish"
	default:
		if preflight {
			return http.StatusServiceUnavailable, "DATASET_GENERATION_UNAVAILABLE", "the active dataset generation could not be validated"
		}
		return http.StatusServiceUnavailable, "DATASET_RELEASE_ACTIVATION_FAILED", "the dataset was built but could not be activated"
	}
}

type activeExplorerDataset struct {
	Generation       string
	SchemaDigest     string
	Materializations []explorer.Materialization
	Dataset          explorer.DatasetMetadata
	Publication      explorer.PublicationMetadata
}

type activeDatasetOutputDiagnostic struct {
	Output string `json:"output"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type activeDatasetCompatibility struct {
	Ready            bool                            `json:"ready"`
	ActiveGeneration string                          `json:"activeGeneration,omitempty"`
	MissingOutputs   []activeDatasetOutputDiagnostic `json:"missingOutputs,omitempty"`
	AvailableOutputs []string                        `json:"availableOutputs,omitempty"`
}

type activeExplorerDatasetError struct {
	message string
	details activeDatasetCompatibility
}

func (e *activeExplorerDatasetError) Error() string { return e.message }

func activeDatasetErrorDetails(err error, bundle recipe.Bundle) activeDatasetCompatibility {
	var datasetErr *activeExplorerDatasetError
	if errors.As(err, &datasetErr) {
		return datasetErr.details
	}
	return activeDatasetCompatibility{MissingOutputs: missingDatasetOutputs(bundle, "ACTIVE_RELEASE_UNAVAILABLE")}
}

func missingDatasetOutputs(bundle recipe.Bundle, reason string) []activeDatasetOutputDiagnostic {
	missing := make([]activeDatasetOutputDiagnostic, 0, len(bundle.Outputs))
	for index, output := range bundle.Outputs {
		missing = append(missing, activeDatasetOutputDiagnostic{Output: output.Name, Path: fmt.Sprintf("recipe.outputs[%d]", index), Reason: reason})
	}
	return missing
}

// resolveActiveExplorerDataset reads the canonical repository release record.
// Explorer revisions are configuration pointers, not dataset-release records;
// they are never used as a compatibility fallback here.
func resolveActiveExplorerDataset(ctx context.Context, service *explorer.Service, project string, bundle recipe.Bundle) (activeExplorerDataset, error) {
	repository, err := service.RepositoryConfig(ctx, project)
	if err != nil || repository == nil {
		return activeExplorerDataset{}, &activeExplorerDatasetError{
			message: fmt.Sprintf("active repository dataset release is unavailable for project %q", project),
			details: activeDatasetCompatibility{MissingOutputs: missingDatasetOutputs(bundle, "ACTIVE_RELEASE_UNAVAILABLE")},
		}
	}
	candidate := activeExplorerDatasetFromRepository(*repository)
	if candidate.Generation == "" {
		return activeExplorerDataset{}, &activeExplorerDatasetError{
			message: fmt.Sprintf("active repository dataset release for project %q has no generation", project),
			details: activeDatasetCompatibility{MissingOutputs: missingDatasetOutputs(bundle, "ACTIVE_RELEASE_UNAVAILABLE")},
		}
	}
	compatibility := inspectActiveExplorerDataset(candidate, bundle)
	if !compatibility.Ready {
		return activeExplorerDataset{}, &activeExplorerDatasetError{
			message: fmt.Sprintf("active repository dataset release for project %q does not contain complete metadata for the requested Explorer outputs", project),
			details: compatibility,
		}
	}
	return candidate, nil
}

func activeExplorerDatasetFromRepository(value explorer.RepositoryConfig) activeExplorerDataset {
	return activeExplorerDataset{Generation: strings.TrimSpace(value.SourceGeneration), SchemaDigest: value.Dataset.SchemaDigest, Materializations: append([]explorer.Materialization(nil), value.Materializations...), Dataset: value.Dataset, Publication: value.Publication}
}

func activeExplorerDatasetSupports(value activeExplorerDataset, bundle recipe.Bundle) bool {
	return inspectActiveExplorerDataset(value, bundle).Ready
}

func inspectActiveExplorerDataset(value activeExplorerDataset, bundle recipe.Bundle) activeDatasetCompatibility {
	compatibility := activeDatasetCompatibility{Ready: true, ActiveGeneration: value.Generation}
	for _, item := range value.Dataset.Outputs {
		if item.Name != "" {
			compatibility.AvailableOutputs = append(compatibility.AvailableOutputs, item.Name)
		}
	}
	sort.Strings(compatibility.AvailableOutputs)
	for index, output := range bundle.Outputs {
		diagnostic := activeDatasetOutputDiagnostic{Output: output.Name, Path: fmt.Sprintf("recipe.outputs[%d]", index)}
		var datasetOutput *explorer.DatasetOutput
		for itemIndex := range value.Dataset.Outputs {
			if value.Dataset.Outputs[itemIndex].Name == output.Name {
				candidate := value.Dataset.Outputs[itemIndex]
				datasetOutput = &candidate
				break
			}
		}
		if datasetOutput == nil {
			diagnostic.Reason = "MISSING_DATASET_OUTPUT"
		} else if !datasetOutput.Queryable && !isQueryableDatasetState(datasetOutput.State) {
			diagnostic.Reason = "OUTPUT_NOT_QUERYABLE"
		} else if !selectorForOutputIsCanonical(datasetOutput.Selector, output.Name) {
			diagnostic.Reason = "MISSING_DATASET_SELECTOR"
		} else {
			var materialization *explorer.Materialization
			for itemIndex := range value.Materializations {
				if value.Materializations[itemIndex].Output == output.Name {
					candidate := value.Materializations[itemIndex]
					materialization = &candidate
					break
				}
			}
			if materialization == nil || materialization.MaterializationID == "" {
				diagnostic.Reason = "MISSING_MATERIALIZATION"
			} else if !selectorForOutputIsCanonical(materialization.Selector, output.Name) {
				diagnostic.Reason = "MISSING_MATERIALIZATION_SELECTOR"
			} else if *datasetOutput.Selector != *materialization.Selector {
				diagnostic.Reason = "SELECTOR_MISMATCH"
			}
		}
		if diagnostic.Reason != "" {
			compatibility.Ready = false
			compatibility.MissingOutputs = append(compatibility.MissingOutputs, diagnostic)
		}
	}
	return compatibility
}

func persistActiveExplorerDataset(ctx context.Context, service *explorer.Service, repository explorer.RepositoryConfig, active activeExplorerDataset) error {
	materializations := mergeExplorerMaterializations(repository.Materializations, active.Materializations)
	dataset := mergeExplorerDataset(repository.Dataset, active.Dataset, active.Generation, active.SchemaDigest)
	repository.SourceGeneration = active.Generation
	repository.Materializations = materializations
	repository.Dataset = dataset
	repository.Publication = active.Publication
	if repository.ExecutionID == "" {
		repository.ExecutionID = active.Publication.ExecutionID
	}
	_, err := service.SaveRepositoryConfig(ctx, repository)
	return err
}

func mergeExplorerMaterializations(existing, updates []explorer.Materialization) []explorer.Materialization {
	byOutput := make(map[string]explorer.Materialization, len(existing)+len(updates))
	for _, value := range existing {
		byOutput[value.Output] = value
	}
	for _, value := range updates {
		byOutput[value.Output] = value
	}
	result := make([]explorer.Materialization, 0, len(byOutput))
	for _, value := range byOutput {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Output < result[j].Output })
	return result
}

func mergeExplorerDataset(existing, updates explorer.DatasetMetadata, generation, schemaDigest string) explorer.DatasetMetadata {
	byOutput := make(map[string]explorer.DatasetOutput, len(existing.Outputs)+len(updates.Outputs))
	for _, value := range existing.Outputs {
		byOutput[value.Name] = value
	}
	for _, value := range updates.Outputs {
		byOutput[value.Name] = value
	}
	outputs := make([]explorer.DatasetOutput, 0, len(byOutput))
	for _, value := range byOutput {
		outputs = append(outputs, value)
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Name < outputs[j].Name })
	if generation == "" {
		generation = existing.Generation
	}
	if schemaDigest == "" {
		schemaDigest = existing.SchemaDigest
	}
	return explorer.DatasetMetadata{Generation: generation, SchemaDigest: schemaDigest, Outputs: outputs}
}

func reuseActiveExplorerDataset(bundle recipe.Bundle, value activeExplorerDataset) ([]explorer.Materialization, explorer.DatasetMetadata, error) {
	materializations := make([]explorer.Materialization, 0, len(bundle.Outputs))
	outputs := make([]explorer.DatasetOutput, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		var materialization *explorer.Materialization
		for index := range value.Materializations {
			if value.Materializations[index].Output == output.Name {
				candidate := value.Materializations[index]
				materialization = &candidate
				break
			}
		}
		var datasetOutput *explorer.DatasetOutput
		for index := range value.Dataset.Outputs {
			if value.Dataset.Outputs[index].Name == output.Name {
				candidate := value.Dataset.Outputs[index]
				datasetOutput = &candidate
				break
			}
		}
		if datasetOutput == nil || (!datasetOutput.Queryable && !isQueryableDatasetState(datasetOutput.State)) {
			return nil, explorer.DatasetMetadata{}, fmt.Errorf("output %q is not queryable in the active dataset release", output.Name)
		}
		if materialization == nil || materialization.MaterializationID == "" || !selectorForOutputIsCanonical(materialization.Selector, output.Name) {
			return nil, explorer.DatasetMetadata{}, fmt.Errorf("output %q has incomplete active dataframe materialization", output.Name)
		}
		if !selectorForOutputIsCanonical(datasetOutput.Selector, output.Name) {
			return nil, explorer.DatasetMetadata{}, fmt.Errorf("output %q has no active dataframe selector", output.Name)
		}
		if *datasetOutput.Selector != *materialization.Selector {
			return nil, explorer.DatasetMetadata{}, fmt.Errorf("output %q has mismatched active dataframe selectors", output.Name)
		}
		materialization.OutputID = output.Name
		materialization.Output = output.Name
		if len(materialization.Columns) == 0 {
			materialization.Columns = append([]publication.PhysicalColumn(nil), datasetOutput.Columns...)
		}
		materializations = append(materializations, *materialization)
		datasetOutput.Name = output.Name
		datasetOutput.Columns = append([]publication.PhysicalColumn(nil), materialization.Columns...)
		outputs = append(outputs, *datasetOutput)
	}
	dataset := explorer.DatasetMetadata{Generation: value.Generation, SchemaDigest: value.SchemaDigest, Outputs: outputs}
	return materializations, dataset, nil
}

func selectorForOutputIsCanonical(selector *dataset.DataframeSelector, output string) bool {
	return selector != nil && selector.Valid() && selector.Output == output
}

func isQueryableDatasetState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "PUBLISHED", "READY", "ACTIVE":
		return true
	default:
		return false
	}
}

func decodeStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func principalFromFiber(c fiber.Ctx) *authscope.Principal {
	principal, _ := c.Locals("principal").(*authscope.Principal)
	return principal
}
func subjectFromFiber(c fiber.Ctx) string {
	if principal := principalFromFiber(c); principal != nil {
		return principal.Subject
	}
	return ""
}
func authorizeExplorerWrite(c fiber.Ctx, authorizer authscope.Authorizer, project string) error {
	path := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
	if err := authorizer.AuthorizeWrite(c.Context(), principalFromFiber(c), project, path); err != nil {
		return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
	}
	return nil
}

func explorerV2Error(c fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}

func explorerV2ErrorWithDetails(c fiber.Ctx, status int, code, message string, details any) error {
	body := fiber.Map{"code": code, "message": message, "details": details}
	if status == http.StatusServiceUnavailable || status == http.StatusConflict {
		body["retryable"] = true
	}
	if requestID, _ := c.Locals("request_id").(string); requestID != "" {
		body["requestId"] = requestID
	}
	return c.Status(status).JSON(fiber.Map{"error": body})
}

// publicExplorerPublishDetails keeps operational causes in structured logs
// while returning only stable, actionable fields to the Builder. In
// particular, raw AQL, SQL, and storage errors must not become UI copy.
func publicExplorerPublishDetails(details any) any {
	values, ok := details.(fiber.Map)
	if !ok {
		return details
	}
	public := make(fiber.Map, len(values))
	for key, value := range values {
		if key == "cause" || key == "retryable" {
			continue
		}
		public[key] = value
	}
	return public
}

func draftConflictResponse(c fiber.Ctx, explorers *explorer.Service, project, id string) error {
	current, err := explorers.Get(c.Context(), project, id)
	if err != nil {
		return explorerV2Error(c, http.StatusConflict, "DRAFT_CONFLICT", "Explorer draft changed; refresh before saving")
	}
	return c.Status(http.StatusConflict).JSON(fiber.Map{"error": fiber.Map{"code": "DRAFT_CONFLICT", "message": "Explorer draft changed; refresh before saving", "currentVersion": current.DraftVersion, "currentDigest": current.DraftDigest, "updatedAt": current.UpdatedAt.UTC().Format(time.RFC3339Nano)}})
}

func verifyExpectedVersion(ctx context.Context, service *explorer.Service, project, id string, expected *int64) error {
	if expected == nil {
		return nil
	}
	current, err := service.Get(ctx, project, id)
	if errors.Is(err, explorer.ErrNotFound) {
		return &v2HTTPError{status: http.StatusNotFound, code: "EXPLORER_NOT_FOUND", message: "Explorer not found"}
	}
	if err != nil {
		return err
	}
	if current.DraftVersion != *expected {
		return &v2HTTPError{status: http.StatusConflict, code: "DRAFT_CONFLICT", message: "Explorer draft changed; refresh before compiling", current: current}
	}
	return nil
}

func verifyExpectedDigest(ctx context.Context, service *explorer.Service, project, id, expected string) error {
	current, err := service.Get(ctx, project, id)
	if errors.Is(err, explorer.ErrNotFound) {
		return &v2HTTPError{status: http.StatusNotFound, code: "EXPLORER_NOT_FOUND", message: "Explorer not found"}
	}
	if err != nil {
		return err
	}
	if current.DraftDigest != expected {
		return &v2HTTPError{status: http.StatusConflict, code: "DRAFT_CONFLICT", message: "Explorer draft changed; refresh before previewing", current: current}
	}
	return nil
}

type v2HTTPError struct {
	status        int
	code, message string
	current       *explorer.Explorer
}

func (e *v2HTTPError) Error() string { return e.message }

func compileExplorerV2(ctx context.Context, compiler ExplorerV2Compiler, request ExplorerV2CompileRequest) (ExplorerV2CompileResult, string, error) {
	management := explorer.ConfigManagementForID(request.ExplorerID)
	_, bundle, canonical, digest, err := explorer.CanonicalConfigV2(request.Config, request.Project, request.ExplorerID, management)
	if err != nil {
		return ExplorerV2CompileResult{}, digest, err
	}
	if compiler != nil {
		result, err := compiler(ctx, request)
		if err != nil {
			return ExplorerV2CompileResult{}, digest, err
		}
		if len(result.Config) == 0 {
			result.Config = canonical
		}
		if _, _, normalized, _, err := explorer.CanonicalConfigV2(result.Config, request.Project, request.ExplorerID, management); err != nil {
			return ExplorerV2CompileResult{}, digest, err
		} else {
			result.Config = normalized
		}
		if result.Bundle.Name == "" {
			result.Bundle = bundle
		}
		if result.RecipeDigest == "" {
			result.RecipeDigest, err = result.Bundle.Digest()
			if err != nil {
				return ExplorerV2CompileResult{}, digest, err
			}
		}
		return result, digest, nil
	}
	return ExplorerV2CompileResult{Config: canonical, Bundle: bundle, RecipeDigest: mustBundleDigest(bundle)}, digest, nil
}

func mustBundleDigest(bundle recipe.Bundle) string { digest, _ := bundle.Digest(); return digest }

func previewFailure(c fiber.Ctx, digest string, err error) error {
	return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"outputColumns": []string{}, "rows": []map[string]any{}, "rowCount": 0, "digest": digest, "diagnostics": []explorer.Diagnostic{{Severity: "ERROR", Code: "PREVIEW_FAILED", Message: err.Error()}}})
}

func bundleHasOutput(bundle recipe.Bundle, name string) bool {
	if name == "" {
		return len(bundle.Outputs) > 0
	}
	for _, output := range bundle.Outputs {
		if output.Name == name {
			return true
		}
	}
	return false
}

func columnsForOutput(result ExplorerV2CompileResult, output string) []string {
	var columns []string
	for _, column := range result.EmittedColumns {
		if column.OutputID == output {
			columns = append(columns, column.PublicColumn)
		}
	}
	if len(columns) > 0 {
		return columns
	}
	for _, item := range result.Bundle.Outputs {
		if item.Name != output {
			continue
		}
		for _, field := range item.Fields {
			columns = append(columns, field.Name)
		}
	}
	return columns
}

func verifyQueryableOutputs(bundle recipe.Bundle, execution graphresolver.RecipeExecution) error {
	states := map[string]string{}
	for _, output := range execution.Outputs {
		states[output.Name] = strings.ToUpper(output.State)
	}
	for _, output := range bundle.Outputs {
		state := states[output.Name]
		if state != "PUBLISHED" && state != "READY" && state != "ACTIVE" {
			return fmt.Errorf("output %q is not queryable (state %q)", output.Name, state)
		}
	}
	return nil
}

func executionMaterializations(bundle recipe.Bundle, execution graphresolver.RecipeExecution) []explorer.Materialization {
	result := make([]explorer.Materialization, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		result = append(result, explorer.Materialization{OutputID: output.Name, Output: output.Name, MaterializationID: execution.ID, Selector: explorerOutputSelector(bundle, output.Name), Columns: append([]publication.PhysicalColumn(nil), output.Columns...)})
	}
	return result
}

func insertFailedV2Revision(ctx context.Context, service *explorer.Service, owner *explorer.Explorer, config []byte, digest, generation, actor string, diagnostics []explorer.Diagnostic) error {
	_, err := service.InsertFailedRevisionV2(ctx, owner, config, digest, generation, actor, diagnostics)
	return err
}

func blankExplorerConfigV2(project, id, title string) []byte {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + id, TranslationVersion: "interactive", Outputs: []recipe.Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference", Fields: []recipe.Field{{Name: "id", FieldRef: "DocumentReference.id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	recipeRaw, _ := json.Marshal(bundle)
	cfg := explorer.ConfigV2{APIVersion: explorer.ConfigV2APIVersion, Kind: "ExplorerConfig", Project: project, Explorer: explorer.ConfigExplorer{ID: id, Title: title, Management: "interactive"}, Recipe: recipeRaw, Views: []explorer.ConfigView{{ID: "document-reference", Title: title, Output: "DocumentReference", Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Label: "ID", Visible: true}}}}}}
	raw, _ := json.Marshal(cfg)
	return raw
}

func forkRepositoryConfig(raw []byte, project, id, title string) ([]byte, error) {
	cfg, bundle, err := explorer.DecodeDefaultConfigV2(raw, project)
	if err != nil {
		return nil, err
	}
	cfg.Explorer.ID, cfg.Explorer.Title, cfg.Explorer.Management = id, title, "interactive"
	cfg.Views = make([]explorer.ConfigView, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		columns := make([]explorer.ConfigColumn, 0, len(output.Fields))
		for _, field := range output.Fields {
			columns = append(columns, explorer.ConfigColumn{Column: field.Name, Label: field.Name, Visible: true})
		}
		if len(columns) == 0 {
			continue
		}
		cfg.Views = append(cfg.Views, explorer.ConfigView{ID: explorer.StableExplorerID(output.Name), Title: output.Name, Output: output.Name, Table: explorer.ConfigTable{Columns: columns}})
	}
	if len(cfg.Views) == 0 {
		return nil, fmt.Errorf("repository baseline has no executable output fields to seed a custom Explorer")
	}
	return json.Marshal(cfg)
}

func getExplorerV2State(ctx context.Context, service *explorer.Service, project, id string) (*explorerV2State, error) {
	value, err := service.Get(ctx, project, id)
	if err == nil {
		state := stateFromExplorer(value)
		if value.ActiveRevisionID != "" {
			if revision, revisionErr := service.Revision(ctx, value.ActiveRevisionID); revisionErr == nil {
				state = mergeRevisionState(state, revision)
			}
		}
		return state, nil
	}
	if id != "default" || !errors.Is(err, explorer.ErrNotFound) {
		return nil, err
	}
	repository, repositoryErr := service.RepositoryConfig(ctx, project)
	if repositoryErr != nil {
		return nil, repositoryErr
	}
	digest := repository.ConfigDigest
	if digest == "" {
		_, _, _, digest, _ = explorer.CanonicalConfigV2(repository.Config, project, "default", "repository")
	}
	version := repository.DraftVersion
	if version == 0 {
		version = 1
	}
	return &explorerV2State{Project: project, ExplorerID: "default", Management: explorer.ManagementRepository, BaselineConfig: repositoryBaselineConfig(repository.Config), DraftConfig: cloneRaw(repository.Config), DraftVersion: version, DraftDigest: digest, ActiveConfig: cloneRaw(repository.Config), ActiveRevisionID: repository.ActiveRevisionID, SourceGeneration: repository.SourceGeneration, Materializations: repository.Materializations, Dataset: repository.Dataset, Publication: repository.Publication, Diagnostics: repository.Diagnostics, PublicationState: repository.Publication.State, ActiveURL: explorerURL(project, "default"), UpdatedAt: repository.UpdatedAt}, nil
}

func listExplorerV2States(ctx context.Context, service *explorer.Service, project string) ([]*explorerV2State, error) {
	ids := map[string]bool{"default": true}
	values, err := service.List(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		ids[value.ExplorerID] = true
	}
	configs, configErr := service.ListConfigs(ctx, project)
	if configErr == nil {
		for _, value := range configs {
			// RepositoryConfig rows are compatibility/readiness records. Custom
			// identities are owned by loom_explorers; accepting arbitrary legacy
			// config IDs here recreates the orphan-row 500 failure mode.
			if value.ExplorerID == "default" {
				ids["default"] = true
			}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i] == "default" {
			return true
		}
		if ordered[j] == "default" {
			return false
		}
		return ordered[i] < ordered[j]
	})
	result := make([]*explorerV2State, 0, len(ordered))
	for _, id := range ordered {
		state, err := getExplorerV2State(ctx, service, project, id)
		if errors.Is(err, explorer.ErrNotFound) && id == "default" {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, nil
}

func stateFromExplorer(value *explorer.Explorer) *explorerV2State {
	if value.ManagementMode == explorer.ManagementRepository {
		return &explorerV2State{Project: value.Project, ExplorerID: value.ExplorerID, Management: value.ManagementMode, BaselineConfig: repositoryBaselineConfig(value.DraftConfig), DraftConfig: cloneRaw(value.DraftConfig), DraftVersion: value.DraftVersion, DraftDigest: value.DraftDigest, ActiveConfig: cloneRaw(value.ActiveConfig), ActiveRevisionID: value.ActiveRevisionID, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, EmittedColumns: append([]explorer.EmittedColumn(nil), value.EmittedColumns...), Materializations: append([]explorer.Materialization(nil), value.Materializations...), Dataset: value.Dataset, Publication: value.Publication, Diagnostics: append([]explorer.Diagnostic(nil), value.Diagnostics...), PublicationState: value.Publication.State, ActiveURL: explorerURL(value.Project, value.ExplorerID), UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt}
	}
	return &explorerV2State{Project: value.Project, ExplorerID: value.ExplorerID, Management: value.ManagementMode, DraftConfig: cloneRaw(value.DraftConfig), DraftVersion: value.DraftVersion, DraftDigest: value.DraftDigest, ActiveConfig: cloneRaw(value.ActiveConfig), ActiveRevisionID: value.ActiveRevisionID, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, EmittedColumns: append([]explorer.EmittedColumn(nil), value.EmittedColumns...), Materializations: append([]explorer.Materialization(nil), value.Materializations...), Dataset: value.Dataset, Publication: value.Publication, Diagnostics: append([]explorer.Diagnostic(nil), value.Diagnostics...), PublicationState: publicationState(value.ActiveRevisionID), ActiveURL: explorerURL(value.Project, value.ExplorerID), UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt}
}

func mergeRevisionState(state *explorerV2State, revision *explorer.Revision) *explorerV2State {
	state.ActiveConfig = cloneRaw(revision.Config)
	state.RecipeDigest, state.ResolvedSchemaDigest, state.SourceGeneration = revision.RecipeDigest, revision.ResolvedSchemaDigest, revision.SourceGeneration
	state.EmittedColumns, state.Diagnostics = append([]explorer.EmittedColumn(nil), revision.EmittedColumns...), append([]explorer.Diagnostic(nil), revision.Diagnostics...)
	state.Materializations, state.Dataset = explorer.WithDataframeSelectors(revision.Recipe, revision.Materializations, revision.Dataset)
	state.Publication = revision.Publication
	state.Publication.State, state.Publication.RevisionID = string(revision.Status), revision.ID
	state.PublicationState = string(revision.Status)
	return state
}
func publicationState(id string) string {
	if id == "" {
		return ""
	}
	return string(explorer.RevisionActive)
}
func explorerURL(project, id string) string {
	return "/api/v1/projects/" + project + "/explorers/" + id
}
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), raw...)
}

func repositoryBaselineConfig(raw json.RawMessage) json.RawMessage {
	var cfg explorer.ConfigV2
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cloneRaw(raw)
	}
	cfg.Views = nil
	cfg.SharedFilters = nil
	cfg.FileActions = explorer.FileActions{}
	baseline, err := json.Marshal(cfg)
	if err != nil {
		return cloneRaw(raw)
	}
	return baseline
}

func stateErrorResponse(c fiber.Ctx, err error) error {
	var httpErr *v2HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.current != nil {
			return c.Status(httpErr.status).JSON(fiber.Map{"error": fiber.Map{"code": httpErr.code, "message": httpErr.message, "currentVersion": httpErr.current.DraftVersion, "currentDigest": httpErr.current.DraftDigest, "updatedAt": httpErr.current.UpdatedAt.UTC().Format(time.RFC3339Nano)}})
		}
		return explorerV2Error(c, httpErr.status, httpErr.code, httpErr.message)
	}
	return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_FAILED", err.Error())
}
