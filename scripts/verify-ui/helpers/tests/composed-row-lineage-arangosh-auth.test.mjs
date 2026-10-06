import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';

test('owned Arango query invocation reads credentials in-container without a prompt or host secret', async () => {
  const script = `print(JSON.stringify(db._query({ value: "a'b" }).toArray()));`;
  const invocation = buildArangoShellInvocation({ container: 'owned-arango', script });

  assert.equal(invocation.command, 'rtk');
  assert.deepEqual(invocation.args.slice(0, 6), ['proxy', 'docker', 'exec', 'owned-arango', 'sh', '-lc']);
  const shellCommand = invocation.args[6];
  assert.match(shellCommand, /--server\.endpoint tcp:\/\/127\.0\.0\.1:8529/);
  assert.match(shellCommand, /--server\.username root/);
  assert.match(shellCommand, /--server\.password "\$ARANGO_ROOT_PASSWORD"/);
  assert.match(shellCommand, /--server\.database 'loom_dev'/);
  assert.match(shellCommand, /--javascript\.execute-string /);
  assert.match(shellCommand, /a'\\''b/);
  assert(!invocation.args.some(argument => argument.includes('test-password')));

  const workflow = await readFile(new URL('../../workflows/verify-cda-composed-row-lineage-browser.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /buildArangoShellInvocation\(\{ container: arangoContainer, script \}\)/);
  assert.match(workflow, /spawnSync\(invocation\.command, invocation\.args, \{ encoding: 'utf8', timeout: 30000 \}\)/);
});
