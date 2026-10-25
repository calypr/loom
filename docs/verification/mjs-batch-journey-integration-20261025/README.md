# MJS batch closeout

This records two existing focused checks, their explicit hash-validated runtime
join, and one registered CDA browser case on promoted source
`e96ca4b28e7c748104a00447b9d2316537f175ef`. It adds no journeys or focused
groups and deletes no source files.

## Focused checks

[The checked-in manifest](first-batch.json) is byte-for-byte identical to the
manifest used (SHA-256 `046609b3ae434d56f583b2128a445260bfcc33ab3b3c6d9e81eff3c4be86437a`).
It selects `builder-authoring:table-reorder` and
`basic-temporal-filter:date`.

The original run command was:

```bash
node scripts/run-native-verification-bracket.mjs \
  --batch /private/tmp/loom-batch-journey-integration/first-batch.json \
  --checks-only \
  --evidence-parent /private/tmp/loom-batch-journey-integration \
  --concurrency 1
```

From this checkout, use
`docs/verification/mjs-batch-journey-integration-20261025/first-batch.json` as
the `--batch` value. Both groups passed; the shared bracket was `NOT_RUN`, and
both cases report `nativeStatus: not-run`.

The byte-for-byte batch summary is [evidence/batch-summary.json](evidence/batch-summary.json)
(SHA-256 `d06c8e687e0c9264da0432cd6fa9714a21a8d5a2a6e2dfaaa9fa6eb59ba9a41d`).
The two case summaries are [table-reorder](evidence/table-reorder/summary.json)
(SHA-256 `19f8cdffd04183a2d25aa807f9fce024b06393129b44140487943d190a39650b`) and
[date](evidence/temporal-filter/summary.json)
(SHA-256 `a8a702c820fc4eef74220c701ccb809c80596b592a4b5ba54c2c130229d2b88e`).
Their captured stdout/stderr logs are retained in the matching `logs/`
directories.

The exact summaries were joined from the candidate worktree with:

```bash
node scripts/build-test-obligations.mjs \
  --runtime-summary /private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-verification-brackets/run-builder-authoring-table-reorder-ODB24O/summary.json \
  --runtime-summary /private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-verification-brackets/run-basic-temporal-filter-date-mxALrZ/summary.json \
  --runtime-output /private/tmp/loom-batch-journey-integration/focused-command-join.json
```

The output is [evidence/focused-command-join.json](evidence/focused-command-join.json)
(SHA-256 `377d1f77e9f9e98f7fd7aec34a36779c2391417c87f9474966ec2a8b3f5754bb`).
It credits only these two files as `focused-command-passed`:

- `scripts/verify-ui/helpers/tests/ndjson-resource-oracle.test.mjs`
- `scripts/verify-ui/helpers/tests/basic-temporal-filter-oracle.test.mjs`

For a rerun, use the new per-case `summaryPath` values printed by the batch and
an external `--runtime-output` path. The join validates the current plan and
complete inputs. It does not prove execution of pinned `sourceFiles`, a browser
journey, or TLA. The canonical static inventory still marks all 676 active MJS
rows unverified.

## Native CDA case

After promotion, `make dev-doctor` returned `DEV_DOCTOR_PASSED`. The exact dataset
preflight returned `DATASET_READY` for project `loom_dev_cda_fhir`, generation
`repo-c0ec9689932caec424ad34a9`, Explorer `loom-cda-add-column-1`:

```bash
node scripts/verify-ui/check-loaded-dataset.mjs \
  --api http://127.0.0.1:8180 \
  --project loom_dev_cda_fhir \
  --generation repo-c0ec9689932caec424ad34a9
```

The native bracket used the approved owned target:

```bash
node scripts/run-native-verification-bracket.mjs \
  --scenario standalone-reshape-group-edit-before-related-column \
  --case group-edit-before-related-column \
  --target /private/tmp/loom-group-scraps-owned-cda-target.json
```

It passed 16/16 required checks and the domain report. All 28 render checkpoints
passed (maximum 4,371 ms); no owned requests remained pending. Source integrity
matched before and after at SHA-256
`9b20ac83075362ba1491bc08a82fcfea6e1c1e2df7fbce2f1b6f0efc2b758406` (2,091
files). API identity also matched before and after:
`db279cf6a3b2f32e816e94fdd3e43c9871f829477584a4f15189f8a0460bc6be:db279cf6a3b2f32e816e94fdd3e43c9871f829477584a4f15189f8a0460bc6be:0bf9e8e1cc663aeeb2f2704fe8d326ea8a88b24f824a82385e28b9ae25c91a41`.
The runner records runtime dataset identity as `not-checked`; the separate
preflight above is the generation evidence.

The byte-for-byte [native summary](evidence/cda/native-summary.json) has SHA-256
`0bd874007aae138c157fd81731bad96edcfd9a1511d9a22034b9e2b1809f885b`; the
[native domain report](evidence/cda/native-domain-report.json) has SHA-256
`fcc0da7c8d1adc3b3b32ede9ea4d79d758ff522576387654d02d2e812173544a`. Both,
like the focused summaries and logs, retain their original contents and paths.
