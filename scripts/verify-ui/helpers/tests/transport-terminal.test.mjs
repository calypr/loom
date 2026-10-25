import assert from 'node:assert/strict';
import test from 'node:test';
import { classifyTransportTerminal } from '../transport-terminal.mjs';

test('requestfailed after response headers keeps the observed header but grants no response credit', () => {
  assert.deepEqual(classifyTransportTerminal({ responseHeadersObserved: true, requestFailed: true }), {
    settlement: 'request-failed',
    responseObservedBeforeFailure: true,
    responseStatusCredit: false,
    responseOwnerCredit: false,
  });
});

test('requestfailed before response headers remains an uncredited request failure', () => {
  assert.deepEqual(classifyTransportTerminal({ responseHeadersObserved: false, requestFailed: true }), {
    settlement: 'request-failed',
    responseObservedBeforeFailure: false,
    responseStatusCredit: false,
    responseOwnerCredit: false,
  });
});

test('a response without requestfailed retains response status and owner credit', () => {
  assert.deepEqual(classifyTransportTerminal({ responseHeadersObserved: true, requestFailed: false }), {
    settlement: 'response',
    responseObservedBeforeFailure: false,
    responseStatusCredit: true,
    responseOwnerCredit: true,
  });
});
