package queryapi

import (
	"context"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe"
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
	RowIdentity        *dataframe.RowIdentity
	RequestFingerprint string
	Warnings           []dataframe.ValidationWarning
	Plan               dataframe.CompilerPlanDiagnostics
	PreviewAllowed     bool
	ExportAllowed      bool
	Diagnostics        dataframe.QueryDiagnostics
}

// Validate resolves public fieldRefs, pins generation/scope, and delegates to
// the same internal preparation/compiler boundary used by Run. It never opens
// an Arango cursor or invokes row execution.
func (s *Service) Validate(ctx context.Context, input model.FhirDataframeInput) (ValidationResult, error) {
	normalized, scope, generation, err := s.prepareRunInput(ctx, input)
	if err != nil {
		return ValidationResult{}, err
	}
	builder := BuilderFromInput(normalized)
	builder.DatasetGeneration = generation
	builder.AuthScopeMode = scope.Mode
	limit := 0
	if normalized.Limit != nil {
		limit = *normalized.Limit
	}
	validated, err := s.dataframes.Validate(ctx, dataframe.ValidateRequest{Builder: builder, Limit: limit})
	if err != nil {
		return ValidationResult{}, err
	}
	validatedWarnings := cloneValidationWarnings(validated.Warnings)
	// The internal service owns the normalized builder. Keep the adapter's
	// public normalized input as the fieldRef-resolved model so a caller can
	// display or persist it without learning compiler selectors.
	return ValidationResult{
		Valid:              validated.Valid,
		NormalizedInput:    normalized,
		Project:            validated.Project,
		DatasetGeneration:  validated.DatasetGeneration,
		RootResourceType:   validated.RootResourceType,
		Limit:              validated.Limit,
		Columns:            cloneStrings(validated.Columns),
		PivotFields:        cloneStrings(validated.PivotFields),
		RowIdentity:        cloneRowIdentity(validated.RowIdentity),
		RequestFingerprint: validated.RequestFingerprint,
		Warnings:           validatedWarnings,
		Plan:               validated.Plan,
		PreviewAllowed:     validated.PreviewAllowed,
		ExportAllowed:      validated.ExportAllowed,
		Diagnostics:        validated.Diagnostics,
	}, nil
}

func cloneValidationWarnings(in []dataframe.ValidationWarning) []dataframe.ValidationWarning {
	if len(in) == 0 {
		return nil
	}
	out := make([]dataframe.ValidationWarning, len(in))
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

func cloneRowIdentity(in *dataframe.RowIdentity) *dataframe.RowIdentity {
	if in == nil {
		return nil
	}
	out := *in
	out.Fields = cloneStrings(in.Fields)
	return &out
}
