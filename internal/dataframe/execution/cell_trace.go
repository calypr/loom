package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const defaultCellTraceWitnessRows int64 = 1_000_000

// CellTraceStatus describes what the canonical output plan established for
// one published cell. Incomplete is reserved for a bounded trace that could
// not reach the requested row; it must never be presented as absence.
type CellTraceStatus string

const (
	CellTraceValue        CellTraceStatus = "VALUE"
	CellTraceNoMatch      CellTraceStatus = "NO_MATCH"
	CellTraceRecordedNull CellTraceStatus = "RECORDED_NULL"
	CellTraceAmbiguous    CellTraceStatus = "AMBIGUOUS"
	CellTraceInvalidType  CellTraceStatus = "INVALID_TYPE"
	CellTraceInvalidUnit  CellTraceStatus = "INCOMPATIBLE_UNIT"
	CellTraceIncomplete   CellTraceStatus = "INCOMPLETE"
)

type CellTraceRequest struct {
	Output         string
	RowID          string
	Column         string
	Offset         int
	Limit          int
	MaxWitnessRows int64
}

type CellTraceContribution struct {
	ResourceType   string `json:"resourceType,omitempty"`
	ResourceID     string `json:"resourceId,omitempty"`
	InputStageID   string `json:"inputStageId,omitempty"`
	InputColumnID  string `json:"inputColumnId,omitempty"`
	InputColumn    string `json:"inputColumn,omitempty"`
	OutputStageID  string `json:"outputStageId,omitempty"`
	OutputColumnID string `json:"outputColumnId,omitempty"`
	OutputColumn   string `json:"outputColumn,omitempty"`
	FinalStageID   string `json:"finalStageId,omitempty"`
	ConstructionID string `json:"constructionId,omitempty"`
	Operation      string `json:"operation,omitempty"`
	Value          any    `json:"value"`
}

type CellTraceResult struct {
	RowID         string
	Column        string
	Value         any
	Status        CellTraceStatus
	Contributions []CellTraceContribution
	HasMore       bool
	NextOffset    int
	OmissionCode  string
	Complete      bool
}

var errCellTraceFound = errors.New("cell trace row found")
var errCellTraceWitnessLimit = errors.New("cell trace witness limit exceeded")

// ErrCellTraceRowNotFound marks a client-supplied row ID that matched no
// published row in an existing output; the authoring transport presents it as
// a typed not-found, and only drift between bundle and compiled outputs can
// keep it from being resolved upstream.
var ErrCellTraceRowNotFound = errors.New("published cell trace row was not found")

// CellTrace executes a diagnostic terminal compiled from the same resolved
// output as publication. It locates a published row by its stable opaque ID;
// callers never submit FHIR paths or expressions for interpretation here.
func (e *Engine) CellTrace(ctx context.Context, resolved Resolved, request CellTraceRequest) (CellTraceResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return CellTraceResult{}, fmt.Errorf("cell trace output is required")
	}
	compiled, err := compileCellTraceOutput(e.compileTarget, resolved, request)
	if err != nil {
		return CellTraceResult{}, err
	}
	return e.cellTraceCompiled(ctx, compiled, request)
}

// CellTraceCompiled is the narrow execution seam used by focused tests and
// specialized adapters that already hold the compiler result.
func (e *Engine) CellTraceCompiled(ctx context.Context, compiled compiler.CompiledCellTraceQuery, request CellTraceRequest) (CellTraceResult, error) {
	return e.cellTraceCompiled(ctx, compiled, request)
}

