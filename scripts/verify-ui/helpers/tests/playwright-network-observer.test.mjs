import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { chromium } from '@playwright/test';
import {
  createPlaywrightNetworkObserver,
  isMutationRequest,
  READ_ONLY_POST_PATH_SUFFIXES,
} from '../playwright-network-observer.mjs';

let browser;
const origin = 'http://observer.test';

before(async () => {
  browser = await chromium.launch({ channel: 'chrome', headless: true });
});

after(async () => {
  await browser?.close();
});

async function installFixtureRoutes(page) {
  await page.route('**/*', async route => {
    const { origin: requestOrigin, pathname } = new URL(route.request().url());
    if (requestOrigin !== origin) {
      await route.abort();
      return;
    }
    if (pathname === '/') {
      await route.fulfill({ status: 200, contentType: 'text/html',
        body: '<!doctype html><html><body>observer test</body></html>' });
      return;
    }
    if (['/fault/slow-read', '/fault/slow-post', '/authoring/v2/schema-fields',
      '/authoring/v2/commands', '/authoring/v2/publish'].includes(pathname)) {
      await new Promise(resolve => setTimeout(resolve, 250));
      try {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '{"completed":true}' });
      } catch {
        // The browser canceled this controlled slow request before its response was ready.
      }
      return;
    }
    if (pathname === '/fault/over-private-limit') {
      await route.fulfill({ status: 200, contentType: 'application/json',
        body: JSON.stringify({ payload: 'z'.repeat(4 * 1024 * 1024 + 256) }) });
      return;
    }
    const bodies = {
      '/fault/server-error': [500, { code: 'SERVER_DOWN', message: 'service unavailable', error: 'server unavailable' }],
      '/fault/validation': [422, { error: 'invalid selection', reason: 'validation' }],
      '/fault/missing': [200, { other: 'present' }],
      '/fault/stale': [200, { revision: 'old', value: ['old-row'] }],
    };
    const [status, body] = bodies[pathname] ?? (pathname === '/fault/large-body'
      ? [200, { payload: 'x'.repeat(70 * 1024) }]
      : [404, { error: 'not found' }]);
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  });
}

async function withObserver(run, {
  includeReport = true,
  responsePaths = pathname => pathname.startsWith('/fault/'),
  scopeRequest,
  beforeObserver,
} = {}) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await installFixtureRoutes(page);
  await page.goto(`${origin}/`);
  beforeObserver?.(page);
  const report = includeReport ? { errors: [] } : undefined;
  const observer = createPlaywrightNetworkObserver(page, {
    scopeRequest: scopeRequest ?? (request => {
      const url = new URL(request.url());
      return url.origin === origin && (url.pathname.startsWith('/fault/') || [
        '/authoring/v2/schema-fields', '/authoring/v2/commands', '/authoring/v2/publish',
      ].includes(url.pathname));
    }),
    appOrigins: [origin],
    responsePaths,
    report,
  });
  try {
    await run({ context, page, observer, report });
  } finally {
    observer.stop();
    await context.close();
  }
}

test('uses an exact read-only POST suffix allowlist and treats unknown writes as mutations', () => {
  const expectedReadOnlyPaths = [
    '/authoring/v2/schema-fields',
    '/authoring/v2/suggestions',
    '/authoring/v2/semantic-inventory',
    '/authoring/v2/frame-source-options',
    '/authoring/v2/configured-column-context',
    '/authoring/v2/construction-choices',
    '/authoring/v2/related-expand-choices',
    '/authoring/v2/related-field-choices',
    '/authoring/v2/related-expand-contributors',
    '/authoring/v2/construction-inputs',
    '/authoring/v2/construction-category-discoveries',
    '/authoring/v2/table-shape-capabilities',
    '/authoring/v2/table-shape-category-discoveries',
    '/authoring/v2/table-shape-resolutions',
    '/authoring/v2/population-routes',
    '/authoring/v2/column-source',
    '/authoring/v2/row-change',
    '/authoring/v2/row-lineage',
    '/authoring/v2/interpretation-preview',
    '/authoring/v2/population-mapping',
    '/authoring/v2/preview',
    '/authoring/v2/reconcile',
    '/authoring/v2/construction-capabilities',
    '/authoring/v2/construction-proposals',
    '/authoring/v2/construction-choice-proposals',
    '/authoring/v2/row-definition-proposals',
    '/authoring/v2/row-definition-choices',
    '/authoring/v2/table-shape-proposals',
    '/authoring/v2/cell-trace',
  ];
  assert.deepEqual(READ_ONLY_POST_PATH_SUFFIXES, expectedReadOnlyPaths);
  for (const suffix of expectedReadOnlyPaths) {
    assert.equal(isMutationRequest('POST', `/api/v1/projects/p/explorers/e${suffix}`), false, suffix);
    assert.equal(isMutationRequest('POST', `/api/v1/projects/p/explorers/e${suffix}/child`), true,
      `${suffix} must match only its exact suffix`);
  }
  assert.equal(isMutationRequest('POST', '/api/v1/projects/p/explorers/e/authoring/v2/commands'), true);
  assert.equal(isMutationRequest('PATCH', '/api/v1/projects/p/explorers/e/authoring/v2/preview'), true);
  for (const method of ['GET', 'HEAD', 'OPTIONS']) {
    assert.equal(isMutationRequest(method, '/api/v1/projects/p/explorers/e/authoring/v2/commands'), false);
  }
});

