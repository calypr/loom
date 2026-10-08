import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
import { scenarioCaseFor } from '../../registry.mjs';
import { assertSavedPreviewIdentity, cdaExplorerSelectionsPath, isPostPivotRawOracleUnavailable, matchesSavedPreviewRequest, readPivotSelectOptions, readProposalPreviewDocument, relatedRouteChoice, proveSourceBinding, relatedSourceEditEvidence, sameWorkspace, uniqueEnabledSelectValue, waitForCdaCapturedResponse } from '../../workflows/verify-cda-related-source-after-pivot-browser.mjs';
import { choosePostPivotRelatedSourcePair, verifyPostPivotRelatedSourceWitness } from '../post-pivot-related-source-oracle.mjs';
import { assertVisibleRowsMatchOracle } from '../cda-row-oracle.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';

const rows = [
  { _id: 'Observation/a', id: 'obs-a', project, generation, resourceType: 'Observation', status: 'final', patientReference: 'Patient/p-a' },
  { _id: 'Observation/b', id: 'obs-b', project, generation, resourceType: 'Observation', status: 'final', patientReference: 'Patient/p-b' },
  { _id: 'Observation/c', id: 'obs-c', project, generation, resourceType: 'Observation', status: 'preliminary', patientReference: 'Patient/p-c' },
];

const linked = pair => pair.members.flatMap(member => {
  const patientID = member.patientReference.slice('Patient/'.length);
  return [{
    source: { ...member },
    patient: {
      _id: `Patient/${patientID}`,
      id: patientID,
      project,
      generation,
      resourceType: 'Patient',
    },
  }];
});

test('post-Pivot source oracle requires two same-key Observation roots with distinct exact Patient terminals', () => {
  const pair = choosePostPivotRelatedSourcePair(rows, { project, generation });
  assert.deepEqual(pair.members.map(member => member.id), ['obs-a', 'obs-b']);
  const oracle = verifyPostPivotRelatedSourceWitness(pair, linked(pair), { project, generation });
  assert.equal(oracle.status, 'final');
  assert.deepEqual(oracle.patientIDsInCompilerOrder, ['p-a', 'p-b']);
  assert.equal(oracle.members.length, 2);
});

test('post-Pivot ALL oracle preserves both linked Patient resource types in compiler order', () => {
  const pair = choosePostPivotRelatedSourcePair(rows, { project, generation });
  const oracle = verifyPostPivotRelatedSourceWitness(pair, linked(pair), { project, generation });

  assert.deepEqual(oracle.patientResourceTypesInCompilerOrder, ['Patient', 'Patient']);
  assert.equal(oracle.patientResourceTypesInCompilerOrder.length, 2,
    'Both linked Patient values must remain present even when their resourceType values match.');
  assert.equal(new Set(oracle.patientResourceTypesInCompilerOrder).size, 1,
    'The repeated Patient resourceType must not be collapsed into a distinct-value set.');
});

test('post-Pivot source oracle leaves a bounded scan without a coalescible pair unverified', () => {
  const noPair = rows.map((row, index) => ({ ...row, status: `unique-${index}` }));
  assert.equal(choosePostPivotRelatedSourcePair(noPair, { project, generation }), undefined);
  assert.equal(choosePostPivotRelatedSourcePair(rows.slice(0, 1), { project, generation, limit: 1 }), undefined,
    'A scan capped before the second matching source must not claim a witness.');
});

test('post-Pivot source oracle rejects a wrong project, generation, root, or duplicate source edge', () => {
  assert.throws(() => choosePostPivotRelatedSourcePair([
    { ...rows[0], project: 'another-project' }, rows[1],
  ], { project, generation }), /project scope/);
  assert.throws(() => choosePostPivotRelatedSourcePair([
    { ...rows[0], generation: 'another-generation' }, rows[1],
  ], { project, generation }), /generation scope/);

  const pair = choosePostPivotRelatedSourcePair(rows, { project, generation });
  assert.throws(() => verifyPostPivotRelatedSourceWitness(pair, linked(pair).slice(1), { project, generation }),
    /Exact edge reread must return only the two selected Observation roots/);
  assert.throws(() => verifyPostPivotRelatedSourceWitness(pair, [...linked(pair), linked(pair)[0]], { project, generation }),
    /exactly one scoped subject_Patient edge/);
});

