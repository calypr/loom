package lifecycle

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"unicode"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
)

type constructionBase = construction.Base

func importedSourceChoiceUnavailable(document authoringv2.Document) bool {

	return document.ImportedRecipe != nil
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
func appendStageColumnIfAbsent(columns []authoringv2.StageColumn, column authoringv2.StageColumn) []authoringv2.StageColumn {
	for _, current := range columns {
		if current.ID == column.ID {
			return columns
		}
	}
	return append(columns, column)
}
func hasPivotSourceProjection(projections []authoringv2.ConstructionSourceProjection, columnID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID {
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
func constructionGroupSourceCandidate(snapshot capability.Snapshot, choice capability.RowChoice) (capability.Candidate, bool) {
	var candidate capability.Candidate
	found := false
	for _, current := range snapshot.Candidates {
		if current.NodeID != choice.NodeID || current.ResourceType != choice.ResourceType || canonicalRowPath(current.FieldPath) != choice.Path {
			continue
		}
		if found {
			return capability.Candidate{}, false
		}
		candidate, found = current, true
	}
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 || capability.IsRepeatedCardinality(candidate.Cardinality) ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return capability.Candidate{}, false
	}
	return candidate, true
}
func canonicalRowPath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "root.")
}
func containsSourceProjectionMode(modes []capability.ProjectionMode, want capability.ProjectionMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

func constructionFHIRPathLabel(resourceType, path string) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), resourceType+".")
	return resourceType + "." + path
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
