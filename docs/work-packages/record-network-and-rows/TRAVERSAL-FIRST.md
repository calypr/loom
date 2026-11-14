# Traversal-first Builder

The current follow-up changes the v1 presentation direction. GRAPH-ROWS-08–11's
separate row-expression and Both modes are implementation history. The authored
traversal now owns navigation; existing row, value and construction capabilities
remain contextual operations over the same recipe and proposal owner.

This follow-up is **in progress**. Historical native evidence in VERIFICATION.md
belongs to its recorded source and does not establish this refactor's acceptance.

## Coverage baseline

Baseline commit: `2e12d15e9460858dbca63ac158954e32b2166910`.
Default pin: [fixtures/default-dataframer.json](fixtures/default-dataframer.json),
SHA-256 `998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad`.
The owned cloud API's mounted recipe has that same hash. Authenticated Calypr's
active recipe and deployed bundle have not been inspected.

Before implementation, the source audit inventoried every original recipe entry
and parameter in `work/traversal-first-coverage/inventory.json` in the isolated
candidate checkout. That generated report remains disposable evidence; the
checked-in pin is the complete source of original selectors, arguments and order.
It contains five outputs, 69 source-operation descriptors, and 32 concrete fields
inside 13 field groups. Concrete fields are not 32 extra execution stages. Five
new OUTPUT descriptors expose existing output settings without changing old IDs.

| Output | Root / grain | Descriptors | Explicit field slots | Authored traversal scopes |
| --- | --- | ---: | ---: | --- |
| DocumentReference | DocumentReference / file | 24 | 12 | root; Specimen; Specimen → Patient; ResearchStudy; Observation |
| ResearchSubject | ResearchSubject / study_enrollment | 19 | 7 | root; Patient; Patient → Observation; Patient → Condition |
| MedicationAdministration | MedicationAdministration / resource | 10 | 5 | root; Patient |
| Specimen | Specimen / specimen | 15 | 6 | root; Patient; Observation |
| GroupMember | Group / expanded | 6 | 2 | root.member[]; explicit expanded-row identity |
| Total | | 74 | 32 | |

Inventory `operationEntries` includes individual field slots; those counts must
not be confused with source descriptors or physical compiler stages.

The source-plan list records authored positions and dependencies, not physical
compiler phases. Existing lowering applies authorized scans and required-match
membership filters, explicit expansion, scoped projections/pivots, identity and
dynamic projection lowering, column transformations, then construction. It is not
permitted to reorder those phases when grouping operations visually. Sibling and
construction order stay authored.

## Operation checklist

All rows below require final native evidence; implemented controls alone are not
acceptance. Source settings use typed project-local overlays and the existing
compiler. The immutable pin remains authoritative for imported source structure.

| Family / descriptor count | Baseline editing gap | Implemented contextual parameters | Native status |
| --- | --- | --- | --- |
| OUTPUT / 5 | No output-settings editor | Row grain; collision policy | Pending |
| FIELD_GROUP / 13, FIELD / 32 | Group enabled only | Exact field ID; enabled; name/label; selector or supported expression; ordered fallbacks; value policy | Pending |
| CATALOG_PROJECTION / 13 | Enabled, cap, value policy only | Name; include/exclude paths; kinds; naming; existing cap/policy | Pending |
| DYNAMIC_COLUMN / 23 | Enabled, cap, value policy only | Name; explicit/default prefix; source; key/value expressions; existing cap/policy | Pending |
| EXTENSION_COLUMN / 2 | Enabled/cap only | Name; explicit/default prefix; source; existing cap | Pending |
| PIVOT / 6 | Enabled, cap, value policy only | Name; discovery family/path; explicit columns and aliases; column/value expressions; ordered fallbacks; item source/type; existing cap/policy | Pending |
| TRAVERSAL / 10 | Enable and separate match/removal only | Name; authorized target; alias; source expression; existing match/removal proposals | Pending |
| EXPANSION / 1 | Read-only | Source expression; alias; ordinality; empty policy | Pending |
| IDENTITY / 1 | Read-only | Name; existing uuid5/concat/reference expressions and arguments | Pending |

The nine functions present in this pin are `first`, `all`, `reference_id`,
`last_segment`, `coalesce`, `coalesce_string`, `join`, `uuid5`, and `concat`.
The editor preserves argument and fallback order. It does not introduce an
unrestricted query language. Existing unsupported pinned expressions remain
readable and unchanged; new unsupported expressions are rejected.

Source steps keep their original path IDs. Fields use server-verified immutable
slot identities; clients cannot submit column-ID mappings. Coupled alias and
selector changes can be explicitly staged together, then compiled and applied as
one receipt. Invalid downstream dependencies reject the whole candidate.

## UI and ownership

Output tabs, authored traversal scopes, scoped operations, one inspector and one
saved/proposed result replace the two presentation modes and persistent summaries.
Catalog possibilities remain distinct from authored occurrences. Selecting a
record or relationship is inspection; it does not change a saved stage anchor.
The complete ordered source and resulting-table sequences remain available in
secondary disclosures. Existing result operations remain reachable.

