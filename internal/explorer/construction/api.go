package construction

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func ConstructionChoicesMatchSnapshot(identities []capability.ConstructionChoiceIdentity, snapshotToken string) bool {
	return constructionChoicesMatchSnapshot(identities, snapshotToken)
}

func CheckConstructionProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	return checkConstructionProposalResult(workspace, receipt)
}

func ConstructionCapabilitiesWorkspace(workspace authoringv2.Workspace, targetOutputID string) (authoringv2.Workspace, error) {
	return constructionCapabilitiesWorkspace(workspace, targetOutputID)
}

func ConstructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	return constructionChoiceIdentityMatches(choice, submittedID, identity)
}

func ConstructionChoiceSupports(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	return constructionChoiceSupports(choice, form)
}

func ConstructionDocumentIndex(workspace authoringv2.Workspace, outputID string) int {
	return constructionDocumentIndex(workspace, outputID)
}

func ConstructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot authoringv2.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	return constructionRouteForOccurrence(snapshot, rootResourceType, targetNodeID, authoredRoot, occurrenceID)
}

func ProveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	return proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
}

func ConstructionCompilerScope(authorized ports.AuthorizedCapability) facade.Scope {
	return constructionCompilerScope(authorized)
}
