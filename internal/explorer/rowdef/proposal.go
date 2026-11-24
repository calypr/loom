package rowdef

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/receiptmodel"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/projectid"
)

func unsupportedFieldGroupRowDefinition() error {
	return support.Unprocessable(
		"row-definition-proposal", "FIELD_GROUP_UNSUPPORTED",
		"field-based row grouping is not supported by this workflow; choose an available row definition", nil,
	)
}

func ValidateResolvedRowChoice(route ws.RouteNode, resolved ResolvedRowChoice, expected RowChoiceKind) error {
	if resolved.Kind != expected || support.RequireExactIdentity(resolved.OccurrenceID, "resolved occurrenceId") != nil || !routeContainsOccurrence(route, resolved.OccurrenceID) {
		return fmt.Errorf("row choice does not identify an authored route occurrence")
	}
	switch expected {
	case RowChoiceFieldGroup:
		if support.RequireExactIdentity(resolved.FieldPath, "resolved fieldPath") != nil || resolved.ScopePath != "" {
			return fmt.Errorf("field-group choice has an invalid resolved field")
		}
	case RowChoiceExpanded:
		if support.RequireExactIdentity(resolved.ScopePath, "resolved scopePath") != nil || resolved.FieldPath != "" {
			return fmt.Errorf("expanded choice has an invalid resolved scope")
		}
	default:
		return fmt.Errorf("row choice kind is unsupported")
	}
	return nil
}

func routeContainsOccurrence(route ws.RouteNode, occurrenceID string) bool {
	if route.OccurrenceID == occurrenceID {
		return true
	}
	for _, child := range route.Children {
		if routeContainsOccurrence(child, occurrenceID) {
			return true
		}
	}
	return false
}

func validateExplicitGroupRevisionProof(proof explicitgroup.ExplicitGroupRevisionProof, request explicitgroup.ExplicitGroupRevisionResolveRequest) error {
	if proof.RevisionID != request.RevisionID || projectid.Canonical(proof.Project) != projectid.Canonical(request.Project) ||
		proof.SourceGeneration != request.Snapshot.Identity.Generation || proof.AuthorizationScopeDigest != request.Snapshot.Identity.AuthorizationScopeDigest ||
		proof.RootResourceType != request.RootResourceType || !proof.Complete {
		return fmt.Errorf("explicit group revision identity is incomplete or stale")
	}
	for name, value := range map[string]string{
		"selectionRevisionId": proof.SelectionRevisionID, "selectionMembershipDigest": proof.SelectionMembershipDigest,
		"definitionDigest": proof.DefinitionDigest, "membershipDigest": proof.MembershipDigest,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("explicit group proof %s is missing", name)
		}
	}
	return nil
}

func CheckRowDefinitionProposalResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt) error {
	if receipt == nil {
		return support.Conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied row-definition workspace: %w", err)
	}
	if digest != receipt.IntentDigest {
		return support.Conflict("commands", "ROW_DEFINITION_PROPOSAL_MISMATCH", "the applied row definition does not match the candidate receipt workspace", nil, nil)
	}
	return nil
}

func rowDefinitionWorkspaceChange(base, candidate ws.Workspace, outputID string) error {
	baseWithoutRows := base
	candidateWithoutRows := candidate
	baseWithoutRows.Documents = append([]ws.Document(nil), base.Documents...)
	candidateWithoutRows.Documents = append([]ws.Document(nil), candidate.Documents...)
	baseDocument, candidateDocument := -1, -1
	for index := range base.Documents {
		if base.Documents[index].Output.ID == outputID {
			baseDocument = index
		}
	}
	for index := range candidate.Documents {
		if candidate.Documents[index].Output.ID == outputID {
			candidateDocument = index
		}
	}
	if baseDocument < 0 || candidateDocument < 0 || baseDocument != candidateDocument {
		return fmt.Errorf("output %q is missing or moved in the candidate workspace", outputID)
	}
	if reflect.DeepEqual(base.Documents[baseDocument].Rows, candidate.Documents[candidateDocument].Rows) {
		return fmt.Errorf("candidate row definition does not change output %q", outputID)
	}
	for index := range base.Documents {
		if index != baseDocument && !reflect.DeepEqual(base.Documents[index].Rows, candidate.Documents[index].Rows) {
			return fmt.Errorf("candidate changes rows for output %q outside the requested output", base.Documents[index].Output.ID)
		}
		baseWithoutRows.Documents[index].Rows = ws.RowDefinition{}
		candidateWithoutRows.Documents[index].Rows = ws.RowDefinition{}
	}
	if !reflect.DeepEqual(baseWithoutRows, candidateWithoutRows) {
		return fmt.Errorf("candidate changes authoring fields outside Document.Rows")
	}
	return nil
}

