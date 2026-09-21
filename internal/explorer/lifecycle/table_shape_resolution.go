package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
	"github.com/calypr/loom/internal/projectid"
)

const maxPivotCategories = compiler.MaxCategoryScanValues

type TableShapeResolutionRequest struct {
	Project              string                       `json:"project"`
	ExplorerID           string                       `json:"explorerId"`
	SnapshotToken        string                       `json:"snapshotToken"`
	ExpectedDraftVersion int64                        `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string                       `json:"expectedDraftDigest"`
	OutputID             string                       `json:"outputId"`
	CatalogID            string                       `json:"catalogId"`
	Kind                 tableshapecap.ResolutionKind `json:"kind"`
	Pivot                *TableShapePivotSelection    `json:"pivot,omitempty"`
	Unpivot              *TableShapeUnpivotSelection  `json:"unpivot,omitempty"`
	Derived              *TableShapeDerivedSelection  `json:"derived,omitempty"`
}

type TableShapePivotSelection struct {
	CategoryDiscoveryID     string                             `json:"categoryDiscoveryId"`
	GroupColumnChoiceIDs    []string                           `json:"groupColumnChoiceIds"`
	CategoryColumnChoiceID  string                             `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID     string                             `json:"valueColumnChoiceId"`
	Categories              []TableShapePivotCategorySelection `json:"categories"`
	DuplicatePolicyChoiceID string                             `json:"duplicatePolicyChoiceId"`
	MissingPolicyChoiceID   string                             `json:"missingPolicyChoiceId"`
	UnlistedPolicyChoiceID  string                             `json:"unlistedPolicyChoiceId"`
}

type TableShapePivotCategorySelection struct {
	ChoiceID     string `json:"choiceId"`
	OutputColumn string `json:"outputColumn"`
	OutputLabel  string `json:"outputLabel"`
}

type TableShapeCategoryDiscoveryRequest struct {
	Project                string `json:"project"`
	ExplorerID             string `json:"explorerId"`
	SnapshotToken          string `json:"snapshotToken"`
	ExpectedDraftVersion   int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest    string `json:"expectedDraftDigest"`
	OutputID               string `json:"outputId"`
	CatalogID              string `json:"catalogId"`
	CategoryColumnChoiceID string `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID    string `json:"valueColumnChoiceId"`
}

func (r TableShapeCategoryDiscoveryRequest) Validate() error {
	if err := (TableShapeCatalogRequest{
		Project: r.Project, ExplorerID: r.ExplorerID, SnapshotToken: r.SnapshotToken,
		ExpectedDraftVersion: r.ExpectedDraftVersion, ExpectedDraftDigest: r.ExpectedDraftDigest, OutputID: r.OutputID,
	}).Validate(); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"catalogId": r.CatalogID, "categoryColumnChoiceId": r.CategoryColumnChoiceID, "valueColumnChoiceId": r.ValueColumnChoiceID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.CategoryColumnChoiceID == r.ValueColumnChoiceID {
		return fmt.Errorf("category and value column choices must differ")
	}
	return nil
}

func (r *TableShapeCategoryDiscoveryRequest) UnmarshalJSON(data []byte) error {
	type wire TableShapeCategoryDiscoveryRequest
	value, err := tableshapecap.DecodeStrict[wire](data)
	if err != nil {
		return err
	}
	decoded := TableShapeCategoryDiscoveryRequest(value)
	if err := decoded.Validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

type TableShapeDiscoveredCategory struct {
	ID                    string               `json:"id"`
	Value                 tableshapecap.Scalar `json:"value"`
	Label                 string               `json:"label"`
	SuggestedOutputColumn string               `json:"suggestedOutputColumn"`
	SuggestedOutputLabel  string               `json:"suggestedOutputLabel"`
}

type TableShapeCategoryDiscoveryResult struct {
	CatalogID              string                         `json:"catalogId"`
	DiscoveryID            string                         `json:"discoveryId"`
	CategoryColumnChoiceID string                         `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID    string                         `json:"valueColumnChoiceId"`
	Categories             []TableShapeDiscoveredCategory `json:"categories"`
}

type TableShapeUnpivotSelection struct {
	InputColumnChoiceIDs []string `json:"inputColumnChoiceIds"`
	NullPolicyChoiceID   string   `json:"nullPolicyChoiceId"`
	KeyOutputColumn      string   `json:"keyOutputColumn"`
	KeyOutputLabel       string   `json:"keyOutputLabel"`
	ValueOutputColumn    string   `json:"valueOutputColumn"`
	ValueOutputLabel     string   `json:"valueOutputLabel"`
}

type TableShapeDerivedSelection struct {
	OutputColumn                 string                     `json:"outputColumn"`
	OutputLabel                  string                     `json:"outputLabel"`
	PivotResolutionID            string                     `json:"pivotResolutionId,omitempty"`
	OperatorChoiceID             string                     `json:"operatorChoiceId"`
	Left                         TableShapeOperandSelection `json:"left"`
	Right                        TableShapeOperandSelection `json:"right"`
	MissingPolicyChoiceID        string                     `json:"missingPolicyChoiceId"`
	DivisionByZeroPolicyChoiceID string                     `json:"divisionByZeroPolicyChoiceId,omitempty"`
}

type TableShapeOperandSelection struct {
	Kind         tableshapecap.ResolvedOperandKind `json:"kind"`
	ChoiceID     string                            `json:"choiceId,omitempty"`
	ResolutionID string                            `json:"resolutionId,omitempty"`
	Literal      *tableshapecap.Scalar             `json:"literal,omitempty"`
}

func (r TableShapeOperandSelection) validate() error {
	choices := 0
	if r.ChoiceID != "" {
		choices++
	}
	if r.ResolutionID != "" {
		choices++
	}
	if r.Literal != nil {
		choices++
	}
	switch r.Kind {
	case tableshapecap.ResolvedOperandCatalogChoice:
		if choices != 1 || r.ChoiceID == "" || r.ResolutionID != "" || r.Literal != nil {
			return fmt.Errorf("CATALOG_CHOICE operand requires only choiceId")
		}
	case tableshapecap.ResolvedOperandResolution:
		if choices != 1 || r.ResolutionID == "" || r.ChoiceID != "" || r.Literal != nil {
			return fmt.Errorf("RESOLUTION_OUTPUT operand requires only resolutionId")
		}
	case tableshapecap.ResolvedOperandLiteral:
		if choices != 1 || r.Literal == nil || r.ChoiceID != "" || r.ResolutionID != "" {
			return fmt.Errorf("LITERAL operand requires only literal")
		}
		if err := r.Literal.Validate(); err != nil {
			return err
		}
		if r.Literal.Kind != tableshapecap.ScalarInteger && r.Literal.Kind != tableshapecap.ScalarDecimal {
			return fmt.Errorf("derived literal must be numeric")
		}
	default:
		return fmt.Errorf("unknown operand kind %q", r.Kind)
	}
	return nil
}

