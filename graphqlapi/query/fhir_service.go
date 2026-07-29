package queryapi

// This file contains the transport-neutral FHIR document query facade.  It
// deliberately delegates planning, authorization, generation resolution, and
// execution to the same dataframe recipe/compiler/runtime path as the rest of
// GraphQL; no AQL or Arango client is constructed here.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

const (
	FHIRDefaultLimit = 25
	FHIRMinLimit     = 1
	FHIRMaxReadLimit = 10000
)

// FHIRListRequest is the canonical request used by generated GraphQL root
// adapters.  Filters are the existing FhirFilterInput values; there is no
// second filter language for this API.
type FHIRListRequest struct {
	Project      string
	ResourceType string
	Filters      []*model.FhirFilterInput
	Limit        int
}

// FHIRListResult contains full resource envelopes.  The runtime row shape is
// intentionally hidden from GraphQL callers; each map is decoded by the
// generated resource registry in the resolver layer.
type FHIRListResult struct {
	Resources   []map[string]any
	Diagnostics any
}

// ListFHIR executes one whole-document FHIR read through the production
// semantic and physical compiler.  The Document expression is lowered by the
// compiler to an envelope containing payload, id, resourceType, and _key.
func (s *Service) ListFHIR(ctx context.Context, request FHIRListRequest) (*FHIRListResult, error) {
	resourceType := strings.TrimSpace(request.ResourceType)
	if !fhirschema.HasResource(resourceType) {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "invalid resource type")
	}
	limit := request.Limit
	if limit == 0 {
		limit = FHIRDefaultLimit
	}
	if limit < FHIRMinLimit {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "limit must be positive")
	}
	if strings.TrimSpace(request.Project) == "" {
		return nil, fmt.Errorf("project is required")
	}

	// Reuse the existing GraphQL input preparation.  Besides resolving active
	// READY generation and authorization scope, this validates fieldRef
	// provenance and preserves all existing filter/operator semantics.
	in := model.FhirDataframeInput{
		Project:          request.Project,
		RootResourceType: resourceType,
		RootFilters:      request.Filters,
	}
	prepared, scope, generation, err := s.prepareRunInput(ctx, in)
	if err != nil {
		return nil, err
	}
	if limit > FHIRMaxReadLimit && s.scopeResolver != nil {
		principal, _ := authscope.PrincipalFromContext(ctx)
		if _, err := s.scopeResolver.ResolveWriteScopeForGeneration(ctx, principal, request.Project, generation, nil); err != nil {
			return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "limit must not exceed 10000 without project write access")
		}
	}
	bundle, err := RecipeBundleFromInput(prepared)
	if err != nil {
		return nil, err
	}
	if len(bundle.Outputs) != 1 {
		return nil, fmt.Errorf("FHIR query produced %d outputs, want one", len(bundle.Outputs))
	}
	// The whole-document expression is backend-neutral.  Compiler support is
	// additive to the existing selector/literal/call expression nodes.
	bundle.Outputs[0].Fields = []recipe.Field{{
		Name: "resource",
		Expr: recipe.Expression{Document: &recipe.DocumentRef{Context: "root"}},
	}}

	bindings := recipe.RuntimeBindings{
		Project: request.Project, DatasetGeneration: generation,
		AuthResourcePaths: cloneStrings(scope.AuthResourcePaths),
		AuthScopeMode:     scope.Mode, PreviewLimit: limit,
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return nil, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "", generation)
	if err != nil {
		return nil, err
	}
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, limit, compiler.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return nil, fmt.Errorf("compile FHIR query: %w", err)
	}
	if len(queries) != 1 {
		return nil, fmt.Errorf("FHIR query produced %d physical queries, want one", len(queries))
	}
	result, err := s.dataframes.RunCompiled(ctx, queries[0])
	if err != nil {
		return nil, err
	}
	resources := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		value, ok := row["resource"]
		if !ok || value == nil {
			continue
		}
		envelope, ok := value.(map[string]any)
		if !ok {
			// Some test/runtime adapters return JSON-shaped values.  Normalize
			// those through JSON without exposing backend implementation details.
			data, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return nil, fmt.Errorf("resource decode failed: %w", marshalErr)
			}
			if unmarshalErr := json.Unmarshal(data, &envelope); unmarshalErr != nil {
				return nil, fmt.Errorf("resource decode failed: %w", unmarshalErr)
			}
		}
		if envelope == nil {
			continue
		}
		// Normalize the storage envelope for generated FHIR models.  The
		// compiler emits payload plus envelope metadata under `key`; overlay
		// id/resourceType and intentionally omit all internal keys.
		if payload, ok := envelope["payload"].(map[string]any); ok {
			overlay := make(map[string]any, len(payload)+2)
			for key, value := range payload {
				overlay[key] = value
			}
			if id, exists := envelope["id"]; exists && id != nil && id != "" {
				overlay["id"] = id
			}
			if typ, exists := envelope["resourceType"]; exists && typ != nil && typ != "" {
				overlay["resourceType"] = typ
			}
			envelope = overlay
		}
		if id, ok := envelope["id"]; !ok || id == nil || id == "" {
			if key, exists := envelope["key"]; exists {
				envelope["id"] = key
			}
		}
		if typ, ok := envelope["resourceType"]; !ok || typ == nil || typ == "" {
			envelope["resourceType"] = resourceType
		}
		delete(envelope, "_key")
		delete(envelope, "key")
		resources = append(resources, envelope)
	}
	return &FHIRListResult{Resources: resources, Diagnostics: result.Diagnostics}, nil
}
