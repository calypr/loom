import assert from 'node:assert/strict';
import test from 'node:test';
import { decodePopulationMemberRemovalApiResponse } from '../../workflows/population-member-removal-workflow.mjs';

test('decodes a large builder response for execution while keeping its diagnostic bounded and redacted', () => {
  const privateSnapshotToken = 'snapshot-secret-for-response-regression';
  const payload = {
    apiVersion: 'loom.calypr.org/explorer-authoring/v2',
    catalog: {
      generation: 'cda-fhir-v1',
      snapshotToken: privateSnapshotToken,
      nodes: [],
      candidates: Array.from({ length: 500 }, (_, index) => ({
        candidateId: `candidate-${index}`,
        label: `Observation candidate ${index} ${'x'.repeat(48)}`,
      })),
    },
    draftVersion: 4,
    draftDigest: 'sha256:current-draft',
    workspace: { documents: [] },
  };
  const raw = JSON.stringify(payload);
  assert(raw.length > 12_000, 'fixture response must exceed the diagnostic sanitizer limit');

  const decoded = decodePopulationMemberRemovalApiResponse(raw);
  assert.equal(decoded.validJson, true);
  assert.equal(decoded.value.catalog.generation, 'cda-fhir-v1');
  assert.equal(decoded.value.catalog.snapshotToken, privateSnapshotToken);
  assert.equal(decoded.value.catalog.candidates.length, 500);
  assert.equal(decoded.value.draftVersion, 4);
  assert.equal(typeof decoded.diagnostic, 'string', 'the truncated report excerpt remains diagnostic text');
  assert(decoded.diagnostic.length <= 12_000, 'the retained diagnostic stays bounded');
  assert(!decoded.diagnostic.includes(privateSnapshotToken), 'the report excerpt must redact the snapshot token');
});
