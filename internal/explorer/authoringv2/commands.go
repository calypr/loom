package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	CommandCreateTable        = "CREATE_TABLE"
	CommandDuplicateTable     = "DUPLICATE_TABLE"
	CommandDeleteTable        = "DELETE_TABLE"
	CommandRenameTable        = "RENAME_TABLE"
	CommandReorderTables      = "REORDER_TABLES"
	CommandSetTableRoot       = "SET_TABLE_ROOT"
	CommandAddRoute           = "ADD_ROUTE"
	CommandRemoveRoute        = "REMOVE_ROUTE"
	CommandAddColumn          = "ADD_COLUMN"
	CommandUpdateColumn       = "UPDATE_COLUMN"
	CommandRemoveColumn       = "REMOVE_COLUMN"
	CommandResultTableCreated = "TABLE_CREATED"
	CommandResultTableChanged = "TABLE_CHANGED"
	CommandResultRouteAdded   = "ROUTE_ADDED"
	CommandResultColumnAdded  = "COLUMN_ADDED"
	InitialPresentationTable  = "TABLE"
	InitialPresentationFilter = "FILTER"
	InitialPresentationChart  = "CHART"
)

// ApplyCommandsRequest is the browser's mutation envelope. CommandID is an
// idempotency token, never a durable authoring identity.
type ApplyCommandsRequest struct {
	CommandID            string    `json:"commandId"`
	SnapshotToken        string    `json:"snapshotToken"`
	ExpectedDraftVersion int64     `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string    `json:"expectedDraftDigest,omitempty"`
	Commands             []Command `json:"commands"`
}

type Command struct {
	Type                string   `json:"type"`
	OutputID            string   `json:"outputId,omitempty"`
	SourceOutputID      string   `json:"sourceOutputId,omitempty"`
	Title               string   `json:"title,omitempty"`
	RootNodeID          string   `json:"rootNodeId,omitempty"`
	ParentOccurrenceID  string   `json:"parentOccurrenceId,omitempty"`
	OccurrenceID        string   `json:"occurrenceId,omitempty"`
	EdgeID              string   `json:"edgeId,omitempty"`
	CandidateID         string   `json:"candidateId,omitempty"`
	ProjectionMode      string   `json:"projectionMode,omitempty"`
	InitialPresentation string   `json:"initialPresentation,omitempty"`
	Column              string   `json:"column,omitempty"`
	ColumnValue         *Column  `json:"columnValue,omitempty"`
	OutputIDs           []string `json:"outputIds,omitempty"`
}

type CommandResult struct {
	Type         string `json:"type"`
	OutputID     string `json:"outputId,omitempty"`
	TabID        string `json:"tabId,omitempty"`
	OccurrenceID string `json:"occurrenceId,omitempty"`
	Column       string `json:"column,omitempty"`
}

type ApplyCommandsResponse struct {
	CommandID    string          `json:"commandId"`
	Workspace    Workspace       `json:"workspace"`
	DraftVersion int64           `json:"draftVersion"`
	DraftDigest  string          `json:"draftDigest"`
	Results      []CommandResult `json:"results"`
	Diagnostics  []any           `json:"diagnostics"`
}

func (r ApplyCommandsRequest) Validate() error {
	if emptyID(r.CommandID) || strings.TrimSpace(r.SnapshotToken) == "" {
		return fmt.Errorf("commandId and snapshotToken are required")
	}
	if r.ExpectedDraftVersion < 0 {
		return fmt.Errorf("expectedDraftVersion must not be negative")
	}
	if len(r.Commands) == 0 {
		return fmt.Errorf("at least one command is required")
	}
	for i, command := range r.Commands {
		if err := command.validate(); err != nil {
			return fmt.Errorf("commands[%d]: %w", i, err)
		}
	}
	return nil
}

func (r ApplyCommandsRequest) Digest() (string, error) {
	canonical, err := json.Marshal(struct {
		SnapshotToken string    `json:"snapshotToken"`
		Commands      []Command `json:"commands"`
	}{r.SnapshotToken, r.Commands})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c Command) validate() error {
	required := func(values ...string) bool {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return false
			}
		}
		return true
	}
	switch c.Type {
	case CommandCreateTable:
		if !required(c.Title, c.RootNodeID) {
			return fmt.Errorf("CREATE_TABLE requires title and rootNodeId")
		}
	case CommandDuplicateTable:
		if !required(c.SourceOutputID, c.Title) {
			return fmt.Errorf("DUPLICATE_TABLE requires sourceOutputId and title")
		}
	case CommandDeleteTable:
		if !required(c.OutputID) {
			return fmt.Errorf("DELETE_TABLE requires outputId")
		}
	case CommandRenameTable:
		if !required(c.OutputID, c.Title) {
			return fmt.Errorf("RENAME_TABLE requires outputId and title")
		}
	case CommandReorderTables:
		if len(c.OutputIDs) == 0 {
			return fmt.Errorf("REORDER_TABLES requires outputIds")
		}
	case CommandSetTableRoot:
		if !required(c.OutputID, c.RootNodeID) {
			return fmt.Errorf("SET_TABLE_ROOT requires outputId and rootNodeId")
		}
	case CommandAddRoute:
		if !required(c.OutputID, c.ParentOccurrenceID, c.EdgeID) {
			return fmt.Errorf("ADD_ROUTE requires outputId, parentOccurrenceId, and edgeId")
		}
	case CommandRemoveRoute:
		if !required(c.OutputID, c.OccurrenceID) || c.OccurrenceID == RootOccurrenceID {
			return fmt.Errorf("REMOVE_ROUTE requires a non-root occurrenceId")
		}
	case CommandAddColumn:
		if !required(c.OutputID, c.OccurrenceID, c.CandidateID) {
			return fmt.Errorf("ADD_COLUMN requires outputId, occurrenceId, and candidateId")
		}
		if c.InitialPresentation != "" && !contains([]string{InitialPresentationTable, InitialPresentationFilter, InitialPresentationChart}, strings.ToUpper(strings.TrimSpace(c.InitialPresentation))) {
			return fmt.Errorf("ADD_COLUMN initialPresentation must be TABLE, FILTER, or CHART")
		}
	case CommandUpdateColumn:
		if !required(c.OutputID, c.Column) || c.ColumnValue == nil {
			return fmt.Errorf("UPDATE_COLUMN requires outputId, column, and columnValue")
		}
	case CommandRemoveColumn:
		if !required(c.OutputID, c.Column) {
			return fmt.Errorf("REMOVE_COLUMN requires outputId and column")
		}
	default:
		return fmt.Errorf("unsupported command type %q", c.Type)
	}
	return nil
}

// ApplyCommands is a pure reducer over canonical authoring state. It clones
// the input and either returns one fully valid workspace or no mutation.
func ApplyCommands(workspace Workspace, catalog CatalogSnapshot, commandID string, commands []Command) (Workspace, []CommandResult, error) {
	working, err := cloneWorkspace(workspace)
	if err != nil {
		return Workspace{}, nil, err
	}
	results := make([]CommandResult, 0, len(commands))
	for index, command := range commands {
		result, applyErr := applyCommand(&working, catalog, commandID, index, command)
		if applyErr != nil {
			return Workspace{}, nil, fmt.Errorf("commands[%d]: %w", index, applyErr)
		}
		results = append(results, result)
	}
	working = working.NormalizePresentationOrders()
	if err := (BuilderState{APIVersion: APIVersion, Kind: StateKind, LifecycleState: LifecycleReady, Workspace: &working, Catalog: catalog}).Validate(); err != nil {
		return Workspace{}, nil, err
	}
	return working, results, nil
}

func applyCommand(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	switch command.Type {
	case CommandCreateTable:
		node, ok := catalogNode(catalog, command.RootNodeID)
		if !ok || !node.RowRootEligible {
			return result, fmt.Errorf("rootNodeId %q is not an eligible catalog root", command.RootNodeID)
		}
		outputID := commandGeneratedID("out_", commandID, index, command.Type)
		tabID := commandGeneratedID("tab_", commandID, index, command.Type)
		for _, document := range workspace.Documents {
			if document.Output.ID == outputID {
				return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID}, nil
			}
		}
		title := strings.TrimSpace(command.Title)
		workspace.Documents = append(workspace.Documents, Document{Kind: Kind, Output: Output{ID: outputID, Title: title}, RootResourceType: node.ResourceType, Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}, Columns: []Column{}})
		workspace.Tabs = append(workspace.Tabs, Tab{ID: tabID, Title: title, OutputID: outputID, Order: len(workspace.Tabs), Visible: true})
		return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID, OccurrenceID: RootOccurrenceID}, nil
	case CommandDuplicateTable:
		sourceIndex := documentIndex(workspace, command.SourceOutputID)
		if sourceIndex < 0 {
			return result, fmt.Errorf("source output %q was not found", command.SourceOutputID)
		}
		outputID := commandGeneratedID("out_", commandID, index, command.Type)
		tabID := commandGeneratedID("tab_", commandID, index, command.Type)
		copy := workspace.Documents[sourceIndex]
		copy.Output.ID, copy.Output.Title = outputID, strings.TrimSpace(command.Title)
		workspace.Documents = append(workspace.Documents, copy)
		workspace.Tabs = append(workspace.Tabs, Tab{ID: tabID, Title: copy.Output.Title, OutputID: outputID, Order: len(workspace.Tabs), Visible: true})
		return CommandResult{Type: CommandResultTableCreated, OutputID: outputID, TabID: tabID, OccurrenceID: RootOccurrenceID}, nil
	case CommandDeleteTable:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		workspace.Documents = append(workspace.Documents[:document], workspace.Documents[document+1:]...)
		workspace.Tabs = removeOutputTab(workspace.Tabs, command.OutputID)
		cleanupWorkspaceBindings(workspace)
		return result, nil
	case CommandRenameTable:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		title := strings.TrimSpace(command.Title)
		workspace.Documents[document].Output.Title = title
		for i := range workspace.Tabs {
			if workspace.Tabs[i].OutputID == command.OutputID {
				workspace.Tabs[i].Title = title
			}
		}
		return result, nil
	case CommandReorderTables:
		if len(command.OutputIDs) != len(workspace.Documents) {
			return result, fmt.Errorf("outputIds must list every table exactly once")
		}
		positions := map[string]int{}
		seen := map[string]bool{}
		for order, outputID := range command.OutputIDs {
			if documentIndex(workspace, outputID) < 0 || seen[outputID] {
				return result, fmt.Errorf("outputIds contains an unknown or duplicate output")
			}
			seen[outputID] = true
			positions[outputID] = order + 1
		}
		sort.SliceStable(workspace.Tabs, func(i, j int) bool {
			return positions[workspace.Tabs[i].OutputID] < positions[workspace.Tabs[j].OutputID]
		})
		for order := range workspace.Tabs {
			workspace.Tabs[order].Order = order
		}
		return result, nil
	case CommandSetTableRoot:
		document := documentIndex(workspace, command.OutputID)
		node, ok := catalogNode(catalog, command.RootNodeID)
		if document < 0 || !ok || !node.RowRootEligible {
			return result, fmt.Errorf("output or eligible root node was not found")
		}
		workspace.Documents[document].RootResourceType = node.ResourceType
		workspace.Documents[document].Route = RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}
		workspace.Documents[document].Columns = []Column{}
		workspace.Documents[document].FixedFilters = nil
		workspace.Documents[document].Actions = nil
		cleanupWorkspaceBindings(workspace)
		return result, nil
	case CommandAddRoute:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		parent := findRoute(&workspace.Documents[document].Route, command.ParentOccurrenceID)
		edge, ok := catalogEdge(catalog, command.EdgeID)
		if parent == nil || !ok {
			return result, fmt.Errorf("parent occurrence or edge was not found")
		}
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if !fromOK || !toOK || from.ResourceType != parent.ResourceType {
			return result, fmt.Errorf("edge %q does not extend occurrence %q", edge.ID, command.ParentOccurrenceID)
		}
		occurrenceID := commandGeneratedID("occ_", commandID, index, command.Type)
		if findRoute(&workspace.Documents[document].Route, occurrenceID) != nil {
			return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
		}
		parent.Children = append(parent.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, Relationship: edge.Label})
		return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
	case CommandRemoveRoute:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		removed := map[string]bool{}
		if !removeRoute(&workspace.Documents[document].Route, command.OccurrenceID, removed) {
			return result, fmt.Errorf("occurrence %q was not found", command.OccurrenceID)
		}
		columns := workspace.Documents[document].Columns[:0]
		for _, column := range workspace.Documents[document].Columns {
			if !removed[column.OccurrenceID] {
				columns = append(columns, column)
			}
		}
		workspace.Documents[document].Columns = columns
		cleanupDocumentReferences(&workspace.Documents[document])
		cleanupWorkspaceBindings(workspace)
		return result, nil
	case CommandAddColumn:
		document := documentIndex(workspace, command.OutputID)
		candidate, ok := catalogCandidate(catalog, command.CandidateID)
		if document < 0 || !ok {
			return result, fmt.Errorf("output or candidate was not found")
		}
		occurrence := findRoute(&workspace.Documents[document].Route, command.OccurrenceID)
		node, nodeOK := catalogNode(catalog, candidate.NodeID)
		if occurrence == nil || !nodeOK || occurrence.ResourceType != node.ResourceType {
			return result, fmt.Errorf("candidate %q does not belong to occurrence %q", candidate.ID, command.OccurrenceID)
		}
		mode := strings.ToUpper(strings.TrimSpace(command.ProjectionMode))
		if mode == "" {
			mode = candidate.DefaultProjectionMode
		}
		if !contains(candidate.ProjectionModes, mode) {
			return result, fmt.Errorf("projection mode %q is not advertised", mode)
		}
		presentation := strings.ToUpper(strings.TrimSpace(command.InitialPresentation))
		if presentation == "" {
			presentation = InitialPresentationTable
		}
		if presentation == InitialPresentationFilter && !candidate.Filterable {
			return result, fmt.Errorf("candidate %q does not support filters", candidate.ID)
		}
		if presentation == InitialPresentationChart && !candidate.Chartable {
			return result, fmt.Errorf("candidate %q does not support charts", candidate.ID)
		}
		for i := range workspace.Documents[document].Columns {
			column := &workspace.Documents[document].Columns[i]
			if column.OccurrenceID == command.OccurrenceID && column.Source.Kind == SourceField && strings.TrimPrefix(column.Source.FieldPath, "root.") == strings.TrimPrefix(candidate.FieldPath, "root.") && strings.EqualFold(column.Source.ProjectionMode, mode) {
				applyInitialPresentation(column, presentation, nextTableOrder(workspace.Documents[document].Columns))
				return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: column.Column}, nil
			}
		}
		columnID := commandGeneratedID("col_", command.OutputID, command.OccurrenceID, candidate.ID, mode)
		if command.OccurrenceID != RootOccurrenceID {
			columnID = command.OccurrenceID + "__" + columnID
		}
		label := strings.TrimSpace(command.Title)
		if label == "" {
			label = candidate.Label
		}
		column := Column{Column: columnID, Label: label, LogicalType: candidate.LogicalType, OccurrenceID: command.OccurrenceID, Source: ColumnSource{Kind: SourceField, FieldPath: strings.TrimPrefix(candidate.FieldPath, "root."), ProjectionMode: mode}}
		applyInitialPresentation(&column, presentation, nextTableOrder(workspace.Documents[document].Columns))
		workspace.Documents[document].Columns = append(workspace.Documents[document].Columns, column)
		return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: columnID}, nil
	case CommandUpdateColumn:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		for i := range workspace.Documents[document].Columns {
			current := &workspace.Documents[document].Columns[i]
			if current.Column != command.Column {
				continue
			}
			value := command.ColumnValue
			if strings.TrimSpace(value.Label) == "" {
				return result, fmt.Errorf("column label is required")
			}
			current.Label, current.Table, current.Filter, current.Chart = value.Label, value.Table, value.Filter, value.Chart
			return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
		}
		return result, fmt.Errorf("column %q was not found", command.Column)
	case CommandRemoveColumn:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		columns := workspace.Documents[document].Columns[:0]
		found := false
		for _, column := range workspace.Documents[document].Columns {
			if column.Column == command.Column {
				found = true
				continue
			}
			columns = append(columns, column)
		}
		if !found {
			return result, fmt.Errorf("column %q was not found", command.Column)
		}
		workspace.Documents[document].Columns = columns
		cleanupDocumentReferences(&workspace.Documents[document])
		cleanupWorkspaceBindings(workspace)
		return result, nil
	}
	return result, fmt.Errorf("unsupported command type %q", command.Type)
}

func cloneWorkspace(value Workspace) (Workspace, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return Workspace{}, err
	}
	var result Workspace
	if err := json.Unmarshal(raw, &result); err != nil {
		return Workspace{}, err
	}
	return result, nil
}

func commandGeneratedID(prefix string, values ...any) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprint(value)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:12])
}

func documentIndex(workspace *Workspace, outputID string) int {
	for i := range workspace.Documents {
		if workspace.Documents[i].Output.ID == outputID {
			return i
		}
	}
	return -1
}

func catalogNode(catalog CatalogSnapshot, id string) (CatalogNode, bool) {
	for _, value := range catalog.Nodes {
		if value.ID == id {
			return value, true
		}
	}
	return CatalogNode{}, false
}

func catalogEdge(catalog CatalogSnapshot, id string) (CatalogEdge, bool) {
	for _, value := range catalog.Edges {
		if value.ID == id {
			return value, true
		}
	}
	return CatalogEdge{}, false
}

func catalogCandidate(catalog CatalogSnapshot, id string) (CatalogCandidate, bool) {
	for _, value := range catalog.Candidates {
		if value.ID == id {
			return value, true
		}
	}
	return CatalogCandidate{}, false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func findRoute(route *RouteNode, occurrenceID string) *RouteNode {
	if route.OccurrenceID == occurrenceID {
		return route
	}
	for i := range route.Children {
		if found := findRoute(&route.Children[i], occurrenceID); found != nil {
			return found
		}
	}
	return nil
}

func removeRoute(route *RouteNode, occurrenceID string, removed map[string]bool) bool {
	for i := range route.Children {
		if route.Children[i].OccurrenceID == occurrenceID {
			collectOccurrences(route.Children[i], removed)
			route.Children = append(route.Children[:i], route.Children[i+1:]...)
			return true
		}
		if removeRoute(&route.Children[i], occurrenceID, removed) {
			return true
		}
	}
	return false
}

func collectOccurrences(route RouteNode, values map[string]bool) {
	values[route.OccurrenceID] = true
	for _, child := range route.Children {
		collectOccurrences(child, values)
	}
}

func removeOutputTab(tabs []Tab, outputID string) []Tab {
	result := tabs[:0]
	for _, tab := range tabs {
		if tab.OutputID != outputID {
			result = append(result, tab)
		}
	}
	for i := range result {
		result[i].Order = i
	}
	return result
}

func cleanupDocumentReferences(document *Document) {
	columns := map[string]bool{}
	for _, column := range document.Columns {
		columns[column.Column] = true
	}
	filters := document.FixedFilters[:0]
	for _, filter := range document.FixedFilters {
		if columns[filter.Column] {
			filters = append(filters, filter)
		}
	}
	document.FixedFilters = filters
	for i := range document.Actions {
		kept := document.Actions[i].Columns[:0]
		for _, column := range document.Actions[i].Columns {
			if columns[column.Column] {
				kept = append(kept, column)
			}
		}
		document.Actions[i].Columns = kept
	}
}

func cleanupWorkspaceBindings(workspace *Workspace) {
	available := map[string]map[string]bool{}
	for i := range workspace.Documents {
		cleanupDocumentReferences(&workspace.Documents[i])
		available[workspace.Documents[i].Output.ID] = map[string]bool{}
		for _, column := range workspace.Documents[i].Columns {
			available[workspace.Documents[i].Output.ID][column.Column] = true
		}
	}
	for name, bindings := range workspace.SharedFilters {
		kept := bindings[:0]
		for _, binding := range bindings {
			if available[binding.OutputID][binding.Column] {
				kept = append(kept, binding)
			}
		}
		if len(kept) == 0 {
			delete(workspace.SharedFilters, name)
		} else {
			workspace.SharedFilters[name] = kept
		}
	}
}

func applyInitialPresentation(column *Column, presentation string, tableOrder int) {
	switch presentation {
	case InitialPresentationFilter:
		if column.Filter == nil {
			column.Filter = &FilterPresentation{Label: column.Label}
		}
	case InitialPresentationChart:
		if column.Chart == nil {
			column.Chart = &ChartPresentation{Type: "bar", Title: column.Label}
		}
	default:
		visible := true
		if column.Table == nil {
			order := tableOrder
			column.Table = &TablePresentation{Visible: &visible, Order: &order}
			return
		}
		column.Table.Visible = &visible
		if column.Table.Order == nil {
			order := tableOrder
			column.Table.Order = &order
		}
	}
}

func nextTableOrder(columns []Column) int {
	result := 0
	for _, column := range columns {
		if column.Table != nil && column.Table.Order != nil && *column.Table.Order >= result {
			result = *column.Table.Order + 1
		}
	}
	return result
}
