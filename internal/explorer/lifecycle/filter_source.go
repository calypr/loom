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

// ConstructionFilterSourceCapability advertises the authorized populated root
// scalar fields a FILTER step can consume through a signed source choice,
// beside the existing Pivot and Group source inputs.
type ConstructionFilterSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionFilterSourceChoice `json:"choices"`
}

// ConstructionFilterSourceChoice is pinned to exactly six wire fields. The
// UI capabilities schema is strict, so pivot-style extras (occurrenceId,
// fhirType, valueType, isIdentifier, isReference) must never be added.
// ColumnID is the signed materialized column id from durable server-derived
// state, mirroring pivot's choice.columnId, so the UI can restore a saved
// source-backed filter; it is "" while no live materialization exists.
type ConstructionFilterSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

// ConstructionFilterSourceSelection binds one server-signed root scalar field
// choice to the exact columnId the edited FILTER step will consume.
type ConstructionFilterSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

func validateConstructionFilterSourceSelections(selections []ConstructionFilterSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := requireExactIdentity(selection.ChoiceID, fmt.Sprintf("filterSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, fmt.Sprintf("filterSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("filterSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

func (s *Service) constructionFilterSourceCapability(
	ctx context.Context,
	base constructionBase,
	outputID, stageID string,
) (ConstructionFilterSourceCapability, error) {
	result := ConstructionFilterSourceCapability{StageID: stageID, Choices: []ConstructionFilterSourceChoice{}}
	if _, found := constructionReceiptStage(base.stages, stageID); !found {
		result.ReasonCode = "STALE_STAGE_REFERENCE"
		result.Reason = "the selected stage is not present in the current compiled output"
		return result, nil
	}
	if constructionRootProjectionCanReachStage(base.construction, stageID) && s.config.RowChoicePlanner != nil {
		choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
		if err != nil {
			return result, fmt.Errorf("list authorized root filter source choices for output %q: %w", outputID, err)
		}
		for _, choice := range choices {
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.snapshot, choice)
			if !ok {
				continue
			}
			result.Choices = append(result.Choices, ConstructionFilterSourceChoice{
				ChoiceID: choice.ChoiceID, FieldPath: choice.Path,
				Label:       constructionFHIRPathLabel(base.document.RootResourceType, choice.Path),
				LogicalType: candidate.LogicalType, IsPopulated: candidate.Populated,
				ColumnID: constructionOwnedFilterSourceColumnID(base.construction, stageID, choice.Path),
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
		result.ReasonCode = "NO_ELIGIBLE_POPULATED_FILTER_SOURCE_FIELDS"
		result.Reason = "the selected stage has no authorized populated scalar FHIR fields that the Filter step can consume"
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func (s *Service) resolveConstructionFilterSource(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	acceptedStageID string,
	selection ConstructionFilterSourceSelection,
) (authoringv2.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "the selected filter source field choice is invalid", nil, err)
	}
	if s.config.RowChoiceResolver == nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized filter source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, nil)
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
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
	}
	rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_FILTER_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_FILTER_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
	}
	label := constructionFHIRPathLabel(base.document.RootResourceType, facts.CanonicalPath)
	return authoringv2.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

// constructionCandidateWithFilterSources resolves the signed field choices,
// derives the durable server-owned projection facts for the edited FILTER
// step, and carries those slots through the row-preserving prefix. It mirrors
// the Pivot source contract: the selection set is authoritative only when the
// changed step is the FILTER that consumes it.
func (s *Service) constructionCandidateWithFilterSources(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyFilterSources := request.FilterSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationFilter
	candidate = preserveConstructionOwnedInputs(base.construction, candidate, "")
	if !applyFilterSources {
		return candidate, nil
	}
	filter := candidate.Steps[changedIndex].Operation.Filter
	if filter == nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_REQUIRES_FILTER", "filterSources can only be applied to a Filter step", nil)
	}
	if len(request.FilterSources) == 0 {
		cleared, err := clearConstructionFilterOwnedSources(base.construction, candidate, request.ChangedStepID)
		if err != nil {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_STAGE_UNAVAILABLE", "the Filter input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.stages, acceptedStageID); !found {
		return authoringv2.Construction{}, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the filter source choices are not bound to an accepted stage", nil, nil)
	}
	for _, selection := range request.FilterSources {
		if selection.ColumnID != filter.ColumnID {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_COLUMN_MISMATCH",
				fmt.Sprintf("filterSources columnId %q is not the column filtered by the edited Filter step", selection.ColumnID), nil)
		}
	}

	resolved := make([]authoringv2.ConstructionSourceProjection, 0, len(request.FilterSources))
	for _, selection := range request.FilterSources {
		projection, err := s.resolveConstructionFilterSource(ctx, base, request, acceptedStageID, selection)
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

	steps := make([]authoringv2.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index <= changedIndex; index++ {
		step := &steps[index]
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
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
	if err := validateFilterSourceColumns(candidate.Steps, request.ChangedStepID, request.FilterSources); err != nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

func validateFilterSourceColumns(steps []authoringv2.ConstructionStep, filterID string, sources []ConstructionFilterSourceSelection) error {
	index := constructionStepIndex(steps, filterID)
	if index < 0 || steps[index].Operation.Filter == nil {
		return fmt.Errorf("the selected Filter step is unavailable")
	}
	for _, source := range sources {
		if steps[index].Operation.Filter.ColumnID != source.ColumnID {
			return fmt.Errorf("filterSources columnId %q is not the column filtered by the Filter step", source.ColumnID)
		}
	}
	return nil
}

func clearConstructionFilterOwnedSources(base, candidate authoringv2.Construction, filterID string) (authoringv2.Construction, error) {
	filterIndex := constructionStepIndex(candidate.Steps, filterID)
	if filterIndex < 0 {
		return authoringv2.Construction{}, fmt.Errorf("the edited Filter step is missing")
	}
	owned := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == filterID {
			owned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == filterID {
			owned[projection.ColumnID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= filterIndex {
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
		if projection.OwnerStepID != filterID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

// constructionOwnedFilterSourceColumnID returns the durable server-derived
// materialized column for a root field at one exact stage, mirroring
// constructionOwnedRootProjectionColumnID. It only trusts state that is
// pinned to the current CAS draft and snapshot-bound receipt (enforced by
// loadConstructionBase): the projection must be owned by a FILTER step that
// consumes this exact stage and still filters on this exact column. A
// vanished, re-pointed, or foreign-owned materialization returns "", so the
// UI never renders a restore into a column the server can no longer sign.
func constructionOwnedFilterSourceColumnID(construction authoringv2.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationFilter || step.Operation.Filter == nil ||
			step.Operation.Filter.ColumnID != projection.ColumnID || constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

// ConstructionFilterSourceFacts derives the server-owned projection facts
// for one root field from the authorized snapshot plus generated FHIR
// metadata. Apply reauthorization and receipt reconcile share this single
// derivation so a persisted filter-owned claim can never drift from the
// facts the server would have signed it from. Column and owner identities
// stay claim-sourced, exactly as the Apply-side DeepEquals expects them.
func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
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
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
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

func constructionHasFilterSourceInput(construction authoringv2.Construction) bool {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		if !isFilterOwnedSourceProjection(construction, projection) {
			continue
		}
		return true
	}
	return false
}

func isFilterOwnedSourceProjection(construction authoringv2.Construction, projection authoringv2.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == authoringv2.ConstructionOperationFilter && step.Operation.Filter != nil &&
		step.Operation.Filter.ColumnID == projection.ColumnID
}

// reauthorizeConstructionFilterSources re-derives the server-owned facts for
// every filter-owned projection from the live authorized snapshot so an Apply
// cannot stage a filter whose field stopped qualifying after the proposal.
func (s *Service) reauthorizeConstructionFilterSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	if s.config.RowChoicePlanner == nil {
		return unavailable("commands", "ROW_CHOICE_UNAVAILABLE", "authorized filter source choice planning is not configured", nil)
	}
	var choices []capability.RowChoice
	planned := false
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			return fmt.Errorf("filter source projection %q names a missing owner step", projection.ColumnID)
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationFilter {
			continue
		}
		if step.Operation.Filter == nil || step.Operation.Filter.ColumnID != projection.ColumnID {
			return fmt.Errorf("filter source projection %q is not the column filtered by its owner Filter step", projection.ColumnID)
		}
		if !planned {
			listed, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
			if err != nil {
				return fmt.Errorf("list authorized root filter source choices for output %q: %w", outputID, err)
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
			derived, ok := ConstructionFilterSourceFacts(base.snapshot, base.document.RootResourceType, projection.FieldPath)
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
			return fmt.Errorf("filter source projection %q no longer matches an authorized populated root scalar field", projection.ColumnID)
		}
	}
	return nil
}
