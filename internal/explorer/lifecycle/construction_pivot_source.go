package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func validateConstructionPivotSourceSelections(selections []ConstructionPivotSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := requireExactIdentity(selection.ChoiceID, fmt.Sprintf("pivotSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, fmt.Sprintf("pivotSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("pivotSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

var pivotSourceReasons = rowSourceLaneReasons{
	codeStem:       "PIVOT",
	reachReason:    "the current construction prefix does not carry root scalar source fields to this stage, and no active related-record source field is offered either",
	versionReason:  "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Pivot source fields",
	routeReason:    "every root scalar field enumerated at this stage is a reference, repeated, non-root, or metadata path that cannot be carried into Pivot",
	typeReason:     "every root scalar field enumerated at this stage has a logical type that cannot be carried into Pivot",
	observedReason: "every root scalar field enumerated for Pivot at this stage has no observed values in the current dataset snapshot",
	genericCode:    "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS",
	genericReason:  "the selected stage has no authorized populated scalar FHIR fields that can be carried into Pivot",
}

func (s *Service) constructionPivotSourceCapability(
	ctx context.Context,
	base constructionBase,
	outputID, stageID string,
) (ConstructionPivotSourceCapability, error) {
	result := ConstructionPivotSourceCapability{StageID: stageID, Choices: []ConstructionPivotSourceChoice{}}
	stage, found := constructionReceiptStage(base.stages, stageID)
	if !found {
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
			return result, fmt.Errorf("list authorized root Pivot source choices for output %q: %w", outputID, err)
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
			result.Choices = append(result.Choices, ConstructionPivotSourceChoice{
				ChoiceID: choice.ChoiceID, ColumnID: constructionOwnedRootProjectionColumnID(base.construction, stageID, choice.Path),
				OccurrenceID: choice.OccurrenceID, FieldPath: choice.Path, Label: constructionFieldDisplayLabel(base.document.RootResourceType, choice.Path, candidate.Label, choice.Label),
				FHIRType: choice.FHIRType, LogicalType: candidate.LogicalType, ValueType: choice.ValueType,
				IsIdentifier: fhirIdentifierPath(choice.Path), IsPopulated: candidate.Populated,
			})
		}
	}
	if stage.ActiveRelatedRecord != nil && constructionStageSupportsRelatedField(stage) {
		active := stage.ActiveRelatedRecord
		candidates := make([]capability.Candidate, 0)
		for _, candidate := range base.snapshot.Candidates {
			if candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType || !candidate.Populated ||
				(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") ||
				len(candidate.RepeatedBoundaries) != 0 || !containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
				!relatedFieldPathExecutable(candidate.FieldPath) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
				continue
			}
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		for _, candidate := range candidates {
			proved, err := proveConstructionCandidate(ctx, base.authorized, active.TargetResourceType, candidate, nil)
			if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
				(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 ||
				!relatedFieldPathExecutable(proved.FieldPath) || !relatedFieldLogicalTypeExecutable(proved.LogicalType) {
				continue
			}
			choice, err := capability.NewConstructionRelatedFieldChoice(base.snapshot.Token, stageID, proved)
			if err != nil {
				continue
			}
			fhirType := constructionFHIRScalarType(active.TargetResourceType, proved.FieldPath)
			if fhirType == "" {
				continue
			}
			result.Choices = append(result.Choices, ConstructionPivotSourceChoice{
				ChoiceID: choice.ChoiceID, ColumnID: constructionOwnedRelatedFieldColumnID(base.construction, stageID, proved),
				OccurrenceID: "activeRelatedRecord", FieldPath: proved.FieldPath,
				Label:    constructionFieldDisplayLabel(active.TargetResourceType, proved.FieldPath, proved.Label),
				FHIRType: fhirType, LogicalType: proved.LogicalType, ValueType: proved.LogicalType,
				IsIdentifier: fhirIdentifierPath(proved.FieldPath), IsPopulated: proved.Populated,
			})
		}
	}
	sort.SliceStable(result.Choices, func(i, j int) bool {
		if result.Choices[i].OccurrenceID != result.Choices[j].OccurrenceID {
			return result.Choices[i].OccurrenceID < result.Choices[j].OccurrenceID
		}
		if result.Choices[i].FieldPath != result.Choices[j].FieldPath {
			return result.Choices[i].FieldPath < result.Choices[j].FieldPath
		}
		return result.Choices[i].ChoiceID < result.Choices[j].ChoiceID
	})
	if len(result.Choices) == 0 {
		result.ReasonCode, result.Reason = enumeration.decision(pivotSourceReasons)
		return result, nil
	}
	result.Supported = true
	return result, nil
}

// rowSourceSkipClass categorizes one root field a signed source lane skipped
// during its enumeration pass. When nothing is advertised after the full
// assembly, the lane-level reasonCode names the class if, and only if, every
// recorded skip shares one class; mixed or unmappable skips keep the existing
// generic NO_ELIGIBLE_POPULATED_* verdict (ROW-SOURCE-03 clause: "precise
// reasons for unsupported version, type, route, or stage" plus "distinguish
// empty data ... inventory errors").
type rowSourceSkipClass int

const (
	rowSourceSkipObserved rowSourceSkipClass = iota
	rowSourceSkipType
	rowSourceSkipRoute
	rowSourceSkipVersion
	rowSourceSkipInventory
)

// rowSourceEnumeration carries the gate facts each lane already holds at the
// moment it silently continues, so an empty verdict can name its real cause.
type rowSourceEnumeration struct {
	rootReachable     bool
	plannerConfigured bool
	skips             map[rowSourceSkipClass]int
}

func (e *rowSourceEnumeration) skip(class rowSourceSkipClass) {
	if e.skips == nil {
		e.skips = make(map[rowSourceSkipClass]int)
	}
	e.skips[class]++
}

// rowSourceLaneReasons is the per-lane wording bundle; the code strings are
// derived from the stable lane stem so the four signed lanes stay parallel.
type rowSourceLaneReasons struct {
	codeStem       string
	reachReason    string
	versionReason  string
	routeReason    string
	typeReason     string
	observedReason string
	genericCode    string
	genericReason  string
}

func (e rowSourceEnumeration) decision(reasons rowSourceLaneReasons) (string, string) {
	if !e.rootReachable {
		return "ROOT_SOURCE_PROJECTION_UNAVAILABLE", reasons.reachReason
	}
	if !e.plannerConfigured {
		return "ROW_CHOICE_UNAVAILABLE", "authorized scalar source choices are not configured"
	}
	if class, single := e.onlySkipClass(); single {
		switch class {
		case rowSourceSkipVersion:
			return "STALE_" + reasons.codeStem + "_SOURCE_SNAPSHOT", reasons.versionReason
		case rowSourceSkipRoute:
			return "NO_ROUTE_ELIGIBLE_" + reasons.codeStem + "_SOURCE_FIELDS", reasons.routeReason
		case rowSourceSkipType:
			return "NO_TYPE_ELIGIBLE_" + reasons.codeStem + "_SOURCE_FIELDS", reasons.typeReason
		case rowSourceSkipObserved:
			return "NO_OBSERVED_VALUES_" + reasons.codeStem + "_SOURCE_FIELDS", reasons.observedReason
		}
	}
	return reasons.genericCode, reasons.genericReason
}

// onlySkipClass reports the single reason class when every recorded skip
// shares it. The inventory class (the planner advertised a choice whose
// snapshot candidate is missing or duplicated) is intentionally not mapped to
// a new code: per-lane inventory-error state is the separate S-B slice, and
// mixed causes stay on the honest generic fallback.
func (e rowSourceEnumeration) onlySkipClass() (rowSourceSkipClass, bool) {
	if len(e.skips) != 1 {
		return 0, false
	}
	for class, count := range e.skips {
		if count == 0 || class == rowSourceSkipInventory {
			return 0, false
		}
		return class, true
	}
	return 0, false
}

// rowSourceCandidateSkipClass mirrors the fused gates of
// constructionGroupSourceCandidate for diagnosis only: the lane still decides
// its advertised set with the fused helper, so this never changes which
// fields are offered — only which reason an empty lane reports.
func rowSourceCandidateSkipClass(snapshot capability.Snapshot, choice capability.RowChoice) rowSourceSkipClass {
	var found *capability.Candidate
	for index := range snapshot.Candidates {
		current := &snapshot.Candidates[index]
		if current.NodeID != choice.NodeID || current.ResourceType != choice.ResourceType || canonicalRowPath(current.FieldPath) != choice.Path {
			continue
		}
		if found != nil {
			return rowSourceSkipInventory
		}
		found = current
	}
	switch {
	case found == nil:
		return rowSourceSkipInventory
	case !found.Populated:
		return rowSourceSkipObserved
	case len(found.RepeatedBoundaries) != 0 || capability.IsRepeatedCardinality(found.Cardinality):
		return rowSourceSkipRoute
	case !containsSourceProjectionMode(found.ProjectionModes, capability.ProjectionScalar):
		return rowSourceSkipInventory
	case !capability.IsSupportedConstructionSourceType(found.LogicalType):
		return rowSourceSkipType
	default:
		return rowSourceSkipInventory
	}
}

// rowSourceChoiceIsStaleVersion reports a signed choice issued for a previous
// dataset snapshot or schema digest. Undecodable ids are not flagged: the
// advertised set must stay bit-identical to pre-S-A behavior for every other
// input, and malformed ids remain refused at propose time by the
// INVALID_{LANE}_SOURCE_CHOICE boundary.
func rowSourceChoiceIsStaleVersion(choice capability.RowChoice, snapshot capability.Snapshot) bool {
	identity, err := capability.DecodeRowChoiceID(choice.ChoiceID)
	if err != nil {
		return false
	}
	return identity.SnapshotToken != snapshot.Token || identity.SchemaDigest != snapshot.Identity.SchemaDigest
}

func constructionRootProjectionCanReachStage(construction authoringv2.Construction, stageID string) bool {
	if stageID == recipe.ConstructionSourceProjectionID {
		return true
	}
	for _, step := range construction.Steps {
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return false
		}
		if step.ID == stageID {
			return true
		}
	}
	return false
}

func constructionReceiptStage(stages []explorer.ReceiptConstructionStage, stageID string) (explorer.ReceiptConstructionStage, bool) {
	for _, stage := range stages {
		if stage.ID == stageID {
			return stage, true
		}
	}
	return explorer.ReceiptConstructionStage{}, false
}

func constructionStageSupportsRelatedField(stage explorer.ReceiptConstructionStage) bool {
	for _, item := range stage.Capabilities {
		if item.Kind == "RELATED_FIELD" {
			return item.Supported
		}
	}
	return false
}

func constructionFHIRScalarType(resourceType, path string) string {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ""
	}
	path = strings.TrimPrefix(strings.TrimSpace(path), resourceType+".")
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(resourceType), path)
	if err != nil || facts.Shape != fhirschema.RowPathScalar || facts.Reference || facts.Cardinality != fhirschema.RowCardinalityOne {
		return ""
	}
	return facts.FHIRType
}

func constructionOwnedRootProjectionColumnID(construction authoringv2.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) ||
			constructionPivotInputStageID(construction, projection.OwnerStepID) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func constructionOwnedRelatedFieldColumnID(construction authoringv2.Construction, stageID string, candidate capability.Candidate) string {
	ownerID := ""
	for _, step := range construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationPivot && constructionStepInputStageID(step) == stageID {
			ownerID = step.ID
			break
		}
	}
	if ownerID == "" {
		return ""
	}
	for _, step := range construction.Steps {
		if step.OwnerStepID != ownerID || step.Operation.Kind != authoringv2.ConstructionOperationRelatedField || step.Operation.RelatedField == nil {
			continue
		}
		source := step.Operation.RelatedField.Source
		if source.CandidateID == candidate.ID && source.NodeID == candidate.NodeID && source.ResourceType == candidate.ResourceType &&
			source.Path == candidate.FieldPath && source.LogicalType == candidate.LogicalType {
			return step.Operation.RelatedField.OutputColumnID
		}
	}
	return ""
}

func constructionPivotInputStageID(construction authoringv2.Construction, pivotID string) string {
	for _, step := range construction.Steps {
		if step.ID == pivotID {
			return constructionStepInputStageID(step)
		}
	}
	return ""
}

func constructionStepInputStageID(step authoringv2.ConstructionStep) string {
	if len(step.Inputs) != 1 {
		return ""
	}
	switch step.Inputs[0].Kind {
	case authoringv2.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID
	case authoringv2.ConstructionInputStepOutput:
		return step.Inputs[0].StepID
	default:
		return ""
	}
}

func constructionStepIndex(steps []authoringv2.ConstructionStep, id string) int {
	for index, step := range steps {
		if step.ID == id {
			return index
		}
	}
	return -1
}

func constructionPivotSourceHelperID(pivotID, columnID string) string {
	digest := sha256.Sum256([]byte(pivotID + "\x00" + columnID))
	return "pivot_source_" + hex.EncodeToString(digest[:10])
}

func preserveExistingPivotSourceTable(
	construction authoringv2.Construction,
	pivotID, helperID string,
	source authoringv2.ConstructionRelatedFieldSource,
	output authoringv2.StageColumn,
) authoringv2.StageColumn {
	index := constructionStepIndex(construction.Steps, helperID)
	if index < 0 {
		return output
	}
	previous := construction.Steps[index]
	if previous.OwnerStepID != pivotID || previous.Operation.Kind != authoringv2.ConstructionOperationRelatedField ||
		previous.Operation.RelatedField == nil || previous.Operation.RelatedField.OutputColumnID != output.ID ||
		!reflect.DeepEqual(previous.Operation.RelatedField.Source, source) {
		return output
	}
	for _, column := range previous.Outputs {
		if column.ID != output.ID {
			continue
		}
		if column.Table == nil {
			output.Table = nil
		} else {
			table := *column.Table
			if column.Table.Visible != nil {
				visible := *column.Table.Visible
				table.Visible = &visible
			}
			if column.Table.Order != nil {
				order := *column.Table.Order
				table.Order = &order
			}
			output.Table = &table
		}
		break
	}
	return output
}

func constructionFHIRPathLabel(resourceType, path string) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), resourceType+".")
	return resourceType + "." + path
}