test('post-Pivot source oracle rejects a mismatched or cross-scope Patient target', () => {
  const pair = choosePostPivotRelatedSourcePair(rows, { project, generation });
  const mismatched = linked(pair);
  mismatched[0].patient.id = 'some-other-patient';
  assert.throws(() => verifyPostPivotRelatedSourceWitness(pair, mismatched, { project, generation }),
    /raw subject\.reference must resolve/);

  const crossScope = linked(pair);
  crossScope[1].patient.project = 'another-project';
  assert.throws(() => verifyPostPivotRelatedSourceWitness(pair, crossScope, { project, generation }), /project scope/);
});


const relatedWorkflowPath = new URL('../../workflows/verify-cda-related-source-after-pivot-browser.mjs', import.meta.url);
const reshapeSpecPath = new URL('../../specs/standalone-reshape.spec.mjs', import.meta.url);

const savedRelatedStep = () => ({
  id: 'related-source-step',
  inputs: [{ kind: 'WORKSPACE_OUTPUT', outputId: 'pivot-output' }],
  operation: {
    kind: 'RELATED_SOURCE',
    relatedSource: { outputColumnId: 'patient-id-column', form: 'ALL', route: [{ relationship: 'subject_Patient' }] },
  },
  outputs: [
    { id: 'pivot-inherited-column', name: 'status', label: 'Observation status', type: 'string' },
    { id: 'patient-id-column', name: 'patient_id', label: 'Patient ID', type: 'string' },
  ],
});

test('related-source chooser posts the immutable selection to this Explorer’s exact route', async () => {
  const project = 'loom_dev_cda_fhir';
  const explorer = 'qa-related-source';
  const route = cdaExplorerSelectionsPath(project, explorer);
  assert.equal(route, `/api/v1/projects/${project}/explorers/${explorer}/selections`);
  assert.notEqual(route, `/api/v1/projects/${project}/selections`);
  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  assert.match(workflow, /api\(cdaExplorerSelectionsPath\(project, explorer\),/,
    'The native workflow must use the tested Explorer-scoped route helper.');
});

test('related-source native registration binds its real registry contract and exact emitted checks', async () => {
  const scenarioID = 'standalone-reshape-related-source-after-pivot';
  const caseName = 'related-source-after-pivot';
  const contract = scenarioCaseFor(scenarioID, caseName);
  const spec = await readFile(reshapeSpecPath, 'utf8');
  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  assert.match(spec, /cdaScenarioID: `standalone-reshape-\$\{name\}`/);
  assert.match(spec, /cdaCaseName: name/);
  assert.match(spec, /register\('related-source-after-pivot',\s*runRelatedSourceAfterPivotBrowserWorkflow(?:\s*,[^)]*)?\)/);
  assert.equal(contract.requiredChecks.length, 25);
  const allWorkflow = workflow
    .replaceAll('${sourceFieldLabel}', 'Patient.id')
    .replaceAll('${relatedForm}', 'ALL')
    .replaceAll('${outputValueLabel}', 'values')
    .replaceAll('${formValueLabel}', 'ALL values');
  for (const check of contract.requiredChecks.filter(name => name !== 'CDA watched source and API build stayed unchanged')) {
    assert(allWorkflow.includes(check), `Registered check is not emitted by the ALL workflow: ${check}`);
  }
  assert(contract.requiredChecks.includes('Related-source Cancel preserves the exact saved Pivot and its raw values after reload'));
  assert(!contract.requiredChecks.includes('Canceling related-source proposal preserves the exact saved Pivot and its raw values after reload'));
});

