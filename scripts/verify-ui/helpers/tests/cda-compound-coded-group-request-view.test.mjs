import assert from 'node:assert/strict';
import test from 'node:test';
import { createCDARequestView } from '../cda-playwright-requests.mjs';

const entry = (index, path, request, response) => ({
  index,
  origin: 'http://same-origin.test',
  path,
  method: 'POST',
  status: 200,
  request,
  response,
});

test('compound coded GROUP request checkpoints use the live scoped cursor and body API', async () => {
  const explorerPath = '/api/v1/projects/owned/explorers/one';
  const observer = {
    requests: [
      entry(0, '/api/v1/projects/unrelated/explorers/elsewhere/commands', { operation: 'unrelated-before' }, {}),
      entry(1, `${explorerPath}/commands`, { operation: 'prior-owned' }, {}),
      entry(2, '/api/v1/projects/unrelated/explorers/elsewhere/preview', { operation: 'unrelated-between' }, {}),
    ],
    byRequest: new WeakMap(),
    waiters: [],
    requestBody: request => request.request,
    responseBody: request => request.response,
    markExpectedValidation() {},
    markExpectedTransportFailure() {},
    waitForResponse(predicate, options) {
      const match = () => this.requests.slice(options.fromIndex).find(predicate);
      const existing = match();
      if (existing) return Promise.resolve(existing);
      return new Promise(resolve => this.waiters.push({ predicate, options, resolve }));
    },
  };
  const capture = createCDARequestView(observer, {
    browserRequestOrigin: 'http://same-origin.test',
    ownedPathPrefix: explorerPath,
  });

  const fromIndex = capture.requests.length;
  assert.equal(fromIndex, 1, 'the cursor counts only owned requests despite same-origin unrelated paths');
  const response = capture.waitForResponse(request => request.path.endsWith('/commands')
    && request.request.operation === 'new-owned', { fromIndex, timeoutMs: 5_000 });
  assert.equal(observer.waiters[0].options.fromIndex, 3,
    'the scoped cursor maps past every request already seen by the shared observer');

  observer.requests.push(entry(3, '/api/v1/projects/unrelated/explorers/elsewhere/commands',
    { operation: 'unrelated-after' }, {}));
  observer.requests.push(entry(4, `${explorerPath}/commands`, { operation: 'new-owned' }, { accepted: true }));
  const waiter = observer.waiters[0];
  const match = observer.requests.slice(waiter.options.fromIndex).find(waiter.predicate);
  assert(match);
  waiter.resolve(match);

  const captured = await response;
  assert.equal(captured.index, 4, 'a later owned request satisfies the translated checkpoint');
  assert.equal(capture.requestBody(captured).operation, 'new-owned');
  assert.deepEqual(capture.responseBody(captured), { accepted: true });
  assert.equal(capture.requests.length, 2, 'the live view contains only the owned request paths');
});
