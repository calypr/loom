import assert from 'node:assert/strict';
import test from 'node:test';
import { planFocusedCheckGroups } from '../focused-check-groups.mjs';

const repoRoot = '/workspace/loom';

test('maps allowlisted command arrays to fixed Node and Vitest invocations', () => {
  const plans = planFocusedCheckGroups([
    {
      id: 'native-oracles',
      cwd: '.',
      command: ['node-test', 'scripts/verify-ui/helpers/tests/native-oracles.test.mjs'],
    },
    {
      id: 'loom-ui-units',
      cwd: 'ui/packages/loom-ui',
      command: [
        'vitest',
        'run',
        '--config',
        'vitest.config.ts',
        'ui/packages/loom-ui/src/Builder.unit.test.tsx',
        'ui/packages/loom-ui/src/types.unit.test.ts',
      ],
    },
  ], repoRoot);

  assert.deepEqual(plans, [
    {
      id: 'native-oracles',
      runner: 'node-test',
      executable: process.execPath,
      args: ['--test', 'scripts/verify-ui/helpers/tests/native-oracles.test.mjs'],
      cwd: '/workspace/loom',
    },
    {
      id: 'loom-ui-units',
      runner: 'vitest',
      executable: process.execPath,
      args: [
        '../../node_modules/vitest/vitest.mjs',
        'run',
        '--config',
        'vitest.config.ts',
        'src/Builder.unit.test.tsx',
        'src/types.unit.test.ts',
      ],
      cwd: '/workspace/loom/ui/packages/loom-ui',
    },
  ]);
});

test('rejects traversal and absolute working directories or command paths', () => {
  const valid = { id: 'safe-group', cwd: '.', command: ['node-test', 'scripts/safe.test.mjs'] };
  for (const cwd of ['../outside', '/tmp/outside', 'C:\\outside']) {
    assert.throws(() => planFocusedCheckGroups([{ ...valid, cwd }], repoRoot), /cwd/);
  }
  for (const file of ['../outside.test.mjs', '/tmp/outside.test.mjs', 'C:\\outside.test.mjs', 'scripts/../outside.test.mjs']) {
    assert.throws(() => planFocusedCheckGroups([{ ...valid, command: ['node-test', file] }], repoRoot), /command/);
  }
  const vitest = { id: 'ui-unit', cwd: 'ui/packages/loom-ui', command: ['vitest', 'run', '--config', 'vitest.config.ts', 'scripts/outside.test.ts'] };
  assert.throws(() => planFocusedCheckGroups([vitest], repoRoot), /inside ui\/packages\/loom-ui/);
});

test('rejects unlisted runners, flags, malformed commands, and invalid test extensions', () => {
  assert.throws(() => planFocusedCheckGroups([
    { id: 'shell', cwd: '.', command: ['sh', '-c', 'echo unsafe'] },
  ], repoRoot), /allowlisted runner/);
  assert.throws(() => planFocusedCheckGroups([
    { id: 'node-flags', cwd: '.', command: ['node-test', '--test', 'scripts/safe.test.mjs'] },
  ], repoRoot), /path segments|node-test files/);
  assert.throws(() => planFocusedCheckGroups([
    { id: 'vitest-flags', cwd: 'ui/packages/loom-ui', command: ['vitest', 'run', '--config', 'vitest.config.ts', '--coverage', 'ui/packages/loom-ui/src/unit.test.ts'] },
  ], repoRoot), /path segments|Vitest files/);
  assert.throws(() => planFocusedCheckGroups([
    { id: 'bad-vitest-prefix', cwd: 'ui/packages/loom-ui', command: ['vitest', 'run', '--reporter', 'verbose', 'ui/packages/loom-ui/src/unit.test.ts'] },
  ], repoRoot), /must begin/);
  assert.throws(() => planFocusedCheckGroups([
    { id: 'missing-files', cwd: '.', command: ['node-test'] },
  ], repoRoot), /test file/);
  assert.throws(() => planFocusedCheckGroups([
    { id: 'bad-extension', cwd: '.', command: ['node-test', 'scripts/safe.ts'] },
  ], repoRoot), /node-test files/);
});

test('keeps shell metacharacters inside one argv entry and emits no shell command', () => {
  const file = 'scripts/tests/name; touch /tmp/focused-check-pwned.test.mjs';
  const [plan] = planFocusedCheckGroups([
    { id: 'metacharacter-path', cwd: '.', command: ['node-test', file] },
  ], repoRoot);

  assert.deepEqual(plan.args, ['--test', file]);
  assert.equal(plan.executable, process.execPath);
  assert.equal(Object.hasOwn(plan, 'shell'), false);
  assert.equal(Object.hasOwn(plan, 'command'), false);
});

test('rejects repeated group identifiers and repeated command paths', () => {
  const group = { id: 'same-group', cwd: '.', command: ['node-test', 'scripts/safe.test.mjs'] };
  assert.throws(() => planFocusedCheckGroups([group, { ...group }], repoRoot), /duplicate identifier/);
  assert.throws(() => planFocusedCheckGroups([{
    ...group,
    command: ['node-test', 'scripts/safe.test.mjs', 'scripts/safe.test.mjs'],
  }], repoRoot), /duplicate file/);
});

test('validates recorded implementation source paths without turning them into commands', () => {
  const [plan] = planFocusedCheckGroups([{
    id: 'source-group-regression',
    cwd: '.',
    command: ['node-test', 'scripts/safe.test.mjs'],
    sourceFiles: ['scripts/feature.mjs'],
  }], repoRoot);

  assert.deepEqual(plan.args, ['--test', 'scripts/safe.test.mjs']);
  assert.throws(() => planFocusedCheckGroups([{
    id: 'bad-source-path',
    cwd: '.',
    command: ['node-test', 'scripts/safe.test.mjs'],
    sourceFiles: ['../outside.mjs'],
  }], repoRoot), /sourceFiles/);
});
