import test from 'node:test';
import assert from 'node:assert/strict';
import { isLoadedBuilderSnapshot } from '../../workflows/builder-load.mjs';

test('Builder readiness accepts the selected empty workspace or a populated ready preview', () => {
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: true, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), true);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 1, previewStatus: 'ready',
  }, 'owned-explorer'), true);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'other-explorer', emptyWorkspaceVisible: true, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), false);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 1, previewStatus: 'loading',
  }, 'owned-explorer'), false);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), false);
});
