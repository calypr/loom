import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { sanitizeBody, sanitizeReportPayload, sanitizeText } from '../helpers/playwright-browser.mjs';
import { requireUnique } from '../helpers/playwright-actions.mjs';
import {
  captureCDARequests,
  matchesExpectedEmptyCollectionValidation,
  matchesExpectedEmptyCollectionValidationConsole,
  navigateAfterOwnedConstructionCapabilities,
} from '../helpers/cda-playwright-requests.mjs';
import { waitForCondition } from '../helpers/playwright-observations.mjs';
import { CDA_ACTION_TO_RENDER_BUDGET_MS, summarizeCdaActionToRenderTimings } from '../helpers/cda-action-to-render-budget.mjs';
import { fixtureUnavailableOutcome } from '../helpers/cda-fixture-outcomes.mjs';
import {
  assertCountRowsGroupLabelEditIdentity,
  assertCountRowsGroupLabelEditProposal,
  assertBuilderDraftAdvanced,
  assertNoUnexpectedCdaDiagnostics,
  assertGroupRemovalCancelRestoration,
  selectMissingComponentGroupOracle,
} from '../helpers/missing-component-group-oracle.mjs';

export async function repeatedEmptyWorkflow({ page: nativePage, cda, mode = 'literal-empty' }) {
const missingGroupOnly = mode === 'missing-component-group';
assert(['literal-empty', 'missing-component-group'].includes(mode), `Unsupported repeated-component workflow mode: ${mode}`);
const values = {
  'api-origin': cda.apiOrigin,
  'ui-origin': cda.uiOrigin,
  'api-container': cda.target.apiContainer,
  'compose-project': cda.target.composeProject,
  project: cda.project,
  generation: cda.generation ?? 'cda-fhir-v1',
  evidence: cda.evidence,
  'arango-container': cda.target.arangoContainer,
};

const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = (missingGroupOnly ? 'cda-missing-component-group-' : 'cda-repeated-empty-') + Date.now();
assert.notEqual(explorer, protectedExplorer);
const sourceFreezeRoot = cda.target.sourceRoot ?? fileURLToPath(new URL('../../..', import.meta.url));
const apiBuildTarget = 'local-cda-api';
const apiContainer = values['api-container'];
const readApiBuildStamp = () => checkContainerApiBuildStamp(apiContainer);
const report = {
  errors: [],
  started: new Date().toISOString(),
  target: { apiOrigin: values['api-origin'], uiOrigin: values['ui-origin'], project: values.project, generation: values.generation, protectedExplorer },
  explorer, scenario: missingGroupOnly
    ? 'Owned CDA missing-component Observation source EXPANDED composed with authored GROUP COUNT_ROWS and removal-Cancel restoration checks.'
    : 'Owned CDA Observation.component[] source EXPANDED composed with authored GROUP COUNT_ROWS, with raw-value, preview, cancel, apply, reload, and removal restoration checks.',
  assertions: [], gaps: [], failures: [], timings: [], requests: [], browserRequests: [],
  browserErrors: { exceptions: [], console: [], modules: [], http: [], incidental: [] }, evidencePaths: [],
};
const root = '/api/v1/projects/' + encodeURIComponent(values.project) + '/explorers';
const base = root + '/' + encodeURIComponent(explorer) + '/authoring/v2';
const officialRequestCapture = cda.captureRequests(`${root}/${encodeURIComponent(explorer)}`);
const shapeSelect = 'select[aria-label="What should each row represent?"]';
const policySelect = 'select[aria-label="Unmatched record policy"]';
const tableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
let builder;
let outputId;
let sourceFreeze;
let frozenApiBuild;
let requestMonitor;
let expectedEmptyValidationBinding;
const protocolResponse = entry => requestMonitor.rawResponseBody(entry);
const inspectPage = (page, body) => page.evaluate(`(()=>{${body}})()`);
const waitForBrowser = (page, condition, timeout = 5000) => waitForCondition(page, condition, Math.min(5000, timeout));
const resolveActionLocator = async (page, selector, identity = {}) => {
  const candidates = page.locator(selector);
  const { name, includes } = identity;
  if (name === undefined && includes === undefined) return requireUnique(candidates, selector);
  const matches = await candidates.evaluateAll((nodes, wanted) => nodes.flatMap((node, index) => {
    const label = String(node.getAttribute('aria-label') || node.innerText || node.textContent || '')
      .replace(/\\s+/g, ' ').trim();
    const matched = wanted.name !== undefined ? label === wanted.name
      : label.toLocaleLowerCase().includes(wanted.includes.toLocaleLowerCase());
    return matched ? [index] : [];
  }), { name, includes });
  assert.equal(matches.length, 1, `${selector}: expected one matching control, found ${matches.length}`);
  return requireUnique(candidates.nth(matches[0]), `${selector} ${name ?? includes}`);
};
const click = async (page, selector, identity = {}, timeout = 5000) => {
  const label = `Click ${selector} ${identity.name ?? identity.includes ?? ''}`.trim();
  report.activeAction = { label, locator: selector, targetLocator: page.locator(selector), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector, identity);
  report.activeAction.targetLocator = locator;
  report.activeAction.locator = locator.toString();
  const elapsedMs = await cda.action(label, locator, target => target.click({ timeout }), { timeout });
  report.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const fill = async (page, selector, value, timeout = 5000) => {
  const label = `Fill ${selector}`;
  report.activeAction = { label, locator: selector, targetLocator: page.locator(selector), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector);
  report.activeAction.targetLocator = locator;
  report.activeAction.locator = locator.toString();
  const elapsedMs = await cda.action(label, locator, target => target.fill(value, { timeout }), { timeout, editable: true });
  report.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const selectOption = async (page, selector, value, timeout = 5000) => {
  const label = `Select ${value} in ${selector}`;
  report.activeAction = { label, locator: selector, targetLocator: page.locator(selector), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector);
  report.activeAction.targetLocator = locator;
  report.activeAction.locator = locator.toString();
  const elapsedMs = await cda.action(label, locator, target => target.selectOption(value, { timeout }), { timeout });
  report.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
let browserPending = new Set();

const q = value => JSON.stringify(value);
const recordAssertion = (name, evidence) => report.assertions.push({ name, status: 'passed', evidence });
const api = async (path, body, allowFailure = false) => {
  const requestId = 'cda-repeated-empty-' + randomUUID();
  const startedAt = Date.now();
  const response = await fetch(values['api-origin'] + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let responseBody;
  try { responseBody = JSON.parse(text); } catch { responseBody = text; }
  report.requests.push({ path, requestId, body: sanitizeReportPayload(body), status: response.status, durationMs: Date.now() - startedAt, response: sanitizeReportPayload(responseBody) });
  if (!allowFailure) assert(response.ok, response.status + ' ' + path + ': ' + JSON.stringify(responseBody));
  return { status: response.status, body: responseBody };
};
const identity = (value = builder) => ({
  snapshotToken: value.catalog.snapshotToken, expectedDraftVersion: value.draftVersion, expectedDraftDigest: value.draftDigest,
});
const command = async commands => {
  await api(base + '/commands', {
    ...identity(), commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10, commands,
  });
  builder = (await api(base + '/builder')).body;
};
const document = () => builder.workspace.documents.find(doc => doc.output.id === outputId);
const rowsReady = count => {
  return { kind: 'rows', selector: tableSelector, count, allowEmptyParent: true };
};
const domText = () => inspectPage(nativePage, 'return document.body.innerText;');
const laterSamePathRequest = entry => {
  const index = report.browserRequests.indexOf(entry);
  return report.browserRequests.slice(index + 1).find(candidate => candidate.path === entry.path && candidate.method === entry.method);
};
const responseEvidence = value => {
  if (value === undefined) return undefined;
  const source = typeof value === 'string' ? value : String(value);
  try { return JSON.parse(source); } catch { return source.slice(0, 32768); }
};

const boundedRawOracle = () => {
  const query = 'FOR r IN Observation FILTER r.project == ' + q(values.project) + ' AND r.dataset_generation == ' +
    q(values.generation) + ' SORT r.id LIMIT 1000 RETURN {id:r.id,project:r.project,generation:r.dataset_generation,payload:r.payload}';
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', 'print(JSON.stringify(db._query(' + q(query) + ').toArray()));',
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, 'Arango oracle returned no JSON array: ' + result.stdout.slice(0, 300));
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  assert(scanned.length <= 1000, 'Raw source scan exceeded the 1000 Observation bound');
  if (missingGroupOnly) {
    const oracle = selectMissingComponentGroupOracle(scanned, {
      project: values.project,
      generation: values.generation,
      scanLimit: 1000,
    });
    report.oracle = {
      source: 'ArangoDB raw Observation payloads',
      project: values.project,
      generation: values.generation,
      queryLimit: 1000,
      scanned: oracle.scanned,
      witnessScope: 'one two-item component source plus three records whose component property is absent; bounded sample only',
      status: oracle.status,
      ...(oracle.reason ? { reason: oracle.reason } : {}),
      selected: oracle.selected,
      ...(oracle.status === 'ready' ? {
        expectedComponentRows: oracle.expectedComponentRows,
        expectedRecordIDs: oracle.expectedRecordIDs,
        expectedPreservedEmptyIDs: oracle.expectedMissingOwnerIDs,
        expectedExpandedRowCount: oracle.expectedExpandedRowCount,
        expectedGroupRows: oracle.expectedGroupRows,
      } : {}),
      maxSelectedRoots: 4,
      maxExpectedExpandedRows: 5,
    };
    return oracle.selected;
  }
  const positives = scanned.flatMap(resource => {
    const component = resource.payload?.component;
    if (!Array.isArray(component) || component.length < 2 || component.length > 3) return [];
    const componentValues = component.map((item, ordinal) => ({ ordinal, value: item?.valueString }));
    if (!componentValues.every(item => typeof item.value === 'string' && item.value.trim().length > 0)) return [];
    if (new Set(componentValues.map(item => item.value)).size < 2) return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: resource.payload.resourceType, componentValues }];
  });
  const empties = scanned.flatMap(resource => {
    const payload = resource.payload ?? {};
    const component = payload.component;
    let emptyComponentKind;
    if (!Object.hasOwn(payload, 'component')) emptyComponentKind = 'missing';
    else if (component === null) emptyComponentKind = 'null';
    else if (Array.isArray(component) && component.length === 0) emptyComponentKind = 'empty-array';
    else if (!Array.isArray(component)) emptyComponentKind = 'non-array';
    else return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: payload.resourceType, emptyComponentKind }];
  });
  const positive = positives[0];
  if (!positive) {
    report.oracle = { source: 'ArangoDB raw Observation payloads', queryLimit: 1000, scanned: scanned.length, selected: [] };
    return [];
  }
  const emptyLimit = Math.min(3, 6 - positive.componentValues.length);
  const selectedEmpty = empties.filter(item => item.id !== positive.id).slice(0, emptyLimit);
  const selected = [positive, ...selectedEmpty];
  report.oracle = {
    source: 'ArangoDB raw Observation payloads', queryLimit: 1000, scanned: scanned.length,
    selected: selected.map(item => item.componentValues
      ? { id: item.id, generation: item.generation, resourceType: item.resourceType, componentValues: item.componentValues }
      : { id: item.id, generation: item.generation, resourceType: item.resourceType, emptyComponentKind: item.emptyComponentKind }),
    expectedComponentRows: positive.componentValues.map(({ ordinal, value }) => ({ id: positive.id, ordinal, value })),
    expectedRecordIDs: selected.map(item => item.id),
    expectedPreservedEmptyIDs: selectedEmpty.map(item => item.id),
    maxSelectedRoots: 4, maxExpectedExpandedRows: 6,
  };
  return selected;
};

