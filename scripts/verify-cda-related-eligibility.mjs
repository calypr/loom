import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { sanitizeBody } from './lib/playwright-browser.mjs';

export async function relatedEligibilityWorkflow({ page, cda, expect }) {
const project = cda.project;
const explorer = process.env.LOOM_QA_EXPLORER ?? cda.explorer;
const selectionId = process.env.LOOM_QA_SELECTION_ID;
const uiOrigin = cda.uiOrigin;
const apiOrigin = cda.apiOrigin;
const apiContainer = cda.target.apiContainer;
const composeProject = cda.target.composeProject;
const arangoContainer = cda.target.arangoContainer;
const arangoDatabase = process.env.LOOM_CDA_ARANGO_DATABASE ?? 'loom_dev';
const manyId = process.env.LOOM_QA_MANY_PATIENT_ID ?? '02f8e963-73b8-50ea-b840-c4a80719a06a';
const zeroId = process.env.LOOM_QA_ZERO_PATIENT_ID ?? '54b50ad3-aa10-5483-85e2-5382aac7d374';
assert(project && explorer && selectionId && uiOrigin && apiOrigin && apiContainer && composeProject && arangoContainer && arangoDatabase,
  'Set isolated CDA target, QA Explorer/selection, and Arango container/database explicitly');
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder&selection=${encodeURIComponent(selectionId)}`;
const apiBase = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
const tableName = `CDA related eligibility QA ${Date.now()}`;
const evidence = cda.evidence;
const state = Object.assign(cda.report, { project, explorer, selectionId, pageURL, tableName, evidence, transitions: [], apiReads: [], commands: [], rawSource: {} });
let outputId;
const buttons = name => page.getByRole('button', { name, exact: true });
const action = (label, locator, run) => cda.action(label, locator, () => run(locator));
async function builder() {
  const response = await fetch(`${apiBase}/builder`, { signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  state.apiReads.push({ path: '/builder', status: response.status, ...(response.ok ? {} : { body: sanitizeBody(text) }) });
  assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(text)}`);
  return JSON.parse(text);
}
async function command(commands) {
  const before = await builder();
  const payload = { commandId: crypto.randomUUID(), semanticsVersion: 10, snapshotToken: before.catalog.snapshotToken,
    expectedDraftVersion: before.draftVersion, expectedDraftDigest: before.draftDigest, commands };
  const response = await fetch(`${apiBase}/commands`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(payload), signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  assert(response.ok, `Builder command returned ${response.status}: ${sanitizeBody(text)}`);
  state.commands.push({ types: commands.map(item => item.type), status: response.status });
  return JSON.parse(text);
}
async function rawCounts() {
  const query = `FOR p IN Patient FILTER p.project == ${JSON.stringify(project)} AND p.dataset_generation == "cda-fhir-v1" AND p.id IN ${JSON.stringify([manyId, zeroId])} LET n = LENGTH(UNIQUE(FOR e IN fhir_edge FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == "cda-fhir-v1" RETURN e._from)) RETURN {id:p.id,count:n}`;
  const output = execFileSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', arangoDatabase, '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`], { encoding: 'utf8', maxBuffer: 200000 });
  const counts = JSON.parse(output.slice(output.indexOf('[')));
  assert.deepEqual(Object.fromEntries(counts.map(item => [item.id, item.count])), { [manyId]: 38, [zeroId]: 0 }, 'Raw CDA Observation membership differs from the independent fixture oracle');
  return counts;
}
async function visibleRows(expected) {
  const table = page.getByTestId('preview-table-scroll').getByRole('table');
  await table.waitFor({ state: 'visible', timeout: 5000 });
  await page.waitForFunction(() => !document.body.innerText.includes('Loading the preview…'), null, { timeout: 5000 });
  const rows = await table.getByRole('row').evaluateAll(items => items.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  const actualIds = rows.map(row => row[0]).sort();
  assert.deepEqual(actualIds, [...expected].sort(), 'Visible CDA preview rows differ from the independent Observation membership oracle');
  return rows;
}
async function preview(label, expected) {
  const startedAt = Date.now();
  const responsePromise = page.waitForResponse(response => response.url().startsWith(`${apiBase}/preview`) && response.request().method() === 'POST', { timeout: 5000 });
  await action(`preview ${label}`, buttons('Preview'), target => target.click());
  state.activeAction = { label: `render ${label} preview`, locator: '[data-testid="preview-table-scroll"]', startedAt };
  const response = await responsePromise;
  assert(response.ok(), `Preview returned ${response.status()}`);
  const rows = await visibleRows(expected);
  const elapsedMs = Date.now() - startedAt;
  state.transitions.push({ name: label, elapsedMs, limitMs: 5000, passed: elapsedMs < 5000 });
  assert(elapsedMs < 5000, `${label} preview took ${elapsedMs} ms`);
  const headers = await page.getByTestId('preview-table-scroll').getByRole('columnheader').allTextContents();
  state[label] = { headers: headers.map(value => value.trim()), rows };
  state.baseline ??= { headers: headers.map(value => value.trim().split('\n')[0].toUpperCase()) };
  state.activeAction = undefined;
  return rows;
}
async function propose(label, expected, startedAt) {
  state.activeAction = { label: `render ${label} proposal`, locator: '[data-testid="construction-proposal-panel"]', startedAt };
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', null, { timeout: 5000 });
  const panel = page.getByTestId('construction-proposal-preview');
  const rows = await panel.locator('tbody tr').evaluateAll(elements => elements.map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())));
  const headers = await panel.locator('thead th').allTextContents();
  const apply = page.getByTestId('construction-apply-proposal');
  const elapsedMs = Date.now() - startedAt;
  state.transitions.push({ name: label, elapsedMs, limitMs: 5000, passed: elapsedMs < 5000 });
  assert(elapsedMs < 5000, `${label} proposal took ${elapsedMs} ms`);
  assert.equal(await apply.isEnabled(), true, 'Eligibility proposal must be applicable');
  assert.deepEqual(rows.map(row => row[0]).sort(), [...expected].sort(), `${label} proposed rows differ from raw source oracle`);
  assert.deepEqual(headers.map(value => value.split('\n')[0].toUpperCase()), state.baseline.headers, 'Eligibility changed output columns');
  state[label] = { headers, rows };
  state.activeAction = undefined;
}
async function selectTable() {
  assert(outputId, 'Temporary table identity is required before selecting it');
  const table = page.getByTestId(`construction-table-${outputId}`);
  await table.waitFor({ state: 'visible', timeout: 5000 });
  await action('select temporary table', table, target => target.click());
}
async function editSaved() {
  const history = page.locator('[data-testid^="construction-history-step-"]');
  await expect(history, 'saved eligibility step: expected one history control').toHaveCount(1, { timeout: 5000 });
  await action('select saved eligibility step', history, target => target.click());
  const edit = page.locator('[data-testid^="construction-edit-step-"]');
  await expect(edit, 'edit eligibility step: expected one edit control').toHaveCount(1, { timeout: 5000 });
  await action('edit saved eligibility step', edit, target => target.click());
  await page.getByTestId('construction-related-eligibility-editor').waitFor({ state: 'visible', timeout: 5000 });
}
async function setRule(rule) {
  const select = page.getByLabel('Related eligibility rule', { exact: true });
  await action(`choose ${rule} eligibility rule`, select, target => target.selectOption(rule));
}

try {
  state.rawCounts = await rawCounts();
  await page.setViewportSize({ width: 1280, height: 900 });
  await cda.navigate(pageURL);
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
  await action('start a new table', buttons('New table'), target => target.click());
  const nameInput = page.locator('#first-table-name');
  await action('name temporary eligibility table', nameInput, target => target.fill(tableName));
  await action('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows', exact: true }), target => target.click());
  const created = await builder();
  outputId = created.workspace.documents.find(document => document.output.title === tableName)?.output.id;
  assert(outputId, 'Temporary Patient table was not created');
  await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: 5000 });
  await action('open source setup', page.getByTestId('construction-rows-settings-trigger'), target => target.click());
  const starting = page.locator('[aria-label="Starting collection"]');
  await action('attach selected CDA patients', starting.getByRole('button', { name: 'Use selected resources', exact: true }), target => target.click());
  await page.getByLabel('Starting collection settings').getByText('2 Patient resources attached', { exact: false }).waitFor({ state: 'visible', timeout: 5000 });
  await action('return to table', buttons('Back to table'), target => target.click());
  await preview('baseline', [manyId, zeroId]);
  await action('open row filters', page.locator('button[aria-label^="Filter rows:"]'), target => target.click());
  await action('choose related-record filter', buttons('Related records'), target => target.click());
  const recordType = page.getByLabel('Related eligibility record type', { exact: true });
  await action('choose Observation records', recordType, target => target.selectOption('Observation'));
  const editor = page.getByTestId('construction-related-eligibility-editor');
  const routes = editor.locator('input[type="radio"]');
  const routeLabels = await routes.evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), disabled: input.disabled })));
  state.routes = routeLabels;
  const subjectRoute = editor.locator('input[type="radio"][aria-label*="subject"]');
  const existsStartedAt = Date.now();
  await action('choose Subject relationship', subjectRoute, target => target.check());

  await propose('existsProposal', [manyId], existsStartedAt);
  await action('apply Exists eligibility', page.getByTestId('construction-apply-proposal'), target => target.click());
  await page.locator('[data-testid^="construction-history-step-"]').first().waitFor({ state: 'visible', timeout: 5000 });
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 }); await selectTable(); await preview('savedExists', [manyId]);
  await editSaved();
  const absentStartedAt = Date.now(); await setRule('ABSENT'); await propose('absentProposal', [zeroId], absentStartedAt);
  await action('apply Absent eligibility', page.getByTestId('construction-apply-proposal'), target => target.click());
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 }); await selectTable(); await preview('savedAbsent', [zeroId]);
  await editSaved();
  const countStartedAt = Date.now(); await setRule('COUNT_AT_LEAST'); await propose('countProposal', [manyId], countStartedAt);
  await action('apply Count eligibility', page.getByTestId('construction-apply-proposal'), target => target.click());
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 }); await selectTable(); await preview('savedCount', [manyId]);
  await action('select eligibility for removal', page.locator('[data-testid^="construction-history-step-"]'), target => target.click());
  const remove = page.locator('[data-testid^="construction-remove-step-"]');
  await remove.waitFor({ state: 'visible', timeout: 5000 });
  const responsePromise = page.waitForResponse(response => response.url().startsWith(`${apiBase}/construction-proposals`) && response.request().method() === 'POST', { timeout: 5000 });
  const removeStartedAt = Date.now();
  await action('propose eligibility removal', remove, target => target.click());
  await responsePromise;
  await propose('removeProposal', [manyId, zeroId], removeStartedAt);
  await action('apply eligibility removal', page.getByTestId('construction-apply-proposal'), target => target.click());
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 }); await selectTable(); await preview('restored', [manyId, zeroId]);
  state.diagnostics = cda.diagnostics;
  assert.deepEqual(cda.diagnostics.console, [], 'Unexpected browser console errors');
  assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected browser exceptions');
  assert.deepEqual(cda.diagnostics.httpFailures, [], 'Unexpected local HTTP failures');
  assert.deepEqual(cda.diagnostics.networkFailures, [], 'Unexpected local network failures');
  state.status = 'passed';
} catch (error) {
  state.status = 'failed'; state.error = String(error.stack ?? error);
  throw error;
} finally {
  if (outputId) {
    try { await command([{ type: 'DELETE_TABLE', outputId }]); state.cleanup = 'temporary table deleted'; }
    catch (error) { state.cleanupError = String(error.stack ?? error); state.status = 'failed'; throw error; }
  }
  cda.includeBrowserDiagnostics();
}
return state;
}