func (r TableShapeResolutionRequest) Validate() error {
	if err := (TableShapeCatalogRequest{
		Project: r.Project, ExplorerID: r.ExplorerID, SnapshotToken: r.SnapshotToken,
		ExpectedDraftVersion: r.ExpectedDraftVersion, ExpectedDraftDigest: r.ExpectedDraftDigest, OutputID: r.OutputID,
	}).Validate(); err != nil {
		return err
	}
	if err := requireExactIdentity(r.CatalogID, "catalogId"); err != nil {
		return err
	}
	payloads := 0
	if r.Pivot != nil {
		payloads++
	}
	if r.Unpivot != nil {
		payloads++
	}
	if r.Derived != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("resolution must contain exactly one typed selection")
	}
	switch r.Kind {
	case tableshapecap.ResolutionPivot:
		p := r.Pivot
		if p == nil || r.Unpivot != nil || r.Derived != nil || len(p.GroupColumnChoiceIDs) == 0 || len(p.Categories) == 0 {
			return fmt.Errorf("PIVOT resolution requires only a non-empty pivot selection")
		}
		for _, value := range append(append([]string(nil), p.GroupColumnChoiceIDs...), p.CategoryDiscoveryID, p.CategoryColumnChoiceID, p.ValueColumnChoiceID, p.DuplicatePolicyChoiceID, p.MissingPolicyChoiceID, p.UnlistedPolicyChoiceID) {
			if err := requireExactIdentity(value, "choiceId"); err != nil {
				return err
			}
		}
		for _, category := range p.Categories {
			if err := requireExactIdentity(category.ChoiceID, "categoryChoiceId"); err != nil {
				return err
			}
			if err := requireExactIdentity(category.OutputColumn, "outputColumn"); err != nil {
				return err
			}
			if err := requireExactIdentity(category.OutputLabel, "outputLabel"); err != nil {
				return err
			}
		}
	case tableshapecap.ResolutionUnpivot:
		p := r.Unpivot
		if p == nil || r.Pivot != nil || r.Derived != nil || len(p.InputColumnChoiceIDs) < 2 {
			return fmt.Errorf("UNPIVOT resolution requires only at least two input choices")
		}
		for _, value := range append(append([]string(nil), p.InputColumnChoiceIDs...), p.NullPolicyChoiceID) {
			if err := requireExactIdentity(value, "choiceId"); err != nil {
				return err
			}
		}
		for _, value := range []string{p.KeyOutputColumn, p.KeyOutputLabel, p.ValueOutputColumn, p.ValueOutputLabel} {
			if err := requireExactIdentity(value, "unpivotOutput"); err != nil {
				return err
			}
		}
		if p.KeyOutputColumn == p.ValueOutputColumn {
			return fmt.Errorf("unpivot output columns must differ")
		}
	case tableshapecap.ResolutionDerived:
		p := r.Derived
		if p == nil || r.Pivot != nil || r.Unpivot != nil {
			return fmt.Errorf("DERIVED resolution requires only a derived selection")
		}
		for _, value := range []string{p.OutputColumn, p.OutputLabel, p.OperatorChoiceID, p.MissingPolicyChoiceID} {
			if err := requireExactIdentity(value, "choiceId"); err != nil {
				return err
			}
		}
		if p.PivotResolutionID != "" {
			if err := requireExactIdentity(p.PivotResolutionID, "pivotResolutionId"); err != nil {
				return err
			}
		}
		if p.DivisionByZeroPolicyChoiceID != "" {
			if err := requireExactIdentity(p.DivisionByZeroPolicyChoiceID, "choiceId"); err != nil {
				return err
			}
		}
		if err := p.Left.validate(); err != nil {
			return fmt.Errorf("left operand: %w", err)
		}
		if err := p.Right.validate(); err != nil {
			return fmt.Errorf("right operand: %w", err)
		}
	default:
		return fmt.Errorf("unknown resolution kind %q", r.Kind)
	}
	return nil
}

