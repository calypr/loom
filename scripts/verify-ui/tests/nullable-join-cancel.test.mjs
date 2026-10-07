import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { classifyExpectedCdaCancellation } from '../helpers/cda-fixtures.mjs';
import { validateNullableJoinSwitch } from '../nullable-join-cancel.mjs';

const classifyThroughFixture = (report, record, context) => {
  const request = {
    url: () => record.url,
    method: () => record.method,
    headers: () => ({}),
    postDataJSON: () => record.method === 'POST' ? {
      ...record.requestDetails,
      expectedDraftVersion: record.requestDetails.draftVersion,
      expectedDraftDigest: record.requestDetails.draftDigest,
    } : undefined,
  };
  const requestFailures = new Map([[request, {
    requestId: null,
    playwrightRequestId: record.playwrightRequestId,
    browserRequestId: record.browserRequestId,
    method: record.method,
    url: record.url,
    errorText: record.errorText,
  }]]);
  const capture = { byRequest: new Map() };
  if (record.browserRequestId) capture.byRequest.set(request, { browserRequestId: record.browserRequestId });
  const proof = {
    project: context.scope.project,
    explorer: context.scope.explorer,
    generation: context.scope.generation,
    outgoingOutputId: context.outgoing.outputId,
    outgoingSelectionRevisionId: context.outgoing.selectionId,
    outgoingDraft: context.outgoing.draft,
    retirement: 'replacement owner switch',
    scopeAction: context.action.label,
    scopeRequest: {
      requestId: null,
      draftVersion: record.requestDetails.draftVersion,
      draftDigest: record.requestDetails.draftDigest,
      outputId: record.requestDetails.outputId,
      stageId: record.requestDetails.stageId,
    },
  };
  return classifyExpectedCdaCancellation({
    request,
    reason: 'A later owner switch superseded this exact request.',
    proof,
    report,
    requestFailures,
    trackers: new Set([capture]),
  });
};

