package arango

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// ExplainRequest is the portable JSON body accepted by ArangoDB's AQL
// explain endpoint. It intentionally has no dependency on the Arango driver.
type ExplainRequest struct {
	Query    string         `json:"query"`
	BindVars map[string]any `json:"bindVars,omitempty"`
	Options  ExplainOptions `json:"options,omitempty"`
}

type ExplainOptions struct {
	AllPlans         bool             `json:"allPlans,omitempty"`
	MaxNumberOfPlans int              `json:"maxNumberOfPlans,omitempty"`
	Optimizer        OptimizerOptions `json:"optimizer,omitempty"`
}

type OptimizerOptions struct {
	Rules []string `json:"rules,omitempty"`
}

// ExplainResult models both single-plan and all-plans responses.
type ExplainResult struct {
	Plan      *ExplainPlan     `json:"plan,omitempty"`
	Plans     []ExplainPlan    `json:"plans,omitempty"`
	Warnings  []ExplainWarning `json:"warnings,omitempty"`
	Stats     ExplainStats     `json:"stats,omitempty"`
	Cacheable bool             `json:"cacheable,omitempty"`
}

type ExplainPlan struct {
	Nodes            []ExplainNode       `json:"nodes,omitempty"`
	Rules            []string            `json:"rules,omitempty"`
	Collections      []ExplainCollection `json:"collections,omitempty"`
	EstimatedCost    float64             `json:"estimatedCost,omitempty"`
	EstimatedNrItems float64             `json:"estimatedNrItems,omitempty"`
}

type ExplainNode struct {
	Type             string         `json:"type,omitempty"`
	ID               int64          `json:"id,omitempty"`
	Dependencies     []int64        `json:"dependencies,omitempty"`
	Collection       string         `json:"collection,omitempty"`
	EdgeCollections  []string       `json:"edgeCollections,omitempty"`
	Indexes          ExplainIndexes `json:"indexes,omitempty"`
	EstimatedCost    float64        `json:"estimatedCost,omitempty"`
	EstimatedNrItems float64        `json:"estimatedNrItems,omitempty"`
}

// ExplainIndexes accepts the shapes emitted by different ArangoDB plan nodes.
// Index nodes use an array, while traversal and optimizer nodes can emit a
// single object or an object keyed by an internal index role.
type ExplainIndexes []ExplainIndex

func (indexes *ExplainIndexes) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*indexes = nil
		return nil
	}
	values, err := decodeExplainIndexes(data, true)
	if err != nil {
		return err
	}
	*indexes = values
	return nil
}

// decodeExplainIndexes accepts every index shape emitted by the explain API:
// a direct index object, an array, or nested traversal-index containers such
// as {"base": [...], "levels": {...}}. Nested non-index metadata is ignored;
// a malformed top-level indexes value still returns an error.
func decodeExplainIndexes(data []byte, strict bool) ([]ExplainIndex, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, nil
	}
	switch data[0] {
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		out := make([]ExplainIndex, 0, len(items))
		for _, item := range items {
			values, err := decodeExplainIndexes(item, false)
			if err != nil {
				return nil, err
			}
			out = append(out, values...)
		}
		return out, nil
	case '{':
		var one ExplainIndex
		if err := json.Unmarshal(data, &one); err != nil {
			return nil, err
		}
		if isExplainIndex(one) {
			return []ExplainIndex{one}, nil
		}

		var keyed map[string]json.RawMessage
		if err := json.Unmarshal(data, &keyed); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(keyed))
		for key := range keyed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]ExplainIndex, 0, len(keyed))
		for _, key := range keys {
			values, err := decodeExplainIndexes(keyed[key], false)
			if err != nil {
				return nil, err
			}
			out = append(out, values...)
		}
		return out, nil
	default:
		if strict {
			return nil, fmt.Errorf("unexpected indexes JSON value %q", string(data))
		}
		return nil, nil
	}
}

func isExplainIndex(index ExplainIndex) bool {
	return index.ID != "" || index.Name != "" || index.Type != "" || len(index.Fields) > 0
}

type ExplainIndex struct {
	ID                  string   `json:"id,omitempty"`
	Name                string   `json:"name,omitempty"`
	Type                string   `json:"type,omitempty"`
	Collection          string   `json:"collection,omitempty"`
	Fields              []string `json:"fields,omitempty"`
	Unique              bool     `json:"unique,omitempty"`
	Sparse              bool     `json:"sparse,omitempty"`
	SelectivityEstimate *float64 `json:"selectivityEstimate,omitempty"`
}

type ExplainCollection struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

type ExplainWarning struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type ExplainStats struct {
	PlansCreated    int    `json:"plansCreated,omitempty"`
	RulesExecuted   int    `json:"rulesExecuted,omitempty"`
	RulesSkipped    int    `json:"rulesSkipped,omitempty"`
	PeakMemoryUsage uint64 `json:"peakMemoryUsage,omitempty"`
}

// ExplainIndexUse associates an index with the plan node and collection that
// use it. Collection falls back to the node collection when omitted by Arango.
type ExplainIndexUse struct {
	Plan       int
	NodeID     int64
	NodeType   string
	Collection string
	Index      ExplainIndex
}

type explainEnvelope struct {
	ExplainResult
	Error        bool   `json:"error,omitempty"`
	ErrorNum     int    `json:"errorNum,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Code         int    `json:"code,omitempty"`
}

// ParseExplainResult decodes an Arango explain response, rejects trailing JSON,
// and promotes Arango error envelopes to Go errors.
func ParseExplainResult(data []byte) (ExplainResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var envelope explainEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return ExplainResult{}, fmt.Errorf("decode Arango explain response: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return ExplainResult{}, err
	}
	if envelope.Error {
		return ExplainResult{}, fmt.Errorf("Arango explain error %d (HTTP %d): %s", envelope.ErrorNum, envelope.Code, envelope.ErrorMessage)
	}
	if envelope.Plan == nil && len(envelope.Plans) == 0 {
		return ExplainResult{}, fmt.Errorf("Arango explain response contains no plan")
	}
	return envelope.ExplainResult, nil
}

// ExtractPlanIndexes returns deterministic, deduplicated index uses from the
// single plan followed by any alternative plans.

// explainIndexCollection resolves the collection that owns an index across
// Arango's node shapes. Traversal nodes typically omit `collection` and put
// the edge collection in `edgeCollections`, while ordinary IndexNodes use
// `collection` directly.
func explainIndexCollection(node ExplainNode, index ExplainIndex) string {
	if index.Collection != "" {
		return index.Collection
	}
	if node.Collection != "" {
		return node.Collection
	}
	if len(node.EdgeCollections) == 1 {
		return node.EdgeCollections[0]
	}
	return ""
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing Arango explain data: %w", err)
	}
	return fmt.Errorf("Arango explain response contains trailing JSON")
}
