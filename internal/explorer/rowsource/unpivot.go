package rowsource

import (
	"context"
	"fmt"
	"sort"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func unpivotSourceLogicalTypeIsTableScalar(logicalType string) bool {
	_, ok := facade.TableReshapeScalarKind(logicalType)
	return ok
}

func unpivotSourceLogicalTypeIsMeltEligible(logicalType string) bool {
	switch strings.ToLower(strings.TrimSpace(logicalType)) {
	case "date", "date_time":
		return false
	}
	return unpivotSourceLogicalTypeIsTableScalar(logicalType)
}

func unpivotMeltKindsSatisfiable(kinds []string) bool {
	classes := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if !unpivotSourceLogicalTypeIsMeltEligible(kind) {
			return false
		}
		class, ok := facade.TableReshapeScalarKind(kind)
		if !ok {
			return false
		}
		classes = append(classes, class)
	}
	if len(classes) == 0 {
		return true
	}
	for _, output := range []string{string(recipe.TableScalarString), string(recipe.TableScalarInteger), string(recipe.TableScalarDecimal), string(recipe.TableScalarBoolean)} {
		compatible := true
		for _, input := range classes {
			if !(output == input || (output == string(recipe.TableScalarDecimal) && input == string(recipe.TableScalarInteger))) {
				compatible = false
				break
			}
		}
		if compatible {
			return true
		}
	}
	return false
}

func unpivotInputColumnIDs(unpivot *ws.ConstructionUnpivot) []string {
	if unpivot == nil {
		return nil
	}
	ids := make([]string, 0, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		ids = append(ids, input.ColumnID)
	}
	return ids
}

var unpivotSourceReasons = rowSourceLaneReasons{
	codeStem:        "UNPIVOT",
	reachReason:     "the current construction prefix does not carry root scalar source fields to this stage",
	versionReason:   "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Unpivot source fields",
	routeReason:     "every root scalar field enumerated at this stage is a reference, repeated, non-root, or metadata path that the Unpivot step cannot reach",
	typeReason:      "every root scalar field enumerated at this stage is not a string, integer, decimal, or boolean scalar that the Unpivot step can melt",
	observedReason:  "every root scalar field enumerated for the Unpivot step at this stage has no observed values in the current dataset snapshot",
	genericCode:     "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS",
	genericReason:   "the selected stage has no authorized populated string, integer, decimal, or boolean scalar FHIR fields that the Unpivot step can melt",
	inventoryReason: "the row-choice inventory backing root Unpivot melt source fields is unavailable for the current dataset snapshot",
}

func validateUnpivotSourceColumns(steps []ws.ConstructionStep, unpivotID string, sources []ConstructionUnpivotSourceSelection) error {
	return validateConstructionSourceColumns(steps, unpivotID, ws.ConstructionOperationUnpivot,
		constructionSourceSelectionIDs(sources, func(source ConstructionUnpivotSourceSelection) string { return source.ColumnID }))
}

