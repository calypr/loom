import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { test, expect } from '../helpers/cda-fixtures.mjs';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';

export async function codedFieldLifecycleWorkflow({ page, cda, caseOptions = {} }) {
  const { click, fill, selectOption, navigate, inspect, clickControl, fillControl, selectControl,
    nativeClick, nativeFill, nativeSelect, navigatePage, inspectDOM, browserEval, inspectPage,
    waitForDOM, waitForBrowser, captureCDARequests, captureRequests, waitForCapturedResponse,
    performAction, requireUnique } = createNativeCdaWorkflowTools({ page, cda });
  const target = cda.target;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target, caseOptions.arangoContainer);
  assert(project && generation && apiOrigin && uiOrigin, 'The CDA fixture must bind project, generation, API origin, and UI origin explicitly.');
  assert.equal(generation, 'cda-fhir-v1', 'This verifier requires the loaded CDA FHIR generation.');
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `coded-field-lifecycle-${Date.now()}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const relationship = 'specimen_Specimen';
const familyCode = caseOptions.familyCode ?? cda.env?.LOOM_CDA_CODED_CONCEPT_CODE ?? 'specimen_type';
// FrameSourcePanel presents semantic entries as `display || code`; CDA specimen_type currently has no display.
const familyTitle = caseOptions.familyTitle ?? cda.env?.LOOM_CDA_CODED_CONCEPT_TITLE ?? familyCode;
const report = {
  explorer, project, generation, protectedExplorerUntouched: true,
  scenario: `Grouped Specimens; related Observation ${familyTitle} component valueString; source form ALL; grouped-row policy ONE then ALL`,
  started: new Date().toISOString(), cases: [], apiCalls: [], errors: [],
};

let builder;
let outputId;
const nativeRequests = cda.report.nativeRequests;
report.nativeRequests = nativeRequests;
let requestCapture;

const api = async (path, body) => {
  const startedAt = Date.now();
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': `native-cda-${randomUUID()}` };
  const url = apiOrigin + path;
  const response = body === undefined
    ? await cda.request.get(url, { headers, timeout: 30000 })
    : await cda.request.post(url, { headers, data: body, timeout: 30000 });
  const value = await response.json();
  const apiEvidence = { path, status: response.status(), body, response: value, startedAt, completedAt: Date.now() };
  report.apiCalls?.push(apiEvidence);
  report.requests?.push(apiEvidence);
  assert(response.ok(), `${path}: ${JSON.stringify(value)}`);
  return value;
};
const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1000)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};
const command = async (commands) => {
  await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(`${base}/builder`);
  return builder;
};
const doc = (state = builder) => state.workspace.documents.find((document) => document.output.id === outputId);
const startNativeCapture = () => {
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (path.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
  });
  requestCapture = captureCDARequests(page, {
    apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: `${root}/${explorer}`,
    report, responsePaths: /frame-source-options|semantic-inventory|construction-choice-proposals|construction-proposals|commands|preview/,
  });
};
const waitNative = async (predicate, fromIndex = 0, timeoutMs = 5000) => {
  const match = await requestCapture.waitFor(predicate, { fromIndex, timeoutMs });
  const rawResponse = requestCapture.rawResponseBody(match);
  assert(rawResponse && typeof rawResponse === 'object' && !Array.isArray(rawResponse),
    `${match.path} did not provide a captured JSON object response body`);
  return { ...match, response: rawResponse };
};
const record = (name, started, details = {}) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  report.cases.push({ name, durationMs, ...details });
};
const openTable = async (expectedColumnCount, name, expectedTexts = [], expectedRows) => {
  const started = Date.now();
  const fromIndex = nativeRequests.length;
  await navigatePage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (`[data-testid="construction-table-${outputId}"]`) }, 5000);
  await clickControl(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForDOM(page, args => Boolean((() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'));})()), {}, 5000);
  const previewSelector = '[data-testid="preview-table-scroll"]';
  const previewLocator = page.locator(previewSelector);
  const readMountedRows = () => inspectDOM(page, () => {
    const preview=document.querySelector('[data-testid="preview-table-scroll"]');
    const table=preview?.querySelector('[role="table"]');
    if(!preview||!table) throw new Error('Native preview table disappeared');
    const rows=[...table.querySelectorAll('[role="row"]')].map(row=>{
      const rawIndex=Number(row.getAttribute('aria-rowindex'));
      const labelIndex=Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/^Inspect row (\d+) identity$/)?.[1]);
      const gutterIndex=Number(row.firstElementChild?.textContent?.trim());
      const index=Number.isInteger(rawIndex)&&rawIndex>0?rawIndex:Number.isInteger(labelIndex)&&labelIndex>0?labelIndex+1:Number.isInteger(gutterIndex)&&gutterIndex>0?gutterIndex+1:NaN;
      const cells=[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim());
      return { index, cells };
    }).filter(row=>row.index>1&&row.cells.length);
    return { rowCount:Number(table.getAttribute('aria-rowcount')), columnCount:table.getAttribute('aria-colcount'), headers:[...preview.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()), scrollTop:preview.scrollTop, clientHeight:preview.clientHeight, scrollHeight:preview.scrollHeight, rows };
  });
  const layout = await readMountedRows();
  const rowsByAriaRowIndex = new Map(layout.rows.map(({ index, cells }) => [index, cells]));
  for (let pageIndex=0; pageIndex<100 && rowsByAriaRowIndex.size<layout.rowCount-1; pageIndex++) {
    const previousTop = layout.scrollTop;
    if (previousTop + layout.clientHeight >= layout.scrollHeight) break;
    await performAction(page, 'Scroll native preview rows', previewLocator, async (target) => { await target.hover(); await page.mouse.wheel(0, Math.max(100, Math.floor(layout.clientHeight / 2))); });
    await waitForDOM(page, ({ previousTop }) => { const preview=document.querySelector('[data-testid="preview-table-scroll"]'); return Boolean(preview && (preview.scrollTop > previousTop || preview.scrollTop + preview.clientHeight >= preview.scrollHeight)); }, { previousTop }, 5000);
    Object.assign(layout, await readMountedRows());
    for (const { index, cells } of layout.rows) rowsByAriaRowIndex.set(index, cells);
  }
  if (layout.scrollTop > 0) {
    const previousTop = layout.scrollTop;
    await performAction(page, 'Return native preview to first row', previewLocator, async (target) => { await target.hover(); await page.mouse.wheel(0, -100000); });
    await waitForDOM(page, ({ previousTop }) => { const preview=document.querySelector('[data-testid="preview-table-scroll"]'); return Boolean(preview && preview.scrollTop < previousTop); }, { previousTop }, 5000);
  }
  const indexedRows=[...rowsByAriaRowIndex.entries()].sort((left,right)=>left[0]-right[0]);
  const dom={ rowCount:String(layout.rowCount), dataRowCount:layout.rowCount-1, columnCount:layout.columnCount, headers:layout.headers, rowIndexes:indexedRows.map(([index])=>index), rows:indexedRows.map(([,cells])=>cells) };
  assert.equal(dom.columnCount, String(expectedColumnCount), `${name} rendered the wrong column count: ${JSON.stringify(dom)}`);
  assert.equal(dom.rows.length, dom.dataRowCount, `${name} did not collect every row from the virtualized native preview`);
  assert.deepEqual(dom.rowIndexes, Array.from({ length: dom.dataRowCount }, (_, index) => index + 2), `${name} has a gap in native aria row indexes`);
  const allCells = dom.rows.flat();
  for (const value of expectedTexts) assert(allCells.some((cell) => cell.includes(value)), `${name} omitted ${value}: ${JSON.stringify(dom)}`);
  if (expectedRows) {
    const actual = dom.rows.map((row) => JSON.stringify(row)).sort();
    const expected = expectedRows.map((row) => JSON.stringify(row)).sort();
    assert.deepEqual(actual, expected, `${name} native rows differ from the independent expected rows`);
  }
  const preview = await waitNative((entry) => entry.path.endsWith('/preview') && entry.body?.outputId === outputId, fromIndex);
  assert(preview.response?.receiptId, `${name} saved preview has no current receipt`);
  record(name, started, { headers: dom.headers, receiptId: preview.response.receiptId, rowCount: preview.response.rowCount, nativeRowCount: dom.rows.length });
  return { dom, preview: preview.response };
};
const proposal = async (name, started, expectedRows) => {
  await waitForDOM(page, args => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)), {}, 5000);
  const value = await inspectDOM(page, async args => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:panel?.dataset.proposalStatus,text:panel?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}; });
  assert.equal(value.status, 'ready', `${name}: ${value.text}`);
  assert.equal(value.rows.length, expectedRows.length, `${name}: ${JSON.stringify(value.rows)}`);
  for (const row of value.rows) assert(expectedRows.some((expected) => JSON.stringify(expected) === JSON.stringify(row)), `${name} differs from raw CDA witness: ${JSON.stringify(row)}`);
  record(name, started, { rows: value.rows });
  return value;
};
const clickGroupKey = async () => {
  await clickControl(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled===false), {}, 5000);
  await clickControl(page, '[data-testid="construction-action-group-rows"]');
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-group"] input[aria-label="Group by Patient reference"]:not(:disabled)'))), {}, 5000);
  const started = Date.now();
  await clickControl(page, '[data-testid="construction-reshape-group"] input[aria-label="Group by Patient reference"]');
  return started;
};
const applyGroupProposal = async (started, expectedRows, label) => {
  await proposal(`${label}-preview`, started, expectedRows);
  const applyStarted = Date.now();
  await clickControl(page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), {}, 5000);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(args.__template0)), { __template0: ('Coded field lifecycle QA') }, 5000);
  record(`${label}-apply`, applyStarted);
  builder = await api(`${base}/builder`);
};
const openAddColumns = async () => {
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-action-add-columns"]')?.disabled===false), {}, 5000);
  await clickControl(page, '[data-testid="construction-action-add-columns"]');
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[data-testid="frame-source-panel"]'))), {}, 5000);
  await clickControl(page, '[aria-label="Column types"] button', { name: 'Coded values' });
};
const setSearchInput = async (selector, value) => fillControl(page, selector, value);
const chooseComponentFrame = async () => {
  const panel = '[data-testid="frame-source-panel"]';
  const toggle = await inspectDOM(page, async args => { return [...document.querySelectorAll(args.__template0)].find(button=>['Browse sources','Add coded source'].some(label=>button.innerText.trim().startsWith(label)))?.innerText.replace(/\\s+/g,' ').trim(); }, { __template0: (`${panel} button`) });
  const cancellationAction = 'search Observation framing sources';
  const searchStarted = Date.now();
  let result;
  await cda.withExpectedCancellations({
    origin: uiOrigin,
    method: 'POST',
    paths: [`${base}/frame-source-options`],
    requestIdPrefixes: ['frame-source-options-'],
    reason: 'The initial unfiltered framing-source request was superseded by the explicit Observation search.',
    proof: { priorQuery: null, replacementQuery: 'Observation' },
    actionLabel: cancellationAction,
  }, async () => {
    if (toggle) await clickControl(page, `${panel} button`, { name: toggle });
    await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[aria-label="Search framing sources"]'))), {}, 5000);
    await setSearchInput('[aria-label="Search framing sources"]', 'Observation');
    const fromIndex = nativeRequests.length;
    await clickControl(page, `${panel} form button`, { name: 'Search' });
    result = await waitNative((entry) => entry.path.endsWith('/frame-source-options') && entry.body?.query === 'Observation', fromIndex);
  });
  const cancellations = cda.report.expectedCancellations?.filter(item => item.proof?.scopeAction === cancellationAction) ?? [];
  assert(cancellations.length <= 1, 'Observation frame-source search may classify at most one superseded initial browse');
  const cancellation = cancellations[0];
  if (cancellation) {
    const cancelledRequest = nativeRequests.find(request => request.browserRequestId === cancellation.browserRequestId);
    assert(cancelledRequest, 'Expected frame-source cancellation must point to an exact captured browser request');
    assert.equal(cancelledRequest.path, `${base}/frame-source-options`);
    assert.equal(cancelledRequest.method, 'POST');
    assert([undefined, null, ''].includes(cancelledRequest.body?.query),
      'The cancelled frame-source request must be the initial unfiltered browse');
    const cancelledError = report.errors.find(error => error.kind === 'network'
      && error.browserRequestId === cancellation.browserRequestId);
    assert(cancelledError, 'Expected frame-source cancellation must remain in browser diagnostics');
    assert.equal(cancelledError.error, 'net::ERR_ABORTED');
    cancelledError.expected = true;
    cancelledError.expectedCancellation = cancellation;
    assert.equal(result.body?.query, 'Observation');
    assert.equal(result.status, 200);
    report.frameSourceBrowseCancellation = {
      request: { browserRequestId: cancelledRequest.browserRequestId, path: cancelledRequest.path,
        method: cancelledRequest.method, query: cancelledRequest.body?.query ?? null, error: cancelledError.error },
      replacement: { query: 'Observation', status: result.status },
      reason: cancellation.reason,
    };
  }
  const matchesFor = (response) => response.sources.filter((source) => source.resourceType === 'Observation' &&
    source.sourcePath.toLowerCase().includes('component') && source.route.length === 1 &&
    source.route[0].fromResourceType === 'Specimen' && source.route[0].toResourceType === 'Observation' &&
    source.route[0].relationship === relationship && source.route[0].storageDirection === 'INBOUND' &&
    (source.valuePath.toLowerCase().includes('valuestring') || source.logicalType.toLowerCase() === 'string') &&
    source.forms.some((form) => form.form === 'ALL'));
  let matches = matchesFor(result.response);
  let pageCount = 1;
  while (!matches.length && result.response.nextCursor && pageCount < 8) {
    const next = nativeRequests.length;
    await clickControl(page, `${panel} button`, { name: 'More sources and paths' });
    result = await waitNative((entry) => entry.path.endsWith('/frame-source-options') && Boolean(entry.body?.cursor), next);
    matches = matchesFor(result.response);
    pageCount += 1;
  }
  assert.equal(matches.length, 1, `Direct component source is missing or ambiguous after ${pageCount} pages: ${JSON.stringify(matches.map((source) => ({ title: source.title, path: source.sourcePath, route: source.route })))}`);
  const source = matches[0];
  const choiceSelector = `[data-testid=${JSON.stringify(`frame-source-choice-${source.choiceId}`)}]`;
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (choiceSelector) }, 5000);
  const formSelector = `select[aria-label=${JSON.stringify(`Multiple values for ${source.title}`)}]`;
  const formOptions = await inspectDOM(page, async args => { const select=document.querySelector(args.__template0);return select?[...select.options].map(option=>option.value):[]; }, { __template0: (formSelector) });
  if (formOptions.length) {
    assert(formOptions.includes('ALL'));
    await clickControl(page, `${panel} div.px-3.py-2:has(> ${choiceSelector}) details > summary`, { name: 'When a row has several values' });
    await selectControl(page, formSelector, 'ALL');
  } else assert.equal(source.defaultForm, 'ALL');
  const saveIndex = nativeRequests.length;
  await clickControl(page, choiceSelector);
  const save = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'SET_FRAME_SOURCE'), saveIndex);
  builder = await api(`${base}/builder`);
  const frame = doc().frames?.[0];
  assert(frame, 'The component frame was not saved');
  assert.equal(frame.form, 'ALL', 'Per-record repeated component values must stay intact before grouped contributor reduction');
  assert.equal(frame.zeroPolicy, 'EMPTY_LIST', 'The component frame must preserve the explicit empty-list result for missing coded values');
  assert.equal(frame.source.resourceType, 'Observation');
  assert(frame.source.sourcePath.toLowerCase().includes('component'));
  assert(frame.source.valuePath.toLowerCase().includes('valuestring') || frame.source.logicalType.toLowerCase() === 'string');
  assert.deepEqual(frame.route.map((step) => ({ from: step.fromResourceType, to: step.toResourceType, relationship: step.relationship, storageDirection: step.storageDirection })), [
    { from: 'Specimen', to: 'Observation', relationship, storageDirection: 'INBOUND' },
  ]);
  record('save-fresh-signed-component-frame', searchStarted, { frameId: frame.id, frameChoiceId: source.choiceId, pages: pageCount, status: save.status });
  return frame;
};
const chooseFreshFamily = async (frame) => {
  const panel = `[data-testid=${JSON.stringify(`frame-categories-${frame.id}`)}]`;
  const input = `${panel} input[aria-label^="Search coded values in "]`;
  const started = Date.now();
  await setSearchInput(input, familyCode);
  const fromIndex = nativeRequests.length;
  await clickControl(page, `${panel} form button`, { name: 'Search' });
  const entry = await waitNative((candidate) => candidate.path.endsWith('/semantic-inventory') &&
    candidate.body?.frameId === frame.id && candidate.body?.query === familyCode, fromIndex);
  assert.equal(entry.response.state, 'complete');
  assert.equal(entry.response.frameSource?.id, frame.id);
  const matches = entry.response.entries.filter((item) => item.code === familyCode && item.resourceType === 'Observation');
  assert.equal(matches.length, 1, `Expected one fresh ${familyCode} semantic binding; got ${matches.length}`);
  const item = matches[0];
  assert.equal(item.display || item.code, familyTitle, `The fresh semantic binding title changed for ${familyCode}`);
  assert(item.constructionChoice?.choiceId, 'No fresh signed construction choice was returned');
  assert(item.constructionChoice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED'), 'The coded component binding does not support per-record ALL');
  assert(item.valueType.toLowerCase().includes('string'));
  assert(['READY', 'READY_WITH_WARNING'].includes(item.readiness.status), `Coded binding is not ready: ${item.readiness.status}`);
  const label = item.display || item.code;
  const checkbox = `${panel} input[aria-label=${JSON.stringify(`Select ${label}`)}]`;
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0)&&!document.querySelector(args.__template1).disabled)), { __template0: (checkbox), __template1: (checkbox) }, 5000);
  await clickControl(page, checkbox);
  record(`fresh-signed-${familyCode}-choice`, started, { frameId: frame.id, choiceId: item.constructionChoice.choiceId, valueType: item.valueType });
  return { code: item.code, label, choiceId: item.constructionChoice.choiceId, frameId: frame.id };
};
const beginAddFamily = async (policy) => {
  await selectControl(page, 'select[aria-label="Values per grouped row"]', policy);
  const started = Date.now();
  const fromIndex = nativeRequests.length;
  const frame = doc().frames[0];
  await clickControl(page, `[data-testid=${JSON.stringify(`frame-categories-${frame.id}`)}] button`, { name: 'Add 1 column' });
  return { started, fromIndex };
};
const checkUiRender = async (columnCount, expectedText = []) => {
  await waitForDOM(page, args => Boolean((() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&table.getAttribute('aria-colcount')===args.__template0&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'));})()), { __template0: (String(columnCount)) }, 5000);
  const value = await inspectDOM(page, async args => { return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),cells:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim())}; });
  for (const item of expectedText) assert(value.cells.some((cell) => cell.includes(item)), `Rendered coded result omitted ${item}: ${JSON.stringify(value)}`);
  return value;
};


report.target ??= { ...cda.report.target };
report.nativeRequests = cda.report.nativeRequests;
report.errors = cda.report.errors;

  try {

assert.notEqual(explorer, protectedExplorer, 'Only a fresh owned Explorer may be used');
const witnessFinderQuery = `
FOR p IN (
FOR candidate IN Patient
  FILTER candidate.project == ${JSON.stringify(project)} AND candidate.dataset_generation == ${JSON.stringify(generation)}
  SORT candidate.id
  LIMIT 1000
  RETURN candidate
)
LET members = (
  FOR e IN fhir_edge
    FILTER e._to == p._id AND STARTS_WITH(e._from, "Specimen/") AND e.label == "subject_Patient"
      AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
    COLLECT specimenKey = e._from
    LET s = DOCUMENT(specimenKey)
    FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
    SORT s.id
    LIMIT 25
    RETURN { id: s.id, _id: s._id, patientReference: s.payload.subject.reference }
)
FILTER LENGTH(members) >= 3 AND LENGTH(members) <= 25
FILTER LENGTH(FOR member IN members FILTER member.patientReference == CONCAT("Patient/", p.id) RETURN 1) == LENGTH(members)
LET rawValues = (
  FOR member IN members
    FOR oe IN fhir_edge
      FILTER oe._to == member._id AND STARTS_WITH(oe._from, "Observation/") AND oe.label == ${JSON.stringify(relationship)}
        AND oe.project == ${JSON.stringify(project)} AND oe.dataset_generation == ${JSON.stringify(generation)}
      LET o = DOCUMENT(oe._from)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      FOR component IN (IS_ARRAY(o.payload.component) ? o.payload.component : [])
        FILTER LENGTH(FOR coding IN (IS_ARRAY(component.code.coding) ? component.code.coding : [])
          FILTER coding.system == "https://cda.readthedocs.io" AND coding.code == ${JSON.stringify(familyCode)} RETURN 1) > 0
        FILTER IS_STRING(component.valueString)
        RETURN component.valueString
)
LET distinctValues = SORTED_UNIQUE(rawValues)
FILTER LENGTH(distinctValues) > 1
LIMIT 1
RETURN {
  patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
  members, witnessDistinctValues: distinctValues
}
`;
const [witness] = rawQuery(witnessFinderQuery);
assert(witness, `No current-generation CDA Patient among the first 1000 sorted roots has 3–25 linked Specimens whose ${familyCode} component values contain a conflict; ONE must not be expected to fail without this bounded witness.`);
assert(witness.members.length >= 3 && witness.members.length <= 25);
const selectedSpecimenKeys = witness.members.map((member) => member._id);
const oracleQuery = `
LET selectedKeys = ${JSON.stringify(selectedSpecimenKeys)}
LET members = (
FOR s IN Specimen
  FILTER s._id IN selectedKeys AND s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
  LET observations = (
    FOR oe IN fhir_edge
      FILTER oe._to == s._id AND STARTS_WITH(oe._from, "Observation/") AND oe.label == ${JSON.stringify(relationship)}
        AND oe.project == ${JSON.stringify(project)} AND oe.dataset_generation == ${JSON.stringify(generation)}
      LET o = DOCUMENT(oe._from)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      RETURN DISTINCT { id: o.id, _id: o._id, payload: o.payload }
  )
  LET rawValues = (
    FOR o IN observations
      FOR component IN (IS_ARRAY(o.payload.component) ? o.payload.component : [])
        FILTER LENGTH(FOR coding IN (IS_ARRAY(component.code.coding) ? component.code.coding : [])
          FILTER coding.system == "https://cda.readthedocs.io" AND coding.code == ${JSON.stringify(familyCode)} RETURN 1) > 0
        FILTER IS_STRING(component.valueString)
        RETURN component.valueString
  )
  RETURN {
    id: s.id, _id: s._id, patientReference: s.payload.subject.reference,
    observationIds: (FOR o IN observations SORT o.id RETURN o.id), observationCount: LENGTH(observations),
    rawValues, distinctValues: SORTED_UNIQUE(rawValues)
  }
)
LET rawValues = (FOR member IN members FOR value IN member.rawValues RETURN value)
RETURN {
patient: ${JSON.stringify(witness.patient)},
members, contributorCount: LENGTH(members), rawValueCount: LENGTH(rawValues),
distinctValues: SORTED_UNIQUE(rawValues)
}
`;
const [source] = rawQuery(oracleQuery);
assert(source, 'The exact selected Specimen membership no longer resolves in the current-generation raw CDA oracle.');
assert.equal(source.members.length, source.contributorCount);
assert(source.contributorCount >= 3 && source.contributorCount <= 25);
assert.deepEqual(source.members.map((member) => member._id).sort(), [...selectedSpecimenKeys].sort(), 'The independent raw oracle must cover exactly the selected Specimen contributors');
assert(source.distinctValues.length > 1, 'ONE failure requires independent evidence of more than one distinct coded value across this Group row');
assert.deepEqual(source.distinctValues, witness.witnessDistinctValues, 'The exact-membership raw oracle disagrees with the bounded witness finder');
assert(source.members.every((member) => member.patientReference === source.patient.reference), 'The independently scoped source rows do not share the exact grouping key');
report.oracle = {
  kind: 'bounded raw CDA witness finder plus a separate exact-membership raw CDA oracle over selected current-generation fhir_edge joins',
  witnessFinderQuery,
  query: oracleQuery,
  patient: source.patient,
  contributorCount: source.contributorCount,
  rawValueCount: source.rawValueCount,
  distinctValues: source.distinctValues,
  perSpecimenDistinctValueCounts: source.members.map((member) => ({ specimenId: member.id, valueCount: member.distinctValues.length })),
  members: source.members.map(({ id, patientReference, observationCount, rawValues, distinctValues }) => ({ id, patientReference, observationCount, rawValues, distinctValues })),
  warning: 'Contributor count and each Specimen’s coded-value cardinality are reported separately. The ONE conflict precondition is only an independently observed union of more than one coded value; no per-Specimen zero/one/many composition is assumed.',
};

await api(root, { name: explorer, title: 'Coded field lifecycle QA' });
builder = await api(`${base}/builder`);
assert.equal(builder.catalog.generation, generation);
const rootNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen' && node.rowRootEligible);
assert(rootNode, 'Current catalog has no authorized Specimen row root');
const created = await command([{ type: 'CREATE_TABLE', title: 'Coded field lifecycle QA', rootNodeId: rootNode.nodeId }]);
assert.equal(created.workspace.documents.length, 1, 'CREATE_TABLE must create exactly one saved output in this fresh Explorer');
outputId = created.workspace.documents[0].output.id;
const idField = builder.catalog.candidates.find((candidate) => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
const patientField = builder.catalog.candidates.find((candidate) => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'subject.reference');
assert(idField && patientField, 'Current Specimen catalog must expose independent id and subject.reference fields');
await command([
  { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' },
  { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: patientField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient reference' },
]);
const selection = await api(`${base.replace('/authoring/v2', '')}/selections`, {
  snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
  source: { kind: 'resources', resources: { refs: source.members.map((member) => ({ project, generation, resourceType: 'Specimen', id: member.id })) } },
});
assert.equal(selection.memberCount, source.contributorCount);
const routes = await api(`${base}/population-routes`, { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
const direct = routes.choices.find((choice) => choice.route.length === 0);
assert(direct, 'Exact bounded Specimen selection has no direct population route');
await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
report.ownedWorkspace = { explorer, outputId, selectionRevisionId: selection.id, memberCount: selection.memberCount };

startNativeCapture();
const sourceRows = source.members.map((member) => [member.id, member.patientReference]);
await openTable(2, 'reload-selected-specimen-source', sourceRows.flat(), sourceRows);
const sourceBaseline = await api(`${base}/builder`);
const sourceDocument = doc(sourceBaseline);
assert.equal(sourceDocument.population.selectionRevisionId, selection.id);
assert.deepEqual(sourceDocument.columns.map((column) => column.label).sort(), ['Patient reference', 'Specimen ID']);

const groupedExpected = [[source.patient.reference, String(source.contributorCount)]];
let started = await clickGroupKey();
await proposal('patient-reference-group-preview-cancel-target', started, groupedExpected);
const beforeGroupCancel = await api(`${base}/builder`);
const cancelGroupStarted = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), {}, 5000);
const afterGroupCancel = await api(`${base}/builder`);
assert.deepEqual(afterGroupCancel.workspace, beforeGroupCancel.workspace, 'Cancel must preserve the exact saved source table');
record('cancel-group-preview-preserves-source', cancelGroupStarted);

started = await clickGroupKey();
await applyGroupProposal(started, groupedExpected, 'group-specimens-by-patient-reference');
const groupedWorkspace = structuredClone(builder.workspace);
const groupedTable = await openTable(2, 'reload-grouped-specimen-contributors', [source.patient.reference, String(source.contributorCount)], groupedExpected);
assert.equal(groupedTable.preview.rowCount, 1, 'Raw fixture describes exactly one Patient group row');
const groupStep = doc().construction.steps.find((step) => step.operation.kind === 'GROUP');
assert(groupStep, 'Patient grouping was not saved as a native Group operation');

await openAddColumns();
const frame = await chooseComponentFrame();
const frameBaseline = await api(`${base}/builder`);
assert.deepEqual(doc(frameBaseline).construction, groupedWorkspace.documents.find((document) => document.output.id === outputId).construction, 'Adding a source frame must not change the Group operation');
assert.deepEqual(doc(frameBaseline).population, groupedWorkspace.documents.find((document) => document.output.id === outputId).population, 'Adding a source frame must preserve the exact bounded population');
const codePanel = `[data-testid=${JSON.stringify(`frame-categories-${frame.id}`)}]`;
await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (codePanel) }, 5000);
const beforeCategoryCancel = structuredClone(builder.workspace);
await chooseFreshFamily(frame);
const categoryCancelStart = Date.now();
const cancelNativeIndex = nativeRequests.length;
await nativeClick(page, '[data-testid="construction-close-operation-editor"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-operation-editor"]')), {}, 5000);
builder = await api(`${base}/builder`);
assert.deepEqual(builder.workspace, beforeCategoryCancel, 'Closing the coded-value chooser must not add the selected column');
assert.equal(nativeRequests.slice(cancelNativeIndex).filter((entry) => entry.path.endsWith('/commands')).length, 0, 'Canceling an unsubmitted coded category must not mutate the workspace');
record('cancel-coded-category-selection', categoryCancelStart, { retainedFrameId: frame.id });
await openAddColumns();
const savedFramePanel = `[data-testid=${JSON.stringify(`saved-frame-${frame.id}`)}]`;
await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (savedFramePanel) }, 5000);
await nativeClick(page, `${savedFramePanel} button`, { name: 'Choose values' });
await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (codePanel) }, 5000);
await chooseFreshFamily(frame);
const rowValueControl = await cda.inspect(async args => { return {found:Boolean(document.querySelector('select[aria-label="Values per grouped row"]')),value:document.querySelector('select[aria-label="Values per grouped row"]')?.value}; }, {});
assert.equal(rowValueControl.found, true, 'Grouping must expose the distinct row-contributor ONE/ALL control');

const oneAttempt = await beginAddFamily('ONE');
const oneFailure = await waitNative((entry) => entry.path.endsWith('/construction-choice-proposals') && entry.body?.constructionChoices?.length === 1, oneAttempt.fromIndex);
const oneElapsed = Date.now() - oneAttempt.started;
assert.equal(oneFailure.body.constructionChoices[0].form, 'ALL', 'Per-record repeated component values must be preserved before contributor reduction');
assert.equal(oneFailure.body.constructionChoices[0].rowValuePolicy, 'ONE', 'The failing policy must be grouped-row ONE, separate from source form ALL');
assert.equal(oneFailure.body.constructionChoices[0].frameId, frame.id);
assert.equal(oneFailure.status, 422, JSON.stringify(oneFailure));
const oneError = oneFailure.response?.error?.code ?? oneFailure.response?.code;
assert.equal(oneError, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', JSON.stringify(oneFailure.response));
const oneFailureCapturedEntry = nativeRequests.find((entry) => entry.browserRequestId === oneFailure.browserRequestId);
assert(oneFailureCapturedEntry, 'The ONE-policy response must resolve to its exact captured native request entry');
cda.expectHttpFailure(oneFailureCapturedEntry,
  'The independent raw CDA witness contains multiple distinct values under the explicitly selected grouped-row ONE policy.',
  { action: 'preview coded component values with grouped-row policy ONE', form: 'ALL', rowValuePolicy: 'ONE',
    frameId: frame.id, distinctValues: source.distinctValues });
assert(oneElapsed <= 5000, `ONE disagreement preflight took ${oneElapsed} ms`);
const afterOneFailure = await api(`${base}/builder`);
assert.deepEqual(afterOneFailure.workspace, frameBaseline.workspace, 'Failed ONE preflight must leave the grouped table and frame unchanged');
record('coded-one-disagreement-from-independent-cda-values', oneAttempt.started, {
  httpStatus: oneFailure.status, errorCode: oneError, contributorCount: source.contributorCount,
  rawValueCount: source.rawValueCount, distinctValues: source.distinctValues,
});

const beforeAllSelection = nativeRequests.length;
const repairState = await cda.inspect(async args => {
  const editor = document.querySelector('[data-testid="construction-operation-editor"]');
  const panel = document.querySelector(args.__template0);
  const policy = document.querySelector('select[aria-label="Values per grouped row"]');
  const add = [...(panel?.querySelectorAll('button') ?? [])]
    .find((button) => button.textContent.trim() === 'Add 1 column');
  return {
    editorOpen: Boolean(editor),
    selectionCount: panel?.querySelectorAll('input[type="checkbox"]:checked').length ?? 0,
    policy: policy?.value,
    addDisabled: add?.disabled,
  };
 }, { __template0: (codePanel) });
assert.equal(repairState.editorOpen, true, 'ONE rejection must keep the Add Columns editor open');
assert.equal(repairState.selectionCount, 1, 'The coded choice must remain selected after ONE rejection');
assert.equal(repairState.policy, 'ONE', 'The retained chooser should still show the rejected ONE policy');
assert.equal(repairState.addDisabled, false, 'The retained coded choice should remain available for repair');
const allAttempt = await beginAddFamily('ALL');
const allProposal = await waitNative((entry) => entry.path.endsWith('/construction-choice-proposals') && entry.body?.constructionChoices?.length === 1, allAttempt.fromIndex);
record('coded-all-preflight', allAttempt.started, { previewDurationMs: allProposal.response?.previewDurationMs });
assert.equal(allProposal.status, 200, JSON.stringify(allProposal.response));
assert.equal(allProposal.response.previewStatus, 'READY', JSON.stringify(allProposal.response));
assert(allProposal.response.previewDurationMs <= 5000, `ALL preflight receipt took ${allProposal.response.previewDurationMs} ms`);
assert.equal(allProposal.body.constructionChoices[0].form, 'ALL');
assert.equal(allProposal.body.constructionChoices[0].rowValuePolicy, 'ALL');
assert.equal(allProposal.body.constructionChoices[0].frameId, frame.id);
assert.equal(allProposal.response.candidateColumnIds.length, 1);
const codedColumnId = allProposal.response.candidateColumnIds[0];
assert.deepEqual(allProposal.response.preview.rows[0]?.[codedColumnId], source.distinctValues, 'ALL coded column must equal the independent sorted unique CDA component values');
assert.equal(nativeRequests.slice(beforeAllSelection).filter((entry) => entry.path.endsWith('/semantic-inventory')).length, 0, 'Inline ONE-to-ALL repair must reuse the retained choice without another semantic search');
const allCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE'), allAttempt.fromIndex);
assert.equal(allCommand.status, 200, JSON.stringify(allCommand.response));
assert.equal(allCommand.body.commands.length, 1);
assert.equal(allCommand.body.commands[0].constructionChoice.form, 'ALL');
assert.equal(allCommand.body.commands[0].constructionChoice.rowValuePolicy, 'ALL');
assert.equal(allCommand.body.commands[0].constructionChoice.frameId, frame.id);
const allApplyStarted = Date.now();
await checkUiRender(3, source.distinctValues);
record('coded-all-apply-to-native-render', allApplyStarted, { commandStatus: allCommand.status });
const savedPreviewStarted = Date.now();
builder = await api(`${base}/builder`);
const addedDocument = doc();
const codedColumn = addedDocument.columns.find((column) => column.column === codedColumnId);
assert(codedColumn && codedColumn.frameId === frame.id, 'Saved coded output lost its semantic frame binding');
const savedGroup = addedDocument.construction.steps.find((step) => step.operation.kind === 'GROUP');
const savedRowValue = savedGroup?.rowValues.find((value) => value.inputColumnId === codedColumn.columnId);
assert(savedRowValue, 'Saved Group step did not bind the coded source column as a row-value input');
assert.equal(savedRowValue.policy, 'ALL', 'Saved Group step did not retain the grouped-row ALL policy');
const rowValueOutputId = savedRowValue.outputColumnId;
const groupedOutput = savedGroup.outputs.find((output) => output.id === rowValueOutputId);
assert(groupedOutput, 'Saved Group step did not declare the derived row-value output');
assert.equal(groupedOutput.name, codedColumn.column, 'The Group row-value output must target the coded column’s physical output name');
assert.equal(groupedOutput.label, codedColumn.label);
const groupStepId = savedGroup.id;
const commandIndex = nativeRequests.findIndex((entry) => entry.browserRequestId === allCommand.browserRequestId);
assert(commandIndex >= 0, 'Applied coded choice command must remain in the captured native request sequence');
const appliedPreview = await waitNative((entry) => entry.path.endsWith('/preview') && entry.body?.outputId === outputId, commandIndex + 1, 5000);
assert.deepEqual(appliedPreview.response.rows[0]?.[codedColumn.column], source.distinctValues);
assert.equal(appliedPreview.response.rowCount, 1);
record('coded-all-saved-preview-matches-independent-cda', savedPreviewStarted, {
  candidateColumnId: codedColumnId, values: source.distinctValues,
  sameChooserSemanticInventoryRequestCount: nativeRequests.slice(beforeAllSelection).filter((entry) => entry.path.endsWith('/semantic-inventory')).length,
});

const reloaded = await openTable(3, 'reload-applied-coded-contributor-values', source.distinctValues);
assert.deepEqual(reloaded.preview.rows[0]?.[codedColumn.column], source.distinctValues, 'Reloaded saved values differ from independent raw CDA oracle');

const newLabel = `CDA ${familyTitle} values`;
const editStart = Date.now();
const editFrom = nativeRequests.length;
await nativeClick(page, 'button', { name: 'Columns' });
const labelSelector = `[aria-label=${JSON.stringify(`Column name for ${codedColumn.label}`)}]`;
await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (labelSelector) }, 5000);
report.renameControlBefore = await cda.inspect(async args => { const i=document.querySelector(args.__template0);return {value:i?.value,disabled:i?.disabled,readOnly:i?.readOnly,label:i?.getAttribute('aria-label')}; }, { __template0: (labelSelector) });
assert.equal(report.renameControlBefore.disabled, false, 'Configured coded column rename must be enabled');
const labelLocator = page.locator(labelSelector);
await test.step('Rename configured coded output', async () => { await expect(labelLocator).toBeEditable({ timeout: 5000 }); await labelLocator.fill(newLabel, { timeout: 5000 }); }, { timeout: 5000 });
report.renameControlAfterInput = await cda.inspect(async args => { const i=document.activeElement;return {value:i?.value,label:i?.getAttribute('aria-label')}; }, {});
assert.equal(report.renameControlAfterInput.value, newLabel, 'Native typing must update the focused rename input');
await test.step('Save renamed coded output', () => labelLocator.press('Enter', { timeout: 5000 }), { timeout: 5000 });
const rename = await waitNative((entry) => entry.path.endsWith('/commands') &&
  entry.body?.commands?.some((item) => item.type === 'UPDATE_CONSTRUCTION_OUTPUT' &&
    item.constructionOutput?.stepId === groupStepId &&
    item.constructionOutput?.columnId === rowValueOutputId), editFrom);
assert.equal(rename.status, 200, JSON.stringify(rename.response));
assert.equal(rename.body.commands.length, 1);
assert.equal(rename.body.commands[0].type, 'UPDATE_CONSTRUCTION_OUTPUT');
assert.equal(rename.body.commands[0].constructionOutput.stepId, groupStepId);
assert.equal(rename.body.commands[0].constructionOutput.columnId, rowValueOutputId);
assert.equal(rename.body.commands[0].constructionOutput.label, newLabel);
builder = await api(`${base}/builder`);
const renamedColumn = doc().columns.find((column) => column.column === codedColumn.column);
assert.equal(renamedColumn?.label, codedColumn.label, 'Renaming the visible grouped output must preserve the authored source label');
assert.equal(renamedColumn?.columnId, codedColumn.columnId, 'Rename must preserve the coded source identity');
assert.equal(renamedColumn?.frameId, codedColumn.frameId, 'Rename must preserve the coded source frame binding');
assert.equal(renamedColumn?.frameId, frame.id, 'Rename must retain the original framing source identity');
const renamedGroup = doc().construction.steps.find((step) => step.operation.kind === 'GROUP');
const renamedRowValue = renamedGroup?.rowValues.find((value) => value.inputColumnId === codedColumn.columnId);
assert.equal(renamedRowValue?.outputColumnId, rowValueOutputId, 'Rename must preserve the distinct derived row-value output identity');
assert.equal(renamedRowValue?.policy, 'ALL');
const renamedGroupOutput = renamedGroup?.outputs.find((output) => output.id === rowValueOutputId);
assert.equal(renamedGroupOutput?.name, codedColumn.column);
assert.equal(renamedGroupOutput?.label, newLabel);
await nativeClick(page, 'button', { name: 'Columns' });
record('edit-coded-column-label', editStart, { priorLabel: codedColumn.label, newLabel });
const afterEdit = await openTable(3, 'reload-edited-coded-column-label', source.distinctValues);
assert.deepEqual(afterEdit.preview.rows[0]?.[codedColumn.column], source.distinctValues, 'Renaming the grouped output must preserve its values');
assert(afterEdit.dom.headers.some((header) => header.toLowerCase() === newLabel.toLowerCase()), JSON.stringify(afterEdit.dom.headers));

const visibilitySelector = `input[type="checkbox"][aria-label=${JSON.stringify(newLabel)}]`;
for (const visible of [false, true]) {
  await nativeClick(page, 'button', { name: 'Columns' });
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (visibilitySelector) }, 5000);
  assert.equal(await cda.inspect(async args => { return document.querySelector(args.__template0).checked; }, { __template0: (visibilitySelector) }), !visible, 'Visibility checkbox must reflect the saved output');
  const visibilityStarted = Date.now();
  const visibilityFrom = nativeRequests.length;
  await nativeClick(page, visibilitySelector, {});
  const visibilityCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'UPDATE_CONSTRUCTION_OUTPUT' && item.constructionOutput?.columnId === rowValueOutputId), visibilityFrom);
  assert.equal(visibilityCommand.status, 200);
  assert.equal(visibilityCommand.body.commands[0].constructionOutput.table.visible, visible);
  builder = await api(`${base}/builder`);
  const currentGroup = doc().construction.steps.find((step) => step.id === groupStepId);
  assert.equal(currentGroup.outputs.find((output) => output.id === rowValueOutputId).table.visible, visible);
  assert.deepEqual(doc().columns.find((column) => column.columnId === codedColumn.columnId), renamedColumn, 'Output visibility must preserve the authored source');
  await checkUiRender(visible ? 3 : 2, visible ? source.distinctValues : [source.patient.reference]);
  await nativeClick(page, 'button', { name: 'Columns' });
  record(visible ? 'show-renamed-grouped-output' : 'hide-renamed-grouped-output', visibilityStarted);
  const visibilityReload = await openTable(visible ? 3 : 2, visible ? 'reload-visible-grouped-output' : 'reload-hidden-grouped-output', visible ? source.distinctValues : [source.patient.reference]);
  assert.deepEqual(visibilityReload.preview.rows[0]?.[codedColumn.column], source.distinctValues, 'Hiding presentation must retain exact values');
  assert.equal(visibilityReload.dom.headers.some((header) => header.toLowerCase() === newLabel.toLowerCase()), visible);
}

await openAddColumns();
const framePanel = '[data-testid="frame-source-panel"]';
const removeStart = Date.now();
const removeFrom = nativeRequests.length;
// The coded-source panel is authored-source management, so it labels this
// removal action from Document.Columns even though PreviewTable displays
// the renamed terminal Group output.
const sourceRemoveLabel = `Remove ${codedColumn.label} column`;
await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (`${framePanel} button[aria-label=${JSON.stringify(sourceRemoveLabel)}]`) }, 5000);
await nativeClick(page, `${framePanel} button`, { name: sourceRemoveLabel });
const removeColumn = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'REMOVE_COLUMN' && item.column === codedColumn.column), removeFrom);
assert.equal(removeColumn.status, 200, JSON.stringify(removeColumn.response));
builder = await api(`${base}/builder`);
assert(!doc().columns.some((column) => column.column === codedColumn.column), 'Removed physical column name remains in the authored schema');
assert(!doc().columns.some((column) => column.columnId === codedColumn.columnId), 'Removed coded source identity remains in the authored schema');
const groupAfterColumnRemoval = doc().construction.steps.find((step) => step.operation.kind === 'GROUP');
assert(!groupAfterColumnRemoval?.rowValues?.some((value) => value.inputColumnId === codedColumn.columnId), 'Removing the coded source must remove its Group row-value binding');
assert(!groupAfterColumnRemoval?.rowValues?.some((value) => value.outputColumnId === rowValueOutputId), 'Removing the coded source must remove its distinct Group row-value output identity');
assert(!groupAfterColumnRemoval?.outputs.some((output) => output.id === rowValueOutputId), 'Removed row-value output must no longer appear in the Group stage schema');
await checkUiRender(2, [source.patient.reference, String(source.contributorCount)]);
record('remove-coded-column-restores-group-shape', removeStart, { columnId: codedColumn.column });
const afterRemoval = await openTable(2, 'reload-after-coded-column-removal', [source.patient.reference, String(source.contributorCount)], groupedExpected);
assert.equal(afterRemoval.preview.rowCount, 1);
assert.deepEqual(doc().construction, groupedWorkspace.documents.find((document) => document.output.id === outputId).construction, 'Removing the coded output must restore the exact authored Group construction');
await openAddColumns();
const removeFrameStart = Date.now();
const removeFrameFrom = nativeRequests.length;
await nativeClick(page, `${framePanel} button`, { name: 'Remove' });
const removeFrame = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'REMOVE_FRAME_SOURCE'), removeFrameFrom);
assert.equal(removeFrame.status, 200, JSON.stringify(removeFrame.response));
builder = await api(`${base}/builder`);
assert.equal(doc().frames?.length ?? 0, 0);
assert.deepEqual(doc().population, sourceDocument.population);
await openTable(2, 'reload-after-coded-frame-removal', [source.patient.reference, String(source.contributorCount)], groupedExpected);
assert.deepEqual(doc().construction, groupedWorkspace.documents.find((document) => document.output.id === outputId).construction);
record('remove-empty-component-frame', removeFrameStart, { frameId: frame.id });

assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
await requestCapture.flush();
const expectedFailures = report.errors.filter((error) => error.kind === 'http' && error.path.endsWith('/construction-choice-proposals') && error.status === 422 && error.response?.error?.code === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
const expectedHttpDiagnostics = cda.diagnostics.httpFailures.filter(error => error.status === 422 && error.url.endsWith(`${base}/construction-choice-proposals`));
assert.equal(expectedHttpDiagnostics.length, 1, 'The expected ONE-policy proposal rejection must be captured by Playwright HTTP diagnostics');
const expectedCancellationLedger = new Set(cda.report.expectedCancellations ?? []);
const expectedHttpFailureLedger = new Set(cda.report.expectedHttpFailures ?? []);
const expectedCancellationErrors = new Set(report.errors.filter(error =>
  error.kind === 'network' && error.expected === true && error.error === 'net::ERR_ABORTED' &&
  error.expectedCancellation && expectedCancellationLedger.has(error.expectedCancellation) &&
  error.requestId === error.expectedCancellation.requestId &&
  error.browserRequestId === error.expectedCancellation.browserRequestId &&
  error.method === error.expectedCancellation.method && error.url === error.expectedCancellation.url &&
  error.path === `${base}/frame-source-options`
));
const expectedHttpConsoleErrors = new Set(report.errors.filter(error => {
  const failure = error.expectedHttpFailure;
  return error.kind === 'console' && error.expected === true && failure &&
    expectedHttpFailureLedger.has(failure) && failure.method === 'POST' &&
    failure.path === `${base}/construction-choice-proposals` && failure.status === 422 &&
    failure.console?.fixtureDiagnostic === true && failure.console?.requestCaptureError === true &&
    error.location === failure.console.location && error.message === failure.console.message;
}));
const unexpectedErrors = report.errors.filter((error) =>
  !expectedFailures.includes(error) &&
  !expectedCancellationErrors.has(error) &&
  !expectedHttpConsoleErrors.has(error));
assert.equal(expectedFailures.length, 1, 'Exactly one independently predicted ONE disagreement is expected');
assert.deepEqual(unexpectedErrors, [], 'No unexpected browser or HTTP errors are allowed');
assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected page errors were reported');
assert.deepEqual(cda.diagnostics.console, [], 'Unexpected console errors were reported');
assert.deepEqual(cda.diagnostics.networkFailures.filter(failure => !failure.expectedCancellation), [], 'Unexpected network failures were reported');
assert.deepEqual(cda.diagnostics.httpFailures.filter(error => !expectedHttpDiagnostics.includes(error)), [], 'Unexpected HTTP failures were reported');
report.expectedOneError = { code: oneError, requestId: oneFailure.requestId, independentDistinctValueCount: source.distinctValues.length };
report.status = 'passed';
  } finally {
    try { await requestCapture?.flush(); } catch (error) { report.requestFlushError = String(error); }
    cda.includeBrowserDiagnostics();
    report.finished = new Date().toISOString();
    await cda.attachReport(`standalone-${cda.caseName}-domain.json`, {
      ...cda.report,
      domain: report,
    });
  }
}
