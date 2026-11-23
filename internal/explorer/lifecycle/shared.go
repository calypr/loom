package lifecycle

import (
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
)

type constructionBase = construction.Base

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
func constructionStepIndex(steps []ws.ConstructionStep, id string) int {
	for index, step := range steps {
		if step.ID == id {
			return index
		}
	}
	return -1
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
func hasPivotSourceProjection(projections []ws.ConstructionSourceProjection, columnID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID {
			return true
		}
	}
	return false
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
