package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
)

// ErrReceiptExecutionContract identifies a receipt/capability mismatch that
// must be rejected before a receipt-backed query or materialization starts.
// It is deliberately transport-neutral; HTTP adapters can map it to their
// existing receipt-input conflict diagnostic.
var ErrReceiptExecutionContract = errors.New("receipt execution contract mismatch")

// validateAuthorizedReadScope verifies both the digest and the explicit mode.
// In particular, restricted + zero paths is a valid deny-all result, while an
// empty mode is rejected because downstream legacy code may interpret it as
// unrestricted when the path list is empty.
func validateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if len(scope.AuthResourcePaths) != 0 {
			return receiptExecutionContractError("unrestricted scope must not carry resource paths")
		}
	case authscope.ReadScopeRestricted:
		// A restricted-empty scope is intentional and must remain restricted.
	default:
		return receiptExecutionContractError("authorization scope mode is missing or invalid")
	}
	if explorerScopeDigest(scope) != expectedDigest {
		return receiptExecutionContractError("authorized scope does not match receipt scope digest")
	}
	return nil
}

// validateReceiptEnginePublicColumns proves that the engine and receipt expose
// exactly the same public columns. Column order is deliberately not compared:
// the compiler owns execution order while the receipt's output contract owns
// presentation order. Internal identity/provenance projections are excluded.
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
			// Explicit groups are terminal row sources, so their executable
			// columns are not the authored root-field emissions. Bind this
			// exception to the receipt's full compiler-derived schema and plan.
			if expected, actual := receipt.OutputFingerprints[output.Name], groupedFingerprints[output.Name]; expected == "" || actual == "" || expected != actual {
				return contractMismatch("output_execution", output.Name, expected, actual)
			}
			continue
		}
		actual := make([]string, 0, len(output.OutputSchema))
		for _, column := range output.OutputSchema {
			if !column.Internal {
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
