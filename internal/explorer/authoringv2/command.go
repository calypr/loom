package authoringv2

import (
	"fmt"
	"reflect"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func ResolveColumnPolicyProposal(c Command, columnID, projectionMode string) (ColumnPolicyResolution, error) {
	if c.Type != CommandApplyColumnPolicyProposal || strings.TrimSpace(columnID) == "" || strings.TrimSpace(columnID) != columnID {
		return ColumnPolicyResolution{}, fmt.Errorf("column policy requires an exact resolved column identity")
	}
	switch projectionMode {
	case "VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT":
	default:
		return ColumnPolicyResolution{}, fmt.Errorf("column policy mode is unsupported")
	}
	return ColumnPolicyResolution{ColumnID: columnID, ProjectionMode: projectionMode}, nil
}

func (s SemanticSelection) validate() error {
	if strings.TrimSpace(s.ConceptID) == "" || strings.TrimSpace(s.BindingID) == "" {
		return fmt.Errorf("conceptId and bindingId are required")
	}
	if s.RouteEdgeIDs == nil {
		return fmt.Errorf("routeEdgeIds must be explicit, including an empty root route")
	}
	if !contains([]string{"VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT"}, strings.ToUpper(strings.TrimSpace(s.ProjectionMode))) {
		return fmt.Errorf("projectionMode must be an explicit supported value policy")
	}
	return nil
}

func (c Command) validateApplyInterpretationCandidate() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) || c.InterpretationCandidate() == nil {
		return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires outputId, column, and interpretationCandidate")
	}
	if !commandFieldsPresent(c.InterpretationCandidate().CandidateReceiptID, c.InterpretationCandidate().RevisionID) ||
		c.InterpretationCandidate().CandidateReceiptID != strings.TrimSpace(c.InterpretationCandidate().CandidateReceiptID) ||
		c.InterpretationCandidate().RevisionID != strings.TrimSpace(c.InterpretationCandidate().RevisionID) {
		return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE requires exact candidateReceiptId and revisionId")
	}
	return nil
}

func (c Command) validateApplyColumnPolicyProposal() error {
	if !commandFieldsPresent(c.OutputID, c.ProposalID()) || c.OutputID != strings.TrimSpace(c.OutputID) || c.ProposalID() != strings.TrimSpace(c.ProposalID()) {
		return fmt.Errorf("%s requires exact outputId and proposalId", c.Type)
	}
	if c.SourceOutputID() != "" || c.Title != "" || c.RootNodeID() != "" || c.SelectionRevisionID() != "" ||
		len(c.EdgeIDs()) != 0 || c.RouteChoiceID() != "" || c.ParentOccurrenceID() != "" || c.OccurrenceID() != "" || c.EdgeID() != "" || c.MatchMode() != "" ||
		c.CandidateID() != "" || c.ProjectionMode() != "" || c.InitialPresentation != "" || c.Column() != "" || c.ColumnValue() != nil || c.TransformationChange() != nil || c.Contributor() != nil ||
		c.Source() != nil || c.RowChange() != nil || c.InterpretationCandidate() != nil || c.ContextToken() != "" || len(c.SemanticSelections()) != 0 ||
		c.ConstructionChoice() != nil || len(c.OutputIDs()) != 0 {
		return fmt.Errorf("%s accepts only outputId and proposalId", c.Type)
	}
	return nil
}

func (c Command) validateRestoreDraftRevision() error {
	if !commandFieldsPresent(c.DraftRevisionID()) || c.DraftRevisionID() != strings.TrimSpace(c.DraftRevisionID()) {
		return fmt.Errorf("RESTORE_DRAFT_REVISION requires an exact draftRevisionId")
	}
	if c.OutputID != "" || c.SourceOutputID() != "" || c.Title != "" || c.RootNodeID() != "" || c.SelectionRevisionID() != "" ||
		len(c.EdgeIDs()) != 0 || c.RouteChoiceID() != "" || c.ParentOccurrenceID() != "" || c.OccurrenceID() != "" || c.EdgeID() != "" || c.MatchMode() != "" ||
		c.CandidateID() != "" || c.ProjectionMode() != "" || c.InitialPresentation != "" || c.Column() != "" || c.ColumnValue() != nil || c.TransformationChange() != nil || c.Contributor() != nil ||
		c.Source() != nil || c.RowChange() != nil || c.InterpretationCandidate() != nil || c.ProposalID() != "" || c.ContextToken() != "" || len(c.SemanticSelections()) != 0 ||
		c.ConstructionChoice() != nil || len(c.OutputIDs()) != 0 {
		return fmt.Errorf("RESTORE_DRAFT_REVISION accepts only draftRevisionId")
	}
	return nil
}