test('related-source COUNT after Pivot registers its distinct source-Pivot lifecycle', async () => {
  const contract = scenarioCaseFor('standalone-reshape-related-source-after-pivot', 'related-source-count-after-pivot');
  const spec = await readFile(reshapeSpecPath, 'utf8');
  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  const countWorkflow = workflow
    .replaceAll('${sourceFieldLabel}', 'Patient.id')
    .replaceAll('${relatedForm}', 'COUNT')
    .replaceAll('${outputValueLabel}', 'count')
    .replaceAll('${formValueLabel}', 'count');
  assert.equal(contract.requiredChecks.length, 25);
  assert(contract.requiredChecks.includes('Native chooser and candidate bind Patient.id COUNT through exact Observation.subject → Patient'));
  assert(contract.requiredChecks.includes('Post-Pivot COUNT returns the exact distinct Patient count on one Pivot row'));
  assert(contract.requiredChecks.includes('Related-source removal Cancel preserves the edited binding and exact count after reload'));
  assert(!contract.requiredChecks.some(check => check.includes('ALL returns')));
  assert.equal(contract.playwrightGrep, 'standalone CDA reshape workflows related-source-count-after-pivot related-source-count-after-pivot$');
  assert.deepEqual(contract.expectedIdentity, { project, generation });
  assert(contract.focusedChecks.some(check => check.command.includes('scripts/verify-ui/helpers/tests/post-pivot-related-source-oracle.test.mjs')),
    'The registered case must run the bounded source-oracle prerequisite.');
  for (const check of contract.requiredChecks.filter(name => name !== 'CDA watched source and API build stayed unchanged')) {
    assert(countWorkflow.includes(check), `Registered check is not emitted by the COUNT workflow: ${check}`);
  }
  assert.match(spec, /register\('related-source-count-after-pivot',\s*runRelatedSourceAfterPivotBrowserWorkflow,\s*\{\s*form:\s*'COUNT'\s*\},\s*\{[\s\S]*?cdaUiRouting:\s*'explicit-query'\s*\}\s*\)/);
  assert.match(workflow, /relatedForm === 'COUNT'\s*\? new Set\(oracle\.members\.map\(\(\{ patient \}\) => patient\._id\)\)\.size/);
  assert.match(workflow, /expectedForm === 'COUNT'.*output\.type, 'integer'/);
});

test('RELATED_SOURCE binding proves the authoring wire contributor rule and exact saved identities', () => {
  const step = {
    id: 'step-related-source',
    operation: {
      kind: 'RELATED_SOURCE',
      relatedSource: {
        anchorColumnId: 'pivot-row-id',
        choiceId: 'choice-related-patient-id',
        sourceOccurrenceId: 'occurrence-patient-id',
        source: {
          candidateId: 'candidate-patient-id',
          cardinality: 'optional_one',
          kind: 'FIELD',
          logicalType: 'string',
          nodeId: 'patient-node',
          path: 'id',
          resourceType: 'Patient',
        },
        route: [{
          edgeId: 'subject-patient-edge',
          fromNodeId: 'observation-node',
          fromResourceType: 'Observation',
          matchMode: 'OPTIONAL',
          relationship: 'subject_Patient',
          storageDirection: 'OUTBOUND',
          toNodeId: 'patient-node',
          toResourceType: 'Patient',
        }],
        contributorRule: { policy: 'ALL_MATCHES' },
        form: 'ALL',
        outputColumnId: 'patient-id-output',
      },
    },
    outputs: [{ id: 'patient-id-output', label: 'Patient ID' }],
  };
  const binding = proveSourceBinding(step, 'pivot-row-id', 'Patient ID');
  assert.equal(binding.related, step.operation.relatedSource);
  assert.equal(binding.output, step.outputs[0]);

  const resourceTypeStep = structuredClone(step);
  resourceTypeStep.operation.relatedSource.source.path = 'resourceType';
  resourceTypeStep.outputs[0].label = 'Patient resource types';
  const resourceTypeBinding = proveSourceBinding(
    resourceTypeStep, 'pivot-row-id', 'Patient resource types', 'ALL', 'resourceType',
  );
  assert.equal(resourceTypeBinding.related.source.path, 'resourceType');
  assert.throws(
    () => proveSourceBinding(resourceTypeStep, 'pivot-row-id', 'Patient resource types'),
    /exact selected Patient field path/,
    'An id assertion must reject a resourceType source binding.',
  );

  const privateRecipeOnly = structuredClone(step);
  delete privateRecipeOnly.operation.relatedSource.contributorRule;
  privateRecipeOnly.operation.relatedSource.contributorPolicy = 'ALL_MATCHES';
  assert.throws(() => proveSourceBinding(privateRecipeOnly, 'pivot-row-id', 'Patient ID'), /ALL_MATCHES/,
    'The private recipe-only flat policy must not satisfy the public authoring wire contract.');
});

