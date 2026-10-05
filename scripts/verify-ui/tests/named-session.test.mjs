import assert from 'node:assert/strict';
import test from 'node:test';
import { assertNamedDevSessionEnvironment } from '../fixture-context.mjs';
import { createFreshVerificationFixture } from '../../loom-dev.mjs';

const namedTarget = {
  LOOM_DEV_SOURCE_ROOT: '/private/tmp/loom-construction-implementation',
  LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-6d7df93d6a37',
  LOOM_DEV_API_PORT: '8188',
  LOOM_DEV_UI_PORT: '30008',
  LOOM_DEV_PROJECT: 'loom_dev_c89a69d7e137',
};

test('registry workflows require explicit named target identity', () => {
  assert.throws(() => assertNamedDevSessionEnvironment({}), /named development target explicitly/);
  assert.doesNotThrow(() => assertNamedDevSessionEnvironment(namedTarget));
});

test('fresh fixture helper rejects malformed run IDs before API access', async () => {
  await assert.rejects(createFreshVerificationFixture({}, '../6d-stack'), /short lowercase slug/);
});
