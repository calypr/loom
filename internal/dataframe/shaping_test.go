package dataframe

import "testing"

func TestAggregateConfigValidate(t *testing.T) {
	valid := []AggregateConfig{
		{Function: AggregateCount, NullPolicy: NullIgnore},
		{Function: AggregateCountDistinct, Input: "patient.id", NullPolicy: NullIgnore},
		{Function: AggregateExists, Input: "condition.id", NullPolicy: NullReject},
		{Function: AggregateDistinctValues, Input: "code", NullPolicy: NullInclude},
		{Function: AggregateMin, Input: "date", NullPolicy: NullIgnore},
		{Function: AggregateMax, Input: "date", NullPolicy: NullIgnore},
	}
	for _, config := range valid {
		if err := config.Validate(); err != nil {
			t.Errorf("Validate(%#v): %v", config, err)
		}
	}

	invalid := []AggregateConfig{
		{},
		{Function: AggregateMin, NullPolicy: NullIgnore},
		{Function: AggregateCount, NullPolicy: "sometimes"},
		{Function: "median", Input: "value", NullPolicy: NullIgnore},
	}
	for _, config := range invalid {
		if err := config.Validate(); err == nil {
			t.Errorf("invalid aggregate %#v unexpectedly succeeded", config)
		}
	}
}

func TestPivotConfigRequestedColumns(t *testing.T) {
	config := PivotConfig{
		Key:               "observation.code",
		Value:             "observation.value",
		DuplicatePolicy:   DuplicateDistinctArray,
		NullPolicy:        NullIgnore,
		CardinalityPolicy: PivotRequestedColumns,
		SparsePolicy:      SparsePivotNull,
		RequestedColumns:  []string{"height", "weight"},
		MaxColumns:        2,
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("valid requested-columns pivot: %v", err)
	}

	config.RequestedColumns = nil
	if err := config.Validate(); err == nil {
		t.Fatal("requested-columns pivot without columns unexpectedly succeeded")
	}
}

func TestPivotConfigBoundedDiscovery(t *testing.T) {
	config := PivotConfig{
		Key:               "observation.code",
		Value:             "observation.value",
		DuplicatePolicy:   DuplicateReject,
		NullPolicy:        NullReject,
		CardinalityPolicy: PivotBoundedDiscovery,
		SparsePolicy:      SparsePivotOmit,
		MaxColumns:        100,
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("valid bounded-discovery pivot: %v", err)
	}

	config.MaxColumns = 0
	if err := config.Validate(); err == nil {
		t.Fatal("unbounded pivot discovery unexpectedly succeeded")
	}
}

func TestPivotConfigRejectsInvalidShape(t *testing.T) {
	base := PivotConfig{
		Key:               "code",
		Value:             "value",
		DuplicatePolicy:   DuplicateFirst,
		NullPolicy:        NullIgnore,
		CardinalityPolicy: PivotRequestedColumns,
		SparsePolicy:      SparsePivotNull,
		RequestedColumns:  []string{"a"},
	}
	tests := []PivotConfig{
		func() PivotConfig { c := base; c.Key = ""; return c }(),
		func() PivotConfig { c := base; c.Value = " "; return c }(),
		func() PivotConfig { c := base; c.DuplicatePolicy = "merge"; return c }(),
		func() PivotConfig { c := base; c.SparsePolicy = "zero"; return c }(),
		func() PivotConfig { c := base; c.RequestedColumns = []string{"a", "a"}; return c }(),
		func() PivotConfig { c := base; c.RequestedColumns = []string{" "}; return c }(),
		func() PivotConfig { c := base; c.MaxColumns = 1; c.RequestedColumns = []string{"a", "b"}; return c }(),
	}
	for _, config := range tests {
		if err := config.Validate(); err == nil {
			t.Errorf("invalid pivot %#v unexpectedly succeeded", config)
		}
	}
}
