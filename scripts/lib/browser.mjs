import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const request = (url, { timeout = 10000 } = {}) => fetch(url, { signal: AbortSignal.timeout(timeout) });
const isActionable = (target) => Boolean(target?.found && target.visible && !target.disabled && target.ariaDisabled !== 'true' && target.pointerEvents !== 'none' && target.receivesPointer);

const findChrome = () => {
  const candidates = [
    process.env.CHROME_BIN,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    'google-chrome',
    'chromium',
    'chromium-browser',
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (candidate.startsWith('/') ? existsSync(candidate) : true) return candidate;
  }
  throw new Error('Chrome or Chromium is required. Set CHROME_BIN to its executable.');
};

const freePort = async () => new Promise((resolvePromise, reject) => {
  import('node:net').then(({ createServer }) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      const value = typeof address === 'object' && address ? address.port : 0;
      server.close(() => resolvePromise(value));
    });
  }, reject);
});

class CDPConnection {
  constructor(url) {
    this.url = url;
    this.nextID = 1;
    this.pending = new Map();
    this.listeners = new Map();
  }

  async connect() {
    this.socket = new WebSocket(this.url);
    await new Promise((resolvePromise, reject) => {
      this.socket.addEventListener('open', resolvePromise, { once: true });
      this.socket.addEventListener('error', reject, { once: true });
    });
    this.socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id) {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error(message.error.message));
        else pending.resolve(message.result);
        return;
      }
      for (const listener of this.listeners.get(message.method) ?? []) listener(message.params);
    });
    return this;
  }

  on(method, listener) {
    const listeners = this.listeners.get(method) ?? [];
    listeners.push(listener);
    this.listeners.set(method, listeners);
  }

  send(method, params = {}) {
    const id = this.nextID++;
    return new Promise((resolvePromise, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error('CDP command timed out: ' + method)); }, 10000);
      timer.unref?.();
      this.pending.set(id, { resolve: (value) => { clearTimeout(timer); resolvePromise(value); }, reject: (error) => { clearTimeout(timer); reject(error); } });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  close() {
    this.socket?.close();
  }
}

const evaluate = async (cdp, expression) => {
  const result = await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'browser evaluation failed');
  if (result.result?.subtype === 'error') throw new Error(result.result.description || 'browser evaluation failed');
  return result.result?.value;
};

const waitForBrowser = async (cdp, expression, timeout = 30000) => {
  const started = Date.now();
  let lastError = 'condition was false';
  while (Date.now() - started < timeout) {
    try { if (await evaluate(cdp, 'Boolean((' + expression + '))')) return; }
    catch (error) { lastError = String(error); }
    await sleep(100);
  }
  throw new Error('timed out waiting for browser condition: ' + lastError);
};

export const findPageTarget = (pages, initialUrl) => {
  if (initialUrl !== undefined) return pages.find((candidate) => candidate.type === 'page' && candidate.url === initialUrl);
  return pages.find((candidate) => candidate.type === 'page');
};

