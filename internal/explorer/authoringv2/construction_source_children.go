package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/lineage"
)

func constructionSourceColumnsWithChildren(columns []Column, referencedIDs []string, declared []StageColumn) ([]StageColumn, error) {
	byID := make(map[string]Column, len(columns))
	nonIndexedColumns := make([]Column, 0, len(columns))
	seenColumnIDs, seenNames := make(map[string]bool, len(columns)), make(map[string]bool, len(columns))
	for _, column := range columns {
		if !requiredID(column.ColumnID) {
			return nil, fmt.Errorf("source column %q requires columnId in a staged construction", column.Column)
		}
		if seenColumnIDs[column.ColumnID] || seenNames[column.Column] {
			return nil, fmt.Errorf("source projection contains duplicate columnId or name")
		}
		seenColumnIDs[column.ColumnID], seenNames[column.Column] = true, true
		byID[column.ColumnID] = column
		if !authoringIndexedSource(column) {
			nonIndexedColumns = append(nonIndexedColumns, column)
		}
	}
	source, err := sourceStageColumns(nonIndexedColumns)
	if err != nil {
		return nil, err
	}
	declaredByID := make(map[string]StageColumn, len(declared))
	for _, column := range declared {
		declaredByID[column.ID] = column
	}
	childIDs := make([]string, 0, len(referencedIDs)+len(declared))
	childIDSeen := make(map[string]bool, len(referencedIDs)+len(declared))
	addChildID := func(id string) {
		if !childIDSeen[id] {
			childIDSeen[id] = true
			childIDs = append(childIDs, id)
		}
	}
	for _, id := range referencedIDs {
		addChildID(id)
	}
	for _, column := range declared {
		if _, directSource := byID[column.ID]; !directSource {
			if _, ok := lineage.ParseSourceChildID(column.ID); ok {
				addChildID(column.ID)
			}
		}
	}
	childrenByID := make(map[string]StageColumn, len(childIDs))
	for _, id := range childIDs {
		if _, directSource := byID[id]; directSource {
			continue
		}
		child, ok := lineage.ParseSourceChildID(id)
		if !ok {
			continue
		}
		if len(child.ParentColumnIDs) != 1 {
			return nil, fmt.Errorf("source child %q must resolve to exactly one stable parent", id)
		}
		parent, exists := byID[child.ParentColumnIDs[0]]
		if !exists || parent.Source.Kind != SourceField || parent.Source.Field == nil || !strings.EqualFold(strings.TrimSpace(parent.Source.ProjectionMode()), "INDEXED") {
			return nil, fmt.Errorf("source child %q references a missing or non-INDEXED parent column", id)
		}
		if child.OccurrenceID != parent.OccurrenceID || child.SourcePath != parent.Source.Field.Path {
			return nil, fmt.Errorf("source child %q no longer matches parent source identity", id)
		}
		boundaries := authoringRepeatedSourceBoundaries(parent.Source.Field.Path)
		if child.Kind == lineage.IndexedValueChild {
			if child.BoundaryPath != "" || len(child.Coordinates) == 0 || len(child.Coordinates) != len(boundaries) {
				return nil, fmt.Errorf("source child %q has invalid indexed coordinate lineage", id)
			}
		} else if child.Kind == lineage.RepeatedCountChild {
			if len(child.Coordinates) >= len(boundaries) || boundaries[len(child.Coordinates)] != child.BoundaryPath {
				return nil, fmt.Errorf("source child %q no longer matches a repeated count boundary", id)
			}
		} else {
			return nil, fmt.Errorf("source child %q has unsupported source lineage", id)
		}
		for index, coordinate := range child.Coordinates {
			if index >= len(boundaries) || coordinate.BoundaryPath != boundaries[index] {
				return nil, fmt.Errorf("source child %q coordinate %d no longer matches parent boundaries", id, index)
			}
		}
		for _, boundary := range child.Coordinates {
			if boundary.Index < 0 {
				return nil, fmt.Errorf("source child %q has a negative coordinate", id)
			}
		}

		placeholder := declaredByID[id]
		if placeholder.ID == "" {
			placeholder = StageColumn{
				ID: id, Name: constructionSourceChildPlaceholderName(id),
				Label: constructionSourceChildLabel(parent.Label, child),
			}
		}
		if child.Kind == lineage.RepeatedCountChild {
			if placeholder.Type == "" || strings.EqualFold(placeholder.Type, "INFER") {
				placeholder.Type = "integer"
			}
			placeholder.Nullable = false
		} else {
			if placeholder.Type == "" {
				placeholder.Type = parent.LogicalType
			}
			if placeholder.Type == "" {
				placeholder.Type = "INFER"
			}
			placeholder.Nullable = true
		}
		if strings.TrimSpace(placeholder.Label) == "" {
			placeholder.Label = constructionSourceChildLabel(parent.Label, child)
		}
		childrenByID[id] = placeholder
	}

	ordered := make([]StageColumn, 0, len(source)+len(childrenByID))
	appended := make(map[string]bool, len(source)+len(childrenByID))
	appendSourceID := func(id string) {
		if appended[id] {
			return
		}
		for _, column := range source {
			if column.ID == id {
				ordered = append(ordered, column)
				appended[id] = true
				return
			}
		}
		if child, exists := childrenByID[id]; exists {
			ordered = append(ordered, child)
			appended[id] = true
		}
	}
	for _, column := range declared {
		appendSourceID(column.ID)
	}
	for _, column := range source {
		appendSourceID(column.ID)
	}
	for _, id := range childIDs {
		appendSourceID(id)
	}
	return ordered, nil
}

