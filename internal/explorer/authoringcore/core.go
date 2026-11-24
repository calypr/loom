package authoringcore

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

type Config struct {
	DefaultDataframeWorkspace  func(context.Context, string, capability.Snapshot, ws.CatalogSnapshot) (ws.Workspace, error)
	Capability                 ports.CapabilityResolver
	CompileReceipt             ports.ReceiptCompiler
	PreviewReceipt             func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (execution.PreviewSummary, error)
	ReceiptLookup              func(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error)
	InterpretationRepository   interpretation.Repository
	SelectionMembersCollection string
	NativeReceipts             bool
}

type Core struct {
	store  *explorer.Service
	config Config
}

func New(store *explorer.Service, config Config) *Core {
	return &Core{store: store, config: config}
}

func (s *Core) CompileConfigured() bool {
	return s.config.CompileReceipt != nil
}
