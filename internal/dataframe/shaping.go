package dataframe

import (
	"fmt"
	"strings"
)

// AggregateFunction identifies a stable, backend-independent reduction.
type AggregateFunction string

const (
	AggregateCount          AggregateFunction = "count"
	AggregateCountDistinct  AggregateFunction = "count_distinct"
	AggregateExists         AggregateFunction = "exists"
	AggregateDistinctValues AggregateFunction = "distinct_values"
	AggregateMin            AggregateFunction = "min"
	AggregateMax            AggregateFunction = "max"
)

func (f AggregateFunction) Validate() error {
	switch f {
	case AggregateCount, AggregateCountDistinct, AggregateExists,
		AggregateDistinctValues, AggregateMin, AggregateMax:
		return nil
	case "":
		return fmt.Errorf("aggregate function is required")
	default:
		return fmt.Errorf("unsupported aggregate function %q", f)
	}
}

// NullPolicy defines how missing or null input values participate in shaping.
type NullPolicy string

const (
	NullIgnore  NullPolicy = "ignore"
	NullInclude NullPolicy = "include"
	NullReject  NullPolicy = "reject"
)

func (p NullPolicy) Validate() error {
	switch p {
	case NullIgnore, NullInclude, NullReject:
		return nil
	case "":
		return fmt.Errorf("null policy is required")
	default:
		return fmt.Errorf("unsupported null policy %q", p)
	}
}

// AggregateConfig describes an aggregate without binding it to AQL syntax.
// Input is an opaque semantic expression identifier owned by the future IR.
type AggregateConfig struct {
	Function   AggregateFunction
	Input      string
	NullPolicy NullPolicy
}

func (c AggregateConfig) Validate() error {
	if err := c.Function.Validate(); err != nil {
		return err
	}
	if err := c.NullPolicy.Validate(); err != nil {
		return err
	}
	if c.Function != AggregateCount && strings.TrimSpace(c.Input) == "" {
		return fmt.Errorf("aggregate %s requires an input", c.Function)
	}
	return nil
}

// DuplicatePolicy defines how multiple values for the same pivot key are
// handled. First and last require deterministic ordering from the caller.
type DuplicatePolicy string

const (
	DuplicateReject        DuplicatePolicy = "reject"
	DuplicateFirst         DuplicatePolicy = "first"
	DuplicateLast          DuplicatePolicy = "last"
	DuplicateArray         DuplicatePolicy = "array"
	DuplicateDistinctArray DuplicatePolicy = "distinct_array"
)

func (p DuplicatePolicy) Validate() error {
	switch p {
	case DuplicateReject, DuplicateFirst, DuplicateLast, DuplicateArray,
		DuplicateDistinctArray:
		return nil
	case "":
		return fmt.Errorf("duplicate policy is required")
	default:
		return fmt.Errorf("unsupported duplicate policy %q", p)
	}
}

// PivotCardinalityPolicy controls which pivot columns may be materialized.
// Discovery is always bounded; an unbounded discovery policy is intentionally
// absent from this contract.
type PivotCardinalityPolicy string

const (
	PivotRequestedColumns PivotCardinalityPolicy = "requested_columns"
	PivotBoundedDiscovery PivotCardinalityPolicy = "bounded_discovery"
)

func (p PivotCardinalityPolicy) Validate() error {
	switch p {
	case PivotRequestedColumns, PivotBoundedDiscovery:
		return nil
	case "":
		return fmt.Errorf("pivot cardinality policy is required")
	default:
		return fmt.Errorf("unsupported pivot cardinality policy %q", p)
	}
}

// SparsePivotPolicy defines the value emitted when a requested pivot key has
// no matching input.
type SparsePivotPolicy string

const (
	SparsePivotNull SparsePivotPolicy = "null"
	SparsePivotOmit SparsePivotPolicy = "omit"
)

func (p SparsePivotPolicy) Validate() error {
	switch p {
	case SparsePivotNull, SparsePivotOmit:
		return nil
	case "":
		return fmt.Errorf("sparse pivot policy is required")
	default:
		return fmt.Errorf("unsupported sparse pivot policy %q", p)
	}
}

// PivotConfig describes pivot semantics independently of physical lowering.
// Key and Value are opaque semantic expression identifiers owned by the IR.
type PivotConfig struct {
	Key               string
	Value             string
	DuplicatePolicy   DuplicatePolicy
	NullPolicy        NullPolicy
	CardinalityPolicy PivotCardinalityPolicy
	SparsePolicy      SparsePivotPolicy
	RequestedColumns  []string
	MaxColumns        int
}

func (c PivotConfig) Validate() error {
	if strings.TrimSpace(c.Key) == "" {
		return fmt.Errorf("pivot key is required")
	}
	if strings.TrimSpace(c.Value) == "" {
		return fmt.Errorf("pivot value is required")
	}
	if err := c.DuplicatePolicy.Validate(); err != nil {
		return err
	}
	if err := c.NullPolicy.Validate(); err != nil {
		return err
	}
	if err := c.CardinalityPolicy.Validate(); err != nil {
		return err
	}
	if err := c.SparsePolicy.Validate(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(c.RequestedColumns))
	for index, column := range c.RequestedColumns {
		column = strings.TrimSpace(column)
		if column == "" {
			return fmt.Errorf("requested pivot column %d is empty", index)
		}
		if _, exists := seen[column]; exists {
			return fmt.Errorf("requested pivot column %q is duplicated", column)
		}
		seen[column] = struct{}{}
	}

	switch c.CardinalityPolicy {
	case PivotRequestedColumns:
		if len(c.RequestedColumns) == 0 {
			return fmt.Errorf("requested-columns pivot requires at least one column")
		}
		if c.MaxColumns < 0 {
			return fmt.Errorf("pivot max columns cannot be negative")
		}
		if c.MaxColumns > 0 && len(c.RequestedColumns) > c.MaxColumns {
			return fmt.Errorf("requested pivot columns %d exceed maximum %d", len(c.RequestedColumns), c.MaxColumns)
		}
	case PivotBoundedDiscovery:
		if c.MaxColumns <= 0 {
			return fmt.Errorf("bounded pivot discovery requires a positive maximum")
		}
	}
	return nil
}
