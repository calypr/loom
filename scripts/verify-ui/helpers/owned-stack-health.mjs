import assert from 'node:assert/strict';
import { checkContainerApiBuildStamp } from './api-build-freeze.mjs';

const digestPattern = /^[a-f0-9]{64}$/i;

export function inspectBuildStamp(result) {
  const parts = typeof result?.stdout === 'string' ? result.stdout.trim().split(/\s+/) : [];
  const outputValid = parts.length === 3 && parts.every(part => digestPattern.test(part));
  const normalized = outputValid ? parts.map(part => part.toLowerCase()) : [];
  const sourceDigestMatchesCurrentMountedSource = outputValid && normalized[0] === normalized[1];
  const valid = result?.status === 0 && outputValid;
  const failureKind = result?.failureKind ?? (
    outputValid && !sourceDigestMatchesCurrentMountedSource ? 'source-stamp-mismatch'
      : result?.status !== 0 ? 'stamp-check-failed'
        : !outputValid ? 'invalid-stamp-output' : undefined
  );
  return {
    valid,
    fresh: valid && sourceDigestMatchesCurrentMountedSource,
    sourceDigestMatchesCurrentMountedSource,
    runningBinaryMatchesRecordedBuild: valid,
    apiBuildIdentity: valid ? normalized.join(':') : null,
    exitCode: Number.isInteger(result?.status) ? result.status : null,
    ...(failureKind ? { failureKind } : {}),
    ...(typeof result?.diagnostic === 'string' && result.diagnostic ? { diagnostic: result.diagnostic.slice(0, 1000) } : {}),
  };
}

export function parseCapturedBuildIdentity(value) {
  const parts = typeof value === 'string' ? value.split(':') : [];
  assert.equal(parts.length, 3, 'Captured API identity must contain three SHA-256 digests');
  assert(parts.every(part => digestPattern.test(part)), 'Captured API identity must contain three 64-character hex digests');
  const normalized = parts.map(part => part.toLowerCase());
  assert.equal(normalized[0], normalized[1], 'Captured identity must describe the current mounted source');
  return normalized.join(':');
}

export function assertCapturedTargetMatches(captured, current) {
  assert(captured && typeof captured === 'object', 'Capture must contain an owned target');
  for (const key of [
    'project', 'generation', 'composeProject', 'apiContainer', 'uiContainer', 'arangoContainer',
    'clickhouseContainer', 'apiPort', 'uiPort', 'sourceRoot',
  ]) {
    assert.equal(captured[key] ?? null, current[key] ?? null, `Owned target changed since capture: ${key}`);
  }
}

export async function runOwnedStackHealth({
  apiURL,
  uiURL,
  apiContainer,
  expectedIdentity,
  fetchImpl = fetch,
  readStamp = checkContainerApiBuildStamp,
  sleep = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds)),
  now = () => new Date().toISOString(),
}) {
  const identity = parseCapturedBuildIdentity(expectedIdentity);
  const samples = [];
  for (let index = 0; index < 3; index += 1) {
    const [apiResponse, uiResponse, stamp] = await Promise.all([
      fetchImpl(`${apiURL.replace(/\/$/, '')}/readyz`, { signal: AbortSignal.timeout(5000) }),
      fetchImpl(`${uiURL.replace(/\/$/, '')}/`, { signal: AbortSignal.timeout(5000) }),
      readStamp(apiContainer),
    ]);
    const apiBody = await apiResponse.text();
    const uiBody = await uiResponse.text();
    const observation = inspectBuildStamp(stamp);
    const sample = {
      at: now(),
      apiStatus: apiResponse.status,
      apiBody,
      uiStatus: uiResponse.status,
      uiHasDocument: /<!doctype html>/i.test(uiBody),
      apiBuildIdentity: observation.apiBuildIdentity,
    };
    samples.push(sample);
    assert.equal(apiResponse.status, 200, `Owned API health failed at sample ${index + 1}`);
    assert.match(apiBody, /"status":"ready"/, `Owned API readiness body failed at sample ${index + 1}`);
    assert.equal(uiResponse.status, 200, `Owned UI health failed at sample ${index + 1}`);
    assert(sample.uiHasDocument, `Owned UI document check failed at sample ${index + 1}`);
    assert(observation.fresh, `Owned API build stamp is stale or invalid at sample ${index + 1}`);
    assert.equal(observation.apiBuildIdentity, identity, `Owned API build identity changed at sample ${index + 1}`);
    if (index < 2) await sleep(2000);
  }
  return { status: 'PASS', apiBuildIdentity: identity, samples };
}
