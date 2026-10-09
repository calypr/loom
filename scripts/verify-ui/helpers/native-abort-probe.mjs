const bindingName = '__loomNativeAbortProbeBinding';

/** Page anchors and user actions that can prove a same-document owner retirement. */
export const nativeAbortDomOwnerRules = [
  {
    endpoint: 'construction-capabilities',
    requestIdPrefix: 'cda-request-',
    owner: 'construction-lifecycle-capabilities',
    selector: '[data-testid^="construction-table-"][aria-current="page"]',
    ownerAttributes: { selectedTableTestId: 'data-testid' },
  },
  {
    endpoint: 'semantic-inventory',
    requestIdPrefix: 'feature-catalog-',
    owner: 'feature-catalog',
    selector: '#feature-catalog-search',
  },
  {
    endpoint: 'schema-fields',
    requestIdPrefix: 'schema-fields-',
    owner: 'feature-catalog-generated-fields',
    selector: '#feature-catalog-search',
    retirementAction: 'close-operation-editor',
  },
  {
    endpoint: 'semantic-inventory',
    requestIdPrefix: 'frame-categories-',
    owner: 'frame-category-catalog',
    selector: '[data-testid^="frame-categories-"]',
    retirementAction: 'coded-to-fields-tab',
  },
  {
    endpoint: 'semantic-inventory',
    requestIdPrefix: 'paired-column-inventory-',
    owner: 'paired-column-inventory',
    selector: '[data-testid="paired-column-suggestions"]',
    retirementAction: 'coded-to-fields-tab',
  },
  {
    endpoint: 'population-routes',
    requestIdPrefix: 'population-routes-',
    owner: 'population-route-options',
    selector: '[aria-label="Starting collection"]',
    retirementAction: 'row-settings-dialog-exit',
  },
  {
    endpoint: 'frame-source-options',
    requestIdPrefix: 'frame-source-options-',
    owner: 'frame-source-options',
    selector: '[data-testid="frame-source-panel"]',
    retirementAction: 'coded-to-fields-tab',
  },
  {
    endpoint: 'related-expand-choices',
    requestIdPrefix: 'related-expand-choices-',
    owner: 'related-expand-choice-editor',
    selector: '[data-testid="construction-related-expand-editor"]',
    retirementAction: 'related-expand-proposal-apply',
    ownerAttributes: {
      stageId: 'data-related-stage-id',
      outputId: 'data-related-output-id',
    },
  },
  {
    endpoint: 'construction-choices',
    requestIdPrefix: 'paired-column-choices-',
    owner: 'paired-column-choice-suggestions',
    selector: '[data-testid="paired-column-suggestions"]',
    retirementAction: 'coded-to-fields-tab',
  },
  {
    endpoint: 'construction-choices',
    requestIdPrefix: 'construction-choices-',
    owner: 'catalog-choice-search',
    selector: '#feature-catalog-search',
    retirementAction: 'catalog-add-selected-feature',
  },
];

const requestPrefixes = {
  'construction-capabilities': ['cda-request-'],
  'semantic-inventory': ['feature-catalog-', 'frame-categories-', 'paired-column-inventory-'],
  'schema-fields': ['schema-fields-'],
  'configured-column-context': ['configured-column-context-'],
  'population-routes': ['population-routes-'],
  'frame-source-options': ['frame-source-options-'],
  'related-expand-choices': ['related-expand-choices-'],
  'construction-choices': ['paired-column-choices-', 'construction-choices-'],
};

