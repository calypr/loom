package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type ConstructionExpandSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionExpandSourceChoice `json:"choices"`
}

type ConstructionExpandSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

type ConstructionExpandSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

func validateConstructionExpandSourceSelections(selections []ConstructionExpandSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := requireExactIdentity(selection.ChoiceID, fmt.Sprintf("expandSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, fmt.Sprintf("expandSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("expandSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

func expandSourceElementLogicalTypeIsAdmissible(logicalType string) bool {
	kind := strings.ToLower(strings.TrimSpace(logicalType))
	if kind == "" || kind == "object" {
		return false
	}
	return recipe.IsValidConstructionLogicalType(kind)
}

func expandSourceElementFHIRTypeIsAdmissible(fhirType string) bool {
	switch strings.TrimSpace(fhirType) {
	case "", "object", "Coding":
		return false
	}
	return true
}

const expandSourceCardinality = authoringv2.ConstructionExpandedFieldCardinalityMany

func expandSourceSignedField(projection authoringv2.ConstructionSourceProjection) *authoringv2.ConstructionExpandedFieldSource {
	return &authoringv2.ConstructionExpandedFieldSource{
		Path:        projection.FieldPath,
		Cardinality: expandSourceCardinality,
		FHIRType:    projection.FHIRType,
		LogicalType: projection.LogicalType,
	}
}

func expandSourceBackingCandidate(snapshot capability.Snapshot, nodeID, resourceType, path string) (capability.Candidate, bool) {
	candidate, found := uniqueRowSourceCandidate(snapshot, nodeID, resourceType, path)
	if !found || !candidate.Populated || !capability.IsRepeatedCardinality(candidate.Cardinality) ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionArray) ||
		!expandSourceIsDirectRootRepeated(candidate, path) {
		return capability.Candidate{}, false
	}
	return candidate, true
}

func expandSourceIsDirectRootRepeated(candidate capability.Candidate, path string) bool {
	for _, boundary := range candidate.RepeatedBoundaries {
		if canonicalRowPath(boundary.Path) != canonicalRowPath(path) {
			return false
		}
	}
	return true
}

func expandSourceBackingSkipClass(snapshot capability.Snapshot, choice capability.RowChoice) rowSourceSkipClass {
	candidate, found := uniqueRowSourceCandidate(snapshot, choice.NodeID, choice.ResourceType, choice.Path)
	switch {
	case !found, !containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionArray):
		return rowSourceSkipInventory
	case !candidate.Populated:
		return rowSourceSkipObserved
	case !capability.IsRepeatedCardinality(candidate.Cardinality), !expandSourceIsDirectRootRepeated(candidate, choice.Path):
		return rowSourceSkipRoute
	case !capability.IsSupportedConstructionSourceType(candidate.LogicalType):
		return rowSourceSkipType
	default:
		return rowSourceSkipRoute
	}
}

func expandSourceStepPayloadIsConsistent(step authoringv2.ConstructionStep, fieldPath string) bool {
	if step.Operation.Expand == nil || step.Operation.Expand.ExpandedField == nil {
		return false
	}
	source := step.Operation.Expand.ExpandedField
	if err := authoringv2.ValidateConstructionExpandedFieldSource(source); err != nil {
		return false
	}
	return canonicalRowPath(source.Path) == canonicalRowPath(fieldPath)
}

func expandInputColumnIDs(expand *authoringv2.ConstructionExpand) []string {
	if expand == nil || strings.TrimSpace(expand.InputColumnID) == "" {
		return nil
	}
	return []string{expand.InputColumnID}
}

var expandSourceReasons = rowSourceLaneReasons{
	codeStem:        "EXPAND",
	reachReason:     "the current construction prefix does not carry root array source fields to this stage",
	versionReason:   "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Expand source fields",
	routeReason:     "every root array field enumerated at this stage is a reference, non-root, or metadata path that the Expand step cannot reach",
	typeReason:      "every root array field enumerated at this stage has object or Coding element values that the Expand step cannot flatten to scalar items",
	observedReason:  "every root array field enumerated for the Expand step at this stage has no observed values in the current dataset snapshot",
	genericCode:     "NO_ELIGIBLE_POPULATED_EXPAND_SOURCE_FIELDS",
	genericReason:   "the selected stage has no authorized populated primitive array FHIR fields that the Expand step can flatten",
	inventoryReason: "the row-choice inventory backing root Expand array source fields is unavailable for the current dataset snapshot",
}

func (s *Service) constructionExpandSourceCapability(
	ctx context.Context,
	base constructionBase,
	outputID, stageID string,
) (ConstructionExpandSourceCapability, error) {
	result := ConstructionExpandSourceCapability{StageID: stageID, Choices: []ConstructionExpandSourceChoice{}}
	if _, found := constructionReceiptStage(base.stages, stageID); !found {
		result.ReasonCode = "STALE_STAGE_REFERENCE"
		result.Reason = "the selected stage is not present in the current compiled output"
		return result, nil
	}
	enumeration := rowSourceEnumeration{
		rootReachable:     constructionRootProjectionCanReachStage(base.construction, stageID),
		plannerConfigured: s.config.RowChoicePlanner != nil,
	}
	if enumeration.rootReachable && enumeration.plannerConfigured {
		choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
		if err != nil {
			result.ReasonCode, result.Reason = rowSourceInventoryFailure(expandSourceReasons)
			return result, unavailable("construction-capabilities", result.ReasonCode, result.Reason,
				fmt.Errorf("list authorized root expand source choices for output %q: %w", outputID, err))
		}
		for _, choice := range choices {
			if rowSourceChoiceIsStaleVersion(choice, base.snapshot) {
				enumeration.skip(rowSourceSkipVersion)
				continue
			}
			if choice.Kind != capability.RowChoiceExpandedScope || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceMany || canonicalRowPath(choice.Path) == "resourceType" {
				enumeration.skip(rowSourceSkipRoute)
				continue
			}
			candidate, ok := expandSourceBackingCandidate(base.snapshot, choice.NodeID, choice.ResourceType, choice.Path)
			if !ok {
				enumeration.skip(expandSourceBackingSkipClass(base.snapshot, choice))
				continue
			}
			if !expandSourceElementFHIRTypeIsAdmissible(choice.FHIRType) {
				enumeration.skip(rowSourceSkipType)
				continue
			}
			if !expandSourceElementLogicalTypeIsAdmissible(candidate.LogicalType) {
				enumeration.skip(rowSourceSkipType)
				continue
			}
			result.Choices = append(result.Choices, ConstructionExpandSourceChoice{
				ChoiceID: choice.ChoiceID, FieldPath: choice.Path,
				Label:       constructionFieldDisplayLabel(base.document.RootResourceType, choice.Path, candidate.Label, choice.Label),
				LogicalType: candidate.LogicalType, IsPopulated: candidate.Populated,
				ColumnID: constructionOwnedExpandSourceColumnID(base.construction, stageID, choice.Path),
			})
		}
	}
	sort.SliceStable(result.Choices, func(i, j int) bool {
		if result.Choices[i].FieldPath != result.Choices[j].FieldPath {
			return result.Choices[i].FieldPath < result.Choices[j].FieldPath
		}
		return result.Choices[i].ChoiceID < result.Choices[j].ChoiceID
	})
	if len(result.Choices) == 0 {
		result.ReasonCode, result.Reason = enumeration.decision(expandSourceReasons)
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func (s *Service) resolveConstructionExpandSource(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	acceptedStageID string,
	selection ConstructionExpandSourceSelection,
) (authoringv2.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_EXPAND_SOURCE_CHOICE", "the selected expand source field choice is invalid", nil, err)
	}
	if s.config.RowChoiceResolver == nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized expand source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_EXPAND_SOURCE_CHOICE", "reload the current populated root array field choice", nil, nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.snapshot.Clone(), Route: base.document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceExpanded,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceExpandedScope ||
		identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
		identity.Path != resolved.ScopePath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceMany || identity.Shape != capability.RowChoiceArray ||
		resolved.OccurrenceID != authoringv2.RootOccurrenceID {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_EXPAND_SOURCE_CHOICE", "reload the current populated root array field choice", nil, resolveErr)
	}
	rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_EXPAND_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, ok := expandSourceBackingCandidate(base.snapshot, rootNode.ID, rootNode.ResourceType, resolved.ScopePath)
	if !ok {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_EXPAND_SOURCE_FIELD", "the selected root field is not a populated direct array source field", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.ScopePath)
	if err != nil || facts.CanonicalPath != resolved.ScopePath || facts.Shape != fhirschema.RowPathArray ||
		facts.Cardinality != fhirschema.RowCardinalityMany || facts.Reference || facts.FHIRType != identity.FHIRType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_EXPAND_SOURCE_SCHEMA", "the selected field no longer matches generated array FHIR metadata", nil, err)
	}
	elementType := candidate.LogicalType
	if strings.TrimSpace(elementType) == "" {
		elementType = facts.FHIRType
	}
	if !expandSourceElementFHIRTypeIsAdmissible(facts.FHIRType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_EXPAND_SOURCE_FIELD", "the selected root array holds object or Coding element values that the Expand step cannot flatten to a scalar item column", nil)
	}
	if !expandSourceElementLogicalTypeIsAdmissible(elementType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_EXPAND_SOURCE_FIELD", "the selected root array element is not a primitive logical type the construction recipe can declare", nil)
	}
	label := constructionFieldDisplayLabel(base.document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return authoringv2.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: elementType, Label: label,
	}, nil
}

func (s *Service) constructionCandidateWithExpandSources(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyExpandSources := request.ExpandSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationExpand
	candidate = preserveConstructionOwnedInputs(base.construction, candidate, "")
	if !applyExpandSources {
		return repairSignedExpandFields(base.construction, candidate, nil), nil
	}
	expand := candidate.Steps[changedIndex].Operation.Expand
	if expand == nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_REQUIRES_EXPAND", "expandSources can only be applied to an Expand step", nil)
	}
	if len(request.ExpandSources) == 0 {
		cleared, err := clearConstructionExpandOwnedSources(base.construction, candidate, request.ChangedStepID)
		if err != nil {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return repairSignedExpandFields(base.construction, cleared, nil), nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_STAGE_UNAVAILABLE", "the Expand input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.stages, acceptedStageID); !found {
		return authoringv2.Construction{}, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the expand source choices are not bound to an accepted stage", nil, nil)
	}
	inputIDs := expandInputColumnIDs(expand)
	if len(request.ExpandSources) != 1 || len(inputIDs) != 1 {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_COLUMN_MISMATCH",
			fmt.Sprintf("expandSources carries %d selections but the edited Expand step consumes one array input column", len(request.ExpandSources)), nil)
	}
	for _, selection := range request.ExpandSources {
		if !containsConstructionColumnIDLocal(inputIDs, selection.ColumnID) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_COLUMN_MISMATCH",
				fmt.Sprintf("expandSources columnId %q is not the input column of the edited Expand step", selection.ColumnID), nil)
		}
	}

	resolved := make([]authoringv2.ConstructionSourceProjection, 0, len(request.ExpandSources))
	for _, selection := range request.ExpandSources {
		projection, err := s.resolveConstructionExpandSource(ctx, base, request, acceptedStageID, selection)
		if err != nil {
			return authoringv2.Construction{}, err
		}
		resolved = append(resolved, projection)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ColumnID < resolved[j].ColumnID })

	droppedOwned := make(map[string]bool)
	for _, projection := range base.construction.SourceProjections {
		if projection.OwnerStepID == request.ChangedStepID {
			droppedOwned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == request.ChangedStepID {
			droppedOwned[projection.ColumnID] = true
		}
	}
	reowned := make(map[string]bool, len(resolved))
	for _, projection := range resolved {
		reowned[projection.ColumnID] = true
	}

	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(base.construction.SourceProjections)+len(resolved))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != request.ChangedStepID {
			projections = append(projections, projection)
		}
	}
	for _, projection := range base.construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != request.ChangedStepID && !hasPivotSourceProjection(projections, projection.ColumnID) &&
			!constructionOwnerInputsClearedFromCandidate(base.construction, candidate, projection.OwnerStepID) {
			projections = append(projections, projection)
		}
	}
	projections = append(projections, resolved...)

	for _, projection := range resolved {
		if constructionRootFieldAlreadyProjected(base.document, candidate.Steps, projections, projection.ColumnID, projection.FieldPath) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_FIELD_ALREADY_PROJECTED",
				fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this expand input", projection.FieldPath), nil)
		}
	}

	steps := make([]authoringv2.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index < changedIndex; index++ {
		step := &steps[index]
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_CANNOT_CROSS_STAGE", "a selected root array field cannot be carried through an earlier row-changing operation into the Expand input", nil)
		}
		outputs := make([]authoringv2.StageColumn, 0, len(step.Outputs)+len(resolved))
		for _, output := range step.Outputs {
			if droppedOwned[output.ID] && !reowned[output.ID] {
				continue
			}
			outputs = append(outputs, output)
		}
		for _, projection := range resolved {
			outputs = appendStageColumnIfAbsent(outputs, authoringv2.StageColumn{
				ID: projection.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		step.Outputs = outputs
	}
	expandOutputs := make([]authoringv2.StageColumn, 0, len(steps[changedIndex].Outputs))
	for _, output := range steps[changedIndex].Outputs {
		if droppedOwned[output.ID] && !reowned[output.ID] {
			continue
		}
		expandOutputs = append(expandOutputs, output)
	}
	steps[changedIndex].Outputs = expandOutputs

	signed := expandSourceSignedField(resolved[0])
	expanded := *steps[changedIndex].Operation.Expand
	expanded.ExpandedField = signed
	steps[changedIndex].Operation.Expand = &expanded

	candidate.Steps, candidate.SourceProjections = steps, projections
	candidate = repairSignedExpandFields(base.construction, candidate, resolved)
	if err := validateExpandSourceColumns(candidate.Steps, request.ChangedStepID, request.ExpandSources, resolved); err != nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

func repairSignedExpandFields(base authoringv2.Construction, candidate authoringv2.Construction, minted []authoringv2.ConstructionSourceProjection) authoringv2.Construction {
	baseProjections := make(map[string]authoringv2.ConstructionSourceProjection, len(base.SourceProjections))
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID != "" {
			baseProjections[projection.ColumnID] = projection
		}
	}
	mintedProjections := make(map[string]authoringv2.ConstructionSourceProjection, len(minted))
	for _, projection := range minted {
		mintedProjections[projection.ColumnID] = projection
	}
	candidateProjections := make(map[string]authoringv2.ConstructionSourceProjection, len(candidate.SourceProjections))
	for _, projection := range candidate.SourceProjections {
		candidateProjections[projection.ColumnID] = projection
	}
	expandOwnerIDs := make(map[string]bool, len(candidate.Steps))
	for _, step := range candidate.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationExpand && step.Operation.Expand != nil {
			expandOwnerIDs[step.ID] = true
		}
	}
	trustedByColumn := make(map[string]authoringv2.ConstructionSourceProjection, len(candidate.Steps))
	for index := range candidate.Steps {
		step := &candidate.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationExpand || step.Operation.Expand == nil {
			continue
		}
		inputID := step.Operation.Expand.InputColumnID
		projection, owned := candidateProjections[inputID]
		minted, hasMinted := mintedProjections[inputID]
		if !owned || projection.OwnerStepID != step.ID || !hasMinted || !reflect.DeepEqual(projection, minted) {
			if durable, baseOwned := baseProjections[inputID]; baseOwned && durable.OwnerStepID == step.ID {
				projection = durable
			} else {
				owned = false
			}
		}
		if !owned {
			if step.Operation.Expand.ExpandedField != nil {
				expanded := *step.Operation.Expand
				expanded.ExpandedField = nil
				step.Operation.Expand = &expanded
			}
			continue
		}
		trustedByColumn[inputID] = projection
		if expandSourceStepPayloadIsConsistent(*step, projection.FieldPath) {
			continue
		}
		expanded := *step.Operation.Expand
		expanded.ExpandedField = expandSourceSignedField(projection)
		step.Operation.Expand = &expanded
	}
	if len(candidate.SourceProjections) > 0 {
		filtered := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections))
		for _, projection := range candidate.SourceProjections {
			if !expandOwnerIDs[projection.OwnerStepID] {
				filtered = append(filtered, projection)
				continue
			}
			if trusted, ok := trustedByColumn[projection.ColumnID]; ok && trusted.OwnerStepID == projection.OwnerStepID && reflect.DeepEqual(trusted, projection) {
				filtered = append(filtered, projection)
			}
		}
		candidate.SourceProjections = filtered
	}
	return candidate
}

