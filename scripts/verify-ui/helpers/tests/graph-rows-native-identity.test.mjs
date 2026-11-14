import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertGraphRowsNativeIdentity } from '../graph-rows-native-identity.mjs';

const identity = () => ({
  files: { 'internal/explorer/row.go': 'source-a', 'ui/Builder.tsx': 'ui-a' },
  sourceFingerprint: 'snapshot-a',
  api: { liveSource: 'source-a', runningBinary: 'binary-a' },
  runtimeComposeConfigurationSHA256: 'compose-a', liveBuildLimits: ['2', '-p=1'], runtimeConfigurationSHA256: 'config-a', recipeReferenceSHA256: 'reference-a',
});

test('native source receipt rejects changed code, runtime binary, configuration and reference', () => {
  const before = identity();
  assert.doesNotThrow(() => assertGraphRowsNativeIdentity(before, identity()));
  for (const mutate of [
    after => { after.files['ui/Builder.tsx'] = 'ui-b'; },
    after => { delete after.files['internal/explorer/row.go']; },
    after => { after.sourceFingerprint = 'snapshot-b'; },
    after => { after.api.liveSource = 'source-b'; },
    after => { after.api.runningBinary = 'binary-b'; },
    after => { after.runtimeComposeConfigurationSHA256 = 'compose-b'; },
    after => { after.liveBuildLimits = ['4', '-p=4']; },
    after => { after.runtimeConfigurationSHA256 = 'config-b'; },
    after => { after.recipeReferenceSHA256 = 'reference-b'; },
  ]) {
    const after = identity(); mutate(after);
    assert.throws(() => assertGraphRowsNativeIdentity(before, after));
  }
});
