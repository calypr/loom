package command

import (
	"errors"
	"fmt"
	"strings"
)

var ErrRenderedPostgresPlaceholderMismatch = errors.New("rendered postgres plan placeholder mismatch")

var ErrRenderedPostgresAQLBindSyntax = errors.New("rendered postgres plan contains aql bind syntax")

type RenderedPostgresPhysicalPlan struct {
	SQL           string
	Args          []PostgreSQLParameter
	OutputColumns []string
}

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
