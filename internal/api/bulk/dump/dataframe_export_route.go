package dump

import (
	"encoding/json"
	"strings"

	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
	"github.com/gofiber/fiber/v3"
)

func (s *Handler) exportDataframe(c fiber.Ctx) error {
	var request dfmaterialization.ExportRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_export_request", Message: "request body must be valid JSON"}
	}
	request.DataType = strings.TrimSpace(request.DataType)
	if request.DataType == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_data_type", Message: "dataType is required"}
	}
	if request.Format.Normalize() == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_export_format", Message: "format is required"}
	}
	filename := sanitizeExportFilename(request.Filename, request.Format.Normalize())
	c.Set(fiber.HeaderContentType, exportContentType(request.Format.Normalize()))
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+filename+`"`)
	if err := s.dataframeExporter.ExportDataframe(c.Context(), request, c); err != nil {
		resetExportResponse(c)
		return &apiError{Status: fiber.StatusBadRequest, Code: "EXPORT_FAILED", Message: "export failed", Cause: err}
	}
	return nil
}

func resetExportResponse(c fiber.Ctx) {
	// Fiber buffers response writes until the handler returns. Clear any
	// partial export before the central error handler emits JSON.
	c.Response().ResetBody()
	c.Response().Header.Del(fiber.HeaderContentType)
	c.Response().Header.Del(fiber.HeaderContentDisposition)
}

func exportContentType(format dfmaterialization.ExportFormat) string {
	switch format.Normalize() {
	case dfmaterialization.ExportCSV:
		return "text/csv; charset=utf-8"
	case dfmaterialization.ExportTSV:
		return "text/tab-separated-values; charset=utf-8"
	case dfmaterialization.ExportJSON:
		return "application/json; charset=utf-8"
	default:
		return "application/x-ndjson; charset=utf-8"
	}
}

func sanitizeExportFilename(value string, format dfmaterialization.ExportFormat) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_", "\"", "", "\r", "", "\n", "").Replace(value)
	if value == "" {
		ext := "jsonl"
		switch format.Normalize() {
		case dfmaterialization.ExportCSV:
			ext = "csv"
		case dfmaterialization.ExportTSV:
			ext = "tsv"
		case dfmaterialization.ExportJSON:
			ext = "json"
		}
		return "loom-export." + ext
	}
	return value
}
