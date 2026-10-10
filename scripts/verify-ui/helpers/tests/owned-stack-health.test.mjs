import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assertFreshApiBuildPrecheck,
  assertCapturedTargetMatches,
  inspectBuildStamp,
  parseCapturedBuildIdentity,
  runOwnedStackHealth,
} from '../owned-stack-health.mjs';

const source = 'a'.repeat(64);
const binary = 'b'.repeat(64);
const identity = `${source}:${source}:${binary}`;
const stamp = `${source} ${source} ${binary}\n`;
const response = (status, body) => ({ status, text: async () => body });

const precheckRecord = () => ({
  checkedAt: '2026-10-06T20:00:00.000Z',
  targetContainer: 'owned-api',
  command: '/workspace/loom-dev-build-stamp.sh --check',
  exitCode: 0,
  apiBuildIdentity: identity,
  sourceDigestMatchesCurrentMountedSource: true,
  runningBinaryMatchesRecordedBuild: true,
  fresh: true,
});

test('capture accepts the owned precheck CLI record only for its exact captured API identity', () => {
  assert.equal(assertFreshApiBuildPrecheck(precheckRecord(), {
    targetContainer: 'owned-api',
    apiBuildIdentity: identity,
  }), identity);
});

test('capture rejects stale, failed, wrong-target, and mismatched-identity prechecks', () => {
  for (const [field, value, message] of [
    ['fresh', false, /must be fresh/],
    ['sourceDigestMatchesCurrentMountedSource', false, /current mounted source/],
    ['runningBinaryMatchesRecordedBuild', false, /running binary/],
    ['exitCode', 1, /exit successfully/],
  ]) {
    assert.throws(() => assertFreshApiBuildPrecheck({ ...precheckRecord(), [field]: value }, {
      targetContainer: 'owned-api',
      apiBuildIdentity: identity,
    }), message);
  }
  assert.throws(() => assertFreshApiBuildPrecheck(precheckRecord(), {
    targetContainer: 'other-api',
    apiBuildIdentity: identity,
  }), /owned API container/);
  assert.throws(() => assertFreshApiBuildPrecheck(precheckRecord(), {
    targetContainer: 'owned-api',
    apiBuildIdentity: `${'c'.repeat(64)}:${'c'.repeat(64)}:${binary}`,
  }), /changed after the fresh precheck/);
});

test('precheck stamp requires a successful fresh three-digest result', () => {
  assert.deepEqual(inspectBuildStamp({ status: 0, stdout: stamp }), {
    valid: true,
    fresh: true,
    sourceDigestMatchesCurrentMountedSource: true,
    runningBinaryMatchesRecordedBuild: true,
    apiBuildIdentity: identity,
    exitCode: 0,
  });
  assert.equal(inspectBuildStamp({ status: 1, stdout: stamp }).fresh, false);
  assert.equal(inspectBuildStamp({ status: 0, stdout: `${binary} ${source} ${binary}` }).fresh, false);
  assert.equal(inspectBuildStamp({ status: 1, stdout: '', failureKind: 'docker-access-denied', diagnostic: 'permission denied' }).failureKind,
    'docker-access-denied');
  assert.equal(inspectBuildStamp({ status: 1, stdout: `${source} ${binary} ${binary}` }).failureKind,
    'source-stamp-mismatch');
  assert.throws(() => parseCapturedBuildIdentity(`${source}:${binary}:${binary}`), /current mounted source/);
});

test('health takes three owned samples on a two-second cadence over a four-second window', async () => {
  const calls = [];
  const delays = [];
  const sampleStarts = [];
  let elapsed = 0;
  let stampChecks = 0;
  const health = await runOwnedStackHealth({
    apiURL: 'http://127.0.0.1:8188/',
    uiURL: 'http://127.0.0.1:30008',
    apiContainer: 'owned-api',
    expectedIdentity: identity,
    now: () => 'fixed-time',
    monotonicNow: () => elapsed,
    sleep: async milliseconds => {
      delays.push(milliseconds);
      elapsed += milliseconds;
    },
    fetchImpl: async url => {
      calls.push(url);
      if (url.endsWith('/readyz')) sampleStarts.push(elapsed);
      return url.endsWith('/readyz') ? response(200, '{"status":"ready"}') : response(200, '<!doctype html>');
    },
    readStamp: async container => {
      assert.equal(container, 'owned-api');
      stampChecks += 1;
      elapsed += 350;
      return { status: 0, stdout: stamp };
    },
  });
  assert.equal(health.status, 'PASS');
  assert.equal(health.samples.length, 3);
  assert.deepEqual(delays, [1650, 1650]);
  assert.deepEqual(sampleStarts, [0, 2000, 4000]);
  assert.equal(stampChecks, 3);
  assert.equal(calls.filter(url => url.endsWith('/readyz')).length, 3);
  assert.equal(calls.filter(url => url === 'http://127.0.0.1:30008/').length, 3);
  assert(health.samples.every(sample => sample.apiBuildIdentity === identity && sample.at === 'fixed-time'));
});

