package construction

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func ConstructionChoicesMatchSnapshot(identities []capability.ConstructionChoiceIdentity, snapshotToken string) bool {
	return constructionChoicesMatchSnapshot(identities, snapshotToken)
}

func CheckConstructionProposalResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt) error {
	return checkConstructionProposalResult(workspace, receipt)
}

func ConstructionCapabilitiesWorkspace(workspace ws.Workspace, targetOutputID string) (ws.Workspace, error) {
	return constructionCapabilitiesWorkspace(workspace, targetOutputID)
}

func ConstructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	return constructionChoiceIdentityMatches(choice, submittedID, identity)
}

func ConstructionChoiceSupports(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	return constructionChoiceSupports(choice, form)
}

func ProveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	return proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
}

func ConstructionCompilerScope(authorized ports.AuthorizedCapability) facade.Scope {
	return constructionCompilerScope(authorized)
}
