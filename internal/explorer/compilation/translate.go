// Package compilation translates the V2 Builder intent into the persistence-
// neutral recipe understood by the dataframe compiler.  It deliberately has
// no HTTP, store, or server dependencies: a capability snapshot is the only
// authority consulted while compiling an intent.
package compilation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

const TranslationVersion = "authoring-v2-native-5"

// Error is a structured translation failure. Stage, Code, Path, and Details
// are intentionally transport-neutral so an HTTP adapter can map it to its
// own diagnostic envelope without parsing an error string.
type Error struct {
	Stage   string
	Code    string
	Path    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "compilation error"
	}
	if e.Path == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + " at " + e.Path + ": " + e.Message
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func fail(stage, code, path, message string, details map[string]any, cause error) error {
	return &Error{Stage: stage, Code: code, Path: path, Message: message, Details: details, Cause: cause}
}

// EmittedColumn is the immutable public output-column contract generated for
// one candidate occurrence. It intentionally mirrors the useful subset of
// explorer.EmittedColumn without importing the parent Explorer package.
type EmittedColumn struct {
	EmissionID     string `json:"emissionId"`
	OutputID       string `json:"outputId"`
	NodeID         string `json:"nodeId,omitempty"`
	SelectionID    string `json:"selectionId,omitempty"`
	CandidateID    string `json:"candidateId,omitempty"`
	OccurrenceID   string `json:"occurrenceId,omitempty"`
	ProjectionMode string `json:"projectionMode"`
	PublicColumn   string `json:"publicColumn"`
	LogicalType    string `json:"logicalType"`
	Filterable     bool   `json:"filterable"`
	Chartable      bool   `json:"chartable"`
}

type IdentityMapping struct {
	OutputID       string   `json:"outputId"`
	CandidateID    string   `json:"candidateId"`
	OccurrenceID   string   `json:"occurrenceId"`
	ProjectionMode string   `json:"projectionMode"`
	EmissionIDs    []string `json:"emissionIds"`
}

// PresentationConfig is compiler-owned presentation data. The Builder may
// provide labels, visibility, and order, but never executable selectors.
type PresentationConfig struct {
	OutputID string               `json:"outputId"`
	Title    string               `json:"title"`
	Columns  []PresentationColumn `json:"columns"`
}
type PresentationColumn struct {
	EmissionID   string `json:"emissionId"`
	PublicColumn string `json:"publicColumn"`
	Label        string `json:"label"`
	Visible      bool   `json:"visible"`
	Order        int    `json:"order"`
	Pinned       bool   `json:"pinned"`
	FilterLabel  string `json:"filterLabel,omitempty"`
	FilterOrder  int    `json:"filterOrder,omitempty"`
	ChartType    string `json:"chartType,omitempty"`
	ChartTitle   string `json:"chartTitle,omitempty"`
	ChartOrder   int    `json:"chartOrder,omitempty"`
}

type OutputColumn = explorer.PublicOutputColumn
type OutputContract = explorer.PublicOutputContract

// Result is a complete, deterministic native compilation artifact.
type Result struct {
	Bundle           recipe.Bundle
	RecipeDigest     string
	EmittedColumns   []EmittedColumn
	IdentityMappings []IdentityMapping
	Presentation     PresentationConfig
	OutputContract   OutputContract
}

type WorkspaceResult struct {
	Bundle           recipe.Bundle
	RecipeDigest     string
	EmittedColumns   []EmittedColumn
	IdentityMappings []IdentityMapping
	Presentations    []PresentationConfig
	OutputContracts  []OutputContract
	Workspace        authoringv2.Workspace
}

