package materialization

import "strings"

type ExportFormat string

const (
	ExportCSV   ExportFormat = "CSV"
	ExportTSV   ExportFormat = "TSV"
	ExportJSON  ExportFormat = "JSON"
	ExportJSONL ExportFormat = "JSONL"
)

type ExportRequest struct {
	DataType string
	Columns  []string
	Filters  []Filter
	Sort     *Sort
	Format   ExportFormat
	Filename string
}

func (f ExportFormat) Normalize() ExportFormat {
	return ExportFormat(strings.ToUpper(strings.TrimSpace(string(f))))
}