func (s *Service) ProposeRowDefinition(ctx context.Context, request RowDefinitionProposalRequest) (RowDefinitionProposal, error) {
	return s.propose(ctx, request)
}

func (s *Service) propose(ctx context.Context, request RowDefinitionProposalRequest) (RowDefinitionProposal, error) {
	if err := request.Validate(); err != nil {
		return RowDefinitionProposal{}, support.Malformed("row-definition-proposal", err.Error(), err)
	}
	if request.Selection.Kind == RowDefinitionSelectionFieldGroup {
		return RowDefinitionProposal{}, unsupportedFieldGroupRowDefinition()
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.Core == nil {
		return RowDefinitionProposal{}, support.Unavailable("row-definition-proposal", "PROPOSAL_UNAVAILABLE", "row definition proposal compilation is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	snapshot := authorized.Snapshot
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return RowDefinitionProposal{}, support.Conflict("row-definition-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return RowDefinitionProposal{}, support.Conflict("row-definition-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	owner, err := s.config.Core.GetOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return RowDefinitionProposal{}, support.Conflict("row-definition-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before proposing a row definition", nil, explorer.ErrDraftConflict)
	}
	workspace, err := support.PreviewWorkspace(ctx, s.config.Store, owner)
	if err != nil {
		return RowDefinitionProposal{}, support.Conflict("row-definition-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be proposed", nil, err)
	}
	baseDigest, err := workspace.Digest()
	if err != nil || baseDigest != owner.DraftDigest {
		return RowDefinitionProposal{}, support.Conflict("row-definition-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	baseDocument := workspace.FindOutputDocument(request.OutputID)
	if baseDocument == nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	if baseDocument.ImportedRecipe != nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "IMPORTED_SOURCE_ROW_OVERLAY_REQUIRED", "imported source rows are defined by the immutable recipe pin and source overlay; use a construction Group or related-record expansion, or explicitly replace the source to detach the pin", nil)
	}
	baseDocumentDigest, err := support.DocumentDigest(*baseDocument)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	baseGroupProof, err := s.resolveExplicitGroupForDocument(ctx, request.Project, request.ExplorerID, request.OutputID, snapshot, *baseDocument, nil)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	rows, candidateGroupProof, err := s.ResolveRowDefinitionSelection(ctx, request, snapshot, *baseDocument)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		return RowDefinitionProposal{}, support.Unavailable("row-definition-proposal", "PREVIEW_UNAVAILABLE", "row definition preview execution is not configured", nil)
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyRowDefinitionProposal, OutputID: request.OutputID, Payload: &authoringv2.ProposalPayload{ProposalID: "proposal-preview"}}
	if err := command.ResolveRowDefinitionProposal(rows); err != nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	catalog := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	comparisonBase, _, err := authoringv2.ApplyCommands(workspace, catalog, "row-definition-preview", nil)
	if err != nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, catalog, "row-definition-preview", []authoringv2.Command{command})
	if err != nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	if err := rowDefinitionWorkspaceChange(comparisonBase, candidateWorkspace, request.OutputID); err != nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = dataframeexecution.DefaultPreviewLimit
	}
	baseReceipt, err := s.config.Core.Compile(ctx, authoringcore.CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken, RequestID: "row-definition-proposal-base"})
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if _, err := s.verifyRowDefinitionReceipt(ctx, baseReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &workspace); err != nil {
		return RowDefinitionProposal{}, err
	}
	if err := s.validateExplicitGroupReceipt(ctx, baseGroupProof, baseReceipt); err != nil {
		return RowDefinitionProposal{}, err
	}
	binding := &receiptmodel.RowDefinitionProposalBinding{
		DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, OutputID: request.OutputID,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken,
	}
	candidateReceipt, err := s.config.Core.Compile(ctx, authoringcore.CompileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "row-definition-proposal-candidate",
		RowDefinitionProposal: binding,
	})
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	verifiedCandidate, err := s.verifyRowDefinitionReceipt(ctx, candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &candidateWorkspace)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if err := s.validateExplicitGroupReceipt(ctx, candidateGroupProof, candidateReceipt); err != nil {
		return RowDefinitionProposal{}, err
	}

	candidateDocument := verifiedCandidate.FindOutputDocument(request.OutputID)
	if candidateDocument == nil {
		return RowDefinitionProposal{}, support.Conflict("row-definition-proposal", "INVALID_ROW_DEFINITION_PROPOSAL", "candidate output missing", nil, nil)
	}
	candidateNetwork, err := authoringnetwork.NetworkForDocument(*candidateDocument, catalog)
	if err != nil {
		return RowDefinitionProposal{}, support.Unprocessable("row-definition-proposal", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	candidateRowIdentity := support.RowIdentityForNetwork(*candidateDocument, candidateNetwork)
	comparison, err := s.CompareRowDefinitionReceipts(ctx, request, snapshot, baseReceipt, candidateReceipt, limit)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	return RowDefinitionProposal{
		CandidateDocument: *candidateDocument, CandidateNetwork: candidateNetwork, CandidateRowIdentity: candidateRowIdentity,
		ProposalID: candidateReceipt.ID, BaseReceiptID: baseReceipt.ID, OutputID: request.OutputID,
		SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest, Mode: request.Selection.Kind,
		Comparison: comparison,
	}, nil
}

func (s *Service) ResolveRowDefinitionSelection(ctx context.Context, request RowDefinitionProposalRequest, snapshot capability.Snapshot, document ws.Document) (ws.RowDefinition, *explicitgroup.ExplicitGroupRevisionProof, error) {
	switch request.Selection.Kind {
	case RowDefinitionSelectionRecords:
		return ws.RecordsRowDefinition(), nil, nil
	case RowDefinitionSelectionFieldGroup:
		return ws.RowDefinition{}, nil, unsupportedFieldGroupRowDefinition()
	case RowDefinitionSelectionExplicitGroup:
		policy := request.Selection.ExplicitGroup
		proof, err := s.resolveExplicitGroupRevision(ctx, explicitgroup.ExplicitGroupRevisionResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: snapshot.Clone(), RevisionID: policy.RevisionID, RootResourceType: document.RootResourceType,
		})
		if err != nil {
			return ws.RowDefinition{}, nil, err
		}
		groups := &ws.GroupedRows{
			Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
				RevisionID: proof.RevisionID, UnassignedMemberPolicy: policy.UnassignedMemberPolicy,
			}},
		}
		if existing := document.Rows.Groups; existing != nil && document.Rows.Kind == ws.RowDefinitionGroups && existing.Source.Kind == ws.GroupSourceExplicit {
			groups.AfterStepID = existing.AfterStepID
			groups.RowValues = append([]ws.ExplicitGroupRowValue(nil), existing.RowValues...)
		} else if document.Construction != nil && len(document.Construction.Steps) > 0 {
			groups.AfterStepID = document.Construction.Steps[len(document.Construction.Steps)-1].ID
		}
		return ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: groups}, &proof, nil
	case RowDefinitionSelectionExpanded:
		if s.config.RowChoiceResolver == nil {
			return ws.RowDefinition{}, nil, support.Unavailable("row-definition-proposal", "ROW_CHOICE_UNAVAILABLE", "row choice resolution is not configured", nil)
		}
		resolved, err := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: snapshot.Clone(), Route: document.Route, RowChoiceID: request.Selection.Expanded.RowChoiceID, ExpectedKind: RowChoiceExpanded,
		})
		if err != nil || ValidateResolvedRowChoice(document.Route, resolved, RowChoiceExpanded) != nil {
			return ws.RowDefinition{}, nil, support.Conflict("row-definition-proposal", "STALE_ROW_CHOICE", "the selected expansion choice is stale or unavailable", nil, err)
		}
		return ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
			OccurrenceID: resolved.OccurrenceID, ScopePath: resolved.ScopePath,
			EmptyCollectionPolicy: request.Selection.Expanded.EmptyCollectionPolicy,
		}}, nil, nil
	default:
		return ws.RowDefinition{}, nil, support.Malformed("row-definition-proposal", "row selection kind is unsupported", nil)
	}
}

