package rowsource

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/rowdef"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

type Config struct {
	Capability        ports.CapabilityResolver
	SemanticInventory func(context.Context, semantic.PageOptions) (semantic.Page, error)
	RowChoiceResolver rowdef.RowChoiceResolver
	RowChoicePlanner  rowdef.RowChoicePlanner
}

type Service struct {
	config Config
}

func NewService(config Config) *Service {
	return &Service{config: config}
}

type RowChoiceResolver = rowdef.RowChoiceResolver
type RowChoicePlanner = rowdef.RowChoicePlanner
type RowChoiceResolveRequest = rowdef.RowChoiceResolveRequest

const RowChoiceFieldGroup = rowdef.RowChoiceFieldGroup

const RowChoiceExpanded = rowdef.RowChoiceExpanded

func (s *Service) ConstructionHasFilterSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasFilterSourceInput(construction)
}

func (s *Service) ConstructionHasDeriveSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasDeriveSourceInput(construction)
}

func (s *Service) ConstructionHasExpandSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasExpandSourceInput(construction)
}

func (s *Service) ConstructionHasUnpivotSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasUnpivotSourceInput(construction)
}

func DeriveSourceLogicalTypeIsNumeric(logicalType string) bool {
	return deriveSourceLogicalTypeIsNumeric(logicalType)
}

func UnpivotSourceLogicalTypeIsTableScalar(logicalType string) bool {
	return unpivotSourceLogicalTypeIsTableScalar(logicalType)
}

func UnpivotSourceLogicalTypeIsMeltEligible(logicalType string) bool {
	return unpivotSourceLogicalTypeIsMeltEligible(logicalType)
}

func UnpivotMeltKindsSatisfiable(kinds []string) bool {
	return unpivotMeltKindsSatisfiable(kinds)
}

func (s *Service) ConstructionDeriveSourceCapability(
	ctx context.Context,
	base Base,
	outputID, stageID string,
) (ConstructionDeriveSourceCapability, error) {
	result := ConstructionDeriveSourceCapability{StageID: stageID, Choices: []ConstructionDeriveSourceChoice{}}
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
			result.ReasonCode, result.Reason = rowSourceInventoryFailure(deriveSourceReasons)
			return result, unavailable("construction-capabilities", result.ReasonCode, result.Reason,
				fmt.Errorf("list authorized root derive source choices for output %q: %w", outputID, err))
		}
		for _, choice := range choices {
			if rowSourceChoiceIsStaleVersion(choice, base.Snapshot) {
				enumeration.skip(rowSourceSkipVersion)
				continue
			}
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				enumeration.skip(rowSourceSkipRoute)
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.Snapshot, choice)
			if !ok {
				enumeration.skip(rowSourceCandidateSkipClass(base.Snapshot, choice))
				continue
			}
			if !deriveSourceLogicalTypeIsNumeric(candidate.LogicalType) {
				enumeration.skip(rowSourceSkipType)
				continue
			}
			result.Choices = append(result.Choices, ConstructionDeriveSourceChoice{
				ChoiceID: choice.ChoiceID, FieldPath: choice.Path,
				Label:       constructionFieldDisplayLabel(base.Document.RootResourceType, choice.Path, candidate.Label, choice.Label),
				LogicalType: candidate.LogicalType, IsPopulated: candidate.Populated,
				ColumnID: constructionOwnedDeriveSourceColumnID(base.Construction, stageID, choice.Path),
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

func (s *Service) ResolveConstructionDeriveSource(
	ctx context.Context,
	base Base,
	request ProposalRequest,
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
	if identity.SnapshotToken != base.Snapshot.Token || identity.SchemaDigest != base.Snapshot.Identity.SchemaDigest {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.Snapshot.Clone(), Route: base.Document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceFieldGroupKey ||
		identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
		resolved.OccurrenceID != authoringv2.RootOccurrenceID {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
	}
	rootNode, ok := base.Snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.Document.RootResourceType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_DERIVE_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.Snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
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
	label := constructionFieldDisplayLabel(base.Document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return authoringv2.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

func (s *Service) ConstructionCandidateWithDeriveSources(
	ctx context.Context,
	base Base,
	request ProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyDeriveSources := request.DeriveSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationDerive
	candidate = preserveConstructionOwnedInputs(base.Construction, candidate, "")
	if !applyDeriveSources {
		return candidate, nil
	}
	derive := candidate.Steps[changedIndex].Operation.Derive
	if derive == nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_REQUIRES_DERIVE", "deriveSources can only be applied to a Derive step", nil)
	}
	if len(request.DeriveSources) == 0 {
		cleared, err := clearConstructionDeriveOwnedSources(base.Construction, candidate, request.ChangedStepID)
		if err != nil {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "DERIVE_SOURCE_STAGE_UNAVAILABLE", "the Derive input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.Stages, acceptedStageID); !found {
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
		projection, err := s.ResolveConstructionDeriveSource(ctx, base, request, acceptedStageID, selection)
		if err != nil {
			return authoringv2.Construction{}, err
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

	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(base.Construction.SourceProjections)+len(resolved))
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

func (s *Service) ReauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
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
			listed, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.Snapshot.Clone(), base.Document)
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
			if _, found := base.Snapshot.Node(choice.NodeID); !found {
				continue
			}
			derived, ok := ConstructionDeriveSourceFacts(base.Snapshot, base.Document.RootResourceType, projection.FieldPath)
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
