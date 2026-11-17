package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
)

type CompiledRecipe struct {
	Version               int
	RecipeDigest          string
	TranslationVersion    string
	ResolvedSchemaDigest  string
	ScopeDigest           string
	SourceGeneration      string
	Outputs               []CompiledRecipeOutput
	WorkspaceDependencies []CompiledRecipeOutput
}

type CompiledRecipeOutput struct {
	Name                   string
	TranslationVersion     string
	RootResourceType       string
	RowGrain               spec.RowGrain
	RootColumnNaming       recipe.RootColumnNaming
	Columns                []string
	OutputSchema           []CompiledOutputColumn
	WorkspaceOutputSources []WorkspaceOutputSource
	ScopeEvidence          *ir.WholeRelationScopeEvidence
	ScopeEvidenceIdentity  *ir.WholeRelationScopeIdentity
	RowIdentity            *spec.RowIdentity
	DynamicColumns         []DynamicColumnMetadata
	Stages                 []CompiledStageDescriptor
	Plan                   ir.PhysicalPlan
	OptimizedPlan          *ir.PhysicalPlan
}

type WorkspaceOutputSource struct {
	InputIndex int
	OutputID   string
	Schema     []CompiledOutputColumn
}

type CompiledOutputColumn struct {
	ID                          string
	Name                        string
	Label                       string
	SemanticValueKind           string
	SemanticPath                string
	Kind                        string
	Cardinality                 string
	Nullable                    bool
	NormalizedUnit              *unit.UnitIdentity
	Internal                    bool
	Identity                    bool
	Discovered                  bool
	SourceChild                 *lineage.SourceChild
	PresenceCompanionName       string
	RelatedRecordAnchor         *CompiledRelatedRecordAnchor
	RootContributorResourceType string
}

const CompiledSemanticValueCodingIdentityDisplay = "CODING_IDENTITY_DISPLAY"

type CompiledRelatedRecordAnchor struct {
	TargetNodeID       string
	TargetResourceType string
}

func CloneCompiledOutputSchema(columns []CompiledOutputColumn) []CompiledOutputColumn {
	cloned := append([]CompiledOutputColumn(nil), columns...)
	for index := range cloned {
		cloned[index].NormalizedUnit = cloneUnitIdentity(cloned[index].NormalizedUnit)
		cloned[index].SourceChild = lineage.CloneSourceChild(cloned[index].SourceChild)
		if cloned[index].RelatedRecordAnchor != nil {
			anchor := *cloned[index].RelatedRecordAnchor
			cloned[index].RelatedRecordAnchor = &anchor
		}
	}
	return cloned
}

func cloneUnitIdentity(identity *unit.UnitIdentity) *unit.UnitIdentity {
	if identity == nil {
		return nil
	}
	cloned := *identity
	return &cloned
}

type DynamicColumnMetadata struct {
	Name             string
	SemanticPath     string
	DynamicName      string
	SourceKey        string
	ValueType        string
	Many             bool
	AllowUnknownKeys bool
	Discovered       bool
}

func outputSelectionError(message string, details map[string]any) error {
	options := []dataframeerrors.ErrorOption{dataframeerrors.WithRetryable(false)}
	if details != nil {
		options = append(options, dataframeerrors.WithDetails(details))
	}
	return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, message, options...)
}