const makeFixture = salt => {
  const snapshot = name => `sha256:${createHash('sha256').update(`${salt}:${name}`).digest('hex')}`;
  const scope = { project: `project-${salt}`, generation: `generation-${salt}`, explorer: `explorer-${salt}` };
  const startedAt = '2026-10-07T06:46:25.813Z';
  const base = Date.parse(startedAt);
  const left = { outputId: `out-left-${salt}`, selectionId: `sel-left-${salt}`, draft: { snapshotToken: snapshot('left'), version: 7, digest: `digest-left-${salt}` } };
  const right = { outputId: `out-right-${salt}`, selectionId: `sel-right-${salt}`, draft: { snapshotToken: snapshot('right'), version: 8, digest: `digest-right-${salt}` } };
  const target = { outputId: `out-target-${salt}`, selectionId: null, draft: { snapshotToken: snapshot('target'), version: 9, digest: `digest-target-${salt}` } };
  const rows = [['obs-null', '—'], ['obs-d1', 'd'], ['obs-d2', 'd']];
  const actions = [
    { id: `action-select-${salt}`, label: `Select right ${salt}`, locator: `table-${right.outputId}`, status: 'passed' },
    { id: `action-open-${salt}`, label: `Open Combine ${salt}`, locator: `combine-${salt}`, status: 'passed' },
  ];
  const route = suffix => `/api/v1/projects/${scope.project}/explorers/${scope.explorer}${suffix}`;
  const ownerSwitches = [
    { action: actions[0], outgoing: left, next: { ...right, proof: { kind: 'visible-rows', selectionId: right.selectionId, expectedRows: [rows[1], rows[2], rows[0]] } }, start: 1000 },
    { action: actions[1], outgoing: right, next: { ...target, proof: { kind: 'empty-target' } }, start: 2000 },
  ];
  const network = [];
  const nativeRequests = [];
  for (const [index, item] of ownerSwitches.entries()) {
    const { action, outgoing, start } = item;
    const browserRequestId = `browser-${salt}-${index}`;
    const requestScope = {
      expectedProject: scope.project,
      generation: scope.generation,
      configuredExplorer: scope.explorer,
      requestProject: scope.project,
      requestExplorer: scope.explorer,
    };
    const failureAction = { id: action.id, label: action.label };
    const timeline = { requestStartedMs: start, failedAtMs: start + 100, durationMs: 100, action: failureAction, mainFrameNavigations: [] };
    const requestDetails = { requestId: null, draftVersion: outgoing.draft.version, draftDigest: outgoing.draft.digest, outputId: outgoing.outputId, stageId: 'source_projection' };
    const capabilityFailure = {
      kind: 'network', playwrightRequestId: `cda-request-${salt}-${index}-cap`, browserRequestId,
      method: 'POST', url: `http://127.0.0.1${route('/authoring/v2/construction-capabilities')}`,
      errorText: 'net::ERR_ABORTED', triggerAction: action.label, failureAction, requestDetails,
      requestScope, requestTimeline: timeline,
    };
    const selectionFailure = {
      kind: 'network', playwrightRequestId: `cda-request-${salt}-${index}-selection`, browserRequestId: null,
      method: 'GET', url: `http://127.0.0.1${route(`/selections/${outgoing.selectionId}`)}`,
      errorText: 'net::ERR_ABORTED', triggerAction: action.label, failureAction,
      requestDetails: { requestId: null, draftVersion: null, draftDigest: null, outputId: null, stageId: null },
      requestScope, requestTimeline: timeline,
    };
    network.push(capabilityFailure, selectionFailure);
    nativeRequests.push({
      requestId: `native-${salt}-${index}`, browserRequestId,
      method: 'POST', path: route('/authoring/v2/construction-capabilities'),
      startedAt: base + start, failure: 'net::ERR_ABORTED',
      body: { outputId: outgoing.outputId, stageId: 'source_projection', ...outgoing.draft, expectedDraftVersion: outgoing.draft.version, expectedDraftDigest: outgoing.draft.digest },
    });
    const successAt = start + 250;
    nativeRequests.push({
      requestId: `success-cap-${salt}-${index}`, method: 'POST',
      path: route('/authoring/v2/construction-capabilities'),
      startedAt: base + successAt, responseReceivedAt: base + successAt + 100,
      completedAt: base + successAt + 100, status: 200,
      triggerAction: action.label,
      body: { outputId: item.next.outputId, stageId: 'source_projection', snapshotToken: item.next.draft.snapshotToken, expectedDraftVersion: item.next.draft.version, expectedDraftDigest: item.next.draft.digest },
    });
    nativeRequests.push({
      requestId: `success-preview-${salt}-${index}`, method: 'POST',
      path: route('/authoring/v2/preview'),
      startedAt: base + successAt + 10, responseReceivedAt: base + successAt + 120,
      completedAt: base + successAt + 120, status: 200,
      triggerAction: action.label,
      body: { outputId: item.next.outputId, receiptId: `receipt-${salt}-${index}` },
    });
    if (index === 1) {
      const commandId = `create-command-${salt}`;
      const rootNodeId = `root-${salt}`;
      nativeRequests.push({
        requestId: `create-target-${salt}`, method: 'POST',
        path: route('/authoring/v2/commands'), triggerAction: action.label,
        startedAt: base + start + 150, responseReceivedAt: base + start + 170,
        completedAt: base + start + 200, status: 200,
        body: {
          commandId, snapshotToken: outgoing.draft.snapshotToken,
          expectedDraftVersion: outgoing.draft.version,
          expectedDraftDigest: outgoing.draft.digest,
          commands: [{ type: 'CREATE_TABLE', rootNodeId }],
        },
        response: {
          commandId, draftVersion: item.next.draft.version, draftDigest: item.next.draft.digest,
          results: [{ type: 'TABLE_CREATED', outputId: item.next.outputId }],
          workspace: { documents: [{
            output: { id: item.next.outputId }, rootResourceType: 'Observation', columns: [], construction: { steps: [] },
          }] },
        },
      });
    }
  }
  const selectionAssertion = owner => ({
    status: 'passed', evidence: {
      project: scope.project, generation: scope.generation,
      outputId: owner.outputId, selectionId: owner.selectionId,
      selectionRefs: [`${scope.project}/${scope.generation}/Observation/row-${owner.outputId}`],
    },
  });
  const assertions = [selectionAssertion(left), selectionAssertion(right), {
    status: 'passed', evidence: {
      outputId: right.outputId, selectionId: right.selectionId,
      expectedRows: [rows[1], rows[2], rows[0]], rows,
    },
  }, {
    status: 'passed',
    name: 'Native Combine creates a scoped empty Observation target for the current-draft Join',
    evidence: {
      project: scope.project, generation: scope.generation,
      snapshotToken: target.draft.snapshotToken,
      targetBinding: {
        ok: true, commandBound: true, resultBound: true, workspaceBound: true, editorBound: true,
        responseStatus: 200, commandId: `create-command-${salt}`,
        rootNodeId: `root-${salt}`, expectedRootNodeIds: [`root-${salt}`],
        outputId: target.outputId, mountedOutputId: target.outputId,
        previousOutputIDs: [left.outputId, right.outputId],
        workspaceTargetCount: 1, targetRootResourceType: 'Observation',
        targetColumnCount: 0, targetConstructionSteps: 0,
      },
    },
  }];
  const report = {
    scenario: 'cda-workspace-combine', caseName: 'nullable-code-join', startedAt,
    scope, target: { kind: 'owned-cda', ...scope, uiUrl: 'http://127.0.0.1' },
    actions, network, nativeRequests, assertions,
  };
  const contexts = ownerSwitches.map(({ action, outgoing, next }) => ({
    scope,
    action: { id: action.id, label: action.label, locator: action.locator },
    outgoing, next,
  }));
  return { report, contexts, network, ownerSwitches };
};

