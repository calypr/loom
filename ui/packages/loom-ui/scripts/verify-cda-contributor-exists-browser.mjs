import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
export async function verifyContributorExistsBrowser({ page, cda }) {
const project = cda.project;
const generation = cda.generation;
const explorer = `contributor-exists-browser-${Date.now()}`;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
const browserRequestOrigins = new Set([apiOrigin, uiOrigin].map((origin) => new URL(origin).origin));
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
  cases: [], requests: [], nativeRequests: cda.report.nativeRequests, errors: [], expectedErrorWindow: { active: false }, started: new Date().toISOString(), target: cda.target,
};
let builder;
let outputId;
const nativeTracker = cda.captureRequests(root, { responsePaths: /construction-proposals|related-expand-choices|related-expand-contributors/ });
report.nativeRequests = cda.report.nativeRequests;
const waitFor = (predicate, args = {}, timeout = 5000) => cda.wait(predicate, args, Math.min(timeout, 5000));
const click = (selector, identity = {}) => cda.click(selector, identity);
const navigate = (url) => cda.navigate(url);
const selectOption = (selector, value) => cda.selectOption(selector, value);

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `contributor-rules-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, `${path}: ${JSON.stringify(value)}`);
  return value;
};

const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', cda.target.arangoContainer,
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

const displayedRows = async () => page.locator('[data-testid="preview-table-scroll"] [role="row"]')
  .evaluateAll((rows) => rows.slice(1)
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));

const revealControl = async (selector, includes) => {
  let locator = page.locator(selector);
  if (includes !== undefined) locator = locator.filter({ hasText: includes });
  locator = locator.first();
  await locator.scrollIntoViewIfNeeded({ timeout: 5000 });
  const [box, text, viewport] = await Promise.all([
    locator.boundingBox(), locator.getAttribute('aria-label').catch(() => null), page.evaluate(() => innerHeight),
  ]);
  assert(box, `Could not reveal control: ${selector}`);
  const snapshot = { top: box.y, bottom: box.y + box.height, viewportHeight: viewport, text };
  assert(snapshot.top >= 0 && snapshot.bottom <= snapshot.viewportHeight,
    `Control remains outside the viewport after native scroll: ${JSON.stringify(snapshot)}`);
  return snapshot;
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
  await waitFor(({ rowCount }) => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount)
    && !document.body.innerText.includes('Loading your table…'), { rowCount: Math.min(25, expectedRows.length) + 1 });
  const rows = await displayedRows();
  assertRows(rows, expectedRows, name);
  return rows;
};

const proposal = async (name, startedAt, expectedRows) => {
  await waitFor(() => ['ready', 'error', 'needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus));
  const result = await page.locator('[data-testid="construction-proposal-panel"]').evaluate((panel) => ({
    status: panel?.dataset.proposalStatus, text: panel?.innerText,
    rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
  }));
  assert.equal(result.status, 'ready', `${name}: ${result.text}`);
  assertRows(result.rows, expectedRows, name);
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: expectedRows.length, preview: result.rows });
  return result;
};

const open = async (expectedRows, name) => {
  const startedAt = Date.now();
  await navigate(pageURL);
  await waitFor(({ outputId }) => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), { outputId });
  await click(`[data-testid="construction-table-${outputId}"]`);
  await waitFor(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false);
  const rows = await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: rows.length });
};

const startRelatedExpand = async () => {
  const startedAt = Date.now();
  await click('[data-testid="construction-rows-settings-trigger"]');
  await waitFor(() => document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false);
  await click('[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitFor((panelSelector) => document.querySelector(`${panelSelector} select[aria-label="Related record type"]`)?.disabled === false, panel);
  await selectOption(`${panel} select[aria-label="Related record type"]`, 'Observation');
  const route = 'Patient <-[subject]- Observation';
  await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${panel} input[aria-label="${route}"]` });
  await click(`${panel} input[aria-label="${route}"]`);
  await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${panel} [data-testid="construction-related-expand-contributor-options"]` });
  recordAction('open-related-expand-editor', startedAt);
  return panel;
};

const openContributorOptions = async (panel) => {
  const options = `${panel} [data-testid="construction-related-expand-contributors"]`;
  const disclosure = `${panel} [data-testid="construction-related-expand-contributor-options"]`;
  await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${disclosure} summary` });
  const disclosureState = await page.locator(disclosure).evaluate(element => element.open ?? false);
  if (!disclosureState) {
    await revealControl(`${disclosure} summary`);
    await click(`${disclosure} summary`);
    await waitFor(({ selector }) => document.querySelector(selector)?.open === true, { selector: disclosure });
  }
  return { options, disclosure };
};

