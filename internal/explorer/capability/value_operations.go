package capability

import "strings"

type AggregateOperation string

const (
	AggregateCount          AggregateOperation = "COUNT"
	AggregateCountDistinct  AggregateOperation = "COUNT_DISTINCT"
	AggregateDistinctValues AggregateOperation = "DISTINCT_VALUES"
	AggregateExists         AggregateOperation = "EXISTS"
	AggregateMin            AggregateOperation = "MIN"
	AggregateMax            AggregateOperation = "MAX"
	AggregateSum            AggregateOperation = "SUM"
	AggregateMean           AggregateOperation = "MEAN"
	AggregateContainsAll    AggregateOperation = "CONTAINS_ALL"
	AggregateRequireOne     AggregateOperation = "REQUIRE_ONE"
	AggregateCollect        AggregateOperation = "COLLECT"
	AggregateFirstOrdered   AggregateOperation = "FIRST_ORDERED"
)

type AggregateRowContext string

const (
	AggregateRowsRecords  AggregateRowContext = "RECORDS"
	AggregateRowsGroups   AggregateRowContext = "GROUPS"
	AggregateRowsExpanded AggregateRowContext = "EXPANDED"
)

type AggregateInput struct {
	LogicalType                 string
	Cardinality                 string
	HasField                    bool
	RelatedResource             bool
	ContributorWindowConfigured bool
	OrderingConfigured          bool
	RequiredValuesConfigured    bool
}

type AggregateOperationCapability struct {
	Operation             AggregateOperation  `json:"operation"`
	RowContext            AggregateRowContext `json:"rowContext"`
	Supported             bool                `json:"supported"`
	ReasonCode            string              `json:"reasonCode,omitempty"`
	Reason                string              `json:"reason,omitempty"`
	ResultLogicalType     string              `json:"resultLogicalType,omitempty"`
	ResultCardinality     string              `json:"resultCardinality,omitempty"`
	MissingValueSemantics string              `json:"missingValueSemantics,omitempty"`
	ContributorSemantics  string              `json:"contributorSemantics,omitempty"`
	RequiresConfiguration []string            `json:"requiresConfiguration,omitempty"`
}

var aggregateOperations = []AggregateOperation{
	AggregateCount,
	AggregateCountDistinct,
	AggregateDistinctValues,
	AggregateExists,
	AggregateMin,
	AggregateMax,
	AggregateSum,
	AggregateMean,
	AggregateContainsAll,
	AggregateRequireOne,
	AggregateCollect,
	AggregateFirstOrdered,
}

// DeriveAggregateOperationCapabilities gives every operation a typed,
// row-context-specific support decision. Compiler probes are intersected with
// this domain decision by the server before the choice is advertised.
func DeriveAggregateOperationCapabilities(input AggregateInput, rows AggregateRowContext) []AggregateOperationCapability {
	choices := make([]AggregateOperationCapability, 0, len(aggregateOperations))
	for _, operation := range aggregateOperations {
		choice := aggregateOperationContract(operation, input)
		choice.RowContext = rows
		if rows == AggregateRowsGroups {
			choice.Supported = false
			choice.ReasonCode = "ROW_CONTEXT_UNSUPPORTED"
			choice.Reason = "aggregate reducers are not compiled for grouped rows"
			choice.RequiresConfiguration = nil
			choices = append(choices, choice)
			continue
		}
		if rows == AggregateRowsExpanded {
			choice.Supported = false
			choice.ReasonCode = "EXPANDED_AGGREGATE_SCOPE_UNDEFINED"
			choice.Reason = "the compiler expands rows, but aggregate scope relative to expanded items is not defined"
			choice.RequiresConfiguration = nil
			choices = append(choices, choice)
			continue
		}
		if rows != AggregateRowsRecords {
			choice.Supported = false
			choice.ReasonCode = "ROW_CONTEXT_UNSUPPORTED"
			choice.Reason = "aggregate reducers require a supported row definition"
			choice.RequiresConfiguration = nil
			choices = append(choices, choice)
			continue
		}
		if !choice.Supported {
			choices = append(choices, choice)
			continue
		}
		if choice.Operation == AggregateFirstOrdered {
			if !input.RelatedResource {
				choice.Supported = false
				choice.ReasonCode = "RELATED_RESOURCE_REQUIRED"
				choice.Reason = "ordered selection requires a related-resource source"
				choice.RequiresConfiguration = nil
			} else {
				if !input.ContributorWindowConfigured {
					choice.RequiresConfiguration = append(choice.RequiresConfiguration, "contributorWindow")
				}
				if !input.OrderingConfigured {
					choice.RequiresConfiguration = append(choice.RequiresConfiguration, "ordering")
				}
			}
		}
		if choice.Operation == AggregateContainsAll && input.HasField && !input.RequiredValuesConfigured {
			choice.RequiresConfiguration = []string{"requiredValues"}
		}
		choices = append(choices, choice)
	}
	return choices
}

