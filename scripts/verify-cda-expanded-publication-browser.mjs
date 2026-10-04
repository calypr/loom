import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { launchBrowser, sanitizeBody, sanitizeText } from './lib/playwright-browser.mjs';
import { performAction, requireUnique } from './lib/playwright-actions.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { waitForCondition } from './lib/playwright-observations.mjs';

const { values } = parseArgs({ options: {
  'api-origin': { type: 'string', default: process.env.LOOM_CDA_API_ORIGIN },
  'ui-origin': { type: 'string', default: process.env.LOOM_CDA_UI_ORIGIN },
  'api-container': { type: 'string', default: process.env.LOOM_CDA_API_CONTAINER },
  'compose-project': { type: 'string', default: process.env.LOOM_CDA_COMPOSE_PROJECT },
  project: { type: 'string', default: process.env.LOOM_CDA_PROJECT },
  evidence: { type: 'string', default: `/tmp/loom-cda-expanded-publication-${Date.now()}` },
  'arango-container': { type: 'string', default: process.env.LOOM_ARANGO_CONTAINER },
  'clickhouse-container': { type: 'string', default: process.env.LOOM_CLICKHOUSE_CONTAINER },
} });

const sourceFreezeRoot = process.env.LOOM_SOURCE_FREEZE_ROOT ?? fileURLToPath(new URL('..', import.meta.url));
await assertOwnedCdaTarget({ project: values.project, apiOrigin: values['api-origin'], uiOrigin: values['ui-origin'],
  apiContainer: values['api-container'], composeProject: values['compose-project'], sourceRoot: sourceFreezeRoot,
  arangoContainer: values['arango-container'], clickhouseContainer: values['clickhouse-container'] });

const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `cda-expanded-publication-${Date.now()}-${randomUUID().slice(0, 8)}`;
assert.notEqual(explorer, protectedExplorer);

const apiOrigin = values['api-origin'].replace(/\/$/, '');
const uiOrigin = values['ui-origin'].replace(/\/$/, '');
const apiToken = process.env.LOOM_CDA_API_TOKEN;
const root = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
const explorerPath = `${root}/${encodeURIComponent(explorer)}`;
const base = `${explorerPath}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
const report = {
  errors: [],
  started: new Date().toISOString(),
  invocation: process.argv,
  target: { apiOrigin, uiOrigin, project: values.project, protectedExplorer },
  explorer,
  status: 'running',
  assertions: [],
  gaps: [],
  failures: [],
  timingsMs: {},
  requests: [],
  browserRequests: [],
  browserErrors: { exceptions: [], console: [], modules: [], http: [], network: [], incidental: [] },
  evidencePaths: [],
};
const sourceFreeze = await captureSourceFreeze(sourceFreezeRoot);
const sourceBefore = sourceFingerprint(sourceFreezeRoot);
const frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(values['api-container']));
report.sourceFreeze = { root: sourceFreezeRoot, watchedFileCount: sourceFreeze.watchedFileCount, invalidatesRun: false };
report.sourceFingerprint = { root: sourceFreezeRoot, before: sourceBefore, checked: true, invalidatesRun: false };
report.apiBuildFreeze = { container: values['api-container'], initial: frozenApiBuild.initial, invalidatesRun: false };
const protocol = [];
const protocolById = new Map();
const networkById = new Map();
const responseTasks = [];
let builder;
let datasetGeneration;
let outputId;
let componentChoice;
let oracleRows = [];
let browser;
let requestMonitor;
const inspectPage = (page, body) => page.evaluate(`(()=>{${body}})()`);
const waitForBrowser = (page, condition, timeout = 30000) => waitForCondition(page, condition, timeout);
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
  const locator = await resolveActionLocator(page, selector, identity);
  return performAction(report, `Click ${selector} ${identity.name ?? identity.includes ?? ''}`.trim(), locator,
    (target, options) => target.click(options), { timeout });
};
const fill = async (page, selector, value, timeout = 5000) => {
  const locator = await resolveActionLocator(page, selector);
  return performAction(report, `Fill ${selector}`, locator,
    (target, options) => target.fill(value, options), { timeout, editable: true });
};
const selectOption = async (page, selector, value, timeout = 5000) => {
  const locator = await resolveActionLocator(page, selector);
  return performAction(report, `Select ${value} in ${selector}`, locator,
    (target, options) => target.selectOption(value, options), { timeout });
};
const navigate = (page, url) => page.goto(url, { waitUntil: 'load', timeout: 30000 });
let browserPending = new Set();
let fatal;

const recordAssertion = (name, evidence) => report.assertions.push({ name, status: 'passed', evidence });

const api = async (path, body) => {
  const requestId = `cda-expanded-publication-${randomUUID()}`;
  const startedAt = Date.now();
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': requestId };
  if (apiToken) headers.Authorization = `Bearer ${apiToken}`;
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let responseBody;
  try { responseBody = text ? JSON.parse(text) : undefined; } catch { responseBody = text; }
  report.requests.push({ path, requestId, status: response.status, durationMs: Date.now() - startedAt });
  assert(response.ok, `${response.status} ${path}: ${JSON.stringify(responseBody)}`);
  return responseBody;
};

const rawOracle = (query) => {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango oracle returned no JSON array: ${result.stdout.slice(0, 300)}`);
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  assert(scanned.length <= 1000, 'Raw Observation scan exceeded the 1000-record bound');

  const candidates = scanned.flatMap(resource => {
    const components = resource.payload?.component;
    if (!Array.isArray(components) || components.length < 2) return [];
    const componentValues = components.map((component, ordinal) => ({ ordinal, value: component?.valueString }));
    if (!componentValues.every(item => typeof item.value === 'string' && item.value.trim().length > 0)) return [];
    if (new Set(componentValues.map(item => item.value)).size < 2) return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: resource.payload.resourceType, componentValues }];
  });

  const selected = [];
  let tupleCount = 0;
  for (const candidate of candidates) {
    if (selected.length >= 3) break;
    if (tupleCount + candidate.componentValues.length > 6) continue;
    selected.push(candidate);
    tupleCount += candidate.componentValues.length;
  }
  report.oracle = {
    source: 'ArangoDB raw Observation payloads',
    project: values.project,
    generation: datasetGeneration,
    scanned: scanned.length,
    selected: selected.map(({ id, generation, resourceType, componentValues }) => ({ id, generation, resourceType, componentValues })),
    expectedRows: selected.flatMap(resource => resource.componentValues.map(({ ordinal, value }) => ({ id: resource.id, ordinal, value }))),
  };
  return selected;
};

