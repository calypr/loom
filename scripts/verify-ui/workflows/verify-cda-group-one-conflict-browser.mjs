import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { assertVisibleRowsMatchOracle } from '../helpers/cda-row-oracle.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';

export const groupOneConflictRawFieldsSummarySelector = '[data-testid="feature-catalog-raw-fields"] > summary';
export const groupOneConflictOperationPolicySelector = '[aria-label="Add columns editor"] select[aria-label="Values per grouped row"]';
export const groupOneConflictSelectedFieldSelector = '[aria-label="Add columns editor"] input[aria-label="Select Specimen.id"]';

export function inspectGroupOneConflictRecoveryState({ editorSelector, proposalSelector, selectedFieldSelector }) {
  const editor = document.querySelector(editorSelector);
  const policy = editor?.querySelector('select[aria-label="Values per grouped row"]');
  const field = document.querySelector(selectedFieldSelector);
  const proposal = document.querySelector(proposalSelector);
  return {
    editorOpen: Boolean(editor),
    proposalStatus: proposal?.dataset.proposalStatus,
    fieldSelected: field?.checked === true,
    policy: policy?.value,
    allAvailable: Array.from(policy?.options ?? []).some(option => option.value === 'ALL'),
  };
}

export async function classifyGroupOneExpectedONEFailure({
  fixtureEntry,
  workflowEntry,
  expectedPath,
  outputId,
  expectedDraftVersion,
  expectedDraftDigest,
  expectedSnapshotToken,
  classify,
}) {
  assert.equal(typeof classify, 'function', 'ONE failure needs the fixture-owned classifier callback');
  for (const [source, entry] of [['fixture', fixtureEntry], ['workflow', workflowEntry]]) {
    assert(entry && typeof entry === 'object', `${source} ONE failure capture must be an object`);
    assert.equal(entry.method, 'POST', `${source} ONE failure must be a POST`);
    assert.equal(entry.path, expectedPath, `${source} ONE failure must use the exact owned choice-proposal path`);
    assert.equal(entry.status, 422, `${source} ONE failure must have exact HTTP 422 status`);
    assert.equal(entry.response?.error?.code, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
      `${source} ONE failure must carry the typed multiple-values diagnostic`);
    assert.equal(entry.body?.outputId, outputId, `${source} ONE failure must target the exact saved Group output`);
    assert.equal(entry.body?.expectedDraftVersion, expectedDraftVersion, `${source} ONE failure must use the current draft version`);
    assert.equal(entry.body?.expectedDraftDigest, expectedDraftDigest, `${source} ONE failure must use the current draft digest`);
    assert.equal(entry.body?.snapshotToken, expectedSnapshotToken, `${source} ONE failure must use the current catalog snapshot`);
    assert.equal(entry.body?.constructionChoices?.length, 1, `${source} ONE failure must submit one selected field`);
    assert.equal(entry.body.constructionChoices[0].rowValuePolicy, 'ONE', `${source} ONE failure must be the exact scalar policy`);
    assert.equal(entry.body.constructionChoices[0].title, 'Specimen ID', `${source} ONE failure must target Specimen.id`);
  }
  assert.equal(fixtureEntry.requestId, workflowEntry.requestId, 'Both capture views must identify the same captured request');
  assert.equal(fixtureEntry.browserRequestId, workflowEntry.browserRequestId, 'Both capture views must identify the same browser request');
  assert.equal(typeof fixtureEntry.requestId, 'string', 'ONE failure must have a concrete request identity');
  assert(fixtureEntry.requestId, 'ONE failure request identity must not be empty');
  assert.equal(typeof fixtureEntry.browserRequestId, 'string', 'ONE failure must have a concrete browser request identity');
  assert(fixtureEntry.browserRequestId, 'ONE failure browser identity must not be empty');
  return classify(fixtureEntry);
}