func (r *TableShapeResolutionRequest) UnmarshalJSON(data []byte) error {
	type wire TableShapeResolutionRequest
	value, err := tableshapecap.DecodeStrict[wire](data)
	if err != nil {
		return err
	}
	decoded := TableShapeResolutionRequest(value)
	if err := decoded.Validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

type TableShapeResolvedCategory struct {
	ID           string               `json:"id"`
	Value        tableshapecap.Scalar `json:"value"`
	OutputColumn string               `json:"outputColumn"`
	OutputLabel  string               `json:"outputLabel"`
}

type TableShapeResolutionResult struct {
	CatalogID           string                       `json:"catalogId"`
	ResolutionID        string                       `json:"resolutionId"`
	Kind                tableshapecap.ResolutionKind `json:"kind"`
	CategoryDiscoveryID string                       `json:"categoryDiscoveryId,omitempty"`
	Categories          []TableShapeResolvedCategory `json:"categories,omitempty"`
	Result              *tableshapecap.TypeFact      `json:"result,omitempty"`
	KeyResult           *tableshapecap.TypeFact      `json:"keyResult,omitempty"`
	ValueResult         *tableshapecap.TypeFact      `json:"valueResult,omitempty"`
	DerivedOperands     []TableShapeCatalogChoice    `json:"derivedOperands,omitempty"`
}

func (s *Service) ResolveTableShape(ctx context.Context, request TableShapeResolutionRequest) (TableShapeResolutionResult, error) {
	if err := request.Validate(); err != nil {
		return TableShapeResolutionResult{}, malformed("table-shape-resolution", err.Error(), err)
	}
	if s.config.TableShapeCapabilities == nil {
		return TableShapeResolutionResult{}, unavailable("table-shape-resolution", "CAPABILITY_STORE_UNAVAILABLE", "table-shape capability storage is not configured", nil)
	}
	base, catalog, err := s.tableShapeCatalogForRequest(ctx, TableShapeCatalogRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest, OutputID: request.OutputID,
	}, request.CatalogID)
	if err != nil {
		return TableShapeResolutionResult{}, err
	}
	var pivot *tableshapecap.PivotResolution
	var categoryScan *tableshapecap.CategoryScanReceipt
	var unpivot *tableshapecap.UnpivotResolution
	var derived *tableshapecap.DerivedResolution
	var prior []tableshapecap.ResolutionReceipt
	result := TableShapeResolutionResult{CatalogID: catalog.ID, Kind: request.Kind}
	switch request.Kind {
	case tableshapecap.ResolutionPivot:
		resolved, scan, err := s.resolveTableShapePivot(ctx, catalog, *request.Pivot)
		if err != nil {
			return TableShapeResolutionResult{}, err
		}
		pivot, categoryScan = &resolved, &scan
		result.CategoryDiscoveryID = scan.ID
	case tableshapecap.ResolutionUnpivot:
		resolved, keyType, valueType, err := resolveTableShapeUnpivot(catalog, *request.Unpivot)
		if err != nil {
			return TableShapeResolutionResult{}, err
		}
		unpivot = &resolved
		result.KeyResult, result.ValueResult = &keyType, &valueType
	case tableshapecap.ResolutionDerived:
		resolved, resultType, previous, err := s.resolveTableShapeDerived(ctx, catalog, *request.Derived)
		if err != nil {
			return TableShapeResolutionResult{}, err
		}
		derived, prior, result.Result = &resolved, previous, &resultType
	}
	receipt, err := tableshapecap.NewResolutionReceipt(base.binding, catalog.ID, request.Kind, pivot, unpivot, derived, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return TableShapeResolutionResult{}, unprocessable("table-shape-resolution", "INVALID_TABLE_SHAPE_SELECTION", "the selected table-shape choices are incompatible", err)
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, receipt, prior...); err != nil {
		return TableShapeResolutionResult{}, unprocessable("table-shape-resolution", "INVALID_TABLE_SHAPE_SELECTION", "the selected table-shape choices are not valid for this catalog", err)
	}
	if categoryScan != nil {
		if err := tableshapecap.ValidateResolutionAgainstCategoryScan(catalog, *categoryScan, receipt); err != nil {
			return TableShapeResolutionResult{}, unprocessable("table-shape-resolution", "INVALID_CATEGORY_SELECTION", "the selected pivot categories are not from the bound complete discovery receipt", err)
		}
	}
	stored, err := s.config.TableShapeCapabilities.PutResolution(ctx, receipt)
	if err != nil {
		return TableShapeResolutionResult{}, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "the table-shape resolution could not be stored", err)
	}
	if err := stored.Validate(); err != nil || stored.ID != receipt.ID || stored.Binding != base.binding || stored.ParentCatalogID != catalog.ID || stored.Kind != request.Kind {
		return TableShapeResolutionResult{}, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "the stored table-shape resolution failed identity validation", err)
	}
	result.ResolutionID = stored.ID
	if stored.Pivot != nil {
		for _, operand := range stored.Pivot.DerivedOperands {
			fact := operand.Type
			result.DerivedOperands = append(result.DerivedOperands, TableShapeCatalogChoice{
				Role: tableshapecap.RoleDerivedOperand, ID: operand.ChoiceID, Label: operand.OutputLabel, Type: &fact,
			})
		}
		result.Categories = make([]TableShapeResolvedCategory, len(stored.Pivot.Categories))
		for i, category := range stored.Pivot.Categories {
			result.Categories[i] = TableShapeResolvedCategory{ID: category.ChoiceID, Value: category.Value, OutputColumn: category.OutputColumn, OutputLabel: category.OutputLabel}
		}
	}
	return result, nil
}

func (s *Service) DiscoverTableShapeCategories(ctx context.Context, request TableShapeCategoryDiscoveryRequest) (TableShapeCategoryDiscoveryResult, error) {
	if err := request.Validate(); err != nil {
		return TableShapeCategoryDiscoveryResult{}, malformed("table-shape-category-discovery", err.Error(), err)
	}
	base, catalog, err := s.tableShapeCatalogForRequest(ctx, TableShapeCatalogRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest, OutputID: request.OutputID,
	}, request.CatalogID)
	if err != nil {
		return TableShapeCategoryDiscoveryResult{}, err
	}
	scan, err := s.scanTableShapeCategoryPair(ctx, base, catalog, request.CategoryColumnChoiceID, request.ValueColumnChoiceID)
	if err != nil {
		return TableShapeCategoryDiscoveryResult{}, err
	}
	stored, err := s.config.TableShapeCapabilities.PutCategoryScan(ctx, scan)
	if err != nil {
		return TableShapeCategoryDiscoveryResult{}, unavailable("table-shape-category-discovery", "CAPABILITY_STORE_FAILED", "the pivot category discovery could not be stored", err)
	}
	if err := stored.ValidateAgainstCatalog(catalog); err != nil || stored.ID != scan.ID || stored.Binding != base.binding {
		return TableShapeCategoryDiscoveryResult{}, unavailable("table-shape-category-discovery", "CAPABILITY_STORE_FAILED", "the stored category discovery failed identity validation", err)
	}
	result := TableShapeCategoryDiscoveryResult{
		CatalogID: catalog.ID, DiscoveryID: stored.ID,
		CategoryColumnChoiceID: stored.CategoryColumnChoiceID, ValueColumnChoiceID: stored.ValueColumnChoiceID,
		Categories: make([]TableShapeDiscoveredCategory, len(stored.Categories)),
	}
	for i, category := range stored.Categories {
		outputColumn := fmt.Sprintf("pivot_category_%d", i+1)
		label := tableShapeCategoryLabel(category.Value)
		result.Categories[i] = TableShapeDiscoveredCategory{
			ID: category.ChoiceID, Value: category.Value, Label: label,
			SuggestedOutputColumn: outputColumn, SuggestedOutputLabel: label,
		}
	}
	return result, nil
}