/** Build a page-only probe. It records exact signal ownership and selected request metadata, never raw bodies or credentials. */
export const createNativeAbortProbeSource = ({ project, explorer, apiOrigin }) => {
  const projectRouteExplorerScope = explorer?.mode === 'project-routes';
  if (!projectRouteExplorerScope && (typeof explorer !== 'string' || explorer.length === 0)) {
    throw new TypeError('Native abort probe source requires an exact Explorer or project-routes Explorer scope.');
  }
  return `(() => {
  const marker = '__loomNativeAbortProbeInstalled';
  if (globalThis[marker]) return;
  Object.defineProperty(globalThis, marker, { value: true, configurable: false });
  const scope = {
    project: ${JSON.stringify(project)},
    explorer: ${JSON.stringify(projectRouteExplorerScope ? null : explorer)},
    explorerScope: ${JSON.stringify(projectRouteExplorerScope ? 'project-routes' : 'exact')},
    apiOrigin: ${JSON.stringify(apiOrigin ?? null)},
  };
  const prefixes = ${JSON.stringify(requestPrefixes)};
  const ownerRules = ${JSON.stringify(nativeAbortDomOwnerRules)};
  const controllersBySignal = new WeakMap();
  const domIds = new WeakMap();
  const domOwnersByRequest = new WeakMap();
  const observedDomOwners = new Set();
  const trustedInteractions = [];
  let nextController = 1;
  let nextDomId = 1;
  let lastTrustedInteraction;
  const wallNow = () => Date.now();
  const safeStack = (stack) => String(stack ?? '').split('\\n').slice(2, 10).flatMap((line) => {
    const match = line.match(/((?:https?:\\/\\/)?[^()\\s]+):(\\d+):(\\d+)\\)?$/);
    if (!match) return [];
    let path = match[1];
    try { if (/^https?:\\/\\//.test(path)) path = new URL(path).pathname; } catch { return []; }
    return [{ path: path.slice(0, 300), line: Number(match[2]), column: Number(match[3]) }];
  }).slice(0, 6);
  const send = (event) => {
    try {
      if (typeof globalThis.${bindingName} === 'function') globalThis.${bindingName}(JSON.stringify(event));
    } catch { /* Probe evidence must never affect the application request. */ }
  };
  const readHeader = (headers, name) => {
    try {
      if (typeof headers?.get === 'function') return headers.get(name);
      for (const [key, value] of Object.entries(headers ?? {})) if (key.toLowerCase() === name.toLowerCase()) return String(value);
    } catch { /* Ignore unsupported header containers. */ }
    return undefined;
  };
  const domId = (node) => {
    if (!node || typeof node !== 'object') return undefined;
    let id = domIds.get(node);
    if (!id) { id = 'dom-node-' + nextDomId++; domIds.set(node, id); }
    return id;
  };
  const cleanLabel = (value) => String(value ?? '').replace(/\\s+/g, ' ').trim().slice(0, 100);
  const buttonLabel = (button) => cleanLabel(
    button?.getAttribute?.('aria-label') || button?.textContent || button?.innerText || '',
  );
  const selectedTab = (group) => {
    if (!group) return undefined;
    const selected = [...(group.querySelectorAll?.('button[aria-pressed="true"]') ?? [])];
    return selected.length === 1 ? buttonLabel(selected[0]) : undefined;
  };
  const selectedOwnerState = (element) => {
    const ariaCurrent = element?.getAttribute?.('aria-current');
    const ariaPressed = element?.getAttribute?.('aria-pressed');
    return {
      ...(ariaCurrent !== null && ariaCurrent !== undefined ? { ariaCurrent } : {}),
      ...(ariaPressed !== null && ariaPressed !== undefined ? { ariaPressed } : {}),
    };
  };
  const exactElements = (selector) => {
    try { return [...(globalThis.document?.querySelectorAll?.(selector) ?? [])]; } catch { return []; }
  };
  const findOwnerRule = (metadata) => ownerRules.find((rule) => rule.endpoint === metadata.endpoint &&
    metadata.requestId.startsWith(rule.requestIdPrefix));
  const captureOwnerDom = (rule) => {
    if (!rule) return undefined;
    const matches = exactElements(rule.selector);
    if (matches.length !== 1) {
      return {
        status: matches.length === 0 ? 'missing' : 'ambiguous',
        selector: rule.selector,
        matchCount: matches.length,
        capturedAt: wallNow(),
        connectedAtFetch: false,
        ruleOwner: rule.owner,
        retirementAction: rule.retirementAction,
      };
    }
    const element = matches[0];
    const tabGroups = rule.retirementAction === 'coded-to-fields-tab'
      ? exactElements('[aria-label="Column types"]')
      : [];
    const tabGroup = tabGroups.length === 1 ? tabGroups[0] : undefined;
    const dialog = rule.retirementAction === 'row-settings-dialog-exit'
      ? element.closest?.('[role="dialog"][aria-label="Row definition settings"]')
      : undefined;
    const panel = rule.owner === 'frame-category-catalog'
      ? element.closest?.('[data-testid="frame-source-panel"]')
      : undefined;
    const connected = element.isConnected === true;
    const connectedDialog = dialog?.isConnected === true;
    const connectedPanel = rule.owner === 'frame-category-catalog' && panel?.isConnected === true;
    const contextConnected = rule.owner === 'frame-category-catalog' ? connectedPanel
      : rule.retirementAction === 'row-settings-dialog-exit' ? connectedDialog
        : rule.retirementAction === 'coded-to-fields-tab' ? tabGroup?.isConnected === true : true;
    const ownerAttributes = Object.fromEntries(Object.entries(rule.ownerAttributes ?? {})
      .map(([name, attribute]) => [name, element.getAttribute?.(attribute)])
      .filter(([, value]) => typeof value === 'string' && value.length > 0));
    const record = {
      status: connected && contextConnected ? 'unique' : 'disconnected',
      selector: rule.selector,
      matchCount: 1,
      capturedAt: wallNow(),
      anchorId: domId(element),
      connectedAtFetch: connected,
      ruleOwner: rule.owner,
      retirementAction: rule.retirementAction,
      ...selectedOwnerState(element),
      tabGroupId: domId(tabGroup),
      selectedTabAtFetch: selectedTab(tabGroup),
      dialogId: domId(dialog),
      dialogConnectedAtFetch: dialog ? connectedDialog : undefined,
      parentPanelId: domId(panel),
      parentPanelConnectedAtFetch: panel ? connectedPanel : undefined,
      ...(Object.keys(ownerAttributes).length ? { ownerAttributes } : {}),
    };
    const refs = { element, tabGroup, dialog, panel, ownerAttributes: rule.ownerAttributes, observedDetachedAt: undefined };
    observedDomOwners.add(refs);
    return { record, refs };
  };
  const recordOwnerAtAbort = (refs, abortedAt) => {
    if (!refs) return undefined;
    return {
      anchorId: domId(refs.element),
      connectedAtAbort: refs.element?.isConnected === true,
      detachedAtAbort: refs.element?.isConnected === false,
      detachedObservedAt: refs.observedDetachedAt,
      tabGroupId: domId(refs.tabGroup),
      selectedTabAtAbort: selectedTab(refs.tabGroup),
      tabGroupConnectedAtAbort: refs.tabGroup ? refs.tabGroup.isConnected === true : undefined,
      dialogId: domId(refs.dialog),
      dialogConnectedAtAbort: refs.dialog ? refs.dialog.isConnected === true : undefined,
      parentPanelId: domId(refs.panel),
      parentPanelConnectedAtAbort: refs.panel ? refs.panel.isConnected === true : undefined,
      ...(refs.ownerAttributes ? {
        ownerAttributes: Object.fromEntries(Object.entries(refs.ownerAttributes)
          .map(([name, attribute]) => [name, refs.element?.getAttribute?.(attribute)])
          .filter(([, value]) => typeof value === 'string' && value.length > 0)),
      } : {}),
      observedAtAbort: abortedAt,
      ...selectedOwnerState(refs.element),
    };
  };
  const requestMetadata = (input, init) => {
    let url;
    try { url = new URL(typeof input === 'string' ? input : input.url, globalThis.location.href); } catch { return undefined; }
    const prefix = '/api/v1/projects/' + encodeURIComponent(scope.project) + '/explorers/';
    if ((scope.apiOrigin && url.origin !== scope.apiOrigin) || !url.pathname.startsWith(prefix)) return undefined;
    const route = url.pathname.slice(prefix.length).split('/');
    if (route.length !== 4 || route[1] !== 'authoring' || route[2] !== 'v2' || !route[0] || !route[3]) return undefined;
    let routeExplorer;
    try { routeExplorer = decodeURIComponent(route[0]); } catch { return undefined; }
    if (scope.explorerScope === 'exact' && routeExplorer !== scope.explorer) return undefined;
    const endpoint = route[3];
    if (!Object.hasOwn(prefixes, endpoint)) return undefined;
    const method = String(init?.method ?? input?.method ?? 'GET').toUpperCase();
    if (method !== 'POST') return undefined;
    const headers = init?.headers ?? input?.headers;
    const requestId = readHeader(headers, 'X-Request-ID');
    if (endpoint === 'construction-capabilities' && (requestId === undefined || requestId === null || requestId === '')) {
      return { requestId: undefined, origin: url.origin, path: url.pathname, method, endpoint, requestIdSource: 'probe-injected' };
    }
    if (typeof requestId !== 'string' || !prefixes[endpoint].some((item) => requestId.startsWith(item))) return undefined;
    if (!/^[a-z0-9-]{3,80}-[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(requestId)) return undefined;
    return { requestId, origin: url.origin, path: url.pathname, method, endpoint, explorer: routeExplorer, requestIdSource: 'request-header' };
  };
  const constructionRequestContext = (init, endpoint) => {
    if (endpoint !== 'construction-capabilities' || typeof init?.body !== 'string') return undefined;
    try {
      const body = JSON.parse(init.body);
      if (!body || typeof body !== 'object' || Array.isArray(body)) return undefined;
      return {
        snapshotToken: typeof body.snapshotToken === 'string' ? body.snapshotToken : undefined,
        expectedDraftVersion: Number.isSafeInteger(body.expectedDraftVersion) ? body.expectedDraftVersion : undefined,
        expectedDraftDigest: typeof body.expectedDraftDigest === 'string' ? body.expectedDraftDigest : undefined,
        outputId: typeof body.outputId === 'string' ? body.outputId : undefined,
        stageId: typeof body.stageId === 'string' ? body.stageId : undefined,
      };
    } catch { return undefined; }
  };
  const observer = typeof globalThis.MutationObserver === 'function' && globalThis.document
    ? new globalThis.MutationObserver(() => {
        for (const refs of observedDomOwners) {
          if (refs.observedDetachedAt === undefined && refs.element?.isConnected === false) refs.observedDetachedAt = wallNow();
        }
      })
    : undefined;
  try { observer?.observe(globalThis.document, { childList: true, subtree: true }); } catch { /* Synchronous isConnected sampling remains authoritative. */ }
  const originalFetch = globalThis.fetch;
  globalThis.fetch = function(...args) {
    let metadata = requestMetadata(args[0], args[1]);
    const signal = args[1]?.signal ?? args[0]?.signal;
    const owner = signal && controllersBySignal.get(signal);
    let fetchArgs = args;
    if (metadata?.endpoint === 'construction-capabilities' && metadata.requestId === undefined && owner &&
        typeof globalThis.crypto?.randomUUID === 'function' && typeof globalThis.Headers === 'function') {
      try {
        const requestId = 'cda-request-' + globalThis.crypto.randomUUID();
        const init = args[1] && typeof args[1] === 'object' ? args[1] : {};
        const headers = new globalThis.Headers(init.headers ?? args[0]?.headers);
        headers.set('X-Request-ID', requestId);
        fetchArgs = [args[0], { ...init, headers }];
        metadata = { ...metadata, requestId };
      } catch { metadata = undefined; }
    }
    let record;
    if (metadata?.requestId && owner) {
      const rule = findOwnerRule(metadata);
      const captured = captureOwnerDom(rule);
      const requestContext = constructionRequestContext(fetchArgs[1], metadata.endpoint);
      const selectedTableTestId = captured?.record?.ownerAttributes?.selectedTableTestId;
      record = {
        ...metadata,
        startedAt: wallNow(),
        fetchStateAtAbort: 'pending',
        ownerDomAtFetch: captured?.record ?? captured,
        requestContext,
        ownerOutputBindingAtFetch: requestContext?.outputId
          ? selectedTableTestId === 'construction-table-' + requestContext.outputId
          : undefined,
      };
      owner.requests.set(metadata.requestId, record);
      if (captured?.refs) domOwnersByRequest.set(record, captured.refs);
    }
    const result = Reflect.apply(originalFetch, this, fetchArgs);
    if (record && result && typeof result.then === 'function') {
      result.then(
        () => { record.fetchStateAtAbort = 'fulfilled'; record.settledAt = wallNow(); },
        () => { record.fetchStateAtAbort = 'rejected'; record.settledAt = wallNow(); },
      );
    }
    if (record && owner && signal?.aborted === true && Number.isFinite(owner.lastAbortAt)) {
      send({
        kind: 'abort-controller-fetch-observed-after-abort',
        controllerId: owner.id,
        controllerCreatedAt: owner.createdAt,
        controllerAbortedAt: owner.lastAbortAt,
        abortCount: owner.abortCount,
        observedAt: record.startedAt,
        signalWasAlreadyAborted: true,
        request: record,
      });
    }
    return result;
  };
  const NativeAbortController = globalThis.AbortController;
  globalThis.AbortController = class LoomObservedAbortController extends NativeAbortController {
    constructor(...args) {
      super(...args);
      const owner = {
        id: 'abort-controller-' + nextController++,
        createdAt: wallNow(),
        createdStack: safeStack(new Error('abort-controller-created').stack),
        requests: new Map(),
        abortCount: 0,
        lastAbortAt: undefined,
      };
      controllersBySignal.set(this.signal, owner);
    }
    abort(...args) {
      const owner = controllersBySignal.get(this.signal);
      if (owner) {
        owner.abortCount += 1;
        const abortedAt = wallNow();
        owner.lastAbortAt = abortedAt;
        const requests = [...owner.requests.values()].map((request) => ({
          ...request,
          ownerDomAtAbort: recordOwnerAtAbort(domOwnersByRequest.get(request), abortedAt),
        }));
        send({
          kind: 'abort-controller-call',
          controllerId: owner.id,
          createdAt: owner.createdAt,
          abortedAt,
          abortCount: owner.abortCount,
          signalWasAlreadyAborted: this.signal.aborted,
          createdStack: owner.createdStack,
          abortStack: safeStack(new Error('abort-controller-abort').stack),
          requests,
          trustedInteractions: trustedInteractions.slice(-20),
          lastTrustedInteraction: lastTrustedInteraction ? { ...lastTrustedInteraction } : undefined,
          actionEnvelope: globalThis.__loomNativeAbortAction && typeof globalThis.__loomNativeAbortAction === 'object'
            ? {
                id: String(globalThis.__loomNativeAbortAction.id ?? '').slice(0, 100) || undefined,
                name: String(globalThis.__loomNativeAbortAction.name ?? '').slice(0, 100) || undefined,
                selector: String(globalThis.__loomNativeAbortAction.selector ?? '').slice(0, 200) || undefined,
                startedAt: Number.isFinite(globalThis.__loomNativeAbortAction.startedAt) ? globalThis.__loomNativeAbortAction.startedAt : undefined,
                endedAt: Number.isFinite(globalThis.__loomNativeAbortAction.endedAt) ? globalThis.__loomNativeAbortAction.endedAt : undefined,
              }
            : undefined,
        });
        for (const request of owner.requests.values()) {
          const refs = domOwnersByRequest.get(request);
          if (refs) observedDomOwners.delete(refs);
        }
      }
      const result = super.abort(...args);
      if (owner && owner.requests.size > 0) {
        Promise.resolve().then(() => {
          send({
            kind: 'abort-controller-fetch-settlement',
            controllerId: owner.id,
            observedAt: wallNow(),
            requests: [...owner.requests.values()].map((request) => ({
              requestId: request.requestId,
              origin: request.origin,
              path: request.path,
              method: request.method,
              fetchStateAfterAbort: request.fetchStateAtAbort,
              settledAt: request.settledAt,
            })),
          });
        });
      }
      return result;
    }
  };
  const targetSummary = (target) => {
    if (!target || typeof target !== 'object') return undefined;
    return {
      tag: String(target.tagName ?? '').slice(0, 24),
      role: String(target.getAttribute?.('role') ?? '').slice(0, 40) || undefined,
      testId: String(target.getAttribute?.('data-testid') ?? '').slice(0, 100) || undefined,
      ariaLabel: cleanLabel(target.getAttribute?.('aria-label')) || undefined,
    };
  };
  const trustedButtonSummary = (target) => {
    const button = target?.closest?.('button');
    if (!button) return undefined;
    const tabGroup = button.closest?.('[aria-label="Column types"]');
    const dialog = button.closest?.('[role="dialog"][aria-label="Row definition settings"]');
    return {
      id: domId(button),
      accessibleLabel: buttonLabel(button) || undefined,
      testId: String(button.getAttribute?.('data-testid') ?? '').slice(0, 100) || undefined,
      ariaPressed: button.getAttribute?.('aria-pressed') === 'true' ? true
        : button.getAttribute?.('aria-pressed') === 'false' ? false : undefined,
      tabGroupId: domId(tabGroup),
      dialogId: domId(dialog),
    };
  };
  for (const eventType of ['click', 'change', 'input', 'keydown', 'submit']) {
    globalThis.document?.addEventListener?.(eventType, (event) => {
      if (!event.isTrusted) return;
      const interaction = {
        type: eventType,
        at: wallNow(),
        isTrusted: true,
        target: targetSummary(event.target),
        closestButton: trustedButtonSummary(event.target),
        actionId: globalThis.__loomNativeAbortAction?.id,
      };
      trustedInteractions.push(interaction);
      if (trustedInteractions.length > 40) trustedInteractions.shift();
      lastTrustedInteraction = interaction;
    }, true);
  }
  send({ kind: 'probe-installed', at: wallNow(), project: scope.project, explorer: scope.explorer, explorerScope: scope.explorerScope });
})();`;
};

