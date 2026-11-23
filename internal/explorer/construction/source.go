package construction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/recipe"
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
	codeStem:        "PIVOT",
	reachReason:     "the current construction prefix does not carry root scalar source fields to this stage, and no active related-record source field is offered either",
	versionReason:   "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Pivot source fields",
	routeReason:     "every root scalar field enumerated at this stage is a reference, repeated, non-root, or metadata path that cannot be carried into Pivot",
	typeReason:      "every root scalar field enumerated at this stage has a logical type that cannot be carried into Pivot",
	observedReason:  "every root scalar field enumerated for Pivot at this stage has no observed values in the current dataset snapshot",
	genericCode:     "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS",
	genericReason:   "the selected stage has no authorized populated scalar FHIR fields that can be carried into Pivot",
	inventoryReason: "the row-choice inventory backing root Pivot source fields is unavailable for the current dataset snapshot",
}

type rowSourceSkipClass int

const (
	rowSourceSkipObserved rowSourceSkipClass = iota
	rowSourceSkipType
	rowSourceSkipRoute
	rowSourceSkipVersion
	rowSourceSkipInventory
)

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

type rowSourceLaneReasons struct {
	codeStem        string
	inventoryReason string
	reachReason     string
	versionReason   string
	routeReason     string
	typeReason      string
	observedReason  string
	genericCode     string
	genericReason   string
}

func rowSourceInventoryFailure(reasons rowSourceLaneReasons) (string, string) {
	return reasons.codeStem + "_SOURCE_INVENTORY_UNAVAILABLE", reasons.inventoryReason
}

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

func rowSourceChoiceIsStaleVersion(choice capability.RowChoice, snapshot capability.Snapshot) bool {
	identity, err := capability.DecodeRowChoiceID(choice.ChoiceID)
	if err != nil {
		return false
	}
	return identity.SnapshotToken != snapshot.Token || identity.SchemaDigest != snapshot.Identity.SchemaDigest
}

func constructionRootProjectionCanReachStage(construction ws.Construction, stageID string) bool {
	if stageID == recipe.ConstructionSourceProjectionID {
		return true
	}
	for _, step := range construction.Steps {
		if !ws.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return false
		}
		if step.ID == stageID {
			return true
		}
	}
	return false
}

func constructionReceiptStage(stages []receiptmodel.ReceiptConstructionStage, stageID string) (receiptmodel.ReceiptConstructionStage, bool) {
	for _, stage := range stages {
		if stage.ID == stageID {
			return stage, true
		}
	}
	return receiptmodel.ReceiptConstructionStage{}, false
}

