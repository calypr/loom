package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	CommandCreateTable                   = "CREATE_TABLE"
	CommandDuplicateTable                = "DUPLICATE_TABLE"
	CommandDeleteTable                   = "DELETE_TABLE"
	CommandRenameTable                   = "RENAME_TABLE"
	CommandReorderTables                 = "REORDER_TABLES"
	CommandSetTableRoot                  = "SET_TABLE_ROOT"
	CommandApplyTableRootRebase          = "APPLY_TABLE_ROOT_REBASE"
	CommandSetTablePopulation            = "SET_TABLE_POPULATION"
	CommandClearTablePopulation          = "CLEAR_TABLE_POPULATION"
	CommandAddRoute                      = "ADD_ROUTE"
	CommandUpdateRouteEdge               = "UPDATE_ROUTE_EDGE"
	CommandSetRouteMatchMode             = "SET_ROUTE_MATCH_MODE"
	CommandRemoveRoute                   = "REMOVE_ROUTE"
	CommandAddColumn                     = "ADD_COLUMN"
	CommandAddColumnSource               = "ADD_COLUMN_SOURCE"
	CommandUpdateColumn                  = "UPDATE_COLUMN"
	CommandUpdateColumnRowValuePolicy    = "UPDATE_COLUMN_ROW_VALUE_POLICY"
	CommandUpdateConstructionOutput      = "UPDATE_CONSTRUCTION_OUTPUT"
	CommandUpdateColumnTransformation    = "UPDATE_COLUMN_TRANSFORMATION"
	CommandSetColumnContributor          = "SET_COLUMN_CONTRIBUTOR"
	CommandClearColumnContributor        = "CLEAR_COLUMN_CONTRIBUTOR"
	CommandApplyInterpretationCandidate  = "APPLY_INTERPRETATION_CANDIDATE"
	CommandApplyRowDefinitionProposal    = "APPLY_ROW_DEFINITION_PROPOSAL"
	CommandApplyTableShapeProposal       = "APPLY_TABLE_SHAPE_PROPOSAL"
	CommandApplyConstructionProposal     = "APPLY_CONSTRUCTION_PROPOSAL"
	CommandRestoreDraftRevision          = "RESTORE_DRAFT_REVISION"
	CommandUpdateColumnSource            = "UPDATE_COLUMN_SOURCE"
	CommandRemoveColumn                  = "REMOVE_COLUMN"
	CommandAddSemanticSelections         = "ADD_SEMANTIC_SELECTIONS"
	CommandApplyConstructionChoice       = "APPLY_CONSTRUCTION_CHOICE"
	CommandSetFrameSource                = "SET_FRAME_SOURCE"
	CommandReplaceFrameSource            = "REPLACE_FRAME_SOURCE"
	CommandRemoveFrameSource             = "REMOVE_FRAME_SOURCE"
	CommandResultTableCreated            = "TABLE_CREATED"
	CommandResultTableChanged            = "TABLE_CHANGED"
	CommandResultRouteAdded              = "ROUTE_ADDED"
	CommandResultColumnAdded             = "COLUMN_ADDED"
	CommandResultSemanticSelectionsAdded = "SEMANTIC_SELECTIONS_ADDED"
	CommandResultDraftRestored           = "DRAFT_RESTORED"
	SemanticSelectionAdded               = "ADDED"
	SemanticSelectionAlreadyPresent      = "ALREADY_PRESENT"
	InitialPresentationTable             = "TABLE"
	InitialPresentationFilter            = "FILTER"
	InitialPresentationChart             = "CHART"
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
	draftSourceContext   *DraftSourceContext
}

// DraftSourceContext is attached by lifecycle after resolving the request's
// authorized snapshot. It is never accepted from the browser.
type DraftSourceContext struct {
	SnapshotToken            string
	SourceGeneration         string
	AuthorizationScopeDigest string
}

func (r *ApplyCommandsRequest) ResolveDraftSourceContext(value DraftSourceContext) error {
	if r == nil || strings.TrimSpace(value.SnapshotToken) == "" || value.SnapshotToken != strings.TrimSpace(value.SnapshotToken) ||
		strings.TrimSpace(value.SourceGeneration) == "" || value.SourceGeneration != strings.TrimSpace(value.SourceGeneration) ||
		strings.TrimSpace(value.AuthorizationScopeDigest) == "" || value.AuthorizationScopeDigest != strings.TrimSpace(value.AuthorizationScopeDigest) {
		return fmt.Errorf("resolved draft source context requires exact snapshot, generation, and authorization scope identities")
	}
	r.draftSourceContext = &value
	return nil
}

func (r ApplyCommandsRequest) ResolvedDraftSourceContext() *DraftSourceContext {
	if r.draftSourceContext == nil {
		return nil
	}
	value := *r.draftSourceContext
	return &value
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
	Type                     string                            `json:"type"`
	OutputID                 string                            `json:"outputId,omitempty"`
	SourceOutputID           string                            `json:"sourceOutputId,omitempty"`
	Title                    string                            `json:"title,omitempty"`
	RootNodeID               string                            `json:"rootNodeId,omitempty"`
	SelectionRevisionID      string                            `json:"selectionRevisionId,omitempty"`
	EdgeIDs                  []string                          `json:"edgeIds,omitempty"`
	RouteChoiceID            string                            `json:"routeChoiceId,omitempty"`
	ParentOccurrenceID       string                            `json:"parentOccurrenceId,omitempty"`
	OccurrenceID             string                            `json:"occurrenceId,omitempty"`
	EdgeID                   string                            `json:"edgeId,omitempty"`
	MatchMode                RouteMatchMode                    `json:"matchMode,omitempty"`
	CandidateID              string                            `json:"candidateId,omitempty"`
	ProjectionMode           string                            `json:"projectionMode,omitempty"`
	InitialPresentation      string                            `json:"initialPresentation,omitempty"`
	Column                   string                            `json:"column,omitempty"`
	RowValuePolicy           ConstructionRowValuePolicy        `json:"rowValuePolicy,omitempty"`
	ColumnValue              *Column                           `json:"columnValue,omitempty"`
	ConstructionOutput       *ConstructionOutputPresentation   `json:"constructionOutput,omitempty"`
	TransformationChange     *ColumnTransformationChange       `json:"transformationChange,omitempty"`
	Contributor              *ContributorPredicate             `json:"contributor,omitempty"`
	Source                   *ColumnSource                     `json:"source,omitempty"`
	RowChange                *RowChangeProposal                `json:"rowChange,omitempty"`
	InterpretationCandidate  *ApplyInterpretationCandidate     `json:"interpretationCandidate,omitempty"`
	ProposalID               string                            `json:"proposalId,omitempty"`
	DraftRevisionID          string                            `json:"draftRevisionId,omitempty"`
	ContextToken             string                            `json:"contextToken,omitempty"`
	SemanticSelections       []SemanticSelection               `json:"semanticSelections,omitempty"`
	ConstructionChoice       *ConstructionChoiceSelection      `json:"constructionChoice,omitempty"`
	FrameChoiceID            string                            `json:"frameChoiceId,omitempty"`
	FrameID                  string                            `json:"frameId,omitempty"`
	FrameForm                capability.ConstructionChoiceForm `json:"form,omitempty"`
	ResolvedChoice           *ResolvedConstructionChoice       `json:"-"`
	resolvedFrame            *FrameDefinition
	ResolvedPopulationRoute  []PopulationRouteStep `json:"-"`
	OutputIDs                []string              `json:"outputIds,omitempty"`
	resolvedRowDefinition    *RowDefinition
	resolvedTableShape       *TableShape
	resolvedTableShapeSet    bool
	resolvedConstruction     *Construction
	resolvedConstructionSet  bool
	resolvedConstructionRows *RowDefinition
	resolvedDraftRevision    *Workspace
}