func (s *Service) scanTableShapeCategoryPair(ctx context.Context, base tableShapeBase, catalog tableshapecap.CatalogReceipt, categoryChoiceID, valueChoiceID string) (tableshapecap.CategoryScanReceipt, error) {
	category, err := catalog.FindColumn(tableshapecap.RolePivotCategory, categoryChoiceID)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "INVALID_CHOICE_ID", "the category column choice is not available in this catalog", err)
	}
	if _, err := catalog.FindColumn(tableshapecap.RolePivotValue, valueChoiceID); err != nil {
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "INVALID_CHOICE_ID", "the value column choice is not available in this catalog", err)
	}
	column := findPublicColumn(catalog, category.ColumnKey)
	if column == nil {
		return tableshapecap.CategoryScanReceipt{}, conflict("table-shape-category-discovery", "INVALID_TABLE_SHAPE_CAPABILITY", "the category choice has no compiler-owned output column", nil, nil)
	}
	if s.config.ScanTableShapeCategories == nil {
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "CATEGORY_SCAN_UNAVAILABLE", "complete pivot categories cannot be discovered", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(catalog.Binding.Project), SelectionProject: catalog.Binding.Project,
		DatasetGeneration: catalog.Binding.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
		OutputNames: []string{catalog.Binding.OutputID},
	}
	applyAuthorizedScope(&bindings, base.authorized, false)
	scan, err := s.config.ScanTableShapeCategories(ctx, base.receipt, bindings, dataframeexecution.CategoryScanRequest{Output: catalog.Binding.OutputID, Column: column.Key, MaxValues: maxPivotCategories})
	if err != nil {
		code := "CATEGORY_SCAN_REFUSED"
		if refusal, ok := compiler.CategoryScanRefusalCodeOf(err); ok {
			code = string(refusal)
		}
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", code, "complete compiler-owned pivot categories are unavailable for this column", err)
	}
	proof := scan.Proof
	if !scan.Complete || scan.Overflow || len(scan.Values) > maxPivotCategories || proof.Version != 1 || proof.Output != catalog.Binding.OutputID || proof.Column != column.Key || proof.MaxValues != maxPivotCategories || strings.TrimSpace(proof.Kind) == "" || strings.TrimSpace(proof.Cardinality) == "" || strings.TrimSpace(proof.OutputSchemaDigest) == "" || strings.TrimSpace(proof.PlanFingerprint) == "" || strings.TrimSpace(proof.QueryFingerprint) == "" || strings.TrimSpace(proof.Fingerprint) == "" {
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "CATEGORY_SCAN_INCOMPLETE", "pivot categories require a complete compiler scan within the supported limit", nil)
	}
	values := make([]tableshapecap.Scalar, 0, len(scan.Values))
	for _, item := range scan.Values {
		value, err := categoryScanScalar(item, column.LogicalType)
		if err != nil {
			return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "CATEGORY_SCAN_INVALID", "the compiler category scan returned an unsupported category value", err)
		}
		values = append(values, value)
	}
	valuesDigest, err := tableshapecap.CategoryValuesDigest(values)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "CATEGORY_SCAN_INVALID", "the compiler category scan returned invalid category values", err)
	}
	bound := tableshapecap.CategoryProof{
		Complete: true, DistinctCount: len(values), MaxCategories: maxPivotCategories, ValuesDigest: valuesDigest,
		SourceGeneration: catalog.Binding.SourceGeneration, OutputFingerprint: catalog.Binding.OutputFingerprint,
		ScanFingerprint: proof.Fingerprint, QueryProof: proof.QueryFingerprint,
	}
	receipt, err := tableshapecap.NewCategoryScanReceipt(catalog.Binding, catalog.ID, categoryChoiceID, valueChoiceID, values, bound, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-category-discovery", "CATEGORY_SCAN_INVALID", "the category scan result failed typed receipt validation", err)
	}
	if err := receipt.ValidateAgainstCatalog(catalog); err != nil {
		return tableshapecap.CategoryScanReceipt{}, conflict("table-shape-category-discovery", "INVALID_TABLE_SHAPE_CAPABILITY", "the category scan does not match its catalog choices", nil, err)
	}
	return receipt, nil
}

