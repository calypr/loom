import { mkdirSync } from 'node:fs';
import { launchBrowser, navigate, evaluate, waitForBrowser, snapshot } from '../loom-dev.mjs';
import { classifyNetworkRecord, isActionable, recordCheck, recordUntested } from './report.mjs';

export const booleanPredicate = (expression) => 'Boolean((' + expression + '))';

export const waitFor = async (cdp, expression, timeout = 30000) =>
  waitForBrowser(cdp, booleanPredicate(expression), timeout);

export const onCDP = (cdp, method, listener) => {
  cdp.on(method, listener);
  return () => {
    const listeners = cdp.listeners?.get(method);
    if (!listeners) return;
    const index = listeners.indexOf(listener);
    if (index >= 0) listeners.splice(index, 1);
  };
};

export const waitForCDPEvent = (cdp, method, predicate, timeoutMs = 5000) => {
  const pending = new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      unsubscribe();
      reject(new Error('timed out waiting for CDP event: ' + method));
    }, timeoutMs);
    timer.unref?.();
    const unsubscribe = onCDP(cdp, method, (event) => {
      if (!predicate(event)) return;
      clearTimeout(timer);
      unsubscribe();
      resolve(event);
    });
  });
  // A preceding action can fail before its registered event wait is awaited.
  pending.catch(() => undefined);
  return pending;
};

export const boundCDPCommands = (cdp, timeoutMs = 10000) => {
  const originalSend = cdp.send.bind(cdp);
  cdp.send = (method, params = {}) => {
    const requestId = cdp.nextID;
    let timer;
    const request = originalSend(method, params);
    const timeout = new Promise((_, reject) => {
      timer = setTimeout(() => {
        cdp.pending?.delete(requestId);
        reject(new Error('CDP command timed out: ' + method));
      }, timeoutMs);
      timer.unref?.();
    });
    return Promise.race([request, timeout]).finally(() => clearTimeout(timer));
  };
  return cdp;
};

export const browserInitSource = [
  'window.__loomVerifyLongTasks = [];',
  'try {',
  '  const observer = new PerformanceObserver((list) => {',
  '    const entries = list.getEntries().map((entry) => ({ startTime: entry.startTime, duration: entry.duration }));',
  '    window.__loomVerifyLongTasks.push(...entries);',
  '  });',
  "  observer.observe({ type: 'longtask', buffered: true });",
  '} catch {}',
  'window.addEventListener("error", (event) => {',
  '  window.__loomVerifyPageErrors ??= [];',
  '  window.__loomVerifyPageErrors.push(String(event.message || "page error"));',
  '});',
  'window.addEventListener("unhandledrejection", (event) => {',
  '  window.__loomVerifyPageErrors ??= [];',
  '  window.__loomVerifyPageErrors.push(String(event.reason || "unhandled rejection"));',
  '});',
].join('\n');

