import assert from 'node:assert/strict';
import { basename } from 'node:path';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import {
  assertScope001RestrictedAuthVisibleRows,
  readScope001RestrictedAuthOracle,
} from '../helpers/scope-001-restricted-auth-oracle.mjs';

const ACTION_TIMEOUT_MS = 5_000;

export const scope001DisclosureSummary = details => details.locator(':scope > summary');

export const ensureScope001DisclosureOpen = async ({
  details,
  summary,
  action,
  label,
  waitForContent,
}) => {
  const isOpen = await details.evaluate(element => element.open === true);
  if (isOpen) {
    await waitForContent();
    return;
  }
  await action(label, summary, () => summary.click(), {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    after: waitForContent,
  });
};

const MAX_SCHEMA_FIELDS_DIAGNOSTIC_ENTRIES = 16;
const SCHEMA_FIELDS_DIAGNOSTIC_CAPTURE_TIMEOUT_MS = 300;
const ORGANIZATION = 'loom_dev_scope001';
const PROJECT_ALIAS = 'auth';
const BEARER_ENV = 'LOOM_SCOPE001_AUTH_BEARER';
export const SCOPE001_PROXY_AUTH_REQUIRED_CHECK =
  'restricted authorization was injected only into owned API proxy requests';

export const scope001RestrictedAuthBuilderURL = (target, explorer) => {
  const url = new URL(target.uiUrl);
  url.pathname = '/';
  url.search = '';
  url.hash = '';
  url.searchParams.set('organization', ORGANIZATION);
  url.searchParams.set('project', PROJECT_ALIAS);
  url.searchParams.set('explorer', explorer);
  url.searchParams.set('mode', 'builder');
  return url.toString();
};

/** The local demo proxies these browser API paths; do not authorize other UI traffic. */
export const isScope001AuthorizedApiProxyRequest = (requestURL, uiURL) => {
  try {
    const request = new URL(requestURL);
    return request.origin === new URL(uiURL).origin &&
      (request.pathname.startsWith('/api/') ||
        request.pathname === '/graphql' || request.pathname === '/graphql/graph');
  } catch {
    return false;
  }
};

export const isScope001SchemaFieldsRequest = (requestURL, uiURL, explorerId) => {
  try {
    const request = new URL(requestURL);
    const canonicalProject = `${ORGANIZATION}/${PROJECT_ALIAS}`;
    const encodedProject = encodeURIComponent(encodeURIComponent(canonicalProject));
    const expectedPath = `/api/v1/projects/${encodedProject}/explorers/${encodeURIComponent(String(explorerId))}/authoring/v2/schema-fields`;
    return Boolean(explorerId) && request.origin === new URL(uiURL).origin && request.pathname === expectedPath;
  } catch {
    return false;
  }
};

export const hasScope001AuthorizationHeader = headers => Object.entries(headers ?? {}).some(
  ([name, value]) => name.toLowerCase() === 'authorization' && String(value ?? '').trim().length > 0,
);

const safeScope001RequestId = value => typeof value === 'string' &&
  /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value.trim()) ? value.trim() : null;

const scope001RequestIdFromHeaders = headers => {
  const entries = Object.entries(headers ?? {});
  for (const name of ['x-request-id', 'request-id']) {
    const match = entries.find(([header]) => header.toLowerCase() === name);
    const requestId = safeScope001RequestId(match?.[1]);
    if (requestId) return requestId;
  }
  return null;
};

export const summarizeScope001SchemaFieldsResponse = ({
  status,
  authorizationHeaderPresent,
  body,
  bodyReadable,
  responseRequestId,
  callerRequestId,
}) => {
  const record = body && typeof body === 'object' && !Array.isArray(body) ? body : {};
  const error = record.error && typeof record.error === 'object' && !Array.isArray(record.error)
    ? record.error : record;
  const rawCode = typeof error.code === 'string' ? error.code : undefined;
  const fields = Array.isArray(record.fields) ? record.fields : undefined;
  const payloadRequestId = safeScope001RequestId(error.requestId) ?? safeScope001RequestId(record.requestId);
  return {
    status: Number.isInteger(status) ? status : null,
    requestId: safeScope001RequestId(responseRequestId) ?? payloadRequestId ?? safeScope001RequestId(callerRequestId),
    authorizationHeaderPresent: Boolean(authorizationHeaderPresent),
    bodyReadable: Boolean(bodyReadable),
    hasErrorEnvelope: Object.hasOwn(record, 'error'),
    errorCode: rawCode && /^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(rawCode) ? rawCode : null,
    authenticationRequiredMessage: error.message === 'authentication is required',
    hasFieldsArray: fields !== undefined,
    fieldCount: fields?.length ?? null,
    complete: typeof record.complete === 'boolean' ? record.complete : null,
    truncated: typeof record.truncated === 'boolean' ? record.truncated : null,
    nextCursorPresent: typeof record.nextCursor === 'string' && record.nextCursor.length > 0,
  };
};

