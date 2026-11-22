package ports

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

type CompileReceiptRequest struct {
	Project                         string
	ExplorerID                      string
	Workspace                       authoringv2.Workspace
	SnapshotToken                   string
	RequestID                       string
	Authorized                      AuthorizedCapability
	ResolvedInputs                  explorercompilation.ResolvedInputs
	SelectionMembersCollection      string
	ColumnPolicyProposal            *explorer.ColumnPolicyProposalBinding
	SourceRelationshipProposal      *explorer.SourceRelationshipProposalBinding
	RowDefinitionProposal           *explorer.RowDefinitionProposalBinding
	ConstructionProposal            *explorer.ConstructionProposalBinding
	PopulationMemberRemovalProposal *explorer.PopulationMemberRemovalProposalBinding
}

type ReceiptCompiler func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error)

type ConstructionSourceStageCompiler func(context.Context, ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error)

type ConstructionSourceStageRequest struct {
	Project                    string
	ExplorerID                 string
	OutputID                   string
	Document                   authoringv2.Document
	Authorized                 AuthorizedCapability
	SelectionMembersCollection string
}
