# Related records as rows

## Decision

Reshape offers **Expand related records** when the selected stage retains an authorized source-record anchor. The researcher chooses a relationship path, optional contributor condition, and one of three explicit no-match outcomes: omit the parent row, retain one row with an empty related record, or refuse the construction. The result has one row per distinct matching related resource, even when several graph paths reach that resource. Each row retains its parent identity and the exact related-resource identity. A later Add columns action reads fields from that exact related resource and can follow another authorized path from it.

The compiler, not the browser, determines whether the selected stage has a usable source anchor and which paths, conditions, and empty policies it can execute. The authoring request persists the server-issued choice, exact route, input stage, and policies. Proposal reauthorizes those identities against the current snapshot before preview. Preview and publication use the same AQL stage. A row identity combines the input row identity, expansion step identity, and related resource `_id`; it never depends on list position. A preserved empty parent uses a distinct empty sentinel.

The existing **Expand a repeated value** operation remains position based and applies to scalar lists. The existing **Add related column** operation still produces a list, distinct-record count, or presence flag on one row. They are separate row-grain choices in the UI.

## Interface sketch

```text
Reshape → Expand related records
  Current rows: one per Patient
  Path: Patient → Encounter
  Qualifying records: all matches, or a server-supported condition
  With no match: omit / retain empty parent / error
  Related record ID column: encounter_id
  Result: one row per distinct Encounter, carrying its Patient row identity

Add columns → From this row's Encounter
  Field: Encounter.status
  Result: status from the exact Encounter that defines this row
```

The stage contract separates row identity from source anchor. Row-preserving steps carry the exact anchor forward. Grouping and pivoting discard it unless their result explicitly retains a unique source record. Capability responses explain why a later related-field choice is unavailable. The backend never infers an anchor from a same-named public column.

## Design comparison

The chosen physical shape is a dedicated route-expansion stage plus an exact-record field binding for later columns. The first candidate also establishes an active source anchor so subsequent route search can begin at the selected related resource, rather than assuming the original table root. The second candidate contributes the explicit field-binding operation and the requirement that hidden parent and related identities survive row-preserving stages. The root agent judged these together against stable identity, general FHIR paths, server-proved choices, and a small authoring surface; a separate Sol cross-judge would repeat the root's model judgment.

Reusing `RELATED_SOURCE` followed by scalar `EXPAND` was rejected: it returns scalar values and creates ordinal row identities, so two related records with the same value become indistinguishable for later field selection. Expanding resource objects in a public list was rejected because it exposes storage shape and still requires a second lookup contract.

## Acceptance check

On a real Arango fixture, two parents with zero, one, and multiple related records must produce the declared rows for each empty policy. Duplicate graph paths to one related document produce one output row. A contributor condition removes ineligible related documents before expansion. Adding a second field reads from the same related document, then a later filter or group consumes that field. Preview, Apply, reopen, ClickHouse publication, and trace agree on row and source identities. A stale or altered route choice is refused before preview. Browser authoring offers only backend-proved paths and exposes editing and removal of the saved step.

## Implementation reconciliation

The dedicated `RELATED_EXPAND` stage, stage-bound route search, proposal reauthorization, and guided Reshape editor are implemented. The compiler retains the input row identity and exact terminal Arango `_id` while the public ID column uses the FHIR resource `id`. A saved step can be edited or removed through the construction history.

On the isolated development stack, the Arango oracle passed duplicate-path deduplication, a FILTER input stage, stable row identities, and `EXCLUDE`, `PRESERVE_PARENT`, and `ERROR` empty-match policies. A live HTTP proposal preview produced three Patient–Observation rows in 105–112 ms on the two-Patient fixture. The step was applied and reopened in a disposable Explorer, then published to ClickHouse. The published query returned the same three Patient–Observation ID pairs and distinct row IDs. The ordinary browser verification journey also passed after integration; its scripted path does not exercise this new editor.

The full acceptance check remains open. Add columns cannot yet read a field from the exact terminal record on an expanded row or follow a new route from that record. The editor does not yet create contributor predicates for this operation, and cell trace does not explain its terminal contributors. A single proposal cannot create a predecessor step and expand from it before that predecessor has become a compiled stage. The route menu and preview timings above use the small development fixture; P08's representative scale gate is unmeasured.