export const createScope001SchemaFieldsResponseRecorder = ({
  uiUrl,
  explorerId,
  diagnostics,
  captureTimeoutMs = SCHEMA_FIELDS_DIAGNOSTIC_CAPTURE_TIMEOUT_MS,
}) => {
  let closed = false;
  const pending = new Map();

  const markIncomplete = entry => {
    if (entry.captureState !== 'pending') return;
    entry.captureState = 'incomplete';
    diagnostics.incomplete += 1;
    const timer = pending.get(entry);
    if (timer) clearTimeout(timer);
    pending.delete(entry);
  };

  const onResponse = response => {
    if (closed) return;
    const request = response.request();
    if (!isScope001SchemaFieldsRequest(request.url(), uiUrl, explorerId)) return;
    if (diagnostics.entries.length + diagnostics.dropped >= MAX_SCHEMA_FIELDS_DIAGNOSTIC_ENTRIES) {
      diagnostics.dropped += 1;
      return;
    }

    let fallbackHeaders = {};
    let fallbackResponseHeaders = {};
    try { fallbackHeaders = request.headers(); } catch {}
    try {
      if (typeof response.headers === 'function') fallbackResponseHeaders = response.headers();
    } catch {}
    const callerRequestId = scope001RequestIdFromHeaders(fallbackHeaders);
    const responseRequestId = scope001RequestIdFromHeaders(fallbackResponseHeaders);
    const entry = {
      status: Number.isInteger(response.status()) ? response.status() : null,
      requestId: responseRequestId ?? callerRequestId,
      authorizationHeaderPresent: hasScope001AuthorizationHeader(fallbackHeaders),
      bodyReadable: false,
      hasErrorEnvelope: false,
      errorCode: null,
      authenticationRequiredMessage: false,
      hasFieldsArray: null,
      fieldCount: null,
      complete: null,
      truncated: null,
      nextCursorPresent: false,
      captureState: 'pending',
    };
    diagnostics.entries.push(entry);

    const timer = setTimeout(() => markIncomplete(entry), captureTimeoutMs);
    pending.set(entry, timer);
    const headersPromise = Promise.resolve().then(() =>
      typeof request.allHeaders === 'function' ? request.allHeaders() : fallbackHeaders);
    const bodyPromise = Promise.resolve().then(() => response.json());
    void Promise.allSettled([headersPromise, bodyPromise]).then(([headersResult, bodyResult]) => {
      if (closed || entry.captureState !== 'pending') return;
      const headers = headersResult.status === 'fulfilled' ? headersResult.value : fallbackHeaders;
      const body = bodyResult.status === 'fulfilled' ? bodyResult.value : undefined;
      Object.assign(entry, summarizeScope001SchemaFieldsResponse({
        status: entry.status,
        authorizationHeaderPresent: hasScope001AuthorizationHeader(headers),
        body,
        bodyReadable: bodyResult.status === 'fulfilled',
        responseRequestId,
        callerRequestId: scope001RequestIdFromHeaders(headers),
      }));
      entry.captureState = 'complete';
      clearTimeout(pending.get(entry));
      pending.delete(entry);
    });
  };

  return {
    onResponse,
    close() {
      if (closed) return;
      closed = true;
      for (const entry of pending.keys()) markIncomplete(entry);
    },
  };
};

export const readScope001RenderedRows = table => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  return {
    headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => normalize(cell.textContent)),
    rows: [...table.querySelectorAll('[role="row"]')].slice(1)
      .filter(row => row.querySelector('[role="cell"]'))
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => normalize(cell.innerText))),
  };
};

