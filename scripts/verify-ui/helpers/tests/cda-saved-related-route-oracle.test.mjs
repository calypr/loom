import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { chromium, request as playwrightRequest } from 'playwright';
import {
  assertCdaSavedRelatedRouteRows,
  buildCdaSavedRelatedRouteQuery,
  projectVisibleRoutePairs,
  readCdaSavedRelatedRouteOracle,
} from '../cda-saved-related-route-oracle.mjs';
import {
  assertSavedRouteSignature,
  builderJSON,
  ensureExactRelatedRouteChoices,
  projectSavedRouteSignature,
  resolveSavedRouteBootstrapExplorer,
  resolveSavedRouteSourceIDColumn,
} from '../../workflows/cda-saved-related-route-edit-epoch126.mjs';
import { collectPreviewRows } from '../playwright-preview-rows.mjs';

const oracle = readCdaSavedRelatedRouteOracle();

const rawRowsFromOracle = () => [{
  specimen: oracle.scope.specimen,
  patient: oracle.scope.patient,
  directObservations: oracle.observations
    .filter(row => oracle.routes.direct.observationIds.includes(row.id))
    .map(({ id, _id }) => ({ id, _id })),
  patientObservations: oracle.observations.map((row, index) => ({
    id: row.id,
    _id: row._id,
    specimenReference: row.specimenReference,
    specimenReferenceState: row.specimenReference === null ? (index % 2 ? 'missing' : 'null') : 'present',
  })),
}];

const retainedSpecimenBuilderDocument = {
  kind: 'ExplorerBuilderDocument',
  output: { id: 'out_2bee220ff3d8abd656ea6870', title: 'Specimen route witness' },
  rootResourceType: 'Specimen',
  route: { occurrenceId: 'base', resourceType: 'Specimen' },
  rows: { kind: 'RECORDS', records: {} },
  columns: [{
    columnId: 'source_42a14cadde54956b093d80e8',
    column: 'col_508383e21aa9e8d193733aca',
    label: 'Specimen ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
    table: { visible: true, order: 0 },
  }],
};

test('epoch126 raw route oracle pins direct and Patient routes with exact CDA row identities', () => {
  assert.equal(oracle.scope.project, 'loom_dev_cda_fhir');
  assert.equal(oracle.scope.generation, 'cda-fhir-v1');
  assert.equal(oracle.scope.specimen.id, 'b7cad184-db67-5542-a975-10fffa3e89e7');
  assert.equal(oracle.scope.patient.id, 'afcfb15e-7617-5691-ae2c-ab675322fb33');
  assert.equal(oracle.routeA.rows.length, 1);
  assert.deepEqual(oracle.routeA.rows, [[
    'b7cad184-db67-5542-a975-10fffa3e89e7',
    '35cfec85-56e8-5257-af99-2e9345be2011',
  ]]);
  assert.equal(oracle.routeB.rows.length, 31);
  assert(oracle.routeB.rows.some(([, observationId]) => observationId === '35cfec85-56e8-5257-af99-2e9345be2011'));
  assert.equal(oracle.summary.distinctObservationIdentityCount, 31);
  assert.equal(oracle.summary.distinctNonnullSpecimenReferenceCount, 29);
  assert.equal(oracle.summary.nullOrMissingSpecimenReferenceCount, 2);
});

test('native route setup selects an existing Explorer only from the exact owned CDA project', () => {
  const summaries = [
    { project: 'loom_dev_cda_fhir', explorerId: 'qa-z' },
    { project: 'loom_dev_cda_fhir', explorerId: 'qa-a' },
  ];
  assert.equal(resolveSavedRouteBootstrapExplorer(summaries, { project: 'loom_dev_cda_fhir' }), 'qa-a');
  assert.equal(resolveSavedRouteBootstrapExplorer(summaries, {
    project: 'loom_dev_cda_fhir', preferredExplorer: 'qa-z',
  }), 'qa-z');
  assert.throws(() => resolveSavedRouteBootstrapExplorer([], { project: 'loom_dev_cda_fhir' }), /must have an existing Explorer/);
  assert.throws(() => resolveSavedRouteBootstrapExplorer([
    ...summaries, { project: 'another-project', explorerId: 'foreign' },
  ], { project: 'loom_dev_cda_fhir' }), /exact owned CDA project/);
  assert.throws(() => resolveSavedRouteBootstrapExplorer(summaries, {
    project: 'loom_dev_cda_fhir', preferredExplorer: 'missing',
  }), /must be present/);
});

