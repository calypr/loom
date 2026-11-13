#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { existsSync, readdirSync, writeFileSync } from 'node:fs';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { CDPClient } from '../../browser/cdp.mjs';
import { browserURL } from './builder-url.mjs';
import { assertPatientRows, currentPreviewReady, patientOracle } from './builder-controls.mjs';
import { authoringCommandSemanticsVersion, bootstrapSeedPlan, canonicalProjectID } from '../../loom-dev.mjs';
import { buildArangoShellInvocation, summarizeArangoShellResult } from '../helpers/owned-arangosh-command.mjs';

const options = { budgetMs: 140000 };
for (let i = 2; i < process.argv.length; i++) {
  const key = { '--out': 'out', '--chrome': 'chrome', '--budget-ms': 'budgetMs' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown/missing option ${process.argv[i]}`);
  options[key] = key === 'budgetMs' ? Number(process.argv[++i]) : process.argv[++i];
}
assert.ok(options.out, '--out is required');
assert.ok(Number.isFinite(options.budgetMs) && options.budgetMs >= 30000 && options.budgetMs <= 170000, '--budget-ms must be 30000..170000');
const out = resolve(options.out);
assert.ok(!existsSync(out) || readdirSync(out).length === 0, 'Refusing to overwrite browser evidence');
await mkdir(out, { recursive: true });

const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:3183';
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8183';
const project = process.env.LOOM_CDA_PROJECT ?? 'loom_dev_verify_qwen-ready';
const fixtureDir = process.env.LOOM_CDA_DATASET_DIR;
assert.ok(fixtureDir && existsSync(join(fixtureDir, 'Patient.ndjson')), `Patient fixture oracle is missing under LOOM_CDA_DATASET_DIR (${fixtureDir ?? 'unset'})`);
const runOwnedExplorerPrefix = 'core-protocol-browser-';
const explorerId = `${runOwnedExplorerPrefix}${process.pid}-${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
const authoringPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2`;
const tableTitle = 'Core protocol preview';
const short = randomUUID().replace(/-/g, '').slice(0, 6);
const labelAlpha = `Patient ID-alpha-${short}`;
const labelBeta = `Patient ID-beta-${short}`;
const labelGamma = `Family-gamma-${short}`;
const labelDelta = `Gender-delta-${short}`;
const labelEpsilon = `Patient ID-epsilon-${short}`;
const labelZeta = `Family-zeta-${short}`;
const labelEta = `Gender-eta-${short}`;

const report = {
  schemaVersion: 1,
  status: 'running',
  mode: 'joined-production-browser-witness',
  environment: { uiOrigin, apiOrigin, project, canonicalProject: canonicalProjectID(project), fixtureDir, explorerId, tableTitle, noAuth: process.env.LOOM_CDA_NO_AUTH ?? null, arangoContainer: process.env.LOOM_CDA_ARANGO_CONTAINER ?? null },
  checks: [],
  wire: { reconcileRequests: [], reconcileResponses: [], previewResponses: [], commandResponses: [], responseInterceptions: [] },
  probes: [],
  interceptErrors: [],
  canceledNetwork: [],
  netFailures: [],
  dialogs: [],
  errors: [],
  screenshots: [],
  seed: null,
  hold: null,
  holdC: null,
  holdD: null,
  holdE: null,
  witnessA: null,
  witnessB: null,
  witnessC: null,
  witnessD: null,
  witnessE: null,
  m5: null,
  scope: {
    joined: [
      'real Chromium loaded the deployed Builder UI at LOOM_CDA_UI_ORIGIN (the stack harness serves the repo production ui/packages/loom-ui/src modules directly; script provenance recorded in ui.scriptSrcs)',
      'the production ui/packages/loom-ui api.ts client issued the network calls through the deployed UI proxy to LOOM_CDA_API_ORIGIN',
      'the production Go handlers persisted the edits in Arango-backed Explorer state, re-read over HTTP independently of the page',
      'the same saved head compiled into a reconcile receipt whose rows the same page displayed and which was re-executed by receipt ID over the real wire',
      'Witness B saved through the same chain while the Witness A-prime compile was CDP-held at the Fetch Request stage',
      'Witness C detached the owning Builder context (same-document Viewer context switch) while an independent CDP hold kept the whole-workspace reconcile in flight, then released it so the production Go handler answered the obsolete request late over the real wire',
      'Witness D held the first-save compile response at the Fetch Response stage after the production Go handler had already committed the save (real response body read while paused) and proved the receipt remained re-executable out-of-band while the page stayed bound to the previous receipt; a second real save advanced the persisted head mid-hold and its own receipt-bound preview displayed before release',
      'Witness E held the receipt-bound preview response at the Fetch Response stage after the production preview handler had already produced the rows (real preview body read while paused); a real rename committed the next head mid-hold, the page displayed only the newest receipt-bound preview throughout, and the held receipt appeared on no page surface before, during, or after release',
      'M5 lifecycle on this same joined page: a fresh-document reload re-hydrated the displayed head identity from persisted state, and the real Delete-table and Undo controls (window.confirm accepted by the driver) deleted every persisted table and then restored the previous draft revision over the production commands channel',
    ],
    separate: [
      'compile-seam scheduling and ordering is separately proven by the Go replay (scripts/verify-builder-formal-core.mjs --phase behavior --feature core-protocol)',
      'component-internal render logic is separately proven by mounted-driver checks (scripts/browser/run.mjs)',
    ],
    limits: [
      'client-side cancellation of the held request is observed via deliveryMode and never implies server rollback; persisted head is re-read after release instead',
      'transport cancellation proves no server-side undo; this witness asserts only displayed state and persisted state survive the late arrival or cancellation',
      'late delivery is genuinely observable only around context detach: the automatic-preview compile is owned by a query whose unmount aborts its fetch (ui/packages/loom-ui/src/react.tsx:104-114 -> BuilderWorkspace.tsx:2995-2996), so Witness C holds the ownerless whole-workspace compile (BuilderWorkspace.tsx:3310) which no detach path aborts (react.tsx:151-171 mutations abort only via the returned abort())',
      'Witness C asserts adoption, not server rollback: a released late request does reach the production handler and may store a receipt; the assertions are that no page surface, no compensating request, and no persisted head movement follow it',
      'one run-owned Explorer is created; no other Explorer is touched',
    ],
  },
};
const passed = (name, evidence) => report.checks.push({ name, status: 'passed', evidence });
const wait = ms => new Promise(resolveWait => setTimeout(resolveWait, ms));
const startedAt = performance.now();
const nowMs = () => Math.round(performance.now() - startedAt);
const finishSummary = () => ({
  status: report.status,
  explorerId,
  outputId: report.seed?.outputId ?? null,
  checks: report.checks.map(check => check.name),
  hold: typeof report.hold === 'function' ? report.hold() : report.hold,
  holdC: typeof report.holdC === 'function' ? report.holdC() : report.holdC,
  holdD: typeof report.holdD === 'function' ? report.holdD() : report.holdD,
  holdE: typeof report.holdE === 'function' ? report.holdE() : report.holdE,
  error: report.error?.message ?? null,
});

writeFileSync(join(out, 'browser.json'), JSON.stringify(report, null, 2));
const watchdog = setTimeout(() => {
  report.status = 'failed';
  report.error = { message: `Whole-run budget of ${options.budgetMs}ms exceeded` };
  report.timingMs = { total: nowMs(), budget: options.budgetMs };
  try { writeFileSync(join(out, 'browser.json'), JSON.stringify(report, null, 2)); } catch { }
  console.log(JSON.stringify({ ...finishSummary(), watchdog: true }));
  process.exit(1);
}, options.budgetMs);
watchdog.unref();

async function until(probe, message, milliseconds = 5000) {
  const deadline = performance.now() + milliseconds;
  for (;;) {
    let value = null;
    try { value = await probe(); } catch { value = null; }
    if (value) return value;
    assert.ok(performance.now() < deadline, `Condition failed before deadline: ${message}`);
    await wait(40);
  }
}
function withTimeout(promise, ms, label) {
  let timer;
  return Promise.race([
    promise.finally(() => clearTimeout(timer)),
    new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms); }),
  ]);
}

const api = async (path, body, timeoutMs = 15000) => {
  const method = body === undefined ? 'GET' : 'POST';
  let response;
  try {
    response = await fetch(apiOrigin + path, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': randomUUID() },
      body: method === 'POST' ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(timeoutMs),
    });
  } catch (error) {
    throw new Error(`${method} ${apiOrigin}${path} could not reach the stack: ${error.message}. Set/verify LOOM_CDA_UI_ORIGIN/LOOM_CDA_API_ORIGIN for the owned stack.`);
  }
  report.probes.push({ method, path, status: response.status });
  const value = await response.json().catch(() => null);
  assert.ok(response.ok, `${method} ${path} returned HTTP ${response.status}: ${JSON.stringify(value ?? null).slice(0, 400)}`);
  return value;
};

let client;
let chrome;
let temp;
try {
  {
    const ui = await fetch(`${uiOrigin}/`, { signal: AbortSignal.timeout(8000) })
      .catch(error => { throw new Error(`UI origin ${uiOrigin} unreachable: ${error.message}. Set/verify LOOM_CDA_UI_ORIGIN for the owned stack.`); });
    assert.equal(ui.status, 200, `UI origin ${uiOrigin} returned HTTP ${ui.status}`);
    const list = await api(`/api/v1/projects/${encodeURIComponent(project)}/explorers`, undefined, 8000);
    assert(Array.isArray(list), 'API explorer list was not an array');
    report.explorerLedger = { before: list.map(row => ({ explorerId: row.explorerId, updatedAt: row.updatedAt, title: row.title, management: row.management })) };
    passed('stack-reachable', { uiOrigin, uiStatus: ui.status, apiOrigin, explorerListStatus: 200 });
  }

  {
    await api(`/api/v1/projects/${encodeURIComponent(project)}/explorers`, { name: explorerId, title: `Core protocol join QA ${explorerId}` });
    const builder = await api(`${authoringPath}/builder`);
    const plan = bootstrapSeedPlan(builder);
    const apply = async (commandId, commands, state) => api(`${authoringPath}/commands`, {
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      ...(state.draftDigest ? { expectedDraftDigest: state.draftDigest } : {}),
      commandId,
      semanticsVersion: authoringCommandSemanticsVersion(state),
      commands,
    });
    const created = await apply(`core-protocol-${explorerId}-table`,
      [{ type: 'CREATE_TABLE', title: tableTitle, rootNodeId: plan.rootNodeId }], builder);
    const outputId = created.results?.find(result => result.type === 'TABLE_CREATED')?.outputId;
    assert.ok(outputId, 'Seeded table command returned no output identity');
    const titles = { id: 'ID', 'name[].family': 'Family', gender: 'Gender' };
    await apply(`core-protocol-${explorerId}-columns`, plan.candidates.map(candidate => ({
      type: 'ADD_COLUMN',
      outputId,
      occurrenceId: 'base',
      candidateId: candidate.candidateId,
      projectionMode: candidate.defaultProjectionMode,
      initialPresentation: 'TABLE',
      title: titles[String(candidate.fieldPath ?? '').replace(/^root\./, '')],
    })), { catalog: builder.catalog, draftVersion: created.draftVersion, draftDigest: created.draftDigest });
    const settledSeed = await api(`${authoringPath}/builder`);
    const columnsOf = state => state?.workspace?.documents?.find(document => document.output?.id === outputId)?.columns ?? [];
    const labelsByPath = {};
    for (const column of columnsOf(settledSeed)) labelsByPath[column.source?.field?.path ?? '?'] = column.label ?? null;
    assert.deepEqual(['id', 'name[].family', 'gender'].map(path => labelsByPath[path]),
      ['ID', 'Family', 'Gender'], 'Seeded column labels drifted from the oracle contract');
    report.seed = {
      outputId,
      title: tableTitle,
      head: { draftVersion: settledSeed.draftVersion, draftDigest: settledSeed.draftDigest },
      snapshotToken: builder.catalog.snapshotToken,
      columns: columnsOf(settledSeed).map(column => ({ path: column.source?.field?.path ?? '?', label: column.label ?? null })),
    };
    passed('seed-created-run-owned-explorer', report.seed);
  }

  const { outputId } = report.seed;
  const labelFor = path => report.seed.columns.find(column => column.path === path)?.label ?? null;

  temp = await mkdtemp(join(tmpdir(), 'loom-core-protocol-browser-'));
  const profile = join(temp, 'profile');
  await mkdir(profile);
  const chromePath = options.chrome ?? process.env.LOOM_E2E_CHROME
    ?? ['/usr/bin/chromium', '/opt/google/chrome/chrome'].find(candidate => existsSync(candidate));
  assert.ok(chromePath && existsSync(chromePath), 'No Chrome binary: pass --chrome, set LOOM_E2E_CHROME, or install /usr/bin/chromium or /opt/google/chrome/chrome');
  const version = spawnSync(chromePath, ['--version'], { encoding: 'utf8', timeout: 3000 });
  assert.equal(version.status, 0, version.stderr);
  report.chrome = version.stdout.trim();
  let chromeErrors = '';
  chrome = spawn(chromePath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--window-size=1600,1000', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
  chrome.stderr.on('data', chunk => { chromeErrors = (chromeErrors + chunk).slice(-4000); });
  const portFile = join(profile, 'DevToolsActivePort');
  await until(() => existsSync(portFile), `Chromium DevTools startup; stderr: ${chromeErrors.slice(-800)}`, 10000);
  const [port, browserPath] = (await readFile(portFile, 'utf8')).trim().split('\n');
  const target = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' }).then(response => response.json());
  client = await CDPClient.connect(target.webSocketDebuggerUrl);
  const send = (method, params = {}, ms = 15000) => withTimeout(client.send(method, params), ms, method);
  client.on('Runtime.exceptionThrown', event => report.errors.push(event.exceptionDetails?.exception?.description ?? event.exceptionDetails?.text ?? 'unknown exception'));
  const m5Dialogs = { active: false };
  client.on('Page.javascriptDialogOpening', event => {
    const entry = { atMs: nowMs(), type: event.type ?? null, message: event.message ?? null, phase: m5Dialogs.active ? 'm5-native-journey' : 'unexpected', accepted: false };
    report.dialogs.push(entry);
    if (m5Dialogs.active && event.type === 'confirm' && /^Delete .*\?$/.test(event.message ?? '')) {
      entry.accepted = true;
      withTimeout(client.send('Page.handleJavaScriptDialog', { accept: true }), 5000, 'dialogAccept')
        .catch(error => report.interceptErrors.push({ phase: 'dialogAccept', error: error.message }));
    }
  });
  const networkEvents = new Map();
  client.on('Network.requestWillBeSent', event => networkEvents.set(event.requestId, event.request.url));
  client.on('Network.loadingFailed', event => {
    const entry = { requestId: event.requestId, url: networkEvents.get(event.requestId) ?? null, type: event.type ?? null, errorText: event.errorText, atMs: nowMs() };
    (event.canceled ? report.canceledNetwork : report.netFailures).push(entry);
  });
  const isAuthoredAuthoringURL = url => url.startsWith(`${uiOrigin}/api/v1/projects/`) && url.includes(`/explorers/${explorerId}/authoring/v2/`);
  const netAuthored = new Map();
  client.on('Network.requestWillBeSent', event => {
    if (isAuthoredAuthoringURL(event.request.url)) netAuthored.set(event.requestId, { url: event.request.url, method: event.request.method, status: null });
  });
  client.on('Network.responseReceived', event => {
    const entry = netAuthored.get(event.requestId);
    if (entry) entry.status = event.response?.status ?? null;
  });
  client.on('Network.loadingFinished', event => {
    const entry = netAuthored.get(event.requestId);
    if (!entry) return;
    netAuthored.delete(event.requestId);
    (async () => {
      let body = null;
      try {
        const captured = await send('Network.getResponseBody', { requestId: event.requestId }, 8000);
        body = captured.base64Encoded ? JSON.parse(Buffer.from(captured.body, 'base64').toString('utf8')) : JSON.parse(captured.body);
      } catch (error) { report.interceptErrors.push({ requestId: event.requestId, phase: 'networkGetResponseBody', url: entry.url, error: error.message }); }
      const record = { requestId: event.requestId, atMs: nowMs(), status: entry.status, url: entry.url };
      if (entry.url.includes('/reconcile')) {
        const document_ = body?.builder?.documents?.find(candidate => candidate?.output?.id === outputId);
        report.wire.reconcileResponses.push({
          ...record,
          receiptId: body?.receiptId ?? null, kind: body?.kind ?? null,
          snapshotToken: body?.snapshotToken ?? null, intentDigest: body?.intentDigest ?? null,
          outputs: Array.isArray(body?.outputs) ? body.outputs.map(output => ({ outputId: output.outputId, columns: (output.columns ?? []).map(column => column.label) })) : null,
          diagnostics: Array.isArray(body?.diagnostics) ? body.diagnostics.length : null,
          headLabels: document_ ? (document_.columns ?? []).map(column => column.label ?? null) : null,
          outputIds: Array.isArray(body?.outputs) ? body.outputs.map(output => output.outputId) : null,
          code: body?.code ?? null,
        });
      } else if (entry.url.includes('/preview')) {
        report.wire.previewResponses.push({
          ...record,
          receiptId: body?.receiptId ?? null, outputId: body?.outputId ?? null,
          rowCount: body?.rowCount ?? null, sampled: body?.sampled ?? null,
          columns: Array.isArray(body?.columns) ? body.columns.map(column => column.label) : null,
          rowsSample: Array.isArray(body?.rows) ? body.rows.slice(0, 3) : null,
        });
      } else if (entry.url.endsWith('/authoring/v2/commands')) {
        report.wire.commandResponses.push({
          ...record,
          headVersion: body?.draftVersion ?? null, headDigest: body?.draftDigest ?? null,
          resultTypes: Array.isArray(body?.results) ? body.results.map(result => result.type ?? null) : null,
          code: body?.code ?? null,
        });
      }
    })().catch(error => report.interceptErrors.push({ phase: 'networkCapture', error: error.message }));
  });
  await Promise.all([send('Page.enable'), send('Runtime.enable'), send('Network.enable')]);

  const evaluate = async (expression, ms = 15000) => {
    const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, ms);
    assert.ok(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
    return result.result?.value;
  };
  const pageURL = browserURL({ uiUrl: uiOrigin }, project, explorerId, 'builder');
  report.ui = { pageURL };
  await send('Page.navigate', { url: pageURL });
  await until(() => evaluate('Boolean(document.querySelector(\'[data-testid="construction-preview"]\'))'), 'production Builder preview mounted', 20000);
  report.ui.scriptSrcs = await evaluate('Array.from(document.querySelectorAll("script[src]"), e => e.src)');
  assert.ok(report.ui.scriptSrcs.length > 0, 'Page loaded without any deployed UI module scripts');

  const persistedState = async path => {
    const state = await api(`${authoringPath}/builder`, undefined, 5000);
    const columns = state?.workspace?.documents?.find(document => document.output?.id === outputId)?.columns ?? [];
    return { label: columns.find(column => column.source?.field?.path === path)?.label ?? null, draftVersion: state.draftVersion, draftDigest: state.draftDigest };
  };
  const panelSnapshot = () => evaluate(`(() => {
    const ws = document.querySelector('[data-testid="construction-workspace"]');
    const pv = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return {
      workspaceDraftVersion: ws?.dataset.draftVersion ?? null,
      workspaceDraftDigest: ws?.dataset.draftDigest ?? null,
      previewStatus: pv?.dataset.previewStatus ?? null,
      previewReceiptId: pv?.dataset.previewReceiptId ?? null,
      previewOutputId: pv?.dataset.previewOutputId ?? null,
      previewCurrentDraftVersion: pv?.dataset.currentDraftVersion ?? null,
      previewCurrentDraftDigest: pv?.dataset.currentDraftDigest ?? null,
      headers: table ? Array.from(table.querySelectorAll('[role="columnheader"]'), cell => cell.innerText.trim()) : null,
      rows: table ? Array.from(table.querySelectorAll('[role="row"]'), row => row.innerText) : null,
      ariaRowCount: table?.getAttribute('aria-rowcount') ?? null,
      mismatchText: /MISMATCH|stale draft/i.test(document.body.innerText),
      previewDatasets: pv ? { ...pv.dataset } : null,
      workspaceDatasets: ws ? { ...ws.dataset } : null,
      statusTexts: Array.from(document.querySelectorAll('[role="status"]'), node => node.innerText?.trim() ?? '').filter(Boolean),
    };
  })()`);
  const previewReady = () => evaluate(`(${currentPreviewReady.toString()})(${JSON.stringify(outputId)})`);
  const screenshot = async name => {
    try {
      const image = await send('Page.captureScreenshot', { format: 'png' }, 8000);
      await writeFile(join(out, name), Buffer.from(image.data, 'base64'));
      report.screenshots.push(name);
    } catch (error) { report.screenshots.push({ name, error: error.message }); }
  };
  const headersInclude = (headers, label) => (headers ?? []).some(header => {
    const cell = header.toLowerCase().trim();
    const wanted = label.toLowerCase();
    return cell === wanted || cell.startsWith(`${wanted} [`);
  });
  const oracle = patientOracle({ fixtureDir });
  const oracleCheck = (snapshot, label) => {
    assert.ok(snapshot.rows && snapshot.rows.length >= 3, `${label}: preview table rendered without the two fixture rows`);
    return assertPatientRows(snapshot.rows.slice(1), oracle.ids, oracle.genderByID, snapshot.rows[0]);
  };

  const previewReadyFor = expectedOutputId => evaluate(`(${currentPreviewReady.toString()})(${JSON.stringify(expectedOutputId ?? null)})`);
  const previewReadyAny = () => previewReadyFor(null);
  const selectedTableOutputId = () => evaluate(`(document.querySelector('[data-testid^="construction-table-"][aria-current="page"]')?.getAttribute('data-testid') ?? '').replace('construction-table-', '')`);
  const demoControlFinder = text => `[...document.querySelectorAll('.demo-controls button')].find(node => node.textContent.trim() === ${JSON.stringify(text)})`;
  async function clickDemoControl(text, ms = 8000) {
    await until(() => evaluate(`Boolean(${demoControlFinder(text)})`).catch(() => false), `demo header control mounted ${text}`, ms);
    await evaluate(`(${demoControlFinder(text)})?.scrollIntoView({block:'center',inline:'center'})`);
    await wait(40);
    const point = await until(() => evaluate(`(() => { const e = ${demoControlFinder(text)}; if (!e) return null; const b = e.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2, disabled: e.matches(':disabled') }; })()`).catch(() => null), `visible demo header control ${text}`);
    assert.ok(!point.disabled, `Disabled demo header control ${text}`);
    await client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    return point;
  }
  const detachedSnapshot = () => evaluate(`(() => {
    const ws = document.querySelector('[data-testid="construction-workspace"]');
    const pv = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const receiptBearing = Array.from(document.querySelectorAll('[data-preview-receipt-id],[data-preview-output-id],[data-current-draft-version],[data-draft-version]'),
      node => ({ testId: node.getAttribute('data-testid') ?? null, receiptId: node.dataset.previewReceiptId ?? null, draftVersion: node.dataset.draftVersion ?? node.dataset.currentDraftVersion ?? null }));
    return {
      builderWorkspacePresent: Boolean(ws),
      constructionPreviewPresent: Boolean(pv),
      previewTablePresent: Boolean(table),
      previewHeaders: table ? Array.from(table.querySelectorAll('[role="columnheader"]'), cell => cell.innerText.trim()) : null,
      previewRows: table ? Array.from(table.querySelectorAll('[role="row"]'), row => row.innerText) : null,
      receiptBearingNodes: receiptBearing,
      statusTexts: Array.from(document.querySelectorAll('[role="status"]'), node => node.innerText?.trim() ?? '').filter(Boolean),
      alertTexts: Array.from(document.querySelectorAll('[role="alert"]'), node => node.innerText?.trim() ?? '').filter(Boolean),
      bodyHasObsoleteMarkers: /MISMATCH|stale draft|obsolete/i.test(document.body.innerText),
    };
  })()`);
  const envelopeTruth = async label => {
    const state = await api(`${authoringPath}/builder`, undefined, 8000);
    const documents = state?.workspace?.documents ?? [];
    report.probes.push({ method: 'GET', path: `${authoringPath}/builder`, status: 200, observation: label });
    return {
      draftVersion: state.draftVersion,
      draftDigest: state.draftDigest,
      tableCount: documents.length,
      documents: documents.map(document_ => ({
        outputId: document_.output?.id ?? null,
        columns: (document_.columns ?? []).map(column => ({ path: column.source?.field?.path ?? '?', label: column.label ?? null })),
      })),
    };
  };

  const menuSelector = '[data-testid="preview-table-column-settings"]';
  const columnsOpen = () => evaluate(`Boolean(document.querySelector(${JSON.stringify(menuSelector)}))`).catch(() => false);
  async function click(selector, ms = 5000) {
    await until(() => evaluate(`Boolean(document.querySelector(${JSON.stringify(selector)}))`).catch(() => false), `control mounted ${selector}`, ms);
    await evaluate(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'center',inline:'center'})`);
    await wait(40);
    const point = await until(() => evaluate(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if (!e) return null; const b = e.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2, width: b.width, height: b.height, disabled: e.matches(':disabled') }; })()`).catch(() => null), `visible control ${selector}`);
    assert.ok(!point.disabled, `Disabled target ${selector}`);
    await client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    return point;
  }
  const openColumnsMenu = async () => {
    if (!(await columnsOpen())) {
      await click('[data-testid="preview-table-header-controls"] button');
      await until(async () => await columnsOpen(), 'column settings panel mounted');
    }
  };
  async function renameColumn(columnPath, currentLabel, nextLabel) {
    await openColumnsMenu();
    const selector = `[aria-label="Column name for ${currentLabel}"]`;
    const poll = async ms => until(async () => {
      const state = await persistedState(columnPath);
      return state.label === nextLabel ? { ...state, persistedAt: nowMs() } : null;
    }, `rename committed to persisted state: ${currentLabel} -> ${nextLabel}`, ms);
    const pressEnter = async () => {
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
    };
    let persisted = null;
    for (const mechanism of ['triple-click', 'ctrl-a']) {
      await openColumnsMenu();
      const point = await click(selector);
      if (mechanism === 'triple-click') {
        await client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 3 });
        await client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 3 });
      } else {
        await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'a', code: 'KeyA', windowsVirtualKeyCode: 65, nativeVirtualKeyCode: 65, modifiers: 2 });
        await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'a', code: 'KeyA', windowsVirtualKeyCode: 65, nativeVirtualKeyCode: 65, modifiers: 2 });
      }
      await send('Input.insertText', { text: nextLabel });
      await pressEnter();
      persisted = await poll(4000).catch(() => null);
      if (persisted) break;
    }
    if (!persisted) {
      const value = await evaluate(`document.querySelector(${JSON.stringify(selector)})?.value ?? null`);
      assert.equal(value, nextLabel, `rename input held ${JSON.stringify(value)} instead of ${JSON.stringify(nextLabel)}`);
      await click(`${menuSelector} p`);
      persisted = await poll(6000);
    }
    report.probes.push({ method: 'GET', path: `${authoringPath}/builder`, status: 200, observation: `persisted ${currentLabel} -> ${nextLabel}` });
    assert.equal(persisted.label, nextLabel);
    return persisted;
  }

  let holdArmed = false;
  let holdExpectedDraftVersion = null;
  let hold = null;
  let holdCArmed = false;
  let holdCExpected = null;
  let holdC = null;
  let holdDArmed = false;
  let holdDExpectedDraftVersion = null;
  let holdD = null;
  let holdEArmed = false;
  let holdEArmAtMs = 0;
  let holdEBaselineReceipt = null;
  let holdE = null;
  const previewStageOne = new Map();
  const fetchHeldResponseBody = async holder => {
    try {
      const captured = await send('Fetch.getResponseBody', { requestId: holder.requestId }, 8000);
      const text = captured.base64Encoded ? Buffer.from(captured.body, 'base64').toString('utf8') : captured.body;
      return JSON.parse(text);
    } catch (error) {
      report.interceptErrors.push({ requestId: holder.requestId, phase: 'fetchGetResponseBody', error: error.message });
      return null;
    }
  };
  const requestPostDataOf = async event => {
    if (event.request?.postData) return event.request.postData;
    if (!event.networkId) return null;
    try {
      const post = await send('Network.getRequestPostData', { requestId: event.networkId }, 8000);
      return post.postData ?? null;
    } catch (error) {
      report.interceptErrors.push({ requestId: event.requestId, phase: 'getRequestPostData', error: error.message });
      return null;
    }
  };
  await send('Fetch.enable', { patterns: [
    { urlPattern: `${uiOrigin}/api/v1/projects/*/explorers/${explorerId}/authoring/v2/reconcile*`, requestStage: 'Request' },
    { urlPattern: `${uiOrigin}/api/v1/projects/*/explorers/${explorerId}/authoring/v2/reconcile*`, requestStage: 'Response' },
    { urlPattern: `${uiOrigin}/api/v1/projects/*/explorers/${explorerId}/authoring/v2/preview*`, requestStage: 'Request' },
    { urlPattern: `${uiOrigin}/api/v1/projects/*/explorers/${explorerId}/authoring/v2/preview*`, requestStage: 'Response' },
  ] });
  client.on('Fetch.requestPaused', event => {
    if (event.responseStatusCode !== undefined || event.response !== undefined) {
      (async () => {
        const url = event.request?.url ?? '';
        let intercepted = false;
        if (url.includes('/reconcile') && holdDArmed && !holdD) {
          const body = await fetchHeldResponseBody({ requestId: event.requestId });
          const head = body?.builder?.documents?.find(candidate => candidate?.output?.id === outputId);
          const headLabels = Array.isArray(head?.columns) ? head.columns.map(column => column.label ?? null) : [];
          if (event.responseStatusCode === 200 && body?.receiptId && contractHasLabel(headLabels, labelDelta) && !contractHasLabel(headLabels, labelEpsilon)
            && (body.outputIds === undefined || (body.outputIds.length === 1 && body.outputIds[0] === outputId))) {
            holdD = { requestId: event.requestId, networkId: event.networkId ?? null, url, responseStatus: event.responseStatusCode, response: { status: event.responseStatusCode, receiptId: body.receiptId, kind: body.kind ?? null, headLabels }, heldAtMs: nowMs(), releasedAtMs: null, deliveryMode: null };
            holdDArmed = false;
            report.holdD = () => (holdD ? {
              requestId: holdD.requestId, networkId: holdD.networkId, url: holdD.url,
              heldAtMs: holdD.heldAtMs, releasedAtMs: holdD.releasedAtMs, deliveryMode: holdD.deliveryMode,
              response: holdD.response ? { ...holdD.response } : null,
            } : null);
            intercepted = true;
          }
        }
        if (!intercepted && url.includes('/preview') && holdEArmed && !holdE) {
          const body = await fetchHeldResponseBody({ requestId: event.requestId });
          const receiptValue = body?.receiptId ?? null;
          const outputMatches = body?.outputId === outputId;
          const excludedReceipt = Boolean(receiptValue)
            && (receiptValue === holdEBaselineReceipt
              || (holdD != null && holdD.response != null && receiptValue === holdD.response.receiptId)
              || report.wire.reconcileResponses.some(entry => entry.receiptId === receiptValue && entry.atMs < holdEArmAtMs));
          if (event.responseStatusCode === 200 && outputMatches && receiptValue && !excludedReceipt && nowMs() >= holdEArmAtMs) {
            holdE = { requestId: event.requestId, networkId: event.networkId ?? null, url, responseStatus: event.responseStatusCode, request: { outputId: body?.outputId ?? null, receiptId: receiptValue }, response: { status: event.responseStatusCode, receiptId: body?.receiptId ?? null, rowCount: body?.rowCount ?? null, columns: Array.isArray(body?.columns) ? body.columns.map(column => column.label) : [], rowsSample: Array.isArray(body?.rows) ? body.rows.slice(0, 3) : null }, heldAtMs: nowMs(), releasedAtMs: null, deliveryMode: null };
            holdEArmed = false;
            report.holdE = () => (holdE ? {
              requestId: holdE.requestId, networkId: holdE.networkId, url: holdE.url,
              heldAtMs: holdE.heldAtMs, releasedAtMs: holdE.releasedAtMs, deliveryMode: holdE.deliveryMode,
              request: { ...holdE.request }, response: { ...holdE.response },
            } : null);
            intercepted = true;
          }
        }
        if (intercepted) return;
        report.wire.responseInterceptions.push({ requestId: event.requestId, atMs: nowMs(), url, status: event.responseStatusCode ?? null });
        client.send('Fetch.continueResponse', { requestId: event.requestId }).catch(error => report.interceptErrors.push({ requestId: event.requestId, phase: 'continueResponse', error: error.message }));
      })().catch(error => report.interceptErrors.push({ phase: 'responseStage', error: error.message }));
      return;
    }
    const { requestId, request, networkId } = event;
    const pausedAt = nowMs();
    if (event.canceled) report.canceledNetwork.push({ source: 'fetch', requestId, networkId: networkId ?? null, url: request.url, atMs: pausedAt });
    if (request.url.includes('/preview')) {
      let body0 = null;
      try { body0 = JSON.parse(request.postData ?? 'null'); } catch { body0 = null; }
      previewStageOne.set(requestId, { outputId: body0?.outputId ?? null, receiptId: body0?.receiptId ?? null });
    }
    let body = null;
    try { body = JSON.parse(request.postData ?? 'null'); } catch { body = null; }
    report.wire.reconcileRequests.push({ requestId, atMs: pausedAt, url: request.url, body });
    if (holdCArmed && !holdC && holdCExpected
      && body?.draftVersion === holdCExpected.draftVersion
      && (holdCExpected.outputId === undefined ? !body?.outputId : body?.outputId === holdCExpected.outputId)) {
      holdC = { requestId, networkId: networkId ?? null, url: request.url, body, heldAtMs: pausedAt, releasedAtMs: null, detachedAtMs: null, response: null, deliveryMode: null };
      report.holdC = () => (holdC ? {
        requestId: holdC.requestId,
        networkId: holdC.networkId,
        heldAtMs: holdC.heldAtMs,
        detachedAtMs: holdC.detachedAtMs,
        releasedAtMs: holdC.releasedAtMs,
        deliveryMode: holdC.deliveryMode,
        heldBody: { draftVersion: holdC.body?.draftVersion ?? null, draftDigest: holdC.body?.draftDigest ?? null, outputId: holdC.body?.outputId ?? null, requestId: holdC.body?.requestId ?? null, snapshotToken: holdC.body?.snapshotToken ?? null },
        response: holdC.response ? { status: holdC.response.status, receiptId: holdC.response.receiptId, kind: holdC.response.kind, outputIds: holdC.response.outputIds, headLabels: holdC.response.headLabels, code: holdC.response.code } : null,
      } : null);
      return;
    }
    if (holdArmed && !hold && (holdExpectedDraftVersion === null || body?.draftVersion === holdExpectedDraftVersion)) {
      hold = { requestId, url: request.url, body, heldAtMs: pausedAt, releasedAtMs: null, response: null, deliveryMode: null };
      report.hold = () => (hold ? {
        requestId: hold.requestId,
        heldAtMs: hold.heldAtMs,
        releasedAtMs: hold.releasedAtMs,
        deliveryMode: hold.deliveryMode,
        heldBody: { draftVersion: hold.body?.draftVersion ?? null, draftDigest: hold.body?.draftDigest ?? null, outputId: hold.body?.outputId ?? null, requestId: hold.body?.requestId ?? null },
        response: hold.response ? { status: hold.response.status, receiptId: hold.response.receiptId, headLabels: hold.response.headLabels } : null,
      } : null);
      return;
    }
    client.send('Fetch.continueRequest', { requestId }).catch(error => report.interceptErrors.push({ requestId, phase: 'continueRequest', error: error.message }));
  });
  const betaEra = entry => Array.isArray(entry.headLabels) && entry.headLabels[0] === labelBeta && entry.headLabels[1] === 'Family';

  const initial = await until(async () => ((await previewReady()) ? await panelSnapshot() : null), 'initial automatic preview became ready', 25000);
  oracleCheck(initial, 'baseline');
  passed('initial-automatic-preview-bound-to-seeded-head', {
    boundTo: { outputId, draftVersion: String(report.seed.head.draftVersion), receiptId: initial.previewReceiptId },
    headers: initial.headers, fixturePatientIDs: oracle.ids,
  });

  report.witnessA = { role: 'first rename fully joined through every layer', column: 'id', from: labelFor('id'), to: labelAlpha };
  report.witnessA.persisted = await renameColumn('id', labelFor('id'), labelAlpha);
  const aHead = report.witnessA.persisted;
  passed('witness-A-save-persisted-through-production-handlers', { column: 'id', draftVersion: aHead.draftVersion, draftDigest: aHead.draftDigest, label: labelAlpha });
  const aReconcile = await until(
    () => report.wire.reconcileResponses.findLast(entry => entry.headLabels?.[0] === labelAlpha && entry.headLabels?.[1] === 'Family' && entry.status === 200),
    'witness A compile receipt captured for the saved head', 20000);
  const aReconcileRequest = report.wire.reconcileRequests.findLast(entry => entry.body?.draftVersion === aHead.draftVersion);
  assert.ok(aReconcileRequest, 'witness A never issued a reconcile wire request for the saved head');
  assert.equal(aReconcileRequest.body.outputId, outputId, 'witness A reconcile request targeted a different output');
  assert.ok((aReconcile.outputs ?? []).some(output => output.outputId === outputId), 'witness A compile receipt does not cover the saved output');
  assert.equal(aReconcile.snapshotToken, report.seed.snapshotToken, 'witness A receipt is not bound to the persisted head catalog snapshot');
  const panelA = await until(async () => {
    const snapshot = await panelSnapshot();
    return (await previewReady()) && snapshot.previewReceiptId === aReconcile.receiptId && headersInclude(snapshot.headers, labelAlpha) ? snapshot : null;
  }, 'witness A preview displayed behind its own receipt', 20000);
  const aIDs = oracleCheck(panelA, 'witness-A');
  report.witnessA.reconcile = aReconcile;
  report.witnessA.preview = report.wire.previewResponses.findLast(entry => entry.receiptId === aReconcile.receiptId && entry.status === 200) ?? null;
  report.witnessA.panel = panelA;
  passed('witness-A-displayed-preview-reflects-saved-label-and-oracle', {
    label: labelAlpha, headers: panelA.headers, patientIDs: aIDs,
    receipt: { receiptId: aReconcile.receiptId, status: aReconcile.status, kind: aReconcile.kind, diagnostics: aReconcile.diagnostics },
    wirePreviewReceiptId: report.witnessA.preview?.receiptId ?? null,
    currentDraftVersionMatchesHead: String(panelA.previewCurrentDraftVersion) === String(aHead.draftVersion),
  });
  const replayA = await api(`${authoringPath}/preview`, { receiptId: aReconcile.receiptId, outputId, limit: 50 });
  assert.equal(replayA.receiptId, aReconcile.receiptId);
  assert.equal(replayA.outputId, outputId);
  assert.ok(replayA.rowCount > 0 && (replayA.columns ?? []).some(column => column.label === labelAlpha), 'receipt-bounded replay for witness A lost the saved label');
  await screenshot('witness-a.png');
  passed('witness-A-receipt-re-executable-by-id-over-real-wire', { receiptId: aReconcile.receiptId, rowCount: replayA.rowCount, columns: (replayA.columns ?? []).map(column => column.label), rowsContainFixtureIDs: oracle.ids.every(id => JSON.stringify(replayA.rows).includes(id)) });

  report.witnessB = { role: 'second save persists while the first compile stays wire-held', columns: [{ column: 'id', from: labelAlpha, to: labelBeta }, { column: 'name[].family', from: labelFor('name[].family'), to: labelGamma }] };
  holdArmed = true;
  holdExpectedDraftVersion = Number(aHead.draftVersion) + 1;
  report.witnessB.aPrimePersisted = await renameColumn('id', labelAlpha, labelBeta);
  const betaHead = report.witnessB.aPrimePersisted;
  await until(() => hold !== null, 'Witness A-prime reconcile wire request captured and held at the Fetch Request stage', 12000);
  assert.equal(hold.body.draftVersion, betaHead.draftVersion, 'held reconcile does not cover the Witness A-prime head');
  assert.equal(hold.body.outputId, outputId);
  passed('first-compile-wire-request-held-mid-flight', {
    heldRequestId: hold.requestId,
    held: report.hold(),
    panelWhileHeld: await panelSnapshot(),
    note: 'Fetch Request-stage hold prevents the production Go handler from even starting for this reconcile',
  });

  report.witnessB.bPersisted = await renameColumn('name[].family', labelFor('name[].family'), labelGamma);
  const bHead = report.witnessB.bPersisted;
  assert.ok(Number(bHead.draftVersion) > Number(betaHead.draftVersion), 'Witness B save did not advance the persisted head while the earlier compile was held');
  assert.notEqual(bHead.draftDigest, betaHead.draftDigest);
  assert.ok(!report.wire.reconcileResponses.some(betaEra), 'the held reconcile response arrived while it must have been outstanding');
  report.witnessB.panelDuringHold = await panelSnapshot();
  passed('second-save-persisted-while-first-compile-held', {
    heldRequestId: hold.requestId, heldDraftVersion: hold.body.draftVersion,
    betaHead: { draftVersion: betaHead.draftVersion, label: labelBeta },
    bHead: { draftVersion: bHead.draftVersion, draftDigest: bHead.draftDigest, label: labelGamma },
  });

  const bReconcile = await until(() => report.wire.reconcileResponses.findLast(entry => entry.headLabels?.[1] === labelGamma && entry.status === 200), 'Witness B compile receipt captured', 25000);
  let panelB = null;
  const panelBDeadline = performance.now() + 25000;
  for (;;) {
    const snapshot = await panelSnapshot();
    panelB = snapshot;
    report.witnessB.panelAttempt = snapshot;
    if ((await previewReady()) && snapshot.previewReceiptId === bReconcile.receiptId && headersInclude(snapshot.headers, labelBeta) && headersInclude(snapshot.headers, labelGamma)) break;
    assert.ok(performance.now() < panelBDeadline, `Witness B preview never displayed both renames behind its own receipt; last panel: ${JSON.stringify(panelB)}`);
    await wait(60);
  }
  const bIDs = oracleCheck(panelB, 'witness-B');
  const wirePreviewB = report.wire.previewResponses.findLast(entry => entry.receiptId === bReconcile.receiptId && entry.status === 200) ?? null;
  assert.ok(wirePreviewB, 'Witness B never issued the bound preview wire request');
  report.witnessB.reconcile = bReconcile;
  report.witnessB.preview = wirePreviewB;
  report.witnessB.panel = panelB;
  passed('witness-B-preview-receipt-bound-and-displayed', {
    receiptId: bReconcile.receiptId, status: bReconcile.status, kind: bReconcile.kind,
    receiptSnapshotTokenBoundToHead: bReconcile.snapshotToken === report.seed.snapshotToken,
    panelReceiptId: panelB.previewReceiptId, wirePreviewReceiptId: wirePreviewB.receiptId,
    currentDraftVersionMatchesHead: String(panelB.previewCurrentDraftVersion) === String(bHead.draftVersion),
    headers: panelB.headers, patientIDs: bIDs,
  });
  await screenshot('witness-b.png');

  const panelBeforeRelease = panelB;
  hold.releasedAtMs = nowMs();
  await client.send('Fetch.continueRequest', { requestId: hold.requestId }).catch(error => report.interceptErrors.push({ requestId: hold.requestId, phase: 'release', error: error.message }));
  const released = async () => {
    const delivered = report.wire.reconcileResponses.find(entry => entry.atMs >= hold.releasedAtMs && betaEra(entry));
    if (delivered) return { delivered };
    const canceled = report.canceledNetwork.find(entry => entry.url === hold.url);
    if (canceled) return { canceled };
    return null;
  };
  let releaseOutcome = await until(released, 'held reconcile either arrived at the fetch layer or was cancelled by the client', 3000).catch(() => null);
  if (!releaseOutcome) {
    await wait(1500);
    releaseOutcome = await released();
  }
  if (releaseOutcome?.delivered) {
    hold.deliveryMode = 'response-delivered-through-hold';
    hold.response = releaseOutcome.delivered;
    report.wire.reconcileResponses.find(entry => entry === releaseOutcome.delivered).heldWasHeld = true;
  } else if (releaseOutcome?.canceled) {
    hold.deliveryMode = 'client-cancelled-before-delivery';
    hold.response = null;
  } else {
    hold.deliveryMode = 'no-arrival-observed-in-window';
  }
  await wait(600);
  const panelAfterRelease = await until(async () => ((await previewReady()) ? await panelSnapshot() : null), 'preview settled after held release', 15000);
  assert.equal(panelAfterRelease.previewReceiptId, bReconcile.receiptId, 'late held reconcile response was adopted by the page after release');
  assert.equal(String(panelAfterRelease.previewCurrentDraftVersion), String(bHead.draftVersion), 'preview version moved off the Witness B head after release');
  assert.ok(headersInclude(panelAfterRelease.headers, labelBeta) && headersInclude(panelAfterRelease.headers, labelGamma), 'displayed labels regressed after release');
  assert.ok(!panelAfterRelease.mismatchText, 'page surfaced a mismatch or stale-draft error after release');
  const settled = await api(`${authoringPath}/builder`);
  const settledColumns = settled?.workspace?.documents?.find(document => document.output?.id === outputId)?.columns ?? [];
  const settledLabel = path => settledColumns.find(column => column.source?.field?.path === path)?.label ?? null;
  assert.equal(settled.draftVersion, bHead.draftVersion, 'persisted head changed after the held response arrived or was cancelled');
  assert.equal(settled.draftDigest, bHead.draftDigest, 'persisted digest changed after the held response arrived or was cancelled');
  assert.deepEqual([settledLabel('id'), settledLabel('name[].family'), settledLabel('gender')], [labelBeta, labelGamma, 'Gender'], 'persisted labels changed after release');
  passed('late-held-compile-never-adopted-and-no-rollback-assumed', {
    deliveryMode: hold.deliveryMode,
    heldResponse: hold.response ? { status: hold.response.status, receiptId: hold.response.receiptId, headLabels: hold.response.headLabels } : null,
    cancelEvent: releaseOutcome?.canceled ?? null,
    panelReceiptIdStable: panelBeforeRelease.previewReceiptId === panelAfterRelease.previewReceiptId && panelAfterRelease.previewReceiptId === bReconcile.receiptId,
    persistedHeadUnchanged: { draftVersion: settled.draftVersion, draftDigest: settled.draftDigest },
    note: 'No claim that transport cancellation implies server rollback; persisted state was re-read independently over HTTP',
  });

  const replayB = await api(`${authoringPath}/preview`, { receiptId: bReconcile.receiptId, outputId, limit: 50 });
  assert.equal(replayB.receiptId, bReconcile.receiptId);
  assert.equal(replayB.outputId, outputId);
  const replayLabels = (replayB.columns ?? []).map(column => column.label);
  const contractHasLabel = (labels, label) => labels.some(candidate => candidate === label || candidate.startsWith(`${label} [`));
  assert.ok(contractHasLabel(replayLabels, labelBeta) && contractHasLabel(replayLabels, labelGamma) && replayB.rowCount > 0, `receipt-bounded replay for Witness B lost a saved label: ${JSON.stringify(replayLabels)}`);
  passed('witness-B-receipt-re-executable-by-id-over-real-wire', { receiptId: bReconcile.receiptId, rowCount: replayB.rowCount, columns: replayLabels, rowsContainFixtureIDs: oracle.ids.every(id => JSON.stringify(replayB.rows).includes(id)) });

  if (await columnsOpen()) {
    await click('[data-testid="preview-table-header-controls"] button');
    await until(async () => !(await columnsOpen()), 'column settings panel closed');
  }
  report.witnessC = {
    role: 'real response for a released-but-obsolete whole-workspace compile is admitted nowhere',
    affordance: {
      contextChange: 'demo header Viewer control (same-document React context switch, no navigation)',
      requestSource: 'Review dataset toggle -> void reconcileCurrent() with no execution owner and no outputId',
    },
  };

  const envelopeBaseline = await envelopeTruth('witness C baseline before duplication');
  await click(`[data-testid="construction-table-actions-${outputId}"]`);
  await until(() => evaluate(`(() => { const menu = document.querySelector('[data-testid="construction-table-actions-menu"]'); if (!menu) return false; const style = menu.getAttribute('style') ?? ''; return /top:/.test(style) && !/visibility:\s*hidden/.test(style); })()`).catch(() => false),
    'table actions menu positioned', 5000);
  await click('[data-testid="construction-duplicate-table"]');
  const duplicateTable = await until(async () => {
    const state = await api(`${authoringPath}/builder`, undefined, 5000);
    const documents = state?.workspace?.documents ?? [];
    const copy = documents.find(document_ => document_.output?.id !== outputId);
    if (documents.length !== 2 || !copy) return null;
    return {
      outputId: copy.output?.id ?? null,
      draftVersion: Number(state.draftVersion),
      draftDigest: state.draftDigest,
      labels: (copy.columns ?? []).map(column => column.label ?? null).sort(),
    };
  }, 'duplicate table persisted as a second document under the same run-owned Explorer', 20000);
  assert.ok(duplicateTable.outputId, 'duplicated table exposed no output identity');
  assert.deepEqual(duplicateTable.labels, [...envelopeBaseline.documents
    .find(document_ => document_.outputId === outputId).columns.map(column => column.label)].sort(),
  'the duplicated table lost the persisted column labels');
  assert.ok(Number(duplicateTable.draftVersion) > Number(envelopeBaseline.draftVersion), 'duplicate did not advance the persisted head');

  const panelDuplicateReady = await until(async () => ((await previewReadyFor(duplicateTable.outputId)) ? await panelSnapshot() : null),
    'duplicated table displayed its own current-draft preview', 25000);
  oracleCheck(panelDuplicateReady, 'witness-C-duplicate-context');

  const obsoleteIdentity = { draftVersion: duplicateTable.draftVersion, draftDigest: duplicateTable.draftDigest };
  const obsoleteCompileRequests = () => report.wire.reconcileRequests
    .filter(entry => entry.body?.draftVersion === obsoleteIdentity.draftVersion
      && entry.body?.draftDigest === obsoleteIdentity.draftDigest && !entry.body?.outputId);
  assert.deepEqual(obsoleteCompileRequests(), [], 'an ownerless whole-workspace compile was already outstanding before Witness C armed its hold');

  holdCExpected = { draftVersion: duplicateTable.draftVersion, outputId: undefined };
  holdCArmed = true;
  await click('button[aria-controls="dataset-review-panel"]');
  await until(() => holdC !== null, 'Review dataset affordance issued the ownerless whole-workspace reconcile and it was captured at the Fetch Request stage', 12000);
  holdCArmed = false;
  assert.equal(holdC.body.outputId ?? null, null, 'Witness C held a scoped compile instead of the ownerless whole-workspace compile');
  assert.equal(holdC.body.draftVersion, obsoleteIdentity.draftVersion, 'held compile does not cover the committed head');
  assert.equal(holdC.body.draftDigest, obsoleteIdentity.draftDigest, 'held compile carries a different head digest');
  assert.equal(holdC.body.snapshotToken, report.seed.snapshotToken, 'held compile is not bound to the persisted catalog snapshot');
  const obsoleteRequestsAtHold = obsoleteCompileRequests().length;
  const envelopeAtObsoleteHead = await envelopeTruth('witness C committed head the late request answers');
  assert.equal(envelopeAtObsoleteHead.draftVersion, obsoleteIdentity.draftVersion, 'the held compile does not match the persisted head version');
  assert.equal(envelopeAtObsoleteHead.draftDigest, obsoleteIdentity.draftDigest, 'the held compile does not match the persisted head digest');
  report.witnessC.held = report.holdC();
  report.witnessC.duplicate = { ...duplicateTable, sourceOutputId: outputId };
  passed('witness-C-ownerless-compile-held-un-aborted-for-committed-head', {
    heldRequestId: holdC.requestId,
    held: report.holdC(),
    duplicateTable,
    obsoleteCompileRequests: obsoleteRequestsAtHold,
    panelWhileHeld: await panelSnapshot(),
    note: 'No command wave, Explorer switch, or Explorer deletion happens next, so no activeCompile?.abort() call site is reachable; the pending mutation fetch stays alive at the transport layer',
  });

  const panelJustBeforeDetach = await panelSnapshot();
  assert.ok(panelJustBeforeDetach.previewReceiptId && panelJustBeforeDetach.headers,
    `displayed preview disappeared before detach: ${JSON.stringify(panelJustBeforeDetach)}`);
  const heldDisplayKeys = snapshot => ({
    previewOutputId: snapshot.previewOutputId,
    previewCurrentDraftVersion: snapshot.previewCurrentDraftVersion,
    previewCurrentDraftDigest: snapshot.previewCurrentDraftDigest,
    headers: snapshot.headers,
    rows: snapshot.rows,
    ariaRowCount: snapshot.ariaRowCount,
    mismatchText: snapshot.mismatchText,
  });
  report.witnessC.displayedBeforeDetach = panelJustBeforeDetach;
  const detachedBeforeRelease = await (async () => {
    await clickDemoControl('Viewer');
    await until(() => evaluate(`!document.querySelector('[data-testid="construction-preview"]') && !document.querySelector('[data-testid="construction-workspace"]')`)
      .catch(() => false), 'Builder context detached (Viewer context mounted, no document navigation)', 15000);
    holdC.detachedAtMs = nowMs();
    await until(() => evaluate(`!document.body.innerText.includes('Loading published Explorer')`).catch(() => false),
      'replaced context finished its own load', 5000).catch(() => undefined);
    return await detachedSnapshot();
  })();
  await screenshot('witness-c.png');

  const canceledForHeldRequest = () => report.canceledNetwork.filter(entry => entry.requestId === holdC.requestId);
  holdC.releasedAtMs = nowMs();
  await client.send('Fetch.continueRequest', { requestId: holdC.requestId }).catch(error => report.interceptErrors.push({ requestId: holdC.requestId, phase: 'releaseC', error: error.message }));
  const delivered = await until(
    () => report.wire.reconcileResponses.find(entry => (holdC.networkId && entry.requestId === holdC.networkId)
      || (entry.requestId && entry.requestId === holdC.requestId)
      || (entry.url === holdC.url && entry.status !== null && entry.atMs >= holdC.releasedAtMs)),
    'released obsolete reconcile received a real HTTP response at the wire after detach', 15000);
  assert.ok(delivered.status !== null, 'the obsolete reconcile response arrived without an HTTP status; late delivery is unproven');
  assert.deepEqual(canceledForHeldRequest(), [], 'the obsolete reconcile was transport-cancelled instead of late-delivered; this browser path cannot late-deliver');
  const ownerlessOutputIds = [outputId, duplicateTable.outputId];
  const attribution = {
    mode: holdC.networkId ? (delivered.requestId === holdC.networkId ? 'networkId-equality' : 'none')
      : 'ownerless-response-identity',
    holdNetworkId: holdC.networkId ?? null,
    wireRequestId: delivered.requestId ?? null,
    deliveredRequestIdEqualsHoldInterceptionId: delivered.requestId === holdC.requestId,
    responseCoversBothWorkspaceOutputs: ownerlessOutputIds.every(id => (delivered.outputIds ?? []).includes(id)),
    responseIsTheOnlyOneCoveringBothOutputs: report.wire.reconcileResponses.filter(entry => (entry.outputIds ?? []).length > 1).every(entry => entry === delivered),
    canceledForHeldRequestId: canceledForHeldRequest().length,
  };
  assert.ok(attribution.mode === 'networkId-equality'
    || (attribution.responseCoversBothWorkspaceOutputs && attribution.responseIsTheOnlyOneCoveringBothOutputs && attribution.canceledForHeldRequestId === 0),
    `the late response cannot be attributed to the held ownerless request: ${JSON.stringify(attribution)}`);
  holdC.deliveryMode = 'late-delivered-real-response';
  holdC.response = delivered;
  report.wire.reconcileResponses.find(entry => entry === delivered).heldWasLate = true;
  report.witnessC.response = {
    status: delivered.status, receiptId: delivered.receiptId, kind: delivered.kind, code: delivered.code,
    intentDigest: delivered.intentDigest, outputIds: delivered.outputIds, headLabels: delivered.headLabels,
    requestIdMatch: delivered.requestId === (holdC.networkId ?? holdC.requestId), wireRequestId: delivered.requestId, atMs: delivered.atMs,
    attribution,
    arrivedAfterDetachMs: delivered.atMs - holdC.detachedAtMs,
  };
  const lateReceiptId = delivered.receiptId ?? null;
  passed('witness-C-late-real-response-delivered-after-detach', {
    deliveryMode: holdC.deliveryMode,
    attribution,
    request: report.holdC(),
    canceledForHeldRequest: canceledForHeldRequest(),
    note: 'The production client request reached the production Go handler only after its owning Builder context detached',
  });

  await wait(2000);
  const detachedAfterRelease = await detachedSnapshot();
  const bodyMentionsLateReceipt = lateReceiptId
    ? await evaluate(`document.body.innerText.includes(${JSON.stringify(lateReceiptId)})`) : false;
  const obsoleteTextMarkers = [lateReceiptId, obsoleteIdentity.draftDigest, 'MISMATCH', 'stale draft', `draftVersion=${obsoleteIdentity.draftVersion}`]
    .filter(Boolean)
    .map(needle => ({ needle, inStatus: detachedAfterRelease.statusTexts.some(text => text.includes(needle)), inAlert: detachedAfterRelease.alertTexts.some(text => text.includes(needle)) }));
  const structuralBefore = snapshot => ({
    builderWorkspacePresent: snapshot.builderWorkspacePresent,
    constructionPreviewPresent: snapshot.constructionPreviewPresent,
    previewTablePresent: snapshot.previewTablePresent,
    previewHeaders: snapshot.previewHeaders,
    previewRows: snapshot.previewRows,
    receiptBearingNodes: snapshot.receiptBearingNodes,
    bodyHasObsoleteMarkers: snapshot.bodyHasObsoleteMarkers,
  });
  assert.deepEqual(structuralBefore(detachedAfterRelease), structuralBefore(detachedBeforeRelease),
    `displayed state moved when the obsolete response arrived late: before ${JSON.stringify(structuralBefore(detachedBeforeRelease))} after ${JSON.stringify(structuralBefore(detachedAfterRelease))}`);
  assert.deepEqual(detachedAfterRelease.receiptBearingNodes, [], 'a receipt or draft identity surfaced while the owning context was detached');
  assert.ok(!detachedAfterRelease.previewTablePresent && !detachedAfterRelease.previewRows, 'preview rows or a table rendered for the obsolete late response');
  assert.ok(!detachedAfterRelease.bodyHasObsoleteMarkers, 'the detached page surfaced a mismatch or stale-draft banner');
  assert.equal(bodyMentionsLateReceipt, false, 'the late receipt identity appeared in the detached page text');
  assert.deepEqual(obsoleteTextMarkers.filter(marker => marker.inStatus || marker.inAlert), [],
    `the detached page attributed work to the obsolete response: ${JSON.stringify(obsoleteTextMarkers)}`);
  await screenshot('witness-c-post-release.png');

  const reconcileRequestsAfterRelease = report.wire.reconcileRequests.filter(entry => entry.atMs >= holdC.releasedAtMs);
  const previewResponsesForLateReceipt = report.wire.previewResponses
    .filter(entry => lateReceiptId && entry.receiptId === lateReceiptId && entry.atMs >= holdC.releasedAtMs);
  assert.equal(obsoleteCompileRequests().length, obsoleteRequestsAtHold,
    'the obsolete whole-workspace identity was re-issued after release (compensating retry storm)');
  assert.deepEqual(reconcileRequestsAfterRelease, [], 'a further reconcile wire request appeared while the obsolete response arrived late');
  assert.deepEqual(previewResponsesForLateReceipt, [], 'the late receipt was executed by a preview request');
  passed('witness-C-detached-display-adopted-nothing-without-retry-storm', {
    deliveredBeforeRelease: structuralBefore(detachedBeforeRelease),
    deliveredAfterRelease: structuralBefore(detachedAfterRelease),
    statusTextsBeforeRelease: detachedBeforeRelease.statusTexts,
    statusTextsAfterRelease: detachedAfterRelease.statusTexts,
    alertTextsBeforeRelease: detachedBeforeRelease.alertTexts,
    alertTextsAfterRelease: detachedAfterRelease.alertTexts,
    obsoleteTextMarkers,
    obsoleteCompileRequestCountAfterSettle: obsoleteCompileRequests().length,
    reconcileRequestsAfterRelease: reconcileRequestsAfterRelease.length,
    previewResponsesForLateReceipt: previewResponsesForLateReceipt.length,
    settleWindowMs: 2000,
  });

  const envelopeAfterRelease = await envelopeTruth('witness C after obsolete late response');
  assert.deepEqual(envelopeAfterRelease, envelopeAtObsoleteHead,
    `the obsolete late request moved persisted Builder truth: before ${JSON.stringify(envelopeAtObsoleteHead)} after ${JSON.stringify(envelopeAfterRelease)}`);
  passed('witness-C-persisted-builder-envelope-unchanged-by-late-request', {
    head: { draftVersion: envelopeAfterRelease.draftVersion, draftDigest: envelopeAfterRelease.draftDigest },
    tableCount: envelopeAfterRelease.tableCount,
    documents: envelopeAfterRelease.documents,
    baselineHead: { draftVersion: envelopeBaseline.draftVersion, draftDigest: envelopeBaseline.draftDigest },
    unchangedAgainstPreReleaseHead: true,
  });

  const remountStartedAt = nowMs();
  await clickDemoControl('Builder');
  await until(() => evaluate(`Boolean(document.querySelector('[data-testid="construction-preview"]'))`).catch(() => false),
    'Builder re-mounted after the detached late response', 20000);
  if ((await selectedTableOutputId()) !== duplicateTable.outputId) {
    await click(`[data-testid="construction-table-${duplicateTable.outputId}"]`, 10000);
  }
  const panelRemounted = await until(async () => ((await previewReadyFor(duplicateTable.outputId)) ? await panelSnapshot() : null),
    're-mounted Builder hydrated a current-draft preview for the persisted duplicated table', 25000);
  const remountIDs = oracleCheck(panelRemounted, 'witness-C-remount');
  assert.equal(String(panelRemounted.previewCurrentDraftVersion), String(duplicateTable.draftVersion), 're-mounted preview is not bound to the persisted head version');
  assert.equal(String(panelRemounted.previewCurrentDraftDigest), String(duplicateTable.draftDigest), 're-mounted preview is not bound to the persisted head digest');
  assert.ok(headersInclude(panelRemounted.headers, labelBeta) && headersInclude(panelRemounted.headers, labelGamma) && headersInclude(panelRemounted.headers, 'Gender'),
    `re-mounted preview lost a persisted label: ${JSON.stringify(panelRemounted.headers)}`);
  assert.deepEqual(heldDisplayKeys(panelRemounted), heldDisplayKeys(panelJustBeforeDetach),
    `re-mounted displayed state differs from the pre-release state: ${JSON.stringify(panelJustBeforeDetach)} vs ${JSON.stringify(panelRemounted)}`);
  const remountReceiptWire = report.wire.reconcileResponses.findLast(entry => entry.receiptId === panelRemounted.previewReceiptId && entry.atMs >= remountStartedAt);
  assert.ok(remountReceiptWire, 'the re-mounted preview displays a receipt the page never requested after re-mount (provenance of the late response is unproven)');
  assert.ok(!panelRemounted.mismatchText, 'the re-mounted page surfaced a mismatch or stale-draft error');
  await screenshot('witness-c-post-remount.png');
  report.witnessC.remount = {
    panel: panelRemounted,
    displayedUnchangedVsPreRelease: true,
    displayedReceiptWireRequestId: remountReceiptWire.requestId ?? null,
    lateReceiptReusedByServer: lateReceiptId === panelRemounted.previewReceiptId,
  };
  passed('witness-C-remount-hydrates-from-persisted-state-and-late-response-owns-nothing', {
    selectedOutputId: duplicateTable.outputId,
    patientIDs: remountIDs,
    headers: panelRemounted.headers,
    previewReceiptId: panelRemounted.previewReceiptId,
    displayedReceiptWireRequestId: remountReceiptWire.requestId ?? null,
    lateReceiptId,
    lateReceiptReusedByServer: lateReceiptId === panelRemounted.previewReceiptId,
    headBinding: { draftVersion: panelRemounted.previewCurrentDraftVersion, draftDigest: panelRemounted.previewCurrentDraftDigest },
    displayedKeysJustBeforeDetach: heldDisplayKeys(panelJustBeforeDetach),
    displayedKeysAfterRemount: heldDisplayKeys(panelRemounted),
    receiptBeforeDetach: panelJustBeforeDetach.previewReceiptId,
  });

  const witnessD = { role: 'server-answered compile withheld at the fetch layer while a competing save persists underneath' };
  report.witnessD = witnessD;
  if ((await selectedTableOutputId()) !== outputId) {
    await click(`[data-testid="construction-table-${outputId}"]`, 10000);
  }
  await until(async () => (await previewReady()), 'primary output preview ready for witness D', 20000);
  const genderBeforeD = await persistedState('gender');
  assert.equal(genderBeforeD.label, 'Gender', 'witness D expected the untouched primary gender label before arming');
  const panelBeforeD = await until(async () => ((await previewReady()) ? await panelSnapshot() : null), 'primary panel snapshot before witness D', 20000);
  holdDArmed = true;
  witnessD.persisted = await renameColumn('gender', 'Gender', labelDelta);
  const dHead = witnessD.persisted;
  assert.equal(Number(dHead.draftVersion), Number(genderBeforeD.draftVersion) + 1, 'witness D save did not advance the persisted head by exactly one');
  await until(() => holdD !== null, 'witness D compile response captured complete at the fetch layer and withheld before the client', 25000);
  holdDArmed = false;
  assert.equal(holdD.response.status, 200, 'the withheld witness D response was not the production handler success');
  assert.ok(holdD.response.receiptId, 'the withheld witness D response carried no receipt identity');
  assert.ok(contractHasLabel(holdD.response.headLabels, labelDelta) && !contractHasLabel(holdD.response.headLabels, labelEpsilon),
    `the withheld response is not bound to the pre-competitor head: ${JSON.stringify(holdD.response.headLabels)}`);
  const panelDuringD = await panelSnapshot();
  assert.notEqual(panelDuringD.previewReceiptId, holdD.response.receiptId, 'the withheld response leaked into the display before release');
  witnessD.held = report.holdD();
  witnessD.panelDuringHold = panelDuringD;
  passed('witness-D-real-compile-withheld-at-fetch-response-stage', {
    held: report.holdD(),
    panelReceiptWhileHeld: panelDuringD.previewReceiptId,
    staleReceipt: holdD.response.receiptId,
    note: 'Fetch Response-stage hold lets the production Go handler answer, then withholds the settled response from the client',
  });
  await screenshot('witness-d-held.png');

  const idBeforeCompete = await persistedState('id');
  assert.equal(idBeforeCompete.label, labelBeta, 'witness D baseline expected the witness B beta id label');
  witnessD.competingPersisted = await renameColumn('id', labelBeta, labelEpsilon);
  const eHead = witnessD.competingPersisted;
  assert.ok(Number(eHead.draftVersion) > Number(dHead.draftVersion), 'the competing save did not advance the head past the held compile');
  assert.notEqual(eHead.draftDigest, dHead.draftDigest);
  const fixtureRowCheck = (snapshot, label) => {
    assert.ok(snapshot.rows && snapshot.rows.length >= 3, `${label}: preview table rendered without the two fixture rows`);
    return assertPatientRows(snapshot.rows.slice(1), oracle.ids, null, snapshot.rows[0]);
  };
  const competingCompile = await until(
    () => report.wire.reconcileResponses.findLast(entry => (entry.outputIds ?? []).length === 1 && entry.status === 200 && entry.atMs >= dHead.persistedAt
      && contractHasLabel(entry.headLabels, labelDelta) && contractHasLabel(entry.headLabels, labelEpsilon) && contractHasLabel(entry.headLabels, labelGamma)),
    'competing-save scoped compile delivered at the wire while the witness D response stayed withheld', 25000);
  assert.notEqual(competingCompile.receiptId, holdD.response.receiptId, 'the competing save reused the withheld witness D receipt');
  let panelCompeting = null;
  const competingDeadline = performance.now() + 25000;
  for (;;) {
    const snapshot = await panelSnapshot();
    panelCompeting = snapshot;
    if (snapshot.previewReceiptId && snapshot.previewReceiptId !== holdD.response.receiptId
      && headersInclude(snapshot.headers, labelEpsilon) && headersInclude(snapshot.headers, labelDelta) && headersInclude(snapshot.headers, labelGamma)) break;
    assert.ok(performance.now() < competingDeadline, `competing-save preview never displayed; last panel: ${JSON.stringify(snapshot)}`);
    await wait(60);
  }
  fixtureRowCheck(panelCompeting, 'witness-D-competing');
  assert.equal(String(panelCompeting.previewCurrentDraftVersion), String(eHead.draftVersion), 'competing preview is not bound to the competing head version');
  witnessD.panelCompeting = panelCompeting;
  passed('witness-D-competing-save-persisted-and-displayed-while-stale-compile-withheld', {
    heldDraftVersion: dHead.draftVersion, heldReceipt: holdD.response.receiptId,
    competingHead: { draftVersion: eHead.draftVersion, draftDigest: eHead.draftDigest },
    competingReceipt: competingCompile.receiptId,
    displayedHeaders: panelCompeting.headers,
  });
  await screenshot('witness-d-competitor.png');

  const staleDReceipt = holdD.response.receiptId;
  const displayReceiptBeforeRelease = panelCompeting.previewReceiptId;
  const stalePreviewAttempts = () => report.wire.previewResponses.filter(entry => entry.receiptId === staleDReceipt);
  assert.deepEqual(stalePreviewAttempts(), [], 'the withheld witness D receipt got executed by a preview while still held');
  const versionDReconcileCount = () => report.wire.reconcileRequests.filter(entry => Number(entry.body?.draftVersion) === Number(dHead.draftVersion)).length;
  const versionDReconcileAtRelease = versionDReconcileCount();
  holdD.releasedAtMs = nowMs();
  await client.send('Fetch.continueResponse', { requestId: holdD.requestId }).catch(error => report.interceptErrors.push({ requestId: holdD.requestId, phase: 'releaseD', error: error.message }));
  holdD.deliveryMode = 'response-forwarded-from-hold';
  const canceledForD = () => report.canceledNetwork.filter(entry => entry.requestId === holdD.requestId || (holdD.networkId != null && entry.requestId === holdD.networkId));
  if (canceledForD().length > 0) holdD.deliveryMode = 'client-cancelled-before-delivery';
  await wait(700);
  const panelAfterD = await until(async () => ((await previewReady()) ? await panelSnapshot() : null), 'primary preview settled after witness D release', 20000);
  assert.equal(panelAfterD.previewReceiptId, displayReceiptBeforeRelease, 'the stale witness D response was adopted by the page after release');
  assert.equal(String(panelAfterD.previewCurrentDraftVersion), String(eHead.draftVersion), 'preview head identity moved off the competing head after the stale release');
  assert.deepEqual(panelAfterD.headers, panelCompeting.headers, 'displayed headers moved when the stale response arrived');
  assert.ok(!panelAfterD.mismatchText, 'page surfaced a mismatch or stale-draft error after the stale release');
  assert.equal(stalePreviewAttempts().length, 0, 'the stale witness D receipt was executed by a preview after release');
  assert.equal(versionDReconcileCount(), versionDReconcileAtRelease, 'the stale witness D identity was re-issued after release (compensating retry storm)');
  const settledD = await persistedState('gender');
  assert.ok(Number(settledD.draftVersion) >= Number(eHead.draftVersion) && settledD.label === labelDelta, 'persisted state moved when the stale response was released');
  passed('witness-D-withheld-stale-compile-never-adopted', {
    deliveryMode: holdD.deliveryMode,
    staleReceipt: staleDReceipt,
    displayedReceiptStable: displayReceiptBeforeRelease === panelAfterD.previewReceiptId,
    persistedHead: { draftVersion: settledD.draftVersion, label: settledD.label },
    stalePreviewAttempts: 0,
    versionDReconcileRequests: versionDReconcileCount(),
  });
  await screenshot('witness-d-post-release.png');

  const witnessE = { role: 'settled current-attempt preview withheld at the fetch layer, adopted on release' };
  report.witnessE = witnessE;
  const familyBeforeE = await persistedState('name[].family');
  assert.equal(familyBeforeE.label, labelGamma, 'witness E baseline expected the witness B gamma family label');
  holdEBaselineReceipt = displayReceiptBeforeRelease;
  holdEArmAtMs = nowMs();
  holdEArmed = true;
  witnessE.persisted = await renameColumn('name[].family', labelGamma, labelZeta);
  const zHead = witnessE.persisted;
  await until(() => holdE !== null, 'witness E preview response captured complete at the fetch layer and withheld before the client', 25000);
  holdEArmed = false;
  assert.equal(holdE.response.status, 200, 'the withheld witness E preview response was not a success');
  assert.ok(holdE.response.receiptId && holdE.response.receiptId !== holdEBaselineReceipt && holdE.response.receiptId !== staleDReceipt,
    'the withheld preview response is not bound to a fresh receipt distinct from baseline and stale eras');
  assert.ok(contractHasLabel(holdE.response.columns, labelZeta), `withheld preview columns lost the saved label: ${JSON.stringify(holdE.response.columns)}`);
  assert.ok(oracle.ids.some(id => JSON.stringify(holdE.response.rowsSample).includes(id)), 'the settled preview response carried no fixture oracle row');
  const panelBeforeE = await panelSnapshot();
  assert.notEqual(panelBeforeE.previewReceiptId, holdE.response.receiptId, 'the withheld preview leaked to the display before release');
  assert.ok(!headersInclude(panelBeforeE.headers ?? [], labelZeta), 'zeta headers displayed before the preview delivery');
  witnessE.panelBeforeRelease = panelBeforeE;
  witnessE.held = report.holdE();
  holdE.releasedAtMs = nowMs();
  await client.send('Fetch.continueResponse', { requestId: holdE.requestId }).catch(error => report.interceptErrors.push({ requestId: holdE.requestId, phase: 'releaseE', error: error.message }));
  holdE.deliveryMode = 'response-forwarded-from-hold';
  const canceledForE = () => report.canceledNetwork.filter(entry => entry.requestId === holdE.requestId || (holdE.networkId != null && entry.requestId === holdE.networkId));
  if (canceledForE().length > 0) holdE.deliveryMode = 'client-cancelled-before-delivery';
  const panelAdopted = await until(async () => {
    if (!(await previewReady())) return null;
    const snapshot = await panelSnapshot();
    return snapshot.previewReceiptId === holdE.response.receiptId && headersInclude(snapshot.headers ?? [], labelZeta) ? snapshot : null;
  }, 'page adopted the released preview behind its own receipt', 20000).catch(async error => {
    throw new Error(`${error.message}; holdE=${JSON.stringify(report.holdE())}`);
  });
  const eIDs = fixtureRowCheck(panelAdopted, 'witness-E');
  assert.equal(String(panelAdopted.previewCurrentDraftVersion), String(zHead.draftVersion), 'the adopted preview is not bound to the current head version');
  assert.ok(!panelAdopted.mismatchText, 'page surfaced a mismatch after adopting the released current-attempt preview');
  const settledE = await persistedState('name[].family');
  assert.equal(settledE.label, labelZeta);
  assert.equal(Number(settledE.draftVersion), Number(zHead.draftVersion), 'persisted head moved while the current-attempt preview was withheld or adopted');
  passed('witness-E-current-attempt-preview-adopted-on-release', {
    deliveryMode: holdE.deliveryMode,
    baselineReceipt: holdEBaselineReceipt,
    adoptedReceipt: holdE.response.receiptId,
    head: { draftVersion: zHead.draftVersion, draftDigest: zHead.draftDigest },
    displayedHeaders: panelAdopted.headers,
    patientIDs: eIDs,
  });
  await screenshot('witness-e.png');

  const m5 = { reload: null, deletions: [], restore: null };
  report.m5 = m5;
  const m5Truth = async observation => {
    const state = await api(`${authoringPath}/builder`, undefined, 8000);
    const documents = state?.workspace?.documents ?? [];
    report.probes.push({ method: 'GET', path: `${authoringPath}/builder`, status: 200, observation });
    return {
      draftVersion: Number(state.draftVersion),
      draftDigest: state.draftDigest,
      previousDraftRevisionId: state.previousDraftRevisionId ?? null,
      documents: documents.map(document_ => ({
        outputId: document_.output?.id ?? null,
        title: document_.output?.title ?? null,
        columns: (document_.columns ?? []).map(column => ({ path: column.source?.field?.path ?? '?', label: column.label ?? null })),
      })),
    };
  };

  const preReload = await m5Truth('m5 persisted truth before the user reload');
  await screenshot('m5-before-reload.png');
  await send('Page.reload', { ignoreCache: true });
  await until(() => evaluate('Boolean(document.querySelector(\'[data-testid="construction-preview"]\'))').catch(() => false),
    'Builder mounted from a fresh document after reload', 30000);
  const reloadedPanel = await until(async () => {
    if (!(await previewReadyAny())) return null;
    const snapshot = await panelSnapshot();
    return String(snapshot.workspaceDraftVersion ?? '') === String(preReload.draftVersion) && snapshot.previewReceiptId ? snapshot : null;
  }, 'reloaded display bound to the persisted head', 30000);
  const reloadedDocument = preReload.documents.find(document_ => document_.outputId === reloadedPanel.previewOutputId) ?? null;
  assert.ok(reloadedDocument, `the reloaded preview bound to an output absent from persisted state: ${reloadedPanel.previewOutputId}`);
  for (const column of reloadedDocument.columns) {
    if (column.label) assert.ok(headersInclude(reloadedPanel.headers ?? [], column.label), `the fresh-document display lost the persisted label ${column.label}`);
  }
  const reloadedIDs = fixtureRowCheck(reloadedPanel, 'm5-reload');
  assert.equal(String(reloadedPanel.previewCurrentDraftVersion), String(preReload.draftVersion), 'the preview after the document reload is not bound to the persisted head version');
  assert.ok(!reloadedPanel.mismatchText, 'the reloaded page surfaced a mismatch');
  await screenshot('m5-after-reload.png');
  m5.reload = {
    persistedHead: { draftVersion: preReload.draftVersion, draftDigest: preReload.draftDigest },
    displayed: {
      workspaceDraftVersion: reloadedPanel.workspaceDraftVersion,
      workspaceDraftDigest: reloadedPanel.workspaceDraftDigest,
      previewReceiptId: reloadedPanel.previewReceiptId,
      previewOutputId: reloadedPanel.previewOutputId,
    },
    displayedHeaders: reloadedPanel.headers,
    patientIDs: reloadedIDs,
  };
  passed('m5-fresh-document-reload-hydrates-from-persisted-state', m5.reload);

  const beforeDeletes = await m5Truth('m5 persisted truth before the native table deletions');
  assert.equal(beforeDeletes.documents.length, 2, 'the restore journey expects the seeded table plus the witness C duplicate');
  const deletionHeads = [];
  m5Dialogs.active = true;
  for (const document_ of beforeDeletes.documents) {
    const versionBefore = deletionHeads.length === 0 ? preReload.draftVersion : deletionHeads[deletionHeads.length - 1].draftVersion;
    await click(`[data-testid="construction-table-${document_.outputId}"]`, 10000);
    await click(`[data-testid="construction-table-actions-${document_.outputId}"]`);
    await until(() => evaluate(`(() => { const menu = document.querySelector('[data-testid="construction-table-actions-menu"]'); if (!menu) return false; const style = menu.getAttribute('style') ?? ''; return /top:/.test(style) && !/visibility:\s*hidden/.test(style); })()`).catch(() => false),
      'table actions menu positioned for the delete', 5000);
    await click('[data-testid="construction-delete-table"]');
    const head = await until(async () => {
      const truth = await m5Truth(`m5 persisted head while deleting table ${document_.outputId}`);
      return truth.draftVersion > versionBefore && !truth.documents.some(candidate => candidate.outputId === document_.outputId) ? truth : null;
    }, `native table delete persisted for ${document_.outputId}`, 20000).catch(async error => {
      throw new Error(`${error.message}; dialogs=${JSON.stringify(report.dialogs.slice(-3))}`);
    });
    deletionHeads.push({ outputId: document_.outputId, draftVersion: head.draftVersion, draftDigest: head.draftDigest });
  }
  m5.deletions = deletionHeads;
  const emptyTruth = await m5Truth('m5 persisted truth after the native deletions');
  assert.equal(emptyTruth.documents.length, 0, 'the native deletes did not empty the persisted workspace');
  assert.ok(Number(emptyTruth.draftVersion) > Number(preReload.draftVersion), 'the native deletes did not advance the persisted head');
  await until(() => evaluate(`(() => { const button = document.querySelector('[data-testid="construction-undo"]'); return Boolean(button) && !button.disabled; })()`).catch(() => false),
    'native Undo control mounted and enabled over the empty-workspace panel', 15000);
  await screenshot('m5-empty-undo.png');
  const commandsBeforeUndo = report.wire.commandResponses.length;
  await click('[data-testid="construction-undo"]');
  const lastDeletion = beforeDeletes.documents[beforeDeletes.documents.length - 1];
  const restored = await until(async () => {
    const truth = await m5Truth('m5 persisted head during the native Undo restore');
    if (truth.draftVersion <= emptyTruth.draftVersion) return null;
    if (truth.documents.length !== 1 || truth.documents[0].outputId !== lastDeletion.outputId) return null;
    return truth;
  }, 'the Undo control restored the last-deleted table as a new persisted head', 25000);
  const restoreAck = report.wire.commandResponses.slice(commandsBeforeUndo).find(entry => entry.status === 200 && (entry.resultTypes ?? []).includes('DRAFT_RESTORED')) ?? null;
  assert.ok(restoreAck, `the Undo click issued no successful DRAFT_RESTORED command at the wire: ${JSON.stringify(report.wire.commandResponses.slice(commandsBeforeUndo))}`);
  assert.equal(Number(restoreAck.headVersion), restored.draftVersion, 'the restore ack head disagrees with the persisted head');
  const restoredLabels = restored.documents[0].columns.map(column => column.label).filter(Boolean);
  assert.ok(restoredLabels.length === restored.documents[0].columns.length && restoredLabels.length > 0, `the restored persisted document carries unnamed columns: ${JSON.stringify(restored.documents[0].columns)}`);
  for (const column of beforeDeletes.documents.find(document_ => document_.outputId === lastDeletion.outputId).columns) {
    assert.ok(restored.documents[0].columns.some(candidate => candidate.path === column.path && candidate.label === column.label),
      `the restored persisted document lost column ${JSON.stringify(column)}`);
  }
  const restoredPanel = await until(async () => {
    if (!(await previewReadyFor(lastDeletion.outputId))) return null;
    const snapshot = await panelSnapshot();
    if (!snapshot.previewReceiptId || snapshot.previewOutputId !== lastDeletion.outputId) return null;
    if (String(snapshot.previewCurrentDraftVersion ?? '') !== String(restored.draftVersion)) return null;
    if (String(snapshot.workspaceDraftVersion ?? '') !== String(restored.draftVersion)) return null;
    return restoredLabels.every(label => headersInclude(snapshot.headers ?? [], label)) ? snapshot : null;
  }, 'the restored table displayed its own current-head preview', 30000);
  const restoredIDs = fixtureRowCheck(restoredPanel, 'm5-restore');
  assert.ok(!restoredPanel.mismatchText, 'the restored page surfaced a mismatch');
  const replayedRestore = await api(`${authoringPath}/preview`, { receiptId: restoredPanel.previewReceiptId, outputId: lastDeletion.outputId, limit: 50 });
  assert.equal(replayedRestore.receiptId, restoredPanel.previewReceiptId, 'the restored displayed receipt does not re-execute by identity over the real wire');
  assert.ok((replayedRestore.columns ?? []).some(column => restoredLabels.includes(column.label)), 'the re-executed restored receipt lost the saved labels');
  await screenshot('m5-restored.png');
  m5.restore = {
    restoredOutputId: lastDeletion.outputId,
    headAfterDeletes: { draftVersion: emptyTruth.draftVersion, draftDigest: emptyTruth.draftDigest },
    restoredHead: { draftVersion: restored.draftVersion, draftDigest: restored.draftDigest, previousDraftRevisionId: restored.previousDraftRevisionId },
    restoreAck: { status: restoreAck.status, headVersion: restoreAck.headVersion, resultTypes: restoreAck.resultTypes },
    displayed: {
      workspaceDraftVersion: restoredPanel.workspaceDraftVersion,
      previewReceiptId: restoredPanel.previewReceiptId,
      previewOutputId: restoredPanel.previewOutputId,
    },
    displayedHeaders: restoredPanel.headers,
    patientIDs: restoredIDs,
    receiptReExecuted: { receiptId: replayedRestore.receiptId, rowCount: replayedRestore.rowCount },
  };
  passed('m5-native-undo-restores-persisted-head', m5.restore);
  passed('m5-restored-receipt-re-executable-over-real-wire', m5.restore.receiptReExecuted);
  m5Dialogs.active = false;

  const unexpectedDialogs = report.dialogs.filter(entry => !(entry.phase === 'm5-native-journey' && entry.type === 'confirm' && entry.accepted));
  assert.deepEqual(unexpectedDialogs, [], `Unexpected JavaScript dialogs interrupted the joined flow: ${JSON.stringify(unexpectedDialogs)}`);
  assert.deepEqual(report.errors, [], `Browser runtime exceptions interrupted the joined flow: ${JSON.stringify(report.errors)}`);
  assert.deepEqual(report.netFailures, [], `Network failures interrupted the joined flow: ${JSON.stringify(report.netFailures)}`);
  await screenshot('final.png');
  passed('joined-browser-session-clean', {
    dialogsTotal: report.dialogs.length,
    dialogsUnexpected: unexpectedDialogs.length,
    dialogsAcceptedM5Confirms: report.dialogs.length - unexpectedDialogs.length,
    exceptions: 0,
    netFailures: 0,
    canceledRequests: report.canceledNetwork.length,
    interceptErrors: report.interceptErrors.length,
  });
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = { message: error.message, stack: error.stack };
  if (client) {
    try {
      const image = await withTimeout(client.send('Page.captureScreenshot', { format: 'png' }), 5000, 'failure screenshot');
      await writeFile(join(out, 'failure.png'), Buffer.from(image.data, 'base64'));
      report.screenshots.push('failure.png');
    } catch { }
  }
  process.exitCode = 1;
} finally {
  clearTimeout(watchdog);
  try { client?.close(); } catch { }
  if (chrome && chrome.exitCode === null && chrome.signalCode === null) {
    chrome.kill('SIGTERM');
    await Promise.race([new Promise(resolveExit => chrome.once('exit', resolveExit)), wait(1000)]);
    if (chrome.exitCode === null && chrome.signalCode === null) {
      const stopped = new Promise(resolveExit => chrome.once('exit', resolveExit));
      chrome.kill('SIGKILL');
      await stopped;
    }
  }
  if (temp) await rm(temp, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 }).catch(error => { report.cleanupError = error.message; });
  if (typeof report.holdC === 'function') report.holdC = report.holdC();
  if (report.holdC && report.holdC.deliveryMode === null) {
    report.holdC = { ...report.holdC, deliveryMode: report.holdC.releasedAtMs === null ? 'never-released-run-aborted' : 'released-without-observed-delivery' };
  }
  for (const key of ['holdD', 'holdE']) {
    if (typeof report[key] === 'function') report[key] = report[key]();
    if (report[key] && report[key].deliveryMode === null) {
      report[key] = { ...report[key], deliveryMode: report[key].releasedAtMs === null ? 'never-released-run-aborted' : 'released-without-observed-delivery' };
    }
  }
  if (typeof report.hold === 'function') report.hold = report.hold();
  if (typeof report.holdC === 'function') report.holdC = report.holdC();
  report.timingMs = { total: nowMs(), budget: options.budgetMs };
  let otherExplorersTouched;
  try {
    const listPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
    const fingerprintRows = rows => rows.map(row => JSON.stringify([row.explorerId, row.updatedAt, row.title, row.management]));
    const after = await api(listPath, undefined, 5000);
    const beforeRows = report.explorerLedger?.before ?? [];
    const beforeKeys = new Map(beforeRows.map(row => [row.explorerId, fingerprintRows([row])[0]]));
    const afterIds = new Set(after.map(row => row.explorerId));
    otherExplorersTouched = after.some(row => row.explorerId !== explorerId && beforeKeys.get(row.explorerId) !== fingerprintRows([row])[0])
      || [...beforeKeys.keys()].some(id => id !== explorerId && !afterIds.has(id));
    report.explorerLedger = report.explorerLedger ?? {};
    report.explorerLedger.after = after.map(row => ({ explorerId: row.explorerId, updatedAt: row.updatedAt, title: row.title, management: row.management }));
  } catch (error) {
    otherExplorersTouched = `ledger-unavailable: ${error.message}`;
  }
  report.cleanup = {
    chromiumStopped: !chrome || chrome.exitCode !== null || chrome.signalCode !== null,
    temporaryProfileRemoved: !temp || !existsSync(temp),
    runOwnedExplorer: explorerId,
    otherExplorersTouched,
  };
  const cleanupViolations = [
    ...(!report.cleanup.chromiumStopped ? ['chromium-not-stopped'] : []),
    ...(!report.cleanup.temporaryProfileRemoved ? ['temporary-profile-remains'] : []),
    ...(otherExplorersTouched !== false ? [`other-explorers-touched:${otherExplorersTouched}`] : []),
  ];
  report.checks.push({ name: 'cleanup-own-only', status: cleanupViolations.length === 0 ? 'passed' : 'failed', evidence: { runOwnedExplorer: explorerId, otherExplorersTouched, violations: cleanupViolations } });
  if (cleanupViolations.length > 0 && report.status === 'passed') {
    report.status = 'failed';
    report.error = { message: `cleanup violations: ${cleanupViolations.join('; ')}`, stack: '' };
    process.exitCode = 1;
  }
  await writeFile(join(out, 'browser.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(finishSummary(), null, 2));
}
if (report.status === 'failed') process.exit(1);