test('retains the native response-arrival time separately from completed body capture', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const responseWait = observer.waitForResponse(entry => entry.path === '/fault/stale');
    await page.evaluate(() => fetch('/fault/stale').then(response => response.json()));
    const entry = await responseWait;

    assert.equal(entry.status, 200);
    assert.ok(Number.isFinite(entry.startedAt));
    assert.ok(Number.isFinite(entry.responseReceivedAt));
    assert.ok(Number.isFinite(entry.completedAt));
    assert.ok(entry.startedAt <= entry.responseReceivedAt);
    assert.ok(entry.responseReceivedAt <= entry.completedAt);
    assert.deepEqual(observer.responseBody(entry), { revision: 'old', value: ['old-row'] });

    const projected = report.nativeRequests.find(item => item.browserRequestId === entry.browserRequestId);
    assert.equal(projected.responseReceivedAt, entry.responseReceivedAt);
    assert.equal(projected.completedAt, entry.completedAt);
  });
});

test('a canceled GET is diagnostic while an aborted unknown POST is fatal and preserves earlier assertion errors', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const earlierAssertionFailure = { kind: 'assertion', message: 'product assertion failed first' };
    report.errors.push(earlierAssertionFailure);

    const getWait = observer.waitForResponse(entry => entry.path === '/fault/slow-read');
    await page.evaluate(async () => {
      const controller = new AbortController();
      const response = fetch('/fault/slow-read', { signal: controller.signal }).catch(() => undefined);
      setTimeout(() => controller.abort(), 25);
      await response;
    });
    const canceledGet = await getWait;
    assert.ok(canceledGet.failure);
    assert.equal(canceledGet.isMutation, false);
    assert.equal(canceledGet.readCancellationDiagnostic, true);
    assert.equal(observer.failures.some(failure => failure.entry === canceledGet), false);

    const postWait = observer.waitForResponse(entry => entry.path === '/fault/slow-post');
    await page.evaluate(async () => {
      const controller = new AbortController();
      const response = fetch('/fault/slow-post', {
        method: 'POST', body: JSON.stringify({ action: 'write', apiKey: 'secret-test' }), signal: controller.signal,
      }).catch(() => undefined);
      setTimeout(() => controller.abort(), 25);
      await response;
    });
    const canceledPost = await postWait;
    assert.ok(canceledPost.failure);
    assert.equal(canceledPost.isMutation, true);
    assert.deepEqual(observer.requestBody(canceledPost), { action: 'write', apiKey: '[REDACTED]' });
    assert.deepEqual(canceledPost.request, { action: 'write', apiKey: '[REDACTED]' });
    assert.equal(Object.hasOwn(canceledPost, 'body'), false);
    assert.equal(canceledPost.readCancellationDiagnostic, false);
    assert.ok(observer.failures.some(failure => failure.entry === canceledPost));
    assert.equal(report.errors[0], earlierAssertionFailure);
    assert.ok(report.errors.some(error => error.kind === 'network' && error.url === canceledPost.url));
    assert.equal(report.nativeRequests.length, 2);
    assert.ok(report.network.some(entry => entry.observerRequestIndex === canceledGet.index && entry.readCancellationDiagnostic));
    assert.doesNotThrow(() => JSON.stringify(report));
  });
});

