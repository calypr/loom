import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { chromium } from '@playwright/test';
import { generatedJ01ConceptNDJSON } from '../../../loom-dev.mjs';
import { readNDJSONResourceIdentityOracle } from '../ndjson-resource-oracle.mjs';
import { assertCodedGroupRows, assertRootRows } from '../../workflows/basic-coded-group-recorded-code.mjs';
import {
  BASIC_RECORDED_CODE_GROUPS,
  groupRecordedCodesBySource,
  readBasicCodedGroupRecordedCodeOracle,
  visibleRecordedCodeGroupRows,
} from '../basic-coded-group-recorded-code-oracle.mjs';

const fixtureDirectory = fileURLToPath(new URL('../../../../testdata/basic-coded-group-recorded-code/', import.meta.url));
const fixturePath = new URL('../../../../testdata/basic-coded-group-recorded-code/Observation.ndjson', import.meta.url);

test('native coded-group fixture selects only the literal Basic files without J01 population', async () => {
  assert.deepEqual(readdirSync(fixtureDirectory).sort(), ['Observation.ndjson', 'Patient.ndjson'],
    'The selected fixture must not include generated concept or other source files');
  assert.equal(generatedJ01ConceptNDJSON(fixtureDirectory), undefined,
    'The fixture seeder must not append generated J01 concepts to this case');
  const spec = await readFile(new URL('../../specs/basic-coded-group-recorded-code.spec.mjs', import.meta.url), 'utf8');
  assert.match(spec, /fixtureDir: 'testdata\/basic-coded-group-recorded-code'/,
    'The native case must route fixture creation to this isolated two-file fixture');
});

test('native proposal driver follows the semantic coded-group preview table contract', async () => {
  const component = await readFile(new URL('../../../../ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionProposalPreview.tsx', import.meta.url), 'utf8');
  const builderWorkspace = await readFile(new URL('../../../../ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx', import.meta.url), 'utf8');
  const previewTable = await readFile(new URL('../../../../ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx', import.meta.url), 'utf8');
  const workflow = await readFile(new URL('../../workflows/basic-coded-group-recorded-code.mjs', import.meta.url), 'utf8');
  assert.match(component, /data-testid="construction-proposal-preview"/);
  assert.match(component, /data-preview-status="ready"/);
  assert.match(component, /data-preview-output-id=\{preview\.outputId\}/);
  assert.match(component, /<table[\s\S]*?<tbody>[\s\S]*?data-testid="construction-proposal-preview-row"/);
  assert.match(workflow, /querySelector\('\[data-testid="construction-proposal-preview"\]'\)/);
  assert.match(workflow, /tbody tr\[data-testid="construction-proposal-preview-row"\]/);
  assert.match(workflow, /data-preview-output-id|dataset\.previewOutputId/);
  assert.match(builderWorkspace, /data-testid="construction-operation-editor"[\s\S]*?data-output-id=\{table\.outputId\}/,
    'The visible reshape editor must expose the exact table identity while table navigation is hidden');
  assert.match(previewTable, /role="table"[\s\S]*?aria-colcount=\{columns\.length\}/,
    'The normal Preview grid must expose the accessible table and exact column count');
  assert.match(previewTable, /role="row"[\s\S]*?uppercase tracking-wide/);
  assert.match(previewTable, /role="columnheader"/);
});

