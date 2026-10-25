import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync, mkdirSync, copyFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { BASIC_TEMPORAL_FILTER_CASES } from '../basic-temporal-filter-oracle.mjs';
import { compileTemporalFilterJourneyTrace, validateTemporalFilterJourneyTrace } from '../temporal-filter-tla-adapter.mjs';

const helperDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(helperDirectory, '../../../../');
const modelPath = resolve(repositoryRoot, 'verification/builder-interactions/TemporalFilterJourney.tla');
const tlcJar = process.env.TLA2TOOLS_JAR;

const clone = value => structuredClone(value);
const contractFor = caseName => BASIC_TEMPORAL_FILTER_CASES[caseName];
const canonicalJSON = value => Array.isArray(value)
  ? `[${value.map(canonicalJSON).join(',')}]`
  : value && typeof value === 'object'
    ? `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`
    : JSON.stringify(value);

const makeJourney = caseName => {
  const contract = contractFor(caseName);
  const inputValue = caseName === 'date' ? contract.initial.inputValue : '2026-01-01T16:00';
  const valueKind = contract.filterValueKind;
  const outputId = 'output-temporal-1';
  const columnId = 'column-temporal-1';
  const filterStepId = 'step-filter-1';
  const otherSteps = [
    { id: 'step-source', operation: { kind: 'SOURCE' } },
    { id: 'step-retained', operation: { kind: 'OTHER', value: 'keep' } },
  ];
  const filterStep = (value, operator) => ({
    id: filterStepId,
    operation: { kind: 'FILTER', filter: { columnId, operator, values: [value] } },
  });
  const initialRequestValue = contract.initial.requestValue ?? contract.initial.savedValue;
  const initialResponseValue = contract.initial.savedValue;
  const editedRequestValue = contract.edited.requestValue ?? contract.edited.savedValue;
  const editedResponseValue = contract.edited.savedValue;
  const sourceColumns = [{ columnId: 'column-root-id' }, { columnId }];
  const documentsFor = name => [{ output: { id: outputId, name }, rootResourceType: contract.resourceType }];
  const digestFor = documents => createHash('sha256').update(canonicalJSON(documents)).digest('hex');
  const makeSnapshot = (name, mountEpoch, draftVersion, draftDigest, steps, columns, savedColumnId = columnId) => ({
    readId: `read-${name}-${mountEpoch}-${draftVersion}`,
    requestId: `builder-${name}-${mountEpoch}-${draftVersion}`,
    status: 200,
    mountEpoch,
    catalogGeneration: 'generation-temporal-fixture',
    draftVersion,
    draftDigest,
    workspaceDocuments: documentsFor(name),
    workspaceDigest: digestFor(documentsFor(name)),
    outputId: name === 'blank' ? null : outputId,
    columnId: name === 'blank' || name === 'table' ? null : savedColumnId,
    columns,
    steps,
  });
  const blank = makeSnapshot('blank', 0, 0, 'draft-blank', [], []);
  const table = makeSnapshot('table', 0, 1, 'draft-table', otherSteps, [{ columnId: 'column-root-id' }], null);
  const source = makeSnapshot('source', 0, 2, 'draft-source', otherSteps, sourceColumns);
  const created = makeSnapshot('created', 1, 3, 'draft-created', [...otherSteps, filterStep(initialResponseValue, contract.initial.operator)], sourceColumns);
  const edited = makeSnapshot('edited', 2, 4, 'draft-edited', [...otherSteps, filterStep(editedResponseValue, contract.edited.operator)], sourceColumns);
  const removed = makeSnapshot('removed', 3, 5, 'draft-removed', otherSteps, sourceColumns);
  const readFrom = (saved, label, mountEpoch) => ({
    ...clone(saved),
    readId: `read-${label}-${mountEpoch}-${saved.draftVersion}`,
    requestId: `builder-${label}-${mountEpoch}-${saved.draftVersion}`,
    mountEpoch,
  });

  const requests = [];
  const actions = [];
  const rawSteps = [];
  let requestSequence = 0;
  const observedRequest = ({ method, path, mutation, body, response }) => {
    requestSequence += 1;
    const browserRequestId = `browser-request-${requestSequence}`;
    const time = actions.length * 10 + 0.125;
    const raw = {
      browserRequestId,
      requestId: `raw-request-${requestSequence}`,
      isMutation: mutation,
      method,
      path,
      status: path.endsWith('/explorers') ? 201 : 200,
      request: clone(body),
      response: clone(response),
      startedAt: time - 0.05,
      completedAt: time + 0.05,
      atMonotonicMs: time,
      atEpochMs: 1_800_000_000_000 + actions.length * 10,
      mountEpoch: actionMountEpoch,
    };
    requests.push(raw);
    return {
      captureId: browserRequestId,
      requestId: raw.requestId,
      isMutation: mutation,
      method,
      path,
      status: raw.status,
      body: clone(body),
      responseBody: clone(response),
    };
  };
  let actionMountEpoch = 0;

  const add = ({ label, mountEpoch, request = null, proposalCaptureIds = [], before = null, after = null, ui = null, filter = null }) => {
    actionMountEpoch = mountEpoch;
    const index = actions.length;
    const atMonotonicMs = index * 10 + 0.125;
    const atEpochMs = 1_800_000_000_000 + index * 10;
    if (request) {
      const raw = requests.find(candidate => candidate.browserRequestId === request.captureId);
      raw.mountEpoch = mountEpoch;
      raw.atMonotonicMs = atMonotonicMs;
      raw.atEpochMs = atEpochMs;
    }
    const event = {
      action: label,
      atMonotonicMs,
      atEpochMs,
      mountEpoch,
      request,
      before: clone(before),
      after: clone(after),
      ui: clone(ui),
      filter: clone(filter),
    };
    const rawStep = {
      label,
      atMonotonicMs,
      atEpochMs,
      mountEpoch,
      requestCaptureId: request?.captureId ?? null,
      proposalCaptureIds: [...proposalCaptureIds],
      beforeReadIndex: null,
      afterReadIndex: null,
      domObservationIndex: null,
    };
    actions.push(event);
    rawSteps.push(rawStep);
  };

  const createBlankRequest = observedRequest({
    method: 'POST', path: '/api/v1/projects/{project}/explorers', mutation: true,
    body: { intent: 'CREATE_BLANK', title: 'Temporal filter' },
    response: { explorerId: 'explorer-temporal-1' },
  });
  add({ label: 'ExplorerCreated', mountEpoch: 0, request: createBlankRequest, after: blank });

  const tableRequest = observedRequest({
    method: 'POST', path: '/authoring/v2/commands', mutation: true,
    body: { expectedDraftVersion: 0, expectedDraftDigest: blank.draftDigest, commands: [{ type: 'CREATE_TABLE' }] },
    response: { accepted: true },
  });
  add({ label: 'SourceTableCreated', mountEpoch: 0, request: tableRequest,
    before: readFrom(blank, 'before-table', 0), after: table });

  const choiceProposal = observedRequest({
    method: 'POST', path: '/authoring/v2/construction-choice-proposals', mutation: false,
    body: { outputId, targetColumnIds: ['field-temporal-1'] },
    response: { outputId, previewStatus: 'READY', preview: { receiptId: 'receipt-column-1', outputId, rowCount: contract.rows.length } },
  });
  const columnRequest = observedRequest({
    method: 'POST', path: '/authoring/v2/commands', mutation: true,
    body: { expectedDraftVersion: 1, expectedDraftDigest: table.draftDigest,
      commands: [{ type: 'APPLY_CONSTRUCTION_CHOICE', outputId, columnId }] },
    response: { accepted: true },
  });
  add({ label: 'TemporalColumnApplied', mountEpoch: 0, request: columnRequest,
    proposalCaptureIds: [choiceProposal.captureId], before: readFrom(table, 'before-column', 0), after: source });

  const ui = (mountEpoch, proposalId, receiptId, operator, rows, value = inputValue, status = 'ready') => ({
    capturedAtMonotonicMs: actions.length * 10 + 0.125,
    capturedAtEpochMs: 1_800_000_000_000 + actions.length * 10,
    mountEpoch,
    proposalStatus: status,
    proposalId,
    receiptId,
    outputId,
    columnId,
    inputType: contract.inputType,
    inputValue: value,
    operator,
    rowIds: [...rows],
  });
  const candidate = (intent, value, operator) => {
    if (intent === 'remove') return [...otherSteps];
    return [...otherSteps, filterStep(value, operator)];
  };
  const proposal = ({ intent, mountEpoch, before, requestValue, responseValue, operator, rows, proposalId }) => {
    const requestSteps = candidate(intent, requestValue ?? responseValue, operator);
    const responseSteps = candidate(intent, responseValue ?? requestValue, operator);
    const body = {
      outputId,
      expectedDraftVersion: before.draftVersion,
      expectedDraftDigest: before.draftDigest,
      candidateConstruction: { steps: requestSteps },
    };
    const response = {
      proposalId,
      outputId,
      draftVersion: before.draftVersion,
      draftDigest: before.draftDigest,
      candidateConstruction: { steps: responseSteps },
      preview: { receiptId: proposalId, outputId },
    };
    const eventRequest = observedRequest({ method: 'POST', path: '/authoring/v2/construction-proposals',
      mutation: false, body, response });
    const stepId = intent === 'create' ? filterStepId : filterStepId;
    const filter = {
      intent,
      stepId,
      outputId,
      columnId,
      valueKind,
      operator,
      requestValue: requestValue ?? null,
      responseValue: responseValue ?? null,
      savedValue: null,
      proposalId,
      receiptId: proposalId,
    };
    const proposalUI = ui(mountEpoch, proposalId, proposalId, operator, rows);
    if (intent === 'remove') {
      proposalUI.inputType = null;
      proposalUI.inputValue = null;
      proposalUI.operator = null;
    }
    add({ label: `FilterProposed(${intent})`, mountEpoch, request: eventRequest,
      proposalCaptureIds: [eventRequest.captureId], before,
      ui: proposalUI, filter });
    return { proposalId, requestSteps, responseSteps };
  };
  const cancelUI = mountEpoch => ({
    capturedAtMonotonicMs: actions.length * 10 + 0.125,
    capturedAtEpochMs: 1_800_000_000_000 + actions.length * 10,
    mountEpoch,
    proposalStatus: 'none', proposalId: null, receiptId: null,
    outputId, columnId, inputType: null, inputValue: null, operator: null,
    rowIds: contract.rows.map(row => row.id),
  });
  const apply = ({ action, intent, mountEpoch, before, after, proposalId,
    requestValue, responseValue, savedValue, operator, rows }) => {
    const body = {
      expectedDraftVersion: before.draftVersion,
      expectedDraftDigest: before.draftDigest,
      commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId }],
    };
    const eventRequest = observedRequest({ method: 'POST', path: '/authoring/v2/commands', mutation: true,
      body, response: { accepted: true } });
    const filter = {
      intent, stepId: filterStepId, outputId, columnId, valueKind, operator,
      requestValue: requestValue ?? null,
      responseValue: responseValue ?? null,
      savedValue: savedValue ?? null,
      proposalId,
      receiptId: proposalId,
    };
    add({ label: action, mountEpoch, request: eventRequest, before, after,
      ui: { ...cancelUI(mountEpoch), rowIds: [...rows] }, filter });
  };
  const reload = (before, after, mountEpoch, rows) => add({
    label: 'Reload', mountEpoch, before, after,
    ui: { ...cancelUI(mountEpoch), rowIds: [...rows] },
  });

  proposal({ intent: 'create', mountEpoch: 0, before: source,
    requestValue: initialRequestValue, responseValue: initialResponseValue,
    operator: contract.initial.operator, rows: contract.initial.ids, proposalId: 'proposal-cancelled' });
  add({ label: 'FilterCancelled', mountEpoch: 0, before: readFrom(source, 'before-cancel', 0),
    after: readFrom(source, 'after-cancel', 0), ui: cancelUI(0) });
  reload(readFrom(source, 'before-cancel-reload', 0), readFrom(source, 'after-cancel-reload', 1), 1,
    contract.rows.map(row => row.id));

  const createProposal = proposal({ intent: 'create', mountEpoch: 1,
    before: readFrom(source, 'before-create-proposal', 1),
    requestValue: initialRequestValue, responseValue: initialResponseValue,
    operator: contract.initial.operator, rows: contract.initial.ids, proposalId: 'proposal-create' });
  const createBefore = readFrom(source, 'before-create-apply', 1);
  apply({ action: 'FilterApplied(create)', intent: 'create', mountEpoch: 1,
    before: createBefore, after: readFrom(created, 'after-create-apply', 1),
    proposalId: createProposal.proposalId, requestValue: initialRequestValue,
    responseValue: initialResponseValue, savedValue: contract.initial.savedValue,
    operator: contract.initial.operator, rows: contract.initial.ids });
  reload(readFrom(created, 'before-create-reload', 1), readFrom(created, 'after-create-reload', 2), 2,
  contract.initial.ids);

  add({ label: 'FilterEditOpened', mountEpoch: 2,
    before: readFrom(created, 'before-edit-open', 2),
    ui: { ...cancelUI(2), inputType: contract.inputType, inputValue,
      operator: contract.initial.operator, rowIds: [...contract.initial.ids] } });
  const editProposal = proposal({ intent: 'edit', mountEpoch: 2,
    before: readFrom(created, 'before-edit-proposal', 2),
    requestValue: editedRequestValue, responseValue: editedResponseValue,
    operator: contract.edited.operator, rows: contract.edited.ids, proposalId: 'proposal-edit' });
  apply({ action: 'FilterApplied(edit)', intent: 'edit', mountEpoch: 2,
    before: readFrom(created, 'before-edit-apply', 2),
    after: readFrom(edited, 'after-edit-apply', 2),
    proposalId: editProposal.proposalId, requestValue: editedRequestValue,
    responseValue: editedResponseValue, savedValue: contract.edited.savedValue,
    operator: contract.edited.operator, rows: contract.edited.ids });
  reload(readFrom(edited, 'before-edit-reload', 2), readFrom(edited, 'after-edit-reload', 3), 3,
  contract.edited.ids);

  const removeProposal = proposal({ intent: 'remove', mountEpoch: 3,
    before: readFrom(edited, 'before-remove-proposal', 3),
    requestValue: null, responseValue: null, operator: contract.edited.operator,
    rows: contract.rows.map(row => row.id), proposalId: 'proposal-remove' });
  apply({ action: 'FilterRemoved', intent: 'remove', mountEpoch: 3,
    before: readFrom(edited, 'before-remove-apply', 3),
    after: readFrom(removed, 'after-remove-apply', 3),
    proposalId: removeProposal.proposalId, requestValue: null, responseValue: null,
    savedValue: null, operator: contract.edited.operator, rows: contract.rows.map(row => row.id) });
  reload(readFrom(removed, 'before-remove-reload', 3), readFrom(removed, 'after-remove-reload', 4), 4,
    contract.rows.map(row => row.id));

  const lifecycle = {
    schemaVersion: 1,
    scenario: 'basic-temporal-filter',
    caseName,
    sourceCommit: 'a'.repeat(40),
    sourceFingerprint: { sha256: 'b'.repeat(64) },
    fixtureSha256: 'c'.repeat(64),
    fixturePath: `/fixtures/${contract.resourceType}.ndjson`,
    project: 'loom_dev_verify_temporal-fixture',
    generation: 'generation-temporal-fixture',
    resourceType: contract.resourceType,
    fieldPath: contract.fieldPath,
    outputId,
    temporalColumnId: columnId,
    rows: clone(contract.rows),
    ids: contract.rows.map(row => row.id),
    steps: rawSteps,
    requestEvents: requests,
    builderReads: [],
    domObservations: [],
  };
  return {
    name: `basic-temporal-filter:${caseName}`,
    model: 'TemporalFilterJourney',
    initialObservation: {
      readId: blank.readId,
      workspaceDocuments: blank.workspaceDocuments,
      draftVersion: blank.draftVersion,
      draftDigest: blank.draftDigest,
      mountEpoch: blank.mountEpoch,
    },
    actions,
    unmodeledEvents: [],
    temporalFilterLifecycle: lifecycle,
  };
};

