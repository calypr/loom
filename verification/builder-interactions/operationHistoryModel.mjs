#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const MAX_USER_EDITS = 4;
export const MAX_TOTAL_TRANSITIONS = 6;
export const MAX_DOWNSTREAM_STEPS = 2;
const GROUP_STEP_ID = 'group-step';
const AGGREGATE_COLUMN_ID = 'aggregate-value';
const WORKSPACE_CONTEXT = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  project: 'HTAN_INT/BForePC',
  explorerId: 'builder',
  authResourcePath: '/programs/HTAN_INT/projects/BForePC',
  snapshotToken: 'snapshot-operation-history',
  explorerTitle: 'Operation history',
  outputId: 'patients',
  outputTitle: 'Patients',
  rootResourceType: 'Patient',
  route: { occurrenceId: 'base', resourceType: 'Patient' },
  rowKind: 'RECORDS',
};
const SOURCE_FIELDS = {
  A: { role: 'KEY_A', id: 'gender-id', column: 'gender', path: 'gender', label: 'Gender', type: 'string' },
  B: { role: 'KEY_B', id: 'active-id', column: 'active', path: 'active', label: 'Active', type: 'boolean' },
  numeric: { role: 'NUMERIC', id: 'multiple-birth-integer-id', column: 'multiple_birth_integer', path: 'multipleBirthInteger', label: 'Multiple birth integer', type: 'integer' },
};

const clone = (value) => JSON.parse(JSON.stringify(value));
const stableId = (value) => createHash('sha256').update(JSON.stringify(value)).digest('hex').slice(0, 12);
const countUserActions = (actions) => actions.filter(({ kind }) =>
  ['editAggregate', 'editKey', 'restoreKey', 'cancel', 'apply'].includes(kind),
).length;

function keyOutputId(key, generation) {
  return key ? `group-key-${key}-${generation}` : undefined;
}

function normalizeInitial(seed) {
  const selectedKey = seed.selectedKey ?? (seed.keyA ? 'A' : seed.keyB ? 'B' : undefined);
  const initialKeyId = selectedKey ? keyOutputId(selectedKey, 1) : undefined;
  let previousStepId = GROUP_STEP_ID;
  const downstream = [];
  for (const step of seed.downstream ?? []) {
    downstream.push({
      id: step.id,
      kind: step.kind,
      dependency: step.dependency,
      columnId: step.dependency === 'aggregate' ? AGGREGATE_COLUMN_ID : initialKeyId,
      inputStepId: previousStepId,
      ...(step.kind === 'CALCULATE' ? { outputColumnId: `calculated-${step.id}` } : {}),
    });
    previousStepId = step.id;
  }
  const sourceColumns = [
    ...(seed.keyA ? [SOURCE_FIELDS.A] : []),
    ...(seed.keyB ? [SOURCE_FIELDS.B] : []),
    ...(seed.numericInputPresent ? [SOURCE_FIELDS.numeric] : []),
  ];
  return {
    tableId: 'patients',
    workspaceContext: clone(WORKSPACE_CONTEXT),
    sourceColumns,
    aggregate: seed.aggregate,
    numericInputPresent: seed.numericInputPresent,
    keyA: seed.keyA,
    keyB: seed.keyB,
    selectedKey,
    keyOutputId: initialKeyId,
    aggregateOutputId: AGGREGATE_COLUMN_ID,
    groupStepId: GROUP_STEP_ID,
    downstream,
  };
}

function initialState(initial) {
  const workspace = clone(initial);
  return {
    accepted: clone(workspace),
    candidate: clone(workspace),
    proposalStatus: 'UNPROPOSED',
    resolvedCandidate: undefined,
    removedStepIds: [],
    proposalSnapshot: undefined,
    applyPending: false,
    lastResult: 'NONE',
    acceptedChangesOnLastAction: false,
  };
}

function stateKey(state) {
  return JSON.stringify({
    accepted: state.accepted,
    candidate: state.candidate,
    proposalStatus: state.proposalStatus,
    resolvedCandidate: state.resolvedCandidate,
    removedStepIds: state.removedStepIds,
    proposalSnapshot: state.proposalSnapshot,
    applyPending: state.applyPending,
    lastResult: state.lastResult,
  });
}

