package rowsource

import (
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

type Base struct {
	Owner           *explorer.Explorer
	ExplorerID      string
	Workspace       ws.Workspace
	Document        ws.Document
	Construction    ws.Construction
	Snapshot        capability.Snapshot
	Authorized      ports.AuthorizedCapability
	Catalog         ws.CatalogSnapshot
	Receipt         *receiptmodel.CompilationReceipt
	Stages          []receiptmodel.ReceiptConstructionStage
	BaseDocumentSHA string
}