export const startBrowser = async (evidenceDirectory) => {
  const downloadDirectory = evidenceDirectory + '/downloads';
  mkdirSync(downloadDirectory, { recursive: true, mode: 0o700 });
  const browser = await launchBrowser(downloadDirectory);
  const cdp = boundCDPCommands(browser.cdp);
  await cdp.send('Runtime.enable');
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await cdp.send('Page.addScriptToEvaluateOnNewDocument', { source: browserInitSource });
  cdp.on('Runtime.exceptionThrown', (params) => {
    browser.__exceptions ??= [];
    browser.__exceptions.push({ kind: 'exception', text: params.exceptionDetails?.exception?.description ?? params.exceptionDetails?.text ?? 'browser exception' });
  });
  cdp.on('Runtime.consoleAPICalled', (params) => {
    if (params.type !== 'error') return;
    browser.__exceptions ??= [];
    browser.__exceptions.push({ kind: 'console-error', text: params.args?.map((arg) => arg.value ?? arg.description ?? '').join(' ') });
  });
  return browser;
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
    + (scroll ? "(element.closest('.react-flow__node')?.closest('.react-flow')||element).scrollIntoView({block:'center',inline:'nearest',behavior:'instant'});" : '')
    + "const rect=element.getBoundingClientRect();const x=rect.left+rect.width/2;const y=rect.top+rect.height/2;"
    + "const style=getComputedStyle(element);const hit=document.elementFromPoint(x,y);"
    + "const disabled=Boolean(element.disabled)||element.getAttribute('aria-disabled')==='true'||Boolean(element.closest('fieldset:disabled'));"
    + "return {found:true,visible:!element.hidden&&element.getAttribute('aria-hidden')!=='true'&&style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0,disabled,ariaDisabled:element.getAttribute('aria-disabled'),pointerEvents:style.pointerEvents,receivesPointer:Boolean(hit&&(hit===element||element.contains(hit))),width:rect.width,height:rect.height,blocker:hit&&!(hit===element||element.contains(hit))?{tag:hit.tagName,id:hit.id||null,className:String(hit.className||'').slice(0,160),text:normalize(hit.innerText||hit.textContent).slice(0,160),pointerEvents:getComputedStyle(hit).pointerEvents}:null,x,y,tag:element.tagName,text:normalize(element.getAttribute('aria-label')||element.innerText||element.textContent)};";
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
    snapshot = await inspectAction(cdp, selector, identity, !previous || !previous.receivesPointer);
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
    if (snapshot?.found && (snapshot.disabled || snapshot.ariaDisabled === 'true' || snapshot.pointerEvents === 'none')) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  if (!snapshot?.found) throw new Error('action target not found: ' + selector + ' ' + (identity.name ?? identity.includes ?? ''));
  if (!isActionable(snapshot) || !stable) throw new Error('action target is not actionable: ' + JSON.stringify(snapshot));
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: snapshot.x, y: snapshot.y, button: 'left', clickCount: 1 });
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: snapshot.x, y: snapshot.y, button: 'left', clickCount: 1 });
  return snapshot;
};

export const fill = async (cdp, selector, value) => {
  await click(cdp, selector);
  await evaluate(cdp, "(()=>{const e=document.querySelector(" + JSON.stringify(selector) + ");if(!e)throw Error('input not found');const d=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value');if(!d?.set)throw Error('input has no native value setter');d.set.call(e," + JSON.stringify(value) + ");e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()");
};

export const perform = async (report, cdp, { name, action, after, settle, timeout = 30000, budget = 5000, dimension = 'usability' }) => {
  const started = Date.now();
  let actionDispatched = false;
  try {
    await action();
    actionDispatched = true;
    if (settle) await settle();
    if (after) await waitFor(cdp, after, timeout);
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'passed', elapsedMs, renderTimeoutMs: timeout, performanceBudgetMs: budget });
    report.timings[name] = elapsedMs;
    if (after) recordCheck(report, 'performance', name + ' action-to-render within budget', elapsedMs <= budget, { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
    recordCheck(report, dimension, name + ' completed', true, { elapsedMs });
    return elapsedMs;
  } catch (error) {
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'failed', elapsedMs, actionDispatched, error: error instanceof Error ? error.message : String(error) });
    recordCheck(report, dimension, name + ' completed', false, { elapsedMs, actionDispatched, error: error instanceof Error ? error.message : String(error) });
    if (after && actionDispatched) recordCheck(report, 'performance', name + ' action-to-render within budget', false, { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
    else if (after) recordUntested(report, 'performance', name + ' action-to-render', 'No action was dispatched because the target was not actionable.');
    await captureDOM(report, cdp, name.replace(/[^a-z0-9]+/gi, '-').toLowerCase() + '-failure');
    throw error;
  }
};

export const captureDOM = async (report, cdp, name) => {
  const path = report.evidenceDirectory + '/' + name + '.html';
  await snapshot(cdp, path);
  const summary = await evaluate(cdp, "(()=>({title:document.title,text:(document.body?.innerText||'').slice(0,12000),alerts:[...document.querySelectorAll('[role=alert]')].map(e=>e.innerText),buttons:[...document.querySelectorAll('button')].map(e=>({text:e.innerText,disabled:e.disabled,ariaDisabled:e.getAttribute('aria-disabled'),title:e.title})).slice(0,80)}))()");
  report.failureDom.push({ name, path, ...summary });
  report.evidence.push(path);
  return summary;
};

