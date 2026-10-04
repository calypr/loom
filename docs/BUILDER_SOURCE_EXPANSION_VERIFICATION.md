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