func CompileWorkspace(ctx context.Context, project, explorerID string, workspace authoringv2.Workspace, snapshot capability.Snapshot) (WorkspaceResult, error) {
	wire := catalogFromCapability(snapshot, explorerID)
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: wire}).Validate(); err != nil {
		return WorkspaceResult{}, fail("intent", "INVALID_AUTHORING_INTENT", "$.workspace", err.Error(), nil, err)
	}
	workspace = workspace.NormalizePresentationOrders()
	result := WorkspaceResult{Workspace: workspace, Bundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + safeName(project) + "_" + safeName(explorerID), TranslationVersion: TranslationVersion}, EmittedColumns: []EmittedColumn{}, IdentityMappings: []IdentityMapping{}, Presentations: []PresentationConfig{}, OutputContracts: []OutputContract{}}
	for i, document := range workspace.Documents {
		compiled, err := Compile(ctx, project, explorerID, document, snapshot)
		if err != nil {
			return WorkspaceResult{}, fail("compile", "DOCUMENT_COMPILE_FAILED", fmt.Sprintf("$.workspace.documents[%d]", i), err.Error(), nil, err)
		}
		result.Bundle.Outputs = append(result.Bundle.Outputs, compiled.Bundle.Outputs...)
		result.EmittedColumns = append(result.EmittedColumns, compiled.EmittedColumns...)
		result.IdentityMappings = append(result.IdentityMappings, compiled.IdentityMappings...)
		result.Presentations = append(result.Presentations, compiled.Presentation)
		result.OutputContracts = append(result.OutputContracts, compiled.OutputContract)
	}
	if err := result.Bundle.Validate(); err != nil {
		return WorkspaceResult{}, err
	}
	digest, err := result.Bundle.Digest()
	if err != nil {
		return WorkspaceResult{}, err
	}
	result.RecipeDigest = digest
	return result, nil
}

func catalogFromCapability(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	c := authoringv2.CatalogSnapshot{APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: explorerID, SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, ResolvedSchemaDigest: snapshot.Identity.SchemaDigest, SnapshotToken: snapshot.Token, Complete: snapshot.Complete, RoutePolicy: authoringv2.RoutePolicy{Unbounded: snapshot.Policy.Route.MaxHops == 0, AllowRepeatedEdges: snapshot.Policy.Route.AllowsRepeatedEdges, AllowSelfLoops: snapshot.Policy.Route.AllowsSelfLoops}}
	if snapshot.Policy.Route.MaxHops > 0 {
		max := snapshot.Policy.Route.MaxHops
		c.RoutePolicy.MaxHops = &max
	}
	for _, n := range snapshot.Nodes {
		count := n.DocumentCount
		c.Nodes = append(c.Nodes, authoringv2.CatalogNode{ID: n.ID, ResourceType: n.ResourceType, RowRootEligible: n.RowRootEligible, RowGrain: n.RowGrain, Populated: n.Populated, DocumentCount: &count})
	}
	for _, e := range snapshot.Edges {
		c.Edges = append(c.Edges, authoringv2.CatalogEdge{ID: e.ID, FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Label: e.Label, Populated: e.ObservedEdgeCount > 0})
	}
	for _, candidate := range snapshot.Candidates {
		modes := make([]string, len(candidate.ProjectionModes))
		for i, mode := range candidate.ProjectionModes {
			modes[i] = wireProjectionMode(mode)
		}
		defaultMode := ""
		if len(modes) > 0 {
			defaultMode = modes[0]
		}
		c.Candidates = append(c.Candidates, authoringv2.CatalogCandidate{ID: candidate.ID, NodeID: candidate.NodeID, Label: candidate.Label, LogicalType: candidate.LogicalType, Repeated: candidate.Cardinality != "scalar", Filterable: supportsOperation(candidate.SupportedOperations, capability.OperationFilter), Chartable: supportsOperation(candidate.SupportedOperations, capability.OperationChart), ProjectionModes: modes, DefaultProjectionMode: defaultMode, Populated: candidate.Populated})
	}
	return c
}

// Compilation is a descriptive alias for callers that use the domain term
// rather than the translation verb.
type Compilation = Result

