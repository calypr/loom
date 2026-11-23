package construction

import (
	"context"
	"fmt"
	"sort"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func validateFilterSourceColumns(steps []ws.ConstructionStep, filterID string, sources []ConstructionFilterSourceSelection) error {
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

func clearConstructionFilterOwnedSources(base, candidate ws.Construction, filterID string) (ws.Construction, error) {
	filterIndex := constructionStepIndex(candidate.Steps, filterID)
	if filterIndex < 0 {
		return ws.Construction{}, fmt.Errorf("the edited Filter step is missing")
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
	steps := make([]ws.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= filterIndex {
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
		if projection.OwnerStepID != filterID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnedFilterSourceColumnID(construction ws.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != ws.ConstructionOperationFilter || step.Operation.Filter == nil ||
			step.Operation.Filter.ColumnID != projection.ColumnID || constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
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
		return ws.ConstructionSourceProjection{
			OccurrenceID: ws.RootOccurrenceID, FieldPath: facts.CanonicalPath,
			FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType,
			Label: constructionFieldDisplayLabel(rootResourceType, facts.CanonicalPath, candidate.Label),
		}, true
	}
	return ws.ConstructionSourceProjection{}, false
}

func constructionHasFilterSourceInput(construction ws.Construction) bool {
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

func isFilterOwnedSourceProjection(construction ws.Construction, projection ws.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == ws.ConstructionOperationFilter && step.Operation.Filter != nil &&
		step.Operation.Filter.ColumnID == projection.ColumnID
}

func (s *Service) testResolveConstructionFilterSource(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	acceptedStageID string,
	selection ConstructionFilterSourceSelection,
) (ws.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "the selected filter source field choice is invalid", nil, err)
	}
	if s.config.RowChoiceResolver == nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized filter source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.Snapshot.Token || identity.SchemaDigest != base.Snapshot.Identity.SchemaDigest {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.Snapshot.Clone(), Route: base.Document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceFieldGroupKey ||
		identity.Occurrence.OccurrenceID != ws.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
		resolved.OccurrenceID != ws.RootOccurrenceID {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
	}
	rootNode, ok := base.Snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.Document.RootResourceType {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.Snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_FILTER_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_FILTER_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
	}
	label := constructionFieldDisplayLabel(base.Document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return ws.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

func (s *Service) testConstructionCandidateWithFilterSources(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	candidate ws.Construction,
) (ws.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyFilterSources := request.FilterSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == ws.ConstructionOperationFilter
	if !applyFilterSources {
		return candidate, nil
	}
	filter := candidate.Steps[changedIndex].Operation.Filter
	if filter == nil {
		return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_REQUIRES_FILTER", "filterSources can only be applied to a Filter step", nil)
	}
	if len(request.FilterSources) == 0 {
		cleared, err := clearConstructionFilterOwnedSources(base.Construction, candidate, request.ChangedStepID)
		if err != nil {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_STAGE_UNAVAILABLE", "the Filter input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.Stages, acceptedStageID); !found {
		return ws.Construction{}, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the filter source choices are not bound to an accepted stage", nil, nil)
	}
	for _, selection := range request.FilterSources {
		if selection.ColumnID != filter.ColumnID {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_COLUMN_MISMATCH",
				fmt.Sprintf("filterSources columnId %q is not the column filtered by the edited Filter step", selection.ColumnID), nil)
		}
	}

	resolved := make([]ws.ConstructionSourceProjection, 0, len(request.FilterSources))
	for _, selection := range request.FilterSources {
		projection, err := s.testResolveConstructionFilterSource(ctx, base, request, acceptedStageID, selection)
		if err != nil {
			return ws.Construction{}, err
		}
		resolved = append(resolved, projection)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ColumnID < resolved[j].ColumnID })

	droppedOwned := make(map[string]bool)
	for _, projection := range base.Construction.SourceProjections {
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

	projections := make([]ws.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(base.Construction.SourceProjections)+len(resolved))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != request.ChangedStepID {
			projections = append(projections, projection)
		}
	}
	for _, projection := range base.Construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != request.ChangedStepID && !hasPivotSourceProjection(projections, projection.ColumnID) &&
			!constructionOwnerInputsClearedFromCandidate(base.Construction, candidate, projection.OwnerStepID) {
			projections = append(projections, projection)
		}
	}
	projections = append(projections, resolved...)

	for _, projection := range resolved {
		if constructionRootFieldAlreadyProjected(base.Document, candidate.Steps, projections, projection.ColumnID, projection.FieldPath) {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_FIELD_ALREADY_PROJECTED",
				fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this filter source", projection.FieldPath), nil)
		}
	}

	steps := make([]ws.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index <= changedIndex; index++ {
		step := &steps[index]
		if !ws.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
		}
		outputs := make([]ws.StageColumn, 0, len(step.Outputs)+len(resolved))
		for _, output := range step.Outputs {
			if droppedOwned[output.ID] && !reowned[output.ID] {
				continue
			}
			outputs = append(outputs, output)
		}
		for _, projection := range resolved {
			outputs = appendStageColumnIfAbsent(outputs, ws.StageColumn{
				ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		step.Outputs = outputs
	}

	candidate.Steps, candidate.SourceProjections = steps, projections
	if err := validateFilterSourceColumns(candidate.Steps, request.ChangedStepID, request.FilterSources); err != nil {
		return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}
