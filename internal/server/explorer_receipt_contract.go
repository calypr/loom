package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"

	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

func compileConstructionSourceStage(ctx context.Context, request lifecycle.ConstructionSourceStageRequest, recipeEngine *dataframeexecution.Engine, importedSources ...lifecycle.ConstructionSourceStageCompiler) (explorer.ReceiptConstructionStage, error) {
	if recipeEngine == nil {
		return explorer.ReceiptConstructionStage{}, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "construction-capabilities", Code: "CAPABILITY_UNAVAILABLE", Message: "the construction source compiler is not configured"}
	}
	if request.Document.Output.ID != request.OutputID {
		return explorer.ReceiptConstructionStage{}, fmt.Errorf("source-stage document output does not match requested output")
	}
	if request.Document.ImportedRecipe != nil {
		if len(importedSources) == 0 {
			return explorer.ReceiptConstructionStage{}, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "construction-capabilities", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "imported dataframe source resolution is unavailable"}
		}
		return importedSources[0](ctx, request)
	}
	document := request.Document
	document.Construction = nil
	document.ArchiveRetiredShape = nil
	translated, err := explorercompilation.Compile(ctx, request.Project, request.ExplorerID, document, request.Authorized.Snapshot)
	if err != nil {
		return explorer.ReceiptConstructionStage{}, fmt.Errorf("compile source projection: %w", err)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration:          request.Authorized.Snapshot.Identity.Generation,
		AuthResourcePaths:          append([]string(nil), request.Authorized.Scope.AuthResourcePaths...),
		AuthScopeMode:              request.Authorized.Scope.Mode,
		SelectionMembersCollection: request.SelectionMembersCollection,
		OutputNames:                []string{request.OutputID},
	}
	resolved, err := recipeEngine.CompileResolvedBundle(ctx, translated.Bundle, bindings)
	if err != nil {
		return explorer.ReceiptConstructionStage{}, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "construction-capabilities", Code: "CAPABILITY_UNAVAILABLE", Message: "the source projection could not be compiled", Cause: err}
	}
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != request.OutputID {
			continue
		}
		descriptor, err := lower.DescribeConstructionSourceStage(output.OutputSchema, document.RootResourceType, &output.Plan)
		if err != nil {
			return explorer.ReceiptConstructionStage{}, err
		}
		return explorercompilation.ReceiptConstructionStageFromDescriptor(descriptor)
	}
	return explorer.ReceiptConstructionStage{}, fmt.Errorf("source projection compiler returned no output %q", request.OutputID)
}

func receiptContractFailure(err error) error {
	var missing *constructionInputMissingError
	if errors.As(err, &missing) {
		message := fmt.Sprintf("the compiled receipt references construction input column %q, but that column is absent from the prior stage; the authored construction and compiler schema disagree", missing.ColumnID)
		if missing.StepID != "" || missing.OutputName != "" {
			message = fmt.Sprintf("construction step %q output %q references input column %q, but that column is absent from the prior stage; the authored construction and compiler schema disagree", missing.StepID, missing.OutputName, missing.ColumnID)
		}
		return &explorer.AuthoringError{
			Status: http.StatusInternalServerError,
			Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR",
				Stage:    "reconcile_receipt",
				Code:     "CONSTRUCTION_RECEIPT_INPUT_MISSING",
				Message:  message,
				Details:  map[string]any{"inputColumnId": missing.ColumnID, "stepId": missing.StepID, "outputName": missing.OutputName},
			},
			Cause: err,
		}
	}
	return &explorer.AuthoringError{
		Status: http.StatusInternalServerError,
		Diagnostic: explorer.AuthoringDiagnostic{
			Severity: "ERROR",
			Stage:    "reconcile_receipt",
			Code:     "CONSTRUCTION_RECEIPT_CONTRACT_INVALID",
			Message:  "the compiled receipt metadata is inconsistent with the authored construction; inspect the server log for the failing document, step, and output",
			Details:  map[string]any{"causeType": fmt.Sprintf("%T", err)},
		},
		Cause: err,
	}
}

type importedRecipeResolver interface {
	Resolve(context.Context, string, authoringv2.Workspace, lifecycle.AuthorizedCapability) ([]explorercompilation.ResolvedImportedRecipe, error)
}