test('schema-fields POST cancellation is diagnostic while commands and publish cancellations are fatal', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const cancelPost = async path => {
      const wait = observer.waitForResponse(entry => entry.path === path);
      await page.evaluate(pathname => {
        const controller = new AbortController();
        const response = fetch(pathname, { method: 'POST', body: '{}', signal: controller.signal }).catch(() => undefined);
        setTimeout(() => controller.abort(), 25);
        return response;
      }, path);
      return wait;
    };

    const read = await cancelPost('/authoring/v2/schema-fields');
    assert.equal(read.isMutation, false);
    assert.equal(read.readCancellationDiagnostic, true);
    assert.equal(observer.failures.some(failure => failure.entry === read), false);

    for (const path of ['/authoring/v2/commands', '/authoring/v2/publish']) {
      const entry = await cancelPost(path);
      assert.equal(entry.isMutation, true);
      assert.equal(entry.readCancellationDiagnostic, false);
      assert.ok(observer.failures.some(failure => failure.entry === entry));
    }
    assert.ok(report.errors.some(error => error.url.endsWith('/authoring/v2/commands')));
    assert.ok(report.errors.some(error => error.url.endsWith('/authoring/v2/publish')));
  });
});

test('a scoped HTTP 500 stays fatal and a captured 422 can be exempted with explicit proof', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const serverErrorWait = observer.waitForResponse(entry => entry.path === '/fault/server-error');
    await page.evaluate(() => fetch('/fault/server-error').then(response => response.json()));
    const serverError = await serverErrorWait;
    assert.equal(serverError.status, 500);
    assert.deepEqual(observer.responseBody(serverError), {
      code: 'SERVER_DOWN', message: 'service unavailable', error: 'server unavailable',
    });
    assert.deepEqual(serverError.response, {
      code: 'SERVER_DOWN', message: 'service unavailable', error: 'server unavailable',
    });
    assert.equal(serverError.response.error, 'server unavailable');
    assert.ok(report.network.some(entry => entry.observerRequestIndex === serverError.index && entry.status === 500));
    assert.ok(report.errors.some(error => error.kind === 'http' && error.status === 500));
    assert.deepEqual(report.errors.find(error => error.kind === 'http' && error.status === 500).body,
      {
        diagnosticBody: { code: 'SERVER_DOWN', message: 'service unavailable', error: 'server unavailable' },
        code: 'SERVER_DOWN', message: 'service unavailable',
      });
    assert.throws(() => observer.markExpectedValidation(serverError, {
      status: 500, reason: 'not allowed', proof: { assertion: 'no 500 exemptions' },
    }), RangeError);

    const validationWait = observer.waitForResponse(entry => entry.path === '/fault/validation');
    await page.evaluate(() => fetch('/fault/validation').then(response => response.json()));
    const validation = await validationWait;
    assert.equal(validation.status, 422);
    assert.deepEqual(observer.responseBody(validation), { error: 'invalid selection', reason: 'validation' });
    observer.markExpectedValidation(validation, {
      status: 422,
      reason: 'the fixture submitted an intentionally invalid selection',
      proof: { nativeAssertion: 'validation response is shown to the user' },
    });
    assert.equal(validation.httpFailureExempted, true);
    assert.equal(validation.failure, undefined);
    assert.equal(report.network.some(entry => entry.observerRequestIndex === validation.index), false);
    assert.equal(report.errors.some(error => error.kind === 'http' && error.status === 422), false);
    assert.ok(report.errors.some(error => error.kind === 'http' && error.status === 500));
    assert.ok(observer.failures.some(failure => failure.kind === 'http' && failure.entry === serverError));
    assert.equal(observer.failures.some(failure => failure.kind === 'http' && failure.entry === validation), false);
    assert.doesNotThrow(() => JSON.stringify(report));
  });
});

test('HTTP 500 remains fatal without a report object', async () => {
  await withObserver(async ({ page, observer }) => {
    const wait = observer.waitForResponse(entry => entry.path === '/fault/server-error');
    await page.evaluate(() => fetch('/fault/server-error').then(response => response.json()));
    const entry = await wait;
    assert.equal(entry.status, 500);
    assert.ok(observer.failures.some(failure => failure.kind === 'http' && failure.entry === entry));
  }, { includeReport: false });
});