// constructionFieldDisplayLabel is the single construction-lane label builder
// shared by every signed source lane (Filter, Derive, Unpivot, Pivot). It
// returns the first non-blank TrimSpace'd display candidate — the authorized
// snapshot candidate's schema-Title label first, then the row choice's
// display label at advertise sites — and only then falls back to the legacy
// 'ResourceType.path' form produced by constructionFHIRPathLabel. Display
// candidates must come only from the authorized snapshot or the planner's
// generated FHIR schema truth, never from client input; a candidate carrying
// any Unicode control character (NUL, ESC, other C0/C1) is refused outright
// and falls through to the next candidate — advertised labels flow verbatim
// to the wire and into durable claims, so the refusal mirrors the reason-text
// sanitizer family at the API edge (reviewer witness: a Title with NUL/ESC
// bytes must never ride the label). The result is always trimmed and non-blank so the receipt contract's pre-trimmed label gates
// hold. Every one of the twelve lane label sites must go through this
// builder: the durable claim and the compiled artifact copy its exact bytes,
// so an out-of-band label writer would break the
// explorer_output_reconcile.go compiled-slot equality that anti-drift
// reconcile depends on.
func constructionFieldDisplayLabel(resourceType, path string, displayCandidates ...string) string {
	for _, candidate := range displayCandidates {
		label := strings.TrimSpace(candidate)
		if label == "" || strings.IndexFunc(label, unicode.IsControl) >= 0 {
			continue
		}
		return label
	}
	return constructionFHIRPathLabel(resourceType, path)
}

