package construction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
)

type ConstructionCategoryDiscoveryRequest struct {
	Project               string
	ExplorerID            string
	SnapshotToken         string
	ExpectedDraftVersion  int64
	ExpectedDraftDigest   string
	OutputID              string
	StageID               string
	CategoryColumnID      string
	ValueColumnID         string
	GroupKeyIDs           []string                           `json:"groupKeyIds,omitempty"`
	PivotStepID           string                             `json:"pivotStepId,omitempty"`
	PivotSources          []ConstructionPivotSourceSelection `json:"pivotSources,omitempty"`
	CandidateConstruction *ws.Construction                   `json:"candidateConstruction,omitempty"`

	compileCandidate bool
}

func (r ConstructionCategoryDiscoveryRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID, "stageId": r.StageID,
		"categoryColumnId": r.CategoryColumnID, "valueColumnId": r.ValueColumnID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.CategoryColumnID == r.ValueColumnID {
		return fmt.Errorf("categoryColumnId and valueColumnId must differ")
	}
	if err := validateConstructionPivotSourceSelections(r.PivotSources); err != nil {
		return err
	}
	if len(r.PivotSources) != 0 {
		if err := requireExactIdentity(r.PivotStepID, "pivotStepId"); err != nil {
			return err
		}
		if r.CandidateConstruction == nil {
			return fmt.Errorf("candidateConstruction is required when pivotSources are supplied")
		}
	} else if r.PivotStepID != "" {
		if err := requireExactIdentity(r.PivotStepID, "pivotStepId"); err != nil {
			return err
		}
	}
	return nil
}

type ConstructionDiscoveredCategory struct {
	Key   ws.TableScalar `json:"key"`
	Label string         `json:"label"`
}

type ConstructionCategoryDiscoveryResponse struct {
	SnapshotToken    string                           `json:"snapshotToken"`
	DraftVersion     int64                            `json:"draftVersion"`
	DraftDigest      string                           `json:"draftDigest"`
	OutputID         string                           `json:"outputId"`
	StageID          string                           `json:"stageId"`
	CategoryColumnID string                           `json:"categoryColumnId"`
	ValueColumnID    string                           `json:"valueColumnId"`
	Outcome          string                           `json:"outcome"`
	Complete         bool                             `json:"complete"`
	ProofFingerprint string                           `json:"proofFingerprint,omitempty"`
	Categories       []ConstructionDiscoveredCategory `json:"categories"`
	Limit            int                              `json:"limit,omitempty"`
	Message          string                           `json:"message,omitempty"`
}

const (
	constructionCategoryDiscoveryComplete           = "COMPLETE"
	constructionCategoryDiscoveryLimitExceeded      = "LIMIT_EXCEEDED"
	constructionCategoryDiscoveryMissingUnsupported = "MISSING_UNSUPPORTED"
	constructionCategoryDiscoveryNoValues           = "NO_VALUES"
)

type constructionScanFlow struct {
	loadBase                   func(context.Context, string, string, string, int64, string, string) (Base, error)
	compile                    func(context.Context, authoringcore.CompileRequest) (*receiptmodel.CompilationReceipt, error)
	verifyProposalReceipt      func(context.Context, string, *receiptmodel.CompilationReceipt, string, string, string, capability.Snapshot, *ws.Workspace) (ws.Workspace, error)
	withPivotSources           func(context.Context, Base, ConstructionProposalRequest, ws.Construction) (ws.Construction, map[string]bool, error)
	scanCategories             CategoryScanner
	scanPopulation             ColumnPopulationScanner
	selectionMembersCollection string
	populationCache            *columnPopulationCache
}

