package recipe

import (
	"fmt"
	"strings"
)

const CurrentSchemaVersion = 1

const (
	maxExpressionDepth = 64
	maxExpressionNodes = 4096
	maxLiteralArray    = 256
)

type Bundle struct {
	RecipeSchemaVersion int              `json:"recipeSchemaVersion"`
	Name                string           `json:"name"`
	TranslationVersion  string           `json:"translationVersion"`
	Fragments           *FragmentLibrary `json:"fragments,omitempty"`
	Outputs             []Output         `json:"outputs"`
}

func (b Bundle) Validate() error {
	return b.validate(nil, currentRecipeValidation)
}

func (b Bundle) ValidateWithWorkspaceOutputs(allowed []string) error {
	external := make(map[string]struct{}, len(allowed))
	for _, outputID := range allowed {
		if strings.TrimSpace(outputID) != "" {
			external[outputID] = struct{}{}
		}
	}
	return b.validate(external, currentRecipeValidation)
}

func (b Bundle) validate(allowedWorkspaceOutputs map[string]struct{}, mode recipeValidationMode) error {
	if b.RecipeSchemaVersion != CurrentSchemaVersion {
		return validationError("unsupported_schema_version", "$.recipeSchemaVersion", fmt.Sprintf("must be %d", CurrentSchemaVersion))
	}
	if strings.TrimSpace(b.Name) == "" {
		return validationError("required", "$.name", "name is required")
	}
	if strings.TrimSpace(b.TranslationVersion) == "" {
		return validationError("required", "$.translationVersion", "translationVersion is required")
	}
	if len(b.Outputs) == 0 {
		return validationError("required", "$.outputs", "at least one output is required")
	}
	if b.Fragments != nil {
		if err := b.Fragments.Validate(); err != nil {
			return validationError("invalid_fragments", "$.fragments", err.Error())
		}
	}
	seen := map[string]bool{}
	for i, output := range b.Outputs {
		path := fmt.Sprintf("$.outputs[%d]", i)
		if err := validateRecipeName(output.Name, path+".name"); err != nil {
			return err
		}
		if seen[output.Name] {
			return validationError("duplicate_name", path+".name", "duplicate output name")
		}
		seen[output.Name] = true
		if strings.TrimSpace(output.RootResourceType) == "" {
			return validationError("required", path+".rootResourceType", "rootResourceType is required")
		}
		if err := validateOccurrenceID(output.RootOccurrenceID, path+".rootOccurrenceId"); err != nil {
			return err
		}
		if strings.TrimSpace(output.RowGrain) == "" {
			return validationError("required", path+".rowGrain", "rowGrain is required")
		}
		if output.Population != nil {
			if strings.TrimSpace(output.Population.SelectionRevisionID) == "" || strings.TrimSpace(output.Population.MembershipDigest) == "" || strings.TrimSpace(output.Population.ResourceType) == "" || output.Population.MemberCount < 0 {
				return validationError("invalid_population", path+".population", "population identity and resource type are required")
			}
			for index, step := range output.Population.Route {
				if strings.TrimSpace(step.ResourceType) == "" || strings.TrimSpace(step.Relationship) == "" {
					return validationError("invalid_population_route", fmt.Sprintf("%s.population.route[%d]", path, index), "resourceType and relationship are required")
				}
				if step.StorageDirection != "" && step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND" {
					return validationError("invalid_population_route", fmt.Sprintf("%s.population.route[%d].storageDirection", path, index), "storageDirection must be INBOUND or OUTBOUND")
				}
			}
		}
		if !output.TraversalColumnNaming.Valid() {
			return validationError("invalid_traversal_column_naming", path+".traversalColumnNaming", "must be PATH, ALIAS, or EXACT")
		}
		if !output.RootColumnNaming.Valid() {
			return validationError("invalid_root_column_naming", path+".rootColumnNaming", "must be PREFIXED or EXACT")
		}
		if output.CollisionPolicy != "" && output.CollisionPolicy != "error" && output.CollisionPolicy != "overwrite" && output.CollisionPolicy != "coalesce" {
			return validationError("invalid_collision_policy", path+".collisionPolicy", "must be error, overwrite, or coalesce")
		}
		budget := 0
		if err := validateNodeShape(output.Fields, output.Filters, output.Pivots, output.OwnerRecords, output.Aggregates, output.Slices, path, &budget); err != nil {
			return err
		}
		transformedColumns := make(map[string]struct{}, len(output.ColumnTransformations))
		for index, transformation := range output.ColumnTransformations {
			transformationPath := fmt.Sprintf("%s.columnTransformations[%d]", path, index)
			if err := validateRecipeName(transformation.Column, transformationPath+".column"); err != nil {
				return err
			}
			if _, exists := transformedColumns[transformation.Column]; exists {
				return validationError("duplicate_column_transformation", transformationPath+".column", "each output column can have only one value transformation")
			}
			transformedColumns[transformation.Column] = struct{}{}
			if err := transformation.Transformation.Validate(); err != nil {
				return validationError("invalid_column_transformation", transformationPath+".transformation", err.Error())
			}
		}
		if err := validateDerivedColumns(output.DerivedColumns, path+".derivedColumns"); err != nil {
			return err
		}
		if output.Construction != nil {
			if output.ArchiveRetiredReshape != nil || len(output.DerivedColumns) > 0 {
				return validationError("ambiguous_construction", path+".construction", "construction cannot be combined with legacy tableReshape or derivedColumns")
			}
			if err := output.Construction.ValidateWithGroupRows(output.Fields, output.GroupRows); err != nil {
				return validationError("invalid_construction", path+".construction", err.Error())
			}
			if _, terminalCombine := output.Construction.TerminalCombineStep(); terminalCombine {
				if len(output.Filters) != 0 || len(output.Pivots) != 0 || len(output.OwnerRecords) != 0 || len(output.Aggregates) != 0 || len(output.Slices) != 0 || len(output.Traversals) != 0 || output.Expand != nil || output.Identity != nil || output.GroupRows != nil || len(output.DynamicColumns) != 0 || len(output.ExtensionColumns) != 0 || len(output.CatalogProjections) != 0 || len(output.ColumnTransformations) != 0 || output.Population != nil {
					return validationError("invalid_construction", path+".construction", "terminal combine cannot include source projection operations")
				}
			}
		}
		if output.ArchiveRetiredReshape != nil {
			if mode != archiveRecipeValidation {
				return validationError("retired_recipe_shape", path+".tableReshape", "archived tableReshape requires recompilation and is not executable")
			}
			if err := output.ArchiveRetiredReshape.Validate(); err != nil {
				return validationError("invalid_table_reshape", path+".tableReshape", err.Error())
			}
			if output.ArchiveRetiredReshape.Kind == archivedReshapeUnpivot && len(output.DerivedColumns) != 0 {
				return validationError("invalid_table_reshape", path+".tableReshape", "unpivot cannot be combined with derived columns")
			}
			reshapeID := output.ArchiveRetiredReshape.GroupedPivot
			if reshapeID != nil {
				for _, derived := range output.DerivedColumns {
					if derived.ConstructionID == reshapeID.ConstructionID {
						return validationError("duplicate_construction_id", path+".tableReshape.groupedPivot.constructionId", "constructionId must be unique across table-shape constructions")
					}
				}
			}
			unpivotID := output.ArchiveRetiredReshape.Unpivot
			if unpivotID != nil {
				for _, derived := range output.DerivedColumns {
					if derived.ConstructionID == unpivotID.ConstructionID {
						return validationError("duplicate_construction_id", path+".tableReshape.unpivot.constructionId", "constructionId must be unique across table-shape constructions")
					}
				}
			}
		}
		if err := validateTraversals(output.Traversals, path+".traversals", 0); err != nil {
			return err
		}
		if output.TraversalColumnNaming.Normalized() == TraversalColumnNamingAlias {
			if err := validateGloballyUniqueTraversalAliases(output.Traversals, path+".traversals", map[string]string{}); err != nil {
				return err
			}
		}
		if output.Expand != nil {
			if err := validateOccurrenceID(output.Expand.OwnerOccurrenceID, path+".expand.ownerOccurrenceId"); err != nil {
				return err
			}
			if err := validateRecipeName(output.Expand.As, path+".expand.as"); err != nil {
				return err
			}
			if output.Expand.Ordinality != "" {
				if err := validateRecipeName(output.Expand.Ordinality, path+".expand.ordinality"); err != nil {
					return err
				}
				if output.Expand.Ordinality == output.Expand.As {
					return validationError("binding_collision", path+".expand.ordinality", "ordinality must differ from the item binding")
				}
			}
			if !output.Expand.EmptyPolicy.Valid() {
				return validationError("invalid_empty_policy", path+".expand.emptyPolicy", "must be ERROR, EXCLUDE, or PRESERVE_PARENT")
			}
			if err := validateExpressionBudget(output.Expand.From, path+".expand.from", &budget); err != nil {
				return err
			}
		}
		if output.GroupRows != nil {
			if strings.TrimSpace(output.GroupRows.RevisionID) == "" || output.GroupRows.RevisionID != strings.TrimSpace(output.GroupRows.RevisionID) {
				return validationError("invalid_group_rows", path+".groupRows.revisionId", "revisionId must be a non-empty trimmed identity")
			}
			switch output.GroupRows.UnassignedMemberPolicy {
			case "ERROR", "EXCLUDE", "GROUP_AS_UNASSIGNED":
			default:
				return validationError("invalid_group_rows", path+".groupRows.unassignedMemberPolicy", "must be ERROR, EXCLUDE, or GROUP_AS_UNASSIGNED")
			}
			if output.Expand != nil {
				return validationError("invalid_group_rows", path+".groupRows", "group rows cannot be combined with row expansion")
			}
			if err := validateGroupRowValuePolicies(output, path+".groupRows.rowValues"); err != nil {
				return err
			}
		}
		if output.Identity != nil {
			if err := validateRecipeName(output.Identity.Name, path+".identity.name"); err != nil {
				return err
			}
			if output.Identity.Expansion != nil {
				if expressionPresent(output.Identity.Expr) {
					return validationError("invalid_identity", path+".identity", "expr and expansion are mutually exclusive")
				}
				if output.Expand == nil {
					return validationError("invalid_identity", path+".identity.expansion", "expansion identity requires an output expansion")
				}
			} else if err := validateExpressionBudget(output.Identity.Expr, path+".identity.expr", &budget); err != nil {
				return err
			}
		}
		if err := validateDynamicColumns(output.DynamicColumns, path+".dynamicColumns", &budget); err != nil {
			return err
		}
		if err := validateExtensionColumns(output.ExtensionColumns, path+".extensionColumns", &budget); err != nil {
			return err
		}
		for index, projection := range output.CatalogProjections {
			if err := projection.validateAt(fmt.Sprintf("%s.catalogProjections[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	_, err := b.outputDependencyOrder(allowedWorkspaceOutputs)
	return err
}

func (b Bundle) OutputDependencyOrder() ([]int, error) {
	return b.outputDependencyOrder(nil)
}

func (b Bundle) outputDependencyOrder(allowedExternal map[string]struct{}) ([]int, error) {
	indexes := make(map[string]int, len(b.Outputs))
	for index, output := range b.Outputs {
		if prior, duplicate := indexes[output.Name]; duplicate {
			return nil, validationError("duplicate_name", fmt.Sprintf("$.outputs[%d].name", index), fmt.Sprintf("duplicate output name also used at $.outputs[%d].name", prior))
		}
		indexes[output.Name] = index
	}
	state := make([]uint8, len(b.Outputs))
	order := make([]int, 0, len(b.Outputs))
	var visit func(int) error
	visit = func(index int) error {
		switch state[index] {
		case 1:
			return validationError("workspace_output_cycle", fmt.Sprintf("$.outputs[%d]", index), fmt.Sprintf("output %q participates in a workspace output dependency cycle", b.Outputs[index].Name))
		case 2:
			return nil
		}
		state[index] = 1
		output := b.Outputs[index]
		if output.Construction != nil {
			if step, ok := output.Construction.TerminalCombineStep(); ok {
				for inputIndex, input := range step.Inputs {
					if input.Kind != ConstructionWorkspaceOutputInput {
						continue
					}
					path := fmt.Sprintf("$.outputs[%d].construction.steps[0].inputs[%d]", index, inputIndex)
					dependency, found := indexes[input.OutputID]
					if !found {
						if _, allowed := allowedExternal[input.OutputID]; allowed {
							continue
						}
						return validationError("workspace_output_not_found", path, fmt.Sprintf("outputId %q does not name an output in this bundle", input.OutputID))
					}
					if dependency == index {
						return validationError("workspace_output_self_reference", path, fmt.Sprintf("output %q cannot consume itself", output.Name))
					}
					if err := visit(dependency); err != nil {
						return err
					}
				}
			}
		}
		state[index] = 2
		order = append(order, index)
		return nil
	}
	for index := range b.Outputs {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return order, nil
}