test('scope predicate exceptions are fatal even without a report object', async () => {
  await withObserver(async ({ page, observer }) => {
    await page.evaluate(() => fetch('/fault/scope-throws').catch(() => undefined));
    await page.waitForTimeout(25);
    assert.ok(observer.failures.some(failure => failure.kind === 'observer-scope'));
  }, {
    includeReport: false,
    scopeRequest: request => {
      if (new URL(request.url()).pathname === '/fault/scope-throws') throw new Error('scope probe failed');
      return false;
    },
  });
});

test('a response body capture failure remains separate from its HTTP failure', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const wait = observer.waitForResponse(entry => entry.path === '/fault/server-error');
    await page.evaluate(() => fetch('/fault/server-error').then(response => response.json()));
    const entry = await wait;
    assert.equal(entry.responseCapture, 'failed');
    assert.match(entry.responseCaptureError, /controlled response body failure/);
    assert.equal(entry.failure.kind, 'http');
    assert.equal(observer.failures.filter(failure => failure.entry === entry).length, 1);
    assert.equal(report.errors.filter(error => error.kind === 'http').length, 1);
    assert.equal(report.errors.some(error => error.message?.includes('controlled response body failure')), false);
  }, {
    beforeObserver: page => {
      page.on('response', response => {
        if (new URL(response.url()).pathname !== '/fault/server-error') return;
        Object.defineProperty(response, 'body', {
          configurable: true,
          value: async () => { throw new Error('controlled response body failure'); },
        });
      });
    },
  });
});

test('unconfigured HTTP error bodies are captured diagnostically without changing the fatal HTTP result', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const wait = observer.waitForResponse(entry => entry.path === '/fault/server-error');
    await page.evaluate(() => fetch('/fault/server-error').then(response => response.json()));
    const entry = await wait;
    assert.equal(entry.responseCapture, 'not-configured');
    assert.equal(observer.responseBody(entry), undefined);
    await page.waitForTimeout(25);
    assert.equal(entry.responseBodyCaptured, undefined);
    assert.equal(entry.response, undefined);
    assert.deepEqual(entry.httpDiagnostic.body, {
      captureState: 'completed',
      body: { code: 'SERVER_DOWN', message: 'service unavailable', error: 'server unavailable' },
      code: 'SERVER_DOWN', message: 'service unavailable',
    });
    assert.equal(entry.failure.kind, 'http');
    assert.deepEqual(report.errors.find(error => error.kind === 'http').body, {
      diagnosticBody: { code: 'SERVER_DOWN', message: 'service unavailable', error: 'server unavailable' },
      code: 'SERVER_DOWN', message: 'service unavailable',
    });
  }, { responsePaths: [] });
});

test('large response access is complete while serializable report evidence remains capped', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const wait = observer.waitForResponse(entry => entry.path === '/fault/large-body');
    await page.evaluate(() => fetch('/fault/large-body', {
      method: 'POST', headers: { 'X-Request-ID': 'req-123' },
      body: JSON.stringify({ input: 'y'.repeat(70 * 1024) }),
    }).then(response => response.json()));
    const entry = await wait;
    assert.equal(entry.response.payload.length, 70 * 1024);
    assert.equal(entry.request.input.length, 70 * 1024);
    const body = observer.responseBody(entry);
    assert.equal(body.payload.length, 70 * 1024);
    assert.equal(observer.requestBody(entry).input.length, 70 * 1024);
    assert.equal(report.nativeRequests[0].response.truncated, true);
    assert.ok(report.nativeRequests[0].response.byteLength > 64 * 1024);
    assert.equal(report.nativeRequests[0].request.truncated, true);
    assert.ok(report.nativeRequests[0].request.byteLength > 64 * 1024);
    assert.ok(JSON.stringify(report).length < 8_000);
    assert.equal(entry.requestId, 'req-123');
    assert.equal(typeof entry.browserRequestId, 'string');
    assert.equal(Number.isInteger(entry.startedAt), true);
    assert.ok(entry.completedAt >= entry.startedAt);
  });
});

