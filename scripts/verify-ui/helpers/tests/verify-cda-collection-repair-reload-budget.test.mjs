import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { findSavedAuthoringColumn, savedAuthoringPreviewSchema } from '../collection-repair-preview-schema.mjs';
import { scenarioCaseFor } from '../../registry.mjs';

const workflow = await readFile(new URL('../../workflows/verify-cda-collection-repair.mjs', import.meta.url), 'utf8');
const spec = await readFile(new URL('../../specs/standalone-cda-other.spec.mjs', import.meta.url), 'utf8');

test('partial collection repair is registered to its exact CDA browser case', () => {
  const contract = scenarioCaseFor('cda-collection-repair-partial', 'partial-long-route-repair-and-reload');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(contract.playwrightGrep, 'remove an unmapped selected resource, verify the saved route, and reload$');
  assert.deepEqual(contract.expectedIdentity, { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
  assert.equal(contract.requiredChecks.length, 12);
  assert.match(contract.requiredChecks[11], /same FILTER EXISTS operation and preview/);
  assert.match(spec, /cdaScenarioID:\s*partialLongRoute \? 'cda-collection-repair-partial'/);
  assert.match(spec, /cdaCaseName:\s*partialLongRoute \? 'partial-long-route-repair-and-reload'/);
  const collectionRepairDescribe = spec.slice(spec.indexOf("test.describe('CDA collection repair'"), spec.indexOf("test.describe('CDA starting collection handoff'"));
  assert.match(collectionRepairDescribe, /cdaUiRouting:\s*'explicit-query'/,
    'the collection repair browser case must validate the explicit project, explorer, and Builder route');
});

function assertReloadCheckpoint({ name, start, open, savedState, coverage, duration, record }) {
  const positions = [start, open, savedState, coverage, duration, record].map(value => workflow.indexOf(value));
  assert(positions.every(position => position >= 0), `${name}: every latency-boundary marker must exist`);
  assert(positions.every((position, index) => index === 0 || positions[index - 1] < position),
    `${name}: start → navigation → saved state → coverage → budget → recorded case order changed`);
}

test('partial collection reload budgets include exact preview rows and persisted state', () => {
  assert.match(workflow, /if\(partialLongRoute\)await assertPreviewIDs\(previewName\)/,
    'the UI reload checkpoint must render and compare exact raw-oracle Observation IDs');
  assert.match(workflow, /rendered\.rows\.map\(row=>row\[0\]\)\.sort\(\),\[\.\.\.expectedObservationIDs\]\.sort\(\)/,
    'the preview checkpoint must retain exact identity and multiplicity comparison');
  const openStart = workflow.indexOf('const open = async');
  const openEnd = workflow.indexOf('const checkCoverage = async', openStart);
  const openBody = workflow.slice(openStart, openEnd);
  assert(openBody.includes('if(partialLongRoute)await assertPreviewIDs(previewName);'),
    'each timed reload must call the shared exact-row comparison before returning from navigation');

  assertReloadCheckpoint({
    name: 'repair reload',
    start: 'const repairReloadStartedAt=Date.now();',
    open: "await open(partialLongRoute?'partial-long-route-reload-raw-oracle':undefined);",
    savedState: "assert.deepEqual(reloaded.population.route,report.savedConnection,'Reload must preserve the exact saved Observation → Specimen → parent route');",
    coverage: "await checkCoverage('partial-collection-reload','2 selected · 2 produce rows · 0 needs attention');",
    duration: 'const repairReloadDuration=Date.now()-repairReloadStartedAt;',
    record: "recordCase({name:'partial-long-route-reload-exact-rows-and-state',durationMs:repairReloadDuration,observationIDs:expectedObservationIDs});",
  });
  assert.match(workflow, /assert\(repairReloadDuration<=5000,/,
    'the repair reload must fail the case when exact rows or saved state take more than five seconds');

  assertReloadCheckpoint({
    name: 'reattached reload',
    start: 'const reattachedReloadStartedAt=Date.now();',
    open: "await open(partialLongRoute?'reattached-partial-long-route-reload-raw-oracle':undefined);",
    savedState: "assert.deepEqual(reattachedReload.population,revised.population,'Reload after reattachment must preserve the exact two-member selection and route');",
    coverage: "await checkCoverage('reattached-partial-long-collection-reload','2 selected · 2 produce rows · 0 needs attention');",
    duration: 'const reattachedReloadDuration=Date.now()-reattachedReloadStartedAt;',
    record: "recordCase({name:'reattached-partial-long-route-reload-exact-rows-and-state',durationMs:reattachedReloadDuration,observationIDs:expectedObservationIDs});",
  });
  assert.match(workflow, /assert\(reattachedReloadDuration<=5000,/,
    'the reattached reload must fail the case when exact rows or saved state take more than five seconds');
  assert.match(workflow, /\.\.\.report\.cases\.map\(item => item\.durationMs\)[\s\S]*durationMs <= 5000/,
    'both reload checkpoints must flow into the existing required five-second performance check');
});

test('partial repair starts from a nonempty native construction without changing raw population', () => {
  const routeApply = workflow.indexOf("await command([{type:'SET_TABLE_POPULATION'");
  const seedDefinition = workflow.indexOf('const seedNonEmptyConstruction = async () => {');
  const emptyBaseline = workflow.indexOf("assert(!before.construction?.steps?.length", seedDefinition);
  const seedEnd = workflow.indexOf('const open = async', seedDefinition);
  const seedAction = workflow.indexOf(`await click(page, '[data-testid="construction-action-keep-rows"]')`, seedDefinition);
  const existsChoice = workflow.indexOf("await selectOption(page, conditionSelector, 'EXISTS')", seedDefinition);
  const seedApply = workflow.indexOf(`await click(page, '[data-testid="construction-apply-proposal"]')`, seedDefinition);
  const seedPersisted = workflow.indexOf("assert.equal(seeded.construction?.steps?.length, 1", seedDefinition);
  const seedReturnsToSettings = workflow.indexOf('await openRowSettings();', seedDefinition);
  const returnToTableBudget = workflow.indexOf('const returnToTableDurationMs = Date.now() - returnToTableStartedAt;', seedDefinition);
  const filterEditorBudget = workflow.indexOf('const filterEditorDurationMs = Date.now() - filterEditorStartedAt;', seedDefinition);
  const capturedProposal = workflow.indexOf('requestMonitor.waitFor(entry => {', seedDefinition);
  const exactPreviewRows = workflow.indexOf("assert.deepEqual(protocolObservationIDs, [...expectedObservationIDs].sort()", capturedProposal);
  const exactPreviewColumns = workflow.indexOf('savedAuthoringPreviewSchema(before.columns)', capturedProposal);
  const proposalReceipt = workflow.indexOf('proposalResponse.preview?.receiptId, proposalResponse.proposalId', capturedProposal);
  const proposalDeadline = workflow.indexOf('proposalStartedAt + 5000 - Date.now()', capturedProposal);
  const proposalDuration = workflow.indexOf('const proposalDurationMs = Date.now() - proposalStartedAt;', capturedProposal);
  const cancelProposal = workflow.indexOf("await click(page, '[data-testid=\"construction-cancel-proposal\"]')", capturedProposal);
  const cancelledWorkspace = workflow.indexOf('assert.deepEqual(cancelled.workspace, beforeWorkspace', cancelProposal);
  const cancelledRows = workflow.indexOf("await assertPreviewIDs('cancelled-nonempty-construction-seed-preview')", cancelProposal);
  const reopenFilter = workflow.indexOf("await click(page, '[data-testid=\"construction-action-keep-rows\"]')", cancelProposal);
  const reappliedExists = workflow.indexOf("await selectOption(page, conditionSelector, 'EXISTS')", reopenFilter);
  const reappliedProposal = workflow.indexOf('const appliedProposalEntry = await requestMonitor.waitFor(entry => {', reappliedExists);
  const freshProposalStart = workflow.indexOf('const appliedProposalStartedAt = Date.now();', reopenFilter);
  const freshProposalDeadline = workflow.indexOf('appliedProposalStartedAt + 5000 - Date.now()', freshProposalStart);
  const reopenedConditionCheck = workflow.indexOf("assert.notEqual(reopenedCondition, 'EXISTS'", reopenFilter);
  const intermediateMissingChoice = workflow.indexOf("await selectOption(page, conditionSelector, 'MISSING')", reopenFilter);
  const sameCandidateSemantics = workflow.indexOf('constructionSemantics(appliedProposalResponse.candidateConstruction)', reappliedProposal);
  const freshStepIdentity = workflow.indexOf('appliedProposalEntry.body?.changedStepId, appliedProposalStepId', reappliedProposal);
  const freshProposalDuration = workflow.indexOf('const appliedProposalDurationMs = Date.now() - appliedProposalStartedAt;', reappliedProposal);
  const applyStartedAt = workflow.indexOf('const applyStartedAt = Date.now();', freshProposalDuration);
  const applyDuration = workflow.indexOf('const applyDurationMs = Date.now() - applyStartedAt;', applyStartedAt);
  const persistedDigest = workflow.indexOf('builder.draftDigest, appliedProposalResponse.candidateWorkspaceDigest', reappliedProposal);
  const persistedCandidate = workflow.indexOf('assert.deepEqual(seeded.construction, appliedProposalResponse.candidateConstruction', reappliedProposal);
  const settingsDuration = workflow.indexOf('const settingsDurationMs = Date.now() - settingsStartedAt;', persistedCandidate);
  const seedCall = workflow.indexOf('await seedNonEmptyConstruction();', seedEnd);
  const repairBaseline = workflow.indexOf("assert.equal(original?.construction?.steps?.length, 1", seedCall);
  const removal = workflow.indexOf("const remove = page.getByRole('button', { name: 'Remove from collection', exact: true })", repairBaseline);
  assert(seedDefinition >= 0 && emptyBaseline > seedDefinition && routeApply < seedCall && seedAction > seedDefinition && existsChoice > seedAction
    && proposalDeadline > existsChoice && proposalDuration > proposalDeadline
    && seedApply > proposalDuration && seedPersisted > seedApply && seedReturnsToSettings > seedPersisted
    && returnToTableBudget > seedDefinition && filterEditorBudget > returnToTableBudget
    && reopenedConditionCheck > reopenFilter && freshProposalStart > reopenedConditionCheck
    && intermediateMissingChoice === -1 && freshProposalDeadline > freshProposalStart
    && sameCandidateSemantics > freshProposalDeadline && freshStepIdentity > sameCandidateSemantics
    && freshProposalDuration > freshStepIdentity && applyStartedAt > freshProposalDuration
    && applyDuration > applyStartedAt && settingsDuration > persistedCandidate && seedEnd > settingsDuration
    && repairBaseline > seedCall && removal > repairBaseline,
  'the native EXISTS filter must be applied and verified before the collection-removal baseline');
  assert(capturedProposal > existsChoice && exactPreviewRows > capturedProposal && proposalReceipt > capturedProposal
    && exactPreviewColumns > capturedProposal && cancelProposal > proposalReceipt && cancelledWorkspace > cancelProposal
    && cancelledRows > cancelledWorkspace && reopenFilter > cancelledRows && reappliedExists > reopenFilter
    && reappliedProposal > reappliedExists && freshProposalDeadline > reappliedExists
    && sameCandidateSemantics > reappliedProposal
    && persistedDigest > seedApply && persistedCandidate > persistedDigest,
  'seed evidence must Cancel without changing the source workspace, recreate the same EXISTS semantics under a tracked fresh identity, and Apply its exact raw preview');
  assert.match(workflow, /candidateSteps\.length === 1 && filterStep/,
    'the seed must capture the single exact native FILTER EXISTS proposal rather than a stale or unrelated proposal');
  assert.match(workflow, /constructionSemantics\(appliedProposalResponse\.candidateConstruction\)[\s\S]*?constructionSemantics\(proposalResponse\.candidateConstruction\)/,
    'the re-proposal comparison must ignore only its generated step identity while retaining operation, inputs, and output bindings');
  assert.match(workflow, /appliedProposalEntry\.body\?\.changedStepId, appliedProposalStepId/,
    'the fresh candidate step identity must match the request that is applied');
  assert.doesNotMatch(workflow, /assert\.equal\(appliedProposalResponse\.candidateWorkspaceDigest, proposalResponse\.candidateWorkspaceDigest/,
    'a regenerated step ID must not be treated as a deterministic candidate digest');
  assert.match(workflow, /previousStepId: proposalStepId,[\s\S]*?stepId: appliedProposalStepId/,
    'the report must retain both canceled and fresh candidate step identities');
  assert.match(workflow, /entry\.body\?\.outputId === outputId && entry\.body\?\.expectedDraftDigest === beforeDigest/,
    'the captured seed proposal must match the target output and exact pre-seed draft');
  assert.match(workflow, /findSavedAuthoringColumn\(before\.columns, sourceColumn\.id\)/,
    'the selected UI column ID must resolve through the saved authoring wire identity');
  assert.match(workflow, /sourceOutputColumn\.column\)/,
    'the preview lookup must use the saved authoring wire column key');
  assert.match(workflow, /assert\.deepEqual\(seeded\.population, before\.population/,
    'the construction seed must leave the exact raw selection and population route unchanged');
  assert.match(workflow, /assert\.deepEqual\(cancelledDocument\?\.columns, before\.columns/,
    'Cancel must preserve the exact output binding before the authored filter is applied');
  assert.match(workflow, /recordRequirement\(11, 'persistence'/,
    'the Cancel-before-Apply state preservation must be a registered lifecycle assertion');
  assert.match(workflow, /nonemptyConstructionStepIds: original\.construction\.steps\.map\(step => step\.id\)/,
    'the required-check baseline must identify the nonempty construction steps that repair must preserve');
  assert.match(workflow, /assert\.deepEqual\(revised\.construction,original\.construction/,
    'removal must preserve the now-nonempty saved construction');
  assert.match(workflow, /assert\.deepEqual\(reloaded\.construction,original\.construction/,
    'repair reload must preserve the now-nonempty saved construction');
  assert.match(workflow, /assert\.deepEqual\(attached\.construction,original\.construction/,
    'native reattachment must preserve the now-nonempty saved construction');
});

test('saved Builder authoring columns map through columnId and column wire fields', () => {
  const savedColumns = [{
    columnId: 'observation-id-column-id',
    column: 'observation_id',
    label: 'Observation ID',
    logicalType: 'string',
  }];
  assert.equal(Object.hasOwn(savedColumns[0], 'id'), false);
  assert.equal(Object.hasOwn(savedColumns[0], 'name'), false);
  assert.equal(findSavedAuthoringColumn(savedColumns, 'observation-id-column-id'), savedColumns[0]);
  assert.deepEqual(savedAuthoringPreviewSchema(savedColumns), [
    { column: 'observation_id', label: 'Observation ID' },
  ]);
});
