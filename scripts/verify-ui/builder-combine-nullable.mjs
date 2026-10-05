import assert from 'node:assert/strict';
import { expect, test } from '@playwright/test';
import { createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { recordCheck } from './report.mjs';
import { builderCancelStateEvidence, nativeResponseScopeEvidence, removalProposalEvidence } from './builder-combine-nullable-helpers.mjs';
import { classifyNativeBrowserApiRequest } from '../lib/native-browser-api-scope.mjs';
import {
  builderRequestURL,
  builderResponseIdentity,
  constructionProposalPreviewEvidence,
  currentPublishedRevisionForOutput,
  findColumn,
  isCombineInputIDColumn,
  isNumericClickHouseType,
  isScalarStringColumn,
  nativeCombineTargetBindingEvidence,
  rootedEmptyTargetRestorationEvidence,
  sameSourceDocuments,
  snapshotSourceDocument,
} from './builder-combine-helpers.mjs';

const workspaceReady = "document.body.innerText.includes('DATASET WORKSPACE') && Boolean(document.querySelector('[data-testid=\"construction-workspace\"]'))";
const proposalReady = "(()=>{const panel=document.querySelector('[data-testid=\"construction-proposal-panel\"][data-proposal-status=\"ready\"]');const preview=document.querySelector('[data-testid=\"construction-proposal-preview\"][data-preview-status=\"ready\"]');return Boolean(panel&&preview&&preview.getAttribute('data-preview-receipt-id')===panel.getAttribute('data-proposal-id'))})()";
const proposalPreview = (count) =>
  "(()=>{const panel=document.querySelector('[data-testid=\"construction-proposal-panel\"][data-proposal-status=\"ready\"]');const preview=document.querySelector('[data-testid=\"construction-proposal-preview\"][data-preview-status=\"ready\"]');return Boolean(panel&&preview&&preview.getAttribute('data-preview-receipt-id')===panel.getAttribute('data-proposal-id')&&preview.querySelectorAll('tbody tr[data-testid=\"construction-proposal-preview-row\"]').length===" + count + ')})()';
const savedPreview = (count) =>
  "(()=>{const preview=document.querySelector('[data-testid=\"construction-preview\"]');const table=document.querySelector('[data-testid=\"preview-table-scroll\"] [role=\"table\"]');return Boolean(preview?.getAttribute('data-preview-status')==='ready'&&table?.getAttribute('aria-rowcount')===" + JSON.stringify(String(count + 1)) + "&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()";

const emptyTargetReady = (outputId) => `(()=>{const selected=document.querySelector('[data-testid="construction-table-'+CSS.escape(${JSON.stringify(outputId)})+'"]');const preview=document.querySelector('[data-testid="preview-table-scroll"]');return selected?.getAttribute('aria-current')==='page'&&!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-history"]')&&preview?.textContent?.trim()==='Add a column to see your table.'})()`;

const fixtureRows = (fixtureDir, file) => readFileSync(join(fixtureDir, file), 'utf8')
  .split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));

const check = (report, dimension, name, condition, evidence = {}) => {
  if (!['correctness', 'persistence', 'usability', 'performance'].includes(dimension) ||
      typeof name !== 'string' || name.trim() === '' || typeof condition !== 'boolean') {
    throw new TypeError('Nullable Combine checks require a known dimension, a string name, and a boolean condition.');
  }
  recordCheck(report, dimension, name, Boolean(condition), evidence);
  if (!condition) throw new Error('required nullable KEY_JOIN check failed: ' + name + '; evidence=' + JSON.stringify(evidence).slice(0, 1400));
};

const nativeApiScope = (context, explorer) => ({
  uiOrigin: new URL(context.target.uiUrl).origin,
  apiOrigin: new URL(context.target.apiUrl).origin,
  project: context.target.fixtureProject,
  explorer,
  protectedExplorer: context.target.bootstrapExplorerId,
});

const STEP_TIMEOUT_MS = 5000;
const setSelectValue = async (page, selector, value) => {
  const selected = await page.locator(selector).selectOption(value);
  return Array.isArray(selected) ? selected[0] : selected;
};
const evaluate = (page, expression) => page.evaluate(expression);
const waitFor = (page, expression, timeout = 5000) => page.waitForFunction(expression, undefined, { timeout });
const click = async (page, selector, options = {}) => {
  const locator = selector === 'button' ? page.getByRole('button', { name: options.name, exact: true }) :
    selector === 'summary' ? page.getByText(options.name, { exact: true }) : page.locator(selector);
  await locator.click();
};
const fill = (page, selector, value) => page.locator(selector).fill(value);
const reload = async (page, ready) => { await page.reload({ waitUntil: 'domcontentloaded' }); await waitFor(page, ready, 30000); };
const recordBrowserTiming = (report, page, { name, action: perform, after, timeout = 5000, budget = STEP_TIMEOUT_MS }) =>
  test.step(name, async () => {
    const started = performance.now();
    const budgetMs = Math.min(STEP_TIMEOUT_MS, budget);
    let performCompleted = false;
    let afterCompleted = false;
    let afterMs;
    let elapsedMs;
    try {
      await perform();
      performCompleted = true;
      if (after) {
        const afterStarted = performance.now();
        if (typeof after === 'function') await after();
        else await waitFor(page, after, Math.min(timeout, STEP_TIMEOUT_MS));
        afterMs = performance.now() - afterStarted;
        afterCompleted = true;
      }
      elapsedMs = performance.now() - started;
      expect(elapsedMs, `${name} action-to-render exceeded ${STEP_TIMEOUT_MS} ms`).toBeLessThanOrEqual(budgetMs);
    } finally {
      elapsedMs ??= performance.now() - started;
      const passed = performCompleted && (!after || afterCompleted) && elapsedMs <= budgetMs;
      const recordedElapsedMs = Math.round(elapsedMs);
      report.actions.push({ label: name, status: passed ? 'passed' : 'failed', elapsedMs: recordedElapsedMs, locator: 'native Playwright step' });
      report.timings[name] = recordedElapsedMs;
      recordCheck(report, 'usability', `${name} completed`, passed,
        { elapsedMs: recordedElapsedMs, afterMs: afterMs === undefined ? null : Math.round(afterMs) });
      if (after) recordCheck(report, 'performance', `${name} action-to-render within budget`, passed,
        { elapsedMs: recordedElapsedMs, afterMs: afterMs === undefined ? null : Math.round(afterMs), budgetMs });
    }
  }, { timeout: STEP_TIMEOUT_MS });