const identity = (value = builder) => ({
  snapshotToken: value.catalog.snapshotToken,
  expectedDraftVersion: value.draftVersion,
  expectedDraftDigest: value.draftDigest,
});

const readBuilder = () => api(`${base}/builder`);

const command = async (commands) => {
  await api(`${base}/commands`, {
    ...identity(), commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    commands,
  });
  builder = await readBuilder();
};

const document = () => builder.workspace.documents.find(doc => doc.output.id === outputId);
const previewTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const rowsReady = count => ({ kind: 'rows', selector: previewTableSelector, count });
const domText = () => inspectPage(browser.page, 'return document.body.innerText;');
const scalarCell = cell => {
  let value = cell?.raw;
  if (value) {
    try { value = JSON.parse(value); } catch { /* Keep the literal preview title. */ }
  } else value = cell?.text;
  if (Array.isArray(value)) {
    assert.equal(value.length, 1, 'Each expanded row must retain exactly its own component value');
    value = value[0];
  }
  assert.equal(typeof value, 'string', 'Expanded values must be scalar strings');
  return value;
};
const sortedPairs = pairs => pairs.map(pair => [String(pair[0]), String(pair[1])])
  .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
const expectedPairs = () => report.oracle.expectedRows.map(({ id, value }) => [id, value]);

const fieldPreviewRows = async () => inspectPage(browser.page, `const proposalRow=document.querySelector('[data-testid="construction-proposal-preview-row"]');const root=proposalRow?.closest('table')??document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');if(!root)return null;const proposal=Boolean(proposalRow);const headers=[...root.querySelectorAll(proposal?'thead th':'[role="columnheader"]')].map(cell=>cell.innerText.trim());const rows=[...root.querySelectorAll(proposal?'[data-testid="construction-proposal-preview-row"]':'[role="row"]')].slice(proposal?0:1).map(row=>[...row.querySelectorAll(proposal?'td':'[role="cell"]')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))).filter(row=>row.length);return {headers,rows,rowCount:root.getAttribute('aria-rowcount')};`);

const verifyRenderedPairs = async (label) => {
  const preview = await fieldPreviewRows();
  assert(preview, `${label} omitted the rendered table`);
  const idIndex = preview.headers.findIndex(header => header.split(String.fromCharCode(10))[0].trim().toUpperCase() === 'OBSERVATION ID');
  const valueIndex = preview.headers.findIndex(header => /component.*value.?string/i.test(header));
  assert(idIndex >= 0 && valueIndex >= 0, `${label} is missing ID/value columns: ${JSON.stringify(preview.headers)}`);
  assert.equal(preview.rows.length, report.oracle.expectedRows.length, `${label} row count differs from raw component tuples`);
  const actual = sortedPairs(preview.rows.map(row => [scalarCell(row[idIndex]), scalarCell(row[valueIndex])]));
  assert.deepEqual(actual, sortedPairs(expectedPairs()), `${label} tuples differ from the exact raw Observation/component items`);
  return { headers: preview.headers, rowCount: preview.rows.length, pairs: actual };
};

const renderedPairsCondition = () => ({ kind: 'viewer-pairs', selector: previewTableSelector, pairs: expectedPairs() });

const recordBrowserError = (event) => {
  if (event.type === 'Script') report.browserErrors.modules.push({ error: event.errorText, url: networkById.get(event.requestId)?.url });
  else report.browserErrors.network.push({ type: event.type, error: event.errorText, url: networkById.get(event.requestId)?.url });
};

