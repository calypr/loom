package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/projectid"
)

// WholeRelationScopeIdentity is compiler-owned identity for a finalized
// whole-relation output. The lowerer supplies OutputID and the digest of its
// final schema after constructing the physical plan.
type WholeRelationScopeIdentity struct {
	OutputID          string
	Project           string
	DatasetGeneration string
	AuthScopeMode     string
	AuthResourcePaths []string
	SchemaDigest      string
}

// WholeRelationScopeEvidence is an opaque proof that one exact typed physical
// plan reads only the complete authorized source scope and has no bounded
// output window. Issuance always means whole-scope evidence; it does not attest
// row-level auth_resource_path metadata on grouped output rows.
type WholeRelationScopeEvidence struct {
	planFingerprint string
	identity        WholeRelationScopeIdentity
	issuerKind      string
	operandProof    string
	digest          string
}

// NewWholeRelationScopeEvidence issues evidence only for complete unbounded
// typed AQL source plans whose reads have explicit scope contracts. The plan
// fingerprint is computed here so a caller cannot assert a digest for a
// different plan. Cohort and coded group stages use their typed custom-source
// contracts in addition to generic operation-graph scope validation.
func NewWholeRelationScopeEvidence(plan PhysicalPlan, identity WholeRelationScopeIdentity) (WholeRelationScopeEvidence, error) {
	if err := plan.Validate(); err != nil {
		return WholeRelationScopeEvidence{}, fmt.Errorf("whole-relation evidence physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != PhysicalEngineAQL {
		return WholeRelationScopeEvidence{}, fmt.Errorf("ordinary whole-relation evidence requires an AQL plan; use a typed workspace-combine operand for ClickHouse plans")
	}
	if err := validateWholeRelationIdentity(plan, identity); err != nil {
		return WholeRelationScopeEvidence{}, err
	}
	if err := validateWholeRelationPlan(plan, identity); err != nil {
		return WholeRelationScopeEvidence{}, err
	}
	fingerprint := physicalPlanFingerprint(plan)
	if fingerprint == "" {
		return WholeRelationScopeEvidence{}, fmt.Errorf("whole-relation evidence could not fingerprint the physical plan")
	}
	identity = cloneWholeRelationScopeIdentity(identity)
	const issuerKind = "aql"
	digest, err := wholeRelationScopeEvidenceDigest(fingerprint, identity, issuerKind, "")
	if err != nil {
		return WholeRelationScopeEvidence{}, err
	}
	return WholeRelationScopeEvidence{planFingerprint: fingerprint, identity: identity, issuerKind: issuerKind, digest: digest}, nil
}

// Matches reports whether this evidence was issued for the exact current plan
// and output identity. It is safe to persist Digest only after this check.
func (e WholeRelationScopeEvidence) Matches(plan PhysicalPlan, identity WholeRelationScopeIdentity) bool {
	if e.digest == "" || e.planFingerprint == "" || physicalPlanFingerprint(plan) != e.planFingerprint ||
		!sameWholeRelationScopeIdentity(e.identity, identity) {
		return false
	}
	switch e.issuerKind {
	case "aql":
		return validateWholeRelationIdentity(plan, identity) == nil && validateWholeRelationPlan(plan, identity) == nil
	case "workspace-combine":
		return e.operandProof != "" && validateWorkspaceCombinePhysicalPlan(plan, identity) == nil
	default:
		return false
	}
}

// Digest returns a stable digest of the validated plan fingerprint and exact
// output identity. The zero value returns an empty digest.
func (e WholeRelationScopeEvidence) Digest() string {
	return e.digest
}

func validateWholeRelationIdentity(plan PhysicalPlan, identity WholeRelationScopeIdentity) error {
	if err := validateWholeRelationIdentityShape(identity); err != nil {
		return err
	}
	if plan.Engine == PhysicalEngineClickHouse && plan.ClickHousePrefix == nil {
		return fmt.Errorf("standalone ClickHouse identity must be checked against typed input provenance")
	}
	project, ok := plan.BindVars[physicalScopeProjectBind].(string)
	if !ok || project != identity.Project {
		return fmt.Errorf("whole-relation evidence project differs from the exact physical project binding")
	}
	generation, ok := plan.BindVars[physicalScopeDatasetGenerationBind].(string)
	if !ok || generation != identity.DatasetGeneration {
		return fmt.Errorf("whole-relation evidence dataset generation differs from the exact physical binding")
	}
	paths, ok := plan.BindVars[physicalScopeAuthPathsBind].([]string)
	if !ok || !sameWholeRelationStrings(paths, identity.AuthResourcePaths) {
		return fmt.Errorf("whole-relation evidence authorization paths differ from the exact physical binding")
	}
	unrestricted, ok := plan.BindVars[physicalScopeAuthPathsUnrestrictedBind].(bool)
	if !ok || unrestricted != (identity.AuthScopeMode == "unrestricted") {
		return fmt.Errorf("whole-relation evidence authorization mode differs from the exact physical binding")
	}
	if allowed, exists := plan.BindVars[physicalScopeAllowedBind]; exists {
		value, ok := allowed.(bool)
		if !ok || !value {
			return fmt.Errorf("whole-relation evidence requires the authorized scope to be enabled")
		}
	}
	return nil
}

func validateWholeRelationIdentityShape(identity WholeRelationScopeIdentity) error {
	if strings.TrimSpace(identity.OutputID) == "" || identity.OutputID != strings.TrimSpace(identity.OutputID) {
		return fmt.Errorf("whole-relation evidence requires an exact output ID")
	}
	if strings.TrimSpace(identity.Project) == "" || identity.Project != strings.TrimSpace(identity.Project) {
		return fmt.Errorf("whole-relation evidence requires an exact authorized project")
	}
	if identity.DatasetGeneration != strings.TrimSpace(identity.DatasetGeneration) {
		return fmt.Errorf("whole-relation evidence dataset generation must be exact")
	}
	if strings.TrimSpace(identity.SchemaDigest) == "" || identity.SchemaDigest != strings.TrimSpace(identity.SchemaDigest) {
		return fmt.Errorf("whole-relation evidence requires the finalized output schema digest")
	}
	switch identity.AuthScopeMode {
	case "restricted", "unrestricted":
	default:
		return fmt.Errorf("whole-relation evidence has unsupported authorization scope mode %q", identity.AuthScopeMode)
	}
	for index, path := range identity.AuthResourcePaths {
		if strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) {
			return fmt.Errorf("whole-relation evidence authorization path %d is not canonical", index)
		}
		if index > 0 && identity.AuthResourcePaths[index-1] >= path {
			return fmt.Errorf("whole-relation evidence authorization paths must be sorted and unique")
		}
	}
	return nil
}

