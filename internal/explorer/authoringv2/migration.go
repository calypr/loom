package authoringv2

import (
	"fmt"
	"strings"
)

// MigrateLosslessDefaults upgrades mutable pre-v2 workspaces whose repeated
// fields were historically reduced to FIRST. Published receipts are immutable
// and never pass through this migration.
func MigrateLosslessDefaults(workspace Workspace, catalog CatalogSnapshot) Workspace {
	if workspace.SemanticsVersion >= CurrentSemanticsVersion {
		return workspace
	}
	workspace.Documents = append([]Document(nil), workspace.Documents...)
	workspace.MigrationDecisions = append([]string(nil), workspace.MigrationDecisions...)
	for index := range workspace.Documents {
		workspace.Documents[index].Columns = append([]Column(nil), workspace.Documents[index].Columns...)
	}
	nodes := make(map[string]CatalogNode, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		nodes[node.ID] = node
	}
	for documentIndex := range workspace.Documents {
		document := &workspace.Documents[documentIndex]
		for columnIndex := range document.Columns {
			column := &document.Columns[columnIndex]
			if column.Source.Kind != SourceField {
				continue
			}
			mode := strings.ToUpper(strings.TrimSpace(column.Source.ProjectionMode))
			if mode != "" && mode != "FIRST" {
				continue
			}
			occurrence := findRoute(&document.Route, column.OccurrenceID)
			if occurrence == nil {
				continue
			}
			fieldPath := strings.TrimPrefix(column.Source.FieldPath, "root.")
			for _, candidate := range catalog.Candidates {
				if nodes[candidate.NodeID].ResourceType != occurrence.ResourceType || strings.TrimPrefix(candidate.FieldPath, "root.") != fieldPath || len(candidate.RepeatedBoundaries) == 0 || !contains(candidate.ProjectionModes, "INDEXED") {
					continue
				}
				column.Source.ProjectionMode = "INDEXED"
				workspace.MigrationDecisions = append(workspace.MigrationDecisions, fmt.Sprintf("semantics-v2:repeated-first-to-indexed:%s:%s", document.Output.ID, column.Column))
				break
			}
		}
	}
	workspace.SemanticsVersion = CurrentSemanticsVersion
	return workspace
}
