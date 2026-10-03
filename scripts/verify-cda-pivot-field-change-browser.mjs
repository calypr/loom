import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

// Bounded UI regression for changing a Pivot's category source from quantity
// code to Observation.status. The raw oracle contains every Observation on one
// selected Specimen -> Patient -> Observation route; it never samples categories.
const project = 'loom_dev_cda_fhir';
const explorer = `pivot-field-change-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-pivot-field-change-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const categoryA = 'Observation.valueQuantity.code';
const categoryB = 'Observation.status';
const valuePath = 'Observation.valueQuantity.value';
const groupLabels = ['Specimen ID', 'Patient FHIR resource ID', 'Observation FHIR resource ID'];
const report = { explorer, categories: { A: categoryA, B: categoryB }, cases: [], errors: [], authoringRequests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId;
const pendingResponseReads = new Set();
const requestById = new Map();
const failedRequests = new Map();
const parseJSON = value => { try { return JSON.parse(value); } catch { return value; } };
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `pivot-field-change-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(document => document.output.id === outputId);
const drainResponses = async () => { while (pendingResponseReads.size) await Promise.all([...pendingResponseReads]); };
const newestRequest = (endpoint, from = 0) => report.authoringRequests.slice(from).findLast(request => request.endpoint === endpoint);
const browserCommandCount = () => report.authoringRequests.filter(request => request.endpoint === 'commands').length;
const readyProposal = async (oldId, timeout = 10000) => {
  await waitForBrowser(browser.cdp,
    `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready' && Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId) && document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId!==${JSON.stringify(oldId ?? '')}`,
    timeout);
  await drainResponses();
};
const typedKey = value => value == null ? { kind: 'NULL' } : { kind: 'STRING', string: value };
const keyIdentity = key => JSON.stringify(key);
const categoryValue = (observation, path) => path === categoryA ? observation.quantityCode : observation.status;
const expectedDomain = (observations, path) => [...new Map(observations.map(row => {
  const key = typedKey(categoryValue(row, path));
  return [keyIdentity(key), key];
})).values()].sort((left, right) => keyIdentity(left).localeCompare(keyIdentity(right)));
const labelGroupIndex = label => {
  const normalized = label.toLowerCase();
  if (normalized.includes('specimen id')) return 0;
  if (normalized.includes('patient fhir resource id')) return 1;
  if (normalized.includes('observation fhir resource id')) return 2;
  return -1;
};
const oracleGroupValues = row => [row.specimenId, row.patientId, row.id];
const inputLabelFor = (steps, columnId) => steps.flatMap(step => step.outputs).find(column => column.id === columnId)?.label;
const assertProposalMatchesOracle = (request, path, name) => {
  assert.equal(request?.status, 200, `${name}: native Pivot proposal must succeed`);
  const response = request.response;
  assert(response?.proposalId, `${name}: proposal must include its ID`);
  const preview = response.preview;
  assert(preview, `${name}: proposal must include its exact protocol preview`);
  assert.equal(preview.receiptId, response.proposalId);
  assert.equal(preview.outputId, outputId);
  const step = response.candidateConstruction.steps.find(candidate => candidate.operation.kind === 'PIVOT');
  assert(step, `${name}: candidate must contain a Pivot step`);
  const operation = step.operation.pivot;
  const categoryInputLabel = inputLabelFor(response.candidateConstruction.steps, operation.categoryColumnId);
  const valueInputLabel = inputLabelFor(response.candidateConstruction.steps, operation.valueColumnId);
  assert(categoryInputLabel?.includes(path), `${name}: Pivot category input must be ${path}, got ${categoryInputLabel}`);
  assert(valueInputLabel?.includes(valuePath), `${name}: Pivot numeric value input must remain ${valuePath}, got ${valueInputLabel}`);
  const groupOutputs = operation.groupKeyIds.map(id => step.outputs.find(column => column.id === id));
  assert.equal(groupOutputs.length, groupLabels.length, `${name}: all three stable identity keys must remain selected`);
  assert(groupOutputs.every(Boolean), `${name}: every selected row key must be a Pivot output`);
  assert.deepEqual(new Set(groupOutputs.map(output => output.label)), new Set(groupLabels), `${name}: the Pivot row keys must match the intended source identities`);
  const categoryOutputs = operation.categories.map(category => ({
    key: category.key,
    output: step.outputs.find(column => column.id === category.outputColumnId),
  }));
  assert(categoryOutputs.every(category => category.output), `${name}: every discovered key must have an output`);
  const actualKeys = categoryOutputs.map(category => keyIdentity(category.key)).sort();
  const wantedKeys = expectedDomain(report.oracle.observations, path).map(keyIdentity).sort();
  assert.deepEqual(actualKeys, wantedKeys, `${name}: complete Pivot domain must equal every raw Observation on the selected route`);
  const previewColumns = preview.columns;
  assert(previewColumns.length >= groupOutputs.length + categoryOutputs.length, `${name}: preview must contain groups and category outputs`);
  const bucketCounts = new Map();
  for (const row of report.oracle.observations) {
    const key = keyIdentity(typedKey(categoryValue(row, path)));
    const identity = JSON.stringify([...oracleGroupValues(row), key]);
    bucketCounts.set(identity, (bucketCounts.get(identity) ?? 0) + 1);
  }
  assert([...bucketCounts.values()].every(count => count === 1), `${name}: row keys must uniquely identify every input/category cell`);
  const expectedRows = report.oracle.observations.map(row => Object.fromEntries(previewColumns.map(column => {
    const group = groupOutputs.find(output => output.name === column.column);
    if (group) {
      const index = labelGroupIndex(group.label);
      assert(index >= 0, `${name}: unexpected row key ${group.label}`);
      return [column.column, oracleGroupValues(row)[index] ?? null];
    }
    const category = categoryOutputs.find(candidate => candidate.output.name === column.column);
    assert(category, `${name}: unexpected output column ${column.column}`);
    const matches = keyIdentity(category.key) === keyIdentity(typedKey(categoryValue(row, path)));
    return [column.column, matches && typeof row.quantityValue === 'number' && Number.isFinite(row.quantityValue) ? row.quantityValue : null];
  })));
  assert.equal(preview.rowCount, report.oracle.observations.length, `${name}: every scoped Observation must have one Pivot row`);
  assert.equal(preview.rows.length, expectedRows.length, `${name}: the bounded protocol preview must contain every selected-route row`);
  const ordered = rows => rows.map(row => JSON.stringify(previewColumns.map(column => row[column.column]))).sort();
  assert.deepEqual(ordered(preview.rows), ordered(expectedRows), `${name}: Pivot values must match the raw quantity values for every Observation`);
  assert(preview.rows.every(row => typeof row.__loom_row_id === 'string' && row.__loom_row_id.length > 0), `${name}: every row must retain protocol identity`);
  assert.equal(new Set(preview.rows.map(row => row.__loom_row_id)).size, preview.rows.length, `${name}: row identities must be unique`);
  return { request, response, preview, step, operation, categoryOutputs, expectedRows, columns: previewColumns };
};
const proposalToReady = async (name, start, requestIndex, oldProposalId, path) => {
  await readyProposal(oldProposalId);
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms; automatic discovery and preview must finish within 5s`);
  const proposalRequest = newestRequest('construction-proposals', requestIndex);
  const checked = assertProposalMatchesOracle(proposalRequest, path, name);
  report.cases.push({ name, durationMs, category: path, rowCount: checked.preview.rowCount, domain: expectedDomain(report.oracle.observations, path) });
  return checked;
};
const sourceOptions = async label => browserEval(browser.cdp,
  `return [...document.querySelector('select[aria-label=${JSON.stringify(label)}]').options].map(option=>({value:option.value,label:option.textContent.trim()}));`);
const chooseSource = async (label, path) => {
  const options = await sourceOptions(label);
  const matching = options.filter(option => option.value.startsWith('source:') && option.label.includes(path));
  assert.equal(matching.length, 1, `Require one native source choice for ${path}: ${JSON.stringify(options)}`);
  const selected = `document.querySelector('select[aria-label=${JSON.stringify(label)}]')?.selectedOptions[0]?.textContent.includes(${JSON.stringify(path)})`;
  await selectOption(browser.cdp, `select[aria-label="${label}"]`, matching[0].value, { settledWhen: selected });
};
const setPivotGroups = async () => {
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]'))`);
  const current = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled}));`);
  for (const label of groupLabels) {
    const state = current.find(item => item.label === `Pivot group ${label}`);
    assert(state && !state.disabled, `Native Pivot group ${label} must be available`);
  }
  for (const item of current) {
    const wanted = groupLabels.includes(item.label.replace(/^Pivot group /, ''));
    assert(!item.disabled || item.checked === wanted, `Native Pivot group ${item.label} is disabled in the wrong state`);
    if (!item.disabled && item.checked !== wanted) await click(browser.cdp, `input[aria-label=${JSON.stringify(item.label)}]`);
  }
  const selected = new Set(await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]:checked')].map(input=>input.getAttribute('aria-label'));`));
  assert.deepEqual(selected, new Set(groupLabels.map(label => `Pivot group ${label}`)));
};
const enterPivot = async () => {
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await click(browser.cdp, '[data-testid="construction-action-pivot-rows"]');
  await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')`);
  await setPivotGroups();
};
const snapshotPreview = async () => browserEval(browser.cdp, `return (async()=>{
  const root=document.querySelector('[data-testid="preview-table-scroll"]');
  const table=root?.querySelector('[role="table"]');
  if(!root||!table) return undefined;
  const total=Math.max(0,Number(table.getAttribute('aria-rowcount'))-1), rows=new Map();
  for(let page=0;page<100&&rows.size<total;page++){
    for(const row of table.querySelectorAll('[role="row"]')){
      const rowNumber=Number(row.firstElementChild?.textContent?.trim());
      if(!Number.isInteger(rowNumber)||rowNumber<1) continue;
      const cells=[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim());
      rows.set(rowNumber,cells);
    }
    if(rows.size>=total) break;
    const next=Math.min(root.scrollTop+Math.max(1,root.clientHeight/2),root.scrollHeight-root.clientHeight);
    if(next===root.scrollTop) break;
    root.scrollTop=next; await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  }
  root.scrollTop=0; await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  const rowOrdinals=[...rows.keys()].sort((left,right)=>left-right);
  return {rowCount:total,rowOrdinals,headers:[...root.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim().split('\\n')[0]),rows:rowOrdinals.map(number=>rows.get(number))};
})();`);
const assertSavedPreview = async (expected, name) => {
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')) && !document.body.innerText.includes('Loading your table…')`, 10000);
  const limit = await browserEval(browser.cdp, `return document.querySelector('select[aria-label="Preview row limit"]')?.value;`);
  if (limit !== '100') await selectOption(browser.cdp, 'select[aria-label="Preview row limit"]', '100', { settledWhen: `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(expected.rowCount + 1))}` });
  const actual = await snapshotPreview();
  assert(actual, `${name}: saved preview must render`);
  assert.equal(actual.rowCount, expected.rowCount, `${name}: saved Pivot row count`);
  assert.deepEqual(actual.rowOrdinals, Array.from({ length: expected.rowCount }, (_, index) => index + 1), `${name}: each rendered ordinal must be collected once`);
  assert.deepEqual(actual.headers, expected.headers, `${name}: saved Pivot headers`);
  assert.deepEqual(actual.rows.map(JSON.stringify).sort(), expected.rows.map(JSON.stringify).sort(), `${name}: saved Pivot cells`);
};
const openTable = async () => {
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`, 10000);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`, 10000);
};
const applyAndWait = async (timeout = 10000) => {
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, timeout);
};
const waitForRenderedRows = async (rowCount, timeout = 5000) => waitForBrowser(browser.cdp,
  `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(rowCount + 1))} && !document.body.innerText.includes('Loading your table…')`, timeout);