// Compile translates one V2 document against the exact capability snapshot.
// It performs no catalog discovery, probing, profiling, or recipe execution.
func Compile(ctx context.Context, project, explorerID string, document authoringv2.Document, snapshot capability.Snapshot) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	project = strings.TrimSpace(project)
	explorerID = strings.TrimSpace(explorerID)
	if project == "" {
		return Result{}, fail("protocol", "PROJECT_REQUIRED", "$.project", "project is required", nil, nil)
	}
	if explorerID == "" {
		return Result{}, fail("protocol", "EXPLORER_REQUIRED", "$.explorerId", "explorerId is required", nil, nil)
	}
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return Result{}, fail("capability", "CAPABILITY_SNAPSHOT_UNUSABLE", "$.snapshot", "capability snapshot is unavailable or stale", map[string]any{"token": snapshot.Token}, err)
	}
	if snapshot.Identity.Project != project {
		return Result{}, fail("capability", "PROJECT_MISMATCH", "$.project", "capability snapshot belongs to a different project", map[string]any{"snapshotProject": snapshot.Identity.Project, "project": project}, nil)
	}
	if err := document.Validate(); err != nil {
		return Result{}, fail("intent", "INVALID_DOCUMENT", "$", err.Error(), nil, err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	if document.UsesSemanticColumns() {
		return compileSemanticDocument(ctx, project, explorerID, document, snapshot)
	}
	occurrences, routeEdges, err := resolveRoute(document, snapshot)
	if err != nil {
		return Result{}, err
	}
	if err := validateSelections(document, snapshot, occurrences); err != nil {
		return Result{}, err
	}

	root := occurrences[0]
	rowGrain, ok := spec.InferRowGrain(root.ResourceType)
	if !ok || !root.RowRootEligible {
		return Result{}, fail("lower", "UNSUPPORTED_ROW_ROOT", "$.rootNodeId", "root node is not an eligible recipe row root", map[string]any{"nodeId": root.ID, "resourceType": root.ResourceType}, nil)
	}
	assignments := make([]assignment, 0, len(document.Selections))
	seenPairs := map[string]bool{}
	for i, selection := range document.Selections {
		occurrenceID := selection.OccurrenceID
		if occurrenceID == "" {
			occurrenceID = occurrences[len(occurrences)-1].ID
		}
		candidate, _ := snapshot.Candidate(selection.CandidateID)
		key := selection.CandidateID + "\x00" + occurrenceID + "\x00" + strings.ToUpper(selection.ProjectionMode)
		if seenPairs[key] {
			return Result{}, fail("intent", "DUPLICATE_SELECTION", fmt.Sprintf("$.selections[%d]", i), "candidate occurrence is selected more than once", map[string]any{"candidateId": selection.CandidateID, "occurrenceId": occurrenceID}, nil)
		}
		seenPairs[key] = true
		assignments = append(assignments, assignment{Selection: selection, Candidate: candidate, OccurrenceID: occurrenceID})
	}
	// Route order is semantic; candidate order within an occurrence is stable
	// by ID so receipt identity does not depend on browser array ordering.
	sort.SliceStable(assignments, func(i, j int) bool {
		ai, aj := occurrenceIndex(occurrences, assignments[i].OccurrenceID), occurrenceIndex(occurrences, assignments[j].OccurrenceID)
		if ai != aj {
			return ai < aj
		}
		return assignments[i].Candidate.ID < assignments[j].Candidate.ID
	})

	emitted := make([]EmittedColumn, 0, len(assignments))
	mappings := make([]IdentityMapping, 0, len(assignments))
	byPair := map[string]*IdentityMapping{}
	fieldsByOccurrence := map[string][]recipe.Field{}
	for _, a := range assignments {
		fieldName := generatedID("c_", a.Candidate.ID+"\x00"+a.OccurrenceID+"\x00"+a.Selection.ProjectionMode)
		public := fieldName
		if occurrence := occurrenceIndex(occurrences, a.OccurrenceID); occurrence > 0 {
			aliases := make([]string, occurrence)
			for i := range aliases {
				aliases[i] = routeAlias(i)
			}
			public = strings.Join(aliases, "__") + "__" + fieldName
		}
		emission := generatedID("em_", document.Output.ID+"\x00"+a.OccurrenceID+"\x00"+a.Candidate.ID+"\x00"+a.Selection.ProjectionMode)
		column := EmittedColumn{EmissionID: emission, OutputID: document.Output.ID, NodeID: a.Candidate.NodeID, SelectionID: a.Candidate.ID, CandidateID: a.Candidate.ID, OccurrenceID: a.OccurrenceID, ProjectionMode: a.Selection.ProjectionMode, PublicColumn: public, LogicalType: a.Candidate.LogicalType, Filterable: supportsOperation(a.Candidate.SupportedOperations, capability.OperationFilter), Chartable: supportsOperation(a.Candidate.SupportedOperations, capability.OperationChart)}
		emitted = append(emitted, column)
		pair := a.Candidate.ID + "\x00" + a.OccurrenceID + "\x00" + a.Selection.ProjectionMode
		if byPair[pair] == nil {
			mappings = append(mappings, IdentityMapping{OutputID: document.Output.ID, CandidateID: a.Candidate.ID, OccurrenceID: a.OccurrenceID, ProjectionMode: a.Selection.ProjectionMode})
			byPair[pair] = &mappings[len(mappings)-1]
		}
		byPair[pair].EmissionIDs = append(byPair[pair].EmissionIDs, emission)
		alias := "root"
		if idx := occurrenceIndex(occurrences, a.OccurrenceID); idx > 0 {
			alias = routeAlias(idx - 1)
		}
		fieldPath := strings.TrimPrefix(strings.TrimSpace(a.Candidate.FieldPath), "root.")
		fieldsByOccurrence[a.OccurrenceID] = append(fieldsByOccurrence[a.OccurrenceID], recipe.Field{Name: fieldName, FieldRef: a.Candidate.FieldPath, Expr: recipe.Expression{Select: alias + "." + fieldPath}, ValueMode: projectionValueMode(a.Selection.ProjectionMode)})
	}

	output := recipe.Output{Name: document.Output.ID, RootResourceType: root.ResourceType, RowGrain: string(rowGrain), Fields: fieldsByOccurrence[authoringv2.RootOccurrenceID]}
	var lowerRoute func(int) []recipe.Traversal
	lowerRoute = func(i int) []recipe.Traversal {
		if i >= len(routeEdges) {
			return nil
		}
		item := recipe.Traversal{
			Name:           recipeName(routeEdges[i].Label, routeEdges[i].ID),
			Alias:          routeAlias(i),
			ToResourceType: occurrences[i+1].ResourceType,
			MatchMode:      recipe.MatchOptional,
			Fields:         fieldsByOccurrence[occurrences[i+1].ID],
			Traversals:     lowerRoute(i + 1),
		}
		return []recipe.Traversal{item}
	}
	output.Traversals = lowerRoute(0)
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + safeName(project) + "_" + safeName(explorerID), TranslationVersion: TranslationVersion, Outputs: []recipe.Output{output}}
	if err := bundle.Validate(); err != nil {
		return Result{}, fail("lower", "INVALID_RECIPE", "$.recipe", err.Error(), nil, err)
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Result{}, fail("lower", "RECIPE_DIGEST_FAILED", "$.recipe", "recipe digest could not be calculated", nil, err)
	}
	presentation, err := makePresentation(document, emitted, snapshot)
	if err != nil {
		return Result{}, err
	}
	contract := OutputContract{OutputID: document.Output.ID}
	for _, c := range emitted {
		contract.Columns = append(contract.Columns, OutputColumn{Column: c.PublicColumn, Label: presentation.Columns[len(contract.Columns)].Label, LogicalType: c.LogicalType, Filterable: c.Filterable, Chartable: c.Chartable})
	}
	return Result{Bundle: bundle, RecipeDigest: digest, EmittedColumns: emitted, IdentityMappings: mappings, Presentation: presentation, OutputContract: contract}, nil
}

