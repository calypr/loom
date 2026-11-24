package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func CodedGroupComponent(code string) map[string]any {
	return map[string]any{"code": map[string]any{"coding": []any{
		map[string]any{"system": "urn:loom:inline-test", "code": code},
	}}}
}

func ObservationCodedGroupOutput() recipe.Output {
	return codedGroupOutput("coded_group_rows", "Observation", "resource_id", "component[].code.coding[]")
}

func BodyStructureCodedGroupOutput() recipe.Output {
	return codedGroupOutput("body_structures", "BodyStructure", "body_structure_id", "includedStructure[].structure.coding[]")
}

func codedGroupOutput(name, resourceType, idColumn, codingPath string) recipe.Output {
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	return recipe.Output{
		Name: name, RootResourceType: resourceType, RowGrain: "resource",
		Fields: []recipe.Field{{Name: idColumn, ColumnID: idColumn, Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: idColumn, Name: idColumn}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
					ConstructionID: "group_codes",
					Source: recipe.ConstructionCodedGroupSource{
						OccurrenceID: "base", ResourceType: resourceType, CodingPath: codingPath,
						FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
					},
					MissingKeyPolicy:     recipe.ConstructionGroupMissingKeyGroup,
					SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
					CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
				}}, Outputs: columns,
			}},
		},
	}
}
