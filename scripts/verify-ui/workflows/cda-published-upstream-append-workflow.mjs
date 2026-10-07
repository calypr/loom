import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  CDA_PUBLISHED_APPEND_SOURCES,
  CdaPublishedAppendWitnessUnavailable,
  assertCdaPublishedAppendReread,
  cdaPublishedAppendRereadQuery,
  cdaPublishedAppendScanQuery,
  prepareCdaPublishedAppendOracle,
} from '../helpers/cda-published-upstream-append-oracle.mjs';
import {
  builderDraftStateEvidence,
} from '../helpers/builder-combine-draft-helpers.mjs';
import {
  builderResponseIdentity,
  constructionProposalPreviewEvidence,
  currentPublishedRevisionForOutput,
  findColumn,
  isCombineInputIDColumn,
  isScalarStringColumn,
  nativeCombineTargetBindingEvidence,
  rootedEmptyTargetAppliedExpression,
  rootedEmptyTargetRestorationEvidence,
  snapshotSourceDocument,
  sameSourceDocuments,
  savedAppendPreviewAppliedExpression,
} from '../helpers/builder-combine-helpers.mjs';
import { proposalPreviewReadinessExpression } from '../helpers/proposal-preview-readiness.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { assertCdaNoAuthRuntime } from '../helpers/cda-no-auth-runtime.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';

