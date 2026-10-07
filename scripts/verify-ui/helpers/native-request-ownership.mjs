import { nativeAbortNetworkFailureClock, nativeAbortOwnerRetirementActionFor } from './native-abort-probe.mjs';

const ownerRules = [
  {
    endpoint: 'semantic-inventory',
    component: '/components/ConceptCatalog.tsx',
    apiFunction: 'browseSemanticInventory',
    requestIdPrefix: 'feature-catalog-',
    owner: 'feature-catalog',
  },
  {
    endpoint: 'semantic-inventory',
    component: '/constructionWorkspace/FrameSourcePanel.tsx',
    apiFunction: 'browseSemanticInventory',
    requestIdPrefix: 'frame-categories-',
    owner: 'frame-category-catalog',
  },
  {
    endpoint: 'semantic-inventory',
    component: '/constructionWorkspace/PairedColumnSuggestions.tsx',
    apiFunction: 'browseSemanticInventory',
    requestIdPrefix: 'paired-column-inventory-',
    owner: 'paired-column-inventory',
  },
  {
    endpoint: 'population-routes',
    component: '/components/PopulationPanel.tsx',
    apiFunction: 'searchPopulationRoutes',
    requestIdPrefix: 'population-routes-',
    owner: 'population-route-options',
  },
  {
    endpoint: 'related-expand-choices',
    component: '/constructionOperations/RelatedExpandEditor.tsx',
    apiFunction: 'searchRelatedExpandChoices',
    requestIdPrefix: 'related-expand-choices-',
    owner: 'related-expand-choice-editor',
  },
  {
    endpoint: 'frame-source-options',
    component: '/constructionWorkspace/FrameSourcePanel.tsx',
    apiFunction: 'browseFrameSourceOptions',
    requestIdPrefix: 'frame-source-options-',
    owner: 'frame-source-options',
  },
  {
    endpoint: 'construction-choices',
    component: '/constructionWorkspace/PairedColumnSuggestions.tsx',
    apiFunction: 'searchConstructionChoices',
    requestIdPrefix: 'paired-column-choices-',
    owner: 'paired-column-choice-suggestions',
  },
  {
    endpoint: 'construction-choices',
    component: '/components/ConceptCatalog.tsx',
    apiFunction: 'searchConstructionChoices',
    requestIdPrefix: 'construction-choices-',
    owner: 'catalog-choice-search',
  },
];

const hasStackSource = (entry, suffix) => (entry.initiator?.stack ?? [])
  .some((frame) => typeof frame.url === 'string' && frame.url.includes(suffix));
const hasStackFunction = (entry, functionName) => (entry.initiator?.stack ?? [])
  .some((frame) => frame.functionName === functionName);

const constructionChoiceSourceKeys = (source) => {
  if (source?.kind === 'FIELD') return ['kind', 'candidateId'];
  if (source?.kind === 'SEMANTIC') return ['kind', 'contextToken', 'buildId', 'conceptId', 'bindingId'];
  return undefined;
};

const hasExactConstructionChoiceSourceShape = (source) => {
  const keys = constructionChoiceSourceKeys(source);
  return Boolean(keys && Object.keys(source).length === keys.length &&
    keys.every((key) => typeof source[key] === 'string' && source[key].length > 0));
};

const matchesConstructionChoiceSource = (actual, expected) => {
  const keys = constructionChoiceSourceKeys(expected);
  return hasExactConstructionChoiceSourceShape(actual) && hasExactConstructionChoiceSourceShape(expected) &&
    keys.every((key) => actual[key] === expected[key]);
};

const requestHasRequiredShape = (entry, endpoint) => {
  const request = entry.request;
  if (!request || typeof request.snapshotToken !== 'string') return false;
  if (endpoint === 'semantic-inventory') {
    return typeof request.rowRoot === 'string' &&
      (request.outputId === undefined || typeof request.outputId === 'string');
  }
  if (typeof request.outputId !== 'string') return false;
  if (endpoint === 'population-routes') return typeof request.selectionRevisionId === 'string';
  if (endpoint === 'related-expand-choices') {
    return Number.isSafeInteger(request.expectedDraftVersion) && request.expectedDraftVersion >= 0 &&
      typeof request.expectedDraftDigest === 'string' && request.expectedDraftDigest.length > 0 &&
      typeof request.stageId === 'string' && request.stageId.length > 0 &&
      typeof request.anchorColumnId === 'string' && request.anchorColumnId.length > 0 &&
      typeof request.targetResourceType === 'string' && request.targetResourceType.length > 0;
  }
  if (endpoint === 'construction-choices') {
    return hasExactConstructionChoiceSourceShape(request.source);
  }
  return endpoint === 'frame-source-options';
};

