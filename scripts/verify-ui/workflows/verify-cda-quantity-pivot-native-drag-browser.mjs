import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { expect } from '../helpers/cda-fixtures.mjs';

import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { collectPreviewRows } from '../helpers/playwright-preview-rows.mjs';
import { runCDAQuantityCategoryOracle } from '../helpers/cda-quantity-category-oracle-runner.mjs';
import { compareCDAQuantityCategoryValues } from '../helpers/cda-quantity-category-oracle.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';
import { assertOwnedCdaTarget } from '../helpers/owned-cda-target.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { discoverCompleteRelatedQuantityRouteInProcess } from '../helpers/related-quantity-pivot-discovery-process.mjs';
import { loadRelatedQuantityPivotDiscoveryArtifact } from '../helpers/related-quantity-pivot-discovery-artifact.mjs';
import {
  buildRelatedQuantityOracleExecuteScript,
  expectedTextOnlyQuantityPivotRows,
  summarizeRelatedQuantityPivotDiscoveryResult,
} from '../helpers/related-quantity-pivot-oracle.mjs';
import {
  actionToRenderBudgetMsForReport,
  DEFAULT_ACTION_TO_RENDER_BUDGET_MS,
  recordPivotActionToRender,
  waitForPivotObservable,
} from '../helpers/quantity-pivot-budget.mjs';

