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

// ConstructionExpandSourceCapability advertises the authorized populated direct
// root primitive ARRAY fields an EXPAND step can flatten through signed source
// choices, beside the existing Pivot, Group, Filter, Derive, and Unpivot source
// inputs. EXPAND flattens one array-valued column into its scalar items, so the
// compiler's own array input gate (lowerConstructionExpand requires a public,
// Many-cardinality column whose items are a table scalar kind) qualifies only
// the direct root primitive arrays whose generated-schema element type the
// recipe construction logical-type whitelist accepts as a scalar — objects,
// Coding, nested arrays, repeated-boundary scopes, and references are refused.
type ConstructionExpandSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionExpandSourceChoice `json:"choices"`
}

// ConstructionExpandSourceChoice is pinned to exactly six wire fields. The
// UI capabilities schema is strict, so pivot-style extras (occurrenceId,
// fhirType, valueType, isIdentifier, isReference) must never be added.
// LogicalType is the ELEMENT logical type of the array (for example "string"
// for a HumanName family array), never a container label, because the existing
// EXPAND renderer only accepts scalar items. ColumnID is the signed
// materialized column id from durable server-derived state, mirroring pivot's
// choice.columnId, so the UI can restore a saved source-backed EXPAND array
// input; it is "" while no live materialization exists.
type ConstructionExpandSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

// ConstructionExpandSourceSelection binds one server-signed root primitive
// array field choice to the exact columnId that is the edited EXPAND step's
// InputColumnID. The channel admits exactly one selection per proposal.
type ConstructionExpandSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

// validateConstructionExpandSourceSelections is the request-boundary guard.
// There is no invented per-request cap: the honest bound is structural, and
// the candidate prep refuses any selection whose columnId is not the edited
// step's single declared array input, so the request channel can never
// outgrow what one expand consumes. A duplicate choiceId or columnId is
// refused outright.
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

// expandSourceElementLogicalTypeIsAdmissible is the expand lane's own admission
// of an array element as a flattenable scalar item. It delegates the primitive
// vocabulary to the persisted recipe validator's exported whitelist,
// recipe.IsValidConstructionLogicalType, so this channel can never advertise an
// element type the construction recipe would later reject. The whitelist itself
// still accepts "object" (the recipe carries object columns for other lanes),
// so the lane layers the contract's object refusal on top of the single
// delegated decision; the generated-schema gate re-checks the element through
// FHIRType (including Coding) at resolve time. If the recipe whitelist is ever
// un-exported again, this is the local canonical mirror point; the
// whitelist-drift guard test (parent-owned) must keep this gate and the recipe
// validator in lockstep.
func expandSourceElementLogicalTypeIsAdmissible(logicalType string) bool {
	kind := strings.ToLower(strings.TrimSpace(logicalType))
	if kind == "" || kind == "object" {
		return false
	}
	return recipe.IsValidConstructionLogicalType(kind)
}

// expandSourceElementFHIRTypeIsAdmissible refuses the object-bearing element
// FHIR types the generated index reports for non-primitive array items: the
// anonymous "object" shape and Coding, whose code/display payloads the EXPAND
// renderer cannot flatten to one scalar item column.
func expandSourceElementFHIRTypeIsAdmissible(fhirType string) bool {
	switch strings.TrimSpace(fhirType) {
	case "", "object", "Coding":
		return false
	}
	return true
}

// expandSourceCardinality is the single wire cardinality the lane signs into
// the server-populated ConstructionExpand.ExpandedField payload. It matches
// authoringv2.ConstructionExpandedFieldCardinalityMany, which the compiler mapping turns
// into the expression.Many the physical EXPAND gate requires
// (lower/recipe_construction_reshape.go:473).
const expandSourceCardinality = authoringv2.ConstructionExpandedFieldCardinalityMany

// expandSourceSignedField derives the server-owned operation payload from one
// resolved lane projection. The client never authors it: constructionCandidate-
// WithExpandSources overwrites whatever the candidate carried, and Apply-time
// reauthorization re-derives it from snapshot facts and refuses mismatch.
func expandSourceSignedField(projection authoringv2.ConstructionSourceProjection) *authoringv2.ConstructionExpandedFieldSource {
	return &authoringv2.ConstructionExpandedFieldSource{
		Path:        projection.FieldPath,
		Cardinality: expandSourceCardinality,
		FHIRType:    projection.FHIRType,
		LogicalType: projection.LogicalType,
	}
}