func (e *Engine) cellTraceCompiled(ctx context.Context, compiled compiler.CompiledCellTraceQuery, request CellTraceRequest) (CellTraceResult, error) {
	if e == nil || e.sourceRows == nil {
		return CellTraceResult{}, fmt.Errorf("cell trace query executor is required")
	}
	rowID := strings.TrimSpace(request.RowID)
	column := strings.TrimSpace(request.Column)
	if rowID == "" || column == "" {
		return CellTraceResult{}, fmt.Errorf("cell trace row ID and column are required")
	}
	if compiled.ExplicitIdentityObject {
		canonical, err := canonicalCellTraceIdentityJSON(rowID)
		if err != nil {
			return CellTraceResult{}, fmt.Errorf("cell trace row ID has an invalid object identity: %w", err)
		}
		rowID = canonical
	}
	maxWitness := request.MaxWitnessRows
	if maxWitness <= 0 {
		maxWitness = defaultCellTraceWitnessRows
	}
	if compiled.ValueColumn == "" || compiled.StatusColumn == "" || compiled.ContributionsColumn == "" || (compiled.IdentityPartsColumn == "" && compiled.ExplicitIdentityColumn == "") ||
		(compiled.ExplicitIdentityObject && compiled.ExplicitIdentityColumn == "") {
		return CellTraceResult{}, fmt.Errorf("cell trace query is missing typed result columns")
	}

	// pg-exec-09: exhaustive backend dispatch over the tagged command, keyed
	// the same way category scans (pg-exec-06) and table-shape exclusions
	// (pg-exec-12) dispatch. The AQL arm below stays byte-identical to the
	// pre-dispatch executor, including its contract-free row request and its
	// native-shape parsers; the PostgreSQL arm carries the registered
	// renderer's sealed six-column contract so the row adapter can enforce
	// statement field descriptions before the first row is visited, and
	// decodes the adapter's raw-JSON column shapes. An unregistered or zero
	// tag is refused by name before any port call. SQL generation stays in
	// the pg-sql-24 renderer; this switch executes what it compiles.
	dialect := cellTraceAQLDialect
	var declaredColumns []string
	switch compiled.Command.Backend() {
	case command.CompiledBackendPostgreSQL:
		identityColumn := compiled.IdentityPartsColumn
		if compiled.ExplicitIdentityColumn != "" {
			identityColumn = compiled.ExplicitIdentityColumn
		}
		// The contract is the sealed renderer order
		// (render/postgres/cell_trace.go outputColumns): value, contributions,
		// identity (parts or explicit, the same pick law cellTraceRowIdentity
		// applies), status, hasMore, omission. A duplicated name would make the
		// adapter's positional check meaningless, so the contract must also be
		// pairwise distinct — the row-lineage declared-contract law.
		declaredColumns = []string{compiled.ValueColumn, compiled.ContributionsColumn, identityColumn,
			compiled.StatusColumn, compiled.HasMoreColumn, compiled.OmissionColumn}
		for i := range declaredColumns {
			for j := i + 1; j < len(declaredColumns); j++ {
				if declaredColumns[i] == declaredColumns[j] {
					return CellTraceResult{}, fmt.Errorf("cell trace query is missing typed result columns")
				}
			}
		}
		dialect = cellTracePostgreSQLDialect
	case command.CompiledBackendAQL:
	default:
		return CellTraceResult{}, fmt.Errorf("%w: cell trace command backend %q is not registered",
			command.ErrCompiledCommandBackendNotRegistered, compiled.Command.Backend())
	}
	return e.scanCellTraceEvidence(ctx, compiled, request, rowID, column, maxWitness, declaredColumns, dialect)
}

// cellTraceDialect carries the per-backend row decode law over the shared
// witness-bound scan. The AQL codec is today's parser verbatim; the
// PostgreSQL codec decodes the adapter's raw-JSON column shapes and then
// re-enters the same identity-digest and contribution validation, so both
// dialects converge on one published evidence contract.
type cellTraceDialect struct {
	identity func(compiler.CompiledCellTraceQuery, map[string]any) (string, error)
	parse    func(compiler.CompiledCellTraceQuery, CellTraceRequest, map[string]any) (CellTraceResult, error)
}

var cellTraceAQLDialect = cellTraceDialect{identity: cellTraceRowIdentity, parse: parseCellTraceRow}

var cellTracePostgreSQLDialect = cellTraceDialect{identity: cellTracePostgreSQLIdentity, parse: parseCellTracePostgreSQLRow}

// scanCellTraceEvidence is the shared witness-bound scan the dispatch above
// reaches with the selected dialect: the one row port call, the witness-row
// cap, the identity match, and the incomplete/failure/not-found epilogue are
// dialect-neutral. declaredColumns is nil on the AQL arm, reproducing today's
// contract-free request byte for byte.
func (e *Engine) scanCellTraceEvidence(ctx context.Context, compiled compiler.CompiledCellTraceQuery, request CellTraceRequest, rowID, column string, maxWitness int64, declaredColumns []string, dialect cellTraceDialect) (CellTraceResult, error) {
	var result CellTraceResult
	witnessRows := int64(0)
	err := e.sourceRows(ctx, RowsRequest{Command: compiled.Command, DeclaredColumns: declaredColumns, BatchSize: e.batchSize}, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		witnessRows++
		if witnessRows > maxWitness {
			return errCellTraceWitnessLimit
		}
		identity, identityErr := dialect.identity(compiled, row)
		if identityErr != nil {
			return identityErr
		}
		if identity != rowID {
			return nil
		}
		parsed, parseErr := dialect.parse(compiled, request, row)
		if parseErr != nil {
			return parseErr
		}
		result = parsed
		return errCellTraceFound
	})
	if errors.Is(err, errCellTraceFound) {
		return result, nil
	}
	if errors.Is(err, errCellTraceWitnessLimit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CellTraceResult{RowID: rowID, Column: column, Status: CellTraceIncomplete, OmissionCode: "TRACE_WITNESS_INCOMPLETE", Complete: false}, nil
	}
	if err != nil {
		if status, omission, ok := cellTraceFailureStatus(err); ok {
			return CellTraceResult{RowID: rowID, Column: column, Status: status, OmissionCode: omission, Contributions: []CellTraceContribution{}, Complete: true}, nil
		}
		return CellTraceResult{}, fmt.Errorf("execute cell trace: %w", err)
	}
	return CellTraceResult{}, dataframeerrors.Wrap(ErrCellTraceRowNotFound, dataframeerrors.CodeInvalidRequest, fmt.Sprintf("published row %q was not found in output %q", rowID, request.Output), dataframeerrors.WithDetails(map[string]any{"rowId": rowID, "output": request.Output}))
}

