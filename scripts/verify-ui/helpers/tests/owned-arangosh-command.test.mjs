import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import test from 'node:test';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';

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
