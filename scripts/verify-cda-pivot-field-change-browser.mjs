import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { collectPreviewRows } from './lib/playwright-preview-rows.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';

// Bounded UI regression for changing a Pivot's category source from quantity
// code to Observation.status. The raw oracle contains every Observation on one
// selected Specimen -> Patient -> Observation route; it never samples categories.

export async function runPivotFieldChangeBrowserWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const project = cda.project;
const explorer = cda.explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
assert((cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), 'Set LOOM_ARANGO_DATABASE for the isolated CDA source database.');
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer: (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER),
  composeProject: (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT), sourceRoot, arangoContainer: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER),
  clickhouseContainer: (cda.target.clickhouseContainer ?? cda.env?.LOOM_CLICKHOUSE_CONTAINER) });
const sourceFreeze = await captureSourceFreeze(sourceRoot);
const apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp((cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER)));
const categoryA = 'Observation.valueQuantity.code';
const categoryB = 'Observation.status';
const valuePath = 'Observation.valueQuantity.value';
const groupLabels = ['Specimen ID', 'Patient FHIR resource ID', 'Observation FHIR resource ID'];
const report = { explorer, categories: { A: categoryA, B: categoryB }, cases: [], errors: [], authoringRequests: [], nativeRequests: [],
  sourceFingerprint: { before: sourceFingerprint(sourceRoot) }, apiBuildIdentity: apiBuildFreeze.initial, started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let builder, outputId;
const inspectPage = (_page, inspect, argument) => cda.inspect(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument;
  return cda.wait(predicate, argument ?? {}, Math.min(timeout, 5000));
};
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
const waitForVisible = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'visible', timeout });
const waitForHidden = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'hidden', timeout });
const waitForEnabled = async (page, selector, timeout = 5000) => {
  const locator = page.locator(selector);
  await locator.waitFor({ state: 'visible', timeout });
  await page.waitForFunction(value => { const element = document.querySelector(value); return Boolean(element && !element.disabled); }, selector, { timeout });
};
const gotoPage = (_page, url) => cda.navigate(url);
const clickNative = (_page, selector, identity = {}) => cda.click(selector, identity, 5000);
const selectNative = async (_page, selector, value) => {
  const locator = page.locator(selector);
  await cda.selectOption(selector, value);
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
let browserRequestCapture;
const syncAuthoringRequests = () => {
  const endpoints = new Set(['commands', 'reconcile', 'preview', 'construction-proposals', 'construction-category-discoveries']);
  report.authoringRequests.splice(0, report.authoringRequests.length, ...report.nativeRequests
    .filter(request => endpoints.has(request.path.split('/').at(-1)))
    .map(request => ({ ...request, endpoint: request.path.split('/').at(-1), url: `${request.origin}${request.path}`, startedAtMs: request.startedAt,
      finishedAtMs: request.completedAt, responseFinishedAtMs: request.completedAt, durationMs: request.completedAt - request.startedAt,
      requestDraftVersion: request.body?.expectedDraftVersion ?? request.body?.draftVersion, requestDraftDigest: request.body?.expectedDraftDigest ?? request.body?.draftDigest,
      requestOutputId: request.body?.outputId, requestReceiptId: request.body?.receiptId,
      responseDraftVersion: request.response?.draftVersion, responseDraftDigest: request.response?.draftDigest,
      responseReceiptId: request.response?.receiptId, responseOutputId: request.response?.outputId,
      responseRowCount: request.response?.rowCount ?? request.response?.preview?.rowCount ?? request.response?.rows?.length,
      ...(request.failure ? { loadingFailure: { errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' } } : {}),
    })));
};
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
const drainResponses = async () => { await browserRequestCapture?.flush(); syncAuthoringRequests(); };
const newestRequest = (endpoint, from = 0) => report.authoringRequests.slice(from).findLast(request => request.endpoint === endpoint);
const browserCommandCount = () => report.authoringRequests.filter(request => request.endpoint === 'commands').length;
const readyProposal = async (oldId, timeout = 5000) => {
  await waitForObservable(page, ({ oldId }) => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.dataset.proposalStatus === 'ready' && Boolean(panel.dataset.proposalId) && panel.dataset.proposalId !== oldId;
  }, { oldId: oldId ?? '' }, timeout);
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
const sourceOptions = async label => inspectPage(page,
  ({ label }) => [...document.querySelector(`select[aria-label="${label}"]`).options].map(option=>({value:option.value,label:option.textContent.trim()})), { label: label });
const chooseSource = async (label, path) => {
  const options = await sourceOptions(label);
  const matching = options.filter(option => option.value.startsWith('source:') && option.label.includes(path));
  assert.equal(matching.length, 1, `Require one native source choice for ${path}: ${JSON.stringify(options)}`);
  await selectNative(page, `select[aria-label="${label}"]`, matching[0].value);
};
const setPivotGroups = async () => {
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]'))));
  const current = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled}));
});
  for (const label of groupLabels) {
    const state = current.find(item => item.label === `Pivot group ${label}`);
    assert(state && !state.disabled, `Native Pivot group ${label} must be available`);
  }
  for (const item of current) {
    const wanted = groupLabels.includes(item.label.replace(/^Pivot group /, ''));
    assert(!item.disabled || item.checked === wanted, `Native Pivot group ${item.label} is disabled in the wrong state`);
    if (!item.disabled && item.checked !== wanted) await clickNative(page, `input[aria-label=${JSON.stringify(item.label)}]`);
  }
  const selected = new Set(await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]:checked')].map(input=>input.getAttribute('aria-label'));
}));
  assert.deepEqual(selected, new Set(groupLabels.map(label => `Pivot group ${label}`)));
};
const enterPivot = async () => {
  await clickNative(page, '[data-testid="construction-rows-settings-trigger"]');
  await clickNative(page, '[data-testid="construction-action-pivot-rows"]');
  await waitForObservable(page, () => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
  await setPivotGroups();
};
const snapshotPreview = async () => {
  const collected = await collectPreviewRows(page);
  return { rowCount: collected.rowCount, rowOrdinals: collected.rowOrdinals,
    headers: collected.headers, rows: collected.rows.map(row => row.values) };
};
const assertSavedPreview = async (expected, name) => {
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')) && !document.body.innerText.includes('Loading your table…')), 10000);
  const limit = await inspectPage(page, () => {
return document.querySelector('select[aria-label="Preview row limit"]')?.value;
});
  if (limit !== '100') await selectNative(page, 'select[aria-label="Preview row limit"]', '100');
  const actual = await snapshotPreview();
  assert(actual, `${name}: saved preview must render`);
  assert.equal(actual.rowCount, expected.rowCount, `${name}: saved Pivot row count`);
  assert.deepEqual(actual.rowOrdinals, Array.from({ length: expected.rowCount }, (_, index) => index + 1), `${name}: each rendered ordinal must be collected once`);
  assert.deepEqual(actual.headers, expected.headers, `${name}: saved Pivot headers`);
  assert.deepEqual(actual.rows.map(JSON.stringify).sort(), expected.rows.map(JSON.stringify).sort(), `${name}: saved Pivot cells`);
};
const openTable = async () => {
  await gotoPage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForVisible(page, `[data-testid="construction-table-${outputId}"]`, 10000);
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 10000);
};
const applyAndWait = async (timeout = 5000) => {
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), timeout);
};
const waitForRenderedRows = async (rowCount, timeout = 5000) => waitForObservable(page,
  ({ rowCount }) => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount + 1)
    && !document.body.innerText.includes('Loading your table…'), { rowCount }, timeout);
