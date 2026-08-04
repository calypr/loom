package graphqlapi

import (
	"errors"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestErrorChainIncludesWrappedBackendCause(t *testing.T) {
	err := dataframeerrors.Wrap(errors.New("clickhouse connection refused"), dataframeerrors.CodeBackendUnavailable, "")

	chain := errorChain(err)
	if !strings.Contains(chain, "the dataframe backend is temporarily unavailable") {
		t.Fatalf("error chain omitted public error: %q", chain)
	}
	if !strings.Contains(chain, "clickhouse connection refused") {
		t.Fatalf("error chain omitted backend cause: %q", chain)
	}
}
