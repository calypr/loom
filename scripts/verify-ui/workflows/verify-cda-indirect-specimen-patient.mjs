import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { sanitizeBody, sanitizeText } from '../helpers/playwright-browser.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { waitForCondition } from '../helpers/playwright-observations.mjs';

export async function indirectSpecimenPatientWorkflow({ page, cda, mode = process.env.LOOM_INDIRECT_SPECIMEN_PATIENT_MODE ?? 'values' }) {
const project = cda.project;
const explorerId = cda.explorer;
assert(['values', 'count'].includes(mode), `Unknown indirect route mode: ${mode}`);
const countMode = mode === 'count';
const specimenId = 'b7cad184-db67-5542-a975-10fffa3e89e7';
const knownObservationId = '35cfec85-56e8-5257-af99-2e9345be2011';
const knownPatientId = 'afcfb15e-7617-5691-ae2c-ab675322fb33';
const generation = cda.generation ?? process.env.LOOM_CDA_GENERATION ?? 'cda-fhir-v1';
const explorerRoot = `/api/v1/projects/${encodeURIComponent(project ?? '')}/explorers`;
const expectedRoute = '2-relationship path: Specimen to Observation to Patient via Specimen then Subject';
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = cda.target.arangoContainer;
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const startedAt = Date.now();
const evidenceDirectory = cda.evidenceDirectory;
const tableName = `CDA indirect route QA ${Date.now()}`;
const report = {
  scenario: `CDA Specimen → Observation → Patient.id ${mode} journey`,
  mode,
  target: { apiUrl: uiOrigin, project, explorerId, pageURL },
  temporaryTable: tableName,
  performanceGateMs: 5000,
  oracle: undefined,
  selection: undefined,
  clicks: [],
  requests: [],
  responses: [],
  browserErrors: [],
  incidentalErrors: [],
  nativeRequests: [],
  errors: [],
  renderedPreviews: [],
  assertions: [],
  cleanup: { attempted: false, complete: false },
};

let tableCreated = false;
let scenarioError;
let cleanupError;
const waitForBrowser = (targetPage, condition, timeout = 30000) => waitForCondition(targetPage, condition, timeout);
const inspectPage = (targetPage, inspect, argument) => targetPage.evaluate(inspect, argument);
let actionTracker = {};
const performAction = async (_tracker, label, locator, perform, options = {}) => cda.action(
  label, locator, target => perform(target, { timeout: options.timeout ?? 5000 }), options);

const addAssertion = (name, passed, detail) => {
  report.assertions.push({ name, passed, detail });
  assert(passed, `${name}${detail === undefined ? '' : `: ${JSON.stringify(detail)}`}`);
};

const ownedApiRequest = async (path, { method = 'GET', body } = {}) => {
  const requestId = randomUUID();
  const entry = { requestId, method, path, origin: new URL(apiOrigin).origin, startedAt: Date.now(), ...(body ? { body } : {}) };
  report.requests.push(entry);
  const response = await fetch(`${apiOrigin}${path}`, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  entry.status = response.status;
  entry.completedAt = Date.now();
  const value = await response.json();
  const diagnostic = sanitizeBody(JSON.stringify(value));
  try { entry.response = JSON.parse(diagnostic); }
  catch { entry.response = diagnostic; }
  return { response, entry, value };
};

const shellQuote = (value) => `'${value.replaceAll("'", "'\\''")}'`;

function queryCdaFhirEdgeOracle() {
  const aql = `
    LET specimen = FIRST(
      FOR r IN Specimen
      FILTER r.project_id == "${project}"
        AND r.dataset_generation == "${generation}"
        AND r.id == "${specimenId}"
      RETURN r
    )
    LET chain = (
      FOR specimenEdge IN fhir_edge
      FILTER specimenEdge.project == "${project}"
        AND specimenEdge.dataset_generation == "${generation}"
        AND specimenEdge.label == "specimen_Specimen"
        AND specimenEdge._to == specimen._id
      LET observation = DOCUMENT(specimenEdge._from)
      FILTER observation.project_id == "${project}"
        AND observation.dataset_generation == "${generation}"
      FOR subjectEdge IN fhir_edge
      FILTER subjectEdge.project == "${project}"
        AND subjectEdge.dataset_generation == "${generation}"
        AND subjectEdge.label == "subject_Patient"
        AND subjectEdge._from == observation._id
      LET patient = DOCUMENT(subjectEdge._to)
      FILTER patient.project_id == "${project}"
        AND patient.dataset_generation == "${generation}"
      RETURN {
        observationId: observation.id,
        patientId: patient.id,
        specimenEdge: {
          id: specimenEdge._id,
          label: specimenEdge.label,
          from: specimenEdge._from,
          to: specimenEdge._to,
        },
        subjectEdge: {
          id: subjectEdge._id,
          label: subjectEdge.label,
          from: subjectEdge._from,
          to: subjectEdge._to,
        },
      }
    )
    RETURN { specimenId: specimen.id, chain }
  `;
  const js = `db._useDatabase("loom_dev"); const aql = ${JSON.stringify(aql)}; print(JSON.stringify(db._query(aql).toArray()));`;
  const command = `arangosh --server.endpoint tcp://127.0.0.1:8529 --server.username root --server.password "$ARANGO_ROOT_PASSWORD" --javascript.execute-string ${shellQuote(js)}`;
  const stdout = execFileSync('docker', ['exec', arangoContainer, 'sh', '-lc', command], { encoding: 'utf8' });
  const result = JSON.parse(stdout.trim().split(/\r?\n/).at(-1));
  assert.equal(result.length, 1, 'Arango oracle did not find the requested Specimen');
  const oracle = result[0];
  assert.equal(oracle.specimenId, specimenId);
  assert(oracle.chain.some((item) => item.observationId === knownObservationId && item.patientId === knownPatientId),
    'Arango oracle did not confirm the known Specimen → Observation → Patient chain');
  assert(oracle.chain.length > 0, 'Arango oracle returned no two-hop matches');
  return { container: arangoContainer, database: 'loom_dev', project, generation, specimenId, aql, ...oracle };
}

const actionLocator = async (selector, label) => {
  const pageRef = page;
  if (selector.startsWith('button text: ')) return pageRef.getByRole('button', { name: selector.slice('button text: '.length), exact: true });
  if (selector === 'Other relationship paths details') return pageRef.getByRole('dialog').locator('summary').filter({ hasText: 'Other relationship paths' });
  if (selector === 'Rows settings / Starting collection summary') return pageRef.getByRole('dialog', { name: 'Row definition settings' }).locator('summary').filter({ hasText: 'Starting collection:' });
  if (selector === 'Rows settings / Starting collection / Use selected resources') return pageRef.getByRole('dialog', { name: 'Row definition settings' }).getByRole('button', { name: 'Use selected resources', exact: true });
  if (selector === 'Rows settings / Back to table') return pageRef.getByRole('dialog', { name: 'Row definition settings' }).getByRole('button', { name: 'Back to table', exact: true });
  if (selector.startsWith('button ending in ')) {
    const suffix = selector.slice('button ending in '.length);
    const candidates = pageRef.locator('button');
    const matches = await candidates.evaluateAll((nodes, value) => nodes.flatMap((node, index) => node.innerText.trim().endsWith(value) ? [index] : []), suffix);
    assert.equal(matches.length, 1, `${label}: expected one temporary table button, found ${matches.length}`);
    return candidates.nth(matches[0]);
  }
  if (selector.startsWith('button in ')) return pageRef.getByTestId(selector.slice('button in '.length)).getByRole('button', { name: /Check matching rows|Retry coverage check|Retry match check/i });
  if (label === 'Choose Patient related source') return pageRef.getByRole('button', { name: selector, exact: true });
  if (label.startsWith('Select Specimen → Observation → Patient route')) return pageRef.getByRole('radio', { name: selector, exact: true });
  if (label.startsWith('Choose ')) return pageRef.getByRole('radio', { name: selector, exact: true });
  return pageRef.locator(selector);
};

const recordClick = async (label, selector) => {
  const click = {
    sequence: report.clicks.length + 1,
    label,
    selector,
  method: 'Playwright locator click',
    startedAt: new Date().toISOString(),
  };
  report.clicks.push(click);
  const started = Date.now();
  try {
    actionTracker.activeAction = { label, locator: selector, targetLocator: page.locator('body'), startedAt: started };
    const locator = await actionLocator(selector, label);
    actionTracker.activeAction.targetLocator = locator;
    actionTracker.activeAction.locator = locator.toString();
    await performAction(actionTracker, label, locator, (target, options) => target.click(options));
    actionTracker.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs: Date.now() - started };
    click.durationMs = Date.now() - started;
    click.completedAt = new Date().toISOString();
  } catch (error) {
    click.durationMs = Date.now() - started;
    click.error = error instanceof Error ? error.message : String(error);
    throw error;
  }
};

const clickButton = (text, label) => recordClick(label, `button text: ${text}`);

const setInput = async (selector, value, label) => {
  const item = {
    sequence: report.clicks.length + 1,
    label,
    selector,
    method: 'Playwright locator fill',
    value,
    startedAt: new Date().toISOString(),
  };
  report.clicks.push(item);
  const started = Date.now();
  try {
    actionTracker.activeAction = { label, locator: selector, targetLocator: page.locator('body'), startedAt: started };
    const locator = page.locator(selector);
    actionTracker.activeAction.targetLocator = locator;
    actionTracker.activeAction.locator = locator.toString();
    await performAction(actionTracker, label, locator, (target, options) => target.fill(value, options), { editable: true });
    actionTracker.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs: Date.now() - started };
    item.durationMs = Date.now() - started;
    item.completedAt = new Date().toISOString();
  } catch (error) {
    item.durationMs = Date.now() - started;
    item.error = error instanceof Error ? error.message : String(error);
    throw error;
  }
};

