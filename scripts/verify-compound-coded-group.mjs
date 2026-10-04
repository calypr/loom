import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { inspectDOM, waitForDOM, clickControl, navigatePage } from './lib/playwright-verification.mjs';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { startVerificationIdentity } from './lib/cda-verification-identity.mjs';

// Run against the construction checkout's local stack and loaded CDA fixture.
const origin = process.env.LOOM_CDA_UI_ORIGIN;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const basicMode = process.argv[2] === 'basic';
const project = process.env.LOOM_CDA_PROJECT;
const observationId = basicMode ? 'dev-observation-001' : '485e2567-b566-56f3-b5bd-5f025f37cd95';
const differentialMode = process.argv[2] === 'differential';
let generation = basicMode ? undefined : 'cda-fhir-v1';
const codedPairs = basicMode ? [
  { system: 'https://example.test/codes', code: 'height', label: 'Height' },
] : [
  { system: 'https://cda.readthedocs.io', code: 'specimen_type', label: 'Specimen type' },
  { system: 'https://cda.readthedocs.io', code: 'primary_disease_type', label: 'Primary disease type' },
];
const explorer = `compound-coded-qa-${Date.now()}`;
const evidenceDirectory = process.env.LOOM_VERIFY_OUTPUT ?? (basicMode ? '/tmp/loom-basic-coded-pivot-grouping' : '/tmp/loom-compound-coded-verification');
const base = `/api/v1/projects/${project}/explorers/${explorer}`;
const state = { explorer, observationId, project, mode: basicMode ? 'devloop-basic-coded-grouping' : differentialMode ? 'differential' : 'single-observation', timingsMs: {}, failures: [], requests: [], nativeRequests: [], errors: [] };
state.requests = state.nativeRequests;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin: origin, apiContainer: process.env.LOOM_CDA_API_CONTAINER,
  composeProject: process.env.LOOM_CDA_COMPOSE_PROJECT, sourceRoot, arangoContainer: process.env.LOOM_ARANGO_CONTAINER });
const verificationIdentity = await startVerificationIdentity(sourceRoot, process.env.LOOM_CDA_API_CONTAINER);
state.sourceFingerprint = verificationIdentity.sourceFingerprint;
state.apiBuildIdentity = verificationIdentity.apiBuildIdentity;
state.sourceFreeze = { initialFingerprint: verificationIdentity.sourceFingerprint };
let observationIDs = [observationId];
let rawSources = [];
let statusColumn;
let statusColumnID;
let statusInputColumnID;
let requestCapture;

