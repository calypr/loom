import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertCompletePopulationRowsMatchOracle } from '../helpers/cda-row-oracle.mjs';
import { assertReopenedProposalAfterCancel } from '../helpers/proposal-reopen-binding.mjs';
import { relatedSourceProposalCandidate } from '../helpers/related-source-capture.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { assertCdaNoAuthRuntime } from '../helpers/cda-no-auth-runtime.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';
import { changedConstructionOperationSteps } from '../helpers/construction-operation-diff.mjs';
import { relatedChoiceStageContext } from '../helpers/related-choice-stage-context.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { captureBuilderObservation } from '../helpers/fast-fixtures.mjs';
import {
  builderJourneyTraceBindingFor,
  createBuilderReadMetadataRegistry,
  nativeRequestLedgerFrom,
  writeAndReplayBuilderJourneyTrace,
} from '../helpers/builder-journey-trace.mjs';

export const groupEditRawFieldsDisclosureSelector = '[data-testid="feature-catalog-raw-fields"]';
export const groupEditRawFieldsSummarySelector = `${groupEditRawFieldsDisclosureSelector} > summary`;
export const groupEditRenderBudgetMs = 5000;
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
export const isProtectedExplorerRequestPath = (pathname, explorerRoot, explorerId) => {
  const protectedPath = explorerRoot + '/' + encodeURIComponent(explorerId);
  return pathname === protectedPath || pathname.startsWith(protectedPath + '/');
};
export const groupEditRenderCheckpointNames = Object.freeze([
  'selected-raw-Specimen-reload',
  'expand-Specimen-Patient',
  'apply-expand-Specimen-Patient',
  'expand-Patient-Observation',
  'apply-expand-Patient-Observation',
  'group-by-Specimen-ID-count-rows',
  'apply-initial-Group',
  'reload-initial-Group',
  'edit-Group-to-count-distinct-before-column',
  'cancel-Group-edit-before-column',
  'reapply-Group-count-distinct-before-column',
  'apply-Group-edit-before-column',
  'reload-Group-edit-before-column',
  'automatic-Observation-status-RELATED_SOURCE-preview-after-Group-edit',
  'cancel-related-column-after-Group-edit',
  'reopened-Observation-status-RELATED_SOURCE-preview-before-Apply',
  'apply-related-column-after-Group-edit',
  'reload-related-column-after-Group-edit',
  'edit-Group-back-to-count-rows-with-related-column',
  'cancel-Group-round-trip-edit',
  'reapply-Group-count-rows-with-related-column',
  'apply-Group-round-trip-with-related-column',
  'reload-Group-round-trip-with-related-column',
  'automatic-remove-RELATED_SOURCE-preview-cancel-target',
  'cancel-remove-RELATED_SOURCE',
  'reopened-remove-RELATED_SOURCE-preview-before-Apply',
  'apply-remove-RELATED_SOURCE-to-restore-Group',
  'reload-final-Group-source-restoration',
]);
export const savedColumnLabelsForIds = (document, columnIds) => {
  assert(Array.isArray(document?.columns) && Array.isArray(document?.construction?.steps),
    'Saved Builder document must include source columns and construction steps.');
  assert(Array.isArray(columnIds), 'Saved Group keys must be an array of column IDs.');
  const labelsById = new Map();
  const addLabel = (column, source) => {
    const id = column?.columnId ?? column?.id;
    const label = column?.label;
    if (typeof id !== 'string' || id.length === 0 || typeof label !== 'string' || label.length === 0) return;
    if (labelsById.has(id)) {
      assert.equal(labelsById.get(id), label,
        `Saved Builder column ID ${id} has conflicting labels across ${source}.`);
    } else {
      labelsById.set(id, label);
    }
  };
  for (const column of document.columns) addLabel(column, 'source columns and construction outputs');
  for (const step of document.construction.steps) {
    for (const output of step.outputs ?? []) addLabel(output, 'source columns and construction outputs');
  }
  return columnIds.map(id => {
    assert(typeof id === 'string' && id.length > 0, 'Saved Group key must retain its actual column ID.');
    const label = labelsById.get(id);
    assert(label, `Saved Group key column ID ${id} must resolve in saved source columns or construction outputs.`);
    return label;
  });
};
const cdaJourneyReadOnlyRoutes = new Map([
  ['GET /builder', { kind: 'builder-read', reason: 'The journey model records saved workspace snapshots on Reload actions; Builder reads supply those snapshots.' }],
  ['GET /capability', { kind: 'capability-read', reason: 'The journey model does not own capability discovery.' }],
  ['GET /row-definition-choices', { kind: 'row-definition-choices-read', reason: 'The journey model does not own row-definition chooser inventory.' }],
  ['POST /population-routes', { kind: 'population-route-read', reason: 'The journey model does not own the selected population-route inventory.' }],
  ['POST /construction-capabilities', { kind: 'construction-capability-read', reason: 'The journey model does not own construction capability discovery.' }],
  ['POST /semantic-inventory', { kind: 'semantic-inventory-read', reason: 'The journey model does not own semantic field inventory.' }],
  ['POST /construction-choices', { kind: 'construction-choice-read', reason: 'The journey model does not own field chooser inventory.' }],
  ['POST /configured-column-context', { kind: 'configured-column-context-read', reason: 'The journey model does not own configured-column context reads.' }],
  ['POST /related-field-choices', { kind: 'related-field-choice-read', reason: 'The journey model does not own related-field chooser inventory.' }],
  ['POST /related-expand-choices', { kind: 'related-expand-choice-read', reason: 'The journey model does not own related-expansion choice inventory.' }],
  ['POST /related-expand-contributors', { kind: 'related-expand-contributor-read', reason: 'The journey model does not own related-expansion contributor inventory.' }],
  ['POST /construction-inputs', { kind: 'construction-input-read', reason: 'The journey model does not own construction-input discovery.' }],
  ['POST /construction-category-discoveries', { kind: 'construction-category-discovery-read', reason: 'The journey model does not own construction category discovery.' }],
  ['POST /preview', { kind: 'table-preview-read', reason: 'The journey model records proposal transitions; read-only table previews supply rendered Builder rows.' }],
  ['POST /reconcile', { kind: 'draft-reconcile-read', reason: 'The journey model does not own draft reconciliation reads.' }],
]);

export function recordGroupEditActionToRender(report, name, startedAt, details = {}, now = Date.now) {
  const durationMs = now() - startedAt;
  assert(Number.isFinite(durationMs) && durationMs >= 0, `${name} must have a valid action-to-render duration`);
  assert(durationMs <= groupEditRenderBudgetMs,
    `${name} took ${durationMs} ms (limit ${groupEditRenderBudgetMs} ms)`);
  report.cases.push({ name, durationMs, ...details });
  return durationMs;
}

export function groupEditRenderCheckpointSummary(cases) {
  const expected = new Set(groupEditRenderCheckpointNames);
  const byName = new Map();
  for (const entry of cases) {
    const matches = byName.get(entry.name) ?? [];
    matches.push(entry);
    byName.set(entry.name, matches);
  }
  const missing = groupEditRenderCheckpointNames.filter(name => !byName.has(name));
  const duplicates = groupEditRenderCheckpointNames.filter(name => byName.get(name)?.length !== 1 && byName.has(name));
  const unexpected = cases.filter(entry => !expected.has(entry.name)).map(({ name }) => name);
  const overBudget = cases.filter(({ durationMs }) => !Number.isFinite(durationMs) || durationMs < 0 || durationMs > groupEditRenderBudgetMs)
    .map(({ name, durationMs }) => ({ name, durationMs }));
  const checkpoints = groupEditRenderCheckpointNames.map(name => ({
    name,
    durationMs: byName.get(name)?.[0]?.durationMs ?? null,
  }));
  const passed = cases.length === groupEditRenderCheckpointNames.length && missing.length === 0 &&
    duplicates.length === 0 && unexpected.length === 0 && overBudget.length === 0;
  return {
    status: passed ? 'passed' : 'failed',
    budgetMs: groupEditRenderBudgetMs,
    checkpointCount: cases.length,
    requiredCheckpointCount: groupEditRenderCheckpointNames.length,
    checkpoints,
    missing,
    duplicates,
    unexpected,
    overBudget,
    maximumDurationMs: Math.max(0, ...cases.map(({ durationMs }) => Number.isFinite(durationMs) ? durationMs : 0)),
  };
}

export async function runGroupEditBeforeRelatedColumnBrowserWorkflow({ page, cda }) {
const project = cda.project;
assert(project, 'CDA fixture must provide the isolated project');
const env = cda.env ?? {};
const target = cda.target ?? {};
let explorer = cda.explorer;
const requestedExplorerName = explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = target.arangoContainer ?? env.LOOM_ARANGO_CONTAINER;
const generation = target.fixtureGeneration;
assert(typeof generation === 'string' && generation.length > 0 && generation.trim() === generation,
  'CDA target must provide an explicit fixtureGeneration string');
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
assert.notEqual(explorer, protectedExplorer);


let explorerRoot;
let base;
const root = `/api/v1/projects/${project}/explorers`;
const click = (_page, ...args) => cda.click(...args);
const selectOption = (_page, ...args) => cda.selectOption(...args);
const fill = (_page, ...args) => cda.fill(...args);
const navigate = (_page, ...args) => cda.navigate(...args);
const browserEval = (_page, callback, args) => cda.inspect(callback, args);
const waitForBrowser = (_page, predicate, timeoutOrArgs = 5000, args) => {
  const timeout = Array.isArray(timeoutOrArgs) ? 5000 : timeoutOrArgs;
  const waitArgs = Array.isArray(timeoutOrArgs) ? timeoutOrArgs : args;
  return cda.wait(predicate, waitArgs, timeout);
};
const waitForControl = (_page, selector, { timeout = 5000, enabled = false, hidden = false } = {}) => cda.wait(
  ({ selector: targetSelector, enabled: requireEnabled, hidden: waitHidden }) => {
    const controls = [...document.querySelectorAll(targetSelector)];
    if (controls.length > 1) throw new Error(`Expected one control for ${targetSelector}, found ${controls.length}`);
    const control = controls[0];
    if (waitHidden) return !control || !control.getClientRects().length;
    return Boolean(control && control.getClientRects().length &&
      (!requireEnabled || (!control.disabled && control.getAttribute('aria-disabled') !== 'true')));
  }, { selector, enabled, hidden }, timeout,
);
const report = {
  status: 'running', explorer, project, generation, protectedExplorer,
  scope: { apiOrigin, uiOrigin, browserApiTransport: 'same-origin UI /api proxy',
    rawOracle: 'project + dataset generation + explicit selected Specimen ID' },
  evidence, target, ownedApiRuntimeProof: null, oracleQueryAttempts: [], cases: [], errors: [], requests: [], nativeRequests: [],
  protectedExplorerUntouched: true,
  started: new Date().toISOString(),
};
let builder, outputId, selection, source, selectedPopulationRoute;
const builderSnapshotsByDraft = new Map();
const builderReadMetadata = createBuilderReadMetadataRegistry();
const journeyTraceBinding = builderJourneyTraceBindingFor(cda.scenarioID, cda.caseName);
const journeyTrace = {
  name: journeyTraceBinding.name,
  model: journeyTraceBinding.model,
  initialObservation: null,
  initialPreview: null,
  actions: [],
  unmodeledEvents: [],
};
let mountEpoch = 0;
let documentTimeOrigin = null;
let journeyTraceStarted = false;
const capturedProposalBrowserRequestIds = new Set();
const capturedApplyBrowserRequestIds = new Set();
const proposalMounts = new Map();
const proposalRequestEvidence = new Map();
const applyMounts = new Map();
const proposalSettlementMounts = new Map();
const proposalSettlementEvidence = new Map();
const documentEpochByTimeOrigin = new Map();
const settlementMountWaiters = new Map();
const oracleQueries = [];
const recordLifecycleCheck = (dimension, name, passed, checkEvidence = {}) =>
  cda.check(dimension, name, passed, checkEvidence);

const api = async (path, body) => {
  assert(!path.includes(protectedExplorer), `Refusing to address protected Explorer ${protectedExplorer}`);
  const requestId = `group-edit-column-${randomUUID()}`;
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, requestId, body, status: response.status, response: value });
  if (path.endsWith('/builder')) {
    builderReadMetadata.record(value, {
      path,
      requestId,
      status: response.status,
      draftVersion: value.draftVersion,
      draftDigest: value.draftDigest,
    });
    builderSnapshotsByDraft.set(`${String(value.draftVersion)}\u0000${value.draftDigest}`, structuredClone(value));
  }
  assert(response.ok, `${response.status} ${JSON.stringify(value)}`);
  return value;
};
const identity = (state) => ({ snapshotToken: state.catalog.snapshotToken,
  expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest });
const doc = (state = builder) => state.workspace.documents.find((item) => item.output.id === outputId);
const constructionSteps = state => doc(state).construction?.steps ?? [];
const builderStateForProposal = (entry, fallbackState) => {
  const body = entry.request ?? {};
  const version = body.expectedDraftVersion ?? body.draftVersion;
  const digest = body.expectedDraftDigest ?? body.draftDigest;
  const saved = builderSnapshotsByDraft.get(`${String(version)}\u0000${digest}`);
  if (saved) return saved;
  if (fallbackState && String(fallbackState.draftVersion) === String(version) && fallbackState.draftDigest === digest) {
    return fallbackState;
  }
  throw new Error(`Observed proposal ${entry.browserRequestId} has no matching actual /builder snapshot for its draft CAS.`);
};
const proposalCaptureKind = (entry, state) => {
  const body = entry.request ?? {};
  const steps = [
    ...(body.candidateConstruction?.steps ?? []),
    ...(entry.response?.candidateConstruction?.steps ?? []),
  ];
  const changedStep = steps.find(step => step.id === body.changedStepId);
  const removedRelatedStep = (body.removeStepIds ?? []).some(stepId =>
    constructionSteps(state).some(step => step.id === stepId && step.operation?.kind === 'RELATED_SOURCE'));
  if (removedRelatedStep || changedStep?.operation?.kind === 'RELATED_SOURCE') return 'relatedSource';
  if (changedStep?.operation?.kind === 'RELATED_EXPAND') return 'expandRows';
  if (changedStep?.operation?.kind === 'GROUP') return 'group';
  if (!body.changedStepId) {
    if (relatedSourceProjection(state) === null && steps.some(step => step.operation?.kind === 'RELATED_SOURCE')) {
      return 'relatedSource';
    }
    if (groupProjection(state) === null && steps.some(step => step.operation?.kind === 'GROUP')) return 'group';
    if (steps.some(step => step.operation?.kind === 'RELATED_EXPAND')) return 'expandRows';
  }
  throw new Error(`Observed proposal ${entry.browserRequestId} has no modeled Group, row-expansion, or RELATED_SOURCE candidate.`);
};
const readRenderedProposalPanel = async () => browserEval(page, () => {
  const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
  return { status: panel?.dataset.proposalStatus ?? null,
    id: panel?.dataset.proposalId ?? null,
    text: panel?.innerText ?? '',
    rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => ({ text: cell.innerText, title: cell.title }))) };
});
const recordAction = (name, startedAt, details = {}) =>
  recordGroupEditActionToRender(report, name, startedAt, details);
