# Local verification feature map

Run focused UI/Go checks first, then the matching real-Chrome spec against a
Doctor-validated development stack. See [verify](../SKILL.md) for ownership,
environment, data preflight, diagnostics, and cleanup.

| Guarantee | Browser spec |
|---|---|
| [Authoring and starting records](authoring.md) | `scripts/e2e/root-change.spec.mjs`, `record-network.spec.mjs` |
| [Preview and publication](publication.md) | `scripts/e2e/author-publish-view.spec.mjs`, `group-pivot.spec.mjs`, `related-field.spec.mjs` |
| [Viewer and export](viewer.md) | `scripts/e2e/author-publish-view.spec.mjs` |
| [Development iteration](iteration.md) | No HMR acceptance spec. Use Doctor and focused checks. |

`make e2e` runs the complete local browser set. Individual specs run with
`node --test --test-concurrency=1 scripts/e2e/<name>.spec.mjs` and the target
variables from the skill. These mappings describe assertions, not run results.
Record final source and test evidence before claiming coverage. CI placement
and cadence require their own operator decision.

The fixture oracle comes from `testdata/devloop-fixture/*.ndjson`.
`dev-patient-001` has two related Observations, with values 172.5 and 180;
`dev-patient-002` has one, with value 68. Additional Observations exercise other
graph paths. Derive whole-population counts from the files.
