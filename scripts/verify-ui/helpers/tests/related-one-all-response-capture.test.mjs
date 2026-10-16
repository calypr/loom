import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { chromium } from '@playwright/test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { matchesPlainPatientObservationAssessment } from '../../workflows/verify-cda-plain-patient-root-selector.mjs';
import { relatedOneAllResponsePaths } from '../../workflows/verify-cda-related-one-all-browser.mjs';

const origin = 'http://related-one-all-observer.test';
const ownerPath = '/api/v1/projects/loom_dev_cda_fhir/explorers/owned-explorer/authoring/v2';
const rowChangePath = `${ownerPath}/row-change`;
const requestBody = {
  snapshotToken: `sha256:${'a'.repeat(64)}`,
  draftVersion: 3,
  draftDigest: 'digest-1',
  outputId: 'output-1',
  rootNodeId: 'observation-node-1',
};
const responseBody = {
  snapshotToken: requestBody.snapshotToken,
  draftVersion: 3,
  draftDigest: 'digest-1',
  currentRootResourceType: 'Patient',
  candidateRootResourceType: 'Observation',
  status: 'READY',
  proposal: { outputId: 'output-1', rootNodeId: 'observation-node-1' },
};

let browser;

before(async () => {
  browser = await chromium.launch({ channel: 'chrome', headless: true });
});

after(async () => {
  await browser?.close();
});

test('captures an owned row-change response body for the Patient-to-Observation assessment', async () => {
  for (const path of ['/builder', '/population-routes', '/construction-capabilities', '/semantic-inventory',
    '/construction-choices', '/related-expand-choices', '/construction-proposals', '/commands',
    '/reconcile', '/preview', '/row-change']) {
    assert.equal(relatedOneAllResponsePaths.test(path), true, `Response capture must retain ${path}`);
  }

  const context = await browser.newContext();
  const page = await context.newPage();
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) {
      await route.abort();
      return;
    }
    if (url.pathname === '/') {
      await route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><html><body>ready</body></html>' });
      return;
    }
    if (url.pathname.endsWith('/authoring/v2/row-change')) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(responseBody) });
      return;
    }
    await route.fulfill({ status: 404, contentType: 'application/json', body: '{"error":"not found"}' });
  });
  await page.goto(`${origin}/`);

  const report = { nativeRequests: [], network: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: origin,
    browserRequestOrigin: origin,
    appOrigins: [origin],
    ownedPathPrefix: ownerPath,
    report,
    responsePaths: relatedOneAllResponsePaths,
  });
  try {
    const expected = {
      outputId: requestBody.outputId,
      snapshotToken: requestBody.snapshotToken,
      draftVersion: requestBody.draftVersion,
      draftDigest: requestBody.draftDigest,
      rootNodeId: requestBody.rootNodeId,
    };
    const responseWait = capture.waitForResponse(
      entry => matchesPlainPatientObservationAssessment(entry, expected),
      { fromIndex: 0, timeoutMs: 2_000 },
    );
    const ownedResult = await page.evaluate(async ({ path, body }) => {
      const response = await fetch(path, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      });
      return { status: response.status, body: await response.json() };
    }, { path: rowChangePath, body: requestBody });
    assert.equal(ownedResult.status, 200);
    assert.deepEqual(ownedResult.body, responseBody);

    const entry = await responseWait;
    assert.equal(entry.responseCapture, 'captured');
    assert.equal(entry.responseCaptureComplete, true);
    assert.deepEqual(capture.responseBody(entry), responseBody);
    assert.equal(matchesPlainPatientObservationAssessment(entry, expected), true);

    await page.evaluate(async ({ path, body }) => fetch(path, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    }).then(response => response.text()), {
      path: rowChangePath.replace('/owned-explorer/', '/other-explorer/'),
      body: requestBody,
    });
    assert.equal(capture.requests.length, 1, 'A row-change response from another Explorer must remain outside the owned request scope');
  } finally {
    capture.stop();
    await context.close();
  }
});