test('validates remapped run-local scope, owner, CAS, request, and receipt IDs', () => {
  for (const salt of ['original', 'remapped']) {
    const fixture = makeFixture(salt);
    const before = structuredClone(fixture.report.network);
    const results = fixture.contexts.map(context => validateNullableJoinSwitch(fixture.report, context));
    assert.deepEqual(results.map(result => result.ok), [true, true], JSON.stringify(results));
    assert.deepEqual(results.map(result => result.evidence.abortCount), [2, 2]);
    assert.deepEqual(fixture.report.network, before, 'validator leaves captured report entries unchanged');
  }
});

test('binds duplicate action labels to the exact failure action ID', () => {
  const fixture = makeFixture('duplicate-action');
  const [context] = fixture.contexts;
  const action = fixture.report.actions[0];
  const report = {
    ...fixture.report,
    actions: [{ ...action, id: 'earlier-same-label', locator: 'wrong-locator' }, ...fixture.report.actions],
  };
  assert.equal(validateNullableJoinSwitch(report, context).ok, true);
  const network = report.network.map((record, index) => index === 0
    ? { ...record, failureAction: { ...record.failureAction, id: 'wrong-action-id' } }
    : record);
  assert.equal(validateNullableJoinSwitch({ ...report, network }, context).ok, false);
});

test('accepts capability-only, selection-only, or paired observed aborts', () => {
  const fixture = makeFixture('owner-cas-negative');
  const [context] = fixture.contexts;
  const [capabilityFailure, selectionFailure] = fixture.network;
  for (const retained of [[capabilityFailure], [selectionFailure], [capabilityFailure, selectionFailure]]) {
    const wanted = new Set(retained);
    const network = fixture.report.network.filter(entry =>
      entry.triggerAction !== context.action.label || wanted.has(entry));
    const report = { ...fixture.report, network };
    const result = validateNullableJoinSwitch(report, context);
    assert.equal(result.ok, true, result.reason);
    assert.equal(result.evidence.abortCount, retained.length);
  }
});

