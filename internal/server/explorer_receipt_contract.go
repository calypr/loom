package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

// compileConstructionSourceStage runs the normal semantic and physical
// compiler for a source-only capability probe. The empty document never
// becomes a receipt or public output; only the compiler-derived source schema,
// row identity, and operation reasons are returned to lifecycle discovery.
func compileConstructionSourceStage(ctx context.Context, request lifecycle.ConstructionSourceStageRequest, recipeEngine *dataframeexecution.Engine) (explorer.ReceiptConstructionStage, error) {
	if recipeEngine == nil {
		return explorer.ReceiptConstructionStage{}, fmt.Errorf("recipe engine is required")
	}
	if request.Document.Output.ID != request.OutputID {
		return explorer.ReceiptConstructionStage{}, fmt.Errorf("source-stage document output does not match requested output")
	}
	document := request.Document
	document.Construction = nil
	document.TableShape = nil
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
		return explorer.ReceiptConstructionStage{}, fmt.Errorf("lower source projection: %w", err)
	}
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != request.OutputID {
			continue
		}
		descriptor, err := lower.DescribeConstructionSourceStage(output.OutputSchema, document.RootResourceType)
		if err != nil {
			return explorer.ReceiptConstructionStage{}, err
		}
		return receiptConstructionStageFromDescriptor(descriptor)
	}
	return explorer.ReceiptConstructionStage{}, fmt.Errorf("source projection compiler returned no output %q", request.OutputID)
}

func compileExplorerReceipt(ctx context.Context, request lifecycle.CompileReceiptRequest, capabilityResolver *explorerCapabilityResolver, recipeEngine *dataframeexecution.Engine, explorerService *explorer.Service, logger *slog.Logger) (*explorer.CompilationReceipt, error) {
	started := time.Now()
	authorized := request.Authorized.Clone()
	if strings.TrimSpace(authorized.Snapshot.Token) == "" {
		var err error
		authorized, err = capabilityResolver.ResolveForCompilation(ctx, request.Project, request.SnapshotToken)
		if err != nil {
			return nil, err
		}
	} else if authorized.Snapshot.Identity.Project != projectid.Canonical(request.Project) || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return nil, capability.ErrStaleSnapshot
	}
	snapshot := authorized.Snapshot
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return nil, capability.ErrStaleSnapshot
	}
	catalog := authoringV2Catalog(snapshot, request.ExplorerID)
	workspace, err := authoringv2.MigrateLegacyContributors(request.Workspace, catalog)
	if err != nil {
		return nil, fmt.Errorf("migrate legacy contributor predicates: %w", err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	intentDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	translated, err := explorercompilation.CompileWorkspace(ctx, request.Project, request.ExplorerID, workspace, snapshot, request.ResolvedInputs)
	if err != nil {
		return nil, err
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project), DatasetGeneration: snapshot.Identity.Generation, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode, SelectionMembersCollection: request.SelectionMembersCollection}
	resolved, err := recipeEngine.CompileResolvedBundle(ctx, translated.Bundle, bindings)
	if err != nil {
		return nil, classifyReceiptRecipeError(err)
	}
	translated, err = reconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		return nil, fmt.Errorf("reconcile receipt output metadata: %w", err)
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
	var rowDefinitionProposal *explorer.RowDefinitionProposalBinding
	if request.RowDefinitionProposal != nil {
		binding := *request.RowDefinitionProposal
		rowDefinitionProposal = &binding
	}
	var tableShapeProposal *explorer.TableShapeProposalBinding
	if request.TableShapeProposal != nil {
		binding := *request.TableShapeProposal
		tableShapeProposal = &binding
	}
	var constructionProposal *explorer.ConstructionProposalBinding
	if request.ConstructionProposal != nil {
		binding := *request.ConstructionProposal
		constructionProposal = &binding
	}
	receipt := explorer.CompilationReceipt{ReceiptFormatVersion: explorer.CurrentReceiptFormatVersion, CompilerContractVersion: explorer.CurrentCompilerContractVersion, Project: projectid.Canonical(request.Project), ExplorerID: request.ExplorerID, IntentDigest: intentDigest, ResolvedInputsDigest: translated.ResolvedInputsDigest, ResolvedInterpretations: append([]explorer.ResolvedInterpretation(nil), request.ResolvedInputs.Interpretations...), SnapshotToken: request.SnapshotToken, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest, ShapeDigest: snapshot.Identity.ShapeDigest, SourceGeneration: snapshot.Identity.Generation, RecipeDigest: resolved.StoredRecipeDigest, ResolvedRecipeDigest: resolvedRecipeDigest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, OutputContractDigest: contractDigest, NormalizedBundle: normalized, Bundle: resolved.Bundle, CompiledConfig: compiledConfig, PublicOutputContract: contract, IdentityMappings: translated.IdentityMappings, EmittedColumns: translated.EmittedColumns, OutputFingerprints: fingerprints, OutputColumnProvenance: columnProvenance, RowDefinitionProposal: rowDefinitionProposal, TableShapeProposal: tableShapeProposal, ConstructionProposal: constructionProposal, ConstructionStages: constructionStages, RequestID: request.RequestID, CreatedAt: time.Now().UTC()}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		return nil, err
	}
	stored, err := persistValidatedReceipt(ctx, recipeEngine, &receipt, bindings, explorerService.StoreCompilationReceipt)
	if err != nil {
		return nil, err
	}
	receiptBytes := 0
	if raw, marshalErr := json.Marshal(stored); marshalErr == nil {
		receiptBytes = len(raw)
	}
	if logger != nil {
		logger.Info("Explorer receipt compiled", "project", receipt.Project, "explorer_id", receipt.ExplorerID, "receipt_id", receipt.ID, "duration_ms", time.Since(started).Milliseconds(), "receipt_bytes", receiptBytes, "output_count", len(receipt.Bundle.Outputs), "column_count", len(receipt.EmittedColumns))
	}
	return stored, nil
}

