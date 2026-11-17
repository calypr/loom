import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { existsSync } from 'node:fs';
import { mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { CDPClient } from '../browser/cdp.mjs';
import { EXPLORER_AUTHORING_SEMANTICS_VERSION } from '../../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';

const repo = resolve(import.meta.dirname, '../..');
const fixtureRoot = join(repo, 'testdata/devloop-fixture');
const uiOrigin = process.env.LOOM_DEV_UI_URL || 'http://127.0.0.1:3180';
const apiOrigin = (process.env.LOOM_DEV_API_URL || 'http://127.0.0.1:8180').replace(/\/$/, '');
const stackProject = process.env.LOOM_DEV_PROJECT;
let project = stackProject;
const chromePath = process.env.LOOM_E2E_CHROME || [
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser',
].find(existsSync);

export const wait = (ms = 50) => new Promise(resolvePromise => setTimeout(resolvePromise, ms));

export async function until(probe, label, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await probe();
    if (last) return last;
    await wait(50);
  }
  assert.fail(`Timed out waiting for ${label}${last ? `; last=${JSON.stringify(last)}` : ''}`);
}

export async function readFixture(resourceType, directory = fixtureRoot) {
  const text = await readFile(join(directory, `${resourceType}.ndjson`), 'utf8');
  return text.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
}

export function rowsOf(result) {
  const columns = result.columns.map(column => column.name);
  return result.rows.map(row => columns.map(column => row[column] ?? null));
}

export function assertRowsMatch(actual, expected, label = 'rows') {
  const key = rows => rows.map(row => JSON.stringify(row)).sort();
  assert.deepEqual(key(actual), key(expected), `${label} differ from the fixture oracle`);
}

export async function api(path, { method = 'GET', body } = {}) {
  const response = await fetch(`${apiOrigin}${path}`, {
    method,
    headers: { 'content-type': 'application/json', 'x-request-id': `loom-e2e-${randomUUID()}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : null; } catch { value = text; }
  assert(response.ok, `${method} ${path} returned ${response.status}: ${JSON.stringify(value)}`);
  return value;
}

function executableCandidates() {
  return [chromePath, process.env.CHROME, 'google-chrome', 'chromium', 'chromium-browser'].filter(Boolean);
}

export async function launchBrowser() {
  assert(project, 'Set LOOM_DEV_PROJECT to the project seeded by make dev');
  assert(chromePath || process.env.CHROME, 'Set LOOM_E2E_CHROME to Chrome or Chromium');
  const executable = chromePath || process.env.CHROME || executableCandidates()[0];
  const profile = join(tmpdir(), `loom-e2e-${randomUUID()}`);
  const chrome = spawn(executable, [
    '--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0',
    `--user-data-dir=${profile}`, '--window-size=1600,1100', '--no-first-run', '--no-default-browser-check',
    '--disable-background-networking', '--disable-component-update', '--disable-sync', 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  let stderr = '';
  chrome.stderr.on('data', chunk => { stderr += chunk; });
  const activePort = join(profile, 'DevToolsActivePort');
  await until(() => existsSync(activePort), 'Chrome DevTools endpoint', 10000).catch(async error => {
    chrome.kill('SIGTERM');
    throw new Error(`${error.message}\n${stderr}`);
  });
  const [port, browserPath] = (await readFile(activePort, 'utf8')).trim().split('\n');
  const browser = await CDPClient.connect(`ws://127.0.0.1:${port}${browserPath}`);
  const downloads = join(profile, 'downloads');
  await import('node:fs/promises').then(({ mkdir }) => mkdir(downloads));
  const downloadStarts = [];
  const downloadStates = new Map();
  browser.on('Browser.downloadWillBegin', event => downloadStarts.push(event));
  browser.on('Browser.downloadProgress', event => downloadStates.set(event.guid, event.state));
  await browser.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads, eventsEnabled: true });
  const target = await until(async () => {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' });
      if (!response.ok) return false;
      const value = await response.json();
      return typeof value.webSocketDebuggerUrl === 'string' ? value : false;
    } catch {
      return false;
    }
  }, 'Chrome page target', 10000).catch(error => {
    browser.close();
    chrome.kill('SIGTERM');
    throw error;
  });
  const page = await CDPClient.connect(target.webSocketDebuggerUrl);
  await page.send('Runtime.enable');
  await page.send('Page.enable');
  await page.send('Network.enable');
  await page.send('Page.setLifecycleEventsEnabled', { enabled: true });
  const context = new E2EPage(page);
  context.browserClient = browser;
  context.targetInfo = { targetId: target.id, url: target.url, webSocketDebuggerUrl: target.webSocketDebuggerUrl };
  context.chromeStderr = () => stderr;
  for (const method of [
    'Page.frameStartedLoading', 'Page.frameStoppedLoading', 'Page.frameNavigated',
    'Page.domContentEventFired', 'Page.loadEventFired', 'Page.lifecycleEvent',
    'Runtime.executionContextCreated', 'Runtime.executionContextDestroyed', 'Runtime.exceptionThrown',
    'Inspector.targetCrashed', 'Inspector.targetReloadedAfterCrash', 'Network.loadingFailed',
    'Network.responseReceived', 'Log.entryAdded',
  ]) context.observe(page, method);
  browser.on('Target.targetCrashed', params => {
    context.devtoolsEvents.push({ at: Date.now(), method: 'Target.targetCrashed', params });
  });
  await browser.send('Target.setDiscoverTargets', { discover: true });
  return {
    page: context,
    downloads, downloadStarts, downloadStates,
    async close() {
      page.close(); browser.close(); chrome.kill('SIGTERM');
      await wait(100);
    },
  };
}

