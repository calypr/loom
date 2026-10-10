import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { sanitizeText } from '../helpers/playwright-browser.mjs';
import { requireUnique } from '../helpers/playwright-actions.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { waitForCondition } from '../helpers/playwright-observations.mjs';
import { fixtureUnavailableOutcome } from '../helpers/cda-fixture-outcomes.mjs';

export const nestedRepeatedRawFieldsSummarySelector = '[data-testid="feature-catalog-raw-fields"] > summary';

export async function nestedRepeatedWorkflow({ page, cda }) {
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
const sourceRoot = cda.target.sourceRoot ?? fileURLToPath(new URL('../../..', import.meta.url));

const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `cda-nested-repeated-${Date.now()}`;
assert.notEqual(explorer, protectedExplorer);

const report = {
  errors: [],
  started: new Date().toISOString(),
  target: {
    apiOrigin: values['api-origin'], uiOrigin: values['ui-origin'], project: values.project,
    generation: values.generation, protectedExplorer,
  },
  explorer,
  assertions: [],
  gaps: [
    { assertion: 'nested code/system projection', status: 'unverified', blocking: false, reason: 'This native case verifies the repeated nested row scope and sibling field binding, but does not preview, apply, and reload the nested code/system fields.' },
    { assertion: 'nested-to-outer row-scope edit', status: 'unverified', blocking: false, reason: 'This native case does not edit the nested row scope back to its outer Observation rows and verify restoration.' },
  ],
  failures: [],
  timings: [],
  requests: [],
  browserRequests: [],
  browserErrors: { exceptions: [], console: [], modules: [], http: [], incidental: [] },
  evidencePaths: [],
};
const sourceFreeze = await captureSourceFreeze(sourceRoot);
const sourceBefore = sourceFingerprint(sourceRoot);
const frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(values['api-container']));
report.sourceFreeze = { root: sourceRoot, watchedFileCount: sourceFreeze.watchedFileCount, invalidatesRun: false };
report.sourceFingerprint = { root: sourceRoot, before: sourceBefore, checked: true, invalidatesRun: false };
report.apiBuildFreeze = { container: values['api-container'], initial: frozenApiBuild.initial, invalidatesRun: false };

