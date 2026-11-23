# Recipe and compiler redesign research

Status. Research at `bb50db30b` on `internal-cleanup`. Hold implementation
until the unresolved evidence gates below are satisfied. This plan supersedes
the assumption that the current compiler can be replaced in one cutover.

## Recommendation

Keep the v1 archive intact. Prototype the resolver and emitter behind the
existing entry points before deciding to publish another wire format. Keep
PROJECT as a projection tree inside the step list. Normalize navigation after
resolution into a hop that carries exact catalog edge identity and occurrence
provenance. Keep the unresolved navigation contexts distinct.

Use result and error equivalence as the cutover oracle. Include ordered output
columns, row identity, null versus missing, typed bind values, and scope.
Compare canonical IR as a diagnostic. Require separate exact checks for stored
archive digests, receipt identity, and any renderer snapshot contract.

## Evidence sources and limits

All numeric entries in the corpus tables and test evidence below are measured
unless explicitly labeled otherwise. The test corpus measures exercised shapes.
It cannot estimate production feature adoption.

- The owned NCPI acceptance run passed at the research base. Browser smoke was
  disabled. The export used read-only AQL in the owned database. It returned
  one registry entry, one receipt, two workspaces, one draft revision, and one
  published revision. It returned zero recipe revisions, workspace projections,
  or imported bundle pins. The absence of imports limits the stored-data claim
  to this project.
- The default CLI recipe was read from `/app/testdata/recipe.json` in the owned
  API container. It matched the tracked NCPI acceptance recipe byte for byte.
  Its SHA-256 is
  `23ff30215ec4b909c367cb1b743fbed66f72f1e0c907fb77c5803e288a0cd737`.
  The historical default copies are a separate stratum.
- The repo has 13 JSON compiler conformance fixture envelopes at this base.
  The translation probe read 18 checked-in candidates including fixture
  envelopes, standalone recipe fixtures and historical defaults. The original
  assumption of eight fixtures does not describe either measured set.
- Temporary instrumentation at `recipeplan.BuildRecipePlan` captured 619
  distinct attempted recipe encodings and 593 that completed planning, with
  test assertions unchanged. This covers inputs crossing that entry point in
  the Dataframe and Explorer suites. Tests that only exercise archive
  validation, and producers that bypass this entry point, require separate
  capture. Planner success does not prove physical lowering success.
- The existing `archived-table` golden fixture for `tableReshape` is readable and
  digestible in archive mode. Current parse and execution validation reject it
  with `retired_recipe_shape`. Keep this distinction in the migration.

## Corpus usage and corrected mapping

The combined mined corpus contains 615 bundle occurrences, 609 distinct
normalized fingerprints and 666 output occurrences, with 58 distinct route
signatures. It includes the built captures, the conformance fixture envelopes,
both historical default copies, the owned runtime default and its checked-in
counterpart, and the documents from the first owned export. Duplicate
occurrences are counted by source. The later export independently checked
imported bundle pins. The tables count nonempty output fields and construction
operations by source.
The stored stratum contains five recipe occurrences across two distinct
encodings, because the same recipe appears in multiple stored envelopes.
Use the miner's JSON output for the recipe-by-recipe gap list and full route
shapes. The importer and archive replay need a corpus with more than one
acceptance project before any feature is dropped.

