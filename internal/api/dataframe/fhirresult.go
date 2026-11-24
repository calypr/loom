package dataframe

import (
	"encoding/json"
)

type FhirDataframeResult struct {
	Columns     []string                   `json:"columns"`
	Rows        json.RawMessage            `json:"rows"`
	RowCount    int                        `json:"rowCount"`
	Diagnostics *DataframeQueryDiagnostics `json:"diagnostics"`
}
