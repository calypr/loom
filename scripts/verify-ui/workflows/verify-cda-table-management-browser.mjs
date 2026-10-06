import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';

export async function tableManagementWorkflow({ page, cda }) {
let fatal;
const project = cda.project;
const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
const explorerId = `table-management-browser-${Date.now()}`;
const explorerRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const authoringPath = `${explorerRoot}/${encodeURIComponent(explorerId)}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const evidenceDirectory = cda.evidenceDirectory;
const report = Object.assign(cda.report, {
  explorerId,
  pageURL,
  protectedOriginalMutations: 0,
  setup: [],
  actions: [],
  limitations: [],
  dialogs: [],
  protocol: [],
  errors: [],
  nativeRequests: cda.nativeRequests,
});
const dialogQueue = [];
const browserEval = (_page, callback, args = []) => cda.inspect(callback, args);
const click = (_page, selector, identity, timeout) => cda.click(selector, identity, timeout);
const fill = (_page, selector, value, identity, timeout) => cda.fill(selector, value, identity, timeout);
const navigate = (_page, url) => cda.navigate(url);
const waitForBrowser = (_page, predicate, argsOrTimeout = [], timeout) => Array.isArray(argsOrTimeout)
  ? cda.wait(predicate, argsOrTimeout, timeout ?? 5000)
  : cda.wait(predicate, [], argsOrTimeout);
const captureRequests = (_page, _report, ownedPathPrefix, options = {}) => cda.captureRequests(ownedPathPrefix, options);
const waitForCapturedResponse = (_page, tracker, predicate, timeout) => cda.waitForCapturedResponse(tracker, predicate, timeout);
const includeBrowserDiagnostics = () => cda.includeBrowserDiagnostics();
let browserEvents;
let builder;
let baselineOutputId;
let visiblePreviewIds = new Set();
let datasetGeneration;

const api = async (path, body) => {
  const requestId = `table-management-${randomUUID()}`;
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : undefined; }
  catch { value = text; }
  report.setup.push({ path, method: body === undefined ? 'GET' : 'POST', status: response.status });
  assert(response.ok, `${response.status} ${path}: ${JSON.stringify(value)}`);
  return value;
};

const identity = (value) => ({
  snapshotToken: value.catalog.snapshotToken,
  expectedDraftVersion: value.draftVersion,
  expectedDraftDigest: value.draftDigest,
});

const apply = async (commands) => {
  const result = await api(`${authoringPath}/commands`, {
    ...identity(builder),
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    commands,
  });
  builder = await api(`${authoringPath}/builder`);
  return result;
};

const readBuilder = async () => api(`${authoringPath}/builder`);

const orderedTables = (value) => {
  const documents = new Map(value.workspace.documents.map((document) => [document.output.id, document]));
  return [...value.workspace.tabs]
    .sort((left, right) => left.order - right.order)
    .map((tab) => {
      const document = documents.get(tab.outputId);
      assert(document, `Workspace tab ${tab.outputId} has no document`);
      return { outputId: tab.outputId, title: document.output.title, tabId: tab.id };
    });
};

const nativeTables = async () => browserEval(page, () => { return [...document.querySelectorAll('button[data-testid^="construction-table-"]')].map(button=>({outputId:button.dataset.testid.slice('construction-table-'.length),title:button.querySelector('span:last-child')?.textContent?.trim()??button.innerText.trim(),selected:button.getAttribute('aria-pressed')==='true'})); });

const currentPreview = async () => browserEval(page, () => { const panel=document.querySelector('[data-testid="construction-preview"]');const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');const headers=table?[...table.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()):[];const rows=table?[...table.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())):[];const idIndex=headers.findIndex(header=>header.toUpperCase()==='SPECIMEN ID');return {status:panel?.dataset.previewStatus,outputId:panel?.dataset.previewOutputId,currentDraftVersion:panel?.dataset.currentDraftVersion,currentDraftDigest:panel?.dataset.currentDraftDigest,receiptId:panel?.dataset.previewReceiptId,headers,rows,specimenIds:idIndex<0?[]:rows.map(row=>row[idIndex]).filter(Boolean)}; });

const rawCdaOracle = (aql) => {
  const raw = spawnSync('rtk', [
    'proxy', 'docker', 'exec', cda.target.arangoContainer,
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(aql)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const start = raw.stdout.indexOf('[');
  assert(start >= 0, 'Raw CDA oracle returned no JSON array');
  return JSON.parse(raw.stdout.slice(start));
};

const validateVisiblePreviewIds = () => {
  const ids = [...visiblePreviewIds];
  assert(ids.length > 0, 'Native previews exposed no Specimen IDs to validate');
  const witnessed = [];
  const queries = [];
  for (let index = 0; index < ids.length; index += 25) {
    const batch = ids.slice(index, index + 25);
    const aql = `FOR s IN Specimen FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(datasetGeneration)} FILTER s.id IN ${JSON.stringify(batch)} LIMIT 25 RETURN {id:s.id,key:s._key}`;
    const rows = rawCdaOracle(aql);
    assert(rows.length <= 25, 'Raw scoped Specimen witness exceeded 25 rows');
    assert.equal(rows.length, batch.length, 'Raw scoped Specimen witness has missing or duplicate IDs');
    assert(rows.every((row) => typeof row.id === 'string' && typeof row.key === 'string'));
    assert.equal(new Set(rows.map((row) => row.id)).size, rows.length, 'Raw scoped Specimen witness contains duplicate IDs');
    witnessed.push(...rows.map((row) => row.id));
    queries.push({ query: aql, rows });
  }
  assert.deepEqual([...new Set(witnessed)].sort(), [...ids].sort(), 'A visible Specimen ID is absent from the raw scoped CDA oracle');
  report.oracle.witnessQueries = queries;
};

const readNativeState = async (expectedIds, expectedTitles, { selectedOutputId, maxMs = 5000 } = {}) => {
  const started = Date.now();
  const expectedDOM = JSON.stringify(expectedIds.map((outputId, index) => ({ outputId, title: expectedTitles[index] })));
  await waitForBrowser(page, ([__arg0]) => Boolean((()=>{const buttons=[...document.querySelectorAll('button[data-testid^="construction-table-"]')];const state=buttons.map(button=>({outputId:button.dataset.testid.slice('construction-table-'.length),title:button.querySelector('span:last-child')?.textContent?.trim()??button.innerText.trim()}));return JSON.stringify(state)===__arg0;})()), [expectedDOM], maxMs);
  const saved = await readBuilder();
  const savedTables = orderedTables(saved);
  assert.deepEqual(savedTables.map((table) => table.outputId), expectedIds, 'Saved output order differs');
  assert.deepEqual(savedTables.map((table) => table.title), expectedTitles, 'Saved table titles differ');

  await waitForBrowser(page, ([__arg0, __arg1]) => Boolean((()=>{const p=document.querySelector('[data-testid="construction-preview"]');return Boolean(p&&p.dataset.previewStatus==='ready'&&p.dataset.previewReceiptId&&p.dataset.previewOutputId&&Number(p.dataset.currentDraftVersion)===__arg0&&p.dataset.currentDraftDigest===__arg1);})()), [saved.draftVersion, saved.draftDigest], maxMs);
  const [tables, preview] = await Promise.all([nativeTables(), currentPreview()]);
  assert.deepEqual(tables.map((table) => table.outputId), expectedIds, 'Native table order differs from saved order');
  assert.deepEqual(tables.map((table) => table.title), expectedTitles, 'Native table titles differ from saved titles');
  const selected = tables.find((table) => table.selected);
  assert(selected, 'The native workspace has no selected table');
  assert(savedTables.some((table) => table.outputId === selected.outputId), 'Selected table is absent from saved workspace');
  if (selectedOutputId) assert.equal(selected.outputId, selectedOutputId);
  assert.equal(preview.status, 'ready');
  assert.equal(preview.outputId, selected.outputId, 'Preview is bound to a different table');
  assert.equal(Number(preview.currentDraftVersion), saved.draftVersion, 'Preview draft version is stale');
  assert.equal(preview.currentDraftDigest, saved.draftDigest, 'Preview draft digest is stale');
  assert(preview.receiptId, 'Current preview has no receipt');
  assert(preview.headers.includes('SPECIMEN ID'), 'Preview is missing its authored Specimen ID column');
  assert(preview.specimenIds.length > 0, "Current Specimen preview rendered no source values");
  assert(preview.specimenIds.length <= 25, 'Native preview exposed more than 25 Specimen IDs');
  for (const id of preview.specimenIds) visiblePreviewIds.add(id);
  const elapsedMs = Date.now() - started;
  assert(elapsedMs <= maxMs, `Saved state and fresh preview exceeded ${maxMs} ms (${elapsedMs} ms)`);
  return { savedTables, selectedOutputId: selected.outputId, preview, elapsedMs };
};

const selectTable = async (outputId, expectedIds, expectedTitles) => {
  const started = Date.now();
  const selector = `button[data-testid=${JSON.stringify(`construction-table-${outputId}`)}]`;
  const control = await click(page, selector, {}, 1500);
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)?.getAttribute('aria-pressed')==='true'), [selector], 5000);
  const state = await readNativeState(expectedIds, expectedTitles, { selectedOutputId: outputId, maxMs: 5000 });
  const elapsedMs = Date.now() - started;
  assert(elapsedMs <= 5000, `Selecting ${outputId} took ${elapsedMs} ms to render`);
  report.actions.push({ label: `Select ${outputId}`, selector, control, elapsedMs, ...state });
  return control;
};

const setInputText = async (selector, value) => {
  await fill(page, selector, value, {}, 1500);
  assert.equal(await browserEval(page, ([__arg0]) => { return document.querySelector(__arg0)?.value; }, [selector]), value);
};

const queueDialog = (type, message, response) => dialogQueue.push({ type, message, response });

const dialogHandler = (dialog) => {
  const expected = dialogQueue.shift();
  assert(expected, `Unexpected JavaScript dialog: ${dialog.type} ${dialog.message}`);
  assert.equal(dialog.type, expected.type);
  assert.equal(dialog.message, expected.message);
  report.dialogs.push({ type: dialog.type, message: dialog.message, accepted: expected.response.accept, promptText: expected.response.promptText });
  return expected.response;
};
cda.onDialog(dialogHandler);

const normalizedDocument = (document) => ({
  ...document,
  output: { ...document.output, id: '<fresh-output-id>', title: '<renamed-output-title>' },
});

const reloadAndRead = async (expectedIds, expectedTitles) => {
  const started = Date.now();
  await navigate(page, pageURL);
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-workspace"]'))), [], 5000);
  const state = await readNativeState(expectedIds, expectedTitles, { maxMs: 5000 });
  const elapsedMs = Date.now() - started;
  assert(elapsedMs <= 5000, `Reload and saved state render took ${elapsedMs} ms`);
  return { ...state, reloadMs: elapsedMs };
};

const commandTraffic = () => report.protocol.filter((entry) => entry.path.endsWith('/commands'));

const captureBrowserProtocol = () => {
  browserEvents = captureRequests(page, report, authoringPath, { apiOrigin, uiOrigin, responsePaths: /commands|preview/ });
  report.protocol = report.nativeRequests;
};

const createExplorerAndBaseline = async () => {
  assert(!explorerId.includes('cda-builder-full-qa-1790440983382'));
  await api(explorerRoot, { name: explorerId, title: `Table management QA ${explorerId}` });
  builder = await readBuilder();
  const generation = builder.catalog.generation;
  datasetGeneration = generation;
  const aql = `FOR s IN Specimen FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)} SORT s._key LIMIT 25 RETURN {id:s.id,key:s._key}`;
  const oracle = rawCdaOracle(aql);
  assert(oracle.length > 0 && oracle.length <= 25, 'Raw Specimen oracle must be bounded to 25 rows');
  assert(oracle.every((row) => typeof row.id === 'string' && row.id.length > 0));
  report.oracle = { query: aql, rows: oracle, witnessQueries: [] };

  const node = builder.catalog.nodes.find((candidate) => candidate.resourceType === 'Specimen');
  assert(node, 'The loaded CDA catalog has no Specimen rows');
  const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idCandidate, 'The loaded CDA catalog has no direct Specimen ID field');
  await apply([{ type: 'CREATE_TABLE', title: 'Specimen baseline', rootNodeId: node.nodeId }]);
  const baselineDocument = builder.workspace.documents.find((document) => document.output.title === 'Specimen baseline');
  assert(baselineDocument, 'API seed did not create the baseline table');
  baselineOutputId = baselineDocument.output.id;
  await apply([{
    type: 'ADD_COLUMN',
    outputId: baselineOutputId,
    occurrenceId: 'base',
    candidateId: idCandidate.candidateId,
    projectionMode: 'VALUE',
    initialPresentation: 'TABLE',
    title: 'Specimen ID',
  }]);
  const selection = await api(`${explorerRoot}/${encodeURIComponent(explorerId)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `table-management-${explorerId}`,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: 'Specimen', id: oracle[0].id }] } },
  });
  const routes = await api(`${authoringPath}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken,
    outputId: baselineOutputId,
    selectionRevisionId: selection.id,
    limit: 50,
  });
  const directRoute = routes.choices.find((choice) => choice.route.length === 0);
  assert(directRoute, 'The one-record starting selection has no direct population route');
  await apply([{
    type: 'SET_TABLE_POPULATION',
    outputId: baselineOutputId,
    selectionRevisionId: selection.id,
    routeChoiceId: directRoute.routeChoiceId,
  }]);
  report.baseline = { outputId: baselineOutputId, title: 'Specimen baseline', selectedSpecimenId: oracle[0].id };
};

const main = async () => {
  await createExplorerAndBaseline();
  const baseState = orderedTables(builder);
  assert.deepEqual(baseState.map((table) => table.outputId), [baselineOutputId]);

  captureBrowserProtocol();
  await navigate(page, pageURL);
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-workspace"]'))), [], 30000);
  const firstRender = await readNativeState([baselineOutputId], ['Specimen baseline'], { selectedOutputId: baselineOutputId, maxMs: 30000 });
  assert(firstRender.preview.specimenIds.includes(report.baseline.selectedSpecimenId), 'API-seeded starting selection did not render its exact raw CDA Specimen ID');
  report.initialNativeState = firstRender;

  const newTitle = `Specimen native ${Date.now()}`;
  const openStarted = Date.now();
  const createControl = await click(page, '[data-testid="construction-new-table"]', {}, 1500);
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('#first-table-name'))), [], 3000);
  const openState = await readNativeState([baselineOutputId], ['Specimen baseline'], { selectedOutputId: baselineOutputId, maxMs: 5000 });
  const openMs = Date.now() - openStarted;
  assert(openMs <= 5000, `Opening New table took ${openMs} ms`);
  report.actions.push({ label: 'Open New table', control: createControl, elapsedMs: openMs, ...openState });
  await setInputText('#first-table-name', newTitle);
  const startCreate = Date.now();
  const rootControl = await click(page, 'button[aria-label="Choose Specimen rows"]', {}, 1500);
  await waitForBrowser(page, ([__arg0]) => Boolean([...document.querySelectorAll('button[data-testid^="construction-table-"]')].some(button=>button.innerText.includes(__arg0))), [newTitle], 5000);
  builder = await readBuilder();
  const createdDocument = builder.workspace.documents.find((document) => document.output.title === newTitle);
  assert(createdDocument, 'Native New table did not persist the custom Specimen title');
  const createdOutputId = createdDocument.output.id;
  let ids = [baselineOutputId, createdOutputId];
  let titles = ['Specimen baseline', newTitle];
  let createState = await readNativeState(ids, titles, { selectedOutputId: createdOutputId, maxMs: 5000 });
  const createMs = Date.now() - startCreate;
  assert(createMs <= 5000, `Native table create took ${createMs} ms to save and render`);
  report.actions.push({ label: 'Create Specimen table', controls: [createControl, rootControl], elapsedMs: createMs, ...createState });
  const createdDoc = builder.workspace.documents.find((document) => document.output.id === createdOutputId);
  const createdIdColumn = createdDoc?.columns.find((column) => column.occurrenceId === 'base' &&
    column.source?.kind === 'field' &&
    column.source?.field?.path === 'id' &&
    column.source?.field?.projectionMode === 'VALUE');
  assert(createdIdColumn, 'Native table omitted its direct ID binding');

  const commandCountBeforeRename = commandTraffic().length;
  queueDialog('prompt', 'Table name', { accept: false });
  const renameCancelStarted = Date.now();
  const renameCancelControl = await click(page, `[data-testid="construction-rename-table-${createdOutputId}"]`, {}, 1500);
  const cancelRenameState = await readNativeState(ids, titles, { selectedOutputId: createdOutputId, maxMs: 5000 });
  builder = await readBuilder();
  assert.deepEqual(orderedTables(builder).map((table) => table.title), titles, 'Canceling Rename changed a saved title');
  assert.equal(commandTraffic().length, commandCountBeforeRename, 'Canceling Rename sent a table command');
  const renameCancelMs = Date.now() - renameCancelStarted;
  assert(renameCancelMs <= 5000, `Canceling Rename took ${renameCancelMs} ms to render`);
  report.actions.push({ label: 'Cancel Rename', control: renameCancelControl, elapsedMs: renameCancelMs, ...cancelRenameState });

  const renamedTitle = `Specimen renamed ${Date.now()}`;
  queueDialog('prompt', 'Table name', { accept: true, promptText: renamedTitle });
  const renameStarted = Date.now();
  const renameControl = await click(page, `[data-testid="construction-rename-table-${createdOutputId}"]`, {}, 1500);
  titles = ['Specimen baseline', renamedTitle];
  const renameState = await readNativeState(ids, titles, { selectedOutputId: createdOutputId });
  const renameMs = Date.now() - renameStarted;
  assert(renameMs <= 5000, `Rename took ${renameMs} ms to save and render`);
  assert(commandTraffic().slice(commandCountBeforeRename).some((entry) => entry.body?.commands?.some((command) => command.type === 'RENAME_TABLE')), 'Rename emitted no RENAME_TABLE command');
  report.actions.push({ label: 'Rename table', control: renameControl, elapsedMs: renameMs, ...renameState });

  const beforeDuplicate = await readBuilder();
  const sourceDocument = beforeDuplicate.workspace.documents.find((document) => document.output.id === createdOutputId);
  const sourceTab = beforeDuplicate.workspace.tabs.find((tab) => tab.outputId === createdOutputId);
  const duplicateTitle = `${renamedTitle} copy`;
  const duplicateStarted = Date.now();
  const duplicateControl = await click(page, '[data-testid="construction-duplicate-table"]', {}, 1500);
  await waitForBrowser(page, ([__arg0]) => Boolean([...document.querySelectorAll('button[data-testid^="construction-table-"]')].some(button=>button.innerText.includes(__arg0))), [duplicateTitle], 5000);
  builder = await readBuilder();
  const duplicateDocument = builder.workspace.documents.find((document) => document.output.title === duplicateTitle);
  const duplicateTab = builder.workspace.tabs.find((tab) => tab.outputId === duplicateDocument?.output.id);
  assert(duplicateDocument, 'Duplicate table was not saved');
  assert.notEqual(duplicateDocument.output.id, sourceDocument.output.id, 'Duplicate reused its source output ID');
  assert.notEqual(duplicateTab?.id, sourceTab?.id, 'Duplicate reused its source table/tab ID');
  assert.deepEqual(normalizedDocument(duplicateDocument), normalizedDocument(sourceDocument), 'Duplicate did not preserve the authored document and field bindings');
    const duplicateOutputId = duplicateDocument.output.id;
  ids = [baselineOutputId, createdOutputId, duplicateOutputId];
  titles = ['Specimen baseline', renamedTitle, duplicateTitle];
  const duplicateState = await readNativeState(ids, titles, { selectedOutputId: duplicateOutputId });
  const duplicateMs = Date.now() - duplicateStarted;
  assert(duplicateMs <= 5000, `Duplicate took ${duplicateMs} ms to save and render`);
  assert(commandTraffic().some((entry) => entry.body?.commands?.some((command) => command.type === 'DUPLICATE_TABLE' && command.sourceOutputId === createdOutputId)), 'Duplicate emitted no source-bound DUPLICATE_TABLE command');
  report.actions.push({ label: 'Duplicate table', control: duplicateControl, elapsedMs: duplicateMs, ...duplicateState, sourceOutputId: createdOutputId, duplicateOutputId });
  assert.deepEqual(builder.workspace.documents.find((document) => document.output.id === createdOutputId), sourceDocument, 'Creating a duplicate changed its source document');

  const independentTitle = `${duplicateTitle} independent ${Date.now()}`;
  const independentTitles = ['Specimen baseline', renamedTitle, independentTitle];
  const commandCountBeforeIndependentRename = commandTraffic().length;
  queueDialog('prompt', 'Table name', { accept: true, promptText: independentTitle });
  const independentRenameStarted = Date.now();
  const independentRenameControl = await click(page, `[data-testid="construction-rename-table-${duplicateOutputId}"]`, {}, 1500);
  const independentRenameState = await readNativeState(ids, independentTitles, { selectedOutputId: duplicateOutputId });
  const independentRenameMs = Date.now() - independentRenameStarted;
  assert(independentRenameMs <= 5000, `Renaming the duplicate took ${independentRenameMs} ms to save and render`);
  builder = await readBuilder();
  const sourceAfterIndependentRename = builder.workspace.documents.find((document) => document.output.id === createdOutputId);
  const independentlyRenamedDuplicate = builder.workspace.documents.find((document) => document.output.id === duplicateOutputId);
  assert.deepEqual(sourceAfterIndependentRename, sourceDocument, 'Renaming the duplicate changed its source document');
  assert(independentlyRenamedDuplicate, 'Renamed duplicate document was not saved');
  assert.deepEqual(normalizedDocument(independentlyRenamedDuplicate), normalizedDocument(sourceDocument), 'Renaming the duplicate changed its authored document or field bindings');
  assert(commandTraffic().slice(commandCountBeforeIndependentRename).some((entry) => entry.body?.commands?.some((command) => command.type === 'RENAME_TABLE' && command.outputId === duplicateOutputId)), 'Renaming the duplicate emitted no source-bound RENAME_TABLE command');
  report.actions.push({ label: 'Rename duplicate independently', control: independentRenameControl, elapsedMs: independentRenameMs, ...independentRenameState, sourceOutputId: createdOutputId, duplicateOutputId });

  queueDialog('prompt', 'Table name', { accept: true, promptText: duplicateTitle });
  const restoreDuplicateTitleStarted = Date.now();
  const restoreDuplicateTitleControl = await click(page, `[data-testid="construction-rename-table-${duplicateOutputId}"]`, {}, 1500);
  const restoreDuplicateTitleState = await readNativeState(ids, titles, { selectedOutputId: duplicateOutputId });
  const restoreDuplicateTitleMs = Date.now() - restoreDuplicateTitleStarted;
  assert(restoreDuplicateTitleMs <= 5000, `Restoring the duplicate title took ${restoreDuplicateTitleMs} ms to save and render`);
  builder = await readBuilder();
  assert.deepEqual(builder.workspace.documents.find((document) => document.output.id === createdOutputId), sourceDocument, 'Restoring the duplicate title changed its source document');
  const restoredDuplicate = builder.workspace.documents.find((document) => document.output.id === duplicateOutputId);
  assert.deepEqual(normalizedDocument(restoredDuplicate), normalizedDocument(sourceDocument), 'Restoring the duplicate title changed its authored document or field bindings');
  report.actions.push({ label: 'Restore duplicate title', control: restoreDuplicateTitleControl, elapsedMs: restoreDuplicateTitleMs, ...restoreDuplicateTitleState, sourceOutputId: createdOutputId, duplicateOutputId });

  await selectTable(duplicateOutputId, ids, titles);
  const moveTitles = ['Specimen baseline', duplicateTitle, renamedTitle];
  const moveStarted = Date.now();
  const moveControl = await click(page, `button[aria-label=${JSON.stringify(`Move ${duplicateTitle} up`)}]`, {}, 1500);
  ids = [baselineOutputId, duplicateOutputId, createdOutputId];
  const moveState = await readNativeState(ids, moveTitles, { selectedOutputId: duplicateOutputId });
  const moveMs = Date.now() - moveStarted;
  assert(moveMs <= 5000, `Reorder took ${moveMs} ms to save and render`);
  assert(commandTraffic().some((entry) => entry.body?.commands?.some((command) => command.type === 'REORDER_TABLES')), 'Reorder emitted no REORDER_TABLES command');
  const afterMoveReload = await reloadAndRead(ids, moveTitles);
  report.actions.push({ label: 'Reorder and reload', control: moveControl, elapsedMs: moveMs, ...moveState, afterReload: afterMoveReload });

  const undoReorderStarted = Date.now();
  const undoReorderControl = await click(page, '[data-testid="construction-undo"]', {}, 1500);
  ids = [baselineOutputId, createdOutputId, duplicateOutputId];
  const undoReorderState = await readNativeState(ids, titles, { maxMs: 5000 });
  const undoReorderMs = Date.now() - undoReorderStarted;
  assert(undoReorderMs <= 5000, `Undo reorder took ${undoReorderMs} ms to save and render`);
  assert(commandTraffic().some((entry) => entry.body?.commands?.some((command) => command.type === 'RESTORE_DRAFT_REVISION')), 'Undo emitted no RESTORE_DRAFT_REVISION command');
  const afterUndoReload = await reloadAndRead(ids, titles);
  report.actions.push({ label: 'Undo reorder and reload', control: undoReorderControl, elapsedMs: undoReorderMs, ...undoReorderState, afterReload: afterUndoReload });

  await selectTable(duplicateOutputId, ids, titles);
  const commandCountBeforeDelete = commandTraffic().length;
  queueDialog('confirm', `Delete ${duplicateTitle}?`, { accept: false });
  const deleteCancelStarted = Date.now();
  const deleteCancelControl = await click(page, '[data-testid="construction-delete-table"]', {}, 1500);
  const cancelDeleteState = await readNativeState(ids, titles, { selectedOutputId: duplicateOutputId, maxMs: 5000 });
  builder = await readBuilder();
  assert.deepEqual(orderedTables(builder).map((table) => table.outputId), ids, 'Canceling Delete changed saved table order');
  assert.equal(commandTraffic().length, commandCountBeforeDelete, 'Canceling Delete sent a table command');
  const deleteCancelMs = Date.now() - deleteCancelStarted;
  assert(deleteCancelMs <= 5000, `Canceling Delete took ${deleteCancelMs} ms to render`);
  report.actions.push({ label: 'Cancel Delete', control: deleteCancelControl, elapsedMs: deleteCancelMs, ...cancelDeleteState });

  queueDialog('confirm', `Delete ${duplicateTitle}?`, { accept: true });
  const deleteStarted = Date.now();
  const deleteControl = await click(page, '[data-testid="construction-delete-table"]', {}, 1500);
  ids = [baselineOutputId, createdOutputId];
  titles = ['Specimen baseline', renamedTitle];
  const deleteState = await readNativeState(ids, titles);
  const deleteMs = Date.now() - deleteStarted;
  assert(deleteMs <= 5000, `Delete took ${deleteMs} ms to save and render`);
  assert(commandTraffic().slice(commandCountBeforeDelete).some((entry) => entry.body?.commands?.some((command) => command.type === 'DELETE_TABLE' && command.outputId === duplicateOutputId)), 'Delete emitted no DELETE_TABLE command');
  report.actions.push({ label: 'Delete table', control: deleteControl, elapsedMs: deleteMs, ...deleteState });

  const undoDeleteStarted = Date.now();
  const undoDeleteControl = await click(page, '[data-testid="construction-undo"]', {}, 1500);
  ids = [baselineOutputId, createdOutputId, duplicateOutputId];
  titles = ['Specimen baseline', renamedTitle, duplicateTitle];
  const undoDeleteState = await readNativeState(ids, titles);
  const undoDeleteMs = Date.now() - undoDeleteStarted;
  assert(undoDeleteMs <= 5000, `Undo delete took ${undoDeleteMs} ms to save and render`);
  const afterUndoDeleteReload = await reloadAndRead(ids, titles);
  report.actions.push({ label: 'Undo delete and reload', control: undoDeleteControl, elapsedMs: undoDeleteMs, ...undoDeleteState, afterReload: afterUndoDeleteReload });

  await browserEvents?.flush();
  const previews = report.protocol.filter((entry) => entry.path.endsWith('/preview'));
  assert(previews.length > 0, 'Native table creation did not issue a bounded preview');
  assert(previews.every((entry) => Number(entry.body?.limit) <= 25), 'A native preview exceeded 25 rows');
  assert.equal(dialogQueue.length, 0, 'A queued dialog response was not consumed');
  includeBrowserDiagnostics();
  assert.deepEqual(cda.diagnostics.pageErrors, [], 'Browser JavaScript exceptions occurred');
  assert.deepEqual(report.errors, [], 'Unexpected browser HTTP, runtime, console, or module errors occurred');
  validateVisiblePreviewIds();
};

try {
  await main();
  report.status = 'passed';
} catch (error) {
  fatal = error;
  report.status = 'failed';
  report.failure = {
    message: String(error),
    stack: error?.stack,
    nativePreview: await currentPreview().catch(String),
    nativeTables: await nativeTables().catch(String),
    dom: await browserEval(page, () => document.body.innerText.slice(-18000)).catch((domError) => `DOM capture failed: ${String(domError)}`),
  };
} finally {
  if (builder && baselineOutputId) {
    try {
      builder = await readBuilder();
      const temporaryOutputIds = builder.workspace.documents
        .map((document) => document.output.id)
        .filter((outputId) => outputId !== baselineOutputId);
      for (const outputId of temporaryOutputIds) {
        builder = await readBuilder();
        if (!builder.workspace.documents.some((document) => document.output.id === outputId)) continue;
        assert(builder.workspace.documents.length > 1, 'Cleanup refused to delete the baseline table');
        await apply([{ type: 'DELETE_TABLE', outputId }]);
      }
      builder = await readBuilder();
      assert.deepEqual(orderedTables(builder).map((table) => table.outputId), [baselineOutputId], 'Cleanup did not retain only the API-seeded baseline');
      assert.deepEqual(orderedTables(builder).map((table) => table.title), ['Specimen baseline']);
      report.cleanup = { retainedBaselineOutputId: baselineOutputId, retainedTitle: 'Specimen baseline' };
      {
        await navigate(page, pageURL);
        await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-workspace"]'))), [], 30000);
        report.finalNativeState = await readNativeState([baselineOutputId], ['Specimen baseline'], { selectedOutputId: baselineOutputId, maxMs: 30000 });
      }
    } catch (error) {
      report.cleanupError = String(error);
      report.status = 'failed';
      fatal ??= error;
    }
  }
  await browserEvents?.flush();
  await cda.attachReport('table-management-browser.json', report);
}

if (fatal || report.status === 'failed') throw fatal ?? new Error(report.cleanupError ?? 'Table-management lifecycle failed');
return report;
}
