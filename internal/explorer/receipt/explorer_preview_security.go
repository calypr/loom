package receipt

import (
	"fmt"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
)

var ErrReceiptExecutionContract = explorer.ErrReceiptExecutionContract

func ValidateEnginePublicColumns(receipt *explorer.CompilationReceipt, resolved dataframeexecution.Resolved) error {
	if receipt == nil {
		return ExecutionContractError("receipt is required")
	}
	want := make(map[string][]string)
	groupedOutputs := make(map[string]bool)
	for _, output := range receipt.Bundle.Outputs {
		if output.RowGrain == "groups" && output.GroupRows != nil {
			groupedOutputs[output.Name] = true
		}
	}
	for _, emitted := range receipt.EmittedColumns {
		if strings.TrimSpace(emitted.OutputID) == "" || strings.TrimSpace(emitted.PublicColumn) == "" {
			return ExecutionContractError("receipt contains an invalid emitted column")
		}
		want[emitted.OutputID] = append(want[emitted.OutputID], emitted.PublicColumn)
	}
	var groupedFingerprints map[string]string
	if len(groupedOutputs) > 0 {
		var err error
		groupedFingerprints, _, err = ResolvedOutputArtifacts(resolved)
		if err != nil {
			return ExecutionContractError("cannot reproduce grouped output fingerprints: %v", err)
		}
	}
	seen := make(map[string]bool)
	for _, output := range resolved.Compiled.Outputs {
		seen[output.Name] = true
		if groupedOutputs[output.Name] {

			if expected, actual := receipt.OutputFingerprints[output.Name], groupedFingerprints[output.Name]; expected == "" || actual == "" || expected != actual {
				return contractMismatch("output_execution", output.Name, expected, actual)
			}
			continue
		}
		actual := make([]string, 0, len(output.OutputSchema))
		for _, column := range output.OutputSchema {
			if !column.Internal && !column.Identity {
				actual = append(actual, column.Name)
			}
		}
		if !sameUniqueStrings(actual, want[output.Name]) {
			return ExecutionContractError("engine public columns for output %q differ from receipt", output.Name)
		}
	}
	for output := range want {
		if !seen[output] {
			return ExecutionContractError("receipt contains columns for unknown output %q", output)
		}
	}
	return nil
}

func sameUniqueStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		if _, duplicate := values[value]; duplicate {
			return false
		}
		values[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(right))
	for _, value := range right {
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func ExecutionContractError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrReceiptExecutionContract, fmt.Sprintf(format, args...))
}
