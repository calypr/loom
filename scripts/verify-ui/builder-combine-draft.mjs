import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { isActionable, recordCheck, recordUntested } from './report.mjs';
import { proposalPreviewReadinessExpression } from './proposal-preview-readiness.mjs';
import {
  appendGroupedCounts,
  builderDraftStateEvidence,
  canceledDraftEvidence,
  constructionCandidateWireEquivalent,
  currentDraftSourceEvidence,
  groupCounts,
  groupedPivotRows,
  joinGroupPivotRows,
  joinGroupedCounts,
  joinPivotRows,
  sourceRecompileEvidence,
  workspaceOutputOption,
} from './builder-combine-draft-helpers.mjs';
import { nativeCombineTargetBindingEvidence } from './builder-combine-helpers.mjs';

const expectedPatients = [{ id: 'combine-fixture-patient', gender: 'female' }];
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
const proposalPanel = '[data-testid="construction-proposal-panel"]';
const proposalPreview = '[data-testid="construction-proposal-preview"][data-preview-status="ready"]';
const workspaceReady = "Boolean(document.querySelector('[data-testid=construction-workspace]'))";
const selectedOutputReady = (outputId) => `Boolean(document.querySelector('[data-testid=construction-workspace]'))&&document.querySelector(${JSON.stringify('[data-testid="construction-table-' + outputId + '"]')})?.getAttribute('aria-current')==='page'`;
const savedPreview = (rows) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(p?.dataset.previewStatus==='ready'&&t&&t.getAttribute('aria-rowcount')===${JSON.stringify(String(rows + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
const savedPreviewOutput = (rows, outputId) => savedPreview(rows) + `&&document.querySelector('[data-testid="construction-preview"]')?.dataset.previewOutputId===${JSON.stringify(outputId)}`;
const rootRowsPreview = (expectedIDs) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');if(p?.dataset.previewStatus!=='ready'||!p.dataset.previewOutputId||!t||t.getAttribute('aria-rowcount')!==${JSON.stringify(String(expectedIDs.length + 1))}||document.body.innerText.includes('Loading your table…')||document.body.innerText.includes('Preview failed:'))return false;const tidy=x=>String(x??'').replace(/\\s+/g,' ').trim();const headers=[...t.querySelectorAll('[role="columnheader"]')].map(c=>tidy(c.textContent));const idIndex=headers.findIndex(h=>/(^id$|\\bid\\b)/i.test(h));if(idIndex<0)return false;const rows=[...t.querySelectorAll('[role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>tidy(c.innerText)));const ids=rows.map(row=>row[idIndex]).sort();return JSON.stringify(ids)===JSON.stringify(${JSON.stringify([...expectedIDs].sort())})})()`;

const check = (report, dimension, name, passed, evidence = {}) => {
  if (!['correctness', 'persistence', 'usability', 'performance'].includes(dimension) || typeof passed !== 'boolean') {
    throw new TypeError('Draft Combine checks need a named dimension and boolean result.');
  }
  recordCheck(report, dimension, name, passed, evidence);
  if (!passed) throw new Error('required draft Combine check failed: ' + name + '; evidence=' + JSON.stringify(evidence).slice(0, 1600));
};

const parseNDJSON = (path) => readFileSync(path, 'utf8').split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
const fixture = (directory) => {
  const patients = parseNDJSON(join(directory, 'Patient.ndjson')).map(({ id, gender }) => ({ id, gender }));
  const observations = parseNDJSON(join(directory, 'Observation.ndjson')).map(({ id, status, valueInteger }) => ({ id, status, valueInteger }));
  const reports = parseNDJSON(join(directory, 'DiagnosticReport.ndjson')).map(({ id, status }) => ({ id, status }));
  assert.deepEqual(patients, expectedPatients);
  assert.deepEqual(observations, expectedObservations);
  assert.deepEqual(reports, expectedReports);
  return { patients, observations, reports };
};

const apiRoot = (context, explorer) => context.target.apiUrl + '/api/v1/projects/' +
  encodeURIComponent(context.target.fixtureProject) + '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2';

const requestJSON = async (url, body) => {
  const response = await fetch(url, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  const value = text ? JSON.parse(text) : null;
  if (!response.ok) throw new Error('read-only Builder request ' + new URL(url).pathname + ' returned HTTP ' + response.status);
  return value;
};

const readBuilder = (context, explorer) => requestJSON(
  context.target.apiUrl + '/api/v1/projects/' + encodeURIComponent(context.target.fixtureProject) +
  '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2/builder',
);

const documentByOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter((document) => document.output?.id === outputId);
  if (matches.length !== 1) throw new Error('Expected one workspace output ' + outputId + '; found ' + matches.length);
  return matches[0];
};

const documentByRoot = (builder, rootResourceType) => {
  const matches = (builder.workspace?.documents ?? []).filter((document) => document.rootResourceType === rootResourceType);
  if (matches.length !== 1) throw new Error('Expected one ' + rootResourceType + ' table; found ' + matches.length);
  return matches[0];
};

const evaluate = (page, expression) => page.evaluate(expression);
const waitFor = (page, expression, timeout = 5000) => page.waitForFunction(expression, undefined, { timeout });
const locate = async (page, selector, identity = {}) => {
  const indices = await page.locator(selector).evaluateAll((nodes, wanted) => {
    const normalize = (value) => String(value ?? '').replace(/\s+/g, ' ').trim();
    return nodes.flatMap((node, index) => {
      const text = normalize(node.getAttribute('aria-label') || node.innerText || node.textContent);
      const matches = wanted.name !== undefined ? text === wanted.name
        : wanted.includes !== undefined ? text.toLowerCase().includes(wanted.includes.toLowerCase()) : true;
      return matches ? [index] : [];
    });
  }, identity);
  if (indices.length !== 1) throw new Error('Expected one Playwright target for ' + selector + '; found ' + indices.length);
  return page.locator(selector).nth(indices[0]);
};
const inspectAction = async (page, selector, identity = {}) => {
  const locator = await locate(page, selector, identity);
  return {
    found: true,
    visible: await locator.isVisible(),
    disabled: !(await locator.isEnabled()),
    ariaDisabled: await locator.getAttribute('aria-disabled'),
    pointerEvents: await locator.evaluate((element) => getComputedStyle(element).pointerEvents),
    receivesPointer: true,
  };
};
const click = async (page, selector, identity = {}) => {
  const locator = await locate(page, selector, identity);
  await locator.click();
};
const fill = async (page, selector, value) => {
  const locator = await locate(page, selector);
  await locator.fill(value);
};
const setSelectValue = async (page, selector, value) => {
  const state = await inspectAction(page, selector);
  if (!isActionable(state)) throw new Error('Draft Combine select is not actionable: ' + JSON.stringify(state));
  const locator = await locate(page, selector);
  const selected = await locator.selectOption(value);
  if (!selected.includes(value)) throw new Error('Select rejected requested option ' + value + ': got ' + selected);
};
let activeTimedAction;
let activeTimedActionStartedAt;
let lastTimedAction;
const recordBrowserTiming = async (report, page, { name, action, after, timeout = 5000, budget = 5000 }) => {
  const started = Date.now();
  const previousTimedAction = activeTimedAction;
  const previousTimedActionStartedAt = activeTimedActionStartedAt;
  activeTimedAction = name;
  activeTimedActionStartedAt = started;
  let actionDispatched = false;
  try {
    await action();
    actionDispatched = true;
    if (after) await waitFor(page, after, timeout);
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'passed', elapsedMs, renderTimeoutMs: timeout, performanceBudgetMs: budget });
    report.timings[name] = elapsedMs;
    recordCheck(report, 'usability', name + ' completed', true, { elapsedMs });
    if (after) recordCheck(report, 'performance', name + ' action-to-render within budget', elapsedMs <= budget,
      { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
    return elapsedMs;
  } catch (error) {
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'failed', elapsedMs, actionDispatched, error: error instanceof Error ? error.message : String(error) });
    recordCheck(report, 'usability', name + ' completed', false, { elapsedMs, actionDispatched, error: error instanceof Error ? error.message : String(error) });
    if (after && actionDispatched) recordCheck(report, 'performance', name + ' action-to-render within budget', false,
      { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
    else if (after) recordUntested(report, 'performance', name + ' action-to-render', 'No action was dispatched because the target was not actionable.');
    throw error;
  } finally {
    lastTimedAction = { name, startedAtEpochMs: started, finishedAtEpochMs: Date.now() };
    activeTimedAction = previousTimedAction;
    activeTimedActionStartedAt = previousTimedActionStartedAt;
  }
};

const captureOwnedPreviewLifecycle = (page, target, explorer) => {
  const expectedURL = new URL(apiRoot({ target }, explorer) + '/preview', target.apiUrl);
  const ownedOrigins = new Set([target.uiUrl, target.apiUrl].filter(Boolean).map((value) => new URL(value).origin));
  const requests = new Map();
  const events = [];
  const maxEvents = 256;
  const startedAt = performance.now();
  let nextRequest = 0;
  let nextEvent = 0;
  let droppedEvents = 0;
  const append = (event, request, fields = {}) => {
    const record = {
      sequence: ++nextEvent,
      event,
      elapsedMs: Math.round(performance.now() - startedAt),
      observedAtEpochMs: Date.now(),
      actionPhase: activeTimedAction ?? 'outside timed action',
      actionStartedAtEpochMs: activeTimedActionStartedAt ?? null,
      lastTimedAction: lastTimedAction ?? null,
      requestId: request.id,
      outputId: request.outputId,
      receiptId: request.receiptId,
      ...fields,
    };
    if (events.length >= maxEvents) {
      droppedEvents += 1;
      if (event === 'failed') {
        const replaceAt = events.findIndex((existing) => existing.event !== 'failed');
        if (replaceAt >= 0) {
          events.splice(replaceAt, 1);
          events.push(record);
        }
      }
      return;
    }
    events.push(record);
  };
  const requestIdentity = (request) => {
    if (request.method() !== 'POST') return undefined;
    let url;
    try { url = new URL(request.url()); } catch { return undefined; }
    if (!ownedOrigins.has(url.origin) || url.pathname !== expectedURL.pathname) return undefined;
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    return {
      id: 'preview-request-' + (++nextRequest),
      outputId: typeof body?.outputId === 'string' ? body.outputId : null,
      receiptId: typeof body?.receiptId === 'string' ? body.receiptId : null,
    };
  };
  const onRequest = (request) => {
    const identity = requestIdentity(request);
    if (!identity) return;
    requests.set(request, identity);
    append('request', identity);
  };
  const onResponse = (response) => {
    const identity = requests.get(response.request());
    if (identity) append('response', identity, { status: response.status() });
  };
  const onRequestFinished = (request) => {
    const identity = requests.get(request);
    if (!identity) return;
    append('finished', identity);
    requests.delete(request);
  };
  const onRequestFailed = (request) => {
    const identity = requests.get(request);
    if (!identity) return;
    append('failed', identity, { errorText: String(request.failure()?.errorText ?? 'request failed').slice(0, 240) });
    requests.delete(request);
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  page.on('requestfinished', onRequestFinished);
  page.on('requestfailed', onRequestFailed);
  return {
    stop: () => {
      page.off('request', onRequest);
      page.off('response', onResponse);
      page.off('requestfinished', onRequestFinished);
      page.off('requestfailed', onRequestFailed);
      return { path: expectedURL.pathname, maxEvents, droppedEvents, events };
    },
  };
};
const reload = async (page, expression) => {
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitFor(page, expression, 30000);
};

const optionValueByText = async (page, selector, expectedText) => {
  const options = await evaluate(page, `(()=>{const s=document.querySelector(${JSON.stringify(selector)});return s?[...s.options].map(o=>({value:o.value,text:o.textContent.replace(/\\s+/g,' ').trim(),disabled:o.disabled})):[]})()`);
  const normalize = (value) => String(value ?? '').replace(/\s+/g, ' ').trim().toLowerCase();
  const matches = options.filter((option) => {
    if (option.disabled) return false;
    const text = normalize(option.text).replace(/ · current draft$/, '');
    const wanted = normalize(expectedText);
    return text === wanted || text.startsWith(wanted + ' (') || text.startsWith(wanted + ' · ');
  });
  if (matches.length !== 1) throw new Error('Expected one enabled option ' + JSON.stringify(expectedText) + ' in ' + selector + '; options=' + JSON.stringify(options));
  return matches[0].value;
};

const chooseOption = async (page, selector, expectedText) => {
  const value = await optionValueByText(page, selector, expectedText);
  await setSelectValue(page, selector, value);
  return value;
};

const readGrid = async (page, kind = 'saved') => evaluate(page, `(()=>{
  const proposal=${JSON.stringify(kind === 'proposal')};
  const table=proposal?document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"] table'):document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if(!table)return {ready:false,headers:[],rows:[],ariaRowCount:null};
  const tidy=x=>String(x??'').replace(/\\s+/g,' ').trim();
  const headers=proposal?[...table.querySelectorAll('thead th')].map(c=>tidy(c.querySelector('span')?.textContent??c.textContent)):[...table.querySelectorAll('[role="columnheader"]')].map(c=>tidy(c.textContent));
  const rows=proposal?[...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>tidy(c.innerText))):[...table.querySelectorAll('[role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>tidy(c.innerText)));
  return {ready:true,headers,rows,ariaRowCount:table.getAttribute('aria-rowcount')};
})()`);

const assertRows = (report, name, grid, columns, expectedRows) => {
  const canonicalRows = (rows) => [...rows].map((row) => JSON.stringify(row)).sort();
  const ok = grid.ready && JSON.stringify(grid.headers) === JSON.stringify(columns) &&
    JSON.stringify(canonicalRows(grid.rows)) === JSON.stringify(canonicalRows(expectedRows));
  check(report, 'correctness', name, ok, { headers: grid.headers, expectedHeaders: columns, rows: grid.rows, expectedRows, ariaRowCount: grid.ariaRowCount });
};

const waitSaved = (page, rows) => waitFor(page, savedPreview(rows), 30000);
const reloadAndSelectSavedTable = async (report, page, outputId, rows, name) => {
  await recordBrowserTiming(report, page, {
    name,
    action: async () => {
      await reload(page, workspaceReady);
      await selectTable(page, outputId);
    },
    after: rows === undefined ? selectedOutputReady(outputId) : savedPreviewOutput(rows, outputId),
    timeout: 5000,
    budget: 5000,
  });
};
const proposalRowsReady = (rows, outputId) => proposalPreviewReadinessExpression(outputId, rows);
const proposalReady = (outputId) => proposalPreviewReadinessExpression(outputId);
const waitProposal = (page, rows, outputId) => waitFor(page, proposalRowsReady(rows, outputId), 30000);

const readScope = (context, explorer, builder, expectedScope, expectedDraftState = 'draft') => {
  const route = new URL('/api/v1/projects/' + encodeURIComponent(context.target.fixtureProject) + '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2/builder', context.target.apiUrl);
  const expectedPath = '/api/v1/projects/' + encodeURIComponent(context.target.fixtureProject) + '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2/builder';
  const draftState = builderDraftStateEvidence(builder, expectedDraftState);
  const catalogBound = builder.catalog?.generation === (expectedScope?.generation ?? context.target.fixtureGeneration) &&
    Boolean(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest) &&
    (!expectedScope || (builder.catalog.snapshotToken === expectedScope.snapshotToken &&
      builder.catalog.authorizationScopeDigest === expectedScope.authorizationScopeDigest));
  const bound = route.pathname === expectedPath && catalogBound && draftState.ok;
  return {
    ok: bound,
    catalogBound,
    draftState,
    requestURL: route.toString(),
    project: context.target.fixtureProject,
    explorer,
    generation: builder.catalog?.generation ?? null,
    expectedGeneration: context.target.fixtureGeneration,
    snapshotToken: builder.catalog?.snapshotToken ?? null,
    draftVersion: builder.draftVersion ?? null,
    draftDigest: builder.draftDigest ?? null,
    authorizationScopeDigest: builder.catalog?.authorizationScopeDigest ?? null,
  };
};

const sourceColumnLabels = async (page, resourceType, needsKey = true) => {
  const candidates = await evaluate(page, `(()=>[...document.querySelectorAll('input[type="checkbox"][aria-label^="Group by "]')].map(i=>i.getAttribute('aria-label').slice('Group by '.length)))()`);
  const status = candidates.filter((label) => /status/i.test(label));
  const gender = candidates.filter((label) => /gender/i.test(label));
  const ids = candidates.filter((label) => /\bid\b/i.test(label));
  const key = resourceType === 'Patient' ? gender : status;
  if ((needsKey && key.length !== 1) || (resourceType !== 'Patient' && ids.length !== 1)) {
    throw new Error('Group field identity is ambiguous for ' + resourceType + ': ' + JSON.stringify(candidates));
  }
  return { key: key[0], id: ids[0] };
};

const addRoot = async (context, page, report, explorer, resourceType, title, expectedIDs, expectedDocumentCount, expectedScope) => {
  await fill(page, '#first-table-name', title);
  await recordBrowserTiming(report, page, {
    name: 'create current-draft ' + resourceType + ' source table',
    action: () => click(page, 'button', { name: 'Choose ' + resourceType + ' rows' }),
    after: workspaceReady + '&&document.body.innerText.includes(' + JSON.stringify(title) + ')&&' + rootRowsPreview(expectedIDs),
    timeout: 5000,
    budget: 5000,
  });
  const grid = await readGrid(page);
  const idIndex = grid.headers.findIndex((header) => /(^id$|\bid\b)/i.test(header));
  const ids = idIndex < 0 ? [] : grid.rows.map((row) => row[idIndex]).sort();
  check(report, 'correctness', resourceType + ' root Preview contains exact independent fixture IDs', JSON.stringify(ids) === JSON.stringify([...expectedIDs].sort()), { headers: grid.headers, ids, expectedIDs });
  const builder = await readBuilder(context, explorer);
  const scope = readScope(context, explorer, builder, expectedScope, 'draft');
  const actualDocumentCount = builder.workspace?.documents?.length ?? 0;
  const currentDocument = (builder.workspace?.documents ?? []).find((document) => document.rootResourceType === resourceType && document.output?.title === title);
  const createdCurrentDraft = scope.ok && actualDocumentCount === expectedDocumentCount && Boolean(currentDocument);
  check(report, 'correctness', (expectedDocumentCount === 1 ? 'first native source table' : resourceType + ' native source table') + ' establishes a versioned current draft under the exact scope', createdCurrentDraft, {
    scope,
    expectedDocumentCount,
    actualDocumentCount,
    output: currentDocument?.output ?? null,
    draftVersion: builder.draftVersion ?? null,
    draftDigest: builder.draftDigest ?? null,
  });
  if (!createdCurrentDraft) throw new Error('Native source table did not establish the expected versioned draft under the unchanged authorization scope.');
};

const addRawField = async (page, report, resourceType, path, expectedRows) => {
  await click(page, 'button[data-testid="construction-action-add-columns"]');
  await waitFor(page, "Boolean(document.querySelector('[aria-label=\"Add columns editor\"]'))", 10000);
  await click(page, 'button', { name: 'Fields and related data' });
  await click(page, 'summary', { name: 'Raw FHIR fields (advanced)' });
  const selector = 'input[type="checkbox"][aria-label=' + JSON.stringify('Select ' + resourceType + '.' + path) + ']';
  await waitFor(page, 'Boolean(document.querySelector(' + JSON.stringify(selector) + '))', 10000);
  const outputId = await evaluate(page, "document.querySelector('[data-testid=construction-preview]')?.dataset.previewOutputId??null");
  if (!outputId) throw new Error('Raw field selection has no current root output identity.');
  await recordBrowserTiming(report, page, {
    name: 'select raw ' + resourceType + '.' + path + ' and make Apply columns ready',
    action: async () => {
      await page.locator(selector).check({ timeout: 5000 });
      await click(page, 'button', { name: 'Add 1 selected feature' });
    },
    after: "[...document.querySelectorAll('button')].some(b=>b.innerText.trim()==='Apply columns'&&!b.disabled)&&" +
      "document.querySelector('[data-testid=construction-preview]')?.dataset.previewOutputId===" + JSON.stringify(outputId),
    timeout: 5000,
    budget: 5000,
  });
  await recordBrowserTiming(report, page, {
    name: 'apply native raw ' + resourceType + '.' + path + ' field to a current draft',
    action: () => click(page, 'button', { name: 'Apply columns' }),
    after: savedPreviewOutput(expectedRows, outputId),
    timeout: 5000,
    budget: 5000,
  });
  await click(page, 'button', { name: 'Close operation editor' });
};

const chooseGroupKey = async (page, ariaLabel) => {
  const selector = 'input[type="checkbox"][aria-label=' + JSON.stringify('Group by ' + ariaLabel) + ']';
  const action = await inspectAction(page, selector);
  if (!isActionable(action)) throw new Error('Native Group key control is unavailable: ' + JSON.stringify(action));
  await click(page, selector);
};

const beginGroup = async (page, resourceType, keys) => {
  await click(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitFor(page, "document.querySelector('[data-testid=construction-action-group-rows]')?.disabled===false", 10000);
  await click(page, '[data-testid="construction-action-group-rows"]');
  await waitFor(page, "Boolean(document.querySelector('[data-testid=construction-reshape-editor]'))&&Boolean(document.querySelector('select[aria-label=\"Summary 1\"]'))", 10000);
  const fields = await sourceColumnLabels(page, resourceType, keys !== 'id-only');
  const selectedKeys = keys === 'pivot-source' ? [fields.id, fields.key] : keys === 'id-only' ? [fields.id] : [fields.key];
  for (const key of selectedKeys) await chooseGroupKey(page, key);
  return { fields, selectedKeys };
};

const groupRows = async (page, report, resourceType, composed, rawRows, expectedCount, labelPrefix = '', groupByID = false) => {
  const started = Date.now();
  const groupMode = composed ? 'pivot-source' : groupByID ? 'id-only' : 'ordinary';
  const { fields, selectedKeys } = await beginGroup(page, resourceType, groupMode);
  const outputId = await evaluate(page, "document.querySelector('[data-testid=construction-operation-editor]')?.getAttribute('data-output-id')??null");
  if (!outputId) throw new Error('Native GROUP editor is missing its current draft output identity.');
  await waitProposal(page, expectedCount, outputId);
  const grid = await readGrid(page, 'proposal');
  const countHeaderIndex = grid.headers.findIndex((header) => /row count/i.test(header));
  const keyHeaderIndexes = selectedKeys.map((label) => grid.headers.findIndex((header) => header === label));
  const sorted = grid.rows.map((row) => [...keyHeaderIndexes.map((index) => row[index]), Number(row[countHeaderIndex])])
    .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  const expected = (composed
    ? rawRows.map((row) => [row.id, String(row.status), 1])
    : groupMode === 'id-only' ? groupCounts(rawRows, 'id')
      : groupCounts(rawRows, resourceType === 'Patient' ? 'gender' : 'status'))
    .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  const groupCheckName = (labelPrefix || resourceType).trim() + ' native GROUP count rows match the direct fixture oracle';
  check(report, 'correctness', groupCheckName,
    keyHeaderIndexes.every((index) => index >= 0) && countHeaderIndex >= 0 && JSON.stringify(sorted) === JSON.stringify(expected),
    { fields, selectedKeys, headers: grid.headers, actual: sorted, expected });
  check(report, 'performance', (labelPrefix || resourceType).trim() + ' GROUP automatic preview is within five seconds', Date.now() - started <= 5000,
    { elapsedMs: Date.now() - started, selectedKeys });
  return { fields, selectedKeys, grid, outputId };
};

const applyCurrentProposal = async (page, report, name, rows, outputId) => {
  await recordBrowserTiming(report, page, {
    name,
    action: () => click(page, '[data-testid="construction-apply-proposal"]'),
    after: '!document.querySelector(\'[data-testid="construction-proposal-panel"]\')&&' + savedPreviewOutput(rows, outputId),
    timeout: 5000,
    budget: 5000,
  });
};

const savedGroupStep = (document) => document.construction?.steps?.find((step) => step.operation?.kind === 'GROUP');

const createGroupSource = async (context, page, report, explorer, {
  resourceType, title, fieldPath, rawRows, keyName, idName, outputRows, composed = false, groupByID = false, deriveCountPlusOne = false,
}) => {
  const expectedIDs = rawRows.map((row) => row.id);
  const initial = await readBuilder(context, explorer);
  const alreadyHasDoc = (initial.workspace?.documents ?? []).length > 0;
  if (alreadyHasDoc) {
    await click(page, 'button[data-testid="construction-new-table"]');
    await waitFor(page, "Boolean(document.querySelector('#first-table-name'))&&document.body.innerText.includes('Build another table')", 10000);
  }
  await addRoot(context, page, report, explorer, resourceType, title, expectedIDs,
    (initial.workspace?.documents ?? []).length + 1, report.target.scope);
  await addRawField(page, report, resourceType, fieldPath, rawRows.length);
  const groupKeyCount = composed || groupByID ? rawRows.length : groupCounts(rawRows, resourceType === 'Patient' ? 'gender' : 'status').length;
  const grouped = await groupRows(page, report, resourceType, composed, rawRows, groupKeyCount, composed || groupByID ? title + ' ' : '', groupByID);
  await applyCurrentProposal(page, report, 'Apply ' + title + ' current-draft GROUP', groupKeyCount, grouped.outputId);
  let builder = await readBuilder(context, explorer);
  let document = documentByRoot(builder, resourceType);
  let group = savedGroupStep(document);
  assert(group && group.operation.group.aggregates.some((aggregate) => aggregate.operation === 'COUNT_ROWS'), 'Native GROUP must persist COUNT_ROWS.');
  if (composed) {
    const keyOutputs = group.operation.group.keys.map((key) => group.outputs.find((output) => output.id === key.outputColumnId));
    const idOutput = keyOutputs.find((output) => /id/i.test(output?.label ?? output?.name ?? ''));
    const keyOutput = keyOutputs.find((output) => output?.id !== idOutput?.id);
    const countOutput = group.outputs.find((output) => group.operation.group.aggregates.some((aggregate) => aggregate.outputColumnId === output.id));
    if (!idOutput || !keyOutput || !countOutput) throw new Error('Cannot identify native GROUP outputs for Group→Pivot: ' + JSON.stringify(group));
    const beforePivot = await readGrid(page);
    await click(page, '[data-testid="construction-rows-settings-trigger"]');
    await waitFor(page, "document.querySelector('[data-testid=construction-action-pivot-rows]')?.disabled===false||document.querySelector('[data-testid=construction-action-pivot]')?.disabled===false", 10000);
    const pivotAction = await evaluate(page, "document.querySelector('[data-testid=construction-action-pivot-rows]')?'[data-testid=construction-action-pivot-rows]':'[data-testid=construction-action-pivot]'");
    await click(page, pivotAction);
    await waitFor(page, "Boolean(document.querySelector('[data-testid=construction-reshape-editor]'))&&Boolean(document.querySelector('select[aria-label=\"Pivot category field\"]'))", 10000);
    await waitFor(page, "Boolean(document.querySelector('[data-testid=construction-reshape-pivot]'))", 10000);
    const pivotGroupLabel = await evaluate(page, `(()=>[...document.querySelectorAll('input[type="checkbox"][aria-label^="Pivot group "]')].map(i=>i.getAttribute('aria-label').slice('Pivot group '.length)))()`);
    const idLabel = pivotGroupLabel.find((label) => /id/i.test(label));
    if (!idLabel) throw new Error('Pivot did not expose the grouped source ID as a row key: ' + JSON.stringify(pivotGroupLabel));
    await click(page, 'input[type="checkbox"][aria-label=' + JSON.stringify('Pivot group ' + idLabel) + ']');
    const pivotStarted = Date.now();
    await chooseOption(page, 'select[aria-label="Pivot category field"]', keyOutput.label);
    await chooseOption(page, 'select[aria-label="Pivot values field"]', countOutput.label);
    await waitFor(page, "Boolean(document.querySelector('[data-testid=construction-reshape-pivot-categories] input[aria-label^=\"Include category \"]'))", 30000);
    await click(page, 'summary', { name: 'Change selected categories' });
    const categoryControls = await evaluate(page, "[...document.querySelectorAll('[data-testid=construction-reshape-pivot-categories] input[aria-label^=\"Include category \"]')].map(i=>({label:i.getAttribute('aria-label'),checked:i.checked}))");
    const requiredCategories = [...new Set(rawRows.map((row) => String(row.status)))].sort();
    for (const category of requiredCategories) {
      const control = categoryControls.find((entry) => entry.label === 'Include category ' + category);
      if (!control) throw new Error('Pivot category discovery omitted fixture category ' + category + ': ' + JSON.stringify(categoryControls));
      if (!control.checked) await click(page, 'input[type="checkbox"][aria-label=' + JSON.stringify(control.label) + ']');
    }
    await waitProposal(page, expectedIDs.length, document.output.id);
    check(report, 'performance', title + ' native Pivot category selection, discovery, and exact preview complete within five seconds', Date.now() - pivotStarted <= 5000,
      { elapsedMs: Date.now() - pivotStarted, outputId: document.output.id, requiredCategories });
    const pivotGrid = await readGrid(page, 'proposal');
    const idIndex = pivotGrid.headers.findIndex((header) => /id/i.test(header));
    const categoryIndexes = requiredCategories.map((category) => pivotGrid.headers.findIndex((header) => header.toLowerCase() === category.toLowerCase()));
    const actualByID = pivotGrid.rows.map((row) => [row[idIndex], ...categoryIndexes.map((index) => row[index] === '—' ? null : Number(row[index]))]).sort((a, b) => String(a[0]).localeCompare(String(b[0])));
    const expectedByID = groupedPivotRows(rawRows, 'id', 'status', requiredCategories);
    check(report, 'correctness', title + ' native Pivot preview matches raw Group→Pivot category counts',
      idIndex >= 0 && categoryIndexes.every((index) => index >= 0) && JSON.stringify(actualByID) === JSON.stringify(expectedByID),
      { beforePivot, idLabel, keyOutput, countOutput, requiredCategories, headers: pivotGrid.headers, actualByID, expectedByID });
    await applyCurrentProposal(page, report, 'Apply ' + title + ' current-draft PIVOT', expectedIDs.length, document.output.id);
    await reloadAndSelectSavedTable(report, page, document.output.id, expectedIDs.length, 'reload applied ' + title + ' Group→Pivot and render its exact table');
    assertRows(report, title + ' Group→Pivot values survive reload', await readGrid(page),
      [idLabel, ...requiredCategories], expectedByID.map((row) => row.map((value) => value === null ? '—' : String(value))));
    builder = await readBuilder(context, explorer);
    document = documentByRoot(builder, resourceType);
    const pivot = document.construction?.steps?.find((step) => step.operation?.kind === 'PIVOT');
    if (!pivot || !document.construction?.steps?.some((step) => step.operation?.kind === 'GROUP')) throw new Error('Saved source is not an ordered Group→Pivot construction.');
    outputRows = undefined;
  }
  if (deriveCountPlusOne) {
    const groupAggregate = group?.outputs?.find((output) => group.operation.group.aggregates.some((aggregate) => aggregate.outputColumnId === output.id));
    if (!groupAggregate || groupAggregate.name !== 'row_count') throw new Error('Patient Group did not preserve the expected row_count source for the native derived field.');
    const keyOutput = group.operation.group.keys.map((key) => group.outputs.find((output) => output.id === key.outputColumnId)).find(Boolean);
    if (!keyOutput) throw new Error('Patient Group did not preserve its category key output.');
    const expectedDerived = groupCounts(rawRows, 'gender').map(([gender, count]) => [String(gender), String(count), String(count + 1)]);
    const started = Date.now();
    await click(page, 'button[data-testid="construction-action-calculate"]');
    await waitFor(page, "Boolean(document.querySelector('[data-testid=construction-calculate-editor]'))", 10000);
    await click(page, 'button', { name: 'Formula editor' });
    await fill(page, 'textarea[aria-label="Formula"]', groupAggregate.name + ' + 1');
    await fill(page, 'input[aria-label="Output column name"]', 'row_count_plus_one');
    await recordBrowserTiming(report, page, {
      name: title + ' native Group→DERIVE auto-preview with exact count-plus-one rows',
      action: () => fill(page, 'input[aria-label="Output column label"]', 'Count plus one'),
      after: proposalRowsReady(expectedDerived.length, document.output.id),
      timeout: 5000,
      budget: 5000,
    });
    const deriveGrid = await readGrid(page, 'proposal');
    assertRows(report, title + ' Group→DERIVE preview matches independent category/count arithmetic', deriveGrid,
      [keyOutput.label, groupAggregate.label, 'Count plus one'], expectedDerived);
    check(report, 'performance', title + ' Group→DERIVE configuration and preview complete within five seconds', Date.now() - started <= 5000,
      { elapsedMs: Date.now() - started, outputId: document.output.id });
    await applyCurrentProposal(page, report, 'Apply ' + title + ' current-draft DERIVE', expectedDerived.length, document.output.id);
    builder = await readBuilder(context, explorer);
    document = documentByRoot(builder, resourceType);
    const deriveStep = document.construction?.steps?.find((step) => step.operation?.kind === 'DERIVE');
    const derivedOutput = deriveStep?.outputs?.find((output) => output.name === 'row_count_plus_one');
    const derived = deriveStep?.operation?.kind === 'DERIVE' ? deriveStep.operation.derive : undefined;
    const exactExpression = derived?.operation === 'ADD' &&
      derived.left?.kind === 'COLUMN' && derived.left.columnId === groupAggregate.id &&
      derived.right?.kind === 'LITERAL' && derived.right.literal?.kind === 'INTEGER' && derived.right.literal.integer === 1 &&
      derived.outputColumnId === derivedOutput?.id;
    check(report, 'persistence', title + ' persists its native Group→DERIVE output without pinning a revision',
      Boolean(deriveStep && derivedOutput && exactExpression &&
        deriveStep.inputs?.some((input) => input.kind === 'STEP_OUTPUT') &&
        document.construction.steps.map((step) => step.operation.kind).join(',') === 'GROUP,DERIVE'),
      { outputId: document.output.id, steps: document.construction.steps.map((step) => step.operation.kind), deriveStep, derivedOutput, exactExpression });
    await reloadAndSelectSavedTable(report, page, document.output.id, expectedDerived.length, 'reload applied ' + title + ' Group→DERIVE and render its exact table');
    assertRows(report, title + ' Group→DERIVE values survive reload', await readGrid(page),
      [keyOutput.label, groupAggregate.label, 'Count plus one'], expectedDerived);
    outputRows = expectedDerived.length;
  }
  builder = await readBuilder(context, explorer);
  document = documentByRoot(builder, resourceType);
  const steps = document.construction?.steps ?? [];
  check(report, 'persistence', title + ' remains an unpublished current-draft grouped output',
    steps.some((step) => step.operation?.kind === 'GROUP') && !steps.some((step) => step.operation?.kind === 'COMBINE'),
    { outputId: document.output.id, rootResourceType: document.rootResourceType, steps: steps.map((step) => step.operation.kind) });
  const groupKeyOutputs = (group?.operation.group.keys ?? [])
    .map((key) => group.outputs.find((output) => output.id === key.outputColumnId))
    .filter(Boolean);
  const identityGroupOutput = groupKeyOutputs.find((output) => /\bid\b/i.test(output.name + ' ' + output.label)) ?? groupKeyOutputs[0];
  const groupCountOutput = group?.outputs.find((output) => group.operation.group.aggregates.some((aggregate) => aggregate.outputColumnId === output.id));
  const terminalStep = steps.at(-1);
  const pivotCategoryLabels = terminalStep?.operation.kind === 'PIVOT'
    ? terminalStep.operation.pivot.categories.map((category) => ({
        value: category.key.kind === 'STRING' ? category.key.string : null,
        label: terminalStep.outputs.find((output) => output.id === category.outputColumnId)?.label ?? null,
      }))
    : [];
  const pivotIdentityLabel = terminalStep?.operation.kind === 'PIVOT'
    ? terminalStep.outputs.find((output) => output.id === identityGroupOutput?.id)?.label ?? identityGroupOutput?.label
    : undefined;
  return {
    outputId: document.output.id,
    rootResourceType: resourceType,
    title,
    document,
    group,
    outputRows,
    composed,
    groupKeyLabel: groupKeyOutputs[0]?.label ?? null,
    identityLabel: identityGroupOutput?.label ?? null,
    pivotIdentityLabel: pivotIdentityLabel ?? identityGroupOutput?.label ?? null,
    countOutputLabel: groupCountOutput?.label ?? null,
    pivotCategoryLabels,
    appendCountLabel: deriveCountPlusOne ? 'Count plus one' : groupCountOutput?.label ?? null,
  };
};

const capturePost = (page, pathSuffix) => {
  const entries = [];
  const byRequest = new Map();
  const pendingReads = [];
  const onRequest = (request) => {
    if (request.method() !== 'POST') return;
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (!url.pathname.endsWith(pathSuffix)) return;
    let body;
    try { body = request.postDataJSON(); } catch { return; }
    const entry = { request, url: url.href, path: url.pathname, body, status: null, response: null };
    entries.push(entry);
    byRequest.set(request, entry);
  };
  const onResponse = (response) => {
    const entry = byRequest.get(response.request());
    if (!entry) return;
    entry.status = response.status();
    const read = response.json().then(value => { entry.response = value; }, error => { entry.readError = String(error); });
    pendingReads.push(read);
  };
  const onRequestFailed = (request) => {
    const entry = byRequest.get(request);
    if (entry) entry.readError = request.failure()?.errorText ?? 'request failed';
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  page.on('requestfailed', onRequestFailed);
  return {
    entries,
    stop: async () => {
      page.off('request', onRequest);
      page.off('response', onResponse);
      page.off('requestfailed', onRequestFailed);
      await Promise.all(pendingReads);
    },
    waitFor: async (predicate, timeoutMs = 10000) => {
      const deadline = Date.now() + timeoutMs;
      while (Date.now() < deadline) {
        const found = entries.findLast((entry) => predicate(entry) && entry.response !== null);
        if (found) return found;
        const failed = entries.find((entry) => predicate(entry) && entry.readError);
        if (failed) throw new Error('Could not capture scoped native response: ' + failed.readError);
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error('Timed out capturing POST ' + pathSuffix + '; captured=' + JSON.stringify(entries.map((entry) => ({ path: entry.path, status: entry.status, outputId: entry.body?.outputId }))));
    },
  };
};

const selectTable = async (page, outputId) => {
  const selector = '[data-testid="construction-table-' + outputId + '"]';
  if (!await evaluate(page, 'document.querySelector(' + JSON.stringify(selector) + ')?.getAttribute("aria-current")==="page"')) await click(page, selector);
  await waitFor(page, 'document.querySelector(' + JSON.stringify(selector) + ')?.getAttribute("aria-current")==="page"', 10000);
};

const startCombineTarget = async (context, page, report, explorer, sourceOutputId, sourceBuilder) => {
  await selectTable(page, sourceOutputId);
  const commands = capturePost(page, '/authoring/v2/commands');
  try {
    await recordBrowserTiming(report, page, {
      name: 'open native Combine and create a separate empty target',
      action: () => click(page, 'button[data-testid="construction-action-combine"]'),
      after: "Boolean(document.querySelector('[data-testid=\"construction-operation-editor\"][data-operation-family=\"COMBINE\"][data-output-id]'))&&Boolean(document.querySelector('[data-testid=\"construction-combine-editor\"]'))",
      timeout: 5000,
      budget: 5000,
    });
    const command = await commands.waitFor((entry) => entry.body?.commands?.some((item) => item.type === 'CREATE_TABLE'));
    const mountedOutputId = await evaluate(page, 'document.querySelector(\'[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]\')?.getAttribute("data-output-id")??null');
    const rootNodeIds = (sourceBuilder.catalog?.nodes ?? []).filter((node) => node.resourceType === 'Observation' && node.rowRootEligible).map((node) => node.nodeId);
    const previousOutputIds = (sourceBuilder.workspace?.documents ?? []).map((document) => document.output?.id).filter(Boolean);
    const evidence = nativeCombineTargetBindingEvidence({
      requestBody: command.body,
      responseStatus: command.status,
      response: command.response,
      expectedRootNodeIds: rootNodeIds,
      expectedRootResourceType: 'Observation',
      previousOutputIds,
      mountedOutputId,
    });
    check(report, 'correctness', 'native Combine uses CREATE_TABLE to make an empty rooted target without authoring a source', evidence.ok, evidence);
    if (!evidence.ok) throw new Error('Native Combine target creation did not bind its exact command, response, and editor.');
    return { outputId: evidence.outputId, rootResourceType: 'Observation' };
  } finally {
    await commands.stop();
  }
};

const chooseWorkspaceInputs = async (page, kind, sources) => {
  const choice = kind === 'KEY_JOIN' ? 'key_join' : 'append';
  await click(page, 'button[data-testid="construction-combine-choice-' + choice + '"]');
  await waitFor(page, "Boolean(document.querySelector('select[aria-label=\"Input table 1\"]'))&&Boolean(document.querySelector('select[aria-label=\"Input table 2\"]'))", 10000);
  for (let index = 0; index < sources.length; index += 1) {
    if (index >= 2) {
      await click(page, 'button', { name: 'Add another table' });
      await waitFor(page, 'Boolean(document.querySelector(' + JSON.stringify('select[aria-label="Input table ' + (index + 1) + '"]') + '))', 10000);
    }
    const selector = 'select[aria-label="Input table ' + (index + 1) + '"]';
    const value = workspaceOutputOption(sources[index].outputId);
    const options = await evaluate(page, `(()=>{const s=document.querySelector(${JSON.stringify(selector)});return s?[...s.options].map(o=>({value:o.value,text:o.textContent.trim(),group:o.parentElement?.label,disabled:o.disabled})):[]})()`);
    const option = options.find((item) => item.value === value && item.group === 'Current draft tables' && !item.disabled);
    if (!option) throw new Error('Expected current-draft output in native Combine selector: ' + JSON.stringify({ source: sources[index], options }));
    await setSelectValue(page, selector, value);
  }
};

const addCombineOutput = async (page, kind, index, name, label, sourceLabels, leaveFinalMapping = false) => {
  await click(page, 'button', { name: 'Add output field' });
  await waitFor(page, 'Boolean(document.querySelector(' + JSON.stringify('input[aria-label="Output field ' + index + ' name"]') + '))', 10000);
  await fill(page, 'input[aria-label="Output field ' + index + ' name"]', name);
  await fill(page, 'input[aria-label="Output field ' + index + ' label"]', label);
  let finalMapping;
  for (let inputIndex = 0; inputIndex < sourceLabels.length; inputIndex += 1) {
    if (!sourceLabels[inputIndex]) continue;
    const selector = kind === 'KEY_JOIN'
      ? 'select[aria-label="Output field ' + index + ' source field in input ' + (inputIndex + 1) + '"]'
      : 'select[aria-label="Output field ' + index + ' matching field in input ' + (inputIndex + 1) + '"]';
    if (leaveFinalMapping && inputIndex === sourceLabels.length - 1) {
      finalMapping = { selector, label: sourceLabels[inputIndex] };
    } else {
      await chooseOption(page, selector, sourceLabels[inputIndex]);
    }
  }
  return finalMapping;
};

const configureJoin = async (page, sources, joinType, shape = 'status-counts') => {
  if (sources.length !== 2 || !sources[0].groupKeyLabel) throw new Error('Native Join needs exactly two current-draft sources with a stable first-input key.');
  if (shape === 'status-counts' && (!sources[1].groupKeyLabel || !sources[0].countOutputLabel || !sources[1].countOutputLabel)) {
    throw new Error('Native status Join is missing a persisted source key or count output label.');
  }
  if (shape === 'group-pivot' && (!sources[0].countOutputLabel || !sources[1].pivotIdentityLabel ||
    !['final', 'preliminary'].every((value) => sources[1].pivotCategoryLabels.some((item) => item.value === value && item.label)))) {
    throw new Error('Mixed Group/Pivot Join is missing an exact ID, count, or discovered final/preliminary output label.');
  }
  await chooseWorkspaceInputs(page, 'KEY_JOIN', sources);
  await chooseOption(page, 'select[aria-label="Matching pair 1 first field"]', sources[0].groupKeyLabel);
  await chooseOption(page, 'select[aria-label="Matching pair 1 second field"]', shape === 'status-counts' ? sources[1].groupKeyLabel : sources[1].pivotIdentityLabel);
  await setSelectValue(page, 'select[aria-label="If a row in the first table has no match"]', joinType);
  if (shape === 'group-pivot') {
    const mappings = [
      ['observation_id', 'Observation ID', sources[0].groupKeyLabel, ''],
      ['observation_rows', 'Observation rows', sources[0].countOutputLabel, ''],
      ['report_id', 'DiagnosticReport ID', '', sources[1].pivotIdentityLabel],
      ['report_final', 'Report final', '', sources[1].pivotCategoryLabels.find((item) => item.value === 'final')?.label],
      ['report_preliminary', 'Report preliminary', '', sources[1].pivotCategoryLabels.find((item) => item.value === 'preliminary')?.label],
    ];
    for (let index = 0; index < mappings.length; index += 1) {
      const [name, label, left, right] = mappings[index];
      const finalMapping = await addCombineOutput(page, 'KEY_JOIN', index + 1, name, label, [left, right], index === mappings.length - 1);
      if (finalMapping) return finalMapping;
    }
  } else {
    await addCombineOutput(page, 'KEY_JOIN', 1, 'observation_status', 'Observation status', [sources[0].groupKeyLabel, '']);
    await addCombineOutput(page, 'KEY_JOIN', 2, 'observation_count', 'Observation rows', [sources[0].countOutputLabel, '']);
    await addCombineOutput(page, 'KEY_JOIN', 3, 'report_status', 'Report status', ['', sources[1].groupKeyLabel]);
    return addCombineOutput(page, 'KEY_JOIN', 4, 'report_count', 'Report rows', ['', sources[1].countOutputLabel], true);
  }
};

const configureAppend = async (page, sources) => {
  if (sources.length !== 3 || sources.some((source) => !source.groupKeyLabel || !source.appendCountLabel)) {
    throw new Error('Native APPEND requires three grouped sources with stable category and count output labels.');
  }
  await chooseWorkspaceInputs(page, 'APPEND', sources);
  await addCombineOutput(page, 'APPEND', 1, 'category', 'Category', sources.map((source) => source.groupKeyLabel));
  const countLabels = sources.map((source) => source.appendCountLabel ?? 'Row count');
  return addCombineOutput(page, 'APPEND', 2, 'row_count', 'Row count', countLabels, true);
};

const renderFinalMapping = async (page, report, name, mapping, rows, outputId) => {
  if (!mapping) throw new Error('Native draft Combine setup did not leave a final source mapping for timed Preview.');
  const value = await optionValueByText(page, mapping.selector, mapping.label);
  await recordBrowserTiming(report, page, {
    name,
    action: () => setSelectValue(page, mapping.selector, value),
    after: proposalRowsReady(rows, outputId),
    timeout: 5000,
    budget: 5000,
  });
};

const boundedJSONEvidence = (value, limit = 16000) => {
  const serialized = JSON.stringify(value);
  if (serialized === undefined) return null;
  return serialized.length <= limit
    ? value
    : { truncated: true, serializedLength: serialized.length, jsonPrefix: serialized.slice(0, limit) };
};
const assertDraftCandidate = async (report, page, capture, base, target, sources, expectedKind) => {
  const expectedOutputIDs = sources.map((source) => source.outputId);
  const expectedBuilderURL = new URL(report.target.scope.requestURL);
  const expectedProposalPath = expectedBuilderURL.pathname.replace(/\/builder$/, '/construction-proposals');
  const expectedProposalOrigin = new URL(report.target.uiUrl).origin;
  const event = await capture.waitFor((entry) => {
    if (entry.path !== expectedProposalPath || entry.status !== 200 || entry.response?.previewStatus !== 'READY' || entry.body?.outputId !== target.outputId || !entry.body?.candidateConstruction) return false;
    const step = entry.body.candidateConstruction.steps?.at(-1);
    if (step?.operation?.kind !== 'COMBINE' || step.operation.combine?.kind !== expectedKind) return false;
    const ids = (step.inputs ?? []).map((input) => input?.outputId);
    return ids.length === expectedOutputIDs.length && ids.every((id, index) => id === expectedOutputIDs[index]);
  });
  const steps = event.body.candidateConstruction.steps ?? [];
  const step = steps.at(-1);
  const evidence = currentDraftSourceEvidence({
    inputs: step?.inputs,
    expectedOutputIDs: sources.map((source) => source.outputId),
    sourceDocuments: base.workspace?.documents,
    publishedOutputIDs: [],
  });
  const requestOrigin = new URL(event.url).origin;
  const scopeChecks = {
    routePathMatches: event.path === expectedProposalPath,
    validatedUIProxyOriginMatches: requestOrigin === expectedProposalOrigin,
    generationMatches: base.catalog?.generation === report.target.scope.generation,
    authorizationScopeMatches: base.catalog?.authorizationScopeDigest === report.target.scope.authorizationScopeDigest,
    snapshotTokenMatches: event.body.snapshotToken === base.catalog?.snapshotToken,
    expectedDraftVersionMatches: event.body.expectedDraftVersion === base.draftVersion,
    expectedDraftDigestMatches: event.body.expectedDraftDigest === base.draftDigest,
  };
  const proposalOriginMatched = scopeChecks.validatedUIProxyOriginMatches;
  const scope = Object.values(scopeChecks).every(Boolean);
  const domProposal = await evaluate(page, `(()=>({
    proposalId:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id')??null,
    receiptId:document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id')??null,
    outputId:document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-output-id')??null
  }))()`);
  const responsePreview = event.response?.preview;
  const requestConstruction = event.body.candidateConstruction;
  const responseConstruction = event.response?.candidateConstruction;
  const responseChecks = {
    proposalIdMatchesDOM: event.response?.proposalId === domProposal.proposalId,
    previewReceiptMatchesDOM: responsePreview?.receiptId === domProposal.receiptId,
    previewOutputMatchesTarget: responsePreview?.outputId === target.outputId,
    domOutputMatchesTarget: domProposal.outputId === target.outputId,
    responseOutputMatchesTarget: event.response?.outputId === target.outputId,
    snapshotTokenMatchesRequest: event.response?.snapshotToken === event.body.snapshotToken,
    draftVersionMatchesRequest: event.response?.draftVersion === event.body.expectedDraftVersion,
    draftDigestMatchesRequest: event.response?.draftDigest === event.body.expectedDraftDigest,
    previewStatusReady: event.response?.previewStatus === 'READY',
    candidateConstructionMatchesRequest: constructionCandidateWireEquivalent(requestConstruction, responseConstruction),
  };
  const responseBound = Object.values(responseChecks).every(Boolean);
  check(report, 'correctness', 'automatic Combine proposal is bound to this exact draft CAS and UI proxy scope',
    evidence.ok && scope && responseBound && event.status === 200, {
      path: event.path,
      url: event.url,
      status: event.status,
      requestId: event.requestId,
      project: report.target.scope.project,
      explorer: report.target.scope.explorer,
      generation: report.target.scope.generation,
      authorizationScopeDigest: report.target.scope.authorizationScopeDigest,
      proposalOriginMatched,
      requestOrigin,
      expectedProposalOrigin,
      expectedDirectApiOrigin: expectedBuilderURL.origin,
      scopeChecks,
      responseBound,
      responseChecks,
      domProposal,
      responseProposalId: event.response?.proposalId ?? null,
      responseOutputId: event.response?.outputId ?? null,
      responsePreview: responsePreview ? { receiptId: responsePreview.receiptId, outputId: responsePreview.outputId } : null,
      candidateStepKind: step?.operation?.kind ?? null,
      candidateCombineKind: step?.operation?.combine?.kind ?? null,
      requestCandidateConstruction: boundedJSONEvidence(requestConstruction),
      responseCandidateConstruction: boundedJSONEvidence(responseConstruction),
      expectedProposalPath,
      evidence,
    });
  if (!evidence.ok || !scope || !responseBound || event.status !== 200) throw new Error('Native Combine candidate did not use the exact current-draft workspace outputs.');
  report.target.nativeCandidateRequest = {
    path: event.path,
    url: event.url,
    status: event.status,
    requestId: event.requestId,
    outputId: event.body.outputId,
    inputRefs: evidence.actual,
    snapshotToken: event.body.snapshotToken,
    expectedDraftVersion: event.body.expectedDraftVersion,
    expectedDraftDigest: event.body.expectedDraftDigest,
  };
};

const readPreviewIdentity = async (page) => evaluate(page, `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');return {status:p?.dataset.previewStatus??null,receipt:p?.dataset.previewReceiptId??null,outputId:p?.dataset.previewOutputId??null,draftVersion:p?.dataset.currentDraftVersion??null,stale:Boolean(document.querySelector('[data-testid="construction-preview-stale-notice"]'))}})()`);

const savedStepEditorReady = (editorSelector) => {
  if (editorSelector.includes('construction-combine-editor')) {
    return `(()=>{const e=document.querySelector(${JSON.stringify(editorSelector)});if(!e)return false;const fieldset=e.querySelector('fieldset');const inputs=[...e.querySelectorAll('select[aria-label^="Input table "]')];const maps=[...e.querySelectorAll('select[aria-label*=" field in input "]')];const labels=[...e.querySelectorAll('input[aria-label^="Output field "][aria-label$=" label"]')];return Boolean(fieldset&&!fieldset.matches(':disabled')&&inputs.length>=2&&inputs.every(s=>!s.matches(':disabled')&&s.options.length>1)&&maps.length>0&&maps.every(s=>!s.matches(':disabled')&&s.options.length>1)&&labels.length>0&&labels.every(input=>!input.matches(':disabled')));})()`;
  }
  if (editorSelector.includes('construction-reshape-group')) {
    return `(()=>{const e=document.querySelector(${JSON.stringify(editorSelector)});if(!e)return false;const summary=e.querySelector('select[aria-label="Summary 1"]');const keys=[...e.querySelectorAll('input[type="checkbox"][aria-label^="Group by "]')];return Boolean(summary&&!summary.matches(':disabled')&&keys.length>0&&keys.every(input=>!input.matches(':disabled')));})()`;
  }
  throw new Error('No readiness contract is defined for saved step editor ' + editorSelector);
};

const editSavedStep = async (page, report, outputId, stepId, editorSelector) => {
  await selectTable(page, outputId);
  await click(page, '[data-testid="construction-history-step-' + stepId + '"]');
  await waitFor(page, 'Boolean(document.querySelector(' + JSON.stringify('[data-testid="construction-edit-step-' + stepId + '"]:not(:disabled)') + '))', 10000);
  await recordBrowserTiming(report, page, {
    name: editorSelector.includes('construction-combine-editor')
      ? 'open saved Combine editor after draft input schemas resolve'
      : 'open saved Group editor after source columns resolve',
    action: () => click(page, '[data-testid="construction-edit-step-' + stepId + '"]'),
    after: savedStepEditorReady(editorSelector),
    timeout: 5000,
    budget: 5000,
  });
};

const cancelStepRemovalAndPreserve = async (context, page, report, explorer, target, stepId, savedRows) => {
  await selectTable(page, target.outputId);
  const before = await readBuilder(context, explorer);
  await click(page, '[data-testid="construction-history-step-' + stepId + '"]');
  await waitFor(page, 'Boolean(document.querySelector(' + JSON.stringify('[data-testid="construction-remove-step-' + stepId + '"]:not(:disabled)') + '))', 10000);
  await recordBrowserTiming(report, page, {
    name: 'propose removal of the saved Combine step and render its exact target',
    action: () => click(page, '[data-testid="construction-remove-step-' + stepId + '"]'),
    after: proposalReady(target.outputId) + '&&' + proposalRowsReady(0, target.outputId),
    timeout: 5000,
    budget: 5000,
  });
  await recordBrowserTiming(report, page, {
    name: 'Cancel proposed Combine removal without changing saved workspace',
    action: () => click(page, '[data-testid="construction-cancel-proposal"]'),
    after: "Boolean(document.querySelector('[data-testid=construction-history]'))&&!document.querySelector('[data-testid=construction-proposal-panel]')",
    timeout: 5000,
    budget: 5000,
  });
  await reloadAndSelectSavedTable(report, page, target.outputId, savedRows, 'reload saved Combine after canceling removal');
  const after = await readBuilder(context, explorer);
  const evidence = canceledDraftEvidence(before, after);
  const document = documentByOutput(after, target.outputId);
  const stepStillSaved = document.construction?.steps?.some((step) => step.id === stepId && step.operation.kind === 'COMBINE');
  check(report, 'persistence', 'Canceling Combine removal preserves its saved step, full workspace, and draft CAS after reload', evidence.ok && stepStillSaved,
    { evidence, stepStillSaved, targetOutputId: target.outputId, steps: document.construction?.steps?.map((step) => step.id) ?? [] });
};

const removeStepAndRestoreTarget = async (context, page, report, explorer, target, stepId) => {
  await selectTable(page, target.outputId);
  await click(page, '[data-testid="construction-history-step-' + stepId + '"]');
  await waitFor(page, 'Boolean(document.querySelector(' + JSON.stringify('[data-testid="construction-remove-step-' + stepId + '"]:not(:disabled)') + '))', 10000);
  await recordBrowserTiming(report, page, {
    name: 'propose Combine removal and render the exact rooted empty output',
    action: () => click(page, '[data-testid="construction-remove-step-' + stepId + '"]'),
    after: proposalReady(target.outputId) + '&&' + proposalRowsReady(0, target.outputId),
    timeout: 5000,
    budget: 5000,
  });
  await recordBrowserTiming(report, page, {
    name: 'apply Combine removal to restore the empty target',
    action: () => click(page, '[data-testid="construction-apply-proposal"]'),
    after: '!document.querySelector(\'[data-testid="construction-proposal-panel"]\')&&' + selectedOutputReady(target.outputId),
    timeout: 5000,
    budget: 5000,
  });
  await reloadAndSelectSavedTable(report, page, target.outputId, undefined, 'reload removed Combine and select its exact rooted empty output');
  const builder = await readBuilder(context, explorer);
  const document = documentByOutput(builder, target.outputId);
  const restored = document.rootResourceType === target.rootResourceType && document.columns?.length === 0 &&
    (document.construction?.steps?.length ?? 0) === 0;
  check(report, 'persistence', 'removing Combine and reloading restores its rooted empty target', restored, {
    outputId: target.outputId,
    rootResourceType: document.rootResourceType,
    columns: document.columns,
    steps: document.construction?.steps ?? [],
  });
};

const createBlankExplorer = async (page, target, runID, label, report) => {
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'));
  await waitFor(page, "document.body.innerText.includes('Build your first table') || document.body.innerText.includes('Dataset graph')", 30000);
  const title = 'Verify ' + runID.slice(-10) + ' ' + label;
  await recordBrowserTiming(report, page, { name: 'open Explorer creation', action: () => click(page, 'summary', { name: 'New explorer' }), after: "Boolean(document.querySelector('#new-explorer-name'))", timeout: 5000 });
  await fill(page, '#new-explorer-name', title);
  await recordBrowserTiming(report, page, { name: 'create blank Explorer', action: () => click(page, 'button', { name: 'Create blank' }),
    after: "document.querySelector('select[aria-label=\"Explorer\"] option:checked')?.textContent.trim() === " + JSON.stringify(title) + " && document.body.innerText.includes('Build your first table')", timeout: 5000 });
  const explorer = await evaluate(page, "document.querySelector('select[aria-label=\"Explorer\"]')?.value || ''");
  check(report, 'correctness', 'created a fresh Explorer distinct from the bootstrap', Boolean(explorer && explorer !== target.bootstrapExplorerId), { title });
  return { explorer, title };
};

const beginOwnedWorkspace = async (context, page, report, label) => {
  assert.equal(context.custom, false, 'Draft Combine requires the owned isolated fixture project.');
  assert.equal(context.seed?.fresh, true, 'Draft Combine requires a fresh verification project.');
  assert(context.target.fixtureDir && context.target.fixtureGeneration, 'The exact Combine fixture and generation are required.');
  const raw = fixture(context.target.fixtureDir);
  report.target.fixtureRawOracle = {
    source: 'fresh project testdata/verify-combine NDJSON; rows are not derived from Builder previews',
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    patients: raw.patients,
    observations: raw.observations,
    diagnosticReports: raw.reports,
  };
  check(report, 'correctness', 'independent fixture contains one Patient, four Observations, and three DiagnosticReports',
    raw.patients.length === 1 && raw.observations.length === 4 && raw.reports.length === 3, report.target.fixtureRawOracle);
  const explorerInfo = await createBlankExplorer(page, context.target, context.runID, 'draft-combine-' + label, report);
  report.target.explorer = explorerInfo.explorer;
  const initial = await readBuilder(context, explorerInfo.explorer);
  const scope = readScope(context, explorerInfo.explorer, initial, undefined, 'empty');
  check(report, 'correctness', 'draft Combine uses the exact project, generation, Builder snapshot, and authorization scope', scope.ok, scope);
  if (!scope.ok) throw new Error('Fresh Builder scope is not bound to the requested owned fixture.');
  report.target.scope = scope;
  let publishRequests = 0;
  const onPublishRequest = (request) => {
    try { if (new URL(request.url()).pathname.endsWith('/authoring/v2/publish')) publishRequests += 1; } catch {}
  };
  page.on('request', onPublishRequest);
  return { explorer: explorerInfo.explorer, raw, stopPublish: () => page.off('request', onPublishRequest), publishCount: () => publishRequests };
};

export const draftJoinWorkflow = async ({ page, report }, context) => {
  const run = await beginOwnedWorkspace(context, page, report, 'join');
  const previewLifecycle = captureOwnedPreviewLifecycle(page, context.target, run.explorer);
  const sources = [];
  try {
    sources.push(await createGroupSource(context, page, report, run.explorer, {
      resourceType: 'Observation', title: 'Observation status counts', fieldPath: 'status', rawRows: run.raw.observations,
    }));
    sources.push(await createGroupSource(context, page, report, run.explorer, {
      resourceType: 'DiagnosticReport', title: 'Report status counts', fieldPath: 'status', rawRows: run.raw.reports,
    }));
    const beforeTarget = await readBuilder(context, run.explorer);
    let target = await startCombineTarget(context, page, report, run.explorer, sources[0].outputId, beforeTarget);
    report.target.combineTarget = target;
    const base = await readBuilder(context, run.explorer);
    const capture = capturePost(page, '/construction-proposals');
    try {
      const expectedInner = joinGroupedCounts(groupCounts(run.raw.observations, 'status'), groupCounts(run.raw.reports, 'status'));
      const finalMapping = await configureJoin(page, sources, 'INNER');
      await renderFinalMapping(page, report, 'INNER grouped draft Join auto-preview action-to-render within five seconds', finalMapping, expectedInner.length, target.outputId);
      await waitProposal(page, expectedInner.length, target.outputId);
      const initialGrid = await readGrid(page, 'proposal');
      const headers = ['Observation status', 'Observation rows', 'Report status', 'Report rows'];
      assertRows(report, 'two unpublished GROUP siblings join on their exact shared statuses', initialGrid, headers,
        expectedInner.map((row) => row.map((value) => value === '—' ? '—' : String(value))));
      await assertDraftCandidate(report, page, capture, base, target, sources, 'KEY_JOIN');
      const cancelBase = await readBuilder(context, run.explorer);
      await recordBrowserTiming(report, page, {
        name: 'Cancel current-draft Join proposal without saving',
        action: () => click(page, '[data-testid="construction-cancel-proposal"]'),
        after: "!document.querySelector('[data-testid=construction-combine-editor]')&&!document.querySelector('[data-testid=construction-proposal-panel]')&&" + selectedOutputReady(target.outputId),
        timeout: 5000,
        budget: 5000,
      });
  await reloadAndSelectSavedTable(report, page, target.outputId, undefined, 'reload canceled Join proposal and select its unchanged empty target');
      const canceled = await readBuilder(context, run.explorer);
      const cancelEvidence = canceledDraftEvidence(cancelBase, canceled);
      check(report, 'persistence', 'Cancel leaves every workspace document and draft CAS unchanged after reload', cancelEvidence.ok, cancelEvidence);

      report.target.canceledCombineTarget = target;
      const retryBase = await readBuilder(context, run.explorer);
      target = await startCombineTarget(context, page, report, run.explorer, sources[0].outputId, retryBase);
      report.target.combineTarget = target;
      const leftCapture = capturePost(page, '/construction-proposals');
      try {
        const currentBase = await readBuilder(context, run.explorer);
        const expectedLeft = joinGroupedCounts(groupCounts(run.raw.observations, 'status'), groupCounts(run.raw.reports, 'status'), 'LEFT');
        const finalMapping = await configureJoin(page, sources, 'LEFT');
        await renderFinalMapping(page, report, 'LEFT grouped draft Join auto-preview action-to-render within five seconds', finalMapping, expectedLeft.length, target.outputId);
        await waitProposal(page, expectedLeft.length, target.outputId);
        const leftGrid = await readGrid(page, 'proposal');
        assertRows(report, 'LEFT Join keeps the unmatched unknown Observation group with null report values', leftGrid, headers,
          expectedLeft.map((row) => row.map((value) => value === '—' ? '—' : String(value))));
        await assertDraftCandidate(report, page, leftCapture, currentBase, target, sources, 'KEY_JOIN');
        await applyCurrentProposal(page, report, 'apply current-draft LEFT Join', expectedLeft.length, target.outputId);
      } finally { await leftCapture.stop(); }
      let builder = await readBuilder(context, run.explorer);
      let targetDocument = documentByOutput(builder, target.outputId);
      let step = targetDocument.construction?.steps?.at(-1);
      const draftEvidence = currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: sources.map((source) => source.outputId), sourceDocuments: builder.workspace.documents });
      check(report, 'persistence', 'saved Join step retains only the two exact unpublished sibling outputs', draftEvidence.ok, { stepId: step?.id, draftEvidence });
      await reloadAndSelectSavedTable(report, page, target.outputId, 3, 'reload applied LEFT Join and render its exact output');
      assertRows(report, 'applied LEFT Join rows survive reload', await readGrid(page), headers, [
        ['final', '2', 'final', '2'], ['preliminary', '1', 'preliminary', '1'], ['unknown', '1', '—', '—'],
      ]);

      const oldReceipt = (await readPreviewIdentity(page)).receipt;
      const sourceGroupStep = savedGroupStep(documentByOutput(builder, sources[0].outputId));
      await editSavedStep(page, report, sources[0].outputId, sourceGroupStep.id, '[data-testid="construction-reshape-group"]');
      const sourceEditStarted = Date.now();
      await chooseOption(page, 'select[aria-label="Summary 1"]', 'Count distinct values');
      await waitFor(page, "Boolean(document.querySelector('select[aria-label=\"Summary field 1\"]'))", 10000);
      await chooseOption(page, 'select[aria-label="Summary field 1"]', 'Status');
      const advanced = await evaluate(page, "(()=>{const d=document.querySelector('[data-testid=construction-reshape-group-advanced]');return Boolean(d&&!d.open)})()");
      if (advanced) await click(page, '[data-testid="construction-reshape-group-advanced"] summary');
      await waitFor(page, "Boolean(document.querySelector('input[aria-label=\"Summary output label 1\"]'))", 10000);
      const distinctLabel = 'Distinct status values';
      const distinctRows = groupCounts(run.raw.observations, 'status').map(([status]) => [status, 1]);
      await recordBrowserTiming(report, page, {
        name: 'edit upstream Observation GROUP aggregate and preview the exact distinct values',
        action: () => fill(page, 'input[aria-label="Summary output label 1"]', distinctLabel),
        after: proposalRowsReady(distinctRows.length, sources[0].outputId),
        timeout: 5000,
        budget: 5000,
      });
      const editedGroupGrid = await readGrid(page, 'proposal');
      assertRows(report, 'upstream COUNT_DISTINCT GROUP edit matches independent per-status raw values', editedGroupGrid,
        ['Status', distinctLabel], distinctRows.map((row) => row.map(String)));
      check(report, 'performance', 'upstream GROUP aggregate edit configuration and preview complete within five seconds', Date.now() - sourceEditStarted <= 5000,
        { elapsedMs: Date.now() - sourceEditStarted, sourceOutputId: sources[0].outputId });
      await applyCurrentProposal(page, report, 'apply upstream Observation COUNT_DISTINCT GROUP edit', distinctRows.length, sources[0].outputId);
      const afterSourceEdit = await readBuilder(context, run.explorer);
      const newScope = readScope(context, run.explorer, afterSourceEdit, report.target.scope, 'draft');
      check(report, 'persistence', 'source GROUP edit advances the same authorized workspace CAS', newScope.ok && afterSourceEdit.draftVersion > builder.draftVersion && afterSourceEdit.draftDigest !== builder.draftDigest, {
        beforeDraftVersion: builder.draftVersion, afterDraftVersion: afterSourceEdit.draftVersion,
        beforeDraftDigest: builder.draftDigest, afterDraftDigest: afterSourceEdit.draftDigest,
        authorizationScopeDigest: newScope.authorizationScopeDigest,
      });
      await recordBrowserTiming(report, page, {
        name: 'recompile downstream Join after the upstream GROUP aggregate changes',
        action: () => selectTable(page, target.outputId),
        after: savedPreviewOutput(3, target.outputId),
        timeout: 5000,
        budget: 5000,
      });
      const newPreview = await readPreviewIdentity(page);
      const recompile = sourceRecompileEvidence({ before: builder, after: afterSourceEdit, targetOutputId: target.outputId, sourceOutputId: sources[0].outputId, oldReceipt, newReceipt: newPreview.receipt });
      check(report, 'persistence', 'source edit invalidates and recompiles dependent Combine against the same draft output', recompile.ok && newPreview.status === 'ready' && !newPreview.stale && newPreview.outputId === target.outputId && Number(newPreview.draftVersion) === afterSourceEdit.draftVersion, { recompile, newPreview });
      const editedLeft = joinGroupedCounts(distinctRows, groupCounts(run.raw.reports, 'status'), 'LEFT').map((row) => row.map((value) => value === '—' ? '—' : String(value)));
      assertRows(report, 'dependent LEFT Join recomputes exact values after source aggregate edit', await readGrid(page), headers, editedLeft);
      await editSavedStep(page, report, target.outputId, step.id, '[data-testid="construction-combine-editor"]');
      const revisedLabelOption = await evaluate(page, "(()=>[...document.querySelector('select[aria-label=\"Output field 2 source field in input 1\"]')?.options??[]].map(o=>o.textContent.trim()))()");
      check(report, 'correctness', 'reopened Combine reads the edited source GROUP schema by stable output ID', revisedLabelOption.some((label) => label.includes(distinctLabel)), { revisedLabelOption });
      const combineEditBase = await readBuilder(context, run.explorer);
      const combineEditCapture = capturePost(page, '/construction-proposals');
      try {
        const innerAfterSourceEdit = joinGroupedCounts(distinctRows, groupCounts(run.raw.reports, 'status')).map((row) => row.map(String));
        await recordBrowserTiming(report, page, {
          name: 'edit saved Join to INNER and render its exact recomputed rows',
          action: () => setSelectValue(page, 'select[aria-label="If a row in the first table has no match"]', 'INNER'),
          after: proposalRowsReady(innerAfterSourceEdit.length, target.outputId),
          timeout: 5000,
          budget: 5000,
        });
        assertRows(report, 'edited saved Join changes to INNER with exact recomputed fixture rows', await readGrid(page, 'proposal'), headers, innerAfterSourceEdit);
        await assertDraftCandidate(report, page, combineEditCapture, combineEditBase, target, sources, 'KEY_JOIN');
        await applyCurrentProposal(page, report, 'apply saved Join edit from LEFT to INNER', innerAfterSourceEdit.length, target.outputId);
      } finally { await combineEditCapture.stop(); }
      const innerAfterSourceEdit = joinGroupedCounts(distinctRows, groupCounts(run.raw.reports, 'status'))
        .map((row) => row.map((value) => value === '—' ? '—' : String(value)));
      await reloadAndSelectSavedTable(report, page, target.outputId, innerAfterSourceEdit.length, 'reload edited INNER Join and render its exact output');
      assertRows(report, 'edited Join values and INNER policy survive reload', await readGrid(page), headers, innerAfterSourceEdit);
      await cancelStepRemovalAndPreserve(context, page, report, run.explorer, target, step.id, innerAfterSourceEdit.length);
      await removeStepAndRestoreTarget(context, page, report, run.explorer, target, step.id);
    } finally { await capture.stop(); }
    const after = await readBuilder(context, run.explorer);
    check(report, 'correctness', 'Join lifecycle made no Publish request or pinned revision reference', run.publishCount() === 0 && sources.every((source) => !JSON.stringify(after.workspace.documents).includes('TABLE_REVISION')), {
      publishRequests: run.publishCount(), inputKinds: after.workspace.documents.flatMap((document) => document.construction?.steps?.flatMap((step) => step.inputs ?? []) ?? []).map((input) => input.kind),
    });
  } finally {
    report.previewRequestLifecycle = previewLifecycle.stop();
    run.stopPublish();
  }
};

