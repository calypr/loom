package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// ConstructionDeriveSourceCapability advertises the authorized populated root
// scalar fields a DERIVE step can consume through signed source choices,
// beside the existing Pivot, Group, and Filter source inputs. DERIVE operands
// feed arithmetic, so only integer and decimal scalar fields qualify.
type ConstructionDeriveSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionDeriveSourceChoice `json:"choices"`
}

// ConstructionDeriveSourceChoice is pinned to exactly six wire fields. The
// UI capabilities schema is strict, so pivot-style extras (occurrenceId,
// fhirType, valueType, isIdentifier, isReference) must never be added.
// ColumnID is the signed materialized column id from durable server-derived
// state, mirroring pivot's choice.columnId, so the UI can restore a saved
// source-backed DERIVE operand; it is "" while no live materialization exists.
type ConstructionDeriveSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

// ConstructionDeriveSourceSelection binds one server-signed root scalar field
// choice to the exact columnId the edited DERIVE step will consume as its
// left or right arithmetic operand.
type ConstructionDeriveSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

// maxDeriveSourceSelections bounds one proposal to the two arithmetic operand
// slots a single DERIVE step owns. This is the mirror of the pivot category
// cap discipline: the request channel can never grow past what one step can
// consume, and a duplicate columnId between the two slots is refused.
const maxDeriveSourceSelections = 2