func (s *Service) resolveExplicitGroupForDocument(ctx context.Context, project, explorerID, outputID string, snapshot capability.Snapshot, document ws.Document, receipt *receiptmodel.CompilationReceipt) (*explicitgroup.ExplicitGroupRevisionProof, error) {
	rows := document.Rows
	if rows.Kind != ws.RowDefinitionGroups || rows.Groups == nil || rows.Groups.Source.Kind != ws.GroupSourceExplicit || rows.Groups.Source.Explicit == nil {
		return nil, nil
	}
	return s.resolveExplicitGroupRevisionForRows(ctx, project, explorerID, outputID, snapshot, document, rows, receipt)
}

func (s *Service) resolveExplicitGroupRevision(ctx context.Context, request explicitgroup.ExplicitGroupRevisionResolveRequest) (explicitgroup.ExplicitGroupRevisionProof, error) {
	if s.config.ExplicitGroupResolver == nil {
		return explicitgroup.ExplicitGroupRevisionProof{}, support.Unavailable("row-definition-proposal", "EXPLICIT_GROUP_UNAVAILABLE", "explicit group revision validation is not configured", nil)
	}
	proof, err := s.config.ExplicitGroupResolver.ResolveExplicitGroupRevision(ctx, request)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return explicitgroup.ExplicitGroupRevisionProof{}, typed
		}
		return explicitgroup.ExplicitGroupRevisionProof{}, support.PersistenceFailure("row-definition-proposal", "the explicit group revision could not be loaded", err)
	}
	if err := validateExplicitGroupRevisionProof(proof, request); err != nil {
		return explicitgroup.ExplicitGroupRevisionProof{}, support.Conflict("row-definition-proposal", "STALE_EXPLICIT_GROUP_REVISION", "the explicit group revision does not match the current project, snapshot, or table", nil, err)
	}
	return proof, nil
}

