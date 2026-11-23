package workspace

import (
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
)

func (d Document) Validate() error { return d.validate(false) }

func (d Document) validate(allowArchive bool) error {
	if d.ArchiveRetiredShape != nil && !allowArchive {
		return fmt.Errorf("tableShape requires explicit stored-data migration")
	}
	if d.Kind != Kind {
		return fmt.Errorf("unsupported V2 document protocol or kind")
	}
	if emptyID(d.Output.ID) || strings.TrimSpace(d.Output.Title) == "" {
		return fmt.Errorf("output id and title are required")
	}
	if !physicalColumnPattern.MatchString(d.Output.ID) {
		return fmt.Errorf("output id must contain only letters, digits, and underscores and may not start with a digit")
	}
	if err := d.Rows.Validate(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	if err := d.validateSemantic(); err != nil {
		return err
	}
	if err := validateDocumentFrames(d); err != nil {
		return fmt.Errorf("framing: %w", err)
	}
	if d.Construction != nil {
		if d.ArchiveRetiredShape != nil {
			return fmt.Errorf("construction and tableShape cannot both define post-source operations")
		}
		var constructionErr error
		if d.Rows.Kind == RowDefinitionGroups && d.Rows.Groups != nil && d.Rows.Groups.Source.Kind == GroupSourceExplicit {
			constructionErr = d.Construction.ValidateWithExplicitGroups(d.Columns, d.Rows.Groups)
		} else {
			constructionErr = d.Construction.Validate(d.Columns)
		}
		if constructionErr != nil {
			return fmt.Errorf("construction: %w", constructionErr)
		}
	} else if d.Rows.Kind == RowDefinitionGroups && d.Rows.Groups != nil && d.Rows.Groups.Source.Kind == GroupSourceExplicit && d.Rows.Groups.AfterStepID != "" {
		return fmt.Errorf("rows.groups.afterStepId requires an authored construction step")
	} else if err := d.ArchiveRetiredShape.Validate(d.Columns); err != nil {
		return fmt.Errorf("tableShape: %w", err)
	}
	return nil
}

func (w Workspace) Validate() error { return w.validate(false) }

func (w Workspace) validate(allowArchive bool) error {
	if w.APIVersion != APIVersion || w.Kind != WorkspaceKind {
		return fmt.Errorf("unsupported V2 workspace protocol or kind")
	}
	if w.SemanticsVersion > CurrentSemanticsVersion {
		return fmt.Errorf("UNSUPPORTED_SEMANTICS_VERSION: semanticsVersion %d is unsupported", w.SemanticsVersion)
	}
	if w.SemanticsVersion < tableShapeSemanticsVersion && workspaceHasArchivedShape(w) {
		return fmt.Errorf("tableShape requires semanticsVersion %d", tableShapeSemanticsVersion)
	}
	if w.SemanticsVersion >= CurrentSemanticsVersion && workspaceHasLegacyContributors(w) {
		return fmt.Errorf("aggregate source where is not writable in semantics version %d; use column.contributor", CurrentSemanticsVersion)
	}
	if strings.TrimSpace(w.Explorer.Title) == "" {
		return fmt.Errorf("explorer.title is required")
	}
	outputs, tabs, tabOutputs, orders := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[int]bool{}
	for i, d := range w.Documents {
		if err := d.validate(allowArchive); err != nil {
			return fmt.Errorf("documents[%d]: %w", i, err)
		}
		if outputs[d.Output.ID] {
			return fmt.Errorf("DUPLICATE_OUTPUT_ID: documents[%d].output.id", i)
		}
		outputs[d.Output.ID] = true
	}
	for i, tab := range w.Tabs {
		if emptyID(tab.ID) || strings.TrimSpace(tab.Title) == "" || emptyID(tab.OutputID) || tab.Order < 0 {
			return fmt.Errorf("tabs[%d] is invalid", i)
		}
		if tabs[tab.ID] {
			return fmt.Errorf("DUPLICATE_TAB_ID: tabs[%d].id", i)
		}
		if tabOutputs[tab.OutputID] || !outputs[tab.OutputID] {
			return fmt.Errorf("INVALID_TAB_OUTPUT_MAPPING: tabs[%d].outputId", i)
		}
		if orders[tab.Order] {
			return fmt.Errorf("INVALID_TAB_ORDER: tabs[%d].order", i)
		}
		tabs[tab.ID], tabOutputs[tab.OutputID], orders[tab.Order] = true, true, true
	}
	if len(w.Tabs) != len(w.Documents) {
		return fmt.Errorf("INVALID_TAB_OUTPUT_MAPPING: exactly one tab is required per document")
	}
	for output := range outputs {
		if !tabOutputs[output] {
			return fmt.Errorf("INVALID_TAB_OUTPUT_MAPPING: output %q has no tab", output)
		}
	}
	for i := 0; i < len(orders); i++ {
		if !orders[i] {
			return fmt.Errorf("INVALID_TAB_ORDER: orders must be contiguous from zero")
		}
	}
	if _, err := w.OutputDependencyOrder(); err != nil {
		return err
	}
	if err := w.validateSemanticBindings(); err != nil {
		return err
	}
	return nil
}

func (w Workspace) ValidateForPublication() error {
	visibleOutputs := make(map[string]bool, len(w.Tabs))
	for _, tab := range w.Tabs {
		visibleOutputs[tab.OutputID] = tab.Visible
	}
	for i, document := range w.Documents {
		if !visibleOutputs[document.Output.ID] {
			continue
		}
		for columnIndex, column := range document.Columns {
			if column.OccurrenceID == RootOccurrenceID || column.Source.Kind != SourceField || column.Source.Field == nil {
				continue
			}
			mode := strings.ToUpper(strings.TrimSpace(column.Source.Field.ProjectionMode))
			if mode == "" {
				mode = "FIRST"
			}
			if mode != "VALUE" && mode != "FIRST" && mode != "INDEXED" {
				continue
			}
			selection := column.Source.Field.RelatedSelection
			if selection == nil || selection.Kind != "first-by-resource-key" || !selection.Acknowledged {
				return fmt.Errorf("UNACKNOWLEDGED_RELATED_FIRST: documents[%d].columns[%d] requires relatedSelection acknowledgement", i, columnIndex)
			}
		}
		visible := 0
		for _, column := range document.Columns {
			if column.Table != nil && (column.Table.Visible == nil || *column.Table.Visible) {
				visible++
			}
		}
		if visible == 0 && document.Construction != nil && len(document.Construction.Steps) > 0 {
			outputs := document.Construction.Steps[len(document.Construction.Steps)-1].Outputs
			for _, output := range outputs {
				if output.Table == nil || output.Table.Visible == nil || *output.Table.Visible {
					visible++
				}
			}
		}
		if visible == 0 {
			return fmt.Errorf("NO_VISIBLE_COLUMNS: documents[%d]", i)
		}
	}
	return nil
}

func (c CatalogSnapshot) Validate() error {
	if c.APIVersion != APIVersion || c.Kind != CatalogKind {
		return fmt.Errorf("unsupported V2 catalog protocol or kind")
	}
	if strings.TrimSpace(c.SnapshotToken) == "" {
		return fmt.Errorf("catalog snapshotToken is required")
	}
	if strings.TrimSpace(c.Project) == "" || strings.TrimSpace(c.ExplorerID) == "" || strings.TrimSpace(c.SourceGeneration) == "" || strings.TrimSpace(c.AuthorizationScopeDigest) == "" {
		return fmt.Errorf("catalog project, explorerId, sourceGeneration, and authorizationScopeDigest are required")
	}
	if !c.Complete || c.Truncated {
		return fmt.Errorf("catalog snapshot is incomplete or truncated")
	}
	if c.RoutePolicy.MaxHops != nil && *c.RoutePolicy.MaxHops < 0 {
		return fmt.Errorf("routePolicy.maxHops must not be negative")
	}
	if c.RoutePolicy.MaxHops == nil && !c.RoutePolicy.Unbounded {
		return fmt.Errorf("routePolicy must explicitly declare an unbounded route when maxHops is absent")
	}
	if c.RoutePolicy.MaxHops != nil && c.RoutePolicy.Unbounded {
		return fmt.Errorf("routePolicy cannot be both bounded and unbounded")
	}
	nodes := map[string]CatalogNode{}
	for i, n := range c.Nodes {
		if emptyID(n.ID) || strings.TrimSpace(n.ResourceType) == "" {
			return fmt.Errorf("nodes[%d] is invalid", i)
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("duplicate catalog node %q", n.ID)
		}
		nodes[n.ID] = n
	}
	edges := map[string]CatalogEdge{}
	for i, e := range c.Edges {
		if emptyID(e.ID) || nodes[e.FromNodeID].ID == "" || nodes[e.ToNodeID].ID == "" {
			return fmt.Errorf("edges[%d] has stale node reference", i)
		}
		if e.StorageDirection != "" && e.StorageDirection != "INBOUND" && e.StorageDirection != "OUTBOUND" {
			return fmt.Errorf("edges[%d] has an unsupported storageDirection", i)
		}
		if _, ok := edges[e.ID]; ok {
			return fmt.Errorf("duplicate catalog edge %q", e.ID)
		}
		if e.FromNodeID == e.ToNodeID && !c.RoutePolicy.AllowSelfLoops {
			return fmt.Errorf("self-loop edge %q is not allowed by route policy", e.ID)
		}
		edges[e.ID] = e
	}
	candidates := map[string]CatalogCandidate{}
	for i, candidate := range c.Candidates {
		if emptyID(candidate.ID) || nodes[candidate.NodeID].ID == "" {
			return fmt.Errorf("candidates[%d] has stale node reference", i)
		}
		if _, ok := candidates[candidate.ID]; ok {
			return fmt.Errorf("duplicate catalog candidate %q", candidate.ID)
		}
		if len(candidate.ProjectionModes) == 0 || candidate.DefaultProjectionMode == "" {
			return fmt.Errorf("candidate %q must advertise projection modes and a default", candidate.ID)
		}
		if err := validateCandidateConstructionChoice(candidate, nodes[candidate.NodeID], c.SnapshotToken); err != nil {
			return fmt.Errorf("candidate %q: %w", candidate.ID, err)
		}
		if candidate.DefaultProjectionMode != "" {
			foundMode := false
			for _, mode := range candidate.ProjectionModes {
				if strings.TrimSpace(mode) == "" {
					return fmt.Errorf("candidates[%d] contains an empty projection mode", i)
				}
				if mode == candidate.DefaultProjectionMode {
					foundMode = true
				}
			}
			if len(candidate.ProjectionModes) > 0 && !foundMode {
				return fmt.Errorf("candidate %q default projection mode is not listed", candidate.ID)
			}
		}
		if candidate.Count != nil && *candidate.Count < 0 {
			return fmt.Errorf("candidate %q count must not be negative", candidate.ID)
		}
		if candidate.SuggestionCount < 0 {
			return fmt.Errorf("candidate %q suggestionCount must not be negative", candidate.ID)
		}
		if candidate.SuggestionsComplete && candidate.SuggestionsTruncated {
			return fmt.Errorf("candidate %q suggestions cannot be complete and truncated", candidate.ID)
		}
		candidates[candidate.ID] = candidate
	}
	return nil
}

func validateCandidateConstructionChoice(candidate CatalogCandidate, node CatalogNode, snapshotToken string) error {
	choice := candidate.ConstructionChoice
	if choice == nil {
		return fmt.Errorf("constructionChoice is required for selectable catalog candidates")
	}
	if strings.TrimSpace(choice.ChoiceID) == "" || len(choice.Options) == 0 {
		return fmt.Errorf("constructionChoice requires an id and at least one compiler-proved option")
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		return fmt.Errorf("constructionChoice id is invalid: %w", err)
	}
	source, ok := choice.Source.(capability.FieldChoiceSource)
	if !ok || identity.Kind != capability.ConstructionChoiceSourceField || identity.SnapshotToken != snapshotToken ||
		source.Kind != capability.ConstructionChoiceSourceField || source.CandidateID != candidate.ID ||
		source.NodeID != candidate.NodeID || source.ResourceType != node.ResourceType ||
		source.Path != candidate.FieldPath || source.Cardinality != candidate.Cardinality {
		return fmt.Errorf("constructionChoice source does not match the pinned field candidate")
	}
	identitySource, ok := identity.Source.(capability.FieldChoiceSource)
	if !ok || !reflect.DeepEqual(identitySource, source) {
		return fmt.Errorf("constructionChoice id does not pin its field source")
	}
	defaults := 0
	for index, option := range choice.Options {
		if option.RowEffect != capability.ConstructionChoicePreservesRows || strings.TrimSpace(option.Reason) == "" {
			return fmt.Errorf("constructionChoice.options[%d] is incomplete", index)
		}
		switch option.Form {
		case capability.ConstructionChoiceValue, capability.ConstructionChoiceFirst:
			if option.Shape != capability.ConstructionChoiceScalar {
				return fmt.Errorf("constructionChoice.options[%d] has a mismatched scalar form and shape", index)
			}
		case capability.ConstructionChoiceAll, capability.ConstructionChoiceDistinct:
			if option.Shape != capability.ConstructionChoiceList {
				return fmt.Errorf("constructionChoice.options[%d] has a mismatched list form and shape", index)
			}
		default:
			return fmt.Errorf("constructionChoice.options[%d] has an unknown form", index)
		}
		if option.Decision != capability.ConstructionChoiceDefault && option.Decision != capability.ConstructionChoiceRequiresDecision {
			return fmt.Errorf("constructionChoice.options[%d] has an unknown decision", index)
		}
		wantPreservation := capability.ConstructionChoicePreserving
		if option.Form == capability.ConstructionChoiceFirst || option.Form == capability.ConstructionChoiceDistinct {
			wantPreservation = capability.ConstructionChoiceReducing
		}
		if option.Preservation != wantPreservation {
			return fmt.Errorf("constructionChoice.options[%d] has incorrect preservation semantics", index)
		}
		if option.Form == capability.ConstructionChoiceFirst && option.Decision == capability.ConstructionChoiceDefault {
			return fmt.Errorf("constructionChoice.options[%d] cannot default to FIRST", index)
		}
		if option.Decision == capability.ConstructionChoiceDefault && option.Form != capability.ConstructionChoiceValue && option.Form != capability.ConstructionChoiceAll {
			return fmt.Errorf("constructionChoice.options[%d] has a reducing default", index)
		}
		if option.Decision == capability.ConstructionChoiceDefault {
			defaults++
		}
		if option.Support != capability.ConstructionChoiceSupported {
			return fmt.Errorf("constructionChoice.options[%d] is not compiler-supported", index)
		}
	}
	if defaults > 1 {
		return fmt.Errorf("constructionChoice has multiple default options")
	}
	return nil
}

func (s BuilderState) Validate() error {
	if s.DraftVersion < 0 {
		return fmt.Errorf("draftVersion must not be negative")
	}
	if s.APIVersion != APIVersion || s.Kind != StateKind {
		return fmt.Errorf("unsupported V2 builder state protocol or kind")
	}
	if err := s.Catalog.Validate(); err != nil {
		return err
	}
	if s.LifecycleState == "" {
		if s.Workspace == nil {
			s.LifecycleState = LifecycleNew
		} else {
			s.LifecycleState = LifecycleReady
		}
	}
	if s.LifecycleState != LifecycleNew && s.LifecycleState != LifecycleReady {
		return fmt.Errorf("unsupported lifecycleState %q", s.LifecycleState)
	}
	if s.LifecycleState == LifecycleNew && s.Workspace != nil {
		return fmt.Errorf("NEW Builder state must have workspace null")
	}
	if s.LifecycleState == LifecycleReady && s.Workspace == nil {
		return fmt.Errorf("READY Builder state requires workspace")
	}
	if s.Workspace == nil {
		return nil
	}
	return s.Workspace.Validate()
}

func emptyID(value string) bool {
	return strings.TrimSpace(value) == "" || value != strings.TrimSpace(value)
}

const maxCodedPivotCategories = 50

func explicitGroupStageColumns(sourceColumns []Column, groups *GroupedRows) ([]StageColumn, error) {
	if groups == nil || groups.Source.Kind != GroupSourceExplicit {
		return nil, fmt.Errorf("explicit group source is required")
	}
	byID := make(map[string]Column, len(sourceColumns))
	for _, column := range sourceColumns {
		if column.ColumnID != "" {
			if _, exists := byID[column.ColumnID]; exists {
				return nil, fmt.Errorf("source columns contain duplicate columnId %q", column.ColumnID)
			}
			byID[column.ColumnID] = column
		}
	}
	columns := []StageColumn{
		{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
		{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
		{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
		{ID: "members", Name: "members", Label: "Members", Type: "array"},
	}
	seen := make(map[string]bool, len(columns)+len(groups.RowValues))
	for _, column := range columns {
		seen[column.ID] = true
		seen[column.Name] = true
	}
	for _, selected := range groups.RowValues {
		column, ok := byID[selected.ColumnID]
		if !ok || !supportsExplicitGroupRowValueColumn(column) {
			return nil, fmt.Errorf("row value columnId %q is not a supported root field projection", selected.ColumnID)
		}
		if seen[column.ColumnID] || seen[column.Column] {
			return nil, fmt.Errorf("row value column %q collides with a cohort stage column", selected.ColumnID)
		}
		seen[column.ColumnID], seen[column.Column] = true, true
		typ := column.LogicalType
		if typ == "" || selected.Policy == ConstructionRowValueAll {
			typ = "INFER"
			if selected.Policy == ConstructionRowValueAll {
				typ = "array"
			}
		}
		columns = append(columns, StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: typ})
	}
	return columns, nil
}

func validateConstructionStepOwnership(construction *Construction) error {
	groupCodedPivotOwners := make(map[string]bool)
	for index, step := range construction.Steps {
		if step.OwnerStepID == "" {
			continue
		}
		if !requiredID(step.OwnerStepID) {
			return fmt.Errorf("step %q ownerStepId must be an exact step id", step.ID)
		}
		ownerIndex := findConstructionStep(construction.Steps, step.OwnerStepID)
		if ownerIndex <= index || ownerIndex < 0 {
			return fmt.Errorf("step %q ownerStepId must name a later operation", step.ID)
		}
		owner := construction.Steps[ownerIndex]
		switch step.Operation.Kind {
		case ConstructionOperationRelatedField:
			if step.Operation.RelatedField == nil ||
				(owner.Operation.Kind != ConstructionOperationPivot && owner.Operation.Kind != ConstructionOperationGroup) ||
				!containsConstructionColumnID(owner.inputColumnIDs(), step.Operation.RelatedField.OutputColumnID) {
				return fmt.Errorf("step %q is not consumed by its owner PIVOT or GROUP", step.ID)
			}
		case ConstructionOperationCodedPivot:
			if step.Operation.CodedPivot == nil || index != 0 || ownerIndex != 1 ||
				owner.Operation.Kind != ConstructionOperationGroup || owner.Operation.Group == nil {
				return fmt.Errorf("step %q CODED_PIVOT owner must be the immediately following GROUP at the direct source stage", step.ID)
			}
			if groupCodedPivotOwners[step.OwnerStepID] {
				return fmt.Errorf("GROUP %q may own only one CODED_PIVOT input", step.OwnerStepID)
			}
			groupCodedPivotOwners[step.OwnerStepID] = true
			if step.Operation.CodedPivot.DuplicatePolicy != ConstructionPivotDuplicateError {
				return fmt.Errorf("GROUP-owned CODED_PIVOT %q must reject duplicate coded values", step.ID)
			}
			groupInputs := owner.inputColumnIDs()
			for _, category := range step.Operation.CodedPivot.Categories {
				if !constructionGroupKeyConsumes(owner.Operation.Group, category.OutputColumnID) {
					return fmt.Errorf("GROUP-owned CODED_PIVOT output %q must be used as a GROUP key", category.OutputColumnID)
				}
			}
			for _, value := range step.RowValues {
				if value.Policy != ConstructionRowValueOne {
					return fmt.Errorf("GROUP-owned CODED_PIVOT row value %q must use ONE selection", value.OutputColumnID)
				}
				if !containsConstructionColumnID(groupInputs, value.OutputColumnID) {
					return fmt.Errorf("GROUP-owned CODED_PIVOT row value %q is not used by its GROUP", value.OutputColumnID)
				}
			}
		default:
			return fmt.Errorf("step %q ownerStepId is only valid for generated reshape inputs", step.ID)
		}
	}
	return nil
}

func constructionGroupKeyConsumes(group *ConstructionGroup, columnID string) bool {
	if group == nil {
		return false
	}
	for _, key := range group.Keys {
		if key.InputColumnID == columnID {
			return true
		}
	}
	return false
}

func validateConstructionSourceProjections(construction *Construction, sourceColumns []Column) error {
	if construction == nil || len(construction.SourceProjections) == 0 {
		return nil
	}
	if len(construction.Steps) == 0 {
		return fmt.Errorf("source projections require a consuming construction step")
	}
	seenProjectionIDs := make(map[string]bool, len(construction.SourceProjections))
	for _, sourceProjection := range construction.SourceProjections {
		if !requiredID(sourceProjection.ColumnID) || !requiredID(sourceProjection.OccurrenceID) ||
			!requiredID(sourceProjection.FieldPath) || !requiredID(sourceProjection.FHIRType) ||
			!requiredID(sourceProjection.LogicalType) || strings.TrimSpace(sourceProjection.Label) == "" {
			return fmt.Errorf("source projection requires exact column, occurrence, field, type, and label facts")
		}
		if seenProjectionIDs[sourceProjection.ColumnID] {
			return fmt.Errorf("source projections contain duplicate columnId %q", sourceProjection.ColumnID)
		}
		seenProjectionIDs[sourceProjection.ColumnID] = true
		if sourceProjection.OwnerStepID == "" {
			if err := validateLegacyGroupSourceProjection(construction, sourceProjection); err != nil {
				return err
			}
		} else {
			if err := validatePivotSourceProjection(construction, sourceProjection); err != nil {
				return err
			}
		}
		if sourceProjection.OccurrenceID != RootOccurrenceID {
			return fmt.Errorf("source projections currently require the root occurrence")
		}
		for _, column := range sourceColumns {
			if column.ColumnID == sourceProjection.ColumnID {
				return fmt.Errorf("source projection columnId %q collides with a public source column", sourceProjection.ColumnID)
			}
		}
	}
	return nil
}

func validateLegacyGroupSourceProjection(construction *Construction, projection ConstructionSourceProjection) error {
	first := construction.Steps[0]
	if first.Operation.Kind != ConstructionOperationGroup || first.Operation.Group == nil ||
		len(first.Inputs) != 1 || first.Inputs[0].Kind != ConstructionInputSourceProjection {
		return fmt.Errorf("unowned source projections are supported only by a direct-source GROUP step")
	}
	group := first.Operation.Group
	if len(construction.SourceProjections) != 1 || len(group.Keys) != 1 || len(group.Aggregates) != 1 || group.Aggregates[0].Operation != ConstructionGroupCountRows {
		return fmt.Errorf("unowned source projections require one GROUP key and one COUNT_ROWS aggregate")
	}
	if group.Keys[0].InputColumnID != projection.ColumnID {
		return fmt.Errorf("unowned source projection must be the direct-source GROUP key")
	}
	return nil
}

func validatePivotSourceProjection(construction *Construction, projection ConstructionSourceProjection) error {
	if !requiredID(projection.OwnerStepID) {
		return fmt.Errorf("source projection ownerStepId must be an exact step id")
	}
	ownerIndex := findConstructionStep(construction.Steps, projection.OwnerStepID)
	if ownerIndex < 0 {
		return fmt.Errorf("source projection owner step %q does not exist", projection.OwnerStepID)
	}
	owner := construction.Steps[ownerIndex]
	switch owner.Operation.Kind {
	case ConstructionOperationPivot, ConstructionOperationGroup:
		if !containsConstructionColumnID(owner.inputColumnIDs(), projection.ColumnID) {
			return fmt.Errorf("source projection must be consumed by its owner PIVOT step")
		}
	case ConstructionOperationFilter:
		if owner.Operation.Filter == nil || owner.Operation.Filter.ColumnID != projection.ColumnID {
			return fmt.Errorf("source projection columnId %q is not the column filtered by its owner FILTER step", projection.ColumnID)
		}
	case ConstructionOperationDerive:
		if owner.Operation.Derive == nil || !containsConstructionColumnID(owner.inputColumnIDs(), projection.ColumnID) {
			return fmt.Errorf("source projection columnId %q is not a COLUMN operand of its owner DERIVE step", projection.ColumnID)
		}
	case ConstructionOperationExpand:
		if owner.Operation.Expand == nil || owner.Operation.Expand.InputColumnID != projection.ColumnID {
			return fmt.Errorf("source projection columnId %q is not the array input of its owner EXPAND step", projection.ColumnID)
		}
	case ConstructionOperationUnpivot:
		if owner.Operation.Unpivot == nil {
			return fmt.Errorf("source projection owner step %q has no unpivot payload", projection.OwnerStepID)
		}
		melted := false
		for _, input := range owner.Operation.Unpivot.Inputs {
			if input.ColumnID == projection.ColumnID {
				melted = true
				break
			}
		}
		if !melted {
			return fmt.Errorf("source projection columnId %q is not a melt input of its owner UNPIVOT step", projection.ColumnID)
		}
	default:
		return fmt.Errorf("source projection must be consumed by its owner PIVOT step")
	}
	for index := 0; index < ownerIndex; index++ {
		step := construction.Steps[index]
		if !ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return fmt.Errorf("source projection cannot cross %s before its owner PIVOT", step.Operation.Kind)
		}
		found := false
		for _, output := range step.Outputs {
			if output.ID == projection.ColumnID && output.Type == projection.LogicalType {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("step %q does not carry source projection %q to its owner PIVOT", step.ID, projection.ColumnID)
		}
	}
	return nil
}

func ConstructionCanCarrySourceProjection(kind ConstructionOperationKind) bool {
	switch kind {
	case ConstructionOperationDerive, ConstructionOperationFilter, ConstructionOperationExpand,
		ConstructionOperationRelatedSource, ConstructionOperationRelatedExpand,
		ConstructionOperationRelatedEligibility, ConstructionOperationRelatedField:
		return true
	default:
		return false
	}
}

func containsConstructionColumnID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func constructionStepInputSchema(steps []ConstructionStep, source []StageColumn, index int) ([]StageColumn, error) {
	step := steps[index]
	if len(step.Inputs) != 1 {
		return nil, fmt.Errorf("requires exactly one input")
	}
	input := step.Inputs[0]
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("inputs[0]: %w", err)
	}
	if step.Operation.Kind == ConstructionOperationCodedGroup {
		if index != 0 || input.Kind != ConstructionInputSourceProjection {
			return nil, fmt.Errorf("CODED_GROUP currently requires the direct source projection stage; prior construction stages are not supported")
		}
	}
	if step.Operation.Kind == ConstructionOperationCodedPivot {
		if index != 0 || input.Kind != ConstructionInputSourceProjection {
			return nil, fmt.Errorf("CODED_PIVOT currently requires the direct source projection stage; prior construction stages are not supported")
		}
	}
	switch input.Kind {
	case ConstructionInputSourceProjection:
		if index != 0 {
			return nil, fmt.Errorf("must consume the preceding step output")
		}
		return source, nil
	case ConstructionInputStepOutput:
		if index == 0 || input.StepID != steps[index-1].ID {
			return nil, fmt.Errorf("must consume the preceding step output")
		}
		return steps[index-1].Outputs, nil
	case ConstructionInputTableRevision:
		return nil, fmt.Errorf("operation %q does not support table revision inputs", step.Operation.Kind)
	default:
		return nil, fmt.Errorf("inputs[0] has unsupported kind %q", input.Kind)
	}
}

func hasCombineOperation(steps []ConstructionStep) bool {
	for _, step := range steps {
		if step.Operation.Kind == ConstructionOperationCombine || step.Operation.Combine != nil {
			return true
		}
	}
	return false
}

func validateConstructionCombineStep(step ConstructionStep) error {
	if !requiredID(step.ID) || step.ID == "source_projection" {
		return fmt.Errorf("combine step id is required and must not be reserved")
	}
	if len(step.Inputs) < 2 {
		return fmt.Errorf("combine requires at least two exact inputs")
	}
	seenInputs := make(map[string]bool, len(step.Inputs))
	for index, input := range step.Inputs {
		if err := input.Validate(); err != nil {
			return fmt.Errorf("inputs[%d]: %w", index, err)
		}
		var identity string
		switch input.Kind {
		case ConstructionInputTableRevision:
			identity = "table\x00" + input.TableID + "\x00" + input.RevisionID + "\x00" + input.OutputID
		case ConstructionInputWorkspaceOutput:
			identity = "workspace\x00" + input.OutputID
		default:
			return fmt.Errorf("inputs[%d] must reference a TABLE_REVISION or WORKSPACE_OUTPUT", index)
		}
		if seenInputs[identity] {
			return fmt.Errorf("inputs[%d] duplicates an exact input reference", index)
		}
		seenInputs[identity] = true
	}
	if step.Operation.Kind != ConstructionOperationCombine || step.Operation.Combine == nil ||
		step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil ||
		step.Operation.Unpivot != nil || step.Operation.Group != nil || step.Operation.CodedGroup != nil || step.Operation.CodedPivot != nil || step.Operation.Expand != nil || step.Operation.RelatedSource != nil {
		return fmt.Errorf("operation must contain only a combine payload")
	}
	if err := validateStageColumns(step.Outputs); err != nil {
		return fmt.Errorf("outputs: %w", err)
	}
	for index, output := range step.Outputs {
		if strings.TrimSpace(output.Type) == "" || strings.EqualFold(output.Type, "INFER") || strings.EqualFold(output.Type, "object") {
			return fmt.Errorf("outputs[%d].type must be an explicit scalar type", index)
		}
	}
	if err := step.Operation.Combine.Validate(len(step.Inputs), step.Outputs); err != nil {
		return fmt.Errorf("combine: %w", err)
	}
	if step.Operation.Combine.Kind == ConstructionCombineKeyJoin && step.Operation.Combine.JoinType == ConstructionCombineLeftJoin {
		outputs, err := stageColumnIndex(step.Outputs)
		if err != nil {
			return err
		}
		for _, projection := range step.Operation.Combine.Projections {
			if projection.InputIndex == 1 && !outputs[projection.OutputColumnID].Nullable {
				return fmt.Errorf("left key join output %q must be nullable", projection.OutputColumnID)
			}
		}
	}
	return nil
}

func constructionCombineNullableScalarType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "string", "code", "uuid", "date", "date_time", "date-time", "datetime", "boolean", "integer", "decimal":
		return true
	default:
		return false
	}
}