let requestMonitor;
const refreshResponses = async () => {
  await requestMonitor.flush();
  report.responses = report.nativeRequests.filter(response => response.status !== undefined).map(entry => ({
    requestId: entry.requestId, path: entry.path, status: entry.status,
    elapsedMs: (entry.responseReceivedAt ?? entry.completedAt ?? entry.startedAt) - entry.startedAt,
    timestamp: new Date(entry.responseReceivedAt ?? entry.completedAt ?? entry.startedAt).toISOString(),
    body: entry.response,
  }));
};
const screenshot = async (label) => {
  await page.screenshot({ path: join(evidenceDirectory, `${label}.png`), fullPage: true });
};

const decodeResponseBody = async (response) => response.body;

const domPreview = async (selector = '[data-testid="preview-table-scroll"]') => inspectPage(page, selector => {
  const root=document.querySelector(selector);
  const table=root?.querySelector('[role="table"]');
  return {
    text:root?.innerText ?? '',
    rowCount:Number(table?.getAttribute('aria-rowcount') ?? 0),
    headers:[...root?.querySelectorAll('[role="columnheader"]') ?? []].map(cell=>cell.innerText.trim()),
    rows:[...root?.querySelectorAll('[role="row"]') ?? []].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())),
    busy:root?.getAttribute('aria-busy') ?? null,
  };
}, selector);

