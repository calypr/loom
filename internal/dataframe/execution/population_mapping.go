package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
)

type PopulationMappingStatus string

const (
	PopulationMappingComplete   PopulationMappingStatus = "COMPLETE"
	PopulationMappingIncomplete PopulationMappingStatus = "INCOMPLETE"
)

type PopulationMemberReader interface {
	VisitMembers(context.Context, func(string) error) error
}

type PopulationMemberReaderFunc func(context.Context, func(string) error) error

func (f PopulationMemberReaderFunc) VisitMembers(ctx context.Context, visit func(string) error) error {
	return f(ctx, visit)
}

type PopulationMappingRequest struct {
	Output             string
	AfterMemberID      string
	MaxUnmapped        int
	MaxSelectedMembers int64
	MaxWitnessRows     int64
}

type PopulationMappingDiagnostic struct {
	Code    string
	Message string
}

type PopulationMappingResult struct {
	Status            PopulationMappingStatus
	SelectedCount     int64
	MappedCount       int64
	UnmappedCount     int64
	EmittedRows       int64
	UnmappedMemberIDs []string
	HasMoreUnmapped   bool
	Diagnostics       []PopulationMappingDiagnostic
}

var errPopulationMappingLimit = errors.New("population mapping resource limit exceeded")

var ErrPopulationMappingPostgresUnsupported = errors.New("population mapping unsupported on the selected PostgreSQL composition")

const (
	defaultPopulationMappingUnmapped = 100
	maxPopulationMappingUnmapped     = 1000
	defaultPopulationMappingMembers  = 100_000
	defaultPopulationMappingWitness  = 1_000_000
)

func (e *Engine) PopulationMapping(ctx context.Context, resolved Resolved, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return PopulationMappingResult{}, fmt.Errorf("population mapping output is required")
	}
	compiled, err := compilePopulationMappingOutput(e.compileTarget, resolved, request.Output)
	if err != nil {
		if errors.Is(err, postgresrender.ErrUnsupportedPostgresPlan) {
			return PopulationMappingResult{}, fmt.Errorf("%w: %v", ErrPopulationMappingPostgresUnsupported, err)
		}
		return PopulationMappingResult{}, err
	}
	return e.populationMappingCompiled(ctx, compiled, request, reader)
}

func (e *Engine) PopulationMappingCompiled(ctx context.Context, compiled compiler.CompiledPopulationMappingQuery, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	return e.populationMappingCompiled(ctx, compiled, request, reader)
}

