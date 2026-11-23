package execution

import (
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type TableShapeExclusionStatus string

const (
	TableShapeExclusionComplete          TableShapeExclusionStatus = "COMPLETE"
	TableShapeExclusionIncomplete        TableShapeExclusionStatus = "INCOMPLETE"
	TableShapeExclusionUnsupported       TableShapeExclusionStatus = "UNSUPPORTED"
	TableShapeExclusionNoExclusionPolicy TableShapeExclusionStatus = "NO_EXCLUSION_POLICY"
)

type TableShapeExclusionRequest struct {
	Output string
	Offset int
	Limit  int
}

type TableShapeSourceIdentity struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
}

type TableShapeExclusion struct {
	SourceIdentity *TableShapeSourceIdentity `json:"sourceIdentity,omitempty"`
	Category       CategoryValue             `json:"category"`
	CategoryType   string                    `json:"categoryType"`
	OutputRowID    string                    `json:"outputRowId"`
	Reason         string                    `json:"reason"`
	OmissionCode   string                    `json:"omissionCode,omitempty"`
}

type TableShapeExclusionResult struct {
	Status     TableShapeExclusionStatus
	Exclusions []TableShapeExclusion
	Complete   bool
	HasMore    bool
	NextOffset int
}

var errTableShapeExclusionOverflow = errors.New("table-shape exclusion page overflow")

func parseTableShapeExclusion(compiled compiler.CompiledTableShapeExclusionQuery, row map[string]any) (TableShapeExclusion, error) {
	categoryPresent, ok := row[compiled.CategoryPresentField].(bool)
	if !ok {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category presence")
	}
	categoryValue, exists := row[compiled.CategoryValueField]
	if !exists {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion is missing category value")
	}
	categoryType, ok := row[compiled.CategoryTypeField].(string)
	if !ok || categoryType == "" {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category type")
	}
	rowID, ok := row[compiled.OutputRowIDField].(string)
	if !ok || rowID == "" {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid output row identity")
	}
	reason, ok := row[compiled.ReasonField].(string)
	if !ok || reason != ir.PhysicalTableShapeExclusionReasonUnlistedCategory {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid reason")
	}
	identityStatus, ok := row[compiled.IdentityStatusField].(string)
	if !ok {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid source identity status")
	}
	result := TableShapeExclusion{
		Category:     CategoryValue{Present: categoryPresent, Value: categoryValue},
		CategoryType: categoryType, OutputRowID: rowID, Reason: reason,
	}
	switch identityStatus {
	case ir.PhysicalTableShapeExclusionIdentityExact:
		resourceType, typeOK := row[compiled.ResourceTypeField].(string)
		resourceID, idOK := row[compiled.ResourceIDField].(string)
		if !typeOK || resourceType == "" || !idOK || resourceID == "" {
			return TableShapeExclusion{}, fmt.Errorf("exact table-shape source identity is incomplete")
		}
		result.SourceIdentity = &TableShapeSourceIdentity{ResourceType: resourceType, ResourceID: resourceID}
	case ir.PhysicalTableShapeExclusionIdentityUnavailable:
		omission, omissionOK := row[compiled.OmissionField].(string)
		if !omissionOK || omission != ir.PhysicalTableShapeExclusionSourceIdentityUnavailable {
			return TableShapeExclusion{}, fmt.Errorf("table-shape source identity omission is invalid")
		}
		result.OmissionCode = omission
	default:
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has unknown source identity status %q", identityStatus)
	}
	return result, nil
}
