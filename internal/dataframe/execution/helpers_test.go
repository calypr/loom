package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type oracleReshape struct {
	Kind         string
	GroupedPivot *oraclePivot
	Unpivot      *oracleUnpivot
}

const (
	oracleReshapePivot   = "GROUPED_PIVOT"
	oracleReshapeUnpivot = "UNPIVOT"
)

type oraclePivot struct {
	ConstructionID              string
	GroupKeys                   []string
	CategoryColumn, ValueColumn string
	Categories                  []oraclePivotCategory
	DuplicatePolicy             recipe.PivotDuplicatePolicy
	MissingCellPolicy           recipe.PivotMissingCellPolicy
	UnlistedCategoryPolicy      recipe.PivotUnlistedCategoryPolicy
}

type oraclePivotCategory struct {
	Key           recipe.TableScalar
	Output, Label string
}

type oracleUnpivot struct {
	ConstructionID                               string
	Inputs                                       []oracleUnpivotInput
	KeyOutput, KeyLabel, ValueOutput, ValueLabel string
	NullRowPolicy                                recipe.UnpivotNullRowPolicy
}

type oracleUnpivotInput struct {
	Column string
	Key    recipe.TableScalar
}

func CompiledQueryFingerprint(compiled CompiledQuery) string {
	keys := make([]string, 0, len(compiled.BindVars))
	for key := range compiled.BindVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(compiled.Query)
	builder.WriteByte('\x00')
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.Columns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.PublicColumns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, field := range compiled.PivotFields {
		builder.WriteString(field)
		builder.WriteByte('\x00')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func TestSanitizeColumnNamePreservesCompilerShape(t *testing.T) {
	if got, want := sanitizeColumnName("field__--value--"), "field____value__"; got != want {
		t.Fatalf("sanitizeColumnName() = %q, want %q", got, want)
	}
}
