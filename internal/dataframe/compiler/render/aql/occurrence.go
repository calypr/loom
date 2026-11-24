package aql

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"strings"
)

func stageOutputColumnID(columns []ir.PhysicalStageColumn, name string) string {
	for _, column := range columns {
		if column.Name == name {
			return column.ID
		}
	}
	return ""
}

func indentLines(lines []string, prefix string) []string {
	indented := make([]string, len(lines))
	for index, line := range lines {
		indented[index] = prefix + strings.TrimLeft(line, " \t")
	}
	return indented
}
