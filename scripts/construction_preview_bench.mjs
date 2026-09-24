#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const DEFAULT_CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const AUTH_HEADERS = new Set(['authorization', 'cookie', 'set-cookie']);
const SAFE_TIMING_KEYS = new Set([
  'cachehit', 'cachereused', 'cachemiss', 'compilationms', 'compilems', 'contextresolutionms',
  'queryms', 'querydurationms', 'queryrowsread', 'rowsread', 'rowsscanned', 'bytesread',
  'serializationms', 'serializems', 'transportms', 'reusedinputs', 'reusedstages',
]);
const SAFE_IDENTITY_KEYS = new Set(['proposalid', 'receiptid', 'resolutionid', 'draftversion', 'draftdigest', 'outputid']);
const SAFE_TIMING_HEADERS = new Set([
  'server-timing', 'x-cache', 'x-query-cache', 'x-preview-cache', 'x-query-rows-read',
  'x-query-bytes-read', 'x-query-duration-ms', 'x-request-id', 'x-reqid',
]);

class UsageError extends Error {}

function parseArgs(argv) {
  const options = {
    command: 'run',
    tokenFile: process.env.LOOM_E2E_TOKEN_FILE ?? '',
    chrome: process.env.LOOM_E2E_CHROME ?? DEFAULT_CHROME,
    scenarioPath: '',
    apiUrl: process.env.LOOM_BENCH_API_URL ?? '',
    pageUrl: process.env.LOOM_BENCH_PAGE_URL ?? '',
    samples: 10,
    concurrency: [1],
    artifacts: path.resolve('.artifacts/construction-preview'),
    noAuth: false,
    captureAssertionDom: false,
  };
  const args = [...argv];
  if (args[0] === 'doctor' || args[0] === 'run') options.command = args.shift();
  while (args.length) {
    const flag = args.shift();
    if (flag === '--no-auth') {
      options.noAuth = true;
      continue;
    }
    if (flag === '--capture-assertion-dom') {
      options.captureAssertionDom = true;
      continue;
    }
    const value = args.shift();
    if (!value || value.startsWith('--')) throw new UsageError(`missing value for ${flag}`);
    if (flag === '--token-file') options.tokenFile = value;
    else if (flag === '--chrome') options.chrome = value;
    else if (flag === '--scenario') options.scenarioPath = path.resolve(value);
    else if (flag === '--api-url') options.apiUrl = value;
    else if (flag === '--page-url') options.pageUrl = value;
    else if (flag === '--samples') options.samples = Number(value);
    else if (flag === '--concurrency') options.concurrency = parseConcurrency(value);
    else if (flag === '--artifacts') options.artifacts = path.resolve(value);
    else throw new UsageError(`unknown option ${flag}`);
  }
  if (!Number.isInteger(options.samples) || options.samples < 1 || options.samples > 1000) {
    throw new UsageError('--samples must be an integer from 1 to 1000');
  }
  if (options.command === 'run' && !options.scenarioPath) {
    throw new UsageError('run requires --scenario PATH');
  }
  return options;
}

function parseConcurrency(value) {
  const result = [...new Set(value.split(',').map(Number))].sort((a, b) => a - b);
  if (!result.length || result.some((count) => !Number.isInteger(count) || count < 1 || count > 32)) {
    throw new UsageError('--concurrency accepts comma-separated integers from 1 to 32');
  }
  return result;
}

function isLoopback(url) {
  return new Set(['127.0.0.1', 'localhost', '::1']).has(new URL(url).hostname);
}

async function loadAuthorization(options, targetUrls) {
  const token = process.env.LOOM_E2E_TOKEN?.trim();
  if (token) return /^bearer\s+/i.test(token) ? token : `Bearer ${token}`;
  if (options.noAuth) {
    if (targetUrls.some((url) => !isLoopback(url))) {
      throw new UsageError('--no-auth is allowed only for loopback page and API URLs');
    }
    return '';
  }
  if (!options.tokenFile) throw new UsageError('set LOOM_E2E_TOKEN or pass --token-file PATH');
  const info = await stat(options.tokenFile);
  if ((info.mode & 0o077) !== 0) {
    throw new UsageError(`token file must not be accessible by group or others: ${options.tokenFile}`);
  }
  const value = (await readFile(options.tokenFile, 'utf8')).trim();
  if (!value || /[\r\n]/.test(value)) throw new UsageError('token file must contain one non-empty token');
  return /^bearer\s+/i.test(value) ? value : `Bearer ${value}`;
}

function redactAuth(value, authorization) {
  if (typeof value !== 'string' || !authorization) return value;
  return value.split(authorization).join('[REDACTED]')
    .split(authorization.replace(/^Bearer\s+/i, '')).join('[REDACTED]');
}

function routeCategory(url) {
  const parts = new URL(url).pathname.toLowerCase().split('/').filter(Boolean);
  const route = parts.at(-1) ?? 'root';
  const joined = parts.join('/');
  if (/resolve|context/.test(joined)) return 'context-resolution';
  if (/capabilit|construction-choice|catalog|discovery|choices/.test(joined)) return 'capability-refinement';
  if (/reconcile|compile/.test(joined)) return 'compilation';
  if (/propos|preview|query/.test(joined)) return 'backend-preview-query';
  return route;
}

function safeRoute(url) {
  return routeCategory(url);
}

function parseServerTiming(value) {
  if (!value) return [];
  return value.split(',').map((entry) => {
    const [name, ...parameters] = entry.trim().split(';');
    const duration = parameters.map((item) => item.trim().match(/^dur=(\d+(?:\.\d+)?)$/i)?.[1])
      .find((item) => item !== undefined);
    return { name: name.trim(), durationMs: duration === undefined ? null : Number(duration) };
  }).filter((item) => item.name);
}

