package recipe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/unit"
)

type AggregateOperation string

const (
	AggregateCount          AggregateOperation = "COUNT"
	AggregateCountDistinct  AggregateOperation = "COUNT_DISTINCT"
	AggregateExists         AggregateOperation = "EXISTS"
	AggregateDistinctValues AggregateOperation = "DISTINCT_VALUES"
	AggregateMin            AggregateOperation = "MIN"
	AggregateMax            AggregateOperation = "MAX"
	AggregateSum            AggregateOperation = "SUM"
	AggregateMean           AggregateOperation = "MEAN"
	AggregateContainsAll    AggregateOperation = "CONTAINS_ALL"
	AggregateRequireOne     AggregateOperation = "REQUIRE_ONE"
	AggregateCollect        AggregateOperation = "COLLECT"
	AggregateFirstOrdered   AggregateOperation = "FIRST_ORDERED"
)

func (op AggregateOperation) Valid() bool {
	switch op {
	case AggregateCount, AggregateCountDistinct, AggregateExists, AggregateDistinctValues, AggregateMin, AggregateMax, AggregateSum, AggregateMean, AggregateContainsAll, AggregateRequireOne, AggregateCollect, AggregateFirstOrdered:
		return true
	default:
		return false
	}
}

type TemporalDirection string

const (
	TemporalAscending  TemporalDirection = "ASC"
	TemporalDescending TemporalDirection = "DESC"
)

type TemporalPrecision string

const TemporalPrecisionInstant TemporalPrecision = "INSTANT"

type TemporalTiePolicy string

const (
	TemporalTieRequireUnique TemporalTiePolicy = "REQUIRE_UNIQUE"
	TemporalTieResourceKey   TemporalTiePolicy = "RESOURCE_KEY"
)

type ContributorWindow struct {
	Timestamp      Expression        `json:"timestamp"`
	Anchor         Expression        `json:"anchor"`
	LowerOffset    int64             `json:"lowerOffsetSeconds"`
	UpperOffset    int64             `json:"upperOffsetSeconds"`
	LowerInclusive bool              `json:"lowerInclusive"`
	UpperInclusive bool              `json:"upperInclusive"`
	Precision      TemporalPrecision `json:"precision"`
}

type TemporalOrdering struct {
	Timestamp Expression        `json:"timestamp"`
	Direction TemporalDirection `json:"direction"`
	TiePolicy TemporalTiePolicy `json:"tiePolicy"`
}

type Aggregate struct {
	Name              string                   `json:"name"`
	OutputName        string                   `json:"outputName,omitempty"`
	Operation         AggregateOperation       `json:"operation"`
	FieldRef          string                   `json:"fieldRef,omitempty"`
	Expr              *Expression              `json:"expr,omitempty"`
	Where             *Filter                  `json:"where,omitempty"`
	ValueMode         ValueMode                `json:"valueMode,omitempty"`
	RequiredValues    []string                 `json:"requiredValues,omitempty"`
	ContributorWindow *ContributorWindow       `json:"contributorWindow,omitempty"`
	Ordering          *TemporalOrdering        `json:"ordering,omitempty"`
	UnitNormalization *UnitNormalizationPolicy `json:"unitNormalization,omitempty"`
}

type UnitNormalizationPolicy struct {
	SystemPath string                   `json:"systemPath"`
	CodePath   string                   `json:"codePath"`
	Target     unit.UnitIdentity        `json:"target"`
	Rules      []unit.UnitRuleReference `json:"rules"`
}

