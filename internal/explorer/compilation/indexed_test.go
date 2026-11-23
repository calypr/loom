package compilation

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestExpandIndexedProjectionPreservesSharedNestedCoordinates(t *testing.T) {
	boundaries := []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}, {Path: "name[].given[]", MaxItems: 3}}
	fields, counts, err := expandIndexedProjection("given", "name[].given[]", boundaries)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 || fields[4].Selector != "name[1].given[1]" || fields[4].Leaf != "given__1__1" {
		t.Fatalf("fields = %#v", fields)
	}
	if len(counts) != 3 || counts[0].Selector != "name[]" || counts[2].Selector != "name[1].given[]" {
		t.Fatalf("counts = %#v", counts)
	}
}

func TestExpandIndexedProjectionRejectsMoreThanOneThousandItems(t *testing.T) {
	_, _, err := expandIndexedProjection("value", "value[]", []capability.RepeatedBoundary{{Path: "value[]", MaxItems: 1001}})
	if err == nil || !strings.Contains(err.Error(), "maximum is 1000") {
		t.Fatalf("error = %v", err)
	}
}

func TestChoiceArmForPathIdentifiesTypedFHIRSibling(t *testing.T) {
	if got := choiceArmForPath("component[].valueQuantity.value"); got != "valueQuantity" {
		t.Fatalf("choice arm = %q", got)
	}
	if got := choiceArmForPath("component[].code.coding[].value"); got != "" {
		t.Fatalf("ordinary field was labeled as a choice arm: %q", got)
	}
}

func TestExpandIndexedProjectionRejectsUnsafeCartesianWidthWithoutTruncating(t *testing.T) {
	_, _, err := expandIndexedProjection("value", "outer[].inner[]", []capability.RepeatedBoundary{{Path: "outer[]", MaxItems: 501}, {Path: "outer[].inner[]", MaxItems: 201}})
	if err == nil || !strings.Contains(err.Error(), "no columns were truncated") {
		t.Fatalf("error = %v", err)
	}
}