function normalizedKey(key) {
  return key.replace(/[^a-z0-9]/gi, '').toLowerCase();
}

function extractSafeMetrics(value, result = {}, depth = 0) {
  if (depth > 5 || value == null || typeof value !== 'object') return result;
  if (Array.isArray(value)) return result;
  for (const [key, item] of Object.entries(value)) {
    const normalized = normalizedKey(key);
    if (SAFE_TIMING_KEYS.has(normalized) && (typeof item === 'number' || typeof item === 'boolean' || typeof item === 'string')) {
      result[key] = item;
    } else if (item && typeof item === 'object' && !['rows', 'data', 'columns', 'cells', 'examples'].includes(normalized)) {
      extractSafeMetrics(item, result, depth + 1);
    }
  }
  return result;
}

function extractSafeIdentities(value, result = {}, depth = 0) {
  if (depth > 5 || value == null || typeof value !== 'object' || Array.isArray(value)) return result;
  for (const [key, item] of Object.entries(value)) {
    const normalized = normalizedKey(key);
    if (SAFE_IDENTITY_KEYS.has(normalized) && (typeof item === 'string' || typeof item === 'number')) {
      result[key] = String(item);
    } else if (item && typeof item === 'object' && !['rows', 'data', 'columns', 'cells', 'examples'].includes(normalized)) {
      extractSafeIdentities(item, result, depth + 1);
    }
  }
  return result;
}

function hash(value) {
  return createHash('sha256').update(JSON.stringify(value)).digest('hex');
}

function median(values) {
  const ordered = [...values].sort((a, b) => a - b);
  const middle = Math.floor(ordered.length / 2);
  return ordered.length % 2 ? ordered[middle] : (ordered[middle - 1] + ordered[middle]) / 2;
}

function percentile(values, fraction) {
  const ordered = [...values].sort((a, b) => a - b);
  return ordered[Math.max(0, Math.ceil(fraction * ordered.length) - 1)];
}

function distribution(values) {
  const center = median(values);
  const variance = values.reduce((sum, value) => sum + ((value - center) ** 2), 0) / values.length;
  return {
    count: values.length,
    medianMs: Number(center.toFixed(2)),
    p95Ms: Number(percentile(values, 0.95).toFixed(2)),
    minMs: Number(Math.min(...values).toFixed(2)),
    maxMs: Number(Math.max(...values).toFixed(2)),
    medianAbsoluteDeviationMs: Number(median(values.map((value) => Math.abs(value - center))).toFixed(2)),
    spreadMs: Number(Math.sqrt(variance).toFixed(2)),
  };
}

function requireString(value, label) {
  if (typeof value !== 'string' || value.trim() === '') throw new UsageError(`${label} is required`);
}

function validateStep(step, label) {
  if (!step || typeof step !== 'object' || Array.isArray(step)) throw new UsageError(`${label} must be an action object`);
  if (!['click', 'fill', 'select', 'wait', 'waitFor'].includes(step.type)) {
    throw new UsageError(`${label}.type must be click, fill, select, wait, or waitFor`);
  }
  if (step.type !== 'wait') requireString(step.selector, `${label}.selector`);
  if (step.type === 'fill') {
    if (typeof step.value !== 'string') throw new UsageError(`${label}.value must be a string`);
  }
  if (step.type === 'select' && typeof step.value !== 'string' && typeof step.optionText !== 'string') {
    throw new UsageError(`${label} select needs value or optionText`);
  }
  if (step.type === 'wait' && (!Number.isFinite(step.ms) || step.ms < 0 || step.ms > 30000)) {
    throw new UsageError(`${label}.ms must be between 0 and 30000`);
  }
}

