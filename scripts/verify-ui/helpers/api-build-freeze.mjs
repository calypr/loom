import { execFile } from 'node:child_process';

const buildStampPath = '/workspace/loom-dev-build-stamp.sh';
const defaultLocalCDAApiContainer = 'loom-dev-6d7df93d6a37-loom-api-1';
const stampPattern = /^([a-f0-9]{64})\s+([a-f0-9]{64})\s+([a-f0-9]{64})$/i;
const dockerAccessPattern = /(?:(?:docker(?:\.sock| daemon| socket)?|unix socket)[^\n]{0,300}(?:permission denied|operation not permitted))|(?:(?:permission denied|operation not permitted)[^\n]{0,300}(?:docker|unix socket))/i;

const safeDiagnostic = (value) => String(value ?? '')
  .replace(/\u001b\[[0-9;]*m/g, '')
  .replace(/\b(authorization\s*[:=]\s*bearer\s+)[^\s,;]+/gi, '$1[redacted]')
  .replace(/\b(password|passwd|token|secret|credential|api[_-]?key)\b(\s*[:=]\s*)[^\s,;]+/gi, '$1$2[redacted]')
  .replace(/(https?:\/\/)[^/@\s]+@/gi, '$1[redacted]@')
  .replace(/\/Users\/[^/\s]+/g, '/Users/[user]')
  .replace(/\/home\/[^/\s]+/g, '/home/[user]')
  .trim()
  .slice(0, 1000);

function failureKind({ error, status, stdout, stderr }) {
  const diagnostic = `${stderr ?? ''}\n${error?.message ?? ''}`;
  if (dockerAccessPattern.test(diagnostic)) return 'docker-access-denied';
  if (error && (typeof error.code === 'string' || error.signal || error.killed)) return 'docker-command-execution-failed';
  const stamp = typeof stdout === 'string' ? stampPattern.exec(stdout.trim()) : undefined;
  if (stamp && stamp[1].toLowerCase() !== stamp[2].toLowerCase()) return 'source-stamp-mismatch';
  if (status !== 0) return 'stamp-check-failed';
  if (!stamp) return 'invalid-stamp-output';
  return undefined;
}

const safeObservation = (result) => {
  const stamp = typeof result?.stdout === 'string' ? stampPattern.exec(result.stdout.trim()) : undefined;
  const status = Number.isInteger(result?.status) ? result.status : null;
  const diagnostic = safeDiagnostic(result?.diagnostic ?? result?.stderr);
  return {
    fresh: status === 0 && Boolean(stamp),
    signature: stamp?.slice(1).join(':'),
    status,
    signal: typeof result?.signal === 'string' ? result.signal : undefined,
    errorCode: typeof result?.errorCode === 'string' ? result.errorCode : undefined,
    failureKind: result?.failureKind ?? failureKind({
      status,
      stdout: result?.stdout,
      stderr: diagnostic,
      error: result?.error,
    }),
    diagnostic: diagnostic || undefined,
  };
};

const sanitizedObservation = (observation) => {
  if (!observation) return { checked: false };
  const { fresh, status, signal, errorCode, failureKind: kind, diagnostic } = observation;
  return {
    checked: true,
    fresh,
    ...(status === null ? {} : { status }),
    ...(signal ? { signal } : {}),
    ...(errorCode ? { errorCode } : {}),
    ...(kind ? { failureKind: kind } : {}),
    ...(diagnostic ? { diagnostic } : {}),
  };
};

export class ApiBuildFreezeError extends Error {
  constructor(reason, before, after) {
    super(`Verifier run invalidated by running API build stamp: ${reason}.`);
    this.name = 'ApiBuildFreezeError';
    this.reason = reason;
    this.before = sanitizedObservation(before);
    this.after = sanitizedObservation(after);
    this.invalidatesRun = true;
    this.productFailure = false;
  }
}

async function observe(readStamp) {
  try {
    return safeObservation(await readStamp());
  } catch (error) {
    return safeObservation({
      status: null,
      signal: typeof error?.signal === 'string' ? error.signal : undefined,
      errorCode: typeof error?.code === 'string' ? error.code : undefined,
      failureKind: 'docker-command-execution-failed',
      diagnostic: error?.message,
    });
  }
}

/**
 * Capture the result of a caller-provided running-API build-stamp check.
 * The callback returns { status, stdout, signal?, errorCode?, failureKind?, diagnostic? }
 * and stays injectable so portable tests and non-Docker deployments need no Docker tool.
 */
export async function captureApiBuildFreeze(readStamp) {
  if (typeof readStamp !== 'function') throw new TypeError('readStamp must be a function');
  const before = await observe(readStamp);
  if (!before.fresh) throw new ApiBuildFreezeError('the initial running API stamp check failed', before);
  return Object.freeze({
    initial: sanitizedObservation(before),
    async assertUnchanged() {
      const after = await observe(readStamp);
      if (!after.fresh) throw new ApiBuildFreezeError('the final running API stamp check failed', before, after);
      if (before.signature !== after.signature) throw new ApiBuildFreezeError('the running API build stamp changed during the run', before, after);
      return {
        checked: true,
        unchanged: true,
        after: sanitizedObservation(after),
        invalidatesRun: false,
        productFailure: false,
      };
    },
  });
}

/** Return the local CDA API container name without starting or changing it. */
export function localCDAApiContainer(env = process.env) {
  return env.LOOM_API_CONTAINER?.trim() || defaultLocalCDAApiContainer;
}

/**
 * Read the existing dev build stamp from one explicitly selected container.
 * This is an optional Docker adapter; callers may instead inject any check
 * into captureApiBuildFreeze.
 */
export function checkContainerApiBuildStamp(container, {
  execFileImpl = execFile,
  timeoutMs = 10000,
} = {}) {
  if (typeof container !== 'string' || container.trim() === '') {
    throw new TypeError('container must be a non-empty string');
  }
  return new Promise((resolve) => {
    execFileImpl('docker', ['exec', container, buildStampPath, '--check'], {
      encoding: 'utf8',
      timeout: timeoutMs,
      maxBuffer: 1024 * 1024,
    }, (error, stdout, stderr) => {
      const output = typeof stdout === 'string' ? stdout : '';
      const status = error ? (Number.isInteger(error.code) ? error.code : null) : 0;
      const safeErrorText = safeDiagnostic(stderr || error?.message);
      resolve({
        status,
        stdout: output,
        signal: typeof error?.signal === 'string' ? error.signal : undefined,
        errorCode: typeof error?.code === 'string' ? error.code : undefined,
        failureKind: failureKind({ error, status, stdout: output, stderr }),
        diagnostic: safeErrorText || undefined,
      });
    });
  });
}
