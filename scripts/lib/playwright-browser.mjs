import { existsSync } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { chromium } from 'playwright';

const maxEntries = 100;
const maxBodyLength = 12000;
const sensitiveName = /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i;

export function sanitizeText(value) {
  return String(value ?? '')
    .replaceAll(process.cwd(), '$CHECKOUT')
    .replace(/(?:file:\/\/)?\/(?:private\/)?tmp\/[^\s)]+/g, '$TMP/<path>')
    .replace(/\/Users\/[^/\s]+/g, '$HOME')
    .replace(/\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi, 'Bearer [REDACTED]')
    .replace(/\b(access[_-]?token|refresh[_-]?token|id[_-]?token|authorization|set-cookie|cookie|password|passwd|secret|credential|session(?:[_-]?id)?|api[_-]?key)(\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^,;\s}\]]+)/gi, '$1$2[REDACTED]')
    .replace(/\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\b/g, '[REDACTED_TOKEN]')
    .replace(/\bsk-[A-Za-z0-9]{16,}\b/g, '[REDACTED_TOKEN]');
}

export function sanitizePayload(value, key = '') {
  if (sensitiveName.test(key)) return '[REDACTED]';
  if (typeof value === 'string') return sanitizeText(value);
  if (Array.isArray(value)) return value.map(item => sanitizePayload(item));
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, sanitizePayload(childValue, childKey)]));
  }
  return value;
}

export function sanitizeBody(body) {
  const text = String(body ?? '').slice(0, maxBodyLength);
  try {
    return JSON.stringify(sanitizePayload(JSON.parse(text)));
  } catch {
    return sanitizeText(text);
  }
}

function safeURL(rawURL) {
  try {
    const url = new URL(rawURL);
    return sanitizeText(`${url.origin}${url.pathname}`);
  } catch {
    return sanitizeText(rawURL);
  }
}

function isLocalAppURL(rawURL, origins) {
  try {
    return origins.has(new URL(rawURL).origin);
  } catch {
    return false;
  }
}

function isLoopback(origin) {
  try {
    return ['127.0.0.1', 'localhost', '::1'].includes(new URL(origin).hostname);
  } catch {
    return false;
  }
}

export function matchesPendingCancellation(request, { origin, method, paths, requestIdPrefixes }) {
  const url = new URL(request.url());
  const requestId = request.headers()['x-request-id'] ?? '';
  return url.origin === origin && request.method() === method && paths.includes(url.pathname) &&
    requestIdPrefixes.some(prefix => requestId.startsWith(prefix));
}

async function inspectLocator(locator) {
  try {
    const count = await locator.count();
    const result = { count };
    if (count === 1) {
      const [visible, enabled, editable] = await Promise.all([
        locator.isVisible().catch(() => undefined),
        locator.isEnabled().catch(() => undefined),
        locator.isEditable().catch(() => undefined),
      ]);
      Object.assign(result, { visible, enabled, editable });
    }
    return result;
  } catch (error) {
    return { inspectionError: sanitizeText(error.message) };
  }
}