test('RELATED_SOURCE COUNT stays anchored to the exact Pivot row and exposes an integer output', () => {
  const step = {
    id: 'step-related-source',
    operation: {
      kind: 'RELATED_SOURCE',
      relatedSource: {
        anchorColumnId: 'pivot-row-id',
        choiceId: 'choice-related-patient-id',
        sourceOccurrenceId: 'occurrence-patient-id',
        source: {
          candidateId: 'candidate-patient-id',
          cardinality: 'optional_one',
          kind: 'FIELD',
          logicalType: 'string',
          nodeId: 'patient-node',
          path: 'id',
          resourceType: 'Patient',
        },
        route: [{
          edgeId: 'subject-patient-edge',
          fromNodeId: 'observation-node',
          fromResourceType: 'Observation',
          matchMode: 'OPTIONAL',
          relationship: 'subject_Patient',
          storageDirection: 'OUTBOUND',
          toNodeId: 'patient-node',
          toResourceType: 'Patient',
        }],
        contributorRule: { policy: 'ALL_MATCHES' },
        form: 'COUNT',
        outputColumnId: 'patient-count-output',
      },
    },
    outputs: [{ id: 'patient-count-output', label: 'Patient count', type: 'integer' }],
  };
  const binding = proveSourceBinding(step, 'pivot-row-id', 'Patient count', 'COUNT');
  assert.equal(binding.related, step.operation.relatedSource);
  assert.equal(binding.output.type, 'integer');
  assert.throws(() => proveSourceBinding(step, 'some-other-column', 'Patient count', 'COUNT'), /row identity/);
});

test('saved related-source edit permits only the output label to change', () => {
  const before = savedRelatedStep();
  const renamed = structuredClone(before);
  renamed.outputs[1].label = 'Patient IDs';
  const evidence = relatedSourceEditEvidence(before, renamed, 'Patient IDs');
  assert.deepEqual(evidence, {
    stableStepId: true,
    unchangedOperation: true,
    unchangedStepExceptOutputLabels: true,
    stableOutputId: true,
    expectedLabelApplied: true,
    ok: true,
  });

  const changedOperation = structuredClone(renamed);
  changedOperation.operation.relatedSource.form = 'ONE';
  assert.equal(relatedSourceEditEvidence(before, changedOperation, 'Patient IDs').ok, false);
  const changedStep = structuredClone(renamed);
  changedStep.id = 'replacement-step';
  assert.equal(relatedSourceEditEvidence(before, changedStep, 'Patient IDs').ok, false);
  const changedOutput = structuredClone(renamed);
  changedOutput.outputs[1].name = 'replacement_name';
  assert.equal(relatedSourceEditEvidence(before, changedOutput, 'Patient IDs').ok, false);
  const changedInheritedLabel = structuredClone(renamed);
  changedInheritedLabel.outputs[0].label = 'Changed Pivot status';
  assert.equal(relatedSourceEditEvidence(before, changedInheritedLabel, 'Patient IDs').ok, false,
    'A label edit must not hide mutations to inherited Pivot output labels.');
});

test('full pre-source workspace restoration detects mutations outside the construction', () => {
  const before = { workspace: {
    selectedOutputId: 'pivot-output',
    documents: [
      { output: { id: 'pivot-output' }, construction: { steps: [{ id: 'pivot-step' }] } },
      { output: { id: 'sibling-output', label: 'Untouched sibling' }, construction: { steps: [] } },
    ],
  } };
  assert.equal(sameWorkspace(before, structuredClone(before)), true);
  const changedSibling = structuredClone(before);
  changedSibling.workspace.documents[1].output.label = 'Changed sibling';
  assert.equal(sameWorkspace(before, changedSibling), false);
});


