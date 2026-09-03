package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

// recipeExecution returns the legacy execution document consumed by ETL. A
// denied execution remains indistinguishable from a missing catalog entry.
func (r *HTTPRoutes) recipeExecution(ctx context.Context, id string) (map[string]any, int) {
	if r.releases == nil {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	id = strings.TrimSpace(id)
	execution, err := r.releases.GetExecution(ctx, id)
	if err != nil || execution.ID == "" {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	if r.scopes != nil {
		principal, _ := authscope.PrincipalFromContext(ctx)
		if _, scopeErr := r.scopes.ResolveReadScopeForGeneration(ctx, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths); scopeErr != nil {
			status := http.StatusForbidden
			if errors.Is(scopeErr, authscope.ErrUnauthenticated) {
				status = http.StatusUnauthorized
			}
			return map[string]any{"error": "recipe execution not found"}, status
		}
	}
	outputs := make([]map[string]any, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		columns := make([]map[string]any, 0, len(output.Columns))
		for _, column := range output.Columns {
			logical, nullable, repeated, filterable, sortable, aggregatable := columnCapabilities(column.ClickHouse)
			if column.LogicalType != "" {
				logical = column.LogicalType
			}
			nullable = nullable || column.Nullable
			repeated = repeated || column.Repeated
			columns = append(columns, map[string]any{"name": column.Name, "semanticPath": column.SemanticPath, "clickhouseType": column.ClickHouse, "logicalType": logical, "nullable": nullable, "repeated": repeated, "filterable": filterable, "sortable": sortable, "aggregatable": aggregatable})
		}
		outputs = append(outputs, map[string]any{"name": output.Name, "state": recipeExecutionHTTPState(output.State), "rowCount": output.RowCount, "columns": columns})
	}
	return map[string]any{"id": execution.ID, "projectId": execution.Project, "datasetGeneration": execution.DatasetGeneration, "recipeDigest": execution.RecipeDigest, "schemaDigest": execution.SchemaDigest, "resolvedSchemaDigest": execution.SchemaDigest, "state": recipeExecutionHTTPState(execution.State), "outputs": outputs}, http.StatusOK
}

func recipeExecutionHTTPState(state publication.BundleState) string {
	if state == publication.BundlePublished {
		return string(publication.BundleReady)
	}
	return string(state)
}

func columnCapabilities(clickHouseType string) (logical string, nullable, repeated, filterable, sortable, aggregatable bool) {
	typ := clickHouseType
	if strings.HasPrefix(typ, "Nullable(") && strings.HasSuffix(typ, ")") {
		nullable = true
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Nullable("), ")")
	}
	if strings.HasPrefix(typ, "Array(") && strings.HasSuffix(typ, ")") {
		repeated = true
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Array("), ")")
	}
	switch {
	case strings.HasPrefix(typ, "Bool"):
		logical = "boolean"
	case strings.HasPrefix(typ, "Int"), strings.HasPrefix(typ, "UInt"):
		logical = "integer"
	case strings.HasPrefix(typ, "Float"), strings.HasPrefix(typ, "Decimal"):
		logical = "number"
	case strings.HasPrefix(typ, "Date"):
		logical = "date"
	case strings.HasPrefix(typ, "String"), strings.HasPrefix(typ, "FixedString"), strings.HasPrefix(typ, "UUID"):
		logical = "string"
	default:
		logical = "json"
	}
	filterable = true
	sortable = !repeated
	aggregatable = !repeated && logical != "json"
	return
}
