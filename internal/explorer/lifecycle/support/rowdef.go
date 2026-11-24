package support

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func ReceiptHasOutput(bundle recipe.Bundle, id string) bool {
	for _, output := range bundle.Outputs {
		if output.Name == id {
			return true
		}
	}
	return false
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
	return ValidateAuthorizedReadScope(authorized.Scope, receipt.AuthorizationScopeDigest)
}

func VerifyPreviewReceiptIntent(receipt *receiptmodel.CompilationReceipt, workspace ws.Workspace) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	digest, err := workspace.Digest()
	if err != nil {
		return err
	}
	if receipt.IntentDigest != digest {
		return fmt.Errorf("receipt intent digest %q does not match workspace digest %q", receipt.IntentDigest, digest)
	}
	return nil
}

func RowIdentityForNetwork(document ws.Document, network authoringnetwork.AuthoringNetwork) ws.RowIdentityPlan {
	effective := document
	effective.Rows = network.SourceRows
	var route func(string) ws.RouteNode
	route = func(id string) ws.RouteNode {
		var result ws.RouteNode
		for _, occurrence := range network.Occurrences {
			if occurrence.OccurrenceID == id {
				result = ws.RouteNode{OccurrenceID: id, ResourceType: occurrence.ResourceType, CatalogEdgeID: occurrence.CatalogEdgeID, Relationship: occurrence.Relationship, MatchMode: occurrence.MatchMode}
				break
			}
		}
		for _, occurrence := range network.Occurrences {
			if occurrence.ParentOccurrenceID == id && occurrence.Enabled {
				result.Children = append(result.Children, route(occurrence.OccurrenceID))
			}
		}
		return result
	}
	effective.Route = route(ws.RootOccurrenceID)
	return ws.DescribeRowIdentity(effective)
}

type WorkspaceReader interface {
	ActiveRevision(context.Context, string, string) (*explorer.Revision, error)
	RevisionWorkspace(context.Context, explorer.Revision) (ws.Workspace, error)
}

func PreviewWorkspace(ctx context.Context, store WorkspaceReader, owner *explorer.Explorer) (ws.Workspace, error) {
	if owner == nil {
		return ws.Workspace{}, fmt.Errorf("Explorer owner is required")
	}
	if len(owner.DraftConfig) != 0 {
		return ws.DecodeWorkspace(owner.DraftConfig)
	}
	if owner.ActiveRevisionID != "" {
		revision, err := store.ActiveRevision(ctx, owner.Project, owner.ExplorerID)
		if err != nil {
			return ws.Workspace{}, err
		}
		return store.RevisionWorkspace(ctx, *revision)
	}
	return ws.NewWorkspace(owner.Title), nil
}

func CapabilityFailure(stage, code, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return Conflict(stage, code, message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return Forbidden(stage, message, err)
	default:
		return Unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}

func ReceiptCapabilityFailure(stage, message string, err error) error {
	return CapabilityFailure(stage, "RECEIPT_STALE", message, err)
}
