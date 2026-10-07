import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { includeBrowserDiagnostics } from '../cda-playwright.mjs';
import { lifecycleAcceptanceDrift, registry, scenarioCaseFor } from '../../registry.mjs';
import {
  assertNoUnexpectedCdaDiagnostics,
  assertCountRowsGroupLabelEditIdentity,
  assertCountRowsGroupLabelEditProposal,
  assertBuilderDraftAdvanced,
  assertGroupRemovalCancelRestoration,
  missingComponentGroupSkipReason,
  selectMissingComponentGroupOracle,
} from '../missing-component-group-oracle.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const scoped = (id, payload) => ({ project, generation, id, payload: { resourceType: 'Observation', ...payload } });
const oracleRows = [
  scoped('observation-positive', { component: [{ valueString: 'alpha' }, { valueString: 'beta' }] }),
  scoped('observation-missing-a', {}),
  scoped('observation-missing-b', {}),
  scoped('observation-missing-c', {}),
  scoped('observation-literal-empty', { component: [] }),
  scoped('observation-null', { component: null }),
];

test('the registered missing-component GROUP case stays separate from literal-empty coverage', () => {
  const scenario = registry.find(item => item.id === 'cda-repeated-missing-component-group');
  assert(scenario, 'Missing-component GROUP scenario is not registered');
  const registeredCase = scenarioCaseFor(scenario, 'missing-component-expanded-group-cancel-restore');
  assert.equal(registeredCase.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-rows.spec.mjs');
  assert(registeredCase.requiredChecks.includes('Canceling GROUP removal leaves the saved GROUP workspace and exact four-row preview unchanged'));
  assert(registeredCase.requiredChecks.includes('Cancel restores exact source EXPANDED component rows before Apply'));
  assert(registeredCase.requiredChecks.includes('authored GROUP is composed after source EXPANDED without changing source bindings'));
  assert(registeredCase.requiredChecks.includes('applied GROUP renders the exact four-row table before reload'));
  assert(registeredCase.requiredChecks.includes('saved GROUP output-label edit proposal preserves exact four COUNT_ROWS rows and stable step/output IDs'));
  assert(registeredCase.requiredChecks.includes('Canceling saved GROUP output-label edit preserves the original saved label and exact four-row table'));
  assert(registeredCase.requiredChecks.includes('Applying saved GROUP output-label edit preserves stable step/output IDs and exact rows after reload'));
  assert(scenario.requiredTransitions.includes('Cancel the initial GROUP proposal and reload the exact source EXPANDED rows before applying GROUP'));
  assert(scenario.requiredTransitions.includes('Apply GROUP and verify the exact four-row visible output before reload and again after reload'));
  assert(scenario.requiredTransitions.includes('edit the saved COUNT_ROWS GROUP output label, Cancel to preserve its original label and rows, then Apply and reload with stable step/output IDs'));
  assert.equal(scenario.coverage[0].acceptance.case, 'missing-component-expanded-group-cancel-restore');
  assert.deepEqual(scenario.coverage[0].acceptance.checks, {
    choice: 2, proposal: 5, cancel: 12, apply: 6, savedRows: 7, reload: 8, edit: 11, restoration: 13,
  });
  const phaseChecks = scenario.coverage[0].acceptance.checks;
  assert.equal(registeredCase.requiredChecks[phaseChecks.choice], 'PRESERVE_PARENT expansion returns five exact item and missing-owner rows');
  assert.equal(registeredCase.requiredChecks[phaseChecks.proposal], 'COUNT_ROWS Group proposal returns four exact Observation ID cardinalities');
  assert.equal(registeredCase.requiredChecks[phaseChecks.cancel], 'Canceling GROUP removal leaves the saved GROUP workspace and exact four-row preview unchanged');
  assert.equal(registeredCase.requiredChecks[phaseChecks.apply], 'authored GROUP is composed after source EXPANDED without changing source bindings');
  assert.equal(registeredCase.requiredChecks[phaseChecks.savedRows], 'applied GROUP renders the exact four-row table before reload');
  assert.equal(registeredCase.requiredChecks[phaseChecks.reload], 'applied Group survives reload with exact four-row counts');
  assert.equal(registeredCase.requiredChecks[phaseChecks.edit], 'Applying saved GROUP output-label edit preserves stable step/output IDs and exact rows after reload');
  assert.equal(registeredCase.requiredChecks[phaseChecks.restoration], 'applying GROUP removal restores the exact five-row source EXPANDED table after reload');
  assert.equal(scenario.coverage[0].acceptance.notApplicable?.edit, undefined);
  assert.deepEqual(lifecycleAcceptanceDrift([scenario]), []);
  const spec = readFileSync(new URL('../../specs/standalone-cda-rows.spec.mjs', import.meta.url), 'utf8');
  const workflow = readFileSync(new URL('../../workflows/verify-cda-repeated-empty-browser.mjs', import.meta.url), 'utf8');
  assert.match(spec, /cdaScenarioID: 'cda-repeated-missing-component-group'/);
  assert.match(spec, /cdaCaseName: 'missing-component-expanded-group-cancel-restore'/);
  assert.match(spec, /missingComponentGroupSkipReason\(result\)/);
  for (const check of registeredCase.requiredChecks.filter(name => name !== 'CDA watched source and API build stayed unchanged')) {
    assert(workflow.includes(check), `Registered native check is not emitted by the workflow: ${check}`);
  }
});

test('only an explicit bounded-witness gap may skip; failed and invalidated lifecycle results stay fatal', () => {
  const reason = 'The bounded raw Observation sample did not contain three missing-component owners.';
  const unavailable = {
    status: 'unverified',
    oracle: { status: 'unverified' },
    gaps: [{ assertion: 'bounded missing-component GROUP source oracle', status: 'unverified', reason }],
    skipReason: reason,
    failures: [], invalidations: [],
  };
  assert.equal(missingComponentGroupSkipReason({ status: 'passed' }), undefined);
  assert.equal(missingComponentGroupSkipReason(unavailable), reason);
  for (const status of ['failed', 'invalidated']) {
    assert.throws(() => missingComponentGroupSkipReason({ ...unavailable, status }), /must fail the native case/);
  }
  assert.throws(() => missingComponentGroupSkipReason({ ...unavailable, gaps: [{ assertion: 'unexpected browser error', status: 'unverified', reason }] }),
    /Only an explicit bounded missing-component witness gap may be skipped/);
  assert.throws(() => missingComponentGroupSkipReason({ ...unavailable, failures: [{ error: 'browser assertion failed' }] }),
    /Only an explicit bounded missing-component witness gap may be skipped/);
});

test('saved COUNT_ROWS GROUP label edit preserves stable IDs, semantics, and exact oracle preview rows', () => {
  const baseline = {
    id: 'group-step-1',
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: 'group-step-1', missingKeyPolicy: 'GROUP',
        keys: [{ inputColumnId: 'source-observation-id', outputColumnId: 'group-id' }],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'group-count' }],
      },
    },
    outputs: [
      { id: 'group-id', name: 'observation_id', label: 'Observation ID', type: 'string' },
      { id: 'group-count', name: 'row_count', label: 'Row count', type: 'integer' },
    ],
  };
  const edited = structuredClone(baseline);
  edited.outputs.find(output => output.id === 'group-count').label = 'Expanded component owners';
  const expectedRows = [
    ['observation-missing-a', 1],
    ['observation-missing-b', 1],
    ['observation-missing-c', 1],
    ['observation-positive', 2],
  ];
  const preview = {
    rowCount: 4,
    columns: [
      { column: 'observation_id', label: 'Observation ID' },
      { column: 'row_count', label: 'Expanded component owners' },
    ],
    rows: [
      { observation_id: 'observation-positive', row_count: 2 },
      { observation_id: 'observation-missing-a', row_count: 1 },
      { observation_id: 'observation-missing-b', row_count: 1 },
      { observation_id: 'observation-missing-c', row_count: 1 },
    ],
  };
  assert.deepEqual(assertCountRowsGroupLabelEditIdentity(baseline, edited, 'Expanded component owners'), {
    stepId: 'group-step-1', keyOutputId: 'group-id', countOutputId: 'group-count', countOutputLabel: 'Expanded component owners',
  });
  assert.deepEqual(assertCountRowsGroupLabelEditProposal({
    beforeStep: baseline, editedStep: edited, preview, expectedRows, expectedLabel: 'Expanded component owners',
  }), {
    stepId: 'group-step-1', keyOutputId: 'group-id', countOutputId: 'group-count',
    countOutputLabel: 'Expanded component owners', rowCount: 4, rows: expectedRows,
  });

  const changedOperation = structuredClone(edited);
  changedOperation.operation.group.aggregates[0].operation = 'COUNT_DISTINCT';
  assert.throws(() => assertCountRowsGroupLabelEditIdentity(baseline, changedOperation, 'Expanded component owners'), /COUNT_ROWS semantics/);
  const changedStepID = structuredClone(edited);
  changedStepID.id = 'replacement-step';
  assert.throws(() => assertCountRowsGroupLabelEditIdentity(baseline, changedStepID, 'Expanded component owners'), /stable step ID/);
  const changedOutputID = structuredClone(edited);
  changedOutputID.outputs[1].id = 'replacement-output';
  assert.throws(() => assertCountRowsGroupLabelEditIdentity(baseline, changedOutputID, 'Expanded component owners'), /preserve every output identity/);
  assert.throws(() => assertCountRowsGroupLabelEditProposal({
    beforeStep: baseline, editedStep: edited,
    preview: { ...preview, columns: [{ ...preview.columns[0] }, { ...preview.columns[1], label: 'Row count' }] },
    expectedRows, expectedLabel: 'Expanded component owners',
  }), /exact edited COUNT_ROWS label/);
  assert.throws(() => assertCountRowsGroupLabelEditProposal({
    beforeStep: baseline, editedStep: edited,
    preview: { ...preview, rows: preview.rows.slice(1) }, expectedRows, expectedLabel: 'Expanded component owners',
  }), /row array count/);
  const wrongRows = structuredClone(preview);
  wrongRows.rows[0].row_count = 3;
  assert.throws(() => assertCountRowsGroupLabelEditProposal({
    beforeStep: baseline, editedStep: edited, preview: wrongRows, expectedRows, expectedLabel: 'Expanded component owners',
  }), /exact raw COUNT_ROWS rows/);
});