type occurrence struct {
	ID, NodeID, ResourceType string
	RowRootEligible          bool
}
type assignment struct {
	Selection    authoringv2.Selection
	Candidate    capability.Candidate
	OccurrenceID string
}

func validateSnapshot(s capability.Snapshot) error {
	nodes := map[string]capability.Node{}
	for i, n := range s.Nodes {
		if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.ResourceType) == "" {
			return fail("capability", "INVALID_NODE", fmt.Sprintf("$.snapshot.nodes[%d]", i), "node id and resourceType are required", nil, nil)
		}
		if _, ok := nodes[n.ID]; ok {
			return fail("capability", "DUPLICATE_NODE", fmt.Sprintf("$.snapshot.nodes[%d]", i), "duplicate node id", map[string]any{"nodeId": n.ID}, nil)
		}
		nodes[n.ID] = n
	}
	edges := map[string]bool{}
	for i, e := range s.Edges {
		if e.ID == "" || nodes[e.FromNodeID].ID == "" || nodes[e.ToNodeID].ID == "" || strings.TrimSpace(e.Label) == "" {
			return fail("capability", "INVALID_EDGE", fmt.Sprintf("$.snapshot.edges[%d]", i), "edge has stale endpoints or empty label", nil, nil)
		}
		if edges[e.ID] {
			return fail("capability", "DUPLICATE_EDGE", fmt.Sprintf("$.snapshot.edges[%d]", i), "duplicate edge id", map[string]any{"edgeId": e.ID}, nil)
		}
		edges[e.ID] = true
	}
	candidates := map[string]bool{}
	for i, c := range s.Candidates {
		if c.ID == "" || nodes[c.NodeID].ID == "" || strings.TrimSpace(c.FieldPath) == "" {
			return fail("capability", "INVALID_CANDIDATE", fmt.Sprintf("$.snapshot.candidates[%d]", i), "candidate has stale node or empty field path", nil, nil)
		}
		if candidates[c.ID] {
			return fail("capability", "DUPLICATE_CANDIDATE", fmt.Sprintf("$.snapshot.candidates[%d]", i), "duplicate candidate id", map[string]any{"candidateId": c.ID}, nil)
		}
		candidates[c.ID] = true
	}
	return nil
}