function proposalRequestKey(state) {
  const constructionIntent = (workspace) => ({
    groupStepId: workspace.groupStepId,
    groupKey: workspace.selectedKey
      ? {
          inputColumnId: workspace.selectedKey === 'A' ? SOURCE_FIELDS.A.id : SOURCE_FIELDS.B.id,
          outputColumnId: workspace.keyOutputId,
        }
      : undefined,
    aggregate: {
      operation: workspace.aggregate,
      outputColumnId: workspace.aggregateOutputId,
      ...(workspace.aggregate === 'MEAN' ? { inputColumnId: SOURCE_FIELDS.numeric.id } : {}),
    },
    downstream: workspace.downstream.map(({ id, kind, dependency, columnId, inputStepId, outputColumnId }) => ({ id, kind, dependency, columnId, inputStepId, outputColumnId })),
  });
  return JSON.stringify({
    tableId: state.accepted.tableId,
    workspaceContext: state.accepted.workspaceContext,
    sourceColumns: state.accepted.sourceColumns,
    acceptedWorkspace: constructionIntent(state.accepted),
    candidateConstruction: constructionIntent(state.candidate),
    changedStepId: GROUP_STEP_ID,
    removeStepIds: [],
  });
}

function groupOutputTypes(workspace) {
  const groupOutputTypes = {};
  if (workspace.keyOutputId) {
    const sourceKey = workspace.selectedKey === 'A' ? SOURCE_FIELDS.A : SOURCE_FIELDS.B;
    groupOutputTypes[workspace.keyOutputId] = sourceKey.type;
  }
  groupOutputTypes[workspace.aggregateOutputId] = workspace.aggregate === 'COUNT_ROWS' ? 'integer' : 'decimal';
  return groupOutputTypes;
}

function modelView(workspace) {
  const outputs = groupOutputTypes(workspace);
  const downstreamInputTypes = Object.fromEntries(workspace.downstream.map((step) => {
    const type = outputs[step.columnId] ?? 'MISSING';
    return [step.id, { columnId: step.columnId, inputStepId: step.inputStepId, type }];
  }));
  const downstreamOutputTypes = Object.fromEntries(workspace.downstream
    .filter((step) => step.kind === 'CALCULATE')
    .map((step) => [step.id, {
      columnId: step.outputColumnId,
      // The bounded expression is aggregate + integer literal 1; it has no user cast.
      type: workspace.aggregate === 'COUNT_ROWS' ? 'integer' : 'decimal',
    }]));
  return {
    stepIds: [GROUP_STEP_ID, ...workspace.downstream.map(({ id }) => id)],
    groupOutputTypes: outputs,
    downstreamInputTypes,
    downstreamOutputTypes,
  };
}

function missingGroupInputs(workspace) {
  const missing = [];
  if (workspace.aggregate === 'MEAN' && !workspace.numericInputPresent) {
    missing.push({ id: GROUP_STEP_ID, columnId: 'numeric-input' });
  }
  return missing;
}

function resolveCandidate(workspace) {
  const available = groupOutputTypes(workspace);
  const resolved = clone(workspace);
  resolved.downstream = [];
  const removedStepIds = [];
  let previousStepId = GROUP_STEP_ID;
  for (const step of workspace.downstream) {
    if (!available[step.columnId]) {
      removedStepIds.push(step.id);
      continue;
    }
    const survivor = { ...step, inputStepId: previousStepId };
    resolved.downstream.push(survivor);
    previousStepId = survivor.id;
  }
  return { resolvedCandidate: resolved, removedStepIds };
}

function reviewedSnapshot(candidate, resolvedCandidate, removedStepIds) {
  return JSON.stringify({ candidate, resolvedCandidate, removedStepIds });
}

function dirty(state) {
  return JSON.stringify(state.candidate) !== JSON.stringify(state.accepted);
}

