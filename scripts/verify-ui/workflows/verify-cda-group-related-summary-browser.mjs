import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { sanitizeReportPayload } from '../helpers/playwright-browser.mjs';

export async function runGroupRelatedSummaryBrowserWorkflow({ page, cda }) {
const env = cda.env ?? {};
const project = cda.project;
assert(project, 'CDA fixture must provide the isolated project');
const explorer = cda.explorer;
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
assert.notEqual(explorer, protectedExplorer, 'The protected full-QA Explorer must remain untouched');
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = cda.target?.arangoContainer ?? env.LOOM_ARANGO_CONTAINER;
const groupedRelatedFilter = env.LOOM_GROUPED_RELATED_FILTER === '1';
const groupedFilterDirect = env.LOOM_GROUPED_FILTER_DIRECT === '1';
assert(!groupedFilterDirect || groupedRelatedFilter, 'Direct grouped filtering requires LOOM_GROUPED_RELATED_FILTER=1');
const expandAfterGroup = env.LOOM_EXPAND_AFTER_GROUP === '1';
assert(!groupedRelatedFilter || expandAfterGroup, 'Grouped related filtering requires LOOM_EXPAND_AFTER_GROUP=1');
const zeroMatches = env.LOOM_RELATED_ZERO === '1';
const absentID = `loom-summary-no-match-${randomUUID()}`;
const resultForm = env.LOOM_RELATED_FORM ?? 'COUNT';
assert(['COUNT','PRESENCE'].includes(resultForm), 'LOOM_RELATED_FORM must be COUNT or PRESENCE');
const collectionRoundTrip = env.LOOM_COLLECTION_ROUND_TRIP === '1';
const unmappedMemberRepair = env.LOOM_UNMAPPED_MEMBER_REPAIR === '1';
const upstreamGroupEdit = env.LOOM_UPSTREAM_GROUP_EDIT === '1';
const summaryShape = env.LOOM_SUMMARY_SHAPE ?? 'GROUP';
assert(['GROUP','PIVOT','UNPIVOT'].includes(summaryShape), 'LOOM_SUMMARY_SHAPE must be GROUP, PIVOT, or UNPIVOT');
assert(!unmappedMemberRepair || (!groupedRelatedFilter && !groupedFilterDirect && !expandAfterGroup && !collectionRoundTrip && !upstreamGroupEdit && summaryShape === 'GROUP' && resultForm === 'COUNT' && !zeroMatches), 'Unmapped-member repair is a standalone GROUP→related COUNT lifecycle');
assert(!collectionRoundTrip || (summaryShape === 'GROUP' && !upstreamGroupEdit && !zeroMatches && resultForm === 'COUNT'), 'Collection round trip requires unchanged Group and positive COUNT');
assert(!upstreamGroupEdit || summaryShape === 'GROUP', 'Upstream Group edit requires GROUP shape');
assert.equal(project, 'loom_dev_cda_fhir', 'This verifier is bound to the CDA-FHIR project');
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = base.replace('/authoring/v2','/selections');
const report = { evidence, target: cda.target, groupedFilterDirect, groupedRelatedFilter, expandAfterGroup, collectionRoundTrip, unmappedMemberRepair, upstreamGroupEdit, summaryShape, resultForm, zeroMatches, explorer, protectedExplorerUntouched: true, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString() };
let nativeCapture, builder, outputId, source, initialSelection;
const click = (_page, ...args) => cda.click(...args);
const selectOption = (_page, ...args) => cda.selectOption(...args);
const fill = (_page, ...args) => cda.fill(...args);
const browserEval = (_page, ...args) => cda.inspect(...args);
const waitForBrowser = (_page, predicate, timeoutOrArgs = 5000, args) => {
  const timeout = Array.isArray(timeoutOrArgs) ? 5000 : timeoutOrArgs;
  const waitArgs = Array.isArray(timeoutOrArgs) ? timeoutOrArgs : args;
  return cda.wait(predicate, waitArgs, timeout);
};
const navigate = (_page, ...args) => cda.navigate(...args);
const waitForControl = (_page, selector, { timeout = 5000, enabled = false, hidden = false } = {}) => cda.wait(
  ({ selector: targetSelector, enabled: requireEnabled, hidden: waitHidden }) => {
    const controls = [...document.querySelectorAll(targetSelector)];
    if (controls.length > 1) throw new Error(`Expected one control for ${targetSelector}, found ${controls.length}`);
    const control = controls[0];
    if (waitHidden) return !control || !control.getClientRects().length;
    return Boolean(control && control.getClientRects().length &&
      (!requireEnabled || (!control.disabled && control.getAttribute('aria-disabled') !== 'true')));
  }, { selector, enabled, hidden }, timeout,
);
const sanitizeText = value => String(value ?? '')
  .replaceAll(process.cwd(), '$CHECKOUT')
  .replace(/(?:file:\/\/)?\/(?:private\/)?tmp\/[^\s)]+/g, '$TMP/<path>')
  .replace(/\/Users\/[^/\s]+/g, '$HOME')
  .replace(/\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi, 'Bearer [REDACTED]')
  .replace(/\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\b/g, '[REDACTED_TOKEN]')
  .replace(/\bsk-[A-Za-z0-9]{16,}\b/g, '[REDACTED_TOKEN]');
