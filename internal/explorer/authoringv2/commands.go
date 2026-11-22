package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/catalog/semanticinventory"
	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	CommandCreateTable                       = "CREATE_TABLE"
	CommandDuplicateTable                    = "DUPLICATE_TABLE"
	CommandDeleteTable                       = "DELETE_TABLE"
	CommandRenameTable                       = "RENAME_TABLE"
	CommandReorderTables                     = "REORDER_TABLES"
	CommandSetTableRoot                      = "SET_TABLE_ROOT"
	CommandApplyTableRootRebase              = "APPLY_TABLE_ROOT_REBASE"
	CommandSetTablePopulation                = "SET_TABLE_POPULATION"
	CommandClearTablePopulation              = "CLEAR_TABLE_POPULATION"
	CommandAddRoute                          = "ADD_ROUTE"
	CommandUpdateRouteEdge                   = "UPDATE_ROUTE_EDGE"
	CommandSetRouteMatchMode                 = "SET_ROUTE_MATCH_MODE"
	CommandRemoveRoute                       = "REMOVE_ROUTE"
	CommandAddColumn                         = "ADD_COLUMN"
	CommandAddColumnSource                   = "ADD_COLUMN_SOURCE"
	CommandUpdateColumn                      = "UPDATE_COLUMN"
	CommandUpdateColumnRowValuePolicy        = "UPDATE_COLUMN_ROW_VALUE_POLICY"
	CommandUpdateConstructionOutput          = "UPDATE_CONSTRUCTION_OUTPUT"
	CommandUpdateColumnTransformation        = "UPDATE_COLUMN_TRANSFORMATION"
	CommandSetColumnContributor              = "SET_COLUMN_CONTRIBUTOR"
	CommandClearColumnContributor            = "CLEAR_COLUMN_CONTRIBUTOR"
	CommandApplyInterpretationCandidate      = "APPLY_INTERPRETATION_CANDIDATE"
	CommandApplyRowDefinitionProposal        = "APPLY_ROW_DEFINITION_PROPOSAL"
	CommandApplyConstructionProposal         = "APPLY_CONSTRUCTION_PROPOSAL"
	CommandApplyPopulationMemberProposal     = "APPLY_POPULATION_MEMBER_PROPOSAL"
	CommandRestoreDraftRevision              = "RESTORE_DRAFT_REVISION"
	CommandUpdateImportedSourceStep          = "UPDATE_IMPORTED_SOURCE_STEP"
	CommandRefreshImportedSource             = "REFRESH_IMPORTED_SOURCE"
	CommandApplyImportedRelationshipProposal = "APPLY_IMPORTED_RELATIONSHIP_PROPOSAL"
	CommandApplyColumnPolicyProposal         = "APPLY_COLUMN_POLICY_PROPOSAL"
	CommandUpdateColumnSource                = "UPDATE_COLUMN_SOURCE"
	CommandRemoveColumn                      = "REMOVE_COLUMN"
	CommandRemoveProjectedField              = "REMOVE_PROJECTED_FIELD"
	CommandAddSemanticSelections             = "ADD_SEMANTIC_SELECTIONS"
	CommandApplyConstructionChoice           = "APPLY_CONSTRUCTION_CHOICE"
	CommandSetFrameSource                    = "SET_FRAME_SOURCE"
	CommandReplaceFrameSource                = "REPLACE_FRAME_SOURCE"
	CommandRemoveFrameSource                 = "REMOVE_FRAME_SOURCE"
	CommandResultTableCreated                = "TABLE_CREATED"
	CommandResultTableChanged                = "TABLE_CHANGED"
	CommandResultRouteAdded                  = "ROUTE_ADDED"
	CommandResultColumnAdded                 = "COLUMN_ADDED"
	CommandResultSemanticSelectionsAdded     = "SEMANTIC_SELECTIONS_ADDED"
	CommandResultDraftRestored               = "DRAFT_RESTORED"
	SemanticSelectionAdded                   = "ADDED"
	SemanticSelectionAlreadyPresent          = "ALREADY_PRESENT"
	InitialPresentationTable                 = "TABLE"
	InitialPresentationFilter                = "FILTER"
	InitialPresentationChart                 = "CHART"
)