function validateWorkload(workload) {
  requireString(workload.id, 'workload.id');
  requireString(workload.family, 'workload.family');
  const identity = workload.identity;
  if (!identity || typeof identity !== 'object') throw new UsageError(`${workload.id}.identity is required`);
  requireString(identity.sourceSnapshot, `${workload.id}.identity.sourceSnapshot`);
  for (const name of ['inputRows', 'outputWidth', 'constructionDepth', 'previewLimit']) {
    if (!Number.isInteger(identity[name]) || identity[name] < 0) {
      throw new UsageError(`${workload.id}.identity.${name} must be a non-negative integer`);
    }
  }
  if (identity.inputRows < 1 || identity.outputWidth < 1 || identity.constructionDepth < 1 || identity.previewLimit < 1) {
    throw new UsageError(`${workload.id}.identity row count, width, depth, and preview limit must be positive`);
  }
  if (!identity.categoryCardinality || typeof identity.categoryCardinality !== 'object') {
    throw new UsageError(`${workload.id}.identity.categoryCardinality must describe measured cardinalities`);
  }
  requireString(identity.matchMultiplicity, `${workload.id}.identity.matchMultiplicity`);
  if (!Array.isArray(workload.samples) || workload.samples.length < 1) {
    throw new UsageError(`${workload.id}.samples must contain at least one sample variant`);
  }
  for (const [index, sample] of workload.samples.entries()) {
    requireString(sample.id, `${workload.id}.samples[${index}].id`);
    if (!Array.isArray(sample.request) || sample.request.length === 0) {
      throw new UsageError(`${workload.id}.samples[${index}].request must contain the user action that requests preview`);
    }
    for (const [stepIndex, step] of [...(sample.setup ?? []), ...sample.request].entries()) {
      validateStep(step, `${workload.id}.samples[${index}].steps[${stepIndex}]`);
    }
    const tables = sample.expectedTables ?? [{ columns: sample.expectedColumns, rows: sample.expectedRows }];
    if (!Array.isArray(tables) || tables.length === 0 || tables.some((table) =>
      !Array.isArray(table.columns) || !Array.isArray(table.rows)
      || table.rows.some((row) => !Array.isArray(row) || row.length !== table.columns.length))) {
      throw new UsageError(`${workload.id}.samples[${index}] expected tables need matching columns and rows`);
    }
  }
  for (const [index, step] of (workload.prepare ?? []).entries()) validateStep(step, `${workload.id}.prepare[${index}]`);
  for (const [index, step] of (workload.cleanup ?? []).entries()) validateStep(step, `${workload.id}.cleanup[${index}]`);
  const completion = workload.completion;
  if (!completion || typeof completion !== 'object') throw new UsageError(`${workload.id}.completion is required`);
  for (const field of ['rootSelector', 'applySelector']) {
    requireString(completion[field], `${workload.id}.completion.${field}`);
  }
  if (!completion.tableSelector && !completion.tablesSelector) {
    throw new UsageError(`${workload.id}.completion needs tableSelector or tablesSelector`);
  }
  if (workload.supersession) {
    if (!Array.isArray(workload.supersession.actions) || workload.supersession.actions.length < 2) {
      throw new UsageError(`${workload.id}.supersession.actions must contain at least two action bursts`);
    }
    if (!workload.supersession.expected || !Array.isArray(workload.supersession.expected.columns) || !Array.isArray(workload.supersession.expected.rows)) {
      throw new UsageError(`${workload.id}.supersession.expected needs columns and rows`);
    }
    for (const [i, burst] of workload.supersession.actions.entries()) {
      if (!Array.isArray(burst) || burst.length === 0) throw new UsageError(`${workload.id}.supersession.actions[${i}] must not be empty`);
      for (const [j, step] of burst.entries()) validateStep(step, `${workload.id}.supersession.actions[${i}][${j}]`);
    }
  }
}

async function loadScenario(options) {
  const value = JSON.parse(await readFile(options.scenarioPath, 'utf8'));
  if (value.schemaVersion !== 1) throw new UsageError('scenario.schemaVersion must be 1');
  if (!Array.isArray(value.workloads) || value.workloads.length === 0) {
    throw new UsageError('scenario.workloads must be a non-empty array');
  }
  for (const workload of value.workloads) validateWorkload(workload);
  const pageUrl = options.pageUrl || value.target?.pageUrl;
  const apiUrl = options.apiUrl || value.target?.apiUrl;
  requireString(pageUrl, 'target.pageUrl / --page-url');
  requireString(apiUrl, 'target.apiUrl / --api-url');
  return { pageUrl, apiUrl, workloads: value.workloads, metadata: value.metadata ?? {} };
}

async function requestBuilder(apiUrl, authorization) {
  const headers = { accept: 'application/json' };
  if (authorization) headers.authorization = authorization;
  const response = await fetch(apiUrl, {
    headers,
    redirect: 'manual',
    signal: AbortSignal.timeout(15000),
  });
  const bodyText = await response.text();
  let body;
  try { body = JSON.parse(bodyText); } catch { body = null; }
  const state = body?.kind === 'ExplorerBuilderState' && body?.workspace;
  return {
    status: response.status,
    ready: response.ok && Boolean(state),
    apiVersion: body?.apiVersion ?? null,
    kind: body?.kind ?? null,
    lifecycleState: body?.lifecycleState ?? null,
    draftVersion: Number.isInteger(body?.draftVersion) ? body.draftVersion : null,
    catalogComplete: typeof body?.catalog?.complete === 'boolean' ? body.catalog.complete : null,
  };
}

class CDPClient {
  constructor(socket) {
    this.socket = socket;
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Map();
    socket.addEventListener('message', (event) => this.receive(event.data));
  }

  static async connect(url) {
    const socket = new WebSocket(url);
    await new Promise((resolve, reject) => {
      socket.addEventListener('open', resolve, { once: true });
      socket.addEventListener('error', reject, { once: true });
    });
    return new CDPClient(socket);
  }

  receive(raw) {
    const message = JSON.parse(raw);
    if (message.id) {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(`${pending.method}: ${message.error.message}`));
      else pending.resolve(message.result ?? {});
      return;
    }
    for (const listener of this.listeners.get(message.method) ?? []) listener(message.params ?? {});
  }

  send(method, params = {}) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { method, resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  on(method, listener) {
    const listeners = this.listeners.get(method) ?? [];
    listeners.push(listener);
    this.listeners.set(method, listeners);
  }

  close() { this.socket.close(); }
}

async function waitForFile(file, child, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode != null) throw new Error(`Chrome exited before DevTools was ready (${child.exitCode})`);
    try { return await readFile(file, 'utf8'); } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 75));
  }
  throw new Error('Chrome did not publish its DevTools port within 10 seconds');
}

class BrowserSession {
  static async start(options, authorization) {
    const profile = await mkdtemp(path.join(tmpdir(), 'construction-preview-bench-'));
    const chrome = spawn(options.chrome, [
      '--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`,
      '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
      '--disable-component-update', '--disable-sync', '--ignore-certificate-errors', 'about:blank',
    ], { stdio: 'ignore' });
    let client;
    try {
      const activePort = await waitForFile(path.join(profile, 'DevToolsActivePort'), chrome);
      const port = activePort.split(/\r?\n/, 1)[0];
      const target = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' }).then((res) => res.json());
      client = await CDPClient.connect(target.webSocketDebuggerUrl);
      const session = new BrowserSession(options, authorization, profile, chrome, client);
      await session.initialize();
      return session;
    } catch (error) {
      client?.close();
      chrome.kill('SIGTERM');
      await rm(profile, { recursive: true, force: true });
      throw error;
    }
  }

