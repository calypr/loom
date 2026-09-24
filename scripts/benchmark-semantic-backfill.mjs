#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { unlinkSync, writeFileSync } from 'node:fs';
import process from 'node:process';
import { performance } from 'node:perf_hooks';

const defaults = {
  apiContainer: 'loom-dev-6d7df93d6a37-loom-api-1',
  arangoContainer: 'loom-dev-6d7df93d6a37-arangodb-1',
  database: 'loom_dev',
  sourceProject: 'loom_dev_cda_fhir',
  sourceGeneration: 'cda-fhir-v1',
  resourceType: 'Specimen',
  rows: 50_000,
  mode: 'backfill',
  evidence: '',
};

const optionNames = new Map([
  ['--api-container', 'apiContainer'],
  ['--arango-container', 'arangoContainer'],
  ['--database', 'database'],
  ['--source-project', 'sourceProject'],
  ['--source-generation', 'sourceGeneration'],
  ['--resource-type', 'resourceType'],
  ['--rows', 'rows'],
  ['--mode', 'mode'],
  ['--evidence', 'evidence'],
]);

const assertSafeIdentifier = (value, label) => {
  assert.match(value, /^[A-Za-z][A-Za-z0-9_-]{0,127}$/, `${label} must be an opaque identifier`);
  return value;
};

export const parseArgs = (argv) => {
  const options = { ...defaults };
  for (let index = 0; index < argv.length; index += 2) {
    const key = optionNames.get(argv[index]);
    assert.ok(key && argv[index + 1], `unknown or incomplete option ${argv[index] ?? ''}`);
    options[key] = key === 'rows' ? Number.parseInt(argv[index + 1], 10) : argv[index + 1];
  }
  assert.ok(Number.isInteger(options.rows) && options.rows > 0 && options.rows <= 50_000, '--rows must be between 1 and 50000');
  assert.ok(options.mode === 'backfill' || options.mode === 'index', '--mode must be backfill or index');
  assertSafeIdentifier(options.sourceProject, '--source-project');
  assertSafeIdentifier(options.sourceGeneration, '--source-generation');
  assertSafeIdentifier(options.resourceType, '--resource-type');
  assert.notEqual(options.sourceProject, options.sourceGeneration, 'source project and generation must be distinct');
  return options;
};

export const documentKey = (kind, ...values) => {
  const hash = createHash('sha256');
  hash.update('loom.datasetstore.v1');
  hash.update(`\0${kind}`);
  for (const value of values) hash.update(`\0${value}`);
  return `${kind}_${hash.digest('hex')}`;
};

const suffix = () => `${Date.now().toString(36)}_${randomBytes(4).toString('hex')}`;

const runDocker = (container, args, options = {}) => execFileSync('docker', ['exec', container, ...args], {
  encoding: 'utf8',
  maxBuffer: 128 * 1024 * 1024,
  ...options,
});

const runDockerClient = (args, options = {}) => execFileSync('docker', args, {
  encoding: 'utf8',
  maxBuffer: 128 * 1024 * 1024,
  ...options,
});

const arangoProgram = (options, program) => runDocker(options.arangoContainer, [
  'arangosh', '--server.endpoint', 'tcp://127.0.0.1:8529',
  '--server.database', options.database,
  '--server.username', 'root', '--server.password', '',
  '--javascript.execute-string', program,
]);

const arangoJSON = (options, query) => {
  const program = `const rows = db._query(${JSON.stringify(query)}).toArray(); print(JSON.stringify(rows));`;
  const output = arangoProgram(options, program).trim();
  const line = output.split('\n').filter(Boolean).at(-1);
  assert.ok(line, `Arango query returned no JSON: ${output}`);
  return JSON.parse(line);
};

const arangoCommand = (options, program) => {
  const output = arangoProgram(options, `${program}\nprint(JSON.stringify({ok: true}));`).trim();
  const line = output.split('\n').filter(Boolean).at(-1);
  assert.deepEqual(JSON.parse(line), { ok: true });
};

const runAQLMutation = (options, query) => {
  const result = arangoJSON(options, query);
  return result;
};

const literal = (value) => JSON.stringify(value);