func validateExpandSourceColumns(steps []authoringv2.ConstructionStep, expandID string, sources []ConstructionExpandSourceSelection, resolved []authoringv2.ConstructionSourceProjection) error {
	index := constructionStepIndex(steps, expandID)
	if index < 0 || steps[index].Operation.Expand == nil {
		return fmt.Errorf("the selected Expand step is unavailable")
	}
	inputIDs := expandInputColumnIDs(steps[index].Operation.Expand)
	for _, source := range sources {
		if !containsConstructionColumnIDLocal(inputIDs, source.ColumnID) {
			return fmt.Errorf("expandSources columnId %q is not the input column of the Expand step", source.ColumnID)
		}
	}
	byColumnID := make(map[string]authoringv2.ConstructionSourceProjection, len(resolved))
	for _, projection := range resolved {
		byColumnID[projection.ColumnID] = projection
	}
	signed := steps[index].Operation.Expand.ExpandedField
	if signed == nil {
		return fmt.Errorf("the Expand step is missing its server-signed array source payload")
	}
	if err := authoringv2.ValidateConstructionExpandedFieldSource(signed); err != nil {
		return fmt.Errorf("the Expand step's server-signed array source payload is invalid: %w", err)
	}
	for _, source := range sources {
		projection, found := byColumnID[source.ColumnID]
		if !found {
			return fmt.Errorf("expandSources columnId %q has no resolved source projection", source.ColumnID)
		}
		if canonicalRowPath(signed.Path) != canonicalRowPath(projection.FieldPath) {
			return fmt.Errorf("the Expand step's signed array source path %q does not match the resolved root field %q", signed.Path, projection.FieldPath)
		}
		if signed.Cardinality != expandSourceCardinality {
			return fmt.Errorf("the Expand step's signed array source cardinality %q must be %q", signed.Cardinality, expandSourceCardinality)
		}
		if signed.FHIRType != projection.FHIRType || signed.LogicalType != projection.LogicalType {
			return fmt.Errorf("the Expand step's signed array source element type does not match the resolved root field %q", projection.FieldPath)
		}
	}
	return nil
}

