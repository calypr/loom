package recipe

import (
	"fmt"
)

// ConstructionCombineKind names one bounded operation over immutable table
// revisions. Every input is resolved by exact table, revision, and output ID.
type ConstructionCombineKind string

const (
	ConstructionCombineKeyJoin    ConstructionCombineKind = "KEY_JOIN"
	ConstructionCombineAppend     ConstructionCombineKind = "APPEND"
	ConstructionCombineMembership ConstructionCombineKind = "MEMBERSHIP"
)

type ConstructionCombineJoinType string

const (
	ConstructionCombineInnerJoin ConstructionCombineJoinType = "INNER"
	ConstructionCombineLeftJoin  ConstructionCombineJoinType = "LEFT"
)

// ConstructionCombineRightMatchPolicy makes duplicate-key behavior explicit.
// PRESERVE_ALL retains every right match, including its expected row
// multiplication. The compiler does not deduplicate or select an arbitrary
// right row.
type ConstructionCombineRightMatchPolicy string

const ConstructionCombinePreserveAllMatches ConstructionCombineRightMatchPolicy = "PRESERVE_ALL"

type ConstructionCombineMembershipMode string

const (
	ConstructionCombineIncludeMatches ConstructionCombineMembershipMode = "INCLUDE"
	ConstructionCombineExcludeMatches ConstructionCombineMembershipMode = "EXCLUDE"
)

// ConstructionCombine describes one relational operation over the ordered
// TABLE_REVISION inputs on its construction step. Output IDs are authored
// identities; physical names and logical types are resolved from each exact
// published revision before lowering.
type ConstructionCombine struct {
	Kind             ConstructionCombineKind             `json:"kind"`
	Keys             []ConstructionCombineKey            `json:"keys,omitempty"`
	Projections      []ConstructionCombineProjection     `json:"projections"`
	JoinType         ConstructionCombineJoinType         `json:"joinType,omitempty"`
	RightMatchPolicy ConstructionCombineRightMatchPolicy `json:"rightMatchPolicy,omitempty"`
	MembershipMode   ConstructionCombineMembershipMode   `json:"membershipMode,omitempty"`
}

// ConstructionCombineKey pairs a left and right stable column ID. Key order
// defines tuple order when a compound key is used.
type ConstructionCombineKey struct {
	LeftColumnID  string `json:"leftColumnId"`
	RightColumnID string `json:"rightColumnId"`
}

// ConstructionCombineProjection maps a stable output column ID to a stable
// input column ID. APPEND uses one mapping per input/output pair so sources
// with different column IDs can share one output schema.
type ConstructionCombineProjection struct {
	OutputColumnID string `json:"outputColumnId"`
	InputIndex     int    `json:"inputIndex"`
	InputColumnID  string `json:"inputColumnId"`
}

