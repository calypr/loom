package ir

import (
	"fmt"
	"strings"
)

type PhysicalEngine string

const (
	PhysicalEngineAQL        PhysicalEngine = "AQL"
	PhysicalEngineClickHouse PhysicalEngine = "CLICKHOUSE"
)

type PhysicalCombineKind string

const (
	PhysicalCombineKeyJoin    PhysicalCombineKind = "KEY_JOIN"
	PhysicalCombineAppend     PhysicalCombineKind = "APPEND"
	PhysicalCombineMembership PhysicalCombineKind = "MEMBERSHIP"
)

type PhysicalCombineInputRef struct {
	TableID    string
	RevisionID string
	OutputID   string
}

type PhysicalCombineKey struct {
	LeftColumnID  string
	RightColumnID string
}

type PhysicalCombineProjection struct {
	OutputColumnID string
	InputIndex     int
	InputColumnID  string
}

type PhysicalCombineOutputColumn struct {
	ID             string
	Name           string
	SemanticPath   string
	LogicalType    string
	ClickHouseType string
	Nullable       bool
	Repeated       bool
}

// PhysicalClickHouseCombine is the closed semantic/physical contract for a
// constructed-table operation. Published table names and authorization are
// request scoped and are attached only after exact revision resolution.
type PhysicalClickHouseCombine struct {
	Kind             PhysicalCombineKind
	Inputs           []PhysicalCombineInputRef
	Keys             []PhysicalCombineKey
	Projections      []PhysicalCombineProjection
	JoinType         string
	RightMatchPolicy string
	MembershipMode   string
	Outputs          []PhysicalCombineOutputColumn
}

