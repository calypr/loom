import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { spawn, spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import test from 'node:test';
import {
  ARANGOSH_PROCESS_PATH_MARKER,
  buildArangoShellInvocation,
  buildArangoShellStopInvocation,
} from '../owned-arangosh-command.mjs';

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));

const waitFor = async (predicate, description, timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await delay(10);
  }
  throw new Error(`Timed out waiting for ${description}`);
};

const withTimeout = (promise, milliseconds, description) => {
  let timer;
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`Timed out waiting for ${description}`)), milliseconds);
    }),
  ]).finally(() => clearTimeout(timer));
};

test('Arangosh receives exact long script bytes from file and expands its password inside the container shell', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-owned-arangosh-command-'));
  t.after(async () => rm(directory, { recursive: true, force: true }));
  const binDirectory = join(directory, 'bin');
  await mkdir(binDirectory);
  const capturePath = join(directory, 'arangosh-args.json');
  const failureCapturePath = join(directory, 'arangosh-failure-args.json');
  const unsetPasswordCapturePath = join(directory, 'arangosh-unset-password-args.json');
  const arangoshPath = join(binDirectory, 'arangosh');
  const arangoshShim = `#!${process.execPath}
import { readFileSync, statSync, writeFileSync } from 'node:fs';
const args = process.argv.slice(2);
const executePath = args[args.indexOf('--javascript.execute') + 1];
const password = args[args.indexOf('--server.password') + 1];
const database = args[args.indexOf('--server.database') + 1];
writeFileSync(process.env.OWNED_ARANGOSH_CAPTURE, JSON.stringify({
  scriptBase64: readFileSync(executePath).toString('base64'),
  executePath, fileMode: statSync(executePath).mode & 0o777, password, database,
}));
if (process.env.OWNED_ARANGOSH_EXIT_CODE) process.exit(Number(process.env.OWNED_ARANGOSH_EXIT_CODE));
`;
  await writeFile(arangoshPath, arangoshShim);
  await chmod(arangoshPath, 0o755);

  const script = `const single = 'single quotes stay literal';
const double = "double quotes stay literal";
const slash = "\\\"path\\\\with\\\\backslashes\\\"";
const dollar = "$ARANGO_ROOT_PASSWORD and \${host_side_expansion_must_not_run}";
const long = "${'x'.repeat(14_000)}";
print(single + double + slash + dollar + long);
`;
  const password = `container's "$value\\still literal`;
  const invocation = buildArangoShellInvocation({ container: 'owned-arango', script, database: 'loom_dev' });
  const shellCommand = invocation.args.at(-1);

  assert.equal(invocation.command, 'rtk');
  assert.deepEqual(invocation.args.slice(0, 6), ['proxy', 'docker', 'exec', 'owned-arango', 'sh', '-lc']);
  assert.match(shellCommand, /--javascript\.execute "\$script_file"/);
  assert.doesNotMatch(shellCommand, /--javascript\.execute-string/);
  assert.match(shellCommand, /--server\.password "\$ARANGO_ROOT_PASSWORD"/);
  assert.doesNotMatch(shellCommand, new RegExp(password.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));

  const runInvocation = (captureDestination, { exitCode = '', password: containerPassword = password, omitPassword = false } = {}) => {
    const env = {
      ...process.env,
      PATH: `${binDirectory}:${process.env.PATH}`,
      OWNED_ARANGOSH_CAPTURE: captureDestination,
      OWNED_ARANGOSH_EXIT_CODE: exitCode,
    };
    if (omitPassword) delete env.ARANGO_ROOT_PASSWORD;
    else env.ARANGO_ROOT_PASSWORD = containerPassword;
    return spawnSync('sh', ['-lc', shellCommand], {
      encoding: 'utf8', env, timeout: 10000,
    });
  };
  const run = runInvocation(capturePath);
  assert.equal(run.status, 0, run.stderr || run.error?.message);

  const captured = JSON.parse(await readFile(capturePath, 'utf8'));
  assert.equal(Buffer.from(captured.scriptBase64, 'base64').toString('utf8'), script);
  assert.equal(captured.password, password);
  assert.equal(captured.database, 'loom_dev');
  assert.equal(captured.fileMode, 0o600);
  assert.equal(existsSync(captured.executePath), false, 'temporary script is removed after a successful invocation');

  const missingPasswordRun = runInvocation(unsetPasswordCapturePath, { omitPassword: true });
  assert.equal(missingPasswordRun.status, 0, missingPasswordRun.stderr || missingPasswordRun.error?.message);
  const unsetPasswordCapture = JSON.parse(await readFile(unsetPasswordCapturePath, 'utf8'));
  assert.equal(unsetPasswordCapture.password, '', 'an unset container password expands to empty for the owned no-auth fixture');
  assert.equal(unsetPasswordCapture.fileMode, 0o600);
  assert.equal(existsSync(unsetPasswordCapture.executePath), false, 'temporary script is removed after an invocation with no password variable');

  const failedRun = runInvocation(failureCapturePath, { exitCode: '23' });
  assert.equal(failedRun.status, 23, failedRun.stderr || failedRun.error?.message);
  const failedCapture = JSON.parse(await readFile(failureCapturePath, 'utf8'));
  assert.equal(failedCapture.fileMode, 0o600);
  assert.equal(existsSync(failedCapture.executePath), false, 'temporary script is removed after a failed invocation');
});