| v1 output field | Built test outputs | Fixture outputs | Stored outputs | Corrected mapping |
| --- | ---: | ---: | ---: | --- |
| `name` | 636 | 13 | 5 | Output metadata |
| `rootResourceType` | 636 | 13 | 5 | Root.resourceType |
| `rootOccurrenceId` | 202 | 0 | 4 | Root.occurrenceId |
| `rowGrain` | 636 | 13 | 5 | Output.rowGrain |
| `rootColumnNaming` | 174 | 0 | 4 | PROJECT naming |
| `traversalColumnNaming` | 120 | 0 | 4 | PROJECT naming |
| `fields` | 516 | 11 | 5 | PROJECT fields |
| `filters` | 6 | 2 | 0 | PROJECT filters |
| `pivots` | 17 | 1 | 0 | PROJECT pivots |
| `ownerRecords` | 1 | 0 | 0 | PROJECT owner records |
| `aggregates` | 7 | 0 | 0 | PROJECT aggregates |
| `slices` | 2 | 0 | 0 | PROJECT slices |
| `traversals` | 93 | 8 | 4 | Nested PROJECT scopes with distinct unresolved traversal inputs. |
| `tableReshape` | 0 | 0 | 0 | Archive-only shape. Runtime rejects it today. Translate executable legacy data only if the operator selects an explicit policy. |
| `expand` | 66 | 0 | 0 | PROJECT expansion |
| `groupRows` | 41 | 0 | 0 | Explicit GROUP_ROWS insertion. Empty afterStepId means immediately after PROJECT. |
| `identity` | 65 | 0 | 0 | PROJECT identity |
| `dynamicColumns` | 36 | 0 | 4 | PROJECT dynamic columns |
| `extensionColumns` | 17 | 0 | 0 | PROJECT extensions |
| `catalogProjections` | 12 | 0 | 0 | PROJECT catalog projections |
| `columnTransformations` | 5 | 0 | 0 | Preserve lowering order and the group-row value transformation stage. |
| `derivedColumns` | 7 | 0 | 0 | Ordered legacy DERIVE payload with legacy missing and arithmetic behavior. |
| `construction` | 364 | 0 | 0 | Typed steps with explicit source, prior-stage, table revision, and workspace-output inputs. Terminal COMBINE needs no evaluated PROJECT. |
| `population` | 45 | 0 | 0 | Root population with selection revision, membership evidence, and resolved route provenance. |
| `collisionPolicy` | 96 | 0 | 5 | Carry the policy on PROJECT. Preserve strict and existing alternative collision behavior. |

| Construction kind | Built test operations | Fixture operations | Stored operations |
| --- | ---: | ---: | ---: |
| `CODED_GROUP` | 14 | 0 | 0 |
| `CODED_PIVOT` | 7 | 0 | 0 |
| `COMBINE` | 12 | 0 | 0 |
| `DERIVE` | 35 | 0 | 0 |
| `EXPAND` | 11 | 0 | 0 |
| `FILTER` | 89 | 0 | 0 |
| `GROUP` | 100 | 0 | 0 |
| `PIVOT` | 76 | 0 | 0 |
| `RELATED_ELIGIBILITY` | 11 | 0 | 0 |
| `RELATED_EXPAND` | 58 | 0 | 0 |
| `RELATED_FIELD` | 27 | 0 | 0 |
| `RELATED_SOURCE` | 36 | 0 | 0 |
| `UNPIVOT` | 31 | 0 | 0 |

| Route or unusual combination | Built test occurrences |
| --- | ---: |
| `construction.CODED_GROUP` | 14 |
| `construction.CODED_PIVOT` | 7 |
| `construction.GROUP` | 10 |
| `construction.RELATED_ELIGIBILITY` | 11 |
| `construction.RELATED_EXPAND` | 58 |
| `construction.RELATED_SOURCE` | 36 |
| `population.route` | 6 |
| `project.traversal` | 122 |
| `construction+groupRows` | 26 |
| `construction+population` | 27 |
| `traversals+construction` | 31 |

Fragment definitions and references were absent from these measured strata.
That does not cover inputs normalized before BuildRecipePlan, so fragment
retirement is unproven. The archive fixtures still exercise fragments and
retired reshape separately. The proposed mapping missed `collisionPolicy`
and route provenance. The missing collision policy affects 96 outputs across
78 built test bundles and all five stored output occurrences. Across the combined
corpus it affected 113 output occurrences in 87 bundles. There were 87 ERROR
and 26 OVERWRITE occurrences. The miner lists
the affected recipe digests, output names and fields. See [draft mapping gaps](COMPILER_REDESIGN_GAPS.tsv) for the observed cases. The translator
preserves the collision policy and keeps route input objects intact. This
tests transport fidelity before implementing route normalization.

The schema source is the checked-in HL7 R5 StructureDefinition package `hl7.fhir.r5.core` version
`5.0.0` under
`fhir/r5/definitions`, consumed by `cmd/generate` to produce
`fhir/schema/generated.go`. The resolver uses `internal/fhir/schema`. Treat
the same generated metadata as an input to both the old path and resolution
pilot. Record the schema package version whenever the oracle is rerun.

