import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import vm from 'node:vm';
import { buildInventory, inventoryCoverageDrift, scanTypeScriptSource } from '../inventory.mjs';
import { booleanPredicate, boundCDPCommands, browserInitSource, click, evaluate, inspectAction, injectReadFaultOnce, networkRecordKind, safeURL, startBrowser, waitFor, waitForCDPEvent } from '../browser.mjs';
import { browserURL } from '../common.mjs';
import { classifyNetworkRecord, createReport, finishReport, isActionable, recordCheck, recordUntested } from '../report.mjs';
import { getScenario, requiredChecksFor } from '../registry.mjs';

test('network classification keeps injected transport faults separate from browser and server failures', () => {
  assert.equal(classifyNetworkRecord({ injectedFault: true, errorText: 'net::ERR_FAILED' }), 'expected-injected');
  assert.equal(classifyNetworkRecord({ injectedFault: true, status: 500 }), 'unexpected-error');
  assert.equal(classifyNetworkRecord({ injectedFault: true, status: 422, injectedStatus: 422 }), 'expected-injected');
  assert.equal(classifyNetworkRecord({ injectedFault: true, status: 422 }), 'unexpected-error');
  assert.equal(classifyNetworkRecord({ injectedFault: true, status: 422, injectedStatus: 422, internalError: true }), 'unexpected-error');
  assert.equal(classifyNetworkRecord({ injectedFault: true, internalError: true }), 'unexpected-error');
  assert.equal(classifyNetworkRecord({ kind: 'exception', injectedFault: true, errorText: 'net::ERR_FAILED' }), 'unexpected-error');
  assert.equal(classifyNetworkRecord({ errorText: 'net::ERR_ABORTED', canceled: true }), 'cancelled');
  assert.equal(classifyNetworkRecord({ errorText: 'net::ERR_CONNECTION_RESET' }), 'unexpected-error');
  assert.equal(classifyNetworkRecord({ kind: 'asset-failure', status: 404, url: 'http://localhost/favicon.ico' }), 'incidental-asset');
  assert.equal(networkRecordKind({ url: 'http://localhost/favicon.ico', status: 404 }), 'asset-failure');
  assert.equal(networkRecordKind({ url: 'http://localhost/api/missing', status: 404 }), 'network');
  assert.equal(networkRecordKind({ url: 'http://localhost/api/favicon.ico', status: 404 }), 'network');
  assert.equal(classifyNetworkRecord({ kind: 'asset-failure', internalError: true }), 'unexpected-error');
  assert.equal(safeURL('data:text/html;base64,secret?token=secret'), 'data:[redacted]');
  assert.equal(safeURL('https://user:password@example.test/path?token=secret'), 'https://example.test/path');
});

test('a report with only untested coverage cannot pass, and any failed required check fails it', () => {
  const empty = createReport({ scenario: 'unit', target: {}, evidenceDirectory: '/tmp' });
  recordUntested(empty, 'usability', 'not exercised', 'fixture');
  assert.equal(finishReport(empty).status, 'untested');
  const failed = createReport({ scenario: 'unit', target: {}, evidenceDirectory: '/tmp' });
  recordCheck(failed, 'usability', 'retry exists', false, { found: false });
  assert.equal(finishReport(failed).status, 'failed');
  const mixed = createReport({ scenario: 'unit', target: {}, evidenceDirectory: '/tmp' });
  recordCheck(mixed, 'performance', 'manual preview', true);
  recordUntested(mixed, 'performance', 'automatic preview', 'no automatic contract');
  assert.ok(mixed.dimensions.performance.evidence.some((entry) => entry.name === 'automatic preview' && entry.status === 'untested'));
});

