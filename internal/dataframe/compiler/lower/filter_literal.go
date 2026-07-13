package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func filterLiteral(value FilterValue) (any, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	switch value.Kind {
	case spec.FilterString:
		return *value.String, nil
	case spec.FilterCode:
		return value.Code.Code, nil
	case spec.FilterBoolean:
		return *value.Boolean, nil
	case spec.FilterInteger:
		return *value.Integer, nil
	case spec.FilterDecimal:
		return *value.Decimal, nil
	case spec.FilterDate:
		return *value.Date, nil
	case spec.FilterDateTime:
		return *value.DateTime, nil
	default:
		return nil, fmt.Errorf("unsupported filter literal kind %q", value.Kind)
	}
}