The history scan identified required invariants in the fixes for traversal and
output ordering (`3a45722ff`), Group route binds (`3de5239c7`), Unpivot source
identity (`1a2e5895c`), filtered population routes (`b84bfb47c`), full-scope
quantity Pivot (`45728fdb8`), related filter anchors (`843deac0e`), Combine output
columns (`bdf005703`), and fragment canonicalization (`e00201467`). Add their
regression inputs to the oracle rather than inferring behavior from commit
subjects alone.

## Prototype findings


| Prototype | Options and evidence | Recommendation |
| --- | --- | --- |
| a. Resolution gate | A direct-root test pilot matched the existing source-shape digest, bound a Patient-to-Specimen occurrence route, and emitted the typed GROUP adapter. The current compiler explicitly rejects an expanded source as a connected route anchor. The initial CODED_GROUP probe established direct-root identity facts. Its production uniqueness gate needs separate evidence. | Pilot from typed shape facts with explicit unsupported cases. Retain the old proof for cases outside the measured set. |
| b. Route unification | The seven-field sketch drops Group step and occurrence identities, the catalog edge pin on the workspace population wire, and PROJECT traversal source expressions. Existing pinned-route and exact Group adapter tests passed unedited. | Keep distinct raw inputs until schema resolution. Normalize exact graph edges and occurrence provenance in one resolved hop; retain lexical projection payloads on their owners. |
| c. Step granularity | Split and rejoined 169 traversals over 106 distinct recipes. The shared emitter produced identical canonical IR in 70 accepted cases and identical rejection in 36. The flatten/join prototype adds 48 lines and parent identities, then rebuilds the tree for the current emitter. Independent emission and reuse were unverified. | Keep the PROJECT tree as one step. The flat representation adds bookkeeping without an observed emitter benefit. |
| d. Equivalence oracle | The unused-bind control changed canonical IR without changing AQL/binds. The wrong-project bind control returned zero rows instead of 100. Separately, 12 of 987 exact AQL/bind groups in the baseline snapshot had multiple input-plan digests. These groupings do not prove equivalence of output processing. | Require result, schema, scope, and exact error parity. Keep IR comparison as a diagnostic and preserve explicit digest contracts. |
| e. SQL-on-FHIR | Upstream v2 reference runner: 135 of 144 official cases passed. Eight boundary-function cases and one invalid non-boolean where case failed. The projection probe found root selection representable in 14 of 18 checked-in outputs and the whole output representable in one ViewDefinition for four. These are reference-runner and structural probe results, not Loom conformance. | Use a separate optional ViewDefinition subset. Preserve graph navigation and shaping outside that subset. Block a conformance claim on the nine failures. |
| f. Resolver placement | Both temporary import stubs passed `go list`: recipeplan importing lower/physical, and lower importing a sibling resolution package that imports recipeplan and lower/physical. The candidate audit failed because the untracked candidate was absent from PACKAGE_AUDIT.csv. The compiler audit dry run identified nine impacted packages. | Favor a sibling resolver when the facts are compiler-specific. Keep recipeplan independent of the compiler. Audit the actual dependency diff once concrete code exists. |
| g. Step encoding | The in-memory decode probe compared single-pass tagged structs with tag-first dispatch over 507 construction operations. Both decoded equal typed values for that set. The probe decoder bodies are 20 and 44 lines respectively, excluding shared declarations and fixture setup. On an M1 Max with two Go workers, three 200 ms samples measured tagged 3.57–3.95 µs and 1,076 B/13 allocations per operation versus dispatch 5.92–6.20 µs and 1,937 B/21 allocations. This isolates JSON decoding, nested allocation, and validation and does not measure compiler latency. It compares a v1-style wire union and a prototype dispatch codec, with different malformed-input paths. | Start from the simpler tagged wire decoder with a single explicit kind/payload check. If adopting a sealed in-memory Step, convert at the boundary and measure again with a proposed v2 wire shape. |

### Translation and result coverage

- The translation probe read 637 attempted and checked-in encodings, including
  one archive-invalid candidate and two canonical duplicates. It translated
  634 unique archive-valid encodings and reproduced the separately exported
  receipt digest. The owned registry bundle also passed. All tested v2 outputs round-tripped byte for byte. The
  prototype preserves v1 archive JSON through a transport reconstruction. It
  has no runtime emitter and explicitly rejects archived reshape.
- A first transport test changed the archive digest when a present empty
  `construction.steps` array came back as null. The prototype now preserves
  that distinction. Keep stored v1 as the checksum authority across a cutover.