func compileExplorerReceipt(ctx context.Context, request lifecycle.CompileReceiptRequest, capabilityResolver *capabilityresolver.Resolver, recipeEngine *dataframeexecution.Engine, explorerService *explorer.Service, logger *slog.Logger, combineResolver combineInputSchemaResolver, importedSources ...importedRecipeResolver) (result *explorer.CompilationReceipt, retErr error) {
	started := time.Now()
	var outputIDs, stageIDs, columnIDs []string
	var prepareDuration, compileWorkspaceDuration, compileResolvedDuration, contractBuildDuration, validatePersistDuration time.Duration
	var receiptBytes, outputCount, columnCount int
	var receiptID string
	defer func() {
		level := slog.LevelInfo
		if retErr != nil {
			level = slog.LevelError
		}
		httpapi.LogServerDiagnostic(logger, level, "Explorer receipt compilation", httpapi.RequestIDFromContext(ctx), "compile_receipt", time.Since(started), retErr,
			"project", request.Project, "explorer_id", request.ExplorerID, "receipt_id", receiptID,
			"compile_role", request.RequestID, "output_ids", outputIDs, "stage_ids", stageIDs, "output_column_ids", columnIDs,
			"prepare_ms", prepareDuration.Milliseconds(), "compile_workspace_ms", compileWorkspaceDuration.Milliseconds(),
			"compile_resolved_bundle_ms", compileResolvedDuration.Milliseconds(), "contract_build_ms", contractBuildDuration.Milliseconds(),
			"validate_persist_ms", validatePersistDuration.Milliseconds(), "receipt_bytes", receiptBytes,
			"output_count", outputCount, "column_count", columnCount)
	}()
	authorized := request.Authorized.Clone()
	if strings.TrimSpace(authorized.Snapshot.Token) == "" {
		var err error
		authorized, err = capabilityResolver.ResolveForCompilation(ctx, request.Project, request.SnapshotToken)
		if err != nil {
			var typed *lifecycle.Error
			if errors.As(err, &typed) {
				return nil, typed
			}
			return nil, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "compile", Code: "CAPABILITY_UNAVAILABLE", Message: "the capability snapshot could not be resolved", Cause: err}
		}
	} else if authorized.Snapshot.Identity.Project != projectid.Canonical(request.Project) || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return nil, capability.ErrStaleSnapshot
	}
	snapshot := authorized.Snapshot
	if err := capabilityresolver.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return nil, capability.ErrStaleSnapshot
	}
	prepareStarted := time.Now()
	catalog := authoringv2.CatalogFromCapability(snapshot, request.ExplorerID)
	workspace, err := authoringv2.PrepareWorkspaceForCompilation(request.Workspace, catalog)
	if err != nil {
		return nil, &explorercompilation.Error{
			Stage: "intent", Code: "LEGACY_CONTRIBUTOR_MIGRATION_FAILED", Path: "$.workspace",
			Message: err.Error(), Cause: err,
		}
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	prepareDuration = time.Since(prepareStarted)
	compileWorkspaceStarted := time.Now()
	if len(importedSources) > 0 {
		sources, resolveErr := importedSources[0].Resolve(ctx, request.Project, workspace, authorized)
		if resolveErr != nil {
			var typed *lifecycle.Error
			if errors.As(resolveErr, &typed) {
				return nil, typed
			}
			return nil, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "compile", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "imported recipe sources could not be resolved", Cause: resolveErr}
		}
		request.ResolvedInputs.ImportedRecipes = sources
	}
	translated, err := explorercompilation.CompileWorkspace(ctx, request.Project, request.ExplorerID, workspace, snapshot, request.ResolvedInputs)
	if err != nil {
		return nil, err
	}
	compileWorkspaceDuration = time.Since(compileWorkspaceStarted)
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project), DatasetGeneration: snapshot.Identity.Generation, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode, SelectionMembersCollection: request.SelectionMembersCollection}
	compileResolvedStarted := time.Now()
	resolved, err := recipeEngine.CompileResolvedBundle(ctx, translated.Bundle, bindings)
	if err != nil {
		return nil, classifyReceiptRecipeError(err)
	}
	for _, output := range resolved.Compiled.Outputs {
		outputIDs = append(outputIDs, output.Name)
		for _, column := range output.OutputSchema {
			columnIDs = append(columnIDs, column.Name)
		}
		for _, stage := range output.Stages {
			stageIDs = append(stageIDs, stage.ID)
		}
	}
	compileResolvedDuration = time.Since(compileResolvedStarted)
	combineInputSchemas := make(map[string]map[int]ir.ResolvedClickHouseTable)
	for _, output := range resolved.Compiled.Outputs {
		combine := output.Plan.ClickHouseCombine
		if combine == nil {
			continue
		}
		hasPublishedInputs := false
		for _, input := range combine.Inputs {
			if input.WorkspaceOutputID == "" {
				hasPublishedInputs = true
				break
			}
		}
		if !hasPublishedInputs {
			continue
		}
		if combineResolver == nil {
			return nil, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "compile", Code: "COMBINE_INPUT_UNAVAILABLE", Message: "published Combine inputs are not configured"}
		}
		inputSchemas, err := combineResolver(ctx, output, bindings)
		if err != nil {
			var typed *lifecycle.Error
			if errors.As(err, &typed) {
				return nil, typed
			}
			return nil, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "compile", Code: "COMBINE_INPUT_UNAVAILABLE", Message: fmt.Sprintf("published Combine inputs for output %q could not be resolved", output.Name), Cause: err}
		}
		combineInputSchemas[output.Name] = inputSchemas
	}
	contractBuildStarted := time.Now()
	translated, err = reconcileFinalOutputMetadataWithCombineInputs(translated, resolved, combineInputSchemas, snapshot)
	if err != nil {
		return nil, receiptContractFailure(err)
	}
	contract, err := json.Marshal(explorer.PublicOutputContracts{Outputs: translated.OutputContracts})
	if err != nil {
		return nil, err
	}
	contractDigest, err := explorer.CompilationArtifactDigest(contract)
	if err != nil {
		return nil, err
	}
	resolvedRecipeDigest, err := resolved.Bundle.Digest()
	if err != nil {
		return nil, err
	}
	compiledConfig, err := compiledExplorerWorkspaceConfigV2(request.Project, request.ExplorerID, translated)
	if err != nil {
		return nil, err
	}
	fingerprints, columnProvenance, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		return nil, fmt.Errorf("build receipt execution contract: %w", err)
	}
	constructionStages, err := receiptConstructionStages(&resolved)
	if err != nil {
		return nil, fmt.Errorf("build receipt construction stages: %w", err)
	}
	compiledOutputSchemas, err := receiptCompiledOutputSchemas(&resolved)
	if err != nil {
		return nil, fmt.Errorf("build receipt compiled output schemas: %w", err)
	}
	var rowDefinitionProposal *explorer.RowDefinitionProposalBinding
	if request.RowDefinitionProposal != nil {
		binding := *request.RowDefinitionProposal
		rowDefinitionProposal = &binding
	}
	var constructionProposal *explorer.ConstructionProposalBinding
	if request.ConstructionProposal != nil {
		binding := *request.ConstructionProposal
		constructionProposal = &binding
	}
	var populationMemberRemovalProposal *explorer.PopulationMemberRemovalProposalBinding
	if request.PopulationMemberRemovalProposal != nil {
		binding := *request.PopulationMemberRemovalProposal
		populationMemberRemovalProposal = &binding
	}
	receipt := explorer.CompilationReceipt{ReceiptFormatVersion: explorer.CurrentReceiptFormatVersion, CompilerContractVersion: explorer.CurrentCompilerContractVersion, Project: projectid.Canonical(request.Project), ExplorerID: request.ExplorerID, IntentDigest: intentDigest, ResolvedInputsDigest: translated.ResolvedInputsDigest, ResolvedInterpretations: append([]explorer.ResolvedInterpretation(nil), request.ResolvedInputs.Interpretations...), SnapshotToken: request.SnapshotToken, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest, ShapeDigest: snapshot.Identity.ShapeDigest, SourceGeneration: snapshot.Identity.Generation, RecipeDigest: resolved.StoredRecipeDigest, ResolvedRecipeDigest: resolvedRecipeDigest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, OutputContractDigest: contractDigest, NormalizedBundle: normalized, Bundle: resolved.Bundle, CompiledConfig: compiledConfig, PublicOutputContract: contract, IdentityMappings: translated.IdentityMappings, EmittedColumns: translated.EmittedColumns, OutputFingerprints: fingerprints, OutputColumnProvenance: columnProvenance, CompiledOutputSchemas: compiledOutputSchemas, ColumnPolicyProposal: cloneColumnPolicyBinding(request.ColumnPolicyProposal), SourceRelationshipProposal: request.SourceRelationshipProposal, RowDefinitionProposal: rowDefinitionProposal, ConstructionProposal: constructionProposal, PopulationMemberRemovalProposal: populationMemberRemovalProposal, ConstructionStages: constructionStages, RequestID: request.RequestID, CreatedAt: time.Now().UTC()}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		return nil, err
	}
	receiptID = receipt.ID
	outputCount = len(receipt.Bundle.Outputs)
	columnCount = len(receipt.EmittedColumns)
	contractBuildDuration = time.Since(contractBuildStarted)
	validatePersistStarted := time.Now()
	if err := validateReceiptResolution(&receipt, &resolved); err != nil {
		return nil, receiptCompilationConflict(receipt.ID, err)
	}
	if err := validateReceiptEnginePublicColumns(&receipt, resolved); err != nil {
		return nil, receiptCompilationConflict(receipt.ID, contractMismatch("public_columns", "", "receipt public columns", err.Error()))
	}
	stored, err := explorerService.StoreCompilationReceipt(ctx, receipt)
	if err != nil {
		if errors.Is(err, explorer.ErrCorruptReceipt) {
			return nil, err
		}
		return nil, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "compile", Code: "RECEIPT_STORE_UNAVAILABLE", Message: "the compilation receipt could not be stored", Cause: err}
	}
	validatePersistDuration = time.Since(validatePersistStarted)
	if raw, marshalErr := json.Marshal(stored); marshalErr == nil {
		receiptBytes = len(raw)
	}
	return stored, nil
}