func (combine PhysicalClickHouseCombine) Validate() error {
	if len(combine.Inputs) < 2 {
		return fmt.Errorf("ClickHouse combine requires at least two exact table inputs")
	}
	seenRefs := make(map[string]bool, len(combine.Inputs))
	for index, input := range combine.Inputs {
		if strings.TrimSpace(input.TableID) == "" || strings.TrimSpace(input.RevisionID) == "" || strings.TrimSpace(input.OutputID) == "" {
			return fmt.Errorf("ClickHouse combine input %d requires table, revision, and output IDs", index)
		}
		key := input.TableID + "\x00" + input.RevisionID + "\x00" + input.OutputID
		if seenRefs[key] {
			return fmt.Errorf("ClickHouse combine input %d duplicates an exact table revision", index)
		}
		seenRefs[key] = true
	}
	if len(combine.Outputs) == 0 {
		return fmt.Errorf("ClickHouse combine output schema is required")
	}
	outputIDs, outputNames := make(map[string]bool, len(combine.Outputs)), make(map[string]bool, len(combine.Outputs))
	for index, output := range combine.Outputs {
		if strings.TrimSpace(output.ID) == "" || output.ID != strings.TrimSpace(output.ID) || outputIDs[output.ID] {
			return fmt.Errorf("ClickHouse combine output %d has an empty, untrimmed, or duplicate ID", index)
		}
		if !physicalPathPartPattern.MatchString(output.Name) || output.Name == "__loom_row_id" || output.Name == "auth_resource_path" || output.Name == "project_id" || outputNames[output.Name] {
			return fmt.Errorf("ClickHouse combine output %d has an unsafe, reserved, or duplicate name %q", index, output.Name)
		}
		if strings.TrimSpace(output.ClickHouseType) == "" || strings.TrimSpace(output.LogicalType) == "" {
			return fmt.Errorf("ClickHouse combine output %q requires logical and physical types", output.Name)
		}
		if output.Repeated && !strings.HasPrefix(output.ClickHouseType, "Array(") {
			return fmt.Errorf("ClickHouse combine repeated output %q requires an Array type", output.Name)
		}
		outputIDs[output.ID], outputNames[output.Name] = true, true
	}
	if len(combine.Projections) == 0 {
		return fmt.Errorf("ClickHouse combine projections are required")
	}
	seenProjections := make(map[string]bool, len(combine.Projections))
	for index, projection := range combine.Projections {
		if !outputIDs[projection.OutputColumnID] || strings.TrimSpace(projection.InputColumnID) == "" {
			return fmt.Errorf("ClickHouse combine projection %d has an unknown output or empty input column ID", index)
		}
		if projection.InputIndex < 0 || projection.InputIndex >= len(combine.Inputs) {
			return fmt.Errorf("ClickHouse combine projection %d input index is out of range", index)
		}
		key := fmt.Sprintf("%d\x00%s", projection.InputIndex, projection.OutputColumnID)
		if seenProjections[key] {
			return fmt.Errorf("ClickHouse combine has duplicate projection for input %d and output %q", projection.InputIndex, projection.OutputColumnID)
		}
		seenProjections[key] = true
	}
	for _, output := range combine.Outputs {
		found := false
		for _, projection := range combine.Projections {
			if projection.OutputColumnID == output.ID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("ClickHouse combine output %q is not projected", output.Name)
		}
	}
	switch combine.Kind {
	case PhysicalCombineKeyJoin:
		if len(combine.Inputs) != 2 || len(combine.Keys) == 0 {
			return fmt.Errorf("ClickHouse key join requires two inputs and at least one key pair")
		}
		if combine.JoinType != "INNER" && combine.JoinType != "LEFT" {
			return fmt.Errorf("ClickHouse key join type must be INNER or LEFT")
		}
		if combine.RightMatchPolicy != "PRESERVE_ALL" || combine.MembershipMode != "" {
			return fmt.Errorf("ClickHouse key join requires PRESERVE_ALL and no membership mode")
		}
		if err := validatePhysicalCombineKeys(combine.Keys); err != nil {
			return err
		}
		seenOutput := make(map[string]bool, len(combine.Outputs))
		for _, projection := range combine.Projections {
			if seenOutput[projection.OutputColumnID] {
				return fmt.Errorf("ClickHouse key join output %q has more than one input projection", projection.OutputColumnID)
			}
			seenOutput[projection.OutputColumnID] = true
		}
	case PhysicalCombineAppend:
		if len(combine.Keys) != 0 || combine.JoinType != "" || combine.RightMatchPolicy != "" || combine.MembershipMode != "" {
			return fmt.Errorf("ClickHouse append does not accept key, join, or membership fields")
		}
		for inputIndex := range combine.Inputs {
			for _, output := range combine.Outputs {
				if !seenProjections[fmt.Sprintf("%d\x00%s", inputIndex, output.ID)] {
					return fmt.Errorf("ClickHouse append output %q is not mapped from input %d", output.Name, inputIndex)
				}
			}
		}
	case PhysicalCombineMembership:
		if len(combine.Inputs) != 2 || len(combine.Keys) == 0 {
			return fmt.Errorf("ClickHouse membership requires two inputs and at least one key pair")
		}
		if combine.MembershipMode != "INCLUDE" && combine.MembershipMode != "EXCLUDE" {
			return fmt.Errorf("ClickHouse membership mode must be INCLUDE or EXCLUDE")
		}
		if combine.JoinType != "" || combine.RightMatchPolicy != "" {
			return fmt.Errorf("ClickHouse membership does not accept join policy fields")
		}
		if err := validatePhysicalCombineKeys(combine.Keys); err != nil {
			return err
		}
		for _, projection := range combine.Projections {
			if projection.InputIndex != 0 {
				return fmt.Errorf("ClickHouse membership projections must preserve the left input")
			}
		}
	default:
		return fmt.Errorf("unsupported ClickHouse combine kind %q", combine.Kind)
	}
	return nil
}

func validatePhysicalCombineKeys(keys []PhysicalCombineKey) error {
	seenLeft, seenRight := map[string]bool{}, map[string]bool{}
	for index, key := range keys {
		if strings.TrimSpace(key.LeftColumnID) == "" || strings.TrimSpace(key.RightColumnID) == "" {
			return fmt.Errorf("ClickHouse combine key %d requires left and right column IDs", index)
		}
		if seenLeft[key.LeftColumnID] || seenRight[key.RightColumnID] {
			return fmt.Errorf("ClickHouse combine key IDs must be unique on each side")
		}
		seenLeft[key.LeftColumnID], seenRight[key.RightColumnID] = true, true
	}
	return nil
}

// ResolvedClickHouseTable is populated by the exact revision resolver after
// project, output, schema, scope, and authorization checks succeed.
type ResolvedClickHouseTable struct {
	TableID            string
	RevisionID         string
	OutputID           string
	Recipe             string
	TranslationVersion string
	Project            string
	DatasetGeneration  string
	ReceiptID          string
	SchemaDigest       string
	ScopeDigest        string
	PhysicalTable      string
	Unrestricted       bool
	AuthResourcePaths  []string
	Columns            []ResolvedClickHouseColumn
}

type ResolvedClickHouseColumn struct {
	ID             string
	Name           string
	SemanticPath   string
	LogicalType    string
	ClickHouseType string
	Nullable       bool
	Repeated       bool
}
