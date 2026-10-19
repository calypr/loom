import assert from 'node:assert/strict';
import test from 'node:test';
import {
  fixtureExplorerRequestPathMatches,
  fixtureProjectExplorerCollectionPath,
} from '../fixture-context.mjs';

const project = 'loom_dev_verify_mv37bf7o-a7738e2';
const selectedExplorer = 'explorer-123';
const encodedCollection = '/api/v1/projects/loom_dev_verify_mv37bf7o%252Fa7738e2/explorers';

test('fixture observer owns the canonical browser project path and selected Explorer subtree', () => {
  assert.equal(fixtureProjectExplorerCollectionPath(project), encodedCollection);
  assert.equal(fixtureExplorerRequestPathMatches(encodedCollection, project, selectedExplorer), true);
  assert.equal(
    fixtureExplorerRequestPathMatches(`${encodedCollection}/${selectedExplorer}/authoring/v2/builder`, project, selectedExplorer),
    true,
  );
  assert.equal(
    fixtureExplorerRequestPathMatches('/api/v1/projects/loom_dev_verify_mv37bf7o-a7738e2/explorers', project, selectedExplorer),
    false,
  );
  assert.equal(
    fixtureExplorerRequestPathMatches(`${encodedCollection}/another-explorer/authoring/v2/builder`, project, selectedExplorer),
    false,
  );
});
