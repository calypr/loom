import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { findSavedAuthoringColumn, savedAuthoringPreviewSchema } from '../collection-repair-preview-schema.mjs';

const workflow = await readFile(new URL('../../workflows/verify-cda-collection-repair.mjs', import.meta.url), 'utf8');

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
  const seedDuration = workflow.indexOf('const durationMs = Date.now() - seedStartedAt;', seedDefinition);
  const capturedProposal = workflow.indexOf('requestMonitor.waitFor(entry => {', seedDefinition);
  const exactPreviewRows = workflow.indexOf("assert.deepEqual(protocolObservationIDs, [...expectedObservationIDs].sort()", capturedProposal);
  const exactPreviewColumns = workflow.indexOf('savedAuthoringPreviewSchema(before.columns)', capturedProposal);
  const proposalReceipt = workflow.indexOf('proposalResponse.preview?.receiptId, proposalResponse.proposalId', capturedProposal);
  const persistedDigest = workflow.indexOf('builder.draftDigest, proposalResponse.candidateWorkspaceDigest', capturedProposal);
  const persistedCandidate = workflow.indexOf('assert.deepEqual(seeded.construction, proposalResponse.candidateConstruction', capturedProposal);
  const seedCall = workflow.indexOf('await seedNonEmptyConstruction();', seedEnd);
  const repairBaseline = workflow.indexOf("assert.equal(original?.construction?.steps?.length, 1", seedCall);
  const removal = workflow.indexOf("const remove = page.getByRole('button', { name: 'Remove from collection', exact: true })", repairBaseline);
  assert(seedDefinition >= 0 && emptyBaseline > seedDefinition && routeApply < seedCall && seedAction > seedDefinition && existsChoice > seedAction
    && seedApply > existsChoice && seedPersisted > seedApply && seedReturnsToSettings > seedPersisted
    && seedDuration > seedReturnsToSettings && seedEnd > seedDuration
    && repairBaseline > seedCall && removal > repairBaseline,
  'the native EXISTS filter must be applied and verified before the collection-removal baseline');
  assert(capturedProposal > existsChoice && exactPreviewRows > capturedProposal && proposalReceipt > capturedProposal
    && exactPreviewColumns > capturedProposal && persistedDigest > seedApply && persistedCandidate > persistedDigest,
  'seed evidence must bind the captured native EXISTS request, receipt, exact raw IDs, and persisted candidate digest');
  assert.match(workflow, /candidateSteps\.length === 1 && filterStep/,
    'the seed must capture the single exact native FILTER EXISTS proposal rather than a stale or unrelated proposal');
  assert.match(workflow, /entry\.body\?\.outputId === outputId && entry\.body\?\.expectedDraftDigest === beforeDigest/,
    'the captured seed proposal must match the target output and exact pre-seed draft');
  assert.match(workflow, /findSavedAuthoringColumn\(before\.columns, sourceColumn\.id\)/,
    'the selected UI column ID must resolve through the saved authoring wire identity');
  assert.match(workflow, /sourceOutputColumn\.column\)/,
    'the preview lookup must use the saved authoring wire column key');
  assert.match(workflow, /assert\.deepEqual\(seeded\.population, before\.population/,
    'the construction seed must leave the exact raw selection and population route unchanged');
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