export class E2EPage {
  constructor(client) {
    this.client = client;
    this.targetId = 0;
    this.lastClick = null;
    this.browserClient = null;
    this.targetInfo = null;
    this.chromeStderr = () => '';
    this.devtoolsEvents = [];
    this.lifecycleEvents = [];
    this.eventSequence = 0;
  }

  observe(client, method) {
    client.on(method, params => {
      let details = params;
      if (method === 'Network.responseReceived') details = {
        url: params.response?.url,
        status: params.response?.status,
        mimeType: params.response?.mimeType,
        type: params.type,
      };
      if (method === 'Network.loadingFailed') details = {
        errorText: params.errorText,
        canceled: params.canceled,
        type: params.type,
      };
      if (method === 'Page.frameNavigated') details = {
        frameId: params.frame?.id,
        parentId: params.frame?.parentId,
        loaderId: params.frame?.loaderId,
        url: params.frame?.url,
      };
      if (method === 'Runtime.exceptionThrown') details = {
        text: params.exceptionDetails?.text,
        description: params.exceptionDetails?.exception?.description?.slice(0, 500),
      };
      if (method === 'Runtime.executionContextCreated') details = {
        id: params.context?.id,
        origin: params.context?.origin,
        name: params.context?.name,
      };
      const event = { sequence: ++this.eventSequence, at: Date.now(), method, params: details };
      this.devtoolsEvents.push(event);
      if (this.devtoolsEvents.length > 60) this.devtoolsEvents.shift();
      if (method === 'Page.lifecycleEvent') {
        this.lifecycleEvents.push(event);
        if (this.lifecycleEvents.length > 100) this.lifecycleEvents.shift();
      }
    });
  }

  async waitForNavigationLifecycle(frameId, loaderId, timeoutMs = 30000) {
    try {
      return await until(() => this.lifecycleEvents.find(event =>
        event.params.frameId === frameId && event.params.loaderId === loaderId &&
        ['DOMContentLoaded', 'load'].includes(event.params.name)),
      `main-frame DOMContentLoaded/load for loader ${loaderId}`, timeoutMs);
    } catch (error) {
      throw new Error(`${error.message}; CDP diagnostics=${JSON.stringify(await this.timeoutDiagnostics())}`);
    }
  }

  async waitForReloadLifecycle(frameId, previousLoaderId, afterSequence, timeoutMs = 30000) {
    return until(() => this.lifecycleEvents.find(event =>
        event.sequence > afterSequence
        && event.params.frameId === frameId && event.params.loaderId && event.params.loaderId !== previousLoaderId &&
        ['DOMContentLoaded', 'load'].includes(event.params.name)),
      `main-frame reload DOMContentLoaded/load after loader ${previousLoaderId}`, timeoutMs);
  }

  async browserProbe(method, timeoutMs = 1500) {
    if (!this.browserClient) return { unavailable: true };
    let timer;
    try {
      return await Promise.race([
        this.browserClient.send(method),
        new Promise(resolvePromise => { timer = setTimeout(() => resolvePromise({ timedOut: true }), timeoutMs); }),
      ]);
    } catch (error) {
      return { error: error.message };
    } finally {
      clearTimeout(timer);
    }
  }

