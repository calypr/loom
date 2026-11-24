package rowsource

import (
	"context"
	"fmt"
	"sort"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

type Config struct {
	Capability        ports.CapabilityResolver
	SemanticInventory func(context.Context, semantic.PageOptions) (semantic.Page, error)
	RowChoiceResolver ports.RowChoiceResolver
	RowChoicePlanner  ports.RowChoicePlanner
}

type Service struct {
	config Config
}

func NewService(config Config) *Service {
	return &Service{config: config}
}

func (s *Service) ConstructionHasFilterSourceInput(construction ws.Construction) bool {
	return ConstructionHasFilterSourceInput(construction)
}

func (s *Service) ConstructionHasDeriveSourceInput(construction ws.Construction) bool {
	return ConstructionHasDeriveSourceInput(construction)
}

func (s *Service) ConstructionHasExpandSourceInput(construction ws.Construction) bool {
	return ConstructionHasExpandSourceInput(construction)
}

func (s *Service) ConstructionHasUnpivotSourceInput(construction ws.Construction) bool {
	return ConstructionHasUnpivotSourceInput(construction)
}

func (s *Service) ConstructionDeriveSourceCapability(
	ctx context.Context,
	base Base,
	outputID, stageID string,
) (ConstructionDeriveSourceCapability, error) {
	result, err := s.constructionSourceCapability(ctx, base, outputID, stageID, deriveSourceReasons, "derive",
		deriveSourceLogicalTypeIsNumeric, constructionOwnedDeriveSourceColumnID)
	return result.derive(), err
}

func (s *Service) ResolveConstructionDeriveSource(
	ctx context.Context,
	base Base,
	request ProposalRequest,
	acceptedStageID string,
	selection ConstructionDeriveSourceSelection,
) (ws.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_DERIVE_SOURCE_CHOICE", "the selected derive source field choice is invalid", err)
	}
	if s.config.RowChoiceResolver == nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized derive source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.Snapshot.Token || identity.SchemaDigest != base.Snapshot.Identity.SchemaDigest {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_DERIVE_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil)
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
		return ws.ConstructionSourceProjection{}, conflict("INVALID_DERIVE_SOURCE_CHOICE", "reload the current populated root scalar field choice", resolveErr)
	}
	rootNode, ok := base.Snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.Document.RootResourceType {
		return ws.ConstructionSourceProjection{}, conflict("INVALID_DERIVE_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.Snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("UNSUPPORTED_DERIVE_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	if !deriveSourceLogicalTypeIsNumeric(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("UNSUPPORTED_DERIVE_SOURCE_FIELD", "the selected root field is not an integer or decimal scalar source field", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return ws.ConstructionSourceProjection{}, conflict("STALE_DERIVE_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", err)
	}
	label := constructionFieldDisplayLabel(base.Document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return ws.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

func (s *Service) ConstructionCandidateWithDeriveSources(
	ctx context.Context,
	base Base,
	request ProposalRequest,
	candidate ws.Construction,
) (ws.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyDeriveSources := request.DeriveSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == ws.ConstructionOperationDerive
	candidate = preserveConstructionOwnedInputs(base.Construction, candidate, "")
	if !applyDeriveSources {
		return candidate, nil
	}
	derive := candidate.Steps[changedIndex].Operation.Derive
	if derive == nil {
		return ws.Construction{}, unprocessable("DERIVE_SOURCE_REQUIRES_DERIVE", "deriveSources can only be applied to a Derive step", nil)
	}
	if len(request.DeriveSources) == 0 {
		cleared, err := clearConstructionDeriveOwnedSources(base.Construction, candidate, request.ChangedStepID)
		if err != nil {
			return ws.Construction{}, unprocessable("DERIVE_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return ws.Construction{}, unprocessable("DERIVE_SOURCE_STAGE_UNAVAILABLE", "the Derive input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.Stages, acceptedStageID); !found {
		return ws.Construction{}, conflict("STALE_STAGE_REFERENCE", "the derive source choices are not bound to an accepted stage", nil)
	}
	operandIDs := deriveOperandColumnIDs(derive)
	if len(request.DeriveSources) > len(operandIDs) {
		return ws.Construction{}, unprocessable("DERIVE_SOURCE_COLUMN_MISMATCH", fmt.Sprintf("deriveSources carries %d selections but the edited Derive step consumes %d column operands", len(request.DeriveSources), len(operandIDs)), nil)
	}
	for _, selection := range request.DeriveSources {
		if !containsConstructionColumnIDLocal(operandIDs, selection.ColumnID) {
			return ws.Construction{}, unprocessable("DERIVE_SOURCE_COLUMN_MISMATCH", fmt.Sprintf("deriveSources columnId %q is not a column operand of the edited Derive step", selection.ColumnID), nil)
		}
	}

	resolved := make([]ws.ConstructionSourceProjection, 0, len(request.DeriveSources))
	for _, selection := range request.DeriveSources {
		projection, err := s.ResolveConstructionDeriveSource(ctx, base, request, acceptedStageID, selection)
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
			return ws.Construction{}, unprocessable("DERIVE_SOURCE_FIELD_ALREADY_PROJECTED", fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this operand", projection.FieldPath), nil)
		}
	}

	steps := make([]ws.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index <= changedIndex; index++ {
		step := &steps[index]
		if !ws.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return ws.Construction{}, unprocessable("DERIVE_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
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
	if err := validateDeriveSourceColumns(candidate.Steps, request.ChangedStepID, request.DeriveSources); err != nil {
		return ws.Construction{}, unprocessable("DERIVE_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

func (s *Service) ReauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.reauthorizeConstructionSources(ctx, base, outputID, construction, deriveSourceReauthorization)
}