func clearConstructionUnpivotOwnedSources(base, candidate ws.Construction, unpivotID string) (ws.Construction, error) {
	unpivotIndex := constructionStepIndex(candidate.Steps, unpivotID)
	if unpivotIndex < 0 {
		return ws.Construction{}, fmt.Errorf("the edited Unpivot step is missing")
	}
	owned := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == unpivotID {
			owned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == unpivotID {
			owned[projection.ColumnID] = true
		}
	}
	steps := make([]ws.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= unpivotIndex {
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
		if projection.OwnerStepID != unpivotID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnedUnpivotSourceColumnID(construction ws.Construction, stageID, path string) string {
	return constructionOwnedSourceColumnID(construction, stageID, path, ws.ConstructionOperationUnpivot)
}

func ConstructionUnpivotSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return constructionSourceFacts(snapshot, rootResourceType, fieldPath, unpivotSourceLogicalTypeIsMeltEligible)
}

func ConstructionHasUnpivotSourceInput(construction ws.Construction) bool {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		if !isUnpivotOwnedSourceProjection(construction, projection) {
			continue
		}
		return true
	}
	return false
}

func isUnpivotOwnedSourceProjection(construction ws.Construction, projection ws.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == ws.ConstructionOperationUnpivot && step.Operation.Unpivot != nil &&
		containsConstructionColumnIDLocal(unpivotInputColumnIDs(step.Operation.Unpivot), projection.ColumnID)
}

func (s *Service) ConstructionUnpivotSourceCapability(
	ctx context.Context,
	base Base,
	outputID, stageID string,
) (ConstructionUnpivotSourceCapability, error) {
	result, err := s.constructionSourceCapability(ctx, base, outputID, stageID, unpivotSourceReasons, "unpivot",
		unpivotSourceLogicalTypeIsMeltEligible, constructionOwnedUnpivotSourceColumnID)
	return ConstructionUnpivotSourceCapability(result), err
}

func (s *Service) ResolveConstructionUnpivotSource(
	ctx context.Context,
	base Base,
	request ProposalRequest,
	acceptedStageID string,
	selection ConstructionUnpivotSourceSelection,
) (ws.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_UNPIVOT_SOURCE_CHOICE", "the selected unpivot source field choice is invalid", err)
	}
	if s.config.RowChoiceResolver == nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized unpivot source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.Snapshot.Token || identity.SchemaDigest != base.Snapshot.Identity.SchemaDigest {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_UNPIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, ports.RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.Snapshot.Clone(), Route: base.Document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: ports.RowChoiceFieldGroup,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceFieldGroupKey ||
		identity.Occurrence.OccurrenceID != ws.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
		resolved.OccurrenceID != ws.RootOccurrenceID {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_UNPIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", resolveErr)
	}
	rootNode, ok := base.Snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.Document.RootResourceType {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_UNPIVOT_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.Snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("UNSUPPORTED_UNPIVOT_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	if !unpivotSourceLogicalTypeIsTableScalar(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("UNSUPPORTED_UNPIVOT_SOURCE_FIELD", "the physical table compiler refuses this logical type as an unpivot melt value column", nil)
	}
	if !unpivotSourceLogicalTypeIsMeltEligible(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD", "the unpivot melt lane refuses date and date_time value columns; the physical compiler's acceptance of the date family is not consent for this channel", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return ws.ConstructionSourceProjection{}, conflict("STALE_UNPIVOT_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", err)
	}
	label := constructionFieldDisplayLabel(base.Document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return ws.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

func (s *Service) ConstructionCandidateWithUnpivotSources(
	ctx context.Context,
	base Base,
	request ProposalRequest,
	candidate ws.Construction,
) (ws.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyUnpivotSources := request.UnpivotSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == ws.ConstructionOperationUnpivot
	candidate = preserveConstructionOwnedInputs(base.Construction, candidate, "")
	if !applyUnpivotSources {
		return candidate, nil
	}
	unpivot := candidate.Steps[changedIndex].Operation.Unpivot
	if unpivot == nil {
		return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_REQUIRES_UNPIVOT", "unpivotSources can only be applied to an Unpivot step", nil)
	}
	if len(request.UnpivotSources) == 0 {
		cleared, err := clearConstructionUnpivotOwnedSources(base.Construction, candidate, request.ChangedStepID)
		if err != nil {
			return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_STAGE_UNAVAILABLE", "the Unpivot input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.Stages, acceptedStageID); !found {
		return ws.Construction{}, conflict("STALE_STAGE_REFERENCE", "the unpivot source choices are not bound to an accepted stage", nil)
	}
	inputIDs := unpivotInputColumnIDs(unpivot)
	if len(request.UnpivotSources) > len(inputIDs) {
		return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_COLUMN_MISMATCH", fmt.Sprintf("unpivotSources carries %d selections but the edited Unpivot step melts %d value inputs", len(request.UnpivotSources), len(inputIDs)), nil)
	}
	for _, selection := range request.UnpivotSources {
		if !containsConstructionColumnIDLocal(inputIDs, selection.ColumnID) {
			return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_COLUMN_MISMATCH", fmt.Sprintf("unpivotSources columnId %q is not a melt input of the edited Unpivot step", selection.ColumnID), nil)
		}
	}

	resolved := make([]ws.ConstructionSourceProjection, 0, len(request.UnpivotSources))
	for _, selection := range request.UnpivotSources {
		projection, err := s.ResolveConstructionUnpivotSource(ctx, base, request, acceptedStageID, selection)
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
			return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_FIELD_ALREADY_PROJECTED", fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this melt input", projection.FieldPath), nil)
		}
	}

	meltKinds := make([]string, 0, len(inputIDs))
	byColumnID := make(map[string]ws.ConstructionSourceProjection, len(projections))
	for _, projection := range projections {
		byColumnID[projection.ColumnID] = projection
	}
	for _, inputID := range inputIDs {
		if projection, owned := byColumnID[inputID]; owned {
			meltKinds = append(meltKinds, projection.LogicalType)
			continue
		}
		if stage, found := constructionReceiptStage(base.Stages, acceptedStageID); found {
			for _, column := range stage.Columns {
				if column.ID == inputID {
					meltKinds = append(meltKinds, column.Type)
					break
				}
			}
		}
	}
	if !unpivotMeltKindsSatisfiable(meltKinds) {
		return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_TYPES_INCOMPATIBLE", "the melted value columns, including the selected root field, cannot share one scalar value type; the physical table compiler refuses this melt (tableScalarKindsCompatible)", nil)
	}

	steps := make([]ws.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index < changedIndex; index++ {
		step := &steps[index]
		if !ws.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation into the Unpivot input", nil)
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
	meltOutputs := make([]ws.StageColumn, 0, len(steps[changedIndex].Outputs))
	for _, output := range steps[changedIndex].Outputs {
		if droppedOwned[output.ID] && !reowned[output.ID] {
			continue
		}
		meltOutputs = append(meltOutputs, output)
	}
	steps[changedIndex].Outputs = meltOutputs

	candidate.Steps, candidate.SourceProjections = steps, projections
	if err := validateUnpivotSourceColumns(candidate.Steps, request.ChangedStepID, request.UnpivotSources); err != nil {
		return ws.Construction{}, unprocessable("UNPIVOT_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

func (s *Service) ReauthorizeConstructionUnpivotSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.reauthorizeConstructionSources(ctx, base, outputID, construction, unpivotSourceReauthorization)
}
