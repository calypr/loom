package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func constructionChoicesMatchSnapshot(identities []capability.ConstructionChoiceIdentity, snapshotToken string) bool {
	return construction.ConstructionChoicesMatchSnapshot(identities, snapshotToken)
}

func checkConstructionProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	return construction.CheckConstructionProposalResult(workspace, receipt)
}

func constructionCapabilitiesWorkspace(workspace authoringv2.Workspace, targetOutputID string) (authoringv2.Workspace, error) {
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

func constructionDocumentIndex(workspace authoringv2.Workspace, outputID string) int {
	return construction.ConstructionDocumentIndex(workspace, outputID)
}

func constructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot authoringv2.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	return construction.ConstructionRouteForOccurrence(snapshot, rootResourceType, targetNodeID, authoredRoot, occurrenceID)
}

func proveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	return construction.ProveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
}
