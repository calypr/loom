import { test } from '../helpers/fixtures.mjs';
import { basicNamedCohortPresenceWorkflow } from '../workflows/basic-named-cohort-presence.mjs';

test.describe('Basic named-cohort related PRESENCE', () => {
  test.use({
    scenarioID: 'basic-named-cohort-presence',
    caseName: 'nonempty-zero-match',
    fixtureDir: 'testdata/basic-named-cohort-presence-zero-match',
  });

  test('Basic native PRESENCE maps a nonempty zero-match Group to false with exact true comparator and full lifecycle', async ({ page, workflow, loomContext }) => {
    test.setTimeout(300_000);
    await basicNamedCohortPresenceWorkflow({ ...workflow, page }, loomContext);
  });
});
