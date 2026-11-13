package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
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

func (e *Engine) TableShapeExclusions(ctx context.Context, resolved Resolved, request TableShapeExclusionRequest) (TableShapeExclusionResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion output is required")
	}
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != request.Output {
			continue
		}
		compiled, err := compiler.CompileTableShapeExclusions(e.compileTarget, output, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			var refusal *compiler.TableShapeExclusionRefusal
			if errors.As(err, &refusal) {
				switch refusal.Code {
				case compiler.TableShapeExclusionUnsupported:
					return TableShapeExclusionResult{Status: TableShapeExclusionUnsupported, Exclusions: []TableShapeExclusion{}}, nil
				case compiler.TableShapeExclusionNoPolicy:
					return TableShapeExclusionResult{Status: TableShapeExclusionNoExclusionPolicy, Exclusions: []TableShapeExclusion{}}, nil
				}
			}
			if errors.Is(err, postgresrender.ErrUnsupportedPostgresPlan) {
				return TableShapeExclusionResult{Status: TableShapeExclusionUnsupported, Exclusions: []TableShapeExclusion{}}, nil
			}
			return TableShapeExclusionResult{}, err
		}
		return e.TableShapeExclusionsCompiled(ctx, compiled)
	}
	return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion output %q was not found", request.Output)
}

func (e *Engine) TableShapeExclusionsCompiled(ctx context.Context, compiled compiler.CompiledTableShapeExclusionQuery) (TableShapeExclusionResult, error) {
	isPostgreSQL := false
	switch compiled.Command.Backend() {
	case command.CompiledBackendAQL:
	case command.CompiledBackendPostgreSQL:
		isPostgreSQL = true
	}
	if e == nil || e.sourceRows == nil {
		if isPostgreSQL {
			return TableShapeExclusionResult{Status: TableShapeExclusionUnsupported, Exclusions: []TableShapeExclusion{}}, nil
		}
		return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion query executor is required")
	}
	if !isPostgreSQL && compiled.Command.Backend() != command.CompiledBackendAQL ||
		compiled.Limit <= 0 || compiled.Offset < 0 || compiled.ResourceTypeField == "" || compiled.ResourceIDField == "" || compiled.CategoryPresentField == "" || compiled.CategoryValueField == "" || compiled.CategoryTypeField == "" || compiled.OutputRowIDField == "" || compiled.ReasonField == "" || compiled.IdentityStatusField == "" || compiled.OmissionField == "" {
		return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion query is missing typed result metadata")
	}
	parse := parseTableShapeExclusion
	request := RowsRequest{Command: compiled.Command, BatchSize: e.batchSize}
	if isPostgreSQL {
		parse = parsePostgreSQLTableShapeExclusion
		request.DeclaredColumns = []string{
			compiled.ResourceTypeField,
			compiled.ResourceIDField,
			compiled.IdentityStatusField,
			compiled.CategoryValueField,
			compiled.CategoryPresentField,
			compiled.CategoryTypeField,
			compiled.OutputRowIDField,
			compiled.ReasonField,
			compiled.OmissionField,
		}
	}
	rows := make([]TableShapeExclusion, 0, compiled.Limit+1)
	tooMany := false
	err := e.sourceRows(ctx, request, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(rows) == compiled.Limit+1 {
			tooMany = true
			return errTableShapeExclusionOverflow
		}
		parsed, parseErr := parse(compiled, row)
		if parseErr != nil {
			return parseErr
		}
		rows = append(rows, parsed)
		return nil
	})
	if errors.Is(err, errTableShapeExclusionOverflow) {
		err = nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return TableShapeExclusionResult{Status: TableShapeExclusionIncomplete, Exclusions: []TableShapeExclusion{}, Complete: false, NextOffset: compiled.Offset}, nil
		}
		if isPostgreSQL && errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			return TableShapeExclusionResult{Status: TableShapeExclusionUnsupported, Exclusions: []TableShapeExclusion{}}, nil
		}
		return TableShapeExclusionResult{}, fmt.Errorf("execute table-shape exclusions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return TableShapeExclusionResult{Status: TableShapeExclusionIncomplete, Exclusions: []TableShapeExclusion{}, Complete: false, NextOffset: compiled.Offset}, nil
	}
	hasMore := tooMany || len(rows) > compiled.Limit
	if hasMore {
		rows = rows[:compiled.Limit]
	}
	complete := !hasMore
	for _, exclusion := range rows {
		if exclusion.OmissionCode != "" {
			complete = false
		}
	}
	status := TableShapeExclusionComplete
	if !complete {
		status = TableShapeExclusionIncomplete
	}
	nextOffset := compiled.Offset
	if hasMore {
		nextOffset += len(rows)
	}
	return TableShapeExclusionResult{
		Status: status, Exclusions: rows, Complete: complete, HasMore: hasMore, NextOffset: nextOffset,
	}, nil
}

var errTableShapeExclusionOverflow = errors.New("table-shape exclusion page overflow")

type tableShapeExclusionEvidence struct {
	present          bool
	categoryValue    any
	categoryType     string
	categoryTypeOK   bool
	outputRowID      string
	outputRowIDOK    bool
	reason           string
	reasonOK         bool
	identityStatus   string
	identityStatusOK bool
	sourceIdentityOK bool
	resourceType     string
	resourceID       string
	omission         string
	omissionOK       bool
}

