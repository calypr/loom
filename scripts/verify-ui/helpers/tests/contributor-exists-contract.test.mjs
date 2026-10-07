import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';
import {
  assertCompletePreview,
  buildContributorExistsRawQueryInvocation,
  proposalResponseCandidateMatchesRequest,
  unfilteredRelatedExpandStep,
} from '../../workflows/contributor-exists-workflow.mjs';
import { expectedPreviewColumns } from '../complete-preview-row-projection.mjs';
import { registry, scenarioCaseFor, coverageDrift, hasLifecycleContract } from '../../registry.mjs';

const workflowURL = new URL('../../workflows/contributor-exists-workflow.mjs', import.meta.url);
const specURL = new URL('../../specs/standalone-cda-fields.spec.mjs', import.meta.url);

test('proposal response comparison derives only the changed RELATED_EXPAND record nullability from policy', () => {
  const request = {
    changedStepId: 'related-step',
    candidateConstruction: {
      version: 1,
      steps: [{
        id: 'related-step',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'RELATED_EXPAND', relatedExpand: {
          relatedRecordColumnId: 'related-record', emptyPolicy: 'PRESERVE_PARENT',
        } },
        outputs: [
          { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' },
          { id: 'related-record', name: 'related_id', label: 'Observation FHIR resource ID', type: 'string' },
        ],
      }],
    },
  };
  const before = structuredClone(request);
  const response = structuredClone(request.candidateConstruction);
  response.steps[0].outputs[1].nullable = true;

  assert.equal(proposalResponseCandidateMatchesRequest(request, response), true,
    'the server derives nullable:true for the related-record output under PRESERVE_PARENT');
  assert.deepEqual(request, before, 'expected-response derivation must not mutate the submitted request');

  for (const mutate of [
    candidate => { candidate.steps[0].outputs[1].nullable = false; },
    candidate => { candidate.steps[0].outputs[0].nullable = true; },
    candidate => { candidate.steps[0].outputs[1].label = 'Changed label'; },
    candidate => { candidate.steps[0].operation.relatedExpand.emptyPolicy = 'EXCLUDE'; },
    candidate => { candidate.steps[0].operation.kind = 'FILTER'; },
    candidate => { candidate.steps[0].id = 'different-step'; },
    candidate => { candidate.steps[0].outputs[1].id = 'different-output'; },
  ]) {
    const changedResponse = structuredClone(response);
    mutate(changedResponse);
    assert.equal(proposalResponseCandidateMatchesRequest(request, changedResponse), false,
      'response changes outside the exact server-derived nullable field must remain visible');
  }

  const excludeRequest = structuredClone(request);
  excludeRequest.candidateConstruction.steps[0].operation.relatedExpand.emptyPolicy = 'EXCLUDE';
  const excludeResponse = structuredClone(excludeRequest.candidateConstruction);
  delete excludeResponse.steps[0].outputs[1].nullable;
  assert.equal(proposalResponseCandidateMatchesRequest(excludeRequest, excludeResponse), true,
    'the server omits nullable:false when the exact policy is EXCLUDE');
});