- The Go fuzz budget was 2,000 executions with two workers, using 635 seed
  coverage entries from the checked-in and captured recipes. The attempted
  capture manifest SHA-256 was
  `cb1c38ebb15cea69b4f3d0daa9a272bbcaa9e56c1b27800ce7bda2cd60c6c992`.
  No transport divergence occurred. This budget did not fuzz backend results.
- The original and transport-restored acceptance receipt produced identical
  queries, binds, and ordered runtime results with today's emitter. Its 100
  public rows matched the published ClickHouse table as a multiset.
  This isolates transport parity and published-result parity for one output.


- The independent interpreter and both Arango compiler paths agreed on two
  public rows from a fixed four-Observation fixture through PROJECT, PIVOT/SUM,
  DERIVE, FILTER and UNPIVOT. The second compiler path passes through the v2
  prototype and shares the existing emitter. The comparison checks values and
  multiplicity, with public null and missing collapsed. It does not yet compare
  row identity or ordered schema. Direct-root CODED_GROUP and CODED_PIVOT
  passed interpreter fixtures. Full nested PROJECT, related operations,
  COMBINE and fuzzed backend execution remain unverified. Unsupported shapes
  fail closed in the interpreter.


## Operator decisions

| Decision | Evidence and lean |
| --- | --- |
| Adopt wire v2 and use it for new writes | Keep v1 storage during resolver pilots. The stored receipt digest passed and the transport exposed a real nil-versus-empty construction encoding distinction. Decide to write v2 only after validating receipt replay and imports. |
| Drop rarely used v1 features | Keep the existing archive reader and executable mappings. In the built test stratum, owner records occur once, slices twice, and legacy derived columns seven times. Zero stored occurrences in this project cannot justify dropping them. Decide fragment and archive policy independently. |
| Align PROJECT with SQL-on-FHIR ViewDefinition | Favor an explicit compatible subset first. Use official conformance as a separate contract. Preserve graph routes and Builder semantics in the internal projection tree. |
| Keep the current query API | Lean keep. The current adapter builds recipes in memory, so resolver cleanup can proceed without an API break. Preserve row and error contracts until there is a product reason to change them. |
| Cutover window and risk | Lean shadow compare first, followed by a bounded switch. Require replay against multiple owned project shapes and rollback to the old compile path before authorizing a wire migration. |

## Implementation sequence and gates

1. **Complete the corpus and oracle.** Expand raw capture to imports and
   archive-only cases. Freeze corpus manifests. Establish independent
   interpreter, Arango, and ClickHouse result checks for supported features.
   Treat unsupported interpreter cases as coverage gaps. Preserve reproducers
   for each classified product defect.
2. **Pilot resolved facts.** Run the existing compiler alongside a pilot for
   CODED_GROUP and related-occurrence GROUP. Prove stage IDs, shape IDs,
   occurrence identity and source cardinality without relying on emitted IR.
   Include stale binding, pinned-route, and scoped authorization negatives.
   Decide package placement from actual compile and import-graph results.
3. **Pilot the step and route shape.** Keep PROJECT lexical scopes nested.
   Resolve distinct raw route forms to a common internal contract. Require all
   producer and consumer mappings before translating one kind at a time.
   Carry group insertion, branch inputs, exact route provenance and output
   identity explicitly. Publish no v2 writes during this phase.
4. **Shadow emission.** Compare rows, schema, ordering, row identities,
   errors, scopes and materialized publication. Track IR and AQL differences
   alongside results. Add fuzz seeds from observed routes and bug reproducers.
   A shared-emitter transport success does not qualify as this gate.
5. **Decide wire and cutover.** Recheck archived receipt digests and replay,
   current imports, and existing API assertions. Run the full Go, UI,
   integration, browser, and isolated acceptance suites against the final
   source. Set the compatibility and rollback window before moving writes.
6. **Remove legacy runtime paths only after the gate.** Retain archive parsing
   and canonical bytes. Reduce duplicate Explorer checks after coverage
   demonstrates that the resolver owns them. Treat forward/inverse renderer
   consolidation as a separate change with its own lineage oracles.

## Reproduce the probes

Run the capture in a disposable worktree at the recorded base. The patch
instruments planning only. Keep raw recipe bundles, receipt dumps and row
output outside Git.