test('accepts zero-abort switches only with exact action, successor response, and concrete proof', () => {
  const fixture = makeFixture('zero-abort');
  for (const context of fixture.contexts) {
    const report = {
      ...fixture.report,
      actions: [{ ...fixture.report.actions.find(entry => entry.id === context.action.id), id: 'earlier-same-label' }, ...fixture.report.actions],
      network: fixture.report.network.filter(entry => entry.failureAction?.id !== context.action.id),
    };
    const result = validateNullableJoinSwitch(report, context);
    assert.equal(result.ok, true, result.reason);
    assert.equal(result.evidence.abortCount, 0);
    assert.equal(result.evidence.actionId, context.action.id);
    assert.equal(validateNullableJoinSwitch(report, {
      ...context, action: { ...context.action, id: 'wrong-action-id' },
    }).ok, false);
  }
  const [visibleContext, targetContext] = fixture.contexts;
  const visibleZeroReport = {
    ...fixture.report,
    network: fixture.report.network.filter(entry => entry.failureAction?.id !== visibleContext.action.id),
  };
  assert.equal(validateNullableJoinSwitch({
    ...visibleZeroReport, target: { ...visibleZeroReport.target, generation: 'other-generation' },
  }, visibleContext).ok, false);
  const withoutNextCapability = fixture.report.nativeRequests.filter(entry =>
    !(entry.path.endsWith('/construction-capabilities') && entry.body?.outputId === visibleContext.next.outputId && entry.status === 200));
  assert.equal(validateNullableJoinSwitch({
    ...fixture.report,
    network: fixture.report.network.filter(entry => entry.failureAction?.id !== visibleContext.action.id),
    nativeRequests: withoutNextCapability,
  }, visibleContext).ok, false);
  const withoutTargetBinding = fixture.report.assertions.filter(assertion =>
    assertion.name !== 'Native Combine creates a scoped empty Observation target for the current-draft Join');
  assert.equal(validateNullableJoinSwitch({
    ...fixture.report,
    network: fixture.report.network.filter(entry => entry.failureAction?.id !== targetContext.action.id),
    assertions: withoutTargetBinding,
  }, targetContext).ok, false);
});

test('rejects legacy empty-target evidence without the current fresh target binding on abort paths', () => {
  const fixture = makeFixture('legacy-empty-target');
  const [, context] = fixture.contexts;
  const targetOutputId = context.next.outputId;
  const emptyDocument = {
    output: { id: targetOutputId },
    columns: [],
    rows: { kind: 'RECORDS', records: {} },
  };
  const assertions = fixture.report.assertions.filter(assertion =>
    assertion.name !== 'Native Combine creates a scoped empty Observation target for the current-draft Join');
  assertions.push({
    status: 'passed',
    name: 'Legacy cancellation left the empty target unchanged',
    evidence: {
      targetOutputId,
      before: structuredClone(emptyDocument),
      after: structuredClone(emptyDocument),
    },
  });

  const result = validateNullableJoinSwitch({ ...fixture.report, assertions }, context);
  assert.equal(result.ok, false, 'a restored empty target does not prove the current CREATE_TABLE result');
});

test('rejects a wrong outgoing owner or native captured draft CAS', () => {
  const fixture = makeFixture('wrong-owner-cas');
  const [context] = fixture.contexts;
  assert.equal(validateNullableJoinSwitch(fixture.report, {
    ...context, outgoing: { ...context.outgoing, outputId: 'wrong-output' },
  }).ok, false);
  const capabilityOnly = { ...fixture.report, network: [fixture.network[0], ...fixture.network.slice(2)] };
  assert.equal(validateNullableJoinSwitch(capabilityOnly, {
    ...context, outgoing: { ...context.outgoing, draft: { ...context.outgoing.draft, digest: 'wrong-digest' } },
  }).ok, false);
  const [capabilityFailure] = fixture.network;
  const nativeRequests = fixture.report.nativeRequests.map(entry => entry.browserRequestId === capabilityFailure.browserRequestId
    ? { ...entry, body: { ...entry.body, expectedDraftDigest: 'wrong-digest' } }
    : entry);
  assert.equal(validateNullableJoinSwitch({ ...fixture.report, nativeRequests }, context).ok, false);
});

