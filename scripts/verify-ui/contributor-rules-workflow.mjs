import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from './native-cda-workflow-tools.mjs';

export async function contributorRulesWorkflow({ page, cda, caseOptions = {} }) {
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
const explorer = `contributor-rules-browser-${Date.now()}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`;
const report = {
  explorer,
  controls: {
    covered: ['Contributor mode: only records meeting a condition', 'Related-record field search and selection: Observation id',
      'Contributor predicate: EQUALS with an independently selected CDA ID', 'No-match policy: PRESERVE_PARENT and EXCLUDE',
      'Preview, Cancel, Apply, reload, edit policy, remove Cancel, remove Apply, exact source restoration'],
    uncovered: ['All matching records rule lifecycle', 'EXISTS predicate', 'ERROR no-match policy',
      'Code-valued field conditions and suggestions', 'Alternative related paths and starting anchors'],
  },
  cases: [], requests: [], errors: [], nativeRequests: [], started: new Date().toISOString(),
};

let builder;
let outputId;
let browserEvents;
let failedResponses = [];

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

const displayedRows = async () => browserEval(page, () => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
    .map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length); });

const revealControl = async (selector, includes) => {
  const snapshot = await scrollIntoView(page, selector, { includes });
  assert(snapshot.top >= 0 && snapshot.bottom <= snapshot.viewportHeight,
    `Control remains outside the viewport after native scroll: ${JSON.stringify(snapshot)}`);
  return snapshot;
};

const assertRows = (actual, expected, name) => {
  assert.equal(actual.length, Math.min(25, expected.length), `${name}: visible row count differs`);
  const permitted = new Set(expected.map((row) => JSON.stringify(row)));
  for (const row of actual) assert(permitted.has(JSON.stringify(row)), `${name}: row is absent from the independent CDA oracle: ${JSON.stringify(row)}`);
};

const recordAction = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, ...details });
  return durationMs;
};

const rendered = async (expectedRows, name) => {
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === __arg0 && !document.body.innerText.includes('Loading your table…')), [String(Math.min(25, expectedRows.length) + 1)]);
  const rows = await displayedRows();
  assertRows(rows, expectedRows, name);
  return rows;
};

const proposal = async (name, startedAt, expectedRows) => {
  await waitForBrowser(page, () => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)), []);
  const result = await browserEval(page, () => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {
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
  await navigate(page, pageURL);
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector('[data-testid="construction-table-'+String(__arg0)+'"]')), [outputId]);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false), []);
  const rows = await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: rows.length });
};

const startRelatedExpand = async () => {
  const startedAt = Date.now();
  await click(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false), []);
  await click(page, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(''+String(__arg0)+' select[aria-label="Related record type"]')?.disabled === false), [panel]);
  await selectOption(page, `${panel} select[aria-label="Related record type"]`, 'Observation');
  const route = 'Patient <-[subject]- Observation';
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)), [`${panel} input[aria-label="${route}"]`]);
  await click(page, `${panel} input[aria-label="${route}"]`);
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(''+String(__arg0)+' [data-testid="construction-related-expand-contributor-options"]')), [panel]);
  recordAction('open-related-expand-editor', startedAt);
  return panel;
};