const historyPivot = async () => {
  const items = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
  const item = items.findLast(value => /pivot|categories into columns/i.test(value.text));
  assert(item, `Saved history must include the Pivot step: ${JSON.stringify(items)}`);
  return item;
};
try {
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
  const rawResult = spawnSync('rtk', ['proxy', 'docker', 'exec', (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER), 'arangosh', '--server.database', (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(rawQuery)}).toArray()));`], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
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
  browserRequestCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: base, report, responsePaths: /\/(?:commands|reconcile|preview|construction-proposals|construction-category-discoveries)$/ });
  page.on('request', request => {
    const captured = browserRequestCapture.byRequest.get(request);
    if (captured) captured.observedAfter = report.cases.at(-1)?.name;
  });

  await openTable();
  const hops = [
    { from: 'Specimen', to: 'Patient', label: 'Specimen -[subject]-> Patient', field: 'subject', direction: 'OUTBOUND', edge: 'subject_Patient' },
    { from: 'Patient', to: 'Observation', label: 'Patient <-[subject]- Observation', field: 'subject', direction: 'INBOUND', edge: 'subject_Patient' },
  ];
  for (const hop of hops) {
    await clickNative(page, '[data-testid="construction-rows-settings-trigger"]');
    await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await clickNative(page, '[data-testid="construction-action-related-rows"]');
    const panel = '[data-testid="construction-related-expand-editor"]';
    await waitForEnabled(page, `${panel} select[aria-label="Related record type"]`);
    await selectNative(page, panel + ' select[aria-label="Related record type"]', hop.to);
    const inputLabel = hop.label;
    await waitForVisible(page, `${panel} input[aria-label="${inputLabel}"]`);
    await clickNative(page, panel + ' input[aria-label=' + JSON.stringify(inputLabel) + ']');
    await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'), 10000);
    await clickNative(page, '[data-testid="construction-apply-proposal"]');
    await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), 10000);
    builder = await api(base + '/builder');
  }
  await openTable();
  const expandedWorkspace = (await api(base + '/builder')).workspace;
  const baselineLimit = await inspectPage(page, () => {
return document.querySelector('select[aria-label="Preview row limit"]')?.value;
});
  if (baselineLimit !== '100') await selectNative(page, 'select[aria-label="Preview row limit"]', '100');
  await waitForObservable(page, ({ rowCount }) => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount + 1), { rowCount: fixture.observations.length }, 10000);
  const fullBaseline = await snapshotPreview();
  assert.equal(fullBaseline.rowCount, fixture.observations.length);
  report.baseline = { headers: fullBaseline.headers, rowCount: fullBaseline.rowCount, rows: fullBaseline.rows };

  await enterPivot();
  let requestIndex = report.authoringRequests.length;
  const oldIdA = await inspectPage(page, () => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
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
  await clickNative(page, '[data-testid="construction-cancel-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), 5000);
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, expandedWorkspace, 'Cancel must not mutate the saved pre-Pivot construction');
  assert.equal(report.authoringRequests.filter(request => request.endpoint === 'commands').length, commandsBeforeCancel, 'Cancel must not send a draft command');
  assert(Date.now() - cancelStartedAt <= 5000, 'Cancel must close within 5s');
  report.cases.push({ name: 'cancel-keeps-pre-pivot-draft', durationMs: Date.now() - cancelStartedAt });

  requestIndex = report.authoringRequests.length;
  await enterPivot();
  const oldIdConfirm = await inspectPage(page, () => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
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
  await clickNative(page, `[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))));
  const pivotStep = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  await clickNative(page, `[data-testid="construction-edit-step-${pivotStep.id}"]`);
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))));
  const restoredEditor = await inspectPage(page, () => {
const panel=document.querySelector('[data-testid="construction-reshape-pivot"]');return {category:panel.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:panel.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent};
});
  assert(restoredEditor.category.includes(categoryB), `Edit must restore category B, got ${restoredEditor.category}`);
  assert(restoredEditor.value.includes(valuePath), `Edit must preserve numeric values field ${valuePath}, got ${restoredEditor.value}`);
  const advancedSelector = '[data-testid="construction-reshape-pivot-advanced"]';
  if (!await inspectPage(page, ({ selector }) => document.querySelector(selector)?.open, { selector: advancedSelector })) {
    await clickNative(page, `${advancedSelector} summary`);
  }
  const editLabels = await inspectPage(page, () => {
return [...document.querySelectorAll('input[aria-label^="Pivot output label"]')].map(input=>({label:input.getAttribute('aria-label'),value:input.value}));
});
  assert(editLabels.length > 0, 'Edit must restore category output labels');
  const editTarget = editLabels[0];
  const nextLabel = `${editTarget.value} Verified`;
  const previousProposalId = await inspectPage(page, () => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
  requestIndex = report.authoringRequests.length;
  start = Date.now();
  const editSelector = `input[aria-label=${JSON.stringify(editTarget.label)}]`;
  const labelInput = page.locator(editSelector);
  await cda.action('fill Pivot output label', labelInput, locator => locator.fill(nextLabel, { timeout: 5000 }), { timeout: 5000, budget: 5000, editable: true });
  assert.equal(await labelInput.inputValue(), nextLabel, 'Native typing must update the Pivot label');
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
  await clickNative(page, `[data-testid=${JSON.stringify(removeHistory.testId)}]`);
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))));
  requestIndex = report.authoringRequests.length;
  const removePreviewCommandCount = browserCommandCount();
  const removePreviewStartedAt = Date.now();
  await clickNative(page, '[data-testid^="construction-remove-step-"]');
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'), 5000);
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
  report.__nativeFailure = true;
  await captureFailure(error, { phase: report.activeAction?.label ?? report.cases.at(-1)?.name,
    elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : undefined,
    action: report.activeAction, requestEvidence: report.nativeRequests.slice(-20), latestCase: report.cases.at(-1) });
  await drainResponses();