  async timeoutDiagnostics() {
    const before = await this.browserProbe('SystemInfo.getProcessInfo');
    await wait(750);
    const after = await this.browserProbe('SystemInfo.getProcessInfo');
    const cpu = new Map((before.processInfo ?? []).map(process => [process.id, process]));
    const processCPU = (after.processInfo ?? []).map(process => {
      const prior = cpu.get(process.id);
      return {
        id: process.id,
        type: process.type,
        deltaSeconds: prior ? process.cpuTime - prior.cpuTime : null,
      };
    });
    return {
      target: this.targetInfo,
      browserVersion: await this.browserProbe('Browser.getVersion'),
      targets: await this.browserProbe('Target.getTargets'),
      processInfoBefore: before,
      processInfoAfter: after,
      processCPUOver750ms: processCPU,
      chromeStderr: this.chromeStderr().slice(-8000),
      recentPageEvents: this.devtoolsEvents.slice(-60),
    };
  }

  async saveReloadDiagnostics(error, reload) {
    const directory = process.env.LOOM_E2E_DIAGNOSTICS_DIR || join(tmpdir(), 'loom-e2e-diagnostics');
    await mkdir(directory, { recursive: true });
    const filename = `reload-${Date.now()}-${randomUUID()}.json`;
    const filepath = join(directory, filename);
    const previousMainFrameLifecycle = this.lifecycleEvents.filter(event =>
      event.sequence <= reload.afterSequence
      && event.params.frameId === reload.frameId
      && event.params.loaderId
      && event.params.loaderId !== reload.previousLoaderId
      && ['DOMContentLoaded', 'load'].includes(event.params.name));
    const diagnostics = {
      error: { name: error.name, message: error.message, stack: error.stack },
      reload,
      previousMainFrameLifecycleEventsRejectedBySequence: previousMainFrameLifecycle,
      pageEvents: this.devtoolsEvents.slice(),
      lifecycleEvents: this.lifecycleEvents.slice(),
      browserDiagnostics: await this.timeoutDiagnostics(),
      chromeStderr: this.chromeStderr(),
    };
    await writeFile(filepath, `${JSON.stringify(diagnostics, null, 2)}\n`);
    return filepath;
  }