const root = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
let builder;
let outputId;
const officialRequestCapture = cda.captureRequests(`${root}/${encodeURIComponent(explorer)}`);
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
  report.activeAction = { label, locator: page.locator(selector).toString(), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector, identity);
  report.activeAction.locator = locator.toString();
  const elapsedMs = await cda.action(label, locator, target => target.click({ timeout }), { timeout });
  report.lastAction = { label, locator: locator.toString(), elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const fill = async (page, selector, value, timeout = 5000) => {
  const label = `Fill ${selector}`;
  report.activeAction = { label, locator: page.locator(selector).toString(), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector);
  report.activeAction.locator = locator.toString();
  const elapsedMs = await cda.action(label, locator, target => target.fill(value, { timeout }), { timeout, editable: true });
  report.lastAction = { label, locator: locator.toString(), elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const selectOption = async (page, selector, value, timeout = 5000) => {
  const label = `Select ${value} in ${selector}`;
  report.activeAction = { label, locator: page.locator(selector).toString(), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector);
  report.activeAction.locator = locator.toString();
  const elapsedMs = await cda.action(label, locator, target => target.selectOption(value, { timeout }), { timeout });
  report.lastAction = { label, locator: locator.toString(), elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const navigate = (_page, url) => cda.navigate(url);
let browserPending = new Set();

const recordAssertion = (name, evidence) => report.assertions.push({ name, status: 'passed', evidence });
const api = async (path, body, allowFailure = false) => {
  const requestId = `cda-nested-repeated-${randomUUID()}`;
  const startedAt = Date.now();
  const response = await fetch(values['api-origin'] + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let responseBody;
  try { responseBody = JSON.parse(text); } catch { responseBody = text; }
  report.requests.push({ path, requestId, body, status: response.status, durationMs: Date.now() - startedAt, response: responseBody });
  if (!allowFailure) assert(response.ok, `${response.status} ${path}: ${JSON.stringify(responseBody)}`);
  return { status: response.status, body: responseBody };
};

const identity = (value = builder) => ({
  snapshotToken: value.catalog.snapshotToken,
  expectedDraftVersion: value.draftVersion,
  expectedDraftDigest: value.draftDigest,
});

const command = async (commands) => {
  await api(`${base}/commands`, {
    ...identity(), commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    commands,
  });
  builder = (await api(`${base}/builder`)).body;
};

const document = () => builder.workspace.documents.find(doc => doc.output.id === outputId);
const previewTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const rowsReady = count => ({ kind: 'rows', selector: previewTableSelector, count });
const domText = () => inspectPage(page, 'return document.body.innerText;');

const boundedRawOracle = () => {
  const query = `FOR r IN Observation FILTER r.project == ${JSON.stringify(values.project)} AND r.dataset_generation == ${JSON.stringify(values.generation)} FILTER IS_ARRAY(r.payload.component) SORT r.id LIMIT 1000 RETURN {id:r.id, generation:r.dataset_generation, payload:r.payload}`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango oracle returned no JSON array: ${result.stdout.slice(0, 300)}`);
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  assert(scanned.length <= 1000, 'Raw source scan exceeded the 1000 Observation bound');
  const candidates = scanned.flatMap(resource => {
    const components = resource.payload?.component;
    if (!Array.isArray(components) || components.length < 2 || components.length > 20) return [];
    if (components.length > 4) return [];
    const componentValues = components.flatMap((component, outerOrdinal) => {
      const coding = component?.code?.coding;
      if (!Array.isArray(coding) || coding.length === 0 || coding.length > 3) return [];
      return coding.map((item, innerOrdinal) => ({ ordinal: [outerOrdinal, innerOrdinal], value: component?.valueString, code: item?.code, system: item?.system }));
    });
    if (componentValues.length < 2 || componentValues.length > 8) return [];
    if (!componentValues.every(item => typeof item.value === 'string' && item.value.trim().length > 0 && typeof item.code === 'string' && typeof item.system === 'string')) return [];
    if (new Set(componentValues.map(item => item.value)).size < 2) return [];
    if (new Set(componentValues.map(item => item.ordinal[0])).size !== components.length) return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: resource.payload.resourceType, componentValues }];
  });
  const selected = [];
  let totalComponents = 0;
  for (const candidate of candidates) {
    if (selected.length === 3) break;
    if (totalComponents + candidate.componentValues.length > 8) continue;
    selected.push(candidate);
    totalComponents += candidate.componentValues.length;
  }
  report.oracle = {
    source: 'ArangoDB raw Observation payloads', queryLimit: 1000, scanned: scanned.length,
    selected: selected.map(({ id, generation, resourceType, componentValues }) => ({ id, generation, resourceType, componentValues })),
    expectedRows: selected.flatMap(resource => resource.componentValues.map(({ ordinal, value }) => ({ id: resource.id, ordinal, value }))),
  };
  return selected;
};

const responseEvidence = text => {
  if (text === undefined) return undefined;
  const source = typeof text === 'string' ? text : String(text);
  try { return JSON.parse(source); } catch { return source.slice(0, 32768); }
};
const laterSamePathRequest = entry => {
  const index = report.browserRequests.indexOf(entry);
  return report.browserRequests.slice(index + 1).find(candidate => candidate.path === entry.path && candidate.method === entry.method);
};

const monitorBrowser = () => {
  const monitor = captureCDARequests(page, {
    apiOrigin: values['ui-origin'],
    appOrigins: [values['api-origin'], values['ui-origin']],
    ownedPathPrefix: `${root}/${encodeURIComponent(explorer)}`,
    report: { nativeRequests: report.browserRequests, errors: report.errors },
    shouldReportRequestFailure: (entry, request) => {
      const replacement = laterSamePathRequest(entry);
      return request.failure()?.errorText === 'net::ERR_ABORTED'
        && ['/row-definition-proposals', '/construction-proposals', '/construction-choice-proposals'].some(path => entry.path.endsWith(path))
        && replacement
        ? { expected: true, reason: `A later same-path owned draft request (${replacement.requestId}) superseded this proposal.` } : true;
    },
  });
  browserPending = monitor.pendingReads;
  page.on('response', response => {
    const url = new URL(response.url());
    if (url.origin !== new URL(values['ui-origin']).origin || response.status() < 400) return;
    const incident = { url: sanitizeText(response.url()), status: response.status() };
    if (url.pathname.endsWith('/favicon.ico')) report.browserErrors.incidental.push(incident);
    else report.browserErrors.http.push(incident);
  });
  page.on('requestfailed', request => {
    const url = new URL(request.url());
    if (url.origin !== new URL(values['ui-origin']).origin || request.failure()?.errorText === 'net::ERR_ABORTED') return;
    if (request.resourceType() === 'script') report.browserErrors.modules.push({ url: sanitizeText(request.url()), error: sanitizeText(request.failure()?.errorText) });
  });
  return monitor;
};

const remaining = startedAt => Math.max(100, 5000 - (Date.now() - startedAt));
const fastWait = async (startedAt, condition, message) => {
  try { await waitForBrowser(page, condition, remaining(startedAt)); }
  catch (error) { throw new Error(`${message} within the five-second action budget: ${String(error)}`); }
};
const measure = async (name, action) => {
  const startedAt = Date.now();
  await action(startedAt);
  const durationMs = Date.now() - startedAt;
  report.timings.push({ name, durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  return durationMs;
};

const openTable = async (expectedRows, name) => measure(name, async startedAt => {
  const url = `${values['ui-origin']}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  await navigate(page, url);
  await fastWait(startedAt, { kind: 'present', selector: `[data-testid="construction-table-${outputId}"]` }, 'Explorer table discovery');
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await fastWait(startedAt, rowsReady(expectedRows), 'CDA table render');
});

const openRowSettings = async () => measure('row-definition-choice-discovery', async startedAt => {
  await click(page, '[data-testid="construction-rows-settings-trigger"]');
  await fastWait(startedAt, { kind: 'enabled', selector: 'select[aria-label="What should each row represent?"]' }, 'Row definition choice discovery');
});

const selectAndPreview = async ({ kind, policy, expectedRows }) => measure(`row-definition-${kind.toLowerCase()}-preview`, async startedAt => {
  const shapeSelect = 'select[aria-label="What should each row represent?"]';
  await selectOption(page, shapeSelect, kind === 'RECORDS' ? 'records' : `expanded:${report.choice.choiceId}`);
  if (policy) {
    const policySelect = 'select[aria-label="Unmatched record policy"]';
    await fastWait(startedAt, { kind: 'enabled', selector: policySelect }, 'Expansion policy discovery');
    const selected = await inspectPage(page, `return document.querySelector(${JSON.stringify(policySelect)})?.value;`);
    const wanted = `expanded:${report.choice.choiceId}:${policy}`;
    if (selected !== wanted) await selectOption(page, policySelect, wanted);
  }
  await fastWait(startedAt, { kind: 'any', conditions: [
    { kind: 'all', conditions: [
      { kind: 'text-includes', selector: '[aria-label="Row definition preview"]', text: `→ ${expectedRows} rows` },
      { kind: 'body-text-excludes', text: 'Compiling and comparing row membership' },
    ] },
    { kind: 'present', selector: '[aria-label="Row definition settings"] [role="alert"]' },
  ] }, 'Automatic row definition preview');
  const proposalError = await inspectPage(page, `return document.querySelector('[aria-label="Row definition settings"] [role="alert"]')?.innerText;`);
  assert(!proposalError, `Row-definition preview failed: ${proposalError}`);
  await Promise.all([...browserPending]);
  const request = report.browserRequests.findLast(entry => entry.path.endsWith('/row-definition-proposals') &&
    entry.body?.selection?.kind === kind && (kind !== 'EXPANDED' || entry.body.selection.expanded?.rowChoiceId === report.choice.choiceId));
  assert(request, `Native ${kind} row selection did not send the expected proposal request`);
  assert.equal(request.status, 200, `Row definition proposal response: ${JSON.stringify(request.response)}`);
  if (kind === 'EXPANDED') {
    assert.equal(request.body.selection.expanded.emptyCollectionPolicy, policy);
    assert.equal(request.response?.mode, 'EXPANDED');
    assert.equal(request.response?.comparison?.candidate?.rowCount, expectedRows);
  }
  report.latestRowProposal = request;
});

const rowTable = async () => inspectPage(page, `return {rowCount:document.querySelector(${JSON.stringify(previewTableSelector)})?.getAttribute('aria-rowcount'),text:document.body.innerText.slice(0,12000)};`);
const saveDOM = async name => {
  const path = join(values.evidence, `${name}.dom.txt`);
  await writeFile(path, await domText());
  report.evidencePaths.push(path);
};

const fieldPreviewRows = async () => inspectPage(page, `const proposalRow=document.querySelector('[data-testid="construction-proposal-preview-row"]');const root=proposalRow?.closest('table')??document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');if(!root)return null;const proposal=Boolean(proposalRow);const headers=[...root.querySelectorAll(proposal?'thead th':'[role="columnheader"]')].map(cell=>cell.innerText.trim());const rows=[...root.querySelectorAll(proposal?'[data-testid="construction-proposal-preview-row"]':'[role="row"]')].slice(proposal?0:1).map(row=>[...row.querySelectorAll(proposal?'td':'[role="cell"]')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))).filter(row=>row.length);return {headers,rows,rowCount:root.getAttribute('aria-rowcount')};`);

const verifyPairs = preview => {
  assert(preview, 'Native field proposal omitted its rendered row preview');
  const idIndex = preview.headers.findIndex(header => header.split(String.fromCharCode(10))[0].trim().toUpperCase() === 'OBSERVATION ID');
  const valueIndex = preview.headers.findIndex(header => /component.*value.?string/i.test(header));
  assert(idIndex >= 0, `Native preview is missing Observation ID: ${JSON.stringify(preview.headers)}`);
  assert(valueIndex >= 0, `Native preview is missing component[].valueString: ${JSON.stringify(preview.headers)}`);
  assert.equal(preview.rows.length, report.oracle.expectedRows.length, 'Expanded preview row count must equal bounded raw component count');
  const tuple = (id, value) => `${id}\u0000${value}`;
  const expected = report.oracle.expectedRows.map(row => tuple(row.id, row.value)).sort();
  const cellValue = cell => {
    if (typeof cell === 'string') return cell;
    let value = cell.text;
    if (cell.raw) {
      try { value = JSON.parse(cell.raw); } catch { value = cell.raw; }
    }
    if (Array.isArray(value)) {
      assert.equal(value.length, 1, 'Each expanded item must have only its own component value');
      value = value[0];
    }
    assert.equal(typeof value, 'string', 'Component item values must be scalar strings');
    return value;
  };
  const actual = preview.rows.map(row => tuple(cellValue(row[idIndex]), cellValue(row[valueIndex]))).sort();
  assert.deepEqual(actual, expected, 'Expanded Observation ID/valueString pairs must match their exact raw component items');
  return { headers: preview.headers, rowCount: preview.rows.length, pairs: actual };
};

const addFieldAndPreview = async () => {
  await click(page, '[data-testid="construction-action-add-columns"]');
  await click(page, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-add-columns-source"]' }, 5000);
  await click(page, nestedRepeatedRawFieldsSummarySelector);
  const checkbox = 'input[aria-label="Select Observation.component[].valueString"]';
  await waitForBrowser(page, { kind: 'present', selector: checkbox }, 5000);
  const offered = await inspectPage(page, `const input=document.querySelector(${JSON.stringify(checkbox)});return {disabled:input?.disabled,checked:input?.checked,label:input?.getAttribute('aria-label')};`);
  assert.equal(offered?.disabled, false, 'The sibling component[].valueString field must be natively addable after expansion');

  const propose = async actionName => measure(actionName, async startedAt => {
    const checked = await inspectPage(page, `return document.querySelector(${JSON.stringify(checkbox)})?.checked;`);
    if (!checked) await click(page, checkbox);
    await click(page, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    await fastWait(startedAt, { kind: 'present', selector: '[role="dialog"] input[aria-label="Component Value String: Keep all matching values"]' }, 'Field form choice discovery');
    await click(page, '[role="dialog"] input[aria-label="Component Value String: Keep all matching values"]');
    await click(page, '[role="dialog"] button', { name: 'Add 1 column' });
    await fastWait(startedAt, { kind: 'status-in', selector: '[data-testid="construction-choice-proposal-panel"]', statuses: ['ready', 'error'] }, 'Native component field preview');
    const status = await inspectPage(page, `return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus;`);
    assert.equal(status, 'ready', await inspectPage(page, `return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText;`));
    await Promise.all([...browserPending]);
    const preview = await fieldPreviewRows();
    const pairEvidence = verifyPairs(preview);
    report.fieldProposalPreview = { ...pairEvidence, proposal: report.browserRequests.findLast(entry => /construction.*proposals/.test(entry.path) && entry.status === 200)?.response };
  });

  await propose('native-component-field-preview');
  const beforeCancel = (await api(`${base}/builder`)).body;
  const beforeCancelDigest = beforeCancel.draftDigest;
  await measure('native-component-field-cancel', async startedAt => {
    await click(page, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-choice-proposal-panel"]' }, rowsReady(report.oracle.expectedRows.length),
    ] }, 'Canceled field preview restoration');
  });
  assert.equal((await api(`${base}/builder`)).body.draftDigest, beforeCancelDigest, 'Cancel must leave the saved workspace unchanged');
  await saveDOM('field-preview-canceled');

  await propose('native-component-field-confirmed-preview');
  await measure('native-component-field-apply', async startedAt => {
    await click(page, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-choice-proposal-panel"]' },
      rowsReady(report.oracle.expectedRows.length),
      { kind: 'some-text', selector: '[data-testid="preview-table-scroll"] [role="columnheader"]', text: 'Component Value String' },
    ] }, 'Applied component field preview');
    report.appliedPairs = verifyPairs(await fieldPreviewRows());
  });
  builder = (await api(`${base}/builder`)).body;
  const saved = document();
  const fieldColumn = saved.columns.find(column => JSON.stringify(column).includes('component[].valueString'));
  assert(fieldColumn, 'Applied document must persist the exact component[].valueString source path');
  assert.equal(fieldColumn.occurrenceId, 'base', 'Sibling field must remain bound to the base Observation occurrence');
  assert.equal(fieldColumn.source?.field?.path, 'component[].valueString', 'Sibling field binding must preserve the canonical component scope');
  report.savedField = fieldColumn;
  await saveDOM('component-field-applied');
  await openTable(report.oracle.expectedRows.length, 'reload-added-component-field');
  report.reloadedPairs = verifyPairs(await fieldPreviewRows());
  return fieldColumn;
};

const removeFieldAndRestoreRows = async fieldColumn => {
  const label = fieldColumn.label;
  assert(label, 'Applied sibling field must have a user-visible label');
  await click(page, 'button', { name: 'Columns' });
  await waitForBrowser(page, { kind: 'present', selector: `button[aria-label=${JSON.stringify(`Remove ${label} column`)}]` }, 5000);
  await measure('native-component-field-remove', async startedAt => {
    await click(page, `button[aria-label=${JSON.stringify(`Remove ${label} column`)}]`);
    await fastWait(startedAt, { kind: 'all', conditions: [rowsReady(report.oracle.expectedRows.length),
      { kind: 'no-text', selector: '[data-testid="preview-table-scroll"] [role="columnheader"]', text: 'Component Value String' }] }, 'Removed field row restoration');
  });
  builder = (await api(`${base}/builder`)).body;
  assert(!document().columns.some(column => JSON.stringify(column).includes('component[].valueString')),
    'Remove must delete the applied sibling field from the saved document');
  const afterRemove = await rowTable();
  assert.equal(Number(afterRemove.rowCount) - 1, report.oracle.expectedRows.length, 'Removing the field must preserve expanded component rows');
  await saveDOM('component-field-removed');

  await openTable(report.oracle.expectedRows.length, 'reload-after-field-removal');
  await recordAssertion('native field removal restores prior columns and preserves repeated rows', {
    remainingColumns: document().columns.map(column => column.label), rowCount: report.oracle.expectedRows.length,
  });

  await openRowSettings();
  await selectAndPreview({ kind: 'RECORDS', expectedRows: report.oracle.selected.length });
  await measure('edit-cancel-preserves-expanded-row-definition', async startedAt => {
    await click(page, '[aria-label="Row definition settings"] button', { name: 'Cancel' });
    await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(report.oracle.expectedRows.length)] }, 'Canceled row-definition edit restoration');
  });
  builder = (await api(`${base}/builder`)).body;
  assert.equal(document().rows.kind, 'EXPANDED', 'Canceling an edit must retain the applied component expansion');

  await openRowSettings();
  await selectAndPreview({ kind: 'RECORDS', expectedRows: report.oracle.selected.length });
  await measure('edit-row-definition-to-source-records', async startedAt => {
    await click(page, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
    await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(report.oracle.selected.length)] }, 'Restored source-record rows');
  });
  builder = (await api(`${base}/builder`)).body;
  assert.equal(document().rows.kind, 'RECORDS', 'Applying the edit must restore one row per selected Observation');
  assert.equal(document().columns.length, 1, 'Final restored table must retain only its original Observation ID column');
  await saveDOM('source-record-rows-restored');
  await openTable(report.oracle.selected.length, 'reload-after-row-restoration');
  await recordAssertion('row-definition edit and reload restore one row per selected Observation', {
    rowKind: document().rows.kind, rowCount: report.oracle.selected.length,
  });
};

