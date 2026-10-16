import { test } from '../helpers/fixtures.mjs';
import { cohortGroupPivotPublicationWorkflow } from '../workflows/cohort-group-pivot-publication.mjs';

test.describe('Basic named cohort Group→Pivot publication', () => {
  test.use({
    scenarioID: 'cohort-group-pivot-publication',
    caseName: 'basic',
    fixtureDir: 'testdata/basic-unpivot-pivot',
  });

  test('native cohort Group→Pivot publishes and reloads exact Viewer counts', async ({ page, workflow }) => {
    await cohortGroupPivotPublicationWorkflow({ ...workflow, page });
  });
});