const historyPivot = async () => {
  const items = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));`);
  const item = items.findLast(value => /pivot|categories into columns/i.test(value.text));
  assert(item, `Saved history must include the Pivot step: ${JSON.stringify(items)}`);
  return item;
};
try {
  assert.equal(new URL(apiOrigin).hostname, '127.0.0.1', 'Raw CDA oracle is restricted to the local no-auth Compose API');
  assert.equal(new URL(apiOrigin).port, '8188', 'Raw CDA oracle is restricted to the retained local no-auth Compose API');
  assert.equal(new URL(uiOrigin).hostname, '127.0.0.1', 'The browser verifier must use the retained local Builder UI');
  assert.equal(new URL(uiOrigin).port, '30008', 'The browser verifier must use the retained local Builder UI');
  const rawQuery = `FOR s IN Specimen
    FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == "cda-fhir-v1"
    LET patients = (FOR e IN fhir_edge
      FILTER e._from == s._id AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == "cda-fhir-v1"
      LET p = DOCUMENT(e._to) FILTER p.project == ${JSON.stringify(project)} AND p.dataset_generation == "cda-fhir-v1" RETURN DISTINCT p)
    LET observations = (FOR p IN patients FOR e IN fhir_edge
      FILTER e._to == p._id AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == "cda-fhir-v1"
      FILTER STARTS_WITH(e._from, "Observation/")
      LET o = DOCUMENT(e._from) FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == "cda-fhir-v1"
      RETURN DISTINCT {id:o.id,_id:o._id,patientId:p.id,status:o.payload.status,quantityCode:o.payload.valueQuantity.code,quantityValue:o.payload.valueQuantity.value,specimenId:s.id})
    FILTER LENGTH(observations) > 0
    LIMIT 1
    RETURN {source:{id:s.id,_id:s._id,generation:s.dataset_generation},patientCount:LENGTH(patients),observations}`;
  const rawResult = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(rawQuery)}).toArray()));`], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(rawResult.status, 0, rawResult.stderr);
  const fixtures = JSON.parse(rawResult.stdout.slice(rawResult.stdout.indexOf('[')));
  const fixture = fixtures[0];
  assert(fixture?.source?.id && fixture.source.generation, 'Raw route oracle must select one scoped Specimen');
  assert(Array.isArray(fixture.observations) && fixture.observations.length > 0 && fixture.observations.length <= 1000, 'Oracle must retain every Observation on one bounded selected route');
  assert(fixture.observations.every(row => typeof row.id === 'string' && typeof row.patientId === 'string' && typeof row.specimenId === 'string'), 'Each scoped Observation must have its raw relationship identities');
  assert(fixture.observations.every(row => row.quantityCode == null || typeof row.quantityCode === 'string'), 'Raw quantity codes must be strings or null');
  assert(fixture.observations.every(row => row.status == null || typeof row.status === 'string'), 'Raw Observation statuses must be strings or null');
  assert(fixture.observations.every(row => row.quantityValue == null || (typeof row.quantityValue === 'number' && Number.isFinite(row.quantityValue))), 'Raw quantity values must be finite numbers or null');
  const domainA = expectedDomain(fixture.observations, categoryA);
  const domainB = expectedDomain(fixture.observations, categoryB);
  assert(domainA.some(key => key.kind === 'STRING') && domainB.some(key => key.kind === 'STRING'), 'Both native fields must have real finite string categories on the selected route');
  assert.notDeepEqual(domainA, domainB, 'The two chosen fields must have distinct finite raw category domains');
  assert(domainA.every(key => !domainB.some(other => keyIdentity(key) === keyIdentity(other))), 'The selected route must distinguish the old category keys from the new category keys');
  report.oracle = { query: rawQuery, source: fixture.source, scope: 'all related Observation rows for one selected Specimen -> Patient -> Observation route', observationCount: fixture.observations.length, domains: { [categoryA]: domainA, [categoryB]: domainB }, observations: fixture.observations };

  await api(root, { name: explorer, title: 'Pivot category field rediscovery QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, fixture.source.generation);
  const specimenNode = builder.catalog.nodes.find(node => node.resourceType === 'Specimen');
  assert(specimenNode, 'Catalog must expose the selected Specimen root');
  await command([{ type: 'CREATE_TABLE', title: 'Pivot category field change', rootNodeId: specimenNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === specimenNode.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'Catalog must expose Specimen.id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: fixture.source.generation, resourceType: 'Specimen', id: fixture.source.id }] } },
  });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(choice => choice.route.length === 0);
  assert(direct, 'Population route must include the selected Specimen directly');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);

  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', details => report.errors.push({ kind: 'runtime', details }));
  browser.cdp.on('Runtime.consoleAPICalled', event => { if (event.type === 'error') report.errors.push({ kind: 'console', args: event.args }); });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, timestamp }) => {
    const endpoint = ['commands', 'reconcile', 'preview', 'construction-proposals', 'construction-category-discoveries'].find(path => new URL(request.url).pathname.endsWith(`/authoring/v2/${path}`));
    if (!endpoint) return;
    const body = request.postData ? parseJSON(request.postData) : undefined;
    const captured = { endpoint, requestId, method: request.method, url: request.url, startedAtMs: timestamp * 1000, body };
    report.authoringRequests.push(captured);
    requestById.set(requestId, captured);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response, timestamp }) => {
    const captured = requestById.get(requestId);
    if (captured) { captured.status = response.status; captured.responseStartedAtMs = timestamp * 1000; }
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) failedRequests.set(requestId, { requestId, status: response.status, url: response.url });
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId, timestamp }) => {
    const captured = requestById.get(requestId);
    if (!captured && !failedRequests.has(requestId)) return;
    const read = browser.cdp.send('Network.getResponseBody', { requestId }).then(value => {
      const text = value.isBase64Encoded ? Buffer.from(value.body, 'base64').toString('utf8') : value.body;
      if (captured) { captured.response = parseJSON(text); captured.finishedAtMs = timestamp * 1000; captured.durationMs = captured.finishedAtMs - captured.startedAtMs; }
      if (failedRequests.has(requestId)) report.errors.push({ kind: 'http', ...failedRequests.get(requestId), body: text });
    }).catch(error => { if (captured) captured.responseReadError = String(error); }).finally(() => pendingResponseReads.delete(read));
    pendingResponseReads.add(read);
  });
  browser.cdp.on('Network.loadingFailed', event => { if (event.type === 'Script' && event.errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', error: event.errorText }); });

  await openTable();
  const hops = [
    { from: 'Specimen', to: 'Patient', label: 'Specimen -[subject]-> Patient', field: 'subject', direction: 'OUTBOUND', edge: 'subject_Patient' },
    { from: 'Patient', to: 'Observation', label: 'Patient <-[subject]- Observation', field: 'subject', direction: 'INBOUND', edge: 'subject_Patient' },
  ];
  for (const hop of hops) {
    await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false`);
    await click(browser.cdp, '[data-testid="construction-action-related-rows"]');
    const panel = '[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(panel)}+' select[aria-label="Related record type"]')?.disabled===false`);
    await selectOption(browser.cdp, panel + ' select[aria-label="Related record type"]', hop.to);
    const inputLabel = hop.label;
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(panel + ' input[aria-label="' + inputLabel + '"]')}))`);
    await click(browser.cdp, panel + ' input[aria-label=' + JSON.stringify(inputLabel) + ']');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'`, 10000);
    await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 10000);
    builder = await api(base + '/builder');
  }
  await openTable();
  const expandedWorkspace = (await api(base + '/builder')).workspace;
  const baselineLimit = await browserEval(browser.cdp, `return document.querySelector('select[aria-label="Preview row limit"]')?.value;`);
  if (baselineLimit !== '100') await selectOption(browser.cdp, 'select[aria-label="Preview row limit"]', '100', { settledWhen: `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(fixture.observations.length + 1))}` });
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(fixture.observations.length + 1))}`, 10000);
  const fullBaseline = await snapshotPreview();
  assert.equal(fullBaseline.rowCount, fixture.observations.length);
  report.baseline = { headers: fullBaseline.headers, rowCount: fullBaseline.rowCount, rows: fullBaseline.rows };

  await enterPivot();
  let requestIndex = report.authoringRequests.length;
  const oldIdA = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';`);
  let start = Date.now();
  await chooseSource('Pivot category field', categoryA);
  await chooseSource('Pivot values field', valuePath);
  const pivotA = await proposalToReady('category-A-discovery-and-preview', start, requestIndex, oldIdA, categoryA);
  const discoveryA = newestRequest('construction-category-discoveries', requestIndex);
  assert.equal(discoveryA?.status, 200, 'Category A discovery request must succeed');
  assert.equal(discoveryA.response?.outcome, 'COMPLETE', 'Category A discovery must complete');
  assert.equal(discoveryA.response?.complete, true, 'Category A discovery must be complete');
  const discoveredA = (discoveryA.response.categories ?? []).map(category => keyIdentity(category.key)).sort();
  assert.deepEqual(discoveredA, domainA.map(keyIdentity).sort(), 'Category A discovery must match the complete raw route domain');
  report.cases.push({ name: 'category-A-discovery-complete', complete: true, categoryCount: domainA.length });

  const oldIdB = pivotA.response.proposalId;
  requestIndex = report.authoringRequests.length;
  start = Date.now();
  await chooseSource('Pivot category field', categoryB);
  const pivotB = await proposalToReady('category-B-change-discovery-and-preview', start, requestIndex, oldIdB, categoryB);
  const discoveryB = newestRequest('construction-category-discoveries', requestIndex);
  assert.equal(discoveryB?.status, 200, 'Category B rediscovery request must succeed');
  assert.equal(discoveryB.response?.outcome, 'COMPLETE', 'Category B rediscovery must complete');
  assert.equal(discoveryB.response?.complete, true, 'Category B rediscovery must be complete');
  const discoveredB = (discoveryB.response.categories ?? []).map(category => keyIdentity(category.key)).sort();
  assert.deepEqual(discoveredB, domainB.map(keyIdentity).sort(), 'Category B rediscovery must exactly match every raw status value');
  const keysA = new Set(pivotA.operation.categories.map(category => keyIdentity(category.key)));
  const keysB = new Set(pivotB.operation.categories.map(category => keyIdentity(category.key)));
  assert([...keysA].every(key => !keysB.has(key)), 'Changing the source field must remove every stale category A key');
  assert.equal(pivotB.step.outputs.length, groupLabels.length + domainB.length, 'The changed Pivot step must contain only row keys and the newly discovered category outputs');
  assert(inputLabelFor(pivotB.response.candidateConstruction.steps, pivotB.operation.valueColumnId)?.includes(valuePath),
    'Changing category source must preserve Observation.valueQuantity.value as the numeric Pivot value');
  report.cases.push({ name: 'category-A-outputs-removed', staleKeys: [...keysA], currentKeys: [...keysB] });

  const commandsBeforeCancel = report.authoringRequests.filter(request => request.endpoint === 'commands').length;
  const cancelStartedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, expandedWorkspace, 'Cancel must not mutate the saved pre-Pivot construction');
  assert.equal(report.authoringRequests.filter(request => request.endpoint === 'commands').length, commandsBeforeCancel, 'Cancel must not send a draft command');
  assert(Date.now() - cancelStartedAt <= 5000, 'Cancel must close within 5s');
  report.cases.push({ name: 'cancel-keeps-pre-pivot-draft', durationMs: Date.now() - cancelStartedAt });

  requestIndex = report.authoringRequests.length;
  await enterPivot();
  const oldIdConfirm = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';`);
  start = Date.now();
  await chooseSource('Pivot category field', categoryB);
  await chooseSource('Pivot values field', valuePath);
  const applyChecked = await proposalToReady('category-B-confirmed-preview', start, requestIndex, oldIdConfirm, categoryB);
  await applyAndWait();
  builder = await api(base + '/builder');
  const savedPivot = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(savedPivot, 'Apply must save the changed Pivot');
  const savedKeys = savedPivot.operation.pivot.categories.map(category => keyIdentity(category.key)).sort();
  assert.deepEqual(savedKeys, domainB.map(keyIdentity).sort(), 'Applied Pivot must persist the newly discovered status domain');
  assert(inputLabelFor(doc(builder).construction.steps, savedPivot.operation.pivot.valueColumnId)?.includes(valuePath),
    'Applied Pivot must persist the original numeric value source');
  // Switch to the full saved Pivot render and retain a user-visible exact oracle snapshot.
  const appliedSnapshot = await snapshotPreview();
  assert(appliedSnapshot && appliedSnapshot.rowCount === fixture.observations.length);
  report.applied = { categoryKeys: savedKeys, valuePath, rowCount: applyChecked.preview.rowCount, protocolColumns: applyChecked.columns.map(column => ({ column: column.column, label: column.label })) };
  const reloadStart = Date.now();
  await openTable();
  await assertSavedPreview(appliedSnapshot, 'category-B-reload');
  assert(Date.now() - reloadStart <= 5000, 'Applied Pivot reload must render within 5s');
  report.cases.push({ name: 'category-B-apply-reload', durationMs: Date.now() - reloadStart, rowCount: fixture.observations.length });

  const pivotHistory = await historyPivot();
  await click(browser.cdp, `[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`);
  const pivotStep = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  await click(browser.cdp, `[data-testid="construction-edit-step-${pivotStep.id}"]`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))`);
  const restoredEditor = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-reshape-pivot"]');return {category:panel.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:panel.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent};`);
  assert(restoredEditor.category.includes(categoryB), `Edit must restore category B, got ${restoredEditor.category}`);
  assert(restoredEditor.value.includes(valuePath), `Edit must preserve numeric values field ${valuePath}, got ${restoredEditor.value}`);
  const advancedSelector = '[data-testid="construction-reshape-pivot-advanced"]';
  if (!await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(advancedSelector)})?.open;`)) {
    await click(browser.cdp, `${advancedSelector} summary`);
  }
  const editLabels = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Pivot output label"]')].map(input=>({label:input.getAttribute('aria-label'),value:input.value}));`);
  assert(editLabels.length > 0, 'Edit must restore category output labels');
  const editTarget = editLabels[0];
  const nextLabel = `${editTarget.value} Verified`;
  const previousProposalId = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';`);
  requestIndex = report.authoringRequests.length;
  start = Date.now();
  const editSelector = `input[aria-label=${JSON.stringify(editTarget.label)}]`;
  await browserEval(browser.cdp, `document.querySelector(${JSON.stringify(editSelector)}).scrollIntoView({block:'center',inline:'nearest'});await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));`);
  await click(browser.cdp, editSelector);
  await browserEval(browser.cdp, `document.activeElement.select();`);
  await browser.cdp.send('Input.insertText', { text: nextLabel });
  assert.equal(await browserEval(browser.cdp, `return document.activeElement.value;`), nextLabel, 'Native typing must update the Pivot label');
  const editPreviewCommandCount = browserCommandCount();
  const edited = await proposalToReady('category-B-edit-preview', start, requestIndex, previousProposalId, categoryB);
  const editPreviewDurationMs = Date.now() - start;
  assert(editPreviewDurationMs <= 5000, `Edited Pivot label-change-to-preview took ${editPreviewDurationMs}ms`);
  assert.equal(browserCommandCount(), editPreviewCommandCount, 'Editing the output label and previewing must not issue a draft command');
  const editApplyCommandCount = browserCommandCount();
  const editApplyStartedAt = Date.now();
  await applyAndWait(5000);
  await waitForRenderedRows(fixture.observations.length, Math.max(1, 5000 - (Date.now() - editApplyStartedAt)));
  const editApplyDurationMs = Date.now() - editApplyStartedAt;
  assert(editApplyDurationMs <= 5000, `Edited Pivot apply-to-render took ${editApplyDurationMs}ms`);
  await drainResponses();
  assert.equal(browserCommandCount() - editApplyCommandCount, 1, 'Applying the edited Pivot must issue exactly one bounded draft command');
  report.cases.push({ name: 'edit-category-B-apply-to-render', durationMs: editApplyDurationMs, browserCommandDelta: browserCommandCount() - editApplyCommandCount });
  builder = await api(base + '/builder');
  const editedPivot = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(editedPivot && editedPivot.operation.pivot.categories.map(category => keyIdentity(category.key)).sort().join('|') === domainB.map(keyIdentity).sort().join('|'), 'Edited Pivot must retain the complete category B domain');
  assert(edited.categoryOutputs.length === domainB.length, 'Edited Pivot must retain all status outputs');
  const afterEditSnapshot = await snapshotPreview();
  const editReloadCommandCount = browserCommandCount();
  const editReloadStartedAt = Date.now();
  await openTable();
  await assertSavedPreview(afterEditSnapshot, 'edited-category-B-reload');
  const editReloadDurationMs = Date.now() - editReloadStartedAt;
  assert(editReloadDurationMs <= 5000, `Edited Pivot reload-to-render took ${editReloadDurationMs}ms`);
  assert.equal(browserCommandCount(), editReloadCommandCount, 'Reloading the edited Pivot must not issue a draft command');
  report.cases.push({ name: 'edit-category-B-reload-to-render', durationMs: editReloadDurationMs, browserCommandDelta: 0 });
  const editState = await api(base + '/builder');
  const persistedEditedStep = doc(editState).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(persistedEditedStep, 'Edited Pivot must survive reload');
  assert(persistedEditedStep.outputs.some(column => column.label === nextLabel), 'Edited output label must survive reload');
  report.cases.push({ name: 'edit-category-B-preserves-values-and-reloads', durationMs: editReloadDurationMs, timingMs: { labelChangeToPreview: editPreviewDurationMs, applyToRender: editApplyDurationMs, reloadToRender: editReloadDurationMs }, categoryCount: domainB.length, valuePath, editedLabel: nextLabel });

  const removeHistory = await historyPivot();
  await click(browser.cdp, `[data-testid=${JSON.stringify(removeHistory.testId)}]`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`);
  requestIndex = report.authoringRequests.length;
  const removePreviewCommandCount = browserCommandCount();
  const removePreviewStartedAt = Date.now();
  await click(browser.cdp, '[data-testid^="construction-remove-step-"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'`, 5000);
  await drainResponses();
  const removePreviewDurationMs = Date.now() - removePreviewStartedAt;
  assert(removePreviewDurationMs <= 5000, `Pivot removal action-to-preview took ${removePreviewDurationMs}ms`);
  assert.equal(browserCommandCount(), removePreviewCommandCount, 'Opening the Pivot removal proposal must not issue a draft command');
  report.cases.push({ name: 'pivot-removal-action-to-preview', durationMs: removePreviewDurationMs, browserCommandDelta: 0 });
  const removal = newestRequest('construction-proposals', requestIndex);
  assert.equal(removal?.status, 200, 'Removing Pivot must produce a valid restoration proposal');
  assert(!removal.response.candidateConstruction.steps.some(step => step.operation.kind === 'PIVOT'), 'Removal proposal must omit the Pivot step');
  const removeApplyCommandCount = browserCommandCount();
  const removeApplyStartedAt = Date.now();
  await applyAndWait(5000);
  await waitForRenderedRows(fixture.observations.length, Math.max(1, 5000 - (Date.now() - removeApplyStartedAt)));
  const removeApplyDurationMs = Date.now() - removeApplyStartedAt;
  assert(removeApplyDurationMs <= 5000, `Pivot removal apply-to-render took ${removeApplyDurationMs}ms`);
  await drainResponses();
  assert.equal(browserCommandCount() - removeApplyCommandCount, 1, 'Applying Pivot removal must issue exactly one bounded draft command');
  report.cases.push({ name: 'pivot-removal-apply-to-render', durationMs: removeApplyDurationMs, browserCommandDelta: browserCommandCount() - removeApplyCommandCount });
  builder = await api(base + '/builder');
  assert(!doc(builder).construction.steps.some(step => step.operation.kind === 'PIVOT'), 'Applied removal must delete the Pivot step');
  const restoredSnapshot = await snapshotPreview();
  assert.deepEqual(restoredSnapshot.headers, fullBaseline.headers, 'Removing Pivot must restore the pre-Pivot columns');
  assert.deepEqual(restoredSnapshot.rows.map(JSON.stringify).sort(), fullBaseline.rows.map(JSON.stringify).sort(), 'Removing Pivot must restore the exact pre-Pivot values');
  assert.equal(restoredSnapshot.rowCount, fullBaseline.rowCount);
  const removeReloadCommandCount = browserCommandCount();
  const removeReloadStartedAt = Date.now();
  await openTable();
  await assertSavedPreview(fullBaseline, 'pivot-removal-reload-restores-original-table');
  const removeReloadDurationMs = Date.now() - removeReloadStartedAt;
  assert(removeReloadDurationMs <= 5000, `Pivot removal reload-to-render took ${removeReloadDurationMs}ms`);
  assert.equal(browserCommandCount(), removeReloadCommandCount, 'Reloading the restored table must not issue a draft command');
  report.cases.push({ name: 'pivot-removal-reload-to-render', durationMs: removeReloadDurationMs, browserCommandDelta: 0 });
  report.cases.push({ name: 'pivot-removal-restores-pre-pivot-table', durationMs: removeReloadDurationMs, rowCount: restoredSnapshot.rowCount, headers: restoredSnapshot.headers });

  await drainResponses();
  assert.deepEqual(report.errors, [], 'Unexpected browser console, runtime, or HTTP errors must fail this verifier');
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  process.exitCode = 1;
  await drainResponses();
  if (browser) report.failureDOM = await browserEval(browser.cdp, `return (()=>{const p=document.querySelector('[data-testid="construction-reshape-pivot"]');const describe=selector=>{const select=document.querySelector(selector);return {selector,selected:select?.selectedOptions[0]?.textContent?.trim(),options:[...(select?.options??[])].map(option=>({label:option.textContent.trim(),value:option.value,disabled:option.disabled}))};};return {pivotVisible:Boolean(p),groupCheckboxes:[...(p?.querySelectorAll('input[aria-label^="Pivot group "]')??[])].map(input=>({selector:'input[aria-label='+JSON.stringify(input.getAttribute('aria-label'))+']',label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled})),groupSource:describe('select[aria-label="Add pivot group field"]'),category:describe('select[aria-label="Pivot category field"]'),value:describe('select[aria-label="Pivot values field"]'),proposal:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText};})()`).catch(captureError => ({ captureError: String(captureError) }));
} finally {
  await drainResponses();
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases, error: report.error }));
