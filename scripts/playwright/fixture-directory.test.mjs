import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { environmentForFixtureDir } from '../verify-ui/fixture-environment.mjs';

const makeFixture = (root, name, records) => {
  const directory = join(root, name);
  mkdirSync(directory, { recursive: true });
  for (const [file, value] of Object.entries(records)) writeFileSync(join(directory, file), value);
  return directory;
};

test('a per-test fixture directory selects its own source data without mutating process environment', (t) => {
  const sourceRoot = mkdtempSync(join(tmpdir(), 'loom-fault-fixture-source-'));
  t.after(() => rmSync(sourceRoot, { recursive: true, force: true }));
  const ordinaryFixture = makeFixture(sourceRoot, 'testdata/devloop-fixture', {
    'Patient.ndjson': '{"id":"ordinary"}\n',
    'Observation.ndjson': '{"id":"ordinary-observation"}\n',
  });
  const combineFixture = makeFixture(sourceRoot, 'testdata/verify-combine', {
    'Patient.ndjson': '{"id":"combine"}\n',
    'Observation.ndjson': '{"id":"combine-observation"}\n',
  });
  const env = { LOOM_DEV_SOURCE_ROOT: sourceRoot, LOOM_DEV_FIXTURE_DIR: ordinaryFixture };

  const selected = environmentForFixtureDir(env, 'testdata/verify-combine');
  assert.equal(selected.LOOM_DEV_FIXTURE_DIR, resolve(combineFixture));
  assert.deepEqual(readdirSync(selected.LOOM_DEV_FIXTURE_DIR).sort(), ['Observation.ndjson', 'Patient.ndjson']);
  assert.equal(env.LOOM_DEV_FIXTURE_DIR, ordinaryFixture);
  assert.notEqual(selected, env);

  assert.equal(environmentForFixtureDir(env, undefined), env);
  assert.equal(environmentForFixtureDir(env, ''), env);
});

test('an absolute per-test fixture directory remains absolute and requires a named source root', (t) => {
  const sourceRoot = mkdtempSync(join(tmpdir(), 'loom-fault-fixture-source-'));
  const absoluteFixture = mkdtempSync(join(tmpdir(), 'loom-fault-fixture-data-'));
  t.after(() => {
    rmSync(sourceRoot, { recursive: true, force: true });
    rmSync(absoluteFixture, { recursive: true, force: true });
  });
  const env = { LOOM_DEV_SOURCE_ROOT: sourceRoot };
  assert.equal(environmentForFixtureDir(env, absoluteFixture).LOOM_DEV_FIXTURE_DIR, absoluteFixture);
  assert.throws(() => environmentForFixtureDir({}, 'testdata/verify-combine'), /LOOM_DEV_SOURCE_ROOT is required/);
});