type ApplyCommandsRequest struct {
	CommandID            string    `json:"commandId"`
	SemanticsVersion     int       `json:"semanticsVersion"`
	SnapshotToken        string    `json:"snapshotToken"`
	ExpectedDraftVersion int64     `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string    `json:"expectedDraftDigest,omitempty"`
	Commands             []Command `json:"commands"`
	draftSourceContext   *DraftSourceContext
}

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
	ColumnID                 string                            `json:"columnId,omitempty"`
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
	ResolvedPopulationRoute  []PopulationRouteStep     `json:"-"`
	OutputIDs                []string                  `json:"outputIds,omitempty"`
	SourceStep               *ImportedSourceStepChange `json:"sourceStep,omitempty"`
	resolvedColumnPolicy     *resolvedColumnPolicy
	resolvedImportedDocument *Document
	resolvedRowDefinition    *RowDefinition
	resolvedConstruction     *Construction
	resolvedConstructionSet  bool
	resolvedConstructionRows *RowDefinition
	resolvedPopulation       *Population
	resolvedPopulationSet    bool
	resolvedDraftRevision    *Workspace
}

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

type ResolvedConstructionChoice struct {
	CandidateID string
	Source      ColumnSource
	LogicalType string
	Route       []capability.ConstructionRouteStep
	FrameID     string
}

type SemanticSelection struct {
	ConceptID           string                   `json:"conceptId"`
	BindingID           string                   `json:"bindingId"`
	RouteEdgeIDs        []string                 `json:"routeEdgeIds"`
	ProjectionMode      string                   `json:"projectionMode"`
	Title               string                   `json:"title,omitempty"`
	ResolvedObservation *semanticinventory.Entry `json:"-"`
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
	if IsImportedSourceCommand(decoded.Type) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for name := range fields {
			if name != "type" && name != "outputId" && !(decoded.Type == CommandUpdateImportedSourceStep && name == "sourceStep") && !(decoded.Type == CommandApplyImportedRelationshipProposal && name == "proposalId") {
				return fmt.Errorf("%s does not accept field %q", decoded.Type, name)
			}
		}
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
	if decoded.Type == CommandRemoveProjectedField {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for name := range fields {
			if name != "type" && name != "outputId" && name != "columnId" {
				return fmt.Errorf("%s does not accept field %q", decoded.Type, name)
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
	if decoded.Type == CommandApplyColumnPolicyProposal || decoded.Type == CommandApplyRowDefinitionProposal || decoded.Type == CommandApplyConstructionProposal || decoded.Type == CommandApplyPopulationMemberProposal {
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

func (c *Command) ResolvePopulationMemberProposal(selectionRevisionID string, route []PopulationRouteStep) error {
	if c == nil || c.Type != CommandApplyPopulationMemberProposal {
		return fmt.Errorf("population can only be resolved for APPLY_POPULATION_MEMBER_PROPOSAL")
	}
	if strings.TrimSpace(c.OutputID) == "" || c.OutputID != strings.TrimSpace(c.OutputID) || strings.TrimSpace(c.ProposalID) == "" || c.ProposalID != strings.TrimSpace(c.ProposalID) {
		return fmt.Errorf("APPLY_POPULATION_MEMBER_PROPOSAL requires outputId and proposalId")
	}
	population := Population{SelectionRevisionID: strings.TrimSpace(selectionRevisionID), Route: clonePopulationRouteSteps(route)}
	if err := population.Validate(); err != nil {
		return fmt.Errorf("resolved population is invalid: %w", err)
	}
	c.resolvedPopulation = &population
	c.resolvedPopulationSet = true
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
		if IsImportedSourceCommand(command.Type) {
			if len(r.Commands) != 1 || r.ExpectedDraftVersion < 1 || strings.TrimSpace(r.ExpectedDraftDigest) == "" {
				return fmt.Errorf("source changes require one command and an exact expected draft version and digest")
			}
		}
		if command.Type == CommandRemoveProjectedField && (len(r.Commands) != 1 || r.ExpectedDraftVersion < 1 || strings.TrimSpace(r.ExpectedDraftDigest) == "" || r.ExpectedDraftDigest != strings.TrimSpace(r.ExpectedDraftDigest)) {
			return fmt.Errorf("REMOVE_PROJECTED_FIELD requires one command and an exact expected draft version and digest")
		}
		if command.Type == CommandAddSemanticSelections {
			semanticCommandCount++
		}
		if command.Type == CommandApplyConstructionChoice {
			constructionChoiceCommandCount++
		}
		if command.Type == CommandSetFrameSource || command.Type == CommandReplaceFrameSource || command.Type == CommandRemoveFrameSource {
			frameSourceCommandCount++
		}
		if command.Type == CommandApplyColumnPolicyProposal || command.Type == CommandApplyRowDefinitionProposal || command.Type == CommandApplyConstructionProposal || command.Type == CommandApplyPopulationMemberProposal || command.Type == CommandApplyTableRootRebase && command.RowChange != nil && command.RowChange.Mode == RowChangeModeReplace {
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
	if c.Type != CommandUpdateColumnRowValuePolicy && c.RowValuePolicy != "" {
		return fmt.Errorf("rowValuePolicy is only accepted by UPDATE_COLUMN_ROW_VALUE_POLICY")
	}
	if c.Type != CommandRemoveProjectedField && c.ColumnID != "" {
		return fmt.Errorf("columnId is only accepted by REMOVE_PROJECTED_FIELD")
	}
	if c.Type != CommandUpdateImportedSourceStep && c.SourceStep != nil {
		return fmt.Errorf("sourceStep is only accepted by UPDATE_IMPORTED_SOURCE_STEP")
	}
	switch c.Type {
	case CommandUpdateImportedSourceStep, CommandRefreshImportedSource, CommandApplyImportedRelationshipProposal:
		return c.validateUpdateImportedSourceStep()
	case CommandCreateTable:
		return c.validateCreateTable()
	case CommandDuplicateTable:
		return c.validateDuplicateTable()
	case CommandDeleteTable:
		return c.validateDeleteTable()
	case CommandRenameTable:
		return c.validateRenameTable()
	case CommandReorderTables:
		return c.validateReorderTables()
	case CommandSetTableRoot:
		return c.validateSetTableRoot()
	case CommandApplyTableRootRebase:
		return c.validateApplyTableRootRebase()
	case CommandSetTablePopulation:
		return c.validateSetTablePopulation()
	case CommandClearTablePopulation:
		return c.validateClearTablePopulation()
	case CommandAddRoute:
		return c.validateAddRoute()
	case CommandUpdateRouteEdge:
		return c.validateUpdateRouteEdge()
	case CommandSetRouteMatchMode:
		return c.validateSetRouteMatchMode()
	case CommandRemoveRoute:
		return c.validateRemoveRoute()
	case CommandAddColumn:
		return c.validateAddColumn()
	case CommandUpdateColumn:
		return c.validateUpdateColumn()
	case CommandUpdateColumnRowValuePolicy:
		return c.validateUpdateColumnRowValuePolicy()
	case CommandUpdateConstructionOutput:
		return c.validateUpdateConstructionOutput()
	case CommandUpdateColumnTransformation:
		return c.validateUpdateColumnTransformation()
	case CommandSetColumnContributor:
		return c.validateSetColumnContributor()
	case CommandClearColumnContributor:
		return c.validateClearColumnContributor()
	case CommandApplyInterpretationCandidate:
		return c.validateApplyInterpretationCandidate()
	case CommandApplyColumnPolicyProposal, CommandApplyRowDefinitionProposal, CommandApplyConstructionProposal, CommandApplyPopulationMemberProposal:
		return c.validateApplyColumnPolicyProposal()
	case CommandRestoreDraftRevision:
		return c.validateRestoreDraftRevision()
	case CommandAddColumnSource:
		return c.validateAddColumnSource()
	case CommandUpdateColumnSource:
		return c.validateUpdateColumnSource()
	case CommandRemoveColumn:
		return c.validateRemoveColumn()
	case CommandRemoveProjectedField:
		return c.validateRemoveProjectedField()
	case CommandAddSemanticSelections:
		return c.validateAddSemanticSelections()
	case CommandApplyConstructionChoice:
		return c.validateApplyConstructionChoice()
	case CommandSetFrameSource:
		return c.validateSetFrameSource()
	case CommandReplaceFrameSource:
		return c.validateReplaceFrameSource()
	case CommandRemoveFrameSource:
		return c.validateRemoveFrameSource()
	default:
		return fmt.Errorf("unsupported command type %q", c.Type)
	}
}

func commandFieldsPresent(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func ApplyCommands(workspace Workspace, catalog CatalogSnapshot, commandID string, commands []Command) (Workspace, []CommandResult, error) {
	working, err := CloneCurrentWorkspace(workspace)
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
	case CommandAddRoute, CommandUpdateRouteEdge, CommandSetRouteMatchMode, CommandRemoveRoute:
		position := documentIndex(workspace, command.OutputID)
		if position >= 0 && workspace.Documents[position].ImportedRecipe != nil {
			return result, fmt.Errorf("IMPORTED_SOURCE_REQUIRED: edit imported relationships through a native source proposal")
		}
	}
	switch command.Type {
	case CommandApplyColumnPolicyProposal:
		return applyApplyColumnPolicyProposal(workspace, catalog, commandID, index, command)
	case CommandUpdateImportedSourceStep:
		return applyUpdateImportedSourceStepVariants(workspace, catalog, commandID, index, command)
	case CommandRefreshImportedSource:
		return applyUpdateImportedSourceStepVariants(workspace, catalog, commandID, index, command)
	case CommandApplyImportedRelationshipProposal:
		return applyUpdateImportedSourceStepVariants(workspace, catalog, commandID, index, command)
	case CommandSetFrameSource:
		return applySetFrameSourceVariants(workspace, catalog, commandID, index, command)
	case CommandReplaceFrameSource:
		return applySetFrameSourceVariants(workspace, catalog, commandID, index, command)
	case CommandRemoveFrameSource:
		return applySetFrameSourceVariants(workspace, catalog, commandID, index, command)
	case CommandCreateTable:
		return applyCreateTable(workspace, catalog, commandID, index, command)
	case CommandDuplicateTable:
		return applyDuplicateTable(workspace, catalog, commandID, index, command)
	case CommandDeleteTable:
		return applyDeleteTable(workspace, catalog, commandID, index, command)
	case CommandRenameTable:
		return applyRenameTable(workspace, catalog, commandID, index, command)
	case CommandReorderTables:
		return applyReorderTables(workspace, catalog, commandID, index, command)
	case CommandSetTableRoot:
		return applySetTableRoot(workspace, catalog, commandID, index, command)
	case CommandApplyTableRootRebase:
		return applyApplyTableRootRebase(workspace, catalog, commandID, index, command)
	case CommandSetTablePopulation:
		return applySetTablePopulation(workspace, catalog, commandID, index, command)
	case CommandClearTablePopulation:
		return applyClearTablePopulation(workspace, catalog, commandID, index, command)
	case CommandAddRoute:
		return applyAddRoute(workspace, catalog, commandID, index, command)
	case CommandUpdateRouteEdge:
		return applyUpdateRouteEdge(workspace, catalog, commandID, index, command)
	case CommandSetRouteMatchMode:
		return applySetRouteMatchMode(workspace, catalog, commandID, index, command)
	case CommandRemoveRoute:
		return applyRemoveRoute(workspace, catalog, commandID, index, command)
	case CommandAddColumn:
		return applyAddColumn(workspace, catalog, commandID, index, command)
	case CommandAddColumnSource:
		return applyAddColumnSource(workspace, catalog, commandID, index, command)
	case CommandApplyConstructionChoice:
		return applyApplyConstructionChoice(workspace, catalog, commandID, index, command)
	case CommandAddSemanticSelections:
		return applyAddSemanticSelections(workspace, catalog, commandID, index, command)
	case CommandUpdateColumn:
		return applyUpdateColumn(workspace, catalog, commandID, index, command)
	case CommandUpdateConstructionOutput:
		return applyUpdateConstructionOutput(workspace, catalog, commandID, index, command)
	case CommandUpdateColumnTransformation:
		return applyUpdateColumnTransformation(workspace, catalog, commandID, index, command)
	case CommandSetColumnContributor:
		return applySetColumnContributor(workspace, catalog, commandID, index, command)
	case CommandClearColumnContributor:
		return applyClearColumnContributor(workspace, catalog, commandID, index, command)
	case CommandApplyInterpretationCandidate:
		return applyApplyInterpretationCandidate(workspace, catalog, commandID, index, command)
	case CommandApplyRowDefinitionProposal:
		return applyApplyRowDefinitionProposal(workspace, catalog, commandID, index, command)
	case CommandApplyPopulationMemberProposal:
		return applyApplyPopulationMemberProposal(workspace, catalog, commandID, index, command)
	case CommandApplyConstructionProposal:
		return applyApplyConstructionProposal(workspace, catalog, commandID, index, command)
	case CommandRestoreDraftRevision:
		return applyRestoreDraftRevision(workspace, catalog, commandID, index, command)
	case CommandUpdateColumnSource:
		return applyUpdateColumnSource(workspace, catalog, commandID, index, command)
	case CommandUpdateColumnRowValuePolicy:
		return applyUpdateColumnRowValuePolicy(workspace, catalog, commandID, index, command)
	case CommandRemoveColumn:
		return applyRemoveColumn(workspace, catalog, commandID, index, command)
	case CommandRemoveProjectedField:
		return applyRemoveProjectedField(workspace, catalog, commandID, index, command)
	default:
		return result, fmt.Errorf("unsupported command type %q", command.Type)
	}
}

func authoredColumns(columns []Column) []Column {
	result := make([]Column, 0, len(columns))
	for _, column := range columns {
		if column.Source.Kind != SourceImported {
			result = append(result, column)
		}
	}
	return result
}

func removeDocumentColumnByID(workspace *Workspace, documentPos int, columnID string) error {
	if columnID == "" {
		return fmt.Errorf("columnId is required")
	}
	document := &workspace.Documents[documentPos]
	columnIndex := -1
	for index := range document.Columns {
		if document.Columns[index].ColumnID != columnID {
			continue
		}
		if columnIndex >= 0 {
			return fmt.Errorf("columnId %q is ambiguous", columnID)
		}
		columnIndex = index
	}
	if columnIndex < 0 {
		return fmt.Errorf("columnId %q was not found", columnID)
	}
	column := document.Columns[columnIndex]
	if column.Source.Kind == SourceImported {
		return fmt.Errorf("pinned imported fields require a lifecycle-resolved source edit")
	}
	document.Columns = append(document.Columns[:columnIndex], document.Columns[columnIndex+1:]...)
	removeConstructionColumnValues(document, column.ColumnID)
	removeExplicitGroupRowValue(document, column.ColumnID)
	cleanupDocumentReferences(document)
	cleanupWorkspaceBindings(workspace)
	return nil
}

func initializeEmptyConstructionBeforeSourceAdd(workspace *Workspace, outputID string) error {
	documentPos := documentIndex(workspace, outputID)
	if documentPos < 0 {
		return nil
	}
	document := workspace.Documents[documentPos]
	if len(document.Columns) != 0 || document.Construction != nil || document.ArchiveRetiredShape != nil {
		return nil
	}
	upgraded, err := StartDocumentConstruction(document)
	if err != nil {
		return fmt.Errorf("initialize empty source construction: %w", err)
	}
	workspace.Documents[documentPos] = upgraded
	return nil
}

func clonePopulationRouteSteps(route []PopulationRouteStep) []PopulationRouteStep {
	if route == nil {
		return nil
	}
	cloned := make([]PopulationRouteStep, len(route))
	copy(cloned, route)
	return cloned
}

func setPopulationRoute(document *Document, selectionRevisionID string, route []PopulationRouteStep) {
	steps := make([]PopulationRouteStep, len(route))
	copy(steps, route)
	document.Population = &Population{SelectionRevisionID: strings.TrimSpace(selectionRevisionID), Route: steps}
}

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
	return validateEditableSourcePolicy(document, catalog, occurrenceID, source, false)
}

func validateEditableSourcePolicy(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, allowImportedRecipe bool) error {
	if source.Kind == SourceImported || (!allowImportedRecipe && document.ImportedRecipe != nil) {
		return fmt.Errorf("imported recipe source columns are immutable")
	}
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
