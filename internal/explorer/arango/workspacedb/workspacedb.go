package workspacedb

import arango "github.com/calypr/loom/internal/explorer/arango"

type Store = arango.Store
type WorkspaceAuditReport = arango.WorkspaceAuditReport
type WorkspaceAuditEntry = arango.WorkspaceAuditEntry

const (
	ExplorersCollection           = arango.ExplorersCollection
	DraftRevisionsCollection      = arango.DraftRevisionsCollection
	RevisionsCollection           = arango.RevisionsCollection
	CompilationReceiptsCollection = arango.CompilationReceiptsCollection
)