func (e *Engine) populationMappingCompiled(ctx context.Context, compiled compiler.CompiledPopulationMappingQuery, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	isPostgreSQL := false
	switch compiled.Command.Backend() {
	case command.CompiledBackendAQL:
	case command.CompiledBackendPostgreSQL:
		isPostgreSQL = true
	default:
		return PopulationMappingResult{}, fmt.Errorf("population mapping command has unsupported backend %q", string(compiled.Command.Backend()))
	}
	if e == nil || e.sourceRows == nil {
		if isPostgreSQL {
			return PopulationMappingResult{}, fmt.Errorf("%w: no row port serves the selected PostgreSQL composition", ErrPopulationMappingPostgresUnsupported)
		}
		return PopulationMappingResult{}, fmt.Errorf("population mapping query executor is required")
	}
	if reader == nil {
		return PopulationMappingResult{}, fmt.Errorf("population mapping member reader is required")
	}
	limit := request.MaxUnmapped
	if limit <= 0 {
		limit = defaultPopulationMappingUnmapped
	}
	if limit > maxPopulationMappingUnmapped {
		return PopulationMappingResult{}, fmt.Errorf("population mapping unmapped limit exceeds %d", maxPopulationMappingUnmapped)
	}
	maxMembers := request.MaxSelectedMembers
	if maxMembers <= 0 {
		maxMembers = defaultPopulationMappingMembers
	}
	maxWitness := request.MaxWitnessRows
	if maxWitness <= 0 {
		maxWitness = defaultPopulationMappingWitness
	}

	selected := make(map[string]struct{})
	if err := reader.VisitMembers(ctx, func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("population mapping member ID is empty")
		}
		selected[id] = struct{}{}
		if int64(len(selected)) > maxMembers {
			return errPopulationMappingLimit
		}
		return nil
	}); err != nil {
		if populationMappingIncomplete(err) {
			return incompletePopulationMappingResult(), nil
		}
		return PopulationMappingResult{}, fmt.Errorf("read population members: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return incompletePopulationMappingResult(), nil
	}

	if compiled.MemberColumn == "" || (compiled.IdentityPartsColumn == "" && compiled.ExplicitIdentityColumn == "") {
		return PopulationMappingResult{}, fmt.Errorf("population mapping query is missing typed witness columns")
	}
	stream := RowsRequest{Command: compiled.Command, BatchSize: e.batchSize}
	if isPostgreSQL {
		if compiled.IdentityPartsColumn != "" && compiled.ExplicitIdentityColumn != "" {
			return PopulationMappingResult{}, fmt.Errorf("population mapping PostgreSQL witness requires exactly one identity column")
		}
		identityColumn := compiled.IdentityPartsColumn
		if identityColumn == "" {
			identityColumn = compiled.ExplicitIdentityColumn
		}
		stream.DeclaredColumns = []string{compiled.MemberColumn, identityColumn}
	}

	mapped := make(map[string]struct{})
	emitted := make(map[string]struct{})
	witnessRows := int64(0)
	err := e.sourceRows(ctx, stream, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		witnessRows++
		if witnessRows > maxWitness {
			return errPopulationMappingLimit
		}
		memberID, ok := row[compiled.MemberColumn].(string)
		memberID = strings.TrimSpace(memberID)
		if !ok || memberID == "" {
			return fmt.Errorf("population mapping witness has an invalid member ID")
		}
		if _, ok := selected[memberID]; !ok {
			return fmt.Errorf("population mapping witness member is not selected")
		}
		identity, identityErr := populationWitnessIdentity(compiled, row)
		if identityErr != nil {
			return identityErr
		}
		mapped[memberID] = struct{}{}
		emitted[identity] = struct{}{}
		return nil
	})
	if err != nil {
		if populationMappingIncomplete(err) {
			return incompletePopulationMappingResult(), nil
		}
		if isPostgreSQL && errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			return PopulationMappingResult{}, fmt.Errorf("%w: PostgreSQL row adapter is not registered for the mapping command", ErrPopulationMappingPostgresUnsupported)
		}
		return PopulationMappingResult{}, fmt.Errorf("execute population mapping: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return incompletePopulationMappingResult(), nil
	}

	selectedIDs := make([]string, 0, len(selected))
	for id := range selected {
		selectedIDs = append(selectedIDs, id)
	}
	sort.Strings(selectedIDs)
	after := strings.TrimSpace(request.AfterMemberID)
	unmapped := make([]string, 0, min(limit, len(selectedIDs)))
	hasMore := false
	for _, id := range selectedIDs {
		if _, ok := mapped[id]; ok || (after != "" && id <= after) {
			continue
		}
		if len(unmapped) == limit {
			hasMore = true
			break
		}
		unmapped = append(unmapped, id)
	}
	return PopulationMappingResult{
		Status:            PopulationMappingComplete,
		SelectedCount:     int64(len(selected)),
		MappedCount:       int64(len(mapped)),
		UnmappedCount:     int64(len(selected) - len(mapped)),
		EmittedRows:       int64(len(emitted)),
		UnmappedMemberIDs: unmapped,
		HasMoreUnmapped:   hasMore,
	}, nil
}

func compilePopulationMappingOutput(target compiler.Target, resolved Resolved, name string) (compiler.CompiledPopulationMappingQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != name {
			continue
		}
		return compiler.CompilePopulationMappingOutput(target, output, resolved.Semantic.SemanticPlan.Bindings, ir.DefaultPhysicalOptimizationPolicy())
	}
	return compiler.CompiledPopulationMappingQuery{}, fmt.Errorf("population mapping output %q was not found", name)
}