  constructor(options, authorization, profile, chrome, client) {
    this.options = options;
    this.authorization = authorization;
    this.profile = profile;
    this.chrome = chrome;
    this.client = client;
    this.requests = [];
    this.requestById = new Map();
    this.bodyTasks = new Set();
    this.allowedOrigins = [...new Set([new URL(options.pageUrl).origin, new URL(options.apiUrl).origin])];
  }

  async initialize() {
    this.client.on('Network.requestWillBeSent', ({ requestId, request, type, timestamp }) => {
      const item = { requestId, route: safeRoute(request.url), category: routeCategory(request.url), type, startTimestamp: timestamp, response: null, failed: null, metrics: null, identities: null };
      this.requests.push(item);
      this.requestById.set(requestId, item);
    });
    this.client.on('Network.responseReceived', ({ requestId, response }) => {
      const item = this.requestById.get(requestId);
      if (!item) return;
      const headers = Object.fromEntries(Object.entries(response.headers ?? {}).map(([key, value]) => [key.toLowerCase(), String(value)]));
      item.response = {
        status: response.status,
        mimeType: response.mimeType,
        serverTiming: parseServerTiming(headers['server-timing']),
        headers: Object.fromEntries([...SAFE_TIMING_HEADERS].filter((key) => headers[key] !== undefined).map((key) => [key, redactAuth(headers[key], this.authorization)])),
        timestamp: response.responseTime,
      };
    });
    this.client.on('Network.loadingFailed', ({ requestId, errorText, canceled }) => {
      const item = this.requestById.get(requestId);
      if (item) item.failed = { errorText: redactAuth(errorText, this.authorization), canceled: Boolean(canceled) };
    });
    this.client.on('Network.loadingFinished', ({ requestId, timestamp, encodedDataLength }) => {
      const item = this.requestById.get(requestId);
      if (!item) return;
      item.finishedTimestamp = timestamp;
      item.encodedDataLength = encodedDataLength;
      if (item.response?.status >= 200 && /preview|proposal|query/i.test(item.route)) {
        const task = this.captureSafeBodyMetrics(requestId, item).finally(() => this.bodyTasks.delete(task));
        this.bodyTasks.add(task);
      }
    });
    await Promise.all([
      this.client.send('Page.enable'),
      this.client.send('Runtime.enable'),
      this.client.send('Network.enable'),
      this.client.send('Fetch.enable', { patterns: this.allowedOrigins.map((origin) => ({ urlPattern: `${origin}/*` })) }),
    ]);
    this.client.on('Fetch.requestPaused', ({ requestId, request }) => {
      const headers = Object.entries(request.headers)
        .filter(([name]) => !AUTH_HEADERS.has(name.toLowerCase()))
        .map(([name, value]) => ({ name, value: String(value) }));
      if (this.authorization && this.allowedOrigins.includes(new URL(request.url).origin)) {
        headers.push({ name: 'Authorization', value: this.authorization });
      }
      this.client.send('Fetch.continueRequest', { requestId, headers }).catch(() => {});
    });
    this.startedAt = Date.now();
    await this.client.send('Page.navigate', { url: this.options.pageUrl });
    await this.waitForDocument();
    this.browserVersion = await this.client.send('Browser.getVersion');
  }

  async captureSafeBodyMetrics(requestId, item) {
    try {
      const result = await this.client.send('Network.getResponseBody', { requestId });
      const raw = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      const value = JSON.parse(raw);
      item.metrics = extractSafeMetrics(value);
      item.identities = extractSafeIdentities(value);
    } catch {
      item.metrics = null;
    }
  }

  async evaluate(expression, awaitPromise = false) {
    const result = await this.client.send('Runtime.evaluate', {
      expression,
      returnByValue: true,
      awaitPromise,
      userGesture: true,
    });
    if (result.exceptionDetails) {
      const detail = result.exceptionDetails.exception?.description
        ?? result.exceptionDetails.text
        ?? 'unknown exception';
      throw new Error(`browser evaluation failed: ${detail.split('\n')[0]}`);
    }
    return result.result?.value;
  }