func (p UnitNormalizationPolicy) Validate() error {
	if !p.Target.Valid() {
		return fmt.Errorf("unit normalization target requires system and code")
	}
	if strings.TrimSpace(p.SystemPath) == "" || strings.TrimSpace(p.CodePath) == "" {
		return fmt.Errorf("unit normalization requires systemPath and codePath")
	}
	if len(p.Rules) == 0 {
		return fmt.Errorf("unit normalization requires at least one approved conversion rule")
	}
	seen := make(map[string]struct{}, len(p.Rules))
	for index, rule := range p.Rules {
		if err := unit.ValidateUnitRuleReference(rule); err != nil {
			return fmt.Errorf("rules[%d]: %w", index, err)
		}
		key := strings.TrimSpace(rule.ID) + "\x00" + strings.TrimSpace(rule.Version)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("rules[%d] duplicates source unit identity", index)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type RepresentativeSlice struct {
	Name   string  `json:"name"`
	Where  *Filter `json:"where,omitempty"`
	Limit  int     `json:"limit"`
	Fields []Field `json:"fields"`
}

func (a Aggregate) validateAt(path string, budget *int) error {
	if err := validateRecipeName(a.Name, path+".name"); err != nil {
		return err
	}
	if a.OutputName != "" {
		if err := validateRecipeName(a.OutputName, path+".outputName"); err != nil {
			return err
		}
	}
	if !a.Operation.Valid() {
		return validationError("invalid_aggregate_operation", path+".operation", fmt.Sprintf("unsupported operation %q", a.Operation))
	}
	if !a.ValueMode.Valid() {
		return validationError("invalid_value_mode", path+".valueMode", fmt.Sprintf("unsupported value mode %q", a.ValueMode))
	}
	if a.ValueMode != "" && a.ValueMode != ValueModeAuto {
		return validationError("unsupported_value_mode", path+".valueMode", "aggregate valueMode must be AUTO")
	}
	requiresExpr := a.Operation == AggregateCountDistinct || a.Operation == AggregateDistinctValues || a.Operation == AggregateMin || a.Operation == AggregateMax || a.Operation == AggregateSum || a.Operation == AggregateMean || a.Operation == AggregateContainsAll || a.Operation == AggregateRequireOne || a.Operation == AggregateCollect || a.Operation == AggregateFirstOrdered
	if requiresExpr && a.Expr == nil {
		return validationError("required", path+".expr", "operation requires expr")
	}
	if a.Expr != nil {
		if err := validateSelectorExpression(*a.Expr, path+".expr"); err != nil {
			return err
		}
		if err := validateExpressionBudget(*a.Expr, path+".expr", budget); err != nil {
			return err
		}
	}
	if a.UnitNormalization != nil {
		if a.Expr == nil {
			return validationError("required", path+".expr", "unit normalization requires a measurement expression")
		}
		if err := a.UnitNormalization.Validate(); err != nil {
			return validationError("invalid_unit_normalization", path+".unitNormalization", err.Error())
		}
	}
	if a.ContributorWindow != nil {
		switch a.Operation {
		case AggregateCount, AggregateExists, AggregateMin, AggregateMax, AggregateMean, AggregateSum, AggregateFirstOrdered:
		default:
			return validationError("invalid_contributor_window", path+".contributorWindow", "contributor window is not valid for this aggregate operation")
		}
		if err := a.ContributorWindow.validateAt(path+".contributorWindow", budget); err != nil {
			return err
		}
	}
	if a.Operation == AggregateFirstOrdered {
		if a.ContributorWindow == nil {
			return validationError("required", path+".contributorWindow", "FIRST_ORDERED requires a contributor window")
		}
		if a.Ordering == nil {
			return validationError("required", path+".ordering", "FIRST_ORDERED requires temporal ordering")
		}
		if err := a.Ordering.validateAt(path+".ordering", budget); err != nil {
			return err
		}
	} else if a.Ordering != nil {
		return validationError("invalid_ordering", path+".ordering", "ordering is only valid for FIRST_ORDERED")
	}
	if a.Where != nil {
		if err := validateRichFilterAt(*a.Where, path+".where", true); err != nil {
			return err
		}
	}
	if a.Operation == AggregateContainsAll {
		if len(a.RequiredValues) == 0 {
			return validationError("required", path+".requiredValues", "CONTAINS_ALL requires at least one required value")
		}
		seen := map[string]bool{}
		for index, value := range a.RequiredValues {
			if strings.TrimSpace(value) == "" {
				return validationError("required", fmt.Sprintf("%s.requiredValues[%d]", path, index), "required value must not be empty")
			}
			if seen[value] {
				return validationError("duplicate_name", fmt.Sprintf("%s.requiredValues[%d]", path, index), "duplicate required value")
			}
			seen[value] = true
		}
	} else if len(a.RequiredValues) != 0 {
		return validationError("invalid_required_values", path+".requiredValues", "requiredValues is only valid for CONTAINS_ALL")
	}
	return nil
}

func (w ContributorWindow) validateAt(path string, budget *int) error {
	if err := validateSelectorExpression(w.Timestamp, path+".timestamp"); err != nil {
		return err
	}
	if err := validateExpressionBudget(w.Timestamp, path+".timestamp", budget); err != nil {
		return err
	}
	if err := validateSelectorExpression(w.Anchor, path+".anchor"); err != nil {
		return err
	}
	if err := validateExpressionBudget(w.Anchor, path+".anchor", budget); err != nil {
		return err
	}
	if w.LowerOffset > w.UpperOffset {
		return validationError("invalid_temporal_window", path, "lowerOffsetSeconds must not exceed upperOffsetSeconds")
	}
	if w.Precision != TemporalPrecisionInstant {
		return validationError("invalid_temporal_precision", path+".precision", "precision must be INSTANT")
	}
	return nil
}

func (o TemporalOrdering) validateAt(path string, budget *int) error {
	if err := validateSelectorExpression(o.Timestamp, path+".timestamp"); err != nil {
		return err
	}
	if err := validateExpressionBudget(o.Timestamp, path+".timestamp", budget); err != nil {
		return err
	}
	if o.Direction != TemporalAscending && o.Direction != TemporalDescending {
		return validationError("invalid_temporal_direction", path+".direction", "direction must be ASC or DESC")
	}
	if o.TiePolicy != TemporalTieRequireUnique && o.TiePolicy != TemporalTieResourceKey {
		return validationError("invalid_temporal_tie_policy", path+".tiePolicy", "tiePolicy must be REQUIRE_UNIQUE or RESOURCE_KEY")
	}
	return nil
}

func (s RepresentativeSlice) validateAt(path string, budget *int) error {
	if err := validateRecipeName(s.Name, path+".name"); err != nil {
		return err
	}
	if s.Limit <= 0 || s.Limit > maxRepresentativeSliceLimit {
		return validationError("invalid_limit", path+".limit", fmt.Sprintf("must be between 1 and %d", maxRepresentativeSliceLimit))
	}
	if len(s.Fields) == 0 {
		return validationError("required", path+".fields", "at least one field is required")
	}
	if s.Where != nil {
		if err := validateRichFilterAt(*s.Where, path+".where", false); err != nil {
			return err
		}
	}
	for index, field := range s.Fields {
		fieldPath := fmt.Sprintf("%s.fields[%d]", path, index)
		if err := validateSelectorExpression(field.Expr, fieldPath+".expr"); err != nil {
			return err
		}
	}
	return validateFieldsBudget(s.Fields, path+".fields", budget)
}