func clearConstructionExpandOwnedSources(base, candidate authoringv2.Construction, expandID string) (authoringv2.Construction, error) {
	expandIndex := constructionStepIndex(candidate.Steps, expandID)
	if expandIndex < 0 {
		return authoringv2.Construction{}, fmt.Errorf("the edited Expand step is missing")
	}
	owned := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == expandID {
			owned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == expandID {
			owned[projection.ColumnID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= expandIndex {
			outputs := make([]authoringv2.StageColumn, 0, len(step.Outputs))
			for _, output := range step.Outputs {
				if owned[output.ID] {
					continue
				}
				outputs = append(outputs, output)
			}
			step.Outputs = outputs
		}
		steps = append(steps, step)
	}
	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != expandID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnedExpandSourceColumnID(construction authoringv2.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationExpand || step.Operation.Expand == nil ||
			!containsConstructionColumnIDLocal(expandInputColumnIDs(step.Operation.Expand), projection.ColumnID) ||
			constructionStepInputStageID(step) != stageID {
			continue
		}
		if !expandSourceStepPayloadIsConsistent(step, projection.FieldPath) {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func ConstructionExpandSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	path := canonicalRowPath(fieldPath)
	if strings.TrimSpace(rootResourceType) == "" || path == "" {
		return authoringv2.ConstructionSourceProjection{}, false
	}
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		candidate, ok := expandSourceBackingCandidate(snapshot, node.ID, rootResourceType, path)
		if !ok {
			continue
		}
		schemaIndex, err := fhirschema.GeneratedIndex()
		if err != nil {
			continue
		}
		facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), path)
		if err != nil || facts.CanonicalPath != path || facts.Shape != fhirschema.RowPathArray ||
			facts.Cardinality != fhirschema.RowCardinalityMany || facts.Reference {
			continue
		}
		elementType := candidate.LogicalType
		if strings.TrimSpace(elementType) == "" {
			elementType = facts.FHIRType
		}
		if !expandSourceElementFHIRTypeIsAdmissible(facts.FHIRType) || !expandSourceElementLogicalTypeIsAdmissible(elementType) {
			continue
		}
		return authoringv2.ConstructionSourceProjection{
			OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: facts.CanonicalPath,
			FHIRType: facts.FHIRType, LogicalType: elementType,
			Label: constructionFieldDisplayLabel(rootResourceType, facts.CanonicalPath, candidate.Label),
		}, true
	}
	return authoringv2.ConstructionSourceProjection{}, false
}

func constructionHasExpandSourceInput(construction authoringv2.Construction) bool {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		if !isExpandOwnedSourceProjection(construction, projection) {
			continue
		}
		return true
	}
	return false
}

