import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

const API_EXECUTABLE = 'arango-fhir-server';
const NO_AUTH_ARGUMENT = /(?:^|\s)--no-auth(?:\s|$)/;

export function assertCdaNoAuthRuntime({ apiContainer, dockerTop = container =>
  execFileSync('docker', ['top', container, '-eo', 'pid,args'], { encoding: 'utf8', timeout: 10_000 }) }) {
  assert.match(apiContainer ?? '', /^[A-Za-z0-9_.-]+$/, 'The validated owned CDA API container is required');
  const processList = dockerTop(apiContainer);
  assert(typeof processList === 'string', 'Owned API process listing must be text');
  const apiCommands = processList.split(/\r?\n/).map(line => {
    const match = /^\s*\d+\s+(\S+)(?:\s|$)/.exec(line);
    const executable = match?.[1].split('/').at(-1);
    return executable === API_EXECUTABLE ? line.trim() : null;
  }).filter(Boolean);
  assert.equal(apiCommands.length, 1,
    'CDA authorization fixture mismatch: the owned API container must expose exactly one arango-fhir-server process');
  assert(NO_AUTH_ARGUMENT.test(apiCommands[0]),
    'CDA authorization fixture mismatch: the owned Loom API process must include --no-auth');
  return { apiContainer, executable: 'arango-fhir-server', noAuthArgumentVerified: true };
}
