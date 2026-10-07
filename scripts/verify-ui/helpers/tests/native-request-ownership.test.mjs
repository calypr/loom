import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyExpectedOwnedCancellation,
  classifyNativeRequestOwnerRetirement,
  nativeReadRequestMatchesExpectedScope,
} from '../native-request-ownership.mjs';
import { nativeAbortNetworkFailureClock } from '../native-abort-probe.mjs';

const ownedRequest = (endpoint = 'semantic-inventory', overrides = {}) => {
  const ownerByEndpoint = {
    'semantic-inventory': {
      component: 'http://localhost/ui/components/ConceptCatalog.tsx',
      apiFunction: 'browseSemanticInventory',
      prefix: 'feature-catalog-',
      request: { snapshotToken: 'snapshot-1', outputId: 'output-1', rowRoot: 'Patient' },
    },
    'population-routes': {
      component: 'http://localhost/ui/components/PopulationPanel.tsx',
      apiFunction: 'searchPopulationRoutes',
      prefix: 'population-routes-',
      request: { snapshotToken: 'snapshot-1', outputId: 'output-1', selectionRevisionId: 'selection-1' },
    },
    'related-expand-choices': {
      component: 'http://localhost/ui/constructionOperations/RelatedExpandEditor.tsx',
      apiFunction: 'searchRelatedExpandChoices',
      prefix: 'related-expand-choices-',
      request: { snapshotToken: 'snapshot-1', outputId: 'output-1', expectedDraftVersion: 7,
        expectedDraftDigest: 'draft-7', stageId: 'related-stage', anchorColumnId: 'anchor-column',
        targetResourceType: 'Patient', limit: 50 },
    },
    'frame-source-options': {
      component: 'http://localhost/ui/constructionWorkspace/FrameSourcePanel.tsx',
      apiFunction: 'browseFrameSourceOptions',
      prefix: 'frame-source-options-',
      request: { snapshotToken: 'snapshot-1', outputId: 'output-1', limit: 20 },
    },
    'construction-choices': {
      component: 'http://localhost/ui/constructionWorkspace/PairedColumnSuggestions.tsx',
      apiFunction: 'searchConstructionChoices',
      prefix: 'paired-column-choices-',
      request: { snapshotToken: 'snapshot-1', outputId: 'output-1', source: {
        kind: 'SEMANTIC', contextToken: 'context-1', buildId: 'build-1', conceptId: 'concept-1', bindingId: 'binding-1',
      } },
    },
  }[endpoint];
  assert(ownerByEndpoint, `test fixture does not define ${endpoint}`);
  const entry = {
    requestId: '42.1',
    requestCorrelationId: ownerByEndpoint.prefix + 'request',
    method: 'POST',
    path: `/api/v1/projects/p/explorers/e/authoring/v2/${endpoint}`,
    origin: 'http://127.0.0.1:30008',
    scopeProject: 'p',
    scopeExplorer: 'e',
    request: ownerByEndpoint.request,
    resourceType: 'Fetch',
    frameId: 'main-frame',
    loaderId: 'old-loader',
    owningFrameWasCurrentMainLoader: true,
    owningFrameLoaderCommittedAt: 80,
    initiatorExecutionContexts: [{ executionContextId: 8, uniqueId: 'context-8', createdAt: 90 }],
    startedAt: 100,
    status: undefined,
    responseReceivedAt: undefined,
    networkTerminal: false,
    bodyReadStatus: 'pending',
    complete: false,
    initiator: { type: 'script', stack: [
      { url: 'http://localhost/ui/api.ts', functionName: ownerByEndpoint.apiFunction },
      { url: ownerByEndpoint.component, functionName: 'loadPage' },
    ] },
    ...overrides,
  };
  entry.cdpRequestId ??= `cdp-${entry.requestId}`;
  entry.cdpRequestMatchCount ??= 1;
  return entry;
};

const retiredOwnerEvidence = {
  frameNavigations: [{ frameId: 'main-frame', isMainFrame: true, loaderId: 'new-loader', at: 110 }],
  executionContextRetirements: [{ frameId: 'main-frame', isDefault: true, executionContextId: 8,
    uniqueId: 'context-8', createdAt: 90, at: 109, eventType: 'Runtime.executionContextDestroyed' }],
};
const canceledAfterNavigation = {
  networkTerminal: true,
  bodyReadStatus: 'failed',
  complete: false,
  loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, at: 111 },
};

