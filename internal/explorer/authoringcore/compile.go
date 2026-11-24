package authoringcore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
)

type CompileRequest struct {
	Project                         string
	ExplorerID                      string
	Workspace                       ws.Workspace
	SnapshotToken                   string
	RequestID                       string
	ColumnPolicyProposal            *receiptmodel.ColumnPolicyProposalBinding
	SourceRelationshipProposal      *receiptmodel.SourceRelationshipProposalBinding
	RowDefinitionProposal           *receiptmodel.RowDefinitionProposalBinding
	ConstructionProposal            *receiptmodel.ConstructionProposalBinding
	PopulationMemberRemovalProposal *receiptmodel.PopulationMemberRemovalProposalBinding
}

func (s *Core) Compile(ctx context.Context, request CompileRequest) (*receiptmodel.CompilationReceipt, error) {
	if (s.config.Capability.Token == nil && s.config.Capability.ForCompilation == nil) || s.config.CompileReceipt == nil {
		return nil, support.Unavailable("compile", "CAPABILITY_UNAVAILABLE", "Explorer V2 compiler is not configured", nil)
	}
	authorized, _, err := s.ResolveCompilationCapability(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return nil, CapabilityResolutionFailure("capability", "the capability snapshot is stale or unavailable", err)
	}
	return s.CompileAuthorized(ctx, request, authorized)
}

func (s *Core) CompileAuthorized(ctx context.Context, request CompileRequest, authorized ports.AuthorizedCapability) (*receiptmodel.CompilationReceipt, error) {
	if s.config.CompileReceipt == nil {
		return nil, support.Unavailable("compile", "CAPABILITY_UNAVAILABLE", "Explorer V2 compiler is not configured", nil)
	}
	snapshot := authorized.Snapshot
	workspace := request.Workspace.NormalizePresentationOrders()
	if err := (ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &workspace, Catalog: s.Catalog(snapshot, request.ExplorerID)}).Validate(); err != nil {
		return nil, support.Unprocessable("intent", support.WorkspaceValidationCode(err), err.Error(), err)
	}
	if authorized.Scope.Mode != "" {
		if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
			return nil, support.Conflict("population", "INVALID_POPULATION_SCOPE", "the effective authorization scope is stale", nil, err)
		}
	}
	resolvedInputs, err := ResolveWorkspacePopulations(ctx, request.Project, workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest, s.store)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, support.Unprocessable("population", "INVALID_POPULATION", err.Error(), err)
	}
	resolvedInterpretations, err := s.ResolveWorkspaceInterpretations(ctx, request.Project, workspace, snapshot)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, support.Unprocessable("interpretation", "INVALID_INTERPRETATION", err.Error(), err)
	}
	resolvedInputs.Interpretations = resolvedInterpretations.Interpretations
	receipt, err := s.config.CompileReceipt(ctx, ports.CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: request.RequestID, Authorized: authorized, ResolvedInputs: resolvedInputs, SelectionMembersCollection: s.config.SelectionMembersCollection, ColumnPolicyProposal: CloneColumnPolicyProposalBinding(request.ColumnPolicyProposal), SourceRelationshipProposal: CloneSourceRelationshipProposalBinding(request.SourceRelationshipProposal), RowDefinitionProposal: CloneRowDefinitionProposalBinding(request.RowDefinitionProposal), ConstructionProposal: CloneConstructionProposalBinding(request.ConstructionProposal), PopulationMemberRemovalProposal: clonePopulationMemberRemovalProposalBinding(request.PopulationMemberRemovalProposal)})
	if err != nil {
		var compileErr *explorercompilation.Error
		if errors.As(err, &compileErr) {
			return nil, &ports.Error{Class: ports.ClassUnprocessable, Stage: compileErr.Stage, Code: CompilationErrorCode(compileErr.Code), Message: compileErr.Message, Details: compileErr.Details, Cause: err}
		}
		var typed *ports.Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, support.Unavailable("compile", "COMPILATION_UNAVAILABLE", "the Explorer workspace could not be compiled", err)
	}
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" {
		return nil, support.Unavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "compiled authoring receipt was not persisted", nil)
	}
	if err := s.validateCompiledReceipt(ctx, request, authorized, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *Core) validateCompiledReceipt(ctx context.Context, request CompileRequest, authorized ports.AuthorizedCapability, receipt *receiptmodel.CompilationReceipt) error {
	if receipt == nil {
		return support.Unprocessable("compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt is required", nil)
	}
	snapshot := authorized.Snapshot
	identityChecks := []struct {
		name string
		got  string
		want string
	}{
		{"project", projectid.Canonical(receipt.Project), projectid.Canonical(request.Project)},
		{"explorerId", receipt.ExplorerID, request.ExplorerID},
		{"snapshotToken", receipt.SnapshotToken, request.SnapshotToken},
		{"sourceGeneration", receipt.SourceGeneration, snapshot.Identity.Generation},
		{"authorizationScopeDigest", receipt.AuthorizationScopeDigest, snapshot.Identity.AuthorizationScopeDigest},
		{"capabilitySchemaDigest", receipt.CapabilitySchemaDigest, snapshot.Identity.SchemaDigest},
		{"shapeDigest", receipt.ShapeDigest, snapshot.Identity.ShapeDigest},
	}
	for _, check := range identityChecks {
		if check.got != check.want {
			return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt identity does not match the request capability", map[string]any{"field": check.name, "expected": check.want, "actual": check.got}, nil)
		}
	}
	if !reflect.DeepEqual(request.ColumnPolicyProposal, receipt.ColumnPolicyProposal) {
		return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled column policy proposal binding does not match the request", nil, nil)
	}
	if !reflect.DeepEqual(request.SourceRelationshipProposal, receipt.SourceRelationshipProposal) {
		return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled source relationship proposal binding does not match the request", nil, nil)
	}
	if !sameRowDefinitionProposalBinding(request.RowDefinitionProposal, receipt.RowDefinitionProposal) {
		return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt row-definition proposal binding does not match the request", nil, nil)
	}
	if !sameConstructionProposalBinding(request.ConstructionProposal, receipt.ConstructionProposal) {
		return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt construction proposal binding does not match the request", nil, nil)
	}
	if !samePopulationMemberRemovalProposalBinding(request.PopulationMemberRemovalProposal, receipt.PopulationMemberRemovalProposal) {
		return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt population member proposal binding does not match the request", nil, nil)
	}
	if authorized.Scope.Mode != "" {
		if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
			return support.Unprocessable("compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt scope is not authorized", err)
		}
	}
	if err := receipt.Validate(); err != nil {
		return support.FailureDetails(ports.ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt failed integrity validation", nil, err)
	}
	persisted, err := s.LookupReceipt(ctx, request.Project, request.ExplorerID, receipt.ID)
	if err != nil {
		return support.Unavailable("compile", "COMPILATION_RECEIPT_NOT_PERSISTED", "compiled authoring receipt was not found after persistence", err)
	}
	if persisted == nil || persisted.ID != receipt.ID {
		return support.Unavailable("compile", "COMPILATION_RECEIPT_NOT_PERSISTED", "compiled authoring receipt was not returned from persistence", nil)
	}
	if err := persisted.Validate(); err != nil {
		return support.Unavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "persisted compilation receipt failed integrity validation", err)
	}
	return nil
}

