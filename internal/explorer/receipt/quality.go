package receipt

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/expression"
)

func derivedOutputQuality(operation string) (ConstructedOutputQuality, error) {
	operation = strings.ToUpper(strings.TrimSpace(operation))
	switch operation {
	case "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE":
		return ConstructedOutputQuality{
			Lossless: false, StructuralSuitability: "requires-review",
			LossReasons: []string{"TABLE_SHAPE_DERIVED_" + operation + "_NON_LOSSLESS"},
		}, nil
	default:
		return ConstructedOutputQuality{}, fmt.Errorf("unsupported derived operation %q", operation)
	}
}

func pivotOutputQuality(duplicatePolicy, missingCellPolicy, unlistedCategoryPolicy string) (ConstructedOutputQuality, error) {
	quality := ConstructedOutputQuality{
		Lossless: false, StructuralSuitability: "requires-review",
		LossReasons: []string{"TABLE_SHAPE_PIVOT_BASE_COLUMNS_DROPPED"},
	}
	switch duplicatePolicy {
	case "ERROR":
	case "SUM", "MIN", "MAX":
		quality.Lossless = false
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_PIVOT_DUPLICATES_AGGREGATED")
	default:
		return ConstructedOutputQuality{}, fmt.Errorf("unsupported duplicate policy %q", duplicatePolicy)
	}
	switch missingCellPolicy {
	case "NULL", "ERROR":
	default:
		return ConstructedOutputQuality{}, fmt.Errorf("unsupported missing-cell policy %q", missingCellPolicy)
	}
	switch unlistedCategoryPolicy {
	case "ERROR":
	case "EXCLUDE_WITH_EVIDENCE":
		quality.Lossless = false
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_PIVOT_UNLISTED_CATEGORIES_EXCLUDED")
	default:
		return ConstructedOutputQuality{}, fmt.Errorf("unsupported unlisted-category policy %q", unlistedCategoryPolicy)
	}
	if !quality.Lossless {
		quality.StructuralSuitability = "requires-review"
	}
	return quality, nil
}

func unpivotOutputQuality(nullRowPolicy string) (ConstructedOutputQuality, error) {
	quality := ConstructedOutputQuality{Lossless: true, StructuralSuitability: "scalar"}
	switch nullRowPolicy {
	case "PRESERVE":
	case "DROP":
		quality.Lossless = false
		quality.StructuralSuitability = "requires-review"
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_UNPIVOT_NULL_ROWS_DROPPED")
	default:
		return ConstructedOutputQuality{}, fmt.Errorf("unsupported null-row policy %q", nullRowPolicy)
	}
	return quality, nil
}

type constructedColumnProfile struct {
	Shape                 string
	Lossless              bool
	MLReady               bool
	StructuralSuitability string
	LossReasons           []string
	Filterable            bool
	Chartable             bool
}

func constructedOutputProfileFor(quality ConstructedOutputQuality, column facade.CompiledOutputColumn) (constructedColumnProfile, error) {
	cardinality := expression.Cardinality(column.Cardinality)
	if cardinality == expression.Many {
		if quality.Shape != "array" {
			return constructedColumnProfile{}, fmt.Errorf("constructed output cardinality %q requires an explicit array policy", column.Cardinality)
		}
		if !isSupportedConstructedScalarKind(expression.ValueKind(column.Kind)) {
			return constructedColumnProfile{}, fmt.Errorf("constructed array element kind %q is not a supported scalar type", column.Kind)
		}
		reasons, err := uniqueColumnsInOrder(append(append([]string(nil), quality.LossReasons...), TableShapeMLReadinessUnassessed))
		if err != nil {
			return constructedColumnProfile{}, fmt.Errorf("constructed output quality policy has an invalid reason: %w", err)
		}
		if quality.StructuralSuitability == "" {
			return constructedColumnProfile{}, fmt.Errorf("constructed output quality policy is missing structural suitability")
		}
		return constructedColumnProfile{
			Shape: "array", Lossless: quality.Lossless, MLReady: false,
			StructuralSuitability: quality.StructuralSuitability, LossReasons: reasons,
			Filterable: false, Chartable: false,
		}, nil
	}
	if cardinality != expression.RequiredOne && cardinality != expression.OptionalOne {
		return constructedColumnProfile{}, fmt.Errorf("constructed output cardinality %q is not scalar", column.Cardinality)
	}
	if quality.Shape != "" {
		return constructedColumnProfile{}, fmt.Errorf("constructed output array policy has scalar cardinality %q", column.Cardinality)
	}
	if !isSupportedConstructedScalarKind(expression.ValueKind(column.Kind)) {
		return constructedColumnProfile{}, fmt.Errorf("constructed output kind %q is not a supported scalar type", column.Kind)
	}
	if quality.StructuralSuitability == "" {
		return constructedColumnProfile{}, fmt.Errorf("constructed output quality policy is missing structural suitability")
	}
	reasons, err := uniqueColumnsInOrder(append(append([]string(nil), quality.LossReasons...), TableShapeMLReadinessUnassessed))
	if err != nil {
		return constructedColumnProfile{}, fmt.Errorf("constructed output quality policy has an invalid reason: %w", err)
	}
	return constructedColumnProfile{
		Shape: "scalar", Lossless: quality.Lossless, MLReady: false,
		StructuralSuitability: quality.StructuralSuitability, LossReasons: reasons,
		Filterable: true, Chartable: true,
	}, nil
}

func isSupportedConstructedScalarKind(kind expression.ValueKind) bool {
	switch kind {
	case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
		expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		return true
	default:
		return false
	}
}