const verifyStableItemIdentities = () => {
  const expectedPairs = report.oracle.expectedRows.map(({ id, value }) => JSON.stringify([id, value])).sort();
  const snapshots = [];
  for (const entry of report.browserRequests) {
    const response = entry.response;
    const preview = response?.preview ?? response;
    if (!preview?.columns || !preview?.rows) continue;
    const columns = new Map(preview.columns.map(column => [column.label.toUpperCase(), column.column]));
    const valueColumn = columns.get('COMPONENT VALUE STRING');
    const idColumn = columns.get('OBSERVATION ID');
    if (!valueColumn || !idColumn) continue;
    const identities = [];
    const pairs = [];
    const mapping = [];
    for (const row of preview.rows) {
      const value = row[valueColumn];
      assert(Array.isArray(value) && value.length === 1 && typeof value[0] === 'string', 'Each expanded row must retain exactly its own component value');
      assert.equal(typeof row.__loom_row_id, 'string', 'Expanded rows need stable item identities');
      const pair = [row[idColumn], value[0]];
      pairs.push(JSON.stringify(pair));
      identities.push(row.__loom_row_id);
      mapping.push(JSON.stringify([...pair, row.__loom_row_id]));
    }
    assert.deepEqual(pairs.sort(), expectedPairs, 'Native protocol values must match the independently scoped raw item tuples');
    assert.equal(new Set(identities).size, expectedPairs.length, 'Expanded component rows must have distinct identities');
    snapshots.push({ path: entry.path, mapping: mapping.sort() });
  }
  assert(snapshots.length >= 3 && snapshots.some(snapshot => snapshot.path.endsWith('/preview')), 'Need proposal and saved/reloaded native identity evidence');
  for (const snapshot of snapshots) assert.deepEqual(snapshot.mapping, snapshots[0].mapping, 'Component identity must survive proposal, Apply, and reload');
  report.itemIdentityEvidence = { snapshots: snapshots.length, uniqueItems: expectedPairs.length, stable: true };
  recordAssertion('expanded item identities are unique and stable across proposals and saved reload', report.itemIdentityEvidence);
};