const readGrid = async (page, kind = 'saved') => page.evaluate((kind) => {
  const proposal = kind === 'proposal';
  const table = proposal ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"] table') : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return {ready:false,headers:[],rows:[],ariaRowCount:null};
  const tidy = value => String(value ?? '').replace(/\s+/g,' ').trim();
  const headers = proposal ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent)) : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
  const rows = proposal ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText))) : [...table.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
  return {ready:true,headers,rows,ariaRowCount:table.getAttribute('aria-rowcount')};
}, kind);

const exactRows = (report, name, grid, headers, rows) => {
  const actualRows = [...grid.rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expectedRows = [...rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const ok = grid.ready && JSON.stringify(grid.headers) === JSON.stringify(headers) && JSON.stringify(actualRows) === JSON.stringify(expectedRows);
  check(report, 'correctness', name, ok, { headers: grid.headers, expectedHeaders: headers, rows: actualRows, expectedRows, ariaRowCount: grid.ariaRowCount });
};

const requestJSON = async (url, body) => {
  const response = await fetch(url, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : null; } catch { value = { responseText: text.slice(0, 500) }; }
  if (!response.ok) throw new Error('API request ' + new URL(url).pathname + ' returned HTTP ' + response.status + ': ' + JSON.stringify(value).slice(0, 900));
  return value;
};

const apiRoot = (context, explorer) => context.target.apiUrl + '/api/v1/projects/' +
  encodeURIComponent(context.target.fixtureProject) + '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2';
const readBuilder = (context, explorer) => requestJSON(builderRequestURL(
  context.target.apiUrl, context.target.fixtureProject, explorer,
).toString());

const readPublishedInputs = async (context, explorer, builder) => {
  const endpoint = apiRoot(context, explorer) + '/construction-inputs';
  const entries = [];
  let cursor;
  do {
    const response = await requestJSON(endpoint, {
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      ...(cursor ? { cursor } : {}),
      limit: 100,
    });
    if (response.snapshotToken !== builder.catalog.snapshotToken ||
        response.draftVersion !== builder.draftVersion ||
        response.draftDigest !== builder.draftDigest ||
        response.datasetGeneration !== builder.catalog.generation) {
      throw new Error('Published-input read did not preserve the exact Builder snapshot and generation.');
    }
    entries.push(...response.entries);
    cursor = response.nextCursor || undefined;
  } while (cursor);
  return entries;
};

const documentByOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter((document) => document.output?.id === outputId);
  if (matches.length !== 1) throw new Error('Expected one table output ' + outputId + '; found ' + matches.length + '.');
  return matches[0];
};
const currentRevisionFor = currentPublishedRevisionForOutput;
const publishedRef = (entry) => JSON.stringify([entry.tableId, entry.revisionId, entry.outputId]);

const captureProposalRequests = (page, outputId, scope) => {
  const entries = [];
  const scopeRejections = [];
  const byRequest = new Map();
  const onRequest = request => {
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (!url.pathname.endsWith('/construction-proposals') || request.method() !== 'POST') return;
    let body;
    try { body = request.postDataJSON(); } catch { return; }
    if (body?.outputId !== outputId) return;
    const requestScope = classifyNativeBrowserApiRequest(request.url(), scope);
    const requestOwnedExplorer = requestScope.kind === 'capture' && requestScope.scope === 'owned-project-explorer';
    const entry = { requestId: entries.length + 1, requestURL: request.url(), body, status: undefined, responseURL: undefined, response: undefined,
      ...(!requestOwnedExplorer ? { scopeRejected: { kind: requestScope.kind, scope: requestScope.scope ?? null, reason: requestScope.reason ?? 'not-owned-explorer-route' } } : {}) };
    if (entry.scopeRejected) scopeRejections.push({ requestURL: request.url(), ...entry.scopeRejected });
    entries.push(entry);
    byRequest.set(request, entry);
  };
  const onResponse = response => {
    const entry = byRequest.get(response.request());
    if (!entry) return;
    entry.status = response.status();
    entry.responseURL = response.url();
    entry.responsePromise = response.json().then(value => { entry.response = value; }, error => { entry.responseReadError = String(error); });
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  return {
    startIndex: () => entries.length,
    stop: () => { page.off('request', onRequest); page.off('response', onResponse); },
    async find(joinType, afterIndex = 0) {
      const candidates = entries.slice(afterIndex).filter((entry) => {
        const step = entry.body?.candidateConstruction?.steps?.at(-1);
        return step?.operation?.combine?.kind === 'KEY_JOIN' && step.operation.combine.joinType === joinType;
      });
      const entry = candidates.at(-1);
      if (!entry) throw new Error('No automatic ' + joinType + ' KEY_JOIN proposal was sent for this output; rejected scope traffic=' + JSON.stringify(scopeRejections));
      await expect.poll(() => entry.response !== undefined || Boolean(entry.responseReadError), {
        timeout: STEP_TIMEOUT_MS,
        message: 'Timed out waiting for the exact ' + joinType + ' nullable Join proposal response.',
      }).toBe(true);
      if (entry.responseReadError) throw new Error('Could not read exact ' + joinType + ' proposal response: ' + entry.responseReadError);
      if (entry.response === null) throw new Error('Could not read exact ' + joinType + ' proposal response: response body was null.');
      entry.transportEvidence = nativeResponseScopeEvidence(entry.requestURL, entry.responseURL, scope);
      if (entry.scopeRejected) entry.transportEvidence = { ...entry.transportEvidence, ok: false, requestScopeRejected: entry.scopeRejected };
      return entry;
    },
    async findRemoval(stepID, afterIndex = 0) {
      const candidates = entries.slice(afterIndex).filter((entry) => {
        const body = entry.body;
        return body?.outputId === outputId && Array.isArray(body.removeStepIds) &&
          body.removeStepIds.includes(stepID) && body.candidateConstruction?.steps?.length === 0;
      });
      const entry = candidates.at(-1);
      if (!entry) throw new Error('No scoped removal proposal was sent for step ' + stepID + '.');
      await expect.poll(() => entry.response !== undefined || Boolean(entry.responseReadError), {
        timeout: STEP_TIMEOUT_MS,
        message: 'Timed out waiting for the exact nullable KEY_JOIN removal response.',
      }).toBe(true);
      if (entry.responseReadError) throw new Error('Could not read the exact removal proposal response: ' + entry.responseReadError);
      if (entry.response === null) throw new Error('Could not read the exact removal proposal response: response body was null.');
      entry.transportEvidence = nativeResponseScopeEvidence(entry.requestURL, entry.responseURL, scope);
      if (entry.scopeRejected) entry.transportEvidence = { ...entry.transportEvidence, ok: false, requestScopeRejected: entry.scopeRejected };
      return entry;
    },
  };
};

const checkProposalBinding = async (report, page, capture, afterIndex, joinType, targetOutputId, columns, rows, expectedKeyIDs) => {
  const entry = await capture.find(joinType, afterIndex);
  const dom = await evaluate(page, `(()=>({
    proposalId:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id')??null,
    receiptId:document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id')??null
  }))()`);
  const evidence = constructionProposalPreviewEvidence({
    responseStatus: entry.status,
    response: entry.response,
    requestBody: entry.body,
    expectedOutputId: targetOutputId,
    expectedColumns: columns,
    expectedRows: rows,
    domProposalId: dom.proposalId,
    domReceiptId: dom.receiptId,
  });
  const combineKeys = entry.body?.candidateConstruction?.steps?.at(-1)?.operation?.combine?.keys ?? [];
  const keyPairBound = combineKeys.length === 1 && combineKeys[0]?.leftColumnId === expectedKeyIDs[0] && combineKeys[0]?.rightColumnId === expectedKeyIDs[1];
  const transportBound = entry.transportEvidence?.ok === true;
  check(report, 'correctness', joinType + ' preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows', evidence.ok && keyPairBound && transportBound,
    { ...evidence, transportEvidence: entry.transportEvidence, transportBound, keyPairBound, combineKeys, expectedKeyIDs, requestId: entry.requestId, outputId: entry.body.outputId, proposalId: dom.proposalId, receiptId: dom.receiptId });
};

const checkRemovalProposalBinding = async (report, page, capture, afterIndex, targetOutputId, stepID, builderSnapshot) => {
  const entry = await capture.findRemoval(stepID, afterIndex);
  const dom = await evaluate(page, `(()=>({
    proposalId:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id')??null,
    receiptId:document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id')??null
  }))()`);
  const evidence = removalProposalEvidence({
    responseStatus: entry.status,
    response: entry.response,
    requestBody: entry.body,
    expectedOutputId: targetOutputId,
    expectedStepID: stepID,
    expectedSnapshotToken: builderSnapshot.catalog?.snapshotToken,
    expectedDraftVersion: builderSnapshot.draftVersion,
    expectedDraftDigest: builderSnapshot.draftDigest,
    domProposalId: dom.proposalId,
    domReceiptId: dom.receiptId,
    transportEvidence: entry.transportEvidence,
  });
  check(report, 'correctness', 'nullable KEY_JOIN removal preview receipt binds the exact scoped request, removed step, target, snapshot, and DOM receipt', evidence.ok,
    { ...evidence, requestId: entry.requestId, requestURL: entry.requestURL, responseURL: entry.responseURL, domProposalId: dom.proposalId, domReceiptId: dom.receiptId });
};

const selectTarget = async (page, outputId) => {
  const selector = '[data-testid="construction-table-' + outputId + '"]';
  const selected = await evaluate(page, 'document.querySelector(' + JSON.stringify(selector) + ')?.getAttribute("aria-current")==="page"');
  if (!selected) await click(page, selector);
  await waitFor(page, 'document.querySelector(' + JSON.stringify(selector) + ')?.getAttribute("aria-current")==="page"', 10000);
};

const reloadTarget = (report, page, outputId, expectedRows, name) => recordBrowserTiming(report, page, {
  name,
  action: async () => {
    await reload(page, workspaceReady);
    await selectTarget(page, outputId);
  },
  after: savedPreview(expectedRows),
  timeout: 5000,
});

const openSavedEdit = (report, page, stepID, name) => recordBrowserTiming(report, page, {
  name,
  action: () => editSavedStep(page, stepID),
  after: "Boolean(document.querySelector('[data-testid=\"construction-combine-editor\"]'))",
  timeout: 5000,
});

const captureCreateTableCommand = (page, scope) => {
  const entries = [];
  const scopeRejections = [];
  const byRequest = new Map();
  const onRequest = request => {
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (!url.pathname.endsWith('/authoring/v2/commands') || request.method() !== 'POST') return;
    let body;
    try { body = request.postDataJSON(); } catch { return; }
    if (!body?.commands?.some((command) => command?.type === 'CREATE_TABLE')) return;
    const requestScope = classifyNativeBrowserApiRequest(request.url(), scope);
    const requestOwnedExplorer = requestScope.kind === 'capture' && requestScope.scope === 'owned-project-explorer';
    const entry = { requestId: entries.length + 1, requestURL: request.url(), body, status: undefined, responseURL: undefined, response: undefined,
      ...(!requestOwnedExplorer ? { scopeRejected: { kind: requestScope.kind, scope: requestScope.scope ?? null, reason: requestScope.reason ?? 'not-owned-explorer-route' } } : {}) };
    if (entry.scopeRejected) scopeRejections.push({ requestURL: request.url(), ...entry.scopeRejected });
    entries.push(entry);
    byRequest.set(request, entry);
  };
  const onResponse = response => {
    const entry = byRequest.get(response.request());
    if (!entry) return;
    entry.status = response.status();
    entry.responseURL = response.url();
    entry.responsePromise = response.json().then(value => { entry.response = value; }, error => { entry.responseReadError = String(error); });
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  return {
    stop: () => { page.off('request', onRequest); page.off('response', onResponse); },
    read: async () => {
      let entry;
      await expect.poll(() => {
        const candidates = entries.filter((item) => item.body?.commands?.some((command) => command?.type === 'CREATE_TABLE'));
        if (candidates.length > 1) throw new Error('Expected one native CREATE_TABLE request while opening Combine; found ' + candidates.length);
        entry = candidates[0];
        return Boolean(entry?.response || entry?.responseReadError);
      }, { timeout: STEP_TIMEOUT_MS, message: 'Timed out capturing native CREATE_TABLE and returned workspace.' }).toBe(true);
      if (entry.responseReadError) throw new Error('Could not read native Combine target creation response: ' + entry.responseReadError);
      entry.transportEvidence = nativeResponseScopeEvidence(entry.requestURL, entry.responseURL, scope);
      if (entry.scopeRejected) entry.transportEvidence = { ...entry.transportEvidence, ok: false, requestScopeRejected: entry.scopeRejected, scopeRejections };
      return entry;
    },
  };
};

const startCombineTarget = async (context, page, report, explorer, observationOutputId, sourceBuilder) => {
  await selectTarget(page, observationOutputId);
  const scope = nativeApiScope(context, explorer);
  const capture = captureCreateTableCommand(page, scope);
  try {
    await recordBrowserTiming(report, page, {
      name: 'open native Combine for nullable Join',
      action: () => click(page, 'button[data-testid="construction-action-combine"]'),
      after: 'Boolean(document.querySelector(\'[data-testid="construction-operation-editor"][data-operation-family="COMBINE"][data-output-id]\')) && Boolean(document.querySelector(\'[data-testid="construction-combine-editor"]\'))',
      timeout: 5000,
    });
    const command = await capture.read();
    check(report, 'correctness', 'native CREATE_TABLE request and response bind to the owned UI proxy project and Explorer route', command.transportEvidence.ok, command.transportEvidence);
    const mountedOutputId = await evaluate(page, 'document.querySelector(\'[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]\')?.getAttribute("data-output-id") ?? null');
    const expectedRootNodeIds = (sourceBuilder.catalog?.nodes ?? []).filter((node) => node.resourceType === 'Observation' && node.rowRootEligible).map((node) => node.nodeId);
    const previousOutputIds = (sourceBuilder.workspace?.documents ?? []).map((document) => document.output?.id).filter(Boolean);
    const evidence = nativeCombineTargetBindingEvidence({
      requestBody: command.body,
      responseStatus: command.status,
      response: command.response,
      expectedRootNodeIds,
      expectedRootResourceType: 'Observation',
      previousOutputIds,
      mountedOutputId,
    });
    check(report, 'correctness', 'native Combine creates the scoped rooted empty target without adding an authored step or output column', evidence.ok, evidence);
    if (!evidence.ok) throw new Error('Native Combine target did not bind its creation request, response, and mounted editor: ' + JSON.stringify(evidence));
    return { outputId: evidence.outputId, rootNodeId: evidence.rootNodeId };
  } finally { capture.stop(); }
};

const chooseOperation = async (report, page, inputs) => {
  const selectorsReady = inputs.slice(0, 2).map((_, index) =>
    'Boolean(document.querySelector(\'select[aria-label="Input table ' + (index + 1) + '"]:not(:disabled)\'))').join('&&');
  await recordBrowserTiming(report, page, {
    name: 'choose nullable KEY_JOIN and load its input selectors',
    action: () => click(page, 'button[data-testid="construction-combine-choice-key_join"]'),
    after: selectorsReady,
    timeout: 5000,
  });
  for (let index = 0; index < inputs.length; index += 1) {
    const value = publishedRef(inputs[index]);
    const actual = await setSelectValue(page, 'select[aria-label="Input table ' + (index + 1) + '"]', value);
    if (actual !== value) throw new Error('Nullable KEY_JOIN did not retain exact input revision ' + (index + 1));
  }
};

const configureOutput = async (page, index, name, label, inputIndex, columnID) => {
  await click(page, 'button', { name: 'Add output field' });
  await waitFor(page, 'Boolean(document.querySelector(\'input[aria-label="Output field ' + index + ' name"]\'))', 10000);
  await fill(page, 'input[aria-label="Output field ' + index + ' name"]', name);
  await fill(page, 'input[aria-label="Output field ' + index + ' label"]', label);
  if (columnID !== null) {
    await setSelectValue(page, 'select[aria-label="Output field ' + index + ' source field in input ' + (inputIndex + 1) + '"]', columnID);
  }
};

const editSavedStep = async (page, stepId) => {
  await click(page, '[data-testid="construction-history-step-' + stepId + '"]');
  await waitFor(page, 'Boolean(document.querySelector(\'[data-testid="construction-edit-step-' + stepId + '"]:not(:disabled)\'))', 10000);
  await click(page, '[data-testid="construction-edit-step-' + stepId + '"]');
  await waitFor(page, "Boolean(document.querySelector('[data-testid=\"construction-combine-editor\"]'))", 10000);
};

const assertSavedStep = (report, builder, target, expectedInputRefs, observationKeyID, reportKeyID, expectedJoinType, expectedStepID) => {
  const document = documentByOutput(builder, target.outputId);
  const steps = document.construction?.steps ?? [];
  const step = steps[0];
  const actualInputs = (step?.inputs ?? []).map((input) => [input.tableId, input.revisionId, input.outputId]);
  const actualRefs = expectedInputRefs.map((entry) => [entry.tableId, entry.revisionId, entry.outputId]);
  const keys = step?.operation?.combine?.keys ?? [];
  const ok = steps.length === 1 && step?.operation?.combine?.kind === 'KEY_JOIN' &&
    step?.operation?.combine?.joinType === expectedJoinType &&
    (!expectedStepID || step.id === expectedStepID) &&
    JSON.stringify(actualInputs) === JSON.stringify(actualRefs) && keys.length === 1 &&
    keys[0]?.leftColumnId === observationKeyID && keys[0]?.rightColumnId === reportKeyID;
  check(report, 'persistence', expectedJoinType + ' saved step retains exact current revisions and the nullable subject.reference key pair', ok,
    { stepId: step?.id, expectedStepId: expectedStepID ?? null, inputs: actualInputs, expectedInputs: actualRefs, keys, expectedKeys: [{ leftColumnId: observationKeyID, rightColumnId: reportKeyID }], joinType: step?.operation?.combine?.joinType, expectedJoinType });
  return { document, step };
};

const expectedObservations = [
  { id: 'combine-observation-final-1', status: 'final', valueInteger: 10 },
  { id: 'combine-observation-final-2', status: 'final', valueInteger: 20 },
  { id: 'combine-observation-preliminary', status: 'preliminary', valueInteger: 30 },
  { id: 'combine-observation-unmatched', status: 'unknown', valueInteger: 40 },
];
const expectedReports = [
  { id: 'combine-observation-final-1', status: 'final' },
  { id: 'combine-observation-final-2', status: 'final' },
  { id: 'combine-observation-preliminary', status: 'preliminary' },
];
const documentByRoot = (builder, resourceType) => {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.rootResourceType === resourceType);
  if (matches.length !== 1) throw new Error('Expected exactly one ' + resourceType + ' source table; found ' + matches.length + '.');
  return matches[0];
};

const createAndPublishSources = async (context, page, report, _includePatient, rawFieldsByResource) => {
  await page.goto(browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  await recordBrowserTiming(report, page, {
    name: 'open Explorer creation',
    action: () => click(page, 'text=New explorer'),
    after: "Boolean(document.querySelector('#new-explorer-name'))",
  });
  const title = `Verify ${context.runID.slice(-10)} combine`;
  await fill(page, '#new-explorer-name', title);
  await recordBrowserTiming(report, page, {
    name: 'create blank Explorer',
    action: () => click(page, 'button', { name: 'Create blank' }),
    after: "document.querySelector('select[aria-label=\"Explorer\"]')?.selectedOptions[0]?.textContent?.trim()===" + JSON.stringify(title) + "&&document.body.innerText.includes('Build your first table')",
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  report.target.explorer = explorer;
  check(report, 'persistence', 'created a fresh Explorer distinct from the bootstrap',
    Boolean(explorer) && explorer !== context.target.bootstrapExplorerId,
    { explorer, bootstrapExplorerId: context.target.bootstrapExplorerId });

  const addRoot = async (resourceType, tableTitle, rows) => {
    await fill(page, '#first-table-name', tableTitle);
    await recordBrowserTiming(report, page, { name: `create ${resourceType} source table with its direct identity`, action: () => click(page, 'button', { name: `Choose ${resourceType} rows` }), timeout: 5000,
      after: "Boolean(document.querySelector('[data-testid=\"construction-workspace\"]'))&&document.body.innerText.includes('" + tableTitle + "')" });
    await waitFor(page, savedPreview(rows), 30000);
    const grid = await readGrid(page);
    const idIndex = grid.headers.findIndex(header => header.toLowerCase() === resourceType.toLowerCase() + ' id' || header.toLowerCase() === 'id');
    const ids = idIndex < 0 ? [] : grid.rows.map(row => row[idIndex]).sort();
    const expectedIDs = (resourceType === 'Observation' ? expectedObservations : expectedReports).map(row => row.id);
    check(report, 'correctness', resourceType + ' source starts with every literal fixture identity', JSON.stringify(ids) === JSON.stringify([...expectedIDs].sort()), { headers: grid.headers, ids, expectedIDs });
  };
  const addFields = async (resourceType, paths, rows) => {
    await click(page, '[data-testid="construction-action-add-columns"]');
    await waitFor(page, "Boolean(document.querySelector('[aria-label=\"Add columns editor\"]'))", 10000);
    await click(page, 'button', { name: 'Fields and related data' });
    await click(page, 'text=Raw FHIR fields (advanced)');
    for (const field of paths) {
      const checkbox = page.getByRole('checkbox', { name: `Select ${resourceType}.${field}`, exact: true });
      await checkbox.check();
    }
    const label = `Add ${paths.length} selected feature${paths.length === 1 ? '' : 's'}`;
    await click(page, 'button', { name: label });
    await waitFor(page, "[...document.querySelectorAll('button')].some(button=>button.innerText.trim()==='Apply columns'&&!button.disabled)", 30000);
    await recordBrowserTiming(report, page, { name: `apply ${resourceType} source fields and render its preview`, action: () => click(page, 'button', { name: 'Apply columns' }), timeout: 5000,
      after: savedPreview(rows) });
    await click(page, 'button', { name: 'Close operation editor' });
  };
  await addRoot('Observation', 'Observations', expectedObservations.length);
  await addFields('Observation', rawFieldsByResource.Observation, expectedObservations.length);
  await click(page, '[data-testid="construction-new-table"]');
  await waitFor(page, "Boolean(document.querySelector('#first-table-name'))", 10000);
  await addRoot('DiagnosticReport', 'Diagnostic reports', expectedReports.length);
  await addFields('DiagnosticReport', rawFieldsByResource.DiagnosticReport, expectedReports.length);
  const publishPath = `/api/v1/projects/${encodeURIComponent(context.target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/publish`;
  const publication = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).origin === new URL(context.target.uiUrl).origin && new URL(response.url()).pathname === publishPath, { timeout: 5000 });
  await recordBrowserTiming(report, page, { name: 'publish both exact source tables', action: () => click(page, 'button', { name: 'Publish' }), timeout: 5000,
    after: async () => { const response = await publication; if (!response.ok()) throw new Error('source table publication returned HTTP ' + response.status()); } });
  const publishResponse = await publication;
  check(report, 'correctness', 'native source-table publication completed successfully', publishResponse.ok(), { status: publishResponse.status(), path: publishPath });
  const started = Date.now();
  const builder = await readBuilder(context, explorer);
  const observation = documentByRoot(builder, 'Observation');
  const diagnosticReport = documentByRoot(builder, 'DiagnosticReport');
  const docs = { observation, report: diagnosticReport, documents: [observation, diagnosticReport] };
  const initialScope = builder.catalog?.authorizationScopeDigest;
  const scopeInvalid = builder.catalog?.generation !== context.target.fixtureGeneration || !builder.catalog?.snapshotToken || !builder.draftDigest || !initialScope;
  check(report, 'correctness', 'source Builder snapshot is bound to the expected generation, draft, and authorization scope', !scopeInvalid,
    { generation: builder.catalog?.generation, expectedGeneration: context.target.fixtureGeneration, snapshotToken: builder.catalog?.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, authorizationScopeDigest: initialScope });
  if (scopeInvalid) throw new Error('The source Builder snapshot is not bound to the expected generation, draft, and authorization scope.');
  const entries = await readPublishedInputs(context, explorer, builder);
  report.publishedSourceCatalog = { snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, entries };
  const revisions = { Observation: currentRevisionForOutput(builder, observation.output.id, entries), DiagnosticReport: currentRevisionForOutput(builder, diagnosticReport.output.id, entries) };
  const columns = {
    observationID: findColumn(revisions.Observation, 'Observation', 'id'), observationStatus: findColumn(revisions.Observation, 'Observation', 'status'), observationInteger: findColumn(revisions.Observation, 'Observation', 'valueInteger'),
    reportID: findColumn(revisions.DiagnosticReport, 'DiagnosticReport', 'id'), reportStatus: findColumn(revisions.DiagnosticReport, 'DiagnosticReport', 'status'),
  };
  const schemaOK = isCombineInputIDColumn(columns.observationID, 'KEY_JOIN') && isCombineInputIDColumn(columns.reportID, 'KEY_JOIN') && isScalarStringColumn(columns.observationStatus) && isScalarStringColumn(columns.reportStatus) && isNumericClickHouseType(columns.observationInteger.clickhouseType);
  check(report, 'correctness', 'source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value', schemaOK, columns);
  if (!schemaOK) throw new Error('Published source schema does not support nullable KEY_JOIN.');
  const identity = builderResponseIdentity(builder, context.target.apiUrl, context.target.fixtureProject, explorer, title, [
    { rootResourceType: 'Observation', title: 'Observations', outputId: observation.output.id },
    { rootResourceType: 'DiagnosticReport', title: 'Diagnostic reports', outputId: diagnosticReport.output.id },
  ]);
  check(report, 'correctness', 'Builder response matches the exact project-scoped route, workspace title, and source output identities', identity.bound, identity);
  if (!identity.bound) throw new Error('The Builder route and workspace do not match the created Explorer and source tables.');
  check(report, 'correctness', 'source revisions are published in the exact project, generation, and authorization scope', Boolean(revisions.Observation.tableId && revisions.DiagnosticReport.tableId && builder.catalog.generation === context.target.fixtureGeneration && builder.catalog.authorizationScopeDigest && entries.length >= 2), { project: context.target.fixtureProject, generation: builder.catalog.generation, authorizationScopeDigest: builder.catalog.authorizationScopeDigest, revisions });
  const sourceSnapshot = docs.documents.map(snapshotSourceDocument);
  const apiFingerprint = createHash('sha256').update(JSON.stringify({ project: context.target.fixtureProject, generation: builder.catalog.generation, explorer,
    authorizationScopeDigest: initialScope, snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
    revisions: Object.fromEntries(Object.entries(revisions).map(([key, entry]) => [key, { tableId: entry.tableId, revisionId: entry.revisionId, outputId: entry.outputId, columns: entry.columns }])) })).digest('hex');
  const api = { builder, entries, revisions, columns, sourceSnapshot, sourceOutputIds: docs.documents.map(document => document.output.id).sort(), apiFingerprint, apiElapsedMs: Date.now() - started };
  check(report, 'performance', 'published source API and schema fingerprint captured within five seconds', api.apiElapsedMs <= 5000, { elapsedMs: api.apiElapsedMs, generation: builder.catalog.generation });
  return { explorer, docs, api };
};
const currentRevisionForOutput = (builder, outputId, entries) => currentPublishedRevisionForOutput(entries, outputId);

export const nullableJoinWorkflow = async ({ page, report, action }, context) => {
  assert.equal(context.custom, false, 'nullable Combine authoring requires an owned isolated fixture.');
  assert.equal(context.seed?.fresh, true, 'nullable Combine authoring requires a fresh verification project.');
  const rawObservations = fixtureRows(context.target.fixtureDir, 'Observation.ndjson');
  const rawReports = fixtureRows(context.target.fixtureDir, 'DiagnosticReport.ndjson');
  check(report, 'correctness', 'fixture contains four Observations and three DiagnosticReports with non-empty resource IDs',
    rawObservations.length === 4 && rawReports.length === 3 && rawObservations.every((row) => Boolean(row.id)) && rawReports.every((row) => Boolean(row.id)),
    { observationIDs: rawObservations.map((row) => row.id), diagnosticReportIDs: rawReports.map((row) => row.id) });
  const observationKeyRows = rawObservations.map((row) => [row.id, row.subject?.reference ?? null]);
  const reportKeyRows = rawReports.map((row) => [row.id, row.subject?.reference ?? null]);
  check(report, 'correctness', 'nullable-key fixture matches the exact ID-to-reference maps with two shared keys and NULL on both sides',
    JSON.stringify(observationKeyRows) === JSON.stringify([
      ['combine-observation-final-1', 'Patient/combine-null-key-match'],
      ['combine-observation-final-2', null],
      ['combine-observation-preliminary', 'Patient/combine-null-key-preliminary'],
      ['combine-observation-unmatched', 'Patient/combine-null-key-unmatched'],
    ]) && JSON.stringify(reportKeyRows) === JSON.stringify([
      ['combine-observation-final-1', null],
      ['combine-observation-final-2', 'Patient/combine-null-key-match'],
      ['combine-observation-preliminary', 'Patient/combine-null-key-preliminary'],
    ]),
    { observations: observationKeyRows, reports: reportKeyRows });

  const prepared = await createAndPublishSources(context, page, report, false, {
    Observation: ['status', 'valueInteger', 'subject.reference'],
    DiagnosticReport: ['status', 'subject.reference'],
  });
  const { explorer, docs, api } = prepared;
  const observationKey = findColumn(api.revisions.Observation, 'Observation', 'subject.reference');
  const reportKey = findColumn(api.revisions.DiagnosticReport, 'DiagnosticReport', 'subject.reference');
  const keyMetadataOK = [observationKey, reportKey].every((column) => column.clickhouseType === 'Nullable(String)' && column.nullable === true && column.repeated === false);
  check(report, 'correctness', 'both published subject.reference fields are nullable scalar strings', keyMetadataOK,
    { observationKey, reportKey });
  if (!keyMetadataOK) throw new Error('Published subject.reference keys are not exactly nullable scalar strings.');
  check(report, 'correctness', 'nullable key metadata is distinct from the required resource IDs',
    observationKey.id !== api.columns.observationID.id && reportKey.id !== api.columns.reportID.id &&
    typeof api.columns.observationID.id === 'string' && typeof api.columns.reportID.id === 'string',
    { observationKeyID: observationKey.id, observationID: api.columns.observationID.id, reportKeyID: reportKey.id, reportID: api.columns.reportID.id });

  const innerRows = [
    ['combine-observation-final-1', 'combine-observation-final-2'],
    ['combine-observation-preliminary', 'combine-observation-preliminary'],
  ];
  const leftRows = [
    ['combine-observation-final-1', 'combine-observation-final-2'],
    ['combine-observation-final-2', '—'],
    ['combine-observation-preliminary', 'combine-observation-preliminary'],
    ['combine-observation-unmatched', '—'],
  ];
  report.target.fixtureRawOracle = {
    key: 'Observation.subject.reference = DiagnosticReport.subject.reference under ordinary SQL NULL equality',
    innerRows,
    leftRows,
    nullMatchesNull: false,
    requiredResourceIDs: { observations: rawObservations.map((row) => row.id), reports: rawReports.map((row) => row.id) },
  };

  const target = await startCombineTarget(context, page, report, explorer, docs.observation.output.id, api.builder);
  report.target.combineTarget = target;
  const builderAtTarget = await readBuilder(context, explorer);
  const preCombineTargetDocument = structuredClone(documentByOutput(builderAtTarget, target.outputId));
  const entries = await readPublishedInputs(context, explorer, builderAtTarget);
  const observationRevision = currentRevisionFor(entries, docs.observation.output.id);
  const reportRevision = currentRevisionFor(entries, docs.report.output.id);
  const pinnedCurrentInputs = publishedRef(observationRevision) === publishedRef(api.revisions.Observation) &&
    publishedRef(reportRevision) === publishedRef(api.revisions.DiagnosticReport) &&
    builderAtTarget.catalog?.generation === context.target.fixtureGeneration &&
    builderAtTarget.catalog?.authorizationScopeDigest === api.builder.catalog.authorizationScopeDigest;
  check(report, 'correctness', 'nullable Join pins the exact current published revisions in the expected generation', pinnedCurrentInputs,
    { observationRevision, reportRevision, expected: api.revisions, generation: context.target.fixtureGeneration });
  if (!pinnedCurrentInputs) throw new Error('Nullable Join source revisions changed or are out of scope.');

  const proposalCapture = captureProposalRequests(page, target.outputId, nativeApiScope(context, explorer));
  try {
  await chooseOperation(report, page, [observationRevision, reportRevision]);
  await setSelectValue(page, 'select[aria-label="Matching pair 1 first field"]', observationKey.id);
  await setSelectValue(page, 'select[aria-label="Matching pair 1 second field"]', reportKey.id);
  await setSelectValue(page, 'select[aria-label="If a row in the first table has no match"]', 'INNER');
  await configureOutput(page, 1, 'observation_id', 'Observation ID', 0, api.columns.observationID.id);
  await configureOutput(page, 2, 'report_id', 'Report ID', 1, null);
  await recordBrowserTiming(report, page, {
    name: 'render INNER nullable-key preview',
    action: async () => {
      const actual = await setSelectValue(page, 'select[aria-label="Output field 2 source field in input 2"]', api.columns.reportID.id);
      if (actual !== api.columns.reportID.id) throw new Error('Report ID projection was not selected.');
    },
    after: proposalPreview(innerRows.length),
    timeout: 5000,
  });
  await checkProposalBinding(report, page, proposalCapture, 0, 'INNER', target.outputId,
    ['observation_id', 'report_id'], innerRows, [observationKey.id, reportKey.id]);
  exactRows(report, 'INNER nullable Join matches exactly two equal non-NULL subject references and does not match NULL to NULL',
    await readGrid(page, 'proposal'), ['Observation ID', 'Report ID'], innerRows);

  await recordBrowserTiming(report, page, {
    name: 'Apply INNER nullable Join',
    action: () => click(page, '[data-testid="construction-apply-proposal"]'),
    after: '!document.querySelector(\'[data-testid="construction-proposal-panel"]\') && Boolean(document.querySelector(\'[data-testid="construction-history"]\')) && ' + savedPreview(innerRows.length),
    timeout: 5000,
  });
  const appliedInner = await readBuilder(context, explorer);
  const innerSaved = assertSavedStep(report, appliedInner, target, [observationRevision, reportRevision], observationKey.id, reportKey.id, 'INNER');
  const savedStepID = innerSaved.step.id;
  exactRows(report, 'INNER applied rows retain the exact nullable-key matches', await readGrid(page), ['Observation ID', 'Report ID'], innerRows);
  await reloadTarget(report, page, target.outputId, innerRows.length, 'reload INNER nullable-key table');
  exactRows(report, 'INNER nullable-key rows survive Builder reload', await readGrid(page), ['Observation ID', 'Report ID'], innerRows);

  const beforeCancelBuilder = await readBuilder(context, explorer);
  await openSavedEdit(report, page, savedStepID, 'open saved INNER nullable Join for Cancelled LEFT edit');
  const leftCancelRequestStart = proposalCapture.startIndex();
  await recordBrowserTiming(report, page, {
    name: 'preview LEFT nullable Join before Cancel',
    action: () => setSelectValue(page, 'select[aria-label="If a row in the first table has no match"]', 'LEFT'),
    after: proposalPreview(leftRows.length),
    timeout: 5000,
  });
  await checkProposalBinding(report, page, proposalCapture, leftCancelRequestStart, 'LEFT', target.outputId,
    ['observation_id', 'report_id'], [
      ['combine-observation-final-1', 'combine-observation-final-2'],
      ['combine-observation-final-2', null],
      ['combine-observation-preliminary', 'combine-observation-preliminary'],
      ['combine-observation-unmatched', null],
  ], [observationKey.id, reportKey.id]);
  exactRows(report, 'LEFT preview keeps unmatched left rows and never matches NULL keys together', await readGrid(page, 'proposal'), ['Observation ID', 'Report ID'], leftRows);
  await recordBrowserTiming(report, page, {
    name: 'Cancel LEFT nullable Join edit',
    action: () => click(page, '[data-testid="construction-cancel-proposal"]'),
    after: "Boolean(document.querySelector('[data-testid=\"construction-history\"]')) && !document.querySelector('[data-testid=\"construction-combine-editor\"]') && !document.querySelector('[data-testid=\"construction-proposal-panel\"]')",
    timeout: 5000,
  });
  await reloadTarget(report, page, target.outputId, innerRows.length, 'reload saved INNER after LEFT Cancel');
  const cancelledBuilder = await readBuilder(context, explorer);
  const cancelState = builderCancelStateEvidence(beforeCancelBuilder, cancelledBuilder);
  check(report, 'persistence', 'Cancel leaves the full Builder workspace, draft version, and digest unchanged after reload', cancelState.ok, cancelState);
  assertSavedStep(report, cancelledBuilder, target, [observationRevision, reportRevision], observationKey.id, reportKey.id, 'INNER', savedStepID);
  exactRows(report, 'Cancel leaves saved INNER nullable Join rows unchanged after reload', await readGrid(page), ['Observation ID', 'Report ID'], innerRows);

  await openSavedEdit(report, page, savedStepID, 'reopen saved INNER nullable Join for LEFT Apply');
  const leftApplyRequestStart = proposalCapture.startIndex();
  await recordBrowserTiming(report, page, {
    name: 'preview LEFT nullable Join for Apply',
    action: () => setSelectValue(page, 'select[aria-label="If a row in the first table has no match"]', 'LEFT'),
    after: proposalPreview(leftRows.length),
    timeout: 5000,
  });
  await checkProposalBinding(report, page, proposalCapture, leftApplyRequestStart, 'LEFT', target.outputId,
    ['observation_id', 'report_id'], [
      ['combine-observation-final-1', 'combine-observation-final-2'],
      ['combine-observation-final-2', null],
      ['combine-observation-preliminary', 'combine-observation-preliminary'],
      ['combine-observation-unmatched', null],
    ], [observationKey.id, reportKey.id]);
  exactRows(report, 'LEFT preview before Apply has the literal unmatched-null rows', await readGrid(page, 'proposal'), ['Observation ID', 'Report ID'], leftRows);
  await recordBrowserTiming(report, page, {
    name: 'Apply LEFT nullable Join edit',
    action: () => click(page, '[data-testid="construction-apply-proposal"]'),
    after: '!document.querySelector(\'[data-testid="construction-proposal-panel"]\') && ' + savedPreview(leftRows.length),
    timeout: 5000,
  });
  const appliedLeft = await readBuilder(context, explorer);
  const leftSaved = assertSavedStep(report, appliedLeft, target, [observationRevision, reportRevision], observationKey.id, reportKey.id, 'LEFT', savedStepID);
  exactRows(report, 'LEFT applied output contains the exact matched and unmatched rows', await readGrid(page), ['Observation ID', 'Report ID'], leftRows);
  await reloadTarget(report, page, target.outputId, leftRows.length, 'reload applied LEFT nullable-key table');
  exactRows(report, 'LEFT nullable Join rows and null projections survive Builder reload', await readGrid(page), ['Observation ID', 'Report ID'], leftRows);

  const beforeRemovalBuilder = await readBuilder(context, explorer);
  const firstRemovalStart = proposalCapture.startIndex();
  const openRemovalProposal = async () => {
    await click(page, '[data-testid="construction-history-step-' + leftSaved.step.id + '"]');
    await waitFor(page, 'Boolean(document.querySelector(\'[data-testid="construction-remove-step-' + leftSaved.step.id + '"]:not(:disabled)\'))', 10000);
    await click(page, '[data-testid="construction-remove-step-' + leftSaved.step.id + '"]');
  };
  await recordBrowserTiming(report, page, {
    name: 'open nullable KEY_JOIN removal proposal before Cancel',
    action: openRemovalProposal,
    after: proposalReady,
    timeout: 5000,
  });
  await checkRemovalProposalBinding(report, page, proposalCapture, firstRemovalStart, target.outputId, leftSaved.step.id, beforeRemovalBuilder);
  await recordBrowserTiming(report, page, {
    name: 'Cancel nullable KEY_JOIN removal proposal',
    action: () => click(page, '[data-testid="construction-cancel-proposal"]'),
    after: "Boolean(document.querySelector('[data-testid=\"construction-history\"]')) && !document.querySelector('[data-testid=\"construction-proposal-panel\"]')",
    timeout: 5000,
  });
  await reloadTarget(report, page, target.outputId, leftRows.length, 'reload saved LEFT after removal Cancel');
  const cancelledRemovalBuilder = await readBuilder(context, explorer);
  const removalCancelState = builderCancelStateEvidence(beforeRemovalBuilder, cancelledRemovalBuilder);
  check(report, 'persistence', 'Cancel removal leaves the full Builder workspace, draft version, and digest unchanged after reload', removalCancelState.ok, removalCancelState);
  assertSavedStep(report, cancelledRemovalBuilder, target, [observationRevision, reportRevision], observationKey.id, reportKey.id, 'LEFT', leftSaved.step.id);
  exactRows(report, 'Cancel removal preserves the exact LEFT nullable Join rows after reload', await readGrid(page), ['Observation ID', 'Report ID'], leftRows);

  const applyRemovalStart = proposalCapture.startIndex();
  await recordBrowserTiming(report, page, {
    name: 'reopen nullable KEY_JOIN removal proposal for Apply',
    action: openRemovalProposal,
    after: proposalReady,
    timeout: 5000,
  });
  await checkRemovalProposalBinding(report, page, proposalCapture, applyRemovalStart, target.outputId, leftSaved.step.id, cancelledRemovalBuilder);
  await recordBrowserTiming(report, page, {
    name: 'Apply nullable KEY_JOIN removal',
    action: () => click(page, '[data-testid="construction-apply-proposal"]'),
    after: emptyTargetReady(target.outputId),
    timeout: 5000,
  });
  await recordBrowserTiming(report, page, {
    name: 'reload nullable KEY_JOIN removal result',
    action: async () => { await reload(page, workspaceReady); await selectTarget(page, target.outputId); },
    after: emptyTargetReady(target.outputId),
  });
  const afterRemoval = await readBuilder(context, explorer);
  const restored = documentByOutput(afterRemoval, target.outputId);
  const restoredEvidence = rootedEmptyTargetRestorationEvidence(restored, preCombineTargetDocument, target);
  check(report, 'persistence', 'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document', restoredEvidence.ok,
    { ...restoredEvidence, before: preCombineTargetDocument, after: restored });

  const finalBuilder = await readBuilder(context, explorer);
  const currentSources = docs.documents.map((document) => snapshotSourceDocument(documentByOutput(finalBuilder, document.output.id)));
  const sourceIdentity = builderResponseIdentity(finalBuilder, context.target.apiUrl, context.target.fixtureProject, explorer,
    api.builder.workspace.explorer.title, docs.documents.map((document) => ({ rootResourceType: document.rootResourceType, title: document.output.title, outputId: document.output.id })));
  const sourceUnchanged = sameSourceDocuments(currentSources, api.sourceSnapshot);
  check(report, 'persistence', 'both published nullable-key source tables remain byte-structured unchanged', sourceUnchanged,
    { outputIds: api.sourceOutputIds, before: api.sourceSnapshot, after: currentSources });
  const scopeStable = sourceIdentity.bound && finalBuilder.catalog?.generation === context.target.fixtureGeneration &&
    finalBuilder.catalog?.authorizationScopeDigest === api.builder.catalog.authorizationScopeDigest;
  check(report, 'persistence', 'project, generation, Explorer, and authorization scope remain stable', scopeStable,
    { project: context.target.fixtureProject, generation: finalBuilder.catalog?.generation, expectedGeneration: context.target.fixtureGeneration,
      requestURL: sourceIdentity.requestURL, workspaceTitle: sourceIdentity.workspaceTitle, outputIDs: sourceIdentity.outputIDs,
      authorizationScopeDigest: finalBuilder.catalog?.authorizationScopeDigest, expectedAuthorizationScopeDigest: api.builder.catalog.authorizationScopeDigest });
  report.target.explorer = explorer;
  report.target.nullableCombineTarget = target;
  } finally { proposalCapture.stop(); }
};
