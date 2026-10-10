import assert from 'node:assert/strict';
import test from 'node:test';
import { scenarioCaseFor } from '../../registry.mjs';
import { sanitizeText } from '../playwright-browser.mjs';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';
import {
  assertScope001SavedPatientRows,
  createScope001SchemaFieldsResponseRecorder,
  ensureScope001DisclosureOpen,
  scope001DisclosureSummary,
  isScope001AuthorizedApiProxyRequest,
  isScope001SchemaFieldsRequest,
  hasScope001AuthorizationHeader,
  readScope001ProposalRows,
  readScope001RenderedRows,
  assertScope001ProposalRows,
  summarizeScope001SchemaFieldsResponse,
  scope001RestrictedAuthWorkflow,
  SCOPE001_PROXY_AUTH_REQUIRED_CHECK,
  scope001RestrictedAuthBuilderURL,
  waitForVisibleRestrictedRows,
} from '../../workflows/scope-001-restricted-auth.mjs';

test('registered restricted-auth proxy check label survives report sanitization', () => {
  const label = SCOPE001_PROXY_AUTH_REQUIRED_CHECK;
  assert.equal(label, 'restricted authorization was injected only into owned API proxy requests');
  assert.equal(sanitizeText(label), label);
  const registeredCase = scenarioCaseFor('scope-001-restricted-auth', 'restricted-related-records');
  assert.ok(registeredCase.requiredChecks.includes(label));
});

test('serialized restricted-row waiter receives both allowed and blocked IDs', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <div data-testid="construction-preview" data-preview-status="ready">
        <div data-testid="preview-table-scroll">
          <div role="table">
            <div role="row">
              <div role="columnheader">Patient ID</div>
              <div role="columnheader">Observation IDs</div>
              <div role="columnheader">Observation valueQuantity.value</div>
            </div>
            <div role="row">
              <div role="cell">scope-001-auth-patient-allowed</div>
              <div role="cell">scope-001-auth-observation-allowed</div>
              <div role="cell">111</div>
            </div>
          </div>
        </div>
      </div>
    `);

    await waitForVisibleRestrictedRows(page, [{
      patientId: 'scope-001-auth-patient-allowed',
      observationId: 'scope-001-auth-observation-allowed',
      values: [111],
    }], 'scope-001-auth-observation-blocked', 999, 'applied restricted-auth table');
  } finally {
    await browser.close();
  }
});

test('direct disclosure summary remains unique beside nested details', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <details id="raw-fields">
        <summary>Raw FHIR fields</summary>
        <div>
          <input id="observation-id" aria-label="Observation.id">
          <details id="nested"><summary>Nested group</summary><span>Nested field</span></details>
        </div>
      </details>
    `);
    const details = page.locator('#raw-fields');
    const summary = scope001DisclosureSummary(details);
    const nested = page.locator('#nested');

    assert.equal(await details.locator('summary').count(), 2, 'the disclosure contains a nested summary');
    assert.equal(await summary.count(), 1, 'only the direct summary controls the raw-fields disclosure');

    await ensureScope001DisclosureOpen({
      details,
      summary,
      action: async (_label, locator, perform, options) => {
        assert.equal(await locator.count(), 1, 'one native disclosure control is actionable');
        await perform(locator);
        await options.after();
      },
      label: 'open raw Observation fields',
      waitForContent: () => page.locator('#observation-id').waitFor({ state: 'visible', timeout: 5_000 }),
    });

    assert.equal(await details.evaluate(element => element.open), true, 'the outer disclosure opens');
    assert.equal(await nested.evaluate(element => element.open), false, 'nested disclosure remains closed');
  } finally {
    await browser.close();
  }
});

