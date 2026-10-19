import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from '@playwright/test';
import { canonicalProjectID } from '../../../loom-dev.mjs';
import { BASIC_VIEWER_CHARTS_FIXTURE_DIR, readBasicViewerChartsOracle } from '../basic-viewer-charts-oracle.mjs';
import {
  assertChartFacet,
  chartSpecFor,
  graphInputUsesFixtureProject,
  requestBodyFromResponse,
  sortViewerPreviewRowsByPatientID,
  visibleGridSnapshotFromElement,
} from '../../workflows/viewer-charts.mjs';

const cell = (tagName, text) => ({ tagName, textContent: text, innerText: text });
const rowWithCells = (selector, cells) => ({
  querySelectorAll: actualSelector => actualSelector === selector ? cells : [],
});

test('Patient chart oracle retains female as a term and omitted gender as missing', () => {
  const oracle = readBasicViewerChartsOracle();
  assert.equal(oracle.path, `${BASIC_VIEWER_CHARTS_FIXTURE_DIR}Patient.ndjson`);
  assert.equal(oracle.resourceType, 'Patient');
  assert.equal(oracle.displayedCount, 2);
  assert.equal(oracle.populatedCount, 1);
  assert.equal(oracle.missingCount, 1);
  assert.deepEqual(oracle.expectedTerms, [{ key: 'female', doc_count: 1 }]);
  assert.deepEqual(oracle.rows, [
    { id: 'dev-patient-001', state: 'present', gender: 'female' },
    { id: 'dev-patient-002', state: 'missing' },
  ]);
  assert.equal(Object.hasOwn(oracle.rows[1], 'gender'), false);
});

test('Viewer facet oracle preserves ClickHouse count strings and numeric missing count', () => {
  const payload = {
    errors: [],
    data: {
      dataframeAggregations: {
        aggregations: [{
          name: 'loom:patients:chart:gender',
          kind: 'TERMS',
          columns: ['key', 'doc_count'],
          rows: [{ key: 'female', doc_count: '1' }],
          missingCount: 1,
          truncated: false,
        }],
      },
    },
  };
  const facet = assertChartFacet(payload);
  assert.deepEqual(facet, payload.data.dataframeAggregations.aggregations[0]);
  assert.deepEqual(facet.rows, [{ key: 'female', doc_count: '1' }]);
  assert.equal(typeof facet.rows[0].doc_count, 'string');
  assert.equal(facet.missingCount, 1);
  assert.equal(typeof facet.missingCount, 'number');

  const numericDocCountPayload = structuredClone(payload);
  numericDocCountPayload.data.dataframeAggregations.aggregations[0].rows[0].doc_count = 1;
  assert.throws(() => assertChartFacet(numericDocCountPayload),
    /TERMS rows must contain the one populated value and omit the missing-value bin/);

  const stringMissingCountPayload = structuredClone(payload);
  stringMissingCountPayload.data.dataframeAggregations.aggregations[0].missingCount = '1';
  assert.throws(() => assertChartFacet(stringMissingCountPayload),
    /the raw TERMS facet must separately report the Patient with missing Gender/);
});

test('Viewer chart snapshots support native HTML tables and the Builder ARIA grid', () => {
  const nativeHeaders = [cell('TH', 'Patient ID'), cell('TH', 'Gender')];
  const nativeRows = [
    rowWithCells('td', [cell('TD', 'dev-patient-001'), cell('TD', 'female')]),
    rowWithCells('td', [cell('TD', 'dev-patient-002'), cell('TD', '—')]),
  ];
  const nativeTable = {
    tagName: 'TABLE',
    querySelectorAll: selector => selector === 'thead th' ? nativeHeaders : selector === 'tbody tr' ? nativeRows : [],
  };
  const expected = {
    headers: ['Patient ID', 'Gender'],
    rows: [
      { 'Patient ID': 'dev-patient-001', Gender: 'female' },
      { 'Patient ID': 'dev-patient-002', Gender: '—' },
    ],
  };
  assert.deepEqual(visibleGridSnapshotFromElement(nativeTable), expected);

  const gridHeader = rowWithCells('[role="columnheader"]', [
    cell('DIV', 'Patient ID'), cell('DIV', 'Gender'),
  ]);
  const gridRows = [
    rowWithCells('[role="cell"]', [cell('DIV', 'dev-patient-001'), cell('DIV', 'female')]),
    rowWithCells('[role="cell"]', [cell('DIV', 'dev-patient-002'), cell('DIV', '—')]),
  ];
  const roleGrid = {
    tagName: 'DIV',
    querySelectorAll: selector => selector === '[role="row"]' ? [gridHeader, ...gridRows] : [],
  };
  assert.deepEqual(visibleGridSnapshotFromElement(roleGrid), expected);
});