func tableShapeExclusionFromEvidence(evidence tableShapeExclusionEvidence) (TableShapeExclusion, error) {
	if !evidence.categoryTypeOK || evidence.categoryType == "" {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category type")
	}
	if !evidence.outputRowIDOK || evidence.outputRowID == "" {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid output row identity")
	}
	if !evidence.reasonOK || evidence.reason != ir.PhysicalTableShapeExclusionReasonUnlistedCategory {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid reason")
	}
	if !evidence.identityStatusOK {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid source identity status")
	}
	result := TableShapeExclusion{
		Category:     CategoryValue{Present: evidence.present, Value: evidence.categoryValue},
		CategoryType: evidence.categoryType, OutputRowID: evidence.outputRowID, Reason: evidence.reason,
	}
	switch evidence.identityStatus {
	case ir.PhysicalTableShapeExclusionIdentityExact:
		if !evidence.sourceIdentityOK || evidence.resourceType == "" || evidence.resourceID == "" {
			return TableShapeExclusion{}, fmt.Errorf("exact table-shape source identity is incomplete")
		}
		result.SourceIdentity = &TableShapeSourceIdentity{ResourceType: evidence.resourceType, ResourceID: evidence.resourceID}
	case ir.PhysicalTableShapeExclusionIdentityUnavailable:
		if !evidence.omissionOK || evidence.omission != ir.PhysicalTableShapeExclusionSourceIdentityUnavailable {
			return TableShapeExclusion{}, fmt.Errorf("table-shape source identity omission is invalid")
		}
		result.OmissionCode = evidence.omission
	default:
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has unknown source identity status %q", evidence.identityStatus)
	}
	return result, nil
}

func parseTableShapeExclusion(compiled compiler.CompiledTableShapeExclusionQuery, row map[string]any) (TableShapeExclusion, error) {
	categoryPresent, ok := row[compiled.CategoryPresentField].(bool)
	if !ok {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category presence")
	}
	categoryValue, exists := row[compiled.CategoryValueField]
	if !exists {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion is missing category value")
	}
	categoryType, categoryTypeOK := row[compiled.CategoryTypeField].(string)
	outputRowID, outputRowIDOK := row[compiled.OutputRowIDField].(string)
	reason, reasonOK := row[compiled.ReasonField].(string)
	identityStatus, identityStatusOK := row[compiled.IdentityStatusField].(string)
	resourceType, resourceTypeOK := row[compiled.ResourceTypeField].(string)
	resourceID, resourceIDOK := row[compiled.ResourceIDField].(string)
	omission, omissionOK := row[compiled.OmissionField].(string)
	return tableShapeExclusionFromEvidence(tableShapeExclusionEvidence{
		present:          categoryPresent,
		categoryValue:    categoryValue,
		categoryType:     categoryType,
		categoryTypeOK:   categoryTypeOK,
		outputRowID:      outputRowID,
		outputRowIDOK:    outputRowIDOK,
		reason:           reason,
		reasonOK:         reasonOK,
		identityStatus:   identityStatus,
		identityStatusOK: identityStatusOK,
		sourceIdentityOK: resourceTypeOK && resourceIDOK,
		resourceType:     resourceType,
		resourceID:       resourceID,
		omission:         omission,
		omissionOK:       omissionOK,
	})
}

func parsePostgreSQLTableShapeExclusion(compiled compiler.CompiledTableShapeExclusionQuery, row map[string]any) (TableShapeExclusion, error) {
	present, ok := decodePostgreSQLPresence(row[compiled.CategoryPresentField])
	if !ok {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category presence")
	}
	categoryValue, exists := row[compiled.CategoryValueField]
	if !exists {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion is missing category value")
	}
	categoryType, categoryTypeOK := row[compiled.CategoryTypeField].(string)
	outputRowID, outputRowIDOK := row[compiled.OutputRowIDField].(string)
	reason, reasonOK := row[compiled.ReasonField].(string)
	identityStatus, identityStatusOK := row[compiled.IdentityStatusField].(string)
	resourceType, resourceTypeOK := decodePostgreSQLEvidenceText(row[compiled.ResourceTypeField])
	resourceID, resourceIDOK := decodePostgreSQLEvidenceText(row[compiled.ResourceIDField])
	omission, omissionOK := row[compiled.OmissionField].(string)
	return tableShapeExclusionFromEvidence(tableShapeExclusionEvidence{
		present:          present,
		categoryValue:    categoryValue,
		categoryType:     categoryType,
		categoryTypeOK:   categoryTypeOK,
		outputRowID:      outputRowID,
		outputRowIDOK:    outputRowIDOK,
		reason:           reason,
		reasonOK:         reasonOK,
		identityStatus:   identityStatus,
		identityStatusOK: identityStatusOK,
		sourceIdentityOK: resourceTypeOK && resourceIDOK,
		resourceType:     resourceType,
		resourceID:       resourceID,
		omission:         omission,
		omissionOK:       omissionOK,
	})
}

func decodePostgreSQLPresence(value any) (bool, bool) {
	raw, ok := value.(json.RawMessage)
	if !ok {
		return false, false
	}
	switch string(raw) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func decodePostgreSQLEvidenceText(value any) (string, bool) {
	raw, ok := value.(json.RawMessage)
	if !ok || len(raw) == 0 {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || text == "" {
		return "", false
	}
	return text, true
}