func compileCellTraceOutput(target compiler.Target, resolved Resolved, request CellTraceRequest) (compiler.CompiledCellTraceQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == request.Output {
			return compiler.CompileCellTraceOutput(target, output, request.Column, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		}
	}
	return compiler.CompiledCellTraceQuery{}, fmt.Errorf("cell trace output %q was not found", request.Output)
}

func cellTraceRowIdentity(query compiler.CompiledCellTraceQuery, row map[string]any) (string, error) {
	if query.ExplicitIdentityColumn != "" {
		value, ok := row[query.ExplicitIdentityColumn]
		if !ok || value == nil {
			return "", fmt.Errorf("cell trace row has an invalid explicit identity")
		}
		if query.ExplicitIdentityObject {
			return canonicalCellTraceObjectValue(value)
		}
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "", fmt.Errorf("cell trace row has an invalid explicit identity")
		}
		return fmt.Sprint(value), nil
	}
	parts, ok := row[query.IdentityPartsColumn].([]any)
	if !ok {
		return "", fmt.Errorf("cell trace row has an invalid default identity shape")
	}
	return cellTraceIdentityPartsDigest(parts, query)
}

// cellTraceIdentityPartsDigest applies the shared default-identity law: the
// ordered parts must match the compiled RowIdentity field list, carry no
// null, and hash over their canonical JSON encoding — byte-for-byte the law
// ensureStableRowIdentity uses to mint the preview's published __loom_row_id,
// so a preview-published ID resolves on either dialect once the adapter's
// column shapes are decoded to the same natives.
func cellTraceIdentityPartsDigest(parts []any, query compiler.CompiledCellTraceQuery) (string, error) {
	if query.RowIdentity == nil || len(parts) == 0 || len(parts) != len(query.RowIdentity.Fields) {
		return "", fmt.Errorf("cell trace row has an invalid default identity shape")
	}
	for _, part := range parts {
		if part == nil {
			return "", fmt.Errorf("cell trace row has a null default identity part")
		}
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("encode cell trace row identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// cellTracePostgreSQLIdentity resolves the identity of one published row on
// the PostgreSQL arm. The adapter delivers jsonb columns as raw JSON bytes
// (decodeColumn), so the identity-parts array and the explicit identity
// scalar/object arrive as json.RawMessage; both are decoded to natives
// BEFORE the shared digest/normalization law so digests converge with the
// preview side. The dual tolerance ([]any as well) keeps the pg-sql-24
// convergence contract — a PostgreSQL-tagged command served AQL-shaped rows
// through the shared port — byte-verbatim green.
func cellTracePostgreSQLIdentity(query compiler.CompiledCellTraceQuery, row map[string]any) (string, error) {
	if query.ExplicitIdentityColumn != "" {
		value, ok := row[query.ExplicitIdentityColumn]
		if raw, isRaw := value.(json.RawMessage); isRaw {
			decoded, err := decodePostgreSQLCellTraceJSON(raw)
			if err != nil {
				return "", fmt.Errorf("cell trace row has an invalid explicit identity: %w", err)
			}
			value, ok = decoded, true
		}
		if !ok || value == nil {
			return "", fmt.Errorf("cell trace row has an invalid explicit identity")
		}
		if query.ExplicitIdentityObject {
			return canonicalCellTraceObjectValue(value)
		}
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "", fmt.Errorf("cell trace row has an invalid explicit identity")
		}
		return fmt.Sprint(value), nil
	}
	parts, err := cellTracePostgreSQLJSONParts(row[query.IdentityPartsColumn])
	if err != nil {
		return "", fmt.Errorf("cell trace row has an invalid default identity shape")
	}
	return cellTraceIdentityPartsDigest(parts, query)
}

// cellTracePostgreSQLJSONParts reads one published jsonb array column
// (identity parts or the contributor page). The registered renderer always
// COALESCEs these pages to a JSON array — jsonb_build_array / '[]'::jsonb —
// so SQL NULL, empty bytes, malformed JSON, the JSON null scalar, and any
// non-array value are contract violations refused fail-closed with no value,
// never a silent empty page (the row-lineage contributor-page law). A native
// []any is tolerated because the convergence contract serves adapter-free
// rows through the same port.
func cellTracePostgreSQLJSONParts(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case json.RawMessage:
		decoded, err := decodePostgreSQLCellTraceJSON(typed)
		if err != nil {
			return nil, err
		}
		items, ok := decoded.([]any)
		if !ok {
			return nil, fmt.Errorf("cell trace row carries a non-array JSON value where an array page is published")
		}
		return items, nil
	default:
		return nil, fmt.Errorf("cell trace row is missing its published JSON array")
	}
}

// decodePostgreSQLCellTraceJSON fail-closes one adapter-delivered raw JSON
// column: empty or malformed bytes are a typed error, never a silent nil.
// A stored JSON null scalar legitimately decodes to Go nil.
func decodePostgreSQLCellTraceJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("cell trace row carries invalid raw JSON bytes")
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("cell trace row carries invalid raw JSON bytes: %w", err)
	}
	return decoded, nil
}

