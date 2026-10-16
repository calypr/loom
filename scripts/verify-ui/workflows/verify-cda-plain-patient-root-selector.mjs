import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  buildArangoShellInvocation,
  buildBoundedArangoQueryScript,
  summarizeArangoShellResult,
} from '../helpers/owned-arangosh-command.mjs';
import {
  matchesRootRebaseAssessment,
  rootRebaseColumnChoiceIdentity,
  rootRebasePreservesAuthoredDocument,
  rootRebasePreservesFilterWhenAddingColumn,
  waitForRootRebaseColumnApply,
} from '../helpers/root-rebase-preservation.mjs';

export const plainPatientRootSelectorOracleBounds = Object.freeze({
  observationSampleLimit: 10_000,
  candidatePatientLimit: 25,
  observationSentinelLimit: 26,
  maxRuntimeSeconds: 8,
  memoryLimitBytes: 256 * 1024 * 1024,
  hostTimeoutMs: 30_000,
  visiblePreviewLimit: 25,
});

export function selectPlainPatientRootSelectorWitness(candidates, rereadCandidate, expectedScope) {
  if (!Array.isArray(candidates)) throw new TypeError('Plain Patient root selector candidates must be an array.');
  if (typeof rereadCandidate !== 'function') throw new TypeError('Plain Patient root selector candidate rereader must be a function.');
  if (!expectedScope || typeof expectedScope.project !== 'string' || typeof expectedScope.generation !== 'string') {
    throw new TypeError('Plain Patient root selector candidate validation requires an expected project and generation.');
  }

  const { candidatePatientLimit } = plainPatientRootSelectorOracleBounds;
  const boundedCandidates = candidates.slice(0, candidatePatientLimit);
  const attempts = [];
  let witness = null;

  for (const candidate of boundedCandidates) {
    const patientId = candidate?.patientId;
    const sampledObservationCount = candidate?.sampledObservationCount;
    if (typeof patientId !== 'string' || !patientId) {
      attempts.push({ patientId: null, sampledObservationCount: sampledObservationCount ?? null, outcome: 'invalid-sample-candidate' });
      continue;
    }
    if (!Number.isInteger(sampledObservationCount) || sampledObservationCount < 2 || sampledObservationCount > 25) {
      attempts.push({ patientId, sampledObservationCount: sampledObservationCount ?? null, outcome: 'sample-count-out-of-range' });
      continue;
    }

    const exactRows = rereadCandidate(patientId);
    const exact = Array.isArray(exactRows) && exactRows.length === 1 ? exactRows[0] : null;
    const observations = Array.isArray(exact?.observations) ? exact.observations : [];
    const observationIDs = observations.map(observation => observation?.id);
    const duplicateObservationIDs = new Set(observationIDs).size !== observationIDs.length;
    let outcome = 'accepted';

    if (!Array.isArray(exactRows) || exactRows.length !== 1) outcome = 'patient-row-count';
    else if (exact.patientId !== patientId) outcome = 'patient-identity-mismatch';
    else if (exact.project !== expectedScope.project || exact.generation !== expectedScope.generation) outcome = 'patient-scope-mismatch';
    else if (!Array.isArray(exact.observations)) outcome = 'observation-rows-missing';
    else if (observations.some(observation => observation?.project !== expectedScope.project
      || observation?.generation !== expectedScope.generation)) outcome = 'observation-scope-mismatch';
    else if (observationIDs.length === plainPatientRootSelectorOracleBounds.observationSentinelLimit
      || observationIDs.length > plainPatientRootSelectorOracleBounds.observationSentinelLimit) outcome = 'observation-sentinel-exceeded';
    else if (observationIDs.length < 2) outcome = 'too-few-observations';
    else if (observationIDs.some(id => typeof id !== 'string' || !id) || duplicateObservationIDs) outcome = 'invalid-observation-identities';

    const attempt = {
      patientId,
      sampledObservationCount: candidate.sampledObservationCount ?? null,
      exactPatientRows: Array.isArray(exactRows) ? exactRows.length : null,
      exactObservationCount: observationIDs.length,
      outcome,
    };
    attempts.push(attempt);

    if (outcome === 'accepted' && typeof exact.patientKey === 'string'
      && exact.patientKey.startsWith('Patient/')) {
      witness = {
        patientId: exact.patientId,
        patientKey: exact.patientKey,
        project: exact.project,
        generation: exact.generation,
        observationIDs,
      };
      break;
    }
    if (outcome === 'accepted') attempt.outcome = 'invalid-patient-key';
  }

  return {
    witness,
    attempts,
    sampledCandidateCount: candidates.length,
    candidatePatientLimit,
    candidateLimitReached: !witness && boundedCandidates.length === candidatePatientLimit,
  };
}

export function buildPlainPatientRootSelectorOracleMetadata(witness, selection) {
  const {
    observationSampleLimit,
    candidatePatientLimit,
    observationSentinelLimit,
    maxRuntimeSeconds,
    memoryLimitBytes,
    hostTimeoutMs,
    visiblePreviewLimit,
  } = plainPatientRootSelectorOracleBounds;
  return {
    patientId: witness.patientId,
    patientKey: witness.patientKey,
    observationIDs: witness.observationIDs,
    observationCount: witness.observationIDs.length,
    observationSampleLimit,
    candidatePatientLimit,
    candidateSelectionAttempts: selection.attempts,
    candidateLimitReached: selection.candidateLimitReached,
    sampleSelection: 'First 10,000 scoped Observation rows sorted by id; grouped Patient.subject counts only propose candidates.',
    candidateSort: 'Patient subject reference',
    perPatientObservationSentinelLimit: observationSentinelLimit,
    sentinelMeaning: 'A 26-row exact result is at least 26 matches and is excluded; only exact scoped rereads with 2–25 rows are accepted.',
    queryCaps: { maxRuntimeSeconds, memoryLimitBytes, hostTimeoutMs },
    exactSelectedPatientReread: true,
    expectedProject: witness.project,
    expectedGeneration: witness.generation,
    expectedPatientRows: [witness.patientId],
    expectedObservationRows: witness.observationIDs,
    expectedRestoredPatientRows: [witness.patientId],
    visiblePreviewLimit,
  };
}

export function isPlainPatientRoot(document) {
  return document?.rootResourceType === 'Patient'
    && document.route?.resourceType === 'Patient'
    && (!Array.isArray(document.route.children) || document.route.children.length === 0);
}

export function assertPatientSelectionCreated(response, selection) {
  assert.equal(response?.status, 201,
    `Creating the nondefault Patient collection failed: ${JSON.stringify(selection)}`);
}

export function choosePatientLinkedObservationOption(options) {
  if (!Array.isArray(options)) throw new TypeError('Starting-record options must be an array.');
  const enabled = options.filter(option => !option.disabled
    && typeof option.label === 'string'
    && /^Observation\b/i.test(option.label.trim()));
  if (enabled.length === 1) return enabled[0];
  const patientSubject = enabled.filter(option => /patient/i.test(option.label) && /subject/i.test(option.label));
  if (patientSubject.length === 1) return patientSubject[0];
  throw new Error(`Expected one enabled Observation choice or one Patient Subject path, found ${enabled.length}.`);
}

export function matchesPlainPatientObservationAssessment(entry, expected, { status = 'READY' } = {}) {
  const request = entry?.request;
  const response = entry?.response;
  const proposal = response?.proposal;
  if (!expected?.outputId || !expected.snapshotToken || !Number.isInteger(expected.draftVersion)
    || !expected.draftDigest || !expected.rootNodeId) return false;
  if (entry?.path?.endsWith('/row-change') !== true || entry.method !== 'POST' || entry.status !== 200
    || request?.outputId !== expected.outputId || request?.snapshotToken !== expected.snapshotToken
    || request?.draftVersion !== expected.draftVersion || request?.draftDigest !== expected.draftDigest
    || request?.rootNodeId !== expected.rootNodeId
    || (request.rootOccurrenceId !== undefined && (typeof request.rootOccurrenceId !== 'string' || !request.rootOccurrenceId))
    || response?.snapshotToken !== expected.snapshotToken || response?.draftVersion !== expected.draftVersion
    || response?.draftDigest !== expected.draftDigest || response?.currentRootResourceType !== 'Patient'
    || response?.candidateRootResourceType !== 'Observation' || response?.status !== status) return false;
  if (status === 'READY' && (proposal?.outputId !== expected.outputId || proposal?.rootNodeId !== expected.rootNodeId)) return false;
  if (status === 'BLOCKED' && (proposal !== undefined
    || !response.unresolved?.some(reference => reference?.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE'
      && Array.isArray(reference.alternatives) && reference.alternatives.length > 0))) return false;
  if (status !== 'READY' && status !== 'BLOCKED') return false;
  if (status === 'BLOCKED') return true;
  return request.rootOccurrenceId === undefined || proposal.rootOccurrenceId === undefined
    || request.rootOccurrenceId === proposal.rootOccurrenceId;
}

export function choosePatientSubjectObservationRepairButton(buttons) {
  if (!Array.isArray(buttons)) throw new TypeError('Root occurrence repair buttons must be an array.');
  const eligible = buttons.filter(button => !button.disabled && button.text ===
    'Use Patient → Subject → Observation from Patient rows');
  if (eligible.length !== 1) {
    throw new Error(`Expected one enabled Patient Subject Observation repair button, found ${eligible.length}.`);
  }
  return eligible[0];
}

