import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { scenarioCaseFor } from '../registry.mjs';

const CASE_ID = 'cda-starting-collection-handoff';
const CASE_NAME = 'initial-selection-handoff-route-preview-apply-reload';
const ACTION_BUDGET_MS = 5_000;

export const startingCollectionPreviewMatchesExpected = ([expectedIDs]) => {
  const expected = [...expectedIDs].sort();
  const preview = document.querySelector('[data-testid="preview-table-scroll"]');
  const table = preview?.querySelector('[role="table"]');
  if (!preview || !table || !preview.getClientRects().length) return false;
  if (preview.innerText.includes('Loading your table…') || preview.innerText.includes('Preview did not complete')) return false;
  const headers = [...table.querySelectorAll('[role="columnheader"]')]
    .map(header => String(header.textContent ?? '').replace(/\s+/g, ' ').trim());
  const patientColumn = headers.indexOf('Patient ID');
  if (patientColumn < 0) return false;
  const visibleIDs = [...table.querySelectorAll('[role="row"]')]
    .slice(1)
    .map(row => row.querySelectorAll('[role="cell"]')[patientColumn]?.innerText.trim())
    .filter(Boolean)
    .sort();
  return Number(table.getAttribute('aria-rowcount')) === expected.length + 1 &&
    JSON.stringify(visibleIDs) === JSON.stringify(expected);
};

export const startingCollectionVisiblePreviewSnapshot = () => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  const headers = [...(table?.querySelectorAll('[role="columnheader"]') ?? [])]
    .map(header => String(header.textContent ?? '').replace(/\s+/g, ' ').trim());
  const patientColumn = headers.indexOf('Patient ID');
  const ids = [...(table?.querySelectorAll('[role="row"]') ?? [])]
    .slice(1)
    .map(row => row.querySelectorAll('[role="cell"]')[patientColumn]?.innerText.trim())
    .filter(Boolean)
    .sort();
  return {
    rowCount: Math.max(0, Number(table?.getAttribute('aria-rowcount') ?? 0) - 1),
    headers,
    patientIDs: ids,
  };
};

const compareByID = (left, right) => left.id.localeCompare(right.id);

const savedRouteForChoice = choice => choice.route.map(step => ({
  resourceType: step.toResourceType,
  relationship: step.relationship,
  catalogEdgeId: step.edgeId,
  storageDirection: step.storageDirection,
}));

export const startingCollectionReconcileReceiptEvidence = ({ request, requestBody, response, expected }) => {
  const failures = [];
  if (request?.method !== 'POST' || request.path !== expected.path || request.status !== 200) {
    failures.push('reconcile must be the successful native POST for this Explorer');
  }
  if (requestBody?.snapshotToken !== expected.snapshotToken) failures.push('reconcile request snapshot differs from the saved draft');
  if (requestBody?.draftVersion !== expected.draftVersion) failures.push('reconcile request draft version differs from the saved draft');
  if (requestBody?.draftDigest !== expected.draftDigest) failures.push('reconcile request draft digest differs from the saved draft');
  if (response?.kind !== 'ExplorerBuilderReceipt') failures.push('reconcile response is not a Builder receipt');
  if (typeof response?.receiptId !== 'string' || response.receiptId.length === 0) failures.push('reconcile response has no receipt ID');
  if (response?.snapshotToken !== expected.snapshotToken) failures.push('reconcile receipt snapshot differs from the saved draft');
  if (response?.generation !== expected.generation) failures.push('reconcile receipt generation differs from the saved draft');
  if (response?.authorizationScopeDigest !== expected.authorizationScopeDigest) failures.push('reconcile receipt authorization scope differs from the saved draft');
  const matchingOutputs = Array.isArray(response?.outputs)
    ? response.outputs.filter(output => output?.outputId === expected.outputId).length
    : 0;
  if (matchingOutputs !== 1) failures.push('reconcile receipt does not contain exactly one matching output');
  const savedDocuments = Array.isArray(expected.savedWorkspace?.documents)
    ? expected.savedWorkspace.documents.filter(document => document?.output?.id === expected.outputId)
    : [];
  const receiptDocuments = Array.isArray(response?.builder?.documents)
    ? response.builder.documents.filter(document => document?.output?.id === expected.outputId)
    : [];
  if (savedDocuments.length !== 1 || receiptDocuments.length !== 1) {
    failures.push('reconcile receipt and saved workspace must each contain the exact output document once');
  } else {
    if (!isDeepStrictEqual(receiptDocuments[0].population, savedDocuments[0].population)) {
      failures.push('reconcile receipt population differs from the persisted output document');
    }
    if (!isDeepStrictEqual(receiptDocuments[0].route, savedDocuments[0].route)) {
      failures.push('reconcile receipt route differs from the persisted output document');
    }
  }
  return { ok: failures.length === 0, failures, receiptId: response?.receiptId };
};

export const startingCollectionReconcileRequestMatchesSavedDraft = ({ request, requestBody, expected }) =>
  request?.method === 'POST' && request.path === expected.path && request.status === 200 &&
  requestBody?.snapshotToken === expected.snapshotToken &&
  requestBody?.draftVersion === expected.draftVersion &&
  requestBody?.draftDigest === expected.draftDigest;