func validateWholeRelationPlan(plan PhysicalPlan, identity WholeRelationScopeIdentity) error {
	if plan.Engine != "" && plan.Engine != PhysicalEngineAQL {
		return fmt.Errorf("whole-relation evidence requires a typed AQL source plan")
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil || len(plan.DeferredExpressionLets) != 0 {
		return fmt.Errorf("whole-relation evidence cannot attest a ClickHouse combine or deferred source plan")
	}
	if plan.PreviewSourceWindowByRootID {
		return fmt.Errorf("whole-relation evidence cannot attest a bounded source window")
	}
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalLimitOp {
			return fmt.Errorf("whole-relation evidence cannot attest physical LIMIT operation %d", index)
		}
	}
	if plan.StageSequence == nil {
		if len(plan.Operations) == 1 && plan.Operations[0].Kind == PhysicalGroupRowsOp && plan.Operations[0].GroupRows != nil {
			if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
				return fmt.Errorf("whole-relation GROUP_ROWS source scope: %w", err)
			}
			if err := validateWholeRelationGroupRows(plan.Operations[0].GroupRows, plan.BindVars, identity); err != nil {
				return err
			}
			return nil
		}
		if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
			return fmt.Errorf("whole-relation source scope: %w", err)
		}
		return nil
	}
	sequence := plan.StageSequence
	if sequence.PreviewLimitBindKey != "" || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		return fmt.Errorf("whole-relation evidence cannot attest a bounded construction preview")
	}
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		return fmt.Errorf("whole-relation source scope: %w", err)
	}
	for index := range sequence.Stages {
		stage := &sequence.Stages[index]
		switch stage.Kind {
		case PhysicalStageGroupOp, PhysicalStagePivotOp:
		case PhysicalStageCohortGroupOp:
			if stage.CohortGroup == nil {
				return fmt.Errorf("whole-relation cohort stage %q has no typed source contract", stage.ID)
			}
			if err := validateWholeRelationGroupRows(&stage.CohortGroup.Rows, plan.BindVars, identity); err != nil {
				return fmt.Errorf("whole-relation cohort stage %q: %w", stage.ID, err)
			}
		case PhysicalStageCodedGroupOp:
			if stage.CodedGroup == nil {
				return fmt.Errorf("whole-relation coded-group stage %q has no typed source contract", stage.ID)
			}
			if err := validateWholeRelationCodedGroup(plan, *stage.CodedGroup); err != nil {
				return fmt.Errorf("whole-relation coded-group stage %q: %w", stage.ID, err)
			}
		}
	}
	return nil
}

