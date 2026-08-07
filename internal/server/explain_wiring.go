package server

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/runtime"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func explainCompiledQuery(ctx context.Context, client *arangostore.Client, compiled runtime.CompiledQuery) (engine.ExplainAssessment, error) {
	result, err := client.Explain(ctx, arangostore.ExplainRequest{Query: compiled.Query, BindVars: compiled.BindVars})
	if err != nil {
		return engine.ExplainAssessment{}, err
	}
	return engine.AssessmentFromArango(arangostore.AssessExplainResult(result)), nil
}