func (s *Core) Catalog(snapshot capability.Snapshot, explorerID string) ws.CatalogSnapshot {
	if s.config.Capability.Catalog == nil {
		return ws.CatalogSnapshot{}
	}
	return s.config.Capability.Catalog(snapshot, explorerID)
}

func (s *Core) ResolveCompilationCapability(ctx context.Context, project, token string) (ports.AuthorizedCapability, capability.Snapshot, error) {
	if s.config.Capability.ForCompilation != nil {
		authorized, err := s.config.Capability.ForCompilation(ctx, project, token)
		return authorized, authorized.Snapshot, err
	}
	snapshot, err := s.config.Capability.Token(ctx, project, token)
	return ports.AuthorizedCapability{Snapshot: snapshot}, snapshot, err
}

func sameRowDefinitionProposalBinding(left, right *receiptmodel.RowDefinitionProposalBinding) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func samePopulationMemberRemovalProposalBinding(left, right *receiptmodel.PopulationMemberRemovalProposalBinding) bool {
	return reflect.DeepEqual(clonePopulationMemberRemovalProposalBinding(left), clonePopulationMemberRemovalProposalBinding(right))
}

func clonePopulationMemberRemovalProposalBinding(binding *receiptmodel.PopulationMemberRemovalProposalBinding) *receiptmodel.PopulationMemberRemovalProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func sameConstructionProposalBinding(left, right *receiptmodel.ConstructionProposalBinding) bool {
	return reflect.DeepEqual(left, right)
}

func CloneRowDefinitionProposalBinding(binding *receiptmodel.RowDefinitionProposalBinding) *receiptmodel.RowDefinitionProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func CloneConstructionProposalBinding(binding *receiptmodel.ConstructionProposalBinding) *receiptmodel.ConstructionProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	cloned.RemoveStepIDs = append([]string(nil), binding.RemoveStepIDs...)
	return &cloned
}

func CloneColumnPolicyProposalBinding(binding *receiptmodel.ColumnPolicyProposalBinding) *receiptmodel.ColumnPolicyProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func CloneSourceRelationshipProposalBinding(binding *receiptmodel.SourceRelationshipProposalBinding) *receiptmodel.SourceRelationshipProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func (s *Core) ResolveWorkspaceInterpretations(ctx context.Context, project string, workspace ws.Workspace, snapshot capability.Snapshot) (explorercompilation.ResolvedInputs, error) {
	ids := make(map[interpretation.InterpretationRevisionID]struct{})
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Interpretation == nil || column.Interpretation.Kind != ws.FeatureInterpretationPinned || column.Interpretation.Pinned == nil {
				continue
			}
			id, err := interpretation.NewInterpretationRevisionID(strings.TrimSpace(column.Interpretation.Pinned.RevisionID))
			if err != nil {
				return explorercompilation.ResolvedInputs{}, fmt.Errorf("interpretation %s/%s: %w", document.Output.ID, column.Column, err)
			}
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return explorercompilation.ResolvedInputs{}, nil
	}
	if s.config.InterpretationRepository == nil {
		return explorercompilation.ResolvedInputs{}, support.Unavailable("interpretation", "INTERPRETATION_UNAVAILABLE", "interpretation revisions are not configured", nil)
	}
	revisions := make(map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision, len(ids))
	for id := range ids {
		revision, err := s.config.InterpretationRepository.GetInterpretationRevision(ctx, projectid.Canonical(project), id)
		if err != nil {
			return explorercompilation.ResolvedInputs{}, support.Unavailable("interpretation", "INTERPRETATION_UNAVAILABLE", fmt.Sprintf("load interpretation revision %q", id), err)
		}
		if revision == nil {
			return explorercompilation.ResolvedInputs{}, fmt.Errorf("load interpretation revision %q: repository returned nil", id)
		}
		revisions[id] = *revision
	}
	return explorercompilation.ResolveWorkspaceInterpretations(project, workspace, snapshot, revisions)
}