test('reuses an already-open disclosure between canceled and applied proposals', async () => {
  let open = true;
  let summaryClicks = 0;
  let actionCalls = 0;
  let contentWaits = 0;
  const details = { evaluate: async readOpen => readOpen({ open }) };
  const summary = { click: async () => { summaryClicks += 1; open = !open; } };
  const content = {
    waitFor: async ({ state, timeout }) => {
      contentWaits += 1;
      assert.equal(state, 'visible');
      assert.equal(timeout, 5_000);
      assert.equal(open, true, 'the chooser content must remain visible');
    },
  };

  await ensureScope001DisclosureOpen({
    details,
    summary,
    action: async () => { actionCalls += 1; },
    label: 'apply checkpoint: open related resource choices',
    waitForContent: () => content.waitFor({ state: 'visible', timeout: 5_000 }),
  });

  assert.equal(summaryClicks, 0, 'an already-open menu must not be toggled closed');
  assert.equal(actionCalls, 0, 'no redundant disclosure action is needed');
  assert.equal(contentWaits, 1, 'the exact visible chooser content remains required');
});

test('opens a disclosure only when closed and keeps the native action budget', async () => {
  let open = false;
  let summaryClicks = 0;
  const details = { evaluate: async readOpen => readOpen({ open }) };
  const summary = { click: async () => { summaryClicks += 1; open = true; } };
  const content = {
    waitFor: async ({ state, timeout }) => {
      assert.equal(state, 'visible');
      assert.equal(timeout, 5_000);
      assert.equal(open, true, 'content is required after expanding the disclosure');
    },
  };
  let actionOptions;

  await ensureScope001DisclosureOpen({
    details,
    summary,
    action: async (label, locator, perform, options) => {
      assert.equal(label, 'cancel checkpoint: open related resource choices');
      assert.equal(locator, summary);
      actionOptions = options;
      await perform(locator);
      await options.after();
    },
    label: 'cancel checkpoint: open related resource choices',
    waitForContent: () => content.waitFor({ state: 'visible', timeout: 5_000 }),
  });

  assert.equal(summaryClicks, 1);
  assert.equal(actionOptions.timeout, 5_000);
  assert.equal(actionOptions.budget, 5_000);
});

test('rendered Patient header keeps its semantic label when CSS uppercases display text', () => {
  const header = { innerText: 'PATIENT ID', textContent: 'Patient ID' };
  const value = { innerText: 'scope-001-auth-patient-allowed', textContent: 'scope-001-auth-patient-allowed' };
  const headerRow = { querySelector: () => null, querySelectorAll: () => [] };
  const valueRow = {
    querySelector: selector => selector === '[role="cell"]' ? {} : null,
    querySelectorAll: selector => selector === '[role="cell"]' ? [value] : [],
  };
  const table = {
    querySelectorAll: selector => selector === '[role="columnheader"]' ? [header]
      : selector === '[role="row"]' ? [headerRow, valueRow] : [],
  };

  const visible = readScope001RenderedRows(table);
  assert.deepEqual(visible, {
    headers: ['Patient ID'],
    rows: [['scope-001-auth-patient-allowed']],
  });
  assertScope001SavedPatientRows(visible, 'scope-001-auth-patient-allowed');
});

test('proposal reader uses semantic labels instead of concatenating logical-type badges', () => {
  const labels = ['Patient ID', 'Observation IDs', 'Observation valueQuantity.value'];
  const types = ['string', 'string', 'decimal'];
  const headers = labels.map((label, index) => ({
    textContent: `${label}${types[index]}`,
    firstElementChild: { textContent: label },
  }));
  const values = ['scope-001-auth-patient-allowed', 'scope-001-auth-observation-allowed', '111'];
  const row = {
    querySelectorAll: selector => selector === 'td'
      ? values.map(innerText => ({ innerText }))
      : [],
  };
  const table = {
    querySelectorAll: selector => selector === 'thead th' ? headers
      : selector === 'tbody tr' ? [row] : [],
  };

  const visible = readScope001ProposalRows(table);
  assert.deepEqual(visible, { headers: labels, rows: [values] });
  assertScope001ProposalRows(visible, [{
    patientId: 'scope-001-auth-patient-allowed',
    observationId: 'scope-001-auth-observation-allowed',
    values: [111],
  }], 'scope-001-auth-observation-blocked', 'retained restricted-auth preview');
});

