package explorer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Catalog is assembled server-side from scoped Loom catalog discovery. The
// browser receives only opaque IDs; source selectors never cross this seam.
type Catalog struct {
	Nodes      []CatalogNode
	Edges      []CatalogEdge
	Selections map[string]CatalogSelection
}

type CatalogNode struct {
	ID           string
	ResourceType string
}

type CatalogEdge struct{ ID, FromNodeID, ToNodeID, Label string }

type CatalogSelection struct {
	ID                    string
	NodeID                string
	FieldRef              string
	Select                string
	LogicalType           string
	Cardinality           string
	ProjectionModes       []string
	DefaultProjectionMode string
	FilterOperators       []string
	ChartOperations       []string
	Filterable            bool
	Chartable             bool
}

// DiscoveryResult makes incomplete discovery explicit. Production catalog
// adapters must never silently translate an upstream failure into an empty,
// apparently complete catalog.
type DiscoveryResult[T any] struct {
	Values      []T
	Complete    bool
	Truncated   bool
	Diagnostics []Diagnostic
}
type CatalogSnapshot struct {
	Project, Generation, AuthorizationScopeDigest string
	Catalog                                       Catalog
	ResolvedSchemaDigest                          string
	Complete                                      bool
	Truncated                                     bool
	Diagnostics                                   []Diagnostic
	Token                                         string
}

func NewCatalogSnapshot(project, generation, scope string, catalog Catalog, complete, truncated bool, diagnostics []Diagnostic) (CatalogSnapshot, error) {
	return NewCatalogSnapshotWithSchema(project, generation, scope, "", catalog, complete, truncated, diagnostics)
}

// NewCatalogSnapshotWithSchema is the compatibility snapshot constructor used
// while V1 authoring documents are migrated to compiler-owned capabilities.
// The schema digest is part of the content address: attaching it after token
// construction would allow a generated-schema change to reuse stale opaque
// node, edge, and selection IDs.
func NewCatalogSnapshotWithSchema(project, generation, scope, schemaDigest string, catalog Catalog, complete, truncated bool, diagnostics []Diagnostic) (CatalogSnapshot, error) {
	nodes := append([]CatalogNode(nil), catalog.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	edges := append([]CatalogEdge(nil), catalog.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	selectionIDs := make([]string, 0, len(catalog.Selections))
	for id := range catalog.Selections {
		selectionIDs = append(selectionIDs, id)
	}
	sort.Strings(selectionIDs)
	identity := struct {
		Project, Generation, Scope, SchemaDigest string
		Nodes                                    []CatalogNode
		Edges                                    []CatalogEdge
		Selections                               []CatalogSelection
		Complete, Truncated                      bool
	}{Project: project, Generation: generation, Scope: scope, SchemaDigest: schemaDigest, Nodes: nodes, Edges: edges, Complete: complete, Truncated: truncated}
	for _, id := range selectionIDs {
		identity.Selections = append(identity.Selections, catalog.Selections[id])
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	sum := sha256.Sum256(raw)
	return CatalogSnapshot{Project: project, Generation: generation, AuthorizationScopeDigest: scope, Catalog: catalog, ResolvedSchemaDigest: schemaDigest, Complete: complete, Truncated: truncated, Diagnostics: append([]Diagnostic(nil), diagnostics...), Token: "sha256:" + hex.EncodeToString(sum[:])}, nil
}
func (s CatalogSnapshot) ValidateToken(token string) error {
	if token == "" || token != s.Token {
		return ErrStaleCatalog
	}
	if !s.Complete || s.Truncated {
		return ErrIncompleteCatalog
	}
	return nil
}
