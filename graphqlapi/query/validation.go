package queryapi

import (
	"context"
	"time"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	dfspec "github.com/calypr/loom/internal/dataframe/spec"
)

// ValidationResult is the transport-neutral dataframe adapter result used by
// the future GraphQL resolver. It intentionally contains the normalized input
// and compiler metadata, but never rendered AQL or bind variables.
type ValidationResult struct {
	Valid              bool
	NormalizedInput    model.FhirDataframeInput
	Project            string
	DatasetGeneration  string
	RootResourceType   string
	Limit              int
	Columns            []string
	PivotFields        []string
	RowIdentity        *spec.RowIdentity
	RequestFingerprint string
	Warnings           []runtime.ValidationWarning
	Plan               ir.CompilerPlanDiagnostics
	PreviewAllowed     bool
	ExportAllowed      bool
	Diagnostics        runtime.QueryDiagnostics
}

// Validate resolves public fieldRefs, pins generation/scope, and delegates to
// the same internal preparation/compiler boundary used by Run. It never opens
// an Arango cursor or invokes row execution.
func (s *Service) Validate(ctx context.Context, input model.FhirDataframeInput) (ValidationResult, error) {
	started := time.Now()
	normalized, scope, generation, err := s.prepareRunInput(ctx, input)
	if err != nil {
		return ValidationResult{}, err
	}
	limit := 0
	if normalized.Limit != nil {
		limit = *normalized.Limit
	}
	if limit <= 0 {
		limit = 25
	}
	preparationStarted := time.Now()
	bundle, err := RecipeBundleFromInput(normalized)
	if err != nil {
		return ValidationResult{}, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	bindings := recipe.RuntimeBindings{Project: normalized.Project, DatasetGeneration: generation, AuthResourcePaths: cloneStrings(scope.AuthResourcePaths), AuthScopeMode: scope.Mode, PreviewLimit: limit}
	bundle, err = s.resolveRecipeBundle(ctx, bundle, bindings)
	if err != nil {
		return ValidationResult{}, queryInvalidErrorOrBackend(err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return ValidationResult{}, queryInvalidErrorOrBackend(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "", generation)
	if err != nil {
		return ValidationResult{}, queryInvalidErrorOrBackend(err)
	}
	preparationDuration := time.Since(preparationStarted)
	compileStarted := time.Now()
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return ValidationResult{}, queryInvalidErrorOrBackend(err)
	}
	if len(queries) != 1 {
		return ValidationResult{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	compiled := queries[0]
	var identity *spec.RowIdentity
	if value, ok := dfspec.DefaultRowIdentity(dfspec.RowGrain(resolved.SemanticPlan.Outputs[0].RowGrain)); ok {
		identity = &value
	}
	warnings := make([]runtime.ValidationWarning, 0, 1)
	if len(normalized.RootFields) == 0 && len(normalized.RootPivots) == 0 && len(normalized.RootAggregates) == 0 && len(normalized.RootSlices) == 0 && len(normalized.Traverse) == 0 {
		warnings = append(warnings, runtime.ValidationWarning{Code: "NO_SELECTED_COLUMNS", Message: "No explicit fields, pivots, aggregates, slices, or traversals were selected; only the row identity will be returned."})
	}
	compileDuration := time.Since(compileStarted)
	// Keep the adapter's public normalized input as the fieldRef-resolved model
	// so a caller can display or persist it without learning compiler selectors.
	return ValidationResult{
		Valid:              true,
		NormalizedInput:    normalized,
		Project:            compiled.Project,
		DatasetGeneration:  compiled.DatasetGeneration,
		RootResourceType:   compiled.RootResourceType,
		Limit:              compiled.Limit,
		Columns:            cloneStrings(compiled.Columns),
		PivotFields:        cloneStrings(compiled.PivotFields),
		RowIdentity:        cloneRowIdentity(identity),
		RequestFingerprint: resolved.SemanticPlan.RecipeDigest,
		Warnings:           cloneValidationWarnings(warnings),
		Plan:               compiled.PlanDiagnostics,
		PreviewAllowed:     true,
		ExportAllowed:      true,
		Diagnostics:        runtime.QueryDiagnostics{InputResolution: time.Since(started) - preparationDuration - compileDuration, RequestPreparation: preparationDuration, Compilation: compileDuration, Total: time.Since(started), Plan: compiled.PlanDiagnostics},
	}, nil
}

func cloneValidationWarnings(in []runtime.ValidationWarning) []runtime.ValidationWarning {
	if len(in) == 0 {
		return nil
	}
	out := make([]runtime.ValidationWarning, len(in))
	for i, warning := range in {
		out[i] = warning
		out[i].Path = append([]string(nil), warning.Path...)
		if warning.Details != nil {
			out[i].Details = make(map[string]any, len(warning.Details))
			for key, value := range warning.Details {
				out[i].Details[key] = value
			}
		}
	}
	return out
}

func cloneRowIdentity(in *spec.RowIdentity) *spec.RowIdentity {
	if in == nil {
		return nil
	}
	out := *in
	out.Fields = cloneStrings(in.Fields)
	return &out
}