test('a passing setup assertion cannot hide a missing required path outcome', () => {
  const report = createReport({ scenario: 'builder', caseName: 'load', target: {}, evidenceDirectory: '/tmp', requiredChecks: ['Builder recovered through Retry'] });
  recordCheck(report, 'correctness', 'fixture is ready', true);
  assert.equal(finishReport(report).status, 'partial');
  assert.deepEqual(report.missingRequiredChecks, ['Builder recovered through Retry']);
  recordCheck(report, 'persistence', 'Builder recovered through Retry', true);
  assert.equal(finishReport(report).status, 'passed');
  const unregistered = createReport({ scenario: 'builder', caseName: 'new-path', target: {}, evidenceDirectory: '/tmp' });
  recordCheck(unregistered, 'correctness', 'fixture is ready', true);
  assert.equal(finishReport(unregistered).status, 'partial');
});

test('an unexpected API failure defeats an otherwise passing browser case', () => {
  const report = createReport({ scenario: 'viewer', caseName: 'query', target: {}, evidenceDirectory: '/tmp', requiredChecks: ['rows rendered'] });
  recordCheck(report, 'correctness', 'rows rendered', true);
  report.network.push({ kind: 'network', status: 500, url: 'http://localhost/graphql/graph' });
  assert.equal(finishReport(report).status, 'failed');
  assert.ok(report.assertions.some((assertion) => assertion.name === 'no unexpected network, module, or browser errors' && assertion.status === 'failed'));
});

test('Viewer requirements follow the chosen fixture target', () => {
  const scenario = getScenario('viewer-query');
  assert.ok(requiredChecksFor(scenario, 'output').includes('retried Viewer results contain both independent fixture Patients'));
  assert.equal(requiredChecksFor(scenario, 'output').includes('Gender facet returns the matching fixture Patient only'), false);
  assert.equal(scenario.coverage.find((entry) => entry.feature === 'facet checkbox and filtered query')?.status, 'untested');
  assert.ok(requiredChecksFor(scenario, 'output', true).includes('Retry restored the custom output table'));
  assert.equal(requiredChecksFor(scenario, 'output', true).includes('Gender facet returns the matching fixture Patient only'), false);
});

test('actionability requires visibility, enabled state, pointer reception, and no pointer-events gate', () => {
  const base = { visible: true, disabled: false, ariaDisabled: null, pointerEvents: 'auto', receivesPointer: true };
  assert.equal(isActionable(base), true);
  assert.equal(isActionable({ ...base, receivesPointer: false }), false);
  assert.equal(isActionable({ ...base, pointerEvents: 'none' }), false);
  assert.equal(isActionable({ ...base, ariaDisabled: 'true' }), false);
  assert.equal(isActionable({ ...base, disabled: true }), false);
  assert.equal(isActionable({ ...base, visible: false }), false);
});

test('the injected startup observer source is valid JavaScript', () => {
  assert.doesNotThrow(() => new vm.Script(browserInitSource));
  assert.equal(booleanPredicate("document.querySelector('#new-explorer-name')"), "Boolean((document.querySelector('#new-explorer-name')))");
});

test('CDP command timeout rejects and clears the orphaned pending command', async () => {
  const cdp = {
    nextID: 1,
    pending: new Map(),
    send() {
      const id = this.nextID++;
      this.pending.set(id, {});
      return new Promise(() => {});
    },
  };
  boundCDPCommands(cdp, 10);
  const keepAlive = setTimeout(() => {}, 100);
  try {
    await assert.rejects(cdp.send('Runtime.evaluate'), /CDP command timed out: Runtime.evaluate/);
    assert.equal(cdp.pending.size, 0);
  } finally {
    clearTimeout(keepAlive);
  }
});