const componentOwnerCancellation = (entry, {
  action = 'coded-to-fields-tab',
  detached = true,
  replacementAnchorId,
  trusted = true,
  abortedAt = 109,
} = {}) => {
  const failureAt = nativeAbortNetworkFailureClock(entry).at;
  const coded = entry.requestCorrelationId.startsWith('paired-column-inventory-');
  const anchorId = 'captured-owner-node';
  const tabGroupId = 'captured-tab-group';
  const dialogId = 'captured-row-dialog';
  const button = coded
    ? action === 'coded-to-fields-tab'
      ? { accessibleLabel: 'Fields and related data', tabGroupId }
      : { accessibleLabel: 'Cancel', testId: 'construction-cancel-proposal', tabGroupId }
    : action === 'row-settings-group-rows'
      ? { accessibleLabel: 'Combine rows into groups', testId: 'construction-action-group-rows', dialogId }
      : action === 'row-settings-back-to-table'
        ? { accessibleLabel: 'Back to table', dialogId }
        : { accessibleLabel: 'Cancel', testId: 'construction-cancel-proposal', dialogId };
  const owner = coded ? 'paired-column-inventory' : 'population-route-options';
  const endpoint = coded ? 'semantic-inventory' : 'population-routes';
  const selector = coded ? '[data-testid="paired-column-suggestions"]' : '[aria-label="Starting collection"]';
  const ownerDomAtFetch = {
    status: 'unique', selector, matchCount: 1, capturedAt: entry.startedAt,
    anchorId, connectedAtFetch: true, ruleOwner: owner,
    retirementAction: coded ? 'coded-to-fields-tab' : 'row-settings-back-to-table',
    ...(coded
      ? { tabGroupId, selectedTabAtFetch: 'Coded values' }
      : { dialogId, dialogConnectedAtFetch: true }),
  };
  const ownerDomAtAbort = {
    anchorId: replacementAnchorId ?? anchorId,
    connectedAtAbort: !detached,
    detachedAtAbort: detached,
    observedAtAbort: abortedAt,
    ...(coded
      ? { tabGroupId, selectedTabAtAbort: 'Fields and related data', tabGroupConnectedAtAbort: true }
      : { dialogId, dialogConnectedAtAbort: !detached }),
  };
  const ownerRequest = {
    requestId: entry.requestCorrelationId,
    path: entry.path,
    method: entry.method,
    endpoint,
    startedAt: entry.startedAt,
    ownerDomAtFetch,
    ownerDomAtAbort,
  };
  const ownerRetirementAction = {
    type: 'click', at: 105, isTrusted: trusted,
    trustEvidence: trusted ? 'native-event-isTrusted-true' : undefined,
    closestButton: button,
  };
  return {
    networkRequestId: entry.cdpRequestId,
    controllerId: 'abort-controller-component-1',
    controllerAbortedAt: abortedAt,
    request: ownerRequest,
    exactRequestSignalCorrelation: true,
    networkFailureObservedSeparately: true,
    networkFailureObservedAt: failureAt,
    networkFailureClockBasis: 'request-wall-time-calibrated-cdp-monotonic',
    signalWasAlreadyAborted: false,
    ownerRetirementAction,
    ownerDomAtFetch,
    ownerDomAtAbort,
    sameDocumentOwnerRetirement: detached && trusted && (coded
      ? action === 'coded-to-fields-tab'
      : ['row-settings-group-rows', 'row-settings-back-to-table'].includes(action)),
  };
};

const relatedExpandOwnerCancellation = (entry, {
  detached = true,
  stageId = entry.request.stageId,
  outputId = entry.request.outputId,
  actionTestId = 'construction-apply-proposal',
} = {}) => {
  const failureAt = nativeAbortNetworkFailureClock(entry).at;
  const anchorId = 'related-expand-editor-node';
  const ownerDomAtFetch = {
    status: 'unique', selector: '[data-testid="construction-related-expand-editor"]', matchCount: 1,
    capturedAt: entry.startedAt, anchorId, connectedAtFetch: true, ruleOwner: 'related-expand-choice-editor',
    retirementAction: 'related-expand-proposal-apply',
    ownerAttributes: { stageId: entry.request.stageId, outputId: entry.request.outputId },
  };
  const ownerDomAtAbort = {
    anchorId, connectedAtAbort: !detached, detachedAtAbort: detached, observedAtAbort: 109,
    ownerAttributes: { stageId, outputId },
  };
  const ownerRetirementAction = {
    type: 'click', at: 105, isTrusted: true, trustEvidence: 'native-event-isTrusted-true',
    closestButton: { accessibleLabel: 'Apply', testId: actionTestId },
  };
  return {
    networkRequestId: entry.cdpRequestId,
    controllerId: 'abort-controller-related-expand-1',
    controllerAbortedAt: 109,
    request: {
      requestId: entry.requestCorrelationId, path: entry.path, method: entry.method,
      endpoint: 'related-expand-choices', startedAt: entry.startedAt, ownerDomAtFetch, ownerDomAtAbort,
    },
    exactRequestSignalCorrelation: true,
    networkFailureObservedSeparately: true,
    networkFailureObservedAt: failureAt,
    networkFailureClockBasis: 'request-wall-time-calibrated-cdp-monotonic',
    signalWasAlreadyAborted: false,
    ownerRetirementAction, ownerDomAtFetch, ownerDomAtAbort,
    sameDocumentOwnerRetirement: detached && stageId === entry.request.stageId &&
      outputId === entry.request.outputId && actionTestId === 'construction-apply-proposal',
  };
};