type resolvedConstructionPivotSource struct {
	selection ConstructionPivotSourceSelection
	root      *authoringv2.ConstructionSourceProjection
	related   *authoringv2.ConstructionRelatedFieldSource
	choiceID  string
	label     string
}

// constructionCandidateWithPivotSources resolves the signed source choices,
// moves them into the accepted Pivot's private input chain, and returns the
// generated RELATED_FIELD steps that have already passed authorization.
func (s *Service) constructionCandidateWithPivotSources(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, map[string]bool, error) {
	authorizedHelpers := make(map[string]bool)
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyPivotSources := request.PivotSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationPivot
	clearOwner := ""
	if applyPivotSources {
		clearOwner = request.ChangedStepID
	}
	candidate = preserveConstructionOwnedInputs(base.construction, candidate, clearOwner)
	if !applyPivotSources {
		return candidate, authorizedHelpers, nil
	}

	pivotIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	if pivotIndex < 0 || candidate.Steps[pivotIndex].Operation.Kind != authoringv2.ConstructionOperationPivot || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_REQUIRES_PIVOT", "pivotSources can only be applied to a Pivot step", nil)
	}
	pivotID := request.ChangedStepID
	if len(request.PivotSources) == 0 {
		cleared, err := clearConstructionPivotInputs(base.construction, candidate, pivotID)
		if err != nil {
			return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, authorizedHelpers, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[pivotIndex])
	anchorStageID := constructionOwnerAnchorStageID(base.construction, pivotID)
	if anchorStageID == "" {
		anchorStageID = acceptedStageID
	}
	if acceptedStageID == "" || anchorStageID == "" {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", "the Pivot input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.stages, acceptedStageID); !found {
		return authoringv2.Construction{}, nil, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the Pivot source choices are not bound to an accepted stage", nil, nil)
	}
	if _, found := constructionReceiptStage(base.stages, anchorStageID); !found {
		return authoringv2.Construction{}, nil, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the Pivot source input anchor is not present in the accepted compilation", nil, nil)
	}

	resolved := make([]resolvedConstructionPivotSource, 0, len(request.PivotSources))
	for _, selection := range request.PivotSources {
		item, err := s.resolveConstructionPivotSource(ctx, base, request, acceptedStageID, anchorStageID, selection)
		if err != nil {
			return authoringv2.Construction{}, nil, err
		}
		resolved = append(resolved, item)
	}
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].selection.ColumnID < resolved[j].selection.ColumnID
	})

	// Editing a saved Pivot starts from the first accepted input before its old
	// owned RELATED_FIELD chain. The chain is rebuilt from signed choices.
	oldOwned := make(map[string]bool)
	for _, step := range base.construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			oldOwned[step.ID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps)+len(resolved))
	for _, step := range candidate.Steps {
		if oldOwned[step.ID] {
			continue
		}
		steps = append(steps, step)
	}
	pivotIndex = constructionStepIndex(steps, pivotID)
	if pivotIndex < 0 {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_REQUIRES_PIVOT", "the candidate omits the Pivot being edited", nil)
	}

	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(resolved))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != pivotID {
			projections = append(projections, projection)
		}
	}
	for _, projection := range base.construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != pivotID && !hasPivotSourceProjection(projections, projection.ColumnID) &&
			!constructionOwnerInputsClearedFromCandidate(base.construction, candidate, projection.OwnerStepID) {
			projections = append(projections, projection)
		}
	}
	for _, item := range resolved {
		if item.root == nil {
			continue
		}
		projections = append(projections, *item.root)
	}
	candidate.SourceProjections = projections

	// Source projections enter at the source stage. Preserve them through each
	// row-preserving prefix stage that leads to this Pivot.
	for index := 0; index < pivotIndex; index++ {
		step := &steps[index]
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			if hasNewRootPivotSource(resolved) {
				return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
			}
			continue
		}
		for _, item := range resolved {
			if item.root == nil {
				continue
			}
			step.Outputs = appendStageColumnIfAbsent(step.Outputs, authoringv2.StageColumn{
				ID: item.root.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(item.root.ColumnID),
				Label: item.root.Label, Type: item.root.LogicalType,
			})
		}
	}

	pivot := &steps[pivotIndex]
	pivotAnchor := authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
	if anchorStageID != recipe.ConstructionSourceProjectionID {
		pivotAnchor = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: anchorStageID}
	}
	currentInputColumns, err := constructionAuthoringStageColumns(base, steps, anchorStageID, projections)
	if err != nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
	}
	for _, item := range resolved {
		if item.root != nil {
			currentInputColumns = appendStageColumnIfAbsent(currentInputColumns, authoringv2.StageColumn{
				ID: item.root.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(item.root.ColumnID),
				Label: item.root.Label, Type: item.root.LogicalType,
			})
		}
	}
	inserted := make([]authoringv2.ConstructionStep, 0, len(resolved))
	for _, item := range resolved {
		if item.related == nil {
			continue
		}
		helperID := constructionPivotSourceHelperID(pivotID, item.selection.ColumnID)
		choice, err := capability.NewConstructionRelatedFieldChoice(base.snapshot.Token, constructionRefStageID(pivotAnchor), sourceCandidate(base.snapshot, *item.related))
		if err != nil {
			return authoringv2.Construction{}, nil, unavailable("construction-proposal", "PIVOT_SOURCE_CHOICE_UNAVAILABLE", "the selected related field could not be reissued for the rebuilt input chain", err)
		}
		step := authoringv2.ConstructionStep{
			ID: helperID, OwnerStepID: pivotID,
			Inputs: []authoringv2.ConstructionInputRef{pivotAnchor},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
				ChoiceID: choice.ChoiceID, Source: *item.related, OutputColumnID: item.selection.ColumnID,
			}},
			Outputs: append([]authoringv2.StageColumn(nil), currentInputColumns...),
		}
		pivotSourceOutput := authoringv2.StageColumn{
			ID: item.selection.ColumnID, Name: constructionPivotSourceOutputName(item.selection.ColumnID),
			Label: item.label, Type: item.related.LogicalType, Nullable: true,
		}
		pivotSourceOutput = preserveExistingPivotSourceTable(base.construction, pivotID, helperID, *item.related, pivotSourceOutput)
		step.Outputs = appendStageColumnIfAbsent(step.Outputs, pivotSourceOutput)
		inserted = append(inserted, step)
		currentInputColumns = append([]authoringv2.StageColumn(nil), step.Outputs...)
		pivotAnchor = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: helperID}
		authorizedHelpers[helperID] = true
	}

	pivot = &steps[pivotIndex]
	pivot.Inputs = []authoringv2.ConstructionInputRef{pivotAnchor}
	steps = append(steps[:pivotIndex], append(inserted, steps[pivotIndex:]...)...)
	candidate.Steps = steps
	if err := validatePivotSourceColumns(candidate.Steps, pivotID, resolved); err != nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, authorizedHelpers, nil
}

