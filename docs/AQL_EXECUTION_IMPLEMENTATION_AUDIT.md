# AQL execution implementation audit

## Conclusion

The compiler has two real production optimizations, but it is not close to a
fully optimized physical translation. Generic sibling traversal sharing and
compact set projection are enabled and measurable. Most other named
optimizer rules are experiments, diagnostics, or policy switches with no
production rewrite behind them.

The concern about `LUNA_AQL_EXECUTION_ROUND_3.md` is justified. Its prepared
selector package largely repeats a previously rejected second-pass prepared
set, and its fusion package starts from a classifier that currently finds no
real candidates. Those packages may reduce some selector work after a major
redesign, but they are unlikely to be the first large reduction in the
5.9-second Arango phase.

The highest-confidence missing translation is index-aware edge lowering. The
actual query's four native traversal nodes all use the default `_to` edge
index while existing endpoint-first compound indexes remain unused. The next
largest architectural gap is that Loom materializes payload-bearing child
arrays and only then performs rich shaping in separate loops.

## Plan versus production code

| Capability | Plan/document claim | Current code truth | Production effect |
|---|---|---|---|
| Physical compiler/renderer | complete | complete; the physical compiler is the execution path | required foundation |
| Root scope/window | complete | project/generation/auth before `_key` sort and limit | root index selected; not the bottleneck |
| Generic sibling traversal sharing | complete | implemented in `sharePhysicalSetGroup`; enabled by default | large win: roughly 7.4s unoptimized to 5.6–5.9s current |
| Compact set projection | complete | implemented and enabled by default | modest runtime win; meaningful memory reduction |
| Prepared selectors | described as complete in old status text | implemented as an optional second prepared array; disabled by default | previously slower and about 303 MB more memory at 1,000 rows |
| Rich-consumer fusion | described as complete in old status text | diagnostics classify only byte-identical expressions; no fusion rewrite/rendering exists | no AQL change |
| Nested traversal sharing | named rule | no optimizer implementation; prior corpus had no repeated nested candidate | no AQL change |
| Required-match reuse | complete | identical required matches are deduplicated and counted | useful, but absent from this optional-only GDC shape |
| Endpoint-first indexes | declared and Explain-tested | native traversal still selects only default edge `_to`; explicit filtered-edge probes select compound indexes | unused by production translation |
| Relationship cardinality catalog | complete | used for discovery/validation; `EdgeCount` is not carried into semantic/physical planning | no cost-aware strategy selection |
| Durable profile tooling | mostly complete | exact variables input, AQL/result hashes, Explain/Profile artifacts now exist | diagnostic only |
| Cost policy | complete structurally | estimates operation counts, not cardinality, fan-out, retained width, or selected index | cannot choose the cheapest physical strategy reliably |

The old `AQL_OPTIMIZATION_WORKLIST.md` execution-status section is therefore
stale where it calls prepared reuse and aggregate/pivot/slice fusion complete.
Prepared references exist, but their current rendering was rejected for
production. Fusion is diagnostics-only.

## What the current AQL actually does

The exact 1,000-row GDC query contains:

| Rendered operation | Count |
|---|---:|
| `UNIQUE` materializations/expressions | 8 |
| `SORT` clauses | 11 |
| native graph traversals | 4 |
| post-materialization `FOR __item IN child_set` loops | 7 |
| child sets retaining `payload` | 6 |

The query first materializes a broad Patient-neighbor array, derives typed
arrays, materializes three nested arrays, and then re-enumerates those arrays
for aggregates, slices, and the pivot. Representative slices sort an array
that was already sorted during set materialization; when the requested sort
is `_key`, the renderer currently emits `_key` twice as the primary key and
tie-breaker.

This is correct AQL, but it is a general-purpose materialize-then-shape plan,
not an optimized query plan.

## Profile-backed bottlenecks

The exact request profile reports 475,876 indexed items, no full scans, and
269,844,480 bytes peak memory. The root `Patient(project, _key)` index is
working. The remaining traversal fan-out is:

| Region | Node | Items | Filtered | Cumulative runtime |
|---|---:|---:|---:|---:|
| shared first hop | 12 | 34,852 | 11,622 | 1.079s |
| Specimen → DocumentReference | 55 | 14,334 | 60,944 | 2.888s |
| Specimen → Group | 79 | 22,950 | 52,328 | 4.124s |
| Group → DocumentReference | 103 | 12,074 | 11,074 | 5.673s |

These runtimes overlap, but the calls/items/filtered counts are decisive: the
renderer asks native traversal to retrieve broad endpoint adjacency and then
filters label, type, project, generation, and auth. `EXPLAIN` shows only the
default `_to` edge index. The endpoint-first compound index is proven usable
only by explicit edge-filter AQL.

The other large node family is payload list enumeration. The same selector is
often recomputed for distinct aggregation, a representative slice, or a
pivot. The current prepared-set experiment addresses this by creating another
array that also retains `payload` and `__loom_prepared_node`, which explains
its memory and runtime regression.

## Assessment of Round 3

### Prepared selector unions

Do not execute the existing package unchanged. The repository already has a
selector-union collector and prepared renderer. Previous live evidence showed
it was slower because it performs a second pass and duplicates payload-bearing
objects.

