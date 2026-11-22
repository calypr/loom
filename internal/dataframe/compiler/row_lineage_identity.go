package compiler

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func decodeConstructionPivotLineageIdentity(rowID string, pivot ir.PhysicalGroupedPivot) ([]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(rowID))
	decoder.UseNumber()
	var identity []json.RawMessage
	if decoder.Decode(&identity) != nil || len(identity) != len(pivot.GroupKeys)+2 {
		return nil, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, false
	}
	var marker, constructionID string
	if json.Unmarshal(identity[0], &marker) != nil || marker != "GROUPED_PIVOT" ||
		json.Unmarshal(identity[1], &constructionID) != nil || constructionID != pivot.ConstructionID {
		return nil, false
	}
	keys := make([]any, len(pivot.GroupKeys))
	for index, key := range pivot.GroupKeys {
		var pair []json.RawMessage
		if json.Unmarshal(identity[index+2], &pair) != nil || len(pair) != 2 {
			return nil, false
		}
		var kind string
		if json.Unmarshal(pair[0], &kind) != nil || kind != key.Kind {
			return nil, false
		}
		value, valid := decodePivotLineageScalar(pair[1], kind)
		if !valid {
			return nil, false
		}
		keys[index] = value
	}
	return keys, true
}

func decodePivotLineageScalar(raw json.RawMessage, kind string) (any, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	switch kind {
	case "STRING":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		return value, true
	case "INTEGER":
		var value json.Number
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		integer, err := value.Int64()
		return integer, err == nil
	case "DECIMAL":
		var value json.Number
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		decimal, err := value.Float64()
		return decimal, err == nil && !math.IsInf(decimal, 0) && !math.IsNaN(decimal)
	case "BOOLEAN":
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		return value, true
	default:
		return nil, false
	}
}

func parseGroupRowsRowID(rowID string) (revisionID, groupID string, canonical bool) {
	decoder := json.NewDecoder(strings.NewReader(rowID))
	decoder.DisallowUnknownFields()
	var identity map[string]string
	if decoder.Decode(&identity) != nil || len(identity) != 2 || identity["group_revision_id"] == "" || identity["group_id"] == "" {
		return "", "", false
	}
	if decoder.Decode(new(any)) == nil {
		return "", "", false
	}
	encoded, err := json.Marshal(identity)
	if err != nil || string(encoded) != rowID {
		return "", "", false
	}
	return identity["group_revision_id"], identity["group_id"], true
}

func parseRelatedExpandRowID(rowID string, constructionID any, preserveEmpty bool) (string, string, string, bool) {
	var fields [][]string
	if err := json.Unmarshal([]byte(rowID), &fields); err != nil || len(fields) != 3 || len(fields[0]) != 2 || len(fields[1]) != 2 || len(fields[2]) == 0 {
		return "", "", "", false
	}
	expectedConstruction, constructionOK := constructionID.(string)
	if fields[0][0] != "input" || fields[0][1] == "" || fields[1][0] != "construction" ||
		!constructionOK || fields[1][1] != expectedConstruction {
		return "", "", "", false
	}
	if len(fields[2]) == 1 {
		if fields[2][0] == "empty" && preserveEmpty {
			return fields[0][1], "", "EMPTY", true
		}
		return "", "", "", false
	}
	if len(fields[2]) != 2 {
		return "", "", "", false
	}
	terminalID := fields[2][1]
	if fields[2][0] != "related" || terminalID == "" {
		return "", "", "", false
	}
	collection, key, ok := strings.Cut(terminalID, "/")
	if !ok || collection == "" || key == "" {
		return "", "", "", false
	}
	return fields[0][1], terminalID, "RELATED", true
}

type decodedRelatedLineageStage struct {
	StageID    string
	RowID      string
	TerminalID string
	RowKind    string
	StageIndex int
}

func relatedLineageStageBindKeys(stageIndex int) (rowIDBindKey, terminalIDBindKey string) {
	return fmt.Sprintf("row_lineage_stage_%d_row_id", stageIndex), fmt.Sprintf("row_lineage_stage_%d_terminal_id", stageIndex)
}

func decodeRelatedRowLineageIdentity(rowID string, stages []ir.PhysicalConstructionStage, bindVars map[string]any) (string, []decodedRelatedLineageStage, bool) {
	current := rowID
	backward := make([]decodedRelatedLineageStage, 0)
	for index := len(stages) - 1; index >= 0; index-- {
		stage := stages[index]
		if stage.Kind != ir.PhysicalStageRelatedExpandOp {
			continue
		}
		related := stage.RelatedExpand
		if related == nil {
			return "", nil, false
		}
		parent, terminalID, rowKind, ok := parseRelatedExpandRowID(
			current, bindVars[related.ConstructionIDBindKey],
			related.EmptyPolicy == ir.PhysicalUnnestPreserveParent,
		)
		if !ok {
			return "", nil, false
		}
		backward = append(backward, decodedRelatedLineageStage{
			StageID: stage.ID, RowID: current, TerminalID: terminalID, RowKind: rowKind, StageIndex: index,
		})
		current = parent
	}
	if current == "" || len(backward) == 0 {
		return "", nil, false
	}
	for left, right := 0, len(backward)-1; left < right; left, right = left+1, right-1 {
		backward[left], backward[right] = backward[right], backward[left]
	}
	return current, backward, true
}