const monitorBrowser = () => {
  requestMonitor = captureCDARequests(browser.page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: `${explorerPath}`,
    responsePaths: /publish|commands|selections|explicit-groups|row-definition-proposals|construction-choice-proposals|construction-proposals|construction-capabilities|row-lineage|population-mapping|preview/,
    report: { nativeRequests: report.browserRequests, errors: report.errors },
    shouldReportRequestFailure: (entry, request) => {
      const index = report.browserRequests.indexOf(entry);
      const replacement = report.browserRequests.slice(index + 1).find(candidate =>
        candidate.path === entry.path && candidate.method === entry.method);
      return request.failure()?.errorText === 'net::ERR_ABORTED'
        && ['/row-definition-proposals', '/preview'].some(path => entry.path.endsWith(path)) && replacement
        ? { expected: true, reason: `A later same-path owned preview request (${replacement.requestId}) superseded this request.` } : true;
    },
  });
  browserPending = requestMonitor.pendingReads;
  browser.page.on('request', request => {
    const captured = requestMonitor.byRequest.get(request);
    if (!captured || !captured.path.includes('/authoring/v2/')) return;
    networkById.set(request, { path: captured.path, url: `${captured.origin}${captured.path}` });
    protocolById.set(request, captured);
    protocol.push(captured);
  });
  browser.page.on('response', response => {
    const url = new URL(response.url());
    if (url.origin !== new URL(uiOrigin).origin || response.status() < 400) return;
    const failure = { url: sanitizeText(response.url()), status: response.status(), resourceType: response.request().resourceType() };
    if (url.pathname.endsWith('/favicon.ico') && response.status() === 404) report.browserErrors.incidental.push(failure);
    else report.browserErrors.http.push(failure);
  });
  browser.page.on('requestfailed', request => {
    const url = new URL(request.url());
    if (url.origin !== new URL(uiOrigin).origin || request.failure()?.errorText === 'net::ERR_ABORTED') return;
    const failure = { url: sanitizeText(request.url()), type: request.resourceType(), error: sanitizeText(request.failure()?.errorText) };
    if (url.pathname.endsWith('/favicon.ico')) report.browserErrors.incidental.push(failure);
    else if (request.resourceType() === 'script') report.browserErrors.modules.push(failure);
    else report.browserErrors.network.push(failure);
  });
  return monitor;
};

const remaining = startedAt => Math.max(100, 5000 - (Date.now() - startedAt));
const fastWait = async (startedAt, condition, message) => {
  try { await waitForBrowser(browser.page, condition, remaining(startedAt)); }
  catch (error) { throw new Error(`${message} within the five-second action budget: ${String(error)}`); }
};
const measure = async (name, action) => {
  const startedAt = Date.now();
  await action(startedAt);
  const durationMs = Date.now() - startedAt;
  report.timingsMs[name] = durationMs;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  return durationMs;
};

const waitForNativeResponse = async (suffix, priorCount, timeoutMs = 5000) => {
  const existing = protocol.filter(entry => entry.path.endsWith(suffix) && entry.status !== undefined);
  let entry = existing.length > priorCount ? existing.at(-1) : undefined;
  if (!entry) {
    const response = await browser.page.waitForResponse(candidate => {
      const candidateEntry = requestMonitor.byRequest.get(candidate.request());
      return candidateEntry?.path.endsWith(suffix) && candidateEntry.status !== undefined;
    }, { timeout: timeoutMs });
    entry = requestMonitor.byRequest.get(response.request());
  }
  await requestMonitor.flush();
  assert(entry?.response !== undefined, `Timed out waiting for native ${suffix} response body`);
  return entry;
};

const previewEntries = () => protocol.filter(entry => entry.path.endsWith('/preview') && entry.status === 200 && entry.response);
const nativePreview = entry => entry?.response?.preview ?? entry?.response;
const previewColumn = (preview, label) => preview?.columns?.find(column => column.label.toUpperCase() === label.toUpperCase())?.column;
const verifyNativePairs = (entry, label) => {
  const preview = nativePreview(entry);
  assert(preview?.columns && Array.isArray(preview.rows), `${label} native response omitted preview columns/rows`);
  const idColumn = previewColumn(preview, 'Observation ID');
  const valueColumn = previewColumn(preview, 'Component Value String');
  assert(idColumn && valueColumn, `${label} native response omitted a tuple column`);
  const actual = sortedPairs(preview.rows.map(row => {
    const id = row[idColumn];
    let value = row[valueColumn];
    if (Array.isArray(value)) {
      assert.equal(value.length, 1, `${label} must retain exactly one component value per expanded row`);
      value = value[0];
    }
    assert.equal(typeof id, 'string', `${label} Observation ID is not a string`);
    assert.equal(typeof value, 'string', `${label} component value is not a string`);
    return [id, value];
  }));
  assert.equal(actual.length, report.oracle.expectedRows.length, `${label} native response has the wrong row count`);
  assert.deepEqual(actual, sortedPairs(expectedPairs()), `${label} native tuple membership differs from raw CDA`);
  return { preview, idColumn, valueColumn, pairs: actual };
};

const nativeIdentityMapping = (verified, label) => {
  const { preview, idColumn, valueColumn } = verified;
  const identities = preview.rows.map(row => row.__loom_row_id);
  if (!identities.some(value => value !== undefined)) return undefined;
  assert(identities.every(value => typeof value === 'string' && value.length > 0), `${label} exposed incomplete row identities`);
  assert.equal(new Set(identities).size, preview.rows.length, `${label} row identities are not unique`);
  return preview.rows.map(row => JSON.stringify([row[idColumn], Array.isArray(row[valueColumn]) ? row[valueColumn][0] : row[valueColumn], row.__loom_row_id])).sort();
};