/** Match a captured background read against the UI's independent owner/CAS state at request start. */
export const nativeReadRequestMatchesExpectedScope = (entry, expected) => {
  if (!entry || !expected || typeof entry.path !== 'string' || entry.method !== 'POST' ||
      typeof expected.project !== 'string' || typeof expected.explorer !== 'string' ||
      typeof expected.origin !== 'string' || typeof entry.origin !== 'string' ||
      typeof expected.outputId !== 'string' || typeof expected.snapshotToken !== 'string') return false;
  try {
    if (new URL(expected.origin).origin !== expected.origin || entry.origin !== expected.origin) return false;
  } catch {
    return false;
  }
  const match = /^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)\/authoring\/v2\/([^/]+)$/.exec(entry.path);
  if (!match) return false;
  let project;
  let explorer;
  try {
    project = decodeURIComponent(match[1]);
    explorer = decodeURIComponent(match[2]);
  } catch {
    return false;
  }
  if (project !== expected.project || explorer !== expected.explorer ||
      entry.scopeProject !== project || entry.scopeExplorer !== explorer ||
      entry.request?.outputId !== expected.outputId || entry.request?.snapshotToken !== expected.snapshotToken) return false;

  const endpoint = match[3];
  if (endpoint === 'population-routes') {
    return typeof expected.selectionRevisionId === 'string' && expected.selectionRevisionId.length > 0 &&
      entry.request.selectionRevisionId === expected.selectionRevisionId;
  }
  if (endpoint === 'related-expand-choices') {
    return Number.isSafeInteger(expected.expectedDraftVersion) && expected.expectedDraftVersion >= 0 &&
      typeof expected.expectedDraftDigest === 'string' && expected.expectedDraftDigest.length > 0 &&
      typeof expected.stageId === 'string' && expected.stageId.length > 0 &&
      typeof expected.anchorColumnId === 'string' && expected.anchorColumnId.length > 0 &&
      typeof expected.targetResourceType === 'string' && expected.targetResourceType.length > 0 &&
      entry.request.expectedDraftVersion === expected.expectedDraftVersion &&
      entry.request.expectedDraftDigest === expected.expectedDraftDigest &&
      entry.request.stageId === expected.stageId &&
      entry.request.anchorColumnId === expected.anchorColumnId &&
      entry.request.targetResourceType === expected.targetResourceType;
  }
  if (endpoint === 'construction-choices') {
    const expectedSources = Array.isArray(expected.sources)
      ? expected.sources
      : expected.source ? [expected.source] : [];
    return expectedSources.length > 0 && expectedSources.every(hasExactConstructionChoiceSourceShape) &&
      expectedSources.some((source) => matchesConstructionChoiceSource(entry.request.source, source));
  }
  return endpoint === 'frame-source-options';
};

const findOwnerRule = (entry) => {
  const match = /^\/api\/v1\/projects\/([^/]+)\/explorers\/([^/]+)\/authoring\/v2\/([^/]+)$/.exec(entry.path ?? '');
  if (!match || entry.method !== 'POST' || entry.resourceType !== 'Fetch') return undefined;
  const [, pathProject, pathExplorer, endpoint] = match;
  if (entry.scopeProject !== pathProject || entry.scopeExplorer !== pathExplorer) return undefined;
  return ownerRules.find((rule) => rule.endpoint === endpoint &&
    entry.requestCorrelationId?.startsWith(rule.requestIdPrefix) === true &&
    hasStackSource(entry, rule.component) && hasStackFunction(entry, rule.apiFunction) &&
    requestHasRequiredShape(entry, endpoint));
};