export const readScope001ProposalRows = table => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  return {
    headers: [...table.querySelectorAll('thead th')].map(cell =>
      normalize(cell.firstElementChild?.textContent ?? cell.textContent)),
    rows: [...table.querySelectorAll('tbody tr')].map(row =>
      [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText))),
  };
};

const waitFor = (page, predicate, argument, timeoutMs = ACTION_TIMEOUT_MS) => page.waitForFunction(
  ({ predicateSource, value }) => Boolean(Function('value', `return (${predicateSource})(value);`)(value)),
  { predicateSource: predicate.toString(), value: argument },
  { timeout: Math.min(ACTION_TIMEOUT_MS, timeoutMs) },
);

const proposalPreview = page => page.locator('[data-testid="construction-proposal-preview"]');

export const assertScope001SavedPatientRows = (visible, patientId) => {
  assert.deepEqual(visible.headers, ['Patient ID'],
    'the saved Patient-root table must retain exactly its Patient ID column');
  assert.deepEqual(visible.rows, [[patientId]],
    'the saved Patient-root table must retain exactly its literal Patient row');
  return visible;
};

const waitForSavedPatientRows = async (page, patientId) => waitFor(page, expected => {
  const region = document.querySelector('[data-testid="construction-preview"]');
  const table = region?.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (region?.dataset.previewStatus !== 'ready' || !table) return false;
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const headers = [...table.querySelectorAll('[role="columnheader"]')].map(cell => normalize(cell.textContent));
  const rows = [...table.querySelectorAll('[role="row"]')].slice(1)
    .filter(row => row.querySelector('[role="cell"]'))
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => normalize(cell.innerText)));
  return JSON.stringify({ headers, rows }) === JSON.stringify({ headers: ['Patient ID'], rows: [[expected.patientId]] });
}, { patientId }, ACTION_TIMEOUT_MS);

export const waitForVisibleRestrictedRows = async (page, expectedRows, blockedObservationId, blockedValue, label) => {
  await waitFor(page, expected => {
    const region = document.querySelector('[data-testid="construction-preview"]');
    const table = region?.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (region?.dataset.previewStatus !== 'ready' || !table) return false;
    const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = [...table.querySelectorAll('[role="columnheader"]')].map(cell => normalize(cell.textContent));
    const patientIndex = headers.indexOf('Patient ID');
    const observationIndex = headers.findIndex(header => /observation/i.test(header) && /\bIDs?\b/i.test(header));
    const valueIndex = headers.findIndex(header => /value/i.test(header) && !/\bIDs?\b/i.test(header));
    if (headers.length !== 3 || patientIndex < 0 || observationIndex < 0 || valueIndex < 0) return false;
    const rows = [...table.querySelectorAll('[role="row"]')].slice(1)
      .filter(row => row.querySelector('[role="cell"]'))
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => normalize(cell.innerText)));
    if (rows.some(row => row.includes(expected.blockedObservationId)
      || row.includes(String(expected.blockedValue)))) return false;
    const actual = rows.map(row => ({
      patientId: row[patientIndex],
      observationId: row[observationIndex],
      values: [Number(row[valueIndex])],
    }));
    return JSON.stringify(actual) === JSON.stringify(expected.expectedRows);
  }, { expectedRows, blockedObservationId, blockedValue }, ACTION_TIMEOUT_MS).catch(error => {
    throw new Error(`${label}: the exact authorized rows did not settle within ${ACTION_TIMEOUT_MS} ms: ${error.message}`);
  });
};

