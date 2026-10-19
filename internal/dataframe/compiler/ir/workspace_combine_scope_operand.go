package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type workspaceCombineOperandKind string

const (
	workspaceCombineOutputOperand    workspaceCombineOperandKind = "workspace-output"
	workspaceCombinePublishedOperand workspaceCombineOperandKind = "published"
	workspaceCombinePrivateOperand   workspaceCombineOperandKind = "private-aql-prefix"
)

// WorkspaceCombineScopeOperand is opaque provenance for one ordered input to
// a workspace Combine. Callers can only obtain one through a validating
// constructor below; its fields are copied so later resolver mutations cannot
// change the issued evidence.
type WorkspaceCombineScopeOperand struct {
	inputIndex            int
	ref                   PhysicalCombineInputRef
	kind                  workspaceCombineOperandKind
	identity              WholeRelationScopeIdentity
	sourcePlanFingerprint string
	provenanceDigest      string
	resolvedSchemaDigest  string
	resolvedColumns       []ResolvedClickHouseColumn
}

// NewWorkspaceOutputScopeOperand binds one exact same-workspace output ref to
// the already validated evidence for that output. The source plan may be an
// ordinary AQL plan or a previously composed ClickHouse plan.
func NewWorkspaceOutputScopeOperand(inputIndex int, ref PhysicalCombineInputRef, sourcePlan PhysicalPlan, sourceIdentity WholeRelationScopeIdentity, sourceEvidence WholeRelationScopeEvidence) (WorkspaceCombineScopeOperand, error) {
	if inputIndex < 0 || strings.TrimSpace(ref.WorkspaceOutputID) == "" || ref.WorkspaceOutputID != strings.TrimSpace(ref.WorkspaceOutputID) ||
		ref.TableID != "" || ref.RevisionID != "" || ref.OutputID != "" || ref.PrivateStageID != "" {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("workspace Combine output operand requires one exact sibling output reference")
	}
	if ref.WorkspaceOutputID != sourceIdentity.OutputID {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("workspace Combine output operand differs from its exact compiler output identity")
	}
	if !sourceEvidence.Matches(sourcePlan, sourceIdentity) {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("workspace Combine output operand lacks evidence for its exact source plan and identity")
	}
	if err := validateWholeRelationIdentityShape(sourceIdentity); err != nil {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("workspace Combine output operand identity: %w", err)
	}
	fingerprint := physicalPlanFingerprint(sourcePlan)
	provenance, err := workspaceCombineOperandDigest(workspaceCombineOutputOperand, inputIndex, ref, sourceIdentity, fingerprint, sourceEvidence.Digest(), nil)
	if err != nil {
		return WorkspaceCombineScopeOperand{}, err
	}
	return WorkspaceCombineScopeOperand{
		inputIndex: inputIndex, ref: ref, kind: workspaceCombineOutputOperand,
		identity: cloneWholeRelationScopeIdentity(sourceIdentity), sourcePlanFingerprint: fingerprint,
		provenanceDigest: provenance, resolvedSchemaDigest: sourceIdentity.SchemaDigest,
	}, nil
}

