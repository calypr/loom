import assert from 'node:assert/strict';
import test from 'node:test';
import { assertCdaNoAuthRuntime } from '../cda-no-auth-runtime.mjs';

const validArgs = {
  apiContainer: 'loom-cda-api',
  dockerTop: () => 'PID COMMAND\n41 /bin/sh -c ./tmp/arango-fhir-server --listen :8080 --no-auth --database loom_dev\n42 /workspace/tmp/arango-fhir-server --listen :8080 --no-auth --database loom_dev',
};

test('CDA raw-oracle preflight verifies --no-auth on the exact owned API runtime process', () => {
  assert.deepEqual(assertCdaNoAuthRuntime(validArgs), {
    apiContainer: 'loom-cda-api', executable: 'arango-fhir-server', noAuthArgumentVerified: true,
  });
  assert.throws(() => assertCdaNoAuthRuntime({ ...validArgs,
    dockerTop: () => 'PID COMMAND\n42 /workspace/tmp/arango-fhir-server --listen :8080',
  }), /authorization fixture mismatch.*--no-auth/);
  assert.throws(() => assertCdaNoAuthRuntime({ ...validArgs,
    dockerTop: () => 'PID COMMAND\n42 /bin/sh -c ./tmp/arango-fhir-server --listen :8080 --no-auth',
  }), /authorization fixture mismatch.*exactly one arango-fhir-server process/);
});