export const draftAppendWorkflow = async ({ page, report }, context) => {
  const run = await beginOwnedWorkspace(context, page, report, 'append');
  const sources = [];
  try {
    sources.push(await createGroupSource(context, page, report, run.explorer, { resourceType: 'Observation', title: 'Observation status counts', fieldPath: 'status', rawRows: run.raw.observations }));
    sources.push(await createGroupSource(context, page, report, run.explorer, { resourceType: 'DiagnosticReport', title: 'Report status counts', fieldPath: 'status', rawRows: run.raw.reports }));
    sources.push(await createGroupSource(context, page, report, run.explorer, {
      resourceType: 'Patient', title: 'Patient gender counts', fieldPath: 'gender', rawRows: run.raw.patients, deriveCountPlusOne: true,
    }));
    const beforeTarget = await readBuilder(context, run.explorer);
    let target = await startCombineTarget(context, page, report, run.explorer, sources[0].outputId, beforeTarget);
    report.target.combineTarget = target;
    const base = await readBuilder(context, run.explorer);
    const capture = capturePost(page, '/construction-proposals');
    try {
      const observationGroups = groupCounts(run.raw.observations, 'status');
      const reportGroups = groupCounts(run.raw.reports, 'status');
      const patientGroups = groupCounts(run.raw.patients, 'gender');
      const patientDerivedCounts = patientGroups.map(([gender, count]) => [gender, count + 1]);
      const groups = [
        { keyRows: observationGroups.map(([value]) => [value]), countRows: observationGroups },
        { keyRows: reportGroups.map(([value]) => [value]), countRows: reportGroups },
        { keyRows: patientGroups.map(([value]) => [value]), countRows: patientDerivedCounts },
      ];
      const expected = appendGroupedCounts(groups).map((row) => row.map(String));
      const finalMapping = await configureAppend(page, sources);
      await renderFinalMapping(page, report, 'three-sibling draft APPEND auto-preview action-to-render within five seconds', finalMapping, expected.length, target.outputId);
      await waitProposal(page, expected.length, target.outputId);
      const grid = await readGrid(page, 'proposal');
      assertRows(report, 'three unpublished current-draft siblings including GROUP→DERIVE APPEND into exact category/count rows', grid, ['Category', 'Row count'], expected);
      await assertDraftCandidate(report, page, capture, base, target, sources, 'APPEND');
      const cancelBase = await readBuilder(context, run.explorer);
      await recordBrowserTiming(report, page, {
        name: 'Cancel current-draft APPEND proposal without saving',
        action: () => click(page, '[data-testid="construction-cancel-proposal"]'),
        after: "!document.querySelector('[data-testid=construction-combine-editor]')&&!document.querySelector('[data-testid=construction-proposal-panel]')&&" + selectedOutputReady(target.outputId),
        timeout: 5000,
        budget: 5000,
      });
      await reloadAndSelectSavedTable(report, page, target.outputId, undefined, 'reload canceled APPEND proposal and select its unchanged empty target');
      const canceled = await readBuilder(context, run.explorer);
      const evidence = canceledDraftEvidence(cancelBase, canceled);
      check(report, 'persistence', 'Cancel leaves the entire APPEND workspace and draft CAS unchanged', evidence.ok, evidence);
      report.target.canceledCombineTarget = target;
      const retryBase = await readBuilder(context, run.explorer);
      target = await startCombineTarget(context, page, report, run.explorer, sources[0].outputId, retryBase);
      report.target.combineTarget = target;
      const applyCapture = capturePost(page, '/construction-proposals');
      try {
        const applyBase = await readBuilder(context, run.explorer);
        const finalMapping = await configureAppend(page, sources);
        await renderFinalMapping(page, report, 'three-sibling draft APPEND edit auto-preview action-to-render within five seconds', finalMapping, expected.length, target.outputId);
        await waitProposal(page, expected.length, target.outputId);
        await assertDraftCandidate(report, page, applyCapture, applyBase, target, sources, 'APPEND');
        await applyCurrentProposal(page, report, 'apply three-input current-draft APPEND', expected.length, target.outputId);
      } finally { await applyCapture.stop(); }
      let builder = await readBuilder(context, run.explorer);
      const saved = documentByOutput(builder, target.outputId);
      const step = saved.construction?.steps?.at(-1);
      const draftEvidence = currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: sources.map((source) => source.outputId), sourceDocuments: builder.workspace.documents });
      check(report, 'persistence', 'saved APPEND step uses exactly three current-draft grouped siblings', draftEvidence.ok, { stepId: step?.id, draftEvidence });
      await reloadAndSelectSavedTable(report, page, target.outputId, expected.length, 'reload applied APPEND and render its exact union');
      assertRows(report, 'three-input APPEND rows survive reload', await readGrid(page), ['Category', 'Row count'], expected);
      await editSavedStep(page, report, target.outputId, step.id, '[data-testid="construction-combine-editor"]');
      await recordBrowserTiming(report, page, {
        name: 'edit saved APPEND output label and preview all exact composed-source rows',
        action: () => fill(page, 'input[aria-label="Output field 1 label"]', 'Grouped category'),
        after: proposalRowsReady(expected.length, target.outputId),
        timeout: 5000,
        budget: 5000,
      });
      assertRows(report, 'edited APPEND label preserves every independent grouped row', await readGrid(page, 'proposal'), ['Grouped category', 'Row count'], expected);
      await applyCurrentProposal(page, report, 'apply APPEND label edit from its saved draft step', expected.length, target.outputId);
      builder = await readBuilder(context, run.explorer);
      const edited = documentByOutput(builder, target.outputId).construction.steps.at(-1);
      check(report, 'persistence', 'edited APPEND label retains its stable step and draft-only input refs', edited?.id === step.id && edited?.outputs?.[0]?.label === 'Grouped category' && currentDraftSourceEvidence({ inputs: edited?.inputs, expectedOutputIDs: sources.map((source) => source.outputId), sourceDocuments: builder.workspace.documents }).ok,
        { stepId: edited?.id, outputs: edited?.outputs, inputs: edited?.inputs });
      await reloadAndSelectSavedTable(report, page, target.outputId, expected.length, 'reload edited APPEND and render its exact union');
      assertRows(report, 'edited APPEND label and values survive reload', await readGrid(page), ['Grouped category', 'Row count'], expected);
      await cancelStepRemovalAndPreserve(context, page, report, run.explorer, target, step.id, expected.length);
      await removeStepAndRestoreTarget(context, page, report, run.explorer, target, step.id);
    } finally { await capture.stop(); }
    const final = await readBuilder(context, run.explorer);
    const inputs = final.workspace.documents.flatMap((document) => document.construction?.steps?.flatMap((step) => step.inputs ?? []) ?? []);
    check(report, 'correctness', 'APPEND lifecycle never publishes or pins source revisions', run.publishCount() === 0 && inputs.every((input) => input.kind !== 'TABLE_REVISION'), { publishRequests: run.publishCount(), inputKinds: inputs.map((input) => input.kind) });
  } finally { run.stopPublish(); }
};