func (s *Service) resolveTableShapePivot(ctx context.Context, catalog tableshapecap.CatalogReceipt, selection TableShapePivotSelection) (tableshapecap.PivotResolution, tableshapecap.CategoryScanReceipt, error) {
	scan, err := s.config.TableShapeCapabilities.GetCategoryScan(ctx, catalog.Binding, catalog.ID, selection.CategoryDiscoveryID)
	if err != nil {
		if errors.Is(err, tableshapecap.ErrNotFound) {
			return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "CATEGORY_DISCOVERY_NOT_FOUND", "the pivot category discovery is not available for this catalog", err)
		}
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "the pivot category discovery could not be loaded", err)
	}
	if err := scan.ValidateAgainstCatalog(catalog); err != nil {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, conflict("table-shape-resolution", "INVALID_CATEGORY_DISCOVERY", "the pivot category discovery failed binding validation", nil, err)
	}
	if scan.CategoryColumnChoiceID != selection.CategoryColumnChoiceID || scan.ValueColumnChoiceID != selection.ValueColumnChoiceID {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "CATEGORY_DISCOVERY_MISMATCH", "the category discovery belongs to a different pivot category/value pair", nil)
	}
	valueChoice, err := catalog.FindColumn(tableshapecap.RolePivotValue, selection.ValueColumnChoiceID)
	if err != nil {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the pivot value choice is not available in this catalog", err)
	}
	valueColumn := findPublicColumn(catalog, valueChoice.ColumnKey)
	if valueColumn == nil {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the pivot value choice has no compiler-owned output column", nil, nil)
	}
	duplicatePolicy, err := catalog.FindPolicy(tableshapecap.RolePolicyDuplicate, selection.DuplicatePolicyChoiceID)
	if err != nil {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the pivot duplicate-cell policy is not available in this catalog", err)
	}
	if duplicatePolicy.PolicyID != "ERROR" && valueColumn.LogicalType != tableshapecap.LogicalInteger && valueColumn.LogicalType != tableshapecap.LogicalDecimal {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INCOMPATIBLE_PIVOT_REDUCER", "SUM, MIN, and MAX require an integer or decimal pivot value column", nil)
	}
	resolved := tableshapecap.PivotResolution{
		GroupColumnChoiceIDs:   append([]string(nil), selection.GroupColumnChoiceIDs...),
		CategoryColumnChoiceID: selection.CategoryColumnChoiceID, ValueColumnChoiceID: selection.ValueColumnChoiceID,
		CategoryDiscoveryID: scan.ID, CategoryProof: scan.Proof,
		DuplicatePolicyChoiceID: selection.DuplicatePolicyChoiceID,
		MissingPolicyChoiceID:   selection.MissingPolicyChoiceID, UnlistedPolicyChoiceID: selection.UnlistedPolicyChoiceID,
	}
	usedOutputNames := make(map[string]struct{}, len(catalog.Columns))
	for _, column := range catalog.Columns {
		usedOutputNames[column.Key] = struct{}{}
	}
	for _, selected := range selection.Categories {
		if err := authoringv2.ValidateTableShapeOutput(authoringv2.ColumnOutput{Column: selected.OutputColumn, Label: selected.OutputLabel}); err != nil {
			return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INVALID_OUTPUT_NAME", "a pivot output name or label is invalid", err)
		}
		if _, exists := usedOutputNames[selected.OutputColumn]; exists {
			return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "DUPLICATE_OUTPUT_NAME", "pivot output names must not collide with base columns or each other", nil)
		}
		usedOutputNames[selected.OutputColumn] = struct{}{}
		category, err := scan.FindCategoryChoice(selected.ChoiceID)
		if err != nil {
			return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INVALID_CATEGORY_CHOICE_ID", "a selected pivot category does not belong to the complete discovery receipt", err)
		}
		resolved.Categories = append(resolved.Categories, tableshapecap.FrozenCategory{
			ChoiceID: category.ChoiceID, Value: category.Value,
			OutputColumn: selected.OutputColumn, OutputLabel: selected.OutputLabel,
		})
	}
	missingPolicy, err := catalog.FindPolicy(tableshapecap.RolePolicyMissing, resolved.MissingPolicyChoiceID)
	if err != nil {
		return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the pivot missing-cell policy is not available in this catalog", err)
	}
	valueType := tableShapeTypeFact(*valueColumn)
	categoryType := valueType
	categoryType.Nullable = valueType.Nullable || missingPolicy.PolicyID == "NULL"
	for _, id := range resolved.GroupColumnChoiceIDs {
		group, err := catalog.FindColumn(tableshapecap.RolePivotGroup, id)
		if err != nil {
			return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "a pivot group choice is not available in this catalog", err)
		}
		column := findPublicColumn(catalog, group.ColumnKey)
		if column == nil {
			return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the pivot group choice has no compiler-owned output column", nil, nil)
		}
		if column.LogicalType == tableshapecap.LogicalInteger || column.LogicalType == tableshapecap.LogicalDecimal {
			if err := appendPivotDerivedOperand(catalog.ID, &resolved, tableshapecap.NamedOutput{Name: column.Key, Label: column.Label}, tableShapeTypeFact(*column)); err != nil {
				return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the pivot group output choice could not be created", nil, err)
			}
		}
	}
	if valueType.LogicalType == tableshapecap.LogicalInteger || valueType.LogicalType == tableshapecap.LogicalDecimal {
		for _, category := range resolved.Categories {
			if err := appendPivotDerivedOperand(catalog.ID, &resolved, tableshapecap.NamedOutput{Name: category.OutputColumn, Label: category.OutputLabel}, categoryType); err != nil {
				return tableshapecap.PivotResolution{}, tableshapecap.CategoryScanReceipt{}, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the pivot category output choice could not be created", nil, err)
			}
		}
	}
	return resolved, scan, nil
}

func appendPivotDerivedOperand(catalogID string, pivot *tableshapecap.PivotResolution, output tableshapecap.NamedOutput, fact tableshapecap.TypeFact) error {
	choiceID, err := tableshapecap.NewPivotDerivedOperandChoiceID(catalogID, *pivot, output, fact)
	if err != nil {
		return err
	}
	pivot.DerivedOperands = append(pivot.DerivedOperands, tableshapecap.PivotDerivedOperand{
		ChoiceID: choiceID, OutputColumn: output.Name, OutputLabel: output.Label, Type: fact,
	})
	return nil
}

func tableShapeCategoryLabel(value tableshapecap.Scalar) string {
	switch value.Kind {
	case tableshapecap.ScalarString:
		if *value.String == "" {
			return "(empty)"
		}
		return *value.String
	case tableshapecap.ScalarInteger:
		return fmt.Sprintf("%d", *value.Integer)
	case tableshapecap.ScalarDecimal:
		return fmt.Sprintf("%g", *value.Decimal)
	case tableshapecap.ScalarBoolean:
		return fmt.Sprintf("%t", *value.Boolean)
	case tableshapecap.ScalarNull:
		return "Null"
	case tableshapecap.ScalarMissing:
		return "Missing"
	default:
		return ""
	}
}

func categoryScanScalar(value dataframeexecution.CategoryValue, logical tableshapecap.LogicalType) (tableshapecap.Scalar, error) {
	if !value.Present {
		return tableshapecap.MissingScalar(), nil
	}
	if value.Value == nil {
		return tableshapecap.NullScalar(), nil
	}
	switch logical {
	case tableshapecap.LogicalString, tableshapecap.LogicalDate, tableshapecap.LogicalDateTime:
		v, ok := value.Value.(string)
		if !ok {
			return tableshapecap.Scalar{}, fmt.Errorf("expected string, got %T", value.Value)
		}
		return tableshapecap.StringScalar(v), nil
	case tableshapecap.LogicalInteger:
		switch v := value.Value.(type) {
		case int:
			return tableshapecap.IntegerScalar(int64(v)), nil
		case int32:
			return tableshapecap.IntegerScalar(int64(v)), nil
		case int64:
			return tableshapecap.IntegerScalar(v), nil
		case json.Number:
			n, err := v.Int64()
			if err == nil {
				return tableshapecap.IntegerScalar(n), nil
			}
		}
		return tableshapecap.Scalar{}, fmt.Errorf("expected integer, got %T", value.Value)
	case tableshapecap.LogicalDecimal:
		switch v := value.Value.(type) {
		case int:
			return tableshapecap.DecimalScalar(float64(v)), nil
		case int32:
			return tableshapecap.DecimalScalar(float64(v)), nil
		case int64:
			return tableshapecap.DecimalScalar(float64(v)), nil
		case float32:
			return tableshapecap.DecimalScalar(float64(v)), nil
		case float64:
			return tableshapecap.DecimalScalar(v), nil
		case json.Number:
			n, err := v.Float64()
			if err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
				return tableshapecap.DecimalScalar(n), nil
			}
		}
		return tableshapecap.Scalar{}, fmt.Errorf("expected decimal, got %T", value.Value)
	case tableshapecap.LogicalBoolean:
		v, ok := value.Value.(bool)
		if !ok {
			return tableshapecap.Scalar{}, fmt.Errorf("expected boolean, got %T", value.Value)
		}
		return tableshapecap.BooleanScalar(v), nil
	default:
		return tableshapecap.Scalar{}, fmt.Errorf("logical type %s cannot be a category", logical)
	}
}