Connecting records, requiring a match, collecting values, expanding rows, and
Group remain distinct actions. Existing list policies survive expansion. A scalar
per related match is an explicit separate choice. Root replacement keeps its
existing disclosure and dependency boundary.

Two working internal compositions use identical controls and handlers:
`inspector-right` and `inspector-below`. Hosts select placement through the optional
`LoomExplorerBuilder.workspaceComposition` prop, which defaults to `inspector-right`.
The Vite demo reads `VITE_LOOM_WORKSPACE_COMPOSITION=inspector-below` and passes
the comparison variant through that prop. The reusable package does not read Vite globals;
there is no user-facing mode switch. Final composition selection and native
geometry evidence are pending. Owned standalone evidence cannot establish fit
inside authenticated Calypr's navigation frame.

## Verification checkpoint

These results are scoped evidence, not completed native acceptance.

- Integrated frontend typechecks and focused contract, navigator, source-control,
  row-settings, graph and ownership checks passed against their reviewed units.
- Actual producer-valid compiler probes confirmed and repaired two generated-column
  identity defects: renaming a shadowed explicit field changed a discovered
  column ID, and renaming a newly created Pivot family invalidated a downstream
  Filter. The same independent reproducers now pass; backend review closed.
- Repeated exact-field navigation restores focus. Legacy source plans explicitly
  load editable descriptors without mutating saved digests during GET. Pivot
  alias maps preserve ordinary map keys. Delayed named-group completions cannot
  reopen canceled or obsolete settings. Their exact-source reviews are closed.
- Live API-only checks located all 32 explicit field slots through their original
  source owners and checked independently expected values for all five outputs.
  The current compiler's AUTO policy yields `null` for missing `based_on`;
  this is distinct from explicit ALL's empty list. The fixture oracle preserves
  that existing behavior. These checks did not Apply or establish persistence.
- A real native preflight exposed clipped Apply/Cancel controls in a tall source
  inspector. The reviewed portal-height repair passed the same geometry checks
  at 900, 1100 and back to 900 pixels in the right-inspector composition.
  This is partial standalone evidence, not completed A/B or embedded acceptance.
- The final affected UI snapshot passed 53 focused tests and the complete library
  and demo build (`npm run build` in `ui`). Integrated native oracle/guard checks
  passed 79 Node tests. Test-only repairs retain the existing selector-only Pivot
  boundary and exact generated-column naming; compiler semantics are unchanged.
- Native harness integration, fixture provenance, active-owner write guards and
  exact source collection have scoped same-model review receipts. The unavailable
  `pstack:interrogate` skill was not invoked; manual exact-source review is not
  claimed as cross-model assurance.
- Full A/B comparison with a real 100-operation history, all nine source-family
  persistence cases, legacy upgrade, network, related rows and Group are pending.
  The separate authenticated Calypr acceptance requires authorized credentials,
  access to the embedded target, and a matching deployed bundle. The active
  deployment's recipe and frame have not been inspected.

## Authorized checkpoint — 2026-11-13

The operator explicitly requested committing and pushing the current implementation
with these known gaps. This checkpoint is not completed acceptance.

- Native OUTPUT lifecycle passed: Preview, Cancel, Apply, reload, edit and restore.
  `node scripts/run-traversal-first-native.mjs --case source-output` exited 0;
  owned standalone evidence only, source fingerprint
  `8d5b2e5cbd56cc45ab2dddc11a89820c7b15f9849c55f96fd2b8bb53997c5b0d`.
- Native FIELD_GROUP stopped on a test locator expecting one field summary but
  finding two. The failure is retained; field persistence and real stale recovery
  have not passed final native acceptance.
- Default-recipe history preparation timed out after four and, in a fresh run,
  33 applied operations. The fifth candidate independently retried successfully
  in about four seconds. The cause remains unattributed. DERIVE preview has an
  existing 10-second budget; endpoint time includes work before that budget.
  No timeout limits were raised. Full 100-operation A/B comparison is incomplete.
- Remaining source-family, legacy, network, related-row and Group native cases
  are pending. Historical checks are not relabeled as this snapshot's acceptance.
- Authenticated doctor exited 2: `set LOOM_E2E_TOKEN or pass --token-file PATH`.
  No authenticated Calypr frame, deployed bundle or deployment recipe was tested.
- Exact-source reviews used independent same-model agents; the unavailable
  `pstack:interrogate` skill was not invoked. This limitation remains explicit.

Reproduction uses the checked-in `prepare-traversal-first-targets.mjs` owned-fixture
producer and `run-traversal-first-native.mjs --case NAME` entry point. Target proof
paths/hashes and the owned runtime configuration must be supplied as documented
in those scripts; credentials and disposable runtime evidence are excluded from Git.

Pushing this checkpoint is authorized. Deployment and production changes are not.