func (s constructionScanFlow) compileConstructionCategoryCandidate(
	ctx context.Context,
	base Base,
	request ConstructionCategoryDiscoveryRequest,
) (Base, string, error) {
	if request.CandidateConstruction == nil {
		return Base{}, "", malformed("construction-category-discovery", "candidateConstruction is required when pivotSources are supplied", nil)
	}
	raw, err := json.Marshal(request.CandidateConstruction)
	if err != nil {
		return Base{}, "", malformed("construction-category-discovery", "candidateConstruction is invalid", err)
	}
	var candidate ws.Construction
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return Base{}, "", malformed("construction-category-discovery", "candidateConstruction is invalid", err)
	}
	pivotIndex := constructionStepIndex(candidate.Steps, request.PivotStepID)
	if pivotIndex < 0 {
		groupKeys := append([]string(nil), request.GroupKeyIDs...)
		if len(groupKeys) == 0 {
			for _, selection := range request.PivotSources {
				if selection.ColumnID != request.CategoryColumnID && selection.ColumnID != request.ValueColumnID {
					groupKeys = append(groupKeys, selection.ColumnID)
				}
			}
		}
		if len(groupKeys) == 0 {
			return Base{}, "", unprocessable("construction-category-discovery", "PIVOT_ROW_KEY_REQUIRED", "choose at least one row key before discovering Pivot categories", nil)
		}
		input := ws.ConstructionInputRef{Kind: ws.ConstructionInputSourceProjection}
		if request.StageID != recipe.ConstructionSourceProjectionID {
			inputIndex := constructionStepIndex(candidate.Steps, request.StageID)
			if inputIndex < 0 {
				return Base{}, "", conflict("construction-category-discovery", "STALE_STAGE_REFERENCE", "the requested input stage is not present in candidateConstruction", nil, nil)
			}
			candidate.Steps = candidate.Steps[:inputIndex+1]
			input = ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: request.StageID}
		}
		candidate.Steps = append(candidate.Steps, ws.ConstructionStep{
			ID: request.PivotStepID, Inputs: []ws.ConstructionInputRef{input},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
				ConstructionID: request.PivotStepID, GroupKeyIDs: groupKeys,
				CategoryColumnID: request.CategoryColumnID, ValueColumnID: request.ValueColumnID,
				DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
				MissingCellPolicy:      ws.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
			}},
		})
		pivotIndex = len(candidate.Steps) - 1
	}
	if candidate.Steps[pivotIndex].Operation.Kind != ws.ConstructionOperationPivot || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return Base{}, "", unprocessable("construction-category-discovery", "PIVOT_SOURCE_REQUIRES_PIVOT", "pivotStepId must identify the candidate Pivot", nil)
	}
	pivot := candidate.Steps[pivotIndex].Operation.Pivot
	pivot.CategoryColumnID, pivot.ValueColumnID = request.CategoryColumnID, request.ValueColumnID
	if len(request.GroupKeyIDs) != 0 {
		pivot.GroupKeyIDs = append([]string(nil), request.GroupKeyIDs...)
	}
	proposalRequest := ConstructionProposalRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		ChangedStepID: request.PivotStepID, CandidateConstruction: candidate,
		PivotSources: append([]ConstructionPivotSourceSelection(nil), request.PivotSources...),
	}
	candidate, _, err = s.withPivotSources(ctx, base, proposalRequest, candidate)
	if err != nil {
		return Base{}, "", err
	}
	pivotIndex = constructionStepIndex(candidate.Steps, request.PivotStepID)
	if pivotIndex < 0 || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return Base{}, "", unprocessable("construction-category-discovery", "PIVOT_SOURCE_REQUIRES_PIVOT", "the candidate Pivot was not preserved", nil)
	}

	candidate.Steps = append([]ws.ConstructionStep(nil), candidate.Steps[:pivotIndex+1]...)
	candidate.SourceProjections = pruneConstructionCategorySourceProjections(candidate.SourceProjections, candidate.Steps)
	pivotStep := &candidate.Steps[pivotIndex]
	inputStageID := constructionStepInputStageID(*pivotStep)
	inputColumns, err := constructionAuthoringStageColumns(base, candidate.Steps, inputStageID, candidate.SourceProjections)
	if err != nil {
		return Base{}, "", unprocessable("construction-category-discovery", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
	}
	inputByID := make(map[string]ws.StageColumn, len(inputColumns))
	for _, column := range inputColumns {
		inputByID[column.ID] = column
	}
	category, categoryOK := inputByID[pivotStep.Operation.Pivot.CategoryColumnID]
	value, valueOK := inputByID[pivotStep.Operation.Pivot.ValueColumnID]
	if !categoryOK || !valueOK {
		return Base{}, "", unprocessable("construction-category-discovery", "INVALID_PIVOT_COLUMN_PAIR", "the selected category and value columns are not present in the rebuilt Pivot input", nil)
	}
	key, err := constructionProbeCategoryKey(category.Type)
	if err != nil {
		return Base{}, "", unprocessable("construction-category-discovery", "INVALID_CATEGORY_COLUMN_ID", "the selected category column has no supported scalar type", err)
	}
	probeID := "pivot_probe_" + strings.TrimPrefix(constructionPivotSourceHelperID(request.PivotStepID, "category-discovery"), "pivot_source_")
	pivotPayload := pivotStep.Operation.Pivot

	pivotPayload.Discovery = nil
	pivotPayload.Categories = []ws.ConstructionPivotCategory{{Key: key, OutputColumnID: probeID}}
	pivotPayload.DuplicatePolicy = ws.ConstructionPivotDuplicateError
	pivotPayload.MissingCellPolicy = ws.ConstructionPivotMissingNull
	pivotPayload.UnlistedCategoryPolicy = ws.ConstructionPivotUnlistedError
	pivotStep.Outputs = make([]ws.StageColumn, 0, len(pivotPayload.GroupKeyIDs)+1)
	publicNames := make(map[string]bool, len(inputColumns))
	if sourceStage, found := constructionReceiptStage(base.Stages, request.StageID); found {
		for _, column := range sourceStage.Columns {
			if !strings.HasPrefix(column.Name, "__construction_source_") && !strings.HasPrefix(column.Name, "__pivot_source_") {
				publicNames[strings.ToLower(column.Name)] = true
			}
		}
	}
	pivotSourcePaths := constructionPivotSourcePaths(candidate, request.PivotStepID)
	for _, id := range pivotPayload.GroupKeyIDs {
		column, exists := inputByID[id]
		if !exists {
			return Base{}, "", unprocessable("construction-category-discovery", "INVALID_GROUP_KEY_COLUMN_ID", "a Pivot row key is not present in the rebuilt input", nil)
		}
		if path := pivotSourcePaths[id]; path != "" {
			column.Name = uniqueConstructionPivotOutputName(path, publicNames)
		}
		publicNames[strings.ToLower(column.Name)] = true
		pivotStep.Outputs = append(pivotStep.Outputs, column)
	}
	pivotStep.Outputs = append(pivotStep.Outputs, ws.StageColumn{
		ID: probeID, Name: "__category_discovery_probe", Label: "Category discovery probe", Type: value.Type,
	})

	document, err := ws.StartDocumentConstruction(base.Document)
	if err != nil {
		return Base{}, "", unprocessable("construction-category-discovery", "INVALID_CONSTRUCTION_CANDIDATE", "the output cannot be upgraded for temporary category discovery", err)
	}
	document.Construction = &candidate
	document.ArchiveRetiredShape = nil
	workspace := base.Workspace
	workspace.Documents = append([]ws.Document(nil), base.Workspace.Documents...)
	documentIndex := workspace.DocumentIndex(request.OutputID)
	if documentIndex < 0 {
		return Base{}, "", conflict("construction-category-discovery", "OUTPUT_NOT_FOUND", "the output is missing from the candidate workspace", nil, nil)
	}
	workspace.Documents[documentIndex] = document
	if _, err := workspace.Digest(); err != nil {
		return Base{}, "", failureDetails(ports.ClassUnprocessable, "INVALID_CONSTRUCTION_CANDIDATE",
			"the temporary category input construction is invalid", nil, err)
	}
	receipt, err := s.compile(ctx, authoringcore.CompileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace,
		SnapshotToken: request.SnapshotToken, RequestID: "construction-category-discovery-candidate",
	})
	if err != nil {
		return Base{}, "", err
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-category-discovery", receipt, request.Project, request.ExplorerID, request.SnapshotToken, base.Snapshot, &workspace); err != nil {
		return Base{}, "", err
	}
	stages := receipt.ConstructionStages[request.OutputID]
	if _, found := constructionReceiptStage(stages, inputStageID); !found {
		return Base{}, "", conflict("construction-category-discovery", "INVALID_COMPILATION_RECEIPT", "the rebuilt category input stage is missing from the compiler receipt", nil, nil)
	}
	base.Receipt, base.Stages = receipt, stages
	return base, inputStageID, nil
}

