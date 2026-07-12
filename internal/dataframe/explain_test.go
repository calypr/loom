package dataframe

import (
	"context"
	"testing"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestExplainCompiledQueryRequiresConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ExplainCompiledQuery(ctx, arangostore.ConnectionOptions{
		URL:      "http://127.0.0.1:8529",
		Database: "fhir_proto",
	}, CompiledQuery{})
	if err == nil {
		t.Fatal("expected connection error")
	}
}
