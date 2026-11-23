package ports

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

type CompileReceiptRequest struct {
	Project                         string
	ExplorerID                      string
	Workspace                       ws.Workspace
	SnapshotToken                   string
	RequestID                       string
	Authorized                      AuthorizedCapability
	ResolvedInputs                  explorercompilation.ResolvedInputs
	SelectionMembersCollection      string
	ColumnPolicyProposal            *receiptmodel.ColumnPolicyProposalBinding
	SourceRelationshipProposal      *receiptmodel.SourceRelationshipProposalBinding
	RowDefinitionProposal           *receiptmodel.RowDefinitionProposalBinding
	ConstructionProposal            *receiptmodel.ConstructionProposalBinding
	PopulationMemberRemovalProposal *receiptmodel.PopulationMemberRemovalProposalBinding
}

type ReceiptCompiler func(context.Context, CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error)

type ConstructionSourceStageCompiler func(context.Context, ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error)

type ConstructionSourceStageRequest struct {
	Project                    string
	ExplorerID                 string
	OutputID                   string
	Document                   ws.Document
	Authorized                 AuthorizedCapability
	SelectionMembersCollection string
}