func (s constructionScanFlow) discoverConstructionCategories(ctx context.Context, request ConstructionCategoryDiscoveryRequest) (ConstructionCategoryDiscoveryResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionCategoryDiscoveryResponse{}, malformed("construction-category-discovery", err.Error(), err)
	}
	base, err := s.loadBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionCategoryDiscoveryResponse{}, err
	}
	if base.Receipt == nil {
		return ConstructionCategoryDiscoveryResponse{}, conflict("construction-category-discovery", "STAGE_SCAN_UNAVAILABLE", "the exact compiled output is not available for category discovery", nil, nil)
	}
	if s.scanCategories == nil {
		return ConstructionCategoryDiscoveryResponse{}, unavailable("construction-category-discovery", "CATEGORY_SCAN_UNAVAILABLE", "compiler-owned category discovery is not configured", nil)
	}
	responseStageID := request.StageID
	if len(request.PivotSources) != 0 || request.compileCandidate {
		var scanStageID string
		base, scanStageID, err = s.compileConstructionCategoryCandidate(ctx, base, request)
		if err != nil {
			return ConstructionCategoryDiscoveryResponse{}, err
		}
		request.StageID = scanStageID
	}
	var stage *receiptmodel.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == request.StageID {
			stage = &base.Stages[index]
			break
		}
	}
	if stage == nil {
		return ConstructionCategoryDiscoveryResponse{}, conflict("construction-category-discovery", "STALE_STAGE_REFERENCE", "the requested stage is not present in the current compiled output", nil, nil)
	}
	pivotSupported := false
	for _, capability := range stage.Capabilities {
		if capability.Kind == string(recipe.ConstructionPivotOp) {
			pivotSupported = capability.Supported
			break
		}
	}
	if !pivotSupported {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "PIVOT_UNSUPPORTED", "the selected stage does not support pivot", nil)
	}
	category, ok := constructionCategoryColumn(stage.Columns, request.CategoryColumnID)
	if !ok {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "INVALID_CATEGORY_COLUMN_ID", "the category column is not a public scalar in the selected stage", nil)
	}
	value, ok := constructionCategoryColumn(stage.Columns, request.ValueColumnID)
	if !ok {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "INVALID_VALUE_COLUMN_ID", "the value column is not a public scalar in the selected stage", nil)
	}
	if category.ID == value.ID {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "INVALID_PIVOT_COLUMN_PAIR", "category and value columns must differ", nil)
	}
	if category.Cardinality != expression.RequiredOne && category.Cardinality != expression.OptionalOne {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "INVALID_CATEGORY_COLUMN_ID", "the category column must be scalar at the selected stage", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(base.Catalog.Project), SelectionProject: base.Catalog.Project,
		DatasetGeneration: base.Snapshot.Identity.Generation, SelectionMembersCollection: s.selectionMembersCollection,
		OutputNames: []string{request.OutputID},
	}
	applyAuthorizedScope(&bindings, base.Authorized, false)
	scan, err := s.scanCategories(ctx, base.Receipt, bindings, dataframeexecution.CategoryScanRequest{
		Output: request.OutputID, StageID: request.StageID, ColumnID: request.CategoryColumnID,
		ValueColumnID: request.ValueColumnID, MaxValues: facade.MaxCategoryScanValues, RequireValue: true,
	})
	if err != nil {
		return ConstructionCategoryDiscoveryResponse{}, classifyConstructionCategoryScanError(ctx, err)
	}
	proof := scan.Proof
	overflow := scan.Overflow || len(scan.Values) > facade.MaxCategoryScanValues
	if (!overflow && !scan.ConclusiveMissing && !scan.Complete) ||
		proof.Version != 2 || proof.Output != request.OutputID || proof.StageID != request.StageID ||
		proof.ColumnID != request.CategoryColumnID || proof.ValueColumnID != request.ValueColumnID ||
		proof.Column != category.Name || proof.MaxValues != facade.MaxCategoryScanValues ||
		proof.Kind != category.Type || proof.Cardinality != string(category.Cardinality) || !proof.ValuePresenceRequired ||
		strings.TrimSpace(proof.OutputSchemaDigest) == "" || strings.TrimSpace(proof.PlanFingerprint) == "" ||
		strings.TrimSpace(proof.QueryFingerprint) == "" || strings.TrimSpace(proof.Fingerprint) == "" ||
		(scan.ConclusiveMissing && (strings.TrimSpace(proof.OverflowWitnessFingerprint) == "" || proof.PresenceTracked)) {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "CATEGORY_SCAN_INCOMPLETE", "pivot categories require a complete stage-bound scan within the supported limit", nil)
	}
	if overflow {
		return ConstructionCategoryDiscoveryResponse{
			SnapshotToken: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
			OutputID: request.OutputID, StageID: responseStageID, CategoryColumnID: request.CategoryColumnID,
			ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryLimitExceeded,
			Categories: []ConstructionDiscoveredCategory{}, Limit: facade.MaxCategoryScanValues,
			Message: "This field has more than 256 category values in the current rows. Choose another category field or filter rows before pivoting.",
		}, nil
	}
	missing := scan.ConclusiveMissing
	for _, item := range scan.Values {
		missing = missing || !item.Present
	}
	if missing && !proof.PresenceTracked {
		return ConstructionCategoryDiscoveryResponse{
			SnapshotToken: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
			OutputID: request.OutputID, StageID: responseStageID, CategoryColumnID: request.CategoryColumnID,
			ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryMissingUnsupported,
			Categories: []ConstructionDiscoveredCategory{},
			Message:    "Some records have no category field. Loom cannot currently distinguish an absent field from a field present with no value in this Pivot. Filter rows where the category field is missing, or choose a field populated in every row.",
		}, nil
	}
	categories := make([]ConstructionDiscoveredCategory, 0, len(scan.Values))
	for _, item := range scan.Values {
		key, err := constructionCategoryScalar(item, category.Type)
		if err != nil {
			return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "CATEGORY_SCAN_INVALID", "the compiler returned a category value outside the selected column type", err)
		}
		categories = append(categories, ConstructionDiscoveredCategory{Key: key, Label: constructionCategoryLabel(key)})
	}
	if len(categories) == 0 {
		return ConstructionCategoryDiscoveryResponse{
			SnapshotToken: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
			OutputID: request.OutputID, StageID: responseStageID, CategoryColumnID: request.CategoryColumnID,
			ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryNoValues,
			Categories: []ConstructionDiscoveredCategory{},
			Message:    "No selected value is populated for any category in the current rows. Choose another value column or filter rows before pivoting.",
		}, nil
	}
	return ConstructionCategoryDiscoveryResponse{
		SnapshotToken: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
		OutputID: request.OutputID, StageID: responseStageID, CategoryColumnID: request.CategoryColumnID,
		ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryComplete,
		Complete: true, ProofFingerprint: proof.Fingerprint, Categories: categories,
	}, nil
}