// ConstructionOutputPresentation updates user-facing metadata for a stable
// construction output. Name and Type remain compiler-owned identities.
type ConstructionOutputPresentation struct {
	StepID   string             `json:"stepId"`
	ColumnID string             `json:"columnId"`
	Label    string             `json:"label"`
	Table    *TablePresentation `json:"table,omitempty"`
}

func (p ConstructionOutputPresentation) Validate() error {
	if strings.TrimSpace(p.StepID) == "" || strings.TrimSpace(p.ColumnID) == "" || strings.TrimSpace(p.Label) == "" ||
		p.StepID != strings.TrimSpace(p.StepID) ||
		p.ColumnID != strings.TrimSpace(p.ColumnID) || p.Label != strings.TrimSpace(p.Label) {
		return fmt.Errorf("stepId, columnId, and label must be non-empty and trimmed")
	}
	if p.Table != nil {
		if p.Table.Order != nil && *p.Table.Order < 0 {
			return fmt.Errorf("table.order must be non-negative")
		}
		if p.Table.CellRenderer != "" && p.Table.CellRenderer != "fileActions" {
			return fmt.Errorf("table.cellRenderer is unsupported")
		}
	}
	return nil
}

// ResolveDraftRevision binds a restore command to a workspace resolved by the
// lifecycle service from the owner's exact previous-draft pointer.
func (c *Command) ResolveDraftRevision(workspace Workspace) error {
	if c == nil || c.Type != CommandRestoreDraftRevision || strings.TrimSpace(c.DraftRevisionID) == "" || c.DraftRevisionID != strings.TrimSpace(c.DraftRevisionID) {
		return fmt.Errorf("RESTORE_DRAFT_REVISION requires an exact server-resolved draft revision")
	}
	cloned, err := cloneWorkspace(workspace)
	if err != nil {
		return err
	}
	c.resolvedDraftRevision = &cloned
	return nil
}

// ColumnTransformationChange replaces or removes the one typed value
// transformation attached to an authored column.
type ColumnTransformationChange struct {
	Kind           string                               `json:"kind"`
	Transformation *columntransform.ValueTransformation `json:"transformation,omitempty"`
}

func (c ColumnTransformationChange) Validate() error {
	switch c.Kind {
	case "SET":
		if c.Transformation == nil {
			return fmt.Errorf("SET transformationChange requires transformation")
		}
		return c.Transformation.Validate()
	case "REMOVE":
		if c.Transformation != nil {
			return fmt.Errorf("REMOVE transformationChange must omit transformation")
		}
		return nil
	default:
		return fmt.Errorf("transformationChange.kind must be SET or REMOVE")
	}
}

// ConstructionChoiceSelection carries only the server-issued source identity
// and the form the author chose from its current supported options.
type ConstructionChoiceSelection struct {
	RowValuePolicy ConstructionRowValuePolicy        `json:"rowValuePolicy,omitempty"`
	ChoiceID       string                            `json:"choiceId"`
	Form           capability.ConstructionChoiceForm `json:"form"`
	FrameID        string                            `json:"frameId,omitempty"`
}

func (s *ConstructionChoiceSelection) UnmarshalJSON(raw []byte) error {
	type wire ConstructionChoiceSelection
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields["choiceId"] == nil || fields["form"] == nil {
		return fmt.Errorf("constructionChoice requires choiceId and form")
	}
	*s = ConstructionChoiceSelection(decoded)
	return nil
}

func (s ConstructionChoiceSelection) validate() error {
	if s.RowValuePolicy != "" && s.RowValuePolicy != ConstructionRowValueAll && s.RowValuePolicy != ConstructionRowValueOne {
		return fmt.Errorf("constructionChoice.rowValuePolicy must be ALL or ONE")
	}

	if strings.TrimSpace(s.ChoiceID) == "" || s.ChoiceID != strings.TrimSpace(s.ChoiceID) {
		return fmt.Errorf("constructionChoice.choiceId must be an exact non-empty token")
	}
	if s.FrameID != "" && (strings.TrimSpace(s.FrameID) == "" || s.FrameID != strings.TrimSpace(s.FrameID)) {
		return fmt.Errorf("constructionChoice.frameId must be an exact non-empty id")
	}
	switch s.Form {
	case capability.ConstructionChoiceValue, capability.ConstructionChoiceFirst,
		capability.ConstructionChoiceAll, capability.ConstructionChoiceDistinct,
		capability.ConstructionChoiceOwnerRecords:
		return nil
	default:
		return fmt.Errorf("constructionChoice.form is unsupported")
	}
}

// ResolvedConstructionChoice is populated by lifecycle after it re-authorizes
// and reconstructs the choice against the current compiler snapshot.
type ResolvedConstructionChoice struct {
	CandidateID string
	Source      ColumnSource
	LogicalType string
	Route       []capability.ConstructionRouteStep
	FrameID     string
}

// SemanticSelection is client intent plus an internal-only resolution filled
// by lifecycle after authorization and context checks.
type SemanticSelection struct {
	ConceptID           string                          `json:"conceptId"`
	BindingID           string                          `json:"bindingId"`
	RouteEdgeIDs        []string                        `json:"routeEdgeIds"`
	ProjectionMode      string                          `json:"projectionMode"`
	Title               string                          `json:"title,omitempty"`
	ResolvedObservation *catalog.SemanticInventoryEntry `json:"-"`
}

