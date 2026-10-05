# Standalone CDA field workflow conversion map

These eight former standalone browser verifiers now run as native Playwright
Test cases with the shared `scripts/playwright/cda-fixtures.mjs` fixture. Each
case receives Playwright's owned `page` and the fixture's isolated CDA context.
The shared workflow tools use native locators, `expect`, fixture actions, and
five-second browser action and wait deadlines. Raw Arango and source payload
queries remain independent correctness oracles.

| Former source | Native case | Scenario and spec | Independent oracle retained |
| --- | --- | --- | --- |
| `scripts/verify-cda-coded-field-lifecycle-browser.mjs` | `CDA coded field lifecycle` / `coded-field-lifecycle` | `cda-native` in `scripts/playwright/standalone-cda-fields.spec.mjs`; `codedFieldLifecycleWorkflow` | Bounded raw Specimen and related Observation/component values; exact group and row-value ONE/ALL expectations; persisted preview, rename, visibility, removal, and restoration checks. |
| `scripts/verify-cda-cohort-fields-browser.mjs` | `CDA cohort fields` / `cohort-fields` | `cda-native` in the same spec; `cohortFieldsWorkflow` | Raw scoped Specimen witnesses, member IDs, immutable selection/cohort revisions, and exact member rows across Apply, Cancel, edit, collection changes, anchor removal, and reload. |
| `scripts/verify-cda-compound-fields-browser.mjs` | `CDA compound fields` / `compound-fields` | `cda-native` in the same spec; `compoundFieldsWorkflow` | Scoped raw Specimen-to-Observation edge and component values; exact semantic choice identity, ALL form, preview rows, and frame/column restoration. |
| `scripts/verify-cda-contributor-code-browser.mjs` | `CDA contributor code` / `contributor-code` | `cda-native` in the same spec; `contributorCodeWorkflow` | Bounded raw Patient/Observation values plus the quantity witness report; exact suggestion, contributor predicate, zero/one/many row sets, policy edit, and source restoration. |
| `scripts/verify-cda-contributor-exists-browser.mjs` | `CDA contributor exists` / `contributor-exists` | `cda-native` in the same spec; `contributorExistsWorkflow` | Raw zero/one/many Patient-to-Observation witnesses; exact ALL/EXISTS results, ERROR repair response, policy edit, and source restoration. |
| `scripts/verify-cda-contributor-rules-browser.mjs` | `CDA contributor rules` / `contributor-rules` | `cda-native` in the same spec; `contributorRulesWorkflow` | Raw zero/one/many Patient-to-Observation IDs and exact EQUALS result rows; PRESERVE_PARENT/EXCLUDE, Cancel, Apply, reload, and removal checks. |
| `scripts/verify-cda-source-fields-browser.mjs` | `CDA source fields` / `source-fields` | `cda-native` in the same spec; `sourceFieldsWorkflow` | Bounded raw Observation payloads for scalar `status` and repeated `component[].valueString`, including exact IDs, value order, and source types. |
| `scripts/verify-cda-repeated-contributor-any-browser.mjs` | `CDA repeated contributor ANY` / `repeated-contributor-any` | `cda-native` in the same spec; `repeatedContributorAnyWorkflow` | Bounded first-2000 Patient scan and raw nested `Observation.category[].coding[].code` values; zero/one/many witnesses, ANY semantics, exact result rows, policy edit, and source restoration. |

The cohort case preserves its environment-selected variants: `LOOM_COHORT_FIELD`
(`resourceType` or `id`), `LOOM_COHORT_COMPOSITION`,
`LOOM_COHORT_FILTER_ONE`, `LOOM_COHORT_POLICY_EDIT`,
`LOOM_COHORT_POST_FILTER`, `LOOM_COHORT_COLLECTION_ROUND_TRIP`,
`LOOM_COHORT_REMOVE_ANCHOR`, and `LOOM_COHORT_AUTHORED_EXPAND`. The coded
field and contributor-code cases also retain their optional environment inputs
through the `caseOptions` passed by the spec.

Conversion scope is mechanical; no browser lifecycle was run for these cases.
Syntax checks do not establish that any CDA lifecycle passes against a live
stack.