export const groupPivotJoinWorkflow = async ({ page, report }, context) => {
  const run = await beginOwnedWorkspace(context, page, report, 'group-pivot');
  const sources = [];
  try {
    sources.push(await createGroupSource(context, page, report, run.explorer, {
      resourceType: 'Observation', title: 'Observation ID source', fieldPath: 'status', rawRows: run.raw.observations, groupByID: true,
    }));
    sources.push(await createGroupSource(context, page, report, run.explorer, {
      resourceType: 'DiagnosticReport', title: 'Report grouped pivot source', fieldPath: 'status', rawRows: run.raw.reports, composed: true,
    }));
    const beforeTarget = await readBuilder(context, run.explorer);
    let target = await startCombineTarget(context, page, report, run.explorer, sources[0].outputId, beforeTarget);
    report.target.combineTarget = target;
    const base = await readBuilder(context, run.explorer);
    const capture = capturePost(page, '/construction-proposals');
    const categories = [...new Set(run.raw.reports.map((row) => String(row.status)))].sort();
    const observationGroups = groupCounts(run.raw.observations, 'id');
    const reportPivot = groupedPivotRows(run.raw.reports, 'id', 'status', categories);
    const expectedInner = joinGroupPivotRows(observationGroups, reportPivot).map((row) => row.map((value) => value === null ? '—' : String(value)));
    const expectedLeft = joinGroupPivotRows(observationGroups, reportPivot, 'LEFT').map((row) => row.map((value) => value === null ? '—' : String(value)));
    const headers = ['Observation ID', 'Observation rows', 'DiagnosticReport ID', ...categories.map((category) => 'Report ' + category)];
    try {
      const finalMapping = await configureJoin(page, sources, 'INNER', 'group-pivot');
      await renderFinalMapping(page, report, 'mixed Group and Group→Pivot draft INNER Join auto-preview within five seconds', finalMapping, expectedInner.length, target.outputId);
      await waitProposal(page, expectedInner.length, target.outputId);
      assertRows(report, 'one GROUP sibling and one GROUP→PIVOT sibling join on their exact current-draft IDs', await readGrid(page, 'proposal'), headers, expectedInner);
      await assertDraftCandidate(report, page, capture, base, target, sources, 'KEY_JOIN');
      const cancelBase = await readBuilder(context, run.explorer);
      await recordBrowserTiming(report, page, {
        name: 'Cancel mixed Group/Pivot Join proposal without saving',
        action: () => click(page, '[data-testid="construction-cancel-proposal"]'),
        after: "!document.querySelector('[data-testid=construction-combine-editor]')&&!document.querySelector('[data-testid=construction-proposal-panel]')&&" + selectedOutputReady(target.outputId),
        timeout: 5000,
        budget: 5000,
      });
      await reloadAndSelectSavedTable(report, page, target.outputId, undefined, 'reload canceled mixed Join and select its unchanged empty target');
      const canceled = canceledDraftEvidence(cancelBase, await readBuilder(context, run.explorer));
      check(report, 'persistence', 'Cancel leaves both mixed source shapes, full workspace, and CAS unchanged after reload', canceled.ok, canceled);

      report.target.canceledCombineTarget = target;
      const retryBase = await readBuilder(context, run.explorer);
      target = await startCombineTarget(context, page, report, run.explorer, sources[0].outputId, retryBase);
      report.target.combineTarget = target;
      const applyBase = await readBuilder(context, run.explorer);
      const applyCapture = capturePost(page, '/construction-proposals');
      try {
        const leftMapping = await configureJoin(page, sources, 'LEFT', 'group-pivot');
        await renderFinalMapping(page, report, 'mixed Group/Pivot LEFT Join auto-preview within five seconds', leftMapping, expectedLeft.length, target.outputId);
        await waitProposal(page, expectedLeft.length, target.outputId);
        assertRows(report, 'LEFT mixed Join keeps the unmatched Observation identity with null Pivot values', await readGrid(page, 'proposal'), headers, expectedLeft);
        await assertDraftCandidate(report, page, applyCapture, applyBase, target, sources, 'KEY_JOIN');
        await applyCurrentProposal(page, report, 'apply mixed Group/Group→Pivot current-draft LEFT Join', expectedLeft.length, target.outputId);
      } finally { await applyCapture.stop(); }
      let builder = await readBuilder(context, run.explorer);
      const targetDocument = documentByOutput(builder, target.outputId);
      const step = targetDocument.construction?.steps?.at(-1);
      const refs = currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: sources.map((source) => source.outputId), sourceDocuments: builder.workspace.documents });
      check(report, 'persistence', 'mixed Join persists only the exact unpublished Group and Group→Pivot outputs', refs.ok, { stepId: step?.id, refs });
      await reloadAndSelectSavedTable(report, page, target.outputId, expectedLeft.length, 'reload applied mixed LEFT Join and render exact values');
      assertRows(report, 'mixed LEFT Join values survive reload exactly', await readGrid(page), headers, expectedLeft);

      await editSavedStep(page, report, target.outputId, step.id, '[data-testid="construction-combine-editor"]');
      const editBase = await readBuilder(context, run.explorer);
      const editCapture = capturePost(page, '/construction-proposals');
      try {
        await recordBrowserTiming(report, page, {
          name: 'edit mixed Group/Pivot Join to INNER and render exact matching identities',
          action: () => setSelectValue(page, 'select[aria-label="If a row in the first table has no match"]', 'INNER'),
          after: proposalRowsReady(expectedInner.length, target.outputId),
          timeout: 5000,
          budget: 5000,
        });
        assertRows(report, 'edited mixed Join INNER values match the independent raw fixture oracle', await readGrid(page, 'proposal'), headers, expectedInner);
        await assertDraftCandidate(report, page, editCapture, editBase, target, sources, 'KEY_JOIN');
        await applyCurrentProposal(page, report, 'apply mixed Group/Pivot Join edit to INNER', expectedInner.length, target.outputId);
      } finally { await editCapture.stop(); }
      await reloadAndSelectSavedTable(report, page, target.outputId, expectedInner.length, 'reload edited mixed INNER Join and render exact values');
      assertRows(report, 'edited mixed INNER Join exact values survive reload', await readGrid(page), headers, expectedInner);
      await cancelStepRemovalAndPreserve(context, page, report, run.explorer, target, step.id, expectedInner.length);
      await removeStepAndRestoreTarget(context, page, report, run.explorer, target, step.id);

      const final = await readBuilder(context, run.explorer);
      const savedSourceShapes = sources.map((source) => documentByOutput(final, source.outputId).construction?.steps?.map((item) => item.operation.kind) ?? []);
      check(report, 'persistence', 'mixed source lifecycle keeps one GROUP and one ordered GROUP→PIVOT without publication',
        run.publishCount() === 0 && JSON.stringify(savedSourceShapes) === JSON.stringify([['GROUP'], ['GROUP', 'PIVOT']]) &&
          final.workspace.documents.every((document) => (document.construction?.steps ?? []).flatMap((item) => item.inputs ?? []).every((input) => input.kind !== 'TABLE_REVISION')),
        { publishRequests: run.publishCount(), sourceSteps: savedSourceShapes });
    } finally { await capture.stop(); }
  } finally { run.stopPublish(); }
};