const chooseContributorIDEquals = async (panel, observationId, actionName) => {
  const startedAt = Date.now();
  const options = `${panel} [data-testid="construction-related-expand-contributors"]`;
  await selectOption(page, `${panel} select[aria-label="If a current row has no matches"]`, 'PRESERVE_PARENT');
  const disclosure = `${panel} [data-testid="construction-related-expand-contributor-options"]`;
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)), [disclosure+' summary']);
  const disclosureState = await browserEval(page, ([__arg0]) => { return document.querySelector(__arg0)?.open ?? false; }, [disclosure]);
  if (!disclosureState) {
    await revealControl(`${disclosure} summary`);
    await click(page, `${disclosure} summary`);
    await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)?.open === true), [disclosure]);
  }
  const onlyRecords = `${options} label`;
  await revealControl(onlyRecords, 'Only records meeting a condition');
  await click(page, onlyRecords, { includes: 'Only records meeting a condition' });
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(''+String(__arg0)+' input[placeholder="Search field name or path"]')), [options]);
  const search = `${options} input[placeholder="Search field name or path"]`;
  await revealControl(search);
  await fill(page, search, 'id');
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(''+String(__arg0)+' [role="group"][aria-label="Fields for related-record condition"] button')), [options]);
  const choices = await browserEval(page, ([__arg0]) => { return [...document.querySelectorAll(''+String(__arg0)+' [role="group"][aria-label="Fields for related-record condition"] button')]
      .map(button=>({text:button.innerText.trim(),pressed:button.getAttribute('aria-pressed')})); }, [options]);
  report.contributorChoices = choices;
  const fieldButton = choices.find((choice) => choice.text.split('\n')[0].trim() === 'id');
  assert(fieldButton, `Observation ID field was not offered: ${JSON.stringify(choices)}`);
  const contributorFieldButtons = `${options} [role="group"][aria-label="Fields for related-record condition"] button`;
  const fieldLabel = fieldButton.text.split('\n')[1]?.trim() ?? fieldButton.text;
  await revealControl(contributorFieldButtons, fieldLabel);
  await click(page, contributorFieldButtons, { includes: fieldLabel });
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(''+String(__arg0)+' select')?.value === 'EXISTS'), [options]);
  await revealControl(`${options} select`);
  await selectOption(page, `${options} select`, 'EQUALS');
  await waitForBrowser(page, ([__arg0]) => Boolean(Boolean([...document.querySelectorAll(''+String(__arg0)+' label')].find(label=>label.innerText.trim().startsWith('Exact value'))?.querySelector('input'))), [options]);
  await revealControl(`${options} label`, 'Exact value');
  await click(page, `${options} label`, { includes: 'Exact value' });
  await fill(page, `${options} label:has-text("Exact value") input`, observationId);
  await waitForBrowser(page, ([__arg0, __arg1]) => Boolean(([...document.querySelectorAll(''+String(__arg0)+' label')].find(label=>label.innerText.trim().startsWith('Exact value'))?.querySelector('input')?.value === __arg1)), [options, observationId]);
  const selected = await browserEval(page, ([__arg0, __arg1, __arg2]) => { return {condition:document.querySelector(''+String(__arg0)+' select')?.value,
      field:document.querySelector(''+String(__arg1)+' [aria-pressed="true"]')?.innerText.trim(),
      value:[...document.querySelectorAll(''+String(__arg2)+' label')].find(label=>label.innerText.trim().startsWith('Exact value'))?.querySelector('input')?.value}; }, [options, options, options]);
  assert.equal(selected.condition, 'EQUALS');
  assert(selected.field?.includes('id'), JSON.stringify(selected));
  assert.equal(selected.value, observationId);
  report.contributorRule = selected;
  recordAction(actionName, startedAt, { condition: selected.condition, sourceField: 'id', policy: 'PRESERVE_PARENT' });
  return selected;
};

const beginEdit = async (stepId) => {
  const startedAt = Date.now();
  await click(page, `[data-testid="construction-history-step-${stepId}"]`);
  await click(page, `[data-testid="construction-edit-step-${stepId}"]`);
  const policy = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)), [policy+':not(:disabled)']);
  await revealControl(policy);
  recordAction('edit-saved-step-to-policy-control', startedAt);
};

const applyProposal = async (expectedRows, name) => {
  const startedAt = Date.now();
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
  await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: expectedRows.length });
  builder = await api(base + '/builder');
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
const selectedObservationID = witnesses.find((item) => item.bucket === 'many').observations[0].id;
const selectedMatches = witnesses.flatMap((item) => item.observations
  .filter((observation) => observation.id === selectedObservationID)
  .map((observation) => [item.patient.id, observation.id]));
assert.equal(selectedMatches.length, 1, 'The selected CDA Observation ID must identify exactly one related source record');
const allRows = witnesses.flatMap((item) => {
  const matches = item.observations.filter((observation) => observation.id === selectedObservationID);
  return matches.length ? matches.map((observation) => [item.patient.id, observation.id]) : [[item.patient.id, '—']];
});
const excludedRows = selectedMatches;
assert(allRows.length > excludedRows.length, 'PRESERVE_PARENT must retain the zero-match source witness');

await api(root, { name: explorer, title: 'Contributor rules lifecycle QA' });
builder = await api(base + '/builder');
assert.equal(builder.catalog.generation, generation);
const patientNode = builder.catalog.nodes.find((node) => node.resourceType === 'Patient');
assert(patientNode, 'Patient source type is absent from the current catalog');
await command([{ type: 'CREATE_TABLE', title: 'Contributor rules QA', rootNodeId: patientNode.nodeId }]);
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

report.nativeRequests = cda.report.nativeRequests;
browserEvents = cda.captureRequests(root + '/' + explorer, { responsePaths: { apiOrigin, uiOrigin, responsePaths: /related-expand|commands|preview/ }.responsePaths });



await open(baselineRows, 'source-selection-zero-one-many');
let panel = await startRelatedExpand();
await chooseContributorIDEquals(panel, selectedObservationID, 'configure-contributor-rule-controls');
let startedAt = Date.now();
await proposal('contributor-id-equals-preview', startedAt, allRows);
const beforeCancel = await api(base + '/builder');
startedAt = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), [], 5000);
await rendered(baselineRows, 'cancel-contributor-preview');
recordAction('cancel-contributor-preview', startedAt, { rowCount: baselineRows.length });
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeCancel.workspace, 'Cancel must leave the saved workspace unchanged');
assert.deepEqual(documentForOutput(builder), original, 'Cancel must preserve the exact source table');
report.cases.push({ name: 'contributor-rule-cancel-preserves-source', workspaceUnchanged: true });