function applyAction(state, action, actionIndex) {
  const next = clone(state);
  next.acceptedChangesOnLastAction = false;
  next.lastResult = action.kind;

  switch (action.kind) {
    case 'editAggregate':
      if (action.aggregate === 'MEAN' && !next.candidate.numericInputPresent) return undefined;
      if (action.aggregate === next.candidate.aggregate) return undefined;
      next.candidate.aggregate = action.aggregate;
      next.proposalStatus = 'UNPROPOSED';
      next.resolvedCandidate = undefined;
      next.removedStepIds = [];
      next.proposalSnapshot = undefined;
      break;
    case 'editKey':
    case 'restoreKey': {
      const key = action.key === 'none' ? undefined : action.key;
      if (key && !next.candidate[`key${key}`]) return undefined;
      if (key === next.candidate.selectedKey) return undefined;
      const generations = next.candidate.keyGenerations ?? { A: next.candidate.selectedKey === 'A' ? 1 : 0, B: next.candidate.selectedKey === 'B' ? 1 : 0 };
      const updated = { ...next.candidate, selectedKey: key, keyOutputId: undefined, keyGenerations: clone(generations) };
      if (key) {
        updated.keyGenerations[key] += 1;
        updated.keyOutputId = keyOutputId(key, updated.keyGenerations[key]);
      }
      next.candidate = updated;
      next.proposalStatus = 'UNPROPOSED';
      next.resolvedCandidate = undefined;
      next.removedStepIds = [];
      next.proposalSnapshot = undefined;
      break;
    }
    case 'propose':
      if (!dirty(next) || next.applyPending || next.proposalStatus !== 'UNPROPOSED') return undefined;
      ({ resolvedCandidate: next.resolvedCandidate, removedStepIds: next.removedStepIds } = resolveCandidate(next.candidate));
      next.proposalStatus = missingGroupInputs(next.candidate).length === 0 ? 'READY' : 'NEEDS_REPAIR';
      next.proposalSnapshot = reviewedSnapshot(next.candidate, next.resolvedCandidate, next.removedStepIds);
      break;
    case 'cancel':
      if (!dirty(next) && next.proposalStatus === 'UNPROPOSED') return undefined;
      if (next.applyPending) return undefined;
      next.candidate = clone(next.accepted);
      next.proposalStatus = 'CANCELLED';
      next.resolvedCandidate = undefined;
      next.removedStepIds = [];
      next.proposalSnapshot = undefined;
      break;
    case 'apply':
      if (next.proposalStatus !== 'READY' || next.applyPending) return undefined;
      if (!next.resolvedCandidate || next.proposalSnapshot !== reviewedSnapshot(next.candidate, next.resolvedCandidate, next.removedStepIds)) return undefined;
      next.proposalStatus = 'APPLYING';
      next.applyPending = true;
      break;
    case 'applyAck':
      if (!next.applyPending) return undefined;
      if (action.result === 'success') {
        next.accepted = clone(next.resolvedCandidate);
        next.proposalStatus = 'APPLIED';
        next.acceptedChangesOnLastAction = true;
      } else {
        next.proposalStatus = 'APPLY_ERROR';
      }
      next.applyPending = false;
      next.proposalSnapshot = undefined;
      break;
    default:
      throw new Error(`unknown model action ${action.kind}`);
  }

  next.lastActionIndex = actionIndex;
  return next;
}