const recordPreview = async (name, startAt, responseCountBefore, expectedId, selector = '[data-testid="preview-table-scroll"]') => {
  await page.waitForFunction(({ selector, expectedId }) => {
    const root = document.querySelector(selector);
    return Boolean(root?.querySelector('[role="table"]'))
      && [...root.querySelectorAll('button')].every(button => !button.disabled)
      && root.innerText.includes(expectedId);
  }, { selector, expectedId }, { timeout: 60000 });
  const renderedAt = Date.now();
  await refreshResponses();
  const previewResponses = report.responses.filter(response => response.path.endsWith('/preview'));
  assert(previewResponses.length > responseCountBefore, `${name}: preview UI rendered without a captured native /preview response`);
  const response = previewResponses.at(-1);
  const rendered = await domPreview(selector);
  const payload = await decodeResponseBody(response);
  const item = {
    name,
    elapsedMs: renderedAt - startAt,
    startedAt: new Date(startAt).toISOString(),
    completedAt: new Date().toISOString(),
    response: { path: response.path, status: response.status, elapsedMs: response.elapsedMs },
    rowCount: payload.rowCount,
    columns: payload.columns,
    rendered,
    firstRawRow: payload.rows?.[0],
  };
  report.renderedPreviews.push(item);
  addAssertion(`${name} returned HTTP success`, response.status >= 200 && response.status < 300, response.status);
  report.assertions.push({ name: `${name} completed under ${report.performanceGateMs} ms`, passed: item.elapsedMs < report.performanceGateMs, detail: item.elapsedMs });
  addAssertion(`${name} rendered the selected FHIR id`, rendered.text.includes(expectedId), expectedId);
  return { item, payload };
};

const waitForProposal = async (label, startedAt, responseCountBefore) => {
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready'
    && Boolean(document.querySelector('[data-testid="construction-proposal-preview"]')), null, { timeout: 60000 });
  const renderedAt = Date.now();
  await refreshResponses();
  const proposalResponses = report.responses.filter(response => response.path.endsWith('/construction-proposals'));
  assert(proposalResponses.length > responseCountBefore, `${label}: ready proposal omitted its native construction-proposals response`);
  const response = proposalResponses.at(-1);
  const state = await inspectPage(page, () => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    const root=document.querySelector('[data-testid="construction-proposal-preview"]');
    const table=root?.querySelector('[role="table"]');
    return {
      status:panel?.getAttribute('data-proposal-status'),
      panel:panel?.innerText,
      applyDisabled:document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled,
      text:root?.innerText ?? '',
      rowCount:Number(table?.getAttribute('aria-rowcount') ?? 0),
      headers:[...root?.querySelectorAll('[role="columnheader"]') ?? []].map(cell=>cell.innerText.trim()),
      rows:[...root?.querySelectorAll('[role="row"]') ?? []].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())),
    };
  });
  const item = {
    name: label,
    elapsedMs: renderedAt - startedAt,
    startedAt: new Date(startedAt).toISOString(),
    completedAt: new Date().toISOString(),
    response: { path: response.path, status: response.status, elapsedMs: response.elapsedMs },
    ...state,
  };
  report.renderedPreviews.push(item);
  addAssertion(`${label} proposal is ready`, state.status === 'ready' && !state.applyDisabled, state);
  report.assertions.push({ name: `${label} completed under ${report.performanceGateMs} ms`, passed: item.elapsedMs < report.performanceGateMs, detail: item.elapsedMs });
  return item;
};

const selectTemporaryTable = async () => {
  await page.waitForFunction(name => [...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(name)), tableName, { timeout: 60000 });
  await recordClick('Select temporary table', `button ending in ${tableName}`);
  await page.waitForFunction(name => document.body.innerText.includes(`DATASET WORKSPACE\n\n${name}`), tableName, { timeout: 30000 });
};

const captureErrorState = async () => inspectPage(page, () => ({
  alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText.trim()).filter(Boolean),
  status:[...document.querySelectorAll('[role="status"]')].map(element=>element.innerText.trim()).filter(Boolean),
  body:document.body.innerText.slice(0,3000),
}));

