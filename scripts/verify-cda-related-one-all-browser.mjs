import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

// Adapted from verify-cda-group-related-values-browser.mjs and
// verify-cda-coded-field-lifecycle-browser.mjs. The raw CDA oracle is kept
// separate from Explorer previews and bounds independent witnesses.
const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `related-one-all-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-related-one-all-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = {
  explorer, project, generation, protectedExplorerUntouched: true,
  scenario: 'Patient-grouped rows from one selected Specimen per witness, with related Observation ID values; a many-Observation witness must reproduce grouped-row ONE rejection and same-chooser ALL repair. Zero/one Observation witnesses are additional coverage when available.',
  started: new Date().toISOString(), cases: [], apiCalls: [], errors: [], nativeRequests: [],
};
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };

let browser;
let builder;
let outputId;
let verificationPhase = 'raw-oracle';
const nativeByRequestId = new Map();
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-one-all-${randomUUID()}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.apiCalls.push({ path, status: response.status, body, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1',
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1000)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};
const command = async (commands) => {
  await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(`${base}/builder`);
  return builder;
};
const doc = (state = builder) => state.workspace.documents.find((document) => document.output.id === outputId);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const startNativeCapture = () => {
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request }) => {
    const url = new URL(request.url);
    if (url.pathname.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
    if (!url.pathname.includes(`/explorers/${explorer}/authoring/v2/`)) return;
    let body;
    try { body = request.postData ? JSON.parse(request.postData) : undefined; } catch { body = request.postData; }
    const entry = {
      requestId,
      method: request.method,
      url: request.url,
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      request: body,
      startedAt: Date.now(),
      status: undefined,
      response: undefined,
      complete: false,
    };
    nativeByRequestId.set(requestId, entry);
    report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = nativeByRequestId.get(requestId);
    if (entry) entry.status = response.status;
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = nativeByRequestId.get(requestId);
    if (!entry) return;
    void browser.cdp.send('Network.getResponseBody', { requestId }).then((body) => {
      const raw = body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body;
      try { entry.response = JSON.parse(raw); } catch { entry.response = raw; }
    }).catch((error) => { entry.bodyError = String(error); }).finally(() => {
      entry.completedAt = Date.now();
      entry.complete = true;
    });
  });
  browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
    report.errors.push({ kind: 'runtime', message: exceptionDetails.exception?.description ?? exceptionDetails.text });
  });
  browser.cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (type === 'error') report.errors.push({ kind: 'console', message: args.map((arg) => arg.value ?? arg.description ?? '').join(' ').slice(0, 400) });
  });
  browser.cdp.on('Network.loadingFailed', ({ type, errorText }) => {
    if (type === 'Script' && errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', message: errorText });
  });
};
const waitNative = async (predicate, fromIndex = 0, timeoutMs = 5000) => {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const match = report.nativeRequests.slice(fromIndex).find((entry) => entry.complete && predicate(entry));
    if (match) return match;
    await pause(50);
  }
  throw new Error(`Timed out waiting for native request: ${JSON.stringify(report.nativeRequests.slice(fromIndex).map(({ method, url, path, query, status, request }) => ({ method, url, path, query, status, request })))}`);
};
const nativeRequestValue = (entry, key) => entry.request?.[key] ?? entry.query?.[key];
const record = (name, started, details = {}) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  report.cases.push({ name, durationMs, ...details });
};
const rendered = async (expectedRows) => {
  const columnCount = expectedRows[0]?.length ?? 2;
  await waitForBrowser(browser.cdp, `(() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return table?.getAttribute('aria-colcount')===${JSON.stringify(String(columnCount))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:');})()`, 5000);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length);`);
  assert(rows.length > 0 || expectedRows.length === 0);
  for (const row of rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `Visible row is not in the independent CDA witness: ${JSON.stringify(row)}`);
};
const openTable = async (expectedRows, name) => {
  const started = Date.now();
  const fromIndex = report.nativeRequests.length;
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`, 5000);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await rendered(expectedRows);
  const preview = await waitNative((entry) => entry.path.endsWith('/preview') && entry.request?.outputId === outputId, fromIndex);
  assert(preview.response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: preview.response.receiptId, rowCount: preview.response.rowCount });
  return preview.response;
};
const proposal = async (name, started, expectedRows) => {
  const deadline = started + 5000;
  let adopted;
  while (!adopted) {
    const proposalID = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId;`);
    adopted = report.nativeRequests.findLast((entry) => entry.startedAt >= started && entry.complete &&
      entry.path.endsWith('/construction-proposals') && entry.response?.proposalId === proposalID);
    if (adopted) break;
    assert(Date.now() < deadline, `${name} did not adopt a fresh native construction proposal within five seconds`);
    await pause(50);
  }
  await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`, 5000);
  const value = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {proposalId:panel?.dataset.proposalId,status:panel?.dataset.proposalStatus,text:panel?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
  assert.equal(value.status, 'ready', `${name}: ${value.text}`);
  assert.equal(value.rows.length, Math.min(25, expectedRows.length));
  for (const row of value.rows) assert(expectedRows.some((expected) => JSON.stringify(expected) === JSON.stringify(row)), `${name} differs from the raw CDA witness: ${JSON.stringify(row)}`);
  record(name, started, { proposalId: adopted.response.proposalId, rows: value.rows });
  return value;
};
const applyProposal = async (expectedRows, name) => {
  const started = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  await rendered(expectedRows);
  record(name, started);
  builder = await api(`${base}/builder`);
};
const waitRelatedProposal = async (name, started, expectedRequestPolicy, fromIndex) => {
  const entry = await waitNative((candidate) => candidate.path.endsWith('/construction-choice-proposals') &&
    candidate.request?.constructionChoices?.length === 1 &&
    candidate.request.constructionChoices[0].rowValuePolicy === expectedRequestPolicy, fromIndex);
  record(name, started, { status: entry.status, responseStatus: entry.response?.previewStatus, previewDurationMs: entry.response?.previewDurationMs });
  return entry;
};
const waitAdoptedChoicePreview = async (entry, name) => {
  const receiptId = entry.response?.preview?.receiptId;
  assert(receiptId, `${name} response has no preview receipt to adopt`);
  await waitForBrowser(browser.cdp, `(() => {const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');const preview=document.querySelector('[data-testid="construction-preview"]');return panel?.dataset.proposalStatus==='ready'&&preview?.dataset.previewStatus==='ready'&&preview?.dataset.previewReceiptId===${JSON.stringify(receiptId)}&&preview?.dataset.previewProposalId===${JSON.stringify(receiptId)};})()`, 5000);
  return receiptId;
};
const expand = async (hop, witnesses, expectedRows) => {
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false`, 5000);
  await click(browser.cdp, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForBrowser(browser.cdp, `document.querySelector('${panel} select[aria-label="Related record type"]')?.disabled===false`, 5000);
  const started = Date.now();
  await selectOption(browser.cdp, `${panel} select[aria-label="Related record type"]`, hop.to);
  const label = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`${panel} input[aria-label="${label}"]`)}))`, 5000);
  const proposalFromIndex = report.nativeRequests.length;
  const proposalStarted = Date.now();
  await click(browser.cdp, `${panel} input[aria-label="${label}"]`);
  const value = await proposal(`expand-${hop.from}-${hop.to}-preview`, proposalStarted, expectedRows);
  assert(report.nativeRequests.slice(proposalFromIndex).some((entry) => entry.response?.proposalId === value.proposalId), 'The displayed expansion preview must match its captured native proposal');
  report.cases.at(-1).witnessCount = witnesses.length;
  await applyProposal(expectedRows, `expand-${hop.from}-${hop.to}-apply-to-render`);
};
const openRelatedFieldChooser = async () => {
  const alreadyOpen = await browserEval(browser.cdp, `return Boolean(document.querySelector('[aria-label="Add columns editor"]'));`);
  if (!alreadyOpen) {
    await click(browser.cdp, '[data-testid="construction-action-add-columns"]');
    await click(browser.cdp, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  }
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 5000);
  const relatedResourcesOpen = await browserEval(browser.cdp, `return document.querySelector('[aria-label="Related resources"]')?.open===true;`);
  if (!relatedResourcesOpen) {
    await click(browser.cdp, '[aria-label="Related resources"] summary');
    await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Related resources"]')?.open===true`, 5000);
  }
  await click(browser.cdp, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  const rawFieldsOpen = await browserEval(browser.cdp, `return document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open===true;`);
  if (!rawFieldsOpen) {
    await click(browser.cdp, '[data-testid="feature-catalog-raw-fields"] summary');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open===true`, 5000);
  }
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)'))`, 5000);
  await click(browser.cdp, 'input[aria-label="Select Observation.id"]');
  await click(browser.cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 5000);
  await click(browser.cdp, '[role="dialog"] summary', { includes: 'Other relationship paths' });
  await click(browser.cdp, '[role="dialog"] input[aria-label="Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation"]');
  await click(browser.cdp, '[role="dialog"] input[aria-label="Observation ID: Keep all matching values"]');
  const control = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');return {dialog:Boolean(dialog),policyOptions:policy?[...policy.options].map(option=>({value:option.value,label:option.textContent})):[],policy:policy?.value};`);
  assert(control.dialog, 'Related field ONE/ALL chooser is not open');
  assert.deepEqual(control.policyOptions.map((option) => option.value), ['ALL', 'ONE'], 'The current Add columns chooser does not expose grouped-row ONE and ALL');
  report.groupedRowPolicyControl = control;
};
const selectRelatedPolicy = async (policy) => {
  await selectOption(browser.cdp, '[role="dialog"] select[aria-label="Values per grouped row"]', policy);
  const state = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');return {policy:dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value,routeChecked:dialog?.querySelector('input[aria-label="Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation"]')?.checked,formChecked:dialog?.querySelector('input[aria-label="Observation ID: Keep all matching values"]')?.checked};`);
  assert.equal(state.policy, policy);
  assert.equal(state.routeChecked, true, 'The exact selected Observation relationship path was lost');
  assert.equal(state.formChecked, true, 'Per-record matching Observation IDs must remain ALL');
  return state;
};
const clickAddRelated = async () => {
  await click(browser.cdp, '[role="dialog"] button', { name: 'Add 1 column' });
};
const readPreviewTable = async (expectedRows, expectedColumns, name) => {
  const started = Date.now();
  const nativeFrom = report.nativeRequests.length;
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`, 5000);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `(() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&table.getAttribute('aria-colcount')===${JSON.stringify(String(expectedColumns))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'));})()`, 5000);
  const dom = await browserEval(browser.cdp, `const area=document.querySelector('[data-testid="preview-table-scroll"]');const table=area?.querySelector('[role="table"]');return {headers:[...area.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...area.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length),columnCount:table?.getAttribute('aria-colcount')};`);
  assert(dom.rows.length > 0 || expectedRows.length === 0, `${name} did not render a witness row: ${JSON.stringify(dom)}`);
  for (const row of dom.rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `${name} visible rows differ from the raw witnesses: ${JSON.stringify(row)}`);
  const preview = await waitNative((entry) => entry.path.endsWith('/preview') && entry.request?.outputId === outputId, nativeFrom);
  assert(preview.response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: preview.response.receiptId, headers: dom.headers, nativeRowCount: preview.response.rowCount, mountedRowCount: dom.rows.length });
  return preview.response;
};

try {
  assert.notEqual(explorer, protectedExplorer, 'Only a fresh QA Explorer may be used');
  const patientWitnessLimit = 2000;
  const specimensPerPatient = 1;
  const observationWitnessLimit = 11;
  const categories = [
    { name: 'zero', predicate: 'LENGTH(observations) == 0' },
    { name: 'one', predicate: 'LENGTH(observations) == 1' },
    { name: 'many', predicate: 'LENGTH(observations) >= 2 AND LENGTH(observations) <= 10' },
  ];
  const witnessQueries = {};
  const witnessSeeds = [];
  report.oracle = {
    kind: 'bounded current-generation raw fhir_edge witness finder plus a separate exact-membership join over selected Specimen keys',
    searchBounds: {
      patientCandidates: { project, generation, sortedBy: 'id', limit: patientWitnessLimit },
      selectedSpecimensPerPatient: specimensPerPatient,
      distinctObservationsPerCandidate: { deduplicateBy: '_id', cap: observationWitnessLimit, manyMaximum: 10, capSemantics: '11 distinct documents is a sentinel for more than 10 and is excluded; selected zero/one/many witnesses therefore have at most 10 and are reread exactly.' },
      missingWitnessMeaning: 'A missing category is bounded fixture unavailability, not proof that no such witness exists elsewhere in the project or generation.',
    },
    witnessQueries, witnessSeeds: [], missingWitnessCategories: [],
  };
  for (const category of categories) {
    const query = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)} AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
    SORT scopedPatient.id
    LIMIT ${patientWitnessLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET specimens = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Specimen/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT specimenKey = e._from
      LET s = DOCUMENT(specimenKey)
      FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
        AND s.payload.subject.reference == CONCAT("Patient/", p.id)
      SORT s.id
      LIMIT ${specimensPerPatient}
      RETURN { id: s.id, _id: s._id, patientReference: s.payload.subject.reference }
  )
  FILTER LENGTH(specimens) == ${specimensPerPatient}
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      SORT o.id
      LIMIT ${observationWitnessLimit}
      RETURN { id: o.id, _id: o._id }
  )
  FILTER ${category.predicate}
  SORT p.id
  LIMIT 1
  RETURN {
    patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
    specimens, observations: (FOR o IN observations SORT o.id RETURN o)
  }
`;
    witnessQueries[category.name] = query;
    const [seed] = rawQuery(query);
    if (!seed) {
      const boundedAbsence = `No ${category.name}-Observation witness with at least one linked Specimen was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates. The query selects one Specimen per Patient for the exact membership reread. This is bounded fixture absence, not proof that no such witness exists elsewhere in the project or generation.`;
      report.oracle.boundedAbsence = [...(report.oracle.boundedAbsence ?? []), {
        category: category.name, patientCandidateLimit: patientWitnessLimit,
        selectedSpecimensPerPatient: specimensPerPatient, meaning: boundedAbsence,
      }];
      report.oracle.missingWitnessCategories.push({ category: category.name, patientCandidateLimit: patientWitnessLimit,
        selectedSpecimensPerPatient: specimensPerPatient, meaning: boundedAbsence });
      continue;
    }
    const witnessSeed = { category: category.name, ...seed };
    witnessSeeds.push(witnessSeed);
    report.oracle.witnessSeeds.push(witnessSeed);
  }
  const manySeed = witnessSeeds.find((seed) => seed.category === 'many');
  report.oracle.fixtureAvailability = {
    requiredForRepair: ['many'], requiredManyWitnessAvailable: Boolean(manySeed),
    availableCategories: witnessSeeds.map((seed) => seed.category),
    missingCategories: report.oracle.missingWitnessCategories.map(({ category }) => category),
    completeZeroOneManyCoverage: report.oracle.missingWitnessCategories.length === 0,
  };
  if (!manySeed) {
    throw new Error(`No many-Observation witness with at least one linked Specimen was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates; the ONE-to-ALL repair cannot be exercised. The witness query selects one Specimen per Patient.`);
  }
  assert.equal(new Set(witnessSeeds.map((seed) => seed.patient.id)).size, witnessSeeds.length,
    'Every available zero/one/many category must use an independent Patient witness');

  const selectedKeys = witnessSeeds.flatMap((seed) => seed.specimens.map((specimen) => specimen._id)).sort();
  const exactMembershipQuery = `
LET selectedKeys = ${JSON.stringify(selectedKeys)}
FOR s IN Specimen
  FILTER s._id IN selectedKeys AND s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
  LET patientEdge = FIRST(
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)}
        AND e.dataset_generation == ${JSON.stringify(generation)}
      RETURN e
  )
  FILTER patientEdge != null
  LET p = DOCUMENT(patientEdge._to)
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      LET o = DOCUMENT(e._from)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      RETURN DISTINCT { id: o.id, _id: o._id }
  )
  RETURN {
    id: s.id, _id: s._id, patientReference: s.payload.subject.reference,
    patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
    observationIds: (FOR o IN observations SORT o.id RETURN o.id),
    observationKeys: (FOR o IN observations SORT o.id RETURN o._id)
  }