test('managed Arangosh invocation emits exact script/PID identities and waits while normal invocation stays unchanged', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-managed-arangosh-command-'));
  const binDirectory = join(directory, 'bin');
  await mkdir(binDirectory);
  const startedPath = join(directory, 'arangosh-started.json');
  const releasePath = join(directory, 'release-arangosh');
  const arangoshPath = join(binDirectory, 'arangosh');
  const arangoshShim = `#!${process.execPath}
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
const args = process.argv.slice(2);
const executePath = args[args.indexOf('--javascript.execute') + 1];
writeFileSync(process.env.OWNED_ARANGOSH_STARTED, JSON.stringify({
  pid: process.pid,
  executePath,
  script: readFileSync(executePath, 'utf8'),
}));
while (!existsSync(process.env.OWNED_ARANGOSH_RELEASE)) await new Promise(resolve => setTimeout(resolve, 10));
process.stdout.write('FAKE_ARANGOSH_DONE\\n');
`;
  await writeFile(arangoshPath, arangoshShim);
  await chmod(arangoshPath, 0o755);

  const script = `print('managed query remains byte exact');\n`;
  const processIdMarker = '__LOOM_ARANGOSH_PID__:';
  const normal = buildArangoShellInvocation({ container: 'managed-arango', script, database: 'loom_dev' });
  const managed = buildArangoShellInvocation({ container: 'managed-arango', script, database: 'loom_dev', processIdMarker });
  assert.equal(normal.command, 'rtk');
  assert.equal(managed.command, normal.command);
  assert.deepEqual(managed.args.slice(0, 6), normal.args.slice(0, 6));
  const normalShellCommand = normal.args.at(-1);
  const managedShellCommand = managed.args.at(-1);
  assert.match(normalShellCommand, /--javascript\.execute "\$script_file"/);
  assert.doesNotMatch(normalShellCommand, /__LOOM_ARANGOSH_SCRIPT__|__LOOM_ARANGOSH_PID__|wait "\$arangosh_pid"/);
  assert.match(managedShellCommand, /--javascript\.execute "\$script_file"/);
  assert.match(managedShellCommand, /wait "\$arangosh_pid"/);
  assert.match(managedShellCommand, /__LOOM_ARANGOSH_SCRIPT__/);
  assert.match(managedShellCommand, /__LOOM_ARANGOSH_PID__:/);
  assert.throws(() => buildArangoShellInvocation({ container: 'managed-arango', script, processIdMarker: 'bad-prefix' }), /identity marker/i);

  let stdout = '';
  let stderr = '';
  let child;
  let closed;
  try {
    const env = {
      ...process.env,
      PATH: `${binDirectory}:${process.env.PATH}`,
      OWNED_ARANGOSH_STARTED: startedPath,
      OWNED_ARANGOSH_RELEASE: releasePath,
    };
    child = spawn('sh', ['-lc', managedShellCommand], { env, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout.setEncoding('utf8').on('data', chunk => { stdout += chunk; });
    child.stderr.setEncoding('utf8').on('data', chunk => { stderr += chunk; });
    closed = new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', (code, signal) => resolve({ code, signal }));
    });

    await waitFor(() => existsSync(startedPath), 'fake Arangosh start marker');
    const started = JSON.parse(await readFile(startedPath, 'utf8'));
    assert.equal(started.script, script);
    assert.match(started.executePath, /^\/tmp\/loom-arangosh\.[A-Za-z0-9]+$/);
    await delay(50);
    assert.equal(child.exitCode, null, 'managed shell must remain blocked in wait while Arangosh is running');

    await writeFile(releasePath, 'release');
    const outcome = await withTimeout(closed, 5000, 'managed shell exit after the Arangosh process exited');
    assert.deepEqual(outcome, { code: 0, signal: null }, stderr);
    const outputLines = stdout.trim().split('\n');
    assert.deepEqual(outputLines, [
      `${ARANGOSH_PROCESS_PATH_MARKER}${started.executePath}`,
      `${processIdMarker}${started.pid}:${started.executePath}`,
      'FAKE_ARANGOSH_DONE',
    ]);
    assert.ok(outputLines.indexOf(`${ARANGOSH_PROCESS_PATH_MARKER}${started.executePath}`)
      < outputLines.indexOf(`${processIdMarker}${started.pid}:${started.executePath}`));
    assert.equal(existsSync(started.executePath), false, 'managed temp script is removed after wait completes');
  } finally {
    await writeFile(releasePath, 'release').catch(() => {});
    if (child && child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM');
      await withTimeout(closed?.catch(() => {}), 1000, 'managed child cleanup').catch(() => {});
    }
    await rm(directory, { recursive: true, force: true });
  }
});