export function buildGroupOneConflictRawWitnessQuery(project, generation) {
  return `FOR s IN Specimen
    FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
    SORT s._id LIMIT 1
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e.label == "subject_Patient"
        AND e.project == s.project AND e.dataset_generation == s.dataset_generation
        AND STARTS_WITH(e._to, "Patient/")
      LET patient = DOCUMENT(e._to)
      FILTER patient.project == s.project AND patient.dataset_generation == s.dataset_generation
      LET members = (
        FOR se IN fhir_edge
          FILTER se._to == e._to AND se.label == "subject_Patient"
            AND se.project == s.project AND se.dataset_generation == s.dataset_generation
            AND STARTS_WITH(se._from, "Specimen/")
          COLLECT specimenKey = se._from
          SORT specimenKey
          LIMIT 2
          LET d = DOCUMENT(specimenKey)
          FILTER d.project == s.project AND d.dataset_generation == s.dataset_generation
            AND IS_STRING(d.id) AND LENGTH(TRIM(d.id)) > 0
          RETURN {id: d.id, _id: d._id}
      )
      FILTER LENGTH(members) == 2
      RETURN {patientID: patient.id, patientResourceID: patient._id,
        resourceType: "Specimen", generation: s.dataset_generation, sources: members}`;
}

export async function runGroupOneConflictBrowserWorkflow({ page, cda }) {
  const project = cda.project;
  assert(project, 'CDA fixture must provide the isolated project');
  const requestedExplorerName = cda.explorer;
  assert(requestedExplorerName, 'CDA fixture must provide an isolated requested Explorer name');
  const generation = cda.generation;
  assert.equal(generation, 'cda-fhir-v1', 'Group ONE requires the pinned CDA fixture generation');
  let explorer;
  const evidence = cda.evidence;
  const apiOrigin = cda.apiOrigin;
  const uiOrigin = cda.uiOrigin;
  const env = cda.env ?? {};
  const arangoContainer = cda.target?.arangoContainer ?? env.LOOM_ARANGO_CONTAINER;
  cda.report.errors ??= [];
  cda.report.nativeRequests ??= [];
const explorerCollection = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
let explorerRoot;
let base;
const report = { requestedExplorerName, evidence, target: cda.target, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString(),
  workflowBoundary: {
    intent: 'ONE rejects a scalar field when the two independent Specimen IDs disagree; the inline error leaves the Add Columns editor and selected field available for changing to ALL.',
    notCovered: 'Editing a pre-existing saved related-field policy is a separate future workflow.',
  },
};
let builder, outputId;
const click = (...args) => cda.click(...args);
const selectOption = (...args) => cda.selectOption(...args);
const fill = (...args) => cda.fill(...args);
const browserEval = (...args) => cda.inspect(...args);
const waitForBrowser = (...args) => cda.wait(...args);
const navigate = (...args) => cda.navigate(...args);
const recordCheck = (dimension, name, passed, checkEvidence = {}) => cda.check(dimension, name, passed, checkEvidence);
const sensitiveName = /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i;
const sanitizeText = value => String(value ?? '')
  .replaceAll(process.cwd(), '$CHECKOUT')
  .replace(/(?:file:\/\/)?\/(?:private\/)?tmp\/[^\s)]+/g, '$TMP/<path>')
  .replace(/\/Users\/[^/\s]+/g, '$HOME')
  .replace(/\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi, 'Bearer [REDACTED]')
  .replace(/["']?[\w-]*(?:token|authorization|set-cookie|cookie|password|passwd|secret|credential|session(?:[_-]?id)?|api[_-]?key)[\w-]*["']?\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^,;\s}\]]+)/gi, '[REDACTED]')
  .replace(/<input\b[^>]*>/gi, tag => sensitiveName.test(tag) ? tag.replace(/(\bvalue\s*=\s*)(["'])(.*?)\2/gi, '$1$2[REDACTED]$2') : tag)
  .replace(/\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\b/g, '[REDACTED_TOKEN]')
  .replace(/\bsk-[A-Za-z0-9]{16,}\b/g, '[REDACTED_TOKEN]');
const sanitizeReportValue = (value, key = '') => {
  if (sensitiveName.test(key)) return '[REDACTED]';
  if (typeof value === 'string') return sanitizeText(value);
  if (Array.isArray(value)) return value.map(item => sanitizeReportValue(item));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, sanitizeReportValue(childValue, childKey)]));
  return value;
};

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `group-one-conflict-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, ...(body ? { body: sanitizeReportValue(body) } : {}), status: response.status, response: sanitizeReportValue(value) });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const proposal = async (name, start, expectedRows) => {
  await waitForBrowser(() => (['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await browserEval(() => { const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))}; });
  assert.equal(result.status, 'ready', result.text);
  assertVisibleRowsMatchOracle(result.rows, expectedRows, { label: `${name} preview` });
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async (expectedRows, name) => {
  const start = Date.now();
  await click( '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender(`${name}-apply-to-render`, start);
  builder = await api(base + '/builder');
};
const open = async (expectedRows, name) => {
  const start = Date.now();
  const query = new URLSearchParams({ project, explorer, mode: 'builder' });
  await navigate(`${uiOrigin}/?${query}`);
  await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` });
  await click( `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows);
  recordRender(`${name}-load-to-render`, start);
};
const rendered = async expectedRows => {
  await waitForBrowser(({ rowCount, columnCount }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(Math.min(25, rowCount) + 1) && table?.getAttribute('aria-colcount') === String(columnCount) && !document.body.innerText.includes('Loading your table…');
  }, { rowCount: expectedRows.length, columnCount: expectedRows[0]?.length ?? 2 });
  const rows = await browserEval(() => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length); });
  const savedRows = expectedRows;
  assertVisibleRowsMatchOracle(rows, savedRows, { label: 'saved table' });
};
try {
  const query = buildGroupOneConflictRawWitnessQuery(project, generation);
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.patientID && source?.patientResourceID);
  report.oracle = { query, source };
  assert.equal(source.sources.length, 2, 'Raw Group ONE witness must contain exactly two distinct Specimens');
  assert.equal(new Set(source.sources.map(member => member._id)).size, 2, 'Raw Group ONE witness must use distinct Specimen resources');
  assert.equal(new Set(source.sources.map(member => member.id)).size, 2, 'Raw Group ONE witness must have distinct Specimen IDs');
  assert(source.patientID && source.patientResourceID, 'Raw Group ONE witness must identify its shared Patient');
  recordCheck('correctness', 'scoped raw oracle proves two distinct Specimen IDs share one Patient', true, {
    patientID: source.patientID,
    patientResourceID: source.patientResourceID,
    specimenIDs: source.sources.map(member => member.id),
    specimenResourceIDs: source.sources.map(member => member._id),
    generation: source.generation,
  });
  const createdExplorer = await api(explorerCollection, { name: requestedExplorerName, title: 'Group ONE conflict QA' });
  const creationRequest = report.requests.at(-1);
  assert.equal(creationRequest?.path, explorerCollection);
  assert.equal(creationRequest?.status, 201);
  assert.equal(creationRequest?.body?.name, requestedExplorerName);
  const createdScope = createdExplorerScope(project, createdExplorer);
  explorer = createdScope.explorerId;
  explorerRoot = createdScope.explorerRoot;
  base = createdScope.authoringBase;
  report.explorer = explorer;
  report.target = { ...report.target, explorer };
  report.explorerProvisioning = {
    requestedName: requestedExplorerName,
    createStatus: creationRequest.status,
    returnedProject: createdExplorer.project,
    returnedExplorerId: explorer,
  };
  builder = await api(base + '/builder');
  assert.equal(source.generation, generation, 'Raw source witness must match the pinned CDA fixture generation');
  assert.equal(builder.catalog.generation, generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Group QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(explorerRoot + '/selections', { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: source.sources.map(member=>({ project, generation: source.generation, resourceType: 'Specimen', id: member.id })) } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  const fixtureCapture = cda.captureRequests(explorerRoot);
  const nativeCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [uiOrigin], ownedPathPrefix: explorerRoot, report });
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',arangoContainer,'arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
    assert.equal(r.status,0,r.stderr);return JSON.parse(r.stdout.slice(r.stdout.indexOf('[')));
  };
  assert.equal(source.sources.length, 2);
  let witnesses=source.sources.map(member=>({anchor:member._id,values:[member.id]}));
  let expected=witnesses.map(w=>w.values);
  await open(expected, 'initial-specimen');
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
  ];
  for(const hop of chain){
    const next=[];
    for(const witness of witnesses){
      const endpoint=hop.direction==='OUTBOUND'?'_from':'_to';
      const target=hop.direction==='OUTBOUND'?'_to':'_from';
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)} FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project==${JSON.stringify(project)} AND d.dataset_generation==${JSON.stringify(generation)} RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches=witness.anchor?rawQuery(query):[];
      if(matches.length)for(const match of matches)next.push({anchor:match._id,values:[...witness.values,match.id]});
      else next.push({anchor:null,values:[...witness.values,'—']});
    }
    assert(next.length<=1000,'Use a bounded CDA chain fixture');
    witnesses=next;expected=witnesses.map(w=>w.values);
    report.oracle.chain??=[];report.oracle.chain.push({hop,witnesses});
    await click('[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(() => (document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await click('[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${panel} select[aria-label="Related record type"]` });
    let start=Date.now();
    await selectOption(panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${panel} input[aria-label="${label}"]` }, 5000);
    await click(panel+' input[aria-label="'+label+'"]');
    await proposal('specimen-to-patient-preview',start,expected);
    await apply(expected, 'specimen-to-patient');
  }
  const expanded=builder;
  assert(expected.length>1,'CDA fixture must exercise many related records before grouping');
  assert.equal(new Set(witnesses.map(w=>w.values.at(-1))).size,1);
  assert.equal(witnesses[0].values.at(-1), source.patientID, 'Expanded source rows must resolve to the raw witness Patient');
  const grouped=[[witnesses[0].values.at(-1),String(expected.length)]];
  await open(expected, 'after-expansion');
  const configureGroup=async()=>{
    await click('[data-testid="construction-rows-settings-trigger"]');
    await click('[data-testid="construction-action-group-rows"]');
    await waitForBrowser(() => (document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]:not(:disabled)')));
    start=Date.now();
    await click('input[aria-label="Group by Patient FHIR resource ID"]');
  };
  let start;
  await configureGroup();
  await proposal('patient-group-preview-cancelled',start,grouped);
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  const afterGroupCancel = await api(base+'/builder');
  assert.deepEqual(afterGroupCancel.workspace,expanded.workspace);
  recordCheck('persistence', 'Group Cancel preserves the exact expanded workspace before Group Apply', true, {
    draftVersion: afterGroupCancel.draftVersion,
    draftDigest: afterGroupCancel.draftDigest,
  });
  await configureGroup();
  await proposal('patient-group-preview-confirmed',start,grouped);
  recordCheck('correctness', 'Group by Patient preview matches the exact independent raw rows', true, {
    expectedRows: grouped,
    rawPatientID: source.patientID,
    contributingSpecimenIDs: source.sources.map(member => member.id),
  });
  await apply(grouped, 'patient-group');
  const appliedGroupConstruction = doc(builder).construction;
  assert(appliedGroupConstruction, 'Applied Patient Group must have a saved construction');
  await open(grouped, 'after-group');
  builder = await api(base + '/builder');
  assert.deepEqual(doc(builder).construction, appliedGroupConstruction, 'Applied Patient Group construction must persist after Builder reload');
  recordCheck('persistence', 'Applied Patient Group construction and rows persist after Builder reload', true, {
    expectedRows: grouped,
    construction: appliedGroupConstruction,
  });
  await click('button',{name:'Columns'});
  const consumedControls=await browserEval(() => { return [...document.querySelectorAll('button[aria-label="Remove Specimen ID column"]')].length; });
  assert.equal(consumedControls,0,'Columns must manage final grouped outputs, not a consumed upstream Specimen ID');
  await click('button',{name:'Columns'});
  await click('[data-testid="construction-action-add-columns"]');
  await click('[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-add-columns-source"]')));
  report.addFieldsUI=await browserEval(() => { return {text:document.querySelector('[aria-label="Add columns editor"]').innerText,controls:[...document.querySelectorAll('[aria-label="Add columns editor"] input,[aria-label="Add columns editor"] select,[aria-label="Add columns editor"] button')].map(e=>({tag:e.tagName,label:e.getAttribute('aria-label'),testId:e.dataset.testid,text:e.innerText,disabled:e.disabled}))}; });
  const beforeField=builder;
  await selectOption(groupOneConflictOperationPolicySelector,'ONE');
  await click(groupOneConflictRawFieldsSummarySelector);
  await waitForBrowser(() => (document.querySelector('input[aria-label="Select Specimen.id"]:not(:disabled)')));
  start=Date.now();
  await click('input[aria-label="Select Specimen.id"]');
  await click('[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
  await waitForBrowser(() => (['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)));
  report.oneResult=await browserEval(() => {
    const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');
    const alert=panel?.querySelector('[role="alert"]');
    return {status:panel?.dataset.proposalStatus,text:panel?.innerText,alertText:alert?.innerText,
      alertVisible:Boolean(alert && alert.getClientRects().length && getComputedStyle(alert).visibility!=='hidden')};
  });
  assert.equal(report.oneResult.status,'error','ONE must reject two distinct contributing Specimen IDs');
  assert.equal(report.oneResult.alertVisible,true,'ONE failure must be visible in the inline proposal as an alert');
  assert(!/INTERNAL_ERROR|internal server error/i.test(report.oneResult.text),report.oneResult.text);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeField.workspace);
  recordRender('one-disagreement-diagnostic',start);
  const expectedPath = `${base}/construction-choice-proposals`;
  const fixtureFailure = await cda.waitForCapturedResponse(fixtureCapture, request => request.method === 'POST' && request.path === expectedPath && request.status >= 400, 5000);
  await nativeCapture.flush();
  const disagreement = report.nativeRequests.filter(request => request.method === 'POST' && request.path === expectedPath && request.status >= 400);
  assert.equal(fixtureFailure.response.error.code, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
  assert.equal(disagreement.length, 1, JSON.stringify(report.nativeRequests));
  const [expectedFailure] = disagreement;
  assert(expectedFailure.requestId && expectedFailure.browserRequestId, 'Expected choice failure must have an exact browser request identity');
  assert.equal(expectedFailure.status, 422);
  assert.equal(expectedFailure.response.error.code, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
  recordCheck('correctness', 'ONE reports the multiple-values 422 and leaves the saved Group unchanged', true, {
    status: expectedFailure.status,
    code: expectedFailure.response.error.code,
    proposalStatus: report.oneResult.status,
    proposalText: report.oneResult.text,
    savedWorkspaceUnchanged: true,
  });
  const expectedFailureReason = 'The ONE grouping mode must reject multiple contributor IDs.';
  const expectedFailureProof = {
    expectedStatus: 422,
    expectedCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
    outputId,
    expectedDraftVersion: beforeField.draftVersion,
    expectedDraftDigest: beforeField.draftDigest,
    expectedSnapshotToken: beforeField.catalog.snapshotToken,
    proposal: report.oneResult,
  };
  const fixtureClassification = await classifyGroupOneExpectedONEFailure({
    fixtureEntry: fixtureFailure,
    workflowEntry: expectedFailure,
    expectedPath,
    outputId,
    expectedDraftVersion: beforeField.draftVersion,
    expectedDraftDigest: beforeField.draftDigest,
    expectedSnapshotToken: beforeField.catalog.snapshotToken,
    classify: entry => cda.expectHttpFailure(entry, expectedFailureReason, expectedFailureProof),
  });
  const failureErrors = report.errors.filter(error => error.kind === 'http' &&
    error.requestId === expectedFailure.requestId && error.browserRequestId === expectedFailure.browserRequestId &&
    error.method === expectedFailure.method && error.path === expectedFailure.path && error.status === expectedFailure.status);
  assert.equal(failureErrors.length, 1, 'The exact workflow-capture 422 must be recorded once before classification');
  const consoleErrors = report.errors.filter(error => error.kind === 'console' &&
    error.location === `${uiOrigin}${expectedPath}` &&
    /status of 422 \(Unprocessable Entity\)/.test(error.message));
  assert.equal(consoleErrors.length, 1, 'The exact workflow-capture 422 console diagnostic must be recorded once');
  report.expectedFailures ??= [];
  report.expectedFailures.push({ requestId: expectedFailure.requestId, browserRequestId: expectedFailure.browserRequestId,
    method: expectedFailure.method, path: expectedFailure.path, status: expectedFailure.status,
    code: expectedFailure.response.error.code, reason: expectedFailureReason,
    fixtureClassification,
  });
  for (const error of [...failureErrors, ...consoleErrors]) report.errors.splice(report.errors.indexOf(error), 1);
  const contributorIDs=source.sources.map(member=>member.id).sort();
  const withField=[[...grouped[0],contributorIDs.join('; ')]];
  // Repair the existing selection instead of forcing the user to select it again.
  const recoveryStateArgs = {
    editorSelector: '[aria-label="Add columns editor"]',
    proposalSelector: '[data-testid="construction-choice-proposal-panel"]',
    selectedFieldSelector: groupOneConflictSelectedFieldSelector,
  };
  report.oneFailureState=await browserEval(inspectGroupOneConflictRecoveryState, recoveryStateArgs);
  assert.deepEqual(report.oneFailureState,{editorOpen:true,proposalStatus:'error',fieldSelected:true,policy:'ONE',allAvailable:true},
    'The inline ONE error must leave the selected field and policy control in the Add Columns editor');
  start=Date.now();
  await selectOption(groupOneConflictOperationPolicySelector,'ALL');
  report.recoveryState=await browserEval(inspectGroupOneConflictRecoveryState, recoveryStateArgs);
  assert.deepEqual(report.recoveryState,{editorOpen:true,proposalStatus:'error',fieldSelected:true,policy:'ALL',allAvailable:true},
    'Changing to ALL must preserve the selected field in the same Add Columns editor');
  recordCheck('usability', 'failed ONE keeps the selected Specimen.id field and ALL recovery in the same editor', true, {
    afterONE: report.oneFailureState,
    afterSwitchToALL: report.recoveryState,
  });
  await click('[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
  await waitForBrowser(() => (['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)));
  const repaired=await browserEval(() => { const p=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:p.dataset.proposalStatus,text:p.innerText}; });
  assert.equal(repaired.status,'ready',repaired.text);
  const cells=await browserEval(() => { return [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')].map(cell=>({text:cell.innerText,raw:cell.title})); });
  assert.deepEqual(cells.slice(0,2).map(cell=>cell.text),grouped[0]);
  assert.deepEqual(JSON.parse(cells[2].raw),contributorIDs);
  recordRender('all-repair-preview',start);
  recordCheck('correctness', 'ALL preview contains exactly the two raw Specimen IDs', true, {
    patientID: source.patientID,
    specimenIDs: contributorIDs,
    previewRow: cells.map(cell => cell.text),
    rawContributorIDs: JSON.parse(cells[2].raw),
  });
  start=Date.now();
  await click('[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-choice-proposal-panel"]')));
  await rendered(withField);
  recordRender('all-repair-apply-to-render',start);
  builder=await api(base+'/builder');
  const appliedConstruction = doc(builder).construction;
  await open(withField, 'after-all-repair');
  const reloadedBuilder = await api(base+'/builder');
  assert.deepEqual(doc(reloadedBuilder).construction, appliedConstruction, 'Applied ALL construction must persist after Builder reload');
  recordCheck('persistence', 'applied ALL field renders and persists after Builder reload', true, {
    expectedRows: withField,
    construction: appliedConstruction,
  });
  await click('button',{name:'Columns'});
  assert.equal(await browserEval(() => { return document.querySelectorAll('button[aria-label="Remove Specimen ID column"]').length; }),1,'Added field must have one unambiguous removal control');
  start=Date.now();
  await click('button[aria-label="Remove Specimen ID column"]');
  await rendered(grouped);
  recordRender('remove-repaired-field',start);
  builder=await api(base+'/builder');
  const restoredConstruction = doc(builder).construction;
  await open(grouped, 'after-removal');
  const reloadedRestoredBuilder = await api(base+'/builder');
  assert.deepEqual(doc(reloadedRestoredBuilder).construction,doc(beforeField).construction);
  assert.deepEqual(doc(reloadedRestoredBuilder).construction,restoredConstruction);
  recordCheck('persistence', 'removing the field and reloading restores the original Group', true, {
    expectedRows: grouped,
    restoredConstruction,
  });
  await nativeCapture.flush();
  assert.deepEqual(report.errors,[]);
  const expectedRenderCheckpoints = [
    'initial-specimen-load-to-render',
    'specimen-to-patient-preview',
    'specimen-to-patient-apply-to-render',
    'after-expansion-load-to-render',
    'patient-group-preview-cancelled',
    'patient-group-preview-confirmed',
    'patient-group-apply-to-render',
    'after-group-load-to-render',
    'one-disagreement-diagnostic',
    'all-repair-preview',
    'all-repair-apply-to-render',
    'after-all-repair-load-to-render',
    'remove-repaired-field',
    'after-removal-load-to-render',
  ];
  const actualRenderCheckpoints = report.cases.map(checkpoint => checkpoint.name);
  const maxRenderMs = Math.max(...report.cases.map(checkpoint => checkpoint.durationMs));
  const renderCheckpointsComplete = expectedRenderCheckpoints.length === actualRenderCheckpoints.length &&
    expectedRenderCheckpoints.every(name => actualRenderCheckpoints.includes(name));
  const renderBudgetPassed = renderCheckpointsComplete && report.cases.every(checkpoint => checkpoint.durationMs <= 5000);
  recordCheck('performance', 'all Group ONE action-to-render checkpoints complete within five seconds', renderBudgetPassed, {
    expectedCheckpoints: expectedRenderCheckpoints,
    actualCheckpoints: actualRenderCheckpoints,
    timingCheckpoints: report.cases.map(checkpoint => ({
      name: checkpoint.name,
      durationMs: checkpoint.durationMs,
      budgetMs: 5000,
      passed: checkpoint.durationMs <= 5000,
    })),
    maximumDurationMs: maxRenderMs,
    budgetMs: 5000,
  });
  report.expectedDiagnostics=disagreement;
  report.status='passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.savedBuilderAtFailure = base
    ? await api(base + '/builder').catch(readError => ({ readError: String(readError) }))
    : { unavailable: 'Explorer creation did not return a usable server-assigned scope' };
  report.failureUI = await browserEval(() => document.body.innerText).catch(String);
  throw error;
} finally {
  report.finished = new Date().toISOString();
  await cda.attachReport('group-one-conflict', report);
}
return report;
}
