package lower

// This file contains the canonical recipe lowering boundary. Persisted
// recipes are a frontend: after resolution each output is lowered to the same
// ir.PhysicalPlan used by the GraphQL dataframe compiler.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
)

// CompiledRecipe is orchestration metadata around one canonical physical plan
// per output.  It deliberately contains no recipe-specific traversal,
// projection, or renderer structures.  Output order is the persisted recipe
// order and therefore part of the stable materialization contract.
type CompiledRecipe struct {
	Version              int
	RecipeDigest         string
	TranslationVersion   string
	ResolvedSchemaDigest string
	ScopeDigest          string
	SourceGeneration     string
	Outputs              []CompiledRecipeOutput
}

// CompiledRecipeOutput is the canonical compiler result for one output.
// Columns is metadata for the stream/materialization layer; all query
// semantics live in Plan.
type CompiledRecipeOutput struct {
	Name               string
	TranslationVersion string
	RootResourceType   string
	RowGrain           spec.RowGrain
	RootColumnNaming   recipe.RootColumnNaming
	Columns            []string
	// OutputSchema is the compiler-owned ordered projection schema. It is
	// captured from the finalized physical RETURN projections rather than
	// reconstructed by a transport adapter from the semantic recipe tree.
	// Internal projections remain present here so execution and diagnostics can
	// distinguish them from the public dataframe contract.
	OutputSchema []CompiledOutputColumn
	// RowIdentity describes the stable semantic identity used by publication
	// targets. It is metadata only; the physical plan remains authoritative for
	// the returned values.
	RowIdentity *spec.RowIdentity
	// DynamicColumns is post-query validation metadata. The physical plan
	// contains the bounded projections; observed-key/type checks remain above
	// the backend execution boundary.
	DynamicColumns []DynamicColumnMetadata
	// Stages exposes the exact typed schemas and operation capabilities used
	// to compile an ordered construction sequence.
	Stages []CompiledStageDescriptor
	Plan   ir.PhysicalPlan
	// OptimizedPlan is populated by the request orchestrator after all outputs
	// have been lowered. Keeping it separate from Plan lets preview windows be
	// rendered repeatedly without re-running the optimizer or lowering stage.
	OptimizedPlan *ir.PhysicalPlan
}

// CompiledOutputColumn describes one finalized physical projection. Kind and
// Cardinality are logical, backend-neutral values; Internal projections are
// never exposed by dataframe transports.
type CompiledOutputColumn struct {
	ID    string
	Name  string
	Label string
	// SemanticPath is the stable FHIR/provenance identity for this column.
	// Physical names are deliberately excluded so storage renames do not
	// invalidate Explorer configuration.
	SemanticPath string
	Kind         string
	Cardinality  string
	Nullable     bool
	// NormalizedUnit is the compiler-resolved identity of the value in this
	// column. It does not describe source conversion rules or selectors.
	NormalizedUnit *unit.UnitIdentity
	Internal       bool
	Identity       bool
	Discovered     bool
	SourceChild    *lineage.SourceChild
	// RelatedRecordAnchor marks the hidden exact terminal document identity
	// emitted by RELATED_EXPAND. It is copied only by row-preserving stages.
	RelatedRecordAnchor *CompiledRelatedRecordAnchor
}

type CompiledRelatedRecordAnchor struct {
	TargetNodeID       string
	TargetResourceType string
}

// CloneCompiledOutputSchema copies output metadata, including optional unit identities.
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

// CompileResolvedRecipePlan lowers every resolved recipe output into the
// canonical physical IR.  The optimizer and renderer are intentionally not
// called here: callers can apply an explicit PhysicalOptimizationPolicy and
// execution window at the same boundary used by generic dataframe requests.
//
// Dynamic maps are lowered into bounded named projections below. Their
// observed-key/type checks remain metadata for post-query validation; no
// runtime map-shaped AQL is emitted.
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
			return CompiledRecipe{}, fmt.Errorf("invalid output selection: output name is required")
		}
		if selected[name] {
			return CompiledRecipe{}, fmt.Errorf("invalid output selection: duplicate output %q", name)
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
				return CompiledRecipe{}, fmt.Errorf("invalid output selection: unknown output %q", name)
			}
		}
	}
	for _, output := range semanticPlan.Outputs {
		if len(selected) > 0 && !selected[output.Name] {
			continue
		}
		compiled, err := compileRecipeOutput(output, semanticPlan.Bindings, resolved.ResolvedColumns, policy)
		if err != nil {
			return CompiledRecipe{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		result.Outputs = append(result.Outputs, compiled)
	}
	return result, nil
}

func compileRecipeOutput(output semantic.OutputPlan, bindings recipe.RuntimeBindings, resolvedColumns map[string][]semantic.ResolvedColumn, policy ir.PhysicalOptimizationPolicy) (CompiledRecipeOutput, error) {
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
	if output.GroupRows != nil {
		physical, err = buildGroupRowsPhysicalPlan(output, context)
	} else {
		physical, err = buildGenericPhysicalPlanWithPolicy(output, context, policy, recipeFieldProjectionLowerer(output))
	}
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	if output.GroupRows == nil {
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
	if err := appendRecipeColumnTransformations(&physical, output.ColumnTransformations); err != nil {
		return CompiledRecipeOutput{}, err
	}
	baseOutputSchema, err := recipeOutputSchema(physical, output, dynamicMetadata, nil)
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	var reshapeSchema []CompiledOutputColumn
	var stageDescriptors []CompiledStageDescriptor
	finalStageIdentity := ""
	var derivedTypes map[string]derivedColumnMetadata
	if output.Construction != nil {
		reshapeSchema, stageDescriptors, finalStageIdentity, err = appendRecipeConstructionStages(&physical, output.Name, output.RootResourceType, *output.Construction, baseOutputSchema, policy)
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
	if err := physical.Validate(); err != nil {
		return CompiledRecipeOutput{}, fmt.Errorf("validate canonical physical plan: %w", err)
	}
	var outputSchema []CompiledOutputColumn
	if output.Construction != nil {
		outputSchema = CloneCompiledOutputSchema(reshapeSchema)
		if len(output.Construction.Steps) > 0 {
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
	}
	return CompiledRecipeOutput{
		Name: output.Name, RootResourceType: output.RootResourceType,
		RowGrain: output.RowGrain, RootColumnNaming: output.RootColumnNaming, Columns: physicalOutputColumns(outputSchema), OutputSchema: outputSchema,
		RowIdentity: (&identity).Clone(), DynamicColumns: dynamicMetadata, Stages: stageDescriptors, Plan: physical,
	}, nil
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
