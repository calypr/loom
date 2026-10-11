import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { chromium } from '@playwright/test';
import test from 'node:test';
import { basicRootReplacementWorkflow, browserExplorerCollectionPathFor, replacementRootIDColumnFromPreview, visibleRowsWaitArgs } from '../../workflows/basic-root-replacement.mjs';
import {
  replacementApplyCommandFromRequest,
  replacementApplyCommandMatches,
  replacementAssessmentEffectsMatch,
  replacementAssessmentMatches,
  replacementProposalMatches,
  explicitResourceRefs,
  workspaceColumnConsumerEvidence,
  workspaceTabIdentitySnapshot,
} from '../basic-root-replacement-contract.mjs';

const source = Object.freeze({
  snapshotToken: 'catalog-snapshot-patient',
  catalogGeneration: 'fixture-generation-42',
  catalogDigest: 'catalog-patient-digest',
  draftVersion: 7,
  draftDigest: 'draft-patient-digest',
  outputId: 'output-table-1',
  rootNodeId: 'node-research-study',
  sourceDocumentDigest: 'document-patient-digest',
});

const assessmentRequest = () => ({
  snapshotToken: source.snapshotToken,
  draftVersion: source.draftVersion,
  draftDigest: source.draftDigest,
  outputId: source.outputId,
  rootNodeId: source.rootNodeId,
  mode: 'REPLACE',
});

const proposalFor = () => ({
  mode: 'REPLACE',
  outputId: source.outputId,
  rootNodeId: source.rootNodeId,
  rootOccurrenceId: 'base',
  sourceDocumentDigest: source.sourceDocumentDigest,
  catalogSnapshotToken: source.snapshotToken,
  catalogGeneration: source.catalogGeneration,
  catalogDigest: source.catalogDigest,
  routeRebase: [],
  preservedFeatureKeys: [],
  removedFeatureKeys: ['patient-birthDate-filter'],
  workspaceOutputConsumers: [],
  sharedFilterBindings: [],
});


test('explicit Patient selection refs preserve the exact Builder project scope', () => {
  assert.deepEqual(explicitResourceRefs({
    project: 'loom_dev_verify_mv32aj73-10f61de',
    generation: 'cda-fhir-v1',
    resourceType: 'Patient',
    ids: ['replacement-patient-001', 'replacement-patient-002'],
  }), [
    { project: 'loom_dev_verify_mv32aj73-10f61de', generation: 'cda-fhir-v1', resourceType: 'Patient', id: 'replacement-patient-001' },
    { project: 'loom_dev_verify_mv32aj73-10f61de', generation: 'cda-fhir-v1', resourceType: 'Patient', id: 'replacement-patient-002' },
  ]);
});

test('replacement assessment, proposal, effects, and Apply retain the exact server contract', () => {
  const assessment = replacementAssessmentMatches(assessmentRequest(), source);
  assert.deepEqual(assessment, { ok: true, failures: [] });

  const proposal = proposalFor();
  assert.deepEqual(replacementProposalMatches(proposal, source), { ok: true, failures: [] });
  const response = {
    status: 'READY',
    candidateReceiptId: 'receipt-research-study-replacement',
    proposal,
    preservedFeatureKeys: proposal.preservedFeatureKeys,
    removedFeatureKeys: proposal.removedFeatureKeys,
    workspaceOutputConsumers: proposal.workspaceOutputConsumers,
    sharedFilterBindings: proposal.sharedFilterBindings,
  };
  assert.deepEqual(replacementAssessmentEffectsMatch(response, proposal), { ok: true, failures: [] });

  const request = { commands: [{ type: 'APPLY_TABLE_ROOT_REBASE', rowChange: proposal }] };
  assert.equal(replacementApplyCommandFromRequest(request).command, request.commands[0]);
  assert.deepEqual(replacementApplyCommandMatches(request, proposal), {
    ok: true, failures: [], command: request.commands[0],
  });
});

