package dataframe

import "fmt"

// filterLiteral converts the typed filter value into the scalar representation
// bound to AQL. Validation has already established the active member and its
// kind; retaining this conversion beside the physical planner keeps the old
// string renderer out of the execution path.
func filterLiteral(value FilterValue) (any, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	switch value.Kind {
	case FilterString:
		return *value.String, nil
	case FilterCode:
		return value.Code.Code, nil
	case FilterBoolean:
		return *value.Boolean, nil
	case FilterInteger:
		return *value.Integer, nil
	case FilterDecimal:
		return *value.Decimal, nil
	case FilterDate:
		return *value.Date, nil
	case FilterDateTime:
		return *value.DateTime, nil
	default:
		return nil, fmt.Errorf("unsupported filter literal kind %q", value.Kind)
	}
}
