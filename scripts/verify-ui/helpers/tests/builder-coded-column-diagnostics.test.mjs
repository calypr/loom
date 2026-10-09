import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyCodedColumnDiagnostics,
  previewHeaderMatches,
} from '../../workflows/builder-coded-source-column.mjs';

test('coded-column diagnostic classifier accepts only marked capability aborts with exact successful replacement evidence', () => {
  const route = 'http://127.0.0.1:30008/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/construction-capabilities';
  const binding = {
    route,
    snapshotToken: 'snapshot-a',
    draftVersion: 1,
    draftDigest: 'digest-a',
    outputId: 'observations',
    stageId: 'stage-a',
  };
  const verifiedReplacement = {
    sequence: 12,
    status: 200,
    finished: true,
    responseMatches: true,
    failed: false,
    binding: { ...binding, snapshotToken: 'snapshot-b', draftVersion: 2, draftDigest: 'digest-b' },
  };
  const verifiedCancellation = {
    kind: 'network',
    method: 'POST',
    url: route,
    errorText: 'net::ERR_ABORTED',
    canceled: true,
    cancellationReason: 'superseded capability binding has a later successful replacement',
    sequence: 11,
    binding,
    replacement: verifiedReplacement,
  };
  const samePathUnmatchedAbort = {
    ...verifiedCancellation,
    canceled: false,
    cancellationReason: undefined,
    replacement: undefined,
  };
  const wrongPathAbort = {
    ...verifiedCancellation,
    url: route.replace('construction-capabilities', 'semantic-inventory'),
  };

  const result = classifyCodedColumnDiagnostics({
    network: [verifiedCancellation, samePathUnmatchedAbort, wrongPathAbort],
  });

  assert.deepEqual(result.cancelledReads, [verifiedCancellation]);
  assert.deepEqual(result.unexpected, [samePathUnmatchedAbort, wrongPathAbort]);
});

test('rendered uppercase coded-column headers match labels after semantic normalization', () => {
  assert.equal(previewHeaderMatches('FIXTURE HEIGHT ABC (KG)', 'Fixture Height ABC'), true);
  assert.equal(previewHeaderMatches('OBSERVATION ID', 'Observation ID'), true);
  assert.equal(previewHeaderMatches('OTHER HEIGHT ABC (KG)', 'Fixture Height ABC'), false);
});
