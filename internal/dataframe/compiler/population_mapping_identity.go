package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func finalConstructionPopulationMappingIdentity(plan ir.PhysicalPlan, output lower.CompiledRecipeOutput) (*ir.PhysicalStagePopulationMappingReturn, *spec.RowIdentity, error) {
	sequence := plan.StageSequence
	if sequence == nil || output.RowIdentity == nil || output.RowIdentity.Validate() != nil || output.RowIdentity.Grain != output.RowGrain {
		return nil, nil, fmt.Errorf("construction population mapping requires a valid row identity")
	}
	effectiveIdentity := output.RowIdentity.Clone()
	if len(effectiveIdentity.Fields) != 1 || effectiveIdentity.Fields[0] != sequence.FinalRowIdentity {
		defaultIdentity, ok := spec.DefaultRowIdentity(output.RowGrain)
		canonicalDefault := ok && len(effectiveIdentity.Fields) == len(defaultIdentity.Fields)
		for index := range effectiveIdentity.Fields {
			canonicalDefault = canonicalDefault && effectiveIdentity.Fields[index] == defaultIdentity.Fields[index]
		}
		if !canonicalDefault {
			return nil, nil, fmt.Errorf("construction population mapping requires the exact final row identity")
		}
		switch sequence.FinalRowIdentity {
		case "_key":
			if len(defaultIdentity.Fields) != 2 || defaultIdentity.Fields[0] != "project" || defaultIdentity.Fields[1] != "_key" {
				return nil, nil, fmt.Errorf("construction population mapping requires the exact final row identity")
			}
			project, ok := output.Plan.BindVars["project"].(string)
			if !ok || strings.TrimSpace(project) == "" {
				return nil, nil, fmt.Errorf("canonical project/key identity requires the pinned project bind")
			}
		case "__loom_row_id":
		default:
			return nil, nil, fmt.Errorf("construction population mapping requires the exact final row identity")
		}
		effectiveIdentity.Fields = []string{sequence.FinalRowIdentity}
	}
	var finalIdentityColumn ir.PhysicalStageColumn
	identityColumnFound := false
	for _, column := range sequence.FinalColumns {
		if column.Name == sequence.FinalRowIdentity {
			finalIdentityColumn, identityColumnFound = column, true
			break
		}
	}
	cohortIdentityLineage, validCohortIdentity := constructionCohortGroupPopulationMappingIdentity(sequence, output, finalIdentityColumn)
	identityFound := false
	if cohortIdentityLineage {
		identityFound = identityColumnFound && validCohortIdentity
	} else {
		identityFound = identityColumnFound && finalIdentityColumn.Internal && finalIdentityColumn.Identity &&
			finalIdentityColumn.Kind == "string" && finalIdentityColumn.Cardinality == "required_one"
	}
	if !identityFound {
		if cohortIdentityLineage {
			return nil, nil, fmt.Errorf("construction cohort identity field %q is not a required group identity object over required string components", sequence.FinalRowIdentity)
		}
		return nil, nil, fmt.Errorf("construction identity field %q is not a required scalar string in the final typed stage", sequence.FinalRowIdentity)
	}
	for _, name := range []string{"__loom_root_contributor_keys", "_key"} {
		for _, column := range sequence.FinalColumns {
			if !column.Internal || column.Name != name || column.RootContributorResourceType != output.RootResourceType || column.Kind != "string" {
				continue
			}
			many := column.Cardinality == "many"
			if !many && column.Cardinality != "required_one" && column.Cardinality != "optional_one" {
				continue
			}
			return &ir.PhysicalStagePopulationMappingReturn{
				FinalRootContributorColumn: column.Name, FinalRootContributorsMany: many,
				RowIdentityColumn: sequence.FinalRowIdentity,
			}, effectiveIdentity, nil
		}
	}
	return nil, nil, fmt.Errorf("construction population mapping requires compiler-proven root contributor identities in its final stage")
}

func constructionCohortGroupPopulationMappingIdentity(sequence *ir.PhysicalStageSequence, output lower.CompiledRecipeOutput, finalIdentityColumn ir.PhysicalStageColumn) (bool, bool) {
	if sequence == nil || len(sequence.Stages) == 0 || sequence.FinalRowIdentity != "__loom_row_id" {
		return false, false
	}
	identityName := sequence.FinalRowIdentity
	cohortStageIndex := -1
	for index := len(sequence.Stages) - 1; index >= 0; index-- {
		stage := sequence.Stages[index]
		if stage.RowIdentityColumn != identityName {
			return false, false
		}
		if stage.Kind == ir.PhysicalStageCohortGroupOp || stage.CohortGroup != nil {
			cohortStageIndex = index
			break
		}
		switch stage.Kind {
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedSourceOp,
			ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
		default:
			return false, false
		}
	}
	if cohortStageIndex < 0 {
		return false, false
	}

	if output.RowGrain != spec.RowGrainGroups || output.RowIdentity == nil ||
		output.RowIdentity.Grain != output.RowGrain || len(output.RowIdentity.Fields) != 1 || output.RowIdentity.Fields[0] != identityName {
		return true, false
	}
	if !validCohortGroupPopulationMappingIdentityColumn(finalIdentityColumn, identityName) {
		return true, false
	}
	if sequence.Stages[len(sequence.Stages)-1].ID != sequence.FinalStageID {
		return true, false
	}

	for index := cohortStageIndex; index < len(sequence.Stages); index++ {
		stage := sequence.Stages[index]
		previousStageID := sequence.SourceStageID
		if index > 0 {
			previousStageID = sequence.Stages[index-1].ID
		}
		if stage.ID == "" || stage.InputStageID != previousStageID || stage.RowIdentityColumn != identityName {
			return true, false
		}
		if index == cohortStageIndex {
			if stage.Kind != ir.PhysicalStageCohortGroupOp || stage.CohortGroup == nil ||
				!validCohortGroupPopulationMappingIdentityColumns(stage.OutputColumns, identityName) {
				return true, false
			}
			continue
		}
		if !constructionPopulationMappingPreservesIdentity(stage.Kind) ||
			!hasCohortGroupPopulationMappingIdentityColumn(stage.InputColumns, identityName) ||
			!hasCohortGroupPopulationMappingIdentityColumn(stage.OutputColumns, identityName) {
			return true, false
		}
	}
	return true, hasCohortGroupPopulationMappingIdentityColumn(sequence.FinalColumns, identityName)
}

