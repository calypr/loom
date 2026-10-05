import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { assertExactPreviewMultiset, assertPopulationMemberRemovalApplyCommand, assertPopulationMemberRemovalProposal, shouldCapturePopulationMemberNativeResponse } from '../lib/population-member-removal-proposal.mjs';
import { captureCDARequests } from '../lib/cda-playwright-requests.mjs';
import { captureSourceFreeze } from '../lib/source-freeze.mjs';
import { sourceFingerprint } from './source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp, ApiBuildFreezeError } from '../lib/api-build-freeze.mjs';

const sensitiveName = /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i;
const sanitizeText = value => String(value ?? '')
  .replaceAll(process.cwd(), '$CHECKOUT')
  .replace(/(?:file:\/\/)?\/(?:private\/)?tmp\/[^\s)]+/g, '$TMP/<path>')
  .replace(/\/Users\/[^/\s]+/g, '$HOME')
  .replace(/\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi, 'Bearer [REDACTED]')
  .replace(/["']?[\w-]*(?:token|authorization|set-cookie|cookie|password|passwd|secret|credential|session(?:[_-]?id)?|api[_-]?key)[\w-]*["']?\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^,;\s}\]]+)/gi, '[REDACTED]')
  .replace(/<input\b[^>]*>/gi, tag => sensitiveName.test(tag)
    ? tag.replace(/(\bvalue\s*=\s*)(["'])(.*?)\2/gi, '$1$2[REDACTED]$2')
    : tag)
  .replace(/\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\b/g, '[REDACTED_TOKEN]')
  .replace(/\bsk-[A-Za-z0-9]{16,}\b/g, '[REDACTED_TOKEN]');
const sanitizePayload = (value, key = '') => {
  if (sensitiveName.test(key)) return '[REDACTED]';
  if (typeof value === 'string') return sanitizeText(value);
  if (Array.isArray(value)) return value.map(item => sanitizePayload(item));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, sanitizePayload(childValue, childKey)]));
  return value;
};
const sanitizeBody = body => {
  const text = String(body ?? '').slice(0, 12000);
  try { return JSON.stringify(sanitizePayload(JSON.parse(text))); } catch { return sanitizeText(text); }
};

export async function populationMemberRemovalWorkflow(page, nativeReport, action, check, fault, context) {
  const env = { ...process.env, ...(context.env ?? {}) };
  const target = context.target ?? {};
  const project = context.project ?? target.project ?? target.fixtureProject ?? env.LOOM_CDA_PROJECT;
  const generation = 'cda-fhir-v1';
  const explorer = context.explorer ?? target.explorer ?? env.LOOM_CDA_EXPLORER ?? `population-member-removal-${Date.now()}`;
  assert(/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(explorer), 'LOOM_CDA_EXPLORER must be a simple owned Explorer identifier.');
  assert.notEqual(explorer, 'cda-builder-full-qa-1790440983382', 'The shared protected CDA Explorer is not an owned test target');
  const apiOrigin = String(context.apiOrigin ?? target.apiOrigin ?? target.apiUrl ?? env.LOOM_CDA_API_ORIGIN ?? '').replace(/\/$/, '');
  const uiOrigin = String(context.uiOrigin ?? target.uiOrigin ?? target.uiUrl ?? env.LOOM_CDA_UI_ORIGIN ?? '').replace(/\/$/, '');
  const apiContainer = target.apiContainer ?? env.LOOM_CDA_API_CONTAINER;
  const arangoContainer = target.arangoContainer ?? env.LOOM_CDA_ARANGO_CONTAINER ?? env.LOOM_ARANGO_CONTAINER;
  const composeProject = target.composeProject ?? env.LOOM_CDA_COMPOSE_PROJECT;
  const sourceRoot = target.sourceRoot ?? env.LOOM_SOURCE_FREEZE_ROOT ?? fileURLToPath(new URL('../..', import.meta.url));
  assert(project, 'The native CDA fixture must provide the exact LOOM_CDA_PROJECT target.');
  assert(apiOrigin, 'The native CDA fixture must provide LOOM_CDA_API_ORIGIN.');
  assert(uiOrigin, 'The native CDA fixture must provide LOOM_CDA_UI_ORIGIN.');
  assert(apiContainer, 'The native CDA fixture must provide LOOM_CDA_API_CONTAINER.');
  assert(arangoContainer, 'The native CDA fixture must provide LOOM_ARANGO_CONTAINER for the independent Arango oracle.');
  assert(composeProject, 'The native CDA fixture must provide LOOM_CDA_COMPOSE_PROJECT.');
  const apiBuildContainer = apiContainer;
  const explorerRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const authoring = `${explorerRoot}/${encodeURIComponent(explorer)}/authoring/v2`;
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
  const report = nativeReport.populationMemberRemoval ?? (nativeReport.populationMemberRemoval = {});
  Object.assign(report, {
    scenario, case: caseName, title: 'Mapped-plus-orphan source removal to empty GROUP→RELATED_SOURCE output', project, explorer,
    assertions: [], cases: [], requests: [], nativeRequests: [], errors: [], incidentalErrors: [], oracle: {}, started: new Date().toISOString(),
  });
  nativeReport.scenario = scenario;
  nativeReport.case = caseName;
  report.requiredChecks = requiredChecks;

let requestMonitor;
let builder;
let outputId;
let source;
let baseSelection;
let sourceFreeze;
let frozenApiBuild;
let apiBuildCheckStarted = false;
const networkRequests = report.nativeRequests;
const mark = name => { report.assertions.push({ name, status: 'passed' }); check('correctness', name, true, { workflow: 'population-member-removal' }); };
const api = async (path, body) => {
  const requestId = randomUUID();
  const startedAt = Date.now();
  const entry = { requestId, path, method: body ? 'POST' : 'GET', body, startedAt };
  report.requests.push(entry);
  const response = await fetch(apiOrigin + path, {
    method: entry.method,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  entry.status = response.status;
  entry.completedAt = Date.now();
  const raw = await response.text();
  try { entry.response = JSON.parse(sanitizeBody(raw)); } catch { entry.response = sanitizeBody(raw); }
  assert(response.ok, `${path} returned ${response.status}: ${JSON.stringify(entry.response)}`);
  return entry.response;
};
const shellQuote = value => `'${String(value).replaceAll("'", "'\\''")}'`;
const rawQuery = query => {
  const js = `db._useDatabase("loom_dev"); const aql = ${JSON.stringify(query)}; print(JSON.stringify(db._query(aql).toArray()));`;
  const command = `arangosh --server.endpoint tcp://127.0.0.1:8529 --server.username root --server.password "$ARANGO_ROOT_PASSWORD" --javascript.execute-string ${shellQuote(js)}`;
  const stdout = execFileSync('docker', ['exec', arangoContainer, 'sh', '-lc', command], { encoding: 'utf8', timeout: 30000 });
  return JSON.parse(stdout.trim().split(/\r?\n/).at(-1));
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
const inspect = (callback, argument) => page.evaluate(callback, argument);
const waitFor = (callback, argument, timeout = 30000) => page.waitForFunction(callback, argument, { timeout });
const locatorFor = (selector, options = {}) => {
  if (options.name) return page.getByRole('button', { name: options.name, exact: true });
  const locator = page.locator(selector);
  return options.includes ? locator.filter({ hasText: options.includes }) : locator;
};
const click = async (selector, options = {}, timeout = 5000) => {
  const label = options.name ?? options.includes ?? selector;
  const locator = locatorFor(selector, options);
  await action(`click ${label}`, locator, () => locator.click({ timeout }), { timeout, budget: Math.min(timeout, 5000) });
};
const requestEntry = (path, startedAt) => report.nativeRequests.findLast(request => shouldCapturePopulationMemberNativeResponse(request, { project, explorer }) && request.path === path && request.startedAt >= startedAt && request.completedAt && request.response);
const visibleRows = async () => inspect(() => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
const assertRendered = async expectedRows => {
  await waitFor(() => !document.body.innerText.includes('Loading your table…'), null, 30000);
  const rows = await visibleRows();
  assertExactPreviewMultiset(rows, expectedRows, 'visible source multiset');
};
const openBuilder = async expectedRows => {
  await refreshBuilder();
  await page.goto(`${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
  await waitFor(id => Boolean(document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)), outputId, 30000);
  await click(`[data-testid="construction-table-${outputId}"]`);
  await waitFor(() => {
    const button = document.querySelector('[data-testid="construction-rows-settings-trigger"]');
    return Boolean(button && !button.disabled);
  }, null, 30000);
  await waitFor(({ version, digest }) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && preview.dataset.currentDraftVersion === version && preview.dataset.currentDraftDigest === digest;
  }, { version: String(builder.draftVersion), digest: builder.draftDigest }, 30000);
  await assertRendered(expectedRows);
};
const waitForMemberProposal = async (startAt, { expectedRows, expected }) => {
  const path = `${authoring}/population-member-proposals`;
  await waitFor(({ project, explorer }) => {
    const panel = document.querySelector('[data-testid="population-member-removal-proposal"]');
    return panel?.dataset.proposalId && panel.dataset.previewStatus === 'READY'
      && panel.dataset.baseSelectionRevisionId && panel.dataset.candidateSelectionRevisionId;
  }, { project, explorer }, 5000);
  await requestMonitor.flush();
  const entry = requestEntry(path, startAt);
  assert(entry, 'Ready member proposal must have a captured native request and response');
  assert.equal(entry.status, 200, `Member removal proposal failed: ${JSON.stringify(entry.response)}`);
  const value = entry.response;
  const candidate = assertPopulationMemberRemovalProposal(value, expected);
  assert.equal(value.previewStatus, 'READY');
  assert(value.preview && value.preview.receiptId === value.proposalId && value.preview.outputId === outputId, 'Proposal must carry the candidate receipt preview for this exact table');
  assert.equal(value.preview.rowCount, expectedRows.length, 'Candidate preview row count must match independent raw route oracle');
  const byColumn = value.preview.rows.map(row => value.preview.columns.map(column => row[column.column] == null ? '—' : String(row[column.column])));
  assertExactPreviewMultiset(byColumn, expectedRows, 'candidate preview multiset');
  const panelPreview = await inspect(() => ({
    target: document.querySelector('[data-testid="population-member-removal-target"]')?.innerText.trim(),
    table: Boolean(document.querySelector('[data-testid="population-member-candidate-preview"] table[aria-label="Candidate table after member removal"]')),
    rows: [...document.querySelectorAll('[data-testid="population-member-candidate-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
  }));
  assert.equal(panelPreview.target, `Remove ${expected.removedMember.resourceType}/${expected.removedMember.id}`);
  assert.equal(panelPreview.table, true, 'The UI must render the candidate table under the removal proposal');
  assertExactPreviewMultiset(panelPreview.rows, expectedRows, 'member-proposal preview multiset');
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
  const selectionPage = await api(`${selections}/${baseSelection.id}?limit=10`);
  assert.deepEqual(selectionPage.members.map(member => member.ref).sort((a, b) => a.id.localeCompare(b.id)), refs);
  report.oracle.baseSelection = selectionPage.revision;

  report.browserTarget = { uiOrigin, project, explorer };
  requestMonitor = captureCDARequests(page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: `${explorerRoot}/${encodeURIComponent(explorer)}`,
    responsePaths: /./,
    report: { nativeRequests: report.nativeRequests, errors: report.errors },
  });
  page.on('request', request => {
    const entry = requestMonitor.byRequest.get(request);
    if (!entry) return;
    const scope = /^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)(?:\/|$)/.exec(entry.path);
    if (scope) { entry.scopeProject = decodeURIComponent(scope[1]); entry.scopeExplorer = decodeURIComponent(scope[2]); }
  });
  page.on('response', response => {
    if (response.status() < 400) return;
    const url = new URL(response.url());
    if (![new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin)) return;
    const failure = { path: url.pathname, status: response.status() };
    if (response.status() === 404 && url.pathname.endsWith('/favicon.ico')) report.incidentalErrors.push({ ...failure, reason: 'The local UI does not serve a favicon asset.' });
    else if (!requestMonitor.byRequest.has(response.request())) report.errors.push({ kind: url.pathname.startsWith('/api/') ? 'http' : 'asset-http', ...failure });
  });
  page.on('requestfailed', request => {
    const url = new URL(request.url());
    if (![new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin) || requestMonitor.byRequest.has(request)) return;
    report.errors.push({ kind: 'network', path: url.pathname, method: request.method(), error: sanitizeText(request.failure()?.errorText) });
  });

  const drainNativeNetwork = async () => {
    await requestMonitor.flush();
    assert.deepEqual(report.errors, [], 'Native browser path must not issue unexpected failed requests or console/runtime errors');
    assert.deepEqual(report.nativeRequests.filter(request => !request.completedAt || (request.status === undefined && !request.failure)), [],
      'Every owned browser request must complete or retain an explicit network failure');
  };

  const expectProposalForConstruction = async (startedAt, expectedRows) => {
    await waitFor(() => ['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)
      || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus), null, 60000);
    await requestMonitor.flush();
    const entry = report.nativeRequests.findLast(request => shouldCapturePopulationMemberNativeResponse(request, { project, explorer }) && /\/construction-(?:choice-)?proposals$/.test(request.path) && request.startedAt >= startedAt && request.completedAt && request.response);
    assert(entry, 'Visible construction proposal omitted its captured owned response');
    assert.equal(entry.status, 200, JSON.stringify(entry.response));
    assert.equal(entry.response.previewStatus, 'READY', JSON.stringify(entry.response));
    const preview = entry.response.preview;
    const rows = preview.rows.map(row => preview.columns.map(column => row[column.column] == null ? '—' : String(row[column.column])));
    assertExactPreviewMultiset(rows, expectedRows, 'construction proposal multiset');
    return entry;
  };
  const applyConstruction = async (expectedRows, panel = 'construction-proposal-panel') => {
    const start = Date.now();
    if (panel === 'construction-choice-proposal-panel') await click(`[data-testid="${panel}"] button`, { name: 'Apply columns' });
    else await click('[data-testid="construction-apply-proposal"]');
    await waitFor(({ panel }) => !document.querySelector(`[data-testid="${panel}"]`), { panel }, 30000);
    await assertRendered(expectedRows);
    assert(Date.now() - start <= 5000, 'Construction Apply must render within five seconds');
    await refreshBuilder();
  };

  await openBuilder(roots.map(row => [row.id]));
  let started = Date.now();
  await click('[data-testid="construction-rows-settings-trigger"]');
  await page.locator('[data-testid="construction-action-group-rows"]').waitFor({ state: 'visible', timeout: 30000 });
  await click('[data-testid="construction-action-group-rows"]');
  await page.locator('input[aria-label="Group by Observation ID"]').waitFor({ state: 'visible', timeout: 30000 });
  await click('input[aria-label="Group by Observation ID"]');
  await expectProposalForConstruction(started, roots.map(row => [row.id, '1']));
  await applyConstruction(roots.map(row => [row.id, '1']));

  const openRelatedFieldChooser = async () => {
    await click('[data-testid="construction-action-add-columns"]');
    await click('[aria-label="Column types"] button', { includes: 'Fields and related data' });
    await page.locator('[data-testid="construction-add-columns-source"]').waitFor({ state: 'visible', timeout: 30000 });
    if (!await inspect(() => document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open === true)) await click('[aria-label="Related resources"] summary');
    await click('[data-testid="construction-add-columns-source-option"][aria-label="Specimen, Related resource"]');
    if (!await inspect(() => document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open === true)) await click('[data-testid="feature-catalog-raw-fields"] summary');
    await page.locator('input[aria-label="Select Specimen.id"]').waitFor({ state: 'visible', timeout: 30000 });
  };
  const configureRelated = async () => {
    await openRelatedFieldChooser();
    await click('input[aria-label="Select Specimen.id"]');
    await click('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    await page.getByRole('dialog').waitFor({ state: 'visible', timeout: 30000 });
    if (!await inspect(() => [...document.querySelectorAll('[role="dialog"] summary')].find(summary => summary.innerText.includes('Other relationship paths'))?.parentElement.open === true)) await click('[role="dialog"] summary', { includes: 'Other relationship paths' });
    const relationLabel = 'Observation -[specimen]-> Specimen';
    const pathSelector = `[role="dialog"] input[aria-label=${JSON.stringify(`Specimen ID: ${relationLabel}`)}]`;
    await page.locator(pathSelector).waitFor({ state: 'visible', timeout: 30000 });
    await click(pathSelector);
    await click('[role="dialog"] input[aria-label="Specimen ID: Count matching records"]');
    const proposalStarted = Date.now();
    await click('[role="dialog"] button', { name: 'Add 1 column' });
    await waitFor(() => ['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus) || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus), null, 60000);
    const panel = await inspect(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]') ? 'construction-choice-proposal-panel' : 'construction-proposal-panel');
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
  await click('[data-testid="construction-rows-settings-trigger"]', { name: 'Configure rows' }, Math.max(1, rowSettingsDeadline - Date.now()));
  await waitFor(name => { const dialog=document.querySelector('[role="dialog"][aria-label="Row definition settings"]'); const collection=dialog?.querySelector('section[aria-label="Starting collection"]'); const members=collection?.querySelector('[data-testid="population-member-list"]'); return Boolean(dialog && collection && members && [...members.querySelectorAll('button')].some(button => (button.getAttribute('aria-label') || button.innerText).trim() === name)); }, removeName, Math.max(1, rowSettingsDeadline - Date.now()));
  const collectionState = await inspect(() => { const dialog=document.querySelector('[role="dialog"][aria-label="Row definition settings"]'); const collection=dialog?.querySelector('section[aria-label="Starting collection"]'); return { dialog:Boolean(dialog), selectionId:collection?.dataset.selectionRevisionId, attachedSelectionId:collection?.dataset.attachedSelectionRevisionId, memberList:Boolean(collection?.querySelector('[data-testid="population-member-list"]')) }; });
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
    await click('[data-testid="population-member-list"] button', { name: removeName });
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
  const firstPanel = await inspect(() => { const p=document.querySelector('[data-testid="population-member-removal-proposal"]'); return { proposalId:p?.dataset.proposalId, baseSelectionId:p?.dataset.baseSelectionRevisionId, candidateSelectionId:p?.dataset.candidateSelectionRevisionId, previewStatus:p?.dataset.previewStatus }; });
  assert.deepEqual(firstPanel, { proposalId: firstProposalID, baseSelectionId: baseHeader.id, candidateSelectionId: firstCandidateID, previewStatus: 'READY' });
  const cancelStarted = Date.now();
  await click('button', { name: 'Cancel member removal' });
  await waitFor(() => !document.querySelector('[data-testid="population-member-removal-proposal"]') && !document.querySelector('[data-testid="population-member-removal-proposal-panel"]'), null, 30000);
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
  await click('button', { name: 'Apply member removal' });
  await waitFor(() => !document.querySelector('[data-testid="population-member-removal-proposal"]') && !document.querySelector('[data-testid="population-member-removal-proposal-panel"]'), null, 30000);
  await waitFor(digest => { const p=document.querySelector('[data-testid="construction-preview"]'); return p?.dataset.previewStatus === 'ready' && p.dataset.currentDraftDigest !== digest; }, originalDigest, 30000);
  await assertRendered(candidateRows);
  await requestMonitor.flush();
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
  const visible = await inspect(() => { const p=document.querySelector('[data-testid="construction-preview"]'); return { status:p?.dataset.previewStatus, receiptId:p?.dataset.previewReceiptId, outputId:p?.dataset.previewOutputId, draftVersion:p?.dataset.currentDraftVersion, draftDigest:p?.dataset.currentDraftDigest }; });
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
  await click('button', { name: 'Undo last saved draft change' });
  await waitFor(digest => { const p=document.querySelector('[data-testid="construction-preview"]'); return p?.dataset.previewStatus === 'ready' && p.dataset.currentDraftDigest !== digest; }, reloaded.draftDigest, 30000);
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
  report.networkRequests = report.nativeRequests.map(({ requestId, browserRequestId, path, method, startedAt, completedAt, status, failure, expectedCancellation }) => ({ requestId, browserRequestId, path, method, startedAt, completedAt, status, failure, expectedCancellation }));
  report.networkFailures = report.nativeRequests.filter(request => request.failure).map(({ requestId, path, method, failure }) => ({ requestId, path, method, failure }));
  assert.deepEqual(report.errors, [], 'Native browser path must not issue unexpected failed requests or console/runtime errors');
  assert.deepEqual(report.requests.filter(request => request.status >= 400), [], 'No direct product API request may fail in the removal lifecycle');
  assert(report.cases.some(testCase => testCase.name === 'mapped-member-auto-impact-preview' && testCase.durationMs <= 5000));
  assert(report.cases.some(testCase => testCase.name === 'apply-exact-proposal-to-empty-related-source' && testCase.durationMs <= 5000));
  assert(report.cases.some(testCase => testCase.name === 'reload-applied-candidate-to-render' && testCase.durationMs <= 5000));
  mark(requiredChecks[9]);
  report.status = 'passed';
  };

  let primaryError;
  try {
    await run();
  } catch (error) {
    primaryError = error;
    report.status = error instanceof ApiBuildFreezeError ? 'invalidated' : 'failed';
    report.error = String(error?.stack ?? error);
    if (error instanceof ApiBuildFreezeError) report.apiBuildFreeze = { ...report.apiBuildFreeze, invalidatesRun: true, productFailure: false, error: String(error), reason: error.reason };
  } finally {
    if (requestMonitor) {
      try { await requestMonitor.flush(); } catch (error) { report.responseDrainError = String(error); }
    }
    report.networkRequests ??= report.nativeRequests.map(({ requestId, browserRequestId, path, method, startedAt, completedAt, status, failure, expectedCancellation }) => ({ requestId, browserRequestId, path, method, startedAt, completedAt, status, failure, expectedCancellation }));
    report.networkFailures ??= report.nativeRequests.filter(request => request.failure).map(({ requestId, path, method, failure }) => ({ requestId, path, method, failure }));
    const invalidations = [];
    if (sourceFreeze) {
      try { report.sourceFreeze = { ...report.sourceFreeze, ...await sourceFreeze.assertUnchanged() }; }
      catch (error) { report.status = 'invalidated'; report.sourceFreeze = { ...report.sourceFreeze, invalidatesRun: true, productFailure: false, changedPaths: error.changedPaths }; invalidations.push(error); }
    }
    if (apiBuildCheckStarted && frozenApiBuild) {
      try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...await frozenApiBuild.assertUnchanged() }; }
      catch (error) { report.status = 'invalidated'; report.apiBuildFreeze = { ...report.apiBuildFreeze, invalidatesRun: true, productFailure: false, error: String(error) }; invalidations.push(error); }
    }
    if (report.sourceFingerprint) {
      report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
      report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === report.sourceFingerprint.after.sha256 && report.sourceFingerprint.before.files === report.sourceFingerprint.after.files;
      report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
      report.sourceFingerprint.productFailure = false;
      if (!report.sourceFingerprint.unchanged) { report.status = 'invalidated'; invalidations.push(new Error('Watched source fingerprint changed during native browser lifecycle.')); }
    }
    report.finished = new Date().toISOString();
    if (invalidations.length && !primaryError) primaryError = invalidations[0];
  }
  if (primaryError) throw primaryError;
}
