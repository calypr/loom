import test from 'node:test';
import assert from 'node:assert/strict';
import { capabilityBinding, capabilityResponseMatches, isIncidentalFavicon, supersedingCapabilityRequest } from '../../playwright/network-evidence.mjs';

const target = { uiUrl: 'http://127.0.0.1:30008', fixtureProject: 'owned', explorer: 'editor' };
const route = '/api/v1/projects/owned/explorers/editor/authoring/v2/construction-capabilities';
const body = { snapshotToken: 's', expectedDraftVersion: 1, expectedDraftDigest: 'd', outputId: 'observation', stageId: 'source_projection' };
const request = (overrides = {}) => ({ url: () => target.uiUrl + route, method: () => 'POST', postDataJSON: () => body, ...overrides });

test('capability ownership requires exact project, origin, route, method and complete draft binding', () => {
  assert.equal(capabilityBinding(request(), target).outputId, 'observation');
  for (const overrides of [
    { url: () => 'http://elsewhere.invalid' + route },
    { url: () => target.uiUrl + route.replace('/owned/', '/other/') },
    { url: () => target.uiUrl + route + '/other' },
    { method: () => 'GET' },
    { url: () => target.uiUrl + route.replace('/editor/', '/elsewhere/') },
    { postDataJSON: () => ({ ...body, expectedDraftVersion: 0 }) },
    { postDataJSON: () => ({ ...body, stageId: undefined }) },
    { postDataJSON: () => ({ ...body, expectedDraftDigest: undefined }) },
  ]) assert.equal(capabilityBinding(request(overrides), target), null);
});

test('an aborted capability request needs a later successful differently bound replacement on its exact route', () => {
  const binding = capabilityBinding(request(), target);
  const failure = { sequence: 2, binding, errorText: 'net::ERR_ABORTED' };
  const replacement = { sequence: 3, status: 200, finished: true, responseMatches: true, binding: { ...binding, outputId: 'patient' } };
  assert.equal(supersedingCapabilityRequest(failure, [replacement]), replacement);
  for (const candidate of [
    { ...replacement, sequence: 1 },
    { ...replacement, status: 422 },
    { ...replacement, status: 0 },
    { ...replacement, finished: false },
    { ...replacement, failed: true },
    { ...replacement, responseMatches: false },
    { ...replacement, binding },
    { ...replacement, binding: { ...replacement.binding, route: binding.route + '/other' } },
  ]) assert.equal(supersedingCapabilityRequest(failure, [candidate]), null);
  assert.equal(supersedingCapabilityRequest({ ...failure, errorText: 'net::ERR_FAILED' }, [replacement]), null);
});

test('incidental favicon classification never hides other assets or API errors', () => {
  assert.equal(isIncidentalFavicon(target.uiUrl + '/favicon.ico', target, 404), true);
  assert.equal(isIncidentalFavicon(target.uiUrl + '/favicon.ico', target, 500), false);
  assert.equal(isIncidentalFavicon('http://elsewhere.invalid/favicon.ico', target, 404), false);
  assert.equal(isIncidentalFavicon(target.uiUrl + route, target, 404), false);
});

test('capability replacement response must match every requested binding field', () => {
  const binding = capabilityBinding(request(), target);
  const response = { snapshotToken: binding.snapshotToken, draftVersion: binding.draftVersion, draftDigest: binding.draftDigest, outputId: binding.outputId, stageId: binding.stageId };
  assert.equal(capabilityResponseMatches(binding, response), true);
  for (const field of Object.keys(response)) assert.equal(capabilityResponseMatches(binding, { ...response, [field]: 'wrong' }), false);
});