func classifyReceiptRecipeError(err error) error {
	var invalidAnchor *lower.RelatedEligibilityAnchorError
	if errors.As(err, &invalidAnchor) {
		return &explorercompilation.Error{
			Stage: "construction", Code: "CONSTRUCTION_ANCHOR_INVALID",
			Message: "A related-record step no longer has a valid row anchor. Edit or remove the affected step before applying this change.",
			Details: map[string]any{"stepId": invalidAnchor.StepID}, Cause: err,
		}
	}
	var invalidRelatedSourceAnchor *lower.RelatedSourceAnchorError
	if errors.As(err, &invalidRelatedSourceAnchor) {
		message := "This related source does not match the current table rows. Reopen the step and choose a source for this table."
		if invalidRelatedSourceAnchor.Reason == "root_resource_changed" {
			message = "This related source belongs to a different table root. Choose a source for this table."
		}
		details := map[string]any{"stepId": invalidRelatedSourceAnchor.StepID, "reason": invalidRelatedSourceAnchor.Reason}
		if invalidRelatedSourceAnchor.OutputID != "" {
			details["outputId"] = invalidRelatedSourceAnchor.OutputID
		}
		return &explorercompilation.Error{
			Stage: "construction", Code: "CONSTRUCTION_ANCHOR_INVALID",
			Message: message, Details: details, Cause: err,
		}
	}
	var reducerType *lower.PivotReducerTypeError
	if errors.As(err, &reducerType) {
		return &explorercompilation.Error{
			Stage: "construction", Code: "PIVOT_REDUCER_REQUIRES_NUMERIC",
			Message: "This Pivot duplicate rule needs a numeric value field. Choose Keep the smallest, Keep the largest, or Stop with an error.",
			Details: map[string]any{"duplicatePolicy": reducerType.Policy, "valueColumn": reducerType.ValueColumn}, Cause: err,
		}
	}
	return err
}