func constructionPopulationMappingPreservesIdentity(kind ir.PhysicalStageOperationKind) bool {
	switch kind {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedSourceOp,
		ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func validCohortGroupPopulationMappingIdentityColumns(columns []ir.PhysicalStageColumn, identityName string) bool {
	identityFound := false
	var revisionFound, groupFound bool
	for _, column := range columns {
		switch column.Name {
		case identityName:
			identityFound = validCohortGroupPopulationMappingIdentityColumn(column, identityName)
		case "group_revision_id":
			revisionFound = column.Identity && column.Kind == "string" && column.Cardinality == "required_one"
		case "group_id":
			groupFound = column.Identity && column.Kind == "string" && column.Cardinality == "required_one"
		}
	}
	return identityFound && revisionFound && groupFound
}

func hasCohortGroupPopulationMappingIdentityColumn(columns []ir.PhysicalStageColumn, identityName string) bool {
	for _, column := range columns {
		if column.Name == identityName {
			return validCohortGroupPopulationMappingIdentityColumn(column, identityName)
		}
	}
	return false
}

func validCohortGroupPopulationMappingIdentityColumn(column ir.PhysicalStageColumn, identityName string) bool {
	return column.Name == identityName && column.Internal && column.Identity &&
		column.Kind == "object" && column.Cardinality == "required_one"
}

func finalPopulationMappingIdentity(plan ir.PhysicalPlan, identity *spec.RowIdentity) (int, []ir.PhysicalPopulationMappingIdentityPart, *ir.PhysicalExpression, error) {
	if identity == nil {
		return 0, nil, nil, fmt.Errorf("population mapping requires a row identity")
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := make(map[string]ir.PhysicalProjection, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			projections[projection.Name] = projection
		}
		parts := make([]ir.PhysicalPopulationMappingIdentityPart, 0, len(identity.Fields))
		for _, field := range identity.Fields {
			projection, projected := projections[field]
			if projected {
				expression, err := physicalProjectionExpression(projection)
				if err != nil {
					return 0, nil, nil, fmt.Errorf("identity field %q: %w", field, err)
				}
				parts = append(parts, ir.PhysicalPopulationMappingIdentityPart{Name: field, Expression: expression})
				continue
			}
			if _, bound := plan.BindVars[field]; bound {
				parts = append(parts, ir.PhysicalPopulationMappingIdentityPart{Name: field, Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: field}}})
				continue
			}
			return 0, nil, nil, fmt.Errorf("identity field %q is not present in the final return projection or compiler bind vars", field)
		}
		var explicit *ir.PhysicalExpression
		if projection, ok := projections["__loom_row_id"]; ok {
			expression, err := physicalProjectionExpression(projection)
			if err != nil {
				return 0, nil, nil, fmt.Errorf("explicit row identity: %w", err)
			}
			explicit = &expression
		}
		return index, parts, explicit, nil
	}
	return 0, nil, nil, fmt.Errorf("population mapping final output has no RETURN operation")
}

func physicalProjectionExpression(projection ir.PhysicalProjection) (ir.PhysicalExpression, error) {
	if projection.Expression != nil {
		return ir.ClonePhysicalExpression(*projection.Expression), nil
	}
	if projection.Value.Variable == "" && projection.Value.BindKey == "" {
		return ir.PhysicalExpression{}, fmt.Errorf("final return projection has no typed expression or value")
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &projection.Value}, nil
}

func mappingIdentityPartsColumn(plan ir.PhysicalPlan) string {
	if plan.StageSequence != nil && plan.StageSequence.PopulationMappingReturn != nil {
		return ir.PhysicalPopulationMappingIdentityPartsField
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp && operation.PopulationMappingReturn != nil && operation.PopulationMappingReturn.ExplicitIdentity == nil && len(operation.PopulationMappingReturn.IdentityParts) > 0 {
			return ir.PhysicalPopulationMappingIdentityPartsField
		}
	}
	return ""
}

func mappingExplicitIdentityColumn(plan ir.PhysicalPlan) string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp && operation.PopulationMappingReturn != nil && operation.PopulationMappingReturn.ExplicitIdentity != nil {
			return ir.PhysicalPopulationMappingExplicitIdentityField
		}
	}
	return ""
}