const monitorBrowser = () => {
  requestMonitor = captureCDARequests(nativePage, {
    apiOrigin: values['ui-origin'],
    appOrigins: [values['api-origin'], values['ui-origin']],
    ownedPathPrefix: `${root}/${encodeURIComponent(explorer)}`,
    report: { nativeRequests: report.browserRequests, errors: report.errors },
    shouldReportRequestFailure: (entry, request) => {
      const replacement = laterSamePathRequest(entry);
      return request.failure()?.errorText === 'net::ERR_ABORTED'
        && ['/row-definition-proposals', '/construction-proposals'].some(path => entry.path.endsWith(path))
        && replacement
        ? { expected: true, reason: `A later same-path owned draft request (${replacement.requestId}) superseded this proposal.` } : true;
    },
  });
  browserPending = requestMonitor.pendingReads;
  nativePage.on('response', response => {
    const url = new URL(response.url());
    if (url.origin !== new URL(values['ui-origin']).origin || response.status() < 400) return;
    const request = requestMonitor.byRequest.get(response.request());
    const incident = {
      url: sanitizeText(response.url()), status: response.status(),
      ...(request ? { browserRequestId: request.browserRequestId, requestId: request.requestId } : {}),
    };
    if (url.pathname.endsWith('/favicon.ico')) report.browserErrors.incidental.push(incident);
    else report.browserErrors.http.push(incident);
  });
  nativePage.on('requestfailed', request => {
    const url = new URL(request.url());
    if (url.origin !== new URL(values['ui-origin']).origin || request.failure()?.errorText === 'net::ERR_ABORTED') return;
    if (request.resourceType() === 'script') report.browserErrors.modules.push({ url: sanitizeText(request.url()), error: sanitizeText(request.failure()?.errorText) });
  });
};
const remaining = startedAt => Math.max(100, 5000 - (Date.now() - startedAt));
const fastWait = async (startedAt, condition, message) => {
  try { await waitForBrowser(nativePage, condition, remaining(startedAt)); }
  catch (error) { throw new Error(message + ' within the five-second action budget: ' + String(error)); }
};
const measure = async (name, action) => {
  const startedAt = Date.now();
  const result = await action(startedAt);
  const durationMs = Date.now() - startedAt;
  report.timings.push({ name, durationMs, limitMs: CDA_ACTION_TO_RENDER_BUDGET_MS });
  assert(durationMs <= CDA_ACTION_TO_RENDER_BUDGET_MS, name + ' took ' + durationMs + 'ms');
  return result;
};
const tableURL = () => values['ui-origin'] + '/?project=' + encodeURIComponent(values.project) + '&explorer=' + encodeURIComponent(explorer) + '&mode=builder';
const openTable = async (expectedRows, name) => measure(name, async startedAt => {
  const navigation = await navigateAfterOwnedConstructionCapabilities(
    cda,
    requestMonitor,
    () => report.browserRequests,
    base + '/construction-capabilities',
    Math.max(1, startedAt + CDA_ACTION_TO_RENDER_BUDGET_MS - Date.now()),
    () => cda.navigate(tableURL()),
  );
  if (navigation.settledEntries.length) {
    (report.preNavigationCapabilitySettlements ??= []).push({
      observedAt: new Date().toISOString(),
      requests: navigation.settledEntries.map(entry => ({
        requestId: entry.requestId,
        browserRequestId: entry.browserRequestId,
        status: entry.status,
        startedAt: entry.startedAt,
        completedAt: entry.completedAt,
      })),
    });
  }
  const table = '[data-testid="construction-table-' + outputId + '"]';
  await fastWait(startedAt, { kind: 'present', selector: table }, 'Explorer table discovery');
  await click(nativePage, table);
  await fastWait(startedAt, rowsReady(expectedRows), 'CDA table render');
});
const openRowSettings = async () => measure('row-definition-choice-discovery', async startedAt => {
  await click(nativePage, '[data-testid="construction-rows-settings-trigger"]');
  await fastWait(startedAt, { kind: 'enabled', selector: shapeSelect }, 'Row definition choice discovery');
});
const rowProposalRequest = selection => report.browserRequests.findLast(entry =>
  entry.path.endsWith('/row-definition-proposals') && entry.body?.selection?.kind === selection.kind &&
  (selection.kind !== 'EXPANDED' || (entry.body.selection.expanded?.rowChoiceId === selection.expanded.rowChoiceId &&
    entry.body.selection.expanded?.emptyCollectionPolicy === selection.expanded.emptyCollectionPolicy)));
const awaitRequestBody = async (entry, startedAt) => {
  assert(entry, 'Native row proposal request was not captured');
  if (entry.status === undefined) {
    await nativePage.waitForResponse(response => requestMonitor.byRequest.get(response.request()) === entry,
      { timeout: Math.max(1, 5000 - (Date.now() - startedAt)) });
  }
  await requestMonitor.flush();
  assert(entry && protocolResponse(entry) !== undefined, 'Native row proposal response was not captured: ' + JSON.stringify(entry));
};
const selectAndPreview = async (policy, expectedRows, emptyError = false) => measure('row-definition-preview-' + policy, async startedAt => {
  expectedEmptyValidationBinding = emptyError ? {
    path: `${base}/row-definition-proposals`,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    outputId,
    rowChoiceId: report.choice.choiceId,
    code: 'EMPTY_COLLECTION_ERROR',
    stage: 'row-definition-proposal',
  } : undefined;
  const selectedShape = await inspectPage(nativePage, 'return document.querySelector(' + q(shapeSelect) + ')?.value;');
  if (selectedShape !== 'expanded:' + report.choice.choiceId) {
    await selectOption(nativePage, shapeSelect, 'expanded:' + report.choice.choiceId);
  }
  const wanted = 'expanded:' + report.choice.choiceId + ':' + policy;
  const selectedPolicy = await inspectPage(nativePage, 'return document.querySelector(' + q(policySelect) + ')?.value;');
  if (selectedPolicy !== wanted) await selectOption(nativePage, policySelect, wanted);
  const terminal = emptyError
    ? { kind: 'any', conditions: [
      { kind: 'present', selector: '[aria-label="Row definition settings"] [role="alert"]' },
      { kind: 'present', selector: '[aria-label="Row definition preview"]' },
    ] }
    : { kind: 'text-includes', selector: '[aria-label="Row definition preview"]', text: '→ ' + expectedRows + ' rows' };
  await fastWait(startedAt, terminal, emptyError ? 'Expected empty-collection validation' : 'Automatic row definition preview');
  await Promise.all([...browserPending]);
  const selection = { kind: 'EXPANDED', expanded: { rowChoiceId: report.choice.choiceId, emptyCollectionPolicy: policy } };
  const request = rowProposalRequest(selection);
  await awaitRequestBody(request, startedAt);
  report.latestProposal = request;
  if (!emptyError) {
    const response = protocolResponse(request);
    assert.equal(request.status, 200, 'Row-definition ' + policy + ' proposal response: ' + JSON.stringify(request.response));
    assert.equal(response?.mode, 'EXPANDED');
    assert.equal(response?.comparison?.candidate?.rowCount, expectedRows);
  }
  return request;
});
const selectRecordsPreview = expectedRows => measure('row-definition-preview-records', async startedAt => {
  await selectOption(nativePage, shapeSelect, 'records');
  await fastWait(startedAt, { kind: 'text-includes', selector: '[aria-label="Row definition preview"]', text: '→ ' + expectedRows + ' rows' }, 'Source-record row preview');
  await Promise.all([...browserPending]);
  const request = rowProposalRequest({ kind: 'RECORDS' });
  await awaitRequestBody(request, startedAt);
  const response = protocolResponse(request);
  assert.equal(request.status, 200, 'RECORDS proposal response: ' + JSON.stringify(request.response));
  assert.equal(response?.comparison?.candidate?.rowCount, expectedRows);
  report.latestProposal = request;
});
const applyRowDefinition = expectedRows => measure('row-definition-apply', async startedAt => {
  await click(nativePage, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(expectedRows)] }, 'Applied row definition render');
});
const cancelRowDefinition = expectedRows => measure('row-definition-cancel', async startedAt => {
  await click(nativePage, '[aria-label="Row definition settings"] button', { name: 'Cancel' });
  await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(expectedRows)] }, 'Canceled row definition restoration');
});
const fieldPreviewRows = () => inspectPage(nativePage,
  'const proposalRow=document.querySelector(\'[data-testid="construction-proposal-preview-row"]\');' +
  'const root=proposalRow?.closest("table")??document.querySelector(\'[data-testid="preview-table-scroll"] [role="table"]\');' +
  'if(!root)return null;const proposal=Boolean(proposalRow);' +
  'const headers=[...root.querySelectorAll(proposal?"thead th":"[role=columnheader]")].map(cell=>cell.innerText.trim());' +
  'const rows=[...root.querySelectorAll(proposal?\'[data-testid="construction-proposal-preview-row"]\':"[role=row]")].slice(proposal?0:1)' +
  '.map(row=>[...row.querySelectorAll(proposal?"td":"[role=cell]")].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))).filter(row=>row.length);' +
  'return {headers,rows,rowCount:root.getAttribute("aria-rowcount")};');