test('retained Builder source identity uses columnId and maps to the native filter option ID', () => {
  const sourceColumn = resolveSavedRouteSourceIDColumn(retainedSpecimenBuilderDocument, 'Specimen');
  assert.equal(sourceColumn.columnId, 'source_42a14cadde54956b093d80e8');
  assert.equal(sourceColumn.column, 'col_508383e21aa9e8d193733aca');
  assert.equal(sourceColumn.label, 'Specimen ID');
  assert.equal(sourceColumn.id, undefined);

  assert.throws(() => resolveSavedRouteSourceIDColumn({
    ...retainedSpecimenBuilderDocument,
    columns: [{ ...sourceColumn, id: sourceColumn.columnId }],
  }, 'Specimen'), /Persisted Builder source columns use columnId/);
  assert.throws(() => resolveSavedRouteSourceIDColumn({
    ...retainedSpecimenBuilderDocument,
    columns: [sourceColumn, { ...sourceColumn, columnId: 'another_source_column' }],
  }, 'Specimen'), /exactly one base VALUE id source column/);
});

test('saved routes compare four semantic fields while retaining edge and node identity separately', () => {
  const direct = oracle.routeA.signature.map((edge, index) => ({
    ...edge,
    edgeId: 'saved-edge-' + index,
    fromNodeId: 'saved-from-node-' + index,
    toNodeId: 'saved-to-node-' + index,
    matchMode: 'OPTIONAL',
  }));
  const sameRouteWithDifferentMetadata = direct.map((edge, index) => ({
    ...edge,
    edgeId: 'reloaded-edge-' + index,
    fromNodeId: 'reloaded-from-node-' + index,
    toNodeId: 'reloaded-to-node-' + index,
  }));

  assert.deepEqual(assertSavedRouteSignature(direct, oracle.routeA.signature, 'direct route'),
    oracle.routeA.signature);
  assert.deepEqual(projectSavedRouteSignature(sameRouteWithDifferentMetadata), oracle.routeA.signature);
  assert.notDeepEqual(sameRouteWithDifferentMetadata, direct,
    'The full saved route retains per-Explorer edge/node identity for lifecycle comparisons.');

  assert.throws(() => assertSavedRouteSignature([{
    ...sameRouteWithDifferentMetadata[0], relationship: 'subject_Patient',
  }], oracle.routeA.signature, 'wrong relationship'), /semantic signature changed/);
  assert.throws(() => assertSavedRouteSignature([{
    ...sameRouteWithDifferentMetadata[0], storageDirection: 'OUTBOUND',
  }], oracle.routeA.signature, 'wrong direction'), /semantic signature changed/);
});

test('saved route chooser reveals both exact paths when either selected path hides its alternative', async (t) => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  for (const selected of ['direct', 'viaPatient']) {
    const selectedLabel = selected === 'direct' ? oracle.routeA.displayLabel : oracle.routeB.displayLabel;
    const alternativeLabel = selected === 'direct' ? oracle.routeB.displayLabel : oracle.routeA.displayLabel;
    await page.setContent(`<!doctype html><fieldset>
      <label><input type="radio" name="route" aria-label="${selectedLabel}" checked></label>
      <details data-testid="construction-related-expand-other-routes">
        <summary>Other relationship paths (1)</summary>
        <label><input type="radio" name="route" aria-label="${alternativeLabel}"></label>
      </details>
    </fieldset>`);
    let disclosureActions = 0;
    const routes = await ensureExactRelatedRouteChoices(page, {
      directLabel: oracle.routeA.displayLabel,
      viaPatientLabel: oracle.routeB.displayLabel,
      phase: 'selected ' + selected + ' saved-route edit',
      onOpenDisclosure: async ({ summary, perform, after, timeout }) => {
        disclosureActions += 1;
        await summary.click({ timeout });
        await after();
      },
    });
    assert.equal(await routes.direct.isVisible(), true);
    assert.equal(await routes.viaPatient.isVisible(), true);
    assert.notEqual(await page.getByTestId('construction-related-expand-other-routes').getAttribute('open'), null);
    assert.equal(disclosureActions, 1,
      'a collapsed alternate-path disclosure opens exactly once for selected ' + selected);
    assert.equal(await page.getByRole('radio', { name: selectedLabel, exact: true }).isChecked(), true,
      'making both routes visible preserves selected ' + selected);
  }
});