func findPublicColumn(catalog tableshapecap.CatalogReceipt, key string) *tableshapecap.PublicColumn {
	for i := range catalog.Columns {
		if catalog.Columns[i].Key == key {
			return &catalog.Columns[i]
		}
	}
	return nil
}

func resolveTableShapeUnpivot(catalog tableshapecap.CatalogReceipt, selection TableShapeUnpivotSelection) (tableshapecap.UnpivotResolution, tableshapecap.TypeFact, tableshapecap.TypeFact, error) {
	for _, output := range []authoringv2.ColumnOutput{
		{Column: selection.KeyOutputColumn, Label: selection.KeyOutputLabel},
		{Column: selection.ValueOutputColumn, Label: selection.ValueOutputLabel},
	} {
		if err := authoringv2.ValidateTableShapeOutput(output); err != nil {
			return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, unprocessable("table-shape-resolution", "INVALID_OUTPUT_NAME", "an unpivot output name or label is invalid", err)
		}
		if findPublicColumn(catalog, output.Column) != nil {
			return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, unprocessable("table-shape-resolution", "DUPLICATE_OUTPUT_NAME", "unpivot output names must not collide with base columns", nil)
		}
	}
	seen := make(map[string]struct{}, len(selection.InputColumnChoiceIDs))
	var logical tableshapecap.LogicalType
	var unit string
	inputs := make([]tableshapecap.ResolvedUnpivotInput, 0, len(selection.InputColumnChoiceIDs))
	ids := append([]string(nil), selection.InputColumnChoiceIDs...)
	for i, id := range selection.InputColumnChoiceIDs {
		choice, err := catalog.FindColumn(tableshapecap.RoleUnpivotInput, id)
		if err != nil {
			return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "an unpivot input choice is not available in this catalog", err)
		}
		if _, exists := seen[choice.ColumnKey]; exists {
			return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, unprocessable("table-shape-resolution", "INVALID_SELECTION", "unpivot input columns must be distinct", nil)
		}
		seen[choice.ColumnKey] = struct{}{}
		column := findPublicColumn(catalog, choice.ColumnKey)
		if column == nil {
			return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the catalog unpivot choice has no compiler-owned column", nil, nil)
		}
		if i == 0 {
			logical, unit = column.LogicalType, column.UnitIdentity
		} else if column.UnitIdentity != unit || !compatibleUnpivotLogicalType(logical, column.LogicalType) {
			return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, unprocessable("table-shape-resolution", "INCOMPATIBLE_UNPIVOT_INPUTS", "unpivot inputs must have identical compiler-resolved types and units", nil)
		}
		logical = promotedUnpivotLogicalType(logical, column.LogicalType)
		inputs = append(inputs, tableshapecap.ResolvedUnpivotInput{ChoiceID: choice.ID, Key: tableshapecap.StringScalar(choice.ColumnKey)})
	}
	policy, err := catalog.FindPolicy(tableshapecap.RolePolicyUnpivotNull, selection.NullPolicyChoiceID)
	if err != nil {
		return tableshapecap.UnpivotResolution{}, tableshapecap.TypeFact{}, tableshapecap.TypeFact{}, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the unpivot null policy choice is not available in this catalog", err)
	}
	keyType := tableshapecap.TypeFact{LogicalType: tableshapecap.LogicalString, Nullable: false}
	valueType := tableshapecap.TypeFact{LogicalType: logical, Nullable: policy.PolicyID == "PRESERVE", UnitIdentity: unit}
	resolution := tableshapecap.UnpivotResolution{
		InputColumnChoiceIDs: ids, Inputs: inputs,
		KeyOutput:   tableshapecap.NamedOutput{Name: selection.KeyOutputColumn, Label: selection.KeyOutputLabel},
		ValueOutput: tableshapecap.NamedOutput{Name: selection.ValueOutputColumn, Label: selection.ValueOutputLabel},
		KeyResult:   keyType, ValueResult: valueType, NullPolicyChoiceID: policy.ID,
	}
	return resolution, keyType, valueType, nil
}

func compatibleUnpivotLogicalType(left, right tableshapecap.LogicalType) bool {
	if left == right {
		return true
	}
	return left == tableshapecap.LogicalInteger && right == tableshapecap.LogicalDecimal || left == tableshapecap.LogicalDecimal && right == tableshapecap.LogicalInteger
}

func promotedUnpivotLogicalType(left, right tableshapecap.LogicalType) tableshapecap.LogicalType {
	if left == tableshapecap.LogicalDecimal || right == tableshapecap.LogicalDecimal {
		return tableshapecap.LogicalDecimal
	}
	return left
}