const parseCell = cell => {
  if (cell?.raw) { try { return JSON.parse(cell.raw); } catch { return cell.raw; } }
  const value = cell?.text?.trim() ?? '';
  if (!value) return '';
  try { return JSON.parse(value); } catch { return value; }
};
const columnIndices = preview => {
  assert(preview, 'Native table preview is unavailable');
  const idIndex = preview.headers.findIndex(header => header.split(String.fromCharCode(10))[0].trim().toUpperCase() === 'OBSERVATION ID');
  const valueIndex = preview.headers.findIndex(header => /component.*value.?string/i.test(header));
  assert(idIndex >= 0, 'Observation ID is missing from native preview: ' + JSON.stringify(preview.headers));
  assert(valueIndex >= 0, 'component[].valueString is missing from native preview: ' + JSON.stringify(preview.headers));
  return { idIndex, valueIndex };
};
const verifyPreview = async (policy, expectedPairs, emptyIDs = []) => {
  const preview = await fieldPreviewRows();
  const { idIndex, valueIndex } = columnIndices(preview);
  assert.equal(preview.rows.length, expectedPairs.length + (policy === 'PRESERVE_PARENT' ? emptyIDs.length : 0), policy + ' native preview row count');
  const emptySet = new Set(emptyIDs);
  const actualPairs = [];
  const preservedEmptyIDs = [];
  for (const row of preview.rows) {
    const id = String(parseCell(row[idIndex]));
    const value = parseCell(row[valueIndex]);
    if (emptySet.has(id)) {
      preservedEmptyIDs.push(id);
      assert(Array.isArray(value) ? value.length === 0 : value === '' || value === null || (value === '—' && !row[valueIndex].raw),
        'Preserved empty-component row ' + id + ' contains a value: ' + JSON.stringify(value));
    } else {
      const items = Array.isArray(value) ? value : [value];
      assert.equal(items.length, 1, 'Expanded row ' + id + ' must contain one component value: ' + JSON.stringify(value));
      assert.equal(typeof items[0], 'string', 'Expanded component value must be text');
      actualPairs.push([id, items[0]]);
    }
  }
  const sortPairs = pairs => pairs.slice().sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expected = report.oracle.expectedComponentRows.map(item => [item.id, item.value]);
  assert.deepEqual(sortPairs(actualPairs), sortPairs(expected), policy + ' values must match raw component array items');
  assert.deepEqual(preservedEmptyIDs.sort(), (policy === 'PRESERVE_PARENT' ? emptyIDs : []).slice().sort(), policy + ' empty owner rows');
  const request = report.browserRequests.findLast(entry => entry.path.endsWith('/preview') && entry.status === 200 && protocolResponse(entry));
  const response = protocolResponse(request);
  assert(response?.rows && response?.columns, 'Saved native preview protocol is missing');
  const idColumn = response.columns.find(column => column.label === 'Observation ID')?.column;
  const valueColumn = response.columns.find(column => /component.*value.?string/i.test(column.label))?.column;
  assert(idColumn && valueColumn, 'Saved native preview protocol is missing field metadata');
  for (const id of preservedEmptyIDs) {
    const rows = response.rows.filter(row => row[idColumn] === id);
    assert.equal(rows.length, 1, 'Saved protocol must preserve exactly one empty owner row');
    assert.deepEqual(rows[0][valueColumn], [], 'Rendered empty placeholder must correspond to an empty value list');
  }
  return { headers: preview.headers, rowCount: preview.rows.length, positivePairs: sortPairs(actualPairs), preservedEmptyIDs };
};
const sortedRows = rows => rows.slice().sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
const expectedGroupRows = () => missingGroupOnly
  ? report.oracle.expectedGroupRows
  : sortedRows([
    ...report.oracle.expectedComponentRows.reduce((groups, item) => {
      const existing = groups.find(group => group[0] === item.id);
      if (existing) existing[1] += 1;
      else groups.push([item.id, 1]);
      return groups;
    }, []),
    ...report.oracle.expectedPreservedEmptyIDs.map(id => [id, 1]),
  ]);
