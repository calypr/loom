package rowsource

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type sourceCapability struct {
	Supported  bool
	StageID    string
	ReasonCode string
	Reason     string
	Choices    []SourceChoice
}

func (s *Service) constructionSourceCapability(
	ctx context.Context,
	base Base,
	outputID, stageID string,
	reasons rowSourceLaneReasons,
	operation string,
	eligible func(string) bool,
	ownedColumn func(ws.Construction, string, string) string,
) (sourceCapability, error) {
	result := sourceCapability{StageID: stageID, Choices: []SourceChoice{}}
	if importedSourceChoiceUnavailable(base.Document) {
		result.Supported = true
		return result, nil
	}
	if _, found := constructionReceiptStage(base.Stages, stageID); !found {
		result.ReasonCode = "STALE_STAGE_REFERENCE"
		result.Reason = "the selected stage is not present in the current compiled output"
		return result, nil
	}
	enumeration := rowSourceEnumeration{
		rootReachable:     constructionRootProjectionCanReachStage(base.Construction, stageID),
		plannerConfigured: s.config.RowChoicePlanner != nil,
	}
	if enumeration.rootReachable && enumeration.plannerConfigured {
		choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.Snapshot.Clone(), base.Document)
		if err != nil {
			result.ReasonCode, result.Reason = rowSourceInventoryFailure(reasons)
			return result, unavailable("construction-capabilities", result.ReasonCode, result.Reason,
				fmt.Errorf("list authorized root %s source choices for output %q: %w", operation, outputID, err))
		}
		for _, choice := range choices {
			if rowSourceChoiceIsStaleVersion(choice, base.Snapshot) {
				enumeration.skip(rowSourceSkipVersion)
				continue
			}
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != ws.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				enumeration.skip(rowSourceSkipRoute)
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.Snapshot, choice)
			if !ok {
				enumeration.skip(rowSourceCandidateSkipClass(base.Snapshot, choice))
				continue
			}
			if !eligible(candidate.LogicalType) {
				enumeration.skip(rowSourceSkipType)
				continue
			}
			result.Choices = append(result.Choices, SourceChoice{
				ChoiceID: choice.ChoiceID, FieldPath: choice.Path,
				Label:       constructionFieldDisplayLabel(base.Document.RootResourceType, choice.Path, candidate.Label, choice.Label),
				LogicalType: candidate.LogicalType, IsPopulated: candidate.Populated,
				ColumnID: ownedColumn(base.Construction, stageID, choice.Path),
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
		result.ReasonCode, result.Reason = enumeration.decision(reasons)
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func constructionSourceFacts(
	snapshot capability.Snapshot,
	rootResourceType, fieldPath string,
	eligible func(string) bool,
) (ws.ConstructionSourceProjection, bool) {
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
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) || !eligible(candidate.LogicalType) {
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

func constructionOwnedSourceColumnID(construction ws.Construction, stageID, path string, kind ws.ConstructionOperationKind) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		inputs := constructionSourceInputColumnIDs(step, kind)
		if inputs == nil || !containsConstructionColumnIDLocal(inputs, projection.ColumnID) ||
			constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func constructionSourceInputColumnIDs(step ws.ConstructionStep, kind ws.ConstructionOperationKind) []string {
	switch kind {
	case ws.ConstructionOperationDerive:
		if step.Operation.Kind == kind && step.Operation.Derive != nil {
			return deriveOperandColumnIDs(step.Operation.Derive)
		}
	case ws.ConstructionOperationUnpivot:
		if step.Operation.Kind == kind && step.Operation.Unpivot != nil {
			return unpivotInputColumnIDs(step.Operation.Unpivot)
		}
	}
	return nil
}

func validateConstructionSourceColumns(
	steps []ws.ConstructionStep,
	stepID string,
	kind ws.ConstructionOperationKind,
	selections []string,
) error {
	index := constructionStepIndex(steps, stepID)
	if index < 0 {
		return unavailableSourceOperation(kind)
	}
	inputs := constructionSourceInputColumnIDs(steps[index], kind)
	if inputs == nil {
		return unavailableSourceOperation(kind)
	}
	for _, columnID := range selections {
		if !containsConstructionColumnIDLocal(inputs, columnID) {
			return invalidSourceSelection(kind, columnID)
		}
	}
	return nil
}

func unavailableSourceOperation(kind ws.ConstructionOperationKind) error {
	if kind == ws.ConstructionOperationDerive {
		return fmt.Errorf("the selected Derive step is unavailable")
	}
	return fmt.Errorf("the selected Unpivot step is unavailable")
}

func invalidSourceSelection(kind ws.ConstructionOperationKind, columnID string) error {
	if kind == ws.ConstructionOperationDerive {
		return fmt.Errorf("deriveSources columnId %q is not a column operand of the Derive step", columnID)
	}
	return fmt.Errorf("unpivotSources columnId %q is not a melt input of the Unpivot step", columnID)
}

type constructionSourceReauthorization struct {
	kind           ws.ConstructionOperationKind
	name           string
	plannerMissing string
	invalidInput   string
	mismatch       string
	facts          func(capability.Snapshot, string, string) (ws.ConstructionSourceProjection, bool)
}

var deriveSourceReauthorization = constructionSourceReauthorization{
	kind: ws.ConstructionOperationDerive, name: "derive",
	plannerMissing: "authorized derive source choice planning is not configured",
	invalidInput:   "derive source projection %q is not a column operand of its owner Derive step",
	mismatch:       "derive source projection %q no longer matches an authorized populated root scalar field",
	facts:          ConstructionDeriveSourceFacts,
}

var unpivotSourceReauthorization = constructionSourceReauthorization{
	kind: ws.ConstructionOperationUnpivot, name: "unpivot",
	plannerMissing: "authorized unpivot source choice planning is not configured",
	invalidInput:   "unpivot source projection %q is not a melt input of its owner Unpivot step",
	mismatch:       "unpivot source projection %q no longer matches an authorized populated root scalar field",
	facts:          ConstructionUnpivotSourceFacts,
}

func (s *Service) reauthorizeConstructionSources(
	ctx context.Context,
	base Base,
	outputID string,
	construction ws.Construction,
	policy constructionSourceReauthorization,
) error {
	if s.config.RowChoicePlanner == nil {
		return unavailable("commands", "ROW_CHOICE_UNAVAILABLE", policy.plannerMissing, nil)
	}
	var choices []capability.RowChoice
	planned := false
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			return fmt.Errorf("%s source projection %q names a missing owner step", policy.name, projection.ColumnID)
		}
		step := construction.Steps[index]
		if step.Operation.Kind != policy.kind {
			continue
		}
		if !containsConstructionColumnIDLocal(constructionSourceInputColumnIDs(step, policy.kind), projection.ColumnID) {
			return fmt.Errorf(policy.invalidInput, projection.ColumnID)
		}
		if !planned {
			listed, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.Snapshot.Clone(), base.Document)
			if err != nil {
				return fmt.Errorf("list authorized root %s source choices for output %q: %w", policy.name, outputID, err)
			}
			choices, planned = listed, true
		}
		matched := false
		for _, choice := range choices {
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != ws.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) != canonicalRowPath(projection.FieldPath) {
				continue
			}
			if _, found := base.Snapshot.Node(choice.NodeID); !found {
				continue
			}
			derived, ok := policy.facts(base.Snapshot, base.Document.RootResourceType, projection.FieldPath)
			if !ok {
				continue
			}
			expected := ws.ConstructionSourceProjection{
				ColumnID: projection.ColumnID, OwnerStepID: projection.OwnerStepID,
				OccurrenceID: derived.OccurrenceID, FieldPath: derived.FieldPath,
				FHIRType: derived.FHIRType, LogicalType: derived.LogicalType, Label: derived.Label,
			}
			matched = reflect.DeepEqual(expected, projection)
			break
		}
		if !matched {
			return fmt.Errorf(policy.mismatch, projection.ColumnID)
		}
	}
	return nil
}

func constructionSourceSelectionIDs[T any](selections []T, columnID func(T) string) []string {
	ids := make([]string, 0, len(selections))
	for _, selection := range selections {
		ids = append(ids, columnID(selection))
	}
	return ids
}

func (result sourceCapability) derive() ConstructionDeriveSourceCapability {
	return ConstructionDeriveSourceCapability{
		Supported: result.Supported, StageID: result.StageID, ReasonCode: result.ReasonCode,
		Reason: result.Reason, Choices: result.Choices,
	}
}

func (result sourceCapability) unpivot() ConstructionUnpivotSourceCapability {
	return ConstructionUnpivotSourceCapability{
		Supported: result.Supported, StageID: result.StageID, ReasonCode: result.ReasonCode,
		Reason: result.Reason, Choices: result.Choices,
	}
}