const chooseContributorIDExists = async (panel, actionName) => {
  const startedAt = Date.now();
  const { options } = await openContributorOptions(panel);
  const onlyRecords = `${options} label`;
  await revealControl(onlyRecords, 'Only records meeting a condition');
  await click(onlyRecords, { includes: 'Only records meeting a condition' });
  await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${options} input[placeholder="Search field name or path"]` });
  const search = `${options} input[placeholder="Search field name or path"]`;
  await revealControl(search);
  await click(search);
  await page.keyboard.insertText('id');
  const contributorFieldButtons = `${options} [role="group"][aria-label="Fields for related-record condition"] button`;
  await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: contributorFieldButtons });
  const choices = await page.locator(contributorFieldButtons).evaluateAll(buttons => buttons
    .map(button => ({ text: button.innerText.trim(), pressed: button.getAttribute('aria-pressed') })));
  report.contributorChoices = choices;
  const fieldButton = choices.find((choice) => choice.text.split('\n')[0].trim() === 'id');
  assert(fieldButton, `Observation ID field was not offered: ${JSON.stringify(choices)}`);
  const fieldLabel = fieldButton.text.split('\n')[1]?.trim() ?? fieldButton.text;
  await revealControl(contributorFieldButtons, fieldLabel);
  await click(contributorFieldButtons, { includes: fieldLabel });
  await waitFor(({ selector }) => document.querySelector(selector)?.value === 'EXISTS', { selector: `${options} select` });
  await revealControl(`${options} select`);
  const selected = await page.locator(options).evaluate(root => ({
    condition: root.querySelector('select')?.value,
    field: root.querySelector('[aria-pressed="true"]')?.innerText.trim(),
    options: [...(root.querySelector('select')?.options ?? [])].map(option => ({ value: option.value, disabled: option.disabled })),
  }));
  assert.equal(selected.condition, 'EXISTS');
  assert(selected.options.some((option) => option.value === 'EXISTS' && !option.disabled), JSON.stringify(selected));
  assert(selected.field?.includes('id'), JSON.stringify(selected));
  report.contributorRule = selected;
  recordAction(actionName, startedAt, { condition: selected.condition, sourceField: 'id' });
  return selected;
};

const beginEdit = async (stepId) => {
  const startedAt = Date.now();
  await click(`[data-testid="construction-history-step-${stepId}"]`);
  await click(`[data-testid="construction-edit-step-${stepId}"]`);
  const policy = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${policy}:not(:disabled)` });
  await revealControl(policy);
  recordAction('edit-saved-step-to-policy-control', startedAt);
};

const applyProposal = async (expectedRows, name) => {
  const startedAt = Date.now();
  await click('[data-testid="construction-apply-proposal"]');
  await waitFor(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
  await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: expectedRows.length });
  builder = await api(base + '/builder');
};

