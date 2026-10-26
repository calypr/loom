import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const TEST_DIR = dirname(fileURLToPath(import.meta.url));
const ROOT = realpathSync(resolve(TEST_DIR, '../../../..'));
const MODEL_DIR = join(ROOT, 'verification/builder-interactions');
const MODULE = 'TemporalFilterJourney';
const TLC_VERSION = '1.7.4';
const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';

function tlcJar() {
  const path = process.env.TLA2TOOLS_JAR ??
    join(tmpdir(), 'loom-builder-interactions-tools', `tla2tools-${TLC_VERSION}.jar`);
  assert(existsSync(path), `TLC ${TLC_VERSION} is missing from the project cache at ${path}.`);
  const actual = createHash('sha256').update(readFileSync(path)).digest('hex');
  assert.equal(actual, TLC_SHA256, `TLC ${TLC_VERSION} cache checksum mismatch at ${path}.`);
  return realpathSync(path);
}

function runTlc(config, { mutateApplyGuard = false, disableDeadlockCheck = false } = {}) {
  const jar = tlcJar();
  const work = mkdtempSync(join(tmpdir(), 'loom-temporal-filter-tlc-'));
  const modelDirectory = join(work, 'model');
  const metadataDirectory = join(work, 'states');
  mkdirSync(modelDirectory);
  mkdirSync(metadataDirectory);
  const sourcePath = join(MODEL_DIR, `${MODULE}.tla`);
  let source = readFileSync(sourcePath, 'utf8');
  if (mutateApplyGuard) {
    const original = 'EventFits(event, action, journey.mountEpoch, TRUE, TRUE)';
    assert.equal(source.split(original).length - 1, 1, 'the abstract CAS-path mutation target must be unique');
    source = source.replace(original, 'EventFits(event, action, journey.mountEpoch, TRUE, FALSE)');
  }
  writeFileSync(join(modelDirectory, `${MODULE}.tla`), source);
  copyFileSync(join(MODEL_DIR, config), join(modelDirectory, config));

  const args = [
    '-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC',
    ...(disableDeadlockCheck ? ['-deadlock'] : []),
    '-workers', '1', '-metadir', metadataDirectory,
    '-config', config, `${MODULE}.tla`,
  ];
  const command = ['java', ...args].join(' ');
  const result = spawnSync('java', args, {
    cwd: modelDirectory, encoding: 'utf8', maxBuffer: 8 * 1024 * 1024,
  });
  const output = (result.stdout ?? '') + (result.stderr ?? '');
  return { work, command, result, output };
}

function diagnostics(output) {
  const lines = output.split(/\r?\n/);
  const matches = lines.filter(line => /Error:|Invariant .* violated|Temporal property .* violated|Model checking completed/.test(line));
  const cursor = [...output.matchAll(/cursor \|-> (\d+)/g)].at(-1)?.[1] ?? 'unknown';
  const action = [...output.matchAll(/lastAction \|-> "([^"]+)"/g)].at(-1)?.[1] ?? 'unknown';
  return `${output.slice(0, 4_000)}\n${matches.join('\n')}\nlast state cursor=${cursor}, action=${action}`;
}

function stateSummary(output) {
  const match = output.match(/([0-9,]+) states generated, ([0-9,]+) distinct states found/);
  assert(match, 'TLC must report the completed state graph size.');
  return `${match[1]} states generated, ${match[2]} distinct`;
}

test('DATE temporal filter lifecycle completes its bounded TLC schedule', { timeout: 120_000 }, (t) => {
  const run = runTlc(`${MODULE}Date.cfg`);
  t.after(() => rmSync(run.work, { recursive: true, force: true }));
  assert.ifError(run.result.error);
  assert.equal(run.result.status, 0, diagnostics(run.output));
  assert.match(run.output, /Model checking completed\. No error has been found\./);
  assert.match(run.output, /[1-9][0-9,]* states generated, [1-9][0-9,]* distinct states found/);
  console.log(`TLC command: ${run.command}`);
  console.log(`DATE TLC passed with ${stateSummary(run.output)}. This is abstract model evidence, not UI evidence.`);
});

test('DATE_TIME temporal filter lifecycle preserves the request/saved boundary normalization', { timeout: 120_000 }, (t) => {
  const run = runTlc(`${MODULE}DateTime.cfg`);
  t.after(() => rmSync(run.work, { recursive: true, force: true }));
  assert.ifError(run.result.error);
  assert.equal(run.result.status, 0, diagnostics(run.output));
  assert.match(run.output, /Model checking completed\. No error has been found\./);
  assert.match(run.output, /[1-9][0-9,]* states generated, [1-9][0-9,]* distinct states found/);
  console.log(`TLC command: ${run.command}`);
  console.log(`DATE_TIME TLC passed with ${stateSummary(run.output)}. This is abstract model evidence, not UI evidence.`);
});

test('abstract mutation that disables Apply is rejected by the completion property', { timeout: 120_000 }, (t) => {
  const run = runTlc(`${MODULE}Date.cfg`, { mutateApplyGuard: true, disableDeadlockCheck: true });
  t.after(() => rmSync(run.work, { recursive: true, force: true }));
  assert.ifError(run.result.error);
  assert.notEqual(run.result.status, 0, diagnostics(run.output));
  assert.match(diagnostics(run.output), /Temporal properties were violated|Temporal property.*violated/s);
  console.log(`TLC command: ${run.command}`);
  console.log('Expected abstract model mutation was rejected. This is not a real UI regression result.');
});