/** Install the probe for the current document and future navigations in its browser context. */
export const installNativeAbortProbe = async ({ page, report, project, explorer, apiOrigin }) => {
  if (!page || typeof page.context !== 'function' || typeof page.evaluate !== 'function' ||
      !report || typeof report !== 'object' || typeof project !== 'string' || project.length === 0 ||
      (typeof explorer !== 'string' && explorer?.mode !== 'project-routes') ||
      (typeof explorer === 'string' && explorer.length === 0) || typeof apiOrigin !== 'string') {
    throw new TypeError('Native abort capture requires a Playwright page, report, project, Explorer, and API origin.');
  }

  let scopedApiOrigin;
  try { scopedApiOrigin = new URL(apiOrigin).origin; }
  catch { throw new TypeError('Native abort capture requires a valid API origin URL.'); }

  if (report.nativeAbortProbeEvents === undefined) report.nativeAbortProbeEvents = [];
  else if (!Array.isArray(report.nativeAbortProbeEvents)) {
    throw new TypeError('Native abort capture report events must be an array.');
  }

  const browserContext = page.context();
  if (!browserContext || typeof browserContext.exposeBinding !== 'function' ||
      typeof browserContext.addInitScript !== 'function') {
    throw new TypeError('Native abort capture requires a Playwright browser context.');
  }

  await browserContext.exposeBinding(bindingName, (_source, payload) => {
    let event;
    try { event = JSON.parse(payload); }
    catch {
      report.nativeAbortProbeEvents.push({ kind: 'probe-payload-invalid', payloadLength: String(payload).length });
      return;
    }
    report.nativeAbortProbeEvents.push(event);
  });
  const source = createNativeAbortProbeSource({ project, explorer, apiOrigin: scopedApiOrigin });
  await browserContext.addInitScript(source);
  await page.evaluate(source);
  return report.nativeAbortProbeEvents;
};