func sourceStageColumns(columns []Column) ([]StageColumn, error) {
	stage := make([]StageColumn, len(columns))
	seenIDs := make(map[string]bool, len(columns))
	for i, column := range columns {
		if !requiredID(column.ColumnID) {
			return nil, fmt.Errorf("source column %q requires columnId in a staged construction", column.Column)
		}
		if seenIDs[column.ColumnID] {
			return nil, fmt.Errorf("source columns contain duplicate columnId %q", column.ColumnID)
		}
		seenIDs[column.ColumnID] = true
		stage[i] = StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType}
	}
	if err := validateStageColumns(stage); err != nil {
		return nil, fmt.Errorf("source projection: %w", err)
	}
	return stage, nil
}

func validateConstructionStep(step ConstructionStep, inputColumns []StageColumn) error {
	if !requiredID(step.ID) {
		return fmt.Errorf("id is required")
	}
	input, err := stageColumnIndex(inputColumns)
	if err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	if err := validateStageColumns(step.Outputs); err != nil {
		return fmt.Errorf("outputs: %w", err)
	}
	if err := validateConstructionRowValues(step, input); err != nil {
		return err
	}
	step = constructionStepWithoutRowValueOutputs(step)
	payloads := 0
	for _, present := range []bool{
		step.Operation.Pivot != nil, step.Operation.Derive != nil, step.Operation.Filter != nil,
		step.Operation.Unpivot != nil, step.Operation.Group != nil, step.Operation.CodedGroup != nil, step.Operation.CodedPivot != nil, step.Operation.Expand != nil,
		step.Operation.Combine != nil, step.Operation.RelatedSource != nil, step.Operation.RelatedExpand != nil,
		step.Operation.RelatedEligibility != nil,
		step.Operation.RelatedField != nil,
	} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("operation must contain exactly one payload matching kind")
	}
	switch step.Operation.Kind {
	case ConstructionOperationPivot:
		if step.Operation.Pivot == nil || step.Operation.Derive != nil || step.Operation.Filter != nil || step.Operation.Unpivot != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionPivot(step, input)
	case ConstructionOperationDerive:
		if step.Operation.Derive == nil || step.Operation.Pivot != nil || step.Operation.Filter != nil || step.Operation.Unpivot != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionDerive(step, input, inputColumns)
	case ConstructionOperationFilter:
		if step.Operation.Filter == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Unpivot != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionFilter(step, input, inputColumns)
	case ConstructionOperationUnpivot:
		if step.Operation.Unpivot == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil || step.Operation.Group != nil || step.Operation.Expand != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionUnpivot(step, input, inputColumns)
	case ConstructionOperationGroup:
		if step.Operation.Group == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil || step.Operation.Unpivot != nil || step.Operation.Expand != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionGroup(step, input, inputColumns)
	case ConstructionOperationCodedGroup:
		if step.Operation.CodedGroup == nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionCodedGroup(step, input, inputColumns)
	case ConstructionOperationCodedPivot:
		if step.Operation.CodedPivot == nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionCodedPivot(step, input, inputColumns)
	case ConstructionOperationExpand:
		if step.Operation.Expand == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil || step.Operation.Unpivot != nil || step.Operation.Group != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionExpand(step, input, inputColumns)
	case ConstructionOperationCombine:
		return validateConstructionCombineStep(step)
	case ConstructionOperationRelatedSource:
		if step.Operation.RelatedSource == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil ||
			step.Operation.Filter != nil || step.Operation.Unpivot != nil || step.Operation.Group != nil ||
			step.Operation.Expand != nil || step.Operation.Combine != nil {
			return fmt.Errorf("operation must contain only a relatedSource payload")
		}
		return validateConstructionRelatedSource(step, input, inputColumns)
	case ConstructionOperationRelatedExpand:
		if step.Operation.RelatedExpand == nil || step.Operation.RelatedField != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionRelatedExpand(step, input, inputColumns)
	case ConstructionOperationRelatedEligibility:
		if step.Operation.RelatedEligibility == nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionRelatedEligibility(step, input, inputColumns)
	case ConstructionOperationRelatedField:
		if step.Operation.RelatedField == nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionRelatedField(step, input, inputColumns)
	default:
		return fmt.Errorf("unsupported operation kind %q", step.Operation.Kind)
	}
}