const runTlc = (trace, moduleSuffix) => {
  assert(tlcJar, 'Set TLA2TOOLS_JAR to the verified TLC 1.7.4 jar for this focused check.');
  const compiled = compileTemporalFilterJourneyTrace(trace, { moduleName: `TemporalFilter${moduleSuffix}` });
  const directory = mkdtempSync(join(tmpdir(), 'loom-temporal-filter-tla-'));
  const metadataDirectory = join(directory, 'states');
  mkdirSync(metadataDirectory);
  try {
    copyFileSync(modelPath, join(directory, 'TemporalFilterJourney.tla'));
    writeFileSync(join(directory, `${compiled.moduleName}.tla`), compiled.moduleText);
    writeFileSync(join(directory, `${compiled.moduleName}.cfg`), compiled.configText);
    const result = spawnSync('java', [
      '-XX:+UseParallelGC', '-cp', tlcJar, 'tlc2.TLC', '-workers', '1',
      '-metadir', metadataDirectory, '-config', `${compiled.moduleName}.cfg`,
      `${compiled.moduleName}.tla`,
    ], { cwd: directory, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
    return { ...result, output: `${result.stdout ?? ''}${result.stderr ?? ''}`, compiled };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
};

test('compiler binds all DATE capture actions and the observer ledger into a TLA instance', () => {
  const trace = makeJourney('date');
  const validated = validateTemporalFilterJourneyTrace(trace);
  const compiled = compileTemporalFilterJourneyTrace(trace);
  assert.equal(validated.eventCount, 16);
  assert.equal(validated.mutationCount, 6);
  assert.equal(validated.proposalCount, 5);
  assert.equal(compiled.moduleName, 'TemporalFilterJourneyDateTrace');
  assert.match(compiled.moduleText, /EXTENDS TemporalFilterJourney/u);
  assert.match(compiled.moduleText, /TraceEvents == <<\[action \|-> "ExplorerCreated"/u);
  assert.match(compiled.configText, /CONSTANT ModelEvents <- TraceEvents/u);
  assert.match(compiled.configText, /CONSTANT ObservedRequests <- TraceObservedRequests/u);
  assert.match(compiled.configText, /INVARIANT InvObservedMutationsConsumed/u);
  assert.match(compiled.configText, /PROPERTY EventuallyComplete/u);
  assert.equal(compiled.eventCount, trace.actions.length);
});

test('compiler maps DATE_TIME through the literal typed oracle and captured native local input', () => {
  const trace = makeJourney('date-time');
  const input = validateTemporalFilterJourneyTrace(trace);
  assert.equal(input.profile, 'DATE_TIME');
  assert.equal(input.caseValue.inputValue, '2026-01-01T16:00');
  assert.equal(input.caseValue.initialRequestValue.dateTime, '2026-01-02T00:00:00.000Z');
  assert.equal(input.caseValue.initialResponseValue.dateTime, '2026-01-02T00:00:00Z');
  assert.deepEqual(input.caseValue.initialRowIds, []);
  assert.deepEqual(input.caseValue.editedRowIds, [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
  ]);
});

test('compiler rejects missing semantic actions and unlinked observer mutations before emission', () => {
  const missingAction = makeJourney('date');
  missingAction.actions.pop();
  missingAction.temporalFilterLifecycle.steps.pop();
  assert.throws(() => compileTemporalFilterJourneyTrace(missingAction), /Every semantic lifecycle action/u);

  const missingMutation = makeJourney('date');
  const command = missingMutation.actions.find(event => event.action === 'SourceTableCreated');
  command.request.isMutation = false;
  assert.throws(() => compileTemporalFilterJourneyTrace(missingMutation), /preserve the observer's mutation classification/u);
});

test('DATE and DATE_TIME captured evidence both pass their generated TLA trace instances', { timeout: 30_000 }, () => {
  for (const [caseName, suffix] of [['date', 'DatePositive'], ['date-time', 'DateTimePositive']]) {
    const result = runTlc(makeJourney(caseName), suffix);
    assert.equal(result.status, 0, result.output);
    assert.match(result.output, /Model checking completed\. No error has been found\./u, result.output);
    assert.match(result.output, /states generated, [0-9,]+ distinct states found/u, result.output);
  }
});

test('TLA rejects stale proposal ownership, stale draft CAS, and reordered actions', { timeout: 30_000 }, () => {
  const wrongOwner = makeJourney('date');
  const ownerApply = wrongOwner.actions.find(event => event.action === 'FilterApplied(create)');
  ownerApply.request.body.commands[0].proposalId = 'stale-proposal';
  const ownerRaw = wrongOwner.temporalFilterLifecycle.requestEvents.find(request =>
    request.browserRequestId === ownerApply.request.captureId);
  ownerRaw.request.commands[0].proposalId = 'stale-proposal';
  const ownerResult = runTlc(wrongOwner, 'WrongOwner');
  assert.notEqual(ownerResult.status, 0, ownerResult.output);
  assert.match(ownerResult.output, /Temporal property EventuallyComplete violated|Deadlock reached/u,
    ownerResult.output);

  const staleCAS = makeJourney('date');
  const casApply = staleCAS.actions.find(event => event.action === 'FilterApplied(create)');
  casApply.request.body.expectedDraftVersion += 1;
  const casRaw = staleCAS.temporalFilterLifecycle.requestEvents.find(request =>
    request.browserRequestId === casApply.request.captureId);
  casRaw.request.expectedDraftVersion += 1;
  const casResult = runTlc(staleCAS, 'WrongCas');
  assert.notEqual(casResult.status, 0, casResult.output);
  assert.match(casResult.output, /Temporal property EventuallyComplete violated|Deadlock reached/u,
    casResult.output);

  const wrongOrder = makeJourney('date');
  [wrongOrder.actions[3], wrongOrder.actions[4]] = [wrongOrder.actions[4], wrongOrder.actions[3]];
  [wrongOrder.temporalFilterLifecycle.steps[3], wrongOrder.temporalFilterLifecycle.steps[4]] =
    [wrongOrder.temporalFilterLifecycle.steps[4], wrongOrder.temporalFilterLifecycle.steps[3]];
  const orderResult = runTlc(wrongOrder, 'WrongOrder');
  assert.notEqual(orderResult.status, 0, orderResult.output);
  assert.match(orderResult.output, /InvSemanticActionOrder is equal to FALSE/u, orderResult.output);
});