const componentOwnedRequest = (kind = 'coded', overrides = {}) => {
  const entry = kind === 'coded'
    ? ownedRequest('semantic-inventory', {
        requestTimestamp: 1,
        requestWallTime: 0.1,
        requestCorrelationId: 'paired-column-inventory-request',
        initiator: { type: 'script', stack: [
          { url: 'http://localhost/ui/api.ts', functionName: 'browseSemanticInventory' },
          { url: 'http://localhost/ui/constructionWorkspace/PairedColumnSuggestions.tsx', functionName: '' },
        ] },
        ...overrides,
      })
    : ownedRequest('population-routes', { requestTimestamp: 1, requestWallTime: 0.1, ...overrides });
  entry.networkTerminal = true;
  entry.bodyReadStatus = 'failed';
  entry.complete = false;
  entry.loadingFailed = { errorText: 'net::ERR_ABORTED', canceled: true, timestamp: 1.011, at: 999 };
  return entry;
};

test('active loader with pending catalog request remains unresolved', () => {
  assert.deepEqual(classifyNativeRequestOwnerRetirement(ownedRequest()),
    { ownerRetired: false, reason: 'owning-loader-still-active' });
});

test('ConceptCatalog request body without outputId matches its authored semantic-inventory contract', () => {
  const entry = ownedRequest('semantic-inventory', {
    request: { snapshotToken: 'snapshot-1', rowRoot: 'Patient', limit: 50 },
  });
  const result = classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence);
  assert.equal(result.ownerRetired, true);
  assert.equal(result.owner, 'feature-catalog');
  assert.equal(result.endpoint, 'semantic-inventory');
});

test('saved native 25074.671 evidence is recognized from its exact current loader and cleared context', () => {
  const frameId = 'A2D1DC7F77B030FB51EE21568DD6885C';
  const entry = {
    requestId: '25074.671',
    requestCorrelationId: 'feature-catalog-f0196055-09a2-4692-90ee-7cfa9cc3b309',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_c89a69d7e137/explorers/related-one-all-1791114555031/authoring/v2/semantic-inventory',
    scopeProject: 'loom_dev_c89a69d7e137',
    scopeExplorer: 'related-one-all-1791114555031',
    request: { snapshotToken: 'sha256:85941f8e17cef7b97306a98f0436907b9608b4979e8439bc1fa240cf07d5af93', rowRoot: 'Patient', limit: 50 },
    resourceType: 'Fetch',
    frameId,
    loaderId: '06A71C109086329DBF116B6AD4A87D40',
    owningFrameWasCurrentMainLoader: true,
    owningFrameLoaderCommittedAt: 1791114583990,
    initiatorExecutionContexts: [{ executionContextId: 5,
      uniqueId: '6272126605925005074.-7705095776758380848', createdAt: 1791114583994 }],
    startedAt: 1791114586817,
    networkTerminal: false,
    bodyReadStatus: 'pending',
    complete: false,
    initiator: { type: 'script', stack: [
      { functionName: 'request', url: 'http://127.0.0.1:30008/@fs/workspace/packages/loom-ui/src/api.ts?t=1791107552072' },
      { functionName: 'browseSemanticInventory', url: 'http://127.0.0.1:30008/@fs/workspace/packages/loom-ui/src/api.ts?t=1791107552072' },
      { functionName: '', url: 'http://127.0.0.1:30008/@fs/workspace/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx?t=1791107552076' },
    ] },
  };
  const evidence = {
    frameNavigations: [{ frameId, loaderId: '602E7BF1BEBF8AD322633D49C929EF16', isMainFrame: true, at: 1791114586900 }],
    executionContextRetirements: [{ frameId, executionContextId: 5,
      uniqueId: '6272126605925005074.-7705095776758380848', createdAt: 1791114583994,
      isDefault: true, eventType: 'Runtime.executionContextsCleared', at: 1791114586897 }],
  };
  const result = classifyNativeRequestOwnerRetirement(entry, evidence);
  assert.equal(result.ownerRetired, true);
  assert.equal(result.owner, 'feature-catalog');
  assert.equal(result.reason, 'same-frame-main-loader-navigated-and-captured-default-context-retired');
  assert.equal(entry.networkTerminal, false);
  assert.equal(entry.bodyReadStatus, 'pending');
  assert.equal(entry.complete, false);
});