func resolveRoute(d authoringv2.Document, s capability.Snapshot) ([]occurrence, []capability.Edge, error) {
	nodes := map[string]capability.Node{}
	for _, n := range s.Nodes {
		nodes[n.ID] = n
	}
	root, ok := nodes[d.RootNodeID]
	if !ok {
		return nil, nil, fail("route", "STALE_ROOT_NODE", "$.rootNodeId", "root node is not in the capability snapshot", map[string]any{"nodeId": d.RootNodeID}, nil)
	}
	if !root.RowRootEligible {
		return nil, nil, fail("route", "ROOT_NOT_ELIGIBLE", "$.rootNodeId", "root node is not row-root eligible", nil, nil)
	}
	edges := map[string]capability.Edge{}
	for _, e := range s.Edges {
		edges[e.ID] = e
	}
	occ := []occurrence{{ID: authoringv2.RootOccurrenceID, NodeID: root.ID, ResourceType: root.ResourceType, RowRootEligible: root.RowRootEligible}}
	path := []capability.Edge{}
	seen := map[string]bool{}
	seenOccurrences := map[string]bool{authoringv2.RootOccurrenceID: true}
	current := root.ID
	for i, step := range d.RouteSteps {
		e, ok := edges[step.EdgeID]
		if !ok {
			return nil, nil, fail("route", "STALE_EDGE", fmt.Sprintf("$.routeSteps[%d].edgeId", i), "route edge is not in the capability snapshot", map[string]any{"edgeId": step.EdgeID}, nil)
		}
		if e.FromNodeID != current {
			return nil, nil, fail("route", "DISCONNECTED_ROUTE", fmt.Sprintf("$.routeSteps[%d]", i), "route edge does not start at the current occurrence node", map[string]any{"expectedNodeId": current, "actualNodeId": e.FromNodeID}, nil)
		}
		if seen[e.ID] && !s.Policy.Route.AllowsRepeatedEdges {
			return nil, nil, fail("route", "REPEATED_EDGE_NOT_ALLOWED", fmt.Sprintf("$.routeSteps[%d].edgeId", i), "route policy does not allow repeated edges", nil, nil)
		}
		if e.FromNodeID == e.ToNodeID && !s.Policy.Route.AllowsSelfLoops {
			return nil, nil, fail("route", "SELF_LOOP_NOT_ALLOWED", fmt.Sprintf("$.routeSteps[%d].edgeId", i), "route policy does not allow self-loops", nil, nil)
		}
		seen[e.ID] = true
		id := step.OccurrenceID
		if id == "" {
			id = authoringv2.DerivedOccurrenceID(i)
		}
		if seenOccurrences[id] {
			return nil, nil, fail("route", "DUPLICATE_OCCURRENCE", fmt.Sprintf("$.routeSteps[%d].occurrenceId", i), "route occurrence id is not unique", map[string]any{"occurrenceId": id}, nil)
		}
		seenOccurrences[id] = true
		n := nodes[e.ToNodeID]
		occ = append(occ, occurrence{ID: id, NodeID: n.ID, ResourceType: n.ResourceType, RowRootEligible: n.RowRootEligible})
		path = append(path, e)
		current = e.ToNodeID
	}
	if s.Policy.Route.MaxHops > 0 && len(path) > s.Policy.Route.MaxHops {
		return nil, nil, fail("route", "ROUTE_TOO_LONG", "$.routeSteps", "route exceeds capability route policy", map[string]any{"maxHops": s.Policy.Route.MaxHops, "hops": len(path)}, nil)
	}
	return occ, path, nil
}