func aggregateOperationContract(operation AggregateOperation, input AggregateInput) AggregateOperationCapability {
	choice := AggregateOperationCapability{Operation: operation}
	set := func(resultType, cardinality, missing, contributors string) {
		choice.Supported = true
		choice.ResultLogicalType = resultType
		choice.ResultCardinality = cardinality
		choice.MissingValueSemantics = missing
		choice.ContributorSemantics = contributors
	}
	unsupported := func(code, reason string) AggregateOperationCapability {
		choice.ReasonCode = code
		choice.Reason = reason
		return choice
	}

	if operation == AggregateCount || operation == AggregateExists {
		if input.HasField {
			if reason := aggregateInputReason(input); reason != nil {
				return unsupported(reason.Code, reason.Message)
			}
		}
		if operation == AggregateCount {
			set("integer", "ONE", "missing values are excluded; an empty set returns zero", "counts matching resources or non-null field values, according to whether a field is selected")
		} else {
			set("boolean", "ONE", "missing values are false; an empty set returns false", "true when at least one matching resource or non-null field value exists")
		}
		return choice
	}
	if reason := aggregateInputReason(input); reason != nil {
		return unsupported(reason.Code, reason.Message)
	}

	inputType := normalizedLogicalType(input.LogicalType)
	switch operation {
	case AggregateCountDistinct:
		set("integer", "ONE", "missing values are excluded; an empty set returns zero", "each distinct non-null input value contributes once")
	case AggregateDistinctValues:
		set(inputType, "MANY", "missing values are excluded; an empty set returns an empty list", "each distinct non-null input value appears once")
	case AggregateMin, AggregateMax:
		set(inputType, "OPTIONAL_ONE", "missing values are excluded; no non-null inputs returns null", "all non-null input values are considered")
	case AggregateSum:
		if !numericLogicalType(inputType) {
			return unsupported("NUMERIC_INPUT_REQUIRED", "SUM requires an integer or decimal input")
		}
		set("decimal", "OPTIONAL_ONE", "null inputs are ignored; no non-null inputs returns null", "every non-null numeric input contributes once, grouped by its source resource")
	case AggregateMean:
		if !numericLogicalType(inputType) {
			return unsupported("NUMERIC_INPUT_REQUIRED", "MEAN requires an integer or decimal input")
		}
		set("decimal", "OPTIONAL_ONE", "null inputs are ignored; no non-null inputs returns null", "every non-null numeric input contributes once, grouped by its source resource")
	case AggregateContainsAll:
		if inputType != "string" && inputType != "code" {
			return unsupported("CODE_OR_STRING_INPUT_REQUIRED", "CONTAINS_ALL requires a string or code input")
		}
		set("boolean", "ONE", "missing values are excluded; an empty set returns false", "all required values must be present among the non-null inputs")
	case AggregateRequireOne:
		set(inputType, "OPTIONAL_ONE", "missing values are excluded; no non-null input returns null; multiple values fail", "the single non-null input contributes, and multiplicity is checked")
	case AggregateCollect:
		set(inputType, "MANY", "input nulls are preserved; an empty set returns an empty list", "source values are retained in source order and grouped by resource")
	case AggregateFirstOrdered:
		set(inputType, "OPTIONAL_ONE", "missing values are excluded; no eligible input returns null", "the value attached to the selected timestamp/resource contributes")
	default:
		return unsupported("UNKNOWN_OPERATION", "aggregate operation is not supported")
	}
	return choice
}

type aggregateInputFailure struct {
	Code    string
	Message string
}

func aggregateInputReason(input AggregateInput) *aggregateInputFailure {
	if !input.HasField {
		return &aggregateInputFailure{Code: "FIELD_INPUT_REQUIRED", Message: "this aggregate operation requires a selected scalar field"}
	}
	inputType := normalizedLogicalType(input.LogicalType)
	if inputType == "" || inputType == "unknown" || inputType == "object" || inputType == "array" {
		return &aggregateInputFailure{Code: "SCALAR_INPUT_REQUIRED", Message: "aggregate input must resolve to a supported scalar type"}
	}
	switch strings.ToLower(strings.TrimSpace(input.Cardinality)) {
	case "one", "optional_one", "required_one", "many", "unknown_observed_many":
		return nil
	default:
		return &aggregateInputFailure{Code: "INPUT_CARDINALITY_UNRESOLVED", Message: "aggregate input cardinality is not compiler-resolved"}
	}
}

func normalizedLogicalType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func numericLogicalType(value string) bool {
	return value == "integer" || value == "decimal"
}

func cloneAggregateOperationCapabilities(values []AggregateOperationCapability) []AggregateOperationCapability {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]AggregateOperationCapability, len(values))
	copy(cloned, values)
	for index := range cloned {
		cloned[index].RequiresConfiguration = append([]string(nil), values[index].RequiresConfiguration...)
	}
	return cloned
}
