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
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// PopulationMappingStatus is deliberately closed. Exact counts are only
// meaningful for COMPLETE results.
type PopulationMappingStatus string

const (
	PopulationMappingComplete   PopulationMappingStatus = "COMPLETE"
	PopulationMappingIncomplete PopulationMappingStatus = "INCOMPLETE"
)

// PopulationMemberReader is the smallest reader needed by the mapping
// operation. Lifecycle owns paging and authorization; execution only needs
// the immutable member IDs to compare with witness rows.
type PopulationMemberReader interface {
	VisitMembers(context.Context, func(string) error) error
}

// PopulationMemberReaderFunc adapts a lifecycle-owned member stream without
// exposing its persistence details to execution.
type PopulationMemberReaderFunc func(context.Context, func(string) error) error

func (f PopulationMemberReaderFunc) VisitMembers(ctx context.Context, visit func(string) error) error {
	return f(ctx, visit)
}

// PopulationMappingRequest bounds one request-scoped mapping computation.
// AfterMemberID is an opaque lifecycle cursor position; it is never sent to
// the compiler or backend.
type PopulationMappingRequest struct {
	Output             string
	AfterMemberID      string
	MaxUnmapped        int
	MaxSelectedMembers int64
	MaxWitnessRows     int64
}

// PopulationMappingDiagnostic contains only safe, transport-neutral
// diagnostics. It deliberately excludes query text and storage details.
type PopulationMappingDiagnostic struct {
	Code    string
	Message string
}

// PopulationMappingResult is the concrete execution result. Lifecycle turns
// its bounded IDs into authorized ResourceRefs and adds the receipt binding.
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

const (
	defaultPopulationMappingUnmapped = 100
	maxPopulationMappingUnmapped     = 1000
	defaultPopulationMappingMembers  = 100_000
	defaultPopulationMappingWitness  = 1_000_000
)

// PopulationMapping compiles the dedicated witness terminal from the same
// resolved output used by ordinary dataframe execution, then streams its
// rows through the existing query executor. It never exposes compiler fields
// or adds provenance to ordinary dataframe rows.
func (e *Engine) PopulationMapping(ctx context.Context, resolved Resolved, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return PopulationMappingResult{}, fmt.Errorf("population mapping output is required")
	}
	compiled, err := compilePopulationMappingOutput(resolved, request.Output)
	if err != nil {
		return PopulationMappingResult{}, err
	}
	return e.populationMappingCompiled(ctx, compiled, request, reader)
}

// PopulationMappingCompiled is the execution seam for callers that already
// hold the dedicated compiler result. The resolved-output method above is the
// normal lifecycle entry point; this method keeps tests and specialized
// execution adapters from rebuilding compiler state.
func (e *Engine) PopulationMappingCompiled(ctx context.Context, compiled compiler.CompiledPopulationMappingQuery, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	return e.populationMappingCompiled(ctx, compiled, request, reader)
}

func (e *Engine) populationMappingCompiled(ctx context.Context, compiled compiler.CompiledPopulationMappingQuery, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	if e == nil || e.queryRows == nil {
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

	mapped := make(map[string]struct{})
	emitted := make(map[string]struct{})
	witnessRows := int64(0)
	err := e.queryRows(ctx, compiled.Query, e.batchSize, compiled.BindVars, func(row map[string]any) error {
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

func compilePopulationMappingOutput(resolved Resolved, name string) (compiler.CompiledPopulationMappingQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != name {
			continue
		}
		return compiler.CompilePopulationMappingOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, ir.DefaultPhysicalOptimizationPolicy())
	}
	return compiler.CompiledPopulationMappingQuery{}, fmt.Errorf("population mapping output %q was not found", name)
}

func populationWitnessIdentity(query compiler.CompiledPopulationMappingQuery, row map[string]any) (string, error) {
	if query.ExplicitIdentityColumn != "" {
		return populationExplicitIdentity(row[query.ExplicitIdentityColumn])
	}
	value, ok := row[query.IdentityPartsColumn].([]any)
	if !ok || len(value) == 0 || query.RowIdentity == nil || len(value) != len(query.RowIdentity.Fields) {
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
		canonical, err := canonicalJSONNumber(value.String())
		if err != nil {
			return "", fmt.Errorf("population mapping witness has an invalid explicit row identity: %w", err)
		}
		return "explicit:number:" + canonical, nil
	case int:
		return "explicit:number:" + strconv.FormatInt(int64(value), 10), nil
	case int8:
		return "explicit:number:" + strconv.FormatInt(int64(value), 10), nil
	case int16:
		return "explicit:number:" + strconv.FormatInt(int64(value), 10), nil
	case int32:
		return "explicit:number:" + strconv.FormatInt(int64(value), 10), nil
	case int64:
		return "explicit:number:" + strconv.FormatInt(value, 10), nil
	case uint:
		return "explicit:number:" + strconv.FormatUint(uint64(value), 10), nil
	case uint8:
		return "explicit:number:" + strconv.FormatUint(uint64(value), 10), nil
	case uint16:
		return "explicit:number:" + strconv.FormatUint(uint64(value), 10), nil
	case uint32:
		return "explicit:number:" + strconv.FormatUint(uint64(value), 10), nil
	case uint64:
		return "explicit:number:" + strconv.FormatUint(value, 10), nil
	default:
		return "", fmt.Errorf("population mapping witness has an invalid explicit row identity")
	}
}

func populationNumericIdentity(value float64, bits int) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("population mapping witness has an invalid explicit row identity")
	}
	return "explicit:number:" + strconv.FormatFloat(value, 'g', -1, bits), nil
}

func canonicalJSONNumber(raw string) (string, error) {
	if rational, ok := new(big.Rat).SetString(raw); ok {
		return rational.RatString(), nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid numeric identity")
	}
	return strconv.FormatFloat(value, 'g', -1, 64), nil
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
