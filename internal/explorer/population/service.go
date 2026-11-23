package population

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/selection"
)

type CompileRequest struct {
	Project                         string
	ExplorerID                      string
	Workspace                       authoringv2.Workspace
	SnapshotToken                   string
	RequestID                       string
	PopulationMemberRemovalProposal *explorer.PopulationMemberRemovalProposalBinding
}

type PopulationMappingExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, string, []string, string, int) (execution.PopulationMappingResult, error)

type Config struct {
	Capability                     func() ports.CapabilityResolver
	Store                          selection.Store
	SelectionMembersCollection     string
	LookupReceipt                  func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
	ValidateExecutableReceiptRoute func(*explorer.CompilationReceipt, string, string) error
	Compile                        func(context.Context, CompileRequest) (*explorer.CompilationReceipt, error)
	VerifyProposalReceipt          func(context.Context, string, *explorer.CompilationReceipt, string, string, string, capability.Snapshot, *authoringv2.Workspace) (authoringv2.Workspace, error)
	GetOwner                       func(context.Context, string, string) (*explorer.Explorer, error)
	CurrentWorkspace               func(context.Context, string, string) (authoringv2.Workspace, error)
	CreateSelection                func(context.Context, selection.SelectionIntentCreateRequest) (selection.SelectionCreateResult, error)
	Catalog                        func(capability.Snapshot, string) authoringv2.CatalogSnapshot
	PreviewMemberRemoval           func(context.Context, *explorer.CompilationReceipt, string, string) error
	DocumentDigest                 func(authoringv2.Document) (string, error)
	ConstructionDocumentIndex      func(authoringv2.Workspace, string) int
	ConstructionCompilerScope      func(ports.AuthorizedCapability) facade.Scope
	ClassifyMaterializationError   func(string, error) error
	PopulationMapping              PopulationMappingExecutor
	PopulationMappingCursorCodec   *HMACPopulationMappingCursorCodec
}

type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	return &Service{config: config}, nil
}

func (s *Service) capability() ports.CapabilityResolver {
	if s.config.Capability == nil {
		return ports.CapabilityResolver{}
	}
	return s.config.Capability()
}

func (s *Service) ResolveWorkspacePopulations(ctx context.Context, project string, workspace authoringv2.Workspace, snapshot capability.Snapshot, scopeDigest string) (explorercompilation.ResolvedInputs, error) {
	return ResolveWorkspacePopulations(ctx, project, workspace, snapshot, scopeDigest, s.config.Store)
}
