import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { spawnSync } from 'node:child_process';
import { assertVisibleRowsMatchOracle } from '../helpers/cda-row-oracle.mjs';
import { proposalPreviewReadinessExpression } from '../helpers/proposal-preview-readiness.mjs';
import { navigateAfterOwnedConstructionCapabilities } from '../helpers/cda-playwright-requests.mjs';
import { choosePostPivotRelatedSourcePair, verifyPostPivotRelatedSourceWitness } from '../helpers/post-pivot-related-source-oracle.mjs';

const generation = 'cda-fhir-v1';
const rawScanLimit = 2000;

const scalar = value => typeof value === 'string' && value.length > 0;
const canonicalRows = rows => [...rows].map(row => JSON.stringify(Object.fromEntries(
  Object.entries(row).sort(([left], [right]) => left.localeCompare(right)),
))).sort();

export const cdaExplorerSelectionsPath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/selections`;

export function isPostPivotRawOracleUnavailable(error) {
  return error?.name === 'RawOracleUnavailableError' && error.rawOracleFailure === true;
}

export function buildRawQueryExecuteString(query, bindVars = {}) {
  return `print(JSON.stringify(db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVars)}).toArray()));`.replaceAll('@', '\\u0040');
}

export function waitForCdaCapturedResponse(cda, tracker, predicate, timeout) {
  return cda.waitForCapturedResponse(tracker, predicate, timeout);
}

export function matchesSavedPreviewRequest(entry, { path, outputId, startedAt }) {
  return entry?.path === path
    && entry.method === 'POST'
    && entry.body?.outputId === outputId
    && scalar(entry.body?.receiptId)
    && entry.startedAt >= startedAt;
}

export function assertSavedPreviewIdentity(entry, preview, outputId) {
  assert.equal(entry.body?.outputId, outputId, 'Saved preview request must belong to the selected output.');
  assert(scalar(entry.body?.receiptId), 'Saved preview request must include its receipt identity.');
  assert.equal(preview.outputId, outputId, 'Saved preview response must belong to the selected output.');
  assert.equal(preview.receiptId, entry.body.receiptId, 'Saved preview response must match the captured request receipt.');
}

export function relatedRouteChoice(choices, expectedRouteLabel) {
  const routeSuffix = `: ${expectedRouteLabel}`;
  const matching = choices.filter(choice => choice.label === expectedRouteLabel || choice.label.endsWith(routeSuffix));
  assert(matching.length <= 1, `Expected at most one ${expectedRouteLabel} route choice: ${JSON.stringify(choices)}`);
  if (matching.length === 0) return undefined;
  assert.equal(matching[0].disabled, false, `Exact ${expectedRouteLabel} route must be enabled.`);
  return matching[0];
}

export function proveSourceBinding(step, anchorColumnId, outputLabel, expectedForm = 'ALL', expectedPath = 'id') {
  assert(step, 'Candidate construction must contain a RELATED_SOURCE step after Pivot.');
  const related = step.operation.relatedSource;
  assert(related, 'RELATED_SOURCE operation payload is required.');
  assert.equal(related.anchorColumnId, anchorColumnId, 'Related source must bind the compiler stage row identity.');
  assert.equal(related.source.resourceType, 'Patient');
  assert.equal(related.source.path, expectedPath, 'Related source must use the exact selected Patient field path.');
  assert.equal(related.form, expectedForm);
  assert.equal(related.contributorRule?.policy, 'ALL_MATCHES', 'Related source must use the authoring wire contributor rule.');
  assert.equal(related.route.length, 1, JSON.stringify(related.route));
  const [hop] = related.route;
  assert.equal(hop.fromResourceType, 'Observation');
  assert.equal(hop.toResourceType, 'Patient');
  assert.equal(hop.relationship, 'subject_Patient');
  assert.equal(hop.storageDirection, 'OUTBOUND');
  const output = step.outputs.find(column => column.id === related.outputColumnId);
  assert(output, 'RELATED_SOURCE output identity must be present on its authored step.');
  assert.equal(output.label, outputLabel);
  if (expectedForm === 'COUNT') assert.equal(output.type, 'integer', 'Related-source COUNT output must retain its integer type.');
  return { related, output };
}

export function assertRelatedSourceStepAfterUpstreamChange(before, after, upstreamStep) {
  assert.equal(before?.operation?.kind, 'RELATED_SOURCE', 'Saved step must be the authored RELATED_SOURCE.');
  assert.equal(after?.operation?.kind, 'RELATED_SOURCE', 'Candidate must retain the authored RELATED_SOURCE.');
  assert(scalar(upstreamStep?.id) && Array.isArray(upstreamStep.outputs), 'Candidate upstream step and outputs are required.');
  assert.equal(after.id, before.id, 'Upstream edits must preserve the RELATED_SOURCE step identity.');
  assert.deepEqual(after.inputs, before.inputs, 'Upstream edits must preserve the RELATED_SOURCE dependency identity.');
  assert(after.inputs?.some(input => input.kind === 'STEP_OUTPUT' && input.stepId === upstreamStep?.id),
    'RELATED_SOURCE must remain attached to the edited upstream step.');
  assert.deepEqual(after.operation, before.operation, 'Upstream edits must preserve the full authored RELATED_SOURCE binding.');
  const outputID = before.operation.relatedSource?.outputColumnId;
  const authoredOutput = before.outputs?.find(output => output.id === outputID);
  assert(authoredOutput, 'Saved RELATED_SOURCE must own its authored output identity.');
  assert.deepEqual(after.outputs, [...(upstreamStep?.outputs ?? []), authoredOutput],
    'Candidate RELATED_SOURCE must refresh inherited outputs from the candidate upstream schema and retain its authored output.');
}

const withoutRelatedOutputLabel = step => {
  const relatedOutputId = step?.operation?.relatedSource?.outputColumnId;
  return step && ({
    ...step,
    outputs: Array.isArray(step.outputs)
      ? step.outputs.map(output => {
        if (output.id !== relatedOutputId) return output;
        const { label, ...identity } = output;
        return identity;
      })
      : step.outputs,
  });
};

export function relatedSourceEditEvidence(before, after, expectedLabel) {
  const outputID = before?.operation?.relatedSource?.outputColumnId;
  const beforeOutput = before?.outputs?.find(output => output.id === outputID);
  const afterOutput = after?.outputs?.find(output => output.id === outputID);
  const stableStepId = Boolean(before?.id) && before.id === after?.id;
  const unchangedOperation = isDeepStrictEqual(before?.operation, after?.operation);
  const unchangedStepExceptOutputLabels = isDeepStrictEqual(withoutRelatedOutputLabel(before), withoutRelatedOutputLabel(after));
  const stableOutputId = Boolean(outputID) && after?.operation?.relatedSource?.outputColumnId === outputID;
  const expectedLabelApplied = afterOutput?.label === expectedLabel && beforeOutput?.label !== expectedLabel;
  return {
    stableStepId,
    unchangedOperation,
    unchangedStepExceptOutputLabels,
    stableOutputId,
    expectedLabelApplied,
    ok: stableStepId && unchangedOperation && unchangedStepExceptOutputLabels && stableOutputId && expectedLabelApplied,
  };
}

export const sameWorkspace = (before, after) => isDeepStrictEqual(before?.workspace, after?.workspace);

const emptySourceConstruction = construction => construction == null
  || isDeepStrictEqual(construction, { version: 1, steps: [] });

const withCanonicalEmptySourceConstruction = (workspace, outputId) => {
  if (!scalar(outputId) || !Array.isArray(workspace?.documents)) return undefined;
  const normalized = structuredClone(workspace);
  const document = normalized.documents.find(item => item.output?.id === outputId);
  if (!document) return undefined;
  if (emptySourceConstruction(document.construction)) document.construction = { version: 1, steps: [] };
  return normalized;
};

export function sameWorkspaceIgnoringEmptySourceConstruction(before, after, outputId) {
  const beforeWorkspace = withCanonicalEmptySourceConstruction(before?.workspace, outputId);
  const afterWorkspace = withCanonicalEmptySourceConstruction(after?.workspace, outputId);
  return beforeWorkspace !== undefined && afterWorkspace !== undefined
    && isDeepStrictEqual(beforeWorkspace, afterWorkspace);
}

function pivotCategoryOutputIDMap(workspace) {
  if (!Array.isArray(workspace?.documents)) return undefined;
  const ids = new Map();
  for (const document of workspace.documents) {
    for (const step of document.construction?.steps ?? []) {
      if (step.operation?.kind !== 'PIVOT') continue;
      const categories = step.operation.pivot?.categories;
      if (!Array.isArray(categories)) return undefined;
      if (categories.some(category => !category.key || typeof category.key !== 'object'
        || Array.isArray(category.key) || !scalar(category.key.kind))) return undefined;
      const keys = categories.map(category => JSON.stringify(category.key));
      const outputIDs = categories.map(category => category.outputColumnId);
      if (keys.some(key => typeof key !== 'string')
        || new Set(keys).size !== keys.length
        || outputIDs.some(id => !scalar(id))
        || new Set(outputIDs).size !== outputIDs.length) return undefined;
      for (let index = 0; index < categories.length; index += 1) {
        const outputID = outputIDs[index];
        if (!Array.isArray(step.outputs)) return undefined;
        const matches = step.outputs.filter(output => output.id === outputID);
        if (matches.length !== 1 || ids.has(outputID)) return undefined;
        ids.set(outputID, `__pivot_category_output__:${document.output?.id ?? ''}:${step.id}:${keys[index]}`);
      }
    }
  }
  return ids;
}

function replacePivotCategoryOutputIDs(value, ids) {
  if (typeof value === 'string') return ids.get(value) ?? value;
  if (Array.isArray(value)) return value.map(item => replacePivotCategoryOutputIDs(item, ids));
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, replacePivotCategoryOutputIDs(item, ids)]));
  }
  return value;
}

export function sameWorkspaceIgnoringPivotCategoryOutputIDs(before, after) {
  const beforeIDs = pivotCategoryOutputIDMap(before?.workspace);
  const afterIDs = pivotCategoryOutputIDMap(after?.workspace);
  if (!beforeIDs || !afterIDs) return false;
  return isDeepStrictEqual(
    replacePivotCategoryOutputIDs(before.workspace, beforeIDs),
    replacePivotCategoryOutputIDs(after.workspace, afterIDs),
  );
}

export function sameConstructionIgnoringPivotCategoryOutputIDs(before, after) {
  const state = construction => ({
    workspace: { documents: [{ output: { id: 'construction-under-test' }, construction }] },
  });
  return sameWorkspaceIgnoringPivotCategoryOutputIDs(state(before), state(after));
}

export function withoutRelatedSourceFromWorkspace(workspace, { outputId, stepId, outputColumnId }) {
  assert(Array.isArray(workspace?.documents), 'The saved Builder workspace must contain documents.');
  const expected = structuredClone(workspace);
  const documents = expected.documents.filter(document => document.output?.id === outputId);
  assert.equal(documents.length, 1, 'The related-source removal baseline must identify exactly one saved output.');
  const [document] = documents;
  const stepMatches = (document.construction?.steps ?? []).filter(step => step.id === stepId);
  assert.equal(stepMatches.length, 1, 'The removal baseline must contain exactly the selected construction step.');
  const [step] = stepMatches;
  assert.equal(step.operation?.kind, 'RELATED_SOURCE', 'The selected removal step must be RELATED_SOURCE.');
  assert.equal(step.operation.relatedSource?.outputColumnId, outputColumnId,
    'The selected RELATED_SOURCE must own the output column being removed.');
  assert.equal(step.outputs?.filter(output => output.id === outputColumnId).length, 1,
    'The selected RELATED_SOURCE step must expose its exact output once.');
  document.construction.steps = document.construction.steps.filter(candidate => candidate.id !== stepId);
  document.columns = (document.columns ?? []).filter(column => column.columnId !== outputColumnId);
  assert(!document.construction.steps.some(candidate => candidate.id === stepId));
  assert(!document.columns.some(column => column.columnId === outputColumnId));
  return expected;
}

export const readPivotSelectOptions = select => Array.from(select.options, option => ({
  value: option.value,
  label: option.textContent.trim(),
  disabled: Boolean(option.disabled),
}));

export function uniqueEnabledSelectValue(options, value, selector) {
  const matches = options.filter(option => !option.disabled && option.value === value);
  assert.equal(matches.length, 1, `${selector} must expose one enabled exact value ${JSON.stringify(value)} option: ${JSON.stringify(options)}`);
  return matches[0];
}

export function readProposalPreviewDocument() {
  const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
  const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
  const table = preview?.querySelector('table');
  const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  return {
    proposalStatus: panel?.getAttribute('data-proposal-status') ?? '',
    proposalId: panel?.getAttribute('data-proposal-id') ?? '',
    previewStatus: preview?.getAttribute('data-preview-status') ?? '',
    receiptId: preview?.getAttribute('data-preview-receipt-id') ?? '',
    outputId: preview?.getAttribute('data-preview-output-id') ?? '',
    headers: [...(table?.querySelectorAll('thead th') ?? [])]
      .map(cell => clean(cell.querySelector('span')?.textContent ?? cell.textContent)),
    rows: [...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => clean(cell.textContent))),
  };
}

export async function runRelatedSourceAfterPivotBrowserWorkflow({ page, cda }, originalArgs = {}) {
  const relatedForm = originalArgs.form ?? 'ALL';
  const sourcePath = originalArgs.sourcePath ?? 'id';
  const splitGroupKey = originalArgs.splitGroupKey === true;
  assert(['ALL', 'COUNT'].includes(relatedForm), `Unsupported post-Pivot RELATED_SOURCE form ${relatedForm}`);
  assert(['id', 'resourceType'].includes(sourcePath), `Unsupported post-Pivot Patient field ${sourcePath}`);
  assert(sourcePath === 'id' || relatedForm === 'ALL', 'Patient.resourceType coverage uses the ALL form only.');
  assert(!splitGroupKey || (sourcePath === 'resourceType' && relatedForm === 'ALL'),
    'Upstream Pivot group-key splitting is scoped to the Patient.resourceType ALL lifecycle.');
  const sourceFieldLabel = `Patient.${sourcePath}`;
  const chooserFormLabel = relatedForm === 'COUNT' ? 'Count matching records' : 'Keep all matching values';
  const outputLabel = relatedForm === 'COUNT'
    ? 'Patient count from Pivot contributors'
    : sourcePath === 'id' ? 'Patient IDs from Pivot contributors' : 'Patient resource types from Pivot contributors';
  const outputValueLabel = relatedForm === 'COUNT' ? 'count' : sourcePath === 'id' ? 'values' : 'resourceType values';
  const formValueLabel = relatedForm === 'COUNT' ? 'count' : sourcePath === 'id' ? 'ALL values' : 'ALL resourceType values';
  const project = cda.project;
  const explorer = cda.explorer;
  const apiOrigin = cda.apiOrigin;
  const uiOrigin = cda.uiOrigin;
  const arangoContainer = cda.target?.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER;
  assert.equal(project, 'loom_dev_cda_fhir', 'This native transition is scoped to the CDA FHIR project.');
  assert.equal(cda.generation ?? cda.env?.LOOM_CDA_GENERATION, generation);
  assert(scalar(explorer) && scalar(apiOrigin) && scalar(uiOrigin) && scalar(arangoContainer));

  const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
  const report = cda.report;
  report.nativeRequests ??= cda.nativeRequests;
  report.errors ??= [];
  report.cases ??= [];
  report.phase = splitGroupKey
    ? 'post-Pivot upstream group-key split with Patient.resourceType ALL lifecycle'
    : sourcePath === 'id'
    ? `post-Pivot RELATED_SOURCE ${relatedForm} lifecycle`
    : `post-Pivot RELATED_SOURCE ${sourceFieldLabel} ${relatedForm} lifecycle`;
  report.scope = { project, generation, rawScanLimit };

  let builder;
  let outputId;
  let currentPreview;
  const requestCaptureStartIndex = report.nativeRequests.length;
  const requestCapture = cda.captureRequests(base);
  const doc = state => state.workspace.documents.find(document => document.output.id === outputId);
  const steps = state => doc(state)?.construction?.steps ?? [];
  const api = async (path, body) => {
    const response = await fetch(apiOrigin + path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-source-after-pivot-${randomUUID()}` },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    assert(response.ok, `${path}: ${JSON.stringify(value)}`);
    return value;
  };
  const command = async commands => {
    const response = await api(base + '/commands', {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await api(base + '/builder');
    return response;
  };
  const rawQuery = (query, bindVars = {}) => {
    const code = buildRawQueryExecuteString(query, bindVars);
    const result = spawnSync('docker', [
      'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', code,
    ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
    assert.equal(result.status, 0, `Raw CDA query failed: ${result.stderr || result.error?.message || result.stdout}`);
    const start = result.stdout.indexOf('[');
    assert(start >= 0, `Raw CDA query returned no JSON array: ${result.stdout.slice(-1000)}`);
    const rows = JSON.parse(result.stdout.slice(start));
    assert(Array.isArray(rows));
    return rows;
  };
  const recordCheck = (name, condition, evidence = {}) => cda.check('correctness', name, Boolean(condition), evidence);
  const recordBudget = (name, startedAt) => {
    const elapsedMs = Date.now() - startedAt;
    report.cases.push({ name, elapsedMs });
    assert(elapsedMs <= 5000, `${name} exceeded five seconds: ${elapsedMs}ms`);
    return elapsedMs;
  };
  const wait = (predicate, args = {}, timeout = 5000) => cda.wait(predicate, args, Math.min(timeout, 5000));
  const click = (selector, identity) => cda.click(selector, identity, 5000);
  const select = (selector, value) => cda.selectOption(selector, value, { timeout: 5000 });
  const fill = (selector, value) => cda.fill(selector, value, { timeout: 5000 });
  const inspect = (callback, args = {}) => cda.inspect(callback, args);
  const tableURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;

  const previewValues = preview => {
    assert(preview?.columns?.length, 'Preview must declare its output columns.');
    assert(Array.isArray(preview.rows), 'Preview must return raw protocol rows.');
    const labels = preview.columns.map(column => column.label);
    assert.equal(new Set(labels).size, labels.length, 'Preview labels must be unique.');
    return preview.rows.map(row => Object.fromEntries(preview.columns.map(column => [column.label, row[column.column]])));
  };
  const publicPreviewSchema = preview => preview.columns.map(({ label, logicalType, nullable, shape }) => ({
    label, logicalType, nullable, shape,
  })).sort((left, right) => left.label.localeCompare(right.label));
  const assertProtocolRows = (preview, expectedRows, label) => {
    assert.equal(preview.rowCount, expectedRows.length, `${label}: raw protocol row count`);
    assert.equal(preview.rows.length, expectedRows.length, `${label}: bounded protocol window must contain every row`);
    const actualRows = previewValues(preview);
    assert.deepEqual(canonicalRows(actualRows), canonicalRows(expectedRows), `${label}: native protocol values differ from the raw oracle`);
  };
  const displayCell = value => {
    if (value === undefined || value === null) return '—';
    if (Array.isArray(value)) return value.map(displayCell).filter(item => item !== '—').join('; ') || '—';
    if (typeof value === 'string') return value.trim() || '—';
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
    throw new TypeError(`Unsupported preview cell type: ${Object.prototype.toString.call(value)}`);
  };
  const readTable = async (expectedRows, label) => {
    await wait(({ rowCount }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table?.getAttribute('aria-rowcount') === String(Math.min(25, rowCount) + 1)
        && !document.body.innerText.includes('Loading your table…')
        && !document.body.innerText.includes('Preview failed:');
    }, { rowCount: expectedRows.length });
    const rendered = await inspect(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return {
        headers: [...(table?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.textContent.trim()),
        rows: [...(table?.querySelectorAll('[role="row"]') ?? [])]
          .filter(row => row.querySelector('[role="cell"]'))
          .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.textContent.trim())),
      };
    });
    const expectedLabels = Object.keys(expectedRows[0] ?? {});
    assert.deepEqual([...rendered.headers].sort(), [...expectedLabels].sort(), `${label}: saved table schema`);
    const expectedDisplayRows = expectedRows.map(row => rendered.headers.map(header => displayCell(row[header])));
    assertVisibleRowsMatchOracle(rendered.rows, expectedDisplayRows, { label });
  };
  const openTable = async (expectedRows, name) => {
    const startedAt = Date.now();
    const navigation = await navigateAfterOwnedConstructionCapabilities(
      cda,
      requestCapture,
      () => report.nativeRequests.slice(requestCaptureStartIndex),
      `${base}/construction-capabilities`,
      Math.max(1, startedAt + 5000 - Date.now()),
      () => cda.navigate(tableURL),
    );
    if (navigation.settledEntries.length) {
      (report.preNavigationCapabilitySettlements ??= []).push({
        observedAt: new Date().toISOString(),
        requests: navigation.settledEntries.map(entry => ({
          requestId: entry.requestId,
          browserRequestId: entry.browserRequestId,
          status: entry.status,
          startedAt: entry.startedAt,
          completedAt: entry.completedAt,
        })),
      });
    }
    await wait(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` });
    await click(`[data-testid="construction-table-${outputId}"]`);
    await wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false);
    const previewEntry = await waitForCdaCapturedResponse(cda, requestCapture, entry =>
      matchesSavedPreviewRequest(entry, { path: `${base}/preview`, outputId, startedAt }),
    Math.max(1, startedAt + 5000 - Date.now()));
    assert.equal(previewEntry.status, 200, `${name}: saved preview request status`);
    const savedPreview = requestCapture.rawResponseBody(previewEntry) ?? previewEntry.response;
    assertSavedPreviewIdentity(previewEntry, savedPreview, outputId);
    assertProtocolRows(savedPreview, expectedRows, `${name} direct saved preview`);
    await readTable(expectedRows, name);
    recordBudget(name, startedAt);
    return savedPreview;
  };
  const proposalResponse = async (name, startedAt) => {
    await wait(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
    }, {}, Math.max(1, startedAt + 5000 - Date.now()));
    const panel = await inspect(() => {
      const element = document.querySelector('[data-testid="construction-proposal-panel"]');
      return { status: element?.dataset.proposalStatus, proposalId: element?.dataset.proposalId, text: element?.innerText };
    });
    assert.equal(panel.status, 'ready', `${name}: ${panel.text}`);
    const remaining = Math.max(1, startedAt + 5000 - Date.now());
    const entry = await waitForCdaCapturedResponse(cda, requestCapture, candidate =>
      candidate.path === `${base}/construction-proposals`
      && candidate.method === 'POST'
      && candidate.body?.outputId === outputId
      && candidate.startedAt >= startedAt
      && candidate.response?.proposalId === panel.proposalId, remaining);
    assert.equal(entry.status, 200, `${name}: proposal request status`);
    const response = requestCapture.rawResponseBody(entry) ?? entry.response;
    assert.equal(response.outputId, outputId, `${name}: proposal response output identity`);
    assert.equal(response.proposalId, panel.proposalId, `${name}: proposal and preview receipt ownership`);
    assert.equal(response.preview?.outputId, outputId, `${name}: proposal preview output identity`);
    assert.equal(response.preview?.receiptId, response.proposalId, `${name}: preview receipt must belong to proposal`);
    return { entry, response, preview: response.preview, startedAt };
  };
  const apply = async (expectedRows, name) => {
    const startedAt = Date.now();
    await click('[data-testid="construction-apply-proposal"]');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    builder = await api(base + '/builder');
    recordBudget(name, startedAt);
    return openTable(expectedRows, `${name} reload`);
  };
  const cancel = async (baseline, expectedRows, name) => {
    const startedAt = Date.now();
    await click('[data-testid="construction-cancel-proposal"]');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    builder = await api(base + '/builder');
    assert.deepEqual(builder.workspace, baseline.workspace, `${name}: Cancel must preserve the complete saved workspace`);
    assert.equal(builder.draftVersion, baseline.draftVersion, `${name}: Cancel draft version`);
    assert.equal(builder.draftDigest, baseline.draftDigest, `${name}: Cancel draft digest`);
    await readTable(expectedRows, `${name} current render`);
    recordBudget(name, startedAt);
    await openTable(expectedRows, `${name} reload`);
  };

  const observationScanQuery = `FOR o IN Observation
    FILTER o.project == @project AND o.dataset_generation == @generation
      AND o.payload.resourceType == "Observation"
      AND IS_STRING(o.payload.status) AND LENGTH(TRIM(o.payload.status)) > 0
      AND IS_STRING(o.payload.subject.reference) AND STARTS_WITH(o.payload.subject.reference, "Patient/")
    SORT o._id
    LIMIT ${rawScanLimit}
    RETURN { _id: o._id, id: o.id, project: o.project, generation: o.dataset_generation,
      resourceType: o.payload.resourceType, status: o.payload.status,
      patientReference: o.payload.subject.reference }`;

  const assertPivotProposal = (response, pair, pivotValuesLabel) => {
    const candidate = response.candidateConstruction;
    assert(candidate?.steps, 'Pivot proposal must include its candidate construction.');
    const pivotStep = candidate.steps.find(step => step.operation.kind === 'PIVOT');
    assert(pivotStep, 'Pivot proposal must contain a native PIVOT step.');
    const pivot = pivotStep.operation.pivot;
    assert.deepEqual(pivot.groupKeyIds, [sourceColumns.status.columnId], 'Pivot must group by the exact raw status binding.');
    assert.equal(pivot.categoryColumnId, sourceColumns.patientReference.columnId, 'Pivot categories must bind to subject.reference.');
    assert.equal(pivot.valueColumnId, sourceColumns.id.columnId, 'Pivot values must bind to Observation.id.');
    assert.deepEqual(pivot.categories.map(category => category.key.string).sort(), pair.members.map(member => member.patientReference).sort(),
      'The complete typed Pivot category domain must equal the two raw Patient references.');
    assert(pivot.categories.every(category => category.key.kind === 'STRING'),
      'Every Pivot category must retain its typed string key.');
    assert.equal(new Set(pivot.categories.map(category => JSON.stringify(category.key))).size, pivot.categories.length,
      'Pivot category keys must be one-to-one.');
    assert.equal(response.preview.rowCount, 1, 'Two same-status source roots must coalesce into one Pivot row.');
    const groupOutput = pivotStep.outputs.find(output => output.id === sourceColumns.status.columnId);
    assert(groupOutput, 'Pivot must preserve the raw status group-key identity.');
    assert.equal(groupOutput.label, sourceColumns.status.label);
    assert.equal(groupOutput.type, 'string');
    const expectedRow = { [groupOutput.label]: pair.status };
    for (const category of pivot.categories) {
      const output = pivotStep.outputs.find(column => column.id === category.outputColumnId);
      assert(output, 'Each Pivot category must persist its generated output identity.');
      assert.equal(output.label, category.key.string, 'Pivot category must retain its exact public label.');
      assert.equal(output.type, 'string', 'Pivot category must retain its exact public type.');
      const member = pair.members.find(source => source.patientReference === category.key.string);
      assert(member, `Pivot emitted an unowned category ${category.key.string}.`);
      expectedRow[output.label] = member.id;
    }
    assert.equal(Object.keys(expectedRow).length, 3, 'Pivot row must contain one status plus both category outputs.');
    assert.equal(pivotValuesLabel, sourceColumns.id.label);
    assertProtocolRows(response.preview, [expectedRow], 'Pivot proposal');
    return { pivotStep, expectedRows: [expectedRow] };
  };

  const selectPivotField = async (selector, columnId) => {
    const options = await page.locator(selector).evaluate(readPivotSelectOptions);
    const match = uniqueEnabledSelectValue(options, columnId, selector);
    await select(selector, match.value);
  };

  let sourceColumns;
  const doPivot = async () => {
    await click('[data-testid="construction-rows-settings-trigger"]');
    await wait(() => document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled === false);
    await wait(() => document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled === false);
    await click('[data-testid="construction-action-table-pivot-rows"]');
    await wait(() => document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)'));
    const groupSelector = `input[aria-label=${JSON.stringify(`Pivot group ${sourceColumns.status.label}`)}]`;
    await click(groupSelector);
    await selectPivotField('select[aria-label="Pivot category field"]', sourceColumns.patientReference.columnId);
    const startedAt = Date.now();
    await selectPivotField('select[aria-label="Pivot values field"]', sourceColumns.id.columnId);
    return startedAt;
  };

  const editPivotRoles = async ({ fromGroup, toGroup, toValue }) => {
    const startedAt = Date.now();
    const pivotStep = steps(builder).find(step => step.operation.kind === 'PIVOT');
    assert(pivotStep, 'A saved Pivot step must exist before changing its native roles.');
    await click(`[data-testid="construction-history-step-${pivotStep.id}"]`);
    await click(`[data-testid="construction-edit-step-${pivotStep.id}"]`);
    await wait(({ selector }) => Boolean(document.querySelector(selector)), {
      selector: '[data-testid="construction-reshape-pivot"] select[aria-label="Pivot values field"]',
    });
    const fromGroupSelector = `input[aria-label=${JSON.stringify(`Pivot group ${fromGroup.label}`)}]`;
    await wait(({ selector }) => Boolean(document.querySelector(selector + ':not(:disabled)')), { selector: fromGroupSelector });
    assert.equal(await page.locator(fromGroupSelector).isChecked(), true, `${fromGroup.label} must be the current Pivot group key.`);
    await click(fromGroupSelector);
    await selectPivotField('select[aria-label="Pivot values field"]', toValue.columnId);
    const toGroupSelector = `input[aria-label=${JSON.stringify(`Pivot group ${toGroup.label}`)}]`;
    await wait(({ selector }) => Boolean(document.querySelector(selector + ':not(:disabled)')), { selector: toGroupSelector });
    assert.equal(await page.locator(toGroupSelector).isChecked(), false, `${toGroup.label} must be available as the replacement group key.`);
    await click(toGroupSelector);
    return startedAt;
  };

  const startRelatedField = async () => {
    await click('[data-testid="construction-action-add-columns"]');
    await click('[aria-label="Column types"] button', { includes: 'Fields and related data' });
    await wait(({ selector }) => Boolean(document.querySelector(selector)), { selector: '[data-testid="construction-add-columns-source"]' });
    const relatedDetails = page.locator('[aria-label="Related resources"] summary');
    if (!await relatedDetails.evaluate(summary => summary.parentElement.open)) await click('[aria-label="Related resources"] summary');
    const sources = await page.locator('[data-testid="construction-add-columns-source-option"]').evaluateAll(options =>
      options.map(option => ({ label: option.getAttribute('aria-label') ?? '', disabled: option.disabled }))
        .filter(option => option.label.includes('Patient') && option.label.includes('Related resource')));
    assert.equal(sources.length, 1, `Post-Pivot source chooser must expose one Patient route: ${JSON.stringify(sources)}`);
    assert.equal(sources[0].disabled, false, `Patient route must be enabled after Pivot: ${sources[0].label}`);
    await click(`[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(sources[0].label)}]`);
    const rawFields = page.getByTestId('feature-catalog-raw-fields').locator(':scope > summary');
    if (!await rawFields.evaluate(summary => summary.parentElement.open)) await click('[data-testid="feature-catalog-raw-fields"] > summary');
    const candidate = `input[aria-label="Select ${sourceFieldLabel}"]`;
    await wait(({ selector }) => Boolean(document.querySelector(selector + ':not(:disabled)')), { selector: candidate });
    await click(candidate);
    await click('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    await wait(() => Boolean(document.querySelector('[role="dialog"]')));
    const choices = await page.getByRole('dialog').locator('input[type="radio"]').evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label') ?? '', disabled: input.disabled })));
    const route = relatedRouteChoice(choices, 'Observation -[subject]-> Patient');
    const startedAt = Date.now();
    if (route) await click(`[role="dialog"] input[aria-label=${JSON.stringify(route.label)}]`);
    await wait(({ label }) => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')]
      .some(input => (input.getAttribute('aria-label') ?? '').includes(label)), { label: chooserFormLabel });
    const formChoices = await page.getByRole('dialog').locator('input[type="radio"]').evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label') ?? ''));
    const selectedForm = formChoices.find(label => label.includes(chooserFormLabel));
    assert(selectedForm, `Post-Pivot related source must provide ${relatedForm}: ${JSON.stringify(formChoices)}`);
    await click(`[role="dialog"] input[aria-label=${JSON.stringify(selectedForm)}]`);
    const groupedPolicy = page.getByRole('dialog').locator('select[aria-label="Values per grouped row"]');
    if (await groupedPolicy.count()) await select('[role="dialog"] select[aria-label="Values per grouped row"]', 'ALL');
    await click('[role="dialog"] button', { name: 'Add 1 column' });
    return startedAt;
  };

  const nativeProposal = async (name, startedAt, expectedRows, candidateCheck) => {
    const proposal = await proposalResponse(name, startedAt);
    const result = candidateCheck(proposal.response) ?? {};
    if (expectedRows.length === 0) assert(Array.isArray(result.expectedRows), `${name}: candidate oracle must return its expected output rows.`);
    const expected = result.expectedRows ?? expectedRows;
    assertProtocolRows(proposal.preview, expected, name);
    const remaining = Math.max(1, proposal.startedAt + 5000 - Date.now());
    await page.waitForFunction(
      proposalPreviewReadinessExpression(outputId, Math.min(25, expected.length)),
      undefined,
      { timeout: remaining },
    );
    const rendered = await inspect(readProposalPreviewDocument);
    assert.equal(rendered.proposalStatus, 'ready', `${name}: proposal panel readiness`);
    assert.equal(rendered.proposalId, proposal.response.proposalId, `${name}: visible proposal receipt`);
    assert.equal(rendered.previewStatus, 'ready', `${name}: visible preview readiness`);
    assert.equal(rendered.receiptId, proposal.response.proposalId, `${name}: rendered table belongs to the proposal receipt`);
    assert.equal(rendered.outputId, outputId, `${name}: rendered table belongs to the selected output`);
    const expectedLabels = Object.keys(expected[0] ?? {});
    assert.deepEqual([...rendered.headers].sort(), [...expectedLabels].sort(), `${name}: proposal preview headers`);
    const expectedVisibleRows = expected.map(row => rendered.headers.map(header => displayCell(row[header])));
    assertVisibleRowsMatchOracle(rendered.rows, expectedVisibleRows, {
      label: `${name} rendered proposal preview`,
      exactWindow: !splitGroupKey,
    });
    recordBudget(name, proposal.startedAt);
    recordCheck(name, true, {
      proposalId: proposal.response.proposalId,
      rowCount: proposal.preview.rowCount,
      renderedHeaders: rendered.headers,
      renderedRows: rendered.rows,
      renderedOutputId: rendered.outputId,
      renderedReceiptId: rendered.receiptId,
      ...result,
    });
    return { ...proposal, ...result, expectedRows: expected };
  };

  try {
    const scan = rawQuery(observationScanQuery, { project, generation });
    const pair = choosePostPivotRelatedSourcePair(scan, { project, generation, limit: rawScanLimit });
    if (!pair) {
      const error = new Error(`The bounded first-${rawScanLimit} Observation scan has no two-root same-status/different-Patient witness; no post-Pivot lifecycle result is claimed.`);
      error.name = 'RawOracleUnavailableError';
      error.rawOracleFailure = true;
      report.status = 'unverified';
      report.unverifiedReason = error.message;
      throw error;
    }
    const selectedIDs = pair.members.map(member => member._id);
    const edgeQuery = `FOR source IN Observation
      FILTER source._id IN @sourceIDs AND source.project == @project
        AND source.dataset_generation == @generation AND source.payload.resourceType == "Observation"
      FOR edge IN fhir_edge
        FILTER edge._from == source._id AND edge.label == "subject_Patient"
          AND edge.project == @project AND edge.dataset_generation == @generation
          AND STARTS_WITH(edge._to, "Patient/")
        LET patient = DOCUMENT(edge._to)
        FILTER patient != null AND patient.project == @project
          AND patient.dataset_generation == @generation AND patient.payload.resourceType == "Patient"
      SORT source._id, patient._id
      RETURN {
        source: { _id: source._id, id: source.id, project: source.project,
          generation: source.dataset_generation, resourceType: source.payload.resourceType,
          status: source.payload.status, patientReference: source.payload.subject.reference },
        patient: { _id: patient._id, id: patient.id, project: patient.project,
          generation: patient.dataset_generation, resourceType: patient.payload.resourceType }
      }`;
    const linked = rawQuery(edgeQuery, { sourceIDs: selectedIDs, project, generation });
    const oracle = verifyPostPivotRelatedSourceWitness(pair, linked, { project, generation });
    if (splitGroupKey) {
      const expectedWitness = originalArgs.expectedRetainedWitness;
      assert(expectedWitness && typeof expectedWitness === 'object',
        'The upstream split case must bind to its retained two-Observation witness.');
      const retainedWitness = Object.fromEntries(oracle.members.map(({ observation }) => [
        observation.id,
        { observationID: observation._id, patientReference: observation.patientReference },
      ]));
      assert.deepEqual(retainedWitness, expectedWitness,
        'The bounded raw scan must select the exact retained Observation-to-Patient witness for this case.');
    }
    const expectedRelatedValue = relatedForm === 'COUNT'
      ? new Set(oracle.members.map(({ patient }) => patient._id)).size
      : sourcePath === 'id' ? oracle.patientIDsInCompilerOrder : oracle.patientResourceTypesInCompilerOrder;
    if (relatedForm === 'COUNT') {
      assert.equal(expectedRelatedValue, 2, 'The raw oracle must prove two distinct Patient documents for COUNT.');
    }
    report.oracle = {
      query: { boundedScan: observationScanQuery, exactEdgeRead: edgeQuery },
      scope: { project, generation, maxObservationRows: rawScanLimit },
      status: oracle.status,
      members: oracle.members.map(({ observation, patient }) => ({ observation, patient })),
      expectedPatientIDs: oracle.patientIDsInCompilerOrder,
      expectedRelatedValue,
      expectedPivotRows: 'One final-status row with two patient-reference categories whose cells equal the corresponding Observation IDs.',
      ...(splitGroupKey ? { expectedSplitRowsByObservationID: oracle.expectedSplitRowsByObservationID } : {}),
    };
    recordCheck('CDA raw oracle selects two same-status Observation roots with distinct Patient references', true, {
      scannedRows: scan.length, selectedObservationIDs: oracle.members.map(({ observation }) => observation.id), status: oracle.status,
    });
    recordCheck('Exact scoped subject_Patient edges resolve the two roots to distinct Patient documents', true, {
      observationIDs: oracle.members.map(({ observation }) => observation.id), patientIDs: oracle.patientIDsInCompilerOrder,
    });

    const created = await api(root, { name: explorer, title: 'CDA post-Pivot related-source QA' });
    assert.equal(created.explorerId, explorer, 'The create response must preserve this workflow’s owned Explorer identity.');
    builder = await api(base + '/builder');
    assert.equal(builder.workspace?.documents?.length ?? 0, 0, 'The owned Explorer must begin with an empty workspace.');
    assert.equal(builder.catalog.generation, generation);
    const observationNode = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
    assert(observationNode, 'The scoped catalog must expose Observation as a direct root.');
    await command([{ type: 'CREATE_TABLE', title: 'Post-Pivot related source', rootNodeId: observationNode.nodeId }]);
    outputId = builder.workspace.documents[0].output.id;
    const candidatesByPath = new Map(['id', 'status', 'subject.reference'].map(path => {
      const candidate = builder.catalog.candidates.find(item => item.nodeId === observationNode.nodeId && item.fieldPath === path);
      assert(candidate, `The scoped Observation catalog must expose ${path}.`);
      assert.equal(candidate.logicalType, 'string', `${path} must have a scalar string catalog type.`);
      assert(['optional_one', 'required_one'].includes(candidate.cardinality), `${path} must have a one-valued source binding.`);
      assert.deepEqual(candidate.repeatedBoundaries ?? [], [], `${path} must not cross a repeated source boundary.`);
      return [path, candidate];
    }));
    await command([...candidatesByPath].map(([path, candidate]) => ({
      type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: candidate.label,
    })));
    const sourceColumnsByPath = Object.fromEntries([...candidatesByPath].map(([path, candidate]) => {
      const column = doc(builder).columns.find(item => item.source.kind === 'field' && item.source.field.path === path);
      assert(column, `The exact Observation.${path} binding must persist before Pivot.`);
      return [path, { columnId: column.columnId, label: column.label, candidate }];
    }));
    sourceColumns = {
      id: sourceColumnsByPath.id,
      status: sourceColumnsByPath.status,
      patientReference: sourceColumnsByPath['subject.reference'],
    };
    const selection = await api(cdaExplorerSelectionsPath(project, explorer), {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: explorer,
      source: { kind: 'resources', resources: { refs: oracle.members.map(({ observation }) => ({
        project, generation, resourceType: 'Observation', id: observation.id,
      })) } },
    });
    assert.equal(selection.memberCount, 2, 'The immutable source selection must contain the two exact raw roots.');
    const routes = await api(base + '/population-routes', {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
    });
    const direct = routes.choices.find(choice => choice.route.length === 0);
    assert(direct, 'The exact Observation selection must provide a direct rooted population route.');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
    recordCheck('Fresh Explorer binds the exact two-root Observation population to its direct source table', true, {
      selectionRevisionId: selection.id, memberCount: selection.memberCount,
      selectedIDs: oracle.members.map(({ observation }) => observation.id),
      project, generation, rowPopulationRoute: direct.route,
    });

    const sourceRows = oracle.members.map(({ observation }) => ({
      [sourceColumns.id.label]: observation.id,
      [sourceColumns.status.label]: observation.status,
      [sourceColumns.patientReference.label]: observation.patientReference,
    }));
    await openTable(sourceRows, 'Initial exact Observation source rows survive reload');

    let pivotStart = await doPivot();
    const pivotCancelProposal = await nativeProposal('Native Pivot preview matches the two exact source categories and coalesces one row', pivotStart, [], response => {
      const verified = assertPivotProposal(response, pair, sourceColumns.id.label);
      currentPreview = verified.expectedRows;
      return { expectedRows: verified.expectedRows, categories: pair.members.map(member => member.patientReference), rowCount: 1, pivotStepId: verified.pivotStep.id };
    });
    assert.equal(pivotCancelProposal.preview.rowCount, 1);
    const beforePivot = structuredClone(builder);
    await cancel(beforePivot, sourceRows, 'Canceling Pivot preserves exact root workspace and rows');
    recordCheck('Pivot Cancel preserves exact source workspace and rows after reload',
      builder.draftDigest === beforePivot.draftDigest && canonicalRows(sourceRows).length === 2,
      { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });

    pivotStart = await doPivot();
    const pivotApplyProposal = await nativeProposal('Confirmed Pivot proposal retains exact typed category and value bindings', pivotStart, [], response => {
      const verified = assertPivotProposal(response, pair, sourceColumns.id.label);
      currentPreview = verified.expectedRows;
      return { expectedRows: verified.expectedRows, pivotStepId: verified.pivotStep.id, categories: pair.members.map(member => member.patientReference) };
    });
    const coalescedPivotPreviewSchema = publicPreviewSchema(pivotApplyProposal.preview);
    let pivotRows = currentPreview;
    const coalescedPivotRows = pivotRows;
    await apply(pivotRows, 'Apply Pivot and render exact coalesced row');
    let savedPivotStep = steps(builder).find(step => step.operation.kind === 'PIVOT');
    assert(savedPivotStep, 'Applied workspace must retain the native Pivot step.');
    assert.deepEqual(savedPivotStep.operation.pivot.groupKeyIds, [sourceColumns.status.columnId]);
    assert.equal(savedPivotStep.operation.pivot.categoryColumnId, sourceColumns.patientReference.columnId);
    assert.equal(savedPivotStep.operation.pivot.valueColumnId, sourceColumns.id.columnId);
    assert.deepEqual(savedPivotStep.operation.pivot.categories.map(category => category.key.string).sort(), pair.members.map(member => member.patientReference).sort());
    recordCheck('Applied Pivot persists typed categories and one independently verified coalesced row after reload', true, {
      stepId: savedPivotStep.id, categoryKeys: savedPivotStep.operation.pivot.categories.map(category => category.key),
      rowCount: 1, selectedObservationIDs: oracle.members.map(({ observation }) => observation.id),
    });
    const pivotBaseline = structuredClone(builder);

    let capabilities = await api(base + '/construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      outputId,
      stageId: savedPivotStep.id,
    });
    const relatedCapability = capabilities.selectedStage.capabilities.find(capability => capability.kind === 'RELATED_SOURCE');
    assert.equal(capabilities.selectedStage.id, savedPivotStep.id);
    assert(scalar(capabilities.selectedStage.rowIdentityColumn), 'Pivot stage must expose a hidden row identity for contributor binding.');
    assert.equal(relatedCapability?.supported, true, `Pivot stage lacks RELATED_SOURCE support: ${JSON.stringify(relatedCapability)}`);
    recordCheck('Pivot compiler stage exposes the exact row identity and RELATED_SOURCE capability', true, {
      stageId: capabilities.selectedStage.id, rowIdentityColumn: capabilities.selectedStage.rowIdentityColumn,
      capability: relatedCapability,
    });

    let relatedStart = await startRelatedField();
    const relatedCancelProposal = await nativeProposal(`Post-Pivot ${sourceFieldLabel} ${relatedForm} proposal retains both source contributors`, relatedStart, [], response => {
      const candidate = response.candidateConstruction;
      const sourceStep = candidate.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
      const { related, output } = proveSourceBinding(sourceStep, capabilities.selectedStage.rowIdentityColumn, outputLabelOf(sourceStep), relatedForm, sourcePath);
      const expected = [{ ...pivotRows[0], [output.label]: expectedRelatedValue }];
      assertProtocolRows(response.preview, expected, `Post-Pivot ${sourceFieldLabel} ${relatedForm} proposal`);
      return { expectedRows: expected, step: sourceStep, related, output };
    });
    let relatedRows = relatedCancelProposal.expectedRows;
    recordCheck(`Native chooser and candidate bind ${sourceFieldLabel} ${relatedForm} through exact Observation.subject → Patient`, true, {
      source: relatedCancelProposal.related.source, route: relatedCancelProposal.related.route,
      form: relatedCancelProposal.related.form, anchorColumnId: relatedCancelProposal.related.anchorColumnId,
      rowIdentityColumn: capabilities.selectedStage.rowIdentityColumn,
    });
    recordCheck(relatedForm === 'COUNT'
      ? 'Post-Pivot COUNT returns the exact distinct Patient count on one Pivot row'
      : sourcePath === 'id'
        ? 'Post-Pivot ALL returns both independently resolved Patient IDs on one Pivot row'
        : 'Post-Pivot Patient.resourceType ALL returns duplicate-preserving resourceType values on one Pivot row', true, {
      pivotRowCount: relatedCancelProposal.preview.rowCount,
      expectedPatientIDs: oracle.patientIDsInCompilerOrder,
      expectedRelatedValue,
      actual: previewValues(relatedCancelProposal.preview),
    });
    await cancel(pivotBaseline, pivotRows, 'Canceling related-source proposal preserves Pivot after reload');
    recordCheck('Related-source Cancel preserves the exact saved Pivot and its raw values after reload',
      builder.draftDigest === pivotBaseline.draftDigest
        && !steps(builder).some(step => step.operation.kind === 'RELATED_SOURCE'),
      { draftVersion: builder.draftVersion, pivotStepId: savedPivotStep.id });

    relatedStart = await startRelatedField();
    const relatedApplyProposal = await nativeProposal(`Confirmed post-Pivot ${sourceFieldLabel} ${relatedForm} preview`, relatedStart, [], response => {
      const candidate = response.candidateConstruction;
      const sourceStep = candidate.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
      const { related, output } = proveSourceBinding(sourceStep, capabilities.selectedStage.rowIdentityColumn, outputLabelOf(sourceStep), relatedForm, sourcePath);
      const expected = [{ ...pivotRows[0], [output.label]: expectedRelatedValue }];
      assertProtocolRows(response.preview, expected, `Confirmed post-Pivot ${sourceFieldLabel} ${relatedForm} proposal`);
      return { expectedRows: expected, step: sourceStep, related, output };
    });
    await apply(relatedApplyProposal.expectedRows, `Apply post-Pivot ${sourceFieldLabel} ${relatedForm} and reload exact values`);
    let savedRelated = steps(builder).find(step => step.operation.kind === 'RELATED_SOURCE');
    assert(savedRelated, 'Applied post-Pivot RELATED_SOURCE must persist.');
    proveSourceBinding(savedRelated, capabilities.selectedStage.rowIdentityColumn, relatedApplyProposal.output.label, relatedForm, sourcePath);
    recordCheck(`Applied post-Pivot RELATED_SOURCE retains exact ${relatedForm} binding and contributors after reload`, true, {
      stepId: savedRelated.id, anchorColumnId: savedRelated.operation.relatedSource.anchorColumnId,
      outputColumnId: savedRelated.operation.relatedSource.outputColumnId,
      expectedPatientIDs: oracle.patientIDsInCompilerOrder,
      expectedRelatedValue,
    });
    let relatedBaseline = structuredClone(builder);
    let expectedSplitPivotWorkspace;
    let expectedSplitRows;

    if (splitGroupKey) {
      const coalescedRelatedBaseline = structuredClone(builder);
      const originalRelatedStep = structuredClone(savedRelated);
      const originalRelatedOperation = structuredClone(savedRelated.operation.relatedSource);
      expectedSplitRows = (construction, { requireRelatedSource = true } = {}) => {
        const pivotStep = construction.steps.find(step => step.operation.kind === 'PIVOT');
        assert(pivotStep, 'The upstream role-swap proposal must retain the Pivot step.');
        const pivot = pivotStep.operation.pivot;
        assert.deepEqual(pivot.groupKeyIds, [sourceColumns.id.columnId], 'Pivot must group by Observation.id after the role swap.');
        assert.equal(pivot.categoryColumnId, sourceColumns.patientReference.columnId, 'Pivot categories must remain bound to subject.reference.');
        assert.equal(pivot.valueColumnId, sourceColumns.status.columnId, 'Pivot values must become the raw Observation.status field.');
        assert.deepEqual(pivot.categories.map(category => category.key.string).sort(),
          oracle.members.map(({ observation }) => observation.patientReference).sort(),
          'Pivot category domain must preserve the exact two raw Patient references.');
        const groupOutput = pivotStep.outputs.find(output => output.id === sourceColumns.id.columnId);
        assert(groupOutput, 'Split Pivot must expose the exact Observation.id group output.');
        const categoryOutputs = pivot.categories.map(category => {
          const output = pivotStep.outputs.find(column => column.id === category.outputColumnId);
          assert(output, `Split Pivot category ${category.key.string} must retain its output identity.`);
          return { key: category.key.string, label: output.label };
        });
        assert.equal(new Set(categoryOutputs.map(({ key }) => key)).size, 2);
        assert.equal(new Set(categoryOutputs.map(({ label }) => label)).size, 2);

        const relatedStep = construction.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
        if (requireRelatedSource) {
          assert(relatedStep, 'The upstream Pivot role swap must preserve the downstream RELATED_SOURCE step.');
          assertRelatedSourceStepAfterUpstreamChange(originalRelatedStep, relatedStep, pivotStep);
          const { related } = proveSourceBinding(
            relatedStep,
            capabilities.selectedStage.rowIdentityColumn,
            outputLabelOf(relatedStep),
            'ALL',
            'resourceType',
          );
        } else {
          assert.equal(relatedStep, undefined, 'The restored split Pivot must not retain RELATED_SOURCE.');
        }

        return oracle.members.map(({ observation }) => {
          const contributor = oracle.expectedSplitRowsByObservationID[observation.id];
          assert(contributor, `Raw split oracle must contain Observation ${observation.id}.`);
          const row = { [groupOutput.label]: observation.id };
          for (const category of categoryOutputs) {
            row[category.label] = category.key === contributor.patientReference ? contributor.status : null;
          }
          if (relatedStep) {
            const output = relatedStep.outputs.find(column => column.id === relatedStep.operation.relatedSource.outputColumnId);
            assert(output, 'RELATED_SOURCE must retain the exact authored output identity.');
            row[output.label] = contributor.patientResourceTypesInCompilerOrder;
          }
          return row;
        });
      };
      const splitPivotProposal = async name => {
        const startedAt = await editPivotRoles({
          fromGroup: sourceColumns.status,
          toGroup: sourceColumns.id,
          toValue: sourceColumns.status,
        });
        return nativeProposal(name, startedAt, [], response => {
          const expectedRows = expectedSplitRows(response.candidateConstruction);
          return { expectedRows, pivotRowCount: expectedRows.length, contributorIDs: expectedRows.map(row => row[sourceColumns.id.label]) };
        });
      };

      await splitPivotProposal('Upstream Pivot role-swap preview splits the coalesced row by exact Observation IDs');
      await cancel(coalescedRelatedBaseline, relatedRows, 'Canceling upstream Pivot split preserves the coalesced related-source workspace and values');
      recordCheck('Upstream Pivot split Cancel preserves the exact coalesced row and downstream binding after reload',
        builder.draftDigest === coalescedRelatedBaseline.draftDigest
          && isDeepStrictEqual(steps(builder).find(step => step.operation.kind === 'RELATED_SOURCE')?.operation.relatedSource, originalRelatedOperation),
        { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, relatedSource: originalRelatedOperation });

      const splitApplyProposal = await splitPivotProposal('Confirmed upstream Pivot role-swap preview preserves exact per-contributor Patient values');
      const relatedOutputLabel = outputLabelOf(savedRelated);
      pivotRows = splitApplyProposal.expectedRows.map(row => Object.fromEntries(
        Object.entries(row).filter(([label]) => label !== relatedOutputLabel),
      ));
      relatedRows = splitApplyProposal.expectedRows;
      await apply(relatedRows, 'Apply upstream Pivot split and reload one exact Patient resourceType per partition');
      savedPivotStep = steps(builder).find(step => step.operation.kind === 'PIVOT');
      savedRelated = steps(builder).find(step => step.operation.kind === 'RELATED_SOURCE');
      assert(savedPivotStep && savedRelated, 'Applied upstream Pivot split must retain Pivot and RELATED_SOURCE steps.');
      assert.deepEqual(savedPivotStep.operation.pivot.groupKeyIds, [sourceColumns.id.columnId]);
      assert.equal(savedPivotStep.operation.pivot.valueColumnId, sourceColumns.status.columnId);
      assert.deepEqual(savedRelated.operation.relatedSource, originalRelatedOperation);
      const splitCapabilities = await api(base + '/construction-capabilities', {
        snapshotToken: builder.catalog.snapshotToken,
        expectedDraftVersion: builder.draftVersion,
        expectedDraftDigest: builder.draftDigest,
        outputId,
        stageId: savedPivotStep.id,
      });
      assert.equal(splitCapabilities.selectedStage.rowIdentityColumn, capabilities.selectedStage.rowIdentityColumn,
        'The upstream split must preserve the Pivot stage identity used by the authored RELATED_SOURCE anchor.');
      proveSourceBinding(savedRelated, splitCapabilities.selectedStage.rowIdentityColumn, outputLabelOf(savedRelated), 'ALL', 'resourceType');
      capabilities = splitCapabilities;
      relatedBaseline = structuredClone(builder);
      expectedSplitPivotWorkspace = withoutRelatedSourceFromWorkspace(relatedBaseline.workspace, {
        outputId,
        stepId: savedRelated.id,
        outputColumnId: savedRelated.operation.relatedSource.outputColumnId,
      });
      recordCheck('Applied upstream Pivot split preserves two exact partitions and the authored Patient.resourceType ALL binding after reload', true, {
        pivotStepId: savedPivotStep.id,
        rowCount: relatedRows.length,
        observationIDs: oracle.members.map(({ observation }) => observation.id),
        patientReferences: oracle.members.map(({ observation }) => observation.patientReference),
        relatedSource: savedRelated.operation.relatedSource,
        rowIdentityColumn: capabilities.selectedStage.rowIdentityColumn,
      });
    }

    const expectedRelatedRows = (rows, label) => rows.map(row => {
      if (!splitGroupKey) return { ...row, [label]: expectedRelatedValue };
      const observationID = row[sourceColumns.id.label];
      const contributor = oracle.expectedSplitRowsByObservationID[observationID];
      assert(contributor, `Expected split oracle must contain Pivot group ${observationID}.`);
      return { ...row, [label]: contributor.patientResourceTypesInCompilerOrder };
    });

    const editedLabel = outputLabel;
    const editRelatedLabel = async () => {
      await click(`[data-testid="construction-history-step-${savedRelated.id}"]`);
      await click(`[data-testid="construction-edit-step-${savedRelated.id}"]`);
      const selector = '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]';
      await wait(({ selector }) => Boolean(document.querySelector(selector + ':not(:disabled)')), { selector });
      const startedAt = Date.now();
      await fill(selector, editedLabel);
      return startedAt;
    };
    let editStart = await editRelatedLabel();
    const editCancelProposal = await nativeProposal(`Edited related-source label keeps exact contributor ${outputValueLabel}`, editStart, [], response => {
      const sourceStep = response.candidateConstruction.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
      const { related, output } = proveSourceBinding(sourceStep, capabilities.selectedStage.rowIdentityColumn, editedLabel, relatedForm, sourcePath);
      const expected = expectedRelatedRows(pivotRows, editedLabel);
      assertProtocolRows(response.preview, expected, 'Related-source edit cancel proposal');
      return { expectedRows: expected, step: sourceStep, related, output };
    });
    await cancel(relatedBaseline, relatedRows, `Cancel related-source edit and reload unchanged ${relatedForm} binding`);
    recordCheck(`Editing the saved related source and Cancel preserves its exact construction and ${formValueLabel}`,
      builder.draftDigest === relatedBaseline.draftDigest && steps(builder).some(step => step.id === savedRelated.id),
      { draftVersion: builder.draftVersion, sourceStepId: savedRelated.id });

    savedRelated = steps(builder).find(step => step.operation.kind === 'RELATED_SOURCE');
    editStart = await editRelatedLabel();
    const editApplyProposal = await nativeProposal('Apply related-source edit preserves the post-Pivot binding', editStart, [], response => {
      const sourceStep = response.candidateConstruction.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
      const { related, output } = proveSourceBinding(sourceStep, capabilities.selectedStage.rowIdentityColumn, editedLabel, relatedForm, sourcePath);
      const expected = expectedRelatedRows(pivotRows, editedLabel);
      assertProtocolRows(response.preview, expected, 'Related-source edit apply proposal');
      const editEvidence = relatedSourceEditEvidence(savedRelated, sourceStep, editedLabel);
      assert(editEvidence.ok, `Label-only proposal changed more than the output label: ${JSON.stringify(editEvidence)}`);
      return { expectedRows: expected, step: sourceStep, related, output, editEvidence };
    });
    await apply(editApplyProposal.expectedRows, `Apply edited related-source label and reload exact contributor ${outputValueLabel}`);
    savedRelated = steps(builder).find(step => step.operation.kind === 'RELATED_SOURCE');
    assert.equal(savedRelated.operation.relatedSource.outputColumnId, relatedBaseline.workspace.documents[0].construction.steps.find(step => step.operation.kind === 'RELATED_SOURCE').operation.relatedSource.outputColumnId);
    proveSourceBinding(savedRelated, capabilities.selectedStage.rowIdentityColumn, editedLabel, relatedForm, sourcePath);
    const appliedEditEvidence = relatedSourceEditEvidence(
      relatedBaseline.workspace.documents[0].construction.steps.find(step => step.operation.kind === 'RELATED_SOURCE'),
      savedRelated,
      editedLabel,
    );
    recordCheck(`Applied related-source edit changes only the label and survives reload with exact Patient ${outputValueLabel}`, appliedEditEvidence.ok, {
      ...appliedEditEvidence, stepId: savedRelated.id,
      outputColumnId: savedRelated.operation.relatedSource.outputColumnId,
      label: outputLabelOf(savedRelated), form: savedRelated.operation.relatedSource.form,
    });
    const editedBaseline = structuredClone(builder);
    const restoredPivot = doc(pivotBaseline).construction;

    const removeRelated = async () => {
      await click(`[data-testid="construction-history-step-${savedRelated.id}"]`);
      const startedAt = Date.now();
      await click(`[data-testid="construction-remove-step-${savedRelated.id}"]`);
      return startedAt;
    };
    let removeStart = await removeRelated();
    const removeCancelProposal = await nativeProposal(
      splitGroupKey
        ? 'Related-source removal preview restores the exact two-partition Pivot values'
        : 'Related-source removal preview restores exact Pivot values',
      removeStart,
      pivotRows,
      response => {
      assert(!response.candidateConstruction.steps.some(step => step.operation.kind === 'RELATED_SOURCE'),
        'Removal candidate must omit only the selected post-Pivot RELATED_SOURCE.');
      if (splitGroupKey) {
        assert.deepEqual(expectedSplitRows(response.candidateConstruction, { requireRelatedSource: false }), pivotRows,
          'Removing RELATED_SOURCE must restore the exact two-partition Pivot values.');
        const expectedDocument = expectedSplitPivotWorkspace.documents.find(document => document.output?.id === outputId);
        assert.deepEqual(response.candidateConstruction, expectedDocument.construction,
          'Removal proposal must restore the complete saved split Pivot construction after deleting only RELATED_SOURCE.');
      } else {
        assert.deepEqual(response.candidateConstruction, restoredPivot,
          'Removal candidate must restore the exact saved Pivot construction.');
      }
      return { restoredStepCount: response.candidateConstruction.steps.length };
    });
    await cancel(editedBaseline, editApplyProposal.expectedRows, 'Cancel related-source removal preserves edited binding after reload');
    recordCheck(`Related-source removal Cancel preserves the edited binding and exact ${formValueLabel} after reload`,
      builder.draftDigest === editedBaseline.draftDigest && steps(builder).some(step => step.operation.kind === 'RELATED_SOURCE'),
      { sourceStepId: savedRelated.id, draftVersion: builder.draftVersion });

    savedRelated = steps(builder).find(step => step.operation.kind === 'RELATED_SOURCE');
    removeStart = await removeRelated();
    const removeApplyProposal = await nativeProposal(
      splitGroupKey
        ? 'Confirmed related-source removal restores both exact Pivot partitions'
        : 'Confirmed related-source removal restores the exact Pivot row',
      removeStart,
      pivotRows,
      response => {
      assert(!response.candidateConstruction.steps.some(step => step.operation.kind === 'RELATED_SOURCE'));
      if (splitGroupKey) {
        assert.deepEqual(expectedSplitRows(response.candidateConstruction, { requireRelatedSource: false }), pivotRows);
        const expectedDocument = expectedSplitPivotWorkspace.documents.find(document => document.output?.id === outputId);
        assert.deepEqual(response.candidateConstruction, expectedDocument.construction,
          'Confirmed removal must preserve every split Pivot step and output after deleting RELATED_SOURCE.');
      } else {
        assert.deepEqual(response.candidateConstruction, restoredPivot);
      }
      return { restoredStepCount: response.candidateConstruction.steps.length };
    });
    await apply(pivotRows, 'Apply related-source removal and restore exact Pivot output');
    if (splitGroupKey) {
      assert.deepEqual(expectedSplitRows(doc(builder).construction, { requireRelatedSource: false }), pivotRows,
        'Applying related-source removal must restore both exact split Pivot partitions.');
      const expectedDocument = expectedSplitPivotWorkspace.documents.find(document => document.output?.id === outputId);
      assert.deepEqual(doc(builder).construction, expectedDocument.construction,
        'Applying related-source removal must restore the exact split Pivot construction.');
      assert.deepEqual(doc(builder).columns, expectedDocument.columns,
        'Removing RELATED_SOURCE must preserve every authored source column and remove only its output column.');
      assert.deepEqual(doc(builder).population, expectedDocument.population,
        'Removing RELATED_SOURCE must preserve the exact selected source population.');
      assert.deepEqual(doc(builder).rows, expectedDocument.rows,
        'Removing RELATED_SOURCE must preserve the exact authored row definition.');
    } else {
      assert.deepEqual(doc(builder).construction, restoredPivot,
        'Applying related-source removal must restore the saved Pivot construction exactly.');
    }
    const fullWorkspaceRestored = splitGroupKey
      ? sameWorkspace({ workspace: expectedSplitPivotWorkspace }, builder)
      : sameWorkspace(pivotBaseline, builder);
    recordCheck(splitGroupKey
      ? 'Applying related-source removal restores the exact two-partition Pivot workspace and values'
      : 'Applying related-source removal restores the exact pre-source Pivot workspace and values', fullWorkspaceRestored, {
      pivotStepId: savedPivotStep.id, constructionStepCount: doc(builder).construction.steps.length,
      removedRelatedStepId: savedRelated.id, restoredRows: pivotRows,
      fullWorkspaceRestored,
    });
    await openTable(pivotRows, splitGroupKey
      ? 'Reload after RELATED_SOURCE removal restores both exact Pivot partitions'
      : 'Final reload restores exact Pivot categories and rows');

    if (splitGroupKey) {
      const restoreStart = await editPivotRoles({
        fromGroup: sourceColumns.id,
        toGroup: sourceColumns.status,
        toValue: sourceColumns.id,
      });
      const restoreProposal = await nativeProposal(
        'Pivot role restoration preview coalesces the exact original contributors',
        restoreStart,
        [],
        response => {
          assert(!response.candidateConstruction.steps.some(step => step.operation.kind === 'RELATED_SOURCE'),
            'Final Pivot restoration must not recreate the removed RELATED_SOURCE step.');
          assert(sameConstructionIgnoringPivotCategoryOutputIDs(restoredPivot, response.candidateConstruction),
            'Restored Pivot must reproduce the original construction except for regenerated category output IDs.');
          assert.deepEqual(publicPreviewSchema(response.preview), coalescedPivotPreviewSchema,
            'Restored Pivot must reproduce the original public labels, types, nullability, and shapes.');
          const verified = assertPivotProposal(response, pair, sourceColumns.id.label);
          return { expectedRows: verified.expectedRows, restoredRowCount: verified.expectedRows.length };
        },
      );
      const restoredPivotPreview = await apply(coalescedPivotRows, 'Apply Pivot role restoration and reload the exact coalesced row');
      const restoredPivotConstruction = doc(builder).construction;
      const coalescedConstructionRestored = sameConstructionIgnoringPivotCategoryOutputIDs(restoredPivot, restoredPivotConstruction);
      assert(coalescedConstructionRestored,
        'Final Pivot role restoration must reproduce the original construction except for regenerated category output IDs.');
      assert.deepEqual(publicPreviewSchema(restoredPivotPreview), coalescedPivotPreviewSchema,
        'Restored Pivot must retain the original public labels, types, nullability, and shapes after reload.');
      const originalWorkspaceRestored = sameWorkspaceIgnoringPivotCategoryOutputIDs(pivotBaseline, builder);
      recordCheck('Applying Pivot role restoration returns original coalesced workspace with regenerated category output IDs',
        originalWorkspaceRestored && coalescedConstructionRestored
          && canonicalRows(restoreProposal.expectedRows).join() === canonicalRows(coalescedPivotRows).join(), {
          restoredRows: restoreProposal.expectedRows,
          originalRows: coalescedPivotRows,
          originalWorkspaceRestored,
          coalescedConstructionRestored,
          originalCategoryOutputIDs: restoredPivot.steps.find(step => step.operation.kind === 'PIVOT').operation.pivot.categories.map(category => category.outputColumnId),
          restoredCategoryOutputIDs: restoredPivotConstruction.steps.find(step => step.operation.kind === 'PIVOT').operation.pivot.categories.map(category => category.outputColumnId),
        });
      await openTable(coalescedPivotRows, 'Final reload restores the original coalesced Pivot row');

      const originalSourceDocument = doc(beforePivot);
      const removePivot = async () => {
        const pivotStep = steps(builder).find(step => step.operation.kind === 'PIVOT');
        assert(pivotStep, 'The restored original Pivot step must exist before its removal lifecycle.');
        await click(`[data-testid="construction-history-step-${pivotStep.id}"]`);
        const startedAt = Date.now();
        await click(`[data-testid="construction-remove-step-${pivotStep.id}"]`);
        return startedAt;
      };
      const pivotRemovalBaseline = structuredClone(builder);
      let pivotRemovalStart = await removePivot();
      await nativeProposal(
        'Pivot removal preview restores exact Observation source construction and original raw rows',
        pivotRemovalStart,
        sourceRows,
        response => {
          assert(sameWorkspaceIgnoringEmptySourceConstruction(
            { workspace: { documents: [{ output: { id: outputId }, construction: originalSourceDocument.construction }] } },
            { workspace: { documents: [{ output: { id: outputId }, construction: response.candidateConstruction }] } },
            outputId,
          ), 'Pivot removal preview must restore the original Observation source construction, allowing only its canonical empty default.');
          return { restoredSourceStepCount: response.candidateConstruction.steps.length, sourceColumns: originalSourceDocument.columns };
        },
      );
      await cancel(pivotRemovalBaseline, coalescedPivotRows,
        'Pivot removal Cancel preserves the coalesced Pivot workspace and rows after reload');
      recordCheck('Pivot removal Cancel preserves the exact coalesced Pivot workspace and rows after reload',
        sameWorkspace(pivotRemovalBaseline, builder)
          && canonicalRows(coalescedPivotRows).length === 1,
        { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });

      pivotRemovalStart = await removePivot();
      await nativeProposal(
        'Confirmed Pivot removal restores exact Observation source construction and original raw rows',
        pivotRemovalStart,
        sourceRows,
        response => {
          assert(sameWorkspaceIgnoringEmptySourceConstruction(
            { workspace: { documents: [{ output: { id: outputId }, construction: originalSourceDocument.construction }] } },
            { workspace: { documents: [{ output: { id: outputId }, construction: response.candidateConstruction }] } },
            outputId,
          ), 'Confirmed Pivot removal must restore the original Observation source construction, allowing only its canonical empty default.');
          return { restoredSourceStepCount: response.candidateConstruction.steps.length };
        },
      );
      await apply(sourceRows, 'Apply Pivot removal and reload exact original Observation source rows');
      const restoredSourceDocument = doc(builder);
      const sourceWorkspaceRestored = sameWorkspaceIgnoringEmptySourceConstruction(beforePivot, builder, outputId)
        && isDeepStrictEqual(restoredSourceDocument.columns, originalSourceDocument.columns)
        && isDeepStrictEqual(restoredSourceDocument.population, originalSourceDocument.population)
        && isDeepStrictEqual(restoredSourceDocument.rows, originalSourceDocument.rows);
      recordCheck('Applying Pivot removal restores exact Observation source construction, columns, population, and rows',
        sourceWorkspaceRestored, {
          sourceWorkspaceRestored,
          restoredSourceColumnCount: restoredSourceDocument.columns.length,
          restoredPopulation: restoredSourceDocument.population,
          restoredRows: sourceRows,
        });
      await openTable(sourceRows, 'Final reload restores the exact original Observation source rows and schema');
      builder = await api(base + '/builder');
      assert(sameWorkspaceIgnoringEmptySourceConstruction(beforePivot, builder, outputId),
        'Final source-table reload must restore the exact pre-Pivot Builder workspace, allowing only its canonical empty construction default.');
      recordCheck('Final reload restores the exact original Observation source rows and schema',
        sameWorkspaceIgnoringEmptySourceConstruction(beforePivot, builder, outputId)
          && isDeepStrictEqual(doc(builder).columns, originalSourceDocument.columns)
          && isDeepStrictEqual(doc(builder).population, originalSourceDocument.population)
          && isDeepStrictEqual(doc(builder).rows, originalSourceDocument.rows), {
          sourceWorkspaceRestored: sameWorkspaceIgnoringEmptySourceConstruction(beforePivot, builder, outputId),
          restoredRows: sourceRows,
        });
    }

    const unfinishedOwnedRequests = report.nativeRequests.slice(requestCaptureStartIndex)
      .filter(entry => !Number.isFinite(entry.completedAt));
    await Promise.all(unfinishedOwnedRequests.map(entry =>
      waitForCdaCapturedResponse(cda, requestCapture, captured => captured === entry, 5000)));
    await requestCapture.flush();
    await new Promise(resolve => setImmediate(resolve));
    const unexpectedDiagnostics = cda.report.errors.length + cda.diagnostics.networkFailures.length
      + cda.diagnostics.httpFailures.length + cda.diagnostics.pageErrors.length + cda.diagnostics.console.length;
    recordCheck('No unexpected native network, module, or browser errors occurred', unexpectedDiagnostics === 0, {
      errors: cda.report.errors, networkFailures: cda.diagnostics.networkFailures,
      httpFailures: cda.diagnostics.httpFailures, pageErrors: cda.diagnostics.pageErrors, console: cda.diagnostics.console,
    });
    const actionRecords = cda.report.actions ?? [];
    const actionTimings = report.cases ?? [];
    const withinBudget = actionRecords.length > 0 && actionTimings.length > 0
      && actionRecords.every(action => action.status === 'passed' && action.elapsedMs <= 5000)
      && actionTimings.every(action => action.elapsedMs <= 5000);
    recordCheck('All native lifecycle actions complete within five seconds', withinBudget, {
      actionCount: actionRecords.length,
      measuredTransitionCount: actionTimings.length,
      workflowCheckpoints: actionTimings.map(({ name, elapsedMs }) => ({ name, durationMs: elapsedMs })),
      maxActionMs: actionRecords.reduce((max, action) => Math.max(max, action.elapsedMs ?? 0), 0),
    });
    report.status = 'passed';
  } finally {
    await requestCapture.flush();
    report.nativeRequestCount = cda.report.nativeRequests.length;
    report.finished = new Date().toISOString();
    const caseName = splitGroupKey
      ? 'related-resource-type-after-pivot-group-split'
      : relatedForm === 'COUNT'
        ? 'related-source-count-after-pivot'
        : sourcePath === 'id' ? 'related-source-after-pivot' : 'related-resource-type-after-pivot';
    await cda.attachReport(caseName, report);
  }
}

function outputLabelOf(step) {
  const related = step?.operation?.relatedSource;
  return step?.outputs?.find(column => column.id === related?.outputColumnId)?.label;
}