// The compiler publishes a singleton __loom_row_id for composed cohort
// outputs; its cohort stage defines that identity as a required object. Other
// construction identities retain their scalar or identity-parts metadata.
func canonicalCellTraceObjectValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode cell trace object identity: %w", err)
	}
	return canonicalCellTraceIdentityJSON(string(encoded))
}

func canonicalCellTraceIdentityJSON(raw string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	start, err := decoder.Token()
	if err != nil {
		return "", fmt.Errorf("decode object identity: %w", err)
	}
	if start != json.Delim('{') {
		return "", fmt.Errorf("object identity must be a JSON object")
	}
	parts := make(map[string]string, 2)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		if keyErr != nil {
			return "", fmt.Errorf("decode object identity key: %w", keyErr)
		}
		key, ok := keyToken.(string)
		if !ok || (key != "group_revision_id" && key != "group_id") {
			return "", fmt.Errorf("object identity contains an unsupported key")
		}
		if _, exists := parts[key]; exists {
			return "", fmt.Errorf("object identity repeats key %q", key)
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("decode object identity key %q: %w", key, err)
		}
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("object identity key %q is empty", key)
		}
		parts[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		if err != nil {
			return "", fmt.Errorf("decode object identity end: %w", err)
		}
		return "", fmt.Errorf("object identity has an invalid terminator")
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("object identity contains trailing JSON")
		}
		return "", fmt.Errorf("decode trailing object identity: %w", err)
	}
	if len(parts) != 2 {
		return "", fmt.Errorf("object identity must contain group revision and group keys")
	}
	canonical, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("canonicalize object identity: %w", err)
	}
	return string(canonical), nil
}

func parseCellTraceRow(query compiler.CompiledCellTraceQuery, request CellTraceRequest, row map[string]any) (CellTraceResult, error) {
	status, ok := row[query.StatusColumn].(string)
	if !ok || !validCellTraceStatus(CellTraceStatus(status)) {
		return CellTraceResult{}, fmt.Errorf("cell trace row has an invalid status")
	}
	contributions, err := parseCellTraceContributions(row[query.ContributionsColumn])
	if err != nil {
		return CellTraceResult{}, err
	}
	hasMore, _ := row[query.HasMoreColumn].(bool)
	omission, _ := row[query.OmissionColumn].(string)
	nextOffset := 0
	if hasMore {
		nextOffset = request.Offset + len(contributions)
	}
	return CellTraceResult{
		RowID: strings.TrimSpace(request.RowID), Column: strings.TrimSpace(request.Column), Value: row[query.ValueColumn],
		Status: CellTraceStatus(status), Contributions: contributions, HasMore: hasMore, NextOffset: nextOffset,
		OmissionCode: omission, Complete: true,
	}, nil
}

