const shellQuote = value => `'${String(value).replaceAll("'", "'\\''")}'`;

const redactArangoshDiagnostic = (value, { project, generation }) => String(value ?? '')
  .replaceAll(project, '[PROJECT]')
  .replaceAll(generation, '[GENERATION]')
  .replace(/\b(?:Patient|Specimen|Observation)\/[A-Za-z0-9._-]+/g, '[FHIR_RESOURCE]')
  .replace(/\bg_[A-Za-z0-9_-]{16,}\b/g, '[KEY]')
  .replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, '[ID]')
  .replace(/\b[0-9a-f]{16,}\b/gi, '[ID]')
  .slice(0, 500);

export const buildArangoShellInvocation = ({ container, script, database = 'loom_dev' }) => ({
  command: 'rtk',
  args: [
    'proxy', 'docker', 'exec', container, 'sh', '-lc',
    `set -e; umask 077; script_file=$(mktemp /tmp/loom-arangosh.XXXXXX); trap 'rm -f "$script_file"' EXIT; printf '%s' ${shellQuote(script)} > "$script_file"; arangosh --server.endpoint tcp://127.0.0.1:8529 --server.username root --server.password "$ARANGO_ROOT_PASSWORD" --server.database ${shellQuote(database)} --javascript.execute "$script_file"`,
  ],
});

export const buildBoundedArangoQueryScript = ({ query, maxRuntimeSeconds, memoryLimitBytes }) => {
  if (typeof query !== 'string' || !query.trim()) throw new TypeError('Arango query must be non-empty.');
  if (!Number.isFinite(maxRuntimeSeconds) || maxRuntimeSeconds <= 0) throw new TypeError('Arango query runtime must be positive.');
  if (!Number.isSafeInteger(memoryLimitBytes) || memoryLimitBytes <= 0) throw new TypeError('Arango query memory limit must be positive.');
  return `print(JSON.stringify(db._query(${JSON.stringify(query)}, {}, { maxRuntime: ${maxRuntimeSeconds}, memoryLimit: ${memoryLimitBytes} }).toArray()));`;
};

export const summarizeArangoShellResult = (result, { project, generation }) => {
  const stdout = String(result?.stdout ?? '');
  const stderr = String(result?.stderr ?? '');
  let rows = null;
  const jsonStart = stdout.indexOf('[');
  if (jsonStart >= 0) {
    try {
      const parsed = JSON.parse(stdout.slice(jsonStart));
      if (Array.isArray(parsed)) rows = parsed;
    } catch {}
  }
  return {
    processSucceeded: result?.status === 0 && !result?.error,
    exitStatus: Number.isInteger(result?.status) ? result.status : null,
    signal: typeof result?.signal === 'string' ? result.signal : null,
    spawnError: result?.error ? redactArangoshDiagnostic(result.error.message, { project, generation }) : null,
    stdoutBytes: Buffer.byteLength(stdout),
    stderrBytes: Buffer.byteLength(stderr),
    stdoutJsonComplete: rows !== null,
    stdoutRowCount: rows?.length ?? null,
    stdoutExcerpt: redactArangoshDiagnostic(stdout.slice(0, 500), { project, generation }),
    stdoutTailExcerpt: redactArangoshDiagnostic(stdout.slice(-500), { project, generation }),
    stderrExcerpt: redactArangoshDiagnostic(stderr, { project, generation }),
  };
};
