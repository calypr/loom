import assert from 'node:assert/strict';
import test from 'node:test';
import {
  selectAcceptedChoicePreviewBinding,
  selectCanceledAcceptedChoicePreviewBinding,
  selectCanceledSavedPreviewRequest,
  selectSavedPreviewRequest,
} from '../saved-preview-binding.mjs';

const stalePreview = {
  path: '/preview', status: 200, completedAt: 10,
  body: { receiptId: 'receipt-before-repair', outputId: 'output-a' },
  response: { receiptId: 'receipt-before-repair', outputId: 'output-a' },
};

test('saved preview selection rejects a successful receipt from before the action window', () => {
  assert.equal(selectSavedPreviewRequest([stalePreview], {
    startIndex: 1, path: '/preview', receiptId: 'receipt-before-repair', outputId: 'output-a',
  }), undefined);
});

test('saved preview selection rejects a receipt from a different draft/output context', () => {
  assert.equal(selectSavedPreviewRequest([stalePreview], {
    startIndex: 0, path: '/preview', receiptId: 'receipt-after-repair', outputId: 'output-a',
  }), undefined);
  assert.equal(selectSavedPreviewRequest([stalePreview], {
    startIndex: 0, path: '/preview', receiptId: 'receipt-before-repair', outputId: 'output-b',
  }), undefined);
});

const savedState = (receiptId = 'receipt-a') => ({
  snapshotToken: 'snapshot-a',
  draftVersion: 4,
  draftDigest: 'draft-a',
  outputId: 'output-a',
  construction: { version: 1, steps: [{ id: 'expand-a', operation: { kind: 'EXPAND' } }] },
  preview: {
    status: 'ready', receiptId, outputId: 'output-a',
    draftVersion: '4', draftDigest: 'draft-a',
  },
});

const savedRequest = (receiptId = 'receipt-a', outputId = 'output-a', path = '/preview') => ({
  path, status: 200, completedAt: 10,
  body: { receiptId, outputId },
  response: { receiptId, outputId, rows: [{ __loom_row_id: 'expand-row-1' }] },
});

test('Cancel reuses the exact after-state saved receipt only when draft, snapshot, output, and construction are unchanged', () => {
  const preview = savedRequest();
  const state = savedState();
  const selected = selectCanceledSavedPreviewRequest([preview], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before: state, after: structuredClone(state),
  });
  assert.deepEqual(selected, { request: preview, source: 'restored-saved-preview' });
  assert.deepEqual(selected.request.response.rows, [{ __loom_row_id: 'expand-row-1' }]);
});

test('Cancel restores a cached saved Preview when the visible before receipt belongs to the proposal', () => {
  const saved = savedRequest('saved-receipt');
  const proposal = {
    path: '/construction-proposals', status: 200, completedAt: 11,
    body: { outputId: 'output-a' },
    response: {
      proposalId: 'proposal-receipt',
      preview: { receiptId: 'proposal-receipt', outputId: 'output-a', rows: [{ __loom_row_id: 'proposal-row' }] },
    },
  };
  const selected = selectCanceledSavedPreviewRequest([saved, proposal], {
    startIndex: 2,
    path: '/preview',
    outputId: 'output-a',
    before: savedState('proposal-receipt'),
    after: savedState('saved-receipt'),
  });
  assert.deepEqual(selected, { request: saved, source: 'restored-saved-preview' });
});

test('Cancel uses a fresh saved receipt when one arrives in the action window', () => {
  const previous = savedRequest('receipt-a');
  const current = savedRequest('receipt-b');
  const before = savedState('proposal-receipt');
  const after = savedState('receipt-b');
  const selected = selectCanceledSavedPreviewRequest([previous, current], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after,
  });
  assert.deepEqual(selected, { request: current, source: 'after-cancel' });
});

test('Cancel refuses stale drafts, wrong outputs, and proposal-embedded preview rows', () => {
  const cached = savedRequest();
  const before = savedState();
  const changedDraft = savedState();
  changedDraft.draftDigest = 'draft-b';
  changedDraft.preview.draftDigest = 'draft-b';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: changedDraft,
  }), undefined);

  const changedSnapshot = savedState();
  changedSnapshot.snapshotToken = 'snapshot-b';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: changedSnapshot,
  }), undefined);

  const changedConstruction = savedState();
  changedConstruction.construction.steps[0].operation.kind = 'FILTER';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: changedConstruction,
  }), undefined);

  const wrongOutput = savedState();
  wrongOutput.preview.outputId = 'output-b';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: wrongOutput,
  }), undefined);

  const staleDomDigest = savedState();
  staleDomDigest.preview.draftDigest = 'stale-draft';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: staleDomDigest,
  }), undefined);

  const staleDomVersion = savedState();
  staleDomVersion.preview.draftVersion = '3';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: staleDomVersion,
  }), undefined);

  const proposalCache = savedRequest('receipt-a', 'output-a', '/construction-proposals');
  assert.equal(selectCanceledSavedPreviewRequest([proposalCache], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: structuredClone(before),
  }), undefined);

  const proposalOnlyResponse = {
    path: '/construction-proposals', status: 200, completedAt: 12,
    body: { outputId: 'output-a' },
    response: { preview: { receiptId: 'receipt-a', outputId: 'output-a', rows: [{ __loom_row_id: 'row-a' }] } },
  };
  assert.equal(selectCanceledSavedPreviewRequest([proposalOnlyResponse], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: structuredClone(before),
  }), undefined);
});