func (s *Service) resolveConstructionPivotSource(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	acceptedStageID, anchorStageID string,
	selection ConstructionPivotSourceSelection,
) (resolvedConstructionPivotSource, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err == nil {
		if s.config.RowChoiceResolver == nil {
			return resolvedConstructionPivotSource{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized Pivot source choice resolution is not configured", nil)
		}
		resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: base.snapshot.Clone(), Route: base.document.Route,
			RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
		})
		if resolveErr != nil || identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest ||
			identity.Kind != capability.RowChoiceFieldGroupKey || identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
			identity.Path != resolved.FieldPath || identity.Reference || identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
			resolved.OccurrenceID != authoringv2.RootOccurrenceID {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
		}
		rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
		if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
		}
		candidate, found := uniqueRowSourceCandidate(base.snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
		if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
			!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
			return resolvedConstructionPivotSource{}, unprocessable("construction-proposal", "UNSUPPORTED_PIVOT_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
		}
		index, err := fhirschema.GeneratedIndex()
		if err != nil {
			return resolvedConstructionPivotSource{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
		}
		facts, err := index.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
		if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
			facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
		}
		label := constructionFieldDisplayLabel(base.document.RootResourceType, facts.CanonicalPath, candidate.Label)
		projection := authoringv2.ConstructionSourceProjection{
			ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
			FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
		}
		return resolvedConstructionPivotSource{selection: selection, root: &projection, choiceID: selection.ChoiceID, label: label}, nil
	}

	choice, choiceErr := capability.DecodeConstructionChoiceID(selection.ChoiceID)
	if choiceErr != nil {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected field choice is invalid", nil, choiceErr)
	}
	source, ok := choice.Source.(capability.RelatedFieldChoiceSource)
	if !ok || choice.SnapshotToken != base.snapshot.Token || source.StageID != acceptedStageID {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "reload the active related field choice for the current Pivot input stage", nil, nil)
	}
	acceptedStage, found := constructionReceiptStage(base.stages, acceptedStageID)
	anchorStage, anchorFound := constructionReceiptStage(base.stages, anchorStageID)
	if !found || !anchorFound || acceptedStage.ActiveRelatedRecord == nil || anchorStage.ActiveRelatedRecord == nil ||
		*acceptedStage.ActiveRelatedRecord != *anchorStage.ActiveRelatedRecord {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the active related record is not preserved at the rebuilt Pivot input", nil, nil)
	}
	candidate, found := uniqueCapabilityCandidate(base.snapshot, source.CandidateID)
	active := anchorStage.ActiveRelatedRecord
	if !found || candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
		candidate.FieldPath != source.Path || candidate.LogicalType != source.LogicalType || !candidate.Populated ||
		(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!relatedFieldPathExecutable(candidate.FieldPath) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the selected scalar is no longer available on the active related resource", nil, nil)
	}
	proved, err := proveConstructionCandidate(ctx, base.authorized, active.TargetResourceType, candidate, nil)
	if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
		proved.FieldPath != source.Path || proved.LogicalType != source.LogicalType || !proved.Populated ||
		(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the selected scalar is no longer supported by the compiler", nil, err)
	}
	if source.NodeID != proved.NodeID || source.ResourceType != proved.ResourceType || source.Cardinality != proved.Cardinality {
		return resolvedConstructionPivotSource{}, unprocessable("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected related field does not match its signed source identity", nil)
	}
	related := &authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: proved.ID, NodeID: proved.NodeID,
		ResourceType: proved.ResourceType, Path: proved.FieldPath, Cardinality: proved.Cardinality,
		LogicalType: proved.LogicalType, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), proved.RepeatedBoundaries...),
	}
	label := constructionFieldDisplayLabel(proved.ResourceType, proved.FieldPath, proved.Label)
	return resolvedConstructionPivotSource{selection: selection, related: related, choiceID: selection.ChoiceID, label: label}, nil
}

