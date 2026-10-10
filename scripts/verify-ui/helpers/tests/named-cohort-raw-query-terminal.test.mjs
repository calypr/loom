import assert from 'node:assert/strict';
import test from 'node:test';
import { createRawQuery } from '../../workflows/verify-cda-named-cohort-related-count-browser.mjs';

test('named cohort raw query preserves bounded terminal evidence when spawnSync returns null status', () => {
  const nowValues = [100, 148];
  const stdout = `Bearer secret-token ${'x'.repeat(1500)}`;
  const stderr = `password=secret-value ${'y'.repeat(1500)}`;
  const rawQuery = createRawQuery('arango-container', {
    now: () => nowValues.shift(),
    spawn: () => ({
      status: null,
      signal: 'SIGTERM',
      error: Object.assign(new Error('spawnSync rtk ENOBUFS api_key=secret-key'), { code: 'ENOBUFS' }),
      stdout,
      stderr,
    }),
  });

  assert.throws(() => rawQuery('FOR doc IN Specimen RETURN doc'), error => {
    assert.match(error.message, /elapsedMs=48, status=null, signal=SIGTERM/);
    assert.match(error.message, /error: code=ENOBUFS message=spawnSync rtk ENOBUFS \[REDACTED\]/);
    assert.match(error.message, /stdout: Bearer \[REDACTED\] x{20,}… \[truncated \d+ chars\]/);
    assert.match(error.message, /stderr: \[REDACTED\] y{20,}… \[truncated \d+ chars\]/);
    assert.doesNotMatch(error.message, /secret-token|secret-value|secret-key/);
    assert.ok(error.message.length < 2500, 'terminal evidence must remain bounded');
    return true;
  });
});

test('named cohort raw query still parses successful terminal output', () => {
  let invocation;
  const rawQuery = createRawQuery('arango-container', {
    now: () => 100,
    spawn: (...args) => {
      invocation = args;
      return { status: 0, signal: null, error: null, stdout: 'arangosh banner\n[{"id":"specimen-1"}]', stderr: '' };
    },
  });

  assert.deepEqual(rawQuery('FOR doc IN Specimen RETURN doc'), [{ id: 'specimen-1' }]);
  assert.equal(invocation[0], 'rtk');
  assert.equal(invocation[1][3], 'arango-container');
  assert.equal(invocation[2].timeout, 30000);
});
