package dataframe

// This file is an opt-in experiment harness, not a production lowering. It
// discovers routes from fhir_edge, resolves them through generated schema
// metadata, and compares four generic AQL shapes without touching compiler IR.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/fhirschema"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type endpointExperimentMode string

const (
	endpointNativeShared        endpointExperimentMode = "native_shared"
	endpointNativeIndependent   endpointExperimentMode = "native_independent"
	endpointExplicitShared      endpointExperimentMode = "explicit_shared"
	endpointExplicitIndependent endpointExperimentMode = "explicit_independent"
)

type endpointExperimentRoute struct {
	ParentType string `json:"parent_type"`
	Label      string `json:"label"`
	TargetType string `json:"target_type"`
	EdgeCount  int    `json:"edge_count"`
	Route      storageRoute
}

type endpointExperimentGroup struct {
	ParentType string
	Label      string
	Direction  PhysicalTraversalDirection
	Routes     []endpointExperimentRoute
}

type endpointRouteRow struct {
	FromType string `json:"from_type"`
	ToType   string `json:"to_type"`
	Label    string `json:"label"`
	Count    int    `json:"edge_count"`
}

type endpointExperimentRun struct {
	Mode          endpointExperimentMode  `json:"mode"`
	QueryHash     string                  `json:"query_hash"`
	ResultHash    string                  `json:"result_hash"`
	Rows          int                     `json:"rows"`
	Bytes         int                     `json:"bytes"`
	WarmSeconds   []float64               `json:"warm_seconds"`
	MedianSeconds float64                 `json:"median_seconds"`
	MinSeconds    float64                 `json:"min_seconds"`
	Explain       arangoExplainExperiment `json:"explain"`
	Profile       arangoProfileExperiment `json:"profile"`
}

type arangoExplainExperiment struct {
	FullScans []arangostore.ExplainCollectionScan `json:"full_scans"`
	Indexes   []arangostore.ExplainIndexSummary   `json:"indexes"`
	Plans     []arangostore.ExplainPlanEstimate   `json:"plans"`
}

type arangoProfileExperiment struct {
	ScannedFull  int                              `json:"scanned_full"`
	ScannedIndex int                              `json:"scanned_index"`
	PeakMemory   uint64                           `json:"peak_memory"`
	Runtime      float64                          `json:"profile_runtime_seconds"`
	TopNodes     []arangostore.ProfileNodeSummary `json:"top_nodes"`
	TopItemNodes []arangostore.ProfileNodeSummary `json:"top_item_nodes"`
}

func TestRenderEndpointExperimentQueryShapes(t *testing.T) {
	group := endpointExperimentGroup{
		ParentType: "RootCollection",
		Label:      "generated_label",
		Direction:  PhysicalInbound,
		Routes: []endpointExperimentRoute{
			{ParentType: "RootCollection", Label: "generated_label", TargetType: "TargetA"},
			{ParentType: "RootCollection", Label: "generated_label", TargetType: "TargetB"},
		},
	}
	for _, mode := range []endpointExperimentMode{endpointNativeShared, endpointNativeIndependent, endpointExplicitShared, endpointExplicitIndependent} {
		query, bindVars, err := renderEndpointExperimentQuery(group, "project", 1000, mode)
		if err != nil {
			t.Fatalf("render %s: %v", mode, err)
		}
		if !strings.Contains(query, "@@root_collection") || !strings.Contains(query, "@@edge_collection") {
			t.Fatalf("render %s omitted collection binds:\n%s", mode, query)
		}
		if strings.Contains(query, "TargetA") || strings.Contains(query, "TargetB") {
			t.Fatalf("render %s interpolated a target type:\n%s", mode, query)
		}
		if mode == endpointNativeShared || mode == endpointExplicitShared {
			if !strings.Contains(query, "IN @target_types") {
				t.Fatalf("render %s omitted shared type predicate:\n%s", mode, query)
			}
		} else if !strings.Contains(query, "== @target_type_0") || !strings.Contains(query, "== @target_type_1") {
			t.Fatalf("render %s omitted independent type predicates:\n%s", mode, query)
		}
		if mode == endpointNativeShared || mode == endpointExplicitShared {
			if _, ok := bindVars["target_types"]; !ok {
				t.Fatalf("render %s omitted target_types bind", mode)
			}
		} else if _, ok := bindVars["target_types"]; ok {
			t.Fatalf("render %s retained unused target_types bind", mode)
		}
	}
}