func (c Command) validateAddColumn() error {
	if !commandFieldsPresent(c.OutputID, c.OccurrenceID(), c.CandidateID()) {
		return fmt.Errorf("ADD_COLUMN requires outputId, occurrenceId, and candidateId")
	}
	if c.InitialPresentation != "" && !contains([]string{InitialPresentationTable, InitialPresentationFilter, InitialPresentationChart}, strings.ToUpper(strings.TrimSpace(c.InitialPresentation))) {
		return fmt.Errorf("ADD_COLUMN initialPresentation must be TABLE, FILTER, or CHART")
	}
	return nil
}

func (c Command) validateUpdateColumn() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) || c.ColumnValue() == nil {
		return fmt.Errorf("UPDATE_COLUMN requires outputId, column, and columnValue")
	}
	return nil
}

func (c Command) validateUpdateColumnRowValuePolicy() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) {
		return fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY requires outputId and column")
	}
	if c.RowValuePolicy() != ws.ConstructionRowValueAll && c.RowValuePolicy() != ws.ConstructionRowValueOne {
		return fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY rowValuePolicy must be ALL or ONE")
	}
	if c.SourceOutputID() != "" || c.Title != "" || c.RootNodeID() != "" || c.SelectionRevisionID() != "" ||
		len(c.EdgeIDs()) != 0 || c.RouteChoiceID() != "" || c.ParentOccurrenceID() != "" || c.OccurrenceID() != "" || c.EdgeID() != "" || c.MatchMode() != "" ||
		c.CandidateID() != "" || c.ProjectionMode() != "" || c.InitialPresentation != "" || c.ColumnValue() != nil || c.ConstructionOutput() != nil || c.TransformationChange() != nil || c.Contributor() != nil ||
		c.Source() != nil || c.RowChange() != nil || c.InterpretationCandidate() != nil || c.ProposalID() != "" || c.DraftRevisionID() != "" || c.ContextToken() != "" || len(c.SemanticSelections()) != 0 ||
		c.ConstructionChoice() != nil || c.FrameChoiceID() != "" || c.FrameID() != "" || c.FrameForm() != "" || len(c.OutputIDs()) != 0 {
		return fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY accepts only outputId, column, and rowValuePolicy")
	}
	return nil
}

func (c Command) validateUpdateConstructionOutput() error {
	if !commandFieldsPresent(c.OutputID) || c.ConstructionOutput() == nil {
		return fmt.Errorf("UPDATE_CONSTRUCTION_OUTPUT requires outputId and constructionOutput")
	}
	if err := c.ConstructionOutput().Validate(); err != nil {
		return fmt.Errorf("constructionOutput: %w", err)
	}
	return nil
}

func (c Command) validateUpdateColumnTransformation() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) || c.TransformationChange() == nil {
		return fmt.Errorf("UPDATE_COLUMN_TRANSFORMATION requires outputId, column, and transformationChange")
	}
	if err := c.TransformationChange().Validate(); err != nil {
		return fmt.Errorf("transformationChange: %w", err)
	}
	return nil
}

func (c Command) validateSetColumnContributor() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) || c.Contributor() == nil {
		return fmt.Errorf("SET_COLUMN_CONTRIBUTOR requires outputId, column, and contributor")
	}
	if err := c.Contributor().Validate(); err != nil {
		return fmt.Errorf("contributor: %w", err)
	}
	return nil
}

func (c Command) validateClearColumnContributor() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) {
		return fmt.Errorf("CLEAR_COLUMN_CONTRIBUTOR requires outputId and column")
	}
	return nil
}

func (c Command) validateAddColumnSource() error {
	if !commandFieldsPresent(c.OutputID, c.OccurrenceID()) || c.Source() == nil {
		return fmt.Errorf("ADD_COLUMN_SOURCE requires outputId, occurrenceId, and source")
	}
	if err := c.Source().ValidateSource("source"); err != nil {
		return err
	}
	if c.Contributor() != nil {
		if err := c.Contributor().Validate(); err != nil {
			return fmt.Errorf("contributor: %w", err)
		}
	}
	return nil
}

func (c Command) validateUpdateColumnSource() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) || c.Source() == nil {
		return fmt.Errorf("UPDATE_COLUMN_SOURCE requires outputId, column, and source")
	}
	if err := c.Source().ValidateSource("source"); err != nil {
		return err
	}
	return nil
}

func (c Command) validateRemoveColumn() error {
	if !commandFieldsPresent(c.OutputID, c.Column()) {
		return fmt.Errorf("REMOVE_COLUMN requires outputId and column")
	}
	return nil
}