test('Arangosh stop invocation binds container, optional exact PID, and one exact owned script identity', () => {
  const container = 'owned-arango-42';
  const scriptPath = '/tmp/loom-arangosh.A1b2C3';
  const withPid = buildArangoShellStopInvocation({ container, pid: 731, scriptPath });
  assert.equal(withPid.command, 'rtk');
  assert.deepEqual(withPid.args.slice(0, 6), ['proxy', 'docker', 'exec', container, 'sh', '-lc']);
  const pidScript = withPid.args.at(-1);
  assert.match(pidScript, /script_path='\/tmp\/loom-arangosh\.A1b2C3'/);
  assert.match(pidScript, /\[ "\$\{executable##\*\/\}" = 'arangosh' \] \|\| continue/);
  assert.match(pidScript, /--javascript\.execute\$newline\$script_path\$newline/);
  assert.match(pidScript, /if \[ "\$#" -ne 1 \] \|\| \[ "\$1" != '731' \]; then/);
  assert.match(pidScript, /__LOOM_ARANGOSH_STOPPED__:identity-mismatch/);

  const withoutPid = buildArangoShellStopInvocation({ container, scriptPath });
  assert.deepEqual(withoutPid.args.slice(0, 6), ['proxy', 'docker', 'exec', container, 'sh', '-lc']);
  const noPidScript = withoutPid.args.at(-1);
  assert.match(noPidScript, /if \[ "\$#" -ne 1 \]; then/,
    'without a requested PID, ambiguous multiple matches must fail the exact-one-process check');
  assert.doesNotMatch(noPidScript.slice(0, noPidScript.indexOf('pid=$1')), /\|\| \[ "\$1" !=/,
    'without a requested PID, the initial match must be selected by unique script identity alone');
  assert.match(noPidScript, /script_path='\/tmp\/loom-arangosh\.A1b2C3'/);

  for (const invalid of [
    { container: '', scriptPath },
    { container: '   ', scriptPath },
    { container, pid: 1, scriptPath },
    { container, pid: 0, scriptPath },
    { container, pid: -2, scriptPath },
    { container, pid: 1.5, scriptPath },
    { container, pid: Number.MAX_SAFE_INTEGER + 1, scriptPath },
    { container, scriptPath: '/tmp/not-owned-script' },
    { container, scriptPath: '/tmp/loom-arangosh.' },
    { container, scriptPath: '/tmp/loom-arangosh.A1b2C3/extra' },
    { container, scriptPath: '/tmp/loom-arangosh.A1b2C3;echo injected' },
    { container, scriptPath: '/tmp/loom-arangosh.é' },
  ]) {
    assert.throws(() => buildArangoShellStopInvocation(invalid), /container|process ID|temporary script/i, JSON.stringify(invalid));
  }
});

test('Arangosh stop identity matches argv tokens exactly and rejects a script-path prefix decoy', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-owned-arangosh-stop-identity-'));
  t.after(async () => rm(directory, { recursive: true, force: true }));
  const procRoot = join(directory, 'proc');
  const binDirectory = join(directory, 'bin');
  await mkdir(binDirectory);
  const scriptPath = '/tmp/loom-arangosh.A1b2C3';
  const invocation = buildArangoShellStopInvocation({
    container: 'owned-arango-42',
    pid: 731,
    scriptPath,
  });
  const script = invocation.args.at(-1)
    .replace('/proc/[0-9]*/cmdline', `${procRoot}/[0-9]*/cmdline`)
    .replace('candidate_pid=${cmdline#/proc/}', 'candidate_pid=${cmdline#' + procRoot + '/}');

  for (const [pid, executable, executedPath] of [
    [731, '/usr/bin/arangosh', scriptPath],
    [732, '/usr/bin/arangosh', `${scriptPath}extra`],
    [733, '/usr/bin/not-arangosh', scriptPath],
  ]) {
    const processDirectory = join(procRoot, String(pid));
    await mkdir(processDirectory, { recursive: true });
    await writeFile(join(processDirectory, 'cmdline'), Buffer.from([
      executable,
      '--server.endpoint',
      'tcp://127.0.0.1:8529',
      '--javascript.execute',
      executedPath,
      '',
    ].join('\0')));
  }

  const psPath = join(binDirectory, 'ps');
  await writeFile(psPath, '#!/bin/sh\nprintf "Z\\n"\n');
  await chmod(psPath, 0o755);
  const result = spawnSync('sh', ['-c', script], {
    encoding: 'utf8',
    env: { ...process.env, PATH: `${binDirectory}:${process.env.PATH}` },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), '__LOOM_ARANGOSH_STOPPED__:already-exited');
});