test('saved GROUP Apply compares CAS fields from BuilderState, not its workspace', () => {
  const before = {
    draftVersion: 7,
    draftDigest: 'draft-before',
    workspace: { documents: [{ id: 'saved-output', construction: [{ id: 'group-step' }] }] },
  };
  const after = {
    draftVersion: 8,
    draftDigest: 'draft-after',
    workspace: { documents: [{ id: 'saved-output', construction: [{ id: 'group-step', label: 'Expanded component owners' }] }] },
  };
  assert.equal(Object.hasOwn(before.workspace, 'draftVersion'), false);
  assert.equal(Object.hasOwn(before.workspace, 'draftDigest'), false);
  assert.deepEqual(assertBuilderDraftAdvanced(before, after), {
    beforeDraftVersion: 7,
    afterDraftVersion: 8,
    beforeDraftDigest: 'draft-before',
    afterDraftDigest: 'draft-after',
  });
  assert.throws(() => assertBuilderDraftAdvanced(before.workspace, after.workspace), /BuilderState must own an integer draftVersion/);
  assert.throws(() => assertBuilderDraftAdvanced(before, { ...after, draftVersion: 7 }), /advance the BuilderState draftVersion/);
  assert.throws(() => assertBuilderDraftAdvanced(before, { ...after, draftDigest: 'draft-before' }), /advance the BuilderState draftDigest/);

  const workflow = readFileSync(new URL('../../workflows/verify-cda-repeated-empty-browser.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /const beforeApplyBuilder = structuredClone\(builder\)/);
  assert.match(workflow, /assertBuilderDraftAdvanced\(beforeApplyBuilder, builder\)/);
  assert.doesNotMatch(workflow, /beforeApplyWorkspace\.draft(?:Version|Digest)/);
});

test('saved GROUP label edit opens the collapsed advanced section before waiting for its label input', () => {
  const reshapeSource = readFileSync(new URL('../../../../ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx', import.meta.url), 'utf8');
  const detailsStart = reshapeSource.indexOf('<details data-testid="construction-reshape-group-advanced"');
  assert(detailsStart >= 0, 'Native GROUP editor must expose its advanced disclosure');
  const detailsOpen = reshapeSource.indexOf('>', detailsStart);
  const detailsTag = reshapeSource.slice(detailsStart, detailsOpen + 1);
  assert(!/\bopen(?:\s|=|>)/.test(detailsTag), 'Native GROUP advanced disclosure must start collapsed');
  const labelInput = reshapeSource.indexOf('aria-label={`Summary output label ${index + 1}`}', detailsOpen);
  const detailsClose = reshapeSource.indexOf('</details>', detailsOpen);
  assert(labelInput > detailsOpen && labelInput < detailsClose, 'COUNT_ROWS label input must be inside the advanced disclosure');

  const workflow = readFileSync(new URL('../../workflows/verify-cda-repeated-empty-browser.mjs', import.meta.url), 'utf8');
  const openEditorStart = workflow.indexOf('const openSavedGroupEditor =');
  const openEditorEnd = workflow.indexOf('\n};', openEditorStart);
  assert(openEditorStart >= 0 && openEditorEnd > openEditorStart, 'Saved GROUP editor opener must be present');
  const openEditor = workflow.slice(openEditorStart, openEditorEnd);
  const openCheck = openEditor.indexOf("{ kind: 'open', selector: advanced, value: true }");
  const labelWait = openEditor.indexOf('input[aria-label="Summary output label 1"]');
  assert(openEditor.includes("await click(nativePage, advanced + ' summary')"), 'Saved GROUP editor must open the collapsed advanced disclosure');
  assert(openCheck >= 0 && labelWait > openCheck, 'Saved GROUP editor must confirm the disclosure is open before waiting for the label input');
});

test('saved GROUP edit Cancel times only through the exact restored visible table, before the Builder reread', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-repeated-empty-browser.mjs', import.meta.url), 'utf8');
  const cancelStart = workflow.indexOf("await measure('Cancel saved GROUP output-label edit and restore the original table'");
  const cancelEnd = workflow.indexOf('\n    });', cancelStart);
  const visibleProof = workflow.indexOf('report.groupLabelEditCancel = await verifyGroupedTable(', cancelStart);
  const builderRead = workflow.indexOf("builder = (await api(base + '/builder')).body;", cancelStart);
  assert(cancelStart >= 0 && cancelEnd > cancelStart, 'Saved GROUP Cancel must have an action-to-render measurement');
  assert(visibleProof > cancelStart && visibleProof < cancelEnd, 'Saved GROUP Cancel timing must include exact restored visible headers and rows');
  assert(builderRead > cancelEnd, 'Saved GROUP Builder persistence reread must happen after the render latency checkpoint');
});

test('the independent bounded oracle selects exactly five expanded rows and four COUNT_ROWS groups', () => {
  const result = selectMissingComponentGroupOracle(oracleRows, { project, generation, scanLimit: 6 });
  assert.equal(result.status, 'ready');
  assert.deepEqual(result.selected.map(item => item.id), [
    'observation-positive', 'observation-missing-a', 'observation-missing-b', 'observation-missing-c',
  ]);
  assert.deepEqual(result.expectedComponentRows, [
    { id: 'observation-positive', ordinal: 0, value: 'alpha' },
    { id: 'observation-positive', ordinal: 1, value: 'beta' },
  ]);
  assert.deepEqual(result.expectedGroupRows, [
    ['observation-missing-a', 1],
    ['observation-missing-b', 1],
    ['observation-missing-c', 1],
    ['observation-positive', 2],
  ]);
  assert.equal(result.expectedExpandedRowCount, 5);
  assert.equal(result.expectedGroupRows.length, 4);
  assert(result.selected.slice(1).every(item => item.emptyComponentKind === 'missing'));
  assert(!result.selected.some(item => item.id === 'observation-literal-empty' || item.id === 'observation-null'));
});

test('literal-empty or null Observations cannot substitute for three missing-property owners', () => {
  const result = selectMissingComponentGroupOracle(oracleRows.slice(0, 3).concat(oracleRows[4]), { project, generation, scanLimit: 4 });
  assert.equal(result.status, 'unverified');
  assert.match(result.reason, /three Observations whose component property is absent/);
});

test('the missing-component oracle rejects rows outside the exact scope, duplicate IDs, and a scan over its bound', () => {
  assert.throws(() => selectMissingComponentGroupOracle([scoped('wrong-project', { component: [] })], {
    project: 'another-project', generation,
  }), /another project/);
  assert.throws(() => selectMissingComponentGroupOracle([scoped('wrong-generation', { component: [] })], {
    project, generation: 'another-generation',
  }), /another generation/);
  assert.throws(() => selectMissingComponentGroupOracle([oracleRows[0], oracleRows[0]], { project, generation }), /duplicate ID/);
  assert.throws(() => selectMissingComponentGroupOracle(oracleRows.slice(0, 2), { project, generation, scanLimit: 1 }), /exceeded its 1-row bound/);
});

test('GROUP-removal Cancel requires the saved workspace and exact rendered preview to remain unchanged', () => {
  const workspace = { draftVersion: 8, construction: [{ id: 'group-step', operation: 'GROUP' }] };
  const rows = [
    ['observation-missing-a', 1],
    ['observation-missing-b', 1],
    ['observation-missing-c', 1],
    ['observation-positive', 2],
  ];
  const beforePreview = { headers: ['Observation ID', 'Row count'], rowCount: 4, rows };
  assert.deepEqual(assertGroupRemovalCancelRestoration({
    beforeWorkspace: workspace,
    afterWorkspace: structuredClone(workspace),
    beforePreview,
    afterPreview: structuredClone(beforePreview),
    expectedRows: rows,
  }), { rowCount: 4, rows });
  assert.throws(() => assertGroupRemovalCancelRestoration({
    beforeWorkspace: workspace,
    afterWorkspace: { ...workspace, draftVersion: 9 },
    beforePreview,
    afterPreview: structuredClone(beforePreview),
    expectedRows: rows,
  }), /changed the saved Builder workspace/);
  const changedPreview = structuredClone(beforePreview);
  changedPreview.rows[0][1] = 2;
  assert.throws(() => assertGroupRemovalCancelRestoration({
    beforeWorkspace: workspace,
    afterWorkspace: structuredClone(workspace),
    beforePreview,
    afterPreview: changedPreview,
    expectedRows: rows,
  }), /GROUP removal Cancel did not restore the raw-oracle preview/);
});

test('the no-error gate consumes the official live diagnostic projection and rejects late fixture errors', () => {
  const report = { network: [], errors: [], incidentalErrors: [] };
  const cleanDiagnostics = { console: [], pageErrors: [], networkFailures: [], httpFailures: [], assetFailures: [] };
  includeBrowserDiagnostics(cleanDiagnostics, report);
  assert.deepEqual(assertNoUnexpectedCdaDiagnostics(report), { networkEntries: 0, errorEntries: 0 });

  const failingReport = { network: [], errors: [], incidentalErrors: [] };
  const failingDiagnostics = {
    console: [{ text: 'Unexpected console error', location: 'builder.tsx:1:1' }],
    pageErrors: [], networkFailures: [],
    httpFailures: [{ url: 'http://127.0.0.1:30008/api/failure', status: 500, body: { message: 'failure' } }],
    assetFailures: [],
  };
  failingReport.network.push({ kind: 'http', method: 'GET', url: 'http://127.0.0.1:30008/api/failure', status: 500 });
  includeBrowserDiagnostics(failingDiagnostics, failingReport);
  assert.equal(failingReport.errors.filter(error => error.kind === 'console').length, 1);
  assert.equal(failingReport.errors.filter(error => error.kind === 'http').length, 1);
  assert.throws(() => assertNoUnexpectedCdaDiagnostics(failingReport), /unexpected network failure/);
});