test('cancel checkpoint accepts only the unchanged saved Patient-root row and column', () => {
  const expected = {
    headers: ['Patient ID'],
    rows: [['scope-001-auth-patient-allowed']],
  };
  assert.deepEqual(
    assertScope001SavedPatientRows(expected, 'scope-001-auth-patient-allowed'),
    expected,
  );
  assert.throws(
    () => assertScope001SavedPatientRows({
      headers: ['Patient ID', 'Observation ID'],
      rows: [['scope-001-auth-patient-allowed', 'scope-001-auth-observation-allowed']],
    }, 'scope-001-auth-patient-allowed'),
    /exactly its Patient ID column/,
  );
  assert.throws(
    () => assertScope001SavedPatientRows({
      headers: ['Patient ID'],
      rows: [['scope-001-auth-patient-allowed'], ['scope-001-auth-patient-extra']],
    }, 'scope-001-auth-patient-allowed'),
    /exactly its literal Patient row/,
  );
});

test('restricted-auth Builder URL selects only the exact organization/project in Builder mode', () => {
  const result = new URL(scope001RestrictedAuthBuilderURL({
    uiUrl: 'http://127.0.0.1:30008/?stale=1&project=wrong&project=duplicate',
  }, 'fresh-explorer'));

  assert.equal(result.origin, 'http://127.0.0.1:30008');
  assert.equal(result.pathname, '/');
  assert.deepEqual([...result.searchParams.entries()], [
    ['organization', 'loom_dev_scope001'],
    ['project', 'auth'],
    ['explorer', 'fresh-explorer'],
    ['mode', 'builder'],
  ]);
});

test('restricted-auth bearer routing is limited to the owned UI origin API proxy paths', () => {
  const uiOrigin = 'http://127.0.0.1:30008';
  assert.equal(isScope001AuthorizedApiProxyRequest(`${uiOrigin}/api/v1/projects/auth/explorers/default`, uiOrigin), true);
  assert.equal(isScope001AuthorizedApiProxyRequest(`${uiOrigin}/graphql`, uiOrigin), true);
  assert.equal(isScope001AuthorizedApiProxyRequest(`${uiOrigin}/graphql/graph`, uiOrigin), true);
  assert.equal(isScope001AuthorizedApiProxyRequest(`${uiOrigin}/`, uiOrigin), false);
  assert.equal(isScope001AuthorizedApiProxyRequest(`${uiOrigin}/assets/index.js`, uiOrigin), false);
  assert.equal(isScope001AuthorizedApiProxyRequest(`${uiOrigin}/api`, uiOrigin), false);
  assert.equal(isScope001AuthorizedApiProxyRequest('http://127.0.0.1:8188/api/v1/projects/auth', uiOrigin), false);
  assert.equal(isScope001AuthorizedApiProxyRequest('https://example.test/api/v1/projects/auth', uiOrigin), false);
  assert.equal(isScope001AuthorizedApiProxyRequest('not a URL', uiOrigin), false);
});

test('schema-fields diagnostics match only the exact owned project, Explorer, origin, and endpoint', () => {
  const uiOrigin = 'http://127.0.0.1:30008';
  const path = '/api/v1/projects/loom_dev_scope001%252Fauth/explorers/fresh-explorer/authoring/v2/schema-fields';
  assert.equal(isScope001SchemaFieldsRequest(`${uiOrigin}${path}`, uiOrigin, 'fresh-explorer'), true);
  assert.equal(isScope001SchemaFieldsRequest(`http://127.0.0.1:8188${path}`, uiOrigin, 'fresh-explorer'), false);
  assert.equal(isScope001SchemaFieldsRequest(`${uiOrigin}${path}`, uiOrigin, 'another-explorer'), false);
  assert.equal(isScope001SchemaFieldsRequest(`${uiOrigin}${path}?cursor=secret`, uiOrigin, 'fresh-explorer'), true);
  assert.equal(isScope001SchemaFieldsRequest(`${uiOrigin}${path}/extra`, uiOrigin, 'fresh-explorer'), false);
  assert.equal(isScope001SchemaFieldsRequest(`${uiOrigin}/api/v1/projects/foreign/explorers/fresh-explorer/authoring/v2/schema-fields`, uiOrigin, 'fresh-explorer'), false);
  assert.equal(hasScope001AuthorizationHeader({ Authorization: 'Bearer private-value' }), true);
  assert.equal(hasScope001AuthorizationHeader({ authorization: '  ' }), false);
  assert.equal(hasScope001AuthorizationHeader({ accept: 'application/json' }), false);
});