function enabledActions(state) {
  if (state.lastResult === 'applyAck' || state.lastResult === 'cancel') return [];
  const actions = [];
  const candidate = state.candidate;
  if (state.proposalStatus !== 'APPLYING' && !state.applyPending) {
    const alternative = candidate.aggregate === 'COUNT_ROWS' ? 'MEAN' : 'COUNT_ROWS';
    if (alternative !== 'MEAN' || candidate.numericInputPresent) {
      actions.push({ kind: 'editAggregate', aggregate: alternative });
    }
    for (const key of ['none', 'A', 'B']) {
      const selected = key === 'none' ? undefined : key;
      if (selected === candidate.selectedKey) continue;
      if (selected && !candidate[`key${selected}`]) continue;
      if (selected && !candidate.selectedKey && selected === state.accepted.selectedKey) continue;
      actions.push({ kind: 'editKey', key });
    }
    if (!candidate.selectedKey && state.accepted.selectedKey && candidate[`key${state.accepted.selectedKey}`]) {
      actions.push({ kind: 'restoreKey', key: state.accepted.selectedKey });
    }
    if (dirty(state) || state.proposalStatus !== 'UNPROPOSED') actions.push({ kind: 'cancel' });
    if (state.proposalStatus === 'READY') actions.push({ kind: 'apply' });
    if (dirty(state) && state.proposalStatus === 'UNPROPOSED') actions.push({ kind: 'propose' });
  } else if (state.applyPending) {
    actions.push({ kind: 'applyAck', result: 'success' });
  }
  return actions;
}

function assertState(node) {
  const state = node.state;
  const candidateIDs = [GROUP_STEP_ID, ...state.candidate.downstream.map(({ id }) => id)];
  const rawProjection = modelView(state.candidate);
  const currentProjection = modelView(state.resolvedCandidate ?? state.candidate);
  const allowedApplyState = state.proposalStatus !== 'APPLYING' || state.applyPending;
  const readyHasResolvedProposal = state.proposalStatus !== 'READY' || (
    state.resolvedCandidate !== undefined && missingGroupInputs(state.candidate).length === 0
  );
  const expectedAccepted = node.action?.kind === 'applyAck' && node.action.result === 'success'
    ? node.parent.state.resolvedCandidate
    : node.parent?.state.accepted ?? state.accepted;
  const acceptedIsCandidateOnlyAfterAck = JSON.stringify(state.accepted) === JSON.stringify(expectedAccepted);
  const stableGroupID = state.accepted.groupStepId === GROUP_STEP_ID && state.candidate.groupStepId === GROUP_STEP_ID;
  const uniqueIDs = new Set(candidateIDs).size === candidateIDs.length && candidateIDs.length <= MAX_DOWNSTREAM_STEPS + 1;
  const rawCandidatePreservedByProposal = node.action?.kind !== 'propose' || JSON.stringify(state.candidate) === JSON.stringify(node.parent.state.candidate);
  const proposalSnapshotMatches = !state.proposalSnapshot || state.proposalSnapshot === reviewedSnapshot(state.candidate, state.resolvedCandidate, state.removedStepIds);

  // This oracle is independent from resolveCandidate: only the Group's current key and aggregate IDs are available inputs.
  const groupColumns = new Set([state.candidate.aggregateOutputId]);
  if (state.candidate.keyOutputId) groupColumns.add(state.candidate.keyOutputId);
  const expectedSurvivors = state.candidate.downstream.filter(({ columnId }) => groupColumns.has(columnId));
  const expectedRemoved = state.candidate.downstream.filter(({ columnId }) => !groupColumns.has(columnId)).map(({ id }) => id);
  const actualResolved = state.resolvedCandidate?.downstream;
  const exactCascadeMembership = !state.resolvedCandidate || (
    JSON.stringify(state.removedStepIds) === JSON.stringify(expectedRemoved) &&
    actualResolved.length === expectedSurvivors.length &&
    actualResolved.every((resolved, index) => {
      const raw = expectedSurvivors[index];
      return resolved.id === raw.id && resolved.kind === raw.kind && resolved.dependency === raw.dependency &&
        resolved.columnId === raw.columnId && resolved.outputColumnId === raw.outputColumnId;
    })
  );
  const resolvedBaseMatchesRaw = !state.resolvedCandidate || (() => {
    const { downstream: _rawDownstream, ...rawBase } = state.candidate;
    const { downstream: _resolvedDownstream, ...resolvedBase } = state.resolvedCandidate;
    return JSON.stringify(rawBase) === JSON.stringify(resolvedBase);
  })();
  const resolvedStagesReconnected = !state.resolvedCandidate || state.resolvedCandidate.downstream.every((step, index) =>
    step.inputStepId === (index === 0 ? GROUP_STEP_ID : state.resolvedCandidate.downstream[index - 1].id));
  if (!allowedApplyState || !readyHasResolvedProposal || !acceptedIsCandidateOnlyAfterAck || !stableGroupID || !uniqueIDs ||
    !currentProjection.stepIds.includes(GROUP_STEP_ID) || !rawCandidatePreservedByProposal || !proposalSnapshotMatches ||
    !exactCascadeMembership || !resolvedBaseMatchesRaw || !resolvedStagesReconnected) {
    const caseId = `op-${stableId({ seed: node.seedId, actions: node.actions })}`;
    const expected = {
      independentCascadeInvariant: 'retain every dependent whose Group input ID exists; remove exactly the others',
      acceptedUnchanged: clone(expectedAccepted),
      proposalStatus: state.proposalStatus,
      uniqueStepIds: true,
      stepIds: currentProjection.stepIds,
      groupOutputTypes: currentProjection.groupOutputTypes,
      downstreamInputTypes: currentProjection.downstreamInputTypes,
      downstreamOutputTypes: currentProjection.downstreamOutputTypes,
      removedStepIds: state.removedStepIds,
    };
    const actual = {
      accepted: state.accepted,
      candidate: state.candidate,
      resolvedCandidate: state.resolvedCandidate,
      missingGroupInputs: missingGroupInputs(state.candidate),
      rawCandidateProjection: rawProjection,
      expectedSurvivorIDs: expectedSurvivors.map(({ id }) => id),
      expectedRemovedStepIDs: expectedRemoved,
      exactCascadeMembership,
      resolvedBaseMatchesRaw,
    };
    throw new Error(`counterexample ${caseId}\npreceding events: ${JSON.stringify(node.actions)}\nexpected: ${JSON.stringify(expected)}\nactual: ${JSON.stringify(actual)}`);
  }
}