test('proposal catalog digest is non-empty and compared to the prior proposal, not schema digest', () => {
  const proposal = proposalFor();
  const expectedWithoutDigest = { ...source };
  delete expectedWithoutDigest.catalogDigest;
  assert.deepEqual(replacementProposalMatches(proposal, expectedWithoutDigest), { ok: true, failures: [] });
  assert.equal(replacementProposalMatches({ ...proposal, catalogDigest: '' }, expectedWithoutDigest).ok, false);
  assert.equal(replacementProposalMatches({ ...proposal, catalogDigest: 'different-snapshot' }, source).ok, false);
});

test('replacement checks reject stale source/catalog identity, omitted effects, and altered Apply proposal', () => {
  const request = assessmentRequest();
  assert.equal(replacementAssessmentMatches({ ...request, draftDigest: 'stale' }, source).ok, false);
  assert.equal(replacementAssessmentMatches({ ...request, rootOccurrenceId: 'old-patient-route' }, source).ok, false);

  const proposal = proposalFor();
  assert.equal(replacementProposalMatches({ ...proposal, sourceDocumentDigest: 'stale' }, source).ok, false);
  assert.equal(replacementProposalMatches({ ...proposal, catalogDigest: 'stale' }, source).ok, false);
  assert.equal(replacementProposalMatches({ ...proposal, rootOccurrenceId: 'old-patient-route' }, source).ok, false);
  assert.equal(replacementProposalMatches({ ...proposal, workspaceOutputConsumers: undefined }, source).ok, false);

  const assessment = {
    status: 'READY',
    candidateReceiptId: 'receipt-research-study-replacement',
    proposal,
    preservedFeatureKeys: proposal.preservedFeatureKeys,
    removedFeatureKeys: proposal.removedFeatureKeys,
    workspaceOutputConsumers: proposal.workspaceOutputConsumers,
    sharedFilterBindings: proposal.sharedFilterBindings,
  };
  assert.equal(replacementAssessmentEffectsMatch({ ...assessment, sharedFilterBindings: ['changed'] }, proposal).ok, false);
  assert.equal(replacementApplyCommandMatches({ commands: [{
    type: 'APPLY_TABLE_ROOT_REBASE',
    rowChange: { ...proposal, rootNodeId: 'another-target' },
  }] }, proposal).ok, false);
  assert.equal(replacementApplyCommandMatches({ commands: [
    { type: 'APPLY_TABLE_ROOT_REBASE', rowChange: proposal },
    { type: 'UNRELATED_COMMAND' },
  ] }, proposal).ok, false);
});

test('native replacement workflow module loads for the registered Playwright spec', () => {
  assert.equal(typeof basicRootReplacementWorkflow, 'function');
});

test('Combine candidate preview waits for the new consumer output identity', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /consumerOutputId = await operationEditor\.getAttribute\('data-output-id'\);[\s\S]*?await waitForCandidatePreview\(consumerOutputId\);/);
});

test('saved preview headers prefer semantic text over CSS-transformed innerText', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /cell\.getAttribute\('aria-label'\) \?\? cell\.querySelector\('span'\)\?\.textContent \?\? cell\.textContent \?\? cell\.innerText/);
});

test('replacement keeps the separately scoped connected-root preservation choice', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /const startingTypeRegion = page\.getByRole\('region', \{ name: 'Starting record type', exact: true \}\);[\s\S]*?startingTypeRegion\.getByRole\('combobox', \{ name: 'Choose starting record type', exact: true \}\)/);
  assert.match(workflowSource, /preserveOptions\.includes\('Observation via Patient → Subject → Observation'\)/);
  assert.doesNotMatch(workflowSource, /getByRole\('combobox', \{ name: 'Record type', exact: true \}\)/);
});

