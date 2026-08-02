package runtime

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type ValidateRequest struct {
	Recipe   recipe.Bundle
	Bindings recipe.RuntimeBindings
	Limit    int
}

type ValidationWarning struct {
	Code    string
	Message string
	Path    []string
	Details map[string]any
}

type ValidationResult struct {
	Valid              bool
	Recipe             recipe.Bundle
	Bindings           recipe.RuntimeBindings
	Project            string
	DatasetGeneration  string
	RootResourceType   string
	Limit              int
	Columns            []string
	PivotFields        []string
	RowIdentity        *RowIdentity
	RequestFingerprint string
	Warnings           []ValidationWarning
	Plan               CompilerPlanDiagnostics
	PreviewAllowed     bool
	ExportAllowed      bool
	Diagnostics        QueryDiagnostics
}

func (s *Service) Validate(ctx context.Context, req ValidateRequest) (ValidationResult, error) {
	started := now()
	compiled, diagnostics, err := s.prepareAndCompile(ctx, RunRequest{Recipe: req.Recipe, Bindings: req.Bindings, Limit: req.Limit})
	if err != nil {
		return ValidationResult{}, err
	}
	limit := compiled.Limit
	warnings := make([]ValidationWarning, 0, 1)
	if len(req.Recipe.Outputs) == 1 {
		output := req.Recipe.Outputs[0]
		if len(output.Fields) == 0 && len(output.Pivots) == 0 && len(output.Aggregates) == 0 && len(output.Slices) == 0 && len(output.Traversals) == 0 {
			warnings = append(warnings, ValidationWarning{Code: "NO_SELECTED_COLUMNS", Message: "No explicit fields, pivots, aggregates, slices, or traversals were selected; only the row identity will be returned."})
		}
	}
	if limit > 1000 {
		warnings = append(warnings, ValidationWarning{Code: "PREVIEW_LIMIT_CAPPED", Message: "Preview limits above 1000 rows may be capped by the frontend.", Details: map[string]any{"limit": limit, "recommended_max": 1000}})
	}
	diagnostics.Total = now().Sub(started)
	return ValidationResult{
		Valid: true, Recipe: req.Recipe, Bindings: req.Bindings,
		Project: compiled.Project, DatasetGeneration: compiled.DatasetGeneration,
		RootResourceType: compiled.RootResourceType, Limit: limit,
		Columns: cloneStrings(compiled.Columns), PivotFields: cloneStrings(compiled.PivotFields),
		RowIdentity:        compiled.RowIdentity.Clone(),
		RequestFingerprint: compiled.PlanDiagnostics.Fingerprint,
		Warnings:           warnings, Plan: compiled.PlanDiagnostics,
		PreviewAllowed: true, ExportAllowed: true, Diagnostics: diagnostics,
	}, nil
}

var now = time.Now
