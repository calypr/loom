package publication

import "fmt"

func WithPublicationMetadata(columns []LogicalColumn) ([]LogicalColumn, error) {
	result := make([]LogicalColumn, 0, len(columns)+2)
	result = append(result,
		LogicalColumn{Name: "auth_resource_path", Kind: "string", Nullable: true, Provenance: ColumnExplicit, LoomOwned: true},
		LogicalColumn{Name: "project_id", Kind: "string", Provenance: ColumnExplicit, LoomOwned: true},
	)
	for _, column := range columns {
		if column.Name == "auth_resource_path" {
			return nil, fmt.Errorf("column %q is reserved by Loom", column.Name)
		}
		if column.Name != "project_id" {
			result = append(result, column)
		}
	}
	return result, nil
}
