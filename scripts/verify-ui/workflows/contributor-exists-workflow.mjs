import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import {
  classifyPendingRelatedExpandChoicesAfterProposalCancel,
  proposalCancelActionSelector,
  snapshotPendingRelatedExpandChoices,
} from '../helpers/related-expand-cancel.mjs';
import { constructionCandidateWireEquivalent } from '../helpers/builder-combine-draft-helpers.mjs';
import { assertCompletePreviewRows, expectedPreviewColumns } from '../helpers/complete-preview-row-projection.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';

export function assertCompletePreview(preview, { expectedRows, expectedColumns, outputId, receiptId, label = 'Preview' }) {
  assert(Array.isArray(expectedRows), `${label}: raw oracle rows are required`);
  assert(expectedRows.length < 25, `${label}: raw oracle has ${expectedRows.length} rows, which can reach the native preview cap`);
  assert(preview && typeof preview === 'object', `${label}: native response omitted its preview`);
  assert.equal(preview.outputId, outputId, `${label}: preview belongs to another output`);
  assert.equal(preview.receiptId, receiptId, `${label}: preview belongs to another receipt`);
  assert.equal(preview.sampled, false, `${label}: preview is sampled or completeness is unknown`);
  assert.equal(preview.partialValidation, false, `${label}: preview has partial validation`);
  assert.equal(preview.rowCount, expectedRows.length, `${label}: native complete row count differs from the raw oracle`);
  assert(Array.isArray(preview.rows), `${label}: native response omitted preview rows`);
  assert.equal(preview.rows.length, expectedRows.length, `${label}: native response row payload is incomplete`);
  const apiExpectedRows = expectedRows.map(row => row.map(value => value === '—' ? null : value));
  assertCompletePreviewRows(preview, expectedColumns, apiExpectedRows, label);
  return { outputId, receiptId, rowCount: preview.rowCount, sampled: preview.sampled, partialValidation: preview.partialValidation };
}

export function buildContributorExistsRawQueryInvocation(container, query) {
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  return buildArangoShellInvocation({ container, script, database: 'loom_dev' });
}

export function unfilteredRelatedExpandStep(proposalRequest) {
  const steps = proposalRequest?.candidateConstruction?.steps;
  assert(Array.isArray(steps), 'Native ALL_MATCHES baseline proposal was not captured as a top-level request body');
  const step = steps.find(candidate => candidate.operation?.kind === 'RELATED_EXPAND');
  assert(step, 'Native ALL_MATCHES baseline proposal omitted its related-expand step');
  assert.deepEqual(step.operation.relatedExpand?.contributorRule, { policy: 'ALL_MATCHES' },
    'All matching baseline must omit the contributor predicate');
  return step;
}

export function proposalResponseCandidateMatchesRequest(proposalRequest, responseCandidate) {
  const expectedCandidate = structuredClone(proposalRequest?.candidateConstruction);
  assert(expectedCandidate && Array.isArray(expectedCandidate.steps),
    'Native proposal request must contain its exact candidate construction');
  const changedStepMatches = expectedCandidate.steps.filter(step => step?.id === proposalRequest.changedStepId);
  if (proposalRequest.changedStepId) {
    assert.equal(changedStepMatches.length, 1, 'Changed proposal step must exist exactly once in its submitted candidate');
  }
  if (changedStepMatches.length === 1 && changedStepMatches[0].operation?.kind === 'RELATED_EXPAND') {
    const relatedExpand = changedStepMatches[0].operation.relatedExpand;
    assert(relatedExpand && ['ERROR', 'EXCLUDE', 'PRESERVE_PARENT'].includes(relatedExpand.emptyPolicy),
      'Changed related-expand step must carry its exact empty policy');
    const outputs = changedStepMatches[0].outputs;
    assert(Array.isArray(outputs), 'Changed related-expand step must declare its output schema');
    const outputMatches = outputs.filter(output => output?.id === relatedExpand.relatedRecordColumnId);
    assert.equal(outputMatches.length, 1, 'Changed related-expand must declare exactly one related-record output');
    outputMatches[0].nullable = relatedExpand.emptyPolicy === 'PRESERVE_PARENT';
  }
  return constructionCandidateWireEquivalent(expectedCandidate, responseCandidate);
}

export async function contributorExistsWorkflow({ page, cda, caseOptions = {} }) {
  const { click, fill, selectOption, navigate, inspect, clickControl, fillControl, selectControl,
    nativeClick, nativeFill, nativeSelect, navigatePage, inspectDOM, browserEval, inspectPage,
    waitForDOM, waitForBrowser, captureCDARequests, captureRequests, waitForCapturedResponse,
    performAction, requireUnique } = createNativeCdaWorkflowTools({ page, cda });
  const recordCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, Boolean(passed), evidence);
  const target = cda.target;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target, caseOptions.arangoContainer);
  assert(project && generation && apiOrigin && uiOrigin, 'The CDA fixture must bind project, generation, API origin, and UI origin explicitly.');
  assert.equal(generation, 'cda-fhir-v1', 'This verifier requires the loaded CDA FHIR generation.');
const explorer = `contributor-exists-browser-${Date.now()}`;