export async function launchBrowser({ evidence, appOrigins = [], noAuth = false, classifyExpectedRequestFailure }) {
  await mkdir(evidence, { recursive: true });
  const systemChrome = [
    process.env.CHROME_BIN,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  ].find(candidate => candidate && existsSync(candidate));
  const browser = await chromium.launch({ headless: true, ...(systemChrome ? { executablePath: systemChrome } : {}) });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const origins = new Set(appOrigins.map(value => new URL(value).origin));
  const traceAllowed = noAuth && appOrigins.length > 0 && appOrigins.every(isLoopback);
  let traceSafe = traceAllowed;
  page.on('request', request => {
    if (!isLocalAppURL(request.url(), origins)) traceSafe = false;
    const headers = request.headers();
    if (Object.keys(headers).some(name => sensitiveName.test(name))) traceSafe = false;
  });
  if (traceAllowed) {
    await context.tracing.start({
      title: 'Loom browser verification; loopback origins and fresh unauthenticated context only',
      screenshots: true,
      snapshots: true,
      sources: false,
    });
  }
  const diagnostics = { console: [], pageErrors: [], networkFailures: [], expectedCancellations: [], httpFailures: [], assetFailures: [], apiResponses: [] };
  const pendingRequests = new Set();
  const expectedCancellations = new WeakMap();
  let activeCancellation;
  page.on('request', request => {
    if (!isLocalAppURL(request.url(), origins)) return;
    pendingRequests.add(request);
    if (activeCancellation && matchesPendingCancellation(request, activeCancellation)) {
      expectedCancellations.set(request, activeCancellation);
    }
  });
  page.on('requestfinished', request => pendingRequests.delete(request));
  const boundedPush = (items, item) => {
    if (items.length < maxEntries) items.push(item);
  };

  page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location && !isLocalAppURL(location, origins)) return;
    if (location && new URL(location).pathname === '/favicon.ico' && message.text().includes('404')) {
      boundedPush(diagnostics.assetFailures, { url: safeURL(location), status: 404, kind: 'console' });
      return;
    }
    boundedPush(diagnostics.console, {
      type: message.type(),
      text: sanitizeText(message.text()),
      ...(location ? { location: safeURL(location) } : {}),
    });
  });
  page.on('pageerror', error => boundedPush(diagnostics.pageErrors, {
    name: error.name,
    message: sanitizeText(error.message),
    stack: sanitizeText(error.stack),
  }));
  page.on('requestfailed', request => {
    pendingRequests.delete(request);
    if (!isLocalAppURL(request.url(), origins)) return;
    const classification = classifyExpectedRequestFailure?.(request);
    if (classification) {
      boundedPush(diagnostics.expectedCancellations, {
        url: safeURL(request.url()),
        method: request.method(),
        failure: sanitizeText(request.failure()?.errorText),
        classification: sanitizeText(classification),
      });
      return;
    }
    boundedPush(diagnostics.networkFailures, {
      url: safeURL(request.url()),
      method: request.method(),
      failure: sanitizeText(request.failure()?.errorText),
      observedAt: Date.now(),
      ...(request.headers()['x-request-id'] ? { requestId: sanitizeText(request.headers()['x-request-id']) } : {}),
      ...(currentAction ? { triggerAction: currentAction } : {}),
      ...(expectedCancellations.has(request) && expectedCancellations.get(request).actionLabel === currentAction &&
          request.failure()?.errorText === 'net::ERR_ABORTED'
        ? { canceled: true, cancellationReason: expectedCancellations.get(request).reason } : {}),
    });
  });
  page.on('response', async response => {
    if (!isLocalAppURL(response.url(), origins)) return;
    const entry = { url: safeURL(response.url()), status: response.status(), method: response.request().method(), observedAt: Date.now() };
    if (response.url().includes('/api/')) boundedPush(diagnostics.apiResponses, entry);
    const pathname = new URL(response.url()).pathname;
    if (pathname.endsWith('/favicon.ico') && response.status() === 404) {
      boundedPush(diagnostics.assetFailures, entry);
      return;
    }
    if (response.status() < 400) return;
    const failure = { ...entry };
    try {
      failure.body = sanitizeBody(await response.text());
    } catch (error) {
      failure.bodyError = sanitizeText(error.message);
    }
    boundedPush(diagnostics.httpFailures, failure);
  });

  let tracingStopped = false;
  let failureCaptured = false;
  let currentAction;
  const tracePath = join(evidence, 'failure-trace.zip');
  return {
    browser,
    context,
    page,
    diagnostics,
    setCurrentAction(label) { currentAction = sanitizeText(label); },
    expectPendingCancellations({ origin, method, paths, requestIdPrefixes, reason, actionLabel }) {
      let marked = 0;
      for (const request of pendingRequests) {
        if (matchesPendingCancellation(request, { origin, method, paths, requestIdPrefixes })) {
          expectedCancellations.set(request, { origin, method, paths, requestIdPrefixes, reason, actionLabel });
          marked += 1;
        }
      }
      return marked;
    },
    async withExpectedCancellations(specification, work) {
      if (activeCancellation) throw new Error('expected cancellation scope already active');
      activeCancellation = specification;
      this.expectPendingCancellations(specification);
      try { return await work(); }
      finally { activeCancellation = undefined; }
    },
    async captureFailure(error, details = {}) {
      if (failureCaptured) return;
      failureCaptured = true;
      const { action, ...safeDetails } = details;
      const contextualEvidence = typeof this.failureContext === 'function'
        ? sanitizePayload(await this.failureContext())
        : {};
      const failure = {
        message: sanitizeText(error?.message ?? error),
        stack: sanitizeText(error?.stack),
        ...contextualEvidence,
        ...sanitizePayload(safeDetails),
        tracePolicy: 'A Playwright zip trace is retained only when the caller confirms no authentication, all origins are loopback, and the context is fresh; other targets receive a sanitized JSON failure trace.',
        diagnostics,
      };
      try {
        const masks = [
          page.locator('input'),
          page.locator('textarea'),
          page.locator('[contenteditable="true"]'),
        ];
        await page.screenshot({ path: join(evidence, 'first-failure.png'), fullPage: true, mask: masks, maskColor: '#000000' });
        failure.screenshot = 'first-failure.png';
      } catch (captureError) {
        failure.screenshotError = sanitizeText(captureError.message);
      }
      try {
        const domText = await page.locator('body').innerText().catch(() => '');
        await writeFile(join(evidence, 'first-failure.dom.txt'), sanitizeText(domText));
        failure.dom = 'first-failure.dom.txt';
      } catch (captureError) {
        failure.domError = sanitizeText(captureError.message);
      }
      try {
        failure.controls = await page.locator('button, input, select, textarea, [role="button"], [role="checkbox"], [role="switch"]')
          .evaluateAll(controls => controls.slice(0, 500).map(control => ({
            tag: control.tagName.toLowerCase(),
            type: control.getAttribute('type'),
            label: control.getAttribute('aria-label') || control.innerText?.trim() || control.textContent?.trim() || '',
            name: control.getAttribute('name'),
            id: control.id || null,
            placeholder: control.getAttribute('placeholder'),
            autocomplete: control.getAttribute('autocomplete'),
            value: 'value' in control ? control.value : undefined,
            checked: 'checked' in control ? control.checked : undefined,
            disabled: 'disabled' in control ? control.disabled : control.getAttribute('aria-disabled') === 'true',
            testId: control.getAttribute('data-testid'),
          }))).then(controls => controls.map(control => {
            const identifyingText = [control.type, control.label, control.name, control.id, control.placeholder, control.autocomplete, control.testId].join(' ');
            if (sensitiveName.test(identifyingText) || control.type === 'password') control.value = '[REDACTED]';
            return sanitizePayload(control);
          }));
      } catch (captureError) {
        failure.controlsError = sanitizeText(captureError.message);
      }
      if (action) {
        failure.action = {
          label: sanitizeText(action.label),
          locator: sanitizeText(action.locator),
          elapsedMs: Number.isFinite(action.startedAt) ? Math.max(0, Date.now() - action.startedAt) : undefined,
          target: await inspectLocator(action.targetLocator),
        };
      }
      try {
        await writeFile(join(evidence, 'first-failure.json'), JSON.stringify(failure, null, 2));
      } catch {
        // Preserve the originating browser assertion if the evidence volume is unavailable.
      }
      if (traceAllowed && traceSafe) {
        try {
          await context.tracing.stop({ path: tracePath });
          tracingStopped = true;
          return 'failure-trace.zip';
        } catch (captureError) {
          diagnostics.traceError = sanitizeText(captureError.message);
        }
      }
      await writeFile(join(evidence, 'failure-trace.json'), JSON.stringify(sanitizePayload({
        message: failure.message,
        phase: failure.phase,
        cycle: failure.cycle,
        action: failure.action,
        ...contextualEvidence,
        diagnostics,
      }), null, 2)).catch(() => undefined);
      if (traceAllowed) await context.tracing.stop().catch(() => undefined);
      tracingStopped = true;
      return 'failure-trace.json';
    },
    async close() {
      if (!tracingStopped) {
        if (traceAllowed) await context.tracing.stop().catch(() => undefined);
        tracingStopped = true;
      }
      await browser.close();
    },
  };
}