The useful replacement is **traversal-time shaping projection**: compute the
union of required selector values in the child traversal's `RETURN` object,
along with only `_id`, `_key`, and fields required for nested navigation. This
removes payload before it enters the materialized array and avoids the second
prepared pass entirely.

### Compatible aggregate/slice/pivot fusion

Do not implement the current identical-expression classifier as a renderer.
The real query contains unlike consumers over the same source—count, distinct
values, slice, and pivot—not repeated identical expressions. The classifier
correctly produces singleton groups, so enabling its rule cannot change this
query.

Fusion becomes useful only as **leaf-set summary pushdown**: after identity
deduplication, a leaf relationship subquery should return both its bounded
slice and aggregate/pivot summary without first returning a payload array to
the outer root projection. That is a different physical operation from the
current diagnostics group.

### Explicit endpoint-filter traversal

Keep and expand this package. It is the only Round 3 proposal directly aimed
at the profile's largest proven work. It must compare four strategies, because
the current sibling-sharing win can conflict with compound-index equality:

1. native shared multi-type traversal;
2. native independent typed traversals;
3. explicit shared multi-type edge lookup; and
4. explicit independent typed edge lookups using endpoint/type equality.

The inbound multi-type `IN` probe previously fell back to the default edge
index, while equality selected the compound index. A production choice cannot
assume that sharing remains cheaper after endpoint filtering.

## Missing work, reordered by expected impact

### P1 — index-aware traversal strategy selection

Add a typed physical strategy for native traversal versus explicit edge/node
lookup. Prototype nested equality routes first, then the shared root route.
Require the endpoint-first compound index, exact parity, and at least a 10%
whole-query median win before production enablement.

This is the highest-confidence package because it targets the observed
filtered adjacency work and an already-proven usable index.

### P2 — traversal-time shaping projection

Replace post-set prepared arrays with a typed projection attached to the
relationship set itself. Compute repeated selector arrays once while each node
is in scope. Retain `payload` only for an unprepared fallback or unsupported
consumer; retain `_id` only when a nested traversal needs it.

This package should delete or replace the current harmful prepared-set
rendering rather than layering another representation on top of it.

### P3 — leaf-set summary pushdown

For a materialized child with no navigated descendants, lower aggregate,
pivot, and representative-slice outputs into a typed summary subquery. The
summary should perform identity deduplication once, then emit named outputs.
The outer root `RETURN` reads the summary object instead of repeatedly looping
over a child payload array.

This is the production form of useful “fusion.” It must preserve count,
distinct ordering, pivot collision reduction, and sort-before-limit.

### P4 — identity, deduplication, and ordering plan

Model identity uniqueness and ordering as physical properties rather than
emitting `UNIQUE` plus `SORT` for every set and another `SORT` for each slice.
Prove whether `_id`/`_key` deduplication can replace object-level `UNIQUE`, and
whether a sorted/deduplicated source satisfies a slice's order.

Do not start with the duplicated `_key` text cleanup alone; it is correct but
unlikely to move the whole query. The package matters only if it removes
materialization/sort executor work.

### P5 — batch-oriented root execution prototype

If P1–P4 do not bring the warm query below roughly three seconds, prototype a
set-oriented plan: materialize the 1,000-root window once, perform edge/node
lookups for the root set, group by root identity, and assemble rows afterward.
The current compiler is correlated root-by-root; no prior work package tested
a batch join/aggregation strategy.

This is higher-risk but has more upside than additional expression-level
fusion. It must remain an experiment until auth/generation and row-order
parity are proven.

### P6 — catalog-backed physical costing

Carry relationship counts from validated catalog references into compilation
as read-only statistics. Use them to choose shared versus independent and
native versus explicit traversal, and to bound retained-set width. Statistics
must never change semantics and must have a deterministic no-statistics
fallback.

This package enables correct strategy choice; it does not count as a speedup
unless it selects a measurably faster rendered plan.

## Recommended execution order

```text
Wave 1, parallel experiments:
  P1 endpoint lookup: nested paths and root shared/unshared matrix
  P2 traversal-time projection: selector/retention prototype
  P4 identity/order: prove which sorts/UNIQUE operations are removable

Wave 2, serialized compiler merges:
  P1 production strategy
  P2 production projection
  P3 leaf summary pushdown
  P4 proven ordering/dedup changes

Wave 3:
  re-profile exact GDC and generic corpus
  implement P5 only if the execution phase remains above the target
  add P6 when more than one physical strategy has survived profiling
```

Rich fusion and prepared selectors should not remain standalone production
goals. They should be absorbed into traversal-time projection and leaf summary
pushdown, where they can remove an actual materialization boundary.

## Completion target

The next round should target more than “below the old 6.34-second baseline.”
That gate has already been met. A meaningful next milestone is:

- exact result and scope parity;
- zero full scans;
- endpoint-first indexes selected where expected;
- no more than 200 MB peak memory for this request; and
- five-run warm Arango median below 4.5 seconds after P1/P2, with a stretch
  target below 3 seconds after leaf pushdown or batch-oriented execution.

Any package that cannot identify removed scanned items, removed payload
materialization, or removed sort/list work is not an AQL translation
optimization and should not be merged as one.
