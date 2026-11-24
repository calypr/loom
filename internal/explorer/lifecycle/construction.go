package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func constructionChoicesMatchSnapshot(identities []capability.ConstructionChoiceIdentity, snapshotToken string) bool {
	return construction.ConstructionChoicesMatchSnapshot(identities, snapshotToken)
}

func checkConstructionProposalResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt) error {
	return construction.CheckConstructionProposalResult(workspace, receipt)
}

func constructionCapabilitiesWorkspace(workspace ws.Workspace, targetOutputID string) (ws.Workspace, error) {
	return construction.ConstructionCapabilitiesWorkspace(workspace, targetOutputID)
}

func constructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	return construction.ConstructionChoiceIdentityMatches(choice, submittedID, identity)
}

func constructionChoiceSupports(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	return construction.ConstructionChoiceSupports(choice, form)
}

func constructionCompilerScope(authorized ports.AuthorizedCapability) facade.Scope {
	return construction.ConstructionCompilerScope(authorized)
}

func proveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	return construction.ProveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
}
