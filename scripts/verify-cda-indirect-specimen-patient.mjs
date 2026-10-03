#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import {
  browserEval,
  launchBrowser,
  navigate,
  waitForBrowser,
} from './loom-dev.mjs';

const project = 'loom_dev_cda_fhir';
const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const mode = process.argv[3] ?? 'values';
assert(['values', 'count'].includes(mode), `Unknown indirect route mode: ${mode}`);
const countMode = mode === 'count';
const specimenId = 'b7cad184-db67-5542-a975-10fffa3e89e7';
const knownObservationId = '35cfec85-56e8-5257-af99-2e9345be2011';
const knownPatientId = 'afcfb15e-7617-5691-ae2c-ab675322fb33';
const generation = 'cda-fhir-v1';
const expectedRoute = '2-relationship path: Specimen to Observation to Patient via Specimen then Subject';
const uiOrigin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorerId}&mode=builder`;
const startedAt = Date.now();
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
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
  renderedPreviews: [],
  assertions: [],
  cleanup: { attempted: false, complete: false },
};

let browser;
let tableCreated = false;
let scenarioError;
let cleanupError;
const requestStarts = new Map();

const addAssertion = (name, passed, detail) => {
  report.assertions.push({ name, passed, detail });
  assert(passed, `${name}${detail === undefined ? '' : `: ${JSON.stringify(detail)}`}`);
};

const shellQuote = (value) => `'${value.replaceAll("'", "'\\''")}'`;

function queryCdaFhirEdgeOracle() {
  const names = execFileSync('docker', ['ps', '--format', '{{.Names}}'], { encoding: 'utf8' })
    .trim().split(/\r?\n/).filter((name) => /^loom-dev-.+-arangodb-1$/.test(name));
  assert.equal(names.length, 1, `Expected one running Loom dev Arango container; found ${names.length}`);
  const container = names[0];
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
  const stdout = execFileSync('docker', ['exec', container, 'sh', '-lc', command], { encoding: 'utf8' });
  const result = JSON.parse(stdout.trim().split(/\r?\n/).at(-1));
  assert.equal(result.length, 1, 'Arango oracle did not find the requested Specimen');
  const oracle = result[0];
  assert.equal(oracle.specimenId, specimenId);
  assert(oracle.chain.some((item) => item.observationId === knownObservationId && item.patientId === knownPatientId),
    'Arango oracle did not confirm the known Specimen → Observation → Patient chain');
  assert(oracle.chain.length > 0, 'Arango oracle returned no two-hop matches');
  return { container, database: 'loom_dev', project, generation, specimenId, aql, ...oracle };
}

const recordClick = async (label, selector, clickExpression) => {
  const click = {
    sequence: report.clicks.length + 1,
    label,
    selector,
    method: 'Browser DOM click()',
    startedAt: new Date().toISOString(),
  };
  report.clicks.push(click);
  const started = Date.now();
  try {
    await browserEval(browser.cdp, `${clickExpression}return true;`);
    click.durationMs = Date.now() - started;
    click.completedAt = new Date().toISOString();
  } catch (error) {
    click.durationMs = Date.now() - started;
    click.error = error instanceof Error ? error.message : String(error);
    throw error;
  }
};

const clickSelector = (selector, label) => recordClick(
  label,
  selector,
  `const target=document.querySelector(${JSON.stringify(selector)});if(!target)throw new Error(${JSON.stringify(`Missing selector ${selector}`)});target.click();`,
);

const clickButton = (text, label) => recordClick(
  label,
  `button text: ${text}`,
  `const target=[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()===${JSON.stringify(text)});if(!target)throw new Error(${JSON.stringify(`Missing button ${text}`)});target.click();`,
);

const setInput = async (selector, value, label) => {
  const item = {
    sequence: report.clicks.length + 1,
    label,
    selector,
    method: 'Browser input/change events',
    value,
    startedAt: new Date().toISOString(),
  };
  report.clicks.push(item);
  const started = Date.now();
  try {
    await browserEval(browser.cdp,
      `const input=document.querySelector(${JSON.stringify(selector)});` +
      `if(!input)throw new Error(${JSON.stringify(`Missing input ${selector}`)});` +
      `Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(value)});` +
      `input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));return true;`,
    );
    item.durationMs = Date.now() - started;
    item.completedAt = new Date().toISOString();
  } catch (error) {
    item.durationMs = Date.now() - started;
    item.error = error instanceof Error ? error.message : String(error);
    throw error;
  }
};

const waitForResponse = async (suffix, previousCount, timeoutMs = 60000) => {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const matches = report.responses.filter((response) => response.path.endsWith(suffix));
    if (matches.length > previousCount) return matches.at(-1);
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out waiting for ${suffix} response`);
};

