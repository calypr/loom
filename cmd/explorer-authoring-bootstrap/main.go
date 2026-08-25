package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/bootstrap"
)

type pinnedCatalog struct {
	Nodes []struct {
		NodeID       string `json:"nodeId"`
		ResourceType string `json:"resourceType"`
	} `json:"nodes"`
	RouteEdges []struct {
		EdgeID     string `json:"edgeId"`
		FromNodeID string `json:"fromNodeId"`
		ToNodeID   string `json:"toNodeId"`
		Label      string `json:"label"`
	} `json:"routeEdges"`
	Candidates []struct {
		CandidateID string `json:"candidateId"`
		NodeID      string `json:"nodeId"`
		FieldRef    string `json:"fieldRef"`
		Select      string `json:"select"`
		LogicalType string `json:"logicalType"`
		Filterable  bool   `json:"filterable"`
		Chartable   bool   `json:"chartable"`
	} `json:"candidates"`
}

func main() {
	configPath := flag.String("config", "", "legacy default ExplorerConfigV2 JSON")
	catalogPath := flag.String("catalog", "", "pinned V1 authoring catalog JSON")
	project := flag.String("project", "", "project identity")
	bundlePath := flag.String("bundle", "", "output canonical authoring bundle JSON")
	reportPath := flag.String("report", "", "output conversion report JSON")
	flag.Parse()
	if *configPath == "" || *catalogPath == "" || *project == "" || *bundlePath == "" || *reportPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	config, err := os.ReadFile(*configPath)
	if err != nil {
		fail(err)
	}
	catalogRaw, err := os.ReadFile(*catalogPath)
	if err != nil {
		fail(err)
	}
	var wire pinnedCatalog
	dec := json.NewDecoder(bytes.NewReader(catalogRaw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		fail(fmt.Errorf("decode pinned catalog: %w", err))
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		fail(fmt.Errorf("pinned catalog has trailing JSON"))
	}
	catalog := explorer.Catalog{Selections: map[string]explorer.CatalogSelection{}}
	for _, node := range wire.Nodes {
		catalog.Nodes = append(catalog.Nodes, explorer.CatalogNode{ID: node.NodeID, ResourceType: node.ResourceType})
	}
	for _, edge := range wire.RouteEdges {
		catalog.Edges = append(catalog.Edges, explorer.CatalogEdge{ID: edge.EdgeID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label})
	}
	for _, candidate := range wire.Candidates {
		catalog.Selections[candidate.CandidateID] = explorer.CatalogSelection{ID: candidate.CandidateID, NodeID: candidate.NodeID, FieldRef: candidate.FieldRef, Select: candidate.Select, LogicalType: candidate.LogicalType, Filterable: candidate.Filterable, Chartable: candidate.Chartable}
	}
	bundle, report, err := bootstrap.ConvertDefault(config, catalog, *project)
	if err != nil {
		writeReport(*reportPath, report)
		fail(err)
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*bundlePath, canonical, 0o644); err != nil {
		fail(err)
	}
	writeReport(*reportPath, report)
}

func writeReport(path string, report bootstrap.Report) {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		fail(err)
	}
}
func fail(err error) { _, _ = fmt.Fprintln(os.Stderr, err); os.Exit(1) }
