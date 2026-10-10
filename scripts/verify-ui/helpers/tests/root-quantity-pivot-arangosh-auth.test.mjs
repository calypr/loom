import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';

test('root quantity CDA oracle callsites use noninteractive owned Arango auth', async () => {
  const workflow = await readFile(new URL('../../workflows/root-quantity-pivot-workflow.mjs', import.meta.url), 'utf8');
  const integrations = [...workflow.matchAll(/const invocation = buildArangoShellInvocation\(\{/g)];
  const spawnCalls = [...workflow.matchAll(/spawnSync\(invocation\.command, invocation\.args, \{ encoding: 'utf8', timeout: 30000, maxBuffer: ([^}]+) \}\);/g)];

  assert.equal(integrations.length, 4, 'all four CDA/fixture oracle callsites must use the shared invocation');
  assert.equal(spawnCalls.length, 4, 'all four callsites must execute the helper command');
  assert.equal((workflow.match(/spawnSync\('rtk'/g) ?? []).length, 0, 'no raw unauthenticated rtk invocation remains');
  assert.equal((workflow.match(/script: program,/g) ?? []).length, 4, 'each original AQL JavaScript program is passed unchanged to the helper');
  assert.equal((workflow.match(/database: arangoDatabase,/g) ?? []).length, 2, 'call sites with required database keep the same database');
  assert.equal((workflow.match(/database: arangoDatabase \?\? 'loom_dev',/g) ?? []).length, 2, 'call sites with the existing fallback keep it');
  const buffers = spawnCalls.map(match => match[1].trim());
  assert.deepEqual(buffers, ['8 * 1024 * 1024', '8 * 1024 * 1024', '2 * 1024 * 1024', '8 * 1024 * 1024']);

  const invocation = buildArangoShellInvocation({ container: 'owned-arango', script: 'print(1);', database: 'loom_dev' });
  assert.equal(invocation.command, 'rtk');
  assert.deepEqual(invocation.args.slice(0, 6), ['proxy', 'docker', 'exec', 'owned-arango', 'sh', '-lc']);
  assert.match(invocation.args[6], /--server\.username root/);
  assert.match(invocation.args[6], /--server\.password "\$ARANGO_ROOT_PASSWORD"/);
  assert.match(invocation.args[6], /--server\.database 'loom_dev'/);
  assert.match(invocation.args[6], /printf '%s' 'print\(1\);' > "\$script_file"/);
  assert.match(invocation.args[6], /--javascript\.execute "\$script_file"/);
  assert(!invocation.args.some(argument => argument.includes('test-password')), 'credentials stay inside the container environment');
});
