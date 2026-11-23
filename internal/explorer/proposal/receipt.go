package proposal

import (
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

func VerifyReceipt(stage string, receipt *receiptmodel.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *ws.Workspace, catalog func() ws.CatalogSnapshot) (ws.Workspace, error) {
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" {
		return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt is missing", nil, nil)
	}
	if err := receiptmodel.ValidateCandidateReceiptIdentity(receipt, project, explorerID, snapshotToken, snapshot); err != nil {
		return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt capability identity does not match the request", nil, err)
	}
	if err := receipt.Validate(); err != nil {
		return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt failed integrity validation", nil, err)
	}
	if strings.TrimSpace(receipt.IntentDigest) == "" {
		return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt has no authoring intent digest", nil, nil)
	}
	if expected != nil {
		preparedExpected, err := ws.PrepareWorkspaceForCompilation(*expected, catalog())
		if err != nil {
			return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the expected workspace cannot be normalized for compilation", nil, err)
		}
		expectedDigest, err := preparedExpected.Digest()
		if err != nil {
			return ws.Workspace{}, err
		}
		if receipt.IntentDigest != expectedDigest {
			return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt does not represent the expected workspace", nil, nil)
		}
	}
	compiled, err := ws.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt workspace is invalid", nil, err)
	}
	compiledDigest, err := compiled.Digest()
	if err != nil || compiledDigest != receipt.IntentDigest {
		return ws.Workspace{}, support.Conflict(stage, "INVALID_COMPILATION_RECEIPT", "the compiled receipt workspace digest does not match its intent digest", nil, err)
	}
	return compiled, nil
}