const saveDOM = async name => {
  const path = join(values.evidence, `${name}.dom.txt`);
  await writeFile(path, await domText());
  report.evidencePaths.push(path);
};

const openTable = async (expectedCount, name, exactTuples = false) => measure(name, async startedAt => {
  await navigate(browser.page, pageURL);
  await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-workspace"]' }, 'Explorer workspace load');
  await fastWait(startedAt, { kind: 'present', selector: `[data-testid="construction-table-${outputId}"]` }, 'Explorer table discovery');
  await click(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await fastWait(startedAt, exactTuples ? renderedPairsCondition() : rowsReady(expectedCount), 'CDA table render');
  if (exactTuples) await verifyRenderedPairs(name);
});

const openRowSettings = async () => measure('row-definition-choice-discovery', async startedAt => {
  await click(browser.page, '[data-testid="construction-rows-settings-trigger"]');
  await fastWait(startedAt, { kind: 'enabled', selector: 'select[aria-label="What should each row represent?"]' }, 'Row definition choice discovery');
});

const selectExpandedAndPreview = async () => measure('native-component-row-preview', async startedAt => {
  const priorProposalCount = protocol.filter(entry => entry.path.endsWith('/row-definition-proposals') && entry.status !== undefined).length;
  const shapeSelect = 'select[aria-label="What should each row represent?"]';
  await selectOption(browser.page, shapeSelect, `expanded:${componentChoice.choiceId}`);
  const policySelect = 'select[aria-label="Unmatched record policy"]';
  await fastWait(startedAt, { kind: 'enabled', selector: policySelect }, 'Expansion policy discovery');
  const wanted = `expanded:${componentChoice.choiceId}:PRESERVE_PARENT`;
  const selected = await inspectPage(browser.page, `return document.querySelector(${JSON.stringify(policySelect)})?.value;`);
  if (selected !== wanted) await selectOption(browser.page, policySelect, wanted);
  const expectedCount = report.oracle.expectedRows.length;
  await fastWait(startedAt, { kind: 'any', conditions: [
    { kind: 'all', conditions: [
      { kind: 'text-includes', selector: '[aria-label="Row definition preview"]', text: `→ ${expectedCount} rows` },
      { kind: 'body-text-excludes', text: 'Compiling and comparing row membership' },
    ] },
    { kind: 'present', selector: '[aria-label="Row definition settings"] [role="alert"]' },
  ] }, 'Automatic row definition proposal');
  const proposalError = await inspectPage(browser.page, `return document.querySelector('[aria-label="Row definition settings"] [role="alert"]')?.innerText;`);
  assert(!proposalError, `Row definition proposal failed: ${proposalError}`);
  const proposal = await waitForNativeResponse('/row-definition-proposals', priorProposalCount, remaining(startedAt));
  assert.equal(proposal.status, 200, `Native row proposal failed: ${JSON.stringify(proposal.response)}`);
  assert.equal(proposal.body?.selection?.kind, 'EXPANDED');
  assert.equal(proposal.body?.selection?.expanded?.rowChoiceId, componentChoice.choiceId);
  assert.equal(proposal.body?.selection?.expanded?.emptyCollectionPolicy, 'PRESERVE_PARENT');
  assert.equal(proposal.response?.comparison?.candidate?.rowCount, expectedCount);
  report.rowProposal = proposal;
});

const viewerSnapshot = async () => inspectPage(browser.page, `const normalize=value=>String(value??'').replace(/\\s+/g,' ').trim();const tables=[...document.querySelectorAll('[role="table"],table')];const readTable=table=>{const headerNodes=[...table.querySelectorAll('[role="columnheader"],thead th')];const headers=headerNodes.map(cell=>normalize(cell.innerText||cell.textContent));const rows=[...table.querySelectorAll('[role="row"],tbody tr')].filter(row=>!row.querySelector('[role="columnheader"],th'));const cells=rows.map(row=>[...row.querySelectorAll('[role="cell"],td')].map(cell=>({text:normalize(cell.innerText||cell.textContent),raw:cell.title})));return {headers,cells,ariaRowCount:table.getAttribute('aria-rowcount')};};return {url:location.href,body:document.body.innerText.slice(0,1800),tables:tables.map(readTable)};`);

const verifyViewerPairs = (snapshot, label) => {
  const table = snapshot.tables.find(candidate => candidate.headers.some(header => header.toUpperCase() === 'OBSERVATION ID') && candidate.headers.some(header => /component.*value.?string/i.test(header)));
  assert(table, `${label} has no published ID/value table`);
  const idIndex = table.headers.findIndex(header => header.toUpperCase() === 'OBSERVATION ID');
  const valueIndex = table.headers.findIndex(header => /component.*value.?string/i.test(header));
  const actual = sortedPairs(table.cells.map(row => [scalarCell(row[idIndex]), scalarCell(row[valueIndex])]));
  assert.equal(table.cells.length, report.oracle.expectedRows.length, `${label} row count differs from raw CDA`);
  assert.deepEqual(actual, sortedPairs(expectedPairs()), `${label} tuple membership differs from raw CDA`);
  return { headers: table.headers, rowCount: table.cells.length, pairs: actual, ariaRowCount: table.ariaRowCount };
};

const verifyViewerAndReload = async () => {
  let viewerURL;
  await measure('native-viewer-open-and-data-render', async startedAt => {
    const control = await inspectPage(browser.page, `const button=[...document.querySelectorAll('button')].find(candidate=>candidate.textContent?.trim()==='Viewer');return {visible:Boolean(button&&button.offsetParent!==null),disabled:button?.disabled};`);
    assert(control.visible && !control.disabled, 'Native Viewer control is missing or disabled after publication');
    await click(browser.page, 'button', { name: 'Viewer' }, 1500);
    await fastWait(startedAt, { kind: 'query', values: { mode: 'viewer', project: values.project, explorer } }, 'Native Viewer navigation');
    await fastWait(startedAt, { kind: 'viewer-pairs', pairs: expectedPairs() }, 'Viewer rendered exact published rows');
    viewerURL = await inspectPage(browser.page, 'return location.href;');
    report.viewer = verifyViewerPairs(await viewerSnapshot(), 'Viewer');
  });
  report.viewerURL = viewerURL;

  await measure('native-viewer-reload-and-data-render', async startedAt => {
    await navigate(browser.page, viewerURL);
    await fastWait(startedAt, { kind: 'query', values: { mode: 'viewer' } }, 'Reloaded Viewer navigation');
    await fastWait(startedAt, { kind: 'viewer-pairs', pairs: expectedPairs() }, 'Reloaded Viewer rendered exact published rows');
    report.viewerReload = verifyViewerPairs(await viewerSnapshot(), 'Reloaded Viewer');
  });
};

const readPublishedMaterialization = (publication, previewVerification, nativeMapping) => {
  assert.equal(publication.state, 'ACTIVE', `Native Publish did not activate the revision: ${JSON.stringify(publication)}`);
  assert(publication.revisionId, 'Native Publish omitted its revision ID');
  assert.equal(publication.receiptId, report.preview.receiptId, 'Native Publish did not consume the current automatic preview receipt');
  assert.equal(publication.outputs?.length, 1, 'Native Publish returned an unexpected output set');
  const output = publication.outputs[0];
  assert.equal(output.outputId, outputId, 'Published output identity differs from the authored table');
  assert.equal(output.state, 'READY', 'Published output is not materialized and ready');
  assert.match(output.materializationId, /^[A-Za-z0-9_-]{1,128}$/, 'Materialization identifier is unsafe');
  assert.match(output.outputId, /^[A-Za-z0-9_-]{1,128}$/, 'Output identifier is unsafe');
  const tableName = `loom_bundle_${output.materializationId.replaceAll('-', '')}_${output.outputId}`;
  assert.match(tableName, /^[A-Za-z0-9_]+$/, 'Generated ClickHouse table identifier contains unsafe characters');
  assert.match(values['clickhouse-container'], /^[A-Za-z0-9_.-]+$/, 'ClickHouse container name contains unsafe characters');
  const query = `SELECT * FROM \`loom_dev\`.\`${tableName}\` LIMIT ${report.oracle.expectedRows.length + 1} FORMAT JSONEachRow`;
  const startedAt = Date.now();
  const raw = spawnSync('rtk', [
    'proxy', 'docker', 'exec', values['clickhouse-container'], 'clickhouse-client', '--query', query,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 });
  report.timingsMs.independentClickHouseRead = Date.now() - startedAt;
  assert.equal(raw.status, 0, raw.stderr || raw.stdout);
  const rows = raw.stdout.trim() ? raw.stdout.trim().split('\n').map(line => JSON.parse(line)) : [];
  assert.equal(rows.length, report.oracle.expectedRows.length, `ClickHouse row count differs from raw component tuples (${rows.length})`);
  const { idColumn, valueColumn } = previewVerification;
  const actual = sortedPairs(rows.map(row => {
    const id = row[idColumn];
    let value = row[valueColumn];
    if (Array.isArray(value)) {
      assert.equal(value.length, 1, 'ClickHouse row must retain exactly its own component value');
      value = value[0];
    }
    assert.equal(typeof id, 'string', 'ClickHouse Observation ID is not a string');
    assert.equal(typeof value, 'string', 'ClickHouse component value is not a string');
    return [id, value];
  }));
  assert.deepEqual(actual, sortedPairs(expectedPairs()), 'Independent ClickHouse tuples differ from raw CDA');

  const materializedIds = rows.map(row => row.__loom_row_id);
  if (materializedIds.some(value => value !== undefined)) {
    assert(materializedIds.every(value => typeof value === 'string' && value.length > 0), 'ClickHouse exposes incomplete row identities');
    assert.equal(new Set(materializedIds).size, rows.length, 'ClickHouse row identities are not unique');
    if (nativeMapping) {
      const mapping = rows.map(row => JSON.stringify([row[idColumn], Array.isArray(row[valueColumn]) ? row[valueColumn][0] : row[valueColumn], row.__loom_row_id])).sort();
      assert.deepEqual(mapping, nativeMapping, 'ClickHouse row identities differ from the native preview identities');
    }
  }
  report.materialization = { outputId: output.outputId, materializationId: output.materializationId, table: `loom_dev.${tableName}`, query, rows, exactTuples: true };
  return { output, rows, query };
};

const prepareExplorer = async () => {
  assert(!explorer.includes(protectedExplorer), 'Protected Explorer cannot be targeted');
  await api(root, { name: explorer, title: `CDA expanded publication QA ${explorer}` });
  builder = await readBuilder();
  datasetGeneration = builder.catalog?.generation;
  assert.equal(typeof datasetGeneration, 'string');
  assert(datasetGeneration.length > 0, 'Fresh Builder catalog has no active CDA generation');
  report.target.generation = datasetGeneration;

  const oracleQuery = `FOR r IN Observation FILTER r.project == ${JSON.stringify(values.project)} AND r.dataset_generation == ${JSON.stringify(datasetGeneration)} FILTER IS_ARRAY(r.payload.component) SORT r.id LIMIT 1000 RETURN {id:r.id,generation:r.dataset_generation,payload:r.payload}`;
  const rawStartedAt = Date.now();
  const selected = rawOracle(oracleQuery);
  report.timingsMs.rawArangoOracle = Date.now() - rawStartedAt;
  oracleRows = selected;
  if (selected.length === 0) {
    report.status = 'untested';
    report.gaps.push({ assertion: 'bounded Observation.component[].valueString source tuple', status: 'untested', reason: 'No qualifying raw Observation was found within the 1000-record scan.' });
    return false;
  }
  assert(selected.length <= 3, 'Raw oracle selected more than three Observation roots');
  assert(report.oracle.expectedRows.length <= 6, 'Raw oracle selected more than six component tuples');
  assert(report.oracle.expectedRows.length >= 2, 'Raw oracle must select differing component items');
  assert(selected.every(resource => resource.generation === datasetGeneration && resource.resourceType === 'Observation'));
  assert(selected.every(resource => typeof resource.id === 'string' && resource.id.length > 0));
  assert.equal(new Set(selected.map(resource => resource.id)).size, selected.length, 'Raw oracle selected duplicate Observation IDs');
  assert(report.oracle.expectedRows.every(row => typeof row.value === 'string' && row.value.trim().length > 0));
  recordAssertion('bounded independent raw CDA oracle selected at most three roots and six differing component tuples', {
    scanned: report.oracle.scanned, roots: selected.length, tuples: report.oracle.expectedRows.length,
    selected: report.oracle.selected.map(resource => ({ id: resource.id, values: resource.componentValues.map(item => item.value) })),
  });

  const observationNode = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
  assert(observationNode, 'Fresh Builder catalog has no Observation root');
  const idCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'id');
  const valueCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'component[].valueString');
  assert(idCandidate && valueCandidate, 'Fresh Builder catalog lacks ID or component[].valueString');
  await command([{ type: 'CREATE_TABLE', title: 'Expanded publication tuples', rootNodeId: observationNode.nodeId }]);
  const created = builder.workspace.documents.find(doc => doc.output.title === 'Expanded publication tuples');
  assert(created, 'API seed did not create the run-owned table');
  outputId = created.output.id;
  await command([
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' },
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidate.candidateId, projectionMode: 'ALL', initialPresentation: 'TABLE', title: 'Component Value String' },
  ]);

  const selection = await api(`${explorerPath}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `cda-expanded-publication-${explorer}`,
    source: { kind: 'resources', resources: { refs: selected.map(resource => ({
      project: values.project, generation: resource.generation, resourceType: 'Observation', id: resource.id,
    })) } },
  });
  const routes = await api(`${base}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
  });
  const rootRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(rootRoute, 'Scoped source selection has no direct Observation population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: rootRoute.routeChoiceId }]);
  assert.equal(builder.workspace.documents.length, 1, 'Publication must contain exactly one output');
  assert.deepEqual(builder.workspace.tabs.map(tab => tab.outputId), [outputId], 'Explorer must contain one selected output');
  const choices = await api(`${base}/row-definition-choices?${new URLSearchParams({ snapshotToken: builder.catalog.snapshotToken, outputId })}`);
  componentChoice = choices.choices.find(choice => choice.kind === 'EXPANDED' && choice.fieldPath === 'component[]' && choice.occurrenceId === 'base');
  assert(componentChoice, 'Fresh QA table has no root Observation.component[] row choice');
  assert(componentChoice.policies.find(policy => policy.name === 'emptyCollectionPolicy')?.options.includes('PRESERVE_PARENT'), 'component[] row choice omits PRESERVE_PARENT');
  const saved = document();
  assert.equal(saved.rows.kind, 'RECORDS', 'Seeded table must begin with source-record rows');
  assert.deepEqual(saved.columns.map(column => column.label), ['Observation ID', 'Component Value String']);
  report.seed = { generation: datasetGeneration, selectionRevisionId: selection.id, outputId, sourceIds: selected.map(resource => resource.id), choiceId: componentChoice.choiceId };
  return true;
};

