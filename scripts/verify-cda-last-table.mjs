import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';

const project = process.env.LOOM_CDA_PROJECT ?? 'loom_dev_cda_fhir';
const explorer = `last-table-command-qa-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
assert(apiOrigin && uiOrigin, 'Set LOOM_CDA_API_ORIGIN and LOOM_CDA_UI_ORIGIN to the owned CDA stack.');
const evidence = process.argv[2] ?? `/tmp/loom-last-table-command-${Date.now()}`;
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
const report = { status: 'running', explorer, project, requests: [], nativeChecks: [], errors: [] };
await mkdir(evidence, { recursive: true });
let state;
let browser;
const api = async (path, body) => {
  assert(path === root || path.startsWith(`${root}/${explorer}/`));
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': randomUUID() },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, status: response.status, ...(response.ok ? {} : { response: value }) });
  assert(response.ok, `${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};
const command = async commands => {
  const result = await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: state.workspace?.semanticsVersion ?? 10,
    snapshotToken: state.catalog.snapshotToken,
    expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest, commands,
  });
  assert(Array.isArray(result.workspace.documents), 'Command responses must serialize documents as an array, including an empty workspace');
  assert(Array.isArray(result.workspace.tabs), 'Command responses must serialize tabs as an array, including an empty workspace');
  state = await api(`${base}/builder`);
  return result;
};
try {
  await api(root, { name: explorer, title: 'Last table command QA' });
  state = await api(`${base}/builder`);
  assert.equal(state.catalog.generation, 'cda-fhir-v1');
  assert.equal(state.workspace?.documents.length ?? 0, 0);
  const node = state.catalog.nodes.find(candidate => candidate.resourceType === 'Specimen');
  assert(node, 'The CDA generation must expose Specimen');
  await command([{ type: 'CREATE_TABLE', rootNodeId: node.nodeId, title: 'Only table' }]);
  assert.equal(state.workspace.documents.length, 1);
  const before = structuredClone(state.workspace);
  const outputId = before.documents[0].output.id;
  await command([{ type: 'DELETE_TABLE', outputId }]);
  assert.equal(state.workspace.documents.length, 0);
  assert.equal(state.workspace.tabs.length, 0);
  assert(state.previousDraftRevisionId, 'Deletion must retain a revision for Undo');
  const previousRevision = state.previousDraftRevisionId;
  state = await api(`${base}/builder`);
  assert.equal(state.workspace.documents.length, 0, 'Empty workspace must persist after reload');
  await command([{ type: 'RESTORE_DRAFT_REVISION', draftRevisionId: previousRevision }]);
  assert.deepEqual(state.workspace, before, 'Undo must restore the only table exactly');
  state = await api(`${base}/builder`);
  assert.deepEqual(state.workspace, before, 'Undo must persist after reload');
  const field = state.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(field);
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const nativeBaseline = structuredClone(state.workspace);

  browser = await launchBrowser({ evidence, appOrigins: [uiOrigin, apiOrigin], noAuth: true });
  const { page } = browser;
  await page.setViewportSize({ width: 1440, height: 1000 });
  const uiURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  await page.goto(uiURL, { waitUntil: 'domcontentloaded' });
  const table = page.locator(`[data-testid="construction-table-${outputId}"]`);
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await performAction(report, 'open only table', table, target => target.click());
  const rename = page.locator(`[data-testid="construction-rename-table-${outputId}"]`);
  await rename.waitFor({ state: 'visible', timeout: 30000 });
  assert.equal(await rename.isEnabled(), true, 'The selected table rename action must be enabled');
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready', null, { timeout: 30000 });
  const renderedRows = async () => page.locator('[data-testid="preview-table-scroll"] [role="row"]').evaluateAll(rows => rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  const originalRows = await renderedRows();
  assert(originalRows.length > 0, 'The source table must render real data before deletion');

  const started = Date.now();
  await performAction(report, 'delete last table', page.locator('[data-testid="construction-delete-table"]'), target => target.click());
  await page.getByText('Build your first table', { exact: false }).waitFor({ state: 'visible', timeout: 5000 });
  state = await api(`${base}/builder`);
  assert.equal(state.workspace.documents.length, 0);
  assert.equal(state.workspace.tabs.length, 0);
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `Deleting the last table took ${durationMs} ms`);
  report.nativeChecks.push({ name: 'delete-last-table-to-empty-creator', durationMs });

  const restoreStart = Date.now();
  await performAction(report, 'undo last table deletion', page.locator('[data-testid="construction-undo"]'), target => target.click());
  await table.waitFor({ state: 'visible', timeout: 5000 });
  state = await api(`${base}/builder`);
  assert.deepEqual(state.workspace, nativeBaseline);
  await page.waitForFunction(expected => {
    const element = document.querySelector('[data-testid="construction-preview"]');
    return element?.dataset.previewStatus === 'ready'
      && Number(element.dataset.currentDraftVersion) === expected.version
      && element.dataset.currentDraftDigest === expected.digest;
  }, { version: state.draftVersion, digest: state.draftDigest }, { timeout: 5000 });
  const restoredRows = await renderedRows();
  assert(restoredRows.length > 0);
  for (const row of restoredRows) assert(originalRows.some(original => JSON.stringify(original) === JSON.stringify(row)), 'Undo must render the original source values');
  const restoreDurationMs = Date.now() - restoreStart;
  assert(restoreDurationMs <= 5000, `Undo and render took ${restoreDurationMs} ms`);
  report.nativeChecks.push({ name: 'undo-last-table-deletion', durationMs: restoreDurationMs });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready', null, { timeout: 30000 });
  assert.deepEqual((await api(`${base}/builder`)).workspace, nativeBaseline);
  for (const row of await renderedRows()) assert(originalRows.some(original => JSON.stringify(original) === JSON.stringify(row)));
  report.errors = [...browser.diagnostics.pageErrors, ...browser.diagnostics.httpFailures, ...browser.diagnostics.networkFailures];
  assert.deepEqual(report.errors, []);
  report.scope = 'API deletion/revision restore plus native last-table deletion, Undo, and reload';
  report.status = 'passed';
  report.outputId = outputId;
  await page.screenshot({ path: join(evidence, 'passed-reload.png'), fullPage: true });
  await writeFile(join(evidence, 'passed-reload.dom.txt'), await page.locator('body').innerText());
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.diagnostics = browser?.diagnostics;
  await browser?.captureFailure(error, {
    phase: 'last-table-lifecycle',
    action: report.activeAction,
    draftVersion: state?.draftVersion,
    draftDigest: state?.draftDigest,
    explorer,
  });
  process.exitCode = 1;
} finally {
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
process.stdout.write(`${JSON.stringify({ status: report.status, explorer, evidence, error: report.error })}\n`);
