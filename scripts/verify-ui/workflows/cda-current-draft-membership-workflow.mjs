import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  membershipGroupCapabilityKeyEvidence,
  membershipOutputNullabilityEvidence,
  nativeCombineTargetBindingEvidence,
  rootedEmptyTargetAppliedExpression,
  rootedEmptyTargetRestorationEvidence,
} from '../helpers/builder-combine-helpers.mjs';
import {
  builderDraftStateEvidence,
  canceledDraftEvidence,
  constructionCandidateWireEquivalent,
  currentDraftSourceEvidence,
  workspaceOutputOption,
} from '../helpers/builder-combine-draft-helpers.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { proposalPreviewReadinessExpression, readProposalPreviewState } from '../helpers/proposal-preview-readiness.mjs';
import { cdaMembershipObservationQuery, prepareCdaMembershipOracle } from '../helpers/cda-current-draft-membership-oracle.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';

const ACTION_CHECK = 'all native CDA Membership lifecycle actions complete within five seconds';
const MAX_ACTION_MS = 5_000;
const TABLE_SELECTOR = '[data-testid="preview-table-scroll"] [role="table"]';
const normalizeText = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const encoded = value => encodeURIComponent(value);
const apiRoot = (project, explorer) => `/api/v1/projects/${encoded(project)}/explorers/${encoded(explorer)}`;
const sourceRef = ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
const sortedRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const queryString = value => JSON.stringify(value);

function runAQL(arangoContainer, query, description) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const invocation = buildArangoShellInvocation({ container: arangoContainer, script: javascript, database: 'loom_dev' });
  const result = spawnSync(invocation.command, invocation.args,
    { encoding: 'utf8', timeout: 30_000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, `${description} failed: ${normalizeText(result.stderr || result.stdout).slice(0, 1800)}`);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `${description} returned no JSON array: ${normalizeText(result.stdout).slice(0, 800)}`);
  const rows = JSON.parse(result.stdout.slice(jsonStart));
  assert(Array.isArray(rows), `${description} did not return an array`);
  return rows;
}

function boundedRawObservationScan(arangoContainer, project, generation) {
  const query = cdaMembershipObservationQuery({ project, generation });
  const rows = runAQL(arangoContainer, query, 'Bounded raw CDA Observation Membership scan');
  const oracle = prepareCdaMembershipOracle(rows, { project, generation });
  const selectedRawIDs = Object.values(oracle.selected).map(row => row._id);
  const rereadQuery = `LET selected = ${JSON.stringify(selectedRawIDs)} FOR r IN Observation FILTER r._id IN selected RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType}`;
  const selectedRows = runAQL(arangoContainer, rereadQuery, 'Exact selected CDA Observation reread');
  assert.equal(selectedRows.length, 3, 'Exact selected raw reread must resolve all three independent Observation IDs');
  const byRawID = new Map(selectedRows.map(row => [row._id, row]));
  for (const expected of Object.values(oracle.selected)) {
    const actual = byRawID.get(expected._id);
    assert(actual, 'Exact selected raw reread omitted a chosen Observation');
    assert.deepEqual([actual.id, actual.project, actual.generation, actual.resourceType],
      [expected.id, project, generation, 'Observation'], 'Exact selected raw reread changed FHIR identity or scope');
  }
  assert.equal(new Set(selectedRows.map(row => row._id)).size, 3);
  return { query, rows, oracle, selectedRows };
}

export function exactRootedMembershipRemovalPreview({ requestExact, response, proposalPreview, rawRootScopeExact, outputId }) {
  const preview = response?.preview;
  const rows = Array.isArray(preview?.rows) ? preview.rows : [];
  const rowIDs = rows.map(row => row?.__loom_row_id);
  return Boolean(requestExact && response?.proposalId === preview?.receiptId &&
    preview?.outputId === outputId && preview?.receiptId === response?.proposalId &&
    Array.isArray(preview?.columns) && preview.columns.length === 0 && preview?.rowCount === 25 && preview?.sampled === true &&
    rows.length === 25 && rows.every(row => Object.keys(row ?? {}).length === 1 && Object.hasOwn(row, '__loom_row_id')) &&
    rowIDs.every(id => typeof id === 'string' && id.length > 0) && new Set(rowIDs).size === 25 && rawRootScopeExact &&
    proposalPreview?.proposalPanelCount === 1 && proposalPreview.proposalStatus === 'ready' &&
    proposalPreview.proposalId === response.proposalId && proposalPreview.resultSectionCount === 1 &&
    proposalPreview.resultStatus === 'ready' && proposalPreview.resultOutputId === outputId &&
    proposalPreview.resultReceiptId === response.proposalId && proposalPreview.resultProposalId === response.proposalId &&
    proposalPreview.proposalPreviewCount === 1 && proposalPreview.previewStatus === 'ready' &&
    proposalPreview.previewOutputId === outputId && proposalPreview.previewReceiptId === response.proposalId &&
    proposalPreview.statusText === 'This table has no visible columns.' &&
    proposalPreview.footerText === 'Showing 25 preview rows. Full-output coverage is unavailable before publication.' &&
    proposalPreview.tableCount === 0);
}