const main = async () => {
  await mkdir(values.evidence, { recursive: true });
  const prepared = await prepareExplorer();
  if (!prepared) return;

  const rootCount = oracleRows.length;
  const expandedCount = report.oracle.expectedRows.length;
  browser = await launchBrowser({ evidence: values.evidence, appOrigins: [values['api-origin'], values['ui-origin']], noAuth: !apiToken && process.env.LOOM_CDA_NO_AUTH === '1' });
  await navigate(browser.page, 'about:blank');
  monitorBrowser();
  if (apiToken) {
    await browser.page.route(url => new URL(url).origin === apiOrigin, route => route.continue({
      headers: { ...route.request().headers(), authorization: `Bearer ${apiToken}` },
    }));
  }

  await openTable(rootCount, 'fresh-explorer-load-to-render');
  await saveDOM('source-record-table');

  await openRowSettings();
  const choiceCount = protocol.filter(entry => entry.path.endsWith('/row-definition-proposals') && entry.status !== undefined).length;
  await selectExpandedAndPreview();
  assert(protocol.filter(entry => entry.path.endsWith('/row-definition-proposals') && entry.status !== undefined).length > choiceCount,
    'Native component row preview did not issue a new row-definition proposal');
  const beforeCancel = await readBuilder();
  const savedDigest = beforeCancel.draftDigest;
  await measure('native-row-definition-cancel', async startedAt => {
    await click(browser.page, '[aria-label="Row definition settings"] button', { name: 'Cancel' });
    await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(rootCount)] }, 'Canceled row-definition preview restoration');
  });
  builder = await readBuilder();
  assert.equal(builder.draftDigest, savedDigest, 'Cancel must leave the saved row definition unchanged');
  assert.equal(document().rows.kind, 'RECORDS');
  recordAssertion('native row-definition Cancel preserves the source-record rows and saved draft', { rowKind: document().rows.kind, rowCount: rootCount, draftDigest: builder.draftDigest });
  await saveDOM('row-definition-canceled');

  await openRowSettings();
  const previousProposalCount = protocol.filter(entry => entry.path.endsWith('/row-definition-proposals') && entry.status !== undefined).length;
  await selectExpandedAndPreview();
  assert(protocol.filter(entry => entry.path.endsWith('/row-definition-proposals') && entry.status !== undefined).length > previousProposalCount,
    'Native component row preview did not issue a second proposal');
  const previousPreviewCount = previewEntries().length;
  let appliedPreviewEntry;
  await measure('native-row-definition-apply-and-render', async startedAt => {
    await click(browser.page, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, renderedPairsCondition(),
    ] }, 'Applied component rows and exact tuples');
    appliedPreviewEntry = await waitForNativeResponse('/preview', previousPreviewCount, remaining(startedAt));
    const verified = verifyNativePairs(appliedPreviewEntry, 'Applied expanded preview');
    report.appliedIdentityMapping = nativeIdentityMapping(verified, 'Applied expanded preview');
  });
  builder = await readBuilder();
  assert.equal(document().rows.kind, 'EXPANDED');
  assert.equal(document().rows.expanded.scopePath, 'component[]');
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'PRESERVE_PARENT');
  const appliedCommand = report.browserRequests.findLast(entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item => item.type === 'APPLY_ROW_DEFINITION_PROPOSAL'));
  assert(appliedCommand, 'Row definition Apply must use the native Builder command');
  const appliedProposal = report.browserRequests.findLast(entry => entry.path.endsWith('/row-definition-proposals') && entry.body?.selection?.expanded?.rowChoiceId === componentChoice.choiceId);
  assert.equal(appliedProposal?.body?.selection?.expanded?.emptyCollectionPolicy, 'PRESERVE_PARENT');
  recordAssertion('native row-definition Apply persists component[] and exact tuple values', {
    rowKind: document().rows.kind, scopePath: document().rows.expanded.scopePath,
    policy: document().rows.expanded.emptyCollectionPolicy, tuples: expandedCount,
  });
  report.appliedPreview = verifyRenderedPairs('Applied expanded preview');
  await saveDOM('component-rows-applied');

  const beforeReloadPreviewCount = previewEntries().length;
  let reloadedPreviewEntry;
  await openTable(expandedCount, 'reload-expanded-component-rows', true);
  reloadedPreviewEntry = await waitForNativeResponse('/preview', beforeReloadPreviewCount, 5000);
  const reloadVerification = verifyNativePairs(reloadedPreviewEntry, 'Reloaded expanded preview');
  report.reloadedIdentityMapping = nativeIdentityMapping(reloadVerification, 'Reloaded expanded preview');
  report.reloadedPreview = verifyRenderedPairs('Reloaded expanded preview');
  if (report.appliedIdentityMapping && report.reloadedIdentityMapping) {
    assert.deepEqual(report.reloadedIdentityMapping, report.appliedIdentityMapping, 'Expanded row identities must remain stable across reload');
    recordAssertion('expanded row identities are unique and stable across Apply/reload', { identities: report.reloadedIdentityMapping.length });
  } else {
    report.gaps.push({ assertion: 'stable expanded row identity', status: 'untested', reason: 'The native preview did not expose __loom_row_id for every expanded row.' });
  }
  const previewPanel = await inspectPage(browser.page, `const panel=document.querySelector('[data-testid="construction-preview"]');return {status:panel?.dataset.previewStatus,outputId:panel?.dataset.previewOutputId,receiptId:panel?.dataset.previewReceiptId,currentDraftDigest:panel?.dataset.currentDraftDigest};`);
  report.preview = { ...nativePreview(reloadedPreviewEntry), ...previewPanel };
  assert.equal(previewPanel.outputId, outputId, 'Automatic preview targets a different output');
  assert.equal(previewPanel.status, 'ready', 'Automatic native preview is not ready');
  assert(report.preview.receiptId, 'Automatic preview has no receipt');
  assert(report.preview.currentDraftDigest, 'Automatic preview has no current draft digest');
  recordAssertion('saved reload renders exact raw component tuples in the native preview', { rowCount: expandedCount, pairs: report.reloadedPreview.pairs });
  await saveDOM('component-rows-reloaded');

  const publishControl = await inspectPage(browser.page, `const button=[...document.querySelectorAll('button')].find(candidate=>candidate.textContent?.trim()==='Publish');return {disabled:button?.disabled,visible:Boolean(button)};`);
  assert(publishControl.visible && !publishControl.disabled, 'Publish is not enabled after the ready expanded preview');
  const priorPublishCount = protocol.filter(entry => entry.path.endsWith('/publish') && entry.status !== undefined).length;
  let publication;
  await measure('native-publish-and-render', async startedAt => {
    await click(browser.page, 'button', { name: 'Publish' }, 1500);
    const publish = await waitForNativeResponse('/publish', priorPublishCount, remaining(startedAt));
    assert.equal(publish.status, 200, `Native Publish request failed: ${JSON.stringify(publish.response)}`);
    publication = publish.response;
    assert(publication && typeof publication === 'object', 'Native Publish response body is missing');
    await fastWait(startedAt, { kind: 'publish-complete' }, 'Native publication completion');
  });
  report.publication = publication;
  const publishedPreview = previewEntries().at(-1);
  const publishedVerification = verifyNativePairs(publishedPreview, 'Published native preview');
  const publishedIdentityMapping = nativeIdentityMapping(publishedVerification, 'Published native preview');
  if (report.reloadedIdentityMapping && publishedIdentityMapping) {
    assert.deepEqual(publishedIdentityMapping, report.reloadedIdentityMapping, 'Published native preview row identities changed');
  }
  const materialized = readPublishedMaterialization(publication, publishedVerification, publishedIdentityMapping ?? report.reloadedIdentityMapping);
  recordAssertion('ClickHouse materialization contains exactly the published raw ID/value tuples', {
    table: report.materialization.table, rows: materialized.rows.length, query: materialized.query,
  });

  await verifyViewerAndReload();
  await Promise.all([...browserPending]);
  assert.equal(browser.dialogErrors.length, 0, `Unexpected browser dialogs: ${JSON.stringify(browser.dialogErrors)}`);
  assert.deepEqual(report.errors, [], 'CDA request capture reported an owned API, runtime, or console failure');
  for (const kind of ['exceptions', 'console', 'modules', 'http', 'network']) {
    assert.deepEqual(report.browserErrors[kind], [], `Unexpected browser ${kind} errors: ${JSON.stringify(report.browserErrors[kind])}`);
  }
  report.dialogs = browser.dialogErrors;
  recordAssertion('native Viewer and reload render exact published tuples with no browser, HTTP, module or console errors', {
    rows: report.viewer.rowCount, errors: report.browserErrors,
  });
  report.status = report.gaps.length ? 'partial' : 'passed';
};