// Validate checks operation structure against ordered inputs and the complete
// declared output schema. Column compatibility is checked after exact input
// revisions have been resolved by the compiler.
func (combine ConstructionCombine) Validate(inputCount int, outputs []StageColumn) error {
	if inputCount < 2 {
		return fmt.Errorf("combine requires at least two immutable table inputs")
	}
	if len(outputs) == 0 {
		return fmt.Errorf("combine output schema is required")
	}
	outputIDs := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		if output.ID == "" || outputIDs[output.ID] {
			return fmt.Errorf("combine output IDs must be non-empty and unique")
		}
		outputIDs[output.ID] = true
	}
	if len(combine.Projections) == 0 {
		return fmt.Errorf("combine projections are required")
	}
	for index, projection := range combine.Projections {
		if projection.OutputColumnID == "" || !outputIDs[projection.OutputColumnID] {
			return fmt.Errorf("combine projection %d references unknown output column %q", index, projection.OutputColumnID)
		}
		if projection.InputIndex < 0 || projection.InputIndex >= inputCount {
			return fmt.Errorf("combine projection %d inputIndex is out of range", index)
		}
		if projection.InputColumnID == "" {
			return fmt.Errorf("combine projection %d inputColumnId is required", index)
		}
	}

	switch combine.Kind {
	case ConstructionCombineKeyJoin:
		if inputCount != 2 {
			return fmt.Errorf("key join requires exactly two immutable table inputs")
		}
		if combine.JoinType != ConstructionCombineInnerJoin && combine.JoinType != ConstructionCombineLeftJoin {
			return fmt.Errorf("key join type must be INNER or LEFT")
		}
		if combine.RightMatchPolicy != ConstructionCombinePreserveAllMatches {
			return fmt.Errorf("key join rightMatchPolicy must be PRESERVE_ALL")
		}
		if len(combine.Keys) == 0 {
			return fmt.Errorf("key join requires at least one key pair")
		}
		if combine.MembershipMode != "" {
			return fmt.Errorf("key join does not accept membershipMode")
		}
		if err := validateCombineKeys(combine.Keys); err != nil {
			return err
		}
		if err := validateCombineProjectionOutputs(combine.Projections, inputCount, outputs, false); err != nil {
			return err
		}
	case ConstructionCombineAppend:
		if len(combine.Keys) != 0 || combine.JoinType != "" || combine.RightMatchPolicy != "" || combine.MembershipMode != "" {
			return fmt.Errorf("append does not accept keys, joinType, rightMatchPolicy, or membershipMode")
		}
		if err := validateCombineProjectionOutputs(combine.Projections, inputCount, outputs, true); err != nil {
			return err
		}
	case ConstructionCombineMembership:
		if inputCount != 2 {
			return fmt.Errorf("membership requires exactly two immutable table inputs")
		}
		if len(combine.Keys) == 0 {
			return fmt.Errorf("membership requires at least one key pair")
		}
		if combine.MembershipMode != ConstructionCombineIncludeMatches && combine.MembershipMode != ConstructionCombineExcludeMatches {
			return fmt.Errorf("membership mode must be INCLUDE or EXCLUDE")
		}
		if combine.JoinType != "" || combine.RightMatchPolicy != "" {
			return fmt.Errorf("membership does not accept joinType or rightMatchPolicy")
		}
		if err := validateCombineKeys(combine.Keys); err != nil {
			return err
		}
		if err := validateCombineProjectionOutputs(combine.Projections, inputCount, outputs, false); err != nil {
			return err
		}
		for _, projection := range combine.Projections {
			if projection.InputIndex != 0 {
				return fmt.Errorf("membership projections must preserve the left input")
			}
		}
	default:
		return fmt.Errorf("unsupported combine kind %q", combine.Kind)
	}
	return nil
}

func validateCombineKeys(keys []ConstructionCombineKey) error {
	seenLeft, seenRight := map[string]bool{}, map[string]bool{}
	for index, key := range keys {
		if key.LeftColumnID == "" || key.RightColumnID == "" {
			return fmt.Errorf("combine key %d requires leftColumnId and rightColumnId", index)
		}
		if seenLeft[key.LeftColumnID] || seenRight[key.RightColumnID] {
			return fmt.Errorf("combine key column IDs must be unique on each side")
		}
		seenLeft[key.LeftColumnID], seenRight[key.RightColumnID] = true, true
	}
	return nil
}

func validateCombineProjectionOutputs(projections []ConstructionCombineProjection, inputCount int, outputs []StageColumn, allowAppendMappings bool) error {
	seen := make(map[string]bool, len(projections))
	for _, projection := range projections {
		key := fmt.Sprintf("%d\x00%s", projection.InputIndex, projection.OutputColumnID)
		if seen[key] {
			return fmt.Errorf("combine has duplicate projection for input %d and output %q", projection.InputIndex, projection.OutputColumnID)
		}
		seen[key] = true
		if !allowAppendMappings && seen[projection.OutputColumnID] {
			return fmt.Errorf("combine output %q has more than one input projection", projection.OutputColumnID)
		}
		if !allowAppendMappings {
			seen[projection.OutputColumnID] = true
		}
	}
	for _, output := range outputs {
		if allowAppendMappings {
			for inputIndex := 0; inputIndex < inputCount; inputIndex++ {
				if !seen[fmt.Sprintf("%d\x00%s", inputIndex, output.ID)] {
					return fmt.Errorf("append output %q is not mapped from input %d", output.ID, inputIndex)
				}
			}
			continue
		}
		found := false
		for _, projection := range projections {
			if projection.OutputColumnID == output.ID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("combine output %q is not projected", output.ID)
		}
	}
	return nil
}
