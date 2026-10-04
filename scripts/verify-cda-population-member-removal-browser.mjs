import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { assertExactPreviewMultiset, assertPopulationMemberRemovalApplyCommand, assertPopulationMemberRemovalProposal, shouldCapturePopulationMemberNativeResponse } from './lib/population-member-removal-proposal.mjs';

const sourceRoot = process.env.LOOM_SOURCE_FREEZE_ROOT ?? '/private/tmp/loom-construction-implementation';
const [{ captureSourceFreeze }, { sourceFingerprint }, apiBuild, browserTools, requestOwnership, nativeAbortProbe] = await Promise.all([
  import(pathToFileURL(join(sourceRoot, 'scripts/lib/source-freeze.mjs')).href),
  import(pathToFileURL(join(sourceRoot, 'scripts/verify-ui/source-fingerprint.mjs')).href),
  import(pathToFileURL(join(sourceRoot, 'scripts/lib/api-build-freeze.mjs')).href),
  import(pathToFileURL(join(sourceRoot, 'scripts/lib/browser.mjs')).href),
  import(pathToFileURL(join(sourceRoot, 'scripts/lib/native-request-ownership.mjs')).href),
  import(pathToFileURL(join(sourceRoot, 'scripts/lib/native-abort-probe.mjs')).href),
]);
const { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } = apiBuild;
const { browserEval, click, launchBrowser, navigate, waitForBrowser } = browserTools;
const { classifyExpectedOwnedCancellation, classifyNativeRequestOwnerRetirement } = requestOwnership;
const { installNativeAbortProbe, nativeAbortProbeEvidenceForRequest } = nativeAbortProbe;

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const explorer = `population-member-removal-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-cda-population-member-removal-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const apiBuildContainer = localCDAApiContainer();
const explorerRoot = `/api/v1/projects/${project}/explorers`;
const authoring = `${explorerRoot}/${explorer}/authoring/v2`;
const selections = authoring.replace('/authoring/v2', '/selections');
const scenario = 'builder-population-member-removal';
const caseName = 'mapped-plus-orphan-to-empty';
const requiredChecks = [
  'bounded independent CDA oracle proves one mapped and one orphan Specimen in the exact authorized project and generation',
  'saved source table contains the exact Observation population route followed by GROUP and RELATED_SOURCE COUNT',
  'opening native Rows settings reveals the attached starting collection and mapped member within five seconds',
  'mapped-member click automatically previews an exact base-minus-one candidate with the correct proposal, draft, output, snapshot, and scope bindings',
  'Cancel leaves the exact saved workspace, draft version, digest, and base selection attached',
  'reopening issues a fresh request for the current intent and returns its content-addressed candidate selection',
  'Apply sends only the exact APPLY_POPULATION_MEMBER_PROPOSAL command and attaches that proposal candidate',
  'candidate membership is the exact base membership minus the mapped member while the immutable base remains unchanged',
  'reload preserves project, generation, route, GROUP, RELATED_SOURCE, output columns, and the exact empty candidate preview',
  'native removal, Apply, and reload stay within five seconds; product API errors fail and incidental favicon 404s are recorded separately',
  'Undo restores the exact original collection, construction, columns and independently predicted populated rows after reload',
];
const report = {
  scenario, case: caseName, title: 'Mapped-plus-orphan source removal to empty GROUP→RELATED_SOURCE output', project, explorer,
  requiredChecks, assertions: [], cases: [], requests: [], nativeRequests: [], errors: [], incidentalErrors: [], oracle: {}, started: new Date().toISOString(),
  nativeAbortProbeEvents: [], frameNavigations: [], executionContextRetirements: [], expectedOwnerCancellations: [],
};
await mkdir(evidence, { recursive: true });

let browser;
let builder;
let outputId;
let source;
let baseSelection;
let sourceFreeze;
let frozenApiBuild;
let apiBuildCheckStarted = false;
const nativeById = new Map();
const networkById = new Map();
const networkRequests = [];
const pendingNetworkReads = new Set();
const failedRequests = [];
const networkFailures = [];
const defaultContexts = new Map();
const activeMainFrameLoader = new Map();
const summarizeNetworkRequests = () => networkRequests.map(entry => {
  const frameNavigation = report.frameNavigations.findLast(frame => frame.frameId === entry.frameId);
  let parsedUrl;
  try { parsedUrl = new URL(entry.url); } catch { parsedUrl = undefined; }
  return {
    requestId: entry.requestId, targetId: browser?.target?.id, url: entry.url, path: entry.path,
    method: entry.method, resourceType: entry.resourceType, startedAt: entry.startedAt,
    completedAt: entry.completedAt, status: entry.status, loadingFailed: entry.loadingFailed,
    requestTimestamp: entry.requestTimestamp, requestWallTime: entry.requestWallTime,
    frameId: entry.frameId, loaderId: entry.loaderId, initiator: entry.initiator,
    frameOwnership: frameNavigation ? {
      frameUrl: frameNavigation.url, frameOrigin: frameNavigation.origin,
      parentFrameId: frameNavigation.parentFrameId, isMainFrame: frameNavigation.isMainFrame,
      project: frameNavigation.project, explorer: frameNavigation.explorer,
      isAppOrigin: frameNavigation.origin === uiOrigin,
      requestOrigin: parsedUrl?.origin,
      isCurrentMainLoader: activeMainFrameLoader.get(entry.frameId)?.loaderId === entry.loaderId,
    } : { knownFrame: false, requestOrigin: parsedUrl?.origin },
    scopeProject: entry.scopeProject, scopeExplorer: entry.scopeExplorer,
    networkTerminal: entry.networkTerminal, bodyReadStatus: entry.bodyReadStatus,
    ownerRetirement: entry.ownerRetirement, expectedOwnerCancellation: entry.expectedOwnerCancellation,
  };
});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const mark = name => report.assertions.push({ name, status: 'passed' });
const initiatorSummary = initiator => initiator ? {
  type: initiator.type,
  url: initiator.url,
  requestId: initiator.requestId,
  stack: (initiator.stack?.callFrames ?? []).slice(0, 8).map(frame => ({
    functionName: frame.functionName,
    url: frame.url,
    lineNumber: frame.lineNumber,
    columnNumber: frame.columnNumber,
  })),
} : undefined;

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `population-member-removal-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, method: body ? 'POST' : 'GET', body, status: response.status, response: value });
  assert(response.ok, `${path} returned ${response.status}: ${JSON.stringify(value)}`);
  return value;
};