func populationWitnessIdentity(query compiler.CompiledPopulationMappingQuery, row map[string]any) (string, error) {
	if query.ExplicitIdentityColumn != "" {
		return populationExplicitIdentity(row[query.ExplicitIdentityColumn])
	}
	value, err := populationIdentityParts(row[query.IdentityPartsColumn])
	if err != nil {
		return "", err
	}
	if len(value) == 0 || query.RowIdentity == nil || len(value) != len(query.RowIdentity.Fields) {
		return "", fmt.Errorf("population mapping witness has an invalid row identity shape")
	}
	for _, part := range value {
		if part == nil {
			return "", fmt.Errorf("population mapping witness has a null row identity part")
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode population mapping row identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "default:" + hex.EncodeToString(digest[:]), nil
}

func populationIdentityParts(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case json.RawMessage:
		if len(typed) == 0 || !json.Valid(typed) {
			return nil, fmt.Errorf("population mapping witness has an invalid row identity shape")
		}
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			return nil, fmt.Errorf("population mapping witness has an invalid row identity shape: %w", err)
		}
		items, ok := decoded.([]any)
		if !ok {
			return nil, fmt.Errorf("population mapping witness has an invalid row identity shape")
		}
		return items, nil
	default:
		return nil, fmt.Errorf("population mapping witness has an invalid row identity shape")
	}
}

func populationExplicitIdentity(value any) (string, error) {
	switch value := value.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("population mapping witness has an invalid explicit row identity")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("encode population mapping explicit row identity: %w", err)
		}
		return "explicit:string:" + string(encoded), nil
	case bool:
		return "explicit:bool:" + strconv.FormatBool(value), nil
	case float32:
		return populationNumericIdentity(float64(value), 32)
	case float64:
		return populationNumericIdentity(value, 64)
	case json.Number:
		return populationNumericStringIdentity(value.String())
	case int:
		return populationNumericStringIdentity(strconv.FormatInt(int64(value), 10))
	case int8:
		return populationNumericStringIdentity(strconv.FormatInt(int64(value), 10))
	case int16:
		return populationNumericStringIdentity(strconv.FormatInt(int64(value), 10))
	case int32:
		return populationNumericStringIdentity(strconv.FormatInt(int64(value), 10))
	case int64:
		return populationNumericStringIdentity(strconv.FormatInt(value, 10))
	case uint:
		return populationNumericStringIdentity(strconv.FormatUint(uint64(value), 10))
	case uint8:
		return populationNumericStringIdentity(strconv.FormatUint(uint64(value), 10))
	case uint16:
		return populationNumericStringIdentity(strconv.FormatUint(uint64(value), 10))
	case uint32:
		return populationNumericStringIdentity(strconv.FormatUint(uint64(value), 10))
	case uint64:
		return populationNumericStringIdentity(strconv.FormatUint(value, 10))
	default:
		return "", fmt.Errorf("population mapping witness has an invalid explicit row identity")
	}
}

func populationNumericIdentity(value float64, bits int) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("population mapping witness has an invalid explicit row identity")
	}
	return populationNumericStringIdentity(strconv.FormatFloat(value, 'g', -1, bits))
}

func populationNumericStringIdentity(raw string) (string, error) {
	canonical, err := canonicalPopulationNumber(raw)
	if err != nil {
		return "", fmt.Errorf("population mapping witness has an invalid explicit row identity: %w", err)
	}
	return "explicit:number:" + canonical, nil
}

func canonicalPopulationNumber(raw string) (string, error) {
	if rational, ok := new(big.Rat).SetString(raw); ok {
		return rational.RatString(), nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid numeric identity")
	}
	decimal := strconv.FormatFloat(value, 'g', -1, 64)
	rational, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return "", fmt.Errorf("invalid numeric identity")
	}
	return rational.RatString(), nil
}

func populationMappingIncomplete(err error) bool {
	return errors.Is(err, errPopulationMappingLimit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func incompletePopulationMappingResult() PopulationMappingResult {
	return PopulationMappingResult{Status: PopulationMappingIncomplete, Diagnostics: []PopulationMappingDiagnostic{{Code: "INCOMPLETE", Message: "population mapping did not finish within the request limits"}}}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
