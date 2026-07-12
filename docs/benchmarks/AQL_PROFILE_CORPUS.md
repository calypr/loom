# Generic AQL profile corpus

`TestProfileCorpusAgainstArango` exercises the Arango adapter against five
schema-independent physical query shapes:

| Shape | Physical work covered |
| --- | --- |
| `root` | scoped root scan, sort, limit, projection |
| `sibling` | three same-parent inbound traversals with typed targets |
| `deep` | two-hop nested traversal |
| `required` | traversal-backed root semi-join |
| `pivot` | repeated value extraction and `COLLECT` grouping |

The test is read-only and opt-in. It uses the same local defaults as the
compiler integration suite and accepts these overrides:

```bash
LOOM_COMPILER_ARANGO_INTEGRATION=1 \
LOOM_ARANGO_URL=http://127.0.0.1:8529 \
LOOM_ARANGO_DATABASE=fhir_proto \
LOOM_ARANGO_PROJECT=ARANGODB_PROTO \
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto \
go test ./internal/store/arango -run TestProfileCorpusAgainstArango -count=1 -v
```

For every shape the test performs both `EXPLAIN` and cursor `PROFILE` level 2
using bind variables. The test requires a project index on the root shape and
an edge index on every traversal shape. It also requires per-node profile
statistics, then logs a stable summary containing result count, execution
runtime, full/index scan counts, and the slowest execution node.

The profile adapter reports Arango's execution plan node IDs and joins them to
node types from `extra.plan`. This makes repeated traversal nodes and nested
subqueries comparable across compiler rewrites without matching generated AQL
variable names. Keep the corpus queries deliberately generic: new FHIR routes
with the same physical shape should be covered without changing this test.

