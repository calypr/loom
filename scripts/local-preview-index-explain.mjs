import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { summarizeExplainResponse, validateExplainInputs, validateOwnedArangoContainer } from './lib/local-preview-index-explain.mjs';

const containerName = 'loom-dev-6d7df93d6a37-arangodb-1';
const explainEndpoint = 'http://127.0.0.1:8529/_db/loom_dev/_api/explain';

function parseArgs(args) {
  const options = {
    spec: '/tmp/loom-category-pivot-covering-index-spec-current.json',
    query: '',
    report: '/tmp/loom-category-pivot-recompiled-explain-current.json',
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (!['--spec', '--query', '--report'].includes(arg)) throw new Error(`unknown option ${arg}`);
    const value = args[++index];
    if (!value) throw new Error(`${arg} requires a path`);
    options[arg.slice(2)] = resolve(value);
  }
  return options;
}

function run(command, args, { maxBuffer = 16 * 1024 * 1024, timeout = 30000 } = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', maxBuffer, timeout });
  if (result.error) throw new Error(`${command} failed: ${result.error.message}`);
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${result.status}: ${result.stderr?.trim() || 'no diagnostic output'}`);
  return result.stdout;
}

function inspectArangoContainer() {
  const rows = JSON.parse(run('docker', ['inspect', containerName]));
  assert.equal(rows.length, 1, `docker inspect must resolve exactly ${containerName}`);
  validateOwnedArangoContainer(rows[0]);
}

async function main(args = process.argv.slice(2)) {
  const options = parseArgs(args);
  const specText = await readFile(options.spec, 'utf8');
  const spec = JSON.parse(specText);
  const queryPath = options.query || spec.fingerprint?.recompiledQueryFile;
  assert(typeof queryPath === 'string' && queryPath, 'compiler proof does not name the exact recompiled query artifact');
  const queryBytes = await readFile(queryPath);
  const input = validateExplainInputs(specText, queryBytes, queryPath);
  inspectArangoContainer();

  const remoteQueryPath = `/tmp/loom-preview-index-explain-${randomUUID()}.json`;
  let copied = false;
  let summary;
  try {
    run('docker', ['cp', queryPath, `${containerName}:${remoteQueryPath}`]);
    copied = true;
    const requestScript = `const response = require('@arangodb/request')({url: ${JSON.stringify(explainEndpoint)}, method: 'POST', body: require('fs').read(${JSON.stringify(remoteQueryPath)}), headers: {'content-type': 'application/json'}}); print(response.body);`;
    const responseText = run('docker', [
      'exec', containerName, 'arangosh', '--server.endpoint', 'tcp://127.0.0.1:8529',
      '--server.authentication', 'false', '--javascript.execute-string', requestScript,
    ], { maxBuffer: 16 * 1024 * 1024, timeout: 120000 });
    let response;
    try { response = JSON.parse(responseText); } catch { throw new Error('Arango EXPLAIN returned invalid JSON'); }
    if (response.error) {
      await writeFile(options.report, `${JSON.stringify({ status: 'explain-failed', compilerIndexName: input.indexName, diagnostic: response }, null, 2)}\n`);
      throw new Error(`Arango EXPLAIN failed (${response.errorNum}): ${response.errorMessage}`);
    }
    summary = summarizeExplainResponse(response, input.indexName);
  } finally {
    if (copied) {
      try { run('docker', ['exec', containerName, 'rm', '-f', remoteQueryPath], { timeout: 10000 }); } catch { /* preserve the primary EXPLAIN result */ }
    }
  }

  const report = {
    schemaVersion: 1,
    operation: 'read-only EXPLAIN of the exact current production recompile',
    status: summary.candidateIndexSelected ? 'candidate-index-selected' : 'candidate-index-not-selected',
    endpoint: explainEndpoint,
    database: 'loom_dev',
    collection: input.collection,
    project: input.project,
    generation: input.generation,
    compilerIndexName: input.indexName,
    compilerIndexFields: input.fields,
    compilerIndexStoredValues: input.storedValues,
    specPath: options.spec,
    specSHA256: input.specFileSha256,
    queryArtifactPath: queryPath,
    queryArtifactSHA256: input.queryFileSha256,
    recompiledAqlSHA256: input.aqlSha256,
    recompiledBindVarsSHA256: input.bindVarsSha256,
    productionSourceSHA256: input.sourceClosureSha256,
    explain: summary,
  };
  await writeFile(options.report, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  process.stdout.write(`${JSON.stringify({ reportPath: options.report, ...report }, null, 2)}\n`);
  if (!summary.candidateIndexSelected) process.exitCode = 1;
}

export { main };

if (!process.env.NODE_TEST_CONTEXT && import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  try {
    await main();
  } catch (error) {
    process.stderr.write(`LOCAL_PREVIEW_INDEX_EXPLAIN_FAILED ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  }
}
