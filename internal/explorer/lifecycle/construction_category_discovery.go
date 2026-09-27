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
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	CategoryColumnID     string
	ValueColumnID        string
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
	constructionCategoryDiscoveryComplete      = "COMPLETE"
	constructionCategoryDiscoveryLimitExceeded = "LIMIT_EXCEEDED"
)

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
	if (!overflow && !scan.Complete) ||
		proof.Version != 2 || proof.Output != request.OutputID || proof.StageID != request.StageID ||
		proof.ColumnID != request.CategoryColumnID || proof.ValueColumnID != request.ValueColumnID ||
		proof.Column != category.Name || proof.MaxValues != compiler.MaxCategoryScanValues ||
		proof.Kind != category.Type || proof.Cardinality != string(category.Cardinality) ||
		strings.TrimSpace(proof.OutputSchemaDigest) == "" || strings.TrimSpace(proof.PlanFingerprint) == "" ||
		strings.TrimSpace(proof.QueryFingerprint) == "" || strings.TrimSpace(proof.Fingerprint) == "" {
		return ConstructionCategoryDiscoveryResponse{}, unprocessable("construction-category-discovery", "CATEGORY_SCAN_INCOMPLETE", "pivot categories require a complete stage-bound scan within the supported limit", nil)
	}
	if overflow {
		return ConstructionCategoryDiscoveryResponse{
			SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
			OutputID: request.OutputID, StageID: request.StageID, CategoryColumnID: request.CategoryColumnID,
			ValueColumnID: request.ValueColumnID, Outcome: constructionCategoryDiscoveryLimitExceeded,
			Categories: []ConstructionDiscoveredCategory{}, Limit: compiler.MaxCategoryScanValues,
			Message: "This field has more than 256 category values in the current rows. Choose another category field or filter rows before pivoting.",
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
		OutputID: request.OutputID, StageID: request.StageID, CategoryColumnID: request.CategoryColumnID,
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