export const startingCollectionPreviewRequestMatchesReceipt = ({ request, requestBody, expected }) =>
  request?.method === 'POST' && request.path === expected.path && request.status === 200 &&
  requestBody?.receiptId === expected.receiptId && requestBody?.outputId === expected.outputId;

export const startingCollectionNativePreviewReceiptEvidence = ({ request, requestBody, response, expected }) => {
  const failures = [];
  if (request?.method !== 'POST' || request.path !== expected.path || request.status !== 200) {
    failures.push('preview must be the successful native POST for this Explorer');
  }
  if (requestBody?.receiptId !== expected.receiptId) failures.push('preview request receipt differs from the exact reconcile receipt');
  if (requestBody?.outputId !== expected.outputId) failures.push('preview request output differs from the saved output');
  if (response?.receiptId !== expected.receiptId) failures.push('preview response receipt differs from the exact reconcile receipt');
  if (response?.outputId !== expected.outputId) failures.push('preview response output differs from the saved output');
  return { ok: failures.length === 0, failures };
};

export const startingCollectionRenderedPreviewDraftEvidence = ({ preview, expected }) => {
  const failures = [];
  if (preview?.status !== 'ready') failures.push('the visible preview is not ready');
  if (preview?.receiptId !== expected.receiptId) failures.push('visible preview receipt differs from the exact reconcile receipt');
  if (preview?.outputId !== expected.outputId) failures.push('visible preview output differs from the saved output');
  if (preview?.draftVersion !== String(expected.draftVersion)) failures.push('visible preview draft version differs from the saved draft');
  if (preview?.draftDigest !== expected.draftDigest) failures.push('visible preview draft digest differs from the saved draft');
  return { ok: failures.length === 0, failures };
};

export async function waitForStartingCollectionConfigurationRequests({
  browserEvents,
  explorerPath,
  selectionId,
  outputId,
  fromIndex,
  startedAt,
  now = Date.now,
  timeoutMs = ACTION_BUDGET_MS,
}) {
  const deadline = startedAt + timeoutMs;
  const waitForOwnedRequest = async (method, path, label, matchesRequest = () => true) => {
    const remainingMs = deadline - now();
    assert(remainingMs > 0, `Configure rows exceeded its ${timeoutMs} ms request-completion deadline before ${label}`);
    const request = await browserEvents.waitFor(entry =>
      entry.method === method && entry.path === path && entry.startedAt >= startedAt &&
        entry.triggerAction === 'Configure rows' && matchesRequest(entry),
    { fromIndex, timeoutMs: remainingMs });
    assert(Number.isFinite(request.completedAt) && request.completedAt <= deadline,
      `Configure rows ${label} did not reach captured terminal completion before its ${timeoutMs} ms deadline`);
    assert.equal(request.failure, undefined,
      `Configure rows ${label} failed: ${request.failure ?? 'request failure was not recorded'}`);
    assert(Number.isInteger(request.status) && request.status >= 200 && request.status < 300,
      `Configure rows ${label} returned HTTP ${request.status ?? 'without a successful status'}`);
    return request;
  };

  const selectionPath = `${explorerPath}/selections/${encodeURIComponent(selectionId)}`;
  const populationRoutesPath = `${explorerPath}/authoring/v2/population-routes`;
  const selectionRead = await waitForOwnedRequest('GET', selectionPath, 'exact selection revision GET',
    entry => entry.query?.limit === '100');
  const populationRoutes = await waitForOwnedRequest('POST', populationRoutesPath, 'population-routes POST',
    entry => entry.body?.selectionRevisionId === selectionId && entry.body?.outputId === outputId);
  return { selectionRead, populationRoutes, deadline };
}

