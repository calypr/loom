import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const explorer = `last-table-command-qa-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const evidence = process.argv[2] ?? `/tmp/loom-last-table-command-${Date.now()}`;
const root = project ? `/api/v1/projects/${encodeURIComponent(project)}/explorers` : '';
const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
const report = { status: 'running', explorer, project, target: { apiOrigin, uiOrigin, apiContainer, composeProject }, requests: [], nativeChecks: [], errors: [], sourceFingerprint: {} };
await mkdir(evidence, { recursive: true });
let state;
let browser;
let sourceFreeze;
let apiBuildFreeze;
let apiBuildBefore;

function validateOrigin(name, raw, forbiddenPort) {
  assert(raw, `Set ${name} to the owned isolated CDA stack.`);
  const url = new URL(raw);
  assert(['http:', 'https:'].includes(url.protocol), `${name} must use HTTP or HTTPS`);
  assert(!url.username && !url.password, `${name} must not embed credentials`);
  assert(['127.0.0.1', 'localhost', '::1'].includes(url.hostname), `${name} must target a local isolated stack`);
  assert(url.port !== forbiddenPort, `${name} must not use the shared CDA port ${forbiddenPort}`);
  assert(url.pathname === '/' && !url.search && !url.hash, `${name} must be an origin without path, query, or fragment`);
  return url;
}

function mappedLoopbackPort(container, port, expectedPort, service) {
  assert.equal(container.Config.Labels['com.docker.compose.service'], service);
  const bindings = container.NetworkSettings.Ports?.[`${port}/tcp`] ?? [];
  assert(bindings.some(binding => binding.HostIp === '127.0.0.1' && binding.HostPort === expectedPort),
    `${service} must publish ${port}/tcp on 127.0.0.1:${expectedPort}`);
}

function containerRecords(names) {
  if (!names.length) return [];
  return JSON.parse(execFileSync('docker', ['inspect', ...names], { encoding: 'utf8', timeout: 15000 }));
}

async function validateOwnedTarget() {
  assert(project, 'Set LOOM_CDA_PROJECT explicitly to the project that contains the loaded CDA dataset.');
  assert.match(project, /^[A-Za-z0-9_-]+$/, 'LOOM_CDA_PROJECT contains unsupported characters');
  assert(composeProject, 'Set LOOM_CDA_COMPOSE_PROJECT to the isolated Compose project.');
  assert.match(composeProject, /^[A-Za-z0-9_.-]+$/, 'LOOM_CDA_COMPOSE_PROJECT contains unsupported characters');
  assert(apiContainer, 'Set LOOM_CDA_API_CONTAINER to the isolated Loom API container.');
  assert.match(apiContainer, /^[A-Za-z0-9_.-]+$/, 'LOOM_CDA_API_CONTAINER contains unsupported characters');
  assert(!apiContainer.startsWith('loom-dev-6d7df93d6a37'), 'Do not use the shared CDA API container');
  assert.notEqual(composeProject, 'loom-dev-6d7df93d6a37', 'Do not use the shared CDA Compose project');
  const apiURL = validateOrigin('LOOM_CDA_API_ORIGIN', apiOrigin, '8188');
  const uiURL = validateOrigin('LOOM_CDA_UI_ORIGIN', uiOrigin, '30008');
  const names = execFileSync('docker', ['ps', '--filter', `label=com.docker.compose.project=${composeProject}`, '--format', '{{.Names}}'], { encoding: 'utf8', timeout: 10000 }).trim().split('\n').filter(Boolean);
  assert(names.includes(apiContainer), `The named API container is not running in Compose project ${composeProject}`);
  const containers = containerRecords(names);
  const api = containers.find(container => container.Name === `/${apiContainer}`);
  const ui = containers.find(container => container.Config.Labels['com.docker.compose.service'] === 'loom-ui');
  assert(api, 'The named API container is missing from the isolated Compose project');
  assert(ui, 'The isolated Compose project must have a running Loom UI container');
  for (const container of [api, ui]) {
    const labels = container.Config.Labels ?? {};
    assert.equal(labels['com.docker.compose.project'], composeProject, 'Container Compose ownership changed');
    assert.equal(container.State.Running, true, `${container.Name} must already be running`);
  }
  mappedLoopbackPort(api, 8080, apiURL.port, 'loom-api');
  mappedLoopbackPort(ui, 8080, uiURL.port, 'loom-ui');
  const apiSource = api.Mounts?.find(mount => mount.Destination === '/workspace/cmd')?.Source;
  const uiSource = ui.Mounts?.find(mount => mount.Destination === '/workspace/packages/loom-ui/src')?.Source;
  assert.equal(apiSource, `${sourceRoot}/cmd`, 'API container must be mounted from this isolated source checkout');
  assert.equal(uiSource, `${sourceRoot}/ui/packages/loom-ui/src`, 'UI container must be mounted from this isolated source checkout');
  return { composeProject, apiContainer, uiContainer: ui.Name.replace(/^\//, ''), apiPort: apiURL.port, uiPort: uiURL.port, sourceRoot };
}

function extractBuildIdentity(observation) {
  assert.equal(observation.status, 0, 'API build stamp check must succeed');
  const values = observation.stdout.trim().match(/^([a-f0-9]{64})\s+([a-f0-9]{64})\s+([a-f0-9]{64})$/i);
  assert(values, 'API build stamp must contain three SHA-256 identities');
  return values.slice(1).join(':').toLowerCase();
}

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
  report.target.ownership = await validateOwnedTarget();
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze = { watchedFileCount: sourceFreeze.watchedFileCount, before: sourceFingerprint(sourceRoot) };
  let firstBuildObservation;
  apiBuildFreeze = await captureApiBuildFreeze(async () => {
    firstBuildObservation = await checkContainerApiBuildStamp(apiContainer);
    return firstBuildObservation;
  });
  apiBuildBefore = extractBuildIdentity(firstBuildObservation);
  report.apiBuildIdentity = { before: apiBuildBefore };
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
  assert.deepEqual(restoredRows, originalRows, 'Undo must restore every original visible source row in the same order');
  const restoreDurationMs = Date.now() - restoreStart;
  assert(restoreDurationMs <= 5000, `Undo and render took ${restoreDurationMs} ms`);
  report.nativeChecks.push({ name: 'undo-last-table-deletion', durationMs: restoreDurationMs });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready', null, { timeout: 30000 });
  assert.deepEqual((await api(`${base}/builder`)).workspace, nativeBaseline);
  const reloadedRows = await renderedRows();
  assert.deepEqual(reloadedRows, originalRows, 'Reload must retain every original visible source row in the same order');
  report.incidentalAssets = browser.diagnostics.assetFailures;
  report.errors = [...browser.diagnostics.console, ...browser.diagnostics.pageErrors, ...browser.diagnostics.httpFailures, ...browser.diagnostics.networkFailures];
  assert.deepEqual(report.errors, []);
  report.scope = 'API deletion/revision restore plus native last-table deletion, Undo, and reload';
  report.status = 'passed';
  report.outputId = outputId;
  await page.screenshot({ path: join(evidence, 'passed-reload.png'), fullPage: true });
  await writeFile(join(evidence, 'passed-reload.dom.txt'), await page.locator('body').innerText());
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = {
    label: report.activeAction.label,
    locator: report.activeAction.locator,
    elapsedMs: Date.now() - report.activeAction.startedAt,
  };
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
  if (browser && report.status !== 'failed') {
    report.incidentalAssets = browser.diagnostics.assetFailures;
    report.errors = [...browser.diagnostics.console, ...browser.diagnostics.pageErrors, ...browser.diagnostics.httpFailures, ...browser.diagnostics.networkFailures];
    if (report.errors.length) {
      report.status = 'failed';
      report.error = `Unexpected browser diagnostics: ${JSON.stringify(report.errors)}`;
      process.exitCode = 1;
    }
  }
  await browser?.close();
  if (apiBuildFreeze) {
    try {
      await apiBuildFreeze.assertUnchanged();
      const finalObservation = await checkContainerApiBuildStamp(apiContainer);
      report.apiBuildIdentity.after = extractBuildIdentity(finalObservation);
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreezeError = String(error.stack ?? error);
      process.exitCode = 1;
    }
  }
  if (sourceFreeze) {
    try {
      report.sourceFreeze.after = sourceFingerprint(sourceRoot);
      report.sourceFreeze.check = await sourceFreeze.assertUnchanged();
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.sourceFreeze.error = String(error.stack ?? error);
      process.exitCode = 1;
    }
  }
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
process.stdout.write(`${JSON.stringify({ status: report.status, explorer, evidence, error: report.error })}\n`);
