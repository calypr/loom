package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

// ExplorerV2CompileRequest is the server-side authoring seam. Browser
// requests contain opaque candidate IDs only; the compiler resolves them from
// Loom's authoritative catalog.
type ExplorerV2CompileRequest struct {
	Project                    string
	ExplorerID                 string
	RequestID                  string
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

// ExplorerV2ReceiptCompileRequest is the native V2 compile request. The
// compiler owns canonicalization, capability validation, and persistence of
// the immutable receipt before returning it to the HTTP layer.
type ExplorerV2ReceiptCompileRequest struct {
	Project       string
	ExplorerID    string
	Document      authoringv2.Document
	SnapshotToken string
	RequestID     string
	// Authorized is the exact active capability already resolved by the HTTP
	// boundary. Production compilation reuses it so idempotent editor compiles
	// do not repeat manifest and authorization discovery.
	Authorized AuthorizedCapability
}

type ExplorerV2ReceiptCompiler func(context.Context, ExplorerV2ReceiptCompileRequest) (*explorer.CompilationReceipt, error)
type ExplorerV2ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)

// ExplorerV2ReceiptPreviewer is the bounded transport seam. The
// engine invokes visitor once per public row and returns summary metadata;
// handlers can encode rows without retaining an unbounded result map.
type ExplorerV2ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (engine.PreviewSummary, error)

type ExplorerV2ReceiptMaterializer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error)
type ExplorerV2GenerationValidator func(context.Context, string, string) error
type ExplorerV2ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error
type ExplorerAuthoringV1Compiler func(context.Context, ExplorerAuthoringV1CompileRequest) (ExplorerAuthoringV1CompileResult, error)

// ExplorerV2LifecycleConfig contains explicit capabilities for the REST
// surface. The HTTP layer does not reach into catalog, compiler, or storage
// internals.
type ExplorerV2LifecycleConfig struct {
	Compile ExplorerV2Compiler
	// CompileReceipt is the production V2 authoring compiler. It returns only
	// after the receipt has been durably persisted.
	CompileReceipt ExplorerV2ReceiptCompiler
	Catalog        explorerV2CatalogReader
	// Capability and CapabilityToken expose the compiler-owned immutable V2
	// snapshot. Catalog remains an internal V1 migration projection only.
	Capability                    ExplorerCapabilityReader
	CapabilityToken               ExplorerCapabilityTokenReader
	AuthorizedCapabilityCompile   ExplorerAuthorizedCapabilityCompilationReader
	AuthorizedCapabilityExecution ExplorerAuthorizedCapabilityExecutionReader
	Preview                       ExplorerV2Previewer
	PreviewReceipt                ExplorerV2ReceiptPreviewer
	Materialize                   ExplorerV2Materializer
	MaterializeReceipt            ExplorerV2ReceiptMaterializer
	// ReceiptLookup must enforce project and Explorer tenancy in the backing
	// repository. The legacy service lookup remains a compatibility fallback
	// for older tests and stored-document migration.
	ReceiptLookup ExplorerV2ReceiptReader
	Logger        *slog.Logger
	// ValidateReleaseGeneration preflights the immutable snapshot required by
	// release activation before Publish performs expensive materialization.
	ValidateReleaseGeneration ExplorerV2GenerationValidator
	// Publish invokes ActivateRelease when the draft requires outputs that are
	// not already present in the active dataset release.
	ActivateRelease ExplorerV2ReleaseActivator
	// AuthoringCompile accepts only V1 authoring intent. It is separate from
	// Compile, which remains the repository/ETL packet seam.
	AuthoringCompile ExplorerAuthoringV1Compiler
}