export async function startingCollectionHandoffWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.target.fixtureGeneration ?? cda.target.generation;
  const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
  const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
  const arangoContainer = cda.target.arangoContainer;
  const arangoDatabase = process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev';
  const explorerId = `selection-handoff-${randomUUID()}`;
  const explorerRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const explorerPath = `${explorerRoot}/${encodeURIComponent(explorerId)}`;
  const authoringPath = `${explorerPath}/authoring/v2`;
  const selectionPath = `${explorerPath}/selections`;
  const requiredChecks = scenarioCaseFor(CASE_ID, CASE_NAME).requiredChecks;
  assert.deepEqual(requiredChecks, cda.report.requiredChecks,
    'CDA fixture report must use the registered starting-collection handoff contract');
  assert(project && generation && arangoContainer, 'The handoff case needs the owned CDA project, generation, and Arango container');

  const report = Object.assign(cda.report, {
    explorerId,
    source: 'standalone-demo selection query parameter',
    selectionQueryParameter: 'selection',
    apiRequests: [],
    lifecycle: {},
  });
  const checkpointDurations = [];
  let builder;
  let outputId;
  let selection;
  let browserEvents;
  let expectedPatientIDs = [];
  let fatal;

  const recordRequirement = (index, dimension, passed, evidence = {}) => {
    const name = requiredChecks[index];
    assert(name, `Registered starting-collection handoff check ${index} is missing`);
    cda.check(dimension, name, passed, evidence);
  };

  const api = async (path, body) => {
    const method = body === undefined ? 'GET' : 'POST';
    const requestId = `selection-handoff-${randomUUID()}`;
    const started = Date.now();
    const response = await fetch(`${apiOrigin}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30_000),
    });
    let value;
    try {
      value = await response.json();
    } catch {
      value = undefined;
    }
    report.apiRequests.push({ method, path, status: response.status, durationMs: Date.now() - started });
    assert(response.ok, `${method} ${path} returned ${response.status}: ${JSON.stringify(value)}`);
    return value;
  };

  const rawCdaQuery = query => {
    const result = spawnSync('rtk', [
      'proxy', 'docker', 'exec', arangoContainer, 'arangosh',
      '--server.database', arangoDatabase,
      '--javascript.execute-string',
      `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
    ], { encoding: 'utf8', timeout: 30_000 });
    assert.equal(result.status, 0, result.stderr || result.stdout);
    const payloadStart = result.stdout.indexOf('[');
    assert(payloadStart >= 0, 'Raw CDA oracle returned no JSON array');
    return JSON.parse(result.stdout.slice(payloadStart));
  };

  const runCommands = async commands => {
    await api(`${authoringPath}/commands`, {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await api(`${authoringPath}/builder`);
  };

  const wait = (callback, args = [], timeout = ACTION_BUDGET_MS) => cda.wait(callback, args, timeout);
  const addCheckpoint = (name, durationMs) => {
    checkpointDurations.push({ name, durationMs });
    assert(durationMs <= ACTION_BUDGET_MS, `${name} took ${durationMs} ms; CDA checkpoints must stay within ${ACTION_BUDGET_MS} ms`);
  };

  const waitForPreview = async patientIDs => {
    const expected = [...patientIDs].sort();
    await wait(startingCollectionPreviewMatchesExpected, [expected]);
    return cda.inspect(startingCollectionVisiblePreviewSnapshot);
  };

  const openStartingCollection = async (selectionId, outputSelector) => {
    await wait(([selector]) => Boolean(document.querySelector(selector)), [outputSelector]);
    await cda.click(outputSelector);
    await wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false);
    const configureRowsStartedAt = Date.now();
    const configureRowsRequestFromIndex = cda.report.nativeRequests.length;
    const remainingConfigureRowsMs = () => {
      const remainingMs = configureRowsStartedAt + ACTION_BUDGET_MS - Date.now();
      assert(remainingMs > 0,
        `Configure rows exceeded its ${ACTION_BUDGET_MS} ms deadline before the starting collection was ready`);
      return remainingMs;
    };
    let requests;
    const settingsTrigger = page.getByTestId('construction-rows-settings-trigger');
    await cda.action('Configure rows', settingsTrigger,
      locator => locator.click({ timeout: remainingConfigureRowsMs() }), {
        timeout: remainingConfigureRowsMs(),
        after: async () => {
          await wait(([id]) => {
            const panel = document.querySelector('section[aria-label="Starting collection"]');
            return panel?.getAttribute('data-selection-revision-id') === id &&
              !panel.innerText.includes('Loading the saved selection…');
          }, [selectionId], remainingConfigureRowsMs());
          requests = await waitForStartingCollectionConfigurationRequests({
            browserEvents,
            explorerPath,
            selectionId,
            outputId,
            fromIndex: configureRowsRequestFromIndex,
            startedAt: configureRowsStartedAt,
          });
        },
      });
    const elapsedMs = Date.now() - configureRowsStartedAt;
    addCheckpoint('Configure rows to captured starting-collection requests', elapsedMs);
    (report.lifecycle.configureRows ??= []).push({
      selectionRevisionId: selectionId,
      startedAt: configureRowsStartedAt,
      deadline: requests.deadline,
      elapsedMs,
      requests: [requests.selectionRead, requests.populationRoutes].map(request => ({
        browserRequestId: request.browserRequestId,
        requestId: request.requestId,
        method: request.method,
        path: request.path,
        status: request.status,
        startedAt: request.startedAt,
        completedAt: request.completedAt,
        responseBodyCaptured: browserEvents.rawResponseBody(request) !== undefined,
      })),
    });
  };

  try {
    const rawOracleQuery = `FOR observation IN Observation FILTER observation.resourceType == "Observation" AND observation.project == ${JSON.stringify(project)} AND observation.dataset_generation == ${JSON.stringify(generation)} SORT observation.id LIMIT 4000 FOR edge IN fhir_edge FILTER edge._from == observation._id AND edge.from_type == "Observation" AND edge.to_type == "Patient" AND edge.label == "subject_Patient" AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)} LET patient = DOCUMENT(edge._to) FILTER patient != null AND patient.resourceType == "Patient" AND patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == ${JSON.stringify(generation)} LIMIT 4000 RETURN DISTINCT { observationId: observation.id, patientId: patient.id }`;
    const rawWitnesses = rawCdaQuery(rawOracleQuery);
    const selectedWitnesses = [];
    const selectedObservationIDs = new Set();
    const selectedPatientIDs = new Set();
    for (const witness of rawWitnesses) {
      if (!witness.observationId || !witness.patientId || selectedObservationIDs.has(witness.observationId) || selectedPatientIDs.has(witness.patientId)) continue;
      selectedWitnesses.push(witness);
      selectedObservationIDs.add(witness.observationId);
      selectedPatientIDs.add(witness.patientId);
      if (selectedWitnesses.length === 2) break;
    }
    assert.equal(selectedWitnesses.length, 2,
      `Bounded raw CDA oracle must find two Observations with distinct scoped Patient subjects; witnesses=${JSON.stringify(rawWitnesses.slice(0, 20))}`);
    expectedPatientIDs = selectedWitnesses.map(witness => witness.patientId).sort();
    report.rawOracle = {
      generation,
      observationIDs: selectedWitnesses.map(witness => witness.observationId).sort(),
      patientIDs: expectedPatientIDs,
      witnessLimit: 4000,
      relationship: 'Observation -[subject_Patient]-> Patient',
    };
    recordRequirement(0, 'correctness', true, report.rawOracle);

    await api(explorerRoot, { name: explorerId, title: 'Starting collection handoff QA' });
    builder = await api(`${authoringPath}/builder`);
    assert.equal(builder.catalog.generation, generation, 'The QA Explorer must use the raw oracle generation');
    const patientNode = builder.catalog.nodes.find(node => node.resourceType === 'Patient');
    assert(patientNode, 'The current CDA catalog must expose Patient as a table root');
    await runCommands([{ type: 'CREATE_TABLE', title: 'Handed-off collection QA', rootNodeId: patientNode.nodeId }]);
    const initialDocument = builder.workspace.documents[0];
    assert(initialDocument, 'The API-seeded Builder table was not created');
    outputId = initialDocument.output.id;
    const patientIDColumn = builder.catalog.candidates.find(candidate =>
      candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id');
    assert(patientIDColumn, 'The current CDA catalog must expose Patient.id');
    await runCommands([{
      type: 'ADD_COLUMN',
      outputId,
      occurrenceId: 'base',
      candidateId: patientIDColumn.candidateId,
      projectionMode: 'VALUE',
      initialPresentation: 'TABLE',
      title: 'Patient ID',
    }]);
    const originalDocument = builder.workspace.documents.find(document => document.output.id === outputId);
    assert(originalDocument, 'The seeded Patient table did not survive column creation');
    assert.equal(originalDocument.population, undefined, 'The seeded table must be unattached before the URL handoff');
    const originalColumns = originalDocument.columns;
    const originalConstruction = originalDocument.construction;
    const selectedRefs = selectedWitnesses.map(witness => ({
      project,
      generation,
      resourceType: 'Observation',
      id: witness.observationId,
    })).sort(compareByID);
    selection = await api(selectionPath, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: explorerId,
      source: { kind: 'resources', resources: { refs: selectedRefs } },
    });
    const selectionPage = await api(`${selectionPath}/${encodeURIComponent(selection.id)}?limit=100`);
    const actualRefs = selectionPage.members.map(member => member.ref).sort(compareByID);
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, 'Observation');
    assert.equal(selection.scopeDigest, builder.catalog.authorizationScopeDigest);
    assert.equal(selection.memberCount, selectedRefs.length);
    assert.equal(selectionPage.revision.id, selection.id);
    assert.equal(selectionPage.revision.scopeDigest, builder.catalog.authorizationScopeDigest);
    assert.equal(selectionPage.revision.generation, generation);
    assert.deepEqual(actualRefs, selectedRefs,
      'The immutable handoff selection must equal the exact scoped raw Observation membership');
    report.lifecycle.seed = {
      selectionRevisionId: selection.id,
      resourceType: selection.resourceType,
      scopeDigest: selection.scopeDigest,
      memberRefs: actualRefs,
    };
    recordRequirement(1, 'correctness', true, {
      selectionRevisionId: selection.id,
      generation,
      scopeDigest: selection.scopeDigest,
      memberRefs: actualRefs,
      rawObservationIDs: report.rawOracle.observationIDs,
    });

    const pageURL = new URL(`${uiOrigin}/`);
    pageURL.searchParams.set('project', project);
    pageURL.searchParams.set('explorer', explorerId);
    pageURL.searchParams.set('mode', 'builder');
    pageURL.searchParams.set('selection', selection.id);
    report.lifecycle.handoffIngress = { host: pageURL.origin, queryParameter: 'selection' };
    browserEvents = cda.captureRequests(explorerPath, {
      responsePaths: /selections|population-routes|commands|reconcile|preview/,
    });
    const initialRequestIndex = cda.report.nativeRequests.length;
    const initialLoadStarted = Date.now();
    await cda.navigate(pageURL.toString());
    await wait(([selector]) => Boolean(document.querySelector(selector)), ['[data-testid="construction-workspace"]']);
    const selectionReadPath = `${explorerPath}/selections/${encodeURIComponent(selection.id)}`;
    const initialSelectionRead = await browserEvents.waitFor(request =>
      request.method === 'GET' && request.path === selectionReadPath && request.status === 200,
    { fromIndex: initialRequestIndex, timeoutMs: ACTION_BUDGET_MS });
    const initialSelectionResponse = browserEvents.rawResponseBody(initialSelectionRead);
    assert.equal(initialSelectionResponse?.revision?.id, selection.id,
      'The standalone browser must fetch the exact handed-off immutable revision');
    assert.equal(initialSelectionResponse.revision.scopeDigest, selection.scopeDigest);
    assert.equal(initialSelectionResponse.revision.generation, generation);
    assert.equal(initialSelectionResponse.revision.resourceType, 'Observation');
    assert.equal(initialSelectionResponse.revision.memberCount, selectedRefs.length);
    assert.equal(initialSelectionResponse.members.length, 1, 'The browser handoff requests one member per page');
    assert.deepEqual(initialSelectionResponse.members[0].ref, selectedRefs.find(ref => ref.id === initialSelectionResponse.members[0].ref.id),
      'The browser page must contain an exact scoped member of the independently verified immutable selection');
    const outputSelector = `[data-testid="construction-table-${outputId}"]`;
    await openStartingCollection(selection.id, outputSelector);
    const handoffLoadMs = Date.now() - initialLoadStarted;
    addCheckpoint('standalone URL handoff to visible starting collection', handoffLoadMs);
    const initialSelectionReads = cda.report.nativeRequests.filter(request =>
      request.method === 'GET' && request.path === selectionReadPath && request.status === 200);
    assert(initialSelectionReads.length > 0, 'The standalone host must fetch the exact handed-off selection through the browser');
    recordRequirement(2, 'correctness', true, {
      queryParameter: 'selection',
      selectionRevisionId: selection.id,
      browserSelectionReadCount: initialSelectionReads.length,
      browserReadStatus: initialSelectionReads.at(-1).status,
    });

    const requestBody = request => browserEvents.rawRequestBody(request);
    const responseBody = request => browserEvents.rawResponseBody(request);
    const waitForSavedPreviewBinding = async ({ fromIndex, expectedBuilder, phase }) => {
      const expected = {
        path: `${authoringPath}/reconcile`,
        snapshotToken: expectedBuilder.catalog.snapshotToken,
        draftVersion: expectedBuilder.draftVersion,
        draftDigest: expectedBuilder.draftDigest,
        generation: expectedBuilder.catalog.generation,
        authorizationScopeDigest: expectedBuilder.catalog.authorizationScopeDigest,
        savedWorkspace: expectedBuilder.workspace,
        outputId,
      };
      const reconcileRequest = await browserEvents.waitFor(request =>
        startingCollectionReconcileRequestMatchesSavedDraft({
          request,
          requestBody: requestBody(request),
          expected,
        }),
      { fromIndex, timeoutMs: ACTION_BUDGET_MS });
      const receiptEvidence = startingCollectionReconcileReceiptEvidence({
        request: reconcileRequest,
        requestBody: requestBody(reconcileRequest),
        response: responseBody(reconcileRequest),
        expected,
      });
      assert(receiptEvidence.ok,
        `${phase} reconcile receipt must belong to the exact saved draft: ${receiptEvidence.failures.join('; ')}`);
      const receiptId = receiptEvidence.receiptId;
      const previewRequest = await browserEvents.waitFor(request =>
        startingCollectionPreviewRequestMatchesReceipt({
          request,
          requestBody: requestBody(request),
          expected: { path: `${authoringPath}/preview`, receiptId, outputId },
        }),
      { fromIndex: cda.report.nativeRequests.indexOf(reconcileRequest) + 1, timeoutMs: ACTION_BUDGET_MS });
      const previewEvidence = startingCollectionNativePreviewReceiptEvidence({
        request: previewRequest,
        requestBody: requestBody(previewRequest),
        response: responseBody(previewRequest),
        expected: { path: `${authoringPath}/preview`, receiptId, outputId },
      });
      assert(previewEvidence.ok,
        `${phase} native preview must use the exact reconcile receipt: ${previewEvidence.failures.join('; ')}`);
      return { reconcileRequest, receiptId, previewRequest };
    };
    const visiblePreviewIdentity = () => cda.inspect(() => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return {
        status: preview?.dataset.previewStatus ?? null,
        receiptId: preview?.dataset.previewReceiptId ?? null,
        outputId: preview?.dataset.previewOutputId ?? null,
        draftVersion: preview?.dataset.currentDraftVersion ?? null,
        draftDigest: preview?.dataset.currentDraftDigest ?? null,
      };
    });
    const routePath = `${authoringPath}/population-routes`;
    const routeRequest = await browserEvents.waitFor(request => {
      const body = requestBody(request);
      return request.method === 'POST' && request.path === routePath &&
        body?.outputId === outputId && body?.selectionRevisionId === selection.id &&
        Array.isArray(responseBody(request)?.choices);
    }, { timeoutMs: ACTION_BUDGET_MS });
    assert(routeRequest?.status === 200, 'The Builder must discover routes for the handed-off selection in the browser');
    const routeRequestBody = requestBody(routeRequest);
    const routeResponse = responseBody(routeRequest);
    assert.equal(routeRequestBody.snapshotToken, builder.catalog.snapshotToken);
    assert.equal(routeResponse.snapshotToken, routeRequestBody.snapshotToken);
    assert.equal(routeResponse.outputId, outputId);
    assert.equal(routeResponse.selectionRevisionId, selection.id);
    const subjectChoices = routeResponse.choices.filter(choice =>
      choice.route.length === 1 && choice.route[0].relationship === 'subject_Patient' &&
      choice.route[0].fromResourceType === 'Patient' && choice.route[0].toResourceType === 'Observation' &&
      choice.route[0].storageDirection === 'INBOUND');
    assert.equal(subjectChoices.length, 1,
      'The browser route response must contain one unambiguous Patient-to-selected-Observation INBOUND subject route');
    const expectedChoice = subjectChoices[0];
    const connectionSelector = 'section[aria-label="Starting collection"] select[aria-label="Population connection"]';
    const connectionOptions = await cda.inspect(() => {
      const select = document.querySelector('section[aria-label="Starting collection"] select[aria-label="Population connection"]');
      return select ? [...select.options].map(option => ({ value: option.value, label: option.text, disabled: option.disabled })) : [];
    });
    if (connectionOptions.length > 0) {
      const subjectOption = connectionOptions.find(option => /subject/i.test(option.label) && !option.disabled);
      assert(subjectOption, `Native route choices must expose the subject connection: ${JSON.stringify(connectionOptions)}`);
      await cda.selectOption(connectionSelector, subjectOption.value);
    } else {
      const panelText = await cda.inspect(() => document.querySelector('section[aria-label="Starting collection"]')?.innerText ?? '');
      assert.match(panelText, /subject/i, 'The only visible connection must be the raw Subject route');
    }

    const preAttach = await api(`${authoringPath}/builder`);
    const attachStarted = Date.now();
    const attachRequestIndex = cda.report.nativeRequests.length;
    await cda.click('section[aria-label="Starting collection"] button', { name: 'Use selected resources' });
    await wait(([id]) => document.querySelector('section[aria-label="Starting collection"]')
      ?.getAttribute('data-attached-selection-revision-id') === id, [selection.id]);
    builder = await api(`${authoringPath}/builder`);
    const savedDocument = builder.workspace.documents.find(document => document.output.id === outputId);
    assert(savedDocument?.population, 'Native route application must save a population route');
    const expectedRoute = savedRouteForChoice(expectedChoice);
    assert.equal(savedDocument.population.selectionRevisionId, selection.id);
    assert.deepEqual(savedDocument.population.route, expectedRoute,
      'Native Apply must persist the Patient-table to selected Observation INBOUND subject route');
    assert.deepEqual(savedDocument.columns, originalColumns, 'Route attachment must preserve the authored Patient.id column');
    assert.deepEqual(savedDocument.construction, originalConstruction, 'Route attachment must preserve the original construction');
    const appliedDraftVersion = builder.draftVersion;
    const appliedDraftDigest = builder.draftDigest;
    report.lifecycle.attachmentHasCancelAffordance = false;
    report.lifecycle.cancelReason = 'Use selected resources saves directly; this attachment action has no Cancel step.';
    const attachCommand = await browserEvents.waitFor(request => {
      const body = browserEvents.rawRequestBody(request);
      return request.method === 'POST' && request.path === `${authoringPath}/commands` &&
        body?.commands?.some(command => command.type === 'SET_TABLE_POPULATION' &&
          command.selectionRevisionId === selection.id && command.outputId === outputId);
    }, { fromIndex: attachRequestIndex, timeoutMs: ACTION_BUDGET_MS });
    assert.equal(attachCommand.status, 200, 'The native population attachment must succeed');
    const attachBody = browserEvents.rawRequestBody(attachCommand);
    assert.equal(attachBody.expectedDraftVersion, preAttach.draftVersion);
    assert.equal(attachBody.expectedDraftDigest, preAttach.draftDigest);
    assert.equal(attachBody.snapshotToken, preAttach.catalog.snapshotToken);
    assert.equal(attachBody.commands.length, 1, 'Attachment must issue exactly one command');
    assert.equal(attachBody.commands[0].type, 'SET_TABLE_POPULATION');
    assert.equal(attachBody.commands[0].outputId, outputId);
    assert.equal(attachBody.commands[0].selectionRevisionId, selection.id);
    assert.equal(attachBody.commands[0].routeChoiceId, expectedChoice.routeChoiceId);
    recordRequirement(3, 'persistence', true, {
      selectionRevisionId: savedDocument.population.selectionRevisionId,
      route: savedDocument.population.route,
      columnsPreserved: true,
      constructionPreserved: true,
      nativeApplyStatus: attachCommand.status,
    });

    const previewStarted = Date.now();
    const appliedPreviewBinding = await waitForSavedPreviewBinding({
      fromIndex: attachRequestIndex,
      expectedBuilder: builder,
      phase: 'Applied draft',
    });
    const preview = await waitForPreview(expectedPatientIDs);
    const appliedPreviewIdentity = await visiblePreviewIdentity();
    const appliedPreviewIdentityEvidence = startingCollectionRenderedPreviewDraftEvidence({
      preview: appliedPreviewIdentity,
      expected: {
        receiptId: appliedPreviewBinding.receiptId,
        outputId,
        draftVersion: appliedDraftVersion,
        draftDigest: appliedDraftDigest,
      },
    });
    assert(appliedPreviewIdentityEvidence.ok,
      `Applied visible preview must match the saved draft receipt: ${appliedPreviewIdentityEvidence.failures.join('; ')}`);
    addCheckpoint('native Apply to exact rendered Patient preview', Date.now() - attachStarted);
    report.lifecycle.previewVerificationMs = Date.now() - previewStarted;
    assert.equal(preview.rowCount, expectedPatientIDs.length);
    assert.deepEqual(preview.patientIDs, expectedPatientIDs,
      'The applied preview must match exact raw Patient IDs reached by the selected Observations');
    recordRequirement(4, 'correctness', true, {
      rowCount: preview.rowCount,
      headers: preview.headers,
      visiblePatientIDs: preview.patientIDs,
      rawPatientIDs: expectedPatientIDs,
      previewRequestId: appliedPreviewBinding.previewRequest.requestId,
      previewResponseStatus: appliedPreviewBinding.previewRequest.status,
      receiptId: appliedPreviewBinding.receiptId,
      reconcileRequestId: appliedPreviewBinding.reconcileRequest.requestId,
      draftVersion: appliedDraftVersion,
      draftDigest: appliedDraftDigest,
    });
    report.lifecycle.appliedPreviewBinding = {
      receiptId: appliedPreviewBinding.receiptId,
      outputId,
      draftVersion: appliedDraftVersion,
      draftDigest: appliedDraftDigest,
      reconcileRequestId: appliedPreviewBinding.reconcileRequest.requestId,
      previewRequestId: appliedPreviewBinding.previewRequest.requestId,
      visiblePreview: appliedPreviewIdentity,
    };

    const savedPopulation = savedDocument.population;
    const savedRoute = savedDocument.population.route;
    const savedColumns = savedDocument.columns;
    const savedConstruction = savedDocument.construction;
    const savedSnapshotToken = builder.catalog.snapshotToken;
    const savedDraftVersion = builder.draftVersion;
    const savedDraftDigest = builder.draftDigest;
    const selectionReadsBeforeReload = cda.report.nativeRequests.filter(request =>
      request.method === 'GET' && request.path === selectionReadPath && request.status === 200).length;
    const reloadRequestIndex = cda.report.nativeRequests.length;
    const reloadStarted = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
    await wait(([selector]) => Boolean(document.querySelector(selector)), ['[data-testid="construction-workspace"]']);
    builder = await api(`${authoringPath}/builder`);
    assert.equal(builder.catalog.snapshotToken, savedSnapshotToken, 'Reload must restore the same catalog snapshot');
    assert.equal(builder.catalog.generation, generation, 'Reload must stay on the exact fixture generation');
    assert.equal(builder.catalog.authorizationScopeDigest, selection.scopeDigest, 'Reload must preserve the handoff authorization scope');
    assert.equal(builder.draftVersion, savedDraftVersion, 'Reload must restore the exact saved draft version');
    assert.equal(builder.draftDigest, savedDraftDigest, 'Reload must restore the exact saved draft digest');
    const reloadedDocument = builder.workspace.documents.find(document => document.output.id === outputId);
    assert.deepEqual(reloadedDocument?.population, savedPopulation,
      'Reload must retain the exact selection revision and subject route');
    assert.deepEqual(reloadedDocument.population.route, expectedRoute,
      'Reload must preserve the exact Patient-table to selected Observation INBOUND route');
    assert.deepEqual(reloadedDocument.columns, savedColumns, 'Reload must retain the authored Patient.id column');
    assert.deepEqual(reloadedDocument.construction, savedConstruction, 'Reload must retain the original construction');
    const reloadedPreviewBinding = await waitForSavedPreviewBinding({
      fromIndex: reloadRequestIndex,
      expectedBuilder: builder,
      phase: 'Reloaded draft',
    });
    await waitForPreview(expectedPatientIDs);
    const reloadedPreviewIdentity = await visiblePreviewIdentity();
    const reloadedPreviewIdentityEvidence = startingCollectionRenderedPreviewDraftEvidence({
      preview: reloadedPreviewIdentity,
      expected: {
        receiptId: reloadedPreviewBinding.receiptId,
        outputId,
        draftVersion: savedDraftVersion,
        draftDigest: savedDraftDigest,
      },
    });
    assert(reloadedPreviewIdentityEvidence.ok,
      `Reloaded visible preview must match the saved draft receipt: ${reloadedPreviewIdentityEvidence.failures.join('; ')}`);
    const reloadRenderMs = Date.now() - reloadStarted;
    report.lifecycle.reloadPreviewRenderMs = reloadRenderMs;
    assert.equal(new URL(page.url()).searchParams.get('selection'), selection.id,
      'Reload must preserve the standalone selection handoff URL');
    await openStartingCollection(selection.id, outputSelector);
    const reloadedSelectionRead = await browserEvents.waitFor(request =>
      request.method === 'GET' && request.path === selectionReadPath && request.status === 200,
    { fromIndex: reloadRequestIndex, timeoutMs: ACTION_BUDGET_MS });
    const reloadedSelectionResponse = responseBody(reloadedSelectionRead);
    assert.equal(reloadedSelectionResponse?.revision?.id, selection.id,
      'The reloaded standalone host must fetch the exact handed-off immutable revision again');
    assert.equal(reloadedSelectionResponse.revision.scopeDigest, selection.scopeDigest);
    assert.equal(reloadedSelectionResponse.revision.generation, generation);
    assert.equal(reloadedSelectionResponse.revision.resourceType, 'Observation');
    assert.equal(reloadedSelectionResponse.revision.memberCount, selectedRefs.length);
    assert.equal(reloadedSelectionResponse.members.length, 1, 'The browser handoff requests one member per page');
    assert.deepEqual(reloadedSelectionResponse.members[0].ref, selectedRefs.find(ref => ref.id === reloadedSelectionResponse.members[0].ref.id),
      'The browser page must contain an exact scoped member of the independently verified immutable selection');
    const reloadedSelectionReads = cda.report.nativeRequests.filter(request =>
      request.method === 'GET' && request.path === selectionReadPath && request.status === 200);
    assert(reloadedSelectionReads.length > selectionReadsBeforeReload,
      'Reload must fetch the handed-off revision again through the standalone host');
    const reloadedPanel = await cda.inspect(() => {
      const panel = document.querySelector('section[aria-label="Starting collection"]');
      return {
        selectionRevisionId: panel?.getAttribute('data-selection-revision-id'),
        attachedSelectionRevisionId: panel?.getAttribute('data-attached-selection-revision-id'),
      };
    });
    assert.deepEqual(reloadedPanel, {
      selectionRevisionId: selection.id,
      attachedSelectionRevisionId: selection.id,
    }, 'Reload must restore both the URL handoff and persisted starting collection in the native panel');
    const reloadedPreview = await waitForPreview(expectedPatientIDs);
    assert.deepEqual(reloadedPreview.patientIDs, expectedPatientIDs);
    addCheckpoint('reload to exact preview and restored starting collection', Date.now() - reloadStarted);
    report.lifecycle.reload = {
      population: reloadedDocument.population,
      panel: reloadedPanel,
      preview: reloadedPreview,
      previewRequestStatus: reloadedPreviewBinding.previewRequest.status,
      previewReceiptId: reloadedPreviewBinding.receiptId,
      reconcileRequestId: reloadedPreviewBinding.reconcileRequest.requestId,
      previewRequestId: reloadedPreviewBinding.previewRequest.requestId,
      visiblePreviewIdentity: reloadedPreviewIdentity,
      browserSelectionReadCount: reloadedSelectionReads.length,
    };
    recordRequirement(5, 'persistence', true, {
      population: reloadedDocument.population,
      columnsPreserved: true,
      constructionPreserved: true,
      panel: reloadedPanel,
      previewPatientIDs: reloadedPreview.patientIDs,
      previewReceiptId: reloadedPreviewBinding.receiptId,
      reconcileRequestId: reloadedPreviewBinding.reconcileRequest.requestId,
      previewRequestId: reloadedPreviewBinding.previewRequest.requestId,
      selectionFetchCountAfterReload: reloadedSelectionReads.length,
    });

    recordRequirement(6, 'performance', checkpointDurations.every(checkpoint => checkpoint.durationMs <= ACTION_BUDGET_MS), {
      budgetMs: ACTION_BUDGET_MS,
      checkpoints: checkpointDurations,
    });
    await browserEvents.flush();
    cda.includeBrowserDiagnostics();
    assert.deepEqual(cda.diagnostics.pageErrors, [], 'Browser JavaScript exceptions occurred');
    assert.deepEqual(report.errors, [], 'Unexpected browser HTTP, runtime, console, or module errors occurred');
    recordRequirement(7, 'correctness', report.errors.length === 0 && cda.diagnostics.pageErrors.length === 0, {
      unexpectedErrors: report.errors,
      pageErrors: cda.diagnostics.pageErrors,
      incidentalAssetFailures: report.assetFailures,
    });
    report.lifecycle.status = 'passed';
  } catch (error) {
    fatal = error;
    report.lifecycle.status = 'failed';
    report.lifecycle.failure = {
      message: String(error?.message ?? error),
      stack: error?.stack,
      currentURL: await cda.inspect(() => `${location.origin}${location.pathname}`).catch(() => undefined),
      builder: outputId ? await api(`${authoringPath}/builder`).catch(apiError => ({ error: String(apiError) })) : undefined,
      bodyText: await cda.inspect(() => document.body.innerText.slice(-12_000)).catch(inspectError => `DOM capture failed: ${String(inspectError)}`),
    };
    cda.includeBrowserDiagnostics();
  } finally {
    await browserEvents?.flush().catch(() => undefined);
    report.lifecycle.checkpointDurations = checkpointDurations;
    report.lifecycle.nativeRequests = cda.report.nativeRequests.map(({ method, path, status, body, response }) => ({ method, path, status, body, response }));
    await cda.attachReport('starting-collection-handoff.json', report);
  }

  if (fatal) throw fatal;
  return report;
}