// expandSourceBackingCandidate is the EXPAND-lane counterpart of the scalar
// row-source backing check. A signed array row choice is backed by a snapshot
// candidate for a direct-root repeated array: many cardinality, the ARRAY
// projection the capability builder mints for repeated fields, and boundaries
// that only ever describe the field itself (a boundary at another path marks
// nested repetition, which is not a direct-root array the EXPAND step can
// flatten). The scalar lane's constructionGroupSourceCandidate rejects exactly
// this shape by design — an array cannot be carried as a scalar VALUE — so the
// lanes must not share one backing rule.
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

// expandSourceBackingSkipClass names the honest classification for a refusal
// to back an array row choice, mirroring expandSourceBackingCandidate clause
// by clause so the lane's reason codes stay as precise as the scalar lane's.
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

// expandSourceStepPayloadIsConsistent reports whether the owner step carries a
// shape-valid signed payload pointing at exactly this canonical field path.
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

// expandInputColumnIDs returns the column ids an EXPAND operation consumes.
// This is the documented divergence from the Unpivot source channel, which
// melts an arbitrary declared value set: EXPAND flattens exactly one declared
// array input, and the array column itself leaves the expand's own output
// schema while its prefix carries it.
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
			// The planner mints an array-shaped root field only as the
			// ExpandedScope class (applicableRowChoiceKinds): FieldGroupKey is
			// reserved for scalar grouping keys. capability.RowChoice carries
			// no Shape field, but the minting gate (capability.NewRowChoice via
			// validateRowChoiceFacts) pins ARRAY<->MANY onto ExpandedScope as
			// one class, so this filter stays alongside resolve's re-pin of
			// identity.Shape == RowChoiceArray against the decoded signed id.
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
	// The generated-schema gate mirrors the array shape rules the semantic row
	// expander enforces (compilation/row_expansion.go): a canonical, generated
	// ARRAY of MANY non-reference values whose element FHIRType still matches
	// the signed identity.
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

// constructionCandidateWithExpandSources resolves the signed field choice,
// derives the durable server-owned projection facts for the edited EXPAND
// step's single array input, and carries that column through the
// row-preserving prefix that feeds the flatten. It mirrors the Unpivot source
// contract; the documented divergences are (1) EXPAND consumes exactly one
// input column — the step's declared InputColumnID — instead of melting an
// arbitrary declared set, (2) the array column is carried by the steps
// strictly before the expand, whose own outputs leave it to be consumed
// exactly as the existing lowerConstructionExpand receives a selected-column
// expand input from an earlier stage (the EXPAND step's own declarations keep
// only its client-declared item/ordinal/preserved outputs; it must not
// re-declare its input), and (3) there is no joint melt-class check because one
// array contributes one scalar item type.
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
		// A nil channel must not strand a signed Expand step: the editing
		// client rebuilds the operation from form state and can never carry
		// the server-signed payload back. Restore it from the durable
		// base-owned projection so save/reopen round-trips compile, and
		// strip payloads nothing owns server-side.
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
		// Removal strips the signed payload as well: the cleared step no
		// longer has a candidate-side owned projection, so the same repair
		// pass that restores intact payloads strips this one's.
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
		// The EXPAND step's own declarations keep only the client-declared
		// item/ordinal/preserved outputs: lowerConstructionExpand refuses any
		// declaration equal to expand.InputColumnID
		// (lower/recipe_construction_reshape.go:530), and the input stage — not
		// this step — must present the public Many array column
		// (lower/recipe_construction_reshape.go:469-478).
		expandOutputs = append(expandOutputs, output)
	}
	steps[changedIndex].Outputs = expandOutputs

	// Sign the operation payload (RelatedExpand ContributorSource precedent):
	// authoringv2.StageColumn carries no Cardinality, so the compiled Many
	// cardinality of the materialized array input is assigned from this
	// server-derived shape in the authoring-to-recipe mapping (owned by the
	// parent at internal/explorer/compilation/construction.go). Clone the
	// operation payload before mutation: the step copy above still aliases the
	// caller's ConstructionExpand pointer.
	signed := expandSourceSignedField(resolved[0])
	expanded := *steps[changedIndex].Operation.Expand
	expanded.ExpandedField = signed
	steps[changedIndex].Operation.Expand = &expanded
	// First-step note: with no prefix, the array column reaches the input stage
	// through construction.SourceProjections, which is how the root
	// SOURCE_PROJECTION stage's public columns are produced
	// (internal/explorer/compilation/construction.go:33-43 plus the semantic
	// source verifier at internal/explorer/compilation/semantic_compile.go:507-533).
	// The semantic source verifier carries the EXPAND-owned array accept
	// branch (facts-proved RowPathArray + Many + non-reference), and the
	// semantic selector resolver derives the compiled cardinality from the
	// generated schema; the lane never hacks cardinality through stage text.

	candidate.Steps, candidate.SourceProjections = steps, projections
	candidate = repairSignedExpandFields(base.construction, candidate, resolved)
	if err := validateExpandSourceColumns(candidate.Steps, request.ChangedStepID, request.ExpandSources, resolved); err != nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "EXPAND_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

