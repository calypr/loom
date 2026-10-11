import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright';
import { chooseCdaGroupPivotJoinWitness } from '../cda-group-pivot-join-oracle.mjs';
import { prepareCdaGroupPivotPublishedViewerOracle, verifyCdaGroupPivotPublicationReread } from '../cda-group-pivot-publication-oracle.mjs';
import { createCDARequestView } from '../cda-playwright-requests.mjs';
import {
  cdaPublishedFirstTableReady,
  readCdaHeaderLabels,
  readCdaSelectOptions,
  resolveCdaBootstrapExplorer,
} from '../../workflows/cda-group-pivot-published-workflow.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const sharedSubject = 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2';
const raw = [
  { _id: 'Observation/7d2fe423-f934-56ff-bd96-426fb1fdf94e', id: '7d2fe423-f934-56ff-bd96-426fb1fdf94e', project, generation, resourceType: 'Observation', subjectReference: sharedSubject, status: 'final', codeCodingCodes: ['81247-9'] },
  { _id: 'Observation/26c7500c-f54f-5229-b5a7-a6757614bc21', id: '26c7500c-f54f-5229-b5a7-a6757614bc21', project, generation, resourceType: 'Observation', subjectReference: sharedSubject, status: 'final', codeCodingCodes: ['1222593009', 'not-the-category'] },
  { _id: 'Observation/64704041-c4da-5375-aac0-88f951f78344', id: '64704041-c4da-5375-aac0-88f951f78344', project, generation, resourceType: 'Observation', subjectReference: sharedSubject, status: 'final', codeCodingCodes: ['1222593009'] },
  { _id: 'Observation/9bd424bb-9617-561e-a787-c9506066eb59', id: '9bd424bb-9617-561e-a787-c9506066eb59', project, generation, resourceType: 'Observation', subjectReference: 'Patient/033718b9-adff-54b7-81a3-b1c390990d28', status: 'final', codeCodingCodes: ['81247-9'] },
];

test('CDA publication uses the actual scoped request-view response and body API', async () => {
  const origin = 'http://owned.test';
  const ownedPrefix = '/api/explorers/one/authoring/v2';
  const priorRequests = Array.from({ length: 260 }, (_, index) => ({
    index,
    origin: index < 29 ? origin : 'http://other.test',
    path: index < 29 ? `${ownedPrefix}/builder` : `${ownedPrefix}/unrelated`,
    method: 'GET',
    status: 200,
    completedAt: index + 1,
    networkComplete: true,
    responseCaptureComplete: true,
  }));
  const outputId = 'output-exact';
  const publication = {
    index: 260,
    origin,
    path: `${ownedPrefix}/construction-proposals`,
    method: 'POST',
    status: 200,
    completedAt: 42,
    networkComplete: true,
    responseCaptureComplete: true,
    request: {
      outputId,
      candidateConstruction: { version: 1, steps: [{ operation: { kind: 'GROUP', group: {
        keys: [{ inputColumnId: 'source-subject' }, { inputColumnId: 'source-code' }],
        aggregates: [{ operation: 'COUNT_ROWS' }],
      } } }] },
      receiptId: 'receipt-exact',
    },
    response: { outputId, receiptId: 'receipt-exact', proposalId: 'receipt-exact', previewStatus: 'READY' },
  };
  let delegatedPredicate;
  let delegatedOptions;
  const observer = {
    requests: priorRequests,
    byRequest: new WeakMap(),
    waitForResponse(predicate, options) {
      delegatedPredicate = predicate;
      delegatedOptions = options;
      return Promise.resolve(this.requests.slice(options.fromIndex).find(entry => predicate(entry) &&
        entry.networkComplete && entry.responseCaptureComplete));
    },
    requestBody: entry => entry.request,
    responseBody: entry => entry.response,
  };
  const capture = createCDARequestView(observer, {
    browserRequestOrigin: origin,
    ownedPathPrefix: ownedPrefix,
  });

  const fromIndex = capture.requests.length;
  observer.requests.push(publication);
  const matchesGroupProposal = entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST' &&
    entry.status === 200 && entry.completedAt && entry.request?.outputId === outputId &&
    entry.request?.candidateConstruction?.steps?.at(-1)?.operation?.kind === 'GROUP' &&
    entry.request?.candidateConstruction?.steps?.at(-1)?.operation?.group?.aggregates?.[0]?.operation === 'COUNT_ROWS';
  const event = await capture.waitForResponse(matchesGroupProposal,
    { fromIndex, timeoutMs: 5_000 });
  assert.equal(fromIndex, 29, 'the request checkpoint counts only earlier requests in this CDA view');
  assert.equal(delegatedOptions.fromIndex, 260,
    'the scoped view translates its view-local checkpoint after unrelated traffic to the completed proposal event');
  assert.equal(delegatedOptions.timeoutMs, 5_000);
  assert.equal(Boolean(delegatedPredicate(publication)), true);
  assert.equal(delegatedPredicate({ ...publication, index: 29, origin: 'http://other.test' }), false,
    'the scoped view must exclude an otherwise matching proposal from another origin');
  assert.equal(event, publication,
    'a completed scoped proposal after the checkpoint remains discoverable when the workflow waits after preview render');
  assert.deepEqual(capture.requestBody(event), publication.request);
  assert.deepEqual(capture.responseBody(event), publication.response);
  const staleGlobalCursor = await capture.waitForResponse(matchesGroupProposal,
    { fromIndex: priorRequests.length, timeoutMs: 5_000 });
  assert.equal(staleGlobalCursor, undefined,
    'the report-wide request count is not a valid cursor for the CDA-scoped view');
  assert.equal(delegatedOptions.fromIndex, 261,
    'an out-of-range scoped cursor falls past the already-completed event');
});