const proveOwnerRetired = (entry, { frameNavigations = [], executionContextRetirements = [] } = {}) => {
  const rule = findOwnerRule(entry);
  if (!rule) return { ownerRetired: false, reason: 'unknown-background-owner-or-scope' };
  if (typeof entry.frameId !== 'string' || typeof entry.loaderId !== 'string') {
    return { ownerRetired: false, reason: 'missing-frame-or-loader-identity' };
  }
  if (entry.owningFrameWasCurrentMainLoader !== true ||
      !Number.isFinite(entry.owningFrameLoaderCommittedAt) || entry.owningFrameLoaderCommittedAt > entry.startedAt) {
    return { ownerRetired: false, reason: 'request-loader-not-proven-current-main-frame' };
  }
  const ownerContexts = new Map((entry.initiatorExecutionContexts ?? [])
    .filter((context) => context && Number.isInteger(context.executionContextId))
    .map((context) => [context.executionContextId, context]));
  if (ownerContexts.size === 0) {
    return { ownerRetired: false, reason: 'missing-request-owner-execution-context' };
  }

  const navigation = frameNavigations
    .filter((event) => event.frameId === entry.frameId && event.isMainFrame === true &&
      event.loaderId && event.loaderId !== entry.loaderId && event.at >= entry.startedAt)
    .sort((left, right) => left.at - right.at)[0];
  if (!navigation) return { ownerRetired: false, reason: 'owning-loader-still-active' };

  const contextRetirement = executionContextRetirements
    .filter((event) => event.frameId === entry.frameId && event.isDefault === true &&
      (event.eventType === 'Runtime.executionContextDestroyed' || event.eventType === 'Runtime.executionContextsCleared') &&
      ownerContexts.has(event.executionContextId) && event.createdAt <= entry.startedAt && event.at >= entry.startedAt &&
      (ownerContexts.get(event.executionContextId).uniqueId === undefined || event.uniqueId === undefined ||
        ownerContexts.get(event.executionContextId).uniqueId === event.uniqueId))
    .sort((left, right) => left.at - right.at)[0];
  if (!contextRetirement) return { ownerRetired: false, reason: 'default-execution-context-still-active' };

  return {
    ownerRetired: true,
    reason: 'same-frame-main-loader-navigated-and-captured-default-context-retired',
    requestId: entry.requestId,
    owner: rule.owner,
    endpoint: rule.endpoint,
    requestCorrelationId: entry.requestCorrelationId,
    frameId: entry.frameId,
    retiredLoaderId: entry.loaderId,
    replacementLoaderId: navigation.loaderId,
    navigationAt: navigation.at,
    executionContextId: contextRetirement.executionContextId,
    executionContextUniqueId: contextRetirement.uniqueId,
    executionContextRetirementEvent: contextRetirement.eventType,
    executionContextCreatedAt: contextRetirement.createdAt,
    executionContextRetiredAt: contextRetirement.at,
    initiator: rule.component,
  };
};

