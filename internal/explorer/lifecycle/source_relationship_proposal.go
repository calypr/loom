package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

type SourceRelationshipProposalRequest struct {
	Project              string                                 `json:"-"`
	ExplorerID           string                                 `json:"-"`
	SnapshotToken        string                                 `json:"snapshotToken"`
	ExpectedDraftVersion int64                                  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string                                 `json:"expectedDraftDigest"`
	OutputID             string                                 `json:"outputId"`
	Change               authoringv2.ImportedRelationshipChange `json:"change,omitempty"`
	SourceStep           *authoringv2.ImportedSourceStepChange  `json:"sourceStep,omitempty"`
	SourceSteps          []authoringv2.ImportedSourceStepChange `json:"sourceSteps,omitempty"`
	Limit                int                                    `json:"limit,omitempty"`
}
type SourceRelationshipProposal struct {
	CandidateDocument        authoringv2.Document         `json:"candidateDocument"`
	CandidateNetwork         authoringv2.AuthoringNetwork `json:"candidateNetwork"`
	CandidateRowIdentity     authoringv2.RowIdentityPlan  `json:"candidateRowIdentity"`
	ProposalID               string                       `json:"proposalId"`
	CandidateReceiptID       string                       `json:"candidateReceiptId"`
	OutputID                 string                       `json:"outputId"`
	SnapshotToken            string                       `json:"snapshotToken"`
	DraftVersion             int64                        `json:"draftVersion"`
	DraftDigest              string                       `json:"draftDigest"`
	BaseDocumentDigest       string                       `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                       `json:"candidateWorkspaceDigest"`
	PreviewStatus            string                       `json:"previewStatus"`
	SampledRows              int                          `json:"sampledRows"`
	Sampled                  bool                         `json:"sampled"`
	AffectedColumns          []string                     `json:"affectedColumns"`
	Notices                  []string                     `json:"notices"`
}

// ProposeSourceRelationship compiles the entire candidate workspace before
// issuing an immutable receipt. No draft write occurs until explicit Apply.
func (s *Service) ProposeSourceRelationship(ctx context.Context, request SourceRelationshipProposalRequest) (SourceRelationshipProposal, error) {
	fail := SourceRelationshipProposal{}
	for name, value := range map[string]string{"project": request.Project, "explorerId": request.ExplorerID, "snapshotToken": request.SnapshotToken, "expectedDraftDigest": request.ExpectedDraftDigest, "outputId": request.OutputID} {
		if err := requireExactIdentity(value, name); err != nil {
			return fail, malformed("source-relationship-proposal", err.Error(), err)
		}
	}
	if request.ExpectedDraftVersion < 1 || request.Limit < 0 || request.Limit > dataframeexecution.MaxPreviewLimit {
		return fail, malformed("source-relationship-proposal", "positive draft version and bounded limit are required", nil)
	}
	if err := request.validateSourceSelection(); err != nil {
		return fail, malformed("source-relationship-proposal", err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.AuthoringNetwork == nil || (!request.hasSourceSteps() && s.config.EditImportedRelationship == nil) || (request.SourceStep != nil && s.config.EditImportedSource == nil) || (request.SourceSteps != nil && s.config.EditImportedSources == nil) {
		return fail, unavailable("source-relationship-proposal", "IMPORTED_SOURCE_UNAVAILABLE", "native relationship editing is unavailable", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) || authorized.Snapshot.Identity.Generation == "" {
		return fail, conflict("source-relationship-proposal", "STALE_CATALOG_SNAPSHOT", "reload the relationship catalog", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return fail, conflict("source-relationship-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized source scope changed", nil, err)
	}
	owner, err := s.getOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return fail, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return fail, conflict("source-relationship-proposal", "DRAFT_CONFLICT", "the saved draft changed; preview again", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return fail, conflict("source-relationship-proposal", "AUTHORING_STATE_MISSING", "the saved source cannot be read", nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil || digest != owner.DraftDigest {
		return fail, conflict("source-relationship-proposal", "DRAFT_CONFLICT", "saved workspace digest does not match", nil, err)
	}
	document := proposalDocument(workspace, request.OutputID)
	if document == nil || document.ImportedRecipe == nil {
		return fail, unprocessable("source-relationship-proposal", "IMPORTED_SOURCE_REQUIRED", "choose an imported output", nil)
	}
	baseDocumentDigest, err := documentDigest(*document)
	if err != nil {
		return fail, err
	}
	var updated authoringv2.Document
	if request.SourceSteps != nil {
		updated, err = s.config.EditImportedSources(ctx, request.Project, authorized, *document, request.SourceSteps)
	} else if request.SourceStep != nil {
		updated, err = s.config.EditImportedSource(ctx, request.Project, authorized, *document, request.SourceStep)
	} else {
		updated, err = s.config.EditImportedRelationship(ctx, request.Project, authorized, *document, request.Change)
	}
	if err != nil {
		return fail, unprocessable("source-relationship-proposal", "INVALID_IMPORTED_RELATIONSHIP", err.Error(), err)
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: request.OutputID, ProposalID: "candidate-preview"}
	if err := command.ResolveImportedDocument(updated); err != nil {
		return fail, unprocessable("source-relationship-proposal", "INVALID_IMPORTED_SOURCE", err.Error(), err)
	}
	candidate, _, err := authoringv2.ApplyCommands(workspace, s.catalog(authorized.Snapshot, request.ExplorerID), "source-relationship-preview", []authoringv2.Command{command})
	if err != nil {
		return fail, unprocessable("source-relationship-proposal", "INVALID_IMPORTED_SOURCE", err.Error(), err)
	}
	candidate, err = authoringv2.PrepareWorkspaceForCompilation(candidate, s.catalog(authorized.Snapshot, request.ExplorerID))
	if err != nil {
		return fail, err
	}
	candidateDigest, err := candidate.Digest()
	if err != nil {
		return fail, err
	}
	binding := &explorer.SourceRelationshipProposalBinding{DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, OutputID: request.OutputID, BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest, SnapshotToken: request.SnapshotToken}
	receipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidate, SnapshotToken: request.SnapshotToken, RequestID: "source-relationship-proposal", SourceRelationshipProposal: binding})
	if err != nil {
		return fail, err
	}
	verifiedCandidate, err := s.verifyProposalReceipt(ctx, "source-relationship-proposal", receipt, request.Project, request.ExplorerID, request.SnapshotToken, authorized.Snapshot, &candidate)
	if err != nil {
		return fail, err
	}
	// Project only the workspace that passed immutable receipt verification. This
	// read model cannot be supplied by the client or persisted as another recipe.
	candidateDocument := proposalDocument(verifiedCandidate, request.OutputID)
	if candidateDocument == nil {
		return fail, conflict("source-relationship-proposal", "INVALID_SOURCE_RELATIONSHIP_PROPOSAL", "candidate output missing", nil, nil)
	}
	candidateNetwork, err := s.config.AuthoringNetwork(ctx, request.Project, authorized, *candidateDocument)
	if err != nil {
		return fail, unprocessable("source-relationship-proposal", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	if candidateNetwork.OutputID != request.OutputID {
		return fail, unprocessable("source-relationship-proposal", "INVALID_AUTHORING_NETWORK", "candidate network belongs to another output", nil)
	}
	candidateRowIdentity := rowIdentityForNetwork(*candidateDocument, candidateNetwork)
	preview, err := s.Preview(ctx, PreviewRequest{Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: receipt.ID, OutputID: request.OutputID, Limit: request.Limit, SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	}})
	if err != nil {
		return fail, err
	}
	affected := []string{}
	old := map[string]bool{}
	for _, c := range document.Columns {
		old[c.ColumnID] = true
	}
	for _, c := range updated.Columns {
		if !old[c.ColumnID] {
			affected = append(affected, c.ColumnID)
		}
		delete(old, c.ColumnID)
	}
	for id := range old {
		affected = append(affected, id)
	}
	if request.Change.Action == "MATCH" || request.hasSourceSteps() {
		affected = affected[:0]
		for _, column := range updated.Columns {
			affected = append(affected, column.ColumnID)
		}
	}
	sort.Strings(affected)
	notices := []string{"Related values preserve the source row identity. Multiplicity is unknown; a preview sample does not prove one-to-one relationships."}
	if request.hasSourceSteps() {
		notices = []string{"Source settings change only the project-local source overlay; the original recipe pin stays unchanged.", "Review the resulting rows and source values with existing value policies and downstream operations before Apply."}
	}
	if request.Change.Action == "ADD" {
		notices = append(notices, "New related identifiers are kept as lists (ALL); no arbitrary related record is chosen.")
	}
	if request.Change.Action == "REMOVE" {
		notices = append(notices, "Removal drops this relationship's source values. Removing a required match can restore previously excluded source rows; downstream work must remain valid.")
	}
	if request.Change.Action == "MATCH" && request.Change.MatchMode == authoringv2.RouteMatchOptional {
		notices = append(notices, "Optional matches retain unmatched source rows with missing related values. This can restore rows previously excluded by a required match.")
	}
	if request.Change.MatchMode == authoringv2.RouteMatchRequired {
		notices = append(notices, "Required matches exclude source rows without a match; they do not expand rows.")
	}
	return SourceRelationshipProposal{CandidateDocument: *candidateDocument, CandidateNetwork: candidateNetwork, CandidateRowIdentity: candidateRowIdentity, ProposalID: receipt.ID, CandidateReceiptID: receipt.ID, OutputID: request.OutputID, SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest, PreviewStatus: "READY", SampledRows: preview.Summary.RowCount, Sampled: !preview.Summary.Complete || preview.Summary.Truncated || preview.Summary.PartialValidation, AffectedColumns: affected, Notices: notices}, nil
}

func (s *Service) prepareImportedRelationshipProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if len(commands) != 1 {
		return nil, malformed("commands", "source relationship Apply requires one command", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID {
		return nil, conflict("commands", "INVALID_SOURCE_RELATIONSHIP_PROPOSAL", "candidate receipt was not found", nil, nil)
	}
	binding := receipt.SourceRelationshipProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest || binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, conflict("commands", "STALE_SOURCE_RELATIONSHIP_PROPOSAL", "preview belongs to another draft, output or snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil || currentDigest != binding.DraftDigest {
		return nil, conflict("commands", "STALE_SOURCE_RELATIONSHIP_PROPOSAL", "saved draft changed after Preview", nil, err)
	}
	base := proposalDocument(current, command.OutputID)
	if base == nil {
		return nil, conflict("commands", "STALE_SOURCE_RELATIONSHIP_PROPOSAL", "saved output is missing", nil, nil)
	}
	baseDigest, err := documentDigest(*base)
	if err != nil || baseDigest != binding.BaseDocumentDigest {
		return nil, conflict("commands", "STALE_SOURCE_RELATIONSHIP_PROPOSAL", "saved output changed after Preview", nil, err)
	}
	candidate, err := s.verifyProposalReceipt(ctx, "commands", receipt, project, explorerID, request.SnapshotToken, authorized.Snapshot, nil)
	if err != nil {
		return nil, err
	}
	updated := proposalDocument(candidate, command.OutputID)
	if updated == nil {
		return nil, conflict("commands", "INVALID_SOURCE_RELATIONSHIP_PROPOSAL", "candidate output missing", nil, nil)
	}
	if err := command.ResolveImportedDocument(*updated); err != nil {
		return nil, err
	}
	applied, _, err := authoringv2.ApplyCommands(current, s.catalog(authorized.Snapshot, explorerID), "source-relationship-apply-validation", commands)
	if err != nil {
		return nil, err
	}
	applied, err = authoringv2.PrepareWorkspaceForCompilation(applied, s.catalog(authorized.Snapshot, explorerID))
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(applied, candidate) {
		return nil, conflict("commands", "INVALID_SOURCE_RELATIONSHIP_PROPOSAL", "candidate changes authoring state outside the requested source output", nil, nil)
	}
	// Validate the exact receipt query before the draft CAS. A transient query
	// failure or a runtime value-policy rejection must not save a source edit.
	if _, err := s.Preview(ctx, PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: command.OutputID, SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	}}); err != nil {
		return nil, err
	}
	// Re-resolve against the current authorized catalog and compile every output;
	// stale persisted edge metadata and downstream dependencies cannot bypass Apply.
	freshReceipt, err := s.compile(ctx, compileRequest{Project: project, ExplorerID: explorerID, Workspace: applied, SnapshotToken: request.SnapshotToken, RequestID: "source-relationship-apply"})
	if err != nil {
		return nil, fmt.Errorf("validate source relationship Apply: %w", err)
	}
	if freshReceipt.IntentDigest != receipt.IntentDigest || freshReceipt.ResolvedRecipeDigest != receipt.ResolvedRecipeDigest || freshReceipt.ResolvedSchemaDigest != receipt.ResolvedSchemaDigest {
		return nil, conflict("commands", "STALE_SOURCE_RELATIONSHIP_PROPOSAL", "the resolved source changed after Preview; preview again", nil, nil)
	}
	return commands, nil
}