test('slow health samples stay serial and do not trigger catch-up starts', async () => {
  const sampleStarts = [];
  const delays = [];
  const durations = [2500, 100, 2100];
  let elapsed = 0;
  let checks = 0;
  let stampPending = false;
  const health = await runOwnedStackHealth({
    apiURL: 'http://127.0.0.1:8188', uiURL: 'http://127.0.0.1:30008', apiContainer: 'owned-api',
    expectedIdentity: identity,
    monotonicNow: () => elapsed,
    sleep: async milliseconds => {
      delays.push(milliseconds);
      elapsed += milliseconds;
    },
    fetchImpl: async url => {
      if (url.endsWith('/readyz')) {
        assert.equal(stampPending, false, 'a health sample overlapped the previous identity check');
        sampleStarts.push(elapsed);
      }
      return url.endsWith('/readyz') ? response(200, '{"status":"ready"}') : response(200, '<!doctype html>');
    },
    readStamp: async () => {
      assert.equal(stampPending, false);
      stampPending = true;
      elapsed += durations[checks++];
      await Promise.resolve();
      stampPending = false;
      return { status: 0, stdout: stamp };
    },
  });
  assert.equal(health.status, 'PASS');
  assert.deepEqual(sampleStarts, [0, 2500, 4500]);
  assert.deepEqual(delays, [1900]);
  assert(sampleStarts.slice(1).every((start, index) => start - sampleStarts[index] >= 2000));
  assert(sampleStarts[2] - sampleStarts[0] >= 4000);
  assert.equal(checks, 3);
});

test('health drains each HTTP body while its sample stamp check is pending', async () => {
  let releaseFirstStamp;
  let firstStampSettled = false;
  let bodiesReadBeforeStamp = 0;
  let elapsed = 0;
  let checks = 0;
  const firstStamp = new Promise(resolve => {
    releaseFirstStamp = () => {
      firstStampSettled = true;
      resolve({ status: 0, stdout: stamp });
    };
  });
  const healthPromise = runOwnedStackHealth({
    apiURL: 'http://127.0.0.1:8188', uiURL: 'http://127.0.0.1:30008', apiContainer: 'owned-api',
    expectedIdentity: identity,
    monotonicNow: () => elapsed,
    sleep: async milliseconds => { elapsed += milliseconds; },
    fetchImpl: async url => ({
      status: 200,
      text: async () => {
        if (!firstStampSettled) bodiesReadBeforeStamp += 1;
        return url.endsWith('/readyz') ? '{"status":"ready"}' : '<!doctype html>';
      },
    }),
    readStamp: () => checks++ === 0 ? firstStamp : Promise.resolve({ status: 0, stdout: stamp }),
  });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(bodiesReadBeforeStamp, 2);
  releaseFirstStamp();
  const health = await healthPromise;
  assert.equal(health.status, 'PASS');
  assert.equal(checks, 3);
});

test('health stops on an identity change and target comparison rejects owner drift', async () => {
  let checks = 0;
  let elapsed = 0;
  const sampleStarts = [];
  await assert.rejects(runOwnedStackHealth({
    apiURL: 'http://127.0.0.1:8188', uiURL: 'http://127.0.0.1:30008', apiContainer: 'owned-api',
    expectedIdentity: identity,
    monotonicNow: () => elapsed,
    fetchImpl: async url => {
      if (url.endsWith('/readyz')) sampleStarts.push(elapsed);
      return url.endsWith('/readyz') ? response(200, '{"status":"ready"}') : response(200, '<!doctype html>');
    },
    readStamp: async () => {
      checks += 1;
      elapsed += 2500;
      return { status: 0, stdout: checks === 1 ? stamp : `${source} ${source} ${'c'.repeat(64)}\n` };
    },
    sleep: async milliseconds => { elapsed += milliseconds; },
  }), /identity changed at sample 2/);
  assert.equal(checks, 2);
  assert.deepEqual(sampleStarts, [0, 2500]);
  const target = { project: 'p', generation: 'g1', composeProject: 'c', apiContainer: 'a', uiContainer: 'u', apiPort: '8188', uiPort: '30008', sourceRoot: '/repo' };
  assertCapturedTargetMatches(target, { ...target });
  assert.throws(() => assertCapturedTargetMatches(target, { ...target, project: 'other' }), /Owned target changed.*project/);
  assert.throws(() => assertCapturedTargetMatches(target, { ...target, generation: 'g2' }), /Owned target changed.*generation/);
});
