package rowsource

import (
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type Base struct {
	Owner           *explorer.Explorer
	ExplorerID      string
	Workspace       authoringv2.Workspace
	Document        authoringv2.Document
	Construction    authoringv2.Construction
	Snapshot        capability.Snapshot
	Authorized      ports.AuthorizedCapability
	Catalog         authoringv2.CatalogSnapshot
	Receipt         *explorer.CompilationReceipt
	Stages          []explorer.ReceiptConstructionStage
	BaseDocumentSHA string
}