export const readPage = async (cdp) => evaluate(cdp, "(()=>({title:document.title,text:document.body?.innerText||'',alerts:[...document.querySelectorAll('[role=alert]')].map(e=>e.innerText),buttons:[...document.querySelectorAll('button')].map(e=>({text:e.innerText.trim(),disabled:e.disabled,ariaDisabled:e.getAttribute('aria-disabled'),title:e.title})),rows:[...document.querySelectorAll('table tbody tr,[role=row]')].map(e=>e.innerText.trim()).filter(Boolean),longTasks:window.__loomVerifyLongTasks||[],pageErrors:window.__loomVerifyPageErrors||[]}))()");

export const reload = async (cdp, waitExpression) => {
  const loaded = waitForCDPEvent(cdp, 'Page.loadEventFired', () => true, 30000);
  await cdp.send('Page.reload', { ignoreCache: true });
  await loaded;
  await waitFor(cdp, waitExpression, 30000);
};

export const safeURL = (value) => {
  try {
    const url = new URL(value);
    if (!['http:', 'https:'].includes(url.protocol)) return url.protocol + '[redacted]';
    return url.origin + url.pathname;
  } catch { return '(invalid URL)'; }
};

export const networkRecordKind = ({ url, status }) => {
  if (status !== 404) return 'network';
  try { return new URL(url).pathname === '/favicon.ico' ? 'asset-failure' : 'network'; } catch { return 'network'; }
};

export const startNetworkMonitor = (cdp) => {
  const requests = new Map();
  const records = [];
  const bodyReads = [];
  cdp.on('Network.requestWillBeSent', (event) => {
    requests.set(event.requestId, { requestId: event.requestId, url: event.request.url, method: event.request.method, resourceType: event.type, startTime: event.timestamp });
  });
  cdp.on('Network.responseReceived', (event) => {
    const request = requests.get(event.requestId);
    if (!request) return;
    request.status = event.response.status;
    request.mimeType = event.response.mimeType;
    request.responseTime = event.timestamp;
    request.fromDiskCache = Boolean(event.response.fromDiskCache);
  });
  cdp.on('Network.loadingFinished', (event) => {
    const request = requests.get(event.requestId);
    if (!request) return;
    const durationMs = Math.max(0, (event.timestamp - request.startTime) * 1000);
    const record = { kind: networkRecordKind({ url: request.url, status: request.status }), requestId: event.requestId, url: safeURL(request.url), method: request.method, resourceType: request.resourceType, status: request.status, durationMs, mimeType: request.mimeType };
    records.push(record);
    if (String(request.mimeType).includes('json')) {
      bodyReads.push(cdp.send('Network.getResponseBody', { requestId: event.requestId }).then((body) => {
        record.internalError = /INTERNAL_ERROR/.test(body.body);
      }).catch(() => undefined));
    }
  });
  cdp.on('Network.loadingFailed', (event) => {
    const request = requests.get(event.requestId);
    if (!request) return;
    records.push({ kind: 'network', requestId: event.requestId, url: safeURL(request.url), method: request.method, resourceType: request.resourceType, errorText: event.errorText, canceled: Boolean(event.canceled), durationMs: Math.max(0, (event.timestamp - request.startTime) * 1000) });
  });
  return {
    async stop(browser, faultControllers = []) {
      await Promise.all(bodyReads);
      for (const record of records) {
        const injected = faultControllers.find((controller) => controller.matches(record));
        if (injected) {
          record.injectedFault = true;
          if (injected.httpStatus) record.injectedStatus = injected.httpStatus;
        }
        delete record.requestId;
      }
      for (const exception of browser.__exceptions ?? []) records.push(exception);
      return records;
    },
  };
};

