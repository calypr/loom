import assert from 'node:assert/strict';
import test from 'node:test';
import { median, timingTarget } from './measure-authoring-loop.mjs';

const target = {
  apiUrl: 'http://127.0.0.1:8182', composeProject: 'loom-dev-test',
  project: 'loom_dev_verify_test', explorerId: 'loom-dev-verification-test',
};

test('timing mutations are restricted to isolated verification explorers', () => {
  assert.match(timingTarget({ target }).base, /loom_dev_verify_test/);
  for (const change of [
    { apiUrl: 'https://example.com:8182' }, { apiUrl: 'http://127.0.0.1:8080' },
    { composeProject: 'loom-demo' }, { project: 'NCPI_ACCEPTANCE' }, { explorerId: 'default' },
  ]) assert.throws(() => timingTarget({ target: { ...target, ...change } }));
});

test('median handles odd and even counts without changing samples', () => {
  const samples = [8, 2, 1];
  assert.equal(median(samples), 2);
  assert.deepEqual(samples, [8, 2, 1]);
  assert.equal(median([4, 1, 3, 2]), 2.5);
  assert.throws(() => median([]));
});
