import { applyInjectedFaultPolicy } from './network-evidence.mjs';
import { recordCheck } from './report.mjs';

const terminalEvents = new Set(['requestfinished', 'requestfailed']);

const projectExplorerCollectionPath = (project) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers`;

const routeFrom = (request) => {
  try {
    const url = new URL(request.url());
    return { origin: url.origin, path: url.pathname };
  } catch {
    return null;
  }
};

const pathBelongsToProject = (path, projectPath) =>
  typeof path === 'string' && (path === projectPath || path.startsWith(`${projectPath}/`));

const belongsToScope = (request, scope) => {
  const route = routeFrom(request);
  return Boolean(route && (!scope.origin || route.origin === scope.origin) &&
    pathBelongsToProject(route.path, scope.projectPath));
};

const safeClone = (value) => {
  if (value === undefined) return undefined;
  try { return structuredClone(value); }
  catch { return undefined; }
};

const sameFrameNavigationEventsAfterStart = (request, navigationTimings) => {
  if (request.frameIdentityStatus !== 'exact' || typeof request.pageId !== 'string' ||
      typeof request.frameId !== 'string' || !Number.isSafeInteger(request.navigationSequenceAtStart)) return [];
  return navigationTimings
    .filter(navigation => navigation.frameIdentityStatus === 'exact' &&
      navigation.pageId === request.pageId && navigation.frameId === request.frameId &&
      Number.isSafeInteger(navigation.sequence) && navigation.sequence > request.navigationSequenceAtStart)
    .map(({ id, sequence, phase }) => ({ id, sequence, phase: phase ?? null }));
};

const hasTerminalPayload = (entry) => Number.isInteger(entry.status) ||
  (typeof entry.failure === 'string' && entry.failure.trim().length > 0);

const appendEvent = (entry, event, observedAt, details = {}) => {
  entry.nativeEventChronology.push({
    event,
    browserRequestId: entry.browserRequestId,
    observedAt,
    objectMatch: true,
    ...details,
  });
  if (terminalEvents.has(event) && !entry.terminalEvent) {
    entry.terminalEvent = event;
    entry.completedAt = observedAt;
  }
};

const serializedRequest = (entry) => {
  const result = { ...entry };
  delete result.fixtureDiagnostic;
  const diagnostic = entry.fixtureDiagnostic;
  if (diagnostic) {
    for (const key of ['expected', 'canceled', 'cancellationReason', 'expectedCancellation',
      'expectedHttpFailure', 'expectedInjectedFault', 'injectedFault', 'injectedAction',
      'injectedRequestId', 'injectedStatus', 'replacement', 'binding', 'requestTimeline']) {
      if (diagnostic[key] !== undefined) result[key] = safeClone(diagnostic[key]);
    }
  }
  return safeClone(result);
};

export function openBasicFixtureNativeRequestScope(ledger, target) {
  const project = target?.fixtureProject;
  if (target?.kind !== 'isolated' || typeof project !== 'string' ||
      !/^loom_dev_verify_[a-z0-9][a-z0-9-]{0,24}$/.test(project)) return null;
  if (!ledger || typeof ledger.openScope !== 'function' || typeof target.uiUrl !== 'string') {
    throw new TypeError('Basic verification scope requires its fixture ledger and owned UI origin.');
  }
  return ledger.openScope({
    project,
    origin: new URL(target.uiUrl).origin,
    automatic: true,
    requireProjectCreate: false,
    ...(typeof target.bootstrapExplorerId === 'string' && target.bootstrapExplorerId.trim()
      ? { defaultExplorer: target.bootstrapExplorerId } : {}),
  });
}

export function createFixtureNativeRequestLedger() {
  const recordByRequest = new WeakMap();
  const requestByDiagnostic = new WeakMap();
  const records = [];
  const scopes = [];
  const waiters = new Set();
  const frameIDs = new WeakMap();
  let frameIDSequence = 0;
  let revision = 0;

  const notify = () => {
    for (const resolve of [...waiters]) resolve();
  };

  const addCorrelationError = (request, event, message) => {
    const route = routeFrom(request);
    if (!route) return;
    for (const scope of scopes) {
      if (!belongsToScope(request, scope)) continue;
      scope.correlationErrors.push({
        kind: 'request-capture-correlation',
        event,
        origin: route.origin,
        path: route.path,
        method: typeof request.method === 'function' ? request.method() : null,
        observedAt: Date.now(),
        objectMatch: false,
        message,
      });
    }
    notify();
  };

  const assertScope = (scope) => {
    if (!scope || scope.owner !== ledger || !scopes.includes(scope)) {
      throw new TypeError('Native request ledger scope must come from this fixture ledger.');
    }
  };

  const requestBelongsToScope = (entry, scope) => (!scope.origin || entry.origin === scope.origin) &&
    pathBelongsToProject(entry.path, scope.projectPath);

  const observedExplorerIDs = (scope) => [...new Set(records
    .filter(entry => requestBelongsToScope(entry, scope))
    .flatMap(entry => {
      const prefix = `${scope.projectPath}/`;
      if (!entry.path.startsWith(prefix)) return [];
      const encodedID = entry.path.slice(prefix.length).split('/')[0];
      if (!encodedID) return [];
      try { return [decodeURIComponent(encodedID)]; }
      catch { return []; }
    }))];

  const selectedEntries = (scope) => {
    const explorerPath = typeof scope.explorer === 'string' && scope.explorer.trim()
      ? `${scope.projectPath}/${encodeURIComponent(scope.explorer)}`
      : undefined;
    const projectEntries = records.filter(entry => requestBelongsToScope(entry, scope));
    const selected = projectEntries.filter(entry =>
      (entry.method === 'POST' && entry.path === scope.projectPath) ||
      (explorerPath && (entry.path === explorerPath || entry.path.startsWith(`${explorerPath}/`))));
    const selectedSet = new Set(selected);
    return { projectEntries, selected, excluded: projectEntries.filter(entry => !selectedSet.has(entry)) };
  };

  const drainEvidenceFor = (entries, allProjectEntries, timeoutMs, startedAt, deadlineAt) => {
    const pending = entries.filter(entry => !terminalEvents.has(entry.terminalEvent));
    if (!pending.length) return [];
    return [{
      status: 'timed-out',
      startedAt,
      deadlineAt,
      timeoutMs,
      reason: 'Native Playwright request did not emit requestfinished or requestfailed before the bounded drain deadline.',
      unresolvedRequests: pending.map(entry => ({
        index: allProjectEntries.indexOf(entry),
        requestId: entry.requestId,
        browserRequestId: entry.browserRequestId,
        method: entry.method,
        path: entry.path,
        status: entry.status ?? null,
        failure: entry.failure ?? null,
      })),
    }];
  };

  const snapshot = (scope) => {
    assertScope(scope);
    const { projectEntries, selected, excluded } = selectedEntries(scope);
    const ownedDrainEvidence = scope.drainEvidence.flatMap(evidence => {
      const selectedBrowserIDs = new Set(selected.map(entry => entry.browserRequestId));
      const unresolvedRequests = (evidence.unresolvedRequests ?? []).filter(entry => selectedBrowserIDs.has(entry.browserRequestId));
      return unresolvedRequests.length ? [{ ...evidence, unresolvedRequests }] : [];
    });
    const excludedDrainEvidence = scope.drainEvidence.flatMap(evidence => {
      const excludedBrowserIDs = new Set(excluded.map(entry => entry.browserRequestId));
      const unresolvedRequests = (evidence.unresolvedRequests ?? []).filter(entry => excludedBrowserIDs.has(entry.browserRequestId));
      return unresolvedRequests.length ? [{ ...evidence, unresolvedRequests }] : [];
    });
    const ledgerRequests = selected.map(entry => {
      const terminalEvent = terminalEvents.has(entry.terminalEvent) ? entry.terminalEvent : null;
      const state = terminalEvent === 'requestfinished' ? 'finished' : terminalEvent === 'requestfailed' ? 'failed' : 'pending';
      const classifiedDiagnostic = serializedRequest(entry);
      return {
        requestId: entry.requestId,
        browserRequestId: entry.browserRequestId,
        method: entry.method,
        origin: entry.origin,
        path: entry.path,
        status: entry.status ?? null,
        failure: entry.failure ?? null,
        terminalEvent,
        state,
        complete: state !== 'pending' && hasTerminalPayload(entry),
        pageId: entry.pageId ?? null,
        frameId: entry.frameId ?? null,
        frameIsMainFrame: entry.frameIsMainFrame ?? null,
        frameIdentityStatus: entry.frameIdentityStatus ?? 'unavailable',
        navigationSequenceAtStart: entry.navigationSequenceAtStart ?? null,
        ...Object.fromEntries(['expected', 'canceled', 'cancellationReason', 'expectedCancellation',
          'expectedHttpFailure', 'expectedInjectedFault', 'injectedFault', 'injectedAction', 'injectedRequestId',
          'injectedStatus', 'replacement', 'binding', 'requestTimeline']
          .filter(key => classifiedDiagnostic[key] !== undefined)
          .map(key => [key, safeClone(classifiedDiagnostic[key])])),
        nativeEventChronology: safeClone(entry.nativeEventChronology),
      };
    });
    const incompleteRequests = ledgerRequests.filter(entry => !entry.complete);
    const hasCreateRequest = selected.some(entry => entry.method === 'POST' && entry.path === scope.projectPath);
    const explorerPath = typeof scope.explorer === 'string' && scope.explorer.trim()
      ? `${scope.projectPath}/${encodeURIComponent(scope.explorer)}`
      : undefined;
    const hasExplorerRequest = Boolean(explorerPath) && selected.some(entry =>
      entry.path === explorerPath || entry.path.startsWith(`${explorerPath}/`));
    const observedIDs = observedExplorerIDs(scope);
    const explorerSelection = scope.explorer ? 'selected' : observedIDs.length > 1 ? 'ambiguous' :
      observedIDs.length === 1 ? 'unresolved' : 'not-observed';
    const complete = Boolean(explorerPath) && (!scope.requireProjectCreate || hasCreateRequest) && hasExplorerRequest &&
      selected.length > 0 && incompleteRequests.length === 0 && ownedDrainEvidence.length === 0 &&
      scope.correlationErrors.length === 0;

    return {
      nativeRequests: selected.map(serializedRequest),
      nativeRequestDrainEvidence: safeClone(ownedDrainEvidence),
      excludedNativeRequests: excluded.map(serializedRequest),
      excludedNativeRequestDrainEvidence: safeClone(excludedDrainEvidence),
      nativeRequestCorrelationErrors: safeClone(scope.correlationErrors),
      nativeRequestTerminalLedger: {
        scope: scope.requireProjectCreate
          ? 'fresh Explorer native routes and its project-scoped create request'
          : 'selected Explorer native routes in the preseeded fresh fixture project',
        project: scope.project,
        explorer: scope.explorer ?? null,
        automaticScope: scope.automatic,
        projectCreateRequestRequired: scope.requireProjectCreate,
        explorerSelection,
        observedExplorerCount: observedIDs.length,
        applicable: Boolean(scope.explorer) || observedIDs.length > 0,
        complete,
        counts: {
          total: ledgerRequests.length,
          finished: ledgerRequests.filter(entry => entry.state === 'finished').length,
          failed: ledgerRequests.filter(entry => entry.state === 'failed').length,
          pending: incompleteRequests.length,
        },
        requests: ledgerRequests,
      },
      incompleteRequests,
    };
  };

  const ledger = {
    frameIdentityForFrame(frame, page, pageId = 'playwright-page-1') {
      if (!page || typeof page.mainFrame !== 'function' || typeof pageId !== 'string' || !pageId.trim()) {
        throw new TypeError('Native frame identity requires a Playwright Page and non-empty page ID.');
      }
      if (!frame || typeof frame !== 'object') {
        return { pageId, frameId: null, frameIsMainFrame: null, frameIdentityStatus: 'unavailable' };
      }
      let frameId = frameIDs.get(frame);
      if (!frameId) {
        frameId = `playwright-frame-${++frameIDSequence}`;
        frameIDs.set(frame, frameId);
      }
      return {
        pageId,
        frameId,
        frameIsMainFrame: frame === page.mainFrame(),
        frameIdentityStatus: 'exact',
      };
    },

    frameIdentityForRequest(request, page, pageId = 'playwright-page-1') {
      try {
        return ledger.frameIdentityForFrame(request?.frame?.(), page, pageId);
      } catch {
        return ledger.frameIdentityForFrame(null, page, pageId);
      }
    },

    openScope({ project, origin, automatic = false, requireProjectCreate = true, defaultExplorer } = {}) {
      if (typeof project !== 'string' || !project.trim()) {
        throw new TypeError('Native request ledger scope needs a project ID.');
      }
      if (origin !== undefined) {
        let parsedOrigin;
        try { parsedOrigin = new URL(origin).origin; }
        catch { throw new TypeError('Native request ledger origin must be an absolute HTTP(S) origin.'); }
        if (!['http:', 'https:'].includes(new URL(origin).protocol) || parsedOrigin !== origin) {
          throw new TypeError('Native request ledger origin must be an absolute HTTP(S) origin without a path, query, or fragment.');
        }
      }
      if (typeof automatic !== 'boolean' || typeof requireProjectCreate !== 'boolean') {
        throw new TypeError('Native request ledger scope flags must be booleans.');
      }
      if (defaultExplorer !== undefined && (typeof defaultExplorer !== 'string' || !defaultExplorer.trim())) {
        throw new TypeError('Native request ledger default Explorer must be a non-empty string when supplied.');
      }
      if (defaultExplorer !== undefined && !automatic) {
        throw new TypeError('Only an automatic Basic scope may declare its preseeded default Explorer.');
      }
      const scope = {
        owner: ledger,
        project,
        origin,
        automatic,
        requireProjectCreate,
        defaultExplorer,
        projectPath: projectExplorerCollectionPath(project),
        explorer: undefined,
        drainEvidence: [],
        correlationErrors: [],
      };
      scopes.push(scope);
      return scope;
    },

    recordRequest(request, details = {}) {
      const route = routeFrom(request);
      const matchingScopes = route ? scopes.filter(candidate => belongsToScope(request, candidate)) : [];
      if (!matchingScopes.length) return undefined;
      if (recordByRequest.has(request)) {
        for (const scope of matchingScopes) {
          scope.correlationErrors.push({
            kind: 'request-capture-correlation', event: 'request', origin: route.origin, path: route.path,
            method: details.method ?? null, observedAt: Date.now(), objectMatch: false,
            message: 'Playwright emitted a duplicate request event for the same Request object.',
          });
        }
        revision += 1;
        notify();
        return recordByRequest.get(request);
      }
      const entry = {
        ...details,
        url: details.url ?? `${route.origin}${route.path}`,
        origin: route.origin,
        path: route.path,
        startedAt: details.startedAt ?? Date.now(),
        status: undefined,
        failure: undefined,
        terminalEvent: undefined,
        fixtureDiagnostic: undefined,
        nativeEventChronology: [],
      };
      appendEvent(entry, 'request', entry.startedAt);
      recordByRequest.set(request, entry);
      records.push(entry);
      revision += 1;
      notify();
      return entry;
    },

    recordResponse(request, { status, serverRequestId, observedAt = Date.now() } = {}) {
      const entry = recordByRequest.get(request);
      if (!entry) {
        addCorrelationError(request, 'response', 'Playwright response did not refer to an exact fixture-captured Request object.');
        return undefined;
      }
      entry.status = status;
      if (serverRequestId !== undefined) entry.serverRequestId = serverRequestId;
      entry.responseReceivedAt = observedAt;
      appendEvent(entry, 'response', observedAt, { status });
      revision += 1;
      notify();
      return entry;
    },

    recordFinished(request, { observedAt = Date.now() } = {}) {
      const entry = recordByRequest.get(request);
      if (!entry) {
        addCorrelationError(request, 'requestfinished', 'Playwright requestfinished did not refer to an exact fixture-captured Request object.');
        return undefined;
      }
      appendEvent(entry, 'requestfinished', observedAt);
      revision += 1;
      notify();
      return entry;
    },

    recordFailed(request, { failure, observedAt = Date.now() } = {}) {
      const entry = recordByRequest.get(request);
      if (!entry) {
        addCorrelationError(request, 'requestfailed', 'Playwright requestfailed did not refer to an exact fixture-captured Request object.');
        return undefined;
      }
      entry.failure = failure ?? null;
      appendEvent(entry, 'requestfailed', observedAt, { failure: entry.failure });
      revision += 1;
      notify();
      return entry;
    },

    linkDiagnostic(request, diagnostic) {
      const entry = recordByRequest.get(request);
      if (!entry) {
        addCorrelationError(request, 'diagnostic', 'Fixture diagnostic did not refer to an exact fixture-captured Request object.');
        return false;
      }
      entry.fixtureDiagnostic = diagnostic;
      return true;
    },

    associateDiagnostic(request, diagnostic) {
      if (!request || typeof request !== 'object' || !diagnostic || typeof diagnostic !== 'object') {
        throw new TypeError('Fixture diagnostic association requires the exact Request and diagnostic objects.');
      }
      requestByDiagnostic.set(diagnostic, request);
    },

    linkProjectedDiagnostics(captured, projected) {
      if (!Array.isArray(captured) || !Array.isArray(projected) || captured.length !== projected.length) {
        throw new TypeError('Fixture network policy projection must preserve captured diagnostic count.');
      }
      let linked = 0;
      for (let index = 0; index < captured.length; index += 1) {
        const request = requestByDiagnostic.get(captured[index]);
        if (request && ledger.linkDiagnostic(request, projected[index])) linked += 1;
      }
      return linked;
    },

    async flush(scope, { explorer, timeoutMs = 5_000 } = {}) {
      assertScope(scope);
      if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
        throw new RangeError('Native request terminal drain timeout must be a finite positive number of milliseconds.');
      }
      if (explorer !== undefined && (typeof explorer !== 'string' || !explorer.trim())) {
        throw new TypeError('Native request ledger Explorer ID must be a non-empty string when supplied.');
      }
      if (scope.explorer && explorer && scope.explorer !== explorer) {
        throw new Error('Native request ledger scope cannot change its selected Explorer.');
      }
      if (explorer) scope.explorer = explorer;
      const startedAt = Date.now();
      const deadlineAt = startedAt + timeoutMs;
      while (true) {
        // The scope owns the complete project prefix while draining. A sibling Explorer is
        // retained as excluded context, but it must not keep the selected Explorer's ledger
        // incomplete when its requests are already terminal.
        const pending = selectedEntries(scope).projectEntries.filter(entry => !terminalEvents.has(entry.terminalEvent));
        if (!pending.length) break;
        const remainingMs = deadlineAt - Date.now();
        if (remainingMs <= 0) break;
        await new Promise(resolve => {
          let settled = false;
          const done = () => {
            if (settled) return;
            settled = true;
            clearTimeout(timer);
            waiters.delete(done);
            resolve();
          };
          const timer = setTimeout(done, remainingMs);
          waiters.add(done);
        });
      }
      const { projectEntries } = selectedEntries(scope);
      const unresolved = projectEntries.filter(entry => !terminalEvents.has(entry.terminalEvent));
      if (unresolved.length) {
        const known = new Set(scope.drainEvidence.flatMap(evidence =>
          (evidence.unresolvedRequests ?? []).map(entry => entry.browserRequestId)));
        const fresh = unresolved.filter(entry => !known.has(entry.browserRequestId));
        if (fresh.length) scope.drainEvidence.push(...drainEvidenceFor(fresh, projectEntries, timeoutMs, startedAt, deadlineAt));
      }
      scope.lastFlushedRevision = revision;
      return snapshot(scope);
    },

    async finalizeScope({ project, explorer, timeoutMs = 5_000 } = {}) {
      if (typeof project !== 'string' || !project.trim()) {
        throw new TypeError('Native request ledger finalization needs a project ID.');
      }
      const candidates = scopes.filter(scope => scope.project === project);
      const explicit = candidates.filter(scope => !scope.automatic);
      const scope = (typeof explorer === 'string' && explorer.trim()
        ? explicit.find(candidate => candidate.explorer === explorer)
        : undefined) ?? explicit.at(-1) ?? candidates.find(candidate => candidate.automatic);
      if (!scope) return null;
      let selectedExplorer = typeof explorer === 'string' && explorer.trim() ? explorer : scope.explorer;
      if (!selectedExplorer && scope.automatic && scope.defaultExplorer) {
        const observedIDs = observedExplorerIDs(scope);
        if (observedIDs.length === 1 && observedIDs[0] === scope.defaultExplorer) {
          selectedExplorer = scope.defaultExplorer;
        }
      }
      if (selectedExplorer && scope.explorer && scope.explorer !== selectedExplorer) {
        throw new Error('Native request ledger finalization cannot change its selected Explorer.');
      }
      if (selectedExplorer && !scope.explorer) scope.explorer = selectedExplorer;
      if (scope.lastFlushedRevision !== revision) {
        await ledger.flush(scope, { explorer: scope.explorer, timeoutMs });
      }
      return snapshot(scope);
    },

    snapshot,

    snapshotAll() {
      return scopes.map(snapshot);
    },
  };

  return ledger;
}

export function projectFixtureNetworkDiagnostics({ report, ledger, faults }) {
  if (!report || !Array.isArray(report.network) || !ledger?.linkProjectedDiagnostics || !Array.isArray(faults)) {
    throw new TypeError('Fixture network projection requires a report, fixture ledger, and fault list.');
  }
  const captured = report.network;
  const projected = applyInjectedFaultPolicy(captured, faults);
  report.network = projected;
  if (projected.length !== captured.length) {
    const projectionError = {
      kind: 'request-capture-correlation',
      event: 'network-policy-projection',
      objectMatch: false,
      expected: false,
      message: 'Network policy projection changed the diagnostic count, so fixture Request identities could not be retained.',
      sourceCount: captured.length,
      projectedCount: projected.length,
    };
    report.errors ??= [];
    report.errors.push(projectionError);
    recordCheck(report, 'correctness', 'fixture network policy projection preserves captured Request associations', false, projectionError);
    return projected;
  }
  ledger.linkProjectedDiagnostics(captured, projected);
  return projected;
}

export async function finalizeFixtureNativeRequestReport({ report, ledger, project, explorer, timeoutMs = 5_000 }) {
  if (!report || !ledger?.finalizeScope || typeof project !== 'string' || !project.trim()) {
    throw new TypeError('Fixture native request finalization requires a report, fixture ledger, and project ID.');
  }
  const selectedExplorer = explorer ?? report.target?.explorer ?? report.explorer;
  const snapshot = await ledger.finalizeScope({ project, explorer: selectedExplorer, timeoutMs });
  if (!snapshot) return null;

  report.nativeRequests = snapshot.nativeRequests;
  report.nativeRequestDrainEvidence = snapshot.nativeRequestDrainEvidence;
  report.excludedNativeRequests = snapshot.excludedNativeRequests;
  report.excludedNativeRequestDrainEvidence = snapshot.excludedNativeRequestDrainEvidence;
  report.nativeRequestCorrelationErrors = snapshot.nativeRequestCorrelationErrors;
  report.nativeRequestTerminalLedger = snapshot.nativeRequestTerminalLedger;
  const navigationTimings = Array.isArray(report.navigationTimings) ? report.navigationTimings : [];
  for (const request of report.nativeRequests) {
    request.sameFrameNavigationEventsAfterStart = sameFrameNavigationEventsAfterStart(request, navigationTimings);
  }
  for (const request of report.nativeRequestTerminalLedger.requests) {
    request.sameFrameNavigationEventsAfterStart = sameFrameNavigationEventsAfterStart(request, navigationTimings);
  }
  if (snapshot.nativeRequestTerminalLedger.automaticScope && snapshot.nativeRequestTerminalLedger.applicable) {
    const complete = snapshot.nativeRequestTerminalLedger.complete === true;
    recordCheck(report, 'correctness', 'basic fixture native request ledger observed a complete selected Explorer lifecycle', complete, {
      project, explorer: snapshot.nativeRequestTerminalLedger.explorer,
      explorerSelection: snapshot.nativeRequestTerminalLedger.explorerSelection,
      observedExplorerCount: snapshot.nativeRequestTerminalLedger.observedExplorerCount,
      requestCount: snapshot.nativeRequestTerminalLedger.counts.total,
      finished: snapshot.nativeRequestTerminalLedger.counts.finished,
      failed: snapshot.nativeRequestTerminalLedger.counts.failed,
      pending: snapshot.nativeRequestTerminalLedger.counts.pending,
      projectCreateRequestRequired: snapshot.nativeRequestTerminalLedger.projectCreateRequestRequired,
    });
  }
  if (snapshot.nativeRequestCorrelationErrors.length) {
    report.errors ??= [];
    report.errors.push(...snapshot.nativeRequestCorrelationErrors.map(entry => ({ ...entry, expected: false })));
    recordCheck(report, 'correctness', 'fixture native request events correlate to exact Playwright Request objects', false,
      { errors: snapshot.nativeRequestCorrelationErrors });
  }
  return snapshot;
}
