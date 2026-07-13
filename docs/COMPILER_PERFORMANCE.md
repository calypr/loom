# Compiler performance

This document keeps the durable conclusions from the retired AQL tournament
harnesses. The old tournament tests and their generated profiles were useful
for exploration, but they are not part of Loom's correctness suite.

## Current production path

Dataframe requests use the generic pipeline:

```text
spec -> semantic -> compiler/ir -> compiler/lower -> compiler/optimize -> render/aql
```

The default physical policy includes generic scope-safe sibling traversal
sharing, compact set projection, required-match reuse, and the proven endpoint
lowering rules. Prepared selector arrays and rich-consumer fusion remain
disabled because the measured GDC candidates were slower or produced no
eligible rewrite. The optimizer must not enable a candidate solely because it
reduces AQL text size; result parity, authorization/generation scope, and live
Arango profile evidence are required.

## Reproducible measurements

For compiler shape and diagnostics:

```bash
make compiler-bench
```

For the checked-in GDC request against a running GraphQL service:

```bash
make dataframe-demo DATAFRAME_LIMIT=1000 DATAFRAME_REPEAT=3 \
  DATAFRAME_PRINT_RESPONSE=false
```

For Explain/Profile output against the configured Arango database:

```bash
make dataframe-profile DATAFRAME_PROFILE_LIMIT=1000
```

Live Arango compiler tests are opt-in with
`LOOM_COMPILER_ARANGO_INTEGRATION=1`; they must never be required for ordinary
unit or GraphQL tests.

## Findings retained from the tournaments

1. **Index-aware traversal strategy.** Compare native traversal with explicit
   endpoint/type equality lookups. Equality predicates can select the
   compound edge indexes where broad `IN` predicates fall back to `_to`.
   Promote only when the generic corpus proves parity and a whole-query median
   improvement.
2. **Traversal-time shaping projection.** Compute the union of selector values
   while a child is in scope and retain only identity, nested-navigation keys,
   and requested values. Do not add a second payload-bearing prepared pass.
3. **Leaf-set summary pushdown.** For leaves without navigated descendants,
   produce aggregate, pivot, and representative-slice outputs from one typed
   summary instead of repeatedly enumerating a payload array.
4. **Identity and ordering properties.** Treat deduplication and ordering as
   physical properties. Remove `UNIQUE`/`SORT` work only when the proof is
   generic and preserves row identity, deterministic order, and slice limits.
5. **Batch root execution.** If the first four changes do not meet the
   latency target, prototype a set-oriented root window and grouped edge
   lookups. This is a higher-risk alternative to correlated root-by-root
   execution and must remain experimental until scope and row-order parity are
   proven.
6. **Catalog-backed costing.** Carry relationship cardinality facts into
   compilation as optional statistics. Use them to choose between equivalent
   physical strategies, with a deterministic no-statistics fallback.

These are implementation candidates, not promises. A candidate that does not
remove measured scanned items, materialized payload, executor work, or peak
memory should be rejected as noise.