try {
  report.oracle = queryCdaFhirEdgeOracle();
  report.oracle.expectedObservationIds = [...new Set(report.oracle.chain.map((item) => item.observationId))].sort();
  report.oracle.expectedPatientIds = [...new Set(report.oracle.chain.map((item) => item.patientId))].sort();
  addAssertion('Arango oracle includes the specified Observation and Patient',
    report.oracle.chain.some((item) => item.observationId === knownObservationId && item.patientId === knownPatientId),
    { observationId: knownObservationId, patientId: knownPatientId, matches: report.oracle.chain.length },
  );

  requestMonitor = captureCDARequests(page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: `${explorerRoot}/${explorerId}`,
    responsePaths: /./,
    report: { nativeRequests: report.nativeRequests, errors: report.browserErrors },
  });
  page.on('response', response => {
    if (response.status() < 400 && !response.url().endsWith('/favicon.ico')) return;
    const url = new URL(response.url());
    if (![new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin)) return;
    if (url.pathname.endsWith('/favicon.ico')) report.incidentalErrors.push({ path: url.pathname, status: response.status() });
    else report.browserErrors.push({ kind: 'http', path: url.pathname, status: response.status() });
  });
  page.on('requestfailed', request => {
    const url = new URL(request.url());
    if ([new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin) && request.resourceType() === 'script') {
      report.browserErrors.push({ kind: 'module', path: url.pathname, error: sanitizeText(request.failure()?.errorText) });
    }
  });

  await page.goto(pageURL);
  await waitForBrowser(page, { kind: 'text-includes', selector: 'body', text: 'DATASET WORKSPACE' }, 30000);
  await screenshot('00-builder-start');

  const builderCall = await ownedApiRequest(`${explorerRoot}/${explorerId}/authoring/v2/builder`);
  const builder = builderCall.value;
  const selectionCall = await ownedApiRequest(`${explorerRoot}/${explorerId}/selections`, { method: 'POST',
    body: { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: `cda-indirect-route-${Date.now()}`,
      source: { kind: 'resources', resources: { refs: [{ project, generation: builder.catalog.generation, resourceType: 'Specimen', id: specimenId }] } } },
  });
  const selectionValue = selectionCall.value;
  const selection = { builderStatus: builderCall.entry.status, generation: builder.catalog.generation,
    snapshotPresent: Boolean(builder.catalog.snapshotToken), status: selectionCall.entry.status, body: selectionCall.entry.response };
  report.selection = selection;
  addAssertion('CDA selection uses the active dataset generation', selection.builderStatus === 200 && selection.generation === generation, selection);
  addAssertion('Specimen selection was created', selection.status === 201 && Boolean(selectionValue.id), selection);

  await page.goto(`${pageURL}&selection=${encodeURIComponent(selectionValue.id)}`);
  await waitForBrowser(page, { kind: 'text-includes', selector: 'body', text: 'DATASET WORKSPACE' }, 30000);
  await recordClick('Start temporary table', 'button text: New table');
  await waitForBrowser(page, { kind: 'enabled', selector: 'button[aria-label="Choose Specimen rows"]' }, 30000);
  await setInput('#first-table-name', tableName, 'Name temporary Specimen table');
  await recordClick('Create Specimen table', 'button[aria-label="Choose Specimen rows"]');
  tableCreated = true;
  await page.waitForFunction(name => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(name), tableName, { timeout: 30000 });
  await waitForBrowser(page, { kind: 'enabled', selector: '[data-testid="construction-rows-settings-trigger"]' }, 30000);
  await recordClick('Open Rows settings', 'button[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(page, { kind: 'present', selector: '[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]' }, 30000);
  await recordClick('Open starting collection choices', 'Rows settings / Starting collection summary');
  await page.getByRole('dialog', { name: 'Row definition settings' }).getByRole('button', { name: 'Use selected resources', exact: true }).waitFor({ state: 'visible', timeout: 30000 });
  const startingCollection = await inspectPage(page, () => document.querySelector('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]')?.innerText);
  addAssertion('Starting collection is bounded to the selected Specimen', startingCollection?.includes('Specimen') && /selected/i.test(startingCollection), startingCollection);
  await recordClick('Use the selected Specimen', 'Rows settings / Starting collection / Use selected resources');
  await page.waitForFunction(() => document.querySelector('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]')?.innerText.includes('constrain one row per Specimen'), null, { timeout: 30000 });
  await recordClick('Return from Rows to the table', 'Rows settings / Back to table');
  await page.getByRole('dialog', { name: 'Row definition settings' }).waitFor({ state: 'hidden', timeout: 30000 });
  await selectTemporaryTable();

  let previewResponseCount = report.responses.filter((response) => response.path.endsWith('/preview')).length;
  let previewStartedAt = Date.now();
  await clickButton('Preview', 'Preview selected Specimen baseline');
  const baselineResult = await recordPreview('Initial Specimen preview', previewStartedAt, previewResponseCount, specimenId);
  report.baseline = baselineResult.item;
  addAssertion('Initial preview contains one selected Specimen row', baselineResult.payload.rowCount === 1, baselineResult.payload.rowCount);
  await screenshot('01-specimen-baseline-preview');

  await recordClick('Open Add columns', 'button[aria-label^="Add columns:"]');
  await clickButton('Fields and related data', 'Browse fields and related data');
  await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-add-columns-source"]' }, 30000);
  const availableSources = await page.locator('[data-testid="construction-add-columns-source-option"]').evaluateAll(nodes => nodes.map(button=>({label:button.getAttribute('aria-label'),kind:button.dataset.sourceKind,key:button.dataset.sourceKey,selected:button.getAttribute('aria-pressed')==='true'})));
  report.availableSources = availableSources;
  const patientSource = availableSources.find((source) => source.kind === 'RELATED' && source.label?.startsWith('Patient,'));
  addAssertion('Related Patient source is offered for the Specimen table', Boolean(patientSource), availableSources);
  await recordClick('Choose Patient related source', patientSource.label);
  await waitForBrowser(page, { kind: 'enabled', selector: 'input[aria-label="Select Patient.id"]' }, 60000);
  await screenshot('02-patient-field-catalog');
  await recordClick('Select Patient.id field', 'input[aria-label="Select Patient.id"]');
  await recordClick('Add selected Patient.id field', 'button text: Add 1 selected feature');
  await page.waitForFunction(() => Boolean(document.querySelector('[role="dialog"]')) || document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready', null, { timeout: 60000 });
  const dialogSnapshot = await inspectPage(page, () => {
    const dialog=document.querySelector('[role="dialog"]');
    return dialog ? {
      title:dialog.querySelector('h2')?.innerText,
      text:dialog.innerText.slice(0,5000),
      routes:[...dialog.querySelectorAll('input[type="radio"][name^="construction-route-"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked})),
      forms:[...dialog.querySelectorAll('input[type="radio"][name^="construction-choice-"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked})),
      summaries:[...dialog.querySelectorAll('summary')].map(item=>item.innerText),
      buttons:[...dialog.querySelectorAll('button')].map(button=>({text:button.innerText.trim(),disabled:button.disabled})),
    } : null;
  });
  report.routeDialog = dialogSnapshot;
  addAssertion('Patient.id route chooser opened for an explicit path', Boolean(dialogSnapshot), dialogSnapshot);
  if (dialogSnapshot.summaries.some((text) => text.startsWith('Other relationship paths'))) {
    await recordClick('Expand other relationship paths', 'Other relationship paths details');
  }
  let wantedRadio = await inspectPage(page, route => [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(route))?.getAttribute('aria-label') ?? null, expectedRoute);
  while (!wantedRadio) {
    const moreRoutes = page.getByRole('dialog').getByRole('button', { name: 'Load more routes', exact: true });
    const hasMore = await moreRoutes.count() === 1 && await moreRoutes.isEnabled();
    if (!hasMore) break;
    const routeCountBefore = await page.getByRole('dialog').locator('input[type="radio"][name^="construction-route-"]').count();
    await recordClick('Load more Patient.id routes', 'button text: Load more routes');
    await page.waitForFunction(previousCount => {
      const count = document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]').length;
      const loading = [...document.querySelectorAll('[role="dialog"] button')].some(button => button.textContent?.trim() === 'Checking for more paths…');
      return count > previousCount || !loading;
    }, routeCountBefore, { timeout: 60000 });
    const otherPaths = page.getByRole('dialog').locator('summary').filter({ hasText: 'Other relationship paths' });
    if (await otherPaths.count() === 1 && !(await otherPaths.locator('..').getAttribute('open'))) await performAction(actionTracker, 'Expand other relationship paths after loading more', otherPaths, (target, options) => target.click(options));
    wantedRadio = await inspectPage(page, route => [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(route))?.getAttribute('aria-label') ?? null, expectedRoute);
  }
  addAssertion('The exact two-hop Specimen → Observation → Patient route is available', Boolean(wantedRadio), dialogSnapshot);
  const expectedMatchCount = report.oracle.chain.length;
  const routeCoverageBefore = report.responses.filter((response) => response.path.endsWith('/construction-proposals')).length;
  const routeCoverageStartedAt = Date.now();
  const routeCoverageCard = await inspectPage(page, route => {
    const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(route));
    const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');
    return {
      radioLabel:radio?.getAttribute('aria-label') ?? null,
      selected:Boolean(radio?.checked),
      testId:coverage?.getAttribute('data-testid') ?? null,
      text:coverage?.innerText?.trim() ?? '',
      loading:coverage?.querySelector('[role="status"]')?.innerText?.includes('Checking') ?? false,
      action:[...coverage?.querySelectorAll('button') ?? []].find(button=>/Check matching rows|Retry coverage check|Retry match check/i.test(button.textContent?.trim() ?? ''))?.textContent?.trim() ?? null,
    };
  }, expectedRoute);
  addAssertion('The two-hop route has a preselection coverage card',
    !routeCoverageCard.selected && Boolean(routeCoverageCard.testId), routeCoverageCard);
  let routeCoverageResponse = null;
  if (routeCoverageCard.action) {
    await recordClick('Check Specimen → Observation → Patient matches before route selection',
      `button in ${routeCoverageCard.testId}`);
  }
  await page.waitForFunction(route => {
    const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(route));
    const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');
    const text=coverage?.innerText ?? '';
    return /In 1 displayed row:|Retry coverage check|Retry match check|could not measure|unavailable/i.test(text) && !text.includes('Checking matching records');
  }, expectedRoute, { timeout: 60000 });
  const routeCoverageRenderedAt = Date.now();
  await refreshResponses();
  const routeCoverageResponses = report.responses.filter(response => response.path.endsWith('/construction-proposals'));
  if (routeCoverageCard.action || routeCoverageCard.loading) {
    assert(routeCoverageResponses.length > routeCoverageBefore, 'Visible route coverage completed without its owned construction-proposals response');
    routeCoverageResponse = routeCoverageResponses.at(-1);
  } else if (routeCoverageResponses.length > routeCoverageBefore) routeCoverageResponse = routeCoverageResponses.at(-1);
  const routeCoverageText = await inspectPage(page, route => {
    const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(route));
    const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');
    return {selected:Boolean(radio?.checked),text:coverage?.innerText?.trim() ?? ''};
  }, expectedRoute);
  const coverageBins = routeCoverageText.text.match(/In (\d+) displayed row: (\d+) with no match, (\d+) with one, (\d+) with two or more/);
  const expectedCoverageBand = expectedMatchCount === 0 ? 'zero' : expectedMatchCount === 1 ? 'one' : 'many';
  const observedCoverageBins = coverageBins ? {
    displayedRows:Number(coverageBins[1]), zero:Number(coverageBins[2]), one:Number(coverageBins[3]), many:Number(coverageBins[4]),
  } : null;
  report.preselectionRouteCoverage = {
    route: routeCoverageCard.radioLabel,
    selectedBeforeAndAfterCoverage: routeCoverageText.selected,
    text: routeCoverageText.text,
    expectedPatientIds: report.oracle.expectedPatientIds,
    expectedMatchCount,
    expectedCoverageBand,
    observedCoverageBins,
    elapsedMs: routeCoverageRenderedAt - routeCoverageStartedAt,
    response: routeCoverageResponse ? { path: routeCoverageResponse.path, status: routeCoverageResponse.status, elapsedMs: routeCoverageResponse.elapsedMs } : null,
  };
  addAssertion('Indirect route shows oracle-aligned zero/one/many counts while unselected',
    !routeCoverageText.selected && Boolean(observedCoverageBins) && observedCoverageBins.displayedRows === 1 &&
      observedCoverageBins.zero + observedCoverageBins.one + observedCoverageBins.many === 1 &&
      observedCoverageBins[expectedCoverageBand] === 1,
    report.preselectionRouteCoverage);
  addAssertion('Indirect route match count completes under five seconds',
    report.preselectionRouteCoverage.elapsedMs < report.performanceGateMs,
    report.preselectionRouteCoverage.elapsedMs);
  if (routeCoverageResponse) addAssertion('Indirect route coverage proposal returned HTTP success',
    routeCoverageResponse.status >= 200 && routeCoverageResponse.status < 300,
    report.preselectionRouteCoverage.response);
  await screenshot('02b-indirect-route-coverage-before-selection');
  await recordClick('Select Specimen → Observation → Patient route', wantedRadio);
  await page.getByRole('radio', { name: wantedRadio, exact: true }).waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(label => [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].some(input=>input.checked && input.getAttribute('aria-label')===label), wantedRadio, { timeout: 30000 });
  const choiceForm = await inspectPage(page, isCountMode => [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-choice-"]')]
    .find(input => (isCountMode ? /Count matching records/i : /Keep all matching values/i).test((input.getAttribute('aria-label') ?? '') + ' ' + (input.closest('label')?.innerText ?? '')))
    ?.getAttribute('aria-label') ?? null, countMode);
  const choiceFormLabel = countMode ? 'Count matching records' : 'Keep all matching Patient.id values';
  addAssertion(`A ${choiceFormLabel} form is available`, Boolean(choiceForm), dialogSnapshot);
  await recordClick(`Choose ${choiceFormLabel}`, choiceForm);
  const addResponseCount = report.responses.filter((response) => response.path.endsWith('/construction-proposals')).length;
  previewStartedAt = Date.now();
  await clickButton('Add 1 column', 'Confirm Patient.id route and Add column');
  const addProposal = await waitForProposal('Patient.id add proposal preview', previewStartedAt, addResponseCount);
  report.addProposal = addProposal;
  if (countMode) {
    const addProposalRawRow = addProposal.text.split(/\r?\n/).find((line) => line.includes(specimenId));
    const addProposalCount = Number(addProposalRawRow?.trim().split(/\s+/).at(-1));
    addProposal.rawCdaComparison = { expectedPatientIds: report.oracle.expectedPatientIds, expectedMatchCount, renderedRow: addProposalRawRow, observedCount: addProposalCount };
    addAssertion('Add proposal preview renders the raw-CDA Patient.id match count',
      addProposal.panel.includes('Count of related Patient records') &&
        addProposal.panel.includes(`1 of 1 rows contain a value`) &&
        addProposal.text.includes(specimenId) && addProposalCount === expectedMatchCount,
      { expectedPatientIds: report.oracle.expectedPatientIds, expectedMatchCount, panel: addProposal.panel, text: addProposal.text, renderedRow: addProposalRawRow, observedCount: addProposalCount },
    );
  } else {
    addAssertion('Add proposal preview shows the oracle Patient.id',
      addProposal.text.includes(knownPatientId) || addProposal.rows.some((row) => row.some((cell) => cell.includes(knownPatientId))),
      { expectedPatientId: knownPatientId, text: addProposal.text, rows: addProposal.rows },
    );
  }
  await screenshot('03-two-hop-route-add-proposal');
  await recordClick('Apply Patient.id related field', '[data-testid="construction-apply-proposal"]');
  await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, null, { timeout: 60000 });
  report.appliedHistory = await inspectPage(page, () => document.querySelector('[data-testid^="construction-history-step-"]')?.innerText);

  await page.goto(pageURL);
  await selectTemporaryTable();
  previewResponseCount = report.responses.filter((response) => response.path.endsWith('/preview')).length;
  previewStartedAt = Date.now();
  await clickButton('Preview', 'Preview applied Patient.id after reload');
  const appliedResult = await recordPreview(`Applied Patient.id ${countMode ? 'COUNT' : 'values'} after reload`, previewStartedAt, previewResponseCount, countMode ? String(expectedMatchCount) : knownPatientId);
  report.appliedPreview = appliedResult.item;
  const appliedHeaders = appliedResult.item.rendered.headers;
  if (countMode) {
    const patientCountHeaderIndex = appliedHeaders.findIndex((header) => /related.*patient.*count|patient.*count|count.*patient/i.test(header));
    addAssertion('Rendered output column identifies the related Patient.id count', patientCountHeaderIndex >= 0, appliedHeaders);
    const countRow = appliedResult.item.rendered.rows.find((row) => row.some((cell) => cell.includes(specimenId)));
    addAssertion('Applied Patient.id count matches the raw Arango route oracle',
      Boolean(countRow) && Number(countRow?.[patientCountHeaderIndex]) === expectedMatchCount,
      { expectedPatientIds: report.oracle.expectedPatientIds, expectedMatchCount, observedRows: appliedResult.item.rendered.rows, countRow },
    );
  } else {
    const patientIDHeaderIndex = appliedHeaders.findIndex((header) => /patient/i.test(header) && /\bids?\b/i.test(header));
    addAssertion('Rendered output column identifies Patient.id', patientIDHeaderIndex >= 0, appliedHeaders);
    const patientDOMRow = appliedResult.item.rendered.rows.find((row) => row.some((cell) => cell.includes(knownPatientId)));
    addAssertion('Visible Patient.id matches the Arango two-hop oracle', Boolean(patientDOMRow), {
      expectedPatientIds: report.oracle.expectedPatientIds,
      observedRows: appliedResult.item.rendered.rows,
    });
    addAssertion('Rendered Patient.id matches every oracle result',
      report.oracle.expectedPatientIds.every((id) => patientDOMRow?.some((cell) => cell.includes(id))),
      { expectedPatientIds: report.oracle.expectedPatientIds, row: patientDOMRow },
    );
  }
  addAssertion('Applied preview still represents one Specimen row', appliedResult.payload.rowCount === 1, appliedResult.payload.rowCount);
  await screenshot('04-applied-preview-after-reload');


  let activeOutputLabel;
  if (!countMode) {
    await recordClick('Select saved related field step for edit', '[data-testid^="construction-history-step-"]');
    await waitForBrowser(page, { kind: 'present', selector: '[data-testid^="construction-edit-step-"]' }, 30000);
    await recordClick('Edit saved Patient.id step', '[data-testid^="construction-edit-step-"]');
    await waitForBrowser(page, { kind: 'present', selector: '[data-testid="related-source-step-editor"]' }, 30000);
    const savedRouteEditor = await inspectPage(page, () => document.querySelector('[data-testid="related-source-step-editor"]')?.innerText);
    report.savedRouteEditor = savedRouteEditor;
    addAssertion('Saved editor retained the exact two-hop route',
      savedRouteEditor?.includes('Current route: Specimen → Observation via Specimen · Observation → Patient via Subject'),
      savedRouteEditor,
    );
    activeOutputLabel = `Patient.id QA edit ${Date.now()}`;
    const editProposalCount = report.responses.filter((response) => response.path.endsWith('/construction-proposals')).length;
    previewStartedAt = Date.now();
    await setInput('[aria-label="Output column label"]', activeOutputLabel, 'Edit Patient.id output label');
    const editProposal = await waitForProposal('Edited Patient.id proposal preview', previewStartedAt, editProposalCount);
    report.editProposal = editProposal;
    await screenshot('05-edit-proposal-preview');
    await recordClick('Apply Patient.id label edit', '[data-testid="construction-apply-proposal"]');
    await page.waitForFunction(() => Boolean(document.querySelector('[data-testid^="construction-history-step-"]')) && !document.querySelector('[data-testid="related-source-step-editor"]'), null, { timeout: 60000 });
    await page.goto(pageURL);
    await selectTemporaryTable();
    previewResponseCount = report.responses.filter((response) => response.path.endsWith('/preview')).length;
    previewStartedAt = Date.now();
    await clickButton('Preview', 'Preview edited Patient.id after reload');
    const editedResult = await recordPreview('Edited Patient.id table preview after reload', previewStartedAt, previewResponseCount, knownPatientId);
    report.editedPreview = editedResult.item;
    addAssertion('Edited Patient.id label survived reload', editedResult.item.rendered.headers.some((header) => header.toLowerCase() === activeOutputLabel.toLowerCase()), editedResult.item.rendered.headers);
    addAssertion('Edited Patient.id value still matches the Arango oracle',
      editedResult.item.rendered.rows.some((row) => report.oracle.expectedPatientIds.every((id) => row.some((cell) => cell.includes(id)))),
      editedResult.item.rendered.rows,
    );
    await screenshot('06-edited-preview-after-reload');
  }

  await recordClick(`Select saved ${countMode ? 'Count' : 'edited'} related field step`, '[data-testid^="construction-history-step-"]');
  await waitForBrowser(page, { kind: 'present', selector: '[data-testid^="construction-remove-step-"]' }, 30000);
  const removeProposalCount = report.responses.filter((response) => response.path.endsWith('/construction-proposals')).length;
  previewStartedAt = Date.now();
  await recordClick(`Remove temporary ${countMode ? 'Count' : 'edited Patient.id'} step`, '[data-testid^="construction-remove-step-"]');
  const removeProposal = await waitForProposal('Remove Patient.id proposal preview', previewStartedAt, removeProposalCount);
  report.removeProposal = removeProposal;
  await screenshot('07-remove-proposal-preview');
  await recordClick('Apply removal of Patient.id step', '[data-testid="construction-apply-proposal"]');
  await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, null, { timeout: 60000 });
  await page.goto(pageURL);
  await selectTemporaryTable();
  previewResponseCount = report.responses.filter((response) => response.path.endsWith('/preview')).length;
  previewStartedAt = Date.now();
  await clickButton('Preview', 'Preview restored Specimen after removal and reload');
  const restoredResult = await recordPreview('Restored Specimen preview after removal and reload', previewStartedAt, previewResponseCount, specimenId);
  report.restoredPreview = restoredResult.item;
  addAssertion('Removing the step restores original preview headers',
    JSON.stringify(restoredResult.item.rendered.headers) === JSON.stringify(report.baseline.rendered.headers),
    { baseline: report.baseline.rendered.headers, restored: restoredResult.item.rendered.headers },
  );
  addAssertion('Removing the step restores original Specimen row values',
    JSON.stringify(restoredResult.item.rendered.rows) === JSON.stringify(report.baseline.rendered.rows),
    { baseline: report.baseline.rendered.rows, restored: restoredResult.item.rendered.rows },
  );
  addAssertion('No construction step remains after restore',
    await page.locator('[data-testid^="construction-history-step-"]').count() === 0,
  );
  await screenshot('08-restored-preview-before-cleanup');
  report.finalUIState = await captureErrorState();
} catch (error) {
  scenarioError = error;
  report.error = { message: error instanceof Error ? error.message : String(error), stack: error instanceof Error ? error.stack : undefined };
  try { report.failureUIState = await captureErrorState(); } catch { /* keep primary failure */ }
  try { await screenshot('failure-state'); } catch { /* best-effort evidence */ }
} finally {
  if (tableCreated) {
    report.cleanup.attempted = true;
    try {
      await page.goto(pageURL);
      await selectTemporaryTable();
      cda.onDialog(async () => ({ accept: true }));
      await recordClick('Delete temporary table', 'button[aria-label="Delete table"]');
      await page.waitForFunction(name => ![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(name)), tableName, { timeout: 60000 });
      report.cleanup.complete = true;
      report.cleanup.responses = report.responses.filter((response) => response.path.endsWith('/commands')).slice(-1);
      await page.goto(pageURL);
      await page.waitForFunction(name => document.body.innerText.includes('DATASET WORKSPACE')
        && ![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(name)), tableName, { timeout: 60000 });
      report.cleanup.reloadedWithoutTemporaryTable = true;
      await screenshot('09-cleanup-confirmed');
    } catch (error) {
      cleanupError = error;
      report.cleanup.error = error instanceof Error ? error.message : String(error);
      report.cleanup.complete = false;
    }
  }
  {
    try { await refreshResponses(); } catch (error) { report.responseCaptureError = sanitizeText(error); }
    report.responses.push(...report.requests.map(({ requestId, path, method, status, response, startedAt, completedAt }) => ({
      requestId, path, method, status, body: response, elapsedMs: completedAt - startedAt,
    })));
    report.requests.push(...report.nativeRequests);
    report.responsesWithErrors = report.responses.filter((response) => response.status >= 400);
    if (!report.responsesWithErrors.length && report.browserErrors.length) report.responsesWithErrors = report.browserErrors;
    report.browserErrorCount = report.browserErrors.length;
  }
  report.elapsedMs = Date.now() - startedAt;
  report.clickCount = report.clicks.length;
  report.completedAt = new Date().toISOString();
  report.performanceFailures = report.assertions.filter((item) => !item.passed);
  report.outcome = scenarioError || cleanupError || report.performanceFailures.length > 0 || report.responsesWithErrors.length > 0 ? 'failed' : 'passed';
  await cda.attachReport('indirect-specimen-patient-journey.json', report);
}

if (scenarioError) throw scenarioError;
if (cleanupError) throw cleanupError;
if (!report.cleanup.complete) throw new Error('The temporary CDA table was not removed and reloaded.');
if (report.performanceFailures?.length) throw new Error(`Indirect specimen/patient performance assertions failed: ${JSON.stringify(report.performanceFailures)}`);
if (report.responsesWithErrors?.length) throw new Error(`Indirect specimen/patient requests failed: ${JSON.stringify(report.responsesWithErrors)}`);
return report;
}
