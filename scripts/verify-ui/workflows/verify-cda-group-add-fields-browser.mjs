import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { assertVisibleRowsMatchOracle } from '../helpers/cda-row-oracle.mjs';

export const groupAddFieldsRawFieldsSummarySelector = '[data-testid="feature-catalog-raw-fields"] > summary';

export async function runGroupAddFieldsBrowserWorkflow({ page, cda }) {
  const project = cda.project;
  assert(project, 'CDA fixture must provide the isolated project');
  const explorer = cda.explorer;
  const evidence = cda.evidence;
  const apiOrigin = cda.apiOrigin;
  const uiOrigin = cda.uiOrigin;
  const env = cda.env ?? {};
  const arangoContainer = cda.target?.arangoContainer ?? env.LOOM_ARANGO_CONTAINER;
  cda.report.errors ??= [];
  cda.report.nativeRequests ??= [];
  const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { explorer, evidence, target: cda.target, cases: [], errors: cda.report.errors, requests: [], nativeRequests: cda.report.nativeRequests, started: new Date().toISOString() };
let builder, outputId;
const click = (...args) => cda.click(...args);
const selectOption = (...args) => cda.selectOption(...args);
const fill = (...args) => cda.fill(...args);
const browserEval = (...args) => cda.inspect(...args);
const waitForBrowser = (...args) => cda.wait(...args);
const navigate = (...args) => cda.navigate(...args);
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
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `group-add-fields-browser-${randomUUID()}` },
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
const apply = async expectedRows => {
  const start = Date.now();
  await click( '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate( `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` });
  await click( `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows);
  recordRender('load-to-render', start);
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
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation,resourceType:s.resourceType}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Group Add fields QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Group QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  cda.captureRequests(`${root}/${explorer}`);
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',arangoContainer,'arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
    assert.equal(r.status,0,r.stderr);return JSON.parse(r.stdout.slice(r.stdout.indexOf('[')));
  };
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
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`;
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
  await proposal('related-many-group-preview',start,grouped);
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await configureGroup();
  await proposal('confirmed-related-many-group-preview',start,grouped);
  await apply(grouped);
  await open(grouped);
  await click('[data-testid="construction-action-add-columns"]');
  await click('[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-add-columns-source"]')));
  report.addFieldsUI=await browserEval(() => { return {text:document.querySelector('[aria-label="Add columns editor"]').innerText,controls:[...document.querySelectorAll('[aria-label="Add columns editor"] input,[aria-label="Add columns editor"] select,[aria-label="Add columns editor"] button')].map(e=>({tag:e.tagName,label:e.getAttribute('aria-label'),testId:e.dataset.testid,text:e.innerText,disabled:e.disabled}))}; });
  assert.equal(source.resourceType,'Specimen');
  const withField=[[...grouped[0],source.resourceType]];
  const beforeField=builder;
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
  };
  await chooseField();
  await click('[data-testid="construction-choice-proposal-panel"] button',{name:'Cancel'});
  await rendered(grouped);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeField.workspace);
  await click(groupAddFieldsRawFieldsSummarySelector);
  await chooseField();
  start=Date.now();
  await click('[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-choice-proposal-panel"]')));
  await rendered(withField);
  recordRender('group-source-field-apply',start);
  builder=await api(base+'/builder');
  report.savedFieldDocument=doc(builder);
  await open(withField);
  assert.deepEqual(report.errors,[]);
  await click('button',{name:'Columns'});
  start=Date.now();
  await click('button[aria-label="Remove Resource Type column"]');
  await rendered(grouped);
  recordRender('remove-group-source-field',start);
  builder=await api(base+'/builder');
  await open(grouped);
  assert.deepEqual(doc(builder).construction,doc(beforeField).construction);
  assert.deepEqual(report.errors,[]);
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
