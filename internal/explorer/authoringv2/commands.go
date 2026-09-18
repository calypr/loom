package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	CommandCreateTable                  = "CREATE_TABLE"
	CommandDuplicateTable               = "DUPLICATE_TABLE"
	CommandDeleteTable                  = "DELETE_TABLE"
	CommandRenameTable                  = "RENAME_TABLE"
	CommandReorderTables                = "REORDER_TABLES"
	CommandSetTableRoot                 = "SET_TABLE_ROOT"
	CommandApplyTableRootRebase         = "APPLY_TABLE_ROOT_REBASE"
	CommandSetTablePopulation           = "SET_TABLE_POPULATION"
	CommandClearTablePopulation         = "CLEAR_TABLE_POPULATION"
	CommandAddRoute                     = "ADD_ROUTE"
	CommandUpdateRouteEdge              = "UPDATE_ROUTE_EDGE"
	CommandSetRouteMatchMode            = "SET_ROUTE_MATCH_MODE"
	CommandRemoveRoute                  = "REMOVE_ROUTE"
	CommandAddColumn                    = "ADD_COLUMN"
	CommandAddColumnSource              = "ADD_COLUMN_SOURCE"
	CommandUpdateColumn                 = "UPDATE_COLUMN"
	CommandSetColumnContributor         = "SET_COLUMN_CONTRIBUTOR"
	CommandClearColumnContributor       = "CLEAR_COLUMN_CONTRIBUTOR"
	CommandApplyInterpretationCandidate = "APPLY_INTERPRETATION_CANDIDATE"
	CommandUpdateColumnSource           = "UPDATE_COLUMN_SOURCE"
	CommandRemoveColumn                 = "REMOVE_COLUMN"
	CommandResultTableCreated           = "TABLE_CREATED"
	CommandResultTableChanged           = "TABLE_CHANGED"
	CommandResultRouteAdded             = "ROUTE_ADDED"
	CommandResultColumnAdded            = "COLUMN_ADDED"
	InitialPresentationTable            = "TABLE"
	InitialPresentationFilter           = "FILTER"
	InitialPresentationChart            = "CHART"
)