test('an awaited response larger than 4MiB stays complete for predicates', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const wait = observer.waitForResponse(entry => entry.path === '/fault/over-private-limit');
    await page.evaluate(() => fetch('/fault/over-private-limit').then(response => response.json()));
    const entry = await wait;
    assert.equal(entry.responseCapture, 'captured');
    assert.equal(entry.response.payload.length, 4 * 1024 * 1024 + 256);
    assert.equal(entry.requestId, undefined, 'requestId is reserved for the actual X-Request-ID header');
    assert.equal(typeof entry.browserRequestId, 'string');
    assert.equal(observer.responseBody(entry).payload.length, 4 * 1024 * 1024 + 256);
    assert.equal(report.nativeRequests[0].response.truncated, true);
    assert.ok(report.nativeRequests[0].response.byteLength > 4 * 1024 * 1024);
    assert.ok(JSON.stringify(report).length < 8_000);
  });
});

test('native assertions reject missing and stale captured operation results', async () => {
  await withObserver(async ({ page, observer }) => {
    const missingWait = observer.waitForResponse(entry => entry.path === '/fault/missing');
    await page.evaluate(() => fetch('/fault/missing').then(response => response.json()));
    const missing = await missingWait;
    const missingBody = observer.responseBody(missing);
    assert.throws(() => assert.equal(missingBody?.revision, 'current'), assert.AssertionError);

    const staleWait = observer.waitForResponse(entry => entry.path === '/fault/stale');
    await page.evaluate(() => fetch('/fault/stale').then(response => response.json()));
    const stale = await staleWait;
    const staleBody = observer.responseBody(stale);
    assert.throws(() => assert.deepEqual(staleBody?.value, ['current-row']), assert.AssertionError);
  });
});

test('the exact injected route fault is expected only for the marked Playwright Request', async () => {
  await withObserver(async ({ page, observer, report }) => {
    let injectedRequest;
    await page.route('**/fault/injected', async route => {
      injectedRequest = route.request();
      observer.markExpectedTransportFailure(injectedRequest, {
        reason: 'this test injects one controlled transport fault',
        action: { name: 'intercept exact injected endpoint' },
      });
      await route.abort('aborted');
    });
    await page.route('**/fault/unmarked', async route => {
      assert.throws(() => observer.markExpectedTransportFailure(route.request(), {
        reason: 'mutations cannot be exempted', action: 'controlled route fault',
      }), /cannot exempt a mutation request/);
      await route.abort('failed');
    });

    const wait = observer.waitForResponse(entry => entry.path === '/fault/injected');
    await page.evaluate(() => fetch('/fault/injected').catch(() => undefined));
    const entry = await wait;
    assert.equal(observer.byRequest.get(injectedRequest), entry);
    assert.equal(entry.isMutation, false);
    assert.equal(entry.expectedTransportFailure.reason, 'this test injects one controlled transport fault');
    assert.ok(entry.failure);
    assert.equal(entry.readCancellationDiagnostic, false);
    assert.equal(observer.failures.some(failure => failure.entry === entry), false);
    assert.ok(report.network.some(failure => failure.observerRequestIndex === entry.index && failure.expected === true));
    assert.equal(report.errors.some(error => error.url === entry.url), false);

    const unmarkedWait = observer.waitForResponse(candidate => candidate.path === '/fault/unmarked');
    await page.evaluate(() => fetch('/fault/unmarked', { method: 'POST', body: '{"write":true}' }).catch(() => undefined));
    const unmarked = await unmarkedWait;
    assert.equal(unmarked.expectedTransportFailure, undefined);
    assert.ok(observer.failures.some(failure => failure.entry === unmarked));
    assert.ok(report.errors.some(error => error.url === unmarked.url && error.kind === 'network'));
  });
});