test('semantic-inventory without its required snapshot or row root remains unknown', () => {
  for (const request of [
    { rowRoot: 'Patient', limit: 50 },
    { snapshotToken: 'snapshot-1', limit: 50 },
    { snapshotToken: 'snapshot-1', rowRoot: 'Patient', outputId: 7 },
  ]) {
    const entry = ownedRequest('semantic-inventory', { request });
    assert.equal(classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence).ownerRetired, false);
  }
});

test('captured related-read scope checks output, stage, and draft CAS against independent UI state', () => {
  const entry = ownedRequest('related-expand-choices');
  const expected = {
    origin: 'http://127.0.0.1:30008',
    project: 'p', explorer: 'e', outputId: 'output-1', snapshotToken: 'snapshot-1',
    expectedDraftVersion: 7, expectedDraftDigest: 'draft-7', stageId: 'related-stage',
    anchorColumnId: 'anchor-column', targetResourceType: 'Patient',
  };
  assert.equal(nativeReadRequestMatchesExpectedScope(entry, expected), true);
  for (const mismatch of [
    { outputId: 'other-output' },
    { snapshotToken: 'other-snapshot' },
    { expectedDraftVersion: 8 },
    { expectedDraftDigest: 'other-digest' },
    { stageId: 'other-stage' },
    { anchorColumnId: 'other-anchor' },
    { targetResourceType: 'Observation' },
    { project: 'other-project' },
    { explorer: 'other-explorer' },
    { origin: 'http://127.0.0.1:30009' },
  ]) {
    assert.equal(nativeReadRequestMatchesExpectedScope(entry, { ...expected, ...mismatch }), false,
      `must reject ${JSON.stringify(mismatch)}`);
  }
  assert.equal(nativeReadRequestMatchesExpectedScope({ ...entry, request: { ...entry.request, stageId: 'other-stage' } }, expected), false);
  assert.equal(nativeReadRequestMatchesExpectedScope({ ...entry, request: { ...entry.request, expectedDraftVersion: '7' } }, expected), false);
  const { origin: _origin, ...missingOrigin } = expected;
  assert.equal(nativeReadRequestMatchesExpectedScope(entry, missingOrigin), false, 'independent UI origin is required');
});

test('population-route cancellation must bind the exact selected source revision', () => {
  const entry = ownedRequest('population-routes');
  const expected = {
    origin: 'http://127.0.0.1:30008',
    project: 'p', explorer: 'e', outputId: 'output-1', snapshotToken: 'snapshot-1',
    selectionRevisionId: 'selection-1',
  };
  assert.equal(nativeReadRequestMatchesExpectedScope(entry, expected), true);
  assert.equal(nativeReadRequestMatchesExpectedScope(entry, { ...expected, selectionRevisionId: 'selection-2' }), false);
  assert.equal(nativeReadRequestMatchesExpectedScope({
    ...entry, request: { ...entry.request, selectionRevisionId: 'selection-2' },
  }, expected), false);
  assert.equal(nativeReadRequestMatchesExpectedScope({
    ...entry, request: { ...entry.request, outputId: 'output-2' },
  }, expected), false);
});

test('construction-choice scope requires the independent complete semantic binding and decoded path identity', () => {
  const entry = ownedRequest('construction-choices', {
    path: '/api/v1/projects/p%2F1/explorers/e%20two/authoring/v2/construction-choices',
    scopeProject: 'p/1',
    scopeExplorer: 'e two',
  });
  const expected = {
    origin: 'http://127.0.0.1:30008',
    project: 'p/1', explorer: 'e two', outputId: 'output-1', snapshotToken: 'snapshot-1',
    source: { kind: 'SEMANTIC', contextToken: 'context-1', buildId: 'build-1', conceptId: 'concept-1', bindingId: 'binding-1' },
  };
  assert.equal(nativeReadRequestMatchesExpectedScope(entry, expected), true);
  assert.equal(nativeReadRequestMatchesExpectedScope(entry, { ...expected, source: undefined }), false,
    'missing UI semantic binding is not accepted');
  for (const key of ['kind', 'contextToken', 'buildId', 'conceptId', 'bindingId']) {
    const incompleteSource = { ...expected.source };
    delete incompleteSource[key];
    assert.equal(nativeReadRequestMatchesExpectedScope(entry, { ...expected, source: incompleteSource }), false,
      `missing UI source ${key} is not accepted`);
  }
  assert.equal(nativeReadRequestMatchesExpectedScope({ ...entry, origin: 'http://127.0.0.1:30009' }, expected), false,
    'request origin must match the active UI origin');
  assert.equal(nativeReadRequestMatchesExpectedScope({ ...entry, path: '/api/v1/projects/p%ZZ/explorers/e/authoring/v2/construction-choices' }, expected), false,
    'malformed encoded scope is not accepted');
});

