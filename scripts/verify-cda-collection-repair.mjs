import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { waitForCondition } from './lib/playwright-observations.mjs';

export async function collectionRepairWorkflow({ page, cda }) {
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const partialLongRoute=process.env.LOOM_COLLECTION_PARTIAL_LONG_ROUTE==='1';
const longRoute=process.env.LOOM_COLLECTION_LONG_ROUTE==='1'||partialLongRoute;
const project = cda.project;
const apiContainer = cda.target.apiContainer;
const arangoContainer = cda.target.arangoContainer;
const composeProject = cda.target.composeProject;
const evidence = cda.evidenceDirectory;
const explorer = `collection-repair-${Date.now()}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = base.replace('/authoring/v2','/selections');
const report = Object.assign(cda.report, { longRoute, partialLongRoute, explorer, cases: [], requests: [], browserRequests: [], nativeRequests: [], errors: [], exceptions: [], http: [], incidental: [], responses: [], responseCaptureErrors: [], started: new Date().toISOString() });
const recordCase = result => {
  report.cases.push(result);
};
const api = async (path, body) => {
  const request = { path, body, requestId: `collection-repair-${randomUUID()}` };
  report.requests.push(request);
  const start = Date.now();
  const response = await fetch(apiOrigin + path, { method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': request.requestId }, ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000) });
  request.status = response.status;
  request.response = await response.json();
  request.durationMs = Date.now() - start;
  assert(response.ok, JSON.stringify(request));
  return request.response;
};
let builder;
let actionTracker = {};
let requestMonitor;
let fatal;
let outputId;
let expectedObservationIDs=[];
const inspectPage = (_page, inspect, argument) => cda.inspect(inspect, argument);
const waitForBrowser = (_page, condition, timeout = 30000) => waitForCondition(page, condition, Math.min(timeout, 5000));
const resolveActionLocator = async (page, selector, identity = {}) => {
  const candidates = page.locator(selector);
  if (identity.name === undefined) return requireUnique(candidates, selector);
  const target = candidates.and(page.getByRole('button', { name: identity.name, exact: true }));
  return requireUnique(target, `${selector} ${identity.name}`);
};
const performAction = async (_tracker, label, locator, action, options = {}) => cda.action(
  label, locator, target => action(target, { timeout: options.timeout ?? 5000 }), options);
const click = async (_page, selector, identity = {}, timeout = 5000) => {
  const label = `Click ${identity.name ?? selector}`;
  actionTracker.activeAction = { label, locator: selector, targetLocator: page.locator(selector), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector, identity);
  actionTracker.activeAction.targetLocator = locator;
  const elapsedMs = await performAction(actionTracker, label, locator, (target, options) => target.click(options), { timeout });
  actionTracker.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs };
  return elapsedMs;
};
const selectOption = async (_page, selector, value, timeout = 5000) => {
  const label = `Select ${value} in ${selector}`;
  actionTracker.activeAction = { label, locator: selector, targetLocator: page.locator(selector), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector);
  actionTracker.activeAction.targetLocator = locator;
  const elapsedMs = await performAction(actionTracker, label, locator, (target, options) => target.selectOption(value, options), { timeout });
  actionTracker.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs };
  return elapsedMs;
};
const navigate = (_page, url) => cda.navigate(url);
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10, snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const rawQuery = query => {
  const result = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const savedPopulationRoute = route => route.map(step=>({
  resourceType:step.toResourceType,
  relationship:step.relationship,
  catalogEdgeId:step.edgeId,
  storageDirection:step.storageDirection,
}));
const assertPreviewIDs = async name => {
  if(!partialLongRoute)return;
  const started=Date.now();
  const expectedCount=expectedObservationIDs.length;
  await page.waitForFunction(({ expectedCount }) => {
    const preview = document.querySelector('[data-testid="preview-table-scroll"]');
    const table = preview?.querySelector('[role="table"]');
    const visible = element => Boolean(element && element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden');
    return visible(preview) && visible(table)
      && table.getAttribute('aria-rowcount') === String(expectedCount + 1)
      && table.getAttribute('aria-colcount') === '1'
      && !document.body.innerText.includes('Loading your table…')
      && !document.body.innerText.includes('Preview failed:');
  }, { expectedCount }, { timeout: 5000 });
  const preview = page.locator('[data-testid="preview-table-scroll"]');
  const table = preview.locator('[role="table"]');
  const rowsByIndex = new Map();
  const collectVisibleRows = async () => {
    const rows = await table.locator('[role="row"]').evaluateAll(nodes => nodes.map(row => {
      const rawIndex = Number(row.getAttribute('aria-rowindex'));
      const labelIndex = Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/^Inspect row (\d+) identity$/)?.[1]);
      const gutterIndex = Number(row.firstElementChild?.textContent?.trim());
      const index = Number.isInteger(rawIndex) && rawIndex > 0 ? rawIndex
        : Number.isInteger(labelIndex) && labelIndex > 0 ? labelIndex + 1
          : Number.isInteger(gutterIndex) && gutterIndex > 0 ? gutterIndex + 1 : NaN;
      return { index, cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()) };
    }));
    for (const row of rows) if (Number.isInteger(row.index) && row.index > 1 && row.cells.length) rowsByIndex.set(row.index, row.cells);
  };
  for (let pageDown = 0; pageDown < expectedCount && rowsByIndex.size < expectedCount; pageDown++) {
    await collectVisibleRows();
    if (rowsByIndex.size >= expectedCount) break;
    const box = await preview.boundingBox();
    assert(box, `${name}: preview scroll container is not visible`);
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    const beforeTop = await preview.evaluate(node => node.scrollTop);
    await page.mouse.wheel(0, Math.max(240, Math.floor(box.height * 0.8)));
    await page.waitForFunction(({ beforeTop }) => {
      const node = document.querySelector('[data-testid="preview-table-scroll"]');
      return node && (node.scrollTop > beforeTop || node.scrollTop + node.clientHeight >= node.scrollHeight);
    }, { beforeTop }, { timeout: 1000 });
  }
  await collectVisibleRows();
  const rendered = {
    rowCount: await table.getAttribute('aria-rowcount'),
    columnCount: await table.getAttribute('aria-colcount'),
    rows: [...rowsByIndex.entries()].sort(([left], [right]) => left - right).map(([, cells]) => cells),
  };
  assert.equal(Number(rendered.rowCount)-1,expectedCount,`${name}: preview row count must match the scoped raw route oracle`);
  assert.deepEqual(rendered.rows.map(row=>row[0]).sort(),[...expectedObservationIDs].sort(),`${name}: rendered Observation IDs and multiplicity must match the scoped edge oracle`);
  recordCase({name,durationMs:Date.now()-started,rowCount:expectedCount,observationIDs:rendered.rows.map(row=>row[0])});
};
const rowSettingsDialog = () => page.getByRole('dialog', { name: 'Row definition settings', exact: true });
const openRowSettings = async () => {
  await click(page, '[data-testid="construction-rows-settings-trigger"]', {name:'Configure rows'});
  const dialog = rowSettingsDialog();
  await dialog.waitFor({ state: 'visible', timeout: 5000 });
  await dialog.getByRole('region', { name: 'Starting collection', exact: true }).waitFor({ state: 'visible', timeout: 5000 });
  return dialog;
};
const returnToTable = async () => {
  const dialog = rowSettingsDialog();
  await click(page, '[role="dialog"][aria-label="Row definition settings"] button', {name:'Back to table'});
  await dialog.waitFor({ state: 'hidden', timeout: 5000 });
};
const open = async (previewName='partial-long-route-preview') => {
  const loadStart=Date.now();
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(page, { kind: 'present', selector: `[data-testid="construction-table-${outputId}"]` });
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, { kind: 'enabled', selector: '[data-testid="construction-rows-settings-trigger"]' });
  if(partialLongRoute)await assertPreviewIDs(previewName);
  else {
    await page.waitForFunction(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const preview = document.querySelector('[data-testid="preview-table-scroll"]');
      return Boolean(preview && preview.getClientRects().length && table && table.getClientRects().length)
        && table.getAttribute('aria-rowcount') === '1' && table.getAttribute('aria-colcount') === '1'
        && !document.body.innerText.includes('Loading your table…');
    }, null, { timeout: 5000 });
    assert.equal(await page.locator('[data-testid="preview-table-scroll"] [role="cell"]').count(),0,'The independently unmapped selection must produce no table rows');
  }
  await openRowSettings();
  const durationMs=Date.now()-loadStart;assert(durationMs<=5000,'The native table and settings must render within five seconds');recordCase({name:partialLongRoute?'partial-long-route-table-load-to-settings':'empty-table-load-to-settings',durationMs});
};
const checkCoverage = async (name, counts) => {
  const start = Date.now();
  await click(page, '[role="dialog"][aria-label="Row definition settings"] section[aria-label="Starting collection"] button', {name:'Check selected-resource coverage'});
  await page.waitForFunction(() => {
    const dialog = document.querySelector('[role="dialog"][aria-label="Row definition settings"]');
    const visible = element => Boolean(element && element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden');
    return visible(dialog?.querySelector('[data-testid="population-coverage-report"]'))
      || [...(dialog?.querySelectorAll('[role="alert"]') ?? [])].some(visible);
  }, null, { timeout: 5000 });
  const text = await inspectPage(page, () => {
    const dialog = document.querySelector('[role="dialog"][aria-label="Row definition settings"]');
    const report = dialog?.querySelector('[data-testid="population-coverage-report"]');
    return report && report.getClientRects().length && getComputedStyle(report).visibility !== 'hidden' ? report.innerText : null;
  });
  const alert = await inspectPage(page, () => {
    const dialog = document.querySelector('[role="dialog"][aria-label="Row definition settings"]');
    return [...(dialog?.querySelectorAll('[role="alert"]') ?? [])]
      .filter(element => element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden')
      .map(element => element.innerText).join(' | ');
  });
  assert((text ?? '').includes(counts), `Coverage must match independent CDA records: report=${text}; alerts=${alert}`);
  assert(Date.now()-start <= 5000, 'Coverage must render within five seconds');
  recordCase({name,durationMs:Date.now()-start,text});
};
try {
  let selected;
  let selectedResources;
  let mappedResources=[];
  let expectedMappedRefs=[];
  if(partialLongRoute){
    const routeOracleQuery=`LET seeds=(FOR s IN Specimen FILTER s.resourceType=="Specimen" AND s.project=="${project}" AND s.dataset_generation=="cda-fhir-v1" SORT s.id LIMIT 4000 RETURN s) FOR s IN seeds LET parents=(FOR e IN fhir_edge FILTER e._from==s._id AND e.label=="parent" AND e.from_type=="Specimen" AND e.to_type=="Specimen" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" LET parent=DOCUMENT(e._to) FILTER parent!=null AND parent.resourceType=="Specimen" AND parent.project=="${project}" AND parent.dataset_generation=="cda-fhir-v1" RETURN parent._id) LET children=(FOR e IN fhir_edge FILTER e._to==s._id AND e.label=="parent" AND e.from_type=="Specimen" AND e.to_type=="Specimen" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" LET child=DOCUMENT(e._from) FILTER child!=null AND child.resourceType=="Specimen" AND child.project=="${project}" AND child.dataset_generation=="cda-fhir-v1" RETURN child.id) LET routeRows=(FOR parentEdge IN fhir_edge FILTER parentEdge._from==s._id AND parentEdge.label=="parent" AND parentEdge.from_type=="Specimen" AND parentEdge.to_type=="Specimen" AND parentEdge.project=="${project}" AND parentEdge.dataset_generation=="cda-fhir-v1" LET parent=DOCUMENT(parentEdge._to) FILTER parent!=null AND parent.resourceType=="Specimen" AND parent.project=="${project}" AND parent.dataset_generation=="cda-fhir-v1" FOR specimenEdge IN fhir_edge FILTER specimenEdge._to==parent._id AND specimenEdge.label=="specimen_Specimen" AND specimenEdge.from_type=="Observation" AND specimenEdge.to_type=="Specimen" AND specimenEdge.project=="${project}" AND specimenEdge.dataset_generation=="cda-fhir-v1" LET observation=DOCUMENT(specimenEdge._from) FILTER observation!=null AND observation.resourceType=="Observation" AND observation.project=="${project}" AND observation.dataset_generation=="cda-fhir-v1" RETURN observation.id) RETURN {id:s.id,parents,children,routeRows}`;
    const candidates=rawQuery(routeOracleQuery);
    const mappedCandidates=candidates.filter(candidate=>candidate.parents.length>0&&candidate.routeRows.length>=1&&candidate.routeRows.length<=25&&new Set(candidate.routeRows).size===candidate.routeRows.length);
    for(let left=0;left<mappedCandidates.length&&!mappedResources.length;left++){
      for(let right=left+1;right<mappedCandidates.length;right++){
        const pair=[mappedCandidates[left],mappedCandidates[right]];
        const observationIDs=pair.flatMap(candidate=>candidate.routeRows);
        if(observationIDs.length<=25&&new Set(observationIDs).size===observationIDs.length){mappedResources=pair;break;}
      }
    }
    const unmapped=candidates.find(candidate=>!mappedResources.some(mapped=>candidate.id===mapped.id)&&candidate.parents.length===0&&candidate.children.length>0&&candidate.routeRows.length===0);
    assert.equal(mappedResources.length,2,`The bounded scoped CDA prefix must contain two mapped Specimens with distinct Observation roots; mapped=${JSON.stringify(mappedCandidates.slice(0,12).map(({id,routeRows})=>({id,routeRows})))}`);
    assert(unmapped,`The bounded scoped CDA prefix must also contain one unmapped childless-parent Specimen; candidates=${JSON.stringify(candidates.slice(0,12).map(({id,parents,children,routeRows})=>({id,parentCount:parents.length,childCount:children.length,routeRows:routeRows.length})))}`);
    expectedObservationIDs=mappedResources.flatMap(mapped=>mapped.routeRows).sort();
    assert(expectedObservationIDs.length<=25,'Mapped Observation roots must fit the complete 25-row native preview limit');
    selectedResources=[...mappedResources,unmapped];
    report.oracle={query:routeOracleQuery,seedLimit:4000,mapped:mappedResources.map(mapped=>({id:mapped.id,parentIDs:mapped.parents,rawObservationIDs:mapped.routeRows})),unmapped:{id:unmapped.id,parentIDs:unmapped.parents,childIDs:unmapped.children}};
  }else{
    const specimens = rawQuery(`FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 10 LET parents = (FOR e IN fhir_edge FILTER e._from == s._id AND e.label == "parent" AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" RETURN e._to) LET children = (FOR e IN fhir_edge FILTER e._to == s._id AND e.label == "parent" AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" LET child = DOCUMENT(e._from) FILTER child.project == "${project}" AND child.dataset_generation == "cda-fhir-v1" RETURN child.id) RETURN {id:s.id, parents, children}`);
    selected = specimens.find(s=>s.parents.length===0 && s.children.length>0);
    assert(selected, 'A real CDA Specimen with children but no parent is needed to detect reversed self-relationship traversal');
    selectedResources=[selected];
    report.oracle = selected;
  }
  await api(root, {name:explorer,title:'Collection repair browser QA'});
  builder = await api(base+'/builder');
  const node = builder.catalog.nodes.find(n=>n.resourceType===(longRoute?'Observation':'Specimen'));
  await command([{type:'CREATE_TABLE',title:'Unmapped collection QA',rootNodeId:node.nodeId}]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c=>c.nodeId===node.nodeId && c.fieldPath==='id');
  await command([{type:'ADD_COLUMN',outputId,occurrenceId:'base',candidateId:field.candidateId,projectionMode:'VALUE',initialPresentation:'TABLE',title:longRoute?'Observation ID':'Specimen ID'}]);
  const selection = await api(selections, {snapshotToken:builder.catalog.snapshotToken,idempotencyKey:explorer,source:{kind:'resources',resources:{refs:selectedResources.map(resource=>({project,generation:builder.catalog.generation,resourceType:'Specimen',id:resource.id}))}}});
  const generation=builder.catalog.generation;
  const scopeDigest=builder.catalog.authorizationScopeDigest;
  if(partialLongRoute)expectedMappedRefs=mappedResources.map(mapped=>({project,generation,resourceType:'Specimen',id:mapped.id})).sort((a,b)=>a.id.localeCompare(b.id));
  assert.equal(selection.project,project);
  assert.equal(selection.generation,generation);
  assert.equal(selection.resourceType,'Specimen');
  assert.equal(selection.scopeDigest,scopeDigest);
  assert.equal(selection.memberCount,selectedResources.length);
  const selectionPage=await api(`${selections}/${selection.id}?limit=100`);
  assert.equal(selectionPage.revision.id,selection.id);
  assert.equal(selectionPage.revision.scopeDigest,scopeDigest);
  assert.equal(selectionPage.revision.generation,generation);
  assert.equal(selectionPage.revision.resourceType,'Specimen');
  assert.deepEqual(selectionPage.members.map(member=>member.ref).sort((a,b)=>a.id.localeCompare(b.id)),selectedResources.map(resource=>({project,generation,resourceType:'Specimen',id:resource.id})).sort((a,b)=>a.id.localeCompare(b.id)),'The initial selection must contain exactly the two scoped raw Specimen witnesses');
  const routes = await api(base+'/population-routes', {snapshotToken:builder.catalog.snapshotToken,outputId,selectionRevisionId:selection.id,limit:50});
  report.parentChoices = routes.choices.filter(c=>c.route.length===1 && c.route[0].relationship==='parent');
  // Routes run from table roots to selected members: parent roots reach child members inbound.
  const parent = longRoute
    ? routes.choices.find(c=>c.route.length===2&&c.route[0].fromResourceType==='Observation'&&c.route[0].toResourceType==='Specimen'&&c.route[0].relationship==='specimen_Specimen'&&c.route[0].storageDirection==='OUTBOUND'&&c.route[1].relationship==='parent'&&c.route[1].storageDirection==='INBOUND')
    : report.parentChoices.find(c=>c.route[0].storageDirection==='INBOUND');
  assert(parent, 'The exact requested parent connection must be available: '+JSON.stringify(routes.choices.map(c=>c.route)));
  report.connection=parent.route;
  report.savedConnection=savedPopulationRoute(parent.route);
  assert.equal(parent.route.length,longRoute?2:1);
  await command([{type:'SET_TABLE_POPULATION',outputId,selectionRevisionId:selection.id,routeChoiceId:parent.routeChoiceId}]);
  const original = builder.workspace.documents[0];
  if(partialLongRoute)assert.deepEqual(original.population.route,report.savedConnection,'The saved route must match the exact catalog route before collection repair');
  requestMonitor = captureCDARequests(page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: `${root}/${explorer}`,
    responsePaths: /./,
    report: { nativeRequests: report.browserRequests, errors: report.errors },
    shouldReportRequestFailure: (entry, request) => {
      const index = report.browserRequests.indexOf(entry);
      const replacement = report.browserRequests.slice(index + 1).find(candidate =>
        candidate.path === entry.path && candidate.method === entry.method);
      return request.failure()?.errorText === 'net::ERR_ABORTED'
        && entry.path.endsWith('/construction-proposals') && replacement
        ? { expected: true, reason: `A later owned proposal request (${replacement.requestId}) superseded the prior request.` } : true;
    },
  });
  page.on('response', response => {
    if (response.status() < 400) return;
    const url = new URL(response.url());
    if (![new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin)) return;
    const incident = { path: url.pathname, status: response.status() };
    if (url.pathname.endsWith('/favicon.ico')) report.incidental.push(incident);
    else report.http.push(incident);
  });
  page.on('requestfailed', request => {
    const url = new URL(request.url());
    if (![new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin)) return;
    if (request.resourceType() === 'script') report.errors.push({ kind: 'module', path: url.pathname, error: sanitizeText(request.failure()?.errorText) });
  });
  await open();
  await checkCoverage(partialLongRoute?'partial-mapped-unmapped-coverage':'unmapped-parent-coverage',partialLongRoute?'3 selected · 2 produce rows · 1 needs attention':'1 selected · 0 produce rows · 1 needs attention');
  if(partialLongRoute){
    const coverageText=await inspectPage(page, () => document.querySelector('[data-testid="population-coverage-report"]')?.innerText ?? '');
    assert(coverageText.includes(report.oracle.unmapped.id),`Coverage must identify the independently unmapped Specimen ${report.oracle.unmapped.id}: ${coverageText}`);
  }
  const remove = page.getByRole('button', { name: 'Remove from collection', exact: true });
  await remove.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await remove.count(), 1, 'The saved parent connection must expose one removal control');
  assert.equal(await remove.isEnabled(), true, 'The saved parent connection must make an unmapped record removable');
  const before = builder.draftDigest;
  const start = Date.now();
  await click(page, '[role="dialog"][aria-label="Row definition settings"] [data-testid="population-coverage-report"] button', {name:'Remove from collection'});
  await page.waitForFunction(selectionId => {
    const panel = document.querySelector('[role="dialog"][aria-label="Row definition settings"] section[aria-label="Starting collection"]');
    const nextSelectionId = panel?.dataset.attachedSelectionRevisionId;
    return Boolean(nextSelectionId && nextSelectionId !== selectionId);
  }, selection.id, { timeout: 5000 });
  builder = await api(base+'/builder');
  const revised = builder.workspace.documents[0];
  assert.notEqual(builder.draftDigest,before);
  assert.notEqual(revised.population.selectionRevisionId,selection.id);
  assert.deepEqual(revised.population.route,original.population.route,'Excluding a record must preserve the exact connection');
  assert.deepEqual(revised.columns,original.columns);
  assert.deepEqual(revised.construction,original.construction);
  if(partialLongRoute){
    assert.deepEqual(revised.population.route,report.savedConnection,'Partial collection repair must retain the exact saved two-hop route');
    assert.equal(generation,'cda-fhir-v1');
    const variantID=revised.population.selectionRevisionId;
    const derived=await api(`${selections}/${variantID}?limit=100`);
    assert.equal(derived.revision.id,variantID);
    assert.equal(derived.revision.source.kind,'SELECTION_REVISION');
    assert.equal(derived.revision.source.revisionId,selection.id);
    assert.equal(derived.revision.source.membershipDigest,selection.membershipDigest);
    assert.equal(derived.revision.scopeDigest,scopeDigest);
    assert.equal(derived.revision.generation,generation);
    assert.equal(derived.revision.resourceType,'Specimen');
    assert.equal(derived.revision.memberCount,mappedResources.length);
    assert.deepEqual(derived.revision.exclusions,[{project,generation,resourceType:'Specimen',id:report.oracle.unmapped.id}]);
    assert.deepEqual(derived.members.map(member=>member.ref).sort((a,b)=>a.id.localeCompare(b.id)),expectedMappedRefs,'The native removal must leave exactly both independently mapped Specimens');
    const rawMembership=rawQuery(`FOR member IN loom_explorer_selection_members FILTER member.selectionId==${JSON.stringify(variantID)} AND member.project==${JSON.stringify(project)} AND member.generation==${JSON.stringify(generation)} AND member.resourceType=="Specimen" SORT member.id RETURN {id:member.id,project:member.project,generation:member.generation,resourceType:member.resourceType}`);
    assert.deepEqual(rawMembership,expectedMappedRefs,'Raw Arango membership must retain both mapped CDA Specimens');
    report.partialRepair={selectionRevisionId:variantID,membershipDigest:derived.revision.membershipDigest,retainedSpecimenIDs:mappedResources.map(mapped=>mapped.id),excludedSpecimenID:report.oracle.unmapped.id,expectedObservationIDs};
  }
  assert(Date.now()-start<=5000,'Collection repair must finish within five seconds');
  recordCase({name:'remove-unmapped-record',durationMs:Date.now()-start});
  await open(partialLongRoute?'partial-long-route-reload-raw-oracle':undefined);
  if(partialLongRoute){
    builder=await api(base+'/builder');
    const reloaded=builder.workspace.documents[0];
    assert.equal(builder.catalog.generation,generation);
    assert.deepEqual(reloaded.population.route,report.savedConnection,'Reload must preserve the exact saved Observation → Specimen → parent route');
    assert.equal(reloaded.population.selectionRevisionId,report.partialRepair.selectionRevisionId);
    const reloadedSelection=await api(`${selections}/${reloaded.population.selectionRevisionId}?limit=100`);
    assert.equal(reloadedSelection.revision.generation,generation);
    assert.equal(reloadedSelection.revision.resourceType,'Specimen');
    assert.deepEqual(reloadedSelection.members.map(member=>member.ref).sort((a,b)=>a.id.localeCompare(b.id)),expectedMappedRefs);
    await checkCoverage('partial-collection-reload','2 selected · 2 produce rows · 0 needs attention');
  }else await checkCoverage('empty-collection-reload','0 selected · 0 produce rows · 0 needs attention');
  if(longRoute){
    const clearStart=Date.now();
    await click(page,'[role="dialog"][aria-label="Row definition settings"] section[aria-label="Starting collection"] button',{name:'Use all authorized rows'});
    await returnToTable();
    const clearedTable = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
    await page.waitForFunction(() => {
      const preview = document.querySelector('[data-testid="preview-table-scroll"]');
      const table = preview?.querySelector('[role="table"]');
      return Boolean(preview && preview.getClientRects().length && table && table.getClientRects().length)
        && table.getAttribute('aria-rowcount') === '26';
    }, null, { timeout: 5000 });
    await clearedTable.waitFor({state:'visible',timeout:5000});
    const visibleIDs=await page.locator('[data-testid="preview-table-scroll"] [role="cell"]').allInnerTexts();
    assert(visibleIDs.length>0,'Authorized table must show records after clearing its collection');
    const clearDuration=Date.now()-clearStart;
    assert(clearDuration<=5000,'Clearing the collection must render visible authorized records within five seconds');
    const verifiedIDs=rawQuery(`FOR d IN Observation FILTER d.id IN ${JSON.stringify(visibleIDs)} AND d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN d.id`);
    assert.deepEqual([...new Set(visibleIDs)].sort(),verifiedIDs.sort(),'Visible rows must belong to the scoped CDA source');
    recordCase({name:'clear-long-collection-to-authorized-rows',durationMs:clearDuration,visibleIDs});
    const routeWaitStart=Date.now();
    await openRowSettings();
    const reattachPanel = page.locator('[role="dialog"][aria-label="Row definition settings"] section[aria-label="Starting collection"]');
    const connection = page.locator('[role="dialog"][aria-label="Row definition settings"] select[aria-label="Population connection"]');
    await page.waitForFunction(() => {
      const dialog = document.querySelector('[role="dialog"][aria-label="Row definition settings"]');
      const panel = dialog?.querySelector('section[aria-label="Starting collection"]');
      const visible = element => Boolean(element && element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden');
      const attach = [...(panel?.querySelectorAll('button') ?? [])].find(button => button.innerText.trim() === 'Use selected resources');
      const routeControl = panel?.querySelector('select[aria-label="Population connection"]');
      const otherConnections = [...(panel?.querySelectorAll('button') ?? [])].find(button => button.innerText.trim() === 'Other connections');
      const routeFact = [...(panel?.querySelectorAll('dd') ?? [])].some(visible);
      return visible(dialog) && visible(panel) && visible(attach) && !attach.disabled
        && (visible(routeControl) || visible(otherConnections) || routeFact);
    }, null, { timeout: 5000 });
    const routeWaitDuration=Date.now()-routeWaitStart;
    assert(routeWaitDuration<=5000,'The saved collection route must become selectable within five seconds');
    if(await connection.isVisible().catch(()=>false)){
      const options=await connection.locator('option').evaluateAll(nodes=>nodes.map(option=>({value:option.value,label:option.text})));
      const exact=options.find(option=>option.label.includes('via specimen_Specimen (outgoing,')&&option.label.includes('via parent (incoming,'));
      assert(exact,'The native dropdown must offer the exact previous connection: '+JSON.stringify(options));
      await selectOption(page,'[role="dialog"][aria-label="Row definition settings"] select[aria-label="Population connection"]',exact.value);
    }else{
      const routeFact=await reattachPanel.locator('dd').allInnerTexts();
      const expectedRouteText=report.connection.map(step=>`${step.fromResourceType} → ${step.relationship} → ${step.toResourceType}`).join(' / ');
      assert(routeFact.some(text=>text.includes(expectedRouteText)),`The sole visible native connection must show the exact saved path ${expectedRouteText}: ${JSON.stringify(routeFact)}`);
    }
    const attachStart=Date.now();
    await click(page,'[role="dialog"][aria-label="Row definition settings"] section[aria-label="Starting collection"] button',{name:'Use selected resources'});
    await page.waitForFunction(() => {
      const dialog = document.querySelector('[role="dialog"][aria-label="Row definition settings"]');
      const panel = dialog?.querySelector('section[aria-label="Starting collection"]');
      const action = [...(panel?.querySelectorAll('button') ?? [])].find(button => button.innerText.trim() === 'Use all authorized rows');
      const visible = element => Boolean(element && element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden');
      return visible(dialog) && visible(panel) && visible(action) && !action.disabled;
    }, null, { timeout: 5000 });
    await returnToTable();
    if(partialLongRoute) await assertPreviewIDs('reattached-partial-long-route-visible-preview');
    else {
      await page.waitForFunction(() => {
        const preview = document.querySelector('[data-testid="preview-table-scroll"]');
        const table = preview?.querySelector('[role="table"]');
        return Boolean(preview && preview.getClientRects().length && table && table.getClientRects().length)
          && table.getAttribute('aria-rowcount') === '1' && table.getAttribute('aria-colcount') === '1'
          && !document.body.innerText.includes('Loading your table…');
      }, null, { timeout: 5000 });
      assert.equal(await page.locator('[data-testid="preview-table-scroll"] [role="cell"]').count(),0,'The reattached empty long-route selection must show a visible header-only preview');
    }
    const attachDuration=Date.now()-attachStart;
    assert(attachDuration<=5000,'Reattaching the saved collection must render its exact visible rows within five seconds');
    builder=await api(base+'/builder');
    const attached=builder.workspace.documents[0];
    assert.deepEqual(attached.population,revised.population,'Native reattachment must preserve the revised selection and exact route direction');
    assert.deepEqual(attached.columns,original.columns);
    if(partialLongRoute){
      assert.equal(attached.population.selectionRevisionId,report.partialRepair.selectionRevisionId);
      assert.deepEqual(attached.population.route,report.savedConnection);
      const attachedSelection=await api(`${selections}/${attached.population.selectionRevisionId}?limit=100`);
      assert.equal(attachedSelection.revision.generation,generation);
      assert.deepEqual(attachedSelection.members.map(member=>member.ref).sort((a,b)=>a.id.localeCompare(b.id)),expectedMappedRefs);
    }
    recordCase({name:partialLongRoute?'reattach-repaired-multi-member-long-collection':'reattach-long-collection',durationMs:attachDuration,routeWaitDurationMs:routeWaitDuration});
    await open(partialLongRoute?'reattached-partial-long-route-reload-raw-oracle':undefined);
    if(partialLongRoute){
      builder=await api(base+'/builder');
      const reattachedReload=builder.workspace.documents[0];
      assert.deepEqual(reattachedReload.population,revised.population,'Reload after reattachment must preserve the exact two-member selection and route');
      const reloadedSelection=await api(`${selections}/${reattachedReload.population.selectionRevisionId}?limit=100`);
      assert.equal(reloadedSelection.revision.generation,generation);
      assert.deepEqual(reloadedSelection.members.map(member=>member.ref).sort((a,b)=>a.id.localeCompare(b.id)),expectedMappedRefs);
      await checkCoverage('reattached-partial-long-collection-reload','2 selected · 2 produce rows · 0 needs attention');
    }else await checkCoverage('reattached-long-collection-reload','0 selected · 0 produce rows · 0 needs attention');
  }
  await requestMonitor.flush();
  report.responses = report.browserRequests.filter(entry => entry.status !== undefined).map(({ path, status, response, requestId, method }) => ({ path, status, response, requestId, method }));
  report.exceptions = report.errors;
  assert.equal(report.http.length,0,JSON.stringify(report.http));
  assert.equal(report.exceptions.length,0,JSON.stringify(report.exceptions));
  report.status='passed';
} catch (error) {
  fatal = error;
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = await inspectPage(page, () => document.body.innerText).catch(()=>undefined);
} finally {
  await requestMonitor?.flush();
  report.finished = new Date().toISOString();
  await cda.attachReport('collection-repair-domain-report.json', report);
}

if (fatal || report.status === 'failed') throw fatal ?? new Error(report.error ?? 'Collection repair lifecycle failed');
return report;
}
