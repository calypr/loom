import assert from 'node:assert/strict';
import test from 'node:test';
import { proveSupersededCapabilitiesAbort } from './superseded-capabilities-abort.mjs';

const sourceFieldsTuple = () => [
  {
    requestId: 'playwright-24', browserRequestId: 'playwright-24',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/cda-source-fields-1791213357335-a15d7b78/authoring/v2/construction-capabilities',
    method: 'POST', origin: 'http://127.0.0.1:30008', startedAt: 1791213364476, completedAt: 1791213365330, failure: 'net::ERR_ABORTED',
    body: { snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', outputId: 'out_5fbed6472b89faf3a8a85790', stageId: 'source_projection', expectedDraftVersion: 4, expectedDraftDigest: 'sha256:5d734427ba64e75ba36c401be2d0c5b12a78277e645adab9ea7f5679d6e61383' },
  },
  {
    requestId: 'builder-command-d21d1fc9-0497-4957-9ca8-81e2ed791274', browserRequestId: 'playwright-27',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/cda-source-fields-1791213357335-a15d7b78/authoring/v2/commands',
    method: 'POST', origin: 'http://127.0.0.1:30008', startedAt: 1791213365240, responseReceivedAt: 1791213365259, completedAt: 1791213365282, status: 200,
    body: { snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', expectedDraftVersion: 4, expectedDraftDigest: 'sha256:5d734427ba64e75ba36c401be2d0c5b12a78277e645adab9ea7f5679d6e61383', commands: [{ type: 'UPDATE_COLUMN', outputId: 'out_5fbed6472b89faf3a8a85790', column: 'col_c8d8959db36d55c1bc555e75', columnValue: { label: 'CDA component values QA' } }] },
    response: { draftVersion: 5, draftDigest: 'sha256:8486eba21a0e80280b92f2ad789f717d2f1325beaf733fe6303b73c95cc3dd54', results: [{ outputId: 'out_5fbed6472b89faf3a8a85790', type: 'TABLE_CHANGED' }] },
  },
  {
    requestId: 'playwright-37', browserRequestId: 'playwright-37',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/cda-source-fields-1791213357335-a15d7b78/authoring/v2/construction-capabilities',
    method: 'POST', origin: 'http://127.0.0.1:30008', startedAt: 1791213366804, responseReceivedAt: 1791213367715, completedAt: 1791213367750, status: 200,
    body: { snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', outputId: 'out_5fbed6472b89faf3a8a85790', stageId: 'source_projection', expectedDraftVersion: 5, expectedDraftDigest: 'sha256:8486eba21a0e80280b92f2ad789f717d2f1325beaf733fe6303b73c95cc3dd54' },
  },
];

const compoundFieldsTuple = () => [
  {
    requestId: 'playwright-47', browserRequestId: 'playwright-47',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/compound-fields-browser-1791214085520/authoring/v2/construction-capabilities',
    method: 'POST', origin: 'http://127.0.0.1:30008', startedAt: 1791214097896, completedAt: 1791214098521, failure: 'net::ERR_ABORTED',
    body: { snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', outputId: 'out_e0a89555b6593ed925e53f3c', stageId: 'source_projection', expectedDraftVersion: 5, expectedDraftDigest: 'sha256:bc7680954b246656d94fc4b6fc722e0a7794d3c863f92983c94cc73b7c5f097a' },
  },
  {
    requestId: 'builder-command-7f56ceab-22bb-4fe1-a6ed-6b4db39b6cdc', browserRequestId: 'playwright-51',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/compound-fields-browser-1791214085520/authoring/v2/commands',
    method: 'POST', origin: 'http://127.0.0.1:30008', triggerAction: 'Remove Primary disease type column',
    startedAt: 1791214098461, responseReceivedAt: 1791214098498, completedAt: 1791214098509, status: 200,
    body: { snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', expectedDraftVersion: 5, expectedDraftDigest: 'sha256:bc7680954b246656d94fc4b6fc722e0a7794d3c863f92983c94cc73b7c5f097a', commands: [{ type: 'REMOVE_COLUMN', outputId: 'out_e0a89555b6593ed925e53f3c', column: 'col_307bbb38f2f65a35448bb526' }] },
    response: { draftVersion: 6, draftDigest: 'sha256:f17ee48c87add0f6a9407e8d90552c8354d4f3e810a0bd9b718ae527e5e25a8b', results: [{ outputId: 'out_e0a89555b6593ed925e53f3c', type: 'TABLE_CHANGED' }] },
  },
  {
    requestId: 'playwright-53', browserRequestId: 'playwright-53',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/compound-fields-browser-1791214085520/authoring/v2/construction-capabilities',
    method: 'POST', origin: 'http://127.0.0.1:30008', startedAt: 1791214098514, responseReceivedAt: 1791214099112, completedAt: 1791214099112, status: 200,
    body: { snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', outputId: 'out_e0a89555b6593ed925e53f3c', stageId: 'source_projection', expectedDraftVersion: 6, expectedDraftDigest: 'sha256:f17ee48c87add0f6a9407e8d90552c8354d4f3e810a0bd9b718ae527e5e25a8b' },
  },
];

test('proves the retained source-fields v4 to v5 rename chronology', () => {
  const requests = sourceFieldsTuple();
  const proof = proveSupersededCapabilitiesAbort(requests, requests[0]);
  assert(proof);
  assert.equal(proof.mutation.commands[0].type, 'UPDATE_COLUMN');
  assert.equal(proof.mutation.commands[0].columnValue.label, 'CDA component values QA');
  assert.deepEqual(proof.chronology, {
    abortedRequestStartedBeforeMutation: true,
    abortedRequestWasPendingAtIdentityChange: true,
    replacementStartedAfterIdentityChange: true,
    replacementMatchesMutationResult: true,
  });
});

test('proves the retained compound-fields v5 to v6 removal chronology', () => {
  const requests = compoundFieldsTuple();
  const proof = proveSupersededCapabilitiesAbort(requests, requests[0]);
  assert(proof);
  assert.equal(proof.mutation.action, 'Remove Primary disease type column');
  assert.equal(proof.mutation.commands[0].type, 'REMOVE_COLUMN');
  assert.equal(proof.mutation.commands[0].column, 'col_307bbb38f2f65a35448bb526');
  assert.deepEqual(proof.chronology, {
    abortedRequestStartedBeforeMutation: true,
    abortedRequestWasPendingAtIdentityChange: true,
    replacementStartedAfterIdentityChange: true,
    replacementMatchesMutationResult: true,
  });
});

test('does not classify an unrelated abort with another output identity', () => {
  const requests = sourceFieldsTuple();
  requests[0].body.outputId = 'out_other';
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('does not classify a replacement that started before the identity change', () => {
  const requests = sourceFieldsTuple();
  requests[2].startedAt = requests[1].responseReceivedAt - 1;
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('does not classify a replacement carrying the wrong resulting digest', () => {
  const requests = sourceFieldsTuple();
  requests[2].body.expectedDraftDigest = 'sha256:wrong';
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('does not classify a GET replacement for the POST capabilities request', () => {
  const requests = sourceFieldsTuple();
  requests[2].method = 'GET';
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('does not classify an abort that finished before its mutation changed the draft', () => {
  const requests = sourceFieldsTuple();
  requests[0].completedAt = requests[1].responseReceivedAt - 1;
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});


test('does not classify a cross-explorer mutation', () => {
  const requests = sourceFieldsTuple();
  requests[1].path = requests[1].path.replace('cda-source-fields-1791213357335-a15d7b78', 'another-explorer');
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('does not classify a cross-origin mutation', () => {
  const requests = sourceFieldsTuple();
  requests[1].origin = 'http://127.0.0.1:8188';
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('does not classify a replacement from another Explorer', () => {
  const requests = sourceFieldsTuple();
  requests[2].path = requests[2].path.replace('cda-source-fields-1791213357335-a15d7b78', 'another-explorer');
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('fails closed when browser request IDs are duplicated', () => {
  const requests = sourceFieldsTuple();
  requests[2].browserRequestId = requests[1].browserRequestId;
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('fails closed when given a cloned request instead of its unique retained member', () => {
  const requests = sourceFieldsTuple();
  assert.equal(proveSupersededCapabilitiesAbort(requests, { ...requests[0] }), undefined);
});

test('fails closed when a request identifier is duplicated', () => {
  const requests = sourceFieldsTuple();
  requests.push({ ...requests[1], path: `${requests[1].path}/duplicate` });
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});

test('fails closed when more than one mutation matches the old draft identity', () => {
  const requests = sourceFieldsTuple();
  requests.push({ ...requests[1], browserRequestId: 'playwright-duplicate', requestId: 'builder-command-duplicate' });
  assert.equal(proveSupersededCapabilitiesAbort(requests, requests[0]), undefined);
});
