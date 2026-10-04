import test from 'node:test';
import assert from 'node:assert/strict';
import { diagnosticsToNetwork } from '../playwright-case.mjs';
import { classifyNetworkRecord } from '../report.mjs';

const failedRead = {
  url: 'http://127.0.0.1:8282/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/builder',
  method: 'GET',
  failure: 'net::ERR_FAILED',
};

test('only the first exact owned method and path failure is attributed to injected fault', () => {
  const records = diagnosticsToNetwork({
    console: [],
    pageErrors: [],
    networkFailures: [failedRead, failedRead],
    httpFailures: [],
  }, {
    origin: 'http://127.0.0.1:8282',
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
      { ...failedRead, url: failedRead.url.replace('8282', '8283') },
      { ...failedRead, url: failedRead.url.replace('project-a', 'project-b') },
      { ...failedRead, url: failedRead.url.replace('explorer-a', 'explorer-b') },
      { ...failedRead, method: 'POST' },
    ],
    httpFailures: [],
  }, {
    origin: 'http://127.0.0.1:8282',
    path: '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/builder',
    method: 'GET',
  });
  assert.deepEqual(records.map(classifyNetworkRecord), Array(4).fill('unexpected-error'));
});