const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`;
const report = {
  explorer,
  controls: {
    covered: ['Contributor mode: all matching related records with no predicate',
      'Contributor mode: only records meeting a condition', 'Related-record field selection: Observation id',
      'Contributor predicate: EXISTS against independently inspected CDA source rows',
      'No-match policy: ERROR expected validation and enabled PRESERVE_PARENT repair, then EXCLUDE edit',
      'Preview, Cancel, Apply, reload, edit, remove Cancel, remove Apply, exact source restoration'],
    uncovered: ['EQUALS predicate (covered by verify-cda-contributor-rules-browser.mjs)',
      'Code-valued fields and value suggestions', 'Alternative related paths and starting anchors'],
  },
  cases: [], requests: [], nativeRequests: [], errors: [], expectedErrorWindow: { active: false }, started: new Date().toISOString(),
};

let builder;
let original;
let outputId;
let requestCapture;
let latestReadyProposal;

const api = async (path, body) => {
  const startedAt = Date.now();
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': `native-cda-${randomUUID()}` };
  const url = apiOrigin + path;
  const response = body === undefined
    ? await cda.request.get(url, { headers, timeout: 30000 })
    : await cda.request.post(url, { headers, data: body, timeout: 30000 });
  const value = await response.json();
  const apiEvidence = { path, status: response.status(), body, response: value, startedAt, completedAt: Date.now() };
  report.apiCalls?.push(apiEvidence);
  report.requests?.push(apiEvidence);
  assert(response.ok(), `${path}: ${JSON.stringify(value)}`);
  return value;
};

const rawQuery = (query) => {
  const invocation = buildContributorExistsRawQueryInvocation(arangoContainer, query);
  const result = spawnSync(invocation.command, invocation.args,
    { encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango did not return JSON: ${result.stdout.slice(-500)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};

const byBucketCountsAreExact = witnesses => {
  const counts = Object.fromEntries(witnesses.map(item => [item.bucket, item.observations.length]));
  return counts.zero === 0 && counts.one === 1 && counts.many >= 2 && counts.many <= 22;
};

const sourceWitnesses = () => {
  const findPatient = (bucket, predicate) => {
    const query = `FOR p IN Patient
      FILTER p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
      LET sample = (
        FOR e IN fhir_edge
          FILTER e._to == p._id AND e.label == "subject_Patient"
            AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
            AND STARTS_WITH(e._from, "Observation/")
          COLLECT observationKey = e._from
          LIMIT 23
          RETURN observationKey
      )
      LET sampleCount = LENGTH(sample)
      FILTER ${predicate}
      SORT p.id
      LIMIT 1
      RETURN { id: p.id, _id: p._id }`;
    const [patient] = rawQuery(query);
    assert(patient?.id && patient?._id, `CDA fixture has no Patient with ${bucket} related Observation records`);
    return { bucket, patient };
  };

  const selected = [
    findPatient('zero', 'sampleCount == 0'),
    findPatient('one', 'sampleCount == 1'),
    findPatient('many', 'sampleCount >= 2 AND sampleCount < 23'),
  ];
  assert.equal(new Set(selected.map((item) => item.patient._id)).size, 3, 'CDA zero/one/many witnesses must be distinct');
  const refs = selected.map((item) => item.patient._id);
  const serializedSources = JSON.stringify(selected.map(({ bucket, patient }) => ({ bucket, ...patient })));
  const detailsQuery = `FOR source IN ${serializedSources}
    LET patient = DOCUMENT(source._id)
    LET observations = (
      FOR e IN fhir_edge
        FILTER e._to == patient._id AND e.label == "subject_Patient"
          AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
          AND STARTS_WITH(e._from, "Observation/")
        COLLECT observationKey = e._from
        LET observation = DOCUMENT(observationKey)
        FILTER observation.project == ${JSON.stringify(project)}
          AND observation.dataset_generation == ${JSON.stringify(generation)}
        SORT observation.id
        RETURN { id: observation.id, _id: observation._id }
    )
    RETURN { bucket: source.bucket, patient: { id: patient.id, _id: patient._id }, observations }`;
  const witnesses = rawQuery(detailsQuery);
  const byBucket = Object.fromEntries(witnesses.map((item) => [item.bucket, item]));
  assert.deepEqual(Object.keys(byBucket).sort(), ['many', 'one', 'zero']);
  assert.equal(byBucket.zero.observations.length, 0);
  assert.equal(byBucket.one.observations.length, 1);
  assert(byBucket.many.observations.length >= 2 && byBucket.many.observations.length <= 22,
    'CDA many witness must keep every lifecycle preview strictly below the 25-row native cap');
  assert.equal(new Set(refs).size, 3);
  return witnesses;
};

const command = async (commands) => {
  await api(base + '/commands', {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(base + '/builder');
};

const documentForOutput = (state = builder) => state.workspace.documents.find((item) => item.output.id === outputId);
  const withoutStageIdentity = (columns) => columns.map(({ columnId: _stageId, ...column }) => column);

const assertStableSourceProjection = (state, baseline, canonicalSourceColumnId, phase) => {
  const sourceColumnName = baseline.columns[0]?.column;
  const restored = documentForOutput(state).columns.find((column) => column.column === sourceColumnName);
  assert(restored, `${phase}: authored source column ${sourceColumnName} is missing`);
  assert.equal(restored.columnId, canonicalSourceColumnId,
    `${phase}: compiler-owned source column identity changed`);
  assert.deepEqual(withoutStageIdentity(documentForOutput(state).columns), withoutStageIdentity(baseline.columns),
    `${phase}: authored source column binding, label, physical name, or table presentation changed`);
};

const displayedRows = async () => inspectDOM(page, async args => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
    .map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length); });

const revealControl = async (selector, includes) => {
  let locator = page.locator(selector);
  if (includes !== undefined) locator = locator.filter({ hasText: includes });
  const label = `Reveal ${includes ?? selector}`;
  await performAction(page, label, locator, (target, options) => target.scrollIntoViewIfNeeded(options));
  const box = await locator.boundingBox();
  assert(box, `${label}: control has no visible bounding box after Playwright scrolling`);
  const viewport = page.viewportSize();
  assert(box.y >= 0 && box.y + box.height <= viewport.height,
    `Control remains outside the viewport after Playwright scrolling: ${JSON.stringify({ box, viewport })}`);
  return { top: box.y, bottom: box.y + box.height, viewportHeight: viewport.height };
};
const rowsMatch = (actual, expected) => Array.isArray(actual) && actual.length === expected.length
  && isDeepStrictEqual(actual.map(row => JSON.stringify(row)).sort(), expected.map(row => JSON.stringify(row)).sort());
const assertRows = (actual, expected, name) => {
  assert(expected.length < 25, `${name}: source witnesses reach the native preview limit (${expected.length})`);
  assert.equal(actual.length, expected.length, `${name}: visible row count differs`);
  assert.deepEqual(actual.map((row) => JSON.stringify(row)).sort(), expected.map((row) => JSON.stringify(row)).sort(),
    `${name}: rendered CDA row identities/values differ from the independent source oracle`);
};

const recordAction = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, ...details });
  return durationMs;
};

const rendered = async (expectedRows, name) => {
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === args.__template0 && !document.body.innerText.includes('Loading your table…')), { __template0: (String(Math.min(25, expectedRows.length) + 1)) });
  const rows = await displayedRows();
  assertRows(rows, expectedRows, name);
  return rows;
};

const proposal = async (name, startedAt, expectedRows) => {
  await waitForDOM(page, args => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await inspectDOM(page, async args => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {
      status:panel?.dataset.proposalStatus,text:panel?.innerText,
      proposalId:panel?.getAttribute('data-proposal-id'),
      preview:(()=>{const node=document.querySelector('[data-testid="construction-proposal-preview"]');return {
        status:node?.getAttribute('data-preview-status'),receiptId:node?.getAttribute('data-preview-receipt-id'),
        outputId:node?.getAttribute('data-preview-output-id')};})(),
      rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}; });
  assert.equal(result.status, 'ready', `${name}: ${result.text}`);
  assertRows(result.rows, expectedRows, name);
  const proposalEntry = await requestCapture.waitFor(entry => entry.path === `${base}/construction-proposals`
    && entry.method === 'POST' && entry.status === 200 && entry.startedAt >= startedAt
    && entry.body?.outputId === outputId, { timeoutMs: Math.max(1, startedAt + 5000 - Date.now()) });
  const proposalRequest = requestCapture.rawRequestBody(proposalEntry);
  const proposalResponse = requestCapture.rawResponseBody(proposalEntry);
  assert(proposalRequest && proposalResponse, `${name}: native proposal request/response bodies were not retained`);
  assert.equal(proposalResponse.previewStatus, 'READY', `${name}: native proposal did not finish previewing`);
  assert.equal(proposalResponse.outputId, outputId);
  assert.equal(proposalRequest.outputId, outputId);
  assert.equal(proposalRequest.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(proposalRequest.expectedDraftVersion, builder.draftVersion);
  assert.equal(proposalRequest.expectedDraftDigest, builder.draftDigest);
  assert(Number.isInteger(proposalRequest.limit) && proposalRequest.limit >= expectedRows.length && proposalRequest.limit <= 25,
    `${name}: native proposal limit cannot prove all ${expectedRows.length} raw rows`);
  assert.equal(proposalResponse.snapshotToken, proposalRequest.snapshotToken);
  assert.equal(proposalResponse.draftVersion, proposalRequest.expectedDraftVersion);
  assert.equal(proposalResponse.draftDigest, proposalRequest.expectedDraftDigest);
  assert(proposalResponse.proposalId, `${name}: proposal response omitted its receipt identity`);
  assert(proposalResponseCandidateMatchesRequest(proposalRequest, proposalResponse.candidateConstruction),
    `${name}: proposal response candidate differs from the exact submitted candidate`);
  assert.equal(result.proposalId, proposalResponse.proposalId, `${name}: proposal panel is bound to another proposal`);
  assert.deepEqual(result.preview, { status: 'ready', receiptId: proposalResponse.proposalId, outputId },
    `${name}: rendered proposal preview is not bound to its proposal receipt and output`);
  const previewEvidence = assertCompletePreview(proposalResponse.preview, {
    expectedRows,
    expectedColumns: expectedPreviewColumns(proposalResponse.candidateConstruction, original.columns),
    outputId, receiptId: proposalResponse.proposalId, label: name,
  });
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: expectedRows.length, proposalId: proposalResponse.proposalId, preview: result.rows, previewEvidence });
  latestReadyProposal = { request: proposalRequest, response: proposalResponse, entry: proposalEntry };
  result.proposalRequest = proposalRequest;
  result.proposalResponse = proposalResponse;
  return result;
};

const actionTimeRemaining = startedAt => Math.max(1, 5000 - (Date.now() - startedAt));
const readSavedPreviewDOM = () => inspectDOM(page, async args => {
  const preview = document.querySelector('[data-testid="construction-preview"]');
  return {
    status: preview?.getAttribute('data-preview-status') ?? null,
    receiptId: preview?.getAttribute('data-preview-receipt-id') ?? null,
    outputId: preview?.getAttribute('data-preview-output-id') ?? null,
    draftVersion: preview?.getAttribute('data-current-draft-version') ?? null,
    draftDigest: preview?.getAttribute('data-current-draft-digest') ?? null,
  };
});
const verifySavedPreview = async ({ expectedRows, name, startedAt, requestStart, savedBuilder }) => {
  const dom = await readSavedPreviewDOM();
  assert.equal(dom.status, 'ready', `${name}: saved preview is not ready`);
  assert.equal(dom.outputId, outputId, `${name}: saved preview is bound to another output`);
  assert(dom.receiptId, `${name}: saved preview omitted its receipt identity`);
  assert.equal(Number(dom.draftVersion), savedBuilder.draftVersion, `${name}: rendered preview belongs to a different draft version`);
  assert.equal(dom.draftDigest, savedBuilder.draftDigest, `${name}: rendered preview belongs to a different draft digest`);
  const entry = await requestCapture.waitFor(request => request.path === `${base}/preview`
    && request.method === 'POST' && request.status === 200 && request.startedAt >= startedAt
    && request.body?.outputId === outputId && request.body?.receiptId === dom.receiptId,
  { fromIndex: requestStart, timeoutMs: actionTimeRemaining(startedAt) });
  const request = requestCapture.rawRequestBody(entry);
  const response = requestCapture.rawResponseBody(entry);
  assert(request && response, `${name}: complete saved preview request/response was not retained`);
  assert.equal(request.outputId, outputId);
  assert.equal(request.receiptId, dom.receiptId);
  assert(Number.isInteger(request.limit) && request.limit >= expectedRows.length && request.limit <= 25,
    `${name}: native preview request cannot prove all ${expectedRows.length} raw rows: ${JSON.stringify(request)}`);
  const savedDocument = documentForOutput(savedBuilder);
  assert(savedDocument, `${name}: saved Builder omitted the preview output document`);
  const evidence = assertCompletePreview(response, {
    expectedRows,
    expectedColumns: expectedPreviewColumns(savedDocument.construction, savedDocument.columns),
    outputId, receiptId: dom.receiptId, label: name,
  });
  return { ...evidence, draftVersion: savedBuilder.draftVersion, draftDigest: savedBuilder.draftDigest };
};

const open = async (expectedRows, name, verifyReloadState) => {
  const startedAt = Date.now();
  const requestStart = report.nativeRequests.length;
  await navigatePage(page, pageURL);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-table-'+args.__template0+'"]')), { __template0: (outputId) });
  await clickControl(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  const rows = await rendered(expectedRows, name);
  builder = await api(base + '/builder');
  const savedPreview = await verifySavedPreview({ expectedRows, name, startedAt, requestStart, savedBuilder: builder });
  if (verifyReloadState) await verifyReloadState(rows, builder, savedPreview);
  recordAction(name, startedAt, { rowCount: rows.length, receiptId: savedPreview.receiptId });
  return rows;
};

const startRelatedExpand = async () => {
  const startedAt = Date.now();
  await clickControl(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false));
  await clickControl(page, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' select[aria-label="Related record type"]')?.disabled === false), { __template0: (panel) });
  await selectControl(page, `${panel} select[aria-label="Related record type"]`, 'Observation');
  const route = 'Patient <-[subject]- Observation';
  await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)), { __template0: (`${panel} input[aria-label="${route}"]`) });
  await clickControl(page, `${panel} input[aria-label="${route}"]`);
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' [data-testid="construction-related-expand-contributor-options"]')), { __template0: (panel) });
  recordAction('open-related-expand-editor', startedAt);
  return panel;
};

const openContributorOptions = async (panel) => {
  const options = `${panel} [data-testid="construction-related-expand-contributors"]`;
  const disclosure = `${panel} [data-testid="construction-related-expand-contributor-options"]`;
  await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)), { __template0: (disclosure+' summary') });
  const disclosureState = await inspectDOM(page, async args => { return document.querySelector(args.__template0)?.open ?? false; }, { __template0: (disclosure) });
  if (!disclosureState) {
    await revealControl(`${disclosure} summary`);
    await clickControl(page, `${disclosure} summary`);
    await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)?.open === true), { __template0: (disclosure) });
  }
  return { options, disclosure };
};

const chooseContributorIDExists = async (panel, actionName) => {
  const startedAt = Date.now();
  const { options } = await openContributorOptions(panel);
  const onlyRecords = `${options} label`;
  const search = `${options} input[placeholder="Search field name or path"]`;
  const contributorRequestPath = base + '/related-expand-contributors';
  const proposalRequestPath = base + '/construction-proposals';
  const contributorRequestStart = report.nativeRequests.length;
  const cancellationAction = 'switch to contributor condition and search for id';
  let filteredRequest;
  await cda.withExpectedCancellations({
    origin: uiOrigin,
    method: 'POST',
    paths: [contributorRequestPath, proposalRequestPath],
    requestIdPrefixes: ['cda-request-', 'construction-proposal-'],
    reason: 'Selecting a contributor condition invalidates the all-matching proposal; the explicit id search supersedes the unfiltered contributor lookup.',
    proof: {
      outputId,
      priorProposal: { contributorRule: 'ALL_MATCHES', emptyPolicy: 'PRESERVE_PARENT' },
      priorQuery: null,
      replacementQuery: 'id',
    },
    actionLabel: cancellationAction,
  }, async () => {
    await revealControl(onlyRecords, 'Only records meeting a condition');
    await clickControl(page, onlyRecords, { includes: 'Only records meeting a condition' });
    await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' input[placeholder="Search field name or path"]')), { __template0: (options) });
    await revealControl(search);
    await clickControl(page, search);
    await fillControl(page, search, 'id');
    filteredRequest = await requestCapture.waitFor(request => request.path === contributorRequestPath
      && request.body?.outputId === outputId && request.body?.query === 'id' && request.status === 200
      && request.response?.choices?.some(choice => choice.source?.path === 'id' && choice.source?.resourceType === 'Observation'),
    { fromIndex: contributorRequestStart, timeoutMs: 5000 });
    assert.equal(filteredRequest.status, 200, 'The superseding Observation id field search must return successfully');
    await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' [role="group"][aria-label="Fields for related-record condition"] button')), { __template0: (options) });
  });
  const cancellations = cda.report.expectedCancellations?.filter(item => item.proof?.scopeAction === cancellationAction) ?? [];
  assert(cancellations.length <= 2, 'Contributor mode and field search may classify at most one superseded request per exact path');
  const cancellationPaths = new Set();
  for (const cancellation of cancellations) {
    const cancelledRequest = report.nativeRequests.find(request => request.browserRequestId === cancellation.browserRequestId);
    assert(cancelledRequest, 'Expected contributor workflow cancellation must point to an exact captured browser request');
    assert([contributorRequestPath, proposalRequestPath].includes(cancelledRequest.path),
      'Expected contributor workflow cancellation must use one of its two exact owned paths');
    assert(!cancellationPaths.has(cancelledRequest.path),
      'Contributor mode and field search may classify at most one cancellation for each exact path');
    cancellationPaths.add(cancelledRequest.path);
    assert.equal(cancelledRequest.method, 'POST');
    assert.equal(cancelledRequest.origin, uiOrigin);
    assert.equal(cancelledRequest.body?.outputId, outputId);
    assert.equal(cancelledRequest.failure, 'net::ERR_ABORTED');
    assert.equal(cancellation.method, 'POST');
    assert.equal(new URL(cancellation.url).pathname, cancelledRequest.path);
    assert.equal(cancellation.proof?.scopeAction, cancellationAction);
    assert.equal(cancellation.proof?.outputId, outputId);
    assert.equal(cancelledRequest.expectedCancellation?.browserRequestId, cancelledRequest.browserRequestId);

    const cancelledErrors = report.errors.filter(error => error.kind === 'network'
      && error.browserRequestId === cancellation.browserRequestId);
    assert(cancelledErrors.length >= 1 && cancelledErrors.length <= 2,
      'Each exact native cancellation must retain its bounded browser network diagnostics');
    for (const cancelledError of cancelledErrors) {
      assert.equal(cancelledError.error ?? cancelledError.failure, 'net::ERR_ABORTED');
      cancelledError.expected = true;
      cancelledError.expectedCancellation = cancellation;
    }

    assert(cancelledRequest.startedAt <= filteredRequest.startedAt,
      'A superseded request must start before the successful id-search replacement');
    if (cancelledRequest.path === contributorRequestPath) {
      assert([undefined, null, ''].includes(cancelledRequest.body?.query),
        'The cancelled contributor request must be the initial unfiltered lookup');
      report.contributorSearchCancellation = {
        request: { browserRequestId: cancelledRequest.browserRequestId, path: cancelledRequest.path,
          method: cancelledRequest.method, outputId, query: cancelledRequest.body?.query ?? null,
          error: cancelledRequest.failure },
        replacement: { browserRequestId: filteredRequest.browserRequestId, query: 'id', status: filteredRequest.status,
          field: 'Observation.id' },
        reason: cancellation.reason,
      };
      continue;
    }

    const relatedSteps = cancelledRequest.body?.candidateConstruction?.steps
      ?.filter(step => step.operation?.kind === 'RELATED_EXPAND') ?? [];
    assert.equal(relatedSteps.length, 1, 'Cancelled stale proposal must contain one related-expansion step');
    const relatedExpand = relatedSteps[0].operation.relatedExpand;
    assert.deepEqual(relatedExpand.contributorRule, { policy: 'ALL_MATCHES' },
      'Only the stale unfiltered all-matching proposal may be classified as an expected cancellation');
    assert.equal(relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.equal(relatedExpand.contributorSource, undefined);
    assert.equal(relatedExpand.contributorRule.predicate, undefined);
    report.staleContributorProposalCancellation = {
      request: { browserRequestId: cancelledRequest.browserRequestId, path: cancelledRequest.path,
        method: cancelledRequest.method, outputId, contributorRule: relatedExpand.contributorRule,
        emptyPolicy: relatedExpand.emptyPolicy, error: cancelledRequest.failure },
      replacement: { browserRequestId: filteredRequest.browserRequestId, query: 'id', status: filteredRequest.status },
      reason: cancellation.reason,
    };
  }
  const choices = await inspectDOM(page, async args => { return [...document.querySelectorAll(''+args.__template0+' [role="group"][aria-label="Fields for related-record condition"] button')]
      .map(button=>({text:button.innerText.trim(),pressed:button.getAttribute('aria-pressed')})); }, { __template0: (options) });
  report.contributorChoices = choices;
  const fieldButton = choices.find((choice) => choice.text.split('\n')[0].trim() === 'id');
  assert(fieldButton, `Observation ID field was not offered: ${JSON.stringify(choices)}`);
  const contributorFieldButtons = `${options} [role="group"][aria-label="Fields for related-record condition"] button`;
  const fieldLabel = fieldButton.text.split('\n')[1]?.trim() ?? fieldButton.text;
  await revealControl(contributorFieldButtons, fieldLabel);
  await clickControl(page, contributorFieldButtons, { includes: fieldLabel });
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' select')?.value === 'EXISTS'), { __template0: (options) });
  await revealControl(`${options} select`);
  const selected = await inspectDOM(page, async args => { return {condition:document.querySelector(''+args.__template0+' select')?.value,
      field:document.querySelector(''+args.__template1+' [aria-pressed="true"]')?.innerText.trim(),
      options:[...document.querySelector(''+args.__template2+' select')?.options??[]].map(option=>({value:option.value,disabled:option.disabled}))}; }, { __template0: (options), __template1: (options), __template2: (options) });
  assert.equal(selected.condition, 'EXISTS');
  assert(selected.options.some((option) => option.value === 'EXISTS' && !option.disabled), JSON.stringify(selected));
  assert(selected.field?.includes('id'), JSON.stringify(selected));
  report.contributorRule = selected;
  recordAction(actionName, startedAt, { condition: selected.condition, sourceField: 'id' });
  return selected;
};

const beginEdit = async (stepId) => {
  const startedAt = Date.now();
  await clickControl(page, `[data-testid="construction-history-step-${stepId}"]`);
  await clickControl(page, `[data-testid="construction-edit-step-${stepId}"]`);
  const policy = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)), { __template0: (policy+':not(:disabled)') });
  await revealControl(policy);
  recordAction('edit-saved-step-to-policy-control', startedAt);
};

const applyProposal = async (expectedRows, name) => {
  const startedAt = Date.now();
  const requestStart = report.nativeRequests.length;
  const proposalId = latestReadyProposal?.response?.proposalId;
  assert(proposalId && latestReadyProposal.response.outputId === outputId,
    `${name}: Apply has no exact ready proposal for this output`);
  const proposalDOM = await inspectDOM(page, async args => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    return { proposalId: panel?.getAttribute('data-proposal-id') ?? null,
      receiptId: preview?.getAttribute('data-preview-receipt-id') ?? null,
      outputId: preview?.getAttribute('data-preview-output-id') ?? null };
  });
  assert.deepEqual(proposalDOM, { proposalId, receiptId: proposalId, outputId },
    `${name}: Apply control is not attached to the captured proposal receipt`);
  await clickControl(page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  const rows = await rendered(expectedRows, name);
  const applyEntry = await requestCapture.waitFor(entry => entry.path === `${base}/commands`
    && entry.method === 'POST' && entry.status === 200 && entry.startedAt >= startedAt
    && entry.body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL'
      && command.outputId === outputId && command.proposalId === proposalId),
  { fromIndex: requestStart, timeoutMs: actionTimeRemaining(startedAt) });
  const applyRequest = requestCapture.rawRequestBody(applyEntry);
  const applyResponse = requestCapture.rawResponseBody(applyEntry);
  const applyCommand = applyRequest?.commands?.filter(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL') ?? [];
  assert.equal(applyCommand.length, 1, `${name}: exactly one construction proposal must be applied`);
  assert.deepEqual(applyCommand[0], { type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId });
  assert.equal(applyRequest.snapshotToken, latestReadyProposal.request.snapshotToken);
  assert.equal(applyRequest.expectedDraftVersion, latestReadyProposal.response.draftVersion);
  assert.equal(applyRequest.expectedDraftDigest, latestReadyProposal.response.draftDigest);
  assert(applyResponse, `${name}: Apply command response was not retained`);
  assert.equal(applyResponse.draftVersion, applyRequest.expectedDraftVersion + 1,
    `${name}: Apply response did not advance the saved draft`);
  assert.equal(applyResponse.results?.filter(result => result.type === 'TABLE_CHANGED' && result.outputId === outputId).length, 1,
    `${name}: Apply response did not save the exact output`);
  builder = await api(base + '/builder');
  assert.equal(builder.draftVersion, applyResponse.draftVersion, `${name}: saved Builder version differs from Apply response`);
  assert.equal(builder.draftDigest, applyResponse.draftDigest, `${name}: saved Builder digest differs from Apply response`);
  const responseDocument = applyResponse.workspace?.documents?.find(document => document.output?.id === outputId);
  assert(responseDocument, `${name}: Apply response omitted the saved target document`);
  assert.deepEqual(responseDocument, documentForOutput(builder), `${name}: Builder state differs from the Apply response`);
  const savedPreview = await verifySavedPreview({ expectedRows, name, startedAt, requestStart, savedBuilder: builder });
  recordAction(name, startedAt, { rowCount: rows.length, proposalId, receiptId: savedPreview.receiptId });
  return rows;
};

const expectedErrorProposal = async (name, startedAt) => {
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'error'));
  const result = await inspectDOM(page, async args => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {
      status:panel?.dataset.proposalStatus,text:panel?.innerText,
      alert:panel?.querySelector('[data-testid="construction-proposal-error"]')?.innerText,
      retryVisible:Boolean(panel?.querySelector('[data-testid="construction-retry-proposal"]'))}; });
  assert.equal(result.status, 'error', `${name}: ERROR policy did not reject the preview`);
  assert.equal(result.retryVisible, false, `${name}: non-retryable validation error exposed Retry preview`);
  const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  const repair = await inspectDOM(page, async args => { const select=document.querySelector(args.__template0);return {
      disabled:select?.disabled,value:select?.value,
      options:[...(select?.options??[])].map(option=>({value:option.value,disabled:option.disabled}))}; }, { __template0: (policySelector) });
  assert.equal(repair.value, 'ERROR');
  assert.equal(repair.disabled, false, 'The no-match policy must remain editable after ERROR preview validation');
  for (const option of ['PRESERVE_PARENT', 'EXCLUDE']) {
    assert(repair.options.some((item) => item.value === option && !item.disabled), `${option} repair choice is unavailable`);
  }

  const request = await requestCapture.waitFor(item => item.path === `${base}/construction-proposals` && item.startedAt >= startedAt, { timeoutMs: Math.max(1, startedAt + 5000 - Date.now()) });
  assert.equal(request.status, 422, `${name}: expected the native validation response to be HTTP 422: ${JSON.stringify(request.response)}`);
  const response = request.response;
  const code = response?.error?.code ?? response?.code ?? response?.errorCode ?? response?.error?.errorCode;
  const diagnostic = `${JSON.stringify(response)} ${result.alert ?? result.text}`;
  assert.notEqual(code, 'INTERNAL_ERROR', `${name}: ERROR policy returned INTERNAL_ERROR: ${diagnostic}`);
  assert(/empty list or no matching related records/i.test(diagnostic),
    `${name}: expected the shared public empty-expansion guidance: ${diagnostic}`);
  cda.expectHttpFailure(request,
    'The independent zero-related-record witness intentionally exercises the ERROR empty-policy validation path.',
    { action: 'preview a related expansion with ERROR empty policy', policy: 'ERROR',
      sourceWitness: 'zero related Observation rows', errorCode: code });
  await cda.flushHttpDiagnostics({
    browserRequestId: request.browserRequestId,
    timeoutMs: Math.max(1, startedAt + 5000 - Date.now()),
  });
  await requestCapture.flush({ timeoutMs: Math.max(1, startedAt + 5000 - Date.now()) });
  const expectedFailures = cda.report.expectedHttpFailures?.filter(failure =>
    failure.browserRequestId === request.browserRequestId && failure.requestId === request.requestId
      && failure.method === request.method && failure.path === request.path && failure.status === request.status) ?? [];
  assert.equal(expectedFailures.length, 1, name + ': exact expected HTTP evidence must be retained for the native request');
  const expectedFailure = expectedFailures[0];
  assert.deepEqual(request.expectedHttpFailure, expectedFailure,
    name + ': captured request classification must match the fixture exact expected HTTP evidence');
  assert.equal(expectedFailure.proof?.action, 'preview a related expansion with ERROR empty policy');
  assert.equal(expectedFailure.proof?.policy, 'ERROR');
  assert.equal(expectedFailure.proof?.sourceWitness, 'zero related Observation rows');
  assert.equal(expectedFailure.proof?.errorCode, code);
  assert.equal(expectedFailure.console?.status, 422,
    name + ': the expected 422 console event must be retained with its exact request classification');
  assert.equal(expectedFailure.console?.location, uiOrigin + request.path);
  assert.equal(Number(expectedFailure.console.fixtureDiagnostic) + Number(expectedFailure.console.requestCaptureError), 1,
    name + ': the expected console event must have exactly one native capture owner');
  report.expectedPolicyError = {
    browserRequestId: request.browserRequestId, requestId: request.requestId, method: request.method, path: request.path,
    status: request.status, code, response, message: result.alert, expectedFailure,
  };
  report.expectedErrorWindow.active = false;
  recordCheck('usability', 'Scoped ERROR validation exposes enabled PRESERVE_PARENT and EXCLUDE repair choices',
    request.status === 422 && code !== 'INTERNAL_ERROR' && !result.retryVisible
      && repair.options.some(item => item.value === 'PRESERVE_PARENT' && !item.disabled)
      && repair.options.some(item => item.value === 'EXCLUDE' && !item.disabled),
    { requestId: request.requestId, status: request.status, errorCode: code, retryVisible: result.retryVisible });
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, status: request.status, code, message: result.alert, repairOptions: repair.options });
};


report.target ??= { ...cda.report.target };
report.nativeRequests = cda.report.nativeRequests;
report.errors = cda.report.errors;

  try {

const witnesses = sourceWitnesses();
report.oracle = {
  project,
  generation,
  relationship: 'Observation --subject_Patient--> Patient',
  witnesses,
  countByBucket: Object.fromEntries(witnesses.map((item) => [item.bucket, item.observations.length])),
};
const baselineRows = witnesses.map((item) => [item.patient.id]);
const allMatchingRows = witnesses.flatMap((item) => item.observations.length
  ? item.observations.map((observation) => [item.patient.id, observation.id])
  : [[item.patient.id, '—']]);
const existenceMatches = witnesses.flatMap((item) => item.observations
  .filter((observation) => typeof observation.id === 'string' && observation.id.trim().length > 0)
  .map((observation) => [item.patient.id, observation.id]));
const existsPreserveRows = witnesses.flatMap((item) => {
  const matches = item.observations.filter((observation) => typeof observation.id === 'string' && observation.id.trim().length > 0);
  return matches.length ? matches.map((observation) => [item.patient.id, observation.id]) : [[item.patient.id, '—']];
});
const existsExcludeRows = existenceMatches;
assert.deepEqual(existsPreserveRows, allMatchingRows,
  'CDA Observation IDs should exist on every independently enumerated related record');
assert.equal(existsPreserveRows.length, existenceMatches.length + 1,
  'PRESERVE_PARENT must retain exactly the zero-match source witness');
assert.equal(existsExcludeRows.length, existenceMatches.length,
  'EXCLUDE must remove the zero-match source witness while retaining all existing Observation IDs');
assert(existenceMatches.length > 0, 'The CDA source oracle must include at least one related Observation with an ID');
for (const [name, rows] of [['ALL_MATCHES', allMatchingRows], ['PRESERVE_PARENT', existsPreserveRows], ['EXCLUDE', existsExcludeRows]]) {
  assert(rows.length < 25, `${name} raw oracle reaches the native preview limit (${rows.length})`);
}
report.oracle.expected = {
  allMatchingRows,
  existsWithPreserveParent: existsPreserveRows,
  existsWithExclude: existsExcludeRows,
};
recordCheck('correctness', 'Independent scoped CDA oracle supplies exact zero, one, and many Patient-to-Observation witnesses',
  witnesses.length === 3 && new Set(witnesses.map(({ patient }) => patient._id)).size === 3
    && byBucketCountsAreExact(witnesses) && baselineRows.length === 3
    && existsPreserveRows.length === allMatchingRows.length && existsExcludeRows.length > 0,
  { project, generation, witnessCount: witnesses.length,
    relatedObservationCounts: Object.fromEntries(witnesses.map(item => [item.bucket, item.observations.length])),
    baselineRowCount: baselineRows.length, preserveRowCount: existsPreserveRows.length,
    excludeRowCount: existsExcludeRows.length });

await api(root, { name: explorer, title: 'Contributor EXISTS lifecycle QA' });
builder = await api(base + '/builder');
assert.equal(builder.catalog.generation, generation);
const patientNode = builder.catalog.nodes.find((node) => node.resourceType === 'Patient');
assert(patientNode, 'Patient source type is absent from the current catalog');
await command([{ type: 'CREATE_TABLE', title: 'Contributor EXISTS QA', rootNodeId: patientNode.nodeId }]);
outputId = builder.workspace.documents[0].output.id;
const patientIDField = builder.catalog.candidates.find((candidate) => candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id');
assert(patientIDField, 'Patient ID is absent from the current catalog');
await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: patientIDField.candidateId,
  projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient ID' }]);
const selection = await api(base.replace('/authoring/v2', '/selections'), {
  snapshotToken: builder.catalog.snapshotToken,
  idempotencyKey: explorer,
  source: { kind: 'resources', resources: { refs: witnesses.map(({ patient }) => ({
    project, generation, resourceType: 'Patient', id: patient.id,
  })) } },
});
assert.equal(selection.memberCount, 3, 'Selection must contain the three independently witnessed source rows');
const routes = await api(base + '/population-routes', {
  snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
});
const direct = routes.choices.find((choice) => choice.route.length === 0);
assert(direct, 'Selected Patient resources have no direct population route');
await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
original = structuredClone(documentForOutput(builder));
assert.equal(original.population.selectionRevisionId, selection.id);

page.on('request', request => {
  const path = new URL(request.url()).pathname;
  if (path.includes('cda-builder-full-qa-1790440983382')) report.errors.push({ kind: 'protected-explorer-request', path });
});
requestCapture = cda.captureRequests(`${root}/${explorer}`, {
  responsePaths: /construction-proposals|related-expand-choices|related-expand-contributors|commands|preview/,
  shouldReportHttpError: (requestPath, status, entry) => {
    const relatedExpand = entry.body?.candidateConstruction?.steps
      ?.find(step => step.operation?.kind === 'RELATED_EXPAND')?.operation?.relatedExpand;
    const expectedExistsValidation = requestPath === `${base}/construction-proposals` && status === 422 &&
      entry.method === 'POST' && entry.body?.outputId === outputId &&
      relatedExpand?.emptyPolicy === 'ERROR' &&
      relatedExpand?.contributorRule?.policy === 'ALL_MATCHES' &&
      relatedExpand?.contributorRule?.predicate?.operator === 'EXISTS' &&
      relatedExpand?.contributorSource?.resourceType === 'Observation' &&
      relatedExpand?.contributorSource?.path === 'id';
    return !expectedExistsValidation;
  },
});

const initialSourceRows = await open(baselineRows, 'source-selection-zero-one-many');
recordCheck('correctness', 'Initial source preview matches the exact three raw Patient witnesses',
  rowsMatch(initialSourceRows, baselineRows),
  { outputId, rowCount: baselineRows.length, selectionRevisionId: selection.id });
let startedAt = Date.now();
let panel = await startRelatedExpand();
const baselineContributor = await openContributorOptions(panel);
const baselineControls = await cda.inspect(async args => { const root=document.querySelector(args.__template0);
    return [...(root?.querySelectorAll('input[type="radio"]')??[])].map(input=>({
      label:input.parentElement?.innerText.trim(),checked:input.checked,disabled:input.disabled})); }, { __template0: (baselineContributor.options) });
assert(baselineControls.some((control) => control.label === 'All matching records' && control.checked),
  `Unfiltered related expansion must start in All matching records mode: ${JSON.stringify(baselineControls)}`);
assert(baselineControls.some((control) => control.label === 'Only records meeting a condition' && !control.checked),
  `Baseline must not silently enable a contributor condition: ${JSON.stringify(baselineControls)}`);
const allMatchingProposal = await proposal('all-matching-contributor-baseline-preview', startedAt, allMatchingRows);
recordCheck('correctness', 'Unfiltered ALL_MATCHES preview equals every exact raw related Observation row',
  rowsMatch(allMatchingProposal.rows, allMatchingRows),
  { outputId, rowCount: allMatchingRows.length, contributorPolicy: 'ALL_MATCHES' });
const baselineRequest = allMatchingProposal.proposalRequest;
const baselineStep = unfilteredRelatedExpandStep(baselineRequest);
report.allMatchingBaseline = { contributorRule: baselineStep.operation.relatedExpand.contributorRule, rows: allMatchingRows };

const beforeBaselineCancel = await api(base + '/builder');
startedAt = Date.now();
const routeChoiceCancellationStart = cda.report.expectedCancellations?.length ?? 0;
const routeChoiceCancellationAction = proposalCancelActionSelector;
const routeChoicesPath = base + '/related-expand-choices';
const routeChoiceSnapshot = snapshotPendingRelatedExpandChoices(cda.nativeRequests, {
  origin: uiOrigin,
  path: routeChoicesPath,
  outputId,
  draftVersion: beforeBaselineCancel.draftVersion,
  draftDigest: beforeBaselineCancel.draftDigest,
  stageId: 'source_projection',
  capturedAt: Date.now(),
});
const routeChoiceCancelActionStartedAt = Date.now();
await nativeClick(page, routeChoiceCancellationAction, {});
const routeChoiceCancelActionCompletedAt = Date.now();
await waitForDOM(page, args => Boolean(!document.querySelector(args.__template0)),
  { __template0: '[data-testid="construction-proposal-panel"]' }, 5000);
const relatedExpandEditorSelector = '[data-testid="construction-related-expand-editor"]';
await waitForDOM(page, args => Boolean(!document.querySelector(args.__template0)),
  { __template0: relatedExpandEditorSelector }, 5000);
const routeChoiceEditorClosedAt = Date.now();
const routeChoiceCancellationReason = 'Explicit baseline Cancel closed the RelatedExpand editor and retired only its exact pending route-choice page.';
const routeChoiceCancelledBrowserRequestIDs = classifyPendingRelatedExpandChoicesAfterProposalCancel({
  cda,
  snapshot: routeChoiceSnapshot,
  action: {
    label: routeChoiceCancellationAction,
    startedAt: routeChoiceCancelActionStartedAt,
    completedAt: routeChoiceCancelActionCompletedAt,
  },
  editorClosed: true,
  editorClosedAt: routeChoiceEditorClosedAt,
  reason: routeChoiceCancellationReason,
});
assert(routeChoiceCancelledBrowserRequestIDs.length <= 1,
  'Cancel may retire at most one active related-choice page because route pagination awaits one page at a time');
const cancelledBaselineRows = await rendered(baselineRows, 'cancel-all-matching-preview');
const routeChoiceCancellations = (cda.report.expectedCancellations ?? []).slice(routeChoiceCancellationStart);
assert(routeChoiceCancellations.length <= 1,
  'Cancel may retire at most one active related-choice page because route pagination awaits one page at a time');
assert.deepEqual(routeChoiceCancellations.map(item => item.browserRequestId).sort(),
  [...routeChoiceCancelledBrowserRequestIDs].sort(),
  'Only exact requests from the pre-Cancel pending snapshot may enter the expected-cancellation ledger');
const verifyRouteChoiceCancellation = cancellation => {
  const requests = report.nativeRequests.filter(request => request.browserRequestId === cancellation.browserRequestId);
  assert.equal(requests.length, 1, 'Cancellation must identify one captured native request');
  const [retiredRequest] = requests;
  const body = retiredRequest.body;
  assert.deepEqual([retiredRequest.path, retiredRequest.origin, retiredRequest.method], [routeChoicesPath, uiOrigin, 'POST']);
  assert.equal(retiredRequest.requestId, cancellation.requestId);
  assert.match(retiredRequest.requestId, /^related-expand-choices-/);
  assert.deepEqual([body?.outputId, body?.stageId, body?.anchorColumnId, body?.targetResourceType, body?.limit], [outputId, 'source_projection', '_key', 'Observation', 50]);
  assert(body?.snapshotToken && Number.isInteger(body.expectedDraftVersion) && body.expectedDraftDigest);
  assert.deepEqual([body.snapshotToken, body.expectedDraftVersion, body.expectedDraftDigest], [
    beforeBaselineCancel.catalog.snapshotToken,
    beforeBaselineCancel.draftVersion,
    beforeBaselineCancel.draftDigest,
  ], 'The retired route page must use the exact baseline snapshot and saved draft CAS');
  assert(typeof body.cursor === 'string' && body.cursor.length > 0);
  assert.equal(retiredRequest.failure, 'net::ERR_ABORTED');
  assert.deepEqual([cancellation.method, new URL(cancellation.url).origin, new URL(cancellation.url).pathname], ['POST', uiOrigin, routeChoicesPath]);
  assert.equal(cancellation.proof?.action, routeChoiceCancellationAction);
  assert.deepEqual(cancellation.proof?.identity, {
    origin: uiOrigin,
    path: routeChoicesPath,
    outputId,
    draftVersion: beforeBaselineCancel.draftVersion,
    draftDigest: beforeBaselineCancel.draftDigest,
    stageId: 'source_projection',
  });
  assert.equal(cancellation.proof?.preCancelSnapshotAt, routeChoiceSnapshot.capturedAt);
  assert.deepEqual(cancellation.proof?.actionWindow, {
    startedAt: routeChoiceCancelActionStartedAt,
    completedAt: routeChoiceCancelActionCompletedAt,
    editorClosedAt: routeChoiceEditorClosedAt,
  });
  assert.equal(cancellation.proof?.editorClosed, true);
  const sameQuery = request => ['outputId', 'stageId', 'anchorColumnId', 'targetResourceType', 'snapshotToken', 'expectedDraftVersion', 'expectedDraftDigest'].every(key => request.body?.[key] === body[key]);
  const previousPage = report.nativeRequests.filter(request => request.path === routeChoicesPath && request.method === 'POST'
    && request.startedAt < retiredRequest.startedAt && request.status === 200 && sameQuery(request)
    && typeof request.body?.cursor === 'string' && request.body.cursor.length > 0).at(-1);
  assert(previousPage, 'Retirement must follow a successful page for the same output, stage, anchor, target, draft, and snapshot');
  assert(previousPage.body.limit === body.limit && previousPage.body.cursor !== body.cursor);
  const nativeNetwork = (cda.report.network ?? []).filter(entry => entry.kind === 'network' && entry.browserRequestId === retiredRequest.browserRequestId);
  assert.equal(nativeNetwork.length, 1, 'The exact route-choice abort must stay in native browser network diagnostics');
  assert.deepEqual([nativeNetwork[0].errorText, nativeNetwork[0].expected], ['net::ERR_ABORTED', true]);
  assert.equal(nativeNetwork[0].expectedCancellation?.browserRequestId, retiredRequest.browserRequestId,
    'The native network ledger must retain the exact helper-classified request identity');
  const requestErrors = report.errors.filter(error => error.kind === 'network' && error.browserRequestId === retiredRequest.browserRequestId);
  assert(requestErrors.length >= 1 && requestErrors.length <= 2, 'Retain bounded diagnostics for the exact route-choice abort');
  for (const error of requestErrors) {
    assert.equal(error.error ?? error.failure, 'net::ERR_ABORTED');
    error.expected = true;
    error.expectedCancellation = cancellation;
  }
  return {
    request: { requestId: retiredRequest.requestId, browserRequestId: retiredRequest.browserRequestId,
      path: retiredRequest.path, method: retiredRequest.method, outputId, stageId: body.stageId,
      anchorColumnId: body.anchorColumnId, targetResourceType: body.targetResourceType,
      snapshotToken: body.snapshotToken, expectedDraftVersion: body.expectedDraftVersion,
      expectedDraftDigest: body.expectedDraftDigest, limit: body.limit, cursor: body.cursor, error: retiredRequest.failure },
    previousPage: { requestId: previousPage.requestId, browserRequestId: previousPage.browserRequestId,
      status: previousPage.status, cursor: previousPage.body.cursor },
    action: cancellation.proof.action,
    actionWindow: cancellation.proof.actionWindow,
    preCancelSnapshotAt: cancellation.proof.preCancelSnapshotAt,
    editorClosed: cancellation.proof.editorClosed,
    reason: cancellation.reason,
  };
};
const routeChoiceCancellationEvidence = routeChoiceCancellations.map(verifyRouteChoiceCancellation);
if (routeChoiceCancellationEvidence.length) report.routeChoiceCancellationOnCancel = routeChoiceCancellationEvidence[0];
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeBaselineCancel.workspace, 'Cancel must leave the saved workspace unchanged');
assert.deepEqual(documentForOutput(builder), original, 'Cancel must preserve the exact source table');
recordCheck('persistence', 'Cancel preserves the exact source document, population, and rendered rows',
  isDeepStrictEqual(builder.workspace, beforeBaselineCancel.workspace)
    && isDeepStrictEqual(documentForOutput(builder), original) && rowsMatch(cancelledBaselineRows, baselineRows),
  { outputId, rowCount: baselineRows.length, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
recordAction('cancel-all-matching-preview', startedAt, { rowCount: baselineRows.length });
report.cases.push({ name: 'all-matching-cancel-preserves-source', workspaceUnchanged: true });

panel = await startRelatedExpand();
const selectedContributor = await chooseContributorIDExists(panel, 'configure-exists-contributor-rule');
recordCheck('usability', 'Native Contributor controls select the exact Observation.id EXISTS condition',
  selectedContributor.condition === 'EXISTS' && selectedContributor.field?.includes('id'),
  { condition: selectedContributor.condition, field: selectedContributor.field });
const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
startedAt = Date.now();
report.expectedErrorWindow = { active: true, startedAt, policy: 'ERROR' };
await nativeSelect(page, policySelector, 'ERROR');
await expectedErrorProposal('exists-error-policy-repair', startedAt);

startedAt = Date.now();
await nativeSelect(page, policySelector, 'PRESERVE_PARENT');
const preserveProposal = await proposal('exists-preserve-parent-repair-preview', startedAt, existsPreserveRows);
const preserveApplyRows = await applyProposal(existsPreserveRows, 'apply-exists-preserve-parent-to-render');
let relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assert(relatedStep, 'Applied EXISTS Contributor rule is missing from saved construction');
const sourceProjection = relatedStep.outputs.find((column) => column.name === original.columns[0].column);
assert(sourceProjection?.id, 'Applied related-expand step omitted the compiler-owned source projection identity');
const canonicalSourceColumnId = sourceProjection.id;
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'initial EXISTS Apply');
report.sourceColumnIdentity = {
  sourceColumnName: sourceProjection.name,
  compilerCanonicalSourceColumnId: canonicalSourceColumnId,
};
const relatedStepId = relatedStep.id;
const savedRelated = relatedStep.operation.relatedExpand;
assert.equal(savedRelated.emptyPolicy, 'PRESERVE_PARENT');
assert.equal(savedRelated.contributorSource.resourceType, 'Observation');
assert.deepEqual(savedRelated.contributorRule, {
  policy: 'ALL_MATCHES', predicate: { candidateId: savedRelated.contributorSource.candidateId, operator: 'EXISTS' },
});
assert.equal(savedRelated.contributorSource.path, 'id');
assert(!Object.hasOwn(savedRelated.contributorRule.predicate, 'value'), 'EXISTS must not carry an exact value');
assert.equal(savedRelated.contributorSource.candidateId, savedRelated.contributorRule.predicate.candidateId);
report.appliedExistsRule = {
  contributorRule: savedRelated.contributorRule,
  contributorSource: savedRelated.contributorSource,
  emptyPolicy: savedRelated.emptyPolicy,
};
recordCheck('correctness', 'PRESERVE_PARENT Apply saves the exact EXISTS rule and raw-oracle rows',
  savedRelated.emptyPolicy === 'PRESERVE_PARENT' && savedRelated.contributorRule.predicate.operator === 'EXISTS'
    && savedRelated.contributorSource.resourceType === 'Observation'
    && savedRelated.contributorSource.candidateId === savedRelated.contributorRule.predicate.candidateId
    && savedRelated.contributorSource.path === 'id' && rowsMatch(preserveProposal.rows, existsPreserveRows)
    && rowsMatch(preserveApplyRows, existsPreserveRows),
  { outputId, stepId: relatedStep.id, emptyPolicy: savedRelated.emptyPolicy,
    rowCount: existsPreserveRows.length, sourcePath: savedRelated.contributorSource.path });
let beforeEdit;
const preserveReloadRows = await open(existsPreserveRows, 'reload-exists-preserve-parent', async (rows, savedBuilder) => {
  beforeEdit = savedBuilder;
  assertStableSourceProjection(beforeEdit, original, canonicalSourceColumnId, 'reload after EXISTS Apply');
  relatedStep = documentForOutput(beforeEdit).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
  assert(relatedStep, 'PRESERVE_PARENT reload lost the saved EXISTS Contributor step');
  assert.equal(relatedStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
  assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.operator, 'EXISTS');
  assert.equal(relatedStep.id, relatedStepId);
  assert.equal(relatedStep.operation.relatedExpand.contributorRule.policy, 'ALL_MATCHES');
  assert.equal(relatedStep.operation.relatedExpand.contributorSource.resourceType, 'Observation');
  assert.equal(relatedStep.operation.relatedExpand.contributorSource.candidateId, savedRelated.contributorSource.candidateId);
  recordCheck('persistence', 'PRESERVE_PARENT rows, EXISTS policy, and source binding survive reload',
    rowsMatch(rows, existsPreserveRows)
      && relatedStep.id === relatedStepId
      && relatedStep.operation.relatedExpand.emptyPolicy === 'PRESERVE_PARENT'
      && relatedStep.operation.relatedExpand.contributorRule.policy === 'ALL_MATCHES'
      && relatedStep.operation.relatedExpand.contributorRule.predicate.operator === 'EXISTS'
      && relatedStep.operation.relatedExpand.contributorRule.predicate.candidateId === savedRelated.contributorSource.candidateId
      && relatedStep.operation.relatedExpand.contributorSource.resourceType === 'Observation'
      && relatedStep.operation.relatedExpand.contributorSource.path === 'id'
      && relatedStep.operation.relatedExpand.contributorSource.candidateId === savedRelated.contributorSource.candidateId,
    { outputId, stepId: relatedStep.id, rowCount: existsPreserveRows.length, emptyPolicy: 'PRESERVE_PARENT' });
});
relatedStep = documentForOutput(beforeEdit).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
await beginEdit(relatedStep.id);
assert.equal(await cda.inspect(async args => { return document.querySelector(args.__template0)?.value; }, { __template0: (policySelector) }), 'PRESERVE_PARENT');
startedAt = Date.now();
await nativeSelect(page, policySelector, 'EXCLUDE');
const excludeProposal = await proposal('edit-exists-policy-exclude-preview', startedAt, existsExcludeRows);
const excludeApplyRows = await applyProposal(existsExcludeRows, 'apply-exists-exclude-to-render');
relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'edited EXISTS EXCLUDE Apply');
assert.equal(relatedStep.operation.relatedExpand.emptyPolicy, 'EXCLUDE');
assert.equal(relatedStep.id, relatedStepId);
assert.equal(relatedStep.operation.relatedExpand.contributorRule.policy, 'ALL_MATCHES');
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.operator, 'EXISTS');
assert.equal(relatedStep.operation.relatedExpand.contributorSource.resourceType, 'Observation');
assert.equal(relatedStep.operation.relatedExpand.contributorSource.candidateId, savedRelated.contributorSource.candidateId);
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.candidateId, savedRelated.contributorSource.candidateId);
assert(!Object.hasOwn(relatedStep.operation.relatedExpand.contributorRule.predicate, 'value'));
assert.equal(relatedStep.operation.relatedExpand.contributorSource.path, 'id');
recordCheck('correctness', 'EXCLUDE edit preserves the EXISTS binding and renders exactly matching raw rows',
  relatedStep.operation.relatedExpand.emptyPolicy === 'EXCLUDE'
    && relatedStep.id === relatedStepId
    && relatedStep.operation.relatedExpand.contributorRule.policy === 'ALL_MATCHES'
    && relatedStep.operation.relatedExpand.contributorRule.predicate.operator === 'EXISTS'
    && relatedStep.operation.relatedExpand.contributorSource.resourceType === 'Observation'
    && relatedStep.operation.relatedExpand.contributorSource.candidateId === savedRelated.contributorSource.candidateId
    && relatedStep.operation.relatedExpand.contributorSource.path === 'id'
    && rowsMatch(excludeProposal.rows, existsExcludeRows) && rowsMatch(excludeApplyRows, existsExcludeRows),
  { outputId, stepId: relatedStep.id, rowCount: existsExcludeRows.length, emptyPolicy: 'EXCLUDE' });
let beforeRemoval;
const excludeReloadRows = await open(existsExcludeRows, 'reload-edited-exists-exclude', async (rows, savedBuilder) => {
  beforeRemoval = savedBuilder;
  assertStableSourceProjection(beforeRemoval, original, canonicalSourceColumnId, 'reload after edited EXISTS EXCLUDE policy');
  relatedStep = documentForOutput(beforeRemoval).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
  assert(relatedStep, 'EXCLUDE reload lost the saved EXISTS Contributor step');
  assert.equal(relatedStep.operation.relatedExpand.emptyPolicy, 'EXCLUDE');
  assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.operator, 'EXISTS');
  assert.equal(relatedStep.id, relatedStepId);
  assert.equal(relatedStep.operation.relatedExpand.contributorRule.policy, 'ALL_MATCHES');
  assert.equal(relatedStep.operation.relatedExpand.contributorSource.resourceType, 'Observation');
  assert.equal(relatedStep.operation.relatedExpand.contributorSource.candidateId, savedRelated.contributorSource.candidateId);
  assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.candidateId, savedRelated.contributorSource.candidateId);
  recordCheck('persistence', 'EXCLUDE rows, policy, and source binding survive reload',
    rowsMatch(rows, existsExcludeRows)
      && relatedStep.id === relatedStepId
      && relatedStep.operation.relatedExpand.emptyPolicy === 'EXCLUDE'
      && relatedStep.operation.relatedExpand.contributorRule.policy === 'ALL_MATCHES'
      && relatedStep.operation.relatedExpand.contributorRule.predicate.operator === 'EXISTS'
      && relatedStep.operation.relatedExpand.contributorSource.resourceType === 'Observation'
      && relatedStep.operation.relatedExpand.contributorSource.candidateId === savedRelated.contributorSource.candidateId
      && relatedStep.operation.relatedExpand.contributorRule.predicate.candidateId === savedRelated.contributorSource.candidateId
      && relatedStep.operation.relatedExpand.contributorSource.path === 'id'
      && !Object.hasOwn(relatedStep.operation.relatedExpand.contributorRule.predicate, 'value'),
    { outputId, stepId: relatedStep.id, rowCount: existsExcludeRows.length, emptyPolicy: 'EXCLUDE' });
});
relatedStep = documentForOutput(beforeRemoval).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
const remove = async () => {
  await nativeClick(page, `[data-testid="construction-history-step-${relatedStep.id}"]`, {});
  startedAt = Date.now();
  await nativeClick(page, `[data-testid="construction-remove-step-${relatedStep.id}"]`, {});
  await proposal('remove-exists-contributor-preview', startedAt, baselineRows);
};
await remove();
startedAt = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), {}, 5000);
const cancelledRemovalRows = await rendered(existsExcludeRows, 'cancel-exists-remove-to-render');
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeRemoval.workspace, 'Cancel removal must preserve the authored EXISTS Contributor rule');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'cancel EXISTS remove');
report.cases.push({ name: 'exists-remove-cancel-preserves-contributor-rule', workspaceUnchanged: true });
recordCheck('persistence', 'Cancel removal preserves saved EXCLUDE rows and the exact Builder workspace',
  isDeepStrictEqual(builder.workspace, beforeRemoval.workspace) && rowsMatch(cancelledRemovalRows, existsExcludeRows),
  { outputId, rowCount: existsExcludeRows.length, stepId: relatedStep.id });
recordAction('cancel-exists-remove-to-render', startedAt, { rowCount: existsExcludeRows.length });
await remove();
const removedRows = await applyProposal(baselineRows, 'apply-exists-remove-to-render');
const restored = documentForOutput(builder);
assert.deepEqual(restored.construction?.steps ?? [], original.construction?.steps ?? [],
  'Removing EXISTS Contributor rules must restore the exact authored source construction');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'EXISTS remove Apply');
assert.deepEqual(restored.rows, original.rows, 'Removing EXISTS Contributor rules must restore the exact row definition');
assert.deepEqual(restored.population, original.population, 'Removing EXISTS Contributor rules must restore the exact selected population');
recordCheck('correctness', 'Removal Apply restores exact source construction, columns, rows, and population',
  rowsMatch(removedRows, baselineRows)
    && isDeepStrictEqual(restored.construction?.steps ?? [], original.construction?.steps ?? [])
    && isDeepStrictEqual(withoutStageIdentity(restored.columns), withoutStageIdentity(original.columns))
    && isDeepStrictEqual(restored.rows, original.rows) && isDeepStrictEqual(restored.population, original.population),
  { outputId, sourceColumnCount: restored.columns.length, constructionStepCount: restored.construction?.steps?.length ?? 0 });
const restoredSourceRows = await open(baselineRows, 'reload-restored-source-table', async (rows, savedBuilder) => {
  builder = savedBuilder;
  assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'reload after EXISTS Contributor removal');
  const restoredDocument = documentForOutput(builder);
  assert.deepEqual(restoredDocument.construction?.steps ?? [], original.construction?.steps ?? [],
    'Restored source construction must survive reload exactly');
  assert.deepEqual(withoutStageIdentity(restoredDocument.columns), withoutStageIdentity(original.columns),
    'Restored source schema must survive reload exactly');
  assert.deepEqual(restoredDocument.rows, original.rows, 'Restored source row definition must survive reload exactly');
  assert.deepEqual(restoredDocument.population, original.population, 'Restored source population must survive reload exactly');
  recordCheck('persistence', 'Restored source schema, population, and exact baseline rows survive reload',
    rowsMatch(rows, baselineRows)
      && isDeepStrictEqual(restoredDocument.construction?.steps ?? [], original.construction?.steps ?? [])
      && isDeepStrictEqual(withoutStageIdentity(restoredDocument.columns), withoutStageIdentity(original.columns))
      && isDeepStrictEqual(restoredDocument.rows, original.rows)
      && isDeepStrictEqual(restoredDocument.population, original.population),
    { outputId, rowCount: baselineRows.length, sourceColumnCount: restoredDocument.columns.length });
});
await Promise.all([
  requestCapture.flush({ timeoutMs: 5_000 }),
  cda.flushHttpDiagnostics({ timeoutMs: 5_000 }),
]);
assert.deepEqual(report.errors.filter((failure) => !failure.expected), [], 'Unexpected browser errors were reported');
const expectedFailureEvidence = cda.report.expectedHttpFailures ?? [];
assert.equal(expectedFailureEvidence.length, 1, 'Exactly one native HTTP response may carry expected-validation evidence');
assert.deepEqual(expectedFailureEvidence[0], report.expectedPolicyError.expectedFailure,
  'The expected HTTP ledger must retain the exact request and console evidence verified during the workflow');
assert.equal(expectedFailureEvidence[0].browserRequestId, report.expectedPolicyError.browserRequestId);
assert.equal(expectedFailureEvidence[0].requestId, report.expectedPolicyError.requestId);
assert.equal(expectedFailureEvidence[0].method, 'POST');
assert.equal(expectedFailureEvidence[0].path, base + '/construction-proposals');
assert.equal(expectedFailureEvidence[0].status, 422);
const expectedHTTP = cda.diagnostics.httpFailures.filter(failure =>
  failure.browserRequestId === report.expectedPolicyError.browserRequestId);
assert.equal(expectedHTTP.length, 1, 'The exact expected construction proposal request must remain in Playwright HTTP diagnostics');
assert.equal(expectedHTTP[0].status, report.expectedPolicyError.status);
assert.equal(expectedHTTP[0].url, uiOrigin + report.expectedPolicyError.path);
assert.deepEqual(cda.diagnostics.httpFailures.filter(failure => !expectedHTTP.includes(failure)), [],
  'Unexpected app-origin HTTP failures were reported');
assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected page errors were reported');
assert.deepEqual(cda.diagnostics.console, [], 'Unexpected console errors were reported');
assert.deepEqual(cda.diagnostics.networkFailures.filter(failure => !failure.expectedCancellation), [], 'Unexpected network failures were reported');
const requestBoundHttpErrors = report.errors.filter(failure =>
  failure.kind === 'http' && failure.browserRequestId === report.expectedPolicyError.browserRequestId);
assert(requestBoundHttpErrors.length <= 1,
  'The exact expected ERROR-policy response may produce at most one generic browser HTTP diagnostic');
for (const failure of requestBoundHttpErrors) {
  assert.equal(failure.expected, true, 'Any generic HTTP diagnostic for the exact expected request must retain its classification');
  assert.deepEqual(failure.expectedHttpFailure, report.expectedPolicyError.expectedFailure);
}
assert.equal(report.errors.filter(failure => failure.kind === 'http').length, requestBoundHttpErrors.length,
  'Unmatched HTTP diagnostics must remain fatal');
report.expectedErrorWindow.active = false;
recordCheck('correctness', 'Only the scoped ERROR validation and proven cancellation are expected; unrelated diagnostics remain fatal',
  report.errors.every(failure => failure.expected === true || failure.expectedCancellation === true)
    && cda.diagnostics.pageErrors.length === 0 && cda.diagnostics.console.length === 0
    && cda.diagnostics.networkFailures.every(failure => failure.expectedCancellation),
  { expectedHttpFailures: expectedFailureEvidence.length,
    expectedCancellations: cda.report.expectedCancellations?.length ?? 0,
    unexpectedErrors: report.errors.filter(failure => !failure.expected && !failure.expectedCancellation).length });
const timedActions = report.cases.filter(item => Number.isFinite(item.durationMs));
const maximumActionMs = Math.max(...timedActions.map(item => item.durationMs));
recordCheck('performance', 'All Contributor EXISTS lifecycle actions finish within five seconds',
  timedActions.length > 0 && timedActions.every(item => item.durationMs <= 5000),
  { actionCount: timedActions.length, maxActionMs: maximumActionMs, budgetMs: 5000 });
report.status = 'passed';
  } finally {
    let requestFlushError;
    try { await requestCapture?.flush({ timeoutMs: 5000 }); } catch (error) {
      report.status = 'failed';
      report.requestFlushError = String(error);
      requestFlushError = error;
    }
    cda.includeBrowserDiagnostics();
    report.finished = new Date().toISOString();
    await cda.attachReport(`standalone-${cda.caseName}-domain.json`, {
      ...cda.report,
      domain: report,
    });
    if (requestFlushError) throw requestFlushError;
  }
}