const buildNames = () => {
  const id = suffix();
  return {
    project: `loom_bench_${id}`,
    generation: `semantic_hillclimb_${id}`,
    snapshotCollection: `loom_bench_snapshot_${id}`,
    binary: `/tmp/loom-semantic-backfill-${id}`,
    localBinary: `/private/tmp/loom-semantic-backfill-${id}`,
    indexedCollections: [],
    exportDirectory: `/tmp/loom-semantic-index-export-${id}`,
  };
};

const buildManifestKey = (project, generation) => documentKey('manifest', project, generation);

const prepareBenchmarkGeneration = (options, names) => {
  const sourceSchema = arangoJSON(options, `
FOR d IN loom_dataset_lifecycle
  FILTER d.recordType == "manifest"
  FILTER d.dataset.project == ${literal(options.sourceProject)}
  FILTER d.dataset.generation == ${literal(options.sourceGeneration)}
  LIMIT 1
  RETURN d.schemaIdentity`);
  assert.equal(sourceSchema.length, 1, 'source staged manifest was not found');
  const schema = { ...sourceSchema[0], generatedResourceTypes: [options.resourceType] };
  const manifestKey = buildManifestKey(names.project, names.generation);
  const cloned = arangoJSON(options, `
LET selected = (
  FOR d IN ${options.resourceType}
    FILTER d.project == ${literal(options.sourceProject)}
    FILTER d.dataset_generation == ${literal(options.sourceGeneration)}
    SORT d._key
    LIMIT ${options.rows}
    RETURN d
)
LET inserted = (
  FOR d IN selected
    LET clone = MERGE(
      UNSET(d, "_id", "_rev", "_key"),
      {
        _key: CONCAT(${literal(names.project)}, "_", d._key),
        project: ${literal(names.project)},
        dataset_generation: ${literal(names.generation)}
      }
    )
    INSERT clone INTO ${options.resourceType}
    RETURN 1
)
RETURN LENGTH(inserted)`);
  assert.deepEqual(cloned, [options.rows], `source ${options.resourceType} population is smaller than requested`);

  arangoCommand(options, `
const manifest = ${JSON.stringify({
    _key: manifestKey,
    recordType: 'manifest',
    dataset: { project: names.project, generation: names.generation },
    state: 'STAGED',
    schemaIdentity: schema,
  })};
db._query('INSERT ' + JSON.stringify(manifest) + ' INTO loom_dataset_lifecycle');`);
  arangoCommand(options, `if (!db._collection(${JSON.stringify(names.snapshotCollection)})) db._createDocumentCollection(${JSON.stringify(names.snapshotCollection)});`);
};

const buildBackfillCommand = (options, names, pageSize, batchSize) => [
  names.binary,
  'backfill-semantic-inventory',
  '--url', 'http://arangodb:8529',
  '--database', options.database,
  '--project', names.project,
  '--generation', names.generation,
  '--page-size', String(pageSize),
  '--batch-size', String(batchSize),
];

const runBackfill = (options, names, pageSize, batchSize) => {
  const started = performance.now();
  const stdout = runDocker(options.apiContainer, buildBackfillCommand(options, names, pageSize, batchSize), {
    maxBuffer: 128 * 1024 * 1024,
  });
  const wallSeconds = (performance.now() - started) / 1000;
  const report = JSON.parse(stdout.trim());
  const scanned = report.scanned_this_run ?? report.scannedTotal ?? 0;
  const contributions = report.contributions_written ?? report.contributions ?? 0;
  return {
    pageSize,
    batchSize,
    wallSeconds,
    resources: scanned,
    contributions,
    resourcesPerSecond: scanned / wallSeconds,
    report,
  };
};

const snapshotBaseline = (options, names) => {
  const result = arangoJSON(options, `
FOR d IN fhir_semantic_inventory
  FILTER d.project == ${literal(names.project)}
  FILTER d.dataset_generation == ${literal(names.generation)}
  INSERT UNSET(d, "_id", "_rev") INTO ${names.snapshotCollection}
  COLLECT WITH COUNT INTO copied
  RETURN copied`);
  assert.equal(result.length, 1);
  return result[0];
};