const sameDocumentOwnerRetirement = (entry, rule) => {
  const expectedAction = nativeAbortOwnerRetirementActionFor(rule.owner);
  if (!expectedAction) return { ownerRetired: false, reason: 'same-document-owner-action-not-allowlisted' };
  const failureClock = nativeAbortNetworkFailureClock(entry);
  if (failureClock.basis !== 'request-wall-time-calibrated-cdp-monotonic' || !Number.isFinite(failureClock.at)) {
    return { ownerRetired: false, reason: 'same-document-retirement-lacks-calibrated-network-clock' };
  }
  const failedAt = failureClock.at;
  const correlation = (entry.abortControllerProbeEvidence ?? []).find((candidate) =>
    candidate?.exactRequestSignalCorrelation === true &&
    typeof entry.cdpRequestId === 'string' && entry.cdpRequestId.length > 0 &&
    entry.cdpRequestMatchCount === 1 &&
    candidate?.networkRequestId === entry.cdpRequestId &&
    candidate?.networkFailureObservedSeparately === true &&
    candidate?.networkFailureClockBasis === failureClock.basis &&
    candidate?.networkFailureObservedAt === failureClock.at &&
    candidate?.signalWasAlreadyAborted === false &&
    candidate?.sameDocumentOwnerRetirement === true &&
    candidate?.request?.requestId === entry.requestCorrelationId &&
    candidate?.request?.path === entry.path &&
    candidate?.request?.method === entry.method &&
    candidate?.request?.endpoint === rule.endpoint &&
    Number.isFinite(candidate?.request?.startedAt) &&
    Number.isFinite(candidate?.controllerAbortedAt) &&
    candidate.request.startedAt <= candidate.controllerAbortedAt &&
    candidate.controllerAbortedAt <= failedAt &&
    candidate?.ownerRetirementAction?.isTrusted === true &&
    ['native-event-isTrusted-true', 'trusted-interaction-list-membership']
      .includes(candidate.ownerRetirementAction.trustEvidence) &&
    candidate.ownerRetirementAction.type === 'click' &&
    candidate.ownerRetirementAction.at >= entry.startedAt &&
    candidate.ownerRetirementAction.at <= candidate.controllerAbortedAt,
  );
  if (!correlation) return { ownerRetired: false, reason: 'missing-exact-signal-and-trusted-owner-retirement-action' };

  const before = correlation.ownerDomAtFetch;
  const after = correlation.ownerDomAtAbort;
  if (!before || before.status !== 'unique' || before.connectedAtFetch !== true || before.ruleOwner !== rule.owner ||
      !before.anchorId || !after || after.anchorId !== before.anchorId ||
      after.connectedAtAbort !== false || after.detachedAtAbort !== true ||
      !Number.isFinite(after.observedAtAbort) || after.observedAtAbort !== correlation.controllerAbortedAt) {
    return { ownerRetired: false, reason: 'exact-owner-anchor-not-proven-detached-at-abort' };
  }
  const legacyDialogExitAction = expectedAction === 'row-settings-dialog-exit' &&
    before.retirementAction === 'row-settings-back-to-table';
  if (before.retirementAction !== expectedAction && !legacyDialogExitAction) {
    return { ownerRetired: false, reason: 'owner-anchor-action-does-not-match-rule' };
  }

  const action = correlation.ownerRetirementAction;
  if (expectedAction === 'coded-to-fields-tab' &&
      (before.selectedTabAtFetch !== 'Coded values' || !before.tabGroupId || before.tabGroupId !== after.tabGroupId ||
       after.tabGroupConnectedAtAbort !== true || after.selectedTabAtAbort !== 'Fields and related data' ||
       action.closestButton?.tabGroupId !== before.tabGroupId ||
       action.closestButton?.accessibleLabel !== 'Fields and related data')) {
    return { ownerRetired: false, reason: 'coded-to-fields-action-and-state-transition-not-proven' };
  }
  if (expectedAction === 'row-settings-dialog-exit' &&
      (!before.dialogId || before.dialogConnectedAtFetch !== true || before.dialogId !== after.dialogId ||
       after.dialogConnectedAtAbort !== false || action.closestButton?.dialogId !== before.dialogId ||
       (action.closestButton?.accessibleLabel !== 'Back to table' &&
        action.closestButton?.testId !== 'construction-action-group-rows' &&
        action.closestButton?.testId !== 'construction-action-related-rows'))) {
    return { ownerRetired: false, reason: 'row-settings-close-action-and-dialog-retirement-not-proven' };
  }
  if (expectedAction === 'related-expand-proposal-apply' &&
      (!before.ownerAttributes?.stageId || !before.ownerAttributes?.outputId ||
       before.ownerAttributes.stageId !== after.ownerAttributes?.stageId ||
       before.ownerAttributes.outputId !== after.ownerAttributes?.outputId ||
       entry.request.stageId !== before.ownerAttributes.stageId ||
       entry.request.outputId !== before.ownerAttributes.outputId ||
       action.closestButton?.testId !== 'construction-apply-proposal')) {
    return { ownerRetired: false, reason: 'related-expansion-stage-output-and-apply-action-not-proven' };
  }
  if (expectedAction === 'catalog-add-selected-feature' &&
      action.closestButton?.accessibleLabel !== 'Add 1 selected feature') {
    return { ownerRetired: false, reason: 'exact-single-feature-catalog-add-action-not-proven' };
  }


  return {
    ownerRetired: true,
    reason: expectedAction === 'coded-to-fields-tab'
      ? 'same-document-coded-owner-detached-after-trusted-fields-tab-selection'
      : expectedAction === 'row-settings-dialog-exit'
        ? 'same-document-population-owner-detached-after-trusted-row-dialog-exit'
        : expectedAction === 'related-expand-proposal-apply'
          ? 'same-document-related-expansion-owner-detached-after-trusted-apply'
          : 'same-document-catalog-owner-detached-after-trusted-add-selected-feature',
    requestId: entry.requestId,
    requestCorrelationId: entry.requestCorrelationId,
    owner: rule.owner,
    endpoint: rule.endpoint,
    componentRetirementAction: expectedAction,
    componentAnchor: before.selector,
    componentAnchorId: before.anchorId,
    componentAnchorCapturedAt: before.capturedAt,
    componentAnchorDetachedAtAbort: correlation.controllerAbortedAt,
    componentAbortControllerId: correlation.controllerId,
    componentAbortAt: correlation.controllerAbortedAt,
    trustedActionAt: action.at,
    trustedActionLabel: action.closestButton.accessibleLabel,
    trustedActionTestId: action.closestButton.testId,
    networkFailureAt: failedAt,
    frameId: entry.frameId,
    loaderId: entry.loaderId,
    initiator: rule.component,
    networkCompleted: false,
    networkTerminal: true,
    bodyReadStatus: 'failed',
  };
};