  async waitForDocument(timeoutMs = 30000) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const ready = await this.evaluate('document.readyState === "complete"');
      if (ready) return;
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    throw new Error('page load timed out after 30 seconds');
  }

  async action(step) {
    if (step.type === 'wait') {
      await new Promise((resolve) => setTimeout(resolve, step.ms));
      return;
    }
    const selector = JSON.stringify(step.selector);
    if (step.type === 'waitFor') {
      const deadline = Date.now() + (step.timeoutMs ?? 15000);
      while (Date.now() < deadline) {
        const found = await this.evaluate(`(() => { const el = document.querySelector(${selector}); return Boolean(el && el.getClientRects().length && (${step.enabled === true} ? (!el.disabled && el.getAttribute('aria-disabled') !== 'true') : true)); })()`);
        if (found) return;
        await new Promise((resolve) => setTimeout(resolve, 25));
      }
      const state = await this.evaluate(`(() => { const el = document.querySelector(${selector}); return { present: Boolean(el), visible: Boolean(el?.getClientRects().length), disabled: el ? Boolean(el.disabled || el.getAttribute('aria-disabled') === 'true') : null, tagName: el?.tagName ?? null, options: el instanceof HTMLSelectElement ? [...el.options].map((option) => option.textContent.trim()) : undefined }; })()`);
      throw new Error(`timed out waiting for browser control ${step.selector}; requireEnabled=${step.enabled === true}; state=${JSON.stringify(state)}`);
    }
    let operation;
    if (step.type === 'click') {
      operation = `(() => { const el = document.querySelector(${selector}); if (!el) throw new Error('selector not found'); if (el.disabled || el.getAttribute('aria-disabled') === 'true') throw new Error('control is disabled'); el.click(); return true; })()`;
    } else if (step.type === 'fill') {
      const value = JSON.stringify(step.value);
      operation = `(() => { const el = document.querySelector(${selector}); if (!el) throw new Error('selector not found'); if (el.disabled) throw new Error('control is disabled'); const proto = Object.getPrototypeOf(el); const descriptor = Object.getOwnPropertyDescriptor(proto, 'value'); if (!descriptor?.set) throw new Error('control has no value setter'); descriptor.set.call(el, ${value}); el.dispatchEvent(new InputEvent('input', {bubbles:true, inputType:'insertText', data:${value}})); el.dispatchEvent(new Event('change', {bubbles:true})); return true; })()`;
    } else {
      const value = JSON.stringify(step.value ?? '');
      const optionText = JSON.stringify(step.optionText ?? '');
      operation = `(() => { const el = document.querySelector(${selector}); if (!(el instanceof HTMLSelectElement)) throw new Error('select target is not a native select'); if (el.disabled) throw new Error('control is disabled'); const option = ${optionText} ? [...el.options].find((item) => item.textContent.trim() === ${optionText}) : null; if (${optionText} && !option) throw new Error('select option text not found'); const next = option ? option.value : ${value}; if (![...el.options].some((item) => item.value === next && !item.disabled)) throw new Error('select option not found or disabled'); el.value = next; el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); return true; })()`;
    }
    let result;
    try {
      result = await this.evaluate(operation);
    } catch (error) {
      throw new Error(`${step.type} failed for ${step.selector}: ${error.message}`);
    }
    if (!result) throw new Error(`${step.type} action did not complete`);
  }

  async actions(steps = []) {
    for (const step of steps) await this.action(step);
  }

  async captureAssertionDom(config, workloadId, reason) {
    if (!this.options.captureAssertionDom) return null;
    const configValue = JSON.stringify(config);
    const snapshot = await this.evaluate(`(() => {
      const config = ${configValue};
      const root = document.querySelector(config.rootSelector);
      const text = (element) => element?.innerText?.trim() ?? '';
      const selector = config.tablesSelector || config.tableSelector;
      const tables = root && config.tablesSelector
        ? [...root.querySelectorAll(selector)]
        : [root?.querySelector(selector) || document.querySelector(selector)].filter(Boolean);
      return {
        page: location.origin + location.pathname,
        title: document.title,
        rootPresent: Boolean(root),
        rootTestId: root?.getAttribute('data-testid') ?? null,
        applyEnabled: Boolean((() => { const apply = document.querySelector(config.applySelector); return apply && !apply.disabled && apply.getAttribute('aria-disabled') !== 'true'; })()),
        tables: tables.map((table) => {
          const group = config.rowGroupSelector ? table.closest(config.rowGroupSelector) : null;
          return {
            rowIdentity: config.rowIdentitySelector ? text(group?.querySelector(config.rowIdentitySelector)) : null,
            headers: [...table.querySelectorAll(config.headerSelector || 'thead th')].filter((el) => el.getClientRects().length).map(text),
            rows: [...table.querySelectorAll(config.rowSelector || 'tbody tr')].filter((row) => row.getClientRects().length).map((row) => [...row.querySelectorAll(config.cellSelector || 'th, td')].filter((cell) => cell.getClientRects().length).map(text)),
          };
        }),
      };
    })()`);
    if (!this.options.artifactDir) return { ...snapshot, reason };
    const filename = `assertion-failure-${hash(workloadId).slice(0, 12)}-${Date.now()}.json`;
    const output = path.join(this.options.artifactDir, filename);
    await writeFile(output, `${JSON.stringify({ schemaVersion: 1, workloadId, reason, capturedAt: new Date().toISOString(), ...snapshot }, null, 2)}\n`, { mode: 0o600 });
    return output;
  }

  async close() {
    this.client.close();
    if (this.chrome.exitCode == null) this.chrome.kill('SIGTERM');
    await new Promise((resolve) => {
      if (this.chrome.exitCode != null) resolve();
      else {
        this.chrome.once('exit', resolve);
        setTimeout(() => {
          if (this.chrome.exitCode == null) this.chrome.kill('SIGKILL');
          resolve();
        }, 3000).unref();
      }
    });
    await rm(this.profile, { recursive: true, force: true });
  }
}