const api = async (path, method = 'GET', body) => {
  const response = await fetch(apiOrigin + path, {
    method, headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert(response.ok, `${method} ${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};

const rowsReady = (page) => waitForDOM(page,
  () => !document.body.innerText.includes('Loading your table') && document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false,
  {}, 30000);
const readTable = (page) => inspectDOM(page, () => ({
  headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText.trim()),
  rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
}));

const finishTiming = (name, started) => {
  state.timingsMs[name] = Date.now() - started;
  assert(state.timingsMs[name] <= 5000, `${name} took ${state.timingsMs[name]} ms`);
};

const proposed = async (page, started, timing, expectedBuilder, requestStart) => {
  const deadline = started + 5000;
  const request = await requestCapture.waitFor(entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST', { fromIndex: requestStart, timeoutMs: Math.max(1, deadline - Date.now()) });
  assert(request.response, `${timing} proposal response could not be captured: ${request.responseBodyError ?? 'unknown response body error'}`);
  assert.equal(request.status, 200, `${timing} proposal request failed: ${JSON.stringify(request.response)}`);
  assert(request.response.proposalId, `${timing} proposal response omitted its receipt`);
  assert.equal(request.body.snapshotToken, expectedBuilder.catalog.snapshotToken, `${timing} must use the current catalog snapshot`);
  assert.equal(request.body.expectedDraftVersion, expectedBuilder.draftVersion, `${timing} must use the current saved draft version`);
  assert.equal(request.body.expectedDraftDigest, expectedBuilder.draftDigest, `${timing} must use the current saved draft digest`);
  assert.equal(request.response.outputId, savedDocument(expectedBuilder).output.id);
  assert.equal(request.response.snapshotToken, expectedBuilder.catalog.snapshotToken);
  assert.equal(request.response.draftVersion, expectedBuilder.draftVersion);
  assert.equal(request.response.draftDigest, expectedBuilder.draftDigest);
  const remaining = deadline - Date.now();
  assert(remaining > 0, `${timing} exceeded five seconds before its receipt rendered`);
  await waitForDOM(page, ({ proposalId }) => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.dataset.proposalId === proposalId && ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, { proposalId: request.response.proposalId }, remaining);
  const result = await inspectDOM(page, () => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return { status: panel?.getAttribute('data-proposal-status'), proposalId: panel?.dataset.proposalId, text: panel?.innerText,
      headers: [...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].map(cell => cell.querySelector('span')?.textContent?.trim() ?? cell.innerText.trim()),
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())) };
  });
  assert.equal(result.proposalId, request.response.proposalId, `${timing} DOM must show the fresh response receipt`);
  finishTiming(timing, started);
  assert.equal(result.status, 'ready', result.text);
  return { ...result, request };
};

const savedDocument = (builder) => {
  assert.equal(builder.workspace.documents.length, 1);
  return builder.workspace.documents[0];
};

const assertNamedPreviewValues = (preview, expected, label) => {
  assert.equal(preview.rows.length, 1, `${label} must produce exactly one grouped row`);
  for (const [columnLabel, expectedValue] of expected) {
    const indices = preview.headers.flatMap((header, index) => header.toLowerCase() === columnLabel.toLowerCase() ? [index] : []);
    assert.equal(indices.length, 1, `${label} must expose one ${columnLabel} column; headers=${JSON.stringify(preview.headers)}`);
    assert.equal(preview.rows[0][indices[0]], expectedValue, `${label} value for ${columnLabel}`);
  }
};

const assertSourceIdentityRows = (rendered, sources, builderDocument, label) => {
  const idColumn = builderDocument.columns.find((column) => column.source?.kind === 'field' && column.source.field?.path === 'id');
  const status = builderDocument.columns.find((column) => column.source?.kind === 'field' && column.source.field?.path === 'status');
  assert(idColumn, `${label} must retain the direct Observation.id binding`);
  assert(status, `${label} must retain the direct Observation.status binding`);
  assert.equal(status.column, statusColumn.column);
  if (status.columnId) assert.equal(status.columnId, statusInputColumnID);
  assert.equal(rendered.rows.length, sources.length, `${label} must render exactly the selected raw resources`);
  const idIndex = rendered.headers.findIndex((header) => header.toLowerCase() === idColumn.label.toLowerCase());
  const statusIndex = rendered.headers.findIndex((header) => header.toLowerCase() === status.label.toLowerCase());
  assert(idIndex >= 0, `${label} is missing the ${idColumn.label} column`);
  assert(statusIndex >= 0, `${label} is missing the ${status.label} column`);
  const renderedIDs = rendered.rows.map((row) => row[idIndex]);
  assert.equal(new Set(renderedIDs).size, sources.length, `${label} must render unique Observation IDs`);
  assert.deepEqual([...renderedIDs].sort(), sources.map((source) => source.id).sort(), `${label} ID population must match the raw source oracle`);
  for (const source of sources) {
    const row = rendered.rows.find((candidate) => candidate[idIndex] === source.id);
    assert(row, `${label} omitted ${source.id}`);
    assert.equal(row[statusIndex], source.status, `${label} must preserve the exact status paired with ${source.id}`);
  }
};

const applyAuthoringCommands = async (commands) => {
  const builder = await api(base + '/authoring/v2/builder');
  const response = await api(base + '/authoring/v2/commands', 'POST', {
    commandId: `${explorer}-${Date.now()}`,
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  return { response, builder: await api(base + '/authoring/v2/builder') };
};

await mkdir(evidenceDirectory, { recursive: true });
const browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [apiOrigin, origin], noAuth: true });
const page = browser.page;
const tracker = { activeAction: undefined, actions: [] };
requestCapture = captureCDARequests(page, { apiOrigin: origin, appOrigins: [apiOrigin, origin], ownedPathPrefix: `/api/v1/projects/${project}/explorers`, report: state, responsePaths: /construction-proposals|commands|builder|preview/ });

try {
  let initialBuilder;
  if (basicMode) {
    await api(`/api/v1/projects/${project}/explorers`, 'POST', { name: explorer, title: 'Basic coded grouping verification' });
    initialBuilder = await api(base + '/authoring/v2/builder');
    generation = initialBuilder.catalog.generation;
    state.generation = generation;
  }
  const readRawObservations = (query) => {
    const raw = execFileSync('rtk', [
      'proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER,
      'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
      `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
    ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
    const start = raw.indexOf('[');
    assert(start >= 0, `Arango returned no JSON array: ${raw.slice(-1000)}`);
    return JSON.parse(raw.slice(start));
  };
  const pairValues = (source) => codedPairs.map((pair) => {
    if (basicMode) {
      const matches = (source.code?.coding ?? [])
        .filter((coding) => coding.system === pair.system && coding.code === pair.code)
        .map(() => source.valueQuantity?.value);
      return matches.length === 1 && typeof matches[0] === 'number' && Number.isFinite(matches[0]) ? String(matches[0]) : undefined;
    }
    const matches = (source.component ?? []).flatMap((component) =>
      (component?.code?.coding ?? [])
        .filter((coding) => coding.system === pair.system && coding.code === pair.code)
        .map(() => component.valueString));
    return matches.length === 1 && typeof matches[0] === 'string' && matches[0].trim() ? matches[0] : undefined;
  });

  if (differentialMode) {
    const query = `FOR d IN Observation FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} SORT d.id LIMIT 1000 RETURN {id:d.id,project:d.project,generation:d.dataset_generation,resourceType:d.payload.resourceType,status:d.payload.status,component:d.payload.component}`;
    const scanned = readRawObservations(query);
    assert(scanned.length <= 1000, 'The independent coded-group oracle must remain bounded to 1000 Observation resources');
    assert.equal(new Set(scanned.map((source) => source.id).filter((id) => typeof id === 'string')).size,
      scanned.filter((source) => typeof source.id === 'string').length,
      'The scoped raw oracle must not group duplicate Observation identities');
    const buckets = new Map();
    for (const source of scanned) {
      if (source.project !== project || source.generation !== generation || source.resourceType !== 'Observation' ||
          typeof source.id !== 'string' || typeof source.status !== 'string' || !source.status.trim()) continue;
      const values = pairValues(source);
      if (values.some((value) => value === undefined) || new Set(values).size !== values.length || values.includes(source.status)) continue;
      const key = JSON.stringify([source.status, ...codedPairs.map((pair, index) => [pair.system, pair.code, values[index]])]);
      const bucket = buckets.get(key) ?? { status: source.status, values, sources: [] };
      bucket.sources.push(source);
      buckets.set(key, bucket);
    }
    const bucket = [...buckets.values()].find((candidate) => candidate.sources.length >= 2);
    assert(bucket, 'The first 1000 scoped Observations contain no two-resource group sharing Observation.status and both exact Coding.system/code pairs');
    rawSources = bucket.sources.slice(0, 2);
    observationIDs = rawSources.map((source) => source.id);
    assert.equal(new Set(observationIDs).size, 2, 'Differential witnesses must be distinct Observation IDs');
    state.oracle = {
      source: 'bounded raw Arango Observation payload query, independently grouped by status and JSON([Coding.system, Coding.code]) identity',
      queryLimit: 1000,
      scanned: scanned.length,
      generation,
      codingPairs: codedPairs.map(({ system, code }) => ({ system, code })),
      status: bucket.status,
      values: bucket.values,
      selectedIDs: observationIDs,
      expectedGroupCount: rawSources.length,
      expectedRows: [[...bucket.values, bucket.status, String(rawSources.length)]],
      groupingPairs: codedPairs.map((pair, index) => ({ system: pair.system, code: pair.code, value: bucket.values[index] })),
    };
  } else if (basicMode) {
    const query = `FOR d IN Observation FILTER d.id == ${JSON.stringify(observationId)} AND d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} LIMIT 2 RETURN {id:d.id,project:d.project,generation:d.dataset_generation,resourceType:d.payload.resourceType,status:d.payload.status,code:d.payload.code,valueQuantity:d.payload.valueQuantity}`;
    rawSources = readRawObservations(query);
    const [source] = rawSources;
    assert.equal(rawSources.length, 1, 'The bounded project/generation/id oracle must resolve exactly one resource');
    assert.equal(source?.project, project, 'The registered basic fixture source record is required');
    assert.equal(source?.generation, generation);
    assert.equal(source?.resourceType, 'Observation');
    assert.equal(source?.id, observationId);
    assert.equal(source?.status, 'final');
    const values = pairValues(source);
    assert.deepEqual(values, ['172.5'], 'Pair the exact height Coding with valueQuantity.value 172.5');
    state.oracle = { generation: source.generation, status: source.status, values, specimenType: values[0], count: 1,
      codingPairs: codedPairs.map(({ system, code }) => ({ system, code })) };
  } else {
    const query = `FOR d IN Observation FILTER d.id == ${JSON.stringify(observationId)} AND d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} LIMIT 2 RETURN {id:d.id,project:d.project,generation:d.dataset_generation,resourceType:d.payload.resourceType,status:d.payload.status,component:d.payload.component}`;
    rawSources = readRawObservations(query);
    const [source] = rawSources;
    assert.equal(source?.project, project, 'The real CDA source record is required');
    assert.equal(source?.generation, generation);
    assert.equal(source?.resourceType, 'Observation');
    assert.equal(source?.id, observationId);
    const values = pairValues(source);
    assert.equal(values.length, 2);
    assert.equal(values[0], 'analyte');
    assert.equal(typeof values[1], 'string');
    state.oracle = { generation: source.generation, specimenType: values[0], primaryDiseaseType: values[1], count: 1,
      codingPairs: codedPairs.map(({ system, code }) => ({ system, code })) };
  }
  state.observationIDs = observationIDs;
  state.oracleSources = rawSources.map((source) => ({ id: source.id, status: source.status, component: source.component }));
  if (!basicMode) await api(`/api/v1/projects/${project}/explorers`, 'POST', { name: explorer, title: 'Compound coded grouping verification' });
  const initial = initialBuilder ?? await api(base + '/authoring/v2/builder');
  assert.equal(initial.catalog.generation, state.oracle.generation);
  const selection = await api(base + '/selections', 'POST', {
    snapshotToken: initial.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: observationIDs.map((id) => ({ project, generation: initial.catalog.generation, resourceType: 'Observation', id })) } },
  });
  const url = `${origin}/?project=${project}&explorer=${explorer}&mode=builder&selection=${encodeURIComponent(selection.id)}`;
  state.url = url;
  await navigatePage(page, url);
  await waitForDOM(page, () => Boolean(document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)')), {}, 30000);
  await clickControl(tracker, page, 'button', { name: 'Choose Observation rows' });
  await rowsReady(page);
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.innerText.includes('Observation'), {}, 30000);
  await clickControl(tracker, page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForDOM(page, () => [...document.querySelectorAll('[aria-label="Starting collection"] button')].some(button => button.innerText === 'Use selected resources' && !button.disabled), {}, 30000);
  await clickControl(tracker, page, '[aria-label="Starting collection"] button', { name: 'Use selected resources' });
  await waitForDOM(page, ({ expected }) => document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes(expected), { expected: `${observationIDs.length} Observation resources attached` }, 30000);
  if (differentialMode) {
    const current = await api(base + '/authoring/v2/builder');
    const document = savedDocument(current);
    assert.equal(document.rootResourceType, 'Observation');
    const observationNode = current.catalog.nodes.find((node) => node.resourceType === 'Observation');
    const idCandidate = current.catalog.candidates.find((candidate) => candidate.nodeId === observationNode?.nodeId && candidate.fieldPath === 'id');
    const statusCandidate = current.catalog.candidates.find((candidate) => candidate.nodeId === observationNode?.nodeId && candidate.fieldPath === 'status');
    assert(idCandidate && statusCandidate, 'The ordinary-grouping differential requires direct Observation id and status candidates');
    assert.equal(document.columns.find((column) => column.source?.field?.path === 'id')?.logicalType, 'string');
    const added = await applyAuthoringCommands([{
      type: 'ADD_COLUMN', outputId: document.output.id, occurrenceId: 'base',
      candidateId: statusCandidate.candidateId, projectionMode: 'VALUE',
      initialPresentation: 'TABLE', title: 'Observation status',
    }]);
    state.statusColumnCommand = added.response;
    state.baseline = added.builder;
    statusColumn = savedDocument(state.baseline).columns.find((column) => column.source?.field?.path === 'status');
    assert(statusColumn, 'The direct Observation.status source binding was not retained');
    assert.equal(statusColumn.source.kind, 'field');
    assert.equal(statusColumn.source.field.path, 'status');
    assert.equal(statusColumn.source.field.projectionMode, 'VALUE');
    assert.equal(statusColumn.logicalType, 'string');
    statusColumnID = statusColumn.columnId ?? statusColumn.column;
    const sourceCapabilities = await api(base + '/authoring/v2/construction-capabilities', 'POST', {
      snapshotToken: state.baseline.catalog.snapshotToken,
      expectedDraftVersion: state.baseline.draftVersion,
      expectedDraftDigest: state.baseline.draftDigest,
      outputId: savedDocument(state.baseline).output.id,
      stageId: 'source_projection',
    });
    const sourceStatus = sourceCapabilities.selectedStage.columns.find((column) => column.name === statusColumn.column);
    assert(sourceStatus?.id, 'The source-stage descriptor must identify the configured status field');
    statusInputColumnID = sourceStatus.id;
    state.statusSourceDescriptor = sourceStatus;
    const savedPopulation = savedDocument(state.baseline).population;
    assert.equal(savedPopulation.selectionRevisionId, selection.id);
    assert.equal(savedPopulation.route?.length ?? 0, 0);
    assert.deepEqual(savedDocument(state.baseline).columns.filter((column) => ['id', 'status'].includes(column.source?.field?.path)).map((column) => column.source.field.path), ['id', 'status']);
    const statusReloadAt = Date.now();
    await navigatePage(page, url);
    await rowsReady(page);
    await waitForDOM(page, ({ ids }) => Boolean(document.querySelector('[data-testid="preview-table-scroll"]')) && ids.every(id => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(id)), { ids: observationIDs }, 30000);
    finishTiming('reloadStatusBinding', statusReloadAt);
    state.statusSourceReload = await readTable(page);
    assert(state.statusSourceReload.headers.some((header) => /status/i.test(header)), 'The ordinary Observation.status grouping field must be visible in the native table');
    assertSourceIdentityRows(state.statusSourceReload, rawSources, savedDocument(state.baseline), 'Reloaded ordinary Observation source table');
  } else {
    state.baseline = await api(base + '/authoring/v2/builder');
  }
  const rowDefinitionDialogOpen = await inspectDOM(page, () => Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"][aria-modal="true"]')));
  if (!rowDefinitionDialogOpen) await clickControl(tracker, page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForDOM(page, () => document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled === false, {}, 5000);
  const requestStart = state.requests.length;
  const openedAt = Date.now();
  await clickControl(tracker, page, '[data-testid="construction-action-group-rows"]');
  await waitForDOM(page, ({ label }) => Boolean(document.querySelector(`input[aria-label="Group by coded value: ${CSS.escape(label)}"]:not(:disabled)`)), { label: codedPairs[0].label }, 5000);
  state.timingsMs.openPicker = Date.now() - openedAt;
  assert(state.timingsMs.openPicker <= 5000, `Opening the coded picker took ${state.timingsMs.openPicker} ms`);
  state.groupChoices = await inspectDOM(page, () => [...document.querySelectorAll('input[aria-label^="Group by"]')].map(input => ({ label: input.getAttribute('aria-label'), disabled: input.disabled, checked: input.checked })));
  assert(!await inspectDOM(page, () => document.body.innerText.includes('Need a coded-value column first?')));
  const proposalRequestStart = state.requests.length;
  const selectedAt = Date.now();
  await clickControl(tracker, page, `input[aria-label="Group by coded value: ${codedPairs[0].label}"]`);
  state.proposal = await proposed(page, selectedAt, 'selectToPreview', state.baseline, proposalRequestStart);
  if (differentialMode) {
    assertNamedPreviewValues(state.proposal, [
      [codedPairs[0].label, state.oracle.values[0]],
      ['Row count', String(state.oracle.expectedGroupCount)],
    ], 'First coded group preview');
  } else {
    assert.deepEqual(state.proposal.rows, [[state.oracle.specimenType, '1']], 'The grouped preview must match the independent source value and record count');
  }
  const beforeApply = await api(base + '/authoring/v2/builder');
  assert.equal(beforeApply.draftDigest, state.baseline.draftDigest, 'Selecting a code must not save its prerequisite');
  assert.deepEqual(beforeApply.workspace, state.baseline.workspace);
  const proposalRequests = state.requests.slice(requestStart);
  assert(!proposalRequests.some(request => request.path.includes('construction-choice-proposals') || request.path.endsWith('/commands')), 'The coded selection must not issue a separate saved-column command');
  const firstApplyAt = Date.now();
  await clickControl(tracker, page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 30000);
  finishTiming('applyFirstCodedGroup', firstApplyAt);
  state.saved = await api(base + '/authoring/v2/builder');
  assert.deepEqual(state.requests.slice(requestStart).filter(request => request.path.endsWith('/commands')).flatMap(request => request.body.commands.map(command => command.type)), ['APPLY_CONSTRUCTION_PROPOSAL'], 'One Apply must save both parts in one command');
  const construction = savedDocument(state.saved).construction;
  assert.equal(construction.steps.length, 2, 'The extraction and grouping must be saved together');
  const [helper, group] = construction.steps;
  assert.equal(helper.operation.kind, 'CODED_PIVOT');
  assert.equal(group.operation.kind, 'GROUP');
  assert.equal(helper.ownerStepId, group.id);
  assert.equal(helper.operation.codedPivot.categories.length, 1);
  if (differentialMode) {
    assert.deepEqual(helper.operation.codedPivot.categories.map(({ system, code }) => ({ system, code })), [{ system: codedPairs[0].system, code: codedPairs[0].code }], 'The first coded Group input must preserve its exact system/code identity');
  }
  assert.equal(savedDocument(state.saved).columns.length, savedDocument(state.baseline).columns.length, 'The prerequisite must not become a standalone source column');

  await navigatePage(page, url);
  await waitForDOM(page, ({ id }) => Boolean(document.querySelector(`[data-testid="construction-history-step-${CSS.escape(id)}"]`)), { id: group.id }, 30000);
  await rowsReady(page);
  await clickControl(tracker, page, `[data-testid="construction-history-step-${group.id}"]`);
  await clickControl(tracker, page, `[data-testid="construction-edit-step-${group.id}"]`);
  await waitForDOM(page, ({ label }) => document.querySelector(`input[aria-label="Group by coded value: ${CSS.escape(label)}"]`)?.checked === true, { label: codedPairs[0].label }, 5000);
  if (!basicMode) {
    await waitForDOM(page, () => Boolean(document.querySelector('input[aria-label="Group by coded value: Primary disease type"]:not(:disabled)')), {}, 5000);
    const editProposalRequestStart = state.requests.length;
    const editedAt = Date.now();
    await clickControl(tracker, page, 'input[aria-label="Group by coded value: Primary disease type"]');
    state.editedProposal = await proposed(page, editedAt, 'editToPreview', state.saved, editProposalRequestStart);
    assert.equal(state.editedProposal.rows.length, 1);
    if (differentialMode) {
      assertNamedPreviewValues(state.editedProposal, [
        ...codedPairs.map((pair, index) => [pair.label, state.oracle.values[index]]),
        ['Row count', String(state.oracle.expectedGroupCount)],
      ], 'Two-coded-field preview');
    } else {
      assert(state.editedProposal.rows[0].includes(state.oracle.specimenType));
      assert(state.editedProposal.rows[0].includes(state.oracle.primaryDiseaseType));
      assert(state.editedProposal.rows[0].includes('1'));
    }
  }
  let ordinaryKeySelector = 'input[aria-label="Group by Observation ID"]';
  if (differentialMode) {
    const ordinaryKeyLabel = await inspectDOM(page, () => {
      const inputs = [...document.querySelectorAll('input[type="checkbox"][aria-label^="Group by"]')];
      return inputs.find(item => /status/i.test(item.getAttribute('aria-label') ?? ''))?.getAttribute('aria-label') ?? '';
    });
    assert(ordinaryKeyLabel, 'The Group editor did not offer the direct Observation.status binding as an ordinary grouping key');
    ordinaryKeySelector = `input[type="checkbox"][aria-label=${JSON.stringify(ordinaryKeyLabel)}]`;
  }
  await waitForDOM(page, ({ selector }) => Boolean(document.querySelector(selector)), { selector: ordinaryKeySelector }, 5000);
  const ordinaryKeyAt = Date.now();
  const ordinaryKeyState = await inspectDOM(page, ({ selector }) => { const input = document.querySelector(selector); return { disabled: input?.disabled, checked: input?.checked }; }, { selector: ordinaryKeySelector });
  assert.equal(ordinaryKeyState.disabled, false);
  assert.equal(ordinaryKeyState.checked, false, 'The direct ordinary source key must start unselected before this edit');
  const ordinaryProposalRequestStart = state.requests.length;
  await clickControl(tracker, page, ordinaryKeySelector);
  state.mixedKeyProposal = await proposed(page, ordinaryKeyAt, 'ordinaryKeyToPreview', state.saved, ordinaryProposalRequestStart);
  assert.equal(state.mixedKeyProposal.rows.length, 1);
  if (differentialMode) {
    assertNamedPreviewValues(state.mixedKeyProposal, [
      ...codedPairs.map((pair, index) => [pair.label, state.oracle.values[index]]),
      ['Observation status', state.oracle.status],
      ['Row count', String(state.oracle.expectedGroupCount)],
    ], 'Coded fields plus ordinary Observation.status group preview');
  } else {
    if (basicMode) {
      assertNamedPreviewValues(state.mixedKeyProposal, [
        [codedPairs[0].label, state.oracle.values[0]],
        ['Observation ID', observationId],
        ['Row count', '1'],
      ], 'Coded value plus exact Observation ID grouping field');
    } else {
      assert(state.mixedKeyProposal.rows[0].includes(observationId), 'An existing source field must survive the coded prerequisite');
      assert(state.mixedKeyProposal.rows[0].includes(state.oracle.specimenType));
      assert(state.mixedKeyProposal.rows[0].includes(state.oracle.primaryDiseaseType));
    }
  }
  const editedApplyAt = Date.now();
  await clickControl(tracker, page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]'), {}, 30000);
  finishTiming('applyEditedCodedGroup', editedApplyAt);
  state.edited = await api(base + '/authoring/v2/builder');
  const editedDocument = savedDocument(state.edited);
  const editedSteps = editedDocument.construction.steps;
  assert.equal(editedSteps.length, 2);
  assert.deepEqual(editedDocument.population, savedDocument(state.baseline).population, 'Coded grouping must preserve the exact selected Observation population');
  assert.equal(editedSteps[0].id, helper.id, 'Editing must reuse the owned prerequisite');
  assert.equal(editedSteps[0].operation.codedPivot.categories.length, codedPairs.length);
  assert.equal(editedSteps[0].rowValues.length, 1);
  assert.equal(editedSteps[0].rowValues[0].policy, 'ONE');
  if (basicMode) {
    const groupStep = editedSteps[1];
    const idPassthrough = editedSteps[0].rowValues[0];
    assert.equal(groupStep.operation.kind, 'GROUP');
    assert(groupStep.operation.group.keys.some((key) => key.inputColumnId === idPassthrough.outputColumnId),
      'The ordinary Observation ID grouping key must bind through the CODED_PIVOT-owned row-value output');
  }
  if (differentialMode) {
    const durablePairs = editedSteps[0].operation.codedPivot.categories
      .map(({ system, code }) => JSON.stringify([system, code])).sort();
    const expectedPairs = codedPairs.map(({ system, code }) => JSON.stringify([system, code])).sort();
    assert.deepEqual(durablePairs, expectedPairs, 'The grouped coded source must persist exact system/code pairs, not code-only keys');
    const helper = editedSteps[0];
    const group = editedSteps[1];
    assert.equal(helper.operation.codedPivot.source.route?.length ?? 0, 0, 'Coded categories must remain bound to the direct Observation source');
    const statusBinding = helper.rowValues.find((value) => value.inputColumnId === statusInputColumnID);
    assert(statusBinding, 'CODED_PIVOT must carry the authored Observation.status column into its GROUP owner');
    assert.equal(statusBinding.policy, 'ONE');
    assert(group.operation.group.keys.some((key) => key.inputColumnId === statusBinding.outputColumnId), 'GROUP must use the status passthrough output as an ordinary key');
  }

  const groupedReloadAt = Date.now();
  await navigatePage(page, url);
  await waitForDOM(page, ({ id }) => Boolean(document.querySelector(`[data-testid="construction-history-step-${CSS.escape(id)}"]`)), { id: group.id }, 30000);
  await rowsReady(page);
  if (basicMode) {
    state.reloadedBuilder = await api(base + '/authoring/v2/builder');
    assert.equal(state.reloadedBuilder.draftDigest, state.edited.draftDigest, 'The edited coded grouping must survive a fresh Builder reload');
    assert.deepEqual(state.reloadedBuilder.workspace, state.edited.workspace);
  }
  finishTiming('reloadEditedCodedGroup', groupedReloadAt);
  state.groupedReload = await readTable(page);
  assert.equal(state.groupedReload.rows.length, 1);
  if (differentialMode) assertNamedPreviewValues(state.groupedReload, [
    ...codedPairs.map((pair, index) => [pair.label, state.oracle.values[index]]),
    ['Observation status', state.oracle.status],
    ['Row count', String(state.oracle.expectedGroupCount)],
  ], 'Reloaded coded group');
  else if (basicMode) assertNamedPreviewValues(state.groupedReload, [
    [codedPairs[0].label, state.oracle.values[0]], ['Observation ID', observationId], ['Row count', '1'],
  ], 'Reloaded coded group with source identity key');

  await clickControl(tracker, page, `[data-testid="construction-history-step-${group.id}"]`);
  const removedAt = Date.now();
  const removalProposalRequestStart = state.requests.length;
  await clickControl(tracker, page, `[data-testid="construction-remove-step-${group.id}"]`);
  state.removalProposal = await proposed(page, removedAt, 'removeToPreview', state.edited, removalProposalRequestStart);
  if (differentialMode) {
    assertSourceIdentityRows(state.removalProposal, rawSources, savedDocument(state.baseline), 'Coded group removal proposal');
  } else {
    assert.deepEqual(state.removalProposal.rows, [[observationId]], 'Removing the compound group must restore its original source table');
  }
  await clickControl(tracker, page, '[data-testid="construction-cancel-proposal"]');
  await waitForDOM(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]'), {}, 30000);
  state.afterRemovalCancel = await api(base + '/authoring/v2/builder');
  assert.equal(state.afterRemovalCancel.draftDigest, state.edited.draftDigest, 'Cancel must preserve the grouped draft');
  assert.deepEqual(state.afterRemovalCancel.workspace, state.edited.workspace);
  await clickControl(tracker, page, `[data-testid="construction-history-step-${group.id}"]`);
  const confirmedRemovalAt = Date.now();
  const confirmedRemovalRequestStart = state.requests.length;
  await clickControl(tracker, page, `[data-testid="construction-remove-step-${group.id}"]`);
  state.confirmedRemovalProposal = await proposed(page, confirmedRemovalAt, 'confirmedRemovalToPreview', state.edited, confirmedRemovalRequestStart);
  if (differentialMode) {
    assertSourceIdentityRows(state.confirmedRemovalProposal, rawSources, savedDocument(state.baseline), 'Confirmed coded group removal proposal');
  } else {
    assert.deepEqual(state.confirmedRemovalProposal.rows, [[observationId]]);
  }
  const finalApplyAt = Date.now();
  await clickControl(tracker, page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, {}, 30000);
  finishTiming('applyGroupRemoval', finalApplyAt);
  state.restored = await api(base + '/authoring/v2/builder');
  assert.equal(savedDocument(state.restored).construction?.steps?.length ?? 0, 0, 'Removing GROUP must remove its owned extraction');
  const sourceColumnSemantics = (columns) => columns.map(({ columnId: generatedStageId, ...column }) => column);
  assert.deepEqual(sourceColumnSemantics(savedDocument(state.restored).columns), sourceColumnSemantics(savedDocument(state.baseline).columns));
  const restoredReloadAt = Date.now();
  await navigatePage(page, url);
  await rowsReady(page);
  await waitForDOM(page, ({ ids }) => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0 && ids.every(id => document.body.innerText.includes(id)), { ids: observationIDs }, 30000);
  finishTiming('reloadRestoredSource', restoredReloadAt);
  state.restoredAfterReload = await api(base + '/authoring/v2/builder');
  assert.deepEqual(state.restoredAfterReload.workspace, state.restored.workspace, 'The restored table must persist after fresh reload');
  if (differentialMode) {
    const restoredRows = await readTable(page);
    assert(restoredRows.headers.some((header) => /status/i.test(header)));
    assertSourceIdentityRows(restoredRows, rawSources, savedDocument(state.restoredAfterReload), 'Restored source table after reload');
  }
  await requestCapture.flush();
  state.failures.push(...browser.diagnostics.console.map(entry => ({ kind: 'console', ...entry })));
  state.failures.push(...browser.diagnostics.pageErrors.map(entry => ({ kind: 'page-error', ...entry })));
  state.failures.push(...browser.diagnostics.networkFailures.map(entry => ({ kind: 'network', ...entry })));
  state.failures.push(...browser.diagnostics.httpFailures.map(entry => ({ kind: 'http', ...entry })));
  state.failures.push(...state.errors.map(entry => ({ kind: 'api', ...entry })));
  assert.deepEqual(state.failures, [], 'The browser lifecycle must not hide HTTP or runtime failures');
} catch (error) {
  state.failures.push({ kind: 'assertion', text: String(error.stack ?? error) });
  await browser.captureFailure(error, { phase: 'compound-coded-group-lifecycle', action: tracker.activeAction,
    elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined,
    state: { explorer, observationIDs, timingsMs: state.timingsMs, requests: state.requests } });
  if (error.invalidatesRun) state.status = 'invalidated';
  process.exitCode = 1;
} finally {
  try { state.verificationIdentity = await verificationIdentity.finish(); }
  catch (error) {
    state.status = 'invalidated';
    state.verificationIdentity = { unchanged: false, invalidatesRun: true, productFailure: false, error: String(error) };
    state.failures.push({ kind: 'source-build-identity', invalidatesRun: true, text: String(error) });
    process.exitCode = 1;
  }
  await requestCapture.flush();
  state.body = await page.locator('body').innerText().catch(String);
  state.diagnostics = browser.diagnostics;
  await writeFile(join(evidenceDirectory, `${explorer}.json`), JSON.stringify(state, null, 2));
  await browser.close();
}
if (state.failures.length) process.exitCode = 1;
console.log(JSON.stringify({ explorer, evidenceDirectory, failures: state.failures, timingsMs: state.timingsMs }, null, 2));