const currentInventoryIndexes = [
  ['project', 'dataset_generation', 'build_id'],
  ['project', 'dataset_generation', 'auth_resource_path', 'resource_type', 'binding_id', 'concept_id'],
  ['project', 'dataset_generation', 'build_id', 'binding_id', 'concept_id', 'auth_resource_path'],
  ['project', 'dataset_generation', 'build_id', 'source_id'],
  ['project', 'dataset_generation', 'build_id', 'source_kind', 'binding_id', 'concept_id', 'auth_resource_path'],
  ['project', 'dataset_generation', 'build_id', 'source_kind', 'auth_resource_path', 'resource_type', 'concept_id', 'concept_slot_id', 'observation.rule_hint'],
];

const candidateInventoryIndexes = currentInventoryIndexes.filter((fields) =>
  fields.join('\0') !== ['project', 'dataset_generation', 'build_id'].join('\0') &&
  fields.join('\0') !== ['project', 'dataset_generation', 'build_id', 'source_id'].join('\0'));

const createIndexedCollection = (options, names, variant, repetition) => {
  const collection = `loom_bench_inventory_${variant}_${repetition}_${names.project.slice(-12)}`;
  const indexes = variant === 'all' ? currentInventoryIndexes : candidateInventoryIndexes;
  names.indexedCollections.push(collection);
  arangoCommand(options, `
const collection = db._createDocumentCollection(${JSON.stringify(collection)});
for (const fields of ${JSON.stringify(indexes)}) collection.ensureIndex({type: "persistent", fields});`);
  return collection;
};

const exportContributionFile = (options, names) => {
  const query = `FOR d IN ${names.snapshotCollection} RETURN UNSET(d, "_id", "_rev")`;
  runDocker(options.arangoContainer, [
    'arangoexport', '--server.endpoint', 'tcp://127.0.0.1:8529',
    '--server.database', options.database, '--server.username', 'root', '--server.password', '',
    '--type', 'jsonl', '--output-directory', names.exportDirectory, '--overwrite', 'true',
    '--custom-query', query, '--documents-per-batch', '1000',
  ], { maxBuffer: 16 * 1024 * 1024 });
  const files = runDocker(options.arangoContainer, [
    'sh', '-lc', `find ${names.exportDirectory} -type f -name '*.jsonl' -print`,
  ]).trim().split('\n').filter(Boolean);
  assert.equal(files.length, 1, `expected one exported contribution file, found ${files.join(', ')}`);
  return files[0];
};

const importContributionFile = (options, file, collection) => {
  const started = performance.now();
  runDocker(options.arangoContainer, [
    'arangoimport', '--server.endpoint', 'tcp://127.0.0.1:8529',
    '--server.database', options.database, '--server.username', 'root', '--server.password', '',
    '--collection', collection, '--file', file, '--type', 'jsonl',
    '--create-collection', 'false', '--on-duplicate', 'replace', '--threads', '1',
    '--batch-size', '4194304',
  ], { maxBuffer: 16 * 1024 * 1024 });
  return (performance.now() - started) / 1000;
};

const compareIndexedCollections = (options, names, left, right) => {
  const result = arangoJSON(options, `
LET leftCount = LENGTH(FOR d IN ${left} RETURN 1)
LET rightCount = LENGTH(FOR d IN ${right} RETURN 1)
LET missingOrChanged = LENGTH(
  FOR d IN ${left}
    LET candidate = DOCUMENT(${literal(right)}, d._key)
    FILTER candidate == null OR UNSET(candidate, "_id", "_rev") != UNSET(d, "_id", "_rev")
    RETURN 1
)
LET unexpected = LENGTH(
  FOR d IN ${right}
    LET baseline = DOCUMENT(${literal(left)}, d._key)
    FILTER baseline == null OR UNSET(d, "_id", "_rev") != UNSET(baseline, "_id", "_rev")
    RETURN 1
)
RETURN {leftCount, rightCount, missingOrChanged, unexpected}`);
  assert.equal(result.length, 1);
  return result[0];
};

const median = (values) => {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? (sorted[middle - 1] + sorted[middle]) / 2 : sorted[middle];
};

