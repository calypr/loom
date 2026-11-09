package compiler

// PostgreSQL renderer result contract (pg-ir-02).
//
// RenderedPostgresPhysicalPlan is the PostgreSQL counterpart of
// aql.RenderedPhysicalPlan: it carries statement SQL plus ordered, typed
// driver arguments and nothing else. It deliberately has no named bind map:
// AQL's @name / @@name bind syntax has no meaning in a PostgreSQL statement,
// and this contract rejects it at construction so a plan can never smuggle
// AQL-shaped text into the PostgreSQL executor. Construction, not caller
// discipline, establishes the guarantees:
//
//   - SQL is non-blank, contains no '@' (AQL bind syntax), and its $n
//     placeholders are exactly $1..$len(Args), each used at least once.
//   - Every argument carries a registered PostgreSQLParameterType and a
//     value consistent with that type; SQL NULL is the dedicated null type.
//   - Args and OutputColumns are copied, so a repeated render that reuses a
//     result cannot be aliased back into plan data, and a caller cannot
//     mutate positional bindings through the result.
//
// OutputColumns is positional metadata: output column names are runtime data
// and never become SQL text; the SELECT list is positional (c0, c1, ...) and
// readers align it with OutputColumns by index.

import (
	"errors"
	"fmt"
	"strings"
)

// ErrRenderedPostgresPlaceholderMismatch reports a rendered PostgreSQL plan
// whose $n placeholders do not line up with its ordered argument list.
var ErrRenderedPostgresPlaceholderMismatch = errors.New("rendered postgres plan placeholder mismatch")

// ErrRenderedPostgresAQLBindSyntax reports rendered PostgreSQL SQL that
// contains AQL bind syntax ('@' tokens). The PostgreSQL renderer never emits
// it; receiving this error means a caller assembled SQL by hand or an AQL
// string leaked into a PostgreSQL path, and the plan must be rejected rather
// than executed.
var ErrRenderedPostgresAQLBindSyntax = errors.New("rendered postgres plan contains aql bind syntax")

// RenderedPostgresPhysicalPlan is an executable PostgreSQL representation of
// a validated physical plan: deterministic statement SQL with $n
// placeholders plus the ordered typed arguments for those placeholders.
type RenderedPostgresPhysicalPlan struct {
	// SQL is the statement text. It contains only registry-validated
	// identifiers and $n placeholders; data never appears in it.
	SQL string
	// Args binds the placeholders: Args[i] is the value for $(i+1).
	Args []PostgreSQLParameter
	// OutputColumns names the positional output columns of the SELECT list
	// in plan order. It is metadata for the reader, never SQL text.
	OutputColumns []string
}

// NewRenderedPostgresPhysicalPlan validates and freezes one rendered
// PostgreSQL plan. The argument slice and the output column names are
// copied so the result never aliases the renderer's (or any caller's)
// slices.
func NewRenderedPostgresPhysicalPlan(sql string, args []PostgreSQLParameter, outputColumns []string) (RenderedPostgresPhysicalPlan, error) {
	if strings.TrimSpace(sql) == "" {
		return RenderedPostgresPhysicalPlan{}, fmt.Errorf("rendered postgres plan requires statement SQL")
	}
	if strings.Contains(sql, "@") {
		return RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: statement SQL must use $n placeholders only; found an '@' token", ErrRenderedPostgresAQLBindSyntax)
	}
	if err := validatePostgreSQLParameters(args); err != nil {
		return RenderedPostgresPhysicalPlan{}, err
	}
	if err := validatePostgresPlaceholders(sql, len(args)); err != nil {
		return RenderedPostgresPhysicalPlan{}, err
	}
	var copiedArgs []PostgreSQLParameter
	if len(args) != 0 {
		copiedArgs = append([]PostgreSQLParameter(nil), args...)
	}
	copiedColumns := make([]string, len(outputColumns))
	for index, name := range outputColumns {
		if strings.TrimSpace(name) == "" {
			return RenderedPostgresPhysicalPlan{}, fmt.Errorf(
				"rendered postgres plan output column %d is blank", index+1)
		}
		copiedColumns[index] = name
	}
	return RenderedPostgresPhysicalPlan{SQL: sql, Args: copiedArgs, OutputColumns: copiedColumns}, nil
}

// validatePostgreSQLParameters is the single argument-validation surface
// shared by NewPostgreSQLCommand and NewRenderedPostgresPhysicalPlan: every
// argument declares a registered type, the SQL-null type carries no value,
// and no other type carries a nil value (missing must be omitted, and JSON
// null must be a json/jsonb value — never a flattened SQL NULL).
func validatePostgreSQLParameters(args []PostgreSQLParameter) error {
	for index, argument := range args {
		if !argument.Type.registered() {
			return fmt.Errorf("PostgreSQL command argument %d has unregistered parameter type %q", index+1, string(argument.Type))
		}
		if argument.Type == PostgreSQLParameterNull {
			if argument.Value != nil {
				return fmt.Errorf("PostgreSQL command argument %d is typed SQL null but carries a value", index+1)
			}
			continue
		}
		if argument.Value == nil {
			return fmt.Errorf("PostgreSQL command argument %d is typed %q but carries a nil value; use %q for SQL NULL", index+1, string(argument.Type), string(PostgreSQLParameterNull))
		}
	}
	return nil
}

// validatePostgresPlaceholders requires the SQL's placeholder set to be
// exactly {$1..$argCount}: every argument is referenced, no placeholder is
// out of range, and the numbering is contiguous.
func validatePostgresPlaceholders(sql string, argCount int) error {
	seen := make([]bool, argCount+1)
	maxSeen := 0
	for index := 0; index < len(sql); index++ {
		if sql[index] != '$' {
			continue
		}
		start := index + 1
		end := start
		for end < len(sql) && sql[end] >= '0' && sql[end] <= '9' {
			end++
		}
		if end == start {
			// A lone '$' is not a placeholder; dollar signs inside the
			// renderer's own text never occur (values are parameters).
			continue
		}
		number := 0
		for _, digit := range sql[start:end] {
			number = number*10 + int(digit-'0')
		}
		index = end - 1
		if number < 1 || number > argCount {
			return fmt.Errorf("%w: $%d is outside the 1..%d argument range",
				ErrRenderedPostgresPlaceholderMismatch, number, argCount)
		}
		if number > maxSeen {
			maxSeen = number
		}
		seen[number] = true
	}
	if maxSeen != argCount {
		return fmt.Errorf("%w: highest placeholder is $%d but %d arguments were rendered",
			ErrRenderedPostgresPlaceholderMismatch, maxSeen, argCount)
	}
	for number := 1; number <= argCount; number++ {
		if !seen[number] {
			return fmt.Errorf("%w: argument %d ($%d) is never referenced by the statement",
				ErrRenderedPostgresPlaceholderMismatch, number, number)
		}
	}
	return nil
}