test('visible route pair projection reads each root value from its own literal grid row', () => {
  const grid = {
    headers: ['Observation FHIR resource ID', 'Record status', 'Specimen FHIR resource ID'],
    rows: [
      ['35cfec85-56e8-5257-af99-2e9345be2011', 'first row', 'b7cad184-db67-5542-a975-10fffa3e89e7'],
      ['6ff35b3b-e7ac-5cc3-bdeb-302fb5f884da', 'second row', 'b7cad184-db67-5542-a975-10fffa3e89e7'],
      ['6492371a-d5f9-587f-9831-4125e93b3f27', 'third row', 'b7cad184-db67-5542-a975-10fffa3e89e7'],
    ],
  };
  const scalarize = value => value === '' || value === '—' ? null : value;

  assert.deepEqual(projectVisibleRoutePairs(
    grid, 'Specimen FHIR resource ID', 'Observation FHIR resource ID', scalarize,
  ), [
    ['b7cad184-db67-5542-a975-10fffa3e89e7', '35cfec85-56e8-5257-af99-2e9345be2011'],
    ['b7cad184-db67-5542-a975-10fffa3e89e7', '6ff35b3b-e7ac-5cc3-bdeb-302fb5f884da'],
    ['b7cad184-db67-5542-a975-10fffa3e89e7', '6492371a-d5f9-587f-9831-4125e93b3f27'],
  ]);
});

test('native preview scrolling preserves repeated cell pairs and rejects conflicting row ordinals', async (t) => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage({ viewport: { width: 800, height: 300 } });
  const virtualTable = ({ conflict = false } = {}) => `<!doctype html><html><head><style>
    #preview { width: 520px; height: 250px; overflow: auto; position: relative; }
    [role="table"] { position: relative; width: 520px; height: 1406px; }
    [role="row"] { position: absolute; left: 0; width: 520px; height: 44px; }
    [role="columnheader"], [role="cell"] { display: inline-block; width: 220px; height: 44px; }
  </style></head><body>
    <div id="preview" data-testid="preview-table-scroll">
      <div role="table" aria-rowcount="32" aria-colcount="2"></div>
    </div>
    <script>
      const preview = document.querySelector('#preview');
      const table = preview.querySelector('[role="table"]');
      const total = 31;
      function render() {
        const first = Math.max(0, Math.floor(Math.max(0, preview.scrollTop - 42) / 44) - 3);
        const visible = Math.max(1, Math.ceil(Math.max(0, preview.clientHeight - 42) / 44));
        const end = Math.min(total, first + visible + 6);
        const header = '<div role="row" style="position:sticky;top:0;height:42px"><span>Row</span><span role="columnheader">Specimen ID</span><span role="columnheader">Observation FHIR resource ID</span></div>';
        const rows = Array.from({ length: total }, (_, index) => index)
          .slice(first, end)
          .map(index => {
            const observation = ${conflict ? "preview.scrollTop > 0 && index === 10 ? 'changed' : 'same observation'" : "'same observation'"};
            return '<div role="row" style="top:' + (42 + index * 44) + 'px"><span>' + (index + 1) + '</span><div role="cell">same specimen</div><div role="cell">' + observation + '</div></div>';
          }).join('');
        table.innerHTML = header + rows;
      }
      preview.addEventListener('scroll', () => setTimeout(render, 15));
      render();
    </script>
  </body></html>`;

  await page.setContent(virtualTable());
  const collected = await collectPreviewRows(page, { timeout: 5000 });
  assert.equal(collected.rowCount, 31);
  assert.deepEqual(collected.rowOrdinals, Array.from({ length: 31 }, (_, index) => index + 1));
  assert.deepEqual(collected.rows.map(row => row.values),
    Array.from({ length: 31 }, () => ['same specimen', 'same observation']),
    'identical visible pairs at distinct row numbers must retain their full multiplicity');
  assert.equal(await page.locator('#preview').evaluate(element => element.scrollTop), 0,
    'successful full-table inspection restores the native preview to the top');

  const conflictPage = await browser.newPage({ viewport: { width: 800, height: 300 } });
  await conflictPage.setContent(virtualTable({ conflict: true }));
  await assert.rejects(collectPreviewRows(conflictPage, { timeout: 5000 }),
    /Preview row 11 changed between scroll windows/);
});

