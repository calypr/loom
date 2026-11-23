package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
)

func selectorsForBundle(bundle recipe.Bundle) []dataset.DataframeSelector {
	selectors := make([]dataset.DataframeSelector, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		selectors = append(selectors, dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name})
	}
	return selectors
}

func verifyQueryableOutputs(bundle recipe.Bundle, execution ports.Execution) error {
	states := make(map[string]string, len(execution.Outputs))
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

func verifyActivationQuality(receipt *explorer.CompilationReceipt, execution ports.Execution) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if len(execution.QualityReports) == 0 {
		return fmt.Errorf("publication produced no quality evidence")
	}
	outputs := make([]publication.BundleOutputRecord, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		outputs = append(outputs, publication.BundleOutputRecord{Name: output.Name})
	}
	identity := publication.BundleIdentity{
		ReceiptID: receipt.ID, Project: execution.Project,
		DatasetGeneration: execution.SourceGeneration, ScopeDigest: execution.ScopeDigest,
	}
	if err := publication.ValidateQualityReports(identity, outputs, execution.QualityReports); err != nil {
		return err
	}
	for _, report := range execution.QualityReports {
		if report.PolicyVersion != publication.DefaultQualityPolicyVersion {
			return fmt.Errorf("output %q uses unsupported quality policy %q", report.Output, report.PolicyVersion)
		}
	}
	return nil
}

func materializations(bundle recipe.Bundle, execution ports.Execution) []explorer.Materialization {
	out := make([]explorer.Materialization, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name}
		out = append(out, explorer.Materialization{OutputID: output.Name, Output: output.Name, MaterializationID: execution.ID, Selector: &selector, Columns: output.Columns})
	}
	return out
}

func datasetMetadataFromExecution(bundle recipe.Bundle, generation, schemaDigest string, execution ports.Execution) explorer.DatasetMetadata {
	outputs := make([]explorer.DatasetOutput, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		state := strings.ToUpper(output.State)
		selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name}
		outputs = append(outputs, explorer.DatasetOutput{Name: output.Name, State: state, Queryable: state == "PUBLISHED" || state == "READY" || state == "ACTIVE", Selector: &selector, Columns: output.Columns})
	}
	return explorer.DatasetMetadata{Generation: generation, SchemaDigest: schemaDigest, Outputs: outputs}
}

func receiptHasOutput(bundle recipe.Bundle, id string) bool {
	for _, output := range bundle.Outputs {
		if output.Name == id {
			return true
		}
	}
	return false
}

func emittedColumnsForOutput(receipt *explorer.CompilationReceipt, outputID string) []explorer.EmittedColumn {
	columns := make([]explorer.EmittedColumn, 0)
	if receipt == nil {
		return columns
	}
	for _, column := range receipt.EmittedColumns {
		if column.OutputID == outputID {
			columns = append(columns, column)
		}
	}
	return columns
}

func (s *Service) lookupReceipt(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
	var (
		receipt *explorer.CompilationReceipt
		err     error
	)
	if s.config.ReceiptLookup != nil {
		receipt, err = s.config.ReceiptLookup(ctx, project, explorerID, receiptID)
	} else {
		receipt, err = s.store.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
	}
	if err != nil {
		return nil, support.WrapReceiptLookup("receipt", err)
	}
	return receipt, nil
}

func (s *Service) validateReceiptRoute(receipt *explorer.CompilationReceipt, project, explorerID string) error {
	if receipt == nil || projectid.Canonical(receipt.Project) != projectid.Canonical(project) || receipt.ExplorerID != explorerID {
		return support.NotFound("receipt", "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", explorer.ErrNotFound)
	}
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RecipeDigest) == "" || len(receipt.Bundle.Outputs) == 0 {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported or incomplete compiler contract", nil, nil)
	}
	native := s.config.CompileReceipt != nil || s.config.PreviewReceipt != nil || s.config.PopulationMapping != nil || s.config.MaterializeReceipt != nil
	if native && !explorer.ReceiptContractSupportedForExecution(*receipt) {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported compiler contract", nil, nil)
	}
	if native {
		if err := receipt.Validate(); err != nil {
			return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt failed integrity validation and must be recompiled", nil, err)
		}
	}
	return nil
}

func (s *Service) validateArchivedReceiptRoute(receipt *explorer.CompilationReceipt, project, explorerID string) error {
	if receipt == nil || projectid.Canonical(receipt.Project) != projectid.Canonical(project) || receipt.ExplorerID != explorerID {
		return support.NotFound("receipt", "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", explorer.ErrNotFound)
	}
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RecipeDigest) == "" || len(receipt.Bundle.Outputs) == 0 {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported or incomplete compiler contract", nil, nil)
	}
	native := s.config.CompileReceipt != nil || s.config.PreviewReceipt != nil || s.config.PopulationMapping != nil || s.config.MaterializeReceipt != nil
	if native {
		if err := receipt.ValidateArchive(); err != nil {
			return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt failed integrity validation and must be recompiled", nil, err)
		}
	}
	return nil
}

func (s *Service) validateExecutableReceiptRoute(receipt *explorer.CompilationReceipt, project, explorerID string) error {
	if err := s.validateReceiptRoute(receipt, project, explorerID); err != nil {
		return err
	}
	if receipt.Bundle.HasArchivedReshape() || receipt.TableShapeProposal != nil {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt contains retired recipe operations and must be recompiled", nil, nil)
	}
	if _, err := receipt.CurrentWorkspace(); err != nil {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt contains historical or inconsistent authoring intent and must be recompiled", nil, err)
	}
	return nil
}