func isExpandOwnedSourceProjection(construction authoringv2.Construction, projection authoringv2.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == authoringv2.ConstructionOperationExpand && step.Operation.Expand != nil &&
		containsConstructionColumnIDLocal(expandInputColumnIDs(step.Operation.Expand), projection.ColumnID)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	if s.config.RowChoicePlanner == nil {
		return unavailable("commands", "ROW_CHOICE_UNAVAILABLE", "authorized expand source choice planning is not configured", nil)
	}
	var choices []capability.RowChoice
	planned := false
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			return fmt.Errorf("expand source projection %q names a missing owner step", projection.ColumnID)
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationExpand {
			continue
		}
		if step.Operation.Expand == nil || !containsConstructionColumnIDLocal(expandInputColumnIDs(step.Operation.Expand), projection.ColumnID) {
			return fmt.Errorf("expand source projection %q is not the input column of its owner Expand step", projection.ColumnID)
		}
		signed := step.Operation.Expand.ExpandedField
		if signed == nil {
			return fmt.Errorf("expand source projection %q has no server-consistent Expand payload: missing signed array source", projection.ColumnID)
		}
		if err := authoringv2.ValidateConstructionExpandedFieldSource(signed); err != nil {
			return fmt.Errorf("expand source projection %q has no server-consistent Expand payload: %w", projection.ColumnID, err)
		}
		if !planned {
			listed, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
			if err != nil {
				return fmt.Errorf("list authorized root expand source choices for output %q: %w", outputID, err)
			}
			choices, planned = listed, true
		}
		matched := false
		for _, choice := range choices {
			if choice.Kind != capability.RowChoiceExpandedScope || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceMany || canonicalRowPath(choice.Path) != canonicalRowPath(projection.FieldPath) {
				continue
			}
			if _, found := base.snapshot.Node(choice.NodeID); !found {
				continue
			}
			derived, ok := ConstructionExpandSourceFacts(base.snapshot, base.document.RootResourceType, projection.FieldPath)
			if !ok {
				continue
			}
			if canonicalRowPath(signed.Path) != canonicalRowPath(derived.FieldPath) ||
				signed.Cardinality != expandSourceCardinality ||
				signed.FHIRType != derived.FHIRType || signed.LogicalType != derived.LogicalType {
				return fmt.Errorf("expand source projection %q no longer matches the live authorized root array field behind its Expand payload", projection.ColumnID)
			}
			expected := authoringv2.ConstructionSourceProjection{
				ColumnID: projection.ColumnID, OwnerStepID: projection.OwnerStepID,
				OccurrenceID: derived.OccurrenceID, FieldPath: derived.FieldPath,
				FHIRType: derived.FHIRType, LogicalType: derived.LogicalType, Label: derived.Label,
			}
			matched = reflect.DeepEqual(expected, projection)
			break
		}
		if !matched {
			return fmt.Errorf("expand source projection %q no longer matches an authorized populated root primitive array field", projection.ColumnID)
		}
	}
	return nil
}
