# Removed TableShape endpoint caller inventory

Baseline: `/private/tmp/loom-cleanup-wp12`, HEAD `04046c45eb860acbbfc02acb091e7eb04f898cbe`.

Initial command:

```sh
rg -n --glob '*.mjs' --glob '*.js' --glob '*.ts' --glob '*.sh' --glob '*.py' '/table-shape-|table-shape-(capabilities|category-discoveries|resolutions|proposals)|APPLY_TABLE_SHAPE_PROPOSAL|tableShapeProposal' scripts ui/packages/loom-ui/src
```

| File | Initial use | Artifact change |
|---|---|---|
| `scripts/verify-ui/workflows/verify-explorer-pivot-keyword-names.mjs` | Directly POSTed capabilities, category discovery, resolution, and proposal as a TableShape comparison probe; Cancel compared a legacy `tableShape` field. | Removed only the obsolete API comparison probe. Native UI keyword pivot keeps visible Construction proposal readiness, Cancel, Apply, preview grid, duplicate-name negative, positive rename, reload, and removal checks. |
| `scripts/verify-ui/registry.mjs` | Registered the four obsolete routes and required the obsolete proposal comparison check. | Routes now name Construction capabilities, category discovery, proposal, preview, and apply. Required transition/check now matches the native workflow. |
| `scripts/lib/builder-route-evidence.mjs` | Classified removed TableShape POST paths as read-like requests. | Removed stale route classifiers. |
| `scripts/normalize_ui_contract_inventory.py` | Transcribed old API request schemas and generated legacy TableShape capability/resolution/proposal/apply flows. | Removed obsolete endpoint contracts and maps historical pivot/unpivot/derived inventory rows to Construction kinds and Construction capability/category-discovery/proposal/preview/apply routes. |
| `scripts/verify_ui_contract_inventory.py` and `scripts/tests/test_verify_ui_contract_inventory.py` | Required/extracted `tableShapeProposalIntentSchema` and `TABLE_SHAPE:` backend kinds. | Validator and fixture now use current Construction discriminants only. |
| `scripts/loom-dev.mjs` and `scripts/loom-dev.test.mjs` | Retained the historical J04 UI04 route, stale proposal command, and `verify-j04` command. | Removed the retired TableShape-only J04 browser function and command, preserving J04 fixture and Patient operator helpers. Retired promises are detailed in [`docs/verification/DELETED_GUARANTEES.md`](../../docs/verification/DELETED_GUARANTEES.md). |

The keyword and registry scans after patch contain no removed endpoint calls. `loom-dev.mjs` contains no TableShape UI04 path or old command. Other tests may mention removed token strings only in negative static guards. The `scripts/measurements/construction-preview/construction_preview_bench.fixture-v1.json` still contains an old UI04 selector string as historical measurement input; it makes no endpoint request and was not changed.

## Pure normalizer check boundary

The normalizer's expanded-inventory command currently exits 1 with `FAIL inventory expansion changed; review and regenerate the compact manifest` because its generated documentation manifest is an input outside this scripts-only artifact. Do not treat this as a passing check. `canonicalize(Path('.'))` independently confirms both historical pivot and unpivot rows now expand only to Construction capability/category-discovery/proposal/preview/commands routes. Regenerate the repository's compact `docs/ui-contract-inventory/ui-supported-affordances.json` after integrating this scripts patch as part of its owning docs update.
