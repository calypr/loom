package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

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
	workspace := request.Workspace.NormalizePresentationOrders()
	intentDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	translated, err := explorercompilation.CompileWorkspace(ctx, request.Project, request.ExplorerID, workspace, snapshot)
	if err != nil {
		return nil, err
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(request.Project), DatasetGeneration: snapshot.Identity.Generation, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode}
	resolved, err := recipeEngine.CompileResolvedBundle(ctx, translated.Bundle, bindings)
	if err != nil {
		return nil, err
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
	receipt := explorer.CompilationReceipt{ReceiptFormatVersion: explorer.CurrentReceiptFormatVersion, CompilerContractVersion: explorer.CurrentCompilerContractVersion, Project: projectid.Canonical(request.Project), ExplorerID: request.ExplorerID, IntentDigest: intentDigest, SnapshotToken: request.SnapshotToken, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest, SourceGeneration: snapshot.Identity.Generation, RecipeDigest: resolved.StoredRecipeDigest, ResolvedRecipeDigest: resolvedRecipeDigest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, OutputContractDigest: contractDigest, NormalizedBundle: normalized, Bundle: resolved.Bundle, CompiledConfig: compiledConfig, PublicOutputContract: contract, IdentityMappings: translated.IdentityMappings, EmittedColumns: translated.EmittedColumns, OutputFingerprints: fingerprints, OutputColumnProvenance: columnProvenance, RequestID: request.RequestID, CreatedAt: time.Now().UTC()}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		return nil, err
	}
	stored, err := explorerService.StoreCompilationReceipt(ctx, receipt)
	if err != nil {
		return nil, err
	}
	if _, err := compileValidatedReceiptResolution(ctx, recipeEngine, stored, bindings); err != nil {
		return nil, receiptCompilationConflict(stored.ID, err)
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
			return columns[i].EmissionID < columns[j].EmissionID
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
			if filterColumns[i].FilterOrder != filterColumns[j].FilterOrder {
				return filterColumns[i].FilterOrder < filterColumns[j].FilterOrder
			}
			return filterColumns[i].EmissionID < filterColumns[j].EmissionID
		})
		for _, column := range filterColumns {
			if column.FilterLabel != "" {
				view.Filters = append(view.Filters, explorer.ConfigFilter{Column: column.PublicColumn, Label: column.FilterLabel})
			}
		}
		chartColumns := append([]explorercompilation.PresentationColumn(nil), presentation.Columns...)
		sort.SliceStable(chartColumns, func(i, j int) bool {
			if chartColumns[i].ChartOrder != chartColumns[j].ChartOrder {
				return chartColumns[i].ChartOrder < chartColumns[j].ChartOrder
			}
			return chartColumns[i].EmissionID < chartColumns[j].EmissionID
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
