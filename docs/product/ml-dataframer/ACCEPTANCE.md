# Verify schema-driven dataframe construction

Use [the active plan](../ML_DATAFRAMER_DELIVERY_PLAN.md) and [ledger](execution.json). Targets below are not measurements. A package needs focused tests, its live DOM journey, literal output and negative-case checks, measured performance, and one coherent review.

## Establish the actual target

Record the source checkout, exact HEAD and dirty-diff fingerprint, Compose project, mounted paths, API/UI URLs, fixture generation, authorization scope, and serving build identity before testing. Check the source watcher rather than trusting a port number. Preserve the populated CDA volumes and existing user Explorers.

Use the [local verification skill](../../../.codex/skills/verify/SKILL.md). Existing commands are:

```bash
rtk make dev
rtk make dev-doctor
rtk make verify-current
rtk make verify-fast
rtk make verify-full
```

`make dev` uses the isolated development workflow, not permission to replace the canonical demo. `dev-rebuild` is for dependency/toolchain changes, not routine source edits. `verify-fast` creates an isolated owned test project. Reuse the populated CDA project for real-data checks; do not reingest the corpus on every iteration.

S01 adds selectable new journeys to the existing driver or adjacent driver modules. Those selectors are planned, not existing commands. UI setup may ingest source fixtures and establish authentication, but it may not pre-create columns, traversals, groups, transformations, or a successful publication that the journey is supposed to author.

## Execute the package journeys

| Journey | User actions | Required evidence |
| --- | --- | --- |
| J01 | Use Add columns from an empty Builder; search concepts and ordinary fields across pages; add three outputs; open Column details and a repeated cell; rename/reorder; Preview, save, reload, export | Zero automatically added feature columns; exactly three requested outputs and stable identities; literal same-owner values, units, choices, and absence; unfamiliar schema-defined path supported without resource-specific UI or graph/path entry |
| J02 | Add a related column without the graph; use Inspect source and Edit in graph; author a five-edge traversal; select a node-local code; cancel a change, then apply, switch views, reload | Actual selected route and code owner preserved; exact alternative values/contributors; inbound direction works; canceled edits change nothing; meaningful ambiguity is not silently resolved by shortest path |
| J03 | Choose starting records; distinguish selected records from all filtered matches; preview/apply Groups; inspect memberships; cancel another change; Expand repeated values; reload/export | Exact selected scope, group membership, and stable identities; explicit overlapping-group behavior; no accidental product of independent arrays; reconstruction of preserved selected source tuples |
| J04 | Use Values, Time and units, and Table shape controls; compare before/after; recode categories; pivot/unpivot; add a typed derived column; cancel and apply, then reload | Every enabled operator has an independent literal oracle; declared order and loss; sample labels; no unknown-unit coercion, hidden vocabulary learning, population change, or mutation of an unrelated column |
| J05 | Use Review dataset; navigate to a blocking column; correct it; Publish; reload Viewer; Explain this value; filter; Download dataset with explicit scope/representation | Same values, row/group membership, output types, and contributors across consumers; exact generation and descriptor/checksum identity; truthful check completeness and export preservation |

At package closure save DOM snapshots, screenshots of the changed interaction, exact request/result identities, and the independent literal comparison. A human review or video is optional unless the user requests it. Automated interaction success is not a usability-study result.

UI01-UI05 are mandatory parts of S01-S05, not a later acceptance phase. Use the controls named in the plan through the DOM. J01 starts with New table and its initial resource-row choice, not a preconfigured table or a graph-created root. Backend setup may prepare source data but must not perform the user action being proved. A passing API test or isolated component test does not substitute for the paired journey.

For changed controls, test keyboard focus/activation and accessible names. Exercise loading, no results, unsupported choices, a recoverable request failure, and stale resolution. Keep drafted edits distinguishable from saved results, prevent duplicate apply, and show a retry path. Confirm that a delayed response for an older selection cannot overwrite the latest table or inspector. Capture wide and narrow layouts for the changed workspace; the table and inspector must remain reachable without clipping the controls.

Each slice owns rendering and existing consumer support for its new shapes. Do not defer a broken Viewer or export shape to S05. Keep browser selectors in the existing driver modules, and narrow the local watcher loop to the changed interaction before running the package journey.

## Use small fixtures with independent expected outputs

Extend `testdata/devloop-fixture` and its fixture generators, without changing existing cases invisibly. Add only records needed by the owning package.