func validateConstructionRelatedEligibility(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	related := step.Operation.RelatedEligibility
	if !requiredID(related.AnchorColumnID) {
		return fmt.Errorf("relatedEligibility.anchorColumnId must identify a retained row resource")
	}
	if !requiredID(related.ChoiceID) || !requiredID(related.TargetNodeID) || !requiredID(related.TargetResourceType) || len(related.Route) == 0 {
		return fmt.Errorf("relatedEligibility requires an exact route choice and target")
	}
	for index, hop := range related.Route {
		if !requiredID(hop.EdgeID) || !requiredID(hop.FromNodeID) || !requiredID(hop.ToNodeID) ||
			!requiredID(hop.FromResourceType) || !requiredID(hop.ToResourceType) || !requiredID(hop.Relationship) ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("relatedEligibility.route[%d] is incomplete", index)
		}
		if index > 0 && (related.Route[index-1].ToNodeID != hop.FromNodeID || related.Route[index-1].ToResourceType != hop.FromResourceType) {
			return fmt.Errorf("relatedEligibility.route is discontinuous at step %d", index)
		}
	}
	last := related.Route[len(related.Route)-1]
	if last.ToNodeID != related.TargetNodeID || last.ToResourceType != related.TargetResourceType {
		return fmt.Errorf("relatedEligibility.route must end at its exact target node and resource type")
	}
	if related.ContributorRule.Policy != ConstructionRelatedAllMatches {
		return fmt.Errorf("relatedEligibility.contributorRule policy must be ALL_MATCHES")
	}
	if predicate := related.ContributorRule.Predicate; predicate == nil {
		if related.ContributorSource != nil || related.ContributorChoiceID != "" {
			return fmt.Errorf("relatedEligibility contributor source and choice require a predicate")
		}
	} else {
		if related.ContributorSource == nil || !requiredID(related.ContributorChoiceID) {
			return fmt.Errorf("relatedEligibility predicate requires an exact contributor source and choice")
		}
		source := *related.ContributorSource
		if source.Kind != capability.ConstructionChoiceSourceField || source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
			!requiredID(source.CandidateID) || !requiredID(source.Path) || !requiredID(source.LogicalType) ||
			(source.Cardinality != "optional_one" && source.Cardinality != "required_one") || len(source.RepeatedBoundaries) != 0 {
			return fmt.Errorf("relatedEligibility contributor source must be one exact scalar field on the target resource")
		}
		if predicate.CandidateID != source.CandidateID {
			return fmt.Errorf("relatedEligibility predicate candidateId must match its contributor source")
		}
		if err := predicate.Validate(); err != nil {
			return fmt.Errorf("relatedEligibility.contributorRule.predicate: %w", err)
		}
		if predicate.Quantifier != "" {
			return fmt.Errorf("relatedEligibility scalar contributor predicate must not specify a quantifier")
		}
		if predicate.Operator == ContributorEquals {
			wantKind := ContributorString
			switch strings.ToLower(strings.TrimSpace(source.LogicalType)) {
			case "string":
			case "code":
				wantKind = ContributorValueCode
			default:
				return fmt.Errorf("relatedEligibility EQUALS predicate supports only string or code fields")
			}
			if predicate.Value.Kind != wantKind {
				return fmt.Errorf("relatedEligibility predicate value kind does not match the contributor field")
			}
		}
	}
	switch related.Match.Kind {
	case ConstructionRelatedEligibilityExists, ConstructionRelatedEligibilityAbsent:
		if related.Match.Threshold != nil {
			return fmt.Errorf("relatedEligibility threshold is only valid for COUNT_AT_LEAST")
		}
	case ConstructionRelatedEligibilityCountAtLeast:
		if related.Match.Threshold == nil || *related.Match.Threshold <= 0 {
			return fmt.Errorf("relatedEligibility COUNT_AT_LEAST requires a positive threshold")
		}
	default:
		return fmt.Errorf("relatedEligibility match kind must be EXISTS, ABSENT, or COUNT_AT_LEAST")
	}
	return validateDeclaredOutputIDs(step.Outputs, orderedStageIDs(inputColumns))
}