func preserveConstructionOwnedInputs(base authoringv2.Construction, candidate authoringv2.Construction, skipOwnerID string) authoringv2.Construction {
	present := make(map[string]bool, len(candidate.Steps))
	for _, step := range candidate.Steps {
		present[step.ID] = true
	}
	for baseIndex, owner := range base.Steps {
		if owner.ID == skipOwnerID || owner.Operation.Kind != authoringv2.ConstructionOperationPivot || constructionStepIndex(candidate.Steps, owner.ID) < 0 {
			continue
		}
		if constructionOwnerInputsClearedFromCandidate(base, candidate, owner.ID) {
			continue
		}
		missing := make([]authoringv2.ConstructionStep, 0)
		for _, step := range base.Steps[:baseIndex] {
			if step.OwnerStepID == owner.ID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField && !present[step.ID] {
				missing = append(missing, step)
				present[step.ID] = true
			}
		}
		index := constructionStepIndex(candidate.Steps, owner.ID)
		if len(missing) != 0 && index >= 0 {
			candidate.Steps = append(candidate.Steps[:index], append(missing, candidate.Steps[index:]...)...)
		}
		for _, projection := range base.SourceProjections {
			if projection.OwnerStepID == owner.ID && !hasPivotSourceProjection(candidate.SourceProjections, projection.ColumnID) {
				candidate.SourceProjections = append(candidate.SourceProjections, projection)
			}
		}
	}
	return candidate
}

