package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// compilePostgreSQLRecipeOutput renders one finalized recipe output through
// the target-bound PostgreSQL renderer and packages it as a CompiledQuery.
// It is the single authority for PostgreSQL-tagged source commands: the
// command, its declared column contract, the public projection, and the
// plan fingerprint all originate here, from the same supported IR the AQL
// target consumes. Unsupported plans fail explicitly with the renderer's
// typed error; there is no AQL fallback, no AQL-to-SQL conversion, and the
// AQL-only preview accelerators (covering indexes, group scans, projection
// factoring) are never attached to a PostgreSQL command.
func compilePostgreSQLRecipeOutput(target Target, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, physical ir.PhysicalPlan, includePreviewSourceID bool) (CompiledQuery, error) {
	tagged, rendered, err := target.renderPostgreSQLFinalPlan(physical)
	if err != nil {
		return CompiledQuery{}, err
	}
	if len(rendered.OutputColumns) == 0 {
		return CompiledQuery{}, fmt.Errorf("postgresql statement declares no result columns")
	}
	outputSchema := lower.CloneCompiledOutputSchema(output.OutputSchema)
	if includePreviewSourceID {
		outputSchema = append(outputSchema, lower.CompiledOutputColumn{
			ID: ir.PreviewSourceResourceIDColumn, Name: ir.PreviewSourceResourceIDColumn,
			Label: ir.PreviewSourceResourceIDColumn, SemanticPath: "preview:source_resource_id",
			Kind: "string", Cardinality: "optional_one", Nullable: true, Internal: true,
		})
	}
	publicColumns := postgreSQLPublicColumns(outputSchema, rendered.OutputColumns)
	if len(outputSchema) > 0 && len(publicColumns) == 0 {
		return CompiledQuery{}, fmt.Errorf("every declared PostgreSQL result column is an internal projection; refusing to publish internal columns in the public row")
	}
	return CompiledQuery{
		PhysicalPlan:       clonePhysicalPlan(physical),
		Project:            bindings.Project,
		DatasetGeneration:  normalizeDatasetGeneration(bindings.DatasetGeneration),
		RootResourceType:   output.RootResourceType,
		TranslationVersion: output.TranslationVersion,
		AuthResourcePaths:  cloneStrings(bindings.AuthResourcePaths),
		PlanMode:           "physical",
		PlanProfile:        "generic_fhir_graph_recipe",
		RowIdentity:        output.RowIdentity.Clone(),
		Command:            tagged,
		Columns:            append([]string(nil), rendered.OutputColumns...),
		OutputSchema:       outputSchema,
		PublicColumns:      append([]string(nil), publicColumns...),
		Limit:              limit,
		PlanDiagnostics:    ir.CompilerPlanDiagnostics{Fingerprint: postgreSQLPlanFingerprint(rendered.SQL, rendered.Args, rendered.OutputColumns)},
	}, nil
}