func validateConstructionRelatedContributorSource(source ConstructionRelatedFieldSource, operation string) (bool, error) {
	pathParts := strings.Split(source.Path, ".")
	expectedBoundaries := make([]string, 0, len(source.RepeatedBoundaries))
	path := make([]string, 0, len(pathParts))
	for _, part := range pathParts {
		if part == "" || strings.TrimSpace(part) != part || strings.ContainsAny(part, "[]") && !strings.HasSuffix(part, "[]") {
			return false, fmt.Errorf("%s contributor source path is not a canonical field path", operation)
		}
		repeated := strings.HasSuffix(part, "[]")
		if repeated {
			part = strings.TrimSuffix(part, "[]")
		}
		if part == "" || strings.ContainsAny(part, "[]") {
			return false, fmt.Errorf("%s contributor source path is not a canonical field path", operation)
		}
		if repeated {
			part += "[]"
		}
		path = append(path, part)
		if repeated {
			expectedBoundaries = append(expectedBoundaries, strings.Join(path, "."))
		}
	}
	switch source.Cardinality {
	case "optional_one", "required_one":
		if len(source.RepeatedBoundaries) != 0 || len(expectedBoundaries) != 0 {
			return false, fmt.Errorf("%s scalar contributor source must not contain repeated boundaries", operation)
		}
		return false, nil
	case "many":
		if len(expectedBoundaries) == 0 || len(source.RepeatedBoundaries) != len(expectedBoundaries) {
			return false, fmt.Errorf("%s repeated contributor source requires exact repeated boundaries", operation)
		}
		for index, boundary := range source.RepeatedBoundaries {
			if boundary.Path != expectedBoundaries[index] || boundary.MaxItems < 0 {
				return false, fmt.Errorf("%s repeated contributor source boundaries must match its exact field path", operation)
			}
		}
		return true, nil
	default:
		return false, fmt.Errorf("%s contributor source cardinality must be scalar or many", operation)
	}
}