func classifyReceiptRecipeError(err error) error {
	var reducerType *lower.PivotReducerTypeError
	if errors.As(err, &reducerType) {
		return &explorercompilation.Error{
			Stage: "construction", Code: "PIVOT_REDUCER_REQUIRES_NUMERIC",
			Message: "Choose a numeric value field for this Pivot duplicate rule, or choose Show an error.",
			Details: map[string]any{"duplicatePolicy": reducerType.Policy, "valueColumn": reducerType.ValueColumn}, Cause: err,
		}
	}
	return err
}

// persistValidatedReceipt proves that the execution engine can reproduce the
// complete immutable receipt before the receipt store sees it. A receipt that
// fails this check must never become an executable immutable artifact.
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
	value := explorerConflict("compile", "COMPILATION_CONTRACT_MISMATCH", "the compiler could not reproduce the stored receipt execution contract", receiptMismatchDetails(receiptID, cause))
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

// validateReceiptResolution proves that deterministic runtime lowering still
// describes the exact resolved semantic artifact frozen in the receipt. It
// intentionally compares no AQL or physical IR because those are
// request-scoped implementation details.
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

// receiptConstructionStages freezes the compiler's exact stage schemas and
// capabilities into the receipt identity. The implicit source projection is
// represented with an empty operation because it is not an authored step.
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
			stage, err := receiptConstructionStageFromDescriptor(descriptor)
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

func receiptConstructionStageFromDescriptor(descriptor lower.CompiledStageDescriptor) (explorer.ReceiptConstructionStage, error) {
	stage := explorer.ReceiptConstructionStage{
		ID: descriptor.ID, InputStageID: descriptor.InputStageID, Operation: descriptor.Operation,
		RowIdentityColumn: descriptor.RowIdentityColumn,
		Columns:           make([]explorer.ReceiptConstructionStageColumn, 0, len(descriptor.Columns)),
		Capabilities:      make([]explorer.ReceiptConstructionOperationChoice, 0, len(descriptor.Capabilities)),
	}
	for _, anchor := range descriptor.RelatedExpandAnchors {
		stage.RelatedExpandAnchors = append(stage.RelatedExpandAnchors, explorer.ReceiptConstructionRelatedExpandAnchor{
			AnchorColumnID: anchor.AnchorColumnID, Kind: anchor.Kind, NodeID: anchor.NodeID,
			ResourceType: anchor.ResourceType, Label: anchor.Label,
		})
	}
	for _, column := range descriptor.Columns {
		if column.Internal || column.Identity {
			continue
		}
		cardinality := expression.Cardinality(column.Cardinality)
		if !cardinality.Valid() {
			return explorer.ReceiptConstructionStage{}, fmt.Errorf("construction stage %q column %q has unsupported cardinality %q", descriptor.ID, column.ID, column.Cardinality)
		}
		stage.Columns = append(stage.Columns, explorer.ReceiptConstructionStageColumn{
			ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Kind, Cardinality: cardinality,
		})
	}
	for _, capability := range descriptor.Capabilities {
		reasonCode, reason := capability.ReasonCode, capability.Reason
		if capability.Supported {
			reasonCode, reason = "", ""
		}
		stage.Capabilities = append(stage.Capabilities, explorer.ReceiptConstructionOperationChoice{
			Kind: string(capability.Operation), Supported: capability.Supported,
			ReasonCode: reasonCode, Reason: reason,
		})
	}
	if descriptor.RelatedExpand != nil {
		related := descriptor.RelatedExpand
		stage.RelatedExpand = &explorer.ReceiptConstructionRelatedExpand{
			AnchorColumnID: related.AnchorColumnID, AnchorColumn: related.AnchorColumn,
			AnchorKind: related.AnchorKind, AnchorNodeID: related.AnchorNodeID,
			AnchorResourceType:     related.AnchorResourceType,
			RelatedRecordColumnID:  related.RelatedRecordColumnID,
			ParentIdentityColumnID: related.ParentIdentityColumnID, ParentIdentityColumn: related.ParentIdentityColumn,
			TerminalIdentityColumn: related.TerminalIdentityColumn,
			TargetNodeID:           related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			Route: append([]recipe.ConstructionRelatedRouteStep(nil), related.Route...),
		}
	}
	if descriptor.ActiveRelatedRecord != nil {
		active := descriptor.ActiveRelatedRecord
		stage.ActiveRelatedRecord = &explorer.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: active.TargetNodeID, TargetResourceType: active.TargetResourceType,
			TerminalIdentityColumn: active.TerminalIdentityColumn,
		}
	}
	return stage, nil
}

// compileValidatedReceiptResolution validates the complete immutable receipt
// even when the caller intends to execute only one output. OutputNames is an
// execution selector: allowing it to narrow compilation here would compare a
// partial fingerprint and column set with the receipt's full contract.
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
