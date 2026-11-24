package authoringcore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Core) LookupReceipt(ctx context.Context, project, explorerID, receiptID string) (*receiptmodel.CompilationReceipt, error) {
	var (
		receipt *receiptmodel.CompilationReceipt
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

func (s *Core) ValidateReceiptRoute(receipt *receiptmodel.CompilationReceipt, project, explorerID string) error {
	if receipt == nil || projectid.Canonical(receipt.Project) != projectid.Canonical(project) || receipt.ExplorerID != explorerID {
		return support.NotFound("receipt", "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", explorer.ErrNotFound)
	}
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RecipeDigest) == "" || len(receipt.Bundle.Outputs) == 0 {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported or incomplete compiler contract", nil, nil)
	}
	native := s.config.NativeReceipts
	if native && !receiptmodel.ReceiptContractSupportedForExecution(*receipt) {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported compiler contract", nil, nil)
	}
	if native {
		if err := receipt.Validate(); err != nil {
			return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt failed integrity validation and must be recompiled", nil, err)
		}
	}
	return nil
}

func (s *Core) ValidateArchivedReceiptRoute(receipt *receiptmodel.CompilationReceipt, project, explorerID string) error {
	if receipt == nil || projectid.Canonical(receipt.Project) != projectid.Canonical(project) || receipt.ExplorerID != explorerID {
		return support.NotFound("receipt", "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", explorer.ErrNotFound)
	}
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RecipeDigest) == "" || len(receipt.Bundle.Outputs) == 0 {
		return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported or incomplete compiler contract", nil, nil)
	}
	native := s.config.NativeReceipts
	if native {
		if err := receipt.ValidateArchive(); err != nil {
			return support.Conflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt failed integrity validation and must be recompiled", nil, err)
		}
	}
	return nil
}

func (s *Core) ValidateExecutableReceiptRoute(receipt *receiptmodel.CompilationReceipt, project, explorerID string) error {
	if err := s.ValidateReceiptRoute(receipt, project, explorerID); err != nil {
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

func WorkspaceValidationCode(err error) string {
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

func CompilationErrorCode(code string) string {
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
func ValidateAuthorizedReceiptExecution(receipt *receiptmodel.CompilationReceipt, authorized ports.AuthorizedCapability) error {
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

func ValidateReceiptOutputContract(receipt *receiptmodel.CompilationReceipt, outputID string) error {
	if receipt == nil {
		return fmt.Errorf("receipt is required")
	}
	outputID = strings.TrimSpace(outputID)
	if outputID == "" {
		return fmt.Errorf("output is required")
	}
	contract, err := receiptmodel.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		return fmt.Errorf("public output contract is invalid: %v", err)
	}
	if _, ok := contract.Output(outputID); !ok {
		return fmt.Errorf("requested output %q is not in the receipt public contract", outputID)
	}
	if !ReceiptHasOutput(receipt.Bundle, outputID) {
		return fmt.Errorf("requested output %q is not in the receipt recipe", outputID)
	}
	return nil
}

func ApplyAuthorizedScope(bindings *recipe.RuntimeBindings, authorized ports.AuthorizedCapability, includeAuthResourcePath bool) {
	if bindings == nil {
		return
	}
	bindings.AuthResourcePaths = append([]string(nil), authorized.Scope.AuthResourcePaths...)
	bindings.AuthScopeMode = authorized.Scope.Mode
	bindings.IncludeAuthResourcePath = includeAuthResourcePath
}

func ReceiptHasOutput(bundle recipe.Bundle, id string) bool {
	for _, output := range bundle.Outputs {
		if output.Name == id {
			return true
		}
	}
	return false
}

func EmittedColumnsForOutput(receipt *receiptmodel.CompilationReceipt, outputID string) []viewer.EmittedColumn {
	columns := make([]viewer.EmittedColumn, 0)
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

func CapabilityResolutionFailure(stage, message string, err error) error {
	return CapabilityFailure(stage, "STALE_CATALOG_SNAPSHOT", message, err)
}

func ReceiptCapabilityFailure(stage, message string, err error) error {
	return CapabilityFailure(stage, "RECEIPT_STALE", message, err)
}
func CapabilityFailure(stage, code, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return support.Conflict(stage, code, message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return support.Forbidden(stage, message, err)
	default:
		return support.Unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}

func (s *Core) VerifyProposalReceipt(_ context.Context, stage string, receipt *receiptmodel.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *ws.Workspace) (ws.Workspace, error) {
	return VerifyReceipt(stage, receipt, project, explorerID, snapshotToken, snapshot, expected, func() ws.CatalogSnapshot {
		return s.Catalog(snapshot, explorerID)
	})
}

func ValidateReceiptCapability(receipt *receiptmodel.CompilationReceipt, snapshot capability.Snapshot) error {
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