const command = async (commands) => {
  await api(base + '/commands', {
    ...identity(builder), commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10, commands,
  });
  builder = await api(base + '/builder');
};
const rawQuery = (query) => {
  assert(query.includes(JSON.stringify(project)), 'Raw oracle query must scope to the CDA project');
  assert(query.includes(JSON.stringify(generation)), 'Raw oracle query must scope to the CDA generation');
  oracleQueries.push(query);
  const timeoutMs = 30000;
  const querySha256 = createHash('sha256').update(query).digest('hex');
  const invocation = buildArangoShellInvocation({
    container: arangoContainer,
    script: `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
    database: 'loom_dev',
  });
  const startedAt = Date.now();
  const result = spawnSync(invocation.command, invocation.args, { encoding: 'utf8', timeout: timeoutMs });
  const attempt = {
    querySha256,
    project,
    generation,
    database: 'loom_dev',
    timeoutMs,
    elapsedMs: Date.now() - startedAt,
    status: Number.isInteger(result.status) ? result.status : null,
    signal: typeof result.signal === 'string' ? result.signal : null,
    errorCode: typeof result.error?.code === 'string' ? result.error.code : null,
    stdoutBytes: Buffer.byteLength(result.stdout ?? ''),
    stderrBytes: Buffer.byteLength(result.stderr ?? ''),
  };
  report.oracleQueryAttempts.push(attempt);
  assert.equal(result.status, 0, `Raw CDA oracle process failed: ${JSON.stringify(attempt)}`);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const assertSourceBinding = (state, route) => {
  assert.equal(state.catalog.generation, source.generation);
  assert.equal(state.catalog.authorizationScopeDigest, selection.scopeDigest);
  const document = doc(state);
  assert.equal(document.population.selectionRevisionId, selection.id);
  assert.deepEqual(document.population.route, route);
  assert.equal(document.rootResourceType, 'Specimen');
  return document;
};
const currentMount = async () => {
  const actualTimeOrigin = await page.evaluate(() => performance.timeOrigin);
  assert(Number.isFinite(actualTimeOrigin) && actualTimeOrigin > 0,
    'Builder journey must read the current document performance.timeOrigin from the page.');
  if (!documentEpochByTimeOrigin.has(actualTimeOrigin)) {
    documentEpochByTimeOrigin.set(actualTimeOrigin, documentEpochByTimeOrigin.size);
  }
  mountEpoch = documentEpochByTimeOrigin.get(actualTimeOrigin);
  documentTimeOrigin = actualTimeOrigin;
  return { mountEpoch, documentTimeOrigin };
};
const captureCurrentMount = () => currentMount()
  .then(mount => ({ mount }), error => ({ error }));
const awaitCapturedMount = async (mounts, browserRequestId) => {
  const captured = mounts.get(browserRequestId);
  assert(captured, `Browser request ${browserRequestId} must retain its request-time mount observation.`);
  const result = await captured;
  if (result.error) throw result.error;
  return result.mount;
};
const awaitSettlementMount = async browserRequestId => {
  if (!proposalSettlementMounts.has(browserRequestId)) {
    return new Promise((resolve, reject) => {
      settlementMountWaiters.set(browserRequestId, { resolve, reject });
    });
  }
  const captured = proposalSettlementMounts.get(browserRequestId);
  const result = await captured;
  if (result.error) throw result.error;
  return result.mount;
};
const awaitProposalSettlementEvidence = async browserRequestId => {
  const captured = proposalSettlementEvidence.get(browserRequestId);
  if (!captured) return { error: 'event-time response observation was not captured' };
  return captured;
};
const awaitProposalRequestEvidence = async browserRequestId => {
  const captured = proposalRequestEvidence.get(browserRequestId);
  if (!captured) return { error: 'request-start observation was not captured' };
  return captured;
};
const readRenderedSourceTable = async () => browserEval(page, () => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
  const headers = [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
    .map(cell => cell.innerText.replace(/\s+/g, ' ').trim());
  const rawRows = rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => ({
    text: cell.innerText.replace(/\s+/g, ' ').trim(),
    title: cell.getAttribute('title') ?? cell.querySelector('[title]')?.getAttribute('title') ?? null,
  })));
  return { headers, rawRows };
});
const readConstructionHistoryDOM = async () => browserEval(page, () =>
  [...document.querySelectorAll('button[data-testid^="construction-history-step-"]')].map(button => {
    const testId = button.getAttribute('data-testid');
    return {
      testId,
      stepId: testId?.replace(/^construction-history-step-/, '') ?? null,
      title: button.querySelector(':scope > span')?.innerText.replace(/\s+/g, ' ').trim() ?? '',
      historyText: button.getAttribute('title') ?? '',
      selected: button.getAttribute('aria-pressed') === 'true',
    };
  }));
const mountedGroupFromHistoryDOM = historySteps => {
  const row = historySteps.find(step => step.title === 'Group rows' || step.title.startsWith('Group by '));
  if (!row) return null;
  const keys = row.title === 'Group rows' ? [] : row.title.slice('Group by '.length).split(/,\s*/);
  const aggregate = row.historyText.includes('counts distinct ')
    ? 'COUNT_DISTINCT'
    : row.historyText.includes('counts rows') ? 'COUNT_ROWS' : null;
  assert(aggregate, 'Mounted Group history text must expose its rendered aggregate.');
  return {
    stepId: row.stepId,
    keys,
    aggregate,
    evidence: 'construction-history-dom',
    historyText: row.historyText,
  };
};
const mountedRelatedSourceFromDOM = ({ historySteps, renderedRows, state }) => {
  const row = historySteps.find(step => step.title === 'Related Observation' &&
    step.historyText.includes('Observation.status'));
  if (!row) return null;
  const sourceMatch = row.historyText.match(/\bfrom ([^\s]+)\.([^\s]+)(?:\s+via\s+|\s+as\s+)/);
  const labelMatch = row.historyText.match(/\bas (.+)\.$/);
  const rowIndex = historySteps.indexOf(row);
  const inputStepId = historySteps[rowIndex - 1]?.stepId ?? null;
  const related = relatedSourceProjection(state);
  assert(sourceMatch && labelMatch && inputStepId && related,
    'Mounted RELATED_SOURCE history DOM must expose its source, output label, and preceding Group input identity.');
  const visibleLabel = renderedRows.headers.find(header => header === labelMatch[1]);
  assert(visibleLabel, 'The mounted RELATED_SOURCE output label must appear in the rendered table header.');
  return {
    stepId: row.stepId,
    inputStepId,
    source: { resourceType: sourceMatch[1], path: sourceMatch[2] },
    outputColumnId: related.outputColumnId,
    route: structuredClone(related.route),
    visibleColumn: {
      id: related.outputColumnId,
      label: visibleLabel,
      visible: true,
      evidence: 'label-from-construction-history-and-table-header-dom; id-joined-from-raw-builder-projection',
    },
    historyText: row.historyText,
  };
};
const groupProjection = state => {
  const step = constructionSteps(state).find(candidate => candidate.operation?.kind === 'GROUP');
  if (!step) return null;
  const aggregates = step.operation.group?.aggregates ?? [];
  return {
    stepId: step.id,
    keys: (step.operation.group?.keys ?? []).map(key => key.inputColumnId),
    aggregate: aggregates[0]?.operation ?? null,
  };
};
const relatedSourceProjectionFromStep = step => {
  if (!step) return null;
  const related = step.operation?.relatedSource;
  const output = step.outputs?.find(candidate => candidate.id === related?.outputColumnId) ?? null;
  return {
    stepId: step.id,
    inputStepId: step.inputs?.find(input => input.kind === 'STEP_OUTPUT')?.stepId ?? null,
    source: structuredClone(related?.source ?? null),
    route: structuredClone(related?.route ?? null),
    choiceId: related?.choiceId ?? null,
    form: related?.form ?? null,
    contributorPolicy: related?.contributorRule?.policy ?? null,
    rowValuePolicy: related?.rowValuePolicy ?? null,
    outputColumnId: related?.outputColumnId ?? null,
    output: output ? structuredClone(output) : null,
  };
};
const relatedSourceProjection = state => {
  const step = constructionSteps(state).find(candidate => candidate.operation?.kind === 'RELATED_SOURCE');
  return relatedSourceProjectionFromStep(step);
};
const savedBuilderProjection = state => ({
  explorerId: explorer,
  group: groupProjection(state),
  relatedSource: relatedSourceProjection(state),
  workspace: structuredClone(state.workspace),
  outputId,
  draftVersion: state.draftVersion,
  draftDigest: state.draftDigest,
});
const nativeRequestRecord = entry => {
  const requestPathSuffix = entry.path.split('/authoring/v2/')[1];
  assert(requestPathSuffix, `Native request ${entry.browserRequestId} must retain its /authoring/v2/ route.`);
  const requestPath = `/${requestPathSuffix.replace(/^\/+/, '')}`;
  const request = entry.request === undefined ? null : structuredClone(entry.request);
  const captureId = typeof request?.captureId === 'string' && request.captureId.length > 0 ? request.captureId : undefined;
  return {
    method: entry.method,
    path: requestPath,
    index: entry.index,
    isMutation: entry.isMutation,
    requestId: entry.requestId ?? null,
    browserRequestId: entry.browserRequestId,
    startedAt: entry.startedAt,
    ...(captureId === undefined ? {} : { captureId }),
    request,
  };
};
const nativeResponseRecord = entry => {
  const body = entry.response === undefined ? null : structuredClone(entry.response);
  const previewBody = body?.preview && typeof body.preview === 'object' && !Array.isArray(body.preview)
    ? body.preview
    : body;
  const rawRowCount = previewBody?.rowCount ?? previewBody?.rows?.length;
  const numericRowCount = rawRowCount === undefined || rawRowCount === null ? null : Number(rawRowCount);
  const rawDraftVersion = body?.draftVersion;
  const numericDraftVersion = rawDraftVersion === undefined || rawDraftVersion === null
    ? null
    : Number(rawDraftVersion);
  const hasPreview = Boolean(previewBody && (
    previewBody.receiptId !== undefined || previewBody.outputId !== undefined ||
    previewBody.rowCount !== undefined || Array.isArray(previewBody.rows)
  ));
  return {
    ...(body && typeof body === 'object' && !Array.isArray(body) ? body : {}),
    status: Number.isInteger(entry.status) ? entry.status : null,
    requestId: typeof body?.requestId === 'string' ? body.requestId : null,
    browserRequestId: entry.browserRequestId,
    ...(typeof body?.captureId === 'string' ? { captureId: body.captureId } : {}),
    proposalId: body?.proposalId ?? null,
    proposalOutputId: body?.outputId ?? previewBody?.outputId ?? null,
    proposalSnapshotToken: body?.snapshotToken ?? null,
    proposalDraftVersion: Number.isFinite(numericDraftVersion) ? numericDraftVersion : null,
    proposalDraftDigest: body?.draftDigest ?? null,
    proposalPreviewStatus: body?.previewStatus ?? null,
    preview: hasPreview ? {
      receiptId: previewBody.receiptId ?? null,
      outputId: previewBody.outputId ?? null,
      rowCount: Number.isFinite(numericRowCount) ? numericRowCount : null,
      rowsPresent: Array.isArray(previewBody.rows),
    } : null,
    body,
  };
};
const nativeSettlementRecord = entry => {
  assert(Number.isFinite(entry.startedAt), `Native request ${entry.browserRequestId} must retain its actual start time.`);
  assert(Number.isFinite(entry.completedAt), `Native request ${entry.browserRequestId} must retain its actual settlement time.`);
  return {
    kind: Number.isInteger(entry.status) ? 'http-response' : 'request-failed',
    browserRequestId: entry.browserRequestId,
    requestId: entry.requestId ?? null,
    ...(typeof entry.request?.captureId === 'string' ? { captureId: entry.request.captureId } : {}),
    ...(Number.isInteger(entry.status) ? { status: entry.status } : {}),
    ...(entry.failure?.errorText ? { errorText: entry.failure.errorText } : {}),
    startedAt: entry.startedAt,
    ...(entry.responseReceivedAt === undefined ? {} : { responseReceivedAt: entry.responseReceivedAt }),
    completedAt: entry.completedAt,
  };
};
const ownerForProposal = (entry, attempt, mount, kind = 'proposal') => ({
  kind,
  attempt,
  browserRequestId: entry.browserRequestId,
  requestId: entry.requestId ?? null,
  mountEpoch: mount.mountEpoch,
  documentTimeOrigin: mount.documentTimeOrigin,
  ...(typeof entry.request?.captureId === 'string' ? { captureId: entry.request.captureId } : {}),
});
const groupCandidateIntent = (entry, savedBefore) => {
  const body = entry.request ?? {};
  const candidateConstruction = entry.response?.candidateConstruction;
  const candidateStep = candidateConstruction?.steps?.find(step =>
    step.operation?.kind === 'GROUP' && step.id === (body.changedStepId ?? body.candidateConstruction?.steps
      ?.find(candidate => candidate.operation?.kind === 'GROUP')?.id))
    ?? candidateConstruction?.steps?.find(step => step.operation?.kind === 'GROUP')
    ?? body.candidateConstruction?.steps?.find(step => step.operation?.kind === 'GROUP');
  const base = groupProjection(savedBefore);
  const targetStepId = body.changedStepId ?? candidateStep?.id ?? null;
  assert(targetStepId, 'Captured Group proposal must retain its actual changed step identity.');
  assert(candidateStep, 'Captured Group proposal must retain its actual candidate Group step.');
  const aggregates = candidateStep.operation.group?.aggregates ?? [];
  assert.equal(aggregates.length, 1, 'Captured Group proposal must retain its actual single aggregate.');
  return {
    kind: base === null ? 'create' : 'edit',
    targetStepId,
    base,
    proposed: {
      stepId: candidateStep.id,
      keys: (candidateStep.operation.group?.keys ?? []).map(key => key.inputColumnId),
      aggregate: aggregates[0]?.operation ?? null,
    },
    rawProposalBody: structuredClone(body),
  };
};
const relatedCandidateIntent = (entry, savedBefore) => {
  const body = entry.request ?? {};
  const candidateConstruction = entry.response?.candidateConstruction;
  const removing = Array.isArray(body.removeStepIds) && body.removeStepIds.length > 0;
  const targetStepId = removing
    ? body.removeStepIds[0]
    : body.changedStepId ?? candidateConstruction?.steps?.find(step => step.operation?.kind === 'RELATED_SOURCE')?.id
      ?? body.candidateConstruction?.steps?.find(step => step.operation?.kind === 'RELATED_SOURCE')?.id;
  assert(targetStepId, 'Captured RELATED_SOURCE proposal must retain its actual target step identity.');
  const proposedStep = removing ? null
    : candidateConstruction?.steps?.find(step => step.id === targetStepId && step.operation?.kind === 'RELATED_SOURCE')
      ?? body.candidateConstruction?.steps?.find(step => step.id === targetStepId && step.operation?.kind === 'RELATED_SOURCE');
  if (!removing) assert(proposedStep, 'Captured RELATED_SOURCE proposal must retain its response candidate step.');
  return {
    kind: removing ? 'remove' : 'add',
    targetStepId,
    base: relatedSourceProjection(savedBefore),
    proposed: relatedSourceProjectionFromStep(proposedStep),
    rawProposalBody: structuredClone(body),
  };
};
const waitForNativeSettlement = async entry => {
  if (entry.networkComplete && entry.responseCaptureComplete) return entry;
  const fromIndex = nativeCapture.requests.indexOf(entry);
  assert(fromIndex >= 0, 'Native settlement must belong to the captured request view.');
  return nativeCapture.waitForResponse(candidate => candidate.browserRequestId === entry.browserRequestId &&
    candidate.networkComplete && candidate.responseCaptureComplete, { fromIndex, timeoutMs: 5000 });
};
const waitForNativeProposalApply = async (fromIndex, proposalId) => {
  const candidates = nativeCapture.requests.slice(fromIndex).filter(entry =>
    entry.path === `${base}/commands` && entry.method === 'POST' &&
    entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' &&
      command.proposalId === proposalId));
  assert(candidates.length > 0, `Apply for proposal ${proposalId} must retain its actual native command request.`);
  return waitForNativeSettlement(candidates.at(-1));
};
const recordProposalCaptureBatch = async ({ requestStart, observedEntries, chosen, state, captureKind,
  attempt = 'P1', candidateReadyObservation = null, candidateReadyObservationMount = null, label }) => {
  const entries = observedEntries ?? nativeCapture.requests.slice(requestStart).filter(entry =>
    entry.path === `${base}/construction-proposals` && entry.method === 'POST' &&
    entry.request?.outputId === outputId);
  assert(entries.includes(chosen), `${label} response must match an actual native proposal request.`);
  const chosenKind = proposalCaptureKind(chosen, builderStateForProposal(chosen, state));
  assert.equal(chosenKind, captureKind,
    `${label} selected native proposal candidate must match its actual Builder operation kind.`);
  for (const entry of entries) await waitForNativeSettlement(entry);
  const unrecorded = entries.filter(entry => !capturedProposalBrowserRequestIds.has(entry.browserRequestId));
  assert(unrecorded.length > 0, `${label} actual proposal requests must not be recorded twice.`);
  const events = [];
  const captures = new Map();
  for (const [index, entry] of unrecorded.entries()) {
    capturedProposalBrowserRequestIds.add(entry.browserRequestId);
    const ownerAttempt = index === 0 ? attempt : 'P2';
    const mount = await awaitCapturedMount(proposalMounts, entry.browserRequestId);
    assert(Number.isInteger(mount.mountEpoch) && Number.isFinite(mount.documentTimeOrigin),
      `Observed proposal ${entry.browserRequestId} must retain its actual page mount and timeOrigin.`);
    const owner = ownerForProposal(entry, ownerAttempt, mount);
    const requestEvidenceResult = await awaitProposalRequestEvidence(entry.browserRequestId);
    const requestEvidence = requestEvidenceResult.evidence ?? null;
    const requestObservationAvailable = requestEvidence?.observationAvailable === true;
    const requestEventTimeSnapshot = requestObservationAvailable
      ? {
          available: true,
          observedAt: requestEvidence.observedAt,
          requestMount: requestEvidence.mount,
          observationMount: requestEvidence.observationMount,
        }
      : {
          available: false,
          reason: 'event-time-snapshot-unavailable',
          ...(Number.isFinite(requestEvidence?.observedAt ?? requestEvidenceResult.observedAt)
            ? { observedAt: requestEvidence?.observedAt ?? requestEvidenceResult.observedAt } : {}),
          ...(requestEvidence ? {
            requestMount: requestEvidence.mount,
            observationMount: requestEvidence.observationMount,
          } : {}),
        };
    if (requestEvidence) {
      assert.equal(requestEvidence.mount.mountEpoch, mount.mountEpoch,
        `Proposal ${entry.browserRequestId} request evidence must retain its actual owner mount.`);
      assert.equal(requestEvidence.mount.documentTimeOrigin, mount.documentTimeOrigin,
        `Proposal ${entry.browserRequestId} request evidence must retain its actual owner timeOrigin.`);
    }
    const requestObservation = requestObservationAvailable ? requestEvidence.observation : null;
    const requestRenderedObservation = requestObservationAvailable ? requestEvidence.renderedObservation : null;
    const request = nativeRequestRecord(entry);
    const response = Number.isInteger(entry.status) ? nativeResponseRecord(entry) : null;
    const settlement = nativeSettlementRecord(entry);
    const settlementMount = await awaitSettlementMount(entry.browserRequestId);
    const settlementEvidenceResult = await awaitProposalSettlementEvidence(entry.browserRequestId);
    const settlementEvidence = settlementEvidenceResult.evidence ?? null;
    const entryState = builderStateForProposal(entry, state);
    const settlementObservationAvailable = settlementEvidence?.observationAvailable === true;
    const eventTimeSnapshot = settlementObservationAvailable
      ? {
          available: true,
          observedAt: settlementEvidence.observedAt,
          responseMount: settlementEvidence.mount,
          observationMount: settlementEvidence.observationMount,
        }
      : {
          available: false,
          reason: 'event-time-snapshot-unavailable',
          ...(Number.isFinite(settlementEvidence?.observedAt ?? settlementEvidenceResult.observedAt)
            ? { observedAt: settlementEvidence?.observedAt ?? settlementEvidenceResult.observedAt } : {}),
          ...(settlementEvidence ? {
            responseMount: settlementEvidence.mount,
            observationMount: settlementEvidence.observationMount,
          } : {}),
        };
    if (settlementEvidence) {
      assert.equal(settlementEvidence.mount.mountEpoch, settlementMount.mountEpoch,
        `Proposal ${entry.browserRequestId} settlement evidence must retain the same actual mount observation.`);
      assert.equal(settlementEvidence.mount.documentTimeOrigin, settlementMount.documentTimeOrigin,
        `Proposal ${entry.browserRequestId} settlement evidence must retain the same actual document timeOrigin.`);
    }
    const pageObservation = settlementObservationAvailable ? settlementEvidence.observation : null;
    const panelId = settlementObservationAvailable ? settlementEvidence.proposalPanel?.id ?? null : null;
    const currentPanelOwners = panelId ? nativeCapture.requests.filter(candidate =>
      candidate.path === `${base}/construction-proposals` &&
      candidate.response?.proposalId === panelId) : [];
    const currentPanelOwner = currentPanelOwners.length === 1 ? currentPanelOwners[0] : null;
    const renderedObservation = settlementObservationAvailable
      ? {
          ...settlementEvidence.renderedObservation,
          proposalPanelObservedAt: settlementEvidence.proposalPanelObservedAt,
          ...(currentPanelOwner ? { proposalPanelBrowserRequestId: currentPanelOwner.browserRequestId } : {}),
        }
      : null;
    const savedBefore = savedBuilderProjection(entryState);
    const savedAfter = settlementObservationAvailable ? settlementEvidence.savedAfter : null;
    const savedAfterBuilder = settlementObservationAvailable ? settlementEvidence.savedAfterBuilder : null;
    const requestBody = entry.request ?? {};
    const currentDraftMatches = savedAfterBuilder &&
      String(savedAfterBuilder.draftVersion) ===
        String(requestBody.expectedDraftVersion ?? requestBody.draftVersion) &&
      savedAfterBuilder.draftDigest === (requestBody.expectedDraftDigest ?? requestBody.draftDigest);
    if (currentDraftMatches) assert.deepEqual(savedAfter, savedBefore,
      `${label} current proposal must leave the actual saved Builder projection unchanged before Apply or Cancel.`);
    const capture = { owner, request, response, settlement, renderedObservation, eventTimeSnapshot };
    const selectedCandidateReady = entry.browserRequestId === chosen.browserRequestId
      ? candidateReadyObservation
      : null;
    const selectedCandidateReadyMount = entry.browserRequestId === chosen.browserRequestId
      ? candidateReadyObservationMount
      : null;
    if (selectedCandidateReady) {
      assert.equal(selectedCandidateReady.proposalPanelBrowserRequestId, entry.browserRequestId,
        'Candidate-ready DOM must bind to its exact native browser request owner.');
      assert.equal(selectedCandidateReady.proposalPanel.id, entry.response?.proposalId,
        'Candidate-ready panel DOM must bind to the selected actual proposal response.');
      assert.equal(selectedCandidateReady.proposalPanel.status, 'ready',
        'Candidate-ready panel DOM must expose its actual ready status.');
    }
    const entryCaptureKind = proposalCaptureKind(entry, entryState);
    if (entryCaptureKind === 'relatedSource') {
      capture.relatedSourceLifecycle = {
        ...mount,
        owner,
        candidateIntent: relatedCandidateIntent(entry, entryState),
        request,
        response,
        settlement,
        settlementObservedEpoch: settlementMount.mountEpoch,
        renderedObservation: selectedCandidateReady ?? null,
        ...(selectedCandidateReadyMount ? { renderedObservationMount: selectedCandidateReadyMount } : {}),
        eventTimeSnapshot,
        savedGroupInput: groupProjection(entryState),
        savedBefore,
        savedAfter,
      };
      events.push({
        sequence: entry.completedAt ?? entry.startedAt,
        tie: entry.index,
        action: {
          label: 'RelatedSourcePreview',
          event: 'related-source-preview',
          ...capture,
          observedEpoch: settlementMount.mountEpoch,
          observation: pageObservation,
          responseEventObservation: renderedObservation,
          renderedObservation: selectedCandidateReady ?? null,
          ...(selectedCandidateReadyMount ? { renderedObservationMount: selectedCandidateReadyMount } : {}),
          eventTimeSnapshot,
          relatedSourceLifecycle: capture.relatedSourceLifecycle,
        },
      });
      captures.set(entry.browserRequestId, capture);
      continue;
    }

    const candidateIntent = entryCaptureKind === 'group' ? groupCandidateIntent(entry, entryState) : null;
    if (entryCaptureKind === 'group') {
      capture.groupLifecycle = {
        ...mount,
        owner: { ...mount, ...owner },
        candidateIntent,
        savedGroupInput: groupProjection(entryState),
        savedBefore,
        savedAfter,
        eventTimeSnapshot,
      };
    } else {
      const candidateSteps = entry.response?.candidateConstruction?.steps
        ?? entry.request?.candidateConstruction?.steps;
      assert(Array.isArray(candidateSteps), `${label} row-expansion proposal must retain its actual candidate construction.`);
      const changedOperationSteps = changedConstructionOperationSteps(constructionSteps(entryState), candidateSteps);
      assert.equal(changedOperationSteps.length, 1,
        `${label} candidate must contain exactly one actual changed operation.`);
      const expandedStep = changedOperationSteps[0];
      assert(['EXPAND', 'RELATED_EXPAND'].includes(expandedStep.operation?.kind),
        `${label} actual changed operation must be a row expansion.`);
      if (requestBody.changedStepId) assert.equal(expandedStep.id, requestBody.changedStepId,
        `${label} changedStepId must identify the actual changed expansion operation.`);
      const currentSteps = constructionSteps(entryState);
      const sourceResourceType = currentSteps.at(-1)?.operation?.relatedExpand?.targetResourceType
        ?? doc(entryState).rootResourceType;
      const targetResourceType = expandedStep.operation?.relatedExpand?.targetResourceType;
      const stageToken = `${sourceResourceType}${targetResourceType}`;
      assert(['SpecimenPatient', 'PatientObservation'].includes(stageToken),
        `${label} actual source and target resource identities must bind a registered row-expansion stage.`);
      capture.savedBeforeTable = settlementEvidence?.sourceTable ?? null;
      capture.mountEpoch = mount.mountEpoch;
      capture.expandRowsLifecycle = {
        ...mount,
        stage: stageToken,
        stepId: expandedStep.id,
        inputStepId: expandedStep.inputs?.find(input => input.kind === 'STEP_OUTPUT')?.stepId ?? null,
        route: structuredClone(expandedStep.operation?.relatedExpand?.route ?? null),
        owner,
        request,
        response,
        renderedObservation,
        eventTimeSnapshot,
        savedBefore,
        savedAfter,
      };
    }
    const previewAction = {
      label: 'Preview',
      event: 'preview',
      owner,
      request,
      observedEpoch: mount.mountEpoch,
      observation: requestObservation,
      renderedObservation: requestRenderedObservation,
      eventTimeSnapshot: requestEventTimeSnapshot,
      ...(capture.groupLifecycle ? {
        groupLifecycle: { ...capture.groupLifecycle, eventTimeSnapshot: requestEventTimeSnapshot },
      } : {}),
      ...(capture.expandRowsLifecycle ? {
        rowExpansionLifecycle: {
          ...capture.expandRowsLifecycle,
          renderedObservation: requestRenderedObservation,
          eventTimeSnapshot: requestEventTimeSnapshot,
        },
      } : {}),
    };
    const aborted = entry.failure?.errorText === 'net::ERR_ABORTED';
    const ready = entry.status >= 200 && entry.status < 300 &&
      entry.response?.previewStatus === 'READY' && entry.response?.proposalId;
    const panelObservedAfterSettlement = settlementEvidence &&
      Number.isFinite(settlementEvidence.proposalPanelObservedAt) &&
      Number.isFinite(entry.completedAt) && settlementEvidence.proposalPanelObservedAt >= entry.completedAt;
    const newerCandidateOwner = currentPanelOwner && panelObservedAfterSettlement &&
      currentPanelOwner.browserRequestId !== entry.browserRequestId &&
      Number.isFinite(currentPanelOwner.startedAt) && currentPanelOwner.startedAt > entry.startedAt &&
      currentPanelOwner.startedAt < entry.completedAt &&
      Number.isFinite(currentPanelOwner.completedAt) &&
      currentPanelOwner.completedAt <= settlementEvidence.proposalPanelObservedAt &&
      currentPanelOwner.response?.previewStatus === 'READY';
    const oldResultIsNotCurrent = ready
      ? Boolean(panelId && panelId !== entry.response.proposalId)
      : Boolean(entry.failure);
    const lateIgnored = Boolean(newerCandidateOwner && oldResultIsNotCurrent);
    const lateClassification = lateIgnored
      ? {
          available: true,
          ownerBrowserRequestId: currentPanelOwner.browserRequestId,
          ownerStartedAt: currentPanelOwner.startedAt,
          ownerCompletedAt: currentPanelOwner.completedAt,
          renderedPanelProposalId: panelId,
          proposalPanelObservedAt: settlementEvidence.proposalPanelObservedAt,
          proposalPanelBrowserRequestId: currentPanelOwner.browserRequestId,
        }
      : {
          available: false,
          reason: !settlementObservationAvailable
            ? 'event-time-snapshot-unavailable'
            : currentPanelOwners.length > 1
              ? 'current-proposal-owner-ambiguous'
              : currentPanelOwners.length === 0
                ? 'current-proposal-owner-unavailable'
                : 'current-newer-candidate-owner-not-proven',
          ...(settlementEvidence?.proposalPanelObservedAt
            ? { proposalPanelObservedAt: settlementEvidence.proposalPanelObservedAt } : {}),
          ...(currentPanelOwner
            ? { proposalPanelBrowserRequestId: currentPanelOwner.browserRequestId } : {}),
        };
    if (lateClassification.available) {
      assert.equal(renderedObservation?.proposalPanel?.id, lateClassification.renderedPanelProposalId,
        'Late-preview classification must match the separately captured current panel DOM.');
      assert.equal(renderedObservation?.proposalPanelObservedAt, lateClassification.proposalPanelObservedAt,
        'Late-preview panel timestamp must come from the same event-time DOM observation.');
      assert.equal(renderedObservation?.proposalPanelBrowserRequestId, lateClassification.proposalPanelBrowserRequestId,
        'Late-preview panel owner must match the unique actual current candidate request.');
    }
    const actionRenderedObservation = selectedCandidateReady ?? (lateClassification.available
      ? {
          ...renderedObservation,
          proposalPanelObservedAt: lateClassification.proposalPanelObservedAt,
          proposalPanelBrowserRequestId: lateClassification.proposalPanelBrowserRequestId,
        }
      : renderedObservation);
    const settlementLabel = aborted ? 'PreviewRequestCancelled'
      : lateIgnored && ready ? 'LatePreviewCompleteIgnored'
        : lateIgnored && entry.failure ? 'LatePreviewErrorIgnored'
          : ready ? 'PreviewComplete' : 'PreviewError';
    const settlementAction = {
      label: settlementLabel,
      event: aborted ? 'preview-request-cancelled' : 'preview-response',
      owner,
      request,
      response,
      settlement,
      observedEpoch: settlementMount.mountEpoch,
      observation: pageObservation,
      responseEventObservation: renderedObservation,
      renderedObservation: actionRenderedObservation,
      ...(selectedCandidateReadyMount ? { renderedObservationMount: selectedCandidateReadyMount } : {}),
      eventTimeSnapshot,
      lateClassification,
      ...(capture.groupLifecycle ? { groupLifecycle: capture.groupLifecycle } : {}),
      ...(capture.expandRowsLifecycle ? { rowExpansionLifecycle: capture.expandRowsLifecycle } : {}),
    };
    events.push({ sequence: entry.startedAt, tie: entry.index, action: previewAction });
    events.push({ sequence: entry.completedAt ?? entry.startedAt, tie: entry.index + 0.5, action: settlementAction });
    captures.set(entry.browserRequestId, capture);
  }
  events.sort((left, right) => left.sequence - right.sequence || left.tie - right.tie);
  journeyTrace.actions.push(...events.map(event => event.action));
  const chosenCapture = captures.get(chosen.browserRequestId);
  assert(chosenCapture, `${label} selected response must retain its captured native request owner.`);
  return chosenCapture;
};
const assertPreviewValues = (preview, expectedRows, label) => {
  assert(expectedRows.length <= 25, `${label} must have a complete bounded witness population of at most 25 rows`);
  assert.equal(preview.outputId, outputId, `${label} receipt must belong to the selected output`);
  assert.equal(preview.rowCount, expectedRows.length, `${label} row count differs from raw witnesses`);
  const displayed = preview.rows.map((row) => preview.columns.map((column) => row[column.column]));
  assert.equal(displayed.length, expectedRows.length, `${label} must return every bounded witness row`);
  assertCompletePopulationRowsMatchOracle(displayed, expectedRows, { label });
};
const expectedPreview = async (expectedRows, { requestStart, expectedBuilder, label, startedAt }) => {
  const deadline = startedAt + 5000;
  await waitForBrowser(page, ({ outputId: expectedOutput, version, digest }) => {
    const p = document.querySelector('[data-testid="construction-preview"]');
    return p?.dataset.previewStatus === 'ready' && p?.dataset.previewOutputId === expectedOutput &&
      p?.dataset.currentDraftVersion === version && p?.dataset.currentDraftDigest === digest && Boolean(p?.dataset.previewReceiptId);
  }, Math.max(1, deadline - Date.now()), { outputId, version: String(expectedBuilder.draftVersion), digest: expectedBuilder.draftDigest });
  const active = await browserEval(page, () => {
    const p = document.querySelector('[data-testid="construction-preview"]');
    return { status: p?.dataset.previewStatus, receiptId: p?.dataset.previewReceiptId, outputId: p?.dataset.previewOutputId,
      draftVersion: p?.dataset.currentDraftVersion, draftDigest: p?.dataset.currentDraftDigest };
  });
  assert.equal(active.status, 'ready');
  assert.equal(active.outputId, outputId);
  assert.equal(active.draftVersion, String(expectedBuilder.draftVersion));
  assert.equal(active.draftDigest, expectedBuilder.draftDigest);
  assert(active.receiptId, `${label} must expose the current saved receipt`);
  const request = await nativeCapture.waitForResponse(entry => entry.path === `${base}/preview` && entry.method === 'POST' && entry.status === 200 && entry.response?.receiptId === active.receiptId && entry.response?.outputId === outputId, { fromIndex: requestStart, timeoutMs: Math.max(1, deadline - Date.now()) });
  assert(request, `${label} needs a native receipt-bound Preview request and response`);
  assert.equal(request.response.receiptId, active.receiptId);
  assert.equal(request.response.outputId, outputId);
  assertPreviewValues(request.response, expectedRows, label);
  assert(Date.now() <= deadline, `${label} exceeded its five-second action budget`);
  return { active, response: request.response, request, requestIndex: nativeCapture.requests.indexOf(request) };
};
const open = async (expectedRows, columnCount = expectedRows[0]?.length ?? 2, label = 'reload') => {
  builder = await api(base + '/builder');
  const savedBefore = savedBuilderProjection(builder);
  const builderReadBefore = builderReadMetadata.forBuilder(builder);
  const startedAt = Date.now();
  const requestStart = nativeCapture.requests.length;
  const previousTimeOrigin = documentTimeOrigin;
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForControl(page, `[data-testid="construction-table-${outputId}"]`);
  const mount = await currentMount();
  if (journeyTraceStarted) {
    assert.notEqual(mount.documentTimeOrigin, previousTimeOrigin,
      'Every recorded Builder reload must create a new browser document timeOrigin.');
  }
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForControl(page, '[data-testid="construction-rows-settings-trigger"]', { enabled: true });
  await waitForBrowser(page, ({ columnCount: count }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-colcount') === count && !document.body.innerText.includes('Loading your table…');
  }, 5000, { columnCount: String(columnCount) });
  await mountedRows(expectedRows, columnCount);
  builder = await api(base + '/builder');
  const builderReadAfter = builderReadMetadata.forBuilder(builder);
  const binding = assertSourceBinding(builder, selectedPopulationRoute);
  assert.equal(binding.output.id, outputId);
  const preview = await expectedPreview(expectedRows, { requestStart, expectedBuilder: builder, label, startedAt });
  const initialPreviewMount = await currentMount();
  const observation = await captureBuilderObservation(page);
  const renderedRows = await readRenderedSourceTable();
  const historySteps = await readConstructionHistoryDOM();
  const mountedGroup = mountedGroupFromHistoryDOM(historySteps);
  const mountedRelatedSource = mountedRelatedSourceFromDOM({ historySteps, renderedRows, state: builder });
  const savedAfter = savedBuilderProjection(builder);
  assert.equal(mountedGroup?.stepId ?? null, savedAfter.group?.stepId ?? null,
    'The mounted Group history identity must match the independently read saved Group step.');
  if (mountedGroup && savedAfter.group) {
    const savedKeyLabels = savedColumnLabelsForIds(doc(builder), savedAfter.group.keys);
    assert.deepEqual(mountedGroup.keys, savedKeyLabels,
      'The mounted Group history key labels must match the independently read saved key identities.');
    assert.equal(mountedGroup.aggregate, savedAfter.group.aggregate,
      'The mounted Group history aggregate must match the independently read saved aggregate.');
  }
  if (mountedRelatedSource) {
    assert(savedAfter.relatedSource, 'Mounted RELATED_SOURCE history must have a saved related-source step.');
    assert.equal(mountedRelatedSource.stepId, savedAfter.relatedSource.stepId);
    assert.equal(mountedRelatedSource.inputStepId, savedAfter.relatedSource.inputStepId);
    assert.deepEqual(mountedRelatedSource.source, savedAfter.relatedSource.source);
    assert.equal(mountedRelatedSource.outputColumnId, savedAfter.relatedSource.outputColumnId);
    assert.deepEqual(mountedRelatedSource.route, savedAfter.relatedSource.route);
    assert.equal(mountedRelatedSource.visibleColumn.id, savedAfter.relatedSource.outputColumnId);
    assert.equal(mountedRelatedSource.visibleColumn.label, savedAfter.relatedSource.output?.label);
  } else {
    assert.equal(savedAfter.relatedSource, null,
      'No mounted RELATED_SOURCE history row must agree with no saved RELATED_SOURCE step.');
  }
  if (!journeyTraceStarted) {
    journeyTraceStarted = true;
    journeyTrace.initialObservation = observation;
    journeyTrace.initialPreview = {
      owner: ownerForProposal(preview.request, 'P1', initialPreviewMount, 'preview'),
      request: nativeRequestRecord(preview.request),
      response: nativeResponseRecord(preview.request),
      settlement: nativeSettlementRecord(preview.request),
      renderedObservation: observation,
      renderedRows,
    };
  } else {
    assert(savedBefore, 'A modeled Builder reload must have a saved-before projection from an actual Builder read.');
    journeyTrace.actions.push({
      label: 'Reload',
      event: 'reload',
      observedAt: Date.now(),
      groupLifecycle: {
        ...mount,
        savedBefore,
        savedAfter,
        mountedGroup,
        mountedRelatedSource,
        renderedSourceRows: renderedRows,
      },
      builderReads: {
        before: builderReadBefore,
        after: builderReadAfter,
      },
      observation,
      renderedRows,
      historySteps,
    });
  }
  recordAction(label, startedAt, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  return builder;
};
const mountedRows = async (expectedRows, columnCount = expectedRows[0]?.length ?? 2) => {
  assert(expectedRows.length <= 25, 'Rendered witness population must be complete within the 25-row preview bound');
  await waitForBrowser(page, ({ rows: rowCount, columns: columnTotal }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === rowCount && table?.getAttribute('aria-colcount') === columnTotal &&
      !document.body.innerText.includes('Loading your table…');
  }, 5000, { rows: String(Math.min(25, expectedRows.length) + 1), columns: String(columnCount) });
  const rows = await browserEval(page, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
    .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => {
      const indicator = cell.querySelector('[data-testid="preview-array-indicator"]');
      if (!indicator) return { value: cell.innerText.trim(), arrayIndicator: null };
      const arrayIndicator = indicator.innerText.trim();
      const value = arrayIndicator === 'Empty list'
        ? ''
        : indicator.parentElement?.querySelector('span:not([data-testid="preview-array-indicator"])')?.innerText.trim();
      return { value, arrayIndicator };
    })).filter(row => row.length));
  assert.equal(rows.length, expectedRows.length, 'Rendered preview must contain the complete bounded witness population');
  const normalizeRendered = (value) => value.split('; ').sort().join('; ');
  const renderedActual = rows.map((row) => row.map((cell) => normalizeRendered(cell.value)));
  const renderedExpected = expectedRows.map((row) => row.map((value) => Array.isArray(value)
    ? [...value].sort().join('; ')
    : normalizeRendered(String(value))));
  assertCompletePopulationRowsMatchOracle(renderedActual, renderedExpected, { label: 'rendered preview' });
  const expectedArrayIndicator = (value) => !Array.isArray(value)
    ? null
    : value.length === 0 ? 'Empty list' : `${value.length} ${value.length === 1 ? 'value' : 'values'}`;
  const actualEvidence = rows.map((row, index) => JSON.stringify({
    values: renderedActual[index],
    indicators: row.map((cell) => cell.arrayIndicator),
  })).sort();
  const expectedEvidence = expectedRows.map((row, index) => JSON.stringify({
    values: renderedExpected[index],
    indicators: row.map(expectedArrayIndicator),
  })).sort();
  assert.deepEqual(actualEvidence, expectedEvidence,
    'Rendered preview array indicators must match the complete raw row values');
};
let nativeCapture;
let nativeRequestListener;
let nativeResponseListener;
let nativeRequestFailedListener;
const installBrowserCapture = async () => {
  nativeCapture = captureCDARequests(page, {
    apiOrigin,
    browserRequestOrigin: uiOrigin,
    appOrigins: [uiOrigin],
    ownedPathPrefix: base,
    report,
    responsePaths: /builder|capability|row-definition-choices|population-routes|construction-capabilities|semantic-inventory|construction-choices|related-expand-choices|construction-proposals|commands|preview/,
  });
  page.on('pageerror', error => report.errors.push({ kind: 'runtime', text: error.message }));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location === `${uiOrigin}/favicon.ico` && /404 \(Not Found\)/.test(message.text())) {
      (report.assetFailures ??= []).push({ url: location, status: 404, kind: 'console' });
      return;
    }
    report.errors.push({ kind: 'console', text: message.text() });
  });
  nativeRequestListener = request => {
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (isProtectedExplorerRequestPath(url.pathname, root, protectedExplorer)) report.protectedExplorerUntouched = false;
    const entry = nativeCapture.observer.byRequest.get(request);
    if (!entry) return;
    if (entry.path === `${base}/construction-proposals`) {
      const requestEventAt = Date.now();
      const mountCapture = captureCurrentMount();
      proposalMounts.set(entry.browserRequestId, mountCapture);
      const evidence = mountCapture.then(async result => {
        if (result.error) throw result.error;
        const [observation, proposalPanel] = await Promise.all([
          captureBuilderObservation(page),
          readRenderedProposalPanel(),
        ]);
        const observedAt = Date.now();
        const observationMount = await currentMount();
        const observationAvailable = observationMount.mountEpoch === result.mount.mountEpoch &&
          observationMount.documentTimeOrigin === result.mount.documentTimeOrigin;
        return {
          observedAt,
          proposalPanelObservedAt: observationAvailable ? observedAt : null,
          mount: result.mount,
          observationMount,
          observationAvailable,
          observation: observationAvailable ? observation : null,
          proposalPanel: observationAvailable ? proposalPanel : null,
          renderedObservation: observationAvailable ? {
            builder: observation,
            proposalPanel: structuredClone(proposalPanel),
          } : null,
        };
      }).then(value => ({ evidence: value }), error => ({ error, observedAt: requestEventAt }));
      proposalRequestEvidence.set(entry.browserRequestId, evidence);
    }
    if (entry.path === `${base}/commands` && entry.request?.commands?.some(command =>
      command.type === 'APPLY_CONSTRUCTION_PROPOSAL')) {
      applyMounts.set(entry.browserRequestId, captureCurrentMount());
    }
  };
  const captureSettlementMount = request => {
    const entry = nativeCapture.observer.byRequest.get(request);
    if (!entry || !(entry.path === `${base}/construction-proposals` ||
      (entry.path === `${base}/commands` && entry.request?.commands?.some(command =>
        command.type === 'APPLY_CONSTRUCTION_PROPOSAL')))) return;
    const settlementEventAt = Date.now();
    const captured = captureCurrentMount();
    proposalSettlementMounts.set(entry.browserRequestId, captured);
    if (entry.path === `${base}/construction-proposals`) {
      const evidence = captured.then(async result => {
        if (result.error) throw result.error;
        const [observation, proposalPanel, sourceTable, savedAfterBuilder] = await Promise.all([
          captureBuilderObservation(page),
          readRenderedProposalPanel(),
          readRenderedSourceTable(),
          api(base + '/builder'),
        ]);
        const observedAt = Date.now();
        const observationMount = await currentMount();
        const observationAvailable = observationMount.mountEpoch === result.mount.mountEpoch &&
          observationMount.documentTimeOrigin === result.mount.documentTimeOrigin;
        return {
          observedAt,
          proposalPanelObservedAt: observationAvailable ? observedAt : null,
          mount: result.mount,
          observationMount,
          observationAvailable,
          observation: observationAvailable ? observation : null,
          proposalPanel: observationAvailable ? proposalPanel : null,
          renderedObservation: observationAvailable ? {
            builder: observation,
            proposalPanel: structuredClone(proposalPanel),
          } : null,
          sourceTable: observationAvailable ? sourceTable : null,
          savedAfterBuilder,
          savedAfter: observationAvailable ? savedBuilderProjection(savedAfterBuilder) : null,
        };
      }).then(value => ({ evidence: value }), error => ({ error, observedAt: settlementEventAt }));
      proposalSettlementEvidence.set(entry.browserRequestId, evidence);
    }
    void captured.then(result => {
      const waiter = settlementMountWaiters.get(entry.browserRequestId);
      if (waiter) {
        settlementMountWaiters.delete(entry.browserRequestId);
        result.error ? waiter.reject(result.error) : waiter.resolve(result.mount);
      }
    });
  };
  nativeResponseListener = response => captureSettlementMount(response.request());
  nativeRequestFailedListener = request => captureSettlementMount(request);
  page.on('request', nativeRequestListener);
  page.on('response', nativeResponseListener);
  page.on('requestfailed', nativeRequestFailedListener);
};
const assertDraftRequest = (entry, state, label) => {
  assert(entry, `${label} must use the native Browser request`);
  assert.equal(entry.origin, uiOrigin, `${label} must use the configured local UI proxy`);
  assert(entry.path.startsWith(`${base}/`), `${label} must target the current project and owned Explorer`);
  assert.equal(entry.request.snapshotToken, state.catalog.snapshotToken);
  assert.equal(entry.request.expectedDraftVersion ?? entry.request.draftVersion, state.draftVersion);
  assert.equal(entry.request.expectedDraftDigest ?? entry.request.draftDigest, state.draftDigest);
  assert.equal(entry.status, 200);
  assert(entry.response, `${label} response body must be captured`);
  if (entry.path.endsWith('/commands')) {
    assert(entry.request.commands?.some((command) => command.outputId === outputId || command.output === outputId),
      `${label} native command must target the selected output`);
    assert(entry.response.workspace, `${label} must return the canonical applied workspace`);
    assert.equal(entry.response.draftVersion, state.draftVersion + 1, `${label} must advance exactly one draft revision`);
  } else {
    assert.equal(entry.request.outputId, outputId);
    assert.equal(entry.response.outputId, outputId);
    assert.equal(entry.response.snapshotToken, state.catalog.snapshotToken);
    assert.equal(entry.response.draftVersion, state.draftVersion);
    assert.equal(entry.response.draftDigest, state.draftDigest);
  }
  return entry.response;
};
const browserProposal = async ({ requestStart, path, expectedRows, state, label, startedAt, captureKind, attempt }) => {
  assert(path.endsWith('/construction-proposals'), `${label} must use the current native construction-proposals endpoint`);
  const panelSelector = '[data-testid="construction-proposal-panel"]';
  await waitForBrowser(page, ({ selector }) => ['ready', 'error', 'needs-repair'].includes(document.querySelector(selector)?.dataset.proposalStatus),
    Math.max(1, startedAt + 5000 - Date.now()), { selector: panelSelector });
  const panel = await browserEval(page, ({ selector }) => {
    const p = document.querySelector(selector);
    return { status: p?.dataset.proposalStatus, id: p?.dataset.proposalId, text: p?.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => ({ text: cell.innerText, title: cell.title }))) };
  }, { selector: panelSelector });
  const observed = nativeCapture.requests.slice(requestStart).filter(entry =>
    entry.path === path && entry.method === 'POST' && entry.request?.outputId === outputId);
  assert(observed.length > 0, `${label} must retain every actual native construction-proposal request.`);
  const request = await nativeCapture.waitForResponse(entry =>
    entry.path === path && entry.method === 'POST' && entry.request?.outputId === outputId &&
    entry.response?.proposalId === panel.id,
  { fromIndex: requestStart, timeoutMs: Math.max(1, startedAt + 5000 - Date.now()) });
  assert(request, `${label} ready proposal panel must match an actual response body and Browser request owner.`);
  await waitForNativeSettlement(request);
  const candidateReadyMount = await currentMount();
  const [candidateReadyBuilder, candidateReadyPanel] = await Promise.all([
    captureBuilderObservation(page),
    readRenderedProposalPanel(),
  ]);
  const candidateReadyObservedAt = Date.now();
  const candidateReadyObservationMount = await currentMount();
  const settlementMount = await awaitSettlementMount(request.browserRequestId);
  assert.equal(candidateReadyObservationMount.mountEpoch, candidateReadyMount.mountEpoch,
    `${label} candidate-ready DOM must stay in one actual page mount during observation.`);
  assert.equal(candidateReadyObservationMount.documentTimeOrigin, candidateReadyMount.documentTimeOrigin,
    `${label} candidate-ready DOM must stay in one actual document timeOrigin during observation.`);
  assert.equal(candidateReadyObservationMount.mountEpoch, settlementMount.mountEpoch,
    `${label} candidate-ready DOM must belong to the proposal response mount.`);
  assert.equal(candidateReadyObservationMount.documentTimeOrigin, settlementMount.documentTimeOrigin,
    `${label} candidate-ready DOM must belong to the proposal response document.`);
  assert.equal(candidateReadyPanel.status, 'ready', `${label} candidate-ready DOM must render a ready panel.`);
  assert.equal(candidateReadyPanel.id, request.response?.proposalId,
    `${label} candidate-ready DOM must bind to the selected actual proposal response.`);
  assert.equal(candidateReadyBuilder.proposalId, request.response?.proposalId,
    `${label} Builder observation must bind to the selected actual proposal response.`);
  assert(Number.isFinite(candidateReadyObservedAt) && candidateReadyObservedAt >= request.completedAt,
    `${label} candidate-ready observation must occur after the selected proposal settlement.`);
  const candidateReadyObservation = {
    builder: candidateReadyBuilder,
    proposalPanel: candidateReadyPanel,
    proposalPanelObservedAt: candidateReadyObservedAt,
    proposalPanelBrowserRequestId: request.browserRequestId,
  };
  const journeyCapture = await recordProposalCaptureBatch({
    requestStart,
    chosen: request,
    state,
    captureKind,
    attempt,
    candidateReadyObservation,
    candidateReadyObservationMount,
    label,
  });
  assert.equal(panel.status, 'ready', panel.text);
  const response = assertDraftRequest(request, state, label);
  assert.equal(response.previewStatus, 'READY', `${label} proposal must compile a ready automatic preview`);
  assert.equal(response.preview?.receiptId, response.proposalId, `${label} candidate receipt must bind to its proposal`);
  assert.equal(response.preview?.outputId, outputId);
  assert.equal(panel.id, response.proposalId);
  assertPreviewValues(response.preview, expectedRows, label);
  const normalizeDisplayedCell = (value) => value.split('; ').sort().join('; ');
  const displayedRows = panel.rows.map((row) => row.map((cell) => normalizeDisplayedCell(cell.text)));
  const expectedDisplayedRows = expectedRows.map((row) => row.map((value) => Array.isArray(value)
    ? [...value].sort().join('; ')
    : normalizeDisplayedCell(String(value))));
  assert.equal(displayedRows.length, expectedRows.length, `${label} proposal panel must render the complete witness population`);
  assertCompletePopulationRowsMatchOracle(displayedRows, expectedDisplayedRows, { label: `${label} proposal panel DOM` });
  recordAction(label, startedAt, { receiptId: response.proposalId, candidateWorkspaceDigest: response.candidateWorkspaceDigest });
  return { request, requestIndex: nativeCapture.requests.indexOf(request), response, panel, journeyCapture };
};
const findNative = (startIndex, path, predicate = () => true) => nativeCapture.requests.slice(startIndex).findLast((entry) => entry.path === path && predicate(entry));
const closeRowSettings = async () => {
  const dialog = '[role="dialog"][aria-label="Row definition settings"]';
  await click(page, `${dialog} button`, { name: 'Back to table' });
  await waitForControl(page, dialog, { hidden: true });
};
const waitForSavedStepEditorClosed = async () => {
  const editor = '[data-testid="construction-operation-editor"]';
  await waitForControl(page, editor, { hidden: true });
};
const recordRelatedSourceApply = async ({ proposal, applyRequest, savedBefore, savedAfter }) => {
  const lifecycle = proposal.journeyCapture.relatedSourceLifecycle;
  assert(lifecycle, 'RELATED_SOURCE Apply must bind to the exact captured proposal owner.');
  capturedApplyBrowserRequestIds.add(applyRequest.browserRequestId);
  const applyMount = await awaitCapturedMount(applyMounts, applyRequest.browserRequestId);
  const settlementMount = await awaitSettlementMount(applyRequest.browserRequestId);
  const observation = await captureBuilderObservation(page);
  journeyTrace.actions.push({
    label: 'RelatedSourceApply',
    event: 'related-source-apply',
    owner: {
      kind: 'apply',
      attempt: proposal.journeyCapture.owner.attempt,
      browserRequestId: applyRequest.browserRequestId,
      mountEpoch: applyMount.mountEpoch,
      documentTimeOrigin: applyMount.documentTimeOrigin,
      requestId: applyRequest.requestId ?? null,
      ...(typeof applyRequest.request?.captureId === 'string' ? { captureId: applyRequest.request.captureId } : {}),
      ...(typeof applyRequest.request?.commandId === 'string' ? { commandId: applyRequest.request.commandId } : {}),
    },
    request: nativeRequestRecord(applyRequest),
    response: nativeResponseRecord(applyRequest),
    settlement: nativeSettlementRecord(applyRequest),
    observedEpoch: settlementMount.mountEpoch,
    observation,
    relatedSourceLifecycle: {
      ...lifecycle,
      savedBefore: savedBuilderProjection(savedBefore),
      savedAfter: savedBuilderProjection(savedAfter),
    },
  });
};
const recordRelatedSourceCancel = async ({ proposal, savedBefore, savedAfter, observedAt }) => {
  const lifecycle = proposal.journeyCapture.relatedSourceLifecycle;
  assert(lifecycle, 'RELATED_SOURCE Cancel must bind to the exact captured proposal owner.');
  const observedMount = await currentMount();
  const observation = await captureBuilderObservation(page);
  journeyTrace.actions.push({
    label: 'RelatedSourceCancel',
    event: 'related-source-cancel',
    observedAt,
    observedEpoch: observedMount.mountEpoch,
    owner: proposal.journeyCapture.owner,
    request: lifecycle.request,
    response: lifecycle.response,
    settlement: lifecycle.settlement,
    exitControl: { kind: 'proposal-cancel', controlTestId: 'construction-cancel-proposal' },
    observation,
    relatedSourceLifecycle: {
      ...lifecycle,
      savedBefore: savedBuilderProjection(savedBefore),
      savedAfter: savedBuilderProjection(savedAfter),
    },
  });
};
const recordGroupEditCancel = async ({ proposal, savedBefore, savedAfter, observedAt }) => {
  const lifecycle = proposal.journeyCapture.groupLifecycle;
  assert(lifecycle, 'Group edit Cancel must bind to the exact captured Group proposal owner.');
  const mount = await currentMount();
  const observation = await captureBuilderObservation(page);
  journeyTrace.actions.push({
    label: 'GroupEditCancel',
    event: 'group-edit-cancel',
    observedAt,
    observedEpoch: mount.mountEpoch,
    observation,
    groupLifecycle: {
      ...mount,
      candidateIntent: lifecycle.candidateIntent,
      candidateOwner: lifecycle.owner,
      savedGroupInput: groupProjection(savedBefore),
      savedBefore: savedBuilderProjection(savedBefore),
      savedAfter: savedBuilderProjection(savedAfter),
      exitControl: { kind: 'proposal-cancel', controlTestId: 'construction-cancel-proposal' },
    },
  });
};
const applyGroupProposal = async (proposal, savedBefore, expectedRows, label, { journeyLabel = 'Group' } = {}) => {
  const proposalResponse = proposal.response;
  const startedAt = Date.now();
  const requestStart = nativeCapture.requests.length;
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(expectedRows, expectedRows[0]?.length ?? 2);
  const applyRequest = await waitForNativeProposalApply(requestStart, proposalResponse.proposalId);
  assertDraftRequest(applyRequest, savedBefore, label);
  assert(applyRequest.request.commands.some((item) => item.proposalId === proposalResponse.proposalId));
  capturedApplyBrowserRequestIds.add(applyRequest.browserRequestId);
  const applyMount = await awaitCapturedMount(applyMounts, applyRequest.browserRequestId);
  const settlementMount = await awaitSettlementMount(applyRequest.browserRequestId);
  builder = await api(base + '/builder');
  assert.equal(builder.draftVersion, proposalResponse.draftVersion + 1);
  assert.equal(builder.draftDigest, proposalResponse.candidateWorkspaceDigest);
  const observation = await captureBuilderObservation(page);
  const mount = settlementMount;
  const request = nativeRequestRecord(applyRequest);
  const response = nativeResponseRecord(applyRequest);
  const settlement = nativeSettlementRecord(applyRequest);
  const owner = {
    kind: 'apply',
    attempt: proposal.journeyCapture.owner.attempt,
    browserRequestId: applyRequest.browserRequestId,
    mountEpoch: applyMount.mountEpoch,
    documentTimeOrigin: applyMount.documentTimeOrigin,
    requestId: applyRequest.requestId ?? null,
    ...(typeof applyRequest.request?.captureId === 'string' ? { captureId: applyRequest.request.captureId } : {}),
    ...(typeof applyRequest.request?.commandId === 'string' ? { commandId: applyRequest.request.commandId } : {}),
  };
  if (proposal.journeyCapture.relatedSourceLifecycle) {
    const lifecycle = proposal.journeyCapture.relatedSourceLifecycle;
    const relatedSourceLifecycle = {
      ...lifecycle,
      savedBefore: savedBuilderProjection(savedBefore),
      savedAfter: savedBuilderProjection(builder),
    };
    journeyTrace.actions.push({
      label: 'RelatedSourceApply',
      event: 'related-source-apply',
      owner,
      request,
      response,
      settlement,
      observation,
      relatedSourceLifecycle,
    });
  } else if (proposal.journeyCapture.expandRowsLifecycle) {
    const lifecycle = proposal.journeyCapture.expandRowsLifecycle;
    journeyTrace.actions.push({
      label: 'ExpandRows',
      event: 'expand-rows',
      owner,
      request,
      response,
      settlement,
      observation,
      observedEpoch: settlementMount.mountEpoch,
      savedBeforeTable: proposal.journeyCapture.savedBeforeTable,
      savedAfterTable: await readRenderedSourceTable(),
      rowExpansionLifecycle: {
        ...lifecycle,
        savedBefore: savedBuilderProjection(savedBefore),
        savedAfter: savedBuilderProjection(builder),
      },
    });
  } else {
    const lifecycle = proposal.journeyCapture.groupLifecycle;
    assert(lifecycle, `${label} must bind its native proposal to a Group lifecycle capture.`);
    journeyTrace.actions.push({
      label: journeyLabel,
      event: journeyLabel === 'Group' ? 'group' : 'edit-group',
      owner,
      request,
      response,
      settlement,
      observation,
      observedEpoch: settlementMount.mountEpoch,
      groupLifecycle: {
        ...mount,
        candidateIntent: lifecycle.candidateIntent,
        candidateOwner: lifecycle.owner,
        savedGroupInput: groupProjection(savedBefore),
        savedBefore: savedBuilderProjection(savedBefore),
        savedAfter: savedBuilderProjection(builder),
      },
    });
  }
  recordAction(label, startedAt, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  return builder;
};
const editGroupAggregate = async (operation, expectedRows, state, label, attempt = 'P1') => {
  const groupStep = doc(state).construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert(groupStep, 'Saved construction must contain the authored Group');
  const stepId = groupStep.id;
  const startIndex = nativeCapture.requests.length;
  await click(page, `[data-testid="construction-history-step-${stepId}"]`);
  await click(page, `[data-testid="construction-edit-step-${stepId}"]`);
  await waitForControl(page, 'select[aria-label="Summary 1"]', { enabled: true });
  const startedAt = Date.now();
  await selectOption(page, 'select[aria-label="Summary 1"]', operation);
  let selectedField;
  if (operation === 'COUNT_DISTINCT') {
    await waitForControl(page, 'select[aria-label="Summary field 1"]', { enabled: true });
    const options = await browserEval(page, () => [...document.querySelector('select[aria-label="Summary field 1"]').options]
      .map(option => ({ value: option.value, label: option.textContent })));
    selectedField = options.find((field) => field.label.startsWith('Observation FHIR resource ID'));
    assert(selectedField, `Active related Observation identity must be available to Group before a downstream projection: ${JSON.stringify(options)}`);
    await selectOption(page, 'select[aria-label="Summary field 1"]', selectedField.value);
  }
  const proposal = await browserProposal({ requestStart: startIndex,
    path: base + '/construction-proposals', expectedRows, state, label, startedAt, captureKind: 'group', attempt });
  const group = proposal.response.candidateConstruction.steps.find((step) => step.id === stepId);
  assert(group?.operation.kind === 'GROUP');
  assert.equal(group.operation.group.aggregates[0].operation, operation);
  if (selectedField) assert.equal(group.operation.group.aggregates[0].inputColumnId, selectedField.value);
  return { ...proposal, groupStepId: stepId };
};

try {
  report.ownedApiRuntimeProof = assertCdaNoAuthRuntime({ apiContainer: target.apiContainer });
  const sourceQuery = `
FOR s IN (
  FOR candidate IN Specimen
    FILTER candidate.project == ${JSON.stringify(project)} AND candidate.dataset_generation == ${JSON.stringify(generation)}
      AND candidate.resourceType == "Specimen" AND candidate.payload.resourceType == "Specimen"
    SORT candidate.id
    LIMIT 2000
    RETURN {id:candidate.id,_id:candidate._id,generation:candidate.dataset_generation,resourceType:candidate.resourceType}
)
  LET patients = (
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e._to != null AND e.label == "subject_Patient"
        AND e.from_type == "Specimen" AND e.to_type == "Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      LET p = DOCUMENT(e._to)
      FILTER p != null AND p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
        AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
      RETURN DISTINCT {id:p.id,_id:p._id,resourceType:p.resourceType,generation:p.dataset_generation}
  )
  FILTER LENGTH(patients) == 1
  LET patient = patients[0]
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == patient._id
        AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)}
        AND e.from_type == "Observation" AND e.to_type == "Patient"
        AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o != null AND o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
        AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"
      SORT o.id
      RETURN {id:o.id,_id:o._id,status:o.payload.status,hasScalarStatus:IS_STRING(o.payload.status),resourceType:o.resourceType,generation:o.dataset_generation}
  )
  LET scalarObservations = (FOR observation IN observations FILTER observation.hasScalarStatus RETURN observation)
  LET statuses = UNIQUE(scalarObservations[*].status)
  FILTER LENGTH(observations) >= 2 AND LENGTH(observations) <= 24
    AND LENGTH(scalarObservations) == LENGTH(observations)
  SORT s.id
  LIMIT 1
  RETURN {specimen:s,patient,observations,statuses}