func (s *Service) resolveTableShapeDerived(ctx context.Context, catalog tableshapecap.CatalogReceipt, selection TableShapeDerivedSelection) (tableshapecap.DerivedResolution, tableshapecap.TypeFact, []tableshapecap.ResolutionReceipt, error) {
	if err := authoringv2.ValidateTableShapeOutput(authoringv2.ColumnOutput{Column: selection.OutputColumn, Label: selection.OutputLabel}); err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_OUTPUT_NAME", "the derived output name or label is invalid", err)
	}
	if findPublicColumn(catalog, selection.OutputColumn) != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "DUPLICATE_OUTPUT_NAME", "the derived output name collides with a base column", nil)
	}
	var pivotReceipt *tableshapecap.ResolutionReceipt
	if selection.PivotResolutionID != "" {
		loaded, err := s.config.TableShapeCapabilities.GetResolution(ctx, catalog.Binding, catalog.ID, selection.PivotResolutionID)
		if err != nil {
			if errors.Is(err, tableshapecap.ErrNotFound) {
				return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "PIVOT_RESOLUTION_NOT_FOUND", "the selected pivot output context is not available in this catalog", err)
			}
			return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "the selected pivot output context could not be loaded", err)
		}
		if err := loaded.Validate(); err != nil || loaded.ID != selection.PivotResolutionID || loaded.Binding != catalog.Binding || loaded.ParentCatalogID != catalog.ID || loaded.Kind != tableshapecap.ResolutionPivot || loaded.Pivot == nil {
			return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_PIVOT_RESOLUTION", "the selected pivot output context failed binding validation", err)
		}
		if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, loaded); err != nil {
			return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_PIVOT_RESOLUTION", "the selected pivot output context is invalid for this catalog", err)
		}
		pivotReceipt = &loaded
		for _, output := range pivotReceipt.Pivot.DerivedOperands {
			if output.OutputColumn == selection.OutputColumn {
				return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "DUPLICATE_OUTPUT_NAME", "the derived output name collides with a selected pivot output", nil)
			}
		}
	}
	operator, err := catalog.FindOperator(selection.OperatorChoiceID)
	if err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the derived operator choice is not available in this catalog", err)
	}
	missing, err := catalog.FindPolicy(tableshapecap.RolePolicyDerivedMissing, selection.MissingPolicyChoiceID)
	if err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the derived missing-input policy is not available in this catalog", err)
	}
	divisionPolicy := ""
	if operator.Operator == "DIVIDE" {
		policy, err := catalog.FindPolicy(tableshapecap.RolePolicyDivisionByZero, selection.DivisionByZeroPolicyChoiceID)
		if err != nil {
			return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "division requires a valid division-by-zero policy choice", err)
		}
		divisionPolicy = policy.ID
	} else if selection.DivisionByZeroPolicyChoiceID != "" {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_SELECTION", "a division-by-zero policy is valid only for division", nil)
	}
	left, leftType, leftReceipt, err := s.resolveOperand(ctx, catalog, selection.Left, pivotReceipt)
	if err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, err
	}
	right, rightType, rightReceipt, err := s.resolveOperand(ctx, catalog, selection.Right, pivotReceipt)
	if err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, err
	}
	prior, err := s.collectPriorDerived(ctx, catalog, []*tableshapecap.ResolutionReceipt{leftReceipt, rightReceipt})
	if err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, err
	}
	if pivotReceipt != nil {
		prior = append([]tableshapecap.ResolutionReceipt{*pivotReceipt}, prior...)
	}
	for _, receipt := range prior {
		if receipt.Derived != nil && receipt.Derived.Output.Name == selection.OutputColumn {
			return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "DUPLICATE_OUTPUT_NAME", "derived output names must be distinct from referenced earlier outputs", nil)
		}
	}
	resultType, err := deriveTypeFact(operator.Operator, left, leftType, right, rightType, missing.PolicyID, divisionPolicy)
	if err != nil {
		return tableshapecap.DerivedResolution{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INCOMPATIBLE_DERIVED_OPERANDS", "the selected operands are incompatible for this operation", err)
	}
	return tableshapecap.DerivedResolution{
		Output:            tableshapecap.NamedOutput{Name: selection.OutputColumn, Label: selection.OutputLabel},
		PivotResolutionID: selection.PivotResolutionID,
		OperatorChoiceID:  operator.ID, Left: left, Right: right, Result: resultType,
		MissingPolicyChoiceID: missing.ID, DivisionByZeroPolicyChoiceID: divisionPolicy,
	}, resultType, prior, nil
}