func (c Command) validateRemoveProjectedField() error {
	if !commandFieldsPresent(c.OutputID, c.ColumnID()) || c.OutputID != strings.TrimSpace(c.OutputID) || c.ColumnID() != strings.TrimSpace(c.ColumnID()) {
		return fmt.Errorf("REMOVE_PROJECTED_FIELD requires exact outputId and columnId")
	}
	payload, ok := c.Payload.(*ColumnPayload)
	if !ok || !reflect.DeepEqual(*payload, ColumnPayload{ColumnID: c.ColumnID()}) || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("REMOVE_PROJECTED_FIELD accepts only outputId and columnId")
	}
	return nil
}

func (c Command) validateAddSemanticSelections() error {
	if !commandFieldsPresent(c.OutputID, c.ContextToken()) || len(c.SemanticSelections()) == 0 || len(c.SemanticSelections()) > 100 {
		return fmt.Errorf("ADD_SEMANTIC_SELECTIONS requires outputId, contextToken, and between 1 and 100 selections")
	}
	for i, selection := range c.SemanticSelections() {
		if err := selection.validate(); err != nil {
			return fmt.Errorf("semanticSelections[%d]: %w", i, err)
		}
	}
	return nil
}

func (c Command) validateApplyConstructionChoice() error {
	if !commandFieldsPresent(c.OutputID) || c.ConstructionChoice() == nil {
		return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE requires outputId and constructionChoice")
	}
	if err := c.ConstructionChoice().validate(); err != nil {
		return err
	}
	if c.SourceOutputID() != "" || c.RootNodeID() != "" || c.SelectionRevisionID() != "" ||
		len(c.EdgeIDs()) != 0 || c.ParentOccurrenceID() != "" || c.OccurrenceID() != "" || c.EdgeID() != "" || c.MatchMode() != "" ||
		c.CandidateID() != "" || c.ProjectionMode() != "" || c.Column() != "" || c.ColumnValue() != nil || c.TransformationChange() != nil || c.Contributor() != nil ||
		c.Source() != nil || c.RowChange() != nil || c.InterpretationCandidate() != nil || c.ContextToken() != "" ||
		len(c.SemanticSelections()) != 0 || len(c.OutputIDs()) != 0 {
		return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE accepts only its choice identity and authored-column presentation fields")
	}
	if c.InitialPresentation != "" && !contains([]string{InitialPresentationTable, InitialPresentationFilter, InitialPresentationChart}, strings.ToUpper(strings.TrimSpace(c.InitialPresentation))) {
		return fmt.Errorf("APPLY_CONSTRUCTION_CHOICE initialPresentation must be TABLE, FILTER, or CHART")
	}
	return nil
}

func (c Command) validateUpdateImportedSourceStep() error {
	if !commandFieldsPresent(c.OutputID) || c.OutputID != strings.TrimSpace(c.OutputID) {
		return fmt.Errorf("%s requires an exact outputId", c.Type)
	}
	if c.Type == CommandApplyImportedRelationshipProposal {
		if !commandFieldsPresent(c.ProposalID()) {
			return fmt.Errorf("proposalId is required")
		}
		return nil
	}
	if c.Type == CommandUpdateImportedSourceStep {
		if c.SourceStep() == nil {
			return fmt.Errorf("sourceStep is required")
		}
		return c.SourceStep().Validate()
	}
	if c.SourceStep() != nil {
		return fmt.Errorf("REFRESH_IMPORTED_SOURCE accepts only outputId")
	}
	return nil
}

func (c Command) validateSetFrameSource() error {
	if !commandFieldsPresent(c.OutputID, c.FrameChoiceID()) || c.FrameForm() == "" {
		return fmt.Errorf("SET_FRAME_SOURCE requires outputId, frameChoiceId, and form")
	}
	if c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameChoiceID() != strings.TrimSpace(c.FrameChoiceID()) || !validFrameForm(c.FrameForm()) ||
		c.FrameID() != "" || c.ConstructionChoice() != nil || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("SET_FRAME_SOURCE accepts only exact outputId, frameChoiceId, and supported form")
	}
	return nil
}

func (c Command) validateReplaceFrameSource() error {
	if !commandFieldsPresent(c.OutputID, c.FrameID(), c.FrameChoiceID()) || c.FrameForm() == "" {
		return fmt.Errorf("REPLACE_FRAME_SOURCE requires outputId, frameId, frameChoiceId, and form")
	}
	if c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameID() != strings.TrimSpace(c.FrameID()) || c.FrameChoiceID() != strings.TrimSpace(c.FrameChoiceID()) || !validFrameForm(c.FrameForm()) ||
		c.ConstructionChoice() != nil || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("REPLACE_FRAME_SOURCE accepts only exact outputId, frameId, frameChoiceId, and supported form")
	}
	return nil
}

