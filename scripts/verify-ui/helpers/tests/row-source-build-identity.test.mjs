import assert from 'node:assert/strict';
import test from 'node:test';
import { collectBuildIdentity, parseBuildIdentity } from '../../../loom-dev.mjs';

const DIGEST_A = 'a'.repeat(64);
const DIGEST_B = 'b'.repeat(64);
const DIGEST_C = 'c'.repeat(64);

test('parseBuildIdentity maps a healthy stamp triple, recorded build json, and git head', () => {
  const parsed = parseBuildIdentity({
    stampOutput: `${DIGEST_A} ${DIGEST_A} ${DIGEST_B}\n`,
    recordedOutput: JSON.stringify({ source: DIGEST_A, binary: DIGEST_B }),
    gitHeadOutput: '0123456789abcdef0123456789abcdef01234567\n',
  });
  assert.equal(parsed.expectedSource, DIGEST_A);
  assert.equal(parsed.liveSource, DIGEST_A);
  assert.equal(parsed.runningBinary, DIGEST_B);
  assert.equal(parsed.recordedSource, DIGEST_A);
  assert.equal(parsed.recordedBinary, DIGEST_B);
  assert.equal(parsed.gitHead, '0123456789abcdef0123456789abcdef01234567');
});

test('parseBuildIdentity nulls absent or malformed inputs without throwing', () => {
  const empty = parseBuildIdentity({});
  assert.deepEqual(
    [empty.expectedSource, empty.liveSource, empty.runningBinary, empty.recordedSource, empty.recordedBinary, empty.gitHead],
    [null, null, null, null, null, null],
  );
  const malformed = parseBuildIdentity({ stampOutput: 'not-a-triple more fields', recordedOutput: '{broken', gitHeadOutput: '   ' });
  assert.equal(malformed.expectedSource, 'not-a-triple');
  assert.equal(malformed.liveSource, 'more');
  assert.equal(malformed.runningBinary, 'fields');
  assert.equal(malformed.recordedSource, null);
  assert.equal(malformed.recordedBinary, null);
  assert.equal(malformed.gitHead, null);
});

test('collectBuildIdentity probes the resolved container via docker exec and derives freshness fields', () => {
  const calls = [];
  const target = {
    composeProject: 'loom-dev-unittest',
    sourceRoot: process.cwd(),
    fixtureProject: 'unittest-fixture',
    fixtureGeneration: 'unittest-gen',
    host: '127.0.0.1',
    apiPort: 8182,
    uiPort: 3182,
    populationMappingCursorSecret: 'unittest-cursor-secret',
  };
  const runner = (args) => {
    calls.push(args);
    if (args[2] === '/workspace/loom-dev-build-stamp.sh') {
      return { status: 0, stdout: `${DIGEST_A} ${DIGEST_A} ${DIGEST_B}\n`, stderr: '' };
    }
    if (args[2] === 'cat') {
      assert.equal(args[3], '/workspace/tmp/arango-fhir-server.build.json');
      return { status: 0, stdout: JSON.stringify({ source: DIGEST_A, binary: DIGEST_B }), stderr: '' };
    }
    throw new Error(`unexpected docker exec args: ${JSON.stringify(args)}`);
  };
  const psRunner = (args) => {
    assert.equal(args[0], 'ps');
    assert(args.includes(`label=com.docker.compose.project=${target.composeProject}`));
    assert(args.includes('label=com.docker.compose.service=loom-api'));
    return { status: 0, stdout: `${DIGEST_C.slice(0, 12)}\n`, stderr: '' };
  };
  const identity = collectBuildIdentity(target, { repoRoot: process.cwd(), runner, psRunner });
  assert.equal(identity.checkExit, 0);
  assert.equal(identity.expectedSource, DIGEST_A);
  assert.equal(identity.liveSource, DIGEST_A);
  assert.equal(identity.runningBinary, DIGEST_B);
  assert.equal(identity.recordedSource, DIGEST_A);
  assert.equal(identity.recordedBinary, DIGEST_B);
  assert.equal(calls.length, 2, 'must probe the stamp script and the recorded build json exactly once each');
  assert.equal(calls[0][0], 'exec');
  assert.equal(calls[0][1], DIGEST_C.slice(0, 12));
  assert.equal(calls[0][2], '/workspace/loom-dev-build-stamp.sh');
  assert.equal(calls[0][3], '--check');
});
