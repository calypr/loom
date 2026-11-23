package proposal

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type CompileRequest struct {
	Project                    string
	ExplorerID                 string
	Workspace                  authoringv2.Workspace
	SnapshotToken              string
	RequestID                  string
	ColumnPolicyProposal       *explorer.ColumnPolicyProposalBinding
	SourceRelationshipProposal *explorer.SourceRelationshipProposalBinding
}

type PreviewRequest struct {
	Project     string
	ExplorerID  string
	ReceiptID   string
	OutputID    string
	Limit       int
	SinkFactory PreviewSinkFactory
}

type Config struct {
	Capability               func() ports.CapabilityResolver
	GetOwner                 func(context.Context, string, string) (*explorer.Explorer, error)
	LookupReceipt            func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
	Compile                  func(context.Context, CompileRequest) (*explorer.CompilationReceipt, error)
	CompileConfigured        func() bool
	VerifyProposalReceipt    func(context.Context, string, *explorer.CompilationReceipt, string, string, string, capability.Snapshot, *authoringv2.Workspace) (authoringv2.Workspace, error)
	Preview                  func(context.Context, PreviewRequest) (PreviewResult, error)
	Catalog                  func(capability.Snapshot, string) authoringv2.CatalogSnapshot
	AuthoringNetwork         func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document) (authoringv2.AuthoringNetwork, error)
	EditImportedRelationship func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, authoringv2.ImportedRelationshipChange) (authoringv2.Document, error)
	CreateImportedSourceRule func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, authoringv2.SourceRuleCreate) (authoringv2.Document, error)
	EditImportedSource       func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error)
	EditImportedSources      func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error)
}

type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	return &Service{config: config}, nil
}

func (s *Service) catalog(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	if s.config.Catalog == nil {
		return authoringv2.CatalogSnapshot{}
	}
	return s.config.Catalog(snapshot, explorerID)
}

func (s *Service) PrepareImportedRelationshipProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.prepareImportedRelationshipProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) PrepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.prepareColumnPolicyProposal(ctx, project, explorerID, request, authorized, current, commands)
}