// TestEndpointStrategyMatrixAgainstArango is opt-in because it reads the
// provisioned META database. It never creates indexes or mutates documents.
func TestEndpointStrategyMatrixAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run endpoint strategy experiment")
	}
	url, database, project := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	routes, err := discoverEndpointExperimentRoutes(ctx, client, project)
	if err != nil {
		t.Fatalf("discover endpoint experiment routes: %v", err)
	}
	groups := groupEndpointExperimentRoutes(routes)
	if len(groups) == 0 {
		t.Skip("loaded database has no generated route group with multiple target types")
	}
	sort.SliceStable(groups, func(i, j int) bool { return groupEdgeCount(groups[i]) > groupEdgeCount(groups[j]) })
	if len(groups) > 4 {
		groups = groups[:4]
	}
	limit := experimentLimit()
	for index, group := range groups {
		group := group
		t.Run(fmt.Sprintf("group_%02d_%s_%s", index, group.ParentType, group.Label), func(t *testing.T) {
			runs, err := runEndpointExperimentGroup(ctx, client, project, limit, group)
			if err != nil {
				t.Fatalf("run endpoint strategy matrix: %v", err)
			}
			for _, run := range runs {
				t.Logf("mode=%s query_hash=%s result_hash=%s median=%0.6fs min=%0.6fs rows=%d scanned_index=%d peak_memory=%d indexes=%#v", run.Mode, run.QueryHash, run.ResultHash, run.MedianSeconds, run.MinSeconds, run.Rows, run.Profile.ScannedIndex, run.Profile.PeakMemory, run.Explain.Indexes)
				if len(run.Explain.FullScans) != 0 {
					t.Errorf("mode=%s used full collection scan: %#v", run.Mode, run.Explain.FullScans)
				}
			}
			if err := writeEndpointExperimentEvidence(project, limit, index, group, runs); err != nil {
				t.Fatalf("write endpoint experiment evidence: %v", err)
			}
		})
	}
}

// TestEndpointSingletonStrategyMatrixAgainstArango isolates the nested
// single-target paths that a sibling-union benchmark cannot expose. It uses
// the same generated route discovery, but runs one parent collection at a
// time, comparing native traversal with explicit endpoint equality. This is
// intentionally a route-region benchmark: it does not pretend to reproduce
// the parent-set correlation of the complete GDC dataframe.
func TestEndpointSingletonStrategyMatrixAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run singleton endpoint experiment")
	}
	url, database, project := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	routes, err := discoverEndpointExperimentRoutes(ctx, client, project)
	if err != nil {
		t.Fatalf("discover endpoint experiment routes: %v", err)
	}
	groups := singletonEndpointExperimentGroups(routes)
	if len(groups) == 0 {
		t.Skip("loaded database has no generated singleton route")
	}
	limit := experimentLimit()
	for index, group := range groups {
		group := group
		t.Run(fmt.Sprintf("route_%02d_%s_%s_%s", index, group.ParentType, group.Label, group.Routes[0].TargetType), func(t *testing.T) {
			runs, err := runEndpointExperimentModes(ctx, client, project, limit, group, []endpointExperimentMode{endpointNativeIndependent, endpointExplicitIndependent})
			if err != nil {
				t.Fatalf("run singleton endpoint strategy matrix: %v", err)
			}
			for _, run := range runs {
				t.Logf("mode=%s query_hash=%s result_hash=%s median=%0.6fs min=%0.6fs rows=%d scanned_index=%d peak_memory=%d indexes=%#v", run.Mode, run.QueryHash, run.ResultHash, run.MedianSeconds, run.MinSeconds, run.Rows, run.Profile.ScannedIndex, run.Profile.PeakMemory, run.Explain.Indexes)
				if len(run.Explain.FullScans) != 0 {
					t.Errorf("mode=%s used full collection scan: %#v", run.Mode, run.Explain.FullScans)
				}
			}
			if err := writeEndpointExperimentEvidenceNamed(project, limit, "singleton", index, group, runs); err != nil {
				t.Fatalf("write singleton endpoint experiment evidence: %v", err)
			}
		})
	}
}

