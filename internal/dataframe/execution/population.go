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
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	population "github.com/calypr/loom/internal/dataframe/compiler/population"
)

type PopulationMappingStatus string

const (
	PopulationMappingComplete   PopulationMappingStatus = "COMPLETE"
	PopulationMappingIncomplete PopulationMappingStatus = "INCOMPLETE"
)

type populationMemberReader interface {
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

const (
	defaultPopulationMappingUnmapped = 100
	maxPopulationMappingUnmapped     = 1000
	defaultPopulationMappingMembers  = 100_000
	defaultPopulationMappingWitness  = 1_000_000
)

func compilePopulationMappingOutput(resolved Resolved, name string) (population.CompiledPopulationMappingQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != name {
			continue
		}
		return population.CompilePopulationMappingOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, ir.DefaultPhysicalOptimizationPolicy())
	}
	return population.CompiledPopulationMappingQuery{}, fmt.Errorf("population mapping output %q was not found", name)
}

func populationWitnessIdentity(query population.CompiledPopulationMappingQuery, row map[string]any) (string, error) {
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
