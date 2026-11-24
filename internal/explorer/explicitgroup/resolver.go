package explicitgroup

import (
	"context"
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/projectid"
)

type explicitGroupRevisionStore interface {
	explorerselection.ExplicitGroupRepository
	GetSelection(context.Context, string, string) (*explorerselection.SelectionRevision, error)
}

type repositoryExplicitGroupRevisionResolver struct {
	store explicitGroupRevisionStore
}

func NewRepositoryExplicitGroupRevisionResolver(store explicitGroupRevisionStore) (ExplicitGroupRevisionResolver, error) {
	if store == nil {
		return nil, fmt.Errorf("explicit group revision store is required")
	}
	return repositoryExplicitGroupRevisionResolver{store: store}, nil
}

func (r repositoryExplicitGroupRevisionResolver) ListExplicitGroupRevisions(ctx context.Context, request ExplicitGroupRevisionListRequest) ([]ExplicitGroupRevisionChoice, error) {
	project := projectid.Canonical(request.Project)
	generation := request.Snapshot.Identity.Generation
	scopeDigest := request.Snapshot.Identity.AuthorizationScopeDigest
	if err := request.Snapshot.ValidateToken(request.Snapshot.Token); err != nil || project == "" || generation == "" || scopeDigest == "" || request.RootResourceType == "" {
		return nil, fmt.Errorf("explicit group revisions require an authorized project snapshot and row root")
	}
	revisions, err := r.store.ListExplicitGroupRevisions(ctx, project, generation, scopeDigest, request.RootResourceType, 100)
	if err != nil {
		return nil, err
	}
	if request.PinnedRevisionID != "" && !hasExplicitGroupRevision(revisions, request.PinnedRevisionID) {
		pinned, err := r.store.GetExplicitGroupRevision(ctx, project, explorerselection.ExplicitGroupRevisionID(request.PinnedRevisionID))
		if err != nil {
			return nil, fmt.Errorf("load pinned explicit group revision %q: %w", request.PinnedRevisionID, err)
		}
		if pinned == nil || pinned.Project != project || pinned.Generation != generation || pinned.ScopeDigest != scopeDigest || pinned.ResourceType != request.RootResourceType {
			return nil, fmt.Errorf("pinned explicit group revision %q does not match the authorized table scope", request.PinnedRevisionID)
		}
		revisions = append(revisions, *pinned)
	}
	choices := make([]ExplicitGroupRevisionChoice, 0, len(revisions))
	for _, revision := range revisions {
		selection, err := r.store.GetSelection(ctx, project, revision.SourceSelectionRevisionID)
		if err != nil {
			return nil, fmt.Errorf("load source selection for explicit group revision %q: %w", revision.ID, err)
		}
		if err := revision.ValidateSource(*selection); err != nil {
			return nil, fmt.Errorf("validate explicit group revision %q: %w", revision.ID, err)
		}
		choices = append(choices, ExplicitGroupRevisionChoice{
			RevisionID: string(revision.ID), SourceSelectionRevisionID: revision.SourceSelectionRevisionID,
			GroupCount: revision.GroupCount, MemberCount: revision.MemberCount,
			CreatedAt: revision.CreatedAt,
			UnassignedMemberPolicies: []ws.UnassignedMemberPolicy{
				ws.UnassignedMemberError, ws.UnassignedMemberExclude,
				ws.UnassignedMemberGroupAsUnassigned,
			},
		})
	}
	return choices, nil
}

func hasExplicitGroupRevision(revisions []explorerselection.ExplicitGroupRevision, revisionID string) bool {
	for _, revision := range revisions {
		if string(revision.ID) == revisionID {
			return true
		}
	}
	return false
}

func (r repositoryExplicitGroupRevisionResolver) ResolveExplicitGroupRevision(ctx context.Context, request ExplicitGroupRevisionResolveRequest) (ExplicitGroupRevisionProof, error) {
	project := projectid.Canonical(request.Project)
	if err := request.Snapshot.ValidateToken(request.Snapshot.Token); err != nil {
		return ExplicitGroupRevisionProof{}, err
	}
	revision, err := r.store.GetExplicitGroupRevision(ctx, project, explorerselection.ExplicitGroupRevisionID(request.RevisionID))
	if err != nil {
		return ExplicitGroupRevisionProof{}, err
	}
	selection, err := r.store.GetSelection(ctx, project, revision.SourceSelectionRevisionID)
	if err != nil {
		return ExplicitGroupRevisionProof{}, err
	}
	if err := revision.ValidateSource(*selection); err != nil {
		return ExplicitGroupRevisionProof{}, err
	}
	return ExplicitGroupRevisionProof{
		RevisionID: string(revision.ID), Project: revision.Project,
		SourceGeneration: revision.Generation, AuthorizationScopeDigest: revision.ScopeDigest,
		RootResourceType: revision.ResourceType, SelectionRevisionID: revision.SourceSelectionRevisionID,
		SelectionMembershipDigest: revision.SourceMembershipDigest, DefinitionDigest: revision.DefinitionDigest,
		MembershipDigest: revision.MembershipDigest, Complete: revision.State == explorerselection.ExplicitGroupRevisionComplete,
	}, nil
}

func (repositoryExplicitGroupRevisionResolver) ValidateCompilationReceipt(_ context.Context, proof ExplicitGroupRevisionProof, receipt *receiptmodel.CompilationReceipt) error {
	if receipt == nil {
		return fmt.Errorf("explicit group compilation receipt is missing")
	}
	workspace, err := ws.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return fmt.Errorf("decode explicit group compilation workspace: %w", err)
	}
	outputID := ""
	if receipt.RowDefinitionProposal != nil {
		outputID = receipt.RowDefinitionProposal.OutputID
	}
	for _, document := range workspace.Documents {
		if outputID != "" && document.Output.ID != outputID {
			continue
		}
		if document.Rows.Kind == ws.RowDefinitionGroups && document.Rows.Groups != nil &&
			document.Rows.Groups.Source.Kind == ws.GroupSourceExplicit && document.Rows.Groups.Source.Explicit != nil &&
			document.Rows.Groups.Source.Explicit.RevisionID == proof.RevisionID {
			return nil
		}
	}
	return fmt.Errorf("compiled output does not pin explicit group revision %q", proof.RevisionID)
}