test('schema-fields diagnostics retain safe auth and response facts without body or cursor values', () => {
  const secret = 'private-bearer-token';
  const summary = summarizeScope001SchemaFieldsResponse({
    status: 401,
    authorizationHeaderPresent: true,
    bodyReadable: true,
    body: {
      snapshotToken: 'private-snapshot-token',
      nodeId: 'private-node-id',
      fields: [],
      complete: false,
      truncated: true,
      nextCursor: 'private-cursor-value',
      error: { code: 'UNAUTHENTICATED', message: 'authentication is required', details: { token: secret } },
    },
  });
  assert.deepEqual(summary, {
    status: 401,
    requestId: null,
    authorizationHeaderPresent: true,
    bodyReadable: true,
    hasErrorEnvelope: true,
    errorCode: 'UNAUTHENTICATED',
    authenticationRequiredMessage: true,
    hasFieldsArray: true,
    fieldCount: 0,
    complete: false,
    truncated: true,
    nextCursorPresent: true,
  });
  const serialized = JSON.stringify(summary);
  for (const forbidden of [secret, 'private-snapshot-token', 'private-node-id', 'private-cursor-value']) {
    assert.equal(serialized.includes(forbidden), false, `diagnostics must not retain ${forbidden}`);
  }
});

test('schema-fields diagnostics expose safe request ID, status, and code provenance', () => {
  const summary = summarizeScope001SchemaFieldsResponse({
    status: 403,
    authorizationHeaderPresent: true,
    bodyReadable: true,
    responseRequestId: 'server-request-403',
    callerRequestId: 'schema-fields-caller-request',
    body: {
      requestId: 'outer-payload-request',
      error: { code: 'SCOPE_DENIED', requestId: 'nested-payload-request', details: { token: 'private-bearer' } },
    },
  });
  assert.deepEqual({ requestId: summary.requestId, status: summary.status, code: summary.errorCode }, {
    requestId: 'server-request-403', status: 403, code: 'SCOPE_DENIED',
  });
  assert.equal(JSON.stringify(summary).includes('private-bearer'), false);
  assert.equal(JSON.stringify(summary).includes('outer-payload-request'), false);

  const payloadFallback = summarizeScope001SchemaFieldsResponse({
    status: 401, authorizationHeaderPresent: true, bodyReadable: true,
    callerRequestId: 'schema-fields-caller-request',
    body: { error: { code: 'UNAUTHENTICATED', requestId: 'payload-request-401' } },
  });
  assert.deepEqual({ requestId: payloadFallback.requestId, status: payloadFallback.status, code: payloadFallback.errorCode }, {
    requestId: 'payload-request-401', status: 401, code: 'UNAUTHENTICATED',
  });

  const callerFallback = summarizeScope001SchemaFieldsResponse({
    status: 401, authorizationHeaderPresent: true, bodyReadable: true,
    callerRequestId: 'schema-fields-caller-request',
    body: { error: { code: 'UNAUTHENTICATED' } },
  });
  assert.equal(callerFallback.requestId, 'schema-fields-caller-request');
  const rejectedId = summarizeScope001SchemaFieldsResponse({
    status: 401, authorizationHeaderPresent: true, bodyReadable: true,
    responseRequestId: 'Bearer private-bearer', callerRequestId: 'schema-fields-caller-request',
    body: { error: { code: 'UNAUTHENTICATED', requestId: 'Bearer private-bearer' } },
  });
  assert.equal(rejectedId.requestId, 'schema-fields-caller-request');
  assert.equal(JSON.stringify(rejectedId).includes('private-bearer'), false);
});