const verifySourceRestoration = () => {
  const restored = document();
  for (const key of ['output', 'rootResourceType', 'route', 'population', 'rows', 'columns']) {
    assert.deepEqual(restored[key], report.baselineDocument[key], `Restoration must preserve the original ${key}`);
  }
  const preview = report.browserRequests.findLast(entry => entry.path.endsWith('/preview') && entry.status === 200)?.response;
  assert(preview?.rows, 'Final source-record reload must have a native preview');
  const idColumn = preview.columns.find(column => column.label === 'Observation ID')?.column;
  assert(idColumn, 'Restored source records need their original ID binding');
  assert.deepEqual(preview.rows.map(row => row[idColumn]).sort(), report.oracle.selected.map(resource => resource.id).sort(), 'Source-row restoration must preserve the exact selected CDA members');
  recordAssertion('source restoration preserves original population, route, field bindings and exact CDA members', { members: preview.rows.length });
};

const finish = async () => {
  await officialRequestCapture.flush();
  report.finished = new Date().toISOString();
  if (report.status !== 'invalidated') {
    const blockingGaps = report.gaps.filter(gap => gap.blocking !== false);
    report.status = report.failures.length ? 'failed'
      : blockingGaps.length ? 'unverified'
        : report.assertions.length && report.assertions.every(assertion => assertion.status === 'passed') ? 'passed'
          : 'untested';
    if (report.status === 'unverified') {
      report.skipReason = report.fixtureUnavailable?.reason ?? blockingGaps.map(gap => `${gap.assertion}: ${gap.reason}`).join('; ');
    }
  }
  cda.report.standaloneCdaRows = report;
  await cda.attachReport('standalone-cda-nested-repeated.json', report);
  for (const assertion of report.assertions) cda.check('correctness', assertion.name, assertion.status === 'passed', assertion.evidence ?? {});
  if (report.status === 'failed' || report.status === 'invalidated') {
    throw new Error(`Nested repeated-row workflow ${report.status}: ${JSON.stringify(report.failures ?? report.invalidations)}`);
  }
};

