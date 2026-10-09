import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {
  installGroupJoinNativeCapture,
  prepareCdaGroupJoinOracle,
} from '../workflows/cda-current-draft-group-join-workflow.mjs';

test('subject.reference witness keeps overlapping Group counts 2 to 1 through Count distinct', () => {
  const rows = [
    { _id: 'Observation/1', id: '1', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/2', id: '2', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/3', id: '3', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/4', id: '4', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/left-only' },
  ];

  const oracle = prepareCdaGroupJoinOracle(rows);

  assert.equal(oracle.groupKeyFieldPath, 'subject.reference');
  assert.equal(oracle.memberships.groupKeyFieldPath, 'subject.reference');
  assert.equal(oracle.memberships.sharedGroupKey, 'Patient/shared');
  assert.equal(oracle.memberships.leftOnlyGroupKey, 'Patient/left-only');
  assert.deepEqual(oracle.expectedLeftInitial, [['Patient/left-only', 1], ['Patient/shared', 2]]);
  assert.deepEqual(oracle.expectedRight, [['Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedLeftDistinct, [['Patient/left-only', 1], ['Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedLeft, [
    ['Patient/left-only', 1, '—', '—'],
    ['Patient/shared', 2, 'Patient/shared', 1],
  ]);
  assert.deepEqual(oracle.expectedInner, [['Patient/shared', 2, 'Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedDistinctInner, [['Patient/shared', 1, 'Patient/shared', 1]]);
});

test('Group/Join installs the exact Explorer probe before opening the shared native request tracker', async () => {
  const events = [];
  let binding;
  let capture;
  const project = 'loom_dev_cda_fhir';
  const explorer = 'cda-cdj-fresh-91b6';
  const apiOrigin = 'http://127.0.0.1:8188';
  const uiOrigin = 'http://127.0.0.1:30008';
  const report = { target: { project, explorer: null, uiUrl: `${uiOrigin}/` }, nativeRequests: [] };
  const tracker = { shared: true };
  const probeSources = [];
  const browserContext = {
    async exposeBinding(name, handler) {
      events.push('exposeBinding');
      binding = { name, handler };
    },
    async addInitScript(source) { events.push('addInitScript'); probeSources.push(source); },
  };
  const page = {
    context: () => browserContext,
    async evaluate(source) { events.push('evaluate-current-document'); probeSources.push(source); },
  };
  const cda = {
    report,
    captureRequests(ownedPathPrefix, options) {
      events.push('captureRequests');
      capture = { ownedPathPrefix, options };
      return tracker;
    },
  };

  const result = await installGroupJoinNativeCapture({ page, cda, project, explorer, uiOrigin });

  const explorerBase = `/api/v1/projects/${project}/explorers/${explorer}`;
  assert.deepEqual(events, ['exposeBinding', 'addInitScript', 'evaluate-current-document', 'captureRequests']);
  assert.equal(binding.name, '__loomNativeAbortProbeBinding');
  assert.equal(probeSources.length, 2);
  assert.equal(probeSources[0], probeSources[1]);
  assert(probeSources.every(source => source.includes(JSON.stringify(uiOrigin))));
  assert(probeSources.every(source => !source.includes(JSON.stringify(apiOrigin))));
  assert.equal(result, tracker);
  assert.equal(report.explorer, explorer);
  assert.equal(report.target.explorer, explorer);
  assert.deepEqual(report.nativeRequestCaptureScope, {
    project,
    explorer,
    selectedExplorer: explorer,
    origin: uiOrigin,
    observedPathPrefix: `${explorerBase}/authoring/v2`,
  });
  assert.equal(capture.ownedPathPrefix, `${explorerBase}/authoring/v2`);
  assert.deepEqual(capture.options, { responsePaths: /commands|construction-proposals/ });
  for (const path of [
    `${explorerBase}/authoring/v2/construction-capabilities`,
    `${explorerBase}/authoring/v2/semantic-inventory`,
    `${explorerBase}/authoring/v2/schema-fields`,
  ]) {
    assert(path.startsWith(`${capture.ownedPathPrefix}/`), `${path} must be inside the captured Explorer scope`);
  }

  const sandbox = {
    AbortController,
    URL,
    Date,
    Error,
    Headers,
    crypto: { randomUUID: () => '12345678-1234-4123-8123-123456789abc' },
    location: { href: `${uiOrigin}/` },
    document: { addEventListener() {}, querySelectorAll: () => [] },
    MutationObserver: class { observe() {} },
    fetch(_input, init = {}) {
      const pending = new Promise((_resolve, reject) => {
        if (init.signal?.aborted) reject(new Error('The operation was aborted.'));
        else init.signal?.addEventListener('abort', () => reject(new Error('The operation was aborted.')), { once: true });
      });
      pending.catch(() => undefined);
      return pending;
    },
    __loomNativeAbortProbeBinding: payload => binding.handler({}, payload),
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(probeSources[0], sandbox);
  assert.equal(report.nativeAbortProbeEvents.length, 1);
  assert.deepEqual(
    Object.fromEntries(['kind', 'project', 'explorer', 'explorerScope'].map(key => [key, report.nativeAbortProbeEvents[0][key]])),
    { kind: 'probe-installed', project, explorer, explorerScope: 'exact' },
  );
  assert.equal(Number.isFinite(report.nativeAbortProbeEvents[0].at), true);

  const fetchAndAbort = async (url, requestId) => {
    const controller = new sandbox.AbortController();
    const pending = sandbox.fetch(url, {
      method: 'POST',
      headers: { 'X-Request-ID': requestId },
      signal: controller.signal,
    });
    controller.abort();
    await pending.catch(() => undefined);
    await new Promise(resolve => setImmediate(resolve));
  };
  const schemaFieldsPath = `${explorerBase}/authoring/v2/schema-fields`;
  const exactRequestId = 'schema-fields-11111111-1111-4111-8111-111111111111';
  const wrongOriginRequestId = 'schema-fields-22222222-2222-4222-8222-222222222222';
  const wrongExplorerRequestId = 'schema-fields-33333333-3333-4333-8333-333333333333';
  await fetchAndAbort(`${uiOrigin}${schemaFieldsPath}`, exactRequestId);
  await fetchAndAbort(`${apiOrigin}${schemaFieldsPath}`, wrongOriginRequestId);
  await fetchAndAbort(`${uiOrigin}${schemaFieldsPath.replace(explorer, 'cda-cdj-other-0b37')}`, wrongExplorerRequestId);

  const abortEvents = report.nativeAbortProbeEvents.filter(event => event.kind === 'abort-controller-call');
  const exactMatches = abortEvents.flatMap(event => event.requests ?? [])
    .filter(request => request.requestId === exactRequestId);
  assert.equal(exactMatches.length, 1);
  assert.equal(exactMatches[0].origin, uiOrigin);
  assert.equal(exactMatches[0].path, schemaFieldsPath);
  assert.equal(exactMatches[0].method, 'POST');
  assert.equal(abortEvents.find(event => event.requests?.includes(exactMatches[0])).signalWasAlreadyAborted, false);
  assert.equal(abortEvents.flatMap(event => event.requests ?? []).some(request =>
    [wrongOriginRequestId, wrongExplorerRequestId].includes(request.requestId)), false);
});
