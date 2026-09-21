package recipe

import (
	"math"
	"strings"
	"testing"
)

func TestTableScalarCanonicalIdentityKeepsTypedZeroDistinct(t *testing.T) {
	positiveZero := float64(0)
	negativeZero := math.Copysign(0, -1)
	positive := TableScalar{Kind: TableScalarDecimal, Decimal: &positiveZero}
	negative := TableScalar{Kind: TableScalarDecimal, Decimal: &negativeZero}
	integer := TableScalar{Kind: TableScalarInteger, Integer: int64Pointer(0)}
	if positive.identity() != negative.identity() {
		t.Fatalf("decimal zero identities differ: %q vs %q", positive.identity(), negative.identity())
	}
	if positive.identity() == integer.identity() {
		t.Fatalf("INTEGER zero and DECIMAL zero share canonical identity %q", positive.identity())
	}

	pivot := GroupedPivot{
		ConstructionID: "shape_zero", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
		Categories: []GroupedPivotCategory{
			{Key: positive, Output: "positive", Label: "Positive"},
			{Key: negative, Output: "negative", Label: "Negative"},
		},
		DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
		UnlistedCategoryPolicy: PivotUnlistedCategoryError,
	}
	if err := validateGroupedPivot(pivot); err == nil || !strings.Contains(err.Error(), "category keys must be unique") {
		t.Fatalf("duplicate signed decimal zero validation = %v, want canonical duplicate rejection", err)
	}
}

func int64Pointer(value int64) *int64 { return &value }