func validateWholeRelationGroupRows(rows *PhysicalGroupRows, bindVars map[string]any, identity WholeRelationScopeIdentity) error {
	if rows == nil {
		return fmt.Errorf("whole-relation GROUP_ROWS source is missing")
	}
	if rows.LimitBindKey != "" {
		return fmt.Errorf("whole-relation GROUP_ROWS source cannot use a limit")
	}
	project, projectOK := bindVars[rows.ProjectBindKey].(string)
	resourceProject, resourceProjectOK := bindVars[rows.ResourceProjectBindKey].(string)
	generation, generationOK := bindVars[rows.DatasetGenerationBindKey].(string)
	paths, pathsOK := bindVars[rows.AuthResourcePathsBindKey].([]string)
	unrestricted, unrestrictedOK := bindVars[rows.AuthUnrestrictedBindKey].(bool)
	if !projectOK || projectid.Canonical(project) != projectid.Canonical(identity.Project) ||
		!resourceProjectOK || projectid.Canonical(resourceProject) != projectid.Canonical(identity.Project) {
		return fmt.Errorf("whole-relation GROUP_ROWS source project differs from the authorized project")
	}
	if !generationOK || generation != identity.DatasetGeneration {
		return fmt.Errorf("whole-relation GROUP_ROWS source generation differs from the authorized generation")
	}
	if !pathsOK || !sameWholeRelationStrings(paths, identity.AuthResourcePaths) ||
		!unrestrictedOK || unrestricted != (identity.AuthScopeMode == "unrestricted") {
		return fmt.Errorf("whole-relation GROUP_ROWS source authorization differs from the exact authorized scope")
	}
	return nil
}

func validateWholeRelationCodedGroup(plan PhysicalPlan, coded PhysicalStageCodedGroup) error {
	if !coded.SourceRowsUnique || coded.SourceIdentityColumn != "_key" {
		return fmt.Errorf("CodedGroup source must retain one exact root identity per source row")
	}
	var root *PhysicalRootScan
	for index := range plan.Operations {
		if plan.Operations[index].Kind == PhysicalRootScanOp && plan.Operations[index].RootScan != nil {
			root = plan.Operations[index].RootScan
			break
		}
	}
	if root == nil || root.CollectionBindKey != coded.RootCollectionBindKey {
		return fmt.Errorf("CodedGroup DOCUMENT lookup is not tied to the exact scoped root scan")
	}
	collection, ok := plan.BindVars[coded.RootCollectionBindKey].(string)
	if !ok || collection != plan.Source.ResourceType || collection != coded.ResourceType {
		return fmt.Errorf("CodedGroup root lookup differs from the scoped source resource")
	}
	return nil
}

func cloneWholeRelationScopeIdentity(identity WholeRelationScopeIdentity) WholeRelationScopeIdentity {
	identity.AuthResourcePaths = append([]string(nil), identity.AuthResourcePaths...)
	return identity
}

func sameWholeRelationScopeIdentity(left, right WholeRelationScopeIdentity) bool {
	return left.OutputID == right.OutputID && left.Project == right.Project &&
		left.DatasetGeneration == right.DatasetGeneration && left.AuthScopeMode == right.AuthScopeMode &&
		sameWholeRelationStrings(left.AuthResourcePaths, right.AuthResourcePaths) && left.SchemaDigest == right.SchemaDigest
}

func sameWholeRelationStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func wholeRelationScopeEvidenceDigest(planFingerprint string, identity WholeRelationScopeIdentity, issuerKind, operandProof string) (string, error) {
	encoded, err := json.Marshal(struct {
		Version         int
		PlanFingerprint string
		IssuerKind      string
		OperandProof    string
		Identity        WholeRelationScopeIdentity
	}{Version: 1, PlanFingerprint: planFingerprint, IssuerKind: issuerKind, OperandProof: operandProof, Identity: identity})
	if err != nil {
		return "", fmt.Errorf("marshal whole-relation scope evidence identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