const launchBrowser = async (downloadDir, onDialog, options = {}) => {
  const chrome = findChrome();
  const port = await freePort();
  const profile = mkdtempSync(join(tmpdir(), 'loom-dev-chrome-'));
  const child = spawn(chrome, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-background-networking',
    '--disable-component-update', '--no-first-run', '--no-default-browser-check',
    '--remote-allow-origins=*', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`,
    `--download.default_directory=${downloadDir}`,
    ...(options.initialUrl ? [options.initialUrl] : []),
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  const started = Date.now();
  let browserError = '';
  child.stderr.on('data', (chunk) => { browserError += String(chunk); });
  let page;
  while (Date.now() - started < 15000) {
    try {
      const response = await request(`http://127.0.0.1:${port}/json/list`, { timeout: 1000 });
      const pages = await response.json();
      page = findPageTarget(pages, options.initialUrl);
      if (page) break;
    } catch {
      await sleep(100);
    }
  }
  if (!page) {
    child.kill('SIGTERM');
    rmSync(profile, { recursive: true, force: true });
    throw new Error(`Chrome did not expose a CDP page: ${browserError.slice(-500)}`);
  }
  const cdp = await new CDPConnection(page.webSocketDebuggerUrl).connect();
  await cdp.send('Runtime.enable');
  await cdp.send('Page.enable');
  await cdp.send('Network.enable');
  await cdp.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloadDir });
  const dialogErrors = [];
  const dialogHandler = (dialog) => {
    let decision;
    try {
      decision = onDialog ? onDialog(dialog) : { accept: true };
    } catch (error) {
      dialogErrors.push(String(error));
      decision = { accept: false };
    }
    void Promise.resolve(decision).then((value) => {
      const response = value ?? { accept: true };
      return cdp.send('Page.handleJavaScriptDialog', {
        accept: response.accept ?? true,
        ...(typeof response.promptText === 'string' ? { promptText: response.promptText } : {}),
      });
    }).catch((error) => {
      dialogErrors.push(String(error));
      return cdp.send('Page.handleJavaScriptDialog', { accept: false }).catch(() => undefined);
    });
  };
  cdp.on('Page.javascriptDialogOpening', dialogHandler);
  const awaitExit = () => new Promise((resolvePromise, reject) => {
    if (child.exitCode !== null || child.signalCode !== null) { resolvePromise(); return; }
    const timer = setTimeout(() => {
      try { child.kill('SIGKILL'); resolvePromise(); }
      catch (error) { reject(error); }
    }, 3000);
    child.once('close', () => { clearTimeout(timer); resolvePromise(); });
  });
  return {
    cdp,
    child,
    profile,
    target: { id: page.id, type: page.type, url: page.url },
    dialogErrors,
    close: async () => {
      await cdp.send('Browser.close').catch(() => undefined);
      cdp.close();
      await awaitExit();
      rmSync(profile, { recursive: true, force: true });
    },
  };
};

const navigate = async (cdp, url) => {
  const navigation = await cdp.send('Page.navigate', { url });
  if (navigation.errorText) throw new Error('navigation failed: ' + navigation.errorText);
  const started = Date.now();
  if (navigation.loaderId) {
    while (true) {
      const { frameTree } = await cdp.send('Page.getFrameTree');
      if (frameTree.frame.loaderId === navigation.loaderId) break;
      if (Date.now() - started >= 30000) throw new Error('navigation did not commit: ' + url);
      await sleep(50);
    }
  }
  await waitForBrowser(cdp, `location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`, 30000);
};

const exactActionElement = ({ selector, name, includes, scroll }) => {
  const content = "const normalize=(x)=>String(x??'').replace(/\\s+/g,' ').trim();"
    + "const nodes=[...document.querySelectorAll(" + JSON.stringify(selector) + ")];"
    + "const element=nodes.find((candidate)=>{const label=normalize(candidate.getAttribute('aria-label')||candidate.innerText||candidate.textContent);"
    + (name !== undefined ? "return label===" + JSON.stringify(name) + ";" : '')
    + (includes !== undefined ? "return label.toLowerCase().includes(" + JSON.stringify(includes.toLowerCase()) + ");" : '')
    + (name === undefined && includes === undefined ? 'return true;' : '')
    + "});"
    + "if(!element)return {found:false,selector:" + JSON.stringify(selector) + ",name:" + JSON.stringify(name ?? includes ?? '') + "};"
    + (scroll ? "(element.closest('[role=dialog]')?element:(element.closest('.react-flow__node')?.closest('.react-flow')||element)).scrollIntoView({block:'center',inline:'nearest',behavior:'instant'});" : '')
    + "let closedDisclosure=null;for(let ancestor=element.parentElement;ancestor;ancestor=ancestor.parentElement){if(ancestor.tagName!=='DETAILS'||ancestor.open)continue;const summary=[...ancestor.children].find(child=>child.tagName==='SUMMARY');if(summary?.contains(element))continue;closedDisclosure={testId:ancestor.getAttribute('data-testid'),summary:normalize(summary?.innerText||summary?.textContent)};break;}"
    + "const rect=element.getBoundingClientRect();const x=rect.left+rect.width/2;const y=rect.top+rect.height/2;"
    + "const style=getComputedStyle(element);const hit=document.elementFromPoint(x,y);"
    + "const disabled=Boolean(element.disabled)||element.getAttribute('aria-disabled')==='true'||Boolean(element.closest('fieldset:disabled'));"
    + "return {found:true,viewportWidth:innerWidth,viewportHeight:innerHeight,closedDisclosure,visible:!closedDisclosure&&!element.hidden&&element.getAttribute('aria-hidden')!=='true'&&style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0,disabled,ariaDisabled:element.getAttribute('aria-disabled'),pointerEvents:style.pointerEvents,receivesPointer:Boolean(hit&&(hit===element||element.contains(hit))),width:rect.width,height:rect.height,blocker:hit&&!(hit===element||element.contains(hit))?{tag:hit.tagName,id:hit.id||null,className:String(hit.className||'').slice(0,160),text:normalize(hit.innerText||hit.textContent).slice(0,160),pointerEvents:getComputedStyle(hit).pointerEvents}:null,x,y,tag:element.tagName,text:normalize(element.getAttribute('aria-label')||element.innerText||element.textContent)};";
  return content;
};