export const constructionTableReady = ({ outputId }) =>
  Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`));

export const pivotGroupSourceApplied = ({ selector, sourceValue, previousLabels }) => {
  const picker = document.querySelector(selector);
  const checked = [...document.querySelectorAll('input[aria-label^="Pivot group "]:checked')];
  const added = checked.filter(input => !previousLabels.includes(input.getAttribute('aria-label')));
  return picker?.value === ''
    && ![...picker.options].some(option => option.value === sourceValue)
    && added.length === 1;
};

export const pivotFieldSourceApplied = ({ selector, sourceValue }) => {
  const picker = document.querySelector(selector);
  const selected = picker?.selectedOptions?.[0];
  return Boolean(picker?.value && picker.value !== sourceValue && !picker.value.startsWith('source:')
    && selected?.value === picker.value
    && ![...picker.options].some(option => option.value === sourceValue));
};

export async function selectPivotSourceChoice({ page, label, path, value, role, selectOption, waitFor }) {
  assert(['group', 'category', 'value'].includes(role), `Unsupported Pivot source role ${role}`);
  const selector = `select[aria-label=${JSON.stringify(label)}]`;
  const previousLabels = role === 'group'
    ? await page.locator('input[aria-label^="Pivot group "]:checked').evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')))
    : [];
  await selectOption(selector, value);
  const predicate = role === 'group' ? pivotGroupSourceApplied : pivotFieldSourceApplied;
  await waitFor(predicate, { selector, sourceValue: value, previousLabels }, 5000);

  if (role === 'group') {
    const checked = await page.locator('input[aria-label^="Pivot group "]:checked').evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')));
    const added = checked.filter(inputLabel => !previousLabels.includes(inputLabel));
    assert.equal(added.length, 1, 'Adding a Pivot group source must check exactly one new group field');
    assert(added[0].includes(path), `New Pivot group member ${added[0]} must identify ${path}`);
    assert.equal(await page.locator(selector).inputValue(), '', 'The Add pivot group field picker resets after adding its controlled source');
    return { role, addedGroupLabel: added[0], pickerValue: '' };
  }

  const selected = await page.locator(selector).evaluate((picker, sourceValue) => ({
    columnId: picker.value,
    label: picker.selectedOptions[0]?.textContent?.trim() ?? '',
    sourceOptionPresent: [...picker.options].some(option => option.value === sourceValue),
  }), value);
  assert(selected.columnId && !selected.columnId.startsWith('source:'), `${role} source must remain selected by its generated column ID`);
  assert.equal(selected.sourceOptionPresent, false, `${role} source choice must be consumed when bound as a Pivot column`);
  assert(selected.label, `${role} must retain a selected output column after the source choice rerender`);
  assert(selected.label.includes(path), `${role} output field ${selected.label} must identify ${path}`);
  return { role, ...selected };
}

export function selectSavedPreviewReconcile(authoringRequests, previewRequest, state, outputId) {
  assert.equal(previewRequest.endpoint, 'preview');
  assert.equal(previewRequest.status, 200);
  assert.equal(previewRequest.requestOutputId, outputId);
  assert.equal(previewRequest.body?.outputId, outputId);
  assert.equal(previewRequest.responseOutputId, outputId);
  assert.equal(previewRequest.response?.outputId, outputId);
  assert.equal(previewRequest.requestReceiptId, previewRequest.body?.receiptId, 'Saved preview request receipt must match its captured body');
  assert.equal(previewRequest.responseReceiptId, previewRequest.requestReceiptId, 'Saved preview response receipt must match its request receipt');
  assert.equal(previewRequest.response?.receiptId, previewRequest.requestReceiptId, 'Saved preview response body must echo its request receipt');
  assert.equal(previewRequest.reconciledDraftVersion, state.draftVersion);
  assert.equal(previewRequest.reconciledDraftDigest, state.draftDigest);

  const matches = authoringRequests.filter(request => request.endpoint === 'reconcile'
    && request.status === 200
    && request.requestId === previewRequest.reconcileRequestId
    && request.requestDraftVersion === state.draftVersion
    && request.requestDraftVersion === previewRequest.reconciledDraftVersion
    && request.requestDraftDigest === state.draftDigest
    && request.requestDraftDigest === previewRequest.reconciledDraftDigest
    && request.body?.draftVersion === state.draftVersion
    && request.body?.draftDigest === state.draftDigest
    && request.body?.snapshotToken === state.catalog.snapshotToken
    && request.responseReceiptId === previewRequest.requestReceiptId
    && request.response?.receiptId === previewRequest.requestReceiptId
    && request.response?.builder?.documents?.some(document => document.output?.id === outputId)
    && Number.isFinite(request.requestStartedAtMs)
    && Number.isFinite(request.responseFinishedAtMs)
    && Number.isFinite(previewRequest.requestStartedAtMs)
    && Number.isFinite(previewRequest.responseFinishedAtMs)
    && request.requestStartedAtMs <= previewRequest.responseFinishedAtMs
    && request.responseFinishedAtMs <= previewRequest.responseFinishedAtMs);
  assert.equal(matches.length, 1, `Saved preview must resolve to exactly one successful reconcile for its receipt, draft, snapshot, output, and time scope (found ${matches.length})`);
  return matches[0];
}

export async function runQuantityPivotNativeDragBrowserWorkflow({ page, cda }, originalArgs = {}) {
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
const textOnly = originalArgs.textOnly === true;
assert(!textOnly || originalArgs.fullPopulation === true, 'Text-only quantity lifecycle requires the complete source population');
const retainedDiscoveryConfig = originalArgs.discoveryArtifactConfig;
const retainedDiscoveryConfigFields = [
  'artifactPath', 'artifactSha256', 'sourceCapturePath', 'sourceCaptureSha256',
];
let useRetainedDiscoveryArtifact = false;
if (retainedDiscoveryConfig !== undefined) {
  assert(retainedDiscoveryConfig && typeof retainedDiscoveryConfig === 'object' && !Array.isArray(retainedDiscoveryConfig),
    'Quantity discovery artifact config must be an object');
  assert(Object.keys(retainedDiscoveryConfig).every(field => retainedDiscoveryConfigFields.includes(field)),
    'Quantity discovery artifact config contains an unsupported field');
  const configuredFields = retainedDiscoveryConfigFields.filter(field => retainedDiscoveryConfig[field] !== undefined);
  assert(configuredFields.length === 0 || configuredFields.length === retainedDiscoveryConfigFields.length,
    'Retained quantity discovery requires all four artifact and source-capture inputs');
  if (configuredFields.length > 0) {
    for (const field of ['artifactPath', 'sourceCapturePath']) {
      assert(typeof retainedDiscoveryConfig[field] === 'string' && retainedDiscoveryConfig[field].trim() !== '',
        `Retained quantity discovery ${field} must be a non-empty path`);
    }
    for (const field of ['artifactSha256', 'sourceCaptureSha256']) {
      assert(typeof retainedDiscoveryConfig[field] === 'string' && /^[a-f0-9]{64}$/.test(retainedDiscoveryConfig[field]),
        `Retained quantity discovery ${field} must be a 64-character lowercase SHA-256 hex digest`);
    }
    useRetainedDiscoveryArtifact = true;
  }
}
const registeredActionToRenderBudgetMs = actionToRenderBudgetMsForReport(cda.report);
let activePivotActionToRenderBudgetMs = DEFAULT_ACTION_TO_RENDER_BUDGET_MS;
const project = cda.project;
const explorer = cda.explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const sourceRoot = fileURLToPath(new URL('../../..', import.meta.url));
assert((cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), 'Set LOOM_ARANGO_DATABASE for the isolated CDA source database.');
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer: (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER),
  composeProject: (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT), sourceRoot, arangoContainer: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER),
  clickhouseContainer: (cda.target.clickhouseContainer ?? cda.env?.LOOM_CLICKHOUSE_CONTAINER) });
const report = { explorer, cases: [], assertions: [], errors: [], requests: [], authoringRequests: [], nativeRequests: [], started: new Date().toISOString() };
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
const inspectPage = (_page, inspect, argument) => cda.inspect(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const requestedTimeout = typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument;
  const timeout = activePivotActionToRenderBudgetMs > DEFAULT_ACTION_TO_RENDER_BUDGET_MS
    ? activePivotActionToRenderBudgetMs
    : requestedTimeout;
  return waitForPivotObservable({ page, fallbackWait: cda.wait, predicate, argument: argument ?? {},
    timeoutMs: timeout, budgetMs: activePivotActionToRenderBudgetMs });
};
const gotoPage = (_page, url) => cda.navigate(url);
const clickNative = (_page, selector, identity = {}) => cda.click(selector, identity, 5000);
const selectNative = async (_page, selector, value) => {
  const locator = page.locator(selector);
  await cda.selectOption(selector, value);
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
let browserRequestCapture;
let pendingNativePresentationResponse;
const syncAuthoringRequests = () => {
  const endpoints = new Set(['commands', 'reconcile', 'preview', 'construction-proposals', 'construction-category-discoveries']);
  report.authoringRequests.splice(0, report.authoringRequests.length, ...report.nativeRequests
    .filter(request => endpoints.has(request.path.split('/').at(-1)))
    .map(request => ({ ...request, endpoint: request.path.split('/').at(-1), url: `${request.origin}${request.path}`, requestStartedAtMs: request.startedAt,
      responseFinishedAtMs: request.completedAt, durationMs: request.completedAt - request.startedAt,
      requestDraftVersion: request.body?.expectedDraftVersion ?? request.body?.draftVersion, requestDraftDigest: request.body?.expectedDraftDigest ?? request.body?.draftDigest,
      requestOutputId: request.body?.outputId, requestReceiptId: request.body?.receiptId,
      responseDraftVersion: request.response?.draftVersion, responseDraftDigest: request.response?.draftDigest,
      responseReceiptId: request.response?.receiptId, responseOutputId: request.response?.outputId,
      responseRowCount: request.response?.rowCount ?? request.response?.preview?.rowCount ?? request.response?.rows?.length,
      ...(request.failure ? { loadingFailure: { errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' } } : {}),
    })));
  report.browserCommands = report.authoringRequests.filter(request => request.endpoint === 'commands').map(request => ({
    requestId: request.requestId, body: request.body, status: request.status, response: request.response,
    requestStartedAtMs: request.requestStartedAtMs, responseFinishedAtMs: request.responseFinishedAtMs,
  }));
  correlateAuthoringRequests();
};
await mkdir(evidence, { recursive: true });
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('../../..', import.meta.url)));
const apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp((cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER)));
report.sourceFingerprint = { before: sourceFingerprint(sourceRoot) };
report.apiBuildIdentity = apiBuildFreeze.initial;
let builder, outputId;
const correlateAuthoringRequests = () => {
  for (const reconcile of report.authoringRequests.filter(request => request.endpoint === 'reconcile' && request.responseReceiptId)) {
    const command = [...report.authoringRequests].reverse().find(request =>
      request.endpoint === 'commands' &&
      request.responseDraftVersion === reconcile.requestDraftVersion &&
      request.responseDraftDigest === reconcile.requestDraftDigest &&
      request.requestStartedAtMs <= reconcile.requestStartedAtMs,
    );
    if (command) reconcile.commandRequestId = command.requestId;
    for (const preview of report.authoringRequests.filter(request =>
      request.endpoint === 'preview' && request.requestReceiptId === reconcile.responseReceiptId,
    )) {
      preview.reconcileRequestId = reconcile.requestId;
      preview.reconciledDraftVersion = reconcile.requestDraftVersion;
      preview.reconciledDraftDigest = reconcile.requestDraftDigest;
      if (reconcile.commandRequestId) preview.commandRequestId = reconcile.commandRequestId;
    }
  }
};
const drainPendingResponseReads = async () => {
  await browserRequestCapture?.flush();
  syncAuthoringRequests();
};
const captureFailureDOM = () => inspectPage(page, () => {
return (() => {
  const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
  const preview = document.querySelector('[data-testid="preview-table-scroll"]');
  const table = preview?.querySelector('[role="table"]');
  const rows = selector => [...document.querySelectorAll(selector)].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"], td')].map(cell => cell.innerText.trim()),
  );
  return {
    capturedAt: new Date().toISOString(),
    proposal: proposal ? {
      status: proposal.dataset.proposalStatus,
      proposalId: proposal.dataset.proposalId,
      text: proposal.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row =>
        [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()),
      ),
    } : undefined,
    savedPreview: preview ? {
      visible: preview.getClientRects().length > 0,
      ariaRowCount: table?.getAttribute('aria-rowcount'),
      headers: [...preview.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
      rows: rows('[data-testid="preview-table-scroll"] [role="row"]'),
      loading: document.body.innerText.includes('Loading your table…'),
    } : undefined,
    bodyText: document.body.innerText,
  };
})();
});
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `quantity-category-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const proposal = async (name, start, expectedRows) => {
  await waitForObservable(page, () => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await inspectPage(page, () => {
const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};
});
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const permitted = new Set(expectedRows.map(row=>JSON.stringify(row)));
  for (const row of result.rows) assert(permitted.has(JSON.stringify(row)), 'Preview row must match an independent CDA relationship witness: '+JSON.stringify(row));
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  return recordPivotActionToRender({ cases: report.cases, name, startedAt: start,
    budgetMs: activePivotActionToRenderBudgetMs });
};
const apply = async expectedRows => {
  const start = Date.now();
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await gotoPage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForObservable(page, constructionTableReady, { outputId });
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async (expectedRows, rowLimit = 25) => {
  await waitForObservable(page, ({ rowCount }) => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount + 1) && !document.body.innerText.includes('Loading your table…'), { rowCount: Math.min(rowLimit, expectedRows.length) });
  const rows = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);
});
  assert(rows.length > 0 || expectedRows.length === 0);
  for (const row of rows) assert(expectedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
const setPreviewLimit = async limit => {
  await selectNative(page, 'select[aria-label="Preview row limit"]', String(limit));
};
const typedCategoryIdentity = key => JSON.stringify(key);
const outputColumnValues = columns => columns.map(column => ({
  column: column.column,
  label: column.label,
  logicalType: column.logicalType,
}));
const proposalPreviewFor = async proposalRequest => {
  assert(proposalRequest?.response?.proposalId, 'Pivot proposal must return a proposal ID');
  const preview=proposalRequest.response.preview;
  assert(preview, 'Native proposal must embed its table preview');
  assert.equal(preview.receiptId,proposalRequest.response.proposalId,'Proposal preview must be bound to this exact proposal');
  assert.equal(preview.outputId,outputId);
  return preview;
};
const expectedPivotFor = (proposalRequest, preview, expectedDuplicatePolicy = 'ERROR') => {
  const step = proposalRequest.response.candidateConstruction.steps.find(candidate => candidate.operation.kind === 'PIVOT');
  assert(step, 'Native proposal must contain a PIVOT construction step');
  const operation = step.operation.pivot;
  const groupOutputs = operation.groupKeyIds.map(id => {
    const output = step.outputs.find(column => column.id === id);
    assert(output, `Pivot group output ${id} must be present`);
    return output;
  });
  const categoryOutputs = operation.categories.map(category => {
    const output = step.outputs.find(column => column.id === category.outputColumnId);
    assert(output, `Pivot category output ${category.outputColumnId} must be present`);
    return { key: category.key, output };
  });
  const groupIndex = output => {
    const label = output.label.toLowerCase();
    if (label.includes('specimen id')) return 0;
    if (label.includes('patient fhir resource id')) return 1;
    if (label.includes('observation fhir resource id')) return 2;
    if (label.includes('observation.valuecodeableconcept.text')) return 3;
    assert.fail(`Unexpected Pivot row identity field ${output.label}`);
  };
  assert.equal(groupOutputs.length, 4, 'Pivot must retain the discovery text key and the three independently witnessed row IDs');
  const buckets = new Map();
  for (const observation of report.oracle.observationRows) {
    const groupValues = groupOutputs.map(output => groupIndex(output) === 3 ? observation.text : observation.rowIds[groupIndex(output)]);
    const groupIdentity = JSON.stringify(groupValues);
    let bucket = buckets.get(groupIdentity);
    if (!bucket) {
      bucket = { groupValues, values: new Map(), categoryCounts: new Map(), sourceRows: 0 };
      buckets.set(groupIdentity, bucket);
    }
    bucket.sourceRows += 1;
    const quantity = observation.quantity;
    assert(quantity?.code == null || typeof quantity.code === 'string', 'CDA quantity code values must be strings or null');
    const key = quantity?.code == null
      ? { kind: 'NULL' }
      : { kind: 'STRING', string: quantity.code };
    const category = categoryOutputs.find(candidate => typedCategoryIdentity(candidate.key) === typedCategoryIdentity(key));
    assert(category, `Independent Observation value maps to undiscovered category ${JSON.stringify(key)}`);
    bucket.categoryCounts.set(category.output.name, (bucket.categoryCounts.get(category.output.name) ?? 0) + 1);
    if (typeof quantity?.value === 'number' && Number.isFinite(quantity.value)) {
      const current = bucket.values.get(category.output.name) ?? { count: 0, value: 0 };
      current.count += 1;
      current.value = expectedDuplicatePolicy === 'SUM' ? current.value + quantity.value : quantity.value;
      bucket.values.set(category.output.name, current);
    }
  }
  const duplicateBuckets = [...buckets.values()].reduce((count, bucket) => count + [...bucket.categoryCounts.values()].filter(value => value > 1).length, 0);
  assert.equal(duplicateBuckets, 0, 'The independently witnessed row IDs leave no duplicate group/category inputs');
  assert.equal(operation.duplicatePolicy, expectedDuplicatePolicy, `Native Pivot must use the requested ${expectedDuplicatePolicy} duplicate policy`);
  const previewColumns = outputColumnValues(preview.columns);
  const expectedRows = [...buckets.values()].map(bucket => Object.fromEntries(previewColumns.map(column => {
    const group = groupOutputs.find(output => output.name === column.column);
    if (group) return [column.column, bucket.groupValues[groupOutputs.indexOf(group)]];
    const category = categoryOutputs.find(candidate => candidate.output.name === column.column);
    assert(category, `Preview column ${column.column} is not a Pivot output`);
    const value = bucket.values.get(category.output.name);
    return [column.column, value ? value.value : null];
  })));
  const protocolRows=preview.rows.map(row=>Object.fromEntries(previewColumns.map(column=>[column.column,row[column.column]])));
  const sortRows=rows=>rows.map(row=>JSON.stringify(previewColumns.map(column=>row[column.column]))).sort();
  assert.deepEqual(sortRows(protocolRows),sortRows(expectedRows),'Native proposal protocol values and types must match raw CDA witnesses');
  const identities=preview.rows.map(row=>row.__loom_row_id);
  assert(identities.every(id=>typeof id==='string'&&id.length>0),'Pivot protocol must retain every row identity');
  assert.equal(new Set(identities).size,identities.length,'Pivot row identities must be unique');
  return { step, operation, columns: previewColumns, rows: expectedRows, duplicateBuckets, sourceRowCount: report.oracle.observationRows.length };

};
const assertPersistedPivotIdentity = async (proposalPreview, name) => {
  await drainPendingResponseReads();
  const saved = report.authoringRequests.findLast(request =>
    request.endpoint === 'preview' && request.status === 200 &&
    request.response?.outputId === outputId && request.response?.rowCount === proposalPreview.rowCount);
  assert(saved, `${name}: the saved table must have a completed native preview`);
  const expectedIds = proposalPreview.rows.map(row => row.__loom_row_id).sort();
  const savedIds = saved.response.rows.map(row => row.__loom_row_id).sort();
  assert(expectedIds.every(id => typeof id === 'string' && id.length > 0), `${name}: proposal identities must be nonempty`);
  assert.equal(new Set(savedIds).size, savedIds.length, `${name}: saved identities must be unique`);
  assert.deepEqual(savedIds, expectedIds, `${name}: Apply and reload must preserve the proposed row identities`);
  report.cases.push({name, rowCount:savedIds.length, receiptId:saved.response.receiptId});
};

const assertExactRows = async ({ name, columns, rows, rowLimit = 1000, panel = false }) => {
  const start = Date.now();
  const prefix = panel ? '[data-testid="construction-proposal-preview"]' : '[data-testid="preview-table-scroll"]';
  const rowSelector = panel ? '[data-testid="construction-proposal-preview-row"]' : '[role="row"]';
  const cellSelector = panel ? 'td' : '[role="cell"]';
  if (!panel) await setPreviewLimit(rowLimit, rows.length);
  const ready = panel
    ? ({ prefix, rowSelector, rowCount }) => Boolean(document.querySelector(`${prefix} table`)) && document.querySelectorAll(`${prefix} ${rowSelector}`).length === rowCount
    : ({ prefix, rowCount }) => document.querySelector(`${prefix} [role="table"]`)?.getAttribute('aria-rowcount') === String(rowCount + 1) && !document.body.innerText.includes('Loading your table…');
  await waitForObservable(page, ready, { prefix, rowSelector, rowCount: Math.min(rowLimit, rows.length) }, 10000);
  const actual = panel
    ? await inspectPage(page, ({ prefix, rowSelector, cellSelector }) => { const root=document.querySelector(prefix); return {headers:[...root.querySelectorAll('th,[role="columnheader"]')].map(cell=>cell.innerText.trim().split('\n')[0]),rows:[...root.querySelectorAll(rowSelector)].map(row=>[...row.querySelectorAll(cellSelector)].map(cell=>cell.innerText.trim()))}; }, { prefix, rowSelector, cellSelector })
    : await collectPreviewRows(page, { containerSelector: prefix, tableSelector: '[role="table"]', rowSelector, cellSelector });
  if (!panel) actual.rows = actual.rows.map(row => row.values);
  assert.deepEqual(actual.headers.map(header => header.toLowerCase()), columns.map(column => column.label.trim().toLowerCase()), `${name} column labels must match the native Pivot preview`);
  const expectedCells = rows.map(row => columns.map(column => {
    const value = row[column.column];
    return value === null || value === undefined ? '—' : String(value);
  }));
  assert.equal(actual.rows.length, Math.min(rowLimit, rows.length), `${name} must render every row allowed by the selected preview limit`);
  assert.deepEqual(actual.rows.map(row => JSON.stringify(row)).sort(), expectedCells.map(row => JSON.stringify(row)).sort(), `${name} must match every independent CDA row and value`);
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} exact row check took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: rows.length, visibleRowCount: actual.rows.length, columns });
  return actual;
};
const nativeDragColumnBefore = async (sourceLabel, targetLabel) => {
  const list = '[role="list"][aria-label="Table columns"]';
  const sourceHandle = `${list} span[aria-label=${JSON.stringify(`Drag ${sourceLabel}`)}]`;
  const targetHandle = `${list} span[aria-label=${JSON.stringify(`Drag ${targetLabel}`)}]`;
  await clickNative(page, 'button', { name: 'Columns' });
  await waitForObservable(page, ({ sourceHandle, targetHandle }) => Boolean(document.querySelector(sourceHandle) && document.querySelector(targetHandle)), { sourceHandle, targetHandle });
  const readGeometry = () => inspectPage(page, ({ listSelector, sourceHandle, targetHandle }) => (()=>{
    const list=document.querySelector(listSelector);
    const source=document.querySelector(sourceHandle);
    const target=document.querySelector(targetHandle);
    const row=target?.closest('[role="listitem"]');
    if(!list||!source||!target||!row)return null;
    const inspect=()=>{
      const sourceRect=source.getBoundingClientRect();
      const targetRect=row.getBoundingClientRect();
      const sourcePoint={x:sourceRect.left+sourceRect.width/2,y:sourceRect.top+sourceRect.height/2};
      const targetPoint={x:targetRect.left+targetRect.width/2,y:targetRect.top+Math.max(2,targetRect.height*0.2)};
      const onScreen=point=>point.x>=0&&point.y>=0&&point.x<innerWidth&&point.y<innerHeight;
      const sourceHit=document.elementFromPoint(sourcePoint.x,sourcePoint.y);
      const targetHit=document.elementFromPoint(targetPoint.x,targetPoint.y);
      return {
        viewport:{width:innerWidth,height:innerHeight,scrollX,scrollY},
        source:{x:sourcePoint.x,y:sourcePoint.y,rect:{left:sourceRect.left,top:sourceRect.top,width:sourceRect.width,height:sourceRect.height},onScreen:onScreen(sourcePoint),hit:sourceHit?{tag:sourceHit.tagName,ariaLabel:sourceHit.getAttribute('aria-label'),inside:source.contains(sourceHit)}:null},
        target:{x:targetPoint.x,y:targetPoint.y,rect:{left:targetRect.left,top:targetRect.top,width:targetRect.width,height:targetRect.height},onScreen:onScreen(targetPoint),hit:targetHit?{tag:targetHit.tagName,ariaLabel:targetHit.getAttribute('aria-label'),inside:row.contains(targetHit)}:null},
      };
    };
    return {
      labels:[...list.querySelectorAll('[role="listitem"] span[aria-label^="Drag "]')].map(handle=>handle.getAttribute('aria-label').slice(5)),
      draggable:source.draggable,
      ...inspect(),
    };
  })(), { listSelector: list, sourceHandle, targetHandle });
  const initial = await readGeometry();
  const sourceLocator = page.locator(sourceHandle);
  const targetLocator = page.locator(targetHandle);
  await expect(sourceLocator, `Pivot drag source ${sourceLabel} must be unique`).toHaveCount(1, { timeout: 5000 });
  await expect(targetLocator, `Pivot drop target ${targetLabel} must be unique`).toHaveCount(1, { timeout: 5000 });
  await sourceLocator.scrollIntoViewIfNeeded();
  await targetLocator.scrollIntoViewIfNeeded();
  const geometry = await readGeometry();
  geometry.initial = initial;
  assert(geometry?.draggable, `Pivot output ${sourceLabel} must expose a native draggable handle`);
  report.nativeDragInitialClipping = {
    viewport: geometry.initial?.viewport,
    source: geometry.initial?.source,
    target: geometry.initial?.target,
    sourceClipped: !geometry.initial?.source?.onScreen || !geometry.initial?.source?.hit?.inside,
    targetClipped: !geometry.initial?.target?.onScreen || !geometry.initial?.target?.hit?.inside,
  };
  assert(geometry.source.onScreen && geometry.source.hit?.inside,
    `Native Pivot drag source must be visible and hit-testable after scrolling: ${JSON.stringify(geometry)}`);
  assert(geometry.target.onScreen && geometry.target.hit?.inside,
    `Native Pivot drop target must be visible and hit-testable after scrolling: ${JSON.stringify(geometry)}`);
  report.nativeDragGeometry = geometry;
  report.nativeDragInputPath = { steps: 32, input: 'Playwright mouse',
    source: geometry.source, target: geometry.target };
  await inspectPage(page, () => {
window.__loomNativeDragEvents=[];for(const type of ['dragstart','dragover','drop','dragend']) document.addEventListener(type,event=>{const row=event.target?.closest?.('[role="listitem"]');const list=row?.parentElement;const rect=row?.getBoundingClientRect();window.__loomNativeDragEvents.push({type,isTrusted:event.isTrusted,target:{tag:event.target?.tagName,ariaLabel:event.target?.getAttribute?.('aria-label'),role:event.target?.getAttribute?.('role')},clientX:event.clientX,clientY:event.clientY,rowIndex:row?[...list.querySelectorAll('[role="listitem"]')].indexOf(row):-1,targetRowRect:rect?{top:rect.top,height:rect.height,bottom:rect.bottom}:null,defaultPrevented:event.defaultPrevented})},{capture:true});
});
  pendingNativePresentationResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    let body;
    try { body = request.postDataJSON(); } catch { return false; }
    return url.pathname === `${base}/commands` && body?.commands?.some(command =>
      command.type === 'UPDATE_CONSTRUCTION_OUTPUT' || command.type === 'UPDATE_COLUMN');
  }, { timeout: 5000 });
  pendingNativePresentationResponse.catch(() => undefined);
  await cda.action(`drag Pivot output ${sourceLabel}`, page.locator(sourceHandle), async () => {
    await page.mouse.move(geometry.source.x, geometry.source.y);
    await page.mouse.down();
    await page.mouse.move(geometry.target.x, geometry.target.y, { steps: 32 });
    await page.mouse.up();
  }, { timeout: 5000, budget: 5000 });
  await waitForObservable(page,
    () => Boolean(window.__loomNativeDragEvents?.some(event=>event.type==='drop'&&event.isTrusted)), 5000);
  const events = await inspectPage(page, () => window.__loomNativeDragEvents);
  const dragStart = events.find(event => event.type === 'dragstart' && event.isTrusted);
  const drop = events.findLast(event => event.type === 'drop');
  assert(dragStart, `Native Pivot drag must start with a trusted dragstart: ${JSON.stringify(events)}`);
  assert(drop?.isTrusted, `Native Pivot drag must complete with a trusted drop: ${JSON.stringify(events)}`);
  assert.equal(drop.rowIndex, 0, `Native Pivot drop must target the first row for ${targetLabel}: ${JSON.stringify(drop)}`);
  assert(drop.targetRowRect && drop.clientY >= drop.targetRowRect.top
    && drop.clientY < drop.targetRowRect.top + drop.targetRowRect.height / 2,
  `Native Pivot drop must land in the upper half of the freshly measured target row: ${JSON.stringify(drop)}`);
  report.nativeDragEvents = events;
  return geometry;
};
const waitForNativePresentationCommand = async afterCount => {
  assert(pendingNativePresentationResponse, 'The native Pivot drag must register its observable command response before dragging.');
  const response = await pendingNativePresentationResponse;
  await drainPendingResponseReads();
  const tracked = browserRequestCapture.byRequest.get(response.request());
  assert(tracked, 'Playwright must retain the native Pivot order command by Request identity.');
  const command = report.browserCommands.slice(afterCount).findLast(entry => entry.requestId === tracked.requestId);
  assert(command, 'Native Pivot drag must save an UPDATE_CONSTRUCTION_OUTPUT / UPDATE_COLUMN command.');
  assert.equal(command.status, 200, `Native Pivot order command failed: ${JSON.stringify(command)}`);
  assert(command.response, 'Native Pivot order command response must be retained.');
  pendingNativePresentationResponse = undefined;
  return command;
};
const assertSavedPivotOutputOrder = (state, columns, name) => {
  const document = doc(state);
  const step = document.construction.steps.find(candidate => candidate.operation.kind === 'PIVOT');
  assert(step, `${name}: saved Pivot step must remain in the terminal construction`);
  const expectedNames = columns.map(column => column.column);
  const outputs = step.outputs.filter(output => expectedNames.includes(output.name));
  assert.equal(outputs.length, expectedNames.length, `${name}: every generated Pivot output must remain saved`);
  assert(outputs.every(output => Number.isInteger(output.table?.order)),
    `${name}: saved generated outputs must carry explicit table order: ${JSON.stringify(outputs)}`);
  const actualNames = outputs.sort((left, right) => left.table.order - right.table.order).map(output => output.name);
  assert.deepEqual(actualNames, expectedNames, `${name}: saved terminal output order must match the native drag result`);
  const source = document.columns.find(column => column.label === 'Specimen ID');
  assert.deepEqual(source, report.pivotSourceColumnBeforeDrag,
    `${name}: terminal output reordering must preserve the independently normalized source column and its binding`);
  return { labels: outputs.map(output => output.label), names: actualNames };
};
  const rawQuery = (query, bindVars = {}, phase = 'independent-route-witness-query') => {
    const identity = { phase, sha256: createHash('sha256').update(query).digest('hex'),
      maxRuntimeSeconds: 30, memoryLimit: 268435456, startedAt: Date.now() };
    report.oraclePhase = phase;
    report.oracleQueryIdentity = identity;
    report.rawOracleQueries ??= [];
    report.rawOracleQueries.push(identity);
    try {
      const resultMarker = '__LOOM_RELATED_QUANTITY_ORACLE__';
      const script = buildRelatedQuantityOracleExecuteScript(query, bindVars, resultMarker);
      const invocation = buildArangoShellInvocation({ container: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER), script, database: (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE) });
      const result = spawnSync(invocation.command, invocation.args, { encoding: 'utf8', timeout: 35000, maxBuffer: 16 * 1024 * 1024 });
      identity.exitCode = result.status;
      identity.signal = result.signal;
      assert.equal(result.status, 0, result.stderr || result.stdout);
      const line = result.stdout.split('\n').find(value => value.startsWith(resultMarker));
      assert(line, `Arangosh returned no related quantity result: ${result.stdout.slice(-2000)}`);
      const payload = JSON.parse(line.slice(resultMarker.length));
      identity.errorNum = payload.errorNum;
      assert.equal(payload.ok, true, `Related quantity query failed: ${payload.message ?? 'unknown error'}`);
      assert(Array.isArray(payload.rows) && payload.rows.every(row => row !== null && typeof row === 'object' && !Array.isArray(row)),
        'Independent raw queries must return an array of projected result objects');
      identity.status = 'passed';
      identity.resultRows = payload.rows.length;
      return payload.rows;
    } catch (error) {
      identity.status = 'failed';
      identity.error = String(error.message ?? error);
      error.verificationFailureCategory = 'harness-oracle';
      throw error;
    } finally {
      identity.durationMs = Date.now() - identity.startedAt;
    }
  };
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const [source] = rawQuery(query, {}, 'independent-source-seed-query');
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Related Pivot composition QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Pivot QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  const desktopViewport = { width: 1280, height: 1000, deviceScaleFactor: 1, mobile: false };
  await page.setViewportSize({ width: desktopViewport.width, height: desktopViewport.height });
  report.browserViewport = {
    requested: desktopViewport,
    observed: await inspectPage(page, () => ({width:innerWidth,height:innerHeight,deviceScaleFactor:devicePixelRatio})),
  };
  browserRequestCapture = captureCDARequests(page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base,
    report,
    responsePaths: /\/(?:commands|reconcile|preview|construction-proposals|construction-category-discoveries|construction-capabilities)$/,
  });
  page.on('request', request => {
    const captured = browserRequestCapture.byRequest.get(request);
    if (captured) captured.observedAfter = report.cases.at(-1)?.name;
  });
  let witnesses=[{anchor:source._id,values:[source.id]}];
  let expected=witnesses.map(w=>w.values);
  await open(expected);
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
    {from:'Patient',to:'Observation',label:'subject_Patient',field:'subject',direction:'INBOUND'},
  ];
  for(const hop of chain){
    const next=[];
    for(const witness of witnesses){
      const endpoint=hop.direction==='OUTBOUND'?'_from':'_to';
      const target=hop.direction==='OUTBOUND'?'_to':'_from';
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches=witness.anchor?rawQuery(query):[];
      if(matches.length)for(const match of matches)next.push({anchor:match._id,values:[...witness.values,match.id]});
      else next.push({anchor:null,values:[...witness.values,'—']});
    }
    assert(next.length<=1000,'Use a bounded CDA chain fixture');
    witnesses=next;expected=witnesses.map(w=>w.values);
    report.oracle.chain??=[];report.oracle.chain.push({hop,witnesses});
    await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await clickNative(page,'[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForObservable(page, ({ panel }) => document.querySelector(`${panel} select[aria-label="Related record type"]`)?.disabled === false, { panel });
    let start=Date.now();
    await selectNative(page,panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForObservable(page, ({ panel, label }) => Boolean(document.querySelector(`${panel} input[aria-label="${label}"]`)), { panel, label }, 5000);
    await clickNative(page,panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const fullPopulation=originalArgs.fullPopulation === true || (cda.env?.LOOM_QUANTITY_FULLPOP ?? process.env.LOOM_QUANTITY_FULLPOP)==='1';
  await drainPendingResponseReads();
  const fullPopulationRequestIndex = report.authoringRequests.length;
  const fullPopulationStartedAt = Date.now();
  if(fullPopulation){
    await command([{type:'CLEAR_TABLE_POPULATION',outputId}]);
    const loadStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, constructionTableReady, { outputId });
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    recordRender('full-population-editor-ready',loadStart);
  }
  if (fullPopulation) builder = await api(base + '/builder');
  if (textOnly) {
    activePivotActionToRenderBudgetMs = registeredActionToRenderBudgetMs;
    const required = cda.report.requiredChecks;
    assert.equal(required.length, 12, 'Use the registered complete lifecycle contract');
    const mark = (index, evidence) => {
      cda.check('row-lifecycle', required[index], true, evidence);
      report.assertions.push({ name: required[index], status: 'passed', evidence });
    };
    report.scenario = 'root-quantity-pivot';
    report.case = 'related-text-only-full-population-lifecycle';
    const baseline = structuredClone(builder);
    assert.equal(doc(baseline).population ?? null, null, 'Clear selection must expose the complete authorized source population');
    const prefixSteps = doc(baseline).construction.steps;
    assert.equal(prefixSteps.filter(step => step.operation.kind === 'RELATED_EXPAND').length, 2);
    assert(prefixSteps.filter(step => step.operation.kind === 'RELATED_EXPAND')
      .every(step => step.operation.relatedExpand.emptyPolicy === 'PRESERVE_PARENT'));
    await drainPendingResponseReads();
    const savedPreviewFor = async (state, since) => {
      await drainPendingResponseReads();
      const request = report.authoringRequests.findLast(request => request.endpoint === 'preview'
        && request.status === 200 && request.response?.outputId === outputId
        && request.requestStartedAtMs >= since && request.reconciledDraftVersion === state.draftVersion
        && request.reconciledDraftDigest === state.draftDigest);
      assert(request, 'Saved preview must be fresh and bound to the exact current draft');
      selectSavedPreviewReconcile(report.authoringRequests, request, state, outputId);
      return request.response;
    };
    const baselinePreview = await savedPreviewFor(baseline, fullPopulationStartedAt);
    assert(report.authoringRequests.slice(fullPopulationRequestIndex).some(request => request.response === baselinePreview));
    assert(baselinePreview?.rows?.length, 'Full related source must render before reshaping');
    const scope = { project, dataset_generation: source.generation, scope_allowed: true,
      auth_resource_paths: [], auth_resource_paths_unrestricted: true,
      emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'] };
    let discoveryRun;
    try {
      const arangoContainer = cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER;
      const database = cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE;
      if (useRetainedDiscoveryArtifact) {
        discoveryRun = await loadRelatedQuantityPivotDiscoveryArtifact({
          ...retainedDiscoveryConfig,
          expectedScope: scope,
          expectedTarget: {
            project,
            generation: source.generation,
            arangoContainer,
            database,
            authScope: {
              auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
              auth_resource_paths: scope.auth_resource_paths,
              scope_allowed: scope.scope_allowed,
            },
          },
          expectedSourceRoot: sourceRoot,
        });
        report.relatedQuantityDiscoveryArtifact = discoveryRun.artifactProvenance;
      } else {
        discoveryRun = await discoverCompleteRelatedQuantityRouteInProcess({ scope, container: arangoContainer, database });
        report.relatedQuantityDiscoveryProcess = discoveryRun.processIdentity;
      }
    } catch (error) {
      if (useRetainedDiscoveryArtifact) {
        report.relatedQuantityDiscoveryArtifact = {
          status: 'failed',
          artifactPath: retainedDiscoveryConfig.artifactPath,
          artifactSha256: retainedDiscoveryConfig.artifactSha256,
          sourceCapturePath: retainedDiscoveryConfig.sourceCapturePath,
          sourceCaptureSha256: retainedDiscoveryConfig.sourceCaptureSha256,
          failure: String(error?.message ?? error).slice(0, 300),
        };
      } else {
        report.relatedQuantityDiscoveryProcess = error.discoveryIdentity ?? {
          status: 'failed',
          failure: String(error?.message ?? error).slice(0, 300),
        };
      }
      throw error;
    }
    const independentDiscovery = discoveryRun.discovery;
    assert.equal(independentDiscovery.complete, true, 'Independent raw route discovery must finish every bounded page and Patient batch');
    assert.equal(independentDiscovery.project, scope.project);
    assert.equal(independentDiscovery.generation, scope.dataset_generation);
    assert.deepEqual(independentDiscovery.authScope, {
      auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
      auth_resource_paths: scope.auth_resource_paths,
      scope_allowed: scope.scope_allowed,
    });
    report.fullPopulationDiscovery = independentDiscovery;
    report.fullPopulationDiscoveryRun = { ...discoveryRun, discovery: undefined };
    const sourceColumns = outputColumnValues(baselinePreview.columns);
    const sourceNames = ['Specimen ID', 'Patient FHIR resource ID', 'Observation FHIR resource ID']
      .map(label => {
        const column = sourceColumns.find(column => column.label === label);
        assert(column, `Full source preview requires ${label}`);
        return column.column;
      });
    const tuples = baselinePreview.rows.map(row => Object.fromEntries(
      ['specimen', 'patient', 'observation'].map((name, index) => [name, row[sourceNames[index]] ?? null])));
    assert(tuples.length <= 25, 'Default source preview must stay bounded');
    const tupleProof = rawQuery(`
      FOR tuple IN @tuples
        LET specimens = (FOR s IN Specimen FILTER s.project == @project
          AND s.dataset_generation == @generation AND s.resourceType == "Specimen"
          AND s.payload.resourceType == "Specimen" AND s.id == tuple.specimen LIMIT 2 RETURN s._id)
        LET patients = (FOR e IN fhir_edge FILTER e._from IN specimens
          AND e.project == @project AND e.dataset_generation == @generation
          AND e.label == "subject_Patient" AND e.from_type == "Specimen" AND e.to_type == "Patient"
          LET p = DOCUMENT(e._to) FILTER p != null AND p.project == @project
          AND p.dataset_generation == @generation AND p.resourceType == "Patient"
          AND p.payload.resourceType == "Patient"
          FILTER tuple.patient == null OR p.id == tuple.patient LIMIT 1 RETURN p._id)
        LET observations = (FOR e IN fhir_edge FILTER e._to IN patients
          AND e.project == @project AND e.dataset_generation == @generation
          AND e.label == "subject_Patient" AND e.from_type == "Observation" AND e.to_type == "Patient"
          LET o = DOCUMENT(e._from) FILTER o != null AND o.project == @project
          AND o.dataset_generation == @generation AND o.resourceType == "Observation"
          AND o.payload.resourceType == "Observation"
          FILTER tuple.observation == null OR o.id == tuple.observation LIMIT 1 RETURN o._id)
        RETURN { tuple, valid: LENGTH(specimens) == 1 AND
          (tuple.patient == null ? LENGTH(patients) == 0 AND tuple.observation == null :
            LENGTH(patients) == 1 AND (tuple.observation == null ? LENGTH(observations) == 0 : LENGTH(observations) == 1)) }
    `, { tuples, project, generation: source.generation }, 'independent-source-tuple-query');
    assert.equal(tupleProof.length, tuples.length);
    assert(tupleProof.every(result => result.valid), 'Every full source preview tuple must follow scoped typed edges and both empty policies');
    assert.equal(new Set(tuples.map(tuple => JSON.stringify(tuple))).size, tuples.length,
      'Both authored hops must deduplicate repeated edges per parent');
    report.sourceTupleOracle = { unrestrictedLocalAuthorization: true, tuples: tupleProof };
    const oracleCache = new Map();
    const sameSaved = async (state, name) => {
      const current = await api(base + '/builder');
      assert.equal(current.draftVersion, state.draftVersion, name);
      assert.equal(current.draftDigest, state.draftDigest, name);
      assert.deepEqual(current.workspace, state.workspace, name);
      assert.equal(current.catalog.snapshotToken, state.catalog.snapshotToken, name);
      return current;
    };
    const terminalProposal = async (index, start, name, previousId) => {
      await waitForObservable(page, ({ previousId }) => {
        const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
        return ['error', 'needs-repair'].includes(panel?.dataset.proposalStatus)
          || panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== previousId;
      }, { previousId });
      const status = await page.getByTestId('construction-proposal-panel').getAttribute('data-proposal-status');
      if (status !== 'ready') {
        await drainPendingResponseReads();
        assert.fail(JSON.stringify({ status, requests: report.authoringRequests.slice(index), dom: await captureFailureDOM() }));
      }
      await expect(page.getByTestId('construction-proposal-preview')).toBeVisible({ timeout: activePivotActionToRenderBudgetMs });
      recordRender(name, start);
      const proposalId = await page.getByTestId('construction-proposal-panel').getAttribute('data-proposal-id');
      await drainPendingResponseReads();
      const request = report.authoringRequests.slice(index).findLast(item => item.endpoint === 'construction-proposals'
        && item.response?.proposalId === proposalId);
      assert.equal(request?.status, 200, name);
      return { request, preview: await proposalPreviewFor(request) };
    };
    const choose = async (label, path) => {
      const options = await page.getByLabel(label, { exact: true }).locator('option').evaluateAll(
        elements => elements.map(element => ({ value: element.value, label: element.textContent })));
      const matches = options.filter(option => option.value.startsWith('source:') && option.label.includes(path));
      assert.equal(matches.length, 1, `One active source choice required for ${path}`);
      const role = ({
        'Add pivot group field': 'group',
        'Pivot category field': 'category',
        'Pivot values field': 'value',
      })[label];
      assert(role, `Unsupported Pivot source selector ${label}`);
      return selectPivotSourceChoice({
        page, label, path, value: matches[0].value, role,
        selectOption: (selector, value) => cda.selectOption(selector, value),
        waitFor: (predicate, args, timeout) => waitForObservable(page, predicate, args, timeout),
      });
    };
    const configure = async () => {
      await clickNative(page, '[data-testid="construction-rows-settings-trigger"]');
      await clickNative(page, '[data-testid="construction-action-pivot-rows"]');
      await waitForObservable(page, () => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
      for (const label of ['Specimen ID', 'Patient FHIR resource ID', 'Observation FHIR resource ID']) {
        const selector = `input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
        if (await page.locator(selector).isChecked()) await clickNative(page, selector);
      }
      await choose('Add pivot group field', 'Observation.valueCodeableConcept.text');
      await choose('Pivot category field', 'Observation.valueQuantity.code');
      await choose('Pivot values field', 'Observation.valueQuantity.value');
      const index = report.authoringRequests.length;
      const previousId = await inspectPage(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId);
      const start = Date.now();
      await selectNative(page, 'select[aria-label="Pivot duplicate policy"]', 'SUM');
      return terminalProposal(index, start, 'text-only-SUM-policy-change-to-render', previousId);
    };
    const checkPivot = async ({ request, preview }, policy) => {
      const step = request.response.candidateConstruction.steps.find(item => item.operation.kind === 'PIVOT');
      assert(step, 'Candidate must contain a Pivot');
      const operation = step.operation.pivot;
      assert.equal(operation.duplicatePolicy, policy);
      assert.equal(operation.groupKeyIds.length, 1, 'Text is the only row grouping field');
      const sourceBindings = new Map();
      assert.equal(request.body?.pivotSources?.length, 3, 'Every proposal must identify all three selected source inputs');
      for (const selection of request.body.pivotSources) {
        assert(!sourceBindings.has(selection.columnId), 'Source selections must have unique column identities');
        assert.equal(selection.choiceId.split('.')[0], 'cc2', 'Stage source choice must carry its versioned identity');
        const identity = JSON.parse(Buffer.from(selection.choiceId.split('.')[1], 'base64url').toString('utf8'));
        assert.equal(identity.source?.resourceType, 'Observation');
        assert.equal(identity.snapshotToken, baseline.catalog.snapshotToken);
        sourceBindings.set(selection.columnId, { choiceId: selection.choiceId, source: identity.source });
      }
      for (const [columnId, path, type] of [[operation.groupKeyIds[0], 'valueCodeableConcept.text', 'string'],
        [operation.categoryColumnId, 'valueQuantity.code', 'string'], [operation.valueColumnId, 'valueQuantity.value', 'decimal']]) {
        assert.equal(sourceBindings.get(columnId)?.source.path, path, 'Native Pivot must retain the selected Observation field binding');
        assert.equal(sourceBindings.get(columnId)?.source.logicalType, type);
      }
      const candidate = request.response.candidateConstruction;
      assert.deepEqual(candidate.steps.slice(0, prefixSteps.length), prefixSteps, 'Every pre-Pivot step must remain unchanged');
      const added = candidate.steps.slice(prefixSteps.length);
      assert.equal(added.length, 4, 'Compound Pivot must contain exactly three source helpers and one Pivot');
      let inputId = prefixSteps.at(-1).id;
      for (const helper of added.slice(0, -1)) {
        assert.equal(helper.ownerStepId, step.id);
        assert.equal(helper.operation.kind, 'RELATED_FIELD');
        assert.deepEqual(helper.inputs, [{ kind: 'STEP_OUTPUT', stepId: inputId }]);
        const related = helper.operation.relatedField;
        const selected = sourceBindings.get(related.outputColumnId);
        assert(selected, 'Automatic helper must implement one selected source input');
        assert.equal(related.choiceId.split('.')[0], 'cc2');
        const helperIdentity = JSON.parse(Buffer.from(related.choiceId.split('.')[1], 'base64url').toString('utf8'));
        assert.equal(helperIdentity.snapshotToken, baseline.catalog.snapshotToken);
        assert.equal(helperIdentity.source.stageId, inputId, 'Reissued helper choice must identify its actual input stage');
        for (const key of ['kind', 'candidateId', 'nodeId', 'resourceType', 'path', 'cardinality', 'logicalType']) {
          assert.equal(related.source[key], selected.source[key], `Persisted helper must retain selected source ${key}`);
          assert.equal(helperIdentity.source[key], related.source[key], `Reissued choice must identify persisted source ${key}`);
        }
        inputId = helper.id;
      }
      assert.deepEqual(added.at(-1), step);
      assert.deepEqual(step.inputs, [{ kind: 'STEP_OUTPUT', stepId: inputId }]);
      report.pivotSourceBindingEvidence ??= [];
      report.pivotSourceBindingEvidence.push({ policy, bindings: [...sourceBindings], candidate: request.response.candidateConstruction });
      const groupColumn = step.outputs.find(column => column.id === operation.groupKeyIds[0]);
      assert.equal(groupColumn?.label, 'Observation.valueCodeableConcept.text');
      const categories = operation.categories.map(category => ({ key: category.key,
        output: step.outputs.find(column => column.id === category.outputColumnId) }));
      const visibleTextKeys = preview.rows.map(row => row[groupColumn.name] ?? null);
      const cacheKey = JSON.stringify(visibleTextKeys);
      if (!oracleCache.has(cacheKey)) {
        report.oraclePhase = 'filter-complete-independent-route-domain';
        try {
          oracleCache.set(cacheKey, summarizeRelatedQuantityPivotDiscoveryResult(independentDiscovery, scope, { visibleTextKeys }));
        } catch (error) {
          error.verificationFailureCategory = 'harness-oracle';
          throw error;
        }
      }
      const oracle = oracleCache.get(cacheKey);
      const expected = expectedTextOnlyQuantityPivotRows(oracle, { groupColumn, categories, duplicatePolicy: policy });
      assert(expected.positiveDuplicateWitness, 'Visible preview must prove a nonvacuous SUM/MAX difference');
      const columns = outputColumnValues(preview.columns);
      assert.deepEqual(new Set(columns.map(column => column.column)), new Set(expected.columns.map(column => column.name)));
      const projectRows = rows => rows.map(row => JSON.stringify(columns.map(column => row[column.column]))).sort();
      assert.deepEqual(projectRows(preview.rows), projectRows(expected.rows), 'Every bounded Pivot protocol value must match independent raw data');
      report.fullPopulationOracle = oracle;
      report.oraclePhase = 'complete';
      const rowIds = preview.rows.map(row => row.__loom_row_id);
      assert(rowIds.every(id => typeof id === 'string' && id.length > 0));
      assert.equal(new Set(rowIds).size, rowIds.length);
      return { ...expected, columns, step, preview, rowIds, construction: candidate };
    };
    const visible = async (expected, panel = false) => {
      const prefix = panel ? '[data-testid="construction-proposal-preview"]' : '[data-testid="preview-table-scroll"]';
      const expectedCells = expected.rows.map(row => expected.columns.map(column =>
        row[column.column] == null ? '—' : String(row[column.column])));
      await waitForObservable(page, ({ prefix, count, panel, labels, expectedCells }) => {
        const root = document.querySelector(prefix);
        if (!root || document.body.innerText.includes('Loading your table…')) return false;
        const headers = [...root.querySelectorAll(panel ? 'th' : '[role="columnheader"]')]
          .map(cell => cell.innerText.trim().split('\n')[0].toLowerCase());
        if (JSON.stringify(headers) !== JSON.stringify(labels)) return false;
        if (panel) return root.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length === count;
        if (root.querySelector('[role="table"]')?.getAttribute('aria-rowcount') !== String(count + 1)) return false;
        const rows = [...root.querySelectorAll('[role="row"]')].filter(row => row.querySelector('[role="cell"]'));
        return rows.length > 0 && rows.every(row => {
          const ordinal = Number(row.firstElementChild?.textContent?.trim());
          return Number.isInteger(ordinal) && JSON.stringify([...row.querySelectorAll('[role="cell"]')]
            .map(cell => cell.innerText.trim())) === JSON.stringify(expectedCells[ordinal - 1]);
        });
      }, { prefix, count: expected.rows.length, panel, labels: expected.columns.map(column => column.label.toLowerCase()), expectedCells });
      const actual = panel ? await page.locator(prefix).evaluate(root => ({
        headers: [...root.querySelectorAll('th')].map(cell => cell.innerText.trim().split('\n')[0]),
        rows: [...root.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
      })) : await collectPreviewRows(page, { containerSelector: prefix, tableSelector: '[role="table"]', rowSelector: '[role="row"]', cellSelector: '[role="cell"]' });
      const rows = panel ? actual.rows : actual.rows.map(row => row.values);
      assert.deepEqual(actual.headers.map(label => label.toLowerCase()), expected.columns.map(column => column.label.toLowerCase()));
      const cells = expected.rows.map(row => expected.columns.map(column => row[column.column] == null ? '—' : String(row[column.column])));
      assert.deepEqual(rows.map(JSON.stringify).sort(), cells.map(JSON.stringify).sort());
    };
    const savedExpectation = { columns: outputColumnValues(baselinePreview.columns), rows: baselinePreview.rows,
      rowIds: baselinePreview.rows.map(row => row.__loom_row_id), construction: doc(baseline).construction };
    const assertSavedProtocol = async (expected, state, since) => {
      const preview = await savedPreviewFor(state, since);
      assert.deepEqual(outputColumnValues(preview.columns), expected.columns);
      const rows = values => values.map(row => JSON.stringify(expected.columns.map(column => row[column.column]))).sort();
      assert.deepEqual(rows(preview.rows), rows(expected.rows));
      const ids = preview.rows.map(row => row.__loom_row_id);
      assert(ids.every(id => typeof id === 'string' && id.length > 0));
      assert.equal(new Set(ids).size, ids.length);
      assert.deepEqual([...ids].sort(), [...expected.rowIds].sort(), 'Apply/reload must preserve exact proposed row identities');
      report.savedPreviewReceipts ??= [];
      report.savedPreviewReceipts.push({ receiptId: preview.receiptId, draftVersion: state.draftVersion,
        draftDigest: state.draftDigest, rowIds: ids });
    };
    const reload = async (expected, state, name) => {
      const start = Date.now();
      await gotoPage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
      await waitForObservable(page, constructionTableReady, { outputId });
      await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
      await visible(expected);
      recordRender(name, start);
      await sameSaved(state, name);
      await assertSavedProtocol(expected, state, start);
    };
    const cancel = async (state, expected, name) => {
      const start = Date.now();
      await clickNative(page, '[data-testid="construction-cancel-proposal"]');
      await waitForObservable(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]'));
      await visible(expected);
      recordRender(name, start);
      await sameSaved(state, name);
    };
    const applyCandidate = async (expected, name) => {
      const start = Date.now();
      await clickNative(page, '[data-testid="construction-apply-proposal"]');
      await waitForObservable(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]'));
      await visible(expected);
      recordRender(name, start);
      const state = await api(base + '/builder');
      await assertSavedProtocol(expected, state, start);
      assert.deepEqual(doc(state).construction, expected.construction, 'Saved construction must match the entire approved candidate');
      assert.equal(doc(state).output.id, outputId);
      assert.equal(doc(state).population ?? null, null, 'Pivot lifecycle must not introduce a population restriction');
      for (const key of ['rootResourceType', 'route', 'rows', 'fixedFilters', 'actions']) {
        assert.deepEqual(doc(state)[key], doc(baseline)[key], `Pivot must retain source ${key}`);
      }
      assert.equal(state.catalog.snapshotToken, baseline.catalog.snapshotToken, 'Catalog generation must remain pinned');
      assert.deepEqual(doc(state).construction.steps.filter(step => step.operation.kind === 'RELATED_EXPAND'),
        prefixSteps.filter(step => step.operation.kind === 'RELATED_EXPAND'), 'Reshaping must retain both full-population related source bindings');
      return state;
    };
    const history = async action => {
      const items = await page.locator('[data-testid^="construction-history-step-"]').evaluateAll(elements =>
        elements.map(element => ({ id: element.dataset.testid, text: element.innerText })));
      const pivot = items.findLast(item => /pivot|categories into columns/i.test(item.text));
      assert(pivot, 'Saved Pivot must expose its native history control');
      await clickNative(page, `[data-testid=${JSON.stringify(pivot.id)}]`);
      await clickNative(page, `[data-testid^="construction-${action}-step-"]`);
    };
    const first = await configure();
    const sum = await checkPivot(first, 'SUM');
    await visible(sum, true);
    mark(0, { sourceRows: sum.sourceRows, witness: sum.positiveDuplicateWitness });
    mark(1, { candidate: first.request.response.candidateConstruction });
    mark(2, { fullGroupCount: sum.fullGroupCount, sourceRows: sum.sourceRows, categoryCount: sum.fullCategoryCount });
    await cancel(baseline, savedExpectation, 'SUM-initial-cancel-to-render');
    mark(3, { draftDigest: baseline.draftDigest });
    const second = await configure();
    const appliedSum = await checkPivot(second, 'SUM');
    const sumState = await applyCandidate(appliedSum, 'SUM-apply-to-render');
    mark(4, { sourceRows: appliedSum.sourceRows, draftDigest: sumState.draftDigest });
    await reload(appliedSum, sumState, 'SUM-reload-to-render');
    mark(5, { outputId, sourceRows: appliedSum.sourceRows });
    const editMax = async () => {
      await history('edit');
      const index = report.authoringRequests.length;
      const previousId = await inspectPage(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId);
      const start = Date.now();
      await selectNative(page, 'select[aria-label="Pivot duplicate policy"]', 'MAX');
      return terminalProposal(index, start, 'SUM-to-MAX-edit-to-render', previousId);
    };
    const canceledMax = await checkPivot(await editMax(), 'MAX');
    await visible(canceledMax, true);
    await cancel(sumState, appliedSum, 'MAX-edit-cancel-to-render');
    mark(6, { draftDigest: sumState.draftDigest });
    const max = await checkPivot(await editMax(), 'MAX');
    const maxState = await applyCandidate(max, 'MAX-apply-to-render');
    await reload(max, maxState, 'MAX-reload-to-render');
    mark(7, { sourceRows: max.sourceRows, witness: max.positiveDuplicateWitness });
    const removal = async () => {
      const index = report.authoringRequests.length;
      const start = Date.now();
      await history('remove');
      const candidate = await terminalProposal(index, start, 'Pivot-removal-preview-to-render');
      assert.deepEqual(candidate.request.response.candidateConstruction.steps, prefixSteps);
      const restored = { columns: outputColumnValues(candidate.preview.columns), rows: candidate.preview.rows,
        rowIds: candidate.preview.rows.map(row => row.__loom_row_id), construction: candidate.request.response.candidateConstruction };
      assert.deepEqual(restored.columns, savedExpectation.columns);
      assert.deepEqual(restored.rows, savedExpectation.rows, 'Removal must restore the exact pre-Pivot protocol rows');
      await visible(restored, true);
      return restored;
    };
    await removal();
    await cancel(maxState, max, 'Pivot-removal-cancel-to-render');
    mark(8, { draftDigest: maxState.draftDigest });
    const restored = await removal();
    const restoredState = await applyCandidate(restored, 'Pivot-removal-apply-to-render');
    assert.deepEqual(doc(restoredState).construction.steps, prefixSteps);
    await reload(restored, restoredState, 'Pivot-removal-reload-to-render');
    mark(9, { sourceRows: sum.sourceRows, restoredPrefix: prefixSteps });
    mark(10, { timings: report.cases });
    report.lifecycle = { textOnly: true, fullPopulation: true, sourceRows: sum.sourceRows, initialCancel: true,
      apply: true, reload: true, editCancel: true, editApply: true, removalCancel: true, removalApply: true };
    report.gaps = [];
    report.status = 'passed';
  } else {

  if(!fullPopulation) await setPreviewLimit(100,expected.length);
  if (fullPopulation) builder = await api(base + '/builder');
  const expanded=builder;
  const observations=witnesses.filter(w=>w.anchor).map(w=>({...rawQuery(`FOR o IN Observation FILTER o._id==${JSON.stringify(w.anchor)} AND o.project=="${project}" AND o.dataset_generation=="cda-fhir-v1" RETURN {id:o.id,quantity:o.payload.valueQuantity,text:o.payload.valueCodeableConcept.text}`)[0],rowIds:w.values}));
  assert(observations.length<=1000,'Independent relationship oracle must remain bounded');
  const quantities=observations.filter(o=>typeof o?.quantity?.code==='string' && typeof o.quantity.value==='number');
  assert(quantities.length>0,'Selected indirect route needs populated numeric quantity witnesses');
  report.oracle.scope=fullPopulation?'bounded selected-Specimen witnesses retained only for discovery latency; not a full-population correctness oracle':'single selected-Specimen relationship witnesses';
  report.oracle.quantities=quantities;
  const expectedCategories=[...new Set(quantities.map(o=>o.quantity.code))].sort();
  report.oracle.expectedCategories=expectedCategories;
  await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
  await clickNative(page,'[data-testid="construction-action-pivot-rows"]');
  await waitForObservable(page,() => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
  await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]'))));
  for(const label of ['Specimen ID','Patient FHIR resource ID','Observation FHIR resource ID']){
    const selector=`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
    const state=await inspectPage(page, ({ selector }) => { const i=document.querySelector(selector); return {found:Boolean(i),checked:i?.checked,disabled:i?.disabled}; }, { selector: selector });
    assert(state.found&&!state.disabled,`Pivot group field ${label} must be available`);
    if(state.checked)await clickNative(page,selector);
  }
  const chooseSource=async(label,path)=>{
    const options=await inspectPage(page, ({ label }) => [...document.querySelector(`select[aria-label="${label}"]`).options].map(o=>({value:o.value,label:o.textContent})), { label: label });
    const matching=options.filter(o=>o.value.startsWith('source:') && o.label.includes(path));
    assert.equal(matching.length,1,'Require one exact active related source choice: '+path+' '+JSON.stringify(options));
    await selectNative(page,`select[aria-label="${label}"]`,matching[0].value);
  };
  await chooseSource('Add pivot group field','Observation.valueCodeableConcept.text');
  await chooseSource('Pivot category field','Observation.valueQuantity.code');
  const start=Date.now();
  const requestIndex=report.authoringRequests.length;
  await chooseSource('Pivot values field','Observation.valueQuantity.value');
  await waitForObservable(page,() => Boolean(!document.body.innerText.includes('Finding categories') && (document.querySelector('[data-testid="construction-proposal-panel"]') || document.body.innerText.includes('exceeded') || document.body.innerText.includes('No categories'))),10000);
  await drainPendingResponseReads();
  const discovery=report.authoringRequests.slice(requestIndex).findLast(r=>r.endpoint==='construction-category-discoveries');
  assert(discovery,'Native source pair must automatically discover categories');
  report.initialDiscovery=discovery;
  report.discoveryDOM=await captureFailureDOM();
  recordRender('quantity-category-discovery-to-render',start);
  assert.equal(discovery.status,200,'Quantity discovery must succeed');
  assert.equal(discovery.response.complete,true,'Category discovery must be complete');
  if(!fullPopulation){
    assert.equal(discovery.body.groupKeyIds.length,1,'Initial quantity discovery must retain the native value-text group key');
    const expectedKeys=[...new Set(observations.map(o=>o?.quantity?.code==null?'NULL':JSON.stringify({kind:'STRING',string:o.quantity.code})))].sort();
    const actualKeys=discovery.response.categories.map(c=>c.key.kind==='NULL'?'NULL':JSON.stringify(c.key)).sort();
    assert.deepEqual(actualKeys,expectedKeys,'Complete categories must match every scoped related Observation, including missing values');
    report.categoryCorrectness='bounded independent route oracle passed';
    report.oracle.observationRows=observations.map(({rowIds,id,quantity,text})=>({rowIds,id,quantity,text}));
    await rendered(expected,100);
    const sourceOutputOrder=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim().split('\\n')[0]);
});
    assert.deepEqual(sourceOutputOrder.map(label=>label.toLowerCase()),['specimen id','patient fhir resource id','observation fhir resource id']);
    report.pivotColumnOrder={sourceOutputOrder};
    const groupLabels=['Specimen ID','Patient FHIR resource ID','Observation FHIR resource ID'];
    const groupKeysStart=Date.now();
    const pivotPreviewStart=Date.now();
    for(const label of groupLabels){
      const selector=`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
      const state=await inspectPage(page, ({ selector }) => { const i=document.querySelector(selector); return {found:Boolean(i),checked:i?.checked,disabled:i?.disabled}; }, { selector: selector });
      assert(state.found&&!state.disabled,`Pivot group field ${label} must be available`);
      if(!state.checked)await clickNative(page,selector);
    }
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label="Pivot group Observation.valueCodeableConcept.text"]:checked'))));
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))&&document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')?.innerText.includes('2 of 2 categories')),10000);
    await drainPendingResponseReads();
    const refreshedDiscovery=report.authoringRequests.slice(requestIndex).findLast(request=>request.endpoint==='construction-category-discoveries'&&request.body?.groupKeyIds?.length===4) ?? discovery;
    // Existing ID group keys preserve the source/category pair, so reusing its complete discovery is valid.
    assert.equal(refreshedDiscovery.status,200,'Refreshed quantity discovery must succeed');
    assert.equal(refreshedDiscovery.response.complete,true,'Refreshed quantity discovery must be complete');
    const refreshedKeys=refreshedDiscovery.response.categories.map(category=>category.key.kind==='NULL'?'NULL':JSON.stringify(category.key)).sort();
    const refreshedExpectedKeys=[...new Set(observations.map(observation=>observation?.quantity?.code==null?'NULL':JSON.stringify({kind:'STRING',string:observation.quantity.code})))].sort();
    assert.deepEqual(refreshedKeys,refreshedExpectedKeys,'Changing row keys must preserve the independently witnessed category set');
    report.discovery=refreshedDiscovery;
    report.oracle.categoryDiscoveryGroupKeyCounts=[discovery.body.groupKeyIds.length,refreshedDiscovery.body.groupKeyIds.length];
    report.oracle.categoryDiscoveryReused=refreshedDiscovery===discovery;
    recordRender('quantity-category-state-after-row-keys',groupKeysStart);
    report.categoryCorrectness='bounded independent route oracle passed; category domain remains unchanged when existing ID keys are added';
    const prePivotWorkspace=expanded.workspace;
    assert.deepEqual((await api(base+'/builder')).workspace,prePivotWorkspace,'Category discovery and candidate previews must not mutate the saved draft');
    await waitForObservable(page,() => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)),10000);
    await drainPendingResponseReads();
    recordRender('pivot-preview-to-ready',pivotPreviewStart);
    const firstPivotProposal=report.authoringRequests.slice(requestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(firstPivotProposal?.status,200,'Native Pivot preview request must succeed');
    const firstPivotPreview=await proposalPreviewFor(firstPivotProposal);
    const expectedPivot=expectedPivotFor(firstPivotProposal,firstPivotPreview);
    assert.equal(firstPivotPreview.rowCount,expectedPivot.sourceRowCount,'Native Pivot preview row count must match the independent Observation oracle');
    assert.equal(firstPivotPreview.rows.length,expectedPivot.rows.length,'Preview must include every bounded row at the selected 100-row limit');
    await assertExactRows({name:'pivot-preview-independent-values-and-ids',columns:expectedPivot.columns,rows:expectedPivot.rows,rowLimit:100,panel:true});
    assert.equal(expectedPivot.operation.duplicatePolicy,'ERROR');
    assert.equal(expectedPivot.duplicateBuckets,0);
    await clickNative(page,'[data-testid="construction-reshape-pivot-advanced"] > summary');
    const duplicateOptions=await inspectPage(page,() => {
return [...document.querySelector('select[aria-label="Pivot duplicate policy"]').options].map(option=>({value:option.value,disabled:option.disabled}));
});
    assert(duplicateOptions.some(option=>option.value==='SUM'&&!option.disabled),'Numeric Pivot values must expose the native SUM duplicate option');
    const browserCommandCount=report.browserCommands?.length??0;
    const cancelStart=Date.now();
    await clickNative(page,'[data-testid="construction-cancel-proposal"]');
    await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')),5000);
    assert.deepEqual((await api(base+'/builder')).workspace,prePivotWorkspace,'Cancel must leave the saved construction unchanged');
    assert.equal(report.browserCommands?.length??0,browserCommandCount,'Cancel must not issue a draft command');
    recordRender('pivot-preview-cancel-without-mutation',cancelStart);
    // Cancel closes the candidate editor. Reopen and configure through native controls.
    await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
    await clickNative(page,'[data-testid="construction-action-pivot-rows"]');
    await waitForObservable(page,() => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
    for(const label of groupLabels) {
      const selector=`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
      const checked=await inspectPage(page, ({ selector }) => document.querySelector(selector)?.checked, { selector: selector });
      if(!checked) await clickNative(page,selector);
    }
    await chooseSource('Add pivot group field','Observation.valueCodeableConcept.text');
    await chooseSource('Pivot category field','Observation.valueQuantity.code');
    await chooseSource('Pivot values field','Observation.valueQuantity.value');
    await clickNative(page,'[data-testid="construction-reshape-pivot-advanced"] > summary');
    const sumRequestIndex=report.authoringRequests.length;
    const sumStart=Date.now();
    await selectNative(page,'select[aria-label="Pivot duplicate policy"]','SUM');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'&&Boolean(document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-id]')?.dataset.proposalId)),10000);
    await drainPendingResponseReads();
    recordRender('pivot-sum-policy-preview',sumStart);
    const sumProposal=report.authoringRequests.slice(sumRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(sumProposal?.status,200,'Native numeric SUM duplicate policy must preview successfully');
    const sumPreview=await proposalPreviewFor(sumProposal);
    const sumExpected=expectedPivotFor(sumProposal,sumPreview,'SUM');
    assert.equal(sumExpected.duplicateBuckets,0);
    assert.equal(sumPreview.rowCount,sumExpected.sourceRowCount);
    await assertExactRows({name:'pivot-sum-policy-independent-values-and-ids',columns:sumExpected.columns,rows:sumExpected.rows,rowLimit:100,panel:true});
    const secondPreviewStart=Date.now();
    const secondRequestIndex=report.authoringRequests.length;
    const sumProposalId=sumProposal.response.proposalId;
    await selectNative(page,'select[aria-label="Pivot duplicate policy"]','ERROR');
    await waitForObservable(page, ({ previousId }) => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]'); return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== previousId; }, { previousId: sumProposalId }, 10000);
    await drainPendingResponseReads();
    recordRender('pivot-repreview-after-cancel',secondPreviewStart);
    const proposalAgain=report.authoringRequests.slice(secondRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(proposalAgain?.status,200,'Pivot must preview again after a canceled proposal');
    const appliedPreview=await proposalPreviewFor(proposalAgain);
    const appliedExpected=expectedPivotFor(proposalAgain,appliedPreview,'ERROR');
    assert.equal(appliedExpected.operation.duplicatePolicy,'ERROR');
    assert.equal(appliedExpected.duplicateBuckets,0,'Every independent group/category pair must be unique under the selected duplicate policy');
    assert.equal(appliedPreview.rowCount,appliedExpected.sourceRowCount);
    await assertExactRows({name:'pivot-repreview-independent-values-and-ids',columns:appliedExpected.columns,rows:appliedExpected.rows,rowLimit:100,panel:true});
    const applyStart=Date.now();
    await clickNative(page,'[data-testid="construction-apply-proposal"]');
    await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===3),10000);
    await assertExactRows({name:'pivot-apply-independent-values-and-ids',columns:appliedExpected.columns,rows:appliedExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(appliedPreview,'pivot-apply-stable-protocol-identities');
    recordRender('pivot-apply-to-render',applyStart);
    builder=await api(base+'/builder');
    const appliedDocument=doc(builder);
    const savedPivot=appliedDocument.construction?.steps?.find(step=>step.operation.kind==='PIVOT');
    assert(savedPivot,'Applied Pivot must be present in the saved draft');
    assert.equal(savedPivot.operation.pivot.duplicatePolicy,'ERROR');
    report.lifecycle={duplicatePolicy:'ERROR',duplicateBuckets:appliedExpected.duplicateBuckets,sourceRows:appliedExpected.sourceRowCount,categoryKeys:refreshedKeys,nativeNumericDuplicatePolicies:duplicateOptions.filter(option=>!option.disabled).map(option=>option.value)};
    report.pivotSourceColumnBeforeDrag=appliedDocument.columns.find(column=>column.label==='Specimen ID');
    const pivotColumnDragStart=Date.now();
    const beforeDragRowIDs=appliedPreview.rows.map(row=>row.__loom_row_id).sort();
    const beforeDragCommandCount=report.browserCommands?.length??0;
    const dragGeometry=await nativeDragColumnBefore('d','Specimen ID');
    assert.deepEqual(dragGeometry.labels,appliedExpected.columns.map(column=>column.label),'Native Columns menu must start in the exact generated Pivot output order');
    const draggedExpectedColumns=[appliedExpected.columns.at(-1),...appliedExpected.columns.slice(0,-1)];
    const draggedLabels=draggedExpectedColumns.map(column=>column.label);
    await waitForObservable(page, ({ expectedLabels }) => JSON.stringify([...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim().split('\n')[0].toLowerCase()))===JSON.stringify(expectedLabels), { expectedLabels: draggedLabels.map(label=>label.toLowerCase()) }, 10000);
    await clickNative(page,'button',{name:'Columns'});
    await waitForNativePresentationCommand(beforeDragCommandCount);
    await assertExactRows({name:'pivot-native-drag-independent-values-and-ids',columns:draggedExpectedColumns,rows:appliedExpected.rows,rowLimit:100});
    recordRender('pivot-native-playwright-drag-to-render',pivotColumnDragStart);
    builder=await api(base+'/builder');
    const savedDraggedOrder=assertSavedPivotOutputOrder(builder,draggedExpectedColumns,'pivot-native-playwright-drag');
    assert.deepEqual(beforeDragRowIDs,appliedPreview.rows.map(row=>row.__loom_row_id).sort(),'Column order changes must not alter the canonical Pivot row identities');
    await assertPersistedPivotIdentity(appliedPreview,'pivot-native-playwright-drag-stable-protocol-identities');
    report.pivotColumnOrder={...report.pivotColumnOrder,before:appliedExpected.columns.map(column=>column.label),after:draggedLabels,nativeDrag:dragGeometry,saved:savedDraggedOrder,canonicalRowIDs:beforeDragRowIDs};
    const reloadPivotStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, constructionTableReady, { outputId }, 10000);
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    await assertExactRows({name:'pivot-reload-independent-values-and-ids',columns:draggedExpectedColumns,rows:appliedExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(appliedPreview,'pivot-reload-stable-protocol-identities');
    recordRender('pivot-reload-to-render',reloadPivotStart);
    builder=await api(base+'/builder');
    assertSavedPivotOutputOrder(builder,draggedExpectedColumns,'pivot-reload-native-order');
    const history=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
    const pivotHistory=history.findLast(item=>/pivot|categories into columns/i.test(item.text));
    assert(pivotHistory,'Reloaded construction history must retain the Pivot step');
    const editStart=Date.now();
    await clickNative(page,`[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))),5000);
    await clickNative(page,'[data-testid^="construction-edit-step-"]');
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))),5000);
    const restoredEditor=await inspectPage(page,() => {
const p=document.querySelector('[data-testid="construction-reshape-pivot"]');return {category:p.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:p.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent,groups:[...p.querySelectorAll('input[aria-label^="Pivot group"]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label')),duplicatePolicy:p.querySelector('select[aria-label="Pivot duplicate policy"]')?.value,outputs:[...p.querySelectorAll('input[aria-label^="Pivot output label"]')].map(input=>({label:input.getAttribute('aria-label'),value:input.value}))};
});
    assert(restoredEditor.category.includes('Observation.valueQuantity.code'));
    assert(restoredEditor.value.includes('Observation.valueQuantity.value'));
    assert.deepEqual(new Set(restoredEditor.groups),new Set(['Pivot group Specimen ID','Pivot group Patient FHIR resource ID','Pivot group Observation FHIR resource ID','Pivot group Observation.valueCodeableConcept.text']));
    assert.equal(restoredEditor.duplicatePolicy,'ERROR');
    recordRender('pivot-edit-restores-saved-fields',editStart);
    const editRequestIndex=report.authoringRequests.length;
    const previousEditProposalId=await inspectPage(page,() => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
    const editChangeStart=Date.now();
    await cda.action('fill Pivot output label d', page.getByLabel('Pivot output label d', { exact: true }), locator => locator.fill('Observed quantity d', { timeout: 5000 }), { timeout: 5000, budget: 5000, editable: true });
    await waitForObservable(page, ({ previousId }) => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]'); return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== previousId; }, { previousId: previousEditProposalId }, 10000);
    await drainPendingResponseReads();
    recordRender('pivot-edit-preview-to-ready',editChangeStart);
    const editProposal=report.authoringRequests.slice(editRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(editProposal?.status,200,'Edited Pivot must produce a native proposal');
    const editPreview=await proposalPreviewFor(editProposal);
    const editExpected=expectedPivotFor(editProposal,editPreview);
    const editedColumnOrder=draggedLabels.map(label=>label==='d'?'Observed quantity d':label);
    const editedOutputs=editProposal.response.candidateConstruction.steps.at(-1).outputs;
    const editedOrder=new Map(editedOutputs.map((output,index)=>[output.name,output.table?.order??index]));
    editExpected.columns=[...editExpected.columns].sort((left,right)=>editedOrder.get(left.column)-editedOrder.get(right.column));
    assert.deepEqual(editExpected.columns.map(column=>column.label),editedColumnOrder,'Edited candidate must retain the saved native column order');
    assert.equal(editExpected.operation.duplicatePolicy,'ERROR');
    assert.equal(editExpected.duplicateBuckets,0);
    await assertExactRows({name:'pivot-edit-preview-independent-values-and-ids',columns:editExpected.columns,rows:editExpected.rows,rowLimit:100,panel:true});
    await clickNative(page,'[data-testid="construction-apply-proposal"]');
    await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===3),10000);
    await assertExactRows({name:'pivot-edit-apply-independent-values-and-ids',columns:editExpected.columns,rows:editExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(editPreview,'pivot-edit-apply-stable-protocol-identities');
    builder=await api(base+'/builder');
    const editedPivot=doc(builder).construction.steps.find(step=>step.operation.kind==='PIVOT');
    assert.equal(editedPivot.outputs.find(column=>column.name===editExpected.columns.find(column=>column.label==='Observed quantity d')?.column)?.label,'Observed quantity d');
    assertSavedPivotOutputOrder(builder,editExpected.columns,'pivot-edit-retains-native-order');
    const reloadEditedStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, constructionTableReady, { outputId }, 10000);
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    await assertExactRows({name:'pivot-edited-reload-independent-values-and-ids',columns:editExpected.columns,rows:editExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(editPreview,'pivot-edited-reload-stable-protocol-identities');
    recordRender('pivot-edited-reload-to-render',reloadEditedStart);
    assertSavedPivotOutputOrder(await api(base+'/builder'),editExpected.columns,'pivot-edit-reload-retains-native-order');
    const removeStart=Date.now();
    const editedHistory=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
    const editedPivotHistory=editedHistory.findLast(item=>/pivot|categories into columns/i.test(item.text));
    assert(editedPivotHistory,'Edited Pivot history control must remain available');
    await clickNative(page,`[data-testid=${JSON.stringify(editedPivotHistory.testId)}]`);
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))),5000);
    const removalRequestIndex=report.authoringRequests.length;
    await clickNative(page,'[data-testid^="construction-remove-step-"]');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'),10000);
    await drainPendingResponseReads();
    const removalProposal=report.authoringRequests.slice(removalRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(removalProposal?.status,200,'Pivot removal must produce a native proposal');
    const removalPreview=await proposalPreviewFor(removalProposal);
    const sourcePreviewColumns=outputColumnValues(removalPreview.columns);
    assert.deepEqual(sourcePreviewColumns.map(column=>column.label.toLowerCase()),sourceOutputOrder.map(label=>label.toLowerCase()),'Removing the Pivot must restore the exact pre-Pivot source output order');
    const sourceRows=expected.map(values=>Object.fromEntries(sourcePreviewColumns.map(column=>{
      const label=column.label.toLowerCase();
      const index=label.includes('specimen id')?0:label.includes('patient fhir resource id')?1:label.includes('observation fhir resource id')?2:-1;
      assert(index>=0,`Pivot removal preview must restore a witnessed ID column, got ${column.label}`);
      return [column.column,values[index]];
    })));
    assert.equal(removalPreview.rowCount,sourceRows.length,'Pivot removal must restore the complete bounded source row count');
    assert.equal(removalPreview.rows.length,sourceRows.length,'Pivot removal preview must contain all 31 source rows at the selected 100-row limit');
    await assertExactRows({name:'pivot-remove-preview-restores-source-ids',columns:sourcePreviewColumns,rows:sourceRows,rowLimit:100,panel:true});
    await clickNative(page,'[data-testid="construction-apply-proposal"]');
    await waitForObservable(page,() => Boolean(document.querySelectorAll('[data-testid^="construction-history-step-"]').length===2&&!document.querySelector('[data-testid="construction-proposal-panel"]')),10000);
    await assertExactRows({name:'pivot-remove-apply-restores-source-ids',columns:sourcePreviewColumns,rows:sourceRows,rowLimit:100});
    recordRender('pivot-remove-to-render',removeStart);
    builder=await api(base+'/builder');
    assert(!doc(builder).construction.steps.some(step=>step.operation.kind==='PIVOT'),'Applied removal must delete the Pivot step from the saved draft');
    const restoredWorkspace=builder.workspace;
    const reloadRestoredStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, constructionTableReady, { outputId }, 10000);
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    await assertExactRows({name:'pivot-remove-reload-restores-source-ids',columns:sourcePreviewColumns,rows:sourceRows,rowLimit:100});
    recordRender('pivot-remove-reload-to-render',reloadRestoredStart);
    report.lifecycle={...report.lifecycle,removed:true,restoredSourceRows:sourceRows.length,editOutputLabel:'Observed quantity d'};
    assert.deepEqual((await api(base+'/builder')).workspace,restoredWorkspace,'Pivot lifecycle reload must preserve the restored saved workspace');
    report.gaps=['Full-population category discovery remains unproven; its own independent full-population oracle is required. The bounded native lifecycle uses only the independently queried one-Specimen route witnesses.'];
    report.status='passed';
  }else{
    // This driver targets the local Compose --no-auth API. A different API must
    // supply its actual authorization scope before reusing this unrestricted oracle.
    const independent=runCDAQuantityCategoryOracle({
      project,dataset_generation:source.generation,scope_allowed:true,
      auth_resource_paths:[],auth_resource_paths_unrestricted:true,
    }, { container: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER), database: (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE) });
    compareCDAQuantityCategoryValues(independent.oracle,discovery.response.categories);
    report.fullPopulationOracle=independent;
    report.oracle.scope='complete scoped Specimen→Patient→Observation population, local Compose no-auth authorization';
    report.categoryCorrectness='complete independent full-population route oracle passed';
    report.gaps=['Full-population discovery regression verified; native Pivot Apply/edit/removal is covered separately by the bounded mode.'];
    report.status='passed';
  }
  }
  await drainPendingResponseReads();
  includeFixtureDiagnostics(report);
  report.expectedValidationErrors=report.errors.filter(error => {
    const request=error.request;
    const pivot=request?.body?.candidateConstruction?.steps?.find(step=>step.operation.kind==='PIVOT')?.operation.pivot;
    return error.kind==='http' && error.status===422 && request?.endpoint==='construction-proposals' &&
      request.response?.error?.code==='TABLE_PIVOT_CELL_CARDINALITY' &&
      pivot?.duplicatePolicy==='ERROR' && pivot.groupKeyIds?.length===1 &&
      error.observedAfter==='quantity-category-discovery-to-render';
  });
  assert.deepEqual(report.errors.filter(error=>!report.expectedValidationErrors.includes(error)),[],
    'Unexpected HTTP, JavaScript or module errors must fail the Pivot regression');
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); report.__nativeFailure = true;
  report.failureCategory = error.verificationFailureCategory ?? 'unclassified';
  if (error.verificationFailureCategory) report.failedOraclePhase = report.oraclePhase;
  await captureFailure(error, { phase: report.activeAction?.label ?? report.cases.at(-1)?.name,
    elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : undefined,
    action: report.activeAction, requestEvidence: report.nativeRequests.slice(-20), latestCase: report.cases.at(-1) });
  await drainPendingResponseReads();
  report.savedBuilderAtFailure=await api(base + '/builder').catch(error=>({readError:String(error)}));
  report.nativeDragEvents = page ? await inspectPage(page, () => window.__loomNativeDragEvents).catch(()=>undefined) : undefined;
  report.failureDOM = page ? await captureFailureDOM().catch(error=>({captureError:String(error)})) : undefined;
  report.failureUI = report.failureDOM?.bodyText;
  await drainPendingResponseReads();
  correlateAuthoringRequests();
  report.pendingAuthoringRequests=report.authoringRequests.filter(request=>request.responseFinishedAtMs===undefined&&!request.loadingFailure).map(request=>({endpoint:request.endpoint,requestId:request.requestId,method:request.method,url:request.url,requestStartedAtMs:request.requestStartedAtMs,status:request.status,requestDraftVersion:request.requestDraftVersion,requestDraftDigest:request.requestDraftDigest,requestOutputId:request.requestOutputId,requestReceiptId:request.requestReceiptId}));
} finally {
  activePivotActionToRenderBudgetMs = DEFAULT_ACTION_TO_RENDER_BUDGET_MS;
  await drainPendingResponseReads();
  try {
    report.sourceFreeze = await sourceFreeze.assertUnchanged();
  } catch (error) {
    if (!error.invalidatesRun) throw error;
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      unchanged: false, changedPaths: error.changedPaths,
      invalidatesRun: true, productFailure: false,
    };
    report.__nativeFailure = true;
  }
  report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
  report.sourceFingerprint.unchanged = report.sourceFreeze?.unchanged === true
    && report.sourceFingerprint.before.sha256 === report.sourceFingerprint.after.sha256
    && report.sourceFingerprint.before.files === report.sourceFingerprint.after.files;
  if (!report.sourceFingerprint.unchanged) {
    report.priorStatus = report.status; report.status = 'invalidated'; report.__nativeFailure = true;
  }
  try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
  catch (error) {
    report.priorStatus = report.status; report.status = 'invalidated';
    report.apiBuildFreeze = { checked: true, unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
    report.__nativeFailure = true;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-quantity-pivot-native-drag-browser.mjs workflow failed');
  if (textOnly) {
    includeFixtureDiagnostics(report);
    const integrityEvidence = { sourceFreeze: report.sourceFreeze, apiBuildFreeze: report.apiBuildFreeze, errors: report.errors };
    const integrityPassed = report.sourceFreeze?.unchanged === true && report.apiBuildFreeze?.unchanged === true
      && report.errors.length === 0;
    report.assertions.push({ name: cda.report.requiredChecks[11], status: integrityPassed ? 'passed' : 'failed', evidence: integrityEvidence });
    if (!integrityPassed) { report.status = 'failed'; report.failureCategory = 'integrity-or-unexpected-browser-error'; }
    await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
    cda.check('row-lifecycle', cda.report.requiredChecks[11], integrityPassed, integrityEvidence);
  }
  await cda.attachReport('verify-cda-quantity-pivot-native-drag-browser.mjs', report);
  return report;
}