test('rejects a missing successor capability or preview', () => {
  const fixture = makeFixture('successor-negative');
  const [context] = fixture.contexts;
  const outputId = context.next.outputId;
  const withoutCapability = fixture.report.nativeRequests.filter(entry =>
    !(entry.path.endsWith('/construction-capabilities') && entry.body?.outputId === outputId && entry.status === 200));
  const withoutPreview = fixture.report.nativeRequests.filter(entry => !entry.path.endsWith('/preview'));
  assert.equal(validateNullableJoinSwitch({ ...fixture.report, nativeRequests: withoutCapability }, context).ok, false);
  assert.equal(validateNullableJoinSwitch({ ...fixture.report, nativeRequests: withoutPreview }, context).ok, false);
});

test('filters failed and stale successor candidates before selecting a later success', () => {
  const fixture = makeFixture('candidate-order');
  const [context] = fixture.contexts;
  const success = fixture.report.nativeRequests.find(entry =>
    entry.path.endsWith('/construction-capabilities')
    && entry.body?.outputId === context.next.outputId
    && entry.status === 200);
  const stale = { ...success, requestId: 'stale-capability', startedAt: Date.parse(fixture.report.startedAt) + 10,
    responseReceivedAt: Date.parse(fixture.report.startedAt) + 20, completedAt: Date.parse(fixture.report.startedAt) + 20 };
  const failed = { ...success, requestId: 'failed-capability', status: null, failure: 'net::ERR_FAILED',
    responseReceivedAt: null, completedAt: null };
  const nativeRequests = [stale, failed, ...fixture.report.nativeRequests];
  const result = validateNullableJoinSwitch({ ...fixture.report, nativeRequests }, context);
  assert.equal(result.ok, true, result.reason);
  assert.equal(result.evidence.aborts[0].capabilityRequestId, success.requestId);
});

test('rejects non-ERR_ABORTED events and scope mismatch', () => {
  const fixture = makeFixture('transport-negative');
  const [context] = fixture.contexts;
  const network = fixture.report.network.map((entry, index) => index === 0
    ? { ...entry, errorText: 'net::ERR_FAILED' }
    : entry);
  assert.equal(validateNullableJoinSwitch({ ...fixture.report, network }, context).ok, false);
  const wrongRequestPrefix = fixture.report.network.map((entry, index) => index === 0
    ? { ...entry, playwrightRequestId: 'foreign-request-1' }
    : entry);
  assert.equal(validateNullableJoinSwitch({ ...fixture.report, network: wrongRequestPrefix }, context).ok, false);
  assert.equal(validateNullableJoinSwitch(fixture.report, {
    ...context, scope: { ...context.scope, explorer: 'wrong-explorer' },
  }).ok, false);
});

test('rejects a successor selection whose scoped references are missing', () => {
  const fixture = makeFixture('next-selection-negative');
  const [context] = fixture.contexts;
  const assertions = fixture.report.assertions.map(assertion =>
    assertion.evidence?.outputId === context.next.outputId && assertion.evidence?.selectionId === context.next.selectionId
      ? { ...assertion, evidence: { ...assertion.evidence, selectionRefs: ['other-project/other-generation/Observation/id'] } }
      : assertion);
  assert.equal(validateNullableJoinSwitch({ ...fixture.report, assertions }, context).ok, false);
});

