import assert from 'node:assert/strict';
import test from 'node:test';
import {
  ApiBuildFreezeError,
  captureApiBuildFreeze,
  checkContainerApiBuildStamp,
  localCDAApiContainer,
} from './api-build-freeze.mjs';

const stamp = (char) => `${char.repeat(64)} ${char.repeat(64)} ${char.repeat(64)}\n`;
const result = (status, stdout, extras = {}) => ({ status, stdout, ...extras });

const expectInvalidatedAtEnd = async (beforeResult, afterResult, reason) => {
  let call = 0;
  const freeze = await captureApiBuildFreeze(async () => [beforeResult, afterResult][call++]);
  await assert.rejects(freeze.assertUnchanged(), (error) => {
    assert(error instanceof ApiBuildFreezeError);
    assert.equal(error.reason, reason);
    assert.equal(error.invalidatesRun, true);
    assert.equal(error.productFailure, false);
    assert(!JSON.stringify(error).includes('a'.repeat(64)));
    return true;
  });
};

test('matching fresh running API stamps pass without exposing digests', async () => {
  let calls = 0;
  const freeze = await captureApiBuildFreeze(async () => {
    calls += 1;
    return result(0, stamp('a'));
  });
  assert.deepEqual(freeze.initial, { checked: true, fresh: true, status: 0 });
  assert.deepEqual(await freeze.assertUnchanged(), {
    checked: true, unchanged: true, after: { checked: true, fresh: true, status: 0 },
    invalidatesRun: false, productFailure: false,
  });
  assert.equal(calls, 2);
  assert(!JSON.stringify(freeze.initial).includes('a'.repeat(64)));
});

test('a fresh stamp change during the verifier invalidates the run', async () => {
  await expectInvalidatedAtEnd(result(0, stamp('a')), result(0, stamp('b')), 'the running API build stamp changed during the run');
});

test('a stale initial stamp rejects capture before the verifier can start', async () => {
  let calls = 0;
  await assert.rejects(captureApiBuildFreeze(async () => {
    calls += 1;
    return result(1, stamp('a'));
  }), (error) => {
    assert(error instanceof ApiBuildFreezeError);
    assert.equal(error.reason, 'the initial running API stamp check failed');
    assert.deepEqual(error.before, { checked: true, fresh: false, status: 1 });
    assert.deepEqual(error.after, { checked: false });
    assert.equal(error.invalidatesRun, true);
    assert.equal(error.productFailure, false);
    assert(!String(error).includes('a'.repeat(64)));
    return true;
  });
  assert.equal(calls, 1, 'stale start must stop before later work');
});

test('a stale final API invalidates the run', async () => {
  await expectInvalidatedAtEnd(result(0, stamp('a')), result(1, stamp('a')), 'the final running API stamp check failed');
});

test('a failed final stamp command is sanitized in the invalidation report', async () => {
  let calls = 0;
  const freeze = await captureApiBuildFreeze(async () => {
    calls += 1;
    if (calls === 1) return result(0, stamp('a'));
    const error = new Error('credential=private-value');
    error.code = 'ENOENT';
    throw error;
  });
  await assert.rejects(freeze.assertUnchanged(), (error) => {
    assert(error instanceof ApiBuildFreezeError);
    assert.equal(error.invalidatesRun, true);
    assert.equal(error.productFailure, false);
    assert.equal(error.after.errorCode, 'ENOENT');
    assert(!String(error).includes('private-value'));
    assert(!JSON.stringify(error).includes('private-value'));
    return true;
  });
});

test('the optional Docker adapter targets the selected local CDA API container', async () => {
  assert.equal(localCDAApiContainer({}), 'loom-dev-6d7df93d6a37-loom-api-1');
  assert.equal(localCDAApiContainer({ LOOM_API_CONTAINER: 'custom-api' }), 'custom-api');
  const calls = [];
  const checked = await checkContainerApiBuildStamp('custom-api', {
    execFileImpl: (file, args, options, callback) => {
      calls.push({ file, args, options });
      callback(null, stamp('c'));
    },
  });
  assert.equal(checked.status, 0);
  assert.equal(checked.stdout, stamp('c'));
  assert.deepEqual(calls[0].args, ['exec', 'custom-api', '/workspace/loom-dev-build-stamp.sh', '--check']);
  assert.equal(calls[0].options.encoding, 'utf8');
});