  async evaluate(expression) {
    let timeout;
    const callSite = new Error().stack?.split('\n').slice(2, 5).join('\n') ?? 'unknown caller';
    const request = this.client.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    let result;
    try {
      result = await Promise.race([
        request,
        new Promise((_, reject) => {
          timeout = setTimeout(() => reject(new Error(`Runtime.evaluate did not answer within 10000ms; expression=${expression.slice(0, 220)}\n${callSite}`)), 10000);
        }),
      ]);
    } catch (error) {
      if (String(error.message).includes('Runtime.evaluate did not answer')) {
        throw new Error(`${error.message}\nCDP diagnostics=${JSON.stringify(await this.timeoutDiagnostics())}`);
      }
      throw error;
    } finally {
      clearTimeout(timeout);
    }
    assert.ok(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
    return result.result.value;
  }

  async navigate(url) {
    const navigation = await this.client.send('Page.navigate', { url });
    if (navigation.errorText) throw new Error(`Page.navigate failed: ${navigation.errorText}`);
    if (navigation.frameId && navigation.loaderId) {
      await this.waitForNavigationLifecycle(navigation.frameId, navigation.loaderId);
    }
    await this.until(() => this.evaluate('document.readyState === "complete"'), `navigation to ${url}`, 30000);
    await this.until(() => this.evaluate('document.body.innerText.length > 0'), 'application content', 30000);
  }

  async reload() {
    const frameTree = await this.client.send('Page.getFrameTree');
    const frame = frameTree.frameTree?.frame;
    assert(frame?.id, 'Page.getFrameTree must identify the current main frame before reload');
    const previousLoaderId = frame.loaderId;
    const reload = {
      frameId: frame.id,
      previousLoaderId,
      previousURL: frame.url,
      afterSequence: this.eventSequence,
      startedAt: Date.now(),
    };
    try {
      await this.client.send('Page.reload', { ignoreCache: true });
      reload.reloadCommandReturnedAt = Date.now();
      reload.matchedLifecycle = await this.waitForReloadLifecycle(frame.id, previousLoaderId, reload.afterSequence);
      reload.lifecycleReadyAt = Date.now();
      await this.until(() => this.evaluate('document.readyState === "complete"'), 'page reload', 30000);
      await this.until(() => this.evaluate('document.body.innerText.length > 0'), 'application after reload', 30000);
    } catch (error) {
      reload.failureAt = Date.now();
      reload.failureMessage = error.message;
      let filepath;
      try { filepath = await this.saveReloadDiagnostics(error, reload); }
      catch (diagnosticError) { filepath = `unavailable (${diagnosticError.message})`; }
      const reloadEvents = [...this.devtoolsEvents, ...this.lifecycleEvents]
        .filter(event => event.sequence > reload.afterSequence
          && ['Page.frameStartedLoading', 'Page.frameStoppedLoading', 'Page.frameNavigated', 'Page.lifecycleEvent'].includes(event.method))
        .sort((left, right) => left.sequence - right.sequence).slice(-30);
      throw new Error(`${error.message}; reload events=${JSON.stringify(reloadEvents)}; full CDP diagnostics saved at ${filepath}`);
    }
  }

  until(probe, label, timeoutMs) { return until(probe, label, timeoutMs); }

  async click(selector) {
    const point = await until(() => this.evaluate(`(async () => {const e=document.querySelector(${JSON.stringify(selector)}); if(!e || e.matches(':disabled')) return null; const rect=()=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}}; const beforeScroll=rect(); e.scrollIntoView({block:'center',behavior:'instant'}); const afterScroll=rect(); await new Promise(requestAnimationFrame); const frame1=rect(); await new Promise(requestAnimationFrame); const frame2=rect(); const r=e.getBoundingClientRect(),x=r.x+r.width/2,y=r.y+r.height/2,hit=document.elementFromPoint(x,y); const hitPath=[]; for(let node=hit;node&&hitPath.length<6;node=node.parentElement) hitPath.push({tag:node.tagName,id:node.id,role:node.getAttribute('role'),label:node.getAttribute('aria-label'),className:typeof node.className==='string'?node.className:''}); const geometry={beforeScroll,afterScroll,frame1,frame2,dpr:devicePixelRatio,visualViewport:visualViewport?{scale:visualViewport.scale,offsetLeft:visualViewport.offsetLeft,offsetTop:visualViewport.offsetTop,width:visualViewport.width,height:visualViewport.height}:null,hitPath}; return r.width && r.height && e.contains(hit) ? {x,y,geometry} : null})()`), `visible ${selector}`);
    this.lastClick = point;
    await this.client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await this.client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  }

  async clickLabel(label, tag = 'button') {
    const target = `label-${++this.targetId}`;
    const selector = await this.evaluate(`(() => {const all=[...document.querySelectorAll(${JSON.stringify(tag)})]; const e=all.find(x=>(x.getAttribute('aria-label')||x.innerText||'').trim()===${JSON.stringify(label)}); if(!e)return null; e.setAttribute('data-e2e-target',${JSON.stringify(target)}); return '[data-e2e-target="${target}"]'})()`);
    assert(selector, `Missing ${tag} with label/text ${label}`);
    await this.click(selector);
  }

  async clickContainingText(text, rootSelector = 'body') {
    const target = `label-${++this.targetId}`;
    const selector = await this.evaluate(`(() => {const root=document.querySelector(${JSON.stringify(rootSelector)});const labels=[...(root?.querySelectorAll('label')??[])];const e=labels.find(item=>(item.innerText||'').trim().includes(${JSON.stringify(text)}));if(!e)return null;e.setAttribute('data-e2e-label',${JSON.stringify(target)});return '[data-e2e-label="${target}"]'})()`);
    assert(selector, `Missing label ${text}`);
    await this.click(selector);
  }

  async clickText(text, rootSelector = 'body') {
    const target = `text-${++this.targetId}`;
    const selector = await this.evaluate(`(() => {const root=document.querySelector(${JSON.stringify(rootSelector)});const elements=[...(root?.querySelectorAll('button,[role=button]')??[])];const e=elements.find(item=>(item.innerText||'').trim().includes(${JSON.stringify(text)}));if(!e)return null;e.setAttribute('data-e2e-text',${JSON.stringify(target)});return '[data-e2e-text="${target}"]'})()`);
    assert(selector, `Missing button text ${text}`);
    await this.click(selector);
  }

  async openRowSettings() {
    const selector = await this.evaluate(`(() => {const candidates=[...document.querySelectorAll('button')];const item=candidates.find(x=>x.matches('[data-testid="construction-rows-settings-trigger"]')||/^Change (starting )?records/.test(x.getAttribute('aria-label')||'')||/^Change current rows$/.test(x.getAttribute('aria-label')||'')||/^Edit row definition(?::|$)/.test(x.getAttribute('aria-label')||'')||(x.innerText||'').trim()==='Edit row definition');if(!item)return null;item.setAttribute('data-e2e-row-settings',String(Date.now()));return '[data-e2e-row-settings]';})()`);
    assert(selector, 'Builder exposes the current row settings control');
    await this.click(selector);
  }

  async selectOptionMatching(selector, fragment) {
    const value = await this.evaluate(`(() => {const select=document.querySelector(${JSON.stringify(selector)});if(!select||select.disabled)return null;const option=[...select.options].find(item=>item.textContent.toLowerCase().includes(${JSON.stringify(fragment.toLowerCase())}));return option?.value??null})()`);
    assert.notEqual(value, null, `Missing option containing ${fragment} in ${selector}`);
    await this.select(selector, value);
  }

  async selectOptionText(selector, text) {
    const value = await this.evaluate(`(() => {const select=document.querySelector(${JSON.stringify(selector)});if(!select||select.disabled)return null;const option=[...select.options].find(item=>item.textContent.trim()===${JSON.stringify(text)});return option?.value??null})()`);
    assert.notEqual(value, null, `Missing option ${text} in ${selector}`);
    await this.select(selector, value);
  }

  async fill(selector, value) {
    const point = await until(() => this.evaluate(`(() => {const e=document.querySelector(${JSON.stringify(selector)}); if(!e || e.disabled)return null; e.focus(); e.select?.(); const r=e.getBoundingClientRect(); return r.width && r.height ? {x:r.x+r.width/2,y:r.y+r.height/2}:null})()`), `enabled input ${selector}`);
    await this.client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await this.client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await this.client.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'A', code: 'KeyA', modifiers: 2 });
    await this.client.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'A', code: 'KeyA', modifiers: 2 });
    await this.client.send('Input.insertText', { text: String(value) });
  }

  async pressEscape() {
    await this.client.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape' });
    await this.client.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape' });
  }

  async select(selector, value) {
    await this.evaluate(`(() => {const e=document.querySelector(${JSON.stringify(selector)}); if(!e || e.disabled) throw new Error('Missing enabled select ${selector}'); e.focus(); e.value=${JSON.stringify(String(value))}; e.dispatchEvent(new Event('input',{bubbles:true})); e.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  }

  async text(selector) { return this.evaluate(`document.querySelector(${JSON.stringify(selector)})?.innerText ?? null`); }
  async json(selector) { return this.evaluate(`JSON.parse(document.querySelector(${JSON.stringify(selector)})?.textContent ?? 'null')`); }

  async tableRows() {
    return this.evaluate(`(() => {const rows=Array.from(document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]'));if(rows.length)return rows.slice(1).map(row=>Array.from(row.querySelectorAll('[role="cell"]'),cell=>cell.innerText.trim())).filter(row=>row.length);return Array.from(document.querySelectorAll('tbody tr'),row=>Array.from(row.querySelectorAll('td'),cell=>cell.innerText.trim())).filter(row=>row.length)})()`);
  }
}

async function seedFreshFixtureProject(directory = fixtureRoot) {
  assert(stackProject, 'Set LOOM_DEV_PROJECT to the loom-dev stack project');
  const id = `loom_dev_e2e_${randomUUID().replaceAll('-', '').slice(0, 12)}`;
  const generation = 'devloop-e2e-v1';
  const url = `${apiOrigin}/api/v1/datasets/${encodeURIComponent(id)}/generations/${encodeURIComponent(generation)}`;
  const existing = await fetch(url, { signal: AbortSignal.timeout(10000) });
  assert.equal(existing.status, 404, `E2E fixture generation must be fresh for ${id}`);
  const form = new FormData();
  const names = ['Patient', 'Observation', 'Group', 'Specimen', 'DiagnosticReport', 'ResearchStudy', 'DocumentReference', 'Condition', 'MedicationAdministration', 'MedicationRequest', 'Procedure', 'ServiceRequest', 'Encounter', 'Immunization'];
  for (const name of names) {
    try { form.append('file', new Blob([await readFile(join(directory, `${name}.ndjson`))]), `${name}.ndjson`); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  form.append('defer_activation', 'false');
  const submitted = await fetch(url, { method: 'POST', body: form, signal: AbortSignal.timeout(180000) });
  assert(submitted.ok, `Fixture ingestion returned ${submitted.status}: ${(await submitted.text()).slice(0, 500)}`);
  const deadline = Date.now() + 180000;
  let lastState = 'not-started';
  while (Date.now() < deadline) {
    const response = await fetch(url, { signal: AbortSignal.timeout(10000) });
    if (response.ok) {
      const record = await response.json();
      lastState = String(record.state ?? 'unknown').toUpperCase();
      if (['STAGED', 'READY', 'ACTIVE'].includes(lastState)) break;
      assert(!['FAILED', 'ERROR'].includes(lastState), `Fixture ingestion failed: ${JSON.stringify(record)}`);
    }
    await wait(500);
  }
  assert(['STAGED', 'READY', 'ACTIVE'].includes(lastState), `Fixture ingestion did not become ready (last state ${lastState})`);
  project = id;
  return { project: id, generation, sourceStackProject: stackProject };
}

function projectPath() { return `/api/v1/projects/${encodeURIComponent(project)}/explorers`; }

export async function createExplorer(name) {
  const explorerId = `loom-e2e-${name}-${randomUUID().slice(0, 8)}`;
  const root = projectPath();
  await api(root, { method: 'POST', body: { name: explorerId, title: `E2E ${name} ${explorerId}` } });
  const base = `${root}/${encodeURIComponent(explorerId)}/authoring/v2`;
  const builder = await api(`${base}/builder`);
  return { project, explorerId, root, base, builder, url: `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder` };
}

export async function postCommands(scenario, commands) {
  const current = await api(`${scenario.base}/builder`);
  return api(`${scenario.base}/commands`, {
    method: 'POST',
    body: {
      snapshotToken: current.catalog.snapshotToken,
      expectedDraftVersion: current.draftVersion,
      expectedDraftDigest: current.draftDigest,
      commandId: randomUUID(),
      semanticsVersion: current.workspace?.semanticsVersion ?? EXPLORER_AUTHORING_SEMANTICS_VERSION,
      commands,
    },
  });
}

export async function addDatasetFieldsInUI(scenario, page, fieldPaths) {
  const initialState = await api(`${scenario.base}/builder`);
  const initialColumnCount = initialState.workspace.documents[0].columns.length;
  await page.click('[data-testid="construction-action-add-columns"]');
  await page.until(() => page.evaluate("document.querySelectorAll('[role=option]').length > 0"), 'available Builder fields');
  for (const fieldPath of fieldPaths) {
    const displayField = fieldPath.replace(/([a-z])([A-Z])/g, '$1 $2').replaceAll('.', ' ').toLowerCase();
    const option = await page.evaluate(`(() => {const all=[...document.querySelectorAll('[role="option"]')];const item=all.find(x=>{const label=(x.getAttribute('aria-label')||'').toLowerCase();return label.includes(${JSON.stringify(displayField)}) && (${JSON.stringify(fieldPath.includes('component'))} || !label.includes('component'))});if(!item)return null;item.setAttribute('data-e2e-field',${JSON.stringify(fieldPath)});return '[data-e2e-field='+JSON.stringify(${JSON.stringify(fieldPath)})+']'})()`);
    if (!option) { const details = await page.evaluate(`({text:document.body.innerText.slice(-1500),options:Array.from(document.querySelectorAll('[role=\"option\"]'),item=>item.getAttribute('aria-label')),lists:Array.from(document.querySelectorAll('[role=\"listbox\"]'),item=>item.getAttribute('aria-label'))})`); assert.fail(`Builder field ${fieldPath} is available; details=${JSON.stringify(details)}`); }
    await page.click(option);
  }
  const addButton = await page.evaluate(`(() => {const buttons=[...document.querySelectorAll('button')];const item=buttons.find(x=>x.innerText.trim().startsWith('Add ') && x.innerText.trim().includes('selected feature'));if(!item)return null;item.setAttribute('data-e2e-add','target');return '[data-e2e-add="target"]'})()`);
  if (!addButton) { const buttons = await page.evaluate("Array.from(document.querySelectorAll('button'),x=>x.innerText.trim()).filter(Boolean)"); assert.fail(`Selected Builder fields can be added; buttons=${JSON.stringify(buttons)}`); }
  await page.click(addButton);
  const form = await page.until(async () => {
    if (await page.evaluate('Boolean(document.querySelector("[data-testid=catalog-selection-options]"))')) return 'forms';
    const state = await api(`${scenario.base}/builder`);
    return state.workspace.documents[0].columns.length >= initialColumnCount + fieldPaths.length ? 'added' : false;
  }, 'selected field forms or completed additions', 30000);
  if (form === 'forms') {
    const count = await page.evaluate('document.querySelectorAll("[data-testid=catalog-selection-options] article").length');
    await page.clickText(`Add ${count} selected ${count === 1 ? 'feature' : 'features'}`, '[data-testid="catalog-selection-options"]');
  }
  await page.until(async () => { const state = await api(`${scenario.base}/builder`); return state.workspace.documents[0].columns.length >= initialColumnCount + fieldPaths.length; }, 'selected fields added to table', 45000);
}

export async function createResourceTableInUI(scenario, page, resourceType, { addFields = [] } = {}) {
  await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(`button[aria-label=\"Choose ${resourceType} rows\"]`)}))`), 'first-table row picker', 30000);
  await page.clickLabel(`Choose ${resourceType} rows`);
  const created = await until(async () => {
    const state = await api(`${scenario.base}/builder`);
    return state.workspace?.documents?.length ? state : false;
  }, 'Patient table created through Builder', 45000);
  const outputId = created.workspace.documents[0].output.id;
  if (addFields.length) await addDatasetFieldsInUI(scenario, page, addFields);
  await page.reload();
  try {
    await page.until(() => page.evaluate(`Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]') || [...document.querySelectorAll('button')].some(x=>x.innerText.trim()==='Edit row definition' || /^Change starting records/.test(x.getAttribute('aria-label')||'')))`), 'saved first-table workspace', 45000);
  } catch (error) {
    const diagnostics = await page.evaluate(`({url:location.href,text:document.body.innerText.slice(-1000),buttons:Array.from(document.querySelectorAll('button'),x=>x.getAttribute('aria-label')||x.innerText.trim()).filter(Boolean)})`);
    const state = await api(`${scenario.base}/builder`);
    throw new Error(`${error.message}; UI=${JSON.stringify(diagnostics)}; builder=${JSON.stringify({draftVersion:state.draftVersion,workspace:state.workspace})}`);
  }
  const state = await api(`${scenario.base}/builder`);
  return { outputId, state, uiReady: true };
}