func validateConstructionDeriveSourceSelections(selections []ConstructionDeriveSourceSelection) error {
	if len(selections) > maxDeriveSourceSelections {
		return fmt.Errorf("deriveSources accepts at most %d operand selections", maxDeriveSourceSelections)
	}
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := requireExactIdentity(selection.ChoiceID, fmt.Sprintf("deriveSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, fmt.Sprintf("deriveSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("deriveSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

// deriveSourceLogicalTypeIsNumeric reports whether a root scalar field can
// feed DERIVE arithmetic. The recipe compiler refuses any derived operand
// whose compiled kind is not integer or decimal, so discovery and admission
// here are pinned to exactly the kinds the compiler accepts (expression.
// ValueKind(mapping) rejects "number" and every non-numeric logical type).
func deriveSourceLogicalTypeIsNumeric(logicalType string) bool {
	switch strings.ToLower(strings.TrimSpace(logicalType)) {
	case "integer", "decimal":
		return true
	default:
		return false
	}
}

// deriveOperandColumnIDs returns the column ids the two arithmetic operands
// of a DERIVE operation consume. This is the documented divergence from the
// filter source channel, which binds one selection set to the single
// Filter.ColumnID; DERIVE consumes a list of up to two operand columns.
func deriveOperandColumnIDs(derive *authoringv2.ConstructionDerive) []string {
	if derive == nil {
		return nil
	}
	ids := make([]string, 0, 2)
	if derive.Left.Kind == authoringv2.ConstructionColumnOperand {
		ids = append(ids, derive.Left.ColumnID)
	}
	if derive.Right.Kind == authoringv2.ConstructionColumnOperand {
		ids = append(ids, derive.Right.ColumnID)
	}
	return ids
}

var deriveSourceReasons = rowSourceLaneReasons{
	codeStem:       "DERIVE",
	reachReason:    "the current construction prefix does not carry root scalar source fields to this stage",
	versionReason:  "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Derive source fields",
	routeReason:    "every root scalar field enumerated at this stage is a reference, repeated, non-root, or metadata path that the Derive step cannot reach",
	typeReason:     "every root scalar field enumerated at this stage is not an integer or decimal scalar that the Derive step can consume",
	observedReason: "every root scalar field enumerated for the Derive step at this stage has no observed values in the current dataset snapshot",
	genericCode:    "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS",
	genericReason:  "the selected stage has no authorized populated integer or decimal scalar FHIR fields that the Derive step can consume",
}

func (s *Service) constructionDeriveSourceCapability(
	ctx context.Context,
	base constructionBase,
	outputID, stageID string,
) (ConstructionDeriveSourceCapability, error) {
	result := ConstructionDeriveSourceCapability{StageID: stageID, Choices: []ConstructionDeriveSourceChoice{}}
	if _, found := constructionReceiptStage(base.stages, stageID); !found {
		result.ReasonCode = "STALE_STAGE_REFERENCE"
		result.Reason = "the selected stage is not present in the current compiled output"
		return result, nil
	}
	enumeration := rowSourceEnumeration{
		rootReachable:     constructionRootProjectionCanReachStage(base.construction, stageID),
		plannerConfigured: s.config.RowChoicePlanner != nil,
	}
	if enumeration.rootReachable && enumeration.plannerConfigured {
		choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
		if err != nil {
			return result, fmt.Errorf("list authorized root derive source choices for output %q: %w", outputID, err)
		}
		for _, choice := range choices {
			if rowSourceChoiceIsStaleVersion(choice, base.snapshot) {
				enumeration.skip(rowSourceSkipVersion)
				continue
			}
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				enumeration.skip(rowSourceSkipRoute)
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.snapshot, choice)
			if !ok {
				enumeration.skip(rowSourceCandidateSkipClass(base.snapshot, choice))
				continue
			}
			if !deriveSourceLogicalTypeIsNumeric(candidate.LogicalType) {
				enumeration.skip(rowSourceSkipType)
				continue
			}
			result.Choices = append(result.Choices, ConstructionDeriveSourceChoice{
				ChoiceID: choice.ChoiceID, FieldPath: choice.Path,
				Label:       constructionFHIRPathLabel(base.document.RootResourceType, choice.Path),
				LogicalType: candidate.LogicalType, IsPopulated: candidate.Populated,
				ColumnID: constructionOwnedDeriveSourceColumnID(base.construction, stageID, choice.Path),
			})
		}
	}
	sort.SliceStable(result.Choices, func(i, j int) bool {
		if result.Choices[i].FieldPath != result.Choices[j].FieldPath {
			return result.Choices[i].FieldPath < result.Choices[j].FieldPath
		}
		return result.Choices[i].ChoiceID < result.Choices[j].ChoiceID
	})
	if len(result.Choices) == 0 {
		result.ReasonCode, result.Reason = enumeration.decision(deriveSourceReasons)
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func (s *Service) resolveConstructionDeriveSource(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	acceptedStageID string,
	selection ConstructionDeriveSourceSelection,
) (authoringv2.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "the selected derive source field choice is invalid", nil, err)
	}
	if s.config.RowChoiceResolver == nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized derive source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.snapshot.Clone(), Route: base.document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceFieldGroupKey ||
		identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
		resolved.OccurrenceID != authoringv2.RootOccurrenceID {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
	}
	rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_DERIVE_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	if !deriveSourceLogicalTypeIsNumeric(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_DERIVE_SOURCE_FIELD", "the selected root field is not an integer or decimal scalar source field", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_DERIVE_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
	}
	label := constructionFHIRPathLabel(base.document.RootResourceType, facts.CanonicalPath)
	return authoringv2.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

// constructionCandidateWithDeriveSources resolves the signed field choices,
// derives the durable server-owned projection facts for the edited DERIVE
// step, and carries those slots through the row-preserving prefix. It mirrors
// the Filter source contract; the documented divergence is that DERIVE owns
// two arithmetic operand slots, so every selection columnId must be one of
// the step's COLUMN operand ids instead of a single filter column.
func (s *Service) constructionCandidateWithDeriveSources(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyDeriveSources := request.DeriveSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationDerive
	candidate = preserveConstructionOwnedInputs(base.construction, candidate, "")
	if !applyDeriveSources {
		return candidate, nil
	}
	derive := candidate.Steps[changedIndex].Operation.Derive
	if derive == nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_REQUIRES_DERIVE", "deriveSources can only be applied to a Derive step", nil)
	}
	if len(request.DeriveSources) == 0 {
		cleared, err := clearConstructionDeriveOwnedSources(base.construction, candidate, request.ChangedStepID)
		if err != nil {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_STAGE_UNAVAILABLE", "the Derive input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.stages, acceptedStageID); !found {
		return authoringv2.Construction{}, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the derive source choices are not bound to an accepted stage", nil, nil)
	}
	operandIDs := deriveOperandColumnIDs(derive)
	if len(request.DeriveSources) > len(operandIDs) {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_COLUMN_MISMATCH",
			fmt.Sprintf("deriveSources carries %d selections but the edited Derive step consumes %d column operands", len(request.DeriveSources), len(operandIDs)), nil)
	}
	for _, selection := range request.DeriveSources {
		if !containsConstructionColumnIDLocal(operandIDs, selection.ColumnID) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_COLUMN_MISMATCH",
				fmt.Sprintf("deriveSources columnId %q is not a column operand of the edited Derive step", selection.ColumnID), nil)
		}
	}

	resolved := make([]authoringv2.ConstructionSourceProjection, 0, len(request.DeriveSources))
	for _, selection := range request.DeriveSources {
		projection, err := s.resolveConstructionDeriveSource(ctx, base, request, acceptedStageID, selection)
		if err != nil {
			return authoringv2.Construction{}, err
		}
		resolved = append(resolved, projection)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ColumnID < resolved[j].ColumnID })

	droppedOwned := make(map[string]bool)
	for _, projection := range base.construction.SourceProjections {
		if projection.OwnerStepID == request.ChangedStepID {
			droppedOwned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == request.ChangedStepID {
			droppedOwned[projection.ColumnID] = true
		}
	}
	reowned := make(map[string]bool, len(resolved))
	for _, projection := range resolved {
		reowned[projection.ColumnID] = true
	}

	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(base.construction.SourceProjections)+len(resolved))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != request.ChangedStepID {
			projections = append(projections, projection)
		}
	}
	for _, projection := range base.construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != request.ChangedStepID && !hasPivotSourceProjection(projections, projection.ColumnID) {
			projections = append(projections, projection)
		}
	}
	projections = append(projections, resolved...)

	for _, projection := range resolved {
		if constructionRootFieldAlreadyProjected(base.document, candidate.Steps, projections, projection.ColumnID, projection.FieldPath) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_FIELD_ALREADY_PROJECTED",
				fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this operand", projection.FieldPath), nil)
		}
	}

	steps := make([]authoringv2.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index <= changedIndex; index++ {
		step := &steps[index]
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
		}
		outputs := make([]authoringv2.StageColumn, 0, len(step.Outputs)+len(resolved))
		for _, output := range step.Outputs {
			if droppedOwned[output.ID] && !reowned[output.ID] {
				continue
			}
			outputs = append(outputs, output)
		}
		for _, projection := range resolved {
			outputs = appendStageColumnIfAbsent(outputs, authoringv2.StageColumn{
				ID: projection.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		step.Outputs = outputs
	}

	candidate.Steps, candidate.SourceProjections = steps, projections
	if err := validateDeriveSourceColumns(candidate.Steps, request.ChangedStepID, request.DeriveSources); err != nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

func validateDeriveSourceColumns(steps []authoringv2.ConstructionStep, deriveID string, sources []ConstructionDeriveSourceSelection) error {
	index := constructionStepIndex(steps, deriveID)
	if index < 0 || steps[index].Operation.Derive == nil {
		return fmt.Errorf("the selected Derive step is unavailable")
	}
	operandIDs := deriveOperandColumnIDs(steps[index].Operation.Derive)
	for _, source := range sources {
		if !containsConstructionColumnIDLocal(operandIDs, source.ColumnID) {
			return fmt.Errorf("deriveSources columnId %q is not a column operand of the Derive step", source.ColumnID)
		}
	}
	return nil
}

func containsConstructionColumnIDLocal(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func clearConstructionDeriveOwnedSources(base, candidate authoringv2.Construction, deriveID string) (authoringv2.Construction, error) {
	deriveIndex := constructionStepIndex(candidate.Steps, deriveID)
	if deriveIndex < 0 {
		return authoringv2.Construction{}, fmt.Errorf("the edited Derive step is missing")
	}
	owned := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == deriveID {
			owned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == deriveID {
			owned[projection.ColumnID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= deriveIndex {
			outputs := make([]authoringv2.StageColumn, 0, len(step.Outputs))
			for _, output := range step.Outputs {
				if owned[output.ID] {
					continue
				}
				outputs = append(outputs, output)
			}
			step.Outputs = outputs
		}
		steps = append(steps, step)
	}
	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != deriveID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

// constructionOwnedDeriveSourceColumnID returns the durable server-derived
// materialized column for a root numeric field at one exact stage, mirroring
// constructionOwnedFilterSourceColumnID. A DERIVE owns the slot only while an
// owner DERIVE step consuming this exact stage still lists the projection
// column as one of its two arithmetic operand ids. A vanished, re-pointed, or
// foreign-owned materialization returns "", so the UI never renders a restore
// into a column the server can no longer sign.
func constructionOwnedDeriveSourceColumnID(construction authoringv2.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationDerive || step.Operation.Derive == nil ||
			!containsConstructionColumnIDLocal(deriveOperandColumnIDs(step.Operation.Derive), projection.ColumnID) ||
			constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

// ConstructionDeriveSourceFacts derives the server-owned projection facts for
// one root numeric field from the authorized snapshot plus generated FHIR
// metadata. Apply reauthorization and receipt reconcile share this single
// derivation so a persisted derive-owned claim can never drift from the facts
// the server would have signed it from. Column and owner identities stay
// claim-sourced, exactly as the Apply-side DeepEquals expects them.
func ConstructionDeriveSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	path := canonicalRowPath(fieldPath)
	if strings.TrimSpace(rootResourceType) == "" || path == "" {
		return authoringv2.ConstructionSourceProjection{}, false
	}
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		candidate, ok := uniqueRowSourceCandidate(snapshot, node.ID, rootResourceType, path)
		if !ok || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
			!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) ||
			!deriveSourceLogicalTypeIsNumeric(candidate.LogicalType) {
			continue
		}
		schemaIndex, err := fhirschema.GeneratedIndex()
		if err != nil {
			continue
		}
		facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), path)
		if err != nil || facts.CanonicalPath != path || facts.Shape != fhirschema.RowPathScalar ||
			facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference {
			continue
		}
		return authoringv2.ConstructionSourceProjection{
			OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: facts.CanonicalPath,
			FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType,
			Label: constructionFHIRPathLabel(rootResourceType, facts.CanonicalPath),
		}, true
	}
	return authoringv2.ConstructionSourceProjection{}, false
}

func constructionHasDeriveSourceInput(construction authoringv2.Construction) bool {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		if !isDeriveOwnedSourceProjection(construction, projection) {
			continue
		}
		return true
	}
	return false
}

func isDeriveOwnedSourceProjection(construction authoringv2.Construction, projection authoringv2.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == authoringv2.ConstructionOperationDerive && step.Operation.Derive != nil &&
		containsConstructionColumnIDLocal(deriveOperandColumnIDs(step.Operation.Derive), projection.ColumnID)
}

// reauthorizeConstructionDeriveSources re-derives the server-owned facts for
// every derive-owned projection from the live authorized snapshot so an Apply
// cannot stage a derived column whose operand field stopped qualifying after
// the proposal.
func (s *Service) reauthorizeConstructionDeriveSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	if s.config.RowChoicePlanner == nil {
		return unavailable("commands", "ROW_CHOICE_UNAVAILABLE", "authorized derive source choice planning is not configured", nil)
	}
	var choices []capability.RowChoice
	planned := false
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			return fmt.Errorf("derive source projection %q names a missing owner step", projection.ColumnID)
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationDerive {
			continue
		}
		if step.Operation.Derive == nil || !containsConstructionColumnIDLocal(deriveOperandColumnIDs(step.Operation.Derive), projection.ColumnID) {
			return fmt.Errorf("derive source projection %q is not a column operand of its owner Derive step", projection.ColumnID)
		}
		if !planned {
			listed, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
			if err != nil {
				return fmt.Errorf("list authorized root derive source choices for output %q: %w", outputID, err)
			}
			choices, planned = listed, true
		}
		matched := false
		for _, choice := range choices {
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) != canonicalRowPath(projection.FieldPath) {
				continue
			}
			if _, found := base.snapshot.Node(choice.NodeID); !found {
				continue
			}
			derived, ok := ConstructionDeriveSourceFacts(base.snapshot, base.document.RootResourceType, projection.FieldPath)
			if !ok {
				continue
			}
			expected := authoringv2.ConstructionSourceProjection{
				ColumnID: projection.ColumnID, OwnerStepID: projection.OwnerStepID,
				OccurrenceID: derived.OccurrenceID, FieldPath: derived.FieldPath,
				FHIRType: derived.FHIRType, LogicalType: derived.LogicalType, Label: derived.Label,
			}
			matched = reflect.DeepEqual(expected, projection)
			break
		}
		if !matched {
			return fmt.Errorf("derive source projection %q no longer matches an authorized populated root scalar field", projection.ColumnID)
		}
	}
	return nil
}