const assertVisibleRestrictedRows = async (page, fixtureDir, label) => {
  const table = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
  await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const visible = await table.evaluate(readScope001RenderedRows);
  const patientIndexes = visible.headers.flatMap((header, index) => header === 'Patient ID' ? [index] : []);
  const observationIndexes = visible.headers.flatMap((header, index) =>
    /observation/i.test(header) && /\bIDs?\b/i.test(header) ? [index] : []);
  const valueIndexes = visible.headers.flatMap((header, index) =>
    /value/i.test(header) && !/\bIDs?\b/i.test(header) ? [index] : []);
  assert.equal(patientIndexes.length, 1, `${label}: expected one exact Patient ID column: ${JSON.stringify(visible.headers)}`);
  assert.equal(observationIndexes.length, 1, `${label}: expected one related Observation ID column: ${JSON.stringify(visible.headers)}`);
  assert.equal(valueIndexes.length, 1, `${label}: expected one related Observation value column: ${JSON.stringify(visible.headers)}`);
  assert.equal(visible.headers.length, 3, `${label}: output must contain only Patient ID, related Observation ID, and related value`);

  const [patientIndex] = patientIndexes;
  const [observationIndex] = observationIndexes;
  const [valueIndex] = valueIndexes;
  const rows = visible.rows.map(row => ({
    patientId: row[patientIndex],
    observationId: row[observationIndex],
    values: [Number(row[valueIndex])],
  }));
  const oracle = assertScope001RestrictedAuthVisibleRows(rows, fixtureDir);
  assert.equal(rows.length, 1, `${label}: only the one granted Observation may contribute a visible row`);
  assert.equal(rows[0].patientId, oracle.patientId, `${label}: the exact allowed Patient must remain visible`);
  assert.equal(rows[0].observationId, oracle.expectedVisibleRows[0].observationId,
    `${label}: only the allowed Observation ID may be visible`);
  assert.equal(rows[0].values[0], 111, `${label}: the allowed Observation value must remain literal 111`);
  assert.equal(visible.rows.flat().includes(oracle.blockedObservationId), false,
    `${label}: blocked Observation ID must not appear in the visible Builder table`);
  assert.equal(visible.rows.flat().includes('999'), false,
    `${label}: blocked Observation value must not appear in the visible Builder table`);
  return { visible, rows, oracle };
};

