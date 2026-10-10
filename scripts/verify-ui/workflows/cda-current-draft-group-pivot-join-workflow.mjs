import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { expect } from '@playwright/test';
import {
  builderDraftStateEvidence,
  canceledDraftEvidence,
  constructionCandidateWireEquivalent,
  currentDraftSourceEvidence,
  workspaceOutputOption,
} from '../helpers/builder-combine-draft-helpers.mjs';
import {
  nativeCombineTargetBindingEvidence,
  rootedEmptyTargetAppliedExpression,
  rootedEmptyTargetRestorationEvidence,
} from '../helpers/builder-combine-helpers.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { proposalPreviewReadinessExpression } from '../helpers/proposal-preview-readiness.mjs';
import {
  chooseCdaGroupPivotJoinWitness,
  selectCdaGroupPivotJoinSubjects,
  prepareCdaGroupPivotJoinOracle,
  verifyCdaGroupPivotJoinReread,
} from '../helpers/cda-group-pivot-join-oracle.mjs';

const generationExpected = 'cda-fhir-v1';
const scanLimit = 2_000;
const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const encode = value => encodeURIComponent(value);
const basePath = (project, explorer) => `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const display = value => value === null || value === undefined ? '—' : String(value);

export function isCdaGroupPivotJoinOracleUnavailable(error) {
  return error?.name === 'RawOracleUnavailableError' && error.rawOracleFailure === true;
}

function runArango(arangoContainer, query) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `Bounded owned Arango query failed: ${tidy(result.stderr || result.stdout).slice(0, 1600)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango query returned no JSON array: ${tidy(result.stdout).slice(-800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
}

export async function cdaCurrentDraftGroupPivotJoinWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(cda.target);
  assert.equal(project, cda.target.fixtureProject, 'The bounded raw oracle must use the owned CDA fixture project.');
  assert.equal(generation, generationExpected, 'Group→Pivot→Join requires the pinned CDA FHIR generation.');
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  report.phase = 'CDA current-draft Group→Pivot→Join lifecycle';
  report.scope = { project, generation, initialSampleRowLimit: scanLimit, targetedSubjectRowLimit: scanLimit, maxSelectedSubjects: 10, maxReturnedRows: scanLimit * 2 };
  report.cases ??= [];
  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-group-pivot-join-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${tidy(text).slice(0, 1200)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1600)}`);
    return value;
  };
  const eligibleCodeFilter = `IS_STRING(r.payload.status) AND LENGTH(TRIM(r.payload.status)) > 0
      AND IS_ARRAY(r.payload.code.coding) AND LENGTH(r.payload.code.coding) > 0
      AND IS_STRING(r.payload.code.coding[0].code) AND LENGTH(TRIM(r.payload.code.coding[0].code)) > 0`;
  const codeCodingCodes = `(IS_ARRAY(r.payload.code.coding) ? r.payload.code.coding[*].code : [])`;
  const initialQuery = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0
      AND ${eligibleCodeFilter}
    SORT r._id LIMIT ${scanLimit}
    RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,status:r.payload.status,
      codeCodingCodes:${codeCodingCodes}}`;
  const initialRows = runArango(arangoContainer, initialQuery);
  const selectedSubjects = selectCdaGroupPivotJoinSubjects(initialRows, { project, generation, limit: 10 });
  const targetedQuery = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND r.payload.subject.reference IN ${JSON.stringify(selectedSubjects)}
      AND ${eligibleCodeFilter}
    SORT r._id LIMIT ${scanLimit}
    RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,status:r.payload.status,
      codeCodingCodes:${codeCodingCodes}}`;
  const targetedRows = runArango(arangoContainer, targetedQuery);
  const witness = chooseCdaGroupPivotJoinWitness(targetedRows, { project, generation, limit: scanLimit });
  if (!witness) {
    const error = new Error(`The initial ${scanLimit}-row Observation sample selected ${selectedSubjects.length} subjects; the targeted query returned ${targetedRows.length} rows and lacks the shared-subject A,A,B code-category and left-only subject witness. No lifecycle result is claimed.`);
    error.name = 'RawOracleUnavailableError';
    error.rawOracleFailure = true;
    report.status = 'unverified';
    report.unverifiedReason = error.message;
    report.finished = new Date().toISOString();
    await cda.attachReport('group-pivot-join', report);
    throw error;
  }
  const exactIds = witness.left.map(row => row._id);
  const exactReadQuery = `FOR r IN Observation
    FILTER r._id IN ${JSON.stringify(exactIds)}
      AND r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
    SORT r._id
    RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,status:r.payload.status,
      codeCodingCodes:${codeCodingCodes}}`;
  const exactRows = runArango(arangoContainer, exactReadQuery);
  const rawReread = verifyCdaGroupPivotJoinReread(witness, exactRows, { project, generation });
  const oracle = prepareCdaGroupPivotJoinOracle(witness, { project, generation });
  const scanEvent = {
    rowsReturned: initialRows.length + targetedRows.length,
    initialRowsReturned: initialRows.length, initialSampleLimit: scanLimit,
    selectedSubjectReferences: selectedSubjects, targetedRowsReturned: targetedRows.length,
    targetedQueryLimit: scanLimit, maxReturnedRows: scanLimit * 2, targetedQuery, exactReadQuery,
    leftSelectedFHIRIDs: witness.left.map(row => row.id), rightSelectedFHIRIDs: witness.right.map(row => row.id),
    selectedArangoKeys: rawReread.leftIDs, sharedSubject: witness.sharedSubject,
    leftOnlySubject: witness.leftOnlySubject, categories: witness.categories,
  };
  cda.check('correctness', 'Bounded scoped raw Observation witness has exact A,A,B shared and left-only identities', true, scanEvent);

  const rootPath = `/api/v1/projects/${encode(project)}/explorers`;
  const explorer = `cda-group-pivot-join-${randomUUID()}`;
  const title = 'CDA Group Pivot Join QA';
  const created = await api(rootPath, { name: explorer, title });
  assert.equal(created.explorerId ?? created.id ?? created.explorer?.id, explorer);
  const base = basePath(project, explorer);
  const authoring = `${base}/authoring/v2`;
  const uiURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`;
  const capture = cda.captureRequests(authoring, { responsePaths: /commands|construction-proposals/ });
  let builder = await api(`${authoring}/builder`);
  const empty = builderDraftStateEvidence(builder, 'empty');
  assert(empty.ok, `Fresh Explorer must have an empty draft: ${JSON.stringify(empty)}`);
  assert.equal(builder.catalog.generation, generation);
  const initialScope = {
    snapshotToken: builder.catalog.snapshotToken,
    authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
    generation: builder.catalog.generation,
  };
  assert(initialScope.snapshotToken && initialScope.authorizationScopeDigest);
  const root = builder.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible);
  assert.equal(root.length, 1, 'Scoped catalog must expose exactly one eligible Observation root.');
  const publishPath = `${authoring}/publish`;
  const selectionPrefix = `${base}/selections/`;
  const selectionReadEvents = new Map();
  let activeSelectionTransition;
  let publishRequests = 0;
  const onRequest = request => {
    try {
      const url = new URL(request.url());
      if ([apiOrigin, uiOrigin].some(origin => url.origin === new URL(origin).origin) && url.pathname === publishPath) publishRequests += 1;
      if (request.method() === 'GET' && url.origin === new URL(uiOrigin).origin &&
        url.pathname.startsWith(selectionPrefix) && url.searchParams.get('limit') === '1') {
        const encodedSelectionId = url.pathname.slice(selectionPrefix.length);
        if (encodedSelectionId && !encodedSelectionId.includes('/')) {
          selectionReadEvents.set(request, {
            selectionId: decodeURIComponent(encodedSelectionId), path: url.pathname,
            origin: url.origin, method: request.method(), limit: url.searchParams.get('limit'),
            startedAt: Date.now(), responseStatus: null, responseAt: null, completedAt: null,
            failedAt: null, failure: null, transitionAction: activeSelectionTransition?.label ?? null,
          });
        }
      }
    } catch {}
  };
  const onSelectionResponse = response => {
    const event = selectionReadEvents.get(response.request());
    if (!event) return;
    event.responseStatus = response.status();
    event.responseAt = Date.now();
  };
  const onSelectionRequestFinished = request => {
    const event = selectionReadEvents.get(request);
    if (event) event.completedAt = Date.now();
  };
  const onSelectionRequestFailed = request => {
    const event = selectionReadEvents.get(request);
    if (!event) return;
    event.failedAt = Date.now();
    event.failure = request.failure()?.errorText ?? null;
    event.transitionAction = activeSelectionTransition?.label ?? null;
  };
  page.on('request', onRequest);
  page.on('response', onSelectionResponse);
  page.on('requestfinished', onSelectionRequestFinished);
  page.on('requestfailed', onSelectionRequestFailed);

  const getDocument = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected exactly one document ${outputId}.`);
    return matches[0];
  };
  const readBuilder = () => api(`${authoring}/builder`);
  const checkScope = state => {
    assert.equal(state.catalog.generation, initialScope.generation);
    assert.equal(state.catalog.snapshotToken, initialScope.snapshotToken);
    assert.equal(state.catalog.authorizationScopeDigest, initialScope.authorizationScopeDigest);
  };
  const command = async commands => {
    const state = builder;
    const response = await api(`${authoring}/commands`, {
      commandId: randomUUID(), semanticsVersion: state.workspace?.semanticsVersion ?? 10,
      snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest, commands,
    });
    builder = await readBuilder();
    checkScope(builder);
    return response;
  };
  const reportCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const action = (name, locator, perform, after) => cda.action(name, locator, perform, {
    timeout: 5_000, budget: 5_000,
    ...(after ? { after, requiredCheck: 'all native lifecycle actions complete within five seconds' } : {}),
  });
  const waitFunction = (predicate, timeout = 5_000) => page.waitForFunction(predicate, undefined, { timeout: Math.min(5_000, timeout) });
  const waitSelector = selector => page.locator(selector).waitFor({ state: 'visible', timeout: 5_000 });
  const waitEnabled = async locator => {
    await locator.waitFor({ state: 'visible', timeout: 5_000 });
    await expect(locator).toBeEnabled({ timeout: 5_000 });
  };
  const proposalReady = (outputId, count) => proposalPreviewReadinessExpression(outputId, count);
  const waitProposal = (outputId, count) => waitFunction(proposalReady(outputId, count));
  const savedReady = (outputId, count) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${JSON.stringify(outputId)}&&t?.getAttribute('aria-rowcount')===${JSON.stringify(String(count + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
  const waitSaved = (outputId, count) => waitFunction(savedReady(outputId, count));
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal' ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
    const table = kind === 'proposal' ? proposal?.querySelector('table') : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [] };
    const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = proposal
      ? [...table.querySelectorAll('thead th')].map(cell => clean(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => clean(cell.textContent));
    const rows = proposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => clean(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => clean(cell.innerText)));
    return { ready: true, headers, rows };
  }, { kind });
  const assertGrid = (name, grid, headers, rows, evidence = {}) => {
    const ok = grid.ready && isDeepStrictEqual(grid.headers, headers) && isDeepStrictEqual(sortRows(grid.rows), sortRows(rows));
    reportCheck('correctness', name, ok, { headers: grid.headers, expectedHeaders: headers, rows: grid.rows, expectedRows: rows, ...evidence });
    assert(ok, `${name}: visible rows differ from independent raw oracle.`);
    return grid;
  };
  const previewEvent = async ({ fromIndex, outputId, baseState, matchesCandidate }) => {
    const event = await capture.waitFor(entry => entry.path === `${authoring}/construction-proposals` &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.body?.outputId === outputId &&
      (!matchesCandidate || matchesCandidate(entry.body?.candidateConstruction)),
    { fromIndex, timeoutMs: 5_000 });
    const requestBody = capture.rawRequestBody(event);
    const responseBody = capture.rawResponseBody(event);
    assert(requestBody && responseBody, 'Native proposal request and response bodies must be retained.');
    const dom = await page.evaluate(() => ({
      proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id') ?? null,
      receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
      outputId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-output-id') ?? null,
    }));
    assert.equal(event.origin, new URL(uiOrigin).origin);
    assert.equal(requestBody.outputId, outputId);
    assert.equal(responseBody.outputId, outputId);
    assert.equal(responseBody.previewStatus, 'READY');
    assert.equal(dom.outputId, outputId);
    assert.equal(responseBody.proposalId, dom.proposalId);
    assert.equal(responseBody.preview?.receiptId, responseBody.proposalId);
    assert.equal(dom.receiptId, responseBody.proposalId);
    assert.equal(responseBody.snapshotToken, requestBody.snapshotToken);
    assert.equal(responseBody.draftVersion, requestBody.expectedDraftVersion);
    assert.equal(responseBody.draftDigest, requestBody.expectedDraftDigest);
    if (baseState) {
      assert.equal(requestBody.snapshotToken, baseState.catalog.snapshotToken);
      assert.equal(requestBody.expectedDraftVersion, baseState.draftVersion);
      assert.equal(requestBody.expectedDraftDigest, baseState.draftDigest);
    }
    assert(constructionCandidateWireEquivalent(requestBody.candidateConstruction, responseBody.candidateConstruction));
    return { event, requestBody, responseBody };
  };
  const selectColumnById = async (selector, expectedId) => {
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
      value: option.value, text: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(), disabled: option.disabled,
    })));
    const candidates = options.filter(option => option.value === expectedId && !option.disabled);
    assert.equal(candidates.length, 1, `Expected exact stable column ${expectedId} in ${selector}: ${JSON.stringify(options)}`);
    await action(`Choose ${expectedId}`, page.locator(selector), locator => locator.selectOption(expectedId, { timeout: 5_000 }));
  };
  const outputOption = async (selector, outputId) => {
    const exactValue = workspaceOutputOption(outputId);
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
      value: option.value, group: option.parentElement?.label ?? '', disabled: option.disabled,
    })));
    const matches = options.filter(option => option.value === exactValue && option.group === 'Current draft tables' && !option.disabled);
    assert.equal(matches.length, 1, `Current-draft selector must expose only the requested ${outputId}: ${JSON.stringify(options)}`);
    return exactValue;
  };
  const selectTable = async (outputId, rowCount, label) => {
    const locator = page.getByTestId(`construction-table-${outputId}`);
    await waitSelector(`[data-testid="construction-table-${outputId}"]`);
    if (await locator.getAttribute('aria-current') === 'page') {
      await waitSaved(outputId, rowCount);
      return;
    }
    const exactSourceSwitch = label === 'Select right Group Pivot exact source population';
    if (!exactSourceSwitch) {
      await action(label, locator, target => target.click({ timeout: 5_000 }), async () => waitSaved(outputId, rowCount));
      return;
    }

    const selectedOutputBeforeSwitch = await page.evaluate(() => {
      const selected = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
      return selected?.getAttribute('data-testid')?.slice('construction-table-'.length) ?? null;
    });
    assert(selectedOutputBeforeSwitch && selectedOutputBeforeSwitch !== outputId,
      'The exact right Group Pivot source switch must begin from a different selected table.');
    const previousDocument = getDocument(builder, selectedOutputBeforeSwitch);
    const nextDocument = getDocument(builder, outputId);
    assert.equal(previousDocument.output?.title, 'CDA left Group source',
      'The expected canceled selection read must belong to the selected left Group source.');
    assert.equal(nextDocument.output?.title, 'CDA right Group Pivot source');
    const previousSelectionId = previousDocument.population?.selectionRevisionId;
    const nextSelectionId = nextDocument.population?.selectionRevisionId;
    assert(previousSelectionId && nextSelectionId && previousSelectionId !== nextSelectionId,
      'The native source switch must expose distinct saved left and right selection identities.');
    const previousSelectionPath = `${selectionPrefix}${encode(previousSelectionId)}`;
    const nextSelectionPath = `${selectionPrefix}${encode(nextSelectionId)}`;
    const transitionStartedAt = Date.now();
    const pendingPreviousReads = [...selectionReadEvents.values()].filter(event =>
      event.selectionId === previousSelectionId && event.path === previousSelectionPath &&
      event.origin === new URL(uiOrigin).origin && event.method === 'GET' && event.limit === '1' &&
      event.startedAt <= transitionStartedAt && event.completedAt === null && event.failedAt === null);
    assert(pendingPreviousReads.length <= 1,
      'At most one exact left attached-selection read may be pending when switching tables.');
    const cancellationStart = report.expectedCancellations?.length ?? 0;
    activeSelectionTransition = { label, startedAt: transitionStartedAt };
    const responseOutcome = page.waitForResponse(response => {
      try {
        const url = new URL(response.url());
        return url.origin === new URL(uiOrigin).origin && response.request().method() === 'GET' &&
          url.pathname === nextSelectionPath && url.searchParams.get('limit') === '1';
      } catch { return false; }
    }, { timeout: 5_000 }).then(response => ({ response }), error => ({ error }));
    let rightSelectionRead;
    let transitionFinishedAt;
    try {
      await cda.withExpectedCancellations({
        origin: uiOrigin,
        method: 'GET',
        paths: [previousSelectionPath],
        requestIdPrefixes: ['cda-request-'],
        reason: 'Selecting the right Group Pivot output retires the previous left attached-selection query.',
        proof: {
          project, explorer, generation,
          previousOutputId: selectedOutputBeforeSwitch,
          previousSelectionRevisionId: previousSelectionId,
          nextOutputId: outputId,
          nextSelectionRevisionId: nextSelectionId,
          query: { method: 'GET', limit: 1 },
          retirement: 'BuilderWorkspace replaces the keyed active-table population selection query; resourceFor aborts its stale getSelection AbortSignal when the selected table changes.',
        },
        actionLabel: label,
      }, async () => action(label, locator, target => target.click({ timeout: 5_000 }), async () => {
        const [responseResult] = await Promise.all([
          responseOutcome,
          waitSaved(outputId, rowCount),
        ]);
        if (responseResult.error) {
          throw new Error(`Right Group Pivot selection read did not resolve during ${label}: ${tidy(responseResult.error.message).slice(0, 800)}`);
        }
        const selectionResponse = responseResult.response;
        assert.equal(selectionResponse.status(), 200, 'The exact right Group Pivot selection read must succeed.');
        const selectionPage = await selectionResponse.json();
        const revision = selectionPage?.revision;
        assert.deepEqual({
          id: revision?.id,
          project: revision?.project,
          generation: revision?.generation,
          resourceType: revision?.resourceType,
          scopeDigest: revision?.scopeDigest,
          memberCount: revision?.memberCount,
          complete: revision?.complete,
        }, {
          id: nextSelectionId,
          project,
          generation,
          resourceType: 'Observation',
          scopeDigest: initialScope.authorizationScopeDigest,
          memberCount: rowCount,
          complete: true,
        }, 'The replacement GET must return the exact right source selection in the owned scope.');
        rightSelectionRead = selectionReadEvents.get(selectionResponse.request());
        assert(rightSelectionRead, 'The exact right selection response must have a matching native request-start event.');
        assert.deepEqual([rightSelectionRead.selectionId, rightSelectionRead.path, rightSelectionRead.origin,
          rightSelectionRead.method, rightSelectionRead.limit, rightSelectionRead.responseStatus], [
          nextSelectionId, nextSelectionPath, new URL(uiOrigin).origin, 'GET', '1', 200,
        ]);
        assert(rightSelectionRead.startedAt >= transitionStartedAt && rightSelectionRead.responseAt >= transitionStartedAt,
          'The exact right selection GET must start and resolve after the native table switch begins.');
      }));
    } finally {
      transitionFinishedAt = Date.now();
      activeSelectionTransition = undefined;
    }
    const cancellations = (report.expectedCancellations ?? []).slice(cancellationStart);
    assert(cancellations.length <= 1,
      'Switching to the right Group Pivot may retire at most one pending left selection GET.');
    const previousFailures = [...selectionReadEvents.values()].filter(event =>
      event.selectionId === previousSelectionId && event.path === previousSelectionPath && event.limit === '1' &&
      event.failure === 'net::ERR_ABORTED' && event.failedAt >= transitionStartedAt && event.failedAt <= transitionFinishedAt);
    let retiredPreviousRead;
    if (cancellations.length === 1) {
      const [cancellation] = cancellations;
      assert.equal(pendingPreviousReads.length, 1,
        'A retired left selection GET must be the exact request already pending before the native switch.');
      assert.equal(previousFailures.length, 1,
        'Only the exact pending left selection GET may be aborted during the native switch.');
      retiredPreviousRead = previousFailures[0];
      assert.strictEqual(retiredPreviousRead, pendingPreviousReads[0]);
      assert(retiredPreviousRead.startedAt < transitionStartedAt && retiredPreviousRead.failedAt >= transitionStartedAt);
      assert.equal(retiredPreviousRead.transitionAction, label);
      assert.deepEqual([cancellation.method, new URL(cancellation.url).origin, new URL(cancellation.url).pathname,
        cancellation.reason, cancellation.proof?.scopeAction], [
        'GET', new URL(uiOrigin).origin, previousSelectionPath,
        'Selecting the right Group Pivot output retires the previous left attached-selection query.', label,
      ]);
      const diagnostics = report.network.filter(entry => entry.kind === 'network' &&
        entry.playwrightRequestId === cancellation.playwrightRequestId);
      assert.equal(diagnostics.length, 1, 'The exact retired selection GET must remain in the native network ledger.');
      assert.deepEqual([diagnostics[0].url, diagnostics[0].errorText, diagnostics[0].triggerAction,
        diagnostics[0].expected, diagnostics[0].cancellationAction], [
        `${new URL(uiOrigin).origin}${previousSelectionPath}`, 'net::ERR_ABORTED', label, true, label,
      ]);
      const requestErrors = report.errors.filter(entry => entry.kind === 'network' &&
        entry.playwrightRequestId === cancellation.playwrightRequestId);
      assert(requestErrors.every(entry => entry.expected === true),
        'The exact retired left selection GET must have no unclassified native error diagnostic.');
    } else {
      assert.equal(previousFailures.length, 0,
        'An unclassified left selection abort cannot be hidden by the table-switch proof.');
    }
    report.selectionSwitchEvidence = {
      action: label,
      transition: { startedAt: transitionStartedAt, finishedAt: transitionFinishedAt,
        elapsedMs: transitionFinishedAt - transitionStartedAt },
      previous: { outputId: selectedOutputBeforeSwitch, selectionRevisionId: previousSelectionId },
      next: { outputId, selectionRevisionId: nextSelectionId },
      pendingPreviousReadsAtTransition: pendingPreviousReads.map(event => ({
        selectionId: event.selectionId, path: event.path, limit: event.limit, startedAt: event.startedAt,
      })),
      retiredPreviousRead: retiredPreviousRead ? {
        path: retiredPreviousRead.path, limit: retiredPreviousRead.limit,
        startedAt: retiredPreviousRead.startedAt, failedAt: retiredPreviousRead.failedAt,
        failure: retiredPreviousRead.failure, transitionAction: retiredPreviousRead.transitionAction,
        cancellationRequestId: cancellations[0].requestId,
        playwrightRequestId: cancellations[0].playwrightRequestId,
      } : null,
      rightSelectionRead: rightSelectionRead ? {
        path: rightSelectionRead.path, limit: rightSelectionRead.limit,
        selectionId: rightSelectionRead.selectionId, status: rightSelectionRead.responseStatus,
        startedAt: rightSelectionRead.startedAt, responseAt: rightSelectionRead.responseAt,
      } : null,
      expectedCancellationCount: cancellations.length,
    };
  };
  const reloadTable = async (outputId, count, label) => {
    const locator = page.getByTestId(`construction-table-${outputId}`);
    await action(label, locator, async target => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: 5_000 });
      await waitSelector(`[data-testid="construction-table-${outputId}"]`);
      if (await target.getAttribute('aria-current') !== 'page') await target.click({ timeout: 5_000 });
    }, async () => {
      if (count === undefined) await waitFunction(rootedEmptyTargetAppliedExpression(outputId));
      else await waitSaved(outputId, count);
    });
  };
  const verifySelection = async selection => {
    const pageValue = await api(`${base}/selections/${encode(selection.id)}?limit=100`);
    assert.equal(pageValue.revision?.id, selection.id);
    assert.equal(pageValue.revision?.project, project);
    assert.equal(pageValue.revision?.generation, generation);
    assert.equal(pageValue.revision?.resourceType, 'Observation');
    assert.equal(pageValue.revision?.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(pageValue.revision?.membershipDigest, selection.membershipDigest);
    assert.equal(pageValue.revision?.memberCount, selection.memberCount);
    const memberKeys = (pageValue.members ?? []).map(member => member.memberKey);
    assert(memberKeys.every(key => typeof key === 'string' && key) && new Set(memberKeys).size === selection.memberCount,
      'Immutable selection must retain one unique opaque member key per exact FHIR reference.');
    const refs = (pageValue.members ?? []).map(member => `${member.ref.project}/${member.ref.generation}/${member.ref.resourceType}/${member.ref.id}`).sort();
    const expected = selection.expectedIDs.map(id => `${project}/${generation}/Observation/${id}`).sort();
    assert.deepEqual(refs, expected, 'Immutable selection reread must exactly equal selected scoped FHIR IDs.');
    return refs;
  };
  const createSource = async ({ label, rows }) => {
    builder = await readBuilder();
    checkScope(builder);
    const rootNode = builder.catalog.nodes.find(node => node.resourceType === 'Observation' && node.rowRootEligible);
    const idCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
    assert.equal(idCandidates.length, 1);
    const prior = new Set(builder.workspace?.documents?.map(document => document.output.id) ?? []);
    await command([{ type: 'CREATE_TABLE', title: `CDA ${label} source`, rootNodeId: rootNode.nodeId }]);
    const outputId = builder.workspace.documents.find(document => !prior.has(document.output.id))?.output.id;
    assert(outputId);
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidates[0].candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);
    const expectedIDs = rows.map(row => row.id);
    const selection = await api(`${base}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `cda-group-pivot-join-${label}-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs: rows.map(row => ({
        project, generation, resourceType: 'Observation', id: row.id,
      })) } },
    });
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, 'Observation');
    assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(selection.memberCount, expectedIDs.length);
    selection.expectedIDs = expectedIDs;
    const selectionRefs = await verifySelection(selection);
    const routes = await api(`${authoring}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
    });
    const direct = routes.choices.find(choice => choice.route.length === 0);
    assert(direct, `${label} exact Observation selection needs a direct population route.`);
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
    const doc = getDocument(builder, outputId);
    assert.equal(doc.population?.selectionRevisionId, selection.id);
    assert.equal(doc.population?.route?.length ?? 0, 0);
    assert.equal(doc.columns.length, 1);
    assert.equal(doc.columns[0].source?.field?.path, 'id');
    reportCheck('correctness', `${label} immutable selection and direct population bind exact scoped FHIR IDs`, true, {
      outputId, selectionId: selection.id, selectionRefs, project, generation,
      scopeDigest: selection.scopeDigest, memberCount: selection.memberCount,
    });
    return { label, rows, outputId, selection, selectionRefs, rootResourceType: doc.rootResourceType };
  };
  const addFields = async (source, fieldPaths, { firstValuePaths = [] } = {}) => {
    await cda.navigate(uiURL);
    await waitSelector(`[data-testid="construction-table-${source.outputId}"]`);
    await selectTable(source.outputId, source.rows.length, `Select ${source.label} exact source population`);
    const add = page.getByTestId('construction-action-add-columns');
    await action(`Open ${source.label} raw-field picker`, add, target => target.click({ timeout: 5_000 }),
      async () => waitSelector('[aria-label="Add columns editor"]'));
    await cda.click('button', { name: 'Fields and related data' });
    await cda.click('summary', { name: 'Raw FHIR fields (advanced)' });
    for (const fieldPath of fieldPaths) {
      const selector = `input[type="checkbox"][aria-label=${JSON.stringify(`Select Observation.${fieldPath}`)}]`;
      await waitSelector(selector);
      await action(`Select Observation.${fieldPath} for ${source.label}`, page.locator(selector), target => target.check({ timeout: 5_000 }));
    }
    const n = fieldPaths.length;
    const addButtonText = `Add ${n} selected feature${n === 1 ? '' : 's'}`;
    const addSelected = page.getByRole('button', { name: addButtonText, exact: true });
    const selectionDialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
    assert(firstValuePaths.length === 0 ||
      (firstValuePaths.length === 1 && firstValuePaths[0] === 'code.coding[].code'),
    `The native multi-form chooser is only configured for the CDA coding path: ${JSON.stringify(firstValuePaths)}`);
    await action(`Add ${n} raw columns to ${source.label}`, addSelected, target => target.click({ timeout: 5_000 }),
      async () => firstValuePaths.length
        ? selectionDialog.waitFor({ state: 'visible', timeout: 5_000 })
        : waitFunction(`[...document.querySelectorAll('button')].some(button=>button.textContent.trim()==='Apply columns'&&!button.disabled)`));
    if (firstValuePaths.length) {
      const firstValue = selectionDialog.getByRole('radio', { name: 'Code Coding Code: Use the first value', exact: true });
      await action('Choose the first code coding value for Observation.code.coding[].code', firstValue,
        target => target.check({ timeout: 5_000 }), async () => assert(await firstValue.isChecked(),
          'The native construction chooser must have FIRST selected for Observation.code.coding[].code.'));
      const confirm = selectionDialog.getByRole('button', { name: `Add ${n} columns`, exact: true });
      await action(`Confirm ${n} raw columns with FIRST coding projection`, confirm,
        target => target.click({ timeout: 5_000 }), async () =>
          waitFunction(`[...document.querySelectorAll('button')].some(button=>button.textContent.trim()==='Apply columns'&&!button.disabled)`));
    }
    await action(`Apply ${source.label} raw columns`, page.getByRole('button', { name: 'Apply columns', exact: true }),
      target => target.click({ timeout: 5_000 }), async () => waitSaved(source.outputId, source.rows.length));
    const close = page.getByRole('button', { name: 'Close operation editor', exact: true });
    if (await close.count()) await action(`Return to ${source.label} table after adding raw columns`, close,
      target => target.click({ timeout: 5_000 }), async () => waitSaved(source.outputId, source.rows.length));
    builder = await readBuilder();
    const doc = getDocument(builder, source.outputId);
    const firstValuePathSet = new Set(firstValuePaths);
    const columns = Object.fromEntries(fieldPaths.map(path => {
      const expectedMode = firstValuePathSet.has(path) ? 'FIRST' : 'VALUE';
      const matches = doc.columns.filter(column => column.source?.field?.path === path && column.source.field.projectionMode === expectedMode);
      assert.equal(matches.length, 1, `Expected one exact direct Observation.${path} ${expectedMode} projection in ${source.label}.`);
      return [path, matches[0]];
    }));
    const grid = await readGrid('saved');
    const idIndex = grid.headers.findIndex(header => /\bid\b/i.test(header));
    assert(idIndex >= 0);
    assert.deepEqual(grid.rows.map(row => row[idIndex]).sort(), source.rows.map(row => row.id).sort(),
      `${source.label} must visibly retain exact selected FHIR IDs.`);
    for (const [path, column] of Object.entries(columns)) {
      const index = grid.headers.indexOf(column.label);
      assert(index >= 0, `${source.label} table omits ${path}.`);
      const expected = source.rows.map(row => path === 'subject.reference' ? row.subjectReference :
        path === 'code.coding[].code' ? row.codeCodingCodes[0] : row[path]);
      assert.deepEqual(grid.rows.map(row => row[index]).sort(), expected.map(String).sort(), `${source.label} raw ${path} cells differ.`);
    }
    return columns;
  };
  const keySelector = label => `input[type="checkbox"][aria-label=${JSON.stringify(`Group by ${label}`)}]`;
  const groupSource = async (source, columns, groupPaths) => {
    await cda.click('[data-testid="construction-rows-settings-trigger"]');
    await waitFunction("document.querySelector('[data-testid=construction-action-group-rows]')?.disabled===false");
    await action(`Open ${source.label} native Group editor`, page.getByTestId('construction-action-group-rows'),
      target => target.click({ timeout: 5_000 }), async () => waitSelector('select[aria-label="Summary 1"]'));
    assert.equal(await page.locator('select[aria-label="Summary 1"]').inputValue(), 'COUNT_ROWS');
    const keys = groupPaths.map(path => columns[path]);
    const groupBase = structuredClone(builder);
    let fromIndex;
    for (const key of keys) {
      const selector = keySelector(key.label);
      await waitSelector(selector);
      if (key === keys.at(-1)) fromIndex = cda.report.nativeRequests.length;
      await action(`Group ${source.label} by ${key.label}`, page.locator(selector), target => target.check({ timeout: 5_000 }));
    }
    const rawGroupMap = new Map();
    for (const row of source.rows) {
      const key = groupPaths.map(path => path === 'subject.reference' ? row.subjectReference :
        path === 'code.coding[].code' ? row.codeCodingCodes[0] : row[path]);
      const serialized = JSON.stringify(key);
      rawGroupMap.set(serialized, (rawGroupMap.get(serialized) ?? 0) + 1);
    }
    const expectedGroupRows = [...rawGroupMap].map(([key, count]) => [...JSON.parse(key), count])
      .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    const count = expectedGroupRows.length;
    await waitProposal(source.outputId, count);
    const proposal = await previewEvent({ fromIndex, outputId: source.outputId, baseState: groupBase,
      matchesCandidate: candidate => {
        const step = candidate?.steps?.at(-1);
        return step?.operation?.kind === 'GROUP' &&
          isDeepStrictEqual(step.operation.group.keys?.map(key => key.inputColumnId), keys.map(key => key.columnId)) &&
          step.operation.group.aggregates?.some(aggregate => aggregate.operation === 'COUNT_ROWS');
      } });
    const grid = await readGrid('proposal');
    const countLabel = grid.headers.find(header => /^row count$/i.test(header));
    assert(countLabel, `COUNT_ROWS proposal must expose its exact row-count column: ${JSON.stringify(grid.headers)}`);
    const expectedHeaders = [...keys.map(key => key.label), countLabel];
    assertGrid(`${source.label} native GROUP proposal matches raw group counts`, grid, expectedHeaders,
      expectedGroupRows.map(row => row.map(String)));
    const candidateStep = proposal.requestBody.candidateConstruction.steps.at(-1);
    assert.equal(candidateStep?.operation?.kind, 'GROUP');
    assert.equal(candidateStep.operation.group.aggregates?.length, 1);
    assert.equal(candidateStep.operation.group.aggregates[0].operation, 'COUNT_ROWS');
    assert.deepEqual(candidateStep.operation.group.keys.map(key => key.inputColumnId), keys.map(key => key.columnId));
    reportCheck('correctness', `${source.label} proposal carries exact direct Group keys and COUNT_ROWS`, true, {
      outputId: source.outputId, stepId: candidateStep.id, keys: candidateStep.operation.group.keys,
      aggregate: candidateStep.operation.group.aggregates[0], proposalId: proposal.responseBody.proposalId,
    });
    const button = page.getByTestId('construction-apply-proposal');
    await action(`Apply ${source.label} native COUNT_ROWS Group`, button, target => target.click({ timeout: 5_000 }),
      async () => waitSaved(source.outputId, count));
    builder = await readBuilder();
    const doc = getDocument(builder, source.outputId);
    const step = doc.construction?.steps?.at(-1);
    assert.equal(step?.operation?.kind, 'GROUP');
    assert.equal(step.operation.group.aggregates?.length, 1);
    const keyBindings = step.operation.group.keys;
    assert.equal(keyBindings.length, keys.length);
    keys.forEach((key, index) => assert.equal(keyBindings[index].inputColumnId, key.columnId));
    const keyOutputs = keys.map((key, index) => {
      const binding = keyBindings[index];
      return step.outputs.find(output => output.id === binding.outputColumnId);
    });
    const aggregate = step.operation.group.aggregates[0];
    assert.equal(aggregate.operation, 'COUNT_ROWS');
    const countOutput = step.outputs.find(output => output.id === aggregate.outputColumnId);
    assert(countOutput);
    await reloadTable(source.outputId, count, `Reload saved ${source.label} GROUP rows`);
    const savedGrid = await readGrid('saved');
    assertGrid(`${source.label} saved Group survives reload`, savedGrid,
      [...keyOutputs.map(key => key.label), countOutput.label], expectedGroupRows.map(row => row.map(String)));
    source.group = { step, stepId: step.id, keyOutputs, countOutput, expectedGroupRows, groupPaths };
    return source;
  };

  try {
    const oracleRows = prepareCdaGroupPivotJoinOracle(witness, { project, generation });
    report.oracle = { initialQuery, targetedQuery, exactReadQuery, scope: { project, generation, initialSampleLimit: scanLimit, targetedQueryLimit: scanLimit, maxSelectedSubjects: 10, maxReturnedRows: scanLimit * 2 }, witness: scanEvent, oracle: oracleRows };
    const leftSource = await createSource({ label: 'left Group', rows: witness.left });
    const leftColumns = await addFields(leftSource, ['subject.reference']);
    await groupSource(leftSource, leftColumns, ['subject.reference']);
    const leftGroupExact = isDeepStrictEqual(leftSource.group.expectedGroupRows, oracleRows.leftGroups);
    reportCheck('correctness', 'Left native Group produces the exact shared count 3 and left-only count 1',
      leftGroupExact, {
        outputId: leftSource.outputId, selectionId: leftSource.selection.id,
        expectedRows: oracleRows.leftGroups, actualRows: leftSource.group.expectedGroupRows,
      });
    assert(leftGroupExact, 'Left native COUNT_ROWS Group must match its exact raw oracle.');

    const rightSource = await createSource({ label: 'right Group Pivot', rows: witness.right });
    const rightColumns = await addFields(rightSource, ['subject.reference', 'code.coding[].code'], { firstValuePaths: ['code.coding[].code'] });
    await groupSource(rightSource, rightColumns, ['subject.reference', 'code.coding[].code']);
    const rightGroupExact = isDeepStrictEqual(rightSource.group.expectedGroupRows, oracleRows.rightGroups);
    reportCheck('correctness', 'Right native Group preserves subject/code-category multiplicity as two COUNT_ROWS groups',
      rightGroupExact, {
        outputId: rightSource.outputId, selectionId: rightSource.selection.id,
        expectedRows: oracleRows.rightGroups, actualRows: rightSource.group.expectedGroupRows,
      });
    assert(rightGroupExact, 'Right subject/code-category COUNT_ROWS Group must preserve exact duplicate-category multiplicity.');

    const rightOutputId = rightSource.outputId;
    const rightGroupBase = structuredClone(builder);
    const pivotCaptureFrom = cda.report.nativeRequests.length;
    const pivotStart = Date.now();
    await cda.click('[data-testid="construction-rows-settings-trigger"]');
    const explicitTablePivot = page.getByTestId('construction-action-table-pivot-rows');
    const explicitTablePivotCount = await explicitTablePivot.count();
    assert(explicitTablePivotCount <= 1, 'Rows settings must expose at most one explicit table-Pivot alternative.');
    const useExplicitTablePivot = explicitTablePivotCount === 1 && await explicitTablePivot.isVisible();
    const pivotEntry = useExplicitTablePivot ? explicitTablePivot : page.getByTestId('construction-action-pivot-rows');
    const pivotEntryTestId = useExplicitTablePivot ? 'construction-action-table-pivot-rows' : 'construction-action-pivot-rows';
    const pivotEntryRoute = useExplicitTablePivot ? 'explicit table-Pivot alternative' : 'main Pivot action';
    await waitFunction(`document.querySelector('[data-testid="${pivotEntryTestId}"]')?.disabled===false`);
    await action(`Open native field Pivot editor for right Group via ${pivotEntryRoute}`, pivotEntry,
      target => target.click({ timeout: 5_000 }), async () => waitSelector('select[aria-label="Pivot category field"]'));
    const subjectGroupOutput = rightSource.group.keyOutputs[0];
    const codeGroupOutput = rightSource.group.keyOutputs[1];
    const countOutput = rightSource.group.countOutput;
    const pivotGroupSelector = `input[type="checkbox"][aria-label=${JSON.stringify(`Pivot group ${subjectGroupOutput.label}`)}]`;
    await waitSelector(pivotGroupSelector);
    await action('Group right Pivot by exact subject output', page.locator(pivotGroupSelector), target => target.check({ timeout: 5_000 }));
    const chooseText = async (selector, text) => {
      const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
        value: option.value, text: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(), disabled: option.disabled,
      })));
      const matches = options.filter(option => option.text === text && !option.disabled);
      assert.equal(matches.length, 1, `Expected one enabled ${JSON.stringify(text)} option in ${selector}: ${JSON.stringify(options)}`);
      await action(`Choose ${text}`, page.locator(selector), target => target.selectOption(matches[0].value, { timeout: 5_000 }));
    };
    await chooseText('select[aria-label="Pivot category field"]', codeGroupOutput.label);
    const pivotValueSelector = 'select[aria-label="Pivot values field"]';
    if (await page.locator(pivotValueSelector).inputValue() !== countOutput.id) {
      await selectColumnById(pivotValueSelector, countOutput.id);
    }
    const categoryContainer = page.getByTestId('construction-reshape-pivot-categories');
    const requiredCategories = witness.categories.map(category => category);
    const summary = page.locator('summary').filter({ hasText: 'Change selected categories' });
    await summary.waitFor({ state: 'visible', timeout: 5_000 });
    assert.equal(await summary.count(), 1, 'Pivot editor must expose the category selection disclosure.');
    if (!(await summary.evaluate(element => element.parentElement.open))) {
      await action('Open exact Pivot categories', summary, target => target.click({ timeout: 5_000 }));
    }
    assert.equal(await categoryContainer.count(), 1, 'Pivot editor must expose one category container.');
    await categoryContainer.waitFor({ state: 'visible', timeout: 5_000 });
    const categoryInputs = await categoryContainer.locator('input[type="checkbox"][aria-label^="Include category "]').evaluateAll(inputs => inputs.map(input => ({
      label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled,
    })));
    assert.equal(categoryInputs.length, requiredCategories.length,
      'Pivot editor must render exactly the selected raw code categories.');
    assert.deepEqual(categoryInputs.map(option => option.label).sort(), requiredCategories.map(category => `Include category ${category}`).sort(),
    'Pivot editor must discover every exact selected raw code category.');
    for (const category of requiredCategories) {
      const label = `Include category ${category}`;
      const input = categoryContainer.locator(`input[aria-label=${JSON.stringify(label)}]`);
      assert.equal(await input.count(), 1, `Expected one native checkbox for ${label}.`);
      if (!await input.isChecked()) await action(`Include exact Pivot code category ${category}`, input, target => target.check({ timeout: 5_000 }));
    }
    await waitProposal(rightOutputId, oracleRows.rightPivot.length);
    const pivotProposal = await previewEvent({ fromIndex: pivotCaptureFrom, outputId: rightOutputId, baseState: rightGroupBase,
      matchesCandidate: candidate => {
        const step = candidate?.steps?.at(-1);
        const pivot = step?.operation?.pivot;
        return step?.operation?.kind === 'PIVOT' &&
          isDeepStrictEqual(pivot?.groupKeyIds, [subjectGroupOutput.id]) &&
          pivot?.categoryColumnId === codeGroupOutput.id && pivot?.valueColumnId === countOutput.id &&
          isDeepStrictEqual(pivot?.categories?.map(category => category.key?.string).sort(), [...requiredCategories].sort());
      } });
    const proposedPivot = pivotProposal.requestBody.candidateConstruction.steps.at(-1).operation.pivot;
    const proposedCategories = proposedPivot.categories.map(category => category.key.string);
    const rightCountByCategory = new Map(oracleRows.rightGroups.map(([, category, count]) => [category, count]));
    const expectedPivotHeaders = [subjectGroupOutput.label, ...proposedCategories];
    const expectedPivotRows = [[witness.sharedSubject, ...proposedCategories.map(category => rightCountByCategory.get(category))]
      .map(String)];
    assertGrid('Right native Pivot proposal preserves exact code category multiplicity 2 and 1',
      await readGrid('proposal'), expectedPivotHeaders, expectedPivotRows);
    report.cases.push({ name: 'right Group→Pivot category discovery and exact preview', elapsedMs: Date.now() - pivotStart });
    const proposedSteps = pivotProposal.requestBody.candidateConstruction.steps;
    assert.deepEqual(proposedSteps.map(step => step.operation.kind), ['GROUP', 'PIVOT']);
    const candidatePivot = proposedSteps[1].operation.pivot;
    assert.deepEqual(candidatePivot.groupKeyIds, [subjectGroupOutput.id]);
    assert.equal(candidatePivot.categoryColumnId, codeGroupOutput.id);
    assert.equal(candidatePivot.valueColumnId, countOutput.id);
    assert.deepEqual(candidatePivot.categories.map(category => category.key.string), proposedCategories);
    await action('Apply right Group→Pivot construction', page.getByTestId('construction-apply-proposal'),
      target => target.click({ timeout: 5_000 }), async () => waitSaved(rightOutputId, oracleRows.rightPivot.length));
    builder = await readBuilder();
    const rightDoc = getDocument(builder, rightOutputId);
    const rightSteps = rightDoc.construction?.steps ?? [];
    assert.deepEqual(rightSteps.map(step => step.operation.kind), ['GROUP', 'PIVOT']);
    const pivotStep = rightSteps[1];
    const pivot = pivotStep.operation.pivot;
    assert.deepEqual(pivot.groupKeyIds, [subjectGroupOutput.id]);
    assert.equal(pivot.categoryColumnId, codeGroupOutput.id);
    assert.equal(pivot.valueColumnId, countOutput.id);
    assert.deepEqual(pivot.categories.map(category => category.key.string), proposedCategories);
    const orderedPivotCategories = requiredCategories.map(category => pivot.categories.find(item => item.key.string === category));
    assert(orderedPivotCategories.every(category => category && pivotStep.outputs.some(output => output.id === category.outputColumnId)));
    await reloadTable(rightOutputId, oracleRows.rightPivot.length, 'Reload exact saved right Group→Pivot source');
    assertGrid('Right Group→Pivot rows survive reload with exact code category multiplicity', await readGrid('saved'),
      expectedPivotHeaders, expectedPivotRows);
    rightSource.pivot = {
      step: pivotStep, stepId: pivotStep.id, groupKeyOutput: pivotStep.outputs.find(output => output.id === subjectGroupOutput.id),
      categoryOutputs: orderedPivotCategories.map(category => pivotStep.outputs.find(output => output.id === category.outputColumnId)),
      categories: requiredCategories, expectedRows: oracleRows.rightPivot,
    };
    assert(rightSource.pivot.groupKeyOutput);
    const sourceShapes = [getDocument(builder, leftSource.outputId).construction.steps.map(step => step.operation.kind),
      rightDoc.construction.steps.map(step => step.operation.kind)];
    assert.deepEqual(sourceShapes, [['GROUP'], ['GROUP', 'PIVOT']]);
    reportCheck('persistence', 'Exact source outputs persist as one GROUP and one ordered GROUP→PIVOT', true, {
      sourceOutputIds: [leftSource.outputId, rightSource.outputId], sourceShapes,
      leftSelectionId: leftSource.selection.id, rightSelectionId: rightSource.selection.id,
    });

    const sources = [leftSource, rightSource];
    const headers = ['Left subject', 'Left rows', 'Right subject', ...rightSource.pivot.categoryOutputs.map(output => output.label)];
    const expectedValues = joinType => (joinType === 'LEFT' ? oracleRows.leftJoin : oracleRows.innerJoin)
      .map(row => row.map(display));
    const sourceColumnIDs = [leftSource.group.keyOutputs[0].id, leftSource.group.countOutput.id,
      rightSource.pivot.groupKeyOutput.id, ...rightSource.pivot.categoryOutputs.map(output => output.id)];
    const mappingNames = ['left_subject', 'left_rows', 'right_subject', ...rightSource.pivot.categoryOutputs.map((_, index) => `right_code_${index + 1}`)];
    const mappingLabels = headers;
    const mappings = [
      { inputIndex: 1, sourceColumnId: sourceColumnIDs[0] },
      { inputIndex: 1, sourceColumnId: sourceColumnIDs[1] },
      { inputIndex: 2, sourceColumnId: sourceColumnIDs[2] },
      ...sourceColumnIDs.slice(3).map(sourceColumnId => ({ inputIndex: 2, sourceColumnId })),
    ];
    assert.equal(mappings.length, 5);
    const createTarget = async () => {
      const before = await readBuilder();
      checkScope(before);
      const rootNodeIds = before.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible).map(node => node.nodeId);
      const prior = before.workspace.documents.map(document => document.output.id);
      const fromIndex = cda.report.nativeRequests.length;
      await action('Create current-draft empty Combine target', page.getByTestId('construction-action-combine'),
        target => target.click({ timeout: 5_000 }), async () => waitSelector('[data-testid="construction-combine-editor"]'));
      const event = await capture.waitFor(entry => entry.path === `${authoring}/commands` && entry.method === 'POST' &&
        entry.status === 200 && entry.completedAt && entry.body?.commands?.some(command => command.type === 'CREATE_TABLE'),
      { fromIndex, timeoutMs: 5_000 });
      const requestBody = capture.rawRequestBody(event);
      const responseBody = capture.rawResponseBody(event);
      const outputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]').getAttribute('data-output-id');
      const evidence = nativeCombineTargetBindingEvidence({ requestBody, responseStatus: event.status, response: responseBody,
        expectedRootNodeIds: rootNodeIds, expectedRootResourceType: 'Observation', previousOutputIds: prior, mountedOutputId: outputId });
      assert(evidence.ok, `Join target must be a new rooted empty workspace output: ${JSON.stringify(evidence)}`);
      builder = await readBuilder();
      checkScope(builder);
      const baselineDocument = structuredClone(getDocument(builder, evidence.outputId));
      assert.equal(baselineDocument.columns?.length, 0);
      assert.equal(baselineDocument.construction?.steps?.length ?? 0, 0);
      return { outputId: evidence.outputId, rootResourceType: 'Observation', baselineDocument, createBase: structuredClone(builder) };
    };
    const configureJoin = async (target, joinType, expectedRows) => {
      await action('Choose native KEY_JOIN construction', page.getByTestId('construction-combine-choice-key_join'),
        locator => locator.click({ timeout: 5_000 }), async () => waitSelector('select[aria-label="Input table 1"]'));
      for (let index = 0; index < sources.length; index += 1) {
        const selector = `select[aria-label="Input table ${index + 1}"]`;
        const value = await outputOption(selector, sources[index].outputId);
        await action(`Bind Join input ${index + 1} to current-draft output`, page.locator(selector),
          locator => locator.selectOption(value, { timeout: 5_000 }));
      }
      await selectColumnById('select[aria-label="Matching pair 1 first field"]', sourceColumnIDs[0]);
      await selectColumnById('select[aria-label="Matching pair 1 second field"]', sourceColumnIDs[2]);
      const policy = 'select[aria-label="If a row in the first table has no match"]';
      await action(`Set ${joinType} Join policy`, page.locator(policy), locator => locator.selectOption(joinType, { timeout: 5_000 }));
      for (let index = 0; index < mappings.length; index += 1) {
        const field = index + 1;
        const nameSelector = `input[aria-label="Output field ${field} name"]`;
        await action(`Add Join output field ${field}`, page.getByRole('button', { name: 'Add output field' }),
          locator => locator.click({ timeout: 5_000 }), async () => waitSelector(nameSelector));
        for (const [kind, value] of [['name', mappingNames[index]], ['label', mappingLabels[index]]]) {
          const selector = `input[aria-label="Output field ${field} ${kind}"]`;
          if (kind !== 'name') await waitSelector(selector);
          await action(`Set Join field ${field} ${kind}`, page.locator(selector), locator => locator.fill(value, { timeout: 5_000 }));
        }
        const selector = `select[aria-label="Output field ${field} source field in input ${mappings[index].inputIndex}"]`;
        await waitSelector(selector);
        if (index < mappings.length - 1) await selectColumnById(selector, mappings[index].sourceColumnId);
        else mappings[index].finalSelector = selector;
      }
      const final = mappings.at(-1);
      const options = await page.locator(final.finalSelector).evaluate(select => [...select.options].map(option => ({ value: option.value, disabled: option.disabled })));
      const choice = options.filter(option => option.value === final.sourceColumnId && !option.disabled);
      assert.equal(choice.length, 1, 'Final projected field must be the exact last Group→Pivot category column.');
      const baseState = structuredClone(await readBuilder());
      const fromIndex = cda.report.nativeRequests.length;
      const startedAt = Date.now();
      await action(`Complete native ${joinType} Group/Pivot Join mapping`, page.locator(final.finalSelector),
        locator => locator.selectOption(choice[0].value, { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedRows.length));
      const captured = await previewEvent({ fromIndex, outputId: target.outputId, baseState });
      const rendered = await readGrid('proposal');
      assertGrid(`${joinType} Join preview matches exact Group/Pivot oracle`, rendered, headers, expectedRows);
      const step = captured.requestBody.candidateConstruction.steps.at(-1);
      const combine = step?.operation?.combine;
      assert.equal(step?.operation?.kind, 'COMBINE');
      assert.equal(combine?.kind, 'KEY_JOIN');
      assert.equal(combine?.joinType, joinType);
      assert.deepEqual(step.inputs, sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId })));
      assert.equal(currentDraftSourceEvidence({ inputs: step.inputs, expectedOutputIDs: sources.map(source => source.outputId),
        sourceDocuments: baseState.workspace.documents }).ok, true);
      assert.deepEqual(combine.keys, [{ leftColumnId: sourceColumnIDs[0], rightColumnId: sourceColumnIDs[2] }]);
      const expectedProjection = sourceColumnIDs.map((sourceColumnId, index) => ({
        outputColumnId: step.outputs[index]?.id, inputIndex: mappings[index].inputIndex - 1, inputColumnId: sourceColumnId,
      }));
      assert.deepEqual(step.outputs.map(output => ({ name: output.name, label: output.label })),
        mappingNames.map((name, index) => ({ name, label: mappingLabels[index] })));
      assert.deepEqual(combine.projections.map(item => ({ outputColumnId: item.outputColumnId, inputIndex: item.inputIndex, inputColumnId: item.inputColumnId })), expectedProjection);
      assert(step.inputs.every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId));
      const allWorkspaceInputs = baseState.workspace.documents.flatMap(document => (document.construction?.steps ?? [])
        .flatMap(sourceStep => sourceStep.inputs ?? []));
      assert(allWorkspaceInputs.every(input => input.kind !== 'TABLE_REVISION'));
      const proposal = captured.responseBody;
      assert.equal(proposal.preview?.rowCount, expectedRows.length);
      reportCheck('correctness', `${joinType} native Join binds exact Group/Pivot inputs, keys, mappings, and current CAS`, true, {
        outputId: target.outputId, joinType, inputs: step.inputs, key: combine.keys, projections: combine.projections,
        draftVersion: baseState.draftVersion, draftDigest: baseState.draftDigest, proposalId: proposal.proposalId,
      });
      report.cases.push({ name: `${joinType} Group/Pivot Join proposal and exact visible preview`, elapsedMs: Date.now() - startedAt });
      return { target, baseState, stepId: step.id, expectedRows, captured };
    };
    const applyJoin = async (target, rows, name, baseState) => {
      await action(name, page.getByTestId('construction-apply-proposal'), targetLocator => targetLocator.click({ timeout: 5_000 }),
        async () => waitSaved(target.outputId, rows.length));
      builder = await readBuilder();
      checkScope(builder);
      const casAdvanced = builder.draftVersion > baseState.draftVersion && builder.draftDigest !== baseState.draftDigest;
      assert(casAdvanced, `${name} must advance draft version and digest on Apply.`);
      await reloadTable(target.outputId, rows.length, `Reload ${name}`);
      const rendered = await readGrid('saved');
      assertGrid(`${name} persists exact visible values`, rendered, headers, rows, {
        draftCAS: { beforeVersion: baseState.draftVersion, beforeDigest: baseState.draftDigest,
          afterVersion: builder.draftVersion, afterDigest: builder.draftDigest, advanced: casAdvanced },
      });
      return builder;
    };
    const cancelJoin = async (baseline, target, expectedRows, name) => {
      await action(name, page.getByTestId('construction-cancel-proposal'), targetLocator => targetLocator.click({ timeout: 5_000 }),
        async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-combine-editor"]')`));
      await reloadTable(target.outputId, expectedRows?.length, `${name} reload target after cancel`);
      builder = await readBuilder();
      const evidence = canceledDraftEvidence(baseline, builder);
      assert(evidence.ok, `${name} changed full workspace or CAS: ${JSON.stringify(evidence)}`);
      const restoredTarget = getDocument(builder, target.outputId);
      assert.deepEqual(restoredTarget, getDocument(baseline, target.outputId));
      if (expectedRows?.length) assertGrid(`${name} preserves existing target rows`, await readGrid('saved'), headers, expectedRows);
      return evidence;
    };
    const persistJoin = (state, target, type, stepId) => {
      const doc = getDocument(state, target.outputId);
      const step = doc.construction?.steps?.at(-1);
      const combine = step?.operation?.combine;
      const outputByName = new Map((step?.outputs ?? []).map(output => [output.name, output]));
      const expectedInputs = sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId }));
      const expectedProjections = sourceColumnIDs.map((sourceColumnId, index) => [
        outputByName.get(mappingNames[index])?.id, mappings[index].inputIndex - 1, sourceColumnId,
      ]);
      return {
        step, combine,
        exact: doc.rootResourceType === 'Observation' && (doc.construction?.steps ?? []).length === 1 &&
          step?.id === stepId && step.operation.kind === 'COMBINE' && combine?.kind === 'KEY_JOIN' && combine.joinType === type &&
          isDeepStrictEqual(step.inputs, expectedInputs) &&
          currentDraftSourceEvidence({ inputs: step.inputs, expectedOutputIDs: sources.map(source => source.outputId),
            sourceDocuments: state.workspace.documents }).ok &&
          isDeepStrictEqual(step.outputs.map(output => ({ name: output.name, label: output.label })),
            mappingNames.map((name, index) => ({ name, label: mappingLabels[index] }))) &&
          isDeepStrictEqual(combine.keys, [{ leftColumnId: sourceColumnIDs[0], rightColumnId: sourceColumnIDs[2] }]) &&
          isDeepStrictEqual(combine.projections.map(item => [item.outputColumnId, item.inputIndex, item.inputColumnId]), expectedProjections),
      };
    };
    const beginEdit = async (target, stepId, rowCount) => {
      await selectTable(target.outputId, rowCount, 'Select saved Group/Pivot Join before edit');
      const editAction = page.getByTestId(`construction-edit-step-${stepId}`);
      await action('Open saved Join history step', page.getByTestId(`construction-history-step-${stepId}`),
        locator => locator.click({ timeout: 5_000 }), async () => waitEnabled(editAction));
      await action('Edit saved Group/Pivot Join', editAction,
        locator => locator.click({ timeout: 5_000 }), async () => waitSelector('[data-testid="construction-combine-editor"]'));
    };
    const removeProposal = async (target, stepId, expectedRows) => {
      await selectTable(target.outputId, expectedRows.length, 'Select saved INNER Join for removal');
      const removeAction = page.getByTestId(`construction-remove-step-${stepId}`);
      await action('Open saved Join history for removal', page.getByTestId(`construction-history-step-${stepId}`),
        locator => locator.click({ timeout: 5_000 }), async () => waitEnabled(removeAction));
      const baseState = structuredClone(builder);
      const fromIndex = cda.report.nativeRequests.length;
      await action('Preview Group/Pivot Join removal', removeAction,
        locator => locator.click({ timeout: 5_000 }), async () => waitProposal(target.outputId, 0));
      const response = await previewEvent({ fromIndex, outputId: target.outputId, baseState });
      assert.equal(response.responseBody.candidateConstruction.steps.some(step => step.operation.kind === 'COMBINE'), false);
      const candidateRestoration = rootedEmptyTargetRestorationEvidence({
        ...structuredClone(target.baselineDocument), construction: response.responseBody.candidateConstruction,
      }, target.baselineDocument, target);
      assert(candidateRestoration.ok, `Removed Join candidate must be the exact empty rooted target: ${JSON.stringify(candidateRestoration)}`);
      return response;
    };

    const expectedLeft = expectedValues('LEFT');
    const expectedInner = expectedValues('INNER');
    assert.equal(expectedLeft.length, 2);
    assert.equal(expectedInner.length, 1);
    const left = await createTarget();
    const firstJoin = await configureJoin(left, 'LEFT', expectedLeft);
    const canceled = await cancelJoin(firstJoin.baseState, left, undefined, 'Cancel initial Group/Pivot LEFT Join proposal');
    reportCheck('persistence', 'Join proposal Cancel preserves the exact full workspace and draft CAS after reload', canceled.ok, canceled);

    const target = await createTarget();
    const leftJoin = await configureJoin(target, 'LEFT', expectedLeft);
    builder = await applyJoin(target, expectedLeft, 'Apply native Group/Pivot LEFT Join', leftJoin.baseState);
    const savedLeft = persistJoin(builder, target, 'LEFT', leftJoin.stepId);
    assert(savedLeft.exact);
    const appliedLeftBase = structuredClone(builder);

    await beginEdit(target, savedLeft.step.id, expectedLeft.length);
    const editPolicy = 'select[aria-label="If a row in the first table has no match"]';
    const canceledEditBase = structuredClone(builder);
    const editFrom = cda.report.nativeRequests.length;
    const startedAt = Date.now();
    await action('Preview saved LEFT Join edited to INNER before Cancel', page.locator(editPolicy),
      locator => locator.selectOption('INNER', { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedInner.length));
    const canceledEditProposal = await previewEvent({ fromIndex: editFrom, outputId: target.outputId, baseState: canceledEditBase });
    assertGrid('Edited INNER proposal matches exact shared-key rows', await readGrid('proposal'), headers, expectedInner);
    assert.equal(canceledEditProposal.requestBody.candidateConstruction.steps.at(-1).id, savedLeft.step.id);
    report.cases.push({ name: 'saved LEFT-to-INNER edit preview before Cancel', elapsedMs: Date.now() - startedAt });
    await cancelJoin(canceledEditBase, target, expectedLeft, 'Cancel saved LEFT Join edit to INNER');
    builder = await readBuilder();
    assert(persistJoin(builder, target, 'LEFT', savedLeft.step.id).exact);
    await reloadTable(target.outputId, expectedLeft.length, 'Reload saved LEFT after canceled INNER edit');
    assertGrid('Canceled INNER edit retains LEFT values', await readGrid('saved'), headers, expectedLeft);

    await beginEdit(target, savedLeft.step.id, expectedLeft.length);
    const applyEditBase = structuredClone(builder);
    const applyEditFrom = cda.report.nativeRequests.length;
    await action('Preview saved LEFT Join edited to INNER for Apply', page.locator(editPolicy),
      locator => locator.selectOption('INNER', { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedInner.length));
    const applyEditProposal = await previewEvent({ fromIndex: applyEditFrom, outputId: target.outputId, baseState: applyEditBase });
    assertGrid('INNER Join proposal matches exact shared-key rows', await readGrid('proposal'), headers, expectedInner);
    assert.equal(applyEditProposal.requestBody.candidateConstruction.steps.at(-1).id, savedLeft.step.id);
    builder = await applyJoin(target, expectedInner, 'Apply saved LEFT Join edit as INNER', applyEditBase);
    const savedInner = persistJoin(builder, target, 'INNER', savedLeft.step.id);
    assert(savedInner.exact);
    assert(builder.draftVersion > applyEditBase.draftVersion && builder.draftDigest !== applyEditBase.draftDigest);

    const removalBase = structuredClone(builder);
    await removeProposal(target, savedInner.step.id, expectedInner);
    await cancelJoin(removalBase, target, expectedInner, 'Cancel INNER Join removal');
    builder = await readBuilder();
    assert(persistJoin(builder, target, 'INNER', savedInner.step.id).exact);
    assertGrid('Cancel removal preserves saved INNER values', await readGrid('saved'), headers, expectedInner);

    const removalApplyBase = structuredClone(builder);
    await removeProposal(target, savedInner.step.id, expectedInner);
    await action('Apply Group/Pivot INNER Join removal and restore empty target', page.getByTestId('construction-apply-proposal'),
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(rootedEmptyTargetAppliedExpression(target.outputId)));
    await reloadTable(target.outputId, undefined, 'Reload removed Group/Pivot Join and select empty target');
    builder = await readBuilder();
    checkScope(builder);
    const restored = getDocument(builder, target.outputId);
    const restoreEvidence = rootedEmptyTargetRestorationEvidence(restored, target.baselineDocument, target);
    assert(restoreEvidence.ok);
    const removalCasAdvanced = builder.draftVersion > removalApplyBase.draftVersion &&
      builder.draftDigest !== removalApplyBase.draftDigest;
    assert(removalCasAdvanced, 'Join removal Apply must advance draft version and digest.');
    const leftAfter = getDocument(builder, leftSource.outputId);
    const rightAfter = getDocument(builder, rightSource.outputId);
    assert.deepEqual(leftAfter, getDocument(appliedLeftBase, leftSource.outputId));
    assert.deepEqual(rightAfter, getDocument(appliedLeftBase, rightSource.outputId));
    assert.equal(leftAfter.population.selectionRevisionId, leftSource.selection.id);
    assert.equal(rightAfter.population.selectionRevisionId, rightSource.selection.id);
    assert.deepEqual(rightAfter.construction.steps.map(step => step.operation.kind), ['GROUP', 'PIVOT']);
    reportCheck('persistence', 'Join removal Apply restores exact empty target and preserves scoped Group/Pivot sources', removalCasAdvanced, {
      targetId: target.outputId, restoreEvidence,
      draftCAS: { beforeVersion: removalApplyBase.draftVersion, beforeDigest: removalApplyBase.draftDigest,
        afterVersion: builder.draftVersion, afterDigest: builder.draftDigest, advanced: removalCasAdvanced },
      leftSelectionId: leftAfter.population.selectionRevisionId, rightSelectionId: rightAfter.population.selectionRevisionId,
      leftSteps: leftAfter.construction.steps.map(step => step.operation.kind),
      rightSteps: rightAfter.construction.steps.map(step => step.operation.kind),
    });

    const final = await readBuilder();
    const allInputs = final.workspace.documents.flatMap(document => (document.construction?.steps ?? [])
      .flatMap(step => step.inputs ?? []));
    const noPublishOrPinnedInput = publishRequests === 0 && allInputs.every(input => input.kind !== 'TABLE_REVISION' && !input.tableId && !input.revisionId);
    reportCheck('correctness', 'CDA Group/Pivot/Join lifecycle never publishes or pins TABLE_REVISION inputs', noPublishOrPinnedInput, {
      publishRequests, inputKinds: allInputs.map(input => input.kind), project, generation, explorer,
    });
    assert(noPublishOrPinnedInput);
    const failedActions = (report.actions ?? []).filter(record => record.status !== 'passed' || record.elapsedMs > 5_000);
    reportCheck('performance', 'All native Group/Pivot/Join browser actions remain within five seconds', failedActions.length === 0, {
      actionCount: (report.actions ?? []).length, failedActions,
      customTransitions: report.cases,
    });
    assert.equal(failedActions.length, 0);
    report.status = 'passed';
  } finally {
    page.off('request', onRequest);
    page.off('response', onSelectionResponse);
    page.off('requestfinished', onSelectionRequestFinished);
    page.off('requestfailed', onSelectionRequestFailed);
    await capture.flush();
    report.nativeRequestCount = cda.report.nativeRequests.length;
    report.finished = new Date().toISOString();
    await cda.attachReport('group-pivot-join', report);
  }
}
