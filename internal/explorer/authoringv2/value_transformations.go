package authoringv2

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
)

type ValueTransformationCapability struct {
	Available  bool   `json:"available"`
	ReasonCode string `json:"reasonCode,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ColumnValueTransformationCapabilities struct {
	ExactCategoryRecode ValueTransformationCapability `json:"exactCategoryRecode"`
	CodedValueRecoding  ValueTransformationCapability `json:"codedValueRecoding"`
}

func ColumnValueTransformationCapabilitiesForCatalog(catalog CatalogSnapshot, candidateID string) ColumnValueTransformationCapabilities {
	for _, candidate := range catalog.Candidates {
		if candidate.ID == candidateID {
			result := columnValueTransformationCapabilities(candidate.LogicalType, candidate.Cardinality)
			if !result.ExactCategoryRecode.Available && strings.EqualFold(strings.TrimSpace(candidate.LogicalType), "string") && hasScalarProjection(candidate.ProjectionModes) {
				result.ExactCategoryRecode = ValueTransformationCapability{Available: true}
			}
			return result
		}
	}
	return unavailableColumnValueTransformations("CANDIDATE_NOT_ADVERTISED", "the source candidate is not present in the resolved capability snapshot")
}

func hasScalarProjection(modes []string) bool {
	for _, mode := range modes {
		switch strings.ToUpper(strings.TrimSpace(mode)) {
		case "VALUE", "FIRST":
			return true
		}
	}
	return false
}

func columnValueTransformationCapabilitiesForColumn(document Document, catalog CatalogSnapshot, column Column, source ColumnSource) ColumnValueTransformationCapabilities {
	if source.Kind == SourceCodedValue {
		result := unavailableColumnValueTransformations("COLUMN_VALUE_TYPE_UNSUPPORTED", "exact category recoding requires a scalar string value")
		result.CodedValueRecoding = ValueTransformationCapability{
			ReasonCode: "CODED_VALUE_RECODE_UNAVAILABLE",
			Reason:     "coded value recoding is unavailable because this scalar transformation cannot preserve both Coding.system and Coding.code",
		}
		return result
	}
	if source.Kind == SourceOwnerRecords {
		return unavailableColumnValueTransformations("COLUMN_VALUE_TYPE_UNSUPPORTED", "exact category recoding requires a scalar string value")
	}

	logicalType := inferredSourceLogicalType(document, catalog, column.OccurrenceID, source, column.LogicalType)
	mode := strings.ToUpper(strings.TrimSpace(source.ProjectionMode()))
	if mode == "ALL" || mode == "DISTINCT" || mode == "INDEXED" {
		return unavailableColumnValueTransformations("COLUMN_VALUE_SHAPE_UNSUPPORTED", "exact category recoding requires one scalar public projection, not an array or indexed expansion")
	}
	if source.Aggregate != nil {
		switch strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)) {
		case "COLLECT", "DISTINCT_VALUES":
			return unavailableColumnValueTransformations("COLUMN_VALUE_SHAPE_UNSUPPORTED", "exact category recoding requires one scalar public projection, not an array or indexed expansion")
		}
	}
	return columnValueTransformationCapabilities(logicalType, "optional_one")
}

func validateColumnValueTransformationForCatalog(document Document, catalog CatalogSnapshot, column Column, source ColumnSource, transformation columntransform.ValueTransformation) error {
	if err := transformation.Validate(); err != nil {
		return err
	}
	capabilities := columnValueTransformationCapabilitiesForColumn(document, catalog, column, source)
	if !capabilities.ExactCategoryRecode.Available {
		capability := capabilities.ExactCategoryRecode
		if source.Kind == SourceCodedValue {
			capability = capabilities.CodedValueRecoding
		}
		return fmt.Errorf("value transformation unavailable (%s): %s", capability.ReasonCode, capability.Reason)
	}
	return nil
}

func columnValueTransformationCapabilities(logicalType, cardinality string) ColumnValueTransformationCapabilities {
	result := unavailableColumnValueTransformations("COLUMN_VALUE_TYPE_UNSUPPORTED", "exact category recoding requires a scalar string value")
	if strings.EqualFold(strings.TrimSpace(logicalType), "string") && !capability.IsRepeatedCardinality(cardinality) {
		result.ExactCategoryRecode = ValueTransformationCapability{Available: true}
	}
	return result
}

func unavailableColumnValueTransformations(code, reason string) ColumnValueTransformationCapabilities {
	return ColumnValueTransformationCapabilities{
		ExactCategoryRecode: ValueTransformationCapability{ReasonCode: code, Reason: reason},
		CodedValueRecoding: ValueTransformationCapability{
			ReasonCode: "CODED_VALUE_RECODE_UNAVAILABLE",
			Reason:     "coded value recoding is unavailable because this scalar transformation cannot preserve both Coding.system and Coding.code",
		},
	}
}
