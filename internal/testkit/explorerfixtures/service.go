package explorerfixtures

import (
	"github.com/calypr/loom/internal/explorer"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/workspace"
)

func NewService(store explorer.ExplorerStore) (*explorer.Service, error) {
	owners, _ := store.(explorer.ExplorerOwnerStore)
	drafts, _ := store.(explorer.DraftStore)
	revisions, _ := store.(explorer.RevisionReader)
	revisionWriter, _ := store.(explorer.RevisionWriter)
	receipts, _ := store.(explorer.CompilationReceiptStore)
	authoringPublisher, _ := store.(explorer.AuthoringPublisher)
	repositoryActivator, _ := store.(explorer.RepositoryGenerationActivator)
	selectionStore, _ := store.(explorer.SelectionStore)
	viewBuilder, _ := store.(explorer.ViewBuilderRepository)
	draftRevisions, _ := store.(explorer.DraftRevisionReader)
	workspaceProjections, _ := store.(workspace.ProjectionReader)
	fallback := NewStore()
	if revisions == nil {
		revisions = fallback
	}
	if revisionWriter == nil {
		revisionWriter = fallback
	}
	if receipts == nil {
		receipts = fallback
	}
	if authoringPublisher == nil {
		authoringPublisher = fallback
	}
	if repositoryActivator == nil {
		repositoryActivator = fallback
	}
	if selectionStore == nil {
		selectionStore = &SelectionStore{Store: fallback, Selections: map[string]explorerselection.SelectionRevision{}, Members: map[string][]explorerselection.SelectionMember{}}
	}
	return explorer.NewService(explorer.ServiceDependencies{
		Owners: owners, Drafts: drafts, Revisions: revisions, RevisionWriter: revisionWriter,
		Receipts: receipts, AuthoringPublisher: authoringPublisher, RepositoryActivator: repositoryActivator,
		Selections:  selectionStore,
		ViewBuilder: viewBuilder, DraftRevisions: draftRevisions, WorkspaceProjections: workspaceProjections,
	})
}