func persistValidatedReceipt(ctx context.Context, recipeEngine *dataframeexecution.Engine, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, persist func(context.Context, explorer.CompilationReceipt) (*explorer.CompilationReceipt, error)) (*explorer.CompilationReceipt, error) {
	if _, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings); err != nil {
		id := ""
		if receipt != nil {
			id = receipt.ID
		}
		return nil, receiptCompilationConflict(id, err)
	}
	return persist(ctx, *receipt)
}

type receiptContractMismatch struct {
	Component string
	OutputID  string
	Expected  string
	Actual    string
}

func (e *receiptContractMismatch) Error() string {
	if e.OutputID != "" {
		return fmt.Sprintf("receipt %s mismatch for output %q: expected %q, got %q", e.Component, e.OutputID, e.Expected, e.Actual)
	}
	return fmt.Sprintf("receipt %s mismatch: expected %q, got %q", e.Component, e.Expected, e.Actual)
}

func contractMismatch(component, output, expected, actual string) error {
	return &receiptContractMismatch{Component: component, OutputID: output, Expected: expected, Actual: actual}
}

func receiptMismatchDetails(receiptID string, err error) map[string]any {
	details := map[string]any{"component": "output_execution"}
	if strings.TrimSpace(receiptID) != "" {
		details["receiptId"] = receiptID
	}
	var mismatch *receiptContractMismatch
	if errors.As(err, &mismatch) {
		details["component"] = mismatch.Component
		if mismatch.OutputID != "" {
			details["outputId"] = mismatch.OutputID
		}
	}
	return details
}