try {
  report.target = cda.target;
  await mkdir(values.evidence, { recursive: true });
  const sourceRecords = boundedRawOracle();
  if (sourceRecords.length === 0) {
    const reason = 'The bounded 1000-record scan found no Observation with at least two distinct non-empty component[].valueString values.';
    report.gaps.push({ assertion: 'bounded Observation.component[] raw oracle', status: 'unverified', reason });
    report.fixtureUnavailable = fixtureUnavailableOutcome(reason, report.oracle);
  } else {
    assert(sourceRecords.every(resource => resource.generation === values.generation && resource.resourceType === 'Observation'));
    recordAssertion('bounded independent raw CDA oracle selected at most three Observations', {
      selected: sourceRecords.length, expectedRows: report.oracle.expectedRows.length,
      differingValues: sourceRecords.map(resource => [...new Set(resource.componentValues.map(item => item.value))]),
    });

    await api(root, { name: explorer, title: 'CDA repeated component row QA' });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(builder.catalog.generation, values.generation, 'Fresh QA Explorer must use the requested CDA generation');
    const observationNode = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
    assert(observationNode, 'CDA catalog has no Observation root');
    await command([{ type: 'CREATE_TABLE', title: 'Repeated component values', rootNodeId: observationNode.nodeId }]);
    const createdDocument = builder.workspace.documents.find(doc => doc.output.title === "Repeated component values");
    assert(createdDocument, "API seed did not create the repeated-component table");
    outputId = createdDocument.output.id;
    const idCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'id');
    assert(idCandidate, 'CDA Observation ID field is unavailable');
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);

    const selectionsPath = base.replace('/authoring/v2', '/selections');
    const selection = (await api(selectionsPath, {
      snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
      source: { kind: 'resources', resources: { refs: sourceRecords.map(resource => ({
        project: values.project, generation: resource.generation, resourceType: 'Observation', id: resource.id,
      })) } },
    })).body;
    const routes = (await api(`${base}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
    })).body;
    const rootRoute = routes.choices.find(choice => choice.route.length === 0);
    assert(rootRoute, 'A root Observation population route is unavailable');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: rootRoute.routeChoiceId }]);
    report.baselineDocument = structuredClone(document());
    const choicesQuery = new URLSearchParams({ snapshotToken: builder.catalog.snapshotToken, outputId });
    const choices = (await api(`${base}/row-definition-choices?${choicesQuery}`)).body;
    const componentChoice = choices.choices.find(choice => choice.kind === 'EXPANDED' && choice.fieldPath === 'component[].code.coding[]' && choice.occurrenceId === 'base');
    assert(componentChoice, 'No root Observation.component[] row choice is available');
    assert(componentChoice.policies.find(policy => policy.name === 'emptyCollectionPolicy')?.options.includes('PRESERVE_PARENT'),
      'component[] row choice does not offer PRESERVE_PARENT');
    report.choice = { ...componentChoice, exactScope: 'component[].code.coding[]', occurrenceId: 'base' };
    recordAssertion('modern row-definition choice binds root Observation.component[].code.coding[]', {
      choiceId: componentChoice.choiceId, fieldPath: componentChoice.fieldPath,
      occurrenceId: componentChoice.occurrenceId, policy: 'PRESERVE_PARENT',
    });

    const initialRowCount = sourceRecords.length;
    const expandedRowCount = report.oracle.expectedRows.length;
    monitorBrowser();
    await openTable(initialRowCount, 'fresh-explorer-load-to-render');
    await saveDOM('initial-source-record-rows');

    await openRowSettings();
    const shapeSelect = 'select[aria-label="What should each row represent?"]';
    const nativeOptions = await inspectPage(page, `return [...document.querySelector(${JSON.stringify(shapeSelect)}).options].map(option=>({value:option.value,label:option.text,disabled:option.disabled}));`);
    const nativeOption = nativeOptions.find(option => option.value === `expanded:${componentChoice.choiceId}` && option.label.startsWith('One row per code in Component'));
    assert(nativeOption, `Modern row selector did not offer the component[] option: ${JSON.stringify(nativeOptions)}`);
    report.nativeComponentOption = nativeOption;

    await selectAndPreview({ kind: 'EXPANDED', policy: 'PRESERVE_PARENT', expectedRows: expandedRowCount });
    const unchangedBeforeCancel = (await api(`${base}/builder`)).body;
    await measure('native-row-definition-cancel', async startedAt => {
      await click(page, '[aria-label="Row definition settings"] button', { name: 'Cancel' });
      await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(initialRowCount)] }, 'Canceled row-definition preview restoration');
    });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(builder.draftDigest, unchangedBeforeCancel.draftDigest, 'Cancel must leave the row definition draft unchanged');
    assert.equal(document().rows.kind, 'RECORDS');
    await saveDOM('row-definition-canceled');
    recordAssertion('native row-definition Cancel preserves source-record rows', { rowCount: initialRowCount, rowKind: document().rows.kind });

    await openRowSettings();
    await selectAndPreview({ kind: 'EXPANDED', policy: 'PRESERVE_PARENT', expectedRows: expandedRowCount });
    await saveDOM('component-expansion-preview');
    await measure('native-row-definition-apply', async startedAt => {
      await click(page, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
      await fastWait(startedAt, { kind: 'all', conditions: [{ kind: 'hidden', selector: '[aria-label="Row definition settings"]' }, rowsReady(expandedRowCount)] }, 'Applied repeated component rows');
    });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'EXPANDED');
    assert.equal(document().rows.expanded.scopePath, 'component[].code.coding[]');
    assert.equal(document().rows.expanded.emptyCollectionPolicy, 'PRESERVE_PARENT');
    report.appliedRowDefinition = document().rows;
    const appliedRowRequest = report.browserRequests.findLast(entry => entry.path.endsWith('/commands') &&
      entry.body?.commands?.some(item => item.type === 'APPLY_ROW_DEFINITION_PROPOSAL'));
    assert(appliedRowRequest, 'Apply row definition must go through the native Builder command');
    const appliedProposal = report.browserRequests.findLast(entry => entry.path.endsWith('/row-definition-proposals') &&
      entry.body?.selection?.kind === 'EXPANDED' && entry.body.selection.expanded?.rowChoiceId === componentChoice.choiceId);
    assert.equal(appliedProposal?.body?.selection?.expanded?.rowChoiceId, componentChoice.choiceId,
      'Native row proposal must carry the canonical component[] rowChoiceId');
    assert.equal(appliedProposal?.body?.selection?.expanded?.emptyCollectionPolicy, 'PRESERVE_PARENT');
    await saveDOM('component-expansion-applied');
    recordAssertion('native row-definition Apply persists exact component[].code.coding[] scope and policy', {
      rowChoiceId: componentChoice.choiceId, scopePath: document().rows.expanded.scopePath,
      policy: document().rows.expanded.emptyCollectionPolicy, comparisonRows: expandedRowCount,
    });

    await openTable(expandedRowCount, 'reload-expanded-component-rows');
    await saveDOM('component-expansion-reloaded');
    recordAssertion('reload renders the applied repeated component rows', { rowCount: expandedRowCount });

    const fieldColumn = await addFieldAndPreview();
    recordAssertion('native sibling field preview matches exact raw Observation/component tuples', report.fieldProposalPreview);
    recordAssertion('native Apply persists canonical component[].valueString at base occurrence', {
      column: fieldColumn, scopePath: document().rows.expanded.scopePath,
    });

    await removeFieldAndRestoreRows(fieldColumn);
    await Promise.all([...browserPending]);
    verifyStableItemIdentities();
    verifySourceRestoration();
    assert.deepEqual(report.browserErrors.exceptions, [], 'Browser raised JavaScript exceptions');
    assert.deepEqual(report.browserErrors.console, [], 'Browser logged console errors');
    assert.deepEqual(report.browserErrors.modules, [], 'Browser failed to load a module');
    assert.deepEqual(report.browserErrors.http, [], 'Browser received unexpected 4xx/5xx responses');
    assert.deepEqual(report.errors, [], 'Playwright request capture reported an owned API, runtime, or console failure');
    recordAssertion('browser lifecycle had no runtime, module, console, or HTTP errors', report.browserErrors);
  }
} catch (error) {
  report.status = 'failed';
  report.failures.push({ error: String(error.stack ?? error), phase: report.assertions.length });
  try { report.failureDOM = await domText(); } catch { /* Browser may not have opened. */ }
  try { report.failureBuilder = (await api(`${base}/builder`)).body; } catch (readError) { report.builderReadError = String(readError); }
} finally {
  try { await Promise.all([...browserPending]); } catch { /* Response reads are also captured above. */ }
  try {
    report.sourceFreeze = { ...report.sourceFreeze, ...(await sourceFreeze.assertUnchanged()) };
    const after = sourceFingerprint(sourceRoot);
    const unchanged = sourceBefore.sha256 === after.sha256 && sourceBefore.files === after.files;
    report.sourceFingerprint = { ...report.sourceFingerprint, after, unchanged, invalidatesRun: !unchanged };
    assert(unchanged, 'Watched source fingerprint changed during the run');
    report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()) };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.invalidations = [...(report.invalidations ?? []), { kind: 'source-build-freeze', reason: String(error) }];
    report.status = 'invalidated';
  }
  await finish();
}
return report;
}
