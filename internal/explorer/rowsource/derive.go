package rowsource

import (
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func deriveSourceLogicalTypeIsNumeric(logicalType string) bool {
	switch strings.ToLower(strings.TrimSpace(logicalType)) {
	case "integer", "decimal":
		return true
	default:
		return false
	}
}

func deriveOperandColumnIDs(derive *ws.ConstructionDerive) []string {
	if derive == nil {
		return nil
	}
	ids := make([]string, 0, 2)
	if derive.Left.Kind == ws.ConstructionColumnOperand {
		ids = append(ids, derive.Left.ColumnID)
	}
	if derive.Right.Kind == ws.ConstructionColumnOperand {
		ids = append(ids, derive.Right.ColumnID)
	}
	return ids
}

var deriveSourceReasons = rowSourceLaneReasons{
	codeStem:        "DERIVE",
	reachReason:     "the current construction prefix does not carry root scalar source fields to this stage",
	versionReason:   "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Derive source fields",
	routeReason:     "every root scalar field enumerated at this stage is a reference, repeated, non-root, or metadata path that the Derive step cannot reach",
	typeReason:      "every root scalar field enumerated at this stage is not an integer or decimal scalar that the Derive step can consume",
	observedReason:  "every root scalar field enumerated for the Derive step at this stage has no observed values in the current dataset snapshot",
	genericCode:     "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS",
	genericReason:   "the selected stage has no authorized populated integer or decimal scalar FHIR fields that the Derive step can consume",
	inventoryReason: "the row-choice inventory backing root Derive scalar source fields is unavailable for the current dataset snapshot",
}

func validateDeriveSourceColumns(steps []ws.ConstructionStep, deriveID string, sources []ConstructionDeriveSourceSelection) error {
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

func clearConstructionDeriveOwnedSources(base, candidate ws.Construction, deriveID string) (ws.Construction, error) {
	deriveIndex := constructionStepIndex(candidate.Steps, deriveID)
	if deriveIndex < 0 {
		return ws.Construction{}, fmt.Errorf("the edited Derive step is missing")
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
	steps := make([]ws.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= deriveIndex {
			outputs := make([]ws.StageColumn, 0, len(step.Outputs))
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
	projections := make([]ws.ConstructionSourceProjection, 0, len(candidate.SourceProjections))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != deriveID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnedDeriveSourceColumnID(construction ws.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != ws.ConstructionOperationDerive || step.Operation.Derive == nil ||
			!containsConstructionColumnIDLocal(deriveOperandColumnIDs(step.Operation.Derive), projection.ColumnID) ||
			constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func ConstructionDeriveSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	path := canonicalRowPath(fieldPath)
	if strings.TrimSpace(rootResourceType) == "" || path == "" {
		return ws.ConstructionSourceProjection{}, false
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
		return ws.ConstructionSourceProjection{
			OccurrenceID: ws.RootOccurrenceID, FieldPath: facts.CanonicalPath,
			FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType,
			Label: constructionFieldDisplayLabel(rootResourceType, facts.CanonicalPath, candidate.Label),
		}, true
	}
	return ws.ConstructionSourceProjection{}, false
}

func ConstructionHasDeriveSourceInput(construction ws.Construction) bool {
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

func isDeriveOwnedSourceProjection(construction ws.Construction, projection ws.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == ws.ConstructionOperationDerive && step.Operation.Derive != nil &&
		containsConstructionColumnIDLocal(deriveOperandColumnIDs(step.Operation.Derive), projection.ColumnID)
}