const runIndexWriteBenchmark = (options, names, expectedDocuments) => {
  const file = exportContributionFile(options, names);
  const runs = [];
  for (let repetition = 0; repetition < 2; repetition += 1) {
    const order = repetition % 2 === 0 ? ['all', 'candidate'] : ['candidate', 'all'];
    const collections = {};
    for (const variant of order) {
      const collection = createIndexedCollection(options, names, variant, repetition);
      collections[variant] = collection;
      const seconds = importContributionFile(options, file, collection);
      runs.push({ variant, repetition, collection, seconds, resourcesPerSecond: expectedDocuments / seconds });
    }
    const exact = compareIndexedCollections(options, names, collections.all, collections.candidate);
    assert.deepEqual(exact, {
      leftCount: expectedDocuments,
      rightCount: expectedDocuments,
      missingOrChanged: 0,
      unexpected: 0,
    });
  }
  const baselineSeconds = median(runs.filter((run) => run.variant === 'all').map((run) => run.seconds));
  const candidateSeconds = median(runs.filter((run) => run.variant === 'candidate').map((run) => run.seconds));
  return {
    indexes: {all: currentInventoryIndexes, candidate: candidateInventoryIndexes},
    runs,
    baselineMedianSeconds: baselineSeconds,
    candidateMedianSeconds: candidateSeconds,
    improvement: (baselineSeconds - candidateSeconds) / baselineSeconds,
    exactDocuments: expectedDocuments,
  };
};

const removeDerivedForBenchmark = (options, names) => {
  runAQLMutation(options, `
LET entries = (
  FOR d IN fhir_semantic_inventory_entries
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN fhir_semantic_inventory_entries
    RETURN 1
)
LET contributions = (
  FOR d IN fhir_semantic_inventory
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN fhir_semantic_inventory
    RETURN 1
)
LET builds = (
  FOR d IN fhir_semantic_inventory_builds
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN fhir_semantic_inventory_builds
    RETURN 1
)
RETURN {entries: LENGTH(entries), contributions: LENGTH(contributions), builds: LENGTH(builds)}`);
};

const compareContributionSnapshot = (options, names) => {
  const result = arangoJSON(options, `
LET baselineCount = LENGTH(
  FOR d IN ${names.snapshotCollection}
    RETURN 1
)
LET candidateCount = LENGTH(
  FOR d IN fhir_semantic_inventory
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    RETURN 1
)
LET missingOrChanged = LENGTH(
  FOR d IN ${names.snapshotCollection}
    LET candidate = DOCUMENT("fhir_semantic_inventory", d._key)
    FILTER candidate == null OR UNSET(candidate, "_id", "_rev") != UNSET(d, "_id", "_rev")
    RETURN 1
)
LET unexpected = LENGTH(
  FOR d IN fhir_semantic_inventory
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    LET baseline = DOCUMENT(${literal(names.snapshotCollection)}, d._key)
    FILTER baseline == null OR UNSET(d, "_id", "_rev") != UNSET(baseline, "_id", "_rev")
    RETURN 1
)
RETURN {baselineCount, candidateCount, missingOrChanged, unexpected}`);
  assert.equal(result.length, 1);
  return result[0];
};

const cleanup = (options, names) => {
  runAQLMutation(options, `
LET source = (
  FOR d IN ${options.resourceType}
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN ${options.resourceType}
    RETURN 1
)
LET entries = (
  FOR d IN fhir_semantic_inventory_entries
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN fhir_semantic_inventory_entries
    RETURN 1
)
LET contributions = (
  FOR d IN fhir_semantic_inventory
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN fhir_semantic_inventory
    RETURN 1
)
LET builds = (
  FOR d IN fhir_semantic_inventory_builds
    FILTER d.project == ${literal(names.project)}
    FILTER d.dataset_generation == ${literal(names.generation)}
    REMOVE d IN fhir_semantic_inventory_builds
    RETURN 1
)
LET manifests = (
  FOR d IN loom_dataset_lifecycle
    FILTER d.recordType == "manifest"
    FILTER d.dataset.project == ${literal(names.project)}
    FILTER d.dataset.generation == ${literal(names.generation)}
    REMOVE d IN loom_dataset_lifecycle
    RETURN 1
)
RETURN {source: LENGTH(source), entries: LENGTH(entries), contributions: LENGTH(contributions), builds: LENGTH(builds), manifests: LENGTH(manifests)}`);
  arangoCommand(options, `if (db._collection(${JSON.stringify(names.snapshotCollection)})) db._drop(${JSON.stringify(names.snapshotCollection)});`);
  if (names.indexedCollections.length > 0) {
    arangoCommand(options, `for (const name of ${JSON.stringify(names.indexedCollections)}) if (db._collection(name)) db._drop(name);`);
  }
  try {
    runDocker(options.arangoContainer, ['rm', '-rf', names.exportDirectory], { maxBuffer: 1024 * 1024 });
  } catch {
    // The export directory is explicitly unique to this benchmark run.
  }
  try {
    runDocker(options.apiContainer, ['rm', '-f', names.binary], { maxBuffer: 1024 * 1024 });
  } catch {
    // The binary is inside the disposable API container; cleanup of the
    // explicitly named Arango project remains the required invariant.
  }
  try {
    unlinkSync(names.localBinary);
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
  }
};