const screenshot = async (label) => {
  const result = await browser.cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(join(evidenceDirectory, `${label}.png`), Buffer.from(result.data, 'base64'));
};

const decodeResponseBody = async (response) => {
  const body = await browser.cdp.send('Network.getResponseBody', { requestId: response.requestId });
  return JSON.parse(body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body);
};

const domPreview = async (selector = '[data-testid="preview-table-scroll"]') => browserEval(browser.cdp, `
  const root=document.querySelector(${JSON.stringify(selector)});
  const table=root?.querySelector('[role="table"]');
  return {
    text:root?.innerText ?? '',
    rowCount:Number(table?.getAttribute('aria-rowcount') ?? 0),
    headers:[...root?.querySelectorAll('[role="columnheader"]') ?? []].map(cell=>cell.innerText.trim()),
    rows:[...root?.querySelectorAll('[role="row"]') ?? []].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())),
    busy:root?.getAttribute('aria-busy') ?? null,
  };
`);

const recordPreview = async (name, startAt, responseCountBefore, expectedId, selector = '[data-testid="preview-table-scroll"]') => {
  const response = await waitForResponse('/preview', responseCountBefore);
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector(${JSON.stringify(`${selector} [role="table"]`)})) && ` +
    `[...document.querySelectorAll(${JSON.stringify(`${selector} button`)})].every(button=>!button.disabled) && ` +
    `document.querySelector(${JSON.stringify(selector)})?.innerText.includes(${JSON.stringify(expectedId)})`,
    60000,
  );
  const rendered = await domPreview(selector);
  const payload = await decodeResponseBody(response);
  const item = {
    name,
    elapsedMs: Date.now() - startAt,
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
  const response = await waitForResponse('/construction-proposals', responseCountBefore);
  await waitForBrowser(browser.cdp,
    `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready' && ` +
    `Boolean(document.querySelector('[data-testid="construction-proposal-preview"]'))`,
    60000,
  );
  const state = await browserEval(browser.cdp, `
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
  `);
  const item = {
    name: label,
    elapsedMs: Date.now() - startedAt,
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
  await waitForBrowser(browser.cdp,
    `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`,
    60000,
  );
  await recordClick('Select temporary table', `button ending in ${tableName}`,
    `const target=[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}));if(!target)throw new Error('Temporary table button missing');target.click();`);
  await waitForBrowser(browser.cdp,
    `document.body.innerText.includes(${JSON.stringify(`DATASET WORKSPACE\n\n${tableName}`)})`,
    30000,
  );
};

const captureErrorState = async () => browserEval(browser.cdp, `return {
  alerts:[...document.querySelectorAll('[role="alert"]')].map(element=>element.innerText.trim()).filter(Boolean),
  status:[...document.querySelectorAll('[role="status"]')].map(element=>element.innerText.trim()).filter(Boolean),
  body:document.body.innerText.slice(0,3000),
};`);

