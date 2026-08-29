package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/gofiber/fiber/v3"
)

// RecipeExecutionHandler exposes candidate schema metadata without physical
// table names. Generated OpenAPI routing calls Handle directly.
type RecipeExecutionHandler struct {
	Catalog publication.BundleCatalog
	Scopes  *authscope.ScopeResolver
}

func (h RecipeExecutionHandler) Handle(c fiber.Ctx) error {
	body, status := h.Execute(c.Context(), c.Params("id"))
	return c.Status(status).JSON(body)
}

// Execute returns the legacy recipe execution document for the generated
// OpenAPI adapter. It deliberately keeps the historical not-found response
// indistinguishable for missing catalog entries and denied scopes.
func (h RecipeExecutionHandler) Execute(ctx context.Context, id string) (map[string]any, int) {
	if h.Catalog == nil {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	id = strings.TrimSpace(id)
	execution, err := h.Catalog.GetExecution(ctx, id)
	if err != nil || execution.ID == "" {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	if h.Scopes != nil {
		principal, _ := authscope.PrincipalFromContext(ctx)
		if _, scopeErr := h.Scopes.ResolveReadScopeForGeneration(ctx, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths); scopeErr != nil {
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
			if column.Nullable {
				nullable = true
			}
			if column.Repeated {
				repeated = true
			}
			columns = append(columns, map[string]any{"name": column.Name, "semanticPath": column.SemanticPath, "clickhouseType": column.ClickHouse, "logicalType": logical, "nullable": nullable, "repeated": repeated, "filterable": filterable, "sortable": sortable, "aggregatable": aggregatable})
		}
		outputs = append(outputs, map[string]any{"name": output.Name, "state": recipeExecutionHTTPState(output.State), "rowCount": output.RowCount, "columns": columns})
	}
	return map[string]any{"id": execution.ID, "projectId": execution.Project, "datasetGeneration": execution.DatasetGeneration, "recipeDigest": execution.RecipeDigest, "schemaDigest": execution.SchemaDigest, "resolvedSchemaDigest": execution.SchemaDigest, "state": recipeExecutionHTTPState(execution.State), "outputs": outputs}, http.StatusOK
}

// recipeExecutionHTTPState preserves the READY spelling used by the legacy
// Explorer integration. Internally, a successfully committed bundle is
// PUBLISHED; the HTTP execution contract historically exposed that same
// successful state as READY and Gecko still validates that spelling.
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
	case strings.HasPrefix(typ, "Int") || strings.HasPrefix(typ, "UInt"):
		logical = "integer"
	case strings.HasPrefix(typ, "Float") || strings.HasPrefix(typ, "Decimal"):
		logical = "number"
	case strings.HasPrefix(typ, "Date"):
		logical = "date"
	case strings.HasPrefix(typ, "String") || strings.HasPrefix(typ, "FixedString") || strings.HasPrefix(typ, "UUID"):
		logical = "string"
	default:
		logical = "json"
	}
	filterable = true
	sortable = !repeated
	aggregatable = !repeated && logical != "json"
	return
}
