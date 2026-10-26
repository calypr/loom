import assert from 'node:assert/strict';
import test from 'node:test';
import { captureConstructionChoiceProposalRequest } from '../construction-choice-request.mjs';
import { sanitizePayload } from '../playwright-browser.mjs';

const nativeRequest = (url, body, method = 'POST') => ({
  method: () => method,
  url: () => url,
  postDataJSON: () => body,
});

test('owned choice-proposal capture preserves the raw Patient.id ONE request binding', () => {
  const choiceId = 'cc2.raw-server-issued-choice-id';
  const snapshotToken = 'sha256:' + 'a'.repeat(64);
  const request = nativeRequest(
    'http://127.0.0.1:30008/api/v1/projects/loom_dev_verify_case/explorers/verify-patient-one/authoring/v2/construction-choice-proposals',
    {
      commandId: 'choice-proposal-command', snapshotToken, expectedDraftVersion: 3,
      expectedDraftDigest: 'sha256:draft-digest', outputId: 'out-patient-table',
      constructionChoices: [{ choiceId, form: 'VALUE', rowValuePolicy: 'ONE' }],
    },
  );

  assert.deepEqual(sanitizePayload(captureConstructionChoiceProposalRequest(request)), {
    commandId: 'choice-proposal-command', snapshotToken, expectedDraftVersion: 3,
    expectedDraftDigest: 'sha256:draft-digest', outputId: 'out-patient-table',
    constructionChoices: [{ choiceId, form: 'VALUE', rowValuePolicy: 'ONE' }],
  });
});

test('capture copies an explicit wire route and ignores fields from unrelated endpoints', () => {
  const route = [{ edgeId: 'edge-exact', fromNodeId: 'patient-node', toNodeId: 'observation-node' }];
  const body = { constructionChoices: [{ choiceId: 'choice-exact', form: 'ALL', rowValuePolicy: 'ALL', route }] };
  const request = nativeRequest(
    'http://127.0.0.1:30008/api/v1/projects/p/explorers/e/authoring/v2/construction-choice-proposals', body,
  );
  assert.deepEqual(captureConstructionChoiceProposalRequest(request), {
    constructionChoices: [{ choiceId: 'choice-exact', form: 'ALL', rowValuePolicy: 'ALL', route }],
  });

  assert.equal(captureConstructionChoiceProposalRequest(nativeRequest(
    'http://127.0.0.1:30008/api/v1/projects/p/explorers/e/authoring/v2/construction-proposals', body,
  )), null);
  assert.equal(captureConstructionChoiceProposalRequest(nativeRequest(request.url(), body, 'GET')), null);
});
