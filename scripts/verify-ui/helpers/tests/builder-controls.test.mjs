import assert from 'node:assert/strict';
import test from 'node:test';
import { browserURL } from '../../workflows/builder-url.mjs';

test('browserURL preserves unrelated query parameters and scopes the Builder route', () => {
  const result = new URL(browserURL(
    { uiUrl: 'https://loom.example/app?keep=yes#builder' }, 'project one', 'explorer/two', 'builder',
  ));
  assert.equal(result.origin, 'https://loom.example');
  assert.equal(result.searchParams.get('keep'), 'yes');
  assert.equal(result.searchParams.get('project'), 'project one');
  assert.equal(result.searchParams.get('explorer'), 'explorer/two');
  assert.equal(result.searchParams.get('mode'), 'builder');
  assert.equal(result.hash, '#builder');
});
