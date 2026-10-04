import test from 'node:test';
import assert from 'node:assert/strict';
import { diagnosticsToNetwork, ownedBrowserRequestTarget } from '../playwright-case.mjs';
import { classifyNetworkRecord } from '../report.mjs';
import { isLoadedBuilderSnapshot } from '../builder-load.mjs';

const failedRead = {
  url: 'http://127.0.0.1:30102/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/builder',
  method: 'GET',
  failure: 'net::ERR_FAILED',
};

test('browser request ownership uses Vite proxy origin while backend identity stays separate', () => {
  assert.deepEqual(ownedBrowserRequestTarget({
    uiUrl: 'http://127.0.0.1:30102/builder',
    apiUrl: 'http://127.0.0.1:8282',
  }, {
    method: 'GET',
    path: '/api/v1/projects/project-a/explorers',
  }), {
    origin: 'http://127.0.0.1:30102',
    method: 'GET',
    path: '/api/v1/projects/project-a/explorers',
  });
});

test('Builder readiness accepts the selected empty workspace or a populated ready preview', () => {
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: true, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), true);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 1, previewStatus: 'ready',
  }, 'owned-explorer'), true);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'other-explorer', emptyWorkspaceVisible: true, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), false);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 1, previewStatus: 'loading',
  }, 'owned-explorer'), false);
  assert.equal(isLoadedBuilderSnapshot({
    selectedExplorerId: 'owned-explorer', emptyWorkspaceVisible: false, tableCount: 0, previewStatus: null,
  }, 'owned-explorer'), false);
});

test('one exact owned proxied method and path failure is attributed to injected fault', () => {
  const records = diagnosticsToNetwork({
    console: [],
    pageErrors: [],
    networkFailures: [failedRead, failedRead],
    httpFailures: [],
  }, {
    origin: 'http://127.0.0.1:30102',
    path: '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/builder',
    method: 'GET',
  });
  assert.deepEqual(records.map(classifyNetworkRecord), ['expected-injected', 'unexpected-error']);
});

test('one injected API rejection is expected and a subsequent same-path HTTP failure stays unexpected', () => {
  const injected = {
    url: 'http://127.0.0.1:30102/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/reconcile',
    method: 'POST',
    status: 422,
    body: '{"code":"VERIFY_COMPILE_REJECTED"}',
  };
  const records = diagnosticsToNetwork({
    console: [],
    pageErrors: [],
    networkFailures: [],
    httpFailures: [injected, { ...injected, status: 503, body: '{"code":"UNEXPECTED"}' }],
  }, {
    origin: 'http://127.0.0.1:30102',
    path: '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/reconcile',
    method: 'POST',
  });
  assert.deepEqual(records.map(classifyNetworkRecord), ['expected-injected', 'unexpected-error']);
});

test('one exact resource console error may accompany the injected request failure', () => {
  const requestURL = failedRead.url;
  const records = diagnosticsToNetwork({
    console: [
      { text: 'Failed to load resource: net::ERR_FAILED', location: requestURL },
      { text: 'Failed to load resource: net::ERR_FAILED', location: requestURL },
      { text: 'Unrelated console error', location: 'http://127.0.0.1:30102/app.js' },
    ],
    pageErrors: [],
    networkFailures: [failedRead],
    httpFailures: [],
  }, {
    origin: 'http://127.0.0.1:30102',
    path: new URL(requestURL).pathname,
    method: 'GET',
  });
  assert.deepEqual(records.map(classifyNetworkRecord), [
    'expected-injected', 'unexpected-error', 'unexpected-error', 'expected-injected',
  ]);
});

test('resource console errors stay unexpected without the corresponding injected request failure', () => {
  const records = diagnosticsToNetwork({
    console: [{ text: 'Failed to load resource: net::ERR_FAILED', location: failedRead.url }],
    pageErrors: [],
    networkFailures: [],
    httpFailures: [],
  }, {
    origin: 'http://127.0.0.1:30102',
    path: new URL(failedRead.url).pathname,
    method: 'GET',
  });
  assert.deepEqual(records.map(classifyNetworkRecord), ['unexpected-error']);
});

test('wrong origin, project, Explorer, or HTTP method remains unexpected', () => {
  const records = diagnosticsToNetwork({
    console: [],
    pageErrors: [],
    networkFailures: [
      { ...failedRead, url: failedRead.url.replace('30102', '30103') },
      { ...failedRead, url: failedRead.url.replace('project-a', 'project-b') },
      { ...failedRead, url: failedRead.url.replace('explorer-a', 'explorer-b') },
      { ...failedRead, url: failedRead.url.replace('30102', '8282') },
      { ...failedRead, method: 'POST' },
    ],
    httpFailures: [],
  }, {
    origin: 'http://127.0.0.1:30102',
    path: '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/builder',
    method: 'GET',
  });
  assert.deepEqual(records.map(classifyNetworkRecord), Array(5).fill('unexpected-error'));
});