// NewPublishedWorkspaceScopeOperand binds an exact immutable published
// revision returned by the server resolver to the requested output scope.
// Call it at the resolver boundary, immediately after exact revision lookup,
// reauthorization, and schema validation.
func NewPublishedWorkspaceScopeOperand(inputIndex int, ref PhysicalCombineInputRef, resolved ResolvedClickHouseTable, expectedScope WholeRelationScopeIdentity) (WorkspaceCombineScopeOperand, error) {
	if inputIndex < 0 || strings.TrimSpace(ref.TableID) == "" || ref.TableID != strings.TrimSpace(ref.TableID) ||
		strings.TrimSpace(ref.RevisionID) == "" || ref.RevisionID != strings.TrimSpace(ref.RevisionID) ||
		strings.TrimSpace(ref.OutputID) == "" || ref.OutputID != strings.TrimSpace(ref.OutputID) ||
		ref.WorkspaceOutputID != "" || ref.PrivateStageID != "" {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("published workspace Combine operand requires one exact table, revision, and output reference")
	}
	if resolved.TableID != ref.TableID || resolved.RevisionID != ref.RevisionID || resolved.OutputID != ref.OutputID {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("published workspace Combine operand differs from its exact resolved table/revision/output")
	}
	if strings.TrimSpace(resolved.ReceiptID) == "" || resolved.ReceiptID != strings.TrimSpace(resolved.ReceiptID) ||
		strings.TrimSpace(resolved.SchemaDigest) == "" || resolved.SchemaDigest != strings.TrimSpace(resolved.SchemaDigest) ||
		strings.TrimSpace(resolved.ScopeDigest) == "" || resolved.ScopeDigest != strings.TrimSpace(resolved.ScopeDigest) ||
		strings.TrimSpace(resolved.PhysicalTable) == "" || resolved.PhysicalTable != strings.TrimSpace(resolved.PhysicalTable) {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("published workspace Combine operand is missing immutable resolver metadata")
	}
	if resolved.Project != expectedScope.Project || resolved.DatasetGeneration != expectedScope.DatasetGeneration {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("published workspace Combine operand differs from the exact project or dataset generation")
	}
	if err := validateWholeRelationIdentityShape(expectedScope); err != nil {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("published workspace Combine expected scope: %w", err)
	}
	if resolved.Unrestricted != (expectedScope.AuthScopeMode == "unrestricted") || !sameWholeRelationStrings(resolved.AuthResourcePaths, expectedScope.AuthResourcePaths) {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("published workspace Combine operand differs from the exact authorized scope")
	}
	if err := validateResolvedWorkspaceOperandColumns(resolved.Columns, resolved.Unrestricted); err != nil {
		return WorkspaceCombineScopeOperand{}, err
	}
	identity := WholeRelationScopeIdentity{
		OutputID: ref.OutputID, Project: resolved.Project, DatasetGeneration: resolved.DatasetGeneration,
		AuthScopeMode: expectedScope.AuthScopeMode, AuthResourcePaths: append([]string(nil), resolved.AuthResourcePaths...),
		SchemaDigest: resolved.SchemaDigest,
	}
	provenance, err := workspaceCombineOperandDigest(workspaceCombinePublishedOperand, inputIndex, ref, identity, "", "", &resolved)
	if err != nil {
		return WorkspaceCombineScopeOperand{}, err
	}
	return WorkspaceCombineScopeOperand{
		inputIndex: inputIndex, ref: ref, kind: workspaceCombinePublishedOperand,
		identity: identity, provenanceDigest: provenance, resolvedSchemaDigest: resolved.SchemaDigest,
		resolvedColumns: append([]ResolvedClickHouseColumn(nil), resolved.Columns...),
	}, nil
}

// NewPrivateAQLPrefixScopeOperand binds the composite plan's exact private
// AQL stage to the evidence issued for that complete source query.
func NewPrivateAQLPrefixScopeOperand(inputIndex int, ref PhysicalCombineInputRef, sourcePlan PhysicalPlan, sourceIdentity WholeRelationScopeIdentity, sourceEvidence WholeRelationScopeEvidence) (WorkspaceCombineScopeOperand, error) {
	if inputIndex < 0 || strings.TrimSpace(ref.PrivateStageID) == "" || ref.PrivateStageID != strings.TrimSpace(ref.PrivateStageID) ||
		ref.TableID != "" || ref.RevisionID != "" || ref.OutputID != "" || ref.WorkspaceOutputID != "" {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("private workspace Combine operand requires one exact AQL stage reference")
	}
	if sourcePlan.Engine == PhysicalEngineClickHouse || sourcePlan.ClickHouseCombine != nil || sourcePlan.ClickHousePrefix != nil ||
		sourcePlan.StageSequence == nil || sourcePlan.StageSequence.FinalStageID != ref.PrivateStageID {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("private workspace Combine operand differs from its exact final AQL stage")
	}
	if !sourceEvidence.Matches(sourcePlan, sourceIdentity) {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("private workspace Combine operand lacks evidence for its exact source plan and identity")
	}
	if err := validateWholeRelationIdentityShape(sourceIdentity); err != nil {
		return WorkspaceCombineScopeOperand{}, fmt.Errorf("private workspace Combine operand identity: %w", err)
	}
	fingerprint := canonicalAQLSourceFingerprint(sourcePlan)
	provenance, err := workspaceCombineOperandDigest(workspaceCombinePrivateOperand, inputIndex, ref, sourceIdentity, fingerprint, sourceEvidence.Digest(), nil)
	if err != nil {
		return WorkspaceCombineScopeOperand{}, err
	}
	return WorkspaceCombineScopeOperand{
		inputIndex: inputIndex, ref: ref, kind: workspaceCombinePrivateOperand,
		identity: cloneWholeRelationScopeIdentity(sourceIdentity), sourcePlanFingerprint: fingerprint,
		provenanceDigest: provenance, resolvedSchemaDigest: sourceIdentity.SchemaDigest,
	}, nil
}