func receiptCompilationConflict(receiptID string, cause error) error {
	value := explorer.ExplorerConflict("compile", "COMPILATION_CONTRACT_MISMATCH", "the compiler could not reproduce the stored receipt execution contract", receiptMismatchDetails(receiptID, cause))
	if authoring, ok := value.(*explorer.AuthoringError); ok {
		authoring.Cause = cause
	}
	return value
}

func compiledExplorerWorkspaceConfigV2(project, explorerID string, compiled explorercompilation.WorkspaceResult) ([]byte, error) {
	if len(compiled.EmittedColumns) == 0 {
		return nil, nil
	}
	recipeJSON, err := json.Marshal(compiled.Bundle)
	if err != nil {
		return nil, err
	}
	byOutput := map[string]explorercompilation.PresentationConfig{}
	for _, presentation := range compiled.Presentations {
		byOutput[presentation.OutputID] = presentation
	}
	tabs := append([]authoringv2.Tab(nil), compiled.Workspace.Tabs...)
	sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].Order < tabs[j].Order })
	views := make([]explorer.ConfigView, 0, len(tabs))
	for _, tab := range tabs {
		if !tab.Visible {
			continue
		}
		presentation, ok := byOutput[tab.OutputID]
		if !ok {
			return nil, fmt.Errorf("missing compiled output %q", tab.OutputID)
		}
		columns := append([]explorercompilation.PresentationColumn(nil), presentation.Columns...)
		sort.SliceStable(columns, func(i, j int) bool {
			if columns[i].Order != columns[j].Order {
				return columns[i].Order < columns[j].Order
			}
			return columns[i].PhysicalOrder < columns[j].PhysicalOrder
		})
		document, found := semanticWorkspaceDocument(compiled.Workspace, tab.OutputID)
		view := explorer.ConfigView{ID: tab.ID, Title: tab.Title, Output: tab.OutputID, Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{}}}
		if found {
			view.RowLabel = document.Output.RowLabel
		}
		for _, column := range columns {
			cellRenderer := ""
			if authored, ok := semanticWorkspaceColumn(document, column.PublicColumn); ok && authored.Table != nil {
				cellRenderer = authored.Table.CellRenderer
			}
			view.Table.Columns = append(view.Table.Columns, explorer.ConfigColumn{Column: column.PublicColumn, Label: column.Label, Visible: column.Visible, Pinned: column.Pinned, CellRenderer: cellRenderer})
		}
		filterColumns := append([]explorercompilation.PresentationColumn(nil), presentation.Columns...)
		sort.SliceStable(filterColumns, func(i, j int) bool {
			return filterColumns[i].FilterOrder < filterColumns[j].FilterOrder
		})
		for _, column := range filterColumns {
			if column.FilterLabel != "" {
				view.Filters = append(view.Filters, explorer.ConfigFilter{Column: column.PublicColumn, Label: column.FilterLabel})
			}
		}
		chartColumns := append([]explorercompilation.PresentationColumn(nil), presentation.Columns...)
		sort.SliceStable(chartColumns, func(i, j int) bool {
			return chartColumns[i].ChartOrder < chartColumns[j].ChartOrder
		})
		for _, column := range chartColumns {
			if column.ChartType != "" {
				view.Charts = append(view.Charts, explorer.ConfigChart{Column: column.PublicColumn, Type: column.ChartType, Title: column.ChartTitle})
			}
		}
		if found {
			for _, fixed := range document.FixedFilters {
				if view.FixedFilters == nil {
					view.FixedFilters = map[string][]string{}
				}
				view.FixedFilters[fixed.Column] = append([]string(nil), fixed.Values...)
			}
			for _, action := range document.Actions {
				compiledAction := explorer.ConfigAction{Type: action.Type, Title: action.Title, FileName: action.FileName, Output: document.Output.ID}
				for _, binding := range action.Columns {
					compiledAction.Columns = append(compiledAction.Columns, binding.Column)
					if binding.ExportHeader != "" {
						if compiledAction.ExportHeaders == nil {
							compiledAction.ExportHeaders = map[string]string{}
						}
						compiledAction.ExportHeaders[binding.Column] = binding.ExportHeader
					}
				}
				view.Actions = append(view.Actions, compiledAction)
			}
		}
		views = append(views, view)
	}
	title := firstNonEmptyWorkspaceTitle(compiled.Workspace.Explorer.Title, explorerID)
	if len(tabs) > 0 {
		title = firstNonEmptyWorkspaceTitle(compiled.Workspace.Explorer.Title, tabs[0].Title, explorerID)
	}
	shared := map[string][]explorer.SharedFilter{}
	for name, bindings := range compiled.Workspace.SharedFilters {
		for _, binding := range bindings {
			shared[name] = append(shared[name], explorer.SharedFilter{Output: binding.OutputID, Column: binding.Column})
		}
	}
	fileActions := explorer.FileActions{}
	if compiled.Workspace.FileActions != nil {
		fileActions.Extensions = compiled.Workspace.FileActions.Extensions
		fileActions.Actions = compiled.Workspace.FileActions.Actions
	}
	config := explorer.ConfigV2{APIVersion: explorer.ConfigV2APIVersion, Kind: "ExplorerConfig", Project: projectid.Canonical(project), Explorer: explorer.ConfigExplorer{ID: explorerID, Title: title, Description: compiled.Workspace.Explorer.Description, Management: explorer.ConfigManagementForID(explorerID)}, Recipe: recipeJSON, Views: views, SharedFilters: shared, FileActions: fileActions}
	return json.Marshal(config)
}

