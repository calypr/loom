import assert from 'node:assert/strict';
import test from 'node:test';
import { updateDemoNavigationURL, viewerProjectID } from './demo-route.mjs';

const startingURL = 'http://127.0.0.1:30008/?project=auth&organization=loom_dev_scope001&explorer=one&selection=revision-1&mode=viewer';

test('demo navigation retains organization across mode, Explorer, and repair routes', () => {
  const modeURL = updateDemoNavigationURL(new URL(startingURL), {
    kind: 'mode', project: 'auth', organization: 'loom_dev_scope001', explorerId: 'one', mode: 'builder',
  });
  assert.equal(modeURL.searchParams.get('organization'), 'loom_dev_scope001');
  assert.equal(modeURL.searchParams.get('mode'), 'builder');
  assert.equal(modeURL.searchParams.get('selection'), 'revision-1');

  const explorerURL = updateDemoNavigationURL(modeURL, {
    kind: 'explorer', project: 'auth', organization: 'loom_dev_scope001', explorerId: 'two',
  });
  assert.equal(explorerURL.searchParams.get('organization'), 'loom_dev_scope001');
  assert.equal(explorerURL.searchParams.get('explorer'), 'two');
  assert.equal(explorerURL.searchParams.get('mode'), 'builder');

  const repairURL = updateDemoNavigationURL(explorerURL, {
    kind: 'repair', project: 'auth', organization: 'loom_dev_scope001', explorerId: 'two',
    focus: { outputId: 'output-1', column: 'observation-id', label: 'Observation ID' },
  });
  assert.equal(repairURL.searchParams.get('organization'), 'loom_dev_scope001');
  assert.equal(repairURL.searchParams.get('mode'), 'builder');
  assert.equal(repairURL.searchParams.get('focusOutput'), 'output-1');
  assert.equal(repairURL.searchParams.get('focusColumn'), 'observation-id');
  assert.equal(repairURL.searchParams.get('focusLabel'), 'Observation ID');
});

test('legacy project-only routes stay organization-free', () => {
  const url = updateDemoNavigationURL(new URL('http://127.0.0.1:30008/?organization=stale'), {
    kind: 'explorer', project: 'legacy-project', explorerId: 'default',
  });
  assert.equal(url.searchParams.get('project'), 'legacy-project');
  assert.equal(url.searchParams.has('organization'), false);
});

test('Viewer receives the canonical organization/project ID while legacy IDs stay unchanged', () => {
  assert.equal(viewerProjectID('loom_dev_scope001', 'auth'), 'loom_dev_scope001/auth');
  assert.equal(viewerProjectID(undefined, 'legacy-project'), 'legacy-project');
});
