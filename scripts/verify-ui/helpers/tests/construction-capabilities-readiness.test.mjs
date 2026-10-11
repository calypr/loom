import assert from 'node:assert/strict';
import test from 'node:test';
import {
  builderCapabilitiesIdentityFromState,
  readBuilderCapabilitiesIdentity,
} from '../construction-capabilities-readiness.mjs';
import { recordPlaywrightTiming } from '../playwright-authoring-page.mjs';
import { createReport } from '../report.mjs';

const scope = {
  apiOrigin: 'http://127.0.0.1:30008',
  project: 'loom_dev_verify_caps',
  explorer: 'fresh-caps-explorer',
};
const expected = {
  outputId: 'output-final',
  snapshotToken: 'snapshot-final',
  draftVersion: 7,
  draftDigest: 'digest-final',
};
test('builder identity read is bound to the exact project, Explorer, output, and caller deadline', async () => {
  let capturedURL;
  let capturedSignal;
  const identity = await readBuilderCapabilitiesIdentity({
    apiOrigin: scope.apiOrigin,
    project: scope.project,
    explorer: scope.explorer,
    outputId: expected.outputId,
    timeoutMs: 500,
    fetchImpl: async (url, options) => {
      capturedURL = String(url);
      capturedSignal = options.signal;
      return {
        ok: true,
        status: 200,
        json: async () => ({
          catalog: { snapshotToken: expected.snapshotToken },
          draftVersion: expected.draftVersion,
          draftDigest: expected.draftDigest,
          workspace: { documents: [{ output: { id: expected.outputId } }] },
        }),
      };
    },
  });
  assert.equal(capturedURL, `${scope.apiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/builder`);
  assert.equal(capturedSignal.aborted, false);
  assert.deepEqual(identity, expected);
  assert.deepEqual(builderCapabilitiesIdentityFromState({
    catalog: { snapshotToken: expected.snapshotToken },
    draftVersion: expected.draftVersion,
    draftDigest: expected.draftDigest,
    workspace: { documents: [{ output: { id: expected.outputId } }] },
  }, { outputId: expected.outputId }), expected);
  await assert.rejects(readBuilderCapabilitiesIdentity({
    apiOrigin: scope.apiOrigin,
    project: scope.project,
    explorer: scope.explorer,
    outputId: 'output-not-in-builder',
    timeoutMs: 500,
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        catalog: { snapshotToken: expected.snapshotToken },
        draftVersion: expected.draftVersion,
        draftDigest: expected.draftDigest,
        workspace: { documents: [{ output: { id: expected.outputId } }] },
      }),
    }),
  }), /exactly one requested output/);
});

test('recordPlaywrightTiming passes only the remaining shared budget to readiness settlement', async () => {
  const report = createReport({ scenario: 'readiness-test', target: {} });
  const page = { waitForFunction: async () => new Promise(resolve => setTimeout(resolve, 15)) };
  const workflow = { action: async (_label, _locator, perform) => perform() };
  let settlementTimeout;

  await recordPlaywrightTiming(report, page, workflow, {
    name: 'Apply and settle capabilities',
    action: async () => new Promise(resolve => setTimeout(resolve, 15)),
    after: 'true',
    timeout: 100,
    budget: 100,
    settle: async ({ timeoutMs }) => { settlementTimeout = timeoutMs; },
  });

  assert.equal(report.actions[0].status, 'passed');
  assert.ok(settlementTimeout > 0 && settlementTimeout < 90,
    `settlement must receive the remaining deadline, not a fresh budget; got ${settlementTimeout}`);
  assert.ok(report.timings['Apply and settle capabilities'] <= 100);
});