const ownerRetirementActionForRule = (owner) =>
  nativeAbortDomOwnerRules.find((rule) => rule.owner === owner)?.retirementAction;

/** Convert a Network.loadingFailed monotonic timestamp with this request's own CDP wall/monotonic pair. */
export const nativeAbortNetworkFailureClock = (entry) => {
  const failedTimestamp = entry.loadingFailed?.timestamp;
  const requestTimestamp = entry.requestTimestamp;
  const requestWallTime = entry.requestWallTime;
  if (typeof entry.requestId === 'string' && entry.requestId.length > 0 &&
      [failedTimestamp, requestTimestamp, requestWallTime].every(Number.isFinite) &&
      requestTimestamp > 0 && requestWallTime > 0 && failedTimestamp >= requestTimestamp) {
    return {
      at: (requestWallTime + (failedTimestamp - requestTimestamp)) * 1000,
      basis: 'request-wall-time-calibrated-cdp-monotonic',
    };
  }
  return Number.isFinite(entry.loadingFailed?.at)
    ? { at: entry.loadingFailed.at, basis: 'host-wall-clock-at-cdp-callback' }
    : { at: undefined, basis: undefined };
};

const interactionProvesOwnerRetirementAction = (request, ownerDomAtAbort, interaction, abortedAt) => {
  const rule = nativeAbortDomOwnerRules.find((candidate) => candidate.owner === request.ownerDomAtFetch?.ruleOwner);
  if (!rule?.retirementAction || !interaction || interaction.isTrusted !== true ||
      !['native-event-isTrusted-true', 'trusted-interaction-list-membership'].includes(interaction.trustEvidence) ||
      interaction.type !== 'click' ||
      !Number.isFinite(interaction.at) || interaction.at < request.startedAt || interaction.at > abortedAt ||
      !ownerDomAtAbort?.detachedAtAbort || ownerDomAtAbort.connectedAtAbort !== false ||
      request.ownerDomAtFetch?.status !== 'unique' || request.ownerDomAtFetch?.connectedAtFetch !== true ||
      request.ownerDomAtFetch.anchorId !== ownerDomAtAbort.anchorId) return false;

  if (rule.retirementAction === 'coded-to-fields-tab') {
    return request.ownerDomAtFetch.selectedTabAtFetch === 'Coded values' &&
      request.ownerDomAtFetch.tabGroupId &&
      request.ownerDomAtFetch.tabGroupId === ownerDomAtAbort.tabGroupId &&
      ownerDomAtAbort.tabGroupConnectedAtAbort === true &&
      ownerDomAtAbort.selectedTabAtAbort === 'Fields and related data' &&
      interaction.closestButton?.tabGroupId === request.ownerDomAtFetch.tabGroupId &&
      interaction.closestButton?.accessibleLabel === 'Fields and related data';
  }

  if (rule.retirementAction === 'row-settings-dialog-exit') {
    return request.ownerDomAtFetch.dialogId && request.ownerDomAtFetch.dialogConnectedAtFetch === true &&
      request.ownerDomAtFetch.dialogId === ownerDomAtAbort.dialogId &&
      ownerDomAtAbort.dialogConnectedAtAbort === false &&
      interaction.closestButton?.dialogId === request.ownerDomAtFetch.dialogId &&
      (interaction.closestButton?.accessibleLabel === 'Back to table' ||
        interaction.closestButton?.testId === 'construction-action-group-rows' ||
        interaction.closestButton?.testId === 'construction-action-related-rows');
  }

  if (rule.retirementAction === 'related-expand-proposal-apply') {
    const beforeAttributes = request.ownerDomAtFetch.ownerAttributes;
    const afterAttributes = ownerDomAtAbort.ownerAttributes;
    return beforeAttributes?.stageId === afterAttributes?.stageId &&
      beforeAttributes?.outputId === afterAttributes?.outputId &&
      Boolean(beforeAttributes?.stageId) && Boolean(beforeAttributes?.outputId) &&
      interaction.closestButton?.testId === 'construction-apply-proposal';
  }

  if (rule.retirementAction === 'catalog-add-selected-feature') {
    return interaction.closestButton?.accessibleLabel === 'Add 1 selected feature';
  }

  if (rule.retirementAction === 'close-operation-editor') {
    return interaction.closestButton?.testId === 'construction-close-operation-editor' &&
      interaction.closestButton?.accessibleLabel === 'Close operation editor';
  }

  return false;
};