test('Pivot chooser matches stable column IDs despite typed option labels', async () => {
  const browserCallback = runInNewContext(`(${readPivotSelectOptions.toString()})`);
  const selectElement = {
    nodeName: 'SELECT',
    value: 'observation-id-column',
    textContent: 'Observation ID (String)Observation ID (String)Patient reference (String)',
    options: [
      { nodeName: 'OPTION', value: 'observation-id-column', textContent: 'Observation ID (String)', disabled: false },
      { nodeName: 'OPTION', value: 'observation-id-column', textContent: 'Observation ID (String)', disabled: true },
      { nodeName: 'OPTION', value: 'patient-reference-column', textContent: 'Patient reference (String)', disabled: false },
    ],
  };
  const observedOptions = JSON.parse(JSON.stringify(browserCallback(selectElement)));
  assert.deepEqual(observedOptions, [
    { value: 'observation-id-column', label: 'Observation ID (String)', disabled: false },
    { value: 'observation-id-column', label: 'Observation ID (String)', disabled: true },
    { value: 'patient-reference-column', label: 'Patient reference (String)', disabled: false },
  ], 'The callback must enumerate child OPTION elements, not treat SELECT as an OPTION.');
  assert.equal(uniqueEnabledSelectValue(observedOptions, 'observation-id-column', 'Pivot values field').value, 'observation-id-column',
    'Stable column identity must match even when the rendered label includes its type, and a disabled duplicate must not defeat it.');
  assert.throws(() => uniqueEnabledSelectValue(observedOptions, 'missing-column', 'Pivot values field'), /one enabled exact value "missing-column" option/,
    'A missing column identity must remain rejected.');
  assert.throws(() => uniqueEnabledSelectValue([
    { value: 'disabled-column', label: 'Observation ID (String)', disabled: true },
  ], 'disabled-column', 'Pivot values field'), /one enabled exact value "disabled-column" option/,
  'A disabled-only match must remain rejected.');
  assert.throws(() => uniqueEnabledSelectValue([
    ...observedOptions,
    { value: 'observation-id-column', label: 'Observation ID (String)', disabled: false },
  ], 'observation-id-column', 'Pivot values field'), /one enabled exact value "observation-id-column" option/,
  'Ambiguous enabled stable identities must remain rejected.');
  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  assert.match(workflow, /locator\(selector\)\.evaluate\(readPivotSelectOptions\)/);
  assert.match(workflow, /uniqueEnabledSelectValue\(options, columnId, selector\)/);
  assert.match(workflow, /construction-action-table-pivot-rows/,
    'When coded Pivot is also available, the native table Pivot alternative must open the field-based editor.');
  assert.doesNotMatch(workflow, /await click\('\[data-testid="construction-action-pivot-rows"\]'\)/,
    'The main categories action opens coded Pivot in this supported CDA catalog and cannot expose table-field selectors.');
  assert.match(workflow, /sourceColumns\.patientReference\.columnId/);
  assert.match(workflow, /sourceColumns\.id\.columnId/);
});


test('proposal preview browser callback binds receipt, output, headers, and visible oracle rows', () => {
  const cells = values => values.map(textContent => ({ textContent }));
  const dataRow = {
    querySelectorAll: selector => selector === 'td' ? cells(['final', 'obs-a']) : [],
  };
  const headerCells = ['Status', 'Observation ID'].map(label => ({
    textContent: label,
    querySelector: selector => selector === 'span' ? { textContent: label } : null,
  }));
  const table = {
    querySelectorAll: selector => selector === 'thead th' ? headerCells
      : selector === 'tbody tr[data-testid="construction-proposal-preview-row"]' ? [dataRow] : [],
  };
  const panel = {
    getAttribute: name => ({ 'data-proposal-status': 'ready', 'data-proposal-id': 'receipt-7' })[name] ?? null,
  };
  const preview = {
    getAttribute: name => ({
      'data-preview-status': 'ready',
      'data-preview-receipt-id': 'receipt-7',
      'data-preview-output-id': 'output-1',
    })[name] ?? null,
    querySelector: selector => selector === 'table' ? table : null,
  };
  const documentStub = {
    querySelector: selector => selector.includes('construction-proposal-panel') ? panel
      : selector.includes('construction-proposal-preview') ? preview : null,
  };
  const browserCallback = runInNewContext(`(${readProposalPreviewDocument.toString()})`, { document: documentStub });
  const rendered = JSON.parse(JSON.stringify(browserCallback()));
  assert.deepEqual(rendered, {
    proposalStatus: 'ready',
    proposalId: 'receipt-7',
    previewStatus: 'ready',
    receiptId: 'receipt-7',
    outputId: 'output-1',
    headers: ['Status', 'Observation ID'],
    rows: [['final', 'obs-a']],
  });
});