test('rejects a same-path wrong-owner abort classified by the production CDA cancellation path', () => {
  const fixture = makeFixture('classified-wrong-owner');
  const [context] = fixture.contexts;
  const report = structuredClone(fixture.report);
  const [capabilityFailure] = report.network;
  capabilityFailure.requestDetails.outputId = 'out-unrelated-owner';
  const nativeFailure = report.nativeRequests.find(entry =>
    entry.browserRequestId === capabilityFailure.browserRequestId);
  nativeFailure.body.outputId = 'out-unrelated-owner';

  const cancellation = classifyThroughFixture(report, capabilityFailure, context);
  assert.equal(cancellation.browserRequestId, capabilityFailure.browserRequestId);
  assert.equal(report.expectedCancellations.length, 1);
  assert.equal(report.network[0].expected, true);
  assert.equal(report.network[0].expectedCancellation.browserRequestId, capabilityFailure.browserRequestId);

  const result = validateNullableJoinSwitch(report, context);
  assert.equal(result.ok, false, 'an expected ledger entry must not hide a same-path request owned by another output');
  assert.match(result.reason, /expected cancellation ledger does not bind/);

  const orphaned = structuredClone(report);
  orphaned.network = orphaned.network.filter(entry => entry.playwrightRequestId !== capabilityFailure.playwrightRequestId
    && entry.failureAction?.id !== context.action.id);
  const orphanResult = validateNullableJoinSwitch(orphaned, context);
  assert.equal(orphanResult.ok, false, 'a retained expected entry cannot disappear into the zero-abort path');
  assert.match(orphanResult.reason, /exactly one raw network entry/);

  const wrongCas = structuredClone(report);
  wrongCas.expectedCancellations[0].proof.outgoingDraft.digest = 'wrong-digest';
  assert.equal(validateNullableJoinSwitch(wrongCas, context).ok, false,
    'the scope proof CAS must bind to the expected outgoing draft');

  const duplicate = structuredClone(report);
  duplicate.expectedCancellations.push(structuredClone(duplicate.expectedCancellations[0]));
  const duplicateResult = validateNullableJoinSwitch(duplicate, context);
  assert.equal(duplicateResult.ok, false, 'one raw request cannot be consumed by duplicate expected ledger rows');
  assert.match(duplicateResult.reason, /duplicate raw request identity/);
});

test('reconciles valid production-classified cancellations to exact raw request identities', () => {
  const fixture = makeFixture('classified-valid');
  const [context] = fixture.contexts;
  const report = structuredClone(fixture.report);
  const classified = report.network.filter(record => record.failureAction?.id === context.action.id);
  for (const record of classified) classifyThroughFixture(report, record, context);

  const result = validateNullableJoinSwitch(report, context);
  assert.equal(result.ok, true, result.reason);
  assert.deepEqual(result.evidence.classifiedCancellationIds, classified.map(record => record.playwrightRequestId));
  assert.equal(result.evidence.abortCount, 2);
});

test('preserves a prior same-label ledger entry while selecting the exact current action ID', () => {
  const fixture = makeFixture('same-label-actions');
  const report = structuredClone(fixture.report);
  const [firstContext, secondContext] = fixture.contexts;
  const originalFirstLabel = firstContext.action.label;
  const sharedLabel = secondContext.action.label;
  report.actions.find(entry => entry.id === firstContext.action.id).label = sharedLabel;
  const remappedFirstContext = {
    ...firstContext,
    action: { ...firstContext.action, label: sharedLabel },
  };
  for (const record of report.network) {
    if (record.failureAction?.id !== firstContext.action.id) continue;
    record.failureAction.label = sharedLabel;
    record.triggerAction = sharedLabel;
  }
  for (const request of report.nativeRequests) {
    if (request.triggerAction === originalFirstLabel) request.triggerAction = sharedLabel;
  }

  for (const [context, actionId] of [[remappedFirstContext, firstContext.action.id], [secondContext, secondContext.action.id]]) {
    for (const record of report.network.filter(entry => entry.failureAction?.id === actionId)) {
      classifyThroughFixture(report, record, context);
    }
  }

  assert.equal(report.expectedCancellations.length, 4);
  const firstResult = validateNullableJoinSwitch(report, remappedFirstContext);
  const secondResult = validateNullableJoinSwitch(report, secondContext);
  assert.equal(firstResult.ok, true, firstResult.reason);
  assert.equal(secondResult.ok, true, secondResult.reason);
  assert.deepEqual(firstResult.evidence.classifiedCancellationIds,
    report.expectedCancellations.filter(entry => entry.proof.outgoingOutputId === remappedFirstContext.outgoing.outputId)
      .map(entry => entry.playwrightRequestId));
  assert.deepEqual(secondResult.evidence.classifiedCancellationIds,
    report.expectedCancellations.filter(entry => entry.proof.outgoingOutputId === secondContext.outgoing.outputId)
      .map(entry => entry.playwrightRequestId));
});