func pruneConstructionCategorySourceProjections(
	projections []ws.ConstructionSourceProjection,
	steps []ws.ConstructionStep,
) []ws.ConstructionSourceProjection {
	owners := make(map[string]bool, len(steps))
	for _, step := range steps {
		switch step.Operation.Kind {
		case ws.ConstructionOperationPivot,
			ws.ConstructionOperationGroup,
			ws.ConstructionOperationFilter,
			ws.ConstructionOperationDerive,
			ws.ConstructionOperationUnpivot,
			ws.ConstructionOperationExpand:
			owners[step.ID] = true
		}
	}
	pruned := projections[:0]
	for _, projection := range projections {
		if projection.OwnerStepID == "" || owners[projection.OwnerStepID] {
			pruned = append(pruned, projection)
		}
	}
	return pruned
}

func constructionPivotSourcePaths(construction ws.Construction, pivotID string) map[string]string {
	paths := make(map[string]string)
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == pivotID {
			paths[projection.ColumnID] = projection.FieldPath
		}
	}
	for _, step := range construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == ws.ConstructionOperationRelatedField && step.Operation.RelatedField != nil {
			paths[step.Operation.RelatedField.OutputColumnID] = step.Operation.RelatedField.Source.Path
		}
	}
	return paths
}

func uniqueConstructionPivotOutputName(fieldPath string, used map[string]bool) string {
	var builder strings.Builder
	separator := false
	for _, character := range strings.ToLower(fieldPath) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			if separator && builder.Len() > 0 {
				builder.WriteByte('_')
			}
			builder.WriteRune(character)
			separator = false
		} else {
			separator = true
		}
	}
	name := strings.Trim(builder.String(), "_")
	if name == "" {
		name = "column"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "column_" + name
	}
	base := name
	for suffix := 2; used[strings.ToLower(name)]; suffix++ {
		name = fmt.Sprintf("%s_%d", base, suffix)
	}
	return name
}