export const createPatientTableInUI = (scenario, page, options) => createResourceTableInUI(scenario, page, 'Patient', options);

export async function createResourceTable(scenario, resourceType, { columns = ['id'], filterColumns = [] } = {}) {
  let state = await api(`${scenario.base}/builder`);
  const root = state.catalog.nodes.find(node => node.resourceType === resourceType && node.rowRootEligible && node.populated);
  assert(root, `Fixture catalog must offer populated ${resourceType} rows`);
  await postCommands(scenario, [{ type: 'CREATE_TABLE', title: `E2E ${resourceType} rows`, rootNodeId: root.nodeId }]);
  state = await api(`${scenario.base}/builder`);
  const document = state.workspace.documents[0];
  assert(document, 'CREATE_TABLE did not create an output document');
  for (const fieldPath of columns) {
    const candidate = state.catalog.candidates.find(item => item.nodeId === root.nodeId && item.fieldPath === fieldPath);
    assert(candidate, `Fixture catalog has no Patient.${fieldPath} candidate`);
    await postCommands(scenario, [{ type: 'ADD_COLUMN', outputId: document.output.id, occurrenceId: 'base', candidateId: candidate.candidateId, projectionMode: 'VALUE', initialPresentation: filterColumns.includes(fieldPath) ? 'FILTER' : 'TABLE', title: fieldPath.split('.').at(-1) }]);
    state = await api(`${scenario.base}/builder`);
  }
  return { outputId: document.output.id, state, rootNodeId: root.nodeId };
}

