import { spawnSync } from 'node:child_process';
import {
  buildCDAQuantityCategoryOracle,
  validateCDAQuantityCategoryOracle,
} from './cda-quantity-category-oracle.mjs';

const RESULT_MARKER = '__LOOM_CDA_QUANTITY_ORACLE_RESULT__';

export function runCDAQuantityCategoryOracle(scopeBinds, options = {}) {
  const { query, bindVars } = buildCDAQuantityCategoryOracle(scopeBinds);
  if (bindVars.auth_resource_paths_unrestricted !== true || bindVars.auth_resource_paths.length !== 0) {
    throw new Error('The raw CDA oracle runner supports the verified local no-auth scope only');
  }

  const container = options.container ?? process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
  const database = options.database ?? process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev';
  const dockerCommand = options.dockerCommand ?? [process.env.LOOM_DOCKER_BIN ?? 'docker'];
  const timeoutMs = options.timeoutMs ?? 60_000;
  const maxRuntime = options.maxRuntime ?? 30;
  if (typeof container !== 'string' || container.trim() === '') throw new Error('An ArangoDB container name is required');
  if (typeof database !== 'string' || database.trim() === '') throw new Error('An ArangoDB database name is required');
  if (!Array.isArray(dockerCommand) || dockerCommand.length === 0 || dockerCommand.some(part => typeof part !== 'string' || part === '')) {
    throw new Error('dockerCommand must be a non-empty argv array');
  }
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0) throw new Error('timeoutMs must be a positive integer');
  if (!Number.isFinite(maxRuntime) || maxRuntime <= 0) throw new Error('maxRuntime must be a positive number of seconds');

  const script = `try {
  const rows = db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVars)}, { maxRuntime: ${maxRuntime} }).toArray();
  print(${JSON.stringify(RESULT_MARKER)} + JSON.stringify({ ok: true, rows }));
} catch (error) {
  print(${JSON.stringify(RESULT_MARKER)} + JSON.stringify({
    ok: false,
    message: error && (error.errorMessage || error.message) || String(error),
    errorNum: error && error.errorNum,
  }));
}`;
  const shellScript = `set -eu
umask 077
script_path=$(mktemp /tmp/loom-cda-quantity-oracle.XXXXXX)
trap 'rm -f -- "$script_path"' EXIT
cat > "$script_path"
arangosh --server.database "$1" --javascript.execute "$script_path"`;
  const scriptOwner = 'loom-cda-quantity-category-oracle';
  const startedAt = Date.now();
  const result = spawnSync(dockerCommand[0], [
    ...dockerCommand.slice(1),
    'exec', '-i', container,
    'sh', '-c', shellScript, scriptOwner, database,
  ], {
    encoding: 'utf8',
    input: script,
    timeout: timeoutMs,
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.error) throw new Error(`Could not run raw CDA quantity oracle: ${result.error.message}`);
  if (result.status !== 0) {
    throw new Error(`Raw CDA quantity oracle failed (exit ${result.status}): ${(result.stderr || result.stdout || '').trim().slice(-4000)}`);
  }

  const line = result.stdout.split(/\r?\n/).find(value => value.startsWith(RESULT_MARKER));
  if (!line) throw new Error(`arangosh returned no raw CDA oracle result: ${result.stdout.slice(-4000)}`);
  let payload;
  try {
    payload = JSON.parse(line.slice(RESULT_MARKER.length));
  } catch (error) {
    throw new Error(`Could not parse raw CDA quantity oracle result: ${error.message}`);
  }
  if (payload?.ok !== true) {
    throw new Error(`Raw CDA quantity oracle query failed: ${payload?.message ?? 'unknown ArangoDB error'}${payload?.errorNum === undefined ? '' : ` (error ${payload.errorNum})`}`);
  }
  const oracle = validateCDAQuantityCategoryOracle(payload.rows?.[0]);
  return { durationMs: Date.now() - startedAt, oracle };
}
