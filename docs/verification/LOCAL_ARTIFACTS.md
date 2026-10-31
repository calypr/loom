# Local verification reports

Keep executable tests, verifiers, fixture inputs, TLA models, required-check registrations, source pins, and authored proof decisions in Git. Expanded indexes and summaries are generated outputs.

The obligation index lives in `.cache/builder-verification/test-obligations.json`. Its Markdown summary is beside it. `.cache/` is ignored. Both files can be deleted and rebuilt.

From the repository root, run:

```sh
bash scripts/prepare-builder-reports.sh
node scripts/build-test-obligations.mjs --check
```

Preparation derives reports from TLA models, route contracts, executable test registrations, and recorded browser evidence. The formal runner separately validates its pinned source files. Existing `--check` commands still recompute and compare output byte for byte. A corrupted or stale local report fails; generating a report does not count as passing its check. Invalid source inputs still fail.

Preparation also builds the route-action, contract-proof, browser-case, and mutation-probe proposals, bug-discovery summary, and readiness report in the same ignored directory. Run it before standalone report checks. The formal runner and contract CI already prepare the reports they use. Authored proposal decisions and route overrides remain tracked; preparation does not replace or apply those decisions.

To export the derived report outside the checkout, run:

```sh
node scripts/build-test-obligations.mjs --json /tmp/loom-obligations.json --markdown /tmp/loom-obligations.md
```

The report describes coverage associations. Generating it does not execute the listed tests or prove a browser lifecycle. An explicit `--runtime-summary` join validates registered command identity and current input hashes; its version-2 output credits only the executed focused tests.

## Authoritative inputs

`scripts/verify-ui/registry.mjs` declares executable cases, required checks, and focused commands. The Group Pivot Join and DiagnosticReport Append oracle checks are declared directly with their cases. They no longer need a separate attachment file or migration tool.

The former semantic catalog asserted that each MJS file had a review record and manually assigned claim annotations. That bookkeeping gate, its data, and its migration tool were retired. They did not determine which TLA obligations had executable coverage. Normal verification no longer needs the catalog or a per-assertion prose inventory.

Source/API freezes, model source pins, exact focused-command inputs, retained-evidence hashes, and actual assertion/mutation controls remain mandatory in their respective verifiers. Historical catalog and migration data are recoverable from Git commit `3ffcdfa58`; they are not current verification inputs.

The UI inventory stores compact source and fragment pins. `python3 scripts/normalize_ui_contract_inventory.py --expand` prints its expanded view without refreshing those pins.

Required historical browser reports remain in the retained-evidence archive with raw and compressed hashes. The verifier reads and validates those exact bytes. Disposable reports without active consumers are removed from the checkout; their closure records describe recovery from Git history.

GitNexus stores its machine-local index in the ignored `.gitnexus/` directory. Rebuild it with `gitnexus analyze . --index-only` when needed. It is navigation data and does not establish test coverage or runtime correctness.

Filesystem metadata and generated cache directories are also excluded from source fingerprints. Real verifier source and fixture inputs remain covered, so changing either still invalidates source identity.
