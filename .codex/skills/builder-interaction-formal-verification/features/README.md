# Builder verification map

This map connects remaining executable suites to user behavior. Run the skill's read-only doctor for the current suite inventory; no generated inventory is checked into Git. Every UI suite belongs to exactly one group. Groups are routing aids, not a claim that every user journey is covered.

| Feature | Fast drive | Formal attachment | Remaining boundary |
| --- | --- | --- | --- |
| [Ownership](ownership.md) | `--feature ownership` | BuilderLifecycle / ApplySettlementRace | Real browser and transport scheduling |
| [Draft editing and restore](drafts.md) | `--feature drafts` | ReconcileLifecycle + generated ReconcileAcceptance; focused Apply checks separately | Actual Arango A/B route/storage profile available; full browser wiring and process-restart durability separate |
| [Construction editors](construction.md) | `--feature construction` | No general formal proof | Full server execution and composed journeys |
| [Tables and Viewer](tables.md) | `--feature tables` | No general formal proof | Layout, drag/drop, publication integration |
| [Client contracts](contracts.md) | `--feature contracts` | No general formal proof | Deployed auth/proxy/server compatibility |

All fast commands start `node scripts/verify-builder-formal-core.mjs --phase behavior` from the repository root. Add the feature flag shown and a fresh `--out` directory. These checks use real production code with jsdom/test-owned external boundaries. Preserve skipped/unreachable paths as gaps. A removed browser guarantee earns a replacement only when its assertions have been mapped and exercised at a suitable boundary; membership in this table is insufficient.

## Other retained checks

Run `--phase inventory` for actual repository CI routes and explicit missing-dependency/fixture gaps; it is discovery, not a pass. `--phase api --feature restore|apply|receipts` connects public HTTP/service checks. Node checks use `node scripts/ci/list-node-tests.mjs --explain` for inclusion and exclusion reasons. Python, shell and general Go checks retain their existing CI commands. These are concrete execution routes; only the documented model-linked guarantees receive formal coverage.

Opt-in boundaries are now executable: `--phase browser --feature drag|download` for native Chromium components, and `--phase api --feature route-rows` for actual Arango round trips. `publication` adds real publish/Viewer HTTP identity checks with an isolated repository/materializer. The recovered Viewer export tests exercise facet-vs-export population, response identity, Cancel/reopen and old-output completion; download bytes are checked separately by the browser fixture.