const constructionProposalFor = async (name, startedAt) => {
  await nativePage.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel && ['ready', 'error', 'needs-repair'].includes(panel.dataset.proposalStatus);
  }, undefined, { timeout: Math.max(1, 5000 - (Date.now() - startedAt)) });
  const panel = await inspectPage(nativePage, `const value=document.querySelector('[data-testid="construction-proposal-panel"]');const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {id:value?.dataset.proposalId,status:value?.dataset.proposalStatus,text:value?.innerText,receiptId:preview?.dataset.previewReceiptId};`);
  const request = report.browserRequests.findLast(entry => entry.startedAt >= startedAt && entry.path.endsWith('/construction-proposals'));
  assert(request, `${name} native construction request was not captured`);
  await awaitRequestBody(request, startedAt);
  assert.equal(panel.status, 'ready', `${name} proposal is not ready: ${panel.text}`);
  assert.equal(request.status, 200, `${name} proposal request failed: ${JSON.stringify(request.response)}`);
  const response = protocolResponse(request);
  assert.equal(response?.previewStatus, 'READY', `${name} response preview is not ready: ${JSON.stringify(request.response)}`);
  assert(response?.preview?.receiptId, `${name} proposal is missing a preview receipt`);
  request.durationMs ??= request.responseReceivedAt - request.startedAt;
  assert(Number.isFinite(request.durationMs), `${name} native proposal request timing is unavailable`);
  assert(request.durationMs <= 5000, `${name} native proposal request took ${request.durationMs} ms`);
  assert(response.previewDurationMs <= 5000, `${name} preview took ${response.previewDurationMs} ms`);
  assert.equal(panel.receiptId, response.preview.receiptId, `${name} UI did not adopt the exact automatic-preview receipt`);
  const durationMs = Date.now() - startedAt;
  report.timings.push({ name, durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  return request;
};
const resolveGroupInputColumn = async () => {
  builder = (await api(base + '/builder')).body;
  const authoredColumns = document().columns.filter(column => column.label === 'Observation ID');
  assert.equal(authoredColumns.length, 1, 'The saved source must contain one Observation ID field');
  const authoredColumn = authoredColumns[0];
  assert(authoredColumn.column, 'Saved Observation ID field is missing its physical source column name');
  const owner = {
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    outputId,
    stageId: 'source_projection',
  };
  const capabilities = (await api(base + '/construction-capabilities', owner)).body;
  assert.equal(capabilities.snapshotToken, owner.snapshotToken, 'Construction capabilities belong to a different catalog snapshot');
  assert.equal(capabilities.draftVersion, owner.expectedDraftVersion, 'Construction capabilities belong to a different draft version');
  assert.equal(capabilities.draftDigest, owner.expectedDraftDigest, 'Construction capabilities belong to a different draft digest');
  assert.equal(capabilities.outputId, owner.outputId, 'Construction capabilities belong to a different output');
  assert.equal(capabilities.stageId, owner.stageId, 'Construction capabilities belong to a different requested stage');
  assert.equal(capabilities.selectedStage?.id, owner.stageId, 'Construction capabilities selected a different stage');
  const sourceColumns = document().columns.map(sourceColumn => {
    assert(sourceColumn.column, 'Saved source field is missing its physical column name');
    const matches = capabilities.selectedStage.columns.filter(column => !column.internal && column.name === sourceColumn.column);
    assert.equal(matches.length, 1, 'The current source-stage receipt must identify saved field ' + sourceColumn.label + ' by its exact physical name');
    const capabilityColumn = matches[0];
    assert.equal(capabilityColumn.label, sourceColumn.label, 'Source-stage field label differs from saved field ' + sourceColumn.label);
    return { authoredName: sourceColumn.column, label: sourceColumn.label, capabilityColumn };
  });
  const idSourceColumn = sourceColumns.find(column => column.label === authoredColumn.label);
  assert(idSourceColumn, 'The current source-stage receipt omitted the saved Observation ID field');
  return { owner, authoredName: authoredColumn.column, capabilityColumn: idSourceColumn.capabilityColumn, sourceColumns };
};
const assertSourceColumnBindings = (actualColumns, expectedColumns, sourceReceipt, name, requireReceiptIDs) => {
  const withoutColumnIDs = columns => columns.map(({ columnId, ...column }) => column);
  assert.deepEqual(withoutColumnIDs(actualColumns), withoutColumnIDs(expectedColumns), name + ' changed saved source field bindings');
  assert.equal(actualColumns.length, expectedColumns.length, name + ' changed the saved source field count');
  for (let index = 0; index < expectedColumns.length; index += 1) {
    const expected = expectedColumns[index];
    const actual = actualColumns[index];
    if (expected.columnId) {
      assert.equal(actual.columnId, expected.columnId, name + ' changed previously assigned source identity for ' + expected.label);
      continue;
    }
    if (!requireReceiptIDs) {
      assert.equal(actual.columnId, undefined, name + ' assigned a source identity before Group Apply for ' + expected.label);
      continue;
    }
    const matches = sourceReceipt.sourceColumns.filter(column => column.authoredName === expected.column);
    assert.equal(matches.length, 1, name + ' has no unique owner-bound source identity for ' + expected.label);
    assert.equal(actual.columnId, matches[0].capabilityColumn.id, name + ' did not retain the owner-bound source identity for ' + expected.label);
  }
};
const assertGroupProposal = (request, expected, inputColumn) => {
  const step = request.body?.candidateConstruction?.steps?.find(item => item.operation?.kind === 'GROUP');
  assert(step, 'Native proposal did not author a GROUP step');
  assert.equal(step.operation.group.keys.length, 1, 'Source-expanded regression must group only by Observation ID');
  assert.equal(inputColumn.capabilityColumn.name, inputColumn.authoredName, 'GROUP input capability is not bound to the exact authored source field');
  assert.equal(step.operation.group.keys[0].inputColumnId, inputColumn.capabilityColumn.id, 'GROUP must use the exact receipt-bound source Observation ID column');
  const count = step.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
  assert(count, 'GROUP must count expanded rows');
  const keyOutput = step.outputs.find(column => column.id === step.operation.group.keys[0].outputColumnId);
  const countOutput = step.outputs.find(column => column.id === count.outputColumnId);
  assert(keyOutput?.name && countOutput?.name, 'GROUP key/count outputs are missing stable names');
  const preview = protocolResponse(request).preview;
  assert.equal(preview.rowCount, expected.length, 'GROUP preview row count differs from raw Observation witnesses');
  const key = preview.columns.find(column => column.column === keyOutput.name);
  const rows = preview.columns.find(column => column.column === countOutput.name);
  assert(key && rows, `GROUP preview omitted authored output columns ${keyOutput.name}/${countOutput.name}`);
  const actual = sortedRows(preview.rows.map(row => [String(row[key.column]), Number(row[rows.column])]));
  assert.deepEqual(actual, expected, 'GROUP proposal counts differ from the independent raw component[] oracle');
  return {
    step, keyOutput, countOutput, previewRows: actual, receiptId: preview.receiptId,
    sourceInput: {
      sourceColumn: inputColumn.authoredName, capabilityColumnId: inputColumn.capabilityColumn.id,
      stageId: inputColumn.owner.stageId, snapshotToken: inputColumn.owner.snapshotToken,
      draftVersion: inputColumn.owner.expectedDraftVersion, draftDigest: inputColumn.owner.expectedDraftDigest,
      outputId: inputColumn.owner.outputId,
      columns: inputColumn.sourceColumns.map(sourceColumn => ({
        sourceColumn: sourceColumn.authoredName, capabilityColumnId: sourceColumn.capabilityColumn.id,
        label: sourceColumn.label,
      })),
    },
  };
};
const assertExpandedProposal = (request, expectedRows) => {
  const preview = protocolResponse(request)?.preview;
  assert(preview?.rows && preview?.columns, 'Removing GROUP did not return an expanded-source preview');
  assert.equal(preview.rowCount, expectedRows, 'Removing GROUP preview did not restore exact expanded-source cardinality');
  const idColumn = preview.columns.find(column => column.label === 'Observation ID')?.column;
  const valueColumn = preview.columns.find(column => /component.*value.?string/i.test(column.label))?.column;
  assert(idColumn && valueColumn, 'Removing GROUP preview omitted source field bindings');
  const emptyIDs = new Set(report.oracle.expectedPreservedEmptyIDs);
  const actualPairs = [];
  const preservedEmptyIDs = [];
  for (const row of preview.rows) {
    const id = String(row[idColumn]);
    const value = row[valueColumn];
    if (emptyIDs.has(id)) {
      preservedEmptyIDs.push(id);
      assert.deepEqual(value, [], `Removing GROUP preview must preserve an empty component list for ${id}`);
    } else {
      const items = Array.isArray(value) ? value : [value];
      assert.equal(items.length, 1, `Removing GROUP must restore one source component item per row for ${id}`);
      actualPairs.push([id, items[0]]);
    }
  }
  const expectedPairs = report.oracle.expectedComponentRows.map(item => [item.id, item.value]);
  assert.deepEqual(sortedRows(actualPairs), sortedRows(expectedPairs), 'Removing GROUP preview differs from raw component[] values');
  assert.deepEqual(preservedEmptyIDs.sort(), [...emptyIDs].sort(), 'Removing GROUP preview lost preserved empty owners');
  return { rowCount: preview.rowCount, positivePairs: sortedRows(actualPairs), preservedEmptyIDs };
};
const groupKeyControl = 'input[aria-label="Group by Observation ID"]';
const chooseOnlyObservationIDGroupKey = async (startedAt) => {
  const controls = await inspectPage(nativePage, `return [...document.querySelectorAll('[data-testid="construction-reshape-group"] input[type="checkbox"][aria-label^="Group by "]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled}));`);
  const target = controls.find(control => control.label === 'Group by Observation ID');
  assert(target && !target.disabled, `GROUP editor did not expose an enabled Observation ID key: ${JSON.stringify(controls)}`);
  for (const control of controls) {
    if (control.checked && control.label !== 'Group by Observation ID') {
      await click(nativePage, `input[aria-label=${q(control.label)}]`);
    }
  }
  let current = await inspectPage(nativePage, `return document.querySelector(${q(groupKeyControl)})?.checked === true;`);
  if (current) {
    await click(nativePage, groupKeyControl);
    current = false;
  }
  if (!current) await click(nativePage, groupKeyControl);
  await fastWait(startedAt, { kind: 'checked', selector: groupKeyControl, value: true }, 'Exact Observation ID group key selection');
};
const configureGroup = async () => measure('source-expanded-group-editor-to-key', async startedAt => {
  await click(nativePage, '[data-testid="construction-rows-settings-trigger"]');
  await fastWait(startedAt, { kind: 'enabled', selector: '[data-testid="construction-action-group-rows"]' }, 'Group operation discovery');
  await click(nativePage, '[data-testid="construction-action-group-rows"]');
  await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-reshape-group"]' }, 'Native GROUP editor');
  await chooseOnlyObservationIDGroupKey(startedAt);
});
const verifyGroupedTable = async (expected, name, expectedCountLabel, verifyProtocol = true) => {
  const preview = await fieldPreviewRows();
  const group = document().construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert(group, `${name}: saved GROUP step is missing`);
  const keyOutput = group.outputs.find(column => column.id === group.operation.group.keys[0].outputColumnId);
  const countOutput = group.outputs.find(column => column.id === group.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS')?.outputColumnId);
  assert(keyOutput?.label && countOutput?.label, `${name}: saved GROUP output labels are missing`);
  if (expectedCountLabel !== undefined) assert.equal(countOutput.label, expectedCountLabel, `${name}: saved COUNT_ROWS label differs`);
  const headerLabel = header => header.split(String.fromCharCode(10))[0].trim().toUpperCase();
  const keyIndex = preview.headers.findIndex(header => headerLabel(header) === keyOutput.label.trim().toUpperCase());
  const countIndex = preview.headers.findIndex(header => headerLabel(header) === countOutput.label.trim().toUpperCase());
  assert(keyIndex >= 0 && countIndex >= 0, `${name}: GROUP output columns are missing from table headers ${JSON.stringify(preview.headers)}`);
  const visibleRows = sortedRows(preview.rows.map(row => [String(parseCell(row[keyIndex])), Number(parseCell(row[countIndex]))]));
  assert.deepEqual(visibleRows, expected, `${name}: rendered GROUP rows differ from raw component cardinalities`);
  assert.equal(preview.rows.length, expected.length);
  let protocolRows = visibleRows;
  if (verifyProtocol) {
    const request = report.browserRequests.findLast(entry => entry.path.endsWith('/preview') && entry.status === 200 && protocolResponse(entry));
    const response = protocolResponse(request);
    assert(response?.rows && response?.columns, `${name}: saved preview protocol is missing`);
    const keyColumn = response.columns.find(column => column.label === keyOutput.label)?.column;
    const countColumn = response.columns.find(column => column.label === countOutput.label)?.column;
    assert(keyColumn && countColumn, `${name}: protocol omitted saved GROUP output metadata`);
    protocolRows = sortedRows(response.rows.map(row => [String(row[keyColumn]), Number(row[countColumn])]));
    assert.deepEqual(protocolRows, expected, `${name}: protocol rows differ from raw component cardinalities`);
  }
  return {
    headers: preview.headers, rowCount: preview.rows.length, rows: protocolRows,
    stepId: group.id, keyOutputId: keyOutput.id, countOutputId: countOutput.id, countOutputLabel: countOutput.label,
  };
};
const verifyGroupLabelEditProposal = async ({ request, beforeStep, expectedRows, expectedLabel, name }) => {
  const candidateStep = request.body?.candidateConstruction?.steps?.find(step => step.id === beforeStep.id);
  assert(candidateStep, `${name}: proposal omitted the exact saved GROUP step`);
  const preview = protocolResponse(request)?.preview;
  const summary = assertCountRowsGroupLabelEditProposal({
    beforeStep, editedStep: candidateStep, preview,
    expectedRows, expectedLabel,
  });
  const visible = await fieldPreviewRows();
  assert.equal(visible.rows.length, report.oracle.expectedGroupRows.length, `${name}: visible proposal row count differs from the raw oracle`);
  const output = candidateStep.outputs.find(column => column.id === summary.countOutputId);
  const key = candidateStep.outputs.find(column => column.id === summary.keyOutputId);
  const headerLabel = header => header.split(String.fromCharCode(10))[0].trim();
  const keyIndex = visible.headers.findIndex(header => headerLabel(header) === key.label);
  const countIndex = visible.headers.findIndex(header => headerLabel(header) === expectedLabel);
  assert(keyIndex >= 0, `${name}: visible proposal omitted the exact GROUP key label`);
  assert(countIndex >= 0, `${name}: visible proposal omitted the edited COUNT_ROWS label`);
  const visibleRows = sortedRows(visible.rows.map(row => [String(parseCell(row[keyIndex])), Number(parseCell(row[countIndex]))]));
  assert.deepEqual(visibleRows, expectedRows, `${name}: visible proposal rows differ from the raw oracle`);
  return { ...summary, visibleHeaders: visible.headers, visibleRows, receiptId: preview.receiptId };
};
const applyConstructionProposal = async (expectedRows, name) => measure(name, async startedAt => {
  await click(nativePage, '[data-testid="construction-apply-proposal"]');
  await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' }, rowsReady(expectedRows)] }, 'Applied authored row operation render');
});
const openSavedGroupEditor = async (groupStepId, expectedRows, name) => {
  await openTable(expectedRows, `${name} select saved GROUP output`);
  await measure(name, async startedAt => {
    await click(nativePage, `[data-testid="construction-history-step-${groupStepId}"]`);
    await fastWait(startedAt, { kind: 'enabled', selector: `[data-testid="construction-edit-step-${groupStepId}"]` }, 'Saved GROUP edit control');
    await click(nativePage, `[data-testid="construction-edit-step-${groupStepId}"]`);
    await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-reshape-editor"]' }, 'Saved native GROUP editor');
    const advanced = '[data-testid="construction-reshape-group-advanced"]';
    await fastWait(startedAt, { kind: 'present', selector: advanced }, 'Saved GROUP advanced label section');
    if (!await inspectPage(nativePage, `return document.querySelector(${q(advanced)})?.open === true;`)) {
      await click(nativePage, advanced + ' summary');
    }
    await fastWait(startedAt, { kind: 'open', selector: advanced, value: true }, 'Saved GROUP advanced label section open');
    await fastWait(startedAt, { kind: 'present', selector: 'input[aria-label="Summary output label 1"]' }, 'Saved COUNT_ROWS output label field');
  });
};
const createSelection = async (refs, idempotencyKey) => {
  const selectionsPath = base.replace('/authoring/v2', '/selections');
  const selection = (await api(selectionsPath, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey,
    source: { kind: 'resources', resources: { refs: refs.map(resource => ({
      project: values.project, generation: resource.generation, resourceType: 'Observation', id: resource.id,
    })) } },
  })).body;
  const routes = (await api(base + '/population-routes', {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
  })).body;
  const route = routes.choices.find(choice => choice.route.length === 0);
  assert(route, 'No root Observation population route for selection ' + selection.id);
  return { selection, route };
};
const setPopulation = selectionChoice => command([{
  type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selectionChoice.selection.id,
  routeChoiceId: selectionChoice.route.routeChoiceId,
}]);
const saveDOM = async name => {
  const path = join(values.evidence, name + '.dom.txt');
  await writeFile(path, await domText());
  report.evidencePaths.push(path);
};
const verifyEmptyError = async request => {
  assert(request, 'No native ERROR-policy row proposal was captured');
  assert.equal(request.status, 422, 'Empty-only ERROR must return the observed HTTP 422 validation response: ' + JSON.stringify(request.response));
  assert(expectedEmptyValidationBinding, 'ERROR-policy request has no captured draft binding');
  assert(matchesExpectedEmptyCollectionValidation(request, expectedEmptyValidationBinding),
    'ERROR-policy response did not match the exact owned draft, row choice, output, policy, and EMPTY_COLLECTION_ERROR contract: ' + JSON.stringify(request));
  const alertText = await inspectPage(nativePage, 'return document.querySelector(\'[aria-label="Row definition settings"] [role="alert"]\')?.innerText ?? "";');
  const responseText = JSON.stringify(request.response) + ' ' + alertText;
  const rawResponse = protocolResponse(request);
  const code = rawResponse?.error?.code ?? rawResponse?.code ?? rawResponse?.errorCode ?? rawResponse?.error?.errorCode;
  assert.equal(code, 'EMPTY_COLLECTION_ERROR', 'Empty-only ERROR must return the specific EMPTY_COLLECTION_ERROR diagnostic: ' + responseText);
  assert.equal(rawResponse?.error?.diagnostic?.stage, 'row-definition-proposal', 'Empty-only ERROR must originate from row-definition proposal validation');
  assert.notEqual(code, 'INTERNAL_ERROR', 'Empty-only ERROR returned INTERNAL_ERROR: ' + responseText);
  assert(/empty|no values|at least one|collection/i.test(responseText), 'Validation did not explain the empty collection: ' + responseText);
  const policy = await inspectPage(nativePage,
    'const select=document.querySelector(' + q(policySelect) + ');return {disabled:select?.disabled,options:[...(select?.options??[])].map(option=>({value:option.value,label:option.text,disabled:option.disabled}))};');
  assert.equal(policy.disabled, false, 'Policy repair control must remain enabled after ERROR validation');
  assert(policy.options.some(option => option.value.endsWith(':PRESERVE_PARENT') && !option.disabled), 'PRESERVE_PARENT repair choice must remain enabled');
  assert(policy.options.some(option => option.value.endsWith(':EXCLUDE') && !option.disabled), 'EXCLUDE repair choice must remain enabled');

  const officialMatches = cda.nativeRequests.filter(entry => entry.path === expectedEmptyValidationBinding.path &&
    entry.method === 'POST' && entry.status === request.status &&
    entry.body?.snapshotToken === expectedEmptyValidationBinding.snapshotToken &&
    entry.body?.expectedDraftVersion === expectedEmptyValidationBinding.expectedDraftVersion &&
    entry.body?.expectedDraftDigest === expectedEmptyValidationBinding.expectedDraftDigest &&
    entry.body?.outputId === expectedEmptyValidationBinding.outputId &&
    entry.body?.selection?.kind === 'EXPANDED' &&
    entry.body?.selection?.expanded?.rowChoiceId === expectedEmptyValidationBinding.rowChoiceId &&
    entry.body?.selection?.expanded?.emptyCollectionPolicy === 'ERROR');
  assert.equal(officialMatches.length, 1, 'Expected validation must bind to exactly one official native request capture');
  const officialEntry = officialMatches[0];
  await cda.waitForCapturedResponse(officialRequestCapture, candidate => candidate === officialEntry, 5000);
  assert(matchesExpectedEmptyCollectionValidation(officialEntry, expectedEmptyValidationBinding),
    'Official native capture did not retain the exact EMPTY_COLLECTION_ERROR response body');
  const rawOracle = report.oracle?.selected?.filter(resource => resource.emptyComponentKind)
    .map(({ id, emptyComponentKind }) => ({ id, emptyComponentKind }));
  assert(rawOracle?.length > 0, 'Expected validation classification needs the independent raw empty-component oracle');
  const proof = {
    outputPath: request.path,
    method: request.method,
    status: request.status,
    requestId: request.requestId,
    snapshotToken: expectedEmptyValidationBinding.snapshotToken,
    expectedDraftVersion: expectedEmptyValidationBinding.expectedDraftVersion,
    expectedDraftDigest: expectedEmptyValidationBinding.expectedDraftDigest,
    outputId: expectedEmptyValidationBinding.outputId,
    rowChoiceId: expectedEmptyValidationBinding.rowChoiceId,
    diagnosticCode: code,
    rawOracle,
    policy: 'ERROR',
    reason: 'The UI case deliberately previews the empty-only source selection with ERROR before exercising the enabled PRESERVE_PARENT repair.',
  };
  const classification = cda.expectHttpFailure(officialEntry,
    'The raw CDA oracle predicts empty-only ERROR row-definition validation', proof, { status: officialEntry.status });
  const localHttp = report.errors.filter(error => error.kind === 'http' && error.browserRequestId === request.browserRequestId &&
    error.path === request.path && error.status === request.status);
  assert.equal(localHttp.length, 1, 'Expected validation must match one local native HTTP diagnostic by exact request ID');
  for (const error of localHttp) {
    error.expected = true;
    error.expectedHttpFailure = classification;
  }
  const localConsole = report.errors.filter(error => error.kind === 'console' && error.location === request.origin + request.path &&
    error.message === 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)');
  assert(localConsole.length <= 1, 'Expected validation must not consume ambiguous console diagnostics');
  if (localConsole.length === 1 && matchesExpectedEmptyCollectionValidationConsole(
    localConsole[0], request, report.browserRequests, expectedEmptyValidationBinding)) {
    localConsole[0].expected = true;
    localConsole[0].expectedHttpFailure = classification;
  }
  const browserHttp = report.browserErrors.http.filter(error => error.browserRequestId === request.browserRequestId && error.status === request.status);
  assert.equal(browserHttp.length, 1, 'Expected validation must match one browser HTTP diagnostic by exact request ID');
  for (const error of browserHttp) {
    error.expected = true;
    error.expectedHttpFailure = classification;
  }

  report.expectedEmptyError = {
    status: request.status, code, response: request.response, message: alertText, repairOptions: policy.options,
    requestBinding: expectedEmptyValidationBinding, officialRequestId: officialEntry.browserRequestId,
    classification,
  };
  recordAssertion('ERROR policy gives understandable empty-only validation and keeps repair choices enabled', report.expectedEmptyError);
};
const finish = async () => {
  await officialRequestCapture.flush();
  report.finished = new Date().toISOString();
  if (report.status !== 'invalidated') {
    const blockingGaps = report.gaps.filter(gap => gap.blocking !== false);
    report.status = report.failures.length ? 'failed' : blockingGaps.length ? 'unverified' : report.assertions.length ? 'passed' : 'untested';
  }
  if (report.status === 'unverified') {
    report.skipReason = report.fixtureUnavailable?.reason ?? report.gaps
      .filter(gap => gap.blocking !== false).map(gap => `${gap.assertion}: ${gap.reason}`).join('; ');
  }
  for (const action of [report.activeAction, report.lastAction]) {
    if (action && typeof action === 'object') delete action.targetLocator;
  }
  cda.report[missingGroupOnly ? 'standaloneCdaMissingComponentGroup' : 'standaloneCdaRows'] = report;
  await cda.attachReport(missingGroupOnly ? 'standalone-cda-missing-component-group.json' : 'standalone-cda-repeated-empty.json', report);
  for (const assertion of report.assertions) cda.check('correctness', assertion.name, assertion.status === 'passed', assertion.evidence ?? {});
  if (report.status === 'failed' || report.status === 'invalidated') {
    throw new Error(`${missingGroupOnly ? 'Missing-component GROUP' : 'Repeated-empty'} workflow ${report.status}: ${JSON.stringify(report.failures ?? report.invalidations)}`);
  }
};

const main = async () => {
  await mkdir(values.evidence, { recursive: true });
  const sourceFreezeStartedAt = new Date().toISOString();
  report.sourceFreeze = { root: sourceFreezeRoot, startedAt: sourceFreezeStartedAt, available: false, invalidatesRun: false, productFailure: false };
  report.sourceFingerprint = { root: sourceFreezeRoot, startedAt: sourceFreezeStartedAt, checked: false, invalidatesRun: false, productFailure: false };
  try {
    report.sourceFingerprint.before = sourceFingerprint(sourceFreezeRoot);
    report.sourceFingerprint.checked = true;
    sourceFreeze = await captureSourceFreeze(sourceFreezeRoot);
  } catch (error) {
    report.sourceFingerprint = { ...report.sourceFingerprint, checked: Boolean(report.sourceFingerprint.before), error: String(error) };
    error.initialSourceFreezeFailure = true;
    error.invalidatesRun = true;
    throw error;
  }
  report.sourceFreeze = { ...report.sourceFreeze, available: true, watchedFileCount: sourceFreeze.watchedFileCount };
  const apiBuildStartedAt = new Date().toISOString();
  report.apiBuildFreeze = { target: apiBuildTarget, container: apiContainer, startedAt: apiBuildStartedAt, invalidatesRun: false, productFailure: false };
  frozenApiBuild = await captureApiBuildFreeze(readApiBuildStamp);
  report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: frozenApiBuild.initial };
  const selected = boundedRawOracle();
  const positive = selected.find(resource => resource.componentValues);
  const emptyResources = selected.filter(resource => resource.emptyComponentKind);
  if (missingGroupOnly && report.oracle?.status !== 'ready') {
    report.gaps.push({ assertion: 'bounded missing-component GROUP source oracle', status: 'unverified',
      reason: report.oracle?.reason ?? 'The bounded project/generation scan did not produce the exact missing-component witnesses.' });
    return;
  }
  if (!positive || emptyResources.length === 0) {
    const reason = 'The bounded 1000-resource scan needs one Observation with 2–3 distinct non-empty component values and at least one zero-component Observation.';
    report.gaps.push({ assertion: 'bounded positive and zero-component Observation oracle', status: 'unverified', reason });
    report.fixtureUnavailable = fixtureUnavailableOutcome(reason, report.oracle);
    return;
  }
  assert(selected.length <= 4, 'Raw oracle selected more than four Observation roots');
  assert(report.oracle.expectedComponentRows.length + emptyResources.length <= 6, 'PRESERVE_PARENT expansion exceeds six expected rows');
  assert(selected.every(resource => resource.generation === values.generation && resource.resourceType === 'Observation'));
  if (!missingGroupOnly && !emptyResources.some(resource => resource.emptyComponentKind === 'empty-array')) {
    report.gaps.push({ assertion: 'literal component: [] source shape', status: 'unverified',
      blocking: false,
      reason: 'Only missing/null/non-array component witnesses were selected; this run does not prove a literal empty-array source.' });
  }
  recordAssertion(missingGroupOnly
    ? 'bounded raw oracle selects one two-item and three missing-component Observations'
    : 'bounded raw CDA oracle selected positive and zero-component Observations', {
    roots: selected.length, positiveItems: report.oracle.expectedComponentRows.length,
    emptyWitnesses: emptyResources.map(resource => ({ id: resource.id, kind: resource.emptyComponentKind })),
    expectedPreservedRows: report.oracle.expectedComponentRows.length + emptyResources.length,
  });

  await api(root, { name: explorer, title: missingGroupOnly ? 'CDA missing-component GROUP lifecycle QA' : 'CDA expanded component group composition QA' });
  builder = (await api(base + '/builder')).body;
  assert.equal(builder.catalog.generation, values.generation, 'Fresh QA Explorer must use the requested CDA generation');
  const observationNode = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
  assert(observationNode, 'CDA catalog has no Observation root');
  await command([{ type: 'CREATE_TABLE', title: missingGroupOnly ? 'Missing component group lifecycle' : 'Expanded component group composition', rootNodeId: observationNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'id');
  const valueCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'component[].valueString');
  assert(idCandidate, 'Observation ID field is unavailable');
  assert(valueCandidate, 'Observation.component[].valueString field is unavailable');
  await command([
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' },
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidate.candidateId, projectionMode: 'ALL', initialPresentation: 'TABLE', title: 'Component Value String' },
  ]);
  const originalPopulation = await createSelection(selected, explorer + '-original');
  await setPopulation(originalPopulation);
  report.baselineDocument = structuredClone(document());
  const choices = (await api(base + '/row-definition-choices?' + new URLSearchParams({
    snapshotToken: builder.catalog.snapshotToken, outputId,
  }))).body;
  const choice = choices.choices.find(item => item.kind === 'EXPANDED' && item.fieldPath === 'component[]' && item.occurrenceId === 'base');
  assert(choice, 'Fresh QA table has no root Observation.component[] expansion choice');
  for (const policy of ['PRESERVE_PARENT', 'EXCLUDE', 'ERROR']) {
    assert(choice.policies.find(item => item.name === 'emptyCollectionPolicy')?.options.includes(policy), 'component[] choice omits ' + policy);
  }
  report.choice = { choiceId: choice.choiceId, fieldPath: choice.fieldPath, occurrenceId: choice.occurrenceId };

  monitorBrowser();
  const rootsCount = selected.length;
  const preserveCount = report.oracle.expectedComponentRows.length + emptyResources.length;
  const excludeCount = report.oracle.expectedComponentRows.length;
  if (missingGroupOnly) {
    assert.equal(rootsCount, 4, 'Missing-component GROUP source selection must contain one two-item owner and three missing owners');
    assert.equal(preserveCount, report.oracle.expectedExpandedRowCount, 'Missing-component GROUP expansion must contain exactly five rows');
    assert.equal(report.oracle.expectedGroupRows.length, 4, 'COUNT_ROWS oracle must contain exactly four Observation IDs');
  }
  await openTable(rootsCount, 'fresh-owned-explorer-load');
  await saveDOM('source-record-table');
  const initial = await fieldPreviewRows();
  const initialIndexes = columnIndices(initial);
  assert.deepEqual(initial.rows.map(row => String(parseCell(row[initialIndexes.idIndex]))).sort(), report.oracle.expectedRecordIDs.slice().sort(),
    'Initial RECORDS table must contain the exact selected source resources');
  recordAssertion(missingGroupOnly
    ? 'fresh owned Explorer and selection preserve the exact scoped Observation IDs'
    : 'seeded table starts with exact selected source records and both field bindings',
  { headers: initial.headers, rowCount: initial.rows.length, ids: report.oracle.expectedRecordIDs });

  await openRowSettings();
  await selectAndPreview('PRESERVE_PARENT', preserveCount);
  const beforeCancel = (await api(base + '/builder')).body;
  await cancelRowDefinition(rootsCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(builder.draftDigest, beforeCancel.draftDigest, 'PRESERVE_PARENT Cancel must not mutate the saved draft');
  assert.equal(document().rows.kind, 'RECORDS');
  recordAssertion('native PRESERVE_PARENT preview Cancel restores source-record rows', { rowCount: rootsCount, rowKind: document().rows.kind });

  await openRowSettings();
  await selectAndPreview('PRESERVE_PARENT', preserveCount);
  await applyRowDefinition(preserveCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.kind, 'EXPANDED');
  assert.equal(document().rows.expanded.scopePath, 'component[]');
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'PRESERVE_PARENT');
  report.preserveParentApplied = await verifyPreview('PRESERVE_PARENT', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  if (missingGroupOnly) {
    recordAssertion('PRESERVE_PARENT expansion returns five exact item and missing-owner rows', report.preserveParentApplied);
  }
  await saveDOM('preserve-parent-applied');
  await openTable(preserveCount, 'reload-preserve-parent');
  report.preserveParentReload = await verifyPreview('PRESERVE_PARENT', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  recordAssertion(missingGroupOnly
    ? 'Apply and reload preserve all five raw-oracle EXPANDED rows'
    : 'PRESERVE_PARENT preserves positive item values and one empty row per owner across reload', report.preserveParentReload);

  const expandedSourceState = {
    rows: structuredClone(document().rows), population: structuredClone(document().population),
    columns: structuredClone(document().columns), construction: structuredClone(document().construction ?? null),
  };
  const expectedGroupedRows = expectedGroupRows();
  report.expectedGroupedRows = expectedGroupedRows.map(([id, count]) => ({ id, count }));
  const cancelGroupInput = await resolveGroupInputColumn();
  const firstGroupStarted = Date.now();
  await configureGroup();
  const firstGroupProposal = await constructionProposalFor('source-expanded Group COUNT_ROWS Cancel preview', firstGroupStarted);
  report.groupCancelPreview = assertGroupProposal(firstGroupProposal, expectedGroupedRows, cancelGroupInput);
  recordAssertion(missingGroupOnly
    ? 'COUNT_ROWS Group proposal returns four exact Observation ID cardinalities'
    : 'automatic Group COUNT_ROWS proposal matches raw expanded-item cardinalities', report.groupCancelPreview);
  const beforeGroupCancel = structuredClone((await api(base + '/builder')).body.workspace);
  await measure('source-expanded Group preview Cancel', async startedAt => {
    await click(nativePage, '[data-testid="construction-cancel-proposal"]');
    await fastWait(startedAt, { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' }, 'Canceled authored GROUP preview');
  });
  builder = (await api(base + '/builder')).body;
  assert.deepEqual(builder.workspace, beforeGroupCancel, 'Canceling Group preview must not mutate saved authoring state');
  assert.deepEqual(document().rows, expandedSourceState.rows, 'Group Cancel changed the source EXPANDED definition');
  assert.deepEqual(document().population, expandedSourceState.population, 'Group Cancel changed exact selected source membership');
  assertSourceColumnBindings(document().columns, expandedSourceState.columns, cancelGroupInput, 'Group Cancel', false);
  assert.deepEqual(document().construction ?? null, expandedSourceState.construction, 'Group Cancel persisted an authored operation');
  await openTable(preserveCount, 'reload-expanded-source-after-group-cancel');
  report.groupCancelRestoration = await verifyPreview('PRESERVE_PARENT', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  recordAssertion('Cancel restores exact source EXPANDED component rows before Apply', report.groupCancelRestoration);

  const applyGroupInput = await resolveGroupInputColumn();
  const finalGroupStarted = Date.now();
  await configureGroup();
  const finalGroupProposal = await constructionProposalFor('source-expanded Group COUNT_ROWS Apply preview', finalGroupStarted);
  report.groupApplyPreview = assertGroupProposal(finalGroupProposal, expectedGroupedRows, applyGroupInput);
  const groupStepId = report.groupApplyPreview.step.id;
  await applyConstructionProposal(expectedGroupedRows.length, 'source-expanded Group Apply');
  builder = (await api(base + '/builder')).body;
  assert.deepEqual(document().rows, expandedSourceState.rows, 'Applying GROUP changed source EXPANDED selection or empty policy');
  assert.deepEqual(document().population, expandedSourceState.population, 'Applying GROUP changed exact selected source membership');
  assertSourceColumnBindings(document().columns, expandedSourceState.columns, applyGroupInput, 'Applying GROUP', true);
  const groupAppliedSourceColumns = structuredClone(document().columns);
  const savedGroup = document().construction?.steps?.find(step => step.id === groupStepId && step.operation?.kind === 'GROUP');
  assert(savedGroup, 'Apply did not persist the authored GROUP operation');
  assert.equal(savedGroup.operation.group.aggregates.filter(item => item.operation === 'COUNT_ROWS').length, 1);
  recordAssertion('authored GROUP is composed after source EXPANDED without changing source bindings', {
    sourceRows: document().rows, population: document().population, groupStepId,
    operation: savedGroup.operation, expectedRows: report.expectedGroupedRows,
  });
  if (missingGroupOnly) {
    report.groupApplySavedRows = await verifyGroupedTable(expectedGroupedRows, 'source-expanded GROUP after Apply before reload');
    recordAssertion('applied GROUP renders the exact four-row table before reload', report.groupApplySavedRows);
  }
  await openTable(expectedGroupedRows.length, 'reload-source-expanded-group');
  builder = (await api(base + '/builder')).body;
  assertSourceColumnBindings(document().columns, groupAppliedSourceColumns, applyGroupInput, 'GROUP reload', true);
  report.groupApplyReload = await verifyGroupedTable(expectedGroupedRows, 'source-expanded GROUP reload');
  recordAssertion(missingGroupOnly
    ? 'applied Group survives reload with exact four-row counts'
    : 'reloaded GROUP output matches exact raw Observation component counts', report.groupApplyReload);

  let groupEditIdentity;
  let groupEditCountLabel;
  let groupEditBaselineStep;
  if (missingGroupOnly) {
    groupEditBaselineStep = structuredClone(document().construction?.steps?.find(step => step.id === groupStepId));
    assert(groupEditBaselineStep, 'Reloaded saved GROUP step must be available for edit');
    const baselineCountOutput = groupEditBaselineStep.outputs.find(column =>
      column.id === groupEditBaselineStep.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS')?.outputColumnId);
    assert(baselineCountOutput?.label, 'Saved COUNT_ROWS output must expose its original label');
    const originalCountLabel = baselineCountOutput.label;
    groupEditCountLabel = 'Expanded component owners';
    assert.notEqual(groupEditCountLabel, originalCountLabel, 'GROUP label edit must change the saved label');
    const labelSelector = 'input[aria-label="Summary output label 1"]';
    const proposeLabelEdit = async (name, recordCheck) => {
      await openSavedGroupEditor(groupStepId, expectedGroupedRows.length, `${name} open saved GROUP editor`);
      const edited = await measure(name, async startedAt => {
        await fill(nativePage, labelSelector, groupEditCountLabel);
        const request = await constructionProposalFor(name, startedAt);
        const evidence = await verifyGroupLabelEditProposal({
          request, beforeStep: groupEditBaselineStep, expectedRows: expectedGroupedRows,
          expectedLabel: groupEditCountLabel, name,
        });
        return { request, evidence };
      });
      groupEditIdentity = {
        stepId: edited.evidence.stepId,
        keyOutputId: edited.evidence.keyOutputId,
        countOutputId: edited.evidence.countOutputId,
        countOutputLabel: edited.evidence.countOutputLabel,
      };
      if (recordCheck) recordAssertion(
        'saved GROUP output-label edit proposal preserves exact four COUNT_ROWS rows and stable step/output IDs',
        { ...edited.evidence, requestId: edited.request.requestId, status: edited.request.status },
      );
      return edited;
    };

    const beforeCancelWorkspace = structuredClone(builder.workspace);
    await proposeLabelEdit('Preview saved GROUP output-label edit before Cancel', true);
    await measure('Cancel saved GROUP output-label edit and restore the original table', async startedAt => {
      await click(nativePage, '[data-testid="construction-cancel-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        { kind: 'hidden', selector: '[data-testid="construction-reshape-editor"]' },
        rowsReady(expectedGroupedRows.length),
      ] }, 'Saved GROUP label-edit Cancel restoration');
      report.groupLabelEditCancel = await verifyGroupedTable(expectedGroupedRows,
        'GROUP after output-label edit Cancel', originalCountLabel, false);
    });
    builder = (await api(base + '/builder')).body;
    assert.deepEqual(builder.workspace, beforeCancelWorkspace, 'Canceling saved GROUP label edit changed the saved workspace');
    assert.deepEqual(document().construction?.steps?.find(step => step.id === groupStepId), groupEditBaselineStep,
      'Canceling saved GROUP label edit changed the original GROUP step');
    recordAssertion('Canceling saved GROUP output-label edit preserves the original saved label and exact four-row table', {
      originalLabel: originalCountLabel, rows: report.groupLabelEditCancel.rows,
      stepId: report.groupLabelEditCancel.stepId, keyOutputId: report.groupLabelEditCancel.keyOutputId,
      countOutputId: report.groupLabelEditCancel.countOutputId, countOutputLabel: report.groupLabelEditCancel.countOutputLabel,
    });

    const beforeApplyBuilder = structuredClone(builder);
    await proposeLabelEdit('Preview saved GROUP output-label edit before Apply', false);
    await applyConstructionProposal(expectedGroupedRows.length, 'Apply saved GROUP output-label edit');
    builder = (await api(base + '/builder')).body;
    const appliedGroupStep = document().construction?.steps?.find(step => step.id === groupStepId);
    groupEditIdentity = assertCountRowsGroupLabelEditIdentity(groupEditBaselineStep, appliedGroupStep, groupEditCountLabel);
    assert.deepEqual(document().rows, expandedSourceState.rows, 'Saved GROUP output-label edit changed the source EXPANDED definition');
    assert.deepEqual(document().population, expandedSourceState.population, 'Saved GROUP output-label edit changed exact source membership');
    assertSourceColumnBindings(document().columns, groupAppliedSourceColumns, applyGroupInput, 'GROUP output-label edit', true);
    assert.equal(document().construction.steps.find(step => step.id === groupStepId).outputs.length,
      groupEditBaselineStep.outputs.length, 'Saved GROUP output-label edit changed the output count');
    const draftAdvance = assertBuilderDraftAdvanced(beforeApplyBuilder, builder);
    report.groupLabelEditApplied = await verifyGroupedTable(expectedGroupedRows,
      'GROUP after output-label edit Apply before reload', groupEditCountLabel);
    assert.equal(report.groupLabelEditApplied.stepId, groupEditIdentity.stepId);
    assert.equal(report.groupLabelEditApplied.keyOutputId, groupEditIdentity.keyOutputId);
    assert.equal(report.groupLabelEditApplied.countOutputId, groupEditIdentity.countOutputId);

    await openTable(expectedGroupedRows.length, 'reload saved GROUP after output-label edit Apply');
    builder = (await api(base + '/builder')).body;
    const reloadedGroupStep = document().construction?.steps?.find(step => step.id === groupStepId);
    const reloadedEditIdentity = assertCountRowsGroupLabelEditIdentity(groupEditBaselineStep, reloadedGroupStep, groupEditCountLabel);
    assert.deepEqual(reloadedEditIdentity, groupEditIdentity, 'Reloaded saved GROUP edit changed stable step/output identity');
    assert.deepEqual(document().rows, expandedSourceState.rows, 'Reloaded saved GROUP output-label edit changed source EXPANDED definition');
    assert.deepEqual(document().population, expandedSourceState.population, 'Reloaded saved GROUP output-label edit changed exact source membership');
    assertSourceColumnBindings(document().columns, groupAppliedSourceColumns, applyGroupInput, 'GROUP output-label edit reload', true);
    report.groupLabelEditApplyReload = await verifyGroupedTable(expectedGroupedRows,
      'GROUP after output-label edit reload', groupEditCountLabel);
    assert.equal(report.groupLabelEditApplyReload.stepId, groupEditIdentity.stepId);
    assert.equal(report.groupLabelEditApplyReload.keyOutputId, groupEditIdentity.keyOutputId);
    assert.equal(report.groupLabelEditApplyReload.countOutputId, groupEditIdentity.countOutputId);
    recordAssertion('Applying saved GROUP output-label edit preserves stable step/output IDs and exact rows after reload', {
      ...reloadedEditIdentity, rows: report.groupLabelEditApplyReload.rows,
      headers: report.groupLabelEditApplyReload.headers,
      sourcePopulation: document().population, sourceColumns: document().columns, draftAdvance,
    });
  }

  await measure('open saved GROUP removal control', async startedAt => {
    await click(nativePage, '[data-testid="construction-rows-settings-trigger"]');
    await fastWait(startedAt, { kind: 'present', selector: `[data-testid="construction-row-remove-${groupStepId}"]` }, 'Saved GROUP removal control');
  });
  const groupWorkspaceBeforeRemoval = missingGroupOnly ? structuredClone(builder.workspace) : undefined;
  const groupPreviewBeforeRemoval = missingGroupOnly ? structuredClone(report.groupLabelEditApplyReload) : undefined;
  const removeStarted = Date.now();
  await click(nativePage, '[data-testid="construction-row-remove-' + groupStepId + '"]');
  let removeProposal = await constructionProposalFor('remove source-expanded GROUP', removeStarted);
  assert.deepEqual(removeProposal.body?.candidateConstruction?.steps ?? [], [], 'Removing the only GROUP must restore the source projection construction');
  report.groupRemovalPreview = assertExpandedProposal(removeProposal, preserveCount);
  if (missingGroupOnly) {
    const canceledPreview = await measure('Cancel GROUP removal and render saved four-row preview', async startedAt => {
      await click(nativePage, '[data-testid="construction-cancel-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(expectedGroupedRows.length),
      ] }, 'GROUP removal Cancel restoration');
      return verifyGroupedTable(expectedGroupedRows, 'GROUP preview after removal Cancel');
    });
    builder = (await api(base + '/builder')).body;
    report.groupRemovalCancel = assertGroupRemovalCancelRestoration({
      beforeWorkspace: groupWorkspaceBeforeRemoval,
      afterWorkspace: builder.workspace,
      beforePreview: groupPreviewBeforeRemoval,
      afterPreview: canceledPreview,
      expectedRows: expectedGroupedRows,
    });
    recordAssertion('Canceling GROUP removal leaves the saved GROUP workspace and exact four-row preview unchanged', report.groupRemovalCancel);

    await measure('reopen saved GROUP removal control after Cancel', async startedAt => {
      await click(nativePage, '[data-testid="construction-rows-settings-trigger"]');
      await fastWait(startedAt, { kind: 'present', selector: `[data-testid="construction-row-remove-${groupStepId}"]` }, 'Saved GROUP removal control after Cancel');
    });
    const retryRemoveStarted = Date.now();
    await click(nativePage, '[data-testid="construction-row-remove-' + groupStepId + '"]');
    removeProposal = await constructionProposalFor('reopened source-expanded GROUP removal', retryRemoveStarted);
    assert.deepEqual(removeProposal.body?.candidateConstruction?.steps ?? [], [], 'Reopened GROUP removal must restore the source projection construction');
    report.groupRemovalPreviewAfterCancel = assertExpandedProposal(removeProposal, preserveCount);
  }
  await applyConstructionProposal(preserveCount, 'remove source-expanded GROUP Apply');
  builder = (await api(base + '/builder')).body;
  assert.deepEqual(document().rows, expandedSourceState.rows, 'Removing GROUP changed source EXPANDED definition');
  assert.deepEqual(document().population, expandedSourceState.population, 'Removing GROUP changed selected source membership');
  assertSourceColumnBindings(document().columns, groupAppliedSourceColumns, applyGroupInput, 'Removing GROUP', true);
  assert.equal(document().construction?.steps?.length ?? 0, 0, 'Removing GROUP did not remove the authored operation');
  await openTable(preserveCount, 'reload-expanded-source-after-group-removal');
  builder = (await api(base + '/builder')).body;
  assertSourceColumnBindings(document().columns, groupAppliedSourceColumns, applyGroupInput, 'GROUP removal reload', true);
  report.groupRemovalReload = await verifyPreview('PRESERVE_PARENT', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  recordAssertion(missingGroupOnly
    ? 'applying GROUP removal restores the exact five-row source EXPANDED table after reload'
    : 'removing GROUP restores exact raw component[] rows and preserved empty owners after reload', report.groupRemovalReload);

  if (missingGroupOnly) {
    await Promise.all([...browserPending]);
    await cda.flushHttpDiagnostics({ timeoutMs: 5000 });
    cda.includeBrowserDiagnostics();
    const fixtureDiagnostics = assertNoUnexpectedCdaDiagnostics(cda.report);
    report.browserErrors = {
      exceptions: cda.diagnostics.pageErrors,
      console: cda.diagnostics.console,
      modules: cda.diagnostics.networkFailures.filter(entry => entry.resourceType === 'script'),
      http: cda.diagnostics.httpFailures,
      incidental: cda.diagnostics.assetFailures,
      network: cda.diagnostics.networkFailures,
    };
    assert.deepEqual(report.browserErrors.exceptions, [], 'Browser raised JavaScript exceptions');
    assert.deepEqual(report.browserErrors.console, [], 'Browser logged console errors');
    assert.deepEqual(report.browserErrors.modules, [], 'Browser failed to load a module');
    assert.deepEqual(report.browserErrors.http.filter(error => error.expected !== true), [], 'Browser received unexpected 4xx/5xx responses');
    assert.deepEqual(report.errors.filter(error => error.expected !== true), [], 'Playwright request capture reported an owned API, runtime, or console failure');
    const maximumCheckpointMs = Math.max(...report.timings.map(item => item.durationMs));
    assert(report.timings.length > 0 && report.timings.every(item => item.durationMs <= 5000), 'A native action checkpoint exceeded five seconds');
    recordAssertion('all native action-to-render checkpoints complete within five seconds', {
      checkpointCount: report.timings.length, maximumCheckpointMs, timings: report.timings,
    });
    recordAssertion('no unexpected browser, module, console, or HTTP errors', {
      workflow: report.browserErrors, fixture: fixtureDiagnostics,
    });
    return;
  }

  await openRowSettings();
  await selectAndPreview('EXCLUDE', excludeCount);
  await applyRowDefinition(excludeCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'EXCLUDE');
  report.excludeApplied = await verifyPreview('EXCLUDE', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  await saveDOM('exclude-applied');
  await openTable(excludeCount, 'reload-exclude');
  report.excludeReload = await verifyPreview('EXCLUDE', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  recordAssertion('EXCLUDE omits zero-component owners and retains exact positive item values across reload', report.excludeReload);

  const emptyPopulation = await createSelection(emptyResources, explorer + '-empty-only');
  await setPopulation(emptyPopulation);
  await openTable(0, 'reload-empty-only-exclude');
  await openRowSettings();
  const errorProposal = await selectAndPreview('ERROR', undefined, true);
  await verifyEmptyError(errorProposal);
  await measure('repair-policy-after-error', async startedAt => {
    await selectOption(nativePage, policySelect, 'expanded:' + report.choice.choiceId + ':PRESERVE_PARENT');
    await fastWait(startedAt, { kind: 'text-includes', selector: '[aria-label="Row definition preview"]', text: '→ ' + emptyResources.length + ' rows' }, 'Actionable empty-collection policy repair');
    await Promise.all([...browserPending]);
  });
  const repair = rowProposalRequest({ kind: 'EXPANDED', expanded: { rowChoiceId: report.choice.choiceId, emptyCollectionPolicy: 'PRESERVE_PARENT' } });
  assert.equal(repair?.status, 200, 'Changing from ERROR to PRESERVE_PARENT must produce a valid preview');
  await cancelRowDefinition(0);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'EXCLUDE', 'Cancel after repair preview must retain saved EXCLUDE');
  recordAssertion('empty-only ERROR leaves an actionable repair policy and Cancel retains saved EXCLUDE', {
    error: report.expectedEmptyError, repairStatus: repair.status,
  });

  await setPopulation(originalPopulation);
  await openTable(excludeCount, 'restore-original-selection');
  report.restoredExclude = await verifyPreview('EXCLUDE', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  await openRowSettings();
  await selectRecordsPreview(rootsCount);
  await applyRowDefinition(rootsCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.kind, 'RECORDS');
  assert.deepEqual(document().population, report.baselineDocument.population, 'Final restore must preserve the exact original selection and route');
  assertSourceColumnBindings(document().columns, report.baselineDocument.columns, applyGroupInput, 'Final RECORDS restore', true);
  await saveDOM('source-records-restored');
  await openTable(rootsCount, 'reload-final-source-records');
  const finalPreview = await fieldPreviewRows();
  const finalIndexes = columnIndices(finalPreview);
  assert.deepEqual(finalPreview.rows.map(row => String(parseCell(row[finalIndexes.idIndex]))).sort(), report.oracle.expectedRecordIDs.slice().sort(),
    'Final RECORDS reload must restore exact selected Observation IDs');
  builder = (await api(base + '/builder')).body;
  for (const key of ['output', 'rootResourceType', 'route', 'population']) {
    assert.deepEqual(document()[key], report.baselineDocument[key], 'Final restoration changed original ' + key);
  }
  assertSourceColumnBindings(document().columns, report.baselineDocument.columns, applyGroupInput, 'Final RECORDS reload', true);
  report.restoration = { rowKind: document().rows.kind, population: document().population, ids: report.oracle.expectedRecordIDs };
  recordAssertion('edit and reload restore RECORDS with exact selection and source field bindings', report.restoration);

  await Promise.all([...browserPending]);
  assert.deepEqual(report.browserErrors.exceptions, [], 'Browser raised JavaScript exceptions');
  assert.deepEqual(report.browserErrors.console, [], 'Browser logged console errors');
  assert.deepEqual(report.browserErrors.modules, [], 'Browser failed to load a module');
  assert.deepEqual(report.browserErrors.http.filter(error => error.expected !== true), [], 'Browser received unexpected 4xx/5xx responses');
  assert.deepEqual(report.errors.filter(error => error.expected !== true), [], 'Playwright request capture reported an owned API, runtime, or console failure');
  const timingSummary = summarizeCdaActionToRenderTimings(report.timings);
  const actionDurations = (cda.report.actions ?? []).map(action => action.elapsedMs);
  const actionDurationsValid = actionDurations.length > 0 && actionDurations.every(Number.isFinite);
  const maxActionMs = actionDurationsValid ? Math.max(...actionDurations) : null;
  const timingEvidence = {
    measuredTransitionCount: timingSummary.checkpointCount,
    actionCount: actionDurations.length,
    maxActionMs,
    maximumActionToRenderMs: timingSummary.maximumDurationMs,
    checkpointBudgetMs: CDA_ACTION_TO_RENDER_BUDGET_MS,
    timingCheckpoints: timingSummary.checkpoints,
  };
  cda.check('performance', 'all native action-to-render checkpoints complete within five seconds',
    timingSummary.withinBudget && actionDurationsValid && maxActionMs <= CDA_ACTION_TO_RENDER_BUDGET_MS, timingEvidence);
  report.performance = timingEvidence;
  recordAssertion('native row-policy lifecycle had no browser, module, console, or unexpected HTTP errors', report.browserErrors);
};

try {
  await main();
} catch (error) {
  const sourceFreezeInvalidated = Boolean(error.initialSourceFreezeFailure);
  const apiBuildInvalidated = error instanceof ApiBuildFreezeError;
  if (sourceFreezeInvalidated || apiBuildInvalidated || error.invalidatesRun) {
    report.priorStatus = report.status ?? 'not-started';
    report.status = 'invalidated';
    report.productFailure = false;
    report.invalidations = [{ kind: apiBuildInvalidated ? 'apiBuildFreeze' : 'sourceFreeze', reason: error.reason ?? error.message ?? String(error) }];
    report.error = String(error.stack ?? error);
    if (apiBuildInvalidated) {
      report.apiBuildFreeze = {
        ...report.apiBuildFreeze, initial: error.before,
        ...(error.after?.checked ? { after: error.after } : {}),
        unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason,
      };
    } else if (sourceFreezeInvalidated) {
      report.sourceFreeze = {
        ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [],
        invalidatesRun: true, productFailure: false, error: String(error),
      };
    }
    report.status = 'invalidated';
  } else {
  report.failures.push({ error: String(error.stack ?? error), phase: report.assertions.length });
  }
  try { if (nativePage) report.failureDOM = await domText(); } catch { /* Browser may not have opened. */ }
  try { if (outputId) report.failureBuilder = (await api(base + '/builder')).body; } catch (readError) { report.builderReadError = String(readError); }
} finally {
  try { await Promise.all([...browserPending]); } catch { /* Network response reads remain in the report. */ }
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    const after = sourceFingerprint(sourceFreezeRoot);
    const before = report.sourceFingerprint?.before;
    assert(before, 'Initial source fingerprint was not captured');
    const unchanged = before.sha256 === after.sha256 && before.files === after.files;
    report.sourceFingerprint = {
      ...report.sourceFingerprint, after, unchanged, invalidatesRun: !unchanged,
      productFailure: false, finishedAt: sourceFreezeFinishedAt,
    };
    assert(unchanged, 'Watched source fingerprint changed during the run');
  } catch (error) {
    report.priorStatus = report.status ?? (report.failures.length ? 'failed' : report.gaps.length ? 'unverified' : report.assertions.length ? 'passed' : 'not-started');
    if (report.error) report.priorError = report.error;
    report.status = 'invalidated';
    report.productFailure = false;
    report.invalidations = [...(report.invalidations ?? []), { kind: 'sourceFingerprint', reason: String(error) }];
    report.sourceFingerprint = {
      ...report.sourceFingerprint, unchanged: false, invalidatesRun: true,
      productFailure: false, error: String(error), finishedAt: sourceFreezeFinishedAt,
    };
    report.status = 'invalidated';
  }
  if (sourceFreeze) {
    try {
      report.sourceFreeze = { ...report.sourceFreeze, ...(await sourceFreeze.assertUnchanged()), finishedAt: sourceFreezeFinishedAt };
    } catch (error) {
      report.priorStatus = report.status ?? (report.failures.length ? 'failed' : report.gaps.length ? 'unverified' : report.assertions.length ? 'passed' : 'not-started');
      if (report.error) report.priorError = report.error;
      report.status = 'invalidated';
      report.productFailure = false;
      report.invalidations = [...(report.invalidations ?? []), { kind: 'sourceFreeze', reason: error.message }];
      report.sourceFreeze = {
        ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [],
        invalidatesRun: true, productFailure: false, error: String(error), finishedAt: sourceFreezeFinishedAt,
      };
      report.status = 'invalidated';
    }
  } else if (!report.sourceFreeze?.available) {
    report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, invalidatesRun: true, productFailure: false, finishedAt: sourceFreezeFinishedAt };
  }
  const apiBuildFinishedAt = new Date().toISOString();
  if (frozenApiBuild) {
    try {
      report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()), finishedAt: apiBuildFinishedAt };
    } catch (error) {
      report.priorStatus = report.status ?? (report.failures.length ? 'failed' : report.gaps.length ? 'unverified' : report.assertions.length ? 'passed' : 'not-started');
      if (report.error) report.priorError = report.error;
      report.status = 'invalidated';
      report.productFailure = false;
      report.invalidations = [...(report.invalidations ?? []), { kind: 'apiBuildFreeze', reason: error.reason ?? String(error) }];
      report.apiBuildFreeze = {
        ...report.apiBuildFreeze, ...(error.before ? { initial: error.before } : {}),
        ...(error.after ? { after: error.after } : {}), unchanged: false,
        invalidatesRun: true, productFailure: false, reason: error.reason ?? String(error), finishedAt: apiBuildFinishedAt,
      };
      report.status = 'invalidated';
    }
  } else if (!report.apiBuildFreeze?.initial) {
    report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true, productFailure: false, finishedAt: apiBuildFinishedAt };
  }
  report.finished = new Date().toISOString();
  if (report.status !== 'invalidated') {
    report.status = report.failures.length ? 'failed' : report.gaps.length ? 'unverified' : report.assertions.length ? 'passed' : 'untested';
  }
  await finish();
}
return report;
}