const rawQuery = query => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1',
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `raw Arango response did not contain JSON: ${result.stdout.slice(0, 1000)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};

const currentDocument = state => state.workspace.documents.find(document => document.output.id === outputId);
const refreshBuilder = async () => { builder = await api(`${authoring}/builder`); return builder; };
const command = async commands => {
  await api(`${authoring}/commands`, {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  return refreshBuilder();
};

const visibleRows = async () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length);`);
const assertRendered = async expectedRows => {
  await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading your table…')`);
  const rows = await visibleRows();
  assertExactPreviewMultiset(rows, expectedRows, 'visible source multiset');
};
const openBuilder = async expectedRows => {
  await refreshBuilder();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await waitForBrowser(browser.cdp, `(() => {const p=document.querySelector('[data-testid="construction-preview"]');return p?.dataset.previewStatus==='ready'&&p.dataset.currentDraftVersion===${JSON.stringify(String(builder.draftVersion))}&&p.dataset.currentDraftDigest===${JSON.stringify(builder.draftDigest)};})()`);
  await assertRendered(expectedRows);
};

const waitForMemberProposal = async (startAt, { expectedRows, expected }) => {
  const path = `${authoring}/population-member-proposals`;
  const deadline = startAt + 5000;
  let entry;
  while (!(entry = report.nativeRequests.findLast(request => request.path === path && request.startedAt >= startAt && request.completedAt && request.response))) {
    assert(Date.now() < deadline, 'Mapped-member impact proposal did not finish within five seconds');
    await sleep(25);
  }
  assert.equal(entry.status, 200, `Member removal proposal failed: ${JSON.stringify(entry.response)}`);
  const value = entry.response;
  const candidate = assertPopulationMemberRemovalProposal(value, expected);
  assert.equal(value.previewStatus, 'READY');
  assert(value.preview && value.preview.receiptId === value.proposalId && value.preview.outputId === outputId, 'Proposal must carry the candidate receipt preview for this exact table');
  assert.equal(value.preview.rowCount, expectedRows.length, 'Candidate preview row count must match independent raw route oracle');
  const byColumn = value.preview.rows.map(row => value.preview.columns.map(column => row[column.column] == null ? '—' : String(row[column.column])));
  assertExactPreviewMultiset(byColumn, expectedRows, 'candidate preview multiset');
  await waitForBrowser(browser.cdp, `(() => {const panel=document.querySelector('[data-testid="population-member-removal-proposal"]');return panel?.dataset.proposalId===${JSON.stringify(value.proposalId)}&&panel.dataset.previewStatus==='READY'&&panel.dataset.baseSelectionRevisionId===${JSON.stringify(value.baseSelection.id)}&&panel.dataset.candidateSelectionRevisionId===${JSON.stringify(candidate.id)};})()`);
  const panelPreview = await browserEval(browser.cdp, `return {target:document.querySelector('[data-testid="population-member-removal-target"]')?.innerText.trim(),table:Boolean(document.querySelector('[data-testid="population-member-candidate-preview"] table[aria-label="Candidate table after member removal"]')),rows:[...document.querySelectorAll('[data-testid="population-member-candidate-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
  assert.equal(panelPreview.target, `Remove ${expected.removedMember.resourceType}/${expected.removedMember.id}`);
  assert.equal(panelPreview.table, true, 'The UI must render the candidate table under the removal proposal');
  const panelPreviewRows = panelPreview.rows;
  assertExactPreviewMultiset(panelPreviewRows, expectedRows, 'member-proposal preview multiset');
  const durationMs = Date.now() - startAt;
  assert(durationMs <= 5000, `Removal click-to-ready proposal DOM took ${durationMs}ms`);
  report.cases.push({ name: 'mapped-member-auto-impact-preview', durationMs, proposalId: value.proposalId, candidateSelectionId: candidate.id, rowCount: value.preview.rowCount });
  return { entry, value, candidate };
};

const rawMembership = selectionId => rawQuery(`FOR member IN loom_explorer_selection_members FILTER member.selectionId==${JSON.stringify(selectionId)} AND member.project==${JSON.stringify(project)} AND member.generation==${JSON.stringify(generation)} AND member.resourceType=="Specimen" SORT member.id RETURN {project:member.project,generation:member.generation,resourceType:member.resourceType,id:member.id}`);

const run = async () => {
  report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze = { watchedFileCount: sourceFreeze.watchedFileCount };
  apiBuildCheckStarted = true;
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiBuildContainer));
  report.apiBuildFreeze = { container: apiBuildContainer, initial: frozenApiBuild.initial, invalidatesRun: true, productFailure: false };

  const query = `LET seeds=(FOR s IN Specimen FILTER s.resourceType=="Specimen" AND s.project==${JSON.stringify(project)} AND s.dataset_generation==${JSON.stringify(generation)} SORT s.id LIMIT 2000 RETURN s) FOR s IN seeds LET parents=(FOR e IN fhir_edge FILTER e._from==s._id AND e.label=="parent" AND e.from_type=="Specimen" AND e.to_type=="Specimen" AND e.project==${JSON.stringify(project)} AND e.dataset_generation==${JSON.stringify(generation)} LET parent=DOCUMENT(e._to) FILTER parent!=null AND parent.resourceType=="Specimen" AND parent.project==${JSON.stringify(project)} AND parent.dataset_generation==${JSON.stringify(generation)} RETURN parent._id) LET children=(FOR e IN fhir_edge FILTER e._to==s._id AND e.label=="parent" AND e.from_type=="Specimen" AND e.to_type=="Specimen" AND e.project==${JSON.stringify(project)} AND e.dataset_generation==${JSON.stringify(generation)} LET child=DOCUMENT(e._from) FILTER child!=null AND child.resourceType=="Specimen" AND child.project==${JSON.stringify(project)} AND child.dataset_generation==${JSON.stringify(generation)} RETURN child.id) LET routeRows=(FOR parentEdge IN fhir_edge FILTER parentEdge._from==s._id AND parentEdge.label=="parent" AND parentEdge.from_type=="Specimen" AND parentEdge.to_type=="Specimen" AND parentEdge.project==${JSON.stringify(project)} AND parentEdge.dataset_generation==${JSON.stringify(generation)} LET parent=DOCUMENT(parentEdge._to) FILTER parent!=null AND parent.resourceType=="Specimen" AND parent.project==${JSON.stringify(project)} AND parent.dataset_generation==${JSON.stringify(generation)} FOR specimenEdge IN fhir_edge FILTER specimenEdge._to==parent._id AND specimenEdge.label=="specimen_Specimen" AND specimenEdge.from_type=="Observation" AND specimenEdge.to_type=="Specimen" AND specimenEdge.project==${JSON.stringify(project)} AND specimenEdge.dataset_generation==${JSON.stringify(generation)} LET observation=DOCUMENT(specimenEdge._from) FILTER observation!=null AND observation.resourceType=="Observation" AND observation.project==${JSON.stringify(project)} AND observation.dataset_generation==${JSON.stringify(generation)} RETURN DISTINCT observation.id) RETURN {id:s.id,_id:s._id,parents,children,routeRows}`;
  const candidates = rawQuery(query);
  const mapped = candidates.find(candidate => candidate.parents.length > 0 && candidate.routeRows.length > 0 && candidate.routeRows.length <= 24);
  const mappedIds = new Set(mapped?.routeRows ?? []);
  const orphan = candidates.find(candidate => candidate.id !== mapped?.id && candidate.parents.length === 0 && candidate.children.length > 0 && candidate.routeRows.length === 0);
  assert(mapped && orphan, `Bounded project/generation oracle did not find a mapped+orphan Specimen pair: ${JSON.stringify(candidates.slice(0, 10))}`);
  const roots = rawQuery(`FOR o IN Observation FILTER o.id IN ${JSON.stringify(mapped.routeRows)} AND o.resourceType=="Observation" AND o.project==${JSON.stringify(project)} AND o.dataset_generation==${JSON.stringify(generation)} SORT o.id LET specimens=(FOR edge IN fhir_edge FILTER edge._from==o._id AND edge.label=="specimen_Specimen" AND edge.from_type=="Observation" AND edge.to_type=="Specimen" AND edge.project==o.project AND edge.dataset_generation==o.dataset_generation LET specimen=DOCUMENT(edge._to) FILTER specimen!=null AND specimen.resourceType=="Specimen" AND specimen.project==o.project AND specimen.dataset_generation==o.dataset_generation RETURN DISTINCT specimen.id) RETURN {id:o.id,_id:o._id,specimenIDs:SORTED_UNIQUE(specimens)}`);
  assert.deepEqual(roots.map(row => row.id).sort(), [...mappedIds].sort(), 'The independent root query must recover exactly the mapped member route rows');
  assert(roots.every(row => row.specimenIDs.length > 0), 'Every baseline Group row needs an exact related Specimen witness');
  source = { mapped, orphan, roots };
  const mappedRef = { project, generation, resourceType: 'Specimen', id: mapped.id };
  const orphanRef = { project, generation, resourceType: 'Specimen', id: orphan.id };
  const refs = [mappedRef, orphanRef].sort((a, b) => a.id.localeCompare(b.id));
  const baselineRows = roots.map(row => [row.id, '1', String(row.specimenIDs.length)]);
  const candidateRows = [];
  report.oracle = { query, mapped: { id: mapped.id, parentIDs: mapped.parents, rootObservationIDs: mapped.routeRows }, orphan: { id: orphan.id, childIDs: orphan.children }, roots, baselineRows, candidateRows, sourceRefs: refs };
  mark(requiredChecks[0]);

  await api(explorerRoot, { name: explorer, title: 'Mapped population member removal under Group related summary' });
  builder = await refreshBuilder();
  assert.equal(builder.catalog.generation, generation);
  const observation = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
  assert(observation, 'CDA catalog must contain the Observation row root');
  const beforeCreateDocuments = builder.workspace?.documents ?? [];
  assert(Array.isArray(beforeCreateDocuments), 'A present fresh workspace document list must be an array');
  const outputsBeforeCreate = new Set(beforeCreateDocuments.map(document => document.output.id));
  await command([{ type: 'CREATE_TABLE', title: 'Mapped member removal QA', rootNodeId: observation.nodeId }]);
  assert(Array.isArray(builder.workspace?.documents), 'CREATE_TABLE must materialize the fresh workspace');
  const createdOutputs = builder.workspace.documents.filter(document => !outputsBeforeCreate.has(document.output.id));
  assert.equal(createdOutputs.length, 1, 'CREATE_TABLE must add exactly one new output to this fresh Explorer');
  outputId = createdOutputs[0].output.id;
  const idCandidate = builder.catalog.candidates.find(item => item.nodeId === observation.nodeId && item.fieldPath === 'id');
  assert(idCandidate, 'Observation.id must be available as a stable Group key');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);
  baseSelection = await api(selections, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs } },
  });
  const routePage = await api(`${authoring}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: baseSelection.id, limit: 50,
  });
  const routeChoice = routePage.choices.find(choice => choice.route.length === 2 &&
    choice.route[0].fromResourceType === 'Observation' && choice.route[0].toResourceType === 'Specimen' &&
    choice.route[0].relationship === 'specimen_Specimen' && choice.route[0].storageDirection === 'OUTBOUND' &&
    choice.route[1].relationship === 'parent' && choice.route[1].storageDirection === 'INBOUND');
  assert(routeChoice, `Expected exact Observation→Specimen→parent route, got ${JSON.stringify(routePage.choices.map(choice => choice.route))}`);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: baseSelection.id, routeChoiceId: routeChoice.routeChoiceId }]);
  const pinnedBaseSelection = currentDocument(builder).population.selectionRevisionId;
  assert.equal(pinnedBaseSelection, baseSelection.id);
  assert.deepEqual(await rawMembership(baseSelection.id), refs, 'The independent persisted membership must be the exact two source refs');
  const page = await api(`${selections}/${baseSelection.id}?limit=10`);
  assert.deepEqual(page.members.map(member => member.ref).sort((a, b) => a.id.localeCompare(b.id)), refs);
  report.oracle.baseSelection = page.revision;

  browser = await launchBrowser(evidence, undefined, { initialUrl: 'about:blank' });
  report.browserTarget = browser.target;
  await installNativeAbortProbe(browser.cdp, {
    project,
    explorer,
    onEvent: event => report.nativeAbortProbeEvents.push(event),
  });
  browser.cdp.on('Runtime.executionContextCreated', ({ context }) => {
    const auxData = context.auxData ?? {};
    if (auxData.isDefault !== true || typeof auxData.frameId !== 'string') return;
    defaultContexts.set(context.id, {
      frameId: auxData.frameId, executionContextId: context.id, uniqueId: context.uniqueId, createdAt: Date.now(),
    });
  });
  browser.cdp.on('Runtime.executionContextDestroyed', ({ executionContextId, executionContextUniqueId }) => {
    const context = defaultContexts.get(executionContextId);
    if (!context) return;
    report.executionContextRetirements.push({
      ...context, ...(executionContextUniqueId ? { destroyedUniqueId: executionContextUniqueId } : {}),
      isDefault: true, eventType: 'Runtime.executionContextDestroyed', at: Date.now(),
    });
    defaultContexts.delete(executionContextId);
  });
  browser.cdp.on('Runtime.executionContextsCleared', () => {
    const at = Date.now();
    for (const context of defaultContexts.values()) report.executionContextRetirements.push({
      ...context, isDefault: true, eventType: 'Runtime.executionContextsCleared', at,
    });
    defaultContexts.clear();
  });
  browser.cdp.on('Page.frameNavigated', ({ frame }) => {
    let location;
    try { location = new URL(frame.url); } catch { return; }
    const committedAt = Date.now();
    report.frameNavigations.push({
      frameId: frame.id, parentFrameId: frame.parentId, loaderId: frame.loaderId,
      isMainFrame: !frame.parentId, at: committedAt, url: frame.url,
      origin: location.origin, path: location.pathname,
      project: location.searchParams.get('project'), explorer: location.searchParams.get('explorer'),
    });
    if (!frame.parentId && location.origin === uiOrigin) activeMainFrameLoader.set(frame.id, { loaderId: frame.loaderId, committedAt });
  });
  browser.cdp.on('Runtime.exceptionThrown', event => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Runtime.consoleAPICalled', event => { if (event.type === 'error') report.errors.push({ kind: 'console', args: event.args }); });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, wallTime, timestamp, type, initiator, frameId, loaderId }) => {
    const path = new URL(request.url).pathname;
    const scope = /^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)\//.exec(path);
    const correlationId = Object.entries(request.headers ?? {}).find(([name]) => name.toLowerCase() === 'x-request-id')?.[1];
    const entry = {
      requestId, url: request.url, path, method: request.method, resourceType: type, initiator, frameId, loaderId,
      requestTimestamp: timestamp, requestWallTime: wallTime,
      startedAt: wallTime ? Math.round(wallTime * 1000) : Date.now(),
      requestCorrelationId: correlationId,
      owningFrameWasCurrentMainLoader: activeMainFrameLoader.get(frameId)?.loaderId === loaderId,
      owningFrameLoaderCommittedAt: activeMainFrameLoader.get(frameId)?.committedAt,
      initiatorExecutionContexts: [...defaultContexts.values()]
        .filter(context => context.frameId === frameId)
        .map(({ executionContextId, uniqueId, createdAt }) => ({ executionContextId, uniqueId, createdAt })),
      bodyReadStatus: 'pending', networkTerminal: false,
      ...(scope ? { scopeProject: decodeURIComponent(scope[1]), scopeExplorer: decodeURIComponent(scope[2]) } : {}),
    };
    entry.initiator = initiatorSummary(initiator);
    if (request.postData) { try { entry.request = JSON.parse(request.postData); } catch { entry.request = request.postData.slice(0, 32768); } }
    networkById.set(requestId, entry);
    networkRequests.push(entry);
    if (shouldCapturePopulationMemberNativeResponse(entry, { project, explorer })) {
      entry.body = entry.request;
      nativeById.set(requestId, entry);
      report.nativeRequests.push(entry);
    }
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = networkById.get(requestId);
    if (!entry) return;
    entry.completedAt = Date.now();
    entry.networkTerminal = true;
    if (!shouldCapturePopulationMemberNativeResponse(entry, { project, explorer })) { entry.bodyReadStatus = 'complete'; return; }
    entry.bodyReadStatus = 'reading';
    const read = browser.cdp.send('Network.getResponseBody', { requestId }).then(result => {
      const body = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      try { entry.response = JSON.parse(body); } catch { entry.response = body.slice(0, 32768); }
      entry.bodyReadStatus = 'decoded';
    }).catch(error => {
      entry.responseReadError = String(error);
      entry.bodyReadStatus = 'failed';
      report.errors.push({ kind: 'native-response-body', path: entry.path, message: entry.responseReadError });
    }).finally(() => { entry.completedAt = Date.now(); pendingNetworkReads.delete(read); });
    pendingNetworkReads.add(read);
  });
  browser.cdp.on('Network.responseReceived', ({ response, requestId }) => {
    const entry = networkById.get(requestId);
    if (entry) {
      entry.status = response.status;
      entry.responseReceivedAt = Date.now();
      entry.serverRequestId = Object.entries(response.headers ?? {}).find(([name]) => name.toLowerCase() === 'x-request-id')?.[1];
    }
    if (entry && response.status >= 400) {
      const failure = { path: entry.path, status: response.status, body: entry.body };
      if (response.status === 404 && entry.path === '/favicon.ico') {
        report.incidentalErrors.push({ ...failure, reason: 'The local UI does not serve a favicon asset.' });
      } else {
        if (entry.path.startsWith('/api/v1/')) failedRequests.push(failure);
        report.errors.push({ kind: entry.path.startsWith('/api/v1/') ? 'http' : 'browser-asset-http', ...failure });
      }
    }
  });
  browser.cdp.on('Network.loadingFailed', ({ requestId, timestamp, type, errorText, canceled, blockedReason, corsErrorStatus }) => {
    const entry = networkById.get(requestId);
    const failure = {
      requestId, type, errorText, canceled: Boolean(canceled), blockedReason,
      corsErrorStatus: corsErrorStatus ? { code: corsErrorStatus.code, failedParameter: corsErrorStatus.failedParameter } : undefined,
      at: Date.now(),
    };
    networkFailures.push(failure);
    if (entry) {
      entry.loadingFailed = { timestamp, ...failure };
      entry.networkTerminal = true;
      entry.bodyReadStatus = 'failed';
      entry.bodyError = `Network.loadingFailed: ${errorText}`;
      if (nativeById.has(requestId)) entry.abortControllerProbeEvidence = nativeAbortProbeEvidenceForRequest(entry, report.nativeAbortProbeEvents);
      const ownership = nativeById.has(requestId) ? classifyExpectedOwnedCancellation(entry, {
        frameNavigations: report.frameNavigations,
        executionContextRetirements: report.executionContextRetirements,
      }) : { expected: false, reason: 'request-is-not-a-captured-native-authoring-request' };
      failure.ownership = ownership;
      if (!ownership.expected) report.errors.push({ kind: nativeById.has(requestId) ? 'native-request' : 'network-request', path: entry.path, ...failure });
    } else {
      report.errors.push({ kind: 'unowned-network-failure', ...failure });
    }
  });

  const drainNativeNetwork = async () => {
    const deadline = Date.now() + 5000;
    const collectExpectedCancellations = () => {
      for (const entry of report.nativeRequests) {
        if (!entry.loadingFailed || entry.expectedOwnerCancellation) continue;
        entry.abortControllerProbeEvidence = nativeAbortProbeEvidenceForRequest(entry, report.nativeAbortProbeEvents);
        const decision = classifyExpectedOwnedCancellation(entry, {
          frameNavigations: report.frameNavigations,
          executionContextRetirements: report.executionContextRetirements,
        });
        if (!decision.expected) continue;
        entry.expectedOwnerCancellation = decision;
        const failure = networkFailures.find(item => item.requestId === entry.requestId);
        if (failure) failure.ownership = decision;
        report.expectedOwnerCancellations.push({
          requestId: entry.requestId, requestCorrelationId: entry.requestCorrelationId, path: entry.path,
          owner: decision.ownerRetirement.owner, loadingFailed: entry.loadingFailed, ownerRetirement: decision.ownerRetirement,
        });
        for (const error of report.errors) {
          if (error.requestId === entry.requestId && (error.kind === 'native-request' || error.kind === 'network-request')) {
            error.expectedOwnerCancellation = true;
            error.ownerRetirement = decision.ownerRetirement;
          }
        }
      }
    };
    const nativePendingOwnerRetirement = () => {
      const pending = report.nativeRequests.filter(request =>
        !request.networkTerminal || request.bodyReadStatus === 'pending' || request.bodyReadStatus === 'reading');
      return pending.map(entry => ({ entry, decision: classifyNativeRequestOwnerRetirement(entry, {
        frameNavigations: report.frameNavigations,
        executionContextRetirements: report.executionContextRetirements,
      }) })).filter(item => item.decision.ownerRetired);
    };
    let quietSince;
    let observedCount = networkRequests.length;
    while (Date.now() < deadline) {
      collectExpectedCancellations();
      const retiredOwners = nativePendingOwnerRetirement();
      for (const { entry, decision } of retiredOwners) entry.ownerRetirement = { ...decision, observedAt: Date.now() };
      const retired = new Set(retiredOwners.map(({ entry }) => entry.requestId));
      const pending = networkRequests.filter(request => !request.networkTerminal && !retired.has(request.requestId));
      const unresolvedBodies = report.nativeRequests.filter(request =>
        request.networkTerminal && request.bodyReadStatus === 'failed' && request.expectedOwnerCancellation?.expected !== true);
      if (!pending.length && !unresolvedBodies.length && pendingNetworkReads.size === 0) {
        if (networkRequests.length !== observedCount) {
          observedCount = networkRequests.length;
          quietSince = undefined;
        }
        quietSince ??= Date.now();
        if (Date.now() - quietSince >= 200) return;
      } else {
        quietSince = undefined;
      }
      await sleep(25);
    }
    collectExpectedCancellations();
    const pending = networkRequests.filter(request => !request.networkTerminal).map(request => ({ requestId: request.requestId, path: request.path }));
    assert.deepEqual(pending, [], 'All observed browser network requests must drain before case completion');
    assert.equal(pendingNetworkReads.size, 0, 'Native response bodies must drain before case completion');
    assert.deepEqual(report.nativeRequests.filter(request => request.bodyReadStatus !== 'decoded' &&
      request.ownerRetirement?.ownerRetired !== true && request.expectedOwnerCancellation?.expected !== true), [],
    'Every native request body must decode or have strict evidence of owner retirement/cancellation');
  };

  const expectProposalForConstruction = async (startedAt, expectedRows) => {
    const deadline = startedAt + 5000;
    let entry;
    while (!(entry = report.nativeRequests.findLast(request => /\/construction-(?:choice-)?proposals$/.test(request.path) && request.startedAt >= startedAt && request.completedAt && request.response))) {
      assert(Date.now() < deadline, 'Construction proposal did not complete within five seconds');
      await sleep(25);
    }
    assert.equal(entry.status, 200, JSON.stringify(entry.response));
    assert.equal(entry.response.previewStatus, 'READY', JSON.stringify(entry.response));
    const preview = entry.response.preview;
    const rows = preview.rows.map(row => preview.columns.map(column => row[column.column] == null ? '—' : String(row[column.column])));
    assertExactPreviewMultiset(rows, expectedRows, 'construction proposal multiset');
    return entry;
  };
  const applyConstruction = async (expectedRows, panel = 'construction-proposal-panel') => {
    const start = Date.now();
    if (panel === 'construction-choice-proposal-panel') await click(browser.cdp, `[data-testid="${panel}"] button`, { name: 'Apply columns' });
    else await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="${panel}"]')`);
    await assertRendered(expectedRows);
    assert(Date.now() - start <= 5000, 'Construction Apply must render within five seconds');
    await refreshBuilder();
  };

  await openBuilder(roots.map(row => [row.id]));
  let started = Date.now();
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-group-rows"]:not(:disabled)')`);
  await click(browser.cdp, '[data-testid="construction-action-group-rows"]');
  await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Group by Observation ID"]:not(:disabled)')`);
  await click(browser.cdp, 'input[aria-label="Group by Observation ID"]');
  await expectProposalForConstruction(started, roots.map(row => [row.id, '1']));
  await applyConstruction(roots.map(row => [row.id, '1']));

  const openRelatedFieldChooser = async () => {
    await click(browser.cdp, '[data-testid="construction-action-add-columns"]');
    await click(browser.cdp, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-add-columns-source"]')`);
    if (!await browserEval(browser.cdp, `return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open===true;`)) await click(browser.cdp, '[aria-label="Related resources"] summary');
    await click(browser.cdp, '[data-testid="construction-add-columns-source-option"][aria-label="Specimen, Related resource"]');
    if (!await browserEval(browser.cdp, `return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open===true;`)) await click(browser.cdp, '[data-testid="feature-catalog-raw-fields"] summary');
    await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Select Specimen.id"]:not(:disabled)')`);
  };
  const configureRelated = async () => {
    await openRelatedFieldChooser();
    await click(browser.cdp, 'input[aria-label="Select Specimen.id"]');
    await click(browser.cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    await waitForBrowser(browser.cdp, `document.querySelector('[role="dialog"]')`);
    if (!await browserEval(browser.cdp, `return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open===true;`)) await click(browser.cdp, '[role="dialog"] summary', { includes: 'Other relationship paths' });
    const relationLabel = 'Observation -[specimen]-> Specimen';
    const pathSelector = `[role="dialog"] input[aria-label=${JSON.stringify(`Specimen ID: ${relationLabel}`)}]`;
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(pathSelector)})`);
    await click(browser.cdp, pathSelector);
    await click(browser.cdp, '[role="dialog"] input[aria-label="Specimen ID: Count matching records"]');
    const proposalStarted = Date.now();
    await click(browser.cdp, '[role="dialog"] button', { name: 'Add 1 column' });
    await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus) || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`);
    const panel = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?'construction-choice-proposal-panel':'construction-proposal-panel';`);
    const entry = await expectProposalForConstruction(proposalStarted, baselineRows);
    await applyConstruction(baselineRows, panel);
    return entry;
  };
  await configureRelated();
  await openBuilder(baselineRows);
  await refreshBuilder();
  const originalWorkspace = structuredClone(builder.workspace);
  const originalVersion = builder.draftVersion;
  const originalDigest = builder.draftDigest;
  const savedDocument = structuredClone(currentDocument(builder));
  const groupStep = savedDocument.construction.steps.find(step => step.operation.kind === 'GROUP');
  const relatedStep = savedDocument.construction.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
  assert(groupStep && relatedStep, 'Baseline must save GROUP followed by RELATED_SOURCE');
  assert(savedDocument.construction.steps.indexOf(groupStep) < savedDocument.construction.steps.indexOf(relatedStep));
  assert.equal(relatedStep.operation.relatedSource.form, 'COUNT');
  assert.equal(relatedStep.operation.relatedSource.source.resourceType, 'Specimen');
  assert.equal(relatedStep.operation.relatedSource.source.path, 'id');
  assert.deepEqual(savedDocument.population.route, currentDocument(builder).population.route);
  mark(requiredChecks[1]);
  const baseHeader = (await api(`${selections}/${baseSelection.id}?limit=10`)).revision;
  assert.equal(baseHeader.project, project);
  assert.equal(baseHeader.generation, generation);
  assert.equal(baseHeader.resourceType, 'Specimen');
  assert.equal(baseHeader.scopeDigest, builder.catalog.authorizationScopeDigest, 'The saved selection must use the currently authorized scope');
  report.baseline = { draftVersion: originalVersion, draftDigest: originalDigest, selection: baseHeader, route: savedDocument.population.route, groupStep, relatedStep, document: savedDocument };

  await openBuilder(baselineRows);
  const removeName = `Review removal of Specimen/${source.mapped.id}`;
  const rowSettingsStarted = Date.now();
  const rowSettingsDeadline = rowSettingsStarted + 5000;
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]', { name: 'Configure rows' }, Math.max(1, rowSettingsDeadline - Date.now()));
  const attachedCollectionReady = `(()=>{const dialog=document.querySelector('[role="dialog"][aria-label="Row definition settings"]');const collection=dialog?.querySelector('section[aria-label="Starting collection"]');const members=collection?.querySelector('[data-testid="population-member-list"]');return Boolean(dialog&&collection&&members&&[...members.querySelectorAll('button')].some(button=>(button.getAttribute('aria-label')||button.innerText).trim()===${JSON.stringify(removeName)}));})()`;
  await waitForBrowser(browser.cdp, attachedCollectionReady, Math.max(1, rowSettingsDeadline - Date.now()));
  const collectionState = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"][aria-label="Row definition settings"]');const collection=dialog?.querySelector('section[aria-label="Starting collection"]');return {dialog:Boolean(dialog),selectionId:collection?.dataset.selectionRevisionId,attachedSelectionId:collection?.dataset.attachedSelectionRevisionId,memberList:Boolean(collection?.querySelector('[data-testid="population-member-list"]'))};`);
  const rowSettingsDurationMs = Date.now() - rowSettingsStarted;
  assert(rowSettingsDurationMs <= 5000, `Rows settings to attached mapped-member control took ${rowSettingsDurationMs}ms`);
  assert.deepEqual(collectionState, { dialog: true, selectionId: baseSelection.id, attachedSelectionId: baseSelection.id, memberList: true });
  mark(requiredChecks[2]);
  report.cases.push({ name: 'open-row-settings-to-attached-mapped-member', durationMs: rowSettingsDurationMs, selectionId: baseSelection.id });
  const expectedBaseBinding = {
    outputId, snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
    project, generation, resourceType: 'Specimen', scopeDigest: baseHeader.scopeDigest,
    baseSelectionId: baseHeader.id, baseMembershipDigest: baseHeader.membershipDigest, baseMemberCount: baseHeader.memberCount,
    removedMember: { project, generation, resourceType: 'Specimen', id: source.mapped.id },
  };
  assert.equal(builder.draftVersion, originalVersion);
  assert.equal(builder.draftDigest, originalDigest);

  const clickRemoval = async () => {
    const start = Date.now();
    await click(browser.cdp, '[data-testid="population-member-list"] button', { name: removeName });
    return waitForMemberProposal(start, { expectedRows: candidateRows, expected: expectedBaseBinding });
  };
  const firstProposal = await clickRemoval();
  assert.deepEqual(firstProposal.entry.body.removedMember, expectedBaseBinding.removedMember);
  assert.equal(firstProposal.entry.body.baseSelectionRevisionId, baseHeader.id);
  assert.equal(firstProposal.entry.body.outputId, outputId);
  assert.equal(firstProposal.entry.body.expectedDraftVersion, originalVersion);
  assert.equal(firstProposal.entry.body.expectedDraftDigest, originalDigest);
  mark(requiredChecks[3]);
  const firstProposalID = firstProposal.value.proposalId;
  const firstCandidateID = firstProposal.candidate.id;
  const firstPanel = await browserEval(browser.cdp, `const p=document.querySelector('[data-testid="population-member-removal-proposal"]');return {proposalId:p?.dataset.proposalId,baseSelectionId:p?.dataset.baseSelectionRevisionId,candidateSelectionId:p?.dataset.candidateSelectionRevisionId,previewStatus:p?.dataset.previewStatus};`);
  assert.deepEqual(firstPanel, { proposalId: firstProposalID, baseSelectionId: baseHeader.id, candidateSelectionId: firstCandidateID, previewStatus: 'READY' });
  const cancelStarted = Date.now();
  await click(browser.cdp, 'button', { name: 'Cancel member removal' });
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="population-member-removal-proposal"]') && !document.querySelector('[data-testid="population-member-removal-proposal-panel"]')`);
  await assertRendered(baselineRows);
  const afterCancel = await refreshBuilder();
  const cancelDurationMs = Date.now() - cancelStarted;
  assert(cancelDurationMs <= 5000, `Cancel-to-unchanged-render took ${cancelDurationMs}ms`);
  assert.equal(networkRequests.filter(request => request.path === `${authoring}/commands` &&
    request.startedAt >= cancelStarted && request.startedAt <= Date.now()).length, 0,
  'Cancel must not issue an authoring command');
  assert.equal(afterCancel.draftVersion, originalVersion, 'Cancel must not advance the draft version');
  assert.equal(afterCancel.draftDigest, originalDigest, 'Cancel must not change the draft digest');
  assert.deepEqual(afterCancel.workspace, originalWorkspace, 'Cancel must not attach the candidate selection or change any document');
  assert.equal(currentDocument(afterCancel).population.selectionRevisionId, baseSelection.id);
  mark(requiredChecks[4]);
  report.cases.push({ name: 'cancel-leaves-base-draft-and-selection-attached', durationMs: cancelDurationMs, draftVersion: afterCancel.draftVersion, selectionId: currentDocument(afterCancel).population.selectionRevisionId });

  const secondProposal = await clickRemoval();
  assert.notEqual(secondProposal.entry.requestId, firstProposal.entry.requestId, 'Reopening must issue a fresh native proposal request');
  assert(secondProposal.entry.startedAt > firstProposal.entry.startedAt, 'Reopened proposal must belong to the current member-removal intent');
  assert.equal(secondProposal.entry.body.expectedDraftVersion, originalVersion);
  assert.equal(secondProposal.entry.body.expectedDraftDigest, originalDigest);
  assert.equal(secondProposal.entry.body.baseSelectionRevisionId, baseHeader.id);
  report.cases.push({ name: 'reopen-uses-fresh-request-and-exact-base-cas', durationMs: secondProposal.entry.completedAt - secondProposal.entry.startedAt, proposalId: secondProposal.value.proposalId, candidateSelectionId: secondProposal.candidate.id });
  mark(requiredChecks[5]);
  const applyStarted = Date.now();
  await click(browser.cdp, 'button', { name: 'Apply member removal' });
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="population-member-removal-proposal"]') && !document.querySelector('[data-testid="population-member-removal-proposal-panel"]')`);
  await waitForBrowser(browser.cdp, `(() => {const p=document.querySelector('[data-testid="construction-preview"]');return p?.dataset.previewStatus==='ready'&&p.dataset.currentDraftDigest!==${JSON.stringify(originalDigest)};})()`);
  await assertRendered(candidateRows);
  const applyDomEnded = Date.now();
  assert(applyDomEnded - applyStarted <= 5000, `Apply click-to-updated-preview DOM took ${applyDomEnded - applyStarted}ms`);
  const afterApply = await refreshBuilder();
  assert(Date.now() - applyStarted <= 5000, `Apply click through refreshed draft state took ${Date.now() - applyStarted}ms`);
  assert(afterApply.draftVersion > originalVersion, 'Applying the exact proposal must advance the draft');
  assert.notEqual(afterApply.draftDigest, originalDigest);
  const appliedDocument = currentDocument(afterApply);
  assert.equal(appliedDocument.population.selectionRevisionId, secondProposal.candidate.id, 'Apply must attach the exact candidate revision returned by the open proposal');
  assert.deepEqual(appliedDocument.population.route, savedDocument.population.route, 'Apply must preserve the exact authenticated route');
  assert.deepEqual(appliedDocument.construction, savedDocument.construction, 'Apply must preserve GROUP and RELATED_SOURCE identities and bindings');
  assert.deepEqual(appliedDocument.columns, savedDocument.columns, 'Apply must preserve all stable output columns');
  const applyEntry = assertPopulationMemberRemovalApplyCommand(networkRequests, {
    path: `${authoring}/commands`, startedAt: applyStarted, endedAt: applyDomEnded,
    snapshotToken: expectedBaseBinding.snapshotToken, draftVersion: originalVersion, draftDigest: originalDigest,
    outputId, proposalId: secondProposal.value.proposalId, scopeProject: project, scopeExplorer: explorer,
  });
  assert.deepEqual(applyEntry.body.commands, [{ type: 'APPLY_POPULATION_MEMBER_PROPOSAL', outputId, proposalId: secondProposal.value.proposalId }], 'Apply must send only the purpose-bound member-removal command');
  mark(requiredChecks[6]);
  report.cases.push({ name: 'apply-exact-proposal-to-empty-related-source', durationMs: Date.now() - applyStarted, selectionId: secondProposal.candidate.id });

  const candidatePage = await api(`${selections}/${secondProposal.candidate.id}?limit=10`);
  const expectedRemaining = [orphanRef];
  assert.equal(candidatePage.revision.id, secondProposal.candidate.id);
  assert.equal(candidatePage.revision.source.kind, 'SELECTION_REVISION');
  assert.equal(candidatePage.revision.source.revisionId, baseHeader.id);
  assert.equal(candidatePage.revision.source.membershipDigest, baseHeader.membershipDigest);
  assert.deepEqual(candidatePage.revision.exclusions, [expectedBaseBinding.removedMember]);
  assert.equal(candidatePage.revision.scopeDigest, baseHeader.scopeDigest);
  assert.equal(candidatePage.revision.project, project);
  assert.equal(candidatePage.revision.generation, generation);
  assert.equal(candidatePage.revision.resourceType, 'Specimen');
  assert.equal(candidatePage.revision.memberCount, 1);
  assert.deepEqual(candidatePage.members.map(member => member.ref), expectedRemaining);
  assert.deepEqual(await rawMembership(baseSelection.id), refs, 'Base immutable membership must remain unchanged after applying its derived revision');
  assert.deepEqual(await rawMembership(secondProposal.candidate.id), expectedRemaining, 'Candidate membership must be exactly the original set minus the mapped source');
  mark(requiredChecks[7]);
  report.afterApply = { selection: candidatePage.revision, pageMembers: candidatePage.members, rawBase: await rawMembership(baseSelection.id), rawCandidate: await rawMembership(secondProposal.candidate.id) };

  const reloadStarted = Date.now();
  await openBuilder(candidateRows);
  const reloadDurationMs = Date.now() - reloadStarted;
  assert(reloadDurationMs <= 5000, `Reload-to-render took ${reloadDurationMs}ms`);
  report.cases.push({ name: 'reload-applied-candidate-to-render', durationMs: reloadDurationMs });
  const reloaded = await refreshBuilder();
  const reloadedDocument = currentDocument(reloaded);
  assert.equal(reloadedDocument.population.selectionRevisionId, secondProposal.candidate.id, 'Reload must retain the applied candidate selection revision');
  assert.deepEqual(reloadedDocument.population.route, savedDocument.population.route);
  assert.deepEqual(reloadedDocument.construction, savedDocument.construction);
  assert.deepEqual(reloadedDocument.columns, savedDocument.columns);
  assert.equal(reloaded.catalog.generation, generation);
  const visible = await browserEval(browser.cdp, `const p=document.querySelector('[data-testid="construction-preview"]');return {status:p?.dataset.previewStatus,receiptId:p?.dataset.previewReceiptId,outputId:p?.dataset.previewOutputId,draftVersion:p?.dataset.currentDraftVersion,draftDigest:p?.dataset.currentDraftDigest};`);
  assert.equal(visible.status, 'ready');
  assert.equal(visible.outputId, outputId);
  assert.equal(visible.draftVersion, String(reloaded.draftVersion));
  assert.equal(visible.draftDigest, reloaded.draftDigest);
  const finalPreviewRequest = report.nativeRequests.findLast(request => request.path === `${authoring}/preview` && request.body?.receiptId === visible.receiptId && request.status === 200 && request.completedAt);
  assert(finalPreviewRequest, 'Reload must issue a successful saved-preview request for the exact receipt visible in the UI');
  const reread = await api(`${authoring}/preview`, { receiptId: visible.receiptId, outputId, limit: 100 });
  assert.equal(reread.receiptId, visible.receiptId);
  assert.equal(reread.outputId, outputId);
  assert.equal(reread.rowCount, 0);
  assert.deepEqual(reread.rows, [], 'Receipt-bound reload preview must contain no surviving related-source rows');
  mark(requiredChecks[8]);
  const undoStarted = Date.now();
  await click(browser.cdp, 'button', { name: 'Undo last saved draft change' });
  await waitForBrowser(browser.cdp, `(() => {const p=document.querySelector('[data-testid="construction-preview"]');return p?.dataset.previewStatus==='ready'&&p.dataset.currentDraftDigest!==${JSON.stringify(reloaded.draftDigest)};})()`);
  await assertRendered(baselineRows);
  const restored = await refreshBuilder();
  assert.equal(currentDocument(restored).population.selectionRevisionId, baseSelection.id);
  assert.deepEqual(currentDocument(restored).construction, savedDocument.construction);
  assert.deepEqual(currentDocument(restored).columns, savedDocument.columns);
  assert.deepEqual(await rawMembership(baseSelection.id), refs);
  const undoMs = Date.now() - undoStarted;
  assert(undoMs <= 5000, `Undo-to-restored rows took ${undoMs}ms`);
  report.cases.push({ name: 'undo-restores-original-membership-and-rows', durationMs: undoMs });
  const restorationReloadStarted = Date.now();
  await openBuilder(baselineRows);
  const restoredReload = await refreshBuilder();
  assert.equal(currentDocument(restoredReload).population.selectionRevisionId, baseSelection.id);
  assert.deepEqual(currentDocument(restoredReload).construction, savedDocument.construction);
  assert.deepEqual(currentDocument(restoredReload).columns, savedDocument.columns);
  const restorationReloadMs = Date.now() - restorationReloadStarted;
  assert(restorationReloadMs <= 5000, `Restoration reload took ${restorationReloadMs}ms`);
  report.cases.push({ name: 'reload-restored-original-collection-and-rows', durationMs: restorationReloadMs });
  mark(requiredChecks[10]);
  await drainNativeNetwork();
  report.networkFailures = networkFailures;
  report.networkRequests = summarizeNetworkRequests();
  report.nativeResponseDrain = {
    status: 'complete',
    decodedBodies: report.nativeRequests.filter(request => request.bodyReadStatus === 'decoded').length,
    ownerRetiredPending: report.nativeRequests.filter(request => request.ownerRetirement?.ownerRetired === true).map(request => ({
      requestId: request.requestId, path: request.path, ownerRetirement: request.ownerRetirement,
    })),
    expectedOwnerCancellations: report.expectedOwnerCancellations,
  };
  assert.deepEqual(report.errors.filter(error => error.expectedOwnerCancellation !== true), [], 'Native browser path must not issue unexpected failed requests or console/runtime errors');
  assert.deepEqual(failedRequests, [], 'No product API request may fail in the removal lifecycle');
  assert(networkFailures.every(failure => failure.ownership?.expected === true), 'Every failed browser request must be an explicitly proven owned cancellation');
  assert(report.cases.some(testCase => testCase.name === 'mapped-member-auto-impact-preview' && testCase.durationMs <= 5000));
  assert(report.cases.some(testCase => testCase.name === 'apply-exact-proposal-to-empty-related-source' && testCase.durationMs <= 5000));
  assert(report.cases.some(testCase => testCase.name === 'reload-applied-candidate-to-render' && testCase.durationMs <= 5000));
  mark(requiredChecks[9]);
  report.status = 'passed';
};

