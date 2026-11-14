package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// ConstructionUnpivotSourceCapability advertises the authorized populated root
// scalar fields an UNPIVOT step can melt through signed source choices, beside
// the existing Pivot, Group, Filter, and Derive source inputs. UNPIVOT melts
// value columns into one key/value pair, so only logical kinds the physical
// table compiler accepts as scalar melt inputs qualify — the kinds
// lower.TableReshapeScalarKind maps to a table scalar class, minus the date
// family: the compiler melts date and date_time into the STRING class, but
// that parity is not consent for this lane, which refuses date and date_time
// outright.
type ConstructionUnpivotSourceCapability struct {
	Supported  bool                              `json:"supported"`
	StageID    string                            `json:"stageId"`
	ReasonCode string                            `json:"reasonCode,omitempty"`
	Reason     string                            `json:"reason,omitempty"`
	Choices    []ConstructionUnpivotSourceChoice `json:"choices"`
}

// ConstructionUnpivotSourceChoice is pinned to exactly six wire fields. The
// UI capabilities schema is strict, so pivot-style extras (occurrenceId,
// fhirType, valueType, isIdentifier, isReference) must never be added.
// ColumnID is the signed materialized column id from durable server-derived
// state, mirroring pivot's choice.columnId, so the UI can restore a saved
// source-backed UNPIVOT melt input; it is "" while no live materialization
// exists.
type ConstructionUnpivotSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

// ConstructionUnpivotSourceSelection binds one server-signed root scalar field
// choice to the exact columnId the edited UNPIVOT step melts as one of its
// value inputs.
type ConstructionUnpivotSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

