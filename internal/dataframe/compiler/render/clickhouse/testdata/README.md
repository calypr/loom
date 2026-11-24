# Wave 6 ClickHouse renderer witnesses

Update the two bounded fixtures explicitly:

```sh
LOOM_UPDATE_RENDER_GOLDENS=1 GOMAXPROCS=1 GOFLAGS=-p=1 go test ./internal/dataframe/compiler/render/clickhouse -run '^(TestRenderKeyJoinPinsExactInputsAndPreservesLeftRows|TestRenderWorkspaceAppendCapturesExactSameBundleInputsInOrder)$' -count=1
```

Capture twice and compare before accepting updates. Each fixture contains exact SQL and positional arguments. Existing independently specified columns and JOIN argument order remain assertions.

| Audit row | Replacement or retained assertion | Behavior coverage |
| --- | --- | --- |
| 810 | Replace scoped LEFT JOIN fragments with exact SQL/args: right scope stays in ON, left scope in WHERE, ordinary key equality and null extension remain pinned. Retain nullable key and membership NULL-never-matches checks; `TestRenderNullableCompositeMembershipClickHouseSemantics` executes independent expected INCLUDE/EXCLUDE row IDs for duplicate and null composite/single keys. Retain append projection/typed-null syntax because membership execution does not prove append behavior. Revision/column/type/scope errors remain direct error assertions. | Parent must run the named live ClickHouse membership test; local renderer passes do not count as live acceptance. |
| 811 | Replace small workspace APPEND source-order/LIMIT fragments with complete SQL/args. Keep independent output columns. Retain multi-path and mixed/whole/row scope operand checks, missing-input typed NULL, sealed membership and stale artifact errors: these exact SQL/args and typed rejection contracts lack a same-workspace live row witness, so deleting them would lose coverage. | APPEND fixture pins stable row-key ordering, terminal page limit, both operand projections and authorization args; no new database result claim. |

Negative checks: isolated Go overlays changing `join_use_nulls` to zero or APPEND row-key ordering to descending must fail the respective fixtures. Keep production source unchanged.