if (page) report.failureDOM = await inspectPage(page, () => {
return (()=>{const p=document.querySelector('[data-testid="construction-reshape-pivot"]');const describe=selector=>{const select=document.querySelector(selector);return {selector,selected:select?.selectedOptions[0]?.textContent?.trim(),options:[...(select?.options??[])].map(option=>({label:option.textContent.trim(),value:option.value,disabled:option.disabled}))};};return {pivotVisible:Boolean(p),groupCheckboxes:[...(p?.querySelectorAll('input[aria-label^="Pivot group "]')??[])].map(input=>({selector:'input[aria-label='+JSON.stringify(input.getAttribute('aria-label'))+']',label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled})),groupSource:describe('select[aria-label="Add pivot group field"]'),category:describe('select[aria-label="Pivot category field"]'),value:describe('select[aria-label="Pivot values field"]'),proposal:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText};})()
}).catch(captureError => ({ captureError: String(captureError) }));
} finally {
  await drainResponses();
  try {
    report.sourceFreeze = await sourceFreeze.assertUnchanged();
    report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
    report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === report.sourceFingerprint.after.sha256
      && report.sourceFingerprint.before.files === report.sourceFingerprint.after.files;
    if (!report.sourceFingerprint.unchanged) throw Object.assign(new Error('Source fingerprint changed during verification.'), { invalidatesRun: true });
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false };
    report.__nativeFailure = true;
  }
  try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
  catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.apiBuildFreeze = { checked: true, unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
    report.__nativeFailure = true;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-pivot-field-change-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-pivot-field-change-browser.mjs', report);
  return report;
}