func validateConstructionRelatedField(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	related := step.Operation.RelatedField
	source := related.Source
	if !requiredID(related.ChoiceID) || source.Kind != capability.ConstructionChoiceSourceField ||
		!requiredID(source.CandidateID) || !requiredID(source.NodeID) || !requiredID(source.ResourceType) ||
		!requiredID(source.Path) || !requiredID(source.LogicalType) ||
		(source.Cardinality != "optional_one" && source.Cardinality != "required_one") || len(source.RepeatedBoundaries) != 0 {
		return fmt.Errorf("relatedField requires one exact scalar field choice")
	}
	if !requiredID(related.OutputColumnID) || input[related.OutputColumnID].ID != "" {
		return fmt.Errorf("relatedField.outputColumnId is empty or already exists")
	}
	expected := orderedStageIDs(inputColumns)
	expected = append(expected, related.OutputColumnID)
	if err := validateDeclaredOutputIDs(step.Outputs, expected); err != nil {
		return err
	}
	for _, output := range step.Outputs {
		if output.ID != related.OutputColumnID {
			continue
		}
		if output.Type != "" && output.Type != source.LogicalType {
			return fmt.Errorf("relatedField output type must match the selected field logical type")
		}
		if !output.Nullable {
			return fmt.Errorf("relatedField output must be nullable because the active terminal record may be absent")
		}
	}
	return nil
}

func validateConstructionRelatedExpand(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	related := step.Operation.RelatedExpand
	if !requiredID(related.AnchorColumnID) {
		return fmt.Errorf("relatedExpand.anchorColumnId must identify a retained row resource")
	}
	if !requiredID(related.ChoiceID) || !requiredID(related.TargetNodeID) || !requiredID(related.TargetResourceType) ||
		!requiredID(related.RelatedRecordColumnID) || len(related.Route) == 0 {
		return fmt.Errorf("relatedExpand requires a route choice, exact target, non-empty route, and related record output")
	}
	for index, hop := range related.Route {
		if !requiredID(hop.EdgeID) || !requiredID(hop.FromNodeID) || !requiredID(hop.ToNodeID) ||
			!requiredID(hop.FromResourceType) || !requiredID(hop.ToResourceType) || !requiredID(hop.Relationship) ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("relatedExpand.route[%d] is incomplete", index)
		}
		if index > 0 && (related.Route[index-1].ToNodeID != hop.FromNodeID || related.Route[index-1].ToResourceType != hop.FromResourceType) {
			return fmt.Errorf("relatedExpand.route is discontinuous at step %d", index)
		}
	}
	last := related.Route[len(related.Route)-1]
	if last.ToNodeID != related.TargetNodeID || last.ToResourceType != related.TargetResourceType {
		return fmt.Errorf("relatedExpand.route must end at its exact target node and resource type")
	}
	if related.ContributorRule.Policy != ConstructionRelatedAllMatches {
		return fmt.Errorf("relatedExpand.contributorRule policy must be ALL_MATCHES")
	}
	if predicate := related.ContributorRule.Predicate; predicate == nil {
		if related.ContributorSource != nil || related.ContributorChoiceID != "" {
			return fmt.Errorf("relatedExpand contributor source and choice require a predicate")
		}
	} else {
		if related.ContributorSource == nil || !requiredID(related.ContributorChoiceID) {
			return fmt.Errorf("relatedExpand predicate requires an exact contributor source and choice")
		}
		source := *related.ContributorSource
		if source.Kind != capability.ConstructionChoiceSourceField || source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
			!requiredID(source.CandidateID) || !requiredID(source.Path) || !requiredID(source.LogicalType) {
			return fmt.Errorf("relatedExpand contributor source must identify one exact field on the target resource")
		}
		repeated, err := validateConstructionRelatedContributorSource(source, "relatedExpand")
		if err != nil {
			return err
		}
		if predicate.CandidateID != source.CandidateID {
			return fmt.Errorf("relatedExpand predicate candidateId must match its contributor source")
		}
		if err := predicate.Validate(); err != nil {
			return fmt.Errorf("relatedExpand.contributorRule.predicate: %w", err)
		}
		if repeated && predicate.Quantifier != ContributorAny {
			return fmt.Errorf("relatedExpand repeated contributor predicate requires the ANY quantifier")
		}
		if !repeated && predicate.Quantifier != "" {
			return fmt.Errorf("relatedExpand scalar contributor predicate must not specify a quantifier")
		}
		if predicate.Operator == ContributorEquals {
			wantKind := ContributorString
			switch strings.ToLower(strings.TrimSpace(source.LogicalType)) {
			case "string":
			case "code":
				wantKind = ContributorValueCode
			default:
				return fmt.Errorf("relatedExpand EQUALS predicate supports only string or code fields")
			}
			if predicate.Value.Kind != wantKind {
				return fmt.Errorf("relatedExpand predicate value kind does not match the contributor field")
			}
		}
	}
	switch related.EmptyPolicy {
	case ConstructionExpandEmptyError, ConstructionExpandEmptyExclude, ConstructionExpandEmptyPreserveParent:
	default:
		return fmt.Errorf("relatedExpand emptyPolicy must be ERROR, EXCLUDE, or PRESERVE_PARENT")
	}
	if input[related.RelatedRecordColumnID].ID != "" {
		return fmt.Errorf("relatedExpand relatedRecordColumnId already exists in the input schema")
	}
	expected := orderedStageIDs(inputColumns)
	expected = append(expected, related.RelatedRecordColumnID)
	if err := validateDeclaredOutputIDs(step.Outputs, expected); err != nil {
		return err
	}
	var output StageColumn
	for _, column := range step.Outputs {
		if column.ID == related.RelatedRecordColumnID {
			output = column
			break
		}
	}
	wantNullable := related.EmptyPolicy == ConstructionExpandEmptyPreserveParent
	if output.Nullable != wantNullable {
		return fmt.Errorf("relatedExpand output column %q nullable must be %t for emptyPolicy %s", output.ID, wantNullable, related.EmptyPolicy)
	}
	return nil
}

func validateConstructionRelatedSource(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	related := step.Operation.RelatedSource
	if related.AnchorColumnID != "_key" && related.AnchorColumnID != "__loom_row_id" {
		return fmt.Errorf("relatedSource.anchorColumnId must identify the hidden root row identity")
	}
	if !requiredID(related.ChoiceID) || !requiredID(related.SourceOccurrenceID) {
		return fmt.Errorf("relatedSource.choiceId and sourceOccurrenceId are required")
	}
	source := related.Source
	if source.Kind != capability.ConstructionChoiceSourceField || !requiredID(source.CandidateID) || !requiredID(source.NodeID) ||
		!requiredID(source.ResourceType) || !requiredID(source.Path) || !requiredID(source.LogicalType) {
		return fmt.Errorf("relatedSource.source requires one exact field candidate and field path")
	}
	if related.SourceOccurrenceID != source.NodeID {
		return fmt.Errorf("relatedSource.sourceOccurrenceId must identify the terminal source node")
	}
	if source.Cardinality != "optional_one" && source.Cardinality != "required_one" {
		return fmt.Errorf("relatedSource.source cardinality must be scalar for ALL_MATCHES")
	}
	switch related.Form {
	case capability.ConstructionChoiceAll, capability.ConstructionChoiceCount, capability.ConstructionChoicePresence:
	default:
		return fmt.Errorf("relatedSource.form must be ALL, COUNT, or PRESENCE")
	}
	if related.RowValuePolicy != "" && related.RowValuePolicy != ConstructionRowValueAll && related.RowValuePolicy != ConstructionRowValueOne {
		return fmt.Errorf("relatedSource.rowValuePolicy must be ALL or ONE")
	}
	if related.RowValuePolicy == ConstructionRowValueOne && related.Form != capability.ConstructionChoiceAll {
		return fmt.Errorf("relatedSource.rowValuePolicy ONE is supported only when form is ALL")
	}
	if related.ContributorRule.Policy != ConstructionRelatedAllMatches {
		return fmt.Errorf("relatedSource.contributorRule policy must be ALL_MATCHES")
	}
	if predicate := related.ContributorRule.Predicate; predicate != nil {
		if err := predicate.Validate(); err != nil {
			return fmt.Errorf("relatedSource.contributorRule.predicate: %w", err)
		}
		if predicate.CandidateID != source.CandidateID {
			return fmt.Errorf("relatedSource.contributorRule.predicate candidateId must match relatedSource.source.candidateId")
		}
		if predicate.Quantifier != "" {
			return fmt.Errorf("relatedSource scalar contributor predicate must not specify a quantifier")
		}
		if predicate.Operator == ContributorEquals {
			wantKind := ContributorString
			switch strings.ToLower(strings.TrimSpace(source.LogicalType)) {
			case "string":
			case "code":
				wantKind = ContributorValueCode
			default:
				return fmt.Errorf("relatedSource EQUALS predicate is supported only for string or code source fields")
			}
			if predicate.Value.Kind != wantKind {
				return fmt.Errorf("relatedSource EQUALS predicate requires a %s value for the selected source field", wantKind)
			}
		}
	}
	if len(related.Route) == 0 {
		return fmt.Errorf("relatedSource.route must contain at least one authorized hop")
	}
	priorNode, priorResource := related.Route[0].FromNodeID, related.Route[0].FromResourceType
	for index, hop := range related.Route {
		if !requiredID(hop.EdgeID) || !requiredID(hop.Relationship) || !requiredID(hop.ToNodeID) ||
			!requiredID(hop.ToResourceType) || hop.FromNodeID != priorNode || hop.FromResourceType != priorResource ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("relatedSource.route[%d] does not extend the exact prior route", index)
		}
		priorNode, priorResource = hop.ToNodeID, hop.ToResourceType
	}
	if priorNode != source.NodeID || priorResource != source.ResourceType {
		return fmt.Errorf("relatedSource route terminal does not match its source candidate")
	}
	if !requiredID(related.OutputColumnID) {
		return fmt.Errorf("relatedSource.outputColumnId is required")
	}
	if _, exists := input[related.OutputColumnID]; exists {
		return fmt.Errorf("relatedSource.outputColumnId %q already exists", related.OutputColumnID)
	}
	want := orderedStageIDs(inputColumns)
	want = append(want, related.OutputColumnID)
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateStageColumns(columns []StageColumn) error {
	ids := make(map[string]bool, len(columns))
	names := make(map[string]bool, len(columns))
	for i, column := range columns {
		if !requiredID(column.ID) {
			return fmt.Errorf("outputs[%d].id is required", i)
		}
		if ids[column.ID] {
			return fmt.Errorf("outputs contain duplicate id %q", column.ID)
		}
		ids[column.ID] = true
		if !physicalColumnPattern.MatchString(column.Name) {
			return fmt.Errorf("output %q has invalid physical name %q", column.ID, column.Name)
		}
		if names[column.Name] {
			return fmt.Errorf("outputs contain duplicate name %q", column.Name)
		}
		names[column.Name] = true
		if strings.TrimSpace(column.Label) == "" {
			return fmt.Errorf("output %q label is required", column.ID)
		}
		if column.Type != "" && column.Type != "INFER" && column.Type != strings.TrimSpace(column.Type) {
			return fmt.Errorf("output %q type must not contain surrounding whitespace", column.ID)
		}
	}
	return nil
}