func singletonEndpointExperimentGroups(routes []endpointExperimentRoute) []endpointExperimentGroup {
	if len(routes) == 0 {
		return nil
	}
	// A parent that is itself a target of another generated route is a nested
	// parent in the loaded graph. Prefer those regions, then fall back to the
	// highest fan-out routes when the fixture has no deeper chain.
	targetTypes := map[string]bool{}
	for _, route := range routes {
		targetTypes[route.TargetType] = true
	}
	preferred := make([]endpointExperimentRoute, 0, len(routes))
	for _, route := range routes {
		if targetTypes[route.ParentType] {
			preferred = append(preferred, route)
		}
	}
	if len(preferred) == 0 {
		preferred = append(preferred, routes...)
	}
	sort.SliceStable(preferred, func(i, j int) bool {
		if preferred[i].EdgeCount != preferred[j].EdgeCount {
			return preferred[i].EdgeCount > preferred[j].EdgeCount
		}
		if preferred[i].ParentType != preferred[j].ParentType {
			return preferred[i].ParentType < preferred[j].ParentType
		}
		if preferred[i].Label != preferred[j].Label {
			return preferred[i].Label < preferred[j].Label
		}
		return preferred[i].TargetType < preferred[j].TargetType
	})
	seen := map[string]bool{}
	seenParents := map[string]bool{}
	groups := make([]endpointExperimentGroup, 0, 4)
	// First cover distinct nested parent collections so a high-fanout resource
	// cannot crowd out deeper regions such as Group -> DocumentReference.
	for _, route := range preferred {
		if seenParents[route.ParentType] {
			continue
		}
		seenParents[route.ParentType] = true
		key := fmt.Sprintf("%s\x00%s\x00%s", route.ParentType, route.Label, route.TargetType)
		seen[key] = true
		groups = append(groups, endpointExperimentGroup{ParentType: route.ParentType, Label: route.Label, Direction: route.Route.Direction, Routes: []endpointExperimentRoute{route}})
		if len(groups) == 4 {
			return groups
		}
	}
	for _, route := range preferred {
		key := fmt.Sprintf("%s\x00%s\x00%s", route.ParentType, route.Label, route.TargetType)
		if seen[key] {
			continue
		}
		seen[key] = true
		groups = append(groups, endpointExperimentGroup{ParentType: route.ParentType, Label: route.Label, Direction: route.Route.Direction, Routes: []endpointExperimentRoute{route}})
		if len(groups) == 4 {
			break
		}
	}
	return groups
}

func experimentLimit() int {
	if value := strings.TrimSpace(os.Getenv("DATAFRAME_PROFILE_LIMIT")); value != "" {
		var limit int
		if _, err := fmt.Sscanf(value, "%d", &limit); err == nil && limit > 0 {
			return limit
		}
	}
	return 1000
}

func discoverEndpointExperimentRoutes(ctx context.Context, client *arangostore.Client, project string) ([]endpointExperimentRoute, error) {
	query := `FOR edge IN fhir_edge
  FILTER edge.project == @project
  COLLECT from_type = edge.from_type, to_type = edge.to_type, label = edge.label WITH COUNT INTO edge_count
  RETURN {from_type, to_type, label, edge_count}`
	rows := make([]endpointRouteRow, 0)
	if err := client.QueryRows(ctx, query, 1000, map[string]any{"project": project}, func(row map[string]any) error {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var decoded endpointRouteRow
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return err
		}
		rows = append(rows, decoded)
		return nil
	}); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	out := make([]endpointExperimentRoute, 0, len(rows))
	for _, edge := range rows {
		if !fhirschema.HasResource(edge.FromType) || !fhirschema.HasResource(edge.ToType) || strings.TrimSpace(edge.Label) == "" {
			continue
		}
		// Normal FHIR references are stored child _from -> parent _to. Try the
		// generated parent->child tuple first, then the proven forward tuple.
		candidates := [][2]string{{edge.ToType, edge.FromType}, {edge.FromType, edge.ToType}}
		for _, candidate := range candidates {
			route, err := resolveStorageRoute(candidate[0], edge.Label, candidate[1])
			if err != nil {
				continue
			}
			key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", candidate[0], edge.Label, candidate[1], route.Direction)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, endpointExperimentRoute{ParentType: candidate[0], Label: edge.Label, TargetType: candidate[1], EdgeCount: edge.Count, Route: route})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentType != out[j].ParentType {
			return out[i].ParentType < out[j].ParentType
		}
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].TargetType < out[j].TargetType
	})
	return out, nil
}

