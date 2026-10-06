import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  assertPreserveParentRelatedEdit,
  authoredStepOperations,
  expectedPreserveParentRelatedEdit,
} from '../related-policy-construction.mjs';
import { selectRelatedRouteOption } from '../related-route-disclosure.mjs';
import { coverageDrift, hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';

const scenarioID = 'cda-upstream-related-edit-cascade';
const caseName = 'upstream-edit-cascade';
const requiredChecks = [
  'bounded raw oracle selects one root with an upstream match and 1–24 exact composed rows at every stage',
  'native saved Related editor exposes EXCLUDE and selects PRESERVE_PARENT',
  'upstream edit proposal is ready with dependent steps and exact raw rows',
  'Canceling the upstream policy edit preserves EXCLUDE, downstream steps, draft identity, and complete raw rows',
  'reopened upstream edit proposal retains every dependent step with exact raw rows',
  'upstream edit preserves dependent identities and operations; only policy-derived nullability changes',
  'Apply saves the exact upstream edit with stable dependent step identities',
  'applied upstream edit rows match the complete raw multiset and full row count',
  'upstream Related edit reload preserves bound draft and complete raw rows within five seconds',
  'Canceling the upstream cascade preserves saved dependent construction and draft identity',
  'root Related removal proposals name every dependent step before Apply',
  'root cascade Apply and reload restore the exact rooted starting row within five seconds',
];

test('dedicated COMPOSED_RELATED upstream edit case binds a complete untested lifecycle contract', async () => {
  const scenario = registry.find(entry => entry.id === scenarioID);
  assert(scenario, 'the dedicated upstream Related edit scenario must be registered');
  const contract = scenarioCaseFor(scenario, caseName);
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-cohort.spec.mjs');
  assert.deepEqual(contract.requiredChecks, requiredChecks);
  assert.equal(new Set(contract.requiredChecks).size, requiredChecks.length);

  const coverage = scenario.coverage.find(entry => entry.feature === 'CDA upstream Related policy edit preserves complete dependent rows and cascade removal restores the rooted source');
  assert(coverage, 'the upstream edit/cascade feature must remain in the coverage inventory');
  assert.equal(coverage.status, 'untested', 'a staged contract cannot claim a browser pass');
  assert.equal(hasLifecycleContract(coverage, scenario), true);
  assert.deepEqual(coverage.acceptance.checks, {
    choice: 1, proposal: 4, cancel: 3, apply: 6, savedRows: 7, reload: 8, edit: 5, restoration: 11,
  });
  assert.deepEqual(coverageDrift(registry), []);

  const spec = await readFile(new URL('../../specs/standalone-cda-cohort.spec.mjs', import.meta.url), 'utf8');
  assert.match(spec, /fixtureOptions\('upstream-edit-cascade', 'cda-upstream-related-edit-cascade'\)/);
  assert.match(spec, /test\.describe\('CDA upstream Related edit and cascade'/);
  assert.match(spec, /test\('edit a saved upstream Related expansion while preserving dependent rows, then cascade-remove it'/);
  assert.match(spec, /composedRowLineageWorkflow\(\{ page, cda, lineageMode: 'COMPOSED_RELATED' \}\)/);
  for (const mode of ['COMPOSED_RELATED', 'DIRECT_PIVOT', 'DIRECT_PIVOT_SHARED_CONTRIBUTOR', 'DIRECT_GROUP_COUNT_PIVOT']) {
    assert(spec.includes(`  '${mode}',`), `legacy lineage mode ${mode} must remain registered in the existing spec loop`);
  }

  const workflow = await readFile(new URL('../../workflows/verify-cda-composed-row-lineage-browser.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /report\.scenario === 'cda-upstream-related-edit-cascade'\s*&& report\.caseName === 'upstream-edit-cascade'/);
  assert.match(workflow, /if \(focusedUpstreamRelatedRemovalCase\) \{\s*assert\(stage1Rows\.length <= exactPreviewRowLimit/);
  assert.match(workflow, /else \{\s*assert\(stage3Rows\.length <= 1000, 'Keep the admitted composed chain bounded'\)/);
  assert.match(workflow, /upstreamEditReloadElapsedMs <= 5000/);
  assert.match(workflow, /if \(focusedUpstreamRelatedRemovalCase\) \{\s*const editedUpstreamStepID/);
  assert.match(workflow, /const authoredConstruction = doc\(builder\)\.construction/);
  assert.match(workflow, /\.\.\.\(focusedUpstreamRelatedRemovalCase \? \{ upstreamPolicyEdit: \{/);
  assert.match(workflow, /\.\.\.\(focusedUpstreamRelatedRemovalCase \? \{ strictPreviewMaxRows: exactPreviewRowLimit \} : \{\}\)/);
  assert.match(workflow, /const completeCascadeOptions = focusedUpstreamRelatedRemovalCase \? \{ requireCompletePreview: true \} : \{\}/);
  assert.match(workflow, /if \(focusedUpstreamRelatedRemovalCase\) \{\s*await apply\(basePreviewRows, \{ requireCompletePreview: true/);
  assert.match(workflow, /\} else \{\s*await apply\(basePreviewRows\);/);
  assert.match(workflow, /if \(focusedUpstreamRelatedRemovalCase\) \{\s*const savedCascadeRestoration/);
  assert.match(workflow, /\} else \{\s*await open\(\);\s*builder = await api\(base \+ '\/builder'\);\s*assert\.deepEqual\(doc\(builder\)\.construction, zeroStepConstruction/);
  assert.match(workflow, /const upstreamEditCancel = await cancelRemoval\(upstreamEditBaseline, 'Upstream policy edit', stage3PreviewRows,[\s\S]*?\{ requireCompletePreview: true \}\)/);
  assert.match(workflow, /expectedPreserveParentRelatedEdit\(doc\(upstreamEditBaseline\)\.construction, 0\)/);
  assert.match(workflow, /assertPreserveParentRelatedEdit\(upstreamEdit\.candidateConstruction, upstreamEditExpectation\)/);
  assert.match(workflow, /assertPreserveParentRelatedEdit\(confirmedUpstreamEdit\.candidateConstruction, upstreamEditExpectation\)/);
  assert.match(workflow, /assertPreserveParentRelatedEdit\(doc\(saved\)\.construction, upstreamEditExpectation\)/);
  assert.match(workflow, /assertPreserveParentRelatedEdit\(doc\(builder\)\.construction, upstreamEditExpectation\)/);
  assert.match(workflow, /doc\(builder\)\.construction\.steps\.slice\(1\), authoredDownstreamSteps/,
    'Cancel must still compare downstream schemas with the unchanged pre-edit baseline');
  assert.match(workflow, /upstreamEditExpectation\.relatedRecordColumnId/);
  assert.match(workflow, /upstreamEditExpectation\.propagatedOutputStepIDs/);
  assert.match(workflow, /doc\(builder\)\.construction\.steps\[0\]\.operation\.relatedExpand\.emptyPolicy, 'EXCLUDE'/);
  assert.match(workflow, /assertExactBoundedRows\(mounted, expectedRows, 'Applied upstream policy edit'\)/);
  assert.match(workflow, /assert\.equal\(rawResponse\.preview\?\.sampled, false/);
  assert.match(workflow, /assert\.equal\(previewResponse\?\.sampled, false/);
  assert.match(workflow, /assert\.equal\(reloadedPreview\.sampled, false/);
  assert.match(workflow, /assert\.equal\(restoredPreview\.sampled, false/);
  assert.match(workflow, /assert\.equal\(reloadedPreview\.rowCount, stage3PreviewRows\.length/);
  assert.match(workflow, /assertCompletePreviewRows\(rawResponse\.preview,[\s\S]*?expectedPreviewColumns\(rawResponse\.candidateConstruction, doc\(builder\)\.columns\)/,
    'the focused proposal checkpoint must compare API object values through the candidate output schema');
  assert.match(workflow, /assertCompletePreviewRows\(previewResponse,[\s\S]*?expectedPreviewColumns\(doc\(completeSavedState\)\.construction, doc\(completeSavedState\)\.columns\)/,
    'the focused Apply checkpoint must compare API object values through the saved output schema');
  assert.match(workflow, /assertCompletePreviewRows\(preview,[\s\S]*?expectedPreviewColumns\(doc\(savedState\)\.construction, doc\(savedState\)\.columns\)/,
    'the focused Cancel checkpoint must compare API object values through the saved output schema');
  assert.match(workflow, /preview\.receiptId, active\.receiptId,[\s\S]*?preview\.outputId, outputId/,
    'the focused Cancel checkpoint must bind the full API response to the rendered receipt and output');
  assert.match(workflow, /assertCompletePreviewRows\(reloadedPreview,[\s\S]*?expectedPreviewColumns\(doc\(builder\)\.construction, doc\(builder\)\.columns\)/,
    'the focused edit reload must compare API object values through the saved output schema');
  assert.match(workflow, /assertCompletePreviewRows\(restoredPreview,[\s\S]*?expectedPreviewColumns\(doc\(builder\)\.construction, doc\(builder\)\.columns\)/,
    'the focused cascade restoration must compare API object values through the rooted source schema');
  const completeApplySource = workflow.slice(workflow.indexOf('const apply = async'), workflow.indexOf('const open = async'));
  assert.match(completeApplySource, /previewResponse\.receiptId,[\s\S]*?activeCompletePreview\.receiptId/,
    'focused Apply must bind the API preview rows to the same ready rendered receipt');
  assert.match(completeApplySource, /activeCompletePreview\.draftVersion, String\(completeSavedState\.draftVersion\)/);
  assert.match(completeApplySource, /activeCompletePreview\.draftDigest, completeSavedState\.draftDigest/);
  assert.match(workflow, /import \{ assertCompletePreviewRows, expectedPreviewColumns \} from '\.\.\/helpers\/complete-preview-row-projection\.mjs'/);
  assert.match(workflow, /import \{\s*classifyPendingRelatedExpandChoicesAfterProposalCancel,[\s\S]*?snapshotPendingRelatedExpandChoices,\s*\} from '\.\.\/helpers\/related-expand-cancel\.mjs'/);
  const cancelSource = workflow.slice(workflow.indexOf('const cancelRemoval = async'), workflow.indexOf('const chooseRoute ='));
  assert.match(cancelSource, /focusedUpstreamRelatedRemovalCase && name === 'Upstream policy edit' && requireCompletePreview/,
    'choice-request cancellation classification must be limited to the exact dedicated saved-edit Cancel');
  assert.ok(cancelSource.indexOf('snapshotPendingRelatedExpandChoices(') < cancelSource.indexOf('await clickUI(proposalCancelActionSelector)'),
    'the pending exact choices request must be snapshotted before the native Cancel action');
  assert.ok(cancelSource.indexOf('await waitUI(`!document.querySelector($\{JSON\.stringify\(relatedEditorSelector\)\})`)') < cancelSource.indexOf('classifyPendingRelatedExpandChoicesAfterProposalCancel('),
    'the editor must be proven closed before classifying an aborted choice read');
  assert.match(cancelSource, /stageId: 'source_projection'/);
  assert.match(cancelSource, /draftVersion: baseline\.draftVersion, draftDigest: baseline\.draftDigest/);
  assert.match(workflow, /const isExactClassifiedChoiceAbort = requestID && classifiedChoiceRequestIDs\.has\(requestID\)[\s\S]*?error\.error === 'net::ERR_ABORTED' \|\| error\.errorText === 'net::ERR_ABORTED'[\s\S]*?error\.expected === true[\s\S]*?expectedCancellation\?\.browserRequestId === requestID/,
    'only the exact helper-classified request may be removed from focused error accounting');
  assert.match(workflow, /verifySavedState: async saved => \{[\s\S]*?assertPreserveParentRelatedEdit\(doc\(saved\)\.construction, upstreamEditExpectation\)/);
  assert.match(workflow, /verifySavedState: async saved => \{[\s\S]*?assert\.deepEqual\(doc\(saved\)\.construction, zeroStepConstruction/);
  const applySource = workflow.slice(workflow.indexOf('const apply = async'), workflow.indexOf('const open = async'));
  assert.ok(applySource.indexOf("builder = await api(base + '/builder')") < applySource.indexOf('await verifySavedState(builder'),
    'the Apply checkpoint must fetch persisted Builder state before its saved-state verifier');
  assert.ok(applySource.indexOf('await verifySavedState(builder') < applySource.indexOf("assert(elapsedMs <= 5000"),
    'the Apply checkpoint must stop its five-second timer only after exact saved-state assertions');
  const rawQuerySource = workflow.slice(workflow.indexOf('const rawQuery ='), workflow.indexOf('const uniqueBy ='));
  assert.ok(rawQuerySource.indexOf('oracleQueries.push(query)') < rawQuerySource.indexOf('spawnSync('),
    'the attempted AQL must be retained before the process starts');
  assert.ok(rawQuerySource.indexOf('oracleQueryAttempts.push(attempt)') < rawQuerySource.indexOf('spawnSync('),
    'the first-query attempt record must be attached before the process starts');
  assert.match(rawQuerySource, /attempt\.spawnError = safeOracleDiagnostic\(result\.error\.message\)/);
  assert.match(rawQuerySource, /attempt\.signal = result\.signal/);
  assert.match(rawQuerySource, /attempt\.stdoutExcerpt = safeOracleDiagnostic\(result\.stdout\)/);
  assert.match(rawQuerySource, /attempt\.stderrExcerpt = safeOracleDiagnostic\(result\.stderr\)/);
  assert.match(workflow, /const stage1Rows = relatedRows\(rootParents, stageOneBoundedQuery[\s\S]*?const stage2Rows = relatedRows\(stage1Rows, stageTwoBoundedQuery[\s\S]*?const stage3Rows = relatedRows\(stage2Rows, stageThreeBoundedQuery/);
  assert.match(workflow, /COLLECT parentPath = prior\.pathKey, terminalID = specimen\._id INTO bridgeIDs = observation\._id\s+SORT parentPath, terminalID\s+LIMIT \$\{candidateStageRowSentinel\}\s+LET terminal = DOCUMENT\(terminalID\)[\s\S]*?LET bridgeID = MIN\(bridgeIDs\)/);
  assert.match(workflow, /stage1Count: LENGTH\(stage1\), stage2Count: LENGTH\(stage2\), stage3Count: LENGTH\(stage3\)/);
  assert.match(workflow, /selectBoundedComposedRootCandidates\(evaluatedRootCandidates, rootCandidateLimit\)[\s\S]*?const materializeSelectedCandidate/);
  assert.match(workflow, /const cascadeRestorationReloadStarted = Date\.now\(\);\s*await open\(\);[\s\S]*?assertCompletePreviewRows\(restoredPreview/);
  assert.match(workflow, /cascadeRestorationReloadElapsedMs <= 5000/);
  assert.match(workflow, /root-cascade-restoration-reload-full-row-verification/);
  const checkPositions = requiredChecks.map(name => {
    const position = workflow.indexOf(`recordFocusedUpstreamCheck('${name}'`);
    assert(position >= 0, `workflow must emit required check: ${name}`);
    return position;
  });
  assert.deepEqual(checkPositions, [...checkPositions].sort((left, right) => left - right),
    'the workflow must emit registered required checks in their declared order');

  const transitionSource = await readFile(new URL('../../../../internal/explorer/authoringv2/construction_change.go', import.meta.url), 'utf8');
  assert.match(transitionSource, /func recalculateCandidateStages[\s\S]*?outputs, err := rebuildStageColumns\(\*step, inputColumns, authoredSourceIDs\)[\s\S]*?step\.Outputs = outputs/,
    'a changed upstream step must rebuild each dependent output schema from its current input');
  assert.match(transitionSource, /case ConstructionOperationRelatedExpand:[\s\S]*?outputs = append\(outputs, input\.\.\.\)[\s\S]*?column\.Nullable = step\.Operation\.RelatedExpand\.EmptyPolicy == ConstructionExpandEmptyPreserveParent[\s\S]*?outputs = append\(outputs, column\)/,
    'RELATED_EXPAND derives only its produced related-record column nullable flag from PRESERVE_PARENT while carrying inputs forward');
  const recipeValidation = await readFile(new URL('../../../../internal/dataframe/recipe/construction.go', import.meta.url), 'utf8');
  assert.match(recipeValidation, /wantNullable := related\.EmptyPolicy == ExpansionPreserveParent[\s\S]*?if output\[related\.RelatedRecordColumnID\]\.Nullable != wantNullable/,
    'recipe validation requires the related-record output nullability to match the empty policy');

  const proposalSource = workflow.slice(workflow.indexOf('const proposal = async'), workflow.indexOf('const apply = async'));
  assert.match(proposalSource, /proposalPreviewReadinessExpression\(outputId, renderedRowCount\)/,
    'proposal readiness must bind the summary to its sibling preview receipt, output, and row count');
  assert.match(proposalSource, /document\.querySelectorAll\(\$\{JSON\.stringify\(previewSelector\)\}\)\.length===1/,
    'the proposal preview selector must be unique before reading rows');
  assert.match(proposalSource, /const previews = page\.getByTestId\('construction-proposal-preview'\)/,
    'proposal rows must be read from the sibling preview component');
  assert.match(proposalSource, /assert\.equal\(await previews\.count\(\), 1/);
  assert.match(proposalSource, /result\.receiptId, rawResponse\?\.preview\?\.receiptId/);
  assert.match(proposalSource, /result\.outputId, rawResponse\?\.preview\?\.outputId/);
  const panelInspection = proposalSource.slice(proposalSource.indexOf('const panel ='), proposalSource.indexOf('const previews ='));
  assert.doesNotMatch(panelInspection, /construction-proposal-preview-row/,
    'the summary panel must not be treated as the preview table container');
  assert.match(proposalSource, /tbody tr\[data-testid="construction-proposal-preview-row"\]/,
    'rendered table row identity must be read from the preview sibling');
  const routeSource = workflow.slice(workflow.indexOf('const chooseRoute'), workflow.indexOf('const expand'));
  assert.match(routeSource, /const remaining = \(\) => Math\.max\(1, 5000 - \(Date\.now\(\) - started\)\)/,
    'route discovery and selection must share the existing five-second action budget');
  assert.match(routeSource, /const selector = `\$\{panel\} input\[aria-label=\$\{JSON\.stringify\(label\)\}\]`/,
    'the route choice selector must be scoped to the exact Related editor and accessible label');
  assert.ok(routeSource.indexOf('waitForRouteOrDisclosure:') < routeSource.indexOf('routeIsMounted:'),
    'asynchronous choices must finish mounting a route or disclosure before checking route absence');
  assert.match(routeSource, /openDisclosure: \(\) => clickUI\(`\$\{disclosureSelector\} summary`/,
    'a missing route choice must open the native editor disclosure');
  assert.ok(routeSource.indexOf('waitForRouteEnabled:') < routeSource.indexOf('clickRoute:'),
    'the exact route must become enabled before it is clicked');
});

test('PRESERVE_PARENT permits only its carried related-record nullability transition', () => {
  const stageOutputs = extra => [
    { id: 'source_id', name: 'source_id', label: 'Source ID', type: 'string' },
    { id: 'related_patient_id', name: 'related_patient_id', label: 'Patient FHIR resource ID', type: 'string' },
    ...extra,
  ];
  const baseline = {
    version: 1,
    steps: [
      {
        id: 'related-root-patient', inputs: [{ kind: 'SOURCE_PROJECTION' }], rowValues: [],
        operation: { kind: 'RELATED_EXPAND', relatedExpand: { emptyPolicy: 'EXCLUDE', relatedRecordColumnId: 'related_patient_id' } },
        outputs: stageOutputs([]),
      },
      {
        id: 'related-patient-specimen', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'related-root-patient' }], rowValues: [],
        operation: { kind: 'RELATED_EXPAND', relatedExpand: { emptyPolicy: 'EXCLUDE', relatedRecordColumnId: 'related_specimen_id' } },
        outputs: stageOutputs([{ id: 'related_specimen_id', name: 'related_specimen_id', label: 'Specimen ID', type: 'string' }]),
      },
      {
        id: 'related-specimen-patient', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'related-patient-specimen' }], rowValues: [],
        operation: { kind: 'RELATED_EXPAND', relatedExpand: { emptyPolicy: 'EXCLUDE', relatedRecordColumnId: 'related_patient_id_2' } },
        outputs: stageOutputs([
          { id: 'related_specimen_id', name: 'related_specimen_id', label: 'Specimen ID', type: 'string' },
          { id: 'related_patient_id_2', name: 'related_patient_id_2', label: 'Patient FHIR resource ID', type: 'string' },
        ]),
      },
    ],
  };
  const expectation = expectedPreserveParentRelatedEdit(baseline, 0);
  assert.equal(expectation.relatedRecordColumnId, 'related_patient_id');
  assert.deepEqual(expectation.propagatedOutputStepIDs, baseline.steps.map(step => step.id));
  for (const step of expectation.construction.steps) {
    const related = step.outputs.find(column => column.id === expectation.relatedRecordColumnId);
    assert.deepEqual(related, {
      id: 'related_patient_id', name: 'related_patient_id', label: 'Patient FHIR resource ID', type: 'string', nullable: true,
    });
  }
  assert.deepEqual(authoredStepOperations(expectation.construction.steps.slice(1)),
    authoredStepOperations(baseline.steps.slice(1)), 'dependent operation payloads and step identities stay authored');
  assertPreserveParentRelatedEdit(expectation.construction, expectation);

  const unrelatedNullableChange = JSON.parse(JSON.stringify(expectation.construction));
  unrelatedNullableChange.steps[1].outputs.find(column => column.id === 'source_id').nullable = true;
  assert.throws(() => assertPreserveParentRelatedEdit(unrelatedNullableChange, expectation),
    'the edit must reject nullability changes on every unrelated output');

  const unrelatedMetadataChange = JSON.parse(JSON.stringify(expectation.construction));
  unrelatedMetadataChange.steps[2].outputs.find(column => column.id === 'related_patient_id').label = 'Changed label';
  assert.throws(() => assertPreserveParentRelatedEdit(unrelatedMetadataChange, expectation),
    'the edit must reject unrelated metadata changes even on the propagated output');

  const operationChange = JSON.parse(JSON.stringify(expectation.construction));
  operationChange.steps[1].operation.relatedExpand.route = [{ edgeId: 'unexpected' }];
  assert.throws(() => assertPreserveParentRelatedEdit(operationChange, expectation),
    'dependent operation payloads must remain exact');
});

test('related route chooser opens the disclosure only for an absent exact route', async () => {
  const multihopEvents = [];
  let multihopRouteMounted = false;
  let multihopDisclosureMounted = false;
  let multihopDisclosureOpen = false;
  await selectRelatedRouteOption({
    waitForRouteOrDisclosure: async () => {
      multihopEvents.push('wait for exact radio or disclosure');
      assert.equal(multihopRouteMounted, false);
      assert.equal(multihopDisclosureMounted, false);
      multihopDisclosureMounted = true;
    },
    routeIsMounted: async () => {
      multihopEvents.push('check exact accessible label');
      assert.equal(multihopDisclosureMounted, true);
      return multihopRouteMounted;
    },
    disclosureIsOpen: async () => {
      multihopEvents.push('inspect editor disclosure');
      return multihopDisclosureOpen;
    },
    openDisclosure: async () => {
      multihopEvents.push('click native disclosure summary');
      multihopDisclosureOpen = true;
      multihopRouteMounted = true;
    },
    waitForRouteEnabled: async () => {
      multihopEvents.push('wait exact route enabled');
      assert.equal(multihopRouteMounted, true);
    },
    clickRoute: async () => multihopEvents.push('click exact route'),
  });
  assert.deepEqual(multihopEvents, [
    'wait for exact radio or disclosure',
    'check exact accessible label',
    'inspect editor disclosure',
    'click native disclosure summary',
    'wait exact route enabled',
    'click exact route',
  ]);

  const directEvents = [];
  let directRouteMounted = false;
  await selectRelatedRouteOption({
    waitForRouteOrDisclosure: async () => {
      directEvents.push('wait for exact radio or disclosure');
      directRouteMounted = true;
    },
    routeIsMounted: async () => {
      directEvents.push('check exact accessible label');
      return directRouteMounted;
    },
    disclosureIsOpen: async () => {
      directEvents.push('unexpected disclosure inspection');
      return false;
    },
    openDisclosure: async () => directEvents.push('unexpected disclosure click'),
    waitForRouteEnabled: async () => {
      directEvents.push('wait exact route enabled');
      assert.equal(directRouteMounted, true);
    },
    clickRoute: async () => directEvents.push('click exact route'),
  });
  assert.deepEqual(directEvents, [
    'wait for exact radio or disclosure',
    'check exact accessible label',
    'wait exact route enabled',
    'click exact route',
  ]);
});