export const inspectAction = async (cdp, selector, identity = {}, scroll = true) => {
  const value = await evaluate(cdp, '(()=>{' + exactActionElement({ selector, ...identity, scroll }) + '})()');
  return value;
};

export const click = async (cdp, selector, identity = {}, actionabilityTimeout = 5000) => {
  const started = Date.now();
  let snapshot;
  let stableSince = 0;
  let previous;
  let stable = false;
  let hovered;
  while (Date.now() - started < actionabilityTimeout) {
    snapshot = await inspectAction(cdp, selector, identity, !previous || !previous.receivesPointer || previous.x < 0 || previous.y < 0 || previous.x >= previous.viewportWidth || previous.y >= previous.viewportHeight);
    if (isActionable(snapshot) && (!hovered || Math.abs(snapshot.x - hovered.x) >= 0.5 || Math.abs(snapshot.y - hovered.y) >= 0.5)) {
      await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: snapshot.x, y: snapshot.y });
      hovered = snapshot;
      stableSince = 0;
    }
    const unchanged = previous && ['x', 'y', 'width', 'height'].every((key) => Math.abs(snapshot[key] - previous[key]) < 0.5);
    if (isActionable(snapshot) && unchanged) {
      stableSince ||= Date.now();
      if (Date.now() - stableSince >= 150) { stable = true; break; }
    } else stableSince = 0;
    previous = snapshot;
    if (snapshot?.found && (snapshot.closedDisclosure || snapshot.disabled || snapshot.ariaDisabled === 'true' || snapshot.pointerEvents === 'none')) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  if (!snapshot?.found) throw new Error('action target not found: ' + selector + ' ' + (identity.name ?? identity.includes ?? ''));
  if (!isActionable(snapshot) || !stable) throw new Error('action target is not actionable: ' + JSON.stringify(snapshot));
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: snapshot.x, y: snapshot.y, button: 'left', clickCount: 1 });
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: snapshot.x, y: snapshot.y, button: 'left', clickCount: 1 });
  return snapshot;
};

export const browserEval = (cdp, body) => evaluate(cdp, '(async()=>{' + body + '})()');
export const selectOption = async (cdp, selector, value, { settledWhen, dismissSelector } = {}) => {
  const index = await browserEval(cdp, `return [...document.querySelector(${JSON.stringify(selector)}).options].findIndex(option => option.value === ${JSON.stringify(value)} && !option.disabled);`);
  if (index < 0) throw new Error(`Select does not offer ${value}`);
  await click(cdp, selector);
  // macOS select popups are outside the CDP page keyboard target. Dispatch
  // selection events while the actioned control and its popover remain mounted.
  await browserEval(cdp, `const select=document.querySelector(${JSON.stringify(selector)}); const option=select?.options[${index}]; if(!select || select.disabled || !option || option.disabled) throw new Error('Select option is disabled'); option.selected=true; select.dispatchEvent(new Event('input',{bubbles:true})); select.dispatchEvent(new Event('change',{bubbles:true}));`);
  await waitForBrowser(cdp, settledWhen ?? `document.querySelector(${JSON.stringify(selector)})?.value === ${JSON.stringify(value)}`);
  // Dismiss the native popup only after the application has handled the change.
  // Popovers can own the select and draft state, so callers may target a safe
  // in-popover surface instead of the legacy page-corner dismissal.
  if (dismissSelector) {
    await click(cdp, dismissSelector);
    await waitForBrowser(cdp,
      `Boolean(document.querySelector(${JSON.stringify(selector)}))&&Boolean(document.querySelector(${JSON.stringify(dismissSelector)}))`);
  } else {
    for (const type of ['mousePressed', 'mouseReleased']) await cdp.send('Input.dispatchMouseEvent', { type, x: 1, y: 1, button: 'left', clickCount: 1 });
  }
};
export { launchBrowser, navigate, evaluate, waitForBrowser };