test('proposal driver settles and reads exact literal groups from the native proposal preview DOM', async () => {
  const identityOracle = await readNDJSONResourceIdentityOracle({
    path: fixturePath.pathname,
    project: 'loom_dev_verify_oracle',
    generation: 'fixture-generation-literal-test',
    resourceType: 'Observation',
  });
  const launchOptions = { headless: true, channel: 'chrome' };
  if (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE) {
    delete launchOptions.channel;
    launchOptions.executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
  }
  const browser = await chromium.launch(launchOptions);
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html><html><body>
      <section data-testid="construction-operation-editor" data-operation-family="RESHAPE" data-output-id="out-observation">
        <header><div><h2>Group by coded value</h2></div></header>
        <div>Coding field</div>
      </section>
      <section aria-label="Proposed change" data-testid="construction-proposal-panel"
        data-proposal-status="ready" data-proposal-id="proposal-basic-coded-group">
        <div data-testid="construction-proposal-ready">
          <div data-testid="construction-proposal-preview" data-preview-status="ready"
          data-preview-receipt-id="receipt-basic-coded-group" data-preview-output-id="out-observation">
          <table class="min-w-full border-collapse text-sm"><thead class="sticky top-0 bg-slate-50 text-left text-xs text-slate-600"><tr>
            <th scope="col" class="border-b border-slate-200 px-3 py-2 font-semibold"><span class="block">Code system</span><span class="mt-0.5 block font-normal text-slate-400">string</span></th>
            <th scope="col" class="border-b border-slate-200 px-3 py-2 font-semibold"><span class="block">Code version</span><span class="mt-0.5 block font-normal text-slate-400">string</span></th>
            <th scope="col" class="border-b border-slate-200 px-3 py-2 font-semibold"><span class="block">Code</span><span class="mt-0.5 block font-normal text-slate-400">string</span></th>
            <th scope="col" class="border-b border-slate-200 px-3 py-2 font-semibold"><span class="block">Source records</span><span class="mt-0.5 block font-normal text-slate-400">integer</span></th>
          </tr></thead>
            <tbody>
              <tr data-testid="construction-proposal-preview-row" class="border-b border-slate-100 even:bg-slate-50/60"><td title="https://example.test/codes" class="max-w-72 px-3 py-2 align-top text-slate-800">https://example.test/codes</td><td title="—" class="max-w-72 px-3 py-2 align-top text-slate-800">—</td><td title="height" class="max-w-72 px-3 py-2 align-top text-slate-800">height</td><td title="2" class="max-w-72 px-3 py-2 align-top text-slate-800">2</td></tr>
              <tr data-testid="construction-proposal-preview-row" class="border-b border-slate-100 even:bg-slate-50/60"><td title="https://example.test/codes" class="max-w-72 px-3 py-2 align-top text-slate-800">https://example.test/codes</td><td title="—" class="max-w-72 px-3 py-2 align-top text-slate-800">—</td><td title="weight" class="max-w-72 px-3 py-2 align-top text-slate-800">weight</td><td title="1" class="max-w-72 px-3 py-2 align-top text-slate-800">1</td></tr>
              <tr data-testid="construction-proposal-preview-row" class="border-b border-slate-100 even:bg-slate-50/60"><td title="urn:loom:fixture:panel" class="max-w-72 px-3 py-2 align-top text-slate-800">urn:loom:fixture:panel</td><td title="—" class="max-w-72 px-3 py-2 align-top text-slate-800">—</td><td title="paired" class="max-w-72 px-3 py-2 align-top text-slate-800">paired</td><td title="3" class="max-w-72 px-3 py-2 align-top text-slate-800">3</td></tr>
            </tbody>
          </table>
          <p class="border-t border-slate-200 px-3 py-2 text-xs text-slate-500">Showing all 3 rows in this proposal.</p>
          </div>
        </div>
      </section>
    </body></html>`);

    await assertCodedGroupRows(page, 'out-observation', { groups: BASIC_RECORDED_CODE_GROUPS },
      'Standalone CODED_GROUP proposal', 5_000, true);
    await page.getByTestId('construction-operation-editor').evaluate(editor => {
      editor.dataset.outputId = 'out-another-table';
    });
    await assert.rejects(
      () => assertCodedGroupRows(page, 'out-observation', { groups: BASIC_RECORDED_CODE_GROUPS },
        'Mismatched CODED_GROUP proposal', 5_000, true),
      /native reshape editor must belong to the exact Observation output/,
    );

    const sourceRows = identityOracle.ids.map(id => `<tr data-testid="construction-proposal-preview-row"><td>${id}</td></tr>`).join('');
    await page.setContent(`<!doctype html><html><body>
      <section data-testid="construction-workspace">
        <h1>Editing Basic Observation recorded codes</h1>
        <section aria-label="Proposed change" data-testid="construction-proposal-panel" data-proposal-status="ready">
          <div data-testid="construction-proposal-ready"></div>
        </section>
        <div data-testid="construction-proposal-preview" data-preview-status="ready"
          data-preview-receipt-id="receipt-basic-coded-group-removal" data-preview-output-id="out-observation">
          <table><thead><tr><th scope="col"><span>Observation ID</span></th></tr></thead>
            <tbody>${sourceRows}</tbody>
          </table>
          <p>Showing all 6 rows in this proposal.</p>
        </div>
      </section>
    </body></html>`);
    const restored = await assertRootRows(page, 'out-observation', identityOracle,
      'CODED_GROUP removal proposal', 5_000, true);
    assert.deepEqual(restored.rows.map(row => row.cells), identityOracle.ids.map(id => [id]));

    const groupRows = visibleRecordedCodeGroupRows(BASIC_RECORDED_CODE_GROUPS)
      .map((cells, index) => `<div role="row"><span>${index + 1}</span>${cells.map(cell => `<div role="cell">${cell}</div>`).join('')}</div>`)
      .join('');
    const gridPreview = `<section aria-label="Table result" data-testid="construction-preview" data-preview-status="ready"
      data-preview-receipt-id="receipt-basic-coded-group-grid" data-preview-output-id="out-observation">
      <div data-testid="preview-table-scroll">
        <div role="table" aria-rowcount="4" aria-colcount="4">
          <div role="row" class="sticky top-0 z-10 bg-slate-100 text-[11px] uppercase tracking-wide text-slate-600">
            <div>Row</div><div role="columnheader">Code system</div><div role="columnheader">Code version</div>
            <div role="columnheader">Code</div><div role="columnheader">Source records</div>
          </div>
          ${groupRows}
        </div>
      </div>
    </section>`;
    const pageWithPreview = context => `<!doctype html><html><head><style>.uppercase { text-transform: uppercase; }</style></head><body>
      ${context}
      ${gridPreview}
    </body></html>`;

    await page.setContent(pageWithPreview(`<nav aria-label="Tables">
      <button type="button" data-testid="construction-table-out-observation" aria-current="page">Basic Observation recorded codes</button>
    </nav>`));
    const grid = await assertCodedGroupRows(page, 'out-observation', { groups: BASIC_RECORDED_CODE_GROUPS },
      'Standalone CODED_GROUP applied grid', 5_000);
    assert.deepEqual(grid.headers, ['CODE SYSTEM', 'CODE VERSION', 'CODE', 'SOURCE RECORDS'],
      'PreviewTable uppercase styling must not change semantic output header matching');

    await page.setContent(pageWithPreview(`<section data-testid="construction-operation-editor"
      data-operation-family="RESHAPE" data-output-id="out-observation">
      <header><h2>Group by coded value</h2></header><div>Coding field</div>
    </section>`));
    const savedStep = await assertCodedGroupRows(page, 'out-observation', { groups: BASIC_RECORDED_CODE_GROUPS },
      'Unchanged saved CODED_GROUP edit grid', 5_000, false, true);
    assert.deepEqual(savedStep.headers, ['CODE SYSTEM', 'CODE VERSION', 'CODE', 'SOURCE RECORDS']);
  } finally {
    await browser.close();
  }
});

test('Basic Observation direct code.coding[] has exact literal groups and source counts', async () => {
  const oracle = await readBasicCodedGroupRecordedCodeOracle({
    path: fixturePath.pathname,
    project: 'loom_dev_verify_oracle',
    generation: 'fixture-generation-literal-test',
  });
  assert.equal(oracle.sourceCount, 6);
  assert.deepEqual(oracle.groups, BASIC_RECORDED_CODE_GROUPS);
  assert.deepEqual(visibleRecordedCodeGroupRows(oracle.groups), [
    ['https://example.test/codes', '—', 'height', '2'],
    ['https://example.test/codes', '—', 'weight', '1'],
    ['urn:loom:fixture:panel', '—', 'paired', '3'],
  ]);
});

test('each source contributes at most once to one exact recorded-code tuple', () => {
  const duplicateCodeInOneSource = [
    { resourceType: 'Observation', id: 'one', code: { coding: [
      { system: 'urn:test', code: 'same' },
      { system: 'urn:test', code: 'same' },
      { system: 'urn:test', code: 'other', version: ' v1 ' },
    ] } },
    { resourceType: 'Observation', id: 'two', code: { coding: [
      { system: 'urn:test', code: 'same' },
    ] } },
  ];
  assert.deepEqual(groupRecordedCodesBySource(duplicateCodeInOneSource), [
    { system: 'urn:test', version: null, code: 'same', count: 2, sourceIDs: ['one', 'two'] },
    { system: 'urn:test', version: ' v1 ', code: 'other', count: 1, sourceIDs: ['one'] },
  ]);
});

test('oracle reads only the fixture’s direct populated Observation code path', async () => {
  const text = await readFile(fixturePath, 'utf8');
  const records = text.trim().split(/\r?\n/).map(line => JSON.parse(line));
  assert(records.every(record => Array.isArray(record.code?.coding) && record.code.coding.length > 0));
  assert(records.some(record => record.component?.some(component => component.code?.coding?.length)),
    'fixture keeps nested component Coding decoys distinct from direct code.coding[]');
});
