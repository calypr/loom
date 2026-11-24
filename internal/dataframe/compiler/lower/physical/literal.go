package physical

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func FilterLiteral(value spec.FilterValue) (any, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	if literal, ok := spec.FilterLiteralValue(value); ok {
		return literal, nil
	} else {
		return nil, fmt.Errorf("unsupported filter literal kind %q", value.Kind)
	}
}