test('raw witness absence is skipped only for this case and remains an unverified report', async () => {
  const unavailable = new Error('bounded witness not found');
  unavailable.name = 'RawOracleUnavailableError';
  unavailable.rawOracleFailure = true;
  assert.equal(isPostPivotRawOracleUnavailable(unavailable), true);
  assert.equal(isPostPivotRawOracleUnavailable(new Error('unrelated workflow failure')), false);
  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  const spec = await readFile(reshapeSpecPath, 'utf8');
  assert.match(workflow, /report\.status = 'unverified'/);
  assert.match(workflow, /error\.rawOracleFailure = true/);
  assert(spec.includes("['related-source-after-pivot', 'related-source-count-after-pivot', 'related-resource-type-after-pivot'].includes(name) && isPostPivotRawOracleUnavailable(error)"));
  assert(spec.includes('test.skip(true, error.message);'));
  assert.match(spec, /throw error;/, 'All non-witness failures must continue to fail the registered test.');
});

test('CDA request capture uses the three-argument fixture facade', async () => {
  const calls = [];
  const fixtureFacade = {
    waitForCapturedResponse(...args) {
      calls.push(args);
      return Promise.resolve('response');
    },
  };
  const tracker = {};
  const predicate = () => true;
  assert.equal(await waitForCdaCapturedResponse(fixtureFacade, tracker, predicate, 413), 'response');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].length, 3);
  assert.equal(calls[0][0], tracker);
  assert.equal(calls[0][1], predicate);
  assert.equal(calls[0][2], 413);
  const fixtureSource = await readFile(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  assert.match(fixtureSource, /waitForCapturedResponse: \(tracker, predicate, timeout\) => waitForCapturedResponse\(page, tracker, predicate, timeout\)/);
  assert.equal((workflow.match(/waitForCdaCapturedResponse\(cda, requestCapture,/g) ?? []).length, 2);
});

test('saved-preview wait binds the native POST request and response receipt to the exact output', () => {
  const entry = {
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/qa/authoring/v2/preview',
    method: 'POST',
    startedAt: 200,
    body: { receiptId: 'receipt-1', outputId: 'output-1', limit: 25 },
    status: 200,
  };
  const expected = { path: entry.path, outputId: 'output-1', startedAt: 100 };
  assert.equal(matchesSavedPreviewRequest(entry, expected), true);
  assert.equal(matchesSavedPreviewRequest({
    ...entry,
    method: 'GET',
    query: { outputId: 'output-1' },
    body: undefined,
  }, expected), false, 'The removed GET/query assumption must not match.');
  assert.equal(matchesSavedPreviewRequest({ ...entry, body: { ...entry.body, outputId: 'other-output' } }, expected), false);
  assert.equal(matchesSavedPreviewRequest({ ...entry, body: { outputId: 'output-1', receiptId: '' } }, expected), false);
  assert.equal(matchesSavedPreviewRequest({ ...entry, startedAt: 99 }, expected), false);
  assert.equal(matchesSavedPreviewRequest({ ...entry, path: '/api/v1/other/preview' }, expected), false);

  assertSavedPreviewIdentity(entry, { receiptId: 'receipt-1', outputId: 'output-1' }, 'output-1');
  assert.throws(() => assertSavedPreviewIdentity(entry, { receiptId: 'receipt-1', outputId: 'other-output' }, 'output-1'),
    /response must belong to the selected output/);
  assert.throws(() => assertSavedPreviewIdentity(entry, { receiptId: 'other-receipt', outputId: 'output-1' }, 'output-1'),
    /match the captured request receipt/);
});