const expectedErrorProposal = async (name, startedAt) => {
  await waitFor(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'error');
  const result = await page.locator('[data-testid="construction-proposal-panel"]').evaluate(panel => ({
    status: panel?.dataset.proposalStatus, text: panel?.innerText,
    alert: panel?.querySelector('[data-testid="construction-proposal-error"]')?.innerText,
    retryVisible: Boolean(panel?.querySelector('[data-testid="construction-retry-proposal"]')),
  }));
  assert.equal(result.status, 'error', `${name}: ERROR policy did not reject the preview`);
  assert.equal(result.retryVisible, false, `${name}: non-retryable validation error exposed Retry preview`);
  const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  const repair = await page.locator(policySelector).evaluate(select => ({
    disabled: select?.disabled, value: select?.value,
    options: [...(select?.options ?? [])].map(option => ({ value: option.value, disabled: option.disabled })),
  }));
  assert.equal(repair.value, 'ERROR');
  assert.equal(repair.disabled, false, 'The no-match policy must remain editable after ERROR preview validation');
  for (const option of ['PRESERVE_PARENT', 'EXCLUDE']) {
    assert(repair.options.some((item) => item.value === option && !item.disabled), `${option} repair choice is unavailable`);
  }

  const requestEntry = await nativeTracker.waitFor((item) => item.path === `${base}/construction-proposals`
    && item.startedAt >= startedAt, { timeoutMs: 5000 });
  const request = await cda.waitForCapturedResponse(nativeTracker, item => item === requestEntry, 5000);
  const rawResponse = nativeTracker.rawResponseBody(request);
  const response = typeof rawResponse === 'string'
    ? (() => { try { return JSON.parse(rawResponse); } catch { return null; } })()
    : rawResponse;
  assert([400, 422].includes(request.status), `${name}: expected a validation response, got ${request.status}: ${JSON.stringify(response)}`);
  const code = response?.error?.code ?? response?.code ?? response?.errorCode ?? response?.error?.errorCode;
  const diagnostic = `${JSON.stringify(response)} ${result.alert ?? result.text}`;
  assert.notEqual(code, 'INTERNAL_ERROR', `${name}: ERROR policy returned INTERNAL_ERROR: ${diagnostic}`);
  assert(/empty list or no matching related records/i.test(diagnostic),
    `${name}: expected the shared public empty-expansion guidance: ${diagnostic}`);
  const expectedFailure = cda.expectHttpFailure(
    requestEntry,
    `${name}: ERROR policy rejects an empty contributor expansion`,
    {
      action: 'Select ERROR for the no-match policy and request a contributor preview',
      policy: report.expectedErrorWindow.policy,
      phase: name,
      request: {
        method: requestEntry.method,
        path: requestEntry.path,
        startedAt: requestEntry.startedAt,
        status: requestEntry.status,
      },
      sourceOracle: {
        project: report.oracle.project,
        generation: report.oracle.generation,
        relationship: report.oracle.relationship,
        witnessCount: report.oracle.witnesses.length,
        countByBucket: report.oracle.countByBucket,
        zeroMatchWitnessCount: report.oracle.witnesses.filter((item) => item.observations.length === 0).length,
        diagnostic: response,
      },
      ui: { status: result.status, alert: result.alert, text: result.text, retryVisible: result.retryVisible },
    },
    { status: requestEntry.status },
  );
  report.expectedHttpFailure = expectedFailure;
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, status: request.status, code, message: result.alert, repairOptions: repair.options });
  report.expectedPolicyError = { status: request.status, code, response, message: result.alert };
  report.expectedErrorWindow.active = false;
};

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

  page.on('pageerror', (error) => report.errors.push({ kind: 'runtime', message: error.message }));
  page.on('console', (message) => {
    if (message.type() === 'error') report.errors.push({ kind: 'console', text: message.text() });
  });
  page.on('requestfailed', (request) => {
    const url = new URL(request.url());
    if (browserRequestOrigins.has(url.origin) && request.resourceType() === 'script'
      && request.failure()?.errorText !== 'net::ERR_ABORTED') {
      report.errors.push({ kind: 'module', url: request.url(), error: request.failure()?.errorText });
    }
  });
  page.on('response', (response) => {
    const url = new URL(response.url());
    const path = url.pathname;
    const nativeRequest = nativeTracker.byRequest.get(response.request());
    if (response.status() >= 400 && !response.url().endsWith('/favicon.ico')) {
      const owned = browserRequestOrigins.has(url.origin) && path.startsWith(`${root}/${explorer}/`);
      const isExpected = Boolean(owned && report.expectedErrorWindow.active && path === `${base}/construction-proposals`);
      const failure = { kind: 'http', url: response.url(), status: response.status(), expected: isExpected,
        request: nativeRequest?.body, observedAfter: report.cases.at(-1)?.name };
      report.errors.push(failure);
      if (nativeRequest) {
        nativeRequest.expectedError = isExpected;
        nativeRequest.failure = failure;
      }
      if (nativeRequest) nativeRequest.failure = failure;
    }
  });

  await open(baselineRows, 'source-selection-zero-one-many');
  let startedAt = Date.now();
  let panel = await startRelatedExpand();
  const baselineContributor = await openContributorOptions(panel);
  const baselineControls = await page.locator(baselineContributor.options).locator('input[type="radio"]').evaluateAll(inputs => inputs.map(input => ({
    label: input.parentElement?.innerText.trim(), checked: input.checked, disabled: input.disabled,
  })));
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
  await click('[data-testid="construction-cancel-proposal"]');
  await waitFor(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
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
  await selectOption(policySelector, 'ERROR');
  await expectedErrorProposal('exists-error-policy-repair', startedAt);

  startedAt = Date.now();
  await selectOption(policySelector, 'PRESERVE_PARENT');
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
  assert.equal(await page.locator(policySelector).inputValue(), 'PRESERVE_PARENT');
  startedAt = Date.now();
  await selectOption(policySelector, 'EXCLUDE');
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
    await click(`[data-testid="construction-history-step-${relatedStep.id}"]`);
    startedAt = Date.now();
    await click(`[data-testid="construction-remove-step-${relatedStep.id}"]`);
    await proposal('remove-exists-contributor-preview', startedAt, baselineRows);
  };
  await remove();
  startedAt = Date.now();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitFor(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
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
  assert.deepEqual(report.errors.filter((failure) => !failure.expected), [], 'Unexpected browser errors were reported');
  assert.equal(report.errors.filter((failure) => failure.expected).length, 1,
    'The expected ERROR-policy validation must be the only expected browser HTTP failure');
  report.expectedErrorWindow.active = false;
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = await page.evaluate(() => {
    const body = document.body.innerText;
    return {
      body: body.slice(0, 6000), bodyTail: body.slice(-12000),
      alerts: [...document.querySelectorAll('[role="alert"]')].map(item => item.innerText),
      selects: [...document.querySelectorAll('select')].map(select => ({
        label: select.getAttribute('aria-label'), value: select.value, disabled: select.disabled,
        options: [...select.options].map(option => ({ label: option.textContent.trim(), value: option.value, disabled: option.disabled })),
      })),
      contributorModes: [...document.querySelectorAll('[data-testid="construction-related-expand-contributors"] input[type="radio"]')].map(input => ({
        label: input.parentElement?.innerText.trim(), checked: input.checked, disabled: input.disabled,
      })),
      relatedRoutes: [...document.querySelectorAll('input[name^="related-expand-route-"]')].map(input => ({
        label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled,
        visible: !input.closest('details:not([open])'),
      })),
    };
  }).catch(String);
  throw error;
} finally {
  report.finished = new Date().toISOString();
  await nativeTracker.flush();
  await cda.includeBrowserDiagnostics();
  report.diagnostics = cda.diagnostics;
  await cda.attachReport('contributor-exists-browser', report);
}
return report;
}