export const createPatientTable = (scenario, options) => createResourceTable(scenario, 'Patient', options);

export async function waitForDownload(browser, timeoutMs = 30000) {
  return until(async () => {
    const event = [...browser.downloadStarts].reverse().find(candidate => browser.downloadStates.get(candidate.guid) === 'completed');
    if (!event) return false;
    const state = browser.downloadStates.get(event.guid);
    assert.notEqual(state, 'canceled', `Download canceled: ${event.suggestedFilename}`);
    if (state !== 'completed') return false;
    const path = join(browser.downloads, event.suggestedFilename);
    try { await stat(path); return { path, filename: event.suggestedFilename, event }; } catch { return false; }
  }, 'completed browser download', timeoutMs);
}

export function inspectZip(path) {
  const listing = spawnSync('unzip', ['-Z1', path], { encoding: 'utf8', timeout: 10000 });
  assert.equal(listing.status, 0, listing.stderr || listing.stdout);
  const members = listing.stdout.trim().split('\n').filter(Boolean);
  assert(members.includes('manifest.json'), `ZIP lacks manifest.json: ${members.join(', ')}`);
  const manifestResult = spawnSync('unzip', ['-p', path, 'manifest.json'], { encoding: 'utf8', timeout: 10000 });
  assert.equal(manifestResult.status, 0, manifestResult.stderr);
  const manifest = JSON.parse(manifestResult.stdout);
  const dataFiles = members.filter(name => /\.(csv|ndjson|jsonl)$/i.test(name));
  assert(dataFiles.length > 0, `ZIP has no dataset rows: ${members.join(', ')}`);
  const data = dataFiles.map(name => {
    const result = spawnSync('unzip', ['-p', path, name], { encoding: 'utf8', timeout: 10000 });
    assert.equal(result.status, 0, result.stderr);
    return { name, text: result.stdout };
  });
  assert(Array.isArray(manifest.members) && manifest.members.length > 0, 'Manifest lists checksummed ZIP members');
  for (const member of manifest.members) {
    const result = spawnSync('unzip', ['-p', path, member.name], { encoding: null, timeout: 10000 });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.length, member.bytes, `${member.name} byte count matches manifest`);
    assert.equal(createHash('sha256').update(result.stdout).digest('hex'), member.sha256, `${member.name} SHA-256 matches manifest`);
  }
  return { members, manifest, data };
}

