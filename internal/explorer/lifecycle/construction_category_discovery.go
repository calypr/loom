package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
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
	CandidateConstruction *authoringv2.Construction          `json:"candidateConstruction,omitempty"`
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
	Key   authoringv2.TableScalar `json:"key"`
	Label string                  `json:"label"`
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
)

func (s *Service) compileConstructionCategoryCandidate(
	ctx context.Context,
	base constructionBase,
	request ConstructionCategoryDiscoveryRequest,
) (constructionBase, string, error) {
	if request.CandidateConstruction == nil {
		return constructionBase{}, "", malformed("construction-category-discovery", "candidateConstruction is required when pivotSources are supplied", nil)
	}
	raw, err := json.Marshal(request.CandidateConstruction)
	if err != nil {
		return constructionBase{}, "", malformed("construction-category-discovery", "candidateConstruction is invalid", err)
	}
	var candidate authoringv2.Construction
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return constructionBase{}, "", malformed("construction-category-discovery", "candidateConstruction is invalid", err)
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
			return constructionBase{}, "", unprocessable("construction-category-discovery", "PIVOT_ROW_KEY_REQUIRED", "choose at least one row key before discovering Pivot categories", nil)
		}
		input := authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
		if request.StageID != recipe.ConstructionSourceProjectionID {
			inputIndex := constructionStepIndex(candidate.Steps, request.StageID)
			if inputIndex < 0 {
				return constructionBase{}, "", conflict("construction-category-discovery", "STALE_STAGE_REFERENCE", "the requested input stage is not present in candidateConstruction", nil, nil)
			}
			candidate.Steps = candidate.Steps[:inputIndex+1]
			input = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: request.StageID}
		}
		candidate.Steps = append(candidate.Steps, authoringv2.ConstructionStep{
			ID: request.PivotStepID, Inputs: []authoringv2.ConstructionInputRef{input},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
				ConstructionID: request.PivotStepID, GroupKeyIDs: groupKeys,
				CategoryColumnID: request.CategoryColumnID, ValueColumnID: request.ValueColumnID,
				DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
				MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
			}},
		})
		pivotIndex = len(candidate.Steps) - 1
	}
	if candidate.Steps[pivotIndex].Operation.Kind != authoringv2.ConstructionOperationPivot || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return constructionBase{}, "", unprocessable("construction-category-discovery", "PIVOT_SOURCE_REQUIRES_PIVOT", "pivotStepId must identify the candidate Pivot", nil)
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
	candidate, _, err = s.constructionCandidateWithPivotSources(ctx, base, proposalRequest, candidate)
	if err != nil {
		return constructionBase{}, "", err
	}
	pivotIndex = constructionStepIndex(candidate.Steps, request.PivotStepID)
	if pivotIndex < 0 || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return constructionBase{}, "", unprocessable("construction-category-discovery", "PIVOT_SOURCE_REQUIRES_PIVOT", "the candidate Pivot was not preserved", nil)
	}
	// Keep the Pivot as the owner of its hidden input columns, but stop its
	// downstream chain and replace its categories with one compiler-valid probe.
	candidate.Steps = append([]authoringv2.ConstructionStep(nil), candidate.Steps[:pivotIndex+1]...)
	owners := make(map[string]bool, len(candidate.Steps))
	for _, step := range candidate.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationPivot {
			owners[step.ID] = true
		}
	}
	projections := candidate.SourceProjections[:0]
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == "" || owners[projection.OwnerStepID] {
			projections = append(projections, projection)
		}
	}
	candidate.SourceProjections = projections
	pivotStep := &candidate.Steps[pivotIndex]
	inputStageID := constructionStepInputStageID(*pivotStep)
	inputColumns, err := constructionAuthoringStageColumns(base, candidate.Steps, inputStageID, candidate.SourceProjections)
	if err != nil {
		return constructionBase{}, "", unprocessable("construction-category-discovery", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
	}
	inputByID := make(map[string]authoringv2.StageColumn, len(inputColumns))
	for _, column := range inputColumns {
		inputByID[column.ID] = column
	}
	category, categoryOK := inputByID[pivotStep.Operation.Pivot.CategoryColumnID]
	value, valueOK := inputByID[pivotStep.Operation.Pivot.ValueColumnID]
	if !categoryOK || !valueOK {
		return constructionBase{}, "", unprocessable("construction-category-discovery", "INVALID_PIVOT_COLUMN_PAIR", "the selected category and value columns are not present in the rebuilt Pivot input", nil)
	}
	key, err := constructionProbeCategoryKey(category.Type)
	if err != nil {
		return constructionBase{}, "", unprocessable("construction-category-discovery", "INVALID_CATEGORY_COLUMN_ID", "the selected category column has no supported scalar type", err)
	}
	probeID := "pivot_probe_" + strings.TrimPrefix(constructionPivotSourceHelperID(request.PivotStepID, "category-discovery"), "pivot_source_")
	pivotPayload := pivotStep.Operation.Pivot
	pivotPayload.Categories = []authoringv2.ConstructionPivotCategory{{Key: key, OutputColumnID: probeID}}
	pivotPayload.DuplicatePolicy = authoringv2.ConstructionPivotDuplicateError
	pivotPayload.MissingCellPolicy = authoringv2.ConstructionPivotMissingNull
	pivotPayload.UnlistedCategoryPolicy = authoringv2.ConstructionPivotUnlistedError
	pivotStep.Outputs = make([]authoringv2.StageColumn, 0, len(pivotPayload.GroupKeyIDs)+1)
	publicNames := make(map[string]bool, len(inputColumns))
	if sourceStage, found := constructionReceiptStage(base.stages, request.StageID); found {
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
			return constructionBase{}, "", unprocessable("construction-category-discovery", "INVALID_GROUP_KEY_COLUMN_ID", "a Pivot row key is not present in the rebuilt input", nil)
		}
		if path := pivotSourcePaths[id]; path != "" {
			column.Name = uniqueConstructionPivotOutputName(path, publicNames)
		}
		publicNames[strings.ToLower(column.Name)] = true
		pivotStep.Outputs = append(pivotStep.Outputs, column)
	}
	pivotStep.Outputs = append(pivotStep.Outputs, authoringv2.StageColumn{
		ID: probeID, Name: "__category_discovery_probe", Label: "Category discovery probe", Type: value.Type,
	})

	// Category discovery compiles a temporary staged candidate just like a
	// proposal. Materialize legacy public columns first so they have the stable
	// ColumnIDs required by a staged construction.
	document, err := authoringv2.UpgradeDocumentToConstruction(base.document)
	if err != nil {
		return constructionBase{}, "", unprocessable("construction-category-discovery", "INVALID_CONSTRUCTION_CANDIDATE", "the output cannot be upgraded for temporary category discovery", err)
	}
	document.Construction = &candidate
	document.TableShape = nil
	workspace := base.workspace
	workspace.Documents = append([]authoringv2.Document(nil), base.workspace.Documents...)
	documentIndex := constructionDocumentIndex(workspace, request.OutputID)
	if documentIndex < 0 {
		return constructionBase{}, "", conflict("construction-category-discovery", "OUTPUT_NOT_FOUND", "the output is missing from the candidate workspace", nil, nil)
	}
	workspace.Documents[documentIndex] = document
	if _, err := workspace.Digest(); err != nil {
		return constructionBase{}, "", failureDetails(ClassUnprocessable, "construction-category-discovery", "INVALID_CONSTRUCTION_CANDIDATE",
			"the temporary category input construction is invalid", nil, err)
	}
	receipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace,
		SnapshotToken: request.SnapshotToken, RequestID: "construction-category-discovery-candidate",
	})
	if err != nil {
		return constructionBase{}, "", err
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-category-discovery", receipt, request.Project, request.ExplorerID, request.SnapshotToken, base.snapshot, &workspace); err != nil {
		return constructionBase{}, "", err
	}
	stages := receipt.ConstructionStages[request.OutputID]
	if _, found := constructionReceiptStage(stages, inputStageID); !found {
		return constructionBase{}, "", conflict("construction-category-discovery", "INVALID_COMPILATION_RECEIPT", "the rebuilt category input stage is missing from the compiler receipt", nil, nil)
	}
	base.receipt, base.stages = receipt, stages
	return base, inputStageID, nil
}

