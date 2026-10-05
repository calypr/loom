import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from './native-cda-workflow-tools.mjs';

export async function contributorExistsWorkflow({ page, cda, caseOptions = {} }) {
  const { click, fill, selectOption, navigate, inspect, clickControl, fillControl, selectControl,
    nativeClick, nativeFill, nativeSelect, navigatePage, inspectDOM, browserEval, inspectPage,
    waitForDOM, waitForBrowser, captureCDARequests, captureRequests, waitForCapturedResponse,
    performAction, requireUnique } = createNativeCdaWorkflowTools({ page, cda });
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
let outputId;
let requestCapture;

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
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango did not return JSON: ${result.stdout.slice(-500)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
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
          LIMIT 2
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
    findPatient('many', 'sampleCount == 2'),
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
  assert(byBucket.many.observations.length >= 2);
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
const assertRows = (actual, expected, name) => {
  assert(expected.length <= 25, `${name}: source witnesses exceed the complete native preview limit (${expected.length})`);
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
      rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}; });
  assert.equal(result.status, 'ready', `${name}: ${result.text}`);
  assertRows(result.rows, expectedRows, name);
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: expectedRows.length, preview: result.rows });
  return result;
};

const open = async (expectedRows, name) => {
  const startedAt = Date.now();
  await navigatePage(page, pageURL);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-table-'+args.__template0+'"]')), { __template0: (outputId) });
  await clickControl(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  const rows = await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: rows.length });
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
  const contributorRequestPath = `${base}/related-expand-contributors`;
  const contributorRequestStart = report.nativeRequests.length;
  const cancellationAction = 'search contributor fields for id';
  await cda.withExpectedCancellations({
    origin: uiOrigin,
    method: 'POST',
    paths: [contributorRequestPath],
    requestIdPrefixes: ['cda-request-'],
    reason: 'The initial unfiltered contributor lookup is superseded by the explicit id search.',
    proof: { priorQuery: null, replacementQuery: 'id' },
    actionLabel: cancellationAction,
  }, async () => {
    await revealControl(onlyRecords, 'Only records meeting a condition');
    await clickControl(page, onlyRecords, { includes: 'Only records meeting a condition' });
    await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' input[placeholder="Search field name or path"]')), { __template0: (options) });
    await revealControl(search);
    await clickControl(page, search);
    await fillControl(page, search, 'id');
    const filteredRequest = await requestCapture.waitFor(request => request.path === contributorRequestPath
      && request.body?.query === 'id' && request.status === 200
      && request.response?.choices?.some(choice => choice.source?.path === 'id' && choice.source?.resourceType === 'Observation'),
    { fromIndex: contributorRequestStart, timeoutMs: 5000 });
    assert.equal(filteredRequest.status, 200, 'The superseding Observation id field search must return successfully');
    await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' [role="group"][aria-label="Fields for related-record condition"] button')), { __template0: (options) });
  });
  const cancellations = cda.report.expectedCancellations?.filter(item => item.proof?.scopeAction === cancellationAction) ?? [];
  assert(cancellations.length <= 1, 'Contributor field search may classify at most one superseded initial lookup');
  const cancellation = cancellations[0];
  if (cancellation) {
    const cancelledRequest = report.nativeRequests.find(request => request.browserRequestId === cancellation.browserRequestId);
    assert(cancelledRequest, 'Expected contributor search cancellation must point to an exact captured browser request');
    assert.equal(cancelledRequest.path, contributorRequestPath);
    assert.equal(cancelledRequest.method, 'POST');
    assert([undefined, null, ''].includes(cancelledRequest.body?.query),
      'The cancelled contributor request must be the initial unfiltered lookup');
    const cancelledError = report.errors.find(error => error.kind === 'network'
      && error.browserRequestId === cancellation.browserRequestId);
    assert(cancelledError, 'The exact cancelled contributor request failure must remain in browser diagnostics');
    assert.equal(cancelledError.error, 'net::ERR_ABORTED');
    cancelledError.expected = true;
    cancelledError.expectedCancellation = cancellation;
    report.contributorSearchCancellation = {
      request: { browserRequestId: cancelledRequest.browserRequestId, path: cancelledRequest.path,
        method: cancelledRequest.method, query: cancelledRequest.body?.query ?? null, error: cancelledError.error },
      replacement: { query: 'id', status: 200, field: 'Observation.id' },
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
  await clickControl(page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: expectedRows.length });
  builder = await api(base + '/builder');
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
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, status: request.status, code, message: result.alert, repairOptions: repair.options });
  await requestCapture.flush();
  const expectedError = report.errors.findLast((failure) => failure.kind === 'http' && failure.path === `${base}/construction-proposals` && failure.startedAt >= startedAt && failure.status === request.status && failure.requestId === request.requestId);
  assert(expectedError, `${name}: expected failed response was not retained with request identity`);
  expectedError.expected = true;
  report.expectedPolicyError = { status: request.status, code, response, message: result.alert, requestId: request.requestId };
  report.expectedErrorWindow.active = false;
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
report.oracle.expected = {
  allMatchingRows,
  existsWithPreserveParent: existsPreserveRows,
  existsWithExclude: existsExcludeRows,
};

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
const original = structuredClone(documentForOutput(builder));
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

await open(baselineRows, 'source-selection-zero-one-many');
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
await proposal('all-matching-contributor-baseline-preview', startedAt, allMatchingRows);
const baselineRequest = report.nativeRequests.findLast((request) => request.path === `${base}/construction-proposals`
  && request.startedAt >= startedAt);
