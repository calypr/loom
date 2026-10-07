import test from 'node:test';
import assert from 'node:assert/strict';
import { createPositiveMedicationCdaContext } from '../positive-medication-cda-context.mjs';

const makeContext = ({ caseName = 'medication-positive-fixture', generation = 'cda-fhir-v1' } = {}) => {
  const report = { case: caseName, errors: [], network: [], assetFailures: [] };
  const workflow = {
    target: {
      kind: 'isolated',
      composeProject: 'loom-dev-positive-fixture',
      fixtureProject: 'loom_dev_verify_positive_fixture',
      fixtureGeneration: generation,
      fixtureDir: '/checkout/testdata/cda-zero-column-related-medication-positive',
      apiUrl: 'http://127.0.0.1:8180/',
      uiUrl: 'http://127.0.0.1:30000/',
    },
    report,
    action: async () => {},
    check: () => true,
  };
  const page = {};
  const testInfo = { attach: async () => {} };
  const playwrightTest = { step: async (_label, callback) => callback() };
  return { report, workflow, page, testInfo, playwrightTest };
};

test('positive Medication adapter maps the owned basic fixture to the existing native workflow contract', () => {
  const input = makeContext();
  const cda = createPositiveMedicationCdaContext(input);

  assert.equal(cda.project, 'loom_dev_verify_positive_fixture');
  assert.equal(cda.target.arangoContainer, 'loom-dev-positive-fixture-arangodb-1');
  assert.equal(cda.generation, 'cda-fhir-v1');
  assert.equal(cda.apiOrigin, 'http://127.0.0.1:8180');
  assert.equal(cda.uiOrigin, 'http://127.0.0.1:30000');
  assert.strictEqual(cda.report, input.report);
  assert.strictEqual(cda.report.network, input.report.network);
  assert.equal(cda.diagnostics, undefined, 'the adapter must use the generic runner network as diagnostic authority');
  assert.equal(cda.includeBrowserDiagnostics, undefined);
  assert.strictEqual(cda.check, input.workflow.check);
  assert.deepEqual(cda.report.nativeRequests, []);
  assert.deepEqual(cda.report.apiRequests, []);
  for (const name of ['click', 'fill', 'selectOption', 'navigate', 'inspect', 'wait', 'captureRequests', 'attachReport']) {
    assert.equal(typeof cda[name], 'function', `missing native workflow wrapper ${name}`);
  }
});

test('positive Medication adapter rejects the existing real-CDA case and any other generation', () => {
  assert.throws(() => createPositiveMedicationCdaContext(makeContext({ caseName: 'medication-preserve-parent' })), /only its owned fixture case\/project\/generation/);
  assert.throws(() => createPositiveMedicationCdaContext(makeContext({ generation: 'fixture-v1' })), /only its owned fixture case\/project\/generation/);
});