test('inventory extracts gates, hook calls, and guards from a real TSX parser fixture', () => {
  const root = mkdtempSync(join(tmpdir(), 'loom-ui-inventory-fixture-'));
  try {
    const path = join(root, 'Panel.tsx');
    writeFileSync(path, [
      "const useRowsQuery = () => undefined;",
      "function useCallback(callback: () => void, deps: unknown[]) { return callback; }",
      "function Panel({ blocked }: { blocked: boolean }) {",
      "  const result = useRowsQuery();",
      "  const refresh = useCallback(() => { if (!result) return; refreshRows(); }, []);",
      "  return <button data-testid=\"refresh\" disabled={blocked} aria-disabled={blocked} style={{ pointerEvents: 'none' }} onClick={refresh}>{result ? <span>Ready</span> : null}</button>;",
      "}",
    ].join('\n'));
    const records = scanTypeScriptSource({ path, root });
    assert.ok(records.some((record) => record.kind === 'data-hook-call' && record.hook === 'useRowsQuery'));
    assert.ok(records.some((record) => record.kind === 'data-hook' && record.hook === 'data-testid'));
    assert.ok(records.some((record) => record.kind === 'jsx-interaction-gate' && record.gate === 'disabled'));
    assert.ok(records.some((record) => record.kind === 'jsx-interaction-gate' && record.gate === 'aria-disabled'));
    assert.ok(records.some((record) => record.kind === 'pointer-style-gate' && record.gate === 'pointerEvents:none'));
    assert.ok(records.some((record) => record.kind === 'conditional-render'));
    assert.ok(records.some((record) => record.kind === 'imperative-event-guard' && record.handler === 'refresh' && record.gate === '!result'));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('coverage drift detects both missing registry hooks and unregistered production hooks', () => {
  const drift = inventoryCoverageDrift([
    { kind: 'data-hook-call', hook: 'useCurrent' },
    { kind: 'data-hook-call', hook: 'useForgotten' },
  ], [
    { id: 'sample', hooks: ['useCurrent', 'useMissing'], cases: ['load'], requiredChecks: { load: ['loaded'] }, coverage: [{ status: 'implemented' }] },
  ]);
  assert.deepEqual(drift, [
    'sample: missing source hook useMissing',
    'unregistered production data hook useForgotten',
  ]);
});

test('the production inventory covers Loom data hooks, direct client calls, and guarded Builder actions without drift', () => {
  const inventory = buildInventory();
  assert.deepEqual(inventory.registryCoverageDrift, []);
  assert.ok(inventory.records.some((record) => record.kind === 'data-hook-call' && record.hook === 'useCreateExplorerAuthoringMutation'));
  for (const hook of ['useDeleteExplorerAuthoringMutation', 'useGetExplorerAuthoringCapabilityV2Query']) {
    assert.ok(inventory.records.some((record) => record.kind === 'data-hook-call' && record.hook === hook), 'missing source inventory for ' + hook);
  }
  assert.ok(inventory.records.some((record) => record.kind === 'data-hook-call' && record.hook === 'useKeyedQuery'));
  assert.ok(inventory.records.some((record) => record.kind === 'client-api-call' && record.client === 'client' && record.method === 'exportOutput'));
  for (const handler of ['applyCommandsWithResult', 'ensureSuggestions', 'executePreview', 'publish']) {
    assert.ok(inventory.records.some((record) => record.kind === 'imperative-event-guard' && record.handler === handler), 'missing guard inventory for ' + handler);
  }
});

test('browser URL building preserves a custom path and query while adding the requested route state', () => {
  const url = new URL(browserURL({ uiUrl: 'https://example.test/prefix/demo?tenant=blue&mode=old' }, 'project-a', 'explorer-b', 'viewer'));
  assert.equal(url.pathname, '/prefix/demo');
  assert.equal(url.searchParams.get('tenant'), 'blue');
  assert.equal(url.searchParams.get('project'), 'project-a');
  assert.equal(url.searchParams.get('explorer'), 'explorer-b');
  assert.equal(url.searchParams.get('mode'), 'viewer');
});

test('headless Chrome proves overlays block CDP user input and startup errors are collected', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'loom-ui-browser-selfcheck-'));
  mkdirSync(directory, { recursive: true });
  let browser;
  try {
    try {
      browser = await startBrowser(directory);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (/Chrome or Chromium is required|listen (?:EPERM|EACCES)|EADDRNOTAVAIL/.test(message)) {
        if (process.env.LOOM_VERIFY_REQUIRE_BROWSER === '1') throw error;
        t.skip('headless Chrome is unavailable in this environment: ' + message);
        return;
      }
      throw error;
    }
    const cdp = browser.cdp;
    const html = Buffer.from('<html><body><button id=\"target\">Action</button><div id=\"cover\"></div><fieldset disabled><button id=\"disabled-child\">Disabled child</button></fieldset></body></html>').toString('base64');
    await cdp.send('Page.navigate', { url: 'data:text/html;base64,' + html });
    await waitFor(cdp, "document.readyState === 'complete'", 5000);
    await waitFor(cdp, "document.querySelector('#target')", 5000);
    await evaluate(cdp, "(()=>{const cover=document.querySelector('#cover');Object.assign(cover.style,{position:'fixed',inset:'0',zIndex:'9999',background:'transparent'});document.querySelector('#target').addEventListener('click',()=>document.body.dataset.clicked='yes');setTimeout(()=>{throw new Error('observer-self-check')},0)})()");
    await waitFor(cdp, "window.__loomVerifyPageErrors?.some((message)=>message.includes('observer-self-check'))", 5000);
    await evaluate(cdp, "(()=>{setTimeout(()=>{const start=performance.now();while(performance.now()-start<120){}},0);return true})()");
    await waitFor(cdp, "window.__loomVerifyLongTasks?.some((entry)=>entry.duration>=50)", 5000);
    const covered = await inspectAction(cdp, 'button', { name: 'Action' });
    assert.equal(isActionable(covered), false);
    await assert.rejects(click(cdp, 'button', { name: 'Action' }, 100), /not actionable/);
    await evaluate(cdp, "document.querySelector('#cover').remove()");
    const exposed = await inspectAction(cdp, 'button', { name: 'Action' });
    assert.equal(isActionable(exposed), true);
    const inheritedDisabled = await inspectAction(cdp, 'button', { name: 'Disabled child' });
    assert.equal(inheritedDisabled.disabled, true);
    assert.equal(isActionable(inheritedDisabled), false);
    await assert.rejects(click(cdp, 'button', { name: 'Disabled child' }), /not actionable/);
    await evaluate(cdp, "(()=>{const target=document.querySelector('#target');target.animate([{transform:'translateX(0px)'},{transform:'translateX(200px)'}],{duration:350,fill:'forwards'});target.addEventListener('click',()=>document.body.dataset.clickX=target.getBoundingClientRect().left)})()");
    await click(cdp, 'button', { name: 'Action' });
    assert.ok(Number(await evaluate(cdp, "document.body.dataset.clickX")) >= 200, 'native click waits for the moving target to settle');
    assert.equal(await evaluate(cdp, "document.body.dataset.clicked === 'yes'"), true);
    await evaluate(cdp, "(()=>{document.body.style.minHeight='2000px';const header=document.createElement('div');header.textContent='Sticky header';Object.assign(header.style,{position:'sticky',top:'0',height:'80px',zIndex:'999',background:'white'});document.body.prepend(header);const target=document.createElement('button');target.id='sticky-target';target.textContent='Below sticky header';target.style.marginTop='600px';target.addEventListener('click',()=>document.body.dataset.stickyClicked='yes');document.body.append(target);target.scrollIntoView({block:'start',behavior:'instant'})})()");
    await click(cdp, '#sticky-target');
    assert.equal(await evaluate(cdp, "document.body.dataset.stickyClicked === 'yes'"), true);
    await evaluate(cdp, "(()=>{const graph=document.createElement('div');graph.className='react-flow';graph.id='clipped-graph';Object.assign(graph.style,{overflow:'hidden',width:'200px',height:'200px',marginTop:'600px'});const node=document.createElement('button');node.className='react-flow__node';node.textContent='Clipped graph node';Object.assign(node.style,{marginLeft:'100px',marginTop:'100px',width:'70px'});node.addEventListener('click',()=>document.body.dataset.graphClicked='yes');graph.append(node);document.body.append(graph)})()");
    await click(cdp, '#clipped-graph .react-flow__node');
    assert.equal(await evaluate(cdp, "document.body.dataset.graphClicked === 'yes' && document.querySelector('#clipped-graph').scrollLeft === 0 && document.querySelector('#clipped-graph').scrollTop === 0"), true, 'clicking a graph node must not scroll its clipped viewport');
    const errors = await evaluate(cdp, 'window.__loomVerifyPageErrors');
    assert.ok(errors.some((message) => message.includes('observer-self-check')));
  } finally {
    await browser?.close().catch(() => undefined);
    rmSync(directory, { recursive: true, force: true });
  }
});