func constructionStageSupportsRelatedField(stage receiptmodel.ReceiptConstructionStage) bool {
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

func constructionOwnedRootProjectionColumnID(construction ws.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) ||
			constructionPivotInputStageID(construction, projection.OwnerStepID) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func constructionOwnedRelatedFieldColumnID(construction ws.Construction, stageID string, candidate capability.Candidate) string {
	ownerID := ""
	for _, step := range construction.Steps {
		if step.Operation.Kind == ws.ConstructionOperationPivot && constructionStepInputStageID(step) == stageID {
			ownerID = step.ID
			break
		}
	}
	if ownerID == "" {
		return ""
	}
	for _, step := range construction.Steps {
		if step.OwnerStepID != ownerID || step.Operation.Kind != ws.ConstructionOperationRelatedField || step.Operation.RelatedField == nil {
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

func constructionPivotInputStageID(construction ws.Construction, pivotID string) string {
	for _, step := range construction.Steps {
		if step.ID == pivotID {
			return constructionStepInputStageID(step)
		}
	}
	return ""
}

func constructionStepInputStageID(step ws.ConstructionStep) string {
	if len(step.Inputs) != 1 {
		return ""
	}
	switch step.Inputs[0].Kind {
	case ws.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID
	case ws.ConstructionInputStepOutput:
		return step.Inputs[0].StepID
	default:
		return ""
	}
}

func constructionStepIndex(steps []ws.ConstructionStep, id string) int {
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
	construction ws.Construction,
	pivotID, helperID string,
	source ws.ConstructionRelatedFieldSource,
	output ws.StageColumn,
) ws.StageColumn {
	index := constructionStepIndex(construction.Steps, helperID)
	if index < 0 {
		return output
	}
	previous := construction.Steps[index]
	if previous.OwnerStepID != pivotID || previous.Operation.Kind != ws.ConstructionOperationRelatedField ||
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
	root      *ws.ConstructionSourceProjection
	related   *ws.ConstructionRelatedFieldSource
	choiceID  string
	label     string
}

func preserveConstructionOwnedInputs(base ws.Construction, candidate ws.Construction, skipOwnerID string) ws.Construction {
	present := make(map[string]bool, len(candidate.Steps))
	for _, step := range candidate.Steps {
		present[step.ID] = true
	}
	for baseIndex, owner := range base.Steps {
		if owner.ID == skipOwnerID || owner.Operation.Kind != ws.ConstructionOperationPivot || constructionStepIndex(candidate.Steps, owner.ID) < 0 {
			continue
		}
		if constructionOwnerInputsClearedFromCandidate(base, candidate, owner.ID) {
			continue
		}
		missing := make([]ws.ConstructionStep, 0)
		for _, step := range base.Steps[:baseIndex] {
			if step.OwnerStepID == owner.ID && step.Operation.Kind == ws.ConstructionOperationRelatedField && !present[step.ID] {
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

func constructionOwnerInputsClearedFromCandidate(base ws.Construction, candidate ws.Construction, ownerID string) bool {
	baseInputs := 0
	for _, step := range base.Steps {
		if step.OwnerStepID == ownerID && step.Operation.Kind == ws.ConstructionOperationRelatedField {
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
		if step.OwnerStepID == ownerID && step.Operation.Kind == ws.ConstructionOperationRelatedField {
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

func constructionRootFieldAlreadyProjected(document ws.Document, steps []ws.ConstructionStep, projections []ws.ConstructionSourceProjection, slotColumnID, fieldPath string) bool {
	slot := strings.TrimSpace(fieldPath)
	if slot == "" {
		return false
	}
	for _, column := range document.Columns {
		if column.ColumnID == "" || column.ColumnID == slotColumnID ||
			column.OccurrenceID != ws.RootOccurrenceID ||
			column.Source.Kind != ws.SourceField || column.Source.Field == nil {
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

func constructionStepCarriesSourceLane(steps []ws.ConstructionStep, stepID string) bool {
	if stepID == "" {
		return false
	}
	for _, step := range steps {
		if step.ID != stepID {
			continue
		}
		return step.Operation.Kind == ws.ConstructionOperationDerive ||
			step.Operation.Kind == ws.ConstructionOperationFilter ||
			step.Operation.Kind == ws.ConstructionOperationUnpivot
	}
	return false
}

func clearConstructionPivotInputs(base, candidate ws.Construction, pivotID string) (ws.Construction, error) {
	pivotIndex := constructionStepIndex(candidate.Steps, pivotID)
	if pivotIndex < 0 {
		return ws.Construction{}, fmt.Errorf("the edited Pivot step is missing")
	}
	anchorStageID := constructionOwnerAnchorStageID(base, pivotID)
	if anchorStageID == "" {
		anchorStageID = constructionStepInputStageID(candidate.Steps[pivotIndex])
	}
	if anchorStageID == "" {
		return ws.Construction{}, fmt.Errorf("the owned Pivot input anchor is unavailable")
	}
	oldProjectionIDs := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == pivotID {
			oldProjectionIDs[projection.ColumnID] = true
		}
	}
	steps := make([]ws.ConstructionStep, 0, len(candidate.Steps))
	for _, step := range candidate.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == ws.ConstructionOperationRelatedField {
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
	pivot.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}
	if anchorStageID != recipe.ConstructionSourceProjectionID {
		pivot.Inputs[0] = ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: anchorStageID}
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

func constructionOwnerAnchorStageID(construction ws.Construction, pivotID string) string {
	for _, step := range construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == ws.ConstructionOperationRelatedField {
			return constructionStepInputStageID(step)
		}
	}
	return ""
}

func constructionRefStageID(input ws.ConstructionInputRef) string {
	if input.Kind == ws.ConstructionInputSourceProjection {
		return recipe.ConstructionSourceProjectionID
	}
	return input.StepID
}

func constructionAuthoringStageColumns(base Base, steps []ws.ConstructionStep, stageID string, projections []ws.ConstructionSourceProjection) ([]ws.StageColumn, error) {
	if stageID != recipe.ConstructionSourceProjectionID {
		if index := constructionStepIndex(steps, stageID); index >= 0 {
			return append([]ws.StageColumn(nil), steps[index].Outputs...), nil
		}
	}
	if stage, found := constructionReceiptStage(base.Stages, stageID); found {
		result := make([]ws.StageColumn, 0, len(stage.Columns)+len(projections))
		for _, column := range stage.Columns {
			result = append(result, ws.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type})
		}
		for _, projection := range projections {
			result = appendStageColumnIfAbsent(result, ws.StageColumn{
				ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		return result, nil
	}
	return nil, fmt.Errorf("stage %q is not available", stageID)
}

func appendStageColumnIfAbsent(columns []ws.StageColumn, column ws.StageColumn) []ws.StageColumn {
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

func hasPivotSourceProjection(projections []ws.ConstructionSourceProjection, columnID string) bool {
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

func sourceCandidate(snapshot capability.Snapshot, source ws.ConstructionRelatedFieldSource) capability.Candidate {
	candidate, _ := uniqueCapabilityCandidate(snapshot, source.CandidateID)
	return candidate
}

func validatePivotSourceColumns(steps []ws.ConstructionStep, pivotID string, sources []resolvedConstructionPivotSource) error {
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

func (s *Service) constructionSources() constructionRowSources {
	return constructionRowSources{planner: s.config.RowChoicePlanner, resolver: s.config.RowChoiceResolver}
}