export const scope001RestrictedAuthWorkflow = async ({ page, cda }) => {
  configureNativePage(page);
  const { target, report, action, check, env } = cda;
  const oracle = readScope001RestrictedAuthOracle(target.fixtureDir);
  assert.equal(target.fixtureProject, 'loom_dev_scope001-auth', 'Restricted-auth UI must target the exact isolated storage project');
  assert.equal(target.fixtureGeneration, 'scope001-auth-v1', 'Restricted-auth UI must target the exact isolated generation');
  assert.equal(basename(target.fixtureDir), 'scope-001-restricted-auth', 'Restricted-auth UI must use its exact two-file fixture');
  assert.equal(env.LOOM_CDA_NO_AUTH, '0', 'Restricted-auth API mode must remain enabled');

  const bearer = env[BEARER_ENV] ?? process.env[BEARER_ENV];
  assert.equal(typeof bearer, 'string', `${BEARER_ENV} must be supplied privately by the owned target launcher`);
  assert(bearer.length >= 24 && !/\s/.test(bearer), `${BEARER_ENV} must be a nonempty compact bearer token`);

  const uiOrigin = new URL(target.uiUrl).origin;
  const apiOrigin = new URL(target.apiUrl).origin;
  assert.notEqual(uiOrigin, apiOrigin, 'The restricted-auth UI and API must retain their distinct owned origins');
  let authorizedApiProxyRequests = 0;
  const authorizationHeader = `Bearer ${bearer}`;
  const schemaFieldsDiagnostics = { entries: [], dropped: 0, incomplete: 0 };
  let schemaFieldsRecorder;
  const authRoute = async route => {
    const request = route.request();
    if (!isScope001AuthorizedApiProxyRequest(request.url(), target.uiUrl)) {
      await route.continue();
      return;
    }
    authorizedApiProxyRequests += 1;
    await route.continue({
      headers: { ...request.headers(), authorization: authorizationHeader },
    });
  };

  report.target.authScope = {
    mode: 'calypr',
    browserContextHasNoGlobalCredentials: report.authorization.browserNoAuth,
    perRequestBearerAuthentication: true,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    organization: ORGANIZATION,
    projectAlias: PROJECT_ALIAS,
    expectedGrantResourcePath: '/programs/loom_dev_scope001/projects/auth',
    expectedReadScopeAuthResourcePaths: ['loom_dev_scope001-auth'],
    bearerInjectedOnlyOnOwnedUIApiProxyPaths: true,
    bearerValueRecorded: false,
    signatureValidationExercised: false,
    externalACLExercised: false,
  };
  report.target.authScope.schemaFieldsResponseDiagnostics = schemaFieldsDiagnostics;
  report.target.fixtureOracle = {
    kind: 'independent literal restricted-auth Patient/Observation fixture',
    path: target.fixtureDir,
    patientId: oracle.patientId,
    allowedObservationId: oracle.expectedVisibleRows[0].observationId,
    allowedValue: 111,
    blockedObservationId: oracle.blockedObservationId,
    blockedValue: 999,
  };
  check('correctness', 'native browser auth mode targets the exact restricted-auth project and generation', true,
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      browserContextHasNoGlobalCredentials: report.target.authScope.browserContextHasNoGlobalCredentials,
      perRequestBearerAuthentication: report.target.authScope.perRequestBearerAuthentication,
      expectedGrantResourcePath: report.target.authScope.expectedGrantResourcePath,
      expectedReadScopeAuthResourcePaths: report.target.authScope.expectedReadScopeAuthResourcePaths,
      signatureValidationExercised: false, externalACLExercised: false });

  await page.route('**/*', authRoute);
  try {
    const explorerToOpen = target.explorer || 'default';
    const entryURL = scope001RestrictedAuthBuilderURL(target, explorerToOpen);
    await page.goto(entryURL, { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await page.getByRole('heading', { name: 'Create your first Explorer', exact: true })
      .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const newExplorer = page.getByText('New explorer', { exact: true });
    await newExplorer.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const routedURL = new URL(page.url());
    assert.equal(routedURL.searchParams.get('organization'), ORGANIZATION,
      'Builder must route through the exact allowed Calypr organization');
    assert.equal(routedURL.searchParams.get('project'), PROJECT_ALIAS,
      'Builder must route through the exact allowed Calypr project');
    assert.equal(routedURL.searchParams.get('mode'), 'builder', 'restricted-auth case must use Builder mode');

    await action('open native restricted-auth Explorer creation', newExplorer, () => newExplorer.click(), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    });
    const title = 'SCOPE-001 restricted related records';
    const titleInput = page.locator('#new-explorer-name');
    await action('name the restricted-auth Explorer', titleInput, () => titleInput.fill(title), {
      editable: true,
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
    });
    const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
    await action('create a fresh restricted-auth Builder Explorer', createBlank, () => createBlank.click(), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      after: async () => {
        await waitFor(page, expected =>
          document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected &&
          document.body.innerText.includes('Build your first table') &&
          document.querySelectorAll('[role="alert"]').length === 0, title);
        await page.getByRole('heading', { name: 'Build your first table', exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await page.getByRole('button', { name: 'Choose Patient rows', exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      },
    });
    const explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
    assert(explorer && explorer !== explorerToOpen, 'The restricted-auth case must use its newly created Explorer');
    check('correctness', 'fresh Explorer creation opens a usable Builder without load alerts', true,
      { explorer, initialProjectState: 'empty', firstTableActionVisible: true, loadAlertsVisible: false });
    const createdURL = new URL(page.url());
    assert.equal(createdURL.searchParams.get('organization'), ORGANIZATION);
    assert.equal(createdURL.searchParams.get('project'), PROJECT_ALIAS);
    assert.equal(createdURL.searchParams.get('explorer'), explorer);
    assert.equal(createdURL.searchParams.get('mode'), 'builder');
    target.explorer = explorer;
    report.target.explorer = explorer;
    report.explorer = explorer;
    schemaFieldsRecorder = createScope001SchemaFieldsResponseRecorder({
      uiUrl: target.uiUrl,
      explorerId: explorer,
      diagnostics: schemaFieldsDiagnostics,
    });
    page.on('response', schemaFieldsRecorder.onResponse);
    check('persistence', 'native UI created a fresh Explorer in the exact restricted-auth project', true,
      { explorer, project: target.fixtureProject, generation: target.fixtureGeneration });

    const tableName = page.locator('#first-table-name');
    await action('name the allowed Patient root table', tableName, () => tableName.fill('Authorized Patients'), {
      editable: true,
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
    });
    const savedPatientTable = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
    const patientRows = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('choose authorized Patient rows for the native root table', patientRows,
      () => patientRows.click(), {
        timeout: ACTION_TIMEOUT_MS,
        budget: ACTION_TIMEOUT_MS,
        after: async () => {
          await waitForSavedPatientRows(page, oracle.patientId);
          assertScope001SavedPatientRows(await savedPatientTable.evaluate(readScope001RenderedRows), oracle.patientId);
        },
      });
    check('correctness', 'native Patient root selection uses the exact restricted-auth project and generation', true,
      { resourceType: 'Patient', project: target.fixtureProject, generation: target.fixtureGeneration });

    check('correctness', 'native Patient root has the exact saved authorized row before related fields', true,
      { patientId: oracle.patientId, columns: ['Patient ID'] });

    const proposeRelatedObservationFields = async stage => {
      const step = label => `${stage}: ${label}`;
      const addColumns = page.getByTestId('construction-action-add-columns');
      const addEditor = page.locator('[aria-label="Add columns editor"]');
      await action(step('open native Add columns'), addColumns, () => addColumns.click(), {
        timeout: ACTION_TIMEOUT_MS,
        budget: ACTION_TIMEOUT_MS,
        after: () => addEditor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      });
      const relatedFieldsButton = page.locator('[aria-label="Column types"] button')
        .filter({ hasText: 'Fields and related data' });
      await action(step('choose native fields and related data'), relatedFieldsButton,
        () => relatedFieldsButton.click(), {
          timeout: ACTION_TIMEOUT_MS,
          budget: ACTION_TIMEOUT_MS,
          after: () => page.locator('[data-testid="construction-add-columns-source"]').waitFor({
            state: 'visible', timeout: ACTION_TIMEOUT_MS,
          }),
        });
      const relatedResources = page.locator('[aria-label="Related resources"]');
      const relatedResourcesSummary = scope001DisclosureSummary(relatedResources);
      const observationSource = relatedResources.getByRole('button', {
        name: 'Observation, Related resource', exact: true,
      });
      await ensureScope001DisclosureOpen({
        details: relatedResources,
        summary: relatedResourcesSummary,
        action,
        label: step('open related resource choices'),
        waitForContent: () => observationSource.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      });
      await action(step('select related Observation records'), observationSource, () => observationSource.click(), {
        timeout: ACTION_TIMEOUT_MS,
        budget: ACTION_TIMEOUT_MS,
        after: () => page.locator('[data-testid="feature-catalog-raw-fields"] > summary').waitFor({
          state: 'visible', timeout: ACTION_TIMEOUT_MS,
        }),
      });
      const rawFields = page.locator('[data-testid="feature-catalog-raw-fields"]');
      const rawFieldsSummary = scope001DisclosureSummary(rawFields);
      const observationIdField = page.locator('input[type="checkbox"][aria-label="Select Observation.id"]');
      const observationValueField = page.locator('input[type="checkbox"][aria-label="Select Observation.valueQuantity.value"]');
      const waitForObservationFields = async () => {
        await Promise.all([
          observationIdField.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
          observationValueField.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
        ]);
      };
      await ensureScope001DisclosureOpen({
        details: rawFields,
        summary: rawFieldsSummary,
        action,
        label: step('open raw Observation fields'),
        waitForContent: waitForObservationFields,
      });
      await action(step('select the related Observation ID field'), observationIdField,
        () => observationIdField.check({ timeout: ACTION_TIMEOUT_MS }), {
          timeout: ACTION_TIMEOUT_MS,
          budget: ACTION_TIMEOUT_MS,
          after: () => assertCheckboxState(observationIdField, true),
        });
      await action(step('select the related Observation value field'), observationValueField,
        () => observationValueField.check({ timeout: ACTION_TIMEOUT_MS }), {
          timeout: ACTION_TIMEOUT_MS,
          budget: ACTION_TIMEOUT_MS,
          after: () => assertCheckboxState(observationValueField, true),
        });

      const addSelectedFields = page.getByRole('button', { name: 'Add 2 selected features', exact: true });
      const dialog = page.getByRole('dialog');
      await action(step('choose native relationship and all-value fields'), addSelectedFields,
        () => addSelectedFields.click(), {
          timeout: ACTION_TIMEOUT_MS,
          budget: ACTION_TIMEOUT_MS,
          after: () => dialog.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
        });
      const directPathRadios = dialog.locator('input[type="radio"][aria-label$=": Patient <-[subject]- Observation"]');
      const directPathCount = await directPathRadios.count();
      if (directPathCount > 0) {
        assert.equal(directPathCount, 2, 'Both Observation fields must expose the direct Patient subject route');
        for (let index = 0; index < directPathCount; index += 1) {
          const routeRadio = directPathRadios.nth(index);
          if (!await routeRadio.isChecked()) {
            await action(step(`select direct Observation subject path ${index + 1}`), routeRadio,
              () => routeRadio.check({ timeout: ACTION_TIMEOUT_MS }), {
                timeout: ACTION_TIMEOUT_MS,
                budget: ACTION_TIMEOUT_MS,
                after: () => assertCheckboxState(routeRadio, true),
              });
          }
        }
      } else {
        const pathTexts = await dialog.locator('[data-testid="construction-traversal-path"]').allInnerTexts();
        assert(pathTexts.length >= 2 && pathTexts.every(text => text.includes('Patient') && text.includes('[subject]') && text.includes('Observation')),
          `Both fields must use the direct Patient subject path: ${JSON.stringify(pathTexts)}`);
      }
      const keepAllValueRadios = dialog.locator('input[type="radio"][aria-label$=": Keep all matching values"]');
      assert.equal(await keepAllValueRadios.count(), 2, 'Both related fields must have the explicit all-matching-values form');
      for (let index = 0; index < 2; index += 1) {
        const keepAll = keepAllValueRadios.nth(index);
        if (!await keepAll.isChecked()) {
          await action(step(`keep all matching Observation values ${index + 1}`), keepAll,
            () => keepAll.check({ timeout: ACTION_TIMEOUT_MS }), {
              timeout: ACTION_TIMEOUT_MS,
              budget: ACTION_TIMEOUT_MS,
              after: () => assertCheckboxState(keepAll, true),
            });
        }
      }

      const addColumnsButton = dialog.getByRole('button', { name: 'Add 2 columns', exact: true });
      await action(step('preview the restricted related Observation fields'), addColumnsButton,
        () => addColumnsButton.click(), {
          timeout: ACTION_TIMEOUT_MS,
          budget: ACTION_TIMEOUT_MS,
          after: async () => {
            await dialog.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
            await waitFor(page, () => {
              const panel = document.querySelector('[data-testid="construction-proposal-panel"]') ??
                document.querySelector('[data-testid="construction-choice-proposal-panel"]');
              return panel?.dataset.proposalStatus === 'ready';
            }, undefined);
            const visible = await proposalPreview(page).locator('table').evaluate(readScope001ProposalRows);
            assertScope001ProposalRows(visible, oracle.expectedVisibleRows, oracle.blockedObservationId,
              'restricted related Observation proposal');
          },
        });
      return page.locator('[data-testid="construction-proposal-panel"], [data-testid="construction-choice-proposal-panel"]');
    };

    const firstProposalPanel = await proposeRelatedObservationFields('cancel checkpoint');
    check('correctness', 'native related-field Preview shows only the allowed Patient/Observation tuple', true,
      { expectedRows: oracle.expectedVisibleRows, blockedObservationId: oracle.blockedObservationId,
        blockedValue: 999, directRoute: 'Patient <-[subject]- Observation' });
    const cancelProposal = firstProposalPanel.getByRole('button', { name: 'Cancel', exact: true });
    await action('cancel the proposed related-field change', cancelProposal, () => cancelProposal.click(), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      after: async () => {
        await firstProposalPanel.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
        await waitForSavedPatientRows(page, oracle.patientId);
        assertScope001SavedPatientRows(await savedPatientTable.evaluate(readScope001RenderedRows), oracle.patientId);
      },
    });
    check('persistence', 'Cancel preserves the exact saved Patient-root row and columns', true,
      { patientId: oracle.patientId, columns: ['Patient ID'], canceledObservationIDs: true });

    const proposalPanel = await proposeRelatedObservationFields('apply checkpoint');
    const applyProposal = page.getByTestId('construction-apply-proposal');
    if (await applyProposal.count() !== 1) {
      const choiceApply = page.getByRole('button', { name: 'Apply columns', exact: true });
      assert.equal(await choiceApply.count(), 1, 'The exact related-field proposal must expose one native Apply columns control');
    }
    const applyControl = await applyProposal.count() === 1 ? applyProposal : page.getByRole('button', { name: 'Apply columns', exact: true });
    await action('apply the restricted related Observation fields', applyControl,
      () => applyControl.click(), {
        timeout: ACTION_TIMEOUT_MS,
        budget: ACTION_TIMEOUT_MS,
        after: async () => {
          await proposalPanel.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
          await waitForVisibleRestrictedRows(page, oracle.expectedVisibleRows,
            oracle.blockedObservationId, 999, 'applied restricted-auth table');
          await assertVisibleRestrictedRows(page, target.fixtureDir, 'applied restricted-auth table');
        },
      });
    check('persistence', 'Apply saves exactly the allowed related Observation ID and value', true,
      { expectedRows: oracle.expectedVisibleRows, blockedObservationId: oracle.blockedObservationId, blockedValue: 999 });

    const outputTab = page.locator('[data-testid^="construction-table-"][aria-current="page"]');
    await action('reload the applied restricted-auth Builder table', outputTab,
      () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
        timeout: ACTION_TIMEOUT_MS,
        budget: ACTION_TIMEOUT_MS,
        after: async () => {
          await outputTab.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
          await waitForVisibleRestrictedRows(page, oracle.expectedVisibleRows,
            oracle.blockedObservationId, 999, 'reloaded restricted-auth table');
          await assertVisibleRestrictedRows(page, target.fixtureDir, 'reloaded restricted-auth table');
        },
      });
    check('persistence', 'reload retains only the exact authorized Patient and related Observation row', true,
      { expectedRows: oracle.expectedVisibleRows, blockedObservationId: oracle.blockedObservationId, blockedValue: 999 });

    assert(authorizedApiProxyRequests > 0, 'The browser must send Builder API requests through the owned auth proxy');
    report.target.authScope.authorizedApiProxyRequestCount = authorizedApiProxyRequests;
    check('correctness', SCOPE001_PROXY_AUTH_REQUIRED_CHECK, true,
      { authorizedApiProxyRequestCount: authorizedApiProxyRequests, uiOrigin, apiOrigin,
        externalOriginsModified: false, bearerValueRecorded: false });
    assert.equal(JSON.stringify(report).includes(bearer), false, 'The synthetic bearer must never appear in verification evidence');
  } finally {
    schemaFieldsRecorder?.close();
    if (schemaFieldsRecorder) page.off('response', schemaFieldsRecorder.onResponse);
    await page.unroute('**/*', authRoute);
  }
};

async function assertCheckboxState(locator, expected) {
  await locator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  assert.equal(await locator.isChecked(), expected);
}

export const assertScope001ProposalRows = (visible, expectedRows, blockedObservationId, label) => {
  assert(visible.headers.length >= 3, `${label}: Preview must expose Patient, Observation ID, and value columns`);
  const patientIndex = visible.headers.findIndex(header => header === 'Patient ID');
  const observationIndex = visible.headers.findIndex(header => /observation/i.test(header) && /\bIDs?\b/i.test(header));
  const valueIndex = visible.headers.findIndex(header => /value/i.test(header) && !/\bIDs?\b/i.test(header));
  assert(patientIndex >= 0 && observationIndex >= 0 && valueIndex >= 0,
    `${label}: required field headers were missing: ${JSON.stringify(visible.headers)}`);
  const actualRows = visible.rows.map(row => ({
    patientId: row[patientIndex],
    observationId: row[observationIndex],
    values: [Number(row[valueIndex])],
  }));
  assert.deepEqual(actualRows, expectedRows, `${label}: visible proposal rows must match the independent literal fixture oracle`);
  assert.equal(visible.rows.flat().includes(blockedObservationId), false, `${label}: blocked Observation ID must remain absent`);
  assert.equal(visible.rows.flat().includes('999'), false, `${label}: blocked Observation value must remain absent`);
  return visible;
};