func groupEndpointExperimentRoutes(routes []endpointExperimentRoute) []endpointExperimentGroup {
	groups := map[string]endpointExperimentGroup{}
	for _, route := range routes {
		key := fmt.Sprintf("%s\x00%s\x00%s", route.ParentType, route.Label, route.Route.Direction)
		group := groups[key]
		group.ParentType = route.ParentType
		group.Label = route.Label
		group.Direction = route.Route.Direction
		group.Routes = append(group.Routes, route)
		groups[key] = group
	}
	out := make([]endpointExperimentGroup, 0, len(groups))
	for _, group := range groups {
		seen := map[string]bool{}
		unique := group.Routes[:0]
		for _, route := range group.Routes {
			if seen[route.TargetType] {
				continue
			}
			seen[route.TargetType] = true
			unique = append(unique, route)
		}
		group.Routes = unique
		if len(group.Routes) >= 2 {
			out = append(out, group)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentType != out[j].ParentType {
			return out[i].ParentType < out[j].ParentType
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func groupEdgeCount(group endpointExperimentGroup) int {
	total := 0
	for _, route := range group.Routes {
		total += route.EdgeCount
	}
	return total
}

func runEndpointExperimentGroup(ctx context.Context, client *arangostore.Client, project string, limit int, group endpointExperimentGroup) ([]endpointExperimentRun, error) {
	modes := []endpointExperimentMode{endpointNativeShared, endpointNativeIndependent, endpointExplicitShared, endpointExplicitIndependent}
	return runEndpointExperimentModes(ctx, client, project, limit, group, modes)
}

func runEndpointExperimentModes(ctx context.Context, client *arangostore.Client, project string, limit int, group endpointExperimentGroup, modes []endpointExperimentMode) ([]endpointExperimentRun, error) {
	runs := make([]endpointExperimentRun, 0, len(modes))
	for _, mode := range modes {
		query, bindVars, err := renderEndpointExperimentQuery(group, project, limit, mode)
		if err != nil {
			return nil, err
		}
		explain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
		if err != nil {
			return nil, fmt.Errorf("explain %s: %w\nAQL:\n%s", mode, err, query)
		}
		assessment := arangostore.AssessExplainResult(explain)
		resultHashes := make([]string, 0, 5)
		warm := make([]float64, 0, 5)
		rowsCount, bytesCount := 0, 0
		for run := 0; run < 5; run++ {
			started := time.Now()
			rows := make([]map[string]any, 0, limit)
			if err := client.QueryRows(ctx, query, 1000, bindVars, func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			}); err != nil {
				return nil, fmt.Errorf("execute %s run %d: %w", mode, run+1, err)
			}
			warm = append(warm, time.Since(started).Seconds())
			encoded, err := json.Marshal(rows)
			if err != nil {
				return nil, err
			}
			resultHashes = append(resultHashes, hashBytes(encoded))
			if run == 0 {
				rowsCount, bytesCount = len(rows), len(encoded)
			}
		}
		for _, hash := range resultHashes[1:] {
			if hash != resultHashes[0] {
				return nil, fmt.Errorf("mode %s changed result hash between warm runs: %v", mode, resultHashes)
			}
		}
		profile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: query, BindVars: bindVars, BatchSize: 1000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
		if err != nil {
			return nil, fmt.Errorf("profile %s: %w", mode, err)
		}
		profileSummary := arangostore.SummarizeProfile(profile)
		sort.Float64s(warm)
		runs = append(runs, endpointExperimentRun{
			Mode: mode, QueryHash: hashBytes([]byte(query)), ResultHash: resultHashes[0], Rows: rowsCount, Bytes: bytesCount,
			WarmSeconds: warm, MedianSeconds: warm[len(warm)/2], MinSeconds: warm[0],
			Explain: arangoExplainExperiment{FullScans: assessment.FullCollectionScans, Indexes: assessment.Indexes, Plans: assessment.Plans},
			Profile: arangoProfileExperiment{ScannedFull: profileSummary.ScannedFull, ScannedIndex: profileSummary.ScannedIndex, PeakMemory: profileSummary.PeakMemory, Runtime: profileSummary.RuntimeSeconds, TopNodes: topProfileNodesByRuntime(profileSummary.Nodes, 10), TopItemNodes: topProfileNodesByItems(profileSummary.Nodes, 10)},
		})
	}
	return runs, nil
}

func renderEndpointExperimentQuery(group endpointExperimentGroup, project string, limit int, mode endpointExperimentMode) (string, map[string]any, error) {
	if len(group.Routes) == 0 {
		return "", nil, fmt.Errorf("endpoint experiment group requires at least one target route")
	}
	if (mode == endpointNativeShared || mode == endpointExplicitShared) && len(group.Routes) < 2 {
		return "", nil, fmt.Errorf("shared endpoint experiment requires at least two target routes")
	}
	endpoint, targetEndpoint, discriminator, direction := endpointFields(group.Direction)
	types := make([]string, 0, len(group.Routes))
	for _, route := range group.Routes {
		types = append(types, route.TargetType)
	}
	bindVars := map[string]any{
		"@root_collection":                 group.ParentType,
		"@edge_collection":                 "fhir_edge",
		"project":                          project,
		"dataset_generation":               "",
		"auth_resource_paths_unrestricted": true,
		"auth_resource_paths":              []string{},
		"limit":                            limit,
		"label":                            group.Label,
	}
	var childExpr string
	if mode == endpointNativeShared {
		bindVars["target_types"] = types
		childExpr = fmt.Sprintf(`FOR node, edge IN 1..1 %s root @@edge_collection
          FILTER edge.project == @project
          FILTER @dataset_generation == "" OR edge.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR edge.auth_resource_path IN @auth_resource_paths
          FILTER edge.label == @label
          FILTER edge.%s IN @target_types
          FILTER node.project == @project
          FILTER @dataset_generation == "" OR node.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR node.auth_resource_path IN @auth_resource_paths
          FILTER node.resourceType IN @target_types
          SORT node._key
          RETURN node._key`, direction, discriminator)
	} else if mode == endpointExplicitShared {
		bindVars["target_types"] = types
		childExpr = fmt.Sprintf(`FOR edge IN @@edge_collection
          FILTER edge.%s == root._id
          FILTER edge.project == @project
          FILTER @dataset_generation == "" OR edge.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR edge.auth_resource_path IN @auth_resource_paths
          FILTER edge.label == @label
          FILTER edge.%s IN @target_types
          LET node = DOCUMENT(edge.%s)
          FILTER node != null
          FILTER node.project == @project
          FILTER @dataset_generation == "" OR node.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR node.auth_resource_path IN @auth_resource_paths
          FILTER node.resourceType IN @target_types
          SORT node._key
          RETURN node._key`, endpoint, discriminator, targetEndpoint)
	} else {
		children := make([]string, 0, len(group.Routes))
		for index, route := range group.Routes {
			bindKey := fmt.Sprintf("target_type_%d", index)
			bindVars[bindKey] = route.TargetType
			if mode == endpointNativeIndependent {
				children = append(children, fmt.Sprintf(`FOR node, edge IN 1..1 %s root @@edge_collection
          FILTER edge.project == @project
          FILTER @dataset_generation == "" OR edge.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR edge.auth_resource_path IN @auth_resource_paths
          FILTER edge.label == @label
          FILTER edge.%s == @%s
          FILTER node.project == @project
          FILTER @dataset_generation == "" OR node.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR node.auth_resource_path IN @auth_resource_paths
          FILTER node.resourceType == @%s
          SORT node._key
          RETURN node._key`, direction, discriminator, bindKey, bindKey))
			} else {
				children = append(children, fmt.Sprintf(`FOR edge IN @@edge_collection
          FILTER edge.%s == root._id
          FILTER edge.project == @project
          FILTER @dataset_generation == "" OR edge.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR edge.auth_resource_path IN @auth_resource_paths
          FILTER edge.label == @label
          FILTER edge.%s == @%s
          LET node = DOCUMENT(edge.%s)
          FILTER node != null
          FILTER node.project == @project
          FILTER @dataset_generation == "" OR node.dataset_generation == @dataset_generation
          FILTER @auth_resource_paths_unrestricted == true OR node.auth_resource_path IN @auth_resource_paths
          FILTER node.resourceType == @%s
          SORT node._key
          RETURN node._key`, endpoint, discriminator, bindKey, targetEndpoint, bindKey))
			}
		}
		wrapped := make([]string, 0, len(children))
		for _, child := range children {
			wrapped = append(wrapped, "("+child+")")
		}
		combined := wrapped[0]
		for _, child := range wrapped[1:] {
			// APPEND accepts one array at a time. Nesting it keeps this
			// experiment valid for groups with more than two sibling types.
			combined = fmt.Sprintf("APPEND(%s, %s, true)", combined, child)
		}
		childExpr = "SORTED_UNIQUE(" + combined + ")"
	}
	query := fmt.Sprintf(`FOR root IN @@root_collection
  FILTER root.project == @project
  FILTER @dataset_generation == "" OR root.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths
  SORT root._key
  LIMIT @limit
  LET children = (%s)
  RETURN {"_key": root._key, "children": children}`, childExpr)
	return query, bindVars, nil
}

func endpointFields(direction PhysicalTraversalDirection) (endpoint, targetEndpoint, discriminator, aqlDirection string) {
	if direction == PhysicalOutbound {
		return "_from", "_to", "to_type", "OUTBOUND"
	}
	return "_to", "_from", "from_type", "INBOUND"
}

func topProfileNodesByRuntime(nodes []arangostore.ProfileNodeSummary, limit int) []arangostore.ProfileNodeSummary {
	ordered := append([]arangostore.ProfileNodeSummary(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Runtime != ordered[j].Runtime {
			return ordered[i].Runtime > ordered[j].Runtime
		}
		return ordered[i].ID < ordered[j].ID
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}

func topProfileNodesByItems(nodes []arangostore.ProfileNodeSummary, limit int) []arangostore.ProfileNodeSummary {
	ordered := append([]arangostore.ProfileNodeSummary(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Items != ordered[j].Items {
			return ordered[i].Items > ordered[j].Items
		}
		return ordered[i].ID < ordered[j].ID
	})
	if len(nodes) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}

func writeEndpointExperimentEvidence(project string, limit, index int, group endpointExperimentGroup, runs []endpointExperimentRun) error {
	return writeEndpointExperimentEvidenceNamed(project, limit, "group", index, group, runs)
}

func writeEndpointExperimentEvidenceNamed(project string, limit int, prefix string, index int, group endpointExperimentGroup, runs []endpointExperimentRun) error {
	directory := filepath.Join(round3BenchmarkRoot(), "docs", "benchmarks", "round3", "wp1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%s_%02d_%s_%s", prefix, index, group.ParentType, group.Label)
	name = strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(name)
	payload := map[string]any{"generated_at": time.Now().UTC().Format(time.RFC3339Nano), "git_sha": gitSHAForEvidence(), "project": project, "limit": limit, "group": group, "runs": runs}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, name+".json"), append(encoded, '\n'), 0o644)
}

func round3BenchmarkRoot() string {
	directory, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "."
		}
		directory = parent
	}
}

func gitSHAForEvidence() string {
	if value := strings.TrimSpace(os.Getenv("GIT_COMMIT")); value != "" {
		return value
	}
	return "unknown"
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