func (s *SemanticSelection) UnmarshalJSON(raw []byte) error {
	type wire SemanticSelection
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	*s = SemanticSelection(decoded)
	return nil
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
	if decoded.Type == CommandApplyConstructionChoice {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		allowed := map[string]bool{"type": true, "outputId": true, "title": true, "initialPresentation": true, "constructionChoice": true}
		for name := range fields {
			if !allowed[name] {
				return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE does not accept field %q", name)
			}
		}
	}
	if decoded.Type == CommandUpdateColumnRowValuePolicy {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		allowed := map[string]bool{"type": true, "outputId": true, "column": true, "rowValuePolicy": true}
		for name := range fields {
			if !allowed[name] {
				return fmt.Errorf("%s does not accept field %q", decoded.Type, name)
			}
		}
	}
	if decoded.Type == CommandSetFrameSource || decoded.Type == CommandReplaceFrameSource || decoded.Type == CommandRemoveFrameSource {
		if err := rejectUnknownFrameSourceCommandFields(raw, decoded.Type); err != nil {
			return err
		}
	}
	if decoded.Type == CommandRestoreDraftRevision {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		allowed := map[string]bool{"type": true, "draftRevisionId": true}
		for name := range fields {
			if !allowed[name] {
				return fmt.Errorf("RESTORE_DRAFT_REVISION does not accept field %q", name)
			}
		}
	}
	if decoded.Type == CommandApplyRowDefinitionProposal || decoded.Type == CommandApplyTableShapeProposal || decoded.Type == CommandApplyConstructionProposal {
		if err := rejectUnknownProposalCommandFields(raw, decoded.Type); err != nil {
			return err
		}
	}
	*c = Command(decoded)
	return nil
}

func rejectUnknownFrameSourceCommandFields(raw []byte, commandType string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{"type": true, "outputId": true}
	switch commandType {
	case CommandSetFrameSource:
		allowed["frameChoiceId"], allowed["form"] = true, true
	case CommandReplaceFrameSource:
		allowed["frameId"], allowed["frameChoiceId"], allowed["form"] = true, true, true
	case CommandRemoveFrameSource:
		allowed["frameId"] = true
	}
	for name := range fields {
		if !allowed[name] {
			return fmt.Errorf("%s does not accept field %q", commandType, name)
		}
	}
	return nil
}

func rejectUnknownProposalCommandFields(raw []byte, commandType string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{"type": true, "outputId": true, "proposalId": true}
	for name := range fields {
		if !allowed[name] {
			return fmt.Errorf("%s does not accept field %q", commandType, name)
		}
	}
	return nil
}

// ResolveRowDefinitionProposal attaches the lifecycle-validated row definition
// to an apply command. The resolved value is deliberately not part of the
// browser command or its serialized payload.
func (c *Command) ResolveRowDefinitionProposal(rows RowDefinition) error {
	if c == nil || c.Type != CommandApplyRowDefinitionProposal {
		return fmt.Errorf("row definition can only be resolved for APPLY_ROW_DEFINITION_PROPOSAL")
	}
	if strings.TrimSpace(c.OutputID) == "" || c.OutputID != strings.TrimSpace(c.OutputID) ||
		strings.TrimSpace(c.ProposalID) == "" || c.ProposalID != strings.TrimSpace(c.ProposalID) {
		return fmt.Errorf("APPLY_ROW_DEFINITION_PROPOSAL requires outputId and proposalId")
	}
	if err := rows.Validate(); err != nil {
		return fmt.Errorf("resolved row definition: %w", err)
	}
	cloned, err := cloneRowDefinition(rows)
	if err != nil {
		return err
	}
	c.resolvedRowDefinition = &cloned
	return nil
}

// ResolveTableShapeProposal attaches the lifecycle-validated table shape to an
// apply command. A nil shape is a resolved removal, not an unresolved command.
func (c *Command) ResolveTableShapeProposal(shape *TableShape) error {
	if c == nil || c.Type != CommandApplyTableShapeProposal {
		return fmt.Errorf("table shape can only be resolved for APPLY_TABLE_SHAPE_PROPOSAL")
	}
	if strings.TrimSpace(c.OutputID) == "" || c.OutputID != strings.TrimSpace(c.OutputID) ||
		strings.TrimSpace(c.ProposalID) == "" || c.ProposalID != strings.TrimSpace(c.ProposalID) {
		return fmt.Errorf("APPLY_TABLE_SHAPE_PROPOSAL requires outputId and proposalId")
	}
	cloned, err := cloneTableShape(shape)
	if err != nil {
		return err
	}
	c.resolvedTableShape = cloned
	c.resolvedTableShapeSet = true
	return nil
}

// ResolveConstructionProposal attaches the lifecycle-validated candidate
// document's construction and row intent to an apply command. Both values are
// server-resolved state and never appear in the browser command payload.
func (c *Command) ResolveConstructionProposal(candidate *Document) error {
	if c == nil || c.Type != CommandApplyConstructionProposal {
		return fmt.Errorf("construction can only be resolved for APPLY_CONSTRUCTION_PROPOSAL")
	}
	if strings.TrimSpace(c.OutputID) == "" || c.OutputID != strings.TrimSpace(c.OutputID) ||
		strings.TrimSpace(c.ProposalID) == "" || c.ProposalID != strings.TrimSpace(c.ProposalID) {
		return fmt.Errorf("APPLY_CONSTRUCTION_PROPOSAL requires outputId and proposalId")
	}
	if candidate == nil || candidate.Output.ID != c.OutputID || candidate.Construction == nil {
		return fmt.Errorf("resolved construction candidate is required for the target output")
	}
	if err := candidate.Rows.Validate(); err != nil {
		return fmt.Errorf("resolved construction candidate rows are invalid: %w", err)
	}
	clonedConstruction, err := cloneConstruction(candidate.Construction)
	if err != nil {
		return err
	}
	clonedRows, err := cloneRowDefinition(candidate.Rows)
	if err != nil {
		return err
	}
	c.resolvedConstruction = clonedConstruction
	c.resolvedConstructionSet = true
	c.resolvedConstructionRows = &clonedRows
	return nil
}

type CommandResult struct {
	Type               string                    `json:"type"`
	OutputID           string                    `json:"outputId,omitempty"`
	TabID              string                    `json:"tabId,omitempty"`
	OccurrenceID       string                    `json:"occurrenceId,omitempty"`
	Column             string                    `json:"column,omitempty"`
	FrameID            string                    `json:"frameId,omitempty"`
	RemovedColumns     []string                  `json:"removedColumns,omitempty"`
	SemanticSelections []SemanticSelectionResult `json:"semanticSelections,omitempty"`
}

type SemanticSelectionResult struct {
	ConceptID string `json:"conceptId"`
	BindingID string `json:"bindingId"`
	ColumnID  string `json:"columnId"`
	Status    string `json:"status"`
}