test('post-Apply handoff waits for the consumer tab before selecting the original output', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  const handoffStart = workflowSource.indexOf("check('correctness', 'native current-draft consumer projects the exact source Patient birthDate field'");
  const replacementStart = workflowSource.indexOf('const researchStudyNode', handoffStart);
  assert(handoffStart >= 0 && replacementStart > handoffStart);
  const handoff = workflowSource.slice(handoffStart, replacementStart);
  assert.match(handoff, /construction-operation-editor'\)\.waitFor\(\{\s*state: 'detached'/);
  assert.match(handoff, /waitForFunction\(id =>[\s\S]*?construction-table-\$\{id\}[\s\S]*?consumerOutputId/);
  assert.doesNotMatch(handoff, /construction-close-operation-editor/);
  assert(workflowSource.indexOf('construction-table-${outputId}', replacementStart) > replacementStart,
    'Replacement navigation must select the original output only after confirming the consumer output is active.');
});

test('replacement Cancel closes row settings before visible table assertions', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  const cancelStart = workflowSource.indexOf("const cancelReplacement = firstReview.replacementDialog.getByRole('button', { name: REPLACEMENT_CANCEL");
  const reassessStart = workflowSource.indexOf('const secondAssessmentStart', cancelStart);
  assert(cancelStart >= 0 && reassessStart > cancelStart);
  const cancelPath = workflowSource.slice(cancelStart, reassessStart);
  assert.match(cancelPath, /firstReview\.replacementDialog\.waitFor\(\{ state: 'detached'/);
  assert.match(cancelPath, /Row definition settings'[\s\S]*?Back to table'[\s\S]*?rowSettingsAfterCancel\.waitFor\(\{ state: 'detached'/);
  assert(cancelPath.indexOf('rowSettingsAfterCancel.waitFor') < cancelPath.indexOf("await visibleRows(filteredPatientIDs, 'Patient')"),
    'Cancel must dismiss the blocking row-settings dialog before checking visible Patient rows.');
});

test('replacement Preview recognizes the root ID by exact source identity and retains its server label', () => {
  const preview = {
    columns: [{
      authoredColumns: ['id'],
      column: 'id',
      label: 'id',
      sourcePath: 'id',
      sourceResourceType: 'ResearchStudy',
    }],
  };
  assert.deepEqual(replacementRootIDColumnFromPreview(preview, 'ResearchStudy'), preview.columns[0]);
  assert.throws(() => replacementRootIDColumnFromPreview({
    columns: [{ ...preview.columns[0], sourceResourceType: 'Patient' }],
  }, 'ResearchStudy'), /source-bound root ID column/);
});

test('replacement visible-row checks keep the server label separate from output identity', async () => {
  assert.deepEqual(visibleRowsWaitArgs(['study-1'], 'ResearchStudy', 'id', 'existing-output'), {
    ids: ['study-1'],
    type: 'ResearchStudy',
    expectedHeader: 'id',
    expectedOutput: 'existing-output',
  });
  assert.deepEqual(visibleRowsWaitArgs(['patient-1'], 'Patient', 'Patient ID', 'existing-output'), {
    ids: ['patient-1'],
    type: 'Patient',
    expectedHeader: 'patient id',
    expectedOutput: 'existing-output',
  });

  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /const visibleRows = async \(expectedIDs, resourceType, idLabel = `\$\{resourceType\} ID`, expectedOutputId = outputId\)/);
  assert.match(workflowSource, /visibleRowsWaitArgs\(expectedIDs, resourceType, idLabel, expectedOutputId\)/);
  assert.match(workflowSource, /rowIDs\(snapshot, resourceType, idLabel\)/);
  assert.match(workflowSource, /visibleRows\(studyIDs, 'ResearchStudy', secondReview\.candidateIDColumn\.label\)/);
});

test('Cancel mutation audit scopes all root commands to the click and distinguishes preserved rebases', async () => {
  const proposal = proposalFor();
  const preservedRequest = { commands: [{
    type: 'APPLY_TABLE_ROOT_REBASE',
    rowChange: { ...proposal, mode: 'PRESERVE' },
  }] };
  const replacementRequest = { commands: [{ type: 'APPLY_TABLE_ROOT_REBASE', rowChange: proposal }] };
  assert.equal(replacementApplyCommandFromRequest(preservedRequest).command, undefined,
    'A preserved root rebase must not be counted as applying the replacement proposal.');
  assert.equal(replacementApplyCommandFromRequest(replacementRequest).command, replacementRequest.commands[0],
    'A replacement root rebase must remain visible to the mutation audit.');

  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /const actionRequestCountBeforeCancel = actionRequests\.length/);
  assert.match(workflowSource, /const cancelRequests = actionRequests\.slice\(actionRequestCountBeforeCancel\)/);
  assert.match(workflowSource, /assert\.equal\(cancelRowRootCommands\.length, 0/);
  assert.match(workflowSource, /replacementApplyRequestCount, replacementApplyCountBeforeCancel/);
  assert.doesNotMatch(workflowSource, /assert\.equal\(replacementApplyRequestCount, 0/);
  assert.match(workflowSource, /const actionRequestCountBeforeApply = actionRequests\.length/);
  assert.match(workflowSource, /assert\.equal\(applyRowRootCommands\.length, 1/);
  assert.match(workflowSource, /replacementApplyCommandFromRequest\(applyRowRootCommands\[0\]\.body\)\.command\?\.rowChange,[\s\S]*?secondAssessment\.proposal/);
});

test('replacement dialog locators follow the native dialog, consequences, and footer boundaries', async () => {
  const browser = await chromium.launch({ headless: true, channel: 'chrome' });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <section role="dialog" aria-modal="true" aria-labelledby="root-replacement-title">
        <h2 id="root-replacement-title">Replace this table’s current setup?</h2>
        <section aria-label="Replacement consequences">
          <h3>What will change</h3>
          <p>Starting record: ResearchStudy</p>
        </section>
        <section>
          <h3>Candidate preview</h3>
          <table data-testid="construction-proposal-preview"><tbody><tr><td>study-1</td></tr></tbody></table>
        </section>
        <footer>
          <button type="button">Cancel</button>
          <button type="button">Change starting record type</button>
        </footer>
      </section>
    `);

    const dialog = page.getByRole('dialog', { name: 'Replace this table’s current setup?', exact: true });
    const consequences = dialog.getByRole('region', { name: 'Replacement consequences', exact: true });
    assert.equal(await dialog.count(), 1);
    assert.equal(await dialog.getByRole('heading', { name: 'Replace this table’s current setup?', exact: true }).count(), 1);
    assert.equal(await consequences.getByRole('heading', { name: 'Replace this table’s current setup?', exact: true }).count(), 0);
    assert.equal(await dialog.getByRole('heading', { name: 'Candidate preview', exact: true }).count(), 1);
    assert.equal(await dialog.locator('table[data-testid="construction-proposal-preview"]').count(), 1);
    assert.equal(await dialog.getByRole('button', { name: 'Cancel', exact: true }).count(), 1);
    assert.equal(await dialog.getByRole('button', { name: 'Change starting record type', exact: true }).count(), 1);
    assert.equal(await consequences.getByRole('button', { name: 'Cancel', exact: true }).count(), 0);
    assert.equal(await consequences.getByRole('button', { name: 'Change starting record type', exact: true }).count(), 0);
  } finally {
    await browser.close();
  }
});

test('relationship field locator matches the literal array-backed ResearchStudy result path', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.ok(workflowSource.includes("const resultField = page.getByRole('checkbox', { name: 'Select ResearchStudy.result[].reference', exact: true });"));

  const browser = await chromium.launch({ headless: true, channel: 'chrome' });
  try {
    const page = await browser.newPage();
    await page.setContent('<input type="checkbox" aria-label="Select ResearchStudy.result[].reference">');
    const resultField = page.getByRole('checkbox', { name: 'Select ResearchStudy.result[].reference', exact: true });
    assert.equal(await resultField.count(), 1);
    assert.equal(await resultField.isVisible(), true);
  } finally {
    await browser.close();
  }
});

test('replacement workflow scopes headings and footer actions to the dialog', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /const replacementDialog = page\.getByRole\('dialog', \{ name: REPLACEMENT_REVIEW_HEADING, exact: true \}\);[\s\S]*?replacementDialog\.getByRole\('heading', \{ name: REPLACEMENT_REVIEW_HEADING, exact: true \}\)[\s\S]*?const reviewPanel = replacementDialog\.getByRole\('region', \{ name: 'Replacement consequences', exact: true \}\)/);
  assert.match(workflowSource, /firstReview\.replacementDialog\.getByRole\('button', \{ name: REPLACEMENT_CANCEL, exact: true \}\)/);
  assert.match(workflowSource, /secondReview\.replacementDialog\.getByRole\('button', \{ name: REPLACEMENT_APPLY, exact: true \}\)/);
  assert.match(workflowSource, /firstReview\.replacementDialog\.waitFor\(\{ state: 'detached'/);
  assert.match(workflowSource, /secondReview\.replacementDialog\.waitFor\(\{ state: 'detached'/);
});

test('replacement reassessment retains the proposal catalog digest rather than resolved schema digest', async () => {
  const workflowSource = await readFile(new URL('../../workflows/basic-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /let initialReplacementCatalogDigest;/);
  assert.match(workflowSource, /catalogDigest: initialReplacementCatalogDigest/);
  assert.match(workflowSource, /secondAssessment\.proposal\.catalogDigest, firstAssessment\.proposal\.catalogDigest/);
  assert.doesNotMatch(workflowSource, /catalogDigest:\s*baselineBuilder\.catalog\.resolvedSchemaDigest/);
});

test('browser Explorer request paths use the canonical double-encoded project identity', () => {
  assert.equal(
    browserExplorerCollectionPathFor('loom_dev_verify_run-1234'),
    '/api/v1/projects/loom_dev_verify_run%252F1234/explorers',
  );
  assert.equal(
    browserExplorerCollectionPathFor('study/project'),
    '/api/v1/projects/study%252Fproject/explorers',
  );
});

test('workspace tab snapshots retain exact identity, ordering, visibility, and title', () => {
  const builder = { workspace: { tabs: [
    { id: 'tab-second', outputId: 'output-second', order: 1, visible: false, title: 'Second table' },
    { id: 'tab-first', outputId: 'output-first', order: 0, visible: true, title: 'First table' },
    { id: 'tab-default-visible', outputId: 'output-third', order: 2, title: 'Third table' },
  ] } };
  assert.deepEqual(workspaceTabIdentitySnapshot(builder), [
    { id: 'tab-first', outputId: 'output-first', order: 0, visible: true, title: 'First table' },
    { id: 'tab-second', outputId: 'output-second', order: 1, visible: false, title: 'Second table' },
    { id: 'tab-default-visible', outputId: 'output-third', order: 2, visible: null, title: 'Third table' },
  ]);
});

test('removed-column consumer evidence resolves its exact output, step, and input tuple from the saved document', () => {
  const builder = { workspace: { documents: [{
    output: { id: 'output-consumer' },
    construction: { steps: [{
      id: 'join-patient-source',
      inputs: [
        { kind: 'WORKSPACE_OUTPUT', outputId: 'output-patient' },
        { kind: 'WORKSPACE_OUTPUT', outputId: 'output-patient-copy' },
      ],
      operation: { kind: 'COMBINE', combine: { projections: [
        { outputColumnId: 'projected-birth-date', inputIndex: 0, inputColumnId: 'patient-birth-date' },
        { outputColumnId: 'projected-copy-id', inputIndex: 1, inputColumnId: 'patient-copy-id' },
      ] } },
    }] },
  }] } };
  assert.deepEqual(workspaceColumnConsumerEvidence(builder, {
    sourceOutputId: 'output-patient', columnId: 'patient-birth-date',
  }), [{
    outputId: 'output-consumer', stepId: 'join-patient-source', inputIndex: 0, columnId: 'patient-birth-date',
  }]);
  assert.deepEqual(workspaceColumnConsumerEvidence(builder, {
    sourceOutputId: 'output-patient', columnId: 'patient-copy-id',
  }), []);
});