func validateReceiptCapability(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if snapshot.Identity.Project != receipt.Project || snapshot.Identity.Generation != receipt.SourceGeneration {
		return fmt.Errorf("receipt capability project or generation changed")
	}
	if snapshot.Identity.AuthorizationScopeDigest != receipt.AuthorizationScopeDigest {
		return fmt.Errorf("receipt authorization scope changed")
	}
	if snapshot.Identity.SchemaDigest != receipt.CapabilitySchemaDigest {
		return fmt.Errorf("receipt capability schema changed")
	}
	return nil
}

func validateAuthorizedReceiptExecution(receipt *explorer.CompilationReceipt, authorized ports.AuthorizedCapability) error {
	if receipt == nil {
		return fmt.Errorf("receipt is required")
	}
	snapshot := authorized.Snapshot
	if strings.TrimSpace(receipt.SnapshotToken) == "" || snapshot.Token != receipt.SnapshotToken {
		return fmt.Errorf("capability token does not exactly match receipt token")
	}
	if err := snapshot.ValidateToken(receipt.SnapshotToken); err != nil {
		return fmt.Errorf("capability token is invalid: %v", err)
	}
	if snapshot.Identity.Project != receipt.Project {
		return fmt.Errorf("capability project changed")
	}
	if snapshot.Identity.Generation != receipt.SourceGeneration {
		return fmt.Errorf("capability generation changed")
	}
	if snapshot.Identity.SchemaDigest != receipt.CapabilitySchemaDigest {
		return fmt.Errorf("capability schema changed")
	}
	if snapshot.Identity.AuthorizationScopeDigest != receipt.AuthorizationScopeDigest {
		return fmt.Errorf("capability scope digest changed")
	}
	return support.ValidateAuthorizedReadScope(authorized.Scope, receipt.AuthorizationScopeDigest)
}

func validateReceiptOutputContract(receipt *explorer.CompilationReceipt, outputID string) error {
	if receipt == nil {
		return fmt.Errorf("receipt is required")
	}
	outputID = strings.TrimSpace(outputID)
	if outputID == "" {
		return fmt.Errorf("output is required")
	}
	contract, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		return fmt.Errorf("public output contract is invalid: %v", err)
	}
	if _, ok := contract.Output(outputID); !ok {
		return fmt.Errorf("requested output %q is not in the receipt public contract", outputID)
	}
	if !receiptHasOutput(receipt.Bundle, outputID) {
		return fmt.Errorf("requested output %q is not in the receipt recipe", outputID)
	}
	return nil
}

func applyAuthorizedScope(bindings *recipe.RuntimeBindings, authorized ports.AuthorizedCapability, includeAuthResourcePath bool) {
	if bindings == nil {
		return
	}
	bindings.AuthResourcePaths = append([]string(nil), authorized.Scope.AuthResourcePaths...)
	bindings.AuthScopeMode = authorized.Scope.Mode
	bindings.IncludeAuthResourcePath = includeAuthResourcePath
}

func workspaceValidationCode(err error) string {
	message := err.Error()
	for _, code := range []string{"DUPLICATE_OUTPUT_ID", "DUPLICATE_TAB_ID", "INVALID_TAB_OUTPUT_MAPPING", "INVALID_TAB_ORDER", "ROW_ROOT_NOT_ELIGIBLE", "UNSUPPORTED_FILTER", "UNSUPPORTED_CHART", "NO_VISIBLE_COLUMNS", "UNACKNOWLEDGED_RELATED_FIRST"} {
		if strings.Contains(message, code) {
			return code
		}
	}
	switch {
	case strings.Contains(message, "rootNodeId"):
		return "INVALID_ROOT_NODE"
	case strings.Contains(message, "route") || strings.Contains(message, "edge"):
		return "INVALID_ROUTE"
	case strings.Contains(message, "occurrence"):
		return "INVALID_OCCURRENCE"
	case strings.Contains(message, "projection mode"):
		return "INVALID_PROJECTION_MODE"
	case strings.Contains(message, "duplicate selection"):
		return "DUPLICATE_SELECTION"
	default:
		return "INVALID_AUTHORING_INTENT"
	}
}

func compilationErrorCode(code string) string {
	switch code {
	case "STALE_ROOT_NODE":
		return "INVALID_ROOT_NODE"
	case "ROOT_NOT_ELIGIBLE", "UNSUPPORTED_ROW_ROOT":
		return "ROW_ROOT_NOT_ELIGIBLE"
	case "STALE_EDGE":
		return "STALE_EDGE_ID"
	case "DISCONNECTED_ROUTE", "REPEATED_EDGE_NOT_ALLOWED", "SELF_LOOP_NOT_ALLOWED", "ROUTE_TOO_LONG":
		return "INVALID_ROUTE"
	case "STALE_CANDIDATE":
		return "STALE_CANDIDATE_ID"
	case "STALE_OCCURRENCE", "DUPLICATE_OCCURRENCE":
		return "INVALID_OCCURRENCE"
	case "UNSUPPORTED_PROJECTION_MODE":
		return "INVALID_PROJECTION_MODE"
	default:
		return code
	}
}