// repairSignedExpandFields reconciles every EXPAND step's server-signed
// payload against server-derived ownership. A projection counts as trusted
// ownership ONLY when it equals (byte-for-byte) a projection this wave's
// resolve minted from the live snapshot (minted) or a durable base-owned
// projection the lane's own resolve wrote and Apply re-proved. Client-authored
// candidate projections never establish ownership: without a matching minted
// or durable entry the payload is stripped, and the authoring unowned-source
// wall refuses the dangling step. Restoring an owned payload always re-derives
// it from the trusted projection, never from client payload text. Apply-time
// reauthorization re-proves everything against the live snapshot regardless.
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
		// Candidate entries are trusted only when byte-equal to this wave's
		// mint; everything else falls back to the durable base, then to
		// not-owned (stripped), never to client text.
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
	// Drop expand-owned projections the client authored outright: any
	// projection claiming an EXPAND step as owner that the server did not
	// mint this wave or durably own in the base is client text and must not
	// reach the receipt document.
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

// validateExpandSourceColumns is the final candidate-prep gate: the selection's
// columnId must be the step's single Expand InputColumnID, and the (now
// server-signed) ExpandedField must be internally consistent and name exactly
// the resolved canonical array field with the Many wire cardinality, so a
// client-forged operation payload cannot survive this function.
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

// constructionOwnedExpandSourceColumnID returns the durable server-derived
// materialized column for a root array field at one exact stage, mirroring
// constructionOwnedUnpivotSourceColumnID. An EXPAND owns the slot only while an
// owner EXPAND step whose input stage is this exact step's input stage still
// declares the projection column as its single Expand InputColumnID. A
// vanished, re-pointed, or foreign-owned materialization returns "", so the UI
// never renders a restore into a column the server can no longer sign.
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
		// The server only signs restoration of a live column while the owner
		// step still carries its coherent signed payload for exactly this root
		// field; a vanished, stripped, or forged payload returns "" like any
		// other un-signable materialization.
		if !expandSourceStepPayloadIsConsistent(step, projection.FieldPath) {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

// ConstructionExpandSourceFacts derives the server-owned projection facts for
// one root primitive array field from the authorized snapshot plus generated
// FHIR metadata. Apply reauthorization and receipt reconcile share this single
// derivation so a persisted expand-owned claim can never drift from the facts
// the server would have signed it from. Column and owner identities stay
// claim-sourced, exactly as the Apply-side DeepEquals expects them.
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

// reauthorizeConstructionExpandSources re-derives the server-owned facts for
// every expand-owned projection from the live authorized snapshot so an Apply
// cannot stage a flatten whose array stopped qualifying after the proposal.
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
		// Fail-closed on the operation payload itself: a lane-owned input whose
		// step lost (or forged) its server-signed ExpandedField never reaches
		// the claim-vs-redo DeepEqual below.
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
