package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func lowerConstructionRelatedField(
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedField,
	inputByID map[string]CompiledOutputColumn,
	outputByID map[string]recipe.StageColumn,
	inputSchema []CompiledOutputColumn,
	inputRow string,
	trackPresence bool,
) (ir.PhysicalStageRelatedField, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	anchor, found := activeRelatedRecordColumn(inputSchema)
	if !found || anchor.RelatedRecordAnchor == nil {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field requires an active exact terminal record identity")
	}
	source := related.Source
	if source.NodeID != anchor.RelatedRecordAnchor.TargetNodeID || source.ResourceType != anchor.RelatedRecordAnchor.TargetResourceType {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field source does not match the active terminal resource")
	}
	if source.Cardinality != string(expression.OptionalOne) && source.Cardinality != string(expression.RequiredOne) {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field source must be scalar")
	}
	if _, scalar := tableReshapeScalarKind(source.LogicalType); !scalar {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field logical type %q is not a supported scalar", source.LogicalType)
	}
	path, err := relatedFieldPhysicalPath(source.ResourceType, source.Path)
	if err != nil {
		return ir.PhysicalStageRelatedField{}, nil, nil, err
	}
	if _, ok := inputByID[anchor.ID]; !ok {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("active terminal record identity is absent from the input schema")
	}
	declaration, ok := outputByID[related.OutputColumnID]
	if !ok || declaration.Nullable == false {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field output must exist and be nullable")
	}
	if declaration.Type != "" && declaration.Type != "INFER" && declaration.Type != source.LogicalType {
		return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field output type does not match the exact source type")
	}

	compiled := make([]CompiledOutputColumn, 0, len(step.Outputs))
	projections := make([]ir.PhysicalProjection, 0, len(step.Outputs))
	for _, output := range step.Outputs {
		if prior, exists := inputByID[output.ID]; exists {
			if prior.Internal {
				return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field cannot expose hidden input column %q", output.ID)
			}
			prior.Name = output.Name
			prior.Label = constructionFirstNonEmpty(output.Label, prior.Label, output.Name)
			compiled = append(compiled, prior)
			projections = append(projections, ir.PhysicalProjection{
				Name: output.Name, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{inputByID[output.ID].Name}},
			})
			continue
		}
		if output.ID != related.OutputColumnID {
			return ir.PhysicalStageRelatedField{}, nil, nil, fmt.Errorf("related field output schema contains unexpected column ID %q", output.ID)
		}
		compiled = append(compiled, CompiledOutputColumn{
			ID: output.ID, Name: output.Name, Label: constructionFirstNonEmpty(output.Label, output.Name),
			SemanticPath: "related_field:" + source.CandidateID, Kind: source.LogicalType,
			Cardinality: string(expression.OptionalOne), Nullable: true,
		})
		if trackPresence {
			companionName := constructionPresenceCompanionName("related", output.ID)
			compiled[len(compiled)-1].PresenceCompanionName = companionName
		}
		projections = append(projections, ir.PhysicalProjection{
			Name: output.Name, Expression: &ir.PhysicalExpression{
				Kind: ir.PhysicalRelatedFieldExpression, Cardinality: ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				RelatedField: &ir.PhysicalRelatedField{
					DocumentID:   ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
					ResourceType: source.ResourceType, Path: path,
				},
			},
		})
		if trackPresence {
			companionName := constructionPresenceCompanionName("related", output.ID)
			projections = append(projections, ir.PhysicalProjection{
				Name: companionName, Hidden: true,
				Expression: &ir.PhysicalExpression{
					Kind: ir.PhysicalRelatedFieldExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					RelatedField: &ir.PhysicalRelatedField{
						DocumentID:   ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
						ResourceType: source.ResourceType, Path: append([]string(nil), path...), PresenceOnly: true,
					},
				},
			})
		}
	}
	physical := ir.PhysicalStageRelatedField{
		ActiveRecordColumn: anchor.Name, CandidateID: source.CandidateID,
		TargetNodeID: source.NodeID, TargetResourceType: source.ResourceType,
		OutputColumnID: related.OutputColumnID, LogicalType: source.LogicalType,
		Path: path, Nullable: source.Cardinality == string(expression.OptionalOne),
	}
	return physical, projections, compiled, nil
}

func relatedFieldPhysicalPath(resourceType, path string) ([]string, error) {
	if strings.TrimSpace(resourceType) == "" || strings.TrimSpace(path) != path || path == "" {
		return nil, fmt.Errorf("related field path must be exact")
	}
	selectorPath := strings.TrimPrefix(path, resourceType+".")
	pathSegments, err := spec.ParseDirectScalarSelector(selectorPath)
	if err != nil {
		return nil, fmt.Errorf("related field path is not a direct scalar selector: %w", err)
	}
	return append([]string{"payload"}, pathSegments...), nil
}