test('page errors and app-origin console errors are fatal, while captured browser resource echoes are diagnostic', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const fetchWait = observer.waitForResponse(entry => entry.path === '/fault/server-error');
    await page.evaluate(() => fetch('/fault/server-error').then(response => response.json()));
    await fetchWait;

    const imageFromIndex = observer.requests.length;
    const imageWait = observer.waitForResponse(entry => entry.path === '/fault/server-error', { fromIndex: imageFromIndex });
    const resourceEcho = page.waitForEvent('console', {
      predicate: message => message.type() === 'error' && message.text().startsWith('Failed to load resource'),
      timeout: 2_000,
    });
    await page.evaluate(() => {
      const image = new Image();
      image.src = '/fault/server-error?image=1';
      document.body.append(image);
    });
    const [imageEntry, echo] = await Promise.all([imageWait, resourceEcho]);
    assert.equal(imageEntry.status, 500);
    assert.match(echo.text(), /^Failed to load resource: the server responded with a status of 500/);

    await page.evaluate(() => {
      console.error('app console failure');
      console.error('Failed to load resource: net::ERR_FAILED');
      setTimeout(() => { throw new Error('page runtime failure'); }, 0);
    });
    await page.waitForTimeout(25);
    observer.stop();
    assert.ok(observer.failures.some(failure => failure.kind === 'console' && failure.message === 'app console failure'));
    assert.ok(observer.failures.some(failure => failure.kind === 'console' && failure.message === 'Failed to load resource: net::ERR_FAILED'));
    assert.ok(observer.failures.some(failure => failure.kind === 'pageerror' && failure.message.includes('page runtime failure')));
    assert.equal(observer.failures.some(failure => failure.kind === 'console' &&
      failure.message.startsWith('Failed to load resource: the server responded with a status of 500')), false);
    assert.ok(observer.diagnostics.httpFailures.some(failure => failure.status === 500));
    assert.ok(observer.diagnostics.console.some(failure => failure.text === 'Failed to load resource: net::ERR_FAILED'));
    assert.deepEqual(Object.keys(observer.diagnostics).sort(),
      ['assetFailures', 'console', 'httpFailures', 'networkFailures', 'overflow', 'pageErrors'].sort());
    assert.ok(report.errors.some(error => error.kind === 'console' && error.message === 'app console failure'));
    assert.ok(report.errors.some(error => error.kind === 'console' && error.message === 'Failed to load resource: net::ERR_FAILED'));
    assert.ok(report.errors.some(error => error.kind === 'runtime' && error.message.includes('page runtime failure')));
  });
});

test('generic resource echoes wait for exact request failure and unmatched echoes become fatal at stop', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const target = `${origin}/fault/server-error?event-order=1`;
    page.emit('console', {
      type: () => 'error',
      text: () => 'Failed to load resource: net::ERR_FAILED',
      location: () => ({ url: target }),
    });
    assert.equal(observer.failures.some(failure => failure.kind === 'console'), false);
    const wait = observer.waitForResponse(entry => entry.path === '/fault/server-error');
    await page.evaluate(() => fetch('/fault/server-error?event-order=1').then(response => response.json()));
    const failed = await wait;
    assert.equal(failed.status, 500);
    assert.equal(observer.failures.some(failure => failure.kind === 'console'), false);

    page.emit('console', {
      type: () => 'error',
      text: () => 'Failed to load resource: net::ERR_FAILED',
      location: () => ({ url: `${origin}/fault/unobserved` }),
    });
    page.emit('console', {
      type: () => 'error',
      text: () => 'Failed to load resource: the server responded with a status of 404 (Not Found).',
      location: () => ({ url: `${origin}/favicon.ico` }),
    });
    assert.ok(observer.diagnostics.assetFailures.some(failure => failure.url === `${origin}/favicon.ico`));
    assert.deepEqual(report.assetFailures, [{ url: `${origin}/favicon.ico`, status: 404, kind: 'asset-failure' }]);
    observer.stop();
    assert.ok(observer.failures.some(failure => failure.kind === 'console' && failure.url.endsWith('/fault/unobserved')));
    assert.equal(observer.failures.some(failure => failure.url?.endsWith('/favicon.ico')), false);
  });
});

test('diagnostic channels cap evidence without dropping fatal failures or report errors', async () => {
  await withObserver(async ({ page, observer, report }) => {
    const finalConsole = page.waitForEvent('console', {
      predicate: message => message.text() === 'observer diagnostic 502', timeout: 5_000,
    });
    await page.evaluate(() => {
      for (let index = 0; index < 503; index += 1) console.error(`observer diagnostic ${index}`);
    });
    await finalConsole;
    assert.equal(observer.diagnostics.console.length, 500);
    assert.equal(observer.diagnostics.overflow.console, 3);
    assert.equal(observer.failures.filter(failure => failure.kind === 'console').length, 503);
    assert.equal(report.errors.filter(error => error.kind === 'console').length, 503);
  });
});
