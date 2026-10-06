import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { selectBoundedComposedRootCandidates } from '../composed-row-lineage-fixture.mjs';

const workflowPath = fileURLToPath(new URL('../../workflows/verify-cda-composed-row-lineage-browser.mjs', import.meta.url));

const candidate = (rootID, stageCounts, { overflowStages = [], hasRepeatedPatientLeaf = true } = {}) => ({
  rootID,
  witness: { pathKey: `${rootID}#witness` },
  stageCounts,
  overflowStages,
  hasRepeatedPatientLeaf,
});

test('count-only selector skips an overflowing first root and chooses a bounded member plus distinct decoy', () => {
  const oversizedFirst = candidate('Specimen/oversized', { stage1: 2, stage2: 3, stage3: 25 }, {
    overflowStages: ['stage3'],
  });
  const eligibleMember = candidate('Specimen/member', { stage1: 2, stage2: 4, stage3: 5 });
  const eligibleDecoy = candidate('Specimen/decoy', { stage1: 1, stage2: 1, stage3: 1 });

  const { member, decoy } = selectBoundedComposedRootCandidates([
    oversizedFirst,
    eligibleMember,
    eligibleDecoy,
  ], 25);

  assert.equal(member.rootID, 'Specimen/member');
  assert.equal(decoy.rootID, 'Specimen/decoy');
  assert.notEqual(member.rootID, decoy.rootID);
  assert.equal(oversizedFirst.stageCounts.stage3, 25, '25 is the capped overflow sentinel, not an exact eligible count');
  for (const stage of ['stage1', 'stage2', 'stage3']) {
    assert.ok(member.stageCounts[stage] >= 1 && member.stageCounts[stage] <= 24);
    assert.ok(decoy.stageCounts[stage] >= 1 && decoy.stageCounts[stage] <= 24);
  }
  assert.ok(!Object.hasOwn(oversizedFirst, 'relatedRows'));
  assert.ok(!Object.hasOwn(oversizedFirst, 'stageRows'));
});

test('count-only selector reports an explicit fixture failure when no eligible repeated root exists', () => {
  assert.throws(
    () => selectBoundedComposedRootCandidates([
      candidate('Specimen/stage1-overflow', { stage1: 25, stage2: 2, stage3: 3 }, {
        overflowStages: ['stage1'],
      }),
      candidate('Specimen/stage2-overflow', { stage1: 2, stage2: 25, stage3: 3 }, {
        overflowStages: ['stage2'],
      }),
      candidate('Specimen/stage3-overflow', { stage1: 2, stage2: 3, stage3: 25 }, {
        overflowStages: ['stage3'],
      }),
    ], 25),
    /No .*bounded.*root|No .*eligible.*root|No .*repeated.*root/i,
  );
});