type explorerV2State struct {
	Project               string                  `json:"project"`
	ExplorerID            string                  `json:"explorerId"`
	Title                 string                  `json:"-"`
	Management            explorer.ManagementMode `json:"management"`
	ActiveAuthoringBundle json.RawMessage         `json:"-"`
	ActiveIntentDigest    string                  `json:"-"`
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

func registerLegacyExplorerLifecycleRoutes(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil {
		return
	}

	app.Get("/api/v1/projects/:project/explorers", func(c fiber.Ctx) error {
		project := explorerProjectParam(c)
		if err := authorizeRead(c.Context(), principalFromFiber(c), project); err != nil {
			return explorerV2Error(c, http.StatusForbidden, "FORBIDDEN", "forbidden")
		}
		values, err := listExplorerV2States(c.Context(), explorers, project)
		if err != nil {
			return explorerV2Error(c, http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error())
		}
		states := make([]explorer.ExplorerStateV1, 0, len(values))
		for _, value := range values {
			states = append(states, stateV1FromExplorerV2State(value))
		}
		return c.JSON(states)
	})

	app.Get("/api/v1/projects/:project/explorers/:explorerId", func(c fiber.Ctx) error {
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
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
		return c.JSON(stateV1FromExplorerV2State(value))
	})
	app.Get("/api/v1/projects/:project/explorers/:explorerId/authoring/catalog", func(c fiber.Ctx) error {
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
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
		project := explorerProjectParam(c)
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
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
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
		cfg, _, canonical, _, err := explorer.CanonicalConfigV2(request.Config, project, id, explorer.ConfigManagementForID(id))
		if err != nil {
			return explorerV2Error(c, http.StatusUnprocessableEntity, "INVALID_CONFIG", err.Error())
		}
		var repairDiagnostics []explorer.Diagnostic
		if owner, ownerErr := explorers.Get(c.Context(), project, id); ownerErr == nil {
			if repaired, diagnostics, repairErr := explorer.RepairConfigV2Presentation(cfg, emittedColumnsByOutput(*owner)); repairErr != nil {
				return explorerV2ErrorFromCause(c, http.StatusUnprocessableEntity, "INVALID_CONFIG", repairErr)
			} else if len(diagnostics) > 0 {
				repairDiagnostics = diagnostics
				repairedRaw, marshalErr := json.Marshal(repaired)
				if marshalErr != nil {
					return explorerV2Error(c, http.StatusInternalServerError, "CONFIG_REPAIR_FAILED", marshalErr.Error())
				}
				_, _, canonical, _, err = explorer.CanonicalConfigV2(repairedRaw, project, id, explorer.ConfigManagementForID(id))
				if err != nil {
					return explorerV2Error(c, http.StatusUnprocessableEntity, "INVALID_CONFIG", err.Error())
				}
			}
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
		state := stateFromExplorer(value)
		state.Diagnostics = append(state.Diagnostics, repairDiagnostics...)
		return c.JSON(state)
	})

	app.Post("/api/v1/projects/:project/explorers/:explorerId/authoring/compile", func(c fiber.Ctx) error {
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
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
		result, digest, err := compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, RequestID: requestIDFromFiber(c), Config: request.Config, SnapshotToken: request.SnapshotToken, SelectedCandidateIDsByNode: request.SelectedCandidateIDsByNode, Output: request.Output})
		if err != nil {
			return explorerV2ErrorFromCause(c, http.StatusUnprocessableEntity, "COMPILE_FAILED", err)
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
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
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
		result, digest, err := compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, RequestID: requestIDFromFiber(c), Config: request.Config, Output: request.Output})
		if err != nil {
			return previewFailure(c, digest, err)
		}
		if !bundleHasOutput(result.Bundle, request.Output) {
			return previewFailure(c, digest, fmt.Errorf("unsupported output %q", request.Output))
		}
		if capabilities.Preview == nil {
			return previewFailure(c, digest, fmt.Errorf("preview executor is not configured"))
		}
		rows, err := capabilities.Preview(c.Context(), result.Bundle, recipe.RuntimeBindings{Project: projectid.Legacy(project), PreviewLimit: request.Limit, DatasetGeneration: result.SourceGeneration, OutputNames: []string{request.Output}})
		if err != nil {
			return previewFailure(c, digest, err)
		}
		selected := rows[request.Output]
		return c.JSON(fiber.Map{"project": project, "explorerId": id, "output": request.Output, "config": json.RawMessage(result.Config), "columns": columnsForOutput(result, request.Output), "rows": selected, "rowCount": len(selected), "digest": digest, "diagnostics": result.Diagnostics})
	})

	app.Post("/api/v1/projects/:project/explorers/:explorerId/publish", func(c fiber.Ctx) error {
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
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
		result, digest, err := compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, RequestID: requestIDFromFiber(c), Config: owner.DraftConfig})
		if err != nil {
			// A persisted canonical draft should compile. Retry against the
			// latest draft once so a concurrent last-write-wins save or a
			// transient catalog update does not become a user-facing 422.
			if latest, readErr := explorers.Get(c.Context(), project, id); readErr == nil {
				owner = latest
				result, digest, err = compileExplorerV2(c.Context(), capabilities.Compile, ExplorerV2CompileRequest{Project: project, ExplorerID: id, RequestID: requestIDFromFiber(c), Config: owner.DraftConfig})
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
				if validateErr := capabilities.ValidateReleaseGeneration(c.Context(), projectid.Legacy(project), generation); validateErr != nil {
					status, code, message := explorerReleaseFailure(validateErr, true)
					return publishFailure(status, code, message, "VALIDATE_DATASET_RELEASE", fiber.Map{"generation": generation, "cause": validateErr.Error()})
				}
			}
			var executions []graphresolver.RecipeExecution
			for _, output := range changedOutputs {
				publishInfo("Explorer output materialization started", "output", output.Name)
				execution, materializeErr := capabilities.Materialize(c.Context(), result.Bundle, recipe.RuntimeBindings{Project: projectid.Legacy(project), DatasetGeneration: generation, OutputNames: []string{output.Name}})
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
			if err := activateExplorerReleaseWithRetry(c.Context(), capabilities.ActivateRelease, projectid.Legacy(project), generation, selectorsForOutputs(changedOutputs, result.Bundle)); err != nil {
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

func explorerV2ErrorFromCause(c fiber.Ctx, status int, code string, cause error) error {
	var repairErr *explorer.PresentationRepairError
	if errors.As(cause, &repairErr) {
		diagnostic := repairErr.Diagnostic
		if diagnostic.RequestID == "" {
			diagnostic.RequestID = requestIDFromFiber(c)
		}
		return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{
			"error":       fiber.Map{"code": diagnostic.Code, "message": diagnostic.Message, "fieldPath": diagnostic.FieldPath, "details": diagnostic.Details, "requestId": diagnostic.RequestID},
			"diagnostics": []explorer.Diagnostic{diagnostic},
		})
	}
	return explorerV2Error(c, status, code, cause.Error())
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
		if _, _, normalized, normalizedDigest, err := explorer.CanonicalConfigV2(result.Config, request.Project, request.ExplorerID, management); err != nil {
			return ExplorerV2CompileResult{}, digest, err
		} else {
			result.Config = normalized
			digest = normalizedDigest
		}
		for index := range result.Diagnostics {
			if result.Diagnostics[index].RequestID == "" {
				result.Diagnostics[index].RequestID = request.RequestID
			}
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
	var repairErr *explorer.PresentationRepairError
	if errors.As(err, &repairErr) {
		diagnostic := repairErr.Diagnostic
		if diagnostic.RequestID == "" {
			diagnostic.RequestID = requestIDFromFiber(c)
		}
		return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{"outputColumns": []string{}, "rows": []map[string]any{}, "rowCount": 0, "digest": digest, "diagnostics": []explorer.Diagnostic{diagnostic}})
	}
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

func emittedColumnsByOutput(value explorer.Explorer) map[string]map[string]bool {
	columns := make(map[string]map[string]bool)
	for _, emitted := range value.EmittedColumns {
		if emitted.OutputID == "" || emitted.PublicColumn == "" {
			continue
		}
		if columns[emitted.OutputID] == nil {
			columns[emitted.OutputID] = make(map[string]bool)
		}
		columns[emitted.OutputID][emitted.PublicColumn] = true
	}
	if len(columns) != 0 {
		return columns
	}
	for _, output := range value.Dataset.Outputs {
		if output.Name == "" {
			continue
		}
		if columns[output.Name] == nil {
			columns[output.Name] = make(map[string]bool)
		}
		for _, column := range output.Columns {
			if column.Name != "" {
				columns[output.Name][column.Name] = true
			}
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
		var repairDiagnostics []explorer.Diagnostic
		if repaired, diagnostics, repairErr := repairExplorerDraftOnLoad(ctx, service, value); repairErr == nil {
			value, repairDiagnostics = repaired, diagnostics
		}
		state := stateFromExplorer(value)
		if value.ActiveRevisionID != "" {
			if revision, revisionErr := service.Revision(ctx, value.ActiveRevisionID); revisionErr == nil {
				state = mergeRevisionState(state, revision)
			}
		}
		state.Diagnostics = append(state.Diagnostics, repairDiagnostics...)
		state = repairActiveExplorerPresentation(state)
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
	state := &explorerV2State{Project: project, ExplorerID: "default", Title: configV2Title(repository.Config), Management: explorer.ManagementRepository, BaselineConfig: repositoryBaselineConfig(repository.Config), DraftConfig: cloneRaw(repository.Config), DraftVersion: version, DraftDigest: digest, ActiveConfig: cloneRaw(repository.Config), ActiveRevisionID: repository.ActiveRevisionID, SourceGeneration: repository.SourceGeneration, Materializations: repository.Materializations, Dataset: repository.Dataset, Publication: repository.Publication, Diagnostics: repository.Diagnostics, PublicationState: repository.Publication.State, ActiveURL: explorerURL(project, "default"), UpdatedAt: repository.UpdatedAt}
	return repairActiveExplorerPresentation(state), nil
}

func repairExplorerDraftOnLoad(ctx context.Context, service *explorer.Service, value *explorer.Explorer) (*explorer.Explorer, []explorer.Diagnostic, error) {
	if value == nil || len(value.DraftConfig) == 0 {
		return value, nil, nil
	}
	available := emittedColumnsByOutput(*value)
	if len(available) == 0 {
		return value, nil, nil
	}
	cfg, _, _, _, err := explorer.CanonicalConfigV2(value.DraftConfig, value.Project, value.ExplorerID, explorer.ConfigManagementForID(value.ExplorerID))
	if err != nil {
		return value, nil, nil
	}
	repaired, diagnostics, repairErr := explorer.RepairConfigV2Presentation(cfg, available)
	if repairErr != nil || len(diagnostics) == 0 {
		return value, nil, repairErr
	}
	raw, err := json.Marshal(repaired)
	if err != nil {
		return value, nil, err
	}
	_, _, canonical, _, err := explorer.CanonicalConfigV2(raw, value.Project, value.ExplorerID, explorer.ConfigManagementForID(value.ExplorerID))
	if err != nil {
		return value, nil, err
	}
	saved, err := service.SaveDraftV2(ctx, value.Project, value.ExplorerID, canonical, value.DraftVersion, value.DraftDigest, "loom-presentation-repair")
	if err != nil {
		return value, nil, err
	}
	return saved, diagnostics, nil
}

func repairActiveExplorerPresentation(state *explorerV2State) *explorerV2State {
	if state == nil || len(state.ActiveConfig) == 0 {
		return state
	}
	available := make(map[string]map[string]bool)
	for _, emitted := range state.EmittedColumns {
		if emitted.OutputID == "" || emitted.PublicColumn == "" {
			continue
		}
		if available[emitted.OutputID] == nil {
			available[emitted.OutputID] = make(map[string]bool)
		}
		available[emitted.OutputID][emitted.PublicColumn] = true
	}
	if len(available) == 0 {
		for _, output := range state.Dataset.Outputs {
			if output.Name == "" {
				continue
			}
			if available[output.Name] == nil {
				available[output.Name] = make(map[string]bool)
			}
			for _, column := range output.Columns {
				if column.Name != "" {
					available[output.Name][column.Name] = true
				}
			}
		}
	}
	if len(available) == 0 {
		return state
	}
	cfg, _, _, _, err := explorer.CanonicalConfigV2(state.ActiveConfig, state.Project, state.ExplorerID, explorer.ConfigManagementForID(state.ExplorerID))
	if err != nil {
		return state
	}
	repaired, diagnostics, repairErr := explorer.RepairConfigV2Presentation(cfg, available)
	if repairErr != nil {
		var presentationErr *explorer.PresentationRepairError
		if errors.As(repairErr, &presentationErr) {
			state.Diagnostics = append(state.Diagnostics, presentationErr.Diagnostic)
		}
		return state
	}
	if len(diagnostics) == 0 {
		return state
	}
	raw, err := json.Marshal(repaired)
	if err != nil {
		return state
	}
	state.ActiveConfig = raw
	state.Diagnostics = append(state.Diagnostics, diagnostics...)
	return state
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
	project := projectid.Canonical(value.Project)
	if value.ManagementMode == explorer.ManagementRepository {
		return &explorerV2State{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, BaselineConfig: repositoryBaselineConfig(value.DraftConfig), DraftConfig: cloneRaw(value.DraftConfig), DraftVersion: value.DraftVersion, DraftDigest: value.DraftDigest, ActiveConfig: cloneRaw(value.ActiveConfig), ActiveRevisionID: value.ActiveRevisionID, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, EmittedColumns: append([]explorer.EmittedColumn(nil), value.EmittedColumns...), Materializations: append([]explorer.Materialization(nil), value.Materializations...), Dataset: value.Dataset, Publication: value.Publication, Diagnostics: append([]explorer.Diagnostic(nil), value.Diagnostics...), PublicationState: value.Publication.State, ActiveURL: explorerURL(project, value.ExplorerID), UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt}
	}
	return &explorerV2State{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, DraftConfig: cloneRaw(value.DraftConfig), DraftVersion: value.DraftVersion, DraftDigest: value.DraftDigest, ActiveConfig: cloneRaw(value.ActiveConfig), ActiveRevisionID: value.ActiveRevisionID, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, EmittedColumns: append([]explorer.EmittedColumn(nil), value.EmittedColumns...), Materializations: append([]explorer.Materialization(nil), value.Materializations...), Dataset: value.Dataset, Publication: value.Publication, Diagnostics: append([]explorer.Diagnostic(nil), value.Diagnostics...), PublicationState: publicationState(value.ActiveRevisionID), ActiveURL: explorerURL(project, value.ExplorerID), UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt}
}

func mergeRevisionState(state *explorerV2State, revision *explorer.Revision) *explorerV2State {
	state.ActiveConfig = cloneRaw(revision.Config)
	state.ActiveAuthoringBundle = cloneOptionalRaw(revision.AuthoringBundle)
	state.ActiveIntentDigest = revision.IntentDigest
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
	return "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(id)
}
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneOptionalRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func stateV1FromExplorerV2State(state *explorerV2State) explorer.ExplorerStateV1 {
	result := explorer.ExplorerStateV1{
		APIVersion: explorer.ExplorerStateV1APIVersion,
		Kind:       explorer.ExplorerStateV1Kind,
		Project:    state.Project,
		ExplorerID: state.ExplorerID,
		Title:      state.Title,
		Management: state.Management,
		ActiveURL:  state.ActiveURL,
		UpdatedBy:  state.UpdatedBy,
		UpdatedAt:  state.UpdatedAt,
	}
	result.Draft = explorer.ExplorerStateV1Draft{
		Bundle:       nil,
		Version:      state.DraftVersion,
		Digest:       state.DraftDigest,
		IntentDigest: "",
		ReceiptID:    "",
	}
	result.Active = explorer.ExplorerStateV1Active{
		Bundle:       canonicalAuthoringBundle(state.ActiveAuthoringBundle),
		RevisionID:   state.ActiveRevisionID,
		IntentDigest: state.ActiveIntentDigest,
		Status:       state.Publication.State,
	}
	result.Generated = explorer.ExplorerStateV1Generated{
		RecipeDigest:         state.RecipeDigest,
		ResolvedSchemaDigest: state.ResolvedSchemaDigest,
		SourceGeneration:     state.SourceGeneration,
		EmittedColumns:       append([]explorer.EmittedColumn(nil), state.EmittedColumns...),
		Materializations:     append([]explorer.Materialization(nil), state.Materializations...),
		Dataset:              state.Dataset,
		Publication:          state.Publication,
		Diagnostics:          append([]explorer.Diagnostic(nil), state.Diagnostics...),
	}
	result.Runtime = runtimeV1FromExplorerV2State(state)
	return result
}

type runtimeProjectionState struct {
	ActiveConfig         json.RawMessage
	SourceGeneration     string
	ResolvedSchemaDigest string
	EmittedColumns       []explorer.EmittedColumn
	Materializations     []explorer.Materialization
	Dataset              explorer.DatasetMetadata
	Publication          explorer.PublicationMetadata
	Diagnostics          []explorer.Diagnostic
}

func runtimeV1FromPublishedRevision(revision *explorer.Revision) *explorer.ExplorerRuntimeV1 {
	if revision == nil {
		return nil
	}
	return runtimeV1FromProjection(runtimeProjectionState{
		ActiveConfig:         revision.Config,
		SourceGeneration:     revision.SourceGeneration,
		ResolvedSchemaDigest: revision.ResolvedSchemaDigest,
		EmittedColumns:       revision.EmittedColumns,
		Materializations:     revision.Materializations,
		Dataset:              revision.Dataset,
		Publication:          revision.Publication,
		Diagnostics:          revision.Diagnostics,
	})
}

// runtimeV1FromExplorerV2State is retained only for legacy lifecycle tests and
// migration helpers. Registered runtime reads use runtimeV1FromPublishedRevision.
func runtimeV1FromExplorerV2State(legacy *explorerV2State) *explorer.ExplorerRuntimeV1 {
	if legacy == nil {
		return nil
	}
	return runtimeV1FromProjection(runtimeProjectionState{
		ActiveConfig:         legacy.ActiveConfig,
		SourceGeneration:     legacy.SourceGeneration,
		ResolvedSchemaDigest: legacy.ResolvedSchemaDigest,
		EmittedColumns:       legacy.EmittedColumns,
		Materializations:     legacy.Materializations,
		Dataset:              legacy.Dataset,
		Publication:          legacy.Publication,
		Diagnostics:          legacy.Diagnostics,
	})
}

func runtimeV1FromProjection(state runtimeProjectionState) *explorer.ExplorerRuntimeV1 {
	if len(state.ActiveConfig) == 0 {
		return nil
	}
	var config explorer.ConfigV2
	if err := json.Unmarshal(state.ActiveConfig, &config); err != nil || len(config.Views) == 0 {
		return nil
	}
	recipeLabels := explorerRecipeColumnLabels(config.Recipe)

	datasetOutputs := make(map[string]explorer.DatasetOutput, len(state.Dataset.Outputs))
	physicalColumns := make(map[string]map[string]publication.PhysicalColumn, len(state.Dataset.Outputs))
	for _, output := range state.Dataset.Outputs {
		datasetOutputs[output.Name] = output
		physicalColumns[output.Name] = make(map[string]publication.PhysicalColumn, len(output.Columns))
		for _, column := range output.Columns {
			physicalColumns[output.Name][column.Name] = column
		}
	}
	materializations := make(map[string]explorer.Materialization, len(state.Materializations))
	for _, materialization := range state.Materializations {
		outputID := firstNonEmpty(materialization.OutputID, materialization.Output)
		materializations[outputID] = materialization
		if physicalColumns[outputID] == nil {
			physicalColumns[outputID] = make(map[string]publication.PhysicalColumn, len(materialization.Columns))
		}
		for _, column := range materialization.Columns {
			physicalColumns[outputID][column.Name] = column
		}
	}
	emittedColumns := make(map[string]map[string]explorer.EmittedColumn)
	for _, emitted := range state.EmittedColumns {
		if emitted.OutputID == "" || emitted.PublicColumn == "" {
			continue
		}
		if emittedColumns[emitted.OutputID] == nil {
			emittedColumns[emitted.OutputID] = make(map[string]explorer.EmittedColumn)
		}
		emittedColumns[emitted.OutputID][emitted.PublicColumn] = emitted
	}
	// Authoring emissions use stable logical names (c_<hash>), while the
	// publication layer may qualify those names for the physical dataframe
	// schema (for example patient_c_<hash>). Resolve that boundary from the
	// authoritative published column list instead of asking the renderer to
	// guess physical names. A suffix match is accepted only when it is
	// unambiguous for the output.
	publicToPhysical := make(map[string]map[string]string)
	for outputID, emitted := range emittedColumns {
		physical := physicalColumns[outputID]
		if len(physical) == 0 {
			continue
		}
		aliases := make(map[string]string)
		for publicName, emission := range emitted {
			logicalNames := []string{publicName}
			// Authoring revisions compiled before PublicColumn was aligned with
			// the lowered recipe field used an emission-derived hash here. The
			// persisted candidate/occurrence identity still lets us recover the
			// actual logical field name without rewriting durable state.
			if emission.CandidateID != "" && emission.OccurrenceID != "" {
				legacyLogicalName := generatedFieldName(emission.CandidateID, emission.OccurrenceID)
				if legacyLogicalName != publicName {
					logicalNames = append(logicalNames, legacyLogicalName)
				}
			}
			matches := make([]string, 0, 1)
			for physicalName := range physical {
				for _, logicalName := range logicalNames {
					if physicalName == logicalName || strings.HasSuffix(physicalName, "_"+logicalName) {
						matches = append(matches, physicalName)
						break
					}
				}
			}
			if len(matches) == 1 {
				aliases[publicName] = matches[0]
			}
		}
		if len(aliases) > 0 {
			publicToPhysical[outputID] = aliases
		}
	}

	runtime := &explorer.ExplorerRuntimeV1{
		Generation:    firstNonEmpty(state.Dataset.Generation, state.SourceGeneration, state.Publication.Generation),
		Publication:   state.Publication,
		Schema:        explorer.ExplorerRuntimeSchemaV1{Digest: firstNonEmpty(state.Dataset.SchemaDigest, state.ResolvedSchemaDigest), Version: explorer.ConfigV2APIVersion},
		Outputs:       make([]explorer.ExplorerRuntimeOutputV1, 0, len(config.Views)),
		SharedFilters: map[string][]explorer.ExplorerRuntimeBindingV1{},
		Diagnostics:   append(make([]explorer.Diagnostic, 0, len(state.Diagnostics)), state.Diagnostics...),
	}
	emissionByOutputColumn := make(map[string]map[string]string)
	// Seed the authoritative output/column identity map before walking views.
	// Shared filters are allowed to target a valid emitted column that is not
	// repeated in a table, filter, chart, or fixed-filter presentation binding.
	// Keeping this map complete prevents those bindings from disappearing from
	// the renderer projection.
	for outputID, columns := range physicalColumns {
		if emissionByOutputColumn[outputID] == nil {
			emissionByOutputColumn[outputID] = map[string]string{}
		}
		for name := range columns {
			emitted := emittedColumns[outputID][name]
			if emitted.EmissionID == "" {
				for publicName, physicalName := range publicToPhysical[outputID] {
					if physicalName == name {
						emitted = emittedColumns[outputID][publicName]
						break
					}
				}
			}
			emissionID := emitted.EmissionID
			if emissionID == "" {
				emissionID = explorer.OpaqueID("em_", outputID+"\x00"+name)
			}
			emissionByOutputColumn[outputID][name] = emissionID
		}
	}
	for _, view := range config.Views {
		physicalNameFor := func(name string) string {
			if aliases := publicToPhysical[view.Output]; aliases != nil {
				if physicalName := aliases[name]; physicalName != "" {
					return physicalName
				}
			}
			return name
		}
		selector := (*dataset.DataframeSelector)(nil)
		if output, ok := datasetOutputs[view.Output]; ok && output.Selector != nil {
			copy := *output.Selector
			selector = &copy
		} else if materialization, ok := materializations[view.Output]; ok && materialization.Selector != nil {
			copy := *materialization.Selector
			selector = &copy
		}
		if selector == nil || selector.Validate() != nil {
			return nil
		}

		columnsByName := map[string]explorer.ExplorerRuntimeColumnV1{}
		ensureColumn := func(name string) (explorer.ExplorerRuntimeColumnV1, bool) {
			physicalName := physicalNameFor(name)
			if column, ok := columnsByName[physicalName]; ok {
				return column, true
			}
			physical, ok := physicalColumns[view.Output][physicalName]
			if !ok {
				// A compiled authoring emission is intent metadata. It is not
				// queryable until the active materialization publishes the same
				// physical column. Do not leak such a column into the runtime
				// packet: the dataframe API correctly rejects it otherwise.
				return explorer.ExplorerRuntimeColumnV1{}, false
			}
			emitted := emittedColumns[view.Output][name]
			if emitted.PublicColumn == "" {
				emitted = emittedColumns[view.Output][physicalName]
			}
			emissionID := emitted.EmissionID
			if emissionID == "" {
				emissionID = explorer.OpaqueID("em_", view.Output+"\x00"+name)
			}
			logicalType := firstNonEmpty(emitted.LogicalType, physical.LogicalType, physical.ClickHouse, "string")
			filterable := emitted.Filterable
			chartable := emitted.Chartable
			if emitted.PublicColumn == "" {
				filterable = !physical.Repeated
				chartable = !physical.Repeated
			}
			label := recipeLabels[view.Output][name]
			column := explorer.ExplorerRuntimeColumnV1{
				EmissionID: emissionID, Name: physicalName, Label: firstNonEmpty(label, physicalName), LogicalType: logicalType,
				Repeated: physical.Repeated, Filterable: filterable, Sortable: !physical.Repeated,
				Chartable: chartable, Aggregatable: filterable,
			}
			columnsByName[physicalName] = column
			if emissionByOutputColumn[view.Output] == nil {
				emissionByOutputColumn[view.Output] = map[string]string{}
			}
			emissionByOutputColumn[view.Output][physicalName] = emissionID
			return column, true
		}

		orderedNames := make([]string, 0, len(physicalColumns[view.Output])+len(emittedColumns[view.Output]))
		seenNames := map[string]bool{}
		appendName := func(name string) {
			logicalName := name
			physicalName := physicalNameFor(name)
			if physicalName == "" || seenNames[physicalName] {
				return
			}
			if _, ok := ensureColumn(logicalName); !ok {
				return
			}
			seenNames[physicalName] = true
			orderedNames = append(orderedNames, physicalName)
		}
		for _, binding := range view.Table.Columns {
			appendName(binding.Column)
		}
		remainingNames := make([]string, 0, len(physicalColumns[view.Output])+len(emittedColumns[view.Output]))
		for name := range physicalColumns[view.Output] {
			if !seenNames[name] {
				remainingNames = append(remainingNames, name)
			}
		}
		for name := range emittedColumns[view.Output] {
			if !seenNames[name] {
				remainingNames = append(remainingNames, name)
			}
		}
		sort.Strings(remainingNames)
		for _, name := range remainingNames {
			appendName(name)
		}

		table := explorer.ExplorerRuntimeTableV1{Columns: make([]explorer.ExplorerRuntimeTableColumnV1, 0, len(view.Table.Columns))}
		for index, binding := range view.Table.Columns {
			column, ok := ensureColumn(binding.Column)
			if !ok {
				continue
			}
			label := binding.Label
			if label == "" || label == binding.Column || label == physicalNameFor(binding.Column) {
				label = recipeLabels[view.Output][binding.Column]
			}
			column.Label = firstNonEmpty(label, column.Label, binding.Column)
			column.Visible = binding.Visible
			column.Order = index
			columnsByName[physicalNameFor(binding.Column)] = column
			table.Columns = append(table.Columns, explorer.ExplorerRuntimeTableColumnV1{EmissionID: column.EmissionID, Visible: binding.Visible})
		}
		filters := make([]explorer.ExplorerRuntimeBindingV1, 0, len(view.Filters))
		for _, binding := range view.Filters {
			column, ok := ensureColumn(binding.Column)
			if !ok {
				continue
			}
			filters = append(filters, explorer.ExplorerRuntimeBindingV1{EmissionID: column.EmissionID, OutputID: view.Output, Label: firstNonEmpty(binding.Label, column.Label)})
		}
		charts := make([]explorer.ExplorerRuntimeBindingV1, 0, len(view.Charts))
		for _, binding := range view.Charts {
			column, ok := ensureColumn(binding.Column)
			if !ok {
				continue
			}
			charts = append(charts, explorer.ExplorerRuntimeBindingV1{EmissionID: column.EmissionID, OutputID: view.Output, Type: binding.Type, Title: binding.Title})
		}
		fixedFilters := make(map[string][]string, len(view.FixedFilters))
		for columnName, values := range view.FixedFilters {
			column, ok := ensureColumn(columnName)
			if !ok {
				continue
			}
			fixedFilters[column.EmissionID] = append([]string(nil), values...)
		}
		columns := make([]explorer.ExplorerRuntimeColumnV1, 0, len(orderedNames))
		for index, name := range orderedNames {
			column := columnsByName[name]
			if index >= len(view.Table.Columns) {
				column.Order = index
			}
			columns = append(columns, column)
		}
		// A migrated legacy view can legitimately have no surviving presentation
		// bindings (for example when its old field names no longer match the
		// published physical schema). Keep the renderer usable in that case by
		// projecting every published column into the table. Explicit table
		// bindings still win, including an intentionally hidden table.
		if len(view.Table.Columns) == 0 {
			table.Columns = make([]explorer.ExplorerRuntimeTableColumnV1, 0, len(columns))
			order := 0
			for index := range columns {
				if runtimeColumnIsInternal(columns[index].Name) {
					continue
				}
				columns[index].Visible = true
				columns[index].Order = order
				table.Columns = append(table.Columns, explorer.ExplorerRuntimeTableColumnV1{EmissionID: columns[index].EmissionID, Visible: true})
				order++
			}
		}
		output := explorer.ExplorerRuntimeOutputV1{
			OutputID: view.Output, Name: firstNonEmpty(view.ID, view.Output), Title: view.Title,
			RowLabel: firstNonEmpty(view.RowLabel, view.Title), Selector: *selector, Columns: columns,
			Table: table, Filters: filters, Charts: charts, FixedFilters: fixedFilters,
		}
		if materialization, ok := materializations[view.Output]; ok {
			copy := materialization
			output.Materialization = &copy
		}
		runtime.Outputs = append(runtime.Outputs, output)
	}
	for name, filters := range config.SharedFilters {
		bindings := make([]explorer.ExplorerRuntimeBindingV1, 0, len(filters))
		for _, filter := range filters {
			columnName := filter.Column
			if aliases := publicToPhysical[filter.Output]; aliases != nil {
				if physicalName := aliases[columnName]; physicalName != "" {
					columnName = physicalName
				}
			}
			emissionID := emissionByOutputColumn[filter.Output][columnName]
			if emissionID == "" {
				continue
			}
			bindings = append(bindings, explorer.ExplorerRuntimeBindingV1{EmissionID: emissionID, OutputID: filter.Output})
		}
		if len(bindings) > 0 {
			runtime.SharedFilters[name] = bindings
		}
	}
	return runtime
}

func runtimeColumnIsInternal(name string) bool {
	return name == "auth_resource_path" || strings.HasPrefix(name, "__loom_")
}

func explorerRecipeColumnLabels(raw json.RawMessage) map[string]map[string]string {
	labels := map[string]map[string]string{}
	var bundle recipe.Bundle
	if len(raw) == 0 || json.Unmarshal(raw, &bundle) != nil {
		return labels
	}
	var collectFields func(string, []recipe.Field, []recipe.Traversal)
	collectFields = func(output string, fields []recipe.Field, traversals []recipe.Traversal) {
		if labels[output] == nil {
			labels[output] = map[string]string{}
		}
		for _, field := range fields {
			if field.Name == "" || field.FieldRef == "" {
				continue
			}
			labels[output][field.Name] = humanizeExplorerFieldRef(field.FieldRef)
		}
		for _, traversal := range traversals {
			collectFields(output, traversal.Fields, traversal.Traversals)
		}
	}
	for _, output := range bundle.Outputs {
		collectFields(output.Name, output.Fields, output.Traversals)
	}
	return labels
}

func humanizeExplorerFieldRef(fieldRef string) string {
	value := strings.TrimSpace(fieldRef)
	if index := strings.IndexByte(value, '.'); index >= 0 && index+1 < len(value) {
		value = value[index+1:]
	}
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		word := current.String()
		lower := strings.ToLower(word)
		switch lower {
		case "id", "url", "uri", "uuid":
			word = strings.ToUpper(lower)
		default:
			word = strings.ToUpper(word[:1]) + word[1:]
		}
		words = append(words, word)
		current.Reset()
	}
	for index, char := range value {
		if char == '.' || char == '_' || char == '-' || char == '[' || char == ']' {
			flush()
			continue
		}
		if index > 0 && char >= 'A' && char <= 'Z' && current.Len() > 0 {
			flush()
		}
		current.WriteRune(char)
	}
	flush()
	if len(words) == 0 {
		return fieldRef
	}
	return strings.Join(words, " ")
}

func canonicalAuthoringBundle(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	bundle, err := explorer.DecodeAuthoringBundleV1ForMigration(raw)
	if err != nil {
		return nil
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return nil
	}
	return canonical
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

func configV2Title(raw json.RawMessage) string {
	var cfg explorer.ConfigV2
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "Default"
	}
	return cfg.Explorer.Title
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