const main = () => {
  const options = parseArgs(process.argv.slice(2));
  const names = buildNames();
  assert.notEqual(names.project, options.sourceProject);
  assert.notEqual(names.generation, options.sourceGeneration);
  let primaryError;
  let result;
  try {
    execFileSync('go', ['build', '-trimpath', '-o', names.localBinary, './cmd/arango-fhir-proto'], {
      cwd: process.cwd(),
      env: {
        ...process.env,
        CGO_ENABLED: '0',
        GOOS: 'linux',
        GOARCH: 'arm64',
        GOCACHE: '/private/tmp/loom-semantic-backfill-gocache',
      },
      stdio: 'pipe',
      maxBuffer: 128 * 1024 * 1024,
    });
    runDockerClient(['cp', names.localBinary, `${options.apiContainer}:${names.binary}`], { maxBuffer: 1024 * 1024 });
    prepareBenchmarkGeneration(options, names);
    const baselinePageSize = options.mode === 'backfill' ? 250 : 1000;
    const baselineBatchSize = options.mode === 'backfill' ? 500 : 5000;
    const baseline = runBackfill(options, names, baselinePageSize, baselineBatchSize);
    const copied = snapshotBaseline(options, names);
    assert.equal(copied, baseline.contributions, 'baseline snapshot count differs from backfill report');
    if (options.mode === 'index') {
      const index = runIndexWriteBenchmark(options, names, baseline.contributions);
      result = {
        source: { project: options.sourceProject, generation: options.sourceGeneration, resourceType: options.resourceType, rows: options.rows },
        benchmark: { mode: 'index', backfill: baseline, index },
      };
      process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
      if (options.evidence) writeFileSync(options.evidence, `${JSON.stringify(result, null, 2)}\n`);
      if (index.improvement < 0.10) {
        throw new Error(`candidate index improvement ${(index.improvement * 100).toFixed(2)}% is below the required 10%; leave production indexes unchanged`);
      }
    } else {
      removeDerivedForBenchmark(options, names);
    const candidate = runBackfill(options, names, 1000, 5000);
      const exact = compareContributionSnapshot(options, names);
      assert.equal(exact.missingOrChanged, 0, 'candidate contribution output changed relative to baseline');
      assert.equal(exact.unexpected, 0, 'candidate emitted unexpected contribution documents');
      assert.equal(exact.baselineCount, exact.candidateCount, 'candidate contribution count differs from baseline');
      const improvement = (baseline.wallSeconds - candidate.wallSeconds) / baseline.wallSeconds;
      result = { source: { project: options.sourceProject, generation: options.sourceGeneration, resourceType: options.resourceType, rows: options.rows }, benchmark: { baseline, candidate, improvement, exact } };
      process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
      if (options.evidence) writeFileSync(options.evidence, `${JSON.stringify(result, null, 2)}\n`);
      if (improvement < 0.10) {
      throw new Error(`candidate improvement ${(improvement * 100).toFixed(2)}% is below the required 10%; retain the conservative defaults`);
      }
    }
  } catch (error) {
    primaryError = error;
  } finally {
    try {
      cleanup(options, names);
    } catch (error) {
      if (!primaryError) primaryError = new Error(`benchmark cleanup failed: ${error.message}`);
      else primaryError = new Error(`${primaryError.message}; cleanup failed: ${error.message}`);
    }
  }
  if (primaryError) throw primaryError;
};

if (process.argv[1] && process.argv[1].endsWith('benchmark-semantic-backfill.mjs')) main();