test('related-expand cancellation needs matching body CAS and the detached same editor after trusted Apply', () => {
  const entry = ownedRequest('related-expand-choices', {
    requestId: 'cdp-related-expand-1',
    requestTimestamp: 1,
    requestWallTime: 0.1,
    startedAt: 100,
    networkTerminal: true,
    bodyReadStatus: 'failed',
    loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, timestamp: 1.012 },
  });
  const expected = {
    origin: 'http://127.0.0.1:30008',
    project: 'p', explorer: 'e', outputId: 'output-1', snapshotToken: 'snapshot-1',
    expectedDraftVersion: 7, expectedDraftDigest: 'draft-7', stageId: 'related-stage',
    anchorColumnId: 'anchor-column', targetResourceType: 'Patient',
  };
  const noNavigation = { frameNavigations: [], executionContextRetirements: [] };
  const decide = (candidate, proof = relatedExpandOwnerCancellation(candidate)) => {
    if (!nativeReadRequestMatchesExpectedScope(candidate, expected)) {
      return { expected: false, reason: 'request-scope-or-CAS-mismatch' };
    }
    candidate.abortControllerProbeEvidence = [proof];
    return classifyExpectedOwnedCancellation(candidate, noNavigation);
  };

  const accepted = decide(entry);
  assert.equal(accepted.expected, true, JSON.stringify(accepted));
  assert.equal(accepted.ownerRetirement.owner, 'related-expand-choice-editor');
  assert.equal(accepted.ownerRetirement.componentRetirementAction, 'related-expand-proposal-apply');

  for (const mismatch of [
    { outputId: 'other-output' },
    { stageId: 'other-stage' },
    { expectedDraftVersion: 8 },
    { expectedDraftDigest: 'other-digest' },
  ]) {
    assert.equal(decide({ ...entry, request: { ...entry.request, ...mismatch } }).expected, false,
      `must reject ${JSON.stringify(mismatch)}`);
  }
  assert.equal(decide(entry, relatedExpandOwnerCancellation(entry, { actionTestId: 'construction-cancel-proposal' })).expected,
    false, 'wrong action');
  assert.equal(decide(entry, relatedExpandOwnerCancellation(entry, { detached: false })).expected,
    false, 'captured owner remains mounted');
  assert.equal(decide(entry, relatedExpandOwnerCancellation(entry, { stageId: 'replacement-stage' })).expected,
    false, 'same node changed stage identity before detaching');
});

test('navigation without a recognized request owner remains unresolved', () => {
  const entry = ownedRequest('semantic-inventory', { requestCorrelationId: 'unrecognized-id' });
  assert.deepEqual(classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence),
    { ownerRetired: false, reason: 'unknown-background-owner-or-scope' });
});

test('unknown frame-loader ownership remains unresolved even when navigation follows', () => {
  const entry = ownedRequest('semantic-inventory', { owningFrameWasCurrentMainLoader: false });
  assert.deepEqual(classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence),
    { ownerRetired: false, reason: 'request-loader-not-proven-current-main-frame' });
});

test('a request context with a reused numeric ID but different unique identity remains unresolved', () => {
  const evidence = {
    ...retiredOwnerEvidence,
    executionContextRetirements: [{ frameId: 'main-frame', isDefault: true, executionContextId: 8,
      uniqueId: 'other-context', createdAt: 90, at: 109, eventType: 'Runtime.executionContextDestroyed' }],
  };
  assert.deepEqual(classifyNativeRequestOwnerRetirement(ownedRequest(), evidence),
    { ownerRetired: false, reason: 'default-execution-context-still-active' });
});

test('a context clear retires only a context captured for the request frame', () => {
  const evidence = {
    ...retiredOwnerEvidence,
    executionContextRetirements: [{ frameId: 'main-frame', isDefault: true, executionContextId: 8,
      uniqueId: 'context-8', createdAt: 90, at: 109, eventType: 'Runtime.executionContextsCleared' }],
  };
  const entry = ownedRequest();
  const before = structuredClone(entry);
  const result = classifyNativeRequestOwnerRetirement(entry, evidence);
  assert.equal(result.ownerRetired, true);
  assert.equal(result.executionContextRetirementEvent, 'Runtime.executionContextsCleared');
  assert.deepEqual(entry, before);
  assert.equal(entry.networkTerminal, false);
  assert.equal(entry.bodyReadStatus, 'pending');
  assert.equal(entry.complete, false);
});