func (s *Service) collectPriorDerived(ctx context.Context, catalog tableshapecap.CatalogReceipt, roots []*tableshapecap.ResolutionReceipt) ([]tableshapecap.ResolutionReceipt, error) {
	visited := make(map[string]struct{})
	visiting := make(map[string]struct{})
	ordered := make([]tableshapecap.ResolutionReceipt, 0, len(roots))
	var visit func(tableshapecap.ResolutionReceipt) error
	visit = func(receipt tableshapecap.ResolutionReceipt) error {
		if _, ok := visited[receipt.ID]; ok {
			return nil
		}
		if _, ok := visiting[receipt.ID]; ok {
			return unprocessable("table-shape-resolution", "DERIVED_REFERENCE_CYCLE", "derived resolutions cannot contain a dependency cycle", nil)
		}
		if err := receipt.Validate(); err != nil || receipt.ID == "" || receipt.ParentCatalogID != catalog.ID || receipt.Binding != catalog.Binding || receipt.Kind != tableshapecap.ResolutionDerived || receipt.Derived == nil {
			return unprocessable("table-shape-resolution", "INVALID_DERIVED_REFERENCE", "a derived operand must reference an earlier derived resolution from this catalog", err)
		}
		if len(ordered) >= maxTableShapeResolutionReferences {
			return unprocessable("table-shape-resolution", "TOO_MANY_DERIVED_REFERENCES", "the derived expression references too many earlier resolutions", nil)
		}
		visiting[receipt.ID] = struct{}{}
		for _, operand := range []tableshapecap.ResolvedOperand{receipt.Derived.Left, receipt.Derived.Right} {
			if operand.Kind != tableshapecap.ResolvedOperandResolution {
				continue
			}
			dependency, err := s.config.TableShapeCapabilities.GetResolution(ctx, catalog.Binding, catalog.ID, operand.ResolutionID)
			if err != nil {
				return unprocessable("table-shape-resolution", "DERIVED_REFERENCE_NOT_FOUND", "a derived operand must reference an earlier saved resolution from this catalog", err)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, receipt.ID)
		visited[receipt.ID] = struct{}{}
		ordered = append(ordered, receipt)
		return nil
	}
	for _, receipt := range roots {
		if receipt == nil {
			continue
		}
		if err := visit(*receipt); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func (s *Service) resolveOperand(ctx context.Context, catalog tableshapecap.CatalogReceipt, selection TableShapeOperandSelection, pivot *tableshapecap.ResolutionReceipt) (tableshapecap.ResolvedOperand, tableshapecap.TypeFact, *tableshapecap.ResolutionReceipt, error) {
	switch selection.Kind {
	case tableshapecap.ResolvedOperandCatalogChoice:
		if pivot != nil {
			for _, choice := range pivot.Pivot.DerivedOperands {
				if choice.ChoiceID == selection.ChoiceID {
					return tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: choice.ChoiceID}, choice.Type, nil, nil
				}
			}
			return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "the operand choice is not available after the selected pivot", nil)
		}
		choice, err := catalog.FindOperand(selection.ChoiceID)
		if err != nil {
			return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_CHOICE_ID", "a base operand choice is not available in this catalog", err)
		}
		column := findPublicColumn(catalog, choice.Operand.ColumnKey)
		if column == nil {
			return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, conflict("table-shape-resolution", "INVALID_TABLE_SHAPE_CAPABILITY", "the catalog operand choice has no compiler-owned column", nil, nil)
		}
		return tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: choice.ID}, tableShapeTypeFact(*column), nil, nil
	case tableshapecap.ResolvedOperandResolution:
		receipt, err := s.config.TableShapeCapabilities.GetResolution(ctx, catalog.Binding, catalog.ID, selection.ResolutionID)
		if err != nil {
			if errors.Is(err, tableshapecap.ErrNotFound) {
				return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "DERIVED_REFERENCE_NOT_FOUND", "a derived operand must reference an earlier saved resolution from this catalog", err)
			}
			return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, unavailable("table-shape-resolution", "CAPABILITY_STORE_FAILED", "a referenced derived resolution could not be loaded", err)
		}
		if err := receipt.Validate(); err != nil || receipt.ID != selection.ResolutionID || receipt.ParentCatalogID != catalog.ID || receipt.Binding != catalog.Binding || receipt.Kind != tableshapecap.ResolutionDerived || receipt.Derived == nil {
			return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_DERIVED_REFERENCE", "a derived operand must reference an earlier derived resolution from this catalog", err)
		}
		index := 0
		return tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandResolution, ResolutionID: receipt.ID, OutputIndex: &index}, receipt.Derived.Result, &receipt, nil
	case tableshapecap.ResolvedOperandLiteral:
		value := *selection.Literal
		logical := tableshapecap.LogicalInteger
		if value.Kind == tableshapecap.ScalarDecimal {
			logical = tableshapecap.LogicalDecimal
		}
		return tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandLiteral, Literal: &value}, tableshapecap.TypeFact{LogicalType: logical}, nil, nil
	default:
		return tableshapecap.ResolvedOperand{}, tableshapecap.TypeFact{}, nil, unprocessable("table-shape-resolution", "INVALID_OPERAND", "the derived operand kind is unsupported", nil)
	}
}

func deriveTypeFact(operator string, leftOperand tableshapecap.ResolvedOperand, left tableshapecap.TypeFact, rightOperand tableshapecap.ResolvedOperand, right tableshapecap.TypeFact, missingPolicy, divisionPolicy string) (tableshapecap.TypeFact, error) {
	if left.LogicalType != tableshapecap.LogicalInteger && left.LogicalType != tableshapecap.LogicalDecimal || right.LogicalType != tableshapecap.LogicalInteger && right.LogicalType != tableshapecap.LogicalDecimal {
		return tableshapecap.TypeFact{}, fmt.Errorf("derived operands must be numeric")
	}
	operation := map[string]unit.ArithmeticOperation{
		"ADD": unit.ArithmeticAdd, "SUBTRACT": unit.ArithmeticSubtract,
		"MULTIPLY": unit.ArithmeticMultiply, "DIVIDE": unit.ArithmeticDivide,
	}[operator]
	if operation == 0 {
		return tableshapecap.TypeFact{}, fmt.Errorf("unknown derived operator %q", operator)
	}
	leftUnit, err := parseArithmeticUnit(left.UnitIdentity)
	if err != nil {
		return tableshapecap.TypeFact{}, fmt.Errorf("left operand unit: %w", err)
	}
	rightUnit, err := parseArithmeticUnit(right.UnitIdentity)
	if err != nil {
		return tableshapecap.TypeFact{}, fmt.Errorf("right operand unit: %w", err)
	}
	resolvedUnit, err := unit.ResolveArithmeticUnit(operation, arithmeticOperand(leftOperand.Kind, leftUnit), arithmeticOperand(rightOperand.Kind, rightUnit))
	if err != nil {
		return tableshapecap.TypeFact{}, err
	}
	result := tableshapecap.TypeFact{Nullable: missingPolicy == "PROPAGATE_NULL" || operator == "DIVIDE" && divisionPolicy == "NULL"}
	if resolvedUnit != nil {
		result.UnitIdentity, err = unitIdentityString(resolvedUnit)
		if err != nil {
			return tableshapecap.TypeFact{}, err
		}
	}
	switch operator {
	case "ADD", "SUBTRACT":
		result.LogicalType = tableshapecap.LogicalInteger
		if left.LogicalType == tableshapecap.LogicalDecimal || right.LogicalType == tableshapecap.LogicalDecimal {
			result.LogicalType = tableshapecap.LogicalDecimal
		}
	case "MULTIPLY":
		result.LogicalType = tableshapecap.LogicalInteger
		if left.LogicalType == tableshapecap.LogicalDecimal || right.LogicalType == tableshapecap.LogicalDecimal {
			result.LogicalType = tableshapecap.LogicalDecimal
		}
	case "DIVIDE":
		result.LogicalType = tableshapecap.LogicalDecimal
	}
	if err := result.Validate(); err != nil {
		return tableshapecap.TypeFact{}, err
	}
	return result, nil
}

func parseArithmeticUnit(value string) (*unit.UnitIdentity, error) {
	if value == "" {
		return nil, nil
	}
	var identity unit.UnitIdentity
	if err := json.Unmarshal([]byte(value), &identity); err != nil || !identity.Valid() {
		return nil, fmt.Errorf("invalid normalized unit identity")
	}
	return &identity, nil
}

func arithmeticOperand(kind tableshapecap.ResolvedOperandKind, identity *unit.UnitIdentity) unit.ArithmeticOperand {
	if kind == tableshapecap.ResolvedOperandLiteral {
		return unit.LiteralArithmeticOperand()
	}
	return unit.ColumnArithmeticOperand(identity)
}
