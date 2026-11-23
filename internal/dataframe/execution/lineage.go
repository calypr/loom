package execution

import (
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	rowlineage "github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
)

var ErrRowLineageRowNotFound = errors.New("preview row was not found in the receipt output")

type RowLineageRequest struct {
	Output string
	RowID  string
	Offset int
	Limit  int
}

type RowLineageContributor struct {
	ResourceType  string `json:"resourceType"`
	ResourceID    string `json:"resourceId"`
	OccurrenceKey string `json:"occurrenceKey"`
}

type RowLineageResult struct {
	Contributors []RowLineageContributor
	HasMore      bool
	NextOffset   int
}

func rowLineageString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func compileRowLineageOutput(resolved Resolved, request RowLineageRequest) (rowlineage.CompiledRowLineageQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == request.Output {
			return rowlineage.CompileRowLineageOutput(output, request.RowID, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		}
	}
	return rowlineage.CompiledRowLineageQuery{}, fmt.Errorf("row lineage output %q was not found", request.Output)
}
