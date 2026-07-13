package compiler

import aql "github.com/calypr/loom/internal/dataframe/compiler/render/aql"

func pruneUnusedRuntimeBindVars(bindVars map[string]any, query string) map[string]any {
	return aql.PruneUnusedRuntimeBindVars(bindVars, query)
}
