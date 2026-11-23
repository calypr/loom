package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/rowdef"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type RowChoiceKind = rowdef.RowChoiceKind

const (
	RowChoiceFieldGroup = rowdef.RowChoiceFieldGroup
	RowChoiceExpanded   = rowdef.RowChoiceExpanded
)

type RowDefinitionChoice = rowdef.RowDefinitionChoice
type RowDefinitionChoicePolicy = rowdef.RowDefinitionChoicePolicy
type RowChoiceResolveRequest = rowdef.RowChoiceResolveRequest
type ResolvedRowChoice = rowdef.ResolvedRowChoice
type RowChoiceResolver = rowdef.RowChoiceResolver
type RowChoicePlanner = rowdef.RowChoicePlanner
type SchemaRowChoiceResolver = rowdef.SchemaRowChoiceResolver

type AssessRowChangeRequest = rowdef.AssessRowChangeRequest
type AssessRowChangeResult = rowdef.AssessRowChangeResult
type RowLineageRequest = rowdef.RowLineageRequest
type RowLineageResult = rowdef.RowLineageResult
type RowDefinitionChoicesRequest = rowdef.RowDefinitionChoicesRequest
type RowDefinitionChoicesResponse = rowdef.RowDefinitionChoicesResponse
type RowDefinitionSelectionKind = rowdef.RowDefinitionSelectionKind

const (
	RowDefinitionSelectionRecords       = rowdef.RowDefinitionSelectionRecords
	RowDefinitionSelectionFieldGroup    = rowdef.RowDefinitionSelectionFieldGroup
	RowDefinitionSelectionExplicitGroup = rowdef.RowDefinitionSelectionExplicitGroup
	RowDefinitionSelectionExpanded      = rowdef.RowDefinitionSelectionExpanded
)

type RowDefinitionSelection = rowdef.RowDefinitionSelection
type FieldGroupSelection = rowdef.FieldGroupSelection
type ExplicitGroupSelection = rowdef.ExplicitGroupSelection
type ExpandedSelection = rowdef.ExpandedSelection
type RowDefinitionProposalRequest = rowdef.RowDefinitionProposalRequest
type RowDefinitionComparisonStatus = rowdef.RowDefinitionComparisonStatus

const (
	RowDefinitionComparisonAvailable   = rowdef.RowDefinitionComparisonAvailable
	RowDefinitionComparisonUnavailable = rowdef.RowDefinitionComparisonUnavailable
)

type RowDefinitionPreviewSummary = rowdef.RowDefinitionPreviewSummary
type RowDefinitionComparisonExample = rowdef.RowDefinitionComparisonExample
type RowDefinitionComparison = rowdef.RowDefinitionComparison
type RowDefinitionProposal = rowdef.RowDefinitionProposal

func NewSchemaRowChoiceResolver(index *fhirschema.Index) (*SchemaRowChoiceResolver, error) {
	return rowdef.NewSchemaRowChoiceResolver(index)
}

func fhirIdentifierPath(path string) bool { return rowdef.FHIRIdentifierPath(path) }
func applicableRowChoiceKinds(facts capability.RowChoiceFacts) []capability.RowChoiceKind {
	return rowdef.ApplicableRowChoiceKinds(facts)
}
func resolveRowChoiceOccurrence(snapshot capability.Snapshot, rootResourceType string, authoredRoot authoringv2.RouteNode, occurrenceID, expectedNodeID string) (capability.RowChoiceOccurrence, error) {
	return rowdef.ResolveRowChoiceOccurrence(snapshot, rootResourceType, authoredRoot, occurrenceID, expectedNodeID)
}
func capabilityRowChoiceKind(kind RowChoiceKind) (capability.RowChoiceKind, error) {
	return rowdef.CapabilityRowChoiceKind(kind)
}
func lifecycleRowChoiceKind(kind capability.RowChoiceKind) RowChoiceKind {
	return rowdef.LifecycleRowChoiceKind(kind)
}
func rowChoicePolicies(kind capability.RowChoiceKind) []RowDefinitionChoicePolicy {
	return rowdef.RowChoicePolicies(kind)
}
func rowChoiceOccurrenceSummary(choice capability.RowChoice) string {
	return rowdef.OccurrenceSummary(choice)
}
func rowChoiceRouteSummary(choice capability.RowChoice) string {
	return rowdef.RouteSummary(choice)
}

func (s *Service) AssessRowChange(ctx context.Context, request AssessRowChangeRequest) (AssessRowChangeResult, error) {
	return s.syncRowDefService().AssessRowChange(ctx, request)
}

func (s *Service) ListRowDefinitionChoices(ctx context.Context, request RowDefinitionChoicesRequest) (RowDefinitionChoicesResponse, error) {
	return s.syncRowDefService().ListRowDefinitionChoices(ctx, request)
}

func (s *Service) RowLineage(ctx context.Context, request RowLineageRequest) (RowLineageResult, error) {
	return s.syncRowDefService().RowLineage(ctx, request)
}

func (s *Service) ProposeRowDefinition(ctx context.Context, request RowDefinitionProposalRequest) (RowDefinitionProposal, error) {
	return s.syncRowDefService().ProposeRowDefinition(ctx, request)
}

func (s *Service) validateRowValuePolicyCandidate(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, workspace authoringv2.Workspace, snapshot capability.Snapshot, compilationAuth ports.AuthorizedCapability, command authoringv2.Command) error {
	return s.syncRowDefService().ValidateRowValuePolicyCandidate(ctx, project, explorerID, request, workspace, snapshot, compilationAuth, command)
}

func validateResolvedRowChoice(route authoringv2.RouteNode, resolved ResolvedRowChoice, expected RowChoiceKind) error {
	return rowdef.ValidateResolvedRowChoice(route, resolved, expected)
}

func proposalDocument(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	return proposal.ProposalDocument(workspace, outputID)
}

func documentDigest(document authoringv2.Document) (string, error) {
	return proposal.DocumentDigest(document)
}

func rowChangeReceiptWorkspace(workspace authoringv2.Workspace, outputID string) (authoringv2.Workspace, error) {
	return rowdef.RowChangeReceiptWorkspace(workspace, outputID)
}

func checkRowDefinitionProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	return rowdef.CheckRowDefinitionProposalResult(workspace, receipt)
}
