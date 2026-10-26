const shellQuote = value => `'${String(value).replaceAll("'", "'\\''")}'`;

export const ARANGOSH_PROCESS_PATH_MARKER = '__LOOM_ARANGOSH_SCRIPT__:';

const redactArangoshDiagnostic = (value, { project, generation }) => String(value ?? '')
  .replaceAll(project, '[PROJECT]')
  .replaceAll(generation, '[GENERATION]')
  .replace(/\b(?:Patient|Specimen|Observation)\/[A-Za-z0-9._-]+/g, '[FHIR_RESOURCE]')
  .replace(/\bg_[A-Za-z0-9_-]{16,}\b/g, '[KEY]')
  .replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, '[ID]')
  .replace(/\b[0-9a-f]{16,}\b/gi, '[ID]')
  .slice(0, 500);

export const buildArangoShellInvocation = ({ container, script, database = 'loom_dev', processIdMarker = null }) => {
  if (processIdMarker !== null && (typeof processIdMarker !== 'string' || !/^[A-Z0-9_]+:$/.test(processIdMarker))) {
    throw new TypeError('Arangosh process identity marker must be an uppercase protocol prefix ending in a colon.');
  }
  const execute = `arangosh --server.endpoint tcp://127.0.0.1:8529 --server.username root --server.password "$ARANGO_ROOT_PASSWORD" --server.database ${shellQuote(database)} --javascript.execute "$script_file"`;
  const managedExecute = processIdMarker === null
    ? execute
    : `printf '%s%s\\n' ${shellQuote(ARANGOSH_PROCESS_PATH_MARKER)} "$script_file"; ${execute} & arangosh_pid=$!; printf '%s%s:%s\\n' ${shellQuote(processIdMarker)} "$arangosh_pid" "$script_file"; wait "$arangosh_pid"`;
  return {
    command: 'rtk',
    args: [
      'proxy', 'docker', 'exec', container, 'sh', '-lc',
      `set -e; umask 077; script_file=$(mktemp /tmp/loom-arangosh.XXXXXX); trap 'rm -f "$script_file"' EXIT; printf '%s' ${shellQuote(script)} > "$script_file"; ${managedExecute}`,
    ],
  };
};

export const buildArangoShellStopInvocation = ({ container, pid = null, scriptPath }) => {
  if (typeof container !== 'string' || container.trim() === '') throw new TypeError('Arangosh container is required.');
  if (pid !== null && (!Number.isSafeInteger(pid) || pid < 2)) throw new TypeError('Arangosh process ID must be a positive safe integer.');
  if (typeof scriptPath !== 'string' || !/^\/tmp\/loom-arangosh\.[A-Za-z0-9]+$/.test(scriptPath)) {
    throw new TypeError('Arangosh script path must identify an owned temporary script.');
  }
  const script = `set -eu
script_path=${shellQuote(scriptPath)}
find_script_pids() {
  found=''
  for cmdline in /proc/[0-9]*/cmdline; do
    [ -r "$cmdline" ] || continue
    candidate_pid=\${cmdline#/proc/}
    candidate_pid=\${candidate_pid%/cmdline}
    [ "$candidate_pid" = "$$" ] && continue
    args=$(tr '\\000' '\\n' < "$cmdline" 2>/dev/null || true)
    newline=$(printf '\\nX')
    newline=\${newline%X}
    executable=\${args%%"$newline"*}
    [ "\${executable##*/}" = 'arangosh' ] || continue
    case "$args$newline" in
      *"$newline--javascript.execute$newline$script_path$newline"*) found="$found $candidate_pid" ;;
    esac
  done
  printf '%s\\n' "$found"
}
matches=$(find_script_pids)
if [ -z "$matches" ]; then
  printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:already-exited'
  exit 0
fi
set -- $matches
if [ "$#" -ne 1 ]${pid === null ? '' : ` || [ "$1" != ${shellQuote(pid)} ]`}; then
  printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:identity-mismatch'
  exit 42
fi
pid=$1
state=$(ps -p "$pid" -o stat= 2>/dev/null || true)
case "$state" in *Z*) printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:already-exited'; exit 0 ;; esac
kill -TERM "$pid"
attempt=0
while [ "$attempt" -lt 20 ]; do
  state=$(ps -p "$pid" -o stat= 2>/dev/null || true)
  case "$state" in ''|*Z*) stopped=1 ;; *) stopped=0 ;; esac
  if [ "$stopped" -eq 1 ]; then
    printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:term'
    exit 0
  fi
  matches=$(find_script_pids)
  if [ -z "$matches" ]; then
    printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:term'
    exit 0
  fi
  set -- $matches
  if [ "$#" -ne 1 ] || [ "$1" != "$pid" ]; then
    printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:identity-mismatch'
    exit 42
  fi
  sleep 0.1
  attempt=$((attempt + 1))
done
matches=$(find_script_pids)
set -- $matches
if [ "$#" -eq 1 ] && [ "$1" = "$pid" ]; then kill -KILL "$pid"; fi
attempt=0
while [ "$attempt" -lt 20 ]; do
  state=$(ps -p "$pid" -o stat= 2>/dev/null || true)
  case "$state" in ''|*Z*) stopped=1 ;; *) stopped=0 ;; esac
  if [ "$stopped" -eq 1 ]; then
    printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:kill'
    exit 0
  fi
  sleep 0.1
  attempt=$((attempt + 1))
done
printf '%s\\n' '__LOOM_ARANGOSH_STOPPED__:still-running'
exit 43`;
  return { command: 'rtk', args: ['proxy', 'docker', 'exec', container, 'sh', '-lc', script] };
};

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
