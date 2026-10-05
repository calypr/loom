import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from './native-cda-workflow-tools.mjs';

export async function compoundFieldsWorkflow({ page, cda, caseOptions = {} }) {
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
const explorer = `compound-fields-browser-${Date.now()}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const originalExplorer = 'cda-builder-full-qa-1790440983382';
const directRelationship = 'specimen_Specimen';
const wantedCodes = ['primary_disease_type', 'specimen_type'];
const report = { explorer, project, generation, cases: [], errors: [], apiCalls: [], started: new Date().toISOString() };

let builder;
let outputId;
let rootColumnId;
const nativeRequests = [];
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

const doc = (state) => {
  const result = state.workspace.documents.find((candidate) => candidate.output.id === outputId);
  assert(result, `Builder has no document for ${outputId}`);
  return result;
};

const command = async (commands) => {
  const response = await api(`${base}/commands`, {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(`${base}/builder`);
  return response;
};

const rawQuery = (query) => {
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script,
  ], { encoding: 'utf8', timeout: 20000, maxBuffer: 2_000_000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `arangosh returned no JSON array: ${result.stdout.slice(0, 400)}`);
  return JSON.parse(result.stdout.slice(start));
};

const waitNative = async (predicate, fromIndex, timeoutMs = 5000) => {
  const match = await requestCapture.waitFor(predicate, { fromIndex, timeoutMs });
  assert.equal(match.status, 200, `${match.path} returned ${match.status}: ${JSON.stringify(match.response).slice(0, 1200)}`);
  return match;
};
const flushNetworkReads = async () => requestCapture?.flush();

const requestIndex = () => nativeRequests.length;

const nativePreview = async (fromIndex, expectedColumns) => {
  const entry = await waitNative((candidate) => candidate.path.endsWith('/preview') && candidate.body?.outputId === outputId, fromIndex);
  const preview = entry.response;
  assert.equal(preview.outputId, outputId);
  assert(preview.receiptId, 'Saved preview must use a fresh server receipt');
  assert(preview.rows, 'Saved preview must include rows');
  assert.equal(preview.rowCount, 1, 'The direct-edge witness has exactly one selected Observation row');
  assert.equal(preview.rows.length, 1);
  assert.equal(preview.columns.length, expectedColumns);
  return { entry, preview };
};

const assertPreviewValues = (preview, expectedByColumn) => {
  assert.equal(preview.rows.length, 1);
  for (const [columnId, expected] of Object.entries(expectedByColumn)) {
    assert(Object.hasOwn(preview.rows[0], columnId), `Saved preview omitted ${columnId}`);
    assert.deepEqual(preview.rows[0][columnId], expected, `CDA value mismatch in ${columnId}`);
  }
};

const assertWorkspaceTable = (state, expectedFrameCount, expectedCodedColumns) => {
  const document = doc(state);
  assert.equal(document.rootResourceType, 'Specimen');
  assert.equal(document.population?.selectionRevisionId, report.population.selectionRevisionId);
  assert.equal(document.population?.route?.length ?? 0, 0, 'Starting collection must stay on the selected Specimen records');
  assert.equal(document.frames?.length ?? 0, expectedFrameCount);
  assert.equal(document.columns.filter((column) => column.frameId).length, expectedCodedColumns);
  assert.equal(document.columns.filter((column) => column.column === rootColumnId).length, 1);
  return document;
};

const rendered = async (expectedColumns, timeoutMs = 5000) => {
  await waitForDOM(page, args => { const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'); return Boolean(table && table.getAttribute('aria-rowcount')==='2' && table.getAttribute('aria-colcount')===String(args.expectedColumns) && !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:')); }, { expectedColumns }, timeoutMs);
  const value = await inspectDOM(page, async args => { const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return {rowCount:table?.getAttribute('aria-rowcount'),columnCount:table?.getAttribute('aria-colcount'),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),cells:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim())}; });
  assert.equal(value.rowCount, '2');
  assert.equal(value.columnCount, String(expectedColumns));
  return value;
};

const record = (name, started, details = {}) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  report.cases.push({ name, durationMs, ...details });
};

const openTable = async (expectedColumns, name, expectedText = []) => {
  const started = Date.now();
  const fromIndex = requestIndex();
  await navigatePage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[data-testid="construction-table-'+args.__template0+'"]'))), { __template0: (outputId) }, 5000);
  await clickControl(page, `[data-testid="construction-table-${outputId}"]`);
  const visible = await rendered(expectedColumns);
  for (const value of expectedText) assert(visible.cells.some((cell) => cell.includes(value)), `Rendered table omitted CDA value ${value}`);
  const { preview } = await nativePreview(fromIndex, expectedColumns);
  record(name, started, { headers: visible.headers, receiptId: preview.receiptId });
  return preview;
};

const openAddColumns = async () => {
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-action-add-columns"]')?.disabled===false));
  await clickControl(page, '[data-testid="construction-action-add-columns"]');
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[data-testid="frame-source-panel"]'))));
};

const setSearchInput = async (selector, value) => {
  await fillControl(page, selector, value);
};

const openFrameValues = async (frame) => {
  const panel = `[data-testid=${JSON.stringify(`frame-categories-${frame.id}`)}]`;
  const visible = await inspectDOM(page, async args => { return Boolean(document.querySelector(args.__template0)); }, { __template0: (panel) });
  if (!visible) {
    await clickControl(page, `[data-testid=${JSON.stringify(`saved-frame-${frame.id}`)}] button`, { name: 'Choose values' });
  }
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (panel) });
};

const matchingDirectChoice = (source) => source.resourceType === 'Observation' &&
  source.sourcePath.toLowerCase().includes('component') && source.route.length === 1 &&
  source.route[0].fromResourceType === 'Specimen' && source.route[0].toResourceType === 'Observation' &&
  source.route[0].relationship === directRelationship && source.route[0].storageDirection === 'INBOUND' &&
  (source.valuePath.toLowerCase().includes('valuestring') || source.logicalType.toLowerCase() === 'string') &&
  source.forms.some((form) => form.form === 'ALL');

const chooseDirectFrame = async () => {
  const picker = '[data-testid="frame-source-panel"]';
  const toggle = await inspectDOM(page, async args => { return [...document.querySelectorAll(args.__template0)].find(button=>['Browse sources','Add coded source'].some(label=>button.innerText.trim().startsWith(label)))?.innerText.replace(/\\s+/g,' ').trim(); }, { __template0: (`${picker} button`) });
  if (toggle) await clickControl(page, `${picker} button`, { name: toggle });
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[aria-label="Search framing sources"]'))));
  const search = '[aria-label="Search framing sources"]';
  const searchStarted = Date.now();
  await setSearchInput(search, 'Observation');
  const fromIndex = requestIndex();
  await clickControl(page, `${picker} form button`, { name: 'Search' });
  let response = await waitNative((entry) => entry.path.endsWith('/frame-source-options') && entry.body?.query === 'Observation', fromIndex);
  let matches = response.response.sources.filter(matchingDirectChoice);
  let pageCount = 1;
  while (!matches.length && response.response.nextCursor && pageCount < 8) {
    const moreIndex = requestIndex();
    await clickControl(page, `${picker} button`, { name: 'More sources and paths' });
    response = await waitNative((entry) => entry.path.endsWith('/frame-source-options') && Boolean(entry.body?.cursor), moreIndex);
    matches = response.response.sources.filter(matchingDirectChoice);
    pageCount += 1;
  }
  assert(matches.length > 0, `No executable direct Specimen → Observation ${directRelationship} component source with ALL was returned by native browsing; no alternate route was tried. Searched ${pageCount} page(s).`);
  assert.equal(matches.length, 1, `Direct component source is ambiguous: ${JSON.stringify(matches.map((item) => ({ title: item.title, path: item.sourcePath, route: item.route })))}`);
  const source = matches[0];
  const sourceSelector = `[data-testid=${JSON.stringify(`frame-source-choice-${source.choiceId}`)}]`;
  let visibleChoice = await inspectDOM(page, async args => { return Boolean(document.querySelector(args.__template0)); }, { __template0: (sourceSelector) });
  if (!visibleChoice) {
    const moreFamilies = await inspectDOM(page, async args => { return [...document.querySelectorAll(args.__template0)].find(button=>/^Show \\d+ more families$/.test(button.innerText.trim()))?.innerText.trim(); }, { __template0: (`${picker} button`) });
    if (moreFamilies) await clickControl(page, `${picker} button`, { name: moreFamilies });
    visibleChoice = await inspectDOM(page, async args => { return Boolean(document.querySelector(args.__template0)); }, { __template0: (sourceSelector) });
  }
  assert(visibleChoice, `Native direct source choice ${source.choiceId} is not visible after source browse`);
  record('browse-direct-component-source', searchStarted, { pageCount });
  const routeSelect = `select[aria-label=${JSON.stringify(`Relationship path for ${source.title}`)}]`;
  const routeOptions = await inspectDOM(page, async args => { const select=document.querySelector(args.__template0);return select?[...select.options].map(option=>option.value):[]; }, { __template0: (routeSelect) });
  if (routeOptions.length) {
    assert(routeOptions.includes(source.choiceId), 'The exact direct signed route is not offered by the native route selector');
    await selectControl(page, routeSelect, source.choiceId);
  }
  const formSelect = `select[aria-label=${JSON.stringify(`Multiple values for ${source.title}`)}]`;
  const formOptions = await inspectDOM(page, async args => { const select=document.querySelector(args.__template0);return select?[...select.options].map(option=>option.value):[]; }, { __template0: (formSelect) });
  if (formOptions.length) {
    assert(formOptions.includes('ALL'), 'The native source selector does not offer ALL');
    const formSummary = `${picker} div.px-3.py-2:has(> ${sourceSelector}) details > summary`;
    await clickControl(page, formSummary, { name: 'When a row has several values' });
    await selectControl(page, formSelect, 'ALL');
  } else assert.equal(source.defaultForm, 'ALL', 'ALL was not selected and is not the source default');
  const saveIndex = requestIndex();
  const saveStarted = Date.now();
  await clickControl(page, sourceSelector);
  const save = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'SET_FRAME_SOURCE'), saveIndex);
  builder = await api(`${base}/builder`);
  const frameDoc = assertWorkspaceTable(builder, 1, 0);
  const frame = frameDoc.frames[0];
  assert.equal(frame.form, 'ALL');
  assert.equal(frame.source.resourceType, 'Observation');
  assert(frame.source.sourcePath.toLowerCase().includes('component'));
  assert(frame.source.valuePath.toLowerCase().includes('valuestring') || frame.source.logicalType.toLowerCase() === 'string', 'The native frame must yield string-valued component data');
  assert.deepEqual(frame.route.map((step) => ({ from: step.fromResourceType, to: step.toResourceType, relationship: step.relationship, storageDirection: step.storageDirection })), [
    { from: 'Specimen', to: 'Observation', relationship: directRelationship, storageDirection: 'INBOUND' },
  ]);
  report.frames ??= [];
  report.frames.push({ frameId: frame.id, form: frame.form, title: frame.title, sourcePath: frame.source.sourcePath, route: frame.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) => ({ fromResourceType, toResourceType, relationship, storageDirection })), choicePages: pageCount, saveStatus: save.status });
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (`[data-testid="frame-categories-${frame.id}"]`) });
  record('save-direct-component-frame', saveStarted, { frameId: frame.id });
  return frame;
};

const freshCategory = async (frame, code, fromIndex) => {
  const started = Date.now();
  const panel = `[data-testid=${JSON.stringify(`frame-categories-${frame.id}`)}]`;
  const search = `${panel} input[aria-label^="Search coded values in "]`;
  await setSearchInput(search, code);
  await clickControl(page, `${panel} form button`, { name: 'Search' });
  const entry = await waitNative((candidate) => candidate.path.endsWith('/semantic-inventory') && candidate.body?.frameId === frame.id && candidate.body?.query === code, fromIndex);
  assert.equal(entry.response.frameId, frame.id);
  assert.equal(entry.response.state, 'complete', `Inventory for ${code} is ${entry.response.state}`);
  assert.equal(entry.response.frameSource?.id, frame.id);
  const matches = entry.response.entries.filter((item) => item.code === code && item.resourceType === 'Observation');
  assert.equal(matches.length, 1, `Expected one fresh Observation component binding for ${code}; got ${matches.length}`);
  const item = matches[0];
  assert(item.constructionChoice, `Semantic inventory ${code} has no signed construction choice`);
  assert(item.constructionChoice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED'), `${code} does not support ALL`);
  assert(item.valueType.toLowerCase().includes('string'), `${code} does not identify string component values`);
  assert(item.readiness.status === 'READY' || item.readiness.status === 'READY_WITH_WARNING', `${code} is not ready: ${item.readiness.status}`);
  const label = item.display || item.code;
  const checkbox = `${panel} input[aria-label=${JSON.stringify(`Select ${label}`)}]`;
  await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (checkbox) });
  const enabled = await inspectDOM(page, async args => { return Boolean(document.querySelector(args.__template0)&&!document.querySelector(args.__template1).disabled); }, { __template0: (checkbox), __template1: (checkbox) });
  assert(enabled, `Fresh semantic result ${code} is not an enabled native category choice`);
  await clickControl(page, checkbox);
  record(`native-category-choice-${code}`, started, { frameId: frame.id });
  return { code, label, choiceId: item.constructionChoice.choiceId, frameId: frame.id, valueType: item.valueType, inventoryRequest: entry.requestId };
};


report.target ??= { ...cda.report.target };
report.nativeRequests = cda.report.nativeRequests;
report.errors = cda.report.errors;

  try {

const ownedTarget = target;
report.ownedTarget = ownedTarget;
assert.notEqual(explorer, originalExplorer);
const oracleQuery = `
FOR o IN Observation
FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
LET specimenKeys = (
  FOR e IN fhir_edge
    FILTER e._from == o._id AND e._to LIKE "Specimen/%" AND e.label == ${JSON.stringify(directRelationship)}
      AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
    RETURN DISTINCT e._to
)
FILTER LENGTH(specimenKeys) == 1
LET s = DOCUMENT(specimenKeys[0])
FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
LET linkedObservations = (
  FOR e IN fhir_edge
    FILTER e._to == s._id AND e._from LIKE "Observation/%" AND e.label == ${JSON.stringify(directRelationship)}
      AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
    RETURN DISTINCT e._from
)
FILTER LENGTH(linkedObservations) == 1 AND linkedObservations[0] == o._id
LET diseaseValues = (
  FOR component IN (IS_ARRAY(o.payload.component) ? o.payload.component : [])
    FOR coding IN (IS_ARRAY(component.code.coding) ? component.code.coding : [])
      FILTER coding.system == "https://cda.readthedocs.io" AND coding.code == "primary_disease_type" AND IS_STRING(component.valueString)
      RETURN component.valueString
)
LET specimenValues = (
  FOR component IN (IS_ARRAY(o.payload.component) ? o.payload.component : [])
    FOR coding IN (IS_ARRAY(component.code.coding) ? component.code.coding : [])
      FILTER coding.system == "https://cda.readthedocs.io" AND coding.code == "specimen_type" AND IS_STRING(component.valueString)
      RETURN component.valueString
)
FILTER LENGTH(diseaseValues) == 1 AND LENGTH(specimenValues) == 1
FILTER diseaseValues[0] != null AND specimenValues[0] != null
SORT o.id
LIMIT 1
RETURN { specimen: { id: s.id, key: s._key, _id: s._id }, observation: { id: o.id, key: o._key, _id: o._id }, diseaseValues, specimenValues }
`;
const [source] = rawQuery(oracleQuery);
assert(source, 'No current-generation CDA specimen has exactly one direct linked Observation with both requested component codes; no multihop substitute was used.');
report.oracle = {
  kind: 'raw-Arango direct specimen_Specimen witness',
  query: 'Observation payload.component values joined by one direct fhir_edge specimen_Specimen edge to a single selected Specimen',
  specimen: { id: source.specimen.id, key: source.specimen.key },
  observation: { id: source.observation.id, key: source.observation.key },
  categories: { primary_disease_type: source.diseaseValues, specimen_type: source.specimenValues },
};

await api(root, { name: explorer, title: 'CDA compound coded fields QA' });
builder = await api(`${base}/builder`);
assert.equal(builder.catalog.generation, generation);
const rootNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen');
assert(rootNode, 'Current catalog has no Specimen root node');
await command([{ type: 'CREATE_TABLE', title: 'Direct specimen compound QA', rootNodeId: rootNode.nodeId }]);
outputId = builder.workspace.documents[0].output.id;
const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
assert(idCandidate, 'Current catalog has no Specimen.id candidate');
await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
const selection = await api(base.replace('/authoring/v2', '/selections'), {
  snapshotToken: builder.catalog.snapshotToken,
  idempotencyKey: explorer,
  source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: 'Specimen', id: source.specimen.id }] } },
});
report.population = { selectionRevisionId: selection.id, resourceType: selection.resourceType, memberCount: selection.memberCount };
assert.equal(selection.memberCount, 1, 'Fixture population must contain exactly one Specimen');
const routes = await api(`${base}/population-routes`, { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
const directPopulation = routes.choices.find((choice) => choice.route.length === 0);
assert(directPopulation, 'The explicit Specimen selection has no direct starting collection route');
await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directPopulation.routeChoiceId }]);
const initialWorkspace = structuredClone(builder.workspace);

page.on('request', request => {
  const url = new URL(request.url());
  if (url.pathname.includes(originalExplorer)) report.errors.push({ kind: 'protected-explorer-request', path: url.pathname });
});
requestCapture = cda.captureRequests(`${root}/${explorer}`, { responsePaths: /frame-source-options|semantic-inventory|construction-choice-proposals|commands|preview/, });

const startInitial = Date.now();
const basePreviewIndex = requestIndex();
await navigatePage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
await waitForDOM(page, args => Boolean(Boolean(document.querySelector('[data-testid="construction-table-'+args.__template0+'"]'))), { __template0: (outputId) }, 5000);
await nativeClick(page, `[data-testid="construction-table-${outputId}"]`, {});
const initialVisible = await rendered(1);
assert(initialVisible.cells.some((cell) => cell.includes(source.specimen.id)), 'Rendered starting collection omitted the raw Specimen ID');
const initialPreview = await nativePreview(basePreviewIndex, 1);
rootColumnId = doc(builder).columns[0].column;
assertPreviewValues(initialPreview.preview, { [rootColumnId]: source.specimen.id });
record('initial-explicit-Specimen-table', startInitial, { headers: (await rendered(1)).headers, receiptId: initialPreview.preview.receiptId });

await openAddColumns();
const frame = await chooseDirectFrame();
const framedBaseline = await api(`${base}/builder`);
assertWorkspaceTable(framedBaseline, 1, 0);
const frameOnlyWorkspace = structuredClone(framedBaseline.workspace);
const categoriesPanel = `[data-testid=${JSON.stringify(`frame-categories-${frame.id}`)}]`;
await waitForDOM(page, args => Boolean(Boolean(document.querySelector(args.__template0))), { __template0: (categoriesPanel) }, 5000);

const cancelNativeStart = requestIndex();
const disease = await freshCategory(frame, 'primary_disease_type', requestIndex());
const specimenType = await freshCategory(frame, 'specimen_type', requestIndex());
const cancelStarted = Date.now();
await nativeClick(page, '[data-testid="construction-close-operation-editor"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-operation-editor"]')), {}, 5000);
builder = await api(`${base}/builder`);
assert.deepEqual(builder.workspace, frameOnlyWorkspace, 'Canceling the coded-value chooser must preserve the saved frame and original table');
assert.equal(nativeRequests.slice(cancelNativeStart).filter((entry) => entry.path.endsWith('/commands')).length, 0, 'Cancel must not send a mutation command');
record('cancel-two-category-selection-keeps-framed-baseline', cancelStarted, { frameId: frame.id, selectedCodes: [disease.code, specimenType.code] });

await openAddColumns();
await openFrameValues(frame);
const selected = [];
for (const code of wantedCodes) selected.push(await freshCategory(frame, code, requestIndex()));
assert.deepEqual(selected.map((choice) => choice.code), wantedCodes);
// Leave the fresh selection in the native category picker; the atomic Add click below owns the only save.
const countButton = await cda.inspect(async args => { return [...document.querySelectorAll(args.__template0)].find(button=>button.innerText.trim()==='Add 2 columns')?.innerText.trim(); }, { __template0: (`${categoriesPanel} button`) });
assert.equal(countButton, 'Add 2 columns');
// Reuse the already checked selections in the direct two-column Add flow.
const addStart = Date.now();
const proposalStart = requestIndex();
await nativeClick(page, `${categoriesPanel} button`, { name: 'Add 2 columns' });
const proposal = await waitNative((entry) => entry.path.endsWith('/construction-choice-proposals') && entry.body?.constructionChoices?.length === 2, proposalStart);
assert.equal(proposal.response.previewStatus, 'READY');
assert(proposal.response.previewDurationMs <= 5000, `Fresh preflight receipt took ${proposal.response.previewDurationMs} ms`);
assert.equal(proposal.response.outputId, outputId);
assert.deepEqual(proposal.response.constructionChoices.map((choice) => ({ choiceId: choice.choiceId, form: choice.form, frameId: choice.frameId })), selected.map((choice) => ({ choiceId: choice.choiceId, form: 'ALL', frameId: frame.id })));
assert.equal(proposal.response.candidateColumnIds.length, 2);
const rawValuesByCode = { primary_disease_type: source.diseaseValues, specimen_type: source.specimenValues };
const candidateValues = Object.fromEntries(proposal.response.candidateColumnIds.map((columnId, index) => [columnId, rawValuesByCode[selected[index].code]]));
assertPreviewValues(proposal.response.preview, candidateValues);
assert.equal(proposal.response.preview.columns.length, 3);
const commandRequest = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE'), proposalStart);
assert.equal(commandRequest.body.commands.length, 2, 'Both coded fields must be applied in one atomic command batch');
assert(commandRequest.body.commands.every((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE' && item.outputId === outputId));
assert.equal(commandRequest.body.commandId, proposal.response.commandId, 'The save must use the fresh preflight command id');
assert.deepEqual(commandRequest.body.commands.map((item) => ({ choiceId: item.constructionChoice?.choiceId, form: item.constructionChoice?.form, frameId: item.constructionChoice?.frameId })), selected.map((choice) => ({ choiceId: choice.choiceId, form: 'ALL', frameId: frame.id })));
await rendered(3);
const savedPreview = await nativePreview(proposalStart, 3);
builder = await api(`${base}/builder`);
const savedDocument = assertWorkspaceTable(builder, 1, 2);
assert.deepEqual(new Set(savedDocument.columns.filter((column) => column.frameId === frame.id).map((column) => column.column)), new Set(proposal.response.candidateColumnIds));
assertPreviewValues(savedPreview.preview, { ...candidateValues, [rootColumnId]: source.specimen.id });
const visibleAfterAdd = await rendered(3);
for (const value of [...source.diseaseValues, ...source.specimenValues]) {
  assert(visibleAfterAdd.cells.some((cell) => cell.includes(value)), `Rendered coded-value table omitted CDA value ${value}`);
}
record('native-add-two-compound-ALL-columns', addStart, {
  previewDurationMs: proposal.response.previewDurationMs,
  choiceCodes: selected.map((choice) => choice.code),
  candidateColumnIds: proposal.response.candidateColumnIds,
  freshSavedReceiptId: savedPreview.preview.receiptId,
  atomicCommandCount: commandRequest.body.commands.length,
});

const reloadPreview = await openTable(3, 'reload-compound-ALL-columns', [...source.diseaseValues, ...source.specimenValues]);
assertPreviewValues(reloadPreview, { ...candidateValues, [rootColumnId]: source.specimen.id });
await openAddColumns();
const framePanel = '[data-testid="frame-source-panel"]';
let remainingCodedColumns = 2;
for (const column of savedDocument.columns.filter((candidate) => candidate.frameId === frame.id)) {
  const removeStart = Date.now();
  const removeIndex = requestIndex();
  await nativeClick(page, `${framePanel} button`, { name: `Remove ${column.label} column` });
  await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'REMOVE_COLUMN' && item.column === column.column), removeIndex);
  builder = await api(`${base}/builder`);
  remainingCodedColumns -= 1;
  assertWorkspaceTable(builder, 1, remainingCodedColumns);
  await rendered(remainingCodedColumns + 1);
  record(`remove-coded-column-${column.label}`, removeStart, { columnId: column.column });
}
const removeFrameStart = Date.now();
const removeFrameIndex = requestIndex();
await nativeClick(page, `${framePanel} button`, { name: 'Remove' });
await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'REMOVE_FRAME_SOURCE'), removeFrameIndex);
builder = await api(`${base}/builder`);
assertWorkspaceTable(builder, 0, 0);
const expectedRemovedWorkspace = structuredClone(initialWorkspace);
expectedRemovedWorkspace.documents.find((document) => document.output.id === outputId).route = savedDocument.route;
assert.deepEqual(builder.workspace, expectedRemovedWorkspace, 'Removing the frame must restore the source columns and population while preserving the separately authored relationship');
report.retainedRelationship = { route: savedDocument.route, reason: 'Frame and column removal preserve authored routes; REMOVE_ROUTE owns relationship removal.' };
await rendered(1);
const removalPreview = await nativePreview(removeFrameIndex, 1);
assertPreviewValues(removalPreview.preview, { [rootColumnId]: source.specimen.id });
record('remove-empty-frame', removeFrameStart, { restoredWorkspaceDigest: builder.draftDigest });
const removedPreview = await openTable(1, 'reload-after-removing-frame-and-columns', [source.specimen.id]);
assertPreviewValues(removedPreview, { [rootColumnId]: source.specimen.id });

await openAddColumns();
const restoredFrame = await chooseDirectFrame();
const restoredPanel = `[data-testid=${JSON.stringify(`frame-categories-${restoredFrame.id}`)}]`;
const restoredSelected = [];
for (const code of wantedCodes) restoredSelected.push(await freshCategory(restoredFrame, code, requestIndex()));
const restoreAddStart = Date.now();
const restoreProposalStart = requestIndex();
await nativeClick(page, `${restoredPanel} button`, { name: 'Add 2 columns' });
const restoreProposal = await waitNative((entry) => entry.path.endsWith('/construction-choice-proposals') && entry.body?.constructionChoices?.length === 2, restoreProposalStart);
assert.equal(restoreProposal.response.previewStatus, 'READY');
assert(restoreProposal.response.previewDurationMs <= 5000, `Restored fresh preflight receipt took ${restoreProposal.response.previewDurationMs} ms`);
const restoreValues = Object.fromEntries(restoreProposal.response.candidateColumnIds.map((columnId, index) => [columnId, rawValuesByCode[restoredSelected[index].code]]));
assertPreviewValues(restoreProposal.response.preview, restoreValues);
const restoreCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE'), restoreProposalStart);
assert.equal(restoreCommand.body.commands.length, 2);
assert.equal(restoreCommand.body.commandId, restoreProposal.response.commandId);
const restoredVisible = await rendered(3);
for (const value of [...source.diseaseValues, ...source.specimenValues]) {
  assert(restoredVisible.cells.some((cell) => cell.includes(value)), `Rendered restored table omitted CDA value ${value}`);
}
const restoredSaved = await nativePreview(restoreProposalStart, 3);
builder = await api(`${base}/builder`);
const restoredDocument = assertWorkspaceTable(builder, 1, 2);
assertPreviewValues(restoredSaved.preview, { ...restoreValues, [rootColumnId]: source.specimen.id });
record('restore-two-compound-ALL-columns', restoreAddStart, { previewDurationMs: restoreProposal.response.previewDurationMs, atomicCommandCount: restoreCommand.body.commands.length });
const restoredReload = await openTable(3, 'reload-restored-compound-ALL-columns', [...source.diseaseValues, ...source.specimenValues]);
assertPreviewValues(restoredReload, { ...restoreValues, [rootColumnId]: source.specimen.id });
await flushNetworkReads();
assert.deepEqual(report.errors, []);
assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected page errors were reported');
assert.deepEqual(cda.diagnostics.console, [], 'Unexpected console errors were reported');
assert.deepEqual(cda.diagnostics.networkFailures, [], 'Unexpected network failures were reported');
assert.deepEqual(cda.diagnostics.httpFailures, [], 'Unexpected HTTP failures were reported');
assert(nativeRequests.every((entry) => !entry.path.includes(originalExplorer)));
report.final = {
  status: 'passed',
  tableOutputId: outputId,
  root: 'Specimen',
  directRelationship,
  restoredFrameId: restoredDocument.frames[0].id,
  restoredCodeColumns: restoredDocument.columns.filter((column) => column.frameId === restoredDocument.frames[0].id).map((column) => column.label),
  rawCdaValues: { primary_disease_type: source.diseaseValues, specimen_type: source.specimenValues },
};
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