func constructionProbeCategoryKey(typeName string) (ws.TableScalar, error) {
	switch expression.ValueKind(typeName) {
	case expression.KindBoolean:
		value := false
		return ws.TableScalar{Kind: ws.TableScalarBoolean, Boolean: &value}, nil
	case expression.KindInteger:
		value := int64(0)
		return ws.TableScalar{Kind: ws.TableScalarInteger, Integer: &value}, nil
	case expression.KindDecimal:
		value := float64(0)
		return ws.TableScalar{Kind: ws.TableScalarDecimal, Decimal: &value}, nil
	case expression.KindString, expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		value := "__category_discovery_probe__"
		return ws.TableScalar{Kind: ws.TableScalarString, String: &value}, nil
	default:
		return ws.TableScalar{}, fmt.Errorf("unsupported scalar type %q", typeName)
	}
}

func classifyConstructionCategoryScanError(ctx context.Context, err error) error {
	const stage = "construction-category-discovery"
	if err == nil {
		return nil
	}
	contextErr := ctx.Err()
	cause := err
	if contextErr != nil && !errors.Is(err, contextErr) {
		cause = errors.Join(err, contextErr)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(contextErr, context.DeadlineExceeded) {
		return unavailable(stage, "CATEGORY_SCAN_TIMEOUT", "finding all category values exceeded the preview time limit; filter the source rows and try again", cause)
	}
	if errors.Is(err, context.Canceled) || errors.Is(contextErr, context.Canceled) {
		return cause
	}
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) {
		return err
	}
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) {
		return err
	}
	if errors.Is(err, receiptmodel.ErrReceiptRecompileRequired) {
		return conflict(stage, "RECEIPT_RECOMPILE_REQUIRED", "the compiled receipt no longer matches the current construction; refresh and try again", nil, cause)
	}
	if refusal, ok := facade.CategoryScanRefusalCodeOf(err); ok {
		return unprocessable(stage, string(refusal), "complete compiler-owned pivot categories are unavailable for this stage and pair", cause)
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		code := userErr.Code()
		message := dataframeerrors.PublicMessage(err)
		details := userErr.Details()
		if dataframeerrors.IsFeatureResolutionCode(code) {
			return failureDetails(ports.ClassUnprocessable, code, message, details, cause)
		}
		switch dataframeerrors.ErrorCode(code) {
		case dataframeerrors.CodeSchemaConflict,
			dataframeerrors.CodeStaleCursor,
			dataframeerrors.CodeDatasetGenerationChanged,
			dataframeerrors.CodePublicationInProgress,
			dataframeerrors.CodePublicationConflict,
			dataframeerrors.CodeGenerationActivationUnknown,
			dataframeerrors.CodeRecipeContractViolation,
			dataframeerrors.CodeDynamicSchemaDrift:
			return failureDetails(ports.ClassConflict, code, message, details, cause)
		case dataframeerrors.CodeForbidden, dataframeerrors.CodeUnauthorizedProject:
			return failureDetails(ports.ClassForbidden, code, message, details, cause)
		case dataframeerrors.CodeDatasetNotFound,
			dataframeerrors.CodeNoActiveGeneration,
			dataframeerrors.CodeRecipeNotFound,
			dataframeerrors.CodeRecipeExecutionNotFound:
			return failureDetails(ports.ClassNotFound, code, message, details, cause)
		case dataframeerrors.CodeBackendUnavailable,
			dataframeerrors.CodeReceiptStoreUnavailable,
			dataframeerrors.CodeQueryMemoryLimitExceeded,
			dataframeerrors.CodeQueryResourceLimitExceeded,
			dataframeerrors.CodeQueryBackendOutOfMemory:
			return failureDetails(ports.ClassUnavailable, code, message, details, cause)
		case dataframeerrors.CodeClientCanceled,
			dataframeerrors.CodePreviewTimeout,
			dataframeerrors.CodePreviewResponseTooLarge,
			dataframeerrors.CodePlanTooExpensive,
			dataframeerrors.CodeUnauthenticated:

			return err
		}
		if dataframeerrors.IsRetryableCode(dataframeerrors.ErrorCode(code)) {
			return failureDetails(ports.ClassUnavailable, code, message, details, cause)
		}

		return err
	}
	return internal(stage, "CATEGORY_DISCOVERY_FAILED", "category discovery failed unexpectedly while reading the catalog; inspect the server log for the request ID", cause)
}