test('abandoned event waits do not reject outside a failed scenario and still clean listeners', async () => {
  const cdp = { listeners: new Map(), on(method, handler) { this.listeners.set(method, [handler]); } };
  waitForCDPEvent(cdp, 'Network.responseReceived', () => true, 5);
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(cdp.listeners.get('Network.responseReceived').length, 0);
  const awaited = waitForCDPEvent(cdp, 'Network.responseReceived', () => true, 5);
  const keepAlive = setTimeout(() => {}, 30);
  try { await assert.rejects(awaited, /timed out waiting for CDP event/); }
  finally { clearTimeout(keepAlive); }
});


test('one injected request never exempts a later failure at the same endpoint', async () => {
  const sent = [];
  const listeners = new Map();
  const cdp = {
    on(method, handler) { listeners.set(method, handler); },
    async send(method, params) { sent.push({ method, params }); },
  };
  const fault = await injectReadFaultOnce(cdp, { method: 'POST', pathEndsWith: '/graphql/graph' });
  const pause = listeners.get('Fetch.requestPaused');
  const request = { method: 'POST', url: 'http://localhost/graphql/graph' };
  pause({ requestId: 'fetch-one', networkId: 'one', request });
  pause({ requestId: 'fetch-two', networkId: 'two', request });
  await fault.wait(100);
  assert.equal(fault.count(), 1);
  assert.equal(sent.filter((entry) => entry.method === 'Fetch.failRequest').length, 1);
  assert.equal(fault.matches({ requestId: 'one', errorText: 'net::ERR_FAILED' }), true);
  assert.equal(fault.matches({ requestId: 'two', errorText: 'net::ERR_FAILED' }), false);
  await fault.restore();
});