function seeds() {
  const result = [];
  for (const numericInputPresent of [false, true]) {
    for (const keyA of [false, true]) {
      for (const keyB of [false, true]) {
        const selectedKey = keyA ? 'A' : keyB ? 'B' : undefined;
        result.push({
          id: `inputs-${Number(numericInputPresent)}-${Number(keyA)}-${Number(keyB)}`,
          aggregate: 'COUNT_ROWS', numericInputPresent, keyA, keyB, selectedKey, downstream: [],
        });
      }
    }
  }
  result.push({
    id: 'positive-count-with-filter-and-calculate', aggregate: 'COUNT_ROWS', numericInputPresent: true,
    keyA: true, keyB: false, selectedKey: 'A',
    downstream: [
      { id: 'filter-by-aggregate', kind: 'FILTER', dependency: 'aggregate' },
      { id: 'calculate-from-aggregate', kind: 'CALCULATE', dependency: 'aggregate' },
    ],
  });
  result.push({
    id: 'group-key-cascade-removes-filter-preserves-calculate', aggregate: 'COUNT_ROWS', numericInputPresent: true,
    keyA: true, keyB: true, selectedKey: 'A',
    downstream: [
      { id: 'filter-by-group-key', kind: 'FILTER', dependency: 'group-key' },
      { id: 'calculate-from-aggregate', kind: 'CALCULATE', dependency: 'aggregate' },
    ],
  });
  result.push({
    id: 'accepted-history-none', aggregate: 'COUNT_ROWS', numericInputPresent: true,
    keyA: true, keyB: false, selectedKey: 'A', downstream: [],
  });
  result.push({
    id: 'accepted-history-filter', aggregate: 'COUNT_ROWS', numericInputPresent: true,
    keyA: true, keyB: false, selectedKey: 'A',
    downstream: [{ id: 'filter-by-aggregate', kind: 'FILTER', dependency: 'aggregate' }],
  });
  result.push({
    id: 'accepted-history-calculate', aggregate: 'COUNT_ROWS', numericInputPresent: true,
    keyA: true, keyB: false, selectedKey: 'A',
    downstream: [{ id: 'calculate-from-aggregate', kind: 'CALCULATE', dependency: 'aggregate' }],
  });
  result.push({
    id: 'mean-input-present', aggregate: 'MEAN', numericInputPresent: true,
    keyA: true, keyB: false, selectedKey: 'A',
    downstream: [{ id: 'calculate-from-aggregate', kind: 'CALCULATE', dependency: 'aggregate' }],
  });
  return result;
}