test('a loader commit after request start cannot establish request ownership', () => {
  const entry = ownedRequest('semantic-inventory', { owningFrameLoaderCommittedAt: 101 });
  assert.deepEqual(classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence),
    { ownerRetired: false, reason: 'request-loader-not-proven-current-main-frame' });
});

test('an execution context created after request start cannot prove owner retirement', () => {
  const evidence = {
    ...retiredOwnerEvidence,
    executionContextRetirements: [{ frameId: 'main-frame', isDefault: true, executionContextId: 8,
      uniqueId: 'context-8', createdAt: 105, at: 109, eventType: 'Runtime.executionContextDestroyed' }],
  };
  assert.deepEqual(classifyNativeRequestOwnerRetirement(ownedRequest(), evidence),
    { ownerRetired: false, reason: 'default-execution-context-still-active' });
});

test('same-frame navigation without destruction of the captured request context remains unresolved', () => {
  const evidence = { ...retiredOwnerEvidence, executionContextRetirements: [] };
  assert.deepEqual(classifyNativeRequestOwnerRetirement(ownedRequest(), evidence),
    { ownerRetired: false, reason: 'default-execution-context-still-active' });
});

test('critical construction, Apply, and preview requests are never owner-retirement exceptions', () => {
  for (const endpoint of ['construction-proposals', 'commands', 'preview']) {
    const entry = ownedRequest('semantic-inventory', { path: `/api/v1/projects/p/explorers/e/authoring/v2/${endpoint}` });
    assert.deepEqual(classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence),
      { ownerRetired: false, reason: 'unknown-background-owner-or-scope' });
  }
});

test('retired pending background read keeps pending network state distinct from retirement', () => {
  const entry = ownedRequest();
  const before = structuredClone(entry);
  const result = classifyNativeRequestOwnerRetirement(entry, retiredOwnerEvidence);
  assert.equal(result.ownerRetired, true);
  assert.equal(entry.networkTerminal, false);
  assert.equal(entry.bodyReadStatus, 'pending');
  assert.equal(entry.complete, false);
  assert.deepEqual(entry, before);
});

test('explicit canceled background request is diagnostic, never rewritten as completed', () => {
  const entry = ownedRequest('population-routes', canceledAfterNavigation);
  const before = structuredClone(entry);
  const result = classifyExpectedOwnedCancellation(entry, retiredOwnerEvidence);
  assert.equal(result.expected, true);
  assert.equal(result.ownerRetirement.owner, 'population-route-options');
  assert.equal(result.networkCompleted, false);
  assert.equal(entry.networkTerminal, true);
  assert.equal(entry.bodyReadStatus, 'failed');
  assert.equal(entry.complete, false);
  assert.deepEqual(entry, before);
});

test('page-retired route, frame-source, and choice reads need their exact owner and request identity', () => {
  for (const [endpoint, expectedOwner] of [
    ['frame-source-options', 'frame-source-options'],
    ['construction-choices', 'paired-column-choice-suggestions'],
  ]) {
    const entry = ownedRequest(endpoint, canceledAfterNavigation);
    const result = classifyExpectedOwnedCancellation(entry, retiredOwnerEvidence);
    assert.equal(result.expected, true);
    assert.equal(result.ownerRetirement.owner, expectedOwner);
  }
});

test('same-document coded-owner cancellation requires the exact signal and trusted Coded to Fields action', () => {
  const entry = componentOwnedRequest('coded');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry)];
  const result = classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] });
  assert.equal(result.expected, true);
  assert.equal(result.ownerRetirement.owner, 'paired-column-inventory');
  assert.equal(result.ownerRetirement.componentRetirementAction, 'coded-to-fields-tab');
  assert.equal(result.ownerRetirement.networkCompleted, false);
  assert.equal(entry.networkTerminal, true);
  assert.equal(entry.bodyReadStatus, 'failed');
  assert.equal(entry.complete, false);
});

test('same-document Starting collection cancellation accepts the observed Group action that closes its dialog', () => {
  const entry = componentOwnedRequest('population');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry, { action: 'row-settings-group-rows' })];
  const result = classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] });
  assert.equal(result.expected, true, JSON.stringify({ result, evidence: entry.abortControllerProbeEvidence[0] }));
  assert.equal(result.ownerRetirement.owner, 'population-route-options');
  assert.equal(result.ownerRetirement.trustedActionTestId, 'construction-action-group-rows');
  assert.equal(result.ownerRetirement.reason, 'same-document-population-owner-detached-after-trusted-row-dialog-exit');
  assert.equal(result.networkCompleted, false);
  assert.equal(entry.networkTerminal, true);
  assert.equal(entry.bodyReadStatus, 'failed');
  assert.equal(entry.complete, false);
});