const sanitizeBody = body => {
  const text = String(body ?? '').slice(0, 12000);
  try {
    const sanitize = (value, key = '') => {
      if (/authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i.test(key)) return '[REDACTED]';
      if (typeof value === 'string') return sanitizeText(value);
      if (Array.isArray(value)) return value.map(item => sanitize(item));
      if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, sanitize(childValue, childKey)]));
      return value;
    };
    return JSON.stringify(sanitize(JSON.parse(text)));
  } catch {
    return sanitizeText(text);
  }
};
const failedResponses=[];
const networkRequests=new Map();
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `group-related-summary-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body: sanitizeReportPayload(body), status: response.status, response: sanitizeReportPayload(value) });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const rawQuery = query => {
  const result = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const proposal = async (name, start, expectedRows) => {
  const deadline = start + 5000;
  let response;
  while (!(response = report.nativeRequests.findLast(request =>
    /\/construction-(?:choice-)?proposals$/.test(request.path) &&
    request.startedAt >= start && request.completedAt && nativeCapture.rawResponseBody(request)))) {
    assert(Date.now() < deadline, `${name} did not complete a fresh proposal within five seconds`);
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  const protocolResponse = nativeCapture.rawResponseBody(response);
  if (response.status === 200 && protocolResponse.proposalId) {
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === args[0])); }, [protocolResponse.proposalId]);
  }
  await waitForBrowser(page, (args) => { return Boolean((['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus))); });
  const result = await browserEval(page, (args) => { const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))}; });
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const permitted = new Set(expectedRows.map(row=>JSON.stringify(row)));
  if(expandAfterGroup) assert.equal(new Set(result.rows.map(row=>JSON.stringify(row))).size,result.rows.length,'Grouped-source preview must not duplicate terminal rows');
  for (const row of result.rows) assert(permitted.has(JSON.stringify(row)), 'Preview row must match an independent CDA relationship witness: '+JSON.stringify(row));
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async (expectedRows, columnCount) => {
  const start = Date.now();
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  await waitForBrowser(page, (args) => { return Boolean((!document.body.innerText.includes('Loading your table…'))); });
  const deadline=start+5000;
  while(!report.nativeRequests.some(request=>request.path===base+'/preview'&&request.startedAt>=start&&request.completedAt&&request.status===200)) {
    assert(Date.now()<deadline,'Apply did not complete a fresh saved preview within five seconds');
    await new Promise(resolve=>setTimeout(resolve,50));
  }
  await rendered(expectedRows, columnCount);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async (expectedRows, columnCount) => {
  const start = Date.now();
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-table-' + args[0] + '"]'))); }, [outputId]);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false)); });
  await rendered(expectedRows, columnCount);
  recordRender('load-to-render', start);
};
const rendered = async (expectedRows, columnCount = expectedRows[0]?.length ?? 2) => {
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === args[0] && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === args[1] && !document.body.innerText.includes('Loading your table…'))); }, [String(Math.min(25, expectedRows.length) + 1), String(columnCount)]);
  const rows = await browserEval(page, (args) => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length); });
  if(expectedRows.length<=5) assert.equal(rows.length,expectedRows.length);
  else assert(rows.length>0&&rows.length<=Math.min(25,expectedRows.length),'Visible virtualized rows must fit the preview page');
  if(expandAfterGroup) assert.equal(new Set(rows.map(row=>JSON.stringify(row))).size,rows.length,'Grouped-source terminal union must not duplicate rows');
  const savedRows = expectedRows;
  for (const row of rows) assert(savedRows.some(expected=>row.length===expected.length&&row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
const assertSavedPreviewRows = async (expectedRows, { requestStart, expectedBuilder, phase }) => {
  assert(Number.isInteger(requestStart) && requestStart >= 0, `${phase} needs an explicit native-request start offset`);
  assert(expectedBuilder?.catalog?.snapshotToken, `${phase} needs the current Builder snapshot`);
  assert.equal(expectedBuilder.catalog.generation, source.generation);
  assert.equal(expectedBuilder.catalog.authorizationScopeDigest,initialSelection.scopeDigest,`${phase} must stay in the initially authorized scope`);
  const currentDocument=doc(expectedBuilder);
  assert.equal(currentDocument?.output?.id,outputId,`${phase} needs the current output document`);
  assert(currentDocument?.population?.selectionRevisionId,`${phase} needs a pinned source membership revision`);
  const deadline=Date.now()+5000;
  let activePreview;
  while(true){
    activePreview=await browserEval(page, (args) => { const p=document.querySelector('[data-testid="construction-preview"]');return {status:p?.dataset.previewStatus,receiptId:p?.dataset.previewReceiptId,outputId:p?.dataset.previewOutputId,draftVersion:p?.dataset.currentDraftVersion,draftDigest:p?.dataset.currentDraftDigest}; });
    if(activePreview.status==='ready'&&activePreview.receiptId&&activePreview.outputId===outputId&&activePreview.draftVersion===String(expectedBuilder.draftVersion)&&activePreview.draftDigest===expectedBuilder.draftDigest)break;
    assert(Date.now()<deadline,`${phase} did not render a preview for the current Builder draft within five seconds`);
    await new Promise(resolve=>setTimeout(resolve,50));
  }
  assert.equal(activePreview.status,'ready',`${phase} must show a ready saved preview`);
  assert(activePreview.receiptId,`${phase} must expose its currently displayed native receipt`);
  assert.equal(activePreview.outputId,outputId,`${phase} preview must be bound to the current output`);
  assert.equal(activePreview.draftVersion,String(expectedBuilder.draftVersion),`${phase} preview version must match the current Builder draft`);
  assert.equal(activePreview.draftDigest,expectedBuilder.draftDigest,`${phase} preview digest must match the current Builder draft`);
  let request;
  while(!(request=report.nativeRequests.slice(requestStart).find(entry => {
    const response = nativeCapture.rawResponseBody(entry);
    return entry.path===base+'/preview' && entry.status===200 && entry.completedAt &&
      entry.body?.receiptId===activePreview.receiptId && entry.body?.outputId===outputId &&
      response?.receiptId===activePreview.receiptId && response?.outputId===outputId;
  }))){
    assert(Date.now()<deadline,`${phase} did not complete a current-draft native saved preview within five seconds`);
    await new Promise(resolve=>setTimeout(resolve,50));
  }
  const nativePreviewResponse=nativeCapture.rawResponseBody(request);
  assert.equal(nativePreviewResponse.receiptId,activePreview.receiptId,`${phase} native response must be the receipt visible in the current preview`);
  assert.equal(nativePreviewResponse.outputId,outputId,`${phase} native response must be bound to the current output`);
  const preview=await api(base+'/preview',{receiptId:activePreview.receiptId,outputId,limit:100});
  assert.equal(preview.receiptId,activePreview.receiptId,`${phase} receipt-bound reread must use the current native receipt`);
  assert.equal(preview.outputId,outputId,`${phase} receipt-bound reread must use the current output`);
  assert.equal(preview.rowCount,expectedRows.length,'Receipt-bound saved preview row count must match the independent raw oracle');
  assert.equal(preview.rows.length,expectedRows.length,'Receipt-bound preview must return every expected group');
  const values=preview.rows.map(row=>preview.columns.map(column=>row[column.column]==null?'—':String(row[column.column])));
  assert.deepEqual(values.map(row=>JSON.stringify(row)).sort(),expectedRows.map(row=>JSON.stringify(row)).sort(),'Every receipt-bound saved row must match the independent project/generation raw oracle');
  report.unmappedRepairPreviewBindings??=[];
  report.unmappedRepairPreviewBindings.push({phase,requestStart,receiptId:activePreview.receiptId,outputId,selectionRevisionId:currentDocument.population.selectionRevisionId,snapshotToken:expectedBuilder.catalog.snapshotToken,generation:expectedBuilder.catalog.generation,authorizationScopeDigest:expectedBuilder.catalog.authorizationScopeDigest,draftVersion:expectedBuilder.draftVersion,draftDigest:expectedBuilder.draftDigest,nativeRequestIndex:report.nativeRequests.indexOf(request)});
  return preview;
};
const runUnmappedMemberRepair = async () => {
  report.gaps=['Native starting-collection replacement of arbitrary mapped membership is unavailable; this case only removes one independently proven unmapped member and expects the dataframe output to remain unchanged.'];
  const expectedBase=source.rootRows.map(row=>[row.id]);
  const expectedGrouped=report.oracle.expectedGroupedRows;
  const expectedRelated=report.oracle.expectedRelatedRows;
  const mappedRef={project,generation:source.generation,resourceType:'Specimen',id:source.sources[0].id};
  const unmappedRef={project,generation:source.generation,resourceType:'Specimen',id:source.sources[1].id};
  const sourceMembers=[mappedRef,unmappedRef].sort((a,b)=>a.id.localeCompare(b.id));
  const selectionPage=await api(`${selections}/${initialSelection.id}?limit=100`);
  assert.equal(initialSelection.scopeDigest,selectionPage.revision.scopeDigest,'Creation response and independently reread membership header must agree on authorization scope');
  assert.equal(selectionPage.revision.project,project);
  assert.equal(selectionPage.revision.generation,source.generation);
  assert.equal(selectionPage.revision.scopeDigest,builder.catalog.authorizationScopeDigest);
  assert.equal(selectionPage.revision.resourceType,'Specimen');
  assert.deepEqual(selectionPage.members.map(member=>member.ref).sort((a,b)=>a.id.localeCompare(b.id)),sourceMembers,'Initial selection must match the two independently scoped raw Specimen witnesses');
  const initialRawMembership=rawQuery(`FOR member IN loom_explorer_selection_members FILTER member.selectionId==${JSON.stringify(initialSelection.id)} AND member.project==${JSON.stringify(project)} AND member.generation==${JSON.stringify(source.generation)} AND member.resourceType=="Specimen" SORT member.id RETURN {project:member.project,generation:member.generation,resourceType:member.resourceType,id:member.id}`);
  assert.deepEqual(initialRawMembership,sourceMembers,'Independent raw membership must contain the mapped Specimen and the orphan');
  report.oracle.initialSelectionMembership=initialRawMembership;

  await open(expectedBase,1);
  const configureGroup=async()=>{
    const started=Date.now();
    const waitWithinActionBudget=async selector=>{
      const remaining=5000-(Date.now()-started);
      assert(remaining>0,'Rows-to-Group discovery exceeded the five-second action-to-render budget');
      await page.locator(selector).waitFor({ state: 'visible', timeout: remaining });
    };
    await click(page,'[data-testid="construction-rows-settings-trigger"]');
    await waitWithinActionBudget('[data-testid="construction-action-group-rows"]:not(:disabled)');
    await click(page,'[data-testid="construction-action-group-rows"]');
    await waitWithinActionBudget('input[aria-label="Group by Observation ID"]:not(:disabled)');
    await click(page,'input[aria-label="Group by Observation ID"]');
    return started;
  };
  let started=await configureGroup();
  await proposal('unmapped-repair-group-cancel-preview',started,expectedGrouped);
  await click(page,'[data-testid="construction-cancel-proposal"]');
  await rendered(expectedBase,1);
  assert.deepEqual((await api(base+'/builder')).workspace,builder.workspace,'Cancel must leave the source population and original Observation rows unchanged');
  started=await configureGroup();
  await proposal('unmapped-repair-group-preview',started,expectedGrouped);
  await apply(expectedGrouped,2);
  await open(expectedGrouped,2);
  builder=await api(base+'/builder');
  const groupedDocument=doc(builder);
  const groupStep=groupedDocument.construction.steps.find(step=>step.operation.kind==='GROUP');
  assert(groupStep,'Native grouping must be saved before adding the related field');
  report.unmappedRepairGroupStep=groupStep;

  const openRelatedFieldChooser=async()=>{
    await click(page,'[data-testid="construction-action-add-columns"]');
    await click(page,'[aria-label="Column types"] button',{includes:'Fields and related data'});
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-add-columns-source"]'))); });
    if(!await browserEval(page, (args) => { return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open===true; })){
      await click(page,'[aria-label="Related resources"] summary');
    }
    await click(page,'[data-testid="construction-add-columns-source-option"][aria-label="Specimen, Related resource"]');
    if(!await browserEval(page, (args) => { return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open===true; })){
      await click(page,'[data-testid="feature-catalog-raw-fields"] summary');
    }
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Select Specimen.id"]:not(:disabled)'))); });
  };
  const configureRelatedField=async name=>{
    const discoveryStart=Date.now();
    await openRelatedFieldChooser();
    await click(page,'input[aria-label="Select Specimen.id"]');
    await click(page,'[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[role="dialog"]'))); });
    if(!await browserEval(page, (args) => { return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open===true; })){
      await click(page,'[role="dialog"] summary',{includes:'Other relationship paths'});
    }
    const relationship='Observation -[specimen]-> Specimen';
    const pathSelector=`[role="dialog"] input[aria-label=${JSON.stringify(`Specimen ID: ${relationship}`)}]`;
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0]))); }, [pathSelector]);
    await click(page,pathSelector);
    const started=Date.now();
    await click(page,'[role="dialog"] input[aria-label="Specimen ID: Count matching records"]');
    await click(page,'[role="dialog"] button',{name:'Add 1 column'});
    await waitForBrowser(page, (args) => { return Boolean((['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus) || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus))); });
    const proposalState=await browserEval(page, (args) => { const p=document.querySelector('[data-testid="construction-proposal-panel"]')??document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:p?.dataset.proposalStatus,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}; });
    assert.equal(proposalState.status,'ready',proposalState.text);
    assert.deepEqual(proposalState.rows,expectedRelated,`${name} preview must match each exact scoped Observation→Specimen raw count`);
    recordRender(name+'-field-discovery-to-preview',discoveryStart);
    recordRender(name+'-choice-to-preview',started);
    return await browserEval(page, (args) => { return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?'construction-choice-proposal-panel':'construction-proposal-panel'; });
  };
  const beforeRelated=builder;
  let panel=await configureRelatedField('unmapped-repair');
  await click(page,`[data-testid="${panel}"] button`,{name:panel==='construction-choice-proposal-panel'?'Cancel':'Cancel'});
  await rendered(expectedGrouped,2);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeRelated.workspace,'Canceling the related source proposal must preserve the saved Group');
  panel=await configureRelatedField('unmapped-repair-confirmed');
  const applyStarted=Date.now();
  if(panel==='construction-choice-proposal-panel') await click(page,'[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  else await click(page,'[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="' + args[0] + '"]'))); }, [panel]);
  await rendered(expectedRelated,3);
  recordRender('unmapped-repair-group-related-save-to-render',applyStarted);
  builder=await api(base+'/builder');
  const beforeRepair=doc(builder);
  const groupBefore=beforeRepair.construction.steps.find(step=>step.id===groupStep.id);
  const relatedStep=beforeRepair.construction.steps.find(step=>step.operation.kind==='RELATED_SOURCE');
  assert(groupBefore&&relatedStep,'The saved document must contain GROUP followed by RELATED_SOURCE');
  assert.equal(beforeRepair.construction.steps.indexOf(groupBefore)<beforeRepair.construction.steps.indexOf(relatedStep),true);
  assert.equal(relatedStep.operation.relatedSource.form,'COUNT');
  assert.equal(relatedStep.operation.relatedSource.source.resourceType,'Specimen');
  assert.equal(relatedStep.operation.relatedSource.source.path,'id');
  assert.equal(relatedStep.operation.relatedSource.route.length,1);
  assert.equal(relatedStep.operation.relatedSource.route[0].fromResourceType,'Observation');
  assert.equal(relatedStep.operation.relatedSource.route[0].toResourceType,'Specimen');
  const savedConstruction=beforeRepair.construction;
  const savedColumns=beforeRepair.columns;
  const savedPopulation=beforeRepair.population;
  const beforeRepairBuilder=builder;
  const beforeRevision=initialSelection.id;
  const baselinePreviewRequestStart=report.nativeRequests.length;
  await open(expectedRelated,3);
  builder=await api(base+'/builder');
  const baselineReceiptPreview=await assertSavedPreviewRows(expectedRelated,{requestStart:baselinePreviewRequestStart,expectedBuilder:builder,phase:'before-unmapped-repair'});
  await click(page,'[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('section[aria-label="Starting collection"] button')?.innerText)); });
  await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Check selected-resource coverage'&&!button.disabled))); });
  const coverageStarted=Date.now();
  await click(page,'section[aria-label="Starting collection"] button',{name:'Check selected-resource coverage'});
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="population-coverage-report"]'))); });
  const coverage=await browserEval(page, (args) => { return document.querySelector('[data-testid="population-coverage-report"]')?.innerText??''; });
  assert(coverage.includes('2 selected · 1 produce rows · 1 needs attention'),coverage);
  assert(coverage.includes(unmappedRef.id),`Coverage must surface exact raw orphan ${source.sources[1].id}: ${coverage}`);
  await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('[data-testid="population-coverage-report"] button')].some(button=>button.innerText==='Remove from collection'&&!button.disabled))); });
  recordRender('unmapped-repair-coverage-render',coverageStarted);
  const targetLabel=`Specimen/${unmappedRef.id}`;
  const coverageRows=await browserEval(page, (args) => { return [...document.querySelectorAll('[data-testid="population-coverage-report"] li')].map(item=>({member:item.querySelector('span')?.innerText.trim(),action:item.querySelector('button')?.innerText.trim()})); });
  assert.deepEqual(coverageRows,[{member:targetLabel,action:'Remove from collection'}],'Native repair must offer removal only for the independently proven unmapped Specimen');
  const removeStarted=Date.now();
  await click(page,'[data-testid="population-coverage-report"] button',{name:'Remove from collection'});
  await waitForBrowser(page, (args) => { return Boolean(((()=>{const section=document.querySelector('section[aria-label="Starting collection"]');const revision=section?.dataset.attachedSelectionRevisionId;return Boolean(section&&revision&&revision!==args[0]&&!document.body.innerText.includes('Loading your table…'));})())); }, [beforeRevision]);
  const attachedRevisionID=await browserEval(page, (args) => { return document.querySelector('section[aria-label="Starting collection"]')?.dataset.attachedSelectionRevisionId??''; });
  assert(typeof attachedRevisionID==='string'&&attachedRevisionID.length>0&&attachedRevisionID!==beforeRevision,'Native removal must leave an existing Starting collection section attached to a new nonempty immutable revision');
  await rendered(expectedRelated,3);
  recordRender('unmapped-repair-selection-change-to-render',removeStarted);
  builder=await api(base+'/builder');
  const revised=doc(builder);
  const revisedSelectionID=revised.population.selectionRevisionId;
  assert.notEqual(revisedSelectionID,beforeRevision,'Native removal must create and attach a new immutable selection revision');
  assert.equal(revisedSelectionID,attachedRevisionID,'The saved Builder must attach the exact immutable revision shown by the native Starting collection control');
  assert(builder.draftVersion>beforeRepairBuilder.draftVersion,'Native membership repair must advance the Builder draft version');
  assert.notEqual(builder.draftDigest,beforeRepairBuilder.draftDigest,'Native membership repair must change the Builder draft digest');
  assert.equal(builder.catalog.generation,source.generation);
  assert.deepEqual(revised.population.route,savedPopulation.route,'The exact source route must survive revision replacement');
  assert.deepEqual(revised.construction,savedConstruction,'Excluding the unmapped resource must preserve the complete GROUP and RELATED_SOURCE operations');
  assert.deepEqual(revised.columns,savedColumns,'Physical output labels and stable column IDs must remain unchanged');
  const revisedPage=await api(`${selections}/${revisedSelectionID}?limit=100`);
  assert.equal(revisedPage.revision.id,revisedSelectionID);
  assert.equal(revisedPage.revision.project,project);
  assert.equal(revisedPage.revision.generation,source.generation);
  assert.equal(revisedPage.revision.scopeDigest,builder.catalog.authorizationScopeDigest);
  assert.equal(revisedPage.revision.resourceType,'Specimen');
  assert.equal(revisedPage.revision.source.kind,'SELECTION_REVISION');
  assert.equal(revisedPage.revision.source.revisionId,beforeRevision);
  assert.equal(revisedPage.revision.source.membershipDigest,initialSelection.membershipDigest);
  assert.equal(revisedPage.revision.memberCount,1);
  assert.deepEqual(revisedPage.revision.exclusions,[unmappedRef]);
  assert.deepEqual(revisedPage.members.map(member=>member.ref),[mappedRef],'The new revision must retain exactly the mapped raw source member');
  const revisedRawMembership=rawQuery(`FOR member IN loom_explorer_selection_members FILTER member.selectionId==${JSON.stringify(revisedSelectionID)} AND member.project==${JSON.stringify(project)} AND member.generation==${JSON.stringify(source.generation)} AND member.resourceType=="Specimen" SORT member.id RETURN {project:member.project,generation:member.generation,resourceType:member.resourceType,id:member.id}`);
  assert.deepEqual(revisedRawMembership,[mappedRef],'Independent Arango membership must contain only the selected mapped Specimen');
  report.unmappedRepair={initialSelectionRevisionId:beforeRevision,selectionRevisionId:revisedSelectionID,sourceMembershipBefore:initialRawMembership,sourceMembershipAfter:revisedRawMembership,coverage,constructionPreserved:true,columnsPreserved:true,routePreserved:true,expectedRows:expectedRelated,baselineReceiptPreview,changedContributingMembershipTested:false};
  const reloadedPreviewRequestStart=report.nativeRequests.length;
  await open(expectedRelated,3);
  builder=await api(base+'/builder');
  const reloaded=doc(builder);
  assert.equal(reloaded.population.selectionRevisionId,revisedSelectionID,'Reload must retain the new selection revision');
  assert.deepEqual(reloaded.population.route,savedPopulation.route);
  assert.deepEqual(reloaded.construction,savedConstruction);
  assert.deepEqual(reloaded.columns,savedColumns);
  assert.equal(builder.catalog.generation,source.generation);
  report.unmappedRepair.reloadedRows=expectedRelated;
  const reloadedPage=await api(`${selections}/${revisedSelectionID}?limit=100`);
  assert.deepEqual(reloadedPage.members.map(member=>member.ref),[mappedRef]);
  assert.deepEqual(rawQuery(`FOR member IN loom_explorer_selection_members FILTER member.selectionId==${JSON.stringify(revisedSelectionID)} AND member.project==${JSON.stringify(project)} AND member.generation==${JSON.stringify(source.generation)} AND member.resourceType=="Specimen" SORT member.id RETURN {project:member.project,generation:member.generation,resourceType:member.resourceType,id:member.id}`),[mappedRef]);
  report.unmappedRepair.fullReceiptPreview=await assertSavedPreviewRows(expectedRelated,{requestStart:reloadedPreviewRequestStart,expectedBuilder:builder,phase:'after-unmapped-repair-reload'});
};
try {
  if(unmappedMemberRepair){

    const query=`LET seeds=(FOR s IN Specimen FILTER s.resourceType=="Specimen" AND s.project=="${project}" AND s.dataset_generation=="cda-fhir-v1" SORT s.id LIMIT 2000 RETURN s) FOR s IN seeds LET parents=(FOR e IN fhir_edge FILTER e._from==s._id AND e.label=="parent" AND e.from_type=="Specimen" AND e.to_type=="Specimen" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" LET parent=DOCUMENT(e._to) FILTER parent!=null AND parent.resourceType=="Specimen" AND parent.project=="${project}" AND parent.dataset_generation=="cda-fhir-v1" RETURN parent._id) LET children=(FOR e IN fhir_edge FILTER e._to==s._id AND e.label=="parent" AND e.from_type=="Specimen" AND e.to_type=="Specimen" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" LET child=DOCUMENT(e._from) FILTER child!=null AND child.resourceType=="Specimen" AND child.project=="${project}" AND child.dataset_generation=="cda-fhir-v1" RETURN child.id) LET routeRows=(FOR parentEdge IN fhir_edge FILTER parentEdge._from==s._id AND parentEdge.label=="parent" AND parentEdge.from_type=="Specimen" AND parentEdge.to_type=="Specimen" AND parentEdge.project=="${project}" AND parentEdge.dataset_generation=="cda-fhir-v1" LET parent=DOCUMENT(parentEdge._to) FILTER parent!=null AND parent.resourceType=="Specimen" AND parent.project=="${project}" AND parent.dataset_generation=="cda-fhir-v1" FOR specimenEdge IN fhir_edge FILTER specimenEdge._to==parent._id AND specimenEdge.label=="specimen_Specimen" AND specimenEdge.from_type=="Observation" AND specimenEdge.to_type=="Specimen" AND specimenEdge.project=="${project}" AND specimenEdge.dataset_generation=="cda-fhir-v1" LET observation=DOCUMENT(specimenEdge._from) FILTER observation!=null AND observation.resourceType=="Observation" AND observation.project=="${project}" AND observation.dataset_generation=="cda-fhir-v1" RETURN observation.id) RETURN {id:s.id,_id:s._id,parents,children,routeRows}`;
    const candidates=rawQuery(query);
    const mapped=candidates.find(candidate=>candidate.parents.length>0&&candidate.routeRows.length>=1&&candidate.routeRows.length<=24&&new Set(candidate.routeRows).size===candidate.routeRows.length);
    const unmapped=candidates.find(candidate=>candidate.id!==mapped?.id&&candidate.parents.length===0&&candidate.children.length>0&&candidate.routeRows.length===0);
    const witnessCandidates=candidates.slice(0,12).map(({id,parents,children,routeRows})=>({id,parentCount:parents.length,childCount:children.length,routeRows:routeRows.length}));
    report.oracle={query,seedLimit:2000,candidateCount:candidates.length,witnessStatus:mapped&&unmapped?'found':'not-found',witnessCandidates};
    if(!mapped||!unmapped) throw Object.assign(new Error(`CDA_UNMAPPED_REPAIR_WITNESS_NOT_FOUND: bounded project/generation query found no mapped+unmapped Specimen pair; ${JSON.stringify(witnessCandidates)}`),{code:'CDA_UNMAPPED_REPAIR_WITNESS_NOT_FOUND'});
    const rows=rawQuery(`FOR o IN Observation FILTER o.id IN ${JSON.stringify(mapped.routeRows)} AND o.resourceType=="Observation" AND o.project=="${project}" AND o.dataset_generation=="cda-fhir-v1" SORT o.id LET targets=(FOR e IN fhir_edge FILTER e._from==o._id AND e.label=="specimen_Specimen" AND e.from_type=="Observation" AND e.to_type=="Specimen" AND e.project==o.project AND e.dataset_generation==o.dataset_generation LET specimen=DOCUMENT(e._to) FILTER specimen!=null AND specimen.resourceType=="Specimen" AND specimen.project==o.project AND specimen.dataset_generation==o.dataset_generation RETURN DISTINCT specimen.id) RETURN {id:o.id,_id:o._id,specimenIDs:SORTED_UNIQUE(targets)}`);
    assert.deepEqual(rows.map(row=>row.id).sort(),[...mapped.routeRows].sort(),'The raw root Observation oracle must exactly recover the mapped route rows');
    assert(rows.every(row=>row.specimenIDs.length>0),'Every grouped Observation witness must have at least one scoped related Specimen');
    source={generation:'cda-fhir-v1',sources:[mapped,unmapped],rootRows:rows};
    report.oracle={...report.oracle,mapped:{id:mapped.id,parentIDs:mapped.parents,rootObservationIDs:mapped.routeRows},unmapped:{id:unmapped.id,parentIDs:unmapped.parents,childIDs:unmapped.children},rootRows:rows,expectedGroupedRows:rows.map(row=>[row.id,'1']),expectedRelatedRows:rows.map(row=>[row.id,'1',String(row.specimenIDs.length)])};
    await api(root,{name:explorer,title:'Unmapped collection repair under Group QA'});
    builder=await api(base+'/builder');
    assert.equal(builder.catalog.generation,source.generation);
    const node=builder.catalog.nodes.find(item=>item.resourceType==='Observation');
    assert(node,'CDA catalog must expose Observation roots');
    await command([{type:'CREATE_TABLE',title:'Group related unmapped repair QA',rootNodeId:node.nodeId}]);
    outputId=builder.workspace.documents[0].output.id;
    const field=builder.catalog.candidates.find(candidate=>candidate.nodeId===node.nodeId&&candidate.fieldPath==='id');
    assert(field,'Observation ID must be available for exact base-row identity');
    await command([{type:'ADD_COLUMN',outputId,occurrenceId:'base',candidateId:field.candidateId,projectionMode:'VALUE',initialPresentation:'TABLE',title:'Observation ID'}]);
    initialSelection=await api(selections,{snapshotToken:builder.catalog.snapshotToken,idempotencyKey:explorer,source:{kind:'resources',resources:{refs:source.sources.map(member=>({project,generation:source.generation,resourceType:'Specimen',id:member.id}))}}});
    const routes=await api(base+'/population-routes',{snapshotToken:builder.catalog.snapshotToken,outputId,selectionRevisionId:initialSelection.id,limit:50});
    const route=routes.choices.find(choice=>choice.route.length===2&&choice.route[0].fromResourceType==='Observation'&&choice.route[0].toResourceType==='Specimen'&&choice.route[0].relationship==='specimen_Specimen'&&choice.route[0].storageDirection==='OUTBOUND'&&choice.route[1].relationship==='parent'&&choice.route[1].storageDirection==='INBOUND');
    assert(route,'The CDA mapped/unmapped pair must use the exact native Observation → Specimen → parent route: '+JSON.stringify(routes.choices.map(choice=>choice.route)));
    await command([{type:'SET_TABLE_POPULATION',outputId,selectionRevisionId:initialSelection.id,routeChoiceId:route.routeChoiceId}]);
    report.unmappedRepairFixture={selectionRevisionId:initialSelection.id,route:builder.workspace.documents[0].population.route,rootRows:source.rootRows.length};
  } else {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 FOR e IN fhir_edge FILTER e._from == s._id AND e.label == "subject_Patient" AND e.project == s.project AND e.dataset_generation == s.dataset_generation FILTER STARTS_WITH(e._to,"Patient/") LET members=(FOR se IN fhir_edge FILTER se._to == e._to AND se.label == "subject_Patient" AND se.project == s.project AND se.dataset_generation == s.dataset_generation FILTER STARTS_WITH(se._from,"Specimen/") LIMIT 2 LET d=DOCUMENT(se._from) FILTER d.project == s.project AND d.dataset_generation == s.dataset_generation RETURN {id:d.id,_id:d._id}) FILTER LENGTH(members)==2 RETURN {id:members[0].id,_id:members[0]._id,resourceType:"Specimen",generation:s.dataset_generation,sources:members}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  source = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')))[0];
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
  if(summaryShape==='PIVOT'){
    const kindField=builder.catalog.candidates.find(candidate=>candidate.nodeId===node.nodeId&&candidate.fieldPath==='resourceType');
    assert(kindField,'Pivot fixture needs an independent source grouping field');
    await command([{type:'ADD_COLUMN',outputId,occurrenceId:'base',candidateId:kindField.candidateId,projectionMode:'VALUE',initialPresentation:'TABLE',title:'Specimen resource type'}]);
  }
  initialSelection = await api(selections, { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: source.sources.map(member=>({ project, generation: source.generation, resourceType: 'Specimen', id: member.id })) } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: initialSelection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: initialSelection.id, routeChoiceId: direct.routeChoiceId }]);
  }
  cda.captureRequests(`${root}/${explorer}`);
  const requests = new WeakMap();
  nativeCapture = captureCDARequests(page, {
    apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: `${root}/${explorer}`, report,
    responsePaths: /proposal|preview|commands|construction-capabilities|row-definition/,
    shouldReportHttpError: () => false,
  });
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.pathname.startsWith(`${root}/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
    const entry = nativeCapture.byRequest.get(request);
    if (entry && /related-expand-choices|construction-choice-proposals|construction-proposals/.test(url.pathname)) {
      networkRequests.set(request, entry.body);
    }
    if (entry?.authorizationHeaderPresent) report.errors.push({kind:'unexpected-auth-header',path:entry.path});
  });
  page.on('response', response => {
    if (response.status() < 400 || response.url().endsWith('/favicon.ico')) return;
    const url = new URL(response.url());
    if (url.origin !== new URL(apiOrigin).origin && url.origin !== new URL(uiOrigin).origin) return;
    const request = response.request();
    const error={kind:'http',url:response.url(),status:response.status(),observedAfter:report.cases.at(-1)?.name,request:networkRequests.get(request)};
    report.errors.push(error);
    failedResponses.push(response.text().then(body => { error.body=sanitizeBody(body); }).catch(e => { error.bodyError=String(e); }));
  });
  if(unmappedMemberRepair){
    await runUnmappedMemberRepair();
  } else {
  assert.equal(source.sources.length,2);
  let witnesses=source.sources.map(member=>({anchor:member._id,values:summaryShape==='PIVOT'?[member.id,'Specimen']:[member.id]}));
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
    await click(page,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false)); });
    await click(page,'[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('' + args[0] + ' select[aria-label="Related record type"]')?.disabled===false)); }, [panel]);
    let start=Date.now();
    await selectOption(page,panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0]))); }, 5000, [panel+' input[aria-label="'+label+'"]']);
    await click(page,panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const expanded=builder;
  assert(expected.length>1,'CDA fixture must exercise many related records before grouping');
  const patientIndex=summaryShape==='PIVOT'?2:1;
  assert.equal(new Set(witnesses.map(w=>w.values[patientIndex])).size,1,'Sibling Specimens must share the same Patient grouping key');
  const grouped=[summaryShape==='PIVOT'?[witnesses[0].values[patientIndex],'Specimen',String(expected.length)]:[witnesses[0].values[patientIndex],String(expected.length)]];
  await open(expected);
  const configureGroup=async()=>{
    await click(page,'[data-testid="construction-rows-settings-trigger"]');
    await click(page,'[data-testid="construction-action-group-rows"]');
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]:not(:disabled)'))); });
    start=Date.now();
    await click(page,'input[aria-label="Group by Patient FHIR resource ID"]');
    if(summaryShape==='PIVOT') await click(page,'input[aria-label="Group by Specimen resource type"]');
  };
  let start;
  await configureGroup();
  await proposal('related-many-group-preview',start,grouped);
  await click(page,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await configureGroup();
  await proposal('confirmed-related-many-group-preview',start,grouped);
  await apply(grouped);
  await open(grouped);
  if (expandAfterGroup) {
    assert.equal(summaryShape, 'GROUP');
    const groupedBuilder = builder;
    const relatedPatients = rawQuery(`FOR e IN fhir_edge FILTER e._from IN ${JSON.stringify(source.sources.map(member=>member._id))} AND e.label=="subject_Patient" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" FILTER STARTS_WITH(e._to,"Patient/") LET d=DOCUMENT(e._to) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`);
    assert.equal(relatedPatients.length, 1, 'The two source members must share one terminal Patient');
    const patientRows = relatedPatients.map(patient=>[...grouped[0],patient.id]);
    const distinctPriorObservationIds=new Set(witnesses.map(witness=>witness.anchor));
    assert(!distinctPriorObservationIds.has(null),'The positive grouped expansion fixture must retain exact prior terminal identities');
    report.oracle.groupedExpansion = {relatedPatients,patientRows,priorRows:witnesses.length,distinctPriorObservationCount:distinctPriorObservationIds.size};
    const verifyRelatedFilter = async filterRows => {
      const beforeRelatedFilter=builder;
      const configureRelatedFilter=async()=>{
        const started=Date.now();
        await click(page,'[data-testid="construction-action-keep-rows"]');
        await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('button')].some(button=>button.innerText==='Related records'&&!button.disabled))); });
        await click(page,'button',{name:'Related records'});
        await waitForBrowser(page, (args) => { return Boolean((document.querySelector('select[aria-label="Related eligibility record type"]:not(:disabled)'))); });
        const anchorSelector=await browserEval(page, (args) => { return Boolean(document.querySelector('select[aria-label="Related eligibility anchor"]')); });
        if(anchorSelector) await selectOption(page,'select[aria-label="Related eligibility anchor"]','__loom_root_contributor_keys');
        const startingRecords=await browserEval(page, (args) => { const editor=document.querySelector('[data-testid="construction-related-eligibility-editor"]');return {text:editor.innerText,anchor:editor.querySelector('select[aria-label="Related eligibility anchor"]')?.value}; });
        assert(startingRecords.text.includes('Start from')&&startingRecords.text.includes('Specimen'),'Related filtering must show its contributing source records without an extra click');
        if(anchorSelector) assert.equal(startingRecords.anchor,'__loom_root_contributor_keys');
        report.relatedFilterStartingRecords=startingRecords;
        await selectOption(page,'select[aria-label="Related eligibility record type"]','Patient');
        await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Specimen -[subject]-> Patient"]:not(:disabled)'))); }, 5000);
        await click(page,'input[aria-label="Specimen -[subject]-> Patient"]');
        await proposal('grouped-contributors-related-filter-exists',started,filterRows);
        start=Date.now();
        await selectOption(page,'select[aria-label="Related eligibility rule"]','ABSENT');
        await proposal('grouped-contributors-related-filter-absent',start,[]);
        start=Date.now();
        await selectOption(page,'select[aria-label="Related eligibility rule"]','EXISTS');
        await proposal('grouped-contributors-related-filter-exists-restored',start,filterRows);
        start=Date.now();
        await selectOption(page,'select[aria-label="Related eligibility rule"]','COUNT_AT_LEAST');
        const countExplanation=await browserEval(page, (args) => { return document.querySelector('[data-testid="construction-related-eligibility-editor"]').innerText; });
        assert(countExplanation.includes('Each matching related record is counted once per current row, even if several starting records link to it.'),'Distinct-match counting must be explained in the editor');
        await proposal('grouped-contributors-related-filter-count-distinct-two',start,[]);
      };
      await configureRelatedFilter();
      await click(page,'[data-testid="construction-cancel-proposal"]');
      await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
      await rendered(filterRows);
      assert.deepEqual((await api(base+'/builder')).workspace,beforeRelatedFilter.workspace);
      await configureRelatedFilter();
      await apply([], filterRows[0].length);
      await open([], filterRows[0].length);
      const relatedFilter=doc(builder).construction.steps.at(-1);
      assert.equal(relatedFilter.operation.kind,'RELATED_ELIGIBILITY');
      assert.equal(relatedFilter.operation.relatedEligibility.anchorColumnId,'__loom_root_contributor_keys');
      await click(page,`[data-testid="construction-history-step-${relatedFilter.id}"]`);
      await click(page,`[data-testid="construction-edit-step-${relatedFilter.id}"]`);
      await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Minimum matching records"]:not(:disabled)'))); });
      start=Date.now();
      await fill(page, 'input[aria-label="Minimum matching records"]', '1');
      await proposal('edit-grouped-related-filter-count-one',start,filterRows);
      await apply(filterRows);
      await open(filterRows);
      await click(page,`[data-testid="construction-history-step-${relatedFilter.id}"]`);
      start=Date.now();
      await click(page,`[data-testid="construction-remove-step-${relatedFilter.id}"]`);
      await proposal('remove-grouped-related-filter',start,filterRows);
      await apply(filterRows);
      await open(filterRows);
      assert.deepEqual(doc(builder).construction,doc(beforeRelatedFilter).construction,'Removing related filtering must restore exact grouped expansion');
    };
    if(groupedRelatedFilter && groupedFilterDirect) await verifyRelatedFilter(grouped);
    const configureRelated = async () => {
      const started = Date.now();
      await click(page,'[data-testid="construction-rows-settings-trigger"]');
      await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-action-related-rows"]'))); });
      const action=await browserEval(page, (args) => { const button=document.querySelector('[data-testid="construction-action-related-rows"]');return {disabled:button.disabled,text:button.innerText}; });
      assert(!action.disabled, 'Grouped rows retain source members but related expansion is disabled: '+action.text);
      await click(page,'[data-testid="construction-action-related-rows"]');
      await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]')?.disabled===false)); });
      const explanation=await browserEval(page, (args) => { return document.querySelector('[data-testid="construction-related-expand-editor"]').innerText; });
      assert(explanation.includes('Start from')&&explanation.includes('Specimen'),'Grouped expansion must clearly identify the contributing source record type');
      report.groupedStartingRecords=explanation;
      await selectOption(page,'[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]','Patient');
      await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Specimen -[subject]-> Patient"]:not(:disabled)'))); }, 5000);
      await click(page,'input[aria-label="Specimen -[subject]-> Patient"]');
      await proposal('grouped-source-union-related-preview',started,patientRows);
    };
    await configureRelated();
    await click(page,'[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
    await rendered(grouped);
    assert.deepEqual((await api(base+'/builder')).workspace,groupedBuilder.workspace,'Cancel must retain grouped membership and construction');
    await configureRelated();
    await apply(patientRows);
    await open(patientRows);
    const groupedExpansion=doc(builder).construction.steps.at(-1);
    assert.equal(groupedExpansion.operation.kind,'RELATED_EXPAND');
    assert.equal(groupedExpansion.operation.relatedExpand.anchorColumnId,'__loom_root_contributor_keys');
    if(groupedRelatedFilter && !groupedFilterDirect) await verifyRelatedFilter(patientRows);
    const patientBuilder=builder;
    const onwardMatches=rawQuery(`FOR e IN fhir_edge FILTER e._to==${JSON.stringify(relatedPatients[0]._id)} AND e.label=="subject_Patient" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" FILTER STARTS_WITH(e._from,"Observation/") LET d=DOCUMENT(e._from) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`);
    assert(onwardMatches.length>1&&onwardMatches.length<=1000,'Use bounded one-to-many onward CDA matches');
    assert.deepEqual(onwardMatches.map(match=>match._id).sort(),[...distinctPriorObservationIds].sort(),'Onward expansion must recover the distinct source-witness terminal set, not the duplicated pre-Group rows');
    const onwardRows=onwardMatches.map(match=>[...patientRows[0],match.id]);
    report.oracle.groupedExpansion.onward={matches:onwardMatches,rows:onwardRows};
    await click(page,'[data-testid="construction-rows-settings-trigger"]');
    await click(page,'[data-testid="construction-action-related-rows"]');
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('select[aria-label="Related record type"]')?.disabled===false)); });
    start=Date.now();
    await selectOption(page,'select[aria-label="Related record type"]','Observation');
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Patient <-[subject]- Observation"]:not(:disabled)'))); }, 5000);
    await click(page,'input[aria-label="Patient <-[subject]- Observation"]');
    await proposal('onward-from-grouped-related-identity',start,onwardRows);
    await apply(onwardRows);
    const acceptedOnwardPreview=report.nativeRequests.filter(request=>request.path===base+'/preview'&&request.status===200&&request.completedAt).at(-1);
    assert(acceptedOnwardPreview?.body?.receiptId);
    const acceptedCompletePreview=await api(base+'/preview',{receiptId:acceptedOnwardPreview.body.receiptId,outputId,limit:100});
    await open(onwardRows);
    const lastSavedPreview=report.nativeRequests.filter(request=>request.path===base+'/preview'&&request.status===200&&request.completedAt).at(-1);
    assert(lastSavedPreview?.body?.receiptId,'A native saved preview must supply the exact receipt for the full identity check');
    const completePreview=await api(base+'/preview',{receiptId:lastSavedPreview.body.receiptId,outputId,limit:100});
    assert.equal(completePreview.rows.length,onwardRows.length,'The full saved result must contain each distinct terminal once');
    assert.equal(completePreview.rowCount,onwardRows.length);
    const fullCells=completePreview.rows.map(row=>completePreview.columns.map(column=>row[column.column]==null?'—':String(row[column.column])));
    assert.deepEqual(fullCells.map(row=>JSON.stringify(row)).sort(),onwardRows.map(row=>JSON.stringify(row)).sort(),'All saved rows must match the independent CDA terminal witnesses');
    assert.deepEqual(completePreview.rows,acceptedCompletePreview.rows,'Reload must preserve every expanded row value and identity');
    const identities=completePreview.rows.map(row=>row.__loom_row_id);
    assert(identities.every(identity=>typeof identity==='string'&&identity.length>0),'Expanded rows must carry exact row identities');
    assert.equal(new Set(identities).size,identities.length,'Every grouped onward terminal must have a distinct row identity');
    report.completeGroupedOnwardPreview=completePreview;
    const onwardStep=doc(builder).construction.steps.at(-1);
    assert.equal(onwardStep.operation.kind,'RELATED_EXPAND');
    assert.notEqual(onwardStep.operation.relatedExpand.anchorColumnId,'__loom_root_contributor_keys','Onward expansion must start from the exact active Patient identity');
    await click(page,`[data-testid="construction-history-step-${onwardStep.id}"]`);
    start=Date.now();
    await click(page,`[data-testid="construction-remove-step-${onwardStep.id}"]`);
    await proposal('remove-onward-grouped-expansion',start,patientRows);
    await apply(patientRows);
    await open(patientRows);
    assert.deepEqual(doc(builder).construction,doc(patientBuilder).construction,'Onward removal must restore exact prior grouped expansion');
    await click(page,`[data-testid="construction-history-step-${groupedExpansion.id}"]`);
    await click(page,`[data-testid="construction-edit-step-${groupedExpansion.id}"]`);
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('select[aria-label="If a current row has no matches"]'))); });
    start=Date.now();
    await selectOption(page,'select[aria-label="If a current row has no matches"]','EXCLUDE');
    await proposal('edit-grouped-related-empty-policy',start,patientRows);
    await apply(patientRows);
    await open(patientRows);
    assert.equal(doc(builder).construction.steps.at(-1).operation.relatedExpand.emptyPolicy,'EXCLUDE');
    await click(page,`[data-testid="construction-history-step-${groupedExpansion.id}"]`);
    start=Date.now();
    await click(page,`[data-testid="construction-remove-step-${groupedExpansion.id}"]`);
    await proposal('remove-grouped-related-preview',start,grouped);
    await apply(grouped);
    await open(grouped);
    assert.deepEqual(doc(builder).construction,doc(groupedBuilder).construction,'Removal must restore the exact grouped construction');
  } else {
  let shaped=grouped;
  if(summaryShape!=='GROUP'){
    const beforeShape=builder;
    shaped=summaryShape==='PIVOT'?[['Specimen',String(expected.length)]]:[[grouped[0][0],'Row count',String(expected.length)]];
    const configureShape=async()=>{
      const discoveryStart=Date.now();
      await click(page,'[data-testid="construction-rows-settings-trigger"]');
      if(summaryShape==='PIVOT'){
        await click(page,'[data-testid="construction-action-pivot-rows"]');
        await waitForBrowser(page, (args) => { return Boolean((document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)'))); });
        await click(page,'input[aria-label="Pivot group Specimen resource type"]');
        const chooseField=async(label,prefix)=>{
          const options=await browserEval(page, (args) => { return [...document.querySelector('select[aria-label="' + args[0] + '"]').options].map(option=>({value:option.value,label:option.textContent})); }, [label]);
          const field=options.find(option=>option.label.startsWith(prefix));assert(field,JSON.stringify(options));
          await selectOption(page,`select[aria-label="${label}"]`,field.value);
        };
        await chooseField('Pivot category field','Patient FHIR resource ID');
        start=Date.now();
        await chooseField('Pivot values field','Row count');
      }else{
        await click(page,'button',{name:'Turn columns into rows'});
        await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Unpivot Row count"]:not(:disabled)'))); });
        start=Date.now();
        await click(page,'input[aria-label="Unpivot Row count"]');
      }
      await proposal(summaryShape.toLowerCase()+'-preview',start,shaped);
      recordRender(summaryShape.toLowerCase()+'-discovery-to-preview',discoveryStart);
    };
    await configureShape();
    await click(page,'[data-testid="construction-cancel-proposal"]');
    await rendered(grouped);
    assert.deepEqual((await api(base+'/builder')).workspace,beforeShape.workspace);
    await configureShape();
    await apply(shaped);
    await open(shaped);
  }
  const openRelatedFields=async()=>{
  await click(page,'[data-testid="construction-action-add-columns"]');
  await click(page,'[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-add-columns-source"]'))); });
  report.addFieldsUI=await browserEval(page, (args) => { return {text:document.querySelector('[aria-label="Add columns editor"]').innerText,controls:[...document.querySelectorAll('[aria-label="Add columns editor"] input,[aria-label="Add columns editor"] select,[aria-label="Add columns editor"] button')].map(e=>({tag:e.tagName,label:e.getAttribute('aria-label'),testId:e.dataset.testid,text:e.innerText,disabled:e.disabled}))}; });
  if(!await browserEval(page, (args) => { return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open; })){
    await click(page,'[aria-label="Related resources"] summary');
  }
  await click(page,'[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  if(!await browserEval(page, (args) => { return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open; })){
    await click(page,'[data-testid="feature-catalog-raw-fields"] summary');
  }
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)'))); });
  };
  await openRelatedFields();
  const beforeField=builder;
  const matchingRecords=new Map(witnesses.filter(witness=>witness.anchor!==null).map(witness=>[witness.anchor,witness.values.at(-1)]));
  assert(![...matchingRecords.values()].includes(absentID),'Zero-match predicate must exclude every independently observed target');
  const matchingIDs=zeroMatches?[]:[...matchingRecords.keys()].sort();
  report.oracle.matchingRecordIDs=matchingIDs;
  const expectedSummary=resultForm==='COUNT'?matchingIDs.length:matchingIDs.length>0;
  let withField=[[...shaped[0],String(expectedSummary)]];
  const configureRelatedField=async()=>{
    const selectionStart=Date.now();
    await click(page,'input[aria-label="Select Observation.id"]');
    await click(page,'[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[role="dialog"]'))); });
    if(!await browserEval(page, (args) => { return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open; })){
      await click(page,'[role="dialog"] summary',{includes:'Other relationship paths'});
    }
    await click(page,'[role="dialog"] input[aria-label="Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation"]');
    start=Date.now();
    await click(page,`[role="dialog"] input[aria-label="Observation ID: ${resultForm==='COUNT'?'Count matching records':'Show whether a match exists'}"]`);
    if(zeroMatches){
      if(!await browserEval(page, (args) => { return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Matching records:'))?.parentElement.open; })){
        await click(page,'[role="dialog"] summary',{includes:'Matching records:'});
      }
      await click(page,'[role="dialog"] label',{includes:'Only records where Observation ID equals'});
      await fill(page, 'input[aria-label="Observation ID exact value"]', absentID);
    }
    await click(page,'[role="dialog"] button',{name:'Add 1 column'});
    await waitForBrowser(page, (args) => { return Boolean((['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus) || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus) || document.querySelector('[role="dialog"]')?.innerText.includes('root document identity'))); });
    report.relatedProposal=await browserEval(page, (args) => { const p=document.querySelector('[data-testid="construction-proposal-panel"]')??document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:p?.dataset.proposalStatus,text:p?.innerText??document.querySelector('[role="dialog"]')?.innerText}; });
    assert.equal(report.relatedProposal.status,'ready',report.relatedProposal.text);
    const cells=await browserEval(page, (args) => { return [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')].map(cell=>({text:cell.innerText,raw:cell.title})); });
    assert.equal(cells.length,shaped[0].length+1);
    assert.deepEqual(cells.slice(0,-1).map(cell=>cell.text),shaped[0]);
    assert.equal(cells.at(-1).text,String(expectedSummary));
    recordRender(summaryShape.toLowerCase()+'-related-'+resultForm.toLowerCase()+'-preview',start);
    recordRender('related-field-discovery-to-preview',selectionStart);
  };
  const proposalPanel=async()=>await browserEval(page, (args) => { return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?'construction-choice-proposal-panel':'construction-proposal-panel'; });
  await configureRelatedField();
  await click(page,`[data-testid="${await proposalPanel()}"] button`,{name:'Cancel'});
  await rendered(shaped);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeField.workspace);
  await openRelatedFields();
  await configureRelatedField();
  start=Date.now();
  const panel=await proposalPanel();
  if(panel==='construction-choice-proposal-panel') await click(page,'[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  else await click(page,'[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="' + args[0] + '"]'))); }, [panel]);
  await rendered(withField);
  recordRender('group-related-summary-apply',start);
  builder=await api(base+'/builder');
  report.savedFieldDocument=doc(builder);
  await open(withField);
  const addedStep=doc(builder).construction.steps.find(step=>!doc(beforeField).construction.steps.some(prior=>prior.id===step.id));
  assert.equal(addedStep?.operation.kind,'RELATED_SOURCE','Summary must retain its related-record operation');
  assert.equal(addedStep.operation.relatedSource.form,resultForm);
  if(collectionRoundTrip){
    const beforeCollection=builder;
    const collectionSettingsStart=Date.now();
    await click(page,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled))); });
    recordRender('starting-collection-controls-ready',collectionSettingsStart);
    const clearStart=Date.now();
    await click(page,'section[aria-label="Starting collection"] button',{name:'Use all authorized rows'});
    await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use selected resources'&&!button.disabled) && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='26' && !document.body.innerText.includes('Loading your table…') || document.body.innerText.includes('Preview failed:'))); });
    const collectionError=await browserEval(page, (args) => { return document.body.innerText.split(String.fromCharCode(10)).find(line=>line.startsWith('Preview failed:')); });
    assert.equal(collectionError,undefined,'Changing the starting collection must render the authored dataframe: '+collectionError);
    recordRender('clear-collection-under-group-related-summary',clearStart);
    const scroll=page.locator('[data-testid="preview-table-scroll"]');
    const visibleRows=new Map();
    for(let pageNumber=0;pageNumber<25;pageNumber++){
      const pageRows=await scroll.locator('[role="row"]').evaluateAll(rows=>rows.slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length));
      for(const row of pageRows) visibleRows.set(row[0],row);
      if(visibleRows.size===25) break;
      const position=await scroll.evaluate(element=>({top:element.scrollTop,height:element.clientHeight,max:element.scrollHeight-element.clientHeight}));
      if(position.top>=position.max) break;
      await scroll.hover();
      await page.mouse.wheel(0,Math.max(1,position.height/2));
      await page.waitForFunction(previous=>document.querySelector('[data-testid="preview-table-scroll"]')?.scrollTop>previous,position.top,{timeout:1000});
    }
    for(let pageNumber=0;pageNumber<25;pageNumber++){
      const position=await scroll.evaluate(element=>({top:element.scrollTop,height:element.clientHeight}));
      if(position.top===0) break;
      await scroll.hover();
      await page.mouse.wheel(0,-Math.max(1,position.height/2));
      await page.waitForFunction(previous=>document.querySelector('[data-testid="preview-table-scroll"]')?.scrollTop<previous,position.top,{timeout:1000});
    }
    const visible=[...visibleRows.values()];
    assert.equal(visible.length,25,'Independent source checks must cover every virtualized preview row');
    assert(visible.every(row=>row.length===3&&row[0]!=='—'),'This oracle requires rendered Patient groups');
    const patientIDs=visible.map(row=>row[0]);
    const independent=rawQuery(`LET scopedPatients=(FOR p IN Patient
      FILTER p.id IN ${JSON.stringify(patientIDs)} AND p.resourceType=="Patient" AND p.project=="${project}" AND p.dataset_generation=="cda-fhir-v1"
      RETURN {id:p.id,_id:p._id,project:p.project,dataset_generation:p.dataset_generation})
      FOR groupID IN ${JSON.stringify(patientIDs)}
      LET contributors=(FOR p IN scopedPatients FILTER p.id==groupID
        LET specimens=(FOR edge IN fhir_edge
          FILTER edge._to==p._id AND edge.label=="subject_Patient" AND edge.to_type=="Patient" AND edge.project==p.project AND edge.dataset_generation==p.dataset_generation
          FILTER IS_SAME_COLLECTION("Specimen",edge._from)
          LET s=DOCUMENT(edge._from) FILTER s!=null AND s.project==p.project AND s.dataset_generation==p.dataset_generation
          RETURN DISTINCT s._id)
        LET observations=(FOR edge IN fhir_edge
          FILTER edge._to==p._id AND edge.label=="subject_Patient" AND edge.from_type=="Observation" AND edge.project==p.project AND edge.dataset_generation==p.dataset_generation
          LET o=DOCUMENT(edge._from) FILTER o!=null AND o.resourceType=="Observation" AND o.project==p.project AND o.dataset_generation==p.dataset_generation
          RETURN DISTINCT o._id)
        RETURN {specimens,rows:LENGTH(specimens)*MAX([1,LENGTH(observations)])})
      LET roots=SORTED_UNIQUE(FLATTEN(contributors[*].specimens,1))
      LET related=(FOR rootID IN roots FOR patientEdge IN fhir_edge
        FILTER patientEdge._from==rootID AND patientEdge.label=="subject_Patient" AND patientEdge.to_type=="Patient" AND patientEdge.project=="${project}" AND patientEdge.dataset_generation=="cda-fhir-v1"
        LET p=DOCUMENT(patientEdge._to) FILTER p!=null AND p.resourceType=="Patient" AND p.project==patientEdge.project AND p.dataset_generation==patientEdge.dataset_generation
        FOR observationEdge IN fhir_edge
          FILTER observationEdge._to==p._id AND observationEdge.label=="subject_Patient" AND observationEdge.from_type=="Observation" AND observationEdge.project==p.project AND observationEdge.dataset_generation==p.dataset_generation
          LET o=DOCUMENT(observationEdge._from) FILTER o!=null AND o.resourceType=="Observation" AND o.project==p.project AND o.dataset_generation==p.dataset_generation
          RETURN DISTINCT o._id)
      RETURN [groupID,TO_STRING(SUM(contributors[*].rows)),TO_STRING(LENGTH(related))]`);
    assert.deepEqual([...visible].sort(),independent.sort(),'Group multiplicity and distinct related counts must recompute from scoped raw source records');
    report.fullCollectionOracle={visible,independent};
    builder=await api(base+'/builder');
    assert.equal(doc(builder).population,undefined);
    assert.deepEqual(doc(builder).construction,doc(beforeCollection).construction,'Changing the starting collection must preserve authored operations');
    const attachStart=Date.now();
    await click(page,'section[aria-label="Starting collection"] button',{name:'Use selected resources'});
    await rendered(withField);
    await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled))); });
    recordRender('reattach-collection-under-group-related-summary',attachStart);
    builder=await api(base+'/builder');
    assert.deepEqual(doc(builder).population,doc(beforeCollection).population);
    assert.deepEqual(doc(builder).construction,doc(beforeCollection).construction);
    await open(withField);
  }
  let restoredConstruction=doc(beforeField).construction;
  if(upstreamGroupEdit){
    const beforeEdit=builder;
    const groupStep=doc(builder).construction.steps.find(step=>step.operation.kind==='GROUP');
    assert(groupStep,'The saved Group must exist');
    const editedShape=[[shaped[0].at(-1)]];
    const editedWithField=[[...editedShape[0],String(expectedSummary)]];
    const editGroup=async()=>{
      await click(page,`[data-testid="construction-history-step-${groupStep.id}"]`);
      await click(page,`[data-testid="construction-edit-step-${groupStep.id}"]`);
      await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]:not(:disabled)'))); });
      assert(await browserEval(page, (args) => { return document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]').checked; }));
      start=Date.now();
      await click(page,'input[aria-label="Group by Patient FHIR resource ID"]');
      await proposal('upstream-group-key-removal-retains-summary',start,editedWithField);
      assert.equal(await browserEval(page, (args) => { return document.querySelectorAll('[data-testid^="construction-removal-step-"]').length; }),0,'Removing a visible key must retain a summary anchored to contributing records');
    };
    await editGroup();
    await click(page,'[data-testid="construction-cancel-proposal"]');
    await rendered(withField);
    assert.deepEqual((await api(base+'/builder')).workspace,beforeEdit.workspace);
    await editGroup();
    await apply(editedWithField);
    await open(editedWithField);
    const saved=doc(builder).construction;
    assert.equal((saved.steps.find(step=>step.id===groupStep.id).operation.group.keys??[]).length,0);
    const savedSummary=saved.steps.find(step=>step.id===addedStep.id);
    assert(savedSummary,'The related summary must survive upstream Group editing');
    assert.deepEqual(savedSummary.operation,addedStep.operation,'The signed source route and contributor predicate must survive the edit');
    shaped=editedShape;
    withField=editedWithField;
    restoredConstruction={...saved,steps:saved.steps.filter(step=>step.id!==addedStep.id)};
    report.upstreamEditedDocument=doc(builder);
  }
  if(addedStep){
    await click(page,`[data-testid="construction-history-step-${addedStep.id}"]`);
    await click(page,`[data-testid="construction-edit-step-${addedStep.id}"]`);
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]:not(:disabled)'))); });
    const columnID=addedStep.operation.relatedSource.outputColumnId;
    const originalLabel=addedStep.outputs.find(column=>column.id===columnID).label;
    start=Date.now();
    await fill(page, '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]', `${originalLabel} QA`);
    await proposal('edit-related-summary-label-preview',start,withField);
    await click(page,'[data-testid="construction-cancel-proposal"]');
    await rendered(withField);
    assert.deepEqual((await api(base+'/builder')).workspace,builder.workspace,'Cancel edit must preserve the saved summary');
    await click(page,`[data-testid="construction-history-step-${addedStep.id}"]`);
    await click(page,`[data-testid="construction-edit-step-${addedStep.id}"]`);
    await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]:not(:disabled)'))); });
    start=Date.now();
    await fill(page, '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]', `${originalLabel} QA`);
    const editedLabel=await browserEval(page, (args) => { return document.querySelector('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]').value; });
    assert.notEqual(editedLabel,originalLabel);
    assert.equal(editedLabel.replace(' QA',''),originalLabel,'Native typing must preserve the original label around the inserted text');
    await proposal('confirmed-edit-related-summary-label-preview',start,withField);
    await apply(withField);
    await open(withField);
    assert.equal(doc(builder).construction.steps.find(step=>step.id===addedStep.id).outputs.find(column=>column.id===columnID).label,editedLabel);
    await click(page,`[data-testid="construction-history-step-${addedStep.id}"]`);
    start=Date.now();
    await click(page,`[data-testid="construction-remove-step-${addedStep.id}"]`);
    await proposal('remove-related-summary-preview',start,shaped);
    await apply(shaped);
  } else {
    await click(page,'button',{name:'Columns'});
    start=Date.now();
    await click(page,'button[aria-label="Remove Observation ID column"]');
    await rendered(shaped);
    recordRender('remove-group-related-summary',start);
  }
  builder=await api(base+'/builder');
  await open(shaped);
  assert.deepEqual(doc(builder).construction,restoredConstruction);
  }
  }
  assert(report.protectedExplorerUntouched, `A browser request targeted protected Explorer ${protectedExplorer}`);
  assert.deepEqual(report.errors,[]);
  report.status='passed';
} catch (error) {
  report.status = error?.code==='CDA_UNMAPPED_REPAIR_WITNESS_NOT_FOUND'?'witness-not-found':'failed'; report.error = String(error.stack ?? error);
  report.savedBuilderAtFailure=await api(base+'/builder').catch(error=>({readError:String(error)}));
  report.failureUI = await browserEval(page, () => { return document.body.innerText; }).catch(String);
  throw error;
} finally {
  await Promise.all([...failedResponses]);
  await nativeCapture?.flush();
  report.finished = new Date().toISOString();
  await cda.attachReport('group-related-summary', report);
}
return report;
}