/** Link a CDP failure to the exact signal and page-side owner/action evidence. */
export const nativeAbortProbeEvidenceForRequest = (entry, events) => {
  const failureClock = nativeAbortNetworkFailureClock(entry);
  const failedAt = failureClock.at;
  if (typeof entry.requestCorrelationId !== 'string' || typeof entry.path !== 'string' || !Number.isFinite(failedAt)) return [];
  return events.flatMap((event) => {
    if (event.kind !== 'abort-controller-call' || event.signalWasAlreadyAborted !== false ||
        !Number.isFinite(event.abortedAt) || event.abortedAt > failedAt) return [];
    return (event.requests ?? []).filter((request) =>
      request.requestId === entry.requestCorrelationId && request.origin === entry.origin && request.path === entry.path &&
      request.method === entry.method && Number.isFinite(request.startedAt) && request.startedAt <= event.abortedAt,
    ).map((request) => {
      const hasTrustedInteractionList = Array.isArray(event.trustedInteractions);
      const interactions = (event.trustedInteractions ?? (event.lastTrustedInteraction ? [event.lastTrustedInteraction] : []))
        .filter((interaction) => (interaction?.isTrusted === true ||
          (interaction?.isTrusted === undefined && hasTrustedInteractionList && event.trustedInteractions.includes(interaction))) &&
          interaction?.type === 'click' &&
          interaction.at >= request.startedAt && interaction.at <= event.abortedAt)
        .map((interaction) => ({
          ...interaction,
          isTrusted: true,
          trustEvidence: interaction.isTrusted === true
            ? 'native-event-isTrusted-true'
            : 'trusted-interaction-list-membership',
        }));
      const ownerRetirementAction = interactions.find((interaction) => interactionProvesOwnerRetirementAction(
        request, request.ownerDomAtAbort, interaction, event.abortedAt,
      ));
      return {
        networkRequestId: entry.cdpRequestId,
        controllerId: event.controllerId,
        controllerCreatedAt: event.createdAt,
        controllerAbortedAt: event.abortedAt,
        request,
        abortToNetworkFailureMs: failedAt - event.abortedAt,
        networkFailureObservedAt: failedAt,
        networkFailureClockBasis: failureClock.basis,
        trustedInteractionToAbortMs: Number.isFinite(event.lastTrustedInteraction?.at)
          ? event.abortedAt - event.lastTrustedInteraction.at
          : undefined,
        createdStack: event.createdStack,
        abortStack: event.abortStack,
        lastTrustedInteraction: event.lastTrustedInteraction,
        ownerRetirementAction,
        ownerDomAtFetch: request.ownerDomAtFetch,
        ownerDomAtAbort: request.ownerDomAtAbort,
        exactRequestSignalCorrelation: true,
        networkFailureObservedSeparately: true,
        signalWasAlreadyAborted: event.signalWasAlreadyAborted,
        sameDocumentOwnerRetirement: Boolean(ownerRetirementAction) &&
          typeof entry.cdpRequestId === 'string' && entry.cdpRequestId.length > 0 &&
          entry.cdpRequestMatchCount === 1 &&
          failureClock.basis === 'request-wall-time-calibrated-cdp-monotonic' &&
          Number.isFinite(failedAt) && event.abortedAt <= failedAt,
      };
    });
  });
};