// parseCellTracePostgreSQLRow parses the identity-matched row on the
// PostgreSQL arm. Status/hasMore/omission are plain text/boolean columns the
// adapter already delivers as natives, matching the AQL reads exactly. The
// contributor page is a jsonb array (fail-closed through the published-array
// law — a SQL NULL page is refused, the renderer COALESCEs it to '[]'). The
// value column keeps the cross-backend three-state contract: SQL NULL stays
// nil (a missing traced scalar — the status column is the authority), a
// stored JSON value (including the JSON null scalar) decodes fail-closed
// through encoding/json exactly as the Arango driver path delivers it, and
// adapter-free natives pass through for the convergence contract.
func parseCellTracePostgreSQLRow(query compiler.CompiledCellTraceQuery, request CellTraceRequest, row map[string]any) (CellTraceResult, error) {
	status, ok := row[query.StatusColumn].(string)
	if !ok || !validCellTraceStatus(CellTraceStatus(status)) {
		return CellTraceResult{}, fmt.Errorf("cell trace row has an invalid status")
	}
	items, err := cellTracePostgreSQLJSONParts(row[query.ContributionsColumn])
	if err != nil {
		return CellTraceResult{}, fmt.Errorf("cell trace row has invalid contributions: %w", err)
	}
	contributions, err := cellTraceContributionsFromItems(items)
	if err != nil {
		return CellTraceResult{}, err
	}
	value := row[query.ValueColumn]
	if raw, isRaw := value.(json.RawMessage); isRaw {
		decoded, decodeErr := decodePostgreSQLCellTraceJSON(raw)
		if decodeErr != nil {
			return CellTraceResult{}, fmt.Errorf("cell trace row has an invalid traced value: %w", decodeErr)
		}
		value = decoded
	}
	hasMore, _ := row[query.HasMoreColumn].(bool)
	omission, _ := row[query.OmissionColumn].(string)
	nextOffset := 0
	if hasMore {
		nextOffset = request.Offset + len(contributions)
	}
	return CellTraceResult{
		RowID: strings.TrimSpace(request.RowID), Column: strings.TrimSpace(request.Column), Value: value,
		Status: CellTraceStatus(status), Contributions: contributions, HasMore: hasMore, NextOffset: nextOffset,
		OmissionCode: omission, Complete: true,
	}, nil
}

func parseCellTraceContributions(value any) ([]CellTraceContribution, error) {
	if value == nil {
		return []CellTraceContribution{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("cell trace row has invalid contributions")
	}
	return cellTraceContributionsFromItems(items)
}

// cellTraceContributionsFromItems is the shared per-contribution validation
// both dialects re-enter after their page decode, so provenance fields and
// null-value retention are one law, not two.
func cellTraceContributionsFromItems(items []any) ([]CellTraceContribution, error) {
	result := make([]CellTraceContribution, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cell trace row has an invalid contribution")
		}
		resourceType, _ := object["resourceType"].(string)
		resourceID, _ := object["resourceId"].(string)
		inputStageID, _ := object["inputStageId"].(string)
		inputColumnID, _ := object["inputColumnId"].(string)
		inputColumn, _ := object["inputColumn"].(string)
		outputStageID, _ := object["outputStageId"].(string)
		outputColumnID, _ := object["outputColumnId"].(string)
		outputColumn, _ := object["outputColumn"].(string)
		finalStageID, _ := object["finalStageId"].(string)
		constructionID, _ := object["constructionId"].(string)
		operation, _ := object["operation"].(string)
		result = append(result, CellTraceContribution{
			ResourceType: resourceType, ResourceID: resourceID,
			InputStageID: inputStageID, InputColumnID: inputColumnID, InputColumn: inputColumn,
			OutputStageID: outputStageID, OutputColumnID: outputColumnID, OutputColumn: outputColumn, FinalStageID: finalStageID,
			ConstructionID: constructionID, Operation: operation, Value: object["value"],
		})
	}
	return result, nil
}

func validCellTraceStatus(status CellTraceStatus) bool {
	return status == CellTraceValue || status == CellTraceNoMatch || status == CellTraceRecordedNull || status == CellTraceAmbiguous || status == CellTraceInvalidType || status == CellTraceInvalidUnit
}

func cellTraceFailureStatus(err error) (CellTraceStatus, string, bool) {
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		return "", "", false
	}
	switch dataframeerrors.ErrorCode(userErr.Code()) {
	case dataframeerrors.CodeRelationshipCardinalityViolation, dataframeerrors.CodeTemporalTieAmbiguous:
		return CellTraceAmbiguous, userErr.Code(), true
	case dataframeerrors.CodeUnitIdentityUnknown, dataframeerrors.CodeUnitDimensionIncompatible:
		return CellTraceInvalidUnit, userErr.Code(), true
	case dataframeerrors.CodeInvalidData, dataframeerrors.CodeRecipeContractViolation:
		return CellTraceInvalidType, userErr.Code(), true
	default:
		return "", "", false
	}
}
