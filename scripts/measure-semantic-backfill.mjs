#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import process from 'node:process';

const defaults = {
  container: 'loom-dev-6d7df93d6a37-arangodb-1',
  database: 'loom_dev',
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  intervalMs: 10_000,
  samples: 4,
};

const optionNames = new Map([
  ['--container', 'container'],
  ['--database', 'database'],
  ['--project', 'project'],
  ['--generation', 'generation'],
  ['--interval-ms', 'intervalMs'],
  ['--samples', 'samples'],
]);

const parseArgs = (argv) => {
  const options = { ...defaults };
  for (let index = 0; index < argv.length; index += 2) {
    const key = optionNames.get(argv[index]);
    assert.ok(key && argv[index + 1], `unknown or incomplete option ${argv[index] ?? ''}`);
    options[key] = key === 'intervalMs' || key === 'samples'
      ? Number.parseInt(argv[index + 1], 10)
      : argv[index + 1];
  }
  assert.ok(Number.isInteger(options.intervalMs) && options.intervalMs >= 250, '--interval-ms must be at least 250');
  assert.ok(Number.isInteger(options.samples) && options.samples >= 2, '--samples must be at least 2');
  return options;
};

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

const readSample = (options) => {
  const query = `FOR d IN fhir_semantic_inventory_builds
    FILTER d.project == ${JSON.stringify(options.project)}
    FILTER d.dataset_generation == ${JSON.stringify(options.generation)}
    SORT d.rule_version DESC, d.observation_schema DESC, d.build_id DESC
    LIMIT 1
    RETURN {buildId: d.build_id, ruleVersion: d.rule_version, state: d.state, scannedResources: d.scanned_resources, checkpoint: d.source_checkpoint}`;
  const program = `
const rows = db._query(
  ${JSON.stringify(query)}
).toArray();
if (rows.length !== 1) throw new Error("semantic inventory build not found");
print(JSON.stringify(rows[0]));`;
  const output = execFileSync('docker', [
    'exec', options.container,
    'arangosh', '--server.endpoint', 'tcp://127.0.0.1:8529',
    '--server.database', options.database,
    '--server.username', 'root', '--server.password', '',
    '--javascript.execute-string', program,
  ], { encoding: 'utf8' });
  return { atMs: Date.now(), ...JSON.parse(output.trim()) };
};

const median = (values) => {
  const sorted = [...values].sort((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0
    ? (sorted[middle - 1] + sorted[middle]) / 2
    : sorted[middle];
};

const main = async () => {
  const options = parseArgs(process.argv.slice(2));
  const samples = [];
  for (let index = 0; index < options.samples; index += 1) {
    samples.push(readSample(options));
    if (index + 1 < options.samples) await sleep(options.intervalMs);
  }
  const buildIds = new Set(samples.map((sample) => sample.buildId));
  assert.equal(buildIds.size, 1, 'active semantic inventory build changed during measurement');
  const windows = samples.slice(1).map((sample, index) => {
    const previous = samples[index];
    const elapsedSeconds = (sample.atMs - previous.atMs) / 1000;
    const resources = sample.scannedResources - previous.scannedResources;
    return {
      elapsedSeconds,
      resources,
      resourcesPerSecond: resources / elapsedSeconds,
      fromCheckpoint: previous.checkpoint,
      toCheckpoint: sample.checkpoint,
    };
  });
  process.stdout.write(`${JSON.stringify({
    buildId: samples[0].buildId,
    ruleVersion: samples[0].ruleVersion,
    states: [...new Set(samples.map((sample) => sample.state))],
    intervalMs: options.intervalMs,
    windows,
    medianResourcesPerSecond: median(windows.map((window) => window.resourcesPerSecond)),
  }, null, 2)}\n`);
};

await main();