function completionExpression(config, expected, previousIdentity, requireIdentityChange) {
  const encoded = JSON.stringify({ config, expected, previousIdentity, requireIdentityChange });
  return `(() => {
    const args = ${encoded};
    const root = document.querySelector(args.config.rootSelector);
    if (!root || !root.getClientRects().length) return null;
    const status = args.config.statusAttribute ? root.getAttribute(args.config.statusAttribute) : null;
    const identity = args.config.identityAttribute ? (root.getAttribute(args.config.identityAttribute) || '') : '';
    const apply = document.querySelector(args.config.applySelector);
    const selector = args.config.tablesSelector || args.config.tableSelector;
    const tables = args.config.tablesSelector
      ? [...root.querySelectorAll(selector)]
      : [root.querySelector(selector) || document.querySelector(selector)].filter(Boolean);
    if (tables.length === 0 || tables.some((table) => !table.getClientRects().length)) return null;
    const readTable = (table) => {
      const headers = [...table.querySelectorAll(args.config.headerSelector || 'thead th')]
        .filter((el) => el.getClientRects().length).map((el) => el.innerText.trim());
      const rows = [...table.querySelectorAll(args.config.rowSelector || 'tbody tr')]
        .filter((row) => row.getClientRects().length)
        .map((row) => [...row.querySelectorAll(args.config.cellSelector || 'th, td')]
          .filter((cell) => cell.getClientRects().length).map((cell) => cell.innerText.trim()));
      const group = args.config.rowGroupSelector ? table.closest(args.config.rowGroupSelector) : null;
      const rowIdentity = args.config.rowIdentitySelector ? group?.querySelector(args.config.rowIdentitySelector)?.innerText.trim() : null;
      if (rowIdentity !== null && rowIdentity !== undefined) {
        headers.unshift(args.config.rowIdentityHeader || 'Row');
        for (const row of rows) row.unshift(rowIdentity);
      }
      return { headers, rows };
    };
    const renderedTables = tables.map(readTable);
    const expectedTables = args.expected.tables || [{ columns: args.expected.columns, rows: args.expected.rows }];
    const cellMatches = (actual, expected) => expected === '$any'
      ? typeof actual === 'string' && actual.length > 0
      : actual === expected;
    const rowMatches = (actual, expected) => actual.length === expected.length
      && actual.every((value, index) => cellMatches(value, expected[index]));
    const tablesMatch = renderedTables.length === expectedTables.length && renderedTables.every((table, index) =>
      table.headers.length === expectedTables[index].columns.length
      && table.headers.every((value, headerIndex) => cellMatches(value, expectedTables[index].columns[headerIndex]))
      && table.rows.length === expectedTables[index].rows.length
      && table.rows.every((row, rowIndex) => rowMatches(row, expectedTables[index].rows[rowIndex])));
    const statusReady = !args.config.statusAttribute || status === (args.config.readyValue || 'ready');
    const identityReady = !args.config.identityAttribute || (identity.length > 0
      && (!args.requireIdentityChange || identity !== args.previousIdentity));
    const ready = statusReady && identityReady
      && apply && !apply.disabled && apply.getAttribute('aria-disabled') !== 'true'
      && renderedTables.length > 0;
    return ready ? { identity, tables: renderedTables, matchesExpected: tablesMatch, completedAt: performance.now() } : null;
  })()`;
}

async function waitForCompletion(session, config, expected, previousIdentity, timeoutMs = 30000, requireIdentityChange = true) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const result = await session.evaluate(completionExpression(config, expected, previousIdentity, requireIdentityChange));
    if (result) return result;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  const failure = await session.evaluate(`(() => {
    const root = document.querySelector(${JSON.stringify(config.rootSelector)});
    const apply = document.querySelector(${JSON.stringify(config.applySelector)});
    const tableSelector = ${JSON.stringify(config.tablesSelector || config.tableSelector || '')};
    const table = root?.querySelector(tableSelector) || document.querySelector(tableSelector);
    return {
      rootVisible: Boolean(root?.getClientRects().length),
      status: ${config.statusAttribute ? `root?.getAttribute(${JSON.stringify(config.statusAttribute)}) ?? null` : 'null'},
      identityPresent: ${config.identityAttribute ? `Boolean(root?.getAttribute(${JSON.stringify(config.identityAttribute)}))` : 'false'},
      applyEnabled: Boolean(apply && !apply.disabled && apply.getAttribute('aria-disabled') !== 'true'),
      tableVisible: Boolean(table?.getClientRects().length),
    };
  })()`);
  const evidence = await session.captureAssertionDom(config, 'timeout', 'preview did not reach a rendered, applicable comparison');
  throw new Error(`preview did not reach a rendered, applicable comparison before timeout; checks=${JSON.stringify(failure)}${evidence ? `; assertionDom=${typeof evidence === 'string' ? evidence : 'captured'}` : ''}`);
}