try {
  await main();
} catch (error) {
  fatal = error;
  report.status = 'failed';
  report.failures.push({ error: String(error.stack ?? error), phase: report.assertions.length });
  if (browser) report.failureTrace = await browser.captureFailure(error, {
    phase: report.assertions.length, action: report.activeAction ?? report.lastAction,
    elapsedMs: report.activeAction?.startedAt ? Date.now() - report.activeAction.startedAt : report.lastAction?.elapsedMs,
    requestIdentity: report.browserRequests.at(-1) && (({ requestId, path, method }) => ({ requestId, path, method }))(report.browserRequests.at(-1)),
  }).catch(String);
} finally {
  if (browser) {
    await Promise.all([...browserPending]);
    report.dialogs = browser.dialogErrors;
    await browser.close().catch(error => report.failures.push({ error: `Browser close: ${String(error)}` }));
  }
  try {
    report.sourceFreeze = { ...report.sourceFreeze, ...(await sourceFreeze.assertUnchanged()) };
    const after = sourceFingerprint(sourceFreezeRoot);
    const unchanged = sourceBefore.sha256 === after.sha256 && sourceBefore.files === after.files;
    report.sourceFingerprint = { ...report.sourceFingerprint, after, unchanged, invalidatesRun: !unchanged };
    assert(unchanged, 'Watched source fingerprint changed during the run');
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.invalidations = [...(report.invalidations ?? []), { kind: 'source-freeze', reason: String(error) }];
    process.exitCode = 1;
  }
  try {
    report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()) };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.invalidations = [...(report.invalidations ?? []), { kind: 'api-build-freeze', reason: error.reason ?? String(error) }];
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  report.cleanup = {
    retainedOwnedExplorer: true,
    explorer,
    protectedExplorerTouched: false,
    reason: 'The run-owned Explorer and materialization are retained for evidence; no delete route is used.',
  };
  await mkdir(values.evidence, { recursive: true });
  const evidencePath = join(values.evidence, 'report.json');
  await writeFile(evidencePath, JSON.stringify(report, null, 2));
  report.evidencePaths.push(evidencePath);
  console.log(JSON.stringify({
    status: report.status, evidence: values.evidence, explorer, generation: datasetGeneration,
    outputId, oracleRows: report.oracle?.expectedRows?.length, timingsMs: report.timingsMs,
    failures: report.failures, gaps: report.gaps, protectedExplorerTouched: false,
  }, null, 2));
}

if (fatal || report.status === 'failed') process.exitCode = 1;