export async function builderURL(scenario, mode = 'builder') {
  return scenario.url.replace('mode=builder', `mode=${mode}`);
}

export async function cleanupExplorer(scenario) {
  try {
    const capability = await api(`${scenario.base}/capability`);
    if (capability.features?.deleteExplorer) {
      await api(`${scenario.root}/${encodeURIComponent(scenario.explorerId)}`, { method: 'DELETE' });
      return;
    }
    console.log(`Retained E2E Explorer ${scenario.project}/${scenario.explorerId}: deleteExplorer is unsupported`);
  } catch (error) {
    console.warn(`Could not inspect E2E Explorer cleanup capability for ${scenario.project}/${scenario.explorerId}: ${error.message}`);
  }
}

export async function withFreshUIExplorer(name, setup, run, { fixtureDirectory = fixtureRoot } = {}) {
  const projectFixture = await seedFreshFixtureProject(fixtureDirectory);
  const browser = await launchBrowser();
  let scenario;
  try {
    const page = browser.page;
    await page.navigate(`${uiOrigin}/?project=${encodeURIComponent(project)}&mode=builder`);
    await page.until(() => page.evaluate('document.body.innerText.includes("New explorer") || document.body.innerText.includes("Create your first Explorer")'), 'empty-project Explorer creation UI', 30000);
    await page.clickLabel('New explorer', 'summary');
    const explorerName = `E2E ${name} ${randomUUID().slice(0, 6)}`;
    await page.fill('#new-explorer-name', explorerName);
    assert.equal(await page.evaluate('document.querySelector("#new-explorer-name")?.value'), explorerName, 'Explorer name entered through native browser input');
    await page.clickLabel('Create blank');
    let created;
    try {
      created = await page.until(async () => {
        const explorers = await api(projectPath());
        return explorers.find(item => item.title === explorerName) ?? false;
      }, 'Explorer created from Builder control', 10000);
    } catch (error) {
      const diagnostics = await page.evaluate(`({project:${JSON.stringify(project)},name:${JSON.stringify(explorerName)},url:location.href,input:document.querySelector('#new-explorer-name')?.value,menu:document.querySelector('summary')?.parentElement?.open,alerts:Array.from(document.querySelectorAll('[role=alert]'),x=>x.innerText),text:document.body.innerText.slice(-600),requests:performance.getEntriesByType('resource').map(x=>x.name).filter(x=>x.includes('/api/'))})`);
      throw new Error(`${error.message}; UI create diagnostics=${JSON.stringify(diagnostics)}`);
    }
    const explorerId = created.explorerId;
    const root = projectPath();
    scenario = { project, explorerId, root, base: `${root}/${encodeURIComponent(explorerId)}/authoring/v2`, url: `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`, builder: await api(`${root}/${encodeURIComponent(explorerId)}/authoring/v2/builder`) };
    await page.navigate(scenario.url);
    const fixture = await setup(scenario, page, projectFixture);
    await run({ scenario, fixture, projectFixture, ...browser });
  } finally {
    await browser.close();
    if (scenario) await cleanupExplorer(scenario);
  }
}