func constructionSourceColumnsWithChildrenAndProjections(columns []Column, projections []ConstructionSourceProjection, referencedIDs []string, declared []StageColumn) ([]StageColumn, error) {
	source, err := constructionSourceColumnsWithChildren(columns, referencedIDs, declared)
	if err != nil {
		return nil, err
	}
	byID, err := stageColumnIndex(source)
	if err != nil {
		return nil, err
	}
	seenNames := make(map[string]bool, len(source)+len(projections))
	for _, column := range source {
		seenNames[column.Name] = true
	}
	for _, projection := range projections {
		if _, exists := byID[projection.ColumnID]; exists {
			return nil, fmt.Errorf("source projection columnId %q collides with an existing stage column", projection.ColumnID)
		}
		name := ConstructionSourceProjectionName(projection.ColumnID)
		if seenNames[name] {
			return nil, fmt.Errorf("source projection name for columnId %q collides with an existing stage column", projection.ColumnID)
		}
		seenNames[name] = true
		source = append(source, StageColumn{
			ID: projection.ColumnID, Name: name, Label: projection.Label, Type: projection.LogicalType,
		})
	}
	return source, nil
}

func ConstructionSourceProjectionName(columnID string) string {
	digest := sha256.Sum256([]byte(columnID))
	return "__construction_source_" + hex.EncodeToString(digest[:8])
}

func authoringIndexedSource(column Column) bool {
	return column.Source.Kind == SourceField && column.Source.Field != nil && strings.EqualFold(strings.TrimSpace(column.Source.ProjectionMode()), "INDEXED")
}

func constructionSourceChildPlaceholderName(id string) string {
	digest := sha256.Sum256([]byte(id))
	return "__source_child_" + hex.EncodeToString(digest[:8])
}

func constructionSourceChildLabel(parent string, child lineage.SourceChild) string {
	label := strings.TrimSpace(parent)
	if label == "" {
		label = child.ParentColumnIDs[0]
	}
	for _, coordinate := range child.Coordinates {
		label += fmt.Sprintf(" [%d]", coordinate.Index)
	}
	if child.Kind == lineage.RepeatedCountChild {
		label += " count"
	}
	return label
}

func authoringRepeatedSourceBoundaries(path string) []string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "root.")
	parts := strings.Split(path, ".")
	boundaries := make([]string, 0)
	for index, part := range parts {
		if strings.HasSuffix(part, "[]") {
			boundaries = append(boundaries, strings.Join(parts[:index+1], "."))
		}
	}
	return boundaries
}