func semanticWorkspaceDocument(workspace authoringv2.Workspace, outputID string) (authoringv2.Document, bool) {
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID {
			return document, true
		}
	}
	return authoringv2.Document{}, false
}

func semanticWorkspaceColumn(document authoringv2.Document, column string) (authoringv2.Column, bool) {
	for _, authored := range document.Columns {
		if authored.Column == column {
			return authored, true
		}
	}
	return authoringv2.Column{}, false
}

func firstNonEmptyWorkspaceTitle(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Explorer"
}

func validateReceiptResolution(receipt *explorer.CompilationReceipt, resolved *dataframeexecution.Resolved) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if resolved == nil {
		return fmt.Errorf("resolved compilation is required")
	}
	digest, err := resolved.Bundle.Digest()
	if err != nil {
		return err
	}
	if digest != receipt.ResolvedRecipeDigest {
		return contractMismatch("recipe", "", receipt.ResolvedRecipeDigest, digest)
	}
	if resolved.StoredRecipeDigest != receipt.RecipeDigest {
		return contractMismatch("recipe", "", receipt.RecipeDigest, resolved.StoredRecipeDigest)
	}
	if resolved.ResolvedSchemaDigest != receipt.ResolvedSchemaDigest {
		return contractMismatch("schema", "", receipt.ResolvedSchemaDigest, resolved.ResolvedSchemaDigest)
	}
	want, _, err := resolvedOutputArtifacts(*resolved)
	if err != nil {
		return err
	}
	if len(want) != len(receipt.OutputFingerprints) {
		return contractMismatch("output_set", "", fmt.Sprint(len(receipt.OutputFingerprints)), fmt.Sprint(len(want)))
	}
	for output, fingerprint := range want {
		if strings.TrimSpace(receipt.OutputFingerprints[output]) != fingerprint {
			return contractMismatch("output_execution", output, receipt.OutputFingerprints[output], fingerprint)
		}
	}
	constructionStages, err := receiptConstructionStages(resolved)
	if err != nil {
		return contractMismatch("construction_stages", "", "valid compiler stages", err.Error())
	}
	if !reflect.DeepEqual(receipt.ConstructionStages, constructionStages) {
		return contractMismatch("construction_stages", "", "compiler-derived stage descriptors", "receipt stage descriptors differ")
	}
	compiledOutputSchemas, err := receiptCompiledOutputSchemas(resolved)
	if err != nil {
		return contractMismatch("compiled_output_schemas", "", "valid finalized output schemas", err.Error())
	}
	if len(receipt.CompiledOutputSchemas) > 0 && !reflect.DeepEqual(receipt.CompiledOutputSchemas, compiledOutputSchemas) {
		return contractMismatch("compiled_output_schemas", "", "compiler-derived finalized output schemas", "receipt output schemas differ")
	}
	if len(receipt.OutputColumnProvenance) != len(resolved.Compiled.Outputs) {
		return contractMismatch("provenance", "", fmt.Sprint(len(resolved.Compiled.Outputs)), fmt.Sprint(len(receipt.OutputColumnProvenance)))
	}
	for index := range resolved.Compiled.Outputs {
		output := &resolved.Compiled.Outputs[index]
		values, ok := receipt.OutputColumnProvenance[output.Name]
		if !ok {
			return contractMismatch("provenance", output.Name, "present", "missing")
		}
		if err := applyReceiptColumnProvenance(output, values); err != nil {
			return contractMismatch("provenance", output.Name, "complete", err.Error())
		}
	}
	return nil
}