func (c Command) validateRemoveFrameSource() error {
	if !commandFieldsPresent(c.OutputID, c.FrameID()) || c.OutputID != strings.TrimSpace(c.OutputID) || c.FrameID() != strings.TrimSpace(c.FrameID()) ||
		c.FrameChoiceID() != "" || c.FrameForm() != "" || c.ConstructionChoice() != nil || c.Title != "" || c.InitialPresentation != "" {
		return fmt.Errorf("REMOVE_FRAME_SOURCE requires exact outputId and frameId only")
	}
	return nil
}

func (c Command) validateCreateTable() error {
	if !commandFieldsPresent(c.Title, c.RootNodeID()) {
		return fmt.Errorf("CREATE_TABLE requires title and rootNodeId")
	}
	return nil
}

func (c Command) validateDuplicateTable() error {
	if !commandFieldsPresent(c.SourceOutputID(), c.Title) {
		return fmt.Errorf("DUPLICATE_TABLE requires sourceOutputId and title")
	}
	return nil
}

func (c Command) validateDeleteTable() error {
	if !commandFieldsPresent(c.OutputID) {
		return fmt.Errorf("DELETE_TABLE requires outputId")
	}
	return nil
}

func (c Command) validateRenameTable() error {
	if !commandFieldsPresent(c.OutputID, c.Title) {
		return fmt.Errorf("RENAME_TABLE requires outputId and title")
	}
	return nil
}

func (c Command) validateReorderTables() error {
	if len(c.OutputIDs()) == 0 {
		return fmt.Errorf("REORDER_TABLES requires outputIds")
	}
	return nil
}

func (c Command) validateSetTableRoot() error {
	if !commandFieldsPresent(c.OutputID, c.RootNodeID()) {
		return fmt.Errorf("SET_TABLE_ROOT requires outputId and rootNodeId")
	}
	return nil
}

func (c Command) validateApplyTableRootRebase() error {
	if c.RowChange() == nil {
		return fmt.Errorf("APPLY_TABLE_ROOT_REBASE requires rowChange")
	}
	if err := c.RowChange().validate(); err != nil {
		return err
	}
	if c.OutputID != "" && c.OutputID != c.RowChange().OutputID {
		return fmt.Errorf("APPLY_TABLE_ROOT_REBASE outputId must match rowChange.outputId")
	}
	return nil
}

func (c Command) validateSetTablePopulation() error {
	if !commandFieldsPresent(c.OutputID, c.SelectionRevisionID()) {
		return fmt.Errorf("SET_TABLE_POPULATION requires outputId and selectionRevisionId")
	}
	if strings.TrimSpace(c.RouteChoiceID()) != "" && len(c.EdgeIDs()) != 0 {
		return fmt.Errorf("SET_TABLE_POPULATION cannot combine routeChoiceId with client edgeIds")
	}
	return nil
}

func (c Command) validateClearTablePopulation() error {
	if !commandFieldsPresent(c.OutputID) {
		return fmt.Errorf("CLEAR_TABLE_POPULATION requires outputId")
	}
	return nil
}

func (c Command) validateAddRoute() error {
	if !commandFieldsPresent(c.OutputID, c.ParentOccurrenceID(), c.EdgeID()) {
		return fmt.Errorf("ADD_ROUTE requires outputId, parentOccurrenceId, and edgeId")
	}
	return nil
}

func (c Command) validateUpdateRouteEdge() error {
	if !commandFieldsPresent(c.OutputID, c.OccurrenceID(), c.EdgeID()) || c.OccurrenceID() == ws.RootOccurrenceID {
		return fmt.Errorf("UPDATE_ROUTE_EDGE requires a non-root occurrenceId and edgeId")
	}
	return nil
}

func (c Command) validateSetRouteMatchMode() error {
	if !commandFieldsPresent(c.OutputID, c.OccurrenceID(), string(c.MatchMode())) || c.OccurrenceID() == ws.RootOccurrenceID {
		return fmt.Errorf("SET_ROUTE_MATCH_MODE requires a non-root occurrenceId and matchMode")
	}
	if c.MatchMode() != ws.RouteMatchOptional && c.MatchMode() != ws.RouteMatchRequired {
		return fmt.Errorf("SET_ROUTE_MATCH_MODE matchMode must be OPTIONAL or REQUIRED")
	}
	return nil
}

func (c Command) validateRemoveRoute() error {
	if !commandFieldsPresent(c.OutputID, c.OccurrenceID()) || c.OccurrenceID() == ws.RootOccurrenceID {
		return fmt.Errorf("REMOVE_ROUTE requires a non-root occurrenceId")
	}
	return nil
}