func (s *Service) resolveExplicitGroupRevisionForRows(ctx context.Context, project, explorerID, outputID string, snapshot capability.Snapshot, document ws.Document, rows ws.RowDefinition, receipt *receiptmodel.CompilationReceipt) (*explicitgroup.ExplicitGroupRevisionProof, error) {
	if rows.Kind != ws.RowDefinitionGroups || rows.Groups == nil || rows.Groups.Source.Kind != ws.GroupSourceExplicit || rows.Groups.Source.Explicit == nil {
		return nil, nil
	}
	request := explicitgroup.ExplicitGroupRevisionResolveRequest{
		Project: project, ExplorerID: explorerID, OutputID: outputID, Snapshot: snapshot.Clone(),
		RevisionID: rows.Groups.Source.Explicit.RevisionID, RootResourceType: document.RootResourceType,
	}
	proof, err := s.resolveExplicitGroupRevision(ctx, request)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		if err := s.validateExplicitGroupReceipt(ctx, &proof, receipt); err != nil {
			return nil, err
		}
	}
	return &proof, nil
}

func (s *Service) validateExplicitGroupReceipt(ctx context.Context, proof *explicitgroup.ExplicitGroupRevisionProof, receipt *receiptmodel.CompilationReceipt) error {
	if proof == nil {
		return nil
	}
	if s.config.ExplicitGroupResolver == nil {
		return support.Unavailable("row-definition-proposal", "EXPLICIT_GROUP_UNAVAILABLE", "explicit group receipt validation is not configured", nil)
	}
	if err := s.config.ExplicitGroupResolver.ValidateCompilationReceipt(ctx, *proof, receipt); err != nil {
		return support.Conflict("row-definition-proposal", "INVALID_EXPLICIT_GROUP_RECEIPT", "the candidate receipt does not prove the exact explicit group revision", nil, err)
	}
	return nil
}

