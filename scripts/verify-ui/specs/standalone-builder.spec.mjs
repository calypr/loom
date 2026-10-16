import { expect, test } from '../helpers/cda-fixtures.mjs';
import { builderCaseActions, runBuilderCase } from '../workflows/verify-cda-builder.mjs';

const sourceOracleActions = new Set([
  'Verify Patient related column',
  'Edit and remove Patient related column',
  'Preview limits',
  ...[
    'Apply missing',
    'Missing proposal',
    'Remove saved filter',
    'Edit saved missing filter',
    'Edit saved filter',
    'Apply known filter',
    'Toggle filter flag',
    'Inspect filters',
  ],
]);
const caseOptions = action => ({
  ...(process.env.LOOM_CDA_PROJECT ? { cdaProject: process.env.LOOM_CDA_PROJECT } : {}),
  ...(process.env.LOOM_CDA_EXPLORER ? { cdaExplorer: process.env.LOOM_CDA_EXPLORER } : {}),
  cdaScenarioID: 'cda-builder-native',
  cdaCaseName: action,
  cdaRequireClickhouse: true,
  cdaRequireSourceFixture: sourceOracleActions.has(action),
});

for (const action of builderCaseActions) {
  test.describe(`CDA Builder: ${action}`, () => {
    test.use(caseOptions(action));

    test(action, async ({ page, cda }) => {
      const result = await runBuilderCase({
        page,
        cda,
        action,
        secondExplorerId: process.env.LOOM_CDA_SECOND_EXPLORER,
        expectedSecondExplorerTable: process.env.LOOM_CDA_SECOND_EXPLORER_TABLE,
      });
      expect(result.assertions.some(assertion => assertion.dimension === 'correctness'),
        `${action} must record a native correctness assertion`).toBe(true);
      expect(result.status, `${action} must report a domain outcome`).not.toBe('running');
    });
  });
}