test('same-document Starting collection cancellation also recognizes the exact Back to table exit', () => {
  const entry = componentOwnedRequest('population');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry, { action: 'row-settings-back-to-table' })];
  const result = classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] });
  assert.equal(result.expected, true, JSON.stringify(result));
});

test('a later mainframe retirement cannot mask component evidence ordered before network failure', () => {
  const entry = componentOwnedRequest('coded');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry)];
  const laterNavigation = {
    frameNavigations: [{ frameId: 'main-frame', isMainFrame: true, loaderId: 'new-loader', at: 120 }],
    executionContextRetirements: [{ frameId: 'main-frame', isDefault: true, executionContextId: 8,
      uniqueId: 'context-8', createdAt: 90, at: 119, eventType: 'Runtime.executionContextDestroyed' }],
  };
  const result = classifyExpectedOwnedCancellation(entry, laterNavigation);
  assert.equal(result.expected, true);
  assert.equal(result.ownerRetirement.componentAbortAt, 109);
  assert.equal(result.ownerRetirement.networkCompleted, false);

  const withoutComponentEvidence = { ...entry, abortControllerProbeEvidence: [] };
  assert.deepEqual(classifyExpectedOwnedCancellation(withoutComponentEvidence, laterNavigation), {
    expected: false,
    reason: 'cancellation-precedes-authoritative-owner-retirement',
  });
});

test('same-document component cancellations fail closed on attached/replaced anchors, wrong actions, and synthetic events', () => {
  const cases = [
    { name: 'owner remains attached', options: { detached: false } },
    { name: 'selector replacement differs from captured node', options: { replacementAnchorId: 'new-owner-node' } },
    { name: 'wrong action', options: { action: 'wrong-action' } },
    { name: 'synthetic click', options: { trusted: false } },
  ];
  for (const { name, options } of cases) {
    const entry = componentOwnedRequest(options.action?.startsWith('row-settings') ? 'population' : 'coded');
    entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry, options)];
    assert.equal(
      classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] }).expected,
      false,
      name,
    );
  }
});

test('same-document component cancellation requires exact scope, request shape, and signal correlation', () => {
  const entry = componentOwnedRequest('coded');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry)];
  const noEvidence = { frameNavigations: [], executionContextRetirements: [] };
  assert.equal(classifyExpectedOwnedCancellation({ ...entry, scopeExplorer: 'other' }, noEvidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    request: { rowRoot: 'Patient' },
  }, noEvidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    abortControllerProbeEvidence: [{ ...entry.abortControllerProbeEvidence[0], exactRequestSignalCorrelation: false }],
  }, noEvidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    abortControllerProbeEvidence: [{ ...entry.abortControllerProbeEvidence[0], networkRequestId: 'different-cdp-request' }],
  }, noEvidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    abortControllerProbeEvidence: [{ ...entry.abortControllerProbeEvidence[0], signalWasAlreadyAborted: true }],
  }, noEvidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    requestCorrelationId: 'unrecognized-request-prefix',
  }, noEvidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    path: entry.path.replace('/explorers/e/', '/explorers/other/'),
  }, noEvidence).expected, false);
});

test('component cancellation that precedes abort/failure or has an HTTP response remains fatal', () => {
  const entry = componentOwnedRequest('coded');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry)];
  const evidence = { frameNavigations: [], executionContextRetirements: [] };
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    loadingFailed: { ...entry.loadingFailed, timestamp: 1.008, at: 999 },
  }, evidence).expected, false);
  assert.equal(classifyExpectedOwnedCancellation({
    ...entry,
    status: 503,
    responseReceivedAt: 108,
  }, evidence).expected, false);
});