try {
  report.oracle = queryCdaFhirEdgeOracle();
  report.oracle.expectedObservationIds = [...new Set(report.oracle.chain.map((item) => item.observationId))].sort();
  report.oracle.expectedPatientIds = [...new Set(report.oracle.chain.map((item) => item.patientId))].sort();
  addAssertion('Arango oracle includes the specified Observation and Patient',
    report.oracle.chain.some((item) => item.observationId === knownObservationId && item.patientId === knownPatientId),
    { observationId: knownObservationId, patientId: knownPatientId, matches: report.oracle.chain.length },
  );

  await mkdir(evidenceDirectory, { recursive: true });
  browser = await launchBrowser('/private/tmp');
  browser.cdp.on('Network.requestWillBeSent', (event) => {
    const url = new URL(event.request.url);
    if (!url.pathname.startsWith('/api/v1/')) return;
    requestStarts.set(event.requestId, Date.now());
    report.requests.push({
      requestId: event.requestId,
      method: event.request.method,
      path: url.pathname,
      timestamp: new Date().toISOString(),
    });
  });
  browser.cdp.on('Network.responseReceived', (event) => {
    const url = new URL(event.response.url);
    if (!url.pathname.startsWith('/api/v1/')) return;
    report.responses.push({
      requestId: event.requestId,
      path: url.pathname,
      status: event.response.status,
      elapsedMs: Date.now() - (requestStarts.get(event.requestId) ?? Date.now()),
      timestamp: new Date().toISOString(),
    });
  });
  browser.cdp.on('Network.loadingFailed', (event) => {
    const request = report.requests.find((item) => item.requestId === event.requestId);
    report.browserErrors.push({ kind: 'network-loading-failed', path: request?.path, error: event.errorText, canceled: event.canceled });
  });
  browser.cdp.on('Runtime.consoleAPICalled', (event) => {
    if (event.type !== 'error' && event.type !== 'assert') return;
    report.browserErrors.push({
      kind: `console-${event.type}`,
      text: event.args.map((arg) => arg.value ?? arg.description ?? '').join(' ').slice(0, 600),
      timestamp: new Date().toISOString(),
    });
  });
  browser.cdp.on('Runtime.exceptionThrown', (event) => {
    report.browserErrors.push({
      kind: 'javascript-exception',
      text: event.exceptionDetails?.text ?? 'JavaScript exception',
      url: event.exceptionDetails?.url,
      lineNumber: event.exceptionDetails?.lineNumber,
      timestamp: new Date().toISOString(),
    });
  });

  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
  await screenshot('00-builder-start');

  const selection = await browserEval(browser.cdp, `
    const base='/api/v1/projects/${project}/explorers/${explorerId}';
    const builderResponse=await fetch(base+'/authoring/v2/builder');
    const builder=await builderResponse.json();
    const response=await fetch(base+'/selections',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({
        snapshotToken:builder.catalog.snapshotToken,
        idempotencyKey:'cda-indirect-route-${Date.now()}',
        source:{kind:'resources',resources:{refs:[{
          project:'${project}',
          generation:builder.catalog.generation,
          resourceType:'Specimen',
          id:'${specimenId}',
        }]}}
      })
    });
    return {
      builderStatus:builderResponse.status,
      generation:builder.catalog.generation,
      snapshotPresent:Boolean(builder.catalog.snapshotToken),
      status:response.status,
      body:await response.json(),
    };
  `);
  report.selection = selection;
  addAssertion('CDA selection uses the active dataset generation', selection.builderStatus === 200 && selection.generation === generation, selection);
  addAssertion('Specimen selection was created', selection.status === 201 && Boolean(selection.body.id), selection);

  await navigate(browser.cdp, `${pageURL}&selection=${encodeURIComponent(selection.body.id)}`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
  await recordClick('Start temporary table', 'button text: New table',
    `const target=[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table');if(!target)throw new Error('New table button missing');target.click();`);
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector('button[aria-label="Choose Specimen rows"]:not(:disabled)'))`,
    30000,
  );
  await setInput('#first-table-name', tableName, 'Name temporary Specimen table');
  await recordClick('Create Specimen table', 'button[aria-label="Choose Specimen rows"]',
    `const target=document.querySelector('button[aria-label="Choose Specimen rows"]');if(!target)throw new Error('Choose Specimen rows missing');target.click();`);
  tableCreated = true;
  await waitForBrowser(browser.cdp,
    `document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(${JSON.stringify(tableName)})`,
    30000,
  );
  await waitForBrowser(browser.cdp,
    `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`,
    30000,
  );
  await recordClick('Open Rows settings', 'button[data-testid="construction-rows-settings-trigger"]',
    `const target=document.querySelector('button[data-testid="construction-rows-settings-trigger"]');if(!target||target.disabled)throw new Error('Rows settings trigger missing or disabled');target.click();`);
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]'))`,
    30000,
  );
  await recordClick('Open starting collection choices', 'Rows settings / Starting collection summary',
    `const summary=[...document.querySelectorAll('[role="dialog"][aria-label="Row definition settings"] summary')].find(node=>node.innerText.startsWith('Starting collection:'));if(!summary)throw new Error('Starting collection summary missing');summary.click();`);
  await waitForBrowser(browser.cdp,
    `Boolean([...document.querySelectorAll('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"] button')].find(button=>button.innerText==='Use selected resources'&&!button.disabled))`,
    30000,
  );
  const startingCollection = await browserEval(browser.cdp, `return document.querySelector('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]')?.innerText;`);
  addAssertion('Starting collection is bounded to the selected Specimen', startingCollection?.includes('Specimen') && /selected/i.test(startingCollection), startingCollection);
  await recordClick('Use the selected Specimen', 'Rows settings / Starting collection / Use selected resources',
    `const panel=document.querySelector('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]');const target=[...panel?.querySelectorAll('button') ?? []].find(button=>button.innerText==='Use selected resources');if(!target)throw new Error('Use selected resources missing');target.click();`);
  await waitForBrowser(browser.cdp,
    `document.querySelector('[role="dialog"][aria-label="Row definition settings"] [aria-label="Starting collection"]')?.innerText.includes('constrain one row per Specimen')`,
    30000,
  );
  await recordClick('Return from Rows to the table', 'Rows settings / Back to table',
    `const dialog=document.querySelector('[role="dialog"][aria-label="Row definition settings"]');const target=[...dialog?.querySelectorAll('button') ?? []].find(button=>button.textContent?.trim()==='Back to table');if(!target)throw new Error('Rows settings Back to table button missing');target.click();`);
  await waitForBrowser(browser.cdp, `!document.querySelector('[role="dialog"][aria-label="Row definition settings"]')`, 30000);
  await selectTemporaryTable();

  let previewResponseCount = report.responses.filter((response) => response.path.endsWith('/preview')).length;
  let previewStartedAt = Date.now();
  await clickButton('Preview', 'Preview selected Specimen baseline');
  const baselineResult = await recordPreview('Initial Specimen preview', previewStartedAt, previewResponseCount, specimenId);
  report.baseline = baselineResult.item;
  addAssertion('Initial preview contains one selected Specimen row', baselineResult.payload.rowCount === 1, baselineResult.payload.rowCount);
  await screenshot('01-specimen-baseline-preview');

  await recordClick('Open Add columns', 'button[aria-label^="Add columns:"]',
    `const target=document.querySelector('button[aria-label^="Add columns:"]');if(!target)throw new Error('Add columns action missing');target.click();`);
  await clickButton('Fields and related data', 'Browse fields and related data');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 30000);
  const availableSources = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')].map(button=>({label:button.getAttribute('aria-label'),kind:button.dataset.sourceKind,key:button.dataset.sourceKey,selected:button.getAttribute('aria-pressed')==='true'}));`);
  report.availableSources = availableSources;
  const patientSource = availableSources.find((source) => source.kind === 'RELATED' && source.label?.startsWith('Patient,'));
  addAssertion('Related Patient source is offered for the Specimen table', Boolean(patientSource), availableSources);
  await recordClick('Choose Patient related source', patientSource.label,
    `const target=document.querySelector('[data-source-key=${JSON.stringify(patientSource.key)}]');if(!target)throw new Error('Patient source option missing');target.click();`);
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector('input[aria-label="Select Patient.id"]:not(:disabled)'))`,
    60000,
  );
  await screenshot('02-patient-field-catalog');
  await recordClick('Select Patient.id field', 'input[aria-label="Select Patient.id"]',
    `const target=document.querySelector('input[aria-label="Select Patient.id"]');if(!target)throw new Error('Patient.id field missing');target.click();`);
  await recordClick('Add selected Patient.id field', 'button text: Add 1 selected feature',
    `const target=[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Add 1 selected feature');if(!target)throw new Error('Add 1 selected feature missing');target.click();`);
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector('[role="dialog"]')) || document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`,
    60000,
  );
  const dialogSnapshot = await browserEval(browser.cdp, `
    const dialog=document.querySelector('[role="dialog"]');
    return dialog ? {
      title:dialog.querySelector('h2')?.innerText,
      text:dialog.innerText.slice(0,5000),
      routes:[...dialog.querySelectorAll('input[type="radio"][name^="construction-route-"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked})),
      forms:[...dialog.querySelectorAll('input[type="radio"][name^="construction-choice-"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked})),
      summaries:[...dialog.querySelectorAll('summary')].map(item=>item.innerText),
      buttons:[...dialog.querySelectorAll('button')].map(button=>({text:button.innerText.trim(),disabled:button.disabled})),
    } : null;
  `);
  report.routeDialog = dialogSnapshot;
  addAssertion('Patient.id route chooser opened for an explicit path', Boolean(dialogSnapshot), dialogSnapshot);
  if (dialogSnapshot.summaries.some((text) => text.startsWith('Other relationship paths'))) {
    await recordClick('Expand other relationship paths', 'Other relationship paths details',
      `const target=[...document.querySelectorAll('[role="dialog"] summary')].find(item=>item.innerText.trim().startsWith('Other relationship paths'));if(!target)throw new Error('Other relationship paths section missing');target.click();`);
  }
  let wantedRadio = await browserEval(browser.cdp,
    `return [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(${JSON.stringify(expectedRoute)}))?.getAttribute('aria-label') ?? null;`,
  );
  while (!wantedRadio) {
    const hasMore = await browserEval(browser.cdp,
      `const button=[...document.querySelectorAll('[role="dialog"] button')].find(item=>item.textContent?.trim()==='Load more routes');return Boolean(button&&!button.disabled);`,
    );
    if (!hasMore) break;
    await recordClick('Load more Patient.id routes', 'button text: Load more routes',
      `const target=[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Load more routes');if(!target)throw new Error('Load more routes missing');target.click();`);
    await waitForBrowser(browser.cdp,
      `!([...document.querySelectorAll('[role="dialog"] button')].some(button=>button.textContent?.trim()==='Checking for more paths…'))`,
      60000,
    );
    await browserEval(browser.cdp,
      `const summary=[...document.querySelectorAll('[role="dialog"] summary')].find(item=>item.innerText.trim().startsWith('Other relationship paths'));if(summary&&!summary.parentElement.open)summary.click();return true;`,
    );
    wantedRadio = await browserEval(browser.cdp,
      `return [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(${JSON.stringify(expectedRoute)}))?.getAttribute('aria-label') ?? null;`,
    );
  }
  addAssertion('The exact two-hop Specimen → Observation → Patient route is available', Boolean(wantedRadio), dialogSnapshot);
  const expectedMatchCount = report.oracle.chain.length;
  const routeCoverageBefore = report.responses.filter((response) => response.path.endsWith('/construction-proposals')).length;
  const routeCoverageStartedAt = Date.now();
  const routeCoverageCard = await browserEval(browser.cdp, `
    const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(${JSON.stringify(expectedRoute)}));
    const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');
    return {
      radioLabel:radio?.getAttribute('aria-label') ?? null,
      selected:Boolean(radio?.checked),
      testId:coverage?.getAttribute('data-testid') ?? null,
      text:coverage?.innerText?.trim() ?? '',
      loading:coverage?.querySelector('[role="status"]')?.innerText?.includes('Checking') ?? false,
      action:[...coverage?.querySelectorAll('button') ?? []].find(button=>/Check matching rows|Retry coverage check|Retry match check/i.test(button.textContent?.trim() ?? ''))?.textContent?.trim() ?? null,
    };
  `);
  addAssertion('The two-hop route has a preselection coverage card',
    !routeCoverageCard.selected && Boolean(routeCoverageCard.testId), routeCoverageCard);
  let routeCoverageResponse = null;
  if (routeCoverageCard.action) {
    await recordClick('Check Specimen → Observation → Patient matches before route selection',
      `button in ${routeCoverageCard.testId}`,
      `const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(${JSON.stringify(expectedRoute)}));const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');const target=[...coverage?.querySelectorAll('button') ?? []].find(button=>/Check matching rows|Retry coverage check|Retry match check/i.test(button.textContent?.trim() ?? ''));if(!target)throw new Error('Indirect route coverage action missing');target.click();`);
    routeCoverageResponse = await waitForResponse('/construction-proposals', routeCoverageBefore, 60000);
  } else if (routeCoverageCard.loading) {
    routeCoverageResponse = await waitForResponse('/construction-proposals', routeCoverageBefore, 60000);
  }
  await waitForBrowser(browser.cdp,
    `(() => { const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(${JSON.stringify(expectedRoute)}));const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');const text=coverage?.innerText ?? '';return /In 1 displayed row:|Retry coverage check|Retry match check|could not measure|unavailable/i.test(text) && !text.includes('Checking matching records'); })()`,
    60000,
  );
  const routeCoverageText = await browserEval(browser.cdp, `const radio=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')?.includes(${JSON.stringify(expectedRoute)}));const coverage=radio?.closest('label')?.parentElement?.querySelector('[data-testid^="catalog-route-coverage-"]');return {selected:Boolean(radio?.checked),text:coverage?.innerText?.trim() ?? ''};`);
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
    elapsedMs: Date.now() - routeCoverageStartedAt,
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
  await recordClick('Select Specimen → Observation → Patient route', wantedRadio,
    `const target=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].find(input=>input.getAttribute('aria-label')===${JSON.stringify(wantedRadio)});if(!target)throw new Error('Specified two-hop route radio missing');target.click();`);
  await waitForBrowser(browser.cdp,
    `[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-route-"]')].some(input=>input.checked && input.getAttribute('aria-label')===${JSON.stringify(wantedRadio)})`,
    30000,
  );
  const choiceForm = await browserEval(browser.cdp,
    `return [...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-choice-"]')].find(input=>${countMode ? '/Count matching records/i' : '/Keep all matching values/i'}.test((input.getAttribute('aria-label') ?? '') + ' ' + (input.closest('label')?.innerText ?? '')))?.getAttribute('aria-label') ?? null;`,
  );
  const choiceFormLabel = countMode ? 'Count matching records' : 'Keep all matching Patient.id values';
  addAssertion(`A ${choiceFormLabel} form is available`, Boolean(choiceForm), dialogSnapshot);
  await recordClick(`Choose ${choiceFormLabel}`, choiceForm,
    `const target=[...document.querySelectorAll('[role="dialog"] input[type="radio"][name^="construction-choice-"]')].find(input=>input.getAttribute('aria-label')===${JSON.stringify(choiceForm)});if(!target)throw new Error(${JSON.stringify(`${choiceFormLabel} option missing`)});target.click();`);
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
  await recordClick('Apply Patient.id related field', '[data-testid="construction-apply-proposal"]',
    `const target=document.querySelector('[data-testid="construction-apply-proposal"]');if(!target||target.disabled)throw new Error('Apply proposal is not enabled');target.click();`);
  await waitForBrowser(browser.cdp,
    `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`,
    60000,
  );
  report.appliedHistory = await browserEval(browser.cdp, `return document.querySelector('[data-testid^="construction-history-step-"]')?.innerText;`);

  await navigate(browser.cdp, pageURL);
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
    await recordClick('Select saved related field step for edit', '[data-testid^="construction-history-step-"]',
      `const target=document.querySelector('[data-testid^="construction-history-step-"]');if(!target)throw new Error('Saved history step missing');target.click();`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 30000);
    await recordClick('Edit saved Patient.id step', '[data-testid^="construction-edit-step-"]',
      `const target=document.querySelector('[data-testid^="construction-edit-step-"]');if(!target)throw new Error('Edit step button missing');target.click();`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="related-source-step-editor"]'))`, 30000);
    const savedRouteEditor = await browserEval(browser.cdp, `return document.querySelector('[data-testid="related-source-step-editor"]')?.innerText;`);
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
    await recordClick('Apply Patient.id label edit', '[data-testid="construction-apply-proposal"]',
      `const target=document.querySelector('[data-testid="construction-apply-proposal"]');if(!target||target.disabled)throw new Error('Apply edit proposal is not enabled');target.click();`);
    await waitForBrowser(browser.cdp,
      `Boolean(document.querySelector('[data-testid^="construction-history-step-"]')) && !document.querySelector('[data-testid="related-source-step-editor"]')`,
      60000,
    );
    await navigate(browser.cdp, pageURL);
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

  await recordClick(`Select saved ${countMode ? 'Count' : 'edited'} related field step`, '[data-testid^="construction-history-step-"]',
    `const target=document.querySelector('[data-testid^="construction-history-step-"]');if(!target)throw new Error('Saved history step missing');target.click();`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 30000);
  const removeProposalCount = report.responses.filter((response) => response.path.endsWith('/construction-proposals')).length;
  previewStartedAt = Date.now();
  await recordClick(`Remove temporary ${countMode ? 'Count' : 'edited Patient.id'} step`, '[data-testid^="construction-remove-step-"]',
    `const target=document.querySelector('[data-testid^="construction-remove-step-"]');if(!target||target.disabled)throw new Error('Remove step button missing or disabled');target.click();`);
  const removeProposal = await waitForProposal('Remove Patient.id proposal preview', previewStartedAt, removeProposalCount);
  report.removeProposal = removeProposal;
  await screenshot('07-remove-proposal-preview');
  await recordClick('Apply removal of Patient.id step', '[data-testid="construction-apply-proposal"]',
    `const target=document.querySelector('[data-testid="construction-apply-proposal"]');if(!target||target.disabled)throw new Error('Apply removal proposal is not enabled');target.click();`);
  await waitForBrowser(browser.cdp,
    `document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`,
    60000,
  );
  await navigate(browser.cdp, pageURL);
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
    await browserEval(browser.cdp, `return document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0;`),
  );
  await screenshot('08-restored-preview-before-cleanup');
  report.finalUIState = await captureErrorState();
} catch (error) {
  scenarioError = error;
  report.error = { message: error instanceof Error ? error.message : String(error), stack: error instanceof Error ? error.stack : undefined };
  if (browser) {
    try { report.failureUIState = await captureErrorState(); } catch { /* keep primary failure */ }
    try { await screenshot('failure-state'); } catch { /* best-effort evidence */ }
  }
} finally {
  if (browser && tableCreated) {
    report.cleanup.attempted = true;
    try {
      await navigate(browser.cdp, pageURL);
      await selectTemporaryTable();
      await browserEval(browser.cdp, `window.confirm=()=>true;return true;`);
      await recordClick('Delete temporary table', 'button[aria-label="Delete table"]',
        `const target=document.querySelector('button[aria-label="Delete table"]');if(!target)throw new Error('Delete table control missing');target.click();`);
      await waitForBrowser(browser.cdp,
        `![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`,
        60000,
      );
      report.cleanup.complete = true;
      report.cleanup.responses = report.responses.filter((response) => response.path.endsWith('/commands')).slice(-1);
      await navigate(browser.cdp, pageURL);
      await waitForBrowser(browser.cdp,
        `document.body.innerText.includes('DATASET WORKSPACE') && ![...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`,
        60000,
      );
      report.cleanup.reloadedWithoutTemporaryTable = true;
      await screenshot('09-cleanup-confirmed');
    } catch (error) {
      cleanupError = error;
      report.cleanup.error = error instanceof Error ? error.message : String(error);
      report.cleanup.complete = false;
    }
  }
  if (browser) {
    report.responsesWithErrors = report.responses.filter((response) => response.status >= 400);
    report.browserErrorCount = report.browserErrors.length;
    try { await browser.close(); } catch (error) {
      report.browserCloseError = error instanceof Error ? error.message : String(error);
    }
  }
  report.elapsedMs = Date.now() - startedAt;
  report.clickCount = report.clicks.length;
  report.completedAt = new Date().toISOString();
  report.performanceFailures = report.assertions.filter((item) => !item.passed);
  report.outcome = scenarioError || cleanupError || report.performanceFailures.length > 0 || report.responsesWithErrors.length > 0 ? 'failed' : 'passed';
  try {
    await mkdir(evidenceDirectory, { recursive: true });
    await writeFile(join(evidenceDirectory, 'indirect-specimen-patient-journey.json'), JSON.stringify(report, null, 2));
  } catch (error) {
    console.error(`Could not save evidence: ${error instanceof Error ? error.message : String(error)}`);
  }
}

console.log(JSON.stringify({
  outcome: report.outcome,
  evidenceDirectory,
  elapsedMs: report.elapsedMs,
  temporaryTable: tableName,
  cleanup: report.cleanup,
  oracleChain: report.oracle?.chain,
  clickCount: report.clickCount,
  mode,
  preselectionRouteCoverage: report.preselectionRouteCoverage,
  previewTimingsMs: report.renderedPreviews.map((preview) => ({ name: preview.name, elapsedMs: preview.elapsedMs })),
  responsesWithErrors: report.responsesWithErrors,
  browserErrors: report.browserErrors,
  error: report.error,
}, null, 2));

if (scenarioError || cleanupError || !report.cleanup.complete || report.performanceFailures?.length || report.responsesWithErrors?.length) {
  process.exitCode = 1;
}