```sh
git apply --unidiff-zero /path/to/research/scripts/compilerresearch/capture.patch
mkdir -p /tmp/compiler-research-capture
LOOM_COMPILER_RESEARCH_CORPUS=/tmp/compiler-research-capture \
  go test -count=1 ./internal/dataframe/... ./internal/explorer/...
```

From the research worktree, mine each stratum separately. The JSON report
includes mapping gaps with bundle fingerprints and output names.

```sh
python3 scripts/compilerresearch/mine.py /tmp/compiler-research-capture/built-*.json \
  --json-out /tmp/compiler-built.json
LOOM_COMPILER_RESEARCH_CORPUS=/tmp/compiler-research-capture \
  go test ./internal/research/compilerprobe -count=1 -v
LOOM_COMPILER_RESEARCH_CORPUS=/tmp/compiler-research-capture \
  go test ./internal/research/compilerprobe -run '^$' -fuzz '^FuzzTranslate$' \
  -fuzztime=2000x -parallel=2
LOOM_COMPILER_RESEARCH_CORPUS=/tmp/compiler-research-capture \
  go test ./internal/research/compilerprobe -run '^$' \
  -bench '^BenchmarkOperationCodecs$' -benchmem -benchtime=200ms -count=3
```

Follow the repository's real-data acceptance procedure to create a dedicated
compose project before exporting. Supply the owned Arango container and
generated database to `scripts/compilerresearch/export.py`. Set
`LOOM_COMPILER_RESEARCH_RECEIPTS` to the exported `receipts.jsonl` to check its
archive digest. The acceptance comparison also needs
`LOOM_COMPILER_RESEARCH_COMPOSE` and `LOOM_COMPILER_RESEARCH_DATABASE`. It reads
published data and does not modify it.

The SQL-on-FHIR artifacts are pinned to
[`v2.0.0`](https://github.com/FHIR/sql-on-fhir/tree/26e7033df379544d6526495a108bd95d3eac0d44)
and the [official JS evaluator and fixtures](https://github.com/FHIR/sql-on-fhir.js/tree/80854a0aba559d9f0644d95e846e4c83e6b7baac).
Install the evaluator's pinned dependencies, then run
`bun test tests/compliance.test.js` in its `sof-js` directory and
`bun scripts/compilerresearch/viewdefinition.mjs "$PWD" /path/to/sof-js`
in this worktree. The projected Patient date filter and nested Expand examples
matched their independent expected rows. The official runner's nonzero status
is retained. The failed cases cover decimal, dateTime, date, and time
`lowBoundary` and `highBoundary`, plus a non-boolean `where` expression.

ViewDefinition offers FHIRPath predicates and local `forEach`,
`forEachOrNull`, `repeat`, `unionAll`, and typed collection columns. Loom has
partial local equivalents. Full generic FHIRPath and ViewDefinition's
select-level iteration and union semantics are separate capability work.
The reverse mapping needs Loom's cross-resource graph joins, pivots,
runtime dynamic-column discovery, catalog projections, and
construction, group, population and publication contracts.

## Research verification and handoffs

- Base Go tests, the instrumented Dataframe and Explorer suites, transport
  properties, owned acceptance and retained receipt checksum passed.
- The route rehydration and related GROUP pilot tests passed. The resolver
  placement audit for the future package exited 1 because the path has no row
  in the tracked inventory. The compiler-root audit was a dry run.
- The first transport probes exposed prototype defects in empty-value
  encoding, the test registry adapter and fixture scope. The repeated
  transport and backend checks passed with the corrected prototype.
- No new product defect was reproduced in the completed probes. Unsupported
  recipe shapes and the upstream conformance failures remain separate from
  Loom product bugs.

For the fixture differential, use a separate disposable Arango database with
a `compiler_oracle_` name. The test requires explicit permission to create and
remove its own fixture rows. Never point it at exported or canonical data.

```sh
COMPILER_ORACLE_ALLOW_WRITE=1 LOOM_TEST_ARANGO_URL=http://127.0.0.1:PORT \
  LOOM_TEST_ARANGO_DATABASE=compiler_oracle_reference \
  go test ./internal/compilerresearch/... ./cmd/compiler-reference -count=1 -v
```

The read-only `go run ./cmd/compiler-reference -help` tool compares a local
FHIR JSON fixture with today's compiled AQL. It uses an existing dedicated
oracle database and takes recipe, project and generation arguments.