func constructionPivotSourcePaths(construction authoringv2.Construction, pivotID string) map[string]string {
	paths := make(map[string]string)
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == pivotID {
			paths[projection.ColumnID] = projection.FieldPath
		}
	}
	for _, step := range construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField && step.Operation.RelatedField != nil {
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

func constructionProbeCategoryKey(typeName string) (authoringv2.TableScalar, error) {
	switch expression.ValueKind(typeName) {
	case expression.KindBoolean:
		value := false
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarBoolean, Boolean: &value}, nil
	case expression.KindInteger:
		value := int64(0)
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarInteger, Integer: &value}, nil
	case expression.KindDecimal:
		value := float64(0)
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarDecimal, Decimal: &value}, nil
	case expression.KindString, expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		value := "__category_discovery_probe__"
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &value}, nil
	default:
		return authoringv2.TableScalar{}, fmt.Errorf("unsupported scalar type %q", typeName)
	}
}

func (s *Service) DiscoverConstructionCategories(ctx context.Context, request ConstructionCategoryDiscoveryRequest) (ConstructionCategoryDiscoveryResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionCategoryDiscoveryResponse{}, malformed("construction-category-discovery", err.Error(), err)
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionCategoryDiscoveryResponse{}, err
	}
	if base.receipt == nil {
		return ConstructionCategoryDiscoveryResponse{}, conflict("construction-category-discovery", "STAGE_SCAN_UNAVAILABLE", "the exact compiled output is not available for category discovery", nil, nil)
	}
	if s.config.ScanCategories == nil {
		return ConstructionCategoryDiscoveryResponse{}, unavailable("construction-category-discovery", "CATEGORY_SCAN_UNAVAILABLE", "compiler-owned category discovery is not configured", nil)
	}
	responseStageID := request.StageID
	if len(request.PivotSources) != 0 {
		var scanStageID string
		base, scanStageID, err = s.compileConstructionCategoryCandidate(ctx, base, request)
		if err != nil {
			return ConstructionCategoryDiscoveryResponse{}, err
		}
		request.StageID = scanStageID
	}
	var stage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == request.StageID {
			stage = &base.stages[index]
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
		Project: projectid.Legacy(base.catalog.Project), SelectionProject: base.catalog.Project,
		DatasetGeneration: base.snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		OutputNames: []string{request.OutputID},
	}
	applyAuthorizedScope(&bindings, base.authorized, false)
	scan, err := s.config.ScanCategories(ctx, base.receipt, bindings, dataframeexecution.CategoryScanRequest{
		Output: request.OutputID, StageID: request.StageID, ColumnID: request.CategoryColumnID,
		ValueColumnID: request.ValueColumnID, MaxValues: compiler.MaxCategoryScanValues,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ConstructionCategoryDiscoveryResponse{}, unavailable("construction-category-discovery", "CATEGORY_SCAN_TIMEOUT", "finding all category values exceeded the preview time limit; filter the source rows and try again", err)
		}
		code := "CATEGORY_SCAN_REFUSED"
		if refusal, ok := compiler.CategoryScanRefusalCodeOf(err); ok {
			code = string(refusal)
		}
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", code, "complete compiler-owned pivot categories are unavailable for this stage and pair", err)
	}
	proof := scan.Proof
	overflow := scan.Overflow || len(scan.Values) > compiler.MaxCategoryScanValues
	if (!overflow && !scan.ConclusiveMissing && !scan.Complete) ||
		proof.Version != 2 || proof.Output != request.OutputID || proof.StageID != request.StageID ||
		proof.ColumnID != request.CategoryColumnID || proof.ValueColumnID != request.ValueColumnID ||
		proof.Column != category.Name || proof.MaxValues != compiler.MaxCategoryScanValues ||
		proof.Kind != category.Type || proof.Cardinality != string(category.Cardinality) ||
		strings.TrimSpace(proof.OutputSchemaDigest) == "" || strings.TrimSpace(proof.PlanFingerprint) == "" ||
		strings.TrimSpace(proof.QueryFingerprint) == "" || strings.TrimSpace(proof.Fingerprint) == "" ||
		(scan.ConclusiveMissing && strings.TrimSpace(proof.OverflowWitnessFingerprint) == "") {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "CATEGORY_SCAN_INCOMPLETE", "pivot categories require a complete stage-bound scan within the supported limit", nil)
	}
	if overflow {
		return ConstructionCategoryDiscoveryResponse{
			SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
			OutputID: request.OutputID, StageID: responseStageID, CategoryColumnID: request.CategoryColumnID,
			ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryLimitExceeded,
			Categories: []ConstructionDiscoveredCategory{}, Limit: compiler.MaxCategoryScanValues,
			Message: "This field has more than 256 category values in the current rows. Choose another category field or filter rows before pivoting.",
		}, nil
	}
	// Construction pivot stages project their inputs through materialized stage
	// rows. Those projections currently preserve values but not source property
	// presence, so returning MISSING as a selectable category would produce a
	// candidate that the compiler cannot execute without conflating it with NULL.
	missing := scan.ConclusiveMissing
	for _, item := range scan.Values {
		missing = missing || !item.Present
	}
	if missing {
		return ConstructionCategoryDiscoveryResponse{
			SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
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
	return ConstructionCategoryDiscoveryResponse{
		SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
		OutputID: request.OutputID, StageID: responseStageID, CategoryColumnID: request.CategoryColumnID,
		ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryComplete,
		Complete: true, ProofFingerprint: proof.Fingerprint, Categories: categories,
	}, nil
}

func constructionCategoryColumn(columns []explorer.ReceiptConstructionStageColumn, id string) (explorer.ReceiptConstructionStageColumn, bool) {
	for _, column := range columns {
		if column.ID != id || column.ID == "" || column.Name == "" {
			continue
		}
		if column.Cardinality != expression.RequiredOne && column.Cardinality != expression.OptionalOne {
			return explorer.ReceiptConstructionStageColumn{}, false
		}
		switch expression.ValueKind(column.Type) {
		case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
			expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
			return column, true
		default:
			return explorer.ReceiptConstructionStageColumn{}, false
		}
	}
	return explorer.ReceiptConstructionStageColumn{}, false
}

func constructionCategoryScalar(value dataframeexecution.CategoryValue, kind string) (authoringv2.TableScalar, error) {
	if !value.Present {
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarMissing}, nil
	}
	if value.Value == nil {
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarNull}, nil
	}
	switch expression.ValueKind(kind) {
	case expression.KindString, expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		v, ok := value.Value.(string)
		if !ok {
			return authoringv2.TableScalar{}, fmt.Errorf("expected string, got %T", value.Value)
		}
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &v}, nil
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
				return authoringv2.TableScalar{}, err
			}
			v = parsed
		default:
			return authoringv2.TableScalar{}, fmt.Errorf("expected integer, got %T", value.Value)
		}
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarInteger, Integer: &v}, nil
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
				return authoringv2.TableScalar{}, err
			}
			v = parsed
		default:
			return authoringv2.TableScalar{}, fmt.Errorf("expected decimal, got %T", value.Value)
		}
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarDecimal, Decimal: &v}, nil
	case expression.KindBoolean:
		v, ok := value.Value.(bool)
		if !ok {
			return authoringv2.TableScalar{}, fmt.Errorf("expected boolean, got %T", value.Value)
		}
		return authoringv2.TableScalar{Kind: authoringv2.TableScalarBoolean, Boolean: &v}, nil
	default:
		return authoringv2.TableScalar{}, fmt.Errorf("unsupported category type %q", kind)
	}
}

func constructionCategoryLabel(value authoringv2.TableScalar) string {
	switch value.Kind {
	case authoringv2.TableScalarMissing:
		return "Missing"
	case authoringv2.TableScalarNull:
		return "Null"
	case authoringv2.TableScalarString:
		return *value.String
	case authoringv2.TableScalarInteger:
		return strconv.FormatInt(*value.Integer, 10)
	case authoringv2.TableScalarDecimal:
		return strconv.FormatFloat(*value.Decimal, 'f', -1, 64)
	case authoringv2.TableScalarBoolean:
		return strconv.FormatBool(*value.Boolean)
	default:
		return strings.TrimSpace(string(value.Kind))
	}
}