test('read-only Builder APIRequestContext resolves the project path against the configured API origin', async () => {
  const requests = [];
  const server = createServer((request, response) => {
    requests.push(`${request.method} ${request.url}`);
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ workspace: { documents: [] }, catalog: { generation: 'cda-fhir-v1' } }));
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  const requestContext = await playwrightRequest.newContext();
  try {
    const builder = await builderJSON(
      requestContext,
      `http://127.0.0.1:${address.port}`,
      '/api/v1/projects/loom_dev_cda_fhir/explorers/epoch126-qa/authoring/v2',
    );
    assert.deepEqual(builder, {
      workspace: { documents: [] },
      catalog: { generation: 'cda-fhir-v1' },
    });
    assert.deepEqual(requests, [
      'GET /api/v1/projects/loom_dev_cda_fhir/explorers/epoch126-qa/authoring/v2/builder',
    ]);
  } finally {
    await requestContext.dispose();
    await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
});

test('raw AQL follows both exact edge directions and is scoped to the pinned generation', () => {
  const query = buildCdaSavedRelatedRouteQuery({ project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
  assert.match(query, /DOCUMENT\("Specimen\/g_00003b7dca775d32e82a19f3c3c1227d234ca8d3486df068194137e93b1f796b"\)/);
  assert.match(query, /edge\._from == specimen\._id AND edge\.label == "subject_Patient"/);
  assert.match(query, /edge\._to == specimen\._id AND STARTS_WITH\(edge\._from, "Observation\/"\)/);
  assert.match(query, /edge\.label == "specimen_Specimen"/);
  assert.match(query, /edge\._to == patient\._id AND STARTS_WITH\(edge\._from, "Observation\/"\)/);
  assert.match(query, /observation\.payload\.specimen\.reference/);
  assert.match(query, /observation\.dataset_generation == "cda-fhir-v1"/);
  assert.match(query, /LIMIT 2[\s\S]*RETURN \{ id: observation\.id, _id: observation\._id \}/);
  assert.match(query, /LIMIT 32/);
  assert.throws(() => buildCdaSavedRelatedRouteQuery({ project: 'other', generation: 'cda-fhir-v1' }), /pinned CDA project/);
  assert.throws(() => buildCdaSavedRelatedRouteQuery({ project: 'loom_dev_cda_fhir', generation: 'other' }), /pinned CDA generation/);
});

test('raw result validation preserves both routes and reports null or missing reference state', () => {
  const rows = rawRowsFromOracle();
  const checked = assertCdaSavedRelatedRouteRows(rows, oracle);
  assert.deepEqual(checked.routeA.rows, oracle.routeA.rows);
  assert.deepEqual(checked.routeB.rows, oracle.routeB.rows);
  assert.deepEqual(checked.rawReferenceCounts, { present: 29, nullOrMissing: 2, null: 1, missing: 1 });

  const wrongRows = rawRowsFromOracle();
  wrongRows[0].patientObservations.pop();
  assert.throws(() => assertCdaSavedRelatedRouteRows(wrongRows, oracle), /exact 31 raw Observation identities/);
});