panel = await startRelatedExpand();
await chooseContributorIDEquals(panel, selectedObservationID, 'reconfigure-contributor-rule-after-cancel');
startedAt = Date.now();
await proposal('confirmed-contributor-id-equals-preview', startedAt, allRows);
await applyProposal(allRows, 'apply-contributor-rule-to-render');
let relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assert(relatedStep, 'Applied contributor rule is missing from saved construction');
const sourceProjection = relatedStep.outputs.find((column) => column.name === original.columns[0].column);
assert(sourceProjection?.id, 'Applied related-expand step omitted the compiler-owned source projection identity');
const canonicalSourceColumnId = sourceProjection.id;
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'initial contributor Apply');
report.sourceColumnIdentity = {
  sourceColumnName: sourceProjection.name,
  compilerCanonicalSourceColumnId: canonicalSourceColumnId,
};
assert.equal(relatedStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.operator, 'EQUALS');
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.value.string, selectedObservationID);
assert.equal(relatedStep.operation.relatedExpand.contributorSource.path, 'id');
await open(allRows, 'reload-contributor-id-exists');

const beforeEdit = await api(base + '/builder');
assertStableSourceProjection(beforeEdit, original, canonicalSourceColumnId, 'reload after initial contributor Apply');
relatedStep = documentForOutput(beforeEdit).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
await beginEdit(relatedStep.id);
const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
assert.equal(await cda.inspect(([__arg0]) => { return document.querySelector(__arg0)?.value; }, [policySelector]), 'PRESERVE_PARENT');
startedAt = Date.now();
await nativeSelect(page, policySelector, 'EXCLUDE', {});
await proposal('edit-policy-exclude-preview', startedAt, excludedRows);
await applyProposal(excludedRows, 'apply-exclude-policy-to-render');
relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'edited EXCLUDE policy Apply');
assert.equal(relatedStep.operation.relatedExpand.emptyPolicy, 'EXCLUDE');
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.operator, 'EQUALS');
assert.equal(relatedStep.operation.relatedExpand.contributorRule.predicate.value.string, selectedObservationID);
await open(excludedRows, 'reload-edited-exclude-policy');

const beforeRemoval = await api(base + '/builder');
assertStableSourceProjection(beforeRemoval, original, canonicalSourceColumnId, 'reload after edited EXCLUDE policy');
relatedStep = documentForOutput(beforeRemoval).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
const remove = async () => {
  await nativeClick(page, `[data-testid="construction-history-step-${relatedStep.id}"]`, {});
  startedAt = Date.now();
  await nativeClick(page, `[data-testid="construction-remove-step-${relatedStep.id}"]`, {});
  await proposal('remove-contributor-rule-preview', startedAt, baselineRows);
};
await remove();
startedAt = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), [], 5000);
await rendered(excludedRows, 'cancel-remove-to-render');
recordAction('cancel-remove-to-render', startedAt, { rowCount: excludedRows.length });
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeRemoval.workspace, 'Cancel removal must preserve the authored Contributor rule');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'cancel remove');
report.cases.push({ name: 'remove-cancel-preserves-contributor-rule', workspaceUnchanged: true });
await remove();
await applyProposal(baselineRows, 'apply-remove-to-render');
const restored = documentForOutput(builder);
assert.deepEqual(restored.construction?.steps ?? [], original.construction?.steps ?? [],
  'Removing Contributor rules must restore the exact authored source construction');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'remove Apply');
assert.deepEqual(restored.rows, original.rows, 'Removing Contributor rules must restore the exact row definition');
assert.deepEqual(restored.population, original.population, 'Removing Contributor rules must restore the exact selected population');
await open(baselineRows, 'reload-restored-source-table');
builder = await api(base + '/builder');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'reload after Contributor removal');
await browserEvents?.flush();
cda.includeBrowserDiagnostics();
failedResponses = report.nativeRequests.filter(entry => entry.status >= 400).map(entry => ({
  kind: 'http', path: entry.path, status: entry.status, request: entry.body, body: entry.response, observedAfter: report.cases.at(-1)?.name,
}));
assert.deepEqual(report.errors, [], 'Unexpected browser errors were reported');
report.status = 'passed';
  } finally {
    try { await browserEvents?.flush(); } catch (error) { report.requestFlushError = String(error); }
    cda.includeBrowserDiagnostics();
    report.finished = new Date().toISOString();
    await cda.attachReport(`standalone-${cda.caseName}-domain.json`, {
      ...cda.report,
      domain: report,
    });
  }
}
