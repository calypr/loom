import { test } from './fixtures.mjs';
import { builderLoadWorkflow } from '../verify-ui/builder-load.mjs';

test.describe('Builder load list recovery', () => {
  test.use({ scenarioID: 'builder-load', caseName: 'list', fixtureDir: 'testdata/devloop-fixture' });

  test('shows the list failure and recovers through the in-app Retry', async ({ workflow }, testInfo) => {
    await builderLoadWorkflow({ ...workflow, testInfo }, workflow, 'list');
  });
});

test.describe('Builder load state recovery', () => {
  test.use({ scenarioID: 'builder-load', caseName: 'state', fixtureDir: 'testdata/devloop-fixture' });

  test('shows the state failure and recovers through the in-app Retry', async ({ workflow }, testInfo) => {
    await builderLoadWorkflow({ ...workflow, testInfo }, workflow, 'state');
  });
});
