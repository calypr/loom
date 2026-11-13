package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
)

var ErrReceiptExecutionContract = errors.New("receipt execution contract mismatch")

func validateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if len(scope.AuthResourcePaths) != 0 {
			return receiptExecutionContractError("unrestricted scope must not carry resource paths")
		}
	case authscope.ReadScopeRestricted:
	default:
		return receiptExecutionContractError("authorization scope mode is missing or invalid")
	}
	if explorerScopeDigest(scope) != expectedDigest {
		return receiptExecutionContractError("authorized scope does not match receipt scope digest")
	}
	return nil
}

func validateReceiptEnginePublicColumns(receipt *explorer.CompilationReceipt, resolved dataframeexecution.Resolved) error {
	if receipt == nil {
		return receiptExecutionContractError("receipt is required")
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
			return receiptExecutionContractError("receipt contains an invalid emitted column")
		}
		want[emitted.OutputID] = append(want[emitted.OutputID], emitted.PublicColumn)
	}
	var groupedFingerprints map[string]string
	if len(groupedOutputs) > 0 {
		var err error
		groupedFingerprints, _, err = resolvedOutputArtifacts(resolved)
		if err != nil {
			return receiptExecutionContractError("cannot reproduce grouped output fingerprints: %v", err)
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
			return receiptExecutionContractError("engine public columns for output %q differ from receipt", output.Name)
		}
	}
	for output := range want {
		if !seen[output] {
			return receiptExecutionContractError("receipt contains columns for unknown output %q", output)
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

func receiptExecutionContractError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrReceiptExecutionContract, fmt.Sprintf(format, args...))
}
