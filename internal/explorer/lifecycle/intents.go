package lifecycle

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type authoringCommandIntents struct {
	constructionChoiceCommand       bool
	frameSourceCommand              bool
	columnPolicyProposalCommand     bool
	rowDefinitionProposalCommand    bool
	rowChangeReplacementCommand     bool
	constructionProposalCommand     bool
	populationMemberProposalCommand bool
	restoreDraftRevisionCommand     bool
	rowValuePolicyCommand           bool
	importedSourceCommand           bool
	projectedFieldRemovalCommand    bool
	populationRouteCommand          bool
	constructionIdentities          []capability.ConstructionChoiceIdentity
	frameSourceIdentity             capability.ConstructionChoiceIdentity
	populationRouteIdentities       []capability.PopulationRouteChoiceIdentity
	semanticCommand                 bool
}

func parseAuthoringCommandIntents(request authoringv2.ApplyCommandsRequest) (authoringCommandIntents, error) {
	var intents authoringCommandIntents
	intents.semanticCommand = request.Commands[0].Type == authoringv2.CommandAddSemanticSelections
	intents.constructionChoiceCommand = request.Commands[0].Type == authoringv2.CommandApplyConstructionChoice
	intents.frameSourceCommand = request.Commands[0].Type == authoringv2.CommandSetFrameSource || request.Commands[0].Type == authoringv2.CommandReplaceFrameSource
	intents.columnPolicyProposalCommand = request.Commands[0].Type == authoringv2.CommandApplyColumnPolicyProposal
	intents.rowDefinitionProposalCommand = request.Commands[0].Type == authoringv2.CommandApplyRowDefinitionProposal
	intents.rowChangeReplacementCommand = request.Commands[0].Type == authoringv2.CommandApplyTableRootRebase && request.Commands[0].RowChange() != nil && request.Commands[0].RowChange().Mode == authoringv2.RowChangeModeReplace
	intents.constructionProposalCommand = request.Commands[0].Type == authoringv2.CommandApplyConstructionProposal
	intents.populationMemberProposalCommand = request.Commands[0].Type == authoringv2.CommandApplyPopulationMemberProposal
	intents.restoreDraftRevisionCommand = request.Commands[0].Type == authoringv2.CommandRestoreDraftRevision
	intents.rowValuePolicyCommand = request.Commands[0].Type == authoringv2.CommandUpdateColumnRowValuePolicy
	intents.importedSourceCommand = authoringv2.IsImportedSourceCommand(request.Commands[0].Type)
	intents.projectedFieldRemovalCommand = request.Commands[0].Type == authoringv2.CommandRemoveProjectedField
	for _, command := range request.Commands {
		if command.Type == authoringv2.CommandUpdateColumnRowValuePolicy && len(request.Commands) != 1 {
			return authoringCommandIntents{}, support.Malformed("commands", "UPDATE_COLUMN_ROW_VALUE_POLICY must be the only command in its atomic request", nil)
		}
	}
	intents.constructionIdentities = make([]capability.ConstructionChoiceIdentity, len(request.Commands))
	if intents.frameSourceCommand {
		identity, decodeErr := capability.DecodeConstructionChoiceID(request.Commands[0].FrameChoiceID())
		if decodeErr != nil || identity.Kind != capability.ConstructionChoiceSourceSemanticFrame {
			return authoringCommandIntents{}, support.Malformed("commands", "frameChoiceId is invalid", decodeErr)
		}
		if identity.SnapshotToken != request.SnapshotToken {
			return authoringCommandIntents{}, support.Conflict("commands", "STALE_FRAME_SOURCE", "the frame source belongs to a different catalog snapshot", nil, nil)
		}
		intents.frameSourceIdentity = identity
	}
	populationRouteCount := 0
	for _, command := range request.Commands {
		if command.Type == authoringv2.CommandSetTablePopulation {
			populationRouteCount++
		}
	}
	intents.populationRouteCommand = populationRouteCount > 0
	intents.populationRouteIdentities = make([]capability.PopulationRouteChoiceIdentity, len(request.Commands))
	if intents.populationRouteCommand {
		for index, command := range request.Commands {
			if command.Type != authoringv2.CommandSetTablePopulation || command.RouteChoiceID() == "" || len(command.EdgeIDs()) != 0 {
				return authoringCommandIntents{}, support.Malformed("commands", "population route choices require only SET_TABLE_POPULATION routeChoiceId commands", nil)
			}
			identity, decodeErr := capability.DecodePopulationRouteChoiceID(command.RouteChoiceID())
			if decodeErr != nil {
				return authoringCommandIntents{}, support.Malformed("commands", fmt.Sprintf("commands[%d].routeChoiceId is invalid", index), decodeErr)
			}
			if identity.SnapshotToken != request.SnapshotToken || identity.OutputID != command.OutputID || identity.SelectionRevisionID != command.SelectionRevisionID() {
				return authoringCommandIntents{}, support.Conflict("commands", "STALE_POPULATION_ROUTE", "a population route belongs to another table, selection, or snapshot", nil, nil)
			}
			intents.populationRouteIdentities[index] = identity
		}
		if populationRouteCount != len(request.Commands) {
			return authoringCommandIntents{}, support.Malformed("commands", "population route choices must form the entire atomic request", nil)
		}
	}
	if intents.constructionChoiceCommand {
		for index, command := range request.Commands {
			identity, decodeErr := capability.DecodeConstructionChoiceID(command.ConstructionChoice().ChoiceID)
			if decodeErr != nil {
				return authoringCommandIntents{}, support.Malformed("commands", fmt.Sprintf("commands[%d].constructionChoice.choiceId is invalid", index), decodeErr)
			}
			if identity.SnapshotToken != request.SnapshotToken {
				return authoringCommandIntents{}, support.Conflict("commands", "STALE_CONSTRUCTION_CHOICE", "a construction choice belongs to a different catalog snapshot", nil, nil)
			}
			intents.constructionIdentities[index] = identity
		}
	}
	return intents, nil
}

func (intents authoringCommandIntents) requiresAuthorizedScope() bool {
	return intents.semanticCommand ||
		intents.constructionChoiceCommand ||
		intents.populationRouteCommand ||
		intents.constructionProposalCommand ||
		intents.populationMemberProposalCommand ||
		intents.restoreDraftRevisionCommand ||
		intents.frameSourceCommand ||
		intents.rowValuePolicyCommand ||
		intents.rowChangeReplacementCommand ||
		intents.importedSourceCommand ||
		intents.columnPolicyProposalCommand ||
		intents.projectedFieldRemovalCommand
}