assert(baselineRequest?.body?.candidateConstruction?.steps, 'Native ALL_MATCHES baseline proposal was not captured');
const baselineStep = baselineRequest.body.candidateConstruction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assert.deepEqual(baselineStep?.operation.relatedExpand.contributorRule, { policy: 'ALL_MATCHES' },
  'All matching baseline must omit the contributor predicate');
report.allMatchingBaseline = { contributorRule: baselineStep.operation.relatedExpand.contributorRule, rows: allMatchingRows };

const beforeBaselineCancel = await api(base + '/builder');
startedAt = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), {}, 5000);
await rendered(baselineRows, 'cancel-all-matching-preview');
recordAction('cancel-all-matching-preview', startedAt, { rowCount: baselineRows.length });
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeBaselineCancel.workspace, 'Cancel must leave the saved workspace unchanged');
assert.deepEqual(documentForOutput(builder), original, 'Cancel must preserve the exact source table');
report.cases.push({ name: 'all-matching-cancel-preserves-source', workspaceUnchanged: true });

panel = await startRelatedExpand();
await chooseContributorIDExists(panel, 'configure-exists-contributor-rule');
const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
startedAt = Date.now();
report.expectedErrorWindow = { active: true, startedAt, policy: 'ERROR' };
await nativeSelect(page, policySelector, 'ERROR');
await expectedErrorProposal('exists-error-policy-repair', startedAt);

startedAt = Date.now();
await nativeSelect(page, policySelector, 'PRESERVE_PARENT');
await proposal('exists-preserve-parent-repair-preview', startedAt, existsPreserveRows);
await applyProposal(existsPreserveRows, 'apply-exists-preserve-parent-to-render');
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
const savedRelated = relatedStep.operation.relatedExpand;
assert.equal(savedRelated.emptyPolicy, 'PRESERVE_PARENT');
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
await open(existsPreserveRows, 'reload-exists-preserve-parent');

const beforeEdit = await api(base + '/builder');
assertStableSourceProjection(beforeEdit, original, canonicalSourceColumnId, 'reload after EXISTS Apply');
relatedStep = documentForOutput(beforeEdit).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
await beginEdit(relatedStep.id);
assert.equal(await cda.inspect(async args => { return document.querySelector(args.__template0)?.value; }, { __template0: (policySelector) }), 'PRESERVE_PARENT');
startedAt = Date.now();
await nativeSelect(page, policySelector, 'EXCLUDE');
await proposal('edit-exists-policy-exclude-preview', startedAt, existsExcludeRows);
await applyProposal(existsExcludeRows, 'apply-exists-exclude-to-render');
relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'edited EXISTS EXCLUDE Apply');
assert.equal(relatedStep.operation.relatedExpand.emptyPolicy, 'EXCLUDE');
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.operator, 'EXISTS');
assert(!Object.hasOwn(relatedStep.operation.relatedExpand.contributorRule.predicate, 'value'));
assert.equal(relatedStep.operation.relatedExpand.contributorSource.path, 'id');
await open(existsExcludeRows, 'reload-edited-exists-exclude');

const beforeRemoval = await api(base + '/builder');
assertStableSourceProjection(beforeRemoval, original, canonicalSourceColumnId, 'reload after edited EXISTS EXCLUDE policy');
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
await rendered(existsExcludeRows, 'cancel-exists-remove-to-render');
recordAction('cancel-exists-remove-to-render', startedAt, { rowCount: existsExcludeRows.length });
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeRemoval.workspace, 'Cancel removal must preserve the authored EXISTS Contributor rule');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'cancel EXISTS remove');
report.cases.push({ name: 'exists-remove-cancel-preserves-contributor-rule', workspaceUnchanged: true });
await remove();
await applyProposal(baselineRows, 'apply-exists-remove-to-render');
const restored = documentForOutput(builder);
assert.deepEqual(restored.construction?.steps ?? [], original.construction?.steps ?? [],
  'Removing EXISTS Contributor rules must restore the exact authored source construction');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'EXISTS remove Apply');
assert.deepEqual(restored.rows, original.rows, 'Removing EXISTS Contributor rules must restore the exact row definition');
assert.deepEqual(restored.population, original.population, 'Removing EXISTS Contributor rules must restore the exact selected population');
await open(baselineRows, 'reload-restored-source-table');
builder = await api(base + '/builder');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'reload after EXISTS Contributor removal');
await requestCapture.flush();
assert.deepEqual(report.errors.filter((failure) => !failure.expected), [], 'Unexpected browser errors were reported');
const expectedHTTP = cda.diagnostics.httpFailures.filter((failure) => failure.status === report.expectedPolicyError.status && failure.url.endsWith(`${base}/construction-proposals`));
assert.equal(expectedHTTP.length, 1, 'Exactly the expected construction proposal HTTP failure must be captured by Playwright diagnostics');
assert.deepEqual(cda.diagnostics.httpFailures.filter(failure => !expectedHTTP.includes(failure)), [], 'Unexpected app-origin HTTP failures were reported');
assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected page errors were reported');
assert.deepEqual(cda.diagnostics.console, [], 'Unexpected console errors were reported');
assert.deepEqual(cda.diagnostics.networkFailures.filter(failure => !failure.expectedCancellation), [], 'Unexpected network failures were reported');
assert.equal(report.errors.filter((failure) => failure.kind === 'http' && failure.expected).length, 1,
  'The expected ERROR-policy validation must be the only expected browser HTTP failure');
report.expectedErrorWindow.active = false;
report.status = 'passed';
  } finally {
    try { await requestCapture?.flush(); } catch (error) { report.requestFlushError = String(error); }
    cda.includeBrowserDiagnostics();
    report.finished = new Date().toISOString();
    await cda.attachReport(`standalone-${cda.caseName}-domain.json`, {
      ...cda.report,
      domain: report,
    });
  }
}