export function choosePatientObservationSubjectRestorationButton(buttons) {
  if (!Array.isArray(buttons)) throw new TypeError('Root restoration repair buttons must be an array.');
  const eligible = buttons.filter(button => !button.disabled && button.text ===
    'Match through Subject between Observation and Patient');
  if (eligible.length !== 1) {
    throw new Error(`Expected one enabled Subject route from Observation to Patient, found ${eligible.length}.`);
  }
  return eligible[0];
}

export function chooseDirectPatientPopulationRoute(choices) {
  if (!Array.isArray(choices)) throw new TypeError('Population route choices must be an array.');
  const direct = choices.filter(choice => Array.isArray(choice?.route) && choice.route.length === 0);
  if (direct.length !== 1) throw new Error(`Expected one direct Patient population route, found ${direct.length}.`);
  return direct[0];
}

function routeNodes(route) {
  if (!route || typeof route !== 'object' || Array.isArray(route)) return [];
  return [route, ...(Array.isArray(route.children) ? route.children.flatMap(routeNodes) : [])];
}

function populationKeepsStartingCollection(before, after) {
  if (before == null) return isDeepStrictEqual(before, after);
  if (!before || !after || typeof before !== 'object' || typeof after !== 'object'
    || Array.isArray(before) || Array.isArray(after)) return false;
  const { route: beforeRoute, ...beforeIdentity } = before;
  const { route: afterRoute, ...afterIdentity } = after;
  if (!isDeepStrictEqual(beforeIdentity, afterIdentity)) return false;
  if (beforeRoute === undefined) return afterRoute === undefined;
  if (!Array.isArray(beforeRoute) || !Array.isArray(afterRoute) || afterRoute.length < beforeRoute.length) return false;
  return isDeepStrictEqual(afterRoute.slice(afterRoute.length - beforeRoute.length), beforeRoute);
}

export function directObservationRootPreservesPatientState(beforeDocument, afterDocument) {
  if (!isPlainPatientRoot(beforeDocument) || !beforeDocument.output?.id
    || afterDocument?.rootResourceType !== 'Observation'
    || afterDocument.route?.resourceType !== 'Observation'
    || !Array.isArray(beforeDocument.columns) || beforeDocument.columns.length === 0
    || !Array.isArray(afterDocument.columns)
    || !isDeepStrictEqual(beforeDocument.construction, afterDocument.construction)
    || !populationKeepsStartingCollection(beforeDocument.population, afterDocument.population)) return false;
  const patientNodes = routeNodes(afterDocument.route).filter(node => node.resourceType === 'Patient');
  if (patientNodes.length !== 1 || patientNodes[0].occurrenceId === 'base') return false;
  const expectedColumns = structuredClone(beforeDocument.columns).map(column => ({
    ...column,
    ...(column.occurrenceId === 'base' ? { occurrenceId: patientNodes[0].occurrenceId } : {}),
  }));
  if (!isDeepStrictEqual(afterDocument.columns, expectedColumns)) return false;
  const ignored = new Set(['rootResourceType', 'route', 'columns', 'population']);
  const otherState = document => Object.fromEntries(Object.entries(document).filter(([key]) => !ignored.has(key)));
  return isDeepStrictEqual(otherState(beforeDocument), otherState(afterDocument));
}

export async function createAndNavigatePlainPatientRootSelectorExplorer({ apiOrigin, pageURL, navigate, fetchImpl = fetch }) {
  assert.equal(typeof navigate, 'function', 'Plain Patient root selector setup requires the native route navigator');
  const route = new URL(pageURL);
  const projects = route.searchParams.getAll('project');
  const explorers = route.searchParams.getAll('explorer');
  const modes = route.searchParams.getAll('mode');
  assert.equal(projects.length, 1, 'Plain Patient root selector route must name one project');
  assert.equal(explorers.length, 1, 'Plain Patient root selector route must name one Explorer');
  assert.deepEqual(modes, ['builder'], 'Plain Patient root selector route must open Builder mode');

  const [project] = projects;
  const [explorerId] = explorers;
  const collectionPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const response = await fetchImpl(`${apiOrigin.replace(/\/$/, '')}${collectionPath}`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ name: explorerId, title: 'CDA plain Patient root selector verification' }),
    signal: AbortSignal.timeout(30_000),
  });
  const body = await response.json();
  assert(response.ok, `Failed to create scoped plain-patient-root-selector Explorer (${response.status}): ${JSON.stringify(body)}`);
  await navigate(pageURL);
  return { project, explorerId, pageURL, collectionPath, createStatus: response.status };
}