func stageColumnIndex(columns []StageColumn) (map[string]StageColumn, error) {
	index := make(map[string]StageColumn, len(columns))
	for _, column := range columns {
		if !requiredID(column.ID) {
			return nil, fmt.Errorf("column id is required")
		}
		if _, exists := index[column.ID]; exists {
			return nil, fmt.Errorf("duplicate column id %q", column.ID)
		}
		index[column.ID] = column
	}
	return index, nil
}

func constructionPivotScalarKind(typeName string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "string", "code", "uuid", "date", "date_time", "date-time", "datetime":
		return "STRING", true
	case "integer":
		return "INTEGER", true
	case "decimal":
		return "DECIMAL", true
	case "boolean":
		return "BOOLEAN", true
	default:
		return "", false
	}
}

func constructionPivotTypeIsScalar(typeName string) (scalar, known bool) {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	if normalized == "" || normalized == "infer" {
		return true, false
	}
	if _, ok := constructionPivotScalarKind(normalized); ok {
		return true, true
	}
	switch normalized {
	case "array", "object", "record", "json", "list", "map":
		return false, true
	default:
		return true, false
	}
}

func validateConstructionPivot(step ConstructionStep, input map[string]StageColumn) error {
	pivot := step.Operation.Pivot
	if !sameOperationID(step.ID, pivot.ConstructionID) {
		return fmt.Errorf("pivot constructionId must equal step id")
	}
	if len(pivot.GroupKeyIDs) == 0 {
		return fmt.Errorf("pivot groupKeyIds must be non-empty")
	}
	groupIDs := make(map[string]bool, len(pivot.GroupKeyIDs))
	for _, id := range pivot.GroupKeyIDs {
		if !requiredID(id) {
			return fmt.Errorf("pivot groupKeyIds contain an empty id")
		}
		if groupIDs[id] {
			return fmt.Errorf("pivot groupKeyIds contain duplicate id %q", id)
		}
		column, exists := input[id]
		if !exists {
			return missingConstructionColumn("groupKeyIds", id)
		}
		if scalar, known := constructionPivotTypeIsScalar(column.Type); known && !scalar {
			return fmt.Errorf("pivot group key %q type %q is not a scalar", id, column.Type)
		}
		groupIDs[id] = true
	}
	for _, reference := range []struct{ field, id string }{
		{field: "categoryColumnId", id: pivot.CategoryColumnID},
		{field: "valueColumnId", id: pivot.ValueColumnID},
	} {
		field, id := reference.field, reference.id
		if !requiredID(id) {
			return fmt.Errorf("pivot %s is required", field)
		}
		column, exists := input[id]
		if !exists {
			return missingConstructionColumn(field, id)
		}
		if groupIDs[id] {
			return fmt.Errorf("pivot %s cannot also be a group key", field)
		}
		if scalar, known := constructionPivotTypeIsScalar(column.Type); known && !scalar {
			return fmt.Errorf("pivot %s column %q type %q is not a scalar", field, id, column.Type)
		}
	}
	if pivot.CategoryColumnID == pivot.ValueColumnID {
		return fmt.Errorf("pivot category and value columns must differ")
	}
	if pivot.Discovery != nil && len(pivot.Categories) != 0 {
		return fmt.Errorf("pivot cannot combine explicit categories with discovery")
	}
	if pivot.Discovery == nil && len(pivot.Categories) == 0 {
		return fmt.Errorf("pivot categories must be non-empty")
	}
	if !oneOf(string(pivot.DuplicatePolicy), "ERROR", "SUM", "MIN", "MAX") {
		return fmt.Errorf("pivot duplicatePolicy must be ERROR, SUM, MIN, or MAX")
	}
	if categoryKind, known := constructionPivotScalarKind(input[pivot.CategoryColumnID].Type); known {
		for index, category := range pivot.Categories {
			if category.Key.Kind == TableScalarNull || category.Key.Kind == TableScalarMissing {
				continue
			}
			if string(category.Key.Kind) != categoryKind {
				return fmt.Errorf("pivot categories[%d].key type %q does not match category column type %q", index, category.Key.Kind, input[pivot.CategoryColumnID].Type)
			}
		}
	}
	if pivot.DuplicatePolicy == ConstructionPivotDuplicateSum {
		valueColumn := input[pivot.ValueColumnID]
		if valueKind, known := constructionPivotScalarKind(valueColumn.Type); known && valueKind != "INTEGER" && valueKind != "DECIMAL" {
			return fmt.Errorf("pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got %q", valueColumn.Type)
		}
	}
	if !oneOf(string(pivot.MissingCellPolicy), "NULL", "ERROR") {
		return fmt.Errorf("pivot missingCellPolicy must be NULL or ERROR")
	}
	if !oneOf(string(pivot.UnlistedCategoryPolicy), "ERROR") {
		return fmt.Errorf("pivot unlistedCategoryPolicy must be ERROR; EXCLUDE_WITH_EVIDENCE is not available inside a construction sequence")
	}
	if pivot.Discovery != nil {
		if pivot.Discovery.MaxCategories <= 0 || pivot.Discovery.MaxCategories > maxPivotCategories {
			return fmt.Errorf("pivot discovery maxCategories must be 1..%d", maxPivotCategories)
		}
		if _, known := constructionPivotScalarKind(input[pivot.CategoryColumnID].Type); !known {
			return fmt.Errorf("pivot discovery category column %q type %q is not a supported scalar pair", pivot.CategoryColumnID, input[pivot.CategoryColumnID].Type)
		}
		if _, known := constructionPivotScalarKind(input[pivot.ValueColumnID].Type); !known {
			return fmt.Errorf("pivot discovery value column %q type %q is not a supported scalar pair", pivot.ValueColumnID, input[pivot.ValueColumnID].Type)
		}
		return validateDeclaredOutputIDs(step.Outputs, pivot.GroupKeyIDs)
	}
	seenKeys, outputIDs := map[string]bool{}, map[string]bool{}
	for i, category := range pivot.Categories {
		if err := category.Key.ValidatePivotCategoryKey(); err != nil {
			return fmt.Errorf("categories[%d].key: %w", i, err)
		}
		key := category.Key.identity()
		if seenKeys[key] {
			return fmt.Errorf("pivot categories contain duplicate key at index %d", i)
		}
		seenKeys[key] = true
		id := category.OutputColumnID
		if !requiredID(id) {
			return fmt.Errorf("categories[%d].outputColumnId is required", i)
		}
		if _, exists := input[id]; exists || groupIDs[id] || outputIDs[id] {
			return fmt.Errorf("pivot output column id %q is not unique", id)
		}
		outputIDs[id] = true
	}
	want := make([]string, 0, len(groupIDs)+len(outputIDs))
	want = append(want, pivot.GroupKeyIDs...)
	for _, category := range pivot.Categories {
		want = append(want, category.OutputColumnID)
	}
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionCodedPivot(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	pivot := step.Operation.CodedPivot
	if pivot == nil || !sameOperationID(step.ID, pivot.ConstructionID) {
		return fmt.Errorf("coded pivot constructionId must equal step id")
	}
	proposal := pivot.SourceChoiceID != ""
	durable := pivot.SourceChoiceID == "" && pivot.Source != nil
	if !proposal && !durable {
		return fmt.Errorf("coded pivot requires either proposal sourceChoiceId or durable source facts")
	}
	if proposal && !requiredID(pivot.SourceChoiceID) {
		return fmt.Errorf("coded pivot sourceChoiceId must be an exact non-empty token")
	}
	if pivot.Source != nil {
		source := pivot.Source
		family := source.Family
		for name, value := range map[string]string{
			"candidateId": source.CandidateID, "nodeId": source.NodeID, "fieldPath": source.FieldPath,
			"bindingId": family.BindingID, "resourceType": family.ResourceType, "sourcePath": family.SourcePath,
			"keyPath": family.KeyPath, "valuePath": family.ValuePath, "logicalType": family.LogicalType,
			"ruleVersion": family.RuleVersion,
		} {
			if !requiredID(value) {
				return fmt.Errorf("coded pivot source %s is required", name)
			}
		}
		if family.SchemaVersion <= 0 {
			return fmt.Errorf("coded pivot source schemaVersion must be positive")
		}
		if len(source.Route) != 0 {
			return fmt.Errorf("coded pivot currently supports only direct root sources")
		}
	}
	if len(pivot.Categories) == 0 || len(pivot.Categories) > maxCodedPivotCategories {
		return fmt.Errorf("coded pivot requires 1..%d categories", maxCodedPivotCategories)
	}
	if !oneOf(string(pivot.DuplicatePolicy), "ERROR", "SUM", "MIN", "MAX") || !oneOf(string(pivot.MissingCellPolicy), "NULL", "ERROR") {
		return fmt.Errorf("coded pivot duplicatePolicy or missingCellPolicy is unsupported")
	}
	inputIDs := make(map[string]bool, len(inputColumns))
	for _, column := range inputColumns {
		inputIDs[column.ID] = true
	}
	declarations := make(map[string]StageColumn, len(step.Outputs))
	for _, output := range step.Outputs {
		declarations[output.ID] = output
	}
	sourceLogicalType := ""
	if pivot.Source != nil {
		canonical, ok := recipe.CanonicalConstructionScalarLogicalType(pivot.Source.Family.LogicalType)
		if !ok {
			return fmt.Errorf("coded pivot source logical type %q is not a supported table scalar", pivot.Source.Family.LogicalType)
		}
		sourceLogicalType = canonical
	}
	if pivot.DuplicatePolicy == ConstructionPivotDuplicateSum && sourceLogicalType != "" &&
		sourceLogicalType != "integer" && sourceLogicalType != "decimal" {
		return fmt.Errorf("coded pivot duplicatePolicy SUM requires an INTEGER or DECIMAL source logical type, got %q", pivot.Source.Family.LogicalType)
	}
	seenCodes, outputIDs := map[string]bool{}, map[string]bool{}
	want := make([]string, 0, len(pivot.Categories))
	for index, category := range pivot.Categories {
		if !requiredID(category.OutputColumnID) || inputIDs[category.OutputColumnID] || outputIDs[category.OutputColumnID] {
			return fmt.Errorf("coded pivot categories[%d].outputColumnId is empty, duplicated, or collides with input", index)
		}
		proposalCategory := proposal && requiredID(category.ChoiceID) && category.System == "" && category.Code == ""
		durableCategory := pivot.Source != nil && category.ChoiceID == "" && requiredID(category.System) && requiredID(category.Code)
		if !proposalCategory && !durableCategory {
			return fmt.Errorf("coded pivot categories[%d] must contain either choiceId or durable system/code", index)
		}
		if category.System != "" || category.Code != "" {
			key := category.System + "\x00" + category.Code
			if seenCodes[key] {
				return fmt.Errorf("coded pivot categories duplicate system/code at index %d", index)
			}
			seenCodes[key] = true
		}
		if declaration, ok := declarations[category.OutputColumnID]; ok && sourceLogicalType != "" &&
			declaration.Type != "" && !strings.EqualFold(declaration.Type, "INFER") {
			declaredType, ok := recipe.CanonicalConstructionScalarLogicalType(declaration.Type)
			if !ok || declaredType != sourceLogicalType {
				return fmt.Errorf("CODED_PIVOT output %q type %q does not match source type %q", declaration.ID, declaration.Type, pivot.Source.Family.LogicalType)
			}
		}
		outputIDs[category.OutputColumnID] = true
		want = append(want, category.OutputColumnID)
	}
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionDerive(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	derive := step.Operation.Derive
	if !sameOperationID(step.ID, derive.ConstructionID) {
		return fmt.Errorf("derive constructionId must equal step id")
	}
	if !requiredID(derive.OutputColumnID) {
		return fmt.Errorf("derive outputColumnId is required")
	}
	if _, exists := input[derive.OutputColumnID]; exists {
		return fmt.Errorf("derive outputColumnId %q already exists", derive.OutputColumnID)
	}
	if !oneOf(string(derive.Operation), "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE") {
		return fmt.Errorf("unsupported derived operation %q", derive.Operation)
	}
	if !oneOf(string(derive.MissingInputPolicy), "PROPAGATE_NULL", "ERROR") {
		return fmt.Errorf("missingInputPolicy must be PROPAGATE_NULL or ERROR")
	}
	if derive.Operation == ConstructionDerivedDivide && !oneOf(string(derive.DivisionByZeroPolicy), "NULL", "ERROR") {
		return fmt.Errorf("DIVIDE requires divisionByZeroPolicy")
	}
	if derive.Operation != ConstructionDerivedDivide && derive.DivisionByZeroPolicy != "" {
		return fmt.Errorf("divisionByZeroPolicy is only valid for DIVIDE")
	}
	if err := validateConstructionOperand(derive.Left, input); err != nil {
		return fmt.Errorf("left: %w", err)
	}
	if err := validateConstructionOperand(derive.Right, input); err != nil {
		return fmt.Errorf("right: %w", err)
	}
	want := orderedStageIDs(inputColumns)
	want = append(want, derive.OutputColumnID)
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionOperand(operand ConstructionOperand, input map[string]StageColumn) error {
	switch operand.Kind {
	case ConstructionColumnOperand:
		if !requiredID(operand.ColumnID) || operand.Literal != nil {
			return fmt.Errorf("COLUMN operand must contain only columnId")
		}
		if _, exists := input[operand.ColumnID]; !exists {
			return missingConstructionColumn("columnId", operand.ColumnID)
		}
	case ConstructionLiteralOperand:
		if operand.ColumnID != "" || operand.Literal == nil {
			return fmt.Errorf("LITERAL operand must contain only literal")
		}
		if err := validateConstructionLiteral(*operand.Literal); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operand kind must be COLUMN or LITERAL")
	}
	return nil
}

func validateConstructionLiteral(literal ConstructionLiteral) error {
	payloads := 0
	if literal.Integer != nil {
		payloads++
	}
	if literal.Decimal != nil {
		payloads++
	}
	switch literal.Kind {
	case ConstructionNumericInteger:
		if payloads != 1 || literal.Integer == nil || literal.Decimal != nil {
			return fmt.Errorf("INTEGER literal must contain exactly one integer")
		}
	case ConstructionNumericDecimal:
		if payloads != 1 || literal.Decimal == nil || literal.Integer != nil {
			return fmt.Errorf("DECIMAL literal must contain exactly one decimal")
		}
		if math.IsNaN(*literal.Decimal) || math.IsInf(*literal.Decimal, 0) {
			return fmt.Errorf("DECIMAL literal must be finite")
		}
	default:
		return fmt.Errorf("unsupported literal kind %q", literal.Kind)
	}
	return nil
}

func validateConstructionFilter(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	filter := step.Operation.Filter
	if !requiredID(filter.ColumnID) {
		return fmt.Errorf("filter columnId is required")
	}
	if _, exists := input[filter.ColumnID]; !exists {
		return missingConstructionColumn("columnId", filter.ColumnID)
	}
	if !oneOf(string(filter.Operator), "EQUALS", "NOT_EQUALS", "IN", "EXISTS", "MISSING", "CONTAINS_TEXT", "GT", "GTE", "LT", "LTE") {
		return fmt.Errorf("unsupported filter operator %q", filter.Operator)
	}
	switch filter.Operator {
	case ConstructionFilterExists, ConstructionFilterMissing:
		if len(filter.Values) != 0 {
			return fmt.Errorf("%s does not accept values", filter.Operator)
		}
	case ConstructionFilterIn:
		if len(filter.Values) == 0 {
			return fmt.Errorf("IN requires at least one value")
		}
	default:
		if len(filter.Values) != 1 {
			return fmt.Errorf("%s requires exactly one value", filter.Operator)
		}
	}
	for i, value := range filter.Values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("values[%d]: %w", i, err)
		}
	}
	return validateDeclaredOutputIDs(step.Outputs, orderedStageIDs(inputColumns))
}

func constructionUnpivotValueKind(logicalType string) (string, bool) {
	canonical, ok := recipe.CanonicalConstructionScalarLogicalType(logicalType)
	if !ok {
		return "", false
	}
	switch canonical {
	case "string", "date", "date_time", "code", "uuid":
		return "STRING", true
	case "integer":
		return "INTEGER", true
	case "decimal":
		return "DECIMAL", true
	case "boolean":
		return "BOOLEAN", true
	default:
		return "", false
	}
}

func validateConstructionUnpivot(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	unpivot := step.Operation.Unpivot
	if !sameOperationID(step.ID, unpivot.ConstructionID) {
		return fmt.Errorf("unpivot constructionId must equal step id")
	}
	if len(unpivot.Inputs) == 0 {
		return fmt.Errorf("unpivot inputs must be non-empty")
	}
	if !oneOf(string(unpivot.NullRowPolicy), "DROP", "PRESERVE") {
		return fmt.Errorf("nullRowPolicy must be DROP or PRESERVE")
	}
	inputIDs := make(map[string]bool, len(unpivot.Inputs))
	keys := make(map[string]bool, len(unpivot.Inputs))
	keyKind, valueKind := "", ""
	for i, item := range unpivot.Inputs {
		if !requiredID(item.ColumnID) {
			return fmt.Errorf("inputs[%d].columnId is required", i)
		}
		column, exists := input[item.ColumnID]
		if !exists {
			return missingConstructionColumn(fmt.Sprintf("inputs[%d].columnId", i), item.ColumnID)
		}
		if inputIDs[item.ColumnID] {
			return fmt.Errorf("unpivot inputs contain duplicate column id %q", item.ColumnID)
		}
		inputIDs[item.ColumnID] = true
		if err := item.Key.ValidateConcreteValue(); err != nil {
			return fmt.Errorf("inputs[%d].key: %w", i, err)
		}
		currentKeyKind := string(item.Key.Kind)
		if keyKind == "" {
			keyKind = currentKeyKind
		} else if currentKeyKind != keyKind {
			return fmt.Errorf("unpivot input keys must have one canonical scalar type")
		}
		key := item.Key.identity()
		if keys[key] {
			return fmt.Errorf("unpivot inputs contain duplicate key at index %d", i)
		}
		keys[key] = true
		if kind, ok := constructionUnpivotValueKind(column.Type); ok {
			if valueKind == "" {
				valueKind = kind
			} else if valueKind != kind {
				if valueKind == "INTEGER" && kind == "DECIMAL" {
					valueKind = "DECIMAL"
				} else if valueKind != "DECIMAL" || kind != "INTEGER" {
					return fmt.Errorf("unpivot input %q type %q is incompatible with previous inputs", item.ColumnID, column.Type)
				}
			}
		}
	}
	if !requiredID(unpivot.KeyOutputColumnID) || !requiredID(unpivot.ValueOutputColumnID) || unpivot.KeyOutputColumnID == unpivot.ValueOutputColumnID {
		return fmt.Errorf("unpivot requires distinct key and value output column ids")
	}
	if _, exists := input[unpivot.KeyOutputColumnID]; exists {
		return fmt.Errorf("unpivot key output column id already exists")
	}
	if _, exists := input[unpivot.ValueOutputColumnID]; exists {
		return fmt.Errorf("unpivot value output column id already exists")
	}
	want := make([]string, 0, len(input)-len(inputIDs)+2)
	for _, column := range inputColumns {
		if !inputIDs[column.ID] {
			want = append(want, column.ID)
		}
	}
	want = append(want, unpivot.KeyOutputColumnID, unpivot.ValueOutputColumnID)
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionGroup(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	group := step.Operation.Group
	if !sameOperationID(step.ID, group.ConstructionID) {
		return fmt.Errorf("group constructionId must equal step id")
	}
	if !group.MissingKeyPolicy.Valid() {
		return fmt.Errorf("group missingKeyPolicy is unsupported")
	}
	if len(group.Keys) == 0 && len(group.Aggregates) == 0 {
		return fmt.Errorf("group requires at least one key or summary")
	}
	want := make([]string, 0, len(group.Keys)+len(group.Aggregates))
	inputIDs := make(map[string]bool, len(group.Keys))
	outputIDs := make(map[string]bool, len(group.Keys)+len(group.Aggregates))
	codedKeys := 0
	for index, key := range group.Keys {
		if key.FieldSource != nil {
			if key.InputColumnID != "" || key.SemanticCategory != nil {
				return fmt.Errorf("keys[%d] must choose exactly one Group key source", index)
			}
			if err := validateConstructionGroupFieldSource(*key.FieldSource); err != nil {
				return fmt.Errorf("keys[%d].fieldSource: %w", index, err)
			}
			if key.FieldSource.ValueKind == "CODING_IDENTITY" {
				codedKeys++
			}
		} else if key.SemanticCategory == nil {
			if !requiredID(key.InputColumnID) {
				return fmt.Errorf("keys[%d].inputColumnId is required", index)
			}
			if _, exists := input[key.InputColumnID]; !exists {
				return missingConstructionColumn(fmt.Sprintf("keys[%d].inputColumnId", index), key.InputColumnID)
			}
			if inputIDs[key.InputColumnID] {
				return fmt.Errorf("group keys contain duplicate input column id %q", key.InputColumnID)
			}
			inputIDs[key.InputColumnID] = true
		} else {
			codedKeys++
			if key.InputColumnID != "" || key.FieldSource != nil {
				return fmt.Errorf("keys[%d] must choose exactly one Group key source", index)
			}
			if err := validateConstructionGroupSemanticCategory(*key.SemanticCategory); err != nil {
				return fmt.Errorf("keys[%d].semanticCategory: %w", index, err)
			}
			if len(step.Inputs) != 1 || step.Inputs[0].Kind != ConstructionInputSourceProjection || step.Inputs[0].StepID != "" {
				return fmt.Errorf("semantic category grouping currently requires the direct source projection stage")
			}
			if key.OutputColumnID != "" {
				for _, declaration := range step.Outputs {
					if declaration.ID == key.OutputColumnID && declaration.Type != "" && declaration.Type != "INFER" && declaration.Type != "string" {
						return fmt.Errorf("keys[%d].semanticCategory output must be a string column", index)
					}
				}
			}
		}
		if !requiredID(key.OutputColumnID) || outputIDs[key.OutputColumnID] {
			return fmt.Errorf("keys[%d].outputColumnId is empty or duplicated", index)
		}
		outputIDs[key.OutputColumnID] = true
		want = append(want, key.OutputColumnID)
	}
	for index, aggregate := range group.Aggregates {
		if !requiredID(aggregate.OutputColumnID) || outputIDs[aggregate.OutputColumnID] {
			return fmt.Errorf("aggregates[%d].outputColumnId is empty or duplicated", index)
		}
		switch aggregate.Operation {
		case ConstructionGroupCountRows:
			if aggregate.InputColumnID != "" {
				return fmt.Errorf("aggregates[%d] COUNT_ROWS does not accept inputColumnId", index)
			}
		case ConstructionGroupCountNonNull, ConstructionGroupCountDistinct, ConstructionGroupSum, ConstructionGroupMean:
			if !requiredID(aggregate.InputColumnID) {
				return fmt.Errorf("aggregates[%d] %s requires inputColumnId", index, aggregate.Operation)
			}
			if _, exists := input[aggregate.InputColumnID]; !exists {
				return missingConstructionColumn(fmt.Sprintf("aggregates[%d].inputColumnId", index), aggregate.InputColumnID)
			}
		default:
			return fmt.Errorf("aggregates[%d] has unsupported operation %q", index, aggregate.Operation)
		}
		outputIDs[aggregate.OutputColumnID] = true
		want = append(want, aggregate.OutputColumnID)
	}
	if codedKeys > 1 {
		return fmt.Errorf("group supports at most one coded identity key")
	}
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionGroupFieldSource(source ConstructionGroupFieldSource) error {
	switch source.Kind {
	case "CHOICE":
		if !requiredID(source.ChoiceID) || source.ValueKind != "" || source.SourceStageID != "" || source.SourceShapeID != "" || source.Input != nil ||
			source.SourceRole != "" || source.OccurrenceID != "" || source.ResourceType != "" || source.OwnerOccurrenceID != "" ||
			source.OwnerResourceType != "" || source.CandidateID != "" || source.CanonicalPath != "" || source.BoundarySelector != "" {
			return fmt.Errorf("CHOICE requires only an exact server-issued choiceId")
		}
	case "BINDING":
		if source.ChoiceID != "" {
			return fmt.Errorf("BINDING cannot carry choiceId")
		}
		return source.Validate()
	default:
		return fmt.Errorf("kind must be CHOICE or BINDING")
	}
	return nil
}

func validateConstructionGroupSemanticCategory(category ConstructionGroupSemanticCategory) error {
	switch category.Kind {
	case "CHOICE":
		if !requiredID(category.ChoiceID) || category.BindingID != "" || category.RuleVersion != "" || category.FamilyID != "" || category.SourceKind != "" ||
			category.System != "" || category.Version != "" || category.ResourceType != "" || category.CodingPath != "" {
			return fmt.Errorf("CHOICE requires only an exact choiceId")
		}
	case "BINDING":
		if category.ChoiceID != "" || !requiredID(category.BindingID) || !requiredID(category.RuleVersion) || !requiredID(category.FamilyID) ||
			(category.SourceKind != "file" && category.SourceKind != "retained_vertex") ||
			!requiredID(category.System) || !requiredID(category.ResourceType) || !requiredID(category.CodingPath) ||
			category.Version != strings.TrimSpace(category.Version) || category.System != strings.TrimSpace(category.System) ||
			category.CodingPath != strings.TrimSpace(category.CodingPath) {
			return fmt.Errorf("BINDING requires exact family, coding path, system, version, and rule identities")
		}
	default:
		return fmt.Errorf("kind must be CHOICE or BINDING")
	}
	return nil
}

func validateConstructionCodedGroup(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	group := step.Operation.CodedGroup
	if !sameOperationID(step.ID, group.ConstructionID) {
		return fmt.Errorf("codedGroup constructionId must equal step id")
	}
	if group.ChoiceID != "" && !requiredID(group.ChoiceID) {
		return fmt.Errorf("codedGroup choiceId must be an exact server-issued choice")
	}
	if group.Source.OccurrenceID != "base" || !requiredID(group.Source.ResourceType) || !requiredID(group.Source.CodingPath) {
		return fmt.Errorf("codedGroup source must identify an exact root occurrence and Coding path")
	}
	if group.Source.FHIRType != "Coding" || group.Source.Cardinality != "MANY" || group.Source.Shape != "ARRAY" || len(group.Source.Route) != 0 {
		return fmt.Errorf("codedGroup source must be a root-anchored repeated Coding path with an empty route")
	}
	if !group.MissingKeyPolicy.Valid() {
		return fmt.Errorf("codedGroup missingKeyPolicy must be GROUP, EXCLUDE, or ERROR")
	}
	ids := []string{
		group.SystemOutputColumnID,
		group.VersionOutputColumnID,
		group.CodeOutputColumnID,
		group.DistinctSourceCountOutputColumnID,
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !requiredID(id) || seen[id] {
			return fmt.Errorf("codedGroup requires four distinct output column IDs")
		}
		seen[id] = true
		if _, exists := input[id]; exists {
			return fmt.Errorf("codedGroup output column id %q collides with the input schema", id)
		}
	}
	if len(step.Outputs) != 4 {
		return fmt.Errorf("codedGroup outputs must declare System, Version, Code, and distinct Source records")
	}
	expected := make(map[string]StageColumn, 4)
	for _, output := range step.Outputs {
		expected[output.ID] = output
	}
	for _, id := range ids[:3] {
		output, exists := expected[id]
		if !exists || !strings.EqualFold(output.Type, "string") || !output.Nullable {
			return fmt.Errorf("codedGroup key output %q must be a nullable string", id)
		}
	}
	count, exists := expected[group.DistinctSourceCountOutputColumnID]
	if !exists || !strings.EqualFold(count.Type, "integer") || count.Nullable {
		return fmt.Errorf("codedGroup distinct source count output must be a required integer")
	}
	return validateDeclaredOutputIDs(step.Outputs, ids)
}

func validateConstructionExpand(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	expand := step.Operation.Expand
	if !sameOperationID(step.ID, expand.ConstructionID) {
		return fmt.Errorf("expand constructionId must equal step id")
	}
	if !requiredID(expand.InputColumnID) {
		return fmt.Errorf("inputColumnId is required")
	}
	if _, exists := input[expand.InputColumnID]; !exists {
		return missingConstructionColumn("inputColumnId", expand.InputColumnID)
	}
	if !requiredID(expand.OutputColumnID) {
		return fmt.Errorf("outputColumnId is required")
	}
	if !oneOf(string(expand.EmptyPolicy), "", "ERROR", "EXCLUDE", "PRESERVE_PARENT") {
		return fmt.Errorf("emptyPolicy must be ERROR, EXCLUDE, or PRESERVE_PARENT")
	}
	if err := ValidateConstructionExpandedFieldSource(expand.ExpandedField); err != nil {
		return err
	}
	if expand.OrdinalColumnID != "" {
		if !requiredID(expand.OrdinalColumnID) || expand.OrdinalColumnID == expand.OutputColumnID {
			return fmt.Errorf("ordinalColumnId must be an exact ID distinct from outputColumnId")
		}
		if _, exists := input[expand.OrdinalColumnID]; exists {
			return fmt.Errorf("ordinalColumnId collides with an input column")
		}
	}
	want := make([]string, 0, len(inputColumns)+1)
	for _, column := range inputColumns {
		if column.ID != expand.InputColumnID {
			want = append(want, column.ID)
		}
	}
	want = append(want, expand.OutputColumnID)
	if expand.OrdinalColumnID != "" {
		want = append(want, expand.OrdinalColumnID)
	}
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateDeclaredOutputIDs(outputs []StageColumn, expected []string) error {
	if len(outputs) != len(expected) {
		return fmt.Errorf("outputs contain %d columns; operation requires %d", len(outputs), len(expected))
	}
	declared := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		declared[output.ID] = true
	}
	for _, id := range expected {
		if !declared[id] {
			return fmt.Errorf("outputs are missing required column id %q", id)
		}
	}
	return nil
}

func orderedStageIDs(columns []StageColumn) []string {
	ids := make([]string, 0, len(columns))
	for _, column := range columns {
		ids = append(ids, column.ID)
	}
	return ids
}

func missingConstructionColumn(field, id string) error {
	return fmt.Errorf("%s references missing column id %q", field, id)
}

func sameOperationID(stepID, operationID string) bool {
	return requiredID(operationID) && stepID == operationID
}
