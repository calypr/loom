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
	TableID        string
	RevisionID     string
	OutputID       string
	PrivateStageID string
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
	return combine.validate(false)
}

// ValidateWithPrivateStage is used only by a typed composite physical plan.
// Standalone combines continue to require exact published table revisions.
func (combine PhysicalClickHouseCombine) ValidateWithPrivateStage() error {
	return combine.validate(true)
}

func (combine PhysicalClickHouseCombine) validate(allowPrivateStage bool) error {
	if len(combine.Inputs) < 2 {
		return fmt.Errorf("ClickHouse combine requires at least two exact inputs")
	}
	seenRefs := make(map[string]bool, len(combine.Inputs))
	privateStages := 0
	for index, input := range combine.Inputs {
		if input.PrivateStageID != "" {
			if !allowPrivateStage {
				return fmt.Errorf("ClickHouse combine input %d cannot reference a private stage outside a composite plan", index)
			}
			if strings.TrimSpace(input.PrivateStageID) != input.PrivateStageID || strings.TrimSpace(input.TableID) != "" || strings.TrimSpace(input.RevisionID) != "" || strings.TrimSpace(input.OutputID) != "" {
				return fmt.Errorf("ClickHouse combine input %d must reference either one private stage or one exact table revision", index)
			}
			privateStages++
			key := "stage\x00" + input.PrivateStageID
			if seenRefs[key] {
				return fmt.Errorf("ClickHouse combine input %d duplicates a private stage reference", index)
			}
			seenRefs[key] = true
			continue
		}
		if strings.TrimSpace(input.TableID) == "" || strings.TrimSpace(input.RevisionID) == "" || strings.TrimSpace(input.OutputID) == "" {
			return fmt.Errorf("ClickHouse combine input %d requires table, revision, and output IDs", index)
		}
		key := input.TableID + "\x00" + input.RevisionID + "\x00" + input.OutputID
		if seenRefs[key] {
			return fmt.Errorf("ClickHouse combine input %d duplicates an exact table revision", index)
		}
		seenRefs[key] = true
	}
	if allowPrivateStage && privateStages != 1 {
		return fmt.Errorf("composite ClickHouse combine requires exactly one private stage input")
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

// PhysicalClickHousePrefix binds a terminal ClickHouse Combine input to the
// exact last typed AQL stage. A restricted prefix is supported only for one
// immutable authorization path; the renderer emits that bound path as hidden
// row metadata instead of guessing it from result rows.
type PhysicalClickHousePrefix struct {
	StageID                 string
	AuthScopeMode           string
	AuthResourcePaths       []string
	IncludeAuthResourcePath bool
	AuthResourcePathBindKey string
}

func (prefix PhysicalClickHousePrefix) Validate(sequence *PhysicalStageSequence, bindVars map[string]any) error {
	if sequence == nil || strings.TrimSpace(prefix.StageID) == "" || prefix.StageID != sequence.FinalStageID {
		return fmt.Errorf("private ClickHouse prefix must reference the exact final AQL stage")
	}
	if sequence.CellTraceReturn != nil {
		return fmt.Errorf("cell-trace plans cannot feed a private ClickHouse Combine")
	}
	if sequence.RowLineageReturn != nil {
		return fmt.Errorf("row-lineage plans cannot feed a private ClickHouse Combine")
	}
	if err := prefix.ValidateScope(); err != nil {
		return err
	}
	paths, pathsOK := bindVars[physicalScopeAuthPathsBind].([]string)
	unrestricted, unrestrictedOK := bindVars[physicalScopeAuthPathsUnrestrictedBind].(bool)
	allowed, allowedOK := bindVars[physicalScopeAllowedBind].(bool)
	if !pathsOK || !samePhysicalStrings(paths, prefix.AuthResourcePaths) || !unrestrictedOK || unrestricted != (prefix.AuthScopeMode == "unrestricted") || !allowedOK || !allowed {
		return fmt.Errorf("private ClickHouse prefix scope differs from the AQL authorization bindings")
	}
	for _, column := range sequence.FinalColumns {
		if column.Name == "auth_resource_path" || column.Name == "project_id" {
			return fmt.Errorf("private ClickHouse prefix final schema declares reserved column %q", column.Name)
		}
	}
	for _, stage := range sequence.Stages {
		if stage.Kind == PhysicalStageGroupOp {
			return fmt.Errorf("GROUP prefixes cannot feed a private ClickHouse Combine until authorization scope is preserved by grouping")
		}
	}
	if prefix.AuthResourcePathBindKey == "" {
		return nil
	}
	value, ok := bindVars[prefix.AuthResourcePathBindKey].(string)
	if !ok || value != prefix.AuthResourcePaths[0] {
		return fmt.Errorf("private ClickHouse prefix authorization bind does not match its exact scope")
	}
	return nil
}

func samePhysicalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (prefix PhysicalClickHousePrefix) ValidateScope() error {
	if strings.TrimSpace(prefix.StageID) == "" || prefix.StageID != strings.TrimSpace(prefix.StageID) {
		return fmt.Errorf("private ClickHouse prefix stage ID is required")
	}
	switch prefix.AuthScopeMode {
	case "restricted":
		if len(prefix.AuthResourcePaths) != 1 || strings.TrimSpace(prefix.AuthResourcePaths[0]) == "" || prefix.AuthResourcePaths[0] != strings.TrimSpace(prefix.AuthResourcePaths[0]) {
			return fmt.Errorf("restricted private ClickHouse prefixes require exactly one immutable authorization path")
		}
		if !prefix.IncludeAuthResourcePath || strings.TrimSpace(prefix.AuthResourcePathBindKey) == "" {
			return fmt.Errorf("restricted private ClickHouse prefixes require a bound row authorization path")
		}
	case "unrestricted":
		if len(prefix.AuthResourcePaths) != 0 || prefix.IncludeAuthResourcePath || prefix.AuthResourcePathBindKey != "" {
			return fmt.Errorf("unrestricted private ClickHouse prefixes cannot carry restricted path metadata")
		}
	default:
		return fmt.Errorf("private ClickHouse prefix requires an explicit supported authorization scope")
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
	PrivateStageID     string
	PrivateArtifact    *ResolvedClickHousePrivateArtifact
}

// ClickHouseArtifactIdentity carries the exact identity used by the private
// artifact writer. Expected and actual values are compared in full before the
// artifact can become a ClickHouse input.
type ClickHouseArtifactIdentity struct {
	ExecutionID       string
	StageID           string
	Project           string
	DatasetGeneration string
	RecipeDigest      string
	PlanDigest        string
	SchemaDigest      string
	ScopeDigest       string
	AuthScopeMode     string
	AuthResourcePaths []string
}

type ResolvedClickHousePrivateArtifact struct {
	ArtifactID    string
	Identity      ClickHouseArtifactIdentity
	PhysicalTable string
	Columns       []ResolvedClickHouseColumn
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