export async function cdaCurrentDraftMembershipWorkflow({ page, cda }) {
  const { request, report, target } = cda;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target);
  assert(project && generation && apiOrigin && uiOrigin, 'Owned CDA fixture must provide project, generation, API, and UI origins');
  assert.equal(project, target.fixtureProject, 'CDA Membership source must use the exact owned fixture project');
  assert.equal(generation, 'cda-fhir-v1', 'CDA Membership requires the pinned cda-fhir-v1 generation');

  const api = async (path, body) => {
    const url = `${apiOrigin}${path}`;
    const headers = { 'X-Request-ID': `cda-membership-${randomUUID()}` };
    const response = body === undefined
      ? await request.get(url, { headers, timeout: 30_000 })
      : await request.post(url, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${normalizeText(text).slice(0, 1200)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1800)}`);
    return value;
  };
  let explorer;
  let explorerBase;
  let initialScope;
  let capture;
  let activeSourceForSelection;
  const selectionSwitches = [];
  let publishRequests = 0;
  const applicationOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
  const onRequest = browserRequest => {
    try {
      const url = new URL(browserRequest.url());
      if (applicationOrigins.has(url.origin) && url.pathname.endsWith('/authoring/v2/publish')) publishRequests += 1;
    } catch { /* Non-URL requests cannot be publish operations. */ }
  };
  page.on('request', onRequest);

  const check = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const waitFunction = (predicate, timeout = MAX_ACTION_MS) => page.waitForFunction(predicate, undefined, { timeout: Math.min(MAX_ACTION_MS, timeout) });
  const waitSelector = (selector, timeout = MAX_ACTION_MS) => page.locator(selector).waitFor({ state: 'visible', timeout: Math.min(MAX_ACTION_MS, timeout) });
  const actionDurations = [];
  const action = async (label, locator, perform, after) => {
    const startedAt = Date.now();
    await cda.action(label, locator, perform, {
      timeout: MAX_ACTION_MS,
      budget: MAX_ACTION_MS,
      after,
      requiredCheck: ACTION_CHECK,
    });
    const elapsedMs = Date.now() - startedAt;
    const evidence = { label, elapsedMs, withinBudget: elapsedMs <= MAX_ACTION_MS };
    actionDurations.push(evidence);
    assert(evidence.withinBudget, `${label} exceeded the ${MAX_ACTION_MS}ms native CDA action budget`);
    return elapsedMs;
  };
  const readBuilder = () => api(`${explorerBase}/authoring/v2/builder`);
  const getDocument = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected exactly one Builder document for ${outputId}`);
    return matches[0];
  };
  const command = async (commands, state) => {
    assert(state?.catalog?.snapshotToken, 'CDA Membership command requires the exact current Builder snapshot');
    await api(`${explorerBase}/authoring/v2/commands`, {
      commandId: randomUUID(),
      semanticsVersion: state.workspace?.semanticsVersion ?? 10,
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest,
      commands,
    });
    const next = await readBuilder();
    assert.equal(next.catalog?.generation, generation, 'Builder command returned a different CDA generation');
    assert.equal(next.catalog?.snapshotToken, initialScope.snapshotToken, 'Builder command changed the CDA catalog snapshot');
    assert.equal(next.catalog?.authorizationScopeDigest, initialScope.authorizationScopeDigest, 'Builder command changed authorization scope');
    return next;
  };
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal' ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
    if (kind === 'proposal' && !proposal) return { ready: false, headers: [], rows: [] };
    const table = kind === 'proposal' ? proposal.querySelector('table') : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [] };
    const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = proposal
      ? [...table.querySelectorAll('thead th')].map(cell => clean(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => clean(cell.textContent));
    const rows = proposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => clean(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => clean(cell.innerText)));
    return { ready: true, headers, rows, rowCount: table.getAttribute('aria-rowcount') };
  }, { kind });
  const assertRows = (name, grid, headers, expectedRows) => {
    const exact = grid.ready && isDeepStrictEqual(grid.headers, headers) && isDeepStrictEqual(sortedRows(grid.rows), sortedRows(expectedRows));
    check('correctness', name, exact, { ready: grid.ready, headers: grid.headers, expectedHeaders: headers, rows: grid.rows, expectedRows });
    assert(exact, `${name}: native preview differs from independent raw Observation membership`);
    return grid;
  };
  const proposalReady = (outputId, rowCount) => proposalPreviewReadinessExpression(outputId, rowCount);
  const selectedOutputReady = outputId => `document.querySelector(${queryString(`[data-testid="construction-table-${outputId}"]`)})?.getAttribute('aria-current')==='page'`;
  const savedReady = (outputId, rowCount) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector(${queryString(TABLE_SELECTOR)});return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${queryString(outputId)}&&t&&t.getAttribute('aria-rowcount')===${queryString(String(rowCount + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
  const navigateBuilder = async outputId => {
    await cda.navigate(`${uiOrigin}/?project=${encoded(project)}&explorer=${encoded(explorer)}&mode=builder`);
    await waitSelector(`[data-testid="construction-table-${outputId}"]`);
  };
  const selectTable = async (outputId, rowCount, label, afterSelection) => {
    const locator = page.getByTestId(`construction-table-${outputId}`);
    if (await locator.getAttribute('aria-current') !== 'page') {
      await action(label, locator, targetLocator => targetLocator.click({ timeout: MAX_ACTION_MS }), async () => {
        await waitFunction(`document.querySelector(${queryString(`[data-testid="construction-table-${outputId}"]`)})?.getAttribute('aria-current')==='page'`);
        if (rowCount !== undefined) await waitFunction(savedReady(outputId, rowCount));
        if (afterSelection) await afterSelection();
      });
    } else if (rowCount !== undefined) {
      await waitFunction(savedReady(outputId, rowCount));
    }
  };
  const selectSourceTable = async (source, rowCount, label) => {
    const previous = activeSourceForSelection;
    if (!previous || previous.outputId === source.outputId) {
      await selectTable(source.outputId, rowCount, label);
      activeSourceForSelection = source;
      return;
    }
    assert.notEqual(previous.selection.id, source.selection.id,
      'Distinct Membership sources must retain distinct immutable selections');
    const previousPath = `${apiRoot(project, explorer)}/selections/${encoded(previous.selection.id)}`;
    const nextPath = `${apiRoot(project, explorer)}/selections/${encoded(source.selection.id)}`;
    const selectedOutputBeforeSwitch = await page.evaluate(() => {
      const selected = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
      return selected?.getAttribute('data-testid')?.slice('construction-table-'.length) ?? null;
    });
    assert.equal(selectedOutputBeforeSwitch, previous.outputId,
      'Only the active prior Membership source may be retired by a source switch');
    const fromIndex = report.nativeRequests.length;
    const cancellationStart = report.expectedCancellations?.length ?? 0;
    const transitionStartedAt = Date.now();
    const pendingPreviousReads = [...capture.byRequest.entries()].filter(([, entry]) =>
      entry.path === previousPath && entry.method === 'GET' && entry.startedAt <= transitionStartedAt &&
      entry.completedAt == null && !entry.failure);
    const responseOutcome = capture.waitFor(entry => entry.path === nextPath && entry.method === 'GET' &&
      entry.status === 200 && entry.completedAt, { fromIndex, timeoutMs: MAX_ACTION_MS })
      .then(entry => ({ entry }), error => ({ error }));
    const reason = `Selecting ${source.title} retires the previous Membership source selection read.`;
    let nextSelectionEvent;
    await cda.withExpectedCancellations({
      origin: uiOrigin,
      method: 'GET',
      paths: [previousPath],
      requestIdPrefixes: ['cda-request-'],
      reason,
      proof: {
        project, explorer, generation,
        previousOutputId: previous.outputId,
        previousSelectionRevisionId: previous.selection.id,
        nextOutputId: source.outputId,
        nextSelectionRevisionId: source.selection.id,
        transition: 'source-to-source switch retires the old attached-selection GET through the Builder selectionRevisionId AbortController cleanup',
      },
      actionLabel: label,
    }, async () => selectTable(source.outputId, rowCount, label, async () => {
      const outcome = await responseOutcome;
      if (outcome.error) throw new Error(`Exact next Membership selection read did not resolve during ${label}: ${normalizeText(outcome.error.message).slice(0, 800)}`);
      nextSelectionEvent = outcome.entry;
      const pageBody = capture.rawResponseBody(nextSelectionEvent);
      assert.deepEqual({
        id: pageBody?.revision?.id,
        project: pageBody?.revision?.project,
        generation: pageBody?.revision?.generation,
        resourceType: pageBody?.revision?.resourceType,
        scopeDigest: pageBody?.revision?.scopeDigest,
        membershipDigest: pageBody?.revision?.membershipDigest,
        memberCount: pageBody?.revision?.memberCount,
        complete: pageBody?.revision?.complete,
      }, {
        id: source.selection.id, project, generation, resourceType: 'Observation',
        scopeDigest: initialScope.authorizationScopeDigest,
        membershipDigest: source.selection.membershipDigest,
        memberCount: source.selection.memberCount, complete: true,
      }, 'The active source switch must load the exact project/generation/scope/membership revision');
    }));
    const transitionFinishedAt = Date.now();
    const cancellations = (report.expectedCancellations ?? []).slice(cancellationStart);
    assert(cancellations.length <= 1, 'A source-to-source Membership switch may retire at most one old selection GET');
    const previousFailures = [...capture.byRequest.entries()].filter(([, entry]) =>
      entry.path === previousPath && entry.method === 'GET' && entry.failure === 'net::ERR_ABORTED' &&
      entry.completedAt >= transitionStartedAt && entry.completedAt <= transitionFinishedAt);
    let retiredPreviousRead;
    if (cancellations.length === 1) {
      const [cancellation] = cancellations;
      assert.equal(pendingPreviousReads.length, 1, 'A retired source selection GET must be pending before its table switch');
      assert.equal(previousFailures.length, 1, 'Only one exact previous source selection GET may abort in this switch');
      retiredPreviousRead = previousFailures[0][1];
      assert.strictEqual(previousFailures[0][0], pendingPreviousReads[0][0], 'The abort must be the exact previous request pending before the switch');
      assert(retiredPreviousRead.startedAt < transitionStartedAt && retiredPreviousRead.completedAt >= transitionStartedAt);
      assert.deepEqual([cancellation.method, new URL(cancellation.url).origin, new URL(cancellation.url).pathname,
        cancellation.reason, cancellation.proof?.previousOutputId, cancellation.proof?.previousSelectionRevisionId,
        cancellation.proof?.nextOutputId, cancellation.proof?.nextSelectionRevisionId, cancellation.proof?.scopeAction], [
        'GET', new URL(uiOrigin).origin, previousPath, reason, previous.outputId, previous.selection.id,
        source.outputId, source.selection.id, label,
      ]);
      assert.equal(retiredPreviousRead.browserRequestId, cancellation.browserRequestId);
      const diagnostics = report.network.filter(entry => entry.kind === 'network' &&
        entry.browserRequestId === cancellation.browserRequestId);
      assert.equal(diagnostics.length, 1, 'The exact retired request must remain in the native network ledger');
      assert.deepEqual([diagnostics[0].url, diagnostics[0].errorText, diagnostics[0].triggerAction,
        diagnostics[0].expected, diagnostics[0].cancellationAction], [
        `${new URL(uiOrigin).origin}${previousPath}`, 'net::ERR_ABORTED', label, true, label,
      ]);
      const requestErrors = report.errors.filter(entry => entry.kind === 'network' &&
        (entry.browserRequestId === cancellation.browserRequestId || entry.playwrightRequestId === cancellation.playwrightRequestId));
      assert(requestErrors.length > 0 && requestErrors.every(entry => entry.expected === true),
        'The exact retired source GET must have no unclassified native error diagnostic');
    } else {
      assert.equal(previousFailures.length, 0, 'An unclassified previous-source cancellation cannot be hidden by the switch proof');
    }
    const grid = await readGrid('saved');
    const expectedHeaders = source.group ? [source.group.keyLabel, source.group.countLabel] : [source.idLabel];
    const expectedRows = source.group ? source.group.expectedRows : source.ids.map(id => [id]);
    assertRows(`${label} renders the exact next source rows`, grid, expectedHeaders, expectedRows);
    selectionSwitches.push({
      action: label,
      transition: { startedAt: transitionStartedAt, finishedAt: transitionFinishedAt, elapsedMs: transitionFinishedAt - transitionStartedAt },
      previous: { outputId: previous.outputId, selectionRevisionId: previous.selection.id },
      selectedOutputBeforeSwitch,
      next: { outputId: source.outputId, selectionRevisionId: source.selection.id },
      pendingPreviousReads: pendingPreviousReads.map(([, entry]) => ({ path: entry.path, startedAt: entry.startedAt })),
      retiredPreviousRead: retiredPreviousRead ? { browserRequestId: retiredPreviousRead.browserRequestId,
        path: retiredPreviousRead.path, startedAt: retiredPreviousRead.startedAt,
        completedAt: retiredPreviousRead.completedAt, failure: retiredPreviousRead.failure } : null,
      nextSelectionRead: { browserRequestId: nextSelectionEvent.browserRequestId, path: nextSelectionEvent.path,
        status: nextSelectionEvent.status, startedAt: nextSelectionEvent.startedAt,
        completedAt: nextSelectionEvent.completedAt, revision: capture.rawResponseBody(nextSelectionEvent)?.revision },
    });
    report.selectionSwitches = selectionSwitches;
    activeSourceForSelection = source;
  };
  const reloadAndSelect = async (outputId, rowCount, label) => {
    const locator = page.getByTestId(`construction-table-${outputId}`);
    await action(label, locator, async targetLocator => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: MAX_ACTION_MS });
      await waitSelector(`[data-testid="construction-table-${outputId}"]`);
      await targetLocator.click({ timeout: MAX_ACTION_MS });
    }, async () => {
      if (rowCount === undefined) await waitFunction(rootedEmptyTargetAppliedExpression(outputId));
      else await waitFunction(savedReady(outputId, rowCount));
    });
  };
  const assertScope = state => {
    assert.equal(state.catalog?.generation, generation);
    assert.equal(state.catalog?.snapshotToken, initialScope.snapshotToken);
    assert.equal(state.catalog?.authorizationScopeDigest, initialScope.authorizationScopeDigest);
  };
  const selectOptionByValue = async (selector, value, label, after) => {
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({ value: option.value, disabled: option.disabled })));
    const matches = options.filter(option => !option.disabled && option.value === value);
    assert.equal(matches.length, 1, `Expected one enabled exact value ${JSON.stringify(value)} in ${selector}: ${JSON.stringify(options)}`);
    const locator = page.locator(selector);
    await action(label, locator, targetLocator => targetLocator.selectOption(value, { timeout: MAX_ACTION_MS }),
      after ?? (async () => waitFunction(`document.querySelector(${queryString(selector)})?.value===${queryString(value)}`)));
  };
  const createSource = async (title, ids) => {
    const state = await readBuilder();
    assertScope(state);
    const roots = (state.catalog?.nodes ?? []).filter(node => node.resourceType === 'Observation' && node.rowRootEligible);
    assert.equal(roots.length, 1, 'CDA catalog must expose exactly one eligible Observation root');
    const root = roots[0];
    const candidates = (state.catalog?.candidates ?? []).filter(candidate => candidate.nodeId === root.nodeId && candidate.fieldPath === 'id');
    assert.equal(candidates.length, 1, 'CDA Observation root must expose exactly one ID VALUE candidate');
    const candidate = candidates[0];
    assert.equal(candidate.logicalType, 'string');
    assert.equal(candidate.repeated, false);
    const beforeOutputIDs = new Set((state.workspace?.documents ?? []).map(document => document.output.id));
    let next = await command([{ type: 'CREATE_TABLE', title, rootNodeId: root.nodeId }], state);
    const created = (next.workspace?.documents ?? []).filter(document => !beforeOutputIDs.has(document.output.id));
    assert.equal(created.length, 1, 'CREATE_TABLE must add exactly one raw-ID source output');
    const outputId = created[0].output.id;
    next = await command([{
      type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID',
    }], next);
    const refs = ids.map(id => ({ project, generation, resourceType: 'Observation', id }));
    const selection = await api(`${apiRoot(project, explorer)}/selections`, {
      snapshotToken: next.catalog.snapshotToken,
      idempotencyKey: `cda-membership-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs } },
    });
    assert.deepEqual([selection.project, selection.generation, selection.resourceType, selection.scopeDigest, selection.memberCount],
      [project, generation, 'Observation', initialScope.authorizationScopeDigest, refs.length]);
    const pageBody = await api(`${apiRoot(project, explorer)}/selections/${encoded(selection.id)}?limit=100`);
    const revision = pageBody.revision;
    assert.deepEqual({ id: revision?.id, project: revision?.project, generation: revision?.generation,
      resourceType: revision?.resourceType, scopeDigest: revision?.scopeDigest, membershipDigest: revision?.membershipDigest,
      memberCount: revision?.memberCount, complete: revision?.complete }, {
      id: selection.id, project, generation, resourceType: 'Observation', scopeDigest: initialScope.authorizationScopeDigest,
      membershipDigest: selection.membershipDigest, memberCount: refs.length, complete: true,
    }, 'Selection readback must prove exact project/generation/authorization scope and complete membership');
    assert.equal((pageBody.members ?? []).length, refs.length);
    const actualRefs = pageBody.members.map(member => sourceRef(member.ref)).sort();
    assert.deepEqual(actualRefs, refs.map(sourceRef).sort(), 'Immutable source selection must equal the raw CDA membership IDs');
    assert.equal(new Set(pageBody.members.map(member => member.memberKey)).size, refs.length);
    const routes = await api(`${explorerBase}/authoring/v2/population-routes`, {
      snapshotToken: next.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
    });
    const direct = (routes.choices ?? []).find(choice => (choice.route?.length ?? -1) === 0);
    assert(direct, 'Exact Observation selection must expose its direct root population route');
    next = await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }], next);
    const document = getDocument(next, outputId);
    const idColumn = document.columns.filter(column => column.source?.kind === 'field' && column.source.field?.path === 'id' &&
      column.source.field?.projectionMode === 'VALUE' && column.occurrenceId === 'base');
    assert.equal(idColumn.length, 1, 'Raw-ID root source must reuse exactly one stable ID VALUE projection');
    assert.equal(document.rootResourceType, 'Observation');
    assert.equal(document.population?.selectionRevisionId, selection.id);
    assert.equal(document.population?.route?.length ?? 0, 0);
    assert.equal(document.columns.length, 1);
    check('correctness', `${title} binds its exact immutable raw Observation IDs and stable root ID VALUE projection`, true, {
      outputId, selectionRevisionId: selection.id, refs: actualRefs, idColumnId: idColumn[0].columnId,
      snapshotToken: next.catalog.snapshotToken, authorizationScopeDigest: initialScope.authorizationScopeDigest,
    });
    return { title, outputId, selection, ids, idColumnId: idColumn[0].columnId, idLabel: idColumn[0].label,
      rootNodeId: root.nodeId, baselineDocument: structuredClone(document), group: null };
  };
  const groupSource = async source => {
    await navigateBuilder(source.outputId);
    await selectSourceTable(source, source.ids.length, `Select exact ${source.title} raw ID selection`);
    const sourceGrid = await readGrid('saved');
    assertRows(`${source.title} preview matches exact selected IDs`, sourceGrid,
      [source.idLabel], source.ids.map(id => [id]));
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await action(`Open ${source.title} row settings`, settings, locator => locator.click({ timeout: MAX_ACTION_MS }),
      async () => waitSelector('[data-testid="construction-action-group-rows"]'));
    await waitFunction("document.querySelector('[data-testid=construction-action-group-rows]')?.disabled===false");
    const groupAction = page.getByTestId('construction-action-group-rows');
    await action(`Open ${source.title} native GROUP editor`, groupAction, locator => locator.click({ timeout: MAX_ACTION_MS }),
      async () => waitSelector('select[aria-label="Summary 1"]'));
    const summary = page.locator('select[aria-label="Summary 1"]');
    assert.equal(await summary.inputValue(), 'COUNT_ROWS', 'Native ID GROUP must default to COUNT_ROWS');
    const groupLabel = `Group by ${source.idLabel}`;
    const groupCheckbox = page.locator(`input[type="checkbox"][aria-label=${queryString(groupLabel)}]`);
    await waitFunction(`Boolean(document.querySelector(${queryString(`input[type="checkbox"][aria-label="${groupLabel}"]`)}))`);
    assert.equal(await groupCheckbox.count(), 1, 'Native GROUP editor must expose the exact source root ID column');
    assert.equal(await groupCheckbox.isChecked(), false, 'New source GROUP must not have an implicit group key');
    const expectedGroups = source.ids.map(id => [id, '1']);
    const groupBase = await readBuilder();
    assertScope(groupBase);
    const fromIndex = report.nativeRequests.length;
    const previewExpression = proposalPreviewReadinessExpression(source.outputId, expectedGroups.length);
    await action(`Group ${source.title} by its root ID with COUNT_ROWS`, groupCheckbox,
      locator => locator.check({ timeout: MAX_ACTION_MS }), async () => waitFunction(previewExpression));
    const proposal = await readGrid('proposal');
    const keyIndex = proposal.headers.findIndex(header => header === source.idLabel);
    const countIndexes = proposal.headers.flatMap((header, index) => /row count/i.test(header) ? [index] : []);
    assert.equal(keyIndex, 0, 'Native GROUP preview must put the exact root ID key first');
    assert.equal(countIndexes.length, 1, 'Native COUNT_ROWS preview must expose exactly one row-count column');
    const groupedGrid = {
      ...proposal,
      headers: [proposal.headers[keyIndex], proposal.headers[countIndexes[0]]],
      rows: proposal.rows.map(row => [row[keyIndex], String(Number(row[countIndexes[0]]))]),
    };
    assert.equal(proposal.headers.length, 2, 'Native COUNT_ROWS preview must expose only its selected key and aggregate');
    assertRows(`${source.title} COUNT_ROWS GROUP preview matches the exact raw IDs`, groupedGrid,
      [source.idLabel, proposal.headers[countIndexes[0]]], expectedGroups);
    {
      const event = await capture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/construction-proposals` &&
        entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.body?.outputId === source.outputId,
      { fromIndex, timeoutMs: MAX_ACTION_MS });
      const body = capture.rawRequestBody(event);
      const response = capture.rawResponseBody(event);
      const step = body?.candidateConstruction?.steps?.at(-1);
      const group = step?.operation?.group;
      const checks = {
        ownedRequest: event.origin === new URL(uiOrigin).origin && event.path === `${explorerBase}/authoring/v2/construction-proposals`,
        exactScope: body?.snapshotToken === groupBase.catalog?.snapshotToken && response?.snapshotToken === groupBase.catalog?.snapshotToken,
        draftCAS: body?.expectedDraftVersion === groupBase.draftVersion && body?.expectedDraftDigest === groupBase.draftDigest &&
          response?.draftVersion === groupBase.draftVersion && response?.draftDigest === groupBase.draftDigest,
        exactOutput: body?.outputId === source.outputId && response?.outputId === source.outputId,
        exactIDBinding: step?.operation?.kind === 'GROUP' && group?.keys?.length === 1 &&
          group.keys[0]?.inputColumnId === source.idColumnId,
        countRows: group?.aggregates?.length === 1 && group.aggregates[0]?.operation === 'COUNT_ROWS',
        exactCandidateResponse: constructionCandidateWireEquivalent(body?.candidateConstruction, response?.candidateConstruction),
        ready: response?.previewStatus === 'READY' && response?.preview?.outputId === source.outputId,
      };
      const ok = Object.values(checks).every(Boolean);
      check('correctness', `${source.title} GROUP proposal binds the exact root ID and draft CAS`, ok, { checks, requestId: event.requestId, stepId: step?.id });
      assert(ok, `${source.title} native GROUP proposal lost the exact ID binding or draft CAS`);
    }
    const apply = page.getByTestId('construction-apply-proposal');
    await action(`Apply ${source.title} current-draft COUNT_ROWS GROUP`, apply,
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(savedReady(source.outputId, expectedGroups.length)));
    let builder = await readBuilder();
    assertScope(builder);
    let document = getDocument(builder, source.outputId);
    let step = document.construction?.steps?.find(candidate => candidate.operation?.kind === 'GROUP');
    const group = step?.operation?.group;
    const key = group?.keys?.[0];
    const aggregate = group?.aggregates?.[0];
    const keyOutput = step?.outputs?.find(output => output.id === key?.outputColumnId);
    const countOutput = step?.outputs?.find(output => output.id === aggregate?.outputColumnId);
    const exactSaved = document.construction?.steps?.length === 1 && step?.id && group?.keys?.length === 1 &&
      key?.inputColumnId === source.idColumnId && aggregate?.operation === 'COUNT_ROWS' && Boolean(keyOutput && countOutput);
    check('persistence', `${source.title} GROUP persists the stable root ID binding and COUNT_ROWS`, exactSaved, {
      outputId: source.outputId, stepId: step?.id, inputColumnId: key?.inputColumnId,
      expectedInputColumnId: source.idColumnId, keyOutputId: keyOutput?.id, countOutputId: countOutput?.id,
    });
    assert(exactSaved, `${source.title} saved GROUP changed its exact source ID binding`);
    const groupStepId = step.id;
    await reloadAndSelect(source.outputId, expectedGroups.length, `Reload ${source.title} saved GROUP preview`);
    const grid = await readGrid('saved');
    assertRows(`${source.title} GROUP values survive reload with exact keys and counts`, grid,
      [keyOutput.label, countOutput.label], expectedGroups);
    builder = await readBuilder();
    assertScope(builder);
    document = getDocument(builder, source.outputId);
    step = document.construction?.steps?.find(candidate => candidate.operation?.kind === 'GROUP');
    assert.equal(document.population?.selectionRevisionId, source.selection.id, 'Saved GROUP must retain the exact immutable source selection');
    assert.equal(step?.id, groupStepId, 'Reloaded source GROUP must retain its exact saved step identity');
    source.group = {
      stepId: step.id,
      keyInputColumnId: key.inputColumnId,
      keyColumnId: keyOutput.id,
      keyLabel: keyOutput.label,
      countColumnId: countOutput.id,
      countLabel: countOutput.label,
      expectedRows: expectedGroups,
    };
    source.baselineDocument = structuredClone(document);
    check('persistence', `${source.title} grouped rows and exact selection survive reload`,
      isDeepStrictEqual(sortedRows(grid.rows), sortedRows(expectedGroups)) && document.population?.selectionRevisionId === source.selection.id, {
        outputId: source.outputId, selectionRevisionId: document.population?.selectionRevisionId,
        groupStepId: source.group.stepId, groupKeyColumnId: source.group.keyColumnId,
      });
  };
  const selectInputByOutput = async (selector, outputId, label) => {
    const value = workspaceOutputOption(outputId);
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
      value: option.value, group: option.parentElement?.label ?? '', disabled: option.disabled,
    })));
    const matches = options.filter(option => option.value === value && option.group === 'Current draft tables' && !option.disabled);
    assert.equal(matches.length, 1, `Native Membership input must offer exact current-draft output ${outputId}: ${JSON.stringify(options)}`);
    await selectOptionByValue(selector, value, label);
  };
  const inspectMembershipCapabilities = async (fromIndex, builder, targetOutputId, sources) => {
    const path = `${explorerBase}/authoring/v2/construction-capabilities`;
    const event = await capture.waitFor(entry => entry.path === path && entry.method === 'POST' && entry.status === 200 &&
      entry.completedAt && entry.body?.outputId === targetOutputId && entry.response?.outputId === targetOutputId,
    { fromIndex, timeoutMs: MAX_ACTION_MS });
    const body = capture.rawRequestBody(event);
    const response = capture.rawResponseBody(event);
    const inputs = response?.workspaceInputs ?? [];
    const perSource = sources.map(source => {
      const matchingInputs = inputs.filter(input => input.outputId === source.outputId);
      const input = matchingInputs[0];
      const columns = input?.columns ?? [];
      const keyEvidence = membershipGroupCapabilityKeyEvidence(columns, source.group.keyColumnId);
      const key = keyEvidence.column;
      return {
        outputId: source.outputId,
        inputCount: matchingInputs.length,
        keyMatches: keyEvidence.matchingColumns.length,
        keyId: key?.id ?? null,
        keyLabel: key?.label ?? null,
        logicalType: key?.logicalType ?? null,
        cardinality: key?.cardinality ?? null,
        nullable: key?.nullable ?? null,
        joinCompatibilityKey: key?.joinCompatibilityKey ?? null,
        keyEvidence,
        exact: matchingInputs.length === 1 && keyEvidence.ok,
      };
    });
    const checks = {
      uiOwnedPath: event.origin === new URL(uiOrigin).origin && event.path === path,
      requestCAS: body?.snapshotToken === builder.catalog?.snapshotToken &&
        body?.expectedDraftVersion === builder.draftVersion && body?.expectedDraftDigest === builder.draftDigest,
      responseCAS: response?.snapshotToken === builder.catalog?.snapshotToken &&
        response?.draftVersion === builder.draftVersion && response?.draftDigest === builder.draftDigest,
      outputIdentity: body?.outputId === targetOutputId && response?.outputId === targetOutputId,
      bothNullableStringIDKeysAvailable: perSource.length === 2 && perSource.every(item => item.exact && item.nullable === true),
    };
    return { ok: Object.values(checks).every(Boolean), checks, perSource, requestId: event.requestId };
  };
  const configureMembership = async (sources, targetOutputId, expectedIDs, mode, baseBuilder, phase, expectedStepId) => {
    await waitSelector('select[aria-label="Input table 1"]');
    await waitSelector('select[aria-label="Input table 2"]');
    await selectInputByOutput('select[aria-label="Input table 1"]', sources[0].outputId, 'Bind left exact current-draft GROUP source');
    await selectInputByOutput('select[aria-label="Input table 2"]', sources[1].outputId, 'Bind right exact current-draft GROUP source');
    await selectOptionByValue('select[aria-label="Matching pair 1 first field"]', sources[0].group.keyColumnId,
      'Choose left native GROUP ID key');
    await selectOptionByValue('select[aria-label="Matching pair 1 second field"]', sources[1].group.keyColumnId,
      'Choose right native GROUP ID key');
    const modeSelector = 'select[aria-label="Which rows should stay?"]';
    await selectOptionByValue(modeSelector, mode, `Choose native MEMBERSHIP ${mode} policy`);
    const addOutput = page.getByRole('button', { name: 'Add output field', exact: true });
    await action(`Add native MEMBERSHIP ${mode} output field`, addOutput,
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitSelector('input[aria-label="Output field 1 name"]'));
    const name = page.locator('input[aria-label="Output field 1 name"]');
    await action(`Name native MEMBERSHIP ${mode} output`, name,
      locator => locator.fill('observation_id', { timeout: MAX_ACTION_MS }), async () => waitFunction(`document.querySelector('input[aria-label="Output field 1 name"]')?.value==='observation_id'`));
    const label = page.locator('input[aria-label="Output field 1 label"]');
    await action(`Label native MEMBERSHIP ${mode} output`, label,
      locator => locator.fill('Observation ID', { timeout: MAX_ACTION_MS }), async () => waitFunction(`document.querySelector('input[aria-label="Output field 1 label"]')?.value==='Observation ID'`));
    const projectionSelector = 'select[aria-label="Output field 1 source field in input 1"]';
    const projectionOptions = await page.locator(projectionSelector).evaluate(select => [...select.options].map(option => ({ value: option.value, disabled: option.disabled })));
    const projectionMatches = projectionOptions.filter(option => !option.disabled && option.value === sources[0].group.keyColumnId);
    assert.equal(projectionMatches.length, 1, `Membership must project the exact left grouped ID only: ${JSON.stringify(projectionOptions)}`);
    const fromIndex = report.nativeRequests.length;
    const expectedRows = expectedIDs.map(id => [id]);
    await action(`Complete native ${mode} Membership mapping and render exact preview`, page.locator(projectionSelector),
      locator => locator.selectOption(projectionMatches[0].value, { timeout: MAX_ACTION_MS }), async () => waitFunction(proposalReady(targetOutputId, expectedRows.length)));
    const grid = await readGrid('proposal');
    assertRows(`${phase} Membership preview equals the exact independent raw ID oracle`, grid, ['Observation ID'], expectedRows);
    const proposalPath = `${explorerBase}/authoring/v2/construction-proposals`;
    const event = await capture.waitFor(entry => entry.path === proposalPath && entry.method === 'POST' && entry.status === 200 &&
      entry.completedAt && entry.body?.outputId === targetOutputId,
    { fromIndex, timeoutMs: MAX_ACTION_MS });
    const body = capture.rawRequestBody(event);
    const response = capture.rawResponseBody(event);
    const step = body?.candidateConstruction?.steps?.at(-1);
    const operation = step?.operation?.combine;
    const expectedInputs = sources.map(source => source.outputId);
    const actualInputs = (step?.inputs ?? []).map(input => input.outputId);
    const output = step?.outputs?.[0];
    const expectedKey = { leftColumnId: sources[0].group.keyColumnId, rightColumnId: sources[1].group.keyColumnId };
    const checks = {
      ownedRequest: event.origin === new URL(uiOrigin).origin && event.path === proposalPath,
      exactOutput: body?.outputId === targetOutputId && response?.outputId === targetOutputId,
      exactSnapshot: body?.snapshotToken === baseBuilder.catalog?.snapshotToken && response?.snapshotToken === body.snapshotToken,
      exactDraftCAS: body?.expectedDraftVersion === baseBuilder.draftVersion && body?.expectedDraftDigest === baseBuilder.draftDigest &&
        response?.draftVersion === body.expectedDraftVersion && response?.draftDigest === body.expectedDraftDigest,
      exactCurrentDraftInputs: isDeepStrictEqual(actualInputs, expectedInputs) &&
        (step?.inputs ?? []).every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId) &&
        currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: expectedInputs,
          sourceDocuments: baseBuilder.workspace?.documents }).ok,
      exactMembershipKindAndMode: step?.operation?.kind === 'COMBINE' && operation?.kind === 'MEMBERSHIP' && operation.membershipMode === mode,
      exactNullableIDKeyPair: operation?.keys?.length === 1 && isDeepStrictEqual(operation.keys[0], expectedKey),
      leftOnlyProjection: operation?.projections?.length === 1 && operation.projections[0]?.inputIndex === 0 &&
        operation.projections[0]?.inputColumnId === sources[0].group.keyColumnId && operation.projections[0]?.outputColumnId === output?.id,
      stepIdentityRetained: !expectedStepId || step?.id === expectedStepId,
      responseMatchesCandidate: constructionCandidateWireEquivalent(body?.candidateConstruction, response?.candidateConstruction),
      previewReady: response?.previewStatus === 'READY' && response?.preview?.outputId === targetOutputId &&
        response?.proposalId === await page.locator('[data-testid="construction-proposal-panel"]').getAttribute('data-proposal-id') &&
        response?.preview?.receiptId === await page.locator('[data-testid="construction-proposal-preview"]').getAttribute('data-preview-receipt-id'),
      status200: event.status === 200,
    };
    const ok = Object.values(checks).every(Boolean);
    check('correctness', `${phase} Membership proposal binds exact nullable GROUP ID keys and current draft CAS`, ok, {
      checks, requestId: event.requestId, outputId: targetOutputId, inputOutputIDs: actualInputs,
      keyIDs: operation?.keys ?? [], mode: operation?.membershipMode,
      projection: operation?.projections ?? [], stepId: step?.id,
    });
    assert(ok, `${mode} Membership proposal changed its exact current-draft bindings: ${JSON.stringify(checks)}`);
    return { event, body, response, step, operation, output, expectedRows };
  };
  const applyProposal = async (label, targetOutputId, rowCount) => {
    await action(label, page.getByTestId('construction-apply-proposal'),
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(savedReady(targetOutputId, rowCount)));
  };
  const openSavedStep = async (targetOutputId, stepId, rowCount) => {
    await selectTable(targetOutputId, rowCount, 'Select saved Membership output before editing');
    const history = page.getByTestId(`construction-history-step-${stepId}`);
    await action('Select exact saved Membership history step', history,
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(`Boolean(document.querySelector(${queryString(`[data-testid="construction-edit-step-${stepId}"]:not(:disabled)`)}))`));
    const edit = page.getByTestId(`construction-edit-step-${stepId}`);
    await action('Open saved native Membership editor', edit,
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitSelector('[data-testid="construction-combine-editor"]'));
  };
  const proposeRemove = async (targetOutputId, stepId, phase, baseBuilder) => {
    await selectTable(targetOutputId, undefined, 'Select Membership output before removal');
    const history = page.getByTestId(`construction-history-step-${stepId}`);
    await action('Select exact saved Membership step before removal', history,
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(`Boolean(document.querySelector(${queryString(`[data-testid="construction-remove-step-${stepId}"]:not(:disabled)`)}))`));
    const fromIndex = report.nativeRequests.length;
    await action(`${phase}: propose removal of the exact saved Membership step`, page.getByTestId(`construction-remove-step-${stepId}`),
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(proposalReady(targetOutputId, 0)));
    const path = `${explorerBase}/authoring/v2/construction-proposals`;
    const event = await capture.waitFor(entry => entry.path === path && entry.method === 'POST' && entry.status === 200 &&
      entry.completedAt && entry.body?.outputId === targetOutputId && Array.isArray(entry.body?.removeStepIds),
    { fromIndex, timeoutMs: MAX_ACTION_MS });
    const body = capture.rawRequestBody(event);
    const response = capture.rawResponseBody(event);
    const preview = response?.preview;
    const rootSourceIDs = Array.isArray(preview?.rowSources) ? preview.rowSources.map(source => source?.id) : [];
    const validRootSources = rootSourceIDs.length === 25 && new Set(rootSourceIDs).size === 25 &&
      preview.rowSources.every(source => source?.kind === 'SINGLE' && source?.resourceType === 'Observation' &&
        typeof source?.id === 'string' && source.id.trim().length > 0);
    const rootRereadQuery = `LET ids = ${JSON.stringify(rootSourceIDs)} FOR r IN Observation ` +
      `FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)} ` +
      `AND r.payload.resourceType == "Observation" AND r.id IN ids SORT r._id LIMIT 25 ` +
      `RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType}`;
    const rootRereadStartedAt = Date.now();
    const rootReread = validRootSources
      ? runAQL(arangoContainer, rootRereadQuery, `${phase} removal preview raw root Observation reread`)
      : [];
    const rootRereadElapsedMs = Date.now() - rootRereadStartedAt;
    const rereadIDs = rootReread.map(row => row?.id);
    const rawRootScopeExact = validRootSources && rootReread.length === 25 && new Set(rereadIDs).size === 25 &&
      isDeepStrictEqual([...rereadIDs].sort(), [...rootSourceIDs].sort()) && rootReread.every(row =>
        row?.project === project && row?.generation === generation && row?.resourceType === 'Observation' &&
        typeof row?._id === 'string' && row._id.length > 0 && typeof row?.id === 'string' && row.id.trim().length > 0);
    const proposalPreview = await readProposalPreviewState(page, targetOutputId);
    const rows = Array.isArray(preview?.rows) ? preview.rows : [];
    const rowIDs = rows.map(row => row?.__loom_row_id);
    const expectedCandidate = { version: 1, steps: [] };
    const requestExact = body?.outputId === targetOutputId && isDeepStrictEqual(body.removeStepIds, [stepId]) &&
      event.origin === new URL(uiOrigin).origin && event.path === path && body.limit === 25 &&
      body.snapshotToken === baseBuilder.catalog?.snapshotToken && body.expectedDraftVersion === baseBuilder.draftVersion &&
      body.expectedDraftDigest === baseBuilder.draftDigest && isDeepStrictEqual(body.candidateConstruction, expectedCandidate) &&
      response?.outputId === targetOutputId && response?.snapshotToken === baseBuilder.catalog?.snapshotToken &&
      response?.draftVersion === baseBuilder.draftVersion && response?.draftDigest === baseBuilder.draftDigest &&
      response?.previewStatus === 'READY' && isDeepStrictEqual(response?.candidateConstruction, expectedCandidate) &&
      isDeepStrictEqual(response?.dependencyImpact?.affectedStepIds, []) &&
      isDeepStrictEqual(response?.dependencyImpact?.removedStepIds, [stepId]);
    check('correctness', `${phase} removal proposal targets only the exact saved Membership step and draft CAS`, requestExact, {
      requestExact, requestId: event.requestId, outputId: body?.outputId, removeStepIds: body?.removeStepIds, stepId,
      expectedDraftVersion: body?.expectedDraftVersion, responseDraftVersion: response?.draftVersion,
      candidateConstruction: response?.candidateConstruction, dependencyImpact: response?.dependencyImpact,
    });
    assert(requestExact, `${phase} Membership removal request changed the selected step or saved draft CAS`);
    const previewExact = exactRootedMembershipRemovalPreview({
      requestExact, response, proposalPreview, rawRootScopeExact, outputId: targetOutputId,
    });
    const checkName = `${phase} Membership removal proposal renders the rooted target with no visible columns`;
    check('correctness', checkName, previewExact, {
      previewExact, requestExact, requestId: event.requestId, outputId: body?.outputId, removeStepIds: body?.removeStepIds, stepId,
      previewStatus: response?.previewStatus, proposalId: response?.proposalId, previewOutputId: preview?.outputId,
      previewReceiptId: preview?.receiptId, previewColumns: preview?.columns, previewRowCount: preview?.rowCount,
      previewSampled: preview?.sampled, previewRows: rows.length, previewRowIDs: rowIDs,
      rootSourceCount: rootSourceIDs.length, rawRootRereadCount: rootReread.length, rawRootScopeExact,
      rawRootReread: rootReread, rootRereadElapsedMs, proposalPreview,
    });
    assert(previewExact, `${phase} Membership removal preview did not render the exact ready zero-column rooted target`);
    return event;
  };

  try {
    const raw = boundedRawObservationScan(arangoContainer, project, generation);
    const oracle = raw.oracle;
    report.target.membershipRawOracle = {
      source: 'raw Arango Observation payloads; bounded project/generation/type scan and exact three-record reread',
      project, generation, maxRows: 2_000, scannedRows: oracle.scannedCount,
      selectedCount: 3, leftCount: oracle.left.length, rightCount: oracle.right.length,
      leftIDs: oracle.leftIDs, rightIDs: oracle.rightIDs, includeIDs: oracle.includeIDs, excludeIDs: oracle.excludeIDs,
      exactRawRereadCount: raw.selectedRows.length,
    };
    check('correctness', 'bounded raw CDA oracle derives exact overlapping ID groups and independent INCLUDE/EXCLUDE rows',
      oracle.left.length === 2 && oracle.right.length === 2 && oracle.includeIDs.length === 1 && oracle.excludeIDs.length === 1 &&
        raw.selectedRows.length === 3,
      report.target.membershipRawOracle);

    const explorerName = `cda-membership-${randomUUID()}`;
    const explorerTitle = `CDA current-draft Membership ${randomUUID().slice(0, 8)}`;
    const createdExplorer = await api(`/api/v1/projects/${encoded(project)}/explorers`, { name: explorerName, title: explorerTitle });
    explorer = createdExplorer.explorerId ?? createdExplorer.id ?? createdExplorer.explorer?.id ?? explorerName;
    assert.equal(explorer, explorerName, 'Fresh CDA Explorer creation must preserve its exact unique ID');
    explorerBase = apiRoot(project, explorer);
    const explorerList = await api(`/api/v1/projects/${encoded(project)}/explorers`);
    const explorerItems = Array.isArray(explorerList) ? explorerList : explorerList.explorers ?? explorerList.value ?? [];
    const matchingExplorers = explorerItems.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
    assert.equal(matchingExplorers.length, 1, 'Fresh current-draft Membership Explorer must resolve exactly once');
    assert.deepEqual([matchingExplorers[0].project, matchingExplorers[0].title, matchingExplorers[0].management], [project, explorerTitle, 'INTERACTIVE']);
    let builder = await readBuilder();
    const empty = builderDraftStateEvidence(builder, 'empty');
    initialScope = {
      generation: builder.catalog?.generation,
      snapshotToken: builder.catalog?.snapshotToken,
      authorizationScopeDigest: builder.catalog?.authorizationScopeDigest,
    };
    const freshScope = matchingExplorers[0].project === project && initialScope.generation === generation &&
      Boolean(initialScope.snapshotToken && initialScope.authorizationScopeDigest) && empty.ok;
    check('correctness', 'fresh CDA Membership Explorer matches project, generation, snapshot, scope, and empty draft', freshScope, {
      project, explorer, title: explorerTitle, ...initialScope, emptyDraft: empty,
    });
    assert(freshScope, 'Fresh CDA Membership Explorer is outside the exact source/generation/scope contract');
    report.target.explorer = explorer;
    report.target.membershipSourceIDs = [oracle.leftIDs, oracle.rightIDs];
    capture = cda.captureRequests(explorerBase, { responsePaths: /commands|construction-capabilities|construction-proposals|selections/ });

    const left = await createSource('CDA Membership left raw ID source', oracle.leftIDs);
    const right = await createSource('CDA Membership right raw ID source', oracle.rightIDs);
    const sources = [left, right];
    check('persistence', 'two exact immutable source selections retain the shared ID and distinct left/right members',
      left.selection.id !== right.selection.id && isDeepStrictEqual(left.ids, oracle.leftIDs) && isDeepStrictEqual(right.ids, oracle.rightIDs), {
        selectionIDs: sources.map(source => source.selection.id),
        leftIDs: left.ids, rightIDs: right.ids,
        sharedIDs: oracle.includeIDs, leftOnlyIDs: oracle.excludeIDs,
        generation, authorizationScopeDigest: initialScope.authorizationScopeDigest,
      });
    await groupSource(left);
    await groupSource(right);
    builder = await readBuilder();
    assertScope(builder);
    const groupedSources = sources.map(source => getDocument(builder, source.outputId));
    const exactGroups = groupedSources.length === 2 && groupedSources.every((document, index) =>
      document.population?.selectionRevisionId === sources[index].selection.id && document.construction?.steps?.length === 1 &&
      document.construction.steps[0]?.id === sources[index].group.stepId &&
      document.construction.steps[0]?.operation?.kind === 'GROUP' &&
      document.construction.steps[0]?.operation?.group?.keys?.[0]?.inputColumnId === sources[index].idColumnId &&
      document.construction.steps[0]?.operation?.group?.aggregates?.[0]?.operation === 'COUNT_ROWS');
    check('persistence', 'two current-draft GROUP sources preserve exact ID populations and stable source column bindings', exactGroups, {
      sources: sources.map(source => ({ outputId: source.outputId, selectionId: source.selection.id, groupStepId: source.group.stepId,
        rootIDColumnId: source.idColumnId, groupKeyColumnId: source.group.keyColumnId, expectedRows: source.group.expectedRows })),
    });
    assert(exactGroups, 'CDA Membership GROUP sources changed their immutable population or root ID binding');
    assert.equal(selectionSwitches.length, 1, 'Selecting the second raw source must prove one exact source-to-source selection transition');

    const openNativeTarget = async targetPurpose => {
      await navigateBuilder(left.outputId);
      await selectSourceTable(left, left.group.expectedRows.length, 'Select left exact GROUP source to create a Membership target');
      const before = await readBuilder();
      assertScope(before);
      const rootNodeIDs = (before.catalog?.nodes ?? []).filter(node => node.resourceType === 'Observation' && node.rowRootEligible).map(node => node.nodeId);
      const priorIDs = (before.workspace?.documents ?? []).map(document => document.output.id);
      const fromIndex = report.nativeRequests.length;
      await action('Create a separate empty native Membership target', page.getByTestId('construction-action-combine'),
        locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitSelector('[data-testid="construction-combine-editor"]'));
      const event = await capture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/commands` && entry.method === 'POST' &&
        entry.status === 200 && entry.completedAt && entry.body?.commands?.some(item => item.type === 'CREATE_TABLE'),
      { fromIndex, timeoutMs: MAX_ACTION_MS });
      const body = capture.rawRequestBody(event);
      const response = capture.rawResponseBody(event);
      const mountedOutputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]').getAttribute('data-output-id');
      const evidence = nativeCombineTargetBindingEvidence({ requestBody: body, responseStatus: event.status, response,
        expectedRootNodeIds: rootNodeIDs, expectedRootResourceType: 'Observation', previousOutputIds: priorIDs, mountedOutputId });
      const targetRequestOwned = event.origin === new URL(uiOrigin).origin && event.path === `${explorerBase}/authoring/v2/commands`;
      check('correctness', `${targetPurpose} Membership target is a separate rooted empty current-draft document`, evidence.ok && targetRequestOwned, { ...evidence, targetRequestOwned });
      assert(evidence.ok && targetRequestOwned, `Native Membership target creation lost its exact root/current draft binding: ${JSON.stringify({ evidence, targetRequestOwned })}`);
      const after = await readBuilder();
      assertScope(after);
      const document = getDocument(after, evidence.outputId);
      assert.equal(document.rootResourceType, 'Observation');
      assert.equal(document.columns?.length, 0);
      assert.equal(document.construction?.steps?.length ?? 0, 0);
      await action('Choose native MEMBERSHIP combination', page.getByTestId('construction-combine-choice-membership'),
        locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitSelector('select[aria-label="Input table 1"]'));
      await waitSelector('select[aria-label="Input table 2"]');
      const capabilities = await inspectMembershipCapabilities(0, after, evidence.outputId, sources);
      sources.forEach((source, index) => {
        source.group.compiledNullable = capabilities.perSource[index]?.nullable;
      });
      check('correctness', `${targetPurpose} offers nullable scalar Observation ID GROUP keys to native Membership`, capabilities.ok, capabilities);
      assert(capabilities.ok, `Native MEMBERSHIP did not expose both nullable scalar ID GROUP keys: ${JSON.stringify(capabilities)}`);
      return { outputId: evidence.outputId, rootResourceType: 'Observation', baselineDocument: structuredClone(document), baseBuilder: after, capabilities };
    };

    let canceledTargetOutputId;
    {
      const firstTarget = await openNativeTarget('canceled-proposal');
      canceledTargetOutputId = firstTarget.outputId;
      report.target.canceledMembershipTarget = firstTarget.outputId;
      const draftBefore = await readBuilder();
      await configureMembership(sources, firstTarget.outputId, oracle.includeIDs, 'INCLUDE', draftBefore, 'canceled INCLUDE');
      const includeRows = oracle.includeIDs.map(id => [id]);
      assertRows('first INCLUDE preview keeps only the shared raw Observation ID', await readGrid('proposal'), ['Observation ID'], includeRows);
      const cancelBase = await readBuilder();
      await action('Cancel native INCLUDE Membership proposal', page.getByTestId('construction-cancel-proposal'),
        locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(`!document.querySelector('[data-testid="construction-combine-editor"]')&&!document.querySelector('[data-testid="construction-proposal-panel"]')`));
      await reloadAndSelect(firstTarget.outputId, undefined, 'Reload canceled Membership target without saving the proposal');
      const canceled = await readBuilder();
      const cancelEvidence = canceledDraftEvidence(cancelBase, canceled);
      const targetUnchanged = getDocument(canceled, firstTarget.outputId).columns?.length === 0 &&
        (getDocument(canceled, firstTarget.outputId).construction?.steps?.length ?? 0) === 0;
      check('persistence', 'Cancel leaves Membership candidate inputs, empty target, and draft CAS unchanged after reload', cancelEvidence.ok && targetUnchanged, {
        cancelEvidence, targetUnchanged, targetOutputId: firstTarget.outputId,
      });
      assert(cancelEvidence.ok && targetUnchanged, 'Cancel changed the saved Membership workspace or target');
    }

    let target;
    let includeCandidate;
    {
      target = await openNativeTarget('applied-proposal');
      report.target.membershipTarget = target.outputId;
      const base = await readBuilder();
      const proposal = await configureMembership(sources, target.outputId, oracle.includeIDs, 'INCLUDE', base, 'applied INCLUDE');
      includeCandidate = proposal;
      const applyRows = oracle.includeIDs.map(id => [id]);
      await applyProposal('Apply current-draft INCLUDE Membership proposal', target.outputId, applyRows.length);
    }
    builder = await readBuilder();
    assertScope(builder);
    let targetDocument = getDocument(builder, target.outputId);
    let step = targetDocument.construction?.steps?.at(-1);
    let operation = step?.operation?.combine;
    const includeSources = currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: sources.map(source => source.outputId), sourceDocuments: builder.workspace?.documents, publishedOutputIDs: [] });
    const proposalStepID = includeCandidate?.step?.id;
    const proposalOutputIDs = (includeCandidate?.step?.outputs ?? []).map(output => output.id);
    const projectedOutput = step?.outputs?.find(output => output.id === operation?.projections?.[0]?.outputColumnId);
    const outputNullability = membershipOutputNullabilityEvidence(projectedOutput, left.group.compiledNullable);
    const outputNullabilityPreserved = projectedOutput?.type === 'string' && outputNullability.ok;
    const includePersisted = targetDocument.construction?.steps?.length === 1 && step?.operation?.kind === 'COMBINE' &&
      step?.id === proposalStepID && isDeepStrictEqual((step?.outputs ?? []).map(output => output.id), proposalOutputIDs) &&
      operation?.kind === 'MEMBERSHIP' && operation.membershipMode === 'INCLUDE' && operation.keys?.length === 1 &&
      operation.keys[0]?.leftColumnId === left.group.keyColumnId && operation.keys[0]?.rightColumnId === right.group.keyColumnId &&
      operation.projections?.length === 1 && operation.projections[0]?.inputIndex === 0 &&
      operation.projections[0]?.inputColumnId === left.group.keyColumnId && includeSources.ok && outputNullabilityPreserved;
    check('persistence', 'applied INCLUDE Membership saves exact ID GROUP inputs, keys, and left-only projection', includePersisted, {
      stepId: step?.id, combine: operation, includeSources, projectedOutput,
      leftGroupCapabilityNullable: left.group.compiledNullable, outputNullability,
    });
    assert(includePersisted, 'Saved INCLUDE Membership changed its exact Group source bindings');
    const stepId = step.id;
    const outputIDs = (step.outputs ?? []).map(output => output.id);
    const keyPair = structuredClone(operation.keys);
    const inputBindings = structuredClone(step.inputs);
    const projections = structuredClone(operation.projections);
    const includeRows = oracle.includeIDs.map(id => [id]);
    await reloadAndSelect(target.outputId, includeRows.length, 'Reload applied INCLUDE Membership result');
    assertRows('applied INCLUDE Membership values and shared ID survive reload', await readGrid('saved'), ['Observation ID'], includeRows);
    builder = await readBuilder();
    assertScope(builder);
    const reloadedInclude = getDocument(builder, target.outputId).construction?.steps?.at(-1);
    const includeReloadExact = reloadedInclude?.id === stepId && reloadedInclude?.operation?.combine?.membershipMode === 'INCLUDE' &&
      isDeepStrictEqual(reloadedInclude?.inputs, inputBindings) && isDeepStrictEqual(reloadedInclude?.operation?.combine?.keys, keyPair) &&
      isDeepStrictEqual(reloadedInclude?.operation?.combine?.projections, projections) &&
      isDeepStrictEqual((reloadedInclude?.outputs ?? []).map(output => output.id), outputIDs);
    check('persistence', 'INCLUDE Membership step identity, keys, projection, and source IDs survive reload', includeReloadExact, {
      stepId: reloadedInclude?.id, inputs: reloadedInclude?.inputs, keys: reloadedInclude?.operation?.combine?.keys,
      projections: reloadedInclude?.operation?.combine?.projections, outputIDs: reloadedInclude?.outputs?.map(output => output.id),
    });
    assert(includeReloadExact, 'Reloaded INCLUDE Membership changed its saved construction identity');

    await openSavedStep(target.outputId, stepId, includeRows.length);
    const editBase = await readBuilder();
    assertScope(editBase);
    const excludeRows = oracle.excludeIDs.map(id => [id]);
    const proposeExcludeEdit = async (baseBuilder, phase) => {
      const modeSelector = 'select[aria-label="Which rows should stay?"]';
      const editFromIndex = report.nativeRequests.length;
      await selectOptionByValue(modeSelector, 'EXCLUDE', `${phase}: edit saved Membership from INCLUDE to EXCLUDE`,
        async () => waitFunction(proposalReady(target.outputId, excludeRows.length)));
      assertRows(`${phase} EXCLUDE edit keeps exactly the left-only raw Observation ID`,
        await readGrid('proposal'), ['Observation ID'], excludeRows);
      const event = await capture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/construction-proposals` &&
        entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.body?.outputId === target.outputId,
      { fromIndex: editFromIndex, timeoutMs: MAX_ACTION_MS });
      const body = capture.rawRequestBody(event);
      const response = capture.rawResponseBody(event);
      const candidateStep = body?.candidateConstruction?.steps?.at(-1);
      const candidateOperation = candidateStep?.operation?.combine;
      const editExact = candidateOperation?.membershipMode === 'EXCLUDE' && candidateStep?.id === stepId &&
        event.origin === new URL(uiOrigin).origin && event.path === `${explorerBase}/authoring/v2/construction-proposals` &&
        body?.outputId === target.outputId && body?.snapshotToken === baseBuilder.catalog?.snapshotToken &&
        response?.outputId === target.outputId && response?.snapshotToken === baseBuilder.catalog?.snapshotToken &&
        isDeepStrictEqual(candidateStep.inputs, inputBindings) && isDeepStrictEqual(candidateOperation.keys, keyPair) &&
        isDeepStrictEqual(candidateOperation.projections, projections) &&
        isDeepStrictEqual((candidateStep.outputs ?? []).map(output => output.id), outputIDs) &&
        body?.expectedDraftVersion === baseBuilder.draftVersion && body?.expectedDraftDigest === baseBuilder.draftDigest &&
        response?.draftVersion === body.expectedDraftVersion && response?.draftDigest === body.expectedDraftDigest &&
        response?.previewStatus === 'READY' && response?.preview?.outputId === target.outputId &&
        constructionCandidateWireEquivalent(body?.candidateConstruction, response?.candidateConstruction);
      check('correctness', `${phase} EXCLUDE edit changes only membership policy and retains step, inputs, keys, output, and CAS`, editExact, {
        editExact, stepId: candidateStep?.id, mode: candidateOperation?.membershipMode,
        inputs: candidateStep?.inputs, keys: candidateOperation?.keys, outputIDs: candidateStep?.outputs?.map(output => output.id),
      });
      assert(editExact, 'EXCLUDE edit changed saved Membership source/key/output identity or draft CAS');
      return event;
    };
    await proposeExcludeEdit(editBase, 'initial');
    await action('Cancel saved Membership EXCLUDE edit', page.getByTestId('construction-cancel-proposal'),
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(
        `!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-combine-editor"]')&&${selectedOutputReady(target.outputId)}`));
    await reloadAndSelect(target.outputId, includeRows.length, 'Reload saved INCLUDE Membership after canceling EXCLUDE edit');
    const afterCancelEdit = await readBuilder();
    assertScope(afterCancelEdit);
    const editCancelEvidence = canceledDraftEvidence(editBase, afterCancelEdit);
    const savedIncludeAfterCancel = getDocument(afterCancelEdit, target.outputId).construction?.steps?.at(-1);
    const cancelRetainsInclude = savedIncludeAfterCancel?.id === stepId &&
      savedIncludeAfterCancel?.operation?.kind === 'COMBINE' &&
      savedIncludeAfterCancel?.operation?.combine?.membershipMode === 'INCLUDE' &&
      isDeepStrictEqual(savedIncludeAfterCancel?.inputs, inputBindings) &&
      isDeepStrictEqual(savedIncludeAfterCancel?.operation?.combine?.keys, keyPair) &&
      isDeepStrictEqual(savedIncludeAfterCancel?.operation?.combine?.projections, projections) &&
      isDeepStrictEqual((savedIncludeAfterCancel?.outputs ?? []).map(output => output.id), outputIDs);
    check('persistence', 'Canceling EXCLUDE edit preserves exact saved INCLUDE Membership, rows, and CAS after reload',
      editCancelEvidence.ok && cancelRetainsInclude, {
        editCancelEvidence, cancelRetainsInclude, stepId: savedIncludeAfterCancel?.id,
        mode: savedIncludeAfterCancel?.operation?.combine?.membershipMode,
        inputs: savedIncludeAfterCancel?.inputs, keys: savedIncludeAfterCancel?.operation?.combine?.keys,
        projections: savedIncludeAfterCancel?.operation?.combine?.projections,
      });
    assert(editCancelEvidence.ok && cancelRetainsInclude, 'Canceling EXCLUDE edit changed the saved INCLUDE Membership or its draft CAS');
    assertRows('Canceling EXCLUDE edit preserves saved INCLUDE rows after reload', await readGrid('saved'), ['Observation ID'], includeRows);

    await openSavedStep(target.outputId, stepId, includeRows.length);
    const applyEditBase = await readBuilder();
    assertScope(applyEditBase);
    await proposeExcludeEdit(applyEditBase, 'reopened');
    await applyProposal('Apply reopened saved Membership EXCLUDE edit', target.outputId, excludeRows.length);

    builder = await readBuilder();
    assertScope(builder);
    targetDocument = getDocument(builder, target.outputId);
    step = targetDocument.construction?.steps?.at(-1);
    operation = step?.operation?.combine;
    const excludePersisted = step?.id === stepId && operation?.membershipMode === 'EXCLUDE' &&
      isDeepStrictEqual(step.inputs, inputBindings) && isDeepStrictEqual(operation.keys, keyPair) &&
      isDeepStrictEqual(operation.projections, projections) && isDeepStrictEqual((step.outputs ?? []).map(output => output.id), outputIDs);
    check('persistence', 'saved EXCLUDE Membership edit keeps exact source bindings and stable output identity', excludePersisted, {
      stepId: step?.id, mode: operation?.membershipMode, inputs: step?.inputs, keys: operation?.keys,
      projections: operation?.projections, outputIDs: step?.outputs?.map(output => output.id),
    });
    assert(excludePersisted, 'Applied EXCLUDE Membership did not preserve exact source/key/output bindings');
    await reloadAndSelect(target.outputId, excludeRows.length, 'Reload edited EXCLUDE Membership result');
    assertRows('EXCLUDE values survive reload as the exact left-only ID', await readGrid('saved'), ['Observation ID'], excludeRows);

    const beforeRemoveCancel = await readBuilder();
    await proposeRemove(target.outputId, stepId, 'canceled', beforeRemoveCancel);
    await action('Cancel proposed Membership removal', page.getByTestId('construction-cancel-proposal'),
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')`));
    await reloadAndSelect(target.outputId, excludeRows.length, 'Reload saved EXCLUDE Membership after canceling removal');
    const afterRemoveCancel = await readBuilder();
    const removalCancel = canceledDraftEvidence(beforeRemoveCancel, afterRemoveCancel);
    const stillSaved = getDocument(afterRemoveCancel, target.outputId).construction?.steps?.some(savedStep => savedStep.id === stepId &&
      savedStep.operation?.kind === 'COMBINE' && savedStep.operation?.combine?.membershipMode === 'EXCLUDE');
    check('persistence', 'Cancel removal preserves exact EXCLUDE Membership, full workspace, and CAS after reload', removalCancel.ok && stillSaved, {
      removalCancel, stillSaved, targetOutputId: target.outputId, stepId,
    });
    assert(removalCancel.ok && stillSaved, 'Canceling Membership removal changed its saved workspace');

    const beforeRemoveApply = await readBuilder();
    const removalProposal = await proposeRemove(target.outputId, stepId, 'applied', beforeRemoveApply);
    const removalProposalResponse = capture.rawResponseBody(removalProposal);
    assert(removalProposalResponse?.proposalId && removalProposalResponse?.candidateWorkspaceDigest,
      'Applied Membership removal must retain its exact proposal receipt and next draft digest');
    const applyFromIndex = report.nativeRequests.length;
    const applyActionStartedAt = Date.now();
    await action('Apply Membership removal to restore the rooted empty target', page.getByTestId('construction-apply-proposal'),
      locator => locator.click({ timeout: MAX_ACTION_MS }), async () => {
        await waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&${selectedOutputReady(target.outputId)}`);
        const liveBuilder = await readBuilder();
        assertScope(liveBuilder);
        assert.equal(liveBuilder.draftVersion, beforeRemoveApply.draftVersion + 1,
          'Applying Membership removal must advance the expected draft version once');
        assert.equal(liveBuilder.draftDigest, removalProposalResponse.candidateWorkspaceDigest,
          'Applied Membership removal must match its exact proposal candidate digest');
        const restoredDocument = getDocument(liveBuilder, target.outputId);
        assert.equal(restoredDocument.rootResourceType, 'Observation');
        assert.deepEqual(restoredDocument.columns, []);
        assert.deepEqual(restoredDocument.construction?.steps ?? [], []);

        const capabilitiesPath = `${explorerBase}/authoring/v2/construction-capabilities`;
        const capabilitiesTimeoutMs = MAX_ACTION_MS - (Date.now() - applyActionStartedAt);
        assert(capabilitiesTimeoutMs > 0, 'Membership removal exhausted its action-to-render budget before capabilities completed');
        const capabilitiesEvent = await capture.waitFor(entry => {
          const response = capture.rawResponseBody(entry);
          return entry.origin === new URL(uiOrigin).origin && entry.path === capabilitiesPath && entry.method === 'POST' &&
            Number.isFinite(entry.completedAt) && entry.status === 200 && entry.failure === undefined &&
            entry.responseReadError === undefined && response !== null && typeof response === 'object' &&
            entry.body?.snapshotToken === liveBuilder.catalog?.snapshotToken &&
            entry.body?.expectedDraftVersion === liveBuilder.draftVersion &&
            entry.body?.expectedDraftDigest === liveBuilder.draftDigest && entry.body?.outputId === target.outputId &&
            entry.body?.stageId === 'source_projection';
        },
        { fromIndex: applyFromIndex, timeoutMs: capabilitiesTimeoutMs });
        const capabilitiesBody = capture.rawRequestBody(capabilitiesEvent);
        const capabilitiesResponse = capture.rawResponseBody(capabilitiesEvent);
        assert.equal(capabilitiesEvent.status, 200,
          'Restored Membership target capabilities must complete with HTTP 200');
        assert(capabilitiesEvent.completedAt && capabilitiesResponse && typeof capabilitiesResponse === 'object',
          'Restored Membership target capabilities must retain the completed response body');
        assert.deepEqual({
          snapshotToken: capabilitiesBody?.snapshotToken,
          expectedDraftVersion: capabilitiesBody?.expectedDraftVersion,
          expectedDraftDigest: capabilitiesBody?.expectedDraftDigest,
          outputId: capabilitiesBody?.outputId,
          stageId: capabilitiesBody?.stageId,
        }, {
          snapshotToken: liveBuilder.catalog?.snapshotToken,
          expectedDraftVersion: liveBuilder.draftVersion,
          expectedDraftDigest: liveBuilder.draftDigest,
          outputId: target.outputId,
          stageId: 'source_projection',
        }, 'Restored Membership capabilities must use the exact live draft CAS and source projection');
        assert.deepEqual({
          snapshotToken: capabilitiesResponse.snapshotToken,
          draftVersion: capabilitiesResponse.draftVersion,
          draftDigest: capabilitiesResponse.draftDigest,
          outputId: capabilitiesResponse.outputId,
          stageId: capabilitiesResponse.stageId,
          selectedStageId: capabilitiesResponse.selectedStage?.id,
        }, {
          snapshotToken: liveBuilder.catalog?.snapshotToken,
          draftVersion: liveBuilder.draftVersion,
          draftDigest: liveBuilder.draftDigest,
          outputId: target.outputId,
          stageId: 'source_projection',
          selectedStageId: 'source_projection',
        }, 'Restored Membership capabilities response must match the exact live source projection');
        const sourceStage = capabilitiesResponse.stages?.find(stage => stage.id === 'source_projection');
        const groupCapability = sourceStage?.capabilities?.find(capability => capability.kind === 'GROUP');
        assert(sourceStage && typeof groupCapability?.supported === 'boolean',
          'Restored source projection capabilities must include GROUP support state');

        await page.getByTestId('construction-rows-settings-trigger').click({ timeout: MAX_ACTION_MS });
        await waitSelector('[data-testid="construction-action-group-rows"]');
        const visibleGroupCapability = await page.getByTestId('construction-action-group-rows').evaluate(button => ({
          visible: button.getClientRects().length > 0,
          disabled: button.disabled,
          text: button.innerText,
        }));
        const visibleCapabilityMatches = visibleGroupCapability.visible &&
          visibleGroupCapability.disabled === !groupCapability.supported &&
          (groupCapability.supported || !groupCapability.reason ||
            normalizeText(visibleGroupCapability.text).includes(normalizeText(groupCapability.reason)));
        check('correctness', 'applied Membership removal captures and renders exact source-projection capabilities',
          visibleCapabilityMatches, {
            requestId: capabilitiesEvent.requestId, browserRequestId: capabilitiesEvent.browserRequestId,
            status: capabilitiesEvent.status, completedAt: capabilitiesEvent.completedAt,
            liveDraftVersion: liveBuilder.draftVersion, liveDraftDigest: liveBuilder.draftDigest,
            outputId: capabilitiesResponse.outputId, stageId: capabilitiesResponse.stageId,
            groupCapability: { supported: groupCapability.supported, reason: groupCapability.reason ?? null },
            visibleGroupCapability,
        });
        assert(visibleCapabilityMatches,
          'Visible restored Membership GROUP capability must match the captured source-stage capability');
      });
    await reloadAndSelect(target.outputId, undefined, 'Reload removed Membership and select its empty target');
    builder = await readBuilder();
    assertScope(builder);
    targetDocument = getDocument(builder, target.outputId);
    const restored = rootedEmptyTargetRestorationEvidence(targetDocument, target.baselineDocument, target);
    check('persistence', 'applying Membership removal and reloading restores the exact rooted empty target', restored.ok, {
      targetOutputId: target.outputId, restored,
      rootResourceType: targetDocument.rootResourceType, columns: targetDocument.columns, steps: targetDocument.construction?.steps ?? [],
    });
    assert(restored.ok, 'Membership removal did not restore its exact empty rooted target');
    const sourceDocuments = sources.map(source => getDocument(builder, source.outputId));
    const fullSourceBaselinesRestored = sourceDocuments.every((document, index) =>
      isDeepStrictEqual(document, sources[index].baselineDocument));
    const groupsRemain = fullSourceBaselinesRestored && sourceDocuments.every((document, index) =>
      document.population?.selectionRevisionId === sources[index].selection.id &&
      document.construction?.steps?.length === 1 && document.construction.steps[0]?.id === sources[index].group.stepId &&
      document.construction.steps[0]?.operation?.kind === 'GROUP' &&
      document.construction.steps[0]?.operation?.group?.keys?.[0]?.inputColumnId === sources[index].idColumnId &&
      document.construction.steps[0]?.operation?.group?.aggregates?.[0]?.operation === 'COUNT_ROWS');
    check('persistence', 'removing Membership preserves both exact immutable GROUP source populations after reload', groupsRemain, {
      fullSourceBaselinesRestored,
      sources: sourceDocuments.map((document, index) => ({ outputId: document.output.id,
        fullBaselineMatches: isDeepStrictEqual(document, sources[index].baselineDocument),
        selectionRevisionId: document.population?.selectionRevisionId, expectedSelectionId: sources[index].selection.id,
        groupStepIds: document.construction?.steps?.map(savedStep => savedStep.id),
        expectedGroupStepId: sources[index].group.stepId })),
    });
    assert(groupsRemain, 'Removing Membership changed one of its upstream GROUP source outputs');
    const allInputs = builder.workspace?.documents?.flatMap(document => document.construction?.steps?.flatMap(savedStep => savedStep.inputs ?? []) ?? []) ?? [];
    const hasPinnedRevision = allInputs.some(input => input.kind === 'TABLE_REVISION' || input.revisionId || input.tableId);
    const noPublishOrPinned = publishRequests === 0 && !hasPinnedRevision;
    check('correctness', 'CDA Membership lifecycle makes no Publish request or pinned table revision reference', noPublishOrPinned, {
      publishRequests, inputKinds: allInputs.map(input => input.kind), hasPinnedRevision,
      nativePublishRequests: report.nativeRequests.filter(entry => entry.path.endsWith('/authoring/v2/publish')).length,
    });
    assert(noPublishOrPinned, 'CDA Membership lifecycle published or bound a pinned table revision');
    assert.equal(selectionSwitches.length, 2, 'The Membership lifecycle must prove both exact source-to-source selection switches');
    const maxActionMs = Math.max(0, ...actionDurations.map(item => item.elapsedMs));
    const allActionsWithinBudget = actionDurations.length > 0 && actionDurations.every(item => item.elapsedMs <= MAX_ACTION_MS) && maxActionMs <= MAX_ACTION_MS;
    check('performance', 'all native CDA Membership lifecycle actions complete within five seconds', allActionsWithinBudget, {
      actionCount: actionDurations.length, maxActionMs, actions: actionDurations,
    });
    assert(allActionsWithinBudget, 'One or more native Membership lifecycle actions exceeded five seconds');
    report.membershipLifecycle = {
      rawOracle: report.target.membershipRawOracle,
      sourceOutputIDs: sources.map(source => source.outputId),
      selectionRevisionIDs: sources.map(source => source.selection.id),
      groupStepIDs: sources.map(source => source.group.stepId),
      canceledTargetOutputId,
      appliedTargetOutputId: target.outputId,
      membershipStepId: stepId,
      actionCount: actionDurations.length,
      maxActionMs,
      selectionSwitches,
      stages: ['raw-oracle', 'exact-selections', 'two-native-GROUP-sources', 'INCLUDE-cancel-reload', 'INCLUDE-apply-reload',
        'EXCLUDE-preview-cancel-reload', 'EXCLUDE-reopen-apply-reload', 'removal-cancel-reload', 'removal-apply-reload'],
    };
  } finally {
    page.off('request', onRequest);
  }
}
