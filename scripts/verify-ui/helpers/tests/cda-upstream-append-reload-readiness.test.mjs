import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
import {
  emptyTargetReady,
  exactSelectionReadMatches,
  readReloadedSelectionResponse,
  reloadSelectedTarget,
  selectedSavedReady,
} from '../../workflows/cda-current-draft-upstream-append-workflow.mjs';

const target = 'append-output';

const documentFor = state => ({
  body: { innerText: state.loading ? 'Loading your table…' : '' },
  querySelector(selector) {
    const tableButton = selector.match(/^\[data-testid="construction-table-(.+)"\]\[aria-current="page"\]$/);
    if (tableButton) return state.activeOutput === tableButton[1] ? {} : null;
    if (selector === '[data-testid="construction-preview"]') return {
      dataset: { previewStatus: state.previewStatus, previewOutputId: state.previewOutputId },
      querySelector: inner => inner === '[role="table"]' && state.hasTable ? {} : null,
    };
    if (selector === '[data-testid="preview-table-scroll"] [role="table"]') {
      return state.hasTable ? { getAttribute: name => name === 'aria-rowcount' ? String(state.rowCount + 1) : null } : null;
    }
    if (selector === '[data-testid="construction-proposal-panel"]') return state.proposal ? {} : null;
    if (selector === '[data-testid="construction-operation-editor"]') return state.editor ? {} : null;
    if (selector === '[data-testid="construction-history"]') return state.history ? {} : null;
    return null;
  },
});

const evaluate = (expression, state) => runInNewContext(expression, { document: documentFor(state) });

test('reload readiness rejects a ready preview for an output that is not the restored active table', () => {
  const state = { activeOutput: 'patient-output', previewStatus: 'ready', previewOutputId: target, rowCount: 2, hasTable: true };
  assert.equal(state.previewStatus === 'ready' && state.previewOutputId === target && state.rowCount === 2, true,
    'the old preview-only predicate would accept this state');
  assert.equal(evaluate(selectedSavedReady(target, 2), state), false);
  state.activeOutput = target;
  assert.equal(evaluate(selectedSavedReady(target, 2), state), true);
});

test('empty-target readiness waits for the restored empty output and closed editor state', () => {
  const state = { activeOutput: target, previewStatus: 'empty', previewOutputId: target,
    hasTable: false, proposal: false, editor: true, history: false };
  assert.equal(evaluate(emptyTargetReady(target), state), false);
  state.editor = false;
  assert.equal(evaluate(emptyTargetReady(target), state), true);
});

test('reload sequence waits for target preview before reload and rejects wrong restoration without reselecting', async () => {
  const state = { activeOutput: 'patient-output', previewStatus: 'previewing', previewOutputId: target,
    rowCount: 2, hasTable: true };
  const events = [];
  let releasePreview;
  const previewGate = new Promise(resolve => { releasePreview = resolve; });
  const sequence = reloadSelectedTarget({
    select: async () => { events.push('select target'); state.activeOutput = target; },
    waitReady: async phase => {
      events.push(phase);
      if (phase === 'before reload') {
        assert.equal(evaluate(selectedSavedReady(target, 2), state), false);
        await previewGate;
        state.previewStatus = 'ready';
      }
      assert.equal(evaluate(selectedSavedReady(target, 2), state), true, `target was not ready ${phase}`);
    },
    reload: async () => { events.push('reload'); state.activeOutput = 'patient-output'; },
  });
  await Promise.resolve();
  assert.deepEqual(events, ['select target', 'before reload']);
  releasePreview();
  await assert.rejects(sequence, /target was not ready after reload/);
  assert.deepEqual(events, ['select target', 'before reload', 'reload', 'after reload']);
});

test('selection reload correlation accepts only the exact UI-proxy project, explorer, and revision GET', () => {
  const uiOrigin = 'http://127.0.0.1:30008';
  const apiOrigin = 'http://127.0.0.1:8188';
  const path = '/api/v1/projects/project-1/explorers/explorer-1/selections/selection-1';
  assert.equal(exactSelectionReadMatches('GET', `${uiOrigin}${path}?limit=100`, uiOrigin, path), true);
  assert.equal(exactSelectionReadMatches('GET', `${apiOrigin}${path}`, uiOrigin, path), false);
  assert.equal(exactSelectionReadMatches('GET', `${uiOrigin}/api/v1/projects/project-2/explorers/explorer-1/selections/selection-1`, uiOrigin, path), false);
  assert.equal(exactSelectionReadMatches('GET', `${uiOrigin}/api/v1/projects/project-1/explorers/explorer-2/selections/selection-1`, uiOrigin, path), false);
  assert.equal(exactSelectionReadMatches('GET', `${uiOrigin}/api/v1/projects/project-1/explorers/explorer-1/selections/selection-2`, uiOrigin, path), false);
  assert.equal(exactSelectionReadMatches('POST', `${uiOrigin}${path}`, uiOrigin, path), false);
});

test('selection response uses Request.response even when the response event already arrived', async () => {
  const response = { status: () => 200, finished: async () => null };
  const request = { response: async () => response };
  assert.equal(await readReloadedSelectionResponse(request, () => 100, 'selection-1'), response);
});

test('selection response and body must finish within the same action deadline', async () => {
  await assert.rejects(
    readReloadedSelectionResponse({ response: async () => ({ status: () => 200, finished: () => new Promise(() => {}) }) },
      () => 15, 'selection-1'),
    /Reloaded selection selection-1 response body exceeded the remaining action deadline/,
  );
});