// validateConstructionUnpivotSourceSelections is the request-boundary guard.
// There is no invented per-request cap: the honest bound is structural, and
// the candidate prep refuses any selection whose columnId is not a declared
// melt input of the edited step, so the request channel can never outgrow what
// one melt consumes. A duplicate choiceId or columnId is refused outright.
func validateConstructionUnpivotSourceSelections(selections []ConstructionUnpivotSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := requireExactIdentity(selection.ChoiceID, fmt.Sprintf("unpivotSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, fmt.Sprintf("unpivotSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("unpivotSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

// unpivotSourceLogicalTypeIsTableScalar reports whether a root scalar field can
// be melted by UNPIVOT as far as the physical compiler is concerned. The
// decision is delegated verbatim to the compiler's own melt gate,
// lower.TableReshapeScalarKind, which maps the STRING family (string, date,
// date_time, code, uuid) plus integer, decimal, and boolean to physical scalar
// classes and refuses everything else (number, object, and the misspelled
// datetime/date-time aliases). The lane's own admission is
// unpivotSourceLogicalTypeIsMeltEligible, which layers the date-family refusal
// on top of this single delegated decision so no local whitelist can drift
// from the compiler again.
func unpivotSourceLogicalTypeIsTableScalar(logicalType string) bool {
	_, ok := lower.TableReshapeScalarKind(logicalType)
	return ok
}

// unpivotSourceLogicalTypeIsMeltEligible is the unpivot lane's own admission
// of a root scalar field as a signed melt value column. The compiler maps the
// date family to the STRING class, but compiler parity is not consent for this
// channel: a signed date/date_time melt silently loses date semantics into a
// string value column, so the lane refuses it. The canonicalization matches
// the compiler's (trim + lowercase) so no alias can smuggle the date family
// past the exclusion; the delegation still rejects every kind the compiler
// itself refuses.
func unpivotSourceLogicalTypeIsMeltEligible(logicalType string) bool {
	switch strings.ToLower(strings.TrimSpace(logicalType)) {
	case "date", "date_time":
		return false
	}
	return unpivotSourceLogicalTypeIsTableScalar(logicalType)
}

// unpivotMeltKindsSatisfiable reports whether the melt's value columns can
// share one physical value type. The unpivot lane first refuses the date
// family outright (compiler parity is not consent, see
// unpivotSourceLogicalTypeIsMeltEligible). Every remaining kind is mapped
// through the compiler's authoritative melt gate,
// lower.TableReshapeScalarKind; a melt is unsatisfiable if any input is one
// the compiler refuses as a value column, never silently dropped. The
// remaining cross-class rule is taken verbatim from the compiler's
// compatibility predicate
// (ir.tableScalarKindsCompatible: output == input || (output == "DECIMAL" &&
// input == "INTEGER")), which the lowerer applies to every unpivot input
// against the single chosen value-column type.
func unpivotMeltKindsSatisfiable(kinds []string) bool {
	classes := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if !unpivotSourceLogicalTypeIsMeltEligible(kind) {
			return false
		}
		class, ok := lower.TableReshapeScalarKind(kind)
		if !ok {
			return false
		}
		classes = append(classes, class)
	}
	if len(classes) == 0 {
		return true
	}
	for _, output := range []string{string(recipe.TableScalarString), string(recipe.TableScalarInteger), string(recipe.TableScalarDecimal), string(recipe.TableScalarBoolean)} {
		compatible := true
		for _, input := range classes {
			if !(output == input || (output == string(recipe.TableScalarDecimal) && input == string(recipe.TableScalarInteger))) {
				compatible = false
				break
			}
		}
		if compatible {
			return true
		}
	}
	return false
}

// unpivotInputColumnIDs returns the column ids an UNPIVOT operation melts into
// the key/value pair. This is the documented divergence from the Derive source
// channel, which binds slots to the two arithmetic operand ids; UNPIVOT melts
// an arbitrary declared set of value inputs and the melt columns themselves
// leave the row schema.
func unpivotInputColumnIDs(unpivot *authoringv2.ConstructionUnpivot) []string {
	if unpivot == nil {
		return nil
	}
	ids := make([]string, 0, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		ids = append(ids, input.ColumnID)
	}
	return ids
}

var unpivotSourceReasons = rowSourceLaneReasons{
	codeStem:        "UNPIVOT",
	reachReason:     "the current construction prefix does not carry root scalar source fields to this stage",
	versionReason:   "every enumerated root field choice was issued for a previous dataset snapshot; reload the current Unpivot source fields",
	routeReason:     "every root scalar field enumerated at this stage is a reference, repeated, non-root, or metadata path that the Unpivot step cannot reach",
	typeReason:      "every root scalar field enumerated at this stage is not a string, integer, decimal, or boolean scalar that the Unpivot step can melt",
	observedReason:  "every root scalar field enumerated for the Unpivot step at this stage has no observed values in the current dataset snapshot",
	genericCode:     "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS",
	genericReason:   "the selected stage has no authorized populated string, integer, decimal, or boolean scalar FHIR fields that the Unpivot step can melt",
	inventoryReason: "the row-choice inventory backing root Unpivot melt source fields is unavailable for the current dataset snapshot",
}

func (s *Service) constructionUnpivotSourceCapability(
	ctx context.Context,
	base constructionBase,
	outputID, stageID string,
) (ConstructionUnpivotSourceCapability, error) {
	result := ConstructionUnpivotSourceCapability{StageID: stageID, Choices: []ConstructionUnpivotSourceChoice{}}
	if code, reason, unavailable := importedSourceChoiceUnavailable(base.document); unavailable {
		result.ReasonCode, result.Reason = code, reason
		return result, nil
	}
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
			result.ReasonCode, result.Reason = rowSourceInventoryFailure(unpivotSourceReasons)
			return result, unavailable("construction-capabilities", result.ReasonCode, result.Reason,
				fmt.Errorf("list authorized root unpivot source choices for output %q: %w", outputID, err))
		}
		for _, choice := range choices {
			if rowSourceChoiceIsStaleVersion(choice, base.snapshot) {
				enumeration.skip(rowSourceSkipVersion)
				continue
			}
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				enumeration.skip(rowSourceSkipRoute)
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.snapshot, choice)
			if !ok {
				enumeration.skip(rowSourceCandidateSkipClass(base.snapshot, choice))
				continue
			}
			if !unpivotSourceLogicalTypeIsMeltEligible(candidate.LogicalType) {
				enumeration.skip(rowSourceSkipType)
				continue
			}
			result.Choices = append(result.Choices, ConstructionUnpivotSourceChoice{
				ChoiceID: choice.ChoiceID, FieldPath: choice.Path,
				Label:       constructionFieldDisplayLabel(base.document.RootResourceType, choice.Path, candidate.Label, choice.Label),
				LogicalType: candidate.LogicalType, IsPopulated: candidate.Populated,
				ColumnID: constructionOwnedUnpivotSourceColumnID(base.construction, stageID, choice.Path),
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
		result.ReasonCode, result.Reason = enumeration.decision(unpivotSourceReasons)
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func (s *Service) resolveConstructionUnpivotSource(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	acceptedStageID string,
	selection ConstructionUnpivotSourceSelection,
) (authoringv2.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_UNPIVOT_SOURCE_CHOICE", "the selected unpivot source field choice is invalid", nil, err)
	}
	if s.config.RowChoiceResolver == nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized unpivot source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_UNPIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.snapshot.Clone(), Route: base.document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceFieldGroupKey ||
		identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
		resolved.OccurrenceID != authoringv2.RootOccurrenceID {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_UNPIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
	}
	rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_UNPIVOT_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_UNPIVOT_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	if !unpivotSourceLogicalTypeIsTableScalar(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_UNPIVOT_SOURCE_FIELD", "the physical table compiler refuses this logical type as an unpivot melt value column", nil)
	}
	if !unpivotSourceLogicalTypeIsMeltEligible(candidate.LogicalType) {
		return authoringv2.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD", "the unpivot melt lane refuses date and date_time value columns; the physical compiler's acceptance of the date family is not consent for this channel", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return authoringv2.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return authoringv2.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_UNPIVOT_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
	}
	label := constructionFieldDisplayLabel(base.document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return authoringv2.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

// constructionCandidateWithUnpivotSources resolves the signed field choices,
// derives the durable server-owned projection facts for the edited UNPIVOT
// step's melt inputs, and carries those slots through the row-preserving
// prefix that feeds the melt. It mirrors the Derive source contract; the
// documented divergences are (1) UNPIVOT melts an arbitrary declared input
// set instead of two operand slots, (2) the melted slots are consumed and
// leave the melt's own output schema, so they are carried only by the steps
// strictly before the melt, and (3) every melt value column must share one
// physical value type, so the joint kind set is checked against the
// compiler's own compatibility rule before the candidate is accepted.
func (s *Service) constructionCandidateWithUnpivotSources(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyUnpivotSources := request.UnpivotSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationUnpivot
	candidate = preserveConstructionOwnedInputs(base.construction, candidate, "")
	if !applyUnpivotSources {
		return candidate, nil
	}
	unpivot := candidate.Steps[changedIndex].Operation.Unpivot
	if unpivot == nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_REQUIRES_UNPIVOT", "unpivotSources can only be applied to an Unpivot step", nil)
	}
	if len(request.UnpivotSources) == 0 {
		cleared, err := clearConstructionUnpivotOwnedSources(base.construction, candidate, request.ChangedStepID)
		if err != nil {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_STAGE_UNAVAILABLE", "the Unpivot input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.stages, acceptedStageID); !found {
		return authoringv2.Construction{}, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the unpivot source choices are not bound to an accepted stage", nil, nil)
	}
	inputIDs := unpivotInputColumnIDs(unpivot)
	if len(request.UnpivotSources) > len(inputIDs) {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_COLUMN_MISMATCH",
			fmt.Sprintf("unpivotSources carries %d selections but the edited Unpivot step melts %d value inputs", len(request.UnpivotSources), len(inputIDs)), nil)
	}
	for _, selection := range request.UnpivotSources {
		if !containsConstructionColumnIDLocal(inputIDs, selection.ColumnID) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_COLUMN_MISMATCH",
				fmt.Sprintf("unpivotSources columnId %q is not a melt input of the edited Unpivot step", selection.ColumnID), nil)
		}
	}

	resolved := make([]authoringv2.ConstructionSourceProjection, 0, len(request.UnpivotSources))
	for _, selection := range request.UnpivotSources {
		projection, err := s.resolveConstructionUnpivotSource(ctx, base, request, acceptedStageID, selection)
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
			return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_FIELD_ALREADY_PROJECTED",
				fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this melt input", projection.FieldPath), nil)
		}
	}

	meltKinds := make([]string, 0, len(inputIDs))
	byColumnID := make(map[string]authoringv2.ConstructionSourceProjection, len(projections))
	for _, projection := range projections {
		byColumnID[projection.ColumnID] = projection
	}
	for _, inputID := range inputIDs {
		if projection, owned := byColumnID[inputID]; owned {
			meltKinds = append(meltKinds, projection.LogicalType)
			continue
		}
		if stage, found := constructionReceiptStage(base.stages, acceptedStageID); found {
			for _, column := range stage.Columns {
				if column.ID == inputID {
					meltKinds = append(meltKinds, column.Type)
					break
				}
			}
		}
	}
	if !unpivotMeltKindsSatisfiable(meltKinds) {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_TYPES_INCOMPATIBLE",
			"the melted value columns, including the selected root field, cannot share one scalar value type; the physical table compiler refuses this melt (tableScalarKindsCompatible)", nil)
	}

	steps := make([]authoringv2.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index < changedIndex; index++ {
		step := &steps[index]
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation into the Unpivot input", nil)
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
	meltOutputs := make([]authoringv2.StageColumn, 0, len(steps[changedIndex].Outputs))
	for _, output := range steps[changedIndex].Outputs {
		if droppedOwned[output.ID] && !reowned[output.ID] {
			continue
		}
		meltOutputs = append(meltOutputs, output)
	}
	steps[changedIndex].Outputs = meltOutputs

	candidate.Steps, candidate.SourceProjections = steps, projections
	if err := validateUnpivotSourceColumns(candidate.Steps, request.ChangedStepID, request.UnpivotSources); err != nil {
		return authoringv2.Construction{}, unprocessable("construction-proposal", "UNPIVOT_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}

func validateUnpivotSourceColumns(steps []authoringv2.ConstructionStep, unpivotID string, sources []ConstructionUnpivotSourceSelection) error {
	index := constructionStepIndex(steps, unpivotID)
	if index < 0 || steps[index].Operation.Unpivot == nil {
		return fmt.Errorf("the selected Unpivot step is unavailable")
	}
	inputIDs := unpivotInputColumnIDs(steps[index].Operation.Unpivot)
	for _, source := range sources {
		if !containsConstructionColumnIDLocal(inputIDs, source.ColumnID) {
			return fmt.Errorf("unpivotSources columnId %q is not a melt input of the Unpivot step", source.ColumnID)
		}
	}
	return nil
}

func clearConstructionUnpivotOwnedSources(base, candidate authoringv2.Construction, unpivotID string) (authoringv2.Construction, error) {
	unpivotIndex := constructionStepIndex(candidate.Steps, unpivotID)
	if unpivotIndex < 0 {
		return authoringv2.Construction{}, fmt.Errorf("the edited Unpivot step is missing")
	}
	owned := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == unpivotID {
			owned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == unpivotID {
			owned[projection.ColumnID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= unpivotIndex {
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
		if projection.OwnerStepID != unpivotID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

// constructionOwnedUnpivotSourceColumnID returns the durable server-derived
// materialized column for a root scalar field at one exact stage, mirroring
// constructionOwnedDeriveSourceColumnID. An UNPIVOT owns the slot only while
// an owner UNPIVOT step whose input stage is this exact stage still melts the
// projection column as one of its declared value inputs. A vanished,
// re-pointed, or foreign-owned materialization returns "", so the UI never
// renders a restore into a column the server can no longer sign.
func constructionOwnedUnpivotSourceColumnID(construction authoringv2.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationUnpivot || step.Operation.Unpivot == nil ||
			!containsConstructionColumnIDLocal(unpivotInputColumnIDs(step.Operation.Unpivot), projection.ColumnID) ||
			constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

// ConstructionUnpivotSourceFacts derives the server-owned projection facts for
// one root scalar field from the authorized snapshot plus generated FHIR
// metadata. Apply reauthorization and receipt reconcile share this single
// derivation so a persisted unpivot-owned claim can never drift from the facts
// the server would have signed it from. Column and owner identities stay
// claim-sourced, exactly as the Apply-side DeepEquals expects them.
func ConstructionUnpivotSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	path := canonicalRowPath(fieldPath)
	if strings.TrimSpace(rootResourceType) == "" || path == "" {
		return authoringv2.ConstructionSourceProjection{}, false
	}
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		candidate, ok := uniqueRowSourceCandidate(snapshot, node.ID, rootResourceType, path)
		if !ok || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
			!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) ||
			!unpivotSourceLogicalTypeIsMeltEligible(candidate.LogicalType) {
			continue
		}
		schemaIndex, err := fhirschema.GeneratedIndex()
		if err != nil {
			continue
		}
		facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), path)
		if err != nil || facts.CanonicalPath != path || facts.Shape != fhirschema.RowPathScalar ||
			facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference {
			continue
		}
		return authoringv2.ConstructionSourceProjection{
			OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: facts.CanonicalPath,
			FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType,
			Label: constructionFieldDisplayLabel(rootResourceType, facts.CanonicalPath, candidate.Label),
		}, true
	}
	return authoringv2.ConstructionSourceProjection{}, false
}

func constructionHasUnpivotSourceInput(construction authoringv2.Construction) bool {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		if !isUnpivotOwnedSourceProjection(construction, projection) {
			continue
		}
		return true
	}
	return false
}

func isUnpivotOwnedSourceProjection(construction authoringv2.Construction, projection authoringv2.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == authoringv2.ConstructionOperationUnpivot && step.Operation.Unpivot != nil &&
		containsConstructionColumnIDLocal(unpivotInputColumnIDs(step.Operation.Unpivot), projection.ColumnID)
}

// reauthorizeConstructionUnpivotSources re-derives the server-owned facts for
// every unpivot-owned projection from the live authorized snapshot so an Apply
// cannot stage a melt whose field stopped qualifying after the proposal.
func (s *Service) reauthorizeConstructionUnpivotSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	if s.config.RowChoicePlanner == nil {
		return unavailable("commands", "ROW_CHOICE_UNAVAILABLE", "authorized unpivot source choice planning is not configured", nil)
	}
	var choices []capability.RowChoice
	planned := false
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			return fmt.Errorf("unpivot source projection %q names a missing owner step", projection.ColumnID)
		}
		step := construction.Steps[index]
		if step.Operation.Kind != authoringv2.ConstructionOperationUnpivot {
			continue
		}
		if step.Operation.Unpivot == nil || !containsConstructionColumnIDLocal(unpivotInputColumnIDs(step.Operation.Unpivot), projection.ColumnID) {
			return fmt.Errorf("unpivot source projection %q is not a melt input of its owner Unpivot step", projection.ColumnID)
		}
		if !planned {
			listed, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
			if err != nil {
				return fmt.Errorf("list authorized root unpivot source choices for output %q: %w", outputID, err)
			}
			choices, planned = listed, true
		}
		matched := false
		for _, choice := range choices {
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) != canonicalRowPath(projection.FieldPath) {
				continue
			}
			if _, found := base.snapshot.Node(choice.NodeID); !found {
				continue
			}
			derived, ok := ConstructionUnpivotSourceFacts(base.snapshot, base.document.RootResourceType, projection.FieldPath)
			if !ok {
				continue
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
			return fmt.Errorf("unpivot source projection %q no longer matches an authorized populated root scalar field", projection.ColumnID)
		}
	}
	return nil
}
