package server

import (
	"context"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func explainCompiledQuery(ctx context.Context, client *arangostore.Client, compiled dataframeexecution.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
	result, err := client.Explain(ctx, arangostore.ExplainRequest{Query: compiled.Query, BindVars: compiled.BindVars})
	if err != nil {
		return dataframeexecution.ExplainAssessment{}, err
	}
	return dataframeexecution.AssessmentFromArango(arangostore.AssessExplainResult(result)), nil
}
