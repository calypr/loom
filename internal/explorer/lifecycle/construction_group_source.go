package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func (s *Service) constructionGroupSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (ConstructionGroupSourceCapability, error) {
	result := ConstructionGroupSourceCapability{StageID: stageID, Choices: []ConstructionGroupSourceChoice{}}
	if stageID != recipe.ConstructionSourceProjectionID {
		result.ReasonCode = "TRANSFORMED_STAGE_SOURCE_INPUT_UNSUPPORTED"
		result.Reason = "scalar source inputs are currently available only at the direct source stage"
		return result, nil
	}
	if s.config.RowChoicePlanner == nil {
		result.ReasonCode = "ROW_CHOICE_UNAVAILABLE"
		result.Reason = "authorized scalar source choices are not configured"
		return result, nil
	}
	choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
	if err != nil {
		return result, fmt.Errorf("list authorized row choices for output %q: %w", outputID, err)
	}
	for _, choice := range choices {
		if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID ||
			choice.Reference || fhirNonGroupableSourcePath(choice.Path) {
			continue
		}
		candidate, ok := constructionGroupSourceCandidate(base.snapshot, choice)
		if !ok {
			continue
		}
		result.Choices = append(result.Choices, ConstructionGroupSourceChoice{
			ChoiceID: choice.ChoiceID, OccurrenceID: choice.OccurrenceID, FieldPath: choice.Path,
			Label: choice.Label, FHIRType: choice.FHIRType, LogicalType: candidate.LogicalType,
			ValueType: choice.ValueType, IsIdentifier: false, IsReference: false, IsPopulated: candidate.Populated,
		})
	}
	if len(result.Choices) == 0 {
		result.ReasonCode = "NO_ELIGIBLE_POPULATED_SCALAR_GROUP_FIELDS"
		result.Reason = "the root source stage has no authorized populated non-ID, non-metadata scalar field that supports VALUE projection"
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func (s *Service) constructionCandidateWithGroupSource(ctx context.Context, base constructionBase, request ConstructionProposalRequest) (authoringv2.Construction, error) {
	candidate := request.CandidateConstruction
	var groupKeyInputID string
	if len(candidate.Steps) > 0 && candidate.Steps[0].Operation.Kind == authoringv2.ConstructionOperationGroup && candidate.Steps[0].Operation.Group != nil && len(candidate.Steps[0].Operation.Group.Keys) == 1 {
		groupKeyInputID = candidate.Steps[0].Operation.Group.Keys[0].InputColumnID
	}
	projections := make([]authoringv2.ConstructionSourceProjection, 0, 1)
	if request.GroupSource != nil {
		if len(candidate.Steps) == 0 || candidate.Steps[0].Operation.Kind != authoringv2.ConstructionOperationGroup || candidate.Steps[0].Operation.Group == nil {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "SOURCE_INPUT_REQUIRES_DIRECT_GROUP", "a scalar source choice must be consumed by the first direct-source GROUP step", nil)
		}
		group := candidate.Steps[0].Operation.Group
		if len(group.Keys) != 1 || len(group.Aggregates) != 1 || group.Aggregates[0].Operation != authoringv2.ConstructionGroupCountRows {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "UNSUPPORTED_SOURCE_GROUP_SHAPE", "scalar source grouping currently requires one key and one COUNT_ROWS aggregate", nil)
		}
		if group.Keys[0].InputColumnID != request.GroupSource.ColumnID {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "SOURCE_INPUT_COLUMN_MISMATCH", "groupSource.columnId must match the direct GROUP key inputColumnId", nil)
		}
		projection, err := s.resolveConstructionGroupSource(ctx, base, request, groupKeyInputID)
		if err != nil {
			return authoringv2.Construction{}, err
		}
		projections = append(projections, projection)
	} else if groupKeyInputID != "" {
		for _, projection := range base.construction.SourceProjections {
			if projection.ColumnID == groupKeyInputID {
				projections = append(projections, projection)
				break
			}
		}
	}
	candidate.SourceProjections = projections
	return candidate, nil
}

func (s *Service) resolveConstructionGroupSource(ctx context.Context, base constructionBase, request ConstructionProposalRequest, columnID string) (authoringv2.ConstructionSourceProjection, error) {
	if s.config.RowChoiceResolver == nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized scalar source choice resolution is not configured", nil)
	}
	resolved, err := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.snapshot.Clone(), Route: base.document.Route,
		RowChoiceID: request.GroupSource.RowChoiceID, ExpectedKind: RowChoiceFieldGroup,
	})
	if err != nil || validateResolvedRowChoice(base.document.Route, resolved, RowChoiceFieldGroup) != nil || resolved.OccurrenceID != authoringv2.RootOccurrenceID {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_GROUP_SOURCE_CHOICE", "reload row choices and select a current root scalar field", nil, err)
	}
	identity, err := capability.DecodeRowChoiceID(request.GroupSource.RowChoiceID)
	if err != nil || identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest ||
		identity.Kind != capability.RowChoiceFieldGroupKey || identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference || identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_GROUP_SOURCE_CHOICE", "the selected source choice does not match the current scalar root field", nil, err)
	}
	if fhirIdentifierPath(resolved.FieldPath) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "IDENTIFIER_GROUP_SOURCE_UNSUPPORTED", "resource identifier fields cannot be used as scalar grouping keys", nil)
	}
	if canonicalRowPath(resolved.FieldPath) == "resourceType" {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "RESOURCE_METADATA_GROUP_SOURCE_UNSUPPORTED", "the constant FHIR resourceType metadata field cannot be used as a useful grouping key", nil)
	}
	rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_GROUP_SOURCE_CHOICE", "the selected source choice no longer identifies the authorized root resource", nil, nil)
	}
	var candidate *capability.Candidate
	for index := range base.snapshot.Candidates {
		current := &base.snapshot.Candidates[index]
		if current.NodeID == rootNode.ID && current.ResourceType == rootNode.ResourceType && canonicalRowPath(current.FieldPath) == resolved.FieldPath {
			if candidate != nil {
				return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "AMBIGUOUS_GROUP_SOURCE_FIELD", "the selected scalar field is ambiguous in the current capability snapshot", nil, nil)
			}
			candidate = current
		}
	}
	if candidate == nil || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 || capability.IsRepeatedCardinality(candidate.Cardinality) ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_GROUP_SOURCE_FIELD", "the selected root field does not support a scalar VALUE projection", nil)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_GROUP_SOURCE_SCHEMA", "the selected field no longer matches generated FHIR scalar metadata", nil, err)
	}
	label := strings.TrimSpace(facts.Title)
	if label == "" {
		label = strings.TrimSpace(candidate.Label)
	}
	if label == "" {
		label = resolved.FieldPath
	}
	return authoringv2.ConstructionSourceProjection{
		ColumnID: columnID, OccurrenceID: resolved.OccurrenceID, FieldPath: facts.CanonicalPath,
		FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
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

func fhirNonGroupableSourcePath(path string) bool {
	return fhirIdentifierPath(path) || canonicalRowPath(path) == "resourceType"
}

func containsSourceProjectionMode(modes []capability.ProjectionMode, want capability.ProjectionMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}
