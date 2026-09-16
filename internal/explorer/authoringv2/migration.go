package authoringv2

import (
	"fmt"
	"strings"
)

// MigrateLosslessDefaults upgrades mutable pre-v3 workspaces. Repeated field
// repetition can migrate to INDEXED when the catalog proves it. Related
// resource FIRST remains an explicit, unacknowledged lossy policy. Published
// receipts are immutable and never pass through this migration.
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
			if column.Source.Kind != SourceField || column.Source.Field == nil {
				continue
			}
			// Columns are copied above, but their nested source payloads are
			// pointers. Clone the payload before adding migration metadata so
			// callers retain an immutable pre-migration workspace value.
			column.Source = column.Source.Normalized()
			mode := strings.ToUpper(strings.TrimSpace(column.Source.Field.ProjectionMode))
			if mode == "" {
				mode = "FIRST"
			}
			if column.OccurrenceID != RootOccurrenceID && (mode == "VALUE" || mode == "FIRST" || mode == "INDEXED") {
				if column.Source.Field.RelatedSelection == nil {
					column.Source.Field.RelatedSelection = &RelatedSelection{Kind: "first-by-resource-key"}
				}
				decision := fmt.Sprintf("semantics-v3:related-first-requires-ack:%s:%s", document.Output.ID, column.Column)
				if !contains(workspace.MigrationDecisions, decision) {
					workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
				}
				continue
			}
			if mode != "FIRST" {
				continue
			}
			// Semantics v2 made FIRST explicit. Only pre-v2 persisted drafts
			// may reinterpret a repeated default FIRST as INDEXED.
			if workspace.SemanticsVersion >= 2 {
				continue
			}
			occurrence := findRoute(&document.Route, column.OccurrenceID)
			if occurrence == nil {
				continue
			}
			fieldPath := strings.TrimPrefix(column.Source.Field.Path, "root.")
			for _, candidate := range catalog.Candidates {
				if nodes[candidate.NodeID].ResourceType != occurrence.ResourceType || strings.TrimPrefix(candidate.FieldPath, "root.") != fieldPath || len(candidate.RepeatedBoundaries) == 0 || !contains(candidate.ProjectionModes, "INDEXED") {
					continue
				}
				column.Source.Field.ProjectionMode = "INDEXED"
				workspace.MigrationDecisions = append(workspace.MigrationDecisions, fmt.Sprintf("semantics-v2:repeated-first-to-indexed:%s:%s", document.Output.ID, column.Column))
				break
			}
		}
	}
	workspace.SemanticsVersion = CurrentSemanticsVersion
	return workspace
}