test('v5 ConceptCatalog 200 headers followed by body abort stays fatal while its captured owner remains attached', () => {
  const correlationId = 'feature-catalog-fdeee773-3bcb-483c-b0d0-b0ffa9174ecc';
  const entry = ownedRequest('semantic-inventory', {
    requestId: '78821.407',
    requestCorrelationId: correlationId,
    startedAt: 1_791_125_730_952,
    requestTimestamp: 1_958_135.144659,
    requestWallTime: 1_791_125_730.952425,
    status: 200,
    responseReceivedAt: 1_791_125_731_132,
    networkTerminal: true,
    bodyReadStatus: 'failed',
    loadingFailed: {
      timestamp: 1_958_135.331166,
      requestId: '78821.407',
      errorText: 'net::ERR_ABORTED',
      canceled: true,
      at: 1_791_125_731_140,
    },
  });
  const ownerDomAtFetch = {
    status: 'unique', selector: '#feature-catalog-search', matchCount: 1,
    anchorId: 'dom-node-1', connectedAtFetch: true, ruleOwner: 'feature-catalog',
    capturedAt: 1_791_125_730_952,
  };
  entry.abortControllerProbeEvidence = [{
    networkRequestId: entry.cdpRequestId,
    controllerId: 'abort-controller-21',
    controllerAbortedAt: 1_791_125_731_124,
    request: {
      requestId: correlationId,
      path: entry.path,
      method: 'POST',
      endpoint: 'semantic-inventory',
      startedAt: 1_791_125_730_952,
      ownerDomAtFetch,
      ownerDomAtAbort: {
        anchorId: 'dom-node-1', connectedAtAbort: true, detachedAtAbort: false,
        observedAtAbort: 1_791_125_731_124,
      },
    },
    ownerDomAtFetch,
    ownerDomAtAbort: {
      anchorId: 'dom-node-1', connectedAtAbort: true, detachedAtAbort: false,
      observedAtAbort: 1_791_125_731_124,
    },
    lastTrustedInteraction: {
      type: 'click', at: 1_791_125_730_747, isTrusted: true,
      closestButton: { accessibleLabel: 'Apply member removal' },
    },
    exactRequestSignalCorrelation: true,
    networkFailureObservedSeparately: true,
    signalWasAlreadyAborted: false,
    sameDocumentOwnerRetirement: false,
  }];

  const result = classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] });
  assert.deepEqual(result, { expected: false, reason: 'http-response-started-before-failure' });
});

test('same-document cancellation rejects host callback time without the exact CDP wall/monotonic pair', () => {
  const entry = componentOwnedRequest('coded');
  entry.abortControllerProbeEvidence = [componentOwnerCancellation(entry)];
  const noNavigation = { frameNavigations: [], executionContextRetirements: [] };
  for (const clockless of [
    { ...entry, requestTimestamp: undefined },
    { ...entry, requestWallTime: 0 },
    { ...entry, loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, at: 111 } },
    { ...entry, loadingFailed: { ...entry.loadingFailed, timestamp: 1.009 } },
  ]) {
    assert.equal(classifyExpectedOwnedCancellation(clockless, noNavigation).expected, false);
  }
});

test('a current owner cancellation, unknown owner, or wrong request prefix stays fatal', () => {
  const activeOwner = { frameNavigations: [], executionContextRetirements: [] };
  assert.equal(classifyExpectedOwnedCancellation(ownedRequest('population-routes', canceledAfterNavigation), activeOwner).expected, false);
  assert.equal(classifyExpectedOwnedCancellation(ownedRequest('population-routes', {
    ...canceledAfterNavigation, requestCorrelationId: 'unrecognized-id',
  }), retiredOwnerEvidence).expected, false);
});

test('HTTP errors and cancellations before owner retirement are never accepted', () => {
  const httpError = ownedRequest('population-routes', {
    ...canceledAfterNavigation,
    status: 503,
    responseReceivedAt: 108,
    loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, at: 111 },
  });
  assert.equal(classifyExpectedOwnedCancellation(httpError, retiredOwnerEvidence).expected, false);

  const earlyCancellation = ownedRequest('population-routes', {
    ...canceledAfterNavigation,
    loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, at: 108 },
  });
  assert.deepEqual(classifyExpectedOwnedCancellation(earlyCancellation, retiredOwnerEvidence), {
    expected: false,
    reason: 'cancellation-precedes-authoritative-owner-retirement',
  });
});

test('retired critical proposal, Apply, or preview cancellation remains unexpected', () => {
  for (const endpoint of ['construction-proposals', 'commands', 'preview']) {
    const entry = ownedRequest('population-routes', {
      ...canceledAfterNavigation,
      path: `/api/v1/projects/p/explorers/e/authoring/v2/${endpoint}`,
    });
    assert.equal(classifyExpectedOwnedCancellation(entry, retiredOwnerEvidence).expected, false);
  }
});

 test('Starting collection cancellation recognizes Related rows only inside the retired owning dialog', () => {
  const entry = componentOwnedRequest('population');
  const evidence = componentOwnerCancellation(entry, { action: 'row-settings-group-rows' });
  evidence.ownerRetirementAction.closestButton.testId = 'construction-action-related-rows';
  evidence.ownerRetirementAction.closestButton.accessibleLabel = 'Make a row for each related record';
  entry.abortControllerProbeEvidence = [evidence];
  assert.equal(classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] }).expected, true);
  evidence.ownerRetirementAction.closestButton.dialogId = 'different-dialog';
  assert.equal(classifyExpectedOwnedCancellation(entry, { frameNavigations: [], executionContextRetirements: [] }).expected, false);
});
