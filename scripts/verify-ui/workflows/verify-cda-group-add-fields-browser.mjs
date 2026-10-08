import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { assertVisibleRowsMatchOracle } from '../helpers/cda-row-oracle.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';

export const groupAddFieldsRawFieldsSummarySelector = '[data-testid="feature-catalog-raw-fields"] > summary';

export async function runGroupAddFieldsBrowserWorkflow({ page, cda }) {
  const project = cda.project;
  assert(project, 'CDA fixture must provide the isolated project');
  let explorer = cda.explorer;
  const requestedExplorerName = explorer;
  const evidence = cda.evidence;
  const apiOrigin = cda.apiOrigin;
  const uiOrigin = cda.uiOrigin;
  const env = cda.env ?? {};
  const arangoContainer = cda.target?.arangoContainer ?? env.LOOM_ARANGO_CONTAINER;
  const generation = cda.target?.fixtureGeneration ?? cda.report.target?.generation;
  assert.equal(generation, 'cda-fhir-v1');
  cda.report.errors ??= [];
  cda.report.nativeRequests ??= [];
  const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  let explorerRoot = `${root}/${encodeURIComponent(explorer)}`;
  let base = `${explorerRoot}/authoring/v2`;
  const report = {
    explorer, requestedExplorerName, evidence, target: cda.target, cases: [], errors: cda.report.errors,
    requests: [], nativeRequests: cda.report.nativeRequests, started: new Date().toISOString(),
  };
  let builder, outputId;
  const click = (...args) => cda.click(...args);
  const selectOption = (...args) => cda.selectOption(...args);
  const fill = (...args) => cda.fill(...args);
  const press = (...args) => cda.press(...args);
  const browserEval = (...args) => cda.inspect(...args);
  const waitForBrowser = (...args) => cda.wait(...args);
  const navigate = (...args) => cda.navigate(...args);
  const recordLifecycleCheck = (dimension, name, passed, checkEvidence = {}) =>
    cda.check(dimension, name, passed, checkEvidence);
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
  const requestId = `group-add-fields-browser-${randomUUID()}`;
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ requestId, path, ...(body ? { body: sanitizeReportValue(body) } : {}), status: response.status, response: sanitizeReportValue(value) });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const rawQuery = query => {
  assert(query.includes(JSON.stringify(project)), 'Raw oracle query must scope to the CDA project');
  assert(query.includes(JSON.stringify(generation)), 'Raw oracle query must scope to the CDA generation');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const proposal = async (name, start, expectedRows) => {
  await waitForBrowser(() => (['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await browserEval(() => { const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))}; });
  assert.equal(result.status, 'ready', result.text);
  assertVisibleRowsMatchOracle(result.rows, expectedRows, { label: `${name} preview` });
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
  return result;
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async expectedRows => {
  const start = Date.now();
  await click( '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async (expectedRows, expectedHeader) => {
  const start = Date.now();
  const url = new URL('/', uiOrigin);
  url.searchParams.set('project', project);
  url.searchParams.set('explorer', explorer);
  url.searchParams.set('mode', 'builder');
  await navigate(url.toString());
  await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` });
  await click( `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows, expectedHeader);
  recordRender('load-to-render', start);
};
const rendered = async (expectedRows, expectedHeader) => {
  await waitForBrowser(({ rowCount, columnCount, header }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const headers = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
      .map(cell => cell.innerText.trim());
    return table?.getAttribute('aria-rowcount') === String(Math.min(25, rowCount) + 1) &&
      table?.getAttribute('aria-colcount') === String(columnCount) &&
      (!header || headers.includes(header)) &&
      !document.body.innerText.includes('Loading your table…') &&
      !document.body.innerText.includes('Preview failed:');
  }, { rowCount: expectedRows.length, columnCount: expectedRows[0]?.length ?? 2, header: expectedHeader });
  const view = await browserEval(() => ({
    headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
      .map(cell => cell.innerText.trim()),
    rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
      .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
  }));
  const rows = view.rows;
  if (expectedHeader) assert(view.headers.includes(expectedHeader), `Rendered table omitted ${expectedHeader}`);
  const savedRows = expectedRows;
  assertVisibleRowsMatchOracle(rows, savedRows, { label: 'saved table' });
  return view;
};
try {
  const query = `
FOR source IN (
  FOR candidate IN Specimen
    FILTER candidate.project == ${JSON.stringify(project)} AND candidate.dataset_generation == ${JSON.stringify(generation)}
      AND candidate.resourceType == "Specimen" AND candidate.payload.resourceType == "Specimen"
    SORT candidate.id
    LIMIT 2000
    RETURN {id:candidate.id,_id:candidate._id,generation:candidate.dataset_generation,resourceType:candidate.resourceType}
)
  LET patients = (
    FOR edge IN fhir_edge
      FILTER edge._from == source._id AND edge._to != null AND edge.label == "subject_Patient"
        AND edge.from_type == "Specimen" AND edge.to_type == "Patient"
        AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
      LET patient = DOCUMENT(edge._to)
      FILTER patient != null AND patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == ${JSON.stringify(generation)}
        AND patient.resourceType == "Patient" AND patient.payload.resourceType == "Patient"
      RETURN DISTINCT {id:patient.id,_id:patient._id,resourceType:patient.resourceType,generation:patient.dataset_generation}
  )
  FILTER LENGTH(patients) == 1
  LET patient = patients[0]
  LET observationKeys = (
    FOR edge IN fhir_edge
      FILTER edge._to == patient._id AND edge.label == "subject_Patient"
        AND edge.from_type == "Observation" AND edge.to_type == "Patient"
        AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = edge._from
      SORT observationKey
      LIMIT 25
      RETURN observationKey
  )
  FILTER LENGTH(observationKeys) >= 2 AND LENGTH(observationKeys) <= 24
  LET observations = (
    FOR observationKey IN observationKeys
      LET observation = DOCUMENT(observationKey)
      FILTER observation != null AND observation.project == ${JSON.stringify(project)}
        AND observation.dataset_generation == ${JSON.stringify(generation)}
        AND observation.resourceType == "Observation" AND observation.payload.resourceType == "Observation"
      SORT observation.id
      RETURN {id:observation.id,_id:observation._id,resourceType:observation.resourceType,generation:observation.dataset_generation}
  )
  FILTER LENGTH(observations) == LENGTH(observationKeys)
  LIMIT 1
  RETURN {source,patient,observations}
`;
  const [witness] = rawQuery(query);
  assert(witness?.source?.id && witness.source._id && witness.patient?.id && witness.observations?.length >= 2,
    'Bounded raw CDA scan must find one Specimen, one related Patient, and 2–24 related Observations');
  const source = witness.source;
  const patient = witness.patient;
  const observations = witness.observations;
  const observationIDs = observations.map(observation => observation.id);
  assert.equal(source.generation, generation);
  assert.equal(source.resourceType, 'Specimen');
  assert.equal(patient.generation, generation);
  assert.equal(patient.resourceType, 'Patient');
  assert.equal(new Set(observationIDs).size, observations.length, 'Raw Observation witness IDs must be unique');
  assert(observations.every(observation => observation.generation === generation && observation.resourceType === 'Observation'));
  report.oracle = {
    kind: 'bounded project/generation-scoped raw fhir_edge witness',
    query,
    searchBounds: { sortedSpecimens: 2000, relatedObservationDiscoveryLimit: 25, relatedObservationsMin: 2, relatedObservationsMax: 24 },
    source, patient, observations, chain: [],
  };
  recordLifecycleCheck('correctness',
    'Bounded raw CDA oracle selects one Specimen, one Patient, and exact related Observation identities',
    observations.length >= 2 && observations.length <= 24 && new Set(observationIDs).size === observations.length,
    { project, generation, sourceID: source.id, patientID: patient.id, observationIDs });

  const fixtureSetupStartedAt = Date.now();
  const createdExplorer = await api(root, { name: requestedExplorerName, title: 'Group Add fields QA' });
  const creationRequest = report.requests.at(-1);
  assert.equal(creationRequest?.path, root);
  assert.equal(creationRequest?.status, 201);
  assert.equal(creationRequest?.body?.name, requestedExplorerName);
  const createdScope = createdExplorerScope(project, createdExplorer);
  explorer = createdScope.explorerId;
  explorerRoot = createdScope.explorerRoot;
  base = createdScope.authoringBase;
  report.explorer = explorer;
  report.target = { ...report.target, explorer };
  if (cda.report.target) cda.report.target.explorer = explorer;
  report.explorerProvisioning = {
    requestedName: requestedExplorerName,
    createRequestId: creationRequest.requestId,
    createStatus: creationRequest.status,
    returnedProject: createdExplorer.project,
    returnedExplorerId: explorer,
  };
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  assert(node?.nodeId, 'The CDA catalog must expose a Specimen table root');
  await command([{ type: 'CREATE_TABLE', title: 'Related Group QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  assert(field, 'CDA catalog must advertise Specimen.id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(explorerRoot + '/selections', { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: 'Specimen', id: source.id }] } } });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(source.generation, selection.generation);
  assert.equal(selection.resourceType, 'Specimen');
  assert.equal(selection.memberCount, 1);
  assert(selection.scopeDigest);
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct, 'The raw Specimen source must attach directly to the table');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  report.fixtureSetup = {
    kind: 'direct API setup before the native browser lifecycle',
    durationMs: Date.now() - fixtureSetupStartedAt,
    createdExplorer: { requestedName: requestedExplorerName, serverAssignedId: explorer },
    outputId,
    selection: { id: selection.id, project: selection.project, generation: selection.generation, memberCount: selection.memberCount },
    populationRoute: direct.route,
    apiRequests: report.requests.map(({ path, status }) => ({ path, status })),
  };
  const nativeCapture = cda.captureRequests(explorerRoot);
  let witnesses=[{anchor:source._id,values:[source.id]}];
  let expected=witnesses.map(w=>w.values);
  await open(expected);
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
    {from:'Patient',to:'Observation',label:'subject_Patient',field:'subject',direction:'INBOUND'},
  ];
  for(const hop of chain){
    const next=[];
    for(const witness of witnesses){
      const endpoint=hop.direction==='OUTBOUND'?'_from':'_to';
      const target=hop.direction==='OUTBOUND'?'_to':'_from';
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.from_type == ${JSON.stringify(hop.from)} AND e.to_type == ${JSON.stringify(hop.to)} AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)} FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project==${JSON.stringify(project)} AND d.dataset_generation==${JSON.stringify(generation)} AND d.resourceType==${JSON.stringify(hop.to)} AND d.payload.resourceType==${JSON.stringify(hop.to)} RETURN DISTINCT {id:d.id,_id:d._id}`;
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
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const expanded=builder;
  assert(expected.length>1,'CDA fixture must exercise many related records before grouping');
  const relatedPatientIDs = [...new Set(witnesses.map(witness => witness.values[1]))].sort();
  const relatedObservationIDs = [...new Set(witnesses.map(witness => witness.values[2]))].sort();
  assert.deepEqual(relatedPatientIDs, [patient.id], 'Native related route must retain the one raw Patient witness');
  assert.deepEqual(relatedObservationIDs, [...observationIDs].sort(), 'Native related rows must match the exact raw Observation identities');
  assert(!relatedObservationIDs.includes('—'), 'Every selected related Observation must resolve to a concrete raw record');
  recordLifecycleCheck('correctness',
    'Native Specimen→Patient→Observation rows match exact raw project/generation edges',
    relatedPatientIDs.length === 1 && relatedObservationIDs.length === observations.length,
    { patientID: patient.id, rawObservationIDs: [...observationIDs].sort(), nativeObservationIDs: relatedObservationIDs,
      chain: report.oracle.chain });
  const grouped=[[source.id,String(expected.length)]];
  await open(expected);
  const configureGroup=async()=>{
    await click('[data-testid="construction-rows-settings-trigger"]');
    await click('[data-testid="construction-action-group-rows"]');
    await waitForBrowser(() => (document.querySelector('input[aria-label="Group by Specimen ID"]:not(:disabled)')));
    start=Date.now();
    await click('input[aria-label="Group by Specimen ID"]');
  };
  let start;
  await configureGroup();
  const groupPreview = await proposal('related-many-group-preview',start,grouped);
  recordLifecycleCheck('correctness',
    'Group COUNT_ROWS proposal previews the exact raw Specimen key and Observation count',
    groupPreview.status === 'ready' && JSON.stringify(groupPreview.rows) === JSON.stringify(grouped),
    { rows: groupPreview.rows, expectedRows: grouped, observationCount: observations.length });
  start = Date.now();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expected);
  recordRender('group-cancel-to-render', start);
  const afterGroupCancel = await api(base + '/builder');
  assert.deepEqual(afterGroupCancel.workspace, expanded.workspace);
  recordLifecycleCheck('persistence',
    'Group Cancel preserves the exact expanded workspace and related rows',
    JSON.stringify(afterGroupCancel.workspace) === JSON.stringify(expanded.workspace),
    { draftVersion: afterGroupCancel.draftVersion, draftDigest: afterGroupCancel.draftDigest,
      rows: expected.length, workspace: afterGroupCancel.workspace });
  await configureGroup();
  await proposal('confirmed-related-many-group-preview',start,grouped);
  await apply(grouped);
  await open(grouped);
  const beforeField = structuredClone(builder);
  assert(doc(beforeField).construction.steps.some(step => step.operation.kind === 'GROUP'),
    'Applied table must persist a GROUP step before adding the source field');
  recordLifecycleCheck('persistence',
    'Applied Group and reload persist the exact grouped count and rows',
    doc(builder).construction.steps.some(step => step.operation.kind === 'GROUP') && grouped.length === 1,
    { groupedRows: grouped, construction: doc(builder).construction,
      draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  await click('[data-testid="construction-action-add-columns"]');
  await click('[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-add-columns-source"]')));
  report.addFieldsUI=await browserEval(() => { return {text:document.querySelector('[aria-label="Add columns editor"]').innerText,controls:[...document.querySelectorAll('[aria-label="Add columns editor"] input,[aria-label="Add columns editor"] select,[aria-label="Add columns editor"] button')].map(e=>({tag:e.tagName,label:e.getAttribute('aria-label'),testId:e.dataset.testid,text:e.innerText,disabled:e.disabled}))}; });
  assert.equal(source.resourceType,'Specimen');
  const withField=[[...grouped[0],source.resourceType]];
  const chooseField=async()=>{
    await click(groupAddFieldsRawFieldsSummarySelector);
    await waitForBrowser(() => (document.querySelector('input[aria-label="Select Specimen.resourceType"]:not(:disabled)')));
    start=Date.now();
    await click('input[aria-label="Select Specimen.resourceType"]');
    await click('[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
    await waitForBrowser(() => (['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)));
    const panel=await browserEval(() => { const e=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:e.dataset.proposalStatus,text:e.innerText}; });
    assert.equal(panel.status,'ready',panel.text);
    const rows=await browserEval(() => { return [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText)); });
    assert.deepEqual(rows,withField);
    recordRender('group-source-field-preview',start);
    return { ...panel, rows };
  };
  const fieldPreview = await chooseField();
  recordLifecycleCheck('correctness',
    'Specimen.resourceType choice preview matches the raw value on the grouped row',
    fieldPreview.status === 'ready' && JSON.stringify(fieldPreview.rows) === JSON.stringify(withField),
    { previewRows: fieldPreview.rows, expectedRows: withField, rawResourceType: source.resourceType });
  start = Date.now();
  await click('[data-testid="construction-choice-proposal-panel"] button',{name:'Cancel'});
  await rendered(grouped);
  recordRender('add-fields-cancel-to-render', start);
  const afterFieldCancel = await api(base + '/builder');
  assert.deepEqual(afterFieldCancel.workspace, beforeField.workspace);
  recordLifecycleCheck('persistence',
    'Add Columns Cancel preserves the exact saved Group workspace and rows',
    JSON.stringify(afterFieldCancel.workspace) === JSON.stringify(beforeField.workspace),
    { rows: grouped, draftVersion: afterFieldCancel.draftVersion, draftDigest: afterFieldCancel.draftDigest });
  await click(groupAddFieldsRawFieldsSummarySelector);
  await chooseField();
  start=Date.now();
  await click('[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-choice-proposal-panel"]')));
  await rendered(withField);
  recordRender('group-source-field-apply',start);
  builder=await api(base+'/builder');
  report.savedFieldDocument=doc(builder);
  const savedField = doc(builder).columns.find(column => column.source?.field?.path === 'resourceType');
  assert(savedField, 'Saved Group must contain the selected Specimen.resourceType field');
  assert.equal(savedField.occurrenceId, 'base');
  assert.equal(savedField.source?.kind, 'field');
  assert.equal(savedField.source?.field?.path, 'resourceType');
  assert.equal(savedField.source?.field?.projectionMode, 'VALUE');
  const savedFieldBinding = {
    columnId: savedField.column ?? savedField.id,
    label: savedField.label,
    occurrenceId: savedField.occurrenceId,
    source: structuredClone(savedField.source),
  };
  report.fieldBinding = savedFieldBinding;
  recordLifecycleCheck('persistence',
    'Applied Specimen.resourceType retains the exact direct field binding and grouped value',
    savedField.label === 'Resource Type' && savedFieldBinding.source.field.path === 'resourceType' &&
      savedFieldBinding.source.field.projectionMode === 'VALUE',
    { fieldBinding: savedFieldBinding, rows: withField });
  await open(withField, savedFieldBinding.label);
  builder = await api(base + '/builder');
  const reloadedField = doc(builder).columns.find(column => (column.column ?? column.id) === savedFieldBinding.columnId);
  assert(reloadedField);
  assert.equal(reloadedField.label, savedFieldBinding.label);
  assert.equal(reloadedField.occurrenceId, savedFieldBinding.occurrenceId);
  assert.deepEqual(reloadedField.source, savedFieldBinding.source);
  recordLifecycleCheck('persistence',
    'Reload preserves the Group and exact Specimen.resourceType binding and value',
    JSON.stringify(reloadedField.source) === JSON.stringify(savedFieldBinding.source) &&
      reloadedField.label === savedFieldBinding.label,
    { fieldBinding: { columnId: reloadedField.column ?? reloadedField.id, label: reloadedField.label,
      occurrenceId: reloadedField.occurrenceId, source: reloadedField.source }, rows: withField });
  const editedLabel = 'Specimen Resource Type';
  const renameBase = builder;
  const renameStartedAt = Date.now();
  const renameRequestStart = report.nativeRequests.length;
  await click('button', { name: 'Columns' });
  const labelSelector = `[aria-label=${JSON.stringify(`Column name for ${savedFieldBinding.label}`)}]`;
  await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: labelSelector }, 5000);
  await fill(labelSelector, editedLabel);
  await press(labelSelector, 'Enter');
  const matchesRenameRequest = entry =>
    entry.method === 'POST' && entry.path === `${base}/commands` &&
    entry.body?.expectedDraftVersion === renameBase.draftVersion &&
    entry.body?.expectedDraftDigest === renameBase.draftDigest &&
    entry.body?.commands?.some(change =>
      (change.type === 'UPDATE_COLUMN' && change.column === savedFieldBinding.columnId &&
        change.columnValue?.label === editedLabel) ||
      (change.type === 'UPDATE_CONSTRUCTION_OUTPUT' && change.constructionOutput?.columnId === savedFieldBinding.columnId &&
        change.constructionOutput?.label === editedLabel));
  const renameDeadline = renameStartedAt + 5000;
  const renameRequest = await nativeCapture.waitFor(matchesRenameRequest, {
    fromIndex: renameRequestStart, timeoutMs: Math.max(1, renameDeadline - Date.now()),
  });
  const rename = await cda.waitForCapturedResponse(nativeCapture, entry => entry === renameRequest,
    Math.max(1, renameDeadline - Date.now()));
  assert.equal(rename.status, 200, JSON.stringify(rename.response));
  const renameCommand = rename.body.commands.find(change =>
    (change.type === 'UPDATE_COLUMN' && change.column === savedFieldBinding.columnId) ||
    (change.type === 'UPDATE_CONSTRUCTION_OUTPUT' && change.constructionOutput?.columnId === savedFieldBinding.columnId));
  assert(renameCommand, 'The native rename command must target the exact saved field column');
  assert.equal(rename.body.expectedDraftVersion, renameBase.draftVersion);
  assert.equal(rename.body.expectedDraftDigest, renameBase.draftDigest);
  builder = await api(base + '/builder');
  const renamedField = doc(builder).columns.find(column => (column.column ?? column.id) === savedFieldBinding.columnId);
  assert.equal(renamedField?.label, editedLabel);
  assert.deepEqual(renamedField?.source, savedFieldBinding.source,
    'Editing the display label must preserve the exact Specimen.resourceType source binding');
  await rendered(withField, editedLabel);
  recordRender('native-label-edit-to-exact-rows', renameStartedAt);
  report.labelEdit = {
    requestId: rename.requestId,
    status: rename.status,
    command: renameCommand,
    beforeDraftVersion: renameBase.draftVersion,
    beforeDraftDigest: renameBase.draftDigest,
    afterDraftVersion: builder.draftVersion,
    afterDraftDigest: builder.draftDigest,
  };
  await open(withField, editedLabel);
  builder = await api(base + '/builder');
  const reloadedRenamedField = doc(builder).columns.find(column => (column.column ?? column.id) === savedFieldBinding.columnId);
  assert.equal(reloadedRenamedField?.label, editedLabel);
  assert.deepEqual(reloadedRenamedField?.source, savedFieldBinding.source);
  recordLifecycleCheck('persistence',
    'Native label edit preserves the same Specimen.resourceType column and binding after reload',
    (reloadedRenamedField.column ?? reloadedRenamedField.id) === savedFieldBinding.columnId &&
      reloadedRenamedField.label === editedLabel &&
      JSON.stringify(reloadedRenamedField.source) === JSON.stringify(savedFieldBinding.source),
    { fieldBinding: { columnId: reloadedRenamedField.column ?? reloadedRenamedField.id,
      label: reloadedRenamedField.label, occurrenceId: reloadedRenamedField.occurrenceId,
      source: reloadedRenamedField.source }, rows: withField });
  await click('button',{name:'Columns'});
  start=Date.now();
  await click(`button[aria-label=${JSON.stringify(`Remove ${editedLabel} column`)}]`);
  await rendered(grouped);
  recordRender('remove-group-source-field',start);
  builder=await api(base+'/builder');
  await open(grouped);
  builder=await api(base+'/builder');
  assert.deepEqual(doc(builder).construction,doc(beforeField).construction);
  assert.deepEqual(doc(builder).columns,doc(beforeField).columns,
    'Removing the edited source field must restore the original Group columns');
  recordLifecycleCheck('persistence',
    'Removing the edited field and reloading restores the exact Group construction and raw rows',
    JSON.stringify(doc(builder).construction) === JSON.stringify(doc(beforeField).construction) &&
      JSON.stringify(doc(builder).columns) === JSON.stringify(doc(beforeField).columns),
    { construction: doc(builder).construction, columns: doc(builder).columns, rows: grouped });
  assert.deepEqual(report.errors,[]);
  const lifecycleCheckpointDurations = report.cases.map(({ name, durationMs }) => ({ name, durationMs }));
  const maximumCheckpointDurationMs = Math.max(0, ...lifecycleCheckpointDurations.map(checkpoint => checkpoint.durationMs));
  const nativeActions = cda.report.actions ?? [];
  const failedActions = nativeActions.filter(action => action.status !== 'passed' || action.elapsedMs > 5000);
  const slowCheckpoints = lifecycleCheckpointDurations.filter(checkpoint => checkpoint.durationMs > 5000);
  const maximumNativeActionDurationMs = Math.max(0, ...nativeActions.map(action => action.elapsedMs));
  report.performance = {
    nativeActionCount: nativeActions.length,
    maximumNativeActionDurationMs,
    lifecycleCheckpointDurations,
    maximumCheckpointDurationMs,
  };
  recordLifecycleCheck('performance',
    'All native Group-add-fields lifecycle actions complete within five seconds',
    nativeActions.length > 0 && lifecycleCheckpointDurations.length > 0 &&
      failedActions.length === 0 && slowCheckpoints.length === 0,
    { nativeActionCount: nativeActions.length, maximumNativeActionDurationMs, failedActions,
      lifecycleCheckpointDurations, maximumCheckpointDurationMs });
  recordLifecycleCheck('correctness',
    'No unexpected browser, authoring, transport, or native HTTP errors occur',
    report.errors.length === 0,
    { errors: report.errors });
  report.status='passed';
  assert.deepEqual(cda.report.errors, [], 'CDA fixture must observe no unexpected browser diagnostics');
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.savedBuilderAtFailure = await api(base + '/builder').catch(readError => ({ readError: String(readError) }));
  report.failureUI = await browserEval(() => document.body.innerText).catch(String);
  throw error;
} finally {
  report.finished = new Date().toISOString();
  await cda.attachReport('group-add-fields', report);
}
return report;
}
