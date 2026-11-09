package ingest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestLoadIncompleteErrorRejectsPartialResourceLoad pins the mutable resource
// path's honest-error guarantee (docs/loom-reliability-contract.md:76). The
// accumulation loops in loadResource leave the row-level counters set when
// FailFast is disabled, so a load that inserted partial rows must surface a
// typed error rather than the nil it returned before the incomplete-check existed.
func TestLoadIncompleteErrorRejectsPartialResourceLoad(t *testing.T) {
	if err := loadIncompleteError(LoadSummary{}); err != nil {
		t.Fatalf("loadIncompleteError(clean) = %v, want nil", err)
	}

	for _, summary := range []LoadSummary{
		{ValidationErrors: 3},
		{GenerationErrors: 1},
		{EdgeErrors: 2},
		{ValidationErrors: 1, GenerationErrors: 2, EdgeErrors: 4},
	} {
		err := loadIncompleteError(summary)
		if err == nil {
			t.Fatalf("loadIncompleteError(%+v) = nil, want incomplete error", summary)
		}
		var incomplete *LoadIncompleteError
		if !errors.As(err, &incomplete) {
			t.Fatalf("loadIncompleteError(%+v) = %T, want *LoadIncompleteError", summary, err)
		}
		if incomplete.ValidationErrors != summary.ValidationErrors ||
			incomplete.GenerationErrors != summary.GenerationErrors ||
			incomplete.EdgeErrors != summary.EdgeErrors {
			t.Fatalf("loadIncompleteError(%+v) reported counts %+v, want the summary counts", summary, *incomplete)
		}
		message := incomplete.Error()
		if !strings.Contains(message, "incomplete") {
			t.Fatalf("loadIncompleteError message %q does not say the load is incomplete", message)
		}
		for _, want := range []string{
			fmt.Sprintf("validation_errors=%d", summary.ValidationErrors),
			fmt.Sprintf("generation_errors=%d", summary.GenerationErrors),
			fmt.Sprintf("edge_errors=%d", summary.EdgeErrors),
		} {
			if !strings.Contains(message, want) {
				t.Fatalf("loadIncompleteError message %q does not report %s", message, want)
			}
		}
	}
}