// NewWorkspaceCombineScopeEvidence composes the exact authorized provenance
// of every ordered input and binds it to the actual optimized typed Combine
// plan and its compiler-final output identity.
func NewWorkspaceCombineScopeEvidence(actualPlan PhysicalPlan, finalizedIdentity WholeRelationScopeIdentity, orderedOperands []WorkspaceCombineScopeOperand) (WholeRelationScopeEvidence, error) {
	if err := validateWorkspaceCombinePhysicalPlan(actualPlan, finalizedIdentity); err != nil {
		return WholeRelationScopeEvidence{}, err
	}
	combine := actualPlan.ClickHouseCombine
	if combine == nil || len(orderedOperands) != len(combine.Inputs) {
		return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine evidence requires one opaque operand for every exact ordered input")
	}
	operandRecords := make([]string, len(orderedOperands))
	privateOperands := 0
	for index, operand := range orderedOperands {
		if operand.inputIndex != index || operand.provenanceDigest == "" || operand.resolvedSchemaDigest == "" || !samePhysicalCombineInputRef(operand.ref, combine.Inputs[index]) {
			return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine evidence operand %d does not match its exact ordered input", index)
		}
		if !sameWholeRelationScope(operand.identity, finalizedIdentity) {
			return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine evidence operand %d differs from the exact authorized output scope", index)
		}
		switch operand.kind {
		case workspaceCombineOutputOperand:
			if operand.ref.WorkspaceOutputID == "" || operand.ref.WorkspaceOutputID != operand.identity.OutputID || operand.sourcePlanFingerprint == "" {
				return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine output operand %d has incomplete source evidence", index)
			}
		case workspaceCombinePublishedOperand:
			if operand.ref.TableID == "" || operand.ref.RevisionID == "" || operand.ref.OutputID != operand.identity.OutputID {
				return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine published operand %d has incomplete exact revision provenance", index)
			}
			if err := validateWorkspaceCombineInputSchema(*combine, index, operand.resolvedColumns); err != nil {
				return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine published operand %d schema: %w", index, err)
			}
		case workspaceCombinePrivateOperand:
			privateOperands++
			if operand.ref.PrivateStageID == "" || operand.sourcePlanFingerprint == "" {
				return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine private operand %d has incomplete AQL source evidence", index)
			}
			prefix, err := physicalPlanAQLPrefix(actualPlan)
			if err != nil || canonicalAQLSourceFingerprint(prefix) != operand.sourcePlanFingerprint {
				return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine private operand %d differs from the exact typed AQL prefix", index)
			}
		default:
			return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine evidence operand %d has an unsupported provenance kind", index)
		}
		operandRecords[index] = operand.provenanceDigest
	}
	if actualPlan.ClickHousePrefix != nil && privateOperands != 1 {
		return WholeRelationScopeEvidence{}, fmt.Errorf("composite workspace Combine evidence requires exactly one proven private AQL prefix operand")
	}
	if actualPlan.ClickHousePrefix == nil && privateOperands != 0 {
		return WholeRelationScopeEvidence{}, fmt.Errorf("standalone workspace Combine evidence cannot carry a private AQL prefix operand")
	}
	encodedOperands, err := json.Marshal(operandRecords)
	if err != nil {
		return WholeRelationScopeEvidence{}, fmt.Errorf("marshal workspace Combine operand provenance: %w", err)
	}
	operandHash := sha256.Sum256(encodedOperands)
	operandProof := hex.EncodeToString(operandHash[:])
	fingerprint := physicalPlanFingerprint(actualPlan)
	if fingerprint == "" {
		return WholeRelationScopeEvidence{}, fmt.Errorf("workspace Combine evidence could not fingerprint the actual physical plan")
	}
	identity := cloneWholeRelationScopeIdentity(finalizedIdentity)
	const issuerKind = "workspace-combine"
	digest, err := wholeRelationScopeEvidenceDigest(fingerprint, identity, issuerKind, operandProof)
	if err != nil {
		return WholeRelationScopeEvidence{}, err
	}
	return WholeRelationScopeEvidence{
		planFingerprint: fingerprint, identity: identity, issuerKind: issuerKind,
		operandProof: operandProof, digest: digest,
	}, nil
}

