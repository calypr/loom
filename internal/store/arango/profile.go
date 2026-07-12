package arango

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// ProfileRequest is the parameterized body accepted by ArangoDB's cursor
// endpoint. Profile is intentionally opt-in because profiling adds execution
// overhead and is not appropriate for normal dataframe requests.
type ProfileRequest struct {
	Query     string         `json:"query"`
	BindVars  map[string]any `json:"bindVars,omitempty"`
	BatchSize int            `json:"batchSize,omitempty"`
	Count     bool           `json:"count,omitempty"`
	Options   ProfileOptions `json:"options,omitempty"`
}

type ProfileOptions struct {
	Profile   int              `json:"profile,omitempty"`
	Optimizer OptimizerOptions `json:"optimizer,omitempty"`
}

// ProfileResult is the first cursor response returned by a profiled query.
// ArangoDB places profiling information under extra. Result is retained as
// raw JSON because profile is a diagnostic API and must support arbitrary row
// shapes without coupling this package to dataframe models.
type ProfileResult struct {
	Result       []json.RawMessage `json:"result,omitempty"`
	HasMore      bool              `json:"hasMore,omitempty"`
	ID           string            `json:"id,omitempty"`
	Count        int               `json:"count,omitempty"`
	Cached       bool              `json:"cached,omitempty"`
	Extra        ProfileExtra      `json:"extra,omitempty"`
	Error        bool              `json:"error,omitempty"`
	ErrorNum     int               `json:"errorNum,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	Code         int               `json:"code,omitempty"`
}

type ProfileExtra struct {
	Warnings []ExplainWarning `json:"warnings,omitempty"`
	Stats    ProfileStats     `json:"stats,omitempty"`
	Profile  ProfilePhases    `json:"profile,omitempty"`
	Plan     *ExplainPlan     `json:"plan,omitempty"`
}

type ProfileStats struct {
	WritesExecuted  int           `json:"writesExecuted,omitempty"`
	WritesIgnored   int           `json:"writesIgnored,omitempty"`
	DocumentLookups int           `json:"documentLookups,omitempty"`
	Seeks           int           `json:"seeks,omitempty"`
	ScannedFull     int           `json:"scannedFull,omitempty"`
	ScannedIndex    int           `json:"scannedIndex,omitempty"`
	CursorsCreated  int           `json:"cursorsCreated,omitempty"`
	CursorsRearmed  int           `json:"cursorsRearmed,omitempty"`
	CacheHits       int           `json:"cacheHits,omitempty"`
	HTTPRequests    int           `json:"httpRequests,omitempty"`
	PeakMemoryUsage uint64        `json:"peakMemoryUsage,omitempty"`
	Nodes           []ProfileNode `json:"nodes,omitempty"`
}

type ProfileNode struct {
	ID       int64   `json:"id,omitempty"`
	Calls    int     `json:"calls,omitempty"`
	Items    int     `json:"items,omitempty"`
	Filtered int     `json:"filtered,omitempty"`
	Runtime  float64 `json:"runtime,omitempty"`
}

// ProfilePhases contains the stable phase names emitted by ArangoDB. New
// server versions may add phases; unknown fields are intentionally ignored.
type ProfilePhases struct {
	Initializing           float64 `json:"initializing,omitempty"`
	Parsing                float64 `json:"parsing,omitempty"`
	OptimizingAST          float64 `json:"optimizing ast,omitempty"`
	LoadingCollections     float64 `json:"loading collections,omitempty"`
	InstantiatingPlan      float64 `json:"instantiating plan,omitempty"`
	InstantiatingExecutors float64 `json:"instantiating executors,omitempty"`
	Executing              float64 `json:"executing,omitempty"`
	Finalizing             float64 `json:"finalizing,omitempty"`
}

// ProfileNodeSummary is a deterministic compact view suitable for logs and
// benchmark artifacts. Node type comes from the profiled execution plan.
type ProfileNodeSummary struct {
	ID       int64
	Type     string
	Calls    int
	Items    int
	Filtered int
	Runtime  float64
}

type ProfileSummary struct {
	RuntimeSeconds float64
	Nodes          []ProfileNodeSummary
	ByType         []ProfileNodeGroup
	ScannedFull    int
	ScannedIndex   int
	PeakMemory     uint64
}

type ProfileNodeGroup struct {
	Type     string
	NodeIDs  []int64
	Calls    int
	Items    int
	Filtered int
	Runtime  float64
}

// ParseProfileResult decodes an Arango cursor response and rejects malformed
// or error envelopes. It accepts profile level 1 (phase timings) and level 2
// (plan plus per-node timings).
func ParseProfileResult(data []byte) (ProfileResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var result ProfileResult
	if err := decoder.Decode(&result); err != nil {
		return ProfileResult{}, fmt.Errorf("decode Arango profile response: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return ProfileResult{}, err
	}
	if result.Error {
		return ProfileResult{}, fmt.Errorf("Arango profile error %d (HTTP %d): %s", result.ErrorNum, result.Code, result.ErrorMessage)
	}
	return result, nil
}

// SummarizeProfile joins runtime node statistics to the returned execution
// plan and groups them by node type. Ordering is stable: nodes and groups are
// sorted by descending runtime and then by ID/type.
func SummarizeProfile(result ProfileResult) ProfileSummary {
	summary := ProfileSummary{
		ScannedFull:  result.Extra.Stats.ScannedFull,
		ScannedIndex: result.Extra.Stats.ScannedIndex,
		PeakMemory:   result.Extra.Stats.PeakMemoryUsage,
	}
	for _, node := range result.Extra.Stats.Nodes {
		typ := ""
		if result.Extra.Plan != nil {
			for _, planNode := range result.Extra.Plan.Nodes {
				if planNode.ID == node.ID {
					typ = planNode.Type
					break
				}
			}
		}
		summary.Nodes = append(summary.Nodes, ProfileNodeSummary{ID: node.ID, Type: typ, Calls: node.Calls, Items: node.Items, Filtered: node.Filtered, Runtime: node.Runtime})
		group := -1
		for i := range summary.ByType {
			if summary.ByType[i].Type == typ {
				group = i
				break
			}
		}
		if group < 0 {
			summary.ByType = append(summary.ByType, ProfileNodeGroup{Type: typ})
			group = len(summary.ByType) - 1
		}
		g := &summary.ByType[group]
		g.NodeIDs = append(g.NodeIDs, node.ID)
		g.Calls += node.Calls
		g.Items += node.Items
		g.Filtered += node.Filtered
		g.Runtime += node.Runtime
		summary.RuntimeSeconds += node.Runtime
	}
	sort.SliceStable(summary.Nodes, func(i, j int) bool {
		if summary.Nodes[i].Runtime != summary.Nodes[j].Runtime {
			return summary.Nodes[i].Runtime > summary.Nodes[j].Runtime
		}
		return summary.Nodes[i].ID < summary.Nodes[j].ID
	})
	sort.SliceStable(summary.ByType, func(i, j int) bool {
		if summary.ByType[i].Runtime != summary.ByType[j].Runtime {
			return summary.ByType[i].Runtime > summary.ByType[j].Runtime
		}
		return summary.ByType[i].Type < summary.ByType[j].Type
	})
	for i := range summary.ByType {
		sort.Slice(summary.ByType[i].NodeIDs, func(a, b int) bool { return summary.ByType[i].NodeIDs[a] < summary.ByType[i].NodeIDs[b] })
	}
	return summary
}