test('registered contributor EXISTS lifecycle emits its named checks and keeps reload assertions inside the timing budget', async () => {
  const scenario = registry.find(entry => entry.id === 'cda-contributor-exists');
  assert(scenario, 'dedicated CDA contributor EXISTS scenario must be registered');
  const contract = scenarioCaseFor(scenario, 'contributor-exists');
  const spec = await readFile(specURL, 'utf8');
  const workflow = await readFile(workflowURL, 'utf8');
  assert.match(spec, /cdaScenarioID: 'cda-contributor-exists', cdaCaseName: 'contributor-exists'/);
  const fixtureOwned = new Set(['CDA watched source and API build stayed unchanged']);
  for (const name of contract.requiredChecks.filter(check => !fixtureOwned.has(check))) {
    assert(workflow.includes(`'${name}'`), `required named assertion is not emitted by the workflow: ${name}`);
  }
  assert.equal(coverageDrift([scenario]).length, 0, 'registered required checks and lifecycle references must be valid');
  assert.equal(hasLifecycleContract(scenario.coverage[0], scenario, [scenario]), true);
  assert.equal(scenario.coverage[0].acceptance.checks.proposal, 5, 'proposal acceptance maps to the scoped ERROR repair proposal');
  assert.equal(scenario.coverage[0].acceptance.checks.savedRows, 6, 'saved-row acceptance maps to PRESERVE_PARENT Apply');
  assert(scenario.requiredTransitions.some(item => item.includes('Cancel the unfiltered ALL_MATCHES baseline proposal')));
  assert.equal(contract.requiredChecks.length, 16);

  const openStart = workflow.indexOf('const open = async (expectedRows, name, verifyReloadState) => {');
  const savedPreviewHelper = workflow.indexOf('const verifySavedPreview = async');
  const remainingTimeoutHelper = workflow.indexOf('const actionTimeRemaining = startedAt =>');
  const applyProposalStart = workflow.indexOf('const applyProposal = async');
  assert(savedPreviewHelper >= 0 && savedPreviewHelper < openStart,
    'saved preview validation must be in the same outer scope as reload callers');
  assert(remainingTimeoutHelper >= 0 && remainingTimeoutHelper < applyProposalStart,
    'remaining action timeout must be in the same outer scope as Apply callers');
  const actionRecord = workflow.indexOf('recordAction(name, startedAt, { rowCount: rows.length, receiptId: savedPreview.receiptId });', openStart);
  assert(openStart >= 0 && actionRecord > workflow.indexOf('await verifySavedPreview({ expectedRows, name, startedAt, requestStart, savedBuilder: builder });', openStart)
    && actionRecord > workflow.indexOf('await verifyReloadState(rows, builder, savedPreview);', openStart),
    'reload action timing must include complete saved-preview and post-render Builder validation');
  for (const [marker, check] of [
    ["const preserveReloadRows = await open(existsPreserveRows, 'reload-exists-preserve-parent', async (rows, savedBuilder) => {", 'PRESERVE_PARENT rows, EXISTS policy, and source binding survive reload'],
    ["const excludeReloadRows = await open(existsExcludeRows, 'reload-edited-exists-exclude', async (rows, savedBuilder) => {", 'EXCLUDE rows, policy, and source binding survive reload'],
    ["const restoredSourceRows = await open(baselineRows, 'reload-restored-source-table', async (rows, savedBuilder) => {", 'Restored source schema, population, and exact baseline rows survive reload'],
  ]) {
    const start = workflow.indexOf(marker);
    const end = workflow.indexOf('\n});', start);
    assert(start >= 0 && end > start, `reload validator callback is missing: ${marker}`);
    const callback = workflow.slice(start, end);
    assert(callback.includes('savedBuilder'), `reload validation must consume saved Builder state inside the timed action: ${marker}`);
    assert(callback.includes(`recordCheck('persistence', '${check}'`), `reload check must finish within its timed action: ${marker}`);
  }
  const restoreStart = workflow.indexOf("const restoredSourceRows = await open(baselineRows, 'reload-restored-source-table', async (rows, savedBuilder) => {");
  const restoreCallback = workflow.slice(restoreStart);
  for (const field of ['construction?.steps', 'restoredDocument.columns', 'restoredDocument.rows', 'restoredDocument.population']) {
    assert(restoreCallback.includes(field), `restoration reload must validate exact ${field}`);
  }
  assert(!workflow.includes('const isDeepEqual = isDeepStrictEqual;') && !workflow.includes('isDeepEqual('),
    'use the imported strict comparator directly');
  assert.match(workflow, /assertCompletePreview\(proposalResponse\.preview/,
    'proposal preview must require complete raw-response rows and exact output/receipt identity');
  assert.match(workflow, /proposalResponseCandidateMatchesRequest\(proposalRequest, proposalResponse\.candidateConstruction\)/,
    'proposal receipt must bind the response to the exact submitted candidate plus its documented related-expand schema derivation');
  assert.match(workflow, /expectedPreviewColumns\(proposalResponse\.candidateConstruction, original\.columns\)/,
    'proposal preview must be checked against the submitted candidate output schema');
  assert.match(workflow, /expectedPreviewColumns\(savedDocument\.construction, savedDocument\.columns\)/,
    'saved preview must be checked against the persisted Builder output schema');

  const savedPreviewIndex = workflow.indexOf('const verifySavedPreview = async');
  const savedPreviewDeadline = workflow.indexOf('recordAction(name, startedAt, { rowCount: rows.length, receiptId: savedPreview.receiptId });', openStart);
  assert(savedPreviewIndex >= 0 && savedPreviewIndex < openStart
    && savedPreviewDeadline > workflow.indexOf('await verifyReloadState(rows, builder, savedPreview);', openStart),
    'reload validation must share the caller scope and finish before the timed action closes');
  const applyStart = workflow.indexOf('const applyProposal = async');
  const applyDeadline = workflow.indexOf('recordAction(name, startedAt, { rowCount: rows.length, proposalId, receiptId: savedPreview.receiptId });', applyStart);
  assert(applyDeadline > workflow.indexOf('const applyResponse = requestCapture.rawResponseBody(applyEntry);', applyStart)
    && applyDeadline > workflow.indexOf("builder = await api(base + '/builder');", applyStart)
    && applyDeadline > workflow.indexOf('await verifySavedPreview({ expectedRows, name, startedAt, requestStart, savedBuilder: builder });', applyStart),
    'Apply action timing must include exact Apply response, saved Builder CAS, and complete saved preview');
  const baselineCancel = workflow.indexOf("recordAction('cancel-all-matching-preview'");
  assert(baselineCancel > workflow.indexOf("recordCheck('persistence', 'Cancel preserves the exact source document", baselineCancel - 1200),
    'baseline Cancel timing must include the saved-workspace and raw-row checks');
  assert.match(workflow, /import \{[\s\S]*?classifyPendingRelatedExpandChoicesAfterProposalCancel,[\s\S]*?proposalCancelActionSelector,[\s\S]*?snapshotPendingRelatedExpandChoices,[\s\S]*?\} from '\.\.\/helpers\/related-expand-cancel\.mjs'/,
    'baseline Cancel must use the strict exact-request cancellation helper');
  const baselineCancelStart = workflow.indexOf("const beforeBaselineCancel = await api(base + '/builder');");
  const baselineCancelEnd = workflow.indexOf("panel = await startRelatedExpand();", baselineCancelStart);
  const baselineCancelSource = workflow.slice(baselineCancelStart, baselineCancelEnd);
  assert(baselineCancelStart >= 0 && baselineCancelEnd > baselineCancelStart);
  assert(!baselineCancelSource.includes('withExpectedCancellations'),
    'baseline Cancel must not arm a broad path-wide cancellation scope');
  assert(baselineCancelSource.indexOf('snapshotPendingRelatedExpandChoices(') < baselineCancelSource.indexOf('await nativeClick(page, routeChoiceCancellationAction'),
    'the exact pending request snapshot must precede the explicit Cancel');
  assert(baselineCancelSource.indexOf('await waitForDOM(page, args => Boolean(!document.querySelector(args.__template0))')
    < baselineCancelSource.indexOf('classifyPendingRelatedExpandChoicesAfterProposalCancel('),
    'the proposal/editor close waits must finish before classifying the abort');
  assert(baselineCancelSource.indexOf("{ __template0: relatedExpandEditorSelector }")
    < baselineCancelSource.indexOf('classifyPendingRelatedExpandChoicesAfterProposalCancel('),
    'classification must follow observation that the RelatedExpand editor itself is unmounted');
  assert.match(baselineCancelSource, /routeChoiceCancellations\.map\(item => item\.browserRequestId\)\.sort\(\),[\s\S]*?routeChoiceCancelledBrowserRequestIDs/,
    'the expected-cancellation ledger must equal requests returned from the exact pending snapshot');
  assert.match(baselineCancelSource, /draftVersion: beforeBaselineCancel\.draftVersion,[\s\S]*?draftDigest: beforeBaselineCancel\.draftDigest,[\s\S]*?stageId: 'source_projection'/,
    'the pre-Cancel snapshot must bind the exact saved draft and selected input stage');
  const applySource = workflow.slice(applyProposalStart, workflow.indexOf('const expectedErrorProposal = async', applyProposalStart));
  assert(!applySource.includes('withExpectedCancellations')
    && !applySource.includes('classifyPendingRelatedExpandChoicesAfterProposalCancel'),
    'normal Apply and saved-preview verification must not inherit the Cancel exception');
  assert.match(workflow, /networkFailures\.filter\(failure => !failure\.expectedCancellation\)/,
    'unclassified Apply-time network failures must remain fatal');
  const removalCancel = workflow.indexOf("recordAction('cancel-exists-remove-to-render'");
  assert(removalCancel > workflow.indexOf("recordCheck('persistence', 'Cancel removal preserves saved EXCLUDE rows", removalCancel - 1200),
    'removal Cancel timing must include the saved-workspace and raw-row checks');
  const expectedErrorCheck = workflow.indexOf("recordCheck('usability', 'Scoped ERROR validation exposes enabled PRESERVE_PARENT and EXCLUDE repair choices'");
  const expectedErrorStart = workflow.indexOf('const expectedErrorProposal = async');
  const expectedErrorDeadline = workflow.indexOf('const durationMs = Date.now() - startedAt;', expectedErrorCheck);
  assert(expectedErrorDeadline > workflow.indexOf('await cda.flushHttpDiagnostics({', expectedErrorStart)
    && expectedErrorDeadline > workflow.indexOf('await requestCapture.flush({', expectedErrorStart)
    && expectedErrorDeadline > expectedErrorCheck,
    'ERROR timing must include the fixture and request-capture drains plus HTTP evidence');
  const finalDrain = workflow.lastIndexOf('await Promise.all([');
  const finalDiagnosticsDrain = workflow.lastIndexOf('cda.flushHttpDiagnostics({ timeoutMs: 5_000 })');
  const finalTrackerDrain = workflow.lastIndexOf('requestCapture.flush({ timeoutMs: 5_000 })');
  const finalUnexpectedErrors = workflow.lastIndexOf("assert.deepEqual(report.errors.filter((failure) => !failure.expected)");
  assert(finalDrain >= 0 && finalDiagnosticsDrain > finalDrain && finalTrackerDrain > finalDrain
    && finalUnexpectedErrors > Math.max(finalDiagnosticsDrain, finalTrackerDrain),
    'final diagnostics assertions must follow the concurrent bounded fixture and tracker drains');
  const finallyStart = workflow.lastIndexOf('  } finally {');
  const finallySource = workflow.slice(finallyStart);
  assert(finallyStart >= 0 && /report\.status = 'failed'/.test(finallySource)
    && /if \(requestFlushError\) throw requestFlushError/.test(finallySource),
    'a final request-capture drain failure must mark the report failed and remain fatal');
});

test('complete preview requires exact authored columns and every expected row key', () => {
  const construction = { steps: [{ outputs: [
    { name: 'patient', label: 'Patient ID' },
    { name: 'observation', label: 'Observation ID' },
  ] }] };
  const expectedColumns = expectedPreviewColumns(construction);
  const expectedRows = [['patient-a', 'observation-a'], ['patient-b', '—']];
  const completePreview = {
    outputId: 'output-a', receiptId: 'receipt-a', sampled: false, partialValidation: false, rowCount: 2,
    columns: expectedColumns,
    rows: [
      { __loom_row_id: 'row-1', patient: 'patient-a', observation: 'observation-a' },
      { __loom_row_id: 'row-2', patient: 'patient-b', observation: null },
    ],
  };
  assert.deepEqual(assertCompletePreview(completePreview, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a', label: 'focused complete preview',
  }), { outputId: 'output-a', receiptId: 'receipt-a', rowCount: 2, sampled: false, partialValidation: false });
  assert.deepEqual(expectedPreviewColumns({ steps: [] }, [
    { column: 'patient', label: 'Patient ID' },
  ]), [{ column: 'patient', label: 'Patient ID' }], 'zero-step output schema comes from saved source columns');
  assert.throws(() => assertCompletePreview({ ...completePreview, sampled: true }, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /sampled or completeness is unknown/);
  assert.throws(() => assertCompletePreview({ ...completePreview, partialValidation: true }, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /partial validation/);
  assert.throws(() => assertCompletePreview({ ...completePreview, outputId: 'output-b' }, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /another output/);
  assert.throws(() => assertCompletePreview({ ...completePreview, receiptId: 'receipt-b' }, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /another receipt/);
  assert.throws(() => assertCompletePreview({ ...completePreview, rowCount: 3 }, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /row count differs/);
  assert.throws(() => assertCompletePreview({ ...completePreview, rows: [completePreview.rows[0]] }, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /row payload is incomplete/);
  const missingRowKey = { ...completePreview, rows: completePreview.rows.map(row => ({ ...row })) };
  delete missingRowKey.rows[1].observation;
  assert.throws(() => assertCompletePreview(missingRowKey, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /missing authored key observation/);
  const missingSchemaKey = { ...completePreview, columns: completePreview.columns.slice(0, 1) };
  assert.throws(() => assertCompletePreview(missingSchemaKey, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /exact authored output key order/);
  const extraRowKey = { ...completePreview, rows: completePreview.rows.map(row => ({ ...row })) };
  extraRowKey.rows[0].unexpected = 'must fail';
  assert.throws(() => assertCompletePreview(extraRowKey, {
    expectedRows, expectedColumns, outputId: 'output-a', receiptId: 'receipt-a',
  }), /only the internal row identity and exact authored columns/);
  assert.throws(() => assertCompletePreview(completePreview, {
    expectedRows: Array.from({ length: 25 }, (_, index) => [`patient-${index}`]), expectedColumns,
    outputId: 'output-a', receiptId: 'receipt-a',
  }), /reach the native preview cap/);
});

test('captured baseline request uses its raw top-level body and scoped Arango calls use in-container credentials', async () => {
  const requestBody = {
    outputId: 'output-a',
    candidateConstruction: { steps: [{ id: 'related-step', operation: {
      kind: 'RELATED_EXPAND', relatedExpand: { contributorRule: { policy: 'ALL_MATCHES' } },
    } }] },
  };
  const step = unfilteredRelatedExpandStep(requestBody);
  assert.equal(step.id, 'related-step');
  assert.throws(() => unfilteredRelatedExpandStep({ body: requestBody }), /top-level request body/);

  const query = 'FOR p IN Patient FILTER p.project == "loom_dev_cda_fhir" AND p.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN p.id';
  const invocation = buildContributorExistsRawQueryInvocation('owned-arango', query);
  const shellInvocation = buildArangoShellInvocation({
    container: 'owned-arango',
    script: `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
    database: 'loom_dev',
  });
  assert.deepEqual(invocation, shellInvocation);
  assert.equal(invocation.command, 'rtk');
  assert.deepEqual(invocation.args.slice(0, 6), ['proxy', 'docker', 'exec', 'owned-arango', 'sh', '-lc']);
  const shellCommand = invocation.args[6];
  assert.match(shellCommand, /--server\.endpoint tcp:\/\/127\.0\.0\.1:8529/);
  assert.match(shellCommand, /--server\.username root/);
  assert.match(shellCommand, /--server\.password "\$ARANGO_ROOT_PASSWORD"/);
  assert.match(shellCommand, /--server\.database 'loom_dev'/);
  assert.match(shellCommand, /--javascript\.execute-string /);
  assert(shellCommand.includes(`db._query(${JSON.stringify(query)}).toArray()`));
  assert(!invocation.args.some(argument => argument.includes('test-password')));

  const workflow = await readFile(workflowURL, 'utf8');
  assert.match(workflow, /buildArangoShellInvocation\(\{ container, script, database: 'loom_dev' \}\)/);
  assert.match(workflow, /const invocation = buildContributorExistsRawQueryInvocation\(arangoContainer, query\)/);
  assert.match(workflow, /spawnSync\(invocation\.command, invocation\.args,\s*\{ encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 \}\)/);
  assert.match(workflow, /const baselineStep = unfilteredRelatedExpandStep\(baselineRequest\)/);
  assert.doesNotMatch(workflow, /baselineRequest\.body\.candidateConstruction/);
});