const ACTION_BUDGET_MS = 5_000;
const ACTION_CHECK = 'all native published CDA APPEND actions complete within five seconds';
const encode = value => encodeURIComponent(value);
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const apiRoot = (project, explorer) => `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
const publishedRef = revision => JSON.stringify([revision.tableId, revision.revisionId, revision.outputId]);
const showRows = rows => rows.map(row => row.map(value => value === null || value === undefined ? '—' : String(value)));

function runArango(arangoContainer, query, label) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const invocation = buildArangoShellInvocation({ container: arangoContainer, database: 'loom_dev', script: javascript });
  const result = spawnSync(invocation.command, invocation.args,
    { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `${label} failed: ${normalize(result.stderr || result.stdout).slice(0, 1_800)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `${label} returned no JSON array: ${normalize(result.stdout).slice(-900)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows), `${label} did not return an array`);
  return rows;
}

export async function cdaPublishedUpstreamAppendWorkflow({ page, cda }) {
  const { target, report } = cda;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target);
  assert(project && generation && apiOrigin && uiOrigin, 'Owned CDA fixture must provide project, generation, API, and UI origins');
  assert.equal(project, target.fixtureProject, 'Raw CDA witnesses must use the exact owned project');
  assert.equal(generation, 'cda-fhir-v1', 'Published CDA APPEND requires the pinned FHIR generation');
  const apiRuntimeAuthorization = assertCdaNoAuthRuntime({
    apiContainer: target.apiContainer,
  });
  report.authorization.apiRuntime = apiRuntimeAuthorization;
  report.phase = 'published real-CDA three-source APPEND with exact null padding and restoration';

  const rawScans = Object.fromEntries(['Observation', 'Patient'].map(resourceType => [resourceType,
    runArango(arangoContainer, cdaPublishedAppendScanQuery({ project, generation, resourceType }),
      `Bounded exact-scope ${resourceType} raw witness scan`)]));
  let oracle;
  try {
    oracle = prepareCdaPublishedAppendOracle(rawScans, { project, generation });
  } catch (error) {
    if (!(error instanceof CdaPublishedAppendWitnessUnavailable)) throw error;
    report.boundedWitnessUnavailable = error.evidence;
    report.gaps ??= [];
    report.gaps.push({ assertion: 'published CDA APPEND over two disjoint Observation.status=final pairs and Patient.id rows',
      status: 'untested', reason: error.message, evidence: error.evidence });
    throw error;
  }

  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-published-append-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${normalize(text).slice(0, 1_000)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_600)}`);
    return value;
  };
  const requireCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const waitFunction = (predicate, timeout = ACTION_BUDGET_MS) => page.waitForFunction(predicate, undefined,
    { timeout: Math.min(ACTION_BUDGET_MS, timeout) });
  const waitSelector = (selector, timeout = ACTION_BUDGET_MS) => page.locator(selector).waitFor({ state: 'visible',
    timeout: Math.min(ACTION_BUDGET_MS, timeout) });
  const timedAction = (label, locator, perform, after, editable = false) => cda.action(label, locator, perform, {
    timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
    ...(after ? { after, requiredCheck: ACTION_CHECK } : {}), ...(editable ? { editable: true } : {}),
  });
  const click = (label, locator, after) => timedAction(label, locator,
    item => item.click({ timeout: ACTION_BUDGET_MS }), after);
  const fill = (label, locator, value, after) => timedAction(label, locator,
    item => item.fill(value, { timeout: ACTION_BUDGET_MS }), after, true);
  const select = (label, locator, value, after) => timedAction(label, locator,
    item => item.selectOption(value, { timeout: ACTION_BUDGET_MS }), after);
  const basePath = explorer => apiRoot(project, explorer);
  let explorer;
  let explorerBase;
  let builder;
  let initialScope;
  let requestCapture;
  let publishRequests = 0;
  const ownedOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
  const onRequest = browserRequest => {
    try {
      const url = new URL(browserRequest.url());
      if (ownedOrigins.has(url.origin) && url.pathname.endsWith('/authoring/v2/publish')) publishRequests += 1;
    } catch { /* Non-URL requests are outside the owned publication route. */ }
  };
  page.on('request', onRequest);
  const readBuilder = () => api(`${explorerBase}/authoring/v2/builder`);
  const checkScope = state => {
    assert.equal(state.catalog?.generation, generation, 'Builder left the pinned CDA generation');
    assert.equal(state.catalog?.snapshotToken, initialScope.snapshotToken, 'Builder snapshot changed during publication and APPEND');
    assert.equal(state.catalog?.authorizationScopeDigest, initialScope.authorizationScopeDigest,
      'Builder authorization scope changed during publication and APPEND');
  };
  const command = async commands => {
    assert(builder?.catalog?.snapshotToken, 'Native source commands require the current Builder snapshot');
    const body = {
      commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, commands,
    };
    await api(`${explorerBase}/authoring/v2/commands`, body);
    builder = await readBuilder();
    checkScope(builder);
    return builder;
  };
  const documentByOutput = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected exactly one Builder document for ${outputId}`);
    return matches[0];
  };
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal'
      ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
    if (kind === 'proposal' && !proposal) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const table = kind === 'proposal' ? proposal.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = kind === 'proposal'
      ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
    const rows = kind === 'proposal'
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
    return { ready: true, headers, rows, ariaRowCount: table.getAttribute('aria-rowcount') };
  }, { kind });
  const assertGrid = (name, grid, headers, expectedRows) => {
    const actualRows = sortRows(grid.rows);
    const wantedRows = sortRows(showRows(expectedRows));
    const ok = grid.ready && isDeepStrictEqual(grid.headers, headers) && isDeepStrictEqual(actualRows, wantedRows);
    requireCheck('correctness', name, ok, { headers: grid.headers, expectedHeaders: headers,
      rows: grid.rows, expectedRows: showRows(expectedRows), ariaRowCount: grid.ariaRowCount,
      comparison: 'unordered row multiset; source membership and multiplicity retained' });
    assert(ok, `${name}: visible rows differ from the exact bounded CDA oracle`);
  };
  const waitProposal = (outputId, rowCount) => waitFunction(proposalPreviewReadinessExpression(outputId, rowCount));
  const waitSaved = (outputId, rowCount) => waitFunction(`(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${JSON.stringify(outputId)}&&t&&t.getAttribute('aria-rowcount')===${JSON.stringify(String(rowCount + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview did not complete for this draft:'))})()`);
  const waitEmpty = outputId => waitFunction(rootedEmptyTargetAppliedExpression(outputId));
  const tableLocator = outputId => page.getByTestId(`construction-table-${outputId}`);
  const selectTable = async (outputId, rows, label) => {
    const locator = tableLocator(outputId);
    if (await locator.getAttribute('aria-current') === 'page') {
      if (rows !== undefined) await waitSaved(outputId, rows);
      return;
    }
    await click(label, locator, async () => {
      if (rows === undefined) await waitFunction(`Boolean(document.querySelector('[data-testid="construction-table-${outputId}"][aria-current="page"]'))`);
      else await waitSaved(outputId, rows);
    });
  };
  const reloadResult = async ({ outputId, headers, rows, rootedEmpty = false, name }) => {
    const startedAt = performance.now();
    const table = tableLocator(outputId);
    await timedAction(name, table, async item => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
      await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      if (await page.getByTestId(`construction-table-${outputId}`).getAttribute('aria-current') !== 'page') {
        await page.getByTestId(`construction-table-${outputId}`).click({ timeout: ACTION_BUDGET_MS });
      }
      if (rootedEmpty) await page.waitForFunction(rootedEmptyTargetAppliedExpression(outputId), undefined, { timeout: ACTION_BUDGET_MS });
      else await page.waitForFunction(savedAppendPreviewAppliedExpression(outputId, headers, showRows(rows)), undefined,
        { timeout: ACTION_BUDGET_MS });
    }, async () => {
      if (rootedEmpty) await waitEmpty(outputId);
      else await waitSaved(outputId, rows.length);
    });
    const elapsedMs = performance.now() - startedAt;
    requireCheck('performance', name, elapsedMs <= ACTION_BUDGET_MS,
      { elapsedMs, limitMs: ACTION_BUDGET_MS, outputId, rowCount: rows?.length ?? 0, rootedEmpty });
    if (!rootedEmpty) assertGrid(`${name} renders exact saved APPEND rows`, await readGrid('saved'), headers, rows);
    return elapsedMs;
  };

  const captures = [];
  const captureByRequest = new Map();
  const proposalPath = explorer => `${basePath(explorer)}/authoring/v2/construction-proposals`;
  const onProposalRequest = request => {
    try {
      const url = new URL(request.url());
      const body = request.postDataJSON();
      if (request.method() !== 'POST' || url.origin !== new URL(uiOrigin).origin || url.pathname !== proposalPath(explorer)) return;
      const entry = { request, body, status: null, sequence: captures.length + 1, responsePromise: null };
      captures.push(entry);
      captureByRequest.set(request, entry);
    } catch { /* Ignore requests without a valid owned construction-proposal body. */ }
  };
  const onProposalResponse = response => {
    const entry = captureByRequest.get(response.request());
    if (!entry) return;
    entry.status = response.status();
    entry.responsePromise = response.json().then(value => { entry.response = value; return value; })
      .catch(error => { entry.response = { responseReadError: String(error) }; return entry.response; });
  };

  try {
    const scanEvidence = {
      project, generation, scanLimitPerResource: oracle.scanLimitPerResource,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      resources: oracle.resources,
      selected: Object.fromEntries(Object.entries(oracle.sources).map(([key, members]) => [key, members.map(member => ({
        resourceType: member.resourceType, arangoDocumentKey: member._id, fhirID: member.id,
        fieldValue: member.fieldValue, fieldPresent: member.fieldPresent,
      }))])),
      disjointObservationDocuments: new Set([
        ...oracle.sources['observation-left'], ...oracle.sources['observation-right'],
      ].map(member => member._id)).size === 4,
      duplicateFinalStatusCount: oracle.append.duplicateFinalStatusCount,
      expectedNullPaddedRows: oracle.append.rows,
    };
    requireCheck('correctness', 'bounded raw CDA witnesses provide two disjoint Observation final pairs and two matching Patient FHIR IDs',
      scanEvidence.disjointObservationDocuments && oracle.sources.patient.length === 2, scanEvidence);
    report.rawOracle = scanEvidence;

    const explorerName = `cda-published-append-${randomUUID()}`;
    const explorerTitle = `CDA published APPEND ${randomUUID().slice(0, 8)}`;
    const created = await api(`/api/v1/projects/${encode(project)}/explorers`, { name: explorerName, title: explorerTitle });
    explorer = created.explorerId ?? created.id ?? created.explorer?.id ?? explorerName;
    assert.equal(explorer, explorerName, 'Fresh Explorer must retain the unique requested identity');
    explorerBase = basePath(explorer);
    requestCapture = cda.captureRequests(`${explorerBase}/authoring/v2`, { responsePaths: /commands|construction-proposals|publish/ });
    const list = await api(`/api/v1/projects/${encode(project)}/explorers`);
    const summaries = Array.isArray(list) ? list : list.explorers ?? list.value ?? [];
    const matching = summaries.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
    assert.equal(matching.length, 1, 'Fresh Explorer must appear exactly once in its owned project');
    assert.equal(matching[0].project, project);
    assert.equal(matching[0].title, explorerTitle);
    assert.equal(matching[0].management, 'INTERACTIVE');
    builder = await readBuilder();
    const emptyDraft = builderDraftStateEvidence(builder, 'empty');
    assert.equal(builder.catalog?.generation, generation);
    assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest);
    initialScope = { generation, snapshotToken: builder.catalog.snapshotToken,
      authorizationScopeDigest: builder.catalog.authorizationScopeDigest };
    requireCheck('correctness', 'fresh source Explorer is empty and bound to exact CDA project, generation, and scope',
      emptyDraft.ok && matching[0].project === project && builder.catalog.generation === generation,
      { explorer, title: explorerTitle, initialScope, emptyDraft });

    const sources = [];
    for (const definition of CDA_PUBLISHED_APPEND_SOURCES) {
      const members = oracle.sources[definition.sourceKey];
      assert.equal(members.length, 2, `${definition.sourceKey} needs exactly two bounded raw witnesses`);
      const rootMatches = builder.catalog.nodes.filter(node => node.resourceType === definition.resourceType && node.rowRootEligible);
      assert.equal(rootMatches.length, 1, `Scoped catalog needs one ${definition.resourceType} root`);
      const rootNode = rootMatches[0];
      const idCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
      const valueCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === definition.valueField);
      assert.equal(idCandidates.length, 1, `Catalog needs one ${definition.resourceType}.id candidate`);
      assert.equal(valueCandidates.length, 1, `Catalog needs one ${definition.resourceType}.${definition.valueField} candidate`);
      for (const candidate of [idCandidates[0], valueCandidates[0]]) {
        assert.equal(candidate.logicalType, 'string', `${definition.resourceType}.${candidate.fieldPath} must be scalar text`);
        assert.equal(candidate.repeated, false, `${definition.resourceType}.${candidate.fieldPath} must not repeat`);
      }
      const priorOutputs = new Set((builder.workspace?.documents ?? []).map(document => document.output.id));
      await command([{ type: 'CREATE_TABLE', title: definition.title, rootNodeId: rootNode.nodeId }]);
      const newDocs = builder.workspace.documents.filter(document => !priorOutputs.has(document.output.id));
      assert.equal(newDocs.length, 1);
      const outputId = newDocs[0].output.id;
      const idTitle = definition.resourceType === 'Patient' ? 'Patient ID' : `${definition.title} ID`;
      await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidates[0].candidateId,
        projectionMode: 'VALUE', initialPresentation: 'TABLE', title: idTitle }]);
      if (definition.valueField !== 'id') {
        await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidates[0].candidateId,
          projectionMode: 'VALUE', initialPresentation: 'TABLE', title: definition.valueField === 'status' ? 'Status' : definition.valueField }]);
      }
      const refs = oracle.sourceReferences[definition.sourceKey];
      assert(refs.every(reference => reference.project === project && reference.generation === generation &&
        reference.resourceType === definition.resourceType && !Object.hasOwn(reference, '_id')));
      const rereadRows = runArango(arangoContainer, cdaPublishedAppendRereadQuery({ project, generation,
        resourceType: definition.resourceType, documentIDs: members.map(member => member._id) }),
      `Exact raw reread for ${definition.sourceKey} selected FHIR records`);
      const reread = assertCdaPublishedAppendReread(rereadRows, oracle.exactExpected[definition.sourceKey], {
        project, generation, resourceType: definition.resourceType,
      });
      const selection = await api(`${explorerBase}/selections`, {
        snapshotToken: builder.catalog.snapshotToken,
        idempotencyKey: `cda-published-append-${definition.sourceKey}-${randomUUID()}`,
        source: { kind: 'resources', resources: { refs } },
      });
      assert.equal(selection.project, project);
      assert.equal(selection.generation, generation);
      assert.equal(selection.resourceType, definition.resourceType);
      assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
      assert.equal(selection.memberCount, members.length);
      const selectionRead = await api(`${explorerBase}/selections/${encode(selection.id)}?limit=100`);
      const actualRefs = (selectionRead.members ?? []).map(member => {
        const ref = member.ref ?? {};
        return `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
      }).sort();
      const expectedRefs = refs.map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
      assert.equal(selectionRead.revision?.id, selection.id);
      assert.equal(selectionRead.revision?.project, project);
      assert.equal(selectionRead.revision?.generation, generation);
      assert.equal(selectionRead.revision?.resourceType, definition.resourceType);
      assert.equal(selectionRead.revision?.scopeDigest, initialScope.authorizationScopeDigest);
      assert.equal(selectionRead.revision?.memberCount, members.length);
      assert.equal((selectionRead.members ?? []).length, members.length);
      assert.deepEqual(actualRefs, expectedRefs, `${definition.title} selection must pin the exact raw FHIR witnesses`);
      assert.equal(new Set((selectionRead.members ?? []).map(member => member.memberKey)).size, members.length);
      const routes = await api(`${explorerBase}/authoring/v2/population-routes`, {
        snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
      });
      const direct = routes.choices.find(choice => choice.route.length === 0);
      assert(direct, `${definition.title} exact selection needs a direct root population route`);
      await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
      const doc = documentByOutput(builder, outputId);
      const exactPopulation = doc.rootResourceType === definition.resourceType &&
        doc.population?.selectionRevisionId === selection.id && (doc.population.route?.length ?? 0) === 0 &&
        doc.columns.filter(column => column.source?.field?.path === 'id').length === 1 &&
        doc.columns.some(column => column.source?.field?.path === definition.valueField);
      requireCheck('correctness', `${definition.title} source binds exact immutable raw witness selection`, exactPopulation,
        { outputId, selectionId: selection.id, actualRefs, expectedRefs, rootResourceType: doc.rootResourceType,
          columnPaths: doc.columns.map(column => column.source?.field?.path), reread });
      assert(exactPopulation);
      sources.push({ definition, outputId, selection, members, doc: snapshotSourceDocument(doc) });
    }

    await cda.navigate(`${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`);
    for (const source of sources) {
      await waitSelector(`[data-testid="construction-table-${source.outputId}"]`);
      await selectTable(source.outputId, source.members.length, `Open exact ${source.definition.title} source preview`);
      const doc = documentByOutput(await readBuilder(), source.outputId);
      const headers = doc.columns.map(column => column.label);
      const expected = source.members.map(member => source.definition.resourceType === 'Patient'
        ? [member.id]
        : [member.id, member.fieldValue]);
      assertGrid(`${source.definition.title} preview matches its selected raw witnesses`, await readGrid('saved'), headers, expected);
      source.headers = headers;
      source.rows = expected;
    }

    const publishPath = `${explorerBase}/authoring/v2/publish`;
    const publishResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
      new URL(response.url()).origin === new URL(uiOrigin).origin && new URL(response.url()).pathname === publishPath,
    { timeout: ACTION_BUDGET_MS });
    const publishButton = page.getByRole('button', { name: 'Publish', exact: true });
    await click('Publish all three exact CDA source tables through Builder', publishButton, async () => {
      const response = await publishResponse;
      assert(response.status() >= 200 && response.status() < 300, `Native publish returned HTTP ${response.status()}`);
    });
    const published = await publishResponse;
    requireCheck('correctness', 'native Builder publication succeeds for the three selected raw CDA source tables',
      published.status() >= 200 && published.status() < 300 && publishRequests === 1,
      { status: published.status(), path: publishPath, publishRequests, sourceOutputIDs: sources.map(source => source.outputId) });

    builder = await readBuilder();
    checkScope(builder);
    const entries = [];
    let cursor;
    do {
      const response = await api(`${explorerBase}/authoring/v2/construction-inputs`, {
        snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
        expectedDraftDigest: builder.draftDigest, ...(cursor ? { cursor } : {}), limit: 100,
      });
      assert.equal(response.snapshotToken, builder.catalog.snapshotToken);
      assert.equal(response.draftVersion, builder.draftVersion);
      assert.equal(response.draftDigest, builder.draftDigest);
      assert.equal(response.datasetGeneration, generation);
      entries.push(...response.entries);
      cursor = response.nextCursor || undefined;
    } while (cursor);
    const revisions = sources.map(source => currentPublishedRevisionForOutput(entries, source.outputId));
    const columns = revisions.map((revision, index) => {
      const definition = sources[index].definition;
      const id = findColumn(revision, definition.resourceType, 'id');
      const value = definition.valueField === 'id' ? id : findColumn(revision, definition.resourceType, definition.valueField);
      assert(isCombineInputIDColumn(id, 'APPEND'), `${definition.title} must publish a compatible FHIR ID column`);
      if (definition.valueField !== 'id') assert(isScalarStringColumn(value), `${definition.title} field must publish as scalar text`);
      return { id, value };
    });
    const revisionsCurrent = revisions.every((revision, index) => revision.outputId === sources[index].outputId && revision.isCurrent === true);
    requireCheck('correctness', 'published catalog exposes exact current revisions for each selected source output', revisionsCurrent,
      { revisions: revisions.map(revision => ({ tableId: revision.tableId, revisionId: revision.revisionId,
        outputId: revision.outputId, isCurrent: revision.isCurrent })), sourceOutputIDs: sources.map(source => source.outputId) });
    assert(revisionsCurrent);
    report.publishedSourceInputs = revisions.map((revision, index) => ({ sourceKey: sources[index].definition.sourceKey,
      tableId: revision.tableId, revisionId: revision.revisionId, outputId: revision.outputId,
      columns: columns[index] }));
    const sourceSnapshots = sources.map(source => snapshotSourceDocument(documentByOutput(builder, source.outputId)));

    page.on('request', onProposalRequest);
    page.on('response', onProposalResponse);
    const createTarget = async (label) => {
      await selectTable(sources[0].outputId, sources[0].members.length, `Select Observation source A for ${label}`);
      const before = await readBuilder();
      const priorOutputIDs = before.workspace.documents.map(document => document.output.id);
      const fromIndex = report.nativeRequests.length;
      const combine = page.getByTestId('construction-action-combine');
      let evidence;
      await click(`Create rooted empty APPEND target for ${label}`, combine, async () => {
        await waitSelector('[data-testid="construction-combine-editor"]');
        await waitSelector('[data-testid="construction-combine-choice-append"]');
        const createdEvent = await requestCapture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/commands` &&
          entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
          requestCapture.rawRequestBody(entry)?.commands?.some(item => item.type === 'CREATE_TABLE'),
        { fromIndex, timeoutMs: ACTION_BUDGET_MS });
        const body = requestCapture.rawRequestBody(createdEvent);
        const response = requestCapture.rawResponseBody(createdEvent);
        const mountedOutputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]')
          .getAttribute('data-output-id');
        evidence = nativeCombineTargetBindingEvidence({ requestBody: body, responseStatus: createdEvent.status,
          response, expectedRootNodeIds: before.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible)
            .map(node => node.nodeId), expectedRootResourceType: 'Observation', previousOutputIds: priorOutputIDs, mountedOutputId });
        requireCheck('correctness', `${label} target is rooted empty and bound to the native Combine creation response`, evidence.ok, evidence);
        assert(evidence.ok, `Native target binding failed: ${JSON.stringify(evidence)}`);
      });
      builder = await readBuilder();
      checkScope(builder);
      const doc = documentByOutput(builder, evidence.outputId);
      assert.equal(doc.columns.length, 0);
      assert.equal(doc.construction?.steps?.length ?? 0, 0);
      return { outputId: evidence.outputId, rootResourceType: 'Observation', creationEvidence: evidence,
        baseline: snapshotSourceDocument(doc), builder };
    };
    const chooseAppend = async target => {
      await click('Choose native published-source APPEND', page.getByTestId('construction-combine-choice-append'), async () => {
        await waitSelector('select[aria-label="Input table 1"]');
        await waitSelector('select[aria-label="Input table 2"]');
      });
      for (let index = 0; index < sources.length; index += 1) {
        if (index === 2) await click('Add Patient as the third published APPEND input',
          page.getByRole('button', { name: 'Add another table', exact: true }),
          async () => waitSelector('select[aria-label="Input table 3"]'));
        const selector = `select[aria-label="Input table ${index + 1}"]`;
        const value = publishedRef(revisions[index]);
        await select(`Pin exact published source revision ${index + 1}`, page.locator(selector), value,
          async () => assert.equal(await page.locator(selector).inputValue(), value));
      }
      assert.equal(target.rootResourceType, 'Observation');
    };
    const addOutput = async (index, name, label, mappings, previewOutputId) => {
      await click(`Add APPEND output ${index}`, page.getByRole('button', { name: 'Add output field', exact: true }),
        async () => waitSelector(`input[aria-label="Output field ${index} name"]`));
      await fill(`Name APPEND output ${index}`, page.locator(`input[aria-label="Output field ${index} name"]`), name);
      await fill(`Label APPEND output ${index}`, page.locator(`input[aria-label="Output field ${index} label"]`), label);
      for (const [inputIndex, columnID] of mappings) {
        const selector = `select[aria-label="Output field ${index} matching field in input ${inputIndex + 1}"]`;
        const value = columnID === null ? 'empty-for-this-table' : `column:${columnID}`;
        await select(`Map APPEND ${label} for input ${inputIndex + 1}`, page.locator(selector), value, async () => {
          assert.equal(await page.locator(selector).inputValue(), value);
          if (previewOutputId && inputIndex === mappings.at(-1)[0]) {
            await waitProposal(previewOutputId, oracle.append.rows.length);
          }
        });
      }
    };
    const configureAppend = async target => {
      await chooseAppend(target);
      await addOutput(1, 'record_id', 'Record ID', columns.map((source, inputIndex) => [inputIndex, source.id.id]));
      await addOutput(2, 'status', 'Status', [[0, columns[0].value.id], [1, columns[1].value.id], [2, null]], target.outputId);
      const grid = await readGrid('proposal');
      assertGrid('native APPEND preview equals the exact duplicate-preserving CDA union with Patient null padding', grid,
        ['Record ID', 'Status'], oracle.append.rows);
      const entry = [...captures].reverse().find(item => item.body?.outputId === target.outputId &&
        item.body?.candidateConstruction?.steps?.at(-1)?.operation?.combine?.kind === 'APPEND');
      assert(entry, 'APPEND preview must issue a native construction proposal for the exact target');
      const response = await entry.responsePromise;
      assert(response && !response.responseReadError, 'Native APPEND response body must be retained');
      const step = entry.body.candidateConstruction.steps.at(-1);
      const expectedInputs = revisions.map(revision => [revision.tableId, revision.revisionId, revision.outputId]);
      const actualInputs = (step.inputs ?? []).map(input => [input.tableId, input.revisionId, input.outputId]);
      const publishedInputKinds = (step.inputs ?? []).map(input => input.kind);
      const expectedProjections = [
        ['record_id', 0, columns[0].id.id], ['record_id', 1, columns[1].id.id], ['record_id', 2, columns[2].id.id],
        ['status', 0, columns[0].value.id], ['status', 1, columns[1].value.id],
      ];
      const actualProjections = (step.operation?.combine?.projections ?? []).map(projection => [
        step.outputs?.find(output => output.id === projection.outputColumnId)?.name ?? null,
        projection.inputIndex, projection.inputColumnId,
      ]).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
      const sortedExpectedProjections = [...expectedProjections].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
      const statusOutput = step.outputs?.find(output => output.name === 'status');
      const identity = await page.evaluate(() => ({
        proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id') ?? null,
        receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
      }));
      const responseEvidence = constructionProposalPreviewEvidence({ responseStatus: entry.status, response,
        requestBody: entry.body, expectedOutputId: target.outputId, expectedColumns: ['record_id', 'status'],
        expectedRows: oracle.append.rows, domProposalId: identity.proposalId, domReceiptId: identity.receiptId });
      const exactShape = isDeepStrictEqual(actualInputs, expectedInputs) &&
        publishedInputKinds.length === revisions.length && publishedInputKinds.every(kind => kind === 'TABLE_REVISION') &&
        isDeepStrictEqual(actualProjections, sortedExpectedProjections) &&
        entry.body.snapshotToken === target.builder.catalog.snapshotToken &&
        entry.body.expectedDraftVersion === target.builder.draftVersion &&
        entry.body.expectedDraftDigest === target.builder.draftDigest &&
        statusOutput?.type === 'string' && statusOutput.nullable === true;
      requireCheck('correctness', 'APPEND proposal pins the three exact source revisions and explicit Patient status null padding',
        exactShape && responseEvidence.ok, { actualInputs, expectedInputs, publishedInputKinds, actualProjections,
          expectedProjections: sortedExpectedProjections, statusOutput,
          snapshotTokenMatched: entry.body.snapshotToken === target.builder.catalog.snapshotToken,
          draftVersion: entry.body.expectedDraftVersion, targetDraftVersion: target.builder.draftVersion,
          draftDigestMatched: entry.body.expectedDraftDigest === target.builder.draftDigest, responseEvidence });
      assert(exactShape && responseEvidence.ok, 'Published APPEND proposal did not preserve pinned revisions or literal nulls');
      return { step, responseEvidence, entry };
    };
    const applyAppend = async (target, preview) => {
      await click('Apply the published CDA APPEND proposal', page.getByTestId('construction-apply-proposal'),
        async () => waitSaved(target.outputId, oracle.append.rows.length));
      builder = await readBuilder();
      checkScope(builder);
      const doc = documentByOutput(builder, target.outputId);
      const step = doc.construction?.steps?.at(-1);
      const exactInputs = revisions.map(revision => [revision.tableId, revision.revisionId, revision.outputId]);
      assert.equal(step?.id, preview.step.id);
      assert.deepEqual((step.inputs ?? []).map(input => [input.tableId, input.revisionId, input.outputId]), exactInputs);
      assert((step.inputs ?? []).every(input => input.kind === 'TABLE_REVISION'));
      assert.equal(step.operation?.combine?.kind, 'APPEND');
      assert.equal(step.outputs?.find(output => output.name === 'status')?.nullable, true);
      assertGrid('applied APPEND matches the exact CDA multiset and null padding', await readGrid('saved'),
        ['Record ID', 'Status'], oracle.append.rows);
      return { doc, step, baselineBuilder: builder, snapshot: snapshotSourceDocument(doc) };
    };
    const restoreEmpty = async (target, savedBaseline, stepId) => {
      await click('Select saved APPEND step for removal', page.getByTestId(`construction-history-step-${stepId}`),
        async () => waitSelector(`[data-testid="construction-remove-step-${stepId}"]`));
      await click('Preview published APPEND removal', page.getByTestId(`construction-remove-step-${stepId}`), async () => {
        await waitProposal(target.outputId, 0);
      });
      const removalGrid = await readGrid('proposal');
      requireCheck('correctness', 'APPEND removal proposal targets the exact rooted output',
        removalGrid.ready && removalGrid.rows.length === 0, { outputId: target.outputId, removalGrid });
      await click('Cancel published APPEND removal proposal', page.getByTestId('construction-cancel-proposal'), async () => {
        await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
        await page.getByTestId('construction-history').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      });
      const afterCancel = await readBuilder();
      const canceledDoc = documentByOutput(afterCancel, target.outputId);
      const cancelGrid = await readGrid('saved');
      const cancelEvidence = isDeepStrictEqual(snapshotSourceDocument(canceledDoc), savedBaseline.snapshot) &&
        afterCancel.draftVersion === savedBaseline.baselineBuilder.draftVersion &&
        afterCancel.draftDigest === savedBaseline.baselineBuilder.draftDigest &&
        isDeepStrictEqual(sortRows(cancelGrid.rows), sortRows(showRows(oracle.append.rows)));
      requireCheck('persistence', 'Canceling APPEND removal preserves saved step, draft, and exact rows', cancelEvidence,
        { outputId: target.outputId, draftVersionBefore: savedBaseline.baselineBuilder.draftVersion,
          draftVersionAfter: afterCancel.draftVersion, draftDigestBefore: savedBaseline.baselineBuilder.draftDigest,
          draftDigestAfter: afterCancel.draftDigest, rows: cancelGrid.rows });
      assert(cancelEvidence);
      await click('Apply published APPEND removal and restore empty root', page.getByTestId('construction-apply-proposal'),
        async () => waitEmpty(target.outputId));
      builder = await readBuilder();
      const restored = documentByOutput(builder, target.outputId);
      const evidence = rootedEmptyTargetRestorationEvidence(restored, target.baseline, target);
      requireCheck('persistence', 'applying APPEND removal restores the exact pre-combine rooted empty target', evidence.ok,
        { target, evidence, restored: snapshotSourceDocument(restored), baseline: target.baseline });
      assert(evidence.ok);
      await reloadResult({ outputId: target.outputId, rootedEmpty: true, name: 'APPEND removal reload restores exact rooted empty target within five seconds' });
      return restored;
    };

    const probeTarget = await createTarget('initial Cancel probe');
    const probeBase = await readBuilder();
    await configureAppend(probeTarget);
    await click('Cancel initial published APPEND proposal', page.getByTestId('construction-cancel-proposal'), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitEmpty(probeTarget.outputId);
    });
    builder = await readBuilder();
    const canceledProbe = documentByOutput(builder, probeTarget.outputId);
    const probeEvidence = rootedEmptyTargetRestorationEvidence(canceledProbe, probeTarget.baseline, probeTarget);
    requireCheck('persistence', 'Canceling the initial published APPEND proposal preserves the exact rooted empty target',
      probeEvidence.ok && builder.draftVersion === probeBase.draftVersion && builder.draftDigest === probeBase.draftDigest,
      { probeEvidence, beforeDraftVersion: probeBase.draftVersion, afterDraftVersion: builder.draftVersion,
        beforeDraftDigest: probeBase.draftDigest, afterDraftDigest: builder.draftDigest });
    assert(probeEvidence.ok);
    await reloadResult({ outputId: probeTarget.outputId, rootedEmpty: true,
      name: 'Initial APPEND Cancel reload restores exact rooted empty target within five seconds' });

    const targetDoc = await createTarget('saved lifecycle');
    const initialPreview = await configureAppend(targetDoc);
    const applied = await applyAppend(targetDoc, initialPreview);
    await reloadResult({ outputId: targetDoc.outputId, headers: ['Record ID', 'Status'], rows: oracle.append.rows,
      name: 'APPEND Apply reload reaches exact null-padded CDA rows within five seconds' });
    const savedAfterReload = await readBuilder();
    const savedDocAfterReload = documentByOutput(savedAfterReload, targetDoc.outputId);
    const savedStep = savedDocAfterReload.construction.steps.at(-1);
    assert.equal(savedStep.id, applied.step.id);
    const beforeEditCancel = await readBuilder();
    const beforeEditCancelDoc = snapshotSourceDocument(documentByOutput(beforeEditCancel, targetDoc.outputId));

    const editSaved = async () => {
      await selectTable(targetDoc.outputId, oracle.append.rows.length, 'Select saved APPEND before edit');
      await click('Select saved APPEND step before edit', page.getByTestId(`construction-history-step-${savedStep.id}`),
        async () => waitSelector(`[data-testid="construction-edit-step-${savedStep.id}"]`));
      await click('Open saved published APPEND editor', page.getByTestId(`construction-edit-step-${savedStep.id}`), async () => {
        await waitSelector('[data-testid="construction-combine-editor"]');
        await waitSelector('input[aria-label="Output field 2 label"]');
      });
    };
    const statusLabel = page.locator('input[aria-label="Output field 2 label"]');
    const patientEmptyChoice = page.locator('select[aria-label="Output field 2 matching field in input 3"]');
    await editSaved();
    const reconstructedEmpty = await patientEmptyChoice.inputValue();
    requireCheck('persistence', 'saved APPEND edit reconstructs the Patient status mapping as explicit Empty',
      reconstructedEmpty === 'empty-for-this-table', { value: reconstructedEmpty });
    await fill('Preview a saved APPEND label edit', statusLabel, 'Clinical status', async () => {
      await waitProposal(targetDoc.outputId, oracle.append.rows.length);
    });
    assertGrid('saved APPEND edit preview retains exact rows and Patient null padding', await readGrid('proposal'),
      ['Record ID', 'Clinical status'], oracle.append.rows);
    await click('Cancel saved APPEND edit', page.getByTestId('construction-cancel-proposal'), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await page.getByTestId('construction-history').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
    let afterEditCancel = await readBuilder();
    let afterEditCancelDoc = documentByOutput(afterEditCancel, targetDoc.outputId);
    const editCancelPreserved = sameSourceDocuments(snapshotSourceDocument(afterEditCancelDoc), beforeEditCancelDoc) &&
      afterEditCancel.draftVersion === beforeEditCancel.draftVersion && afterEditCancel.draftDigest === beforeEditCancel.draftDigest;
    requireCheck('persistence', 'Canceling saved APPEND edit leaves the original step and Status label intact',
      editCancelPreserved && afterEditCancelDoc.construction?.steps?.at(-1)?.outputs?.find(output => output.name === 'status')?.label === 'Status',
      { stepId: afterEditCancelDoc.construction?.steps?.at(-1)?.id,
        labels: afterEditCancelDoc.construction?.steps?.at(-1)?.outputs?.map(output => output.label), editCancelPreserved,
        beforeDraftVersion: beforeEditCancel.draftVersion, afterDraftVersion: afterEditCancel.draftVersion,
        beforeDraftDigest: beforeEditCancel.draftDigest, afterDraftDigest: afterEditCancel.draftDigest });
    await reloadResult({ outputId: targetDoc.outputId, headers: ['Record ID', 'Status'], rows: oracle.append.rows,
      name: 'Saved-edit Cancel reload preserves exact original APPEND rows within five seconds' });

    await editSaved();
    await fill('Set saved APPEND output label to Clinical status', statusLabel, 'Clinical status', async () => {
      await waitProposal(targetDoc.outputId, oracle.append.rows.length);
    });
    assertGrid('saved APPEND Apply preview retains exact rows and null padding', await readGrid('proposal'),
      ['Record ID', 'Clinical status'], oracle.append.rows);
    await click('Apply saved APPEND label edit', page.getByTestId('construction-apply-proposal'), async () => {
      await waitSaved(targetDoc.outputId, oracle.append.rows.length);
    });
    builder = await readBuilder();
    const editedDoc = documentByOutput(builder, targetDoc.outputId);
    const editedStep = editedDoc.construction?.steps?.at(-1);
    requireCheck('persistence', 'saved APPEND label edit retains stable step and explicit Patient null padding',
      editedStep?.id === savedStep.id && editedStep.outputs?.find(output => output.name === 'status')?.label === 'Clinical status' &&
      editedStep.operation?.combine?.kind === 'APPEND' && editedStep.outputs?.find(output => output.name === 'status')?.nullable === true,
      { stepId: editedStep?.id, expectedStepId: savedStep.id, outputs: editedStep?.outputs,
        projections: editedStep?.operation?.combine?.projections });
    const editedInputRefs = (editedStep.inputs ?? []).map(input => [input.tableId, input.revisionId, input.outputId]);
    assert.deepEqual(editedInputRefs, revisions.map(revision => [revision.tableId, revision.revisionId, revision.outputId]));
    assert((editedStep.inputs ?? []).every(input => input.kind === 'TABLE_REVISION'));
    await reloadResult({ outputId: targetDoc.outputId, headers: ['Record ID', 'Clinical status'], rows: oracle.append.rows,
      name: 'Saved-edit Apply reload reaches exact APPEND rows within five seconds' });

    const removalBaselineBuilder = await readBuilder();
    const removalBaseline = { baselineBuilder: removalBaselineBuilder,
      snapshot: snapshotSourceDocument(documentByOutput(removalBaselineBuilder, targetDoc.outputId)) };
    await restoreEmpty(targetDoc, removalBaseline, savedStep.id);

    const afterLifecycle = await readBuilder();
    checkScope(afterLifecycle);
    const sourceDocsAfter = sources.map(source => snapshotSourceDocument(documentByOutput(afterLifecycle, source.outputId)));
    const sourcesStable = sameSourceDocuments(sourceDocsAfter, sourceSnapshots);
    const rawWitnessRereads = Object.fromEntries(CDA_PUBLISHED_APPEND_SOURCES.map(definition => {
      const selected = oracle.sources[definition.sourceKey];
      const rereadRows = runArango(arangoContainer, cdaPublishedAppendRereadQuery({ project, generation,
        resourceType: definition.resourceType, documentIDs: selected.map(member => member._id) }),
      `Final exact raw reread for ${definition.sourceKey}`);
      return [definition.sourceKey, assertCdaPublishedAppendReread(rereadRows,
        oracle.exactExpected[definition.sourceKey], { project, generation, resourceType: definition.resourceType })];
    }));
    const sourceIdentity = builderResponseIdentity(afterLifecycle, apiOrigin, project, explorer, explorerTitle,
      sources.map(source => ({ rootResourceType: source.definition.resourceType,
        title: source.doc.output.title, outputId: source.outputId })));
    requireCheck('persistence', 'published raw CDA source tables and exact Explorer scope remain unchanged through APPEND lifecycle',
      sourcesStable && sourceIdentity.bound && publishRequests === 1,
      { sourcesStable, before: sourceSnapshots, after: sourceDocsAfter, sourceIdentity, publishRequests, rawWitnessRereads,
        finalGeneration: afterLifecycle.catalog.generation,
        authorizationScopeDigest: afterLifecycle.catalog.authorizationScopeDigest });
    report.target.explorer = explorer;
    report.target.sourceOutputs = sources.map(source => ({ sourceKey: source.definition.sourceKey,
      outputId: source.outputId, selectionId: source.selection.id }));
    report.target.combineTarget = { outputId: targetDoc.outputId, rootResourceType: 'Observation' };
    report.nativeAppendProposals = captures.map(entry => ({ sequence: entry.sequence, outputId: entry.body?.outputId,
      status: entry.status, response: entry.response ?? null,
      inputRefs: entry.body?.candidateConstruction?.steps?.at(-1)?.inputs ?? [] }));
    page.off('request', onProposalRequest);
    page.off('response', onProposalResponse);
    requestCapture?.stop();
  } finally {
    page.off('request', onRequest);
    page.off('request', onProposalRequest);
    page.off('response', onProposalResponse);
    requestCapture?.stop();
  }
}
