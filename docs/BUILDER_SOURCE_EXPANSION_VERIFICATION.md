# Direct repeated-field row expansion

Configure rows offers “Expand a repeated source field” directly. This changes
the source row definition before existing authored operations; it does not
require a list column or a detour through another operation. Field and
empty-record policy changes automatically request a comparison. Apply saves it;
Cancel preserves the current dataframe.

The registered isolated fixture contains one Observation with two component
items, one with a literal empty component array, and one with no component
property. The driver checks the fixture files independently before opening an
owned Explorer.

```sh
LOOM_DEV_SOURCE_ROOT=/private/tmp/loom-construction-implementation \
LOOM_DEV_COMPOSE_PROJECT=loom-dev-6d7df93d6a37 \
LOOM_DEV_API_PORT=8188 LOOM_DEV_UI_PORT=30008 \
LOOM_DEV_PROJECT=loom_dev_c89a69d7e137 \
LOOM_DEV_FIXTURE_DIR=/private/tmp/loom-construction-implementation/testdata/verify-repeated-empty \
node scripts/verify-ui/builder-authoring.mjs --case repeated-empty \
  --report /tmp/loom-repeated-source-direct-rows-restored-values.json
```

Report: `/tmp/loom-repeated-source-direct-rows-restored-values.json.repeated-empty`.
All required checks and all four dimensions passed. The 26 timed actions took
at most 699 ms. No unexpected browser/network failures occurred. The watched
source fingerprint stayed
`ee63669edeebb827fe1ebf4dc0e185d469eab1a54838675ab5ab968a5debb2f0`
across 1122 files.

The case covers direct entry, automatic comparisons, Cancel, Apply, exact
per-item values, PRESERVE_PARENT and EXCLUDE, policy editing, saved row choices,
reload, removal, and restoration of exact source IDs and retained FIRST field
values. Comparisons show row counts and membership digests; they do not display
a candidate cell grid. Exact cells are checked after Apply and reload.

This fixture does not prove CDA composition, grouped member expansion, or
mid-sequence object expansion. Those remain separate coverage items. The
source-row definition currently permits GROUPS or EXPANDED exclusively.

The same Rows entry work guards Pivot only while matching capabilities load.
Settled unsupported states remain available for investigation. The real CDA
root quantity case reached discovery successfully but still returned
MISSING_UNSUPPORTED; this regression is tracked separately in the matrix.

## CDA source expansion followed by authored grouping

The owned CDA run separately exercises source `EXPANDED` on
`Observation.component[]` followed by authored `GROUP` with `COUNT_ROWS`. It
scans the first 1,000 Observation payloads for the exact project and
generation, then independently selects four roots:
`00004888-0740-540e-bc75-68b5192fbf5a` has the two raw component
values `aliquot` and `Adenoma, NOS`; `00006c79-f75f-5d62-9bba-24ae84c2e65a`,
`00016773-de1e-5397-a798-61e5420624cc`, and
`00016dd6-fe35-5663-a1fa-206e1d3ad866` have no component property. With
`PRESERVE_PARENT`, the source preview is exactly the two item rows plus one
empty row for each of those three roots. The composed group output is exactly
four rows: count 2 for the positive root and count 1 for each preserved empty
root. The report also verifies the exact selection revision and unchanged
source-row definition/bindings across composition.

The run also checks that Cancel returns to the exact expanded rows, Apply and
reload preserve the expanded source definition and bindings under GROUP,
removing GROUP restores those rows after reload, and `EXCLUDE` removes the
three no-component roots while retaining the two exact values. An empty-only
`ERROR` result must expose enabled repair choices and Cancel must retain the
saved `EXCLUDE` policy. Fourteen assertions passed across 34 timed actions;
the slowest measured action was 1,621 ms.

```sh
node scripts/verify-cda-repeated-empty-browser.mjs \
  --evidence /tmp/loom-cda-source-expanded-group-fingerprinted
```

Report: `/tmp/loom-cda-source-expanded-group-fingerprinted/report.json` (status
`partial`). Its only gap is the literal `component: []` source shape: the CDA
oracle found missing-property witnesses only. The standalone isolated fixture
above proves literal-empty-array behavior separately; this CDA report does not
claim that literal-array behavior in composition. The source guard verified 1,059 watched files unchanged, and the API-build
guard verified a fresh running build unchanged from start to finish. The aggregate
source fingerprint `ee63669edeebb827fe1ebf4dc0e185d469eab1a54838675ab5ab968a5debb2f0`
also remained unchanged across 1,122 files.

## Authored list expansion: basic native lifecycle passed

The direct Rows action “Make one row per list value” now starts an automatic proposal from its user action, using the same generated form and step identity as subsequent edits. Focused source tests pass 107 cases, including duplicate prevention and owner-change cancellation. This does not establish a browser lifecycle pass.

The first registered basic run, `/tmp/loom-authored-expand-native/report.json.cohort-expand`, independently verified the two fixture Patient IDs, exact cohort revision, native ALL field proposal, and saved member list. It then stopped because the verifier attempted to open Rows while Add columns remained open. The visible Back to table action must be used before Rows. This is a harness navigation failure; EXPAND Preview/Cancel/Apply/edit/remove/reload remain unverified.

Run: `node scripts/verify-ui/builder-authoring.mjs --case cohort-expand --report /tmp/loom-authored-expand-native/report.json` with the owned local stack environment. Real CDA follow-up remains required after the basic lifecycle passes.

Basic full lifecycle passed in `/tmp/loom-authored-expand-header-case/report.json.cohort-expand`: all four dimensions passed, all 18 required checks present, 20 timed actions maximum2000ms, errors[]. Independent fixture IDs remain exact through automatic preview, Cancel/Apply, saved-label edit, removal and reload. Source fingerprint before=after `5b4791ae606e1aeaef953e011a58314d61c14022a9c98c84c3fdffe9c971c950` (1125 files). Prior failures above were verifier navigation/header assumptions, corrected in the same registered case. Real CDA follow-up remains open.

The CDA follow-up now passes in `/tmp/loom-cda-cohort-authored-expand-normalized-document/report.json`. Run `LOOM_COHORT_FIELD=id LOOM_COHORT_AUTHORED_EXPAND=1 node scripts/verify-cda-cohort-fields-browser.mjs /tmp/loom-cda-cohort-authored-expand-normalized-document`. All 22 actions complete within 1838ms, including automatic Preview, Cancel, Apply, reload, saved-label editing, removal and restored member-list reload. The oracle uses two exact scoped Specimen IDs and their pinned selection/cohort revision; retained ALL bindings and normalized source-column IDs remain stable. Source1064/API unchanged and aggregate fingerprint1127 unchanged (`a3b04db8e37a3ab2f62bab418e72b6d2503f5192128ae958fbebfa1d79f73a7f`). This closes the CDA equivalent of the direct authored-list entry; other expansion compositions and empty-list policies retain their separate gaps.