func constructionCategoryColumn(columns []receiptmodel.ReceiptConstructionStageColumn, id string) (receiptmodel.ReceiptConstructionStageColumn, bool) {
	for _, column := range columns {
		if column.ID != id || column.ID == "" || column.Name == "" {
			continue
		}
		if column.Cardinality != expression.RequiredOne && column.Cardinality != expression.OptionalOne {
			return receiptmodel.ReceiptConstructionStageColumn{}, false
		}
		switch expression.ValueKind(column.Type) {
		case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
			expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
			return column, true
		default:
			return receiptmodel.ReceiptConstructionStageColumn{}, false
		}
	}
	return receiptmodel.ReceiptConstructionStageColumn{}, false
}

func constructionCategoryScalar(value dataframeexecution.CategoryValue, kind string) (ws.TableScalar, error) {
	if !value.Present {
		return ws.TableScalar{Kind: ws.TableScalarMissing}, nil
	}
	if value.Value == nil {
		return ws.TableScalar{Kind: ws.TableScalarNull}, nil
	}
	switch expression.ValueKind(kind) {
	case expression.KindString, expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		v, ok := value.Value.(string)
		if !ok {
			return ws.TableScalar{}, fmt.Errorf("expected string, got %T", value.Value)
		}
		return ws.TableScalar{Kind: ws.TableScalarString, String: &v}, nil
	case expression.KindInteger:
		var v int64
		switch raw := value.Value.(type) {
		case int:
			v = int64(raw)
		case int32:
			v = int64(raw)
		case int64:
			v = raw
		case json.Number:
			parsed, err := raw.Int64()
			if err != nil {
				return ws.TableScalar{}, err
			}
			v = parsed
		default:
			return ws.TableScalar{}, fmt.Errorf("expected integer, got %T", value.Value)
		}
		return ws.TableScalar{Kind: ws.TableScalarInteger, Integer: &v}, nil
	case expression.KindDecimal:
		var v float64
		switch raw := value.Value.(type) {
		case int:
			v = float64(raw)
		case int32:
			v = float64(raw)
		case int64:
			v = float64(raw)
		case float32:
			v = float64(raw)
		case float64:
			v = raw
		case json.Number:
			parsed, err := strconv.ParseFloat(raw.String(), 64)
			if err != nil {
				return ws.TableScalar{}, err
			}
			v = parsed
		default:
			return ws.TableScalar{}, fmt.Errorf("expected decimal, got %T", value.Value)
		}
		return ws.TableScalar{Kind: ws.TableScalarDecimal, Decimal: &v}, nil
	case expression.KindBoolean:
		v, ok := value.Value.(bool)
		if !ok {
			return ws.TableScalar{}, fmt.Errorf("expected boolean, got %T", value.Value)
		}
		return ws.TableScalar{Kind: ws.TableScalarBoolean, Boolean: &v}, nil
	default:
		return ws.TableScalar{}, fmt.Errorf("unsupported category type %q", kind)
	}
}