func validateWorkspaceCombinePhysicalPlan(plan PhysicalPlan, identity WholeRelationScopeIdentity) error {
	if err := validateWholeRelationIdentityShape(identity); err != nil {
		return err
	}
	if plan.Engine != PhysicalEngineClickHouse || plan.ClickHouseCombine == nil || plan.PreviewSourceWindowByRootID || len(plan.DeferredExpressionLets) != 0 {
		return fmt.Errorf("workspace Combine evidence requires the exact unbounded typed ClickHouse plan")
	}
	if plan.ClickHousePrefix != nil {
		if err := plan.Validate(); err != nil {
			return fmt.Errorf("validate composite workspace Combine physical plan: %w", err)
		}
		prefix, err := physicalPlanAQLPrefix(plan)
		if err != nil {
			return err
		}
		if err := validateWholeRelationIdentity(prefix, identity); err != nil {
			return fmt.Errorf("workspace Combine AQL prefix identity: %w", err)
		}
		if err := validateWholeRelationPlan(prefix, identity); err != nil {
			return fmt.Errorf("workspace Combine AQL prefix scope: %w", err)
		}
		return nil
	}
	if len(plan.ClickHouseCombine.Inputs) == 0 {
		return fmt.Errorf("workspace Combine evidence requires typed inputs")
	}
	hasWorkspaceOutput := false
	for _, input := range plan.ClickHouseCombine.Inputs {
		if input.WorkspaceOutputID != "" {
			hasWorkspaceOutput = true
		}
	}
	if hasWorkspaceOutput {
		if err := plan.ValidateForWorkspaceCompilation(); err != nil {
			return fmt.Errorf("validate workspace Combine physical plan: %w", err)
		}
	} else if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate workspace Combine physical plan: %w", err)
	}
	if len(plan.BindVars) != 0 || len(plan.Operations) != 0 || plan.StageSequence != nil {
		return fmt.Errorf("standalone workspace Combine evidence cannot carry unvalidated AQL source operations")
	}
	return nil
}

func physicalPlanAQLPrefix(plan PhysicalPlan) (PhysicalPlan, error) {
	if plan.Engine != PhysicalEngineClickHouse || plan.ClickHousePrefix == nil || plan.ClickHouseCombine == nil {
		return PhysicalPlan{}, fmt.Errorf("workspace Combine has no typed AQL prefix")
	}
	prefix := ClonePhysicalPlan(plan)
	prefix.Engine = ""
	prefix.ClickHouseCombine = nil
	prefix.ClickHousePrefix = nil
	if prefix.StageSequence != nil && prefix.StageSequence.OutputAuthResourcePathBindKey != "" {
		delete(prefix.BindVars, prefix.StageSequence.OutputAuthResourcePathBindKey)
		prefix.StageSequence.OutputAuthResourcePathBindKey = ""
	}
	return prefix, nil
}

func canonicalAQLSourceFingerprint(plan PhysicalPlan) string {
	normalized := ClonePhysicalPlan(plan)
	normalized.Engine = ""
	normalized.ClickHouseCombine = nil
	normalized.ClickHousePrefix = nil
	if normalized.StageSequence != nil && normalized.StageSequence.OutputAuthResourcePathBindKey != "" {
		delete(normalized.BindVars, normalized.StageSequence.OutputAuthResourcePathBindKey)
		normalized.StageSequence.OutputAuthResourcePathBindKey = ""
	}
	return physicalPlanFingerprint(normalized)
}

func sameWholeRelationScope(left, right WholeRelationScopeIdentity) bool {
	return left.Project == right.Project && left.DatasetGeneration == right.DatasetGeneration &&
		left.AuthScopeMode == right.AuthScopeMode && sameWholeRelationStrings(left.AuthResourcePaths, right.AuthResourcePaths)
}

func samePhysicalCombineInputRef(left, right PhysicalCombineInputRef) bool {
	return left.TableID == right.TableID && left.RevisionID == right.RevisionID && left.OutputID == right.OutputID &&
		left.WorkspaceOutputID == right.WorkspaceOutputID && left.PrivateStageID == right.PrivateStageID
}

