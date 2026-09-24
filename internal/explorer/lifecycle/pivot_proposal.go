package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type PivotProposalSelection struct {
	ChoiceID string                            `json:"choiceId"`
	Form     capability.ConstructionChoiceForm `json:"form"`
	Title    string                            `json:"title,omitempty"`
}

type PivotProposalRequest struct {
	Project              string                   `json:"-"`
	ExplorerID           string                   `json:"-"`
	SnapshotToken        string                   `json:"snapshotToken"`
	ExpectedDraftVersion int64                    `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string                   `json:"expectedDraftDigest"`
	OutputID             string                   `json:"outputId"`
	FamilyID             string                   `json:"familyId"`
	CommandID            string                   `json:"commandId"`
	Selections           []PivotProposalSelection `json:"selections"`
}

type PivotProposalColumn struct {
	Code   string `json:"code"`
	Column string `json:"column"`
	Label  string `json:"label"`
}

type PivotProposalResult struct {
	ReceiptID   string                `json:"receiptId"`
	OutputID    string                `json:"outputId"`
	FamilyID    string                `json:"familyId"`
	DraftDigest string                `json:"draftDigest"`
	Columns     []PivotProposalColumn `json:"columns"`
}

// ProposePivot compiles a candidate without writing the Explorer draft. Its
// receipt is executable for preview, but cannot be published.
func (s *Service) ProposePivot(ctx context.Context, request PivotProposalRequest) (PivotProposalResult, error) {
	result := PivotProposalResult{}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		strings.TrimSpace(request.FamilyID) == "" || strings.TrimSpace(request.CommandID) == "" ||
		strings.TrimSpace(request.ExpectedDraftDigest) == "" || request.ExpectedDraftVersion < 1 ||
		len(request.Selections) == 0 || len(request.Selections) > 100 {
		return result, malformed("pivot-proposal", "a current draft, output, family, command, and 1 to 100 choices are required", nil)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return result, conflict("pivot-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before analyzing this pivot", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return result, conflict("pivot-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be analyzed", nil, err)
	}
	if findSemanticOutput(workspace, request.OutputID) == nil {
		return result, malformed("pivot-proposal", "outputId does not identify a saved table", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return result, unavailable("pivot-proposal", "CAPABILITY_UNAVAILABLE", "authorized catalog resolution is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return result, conflict("pivot-proposal", "STALE_CATALOG_SNAPSHOT", "reload the catalog before analyzing this pivot", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("pivot-proposal", "STALE_AUTHORIZATION_SCOPE", "the catalog scope changed", nil, err)
	}
	catalogSnapshot := s.catalog(authorized.Snapshot, request.ExplorerID)
	commands := make([]authoringv2.Command, len(request.Selections))
	identities := make([]capability.ConstructionChoiceIdentity, len(request.Selections))
	for index, selection := range request.Selections {
		if selection.Form != capability.ConstructionChoiceValue && selection.Form != capability.ConstructionChoiceAll {
			return result, unprocessable("pivot-proposal", "INVALID_PIVOT_FORM", "a code pivot must preserve its values as VALUE or ALL", nil)
		}
		identity, decodeErr := capability.DecodeConstructionChoiceID(selection.ChoiceID)
		if decodeErr != nil || identity.SnapshotToken != request.SnapshotToken {
			return result, unprocessable("pivot-proposal", "INVALID_CONSTRUCTION_CHOICE", fmt.Sprintf("choice %d is stale or invalid", index+1), decodeErr)
		}
		source, semantic := identity.Source.(capability.SemanticBindingChoiceSource)
		if !semantic || source.Code == "" || source.System == "" || source.KeySelector == "" || source.ValueSelector == "" {
			return result, unprocessable("pivot-proposal", "INVALID_PIVOT_FAMILY", "every pivot column must be a coded FHIR value", nil)
		}
		familyID, idErr := pivotFamilyID(source, identity.Route, selection.Form)
		if idErr != nil || familyID != request.FamilyID {
			return result, unprocessable("pivot-proposal", "INVALID_PIVOT_FAMILY", "selected codes do not belong to the same current pivot family", idErr)
		}
		identities[index] = identity
		commands[index] = authoringv2.Command{
			Type: authoringv2.CommandApplyConstructionChoice, OutputID: request.OutputID, Title: selection.Title,
			ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: selection.ChoiceID, Form: selection.Form},
		}
	}
	prepared, err := s.prepareConstructionChoice(ctx, request.Project, request.ExplorerID, authorized, identities, workspace, catalogSnapshot, commands)
	if err != nil {
		return result, err
	}
	candidate, applied, err := authoringv2.ApplyCommands(workspace, catalogSnapshot, request.CommandID, prepared)
	if err != nil {
		return result, unprocessable("pivot-proposal", "INVALID_PIVOT_PROPOSAL", "the selected codes could not be added to a candidate table", err)
	}
	receipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidate,
		SnapshotToken: request.SnapshotToken, Purpose: explorer.ReceiptPurposePreviewOnly,
	})
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(candidate, request.OutputID)
	if document == nil {
		return result, unavailable("pivot-proposal", "INVALID_PIVOT_PROPOSAL", "the candidate table disappeared during compilation", nil)
	}
	columns := make([]PivotProposalColumn, 0, len(applied))
	for index, appliedChoice := range applied {
		for _, column := range document.Columns {
			if column.Column == appliedChoice.Column {
				code := identities[index].Source.(capability.SemanticBindingChoiceSource).Code
				columns = append(columns, PivotProposalColumn{Code: code, Column: column.Column, Label: column.Label})
				break
			}
		}
	}
	if len(columns) != len(request.Selections) {
		return result, unavailable("pivot-proposal", "INVALID_PIVOT_PROPOSAL", "the candidate receipt omitted a selected column", nil)
	}
	return PivotProposalResult{ReceiptID: receipt.ID, OutputID: request.OutputID, FamilyID: request.FamilyID,
		DraftDigest: request.ExpectedDraftDigest, Columns: columns}, nil
}
