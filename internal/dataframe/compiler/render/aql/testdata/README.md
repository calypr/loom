# Wave 6 renderer witnesses

Update the two bounded keyless fixtures explicitly:

```sh
LOOM_UPDATE_RENDER_GOLDENS=1 GOMAXPROCS=1 GOFLAGS=-p=1 go test ./internal/dataframe/compiler/render/aql -run '^TestKeylessGroup(One|All)ValuesAggregatesZeroRowsThroughSentinel$' -count=1
```

Capture twice and compare the files before accepting an update. JSON stores the complete query and bind map; map keys are sorted by the JSON encoder. Review sentinel count, ONE null/error and ALL empty/distinct behavior in the actual fixture. These are renderer contracts, not substitutes for database execution.

| Audit row | Replacement or retained assertion | Independent behavior witness |
| --- | --- | --- |
| 805 | Unsupported direct-key/presence, ONE, nondeferred, hinted ordinary and post-scope-filter cases assert the typed frontier selection result and still require successful fallback rendering. Positive ordinary/semantic frontier ordering, scope duplication and nonforcing-index placement checks remain: row equality alone cannot prove expensive work runs after complete group selection; full frontier goldens would duplicate large specialized plans. | `compiler.TestRelatedGroupPreviewKeepsCompleteContributorsAgainstArango`, `compiler.TestSemanticGroupScalarKeyPreviewMatchesFullQueryAgainstArango` |
| 808 | Retain the general-plan serializer assertions. Optional/nested relationship scope, reduction reuse, aggregate predicates, representative slice order/limits, contains-all boolean membership, pivot flattening and temporal window-before-normalization are distinct rendering contracts. Replacing all ten cases with full query fixtures would add large repetitive snapshots. Exact bind assertions and typed plan validation remain. No result-level replacement is claimed for these retained cases. | Existing compiler execution tests supplement these checks; they do not establish every specialized serializer/strategy contract. |
| 813 | Keyless ONE and ALL use complete query/bind fixtures instead of fragments. Keep fallback sentinel, keyed streaming/no-retained-rows, key-type/missing-policy ordering, Coding count/display-min, related route sentinel and scalar occurrence-key strategy checks. They constrain renderer work or error ordering not implied by typed input validity. Keep bind pruning, conflicting-bind and invalid adapter checks as direct typed/error assertions. | `compiler.TestConstructionGroupCountRowsOnlyKeylessEmptyAndNonemptyAgainstArango`, `compiler.TestConstructionGroupOneRowValuePreservesDistinctAndErrorSemanticsAgainstArango`, `compiler.TestConstructionGroupAllRowValuesMatchContributorsAgainstArango`, `compiler.TestConstructionGroupMissingKeyPoliciesAgainstArango`, `compiler.TestConstructionGroupRelatedOccurrenceCodingFieldSourceAgainstArango` |

Negative checks: an isolated Go overlay that counts the null sentinel as one must fail both keyless fixtures. Removing the ALL-only frontier guard must fail the typed ONE fallback case. Keep the production checkout unchanged during these experiments.