func constructionCategoryLabel(value ws.TableScalar) string {
	switch value.Kind {
	case ws.TableScalarMissing:
		return "Missing"
	case ws.TableScalarNull:
		return "Null"
	case ws.TableScalarString:
		return *value.String
	case ws.TableScalarInteger:
		return strconv.FormatInt(*value.Integer, 10)
	case ws.TableScalarDecimal:
		return strconv.FormatFloat(*value.Decimal, 'f', -1, 64)
	case ws.TableScalarBoolean:
		return strconv.FormatBool(*value.Boolean)
	default:
		return strings.TrimSpace(string(value.Kind))
	}
}

func (s *Service) verifyConstructionProposalPivotCategoriesExcept(
	ctx context.Context,
	request ConstructionProposalRequest,
	candidate ws.Construction,
	base *ws.Construction,
	skip map[string]bool,
) ([]ConstructionProposalDiagnostic, error) {
	if s.config.ScanCategories == nil {
		return nil, nil
	}
	var diagnostics []ConstructionProposalDiagnostic
	baseByID := make(map[string]ws.ConstructionStep, len(candidate.Steps))
	if base != nil {
		for _, step := range base.Steps {
			baseByID[step.ID] = step
		}
	}
	removed := make(map[string]struct{}, len(request.RemoveStepIDs))
	for _, stepID := range request.RemoveStepIDs {
		removed[stepID] = struct{}{}
	}
	for _, step := range candidate.Steps {
		if _, gone := removed[step.ID]; gone {
			continue
		}
		if skip[step.ID] {
			continue
		}
		pivot := step.Operation.Pivot
		if step.Operation.Kind != ws.ConstructionOperationPivot || pivot == nil || len(pivot.Categories) == 0 {
			continue
		}
		changed := step.ID == request.ChangedStepID
		if changed && base != nil && constructionPivotLabelOnlyEdit(*base, candidate, step.ID) {
			changed = false
		}
		if !changed && base != nil {
			prior, exists := baseByID[step.ID]
			changed = !exists || !reflect.DeepEqual(prior.Inputs, step.Inputs) || !reflect.DeepEqual(prior.Operation, step.Operation)
		}
		discoveryRequest := ConstructionCategoryDiscoveryRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
			ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
			OutputID: request.OutputID, StageID: constructionStepInputStageID(step),
			CategoryColumnID: pivot.CategoryColumnID, ValueColumnID: pivot.ValueColumnID,
			GroupKeyIDs:           append([]string(nil), pivot.GroupKeyIDs...),
			PivotStepID:           step.ID,
			CandidateConstruction: &candidate,
		}
		if changed {
			discoveryRequest.PivotSources = append([]ConstructionPivotSourceSelection(nil), request.PivotSources...)
		} else {

			discoveryRequest.compileCandidate = true
		}
		discovery, err := s.DiscoverConstructionCategories(ctx, discoveryRequest)
		if err != nil {
			if changed {
				return nil, err
			}
			diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "PROBE_ERROR",
				fmt.Sprintf("Pivot %s categories were not verified because the category probe failed: %v", step.ID, err)))
			continue
		}

		switch discovery.Outcome {
		case constructionCategoryDiscoveryComplete:
			if len(discovery.Categories) == 0 {
				if !changed {
					diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "NO_OBSERVED_CATEGORIES",
						fmt.Sprintf("Pivot %s categories were not verified because the current rows have no observed values that can produce cells for the selected category and value columns", step.ID)))
					continue
				}
				return nil, unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
					fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
			}
		case constructionCategoryDiscoveryNoValues:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "NO_OBSERVED_CATEGORIES",
					fmt.Sprintf("Pivot %s categories were not verified because the current rows have no observed values that can produce cells for the selected category and value columns", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
				fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
		case constructionCategoryDiscoveryLimitExceeded:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "LIMIT_EXCEEDED",
					fmt.Sprintf("Pivot %s categories were not verified because this category field has more than 256 values in the current rows.", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories cannot be verified because this category field has more than 256 values in the current rows. Choose another category field, filter rows before pivoting, or reduce the number of categories, then retry.", nil)
		case constructionCategoryDiscoveryMissingUnsupported:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "MISSING_UNSUPPORTED",
					fmt.Sprintf("Pivot %s categories were not verified because some rows have no category field and this scan cannot distinguish an absent field from a null value.", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories cannot be verified because some rows have no category field and this scan cannot distinguish an absent field from a null value. Filter rows where the category field is missing, or choose a field populated in every row, then retry.", nil)
		default:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "UNVERIFIABLE",
					fmt.Sprintf("Pivot %s categories were not verified against a complete value-bearing scan.", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories could not be verified against a complete value-bearing scan. Refresh the preview and retry; if the problem persists, choose another category field or filter rows before pivoting.", nil)
		}
		observed := make(map[string]struct{}, len(discovery.Categories))
		for _, category := range discovery.Categories {
			observed[constructionProposalCategoryKey(category.Key)] = struct{}{}
		}
		for _, category := range pivot.Categories {
			if _, exists := observed[constructionProposalCategoryKey(category.Key)]; exists {
				continue
			}
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "CATEGORY_HAS_NO_VALUES",
					fmt.Sprintf("Pivot %s categories were not verified because category %s has no observed values for the selected category and value columns", step.ID, constructionCategoryLabel(category.Key))))
				break
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORY_HAS_NO_VALUES",
				fmt.Sprintf("Pivot %s category %s has no observed values for the selected category and value columns", step.ID, constructionCategoryLabel(category.Key)), nil)
		}
	}
	return diagnostics, nil
}

func (s *Service) constructionScans() constructionScanFlow {
	return constructionScanFlow{
		loadBase: s.loadConstructionBase, compile: s.compile, verifyProposalReceipt: s.verifyProposalReceipt,
		withPivotSources: s.constructionSources().constructionCandidateWithPivotSources, scanCategories: s.config.ScanCategories,
		scanPopulation: s.config.ScanColumnPopulation, selectionMembersCollection: s.config.SelectionMembersCollection,
		populationCache: s.columnPopulationCache,
	}
}

func (s *Service) DiscoverConstructionCategories(ctx context.Context, request ConstructionCategoryDiscoveryRequest) (ConstructionCategoryDiscoveryResponse, error) {
	return s.constructionScans().discoverConstructionCategories(ctx, request)
}