// constructionOwnerInputsClearedFromCandidate reports whether the submitted
// candidate intentionally removed every durable input the accepted base keeps
// for ownerID. Under the shipped selection contract (nil never re-attaches,
// [] clears), the candidate is a full replacement of the construction, so
// dropping all of an owner's server-owned inputs in the proposal window is a
// clear: no lane may re-import them from the base. A partial chain (the
// client carried the helper but not its projection, or the reverse) is a
// client omission of server-owned state, which preservation must still
// repair.
func constructionOwnerInputsClearedFromCandidate(base authoringv2.Construction, candidate authoringv2.Construction, ownerID string) bool {
	baseInputs := 0
	for _, step := range base.Steps {
		if step.OwnerStepID == ownerID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			baseInputs++
		}
	}
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == ownerID {
			baseInputs++
		}
	}
	if baseInputs == 0 {
		return false
	}
	for _, step := range candidate.Steps {
		if step.OwnerStepID == ownerID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			return false
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == ownerID {
			return false
		}
	}
	return true
}

// constructionRootFieldAlreadyProjected reports whether materializing this
// signed slot column would collide, in the compiler's per-output duplicate
// semantic-path pass (lower/recipe_schema.go:205-224 suffixes BOTH columns
// with #<name>), with another public column that carries the same compiled
// semantic path. The receipt contract guard compares strings.TrimSpace of the
// compiled column's SemanticPath against the authored projection FieldPath
// verbatim (explorer_output_reconcile.go:497-512), so it fires exactly when a
// second public column shares the slot's verbatim path string. Carriers are
// therefore precisely the columns whose compiled SemanticPath is an authored
// bare field-path string on the root occurrence: user-authored SourceField
// columns (compilation/semantic_compile.go:220 emits FieldRef verbatim, and
// lower.recipeSemanticPath keeps a non-empty fieldRef verbatim) and every
// merged construction source projection — base-persisted, candidate-carried,
// or request-resolved (compilation/semantic_compile.go:522-529 emits
// FieldRef=projection.FieldPath verbatim into the same root node). A
// root.-prefixed authored path is verbatim-distinct and never collides; a
// projection under the slot's own ColumnID is the slot's re-bind entry, not a
// second carrier. Columns whose compiled name is internal (_key/__loom_*) are
// excluded together with the duplicate counter that skips internal columns.
// Only projections owned by a Derive, Filter, or Unpivot step are slot
// carriers: those are the entries the receipt contract guard actually compares at
// :1022/:1156, and shipped lifecycle contracts establish that a slot sharing
// its field with a Pivot-owned source input is legitimate state.
func constructionRootFieldAlreadyProjected(document authoringv2.Document, steps []authoringv2.ConstructionStep, projections []authoringv2.ConstructionSourceProjection, slotColumnID, fieldPath string) bool {
	slot := strings.TrimSpace(fieldPath)
	if slot == "" {
		return false
	}
	for _, column := range document.Columns {
		if column.ColumnID == "" || column.ColumnID == slotColumnID ||
			column.OccurrenceID != authoringv2.RootOccurrenceID ||
			column.Source.Kind != authoringv2.SourceField || column.Source.Field == nil {
			continue
		}
		if column.Column == "_key" || strings.HasPrefix(column.Column, "__loom_") {
			continue
		}
		if strings.TrimSpace(column.Source.Field.Path) == slot {
			return true
		}
	}
	for _, projection := range projections {
		if projection.ColumnID == slotColumnID || !constructionStepCarriesSourceLane(steps, projection.OwnerStepID) {
			continue
		}
		if strings.TrimSpace(projection.FieldPath) == slot {
			return true
		}
	}
	return false
}