test('route chooser matches the exact relationship path and rejects longer-path collisions', async () => {
  const routeLabel = 'Observation -[subject]-> Patient';
  const directCaption = `Patient ID: ${routeLabel}`;
  const longerPathCaption = `Patient ID: ${routeLabel} -[generalPractitioner]-> Practitioner`;

  assert.equal(relatedRouteChoice([], routeLabel), undefined,
    'A single-choice dialog can omit the route radio.');
  assert.deepEqual(relatedRouteChoice([
    { label: directCaption, disabled: false },
    { label: longerPathCaption, disabled: false },
    { label: 'Observation -[encounter]-> Encounter', disabled: false },
  ], routeLabel), { label: directCaption, disabled: false },
  'The chooser may prefix its candidate label, but the route path must match exactly at the end.');
  assert.deepEqual(relatedRouteChoice([
    { label: routeLabel, disabled: false },
  ], routeLabel), { label: routeLabel, disabled: false },
  'A bare exact path remains valid when no candidate prefix is rendered.');
  assert.equal(relatedRouteChoice([
    { label: longerPathCaption, disabled: false },
  ], routeLabel), undefined,
  'An exact path embedded at the start of a longer route must not be mistaken for the selected path.');
  assert.throws(() => relatedRouteChoice([
    { label: directCaption, disabled: false },
    { label: `Another field: ${routeLabel}`, disabled: false },
  ], routeLabel), /Expected at most one/,
  'Two candidates exposing the exact same route remain ambiguous.');
  assert.throws(() => relatedRouteChoice([
    { label: directCaption, disabled: true },
  ], routeLabel), /must be enabled/,
  'The exact route must remain enabled.');

  const workflow = await readFile(relatedWorkflowPath, 'utf8');
  assert.match(workflow, /hop\.relationship, 'subject_Patient'/,
    'The proposal candidate still has to prove the exact route when its only radio is omitted.');
  assert.match(workflow, /if \(route\) await click/);
});

test('a rendered proposal row mismatch fails despite ready receipt and output ownership', () => {
  const cells = values => values.map(textContent => ({ textContent }));
  const dataRow = { querySelectorAll: selector => selector === 'td' ? cells(['preliminary', 'obs-a']) : [] };
  const headerCells = ['Status', 'Observation ID'].map(label => ({
    textContent: label,
    querySelector: selector => selector === 'span' ? { textContent: label } : null,
  }));
  const table = {
    querySelectorAll: selector => selector === 'thead th' ? headerCells
      : selector === 'tbody tr[data-testid="construction-proposal-preview-row"]' ? [dataRow] : [],
  };
  const panel = { getAttribute: name => ({ 'data-proposal-status': 'ready', 'data-proposal-id': 'receipt-7' })[name] ?? null };
  const preview = {
    getAttribute: name => ({ 'data-preview-status': 'ready', 'data-preview-receipt-id': 'receipt-7', 'data-preview-output-id': 'output-1' })[name] ?? null,
    querySelector: selector => selector === 'table' ? table : null,
  };
  const documentStub = { querySelector: selector => selector.includes('construction-proposal-panel') ? panel : preview };
  const browserCallback = runInNewContext(`(${readProposalPreviewDocument.toString()})`, { document: documentStub });
  const rendered = JSON.parse(JSON.stringify(browserCallback()));
  assert.equal(rendered.proposalId, 'receipt-7');
  assert.equal(rendered.outputId, 'output-1');
  assert.throws(() => assertVisibleRowsMatchOracle(rendered.rows, [['final', 'obs-a']], {
    label: 'proposal preview disagreement', exactWindow: true,
  }), /mismatch|different|expected|actual/i,
  'Receipt and output ownership must not substitute for matching visible cells.');
});
