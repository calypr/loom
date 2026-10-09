import assert from 'node:assert/strict';
import test from 'node:test';
import { createBuilderAuthoringRequestEntries } from '../builder-authoring-request-entries.mjs';

const request = ({ url, method = 'POST', body, requestId = 'request-id' }) => ({
  url: () => url,
  method: () => method,
  headers: () => ({ 'x-request-id': requestId }),
  postDataJSON: () => {
    if (body === undefined) throw new Error('request has no JSON body');
    return body;
  },
});

test('production Builder request entries assign unique identities across preview and lifecycle requests', () => {
  const apiRoot = '/api/v1/projects/loom_dev_verify_fixture/explorers/verify-builder/authoring/v2';
  const entries = createBuilderAuthoringRequestEntries({ apiRoot, uiUrl: 'http://127.0.0.1:30008' });
  const unrelated = request({ url: 'http://127.0.0.1:30009' + apiRoot + '/preview', body: { outputId: 'out-ignored' } });
  assert.equal(entries.entryFor(unrelated), undefined, 'requests from another origin are not captured');

  const previewRequest = request({ url: `http://127.0.0.1:30008${apiRoot}/preview`, body: { outputId: 'out-patients' }, requestId: 'preview-request' });
  const proposalRequest = request({ url: `http://127.0.0.1:30008${apiRoot}/construction-choice-proposals`, body: { commandId: 'cmd-1' }, requestId: 'proposal-request' });
  const commandRequest = request({ url: `http://127.0.0.1:30008${apiRoot}/commands`, body: { commandId: 'cmd-1' }, requestId: 'command-request' });
  const reconcileRequest = request({ url: `http://127.0.0.1:30008${apiRoot}/reconcile`, body: { snapshotToken: 'snapshot-1' }, requestId: 'reconcile-request' });

  const preview = entries.entryFor(previewRequest);
  const proposal = entries.entryFor(proposalRequest);
  const command = entries.entryFor(commandRequest);
  const reconcile = entries.entryFor(reconcileRequest);

  assert.deepEqual([preview.identity, proposal.identity, command.identity, reconcile.identity],
    ['preview-1', 'proposal-2', 'command-3', 'reconcile-4']);
  assert.deepEqual([preview.requestObjectIdentity, proposal.requestObjectIdentity, command.requestObjectIdentity, reconcile.requestObjectIdentity],
    ['playwright-request-1', 'playwright-request-2', 'playwright-request-3', 'playwright-request-4']);
  assert.deepEqual([preview.requestId, proposal.requestId, command.requestId, reconcile.requestId],
    ['preview-request', 'proposal-request', 'command-request', 'reconcile-request']);
  assert.equal(preview.outputId, 'out-patients');
  assert.equal(proposal.body.commandId, command.body.commandId);
  assert.deepEqual(entries.previewEntries, [preview]);
  assert.deepEqual(entries.choiceProposalEntries, [proposal]);
  assert.deepEqual(entries.commandEntries, [command]);
  assert.deepEqual(entries.reconciliationEntries, [reconcile]);
  assert.equal(entries.previewByRequest.get(previewRequest), preview);
  assert.equal(entries.lifecycleByRequest.get(proposalRequest), proposal);
  assert.equal(entries.lifecycleByRequest.get(commandRequest), command);
  assert.equal(entries.lifecycleByRequest.get(reconcileRequest), reconcile);
});
