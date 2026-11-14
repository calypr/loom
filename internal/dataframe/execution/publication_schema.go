package execution

import (
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// PublicationColumns is the one conversion point from the finalized
// compiler schema to the backend-neutral publication schema. Publication must
// not reconstruct nested names from semantic recipe nodes because those names
// are finalized by physical lowering.
func PublicationColumns(plan Resolved, outputName string) []publication.LogicalColumn {
	for _, output := range plan.Compiled.Outputs {
		if output.Name != outputName {
			continue
		}
		columns := make([]publication.LogicalColumn, 0, len(output.OutputSchema)+1)
		identityAdded := false
		for _, column := range output.OutputSchema {
			if column.Identity && column.Name == "__loom_row_id" {
				kind := column.Kind
				if kind == "" {
					kind = "string"
				}
				// Group row identity is a structured tuple in the compiler. The
				// publication row key is its canonical JSON text so ClickHouse can
				// order and page by the hidden identity column.
				if output.RowGrain == spec.RowGrainGroups && kind == "object" {
					kind = "string"
				}
				columns = append(columns, publication.LogicalColumn{ID: column.ID, Name: column.Name, SemanticPath: "loom:row_id", Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable, IsIdentity: true, LoomOwned: true, Provenance: publication.ColumnExplicit})
				identityAdded = true
				break
			}
		}
		if !identityAdded {
			columns = append(columns, publication.LogicalColumn{Name: "__loom_row_id", SemanticPath: "loom:row_id", Kind: "string", IsIdentity: true, LoomOwned: true, Provenance: publication.ColumnExplicit})
		}
		for _, column := range output.OutputSchema {
			if column.Internal {
				continue
			}
			kind := column.Kind
			if kind == "date_time" {
				kind = "date-time"
			}
			if kind == "" {
				kind = "string"
			}
			semanticPath := column.SemanticPath
			if semanticPath == "" {
				semanticPath = output.RootResourceType + "." + column.Name
			}
			provenance := publication.ColumnExplicit
			if column.Discovered {
				provenance = publication.ColumnDiscovered
			}
			name := column.Name
			if output.RootColumnNaming != recipe.RootColumnNamingExact {
				name = publication.FlatColumnName(output.RootResourceType, name)
			}
			columns = append(columns, publication.LogicalColumn{ID: column.ID, Name: name, SemanticPath: semanticPath, Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable, Provenance: provenance})
		}
		return columns
	}
	return []publication.LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}}
}