test('schema-fields response capture is bounded and records optional incompleteness without waiting', async () => {
  const uiUrl = 'http://127.0.0.1:30008';
  const diagnostics = { entries: [], dropped: 0, incomplete: 0 };
  const recorder = createScope001SchemaFieldsResponseRecorder({
    uiUrl,
    explorerId: 'fresh-explorer',
    diagnostics,
    captureTimeoutMs: 50,
  });
  const never = new Promise(() => {});
  const response = explorer => ({
    request: () => ({
      url: () => `${uiUrl}/api/v1/projects/loom_dev_scope001%252Fauth/explorers/${explorer}/authoring/v2/schema-fields`,
      headers: () => ({ authorization: 'Bearer private-value' }),
      allHeaders: () => never,
    }),
    status: () => 200,
    json: () => never,
  });

  recorder.onResponse(response('default'));
  assert.equal(diagnostics.entries.length, 0, 'the stale default Explorer must not be captured');
  recorder.onResponse(response('fresh-explorer'));
  assert.equal(diagnostics.entries.length, 1, 'the recorder must bind to the freshly created Explorer');
  assert.equal(diagnostics.entries[0].captureState, 'pending');
  assert.equal(recorder.close(), undefined, 'closing diagnostics must be synchronous and must not drain pending response reads');
  await Promise.resolve();
  assert.equal(diagnostics.entries[0].captureState, 'incomplete');
  assert.equal(diagnostics.incomplete, 1);
  assert.equal(JSON.stringify(diagnostics).includes('private-value'), false);
});

test('schema-fields response recorder stores only bounded safe facts for the fresh Explorer', async () => {
  const uiUrl = 'http://127.0.0.1:30008';
  const diagnostics = { entries: [], dropped: 0, incomplete: 0 };
  const recorder = createScope001SchemaFieldsResponseRecorder({
    uiUrl,
    explorerId: 'fresh-explorer',
    diagnostics,
  });
  recorder.onResponse({
    request: () => ({
      url: () => `${uiUrl}/api/v1/projects/loom_dev_scope001%252Fauth/explorers/fresh-explorer/authoring/v2/schema-fields`,
      headers: () => ({ Authorization: 'Bearer private-value', 'x-request-id': 'caller-request-403' }),
      allHeaders: async () => ({ Authorization: 'Bearer private-value', 'x-request-id': 'caller-request-403' }),
    }),
    headers: () => ({ 'x-request-id': 'server-request-403' }),
    status: () => 403,
    json: async () => ({ error: { code: 'SCOPE_DENIED', message: 'private body text', requestId: 'payload-request-403' } }),
  });
  await new Promise(resolve => setImmediate(resolve));
  recorder.close();
  assert.deepEqual(diagnostics, {
    entries: [{
      status: 403,
      requestId: 'server-request-403',
      authorizationHeaderPresent: true,
      bodyReadable: true,
      hasErrorEnvelope: true,
      errorCode: 'SCOPE_DENIED',
      authenticationRequiredMessage: false,
      hasFieldsArray: false,
      fieldCount: null,
      complete: null,
      truncated: null,
      nextCursorPresent: false,
      captureState: 'complete',
    }],
    dropped: 0,
    incomplete: 0,
  });
  const serialized = JSON.stringify(diagnostics);
  assert.equal(serialized.includes('private-value'), false);
  assert.equal(serialized.includes('private body text'), false);
});