test('CDA published Group→Pivot oracle pins exact raw IDs, category order, and Viewer values', () => {
  const witness = chooseCdaGroupPivotJoinWitness(raw, { project, generation });
  const expected = prepareCdaGroupPivotPublishedViewerOracle(witness, { project, generation });
  assert.deepEqual(expected.sourceObservationIDs, [
    '26c7500c-f54f-5229-b5a7-a6757614bc21',
    '64704041-c4da-5375-aac0-88f951f78344',
    '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
  ]);
  assert.deepEqual(expected.columns, ['Subject Reference', '1222593009', '81247-9']);
  assert.deepEqual(expected.rows, [{
    'Subject Reference': sharedSubject,
    '1222593009': 2,
    '81247-9': 1,
  }]);
  assert.deepEqual(expected.groupPivotRows, [[sharedSubject, 2, 1]]);
});

test('CDA published Group→Pivot reread contains exactly the selected right raw Observations', () => {
  const witness = chooseCdaGroupPivotJoinWitness(raw, { project, generation });
  assert.deepEqual(verifyCdaGroupPivotPublicationReread(witness, [...witness.right].reverse(), { project, generation }), [
    'Observation/26c7500c-f54f-5229-b5a7-a6757614bc21',
    'Observation/64704041-c4da-5375-aac0-88f951f78344',
    'Observation/7d2fe423-f934-56ff-bd96-426fb1fdf94e',
  ]);
  assert.throws(() => verifyCdaGroupPivotPublicationReread(witness, witness.right.slice(1), { project, generation }), /all and only three/);
  assert.throws(() => verifyCdaGroupPivotPublicationReread(witness, [...witness.right, witness.right[0]], { project, generation }), /all and only three/);
  assert.throws(() => verifyCdaGroupPivotPublicationReread(witness, witness.right.map(row =>
    row.id === '26c7500c-f54f-5229-b5a7-a6757614bc21' ? { ...row, codeCodingCodes: ['81247-9'] } : row), { project, generation }), /changed raw Observation/);
});

test('CDA published Group→Pivot oracle rejects changed selected identity or category semantics', () => {
  const witness = chooseCdaGroupPivotJoinWitness(raw, { project, generation });
  assert.throws(() => prepareCdaGroupPivotPublishedViewerOracle({ ...witness, project: 'another' }, { project, generation }), /exact CDA project/);
  assert.throws(() => prepareCdaGroupPivotPublishedViewerOracle({ ...witness, right: witness.right.slice(1) }, { project, generation }), /exact three selected/);
  assert.throws(() => prepareCdaGroupPivotPublishedViewerOracle({ ...witness, categories: ['81247-9', '1222593009'] }, { project, generation }), /raw-code order/);
});