test('Viewer chart GraphQL binding matches the raw hyphenated run project', () => {
  const project = 'loom_dev_verify_mjrgk-abc123';
  const canonicalProject = canonicalProjectID(project);
  assert.equal(canonicalProject, 'loom_dev_verify_mjrgk/abc123');
  assert.notEqual(canonicalProject, project);

  const selector = { recipe: 'recipe-1', translationVersion: 'v1', output: 'patients' };
  const spec = { name: 'loom:patients:chart:gender', kind: 'TERMS', column: 'gender', size: 12 };
  const makeRequest = projectId => ({
    method: () => 'POST',
    url: () => 'http://127.0.0.1:3180/graphql/graph',
    postDataJSON: () => ({ variables: {
      input: { projectId, selector },
      facetInput: { projectId, selector, specs: [spec] },
    } }),
  });

  const rawRequest = makeRequest(project);
  const rawBody = rawRequest.postDataJSON();
  assert.equal(graphInputUsesFixtureProject(rawBody, project), true);
  assert.equal(graphInputUsesFixtureProject(rawBody, canonicalProject), false);
  assert.equal(chartSpecFor({ request: rawRequest, project, output: { outputId: 'patients', selector }, column: 'gender' }), true);
  assert.equal(chartSpecFor({ request: rawRequest, project: canonicalProject, output: { outputId: 'patients', selector }, column: 'gender' }), false);
});

test('exact Patient preview row comparison ignores order and preserves duplicate identities', () => {
  const expected = [
    { 'Patient ID': 'dev-patient-001', Gender: 'female' },
    { 'Patient ID': 'dev-patient-002', Gender: '—' },
  ];
  const reversed = [expected[1], expected[0]];
  assert.deepEqual(sortViewerPreviewRowsByPatientID(reversed), expected);

  const duplicateAndMissing = [expected[0], expected[0]];
  assert.deepEqual(sortViewerPreviewRowsByPatientID(duplicateAndMissing), duplicateAndMissing);
  assert.notDeepEqual(sortViewerPreviewRowsByPatientID(duplicateAndMissing), expected);
});


test('Viewer chart selector reads the request body from a real Playwright response', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const graphURL = 'http://viewer-charts.test/graphql/graph';
    const expectedBody = { variables: { input: { selector: {
      recipe: 'recipe-exact', translationVersion: 'authoring-v2-native-10', output: 'out-exact',
    } } } };
    await page.route('http://viewer-charts.test/**', async route => {
      if (route.request().url() === graphURL) {
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: { ok: true } }) });
        return;
      }
      await route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><title>Viewer request fixture</title>' });
    });
    await page.goto('http://viewer-charts.test/');
    const responsePromise = page.waitForResponse(response =>
      response.url() === graphURL && response.request().method() === 'POST');
    await page.evaluate(body => fetch('/graphql/graph', {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
    }), expectedBody);
    const response = await responsePromise;
    assert.equal(response.status(), 200);
    assert.deepEqual(requestBodyFromResponse(response), expectedBody);
  } finally {
    await browser.close();
  }
});


test('Show charts transition is asserted through its new accessible name', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent('<button aria-pressed="false">Show charts</button>');
    const showCharts = page.getByRole('button', { name: 'Show charts', exact: true });
    await showCharts.evaluate(button => button.addEventListener('click', () => {
      button.textContent = 'Hide charts';
      button.setAttribute('aria-pressed', 'true');
    }));
    await showCharts.click();
    const hideCharts = page.getByRole('button', { name: 'Hide charts', exact: true });
    assert.equal(await hideCharts.getAttribute('aria-pressed'), 'true');
    assert.equal(await hideCharts.isVisible(), true);
  } finally {
    await browser.close();
  }
});
