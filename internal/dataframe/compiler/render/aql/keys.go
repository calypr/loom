package aql

type groupKeyRendering struct {
	lines                            []string
	collectKeys                      []string
	sortKeys                         []string
	keyTypeAssertions                []string
	identityParts                    []string
	semanticDisplayVariable          string
	semanticDisplayAggregateVariable string
	codingDisplayVariable            string
	codingDisplayAggregateVariable   string
}
