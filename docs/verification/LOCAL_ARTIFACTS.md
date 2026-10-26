# Local verification reports

Keep executable tests, verifiers, fixture inputs, TLA models, required-check registrations, source pins, and authored proof decisions in Git. Expanded indexes and summaries are generated outputs.

The obligation index lives in `.cache/builder-verification/test-obligations.json`. Its Markdown summary is beside it. `.cache/` is ignored. Both files can be deleted and rebuilt.

From the repository root, run:

```sh
bash scripts/prepare-builder-reports.sh
node scripts/build-test-obligations.mjs --check
```

Preparation validates the source catalog, reviewed source hashes, registry references, and model associations before writing local reports. Existing `--check` commands still recompute and compare output byte for byte. A corrupted or stale local report fails; generating a report does not count as passing its check. Invalid source inputs still fail.

Preparation also builds the route-action, contract-proof, browser-case, and mutation-probe proposals, bug-discovery summary, and readiness report in the same ignored directory. Run it before standalone report checks or focused-check migration commands. The formal runner and catalogue CI already prepare the reports they use. Authored proposal decisions and route overrides remain tracked; preparation does not replace or apply those decisions.

For the expanded script and semantic-claim view, run:

```sh
node scripts/build-test-obligations.mjs --details --json /tmp/loom-obligation-details.json
```

That export is diagnostic information, not additional test execution or a browser lifecycle pass. Runtime evidence, source freshness, and model replay remain separate checks.

## Authored data and retained evidence

`mjs-source-semantics.json` and its data records contain manually reviewed claims and decisions. They are not reproducible merely by scanning source code. They remain versioned inputs until those decisions have a smaller authoritative replacement. An automatic index must not invent a source review or report that a test passed.

The UI inventory stores compact source and fragment pins. `python3 scripts/normalize_ui_contract_inventory.py --expand` prints its expanded view without refreshing those pins.

Required historical browser reports remain in the retained-evidence archive with raw and compressed hashes. The verifier reads and validates those exact bytes. Disposable reports without active consumers are removed from the checkout; their closure records describe recovery from Git history.

GitNexus stores its machine-local index in the ignored `.gitnexus/` directory. Rebuild it with `gitnexus analyze . --index-only` when needed. It is navigation data and does not establish test coverage or runtime correctness.