- S01 covers an unfamiliar schema-defined root with reusable datatypes under unfamiliar property names, same code in different systems, multiple coding translations, quantity members, string/integer choice arms, recorded absence, nested equal leaf Extension URLs under different parents, primitive metadata without a value, unknown fields, and 1,000 concepts beyond the old 512 bound.
- S02 covers two semantically different routes, a five-edge route, inbound fan-out, a finite repeated/self-loop route if compiler-supported, an unavailable reference, and explicit search truncation. Same resource types do not make two routes equivalent.
- S03 covers missing group keys, typed key collisions, overlapping explicit groups, empty members, unlinked resources, equal-valued distinct members, and independent repeated scopes of lengths two and three. Specify which operations preserve order/membership and which deliberately change it.
- S04 covers zero/one/many contributors, tied timestamps, missing anchors, compatible/incompatible units, category code versus label changes, unexpected categories, pivot duplicates, new categories after pinning, numeric precision, and division by zero.
- S05 covers false, zero, empty string/list, absent values, explicit recorded absence, large integers, literal export-null-marker text, unauthorized records, stale source generation, interrupted publication, and retained old artifact reads.

Keep expected selected tuples and output rows authored independently from the implementation. For a preserving operator, reconstruct and compare the selected values, types, multiplicity, owner/member identities, and coordinates. Comparing counts alone fails. For a reducing operator, compare the exact result and declared contributing records and identify the information discarded.

On `/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META`, choose at least two observed tasks with different supported row roots and one related/repeated feature. Record the actual available paths/codes and dataset manifest. The synthetic fixture supplies hostile cases absent from CDA. Do not require Patient, DocumentReference, or a particular specimen relationship.

## Keep the inner loop proportional

| Change | Inner loop | Closure |
| --- | --- | --- |
| Schema or semantic extraction | Focused generated-metadata/walker/association tests | Executed owner-record values and trace, then J01 |
| Query/row/operator semantics | Changed package and direct-importer tests; one literal executed fixture | Owning journey and independent membership/value checks |
| API or persisted intent | Generated-contract checks, positive/negative parse fixtures, migration/CAS tests | Old draft reload, retry/stale cases, and owning DOM journey |
| UI only | Focused component tests plus the changed DOM action | Owning journey and visual/keyboard inspection |
| Watchers or test orchestration | Existing driver safety tests | `verify-full` recovery and source-to-result timing |

Run relevant existing commands, narrowed to changed packages during iteration:

```bash
rtk go test ./internal/fhir/schema ./internal/fhir/semantic
rtk go test ./internal/explorer/... ./internal/dataframe/... ./internal/server
rtk make openapi-check graphql-check dataframe-boundaries
rtk npm --prefix ui test
rtk npm --prefix ui run build
```

At S05 run `rtk go test ./...`, applicable race tests for changed concurrent lifecycles, generated checks, UI tests/build, all J01-J05 journeys, `rtk make verify-full`, and the populated CDA proof at one integrated checkpoint. Add an actual artifact reader round trip with typed comparison. Do not merely compare Preview to another consumer of the same faulty query.

## Measure the user-visible result

Record fixture, source/build identities, machine/Docker allocation, run count, errors, median, and p95 where applicable. Measure cold startup separately from warm interactions.

| Metric | Release target | Probe |
| --- | --- | --- |
| Catalog or construction-choice response | Warm p95 at most 2 seconds | 30 requests in the same authorization, generation, and table context |
| Small-fixture Preview | Warm p95 at most 5 seconds | 30 requests; wait for current-result identity and literal rows, not a loading-state change |
| Local feedback | Warm p95 at most 250 milliseconds | 30 actions; visible pending/saved/error acknowledgement |
| Warm development loop | Median at most 30 seconds | Five source-edit-to-asserted-result cycles covering both Go rebuild and frontend HMR |
| Comparable query/export work | No repeatable median regression above 20 percent | Five interleaved baseline/head pairs on identical data, intent, authorization, and result correctness |

If the baseline cannot perform the new operation, record that fact. Measure the added work and complete user wait against an absolute budget fixed before optimization. Never compare a lossy baseline result with a correct preserving result as if they were equivalent. Large CDA ingestion or full export is not promised within 30 seconds.

Read catalog pages server-side, bound examples, and cache by schema/semantic revision plus the appropriate dataset/authorization/context identities. Do not transmit all source members or enumerate all routes into browser memory. Record peak application/browser memory for a generated 10x-scale case when grouping, pivoting, or artifact buffering changes.

## Record acceptance without inventing completion

For each package retain an evidence report with exact source/build identities, fixture/generation/scope, commands and exits, journey results, screenshots/DOM, literal comparisons, negative cases, performance, support limits, and reviewer judgment.

Store reports under `docs/product/ml-dataframer/evidence/` when accepted. Large DOM/screenshots/artifacts may remain in `.artifacts/loom-dev/` with stable hashes and pointers in the report. A measured KPI needs a nonempty observation and repository-local evidence. An accepted package needs every task accepted, all KPIs passed, all six gates passed at `verification.sha`, and accepted prerequisites.

Validate plan structure with:

```bash
rtk node --test scripts/validate_ml_dataframer_plan.test.mjs
rtk node scripts/validate_ml_dataframer_plan.mjs
```

The validator checks document/ledger agreement, dependencies, evidence references, acceptance fields, and archive integrity. It does not certify the contents of a screenshot or claim a runtime feature is finished.
