import { test } from '../helpers/cda-fixtures.mjs';
import { scope001RestrictedAuthWorkflow } from '../workflows/scope-001-restricted-auth.mjs';

test.use({
  cdaScenarioID: 'scope-001-restricted-auth',
  cdaCaseName: 'restricted-related-records',
  cdaRequireSourceFixture: true,
  cdaUiRouting: 'explicit-query',
});

test('restricted Calypr auth keeps related Builder rows within the granted path across Cancel and Apply', async ({ page, cda }) => {
  test.setTimeout(300_000);
  await scope001RestrictedAuthWorkflow({ page, cda });
});
