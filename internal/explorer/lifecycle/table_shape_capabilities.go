package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
	"github.com/calypr/loom/internal/projectid"
)

const maxTableShapeResolutionReferences = 64

type TableShapeCatalogRequest struct {
	Project              string `json:"project"`
	ExplorerID           string `json:"explorerId"`
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
	OutputID             string `json:"outputId"`
}

func (r TableShapeCatalogRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	return nil
}

func (r *TableShapeCatalogRequest) UnmarshalJSON(data []byte) error {
	type wire TableShapeCatalogRequest
	value, err := tableshapecap.DecodeStrict[wire](data)
	if err != nil {
		return err
	}
	decoded := TableShapeCatalogRequest(value)
	if err := decoded.Validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

type TableShapeColumnChoice struct {
	Role tableshapecap.ChoiceRole `json:"role"`
	ID   string                   `json:"id"`
}

type TableShapeCatalogColumn struct {
	Label        string                    `json:"label"`
	LogicalType  tableshapecap.LogicalType `json:"logicalType"`
	Nullable     bool                      `json:"nullable"`
	UnitIdentity string                    `json:"unitIdentity,omitempty"`
	Choices      []TableShapeColumnChoice  `json:"choices,omitempty"`
}

type TableShapeCatalogChoice struct {
	Role                         tableshapecap.ChoiceRole `json:"role"`
	ID                           string                   `json:"id"`
	Label                        string                   `json:"label"`
	Type                         *tableshapecap.TypeFact  `json:"type,omitempty"`
	RequiresDivisionByZeroPolicy bool                     `json:"requiresDivisionByZeroPolicy,omitempty"`
}

type TableShapeCatalogResult struct {
	CatalogID              string                           `json:"catalogId"`
	OutputID               string                           `json:"outputId"`
	Columns                []TableShapeCatalogColumn        `json:"columns"`
	ReshapeModes           []TableShapeReshapeModeChoice    `json:"reshapeModes"`
	Operators              []TableShapeCatalogChoice        `json:"operators"`
	Policies               []TableShapeCatalogChoice        `json:"policies"`
	Operands               []TableShapeCatalogChoice        `json:"operands"`
	Availability           []tableshapecap.RoleAvailability `json:"availability"`
	SavedShape             tableshapecap.SavedShapeSummary  `json:"savedShape"`
	SavedProposal          TableShapeSavedProposalIntent    `json:"savedProposalIntent"`
	SavedDerivedReferences []TableShapeDerivedReference     `json:"savedDerivedReferences"`
}

type TableShapeReshapeModeChoice struct {
	ID         string                          `json:"id"`
	Mode       string                          `json:"mode"`
	Label      string                          `json:"label"`
	State      tableshapecap.AvailabilityState `json:"state"`
	ReasonCode string                          `json:"reasonCode,omitempty"`
	Reason     string                          `json:"reason,omitempty"`
}

type tableShapeBase struct {
	owner         *explorer.Explorer
	workspace     authoringv2.Workspace
	document      authoringv2.Document
	snapshot      capability.Snapshot
	authorized    AuthorizedCapability
	receipt       *explorer.CompilationReceipt
	contract      explorer.PublicOutputContract
	finalReceipt  *explorer.CompilationReceipt
	finalContract explorer.PublicOutputContract
	binding       tableshapecap.Binding
}

type tableShapeBaseContext struct {
	owner              *explorer.Explorer
	workspace          authoringv2.Workspace
	document           authoringv2.Document
	snapshot           capability.Snapshot
	authorized         AuthorizedCapability
	baseDocumentDigest string
}

func (s *Service) GetTableShapeCatalog(ctx context.Context, request TableShapeCatalogRequest) (TableShapeCatalogResult, error) {
	if err := request.Validate(); err != nil {
		return TableShapeCatalogResult{}, malformed("table-shape-capabilities", err.Error(), err)
	}
	if s.config.TableShapeCapabilities == nil {
		return TableShapeCatalogResult{}, unavailable("table-shape-capabilities", "CAPABILITY_STORE_UNAVAILABLE", "table-shape capability storage is not configured", nil)
	}
	if s.config.CompileReceipt == nil {
		return TableShapeCatalogResult{}, unavailable("table-shape-capabilities", "CAPABILITY_UNAVAILABLE", "authorized table-shape compilation is not configured", nil)
	}
	current, err := s.loadTableShapeBaseContext(ctx, request)
	if err != nil {
		return TableShapeCatalogResult{}, err
	}
	if current.document.TableShape == nil {
		lookup := tableShapeCatalogLookup(request, current)
		catalog, lookupErr := s.config.TableShapeCapabilities.FindCatalogForLookup(ctx, lookup)
		if lookupErr != nil && !errors.Is(lookupErr, tableshapecap.ErrNotFound) && !errors.Is(lookupErr, tableshapecap.ErrAmbiguous) {
			return TableShapeCatalogResult{}, unavailable("table-shape-capabilities", "CAPABILITY_STORE_FAILED", "the table-shape capability catalog could not be loaded", lookupErr)
		}
		if lookupErr == nil {
			if base, reusable := s.reuseTableShapeCatalogBase(ctx, request, current, lookup, catalog.ID, catalog); reusable {
				_, facts, err := tableShapeCatalogColumns(base.contract)
				if err != nil {
					return TableShapeCatalogResult{}, conflict("table-shape-capabilities", "INVALID_COMPILER_SCHEMA", "the compiler output schema is invalid for table-shape capabilities", nil, err)
				}
				return s.publicTableShapeCatalogResult(ctx, base, catalog, facts), nil
			}
		}
	}
	base, err := s.compileTableShapeBase(ctx, request, current)
	if err != nil {
		return TableShapeCatalogResult{}, err
	}
	columns, facts, err := tableShapeCatalogColumns(base.contract)
	if err != nil {
		return TableShapeCatalogResult{}, conflict("table-shape-capabilities", "INVALID_COMPILER_SCHEMA", "the compiler output schema is invalid for table-shape capabilities", nil, err)
	}
	availability, choices := buildTableShapeChoices(columns, facts, s.config.ScanTableShapeCategories != nil, base.document.TableShape)
	saved, err := savedTableShapeSummary(base.document.TableShape)
	if err != nil {
		return TableShapeCatalogResult{}, fmt.Errorf("summarize saved table shape: %w", err)
	}
	receipt, err := tableshapecap.NewCatalogReceipt(base.binding, columns, availability, choices, saved, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return TableShapeCatalogResult{}, conflict("table-shape-capabilities", "INVALID_COMPILER_SCHEMA", "the compiler output schema cannot produce a table-shape catalog", nil, err)
	}
	stored, err := s.config.TableShapeCapabilities.PutCatalog(ctx, receipt)
	if err != nil {
		return TableShapeCatalogResult{}, unavailable("table-shape-capabilities", "CAPABILITY_STORE_FAILED", "the table-shape capability catalog could not be stored", err)
	}
	if err := stored.Validate(); err != nil || stored.ID != receipt.ID || stored.Binding != base.binding {
		return TableShapeCatalogResult{}, unavailable("table-shape-capabilities", "CAPABILITY_STORE_FAILED", "the stored table-shape catalog failed identity validation", err)
	}
	return s.publicTableShapeCatalogResult(ctx, base, stored, facts), nil
}

func (s *Service) publicTableShapeCatalogResult(ctx context.Context, base tableShapeBase, catalog tableshapecap.CatalogReceipt, facts map[string]tableShapeColumnFacts) TableShapeCatalogResult {
	result := publicTableShapeCatalog(catalog, facts)
	result.ReshapeModes = tableShapeReshapeModes(catalog)
	result.SavedProposal = s.savedTableShapeProposal(ctx, base, catalog, facts)
	for _, derived := range result.SavedProposal.DerivedColumns {
		result.SavedDerivedReferences = append(result.SavedDerivedReferences, TableShapeDerivedReference{ResolutionID: derived.ResolutionID, Label: derived.OutputLabel, Type: derived.Result})
	}
	return result
}

func (s *Service) loadTableShapeBase(ctx context.Context, request TableShapeCatalogRequest) (tableShapeBase, error) {
	if s.config.CompileReceipt == nil {
		return tableShapeBase{}, unavailable("table-shape-capabilities", "CAPABILITY_UNAVAILABLE", "authorized table-shape compilation is not configured", nil)
	}
	baseContext, err := s.loadTableShapeBaseContext(ctx, request)
	if err != nil {
		return tableShapeBase{}, err
	}
	return s.compileTableShapeBase(ctx, request, baseContext)
}

func (s *Service) loadTableShapeBaseContext(ctx context.Context, request TableShapeCatalogRequest) (tableShapeBaseContext, error) {
	if s.config.Capability.ForCompilation == nil {
		return tableShapeBaseContext{}, unavailable("table-shape-capabilities", "CAPABILITY_UNAVAILABLE", "authorized table-shape compilation is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return tableShapeBaseContext{}, conflict("table-shape-capabilities", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return tableShapeBaseContext{}, conflict("table-shape-capabilities", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return tableShapeBaseContext{}, conflict("table-shape-capabilities", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return tableShapeBaseContext{}, err
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return tableShapeBaseContext{}, notFound("table-shape-capabilities", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return tableShapeBaseContext{}, conflict("table-shape-capabilities", "DRAFT_CONFLICT", "the Explorer draft changed; reload before requesting table-shape capabilities", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return tableShapeBaseContext{}, conflict("table-shape-capabilities", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be compiled", nil, err)
	}
	draftDigest, err := workspace.Digest()
	if err != nil || draftDigest != owner.DraftDigest {
		return tableShapeBaseContext{}, conflict("table-shape-capabilities", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	document := proposalDocument(workspace, request.OutputID)
	if document == nil {
		return tableShapeBaseContext{}, unprocessable("table-shape-capabilities", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	baseDocumentDigest, err := documentDigest(*document)
	if err != nil {
		return tableShapeBaseContext{}, fmt.Errorf("digest table-shape output document: %w", err)
	}
	return tableShapeBaseContext{
		owner: owner, workspace: workspace, document: *document, snapshot: snapshot,
		authorized: authorized.Clone(), baseDocumentDigest: baseDocumentDigest,
	}, nil
}

func (s *Service) compileTableShapeBase(ctx context.Context, request TableShapeCatalogRequest, current tableShapeBaseContext) (tableShapeBase, error) {
	owner, workspace := current.owner, current.workspace
	document, snapshot, authorized := current.document, current.snapshot, current.authorized
	baseDocumentDigest := current.baseDocumentDigest
	finalReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken,
		RequestID: "table-shape-capabilities",
	})
	if err != nil {
		return tableShapeBase{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "table-shape-capabilities", finalReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &workspace); err != nil {
		return tableShapeBase{}, err
	}
	if err := validateCandidateReceiptIdentity(finalReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot); err != nil {
		return tableShapeBase{}, conflict("table-shape-capabilities", "INVALID_COMPILATION_RECEIPT", "the compilation receipt does not match the authorized draft", nil, err)
	}
	if err := validateReceiptOutputContract(finalReceipt, request.OutputID); err != nil {
		return tableShapeBase{}, unprocessable("table-shape-capabilities", "OUTPUT_NOT_FOUND", "outputId is not in the compiler output contract", nil)
	}
	finalContracts, err := explorer.DecodePublicOutputContracts(finalReceipt.PublicOutputContract)
	if err != nil {
		return tableShapeBase{}, conflict("table-shape-capabilities", "INVALID_COMPILER_SCHEMA", "the compiler output schema is invalid", nil, err)
	}
	finalContract, ok := finalContracts.Output(request.OutputID)
	if !ok {
		return tableShapeBase{}, unprocessable("table-shape-capabilities", "OUTPUT_NOT_FOUND", "outputId is not in the compiler output contract", nil)
	}
	// A saved reshape is a derived view of a compiler-owned base relation. Build
	// editor choices from that relation, not from the reshaped final schema,
	// while retaining the actual draft/document digests as the capability's
	// binding. This is what makes reload able to restore removed base columns.
	baseWorkspace := workspace
	baseWorkspace.Documents = append([]authoringv2.Document(nil), workspace.Documents...)
	for index := range baseWorkspace.Documents {
		if baseWorkspace.Documents[index].Output.ID == request.OutputID {
			baseWorkspace.Documents[index].TableShape = nil
		}
	}
	baseReceipt := finalReceipt
	if !sameWorkspaceIntent(workspace, baseWorkspace) {
		baseReceipt, err = s.compile(ctx, compileRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, Workspace: baseWorkspace, SnapshotToken: request.SnapshotToken,
			RequestID: "table-shape-capabilities-base",
		})
		if err != nil {
			return tableShapeBase{}, err
		}
		if _, err := s.verifyProposalReceipt(ctx, "table-shape-capabilities", baseReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &baseWorkspace); err != nil {
			return tableShapeBase{}, err
		}
	}
	if err := validateReceiptOutputContract(baseReceipt, request.OutputID); err != nil {
		return tableShapeBase{}, conflict("table-shape-capabilities", "INVALID_COMPILATION_RECEIPT", "the base compilation receipt has no requested output", nil, err)
	}
	baseContracts, err := explorer.DecodePublicOutputContracts(baseReceipt.PublicOutputContract)
	if err != nil {
		return tableShapeBase{}, conflict("table-shape-capabilities", "INVALID_COMPILER_SCHEMA", "the compiler base output schema is invalid", nil, err)
	}
	baseContract, ok := baseContracts.Output(request.OutputID)
	if !ok {
		return tableShapeBase{}, unprocessable("table-shape-capabilities", "OUTPUT_NOT_FOUND", "outputId is not in the compiler base output contract", nil)
	}
	outputFingerprint := baseReceipt.OutputFingerprints[request.OutputID]
	if strings.TrimSpace(outputFingerprint) == "" || outputFingerprint != strings.TrimSpace(outputFingerprint) || strings.TrimSpace(baseReceipt.OutputContractDigest) == "" {
		return tableShapeBase{}, conflict("table-shape-capabilities", "INVALID_COMPILATION_RECEIPT", "the compiler receipt is missing its final output identity", nil, nil)
	}
	binding := tableshapecap.Binding{
		Project: projectid.Canonical(request.Project), ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		SnapshotToken: request.SnapshotToken, AuthorizationScope: snapshot.Identity.AuthorizationScopeDigest,
		SourceGeneration: snapshot.Identity.Generation, DraftVersion: uint64(owner.DraftVersion), DraftDigest: owner.DraftDigest,
		BaseDocumentDigest: baseDocumentDigest, BaseCompilationReceiptID: baseReceipt.ID,
		OutputFingerprint: outputFingerprint, CompilerSchemaDigest: baseReceipt.OutputContractDigest,
	}
	if err := binding.Validate(); err != nil {
		return tableShapeBase{}, conflict("table-shape-capabilities", "INVALID_COMPILATION_RECEIPT", "the compiler receipt does not contain a complete capability binding", nil, err)
	}
	return tableShapeBase{
		owner: owner, workspace: workspace, document: document, snapshot: snapshot, authorized: authorized.Clone(),
		receipt: baseReceipt, contract: baseContract, finalReceipt: finalReceipt, finalContract: finalContract, binding: binding,
	}, nil
}

func sameWorkspaceIntent(left, right authoringv2.Workspace) bool {
	leftDigest, leftErr := left.Digest()
	rightDigest, rightErr := right.Digest()
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

type tableShapeColumnFacts struct {
	contract explorer.PublicOutputColumn
	public   tableshapecap.PublicColumn
}

func tableShapeCatalogColumns(contract explorer.PublicOutputContract) ([]tableshapecap.PublicColumn, map[string]tableShapeColumnFacts, error) {
	columns := make([]tableshapecap.PublicColumn, 0, len(contract.Columns))
	facts := make(map[string]tableShapeColumnFacts, len(contract.Columns))
	for _, column := range contract.Columns {
		logicalType := tableShapeLogicalType(column.LogicalType)
		unitIdentity, err := unitIdentityString(column.ResultUnit)
		if err != nil {
			return nil, nil, fmt.Errorf("column %q has an invalid normalized unit identity: %w", column.Column, err)
		}
		public := tableshapecap.PublicColumn{
			Key: column.Column, Label: column.Label, LogicalType: logicalType,
			Nullable: column.Nullable, UnitIdentity: unitIdentity,
		}
		if err := public.Validate(); err != nil {
			return nil, nil, err
		}
		if _, exists := facts[public.Key]; exists {
			return nil, nil, fmt.Errorf("compiler output contains duplicate column %q", public.Key)
		}
		columns = append(columns, public)
		facts[public.Key] = tableShapeColumnFacts{contract: column, public: public}
	}
	return columns, facts, nil
}

func tableShapeLogicalType(value string) tableshapecap.LogicalType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "string", "code", "uuid":
		return tableshapecap.LogicalString
	case "integer":
		return tableshapecap.LogicalInteger
	case "decimal", "number":
		return tableshapecap.LogicalDecimal
	case "boolean", "bool":
		return tableshapecap.LogicalBoolean
	case "date":
		return tableshapecap.LogicalDate
	case "date_time", "datetime":
		return tableshapecap.LogicalDateTime
	default:
		return tableshapecap.LogicalObject
	}
}

func buildTableShapeChoices(columns []tableshapecap.PublicColumn, facts map[string]tableShapeColumnFacts, scanAvailable bool, savedShape *authoringv2.TableShape) ([]tableshapecap.RoleAvailability, tableshapecap.CatalogChoices) {
	roles := []tableshapecap.ChoiceRole{
		tableshapecap.RolePivotGroup, tableshapecap.RolePivotCategory, tableshapecap.RolePivotValue,
		tableshapecap.RoleUnpivotInput, tableshapecap.RoleDerivedOperand, tableshapecap.RoleDerivedOperator,
		tableshapecap.RolePolicyDuplicate, tableshapecap.RolePolicyMissing, tableshapecap.RolePolicyUnlisted,
		tableshapecap.RolePolicyUnpivotNull, tableshapecap.RolePolicyDerivedMissing, tableshapecap.RolePolicyDivisionByZero,
	}
	choices := tableshapecap.CatalogChoices{
		Columns: make([]tableshapecap.ColumnChoice, 0), Operators: make([]tableshapecap.OperatorChoice, 0),
		Policies: make([]tableshapecap.PolicyChoice, 0), Operands: make([]tableshapecap.OperandChoice, 0),
	}
	counts := make(map[tableshapecap.ChoiceRole]int, len(roles))
	savedDerivedColumns := make(map[string]struct{})
	if savedShape != nil {
		for _, derived := range savedShape.Derived {
			savedDerivedColumns[derived.Output.Column] = struct{}{}
		}
	}
	for _, column := range columns {
		fact := facts[column.Key]
		if !tableShapeScalarColumn(fact.contract) || column.LogicalType == tableshapecap.LogicalObject {
			continue
		}
		for _, role := range []tableshapecap.ChoiceRole{tableshapecap.RolePivotGroup, tableshapecap.RolePivotCategory, tableshapecap.RolePivotValue} {
			if role == tableshapecap.RolePivotCategory && !scanAvailable {
				continue
			}
			choices.Columns = append(choices.Columns, tableshapecap.ColumnChoice{Role: role, ColumnKey: column.Key})
			counts[role]++
		}
		_, isSavedDerived := savedDerivedColumns[column.Key]
		if !isSavedDerived && (column.LogicalType == tableshapecap.LogicalInteger || column.LogicalType == tableshapecap.LogicalDecimal) {
			choices.Operands = append(choices.Operands, tableshapecap.OperandChoice{
				Role:    tableshapecap.RoleDerivedOperand,
				Operand: tableshapecap.OperandRef{Kind: tableshapecap.OperandColumn, ColumnKey: column.Key},
			})
			counts[tableshapecap.RoleDerivedOperand]++
		}
	}
	compatible := compatibleUnpivotColumns(columns, facts)
	for _, column := range columns {
		if _, ok := compatible[column.Key]; ok {
			choices.Columns = append(choices.Columns, tableshapecap.ColumnChoice{Role: tableshapecap.RoleUnpivotInput, ColumnKey: column.Key})
			counts[tableshapecap.RoleUnpivotInput]++
		}
	}
	for _, operator := range []string{"ADD", "SUBTRACT", "MULTIPLY", "DIVIDE"} {
		choices.Operators = append(choices.Operators, tableshapecap.OperatorChoice{Role: tableshapecap.RoleDerivedOperator, Operator: operator})
	}
	counts[tableshapecap.RoleDerivedOperator] = len(choices.Operators)
	for _, policy := range []struct {
		role tableshapecap.ChoiceRole
		id   string
	}{
		{tableshapecap.RolePolicyDuplicate, "ERROR"},
		{tableshapecap.RolePolicyMissing, "NULL"}, {tableshapecap.RolePolicyMissing, "ERROR"},
		{tableshapecap.RolePolicyUnlisted, "ERROR"}, {tableshapecap.RolePolicyUnlisted, "EXCLUDE_WITH_EVIDENCE"},
		{tableshapecap.RolePolicyUnpivotNull, "DROP"}, {tableshapecap.RolePolicyUnpivotNull, "PRESERVE"},
		{tableshapecap.RolePolicyDerivedMissing, "PROPAGATE_NULL"}, {tableshapecap.RolePolicyDerivedMissing, "ERROR"},
		{tableshapecap.RolePolicyDivisionByZero, "NULL"}, {tableshapecap.RolePolicyDivisionByZero, "ERROR"},
	} {
		choices.Policies = append(choices.Policies, tableshapecap.PolicyChoice{Role: policy.role, PolicyID: policy.id})
		counts[policy.role]++
	}
	if counts[tableshapecap.RoleDerivedOperand] > 0 {
		for _, policy := range []string{"SUM", "MIN", "MAX"} {
			choices.Policies = append(choices.Policies, tableshapecap.PolicyChoice{Role: tableshapecap.RolePolicyDuplicate, PolicyID: policy})
			counts[tableshapecap.RolePolicyDuplicate]++
		}
	}
	compatibleInputs := counts[tableshapecap.RoleUnpivotInput]
	pivotPossible := counts[tableshapecap.RolePivotGroup] > 0 && counts[tableshapecap.RolePivotCategory] > 0 && counts[tableshapecap.RolePivotValue] > 0
	availability := make([]tableshapecap.RoleAvailability, 0, len(roles))
	for _, role := range roles {
		entry := tableshapecap.RoleAvailability{Role: role, State: tableshapecap.AvailabilitySupported}
		var code, message string
		switch role {
		case tableshapecap.RolePivotGroup:
			if counts[role] == 0 {
				code, message = "NO_SCALAR_GROUP_COLUMNS", "The compiler output has no scalar columns that can group pivot rows."
			}
		case tableshapecap.RolePivotCategory:
			if !scanAvailable {
				code, message = "CATEGORY_SCAN_UNAVAILABLE", "Complete pivot categories cannot be discovered because the category scanner is not configured."
			} else if counts[role] == 0 {
				code, message = "NO_SUPPORTED_CATEGORY_COLUMNS", "The compiler output has no scalar columns supported by the category scanner."
			}
		case tableshapecap.RolePivotValue:
			if counts[role] == 0 {
				code, message = "NO_SCALAR_VALUE_COLUMNS", "The compiler output has no scalar columns that can provide pivot values."
			}
		case tableshapecap.RoleUnpivotInput:
			if compatibleInputs < 2 {
				code, message = "NO_COMPATIBLE_UNPIVOT_INPUTS", "The compiler output has fewer than two compatible scalar columns for an unpivot."
			}
		case tableshapecap.RoleDerivedOperand:
			if counts[role] == 0 {
				code, message = "NO_NUMERIC_OPERAND_COLUMNS", "The compiler output has no scalar numeric columns for derived calculations."
			}
		case tableshapecap.RolePolicyDuplicate, tableshapecap.RolePolicyMissing, tableshapecap.RolePolicyUnlisted:
			if !pivotPossible {
				code, message = "PIVOT_ROLES_UNAVAILABLE", "Pivot policies are unavailable because the compiler output cannot fill every pivot column role."
			}
		case tableshapecap.RolePolicyUnpivotNull:
			if compatibleInputs < 2 {
				code, message = "UNPIVOT_INPUTS_UNAVAILABLE", "Null-row policies are unavailable because the compiler output has no compatible unpivot inputs."
			}
		}
		if code != "" {
			entry = tableshapecap.RoleAvailability{Role: role, State: tableshapecap.AvailabilityRefused, ReasonCode: code, Message: message}
			filteredColumns := choices.Columns[:0]
			for _, choice := range choices.Columns {
				if choice.Role != role {
					filteredColumns = append(filteredColumns, choice)
				}
			}
			choices.Columns = filteredColumns
			if role == tableshapecap.RolePolicyDuplicate || role == tableshapecap.RolePolicyMissing || role == tableshapecap.RolePolicyUnlisted || role == tableshapecap.RolePolicyUnpivotNull {
				filteredPolicies := choices.Policies[:0]
				for _, choice := range choices.Policies {
					if choice.Role != role {
						filteredPolicies = append(filteredPolicies, choice)
					}
				}
				choices.Policies = filteredPolicies
			}
			if role == tableshapecap.RoleDerivedOperand {
				choices.Operands = choices.Operands[:0]
			}
		}
		availability = append(availability, entry)
	}
	return availability, choices
}

func tableShapeScalarColumn(column explorer.PublicOutputColumn) bool {
	switch column.Shape {
	case "scalar", "indexed_scalar":
		return true
	default:
		return false
	}
}

func compatibleUnpivotColumns(columns []tableshapecap.PublicColumn, facts map[string]tableShapeColumnFacts) map[string]struct{} {
	groups := make(map[string][]string)
	for _, column := range columns {
		fact := facts[column.Key]
		if !tableShapeScalarColumn(fact.contract) || column.LogicalType == tableshapecap.LogicalObject {
			continue
		}
		logical := string(column.LogicalType)
		if column.LogicalType == tableshapecap.LogicalInteger || column.LogicalType == tableshapecap.LogicalDecimal {
			logical = "NUMERIC"
		}
		key := logical + "\x00" + column.UnitIdentity
		groups[key] = append(groups[key], column.Key)
	}
	compatible := make(map[string]struct{})
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		for _, column := range group {
			compatible[column] = struct{}{}
		}
	}
	return compatible
}

func publicTableShapeCatalog(receipt tableshapecap.CatalogReceipt, facts map[string]tableShapeColumnFacts) TableShapeCatalogResult {
	result := TableShapeCatalogResult{
		CatalogID: receipt.ID, OutputID: receipt.Binding.OutputID,
		Columns:      make([]TableShapeCatalogColumn, 0, len(receipt.Columns)),
		Operators:    make([]TableShapeCatalogChoice, 0, len(receipt.Choices.Operators)),
		Policies:     make([]TableShapeCatalogChoice, 0, len(receipt.Choices.Policies)),
		Operands:     make([]TableShapeCatalogChoice, 0, len(receipt.Choices.Operands)),
		Availability: append([]tableshapecap.RoleAvailability(nil), receipt.Availability...),
		SavedShape:   receipt.SavedShape,
	}
	for _, column := range receipt.Columns {
		item := TableShapeCatalogColumn{Label: column.Label, LogicalType: column.LogicalType, Nullable: column.Nullable, UnitIdentity: column.UnitIdentity}
		for _, choice := range receipt.Choices.Columns {
			if choice.ColumnKey == column.Key {
				item.Choices = append(item.Choices, TableShapeColumnChoice{Role: choice.Role, ID: choice.ID})
			}
		}
		result.Columns = append(result.Columns, item)
	}
	for _, choice := range receipt.Choices.Operators {
		result.Operators = append(result.Operators, TableShapeCatalogChoice{
			Role: choice.Role, ID: choice.ID, Label: tableShapeOperatorLabel(choice.Operator),
			RequiresDivisionByZeroPolicy: choice.Operator == "DIVIDE",
		})
	}
	for _, choice := range receipt.Choices.Policies {
		result.Policies = append(result.Policies, TableShapeCatalogChoice{Role: choice.Role, ID: choice.ID, Label: tableShapePolicyLabel(choice.Role, choice.PolicyID)})
	}
	for _, choice := range receipt.Choices.Operands {
		fact := facts[choice.Operand.ColumnKey].public
		typeFact := tableshapecap.TypeFact{LogicalType: fact.LogicalType, Nullable: fact.Nullable, UnitIdentity: fact.UnitIdentity}
		result.Operands = append(result.Operands, TableShapeCatalogChoice{Role: choice.Role, ID: choice.ID, Label: fact.Label, Type: &typeFact})
	}
	return result
}

func tableShapeReshapeModes(receipt tableshapecap.CatalogReceipt) []TableShapeReshapeModeChoice {
	pivot := roleAvailability(receipt, tableshapecap.RolePivotGroup).State == tableshapecap.AvailabilitySupported &&
		roleAvailability(receipt, tableshapecap.RolePivotCategory).State == tableshapecap.AvailabilitySupported &&
		roleAvailability(receipt, tableshapecap.RolePivotValue).State == tableshapecap.AvailabilitySupported
	unpivot := roleAvailability(receipt, tableshapecap.RoleUnpivotInput).State == tableshapecap.AvailabilitySupported
	return []TableShapeReshapeModeChoice{
		{ID: tableShapeModeChoiceID(receipt.ID, "NONE"), Mode: "NONE", Label: "None", State: tableshapecap.AvailabilitySupported},
		modeChoice(receipt, "GROUPED_PIVOT", "Grouped pivot", pivot, "PIVOT_ROLES_UNAVAILABLE", "The compiler output cannot fill all pivot column roles."),
		modeChoice(receipt, "UNPIVOT", "Unpivot", unpivot, "UNPIVOT_INPUTS_UNAVAILABLE", "The compiler output has fewer than two compatible unpivot inputs."),
	}
}

func modeChoice(receipt tableshapecap.CatalogReceipt, mode, label string, supported bool, code, reason string) TableShapeReshapeModeChoice {
	choice := TableShapeReshapeModeChoice{ID: tableShapeModeChoiceID(receipt.ID, mode), Mode: mode, Label: label, State: tableshapecap.AvailabilitySupported}
	if !supported {
		choice.State, choice.ReasonCode, choice.Reason = tableshapecap.AvailabilityRefused, code, reason
	}
	return choice
}

func tableShapeModeChoiceID(catalogID, mode string) string {
	sum := sha256.Sum256([]byte("loom-table-shape-mode\x00" + catalogID + "\x00" + mode))
	return "tsch_" + hex.EncodeToString(sum[:16])
}

func roleAvailability(receipt tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole) tableshapecap.RoleAvailability {
	for _, entry := range receipt.Availability {
		if entry.Role == role {
			return entry
		}
	}
	return tableshapecap.RoleAvailability{Role: role, State: tableshapecap.AvailabilityRefused, ReasonCode: "ROLE_UNAVAILABLE", Message: "This role is unavailable in the current catalog."}
}

func tableShapeOperatorLabel(operator string) string {
	switch operator {
	case "ADD":
		return "Add"
	case "SUBTRACT":
		return "Subtract"
	case "MULTIPLY":
		return "Multiply"
	case "DIVIDE":
		return "Divide"
	default:
		return ""
	}
}

func tableShapePolicyLabel(role tableshapecap.ChoiceRole, policy string) string {
	switch role {
	case tableshapecap.RolePolicyDuplicate:
		switch policy {
		case "ERROR":
			return "Reject duplicate cells"
		case "SUM":
			return "Sum values"
		case "MIN":
			return "Take minimum"
		case "MAX":
			return "Take maximum"
		}
		return ""
	case tableshapecap.RolePolicyMissing:
		if policy == "NULL" {
			return "Use null for missing cells"
		}
		return "Reject missing cells"
	case tableshapecap.RolePolicyUnlisted:
		if policy == "EXCLUDE_WITH_EVIDENCE" {
			return "Exclude new categories with evidence"
		}
		return "Reject new categories"
	case tableshapecap.RolePolicyUnpivotNull:
		if policy == "DROP" {
			return "Drop null rows"
		}
		return "Preserve null rows"
	case tableshapecap.RolePolicyDerivedMissing:
		if policy == "PROPAGATE_NULL" {
			return "Propagate null"
		}
		return "Reject missing inputs"
	case tableshapecap.RolePolicyDivisionByZero:
		if policy == "NULL" {
			return "Return null for division by zero"
		}
		return "Reject division by zero"
	default:
		return ""
	}
}

func savedTableShapeSummary(shape *authoringv2.TableShape) (tableshapecap.SavedShapeSummary, error) {
	if shape == nil {
		return tableshapecap.SavedShapeSummary{}, nil
	}
	summary := tableshapecap.SavedShapeSummary{DerivedColumns: len(shape.Derived)}
	if shape.Reshape != nil {
		summary.ReshapeKind = shape.Reshape.Kind
	}
	payload, err := json.Marshal(shape)
	if err != nil {
		return tableshapecap.SavedShapeSummary{}, err
	}
	digest := sha256.Sum256(payload)
	summary.ShapeDigest = "sha256:" + hex.EncodeToString(digest[:])
	return summary, nil
}

func (s *Service) tableShapeCatalogForRequest(ctx context.Context, request TableShapeCatalogRequest, catalogID string) (tableShapeBase, tableshapecap.CatalogReceipt, error) {
	if s.config.CompileReceipt == nil {
		return tableShapeBase{}, tableshapecap.CatalogReceipt{}, unavailable("table-shape-capabilities", "CAPABILITY_UNAVAILABLE", "authorized table-shape compilation is not configured", nil)
	}
	if s.config.TableShapeCapabilities == nil {
		return tableShapeBase{}, tableshapecap.CatalogReceipt{}, unavailable("table-shape-capabilities", "CAPABILITY_STORE_UNAVAILABLE", "table-shape capability storage is not configured", nil)
	}
	current, err := s.loadTableShapeBaseContext(ctx, request)
	if err != nil {
		return tableShapeBase{}, tableshapecap.CatalogReceipt{}, err
	}
	if current.document.TableShape == nil {
		lookup := tableShapeCatalogLookup(request, current)
		catalog, lookupErr := s.config.TableShapeCapabilities.GetCatalogForLookup(ctx, lookup, catalogID)
		if lookupErr != nil && !errors.Is(lookupErr, tableshapecap.ErrNotFound) {
			return tableShapeBase{}, tableshapecap.CatalogReceipt{}, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "the table-shape capability catalog could not be loaded", lookupErr)
		}
		if lookupErr == nil {
			if base, reusable := s.reuseTableShapeCatalogBase(ctx, request, current, lookup, catalogID, catalog); reusable {
				return base, catalog, nil
			}
		}
	}
	base, err := s.compileTableShapeBase(ctx, request, current)
	if err != nil {
		return tableShapeBase{}, tableshapecap.CatalogReceipt{}, err
	}
	catalog, err := s.config.TableShapeCapabilities.GetCatalog(ctx, base.binding, catalogID)
	if err != nil {
		if errors.Is(err, tableshapecap.ErrNotFound) {
			return tableShapeBase{}, tableshapecap.CatalogReceipt{}, conflict("table-shape-resolution", "STALE_TABLE_SHAPE_CAPABILITY", "the table-shape catalog no longer matches the authorized draft and compiler output", nil, err)
		}
		return tableShapeBase{}, tableshapecap.CatalogReceipt{}, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "the table-shape capability catalog could not be loaded", err)
	}
	if err := catalog.Validate(); err != nil || catalog.ID != catalogID || catalog.Binding != base.binding {
		return tableShapeBase{}, tableshapecap.CatalogReceipt{}, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the table-shape catalog failed binding validation", nil, err)
	}
	return base, catalog, nil
}

func tableShapeCatalogLookup(request TableShapeCatalogRequest, current tableShapeBaseContext) tableshapecap.CatalogLookup {
	return tableshapecap.CatalogLookup{
		Project: projectid.Canonical(request.Project), ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		SnapshotToken: request.SnapshotToken, AuthorizationScope: current.snapshot.Identity.AuthorizationScopeDigest,
		SourceGeneration: current.snapshot.Identity.Generation, DraftVersion: uint64(current.owner.DraftVersion),
		DraftDigest: current.owner.DraftDigest, BaseDocumentDigest: current.baseDocumentDigest,
	}
}

func (s *Service) reuseTableShapeCatalogBase(ctx context.Context, request TableShapeCatalogRequest, current tableShapeBaseContext, lookup tableshapecap.CatalogLookup, catalogID string, catalog tableshapecap.CatalogReceipt) (tableShapeBase, bool) {
	if catalog.ID != catalogID || catalog.Validate() != nil || !lookup.Matches(catalog.Binding) {
		return tableShapeBase{}, false
	}
	receipt, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, catalog.Binding.BaseCompilationReceiptID)
	if err != nil || receipt == nil || receipt.ID != catalog.Binding.BaseCompilationReceiptID {
		return tableShapeBase{}, false
	}
	if err := s.validateReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return tableShapeBase{}, false
	}
	if _, err := s.verifyProposalReceipt(ctx, "table-shape-capabilities", receipt, request.Project, request.ExplorerID, request.SnapshotToken, current.snapshot, &current.workspace); err != nil {
		return tableShapeBase{}, false
	}
	if err := validateAuthorizedReceiptExecution(receipt, current.authorized); err != nil {
		return tableShapeBase{}, false
	}
	if err := validateReceiptOutputContract(receipt, request.OutputID); err != nil {
		return tableShapeBase{}, false
	}
	outputFingerprint := receipt.OutputFingerprints[request.OutputID]
	if outputFingerprint != catalog.Binding.OutputFingerprint || receipt.OutputContractDigest != catalog.Binding.CompilerSchemaDigest {
		return tableShapeBase{}, false
	}
	binding := tableshapecap.Binding{
		Project: lookup.Project, ExplorerID: lookup.ExplorerID, OutputID: lookup.OutputID,
		SnapshotToken: lookup.SnapshotToken, AuthorizationScope: lookup.AuthorizationScope,
		SourceGeneration: lookup.SourceGeneration, DraftVersion: lookup.DraftVersion,
		DraftDigest: lookup.DraftDigest, BaseDocumentDigest: lookup.BaseDocumentDigest,
		BaseCompilationReceiptID: receipt.ID, OutputFingerprint: outputFingerprint,
		CompilerSchemaDigest: receipt.OutputContractDigest,
	}
	if err := binding.Validate(); err != nil || binding != catalog.Binding {
		return tableShapeBase{}, false
	}
	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		return tableShapeBase{}, false
	}
	contract, ok := contracts.Output(request.OutputID)
	if !ok {
		return tableShapeBase{}, false
	}
	columns, facts, err := tableShapeCatalogColumns(contract)
	if err != nil {
		return tableShapeBase{}, false
	}
	availability, choices := buildTableShapeChoices(columns, facts, s.config.ScanTableShapeCategories != nil, current.document.TableShape)
	saved, err := savedTableShapeSummary(current.document.TableShape)
	if err != nil {
		return tableShapeBase{}, false
	}
	expectedCatalog, err := tableshapecap.NewCatalogReceipt(binding, columns, availability, choices, saved, catalog.CreatedAt)
	if err != nil || expectedCatalog.ID != catalog.ID || expectedCatalog.ContentDigest != catalog.ContentDigest {
		return tableShapeBase{}, false
	}
	return tableShapeBase{
		owner: current.owner, workspace: current.workspace, document: current.document,
		snapshot: current.snapshot, authorized: current.authorized,
		receipt: receipt, contract: contract, finalReceipt: receipt, finalContract: contract, binding: binding,
	}, true
}

func tableShapeTypeFact(column tableshapecap.PublicColumn) tableshapecap.TypeFact {
	return tableshapecap.TypeFact{LogicalType: column.LogicalType, Nullable: column.Nullable, UnitIdentity: column.UnitIdentity}
}

func unitIdentityString(identity *unit.UnitIdentity) (string, error) {
	if identity == nil {
		return "", nil
	}
	if !identity.Valid() {
		return "", fmt.Errorf("unit identity must contain a normalized system and code")
	}
	encoded, err := json.Marshal(*identity)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