test('explicit compilation rejection fulfills only one matching request with the declared status', async () => {
  const sent = [];
  const listeners = new Map();
  const cdp = { on(method, handler) { listeners.set(method, handler); }, async send(method, params) { sent.push({ method, params }); } };
  const rejection = { status: 422, body: { code: 'VERIFY_COMPILE_REJECTED', message: 'Controlled rejection.' } };
  const fault = await injectReadFaultOnce(cdp, { method: 'POST', pathEndsWith: '/reconcile', rejection });
  const pause = listeners.get('Fetch.requestPaused');
  const request = { method: 'POST', url: 'http://localhost/authoring/v2/reconcile' };
  pause({ requestId: 'fetch-one', networkId: 'one', request });
  pause({ requestId: 'fetch-two', networkId: 'two', request });
  await fault.wait(100);
  const fulfilled = sent.filter((entry) => entry.method === 'Fetch.fulfillRequest');
  assert.equal(fulfilled.length, 1);
  assert.equal(fulfilled[0].params.responseCode, 422);
  assert.deepEqual(JSON.parse(Buffer.from(fulfilled[0].params.body, 'base64').toString()), rejection.body);
  assert.equal(fault.httpStatus, 422);
  assert.equal(fault.matches({ requestId: 'one', status: 422 }), true);
  assert.equal(fault.matches({ requestId: 'two', status: 422 }), false);
  await fault.restore();
});