/** Correlate an AbortController event with the exact native request ID without classifying it as terminal. */
export const nativeAbortSignalObservationForRequest = (entry, events) => {
  const requestCorrelationId = entry.requestCorrelationId ?? entry.requestDetails?.requestId;
  if (typeof requestCorrelationId !== 'string' || requestCorrelationId.length === 0 ||
      typeof entry.origin !== 'string' || typeof entry.path !== 'string' || typeof entry.method !== 'string' ||
      entry.requestIdentityMatchCount !== 1) {
    return { exactRequestSignalCorrelation: false, matchCount: 0, reason: 'native request identity is incomplete' };
  }
  const matches = (events ?? []).flatMap((event) => {
    if (typeof event.controllerId !== 'string') return [];
    if (event.kind === 'abort-controller-fetch-observed-after-abort') {
      const request = event.request;
      const exactAbortWasCaptured = (events ?? []).some((candidate) => candidate.kind === 'abort-controller-call' &&
        candidate.controllerId === event.controllerId && candidate.abortedAt === event.controllerAbortedAt);
      return exactAbortWasCaptured && request?.requestId === requestCorrelationId && request.origin === entry.origin &&
        request.path === entry.path && request.method === entry.method
        ? [{ event, request, afterAbort: true }]
        : [];
    }
    if (event.kind !== 'abort-controller-call') return [];
    return (event.requests ?? []).filter((request) =>
      request.requestId === requestCorrelationId && request.origin === entry.origin && request.path === entry.path &&
      request.method === entry.method,
    ).map((request) => ({ event, request, afterAbort: false }));
  });
  if (matches.length !== 1) {
    return {
      exactRequestSignalCorrelation: false,
      matchCount: matches.length,
      reason: matches.length === 0 ? 'no exact AbortSignal event was captured' : 'multiple exact AbortSignal events were captured',
    };
  }
  const [{ event, request, afterAbort }] = matches;
  const settlements = (events ?? []).flatMap((candidate) => candidate.kind === 'abort-controller-fetch-settlement' &&
    candidate.controllerId === event.controllerId
      ? (candidate.requests ?? []).filter((item) => item.requestId === request.requestId &&
        item.origin === request.origin && item.path === request.path && item.method === request.method)
        .map((item) => ({ event: candidate, request: item }))
      : []);
  return {
    exactRequestSignalCorrelation: true,
    matchCount: 1,
    nativeEntryIdentityMatchCount: entry.requestIdentityMatchCount,
    requestId: request.requestId,
    requestIdSource: request.requestIdSource,
    origin: request.origin,
    controllerId: event.controllerId,
    controllerCreatedAt: afterAbort ? event.controllerCreatedAt : event.createdAt,
    controllerAbortedAt: afterAbort ? event.controllerAbortedAt : event.abortedAt,
    signalWasAlreadyAborted: afterAbort ? true : event.signalWasAlreadyAborted,
    requestObservedAfterAbort: afterAbort,
    fetchStateAtAbort: request.fetchStateAtAbort,
    ownerDomAtFetch: request.ownerDomAtFetch,
    ownerDomAtAbort: request.ownerDomAtAbort,
    requestContext: request.requestContext,
    ownerOutputBindingAtFetch: request.ownerOutputBindingAtFetch,
    ownerOutputBindingAtAbort: request.requestContext?.outputId
      ? request.ownerDomAtAbort?.ownerAttributes?.selectedTableTestId ===
        'construction-table-' + request.requestContext.outputId
      : undefined,
    selectedOwnerStateAtAbort: {
      ariaCurrent: request.ownerDomAtAbort?.ariaCurrent,
      ariaPressed: request.ownerDomAtAbort?.ariaPressed,
    },
    ...(settlements.length === 1 ? {
      fetchStateAfterAbort: settlements[0].request.fetchStateAfterAbort,
      fetchSettledAt: settlements[0].request.settledAt,
      fetchSettlementObservedAt: settlements[0].event.observedAt,
    } : { fetchSettlementMatchCount: settlements.length }),
    nativeTerminalObserved: Boolean(entry.terminalEvent || entry.completedAt || entry.loadingFailed),
    classificationEffect: 'diagnostic only; does not make an unfinished native request terminal or expected',
  };
};

export const nativeAbortOwnerRetirementActionFor = ownerRetirementActionForRule;