`;
  const [witness] = rawQuery(sourceQuery);
  assert(witness?.specimen?.id && witness?.specimen?._id && witness?.patient?.id && witness?.observations?.length >= 2,
    'Bounded raw CDA scan must find a Specimen→Patient witness with 2–24 distinct Observations, all carrying scalar status');
  source = witness.specimen;
  const patient = witness.patient;
  const observations = witness.observations;
  const observationIDs = observations.map(({ id }) => id);
  const observationStatuses = observations.map(({ status }) => status).sort();
  assert(observations.every(({ hasScalarStatus }) => hasScalarStatus), 'Every selected Observation must carry scalar status');
  assert.equal(source.generation, generation);
  assert.equal(source.resourceType, 'Specimen');
  assert.equal(patient.resourceType, 'Patient');
  assert.equal(patient.generation, generation);
  assert.equal(new Set(observationIDs).size, observations.length, 'Raw witness Observation IDs must be unique');
  assert(observationIDs.length >= 2, 'Raw witness must contain at least two distinct Observation IDs');
  assert(observations.length <= 24, 'Raw witness must fit completely in the 25-row preview bound');
  assert(observations.every(({ resourceType, generation: observationGeneration }) =>
    resourceType === 'Observation' && observationGeneration === generation),
  'Every selected Observation must retain exact resource type and generation identity');
  recordLifecycleCheck('correctness',
    'bounded real CDA oracle selects exact Specimen→Patient→Observation status witness',
    source.generation === generation && observationIDs.length === observationStatuses.length &&
      observationIDs.length >= 2 && observationIDs.length <= 24 && observations.every(({ hasScalarStatus }) => hasScalarStatus),
    { project, generation, sourceID: source.id, patientID: patient.id, observationIDs,
      statusValues: observationStatuses });
  report.oracle = { kind: 'bounded real CDA source fixture and exact project/generation-scoped raw fhir_edge witness',
    sourceQuery, searchBounds: { sortedSpecimens: 2000, observationsPerPatientMin: 2, observationsPerPatientMax: 24 },
    source, patient, observations, statusValuesPresent: [...new Set(observationStatuses)].sort(),
    statusMultiplicityPreserved: true, chain: [] };

  const createdExplorer = await api(root, { name: requestedExplorerName, title: 'Group edit before related column QA' });
  const creationRequest = report.requests.at(-1);
  assert.equal(creationRequest?.path, root);
  assert.equal(creationRequest?.status, 201);
  assert.equal(creationRequest?.body?.name, requestedExplorerName);
  const createdScope = createdExplorerScope(project, createdExplorer);
  explorer = createdScope.explorerId;
  assert.notEqual(explorer, protectedExplorer, 'The server-assigned Explorer must remain isolated from the protected shared Explorer');
  explorerRoot = createdScope.explorerRoot;
  base = createdScope.authoringBase;
  report.explorer = explorer;
  report.target = { ...report.target, explorer };
  report.explorerProvisioning = {
    requestedName: requestedExplorerName,
    createRequestId: creationRequest.requestId,
    createStatus: creationRequest.status,
    returnedProject: createdExplorer.project,
    returnedExplorerId: explorer,
  };
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const node = builder.catalog.nodes.find((item) => item.resourceType === 'Specimen' && item.rowRootEligible);
  assert(node, 'The real CDA Specimen collection must be loaded');
  await command([{ type: 'CREATE_TABLE', title: 'Group edit before related column', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find((candidate) => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'The CDA catalog must advertise Specimen.id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selectionPath = base.replace('/authoring/v2', '/selections');
  selection = await api(selectionPath, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: 'Specimen', id: source.id }] } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Specimen');
  assert.equal(selection.memberCount, 1);
  assert(selection.scopeDigest);
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken,
    outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'The raw Specimen source must attach directly to the table');
  selectedPopulationRoute = direct.route;
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  assertSourceBinding(builder, selectedPopulationRoute);

  await installBrowserCapture();
  const sourceRows = [[source.id]];
  await open(sourceRows, 1, 'selected-raw-Specimen-reload');
  let witnesses = [{ anchor: source._id, values: [source.id] }];
  const chain = [
    { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND',
      expectedStage: 'source_projection' },
    { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND',
      expectedStage: 'authored_construction' },
  ];
  let expectedExpandedRows = sourceRows;
  for (const hop of chain) {
    const relatedContext = { ...relatedChoiceStageContext(doc(builder), hop.expectedStage), targetResourceType: hop.to };
    assert(relatedContext.anchorColumnId,
      `Current ${hop.from} related stage must expose a stable anchor column before opening its editor`);
    const next = [];
    for (const witness of witnesses) {
      const endpoint = hop.direction === 'OUTBOUND' ? '_from' : '_to';
      const target = hop.direction === 'OUTBOUND' ? '_to' : '_from';
      const query = `FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)} FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to + '/')}) LET d=DOCUMENT(e.${target}) FILTER d.project==${JSON.stringify(project)} AND d.dataset_generation==${JSON.stringify(generation)} RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches = witness.anchor ? rawQuery(query) : [];
      if (matches.length) for (const match of matches) next.push({ anchor: match._id, values: [...witness.values, match.id] });
      else next.push({ anchor: null, values: [...witness.values, '—'] });
    }
    assert(next.length <= 1000, 'CDA source chain must remain bounded to at most 1000 witnesses');
    witnesses = next;
    expectedExpandedRows = witnesses.map((witness) => witness.values);
    report.oracle.chain.push({ hop, witnesses });
    const startIndex = nativeCapture.requests.length;
    await click(page, '[data-testid="construction-rows-settings-trigger"]');
    await waitForControl(page, '[data-testid="construction-action-related-rows"]', { enabled: true });
    await click(page, '[data-testid="construction-action-related-rows"]');
    const editor = '[data-testid="construction-related-expand-editor"]';
    await waitForControl(page, editor + ' select[aria-label="Related record type"]', { enabled: true });
    const startedAt = Date.now();
    await selectOption(page, editor + ' select[aria-label="Related record type"]', hop.to);
    const routeLabel = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
    await waitForControl(page, `${editor} input[aria-label="${routeLabel}"]`, { timeout: 5000 });
    await click(page, editor + ` input[aria-label="${routeLabel}"]`);
    const expandedProposal = await browserProposal({ requestStart: startIndex, path: base + '/construction-proposals',
      expectedRows: expectedExpandedRows, state: builder, label: `expand-${hop.from}-${hop.to}`, startedAt,
      captureKind: 'expandRows', attempt: 'P1' });
    builder = await applyGroupProposal(expandedProposal, builder, expectedExpandedRows,
      `apply expand-${hop.from}-${hop.to}`, { journeyLabel: 'ExpandRows' });
    await closeRowSettings();
    assertSourceBinding(builder, selectedPopulationRoute);
  }
  assert(witnesses.length > 1, 'The chosen CDA Specimen must reach multiple Observations');
  const contributorIDs = [...new Set(witnesses.map((witness) => witness.values.at(-1)))].sort();
  assert(contributorIDs.length > 1);
  const pipelineObservationIDs = new Set(witnesses.map((witness) => witness.values.at(-1)));
  assert(!pipelineObservationIDs.has('—'), 'The selected Specimen must have concrete related Observations');
  assert.deepEqual([...pipelineObservationIDs].sort(), [...observationIDs].sort(),
    'The bounded source finder and exact pipeline edge oracle must identify the same Observation set');
  const rawRouteRows = observations.map(({ id: observationID }) => {
    const matching = witnesses.find(({ values }) => values.at(-1) === observationID);
    assert(matching, `Raw Observation ${observationID} must have one exact native-route witness`);
    return matching.values;
  });
  assertCompletePopulationRowsMatchOracle(expectedExpandedRows, rawRouteRows, { label: 'native related rows against raw route oracle' });
  recordLifecycleCheck('correctness',
    'native Specimen→Patient and Patient→Observation choices preserve exact raw edges',
    expectedExpandedRows.length === observations.length && !pipelineObservationIDs.has('—'),
    { chain, expectedRows: expectedExpandedRows, rawObservationIDs: [...pipelineObservationIDs].sort() });
  const groupedRows = [[patient.id, witnesses.length]];
  const distinctRows = [[patient.id, contributorIDs.length]];

  const groupStartIndex = nativeCapture.requests.length;
  await click(page, '[data-testid="construction-rows-settings-trigger"]');
  await click(page, '[data-testid="construction-action-group-rows"]');
  await waitForControl(page, 'input[aria-label="Group by Patient FHIR resource ID"]', { enabled: true });
  const groupStartedAt = Date.now();
  await click(page, 'input[aria-label="Group by Patient FHIR resource ID"]');
  const initialGroupProposal = await browserProposal({ requestStart: groupStartIndex, path: base + '/construction-proposals',
    expectedRows: groupedRows, state: builder, label: 'group-by-Specimen-ID-count-rows', startedAt: groupStartedAt,
    captureKind: 'group', attempt: 'P1' });
  builder = await applyGroupProposal(initialGroupProposal, builder, groupedRows, 'apply-initial-Group', {
    journeyLabel: 'Group',
  });
  const groupBaseline = structuredClone(builder);
  const groupDocumentBaseline = structuredClone(doc(builder));
  assertSourceBinding(builder, selectedPopulationRoute);
  const groupedReload = await open(groupedRows, 2, 'reload-initial-Group');
  assert.deepEqual(doc(groupedReload).construction, groupDocumentBaseline.construction);
  recordLifecycleCheck('correctness',
    'initial Group aggregates exact related Observation rows',
    doc(groupedReload).construction.steps.some((step) => step.operation.kind === 'GROUP') &&
      JSON.stringify(groupedReload.workspace) === JSON.stringify(groupBaseline.workspace),
    { outputId, groupStepIDs: doc(groupedReload).construction.steps.filter((step) => step.operation.kind === 'GROUP').map((step) => step.id),
      groupedRows, draftVersion: groupedReload.draftVersion, draftDigest: groupedReload.draftDigest });

  // Distinct uncovered transition: commit the upstream Group edit while the
  // downstream Observation.status column does not exist yet.
  const beforeFirstEdit = structuredClone(builder);
  const countDistinctProposal = await editGroupAggregate('COUNT_DISTINCT', distinctRows, builder,
    'edit-Group-to-count-distinct-before-column', 'P1');
  assert.deepEqual(countDistinctProposal.panel.rows[0].slice(0, 2).map((cell) => cell.text), distinctRows[0].map(String));
  recordLifecycleCheck('correctness',
    'COUNT_DISTINCT Group proposal previews the exact bounded raw Observation identity count',
    countDistinctProposal.response.preview?.rowCount === 1 &&
      countDistinctProposal.panel.rows.length === 1 &&
      countDistinctProposal.panel.rows[0].slice(0, 2).map((cell) => cell.text).join('\u0000') === distinctRows[0].map(String).join('\u0000'),
    { rows: countDistinctProposal.panel.rows, expectedRows: distinctRows,
      candidateGroup: countDistinctProposal.group, proposalId: countDistinctProposal.response.proposalId });
  const cancelStartedAt = Date.now();

  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { hidden: true });
  await waitForSavedStepEditorClosed();
  await mountedRows(groupedRows, 2);
  const afterEditCancel = await api(base + '/builder');
  assert.equal(afterEditCancel.draftVersion, beforeFirstEdit.draftVersion);
  assert.equal(afterEditCancel.draftDigest, beforeFirstEdit.draftDigest);
  assert.deepEqual(afterEditCancel.workspace, beforeFirstEdit.workspace, 'Group edit Cancel must preserve the exact source and draft bindings');
  await recordGroupEditCancel({
    proposal: countDistinctProposal,
    savedBefore: beforeFirstEdit,
    savedAfter: afterEditCancel,
    observedAt: cancelStartedAt,
  });
  recordAction('cancel-Group-edit-before-column', cancelStartedAt);
  recordLifecycleCheck('persistence',
    'Group COUNT_DISTINCT edit Cancel preserves exact prior draft before adding related column',
    afterEditCancel.draftVersion === beforeFirstEdit.draftVersion &&
      afterEditCancel.draftDigest === beforeFirstEdit.draftDigest &&
      JSON.stringify(afterEditCancel.workspace) === JSON.stringify(beforeFirstEdit.workspace),
    { draftVersion: afterEditCancel.draftVersion, draftDigest: afterEditCancel.draftDigest,
      workspace: afterEditCancel.workspace });

  const appliedDistinctProposal = await editGroupAggregate('COUNT_DISTINCT', distinctRows, afterEditCancel,
    'reapply-Group-count-distinct-before-column', 'P2');
  builder = await applyGroupProposal(appliedDistinctProposal, afterEditCancel, distinctRows,
    'apply-Group-edit-before-column', { journeyLabel: 'EditGroup' });
  await waitForSavedStepEditorClosed();
  const distinctDocument = structuredClone(doc(builder));
  const reloadedDistinct = await open(distinctRows, 2, 'reload-Group-edit-before-column');
  assert.deepEqual(doc(reloadedDistinct).construction, distinctDocument.construction);
  const savedGroupStep = doc(reloadedDistinct).construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert.equal(savedGroupStep.operation.group.aggregates[0].operation, 'COUNT_DISTINCT');
  assert.equal(doc(reloadedDistinct).construction.steps.some((step) => step.operation.kind === 'RELATED_SOURCE'), false,
    'The downstream related source must still be absent after the upstream edit reload');
  recordLifecycleCheck('persistence',
    'Group COUNT_DISTINCT Apply and reload persist exact pre-column Group without RELATED_SOURCE',
    doc(reloadedDistinct).construction.steps.find((step) => step.operation.kind === 'GROUP')?.operation.group.aggregates[0]?.operation === 'COUNT_DISTINCT' &&
      !doc(reloadedDistinct).construction.steps.some((step) => step.operation.kind === 'RELATED_SOURCE') &&
      JSON.stringify(doc(reloadedDistinct).construction) === JSON.stringify(distinctDocument.construction),
    { outputId, construction: doc(reloadedDistinct).construction, rows: distinctRows,
      draftVersion: reloadedDistinct.draftVersion, draftDigest: reloadedDistinct.draftDigest });

  // Add the related field only after the upstream Group edit has been saved and reloaded.
  const beforeFieldAdd = structuredClone(builder);
  const observationNodeIds = new Set(beforeFieldAdd.catalog.nodes
    .filter((node) => node.resourceType === 'Observation').map((node) => node.nodeId));
  const statusCandidates = beforeFieldAdd.catalog.candidates.filter((candidate) =>
    observationNodeIds.has(candidate.nodeId) && candidate.fieldPath === 'status');
  assert(statusCandidates.length > 0, 'The current CDA catalog must expose Observation.status candidates');
  for (const candidate of statusCandidates) {
    assert.equal(candidate.logicalType, 'string');
    assert.equal(candidate.cardinality, 'optional_one');
    assert(candidate.projectionModes.includes('VALUE'));
  }
  const statusCandidateIds = new Set(statusCandidates.map((candidate) => candidate.candidateId));
  const withFieldTyped = [[patient.id, contributorIDs.length, observationStatuses]];
  const relatedRouteLabel = 'Status: Specimen -[subject]-> Patient <-[subject]- Observation';
  const relatedFieldOptionsRegion = '[data-testid="catalog-selection-options"]';
  const prepareRelatedFieldChooser = async () => {
    const alreadyOpen = await browserEval(page, () => Boolean(document.querySelector('[aria-label="Add columns editor"]')));
    if (!alreadyOpen) await click(page, '[data-testid="construction-action-add-columns"]');
    await click(page, '[aria-label="Column types"] button[data-view="fields"]');
    await waitForControl(page, '[data-testid="construction-add-columns-source"]');
    await click(page, '[data-testid="construction-add-columns-source-option"][data-source-kind="RELATED"][aria-label="Observation, Related resource"]');
    const fieldSelector = 'button[aria-label="Select Observation.status"]';
    await waitForControl(page, `${fieldSelector}:not(:disabled)`);
    const fieldPressed = await browserEval(page, ({ selector }) => document.querySelector(selector)?.getAttribute('aria-pressed') === 'true', { selector: fieldSelector });
    if (!fieldPressed) await click(page, fieldSelector);
    await waitForControl(page, `${fieldSelector}[aria-pressed="true"]`);
    await click(page, '[aria-label="Selected feature action"] button', { includes: 'Add 1 selected feature' });
    const optionsRegion = relatedFieldOptionsRegion;
    await waitForControl(page, optionsRegion);
    const routeSelector = `${optionsRegion} input[aria-label=${JSON.stringify(relatedRouteLabel)}]`;
    const routeState = await browserEval(page, ({ selector }) => {
      const route = document.querySelector(selector);
      const alternatives = route?.closest('details[data-testid^="catalog-route-alternatives-"]');
      return {
        count: document.querySelectorAll(selector).length,
        hiddenByClosedAlternatives: Boolean(alternatives && !alternatives.open),
        alternativeSummary: alternatives?.querySelector('summary')?.innerText ?? '',
      };
    }, { selector: routeSelector });
    assert.equal(routeState.count, 1, 'Advanced choices must contain exactly one radio for the intended route');
    if (routeState.hiddenByClosedAlternatives) {
      assert(routeState.alternativeSummary.toLowerCase().includes('relationship path'),
        'The intended route must be inside the native relationship path disclosure');
      await click(page, `${optionsRegion} [data-testid^="catalog-route-alternatives-"] > summary`, { includes: 'relationship path' });
    }
    await waitForControl(page, routeSelector);
    await click(page, routeSelector);
    const formSelector = `${optionsRegion} input[aria-label="Status: Keep all matching values"]`;
    await waitForControl(page, formSelector);
    await click(page, formSelector);
    const policySelector = '[data-testid="construction-operation-editor"] select[aria-label="Values per grouped row"]';
    await selectOption(page, policySelector, 'ALL');
    const selected = await browserEval(page, ({ routeSelector: route, formSelector: form }) => {
      const options = document.querySelector('[data-testid="catalog-selection-options"]');
      const editor = document.querySelector('[data-testid="construction-operation-editor"]');
      return { route: options?.querySelector(route)?.checked, form: options?.querySelector(form)?.checked,
        policy: editor?.querySelector('select[aria-label="Values per grouped row"]')?.value };
    }, { routeSelector, formSelector });
    assert.deepEqual(selected, { route: true, form: true, policy: 'ALL' }, 'Chooser must bind exact source route, ALL matches, and grouped-row ALL policy');
  };
  const proposeRelatedField = async (state, label, attempt = 'P1') => {
    const startedAt = Date.now();
    const requestStart = nativeCapture.requests.length;
    await prepareRelatedFieldChooser();
    await click(page, `${relatedFieldOptionsRegion} button`, { name: 'Preview related columns' });
    const proposal = await browserProposal({ requestStart, path: base + '/construction-proposals',
      expectedRows: withFieldTyped, state, label, startedAt, captureKind: 'relatedSource', attempt });
    const match = relatedSourceProposalCandidate({ path: proposal.request.path, request: proposal.request.request }, {
      resourceType: 'Observation', path: 'status',
    });
    assert(match, `${label} must use the current native RELATED_SOURCE construction proposal`);
    assert(statusCandidateIds.has(match.related.source.candidateId),
      'The native related proposal must bind one of the current Observation.status catalog candidates');
    assert.equal(match.related.form, 'ALL');
    assert.equal(match.related.contributorRule?.policy, 'ALL_MATCHES');
    assert.equal(match.rowValuePolicy, 'ALL');
    assert.equal(match.related.source.logicalType, 'string');
    assert.equal(match.related.source.cardinality, 'optional_one');
    assert.equal(match.related.route.length, 2);
    assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType }) => [fromResourceType, toResourceType]),
      [['Specimen', 'Patient'], ['Patient', 'Observation']]);
    const candidateGroup = proposal.request.request.candidateConstruction.steps.find((step) => step.id === savedGroupStep.id);
    assert(candidateGroup?.operation.kind === 'GROUP');
    assert.deepEqual(match.step.inputs, [{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }],
      'The downstream RELATED_SOURCE must consume the edited Group step output');
    assert(match.related.outputColumnId, 'RELATED_SOURCE must expose a stable output column ID');
    assert(match.step.outputs.some((output) => output.id === match.related.outputColumnId),
      'RELATED_SOURCE step must own its proposed output identity');
    assert(match.related.choiceId, 'The current signed route choice identity must be captured');
    const cells = await browserEval(page, () => [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')]
      .map(cell => ({ text: cell.innerText, raw: cell.title })));
    assert.equal(cells.length, 3);
    assert.deepEqual(cells.slice(0, 2).map((cell) => cell.text), withFieldTyped[0].slice(0, 2).map(String));
    const previewStatuses = JSON.parse(cells[2].raw);
    assert(Array.isArray(previewStatuses) && previewStatuses.every((value) => typeof value === 'string'));
    assert.deepEqual([...previewStatuses].sort(), observationStatuses,
      'Typed automatic preview must equal the scoped raw Observation.status multiset');
    return { ...proposal, match };
  };
  const fieldProposal = await proposeRelatedField(beforeFieldAdd, 'automatic-Observation-status-RELATED_SOURCE-preview-after-Group-edit', 'P1');
  const statusCandidateId = fieldProposal.match.related.source.candidateId;
  recordLifecycleCheck('correctness',
    'Observation.status RELATED_SOURCE proposal matches the exact raw status multiset and edited Group input',
    fieldProposal.response.preview?.rowCount === 1 &&
      JSON.stringify(fieldProposal.match.step.inputs) === JSON.stringify([{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }]) &&
      fieldProposal.match.related.source.path === 'status',
    { candidateId: statusCandidateId, relatedSource: fieldProposal.match.related,
      preview: fieldProposal.response.preview, expectedStatuses: observationStatuses });

  const fieldCancelStartedAt = Date.now();

  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(distinctRows, 2);
  const afterFieldCancel = await api(base + '/builder');
  assert.equal(afterFieldCancel.draftVersion, beforeFieldAdd.draftVersion);
  assert.equal(afterFieldCancel.draftDigest, beforeFieldAdd.draftDigest);
  assert.deepEqual(afterFieldCancel.workspace, beforeFieldAdd.workspace, 'Related-column Cancel must preserve exact Group edit and source bindings');
  await recordRelatedSourceCancel({
    proposal: fieldProposal,
    savedBefore: beforeFieldAdd,
    savedAfter: afterFieldCancel,
    observedAt: fieldCancelStartedAt,
  });
  recordAction('cancel-related-column-after-Group-edit', fieldCancelStartedAt);
  recordLifecycleCheck('persistence',
    'Related-column Cancel preserves exact COUNT_DISTINCT Group workspace and draft CAS',
    afterFieldCancel.draftVersion === beforeFieldAdd.draftVersion &&
      afterFieldCancel.draftDigest === beforeFieldAdd.draftDigest &&
      JSON.stringify(afterFieldCancel.workspace) === JSON.stringify(beforeFieldAdd.workspace),
    { draftVersion: afterFieldCancel.draftVersion, draftDigest: afterFieldCancel.draftDigest,
      workspace: afterFieldCancel.workspace });

  // Cancellation clears the proposed candidate. Reopen the same native chooser
  // and capture a new proposal before Apply so receipt and draft bindings are fresh.
  const applyProposal = await proposeRelatedField(afterFieldCancel, 'reopened-Observation-status-RELATED_SOURCE-preview-before-Apply', 'P2');
  assert.notEqual(applyProposal.response.proposalId, fieldProposal.response.proposalId,
    'The reopened chooser must produce a fresh proposal receipt after Cancel');
  assert.equal(applyProposal.match.related.choiceId, fieldProposal.match.related.choiceId,
    'The reopened chooser must retain the same signed source route choice');
  assert.equal(applyProposal.match.related.source.candidateId, statusCandidateId,
    'The reopened chooser must retain the exact selected catalog candidate');
  const fieldApplyStartedAt = Date.now();
  const applyRequestStart = nativeCapture.requests.length;
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(withFieldTyped, 3);
  const choiceApply = await waitForNativeProposalApply(applyRequestStart, applyProposal.response.proposalId);
  assertDraftRequest(choiceApply, afterFieldCancel, 'apply related Observation.status column');
  assert(choiceApply.request.commands.some((item) => item.proposalId === applyProposal.response.proposalId));
  builder = await api(base + '/builder');
  assert.equal(choiceApply.response.draftVersion, afterFieldCancel.draftVersion + 1);
  assert.equal(choiceApply.response.draftDigest, applyProposal.response.candidateWorkspaceDigest);
  assert.equal(builder.draftVersion, applyProposal.response.draftVersion + 1);
  assert.equal(builder.draftDigest, applyProposal.response.candidateWorkspaceDigest);
  await recordRelatedSourceApply({
    proposal: applyProposal,
    applyRequest: choiceApply,
    savedBefore: afterFieldCancel,
    savedAfter: builder,
  });
  recordAction('apply-related-column-after-Group-edit', fieldApplyStartedAt,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
  const withFieldDocument = structuredClone(doc(builder));
  const savedGroupAfterRelated = withFieldDocument.construction.steps.find((step) => step.id === savedGroupStep.id);
  assert.deepEqual(savedGroupAfterRelated, distinctDocument.construction.steps.find((step) => step.id === savedGroupStep.id),
    'Adding the related column must retain the exact previously edited Group step and output identities');
  assertSourceBinding(builder, selectedPopulationRoute);
  const relatedStep = withFieldDocument.construction.steps.find((step) => step.operation.kind === 'RELATED_SOURCE'
    && step.operation.relatedSource?.source?.candidateId === statusCandidateId);
  assert(relatedStep, 'Saved construction must bind Observation.status through a native RELATED_SOURCE step');
  assert.equal(relatedStep.id, applyProposal.match.step.id, 'Applying the proposal must preserve the stable related step identity');
  assert.deepEqual(relatedStep.inputs, [{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }]);
  assert.deepEqual(relatedStep.operation.relatedSource, applyProposal.match.related);
  assert.equal(relatedStep.operation.relatedSource.rowValuePolicy ?? 'ALL', 'ALL');
  const relatedOutput = relatedStep.outputs.find((output) => output.id === relatedStep.operation.relatedSource.outputColumnId);
  assert(relatedOutput, 'Saved RELATED_SOURCE must own its stable output column identity');
  const relatedColumn = { column: relatedOutput.name, columnId: relatedOutput.id, label: relatedOutput.label, logicalType: relatedOutput.type };
  assert.equal(relatedOutput.type, 'string');
  const proposalOutput = applyProposal.response.preview.columns.find((column) => column.column === relatedColumn.column);
  assert(proposalOutput, 'The accepted preview must bind the exact saved RELATED_SOURCE output identity');
  assert.equal(proposalOutput.label, relatedColumn.label);
  assert.equal(withFieldDocument.population.selectionRevisionId, selection.id);
  assert.deepEqual(withFieldDocument.population.route, selectedPopulationRoute);
  recordLifecycleCheck('persistence',
    'Applying Observation.status RELATED_SOURCE retains the edited Group and exact source binding',
    JSON.stringify(savedGroupAfterRelated) === JSON.stringify(distinctDocument.construction.steps.find((step) => step.id === savedGroupStep.id)) &&
      relatedStep.id === applyProposal.match.step.id &&
      relatedStep.operation.relatedSource.source.candidateId === statusCandidateId &&
      JSON.stringify(relatedStep.inputs) === JSON.stringify([{ kind: 'STEP_OUTPUT', stepId: savedGroupStep.id }]),
    { groupStep: savedGroupAfterRelated, relatedStep, selectionRevisionId: selection.id,
      route: withFieldDocument.population.route });

  const reloadedField = await open(withFieldTyped, 3, 'reload-related-column-after-Group-edit');
  assert.deepEqual(doc(reloadedField), withFieldDocument, 'Reload must preserve the exact field occurrence, ALL policy, and Group edit');
  recordLifecycleCheck('persistence',
    'Reload preserves the exact Group and related Observation.status field construction',
    JSON.stringify(doc(reloadedField)) === JSON.stringify(withFieldDocument),
    { construction: doc(reloadedField).construction, columns: doc(reloadedField).columns });

  // Round-trip the upstream aggregate with the related source present, then remove
  // its owning native step through an automatic proposal and prove restoration.
  const beforeRestoreEdit = structuredClone(builder);
  const countRowsProposal = await editGroupAggregate('COUNT_ROWS', withFieldTyped, builder,
    'edit-Group-back-to-count-rows-with-related-column', 'P1');
  const restoreEditCancelStartedAt = Date.now();

  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { hidden: true });
  await waitForSavedStepEditorClosed();
  await mountedRows(withFieldTyped, 3);
  const afterRestoreCancel = await api(base + '/builder');
  assert.deepEqual(afterRestoreCancel.workspace, beforeRestoreEdit.workspace);
  assert.equal(afterRestoreCancel.draftDigest, beforeRestoreEdit.draftDigest);
  recordAction('cancel-Group-round-trip-edit', restoreEditCancelStartedAt);
  await recordGroupEditCancel({
    proposal: countRowsProposal,
    savedBefore: beforeRestoreEdit,
    savedAfter: afterRestoreCancel,
    observedAt: restoreEditCancelStartedAt,
  });
  recordLifecycleCheck('persistence',
    'Group round-trip Cancel preserves downstream RELATED_SOURCE and exact saved draft',
    afterRestoreCancel.draftVersion === beforeRestoreEdit.draftVersion &&
      afterRestoreCancel.draftDigest === beforeRestoreEdit.draftDigest &&
      JSON.stringify(afterRestoreCancel.workspace) === JSON.stringify(beforeRestoreEdit.workspace),
    { draftVersion: afterRestoreCancel.draftVersion, draftDigest: afterRestoreCancel.draftDigest,
      workspace: afterRestoreCancel.workspace });

  const restoreProposal = await editGroupAggregate('COUNT_ROWS', withFieldTyped, afterRestoreCancel,
    'reapply-Group-count-rows-with-related-column', 'P2');
  builder = await applyGroupProposal(restoreProposal, afterRestoreCancel, withFieldTyped,
    'apply-Group-round-trip-with-related-column', { journeyLabel: 'EditGroupAgain' });
  await waitForSavedStepEditorClosed();
  assert.deepEqual(doc(builder).columns, withFieldDocument.columns,
    'Editing Group must preserve the exact downstream field occurrence and ALL source binding');
  assert.equal(doc(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_SOURCE')?.id, relatedStep.id,
    'Editing Group must preserve the downstream RELATED_SOURCE step identity');
  assertSourceBinding(builder, selectedPopulationRoute);
  const countRowsWithFieldDocument = structuredClone(doc(builder));
  const reloadedCountRows = await open(withFieldTyped, 3, 'reload-Group-round-trip-with-related-column');
  assert.deepEqual(doc(reloadedCountRows), countRowsWithFieldDocument);
  assert.equal(doc(reloadedCountRows).construction.steps.find((step) => step.operation.kind === 'GROUP').operation.group.aggregates[0].operation, 'COUNT_ROWS');
  recordLifecycleCheck('persistence',
    'Group round-trip Apply and reload preserve exact downstream related field bindings',
    JSON.stringify(doc(reloadedCountRows)) === JSON.stringify(countRowsWithFieldDocument) &&
      doc(reloadedCountRows).construction.steps.find((step) => step.operation.kind === 'GROUP')
        .operation.group.aggregates[0].operation === 'COUNT_ROWS',
    { construction: doc(reloadedCountRows).construction, columns: doc(reloadedCountRows).columns });

  const proposeRelatedStepRemoval = async (state, label, attempt = 'P1') => {
    const step = doc(state).construction.steps.find((candidate) => candidate.id === relatedStep.id);
    assert(step?.operation.kind === 'RELATED_SOURCE');
    const requestStart = nativeCapture.requests.length;
    const adjustmentGroupSelector = `[data-testid="construction-history-adjustment-group-${step.id}"]`;
    const stepSelector = `[data-testid="construction-history-step-${step.id}"]`;
    await waitForBrowser(page, ({ selector }) => document.querySelectorAll(selector).length === 1,
      5000, { selector: adjustmentGroupSelector });
    const adjustmentGroupOpen = await browserEval(page, ({ selector }) => {
      const group = document.querySelector(selector);
      return group?.tagName === 'DETAILS' ? group.open : null;
    }, { selector: adjustmentGroupSelector });
    assert.equal(typeof adjustmentGroupOpen, 'boolean', 'Related-source history must belong to one column-adjustment disclosure');
    if (!adjustmentGroupOpen) await click(page, `${adjustmentGroupSelector} > summary`);
    await waitForControl(page, stepSelector, { timeout: 5000, enabled: true });
    await click(page, stepSelector);
    const removeSelector = `[data-testid="construction-remove-step-${step.id}"]:not(:disabled)`;
    await waitForControl(page, removeSelector, { timeout: 5000 });
    const startedAt = Date.now();
    await click(page, `[data-testid="construction-remove-step-${step.id}"]`);
    const proposal = await browserProposal({ requestStart, path: base + '/construction-proposals',
      expectedRows: groupedRows, state, label, startedAt, captureKind: 'relatedSource', attempt });
    assert.deepEqual(proposal.request.request.removeStepIds, [relatedStep.id],
      'Removal must target only the newly added downstream RELATED_SOURCE step');
    assert.deepEqual(proposal.response.candidateConstruction.steps, groupDocumentBaseline.construction.steps,
      'The removal candidate must restore the exact original Group construction');
    return proposal;
  };
  const beforeRemoveCancel = structuredClone(builder);
  const removePreview = await proposeRelatedStepRemoval(beforeRemoveCancel,
    'automatic-remove-RELATED_SOURCE-preview-cancel-target', 'P1');
  const removeCancelStartedAt = Date.now();

  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForControl(page, '[data-testid="construction-proposal-panel"]', { timeout: 5000, hidden: true });
  await mountedRows(withFieldTyped, 3);
  const afterRemoveCancel = await api(base + '/builder');
  assert.equal(afterRemoveCancel.draftVersion, beforeRemoveCancel.draftVersion);
  assert.equal(afterRemoveCancel.draftDigest, beforeRemoveCancel.draftDigest);
  assert.deepEqual(afterRemoveCancel.workspace, beforeRemoveCancel.workspace,
    'Canceling related-source removal must preserve its exact step, Group, and source bindings');
  await recordRelatedSourceCancel({
    proposal: removePreview,
    savedBefore: beforeRemoveCancel,
    savedAfter: afterRemoveCancel,
    observedAt: removeCancelStartedAt,
  });
  recordAction('cancel-remove-RELATED_SOURCE', removeCancelStartedAt);
  recordLifecycleCheck('persistence',
    'Related-source removal Cancel preserves the exact saved Group and related field workspace',
    afterRemoveCancel.draftVersion === beforeRemoveCancel.draftVersion &&
      afterRemoveCancel.draftDigest === beforeRemoveCancel.draftDigest &&
      JSON.stringify(afterRemoveCancel.workspace) === JSON.stringify(beforeRemoveCancel.workspace),
    { draftVersion: afterRemoveCancel.draftVersion, draftDigest: afterRemoveCancel.draftDigest,
      workspace: afterRemoveCancel.workspace });
  const removeCancelledAt = Date.now();

  const appliedRemovePreview = await proposeRelatedStepRemoval(afterRemoveCancel,
    'reopened-remove-RELATED_SOURCE-preview-before-Apply', 'P2');
  // Proposal IDs are content identities; unchanged candidate content may yield
  // the same ID. Require a new completed request after Cancel and exact binding.
  assertReopenedProposalAfterCancel({
    previous: removePreview,
    cancelledAt: removeCancelledAt,
    current: appliedRemovePreview,
    state: afterRemoveCancel,
    outputId,
    removeStepIds: [relatedStep.id],
  });
  builder = await applyGroupProposal(appliedRemovePreview, afterRemoveCancel, groupedRows,
    'apply-remove-RELATED_SOURCE-to-restore-Group');
  assert.deepEqual(doc(builder).columns, groupDocumentBaseline.columns,
    'Removing the related source must restore the original authored column bindings');
  assert.deepEqual(doc(builder).construction, groupDocumentBaseline.construction,
    'Removing the related source must restore the original Group construction exactly');
  assertSourceBinding(builder, selectedPopulationRoute);
  assert.equal(doc(builder).construction.steps.some((step) => step.operation.kind === 'RELATED_SOURCE'), false,
    'The removed related source must no longer be present in the saved construction');
  const finalReload = await open(groupedRows, 2, 'reload-final-Group-source-restoration');
  assert.deepEqual(finalReload.workspace, groupBaseline.workspace,
    'Final reload must restore the exact Group workspace, source membership, output identity, and column bindings');
  recordLifecycleCheck('persistence',
    'Applying related-source removal and reloading restores the exact original Group workspace',
    JSON.stringify(finalReload.workspace) === JSON.stringify(groupBaseline.workspace) &&
      JSON.stringify(doc(finalReload).construction) === JSON.stringify(groupDocumentBaseline.construction),
    { outputId, workspace: finalReload.workspace, construction: doc(finalReload).construction });

  assert.equal(report.protectedExplorerUntouched, true, 'Protected shared Explorer must remain untouched');
  assert.deepEqual(report.errors, [], 'The native lifecycle must have no HTTP, runtime, or console errors');
  recordLifecycleCheck('correctness',
    'No unexpected browser, authoring, transport, or native HTTP errors occur',
    report.errors.length === 0 && report.protectedExplorerUntouched,
    { errors: report.errors, protectedExplorerUntouched: report.protectedExplorerUntouched });
  const renderCheckpointSummary = groupEditRenderCheckpointSummary(report.cases);
  recordLifecycleCheck('performance',
    'All Group edit action-to-render checkpoints complete within five seconds',
    renderCheckpointSummary.status === 'passed', renderCheckpointSummary);
  assert(journeyTraceStarted && journeyTrace.initialObservation && journeyTrace.initialPreview,
    'The BuilderJourney trace must start from the actual mounted source preview.');
  const relevantProposalRequests = nativeCapture.requests.filter(entry =>
    entry.path === `${base}/construction-proposals` && entry.method === 'POST' &&
    entry.request?.outputId === outputId);
  const unrecordedProposals = relevantProposalRequests.filter(entry =>
    !capturedProposalBrowserRequestIds.has(entry.browserRequestId));
  for (const entry of unrecordedProposals.sort((left, right) => left.startedAt - right.startedAt)) {
    const state = builderStateForProposal(entry);
    const captureKind = proposalCaptureKind(entry, state);
    const priorOwners = new Set();
    for (const action of journeyTrace.actions) {
      const actionKind = action.relatedSourceLifecycle ? 'relatedSource'
        : action.rowExpansionLifecycle ? 'expandRows'
          : action.groupLifecycle && action.request?.path === '/construction-proposals' ? 'group' : null;
      const proposalRequest = action.request?.path === '/construction-proposals'
        ? action.request
        : action.relatedSourceLifecycle?.request;
      const body = proposalRequest?.request;
      if (actionKind === captureKind && proposalRequest?.browserRequestId &&
          String(body?.expectedDraftVersion ?? body?.draftVersion) === String(entry.request?.expectedDraftVersion ?? entry.request?.draftVersion) &&
          (body?.expectedDraftDigest ?? body?.draftDigest) === (entry.request?.expectedDraftDigest ?? entry.request?.draftDigest)) {
        priorOwners.add(proposalRequest.browserRequestId);
      }
    }
    await recordProposalCaptureBatch({
      requestStart: nativeCapture.requests.indexOf(entry),
      observedEntries: [entry],
      chosen: entry,
      state,
      captureKind,
      attempt: priorOwners.size === 0 ? 'P1' : 'P2',
      label: `late-${captureKind}-proposal`,
    });
  }
  const actionTime = action => {
    if (action.label === 'Preview') {
      return action.request?.startedAt ?? action.settlement?.startedAt ?? 0;
    }
    if (action.label === 'RelatedSourcePreview') {
      return action.settlement?.completedAt ?? action.request?.startedAt ?? 0;
    }
    if (action.label === 'PreviewComplete' || action.label === 'PreviewError' ||
        action.label === 'LatePreviewCompleteIgnored' || action.label === 'LatePreviewErrorIgnored' ||
        action.label === 'PreviewRequestCancelled' || action.label === 'ExpandRows' ||
        action.label === 'Group' || action.label === 'EditGroup' || action.label === 'EditGroupAgain' ||
        action.label === 'RelatedSourceApply') {
      return action.settlement?.completedAt ?? action.request?.startedAt ?? 0;
    }
    return action.observedAt ?? 0;
  };
  journeyTrace.actions = journeyTrace.actions
    .map((action, index) => ({ action, index }))
    .sort((left, right) => actionTime(left.action) - actionTime(right.action) || left.index - right.index)
    .map(({ action }) => action);
  const unrecordedAfterFlush = relevantProposalRequests.filter(entry =>
    !capturedProposalBrowserRequestIds.has(entry.browserRequestId));
  assert.deepEqual(unrecordedAfterFlush, [],
    'Every actual intermediate, late, rejected, or accepted proposal request must bind to a trace action.');
  for (const entry of relevantProposalRequests) {
    const state = builderStateForProposal(entry);
    const kind = proposalCaptureKind(entry, state);
    const requestId = entry.browserRequestId;
    if (kind === 'relatedSource') {
      assert.equal(journeyTrace.actions.filter(action => action.label === 'RelatedSourcePreview' &&
        action.request?.browserRequestId === requestId).length, 1,
      `RELATED_SOURCE proposal ${requestId} must map to exactly one RelatedSourcePreview transition.`);
    } else {
      assert.equal(journeyTrace.actions.filter(action => action.label === 'Preview' &&
        action.request?.browserRequestId === requestId).length, 1,
      `Builder proposal ${requestId} must map to exactly one Preview request transition.`);
      assert.equal(journeyTrace.actions.filter(action =>
        ['PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled']
          .includes(action.label) && action.request?.browserRequestId === requestId).length, 1,
      `Builder proposal ${requestId} must map to exactly one captured settlement transition.`);
    }
  }
  const relevantApplyRequests = nativeCapture.requests.filter(entry =>
    entry.path === `${base}/commands` && entry.method === 'POST' &&
    entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL'));
  const unrecordedApplies = relevantApplyRequests.filter(entry =>
    !capturedApplyBrowserRequestIds.has(entry.browserRequestId));
  assert.deepEqual(unrecordedApplies, [],
    'Every actual construction-proposal Apply request must bind to a modeled Group, row-expansion, or RELATED_SOURCE action.');
  for (const entry of relevantApplyRequests) {
    assert.equal(journeyTrace.actions.filter(action =>
      ['Group', 'EditGroup', 'EditGroupAgain', 'ExpandRows', 'RelatedSourceApply'].includes(action.label) &&
      action.request?.browserRequestId === entry.browserRequestId).length, 1,
    `Construction-proposal Apply request ${entry.browserRequestId} must map to exactly one modeled transition.`);
  }
  const readOnlySupport = new Map();
  const unclassifiedRequests = [];
  for (const entry of nativeCapture.requests) {
    const route = entry.path.startsWith(base) ? entry.path.slice(base.length) : entry.path;
    if (route === '/construction-proposals' && entry.method === 'POST' && entry.request?.outputId === outputId &&
        capturedProposalBrowserRequestIds.has(entry.browserRequestId)) continue;
    if (route === '/commands' && entry.method === 'POST' &&
        entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL') &&
        capturedApplyBrowserRequestIds.has(entry.browserRequestId)) continue;
    const support = cdaJourneyReadOnlyRoutes.get(`${entry.method} ${route}`);
    if (!support || entry.isMutation !== false) {
      unclassifiedRequests.push({ method: entry.method, path: entry.path,
        index: entry.index, browserRequestId: entry.browserRequestId, startedAt: entry.startedAt,
        isMutation: entry.isMutation });
      continue;
    }
    const events = readOnlySupport.get(support.kind) ?? { ...support, requests: [] };
    events.requests.push({
      method: entry.method,
      path: route,
      index: entry.index,
      browserRequestId: entry.browserRequestId,
      requestId: entry.requestId ?? null,
      startedAt: entry.startedAt,
      isMutation: entry.isMutation,
    });
    readOnlySupport.set(support.kind, events);
  }
  assert.deepEqual(unclassifiedRequests, [],
    'Every same-origin request after formal Init must be modeled or match an explicit method and read-only route family.');
  journeyTrace.unmodeledEvents = [...readOnlySupport.values()].map(({ kind, reason, requests }) => ({
    kind,
    note: `Captured ${requests.length} supported read-only Builder requests.`,
    classification: 'unsupported',
    reason,
    requests,
  }));
  journeyTrace.nativeRequests = nativeRequestLedgerFrom(nativeCapture.requests);
  const traceResult = writeAndReplayBuilderJourneyTrace({
    scenarioID: cda.scenarioID,
    caseName: cda.caseName,
    journeyTrace,
    report,
    fixture: {
      project,
      generation,
      explorer,
      outputId,
      source: structuredClone(source),
      patient: structuredClone(patient),
      observations: structuredClone(observations),
    },
    repositoryRoot,
  });
  recordLifecycleCheck('correctness',
    'Captured Group and RELATED_SOURCE browser transitions replay through BuilderJourney TLC with the registered action contract',
    traceResult.status === 'pass' && traceResult.replayStatus === 'pass' && traceResult.selfTestStatus === 'pass',
    traceResult);
  report.rawOracleQueries = oracleQueries;
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = await cda.inspect(() => document.body.innerText).catch(String);
  throw error;
} finally {
  await nativeCapture?.stop();
  if (nativeRequestListener) page.off('request', nativeRequestListener);
  if (nativeResponseListener) page.off('response', nativeResponseListener);
  if (nativeRequestFailedListener) page.off('requestfailed', nativeRequestFailedListener);
  report.finished = new Date().toISOString();
  await cda.attachReport('group-edit-before-related-column', report);
}

return report;
}