func constructionStepCarriesSourceLane(steps []authoringv2.ConstructionStep, stepID string) bool {
	if stepID == "" {
		return false
	}
	for _, step := range steps {
		if step.ID != stepID {
			continue
		}
		return step.Operation.Kind == authoringv2.ConstructionOperationDerive ||
			step.Operation.Kind == authoringv2.ConstructionOperationFilter ||
			step.Operation.Kind == authoringv2.ConstructionOperationUnpivot
	}
	return false
}

func clearConstructionPivotInputs(base, candidate authoringv2.Construction, pivotID string) (authoringv2.Construction, error) {
	pivotIndex := constructionStepIndex(candidate.Steps, pivotID)
	if pivotIndex < 0 {
		return authoringv2.Construction{}, fmt.Errorf("the edited Pivot step is missing")
	}
	anchorStageID := constructionOwnerAnchorStageID(base, pivotID)
	if anchorStageID == "" {
		anchorStageID = constructionStepInputStageID(candidate.Steps[pivotIndex])
	}
	if anchorStageID == "" {
		return authoringv2.Construction{}, fmt.Errorf("the owned Pivot input anchor is unavailable")
	}
	oldProjectionIDs := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == pivotID {
			oldProjectionIDs[projection.ColumnID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
	for _, step := range candidate.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			continue
		}
		if constructionStepIndex(candidate.Steps, step.ID) < pivotIndex {
			filtered := step.Outputs[:0]
			for _, output := range step.Outputs {
				if !oldProjectionIDs[output.ID] {
					filtered = append(filtered, output)
				}
			}
			step.Outputs = filtered
		}
		steps = append(steps, step)
	}
	pivotIndex = constructionStepIndex(steps, pivotID)
	pivot := &steps[pivotIndex]
	pivot.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}
	if anchorStageID != recipe.ConstructionSourceProjectionID {
		pivot.Inputs[0] = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: anchorStageID}
	}
	projections := candidate.SourceProjections[:0]
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != pivotID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnerAnchorStageID(construction authoringv2.Construction, pivotID string) string {
	for _, step := range construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			return constructionStepInputStageID(step)
		}
	}
	return ""
}