const acceptedChoiceEvents = () => {
  const outputId = 'output-choice';
  const commandId = 'command-choice';
  const snapshotToken = 'snapshot-choice';
  const candidateWorkspaceDigest = 'candidate-workspace-digest';
  const receiptId = 'receipt-choice';
  const workspace = { documents: [{ output: { id: outputId }, columns: [
    { column: 'col-observation-id' }, { column: 'col-subject-reference' },
  ] }] };
  const proposalResponse = {
    commandId, snapshotToken, draftVersion: 3, draftDigest: 'source-draft-digest', outputId,
    constructionChoices: [{ choiceId: 'choice-subject-reference', form: 'VALUE' }],
    candidateColumnIds: ['col-subject-reference'], candidateWorkspaceDigest,
    previewStatus: 'READY',
    preview: { receiptId, outputId, columns: [{ column: 'col-observation-id' }, { column: 'col-subject-reference' }],
      rows: [{ 'col-observation-id': 'Observation/a', 'col-subject-reference': 'Patient/a' }] },
  };
  const requests = [
    { path: '/api/explorers/e/authoring/v2/construction-choice-proposals', method: 'POST', status: 200, startedAt: 1, responseReceivedAt: 8, completedAt: 10,
      body: { commandId, snapshotToken, expectedDraftVersion: 3, expectedDraftDigest: 'source-draft-digest', outputId,
        constructionChoices: [{ choiceId: 'choice-subject-reference', form: 'VALUE' }] }, response: proposalResponse },
    { path: '/api/explorers/e/authoring/v2/commands', method: 'POST', status: 200, startedAt: 11, responseReceivedAt: 15, completedAt: 20,
      body: { commandId, snapshotToken, expectedDraftVersion: 3, expectedDraftDigest: 'source-draft-digest', outputId,
        commands: [{ type: 'APPLY_CONSTRUCTION_CHOICE', outputId, constructionChoice: { choiceId: 'choice-subject-reference', form: 'VALUE' } }] },
      response: { commandId, draftVersion: 4, draftDigest: 'applied-draft-digest',
        results: [{ type: 'COLUMN_ADDED', outputId, column: 'col-subject-reference' }], workspace } },
    { path: '/api/explorers/e/authoring/v2/reconcile', method: 'POST', status: 200, startedAt: 21, responseReceivedAt: 25, completedAt: 30,
      body: { snapshotToken, draftVersion: 4, draftDigest: 'applied-draft-digest' },
      response: { snapshotToken, receiptId, intentDigest: candidateWorkspaceDigest, outputs: [{ outputId }] } },
  ];
  const before = { snapshotToken, draftVersion: 3, draftDigest: 'source-draft-digest', outputId };
  const after = { snapshotToken, draftVersion: 4, draftDigest: 'applied-draft-digest', outputId, workspace: structuredClone(workspace),
    preview: { status: 'ready', receiptId, outputId, draftVersion: '4', draftDigest: 'applied-draft-digest' } };
  const paths = {
    startIndex: 0,
    proposalPath: '/api/explorers/e/authoring/v2/construction-choice-proposals',
    commandPath: '/api/explorers/e/authoring/v2/commands',
    reconcilePath: '/api/explorers/e/authoring/v2/reconcile',
    outputId, before, after,
  };
  return { requests, before, after, paths };
};

test('accepted choice Preview binds the exact proposal, Apply command, current draft, and reconcile receipt', () => {
  const { requests, paths } = acceptedChoiceEvents();
  const proof = selectAcceptedChoicePreviewBinding(requests, paths);
  assert.equal(proof?.receiptId, 'receipt-choice');
  assert.equal(proof?.commandId, 'command-choice');
  assert.equal(proof?.proposal, requests[0]);
  assert.equal(proof?.apply, requests[1]);
  assert.equal(proof?.reconcile, requests[2]);
  assert.deepEqual(proof?.preview.rows, [{ 'col-observation-id': 'Observation/a', 'col-subject-reference': 'Patient/a' }]);
});

