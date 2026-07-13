package dataframe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// ValidateRequest describes a non-executing validation/compile request from a
// client. It deliberately uses the same Builder shape accepted by RunRequest
// so validation and execution cannot drift apart.
type ValidateRequest struct {
	Builder Builder
	Limit   int
}

// ValidationWarning is a stable, machine-readable advisory attached to a
// successful validation result. Warnings never make a request invalid.
type ValidationWarning struct {
	Code    string
	Message string
	Path    []string
	Details map[string]any
}

// ValidationResult is the normalized contract consumed by preview/export
// frontends. No database rows are read by Validate.
type ValidationResult struct {
	Valid              bool
	Builder            Builder
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

// Validate prepares, validates, and compiles a dataframe request without
// executing it against Arango. The exact normalized builder is returned so a
// caller can persist it as a reusable recipe or pass it to Run unchanged.
func (s *Service) Validate(ctx context.Context, req ValidateRequest) (ValidationResult, error) {
	started := now()
	builder, compiled, diagnostics, err := s.prepareAndCompile(ctx, req.Builder, req.Limit)
	if err != nil {
		return ValidationResult{}, err
	}
	fingerprint, err := requestFingerprint(builder, compiled.Limit)
	if err != nil {
		return ValidationResult{}, err
	}
	diagnostics.Total = now().Sub(started)
	return ValidationResult{
		Valid:              true,
		Builder:            builder,
		Project:            compiled.Project,
		DatasetGeneration:  compiled.DatasetGeneration,
		RootResourceType:   compiled.RootResourceType,
		Limit:              compiled.Limit,
		Columns:            cloneStrings(compiled.Columns),
		PivotFields:        cloneStrings(compiled.PivotFields),
		RowIdentity:        cloneRowIdentity(compiled.RowIdentity),
		RequestFingerprint: fingerprint,
		Warnings:           validationWarnings(builder, compiled),
		Plan:               compiled.PlanDiagnostics,
		PreviewAllowed:     true,
		ExportAllowed:      true,
		Diagnostics:        diagnostics,
	}, nil
}

var now = time.Now

func requestFingerprint(builder Builder, limit int) (string, error) {
	payload := struct {
		Builder Builder
		Limit   int
	}{Builder: builder, Limit: limit}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func validationWarnings(builder Builder, compiled CompiledQuery) []ValidationWarning {
	warnings := make([]ValidationWarning, 0, 2)
	if len(builder.Fields) == 0 && len(builder.Pivots) == 0 && len(builder.Aggregates) == 0 && len(builder.Slices) == 0 && len(builder.Traversals) == 0 {
		warnings = append(warnings, ValidationWarning{
			Code:    "NO_SELECTED_COLUMNS",
			Message: "No explicit fields, pivots, aggregates, slices, or traversals were selected; only the row identity will be returned.",
		})
	}
	if compiled.Limit > 1000 {
		warnings = append(warnings, ValidationWarning{
			Code:    "PREVIEW_LIMIT_CAPPED",
			Message: "Preview limits above 1000 rows may be capped by the frontend.",
			Details: map[string]any{"limit": compiled.Limit, "recommended_max": 1000},
		})
	}
	return warnings
}

func cloneValidationWarnings(in []ValidationWarning) []ValidationWarning {
	if len(in) == 0 {
		return nil
	}
	out := make([]ValidationWarning, len(in))
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