`;
  const exactMembers = rawQuery(exactMembershipQuery);
  assert.deepEqual(exactMembers.map((member) => member._id).sort(), selectedKeys,
    `The independent exact-membership oracle did not resolve exactly the ${selectedKeys.length} selected Specimens`);
  const witnesses = witnessSeeds.map((seed) => {
    const members = exactMembers.filter((member) => member.patient.id === seed.patient.id).sort((a, b) => a.id.localeCompare(b.id));
    assert.equal(members.length, specimensPerPatient);
    assert(members.every((member) => member.patientReference === seed.patient.reference));
    const observationIds = [...new Set(members.flatMap((member) => member.observationIds))].sort();
    assert.deepEqual(observationIds, seed.observations.map((observation) => observation.id).sort(), `${seed.category} finder and exact-membership joins disagree`);
    const expectedCount = seed.category === 'zero' ? 0 : seed.category === 'one' ? 1 : observationIds.length;
    assert.equal(observationIds.length, expectedCount, `${seed.category} witness cardinality changed`);
    return {
      category: seed.category, patient: seed.patient, members,
      observationIds, observationCount: observationIds.length,
      expectedContributorRows: members.length * Math.max(1, observationIds.length),
    };
  });
  const manyWitness = witnesses.find((witness) => witness.category === 'many');
  assert(manyWitness && manyWitness.observationIds.length > 1, 'The independent many witness must predict an actual ONE disagreement');
  const selectedMembers = witnesses.flatMap((witness) => witness.members);
  Object.assign(report.oracle, {
    exactMembershipQuery, exactMembershipScope: { selectedSpecimenCount: selectedKeys.length,
      selectedSpecimensPerPatient: specimensPerPatient,
      maximumExpectedDistinctObservationsPerPatient: 10, observationSetsReadCompletelyForSelectedAtMostTenWitnesses: true },
    witnesses: witnesses.map(({ category, patient, members, observationIds, observationCount, expectedContributorRows }) => ({
      category, patient, observationCount, observationIds,
      members: members.map(({ id, _id, patientReference }) => ({ id, _id, patientReference })), expectedContributorRows,
    })),
    selectedSpecimenIds: selectedMembers.map((member) => member.id),
  });

  verificationPhase = 'builder';
  await api(root, { name: explorer, title: 'Related ID ONE to ALL QA' });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation);
  const rootNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen' && node.rowRootEligible);
  assert(rootNode, 'Current catalog has no authorized Specimen row root');
  const created = await command([{ type: 'CREATE_TABLE', title: 'Related ID ONE to ALL QA', rootNodeId: rootNode.nodeId }]);
  assert.equal(created.workspace.documents.length, 1);
  outputId = created.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find((candidate) => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'The current Specimen catalog must expose its id field');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(`${base.replace('/authoring/v2', '')}/selections`, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: selectedMembers.map((member) => ({ project, generation, resourceType: 'Specimen', id: member.id })) } },
  });
  assert.equal(selection.memberCount, selectedMembers.length, 'Population selection must preserve the exact independent witness membership');
  const routes = await api(`${base}/population-routes`, { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'Exact selected Specimen members have no direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  report.ownedWorkspace = { explorer, outputId, selectionRevisionId: selection.id, memberCount: selection.memberCount };
  const sourceRows = selectedMembers.map((member) => [member.id]);
  const groupedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows)]).sort((a, b) => a[0].localeCompare(b[0]));
  const relatedRows = witnesses.flatMap((witness) => witness.members.flatMap((member) => witness.observationIds.length
    ? witness.observationIds.map((observationId) => [member.id, member.patient.id, observationId])
    : [[member.id, member.patient.id, '—']])).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));

  browser = await launchBrowser(evidence);
  startNativeCapture();
  await openTable(sourceRows, 'reload-exact-available-cardinality-source-members');
  const sourceBaseline = await api(`${base}/builder`);
  assert.equal(doc(sourceBaseline).population.selectionRevisionId, selection.id);
  assert.deepEqual(doc(sourceBaseline).columns.map((column) => column.label), ['Specimen ID']);

  let pipelineRows = sourceRows;
  const chain = [
    { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
    { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' },
  ];
  for (let index = 0; index < chain.length; index += 1) {
    const hop = chain[index];
    const expectedRows = index === 0
      ? selectedMembers.map((member) => [member.id, member.patient.id])
      : relatedRows;
    const witnessesForHop = index === 0 ? selectedMembers : witnesses;
    await expand(hop, witnessesForHop, expectedRows);
    pipelineRows = expectedRows;
  }
  assert.equal(pipelineRows.length, relatedRows.length);
  const expandedDoc = doc();
  const populationBeforeGroup = structuredClone(expandedDoc.population);
  const patientExpansion = expandedDoc.construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND' && step.operation.relatedExpand.targetResourceType === 'Patient');
  const patientGroupOutput = patientExpansion?.outputs.find((output) => output.label === 'Patient FHIR resource ID');
  assert(patientGroupOutput, 'The first related expansion must retain the exact Patient ID group key');
  const groupKeyLabel = patientGroupOutput.label;
  const configureGroup = async () => {
    await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled===false`, 5000);
    await click(browser.cdp, '[data-testid="construction-action-group-rows"]');
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Group by ${groupKeyLabel}"]:not(:disabled)`)}))`, 5000);
    const started = Date.now();
    await click(browser.cdp, `input[aria-label=${JSON.stringify(`Group by ${groupKeyLabel}`)}]`);
    return started;
  };
  let groupStarted = await configureGroup();
  await proposal('group-available-patient-witnesses-preview-cancel-target', groupStarted, groupedRows);
  const beforeGroupCancel = await api(`${base}/builder`);
  const cancelGroupStarted = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  assert.deepEqual((await api(`${base}/builder`)).workspace, beforeGroupCancel.workspace, 'Cancel must preserve the exact expanded source workspace');
  record('cancel-group-proposal-preserves-source-bindings', cancelGroupStarted);
  await openTable(relatedRows, 'reload-expanded-source-after-group-cancel');

  groupStarted = await configureGroup();
  await proposal('group-available-patient-witnesses-preview', groupStarted, groupedRows);
  await applyProposal(groupedRows, 'group-available-patient-witnesses-apply-to-render');
  const groupedWorkspace = structuredClone(builder.workspace);
  const groupedBaseline = doc();
  assert.deepEqual(groupedBaseline.population, populationBeforeGroup, 'Grouping must preserve exact selected member scope');
  const groupStepBefore = groupedBaseline.construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert(groupStepBefore, 'The Group operation was not saved');
  const savedGroupKeyOutput = groupStepBefore.outputs.find((output) => output.label === groupKeyLabel);
  assert(savedGroupKeyOutput, 'The saved Group output lost its Patient resource key');
  const groupKeyName = savedGroupKeyOutput.name;
  await openTable(groupedRows, 'reload-independent-grouped-witnesses');

  await openRelatedFieldChooser();
  const beforeOne = await api(`${base}/builder`);
  assert.deepEqual(doc(beforeOne).construction, groupedBaseline.construction);
  assert.deepEqual(doc(beforeOne).population, groupedBaseline.population);
  await selectRelatedPolicy('ONE');
  const oneStarted = Date.now();
  const oneFromIndex = report.nativeRequests.length;
  await clickAddRelated();
  const oneFailure = await waitRelatedProposal('related-observation-id-one-disagreement', oneStarted, 'ONE', oneFromIndex);
  assert.equal(oneFailure.status, 422, JSON.stringify(oneFailure));
  const oneError = oneFailure.response?.error?.code ?? oneFailure.response?.code;
  assert.equal(oneError, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', JSON.stringify(oneFailure.response));
  assert.equal(oneFailure.request.constructionChoices[0].form, 'ALL', 'Related Observation IDs must remain ALL within each contributing record');
  assert.equal(oneFailure.request.constructionChoices[0].rowValuePolicy, 'ONE', 'The rejected policy must apply to grouped rows');
  assert.equal(oneFailure.request.constructionChoices[0].title, 'Observation ID');
  const rejectedChoiceId = oneFailure.request.constructionChoices[0].choiceId;
  assert(rejectedChoiceId, 'The ONE attempt must carry the selected signed related-field choice');
  assert(manyWitness.observationIds.length > 1, 'The raw many witness must independently predict the ONE conflict');
  const afterOne = await api(`${base}/builder`);
  assert.deepEqual(afterOne.workspace, beforeOne.workspace, 'Rejected ONE preflight must not mutate the selected membership, source bindings, route, or Group');
  const retainedChooser = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');return {open:Boolean(dialog),policy:policy?.value,routeChecked:dialog?.querySelector('input[aria-label="Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation"]')?.checked,formChecked:dialog?.querySelector('input[aria-label="Observation ID: Keep all matching values"]')?.checked,addEnabled:[...(dialog?.querySelectorAll('button')??[])].some(button=>button.textContent.trim()==='Add 1 column'&&!button.disabled)};`);
  assert.deepEqual(retainedChooser, { open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true }, 'ONE rejection must retain the exact route, source form, and editable chooser');
  report.oneRejection = { status: oneFailure.status, errorCode: oneError, choiceId: rejectedChoiceId, manyObservationIds: manyWitness.observationIds };

  await selectRelatedPolicy('ALL');
  const allRepairStarted = Date.now();
  const allRepairFromIndex = report.nativeRequests.length;
  await clickAddRelated();
  const firstAll = await waitRelatedProposal('same-chooser-all-repair-preview', allRepairStarted, 'ALL', allRepairFromIndex);
  assert.equal(firstAll.status, 200, JSON.stringify(firstAll.response));
  assert.equal(firstAll.response.previewStatus, 'READY', JSON.stringify(firstAll.response));
  assert(firstAll.response.previewDurationMs <= 5000, `ALL preview took ${firstAll.response.previewDurationMs} ms`);
  const firstAllReceiptId = await waitAdoptedChoicePreview(firstAll, 'Same-chooser ALL repair');
  assert.equal(firstAll.request.constructionChoices[0].form, 'ALL');
  assert.equal(firstAll.request.constructionChoices[0].rowValuePolicy, 'ALL');
  assert.equal(firstAll.request.constructionChoices[0].choiceId, rejectedChoiceId, 'Direct ALL repair must reuse the rejected chooser route choice');
  report.directAllRepair = { choiceId: firstAll.request.constructionChoices[0].choiceId, policy: firstAll.request.constructionChoices[0].rowValuePolicy, receiptId: firstAllReceiptId };
  const previewColumnId = firstAll.response.candidateColumnIds?.[0];
  assert(previewColumnId, 'Native ALL preview did not propose the related ID column');
  const proposedValues = firstAll.response.preview.rows.map((row) => ({ patientReference: row[groupKeyName], values: row[previewColumnId] }));
  assert.equal(proposedValues.length, witnesses.length);
  for (const witness of witnesses) {
    const proposed = proposedValues.find((row) => row.patientReference === witness.patient.id);
    assert(proposed, `Native preview omitted ${witness.category} witness ${witness.patient.reference}`);
    assert.deepEqual(proposed.values, witness.observationIds, `${witness.category} ALL output differs from the independent raw CDA oracle`);
  }
  const cancelAllStarted = Date.now();
  await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')`, 5000);
  await rendered(groupedRows);
  assert.deepEqual((await api(`${base}/builder`)).workspace, groupedWorkspace, 'Canceling the successful ALL preview must preserve the exact grouped workspace');
  record('cancel-related-all-proposal-preserves-group', cancelAllStarted);

  await openRelatedFieldChooser();
  await selectRelatedPolicy('ALL');
  const allApplyStarted = Date.now();
  const allApplyFromIndex = report.nativeRequests.length;
  await clickAddRelated();
  const allProposal = await waitRelatedProposal('reopened-all-preview-before-apply', allApplyStarted, 'ALL', allApplyFromIndex);
  assert.equal(allProposal.status, 200);
  assert.equal(allProposal.response.previewStatus, 'READY');
  const allProposalReceiptId = await waitAdoptedChoicePreview(allProposal, 'Reopened ALL proposal');
  report.reopenedAllProposalReceiptId = allProposalReceiptId;
  const applyStarted = Date.now();
  await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  const allCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE'), allApplyFromIndex);
  assert.equal(allCommand.status, 200, JSON.stringify(allCommand.response));
  assert.equal(allCommand.request.commands.length, 1);
  assert.equal(allCommand.request.commands[0].constructionChoice.form, 'ALL');
  assert.equal(allCommand.request.commands[0].constructionChoice.rowValuePolicy, 'ALL');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')`, 5000);
  const addedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows), witness.observationIds.join('; ')]).sort((a, b) => a[0].localeCompare(b[0]));
  await rendered(addedRows);
  record('apply-related-all-to-native-table-render', applyStarted, { commandStatus: allCommand.status });
  builder = await api(`${base}/builder`);
  const addedDocument = doc();
  assert.deepEqual(addedDocument.population, groupedBaseline.population, 'ALL apply must preserve the exact source selection');
  const relatedColumn = addedDocument.columns.find((column) => column.label === 'Observation ID');
  assert(relatedColumn, 'The related Observation ID source binding was not saved');
  assert.equal(relatedColumn.source.field.path, 'id');
  assert.equal(relatedColumn.source.field.projectionMode, 'ALL');
  const savedGroup = addedDocument.construction.steps.find((step) => step.id === groupStepBefore.id);
  assert(savedGroup, 'The original Group step identity must remain stable');
  assert.deepEqual(savedGroup.operation.group.keys, groupStepBefore.operation.group.keys, 'ALL apply changed the authored group key binding');
  assert.deepEqual(savedGroup.operation.group.aggregates, groupStepBefore.operation.group.aggregates, 'ALL apply changed the authored Group aggregate');
  const rowValue = savedGroup.rowValues.find((value) => value.inputColumnId === relatedColumn.columnId);
  assert(rowValue, 'The saved Group lacks the related source column binding');
  assert.equal(rowValue.policy, 'ALL');
  const rowValueOutput = savedGroup.outputs.find((output) => output.id === rowValue.outputColumnId);
  assert(rowValueOutput);
  assert.equal(rowValueOutput.name, relatedColumn.column);
  for (const sourceColumn of groupedBaseline.columns) {
    assert.deepEqual(addedDocument.columns.find((column) => column.columnId === sourceColumn.columnId), sourceColumn, `ALL apply changed source binding ${sourceColumn.label}`);
  }
  const applyPreviewFrom = report.nativeRequests.indexOf(allCommand);
  const savedPreviewStarted = Date.now();
  const proposalPreview = allProposal.response.preview;
  assert(proposalPreview, 'The accepted ALL proposal did not retain its candidate preview');
  assert.equal(allProposal.response.candidateWorkspaceDigest, builder.draftDigest, 'The saved builder digest must equal the applied proposal digest');
  assert.equal(allProposal.response.outputId, outputId);
  assert.equal(allCommand.response.draftVersion, builder.draftVersion);
  assert.equal(allCommand.response.draftDigest, builder.draftDigest);
  const acceptedReconcile = await waitNative((entry) => entry.path.endsWith('/reconcile') &&
    entry.request?.draftVersion === builder.draftVersion && entry.request?.draftDigest === builder.draftDigest, applyPreviewFrom);
  assert.equal(acceptedReconcile.status, 200, JSON.stringify(acceptedReconcile.response));
  assert.equal(acceptedReconcile.response.snapshotToken, allProposal.response.snapshotToken);
  assert.equal(acceptedReconcile.response.intentDigest, builder.draftDigest);
  assert(acceptedReconcile.response.outputs?.some((output) => output.outputId === outputId), 'The saved reconcile receipt does not include the edited output');
  assert.equal(acceptedReconcile.response.receiptId, proposalPreview.receiptId, 'The applied proposal preview receipt must be accepted for the saved builder');
  assert.equal(proposalPreview.outputId, outputId);
  await waitForBrowser(browser.cdp, `(() => {const preview=document.querySelector('[data-testid="construction-preview"]');return preview?.dataset.previewStatus==='ready'&&preview?.dataset.previewReceiptId===${JSON.stringify(acceptedReconcile.response.receiptId)}&&preview?.dataset.previewOutputId===${JSON.stringify(outputId)}&&preview?.dataset.currentDraftVersion===${JSON.stringify(String(builder.draftVersion))}&&preview?.dataset.currentDraftDigest===${JSON.stringify(builder.draftDigest)};})()`, 5000);
  const activePreview = await browserEval(browser.cdp, `const preview=document.querySelector('[data-testid="construction-preview"]');return {status:preview?.dataset.previewStatus,receiptId:preview?.dataset.previewReceiptId,outputId:preview?.dataset.previewOutputId,draftVersion:preview?.dataset.currentDraftVersion,draftDigest:preview?.dataset.currentDraftDigest};`);
  assert.deepEqual(activePreview, {
    status: 'ready',
    receiptId: acceptedReconcile.response.receiptId,
    outputId,
    draftVersion: String(builder.draftVersion),
    draftDigest: builder.draftDigest,
  }, 'The rendered table must be the active preview for the exact saved draft and accepted receipt');
  const applyPreviewRequests = report.nativeRequests.slice(applyPreviewFrom).filter((entry) => entry.path.endsWith('/preview'));
  for (const entry of applyPreviewRequests) {
    assert(nativeRequestValue(entry, 'outputId'), `Captured ${entry.method} ${entry.path} without outputId body/query: ${JSON.stringify(entry)}`);
    const requestDeadline = Date.now() + 5000;
    while (!entry.complete && Date.now() < requestDeadline) await pause(25);
    assert(entry.complete, `Timed out waiting for captured preview response: ${JSON.stringify({ method: entry.method, url: entry.url, query: entry.query, request: entry.request })}`);
  }
  const targetPreviewRequests = applyPreviewRequests.filter((entry) => nativeRequestValue(entry, 'outputId') === outputId);
  for (const entry of targetPreviewRequests) {
    assert.equal(nativeRequestValue(entry, 'receiptId'), acceptedReconcile.response.receiptId, 'A post-Apply native preview must use the accepted saved receipt');
    assert.equal(entry.status, 200, JSON.stringify(entry.response));
    assert.equal(entry.response?.receiptId, acceptedReconcile.response.receiptId);
    assert.equal(entry.response?.outputId, outputId);
  }
  const previewSource = targetPreviewRequests.length === 0 ? 'accepted-choice-proposal-preview-reused' : 'post-apply-native-preview';
  const savedPreview = targetPreviewRequests.at(-1)?.response ?? proposalPreview;
  if (targetPreviewRequests.length > 0) {
    assert.deepEqual(savedPreview.rows, proposalPreview.rows, 'Post-Apply Preview differs from the accepted proposal preview for the same receipt');
  }
  report.savedPreviewVerification = {
    source: previewSource,
    outputId,
    receiptId: acceptedReconcile.response.receiptId,
    savedDraftVersion: builder.draftVersion,
    savedDraftDigest: builder.draftDigest,
    candidateWorkspaceDigest: allProposal.response.candidateWorkspaceDigest,
    proposalRequestId: allProposal.requestId,
    reconcileRequestId: acceptedReconcile.requestId,
    activePreview,
    postApplyPreviewRequests: applyPreviewRequests.map((entry) => ({
      requestId: entry.requestId,
      method: entry.method,
      url: entry.url,
      path: entry.path,
      query: entry.query,
      request: entry.request,
      status: entry.status,
      responseReceiptId: entry.response?.receiptId,
      responseOutputId: entry.response?.outputId,
      rowCount: entry.response?.rowCount,
      complete: entry.complete,
    })),
  };
  assert.equal(savedPreview.rowCount, witnesses.length);
  for (const witness of witnesses) {
    const row = savedPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Saved native Preview omitted ${witness.category} witness`);
    assert.deepEqual(row[relatedColumn.column], witness.observationIds, `${witness.category} saved values differ from independent CDA`);
  }
  record('native-preview-matches-available-cda-witness-oracle', savedPreviewStarted, {
    receiptId: savedPreview.receiptId,
    source: previewSource,
    postApplyPreviewRequestCount: targetPreviewRequests.length,
  });

  const reloadedPreview = await openTable(addedRows, 'reload-related-all-available-cardinality-values');
  for (const witness of witnesses) {
    const row = reloadedPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Reloaded Preview omitted ${witness.category} witness`);
    assert.deepEqual(row[relatedColumn.column], witness.observationIds);
  }

  const removeStarted = Date.now();
  const removeFromIndex = report.nativeRequests.length;
  await click(browser.cdp, 'button', { name: 'Columns' });
  const removeLabel = `Remove ${relatedColumn.label} column`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`button[aria-label=${JSON.stringify(removeLabel)}]`)}))`, 5000);
  await click(browser.cdp, `button[aria-label=${JSON.stringify(removeLabel)}]`);
  const removeCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) => item.type === 'REMOVE_COLUMN' && item.column === relatedColumn.column), removeFromIndex);
  assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
  builder = await api(`${base}/builder`);
  assert.deepEqual(doc().columns, groupedBaseline.columns, 'Removing the related ID column must restore the original source columns');
  assert.deepEqual(doc().construction, groupedBaseline.construction, 'Removing the related ID column must restore the original Group construction');
  assert.deepEqual(doc().population, groupedBaseline.population, 'Removing the related ID column must restore the original exact source membership');
  const restoredRows = groupedRows;
  await rendered(restoredRows);
  record('remove-related-column-restores-native-group-table', removeStarted, { commandStatus: removeCommand.status });
  const restoredPreview = await openTable(restoredRows, 'reload-restored-available-witness-group-table');
  assert.equal(restoredPreview.rowCount, witnesses.length);
  assert(!restoredPreview.columns.some((column) => column.column === relatedColumn.column), 'Reloaded Group preview still contains the removed related value');
  for (const witness of witnesses) {
    const row = restoredPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Reloaded Group preview omitted ${witness.category} witness`);
    assert.equal(row.row_count, witness.expectedContributorRows, `${witness.category} grouped count differs from the raw CDA oracle`);
    assert.equal(Object.hasOwn(row, relatedColumn.column), false, `${witness.category} restored row still contains the removed related value`);
  }

  const expectedHttpFailures = report.nativeRequests.filter((entry) => entry.path.endsWith('/construction-choice-proposals') && entry.status === 422 && entry.response?.error?.code === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
  const unexpectedHttp = report.nativeRequests.filter((entry) => entry.status >= 400 && !expectedHttpFailures.includes(entry));
  assert.equal(expectedHttpFailures.length, 1, 'Exactly one raw-oracle-predicted ONE disagreement is expected');
  assert.deepEqual(unexpectedHttp, [], 'No unexpected browser HTTP errors are allowed');
  assert.deepEqual(report.errors, [], 'No unexpected browser runtime, console, or module errors are allowed');
  assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
  report.repairStatus = 'passed';
  if (report.oracle.missingWitnessCategories.length > 0) {
    const categories = report.oracle.missingWitnessCategories.map(({ category }) => category);
    report.status = 'unverified';
    report.productFailure = false;
    report.unverifiedReason = `The grouped-row ONE-to-ALL repair passed, but bounded raw witnesses were unavailable for: ${categories.join(', ')}.`;
    report.unverified = {
      kind: 'bounded-optional-witness-unavailable', message: report.unverifiedReason,
      diagnostics: report.oracle.fixtureAvailability,
    };
  } else {
    report.status = 'passed';
  }
} catch (error) {
  const rawOracleUnavailable = verificationPhase === 'raw-oracle';
  report.status = rawOracleUnavailable ? 'unverified' : 'failed';
  if (rawOracleUnavailable) {
    report.productFailure = false;
    report.unverifiedReason = error.message;
    report.unverified = {
      kind: report.oracle?.fixtureAvailability ? 'raw-witness-oracle' : 'raw-witness-oracle-or-query',
      message: error.message,
      diagnostics: {
        fixtureAvailability: report.oracle?.fixtureAvailability,
        searchBounds: report.oracle?.searchBounds,
        boundedAbsence: report.oracle?.boundedAbsence,
        witnessQueries: report.oracle?.witnessQueries,
      },
    };
  }
  report.error = String(error.stack ?? error);
  report.savedBuilderAtFailure = builder ? await api(`${base}/builder`).catch((readError) => ({ readError: String(readError) })) : undefined;
  report.failureUI = browser ? await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');const proposal=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {body:document.body.innerText.slice(0,5000),chooser:{open:Boolean(dialog),policy:policy?.value},proposal:{status:proposal?.dataset.proposalStatus,text:proposal?.innerText}};`).catch(String) : undefined;
  if (!rawOracleUnavailable) process.exitCode = 1;
} finally {
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, explorer, cases: report.cases.map(({ name, durationMs }) => ({ name, durationMs })), error: report.error }));