func receiptCompiledOutputSchemas(resolved *dataframeexecution.Resolved) (map[string][]explorer.ReceiptCompiledOutputColumn, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved compilation is required")
	}
	schemas := make(map[string][]explorer.ReceiptCompiledOutputColumn, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return nil, fmt.Errorf("compiled output has an empty name")
		}
		if _, duplicate := schemas[output.Name]; duplicate {
			return nil, fmt.Errorf("compiled output %q appears more than once", output.Name)
		}
		columns := make([]explorer.ReceiptCompiledOutputColumn, 0, len(output.OutputSchema))
		for _, column := range output.OutputSchema {
			columns = append(columns, explorer.ReceiptCompiledOutputColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, LogicalType: column.Kind,
				Cardinality: column.Cardinality, Nullable: column.Nullable, Internal: column.Internal, Identity: column.Identity,
			})
		}
		schemas[output.Name] = columns
	}
	if len(schemas) == 0 {
		return nil, fmt.Errorf("compiled output schema set is empty")
	}
	return schemas, nil
}

func receiptConstructionStages(resolved *dataframeexecution.Resolved) (map[string][]explorer.ReceiptConstructionStage, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved compilation is required")
	}
	var stagesByOutput map[string][]explorer.ReceiptConstructionStage
	for _, output := range resolved.Compiled.Outputs {
		if len(output.Stages) == 0 {
			continue
		}
		if stagesByOutput == nil {
			stagesByOutput = make(map[string][]explorer.ReceiptConstructionStage)
		}
		if _, exists := stagesByOutput[output.Name]; exists {
			return nil, fmt.Errorf("compiled output %q has duplicate construction stage descriptors", output.Name)
		}
		stages := make([]explorer.ReceiptConstructionStage, 0, len(output.Stages))
		for index, descriptor := range output.Stages {
			stage, err := explorercompilation.ReceiptConstructionStageFromDescriptor(descriptor)
			if err != nil {
				return nil, err
			}
			if index == 0 && descriptor.ID == recipe.ConstructionSourceProjectionID {
				stage.Operation = ""
			}
			stages = append(stages, stage)
		}
		stagesByOutput[output.Name] = stages
	}
	return stagesByOutput, nil
}

func compileValidatedReceiptResolution(ctx context.Context, recipeEngine *dataframeexecution.Engine, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (dataframeexecution.Resolved, error) {
	validationBindings := bindings
	validationBindings.OutputNames = nil
	resolved, err := recipeEngine.CompileResolvedBundle(ctx, receipt.Bundle, validationBindings)
	if err != nil {
		return dataframeexecution.Resolved{}, err
	}
	if err := validateReceiptResolution(receipt, &resolved); err != nil {
		return dataframeexecution.Resolved{}, err
	}
	if err := validateReceiptEnginePublicColumns(receipt, resolved); err != nil {
		return dataframeexecution.Resolved{}, contractMismatch("public_columns", "", "receipt public columns", err.Error())
	}
	return resolved, nil
}

func cloneColumnPolicyBinding(binding *explorer.ColumnPolicyProposalBinding) *explorer.ColumnPolicyProposalBinding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}