async function measureOne(session, workload, sample, lane, sampleIndex) {
  await session.actions(workload.prepare);
  const completion = workload.completion;
  const previousIdentity = completion.identityAttribute
    ? await session.evaluate(`document.querySelector(${JSON.stringify(completion.rootSelector)})?.getAttribute(${JSON.stringify(completion.identityAttribute)}) || ''`)
    : '';
  const requestOffset = session.requests.length;
  const startAt = await session.evaluate('performance.now()');
  await session.actions(sample.setup);
  await session.actions(sample.request);
  const expected = sample.expectedTables
    ? { tables: sample.expectedTables }
    : { columns: sample.expectedColumns, rows: sample.expectedRows };
  const rendered = await waitForCompletion(session, completion, expected, previousIdentity, workload.timeoutMs ?? 45000, workload.requireIdentityChange !== false);
  await Promise.allSettled([...session.bodyTasks]);
  const endToEndMs = rendered.completedAt - startAt;
  const resources = await session.evaluate(`performance.getEntriesByType('resource').filter((entry) => entry.startTime >= ${startAt}).map((entry) => {
    let url; try { url = new URL(entry.name); } catch { return null; }
    return { origin: url.origin, route: url.pathname.toLowerCase().split('/').filter(Boolean).at(-1) || 'root', category: /resolve|context/i.test(url.pathname) ? 'context-resolution' : /capabilit|choice|catalog|discovery/i.test(url.pathname) ? 'capability-refinement' : /reconcile|compile/i.test(url.pathname) ? 'compilation' : /propos|preview|query/i.test(url.pathname) ? 'backend-preview-query' : 'other', startMs: entry.startTime, durationMs: entry.duration, responseEndMs: entry.responseEnd, transferBytes: entry.transferSize, encodedBytes: entry.encodedBodySize, decodedBytes: entry.decodedBodySize };
  }).filter((entry) => entry && [${session.allowedOrigins.map((origin) => JSON.stringify(origin)).join(',')}].includes(entry.origin))`);
  const requests = session.requests.slice(requestOffset).map((request) => ({
    route: request.route,
    category: request.category,
    type: request.type,
    status: request.response?.status ?? null,
    canceled: Boolean(request.failed?.canceled),
    failure: request.failed?.canceled ? 'canceled' : request.failed ? 'failed' : null,
    serverTiming: request.response?.serverTiming ?? [],
    safeResponseMetrics: request.metrics,
    backendIdentityKinds: Object.keys(request.identities ?? {}),
    backendIdentitySha256: Object.fromEntries(Object.entries(request.identities ?? {}).map(([key, value]) => [key, hash(value)])),
    safeTimingHeaders: request.response?.headers ?? {},
    encodedDataLength: request.encodedDataLength ?? null,
    durationMs: Number.isFinite(request.startTimestamp) && Number.isFinite(request.finishedTimestamp)
      ? Number(((request.finishedTimestamp - request.startTimestamp) * 1000).toFixed(2))
      : null,
  }));
  const expectedTables = sample.expectedTables ?? [{ columns: sample.expectedColumns, rows: sample.expectedRows }];
  const expectedHash = hash(expectedTables);
  const observedTables = rendered.tables.map((table) => ({ columns: table.headers, rows: table.rows }));
  const observedHash = hash(observedTables);
  const correct = rendered.matchesExpected;
  if (!correct) {
    const evidence = await session.captureAssertionDom(completion, workload.id, `visible rows differed; expectedSha256=${expectedHash} actualSha256=${observedHash}`);
    throw new Error(`visible preview rows differ from expected rows; expectedSha256=${expectedHash} actualSha256=${observedHash}${evidence ? `; assertionDom=${typeof evidence === 'string' ? evidence : 'captured'}` : ''}`);
  }
  const backendIdentity = completion.networkIdentity
    ? session.requests.slice(requestOffset).map((request) => request.identities?.[completion.networkIdentity]).filter(Boolean).at(-1)
    : undefined;
  if (!rendered.identity && completion.networkIdentity && !backendIdentity) {
    throw new Error(`preview response omitted the configured ${completion.networkIdentity} identity`);
  }
  if (rendered.identity && backendIdentity && rendered.identity !== backendIdentity) {
    throw new Error('visible preview identity does not match the latest backend identity');
  }
  const previewIdentity = rendered.identity || backendIdentity || '';
  await session.actions(workload.cleanup);
  return {
    workloadId: workload.id,
    sampleId: sample.id,
    lane,
    sampleIndex,
    endToEndMs: Number(endToEndMs.toFixed(2)),
    rowCount: rendered.tables.reduce((count, table) => count + table.rows.length, 0),
    outputWidth: Math.max(0, ...rendered.tables.map((table) => table.headers.length)),
    rowSchemaSha256: hash(rendered.tables.map((table) => table.headers)),
    visibleRowsSha256: observedHash,
    previewIdentitySha256: previewIdentity ? hash(previewIdentity) : null,
    identityObservedInDOM: Boolean(rendered.identity),
    backendIdentityObserved: Boolean(backendIdentity),
    correctRows: true,
    applicable: true,
    previewIdentityChanged: rendered.identity !== previousIdentity,
    requestCount: requests.length,
    cancelledRequests: requests.filter((request) => request.canceled).length,
    failedRequests: requests.filter((request) => request.failure === 'failed').length,
    resourceTiming: resources.map(({ origin, ...entry }) => entry),
    requests,
  };
}

async function runSupersessionProbe(session, workload) {
  const probe = workload.supersession;
  if (!probe) return null;
  await session.actions(workload.prepare);
  const previousIdentity = workload.completion.identityAttribute
    ? await session.evaluate(`document.querySelector(${JSON.stringify(workload.completion.rootSelector)})?.getAttribute(${JSON.stringify(workload.completion.identityAttribute)}) || ''`)
    : '';
  const requestOffset = session.requests.length;
  const startAt = await session.evaluate('performance.now()');
  await session.actions(probe.setup ?? []);
  for (const [index, burst] of probe.actions.entries()) {
    await session.actions(burst);
    if (index < probe.actions.length - 1) await new Promise((resolve) => setTimeout(resolve, probe.gapMs ?? 40));
  }
  const rendered = await waitForCompletion(session, workload.completion, probe.expected, previousIdentity, probe.timeoutMs ?? workload.timeoutMs ?? 45000, workload.requireIdentityChange !== false);
  const endToEndMs = rendered.completedAt - startAt;
  await Promise.allSettled([...session.bodyTasks]);
  const requests = session.requests.slice(requestOffset);
  const result = {
    workloadId: workload.id,
    endToEndMs: Number(endToEndMs.toFixed(2)),
    finalPreviewIdentityChanged: rendered.identity !== previousIdentity,
    correctLatestRows: rendered.matchesExpected,
    canceledRequests: requests.filter((request) => request.failed?.canceled).length,
    failedRequests: requests.filter((request) => request.failed && !request.failed.canceled).length,
    requests: requests.map((request) => ({ route: request.route, category: request.category, status: request.response?.status ?? null, canceled: Boolean(request.failed?.canceled) })),
  };
  await session.actions(workload.cleanup);
  if (!result.correctLatestRows) throw new Error(`supersession probe rendered incorrect latest rows for ${workload.id}`);
  return result;
}