func constructionRefStageID(input authoringv2.ConstructionInputRef) string {
	if input.Kind == authoringv2.ConstructionInputSourceProjection {
		return recipe.ConstructionSourceProjectionID
	}
	return input.StepID
}

func constructionAuthoringStageColumns(base constructionBase, steps []authoringv2.ConstructionStep, stageID string, projections []authoringv2.ConstructionSourceProjection) ([]authoringv2.StageColumn, error) {
	if stageID != recipe.ConstructionSourceProjectionID {
		if index := constructionStepIndex(steps, stageID); index >= 0 {
			return append([]authoringv2.StageColumn(nil), steps[index].Outputs...), nil
		}
	}
	if stage, found := constructionReceiptStage(base.stages, stageID); found {
		result := make([]authoringv2.StageColumn, 0, len(stage.Columns)+len(projections))
		for _, column := range stage.Columns {
			result = append(result, authoringv2.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type})
		}
		for _, projection := range projections {
			result = appendStageColumnIfAbsent(result, authoringv2.StageColumn{
				ID: projection.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		return result, nil
	}
	return nil, fmt.Errorf("stage %q is not available", stageID)
}

func appendStageColumnIfAbsent(columns []authoringv2.StageColumn, column authoringv2.StageColumn) []authoringv2.StageColumn {
	for _, current := range columns {
		if current.ID == column.ID {
			return columns
		}
	}
	return append(columns, column)
}

func constructionPivotSourceOutputName(columnID string) string {
	digest := sha256.Sum256([]byte(columnID))
	return "__pivot_source_" + hex.EncodeToString(digest[:8])
}

func hasPivotSourceProjection(projections []authoringv2.ConstructionSourceProjection, columnID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID {
			return true
		}
	}
	return false
}

func hasNewRootPivotSource(sources []resolvedConstructionPivotSource) bool {
	for _, source := range sources {
		if source.root != nil {
			return true
		}
	}
	return false
}

func uniqueRowSourceCandidate(snapshot capability.Snapshot, nodeID, resourceType, path string) (capability.Candidate, bool) {
	var result capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID != nodeID || candidate.ResourceType != resourceType || canonicalRowPath(candidate.FieldPath) != canonicalRowPath(path) {
			continue
		}
		if found {
			return capability.Candidate{}, false
		}
		result, found = candidate, true
	}
	return result, found
}

func sourceCandidate(snapshot capability.Snapshot, source authoringv2.ConstructionRelatedFieldSource) capability.Candidate {
	candidate, _ := uniqueCapabilityCandidate(snapshot, source.CandidateID)
	return candidate
}

func validatePivotSourceColumns(steps []authoringv2.ConstructionStep, pivotID string, sources []resolvedConstructionPivotSource) error {
	index := constructionStepIndex(steps, pivotID)
	if index < 0 || steps[index].Operation.Pivot == nil {
		return fmt.Errorf("the selected Pivot step is unavailable")
	}
	used := make(map[string]bool)
	pivot := steps[index].Operation.Pivot
	for _, id := range pivot.GroupKeyIDs {
		used[id] = true
	}
	used[pivot.CategoryColumnID], used[pivot.ValueColumnID] = true, true
	for _, source := range sources {
		if !used[source.selection.ColumnID] {
			return fmt.Errorf("pivotSources columnId %q is not used by the Pivot row key, category, or value", source.selection.ColumnID)
		}
	}
	return nil
}
