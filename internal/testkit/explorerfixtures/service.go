package explorerfixtures

import (
	"github.com/calypr/loom/internal/explorer"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/workspace"
)

type Stores struct {
	explorer.ServiceDependencies
	RevisionWriter      explorer.RevisionWriter
	Receipts            explorer.CompilationReceiptStore
	AuthoringPublisher  explorer.AuthoringPublisher
	RepositoryActivator explorer.RepositoryGenerationActivator
	Selections          explorer.SelectionStore
	DraftRevisions      explorer.DraftRevisionReader
}

func Dependencies(store explorer.ExplorerStore) Stores {
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
	return Stores{
		ServiceDependencies: explorer.ServiceDependencies{
			Owners: owners, Drafts: drafts, Revisions: revisions,
			ViewBuilder: viewBuilder, WorkspaceProjections: workspaceProjections,
		},
		RevisionWriter: revisionWriter,
		Receipts:       receipts, AuthoringPublisher: authoringPublisher, RepositoryActivator: repositoryActivator,
		Selections: selectionStore, DraftRevisions: draftRevisions,
	}
}

func NewService(store explorer.ExplorerStore) (*explorer.Service, error) {
	return explorer.NewService(Dependencies(store).ServiceDependencies)
}