// ApplyCommandsRequest is the browser's mutation envelope. CommandID is an
// idempotency token retained only for the last successfully applied command,
// never a durable authoring identity; retrying an older command is a draft
// conflict after a later command advances the draft.
type ApplyCommandsRequest struct {
	CommandID            string    `json:"commandId"`
	SemanticsVersion     int       `json:"semanticsVersion"`
	SnapshotToken        string    `json:"snapshotToken"`
	ExpectedDraftVersion int64     `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string    `json:"expectedDraftDigest,omitempty"`
	Commands             []Command `json:"commands"`
}

func (r *ApplyCommandsRequest) UnmarshalJSON(raw []byte) error {
	type wire ApplyCommandsRequest
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	*r = ApplyCommandsRequest(decoded)
	return nil
}

type Command struct {
	Type                    string                        `json:"type"`
	OutputID                string                        `json:"outputId,omitempty"`
	SourceOutputID          string                        `json:"sourceOutputId,omitempty"`
	Title                   string                        `json:"title,omitempty"`
	RootNodeID              string                        `json:"rootNodeId,omitempty"`
	SelectionRevisionID     string                        `json:"selectionRevisionId,omitempty"`
	EdgeIDs                 []string                      `json:"edgeIds,omitempty"`
	ParentOccurrenceID      string                        `json:"parentOccurrenceId,omitempty"`
	OccurrenceID            string                        `json:"occurrenceId,omitempty"`
	EdgeID                  string                        `json:"edgeId,omitempty"`
	MatchMode               RouteMatchMode                `json:"matchMode,omitempty"`
	CandidateID             string                        `json:"candidateId,omitempty"`
	ProjectionMode          string                        `json:"projectionMode,omitempty"`
	InitialPresentation     string                        `json:"initialPresentation,omitempty"`
	Column                  string                        `json:"column,omitempty"`
	ColumnValue             *Column                       `json:"columnValue,omitempty"`
	Contributor             *ContributorPredicate         `json:"contributor,omitempty"`
	Source                  *ColumnSource                 `json:"source,omitempty"`
	RowChange               *RowChangeProposal            `json:"rowChange,omitempty"`
	InterpretationCandidate *ApplyInterpretationCandidate `json:"interpretationCandidate,omitempty"`
	OutputIDs               []string                      `json:"outputIds,omitempty"`
}

// ApplyInterpretationCandidate is the closed, receipt-backed payload for an
// interpretation repair. The receipt ID and revision ID are both required:
// the former proves the preview that was shown to the user, while the latter
// prevents replaying that preview with a different immutable revision.
type ApplyInterpretationCandidate struct {
	CandidateReceiptID string `json:"candidateReceiptId"`
	RevisionID         string `json:"revisionId"`
}

func (c *Command) UnmarshalJSON(raw []byte) error {
	type wire Command
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	*c = Command(decoded)
	return nil
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
	if r.SemanticsVersion != CurrentSemanticsVersion {
		return fmt.Errorf("UNSUPPORTED_SEMANTICS_VERSION: semanticsVersion %d is unsupported", r.SemanticsVersion)
	}
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
		SemanticsVersion int       `json:"semanticsVersion"`
		SnapshotToken    string    `json:"snapshotToken"`
		Commands         []Command `json:"commands"`
	}{r.SemanticsVersion, r.SnapshotToken, r.Commands})
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
	case CommandApplyTableRootRebase:
		if c.RowChange == nil {
			return fmt.Errorf("APPLY_TABLE_ROOT_REBASE requires rowChange")
		}
		if err := c.RowChange.validate(); err != nil {
			return err
		}
		if c.OutputID != "" && c.OutputID != c.RowChange.OutputID {
			return fmt.Errorf("APPLY_TABLE_ROOT_REBASE outputId must match rowChange.outputId")
		}
	case CommandSetTablePopulation:
		if !required(c.OutputID, c.SelectionRevisionID) {
			return fmt.Errorf("SET_TABLE_POPULATION requires outputId and selectionRevisionId")
		}
	case CommandClearTablePopulation:
		if !required(c.OutputID) {
			return fmt.Errorf("CLEAR_TABLE_POPULATION requires outputId")
		}
	case CommandAddRoute:
		if !required(c.OutputID, c.ParentOccurrenceID, c.EdgeID) {
			return fmt.Errorf("ADD_ROUTE requires outputId, parentOccurrenceId, and edgeId")
		}
	case CommandUpdateRouteEdge:
		if !required(c.OutputID, c.OccurrenceID, c.EdgeID) || c.OccurrenceID == RootOccurrenceID {
			return fmt.Errorf("UPDATE_ROUTE_EDGE requires a non-root occurrenceId and edgeId")
		}
	case CommandSetRouteMatchMode:
		if !required(c.OutputID, c.OccurrenceID, string(c.MatchMode)) || c.OccurrenceID == RootOccurrenceID {
			return fmt.Errorf("SET_ROUTE_MATCH_MODE requires a non-root occurrenceId and matchMode")
		}
		if c.MatchMode != RouteMatchOptional && c.MatchMode != RouteMatchRequired {
			return fmt.Errorf("SET_ROUTE_MATCH_MODE matchMode must be OPTIONAL or REQUIRED")
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
	case CommandSetColumnContributor:
		if !required(c.OutputID, c.Column) || c.Contributor == nil {
			return fmt.Errorf("SET_COLUMN_CONTRIBUTOR requires outputId, column, and contributor")
		}
		if err := c.Contributor.Validate(); err != nil {
			return fmt.Errorf("contributor: %w", err)
		}
	case CommandClearColumnContributor:
		if !required(c.OutputID, c.Column) {
			return fmt.Errorf("CLEAR_COLUMN_CONTRIBUTOR requires outputId and column")
		}
	case CommandApplyInterpretationCandidate:
		if !required(c.OutputID, c.Column) || c.InterpretationCandidate == nil {
			return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires outputId, column, and interpretationCandidate")
		}
		if !required(c.InterpretationCandidate.CandidateReceiptID, c.InterpretationCandidate.RevisionID) ||
			c.InterpretationCandidate.CandidateReceiptID != strings.TrimSpace(c.InterpretationCandidate.CandidateReceiptID) ||
			c.InterpretationCandidate.RevisionID != strings.TrimSpace(c.InterpretationCandidate.RevisionID) {
			return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires exact candidateReceiptId and revisionId")
		}
	case CommandAddColumnSource:
		if !required(c.OutputID, c.OccurrenceID) || c.Source == nil {
			return fmt.Errorf("ADD_COLUMN_SOURCE requires outputId, occurrenceId, and source")
		}
		if err := c.Source.validate("source"); err != nil {
			return err
		}
		if c.Contributor != nil {
			if err := c.Contributor.Validate(); err != nil {
				return fmt.Errorf("contributor: %w", err)
			}
		}
	case CommandUpdateColumnSource:
		if !required(c.OutputID, c.Column) || c.Source == nil {
			return fmt.Errorf("UPDATE_COLUMN_SOURCE requires outputId, column, and source")
		}
		if err := c.Source.validate("source"); err != nil {
			return err
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
	working, err = MigrateLegacyContributors(working, catalog)
	if err != nil {
		return Workspace{}, nil, err
	}
	working = MigrateLosslessDefaults(working, catalog)
	results := make([]CommandResult, 0, len(commands))
	for index, command := range commands {
		result, applyErr := applyCommand(&working, catalog, commandID, index, command)
		if applyErr != nil {
			return Workspace{}, nil, fmt.Errorf("commands[%d]: %w", index, applyErr)
		}
		results = append(results, result)
	}
	working = working.NormalizePresentationOrders()
	if err := validateWorkspaceContributors(working, catalog); err != nil {
		return Workspace{}, nil, err
	}
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
		current := &workspace.Documents[document]
		if current.RootResourceType == node.ResourceType {
			return result, nil
		}
		if current.RootResourceType != node.ResourceType && documentHasConfiguredMeaning(*current) {
			return result, fmt.Errorf("ROOT_REBASE_REQUIRED: changing table root would discard configured routes, columns, filters, actions, or population")
		}
		current.RootResourceType = node.ResourceType
		current.Route = RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}
		return result, nil
	case CommandApplyTableRootRebase:
		proposal := *command.RowChange
		document := documentIndex(workspace, proposal.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", proposal.OutputID)
		}
		rebased, err := ApplyRowChange(workspace.Documents[document], catalog, proposal)
		if err != nil {
			return result, err
		}
		workspace.Documents[document] = rebased
		result.OutputID = proposal.OutputID
		result.OccurrenceID = RootOccurrenceID
		return result, nil
	case CommandSetTablePopulation:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		if err := setPopulation(&workspace.Documents[document], catalog, command.SelectionRevisionID, command.EdgeIDs); err != nil {
			return result, err
		}
		return result, nil
	case CommandClearTablePopulation:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		workspace.Documents[document].Population = nil
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
		parentDepth, depthFound := routeDepth(&workspace.Documents[document].Route, command.ParentOccurrenceID)
		if !depthFound {
			return result, fmt.Errorf("parent occurrence or edge was not found")
		}
		if catalog.RoutePolicy.MaxHops != nil && parentDepth+1 > *catalog.RoutePolicy.MaxHops {
			return result, fmt.Errorf("ROUTE_TOO_LONG: route exceeds capability route policy (maxHops=%d, hops=%d)", *catalog.RoutePolicy.MaxHops, parentDepth+1)
		}
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if !fromOK || !toOK || from.ResourceType != parent.ResourceType {
			return result, fmt.Errorf("edge %q does not extend occurrence %q", edge.ID, command.ParentOccurrenceID)
		}
		if !catalog.RoutePolicy.AllowRepeatedEdges && routePathUsesRelationship(&workspace.Documents[document].Route, command.ParentOccurrenceID, from.ResourceType, to.ResourceType, edge.Label) {
			return result, fmt.Errorf("edge %q is already used in this route", edge.ID)
		}
		occurrenceID := commandGeneratedID("occ_", commandID, index, command.Type)
		if findRoute(&workspace.Documents[document].Route, occurrenceID) != nil {
			return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
		}
		parent.Children = append(parent.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, Relationship: edge.Label})
		return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
	case CommandUpdateRouteEdge:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		parent, occurrence := findRouteWithParent(&workspace.Documents[document].Route, command.OccurrenceID)
		edge, ok := catalogEdge(catalog, command.EdgeID)
		if parent == nil || occurrence == nil || !ok {
			return result, fmt.Errorf("route occurrence, parent, or edge was not found")
		}
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if !fromOK || !toOK || from.ResourceType != parent.ResourceType || to.ResourceType != occurrence.ResourceType {
			return result, fmt.Errorf("edge %q cannot replace the relationship for occurrence %q", edge.ID, command.OccurrenceID)
		}
		currentEdges := []CatalogEdge{}
		for _, candidate := range catalog.Edges {
			candidateFrom, candidateFromOK := catalogNode(catalog, candidate.FromNodeID)
			candidateTo, candidateToOK := catalogNode(catalog, candidate.ToNodeID)
			if candidateFromOK && candidateToOK && candidateFrom.ResourceType == parent.ResourceType && candidateTo.ResourceType == occurrence.ResourceType && candidate.Label == occurrence.Relationship {
				currentEdges = append(currentEdges, candidate)
			}
		}
		if len(currentEdges) != 1 || currentEdges[0].FromNodeID != edge.FromNodeID || currentEdges[0].ToNodeID != edge.ToNodeID {
			return result, fmt.Errorf("edge %q must preserve the catalog endpoints for occurrence %q", edge.ID, command.OccurrenceID)
		}
		if !catalog.RoutePolicy.AllowRepeatedEdges && (routePathUsesRelationship(&workspace.Documents[document].Route, parent.OccurrenceID, from.ResourceType, to.ResourceType, edge.Label) || routeSubtreeUsesRelationship(occurrence, from.ResourceType, to.ResourceType, edge.Label)) {
			return result, fmt.Errorf("edge %q is already used in this route", edge.ID)
		}
		occurrence.Relationship = edge.Label
		result.OccurrenceID = occurrence.OccurrenceID
		return result, nil
	case CommandSetRouteMatchMode:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		occurrence := findRoute(&workspace.Documents[document].Route, command.OccurrenceID)
		if occurrence == nil || occurrence.OccurrenceID == RootOccurrenceID {
			return result, fmt.Errorf("non-root route occurrence %q was not found", command.OccurrenceID)
		}
		occurrence.MatchMode = command.MatchMode.Normalized()
		result.OccurrenceID = occurrence.OccurrenceID
		return result, nil
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
			if column.OccurrenceID == command.OccurrenceID && column.Source.Kind == SourceField && column.Source.Field != nil && strings.TrimPrefix(column.Source.Field.Path, "root.") == strings.TrimPrefix(candidate.FieldPath, "root.") && strings.EqualFold(column.Source.Field.ProjectionMode, mode) {
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
		source := editableSource(command.OccurrenceID, ColumnSource{Kind: SourceField, Field: &FieldSource{Path: strings.TrimPrefix(candidate.FieldPath, "root."), ProjectionMode: mode}})
		column := Column{Column: columnID, Label: label, LogicalType: candidate.LogicalType, OccurrenceID: command.OccurrenceID, Source: source}
		applyInitialPresentation(&column, presentation, nextTableOrder(workspace.Documents[document].Columns))
		workspace.Documents[document].Columns = append(workspace.Documents[document].Columns, column)
		return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: columnID}, nil
	case CommandAddColumnSource:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		source := editableSource(command.OccurrenceID, *command.Source)
		if err := validateEditableSource(workspace.Documents[document], catalog, command.OccurrenceID, source); err != nil {
			return result, err
		}
		var contributor *ContributorPredicate
		if command.Contributor != nil {
			normalized := command.Contributor.Normalized()
			if err := ValidateContributorForCatalog(workspace.Documents[document], catalog, command.OccurrenceID, source, normalized); err != nil {
				return result, err
			}
			contributor = &normalized
		}
		columnID := commandGeneratedID("col_", command.OutputID, commandID, index, command.Type)
		for _, existing := range workspace.Documents[document].Columns {
			if existing.Column != columnID {
				continue
			}
			if existing.OccurrenceID != command.OccurrenceID || !sourceEqual(existing.Source, source) || !contributorEqual(existing.Contributor, contributor) {
				return result, fmt.Errorf("generated column identity %q conflicts with a different feature", columnID)
			}
			return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: existing.Column}, nil
		}
		label := strings.TrimSpace(command.Title)
		if label == "" {
			label = strings.TrimSpace(source.fieldPath())
		}
		if label == "" {
			label = source.Kind
		}
		column := Column{Column: columnID, Label: label, LogicalType: inferredSourceLogicalType(workspace.Documents[document], catalog, command.OccurrenceID, source, "string"), OccurrenceID: command.OccurrenceID, Source: source, Contributor: contributor}
		applyInitialPresentation(&column, InitialPresentationTable, nextTableOrder(workspace.Documents[document].Columns))
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
			if value.Contributor != nil {
				contributor := value.Contributor.Normalized()
				current.Contributor = &contributor
			}
			return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
		}
		return result, fmt.Errorf("column %q was not found", command.Column)
	case CommandSetColumnContributor:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		for i := range workspace.Documents[document].Columns {
			current := &workspace.Documents[document].Columns[i]
			if current.Column != command.Column {
				continue
			}
			contributor := command.Contributor.Normalized()
			if err := ValidateContributorForCatalog(workspace.Documents[document], catalog, current.OccurrenceID, current.Source, contributor); err != nil {
				return result, err
			}
			current.Contributor = &contributor
			return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
		}
		return result, fmt.Errorf("column %q was not found", command.Column)
	case CommandClearColumnContributor:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		for i := range workspace.Documents[document].Columns {
			current := &workspace.Documents[document].Columns[i]
			if current.Column != command.Column {
				continue
			}
			current.Contributor = nil
			return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
		}
		return result, fmt.Errorf("column %q was not found", command.Column)
	case CommandApplyInterpretationCandidate:
		if strings.TrimSpace(command.OutputID) == "" || strings.TrimSpace(command.Column) == "" ||
			command.InterpretationCandidate == nil ||
			strings.TrimSpace(command.InterpretationCandidate.CandidateReceiptID) == "" ||
			strings.TrimSpace(command.InterpretationCandidate.RevisionID) == "" ||
			command.InterpretationCandidate.CandidateReceiptID != strings.TrimSpace(command.InterpretationCandidate.CandidateReceiptID) ||
			command.InterpretationCandidate.RevisionID != strings.TrimSpace(command.InterpretationCandidate.RevisionID) {
			return result, fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires outputId, column, and exact interpretationCandidate payload")
		}
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		for index := range workspace.Documents[document].Columns {
			current := &workspace.Documents[document].Columns[index]
			if current.Column != command.Column {
				continue
			}
			revisionID := command.InterpretationCandidate.RevisionID
			current.Interpretation = &FeatureInterpretation{
				Kind:   FeatureInterpretationPinned,
				Pinned: &PinnedInterpretation{RevisionID: revisionID},
			}
			// A pinned human interpretation owns the feature meaning. Any inline
			// contributor is stale for this exact candidate and must not survive.
			current.Contributor = nil
			return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
		}
		return result, fmt.Errorf("column %q was not found", command.Column)
	case CommandUpdateColumnSource:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		for i := range workspace.Documents[document].Columns {
			current := &workspace.Documents[document].Columns[i]
			if current.Column != command.Column {
				continue
			}
			source := editableSource(current.OccurrenceID, *command.Source)
			if err := validateEditableSource(workspace.Documents[document], catalog, current.OccurrenceID, source); err != nil {
				return result, err
			}
			current.Source = source
			current.LogicalType = inferredSourceLogicalType(workspace.Documents[document], catalog, current.OccurrenceID, source, current.LogicalType)
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

// editableSource adds the explicit related-resource policy to new child
// selections. A child column is rendered through a first matching resource
// even when its projection mode is VALUE or INDEXED, so publication must not
// silently present that reduction as lossless.
func editableSource(occurrenceID string, source ColumnSource) ColumnSource {
	source = source.Normalized()
	mode := strings.ToUpper(strings.TrimSpace(source.ProjectionMode()))
	if occurrenceID != RootOccurrenceID && source.Kind == SourceField && source.Field != nil && source.Field.RelatedSelection == nil && (mode == "VALUE" || mode == "FIRST" || mode == "INDEXED") {
		source.Field.RelatedSelection = &RelatedSelection{Kind: "first-by-resource-key", Acknowledged: false}
	}
	return source
}

func documentHasConfiguredMeaning(document Document) bool {
	return len(document.Route.Children) != 0 || len(document.Columns) != 0 || len(document.FixedFilters) != 0 || len(document.Actions) != 0 || document.Population != nil
}

func setPopulation(document *Document, catalog CatalogSnapshot, selectionRevisionID string, edgeIDs []string) error {
	if document == nil || strings.TrimSpace(document.RootResourceType) == "" || document.Route.OccurrenceID != RootOccurrenceID {
		return fmt.Errorf("table root is required before attaching a population")
	}
	if catalog.RoutePolicy.MaxHops != nil && len(edgeIDs) > *catalog.RoutePolicy.MaxHops {
		return fmt.Errorf("ROUTE_TOO_LONG: population route exceeds capability route policy (maxHops=%d, hops=%d)", *catalog.RoutePolicy.MaxHops, len(edgeIDs))
	}
	steps := make([]PopulationRouteStep, 0, len(edgeIDs))
	fromType := document.RootResourceType
	seenEdges := map[string]bool{}
	for index, edgeID := range edgeIDs {
		edge, ok := catalogEdge(catalog, edgeID)
		if !ok {
			return fmt.Errorf("population edge %q was not found", edgeID)
		}
		if seenEdges[edge.ID] && !catalog.RoutePolicy.AllowRepeatedEdges {
			return fmt.Errorf("population edge %q is repeated but repeated edges are not allowed", edgeID)
		}
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if !fromOK || !toOK || from.ResourceType != fromType {
			return fmt.Errorf("population edge %q cannot extend %q at step %d", edgeID, fromType, index)
		}
		if from.ResourceType == to.ResourceType && !catalog.RoutePolicy.AllowSelfLoops {
			return fmt.Errorf("population edge %q is a self-loop but self-loops are not allowed", edgeID)
		}
		steps = append(steps, PopulationRouteStep{ResourceType: to.ResourceType, Relationship: edge.Label})
		seenEdges[edge.ID] = true
		fromType = to.ResourceType
	}
	document.Population = &Population{SelectionRevisionID: strings.TrimSpace(selectionRevisionID), Route: steps}
	return nil
}

func sourceEqual(left, right ColumnSource) bool {
	left, right = left.Normalized(), right.Normalized()
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func contributorEqual(left, right *ContributorPredicate) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
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
	// SourceWhere is a private persisted-draft compatibility payload and is
	// intentionally omitted from current JSON. Preserve it for transactional
	// legacy migration and frozen in-memory callers while keeping it unwritable.
	for documentIndex := range value.Documents {
		if documentIndex >= len(result.Documents) {
			break
		}
		for columnIndex := range value.Documents[documentIndex].Columns {
			if columnIndex >= len(result.Documents[documentIndex].Columns) {
				break
			}
			where := value.Documents[documentIndex].Columns[columnIndex].Source.Aggregate
			if where == nil || where.Where == nil || result.Documents[documentIndex].Columns[columnIndex].Source.Aggregate == nil {
				continue
			}
			legacyWhere := *where.Where
			result.Documents[documentIndex].Columns[columnIndex].Source.Aggregate.Where = &legacyWhere
		}
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

func validateWorkspaceContributors(workspace Workspace, catalog CatalogSnapshot) error {
	for documentIndex := range workspace.Documents {
		document := workspace.Documents[documentIndex]
		for columnIndex := range document.Columns {
			column := document.Columns[columnIndex]
			if column.Contributor == nil {
				continue
			}
			if err := ValidateContributorForCatalog(document, catalog, column.OccurrenceID, column.Source, *column.Contributor); err != nil {
				return fmt.Errorf("documents[%d].columns[%d].contributor: %w", documentIndex, columnIndex, err)
			}
		}
	}
	return nil
}

// ValidateContributorForCatalog proves one authored contributor against the
// pinned occurrence and catalog. It is shared by command application and the
// compilation boundary so a stale/foreign candidate cannot become a recipe
// selector.
func ValidateContributorForCatalog(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, predicate ContributorPredicate) error {
	if source.Kind != SourceAggregate || source.Aggregate == nil {
		return fmt.Errorf("contributor is only supported for aggregate sources")
	}
	if err := predicate.Validate(); err != nil {
		return err
	}
	occurrence := findRoute(&document.Route, occurrenceID)
	if occurrence == nil {
		return fmt.Errorf("occurrence %q was not found", occurrenceID)
	}
	candidate, ok := catalogCandidate(catalog, predicate.CandidateID)
	if !ok {
		return fmt.Errorf("contributor candidate %q was not found", predicate.CandidateID)
	}
	node, ok := catalogNode(catalog, candidate.NodeID)
	if !ok || node.ResourceType != occurrence.ResourceType {
		return fmt.Errorf("contributor candidate %q does not belong to occurrence %q", predicate.CandidateID, occurrenceID)
	}
	path := strings.TrimPrefix(strings.Trim(strings.TrimSpace(candidate.FieldPath), "."), "root.")
	canonical := fhirschema.CanonicalizePath(path)
	field, ok := fhirschema.LookupField(occurrence.ResourceType, canonical)
	if !ok {
		return fmt.Errorf("contributor candidate %q selector %q is not present in generated FHIR schema", predicate.CandidateID, candidate.FieldPath)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(occurrence.ResourceType, canonical)
	if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
		return fmt.Errorf("contributor candidate %q selector %q is not a supported scalar", predicate.CandidateID, candidate.FieldPath)
	}
	repeated := metadata.Repeated || strings.Contains(field.Path, "[]")
	if repeated {
		if predicate.Quantifier != ContributorAny {
			return fmt.Errorf("repeated contributor candidate %q requires explicit ANY quantifier", predicate.CandidateID)
		}
	} else if predicate.Quantifier != "" {
		return fmt.Errorf("scalar contributor candidate %q forbids quantifier", predicate.CandidateID)
	}
	if predicate.Operator != ContributorEquals {
		return nil
	}
	wantKind := ContributorString
	if strings.EqualFold(strings.TrimSpace(candidate.LogicalType), "code") || canonical == "code" || strings.HasSuffix(canonical, ".code") {
		wantKind = ContributorValueCode
	}
	if predicate.Value == nil || predicate.Value.Kind != wantKind {
		return fmt.Errorf("contributor candidate %q requires %s value", predicate.CandidateID, wantKind)
	}
	return nil
}

func validateEditableSource(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource) error {
	occurrence := findRoute(&document.Route, occurrenceID)
	if occurrence == nil {
		return fmt.Errorf("occurrence %q was not found", occurrenceID)
	}
	path := strings.TrimPrefix(strings.TrimSpace(source.fieldPath()), "root.")
	if source.Kind == SourceProjectID {
		return nil
	}
	if source.Lookup != nil && source.Lookup.Extension != nil && source.Kind == SourceExtensionByURL {
		if _, err := fhirschema.ValidateExtensionBinding(occurrence.ResourceType, *source.Lookup.Extension); err != nil {
			return fmt.Errorf("source extension: %w", err)
		}
		return nil
	}
	if source.Lookup != nil && source.Lookup.Binding != nil && (source.Kind == SourceCodingBySystem || source.Kind == SourceObservationComponentByCode) {
		if _, err := fhirschema.ValidateCorrelatedBinding(occurrence.ResourceType, *source.Lookup.Binding); err != nil {
			return fmt.Errorf("source binding: %w", err)
		}
		// Correlated lookups carry their structural source in Binding. They do
		// not have a legacy field path to resolve against a catalog candidate.
		return nil
	}
	if source.Kind == SourceExtensionByURL || source.Kind == SourceObservationComponentByCode || source.Kind == SourceCodingBySystem {
		return fmt.Errorf("new %s lookup requires an explicit typed binding", source.Kind)
	}
	if path == "" && source.Kind != SourceAggregate {
		return fmt.Errorf("source path is required for %s", source.Kind)
	}
	findCandidate := func(candidatePath string) (CatalogCandidate, bool) {
		candidatePath = strings.TrimPrefix(strings.TrimSpace(candidatePath), "root.")
		for _, candidate := range catalog.Candidates {
			node, ok := catalogNode(catalog, candidate.NodeID)
			if !ok || node.ResourceType != occurrence.ResourceType {
				continue
			}
			if candidatePath == strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") {
				return candidate, true
			}
		}
		return CatalogCandidate{}, false
	}
	if source.Kind == SourceAggregate {
		if path != "" {
			if _, ok := findCandidate(path); !ok {
				return fmt.Errorf("source path %q is not present for occurrence %q", path, occurrenceID)
			}
		}
		if source.Aggregate != nil && source.Aggregate.Temporal != nil {
			timestamp, ok := findCandidate(source.Aggregate.Temporal.TimestampPath)
			if !ok || !strings.EqualFold(timestamp.LogicalType, "date_time") {
				return fmt.Errorf("temporal timestamp path %q is not a date_time field for occurrence %q", source.Aggregate.Temporal.TimestampPath, occurrenceID)
			}
			anchorPath := strings.TrimPrefix(strings.TrimSpace(source.Aggregate.Temporal.AnchorPath), "root.")
			root := findRoute(&document.Route, RootOccurrenceID)
			foundAnchor := false
			if root != nil {
				for _, candidate := range catalog.Candidates {
					node, found := catalogNode(catalog, candidate.NodeID)
					if found && node.ResourceType == root.ResourceType && strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") == anchorPath && strings.EqualFold(candidate.LogicalType, "date_time") {
						foundAnchor = true
						break
					}
				}
			}
			if !foundAnchor {
				return fmt.Errorf("temporal anchor path %q is not a root date_time field", source.Aggregate.Temporal.AnchorPath)
			}
		}
		return nil
	}
	candidate, ok := findCandidate(path)
	if !ok {
		return fmt.Errorf("source path %q is not present for occurrence %q", path, occurrenceID)
	}
	mode := strings.ToUpper(strings.TrimSpace(source.ProjectionMode()))
	if mode == "" {
		mode = "FIRST"
	}
	if !contains(candidate.ProjectionModes, mode) {
		return fmt.Errorf("projection mode %q is not advertised for source path %q", mode, path)
	}
	return nil
}

func inferredSourceLogicalType(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, fallback string) string {
	if source.Kind == SourceProjectID {
		return "string"
	}
	if source.Lookup != nil && source.Lookup.Binding != nil && strings.TrimSpace(source.Lookup.Binding.LogicalType) != "" {
		return strings.TrimSpace(source.Lookup.Binding.LogicalType)
	}
	if source.Lookup != nil && source.Lookup.Extension != nil && strings.TrimSpace(source.Lookup.Extension.LogicalType) != "" {
		return strings.TrimSpace(source.Lookup.Extension.LogicalType)
	}
	if source.Kind == SourceAggregate && source.Aggregate != nil {
		switch strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)) {
		case "COUNT", "COUNT_DISTINCT":
			return "integer"
		case "EXISTS", "CONTAINS_ALL":
			return "boolean"
		}
	}
	occurrence := findRoute(&document.Route, occurrenceID)
	if occurrence != nil {
		path := strings.TrimPrefix(strings.TrimSpace(source.fieldPath()), "root.")
		for _, candidate := range catalog.Candidates {
			node, ok := catalogNode(catalog, candidate.NodeID)
			if ok && node.ResourceType == occurrence.ResourceType && strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") == path && strings.TrimSpace(candidate.LogicalType) != "" {
				return candidate.LogicalType
			}
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "string"
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

func findRouteWithParent(route *RouteNode, occurrenceID string) (*RouteNode, *RouteNode) {
	for i := range route.Children {
		if route.Children[i].OccurrenceID == occurrenceID {
			return route, &route.Children[i]
		}
		if parent, found := findRouteWithParent(&route.Children[i], occurrenceID); found != nil {
			return parent, found
		}
	}
	return nil, nil
}

func routeDepth(route *RouteNode, occurrenceID string) (int, bool) {
	var walk func(*RouteNode, int) (int, bool)
	walk = func(node *RouteNode, depth int) (int, bool) {
		if node.OccurrenceID == occurrenceID {
			return depth, true
		}
		for i := range node.Children {
			if found, ok := walk(&node.Children[i], depth+1); ok {
				return found, true
			}
		}
		return 0, false
	}
	return walk(route, 0)
}

// routePathUsesRelationship checks only the root-to-parent path. Independent
// sibling occurrences may share an edge, while a capability that disallows
// repeated edges still rejects cycles/repeats within one route branch.
func routePathUsesRelationship(route *RouteNode, targetOccurrenceID, fromResourceType, toResourceType, relationship string) bool {
	_, repeated := routePathContainsRelationship(route, targetOccurrenceID, fromResourceType, toResourceType, relationship)
	return repeated
}

func routePathContainsRelationship(route *RouteNode, targetOccurrenceID, fromResourceType, toResourceType, relationship string) (found, repeated bool) {
	if route == nil {
		return false, false
	}
	if route.OccurrenceID == targetOccurrenceID {
		return true, false
	}
	for index := range route.Children {
		child := &route.Children[index]
		childFound, childRepeated := routePathContainsRelationship(child, targetOccurrenceID, fromResourceType, toResourceType, relationship)
		if !childFound {
			continue
		}
		edgeRepeated := route.ResourceType == fromResourceType && child.ResourceType == toResourceType && child.Relationship == relationship
		return true, childRepeated || edgeRepeated
	}
	return false, false
}

func routeSubtreeUsesRelationship(route *RouteNode, fromResourceType, toResourceType, relationship string) bool {
	if route == nil {
		return false
	}
	for index := range route.Children {
		child := &route.Children[index]
		if route.ResourceType == fromResourceType && child.ResourceType == toResourceType && child.Relationship == relationship {
			return true
		}
		if routeSubtreeUsesRelationship(child, fromResourceType, toResourceType, relationship) {
			return true
		}
	}
	return false
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