func validateSelections(d authoringv2.Document, s capability.Snapshot, occ []occurrence) error {
	byOcc := map[string]occurrence{}
	for _, o := range occ {
		byOcc[o.ID] = o
	}
	tail := occ[len(occ)-1].ID
	for i, sel := range d.Selections {
		c, ok := s.Candidate(sel.CandidateID)
		if !ok {
			return fail("intent", "STALE_CANDIDATE", fmt.Sprintf("$.selections[%d].candidateId", i), "candidate is not in the capability snapshot", map[string]any{"candidateId": sel.CandidateID}, nil)
		}
		id := sel.OccurrenceID
		if id == "" {
			id = tail
		}
		o, ok := byOcc[id]
		if !ok {
			return fail("intent", "STALE_OCCURRENCE", fmt.Sprintf("$.selections[%d].occurrenceId", i), "selection occurrence is not in the route", map[string]any{"occurrenceId": id}, nil)
		}
		if c.NodeID != o.NodeID {
			return fail("intent", "SELECTION_NODE_MISMATCH", fmt.Sprintf("$.selections[%d]", i), "candidate does not belong to the selected route occurrence", map[string]any{"candidateNodeId": c.NodeID, "occurrenceNodeId": o.NodeID}, nil)
		}
		allowed := false
		for _, m := range c.ProjectionModes {
			if strings.EqualFold(wireProjectionMode(m), sel.ProjectionMode) || strings.EqualFold(string(m), sel.ProjectionMode) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fail("intent", "UNSUPPORTED_PROJECTION_MODE", fmt.Sprintf("$.selections[%d].projectionMode", i), "projection mode is not advertised by the capability snapshot", map[string]any{"candidateId": c.ID, "projectionMode": sel.ProjectionMode, "supported": c.ProjectionModes}, nil)
		}
	}
	return nil
}

