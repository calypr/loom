import test from 'node:test';
import assert from 'node:assert/strict';
import { diagnosticsToNetwork, ownedBrowserRequestTarget } from '../playwright-case.mjs';
import { classifyNetworkRecord } from '../report.mjs';

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