export async function plainPatientRootSelectorWorkflow({ page, cda }) {
  const project = cda.project;
  const explorerId = `cda-plain-patient-root-selector-${Date.now()}`;
  const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
  const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
  const routeProject = encodeURIComponent(project);
  const routeExplorer = encodeURIComponent(explorerId);
  const pageURL = `${uiOrigin}/?project=${routeProject}&explorer=${routeExplorer}&mode=builder`;
  const authoringURL = `${apiOrigin}/api/v1/projects/${routeProject}/explorers/${routeExplorer}/authoring/v2`;
  const explorerURL = `${apiOrigin}/api/v1/projects/${routeProject}/explorers/${routeExplorer}`;
  const selectionURL = `${explorerURL}/selections`;
  const tableName = `CDA plain Patient root selector QA ${Date.now()}`;
  const state = cda.report;
  Object.assign(state, { project, explorer: explorerId, pageURL, tableName, actions: [], timingsMs: {}, rawOracleQueries: [], errors: state.errors ?? [], started: new Date().toISOString() });
  state.target = cda.target;
  const requiredChecks = cda.report.requiredChecks;
  assert.equal(requiredChecks.length, 8, 'The plain-patient-root-selector report must match its eight registered lifecycle checks');
  const recordRequiredCheck = (index, dimension, passed, evidence) => {
    const name = requiredChecks[index];
    assert(name, `Plain Patient root selector case is missing registered required check ${index}`);
    cda.check(dimension, name, passed, evidence);
  };
  const browserEvents = cda.captureRequests(`/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2`, { responsePaths: /commands|row-change|preview|construction-proposals|construction-choice-proposals|population-routes/ });
  let outputId;

  const builder = async () => {
    const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30_000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const readSelection = async selectionId => {
    const response = await fetch(`${selectionURL}/${encodeURIComponent(selectionId)}?limit=100`, {
      signal: AbortSignal.timeout(30_000),
    });
    const body = await response.json();
    assert.equal(response.status, 200, `Reading Patient selection failed: ${JSON.stringify(body)}`);
    return body;
  };
  const refKey = ref => `${ref?.project}/${ref?.generation}/${ref?.resourceType}/${ref?.id}`;
  const assertPatientPopulation = async (document, selectionSeed, expectedRoute) => {
    assert(document?.population, 'The Patient table must retain a concrete starting collection');
    assert.equal(document.population.selectionRevisionId, selectionSeed.id,
      'The table must retain the exact Patient selection revision');
    assert.deepEqual(document.population.route, expectedRoute,
      'The table must preserve or rebase the exact Patient collection route');
    const current = await readSelection(selectionSeed.id);
    assert.equal(current.revision?.id, selectionSeed.id);
    assert.equal(current.revision?.project, project);
    assert.equal(current.revision?.generation, selectionSeed.generation);
    assert.equal(current.revision?.resourceType, 'Patient');
    assert.equal(current.revision?.scopeDigest, selectionSeed.scopeDigest);
    assert.equal(current.revision?.memberCount, 1);
    const memberRefs = (current.members ?? []).map(member => refKey(member.ref)).sort();
    assert.deepEqual(memberRefs, selectionSeed.memberRefs,
      'The immutable Patient starting collection must retain its exact selected Patient membership');
    return current;
  };
  const waitForRoot = async (targetId, root) => {
    const value = await builder();
    assert.equal(value.workspace.documents.find(document => document.output.id === targetId)?.rootResourceType, root);
    return value;
  };
  const rowAssessmentIdentity = (draft, candidateRootResourceType) => {
    const document = draft.workspace.documents.find(candidate => candidate.output.id === outputId);
    const candidateOccurrence = document?.route?.children?.[0];
    assert(document, `Cannot bind row assessment to missing output ${outputId}`);
    assert.equal(candidateOccurrence?.resourceType, candidateRootResourceType,
      `The ${candidateRootResourceType} root assessment must use the current output route`);
    return {
      outputId,
      snapshotToken: draft.catalog.snapshotToken,
      draftVersion: draft.draftVersion,
      draftDigest: draft.draftDigest,
      currentRootResourceType: document.rootResourceType,
      candidateRootResourceType,
      rootOccurrenceId: candidateOccurrence.occurrenceId,
    };
  };
  const directObservationAssessmentIdentity = draft => {
    const document = draft.workspace.documents.find(candidate => candidate.output.id === outputId);
    const targetNodes = draft.catalog.nodes.filter(node => node.resourceType === 'Observation'
      && node.rowRootEligible && node.populated);
    assert(document, `Cannot bind direct root assessment to missing output ${outputId}`);
    assert(isPlainPatientRoot(document), 'Observation must be selected from a plain Patient root with no authored route child');
    assert.equal(targetNodes.length, 1,
      `Expected one loaded eligible Observation root node, found ${targetNodes.length}`);
    return {
      outputId,
      snapshotToken: draft.catalog.snapshotToken,
      draftVersion: draft.draftVersion,
      draftDigest: draft.draftDigest,
      rootNodeId: targetNodes[0].nodeId,
    };
  };
  const waitForRowAssessment = async (after, expected) => {
    const entry = await browserEvents.waitForResponse(
      candidate => matchesRootRebaseAssessment(candidate, expected),
      { fromIndex: after, timeoutMs: 5000 },
    );
    return entry.response;
  };
  const actClick = async (selector, name, identity = {}) => {
    const startedAt = Date.now();
    await cda.click( selector, identity);
    state.actions.push(name);
    return startedAt;
  };
  const selectTable = async () => {
    const locator = page.locator('button[data-testid^="construction-table-"]');
    await cda.wait( ([title]) => [...document.querySelectorAll('button[data-testid^="construction-table-"]')]
      .some(button => button.innerText.trim().endsWith(title)), [tableName]);
    return actClick('button[data-testid^="construction-table-"]', 'Select temporary table', { includes: tableName });
  };
  const attachNondefaultPatientCollection = async (initialDraft, witness) => {
    const reference = {
      project,
      generation: initialDraft.catalog.generation,
      resourceType: 'Patient',
      id: witness.patientId,
    };
    const createResponse = await fetch(selectionURL, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        snapshotToken: initialDraft.catalog.snapshotToken,
        idempotencyKey: `${explorerId}-selected-patient`,
        source: { kind: 'resources', resources: { refs: [reference] } },
      }),
      signal: AbortSignal.timeout(30_000),
    });
    const selection = await createResponse.json();
    assertPatientSelectionCreated(createResponse, selection);
    assert(selection.id, 'The selected Patient collection must have an immutable revision ID');
    assert.equal(selection.project, project);
    assert.equal(selection.generation, initialDraft.catalog.generation);
    assert.equal(selection.resourceType, 'Patient');
    assert.equal(selection.scopeDigest, initialDraft.catalog.authorizationScopeDigest);
    assert.equal(selection.memberCount, 1);
    const seed = {
      id: selection.id,
      generation: selection.generation,
      scopeDigest: selection.scopeDigest,
      memberRefs: [refKey(reference)],
    };
    const createdSelection = await readSelection(seed.id);
    assert.equal(createdSelection.revision?.id, seed.id);
    assert.deepEqual((createdSelection.members ?? []).map(member => refKey(member.ref)).sort(), seed.memberRefs);

    const handoffURL = new URL(pageURL);
    handoffURL.searchParams.set('selection', seed.id);
    const routeRequestStart = browserEvents.requests.length;
    await cda.navigate(handoffURL.toString());
    const tableSelectStartedAt = await selectTable();
    state.timingsMs.patientCollectionTableSelect = Date.now() - tableSelectStartedAt;
    assert(state.timingsMs.patientCollectionTableSelect < 5000,
      `Selecting the Patient table for its collection took ${state.timingsMs.patientCollectionTableSelect} ms`);
    const routePanelStartedAt = Date.now();
    await actClick('[data-testid="construction-rows-settings-trigger"]',
      'Open native Patient starting collection settings');
    await cda.wait(({ selectionId }) => {
      const panel = document.querySelector('section[aria-label="Starting collection"]');
      return panel?.getAttribute('data-selection-revision-id') === selectionId
        && !panel.innerText.includes('Loading the saved selection…');
    }, { selectionId: seed.id }, 5000);
    const routeEntry = await browserEvents.waitForResponse(candidate => candidate.method === 'POST'
      && candidate.path.endsWith('/population-routes') && candidate.status === 200
      && candidate.request?.outputId === outputId
      && candidate.request?.selectionRevisionId === seed.id
      && candidate.response?.outputId === outputId
      && candidate.response?.selectionRevisionId === seed.id
      && Array.isArray(candidate.response?.choices),
    { fromIndex: routeRequestStart, timeoutMs: 5000 });
    assert.equal(routeEntry.request.snapshotToken, initialDraft.catalog.snapshotToken);
    assert.equal(routeEntry.response.snapshotToken, initialDraft.catalog.snapshotToken);
    const directChoice = chooseDirectPatientPopulationRoute(routeEntry.response.choices);
    const routePanel = await cda.inspect(() => {
      const panel = document.querySelector('section[aria-label="Starting collection"]');
      const select = panel?.querySelector('select[aria-label="Population connection"]');
      return {
        selectionRevisionId: panel?.getAttribute('data-selection-revision-id'),
        attachedSelectionRevisionId: panel?.getAttribute('data-attached-selection-revision-id'),
        text: panel?.innerText,
        connection: select?.selectedOptions?.[0]?.textContent?.trim() ?? null,
      };
    });
    assert.equal(routePanel.selectionRevisionId, seed.id);
    assert(routePanel.text?.includes(directChoice.presentation.summary),
      `Native Starting collection panel must show its direct Patient choice: ${JSON.stringify(routePanel)}`);
    state.timingsMs.patientCollectionRoute = Date.now() - routePanelStartedAt;
    assert(state.timingsMs.patientCollectionRoute < 5000,
      `Opening native Patient starting collection took ${state.timingsMs.patientCollectionRoute} ms`);

    const attachStartedAt = Date.now();
    const attachRequestStart = browserEvents.requests.length;
    await actClick('section[aria-label="Starting collection"] button',
      'Attach the exact selected Patient starting collection', { name: 'Use selected resources' });
    await cda.wait(({ selectionId }) => document.querySelector('section[aria-label="Starting collection"]')
      ?.getAttribute('data-attached-selection-revision-id') === selectionId,
    { selectionId: seed.id }, 5000);
    const attachEntry = await browserEvents.waitForResponse(candidate => candidate.method === 'POST'
      && candidate.path.endsWith('/commands') && candidate.status === 200
      && candidate.request?.commands?.some(item => item.type === 'SET_TABLE_POPULATION'
        && item.outputId === outputId && item.selectionRevisionId === seed.id
        && item.routeChoiceId === directChoice.routeChoiceId),
    { fromIndex: attachRequestStart, timeoutMs: 5000 });
    const attached = await builder();
    const document = attached.workspace.documents.find(candidate => candidate.output.id === outputId);
    assert.equal(document.rootResourceType, 'Patient');
    await assertPatientPopulation(document, seed, []);
    assert.deepEqual(document.construction?.steps ?? [], []);
    await waitForVisiblePreviewRows({ rowCount: 1, exactRows: [[witness.patientId]] });
    state.timingsMs.patientCollectionAttach = Date.now() - attachStartedAt;
    assert(state.timingsMs.patientCollectionAttach < 5000,
      `Attaching the selected Patient collection took ${state.timingsMs.patientCollectionAttach} ms to render its exact row`);
    state.startingCollection = {
      selectionRevisionId: seed.id,
      project,
      generation: seed.generation,
      scopeDigest: seed.scopeDigest,
      memberRefs: seed.memberRefs,
      route: document.population.route,
      routeChoiceId: directChoice.routeChoiceId,
      literalRows: [[witness.patientId]],
      nativeCommandStatus: attachEntry.status,
    };
    const backToTableStartedAt = Date.now();
    const backToTableDeadline = backToTableStartedAt + 5000;
    await actClick('[role="dialog"][aria-label="Row definition settings"] button',
      'Return to table after attaching the Patient collection', { name: 'Back to table' });
    await cda.wait(() => !document.querySelector('[role="dialog"][aria-label="Row definition settings"]'), [],
      Math.max(1, backToTableDeadline - Date.now()));
    await waitForVisiblePreviewRows({ rowCount: 1, exactRows: [[witness.patientId]] },
      Math.max(1, backToTableDeadline - Date.now()));
    state.timingsMs.patientCollectionReturn = Date.now() - backToTableStartedAt;
    assert(state.timingsMs.patientCollectionReturn < 5000,
      `Returning from Patient collection settings to the exact Patient row took ${state.timingsMs.patientCollectionReturn} ms`);
    return { draft: attached, document, seed };
  };
  const reloadAndSelectTable = async () => {
    const startedAt = Date.now();
    await cda.navigate(pageURL);
    await selectTable();
    return startedAt;
  };
  const openRowControl = async stage => {
    const startedAt = Date.now();
    await actClick('[data-testid="construction-rows-settings-trigger"]', `Open row definition settings ${stage}`);
    await cda.wait(() => Boolean(document.querySelector(
      '[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]',
    )), [], 5000);
    state.timingsMs[`${stage}RootControl`] = Date.now() - startedAt;
    assert(state.timingsMs[`${stage}RootControl`] < 5000,
      `${stage} row-root control took ${state.timingsMs[`${stage}RootControl`]} ms to render`);
    return cda.inspect( () => {
      const select = document.querySelector(
        '[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]',
      );
      return { disabled: select.disabled, options: [...select.options].map(option => ({ value: option.value, label: option.textContent, disabled: option.disabled })) };
    });
  };
  const preview = async (stage, expectation, startedAt) => {
    assert(Number.isFinite(startedAt), `${stage} preview requires its preceding native action timestamp`);
    await waitForVisiblePreviewRows(expectation);
    const value = await cda.inspect( () => ({
      headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText.trim()),
      rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
    }));
    state.timingsMs[stage] = Date.now() - startedAt;
    assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
    state[stage] = value;
    return value;
  };
  const waitForVisiblePreviewRows = async (expectation, timeoutMs = 5000) => {
    await cda.wait(({ rowCount, exactRows, cells = [], headerIncludes = [], headerColumnValues = [], panelClosed = false }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"]');
      if (!table) return false;
      const headers = [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim().toUpperCase());
      const rows = [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
      return rows.length === rowCount
        && (exactRows === undefined || JSON.stringify(rows) === JSON.stringify(exactRows))
        && cells.every(({ row, column, value }) => rows[row]?.[column] === value)
        && headerIncludes.every(value => headers.some(header => header.includes(value.toUpperCase())))
        && headerColumnValues.every(({ headerIncludes: label, values, sort = false }) => {
          const column = headers.findIndex(header => header.includes(label.toUpperCase()));
          if (column < 0) return false;
          const actual = rows.map(row => row[column]);
          return JSON.stringify(sort ? [...actual].sort() : actual)
            === JSON.stringify(sort ? [...values].sort() : values);
        })
        && (!panelClosed || !document.querySelector('[data-testid="row-change-preview-panel"]'));
    }, expectation, Math.min(5000, timeoutMs));
  };
  const waitForRowChangePreviewRows = async ({ rowCount, patientId }) => {
    await cda.wait(({ rowCount: expectedCount, patientId: expectedPatientId }) => {
      const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
      if (!panel) return false;
      const rows = [...panel.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
      const apply = [...panel.querySelectorAll('button')]
        .find(button => button.textContent?.includes('Apply row change') && !button.disabled);
      return Boolean(apply) && rows.length === expectedCount && rows.every(row => row.includes(expectedPatientId));
    }, { rowCount, patientId }, 5000);
    return cda.inspect(() => ({
      text: document.querySelector('[data-testid="row-change-preview-panel"]')?.innerText,
      rows: [...(document.querySelector('[data-testid="row-change-preview-panel"]')?.querySelectorAll('table tr') ?? [])]
        .map(row => [...row.querySelectorAll('th,td')].map(cell => cell.innerText.trim())),
    }));
  };
  const rawQuery = (query, phase) => {
    const container = cda.target.arangoContainer;
    assert(container, 'The owned CDA target must include its Arango source database container.');
    const identity = {
      phase,
      sha256: createHash('sha256').update(query).digest('hex'),
      maxRuntimeSeconds: plainPatientRootSelectorOracleBounds.maxRuntimeSeconds,
      memoryLimitBytes: plainPatientRootSelectorOracleBounds.memoryLimitBytes,
      hostTimeoutMs: plainPatientRootSelectorOracleBounds.hostTimeoutMs,
      startedAt: Date.now(),
    };
    state.rawOracleQueries.push(identity);
    try {
      const script = buildBoundedArangoQueryScript({
        query,
        maxRuntimeSeconds: identity.maxRuntimeSeconds,
        memoryLimitBytes: identity.memoryLimitBytes,
      });
      const invocation = buildArangoShellInvocation({
        container,
        script,
        database: process.env.LOOM_CDA_DATABASE ?? 'loom_dev',
      });
      const result = spawnSync(invocation.command, invocation.args, {
        encoding: 'utf8',
        timeout: identity.hostTimeoutMs,
        maxBuffer: 200000,
      });
      const processEvidence = summarizeArangoShellResult(result, { project, generation: 'cda-fhir-v1' });
      Object.assign(identity, processEvidence);
      if (!processEvidence.processSucceeded || !processEvidence.stdoutJsonComplete) {
        identity.status = 'failed';
        identity.error = processEvidence.spawnError
          || processEvidence.stderrExcerpt
          || processEvidence.stdoutTailExcerpt
          || 'Arangosh did not return a complete JSON row array.';
        throw new Error(`Scoped Arango query failed (${processEvidence.exitStatus ?? 'no exit status'}${processEvidence.signal ? `, ${processEvidence.signal}` : ''}): ${identity.error}`);
      }
      const output = String(result.stdout ?? '');
      const resultStart = output.indexOf('[');
      assert(resultStart >= 0, `Arangosh returned no JSON rows for ${phase}: ${processEvidence.stdoutTailExcerpt}`);
      const rows = JSON.parse(output.slice(resultStart));
      assert(Array.isArray(rows), `Arango ${phase} must return an array`);
      identity.status = 'passed';
      identity.resultRows = rows.length;
      return rows;
    } catch (error) {
      identity.status = 'failed';
      identity.error ??= error instanceof Error ? error.message : String(error);
      throw error;
    } finally {
      identity.durationMs = Date.now() - identity.startedAt;
    }
  };
  const rawPatients = ids => rawQuery(`FOR d IN Patient FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN d.id`, 'exact-patient-row-reread');
  const rawObservationSubjects = ids => rawQuery(`FOR d IN Observation FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN {id:d.id,subject:d.payload.subject.reference}`, 'exact-observation-row-reread');
  const {
    observationSampleLimit,
    candidatePatientLimit,
    observationSentinelLimit,
  } = plainPatientRootSelectorOracleBounds;
  const rawPatientObservationWitnessCandidates = () => rawQuery(`FOR observation IN Observation FILTER observation.project == ${JSON.stringify(project)} AND observation.dataset_generation == "cda-fhir-v1" AND observation.resourceType == "Observation" AND observation.payload.resourceType == "Observation" AND IS_STRING(observation.payload.subject.reference) AND STARTS_WITH(observation.payload.subject.reference, "Patient/") SORT observation.id LIMIT ${observationSampleLimit} COLLECT patientReference = observation.payload.subject.reference WITH COUNT INTO sampledObservationCount FILTER sampledObservationCount >= 2 AND sampledObservationCount <= 25 SORT patientReference LIMIT ${candidatePatientLimit} RETURN {patientId:SUBSTRING(patientReference, 8),sampledObservationCount}`, 'bounded-observation-subject-candidate-sample');
  const rawPatientObservationExactReread = patientId => rawQuery(`FOR patient IN Patient FILTER patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == "cda-fhir-v1" AND patient.resourceType == "Patient" AND patient.payload.resourceType == "Patient" AND patient.id == ${JSON.stringify(patientId)} LIMIT 2 LET observations = (FOR observation IN Observation FILTER observation.project == ${JSON.stringify(project)} AND observation.dataset_generation == "cda-fhir-v1" AND observation.resourceType == "Observation" AND observation.payload.resourceType == "Observation" AND observation.payload.subject.reference == CONCAT("Patient/", patient.id) SORT observation.id LIMIT ${observationSentinelLimit} RETURN {id:observation.id,project:observation.project,generation:observation.dataset_generation}) RETURN {patientId:patient.id,patientKey:patient._id,project:patient.project,generation:patient.dataset_generation,observations}`, 'exact-candidate-patient-observation-reread');
  const command = async commands => {
    const before = await builder();
    const response = await fetch(`${authoringURL}/commands`, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ commandId: crypto.randomUUID(), semanticsVersion: before.workspace?.semanticsVersion ?? 9,
        snapshotToken: before.catalog.snapshotToken, expectedDraftVersion: before.draftVersion, expectedDraftDigest: before.draftDigest, commands }),
      signal: AbortSignal.timeout(30_000),
    });
    const body = await response.json();
    assert.equal(response.status, 200, JSON.stringify(body));
    return body;
  };
  const applyReviewedRowChange = async (stage, after) => {
    await cda.wait( () => Boolean([...document.querySelectorAll('[data-testid="row-change-preview-panel"] button')]
      .find(button => button.textContent?.includes('Apply row change') && !button.disabled)));
    state[stage] = await cda.inspect( () => {
      const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
      return { text: panel?.innerText, rows: [...panel.querySelectorAll('table tr')].map(row => [...row.querySelectorAll('th,td')].map(cell => cell.innerText.trim())) };
    });
    assert(state[stage].rows.length > 1, `${stage} did not render proposed rows`);
    const requests = browserEvents.requests.slice(after).map(({ path, status }) => ({ path, status }));
    assert(requests.some(response => response.path.endsWith('/preview') && response.status === 200), `${stage} did not request a successful candidate preview`);
    assert(!requests.some(response => response.path.endsWith('/commands')), `${stage} changed the saved table before Apply`);
    const started = Date.now();
    await actClick('[data-testid="row-change-preview-panel"] button', `Apply ${stage}`, { name: 'Apply row change' });
    return started;
  };
  const seedPatientFilter = async (witness, startingCollectionSeed) => {
    const before = await builder();
    const beforeDocument = before.workspace.documents.find(document => document.output.id === outputId);
    assert(beforeDocument, 'The Patient table must exist before authoring its filter');
    assert.equal(beforeDocument.rootResourceType, 'Patient');
    assert.equal(beforeDocument.construction?.steps?.length ?? 0, 0,
      'The plain-patient-root-selector case must author one native construction step from an empty starting state');

    let started = Date.now();
    await actClick('[data-testid="construction-action-keep-rows"]', 'Open Patient row filter', { includes: 'Filter rows' });
    const columnSelector = '[data-testid="construction-filter-editor"] select[aria-label="Column"]';
    const conditionSelector = '[data-testid="construction-filter-editor"] select[aria-label="Condition"]';
    await cda.wait(([column, condition]) => Boolean(document.querySelector(`${column}:not(:disabled)`) && document.querySelector(`${condition}:not(:disabled)`)), [columnSelector, conditionSelector], 5000);
    state.timingsMs.patientFilterEditor = Date.now() - started;
    assert(state.timingsMs.patientFilterEditor < 5000,
      `Opening the native Patient filter editor took ${state.timingsMs.patientFilterEditor} ms`);
    const sourceColumn = await cda.inspect(() => {
      const select = document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');
      return { id: select?.value, label: select?.selectedOptions?.[0]?.textContent?.trim() };
    });
    assert(sourceColumn.id && sourceColumn.label?.startsWith('Patient ID'),
      `The native filter must target the authored Patient ID column: ${JSON.stringify(sourceColumn)}`);
    const savedColumn = beforeDocument.columns.find(column => column.columnId === sourceColumn.id);
    assert(savedColumn, 'The selected Patient ID control must resolve to the exact saved output column');
    await cda.selectOption(conditionSelector, 'EQUALS');
    const valueSelector = '[data-testid="construction-filter-editor"] input[aria-label="Value"]';
    await cda.wait(([selector]) => Boolean(document.querySelector(`${selector}:not(:disabled)`)), [valueSelector], 5000);
    started = Date.now();
    await cda.fill(valueSelector, witness.patientId);
    state.actions.push('Set Patient ID equality filter');
    await cda.wait(() => ['ready', 'error', 'needs-repair'].includes(
      document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus), [], 5000);
    const renderedProposal = await cda.inspect(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return { status: panel?.dataset.proposalStatus, rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())) };
    });
    assert.equal(renderedProposal.status, 'ready');
    assert.deepEqual(renderedProposal.rows, [[witness.patientId]],
      'The visible native proposal must show the exact Patient selected by the raw oracle');
    state.timingsMs.patientFilterProposal = Date.now() - started;
    assert(state.timingsMs.patientFilterProposal < 5000,
      `Patient filter choice-to-preview took ${state.timingsMs.patientFilterProposal} ms`);

    started = Date.now();
    await actClick('[data-testid="construction-apply-proposal"]', 'Apply Patient ID equality filter', { name: 'Apply change' });
    await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), [], 5000);
    await waitForVisiblePreviewRows({ rowCount: 1, exactRows: [[witness.patientId]] });
    state.timingsMs.patientFilterApply = Date.now() - started;
    assert(state.timingsMs.patientFilterApply < 5000,
      `Applying the Patient filter took ${state.timingsMs.patientFilterApply} ms to render`);
    const saved = await builder();
    const savedDocument = saved.workspace.documents.find(document => document.output.id === outputId);
    assert.deepEqual(savedDocument.columns, beforeDocument.columns,
      'Authoring the construction filter must retain the original output columns');
    assert.deepEqual(savedDocument.population, beforeDocument.population,
      'Authoring the construction filter must retain the starting Patient population and route');
    assert.equal(savedDocument.construction.steps.length, 1);
    const filterStep = savedDocument.construction.steps[0];
    assert.equal(filterStep.operation.kind, 'FILTER');
    assert.deepEqual(filterStep.operation.filter, {
      columnId: sourceColumn.id,
      operator: 'EQUALS',
      values: [{ kind: 'STRING', string: witness.patientId }],
    });
    const previewStartedAt = await reloadAndSelectTable();
    const reloadedPreview = await preview('patientPreview', {
      rowCount: 1, exactRows: [[witness.patientId]], headerIncludes: ['PATIENT ID'],
    }, previewStartedAt);
    const reloaded = await builder();
    const reloadedDocument = reloaded.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(reloadedDocument.rootResourceType, 'Patient');
    assert.deepEqual(reloadedDocument.construction, savedDocument.construction,
      'Reload must preserve the authored Patient ID filter before plain Patient root selector');
    assert.deepEqual(reloadedDocument.columns, beforeDocument.columns);
    assert.deepEqual(reloadedDocument.population, beforeDocument.population);
    await assertPatientPopulation(reloadedDocument, startingCollectionSeed, []);
    assert.deepEqual(reloadedPreview.headers, ['PATIENT ID']);
    assert.deepEqual(reloadedPreview.rows, [[witness.patientId]],
      'Reload must render the exact Patient ID selected by the authored filter');
    assert.deepEqual(new Set(reloadedPreview.rows.map(row => row[0])),
      new Set(rawPatients(reloadedPreview.rows.map(row => row[0]))));
    state.patientFilter = {
      status: 'passed', step: filterStep, draftDigest: reloaded.draftDigest,
      patientId: witness.patientId, observationIDs: witness.observationIDs,
      preservedColumns: true, preservedPopulation: true,
    };
    recordRequiredCheck(1, 'persistence', true, {
      patientId: witness.patientId, step: filterStep, columns: reloadedDocument.columns,
      population: reloadedDocument.population, previewRows: reloadedPreview.rows,
      startingCollection: state.startingCollection,
      draftDigest: reloaded.draftDigest,
    });
    return { reloaded, document: reloadedDocument, preview: reloadedPreview };
  };
  const proposeObservationRoot = async (stage, witness, currentDraft = state.before) => {
    const controls = await openRowControl(stage);
    state.beforeControl ??= controls;
    assert.equal(controls.disabled, false, 'Row-root control must remain enabled with the authored filter');
    const observationChoice = choosePatientLinkedObservationOption(controls.options);
    const expectedAssessment = directObservationAssessmentIdentity(currentDraft);
    const assessmentStart = browserEvents.requests.length;
    const started = Date.now();
    await cda.selectOption(
      '[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]',
      observationChoice.value,
    );
    state.actions.push(`Choose Observation rows ${stage}`);
    const assessmentEntry = await browserEvents.waitForResponse(
      candidate => matchesPlainPatientObservationAssessment(candidate, expectedAssessment)
        || matchesPlainPatientObservationAssessment(candidate, expectedAssessment, { status: 'BLOCKED' }),
      { fromIndex: assessmentStart, timeoutMs: 5000 },
    );
    const initialAssessment = assessmentEntry.response;
    let assessmentEntryAfterRepair = assessmentEntry;
    let assessment = initialAssessment;
    if (assessment.status === 'BLOCKED') {
      const repairPanelStarted = Date.now();
      await cda.wait(() => document.body.innerText.includes('Choose how to preserve this table'));
      state.timingsMs[`${stage}RepairPanel`] = Date.now() - repairPanelStarted;
      assert(state.timingsMs[`${stage}RepairPanel`] < 5000,
        `${stage} root occurrence repair panel took ${state.timingsMs[`${stage}RepairPanel`]} ms to render`);
      state[`${stage}RepairPanel`] = await cda.inspect(() => {
        const panel = document.getElementById('row-change-repair-title')?.closest('section');
        return { text: panel?.innerText, buttons: [...(panel?.querySelectorAll('button') ?? [])]
          .map(button => ({ text: button.innerText, disabled: button.disabled })) };
      });
      const repairButton = choosePatientSubjectObservationRepairButton(state[`${stage}RepairPanel`].buttons);
      const repairStarted = browserEvents.requests.length;
      const repairChoiceStarted = Date.now();
      await actClick('section button', `Choose Patient Subject Observation path ${stage}`, { name: repairButton.text });
      assessmentEntryAfterRepair = await browserEvents.waitForResponse(
        candidate => matchesPlainPatientObservationAssessment(candidate, expectedAssessment),
        { fromIndex: repairStarted, timeoutMs: 5000 },
      );
      assessment = assessmentEntryAfterRepair.response;
      state[`${stage}RepairChoiceToAssessmentMs`] = Date.now() - repairChoiceStarted;
      assert(state[`${stage}RepairChoiceToAssessmentMs`] < 5000,
        `${stage} Patient Subject repair choice-to-assessment took ${state[`${stage}RepairChoiceToAssessmentMs`]} ms`);
    }
    state[`${stage}InitialAssessment`] = initialAssessment;
    state[`${stage}Assessment`] = assessment;
    state[`${stage}RootSelection`] = {
      label: observationChoice.label,
      rootNodeId: assessmentEntryAfterRepair.request.rootNodeId,
      rootOccurrenceId: assessmentEntryAfterRepair.request.rootOccurrenceId ?? null,
      initialStatus: initialAssessment.status,
      status: assessment.status,
    };
    state.timingsMs[`${stage}Assessment`] = Date.now() - started;
    assert(state.timingsMs[`${stage}Assessment`] < 5000,
      `${stage} root choice-to-assessment took ${state.timingsMs[`${stage}Assessment`]} ms`);
    assert.equal(assessment.status, 'READY', 'Direct Observation selection or its Patient Subject repair must produce a ready row-change preview');
    assert(!browserEvents.requests.slice(assessmentStart).some(request => request.path.endsWith('/commands')),
      'Selecting Observation must not modify the saved table before Apply');
    state[`${stage}Preview`] = await waitForRowChangePreviewRows({
      rowCount: witness.observationIDs.length, patientId: witness.patientId,
    });
    state.timingsMs[stage] = Date.now() - started;
    assert(state.timingsMs[stage] < 5000, `${stage} choice-to-row-preview took ${state.timingsMs[stage]} ms`);
    return assessmentStart;
  };
  const cancelReviewedRowChange = async (stage, after, original, witness, originalPreview) => {
    const panel = await cda.inspect(() => {
      const element = document.querySelector('[data-testid="row-change-preview-panel"]');
      return { text: element?.innerText, rows: [...(element?.querySelectorAll('table tr') ?? [])]
        .map(row => [...row.querySelectorAll('th,td')].map(cell => {
          const authoredLabel = cell.tagName === 'TH' ? cell.querySelector(':scope > span') : undefined;
          return (authoredLabel?.textContent ?? cell.innerText).trim();
        })) };
    });
    assert(panel.rows.length > 1, `${stage} must show the proposed related-resource rows before Cancel`);
    assert.equal(panel.rows.length - 1, witness.observationIDs.length,
      `${stage} must preview the exact raw Observation multiplicity before Cancel`);
    const expectedColumnLabels = original.document.columns
      .filter(column => column.table?.visible !== false)
      .map(column => column.label);
    assert.deepEqual(panel.rows[0], expectedColumnLabels,
      `${stage} must retain the Patient ID field in the row-change preview`);
    assert.deepEqual(panel.rows.slice(1), witness.observationIDs.map(() => [witness.patientId]),
      `${stage} preview rows must exactly match the independently selected Observation count and retained Patient value`);
    assert(!browserEvents.requests.slice(after).some(request => request.path.endsWith('/commands')),
      `${stage} proposal must not change the saved workspace before Cancel or Apply`);
    state[stage] = panel;
    const requestsBeforeCancel = browserEvents.requests.length;
    const started = Date.now();
    await actClick('[data-testid="row-change-preview-panel"] button', `Cancel ${stage}`, { name: 'Keep current rows' });
    await cda.wait(() => !document.querySelector('[data-testid="row-change-preview-panel"]'));
    await waitForVisiblePreviewRows({
      rowCount: originalPreview.rows.length, exactRows: originalPreview.rows, panelClosed: true,
    });
    state.timingsMs[`${stage}Cancel`] = Date.now() - started;
    assert(state.timingsMs[`${stage}Cancel`] < 5000,
      `${stage} Cancel-to-original-rows took ${state.timingsMs[`${stage}Cancel`]} ms`);
    const cancelled = await builder();
    const cancelledDocument = cancelled.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(cancelled.draftVersion, original.draftVersion, `${stage} Cancel must leave the draft version unchanged`);
    assert.equal(cancelled.draftDigest, original.draftDigest, `${stage} Cancel must leave the draft digest unchanged`);
    assert.deepEqual(cancelled.workspace, original.workspace, `${stage} Cancel must leave the complete saved workspace unchanged`);
    assert.equal(cancelledDocument.rootResourceType, 'Patient');
    assert.deepEqual(cancelledDocument.construction, original.document.construction,
      `${stage} Cancel must retain the authored Patient filter`);
    assert.deepEqual(cancelledDocument.columns, original.document.columns,
      `${stage} Cancel must retain every authored output column`);
    assert.deepEqual(cancelledDocument.population, original.document.population,
      `${stage} Cancel must retain the selected Patient population and route`);
    assert(!browserEvents.requests.slice(requestsBeforeCancel).some(request => request.path.endsWith('/commands')),
      `${stage} Cancel must not write a Builder command`);
    state[`${stage}Cancel`] = { status: 'passed', draftVersion: cancelled.draftVersion,
      draftDigest: cancelled.draftDigest, preservedConstruction: true, preservedColumns: true, preservedPopulation: true };
    if (stage === 'cancelledObservationRootProposal') {
      recordRequiredCheck(2, 'persistence', true, {
        draftVersion: cancelled.draftVersion, draftDigest: cancelled.draftDigest,
        construction: cancelledDocument.construction, columns: cancelledDocument.columns,
        population: cancelledDocument.population, candidatePreviewRows: panel.rows.length - 1,
      });
    }
  };

  try {
    const witnessCandidates = rawPatientObservationWitnessCandidates();
    const nondefaultPatientCollectionSentinel = rawQuery(`FOR patient IN Patient FILTER patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == "cda-fhir-v1" AND patient.resourceType == "Patient" AND patient.payload.resourceType == "Patient" SORT patient.id LIMIT 2 RETURN patient.id`, 'nondefault-patient-collection-sentinel');
    assert.equal(nondefaultPatientCollectionSentinel.length, 2,
      'The one-Patient starting collection must be a strict subset of at least two scoped source Patients');
    const witnessSelection = selectPlainPatientRootSelectorWitness(
      witnessCandidates,
      patientId => rawPatientObservationExactReread(patientId),
      { project, generation: 'cda-fhir-v1' },
    );
    const witness = witnessSelection.witness;
    const witnessValid = Boolean(witness);
    state.oracleCandidateSelection = witnessSelection;
    recordRequiredCheck(0, 'correctness', witnessValid, {
      candidateCount: witnessCandidates.length,
      scopedPatientSentinel: nondefaultPatientCollectionSentinel,
      candidateLimit: candidatePatientLimit,
      observationSampleLimit,
      selectionAttempts: witnessSelection.attempts,
      exactWitness: witness,
      perPatientObservationSentinelLimit: observationSentinelLimit,
      queryCaps: { maxRuntimeSeconds: 8, memoryLimitBytes: 256 * 1024 * 1024, hostTimeoutMs: 30_000 },
      project, generation: 'cda-fhir-v1', visiblePreviewLimit: 25,
    });
    assert(witnessValid,
      `The bounded project/generation raw oracle must find one Patient with 2–25 exact Observations among its first ${candidatePatientLimit} sampled candidates; a 26-row sentinel and any query failure are rejected: ${JSON.stringify(witnessSelection)}`);
    state.oracle = buildPlainPatientRootSelectorOracleMetadata(witness, witnessSelection);
    state.oracle.nondefaultPatientCollectionSentinel = nondefaultPatientCollectionSentinel;
    state.explorerSetup = await createAndNavigatePlainPatientRootSelectorExplorer({
      apiOrigin,
      pageURL,
      navigate: url => cda.navigate(url),
    });
    await cda.wait(() => Boolean(
      document.querySelector('#first-table-name')
      && document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)'),
    ), [], 5000);
    state.tablesBefore = await cda.inspect( () => [...document.querySelectorAll('button[data-testid^="construction-table-"]')].map(button => button.innerText.trim().split(String.fromCharCode(10)).at(-1)));
    await cda.fill( '#first-table-name', tableName);
    await actClick('button[aria-label="Choose Patient rows"]', 'Choose Patient rows');
    await cda.wait( () => document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]').length > 1);
    const created = await builder();
    const initial = created.workspace.documents.find(document => document.output.title === tableName);
    assert(initial, 'The temporary Patient table was not created');
    outputId = initial.output.id;
    assert.equal(isPlainPatientRoot(initial), true,
      'The new table must start at Patient with no authored descendant route');
    assert.equal(initial.columns.length, 1);
    assert.equal(initial.columns[0].label, 'Patient ID', 'The plain Patient table must retain its Patient ID field');
    state.plainPatientRootStart = {
      route: initial.route,
      rootResourceType: initial.rootResourceType,
      columns: initial.columns,
      population: initial.population ?? null,
    };

    const collectionAttached = await attachNondefaultPatientCollection(created, witness);
    state.startingCollectionSeed = collectionAttached.seed;
    const seeded = await seedPatientFilter(witness, collectionAttached.seed);
    state.before = { ...seeded.reloaded, document: seeded.document };
    assert.equal(isPlainPatientRoot(state.before.document), true,
      'The active Patient filter must remain on the plain Patient root before choosing Observation');
    await assertPatientPopulation(state.before.document, state.startingCollectionSeed, []);
    assert.equal(browserEvents.requests.some(request => request.path.endsWith('/commands')
      && request.request?.commands?.some(entry => entry.type === 'ADD_ROUTE')), false,
    'The direct starting-record case must not author a related route before changing the root');
    const originalPreview = seeded.preview;
    assert.deepEqual(originalPreview.headers, ['PATIENT ID']);
    assert.deepEqual(originalPreview.rows, [[witness.patientId]], 'The authored Patient filter must select the exact raw witness');
    assert.deepEqual(new Set(originalPreview.rows.map(row => row[0])), new Set(rawPatients(originalPreview.rows.map(row => row[0]))));

    const cancelledProposalStart = await proposeObservationRoot('cancelledObservationRootProposal', witness);
    await cancelReviewedRowChange('cancelledObservationRootProposal', cancelledProposalStart, state.before, witness, originalPreview);
    const cancelledPreviewStartedAt = await reloadAndSelectTable();
    const cancelledPreview = await preview('cancelledPatientPreview', {
      rowCount: 1, exactRows: [[witness.patientId]], headerIncludes: ['PATIENT ID'],
    }, cancelledPreviewStartedAt);
    const cancelledReload = await builder();
    const cancelledReloadDocument = cancelledReload.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(isPlainPatientRoot(cancelledReloadDocument), true,
      'Reload after Cancel must restore the original plain Patient root');
    await assertPatientPopulation(cancelledReloadDocument, state.startingCollectionSeed, []);
    assert.deepEqual(cancelledReload.workspace, state.before.workspace,
      'Reload after root-change Cancel must preserve the exact authored filter, columns, population, and route');
    assert.deepEqual(cancelledPreview.rows, originalPreview.rows,
      'Reload after root-change Cancel must render the exact filtered Patient row');

    const assessmentStart = await proposeObservationRoot('observationRootProposal', witness, {
      ...cancelledReload, document: cancelledReloadDocument,
    });
    const observationApplyStartedAt = await applyReviewedRowChange('observationRootProposal', assessmentStart);
    const observationRows = witness.observationIDs.map(() => [witness.patientId]);
    await waitForVisiblePreviewRows({
      rowCount: observationRows.length, exactRows: observationRows, panelClosed: true,
    });
    state.timingsMs.observationRootApply = Date.now() - observationApplyStartedAt;
    assert(state.timingsMs.observationRootApply < 5000,
      `Applying Observation rows took ${state.timingsMs.observationRootApply} ms to render the exact raw row count and Patient IDs`);
    const changed = await waitForRoot(outputId, 'Observation');
    state.rootChangeRequests = browserEvents.requests.slice(assessmentStart).map(({ path, status }) => ({ path, status }));
    state.changed = { draftVersion: changed.draftVersion, document: changed.workspace.documents.find(document => document.output.id === outputId) };
    assert.equal(state.changed.document?.rootResourceType, 'Observation', 'Apply did not change the root resource');
    assert.deepEqual(state.changed.document.construction, state.before.document.construction,
      'Applying Observation rows must preserve the exact authored Patient ID filter');
    assert.equal(directObservationRootPreservesPatientState(state.before.document, state.changed.document), true,
      'Direct Observation selection must preserve the Patient field, active filter, and starting collection');
    const selectedPatientOccurrence = state.changed.document.route.children.find(node => node.resourceType === 'Patient');
    assert(selectedPatientOccurrence, 'Observation root must retain the selected Patient route occurrence');
    const selectedPatientEdge = state.before.catalog.edges.find(edge => edge.edgeId === selectedPatientOccurrence.catalogEdgeId);
    assert(selectedPatientEdge, 'The retained Patient route must resolve to its exact catalog relationship');
    const observationPopulationRoute = [{
      resourceType: 'Patient',
      relationship: selectedPatientEdge.label,
      catalogEdgeId: selectedPatientEdge.edgeId,
      ...(typeof selectedPatientEdge.storageDirection === 'string' && selectedPatientEdge.storageDirection
        ? { storageDirection: selectedPatientEdge.storageDirection } : {}),
    }];
    state.expectedObservationPopulationRoute = observationPopulationRoute;
    await assertPatientPopulation(state.changed.document, state.startingCollectionSeed, observationPopulationRoute);
    recordRequiredCheck(3, 'persistence', true, {
      rootResourceType: state.changed.document.rootResourceType,
      construction: state.changed.document.construction,
      columns: state.changed.document.columns, population: state.changed.document.population,
      startingCollection: state.startingCollection,
    });
    const observationPreviewStartedAt = await reloadAndSelectTable();
    const observationPreview = await preview('observationPreview', {
      rowCount: witness.observationIDs.length,
      exactRows: witness.observationIDs.map(() => [witness.patientId]),
      headerIncludes: ['PATIENT ID'],
    }, observationPreviewStartedAt);
    const observationReload = await builder();
    const observationReloadDocument = observationReload.workspace.documents.find(document => document.output.id === outputId);
    const selectedObservationRootNodeId = state.observationRootProposalRootSelection?.rootNodeId;
    const observationNode = observationReload.catalog.nodes.find(node => node.nodeId === selectedObservationRootNodeId);
    assert(observationNode && observationNode.resourceType === 'Observation',
      'The reloaded Observation catalog must retain the exact selected root node');
    assert.equal(observationReloadDocument.rootResourceType, 'Observation');
    await assertPatientPopulation(observationReloadDocument, state.startingCollectionSeed, observationPopulationRoute);
    assert.deepEqual(observationReloadDocument.construction, state.before.document.construction,
      'Observation-root reload must preserve the exact authored Patient filter');
    assert.deepEqual(observationReloadDocument.columns, state.changed.document.columns,
      'Observation-root reload must preserve the rebased authored output columns');
    assert.deepEqual(observationReloadDocument.population, state.changed.document.population,
      'Observation-root reload must preserve the rebased population route');
    assert.equal(observationPreview.rows.length, witness.observationIDs.length,
      'The Observation-root preview must preserve the raw Patient-to-Observation multiplicity');
    assert.deepEqual(observationPreview.rows.map(row => row[0]), witness.observationIDs.map(() => witness.patientId),
      'The authored Patient ID filter must retain the exact parent Patient on every Observation row');
    assert.deepEqual(new Set(observationPreview.rows.map(row => row[0])), new Set(rawPatients(observationPreview.rows.map(row => row[0]))), 'Rebased Patient IDs differ from CDA source');
    const observationFieldPickerStartedAt = await actClick(
      'button[aria-label^="Add columns:"]', 'Open Add columns on Observation rows', { includes: 'Add columns' },
    );
    const observationSchemaStart = browserEvents.requests.length;
    await actClick('[role="group"][aria-label="Column types"] button',
      'Switch to fields and related data', { name: 'Fields and related data' });
    await cda.wait(() => Boolean(document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')), [], 5000);
    await actClick('[data-testid="feature-catalog-raw-fields"] summary',
      'Expand raw FHIR fields', { name: 'Raw FHIR fields (advanced)' });
    await cda.wait(() => {
      const field = document.querySelector('input[aria-label="Select Observation.id"]');
      return Boolean(field && !field.disabled);
    }, [], 5000);
    state.observationSchemaStart = observationSchemaStart;
    state.timingsMs.observationIDFieldPicker = Date.now() - observationFieldPickerStartedAt;
    assert(state.timingsMs.observationIDFieldPicker < 5000,
      `Opening the Observation ID field picker took ${state.timingsMs.observationIDFieldPicker} ms`);
    await actClick('input[aria-label="Select Observation.id"]', 'Select Observation ID');
    const observationColumnChoiceStart = browserEvents.requests.length;
    const observationColumnProposalStartedAt = await actClick(
      '[aria-label="Add columns editor"] button', 'Preview Observation ID column', { name: 'Add 1 selected feature' },
    );
    await cda.wait(() => ['ready', 'error'].includes(
      document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus,
    ), [], 5000);
    state.timingsMs.observationColumnProposal = Date.now() - observationColumnProposalStartedAt;
    assert(state.timingsMs.observationColumnProposal < 5000,
      `Observation ID choice-to-preview took ${state.timingsMs.observationColumnProposal} ms`);
    state.observationColumnProposal = await cda.inspect(() => {
      const panel = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
      return { status: panel?.dataset.proposalStatus, text: panel?.innerText.slice(0, 1000) };
    });
    assert.equal(state.observationColumnProposal.status, 'ready',
      `Observation ID choice did not render a ready proposal: ${state.observationColumnProposal.text ?? 'no proposal text'}`);
    const observationColumnChoice = rootRebaseColumnChoiceIdentity(browserEvents.requests, {
      fromIndex: observationColumnChoiceStart,
      outputId,
      snapshotToken: observationReload.catalog.snapshotToken,
      draftVersion: observationReload.draftVersion,
      draftDigest: observationReload.draftDigest,
    });
    const columnApplyStart = browserEvents.requests.length;
    const columnApplyStartedAt = await actClick(
      '[data-testid="construction-choice-proposal-panel"] button',
      'Apply Observation ID column', { name: 'Apply columns' },
    );
    const columnApply = await waitForRootRebaseColumnApply({
      requests: browserEvents.requests,
      errors: state.errors,
      fromIndex: columnApplyStart,
      schemaFromIndex: state.observationSchemaStart,
      expected: observationColumnChoice,
      schemaIdentity: {
        snapshotToken: observationReload.catalog.snapshotToken,
        nodeId: observationNode.nodeId,
      },
      deadlineAt: columnApplyStartedAt + 5000,
      waitForRequest: (predicate, options) => browserEvents.waitForResponse(predicate, options),
      closeCatalog: async timeoutMs => {
        await cda.click('[data-testid="construction-close-operation-editor"]',
          { name: 'Close operation editor' }, timeoutMs);
        state.actions.push('Back to table after applying Observation ID');
      },
      waitForPairedRows: timeoutMs => waitForVisiblePreviewRows({
        rowCount: witness.observationIDs.length,
        headerColumnValues: [
          { headerIncludes: 'PATIENT ID', values: witness.observationIDs.map(() => witness.patientId) },
          { headerIncludes: 'OBSERVATION ID', values: witness.observationIDs, sort: true },
        ],
      }, timeoutMs),
    });
    state.timingsMs.observationColumnApply = Date.now() - columnApplyStartedAt;
    assert(state.timingsMs.observationColumnApply < 5000,
      `Applying Observation ID took ${state.timingsMs.observationColumnApply} ms through paired preview render`);
    assert.equal(columnApply.command.status, 200, 'Applying Observation ID failed');
    state.observationColumnApply = {
      requestId: columnApply.command.requestId,
      browserRequestId: columnApply.command.browserRequestId,
      status: columnApply.command.status,
      schemaRequests: columnApply.schemaRequests.map(({ browserRequestId, status, completedAt }) => ({
        browserRequestId, status, completedAt,
      })),
    };
    state.observationColumnApplyPreview = await cda.inspect(() => ({
      headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
        .map(cell => cell.innerText.trim()),
      rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
    }));
    const withObservationID = await builder();
    state.withObservationID = withObservationID.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(rootRebasePreservesFilterWhenAddingColumn(state.before.document, state.withObservationID), true,
      'Adding Observation ID must preserve the exact authored Patient filter and append only its output definition');
    assert.deepEqual(state.withObservationID.population, state.changed.document.population,
      'Adding Observation ID must preserve the selected Patient population and route');
    const pairedPreviewStartedAt = await reloadAndSelectTable();
    const pairedPreview = await preview('pairedPreview', {
      rowCount: witness.observationIDs.length,
      headerColumnValues: [
        { headerIncludes: 'PATIENT ID', values: witness.observationIDs.map(() => witness.patientId) },
        { headerIncludes: 'OBSERVATION ID', values: witness.observationIDs, sort: true },
      ],
    }, pairedPreviewStartedAt);
    const pairedReload = await builder();
    const pairedReloadDocument = pairedReload.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(pairedReloadDocument.rootResourceType, 'Observation');
    await assertPatientPopulation(pairedReloadDocument, state.startingCollectionSeed, observationPopulationRoute);
    assert.deepEqual(pairedReloadDocument.construction, state.withObservationID.construction,
      'Reload must preserve the saved Patient filter and its two column output definitions');
    assert.deepEqual(pairedReloadDocument.population, state.withObservationID.population);
    assert.deepEqual(pairedReloadDocument.columns, state.withObservationID.columns);
    const patientIndex = pairedPreview.headers.findIndex(header => header === 'PATIENT ID');
    const observationIndex = pairedPreview.headers.findIndex((header, index) => index !== patientIndex && header.endsWith('ID'));
    assert(patientIndex >= 0 && observationIndex >= 0, 'The rebased preview lacks Patient and Observation identifiers');
    assert.equal(pairedPreview.rows.length, witness.observationIDs.length,
      'The paired Observation-root preview must retain exact raw multiplicity');
    const displayedObservationIDs = pairedPreview.rows.map(row => row[observationIndex]).sort();
    assert.deepEqual(displayedObservationIDs, [...witness.observationIDs].sort(),
      'Observation-root IDs must match the exact raw Patient-to-Observation oracle');
    assert.deepEqual(pairedPreview.rows.map(row => row[patientIndex]), witness.observationIDs.map(() => witness.patientId),
      'Every exact Observation row must retain the selected Patient ID');
    const observations = rawObservationSubjects(pairedPreview.rows.map(row => row[observationIndex]));
    const sourceByID = new Map(observations.map(record => [record.id, record.subject]));
    assert.equal(sourceByID.size, pairedPreview.rows.length, 'Displayed Observation IDs do not match CDA records');
    for (const row of pairedPreview.rows) assert.equal(sourceByID.get(row[observationIndex]), `Patient/${row[patientIndex]}`, `Patient relationship differs for Observation ${row[observationIndex]}`);
    state.pairOracle = observations;
    recordRequiredCheck(4, 'correctness', true, {
      patientId: witness.patientId, observationIDs: displayedObservationIDs,
      subjects: observations, rows: pairedPreview.rows,
    });
    state.afterControl = await openRowControl('restoringPatientRoot');
    const patientChoice = state.afterControl.options.find(option => option.label.startsWith('Patient'));
    assert(patientChoice && !patientChoice.disabled, 'Patient rows cannot be restored');
    const expectedRestorationAssessment = rowAssessmentIdentity(pairedReload, 'Patient');
    const restorationAssessmentStart = browserEvents.requests.length;
    const restorationStartedAt = Date.now();
    await cda.selectOption(
      '[role="dialog"][aria-label="Row definition settings"] select[aria-label="Choose starting record type"]',
      patientChoice.value,
    );
    state.actions.push('Restore Patient rows');
    state.patientAssessment = await waitForRowAssessment(restorationAssessmentStart, expectedRestorationAssessment);
    state.timingsMs.patientRootAssessment = Date.now() - restorationStartedAt;
    assert(state.timingsMs.patientRootAssessment < 5000,
      `Patient root choice-to-assessment took ${state.timingsMs.patientRootAssessment} ms`);
    if (state.patientAssessment.status === 'BLOCKED') {
      const repairPanelStarted = Date.now();
      await cda.wait( () => document.body.innerText.includes('Choose how to preserve this table'));
      state.timingsMs.patientRootRepairChoice = Date.now() - repairPanelStarted;
      assert(state.timingsMs.patientRootRepairChoice < 5000,
        `Patient root Subject repair choice took ${state.timingsMs.patientRootRepairChoice} ms to render`);
      state.restorationRepairPanel = await cda.inspect( () => {
        const panel = document.getElementById('row-change-repair-title')?.closest('section');
        return { text: panel?.innerText, buttons: [...(panel?.querySelectorAll('button') ?? [])].map(button => ({ text: button.innerText, disabled: button.disabled })) };
      });
      const repairButton = choosePatientObservationSubjectRestorationButton(state.restorationRepairPanel.buttons);
      const repairStarted = browserEvents.requests.length;
      const repairChoiceStarted = Date.now();
      await actClick('section button', 'Restore Subject relationship', { name: repairButton.text });
      state.restorationRepairedAssessment = await waitForRowAssessment(repairStarted, expectedRestorationAssessment);
      await waitForRowChangePreviewRows({ rowCount: 1, patientId: witness.patientId });
      state.timingsMs.patientRootRepairChoiceToPreview = Date.now() - repairChoiceStarted;
      assert(state.timingsMs.patientRootRepairChoiceToPreview < 5000,
        `Patient root Subject choice-to-row-preview took ${state.timingsMs.patientRootRepairChoiceToPreview} ms`);
    } else {
      await waitForRowChangePreviewRows({ rowCount: 1, patientId: witness.patientId });
      state.timingsMs.patientRootProposal = Date.now() - restorationStartedAt;
      assert(state.timingsMs.patientRootProposal < 5000,
        `Restoring Patient rows to a rendered proposal took ${state.timingsMs.patientRootProposal} ms`);
    }
    const patientApplyStartedAt = await applyReviewedRowChange('patientRootProposal', restorationAssessmentStart);
    await waitForVisiblePreviewRows({
      rowCount: 1,
      cells: [{ row: 0, column: patientIndex, value: witness.patientId }],
      panelClosed: true,
    });
    state.timingsMs.patientRootApply = Date.now() - patientApplyStartedAt;
    assert(state.timingsMs.patientRootApply < 5000,
      `Applying Patient rows took ${state.timingsMs.patientRootApply} ms to render the exact restored Patient row`);
    const restoredState = await waitForRoot(outputId, 'Patient');
    const restoredDocument = restoredState.workspace.documents.find(document => document.output.id === outputId);
    await assertPatientPopulation(restoredDocument, state.startingCollectionSeed, []);
    assert.equal(rootRebasePreservesFilterWhenAddingColumn(state.before.document, restoredDocument), true,
      'Restoring Patient rows must preserve the authored Patient filter and both output definitions');
    const restorationAssessment = state.restorationRepairedAssessment ?? state.patientAssessment;
    assert.equal(rootRebasePreservesAuthoredDocument(state.withObservationID, restoredDocument, {
      selectedOccurrenceId: restorationAssessment.proposal.rootOccurrenceId,
      routeRebase: restorationAssessment.proposal.routeRebase,
      catalogEdges: state.before.catalog.edges,
    }), true, 'Patient root restoration must preserve authored fields while restoring occurrence routes');
    state.restorationRequests = browserEvents.requests.slice(restorationAssessmentStart).map(({ path, status }) => ({ path, status }));
    const restoredPreviewStartedAt = await reloadAndSelectTable();
    const restoredPreview = await preview('restoredPreview', {
      rowCount: 1,
      headerColumnValues: [{ headerIncludes: 'PATIENT ID', values: [witness.patientId] }],
    }, restoredPreviewStartedAt);
    const restored = await builder();
    state.restored = { draftVersion: restored.draftVersion, document: restored.workspace.documents.find(document => document.output.id === outputId) };
    assert.equal(state.restored.document?.rootResourceType, 'Patient');
    await assertPatientPopulation(state.restored.document, state.startingCollectionSeed, []);
    assert.deepEqual(state.restored.document.construction, restoredDocument.construction,
      'Reload after restoration must preserve the exact authored Patient filter and both output definitions');
    assert.deepEqual(state.restored.document.population, restoredDocument.population);
    assert.deepEqual(state.restored.document.columns, restoredDocument.columns);
    const restoredPatientIndex = restoredPreview.headers.indexOf('PATIENT ID');
    assert(restoredPatientIndex >= 0, 'Restored Patient ID column is missing');
    assert.deepEqual(restoredPreview.rows.map(row => row[restoredPatientIndex]), originalPreview.rows.map(row => row[0]));
    assert.deepEqual(restoredPreview.rows.map(row => row[restoredPatientIndex]), [witness.patientId],
      'Restored Patient rows must still satisfy the exact authored Patient ID filter');
    recordRequiredCheck(5, 'persistence', true, {
      rootResourceType: state.restored.document.rootResourceType,
      construction: state.restored.document.construction, columns: state.restored.document.columns,
      population: state.restored.document.population, previewRows: restoredPreview.rows,
      startingCollection: state.startingCollection,
    });
    const measuredBudgets = Object.entries(state.timingsMs).filter(([, durationMs]) => Number.isFinite(durationMs));
    const withinBudget = measuredBudgets.length > 0 && measuredBudgets.every(([, durationMs]) => durationMs < 5000);
    recordRequiredCheck(6, 'performance', withinBudget, {
      checkpoints: Object.fromEntries(measuredBudgets), maximumDurationMs: Math.max(...measuredBudgets.map(([, durationMs]) => durationMs)),
      budgetMs: 5000,
    });
    const networkClean = state.errors.length === 0;
    recordRequiredCheck(7, 'correctness', networkClean, {
      errors: state.errors,
      networkDiagnostics: state.network,
      nativeRequests: browserEvents.requests.map(({
        requestId, browserRequestId, path, method, status, failure, completedAt,
      }) => ({
        requestId, browserRequestId, path, method,
        status: status ?? null, failure: failure ?? null, completedAt: completedAt ?? null,
      })),
    });
    assert(networkClean, `Unexpected browser or owned API errors: ${JSON.stringify(state.errors)}`);
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
    state.failureUI = await cda.inspect( () => ({ text: document.body.innerText.slice(0, 3500), alerts: [...document.querySelectorAll('[role="alert"]')].map(node => node.innerText) })).catch(() => undefined);
    throw error;
  } finally {
    if (outputId) {
      try {
        await command([{ type: 'DELETE_TABLE', outputId }]);
        state.cleanup = 'temporary table deleted';
      } catch (error) {
        state.cleanup = error instanceof Error ? error.message : String(error);
      }
    }
    await cda.attachReport('plain-patient-root-selector-lifecycle', state);
  }
}