function expectation(state, action, actionIndex, candidateActionIndex) {
  const candidateKeyId = state.candidate.keyOutputId;
  const acceptedKeyId = state.accepted.keyOutputId;
  const candidateView = modelView(state.candidate);
  const resolvedView = modelView(state.resolvedCandidate ?? state.candidate);
  return {
    actionIndex,
    ...(action.kind === 'propose' ? { candidateActionIndex } : {}),
    ...(action.kind === 'propose' ? {
      requestFingerprint: stableId(proposalRequestKey(state)),
    } : {}),
    proposalStatus: state.proposalStatus,
    ...resolvedView,
    candidateStepIds: candidateView.stepIds,
    candidateInputStepIds: Object.fromEntries(Object.entries(candidateView.downstreamInputTypes).map(([id, input]) => [id, input.inputStepId])),
    candidateGroupKeyOutputId: candidateKeyId ?? null,
    acceptedGroupKeyOutputId: acceptedKeyId ?? null,
    groupKeyOutputRelation: !candidateKeyId
      ? 'ABSENT'
      : candidateKeyId === acceptedKeyId ? 'SAME_AS_ACCEPTED' : 'FRESH_FROM_ACCEPTED',
    acceptedAggregate: state.accepted.aggregate,
    candidateAggregate: state.candidate.aggregate,
    acceptedChangesOnApply: state.acceptedChangesOnLastAction,
    applyAck: action.kind === 'applyAck' ? action.result.toUpperCase() : 'NOT_APPLICABLE',
    missingInputs: missingGroupInputs(state.candidate),
    removedStepIds: clone(state.removedStepIds),
  };
}

function makeCase(node, id) {
  const events = node.actions;
  let candidateActionIndex = -1;
  const expectations = node.history.map((item, actionIndex) => {
    const action = events[actionIndex];
    if (['editAggregate', 'editKey', 'restoreKey'].includes(action.kind)) {
      candidateActionIndex = actionIndex;
    }
    return expectation(item, action, actionIndex, candidateActionIndex);
  });
  const caseId = id ?? `operation-history-${coverageName(node) ?? stableId({ seed: node.seedId, actions: events })}`;
  return {
    id: caseId,
    initial: clone(node.initial),
    actions: clone(events),
    expectations,
  };
}

function coverageName(node) {
  const last = node.action?.kind;
  if (last === 'propose' && node.seedId === 'group-key-cascade-removes-filter-preserves-calculate' &&
    node.state.candidate.aggregate === 'MEAN' &&
    node.state.removedStepIds.includes('filter-by-group-key') &&
    node.state.resolvedCandidate?.downstream.some(({ id }) => id === 'calculate-from-aggregate')) {
    return 'group-key-cascade-count-to-mean-ready-preserves-calculate';
  }
  if (last === 'propose' && node.state.proposalStatus === 'READY' && node.seedId.startsWith('accepted-history-') &&
    node.state.candidate.selectedKey !== node.state.accepted.selectedKey) {
    return `key-edit-ready-${node.seedId.slice('accepted-history-'.length)}`;
  }
  if (last === 'cancel' && node.parent?.state.proposalStatus === 'READY') return 'cancel-ready-proposal';
  if (last === 'cancel' && node.parent?.state.proposalStatus === 'NEEDS_REPAIR') return 'cancel-needs-repair';
  if (last === 'applyAck' && node.state.accepted.aggregate === 'MEAN' &&
    (node.seedId === 'positive-count-with-filter-and-calculate' || node.seedId.startsWith('accepted-history-'))) {
    if (node.seedId === 'positive-count-with-filter-and-calculate') return 'count-to-mean-apply';
    return `count-to-mean-${node.seedId}`;
  }
  if (last === 'applyAck' && node.seedId === 'group-key-cascade-removes-filter-preserves-calculate' &&
    node.state.accepted.aggregate === 'MEAN' &&
    node.state.accepted.downstream.some(({ id }) => id === 'calculate-from-aggregate') &&
    !node.state.accepted.downstream.some(({ id }) => id === 'filter-by-group-key')) {
    return 'group-key-cascade-count-to-mean-apply-ack';
  }
  return undefined;
}