func (s *Service) verifyRowDefinitionReceipt(ctx context.Context, receipt *receiptmodel.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *ws.Workspace) (ws.Workspace, error) {
	return s.config.Core.VerifyProposalReceipt(ctx, "row-definition-proposal", receipt, project, explorerID, snapshotToken, snapshot, expected)
}

func (s *Service) PrepareRowDefinitionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyRowDefinitionProposal {
		return nil, nil, support.Malformed("commands", "APPLY_ROW_DEFINITION_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.config.Core.LookupReceipt(ctx, project, explorerID, command.ProposalID())
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID() {
		return nil, nil, support.Conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the proposal ID does not match a candidate receipt", nil, nil)
	}
	binding := receipt.RowDefinitionProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, nil, err
	}
	if currentDigest != binding.DraftDigest {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the saved draft no longer matches the proposal base", nil, nil)
	}
	baseDocument := current.FindOutputDocument(command.OutputID)
	if baseDocument == nil {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the proposal output is missing from the saved draft", nil, nil)
	}
	baseDocumentDigest, err := support.DocumentDigest(*baseDocument)
	if err != nil {
		return nil, nil, err
	}
	if baseDocumentDigest != binding.BaseDocumentDigest {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the target table changed after the proposal was compiled", nil, nil)
	}
	candidate, err := s.verifyRowDefinitionReceipt(ctx, receipt, project, explorerID, request.SnapshotToken, snapshot, nil)
	if err != nil {
		return nil, nil, err
	}
	candidateDocument := candidate.FindOutputDocument(command.OutputID)
	if candidateDocument == nil {
		return nil, nil, support.Conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the candidate receipt does not contain the requested output", nil, nil)
	}
	catalog := s.config.Capability.Catalog(snapshot, explorerID)
	comparisonBase, _, err := authoringv2.ApplyCommands(current, catalog, "row-definition-proposal-apply", nil)
	if err != nil {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the saved draft can no longer be normalized for the proposal", nil, err)
	}
	canonicalBase, err := comparisonBase.CanonicalJSON()
	if err != nil {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the saved draft can no longer be canonicalized for the proposal", nil, err)
	}
	comparisonBase, err = ws.DecodeWorkspace(canonicalBase)
	if err != nil {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the saved draft can no longer be canonicalized for the proposal", nil, err)
	}
	if err := rowDefinitionWorkspaceChange(comparisonBase, candidate, command.OutputID); err != nil {
		return nil, nil, support.Conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the candidate receipt no longer changes only the requested table rows", nil, err)
	}
	if _, err := s.resolveExplicitGroupRevisionForRows(ctx, project, explorerID, command.OutputID, snapshot, *candidateDocument, candidateDocument.Rows, receipt); err != nil {
		return nil, nil, err
	}
	if err := command.ResolveRowDefinitionProposal(candidateDocument.Rows); err != nil {
		return nil, nil, support.Conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the candidate row definition is invalid", nil, err)
	}
	return commands, receipt, nil
}
