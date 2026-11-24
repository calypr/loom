package compilerfixtures

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func MustPhysicalSelector(t *testing.T, input string) spec.Selector {
	t.Helper()
	selector, err := spec.ParseSelector(input)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", input, err)
	}
	return selector
}
