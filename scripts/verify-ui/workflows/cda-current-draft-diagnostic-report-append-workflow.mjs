import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  assertCdaDiagnosticReportAppendReread,
  CDA_DIAGNOSTIC_REPORT_APPEND_SOURCES,
  CdaDiagnosticReportAppendWitnessUnavailable,
  cdaDiagnosticReportAppendRereadQuery,
  cdaDiagnosticReportAppendScanQuery,
  prepareCdaDiagnosticReportAppendOracle,
} from '../helpers/cda-current-draft-diagnostic-report-append-oracle.mjs';
import { currentDraftSourceEvidence, workspaceOutputOption } from '../helpers/builder-combine-draft-helpers.mjs';
import { nativeCombineTargetBindingEvidence } from '../helpers/builder-combine-helpers.mjs';
import { proposalPreviewReadinessExpression } from '../helpers/proposal-preview-readiness.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { assertCdaNoAuthRuntime } from '../helpers/cda-no-auth-runtime.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const TABLE_SELECTOR = '[data-testid="preview-table-scroll"] [role="table"]';
const encoded = value => encodeURIComponent(value);
const apiRoot = (project, explorer) => `/api/v1/projects/${encoded(project)}/explorers/${encoded(explorer)}`;
const normalized = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();

function runAQL(container, query, label) {
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const invocation = buildArangoShellInvocation({ container, script, database: 'loom_dev' });
  const result = spawnSync(invocation.command, invocation.args,
    { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `${label} failed: ${normalized(result.stderr || result.stdout).slice(0, 1_800)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `${label} returned no JSON array: ${normalized(result.stdout).slice(0, 800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows), `${label} did not return an array`);
  return rows;
}

export async function cdaCurrentDraftDiagnosticReportAppendWorkflow({ page, cda }) {
  const { target, request, report } = cda;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target);
  assert(project && generation && apiOrigin && uiOrigin,
    'Owned CDA fixture must provide project, generation, API, and UI origins');
  assert.equal(project, target.fixtureProject, 'DiagnosticReport witnesses must use the exact owned project');
  assert.equal(generation, 'cda-fhir-v1', 'DiagnosticReport APPEND requires the pinned FHIR generation');
  const ownedApiRuntimeProof = assertCdaNoAuthRuntime({ apiContainer: target.apiContainer });
  report.ownedApiRuntimeProof = ownedApiRuntimeProof;

  let oracle;
  try {
    const scan = runAQL(arangoContainer, cdaDiagnosticReportAppendScanQuery({ project, generation }),
      'Bounded exact-scope DiagnosticReport.status scan');
    oracle = prepareCdaDiagnosticReportAppendOracle(scan, { project, generation });
  } catch (error) {
    if (!(error instanceof CdaDiagnosticReportAppendWitnessUnavailable)) throw error;
    report.boundedWitnessUnavailable = error.evidence;
    report.gaps ??= [];
    report.gaps.push({
      assertion: 'bounded DiagnosticReport.status witnesses form two disjoint duplicate-category current-draft APPEND inputs',
      status: 'untested',
      reason: error.message,
      evidence: error.evidence,
    });
    throw error;
  }

  const api = async (path, body) => {
    const url = `${apiOrigin}${path}`;
    const headers = { 'X-Request-ID': `cda-diagnostic-report-append-${randomUUID()}` };
    const response = body === undefined
      ? await request.get(url, { headers, timeout: 30_000 })
      : await request.post(url, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${normalized(text).slice(0, 1_200)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_800)}`);
    return value;
  };
  let explorer;
  let explorerBase;
  let requestCapture;
  let builder;
  let baseline;
  let publishRequests = 0;
  const ownedOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
  const onRequest = browserRequest => {
    try {
      const url = new URL(browserRequest.url());
      if (ownedOrigins.has(url.origin) && url.pathname.endsWith('/authoring/v2/publish')) publishRequests += 1;
    } catch { /* A malformed request URL cannot be a Publish call. */ }
  };
  page.on('request', onRequest);

  const requireCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const waitFunction = (predicate, timeout = ACTION_TIMEOUT_MS) =>
    page.waitForFunction(predicate, undefined, { timeout: Math.min(ACTION_TIMEOUT_MS, timeout) });
  const waitSelector = (selector, timeout = ACTION_TIMEOUT_MS) =>
    page.locator(selector).waitFor({ state: 'visible', timeout: Math.min(ACTION_TIMEOUT_MS, timeout) });
  const action = (label, locator, perform, after, editable = false) => cda.action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after, requiredCheck: 'all native DiagnosticReport current-draft APPEND actions complete within five seconds' } : {}),
    ...(editable ? { editable: true } : {}),
  });
  const click = (label, locator, after) => action(label, locator, item => item.click({ timeout: ACTION_TIMEOUT_MS }), after);
  const fill = (label, locator, value, after) => action(label, locator, item => item.fill(value, { timeout: ACTION_TIMEOUT_MS }), after, true);
  const select = (label, locator, value, after) => action(label, locator, item => item.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const explorerPath = path => `${explorerBase}${path}`;
  const readBuilder = () => api(explorerPath('/authoring/v2/builder'));
  const documentByOutput = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected one current-draft document for ${outputId}`);
    return matches[0];
  };
  const checkScope = state => {
    assert.equal(state.catalog?.generation, generation, 'Builder left the pinned CDA generation');
    assert.equal(state.catalog?.snapshotToken, baseline.snapshotToken, 'Builder snapshot changed during the current-draft lifecycle');
    assert.equal(state.catalog?.authorizationScopeDigest, baseline.authorizationScopeDigest,
      'Builder authorization scope changed during the current-draft lifecycle');
  };
  const command = async commands => {
    assert(builder?.catalog?.snapshotToken, 'Current-draft commands require the exact Builder snapshot');
    await api(explorerPath('/authoring/v2/commands'), {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await readBuilder();
    checkScope(builder);
    return builder;
  };
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal'
      ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
    if (kind === 'proposal' && !proposal) return { ready: false, headers: [], rows: [] };
    const table = kind === 'proposal' ? proposal.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [] };
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
  const assertRows = (name, grid, headers, expectedRows) => {
    const actual = grid.rows.map(row => row.map(normalized));
    const expected = expectedRows.map(row => row.map(normalized));
    const ok = grid.ready && isDeepStrictEqual(grid.headers, headers) &&
      isDeepStrictEqual(sortRows(actual), sortRows(expected));
    requireCheck('correctness', name, ok, { headers: grid.headers, expectedHeaders: headers,
      actual, expected, comparisonMode: 'unordered-row-multiset', rowOrderAsserted: false, multiplicityPreserved: true });
    assert(ok, `${name}: exact rows differ from the bounded raw DiagnosticReport oracle`);
  };
  const savedReady = (outputId, rowCount) => `(()=>{const selected=document.querySelector(${JSON.stringify(
    `[data-testid="construction-table-${outputId}"][aria-current="page"]`)});const table=document.querySelector(${JSON.stringify(TABLE_SELECTOR)});return Boolean(selected&&table&&table.getAttribute('aria-rowcount')===${JSON.stringify(String(rowCount + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
  const emptyTargetReady = outputId => `(()=>{const selected=document.querySelector(${JSON.stringify(
    `[data-testid="construction-table-${outputId}"][aria-current="page"]`)});const preview=document.querySelector('[data-testid="construction-preview"]');return Boolean(selected&&!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-operation-editor"]')&&preview?.dataset.previewStatus==='empty'&&preview.dataset.previewOutputId===${JSON.stringify(outputId)}&&!preview.querySelector('[role="table"]'))})()`;
  const waitSaved = (outputId, count) => waitFunction(savedReady(outputId, count));
  const waitProposal = (outputId, count) => waitFunction(proposalPreviewReadinessExpression(outputId, count));
  const waitEmptyTarget = outputId => waitFunction(emptyTargetReady(outputId));
  const tableLocator = outputId => page.getByTestId(`construction-table-${outputId}`);
  const selectTable = async (outputId, rowCount, label) => {
    const locator = tableLocator(outputId);
    if (await locator.getAttribute('aria-current') === 'page') {
      if (rowCount !== undefined) await waitSaved(outputId, rowCount);
      return;
    }
    await click(label, locator, async () => {
      if (rowCount === undefined) await waitFunction(`Boolean(document.querySelector('[data-testid="construction-table-${outputId}"][aria-current="page"]'))`);
      else await waitSaved(outputId, rowCount);
    });
  };
  const reload = async (outputId, count, label) => {
    await selectTable(outputId, count, label);
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    if (count === undefined) await waitEmptyTarget(outputId);
    else await waitSaved(outputId, count);
  };
  const assertCurrentDraftAppend = async (targetOutputId, expectedSources, expectedRows, fromIndex) => {
    const base = await readBuilder();
    checkScope(base);
    const event = await requestCapture.waitFor(entry => entry.path === explorerPath('/authoring/v2/construction-proposals') &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
      requestCapture.rawRequestBody(entry)?.outputId === targetOutputId &&
      requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.at(-1)?.operation?.combine?.kind === 'APPEND',
    { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    const body = requestCapture.rawRequestBody(event);
    const response = requestCapture.rawResponseBody(event);
    const step = body?.candidateConstruction?.steps?.at(-1);
    const inputIDs = (step?.inputs ?? []).map(input => input.outputId);
    const sources = currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: expectedSources,
      sourceDocuments: base.workspace.documents });
    const preview = response?.preview;
    const currentCAS = body?.expectedDraftVersion === base.draftVersion &&
      body?.expectedDraftDigest === base.draftDigest && body?.snapshotToken === base.catalog.snapshotToken;
    const valid = step?.operation?.kind === 'COMBINE' && step.operation.combine?.kind === 'APPEND' &&
      isDeepStrictEqual(inputIDs, expectedSources) && sources.ok && currentCAS &&
      (step.inputs ?? []).every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId) &&
      response?.previewStatus === 'READY' && response.proposalId === preview?.receiptId &&
      preview?.outputId === targetOutputId && Number(preview?.rowCount) === expectedRows.length;
    requireCheck('correctness', 'native DiagnosticReport APPEND proposal binds exact current-draft GROUP siblings and a ready receipt', valid,
      { origin: event.origin, path: event.path, requestId: event.requestId, inputIDs, expectedSources, currentCAS,
        sourceEvidence: sources, previewStatus: response?.previewStatus, proposalId: response?.proposalId,
        preview, stepId: step?.id });
    assert(valid, 'DiagnosticReport APPEND candidate did not bind the exact current-draft inputs and ready receipt');
    return { event, body, response, step };
  };

  try {
    const rawEvidence = {
      project,
      generation,
      resourceType: 'DiagnosticReport',
      fieldPath: 'status',
      scanLimitPerResource: oracle.scanLimitPerResource,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      resources: oracle.resources,
      selected: Object.fromEntries(Object.entries(oracle.sources).map(([sourceKey, rows]) => [sourceKey,
        rows.map(row => ({ arangoDocumentKey: row._id, fhirID: row.id, status: row.fieldValue }))])),
      status: oracle.status,
      expectedAppendRows: oracle.append.rows,
      duplicateStatusCount: oracle.append.duplicateStatusCount,
    };
    requireCheck('correctness', 'bounded raw DiagnosticReport.status witnesses select two disjoint duplicate-category current-draft APPEND inputs',
      Object.values(oracle.sources).every(rows => rows.length === 2) && oracle.append.duplicateStatusCount === 2, rawEvidence);
    report.rawOracle = rawEvidence;

    const requestedExplorer = `cda-diagnostic-report-append-${randomUUID()}`;
    const title = `CDA current-draft DiagnosticReport APPEND ${randomUUID().slice(0, 8)}`;
    const created = await api(`/api/v1/projects/${encoded(project)}/explorers`, { name: requestedExplorer, title });
    explorer = created.explorerId ?? created.id ?? created.explorer?.id ?? requestedExplorer;
    assert.equal(explorer, requestedExplorer, 'Fresh owned Explorer must retain its unique requested identity');
    explorerBase = apiRoot(project, explorer);
    requestCapture = cda.captureRequests(`${explorerBase}/authoring/v2`, { responsePaths: /commands|construction-proposals|preview/ });
    report.target.explorer = explorer;
    const list = await api(`/api/v1/projects/${encoded(project)}/explorers`);
    const summaries = Array.isArray(list) ? list : list.explorers ?? list.value ?? [];
    const matches = summaries.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
    assert.equal(matches.length, 1, 'Fresh CDA Explorer must appear exactly once in its owned project');
    assert.equal(matches[0].project, project);
    assert.equal(matches[0].title, title);
    assert.equal(matches[0].management, 'INTERACTIVE');
    builder = await readBuilder();
    const emptyDocuments = builder.workspace?.documents ?? [];
    assert.equal(emptyDocuments.length, 0, 'Fresh DiagnosticReport APPEND Explorer must start with an empty draft');
    assert.equal(builder.catalog?.generation, generation);
    assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest);
    baseline = { snapshotToken: builder.catalog.snapshotToken,
      authorizationScopeDigest: builder.catalog.authorizationScopeDigest };
    requireCheck('correctness', 'fresh DiagnosticReport APPEND Explorer preserves exact project, generation, snapshot, baseline scope, and empty draft',
      matches[0].project === project && builder.catalog.generation === generation && emptyDocuments.length === 0,
      { project, explorer, generation, snapshotToken: baseline.snapshotToken,
        authorizationScopeDigest: baseline.authorizationScopeDigest, draftVersion: builder.draftVersion });

    const root = builder.catalog.nodes.find(node => node.resourceType === 'DiagnosticReport' && node.rowRootEligible);
    assert(root, 'Scoped CDA catalog must expose a row-root-eligible DiagnosticReport');
    const candidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === root.nodeId);
    const idCandidate = candidates.find(candidate => candidate.fieldPath === 'id');
    const statusCandidate = candidates.find(candidate => candidate.fieldPath === 'status');
    assert(idCandidate, 'Scoped DiagnosticReport catalog must expose the FHIR id field');
    assert(statusCandidate, 'Scoped DiagnosticReport catalog must expose the status field');
    assert.equal(statusCandidate.logicalType, 'string', 'DiagnosticReport.status must be scalar text');
    assert.equal(statusCandidate.repeated, false, 'DiagnosticReport.status must not be repeated');

    const sources = [];
    for (const definition of CDA_DIAGNOSTIC_REPORT_APPEND_SOURCES) {
      const rawRows = oracle.sources[definition.sourceKey];
      const priorIDs = builder.workspace.documents.map(document => document.output.id);
      await command([{ type: 'CREATE_TABLE', title: definition.title, rootNodeId: root.nodeId }]);
      const createdDocuments = builder.workspace.documents.filter(document => !priorIDs.includes(document.output.id));
      assert.equal(createdDocuments.length, 1, 'CREATE_TABLE must add one rooted DiagnosticReport source');
      const outputId = createdDocuments[0].output.id;
      await command([
        { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
          projectionMode: 'VALUE', initialPresentation: 'TABLE', title: `${definition.title} FHIR ID` },
        { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: statusCandidate.candidateId,
          projectionMode: 'VALUE', initialPresentation: 'TABLE', title: `${definition.title} status` },
      ]);
      const refs = rawRows.map(row => ({ project, generation, resourceType: 'DiagnosticReport', id: row.id }));
      const selection = await api(explorerPath('/selections'), {
        snapshotToken: builder.catalog.snapshotToken,
        idempotencyKey: `cda-diagnostic-report-append-${randomUUID()}`,
        source: { kind: 'resources', resources: { refs } },
      });
      assert.equal(selection.project, project);
      assert.equal(selection.generation, generation);
      assert.equal(selection.resourceType, 'DiagnosticReport');
      assert.equal(selection.scopeDigest, baseline.authorizationScopeDigest);
      assert.equal(selection.memberCount, 2);
      const selectionRead = await api(explorerPath(`/selections/${encoded(selection.id)}?limit=100`));
      assert.equal(selectionRead.revision?.id, selection.id);
      assert.equal(selectionRead.members?.length, 2);
      const actualRefs = (selectionRead.members ?? []).map(member => {
        const ref = member.ref ?? {};
        return `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
      }).sort();
      const expectedRefs = refs.map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
      assert.deepEqual(actualRefs, expectedRefs, 'Immutable DiagnosticReport selection must exactly match the four-row oracle partition');
      const routes = await api(explorerPath('/authoring/v2/population-routes'), {
        snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
      });
      const direct = (routes.choices ?? []).find(choice => choice.route.length === 0);
      assert(direct, 'Exact DiagnosticReport selection must expose its direct root population route');
      await command([{ type: 'SET_TABLE_POPULATION', outputId,
        selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
      const document = documentByOutput(builder, outputId);
      assert.equal(document.rootResourceType, 'DiagnosticReport');
      assert.equal(document.population?.selectionRevisionId, selection.id);
      const statusColumns = document.columns.filter(column => column.source?.field?.path === 'status' &&
        column.source?.field?.projectionMode === 'VALUE');
      assert.equal(statusColumns.length, 1);
      requireCheck('correctness', `${definition.title} immutable current-draft population binds exact scoped raw FHIR witnesses`, true,
        { outputId, selectionId: selection.id, memberCount: selection.memberCount, actualRefs, expectedRefs,
          scopeDigest: selection.scopeDigest, statusColumnId: statusColumns[0].id });
      sources.push({ definition, outputId, selection, rawRows, statusColumn: statusColumns[0] });
      builder = await readBuilder();
      checkScope(builder);
    }

    const checkProposalRows = async (outputId, expectedRows, expectedHeaders, label) => {
      await waitProposal(outputId, expectedRows.length);
      const grid = await readGrid('proposal');
      assertRows(label, grid, expectedHeaders, expectedRows);
    };
    const groupSource = async source => {
      await selectTable(source.outputId, source.rawRows.length, `Open ${source.definition.title} raw population`);
      const sourceGrid = await readGrid('saved');
      const sourceDocument = documentByOutput(await readBuilder(), source.outputId);
      const statusColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'status');
      const idColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'id');
      assert(idColumn && statusColumn);
      const idIndex = sourceGrid.headers.indexOf(idColumn.label);
      const statusIndex = sourceGrid.headers.indexOf(statusColumn.label);
      const visibleRaw = sourceGrid.rows.map(row => [row[idIndex], row[statusIndex]])
        .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
      const expectedRaw = source.rawRows.map(row => [row.id, String(row.fieldValue)])
        .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
      const sourceExact = sourceGrid.ready && idIndex >= 0 && statusIndex >= 0 && isDeepStrictEqual(visibleRaw, expectedRaw);
      requireCheck('correctness', `${source.definition.title} root preview equals its exact immutable raw population`, sourceExact,
        { outputId: source.outputId, selectionId: source.selection.id, headers: sourceGrid.headers, visibleRaw, expectedRaw });
      assert(sourceExact, `${source.definition.title} did not display its exact selected raw rows`);
      await click(`Open ${source.definition.title} row settings`, page.getByTestId('construction-rows-settings-trigger'),
        async () => waitFunction(`document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled===false`));
      await click(`Open native ${source.definition.title} GROUP`, page.getByTestId('construction-action-group-rows'),
        async () => waitSelector('select[aria-label="Summary 1"]'));
      const groupKey = page.locator(`input[type="checkbox"][aria-label=${JSON.stringify(`Group by ${statusColumn.label}`)}]`);
      assert.equal(await groupKey.count(), 1, 'GROUP must expose the selected scalar DiagnosticReport.status field');
      const summary = page.locator('select[aria-label="Summary 1"]');
      assert.equal(await summary.inputValue(), 'COUNT_ROWS', 'Native GROUP starts with COUNT_ROWS');
      const base = await readBuilder();
      const requestFrom = report.nativeRequests.length;
      await click(`Select ${source.definition.title} status GROUP key and render COUNT_ROWS`, groupKey,
        async () => waitProposal(source.outputId, 1));
      await checkProposalRows(source.outputId, [[oracle.status, '2']], [statusColumn.label, 'Row count'],
        `${source.definition.title} native GROUP status count matches exact raw witnesses`);
      const event = await requestCapture.waitFor(entry => entry.path === explorerPath('/authoring/v2/construction-proposals') &&
        entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
        requestCapture.rawRequestBody(entry)?.outputId === source.outputId &&
        requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.at(-1)?.operation?.kind === 'GROUP',
      { fromIndex: requestFrom, timeoutMs: ACTION_TIMEOUT_MS });
      const body = requestCapture.rawRequestBody(event);
      const response = requestCapture.rawResponseBody(event);
      const step = body.candidateConstruction.steps.at(-1);
      const key = step.operation.group.keys.find(item => item.inputColumnId === statusColumn.id);
      const aggregate = step.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
      const cas = body.expectedDraftVersion === base.draftVersion && body.expectedDraftDigest === base.draftDigest &&
        body.snapshotToken === base.catalog.snapshotToken;
      const ready = response?.previewStatus === 'READY' && response.preview?.outputId === source.outputId &&
        response.proposalId === response.preview?.receiptId && Boolean(response.preview?.receiptId);
      const valid = step.operation.kind === 'GROUP' && key && aggregate && cas && ready;
      requireCheck('correctness', `${source.definition.title} native GROUP binds its exact raw status and COUNT_ROWS`, valid,
        { outputId: source.outputId, statusColumnId: statusColumn.id, key, aggregate, cas,
          proposalId: response?.proposalId, preview: response?.preview });
      assert(valid, 'Native DiagnosticReport GROUP did not bind the current CAS and READY receipt');
      await click(`Apply ${source.definition.title} native GROUP`, page.getByTestId('construction-apply-proposal'),
        async () => waitSaved(source.outputId, 1));
      const after = await readBuilder();
      checkScope(after);
      const savedDocument = documentByOutput(after, source.outputId);
      const savedGroup = savedDocument.construction?.steps?.find(item => item.operation?.kind === 'GROUP');
      const savedKey = savedGroup?.outputs?.find(output => savedGroup.operation.group.keys.some(item => item.outputColumnId === output.id));
      const savedCount = savedGroup?.outputs?.find(output => savedGroup.operation.group.aggregates.some(item => item.outputColumnId === output.id));
      assert(savedGroup && savedKey && savedCount);
      await reload(source.outputId, 1, `Reload ${source.definition.title} grouped output`);
      const finalBuilder = await readBuilder();
      checkScope(finalBuilder);
      const persistent = documentByOutput(finalBuilder, source.outputId).construction?.steps
        ?.some(item => item.id === savedGroup.id && item.operation?.kind === 'GROUP');
      requireCheck('persistence', `${source.definition.title} GROUP key and COUNT_ROWS survive reload`, persistent,
        { outputId: source.outputId, groupStepId: savedGroup.id, keyOutput: savedKey, countOutput: savedCount });
      assert(persistent);
      assertRows(`${source.definition.title} grouped status values survive reload`, await readGrid('saved'),
        [savedKey.label, savedCount.label], [[oracle.status, '2']]);
      return { ...source, group: savedGroup, keyOutput: savedKey, countOutput: savedCount,
        keyLabel: savedKey.label, countLabel: savedCount.label };
    };
    const groupedSources = [];
    for (const source of sources) groupedSources.push(await groupSource(source));
    requireCheck('correctness', 'two immutable DiagnosticReport current-draft sources preserve disjoint exact status populations',
      groupedSources.length === 2 && new Set(groupedSources.flatMap(source => source.rawRows.map(row => row._id))).size === 4,
      { sources: groupedSources.map(source => ({ outputId: source.outputId, selectionId: source.selection.id,
        count: source.selection.memberCount, groupStepId: source.group.id })), status: oracle.status });

    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await waitSelector(`[data-testid="construction-table-${groupedSources[0].outputId}"]`);
    await selectTable(groupedSources[0].outputId, 1, 'Open first DiagnosticReport GROUP source before APPEND');
    builder = await readBuilder();
    checkScope(builder);
    const priorOutputs = builder.workspace.documents.map(document => document.output.id);
    const rootNodeIDs = builder.catalog.nodes.filter(node => node.resourceType === 'DiagnosticReport' && node.rowRootEligible)
      .map(node => node.nodeId);
    const combine = page.getByTestId('construction-action-combine');
    await click('Open native Combine and create an empty DiagnosticReport target', combine,
      async () => waitSelector('[data-testid="construction-combine-editor"]'));
    const createEvent = await requestCapture.waitFor(entry => entry.path === explorerPath('/authoring/v2/commands') &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
      requestCapture.rawRequestBody(entry)?.commands?.some(item => item.type === 'CREATE_TABLE'),
    { timeoutMs: ACTION_TIMEOUT_MS });
    const createBody = requestCapture.rawRequestBody(createEvent);
    const createResponse = requestCapture.rawResponseBody(createEvent);
    const mountedOutputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]')
      .getAttribute('data-output-id');
    const targetEvidence = nativeCombineTargetBindingEvidence({ requestBody: createBody, responseStatus: createEvent.status,
      response: createResponse, expectedRootNodeIds: rootNodeIDs, expectedRootResourceType: 'DiagnosticReport',
      previousOutputIds: priorOutputs, mountedOutputId });
    requireCheck('correctness', 'native Combine creates a separate rooted empty DiagnosticReport APPEND target',
      targetEvidence.ok, targetEvidence);
    assert(targetEvidence.ok, `Native Combine target did not match its exact command: ${JSON.stringify(targetEvidence)}`);
    const targetOutputId = targetEvidence.outputId;
    builder = await readBuilder();
    checkScope(builder);
    const emptyTarget = documentByOutput(builder, targetOutputId);
    assert.equal(emptyTarget.columns.length, 0);
    assert.equal(emptyTarget.construction?.steps?.length ?? 0, 0);

    const configureAppend = async () => {
      await click('Choose native APPEND', page.getByTestId('construction-combine-choice-append'),
        async () => waitSelector('select[aria-label="Input table 1"]'));
      for (const [index, source] of groupedSources.entries()) {
        const selector = `select[aria-label="Input table ${index + 1}"]`;
        const expected = workspaceOutputOption(source.outputId);
        const options = await page.locator(selector).evaluate(element => [...element.options].map(option => ({
          value: option.value, group: option.parentElement?.label, disabled: option.disabled,
        })));
        const match = options.filter(option => option.value === expected && option.group === 'Current draft tables' && !option.disabled);
        assert.equal(match.length, 1, `APPEND must offer the exact current-draft ${source.definition.title}`);
        await select(`Bind APPEND input ${index + 1} to exact current-draft GROUP source`, page.locator(selector), expected,
          async () => waitFunction(`document.querySelector(${JSON.stringify(selector)})?.value===${JSON.stringify(expected)}`));
      }
      const addOutput = async (index, name, label, fieldLabels) => {
        await click(`Add APPEND output field ${index}`, page.getByRole('button', { name: 'Add output field', exact: true }),
          async () => waitSelector(`input[aria-label="Output field ${index} name"]`));
        await fill(`Name APPEND output field ${index}`, page.locator(`input[aria-label="Output field ${index} name"]`), name);
        await fill(`Label APPEND output field ${index}`, page.locator(`input[aria-label="Output field ${index} label"]`), label);
        let finalMapping;
        for (const [sourceIndex, fieldLabel] of fieldLabels.entries()) {
          const selector = `select[aria-label="Output field ${index} matching field in input ${sourceIndex + 1}"]`;
          const candidateOptions = await page.locator(selector).evaluate(element => [...element.options].map(option => ({
            value: option.value, text: option.textContent, disabled: option.disabled,
          })));
          const wanted = normalized(fieldLabel).toLowerCase();
          const matching = candidateOptions.filter(option => {
            if (option.disabled) return false;
            const text = normalized(option.text).toLowerCase().replace(/ · current draft$/, '');
            return text === wanted || text.startsWith(`${wanted} (`) || text.startsWith(`${wanted} · `);
          });
          assert.equal(matching.length, 1,
            `APPEND output mapping must expose exactly one ${JSON.stringify(fieldLabel)}: ${JSON.stringify(candidateOptions)}`);
          const isFinal = index === 2 && sourceIndex === fieldLabels.length - 1;
          if (!isFinal) await select(`Map APPEND ${label} from input ${sourceIndex + 1}`,
            page.locator(selector), matching[0].value);
          if (isFinal) finalMapping = { selector, value: matching[0].value };
        }
        return finalMapping;
      };
      await addOutput(1, 'status', 'Status', groupedSources.map(source => source.keyLabel));
      const countTrigger = await addOutput(2, 'row_count', 'Row count', groupedSources.map(source => source.countLabel));
      const trigger = countTrigger;
      assert(trigger?.selector && trigger?.value, 'Final APPEND mapping must provide an automatic-preview trigger');
      const index = report.nativeRequests.length;
      await select('Complete native DiagnosticReport APPEND and render duplicate status rows',
        page.locator(trigger.selector), trigger.value,
        async () => waitProposal(targetOutputId, oracle.append.rows.length));
      assertRows('DiagnosticReport APPEND proposal preserves duplicate status/count rows', await readGrid('proposal'),
        ['Status', 'Row count'], oracle.append.rows);
      return { requestFrom: index };
    };
    const cancelBase = await readBuilder();
    checkScope(cancelBase);
    const canceled = await configureAppend();
    const canceledCandidate = await assertCurrentDraftAppend(targetOutputId, groupedSources.map(source => source.outputId),
      oracle.append.rows, canceled.requestFrom);
    await click('Cancel DiagnosticReport APPEND proposal', page.getByTestId('construction-cancel-proposal'),
      async () => waitEmptyTarget(targetOutputId));
    await reload(targetOutputId, undefined, 'Reload canceled empty DiagnosticReport APPEND target');
    const afterCancel = await readBuilder();
    checkScope(afterCancel);
    const emptyAfterCancel = documentByOutput(afterCancel, targetOutputId);
    const cancelUnchanged = afterCancel.draftVersion === cancelBase.draftVersion &&
      afterCancel.draftDigest === cancelBase.draftDigest &&
      isDeepStrictEqual(afterCancel.workspace.documents, cancelBase.workspace.documents) &&
      emptyAfterCancel.columns.length === 0 && (emptyAfterCancel.construction?.steps?.length ?? 0) === 0;
    requireCheck('persistence', 'Cancel leaves DiagnosticReport APPEND candidate and empty target unchanged after reload', cancelUnchanged,
      { beforeVersion: cancelBase.draftVersion, afterVersion: afterCancel.draftVersion,
        beforeDigest: cancelBase.draftDigest, afterDigest: afterCancel.draftDigest,
        targetOutputId, candidateStepId: canceledCandidate.step.id });
    assert(cancelUnchanged);

    const applied = await configureAppend();
    const applyPreview = await assertCurrentDraftAppend(targetOutputId, groupedSources.map(source => source.outputId),
      oracle.append.rows, applied.requestFrom);
    await click('Apply current-draft DiagnosticReport APPEND', page.getByTestId('construction-apply-proposal'),
      async () => waitSaved(targetOutputId, oracle.append.rows.length));
    builder = await readBuilder();
    checkScope(builder);
    const appliedDocument = documentByOutput(builder, targetOutputId);
    const savedStep = appliedDocument.construction?.steps?.at(-1);
    const inputEvidence = currentDraftSourceEvidence({ inputs: savedStep?.inputs,
      expectedOutputIDs: groupedSources.map(source => source.outputId), sourceDocuments: builder.workspace.documents });
    const appliedExactly = savedStep?.id === applyPreview.step.id && savedStep.operation?.kind === 'COMBINE' &&
      savedStep.operation.combine?.kind === 'APPEND' && inputEvidence.ok;
    requireCheck('persistence', 'applied DiagnosticReport APPEND preserves ordered current-draft GROUP inputs and step identity',
      appliedExactly, { targetOutputId, stepId: savedStep?.id, candidateStepId: applyPreview.step.id, inputEvidence });
    assert(appliedExactly);
    await reload(targetOutputId, oracle.append.rows.length, 'Reload applied DiagnosticReport APPEND output');
    assertRows('applied DiagnosticReport APPEND row multiset survives reload', await readGrid('saved'),
      ['Status', 'Row count'], oracle.append.rows);

    const editedAppendRows = [
      [oracle.status, '1'],
      [oracle.status, '2'],
    ];
    const previewIdentity = async () => page.locator('[data-testid="construction-preview"]').evaluate(element => ({
      status: element.dataset.previewStatus ?? null,
      receiptId: element.dataset.previewReceiptId ?? null,
      outputId: element.dataset.previewOutputId ?? null,
      draftVersion: element.dataset.currentDraftVersion ?? null,
      draftDigest: element.dataset.currentDraftDigest ?? null,
    }));
    const openSavedGroupEditor = async source => {
      await selectTable(source.outputId, 1, `Select ${source.definition.title} for saved GROUP edit`);
      const historyEntry = page.getByTestId(`construction-history-step-${source.group.id}`);
      await click(`Select saved ${source.definition.title} GROUP step`, historyEntry,
        async () => waitFunction(`Boolean(document.querySelector('[data-testid="construction-edit-step-${source.group.id}"]:not(:disabled)'))`));
      const editButton = page.locator(`[data-testid="construction-edit-step-${source.group.id}"]:not(:disabled)`);
      await click(`Open saved ${source.definition.title} GROUP editor`, editButton,
        async () => waitSelector('[data-testid="construction-reshape-editor"]'));
    };
    const prepareDistinctGroupEdit = async source => {
      const base = await readBuilder();
      checkScope(base);
      const beforeSource = documentByOutput(base, source.outputId);
      const beforeGroup = beforeSource.construction?.steps?.find(step => step.id === source.group.id);
      const beforeAggregate = beforeGroup?.operation?.group?.aggregates?.find(item => item.operation === 'COUNT_ROWS');
      const beforeKey = beforeGroup?.operation?.group?.keys?.find(item => item.inputColumnId === source.statusColumn.id);
      assert(beforeGroup && beforeAggregate && beforeKey,
        'Saved DiagnosticReport GROUP must have the expected COUNT_ROWS aggregate and status key');
      await openSavedGroupEditor(source);
      const summarySelector = 'select[aria-label="Summary 1"]';
      const summaryOptions = await page.locator(summarySelector).evaluate(element => [...element.options].map(option => ({
        value: option.value, text: normalized(option.textContent), disabled: option.disabled,
      })));
      const distinctOptions = summaryOptions.filter(option => !option.disabled && option.value === 'COUNT_DISTINCT');
      assert.equal(distinctOptions.length, 1,
        `Saved GROUP editor must expose one COUNT_DISTINCT option: ${JSON.stringify(summaryOptions)}`);
      const index = report.nativeRequests.length;
      await select(`Change ${source.definition.title} aggregate to COUNT_DISTINCT`,
        page.locator(summarySelector), distinctOptions[0].value,
        async () => waitSelector('select[aria-label="Summary field 1"]'));
      const fieldSelector = 'select[aria-label="Summary field 1"]';
      const fieldOptions = await page.locator(fieldSelector).evaluate(element => [...element.options].map(option => ({
        value: option.value, text: normalized(option.textContent), disabled: option.disabled,
      })));
      const statusOptions = fieldOptions.filter(option => !option.disabled && option.value === source.statusColumn.id);
      assert.equal(statusOptions.length, 1,
        `COUNT_DISTINCT must bind the exact selected DiagnosticReport.status column: ${JSON.stringify(fieldOptions)}`);
      await select(`Bind ${source.definition.title} COUNT_DISTINCT to its existing status column`,
        page.locator(fieldSelector), statusOptions[0].value,
        async () => {
          await waitProposal(source.outputId, 1);
          assert.equal(await page.locator('input[aria-label="Summary output label 1"]').inputValue(), source.countLabel,
            'COUNT_DISTINCT edit must preserve the saved count output label without a synthetic rename');
        });
      const advanced = page.locator('[data-testid="construction-reshape-group-advanced"]');
      if (await advanced.count() === 1 && !(await advanced.evaluate(element => element.open))) {
        await click('Open saved GROUP aggregate output details', advanced.locator('summary'));
      }
      assertRows('saved DiagnosticReport COUNT_DISTINCT Group edit previews one distinct status value',
        await readGrid('proposal'), [source.keyLabel, source.countLabel], [[oracle.status, '1']]);
      const event = await requestCapture.waitFor(entry => {
        if (entry.path !== explorerPath('/authoring/v2/construction-proposals') || entry.method !== 'POST' ||
          entry.status !== 200 || !entry.completedAt) return false;
        const body = requestCapture.rawRequestBody(entry);
        const response = requestCapture.rawResponseBody(entry);
        const candidate = body?.candidateConstruction?.steps?.find(step => step.id === source.group.id);
        const aggregate = candidate?.operation?.group?.aggregates?.find(item => item.operation === 'COUNT_DISTINCT');
        const countOutput = candidate?.outputs?.find(output => output.id === source.countOutput.id);
        const key = candidate?.operation?.group?.keys?.find(item => item.outputColumnId === source.keyOutput.id);
        const currentCAS = body?.snapshotToken === base.catalog.snapshotToken &&
          body?.expectedDraftVersion === base.draftVersion && body?.expectedDraftDigest === base.draftDigest;
        const ready = response?.previewStatus === 'READY' && response.outputId === source.outputId &&
          response.snapshotToken === base.catalog.snapshotToken && response.draftVersion === base.draftVersion &&
          response.draftDigest === base.draftDigest && response.proposalId === response.preview?.receiptId &&
          response.preview?.outputId === source.outputId && Number(response.preview?.rowCount) === 1;
        return body?.outputId === source.outputId && currentCAS && candidate?.operation?.kind === 'GROUP' &&
          aggregate?.inputColumnId === source.statusColumn.id && aggregate.outputColumnId === source.countOutput.id &&
          key?.inputColumnId === source.statusColumn.id && countOutput?.label === source.countLabel && ready;
      }, { fromIndex: index, timeoutMs: ACTION_TIMEOUT_MS });
      const body = requestCapture.rawRequestBody(event);
      const response = requestCapture.rawResponseBody(event);
      const candidate = body?.candidateConstruction?.steps?.find(step => step.id === source.group.id);
      const candidateAggregate = candidate?.operation?.group?.aggregates?.find(item => item.operation === 'COUNT_DISTINCT');
      const candidateKey = candidate?.operation?.group?.keys?.find(item => item.outputColumnId === source.keyOutput.id);
      const candidateCount = candidate?.outputs?.find(output => output.id === beforeAggregate.outputColumnId);
      const currentCAS = body?.snapshotToken === base.catalog.snapshotToken &&
        body?.expectedDraftVersion === base.draftVersion && body?.expectedDraftDigest === base.draftDigest;
      const ready = response?.previewStatus === 'READY' && response.outputId === source.outputId &&
        response.snapshotToken === base.catalog.snapshotToken && response.draftVersion === base.draftVersion &&
        response.draftDigest === base.draftDigest &&
        response.proposalId === response.preview?.receiptId && response.preview?.outputId === source.outputId &&
        Number(response.preview?.rowCount) === 1;
      const valid = event.origin === new URL(uiOrigin).origin && candidate?.operation?.kind === 'GROUP' &&
        candidateAggregate?.inputColumnId === source.statusColumn.id &&
        candidateKey?.inputColumnId === source.statusColumn.id && candidateCount?.id === source.countOutput.id &&
        candidateCount?.label === source.countLabel && currentCAS && ready;
      requireCheck('correctness', 'DiagnosticReport COUNT_DISTINCT edit binds the same status key, output identity, and current-draft receipt',
        valid, { origin: event.origin, outputId: source.outputId, stepId: candidate?.id,
          inputColumnId: candidateAggregate?.inputColumnId, expectedInputColumnId: source.statusColumn.id,
          key: candidateKey, aggregate: candidateAggregate, countOutput: candidateCount, currentCAS,
          previewStatus: response?.previewStatus, proposalId: response?.proposalId, preview: response?.preview });
      assert(valid, 'COUNT_DISTINCT proposal must preserve the status key and bind its current-draft receipt');
      return { base, beforeGroup, beforeAggregate, beforeKey, event, candidate, candidateAggregate, candidateKey, candidateCount };
    };

    const firstSource = groupedSources[0];
    const priorTargetPreview = await previewIdentity();
    const cancelEditEvidence = await prepareDistinctGroupEdit(firstSource);
    await click('Cancel saved DiagnosticReport COUNT_DISTINCT Group edit',
      page.getByTestId('construction-cancel-proposal'),
      async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&Boolean(document.querySelector('[data-testid="construction-history"]'))`));
    const afterEditCancel = await readBuilder();
    checkScope(afterEditCancel);
    const cancelSource = documentByOutput(afterEditCancel, firstSource.outputId);
    const cancelGroup = cancelSource.construction?.steps?.find(step => step.id === firstSource.group.id);
    const cancelAggregate = cancelGroup?.operation?.group?.aggregates?.find(item => item.operation === 'COUNT_ROWS');
    const cancelAppend = documentByOutput(afterEditCancel, targetOutputId);
    const cancelAppendStep = cancelAppend.construction?.steps?.find(step => step.id === savedStep.id);
    const cancelInputs = currentDraftSourceEvidence({ inputs: cancelAppendStep?.inputs,
      expectedOutputIDs: groupedSources.map(source => source.outputId), sourceDocuments: afterEditCancel.workspace.documents });
    const cancelCAS = afterEditCancel.draftVersion === cancelEditEvidence.base.draftVersion &&
      afterEditCancel.draftDigest === cancelEditEvidence.base.draftDigest &&
      isDeepStrictEqual(afterEditCancel.workspace.documents, cancelEditEvidence.base.workspace.documents);
    assert.equal(cancelAggregate?.operation, 'COUNT_ROWS');
    await reload(firstSource.outputId, 1, 'Reload DiagnosticReport GROUP after canceled COUNT_DISTINCT edit');
    assertRows('canceled DiagnosticReport GROUP edit leaves the original COUNT_ROWS result after reload',
      await readGrid('saved'), [firstSource.keyLabel, firstSource.countLabel], [[oracle.status, '2']]);
    await reload(targetOutputId, oracle.append.rows.length, 'Reload APPEND after canceled DiagnosticReport GROUP edit');
    assertRows('canceled DiagnosticReport GROUP edit preserves prior dependent APPEND rows after reload',
      await readGrid('saved'), ['Status', 'Row count'], oracle.append.rows);
    const afterEditCancelReload = await readBuilder();
    checkScope(afterEditCancelReload);
    const cancelRowsAndCAS = cancelCAS && cancelAggregate?.operation === 'COUNT_ROWS' && cancelInputs.ok &&
      afterEditCancelReload.draftVersion === cancelEditEvidence.base.draftVersion &&
      afterEditCancelReload.draftDigest === cancelEditEvidence.base.draftDigest &&
      isDeepStrictEqual(afterEditCancelReload.workspace.documents, cancelEditEvidence.base.workspace.documents);
    requireCheck('persistence', 'Canceling saved DiagnosticReport COUNT_DISTINCT edit preserves prior Group, dependent APPEND rows, and draft CAS',
      cancelRowsAndCAS, { beforeVersion: cancelEditEvidence.base.draftVersion,
        afterVersion: afterEditCancelReload.draftVersion, beforeDigest: cancelEditEvidence.base.draftDigest,
        afterDigest: afterEditCancelReload.draftDigest, groupOperation: cancelAggregate?.operation,
        groupCountRows: '2', appendRows: oracle.append.rows, appendInputs: cancelInputs,
        priorAppendReceipt: priorTargetPreview.receiptId,
        cancelProposalReceipt: cancelEditEvidence.event && requestCapture.rawResponseBody(cancelEditEvidence.event)?.proposalId });
    assert(cancelRowsAndCAS, 'Canceling the saved GROUP edit must preserve both current-draft tables and CAS');

    const appliedEdit = await prepareDistinctGroupEdit(firstSource);
    await click('Apply saved DiagnosticReport COUNT_DISTINCT Group edit', page.getByTestId('construction-apply-proposal'),
      async () => waitSaved(firstSource.outputId, 1));
    const afterSourceEdit = await readBuilder();
    checkScope(afterSourceEdit);
    const editedSource = documentByOutput(afterSourceEdit, firstSource.outputId);
    const editedGroup = editedSource.construction?.steps?.find(step => step.id === firstSource.group.id);
    const editedAggregate = editedGroup?.operation?.group?.aggregates?.find(item => item.operation === 'COUNT_DISTINCT');
    const editedKey = editedGroup?.operation?.group?.keys?.find(item => item.outputColumnId === firstSource.keyOutput.id);
    const editedCount = editedGroup?.outputs?.find(output => output.id === firstSource.countOutput.id);
    const selectionStable = editedSource.population?.selectionRevisionId === firstSource.selection.id;
    const scopeStable = afterSourceEdit.catalog?.snapshotToken === baseline.snapshotToken &&
      afterSourceEdit.catalog?.authorizationScopeDigest === baseline.authorizationScopeDigest;
    const sourceEditCAS = afterSourceEdit.draftVersion > appliedEdit.base.draftVersion &&
      afterSourceEdit.draftDigest !== appliedEdit.base.draftDigest;
    const sourceEditPersisted = editedAggregate?.operation === 'COUNT_DISTINCT' &&
      editedAggregate.inputColumnId === firstSource.statusColumn.id && editedKey?.inputColumnId === firstSource.statusColumn.id &&
      editedCount?.id === firstSource.countOutput.id && selectionStable && scopeStable && sourceEditCAS;
    requireCheck('persistence', 'applied DiagnosticReport COUNT_DISTINCT edit preserves status selection, output identity, and authorization scope',
      sourceEditPersisted, { outputId: firstSource.outputId, groupStepId: editedGroup?.id,
        aggregate: editedAggregate, key: editedKey, countOutput: editedCount,
        selectionRevisionId: editedSource.population?.selectionRevisionId,
        expectedSelectionRevisionId: firstSource.selection.id, snapshotToken: afterSourceEdit.catalog?.snapshotToken,
        authorizationScopeDigest: afterSourceEdit.catalog?.authorizationScopeDigest,
        beforeVersion: appliedEdit.base.draftVersion, afterVersion: afterSourceEdit.draftVersion,
        beforeDigest: appliedEdit.base.draftDigest, afterDigest: afterSourceEdit.draftDigest });
    assert(sourceEditPersisted, 'Applied COUNT_DISTINCT edit must retain exact status population and current-draft scope');
    await reload(firstSource.outputId, 1, 'Reload applied DiagnosticReport COUNT_DISTINCT Group edit');
    assertRows('applied DiagnosticReport COUNT_DISTINCT Group result survives reload', await readGrid('saved'),
      [firstSource.keyLabel, firstSource.countLabel], [[oracle.status, '1']]);

    const recomputeFromIndex = report.nativeRequests.length;
    const targetPreviewAfterEdit = `(()=>{const selected=document.querySelector(${JSON.stringify(
      `[data-testid="construction-table-${targetOutputId}"][aria-current="page"]`)});const preview=document.querySelector('[data-testid="construction-preview"]');const table=document.querySelector(${JSON.stringify(TABLE_SELECTOR)});return Boolean(selected&&preview?.dataset.previewStatus==='ready'&&preview.dataset.previewOutputId===${JSON.stringify(targetOutputId)}&&preview.dataset.previewReceiptId!==${JSON.stringify(priorTargetPreview.receiptId)}&&Number(preview.dataset.currentDraftVersion)===${JSON.stringify(afterSourceEdit.draftVersion)}&&preview.dataset.currentDraftDigest===${JSON.stringify(afterSourceEdit.draftDigest)}&&table?.getAttribute('aria-rowcount')===${JSON.stringify(String(editedAppendRows.length + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
    await click('Select dependent APPEND after DiagnosticReport GROUP COUNT_DISTINCT edit', tableLocator(targetOutputId),
      async () => waitFunction(targetPreviewAfterEdit));
    const recomputedPreview = await previewIdentity();
    const recomputedBuilder = await readBuilder();
    checkScope(recomputedBuilder);
    const recomputedAppend = documentByOutput(recomputedBuilder, targetOutputId);
    const recomputedAppendStep = recomputedAppend.construction?.steps?.find(step => step.id === savedStep.id);
    const recomputedInputs = currentDraftSourceEvidence({ inputs: recomputedAppendStep?.inputs,
      expectedOutputIDs: groupedSources.map(source => source.outputId), sourceDocuments: recomputedBuilder.workspace.documents });
    const currentPreview = recomputedPreview.status === 'ready' && recomputedPreview.outputId === targetOutputId &&
      recomputedPreview.receiptId && recomputedPreview.receiptId !== priorTargetPreview.receiptId &&
      Number(recomputedPreview.draftVersion) === afterSourceEdit.draftVersion &&
      recomputedPreview.draftDigest === afterSourceEdit.draftDigest;
    const stableAppend = recomputedAppendStep?.id === savedStep.id && recomputedAppendStep.operation?.kind === 'COMBINE' &&
      recomputedAppendStep.operation.combine?.kind === 'APPEND' && recomputedInputs.ok;
    assertRows('dependent DiagnosticReport APPEND recomputes exact row multiplicity after COUNT_DISTINCT edit',
      await readGrid('saved'), ['Status', 'Row count'], editedAppendRows);
    const recomputeProposal = await assertCurrentDraftAppend(targetOutputId, groupedSources.map(source => source.outputId),
      editedAppendRows, recomputeFromIndex);
    const recomputeProof = currentPreview && stableAppend && recomputeProposal.step.id === savedStep.id &&
      recomputeProposal.body.expectedDraftVersion === afterSourceEdit.draftVersion &&
      recomputeProposal.body.expectedDraftDigest === afterSourceEdit.draftDigest && scopeStable && selectionStable;
    requireCheck('persistence', 'COUNT_DISTINCT edit recompiles dependent APPEND with stable inputs and exact current-draft receipt',
      recomputeProof, { outputId: targetOutputId, appendStepId: recomputedAppendStep?.id,
        expectedAppendStepId: savedStep.id, currentPreview: recomputedPreview, stableAppend,
        inputEvidence: recomputedInputs, requestId: recomputeProposal.event.requestId,
        requestDraftVersion: recomputeProposal.body.expectedDraftVersion,
        requestDraftDigest: recomputeProposal.body.expectedDraftDigest,
        currentDraftVersion: afterSourceEdit.draftVersion, currentDraftDigest: afterSourceEdit.draftDigest,
        scopeStable, selectionStable });
    assert(recomputeProof, 'Dependent APPEND must recompile against the edited GROUP and preserve current-draft input identity');
    await reload(targetOutputId, editedAppendRows.length, 'Reload recomputed DiagnosticReport APPEND after GROUP edit');
    assertRows('recomputed DiagnosticReport APPEND duplicate rows survive reload', await readGrid('saved'),
      ['Status', 'Row count'], editedAppendRows);
    const afterRecomputeReload = await readBuilder();
    checkScope(afterRecomputeReload);
    const finalAppendDoc = documentByOutput(afterRecomputeReload, targetOutputId);
    const finalAppendStep = finalAppendDoc.construction?.steps?.find(step => step.id === savedStep.id);
    const reloadInputEvidence = currentDraftSourceEvidence({ inputs: finalAppendStep?.inputs,
      expectedOutputIDs: groupedSources.map(source => source.outputId), sourceDocuments: afterRecomputeReload.workspace.documents });
    const recomputeSurvivedReload = finalAppendStep?.operation?.combine?.kind === 'APPEND' && reloadInputEvidence.ok &&
      afterRecomputeReload.draftVersion === afterSourceEdit.draftVersion &&
      afterRecomputeReload.draftDigest === afterSourceEdit.draftDigest;
    requireCheck('persistence', 'recomputed DiagnosticReport APPEND rows and current-draft inputs survive reload',
      recomputeSurvivedReload, { targetOutputId, appendStepId: finalAppendStep?.id,
        rows: editedAppendRows, inputEvidence: reloadInputEvidence,
        draftVersion: afterRecomputeReload.draftVersion, expectedDraftVersion: afterSourceEdit.draftVersion,
        draftDigest: afterRecomputeReload.draftDigest, expectedDraftDigest: afterSourceEdit.draftDigest });
    assert(recomputeSurvivedReload);
    let finalAppendRows = editedAppendRows;

    const removeAppend = async applyRemoval => {
      await selectTable(targetOutputId, finalAppendRows.length, 'Select saved DiagnosticReport APPEND for removal');
      const before = await readBuilder();
      const saved = documentByOutput(before, targetOutputId);
      const lastStep = saved.construction?.steps?.at(-1);
      assert(lastStep?.id === savedStep.id, 'Saved APPEND step identity must remain stable before removal');
      await click('Select saved DiagnosticReport APPEND step', page.getByTestId(`construction-history-step-${lastStep.id}`));
      const removeButton = page.locator(`[data-testid="construction-remove-step-${lastStep.id}"]:not(:disabled)`);
      await waitSelector(`[data-testid="construction-remove-step-${lastStep.id}"]:not(:disabled)`);
      await click('Propose DiagnosticReport APPEND removal', removeButton,
        async () => waitProposal(targetOutputId, 0));
      const proposal = await page.locator('[data-testid="construction-proposal-preview"][data-preview-status="ready"]')
        .evaluate(element => ({ outputId: element.dataset.previewOutputId, status: element.dataset.previewStatus }));
      assert.equal(proposal.outputId, targetOutputId);
      if (!applyRemoval) {
        await click('Cancel DiagnosticReport APPEND removal', page.getByTestId('construction-cancel-proposal'),
          async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&Boolean(document.querySelector('[data-testid="construction-history"]'))`));
        await reload(targetOutputId, finalAppendRows.length, 'Reload APPEND after canceling removal');
        const after = await readBuilder();
        const unchanged = after.draftVersion === before.draftVersion && after.draftDigest === before.draftDigest &&
          isDeepStrictEqual(after.workspace.documents, before.workspace.documents);
        requireCheck('persistence', 'Cancel removal preserves exact DiagnosticReport APPEND workspace after reload', unchanged,
          { beforeVersion: before.draftVersion, afterVersion: after.draftVersion,
            beforeDigest: before.draftDigest, afterDigest: after.draftDigest, stepId: lastStep.id });
        assert(unchanged);
        assertRows('canceled removal preserves duplicate DiagnosticReport APPEND rows', await readGrid('saved'),
          ['Status', 'Row count'], finalAppendRows);
        return;
      }
      await click('Apply DiagnosticReport APPEND removal', page.getByTestId('construction-apply-proposal'),
        async () => waitEmptyTarget(targetOutputId));
      await reload(targetOutputId, undefined, 'Reload restored empty DiagnosticReport APPEND target');
      const after = await readBuilder();
      checkScope(after);
      const restored = documentByOutput(after, targetOutputId);
      const empty = restored.rootResourceType === 'DiagnosticReport' && restored.columns.length === 0 &&
        (restored.construction?.steps?.length ?? 0) === 0;
      const sourcesPreserved = groupedSources.every(source => {
        const current = documentByOutput(after, source.outputId);
        return current.population?.selectionRevisionId === source.selection.id &&
          current.construction?.steps?.some(step => step.id === source.group.id && step.operation?.kind === 'GROUP');
      });
      requireCheck('persistence', 'applying APPEND removal restores the rooted empty target and preserves DiagnosticReport GROUP sources',
        empty && sourcesPreserved, { targetOutputId, empty, sourcesPreserved,
          sourceOutputIDs: groupedSources.map(source => source.outputId) });
      assert(empty && sourcesPreserved);
    };
    await removeAppend(false);
    await removeAppend(true);

    const selectedIDs = Object.values(oracle.sources).flat().map(row => row._id);
    const rereadRows = runAQL(arangoContainer, cdaDiagnosticReportAppendRereadQuery({
      project, generation, documentIDs: selectedIDs,
    }), 'Final exact selected DiagnosticReport raw reread');
    const reread = assertCdaDiagnosticReportAppendReread(rereadRows,
      Object.values(oracle.exactExpected).flat(), { project, generation });
    requireCheck('correctness', 'exact selected DiagnosticReport status witnesses reread unchanged after APPEND lifecycle', reread.exact, reread);
    const allInputs = (await readBuilder()).workspace.documents.flatMap(document =>
      document.construction?.steps?.flatMap(step => step.inputs ?? []) ?? []);
    const noPinnedOrPublished = publishRequests === 0 && allInputs.every(input => input.kind !== 'TABLE_REVISION');
    requireCheck('correctness', 'DiagnosticReport current-draft APPEND uses no pinned table revisions or Publish', noPinnedOrPublished,
      { publishRequests, inputKinds: allInputs.map(input => input.kind) });
    assert(noPinnedOrPublished);
    report.diagnosticReportAppend = {
      project, generation, explorer, status: oracle.status,
      sources: groupedSources.map(source => ({ outputId: source.outputId, selectionId: source.selection.id,
        memberCount: source.selection.memberCount, groupStepId: source.group.id })),
      targetOutputId, appendStepId: savedStep.id, initialRows: oracle.append.rows, rows: finalAppendRows,
      editedSourceGroupStepId: firstSource.group.id, editedAggregate: 'COUNT_DISTINCT', reread,
      noPublish: publishRequests === 0, noTableRevision: noPinnedOrPublished,
    };
  } finally {
    page.removeListener('request', onRequest);
    await requestCapture?.flush();
  }
}
