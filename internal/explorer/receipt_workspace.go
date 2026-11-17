package explorer

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// CurrentWorkspace verifies current authoring intent without altering the receipt
// that authenticates historical artifacts under its original content address.
func (receipt CompilationReceipt) CurrentWorkspace() (authoringv2.Workspace, error) {
	workspace, err := authoringv2.DecodeCurrentWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	digest, err := workspace.Digest()
	if err != nil || receipt.IntentDigest == "" || digest != receipt.IntentDigest {
		return authoringv2.Workspace{}, fmt.Errorf("receipt workspace does not match its intent digest")
	}
	return workspace, nil
}