export const injectReadFaultOnce = async (cdp, { method, pathIncludes, pathEndsWith, rejection }) => {
  if (rejection && rejection.status !== 422) throw new Error('only an explicit validation rejection can be injected');
  let count = 0;
  const injectedNetworkIds = new Set();
  let rejectHandler;
  const handlerErrors = [];
  let resolveMatched;
  const matchedPromise = new Promise((resolve) => { resolveMatched = resolve; });
  cdp.on('Fetch.requestPaused', (event) => {
    const requestURL = new URL(event.request.url);
    const pathMatches = pathEndsWith ? requestURL.pathname.endsWith(pathEndsWith) : requestURL.pathname.includes(pathIncludes);
    const matches = count === 0 && event.request.method === method && pathMatches;
    if (matches) {
      count += 1;
      if (event.networkId) injectedNetworkIds.add(event.networkId);
    }
    const action = matches
      ? rejection
        ? cdp.send('Fetch.fulfillRequest', { requestId: event.requestId, responseCode: rejection.status, responseHeaders: [{ name: 'Content-Type', value: 'application/json' }], body: Buffer.from(JSON.stringify(rejection.body)).toString('base64') })
        : cdp.send('Fetch.failRequest', { requestId: event.requestId, errorReason: 'Failed' })
      : cdp.send('Fetch.continueRequest', { requestId: event.requestId });
    void action.then(() => {
      if (matches) resolveMatched();
    }).catch((error) => {
      handlerErrors.push(error instanceof Error ? error.message : String(error));
      if (rejectHandler) rejectHandler(error);
    });
  });
  const handlerError = new Promise((_, reject) => { rejectHandler = reject; });
  await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }] });
  return {
    wait: async (timeout = 30000) => new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('fault target was not requested: ' + (pathEndsWith ?? pathIncludes))), timeout);
      Promise.race([matchedPromise, handlerError]).then((value) => { clearTimeout(timer); resolve(value); }, (error) => { clearTimeout(timer); reject(error); });
    }),
    count: () => count,
    httpStatus: rejection?.status,
    errors: handlerErrors,
    matches: (record) => Boolean(record.requestId && injectedNetworkIds.has(record.requestId)) && Boolean(record.errorText || record.status >= 400),
    async restore() { await cdp.send('Fetch.disable').catch(() => undefined); },
  };
};

export const markNetwork = (report, records, faultControllers = []) => {
  report.network.push(...records);
  report.assetFailures.push(...records.filter((record) => record.kind === 'asset-failure'));
  const unexpected = records.filter((record) => classifyNetworkRecord(record) === 'unexpected-error');
  if (unexpected.length) {
    recordCheck(report, 'correctness', 'no unexpected browser or network failures', false, { failures: unexpected });
  } else {
    recordCheck(report, 'correctness', 'no unexpected browser or network failures', true, { observed: records.length });
  }
};

export const markFaultRecords = (report, faultControllers) => {
  for (const controller of faultControllers) {
    if (controller.count() === 0) recordCheck(report, 'correctness', 'specific read fault was injected', false, { errors: controller.errors });
  }
};

export const goto = async (cdp, url) => navigate(cdp, url);

export const recordBrowserTiming = (report, cdp, options) => perform(report, cdp, options);

const redactError = (value) => String(value ?? '')
  .replace(/https?:\/\/[^\s\"'<>]+/g, (value) => { try { const url = new URL(value); return url.origin + url.pathname; } catch { return '[url]'; } })
  .replace(/Bearer\s+[^\s]+/gi, 'Bearer [redacted]');

export const collectPageDiagnostics = async (cdp) => {
  const value = await evaluate(cdp, "(()=>({longTasks:window.__loomVerifyLongTasks||[],pageErrors:window.__loomVerifyPageErrors||[],navigationDurationMs:(()=>{const entry=performance.getEntriesByType('navigation')[0];return entry?Math.round(entry.duration):undefined})()}))()");
  return { longTasks: value.longTasks, pageErrors: value.pageErrors.map(redactError), navigationDurationMs: value.navigationDurationMs };
};

export const addEvidencePath = (report, path) => {
  report.evidence.push(path);
  return path;
};

export { evaluate };
