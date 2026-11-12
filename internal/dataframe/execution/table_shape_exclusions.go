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
			// pg-exec-12: the selected PostgreSQL target honors its surface to
			// the refusal. A shape outside the registered evidence slice fails
			// closed with the renderer's typed ErrUnsupportedPostgresPlan; the
			// honest execution-layer report is UNSUPPORTED with zero statements
			// executed, so the lifecycle keeps its distinct-status contract (the
			// no-policy refusal above stays separate). The AQL path can never
			// produce this sentinel, so the Arango voice is untouched.
			if errors.Is(err, postgresrender.ErrUnsupportedPostgresPlan) {
				return TableShapeExclusionResult{Status: TableShapeExclusionUnsupported, Exclusions: []TableShapeExclusion{}}, nil
			}
			return TableShapeExclusionResult{}, err
		}
		return e.TableShapeExclusionsCompiled(ctx, compiled)
	}
	return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion output %q was not found", request.Output)
}

// TableShapeExclusionsCompiled is the narrow execution seam for callers and
// tests holding the compiler-produced canonical diagnostic query.
//
// pg-exec-12 gives the seam an exhaustive backend switch over the tagged
// command. The AQL arm is byte-own to Arango and unchanged. The PostgreSQL
// arm consumes the pg-sql-26 evidence terminal: the nine frozen evidence
// fields become the declared result column contract (in the renderer's
// c0..c8 output order) that the backend-tagged row adapter requires, and
// rows are parsed with the PostgreSQL dialect parser, which keeps the AQL
// parser's violation set and preserves the missing / JSON-null / SQL-NULL
// three-state the adapter decodes. Honest results: a missing PostgreSQL row
// port or a port reporting its backend unregistered is the capability gap
// UNSUPPORTED; a backend-mismatch routing error or any other port failure is
// a hard error, never a complete or complete-empty report; cancellation
// stays INCOMPLETE at the requested offset.
func (e *Engine) TableShapeExclusionsCompiled(ctx context.Context, compiled compiler.CompiledTableShapeExclusionQuery) (TableShapeExclusionResult, error) {
	isPostgreSQL := false
	switch compiled.Command.Backend() {
	case command.CompiledBackendAQL:
	case command.CompiledBackendPostgreSQL:
		isPostgreSQL = true
	}
	if e == nil || e.sourceRows == nil {
		if isPostgreSQL {
			// Capability gap, not a routing defect: no port serves the
			// selected PostgreSQL command and there is no AQL fallback.
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
		// The contract is the nine frozen evidence fields in the renderer's
		// output order; the adapter refuses any server column drift before
		// the first row is visited.
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
			// The composition registered no adapter for the command's tag: a
			// capability gap the command package explicitly reports for
			// unsupported backends, never a complete-empty evidence report.
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

// parsePostgreSQLTableShapeExclusion is the PostgreSQL dialect arm of the
// evidence parser. It keeps the AQL parser's violation set exactly — the
// same messages, the same no-partial-success law — and differs only where
// the backend's result decoding differs: the jsonb-extracted presence and
// identity members arrive as json.RawMessage (decoded by the helpers below),
// the text-typed members arrive as driver-decoded Go strings, and the
// category value is passed through untouched so the missing (SQL NULL),
// explicit JSON null, and value-carrying three-state stays distinguishable
// to the lifecycle.
func parsePostgreSQLTableShapeExclusion(compiled compiler.CompiledTableShapeExclusionQuery, row map[string]any) (TableShapeExclusion, error) {
	present, ok := decodePostgreSQLPresence(row[compiled.CategoryPresentField])
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
		Category:     CategoryValue{Present: present, Value: categoryValue},
		CategoryType: categoryType, OutputRowID: rowID, Reason: reason,
	}
	switch identityStatus {
	case ir.PhysicalTableShapeExclusionIdentityExact:
		resourceType, typeOK := decodePostgreSQLEvidenceText(row[compiled.ResourceTypeField])
		resourceID, idOK := decodePostgreSQLEvidenceText(row[compiled.ResourceIDField])
		if !typeOK || !idOK {
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

// decodePostgreSQLPresence decodes the jsonb presence companion into its
// boolean. Only the exact jsonb booleans survive: json.Unmarshal accepts a
// JSON null into a bool target as a silent no-op, so the acceptance is by
// exact bytes ("true"/"false", the compact jsonb rendering the terminal
// emits). SQL NULL, a JSON null, a quoted string, a number, and any
// driver-native Go value are all evidence violations, so a malformed
// companion can never silently mark rows present or missing.
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

// decodePostgreSQLEvidenceText decodes one jsonb-extracted identity member
// into a non-empty JSON string. A missing key, SQL NULL, a JSON null, a
// non-string JSON value, and an empty string all fail: the EXACT branch may
// only carry a complete identity.
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