export function exploreOperationHistory() {
  const queue = seeds().map((seed) => {
    const initial = normalizeInitial(seed);
    return {
      seedId: seed.id,
      initial,
      state: initialState(initial),
      actions: [],
      history: [],
      parent: undefined,
      action: undefined,
      userActions: 0,
    };
  });
  const seen = new Set();
  const reachable = new Set();
  const checkpoints = new Map();
  const proposalRequests = new Map();
  let cursor = 0;

  while (cursor < queue.length) {
    const node = queue[cursor++];
    const key = `${node.seedId}|${node.actions.length}|${node.userActions}|${stateKey(node.state)}`;
    if (seen.has(key)) continue;
    seen.add(key);
    reachable.add(`${node.seedId}|${stateKey(node.state)}`);
    assertState(node);

    if (node.action?.kind === 'propose') {
      const requestKey = proposalRequestKey(node.state);
      if (!proposalRequests.has(requestKey)) proposalRequests.set(requestKey, node);
    }

    if (node.actions.length > 0 && ['propose', 'cancel', 'applyAck'].includes(node.action?.kind)) {
      const coverage = coverageName(node);
      if (coverage && !checkpoints.has(coverage)) checkpoints.set(coverage, makeCase(node));
    }
    if (node.actions.length >= MAX_TOTAL_TRANSITIONS) continue;

    for (const action of enabledActions(node.state)) {
      const userActions = node.userActions + (countUserActions([action]) ? 1 : 0);
      if (userActions > MAX_USER_EDITS) continue;
      const state = applyAction(node.state, action, node.actions.length);
      if (!state) continue;
      const child = {
        ...node,
        state,
        actions: [...node.actions, action],
        history: [...node.history, state],
        parent: node,
        action,
        userActions,
      };
      assertState(child);
      queue.push(child);
    }
  }

  const proposalCases = [...proposalRequests.entries()].map(([requestKey, node]) => {
    const id = `operation-history-request-${stableId(requestKey)}`;
    return {
      ...makeCase(node, id),
      traceKind: 'proposal-input',
      requestFingerprint: stableId(requestKey),
    };
  });
  const witnessCases = [...checkpoints.values()].map((item) => ({ ...item, traceKind: 'lifecycle-witness' }));
  const cases = [...new Map([...proposalCases, ...witnessCases].map((item) => [item.id, item])).values()]
    .sort((a, b) => a.id.localeCompare(b.id));
  return {
    cases,
    stats: {
      seedCount: seeds().length,
      reachableStateCount: reachable.size,
      exploredBoundedNodes: seen.size,
      emittedTraceCount: cases.length,
      uniqueProposalRequestCount: proposalRequests.size,
      proposalInputTraceCount: proposalCases.length,
      lifecycleWitnessCount: witnessCases.length,
      maxTotalTransitions: MAX_TOTAL_TRANSITIONS,
      maxUserEdits: MAX_USER_EDITS,
      maxDownstreamSteps: MAX_DOWNSTREAM_STEPS,
    },
  };
}

function main() {
  const { cases, stats } = exploreOperationHistory();
  const outputPath = process.env.BUILDER_OPERATION_TRACES ?? join(mkdtempSync(join(tmpdir(), 'loom-builder-operation-history-model-')), 'operation-history-cases.json');
  mkdirSync(dirname(outputPath), { recursive: true });
  writeFileSync(outputPath, `${JSON.stringify({ schemaVersion: 1, cases }, null, 2)}\n`);
  console.log(`Operation history model: ${JSON.stringify(stats)}`);
  console.log(`Trace JSON: ${outputPath}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