test('accepted choice causal order uses response arrival while capture completion remains terminal evidence', () => {
  const { requests, paths } = acceptedChoiceEvents();
  requests[0].startedAt = 1_791_471_852_671;
  requests[0].responseReceivedAt = 1_791_471_852_793;
  requests[0].completedAt = 1_791_471_852_809;
  requests[1].startedAt = 1_791_471_852_930;
  requests[1].responseReceivedAt = 1_791_471_852_954;
  requests[1].completedAt = 1_791_471_852_997;
  requests[2].startedAt = 1_791_471_852_991;
  requests[2].responseReceivedAt = 1_791_471_853_074;
  requests[2].completedAt = 1_791_471_853_099;

  const proof = selectAcceptedChoicePreviewBinding(requests, paths);
  assert.equal(proof?.receiptId, 'receipt-choice', 'the retained response event precedes reconcile even though capture completion follows it');
  assert.equal(proof?.apply.completedAt > proof?.reconcile.startedAt, true, 'terminal body capture may finish after the dependent request starts');
  assert.equal(proof?.reconcile.completedAt > proof?.reconcile.startedAt, true, 'the final request still requires terminal capture');

  requests[2].startedAt = requests[1].responseReceivedAt - 1;
  assert.equal(selectAcceptedChoicePreviewBinding(requests, paths), undefined, 'reconcile must not precede the Apply response event');
});

test('accepted choice Preview rejects unaccepted, unfinished, stale, and mismatched wire evidence', () => {
  const invalid = (mutate) => {
    const { requests, paths } = acceptedChoiceEvents();
    mutate(requests, paths);
    return selectAcceptedChoicePreviewBinding(requests, paths);
  };
  assert.equal(invalid(requests => { requests.splice(1); }), undefined, 'a proposal alone has not been accepted');
  assert.equal(invalid(requests => { requests[1].completedAt = undefined; }), undefined, 'Apply must be terminal');
  assert.equal(invalid(requests => { requests[0].response = { bodyNotRead: true }; }), undefined, 'proposal response must be captured');
  assert.equal(invalid((_requests, paths) => { paths.after.preview.receiptId = 'receipt-wrong'; }), undefined, 'visible receipt must match the accepted preview');
  assert.equal(invalid((_requests, paths) => { paths.after.snapshotToken = 'another-snapshot'; }), undefined, 'current snapshot must match the accepted proposal');
  assert.equal(invalid((_requests, paths) => { paths.after.draftDigest = 'stale-draft'; paths.after.preview.draftDigest = 'stale-draft'; }), undefined, 'current draft must match the Apply successor');
  assert.equal(invalid(requests => { requests[2].response.intentDigest = 'wrong-candidate'; }), undefined, 'reconcile must accept the proposal workspace digest');
  assert.equal(invalid(requests => { requests[2].response.outputs = [{ outputId: 'another-output' }]; }), undefined, 'reconcile must contain the exact output');
  assert.equal(invalid(requests => { requests[0].body.constructionChoices[0].choiceId = 'another-choice'; }), undefined, 'proposal response must echo the submitted choice identity');
  assert.equal(invalid(requests => { requests[1].body.commands[0].constructionChoice.choiceId = 'another-choice'; }), undefined, 'Apply must accept the proposed choice identity');
  assert.equal(invalid(requests => { requests[1].startedAt = requests[0].responseReceivedAt - 1; }), undefined, 'Apply must follow the proposal response event');
  assert.equal(invalid(requests => { requests[2].startedAt = requests[1].responseReceivedAt - 1; }), undefined, 'reconcile must follow the Apply response event');
  assert.equal(invalid((_requests, paths) => { paths.after.workspace.documents[0].columns = [{ column: 'col-observation-id' }]; }), undefined, 'the current workspace must include the proposed column');
  assert.equal(invalid((_requests, paths) => { paths.after.workspace.documents[0].columns.push({ column: 'uncommitted-column' }); }), undefined, 'the full current workspace must equal the exact Apply successor');
});

test('Cancel reuses an accepted choice Preview only for the exact unchanged draft and receipt', () => {
  const before = { kind: 'accepted-choice', receiptId: 'receipt-choice', outputId: 'output-choice', commandId: 'command-choice',
    snapshotToken: 'snapshot-choice', draftVersion: 4, draftDigest: 'draft-choice' };
  assert.deepEqual(selectCanceledAcceptedChoicePreviewBinding(before, structuredClone(before)), {
    source: 'restored-accepted-choice-preview', receiptId: 'receipt-choice', outputId: 'output-choice',
  });
  for (const field of ['receiptId', 'outputId', 'commandId', 'snapshotToken', 'draftVersion', 'draftDigest']) {
    const after = { ...before, [field]: field === 'draftVersion' ? before[field] + 1 : `${before[field]}-changed` };
    assert.equal(selectCanceledAcceptedChoicePreviewBinding(before, after), undefined, `${field} must remain exact`);
  }
  assert.equal(selectCanceledAcceptedChoicePreviewBinding({ ...before, kind: 'proposal-only' }, before), undefined);
  assert.equal(selectCanceledAcceptedChoicePreviewBinding({ ...before, draftVersion: undefined }, before), undefined);
});