func occurrenceIndex(all []occurrence, id string) int {
	for i, o := range all {
		if o.ID == id {
			return i
		}
	}
	return -1
}
func routeAlias(i int) string { return fmt.Sprintf("route_%d", i) }
func supportsOperation(items []capability.Operation, want capability.Operation) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
}
func projectionValueMode(mode string) recipe.ValueMode {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case string(capability.ProjectionFirst):
		return recipe.ValueModeFirst
	case "ALL", string(capability.ProjectionArray):
		return recipe.ValueModeAll
	case string(capability.ProjectionDistinctArray):
		return recipe.ValueModeDistinct
	default:
		return recipe.ValueModeAuto
	}
}

func wireProjectionMode(mode capability.ProjectionMode) string {
	switch mode {
	case capability.ProjectionScalar:
		return "VALUE"
	case capability.ProjectionArray, capability.ProjectionDistinctArray:
		return "ALL"
	default:
		return string(mode)
	}
}

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func recipeName(label, id string) string {
	if namePattern.MatchString(label) {
		return label
	}
	return "edge_" + shortHash(id)
}
func safeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	v := strings.Trim(b.String(), "_")
	if v == "" {
		return "x"
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "x_" + v
	}
	return v
}
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}
func generatedID(prefix, s string) string { return prefix + shortHash(s) }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return fail("compile", "CONTEXT_CANCELED", "$", ctx.Err().Error(), nil, ctx.Err())
	default:
		return nil
	}
}

func makePresentation(d authoringv2.Document, emitted []EmittedColumn, snapshot capability.Snapshot) (PresentationConfig, error) {
	candidates := make(map[string]capability.Candidate, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		candidates[candidate.ID] = candidate
	}
	result := PresentationConfig{OutputID: d.Output.ID, Title: d.Output.Title, Columns: make([]PresentationColumn, 0, len(emitted))}
	for i, c := range emitted {
		p := authoringv2.Presentation{}
		key := authoringv2.PresentationKey(c.CandidateID, c.OccurrenceID, c.ProjectionMode)
		if raw, ok := d.Presentation[key]; ok {
			p = raw
		}
		label := c.PublicColumn
		if candidate, ok := candidates[c.CandidateID]; ok {
			label = firstNonEmpty(candidate.Label, candidate.FieldPath, label)
		}
		if strings.TrimSpace(p.Label) != "" {
			label = p.Label
		}
		visible := true
		if p.Visible != nil {
			visible = *p.Visible
		}
		order := i
		if p.Order != nil {
			order = *p.Order
		}
		column := PresentationColumn{EmissionID: c.EmissionID, PublicColumn: c.PublicColumn, Label: label, Visible: visible, Order: order}
		if p.Table != nil {
			column.Pinned = p.Table.Pinned
		}
		if p.Filter != nil {
			column.FilterLabel = p.Filter.Label
			column.FilterOrder = i
			if p.Filter.Order != nil {
				column.FilterOrder = *p.Filter.Order
			}
		}
		if p.Chart != nil {
			column.ChartType, column.ChartTitle = p.Chart.Type, p.Chart.Title
			column.ChartOrder = i
			if p.Chart.Order != nil {
				column.ChartOrder = *p.Chart.Order
			}
		}
		result.Columns = append(result.Columns, column)
	}
	for key := range d.Presentation {
		found := false
		for _, c := range emitted {
			if key == authoringv2.PresentationKey(c.CandidateID, c.OccurrenceID, c.ProjectionMode) {
				found = true
				break
			}
		}
		if !found {
			return PresentationConfig{}, fail("presentation", "STALE_PRESENTATION", "$.presentation."+key, "presentation references an unselected emission", map[string]any{"key": key}, nil)
		}
	}
	return result, nil
}
