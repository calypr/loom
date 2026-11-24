package authoringcore

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
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
	return support.CapabilityFailure(stage, "STALE_CATALOG_SNAPSHOT", message, err)
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
