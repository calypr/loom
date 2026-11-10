package server

import (
	"context"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// explainCompiledQuery is an AQL policy surface: the EXPLAIN view is derived
// from the command through closed dispatch, so a PostgreSQL-tagged, untagged,
// or unknown-tag command is refused here and never sent to the Arango EXPLAIN
// endpoint.
func explainCompiledQuery(ctx context.Context, client *arangostore.Client, compiled dataframeexecution.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
	query, bindVars, dispatchErr := compiled.Command.AQLParameters()
	if dispatchErr != nil {
		return dataframeexecution.ExplainAssessment{}, dispatchErr
	}
	result, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
	if err != nil {
		return dataframeexecution.ExplainAssessment{}, err
	}
	return dataframeexecution.AssessmentFromArango(arangostore.AssessExplainResult(result)), nil
}