try {
  await run();
} catch (error) {
  report.status = error instanceof ApiBuildFreezeError ? 'invalidated' : 'failed';
  report.error = String(error?.stack ?? error);
  process.exitCode = 1;
  if (error instanceof ApiBuildFreezeError) report.apiBuildFreeze = { ...report.apiBuildFreeze, invalidatesRun: true, productFailure: false, error: String(error), reason: error.reason };
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  while (pendingNetworkReads.size) await Promise.all([...pendingNetworkReads]);
  report.networkRequests ??= summarizeNetworkRequests();
  report.networkFailures ??= networkFailures;
  if (sourceFreeze) {
    try { report.sourceFreeze = { ...report.sourceFreeze, ...await sourceFreeze.assertUnchanged() }; }
    catch (error) { report.status = 'invalidated'; report.sourceFreeze = { ...report.sourceFreeze, invalidatesRun: true, productFailure: false, changedPaths: error.changedPaths }; process.exitCode = 1; }
  }
  if (apiBuildCheckStarted && frozenApiBuild) {
    try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...await frozenApiBuild.assertUnchanged() }; }
    catch (error) { report.status = 'invalidated'; report.apiBuildFreeze = { ...report.apiBuildFreeze, invalidatesRun: true, productFailure: false, error: String(error) }; process.exitCode = 1; }
  }
  if (report.sourceFingerprint) {
    report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
    report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === report.sourceFingerprint.after.sha256 && report.sourceFingerprint.before.files === report.sourceFingerprint.after.files;
    report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
    report.sourceFingerprint.productFailure = false;
    if (!report.sourceFingerprint.unchanged) { report.status = 'invalidated'; process.exitCode = 1; }
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases, error: report.error }));