/** Prove owner retirement without changing network/body completion state. */
export const classifyNativeRequestOwnerRetirement = (entry, evidence = {}) => {
  if (entry.networkTerminal || entry.bodyReadStatus !== 'pending') {
    return { ownerRetired: false, reason: 'request-not-pending' };
  }
  if (entry.responseReceivedAt || entry.status !== undefined) {
    return { ownerRetired: false, reason: 'response-already-started' };
  }
  return proveOwnerRetired(entry, evidence);
};

/** Recognize only a recorded AbortController cancellation after its exact UI owner retired. */
export const classifyExpectedOwnedCancellation = (entry, evidence = {}) => {
  if (!entry.networkTerminal || entry.bodyReadStatus !== 'failed') {
    return { expected: false, reason: 'request-not-terminal-failure' };
  }
  if (entry.loadingFailed?.errorText !== 'net::ERR_ABORTED' || entry.loadingFailed?.canceled !== true ||
      entry.loadingFailed?.blockedReason || entry.loadingFailed?.corsErrorStatus) {
    return { expected: false, reason: 'not-explicit-abort-cancellation' };
  }
  if (entry.status !== undefined || entry.responseReceivedAt) {
    return { expected: false, reason: 'http-response-started-before-failure' };
  }
  const navigationOwnership = proveOwnerRetired(entry, evidence);
  const navigationRetiredAt = navigationOwnership.ownerRetired
    ? Math.max(navigationOwnership.navigationAt, navigationOwnership.executionContextRetiredAt)
    : undefined;
  const failureClock = nativeAbortNetworkFailureClock(entry);
  const navigationPrecededFailure = Number.isFinite(navigationRetiredAt) &&
    Number.isFinite(failureClock.at) && navigationRetiredAt <= failureClock.at;
  let ownership = navigationPrecededFailure ? navigationOwnership : undefined;
  if (!ownership) {
    const rule = findOwnerRule(entry);
    if (rule) {
      const componentOwnership = sameDocumentOwnerRetirement(entry, rule);
      if (componentOwnership.ownerRetired && componentOwnership.componentAbortAt <= failureClock.at) {
        ownership = componentOwnership;
      }
    }
  }
  if (!ownership) {
    if (navigationOwnership.ownerRetired) {
      return { expected: false, reason: 'cancellation-precedes-authoritative-owner-retirement' };
    }
    return { expected: false, reason: navigationOwnership.reason };
  }
  const retiredAt = navigationPrecededFailure
    ? navigationRetiredAt
    : ownership.componentAbortAt;
  if (!Number.isFinite(failureClock.at) || failureClock.at < retiredAt) {
    return { expected: false, reason: 'cancellation-precedes-authoritative-owner-retirement' };
  }
  return {
    expected: true,
    reason: 'explicit-canceled-background-read-after-authoritative-owner-retirement',
    ownerRetirement: ownership,
    networkCompleted: false,
    networkTerminal: true,
    bodyReadStatus: 'failed',
  };
};