function summarizeRuns(samples) {
  const byLane = {};
  for (const lane of ['cold-client', 'warm-client']) {
    const values = samples.filter((sample) => sample.lane === lane).map((sample) => sample.endToEndMs);
    if (values.length) byLane[lane] = distribution(values);
  }
  const requestBuckets = new Map();
  for (const sample of samples) {
    for (const request of sample.requests) {
      const values = requestBuckets.get(request.category) ?? [];
      if (request.durationMs != null) values.push(request.durationMs);
      requestBuckets.set(request.category, values);
    }
  }
  const backendByCategory = Object.fromEntries([...requestBuckets.entries()]
    .filter(([, values]) => values.length)
    .map(([category, values]) => [category, distribution(values)]));
  return { endToEndByCacheLabel: byLane, requestDurationByCategory: backendByCategory };
}

async function createArtifactDir(root) {
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const directory = path.join(root, stamp);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  await chmod(directory, 0o700);
  return directory;
}

async function doctor(options, authorization) {
  requireString(options.apiUrl, '--api-url');
  requireString(options.pageUrl, '--page-url');
  const api = await requestBuilder(options.apiUrl, authorization);
  const report = {
    schemaVersion: 1,
    result: api.ready ? 'DEV_PREVIEW_DOCTOR_PASSED' : api.status === 401 ? 'AUTH_REQUIRED' : api.status === 403 ? 'FORBIDDEN' : 'API_OR_CONTRACT_FAILED',
    checkedAt: new Date().toISOString(),
    target: { pageOrigin: new URL(options.pageUrl).origin, apiOrigin: new URL(options.apiUrl).origin },
    api,
    browserOpened: false,
  };
  console.log(JSON.stringify(report, null, 2));
  if (!api.ready) process.exitCode = 1;
}

async function run(options, authorization, scenario) {
  const preflight = await requestBuilder(scenario.apiUrl, authorization);
  if (!preflight.ready) {
    throw new Error(`Builder API preflight failed with status=${preflight.status} kind=${preflight.kind ?? 'missing'} lifecycle=${preflight.lifecycleState ?? 'missing'}`);
  }
  const artifactDir = await createArtifactDir(options.artifacts);
  const startedAt = new Date().toISOString();
  const results = [];
  const probes = [];
  let browserDetails = null;
  for (const workload of scenario.workloads) {
    for (const concurrency of options.concurrency) {
      const sessions = [];
      try {
        for (let index = 0; index < concurrency; index += 1) {
          sessions.push(await BrowserSession.start({ ...options, pageUrl: scenario.pageUrl, apiUrl: scenario.apiUrl, artifactDir }, authorization));
        }
        browserDetails ??= {
          product: sessions[0].browserVersion.product,
          userAgent: sessions[0].browserVersion.userAgent,
          protocolVersion: sessions[0].browserVersion.protocolVersion,
        };
        for (let sampleIndex = 0; sampleIndex < options.samples; sampleIndex += 1) {
          const sample = workload.samples[sampleIndex % workload.samples.length];
          const lane = sampleIndex === 0 ? 'cold-client' : 'warm-client';
          const batch = await Promise.all(sessions.map((session) => measureOne(session, workload, sample, lane, sampleIndex)));
          results.push(...batch.map((item) => ({ ...item, concurrency })));
        }
        if (workload.supersession) {
          const probe = await runSupersessionProbe(sessions[0], workload);
          probes.push({ ...probe, concurrency });
        }
      } finally {
        await Promise.all(sessions.map((session) => session.close()));
      }
    }
  }
  const groups = {};
  for (const workload of scenario.workloads) {
    groups[workload.id] = {};
    for (const concurrency of options.concurrency) {
      const samples = results.filter((item) => item.workloadId === workload.id && item.concurrency === concurrency);
      groups[workload.id][String(concurrency)] = summarizeRuns(samples);
    }
  }
  const report = {
    schemaVersion: 1,
    startedAt,
    finishedAt: new Date().toISOString(),
    target: { pageOrigin: new URL(scenario.pageUrl).origin, apiOrigin: new URL(scenario.apiUrl).origin },
    runner: { node: process.version, platform: process.platform, arch: process.arch, chrome: browserDetails },
    cacheLabels: {
      cold: 'first preview in a new headless Chrome profile; backend cache state is unreported unless returned by server timing/header/metrics',
      warm: 'later preview in the same profile after a successful first preview; server cache state is unreported unless returned by server timing/header/metrics',
    },
    preflight: { status: preflight.status, apiVersion: preflight.apiVersion, kind: preflight.kind, lifecycleState: preflight.lifecycleState, draftVersion: preflight.draftVersion, catalogComplete: preflight.catalogComplete },
    metadata: scenario.metadata,
    workloadIdentity: Object.fromEntries(scenario.workloads.map((workload) => [workload.id, workload.identity])),
    summary: groups,
    supersessionProbes: probes,
    samples: results,
  };
  const output = path.join(artifactDir, 'report.json');
  await writeFile(output, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  console.log(JSON.stringify({ result: 'BENCHMARK_PASSED', report: output, summary: report.summary, supersessionProbes: probes }, null, 2));
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.command === 'doctor') {
    const authorization = await loadAuthorization(options, [options.pageUrl, options.apiUrl]);
    await doctor(options, authorization);
    return;
  }
  const scenario = await loadScenario(options);
  const authorization = await loadAuthorization(options, [scenario.pageUrl, scenario.apiUrl]);
  await run(options, authorization, scenario);
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main().catch((error) => {
    const prefix = error instanceof UsageError ? 'usage' : 'construction-preview-bench';
    console.error(`${prefix}: ${redactAuth(error.message, process.env.LOOM_E2E_TOKEN ?? '')}`);
    process.exitCode = error instanceof UsageError ? 2 : 1;
  });
}
