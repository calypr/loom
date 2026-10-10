import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

const API_EXECUTABLE = 'arango-fhir-server';
const CONFIG_ARGUMENT = '--config /workspace/scope-001-restricted-auth.server.yaml';
const NO_AUTH_ARGUMENT = /(?:^|\s)--no-auth(?:\s|$)/;

/** Verify the owned auth target runs the configured Calypr server entrypoint. */
export function assertScope001RestrictedAuthRuntime({ apiContainer, dockerTop = container =>
  execFileSync('docker', ['top', container, '-eo', 'pid,args'], { encoding: 'utf8', timeout: 10_000 }) }) {
  assert.match(apiContainer ?? '', /^[A-Za-z0-9_.-]+$/, 'The validated owned API container is required.');
  const processList = dockerTop(apiContainer);
  assert.equal(typeof processList, 'string', 'Owned API process listing must be text.');
  const apiCommands = processList.split(/\r?\n/).map(line => {
    const match = /^\s*\d+\s+(\S+)(?:\s|$)/.exec(line);
    const executable = match?.[1].split('/').at(-1);
    return executable === API_EXECUTABLE ? line.trim() : null;
  }).filter(Boolean);
  assert.equal(apiCommands.length, 1,
    'Restricted auth target must expose exactly one arango-fhir-server process.');
  assert(apiCommands[0].includes(CONFIG_ARGUMENT),
    'Restricted auth target must use its explicit Calypr server config.');
  assert(!NO_AUTH_ARGUMENT.test(apiCommands[0]),
    'Restricted auth target must not start arango-fhir-server with --no-auth.');
  return { apiContainer, executable: API_EXECUTABLE, configArgumentVerified: true, noAuthArgumentAbsent: true };
}