export async function withScenario(name, setup, run) {
  const projectFixture = await seedFreshFixtureProject();
  const scenario = await createExplorer(name);
  let browser;
  try {
    browser = await launchBrowser();
    await browser.page.navigate(scenario.url);
    const fixture = await setup(scenario, browser.page, projectFixture);
    if (!fixture?.uiReady) {
      await browser.page.reload();
      try {
        await browser.page.until(() => browser.page.evaluate(`Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]') || [...document.querySelectorAll('button')].some(x=>x.innerText.trim()==='Edit row definition' || /^Change starting records/.test(x.getAttribute('aria-label')||'')))`), 'seeded Builder workspace', 45000);
      } catch (error) {
        const diagnostics = await browser.page.evaluate(`({url:location.href,text:document.body.innerText.slice(-900),buttons:Array.from(document.querySelectorAll('button'),x=>x.getAttribute('aria-label')||x.innerText.trim()).filter(Boolean)})`);
        const state = await api(`${scenario.base}/builder`);
        throw new Error(`${error.message}; UI=${JSON.stringify(diagnostics)}; builder=${JSON.stringify({draftVersion:state.draftVersion,workspace:state.workspace})}`);
      }
    }
    await run({ scenario, fixture, ...browser });
  } finally {
    await browser?.close();
    await cleanupExplorer(scenario);
  }
}