func validateResolvedWorkspaceOperandColumns(columns []ResolvedClickHouseColumn, unrestricted bool) error {
	if len(columns) == 0 {
		return fmt.Errorf("published workspace Combine operand has an empty resolved schema")
	}
	seenIDs, seenNames := map[string]bool{}, map[string]bool{}
	rowIdentity := false
	authResourcePath := false
	for index, column := range columns {
		reservedWithoutStableID := column.ID == "" && (column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id")
		if (!reservedWithoutStableID && (strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID))) ||
			strings.TrimSpace(column.Name) == "" || strings.TrimSpace(column.ClickHouseType) == "" ||
			(column.ID != "" && seenIDs[column.ID]) || seenNames[column.Name] {
			return fmt.Errorf("published workspace Combine operand has an invalid resolved schema column %d", index)
		}
		if column.ID != "" {
			seenIDs[column.ID] = true
		}
		seenNames[column.Name] = true
		rowIdentity = rowIdentity || column.Name == "__loom_row_id"
		authResourcePath = authResourcePath || column.Name == "auth_resource_path"
	}
	if !rowIdentity || (!unrestricted && !authResourcePath) {
		return fmt.Errorf("published workspace Combine operand is missing required row identity or authorization metadata")
	}
	return nil
}

func validateWorkspaceCombineInputSchema(combine PhysicalClickHouseCombine, inputIndex int, columns []ResolvedClickHouseColumn) error {
	byID := make(map[string]ResolvedClickHouseColumn, len(columns))
	for _, column := range columns {
		if column.ID != "" {
			byID[column.ID] = column
		}
	}
	for _, key := range combine.Keys {
		columnID := key.RightColumnID
		if inputIndex == 0 {
			columnID = key.LeftColumnID
		} else if inputIndex > 1 {
			continue
		}
		if _, ok := byID[columnID]; !ok {
			return fmt.Errorf("typed Combine key references a missing resolved input column")
		}
	}
	for _, projection := range combine.Projections {
		if projection.InputIndex != inputIndex {
			continue
		}
		column, ok := byID[projection.InputColumnID]
		if !ok {
			return fmt.Errorf("typed Combine projection references a missing resolved input column")
		}
		var output *PhysicalCombineOutputColumn
		for index := range combine.Outputs {
			if combine.Outputs[index].ID == projection.OutputColumnID {
				output = &combine.Outputs[index]
				break
			}
		}
		if output == nil || column.LogicalType != "" && column.LogicalType != output.LogicalType {
			return fmt.Errorf("typed Combine projection differs from the resolved input logical schema")
		}
	}
	return nil
}

func workspaceCombineOperandDigest(kind workspaceCombineOperandKind, index int, ref PhysicalCombineInputRef, identity WholeRelationScopeIdentity, planFingerprint, evidenceDigest string, resolved *ResolvedClickHouseTable) (string, error) {
	var resolverTuple any
	if resolved != nil {
		resolverTuple = struct {
			TableID           string
			RevisionID        string
			OutputID          string
			Project           string
			DatasetGeneration string
			ReceiptID         string
			SchemaDigest      string
			ScopeDigest       string
			PhysicalTable     string
			Unrestricted      bool
			AuthResourcePaths []string
			Columns           []ResolvedClickHouseColumn
		}{
			TableID: resolved.TableID, RevisionID: resolved.RevisionID, OutputID: resolved.OutputID,
			Project: resolved.Project, DatasetGeneration: resolved.DatasetGeneration,
			ReceiptID: resolved.ReceiptID, SchemaDigest: resolved.SchemaDigest, ScopeDigest: resolved.ScopeDigest,
			PhysicalTable: resolved.PhysicalTable, Unrestricted: resolved.Unrestricted,
			AuthResourcePaths: append([]string(nil), resolved.AuthResourcePaths...), Columns: append([]ResolvedClickHouseColumn(nil), resolved.Columns...),
		}
	}
	encoded, err := json.Marshal(struct {
		Kind               workspaceCombineOperandKind
		InputIndex         int
		Reference          PhysicalCombineInputRef
		Identity           WholeRelationScopeIdentity
		SourcePlan         string
		SourceEvidence     string
		ResolverProvenance any
	}{kind, index, ref, identity, planFingerprint, evidenceDigest, resolverTuple})
	if err != nil {
		return "", fmt.Errorf("marshal workspace Combine operand provenance: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