test('workflow starts from the native New explorer control and scopes proxy bearer auth', async () => {
  const token = 'private-test-bearer-0123456789';
  const uiUrl = 'http://127.0.0.1:30008';
  const apiUrl = 'http://127.0.0.1:8188';
  const target = {
    fixtureProject: 'loom_dev_scope001-auth',
    fixtureGeneration: 'scope001-auth-v1',
    fixtureDir: fileURLToPath(new URL('../../../../testdata/scope-001-restricted-auth/', import.meta.url)),
    explorer: 'default',
    uiUrl,
    apiUrl,
  };
  const report = { authorization: { browserNoAuth: true }, target: {} };
  const navigationStop = new Error('stop after the controlled proxy requests');
  const observed = {
    installed: false,
    removed: false,
    proxyHeaders: null,
    externalHeaders: null,
    legacyWaitCalled: false,
    emptyStateHeadingWaited: false,
    newExplorerWaited: false,
    newExplorerActionReached: false,
  };
  const entryUrl = scope001RestrictedAuthBuilderURL(target, 'default');
  const emptyStateHeading = {
    waitFor: async options => {
      assert.deepEqual(options, { state: 'visible', timeout: 5_000 });
      observed.emptyStateHeadingWaited = true;
    },
  };
  const newExplorer = {
    waitFor: async options => {
      assert.deepEqual(options, { state: 'visible', timeout: 5_000 });
      observed.newExplorerWaited = true;
    },
    click: async () => {},
  };
  let authRoute;
  const route = async (url, headers) => {
    let continuedHeaders;
    await authRoute({
      request: () => ({ method: () => 'GET', url: () => url, headers: () => headers }),
      continue: async options => { continuedHeaders = options?.headers ?? null; },
    });
    return continuedHeaders;
  };
  const page = {
    setDefaultTimeout() {},
    setDefaultNavigationTimeout() {},
    on() {},
    off() {},
    route: async (pattern, handler) => {
      assert.equal(pattern, '**/*');
      authRoute = handler;
      observed.installed = true;
    },
    goto: async () => {
      observed.proxyHeaders = await route(`${uiUrl}/api/v1/projects/auth/explorers/default`, {
        accept: 'application/json',
      });
      observed.externalHeaders = await route('https://example.test/api/v1/projects/foreign', {
        accept: 'application/json',
      });
    },
    waitForFunction: async () => {
      observed.legacyWaitCalled = true;
      throw new Error('obsolete startup text predicate was used');
    },
    getByRole: (role, options) => {
      assert.equal(role, 'heading');
      assert.deepEqual(options, { name: 'Create your first Explorer', exact: true });
      return emptyStateHeading;
    },
    getByText: (text, options) => {
      assert.equal(text, 'New explorer');
      assert.deepEqual(options, { exact: true });
      return newExplorer;
    },
    url: () => entryUrl,
    unroute: async (pattern, handler) => {
      assert.equal(pattern, '**/*');
      assert.equal(handler, authRoute);
      observed.removed = true;
    },
  };
  const cda = {
    target,
    report,
    env: { LOOM_CDA_NO_AUTH: '0', LOOM_SCOPE001_AUTH_BEARER: token },
    action: async (label, locator) => {
      assert.equal(label, 'open native restricted-auth Explorer creation');
      assert.equal(locator, newExplorer);
      observed.newExplorerActionReached = true;
      throw navigationStop;
    },
    check: () => {},
  };

  await assert.rejects(scope001RestrictedAuthWorkflow({ page, cda }), error => error === navigationStop);
  assert.equal(observed.installed, true, 'workflow must reach and install request-scoped auth routing');
  assert.deepEqual(observed.proxyHeaders, {
    accept: 'application/json',
    authorization: `Bearer ${token}`,
  });
  assert.equal(observed.externalHeaders, null, 'external-origin traffic must receive no bearer');
  assert.equal(observed.removed, true, 'workflow must remove the route after the controlled failure');
  assert.equal(observed.legacyWaitCalled, false, 'startup must not wait for obsolete generic body text');
  assert.equal(observed.emptyStateHeadingWaited, true, 'startup must verify the explicit empty-project Explorer state');
  assert.equal(observed.newExplorerWaited, true, 'startup must wait for the visible native Explorer action');
  assert.equal(observed.newExplorerActionReached, true, 'the exact visible native Explorer action must be the next workflow step');
  assert.equal(report.authorization.browserNoAuth, true,
    'the generic report field describes a fresh context without global credentials');
  assert.equal(JSON.stringify(report).includes(token), false, 'the per-request bearer must not be recorded in the report');
  assert.deepEqual(report.target.authScope, {
    mode: 'calypr',
    browserContextHasNoGlobalCredentials: true,
    perRequestBearerAuthentication: true,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    organization: 'loom_dev_scope001',
    projectAlias: 'auth',
    expectedGrantResourcePath: '/programs/loom_dev_scope001/projects/auth',
    expectedReadScopeAuthResourcePaths: ['loom_dev_scope001-auth'],
    bearerInjectedOnlyOnOwnedUIApiProxyPaths: true,
    bearerValueRecorded: false,
    signatureValidationExercised: false,
    externalACLExercised: false,
    schemaFieldsResponseDiagnostics: { entries: [], dropped: 0, incomplete: 0 },
  });
});