type ApplyCommandsResponse struct {
	CommandID               string          `json:"commandId"`
	Workspace               Workspace       `json:"workspace"`
	DraftVersion            int64           `json:"draftVersion"`
	DraftDigest             string          `json:"draftDigest"`
	PreviousDraftRevisionID string          `json:"previousDraftRevisionId,omitempty"`
	Results                 []CommandResult `json:"results"`
	Diagnostics             []any           `json:"diagnostics"`
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
	semanticCommandCount := 0
	constructionChoiceCommandCount := 0
	frameSourceCommandCount := 0
	proposalCommandCount := 0
	restoreCommandCount := 0
	for i, command := range r.Commands {
		if command.Type == CommandAddSemanticSelections {
			semanticCommandCount++
		}
		if command.Type == CommandApplyConstructionChoice {
			constructionChoiceCommandCount++
		}
		if command.Type == CommandSetFrameSource || command.Type == CommandReplaceFrameSource || command.Type == CommandRemoveFrameSource {
			frameSourceCommandCount++
		}
		if command.Type == CommandApplyRowDefinitionProposal || command.Type == CommandApplyTableShapeProposal || command.Type == CommandApplyConstructionProposal {
			proposalCommandCount++
		}
		if command.Type == CommandRestoreDraftRevision {
			restoreCommandCount++
		}
		if err := command.validate(); err != nil {
			return fmt.Errorf("commands[%d]: %w", i, err)
		}
	}
	if semanticCommandCount > 0 && (semanticCommandCount != 1 || len(r.Commands) != 1) {
		return fmt.Errorf("ADD_SEMANTIC_SELECTIONS must be the only command in its atomic request")
	}
	if constructionChoiceCommandCount > 0 && (constructionChoiceCommandCount != len(r.Commands) || constructionChoiceCommandCount > 100) {
		return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE commands must form the entire request and contain at most 100 choices")
	}
	if frameSourceCommandCount > 0 && (frameSourceCommandCount != 1 || len(r.Commands) != 1) {
		return fmt.Errorf("frame source changes must be the only command in their atomic request")
	}
	if proposalCommandCount > 0 {
		if proposalCommandCount != 1 || len(r.Commands) != 1 {
			return fmt.Errorf("proposal apply must be the only command in its atomic request")
		}
		if r.ExpectedDraftVersion < 1 || strings.TrimSpace(r.ExpectedDraftDigest) == "" || r.ExpectedDraftDigest != strings.TrimSpace(r.ExpectedDraftDigest) {
			return fmt.Errorf("proposal apply requires an exact expected draft version and digest")
		}
	}
	if restoreCommandCount > 0 {
		if restoreCommandCount != 1 || len(r.Commands) != 1 {
			return fmt.Errorf("RESTORE_DRAFT_REVISION must be the only command in its atomic request")
		}
		if r.ExpectedDraftVersion < 1 || strings.TrimSpace(r.ExpectedDraftDigest) == "" || r.ExpectedDraftDigest != strings.TrimSpace(r.ExpectedDraftDigest) {
			return fmt.Errorf("RESTORE_DRAFT_REVISION requires an exact expected draft version and digest")
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
	if c.Type != CommandUpdateColumnRowValuePolicy && c.RowValuePolicy != "" {
		return fmt.Errorf("rowValuePolicy is only accepted by UPDATE_COLUMN_ROW_VALUE_POLICY")
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
		if strings.TrimSpace(c.RouteChoiceID) != "" && len(c.EdgeIDs) != 0 {
			return fmt.Errorf("SET_TABLE_POPULATION cannot combine routeChoiceId with client edgeIds")
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
	case CommandUpdateColumnRowValuePolicy:
		if !required(c.OutputID, c.Column) {
			return fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY requires outputId and column")
		}
		if c.RowValuePolicy != ConstructionRowValueAll && c.RowValuePolicy != ConstructionRowValueOne {
			return fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY rowValuePolicy must be ALL or ONE")
		}
		if c.SourceOutputID != "" || c.Title != "" || c.RootNodeID != "" || c.SelectionRevisionID != "" ||
			len(c.EdgeIDs) != 0 || c.RouteChoiceID != "" || c.ParentOccurrenceID != "" || c.OccurrenceID != "" || c.EdgeID != "" || c.MatchMode != "" ||
			c.CandidateID != "" || c.ProjectionMode != "" || c.InitialPresentation != "" || c.ColumnValue != nil || c.ConstructionOutput != nil || c.TransformationChange != nil || c.Contributor != nil ||
			c.Source != nil || c.RowChange != nil || c.InterpretationCandidate != nil || c.ProposalID != "" || c.DraftRevisionID != "" || c.ContextToken != "" || len(c.SemanticSelections) != 0 ||
			c.ConstructionChoice != nil || c.FrameChoiceID != "" || c.FrameID != "" || c.FrameForm != "" || c.ResolvedChoice != nil || len(c.ResolvedPopulationRoute) != 0 || len(c.OutputIDs) != 0 {
			return fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY accepts only outputId, column, and rowValuePolicy")
		}
	case CommandUpdateConstructionOutput:
		if !required(c.OutputID) || c.ConstructionOutput == nil {
			return fmt.Errorf("UPDATE_CONSTRUCTION_OUTPUT requires outputId and constructionOutput")
		}
		if err := c.ConstructionOutput.Validate(); err != nil {
			return fmt.Errorf("constructionOutput: %w", err)
		}
	case CommandUpdateColumnTransformation:
		if !required(c.OutputID, c.Column) || c.TransformationChange == nil {
			return fmt.Errorf("UPDATE_COLUMN_TRANSFORMATION requires outputId, column, and transformationChange")
		}
		if err := c.TransformationChange.Validate(); err != nil {
			return fmt.Errorf("transformationChange: %w", err)
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
	case CommandApplyRowDefinitionProposal, CommandApplyTableShapeProposal, CommandApplyConstructionProposal:
		if !required(c.OutputID, c.ProposalID) || c.OutputID != strings.TrimSpace(c.OutputID) || c.ProposalID != strings.TrimSpace(c.ProposalID) {
			return fmt.Errorf("%s requires exact outputId and proposalId", c.Type)
		}
		if c.SourceOutputID != "" || c.Title != "" || c.RootNodeID != "" || c.SelectionRevisionID != "" ||
			len(c.EdgeIDs) != 0 || c.RouteChoiceID != "" || c.ParentOccurrenceID != "" || c.OccurrenceID != "" || c.EdgeID != "" || c.MatchMode != "" ||
			c.CandidateID != "" || c.ProjectionMode != "" || c.InitialPresentation != "" || c.Column != "" || c.ColumnValue != nil || c.TransformationChange != nil || c.Contributor != nil ||
			c.Source != nil || c.RowChange != nil || c.InterpretationCandidate != nil || c.ContextToken != "" || len(c.SemanticSelections) != 0 ||
			c.ConstructionChoice != nil || c.ResolvedChoice != nil || len(c.ResolvedPopulationRoute) != 0 || len(c.OutputIDs) != 0 {
			return fmt.Errorf("%s accepts only outputId and proposalId", c.Type)
		}
	case CommandRestoreDraftRevision:
		if !required(c.DraftRevisionID) || c.DraftRevisionID != strings.TrimSpace(c.DraftRevisionID) {
			return fmt.Errorf("RESTORE_DRAFT_REVISION requires an exact draftRevisionId")
		}
		if c.OutputID != "" || c.SourceOutputID != "" || c.Title != "" || c.RootNodeID != "" || c.SelectionRevisionID != "" ||
			len(c.EdgeIDs) != 0 || c.RouteChoiceID != "" || c.ParentOccurrenceID != "" || c.OccurrenceID != "" || c.EdgeID != "" || c.MatchMode != "" ||
			c.CandidateID != "" || c.ProjectionMode != "" || c.InitialPresentation != "" || c.Column != "" || c.ColumnValue != nil || c.TransformationChange != nil || c.Contributor != nil ||
			c.Source != nil || c.RowChange != nil || c.InterpretationCandidate != nil || c.ProposalID != "" || c.ContextToken != "" || len(c.SemanticSelections) != 0 ||
			c.ConstructionChoice != nil || c.ResolvedChoice != nil || len(c.ResolvedPopulationRoute) != 0 || len(c.OutputIDs) != 0 || c.resolvedDraftRevision != nil {
			return fmt.Errorf("RESTORE_DRAFT_REVISION accepts only draftRevisionId")
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
	case CommandAddSemanticSelections:
		if !required(c.OutputID, c.ContextToken) || len(c.SemanticSelections) == 0 || len(c.SemanticSelections) > 100 {
			return fmt.Errorf("ADD_SEMANTIC_SELECTIONS requires outputId, contextToken, and between 1 and 100 selections")
		}
		for i, selection := range c.SemanticSelections {
			if err := selection.validate(); err != nil {
				return fmt.Errorf("semanticSelections[%d]: %w", i, err)
			}
		}
	case CommandApplyConstructionChoice:
		if !required(c.OutputID) || c.ConstructionChoice == nil {
			return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE requires outputId and constructionChoice")
		}
		if err := c.ConstructionChoice.validate(); err != nil {
			return err
		}
		if c.ResolvedChoice != nil || c.SourceOutputID != "" || c.RootNodeID != "" || c.SelectionRevisionID != "" ||
			len(c.EdgeIDs) != 0 || c.ParentOccurrenceID != "" || c.OccurrenceID != "" || c.EdgeID != "" || c.MatchMode != "" ||
			c.CandidateID != "" || c.ProjectionMode != "" || c.Column != "" || c.ColumnValue != nil || c.TransformationChange != nil || c.Contributor != nil ||
			c.Source != nil || c.RowChange != nil || c.InterpretationCandidate != nil || c.ContextToken != "" ||
			len(c.SemanticSelections) != 0 || len(c.OutputIDs) != 0 {
			return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE accepts only its choice identity and authored-column presentation fields")
		}
		if c.InitialPresentation != "" && !contains([]string{InitialPresentationTable, InitialPresentationFilter, InitialPresentationChart}, strings.ToUpper(strings.TrimSpace(c.InitialPresentation))) {
			return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE initialPresentation must be TABLE, FILTER, or CHART")
		}
	case CommandSetFrameSource:
		if !required(c.OutputID, c.FrameChoiceID) || c.FrameForm == "" {
			return fmt.Errorf("SET_FRAME_SOURCE requires outputId, frameChoiceId, and form")
		}
		if c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameChoiceID != strings.TrimSpace(c.FrameChoiceID) || !validFrameForm(c.FrameForm) ||
			c.FrameID != "" || c.ConstructionChoice != nil || c.Title != "" || c.InitialPresentation != "" {
			return fmt.Errorf("SET_FRAME_SOURCE accepts only exact outputId, frameChoiceId, and supported form")
		}
	case CommandReplaceFrameSource:
		if !required(c.OutputID, c.FrameID, c.FrameChoiceID) || c.FrameForm == "" {
			return fmt.Errorf("REPLACE_FRAME_SOURCE requires outputId, frameId, frameChoiceId, and form")
		}
		if c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameID != strings.TrimSpace(c.FrameID) || c.FrameChoiceID != strings.TrimSpace(c.FrameChoiceID) || !validFrameForm(c.FrameForm) ||
			c.ConstructionChoice != nil || c.Title != "" || c.InitialPresentation != "" {
			return fmt.Errorf("REPLACE_FRAME_SOURCE accepts only exact outputId, frameId, frameChoiceId, and supported form")
		}
	case CommandRemoveFrameSource:
		if !required(c.OutputID, c.FrameID) || c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameID != strings.TrimSpace(c.FrameID) ||
			c.FrameChoiceID != "" || c.FrameForm != "" || c.ConstructionChoice != nil || c.Title != "" || c.InitialPresentation != "" {
			return fmt.Errorf("REMOVE_FRAME_SOURCE requires exact outputId and frameId only")
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
	if command.Type != CommandUpdateColumnRowValuePolicy && command.RowValuePolicy != "" {
		return result, fmt.Errorf("rowValuePolicy is only accepted by UPDATE_COLUMN_ROW_VALUE_POLICY")
	}
	switch command.Type {
	case CommandSetFrameSource, CommandReplaceFrameSource, CommandRemoveFrameSource:
		return applyFrameSourceCommand(workspace, command)
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
		workspace.Documents = append(workspace.Documents, Document{Kind: Kind, Output: Output{ID: outputID, Title: title}, RootResourceType: node.ResourceType, Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: node.ResourceType}, Rows: RecordsRowDefinition(), Columns: []Column{}})
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
		if command.RouteChoiceID != "" {
			if command.ResolvedPopulationRoute == nil {
				return result, fmt.Errorf("population route choice has no server resolution")
			}
			setPopulationRoute(&workspace.Documents[document], command.SelectionRevisionID, command.ResolvedPopulationRoute)
		} else if err := setPopulation(&workspace.Documents[document], catalog, command.SelectionRevisionID, command.EdgeIDs); err != nil {
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
		parentNodeID, parentNodeFound := catalogNodeIDForOccurrence(workspace.Documents[document].Route, parent.OccurrenceID, catalog)
		if !fromOK || !toOK || !parentNodeFound || edge.FromNodeID != parentNodeID || from.ResourceType != parent.ResourceType {
			return result, fmt.Errorf("edge %q does not extend occurrence %q", edge.ID, command.ParentOccurrenceID)
		}
		if !catalog.RoutePolicy.AllowRepeatedEdges {
			used, err := routePathUsesCatalogEdge(workspace.Documents[document].Route, command.ParentOccurrenceID, edge.ID, catalog)
			if err != nil {
				return result, fmt.Errorf("route parent does not resolve to an exact path: %w", err)
			}
			if used {
				return result, fmt.Errorf("edge %q is already used in this route", edge.ID)
			}
		}
		occurrenceID := commandGeneratedID("occ_", commandID, index, command.Type)
		if findRoute(&workspace.Documents[document].Route, occurrenceID) != nil {
			return CommandResult{Type: CommandResultRouteAdded, OutputID: command.OutputID, OccurrenceID: occurrenceID}, nil
		}
		parent.Children = append(parent.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, CatalogEdgeID: edge.ID, Relationship: edge.Label})
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
		parentNodeID, parentNodeFound := catalogNodeIDForOccurrence(workspace.Documents[document].Route, parent.OccurrenceID, catalog)
		occurrenceNodeID, occurrenceNodeFound := catalogNodeIDForOccurrence(workspace.Documents[document].Route, occurrence.OccurrenceID, catalog)
		if !parentNodeFound || !occurrenceNodeFound {
			return result, fmt.Errorf("route occurrence does not resolve to one exact catalog path")
		}
		from, fromOK := catalogNode(catalog, edge.FromNodeID)
		to, toOK := catalogNode(catalog, edge.ToNodeID)
		if !fromOK || !toOK || edge.FromNodeID != parentNodeID || edge.ToNodeID != occurrenceNodeID || from.ResourceType != parent.ResourceType || to.ResourceType != occurrence.ResourceType {
			return result, fmt.Errorf("edge %q cannot replace the relationship for occurrence %q", edge.ID, command.OccurrenceID)
		}
		if !catalog.RoutePolicy.AllowRepeatedEdges {
			usedOnPath, err := routePathUsesCatalogEdge(workspace.Documents[document].Route, parent.OccurrenceID, edge.ID, catalog)
			if err != nil {
				return result, fmt.Errorf("route parent does not resolve to an exact path: %w", err)
			}
			usedInSubtree, err := routeSubtreeUsesCatalogEdge(*occurrence, occurrenceNodeID, edge.ID, catalog)
			if err != nil {
				return result, fmt.Errorf("route subtree does not resolve to exact paths: %w", err)
			}
			if usedOnPath || usedInSubtree {
				return result, fmt.Errorf("edge %q is already used in this route", edge.ID)
			}
		}
		occurrence.CatalogEdgeID = edge.ID
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
				applyInitialPresentation(column, presentation, nextTableOrder(workspace.Documents[document]))
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
		column.ColumnID = stagedSourceColumnID(workspace.Documents[document].Output.ID, commandID, index, command.Type, column.Column)
		applyInitialPresentation(&column, presentation, nextTableOrder(workspace.Documents[document]))
		workspace.Documents[document].Columns = append(workspace.Documents[document].Columns, column)
		return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: columnID}, nil
	case CommandAddColumnSource:
		return applyColumnSource(workspace, catalog, commandID, index, command, *command.Source, "", InitialPresentationTable)
	case CommandApplyConstructionChoice:
		resolved := command.ResolvedChoice
		if resolved == nil || strings.TrimSpace(resolved.CandidateID) == "" || strings.TrimSpace(resolved.LogicalType) == "" {
			return result, fmt.Errorf("construction choice has no server-resolved source")
		}
		presentation := strings.ToUpper(strings.TrimSpace(command.InitialPresentation))
		if presentation == "" {
			presentation = InitialPresentationTable
		}
		documentPos := documentIndex(workspace, command.OutputID)
		if documentPos < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		document := workspace.Documents[documentPos]
		if document.Route.OccurrenceID != RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
			return result, fmt.Errorf("table root route is invalid")
		}
		targetResourceType := document.RootResourceType
		if len(resolved.Route) > 0 {
			targetResourceType = resolved.Route[len(resolved.Route)-1].ToResourceType
		}
		if presentation != InitialPresentationTable {
			candidate, ok := catalogCandidate(catalog, resolved.CandidateID)
			if !ok {
				return result, fmt.Errorf("construction choice candidate %q was not found", resolved.CandidateID)
			}
			node, ok := catalogNode(catalog, candidate.NodeID)
			if !ok {
				return result, fmt.Errorf("construction choice candidate %q has no catalog node", resolved.CandidateID)
			}
			if node.ResourceType != targetResourceType || len(resolved.Route) > 0 && node.ID != resolved.Route[len(resolved.Route)-1].ToNodeID {
				return result, fmt.Errorf("construction choice candidate %q does not match its exact terminal route", resolved.CandidateID)
			}
			if presentation == InitialPresentationFilter && !candidate.Filterable {
				return result, fmt.Errorf("construction choice candidate %q does not support filters", resolved.CandidateID)
			}
			if presentation == InitialPresentationChart && !candidate.Chartable {
				return result, fmt.Errorf("construction choice candidate %q does not support charts", resolved.CandidateID)
			}
		}
		occurrenceID, routeErr := ensureConstructionRoute(&workspace.Documents[documentPos], catalog, commandID, index, resolved.Route, resolved.CandidateID)
		if routeErr != nil {
			return result, routeErr
		}
		if err := initializeEmptyConstructionBeforeSourceAdd(workspace, command.OutputID); err != nil {
			return result, err
		}
		command.OccurrenceID = occurrenceID
		applied, err := applyColumnSource(workspace, catalog, commandID, index, command, resolved.Source, resolved.LogicalType, presentation)
		if err != nil {
			return result, err
		}
		if resolved.FrameID != "" {
			if presentation != InitialPresentationTable {
				return result, fmt.Errorf("frame categories can only be added as table columns")
			}
			columns := workspace.Documents[documentPos].Columns
			found := false
			for columnIndex := range columns {
				if columns[columnIndex].Column == applied.Column {
					columns[columnIndex].FrameID = resolved.FrameID
					found = true
					break
				}
			}
			if !found {
				return result, fmt.Errorf("applied frame category column %q was not found", applied.Column)
			}
		}
		return applied, nil
	case CommandAddSemanticSelections:
		if err := initializeEmptyConstructionBeforeSourceAdd(workspace, command.OutputID); err != nil {
			return result, err
		}
		return applySemanticSelections(workspace, catalog, commandID, index, command)
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
	case CommandUpdateConstructionOutput:
		documentIndex := documentIndex(workspace, command.OutputID)
		if documentIndex < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		construction := workspace.Documents[documentIndex].Construction
		if construction == nil {
			return result, fmt.Errorf("output %q has no construction", command.OutputID)
		}
		presentation := command.ConstructionOutput
		outputName := ""
		for _, step := range construction.Steps {
			if step.ID != presentation.StepID {
				continue
			}
			for _, output := range step.Outputs {
				if output.ID == presentation.ColumnID {
					outputName = output.Name
					break
				}
			}
			break
		}
		if outputName == "" {
			return result, fmt.Errorf("construction output %q was not found in step %q", presentation.ColumnID, presentation.StepID)
		}
		for stepIndex := range construction.Steps {
			for outputIndex := range construction.Steps[stepIndex].Outputs {
				current := &construction.Steps[stepIndex].Outputs[outputIndex]
				if current.ID != presentation.ColumnID {
					continue
				}
				current.Label = presentation.Label
				if presentation.Table == nil {
					current.Table = nil
				} else {
					table := *presentation.Table
					current.Table = &table
				}
			}
		}
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: outputName}, nil
	case CommandUpdateColumnTransformation:
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		for i := range workspace.Documents[document].Columns {
			current := &workspace.Documents[document].Columns[i]
			if current.Column != command.Column {
				continue
			}
			if command.TransformationChange.Kind == "REMOVE" {
				current.ValueTransformation = nil
			} else {
				if err := validateColumnValueTransformationForCatalog(
					workspace.Documents[document], catalog, *current, current.Source, *command.TransformationChange.Transformation,
				); err != nil {
					return result, err
				}
				transformation := command.TransformationChange.Transformation.Clone()
				current.ValueTransformation = &transformation
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
	case CommandApplyRowDefinitionProposal:
		if command.resolvedRowDefinition == nil {
			return result, fmt.Errorf("APPLY_ROW_DEFINITION_PROPOSAL has no lifecycle-resolved row definition")
		}
		rows, err := cloneRowDefinition(*command.resolvedRowDefinition)
		if err != nil {
			return result, err
		}
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		workspace.Documents[document].Rows = rows
		return result, nil
	case CommandApplyTableShapeProposal:
		if !command.resolvedTableShapeSet {
			return result, fmt.Errorf("APPLY_TABLE_SHAPE_PROPOSAL has no lifecycle-resolved table shape")
		}
		document := documentIndex(workspace, command.OutputID)
		if document < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		shape, err := cloneTableShape(command.resolvedTableShape)
		if err != nil {
			return result, err
		}
		workspace.Documents[document].TableShape = shape
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
	case CommandApplyConstructionProposal:
		if !command.resolvedConstructionSet || command.resolvedConstruction == nil || command.resolvedConstructionRows == nil {
			return result, fmt.Errorf("APPLY_CONSTRUCTION_PROPOSAL has no lifecycle-resolved construction")
		}
		documentIndexValue := documentIndex(workspace, command.OutputID)
		if documentIndexValue < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		document, err := UpgradeDocumentToConstruction(workspace.Documents[documentIndexValue])
		if err != nil {
			return result, fmt.Errorf("prepare staged construction: %w", err)
		}
		construction, err := cloneConstruction(command.resolvedConstruction)
		if err != nil {
			return result, err
		}
		document.Construction = construction
		rows, err := cloneRowDefinition(*command.resolvedConstructionRows)
		if err != nil {
			return result, err
		}
		document.Rows = rows
		document.TableShape = nil
		if err := document.Validate(); err != nil {
			return result, fmt.Errorf("resolved construction is invalid: %w", err)
		}
		workspace.Documents[documentIndexValue] = document
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}, nil
	case CommandRestoreDraftRevision:
		if command.resolvedDraftRevision == nil {
			return result, fmt.Errorf("RESTORE_DRAFT_REVISION has no lifecycle-resolved draft revision")
		}
		restored, err := cloneWorkspace(*command.resolvedDraftRevision)
		if err != nil {
			return result, err
		}
		*workspace = restored
		return CommandResult{Type: CommandResultDraftRestored}, nil
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
			if current.ValueTransformation != nil {
				if err := validateColumnValueTransformationForCatalog(workspace.Documents[document], catalog, *current, source, *current.ValueTransformation); err != nil {
					return result, err
				}
			}
			current.Source = source
			current.LogicalType = inferredSourceLogicalType(workspace.Documents[document], catalog, current.OccurrenceID, source, current.LogicalType)
			return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
		}
		return result, fmt.Errorf("column %q was not found", command.Column)
	case CommandUpdateColumnRowValuePolicy:
		if err := command.validate(); err != nil {
			return result, err
		}
		documentPos := documentIndex(workspace, command.OutputID)
		if documentPos < 0 {
			return result, fmt.Errorf("output %q was not found", command.OutputID)
		}
		document := &workspace.Documents[documentPos]
		if document.Rows.Kind != RowDefinitionGroups || document.Rows.Groups == nil ||
			document.Rows.Groups.Source.Kind != GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil {
			return result, fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY requires explicit grouped rows")
		}
		columnIndex := -1
		for index := range document.Columns {
			if document.Columns[index].Column != command.Column {
				continue
			}
			if columnIndex >= 0 {
				return result, fmt.Errorf("column %q is ambiguous", command.Column)
			}
			columnIndex = index
		}
		if columnIndex < 0 {
			return result, fmt.Errorf("column %q was not found", command.Column)
		}
		column := document.Columns[columnIndex]
		if column.ColumnID == "" || !supportsExplicitGroupRowValueColumn(column) {
			return result, fmt.Errorf("column %q must be a supported root FHIR field with a stable columnId", command.Column)
		}
		matchingBinding := -1
		for index := range document.Rows.Groups.RowValues {
			if document.Rows.Groups.RowValues[index].ColumnID != column.ColumnID {
				continue
			}
			if matchingBinding >= 0 {
				return result, fmt.Errorf("row value binding for columnId %q is ambiguous", column.ColumnID)
			}
			matchingBinding = index
		}
		if matchingBinding < 0 {
			return result, fmt.Errorf("row value binding for columnId %q was not found", column.ColumnID)
		}
		document.Rows.Groups.RowValues[matchingBinding].Policy = command.RowValuePolicy
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: column.Column}, nil
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
				removeConstructionColumnValues(&workspace.Documents[document], column.ColumnID)
				removeExplicitGroupRowValue(&workspace.Documents[document], column.ColumnID)
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

func initializeEmptyConstructionBeforeSourceAdd(workspace *Workspace, outputID string) error {
	documentPos := documentIndex(workspace, outputID)
	if documentPos < 0 {
		return nil
	}
	document := workspace.Documents[documentPos]
	if len(document.Columns) != 0 || document.Construction != nil || document.TableShape != nil {
		return nil
	}
	upgraded, err := UpgradeDocumentToConstruction(document)
	if err != nil {
		return fmt.Errorf("initialize empty source construction: %w", err)
	}
	workspace.Documents[documentPos] = upgraded
	return nil
}

func setPopulationRoute(document *Document, selectionRevisionID string, route []PopulationRouteStep) {
	steps := make([]PopulationRouteStep, len(route))
	copy(steps, route)
	document.Population = &Population{SelectionRevisionID: strings.TrimSpace(selectionRevisionID), Route: steps}
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
	current, found := catalogNodeForConstructionRoot(catalog, document.RootResourceType)
	if !found {
		return fmt.Errorf("table root is not uniquely available in the capability catalog")
	}
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
		if !fromOK || !toOK || edge.FromNodeID != current.ID || from.ResourceType != current.ResourceType {
			return fmt.Errorf("population edge %q cannot extend %q at step %d", edgeID, current.ResourceType, index)
		}
		if from.ResourceType == to.ResourceType && !catalog.RoutePolicy.AllowSelfLoops {
			return fmt.Errorf("population edge %q is a self-loop but self-loops are not allowed", edgeID)
		}
		steps = append(steps, PopulationRouteStep{
			ResourceType: to.ResourceType, Relationship: edge.Label,
			CatalogEdgeID: edge.ID, StorageDirection: edge.StorageDirection,
		})
		seenEdges[edge.ID] = true
		current = to
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
	if source.Lookup != nil && source.Lookup.Identifier != nil && source.Kind == SourceIdentifierBySystem {
		if _, err := fhirschema.ValidateIdentifierBinding(occurrence.ResourceType, *source.Lookup.Identifier); err != nil {
			return fmt.Errorf("source identifier: %w", err)
		}
		return nil
	}
	if source.Lookup != nil && source.Lookup.Binding != nil && source.Kind == SourceCodedValue {
		if _, err := fhirschema.ValidateCorrelatedBinding(occurrence.ResourceType, *source.Lookup.Binding); err != nil {
			return fmt.Errorf("source binding: %w", err)
		}
		// Correlated lookups carry their structural source in Binding. They do
		// not have a legacy field path to resolve against a catalog candidate.
		return nil
	}
	if source.OwnerRecords != nil && source.Kind == SourceOwnerRecords {
		if _, err := fhirschema.ValidateCorrelatedBinding(occurrence.ResourceType, source.OwnerRecords.Binding); err != nil {
			return fmt.Errorf("source owner records binding: %w", err)
		}
		return nil
	}
	if source.Kind == SourceExtensionByURL || source.Kind == SourceCodedValue || source.Kind == SourceOwnerRecords {
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
		var selected CatalogCandidate
		hasSelected := false
		if path != "" {
			selected, hasSelected = findCandidate(path)
			if !hasSelected {
				return fmt.Errorf("source path %q is not present for occurrence %q", path, occurrenceID)
			}
		}
		if source.Aggregate != nil {
			related := occurrenceID != RootOccurrenceID
			input := capability.AggregateInput{
				HasField: path != "" && hasSelected, RelatedResource: related,
				ContributorWindowConfigured: source.Aggregate.ContributorWindow != nil,
				OrderingConfigured:          source.Aggregate.Ordering != nil,
				RequiredValuesConfigured:    len(source.Aggregate.RequiredValues) > 0,
			}
			if hasSelected {
				input.LogicalType, input.Cardinality = selected.LogicalType, selected.Cardinality
			}
			choices := capability.DeriveAggregateOperationCapabilities(input, capability.AggregateRowContext(document.Rows.Kind))
			operation := capability.AggregateOperation(strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)))
			for _, choice := range choices {
				if choice.Operation != operation {
					continue
				}
				if !choice.Supported && (operation == capability.AggregateSum || operation == capability.AggregateMean) {
					return fmt.Errorf("aggregate operation %s unavailable (%s): %s", operation, choice.ReasonCode, choice.Reason)
				}
				break
			}
		}
		if source.Aggregate != nil && source.Aggregate.ContributorWindow != nil {
			if occurrenceID == RootOccurrenceID {
				return fmt.Errorf("contributor window requires a related resource occurrence")
			}
			window := source.Aggregate.ContributorWindow
			timestampPath := canonicalTransformPath(window.TimestampPath)
			timestampCandidate, hasTimestamp := findCandidate(timestampPath)
			if !hasTimestamp {
				return fmt.Errorf("contributor window timestamp path %q is not advertised for occurrence %q", window.TimestampPath, occurrenceID)
			}
			transformCandidate := timestampCandidate
			if hasSelected {
				transformCandidate = selected
			} else if operation := strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)); operation != "COUNT" && operation != "EXISTS" {
				return fmt.Errorf("contributor window requires an advertised aggregate candidate")
			}
			transformations := AggregateTransformationCapabilitiesForCatalog(catalog, transformCandidate.ID)
			if !transformations.Temporal.Available {
				return fmt.Errorf("contributor window unavailable (%s): %s", transformations.Temporal.ReasonCode, transformations.Temporal.Reason)
			}
			if !transformations.Temporal.SupportsTimestamp(occurrence.ResourceType, timestampPath) {
				return fmt.Errorf("contributor window timestamp path %q is not advertised for occurrence %q", window.TimestampPath, occurrenceID)
			}
			anchorPath := canonicalTransformPath(window.AnchorPath)
			root := findRoute(&document.Route, RootOccurrenceID)
			if root == nil || !transformations.Temporal.SupportsAnchor(root.ResourceType, anchorPath) {
				return fmt.Errorf("contributor window anchor path %q is not advertised for the root resource", window.AnchorPath)
			}
			if ordering := source.Aggregate.Ordering; ordering != nil {
				orderingPath := canonicalTransformPath(ordering.TimestampPath)
				if !transformations.Temporal.SupportsTimestamp(occurrence.ResourceType, orderingPath) {
					return fmt.Errorf("ordering timestamp path %q is not advertised for occurrence %q", ordering.TimestampPath, occurrenceID)
				}
			}
		}
		if source.Aggregate != nil && source.Aggregate.UnitNormalization != nil {
			if !hasSelected {
				return fmt.Errorf("unit normalization requires an advertised aggregate candidate")
			}
			transformations := AggregateTransformationCapabilitiesForCatalog(catalog, selected.ID)
			for _, preset := range transformations.UnitNormalization.Presets {
				if preset.PolicyID != source.Aggregate.UnitNormalization.PolicyID || preset.Version != source.Aggregate.UnitNormalization.Version {
					continue
				}
				if !preset.Available {
					return fmt.Errorf("unit normalization policy %s@%s unavailable (%s): %s", preset.PolicyID, preset.Version, preset.ReasonCode, preset.Reason)
				}
				return nil
			}
			return fmt.Errorf("unit normalization policy %s@%s is not advertised for aggregate candidate %q", source.Aggregate.UnitNormalization.PolicyID, source.Aggregate.UnitNormalization.Version, selected.ID)
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
	if source.Lookup != nil && source.Lookup.Identifier != nil && strings.TrimSpace(source.Lookup.Identifier.LogicalType) != "" {
		return strings.TrimSpace(source.Lookup.Identifier.LogicalType)
	}
	if source.Kind == SourceAggregate && source.Aggregate != nil {
		switch strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)) {
		case "COUNT", "COUNT_DISTINCT":
			return "integer"
		case "SUM", "MEAN":
			return "decimal"
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

func nextTableOrder(document Document) int {
	result := 0
	if document.Rows.Kind == RowDefinitionGroups && document.Rows.Groups != nil && document.Rows.Groups.Source.Kind == GroupSourceExplicit {
		// Cohort label, ordinal and members precede the contributed fields.
		result = 3 + len(document.Rows.Groups.RowValues)
	}
	for _, column := range document.Columns {
		if column.Table != nil && column.Table.Order != nil && *column.Table.Order >= result {
			result = *column.Table.Order + 1
		}
	}
	if document.Construction != nil && len(document.Construction.Steps) > 0 {
		outputs := document.Construction.Steps[len(document.Construction.Steps)-1].Outputs
		for index, output := range outputs {
			order := index
			if output.Table != nil && output.Table.Order != nil {
				order = *output.Table.Order
			}
			if order >= result {
				result = order + 1
			}
		}
	}
	return result
}