test('CDA native bootstrap selection uses only the exact owned project Explorer list', () => {
  const summaries = [
    { project, explorerId: 'owned-z', title: 'Later owned Explorer', management: 'INTERACTIVE' },
    { project, explorerId: 'owned-a', title: 'Earlier owned Explorer', management: 'INTERACTIVE' },
  ];
  assert.equal(resolveCdaBootstrapExplorer(summaries, { project }), 'owned-a');
  assert.equal(resolveCdaBootstrapExplorer(summaries, { project, preferredExplorer: 'owned-z' }), 'owned-z');
  assert.throws(() => resolveCdaBootstrapExplorer([], { project }), /must have an existing Explorer/);
  assert.throws(() => resolveCdaBootstrapExplorer([{ project: 'another-project', explorerId: 'foreign' }], { project }), /exact owned CDA project/);
  assert.throws(() => resolveCdaBootstrapExplorer(summaries, { project, preferredExplorer: 'foreign' }), /must be present/);
});

test('CDA native first-table readiness matches the empty Builder root picker', async (t) => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html><html><body>
    <section>
      <h2>Build your first table</h2>
      <p>Choose a populated record type. Loom will add its direct ID column and load a preview. The table name is optional.</p>
      <label for="first-table-name">Table name (optional)</label>
      <input id="first-table-name" placeholder="Defaults to the record type">
      <select id="output-option-reader">
        <option value="subject">  Subject   Reference </option>
        <option value="empty" disabled> Empty output </option>
      </select>
      <section aria-label="Choose row type">
        <h2>What should one row represent?</h2>
        <label>Search row types<input aria-label="Search row types"></label>
        <button type="button" aria-label="Choose Observation rows">
          <span>Observation</span><span>3 authorized records</span>
        </button>
      </section>
    </section>
  </body></html>`);

  assert.equal(await page.evaluate(cdaPublishedFirstTableReady), true,
    'the serialized readiness predicate accepts the visible first-table heading, optional title, and enabled Observation choice');
  assert.equal(await page.getByRole('heading', { name: 'Build your first table', exact: true }).count(), 1);
  assert.equal(await page.locator('#first-table-name').count(), 1);
  assert.deepEqual(await page.locator('#output-option-reader').evaluate(readCdaSelectOptions), [
    { value: 'subject', text: 'Subject Reference', disabled: false },
    { value: 'empty', text: 'Empty output', disabled: true },
  ], 'the serialized option reader normalizes visible labels and preserves disabled state without outer-scope helpers');
  assert.equal(await page.getByRole('button', { name: 'Choose Observation rows', exact: true }).count(), 1);
  assert.equal(await page.getByRole('button', { name: 'New table', exact: true }).count(), 0,
    'the empty Builder root picker does not expose the post-creation New table control');

  await page.locator('button[aria-label="Choose Observation rows"]').evaluate(button => { button.disabled = true; });
  assert.equal(await page.evaluate(cdaPublishedFirstTableReady), false,
    'a disabled root choice is not actionable even while the empty-table heading is present');
  await page.locator('button[aria-label="Choose Observation rows"]').evaluate(button => { button.disabled = false; });
  await page.locator('h2').first().evaluate(heading => { heading.textContent = 'Build another table'; });
  assert.equal(await page.evaluate(cdaPublishedFirstTableReady), false,
    'the first-table predicate rejects a later table-creation state');
});

test('CDA Viewer header oracle reads authored labels under uppercase presentation', async (t) => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html><html><head><style>
    .viewer-header-label { text-transform: uppercase; }
  </style></head><body>
    <table aria-label="published results"><thead><tr>
      <th><button class="viewer-header-label">Subject Reference</button></th>
      <th><button class="viewer-header-label">1222593009</button></th>
      <th><button class="viewer-header-label">81247-9</button></th>
    </tr></thead></table>
  </body></html>`);

  assert.deepEqual(await page.locator('thead th').allInnerTexts(), [
    'SUBJECT REFERENCE', '1222593009', '81247-9',
  ], 'the Viewer deliberately applies uppercase presentation to its header controls');
  assert.deepEqual(await page.locator('thead th').evaluateAll(readCdaHeaderLabels), [
    'Subject Reference', '1222593009', '81247-9',
  ], 'the serialized header reader returns the source label text without CSS text transformation');
});