test('workflow runs capped count-only classification and selects both roots before full relatedRows materialization', () => {
  const workflow = readFileSync(workflowPath, 'utf8');
  const countQueryStart = workflow.indexOf('const boundedRootCandidateCountsQuery =');
  const countQueryEnd = workflow.indexOf('const assertExactBoundedRows =', countQueryStart);
  const stageQueryStart = workflow.indexOf('const stageOneBoundedQuery =');
  const stageQueryEnd = workflow.indexOf('const assertExactBoundedRows =', stageQueryStart);
  const countQueryUse = workflow.indexOf('const candidateCounts = rawQuery(boundedRootCandidateCountsQuery(');
  const selection = workflow.indexOf('selectBoundedComposedRootCandidates(');
  const focusedBranchStart = workflow.lastIndexOf('if (focusedUpstreamRelatedRemovalCase) {', countQueryUse);
  const focusedBranchEnd = workflow.indexOf('  } else {\n    evaluatedRootCandidates = [];', selection);
  const focusedBranch = workflow.slice(focusedBranchStart, focusedBranchEnd);
  const materializerCalls = [...focusedBranch.matchAll(/\brelatedRows\s*\(/g)].map(match => match.index);

  assert.ok(countQueryStart >= 0 && countQueryEnd > countQueryStart, 'workflow must define a separate bounded count-only query');
  const countQuery = workflow.slice(countQueryStart, countQueryEnd);
  const boundedQueries = workflow.slice(stageQueryStart, stageQueryEnd);
  assert.equal((countQuery.match(/\bCOLLECT\b/g) ?? []).length, 3, 'each stage count must deduplicate its exact path key');
  assert.equal((countQuery.match(/LIMIT \$\{candidateStageRowSentinel\}/g) ?? []).length, 3,
    'each stage count must stop at the overflow sentinel before materializing rows');
  assert.doesNotMatch(countQuery, /relatedRows\s*\(/, 'count classification cannot call the full-row materializer');
  assert.match(countQuery, /COLLECT patientID = p\._id/);
  assert.match(countQuery, /COLLECT parentID = patientID, terminalID = specimen\._id/,
    'stage two counts distinct parent-Patient/specimen path keys, collapsing duplicate Observation bridges');
  assert.match(countQuery, /COLLECT parentID = prior\.parentID, priorTerminalID = prior\.terminalID, terminalID = p\._id/,
    'stage three counts distinct composed paths while preserving each prior specimen path');
  assert.ok(stageQueryStart >= 0 && stageQueryEnd > stageQueryStart, 'selected roots need separate bounded stage queries');
  for (const [name, next] of [
    ['stageOneBoundedQuery', 'stageTwoBoundedQuery'],
    ['stageTwoBoundedQuery', 'stageThreeBoundedQuery'],
    ['stageThreeBoundedQuery', 'boundedRootCandidateCountsQuery'],
  ]) {
    const start = boundedQueries.indexOf(`const ${name} =`);
    const end = boundedQueries.indexOf(`const ${next} =`, start + 1);
    const query = boundedQueries.slice(start, end);
    assert.ok(start >= 0 && end > start, `${name} must be present`);
    assert.ok(query.indexOf('COLLECT ') < query.indexOf('LIMIT ${candidateStageRowSentinel}')
      && query.indexOf('LIMIT ${candidateStageRowSentinel}') < query.indexOf('RETURN'),
    `${name} must deduplicate exact path keys before applying its overflow cap`);
  }
  const boundedStageTwo = boundedQueries.slice(boundedQueries.indexOf('const stageTwoBoundedQuery ='),
    boundedQueries.indexOf('const stageThreeBoundedQuery ='));
  assert.match(boundedStageTwo, /COLLECT parentPath = prior\.pathKey, terminalID = specimen\._id INTO bridgeIDs = observation\._id/);
  assert.match(boundedStageTwo, /LET bridgeID = MIN\(bridgeIDs\)/,
    'the unique stage-two path key must retain a deterministic representative Observation bridge');
  assert.ok(countQueryUse > countQueryEnd, 'workflow must execute count-only classification for the discovered witnesses');
  assert.ok(selection >= 0, 'workflow must select from count-only candidate metadata');
  assert.ok(countQueryUse < selection, 'selection must consume the completed count-only classification');
  assert.ok(focusedBranchStart >= 0 && focusedBranchEnd > selection, 'focused workflow branch must contain selection and selected materialization');
  assert.equal(materializerCalls.length, 3, 'the shared materializer should contain one raw-row call for each stage');
  const selectionInBranch = selection - focusedBranchStart;
  assert.ok(materializerCalls.every(index => index > selectionInBranch),
    'all full relatedRows materialization must follow member/decoy selection');
  for (const queryName of ['stageOneBoundedQuery', 'stageTwoBoundedQuery', 'stageThreeBoundedQuery']) {
    assert.match(focusedBranch, new RegExp(`relatedRows\\([^\\n]*${queryName}`),
      `selected member and decoy must use ${queryName}`);
  }
  const memberMaterialization = focusedBranch.indexOf('materializeSelectedCandidate(member)');
  const decoyMaterialization = focusedBranch.indexOf('materializeSelectedCandidate(decoyCandidate)');
  assert.ok(memberMaterialization > selectionInBranch && decoyMaterialization > memberMaterialization,
    'only the chosen member and distinct decoy may be fully materialized, after selection');
  assert.match(workflow, /\{\s*member\s*,\s*decoy:\s*decoyCandidate\s*\}\s*=\s*selectBoundedComposedRootCandidates/);
  assert.match(workflow, /rawResponse\.preview\?\.sampled, false/,
    'complete proposal previews must reject capped or sampled row sets');
  assert.match(workflow, /previewResponse\?\.sampled, false/,
    'complete Apply previews must reject capped or sampled row sets');
  assert.match(workflow, /reloadedPreview\.sampled, false/,
    'complete reload previews must reject capped or sampled row sets');
  assert.match(workflow, /restoredPreview\.sampled, false/,
    'complete rooted-restoration previews must reject capped or sampled row sets');
});