func CompileResolvedRecipePlan(resolved semantic.ResolvedRecipePlan, policy ir.PhysicalOptimizationPolicy) (CompiledRecipe, error) {
	semanticPlan := resolved.SemanticPlan
	if semanticPlan.Version <= 0 || strings.TrimSpace(semanticPlan.RecipeDigest) == "" {
		return CompiledRecipe{}, fmt.Errorf("resolved recipe plan is missing semantic provenance")
	}
	if strings.TrimSpace(semanticPlan.Bindings.Project) == "" {
		return CompiledRecipe{}, fmt.Errorf("resolved recipe bindings project is required")
	}
	result := CompiledRecipe{
		Version:              1,
		RecipeDigest:         semanticPlan.RecipeDigest,
		TranslationVersion:   semanticPlan.TranslationVersion,
		ResolvedSchemaDigest: resolved.ResolvedSchemaDigest,
		ScopeDigest:          resolved.ScopeDigest,
		SourceGeneration:     resolved.SourceGeneration,
		Outputs:              make([]CompiledRecipeOutput, 0, len(semanticPlan.Outputs)),
	}
	selected := map[string]bool{}
	for _, name := range semanticPlan.Bindings.OutputNames {
		if strings.TrimSpace(name) == "" {
			return CompiledRecipe{}, outputSelectionError("invalid output selection: output name is required", nil)
		}
		if selected[name] {
			return CompiledRecipe{}, outputSelectionError(fmt.Sprintf("invalid output selection: duplicate output %q", name), map[string]any{"output": name})
		}
		selected[name] = true
	}
	if len(selected) > 0 {
		available := make(map[string]struct{}, len(semanticPlan.Outputs))
		for _, output := range semanticPlan.Outputs {
			available[output.Name] = struct{}{}
		}
		for name := range selected {
			if _, ok := available[name]; !ok {
				return CompiledRecipe{}, outputSelectionError(fmt.Sprintf("invalid output selection: unknown output %q", name), map[string]any{"output": name})
			}
		}
	}
	dependencyOrder, err := workspaceOutputDependencyOrder(semanticPlan.Outputs)
	if err != nil {
		return CompiledRecipe{}, err
	}
	needed := map[string]bool{}
	if len(selected) > 0 {
		byName := make(map[string]semantic.OutputPlan, len(semanticPlan.Outputs))
		for _, output := range semanticPlan.Outputs {
			byName[output.Name] = output
		}
		var include func(string) error
		include = func(name string) error {
			if needed[name] {
				return nil
			}
			output, ok := byName[name]
			if !ok {
				return fmt.Errorf("workspace output input %q is unavailable in the resolved recipe bundle", name)
			}
			needed[name] = true
			for _, dependency := range workspaceOutputIDs(output) {
				if err := include(dependency); err != nil {
					return err
				}
			}
			return nil
		}
		for name := range selected {
			if err := include(name); err != nil {
				return CompiledRecipe{}, err
			}
		}
	}
	compiledByName := make(map[string]CompiledRecipeOutput, len(dependencyOrder))
	resolvedSchemas := make(map[string][]CompiledOutputColumn, len(dependencyOrder))
	for _, index := range dependencyOrder {
		output := semanticPlan.Outputs[index]
		if len(selected) > 0 && !needed[output.Name] {
			continue
		}
		compiled, err := compileRecipeOutput(output, semanticPlan.Bindings, resolved.ResolvedColumns, policy, resolvedSchemas)
		if err != nil {
			return CompiledRecipe{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		compiledByName[output.Name] = compiled
		resolvedSchemas[output.Name] = CloneCompiledOutputSchema(compiled.OutputSchema)
	}
	for _, output := range semanticPlan.Outputs {
		if len(selected) > 0 && !selected[output.Name] {
			continue
		}
		compiled, ok := compiledByName[output.Name]
		if !ok {
			return CompiledRecipe{}, fmt.Errorf("output %q was not compiled after workspace dependency resolution", output.Name)
		}
		result.Outputs = append(result.Outputs, compiled)
	}
	if len(selected) > 0 {
		for _, index := range dependencyOrder {
			output := semanticPlan.Outputs[index]
			if !needed[output.Name] || selected[output.Name] {
				continue
			}
			compiled, ok := compiledByName[output.Name]
			if !ok {
				return CompiledRecipe{}, fmt.Errorf("workspace dependency %q was not compiled after dependency resolution", output.Name)
			}
			result.WorkspaceDependencies = append(result.WorkspaceDependencies, compiled)
		}
	}
	return result, nil
}

func compileRecipeOutput(output semantic.OutputPlan, bindings recipe.RuntimeBindings, resolvedColumns map[string][]semantic.ResolvedColumn, policy ir.PhysicalOptimizationPolicy, workspaceOutputSchemas map[string][]CompiledOutputColumn) (CompiledRecipeOutput, error) {
	composedCohort := output.GroupRows != nil && output.Construction != nil && len(output.Construction.Steps) > 0
	identity, ok := spec.DefaultRowIdentity(spec.RowGrain(output.RowGrain))
	if !ok {
		return CompiledRecipeOutput{}, fmt.Errorf("row grain %q has no canonical identity", output.RowGrain)
	}
	if output.ExpansionIdentity {
		if output.RowExpansion == nil {
			return CompiledRecipeOutput{}, fmt.Errorf("expansion identity requires a row expansion")
		}
		identity.Fields = append(identity.Fields, "__loom_expansion_identity")
	}
	context := semantic.ExecutionContext{
		Project:                    bindings.Project,
		DatasetGeneration:          bindings.DatasetGeneration,
		AuthResourcePaths:          append([]string(nil), bindings.AuthResourcePaths...),
		AuthScopeMode:              bindings.AuthScopeMode,
		SelectionProject:           bindings.SelectionProject,
		SelectionMembersCollection: bindings.SelectionMembersCollection,
	}
	if err := validateSemanticOutputNames(output); err != nil {
		return CompiledRecipeOutput{}, err
	}
	var physical ir.PhysicalPlan
	var err error
	if output.GroupRows != nil && !composedCohort {
		physical, err = buildGroupRowsPhysicalPlan(output, context)
	} else {
		physical, err = buildGenericPhysicalPlanWithPolicy(output, context, policy, recipeFieldProjectionLowerer(output))
	}
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	if output.GroupRows == nil || composedCohort {
		if err := appendRecipeIdentity(&physical, output); err != nil {
			return CompiledRecipeOutput{}, err
		}
	}
	if err := appendRecipeExpansionIdentity(&physical, output); err != nil {
		return CompiledRecipeOutput{}, err
	}
	dynamicMetadata, err := appendRecipeDynamicColumns(&physical, output, resolvedColumns)
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	columnTransformations := output.ColumnTransformations
	if output.GroupRows != nil && !composedCohort {
		columnTransformations = groupRowValueColumnTransformations(output)
	}
	if err := appendRecipeColumnTransformations(&physical, columnTransformations); err != nil {
		return CompiledRecipeOutput{}, err
	}
	cohort := (*cohortGroupCompileInput)(nil)
	if composedCohort {
		rows, binds, groupErr := buildCohortGroupRows(output, context)
		if groupErr != nil {
			return CompiledRecipeOutput{}, groupErr
		}
		cohort = &cohortGroupCompileInput{Output: output, Rows: rows, BindVars: binds, AfterStepID: output.GroupRows.AfterStepID}
		for key, value := range binds {
			if _, exists := physical.BindVars[key]; exists {
				return CompiledRecipeOutput{}, fmt.Errorf("cohort group bind %q collides with source plan", key)
			}
			physical.BindVars[key] = value
		}
	}
	sourceOutput := output
	if composedCohort {
		sourceOutput.GroupRows = nil
	}
	baseOutputSchema, err := recipeOutputSchema(physical, sourceOutput, dynamicMetadata, nil)
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	if output.Population != nil && output.Construction != nil {
		baseOutputSchema = markRootContributorIdentity(baseOutputSchema, output.RootResourceType)
	}
	if output.GroupRows != nil && !composedCohort {
		baseOutputSchema = append(baseOutputSchema, CompiledOutputColumn{
			ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
			SemanticPath: "construction_root_contributors:group_rows",
			Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
			RootContributorResourceType: output.RootResourceType,
		})
	}
	var reshapeSchema []CompiledOutputColumn
	var stageDescriptors []CompiledStageDescriptor
	finalStageIdentity := ""
	var derivedTypes map[string]derivedColumnMetadata
	if output.GroupRows != nil && !composedCohort {
		reshapeSchema = CloneCompiledOutputSchema(baseOutputSchema)
		stageDescriptors, finalStageIdentity, err = describeGroupRowsStages(output, reshapeSchema)
		if err != nil {
			return CompiledRecipeOutput{}, err
		}
	} else if output.Construction != nil {
		reshapeSchema, stageDescriptors, finalStageIdentity, err = appendRecipeConstructionStages(&physical, output.Name, output.RootResourceType, output.RowExpansion != nil, *output.Construction, baseOutputSchema, policy, cohort, workspaceOutputSchemas)
		if err != nil {
			return CompiledRecipeOutput{}, err
		}
	} else {
		reshapeSchema, err = appendRecipeTableReshape(&physical, output, baseOutputSchema, &identity)
		if err != nil {
			return CompiledRecipeOutput{}, err
		}
		if output.TableReshape != nil && output.TableReshape.Kind == recipe.TableReshapeUnpivot && len(output.DerivedColumns) > 0 {
			return CompiledRecipeOutput{}, fmt.Errorf("derived columns cannot be combined with unpivot")
		}
		derivedTypes, err = appendRecipeDerivedColumns(&physical, output.DerivedColumns, reshapeSchema)
		if err != nil {
			return CompiledRecipeOutput{}, err
		}
	}
	if err := validatePublicProjectionNames(physical, output.Name); err != nil {
		return CompiledRecipeOutput{}, err
	}
	workspaceOutputSources := compiledWorkspaceOutputSources(output, workspaceOutputSchemas)
	if len(workspaceOutputSources) > 0 {
		err = physical.ValidateForWorkspaceCompilation()
	} else {
		err = physical.Validate()
	}
	if err != nil {
		return CompiledRecipeOutput{}, fmt.Errorf("validate canonical physical plan: %w", err)
	}
	var outputSchema []CompiledOutputColumn
	if output.Construction != nil {
		outputSchema = CloneCompiledOutputSchema(reshapeSchema)
		if composedCohort && len(stageDescriptors) > 1 {
			identity.Fields = []string{finalStageIdentity}
		}
	} else {
		outputSchema, err = recipeOutputSchema(physical, output, dynamicMetadata, derivedTypes)
		if err != nil {
			return CompiledRecipeOutput{}, err
		}
		if output.TableReshape != nil {
			outputSchema = reconcileRecipeTableReshapeSchema(outputSchema, reshapeSchema)
		}
		if output.GroupRows != nil && !composedCohort {
			outputSchema = append(outputSchema, CompiledOutputColumn{
				ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
				SemanticPath: "construction_root_contributors:group_rows",
				Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
				RootContributorResourceType: output.RootResourceType,
			})
		}
	}
	scopeEvidence, scopeEvidenceIdentity, err := issueWholeRelationScopeEvidence(output.Name, physical, outputSchema)
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	return CompiledRecipeOutput{
		Name: output.Name, RootResourceType: output.RootResourceType,
		RowGrain: output.RowGrain, RootColumnNaming: output.RootColumnNaming, Columns: physicalOutputColumns(outputSchema), OutputSchema: outputSchema,
		RowIdentity: (&identity).Clone(), DynamicColumns: dynamicMetadata, Stages: stageDescriptors, Plan: physical,
		WorkspaceOutputSources: workspaceOutputSources, ScopeEvidence: scopeEvidence, ScopeEvidenceIdentity: scopeEvidenceIdentity,
	}, nil
}

func groupRowValueColumnTransformations(output semantic.OutputPlan) []recipe.ColumnTransformation {
	if output.GroupRows == nil || len(output.GroupRows.RowValues) == 0 {
		return output.ColumnTransformations
	}
	fields := make(map[string]string, len(output.Root.Fields))
	for _, field := range output.Root.Fields {
		fields[field.ColumnID] = field.Name
	}
	memberColumns := make(map[string]bool, len(output.GroupRows.RowValues))
	for _, selected := range output.GroupRows.RowValues {
		if name := fields[selected.ColumnID]; name != "" {
			memberColumns[name] = true
		}
	}
	remaining := make([]recipe.ColumnTransformation, 0, len(output.ColumnTransformations))
	for _, transformation := range output.ColumnTransformations {
		if !memberColumns[transformation.Column] {
			remaining = append(remaining, transformation)
		}
	}
	return remaining
}

func describeGroupRowsStages(output semantic.OutputPlan, outputSchema []CompiledOutputColumn) ([]CompiledStageDescriptor, string, error) {
	if output.GroupRows == nil {
		return nil, "", fmt.Errorf("explicit group rows are required")
	}
	fields := make(map[string]semantic.SemanticField, len(output.Root.Fields))
	for _, field := range output.Root.Fields {
		fields[field.ColumnID] = field
	}
	sourceColumns := make([]CompiledOutputColumn, 0, len(output.GroupRows.RowValues)+1)
	for _, selected := range output.GroupRows.RowValues {
		field, ok := fields[selected.ColumnID]
		if !ok {
			return nil, "", fmt.Errorf("group row value column ID %q is missing from the root projection", selected.ColumnID)
		}
		sourceColumns = append(sourceColumns, CompiledOutputColumn{
			ID: field.ColumnID, Name: field.Name, Label: constructionFirstNonEmpty(field.Label, field.Name),
			SemanticPath: recipeSemanticPath(output.RootResourceType, output.Root.ResourceType, field.FieldRef, field.Expr.Expression),
			Kind:         string(field.Expr.Type.Kind), Cardinality: string(field.Expr.Type.Cardinality), Nullable: field.Expr.Type.Cardinality.Optional(),
		})
	}
	sourceColumns = append(sourceColumns, CompiledOutputColumn{
		ID: "_key", Name: "_key", Label: "_key", SemanticPath: "source:member_identity",
		Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true,
	})
	const rowIdentity = "__loom_row_id"
	source := CompiledStageDescriptor{
		ID: recipe.ConstructionSourceProjectionID, Operation: "SOURCE_PROJECTION",
		Columns: sourceColumns, RowIdentityColumn: "_key",
	}
	groupRows := CompiledStageDescriptor{
		ID: "group_rows", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP_ROWS",
		Columns: CloneCompiledOutputSchema(outputSchema), RowIdentityColumn: rowIdentity,
		Capabilities: withConstructionCapability(stageCapabilities(outputSchema, hasRootContributorIdentity(outputSchema, output.RootResourceType)), StageOperationCapability{Operation: recipe.ConstructionOperationKind("ROW_VALUES"), Supported: true}),
	}
	return []CompiledStageDescriptor{source, groupRows}, rowIdentity, nil
}

func validateSemanticOutputNames(output semantic.OutputPlan) error {
	seen := make(map[string]string)
	var walk func(semantic.SemanticNode, string) error
	walk = func(node semantic.SemanticNode, prefix string) error {
		name := func(local string) string {
			if prefix == "" {
				return local
			}
			return prefix + "__" + local
		}
		check := func(local, kind string) error {
			public := name(local)
			if prior, exists := seen[public]; exists {
				return fmt.Errorf("output %q has colliding public column name %q between %s and %s", output.Name, public, prior, kind)
			}
			seen[public] = kind
			return nil
		}
		for _, field := range node.Fields {
			if err := check(field.Name, "field"); err != nil {
				return err
			}
		}
		for _, aggregate := range node.Aggregates {
			local := aggregate.Name
			if aggregate.OutputName != "" {
				local = aggregate.OutputName
			}
			if err := check(local, "aggregate"); err != nil {
				return err
			}
		}
		for _, slice := range node.Slices {
			if err := check(slice.Name, "slice"); err != nil {
				return err
			}
		}
		for _, pivot := range node.Pivots {
			for _, column := range pivot.Columns {
				if err := check(pivot.Name+"__"+sanitizeColumnName(column), "pivot"); err != nil {
					return err
				}
			}
		}
		for _, child := range node.Children {
			childPrefix := traversalColumnPrefix(output.TraversalColumnNaming, prefix, child.Alias)
			if err := walk(child, childPrefix); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(output.Root, "")
}

func validatePublicProjectionNames(physical ir.PhysicalPlan, outputName string) error {
	seen := make(map[string]struct{})
	for _, operation := range physical.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "" || projection.Hidden {
				continue
			}
			if _, exists := seen[projection.Name]; exists {
				return fmt.Errorf("output %q has colliding public column name %q", outputName, projection.Name)
			}
			seen[projection.Name] = struct{}{}
		}
	}
	return nil
}
